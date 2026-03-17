package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eformat/openshift-skills-plugin/pkg/agent"
	"github.com/eformat/openshift-skills-plugin/pkg/database"
	"github.com/eformat/openshift-skills-plugin/pkg/kube"
	"github.com/eformat/openshift-skills-plugin/pkg/maas"
	"github.com/gorilla/mux"
	"github.com/robfig/cron/v3"
)

var cronScheduler *cron.Cron
var cronEntries = map[int64]cron.EntryID{}

func InitScheduler() {
	cronScheduler = cron.New()
	cronScheduler.Start()

	db := database.GetDB()
	rows, err := db.Query("SELECT id, schedule FROM scheduled_tasks WHERE enabled = 1")
	if err != nil {
		log.Printf("Failed to load scheduled tasks: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var schedule string
		if err := rows.Scan(&id, &schedule); err == nil {
			addCronJob(id, schedule)
		}
	}
	log.Println("Scheduler initialized")
}

func StopScheduler() {
	if cronScheduler != nil {
		cronScheduler.Stop()
	}
}

func addCronJob(taskID int64, schedule string) {
	entryID, err := cronScheduler.AddFunc(schedule, func() {
		executeScheduledTask(taskID)
	})
	if err != nil {
		log.Printf("Failed to schedule task %d: %v", taskID, err)
		return
	}
	cronEntries[taskID] = entryID
}

func removeCronJob(taskID int64) {
	if entryID, ok := cronEntries[taskID]; ok {
		cronScheduler.Remove(entryID)
		delete(cronEntries, taskID)
	}
}

func loadSessionMessages(db *sql.DB, sessionID string) []maas.ChatMessage {
	rows, err := db.Query("SELECT role, content FROM messages WHERE session_id = ? ORDER BY timestamp", sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var msgs []maas.ChatMessage
	for rows.Next() {
		var m maas.ChatMessage
		if err := rows.Scan(&m.Role, &m.Content); err == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

func executeScheduledTask(taskID int64) {
	db := database.GetDB()
	startTime := time.Now()

	result, err := db.Exec("INSERT INTO task_execution_history (task_id, started_at, status) VALUES (?, ?, 'running')", taskID, startTime)
	if err != nil {
		log.Printf("Failed to create execution history for task %d: %v", taskID, err)
		return
	}
	historyID, _ := result.LastInsertId()

	var task database.ScheduledTask
	var skillID sql.NullInt64
	var sessionID sql.NullString
	err = db.QueryRow(`SELECT id, name, COALESCE(description,''), skill_id, schedule, service_account, namespace,
		provider, model, COALESCE(base_url,''), COALESCE(api_key,''), COALESCE(container_image,''), session_id
		FROM scheduled_tasks WHERE id = ? AND enabled = 1`, taskID).
		Scan(&task.ID, &task.Name, &task.Description, &skillID, &task.Schedule, &task.ServiceAccount,
			&task.Namespace, &task.Provider, &task.Model, &task.BaseURL, &task.APIKey, &task.ContainerImage, &sessionID)
	if err != nil {
		updateHistory(db, historyID, startTime, "failed", "task not found or disabled", "")
		return
	}

	var skillContent, skillName string
	if skillID.Valid {
		db.QueryRow("SELECT name, content FROM skills WHERE id = ?", skillID.Int64).Scan(&skillName, &skillContent)
	}

	// If a container image is set, run in an executor pod
	if task.ContainerImage != "" {
		executeContainerTask(db, historyID, startTime, taskID, &task, sessionID, skillContent, skillName)
		return
	}

	// Otherwise, fall back to LLM-only execution
	executeLLMTask(db, historyID, startTime, taskID, &task, skillID, sessionID, skillContent, skillName)
}

func updateHistory(db *sql.DB, historyID int64, startTime time.Time, status, errorMsg, output string) {
	endTime := time.Now()
	durationMs := endTime.Sub(startTime).Milliseconds()
	db.Exec("UPDATE task_execution_history SET completed_at = ?, duration_ms = ?, status = ?, error_message = ?, output = ? WHERE id = ?",
		endTime, durationMs, status, errorMsg, output, historyID)
}

func executeContainerTask(db *sql.DB, historyID int64, startTime time.Time, taskID int64, task *database.ScheduledTask,
	sessionID sql.NullString, skillContent, skillName string) {

	log.Printf("Executing container task %d (%s) with image %s in %s/%s",
		taskID, task.Name, task.ContainerImage, task.Namespace, task.ServiceAccount)

	// Create or reuse a chat session so results appear in the Chat UI
	sid := ""
	if sessionID.Valid {
		sid = sessionID.String
	}
	if sid == "" {
		sid = "sched-" + strconv.FormatInt(task.ID, 10) + "-" + strconv.FormatInt(time.Now().Unix(), 10)
		db.Exec("INSERT INTO sessions (id, name, provider, model, base_url) VALUES (?, ?, ?, ?, ?)",
			sid, "Scheduled: "+task.Name, task.Provider, task.Model, task.BaseURL)
		db.Exec("UPDATE scheduled_tasks SET session_id = ? WHERE id = ?", sid, taskID)
	}

	// Create an executor pod that the agent can exec commands into
	ep, err := kube.CreateExecutorPod(task.Namespace, task.ServiceAccount, task.ContainerImage, task.Name)
	if err != nil {
		errMsg := "failed to create executor pod: " + err.Error()
		updateHistory(db, historyID, startTime, "failed", errMsg, "")
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, "Error: "+errMsg)
		log.Printf("Container task %d (%s) failed to create executor pod: %v", taskID, task.Name, err)
		return
	}
	defer func() {
		if delErr := kube.DeleteExecutorPod(ep); delErr != nil {
			log.Printf("Warning: failed to delete executor pod for task %d: %v", taskID, delErr)
		}
	}()

	// Look up MaaS credentials
	baseURL := task.BaseURL
	apiKey := task.APIKey
	registryURL := ""
	if apiKey == "" {
		var key, regURL string
		err := db.QueryRow("SELECT COALESCE(api_key,''), url FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&key, &regURL)
		if err == nil {
			apiKey = key
			registryURL = regURL
		}
	} else {
		var regURL string
		err := db.QueryRow("SELECT url FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&regURL)
		if err == nil {
			registryURL = regURL
		}
	}

	if baseURL == "" {
		updateHistory(db, historyID, startTime, "failed", "no model URL configured for this task", "")
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, "Error: no model URL configured for this task")
		return
	}

	// Authenticate to MaaS
	maasClient := maas.NewClient(baseURL, registryURL, apiKey, task.Model)
	if err := maasClient.Authenticate(); err != nil {
		errMsg := "authentication failed: " + err.Error()
		updateHistory(db, historyID, startTime, "failed", errMsg, "")
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, "Error: "+errMsg)
		return
	}

	// Build completions URL
	completionsURL := strings.TrimRight(baseURL, "/")
	if !strings.Contains(completionsURL, "/v1") {
		completionsURL += "/v1/chat/completions"
	} else {
		completionsURL += "/chat/completions"
	}

	// Extract model name from URL
	modelName := task.Model
	trimmedURL := strings.TrimRight(baseURL, "/")
	if idx := strings.LastIndex(trimmedURL, "/"); idx >= 0 {
		modelName = trimmedURL[idx+1:]
	}

	// Build system prompt
	systemPrompt := `You are an AI agent executing a scheduled skill inside a container on an OpenShift cluster.
You have access to the 'shell' tool to execute commands inside the container.
Execute the skill instructions step by step using real commands.
Analyze the actual command output to provide an accurate report.
Do NOT fabricate or hallucinate results - only report what the commands actually return.
When you have completed all the steps, provide a final summary of the results.`
	if skillContent != "" {
		systemPrompt += "\n\n# Skill: " + skillName + "\n" + skillContent
	}

	message := "Execute the scheduled task: " + task.Name
	if task.Description != "" {
		message += "\n\nDescription: " + task.Description
	}

	// Store the user message in chat
	db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'user', ?)", sid, "[Scheduled] "+message)

	// Create a shell executor that runs commands in the executor pod
	shellExec := agent.ShellExecutor(func(command string) string {
		output, err := kube.ExecCommand(ep, command, 60*time.Second)
		if err != nil {
			return "Error: " + err.Error()
		}
		return output
	})

	// Run the agent loop with commands executing in the pod
	log.Printf("Starting agent loop for container task %d (%s) in pod %s/%s", taskID, task.Name, ep.Namespace, ep.Name)
	response, err := agent.RunAgentLoop(completionsURL, maasClient.GetToken(), modelName, systemPrompt, message, 15, shellExec)
	if err != nil {
		updateHistory(db, historyID, startTime, "failed", err.Error(), "")
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, "Error: "+err.Error())
		log.Printf("Container task %d (%s) failed: %v", taskID, task.Name, err)
	} else {
		output := response
		if len(output) > 10000 {
			output = output[:10000]
		}
		updateHistory(db, historyID, startTime, "success", "", output)
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, response)
		log.Printf("Container task %d (%s) completed successfully", taskID, task.Name)
	}

	db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", time.Now(), sid)
	db.Exec("UPDATE scheduled_tasks SET last_run = ?, run_count = run_count + 1, updated_at = ? WHERE id = ?",
		startTime, time.Now(), taskID)
}

func executeLLMTask(db *sql.DB, historyID int64, startTime time.Time, taskID int64, task *database.ScheduledTask,
	_ sql.NullInt64, sessionID sql.NullString, skillContent, skillName string) {

	baseURL := task.BaseURL
	apiKey := task.APIKey
	registryURL := ""
	if apiKey == "" {
		var key, regURL string
		err := db.QueryRow("SELECT COALESCE(api_key,''), url FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&key, &regURL)
		if err == nil {
			apiKey = key
			registryURL = regURL
		}
	} else {
		var regURL string
		err := db.QueryRow("SELECT url FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&regURL)
		if err == nil {
			registryURL = regURL
		}
	}

	if baseURL == "" {
		updateHistory(db, historyID, startTime, "failed", "no model URL configured for this task", "")
		return
	}

	sid := ""
	if sessionID.Valid {
		sid = sessionID.String
	}
	if sid == "" {
		sid = "sched-" + strconv.FormatInt(task.ID, 10) + "-" + strconv.FormatInt(time.Now().Unix(), 10)
		db.Exec("INSERT INTO sessions (id, name, provider, model, base_url) VALUES (?, ?, ?, ?, ?)",
			sid, "Scheduled: "+task.Name, task.Provider, task.Model, task.BaseURL)
		db.Exec("UPDATE scheduled_tasks SET session_id = ? WHERE id = ?", sid, taskID)
	}

	// Build system prompt with skill content and agent instructions
	systemPrompt := `You are an AI agent executing a scheduled skill on an OpenShift cluster.
You have access to the 'shell' tool to execute commands.
Use 'oc' and 'kubectl' commands to interact with the cluster.
Execute the skill instructions step by step using real commands.
Analyze the actual command output to provide an accurate report.
Do NOT fabricate or hallucinate results - only report what the commands actually return.
When you have completed all the steps, provide a final summary of the results.`
	if skillContent != "" {
		systemPrompt += "\n\n# Skill: " + skillName + "\n" + skillContent
	}

	message := "Execute the scheduled task: " + task.Name
	if task.Description != "" {
		message += "\n\nDescription: " + task.Description
	}

	db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'user', ?)", sid, "[Scheduled] "+message)

	// Authenticate to get a session token for the MaaS API
	maasClient := maas.NewClient(baseURL, registryURL, apiKey, task.Model)
	if err := maasClient.Authenticate(); err != nil {
		updateHistory(db, historyID, startTime, "failed", "authentication failed: "+err.Error(), "")
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, "Error: authentication failed: "+err.Error())
		return
	}

	// Build completions URL from the model-specific base URL
	completionsURL := strings.TrimRight(baseURL, "/")
	if !strings.Contains(completionsURL, "/v1") {
		completionsURL += "/v1/chat/completions"
	} else {
		completionsURL += "/chat/completions"
	}

	// Extract model name from URL (last path segment)
	modelName := task.Model
	trimmedURL := strings.TrimRight(baseURL, "/")
	if idx := strings.LastIndex(trimmedURL, "/"); idx >= 0 {
		modelName = trimmedURL[idx+1:]
	}

	// Run the agent loop
	log.Printf("Starting agent loop for task %d (%s) with model %s", taskID, task.Name, modelName)
	response, err := agent.RunAgentLoop(completionsURL, maasClient.GetToken(), modelName, systemPrompt, message, 15, nil)
	if err != nil {
		updateHistory(db, historyID, startTime, "failed", err.Error(), "")
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, "Error: "+err.Error())
		return
	}

	db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sid, response)
	db.Exec("UPDATE scheduled_tasks SET last_run = ?, run_count = run_count + 1, updated_at = ? WHERE id = ?", startTime, time.Now(), taskID)

	output := response
	if len(output) > 10000 {
		output = output[:10000]
	}
	updateHistory(db, historyID, startTime, "success", "", output)
	log.Printf("Agent task %d (%s) completed", taskID, task.Name)
}

type createScheduledTaskRequest struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	SkillID        *int64 `json:"skill_id"`
	Schedule       string `json:"schedule"`
	ServiceAccount string `json:"service_account"`
	Namespace      string `json:"namespace"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ContainerImage string `json:"container_image"`
}

func ListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	rows, err := db.Query(`SELECT id, name, COALESCE(description,''), skill_id, schedule, service_account, namespace,
		enabled, last_run, next_run, run_count, session_id, provider, model, COALESCE(base_url,''),
		COALESCE(container_image,''), created_at, updated_at FROM scheduled_tasks ORDER BY name`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	tasks := []database.ScheduledTask{}
	for rows.Next() {
		var t database.ScheduledTask
		var skillID sql.NullInt64
		var lastRun, nextRun sql.NullTime
		var sessionID sql.NullString
		var enabled int
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &skillID, &t.Schedule, &t.ServiceAccount,
			&t.Namespace, &enabled, &lastRun, &nextRun, &t.RunCount, &sessionID,
			&t.Provider, &t.Model, &t.BaseURL, &t.ContainerImage, &t.CreatedAt, &t.UpdatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		t.Enabled = enabled == 1
		if skillID.Valid {
			t.SkillID = &skillID.Int64
		}
		if lastRun.Valid {
			t.LastRun = &lastRun.Time
		}
		if nextRun.Valid {
			t.NextRun = &nextRun.Time
		}
		if sessionID.Valid {
			t.SessionID = &sessionID.String
		}
		tasks = append(tasks, t)
	}
	jsonResponse(w, tasks)
}

func CreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	var req createScheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Schedule == "" {
		httpError(w, http.StatusBadRequest, "name and schedule are required")
		return
	}
	if req.ServiceAccount == "" {
		req.ServiceAccount = "default"
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Provider == "" {
		req.Provider = "openai-compatible"
	}

	// Validate cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(req.Schedule); err != nil {
		httpError(w, http.StatusBadRequest, "invalid cron schedule: "+err.Error())
		return
	}

	db := database.GetDB()
	result, err := db.Exec(`INSERT INTO scheduled_tasks (name, description, skill_id, schedule, service_account, namespace, provider, model, base_url, api_key, container_image)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.Name, req.Description, req.SkillID, req.Schedule, req.ServiceAccount, req.Namespace,
		req.Provider, req.Model, req.BaseURL, req.APIKey, req.ContainerImage)
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	id, _ := result.LastInsertId()
	addCronJob(id, req.Schedule)
	jsonResponse(w, map[string]interface{}{"id": id, "message": "scheduled task created"})
}

func UpdateScheduledTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	var req createScheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	db := database.GetDB()
	now := time.Now()

	if req.Schedule != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(req.Schedule); err != nil {
			httpError(w, http.StatusBadRequest, "invalid cron schedule: "+err.Error())
			return
		}
	}

	db.Exec(`UPDATE scheduled_tasks SET name=?, description=?, skill_id=?, schedule=?, service_account=?,
		namespace=?, provider=?, model=?, base_url=?, api_key=?, container_image=?, updated_at=? WHERE id=?`,
		req.Name, req.Description, req.SkillID, req.Schedule, req.ServiceAccount, req.Namespace,
		req.Provider, req.Model, req.BaseURL, req.APIKey, req.ContainerImage, now, id)

	// Reschedule
	removeCronJob(id)
	if req.Schedule != "" {
		addCronJob(id, req.Schedule)
	}

	jsonResponse(w, map[string]string{"message": "scheduled task updated"})
}

func DeleteScheduledTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	removeCronJob(id)
	db := database.GetDB()
	db.Exec("DELETE FROM task_execution_history WHERE task_id = ?", id)
	db.Exec("DELETE FROM scheduled_tasks WHERE id = ?", id)
	jsonResponse(w, map[string]string{"message": "scheduled task deleted"})
}

type toggleRequest struct {
	Enabled bool `json:"enabled"`
}

func ToggleScheduledTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	db := database.GetDB()
	enabled := 0
	if req.Enabled {
		enabled = 1
	}
	db.Exec("UPDATE scheduled_tasks SET enabled = ?, updated_at = ? WHERE id = ?", enabled, time.Now(), id)

	if req.Enabled {
		var schedule string
		db.QueryRow("SELECT schedule FROM scheduled_tasks WHERE id = ?", id).Scan(&schedule)
		addCronJob(id, schedule)
	} else {
		removeCronJob(id)
	}

	jsonResponse(w, map[string]string{"message": "task toggled"})
}

func GetTaskHistory(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	db := database.GetDB()
	rows, err := db.Query(`SELECT id, task_id, started_at, completed_at, duration_ms, status, COALESCE(error_message,''), COALESCE(output,'')
		FROM task_execution_history WHERE task_id = ? ORDER BY started_at DESC LIMIT 50`, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	history := []database.TaskExecutionHistory{}
	for rows.Next() {
		var h database.TaskExecutionHistory
		var completedAt sql.NullTime
		var durationMs sql.NullInt64
		if err := rows.Scan(&h.ID, &h.TaskID, &h.StartedAt, &completedAt, &durationMs, &h.Status, &h.ErrorMessage, &h.Output); err != nil {
			continue
		}
		if completedAt.Valid {
			h.CompletedAt = &completedAt.Time
		}
		if durationMs.Valid {
			h.DurationMs = &durationMs.Int64
		}
		history = append(history, h)
	}
	jsonResponse(w, history)
}

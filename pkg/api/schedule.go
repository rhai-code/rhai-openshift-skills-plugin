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
var runOnceTimers = map[int64]*time.Timer{}

func InitScheduler() {
	cronScheduler = cron.New()
	cronScheduler.Start()

	db := database.GetDB()
	rows, err := db.Query("SELECT id, schedule, COALESCE(run_once, 0), COALESCE(run_once_delay, '') FROM scheduled_tasks WHERE enabled = 1")
	if err != nil {
		log.Printf("Failed to load scheduled tasks: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var schedule string
		var runOnce int
		var runOnceDelay string
		if err := rows.Scan(&id, &schedule, &runOnce, &runOnceDelay); err == nil {
			if runOnce == 1 {
				scheduleRunOnce(id, runOnceDelay)
			} else {
				addCronJob(id, schedule)
			}
		}
	}
	log.Println("Scheduler initialized")
}

func StopScheduler() {
	if cronScheduler != nil {
		cronScheduler.Stop()
	}
}

// ReloadScheduler stops all existing cron jobs and run-once timers, then reloads from the database.
func ReloadScheduler() {
	// Remove all existing cron entries
	for taskID := range cronEntries {
		cronScheduler.Remove(cronEntries[taskID])
		delete(cronEntries, taskID)
	}
	// Cancel all run-once timers
	for taskID, timer := range runOnceTimers {
		timer.Stop()
		delete(runOnceTimers, taskID)
	}

	// Reload from database
	db := database.GetDB()
	rows, err := db.Query("SELECT id, schedule, COALESCE(run_once, 0), COALESCE(run_once_delay, '') FROM scheduled_tasks WHERE enabled = 1")
	if err != nil {
		log.Printf("Failed to reload scheduled tasks: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var schedule string
		var runOnce int
		var runOnceDelay string
		if err := rows.Scan(&id, &schedule, &runOnce, &runOnceDelay); err == nil {
			if runOnce == 1 {
				scheduleRunOnce(id, runOnceDelay)
			} else {
				addCronJob(id, schedule)
			}
		}
	}
	log.Println("Scheduler reloaded")
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

func removeRunOnce(taskID int64) {
	if timer, ok := runOnceTimers[taskID]; ok {
		timer.Stop()
		delete(runOnceTimers, taskID)
	}
}

// scheduleRunOnce schedules a one-off task execution.
// delay can be "now", "+30s", "+5m", "+2h", "+1h30m", etc.
func scheduleRunOnce(taskID int64, delay string) {
	d := parseDelay(delay)
	log.Printf("Scheduling run-once task %d with delay %v", taskID, d)
	timer := time.AfterFunc(d, func() {
		executeScheduledTask(taskID)
		// Auto-disable after execution
		db := database.GetDB()
		db.Exec("UPDATE scheduled_tasks SET enabled = 0, updated_at = ? WHERE id = ?", time.Now(), taskID)
		delete(runOnceTimers, taskID)
		log.Printf("Run-once task %d completed and auto-disabled", taskID)
	})
	runOnceTimers[taskID] = timer
}

// parseDelay parses delay notation: "now", "+30s", "+5m", "+2h", "+1h30m"
func parseDelay(delay string) time.Duration {
	delay = strings.TrimSpace(strings.ToLower(delay))
	if delay == "" || delay == "now" {
		return 0
	}
	s := strings.TrimPrefix(delay, "+")
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Printf("Invalid delay %q, running immediately: %v", delay, err)
		return 0
	}
	return d
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

	// Check if the task exists and is enabled before creating any history
	var task database.ScheduledTask
	var skillID sql.NullInt64
	var sessionID sql.NullString
	err := db.QueryRow(`SELECT id, name, COALESCE(description,''), skill_id, schedule, service_account, namespace,
		provider, model, COALESCE(base_url,''), COALESCE(api_key,''), COALESCE(container_image,''),
		COALESCE(temperature, 0.7), COALESCE(max_tokens, 0), session_id
		FROM scheduled_tasks WHERE id = ? AND enabled = 1`, taskID).
		Scan(&task.ID, &task.Name, &task.Description, &skillID, &task.Schedule, &task.ServiceAccount,
			&task.Namespace, &task.Provider, &task.Model, &task.BaseURL, &task.APIKey, &task.ContainerImage,
			&task.Temperature, &task.MaxTokens, &sessionID)
	if err != nil {
		// Task not found or disabled — skip silently
		log.Printf("Skipping task %d: not found or disabled", taskID)
		return
	}

	startTime := time.Now()
	result, err := db.Exec("INSERT INTO task_execution_history (task_id, started_at, status) VALUES (?, ?, 'running')", taskID, startTime)
	if err != nil {
		log.Printf("Failed to create execution history for task %d: %v", taskID, err)
		return
	}
	historyID, _ := result.LastInsertId()

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
	modelName := maas.ExtractModelName(baseURL, task.Model)

	// Build system prompt
	systemPrompt := `You are an AI agent executing a scheduled skill inside a container on an OpenShift cluster.
You have access to the 'shell' tool to execute commands inside the container.
Execute the skill instructions step by step using real commands.
Analyze the actual command output to provide an accurate report.
Do NOT fabricate or hallucinate results - only report what the commands actually return.
IMPORTANT: For multi-line scripts or commands containing quotes, write the script to a temp file using a heredoc (cat > /tmp/script.sh << 'SCRIPT' ... SCRIPT) then run sh /tmp/script.sh.
When you have completed all the steps, provide a final summary of the results.`
	if globalPrompt := GetSystemPrompt(); globalPrompt != "" {
		systemPrompt += "\n\n" + globalPrompt
	}
	if skillContent != "" {
		systemPrompt += "\n\n# Skill: " + skillName + "\n" + skillContent
	}

	message := "Execute the scheduled task: " + task.Name
	if task.Description != "" {
		message += "\n\nPrompt: " + task.Description
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
	response, err := agent.RunAgentLoop(completionsURL, maasClient.GetToken(), modelName, systemPrompt, message, 15, shellExec, &agent.AgentOptions{Temperature: task.Temperature, MaxTokens: task.MaxTokens})
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
IMPORTANT: For multi-line scripts or commands containing quotes, write the script to a temp file using a heredoc (cat > /tmp/script.sh << 'SCRIPT' ... SCRIPT) then run sh /tmp/script.sh.
When you have completed all the steps, provide a final summary of the results.`
	if globalPrompt := GetSystemPrompt(); globalPrompt != "" {
		systemPrompt += "\n\n" + globalPrompt
	}
	if skillContent != "" {
		systemPrompt += "\n\n# Skill: " + skillName + "\n" + skillContent
	}

	message := "Execute the scheduled task: " + task.Name
	if task.Description != "" {
		message += "\n\nPrompt: " + task.Description
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
	modelName := maas.ExtractModelName(baseURL, task.Model)

	// Run the agent loop
	log.Printf("Starting agent loop for task %d (%s) with model %s", taskID, task.Name, modelName)
	response, err := agent.RunAgentLoop(completionsURL, maasClient.GetToken(), modelName, systemPrompt, message, 15, nil, &agent.AgentOptions{Temperature: task.Temperature, MaxTokens: task.MaxTokens})
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
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	SkillID        *int64  `json:"skill_id"`
	Schedule       string  `json:"schedule"`
	ServiceAccount string  `json:"service_account"`
	Namespace      string  `json:"namespace"`
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	BaseURL        string  `json:"base_url"`
	APIKey         string  `json:"api_key"`
	ContainerImage string  `json:"container_image"`
	Temperature    float64 `json:"temperature"`
	MaxTokens      int     `json:"max_tokens"`
	RunOnce        bool    `json:"run_once"`
	RunOnceDelay   string  `json:"run_once_delay"`
}

func ListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	rows, err := db.Query(`SELECT id, name, COALESCE(description,''), skill_id, schedule, service_account, namespace,
		enabled, last_run, next_run, run_count, session_id, provider, model, COALESCE(base_url,''),
		COALESCE(container_image,''), COALESCE(temperature, 0.7), COALESCE(max_tokens, 0),
		COALESCE(run_once, 0), COALESCE(run_once_delay, ''),
		created_at, updated_at FROM scheduled_tasks ORDER BY name`)
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
		var enabled, runOnce int
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &skillID, &t.Schedule, &t.ServiceAccount,
			&t.Namespace, &enabled, &lastRun, &nextRun, &t.RunCount, &sessionID,
			&t.Provider, &t.Model, &t.BaseURL, &t.ContainerImage, &t.Temperature, &t.MaxTokens,
			&runOnce, &t.RunOnceDelay,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		t.Enabled = enabled == 1
		t.RunOnce = runOnce == 1
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
	if req.Name == "" {
		httpError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !req.RunOnce && req.Schedule == "" {
		httpError(w, http.StatusBadRequest, "schedule is required for recurring tasks")
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

	// Validate cron expression for recurring tasks
	if !req.RunOnce {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(req.Schedule); err != nil {
			httpError(w, http.StatusBadRequest, "invalid cron schedule: "+err.Error())
			return
		}
	}

	db := database.GetDB()
	if req.Temperature <= 0 {
		req.Temperature = 0.7
	}

	runOnce := 0
	if req.RunOnce {
		runOnce = 1
		if req.Schedule == "" {
			req.Schedule = "0 0 * * *" // placeholder for run-once tasks
		}
	}
	result, err := db.Exec(`INSERT INTO scheduled_tasks (name, description, skill_id, schedule, service_account, namespace, provider, model, base_url, api_key, container_image, temperature, max_tokens, run_once, run_once_delay)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.Name, req.Description, req.SkillID, req.Schedule, req.ServiceAccount, req.Namespace,
		req.Provider, req.Model, req.BaseURL, req.APIKey, req.ContainerImage, req.Temperature, req.MaxTokens, runOnce, req.RunOnceDelay)
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	id, _ := result.LastInsertId()
	if req.RunOnce {
		scheduleRunOnce(id, req.RunOnceDelay)
	} else {
		addCronJob(id, req.Schedule)
	}
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

	// Validate cron expression for recurring tasks
	if !req.RunOnce && req.Schedule != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(req.Schedule); err != nil {
			httpError(w, http.StatusBadRequest, "invalid cron schedule: "+err.Error())
			return
		}
	}

	if req.Temperature <= 0 {
		req.Temperature = 0.7
	}

	runOnce := 0
	if req.RunOnce {
		runOnce = 1
		if req.Schedule == "" {
			req.Schedule = "0 0 * * *" // placeholder for run-once tasks
		}
	}
	db.Exec(`UPDATE scheduled_tasks SET name=?, description=?, skill_id=?, schedule=?, service_account=?,
		namespace=?, provider=?, model=?, base_url=?, api_key=?, container_image=?, temperature=?, max_tokens=?,
		run_once=?, run_once_delay=?, updated_at=? WHERE id=?`,
		req.Name, req.Description, req.SkillID, req.Schedule, req.ServiceAccount, req.Namespace,
		req.Provider, req.Model, req.BaseURL, req.APIKey, req.ContainerImage, req.Temperature, req.MaxTokens,
		runOnce, req.RunOnceDelay, now, id)

	// Reschedule — remove both cron and run-once timers
	removeCronJob(id)
	removeRunOnce(id)
	if req.RunOnce {
		scheduleRunOnce(id, req.RunOnceDelay)
	} else if req.Schedule != "" {
		addCronJob(id, req.Schedule)
	}

	jsonResponse(w, map[string]string{"message": "scheduled task updated"})
}

func DeleteScheduledTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	removeCronJob(id)
	removeRunOnce(id)
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
		var schedule, runOnceDelay string
		var runOnce int
		db.QueryRow("SELECT schedule, COALESCE(run_once, 0), COALESCE(run_once_delay, '') FROM scheduled_tasks WHERE id = ?", id).Scan(&schedule, &runOnce, &runOnceDelay)
		if runOnce == 1 {
			scheduleRunOnce(id, runOnceDelay)
		} else {
			addCronJob(id, schedule)
		}
	} else {
		removeCronJob(id)
		removeRunOnce(id)
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

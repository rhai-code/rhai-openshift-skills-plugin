package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/eformat/openshift-skills-plugin/pkg/agent"
	"github.com/eformat/openshift-skills-plugin/pkg/database"
	"github.com/eformat/openshift-skills-plugin/pkg/maas"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type chatMessageRequest struct {
	Message string `json:"message"`
}

func SendMessage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	sessionID := mux.Vars(r)["id"]
	var req chatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Message == "" {
		httpError(w, http.StatusBadRequest, "message is required")
		return
	}

	db := database.GetDB()

	// Get session
	var sess database.Session
	err := db.QueryRow("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), temperature, max_tokens, COALESCE(owner,'') FROM sessions WHERE id = ?", sessionID).
		Scan(&sess.ID, &sess.Name, &sess.Provider, &sess.Model, &sess.BaseURL, &sess.SystemPrompt, &sess.Temperature, &sess.MaxTokens, &sess.Owner)
	if err == sql.ErrNoRows {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !authorizeResource(w, user, sess.Owner) {
		return
	}

	// Store user message
	db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'user', ?)", sessionID, req.Message)

	// Load conversation history
	rows, err := db.Query("SELECT role, content FROM messages WHERE session_id = ? ORDER BY timestamp", sessionID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var chatMessages []maas.ChatMessage
	for rows.Next() {
		var m maas.ChatMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			continue
		}
		chatMessages = append(chatMessages, m)
	}

	// Build system prompt with session-specific skills (or all enabled skills if none selected)
	systemPrompt := sess.SystemPrompt
	var skillCount int
	db.QueryRow("SELECT COUNT(*) FROM session_skills WHERE session_id = ?", sessionID).Scan(&skillCount)

	var skillQuery string
	var skillArgs []interface{}
	if skillCount > 0 {
		skillQuery = "SELECT s.name, s.content FROM skills s JOIN session_skills ss ON s.id = ss.skill_id WHERE ss.session_id = ? AND s.enabled = 1"
		skillArgs = []interface{}{sessionID}
	} else {
		skillQuery = "SELECT name, content FROM skills WHERE enabled = 1"
	}
	skillRows, err := db.Query(skillQuery, skillArgs...)
	if err == nil {
		defer skillRows.Close()
		for skillRows.Next() {
			var name, content string
			if err := skillRows.Scan(&name, &content); err == nil {
				systemPrompt += "\n\n# Skill: " + name + "\n" + content
			}
		}
	}

	// The session base_url is the model-specific inference URL.
	// Look up the API key and registry URL from the MaaS endpoint (scoped to visible endpoints).
	baseURL := sess.BaseURL
	apiKey := ""
	registryURL := ""
	var key, regURL string
	if user.IsAdmin {
		err = db.QueryRow("SELECT COALESCE(api_key,''), url FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&key, &regURL)
	} else {
		err = db.QueryRow("SELECT COALESCE(api_key,''), url FROM maas_endpoints WHERE enabled = 1 AND (is_global = 1 OR owner = ?) ORDER BY id LIMIT 1", user.Username).Scan(&key, &regURL)
	}
	if err == nil {
		apiKey = key
		registryURL = regURL
	}

	if baseURL == "" {
		httpError(w, http.StatusBadRequest, "no model URL configured for this session. Create a new chat and select a model.")
		return
	}

	// Authenticate to get a session token
	maasClient := maas.NewClient(baseURL, registryURL, apiKey, sess.Model)
	if err := maasClient.Authenticate(); err != nil {
		log.Printf("MaaS auth error: %v", err)
		httpError(w, http.StatusBadGateway, "authentication failed: "+err.Error())
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
	modelName := maas.ExtractModelName(baseURL, sess.Model)

	// Use agent loop with shell tool access
	agentSystemPrompt := `You are an AI agent running on an OpenShift cluster.
You have access to the 'shell' tool to execute commands.
Use 'oc' and 'kubectl' commands to interact with the cluster.
Execute commands to get real data - do NOT fabricate or hallucinate results.
Only report what the commands actually return.
IMPORTANT: For multi-line scripts or commands containing quotes, write the script to a temp file first using a heredoc, then execute it. Example: cat > /tmp/script.sh << 'SCRIPT'
#!/bin/sh
for pod in $(oc get pods -o name); do echo "$pod"; done
SCRIPT
sh /tmp/script.sh`
	if systemPrompt != "" {
		agentSystemPrompt += "\n\n" + systemPrompt
	}

	// Convert conversation history (excluding the current user message, which is the last one)
	// so the agent has multi-turn context within this session.
	var history []agent.ChatMessage
	if len(chatMessages) > 1 {
		for _, m := range chatMessages[:len(chatMessages)-1] {
			history = append(history, agent.ChatMessage{Role: m.Role, Content: m.Content})
		}
	}

	result, err := agent.RunAgentLoop(context.Background(), completionsURL, maasClient.GetToken(), modelName, agentSystemPrompt, req.Message, 15, nil, &agent.AgentOptions{
		Temperature:    sess.Temperature,
		MaxTokens:      sess.MaxTokens,
		History:        history,
		Source:         "chat",
		ExperimentName: sess.Name,
	})
	if err != nil {
		log.Printf("Agent error: %v", err)
		httpError(w, http.StatusBadGateway, "agent execution failed: "+err.Error())
		return
	}

	// Store assistant message
	db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sessionID, result.Response)
	db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", time.Now(), sessionID)

	jsonResponse(w, map[string]string{"response": result.Response})
}

func WebSocketChat(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["id"]
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	db := database.GetDB()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var req chatMessageRequest
		if err := json.Unmarshal(msgBytes, &req); err != nil {
			conn.WriteJSON(map[string]string{"error": "invalid JSON"})
			continue
		}

		// Get session
		var sess database.Session
		err = db.QueryRow("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), temperature, max_tokens FROM sessions WHERE id = ?", sessionID).
			Scan(&sess.ID, &sess.Name, &sess.Provider, &sess.Model, &sess.BaseURL, &sess.SystemPrompt, &sess.Temperature, &sess.MaxTokens)
		if err != nil {
			conn.WriteJSON(map[string]string{"error": "session not found"})
			continue
		}

		// Store user message
		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'user', ?)", sessionID, req.Message)

		// Send typing indicator
		conn.WriteJSON(map[string]string{"type": "typing"})

		// Load history
		rows, err := db.Query("SELECT role, content FROM messages WHERE session_id = ? ORDER BY timestamp", sessionID)
		if err != nil {
			conn.WriteJSON(map[string]string{"error": err.Error()})
			continue
		}

		var chatMessages []maas.ChatMessage
		for rows.Next() {
			var m maas.ChatMessage
			if err := rows.Scan(&m.Role, &m.Content); err == nil {
				chatMessages = append(chatMessages, m)
			}
		}
		rows.Close()

		// Build system prompt with session-specific skills
		systemPrompt := sess.SystemPrompt
		var wsSkillCount int
		db.QueryRow("SELECT COUNT(*) FROM session_skills WHERE session_id = ?", sessionID).Scan(&wsSkillCount)

		var wsSkillQuery string
		var wsSkillArgs []interface{}
		if wsSkillCount > 0 {
			wsSkillQuery = "SELECT s.name, s.content FROM skills s JOIN session_skills ss ON s.id = ss.skill_id WHERE ss.session_id = ? AND s.enabled = 1"
			wsSkillArgs = []interface{}{sessionID}
		} else {
			wsSkillQuery = "SELECT name, content FROM skills WHERE enabled = 1"
		}
		skillRows, err := db.Query(wsSkillQuery, wsSkillArgs...)
		if err == nil {
			for skillRows.Next() {
				var name, content string
				if err := skillRows.Scan(&name, &content); err == nil {
					systemPrompt += "\n\n# Skill: " + name + "\n" + content
				}
			}
			skillRows.Close()
		}

		baseURL := sess.BaseURL
		apiKey := ""
		registryURL := ""
		var wsKey, wsRegURL string
		err = db.QueryRow("SELECT COALESCE(api_key,''), url FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&wsKey, &wsRegURL)
		if err == nil {
			apiKey = wsKey
			registryURL = wsRegURL
		}

		if baseURL == "" {
			conn.WriteJSON(map[string]string{"type": "error", "content": "no model URL configured for this session"})
			continue
		}

		client := maas.NewClient(baseURL, registryURL, apiKey, sess.Model)
		response, err := client.Complete(chatMessages, systemPrompt)
		if err != nil {
			conn.WriteJSON(map[string]string{"type": "error", "content": err.Error()})
			continue
		}

		db.Exec("INSERT INTO messages (session_id, role, content) VALUES (?, 'assistant', ?)", sessionID, response)
		db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", time.Now(), sessionID)

		conn.WriteJSON(map[string]interface{}{
			"type":    "message",
			"role":    "assistant",
			"content": response,
		})
	}
}

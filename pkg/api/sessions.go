package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/eformat/openshift-skills-plugin/pkg/database"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type createSessionRequest struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	BaseURL      string `json:"base_url,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

func CreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Provider == "" {
		req.Provider = "openai-compatible"
	}

	id := uuid.New().String()
	name := "Chat " + id[:8]
	db := database.GetDB()
	_, err := db.Exec("INSERT INTO sessions (id, name, provider, model, base_url, system_prompt) VALUES (?, ?, ?, ?, ?, ?)",
		id, name, req.Provider, req.Model, req.BaseURL, req.SystemPrompt)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, map[string]string{"id": id, "name": name})
}

func ListSessions(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	rows, err := db.Query("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), created_at, updated_at FROM sessions ORDER BY updated_at DESC")
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	sessions := []database.Session{}
	for rows.Next() {
		var s database.Session
		if err := rows.Scan(&s.ID, &s.Name, &s.Provider, &s.Model, &s.BaseURL, &s.SystemPrompt, &s.CreatedAt, &s.UpdatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sessions = append(sessions, s)
	}
	jsonResponse(w, sessions)
}

func GetSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	db := database.GetDB()

	var s database.Session
	err := db.QueryRow("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), created_at, updated_at FROM sessions WHERE id = ?", id).
		Scan(&s.ID, &s.Name, &s.Provider, &s.Model, &s.BaseURL, &s.SystemPrompt, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Fetch messages
	msgRows, err := db.Query("SELECT id, session_id, role, content, timestamp FROM messages WHERE session_id = ? ORDER BY timestamp", id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer msgRows.Close()

	messages := []database.Message{}
	for msgRows.Next() {
		var m database.Message
		if err := msgRows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Timestamp); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		messages = append(messages, m)
	}

	jsonResponse(w, map[string]interface{}{
		"session":  s,
		"messages": messages,
	})
}

func DeleteSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	db := database.GetDB()
	db.Exec("DELETE FROM messages WHERE session_id = ?", id)
	db.Exec("DELETE FROM sessions WHERE id = ?", id)
	jsonResponse(w, map[string]string{"message": "session deleted"})
}

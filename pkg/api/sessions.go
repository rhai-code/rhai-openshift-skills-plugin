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
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	BaseURL      string  `json:"base_url,omitempty"`
	SystemPrompt string  `json:"system_prompt,omitempty"`
	SkillIDs     []int64 `json:"skill_ids,omitempty"`
	Temperature  float64 `json:"temperature"`
	MaxTokens    int     `json:"max_tokens"`
}

type updateSessionSkillsRequest struct {
	SkillIDs []int64 `json:"skill_ids"`
}

func CreateSession(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
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
	if req.Temperature <= 0 {
		req.Temperature = 0.2
	}
	// Use global system prompt as default if not explicitly set
	if req.SystemPrompt == "" {
		req.SystemPrompt = GetSystemPrompt()
	}
	db := database.GetDB()
	_, err := db.Exec("INSERT INTO sessions (id, name, provider, model, base_url, system_prompt, temperature, max_tokens, owner) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, name, req.Provider, req.Model, req.BaseURL, req.SystemPrompt, req.Temperature, req.MaxTokens, user.Username)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Associate selected skills
	for _, skillID := range req.SkillIDs {
		db.Exec("INSERT INTO session_skills (session_id, skill_id) VALUES (?, ?)", id, skillID)
	}

	jsonResponse(w, map[string]string{"id": id, "name": name})
}

func ListSessions(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	db := database.GetDB()

	var rows *sql.Rows
	var err error
	if user.IsAdmin {
		rows, err = db.Query("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), temperature, max_tokens, COALESCE(owner,''), created_at, updated_at FROM sessions ORDER BY updated_at DESC")
	} else {
		rows, err = db.Query("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), temperature, max_tokens, COALESCE(owner,''), created_at, updated_at FROM sessions WHERE owner = ? ORDER BY updated_at DESC", user.Username)
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	sessions := []database.Session{}
	for rows.Next() {
		var s database.Session
		if err := rows.Scan(&s.ID, &s.Name, &s.Provider, &s.Model, &s.BaseURL, &s.SystemPrompt, &s.Temperature, &s.MaxTokens, &s.Owner, &s.CreatedAt, &s.UpdatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sessions = append(sessions, s)
	}
	jsonResponse(w, sessions)
}

func GetSession(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	id := mux.Vars(r)["id"]
	db := database.GetDB()

	var s database.Session
	err := db.QueryRow("SELECT id, name, provider, model, COALESCE(base_url,''), COALESCE(system_prompt,''), temperature, max_tokens, COALESCE(owner,''), created_at, updated_at FROM sessions WHERE id = ?", id).
		Scan(&s.ID, &s.Name, &s.Provider, &s.Model, &s.BaseURL, &s.SystemPrompt, &s.Temperature, &s.MaxTokens, &s.Owner, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !authorizeResource(w, user, s.Owner) {
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

	// Fetch associated skill IDs
	skillIDs := []int64{}
	skillRows, err := db.Query("SELECT skill_id FROM session_skills WHERE session_id = ?", id)
	if err == nil {
		defer skillRows.Close()
		for skillRows.Next() {
			var sid int64
			if err := skillRows.Scan(&sid); err == nil {
				skillIDs = append(skillIDs, sid)
			}
		}
	}

	jsonResponse(w, map[string]interface{}{
		"session":   s,
		"messages":  messages,
		"skill_ids": skillIDs,
	})
}

func UpdateSessionSkills(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	id := mux.Vars(r)["id"]
	var req updateSessionSkillsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	db := database.GetDB()

	// Verify session exists and check ownership
	var owner string
	err := db.QueryRow("SELECT COALESCE(owner,'') FROM sessions WHERE id = ?", id).Scan(&owner)
	if err != nil {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if !authorizeResource(w, user, owner) {
		return
	}

	// Replace all skill associations
	db.Exec("DELETE FROM session_skills WHERE session_id = ?", id)
	for _, skillID := range req.SkillIDs {
		db.Exec("INSERT INTO session_skills (session_id, skill_id) VALUES (?, ?)", id, skillID)
	}

	jsonResponse(w, map[string]string{"message": "skills updated"})
}

func DeleteSession(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	id := mux.Vars(r)["id"]
	db := database.GetDB()

	var owner string
	err := db.QueryRow("SELECT COALESCE(owner,'') FROM sessions WHERE id = ?", id).Scan(&owner)
	if err != nil {
		httpError(w, http.StatusNotFound, "session not found")
		return
	}
	if !authorizeResource(w, user, owner) {
		return
	}

	db.Exec("DELETE FROM session_skills WHERE session_id = ?", id)
	db.Exec("DELETE FROM messages WHERE session_id = ?", id)
	db.Exec("DELETE FROM sessions WHERE id = ?", id)
	jsonResponse(w, map[string]string{"message": "session deleted"})
}

package api

import (
	"encoding/json"
	"net/http"

	"github.com/eformat/openshift-skills-plugin/pkg/database"
)

// DefaultSystemPrompt is the default global system prompt used when no custom value is configured.
const DefaultSystemPrompt = `You are a helpful AI assistant for OpenShift cluster administration.
Format responses using markdown for readability. Use tables for structured data.
Be concise and accurate in your analysis.
When reporting on cluster resources, include relevant details like status, age, and namespace.`

type configRequest struct {
	Value string `json:"value"`
}

func GetConfig(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "key is required")
		return
	}
	db := database.GetDB()
	var value string
	err := db.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&value)
	if err != nil {
		// Return default for system_prompt if not configured
		if key == "system_prompt" {
			jsonResponse(w, map[string]string{"key": key, "value": DefaultSystemPrompt})
			return
		}
		jsonResponse(w, map[string]string{"key": key, "value": ""})
		return
	}
	jsonResponse(w, map[string]string{"key": key, "value": value})
}

func SetConfig(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "key is required")
		return
	}
	var req configRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	db := database.GetDB()
	db.Exec("INSERT INTO config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?", key, req.Value, req.Value)
	jsonResponse(w, map[string]string{"message": "config updated"})
}

// GetSystemPrompt returns the global system prompt from the config table,
// falling back to DefaultSystemPrompt if not configured.
func GetSystemPrompt() string {
	db := database.GetDB()
	var value string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'system_prompt'").Scan(&value)
	if err != nil || value == "" {
		return DefaultSystemPrompt
	}
	return value
}

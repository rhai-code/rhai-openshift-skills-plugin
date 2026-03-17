package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/eformat/openshift-skills-plugin/pkg/database"
	"github.com/eformat/openshift-skills-plugin/pkg/maas"
	"github.com/gorilla/mux"
)

type createEndpointRequest struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	APIKey       string `json:"api_key"`
	ProviderType string `json:"provider_type"`
}

func ListEndpoints(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	rows, err := db.Query("SELECT id, name, url, COALESCE(api_key,''), provider_type, enabled, created_at FROM maas_endpoints ORDER BY name")
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	endpoints := []database.MaaSEndpoint{}
	for rows.Next() {
		var e database.MaaSEndpoint
		var enabled int
		if err := rows.Scan(&e.ID, &e.Name, &e.URL, &e.APIKey, &e.ProviderType, &enabled, &e.CreatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		e.Enabled = enabled == 1
		// Never return API keys to the frontend
		if e.APIKey != "" {
			e.APIKey = "****"
		}
		endpoints = append(endpoints, e)
	}
	jsonResponse(w, endpoints)
}

func CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req createEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.URL == "" {
		httpError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	if req.ProviderType == "" {
		req.ProviderType = "openai-compatible"
	}

	db := database.GetDB()
	result, err := db.Exec("INSERT INTO maas_endpoints (name, url, api_key, provider_type) VALUES (?, ?, ?, ?)",
		req.Name, req.URL, req.APIKey, req.ProviderType)
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	id, _ := result.LastInsertId()
	jsonResponse(w, map[string]interface{}{"id": id, "message": "endpoint created"})
}

func UpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	var req createEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	db := database.GetDB()
	db.Exec("UPDATE maas_endpoints SET name=?, url=?, api_key=?, provider_type=? WHERE id=?",
		req.Name, req.URL, req.APIKey, req.ProviderType, id)
	jsonResponse(w, map[string]string{"message": "endpoint updated"})
}

func DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	db := database.GetDB()
	db.Exec("DELETE FROM maas_endpoints WHERE id = ?", id)
	jsonResponse(w, map[string]string{"message": "endpoint deleted"})
}

func ListModels(w http.ResponseWriter, r *http.Request) {
	endpointID := r.URL.Query().Get("endpoint_id")
	db := database.GetDB()

	var url, apiKey string
	if endpointID != "" {
		id, _ := strconv.ParseInt(endpointID, 10, 64)
		err := db.QueryRow("SELECT url, COALESCE(api_key,'') FROM maas_endpoints WHERE id = ?", id).Scan(&url, &apiKey)
		if err != nil {
			httpError(w, http.StatusNotFound, "endpoint not found")
			return
		}
	} else {
		err := db.QueryRow("SELECT url, COALESCE(api_key,'') FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&url, &apiKey)
		if err != nil {
			httpError(w, http.StatusBadRequest, "no MaaS endpoint configured")
			return
		}
	}

	client := maas.NewClient("", url, apiKey, "")
	models, err := client.ListModels()
	if err != nil {
		httpError(w, http.StatusBadGateway, "failed to list models: "+err.Error())
		return
	}
	// Return enriched model info including per-model URLs
	jsonResponse(w, models)
}

func HealthCheckEndpoint(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	db := database.GetDB()

	var url, apiKey string
	err := db.QueryRow("SELECT url, COALESCE(api_key,'') FROM maas_endpoints WHERE id = ?", id).Scan(&url, &apiKey)
	if err != nil {
		httpError(w, http.StatusNotFound, "endpoint not found")
		return
	}

	client := maas.NewClient("", url, apiKey, "")
	if err := client.HealthCheck(); err != nil {
		jsonResponse(w, map[string]interface{}{"healthy": false, "error": err.Error()})
		return
	}
	jsonResponse(w, map[string]interface{}{"healthy": true})
}

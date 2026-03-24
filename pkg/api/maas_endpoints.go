package api

import (
	"database/sql"
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
	IsGlobal     *bool  `json:"is_global,omitempty"`
}

func ListEndpoints(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	db := database.GetDB()

	var rows *sql.Rows
	var err error
	if user.IsAdmin {
		rows, err = db.Query("SELECT id, name, url, COALESCE(api_key,''), provider_type, enabled, COALESCE(owner,''), COALESCE(is_global,0), created_at FROM maas_endpoints ORDER BY name")
	} else {
		rows, err = db.Query("SELECT id, name, url, COALESCE(api_key,''), provider_type, enabled, COALESCE(owner,''), COALESCE(is_global,0), created_at FROM maas_endpoints WHERE is_global = 1 OR owner = ? ORDER BY name", user.Username)
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type endpointResponse struct {
		database.MaaSEndpoint
		SingleModel bool   `json:"single_model"`
		ModelName   string `json:"model_name,omitempty"`
	}
	endpoints := []endpointResponse{}
	for rows.Next() {
		var e database.MaaSEndpoint
		var enabled, isGlobal int
		if err := rows.Scan(&e.ID, &e.Name, &e.URL, &e.APIKey, &e.ProviderType, &enabled, &e.Owner, &isGlobal, &e.CreatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		e.Enabled = enabled == 1
		e.IsGlobal = isGlobal == 1
		// Never return API keys to the frontend
		if e.APIKey != "" {
			e.APIKey = "****"
		}
		ep := endpointResponse{MaaSEndpoint: e}
		if maas.IsSingleModelURL(e.URL) {
			ep.SingleModel = true
			ep.ModelName = maas.ModelNameFromURL(e.URL)
		}
		endpoints = append(endpoints, ep)
	}
	jsonResponse(w, endpoints)
}

func CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
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

	isGlobal := 0
	if req.IsGlobal != nil && *req.IsGlobal {
		isGlobal = 1
	}

	db := database.GetDB()
	result, err := db.Exec("INSERT INTO maas_endpoints (name, url, api_key, provider_type, owner, is_global) VALUES (?, ?, ?, ?, ?, ?)",
		req.Name, req.URL, req.APIKey, req.ProviderType, user.Username, isGlobal)
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	id, _ := result.LastInsertId()
	jsonResponse(w, map[string]interface{}{"id": id, "message": "endpoint created"})
}

func UpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)

	db := database.GetDB()
	var owner string
	if err := db.QueryRow("SELECT COALESCE(owner,'') FROM maas_endpoints WHERE id = ?", id).Scan(&owner); err != nil {
		httpError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if !authorizeResource(w, user, owner) {
		return
	}

	var req createEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	db.Exec("UPDATE maas_endpoints SET name=?, url=?, api_key=?, provider_type=? WHERE id=?",
		req.Name, req.URL, req.APIKey, req.ProviderType, id)
	if req.IsGlobal != nil {
		isGlobal := 0
		if *req.IsGlobal {
			isGlobal = 1
		}
		db.Exec("UPDATE maas_endpoints SET is_global=? WHERE id=?", isGlobal, id)
	}
	jsonResponse(w, map[string]string{"message": "endpoint updated"})
}

func DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	db := database.GetDB()

	var owner string
	if err := db.QueryRow("SELECT COALESCE(owner,'') FROM maas_endpoints WHERE id = ?", id).Scan(&owner); err != nil {
		httpError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if !authorizeResource(w, user, owner) {
		return
	}

	db.Exec("DELETE FROM maas_endpoints WHERE id = ?", id)
	jsonResponse(w, map[string]string{"message": "endpoint deleted"})
}

func ListModels(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	endpointID := r.URL.Query().Get("endpoint_id")
	db := database.GetDB()

	var url, apiKey string
	if endpointID != "" {
		id, _ := strconv.ParseInt(endpointID, 10, 64)
		// Check endpoint visibility
		var owner string
		var isGlobal int
		err := db.QueryRow("SELECT url, COALESCE(api_key,''), COALESCE(owner,''), COALESCE(is_global,0) FROM maas_endpoints WHERE id = ?", id).Scan(&url, &apiKey, &owner, &isGlobal)
		if err != nil {
			httpError(w, http.StatusNotFound, "endpoint not found")
			return
		}
		if !user.IsAdmin && isGlobal != 1 && owner != user.Username {
			httpError(w, http.StatusForbidden, "access denied")
			return
		}
	} else {
		if user.IsAdmin {
			err := db.QueryRow("SELECT url, COALESCE(api_key,'') FROM maas_endpoints WHERE enabled = 1 ORDER BY id LIMIT 1").Scan(&url, &apiKey)
			if err != nil {
				httpError(w, http.StatusBadRequest, "no MaaS endpoint configured")
				return
			}
		} else {
			err := db.QueryRow("SELECT url, COALESCE(api_key,'') FROM maas_endpoints WHERE enabled = 1 AND (is_global = 1 OR owner = ?) ORDER BY id LIMIT 1", user.Username).Scan(&url, &apiKey)
			if err != nil {
				httpError(w, http.StatusBadRequest, "no MaaS endpoint configured")
				return
			}
		}
	}

	client := maas.NewClient("", url, apiKey, "")

	// Check if this looks like a single-model OpenAI-compatible URL
	if maas.IsSingleModelURL(url) {
		models, err := client.ListSingleModel(url)
		if err != nil {
			httpError(w, http.StatusBadGateway, "failed to query model: "+err.Error())
			return
		}
		jsonResponse(w, models)
		return
	}

	// Otherwise treat as a model registry
	models, err := client.ListModels()
	if err != nil {
		httpError(w, http.StatusBadGateway, "failed to list models: "+err.Error())
		return
	}
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

	if maas.IsSingleModelURL(url) {
		if err := client.HealthCheckSingleModel(url); err != nil {
			jsonResponse(w, map[string]any{"healthy": false, "error": err.Error()})
			return
		}
		jsonResponse(w, map[string]any{"healthy": true, "single_model": true, "model_name": maas.ModelNameFromURL(url)})
		return
	}

	if err := client.HealthCheck(); err != nil {
		jsonResponse(w, map[string]any{"healthy": false, "error": err.Error()})
		return
	}
	jsonResponse(w, map[string]any{"healthy": true})
}

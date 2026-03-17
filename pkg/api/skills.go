package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/eformat/openshift-skills-plugin/pkg/database"
	"github.com/gorilla/mux"
)

func ListSkills(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	rows, err := db.Query("SELECT id, name, description, content, enabled, created_at, updated_at FROM skills ORDER BY name")
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	skills := []database.Skill{}
	for rows.Next() {
		var s database.Skill
		var enabled int
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Content, &enabled, &s.CreatedAt, &s.UpdatedAt); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.Enabled = enabled == 1
		skills = append(skills, s)
	}
	jsonResponse(w, skills)
}

func GetSkill(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	db := database.GetDB()
	var s database.Skill
	var enabled int
	err := db.QueryRow("SELECT id, name, description, content, enabled, created_at, updated_at FROM skills WHERE id = ?", id).
		Scan(&s.ID, &s.Name, &s.Description, &s.Content, &enabled, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		httpError(w, http.StatusNotFound, "skill not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Enabled = enabled == 1
	jsonResponse(w, s)
}

type createSkillRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

func CreateSkill(w http.ResponseWriter, r *http.Request) {
	var req createSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Description == "" || req.Content == "" {
		httpError(w, http.StatusBadRequest, "name, description, and content are required")
		return
	}

	db := database.GetDB()
	result, err := db.Exec("INSERT INTO skills (name, description, content) VALUES (?, ?, ?)", req.Name, req.Description, req.Content)
	if err != nil {
		httpError(w, http.StatusConflict, "skill already exists or DB error: "+err.Error())
		return
	}
	id, _ := result.LastInsertId()
	jsonResponse(w, map[string]interface{}{"id": id, "name": req.Name, "message": "skill created"})
}

type updateSkillRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Content     *string `json:"content,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

func UpdateSkill(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	var req updateSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	db := database.GetDB()
	now := time.Now()
	if req.Name != nil {
		db.Exec("UPDATE skills SET name = ?, updated_at = ? WHERE id = ?", *req.Name, now, id)
	}
	if req.Description != nil {
		db.Exec("UPDATE skills SET description = ?, updated_at = ? WHERE id = ?", *req.Description, now, id)
	}
	if req.Content != nil {
		db.Exec("UPDATE skills SET content = ?, updated_at = ? WHERE id = ?", *req.Content, now, id)
	}
	if req.Enabled != nil {
		enabled := 0
		if *req.Enabled {
			enabled = 1
		}
		db.Exec("UPDATE skills SET enabled = ?, updated_at = ? WHERE id = ?", enabled, now, id)
	}
	jsonResponse(w, map[string]string{"message": "skill updated"})
}

func DeleteSkill(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	db := database.GetDB()
	db.Exec("DELETE FROM skills WHERE id = ?", id)
	jsonResponse(w, map[string]string{"message": "skill deleted"})
}

func UploadSkill(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "file too large or invalid form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, http.StatusBadRequest, "file required")
		return
	}
	defer file.Close()

	buf := make([]byte, header.Size)
	if _, err := file.Read(buf); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	content := string(buf)

	name := r.FormValue("name")
	if name == "" {
		name = header.Filename
	}
	description := r.FormValue("description")
	if description == "" {
		description = "Uploaded skill: " + name
	}

	db := database.GetDB()
	result, err := db.Exec("INSERT INTO skills (name, description, content) VALUES (?, ?, ?)", name, description, content)
	if err != nil {
		httpError(w, http.StatusConflict, "skill already exists or DB error: "+err.Error())
		return
	}
	id, _ := result.LastInsertId()
	jsonResponse(w, map[string]interface{}{"id": id, "name": name, "message": "skill uploaded"})
}

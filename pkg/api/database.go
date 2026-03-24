package api

import (
	"io"
	"net/http"
	"os"

	"github.com/eformat/openshift-skills-plugin/pkg/database"
)

func ExportDatabase(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if !user.IsAdmin {
		httpError(w, http.StatusForbidden, "admin access required")
		return
	}

	// Flush WAL to ensure the file is complete
	if err := database.Checkpoint(); err != nil {
		httpError(w, http.StatusInternalServerError, "checkpoint failed: "+err.Error())
		return
	}

	dbPath := database.GetDBPath()
	f, err := os.Open(dbPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "open database file: "+err.Error())
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "stat database file: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=skills.db")
	http.ServeContent(w, r, "skills.db", stat.ModTime(), f)
}

func ImportDatabase(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if !user.IsAdmin {
		httpError(w, http.StatusForbidden, "admin access required")
		return
	}

	// Limit upload size to 100MB
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

	file, _, err := r.FormFile("database")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing 'database' file in upload")
		return
	}
	defer file.Close()

	// Write uploaded file to a temp location
	tmpFile, err := os.CreateTemp("", "skills-import-*.db")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "create temp file: "+err.Error())
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		httpError(w, http.StatusInternalServerError, "write temp file: "+err.Error())
		return
	}
	tmpFile.Close()

	// Replace the database and re-initialize
	if err := database.Reinit(tmpPath); err != nil {
		httpError(w, http.StatusInternalServerError, "import failed: "+err.Error())
		return
	}

	// Reload the scheduler with the new database contents
	ReloadScheduler()

	jsonResponse(w, map[string]string{"message": "database imported successfully"})
}

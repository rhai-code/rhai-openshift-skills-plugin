package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var (
	db     *sql.DB
	dbPath string
	mu     sync.Mutex
	once   sync.Once
)

func Init() (*sql.DB, error) {
	var initErr error
	once.Do(func() {
		dataDir := os.Getenv("SKILLS_DATA_DIR")
		if dataDir == "" {
			dataDir = "/tmp/skills-plugin"
		}
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			initErr = fmt.Errorf("create data dir: %w", err)
			return
		}
		dbPath = filepath.Join(dataDir, "skills.db")
		var err error
		db, err = sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			initErr = fmt.Errorf("open database: %w", err)
			return
		}
		db.SetMaxOpenConns(1)
		if err := migrate(db); err != nil {
			initErr = fmt.Errorf("migrate: %w", err)
			return
		}
		log.Printf("Database initialized at %s", dbPath)
	})
	return db, initErr
}

func GetDB() *sql.DB {
	return db
}

func GetDBPath() string {
	return dbPath
}

// Checkpoint flushes the WAL to the main database file.
func Checkpoint() error {
	_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Reinit closes the current DB, replaces the file, and reopens it.
func Reinit(newDBPath string) error {
	mu.Lock()
	defer mu.Unlock()

	// Close current connection
	if db != nil {
		db.Close()
	}

	// Replace the database file
	src, err := os.Open(newDBPath)
	if err != nil {
		return fmt.Errorf("open new db: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dbPath)
	if err != nil {
		return fmt.Errorf("create db file: %w", err)
	}
	defer dst.Close()

	if _, err := dst.ReadFrom(src); err != nil {
		return fmt.Errorf("copy db: %w", err)
	}

	// Remove any leftover WAL/SHM files from old DB
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	// Reopen
	db, err = sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("reopen database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		return fmt.Errorf("migrate after import: %w", err)
	}

	log.Printf("Database re-initialized from import")
	return nil
}

func migrate(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS skills (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			description TEXT NOT NULL,
			content TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			base_url TEXT,
			system_prompt TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS scheduled_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			description TEXT,
			skill_id INTEGER REFERENCES skills(id) ON DELETE SET NULL,
			schedule TEXT NOT NULL,
			service_account TEXT NOT NULL DEFAULT 'default',
			namespace TEXT NOT NULL DEFAULT 'default',
			enabled INTEGER DEFAULT 1,
			last_run DATETIME,
			next_run DATETIME,
			run_count INTEGER DEFAULT 0,
			session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
			provider TEXT NOT NULL DEFAULT 'openai-compatible',
			model TEXT NOT NULL DEFAULT '',
			base_url TEXT,
			api_key TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS task_execution_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME,
			duration_ms INTEGER,
			status TEXT NOT NULL,
			error_message TEXT,
			output TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS maas_endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			url TEXT NOT NULL,
			api_key TEXT,
			provider_type TEXT NOT NULL DEFAULT 'openai-compatible',
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS session_skills (
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			skill_id INTEGER NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
			PRIMARY KEY (session_id, skill_id)
		)`,
		`CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		// Migrations for existing tables
		`ALTER TABLE scheduled_tasks ADD COLUMN container_image TEXT DEFAULT ''`,
		`ALTER TABLE scheduled_tasks ADD COLUMN temperature REAL DEFAULT 0.2`,
		`ALTER TABLE scheduled_tasks ADD COLUMN max_tokens INTEGER DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN temperature REAL DEFAULT 0.2`,
		`ALTER TABLE sessions ADD COLUMN max_tokens INTEGER DEFAULT 0`,
		`ALTER TABLE scheduled_tasks ADD COLUMN run_once INTEGER DEFAULT 0`,
		`ALTER TABLE scheduled_tasks ADD COLUMN run_once_delay TEXT DEFAULT ''`,
	}
	// ALTER TABLE will fail if column already exists, that's fine
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			// ALTER TABLE fails if column already exists — ignore those errors
			if len(stmt) > 5 && stmt[:5] == "ALTER" {
				continue
			}
			return fmt.Errorf("exec %q: %w", stmt[:50], err)
		}
	}
	return nil
}

package database

import "time"

type Skill struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Session struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	BaseURL      string    `json:"base_url,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Message struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type ScheduledTask struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	SkillID        *int64     `json:"skill_id,omitempty"`
	Schedule       string     `json:"schedule"`
	ServiceAccount string     `json:"service_account"`
	Namespace      string     `json:"namespace"`
	Enabled        bool       `json:"enabled"`
	LastRun        *time.Time `json:"last_run,omitempty"`
	NextRun        *time.Time `json:"next_run,omitempty"`
	RunCount       int        `json:"run_count"`
	SessionID      *string    `json:"session_id,omitempty"`
	Provider       string     `json:"provider"`
	Model          string     `json:"model"`
	BaseURL        string     `json:"base_url,omitempty"`
	APIKey         string     `json:"-"`
	ContainerImage string    `json:"container_image,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type TaskExecutionHistory struct {
	ID           int64      `json:"id"`
	TaskID       int64      `json:"task_id"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	DurationMs   *int64     `json:"duration_ms,omitempty"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"error_message,omitempty"`
	Output       string     `json:"output,omitempty"`
}

type MaaSEndpoint struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	APIKey       string    `json:"api_key,omitempty"`
	ProviderType string    `json:"provider_type"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

type Config struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

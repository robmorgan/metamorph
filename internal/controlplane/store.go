package controlplane

import (
	"context"
	"time"
)

// Epic represents a high-level goal that decomposes into tasks via LLM.
type Epic struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"` // open, decomposing, review, active, done
	Project     string     `json:"project"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Task represents a unit of work in the broker.
type Task struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	Status         string     `json:"status"`        // open, claimed, done, blocked
	Priority       int        `json:"priority"`       // 0=critical, 4=backlog
	Assignee       *int       `json:"assignee"`       // agent ID
	RoleAffinity   string     `json:"role_affinity,omitempty"`
	Project        string     `json:"project"`        // project name
	Repo           string     `json:"repo,omitempty"`
	Branch         string     `json:"branch,omitempty"`
	EpicID         string     `json:"epic_id,omitempty"`
	ExpandedPrompt string     `json:"expanded_prompt,omitempty"`
	PromptStatus   string     `json:"prompt_status,omitempty"` // "", pending_review, approved, rejected
	ClaimedAt      *time.Time `json:"claimed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	DependsOn      []string   `json:"depends_on,omitempty"`
	Notes          string     `json:"notes,omitempty"`
}

// ProjectDetail is the enriched project response returned by the API.
type ProjectDetail struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Status      string        `json:"status"`
	Repos       []ProjectRepo `json:"repos"`
	TaskCount   int           `json:"task_count"`
	AgentCount  int           `json:"agent_count"`
	CreatedAt   time.Time     `json:"created_at"`
}

// TaskFilter controls which tasks are returned by ListTasks.
type TaskFilter struct {
	Status       string // filter by status (empty = all)
	RoleAffinity string // filter by role_affinity (empty = all)
	AssigneeID   *int   // filter by assignee (nil = all)
	Project      string // filter by project (empty = all)
	EpicID       string // filter by epic_id (empty = all)
}

// TaskUpdate holds mutable fields for PATCH /tasks/{id}.
type TaskUpdate struct {
	Status         *string `json:"status,omitempty"`
	Priority       *int    `json:"priority,omitempty"`
	Notes          *string `json:"notes,omitempty"`
	Description    *string `json:"description,omitempty"`
	Title          *string `json:"title,omitempty"`
	ExpandedPrompt *string `json:"expanded_prompt,omitempty"`
	PromptStatus   *string `json:"prompt_status,omitempty"`
}

// Message represents an inter-agent message.
type Message struct {
	ID        int64     `json:"id"`
	FromAgent int       `json:"from_agent"`
	TaskID    string    `json:"task_id,omitempty"`
	Text      string    `json:"text"`
	Project   string    `json:"project"`
	CreatedAt time.Time `json:"created_at"`
}

// ProjectInfo holds metadata about a registered project.
type ProjectInfo struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ProjectRepo maps a repo directory path to a project.
type ProjectRepo struct {
	ID        int64     `json:"id"`
	Project   string    `json:"project"`
	Path      string    `json:"path"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is the persistence interface for the task broker. Implementations
// must be safe for concurrent use from a single goroutine (the daemon
// serializes all calls via sync.Mutex).
type Store interface {
	// Task operations.
	ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error)
	GetTask(ctx context.Context, taskID string) (*Task, error)
	CreateTask(ctx context.Context, t Task) error
	ClaimTask(ctx context.Context, taskID string, agentID int) error
	ClaimNextTask(ctx context.Context, agentID int, role string, project string) (*Task, error)
	ReleaseTask(ctx context.Context, taskID string, agentID int) error
	CompleteTask(ctx context.Context, taskID string, summary string) error
	UpdateTask(ctx context.Context, taskID string, updates TaskUpdate) error

	// Epic operations.
	CreateEpic(ctx context.Context, e Epic) error
	GetEpic(ctx context.Context, epicID string) (*Epic, error)
	ListEpics(ctx context.Context, project string) ([]Epic, error)
	UpdateEpicStatus(ctx context.Context, epicID, status string) error
	ListTasksByEpic(ctx context.Context, epicID string) ([]Task, error)

	// Messaging.
	PostMessage(ctx context.Context, msg Message) error
	ListMessages(ctx context.Context, since time.Time, project string) ([]Message, error)

	// Project registry.
	RegisterProject(ctx context.Context, name string) error
	UnregisterProject(ctx context.Context, name string) error
	ListProjects(ctx context.Context) ([]ProjectInfo, error)
	GetProject(ctx context.Context, name string) (*ProjectInfo, error)
	UpdateProjectStatus(ctx context.Context, name, status string) error
	AddProjectRepo(ctx context.Context, project, path, label string) error
	GetProjectByRepoPath(ctx context.Context, repoPath string) (string, error)
	ListProjectRepos(ctx context.Context, project string) ([]ProjectRepo, error)
	CountTasksByProject(ctx context.Context, project string) (int, error)

	// Lifecycle.
	Close() error
}

package broker

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore implements Store using SQLite with WAL mode.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) a SQLite database at path and runs migrations.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("broker: open database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("broker: migrate: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		// Project registry.
		`CREATE TABLE IF NOT EXISTS projects (
			name       TEXT PRIMARY KEY,
			status     TEXT DEFAULT 'stopped',
			created_at DATETIME DEFAULT (datetime('now'))
		)`,
		// Maps repo directories to projects (for CWD-based project resolution).
		`CREATE TABLE IF NOT EXISTS project_repos (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			project    TEXT NOT NULL REFERENCES projects(name),
			path       TEXT NOT NULL,
			label      TEXT DEFAULT '',
			created_at DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_project_repos_path ON project_repos(path)`,
		// Tasks with project scope.
		`CREATE TABLE IF NOT EXISTS tasks (
			id            TEXT PRIMARY KEY,
			title         TEXT NOT NULL,
			description   TEXT DEFAULT '',
			status        TEXT DEFAULT 'open',
			priority      INTEGER DEFAULT 2,
			assignee      INTEGER,
			role_affinity TEXT DEFAULT '',
			project       TEXT REFERENCES projects(name),
			claimed_at    DATETIME,
			created_at    DATETIME DEFAULT (datetime('now')),
			completed_at  DATETIME,
			depends_on    TEXT DEFAULT '',
			notes         TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS task_history (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id     TEXT NOT NULL REFERENCES tasks(id),
			old_status  TEXT,
			new_status  TEXT NOT NULL,
			agent_id    INTEGER,
			changed_at  DATETIME DEFAULT (datetime('now')),
			notes       TEXT DEFAULT ''
		)`,
		// Messages with project scope.
		`CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			from_agent INTEGER NOT NULL,
			task_id    TEXT DEFAULT '',
			text       TEXT NOT NULL,
			project    TEXT DEFAULT '',
			created_at DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project, status)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_task_id ON task_history(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_project ON messages(project, created_at)`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:min(40, len(s))], err)
		}
	}

	// Run additive migrations for existing databases.
	addColumnIfMissing(db, "tasks", "project", "TEXT DEFAULT ''")
	addColumnIfMissing(db, "messages", "project", "TEXT DEFAULT ''")
	addColumnIfMissing(db, "tasks", "repo", "TEXT DEFAULT ''")
	addColumnIfMissing(db, "tasks", "branch", "TEXT DEFAULT ''")

	return nil
}

// addColumnIfMissing adds a column to a table if it doesn't exist yet.
func addColumnIfMissing(db *sql.DB, table, column, colDef string) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return
		}
		if name == column {
			return // column already exists
		}
	}

	_, _ = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, colDef))
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --- Project Registry ---

func (s *SQLiteStore) RegisterProject(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO projects (name, status) VALUES (?, 'stopped')`, name)
	if err != nil {
		return fmt.Errorf("broker: register project: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UnregisterProject(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_repos WHERE project = ?`, name)
	if err != nil {
		return fmt.Errorf("broker: unregister project repos: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM projects WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("broker: unregister project: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, status, created_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("broker: list projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectInfo
	for rows.Next() {
		var p ProjectInfo
		if err := rows.Scan(&p.Name, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("broker: scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *SQLiteStore) GetProject(ctx context.Context, name string) (*ProjectInfo, error) {
	var p ProjectInfo
	err := s.db.QueryRowContext(ctx,
		`SELECT name, status, created_at FROM projects WHERE name = ?`, name).
		Scan(&p.Name, &p.Status, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("broker: project %q not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("broker: get project: %w", err)
	}
	return &p, nil
}

func (s *SQLiteStore) UpdateProjectStatus(ctx context.Context, name, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET status = ? WHERE name = ?`, status, name)
	if err != nil {
		return fmt.Errorf("broker: update project status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("broker: project %q not found", name)
	}
	return nil
}

func (s *SQLiteStore) AddProjectRepo(ctx context.Context, project, path, label string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO project_repos (project, path, label) VALUES (?, ?, ?)`,
		project, path, label)
	if err != nil {
		return fmt.Errorf("broker: add project repo: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetProjectByRepoPath(ctx context.Context, repoPath string) (string, error) {
	var project string
	err := s.db.QueryRowContext(ctx,
		`SELECT project FROM project_repos WHERE path = ?`, repoPath).Scan(&project)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("broker: no project found for repo path %q", repoPath)
	}
	if err != nil {
		return "", fmt.Errorf("broker: lookup project by repo path: %w", err)
	}
	return project, nil
}

// --- Task Operations ---

// ListProjectRepos returns all repos associated with a project.
func (s *SQLiteStore) ListProjectRepos(ctx context.Context, project string) ([]ProjectRepo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project, path, label, created_at FROM project_repos WHERE project = ? ORDER BY id`, project)
	if err != nil {
		return nil, fmt.Errorf("broker: list project repos: %w", err)
	}
	defer rows.Close()

	var repos []ProjectRepo
	for rows.Next() {
		var r ProjectRepo
		if err := rows.Scan(&r.ID, &r.Project, &r.Path, &r.Label, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("broker: scan project repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// CountTasksByProject returns the number of tasks for a project.
func (s *SQLiteStore) CountTasksByProject(ctx context.Context, project string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE project = ?`, project).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("broker: count tasks: %w", err)
	}
	return count, nil
}

// ListTasks returns tasks matching the filter, ordered by priority then creation time.
func (s *SQLiteStore) ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error) {
	query := `SELECT id, title, description, status, priority, assignee,
	                  role_affinity, project, repo, branch,
	                  claimed_at, created_at, completed_at,
	                  depends_on, notes
	           FROM tasks WHERE 1=1`
	var args []interface{}

	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.RoleAffinity != "" {
		query += " AND role_affinity = ?"
		args = append(args, filter.RoleAffinity)
	}
	if filter.AssigneeID != nil {
		query += " AND assignee = ?"
		args = append(args, *filter.AssigneeID)
	}
	if filter.Project != "" {
		query += " AND project = ?"
		args = append(args, filter.Project)
	}

	query += " ORDER BY priority ASC, created_at ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("broker: list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// GetTask returns a single task by ID.
func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, title, description, status, priority, assignee,
		        role_affinity, project, repo, branch,
		        claimed_at, created_at, completed_at,
		        depends_on, notes
		 FROM tasks WHERE id = ?`, taskID)

	t, err := scanTaskRow(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("broker: task %q not found", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("broker: get task: %w", err)
	}
	return &t, nil
}

// CreateTask inserts a new task and records history.
func (s *SQLiteStore) CreateTask(ctx context.Context, t Task) error {
	if t.Status == "" {
		t.Status = "open"
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}

	deps := strings.Join(t.DependsOn, ",")

	// Store NULL instead of empty string so the FK constraint is not triggered
	// for unscoped tasks.
	var project interface{} = t.Project
	if t.Project == "" {
		project = nil
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, description, status, priority, assignee,
		                    role_affinity, project, repo, branch, created_at, depends_on, notes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, nil,
		t.RoleAffinity, project, t.Repo, t.Branch, t.CreatedAt, deps, t.Notes)
	if err != nil {
		return fmt.Errorf("broker: create task: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO task_history (task_id, old_status, new_status, notes)
		 VALUES (?, '', ?, 'created')`, t.ID, t.Status)
	return err
}

// ClaimTask atomically assigns a task to an agent. Returns error if already claimed.
func (s *SQLiteStore) ClaimTask(ctx context.Context, taskID string, agentID int) error {
	now := time.Now().UTC()

	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'claimed', assignee = ?, claimed_at = ?
		 WHERE id = ? AND status = 'open'`,
		agentID, now, taskID)
	if err != nil {
		return fmt.Errorf("broker: claim task: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		var status string
		err := s.db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&status)
		if err == sql.ErrNoRows {
			return fmt.Errorf("broker: task %q not found", taskID)
		}
		return fmt.Errorf("broker: task %q is not open (status=%s)", taskID, status)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO task_history (task_id, old_status, new_status, agent_id, notes)
		 VALUES (?, 'open', 'claimed', ?, 'claimed')`, taskID, agentID)
	return err
}

// ReleaseTask returns a claimed task to the open pool. Only the current assignee can release.
func (s *SQLiteStore) ReleaseTask(ctx context.Context, taskID string, agentID int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'open', assignee = NULL, claimed_at = NULL
		 WHERE id = ? AND status = 'claimed' AND assignee = ?`,
		taskID, agentID)
	if err != nil {
		return fmt.Errorf("broker: release task: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("broker: task %q not claimed by agent %d", taskID, agentID)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO task_history (task_id, old_status, new_status, agent_id, notes)
		 VALUES (?, 'claimed', 'open', ?, 'released')`, taskID, agentID)
	return err
}

// CompleteTask marks a task as done with an optional summary.
func (s *SQLiteStore) CompleteTask(ctx context.Context, taskID string, summary string) error {
	now := time.Now().UTC()

	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'done', completed_at = ?, notes = CASE WHEN ? != '' THEN ? ELSE notes END
		 WHERE id = ? AND status = 'claimed'`,
		now, summary, summary, taskID)
	if err != nil {
		return fmt.Errorf("broker: complete task: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("broker: task %q is not claimed", taskID)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO task_history (task_id, old_status, new_status, notes)
		 VALUES (?, 'claimed', 'done', ?)`, taskID, summary)
	return err
}

// UpdateTask applies partial updates to a task.
func (s *SQLiteStore) UpdateTask(ctx context.Context, taskID string, updates TaskUpdate) error {
	var setClauses []string
	var args []interface{}

	if updates.Status != nil {
		setClauses = append(setClauses, "status = ?")
		args = append(args, *updates.Status)
	}
	if updates.Priority != nil {
		setClauses = append(setClauses, "priority = ?")
		args = append(args, *updates.Priority)
	}
	if updates.Notes != nil {
		setClauses = append(setClauses, "notes = ?")
		args = append(args, *updates.Notes)
	}
	if updates.Description != nil {
		setClauses = append(setClauses, "description = ?")
		args = append(args, *updates.Description)
	}
	if updates.Title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *updates.Title)
	}

	if len(setClauses) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	args = append(args, taskID)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("broker: update task: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("broker: task %q not found", taskID)
	}

	if updates.Status != nil {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO task_history (task_id, old_status, new_status, notes)
			 VALUES (?, '', ?, 'updated')`, taskID, *updates.Status)
	}
	return err
}

// --- Messaging ---

// PostMessage inserts a new inter-agent message.
func (s *SQLiteStore) PostMessage(ctx context.Context, msg Message) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (from_agent, task_id, text, project) VALUES (?, ?, ?, ?)`,
		msg.FromAgent, msg.TaskID, msg.Text, msg.Project)
	if err != nil {
		return fmt.Errorf("broker: post message: %w", err)
	}
	return nil
}

// ListMessages returns messages created after the given timestamp, optionally scoped to a project.
func (s *SQLiteStore) ListMessages(ctx context.Context, since time.Time, project string) ([]Message, error) {
	query := `SELECT id, from_agent, task_id, text, project, created_at
		 FROM messages WHERE created_at > ?`
	args := []interface{}{since}

	if project != "" {
		query += " AND project = ?"
		args = append(args, project)
	}

	query += " ORDER BY created_at ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("broker: list messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var taskID string
		if err := rows.Scan(&m.ID, &m.FromAgent, &taskID, &m.Text, &m.Project, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("broker: scan message: %w", err)
		}
		m.TaskID = taskID
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// --- Scan helpers ---

// scanTask scans a Task from sql.Rows.
func scanTask(rows *sql.Rows) (Task, error) {
	var t Task
	var assignee sql.NullInt64
	var claimedAt, completedAt sql.NullTime
	var deps, roleAffinity string
	var project, repo, branch sql.NullString

	err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&assignee, &roleAffinity, &project, &repo, &branch,
		&claimedAt, &t.CreatedAt, &completedAt,
		&deps, &t.Notes)
	if err != nil {
		return Task{}, fmt.Errorf("broker: scan task: %w", err)
	}

	t.RoleAffinity = roleAffinity
	if project.Valid {
		t.Project = project.String
	}
	if repo.Valid {
		t.Repo = repo.String
	}
	if branch.Valid {
		t.Branch = branch.String
	}
	if assignee.Valid {
		v := int(assignee.Int64)
		t.Assignee = &v
	}
	if claimedAt.Valid {
		t.ClaimedAt = &claimedAt.Time
	}
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}
	if deps != "" {
		t.DependsOn = strings.Split(deps, ",")
	}

	return t, nil
}

// scanTaskRow scans a Task from a single sql.Row.
func scanTaskRow(row *sql.Row) (Task, error) {
	var t Task
	var assignee sql.NullInt64
	var claimedAt, completedAt sql.NullTime
	var deps, roleAffinity string
	var project, repo, branch sql.NullString

	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&assignee, &roleAffinity, &project, &repo, &branch,
		&claimedAt, &t.CreatedAt, &completedAt,
		&deps, &t.Notes)
	if err != nil {
		return Task{}, err
	}

	t.RoleAffinity = roleAffinity
	if project.Valid {
		t.Project = project.String
	}
	if repo.Valid {
		t.Repo = repo.String
	}
	if branch.Valid {
		t.Branch = branch.String
	}
	if assignee.Valid {
		v := int(assignee.Int64)
		t.Assignee = &v
	}
	if claimedAt.Valid {
		t.ClaimedAt = &claimedAt.Time
	}
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}
	if deps != "" {
		t.DependsOn = strings.Split(deps, ",")
	}

	return t, nil
}

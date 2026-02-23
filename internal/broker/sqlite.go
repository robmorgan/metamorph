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
		`CREATE TABLE IF NOT EXISTS tasks (
			id            TEXT PRIMARY KEY,
			title         TEXT NOT NULL,
			description   TEXT DEFAULT '',
			status        TEXT DEFAULT 'open',
			priority      INTEGER DEFAULT 2,
			assignee      INTEGER,
			role_affinity TEXT DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			from_agent INTEGER NOT NULL,
			task_id    TEXT DEFAULT '',
			text       TEXT NOT NULL,
			created_at DATETIME DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_task_id ON task_history(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:40], err)
		}
	}
	return nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ListTasks returns tasks matching the filter, ordered by priority then creation time.
func (s *SQLiteStore) ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error) {
	query := `SELECT id, title, description, status, priority, assignee,
	                  role_affinity, claimed_at, created_at, completed_at,
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
		        role_affinity, claimed_at, created_at, completed_at,
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

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, description, status, priority, assignee,
		                    role_affinity, created_at, depends_on, notes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, nil,
		t.RoleAffinity, t.CreatedAt, deps, t.Notes)
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
		// Check if the task exists at all.
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

	// Record status change in history if status was updated.
	if updates.Status != nil {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO task_history (task_id, old_status, new_status, notes)
			 VALUES (?, '', ?, 'updated')`, taskID, *updates.Status)
	}
	return err
}

// PostMessage inserts a new inter-agent message.
func (s *SQLiteStore) PostMessage(ctx context.Context, msg Message) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (from_agent, task_id, text) VALUES (?, ?, ?)`,
		msg.FromAgent, msg.TaskID, msg.Text)
	if err != nil {
		return fmt.Errorf("broker: post message: %w", err)
	}
	return nil
}

// ListMessages returns messages created after the given timestamp.
func (s *SQLiteStore) ListMessages(ctx context.Context, since time.Time) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, from_agent, task_id, text, created_at
		 FROM messages WHERE created_at > ? ORDER BY created_at ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("broker: list messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var taskID string
		if err := rows.Scan(&m.ID, &m.FromAgent, &taskID, &m.Text, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("broker: scan message: %w", err)
		}
		m.TaskID = taskID
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// scanTask scans a Task from sql.Rows.
func scanTask(rows *sql.Rows) (Task, error) {
	var t Task
	var assignee sql.NullInt64
	var claimedAt, completedAt sql.NullTime
	var deps, roleAffinity string

	err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&assignee, &roleAffinity, &claimedAt, &t.CreatedAt, &completedAt,
		&deps, &t.Notes)
	if err != nil {
		return Task{}, fmt.Errorf("broker: scan task: %w", err)
	}

	t.RoleAffinity = roleAffinity
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

	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&assignee, &roleAffinity, &claimedAt, &t.CreatedAt, &completedAt,
		&deps, &t.Notes)
	if err != nil {
		return Task{}, err
	}

	t.RoleAffinity = roleAffinity
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

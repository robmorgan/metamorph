package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Broker is the HTTP task broker that agents interact with.
type Broker struct {
	store  Store
	mu     sync.Mutex
	server *http.Server
}

// New creates a Broker backed by the given Store.
func New(store Store) *Broker {
	return &Broker{store: store}
}

// Start begins serving the broker API on the given address (e.g. ":8377").
// It returns immediately; call Shutdown to stop.
func (b *Broker) Start(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /tasks", b.handleListTasks)
	mux.HandleFunc("POST /tasks", b.handleCreateTask)
	mux.HandleFunc("GET /tasks/{id}", b.handleGetTask)
	mux.HandleFunc("PATCH /tasks/{id}", b.handleUpdateTask)
	mux.HandleFunc("POST /tasks/{id}/claim", b.handleClaimTask)
	mux.HandleFunc("POST /tasks/{id}/release", b.handleReleaseTask)
	mux.HandleFunc("POST /tasks/{id}/complete", b.handleCompleteTask)

	mux.HandleFunc("GET /messages", b.handleListMessages)
	mux.HandleFunc("POST /messages", b.handlePostMessage)

	mux.HandleFunc("GET /health", b.handleHealth)

	b.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("broker: listen %s: %w", addr, err)
	}

	go func() {
		slog.Info("broker listening", "addr", addr)
		if err := b.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("broker serve error", "error", err)
		}
	}()

	return nil
}

// Shutdown gracefully stops the HTTP server.
func (b *Broker) Shutdown(ctx context.Context) error {
	if b.server != nil {
		return b.server.Shutdown(ctx)
	}
	return nil
}

// Handler returns the HTTP handler for testing without starting a server.
func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /tasks", b.handleListTasks)
	mux.HandleFunc("POST /tasks", b.handleCreateTask)
	mux.HandleFunc("GET /tasks/{id}", b.handleGetTask)
	mux.HandleFunc("PATCH /tasks/{id}", b.handleUpdateTask)
	mux.HandleFunc("POST /tasks/{id}/claim", b.handleClaimTask)
	mux.HandleFunc("POST /tasks/{id}/release", b.handleReleaseTask)
	mux.HandleFunc("POST /tasks/{id}/complete", b.handleCompleteTask)

	mux.HandleFunc("GET /messages", b.handleListMessages)
	mux.HandleFunc("POST /messages", b.handlePostMessage)

	mux.HandleFunc("GET /health", b.handleHealth)

	return mux
}

// --- Handlers ---

func (b *Broker) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (b *Broker) handleListTasks(w http.ResponseWriter, r *http.Request) {
	filter := TaskFilter{
		Status:       r.URL.Query().Get("status"),
		RoleAffinity: r.URL.Query().Get("role"),
	}

	b.mu.Lock()
	tasks, err := b.store.ListTasks(r.Context(), filter)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (b *Broker) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	b.mu.Lock()
	task, err := b.store.GetTask(r.Context(), id)
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, task)
}

type createTaskRequest struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Priority     int      `json:"priority"`
	RoleAffinity string   `json:"role_affinity"`
	DependsOn    []string `json:"depends_on"`
}

func (b *Broker) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("title is required"))
		return
	}

	if req.ID == "" {
		req.ID = generateID()
	}

	task := Task{
		ID:           req.ID,
		Title:        req.Title,
		Description:  req.Description,
		Status:       "open",
		Priority:     req.Priority,
		RoleAffinity: req.RoleAffinity,
		DependsOn:    req.DependsOn,
		CreatedAt:    time.Now().UTC(),
	}

	b.mu.Lock()
	err := b.store.CreateTask(r.Context(), task)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusCreated, task)
}

type claimRequest struct {
	AgentID int `json:"agent_id"`
}

func (b *Broker) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req claimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.AgentID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("agent_id is required"))
		return
	}

	b.mu.Lock()
	err := b.store.ClaimTask(r.Context(), id, req.AgentID)
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusConflict, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "claimed"})
}

type releaseRequest struct {
	AgentID int `json:"agent_id"`
}

func (b *Broker) handleReleaseTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req releaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.AgentID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("agent_id is required"))
		return
	}

	b.mu.Lock()
	err := b.store.ReleaseTask(r.Context(), id, req.AgentID)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "released"})
}

type completeRequest struct {
	Summary string `json:"summary"`
}

func (b *Broker) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	b.mu.Lock()
	err := b.store.CompleteTask(r.Context(), id, req.Summary)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
}

func (b *Broker) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var updates TaskUpdate
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	b.mu.Lock()
	err := b.store.UpdateTask(r.Context(), id, updates)
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

type postMessageRequest struct {
	FromAgent int    `json:"from_agent"`
	TaskID    string `json:"task_id"`
	Text      string `json:"text"`
}

func (b *Broker) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	var req postMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.FromAgent <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("from_agent is required"))
		return
	}
	if req.Text == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("text is required"))
		return
	}

	msg := Message{
		FromAgent: req.FromAgent,
		TaskID:    req.TaskID,
		Text:      req.Text,
	}

	b.mu.Lock()
	err := b.store.PostMessage(r.Context(), msg)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "sent"})
}

func (b *Broker) handleListMessages(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	var since time.Time
	if sinceStr != "" {
		var err error
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid since timestamp: %w", err))
			return
		}
	}

	b.mu.Lock()
	msgs, err := b.store.ListMessages(r.Context(), since)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if msgs == nil {
		msgs = []Message{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// generateID creates a short unique task ID based on timestamp.
func generateID() string {
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

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
	b.server = &http.Server{
		Addr:         addr,
		Handler:      b.buildMux(),
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
	return b.buildMux()
}

// Store returns the underlying Store for direct access (e.g. project resolution).
func (b *Broker) Store() Store {
	return b.store
}

func (b *Broker) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Task endpoints.
	mux.HandleFunc("GET /tasks", b.handleListTasks)
	mux.HandleFunc("POST /tasks", b.handleCreateTask)
	mux.HandleFunc("GET /tasks/{id}", b.handleGetTask)
	mux.HandleFunc("PATCH /tasks/{id}", b.handleUpdateTask)
	mux.HandleFunc("POST /tasks/{id}/claim", b.handleClaimTask)
	mux.HandleFunc("POST /tasks/{id}/release", b.handleReleaseTask)
	mux.HandleFunc("POST /tasks/{id}/complete", b.handleCompleteTask)

	// Message endpoints.
	mux.HandleFunc("GET /messages", b.handleListMessages)
	mux.HandleFunc("POST /messages", b.handlePostMessage)

	// Project endpoints.
	mux.HandleFunc("GET /projects", b.handleListProjects)
	mux.HandleFunc("POST /projects", b.handleRegisterProject)
	mux.HandleFunc("GET /projects/{name}", b.handleGetProject)
	mux.HandleFunc("DELETE /projects/{name}", b.handleUnregisterProject)
	mux.HandleFunc("POST /projects/{name}/start", b.handleStartProject)
	mux.HandleFunc("POST /projects/{name}/stop", b.handleStopProject)
	mux.HandleFunc("GET /projects/{name}/status", b.handleProjectStatus)

	mux.HandleFunc("GET /health", b.handleHealth)

	return mux
}

// --- Task Handlers ---

func (b *Broker) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (b *Broker) handleListTasks(w http.ResponseWriter, r *http.Request) {
	filter := TaskFilter{
		Status:       r.URL.Query().Get("status"),
		RoleAffinity: r.URL.Query().Get("role"),
		Project:      r.URL.Query().Get("project"),
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
	Project      string   `json:"project"`
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
		Project:      req.Project,
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

// --- Message Handlers ---

type postMessageRequest struct {
	FromAgent int    `json:"from_agent"`
	TaskID    string `json:"task_id"`
	Text      string `json:"text"`
	Project   string `json:"project"`
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
		Project:   req.Project,
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
	project := r.URL.Query().Get("project")
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
	msgs, err := b.store.ListMessages(r.Context(), since, project)
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

// --- Project Handlers ---

func (b *Broker) handleListProjects(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	projects, err := b.store.ListProjects(r.Context())
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if projects == nil {
		projects = []ProjectInfo{}
	}
	writeJSON(w, http.StatusOK, projects)
}

type registerProjectRequest struct {
	Name     string `json:"name"`
	RepoPath string `json:"repo_path"`
	Label    string `json:"label"`
}

func (b *Broker) handleRegisterProject(w http.ResponseWriter, r *http.Request) {
	var req registerProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}

	b.mu.Lock()
	err := b.store.RegisterProject(r.Context(), req.Name)
	if err == nil && req.RepoPath != "" {
		err = b.store.AddProjectRepo(r.Context(), req.Name, req.RepoPath, req.Label)
	}
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

func (b *Broker) handleGetProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	b.mu.Lock()
	project, err := b.store.GetProject(r.Context(), name)
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (b *Broker) handleUnregisterProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	b.mu.Lock()
	err := b.store.UnregisterProject(r.Context(), name)
	b.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

func (b *Broker) handleStartProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	b.mu.Lock()
	err := b.store.UpdateProjectStatus(r.Context(), name, "running")
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (b *Broker) handleStopProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	b.mu.Lock()
	err := b.store.UpdateProjectStatus(r.Context(), name, "stopped")
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (b *Broker) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	b.mu.Lock()
	project, err := b.store.GetProject(r.Context(), name)
	b.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, project)
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

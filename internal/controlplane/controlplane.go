package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ControlPlane is the HTTP server that manages projects, tasks, and agents.
type ControlPlane struct {
	store         Store
	mu            sync.Mutex
	server        *http.Server
	extraHandlers []extraHandler
	configReader  func(name string) string // returns project description
	staticFS      fs.FS                    // embedded web UI (nil when built with -tags noui)
}

type extraHandler struct {
	pattern string
	handler http.HandlerFunc
}

// New creates a ControlPlane backed by the given Store.
func New(store Store) *ControlPlane {
	return &ControlPlane{store: store}
}

// Start begins serving the control plane API on the given address (e.g. ":8377").
// It returns immediately; call Shutdown to stop.
func (cp *ControlPlane) Start(addr string) error {
	cp.server = &http.Server{
		Addr:         addr,
		Handler:      cp.buildMux(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("controlplane: listen %s: %w", addr, err)
	}

	go func() {
		slog.Info("control plane listening", "addr", addr)
		if err := cp.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("control plane serve error", "error", err)
		}
	}()

	return nil
}

// Shutdown gracefully stops the HTTP server.
func (cp *ControlPlane) Shutdown(ctx context.Context) error {
	if cp.server != nil {
		return cp.server.Shutdown(ctx)
	}
	return nil
}

// Handler returns the HTTP handler for testing without starting a server.
func (cp *ControlPlane) Handler() http.Handler {
	return cp.buildMux()
}

// RegisterHandler adds an extra HTTP handler to the control plane's mux.
// Must be called before Start.
func (cp *ControlPlane) RegisterHandler(pattern string, handler http.HandlerFunc) {
	cp.extraHandlers = append(cp.extraHandlers, extraHandler{pattern, handler})
}

// SetConfigReader sets a function that returns a project description by name.
func (cp *ControlPlane) SetConfigReader(fn func(name string) string) {
	cp.configReader = fn
}

// SetStaticFS sets the embedded filesystem for serving the web UI.
func (cp *ControlPlane) SetStaticFS(fsys fs.FS) {
	cp.staticFS = fsys
}

// Store returns the underlying Store for direct access (e.g. project resolution).
func (cp *ControlPlane) Store() Store {
	return cp.store
}

func (cp *ControlPlane) buildMux() http.Handler {
	mux := http.NewServeMux()

	// Task endpoints.
	mux.HandleFunc("GET /api/tasks", cp.handleListTasks)
	mux.HandleFunc("POST /api/tasks", cp.handleCreateTask)
	mux.HandleFunc("GET /api/tasks/{id}", cp.handleGetTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", cp.handleUpdateTask)
	mux.HandleFunc("POST /api/tasks/{id}/claim", cp.handleClaimTask)
	mux.HandleFunc("POST /api/tasks/{id}/release", cp.handleReleaseTask)
	mux.HandleFunc("POST /api/tasks/{id}/complete", cp.handleCompleteTask)
	mux.HandleFunc("POST /api/tasks/next", cp.handleClaimNextTask)

	// Message endpoints.
	mux.HandleFunc("GET /api/messages", cp.handleListMessages)
	mux.HandleFunc("POST /api/messages", cp.handlePostMessage)

	// Project endpoints.
	mux.HandleFunc("GET /api/projects", cp.handleListProjects)
	mux.HandleFunc("POST /api/projects", cp.handleRegisterProject)
	mux.HandleFunc("GET /api/projects/{name}", cp.handleGetProject)
	mux.HandleFunc("DELETE /api/projects/{name}", cp.handleUnregisterProject)
	mux.HandleFunc("POST /api/projects/{name}/start", cp.handleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", cp.handleStopProject)
	mux.HandleFunc("GET /api/projects/{name}/status", cp.handleProjectStatus)

	// Health endpoint at both /api/health and /health (bare for daemon startup checks).
	mux.HandleFunc("GET /api/health", cp.handleHealth)
	mux.HandleFunc("GET /health", cp.handleHealth)

	// Extra handlers registered by the daemon.
	for _, h := range cp.extraHandlers {
		mux.HandleFunc(h.pattern, h.handler)
	}

	// Serve embedded web UI if available.
	if cp.staticFS != nil {
		sub, err := fs.Sub(cp.staticFS, "out")
		if err == nil {
			fileServer := http.FileServer(http.FS(sub))
			mux.Handle("GET /", fileServer)
		}
	}

	return corsMiddleware(mux)
}

// corsMiddleware wraps an http.Handler with CORS headers.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// --- Task Handlers ---

func (cp *ControlPlane) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (cp *ControlPlane) handleListTasks(w http.ResponseWriter, r *http.Request) {
	filter := TaskFilter{
		Status:       r.URL.Query().Get("status"),
		RoleAffinity: r.URL.Query().Get("role"),
		Project:      r.URL.Query().Get("project"),
	}

	cp.mu.Lock()
	tasks, err := cp.store.ListTasks(r.Context(), filter)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (cp *ControlPlane) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	cp.mu.Lock()
	task, err := cp.store.GetTask(r.Context(), id)
	cp.mu.Unlock()

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
	Repo         string   `json:"repo"`
	Branch       string   `json:"branch"`
	DependsOn    []string `json:"depends_on"`
}

func (cp *ControlPlane) handleCreateTask(w http.ResponseWriter, r *http.Request) {
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
		Repo:         req.Repo,
		Branch:       req.Branch,
		DependsOn:    req.DependsOn,
		CreatedAt:    time.Now().UTC(),
	}

	cp.mu.Lock()
	err := cp.store.CreateTask(r.Context(), task)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusCreated, task)
}

type claimRequest struct {
	AgentID int `json:"agent_id"`
}

func (cp *ControlPlane) handleClaimTask(w http.ResponseWriter, r *http.Request) {
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

	cp.mu.Lock()
	err := cp.store.ClaimTask(r.Context(), id, req.AgentID)
	cp.mu.Unlock()

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

func (cp *ControlPlane) handleReleaseTask(w http.ResponseWriter, r *http.Request) {
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

	cp.mu.Lock()
	err := cp.store.ReleaseTask(r.Context(), id, req.AgentID)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "released"})
}

type completeRequest struct {
	Summary string `json:"summary"`
}

func (cp *ControlPlane) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	cp.mu.Lock()
	err := cp.store.CompleteTask(r.Context(), id, req.Summary)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
}

type claimNextRequest struct {
	AgentID int    `json:"agent_id"`
	Role    string `json:"role"`
	Project string `json:"project"`
}

func (cp *ControlPlane) handleClaimNextTask(w http.ResponseWriter, r *http.Request) {
	var req claimNextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.AgentID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("agent_id is required"))
		return
	}

	cp.mu.Lock()
	task, err := cp.store.ClaimNextTask(r.Context(), req.AgentID, req.Role, req.Project)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (cp *ControlPlane) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var updates TaskUpdate
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	cp.mu.Lock()
	err := cp.store.UpdateTask(r.Context(), id, updates)
	cp.mu.Unlock()

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

func (cp *ControlPlane) handlePostMessage(w http.ResponseWriter, r *http.Request) {
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

	cp.mu.Lock()
	err := cp.store.PostMessage(r.Context(), msg)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "sent"})
}

func (cp *ControlPlane) handleListMessages(w http.ResponseWriter, r *http.Request) {
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

	cp.mu.Lock()
	msgs, err := cp.store.ListMessages(r.Context(), since, project)
	cp.mu.Unlock()

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

func (cp *ControlPlane) handleListProjects(w http.ResponseWriter, r *http.Request) {
	cp.mu.Lock()
	projects, err := cp.store.ListProjects(r.Context())
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	details := make([]ProjectDetail, 0, len(projects))
	for _, p := range projects {
		details = append(details, cp.enrichProject(r.Context(), p))
	}
	writeJSON(w, http.StatusOK, details)
}

type registerProjectRequest struct {
	Name     string `json:"name"`
	RepoPath string `json:"repo_path"`
	Label    string `json:"label"`
}

func (cp *ControlPlane) handleRegisterProject(w http.ResponseWriter, r *http.Request) {
	var req registerProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}

	cp.mu.Lock()
	err := cp.store.RegisterProject(r.Context(), req.Name)
	if err == nil && req.RepoPath != "" {
		err = cp.store.AddProjectRepo(r.Context(), req.Name, req.RepoPath, req.Label)
	}
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

func (cp *ControlPlane) handleGetProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cp.mu.Lock()
	project, err := cp.store.GetProject(r.Context(), name)
	cp.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, cp.enrichProject(r.Context(), *project))
}

func (cp *ControlPlane) handleUnregisterProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cp.mu.Lock()
	err := cp.store.UnregisterProject(r.Context(), name)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

func (cp *ControlPlane) handleStartProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cp.mu.Lock()
	err := cp.store.UpdateProjectStatus(r.Context(), name, "running")
	cp.mu.Unlock()

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

func (cp *ControlPlane) handleStopProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cp.mu.Lock()
	err := cp.store.UpdateProjectStatus(r.Context(), name, "stopped")
	cp.mu.Unlock()

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

func (cp *ControlPlane) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cp.mu.Lock()
	project, err := cp.store.GetProject(r.Context(), name)
	cp.mu.Unlock()

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

// enrichProject builds a ProjectDetail from a ProjectInfo by querying repos, task counts,
// and reading project description from config.
func (cp *ControlPlane) enrichProject(ctx context.Context, p ProjectInfo) ProjectDetail {
	detail := ProjectDetail{
		Name:      p.Name,
		Status:    p.Status,
		CreatedAt: p.CreatedAt,
	}

	cp.mu.Lock()
	repos, _ := cp.store.ListProjectRepos(ctx, p.Name)
	taskCount, _ := cp.store.CountTasksByProject(ctx, p.Name)
	cp.mu.Unlock()

	if repos == nil {
		repos = []ProjectRepo{}
	}
	detail.Repos = repos
	detail.TaskCount = taskCount

	if cp.configReader != nil {
		detail.Description = cp.configReader(p.Name)
	}

	return detail
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

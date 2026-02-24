package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// --- Epic Handlers ---

func (cp *ControlPlane) handleCreateEpic(w http.ResponseWriter, r *http.Request) {
	if cp.epicService == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("epic decomposition requires an Anthropic API key"))
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Project     string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("title is required"))
		return
	}

	// Look up project description for LLM context.
	var projectDesc string
	if cp.configReader != nil && req.Project != "" {
		projectDesc = cp.configReader(req.Project)
	}

	cp.mu.Lock()
	epic, tasks, err := cp.epicService.DecomposeEpic(r.Context(), req.Title, req.Description, req.Project, projectDesc)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"epic":  epic,
		"tasks": tasks,
	})
}

func (cp *ControlPlane) handleListEpics(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")

	cp.mu.Lock()
	epics, err := cp.store.ListEpics(r.Context(), project)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if epics == nil {
		epics = []Epic{}
	}
	writeJSON(w, http.StatusOK, epics)
}

func (cp *ControlPlane) handleGetEpic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	cp.mu.Lock()
	epic, err := cp.store.GetEpic(r.Context(), id)
	cp.mu.Unlock()

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, epic)
}

func (cp *ControlPlane) handleListEpicTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	cp.mu.Lock()
	tasks, err := cp.store.ListTasksByEpic(r.Context(), id)
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

func (cp *ControlPlane) handleApproveEpic(w http.ResponseWriter, r *http.Request) {
	if cp.epicService == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("epic service not available"))
		return
	}

	id := r.PathValue("id")

	cp.mu.Lock()
	err := cp.epicService.ApproveEpic(r.Context(), id)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (cp *ControlPlane) handleApproveTaskPrompt(w http.ResponseWriter, r *http.Request) {
	if cp.epicService == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("epic service not available"))
		return
	}

	id := r.PathValue("id")

	cp.mu.Lock()
	err := cp.epicService.ApproveTask(r.Context(), id)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (cp *ControlPlane) handleRejectTaskPrompt(w http.ResponseWriter, r *http.Request) {
	if cp.epicService == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("epic service not available"))
		return
	}

	id := r.PathValue("id")

	cp.mu.Lock()
	err := cp.epicService.RejectTask(r.Context(), id)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

func (cp *ControlPlane) handleRegenerateTaskPrompt(w http.ResponseWriter, r *http.Request) {
	if cp.epicService == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("epic service not available"))
		return
	}

	id := r.PathValue("id")

	cp.mu.Lock()
	err := cp.epicService.RegenerateTaskPrompt(r.Context(), id)
	cp.mu.Unlock()

	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "regenerated"})
}

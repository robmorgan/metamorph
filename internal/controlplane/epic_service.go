package controlplane

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robmorgan/metamorph/internal/llm"
)

// EpicService orchestrates epic decomposition and prompt expansion using an LLM.
type EpicService struct {
	store Store
	llm   *llm.Client
}

// NewEpicService creates a new EpicService.
func NewEpicService(store Store, llmClient *llm.Client) *EpicService {
	return &EpicService{store: store, llm: llmClient}
}

// DecomposeEpic creates an epic, decomposes it into tasks via LLM, expands each
// task's prompt, and stores everything. Tasks are created with prompt_status=pending_review.
func (es *EpicService) DecomposeEpic(ctx context.Context, title, desc, project, projectDesc string) (*Epic, []Task, error) {
	epicID := fmt.Sprintf("epic-%d", time.Now().UnixNano())

	epic := Epic{
		ID:          epicID,
		Title:       title,
		Description: desc,
		Status:      "decomposing",
		Project:     project,
		CreatedAt:   time.Now().UTC(),
	}

	if err := es.store.CreateEpic(ctx, epic); err != nil {
		return nil, nil, fmt.Errorf("epic service: create epic: %w", err)
	}

	// Call LLM to decompose.
	decomposed, err := es.llm.DecomposeEpic(ctx, title, desc, projectDesc)
	if err != nil {
		// Roll back epic status on failure.
		_ = es.store.UpdateEpicStatus(ctx, epicID, "open")
		return nil, nil, fmt.Errorf("epic service: decompose: %w", err)
	}

	// Create tasks from decomposition.
	tasks := make([]Task, len(decomposed))
	taskIDs := make([]string, len(decomposed))

	for i, dt := range decomposed {
		taskID := fmt.Sprintf("task-%d", time.Now().UnixNano()+int64(i))
		taskIDs[i] = taskID
		tasks[i] = Task{
			ID:           taskID,
			Title:        dt.Title,
			Description:  dt.Description,
			Status:       "open",
			Priority:     dt.Priority,
			RoleAffinity: dt.RoleAffinity,
			Project:      project,
			EpicID:       epicID,
			PromptStatus: "pending_review",
			CreatedAt:    time.Now().UTC(),
		}
	}

	// Resolve depends_on indices to task IDs.
	for i, dt := range decomposed {
		for _, depIdx := range dt.DependsOn {
			if depIdx >= 0 && depIdx < len(taskIDs) && depIdx != i {
				tasks[i].DependsOn = append(tasks[i].DependsOn, taskIDs[depIdx])
			}
		}
	}

	// Expand prompts in parallel (bounded to 3 concurrent).
	epicContext := fmt.Sprintf("%s: %s", title, desc)
	es.expandPromptsParallel(ctx, tasks, epicContext, projectDesc, 3)

	// Persist all tasks.
	for i := range tasks {
		if err := es.store.CreateTask(ctx, tasks[i]); err != nil {
			return nil, nil, fmt.Errorf("epic service: create task %d: %w", i, err)
		}
	}

	// Move epic to review status.
	if err := es.store.UpdateEpicStatus(ctx, epicID, "review"); err != nil {
		return nil, nil, fmt.Errorf("epic service: update epic to review: %w", err)
	}
	epic.Status = "review"

	return &epic, tasks, nil
}

// expandPromptsParallel expands prompts for all tasks using bounded concurrency.
func (es *EpicService) expandPromptsParallel(ctx context.Context, tasks []Task, epicContext, projectDesc string, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			expanded, err := es.llm.ExpandTaskPrompt(ctx,
				tasks[idx].Title,
				tasks[idx].Description,
				epicContext,
				projectDesc,
			)
			if err == nil {
				tasks[idx].ExpandedPrompt = expanded
			}
			// On error, leave ExpandedPrompt empty; user can regenerate later.
		}(i)
	}

	wg.Wait()
}

// ApproveTask sets a task's prompt_status to "approved".
func (es *EpicService) ApproveTask(ctx context.Context, taskID string) error {
	status := "approved"
	return es.store.UpdateTask(ctx, taskID, TaskUpdate{PromptStatus: &status})
}

// ApproveEpic bulk-approves all pending_review tasks in an epic and sets the epic to "active".
func (es *EpicService) ApproveEpic(ctx context.Context, epicID string) error {
	tasks, err := es.store.ListTasksByEpic(ctx, epicID)
	if err != nil {
		return fmt.Errorf("epic service: list tasks for approval: %w", err)
	}

	approved := "approved"
	for _, t := range tasks {
		if t.PromptStatus == "pending_review" {
			if err := es.store.UpdateTask(ctx, t.ID, TaskUpdate{PromptStatus: &approved}); err != nil {
				return fmt.Errorf("epic service: approve task %s: %w", t.ID, err)
			}
		}
	}

	return es.store.UpdateEpicStatus(ctx, epicID, "active")
}

// RejectTask sets a task's prompt_status to "rejected".
func (es *EpicService) RejectTask(ctx context.Context, taskID string) error {
	status := "rejected"
	return es.store.UpdateTask(ctx, taskID, TaskUpdate{PromptStatus: &status})
}

// RegenerateTaskPrompt re-expands a task's prompt via LLM and resets it to pending_review.
func (es *EpicService) RegenerateTaskPrompt(ctx context.Context, taskID string) error {
	task, err := es.store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("epic service: get task: %w", err)
	}

	var epicContext string
	if task.EpicID != "" {
		epic, err := es.store.GetEpic(ctx, task.EpicID)
		if err == nil {
			epicContext = fmt.Sprintf("%s: %s", epic.Title, epic.Description)
		}
	}

	expanded, err := es.llm.ExpandTaskPrompt(ctx, task.Title, task.Description, epicContext, "")
	if err != nil {
		return fmt.Errorf("epic service: regenerate prompt: %w", err)
	}

	status := "pending_review"
	return es.store.UpdateTask(ctx, taskID, TaskUpdate{
		ExpandedPrompt: &expanded,
		PromptStatus:   &status,
	})
}

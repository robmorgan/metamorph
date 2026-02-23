package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// testStore creates a temporary SQLiteStore for tests.
func testStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// --- Store Tests ---

func TestCreateAndGetTask(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	task := Task{
		ID:          "task-1",
		Title:       "Implement auth",
		Description: "Add JWT authentication",
		Priority:    1,
	}

	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if got.ID != "task-1" {
		t.Errorf("ID = %q, want task-1", got.ID)
	}
	if got.Title != "Implement auth" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want open", got.Status)
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	_, err := store.GetTask(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestListTasksFilter(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "A", Status: "open", Priority: 2})
	store.CreateTask(ctx, Task{ID: "t2", Title: "B", Status: "open", Priority: 1, RoleAffinity: "tester"})
	store.CreateTask(ctx, Task{ID: "t3", Title: "C", Status: "done", Priority: 0})

	// All open tasks.
	tasks, err := store.ListTasks(ctx, TaskFilter{Status: "open"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 open tasks, got %d", len(tasks))
	}
	// Should be ordered by priority: t2 (1) before t1 (2).
	if tasks[0].ID != "t2" {
		t.Errorf("first task = %q, want t2 (lower priority)", tasks[0].ID)
	}

	// Filter by role.
	tasks, err = store.ListTasks(ctx, TaskFilter{RoleAffinity: "tester"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t2" {
		t.Errorf("role filter: got %d tasks", len(tasks))
	}
}

func TestClaimTaskAtomic(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "Work"})

	// First claim succeeds.
	if err := store.ClaimTask(ctx, "t1", 1); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	// Second claim fails (already claimed).
	if err := store.ClaimTask(ctx, "t1", 2); err == nil {
		t.Fatal("expected error on double claim")
	}

	// Verify state.
	task, _ := store.GetTask(ctx, "t1")
	if task.Status != "claimed" {
		t.Errorf("Status = %q, want claimed", task.Status)
	}
	if task.Assignee == nil || *task.Assignee != 1 {
		t.Errorf("Assignee = %v, want 1", task.Assignee)
	}
}

func TestClaimConcurrent(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "race-task", Title: "Race"})

	const numAgents = 10
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		winners  []int
		claimErr []error
	)

	for i := 1; i <= numAgents; i++ {
		wg.Add(1)
		go func(agentID int) {
			defer wg.Done()
			err := store.ClaimTask(ctx, "race-task", agentID)
			mu.Lock()
			if err == nil {
				winners = append(winners, agentID)
			} else {
				claimErr = append(claimErr, err)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(winners) != 1 {
		t.Errorf("expected exactly 1 winner, got %d: %v", len(winners), winners)
	}
	if len(claimErr) != numAgents-1 {
		t.Errorf("expected %d failures, got %d", numAgents-1, len(claimErr))
	}
}

func TestReleaseTask(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "Work"})
	store.ClaimTask(ctx, "t1", 1)

	// Wrong agent can't release.
	if err := store.ReleaseTask(ctx, "t1", 2); err == nil {
		t.Fatal("expected error when wrong agent releases")
	}

	// Correct agent releases.
	if err := store.ReleaseTask(ctx, "t1", 1); err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}

	task, _ := store.GetTask(ctx, "t1")
	if task.Status != "open" {
		t.Errorf("Status = %q, want open", task.Status)
	}
	if task.Assignee != nil {
		t.Errorf("Assignee = %v, want nil", task.Assignee)
	}
}

func TestCompleteTask(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "Work"})
	store.ClaimTask(ctx, "t1", 1)

	if err := store.CompleteTask(ctx, "t1", "All tests pass"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	task, _ := store.GetTask(ctx, "t1")
	if task.Status != "done" {
		t.Errorf("Status = %q, want done", task.Status)
	}
	if task.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
	if task.Notes != "All tests pass" {
		t.Errorf("Notes = %q, want 'All tests pass'", task.Notes)
	}
}

func TestCompleteUnclaimed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "Work"})

	if err := store.CompleteTask(ctx, "t1", "done"); err == nil {
		t.Fatal("expected error completing unclaimed task")
	}
}

func TestUpdateTask(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "Old", Priority: 2})

	newTitle := "New Title"
	newPriority := 0
	if err := store.UpdateTask(ctx, "t1", TaskUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	task, _ := store.GetTask(ctx, "t1")
	if task.Title != "New Title" {
		t.Errorf("Title = %q", task.Title)
	}
	if task.Priority != 0 {
		t.Errorf("Priority = %d, want 0", task.Priority)
	}
}

func TestMessages(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	before := time.Now().UTC().Add(-time.Second)

	store.PostMessage(ctx, Message{FromAgent: 1, Text: "I'm blocked on auth"})
	store.PostMessage(ctx, Message{FromAgent: 2, TaskID: "t1", Text: "Working on it"})

	msgs, err := store.ListMessages(ctx, before)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "I'm blocked on auth" {
		t.Errorf("msg[0].Text = %q", msgs[0].Text)
	}
}

func TestDependsOnRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{
		ID:        "t2",
		Title:     "Depends on t1",
		DependsOn: []string{"t1", "t3"},
	})

	task, _ := store.GetTask(ctx, "t2")
	if len(task.DependsOn) != 2 {
		t.Fatalf("DependsOn = %v, want [t1 t3]", task.DependsOn)
	}
	if task.DependsOn[0] != "t1" || task.DependsOn[1] != "t3" {
		t.Errorf("DependsOn = %v", task.DependsOn)
	}
}

// --- HTTP Handler Tests ---

func testServer(t *testing.T) (*Broker, *httptest.Server) {
	t.Helper()
	store := testStore(t)
	b := New(store)
	ts := httptest.NewServer(b.Handler())
	t.Cleanup(ts.Close)
	return b, ts
}

func TestHTTPCreateAndList(t *testing.T) {
	_, ts := testServer(t)

	// Create a task.
	body := `{"id":"t1","title":"Build API","priority":1}`
	resp, err := http.Post(ts.URL+"/tasks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /tasks status = %d, want 201", resp.StatusCode)
	}

	// List tasks.
	resp, err = http.Get(ts.URL + "/tasks")
	if err != nil {
		t.Fatalf("GET /tasks: %v", err)
	}

	var tasks []Task
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Build API" {
		t.Errorf("Title = %q", tasks[0].Title)
	}
}

func TestHTTPClaimConflict(t *testing.T) {
	_, ts := testServer(t)

	// Create task.
	body := `{"id":"t1","title":"Work"}`
	http.Post(ts.URL+"/tasks", "application/json", bytes.NewBufferString(body))

	// First claim.
	claimBody := `{"agent_id":1}`
	resp, _ := http.Post(ts.URL+"/tasks/t1/claim", "application/json", bytes.NewBufferString(claimBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first claim: status = %d", resp.StatusCode)
	}

	// Second claim → 409.
	claimBody2 := `{"agent_id":2}`
	resp, _ = http.Post(ts.URL+"/tasks/t1/claim", "application/json", bytes.NewBufferString(claimBody2))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second claim: status = %d, want 409", resp.StatusCode)
	}
}

func TestHTTPTaskLifecycle(t *testing.T) {
	_, ts := testServer(t)

	// Create.
	http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Full lifecycle"}`))

	// Claim.
	http.Post(ts.URL+"/tasks/t1/claim", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))

	// Complete.
	resp, _ := http.Post(ts.URL+"/tasks/t1/complete", "application/json",
		bytes.NewBufferString(`{"summary":"All done"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete: status = %d", resp.StatusCode)
	}

	// Get and verify done.
	resp, _ = http.Get(ts.URL + "/tasks/t1")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Status != "done" {
		t.Errorf("Status = %q, want done", task.Status)
	}
}

func TestHTTPRelease(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Release test"}`))
	http.Post(ts.URL+"/tasks/t1/claim", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))

	resp, _ := http.Post(ts.URL+"/tasks/t1/release", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release: status = %d", resp.StatusCode)
	}

	// Task should be open again.
	resp, _ = http.Get(ts.URL + "/tasks/t1")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Status != "open" {
		t.Errorf("Status = %q, want open after release", task.Status)
	}
}

func TestHTTPMessages(t *testing.T) {
	_, ts := testServer(t)

	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)

	// Post message.
	body := `{"from_agent":1,"text":"Need help with auth"}`
	resp, _ := http.Post(ts.URL+"/messages", "application/json", bytes.NewBufferString(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /messages status = %d", resp.StatusCode)
	}

	// List messages.
	resp, _ = http.Get(ts.URL + "/messages?since=" + since)
	var msgs []Message
	json.NewDecoder(resp.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Text != "Need help with auth" {
		t.Errorf("Text = %q", msgs[0].Text)
	}
}

func TestHTTPUpdateTask(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Original"}`))

	req, _ := http.NewRequest("PATCH", ts.URL+"/tasks/t1", bytes.NewBufferString(`{"title":"Updated"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/tasks/t1")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Title != "Updated" {
		t.Errorf("Title = %q, want Updated", task.Title)
	}
}

func TestHTTPHealth(t *testing.T) {
	_, ts := testServer(t)

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHTTPGetTaskNotFound(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Get(ts.URL + "/tasks/nonexistent")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHTTPCreateMissingTitle(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPFilterByStatus(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Open task"}`))
	http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t2","title":"Claimable"}`))
	http.Post(ts.URL+"/tasks/t2/claim", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))

	resp, _ := http.Get(ts.URL + "/tasks?status=open")
	var tasks []Task
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 open task, got %d", len(tasks))
	}
	if tasks[0].ID != "t1" {
		t.Errorf("expected t1, got %q", tasks[0].ID)
	}
}

func TestHTTPAutoGenerateID(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"title":"Auto ID"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.ID == "" {
		t.Error("expected auto-generated ID")
	}
}

package controlplane

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

	store.RegisterProject(ctx, "my-app")

	task := Task{
		ID:          "task-1",
		Title:       "Implement auth",
		Description: "Add JWT authentication",
		Priority:    1,
		Project:     "my-app",
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
	if got.Project != "my-app" {
		t.Errorf("Project = %q, want my-app", got.Project)
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

	store.RegisterProject(ctx, "proj-a")
	store.RegisterProject(ctx, "proj-b")

	store.CreateTask(ctx, Task{ID: "t1", Title: "A", Status: "open", Priority: 2, Project: "proj-a"})
	store.CreateTask(ctx, Task{ID: "t2", Title: "B", Status: "open", Priority: 1, RoleAffinity: "tester", Project: "proj-a"})
	store.CreateTask(ctx, Task{ID: "t3", Title: "C", Status: "done", Priority: 0, Project: "proj-b"})

	// All open tasks.
	tasks, err := store.ListTasks(ctx, TaskFilter{Status: "open"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 open tasks, got %d", len(tasks))
	}
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

	// Filter by project.
	tasks, err = store.ListTasks(ctx, TaskFilter{Project: "proj-a"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("project filter: expected 2 tasks for proj-a, got %d", len(tasks))
	}

	tasks, err = store.ListTasks(ctx, TaskFilter{Project: "proj-b"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t3" {
		t.Errorf("project filter proj-b: got %d tasks", len(tasks))
	}
}

func TestClaimTaskAtomic(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.CreateTask(ctx, Task{ID: "t1", Title: "Work"})

	if err := store.ClaimTask(ctx, "t1", 1); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	if err := store.ClaimTask(ctx, "t1", 2); err == nil {
		t.Fatal("expected error on double claim")
	}

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

	if err := store.ReleaseTask(ctx, "t1", 2); err == nil {
		t.Fatal("expected error when wrong agent releases")
	}

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

	store.PostMessage(ctx, Message{FromAgent: 1, Text: "I'm blocked on auth", Project: "proj-a"})
	store.PostMessage(ctx, Message{FromAgent: 2, TaskID: "t1", Text: "Working on it", Project: "proj-a"})

	msgs, err := store.ListMessages(ctx, before, "")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "I'm blocked on auth" {
		t.Errorf("msg[0].Text = %q", msgs[0].Text)
	}

	// Filter by project.
	msgs, err = store.ListMessages(ctx, before, "proj-a")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages for proj-a, got %d", len(msgs))
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

// --- Project Registry Tests ---

func TestProjectRegistry(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	// Register projects.
	if err := store.RegisterProject(ctx, "proj-a"); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := store.RegisterProject(ctx, "proj-b"); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// List.
	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}

	// Get.
	p, err := store.GetProject(ctx, "proj-a")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Status != "stopped" {
		t.Errorf("Status = %q, want stopped", p.Status)
	}

	// Update status.
	if err := store.UpdateProjectStatus(ctx, "proj-a", "running"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}
	p, _ = store.GetProject(ctx, "proj-a")
	if p.Status != "running" {
		t.Errorf("Status = %q, want running", p.Status)
	}

	// Add repo and lookup.
	if err := store.AddProjectRepo(ctx, "proj-a", "/home/user/proj-a", ""); err != nil {
		t.Fatalf("AddProjectRepo: %v", err)
	}
	name, err := store.GetProjectByRepoPath(ctx, "/home/user/proj-a")
	if err != nil {
		t.Fatalf("GetProjectByRepoPath: %v", err)
	}
	if name != "proj-a" {
		t.Errorf("project = %q, want proj-a", name)
	}

	// Unknown path.
	_, err = store.GetProjectByRepoPath(ctx, "/unknown/path")
	if err == nil {
		t.Fatal("expected error for unknown repo path")
	}

	// Unregister.
	if err := store.UnregisterProject(ctx, "proj-a"); err != nil {
		t.Fatalf("UnregisterProject: %v", err)
	}
	projects, _ = store.ListProjects(ctx)
	if len(projects) != 1 {
		t.Errorf("expected 1 project after unregister, got %d", len(projects))
	}
}

func TestScopedTasks(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.RegisterProject(ctx, "proj-a")
	store.RegisterProject(ctx, "proj-b")

	store.CreateTask(ctx, Task{ID: "a1", Title: "Task A", Project: "proj-a"})
	store.CreateTask(ctx, Task{ID: "b1", Title: "Task B", Project: "proj-b"})

	// Tasks from proj-a not visible in proj-b queries.
	tasks, _ := store.ListTasks(ctx, TaskFilter{Project: "proj-a"})
	if len(tasks) != 1 || tasks[0].ID != "a1" {
		t.Errorf("proj-a tasks: got %d tasks", len(tasks))
	}

	tasks, _ = store.ListTasks(ctx, TaskFilter{Project: "proj-b"})
	if len(tasks) != 1 || tasks[0].ID != "b1" {
		t.Errorf("proj-b tasks: got %d tasks", len(tasks))
	}

	// Unscoped lists all.
	tasks, _ = store.ListTasks(ctx, TaskFilter{})
	if len(tasks) != 2 {
		t.Errorf("unscoped tasks: expected 2, got %d", len(tasks))
	}
}

// --- HTTP Handler Tests ---

func testServer(t *testing.T) (*ControlPlane, *httptest.Server) {
	t.Helper()
	store := testStore(t)
	cp := New(store)
	ts := httptest.NewServer(cp.Handler())
	t.Cleanup(ts.Close)
	return cp, ts
}

func TestHTTPCreateAndList(t *testing.T) {
	_, ts := testServer(t)

	// Register project first (FK constraint).
	http.Post(ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"name":"my-app"}`))

	body := `{"id":"t1","title":"Build API","priority":1,"project":"my-app"}`
	resp, err := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /api/tasks: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/tasks status = %d, want 201", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/tasks?project=my-app")
	if err != nil {
		t.Fatalf("GET /api/tasks: %v", err)
	}

	var tasks []Task
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Build API" {
		t.Errorf("Title = %q", tasks[0].Title)
	}
	if tasks[0].Project != "my-app" {
		t.Errorf("Project = %q", tasks[0].Project)
	}
}

func TestHTTPClaimConflict(t *testing.T) {
	_, ts := testServer(t)

	body := `{"id":"t1","title":"Work"}`
	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))

	claimBody := `{"agent_id":1}`
	resp, _ := http.Post(ts.URL+"/api/tasks/t1/claim", "application/json", bytes.NewBufferString(claimBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first claim: status = %d", resp.StatusCode)
	}

	claimBody2 := `{"agent_id":2}`
	resp, _ = http.Post(ts.URL+"/api/tasks/t1/claim", "application/json", bytes.NewBufferString(claimBody2))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second claim: status = %d, want 409", resp.StatusCode)
	}
}

func TestHTTPTaskLifecycle(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Full lifecycle"}`))
	http.Post(ts.URL+"/api/tasks/t1/claim", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))

	resp, _ := http.Post(ts.URL+"/api/tasks/t1/complete", "application/json",
		bytes.NewBufferString(`{"summary":"All done"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete: status = %d", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/api/tasks/t1")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Status != "done" {
		t.Errorf("Status = %q, want done", task.Status)
	}
}

func TestHTTPRelease(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Release test"}`))
	http.Post(ts.URL+"/api/tasks/t1/claim", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))

	resp, _ := http.Post(ts.URL+"/api/tasks/t1/release", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release: status = %d", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/api/tasks/t1")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Status != "open" {
		t.Errorf("Status = %q, want open after release", task.Status)
	}
}

func TestHTTPMessages(t *testing.T) {
	_, ts := testServer(t)

	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)

	body := `{"from_agent":1,"text":"Need help with auth"}`
	resp, _ := http.Post(ts.URL+"/api/messages", "application/json", bytes.NewBufferString(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/messages status = %d", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/api/messages?since=" + since)
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

	http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Original"}`))

	req, _ := http.NewRequest("PATCH", ts.URL+"/api/tasks/t1", bytes.NewBufferString(`{"title":"Updated"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/api/tasks/t1")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Title != "Updated" {
		t.Errorf("Title = %q, want Updated", task.Title)
	}
}

func TestHTTPHealth(t *testing.T) {
	_, ts := testServer(t)

	// Test bare /health endpoint (used by daemon startup checks).
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Test /api/health endpoint.
	resp, err = http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHTTPGetTaskNotFound(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Get(ts.URL + "/api/tasks/nonexistent")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHTTPCreateMissingTitle(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPFilterByStatus(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Open task"}`))
	http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t2","title":"Claimable"}`))
	http.Post(ts.URL+"/api/tasks/t2/claim", "application/json",
		bytes.NewBufferString(`{"agent_id":1}`))

	resp, _ := http.Get(ts.URL + "/api/tasks?status=open")
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

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json",
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

// --- ClaimNextTask Store Tests ---

func TestClaimNextTaskPriority(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.RegisterProject(ctx, "proj")
	store.CreateTask(ctx, Task{ID: "low", Title: "Low", Priority: 4, Project: "proj"})
	store.CreateTask(ctx, Task{ID: "high", Title: "High", Priority: 0, Project: "proj"})
	store.CreateTask(ctx, Task{ID: "med", Title: "Med", Priority: 2, Project: "proj"})

	task, err := store.ClaimNextTask(ctx, 1, "developer", "proj")
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.ID != "high" {
		t.Errorf("expected highest priority task 'high', got %q", task.ID)
	}
	if task.Status != "claimed" {
		t.Errorf("Status = %q, want claimed", task.Status)
	}
	if task.Assignee == nil || *task.Assignee != 1 {
		t.Errorf("Assignee = %v, want 1", task.Assignee)
	}
}

func TestClaimNextTaskRoleAffinity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.RegisterProject(ctx, "proj")
	// Both priority 2 but different role affinities.
	store.CreateTask(ctx, Task{ID: "dev-task", Title: "Dev", Priority: 2, RoleAffinity: "developer", Project: "proj"})
	store.CreateTask(ctx, Task{ID: "test-task", Title: "Test", Priority: 2, RoleAffinity: "tester", Project: "proj"})

	// A tester agent should prefer the tester-affinity task.
	task, err := store.ClaimNextTask(ctx, 1, "tester", "proj")
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if task.ID != "test-task" {
		t.Errorf("expected tester task, got %q", task.ID)
	}
}

func TestClaimNextTaskProjectScope(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.RegisterProject(ctx, "proj-a")
	store.RegisterProject(ctx, "proj-b")
	store.CreateTask(ctx, Task{ID: "a1", Title: "A", Priority: 0, Project: "proj-a"})
	store.CreateTask(ctx, Task{ID: "b1", Title: "B", Priority: 0, Project: "proj-b"})

	task, err := store.ClaimNextTask(ctx, 1, "developer", "proj-b")
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task")
	}
	if task.ID != "b1" {
		t.Errorf("expected b1, got %q", task.ID)
	}
}

func TestClaimNextTaskDependencies(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.RegisterProject(ctx, "proj")
	store.CreateTask(ctx, Task{ID: "dep", Title: "Dependency", Priority: 2, Project: "proj"})
	store.CreateTask(ctx, Task{ID: "blocked", Title: "Blocked", Priority: 0, DependsOn: []string{"dep"}, Project: "proj"})
	store.CreateTask(ctx, Task{ID: "free", Title: "Free", Priority: 1, Project: "proj"})

	// Should skip "blocked" (higher priority) because its dep is not done.
	task, err := store.ClaimNextTask(ctx, 1, "developer", "proj")
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if task.ID != "free" {
		t.Errorf("expected 'free', got %q", task.ID)
	}

	// Complete the dependency.
	store.ClaimTask(ctx, "dep", 2)
	store.CompleteTask(ctx, "dep", "done")

	// Now "blocked" should be claimable (priority 0 < priority 1).
	task, err = store.ClaimNextTask(ctx, 3, "developer", "proj")
	if err != nil {
		t.Fatalf("ClaimNextTask after dep done: %v", err)
	}
	if task.ID != "blocked" {
		t.Errorf("expected 'blocked' after dep done, got %q", task.ID)
	}
}

func TestClaimNextTaskEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	task, err := store.ClaimNextTask(ctx, 1, "developer", "proj")
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if task != nil {
		t.Errorf("expected nil, got task %q", task.ID)
	}
}

func TestClaimNextTaskConcurrent(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	store.RegisterProject(ctx, "proj")
	store.CreateTask(ctx, Task{ID: "race", Title: "Race", Priority: 0, Project: "proj"})

	const numAgents = 10
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []*Task
	)

	for i := 1; i <= numAgents; i++ {
		wg.Add(1)
		go func(agentID int) {
			defer wg.Done()
			task, err := store.ClaimNextTask(ctx, agentID, "developer", "proj")
			if err != nil {
				return
			}
			if task != nil {
				mu.Lock()
				winners = append(winners, task)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if len(winners) != 1 {
		t.Errorf("expected exactly 1 winner, got %d", len(winners))
	}
}

// --- ClaimNextTask HTTP Tests ---

func TestHTTPClaimNext(t *testing.T) {
	_, ts := testServer(t)

	http.Post(ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"name":"my-app"}`))
	http.Post(ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"id":"t1","title":"Build API","priority":1,"project":"my-app"}`))

	resp, err := http.Post(ts.URL+"/api/tasks/next", "application/json",
		bytes.NewBufferString(`{"agent_id":1,"role":"developer","project":"my-app"}`))
	if err != nil {
		t.Fatalf("POST /api/tasks/next: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.ID != "t1" {
		t.Errorf("ID = %q, want t1", task.ID)
	}
	if task.Status != "claimed" {
		t.Errorf("Status = %q, want claimed", task.Status)
	}
}

func TestHTTPClaimNextEmpty(t *testing.T) {
	_, ts := testServer(t)

	resp, err := http.Post(ts.URL+"/api/tasks/next", "application/json",
		bytes.NewBufferString(`{"agent_id":1,"role":"developer","project":"empty-proj"}`))
	if err != nil {
		t.Fatalf("POST /api/tasks/next: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func TestHTTPProjectCRUD(t *testing.T) {
	_, ts := testServer(t)

	// Register.
	body := `{"name":"test-proj","repo_path":"/tmp/test-proj"}`
	resp, _ := http.Post(ts.URL+"/api/projects", "application/json", bytes.NewBufferString(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/projects status = %d", resp.StatusCode)
	}

	// List.
	resp, _ = http.Get(ts.URL + "/api/projects")
	var projects []ProjectInfo
	json.NewDecoder(resp.Body).Decode(&projects)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}

	// Get.
	resp, _ = http.Get(ts.URL + "/api/projects/test-proj")
	var p ProjectInfo
	json.NewDecoder(resp.Body).Decode(&p)
	if p.Name != "test-proj" {
		t.Errorf("Name = %q", p.Name)
	}

	// Start.
	resp, _ = http.Post(ts.URL+"/api/projects/test-proj/start", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/projects/test-proj/start status = %d", resp.StatusCode)
	}

	// Status.
	resp, _ = http.Get(ts.URL + "/api/projects/test-proj/status")
	json.NewDecoder(resp.Body).Decode(&p)
	if p.Status != "running" {
		t.Errorf("Status = %q, want running", p.Status)
	}

	// Stop.
	resp, _ = http.Post(ts.URL+"/api/projects/test-proj/stop", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/projects/test-proj/stop status = %d", resp.StatusCode)
	}

	// Delete.
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/projects/test-proj", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/projects/test-proj status = %d", resp.StatusCode)
	}
}

package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/logparse"
)

// Message types for async data fetching.

type projectsMsg struct {
	projects []controlplane.ProjectDetail
	err      error
}

type stateMsg struct {
	project string
	state   *daemon.State
	err     error
}

type allStatesMsg struct {
	states map[string]*daemon.State
}

type tasksMsg struct {
	tasks []controlplane.Task
	err   error
}

// logLine holds a formatted log line along with its type classification.
type logLine struct {
	Text     string
	LineType logparse.LogLineType
}

type logsMsg struct {
	logs map[string][]logLine // agentKey -> recent log lines
}

type diffsMsg struct {
	diffs []string
}

type taskCreatedMsg struct {
	err error
}

type taskCompletedMsg struct {
	taskID string
	err    error
}

type taskDeletedMsg struct {
	taskID string
	err    error
}

type projectStartedMsg struct {
	project string
	err     error
}

type projectStoppedMsg struct {
	project string
	err     error
}

type daemonLogMsg struct {
	lines []string
}

type daemonRestartedMsg struct {
	err error
}

type epicCreatedMsg struct {
	epicID string
	tasks  int
	err    error
}

type taskApprovedMsg struct {
	taskID string
	err    error
}

type taskRejectedMsg struct {
	taskID string
	err    error
}

type epicApprovedMsg struct {
	epicID string
	err    error
}

type taskRegeneratedMsg struct {
	taskID string
	err    error
}

type tickMsg time.Time

// fetchProjects fetches the project list from the API.
func fetchProjects(baseURL string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Get(baseURL + "/api/projects")
		if err != nil {
			return projectsMsg{err: err}
		}
		defer resp.Body.Close()

		var projects []controlplane.ProjectDetail
		if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
			return projectsMsg{err: err}
		}
		return projectsMsg{projects: projects}
	}
}

// fetchState fetches agent state for a single project.
func fetchState(baseURL, projectName string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Get(baseURL + "/api/projects/" + projectName + "/state")
		if err != nil {
			return stateMsg{project: projectName, err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return stateMsg{project: projectName, err: fmt.Errorf("status %d", resp.StatusCode)}
		}

		var state daemon.State
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			return stateMsg{project: projectName, err: err}
		}
		return stateMsg{project: projectName, state: &state}
	}
}

// fetchAllStates fetches state for all projects and aggregates results.
func fetchAllStates(baseURL string, projectNames []string) tea.Cmd {
	return func() tea.Msg {
		states := make(map[string]*daemon.State)
		client := &http.Client{Timeout: 5 * time.Second}
		for _, name := range projectNames {
			resp, err := client.Get(baseURL + "/api/projects/" + name + "/state")
			if err != nil {
				continue
			}
			var state daemon.State
			if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
			states[name] = &state
		}
		return allStatesMsg{states: states}
	}
}

// fetchTasks fetches tasks, optionally filtered by project.
func fetchTasks(baseURL, project string) tea.Cmd {
	return func() tea.Msg {
		url := baseURL + "/api/tasks"
		if project != "" {
			url += "?project=" + project
		}
		resp, err := http.Get(url)
		if err != nil {
			return tasksMsg{err: err}
		}
		defer resp.Body.Close()

		var tasks []controlplane.Task
		if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
			return tasksMsg{err: err}
		}
		return tasksMsg{tasks: tasks}
	}
}

// fetchLogs reads the latest log lines for agents from the filesystem.
func fetchLogs(projectName string, agentIDs []int) tea.Cmd {
	return func() tea.Msg {
		logs := make(map[string][]logLine)
		for _, id := range agentIDs {
			key := fmt.Sprintf("%s/agent-%d", projectName, id)
			logDir := filepath.Join(constants.ProjectAgentLogDir(projectName), fmt.Sprintf("agent-%d", id))
			logFile, err := logparse.FindLatestLog(logDir)
			if err != nil {
				continue
			}

			// Read last 8KB of the file.
			data, err := readTail(logFile, 8192)
			if err != nil {
				continue
			}

			lines := strings.Split(string(data), "\n")
			var formatted []logLine
			for _, line := range lines {
				if f, lt, ok := logparse.FormatLogLineTyped(line); ok {
					formatted = append(formatted, logLine{Text: f, LineType: lt})
				}
			}

			// Keep last 8 lines.
			if len(formatted) > 8 {
				formatted = formatted[len(formatted)-8:]
			}
			logs[key] = formatted
		}
		return logsMsg{logs: logs}
	}
}

// fetchAllLogs reads logs for agents across multiple projects.
func fetchAllLogs(states map[string]*daemon.State) tea.Cmd {
	return func() tea.Msg {
		logs := make(map[string][]logLine)
		for projectName, state := range states {
			if state == nil {
				continue
			}
			for _, agent := range state.Agents {
				key := fmt.Sprintf("%s/agent-%d", projectName, agent.ID)
				logDir := filepath.Join(constants.ProjectAgentLogDir(projectName), fmt.Sprintf("agent-%d", agent.ID))
				logFile, err := logparse.FindLatestLog(logDir)
				if err != nil {
					continue
				}

				data, err := readTail(logFile, 8192)
				if err != nil {
					continue
				}

				lines := strings.Split(string(data), "\n")
				var formatted []logLine
				for _, line := range lines {
					if f, lt, ok := logparse.FormatLogLineTyped(line); ok {
						formatted = append(formatted, logLine{Text: f, LineType: lt})
					}
				}

				if len(formatted) > 8 {
					formatted = formatted[len(formatted)-8:]
				}
				logs[key] = formatted
			}
		}
		return logsMsg{logs: logs}
	}
}

// fetchDiffs runs git log --stat on upstream bare repos.
func fetchDiffs(projectNames []string) tea.Cmd {
	return func() tea.Msg {
		var allDiffs []string
		for _, projectName := range projectNames {
			cfg, err := config.LoadProject(projectName)
			if err != nil {
				continue
			}
			for _, repo := range cfg.Repos {
				label := config.RepoLabel(repo)
				repoPath := constants.UpstreamRepoPath(projectName, label)
				if _, err := os.Stat(repoPath); err != nil {
					continue
				}

				cmd := exec.Command("git", "log", "--stat", "--oneline", "-15")
				cmd.Dir = repoPath
				var out bytes.Buffer
				cmd.Stdout = &out
				if err := cmd.Run(); err != nil {
					continue
				}

				if len(projectNames) > 1 {
					allDiffs = append(allDiffs, fmt.Sprintf("--- %s/%s ---", projectName, label))
				}
				lines := strings.Split(strings.TrimSpace(out.String()), "\n")
				allDiffs = append(allDiffs, lines...)
			}
		}
		return diffsMsg{diffs: allDiffs}
	}
}

// createTask posts a new task via the API.
func createTask(baseURL, title, project string, priority int) tea.Cmd {
	return func() tea.Msg {
		body := map[string]interface{}{
			"title":    title,
			"project":  project,
			"priority": priority,
		}
		data, _ := json.Marshal(body)
		resp, err := http.Post(baseURL+"/api/tasks", "application/json", bytes.NewReader(data))
		if err != nil {
			return taskCreatedMsg{err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return taskCreatedMsg{err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return taskCreatedMsg{}
	}
}

// completeTask marks a task as complete via the API.
func completeTask(baseURL, taskID string) tea.Cmd {
	return func() tea.Msg {
		body := []byte(`{"summary":"completed via TUI"}`)
		resp, err := http.Post(baseURL+"/api/tasks/"+taskID+"/complete", "application/json", bytes.NewReader(body))
		if err != nil {
			return taskCompletedMsg{taskID: taskID, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return taskCompletedMsg{taskID: taskID, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return taskCompletedMsg{taskID: taskID}
	}
}

// deleteTask marks a task as done via PATCH.
func deleteTask(baseURL, taskID string) tea.Cmd {
	return func() tea.Msg {
		body := []byte(`{"status":"done"}`)
		req, err := http.NewRequest(http.MethodPatch, baseURL+"/api/tasks/"+taskID, bytes.NewReader(body))
		if err != nil {
			return taskDeletedMsg{taskID: taskID, err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return taskDeletedMsg{taskID: taskID, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return taskDeletedMsg{taskID: taskID, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return taskDeletedMsg{taskID: taskID}
	}
}

// startProject sends a POST to start a project.
func startProject(baseURL, projectName string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Post(baseURL+"/api/projects/"+projectName+"/start", "application/json", nil)
		if err != nil {
			return projectStartedMsg{project: projectName, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return projectStartedMsg{project: projectName, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return projectStartedMsg{project: projectName}
	}
}

// stopProject sends a POST to stop a project.
func stopProject(baseURL, projectName string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Post(baseURL+"/api/projects/"+projectName+"/stop", "application/json", nil)
		if err != nil {
			return projectStoppedMsg{project: projectName, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return projectStoppedMsg{project: projectName, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return projectStoppedMsg{project: projectName}
	}
}

// restartDaemon stops and restarts the daemon process.
func restartDaemon() tea.Cmd {
	return func() tea.Msg {
		oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if oauthToken == "" && apiKey == "" {
			return daemonRestartedMsg{err: fmt.Errorf("no credentials found: set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY")}
		}

		// Best-effort stop — daemon may already be down.
		if daemon.IsRunning() {
			if err := daemon.Stop(); err != nil {
				return daemonRestartedMsg{err: fmt.Errorf("stop: %w", err)}
			}
		}

		if err := daemon.Start(apiKey, oauthToken); err != nil {
			return daemonRestartedMsg{err: fmt.Errorf("start: %w", err)}
		}
		return daemonRestartedMsg{}
	}
}

// connectDaemonLog connects to the daemon's SSE log endpoint over the Unix
// domain socket and returns a channel that receives log lines.
func connectDaemonLog() <-chan string {
	ch := make(chan string, 256)
	go func() {
		client := &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", constants.DaemonSocketPath())
				},
			},
		}
		for {
			resp, err := client.Get("http://daemon/api/daemon/logs")
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					ch <- line[6:] // strip "data: " prefix
				}
			}
			resp.Body.Close()
			time.Sleep(2 * time.Second)
		}
	}()
	return ch
}

// drainLogChannel returns a tea.Cmd that non-blockingly drains available lines
// from the daemon log channel.
func drainLogChannel(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		var lines []string
		for {
			select {
			case line := <-ch:
				lines = append(lines, line)
			default:
				return daemonLogMsg{lines: lines}
			}
		}
	}
}

// createEpic posts a new epic via the API, triggering LLM decomposition.
func createEpic(baseURL, title, desc, project string) tea.Cmd {
	return func() tea.Msg {
		body := map[string]string{
			"title":       title,
			"description": desc,
			"project":     project,
		}
		data, _ := json.Marshal(body)
		resp, err := http.Post(baseURL+"/api/epics", "application/json", bytes.NewReader(data))
		if err != nil {
			return epicCreatedMsg{err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return epicCreatedMsg{err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		var result struct {
			Epic  struct{ ID string } `json:"epic"`
			Tasks []struct{}          `json:"tasks"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return epicCreatedMsg{err: err}
		}
		return epicCreatedMsg{epicID: result.Epic.ID, tasks: len(result.Tasks)}
	}
}

// approveTaskPrompt approves a single task's expanded prompt.
func approveTaskPrompt(baseURL, taskID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Post(baseURL+"/api/tasks/"+taskID+"/approve", "application/json", nil)
		if err != nil {
			return taskApprovedMsg{taskID: taskID, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return taskApprovedMsg{taskID: taskID, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return taskApprovedMsg{taskID: taskID}
	}
}

// rejectTaskPrompt rejects a single task's expanded prompt.
func rejectTaskPrompt(baseURL, taskID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Post(baseURL+"/api/tasks/"+taskID+"/reject", "application/json", nil)
		if err != nil {
			return taskRejectedMsg{taskID: taskID, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return taskRejectedMsg{taskID: taskID, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return taskRejectedMsg{taskID: taskID}
	}
}

// approveEpic bulk-approves all tasks in an epic.
func approveEpic(baseURL, epicID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Post(baseURL+"/api/epics/"+epicID+"/approve", "application/json", nil)
		if err != nil {
			return epicApprovedMsg{epicID: epicID, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return epicApprovedMsg{epicID: epicID, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return epicApprovedMsg{epicID: epicID}
	}
}

// regenerateTaskPrompt re-expands a task's prompt via LLM.
func regenerateTaskPrompt(baseURL, taskID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Post(baseURL+"/api/tasks/"+taskID+"/regenerate", "application/json", nil)
		if err != nil {
			return taskRegeneratedMsg{taskID: taskID, err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return taskRegeneratedMsg{taskID: taskID, err: fmt.Errorf("status %d", resp.StatusCode)}
		}
		return taskRegeneratedMsg{taskID: taskID}
	}
}

// readTail reads the last n bytes of a file. When the file is larger than n,
// the first partial line is dropped to avoid returning truncated JSON.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	start := info.Size() - n
	if start < 0 {
		start = 0
	}

	if _, err := f.Seek(start, 0); err != nil {
		return nil, err
	}

	buf := make([]byte, info.Size()-start)
	_, err = f.Read(buf)
	if err != nil {
		return nil, err
	}

	// If we seeked past the beginning, the first "line" is likely a
	// truncated fragment. Drop everything before the first newline.
	if start > 0 {
		if idx := bytes.IndexByte(buf, '\n'); idx >= 0 {
			buf = buf[idx+1:]
		}
	}

	return buf, nil
}

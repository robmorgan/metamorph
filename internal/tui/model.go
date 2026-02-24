package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/daemon"
)

// Panel indices.
const (
	panelAgents = iota
	panelTasks
	panelDiffs
	panelLog
	panelCount
)

// Modes.
const (
	modeNormal = iota
	modeProjectFilter
	modeAddTask
	modeConfirm
)

// Model is the main Bubbletea model for the TUI dashboard.
type Model struct {
	baseURL string
	width   int
	height  int

	// Panel state.
	activePanel int
	viewports   [panelCount]viewport.Model

	// Data.
	projects    []controlplane.ProjectDetail
	states      map[string]*daemon.State
	tasks       []controlplane.Task
	agentLogs   map[string][]string // "project/agent-N" -> lines
	diffs       []string
	daemonLog   []string
	logCh       <-chan string

	// Filtering.
	projectFilter string // "" means all projects
	projectNames  []string

	// Task selection.
	taskCursor int

	// Mode.
	mode        int
	taskInput   taskInput
	confirmTask    string // task ID being confirmed
	confirmAction  string // "complete", "delete", "start", or "stop"
	confirmProject string // project name being confirmed (for start/stop)

	// Project filter selection.
	filterCursor int
	filterItems  []string // "All" + project names

	// Tick state.
	tickCount  int
	lastErr    string
	ready      bool
}

// NewModel creates a new TUI model.
func NewModel(baseURL, projectFilter string) Model {
	return Model{
		baseURL:       baseURL,
		projectFilter: projectFilter,
		states:        make(map[string]*daemon.State),
		agentLogs:     make(map[string][]string),
		taskInput:     newTaskInput(),
		mode:          modeNormal,
		logCh:         connectDaemonLog(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchProjects(m.baseURL),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.initViewports()
		m.ready = true
		return m, nil

	case tickMsg:
		m.tickCount++
		cmds := m.buildTickCmds()
		cmds = append(cmds, tickCmd())
		return m, tea.Batch(cmds...)

	case projectsMsg:
		if msg.err == nil {
			m.projects = msg.projects
			m.projectNames = nil
			for _, p := range msg.projects {
				m.projectNames = append(m.projectNames, p.Name)
			}
		} else {
			m.lastErr = msg.err.Error()
		}
		// Fetch initial data after projects load.
		return m, tea.Batch(m.buildTickCmds()...)

	case allStatesMsg:
		m.states = msg.states
		m.updateAgentsView()
		return m, nil

	case stateMsg:
		if msg.err == nil && msg.state != nil {
			m.states[msg.project] = msg.state
			m.updateAgentsView()
		}
		return m, nil

	case tasksMsg:
		if msg.err == nil {
			m.tasks = msg.tasks
			// Sort by priority then status.
			sort.Slice(m.tasks, func(i, j int) bool {
				if m.tasks[i].Priority != m.tasks[j].Priority {
					return m.tasks[i].Priority < m.tasks[j].Priority
				}
				return m.tasks[i].Status < m.tasks[j].Status
			})
			if m.taskCursor >= len(m.tasks) {
				m.taskCursor = max(0, len(m.tasks)-1)
			}
			m.updateTasksView()
		}
		return m, nil

	case logsMsg:
		for k, v := range msg.logs {
			m.agentLogs[k] = v
		}
		m.updateAgentsView()
		return m, nil

	case diffsMsg:
		m.diffs = msg.diffs
		m.updateDiffsView()
		return m, nil

	case daemonLogMsg:
		if len(msg.lines) > 0 {
			m.daemonLog = append(m.daemonLog, msg.lines...)
			if len(m.daemonLog) > 500 {
				m.daemonLog = m.daemonLog[len(m.daemonLog)-500:]
			}
			m.updateLogView()
		}
		return m, nil

	case taskCreatedMsg:
		if msg.err != nil {
			m.lastErr = "create task: " + msg.err.Error()
		}
		return m, fetchTasks(m.baseURL, m.projectFilter)

	case taskCompletedMsg:
		if msg.err != nil {
			m.lastErr = "complete task: " + msg.err.Error()
		}
		m.mode = modeNormal
		return m, fetchTasks(m.baseURL, m.projectFilter)

	case taskDeletedMsg:
		if msg.err != nil {
			m.lastErr = "delete task: " + msg.err.Error()
		}
		m.mode = modeNormal
		return m, fetchTasks(m.baseURL, m.projectFilter)

	case projectStartedMsg:
		if msg.err != nil {
			m.lastErr = "start project: " + msg.err.Error()
		}
		return m, fetchProjects(m.baseURL)

	case projectStoppedMsg:
		if msg.err != nil {
			m.lastErr = "stop project: " + msg.err.Error()
		}
		return m, fetchProjects(m.baseURL)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Forward to active viewport or text input.
	if m.mode == modeAddTask {
		var cmd tea.Cmd
		m.taskInput, cmd = m.taskInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit.
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.mode {
	case modeAddTask:
		return m.handleAddTaskKey(msg)
	case modeProjectFilter:
		return m.handleProjectFilterKey(msg)
	case modeConfirm:
		return m.handleConfirmKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "q":
		return m, tea.Quit

	case "tab":
		m.activePanel = (m.activePanel + 1) % panelCount
		m.updateAllViews()
		return m, nil

	case "shift+tab":
		m.activePanel = (m.activePanel + panelCount - 1) % panelCount
		m.updateAllViews()
		return m, nil

	case "1":
		m.activePanel = panelAgents
		m.updateAllViews()
		return m, nil

	case "2":
		m.activePanel = panelTasks
		m.updateAllViews()
		return m, nil

	case "3":
		m.activePanel = panelDiffs
		m.updateAllViews()
		return m, nil

	case "4":
		m.activePanel = panelLog
		m.updateAllViews()
		return m, nil

	case "j", "down":
		if m.activePanel == panelTasks && len(m.tasks) > 0 {
			m.taskCursor = min(m.taskCursor+1, len(m.tasks)-1)
			m.updateTasksView()
		} else {
			vp := &m.viewports[m.activePanel]
			vp.LineDown(1)
		}
		return m, nil

	case "k", "up":
		if m.activePanel == panelTasks && len(m.tasks) > 0 {
			m.taskCursor = max(m.taskCursor-1, 0)
			m.updateTasksView()
		} else {
			vp := &m.viewports[m.activePanel]
			vp.LineUp(1)
		}
		return m, nil

	case "p":
		m.mode = modeProjectFilter
		m.filterItems = append([]string{"All"}, m.projectNames...)
		m.filterCursor = 0
		// Set cursor to current selection.
		for i, item := range m.filterItems {
			if item == m.projectFilter || (m.projectFilter == "" && item == "All") {
				m.filterCursor = i
				break
			}
		}
		return m, nil

	case "a":
		m.mode = modeAddTask
		m.taskInput.Focus()
		return m, nil

	case "d":
		if m.activePanel == panelTasks && len(m.tasks) > 0 {
			task := m.tasks[m.taskCursor]
			if task.Status != "done" {
				m.mode = modeConfirm
				m.confirmTask = task.ID
				m.confirmAction = "complete"
			}
		}
		return m, nil

	case "x":
		if m.activePanel == panelTasks && len(m.tasks) > 0 {
			task := m.tasks[m.taskCursor]
			m.mode = modeConfirm
			m.confirmTask = task.ID
			m.confirmAction = "delete"
		}
		return m, nil

	case "s":
		if m.projectFilter != "" {
			// Determine current project status.
			action := "start"
			for _, p := range m.projects {
				if p.Name == m.projectFilter && p.Status == "running" {
					action = "stop"
					break
				}
			}
			m.mode = modeConfirm
			m.confirmProject = m.projectFilter
			m.confirmAction = action
		}
		return m, nil
	}

	return m, nil
}

func (m Model) handleAddTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.mode = modeNormal
		m.taskInput.Blur()
		return m, nil

	case "enter":
		title := m.taskInput.Value()
		if title == "" {
			return m, nil
		}
		m.mode = modeNormal
		m.taskInput.Blur()
		return m, createTask(m.baseURL, title, m.projectFilter, 2)
	}

	var cmd tea.Cmd
	m.taskInput, cmd = m.taskInput.Update(msg)
	return m, cmd
}

func (m Model) handleProjectFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.mode = modeNormal
		return m, nil

	case "j", "down":
		if m.filterCursor < len(m.filterItems)-1 {
			m.filterCursor++
		}
		return m, nil

	case "k", "up":
		if m.filterCursor > 0 {
			m.filterCursor--
		}
		return m, nil

	case "enter":
		if m.filterCursor == 0 {
			m.projectFilter = ""
		} else {
			m.projectFilter = m.filterItems[m.filterCursor]
		}
		m.mode = modeNormal
		// Refresh data for new filter.
		return m, tea.Batch(m.buildTickCmds()...)
	}

	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "y", "Y":
		action := m.confirmAction
		m.mode = modeNormal
		switch action {
		case "complete":
			return m, completeTask(m.baseURL, m.confirmTask)
		case "delete":
			return m, deleteTask(m.baseURL, m.confirmTask)
		case "start":
			return m, startProject(m.baseURL, m.confirmProject)
		case "stop":
			return m, stopProject(m.baseURL, m.confirmProject)
		}
		return m, nil

	case "n", "N", "esc":
		m.mode = modeNormal
		return m, nil
	}

	return m, nil
}

func (m *Model) buildTickCmds() []tea.Cmd {
	var cmds []tea.Cmd

	// Always drain daemon log channel.
	cmds = append(cmds, drainLogChannel(m.logCh))

	projectNames := m.activeProjectNames()

	if len(projectNames) > 0 {
		cmds = append(cmds, fetchAllStates(m.baseURL, projectNames))
		cmds = append(cmds, fetchTasks(m.baseURL, m.projectFilter))

		// Fetch logs using current states.
		if len(m.states) > 0 {
			filteredStates := make(map[string]*daemon.State)
			for _, name := range projectNames {
				if s, ok := m.states[name]; ok {
					filteredStates[name] = s
				}
			}
			cmds = append(cmds, fetchAllLogs(filteredStates))
		}

		// Fetch diffs every 3rd tick.
		if m.tickCount%3 == 0 {
			cmds = append(cmds, fetchDiffs(projectNames))
		}
	}

	return cmds
}

func (m *Model) activeProjectNames() []string {
	if m.projectFilter != "" {
		return []string{m.projectFilter}
	}
	return m.projectNames
}

func (m *Model) initViewports() {
	if m.width == 0 || m.height == 0 {
		return
	}

	// Layout: header(1) + top panels + bottom panels + status bar(1).
	topHeight := (m.height - 4) * 55 / 100
	bottomHeight := m.height - 4 - topHeight
	leftWidth := m.width/2 - 1
	rightWidth := m.width - leftWidth - 1

	// Account for borders (2 chars each side).
	vpLeftWidth := max(leftWidth-4, 10)
	vpRightWidth := max(rightWidth-4, 10)
	vpTopHeight := max(topHeight-3, 3)    // -3 for border + title
	vpBottomHeight := max(bottomHeight-3, 3)

	m.viewports[panelAgents] = viewport.New(vpLeftWidth, vpTopHeight)
	m.viewports[panelTasks] = viewport.New(vpRightWidth, vpTopHeight)
	m.viewports[panelDiffs] = viewport.New(vpLeftWidth, vpBottomHeight)
	m.viewports[panelLog] = viewport.New(vpRightWidth, vpBottomHeight)

	m.updateAllViews()
}

func (m *Model) updateAllViews() {
	m.updateAgentsView()
	m.updateTasksView()
	m.updateDiffsView()
	m.updateLogView()
}

func (m *Model) updateAgentsView() {
	var lines []string
	projectNames := m.activeProjectNames()
	showProjectTag := len(projectNames) > 1

	for _, projName := range projectNames {
		state, ok := m.states[projName]
		if !ok || state == nil {
			continue
		}

		for _, agent := range state.Agents {
			var line strings.Builder
			if showProjectTag {
				line.WriteString(projectTagStyle.Render("["+projName+"] "))
			}
			line.WriteString(fmt.Sprintf("Agent %d ", agent.ID))
			line.WriteString(dimStyle.Render(agent.Role))
			line.WriteString("  ")
			line.WriteString(statusStyle(agent.Status).Render(agent.Status))
			if agent.CurrentTask != nil {
				line.WriteString("  " + dimStyle.Render(*agent.CurrentTask))
			}
			lines = append(lines, line.String())

			// Show log tail.
			key := fmt.Sprintf("%s/agent-%d", projName, agent.ID)
			if logLines, ok := m.agentLogs[key]; ok {
				for _, l := range logLines {
					// Truncate long lines.
					if len(l) > m.viewports[panelAgents].Width-4 {
						l = l[:m.viewports[panelAgents].Width-7] + "..."
					}
					lines = append(lines, "  "+logLineStyle.Render(l))
				}
			}
			lines = append(lines, "")
		}
	}

	if len(lines) == 0 {
		// Check if any visible project is in startup phase.
		var startupPhase string
		for _, projName := range projectNames {
			if s, ok := m.states[projName]; ok && s != nil && s.Status == "starting" && s.StartupPhase != "" {
				startupPhase = s.StartupPhase
				break
			}
		}

		if startupPhase != "" {
			frame := spinnerFrames[m.tickCount%len(spinnerFrames)]
			lines = append(lines, fmt.Sprintf("  %c %s", frame, startupPhase))
		} else if m.projectFilter != "" {
			// Check if the filtered project is stopped.
			stopped := true
			for _, p := range m.projects {
				if p.Name == m.projectFilter && p.Status == "running" {
					stopped = false
					break
				}
			}
			if stopped {
				lines = append(lines, dimStyle.Render("  Project stopped. Press 's' to start."))
			} else {
				lines = append(lines, dimStyle.Render("  No agents running"))
			}
		} else {
			lines = append(lines, dimStyle.Render("  No agents running"))
		}
	}

	m.viewports[panelAgents].SetContent(strings.Join(lines, "\n"))
}

func (m *Model) updateTasksView() {
	var lines []string

	for i, task := range m.tasks {
		// Skip done tasks unless that's all there is.
		prefix := "  "
		if m.activePanel == panelTasks && i == m.taskCursor {
			prefix = cursorStyle.Render("> ")
		}

		pBadge := priorityStyle(task.Priority).Render(fmt.Sprintf("[P%d]", task.Priority))
		title := task.Title
		maxTitleLen := m.viewports[panelTasks].Width - 20
		if maxTitleLen > 0 && len(title) > maxTitleLen {
			title = title[:maxTitleLen-3] + "..."
		}
		status := taskStatusStyle(task.Status).Render(task.Status)

		line := fmt.Sprintf("%s%s %s  %s", prefix, pBadge, title, status)

		if task.Assignee != nil {
			line += dimStyle.Render(fmt.Sprintf("  -> agent-%d", *task.Assignee))
		}

		lines = append(lines, line)
	}

	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  No tasks"))
	}

	lines = append(lines, "")
	if m.activePanel == panelTasks {
		lines = append(lines, dimStyle.Render("  (a)dd  (d)one  (x)delete"))
	}

	m.viewports[panelTasks].SetContent(strings.Join(lines, "\n"))
}

func (m *Model) updateDiffsView() {
	var lines []string
	for _, line := range m.diffs {
		styled := styleDiffLine(line)
		lines = append(lines, styled)
	}

	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  No recent diffs"))
	}

	m.viewports[panelDiffs].SetContent(strings.Join(lines, "\n"))
}

func (m *Model) updateLogView() {
	var lines []string
	for _, line := range m.daemonLog {
		// Truncate long lines.
		if len(line) > m.viewports[panelLog].Width-4 {
			line = line[:m.viewports[panelLog].Width-7] + "..."
		}
		lines = append(lines, "  "+logLineStyle.Render(line))
	}

	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  Waiting for daemon logs..."))
	}

	m.viewports[panelLog].SetContent(strings.Join(lines, "\n"))
	// Auto-scroll to bottom.
	m.viewports[panelLog].GotoBottom()
}

func styleDiffLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Section headers.
	if strings.HasPrefix(trimmed, "---") {
		return dimStyle.Render(line)
	}

	// Commit lines: hash + message.
	if len(trimmed) >= 7 && isHexString(trimmed[:7]) {
		parts := strings.SplitN(trimmed, " ", 2)
		if len(parts) == 2 {
			return "  " + commitHashStyle.Render(parts[0]) + " " + parts[1]
		}
		return "  " + commitHashStyle.Render(trimmed)
	}

	// Stat lines with +/-.
	if strings.Contains(line, "|") && (strings.Contains(line, "+") || strings.Contains(line, "-")) {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) == 2 {
			stat := parts[1]
			stat = strings.ReplaceAll(stat, "+", addStyle.Render("+"))
			stat = strings.ReplaceAll(stat, "-", removeStyle.Render("-"))
			return "  " + dimStyle.Render(strings.TrimSpace(parts[0])) + " | " + stat
		}
	}

	return "  " + line
}

func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (m Model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	// Header.
	filterLabel := "All Projects"
	if m.projectFilter != "" {
		filterLabel = m.projectFilter
		// Append project status.
		for _, p := range m.projects {
			if p.Name == m.projectFilter {
				if p.Status == "running" {
					filterLabel += " (running)"
				} else {
					filterLabel += " (stopped)"
				}
				break
			}
		}
	}
	activeCount := 0
	for _, p := range m.projects {
		if p.Status == "running" {
			activeCount++
		}
	}
	header := headerStyle.Render(fmt.Sprintf(
		"  MetaMorph   [%s]        %d project(s) active",
		filterLabel, activeCount,
	))
	header = lipgloss.PlaceHorizontal(m.width, lipgloss.Left, header)

	// Top panels (agents + tasks).
	topHeight := (m.height - 4) * 55 / 100
	bottomHeight := m.height - 4 - topHeight
	leftWidth := m.width/2 - 1
	rightWidth := m.width - leftWidth - 1

	agentsPanel := m.renderPanel(panelAgents, "AGENTS", leftWidth, topHeight)
	tasksPanel := m.renderPanel(panelTasks, "TASKS", rightWidth, topHeight)
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, agentsPanel, tasksPanel)

	// Bottom panels (diffs + daemon log).
	diffsPanel := m.renderPanel(panelDiffs, "DIFFS", leftWidth, bottomHeight)
	logPanel := m.renderPanel(panelLog, "DAEMON LOG", rightWidth, bottomHeight)
	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, diffsPanel, logPanel)

	// Status bar.
	statusBar := m.renderStatusBar()

	// Compose main view.
	view := lipgloss.JoinVertical(lipgloss.Left,
		header,
		topRow,
		bottomRow,
		statusBar,
	)

	// Overlay modes.
	switch m.mode {
	case modeProjectFilter:
		view = m.overlayProjectFilter(view)
	case modeAddTask:
		view = m.overlayAddTask(view)
	case modeConfirm:
		view = m.overlayConfirm(view)
	}

	return view
}

// spinnerFrames are the Braille spinner characters cycled during startup.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

func (m Model) renderPanel(panel int, title string, width, height int) string {
	border := normalBorder
	if panel == m.activePanel {
		border = activeBorder
	}

	// Count items for title.
	var count int
	switch panel {
	case panelAgents:
		for _, s := range m.states {
			if s != nil {
				count += len(s.Agents)
			}
		}
	case panelTasks:
		count = len(m.tasks)
	case panelDiffs:
		count = len(m.diffs)
	case panelLog:
		count = len(m.daemonLog)
	}

	titleStr := panelTitleStyle.Render(fmt.Sprintf("%s (%d)", title, count))

	// Show spinner in agents panel title during startup.
	if panel == panelAgents {
		for _, s := range m.states {
			if s != nil && s.StartupPhase != "" {
				frame := spinnerFrames[m.tickCount%len(spinnerFrames)]
				titleStr = panelTitleStyle.Render(fmt.Sprintf("%s %c", title, frame))
				break
			}
		}
	}

	innerWidth := max(width-4, 10)
	innerHeight := max(height-3, 3) // border(2) + title(1)

	// Resize viewport if needed.
	vp := &m.viewports[panel]
	if vp.Width != innerWidth || vp.Height != innerHeight {
		vp.Width = innerWidth
		vp.Height = innerHeight
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStr,
		vp.View(),
	)

	return border.Width(width - 2).Height(height - 2).Render(content)
}

func (m Model) renderStatusBar() string {
	var left string
	switch m.mode {
	case modeNormal:
		left = "  Tab:panel  1-4:panel  p:project  s:start/stop  a:add task  q:quit"
	case modeAddTask:
		left = "  Enter:submit  Esc:cancel"
	case modeProjectFilter:
		left = "  j/k:select  Enter:confirm  Esc:cancel"
	case modeConfirm:
		left = "  y:confirm  n:cancel"
	}

	right := ""
	if m.lastErr != "" {
		right = lipgloss.NewStyle().Foreground(red).Render(m.lastErr)
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	return statusBarStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

func (m Model) overlayProjectFilter(base string) string {
	var lines []string
	lines = append(lines, panelTitleStyle.Render("Select Project"))
	lines = append(lines, "")

	for i, item := range m.filterItems {
		prefix := "  "
		if i == m.filterCursor {
			prefix = cursorStyle.Render("> ")
		}
		label := item
		if item == "All" {
			label = "All Projects"
		} else {
			// Show status badge.
			for _, p := range m.projects {
				if p.Name == item {
					if p.Status == "running" {
						label += "  " + lipgloss.NewStyle().Foreground(green).Render("running")
					} else {
						label += "  " + dimStyle.Render("stopped")
					}
					break
				}
			}
		}
		lines = append(lines, prefix+label)
	}

	overlay := overlayStyle.Render(strings.Join(lines, "\n"))

	// Center the overlay.
	return placeOverlay(m.width, m.height, overlay, base)
}

func (m Model) overlayAddTask(base string) string {
	// Replace the last line (status bar) with the input.
	lines := strings.Split(base, "\n")
	if len(lines) > 1 {
		inputLine := statusBarStyle.Width(m.width).Render(m.taskInput.View())
		lines[len(lines)-1] = inputLine
	}
	return strings.Join(lines, "\n")
}

func (m Model) overlayConfirm(base string) string {
	action := m.confirmAction
	actionLabel := action
	if len(actionLabel) > 0 {
		actionLabel = strings.ToUpper(actionLabel[:1]) + actionLabel[1:]
	}

	var prompt string
	if action == "start" || action == "stop" {
		prompt = fmt.Sprintf("%s project %q? (y/n)", actionLabel, m.confirmProject)
	} else {
		taskTitle := ""
		for _, t := range m.tasks {
			if t.ID == m.confirmTask {
				taskTitle = t.Title
				break
			}
		}
		prompt = fmt.Sprintf("%s task %q? (y/n)", actionLabel, taskTitle)
	}

	overlay := overlayStyle.Render(prompt)
	return placeOverlay(m.width, m.height, overlay, base)
}

// placeOverlay renders an overlay centered on the base view.
func placeOverlay(width, height int, overlay, base string) string {
	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		overlay,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("0")),
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

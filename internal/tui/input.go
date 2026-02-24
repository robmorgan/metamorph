package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// taskInput wraps a text input for adding tasks.
type taskInput struct {
	input   textinput.Model
	active  bool
}

func newTaskInput() taskInput {
	ti := textinput.New()
	ti.Placeholder = "Task title..."
	ti.CharLimit = 200
	ti.Width = 60
	return taskInput{input: ti}
}

func (t *taskInput) Focus() {
	t.active = true
	t.input.Focus()
	t.input.SetValue("")
}

func (t *taskInput) Blur() {
	t.active = false
	t.input.Blur()
}

func (t *taskInput) Value() string {
	return t.input.Value()
}

func (t taskInput) Update(msg tea.Msg) (taskInput, tea.Cmd) {
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	return t, cmd
}

func (t taskInput) View() string {
	return "  Add task: " + t.input.View()
}

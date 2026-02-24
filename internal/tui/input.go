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

// epicInput wraps two text inputs for creating epics (title + description).
type epicInput struct {
	title  textinput.Model
	desc   textinput.Model
	active bool
	field  int // 0 = title, 1 = desc
}

func newEpicInput() epicInput {
	ti := textinput.New()
	ti.Placeholder = "Epic title..."
	ti.CharLimit = 200
	ti.Width = 60

	di := textinput.New()
	di.Placeholder = "Description (optional)..."
	di.CharLimit = 500
	di.Width = 60

	return epicInput{title: ti, desc: di}
}

func (e *epicInput) Focus() {
	e.active = true
	e.field = 0
	e.title.Focus()
	e.title.SetValue("")
	e.desc.Blur()
	e.desc.SetValue("")
}

func (e *epicInput) Blur() {
	e.active = false
	e.title.Blur()
	e.desc.Blur()
}

func (e *epicInput) SwitchField() {
	if e.field == 0 {
		e.field = 1
		e.title.Blur()
		e.desc.Focus()
	} else {
		e.field = 0
		e.desc.Blur()
		e.title.Focus()
	}
}

func (e *epicInput) TitleValue() string {
	return e.title.Value()
}

func (e *epicInput) DescValue() string {
	return e.desc.Value()
}

func (e epicInput) Update(msg tea.Msg) (epicInput, tea.Cmd) {
	var cmd tea.Cmd
	if e.field == 0 {
		e.title, cmd = e.title.Update(msg)
	} else {
		e.desc, cmd = e.desc.Update(msg)
	}
	return e, cmd
}

func (e epicInput) View() string {
	titleLabel := "  Title: "
	descLabel := "  Desc:  "
	if e.field == 0 {
		titleLabel = "> Title: "
	} else {
		descLabel = "> Desc:  "
	}
	return titleLabel + e.title.View() + "\n" + descLabel + e.desc.View()
}

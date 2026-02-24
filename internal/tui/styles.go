package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/robmorgan/metamorph/internal/logparse"
)

// Colors.
var (
	cyan    = lipgloss.Color("6")
	gray    = lipgloss.Color("240")
	green   = lipgloss.Color("2")
	yellow  = lipgloss.Color("3")
	red     = lipgloss.Color("1")
	magenta = lipgloss.Color("5")
	white   = lipgloss.Color("15")
	dimGray = lipgloss.Color("238")
)

// Panel borders.
var (
	activeBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cyan)
	normalBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(gray)
)

// Header bar.
var headerStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(white).
	Background(lipgloss.Color("62")).
	Padding(0, 1)

// Status bar at the bottom.
var statusBarStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("248")).
	Background(lipgloss.Color("236")).
	Padding(0, 1)

// Panel titles.
var panelTitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(cyan).
	Padding(0, 1)

// Agent status colors.
func statusStyle(status string) lipgloss.Style {
	switch status {
	case "running":
		return lipgloss.NewStyle().Foreground(green).Bold(true)
	case "exited", "stopped":
		return lipgloss.NewStyle().Foreground(red)
	case "created":
		return lipgloss.NewStyle().Foreground(yellow)
	default:
		return lipgloss.NewStyle().Foreground(gray)
	}
}

// Task priority badges.
func priorityStyle(priority int) lipgloss.Style {
	switch priority {
	case 0:
		return lipgloss.NewStyle().Foreground(red).Bold(true)
	case 1:
		return lipgloss.NewStyle().Foreground(yellow).Bold(true)
	case 2:
		return lipgloss.NewStyle().Foreground(green)
	case 3:
		return lipgloss.NewStyle().Foreground(cyan)
	default:
		return lipgloss.NewStyle().Foreground(gray)
	}
}

// Task status styles.
func taskStatusStyle(status string) lipgloss.Style {
	switch status {
	case "open":
		return lipgloss.NewStyle().Foreground(green)
	case "claimed":
		return lipgloss.NewStyle().Foreground(yellow)
	case "done":
		return lipgloss.NewStyle().Foreground(gray)
	case "blocked":
		return lipgloss.NewStyle().Foreground(red)
	default:
		return lipgloss.NewStyle().Foreground(gray)
	}
}

// Diff styles.
var (
	commitHashStyle = lipgloss.NewStyle().Foreground(yellow)
	addStyle        = lipgloss.NewStyle().Foreground(green)
	removeStyle     = lipgloss.NewStyle().Foreground(red)
)

// Project tag.
var projectTagStyle = lipgloss.NewStyle().
	Foreground(magenta).
	Bold(true)

// Cursor indicator for selected items.
var cursorStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)

// Dimmed text for inactive/secondary info.
var dimStyle = lipgloss.NewStyle().Foreground(dimGray)

// Log line styles per type.
var (
	logLineStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	logToolStyle   = lipgloss.NewStyle().Foreground(cyan)
	logErrorStyle  = lipgloss.NewStyle().Foreground(red)
	logSystemStyle = lipgloss.NewStyle().Foreground(dimGray)
)

// logLineStyleForType returns the appropriate style for a log line type.
func logLineStyleForType(lt logparse.LogLineType) lipgloss.Style {
	switch lt {
	case logparse.LogLineTool:
		return logToolStyle
	case logparse.LogLineError:
		return logErrorStyle
	case logparse.LogLineSystem:
		return logSystemStyle
	default:
		return logLineStyle
	}
}

// Overlay (project filter / confirm dialog).
var overlayStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(yellow).
	Padding(1, 2)

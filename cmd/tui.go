package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the interactive TUI dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running (use 'metamorph start' first)")
		}

		projectFilter := ""
		if name, _ := cmd.Flags().GetString("project"); name != "" {
			projectFilter = name
		}

		return runTUI(projectFilter)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

// runTUI starts the Bubbletea program with the dashboard model.
func runTUI(projectFilter string) error {
	baseURL := controlPlaneURL()
	m := tui.NewModel(baseURL, projectFilter)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

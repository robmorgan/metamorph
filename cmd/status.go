package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/brightfame/metamorph/internal/daemon"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of running agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		jsonOutput, _ := cmd.Flags().GetBool("json")

		state, err := daemon.GetStatus(projectDir)
		if err != nil {
			if !daemon.IsRunning(projectDir) {
				fmt.Println("Daemon is not running.")
				return nil
			}
			return fmt.Errorf("failed to read status: %w", err)
		}

		if jsonOutput {
			data, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal status: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		// Table mode.
		fmt.Printf("Project:  %s\n", state.ProjectName)
		fmt.Printf("Status:   %s\n", state.Status)
		fmt.Printf("Uptime:   %s\n", formatDuration(state.Stats.UptimeSeconds))
		fmt.Printf("Started:  %s\n", state.StartedAt.Local().Format("2006-01-02 15:04:05"))
		fmt.Println()

		if len(state.Agents) > 0 {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "AGENT\tROLE\tSTATUS\tTASK\tLAST ACTIVITY")
			for _, a := range state.Agents {
				task := "-"
				if a.CurrentTask != nil {
					task = *a.CurrentTask
				}
				lastAct := "-"
				if !a.LastActivity.IsZero() {
					lastAct = formatRelativeTime(a.LastActivity)
				}
				_, _ = fmt.Fprintf(w, "agent-%d\t%s\t%s\t%s\t%s\n", a.ID, a.Role, a.Status, task, lastAct)
			}
			_ = w.Flush()
			fmt.Println()
		}

		fmt.Printf("Commits: %d  Sessions: %d  Tasks completed: %d\n",
			state.Stats.TotalCommits, state.Stats.TotalSessions, state.Stats.TasksCompleted)

		return nil
	},
}

func init() {
	statusCmd.Flags().Bool("json", false, "Output status as JSON")
	rootCmd.AddCommand(statusCmd)
}

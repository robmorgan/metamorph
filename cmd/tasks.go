package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/brightfame/metamorph/internal/constants"
	"github.com/brightfame/metamorph/internal/gitops"
	"github.com/brightfame/metamorph/internal/tasks"
	"github.com/spf13/cobra"
)

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Manage and view agent tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		// Sync to working copy first so we can read task files.
		upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)
		workingCopyPath := filepath.Join(projectDir, ".metamorph", "work")
		if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
			return fmt.Errorf("failed to sync working copy: %w", err)
		}

		clearFlag, _ := cmd.Flags().GetBool("clear")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		if clearFlag {
			return clearStaleTasks(workingCopyPath)
		}

		locks, err := tasks.ListTasks(workingCopyPath)
		if err != nil {
			return fmt.Errorf("failed to list tasks: %w", err)
		}

		if len(locks) == 0 {
			fmt.Println("No active task locks.")
			return nil
		}

		if jsonOutput {
			data, err := json.MarshalIndent(locks, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal tasks: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TASK\tAGENT\tCLAIMED AT\tDURATION")
		for _, lock := range locks {
			duration := time.Since(lock.ClaimedAt).Truncate(time.Second)
			fmt.Fprintf(w, "%s\tagent-%d\t%s\t%s\n",
				lock.Name,
				lock.AgentID,
				lock.ClaimedAt.Local().Format("2006-01-02 15:04:05"),
				duration.String(),
			)
		}
		w.Flush()

		return nil
	},
}

func init() {
	tasksCmd.Flags().Bool("clear", false, "Clear stale task locks (interactive)")
	tasksCmd.Flags().Bool("json", false, "Output tasks as JSON")
	rootCmd.AddCommand(tasksCmd)
}

func clearStaleTasks(workingCopyPath string) error {
	locks, err := tasks.ListTasks(workingCopyPath)
	if err != nil {
		return fmt.Errorf("failed to list tasks: %w", err)
	}

	if len(locks) == 0 {
		fmt.Println("No active task locks.")
		return nil
	}

	fmt.Printf("Found %d active task lock(s):\n", len(locks))
	for _, lock := range locks {
		duration := time.Since(lock.ClaimedAt).Truncate(time.Second)
		fmt.Printf("  %s (agent-%d, %s)\n", lock.Name, lock.AgentID, duration.String())
	}

	fmt.Print("\nClear all stale tasks (older than 2h)? [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	cleared, err := tasks.ClearStaleTasks(workingCopyPath, 2*time.Hour)
	if err != nil {
		return fmt.Errorf("failed to clear stale tasks: %w", err)
	}

	if len(cleared) == 0 {
		fmt.Println("No stale tasks found (all locks are less than 2h old).")
	} else {
		fmt.Printf("Cleared %d stale task(s): %s\n", len(cleared), strings.Join(cleared, ", "))
	}

	return nil
}

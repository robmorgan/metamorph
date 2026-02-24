package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:     "task",
	Aliases: []string{"tasks"},
	Short:   "Manage project tasks via the control plane",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default behavior: list tasks.
		return taskListCmd.RunE(cmd, args)
	},
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		projectName, err := resolveProjectName(cmd)
		if err != nil {
			return err
		}

		status, _ := cmd.Flags().GetString("status")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		url := controlPlaneURL() + "/api/tasks?project=" + projectName
		if status != "" && status != "all" {
			url += "&status=" + status
		}

		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("control plane error (%d): %s", resp.StatusCode, string(body))
		}

		var tasks []controlplane.Task
		if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found.")
			return nil
		}

		if jsonOutput {
			data, err := json.MarshalIndent(tasks, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal tasks: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tPRIORITY\tASSIGNEE\tCREATED")
		for _, t := range tasks {
			assignee := "-"
			if t.Assignee != nil {
				assignee = fmt.Sprintf("agent-%d", *t.Assignee)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
				t.ID,
				truncate(t.Title, 40),
				t.Status,
				t.Priority,
				assignee,
				formatRelativeTime(t.CreatedAt),
			)
		}
		_ = w.Flush()
		return nil
	},
}

var taskAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Add a new task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		projectName, err := resolveProjectName(cmd)
		if err != nil {
			return err
		}

		title := args[0]
		description, _ := cmd.Flags().GetString("description")
		priority, _ := cmd.Flags().GetInt("priority")
		role, _ := cmd.Flags().GetString("role")

		body := map[string]interface{}{
			"title":         title,
			"description":   description,
			"priority":      priority,
			"role_affinity":  role,
			"project":       projectName,
		}
		jsonBody, _ := json.Marshal(body)

		resp, err := http.Post(controlPlaneURL()+"/api/tasks", "application/json", bytes.NewBuffer(jsonBody))
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to create task (%d): %s", resp.StatusCode, string(respBody))
		}

		var task controlplane.Task
		if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		fmt.Printf("Created task %s: %s\n", task.ID, task.Title)
		return nil
	},
}

var taskShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		taskID := args[0]

		resp, err := http.Get(controlPlaneURL() + "/api/tasks/" + taskID)
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("task %q not found", taskID)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("control plane error (%d): %s", resp.StatusCode, string(body))
		}

		var task controlplane.Task
		if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		fmt.Printf("ID:          %s\n", task.ID)
		fmt.Printf("Title:       %s\n", task.Title)
		fmt.Printf("Status:      %s\n", task.Status)
		fmt.Printf("Priority:    %d\n", task.Priority)
		if task.Description != "" {
			fmt.Printf("Description: %s\n", task.Description)
		}
		if task.RoleAffinity != "" {
			fmt.Printf("Role:        %s\n", task.RoleAffinity)
		}
		if task.Assignee != nil {
			fmt.Printf("Assignee:    agent-%d\n", *task.Assignee)
		}
		if task.ClaimedAt != nil {
			fmt.Printf("Claimed:     %s\n", task.ClaimedAt.Format(time.RFC3339))
		}
		if task.CompletedAt != nil {
			fmt.Printf("Completed:   %s\n", task.CompletedAt.Format(time.RFC3339))
		}
		if task.Notes != "" {
			fmt.Printf("Notes:       %s\n", task.Notes)
		}
		fmt.Printf("Created:     %s\n", task.CreatedAt.Format(time.RFC3339))

		return nil
	},
}

var taskCompleteCmd = &cobra.Command{
	Use:   "complete <id>",
	Short: "Mark a task as complete",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		taskID := args[0]
		summary, _ := cmd.Flags().GetString("summary")

		body := map[string]string{"summary": summary}
		jsonBody, _ := json.Marshal(body)

		resp, err := http.Post(controlPlaneURL()+"/api/tasks/"+taskID+"/complete", "application/json", bytes.NewBuffer(jsonBody))
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to complete task (%d): %s", resp.StatusCode, string(respBody))
		}

		fmt.Printf("Task %s marked as complete\n", taskID)
		return nil
	},
}

func init() {
	taskListCmd.Flags().String("status", "", "Filter by status (open, claimed, done, all)")
	taskListCmd.Flags().Bool("json", false, "Output as JSON")

	taskAddCmd.Flags().String("description", "", "Task description")
	taskAddCmd.Flags().Int("priority", 2, "Task priority (0=critical, 4=backlog)")
	taskAddCmd.Flags().String("role", "", "Preferred agent role (e.g. developer, tester)")

	taskCompleteCmd.Flags().String("summary", "", "Completion summary")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskAddCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskCompleteCmd)
	rootCmd.AddCommand(taskCmd)
}

// truncate shortens a string to maxLen with ellipsis.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

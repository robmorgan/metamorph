package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/spf13/cobra"
)

var epicCmd = &cobra.Command{
	Use:     "epic",
	Aliases: []string{"epics"},
	Short:   "Manage epics via the control plane",
	RunE: func(cmd *cobra.Command, args []string) error {
		return epicListCmd.RunE(cmd, args)
	},
}

var epicListCmd = &cobra.Command{
	Use:   "list",
	Short: "List epics for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		projectName, err := resolveProjectName(cmd)
		if err != nil {
			return err
		}

		url := controlPlaneURL() + "/api/epics?project=" + projectName

		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("control plane error (%d): %s", resp.StatusCode, string(body))
		}

		var epics []controlplane.Epic
		if err := json.NewDecoder(resp.Body).Decode(&epics); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		if len(epics) == 0 {
			fmt.Println("No epics found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tCREATED")
		for _, e := range epics {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				e.ID,
				truncate(e.Title, 50),
				e.Status,
				formatRelativeTime(e.CreatedAt),
			)
		}
		_ = w.Flush()
		return nil
	},
}

var epicAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Create an epic and decompose it into tasks via LLM",
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

		body := map[string]string{
			"title":       title,
			"description": description,
			"project":     projectName,
		}
		jsonBody, _ := json.Marshal(body)

		fmt.Println("Decomposing epic via LLM...")

		resp, err := http.Post(controlPlaneURL()+"/api/epics", "application/json", bytes.NewBuffer(jsonBody))
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to create epic (%d): %s", resp.StatusCode, string(respBody))
		}

		var result struct {
			Epic  controlplane.Epic   `json:"epic"`
			Tasks []controlplane.Task `json:"tasks"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		fmt.Printf("Created epic %s: %s\n", result.Epic.ID, result.Epic.Title)
		fmt.Printf("Decomposed into %d tasks (status: pending_review)\n", len(result.Tasks))
		for i, t := range result.Tasks {
			fmt.Printf("  %d. [P%d] %s\n", i+1, t.Priority, t.Title)
		}
		fmt.Printf("\nApprove all tasks: metamorph epic approve %s\n", result.Epic.ID)
		return nil
	},
}

var epicShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of an epic and its tasks",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		epicID := args[0]

		// Fetch epic.
		resp, err := http.Get(controlPlaneURL() + "/api/epics/" + epicID)
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("epic %q not found", epicID)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("control plane error (%d): %s", resp.StatusCode, string(body))
		}

		var epic controlplane.Epic
		if err := json.NewDecoder(resp.Body).Decode(&epic); err != nil {
			return fmt.Errorf("failed to decode epic: %w", err)
		}

		fmt.Printf("ID:          %s\n", epic.ID)
		fmt.Printf("Title:       %s\n", epic.Title)
		fmt.Printf("Status:      %s\n", epic.Status)
		if epic.Description != "" {
			fmt.Printf("Description: %s\n", epic.Description)
		}
		fmt.Printf("Created:     %s\n", epic.CreatedAt.Format("2006-01-02 15:04:05"))

		// Fetch tasks.
		tresp, err := http.Get(controlPlaneURL() + "/api/epics/" + epicID + "/tasks")
		if err != nil {
			return nil // don't fail if tasks fetch fails
		}
		defer tresp.Body.Close()

		var tasks []controlplane.Task
		if err := json.NewDecoder(tresp.Body).Decode(&tasks); err != nil {
			return nil
		}

		if len(tasks) > 0 {
			fmt.Printf("\nTasks (%d):\n", len(tasks))
			for i, t := range tasks {
				promptBadge := ""
				if t.PromptStatus != "" {
					promptBadge = fmt.Sprintf(" [%s]", t.PromptStatus)
				}
				fmt.Printf("  %d. [P%d] %s  %s%s\n", i+1, t.Priority, t.Title, t.Status, promptBadge)
			}
		}

		return nil
	},
}

var epicApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Approve all tasks in an epic (bulk approve)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running. Start it with: metamorph start")
		}

		epicID := args[0]

		resp, err := http.Post(controlPlaneURL()+"/api/epics/"+epicID+"/approve", "application/json", nil)
		if err != nil {
			return fmt.Errorf("failed to connect to control plane: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to approve epic (%d): %s", resp.StatusCode, string(body))
		}

		fmt.Printf("Epic %s approved — all tasks are now claimable by agents\n", epicID)
		return nil
	},
}

func init() {
	epicAddCmd.Flags().String("description", "", "Epic description")

	epicCmd.AddCommand(epicListCmd)
	epicCmd.AddCommand(epicAddCmd)
	epicCmd.AddCommand(epicShowCmd)
	epicCmd.AddCommand(epicApproveCmd)
	rootCmd.AddCommand(epicCmd)
}

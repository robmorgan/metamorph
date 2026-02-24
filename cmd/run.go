package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/robmorgan/metamorph/assets"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a one-shot agent task",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured for project %q", projectName)
		}
		// Use primary repo for AGENT_PROMPT.md path.
		repoPath := cfg.Repos[0].Path

		oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if oauthToken == "" && apiKey == "" {
			return fmt.Errorf("no credentials found: set CLAUDE_CODE_OAUTH_TOKEN (Claude Pro/Max) or ANTHROPIC_API_KEY")
		}

		if _, err := exec.LookPath("claude"); err != nil {
			return fmt.Errorf("'claude' not found in PATH (install Claude Code first)")
		}

		once, _ := cmd.Flags().GetBool("once")
		role, _ := cmd.Flags().GetString("role")
		taskID, _ := cmd.Flags().GetString("task")
		taskQueue, _ := cmd.Flags().GetBool("task-queue")

		// Clone all upstreams to temp dir.
		tmpDir, err := os.MkdirTemp("", "metamorph-run-*")
		if err != nil {
			return fmt.Errorf("failed to create temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Clone primary repo as agent-work.
		primaryLabel := config.RepoLabel(cfg.Repos[0])
		primaryUpstream := constants.UpstreamRepoPath(projectName, primaryLabel)
		agentDir := tmpDir + "/agent-work"
		if err := gitops.CloneForAgent(primaryUpstream, 0, agentDir); err != nil {
			return fmt.Errorf("failed to clone upstream for %q: %w", primaryLabel, err)
		}

		// Clone additional repos alongside.
		for _, repo := range cfg.Repos[1:] {
			label := config.RepoLabel(repo)
			upstreamPath := constants.UpstreamRepoPath(projectName, label)
			destDir := tmpDir + "/" + label
			if err := gitops.CloneForAgent(upstreamPath, 0, destDir); err != nil {
				return fmt.Errorf("failed to clone upstream for %q: %w", label, err)
			}
		}

		// Handle SIGINT for cleanup.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		// Single task mode: claim a specific task, run once, complete it.
		if taskID != "" {
			fmt.Printf("Running agent for task %s (role=%s, model=%s)...\n", taskID, role, cfg.Agents.Model)
			return runSingleTask(taskID, projectName, role, cfg, repoPath, agentDir, oauthToken, apiKey)
		}

		// Task queue mode: poll for tasks from control plane.
		if taskQueue {
			fmt.Printf("Running agent in task queue mode (role=%s, model=%s)...\n", role, cfg.Agents.Model)
			done := make(chan struct{})
			go func() {
				defer close(done)
				runTaskQueueLoop(projectName, role, cfg, repoPath, agentDir, oauthToken, apiKey)
			}()
			select {
			case <-sigCh:
				fmt.Println("\nInterrupted. Cleaning up...")
				return nil
			case <-done:
				return nil
			}
		}

		// Default: static prompt mode.
		fmt.Printf("Running agent (role=%s, model=%s)...\n", role, cfg.Agents.Model)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				runStaticPromptSession(role, cfg, repoPath, agentDir, oauthToken, apiKey)

				if once {
					return
				}
			}
		}()

		select {
		case <-sigCh:
			fmt.Println("\nInterrupted. Cleaning up...")
			return nil
		case <-done:
			return nil
		}
	},
}

func init() {
	runCmd.Flags().Bool("once", false, "Run a single agent iteration and exit")
	runCmd.Flags().String("role", "developer", "Agent role to use")
	runCmd.Flags().String("task", "", "Run a single specific task by ID")
	runCmd.Flags().Bool("task-queue", false, "Poll for tasks from control plane queue")
	rootCmd.AddCommand(runCmd)
}

// runSingleTask claims a specific task, runs Claude, and marks it complete.
func runSingleTask(taskID, projectName, role string, cfg *config.Config, repoPath, agentDir, oauthToken, apiKey string) error {
	cpURL := controlPlaneURL()

	// Claim the task via control plane.
	claimBody, _ := json.Marshal(map[string]int{"agent_id": 0})
	resp, err := http.Post(cpURL+"/api/tasks/"+taskID+"/claim", "application/json", bytes.NewBuffer(claimBody))
	if err != nil {
		// If daemon isn't running, try direct DB access.
		return runSingleTaskDirect(taskID, role, cfg, repoPath, agentDir, oauthToken, apiKey)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to claim task %s: %s", taskID, string(body))
	}

	// Fetch task details.
	resp, err = http.Get(cpURL + "/api/tasks/" + taskID)
	if err != nil {
		return fmt.Errorf("failed to get task details: %w", err)
	}
	defer resp.Body.Close()

	var task controlplane.Task
	json.NewDecoder(resp.Body).Decode(&task)

	// Build prompt with task.
	prompt := buildTaskPrompt(role, cfg, repoPath, &task)

	// Run Claude.
	runClaudeSession(prompt, cfg, agentDir, oauthToken, apiKey)

	// Auto-commit and push.
	autoCommitAndPush(agentDir, fmt.Sprintf("metamorph: work on task %s", taskID))

	// Mark complete.
	completeBody, _ := json.Marshal(map[string]string{"summary": "Completed locally via metamorph run --task"})
	http.Post(cpURL+"/api/tasks/"+taskID+"/complete", "application/json", bytes.NewBuffer(completeBody))

	fmt.Printf("Task %s completed\n", taskID)
	return nil
}

// runSingleTaskDirect accesses the SQLite DB directly when daemon isn't running.
func runSingleTaskDirect(taskID, role string, cfg *config.Config, repoPath, agentDir, oauthToken, apiKey string) error {
	dbPath := constants.ControlPlaneDBPath()
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Claim the task.
	if err := store.ClaimTask(ctx, taskID, 0); err != nil {
		return fmt.Errorf("failed to claim task: %w", err)
	}

	// Get task details.
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	// Build prompt with task.
	prompt := buildTaskPrompt(role, cfg, repoPath, task)

	// Run Claude.
	runClaudeSession(prompt, cfg, agentDir, oauthToken, apiKey)

	// Auto-commit and push.
	autoCommitAndPush(agentDir, fmt.Sprintf("metamorph: work on task %s", taskID))

	// Mark complete.
	if err := store.CompleteTask(ctx, taskID, "Completed locally via metamorph run --task"); err != nil {
		slog.Warn("failed to mark task complete", "error", err)
	}

	fmt.Printf("Task %s completed\n", taskID)
	return nil
}

// runTaskQueueLoop polls the control plane for tasks and executes them.
func runTaskQueueLoop(projectName, role string, cfg *config.Config, repoPath, agentDir, oauthToken, apiKey string) {
	cpURL := controlPlaneURL()

	for {
		// Pull latest.
		pullCmd := exec.Command("git", "pull", "--rebase", "origin", "HEAD")
		pullCmd.Dir = agentDir
		_ = pullCmd.Run()

		// Poll for next task.
		reqBody, _ := json.Marshal(map[string]interface{}{
			"agent_id": 0,
			"role":     role,
			"project":  projectName,
		})
		resp, err := http.Post(cpURL+"/api/tasks/next", "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			slog.Error("failed to poll for tasks", "error", err)
			time.Sleep(10 * time.Second)
			continue
		}

		if resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			slog.Info("no tasks available, sleeping 10s")
			time.Sleep(10 * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			slog.Error("control plane error", "status", resp.StatusCode, "body", string(body))
			time.Sleep(10 * time.Second)
			continue
		}

		var task controlplane.Task
		json.NewDecoder(resp.Body).Decode(&task)
		resp.Body.Close()

		fmt.Printf("Claimed task %s: %s\n", task.ID, task.Title)

		// Build prompt with task.
		prompt := buildTaskPrompt(role, cfg, repoPath, &task)

		sessionStart := time.Now()

		// Run Claude.
		runClaudeSession(prompt, cfg, agentDir, oauthToken, apiKey)

		sessionDuration := time.Since(sessionStart)

		// Auto-commit and push.
		autoCommitAndPush(agentDir, fmt.Sprintf("metamorph: work on task %s", task.ID))

		// Mark complete.
		completeBody, _ := json.Marshal(map[string]string{
			"summary": fmt.Sprintf("Completed by local agent (role=%s, duration=%s)", role, sessionDuration.Round(time.Second)),
		})
		http.Post(cpURL+"/api/tasks/"+task.ID+"/complete", "application/json", bytes.NewBuffer(completeBody))

		fmt.Printf("Task %s completed\n", task.ID)

		if sessionDuration < 30*time.Second {
			slog.Warn("session lasted under 30s, backing off 5m", "duration", sessionDuration)
			time.Sleep(5 * time.Minute)
		} else {
			time.Sleep(5 * time.Second)
		}
	}
}

// runStaticPromptSession runs a single static-prompt agent session (original behavior).
func runStaticPromptSession(role string, cfg *config.Config, repoPath, agentDir, oauthToken, apiKey string) {
	// Pull latest changes.
	pullCmd := exec.Command("git", "pull", "--rebase", "origin", "HEAD")
	pullCmd.Dir = agentDir
	_ = pullCmd.Run()

	// Build prompt from system + user prompt.
	userPromptPath := repoPath + "/" + constants.AgentPromptFile
	userPromptData, err := os.ReadFile(userPromptPath)
	if err != nil {
		slog.Error("failed to read user agent prompt", "error", err)
		return
	}

	combined := assets.SystemPrompt + "\n" + string(userPromptData)
	prompt := os.Expand(combined, func(key string) string {
		switch key {
		case "AGENT_ID":
			return "0"
		case "AGENT_ROLE":
			return role
		case "AGENT_MODEL":
			return cfg.Agents.Model
		default:
			return os.Getenv(key)
		}
	})

	sessionStart := time.Now()

	runClaudeSession(prompt, cfg, agentDir, oauthToken, apiKey)

	sessionDuration := time.Since(sessionStart)

	autoCommitAndPush(agentDir, "metamorph: auto-commit uncommitted changes")

	if sessionDuration < 30*time.Second {
		slog.Warn("session lasted under 30s (possible rate limit), backing off 5m", "duration", sessionDuration)
		time.Sleep(5 * time.Minute)
	} else {
		slog.Info("sleeping before next iteration", "duration", sessionDuration)
		time.Sleep(5 * time.Second)
	}
}

// buildTaskPrompt constructs the full prompt including task details.
func buildTaskPrompt(role string, cfg *config.Config, repoPath string, task *controlplane.Task) string {
	userPromptPath := repoPath + "/" + constants.AgentPromptFile
	userPromptData, _ := os.ReadFile(userPromptPath)

	systemPrompt := os.Expand(assets.SystemPrompt, func(key string) string {
		switch key {
		case "AGENT_ID":
			return "0"
		case "AGENT_ROLE":
			return role
		case "AGENT_MODEL":
			return cfg.Agents.Model
		default:
			return os.Getenv(key)
		}
	})

	taskHeader := fmt.Sprintf(`
## Your Current Task
- **Task ID**: %s
- **Title**: %s
- **Description**: %s

Focus on completing this specific task. Plan your approach, implement the solution, and write tests.
When done, commit your changes with the task ID in the commit message.
`, task.ID, task.Title, task.Description)

	return systemPrompt + taskHeader + "\n" + string(userPromptData)
}

// runClaudeSession executes a claude session with the given prompt.
func runClaudeSession(prompt string, cfg *config.Config, agentDir, oauthToken, apiKey string) {
	claudeCmd := exec.Command("claude", "--print", "--model", cfg.Agents.Model, prompt)
	claudeCmd.Dir = agentDir
	claudeCmd.Stdout = os.Stdout
	claudeCmd.Stderr = os.Stderr
	claudeEnv := os.Environ()
	if oauthToken != "" {
		claudeEnv = append(claudeEnv, "CLAUDE_CODE_OAUTH_TOKEN="+oauthToken)
	} else if apiKey != "" {
		claudeEnv = append(claudeEnv, "ANTHROPIC_API_KEY="+apiKey)
	}
	claudeCmd.Env = claudeEnv

	if err := claudeCmd.Run(); err != nil {
		slog.Error("claude exited with error", "error", err)
	}
}

// autoCommitAndPush auto-commits any uncommitted changes and pushes.
func autoCommitAndPush(agentDir, commitMsg string) {
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = agentDir
	statusOut, _ := statusCmd.CombinedOutput()
	if strings.TrimSpace(string(statusOut)) != "" {
		slog.Info("auto-committing uncommitted changes from agent session")
		addCmd := exec.Command("git", "add", "-A")
		addCmd.Dir = agentDir
		if err := addCmd.Run(); err != nil {
			slog.Warn("git add -A failed", "error", err)
		} else {
			commitCmd := exec.Command("git", "commit", "-m", commitMsg)
			commitCmd.Dir = agentDir
			if err := commitCmd.Run(); err != nil {
				slog.Warn("auto-commit failed", "error", err)
			}
		}
	}

	pushCmd := exec.Command("git", "push", "origin", "HEAD")
	pushCmd.Dir = agentDir
	if err := pushCmd.Run(); err != nil {
		slog.Warn("push failed, pulling and retrying", "error", err)
		retryPull := exec.Command("git", "pull", "--rebase", "origin", "HEAD")
		retryPull.Dir = agentDir
		if err := retryPull.Run(); err != nil {
			slog.Warn("rebase conflict, aborting and falling back to merge", "error", err)
			abortCmd := exec.Command("git", "rebase", "--abort")
			abortCmd.Dir = agentDir
			_ = abortCmd.Run()
			mergePull := exec.Command("git", "pull", "--no-rebase", "-X", "theirs", "origin", "HEAD")
			mergePull.Dir = agentDir
			_ = mergePull.Run()
		}
		retryPush := exec.Command("git", "push", "origin", "HEAD")
		retryPush.Dir = agentDir
		_ = retryPush.Run()
	}
}

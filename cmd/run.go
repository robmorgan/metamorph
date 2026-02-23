package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/robmorgan/metamorph/assets"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
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

		fmt.Printf("Running agent (role=%s, model=%s)...\n", role, cfg.Agents.Model)

		// Handle SIGINT for cleanup.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				// Pull latest changes.
				pullCmd := exec.Command("git", "pull", "--rebase", "origin", "HEAD")
				pullCmd.Dir = agentDir
				_ = pullCmd.Run() // best effort

				// Read the system prompt (embedded) and user prompt (project dir),
				// then concatenate and expand ${VAR} placeholders.
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

				// Execute claude.
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

				sessionStart := time.Now()

				if err := claudeCmd.Run(); err != nil {
					slog.Error("claude exited with error", "error", err)
				}

				sessionDuration := time.Since(sessionStart)

				// Auto-commit any uncommitted changes left by the agent.
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
						commitCmd := exec.Command("git", "commit", "-m", "metamorph: auto-commit uncommitted changes")
						commitCmd.Dir = agentDir
						if err := commitCmd.Run(); err != nil {
							slog.Warn("auto-commit failed", "error", err)
						}
					}
				}

				// Push any commits the agent made during this session.
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

				if once {
					return
				}

				if sessionDuration < 30*time.Second {
					slog.Warn("session lasted under 30s (possible rate limit), backing off 5m", "duration", sessionDuration)
					time.Sleep(5 * time.Minute)
				} else {
					slog.Info("sleeping before next iteration", "duration", sessionDuration)
					time.Sleep(5 * time.Second)
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
	rootCmd.AddCommand(runCmd)
}

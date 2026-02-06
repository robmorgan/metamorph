package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a one-shot agent task",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		cfg, err := loadConfig(projectDir)
		if err != nil {
			return err
		}

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

		upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)

		// Clone upstream to temp dir.
		tmpDir, err := os.MkdirTemp("", "metamorph-run-*")
		if err != nil {
			return fmt.Errorf("failed to create temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		agentDir := filepath.Join(tmpDir, "agent-work")
		if err := gitops.CloneForAgent(upstreamPath, 0, agentDir); err != nil {
			return fmt.Errorf("failed to clone upstream: %w", err)
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

				// Read the agent prompt and expand ${VAR} placeholders.
				promptPath := filepath.Join(agentDir, constants.AgentPromptFile)
				promptData, err := os.ReadFile(promptPath)
				if err != nil {
					slog.Error("failed to read agent prompt", "error", err)
					return
				}

				prompt := os.Expand(string(promptData), func(key string) string {
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

				if err := claudeCmd.Run(); err != nil {
					slog.Error("claude exited with error", "error", err)
				}

				if once {
					return
				}

				slog.Info("sleeping before next iteration")
				time.Sleep(5 * time.Second)
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

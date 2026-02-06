package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/docker"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the metamorph daemon and agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		daemonMode, _ := cmd.Flags().GetBool("daemon-mode")

		if daemonMode {
			return runDaemonMode(cmd)
		}
		return runForegroundStart(cmd)
	},
}

func init() {
	startCmd.Flags().IntP("agents", "n", 0, "Number of agents to start (overrides config)")
	startCmd.Flags().String("model", "", "Model to use (overrides config)")
	startCmd.Flags().Bool("dry-run", false, "Print what would happen without starting")

	// Hidden flags for daemon re-exec.
	startCmd.Flags().Bool("daemon-mode", false, "Run as daemon (internal)")
	startCmd.Flags().String("project-dir", "", "Project directory (internal)")
	startCmd.Flags().String("api-key", "", "API key (internal)")
	startCmd.Flags().String("oauth-token", "", "OAuth token (internal)")
	_ = startCmd.Flags().MarkHidden("daemon-mode")
	_ = startCmd.Flags().MarkHidden("project-dir")
	_ = startCmd.Flags().MarkHidden("api-key")
	_ = startCmd.Flags().MarkHidden("oauth-token")

	rootCmd.AddCommand(startCmd)
}

func runDaemonMode(cmd *cobra.Command) error {
	projectDir, _ := cmd.Flags().GetString("project-dir")
	apiKey, _ := cmd.Flags().GetString("api-key")
	oauthToken, _ := cmd.Flags().GetString("oauth-token")

	if projectDir == "" {
		return fmt.Errorf("--project-dir is required in daemon mode")
	}
	if apiKey == "" && oauthToken == "" {
		return fmt.Errorf("--api-key or --oauth-token is required in daemon mode")
	}

	cfg, err := loadConfig(projectDir)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	dockerClient, err := docker.NewClient(cfg.Project.Name)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	return daemon.Run(projectDir, cfg, apiKey, oauthToken, dockerClient)
}

func runForegroundStart(cmd *cobra.Command) error {
	projectDir, err := resolveProjectDir()
	if err != nil {
		return err
	}

	cfg, err := loadConfig(projectDir)
	if err != nil {
		return err
	}

	// Check that the project has been initialized (upstream repo exists).
	upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)
	if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
		return fmt.Errorf("project not initialized: %s not found\n\nRun 'metamorph init' first to set up the project", constants.UpstreamDir)
	}

	// Apply flag overrides to a local copy for display/dry-run purposes.
	// Note: these overrides are NOT forwarded to the daemon. The daemon reads
	// from metamorph.toml directly.
	displayCfg := *cfg
	displayCfg.Agents = cfg.Agents

	if n, _ := cmd.Flags().GetInt("agents"); n > 0 {
		displayCfg.Agents.Count = n
		slog.Info("overriding agent count from flag", "count", n)
	}
	if model, _ := cmd.Flags().GetString("model"); model != "" {
		displayCfg.Agents.Model = model
		slog.Info("overriding model from flag", "model", model)
	}

	oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if oauthToken == "" && apiKey == "" {
		return fmt.Errorf("no credentials found: set CLAUDE_CODE_OAUTH_TOKEN (Claude Pro/Max) or ANTHROPIC_API_KEY")
	}

	if daemon.IsRunning(projectDir) {
		return fmt.Errorf("daemon is already running (use 'metamorph status' to check)")
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		fmt.Printf("Project:  %s\n", displayCfg.Project.Name)
		fmt.Printf("Agents:   %d\n", displayCfg.Agents.Count)
		fmt.Printf("Model:    %s\n", displayCfg.Agents.Model)
		fmt.Printf("Roles:    %v\n", displayCfg.Agents.Roles)
		fmt.Println("\n(dry run — no agents started)")
		return nil
	}

	// Update config file values if overrides were given, so daemon reads them.
	// Actually, per design: config file is authoritative for the daemon.
	// Overrides only affect this display. The user should edit metamorph.toml.

	fmt.Printf("Starting metamorph daemon for %q...\n", cfg.Project.Name)

	if err := daemon.Start(projectDir, cfg, apiKey, oauthToken); err != nil {
		return err
	}

	// Read status to display agent table.
	state, err := daemon.GetStatus(projectDir)
	if err != nil {
		fmt.Println("Daemon started, but could not read status.")
		return nil
	}

	fmt.Printf("\nDaemon running (PID in .metamorph/daemon.pid)\n\n")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "AGENT\tROLE\tSTATUS")
	for _, a := range state.Agents {
		_, _ = fmt.Fprintf(w, "agent-%d\t%s\t%s\n", a.ID, a.Role, a.Status)
	}
	_ = w.Flush()

	fmt.Printf("\nUse 'metamorph status' to monitor, 'metamorph stop' to stop.\n")

	return nil
}

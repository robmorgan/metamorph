package cmd

import (
	"fmt"
	"os"

	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/docker"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the system-wide metamorph daemon",
	Long:  "Control the background daemon that manages all metamorph projects.",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the metamorph daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		daemonMode, _ := cmd.Flags().GetBool("daemon-mode")
		if daemonMode {
			return runDaemonProcess(cmd)
		}
		return startDaemon()
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the metamorph daemon and all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running")
		}

		fmt.Println("Stopping daemon...")
		if err := daemon.Stop(); err != nil {
			return err
		}
		fmt.Println("Daemon stopped.")
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon and project overview",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			fmt.Println("Daemon is not running.")
			return nil
		}
		fmt.Println("Daemon is running.")
		return nil
	},
}

func init() {
	// Hidden flags used by daemon re-exec.
	daemonStartCmd.Flags().Bool("daemon-mode", false, "Run as background daemon (internal)")
	daemonStartCmd.Flags().String("api-key", "", "API key (internal)")
	daemonStartCmd.Flags().String("oauth-token", "", "OAuth token (internal)")
	_ = daemonStartCmd.Flags().MarkHidden("daemon-mode")
	_ = daemonStartCmd.Flags().MarkHidden("api-key")
	_ = daemonStartCmd.Flags().MarkHidden("oauth-token")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	rootCmd.AddCommand(daemonCmd)
}

// runDaemonProcess is called when re-exec'd with --daemon-mode. It runs the
// main daemon loop (broker, agent management, monitoring).
func runDaemonProcess(cmd *cobra.Command) error {
	apiKey, _ := cmd.Flags().GetString("api-key")
	oauthToken, _ := cmd.Flags().GetString("oauth-token")

	if apiKey == "" && oauthToken == "" {
		return fmt.Errorf("--api-key or --oauth-token is required in daemon mode")
	}

	globalCfg, err := config.LoadGlobal()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	dockerClient, err := docker.NewClient("metamorph")
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	return daemon.Run(globalCfg, apiKey, oauthToken, dockerClient)
}

// startDaemon launches the daemon as a background subprocess.
func startDaemon() error {
	oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if oauthToken == "" && apiKey == "" {
		return fmt.Errorf("no credentials found: set CLAUDE_CODE_OAUTH_TOKEN (Claude Pro/Max) or ANTHROPIC_API_KEY")
	}

	if daemon.IsRunning() {
		return fmt.Errorf("daemon is already running (use 'metamorph daemon status' to check)")
	}

	fmt.Println("Starting metamorph daemon...")
	if err := daemon.Start(apiKey, oauthToken); err != nil {
		return err
	}

	fmt.Println("Daemon started successfully.")
	return nil
}

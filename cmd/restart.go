package cmd

import (
	"fmt"
	"os"

	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the metamorph daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate credentials before stopping to avoid leaving the daemon down.
		oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if oauthToken == "" && apiKey == "" {
			return fmt.Errorf("no credentials found: set CLAUDE_CODE_OAUTH_TOKEN (Claude Pro/Max) or ANTHROPIC_API_KEY")
		}

		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running (use 'metamorph start' to start it)")
		}

		fmt.Println("Stopping daemon...")
		if err := daemon.Stop(); err != nil {
			return fmt.Errorf("failed to stop daemon: %w", err)
		}

		fmt.Println("Starting daemon...")
		if err := daemon.Start(apiKey, oauthToken); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}

		fmt.Println("Daemon restarted successfully.")

		globalCfg, err := config.LoadGlobal()
		if err == nil {
			fmt.Printf("\nWeb UI: http://localhost:%d\n", globalCfg.Daemon.Port)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restartCmd)
}

package cmd

import (
	"fmt"
	"time"

	"github.com/robmorgan/metamorph/internal/notify"
	"github.com/spf13/cobra"
)

var notifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "Manage webhook notifications",
	RunE: func(cmd *cobra.Command, args []string) error {
		testFlag, _ := cmd.Flags().GetBool("test")
		if !testFlag {
			return cmd.Help()
		}

		_, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if cfg.Notifications.WebhookURL == "" {
			return fmt.Errorf("no webhook URL configured (set notifications.webhook_url in config.json)")
		}

		event := notify.Event{
			Type:      "test",
			Project:   cfg.Project.Name,
			Message:   "Test notification from metamorph",
			Timestamp: time.Now().UTC(),
		}

		fmt.Printf("Sending test notification to %s...\n", cfg.Notifications.WebhookURL)

		if err := notify.Send(cfg.Notifications.WebhookURL, event); err != nil {
			return fmt.Errorf("notification failed: %w", err)
		}

		fmt.Println("Notification sent successfully.")
		return nil
	},
}

func init() {
	notifyCmd.Flags().Bool("test", false, "Send a test notification to the webhook")
	rootCmd.AddCommand(notifyCmd)
}

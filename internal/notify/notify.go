package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Event types.
const (
	EventAgentCrashed  = "agent_crashed"
	EventCommitsPushed = "commits_pushed"
	EventStaleLock     = "stale_lock"
	EventTestFailure   = "test_failure"
)

// Event represents a notification to be sent to a webhook.
type Event struct {
	Type      string                 `json:"event"`
	AgentID   int                    `json:"agent_id"`
	AgentRole string                 `json:"agent_role"`
	Project   string                 `json:"project"`
	Message   string                 `json:"message"`
	Timestamp time.Time              `json:"timestamp"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// Send POSTs the event as JSON to webhookURL with a 5s timeout.
// Returns nil if webhookURL is empty (notifications disabled).
func Send(webhookURL string, event Event) error {
	if webhookURL == "" {
		return nil
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("notify: failed to marshal event: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Error("notify: webhook request failed", "url", webhookURL, "error", err)
		return fmt.Errorf("notify: webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Error("notify: webhook returned error", "url", webhookURL, "status", resp.StatusCode)
		return fmt.Errorf("notify: webhook returned status %d", resp.StatusCode)
	}

	return nil
}

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/spf13/cobra"
)

// Stream-JSON event types emitted by Claude Code with --output-format stream-json.
type streamEvent struct {
	Type  string     `json:"type"`
	Event eventInner `json:"event"`
}

type eventInner struct {
	Type         string        `json:"type"`
	ContentBlock *contentBlock `json:"content_block,omitempty"`
	Delta        *delta        `json:"delta,omitempty"`
	Error        *errorInfo    `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type delta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type errorInfo struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// formatLogLine parses a stream-json line and returns a human-readable string.
// Non-JSON lines (e.g. entrypoint echo output) are returned as-is.
// Returns the formatted string and whether it should be printed (empty means skip).
func formatLogLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}

	if trimmed[0] != '{' {
		return line, true
	}

	var ev streamEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return line, true
	}

	inner := ev.Event

	switch inner.Type {
	case "content_block_start":
		if inner.ContentBlock != nil && inner.ContentBlock.Type == "tool_use" {
			return "[tool] " + inner.ContentBlock.Name, true
		}
		return "", false

	case "content_block_delta":
		if inner.Delta == nil {
			return "", false
		}
		switch inner.Delta.Type {
		case "text_delta":
			return inner.Delta.Text, true
		case "thinking_delta", "input_json_delta":
			return "", false
		}
		return "", false

	case "content_block_stop", "message_start", "message_delta", "message_stop", "ping":
		return "", false

	case "error":
		if inner.Error != nil {
			return "[error] " + inner.Error.Type + " - " + inner.Error.Message, true
		}
		return "[error] unknown", true

	default:
		return "", false
	}
}

var logsCmd = &cobra.Command{
	Use:   "logs <agent-id>",
	Short: "View agent logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid agent ID %q: must be a number", args[0])
		}

		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")

		logDir := filepath.Join(projectDir, constants.AgentLogDir, fmt.Sprintf("agent-%d", agentID))

		// Find the latest session log file.
		logFile, err := findLatestLog(logDir)
		if err != nil {
			return err
		}

		// Read the file.
		data, err := os.ReadFile(logFile)
		if err != nil {
			return fmt.Errorf("failed to read log file: %w", err)
		}

		// Print last N lines.
		lines := strings.Split(string(data), "\n")
		start := 0
		if tail > 0 && tail < len(lines) {
			start = len(lines) - tail
		}
		for _, line := range lines[start:] {
			if formatted, ok := formatLogLine(line); ok {
				fmt.Println(formatted)
			}
		}

		if !follow {
			return nil
		}

		// Follow mode: poll for new content.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		offset := int64(len(data))
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-sigCh:
				return nil
			case <-ticker.C:
				f, err := os.Open(logFile)
				if err != nil {
					continue
				}

				info, err := f.Stat()
				if err != nil {
					_ = f.Close()
					continue
				}

				if info.Size() <= offset {
					_ = f.Close()
					continue
				}

				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					_ = f.Close()
					continue
				}

				newData, err := io.ReadAll(f)
				_ = f.Close()
				if err != nil {
					continue
				}

				if len(newData) > 0 {
					offset += int64(len(newData))
					newLines := strings.Split(string(newData), "\n")
					for _, line := range newLines {
						if formatted, ok := formatLogLine(line); ok {
							fmt.Println(formatted)
						}
					}
				}
			}
		}
	},
}

func init() {
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
	logsCmd.Flags().Int("tail", 50, "Number of lines to show from the end")
	rootCmd.AddCommand(logsCmd)
}

// findLatestLog finds the most recent session-*.log file in the given directory.
func findLatestLog(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no logs found for this agent (directory does not exist)")
		}
		return "", fmt.Errorf("failed to read log directory: %w", err)
	}

	var logFiles []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "session-") && strings.HasSuffix(e.Name(), ".log") {
			logFiles = append(logFiles, e.Name())
		}
	}

	if len(logFiles) == 0 {
		return "", fmt.Errorf("no session log files found in %s", dir)
	}

	// Sort by session number (session-1.log, session-2.log, ...).
	sort.Slice(logFiles, func(i, j int) bool {
		ni := extractSessionNumber(logFiles[i])
		nj := extractSessionNumber(logFiles[j])
		return ni < nj
	})

	return filepath.Join(dir, logFiles[len(logFiles)-1]), nil
}

// extractSessionNumber parses the number from "session-N.log".
func extractSessionNumber(name string) int {
	name = strings.TrimPrefix(name, "session-")
	name = strings.TrimSuffix(name, ".log")
	n, _ := strconv.Atoi(name)
	return n
}

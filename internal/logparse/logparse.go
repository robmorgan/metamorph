package logparse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Stream-JSON event types emitted by Claude Code with --output-format stream-json.
type StreamEvent struct {
	Type  string     `json:"type"`
	Event EventInner `json:"event"`
}

type EventInner struct {
	Type         string        `json:"type"`
	ContentBlock *ContentBlock `json:"content_block,omitempty"`
	Delta        *Delta        `json:"delta,omitempty"`
	Error        *ErrorInfo    `json:"error,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type Delta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ErrorInfo struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// LogLineType classifies a parsed log line for styling purposes.
type LogLineType int

const (
	LogLineText   LogLineType = iota // Claude's text responses
	LogLineTool                      // Tool calls (Edit, Bash, etc.)
	LogLineError                     // Error messages
	LogLineSystem                    // Entrypoint output, timestamps, git info
)

// FormatLogLineTyped parses a stream-json line and returns a human-readable
// string, its type classification, and whether it should be displayed.
func FormatLogLineTyped(line string) (string, LogLineType, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", LogLineText, false
	}

	if trimmed[0] != '{' {
		return line, LogLineSystem, true
	}

	var ev StreamEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return line, LogLineSystem, true
	}

	inner := ev.Event

	switch inner.Type {
	case "content_block_start":
		if inner.ContentBlock != nil && inner.ContentBlock.Type == "tool_use" {
			return "[tool] " + inner.ContentBlock.Name, LogLineTool, true
		}
		return "", LogLineText, false

	case "content_block_delta":
		if inner.Delta == nil {
			return "", LogLineText, false
		}
		switch inner.Delta.Type {
		case "text_delta":
			return inner.Delta.Text, LogLineText, true
		case "thinking_delta", "input_json_delta":
			return "", LogLineText, false
		}
		return "", LogLineText, false

	case "content_block_stop", "message_start", "message_delta", "message_stop", "ping":
		return "", LogLineText, false

	case "error":
		if inner.Error != nil {
			return "[error] " + inner.Error.Type + " - " + inner.Error.Message, LogLineError, true
		}
		return "[error] unknown", LogLineError, true

	default:
		return "", LogLineText, false
	}
}

// FormatLogLine parses a stream-json line and returns a human-readable string.
// Non-JSON lines (e.g. entrypoint echo output) are returned as-is.
// Returns the formatted string and whether it should be printed (empty means skip).
func FormatLogLine(line string) (string, bool) {
	s, _, ok := FormatLogLineTyped(line)
	return s, ok
}

// FindLatestLog finds the most recent session-*.log file in the given directory.
func FindLatestLog(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &LogNotFoundError{Message: "no logs found for this agent (directory does not exist)"}
		}
		return "", &LogNotFoundError{Message: "failed to read log directory: " + err.Error()}
	}

	var logFiles []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "session-") && strings.HasSuffix(e.Name(), ".log") {
			logFiles = append(logFiles, e.Name())
		}
	}

	if len(logFiles) == 0 {
		return "", &LogNotFoundError{Message: "no session log files found in " + dir}
	}

	// Sort by session number (session-1.log, session-2.log, ...).
	sort.Slice(logFiles, func(i, j int) bool {
		ni := ExtractSessionNumber(logFiles[i])
		nj := ExtractSessionNumber(logFiles[j])
		return ni < nj
	})

	return filepath.Join(dir, logFiles[len(logFiles)-1]), nil
}

// ExtractSessionNumber parses the number from "session-N.log".
func ExtractSessionNumber(name string) int {
	name = strings.TrimPrefix(name, "session-")
	name = strings.TrimSuffix(name, ".log")
	n, _ := strconv.Atoi(name)
	return n
}

// LogNotFoundError indicates that no log file was found.
type LogNotFoundError struct {
	Message string
}

func (e *LogNotFoundError) Error() string {
	return e.Message
}

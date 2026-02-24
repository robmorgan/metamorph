package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultModel   = "claude-haiku-4-5-20251001"
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	maxTokens      = 4096
)

// Client is a minimal Anthropic Messages API client.
type Client struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// NewClient creates a new LLM client with the given API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		model:   defaultModel,
		baseURL: defaultBaseURL,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// messagesRequest is the Anthropic Messages API request body.
type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messagesResponse is the Anthropic Messages API response.
type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Chat sends a single user message with an optional system prompt and returns
// the assistant's text response.
func (c *Client) Chat(ctx context.Context, system, userMsg string) (string, error) {
	reqBody := messagesRequest{
		Model:     c.model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []message{{Role: "user", Content: userMsg}},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("llm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: API error (status %d): %s", resp.StatusCode, string(body))
	}

	var msgResp messagesResponse
	if err := json.Unmarshal(body, &msgResp); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}

	if msgResp.Error != nil {
		return "", fmt.Errorf("llm: API error: %s: %s", msgResp.Error.Type, msgResp.Error.Message)
	}

	var text strings.Builder
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	return text.String(), nil
}

// DecomposedTask is a single task returned by epic decomposition.
type DecomposedTask struct {
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Priority     int      `json:"priority"`
	DependsOn    []int    `json:"depends_on,omitempty"` // indices into the task array
	RoleAffinity string   `json:"role_affinity,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// DecomposeEpic calls the LLM to decompose a high-level epic into a structured
// list of tasks.
func (c *Client) DecomposeEpic(ctx context.Context, epicTitle, epicDesc, projectDesc string) ([]DecomposedTask, error) {
	system := `You are a senior software architect. Your job is to decompose a high-level epic into concrete, actionable tasks that an AI coding agent can execute independently.

Rules:
- Return a JSON array of task objects.
- Each task has: title (string), description (string), priority (int 0-4, where 0=critical), depends_on (array of zero-based indices of tasks this depends on), role_affinity (string, e.g. "developer", "tester").
- Tasks should be ordered so dependencies come before dependents.
- Keep tasks focused: each should be completable in a single coding session.
- Include testing tasks where appropriate.
- Return ONLY the JSON array, no markdown fences, no explanation.`

	userMsg := fmt.Sprintf("Epic: %s", epicTitle)
	if epicDesc != "" {
		userMsg += fmt.Sprintf("\n\nDescription: %s", epicDesc)
	}
	if projectDesc != "" {
		userMsg += fmt.Sprintf("\n\nProject context: %s", projectDesc)
	}

	raw, err := c.Chat(ctx, system, userMsg)
	if err != nil {
		return nil, fmt.Errorf("llm: decompose epic: %w", err)
	}

	// Strip markdown code fences if present.
	raw = stripCodeFences(raw)

	var tasks []DecomposedTask
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		return nil, fmt.Errorf("llm: parse decomposed tasks: %w (raw: %s)", err, truncateForError(raw, 200))
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("llm: decomposition returned zero tasks")
	}

	return tasks, nil
}

// ExpandTaskPrompt calls the LLM to generate a detailed, actionable agent
// prompt for a single task.
func (c *Client) ExpandTaskPrompt(ctx context.Context, taskTitle, taskDesc, epicContext, projectDesc string) (string, error) {
	system := `You are a senior software engineer writing a detailed work prompt for an AI coding agent. The prompt should be specific and actionable.

Include:
1. Acceptance criteria (what "done" looks like)
2. Implementation approach (suggested steps)
3. File hints (likely files/directories to modify)
4. Testing requirements
5. Edge cases to consider

Write the prompt in second person ("You should..."). Be concise but thorough. Do not wrap in markdown code fences.`

	userMsg := fmt.Sprintf("Task: %s", taskTitle)
	if taskDesc != "" {
		userMsg += fmt.Sprintf("\n\nDescription: %s", taskDesc)
	}
	if epicContext != "" {
		userMsg += fmt.Sprintf("\n\nEpic context: %s", epicContext)
	}
	if projectDesc != "" {
		userMsg += fmt.Sprintf("\n\nProject context: %s", projectDesc)
	}

	expanded, err := c.Chat(ctx, system, userMsg)
	if err != nil {
		return "", fmt.Errorf("llm: expand task prompt: %w", err)
	}

	return strings.TrimSpace(expanded), nil
}

// stripCodeFences removes markdown code fences from the beginning and end of
// a string. Handles ```json ... ``` and ``` ... ```.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (possibly with language tag).
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
	}
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}
	return strings.TrimSpace(s)
}

// truncateForError truncates a string for inclusion in error messages.
func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEventSerialization(t *testing.T) {
	t.Run("marshals all fields", func(t *testing.T) {
		event := Event{
			Type:      EventAgentCrashed,
			AgentID:   1,
			AgentRole: "developer",
			Project:   "my-project",
			Message:   "agent-1 crashed and was restarted",
			Timestamp: time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC),
			Details: map[string]interface{}{
				"restart_count": float64(3),
			},
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		var got map[string]interface{}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if got["event"] != "agent_crashed" {
			t.Errorf("event = %q", got["event"])
		}
		if got["agent_id"] != float64(1) {
			t.Errorf("agent_id = %v", got["agent_id"])
		}
		if got["agent_role"] != "developer" {
			t.Errorf("agent_role = %q", got["agent_role"])
		}
		if got["project"] != "my-project" {
			t.Errorf("project = %q", got["project"])
		}
		if got["message"] != "agent-1 crashed and was restarted" {
			t.Errorf("message = %q", got["message"])
		}

		details, ok := got["details"].(map[string]interface{})
		if !ok {
			t.Fatalf("details not a map: %T", got["details"])
		}
		if details["restart_count"] != float64(3) {
			t.Errorf("details.restart_count = %v", details["restart_count"])
		}
	})

	t.Run("omits empty details", func(t *testing.T) {
		event := Event{
			Type:      EventCommitsPushed,
			AgentID:   2,
			Project:   "proj",
			Message:   "3 new commits",
			Timestamp: time.Now(),
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		var got map[string]interface{}
		_ = json.Unmarshal(data, &got)

		if _, ok := got["details"]; ok {
			t.Error("details should be omitted when nil")
		}
	})

	t.Run("round-trips through JSON", func(t *testing.T) {
		original := Event{
			Type:      EventTestFailure,
			AgentID:   3,
			AgentRole: "tester",
			Project:   "proj",
			Message:   "test failure detected",
			Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			Details: map[string]interface{}{
				"line": "ERROR: test_foo failed",
			},
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		var decoded Event
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if decoded.Type != original.Type {
			t.Errorf("Type = %q, want %q", decoded.Type, original.Type)
		}
		if decoded.AgentID != original.AgentID {
			t.Errorf("AgentID = %d, want %d", decoded.AgentID, original.AgentID)
		}
		if decoded.Message != original.Message {
			t.Errorf("Message = %q, want %q", decoded.Message, original.Message)
		}
		if !decoded.Timestamp.Equal(original.Timestamp) {
			t.Errorf("Timestamp = %v, want %v", decoded.Timestamp, original.Timestamp)
		}
	})
}

func TestSend(t *testing.T) {
	t.Run("posts JSON to webhook", func(t *testing.T) {
		var received Event
		var contentType string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			contentType = r.Header.Get("Content-Type")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &received)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		event := Event{
			Type:      EventAgentCrashed,
			AgentID:   1,
			AgentRole: "developer",
			Project:   "test-proj",
			Message:   "agent crashed",
			Timestamp: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		}

		err := Send(srv.URL, event)
		if err != nil {
			t.Fatalf("Send: %v", err)
		}

		if contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", contentType)
		}
		if received.Type != EventAgentCrashed {
			t.Errorf("received Type = %q", received.Type)
		}
		if received.AgentID != 1 {
			t.Errorf("received AgentID = %d", received.AgentID)
		}
		if received.Project != "test-proj" {
			t.Errorf("received Project = %q", received.Project)
		}
	})

	t.Run("returns nil when webhook URL is empty", func(t *testing.T) {
		err := Send("", Event{Type: EventAgentCrashed})
		if err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})

	t.Run("returns error on HTTP failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := Send(srv.URL, Event{Type: EventAgentCrashed})
		if err == nil {
			t.Fatal("expected error for 500 response")
		}
		if got := err.Error(); got != "notify: webhook returned status 500" {
			t.Errorf("error = %q", got)
		}
	})

	t.Run("returns error on connection failure", func(t *testing.T) {
		err := Send("http://127.0.0.1:1", Event{Type: EventAgentCrashed})
		if err == nil {
			t.Fatal("expected error for connection refused")
		}
	})
}

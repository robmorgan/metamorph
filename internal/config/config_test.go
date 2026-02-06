package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "metamorph.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "valid config",
			toml: `
[project]
name = "my-app"
description = "A test project"

[agents]
count = 3
model = "claude-sonnet"
roles = ["developer", "tester", "reviewer"]

[docker]
image = "ubuntu:22.04"

[testing]
command = "go test ./..."
fast_command = "go test -short ./..."

[notifications]
webhook_url = "https://example.com/hook"
`,
		},
		{
			name: "empty project name",
			toml: `
[project]
name = ""

[agents]
count = 2
model = "claude-sonnet"
roles = ["developer"]
`,
			wantErr: "project.name is required",
		},
		{
			name: "missing project name",
			toml: `
[project]
description = "no name"

[agents]
count = 2
model = "claude-sonnet"
roles = ["developer"]
`,
			wantErr: "project.name is required",
		},
		{
			name: "zero agent count",
			toml: `
[project]
name = "my-app"

[agents]
count = 0
model = "claude-sonnet"
roles = ["developer"]
`,
			wantErr: "agents.count must be greater than 0",
		},
		{
			name: "negative agent count",
			toml: `
[project]
name = "my-app"

[agents]
count = -1
model = "claude-sonnet"
roles = ["developer"]
`,
			wantErr: "agents.count must be greater than 0",
		},
		{
			name: "empty model",
			toml: `
[project]
name = "my-app"

[agents]
count = 2
model = ""
roles = ["developer"]
`,
			wantErr: "agents.model is required",
		},
		{
			name: "missing model",
			toml: `
[project]
name = "my-app"

[agents]
count = 2
roles = ["developer"]
`,
			wantErr: "agents.model is required",
		},
		{
			name: "invalid role",
			toml: `
[project]
name = "my-app"

[agents]
count = 2
model = "claude-sonnet"
roles = ["developer", "hacker"]
`,
			wantErr: `invalid agent role: "hacker"`,
		},
		{
			name: "all valid roles",
			toml: `
[project]
name = "my-app"

[agents]
count = 6
model = "claude-sonnet"
roles = ["developer", "tester", "refactorer", "documenter", "optimizer", "reviewer"]
`,
		},
		{
			name: "minimal valid config",
			toml: `
[project]
name = "minimal"

[agents]
count = 1
model = "gpt-4"
roles = []
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeConfig(t, dir, tt.toml)

			cfg, err := Load(path)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if got := err.Error(); got != tt.wantErr {
					t.Fatalf("expected error %q, got %q", tt.wantErr, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
		})
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/metamorph.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

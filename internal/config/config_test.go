package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "valid config",
			json: `{
				"project": {"name": "my-app", "description": "A test project"},
				"agents": {"count": 3, "model": "claude-sonnet", "roles": ["developer", "tester", "reviewer"]},
				"docker": {"image": "ubuntu:22.04"},
				"testing": {"command": "go test ./...", "fast_command": "go test -short ./..."},
				"notifications": {"webhook_url": "https://example.com/hook"}
			}`,
		},
		{
			name: "empty project name",
			json: `{
				"project": {"name": ""},
				"agents": {"count": 2, "model": "claude-sonnet", "roles": ["developer"]}
			}`,
			wantErr: "project.name is required",
		},
		{
			name: "missing project name",
			json: `{
				"project": {"description": "no name"},
				"agents": {"count": 2, "model": "claude-sonnet", "roles": ["developer"]}
			}`,
			wantErr: "project.name is required",
		},
		{
			name: "zero agent count",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 0, "model": "claude-sonnet", "roles": ["developer"]}
			}`,
			wantErr: "agents.count must be greater than 0",
		},
		{
			name: "negative agent count",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": -1, "model": "claude-sonnet", "roles": ["developer"]}
			}`,
			wantErr: "agents.count must be greater than 0",
		},
		{
			name: "empty model",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 2, "model": "", "roles": ["developer"]}
			}`,
			wantErr: "agents.model is required",
		},
		{
			name: "missing model",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 2, "roles": ["developer"]}
			}`,
			wantErr: "agents.model is required",
		},
		{
			name: "invalid role",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 2, "model": "claude-sonnet", "roles": ["developer", "hacker"]}
			}`,
			wantErr: `invalid agent role: "hacker"`,
		},
		{
			name: "all valid roles",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 6, "model": "claude-sonnet", "roles": ["developer", "tester", "refactorer", "documenter", "optimizer", "reviewer"]}
			}`,
		},
		{
			name: "minimal valid config",
			json: `{
				"project": {"name": "minimal"},
				"agents": {"count": 1, "model": "gpt-4", "roles": []}
			}`,
		},
		{
			name: "empty roles list is valid",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 1, "model": "claude-sonnet", "roles": []}
			}`,
		},
		{
			name: "missing sections uses zero values",
			json: `{
				"project": {"name": "my-app"},
				"agents": {"count": 1, "model": "claude-sonnet"}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeConfig(t, dir, tt.json)

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
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{this is not valid json`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoad_ValidConfigValues(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{
		"project": {"name": "my-project", "description": "A test project"},
		"agents": {"count": 3, "model": "claude-sonnet", "roles": ["developer", "tester", "reviewer"]},
		"docker": {"image": "custom:latest", "extra_packages": ["curl", "jq"]},
		"testing": {"command": "go test ./...", "fast_command": "go test -short"},
		"notifications": {"webhook_url": "https://hooks.example.com/test"}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Project.Name != "my-project" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
	if cfg.Project.Description != "A test project" {
		t.Errorf("Project.Description = %q", cfg.Project.Description)
	}
	if cfg.Agents.Count != 3 {
		t.Errorf("Agents.Count = %d", cfg.Agents.Count)
	}
	if cfg.Agents.Model != "claude-sonnet" {
		t.Errorf("Agents.Model = %q", cfg.Agents.Model)
	}
	if len(cfg.Agents.Roles) != 3 {
		t.Fatalf("Agents.Roles = %v", cfg.Agents.Roles)
	}
	if cfg.Agents.Roles[0] != "developer" || cfg.Agents.Roles[1] != "tester" || cfg.Agents.Roles[2] != "reviewer" {
		t.Errorf("Agents.Roles = %v", cfg.Agents.Roles)
	}
	if cfg.Docker.Image != "custom:latest" {
		t.Errorf("Docker.Image = %q", cfg.Docker.Image)
	}
	if len(cfg.Docker.ExtraPackages) != 2 {
		t.Errorf("Docker.ExtraPackages = %v", cfg.Docker.ExtraPackages)
	}
	if cfg.Testing.Command != "go test ./..." {
		t.Errorf("Testing.Command = %q", cfg.Testing.Command)
	}
	if cfg.Testing.FastCommand != "go test -short" {
		t.Errorf("Testing.FastCommand = %q", cfg.Testing.FastCommand)
	}
	if cfg.Notifications.WebhookURL != "https://hooks.example.com/test" {
		t.Errorf("Notifications.WebhookURL = %q", cfg.Notifications.WebhookURL)
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{
		"project": {"name": "defaults-test"},
		"agents": {"count": 1, "model": "claude-sonnet"}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Docker.Image != "metamorph-agent:latest" {
		t.Errorf("Docker.Image default = %q, want %q", cfg.Docker.Image, "metamorph-agent:latest")
	}

	if cfg.Testing.Command != "" {
		t.Errorf("Testing.Command should be empty, got %q", cfg.Testing.Command)
	}
	if cfg.Notifications.WebhookURL != "" {
		t.Errorf("Notifications.WebhookURL should be empty, got %q", cfg.Notifications.WebhookURL)
	}
	if len(cfg.Agents.Roles) != 0 {
		t.Errorf("Agents.Roles should be empty, got %v", cfg.Agents.Roles)
	}
}

func TestApplyDefaults_GitAuthorFromHostConfig(t *testing.T) {
	wantName := ""
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		wantName = strings.TrimSpace(string(out))
	}
	wantEmail := ""
	if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		wantEmail = strings.TrimSpace(string(out))
	}

	if wantName == "" && wantEmail == "" {
		t.Skip("no git config user.name/user.email set on host")
	}

	dir := t.TempDir()
	path := writeConfig(t, dir, `{
		"project": {"name": "git-defaults"},
		"agents": {"count": 1, "model": "claude-sonnet"}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if wantName != "" && cfg.Git.AuthorName != wantName {
		t.Errorf("Git.AuthorName = %q, want %q (from git config)", cfg.Git.AuthorName, wantName)
	}
	if wantEmail != "" && cfg.Git.AuthorEmail != wantEmail {
		t.Errorf("Git.AuthorEmail = %q, want %q (from git config)", cfg.Git.AuthorEmail, wantEmail)
	}
}

func TestApplyDefaults_GitAuthorExplicitNotOverridden(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{
		"project": {"name": "git-explicit"},
		"agents": {"count": 1, "model": "claude-sonnet"},
		"git": {"author_name": "Explicit Name", "author_email": "explicit@example.com"}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Git.AuthorName != "Explicit Name" {
		t.Errorf("Git.AuthorName = %q, want %q", cfg.Git.AuthorName, "Explicit Name")
	}
	if cfg.Git.AuthorEmail != "explicit@example.com" {
		t.Errorf("Git.AuthorEmail = %q, want %q", cfg.Git.AuthorEmail, "explicit@example.com")
	}
}

func TestLoad_WithRepos(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{
		"project": {"name": "multi-repo"},
		"repos": [
			{"path": "/home/user/backend", "label": "backend"},
			{"path": "/home/user/frontend", "label": "frontend"}
		],
		"agents": {"count": 2, "model": "claude-sonnet"}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Repos) != 2 {
		t.Fatalf("Repos = %v, want 2 entries", cfg.Repos)
	}
	if cfg.Repos[0].Path != "/home/user/backend" {
		t.Errorf("Repos[0].Path = %q", cfg.Repos[0].Path)
	}
	if cfg.Repos[0].Label != "backend" {
		t.Errorf("Repos[0].Label = %q", cfg.Repos[0].Label)
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "config.json")

	cfg := &Config{
		Project: ProjectConfig{Name: "saved-project"},
		Agents:  AgentsConfig{Count: 2, Model: "claude-sonnet"},
	}

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}

	if loaded.Project.Name != "saved-project" {
		t.Errorf("Project.Name = %q", loaded.Project.Name)
	}
}

func TestGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Load non-existent returns defaults.
	cfg, err := LoadGlobalFrom(path)
	if err != nil {
		t.Fatalf("LoadGlobalFrom: %v", err)
	}
	if cfg.Daemon.Port != 8377 {
		t.Errorf("Port = %d, want 8377", cfg.Daemon.Port)
	}
	if cfg.Defaults.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q", cfg.Defaults.Model)
	}

	// Save and reload.
	cfg.Daemon.Port = 9999
	if err := SaveGlobalTo(cfg, path); err != nil {
		t.Fatalf("SaveGlobalTo: %v", err)
	}

	cfg2, err := LoadGlobalFrom(path)
	if err != nil {
		t.Fatalf("LoadGlobalFrom: %v", err)
	}
	if cfg2.Daemon.Port != 9999 {
		t.Errorf("Port = %d after reload, want 9999", cfg2.Daemon.Port)
	}
}

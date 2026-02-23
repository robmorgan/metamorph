package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/robmorgan/metamorph/internal/constants"
)

// RepoConfig describes a source repository associated with a project.
type RepoConfig struct {
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
}

// Config holds per-project configuration from ~/.metamorph/projects/{name}/config.json.
type Config struct {
	Project       ProjectConfig       `json:"project"`
	Repos         []RepoConfig        `json:"repos,omitempty"`
	Agents        AgentsConfig        `json:"agents"`
	Docker        DockerConfig        `json:"docker"`
	Testing       TestingConfig       `json:"testing"`
	Notifications NotificationsConfig `json:"notifications"`
	Git           GitConfig           `json:"git"`
	Broker        BrokerConfig        `json:"broker"`
}

type ProjectConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AgentsConfig struct {
	Count int      `json:"count"`
	Model string   `json:"model"`
	Roles []string `json:"roles"`
}

type DockerConfig struct {
	Image         string   `json:"image"`
	ExtraPackages []string `json:"extra_packages"`
}

type TestingConfig struct {
	Command     string `json:"command"`
	FastCommand string `json:"fast_command"`
}

type NotificationsConfig struct {
	WebhookURL string `json:"webhook_url"`
}

type GitConfig struct {
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
}

type BrokerConfig struct {
	Port int `json:"port"`
}

// Load reads a JSON config file from path and validates it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadProject loads the config for a named project from the global directory.
func LoadProject(projectName string) (*Config, error) {
	path := filepath.Join(constants.ProjectDir(projectName), constants.ProjectConfigFile)
	return Load(path)
}

// Save writes the config as JSON to the given path.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

// applyDefaults fills in default values for optional fields.
func applyDefaults(cfg *Config) {
	if cfg.Docker.Image == "" {
		cfg.Docker.Image = "metamorph-agent:latest"
	}
	if cfg.Broker.Port == 0 {
		cfg.Broker.Port = constants.DefaultBrokerPort
	}
	if cfg.Git.AuthorName == "" {
		if name, err := exec.Command("git", "config", "user.name").Output(); err == nil {
			cfg.Git.AuthorName = strings.TrimSpace(string(name))
		}
	}
	if cfg.Git.AuthorEmail == "" {
		if email, err := exec.Command("git", "config", "user.email").Output(); err == nil {
			cfg.Git.AuthorEmail = strings.TrimSpace(string(email))
		}
	}
}

func validate(cfg *Config) error {
	if cfg.Project.Name == "" {
		return fmt.Errorf("project.name is required")
	}

	if cfg.Agents.Count <= 0 {
		return fmt.Errorf("agents.count must be greater than 0")
	}

	if cfg.Agents.Model == "" {
		return fmt.Errorf("agents.model is required")
	}

	for _, role := range cfg.Agents.Roles {
		if _, ok := constants.AgentRoles[role]; !ok {
			return fmt.Errorf("invalid agent role: %q", role)
		}
	}

	return nil
}

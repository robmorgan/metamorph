package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/robmorgan/metamorph/internal/constants"
)

type Config struct {
	Project       ProjectConfig       `toml:"project"`
	Agents        AgentsConfig        `toml:"agents"`
	Docker        DockerConfig        `toml:"docker"`
	Testing       TestingConfig       `toml:"testing"`
	Notifications NotificationsConfig `toml:"notifications"`
	Git           GitConfig           `toml:"git"`
}

type ProjectConfig struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
}

type AgentsConfig struct {
	Count int      `toml:"count"`
	Model string   `toml:"model"`
	Roles []string `toml:"roles"`
}

type DockerConfig struct {
	Image         string   `toml:"image"`
	ExtraPackages []string `toml:"extra_packages"`
}

type TestingConfig struct {
	Command     string `toml:"command"`
	FastCommand string `toml:"fast_command"`
}

type NotificationsConfig struct {
	WebhookURL string `toml:"webhook_url"`
}

type GitConfig struct {
	AuthorName  string `toml:"author_name"`
	AuthorEmail string `toml:"author_email"`
}

// Load reads a TOML config file from path and validates it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyDefaults fills in default values for optional fields.
func applyDefaults(cfg *Config) {
	if cfg.Docker.Image == "" {
		cfg.Docker.Image = "metamorph-agent:latest"
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

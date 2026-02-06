package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/brightfame/metamorph/internal/constants"
)

type Config struct {
	Project       ProjectConfig       `toml:"project"`
	Agents        AgentsConfig        `toml:"agents"`
	Docker        DockerConfig        `toml:"docker"`
	Testing       TestingConfig       `toml:"testing"`
	Notifications NotificationsConfig `toml:"notifications"`
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

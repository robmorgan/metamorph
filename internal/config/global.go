package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/tidwall/jsonc"
)

// GlobalConfig holds system-wide daemon settings from ~/.metamorph/config.json.
type GlobalConfig struct {
	Daemon   DaemonGlobalConfig `json:"daemon"`
	Defaults DefaultsConfig     `json:"defaults"`
}

// DaemonGlobalConfig holds daemon-level settings.
type DaemonGlobalConfig struct {
	Port int `json:"port"`
}

// DefaultsConfig holds default values applied to new projects.
type DefaultsConfig struct {
	Model string `json:"model"`
}

// LoadGlobal reads the global config from ~/.metamorph/config.json.
// Returns a default config if the file doesn't exist.
func LoadGlobal() (*GlobalConfig, error) {
	path := constants.GlobalConfigPath()
	return LoadGlobalFrom(path)
}

// LoadGlobalFrom reads the global config from the given path.
// Returns a default config if the file doesn't exist.
func LoadGlobalFrom(path string) (*GlobalConfig, error) {
	cfg := &GlobalConfig{}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyGlobalDefaults(cfg)
			return cfg, nil
		}
		return nil, fmt.Errorf("reading global config: %w", err)
	}

	if err := json.Unmarshal(jsonc.ToJSON(data), cfg); err != nil {
		return nil, fmt.Errorf("parsing global config: %w", err)
	}

	applyGlobalDefaults(cfg)
	return cfg, nil
}

// SaveGlobal writes the global config to ~/.metamorph/config.json.
func SaveGlobal(cfg *GlobalConfig) error {
	path := constants.GlobalConfigPath()
	return SaveGlobalTo(cfg, path)
}

// SaveGlobalTo writes the global config to the given path.
func SaveGlobalTo(cfg *GlobalConfig, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating global config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling global config: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing global config: %w", err)
	}

	return nil
}

func applyGlobalDefaults(cfg *GlobalConfig) {
	if cfg.Daemon.Port == 0 {
		cfg.Daemon.Port = constants.DefaultControlPlanePort
	}
	if cfg.Defaults.Model == "" {
		cfg.Defaults.Model = "claude-opus-4-6"
	}
}

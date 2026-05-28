package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CanonicalDirConfig struct {
	Global  string `json:"global,omitempty"`
	Project string `json:"project,omitempty"`
}

type Config struct {
	CanonicalDir CanonicalDirConfig `json:"canonicalDir"`
}

func GetConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, _ := os.UserHomeDir()
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "skills", "config.json")
}

func LoadConfig() (*Config, error) {
	path := GetConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}, nil
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path := GetConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func ResolvePath(path string, cwd string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
				return filepath.Join(home, path[2:])
			}
			return filepath.Join(home, path[1:])
		}
	}
	if !filepath.IsAbs(path) && cwd != "" {
		return filepath.Join(cwd, path)
	}
	return path
}

func GetCanonicalSkillsDir(global bool, cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cfg, err := LoadConfig()
	if err == nil {
		if global {
			if cfg.CanonicalDir.Global != "" {
				return ResolvePath(cfg.CanonicalDir.Global, cwd)
			}
		} else {
			if cfg.CanonicalDir.Project != "" {
				return ResolvePath(cfg.CanonicalDir.Project, cwd)
			}
		}
	}

	if global {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, ".gemini", "antigravity", "skills")
		}
		return filepath.Join(cwd, ".gemini", "antigravity", "skills")
	}
	return filepath.Join(cwd, ".agents", "skills")
}

func SetConfigKey(key, value string) error {
	cfg, err := LoadConfig()
	if err != nil {
		cfg = &Config{}
	}
	switch key {
	case "canonicalDir.global":
		cfg.CanonicalDir.Global = value
	case "canonicalDir.project":
		cfg.CanonicalDir.Project = value
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return SaveConfig(cfg)
}

func GetConfigKey(key string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	switch key {
	case "canonicalDir.global":
		return cfg.CanonicalDir.Global, nil
	case "canonicalDir.project":
		return cfg.CanonicalDir.Project, nil
	default:
		return "", fmt.Errorf("unknown config key %q", key)
	}
}

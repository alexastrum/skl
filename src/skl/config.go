package skl

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SkillsShConfigFile is the repo-level config file declaring extra skill container directories.
const SkillsShConfigFile = "skills.sh.json"

// SkillsShConfig represents the parsed contents of a repo-level skills.sh.json file.
type SkillsShConfig struct {
	Paths []string `json:"paths,omitempty"`
}

type CanonicalDirConfig struct {
	Global  string `json:"global,omitempty"`
	Project string `json:"project,omitempty"`
}

type Config struct {
	CanonicalDir CanonicalDirConfig `json:"canonicalDir"`
}

// ParseSkillsShPaths parses the `paths` field of a skills.sh.json file into normalized,
// repo-relative POSIX paths (e.g. "./tools/skills/" -> "tools/skills"), matching
// parseSkillsShPaths in upstream src/skills-sh-config.ts (#2582).
//
// Entries must start with "./", contain no backslashes, stay inside the repo, and not point
// at the repo root ("." / "./"). Invalid JSON, missing fields, or invalid entries are ignored.
func ParseSkillsShPaths(content string) []string {
	var rawObj map[string]interface{}
	if err := json.Unmarshal([]byte(content), &rawObj); err != nil || rawObj == nil {
		return []string{}
	}

	rawPaths, ok := rawObj["paths"]
	if !ok {
		return []string{}
	}
	entries, ok := rawPaths.([]interface{})
	if !ok {
		return []string{}
	}

	result := []string{}
	seen := make(map[string]bool)
	for _, item := range entries {
		entry, ok := item.(string)
		if !ok || !strings.HasPrefix(entry, "./") {
			continue
		}
		// Reject backslashes so Windows never interprets "./a\\..\\.." as path traversal.
		if strings.Contains(entry, "\\") {
			continue
		}

		normalized := strings.TrimRight(path.Clean(entry), "/")
		if normalized == "." || normalized == "" || normalized == ".." {
			continue
		}
		if strings.HasPrefix(normalized, "../") || path.IsAbs(normalized) {
			continue
		}

		if !seen[normalized] {
			seen[normalized] = true
			result = append(result, normalized)
		}
	}
	return result
}

// GetSkillsShSearchDirs reads skills.sh.json from basePath and returns the absolute
// skill container directories it declares (or an empty slice when missing/invalid).
func GetSkillsShSearchDirs(basePath string) []string {
	data, err := os.ReadFile(filepath.Join(basePath, SkillsShConfigFile))
	if err != nil {
		return []string{}
	}
	relPaths := ParseSkillsShPaths(string(data))
	dirs := make([]string, 0, len(relPaths))
	for _, rel := range relPaths {
		dirs = append(dirs, filepath.Join(basePath, filepath.FromSlash(rel)))
	}
	return dirs
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
	cfgPath := GetConfigPath()
	data, err := os.ReadFile(cfgPath)
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
	cfgPath := GetConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, data, 0644)
}

func ResolvePath(p string, cwd string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
				return filepath.Join(home, p[2:])
			}
			return filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) && cwd != "" {
		return filepath.Join(cwd, p)
	}
	return p
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
			return filepath.Join(home, ".agents", "skills")
		}
		return filepath.Join(cwd, ".agents", "skills")
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

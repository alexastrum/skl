package skl

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrSkillAlreadyExists is returned when the target SKILL.md file already exists.
var ErrSkillAlreadyExists = errors.New("skill already exists")

func formatSkillFrontmatter(name, description string) (string, error) {
	doc := struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{
		Name:        name,
		Description: description,
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\r\n"), nil
}

// InitSkill initializes a new skill directory and template SKILL.md.
// If Name is provided, it creates a subdirectory; otherwise it initializes in Cwd.
// Special YAML characters in skill names (colons, hashes, quotes, booleans, numbers)
// are safely escaped via yaml.v3 (#2507).
// It returns the path of the created SKILL.md file or an error.
func InitSkill(opts InitOptions) (string, error) {
	cwd := opts.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}

	hasName := opts.Name != ""
	skillName := opts.Name
	if !hasName {
		skillName = filepath.Base(cwd)
	}

	skillDir := cwd
	if hasName {
		skillDir = filepath.Join(cwd, skillName)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")

	// Check if already exists
	if _, err := os.Stat(skillFile); err == nil {
		displayPath := "SKILL.md"
		if hasName {
			displayPath = filepath.Join(skillName, "SKILL.md")
		}
		return "", fmt.Errorf("skill already exists at %s: %w", displayPath, ErrSkillAlreadyExists)
	}

	// Create directory if name was provided
	if hasName {
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			return "", fmt.Errorf("create skill directory: %w", err)
		}
	}

	fmText, err := formatSkillFrontmatter(skillName, "A brief description of what this skill does")
	if err != nil {
		return "", fmt.Errorf("marshal skill frontmatter: %w", err)
	}

	skillContent := fmt.Sprintf(`---
%s
---

# %s

Instructions for the agent to follow when this skill is activated.

## When to use

Describe when this skill should be used.

## Instructions

1. First step
2. Second step
3. Additional steps as needed
`, fmText, skillName)

	if err := os.WriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		return "", fmt.Errorf("write skill template: %w", err)
	}

	return skillFile, nil
}

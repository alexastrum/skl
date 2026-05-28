package skl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSkillAlreadyExists is returned when the target SKILL.md file already exists.
var ErrSkillAlreadyExists = errors.New("skill already exists")

// InitSkill initializes a new skill directory and template SKILL.md.
// If Name is provided, it creates a subdirectory; otherwise it initializes in Cwd.
// It returns the path of the created SKILL.md file or an error.
func InitSkill(opts InitOptions) (string, error) {
	cwd := opts.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
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
			return "", fmt.Errorf("failed to create skill directory: %w", err)
		}
	}

	skillContent := fmt.Sprintf(`---
name: %s
description: A brief description of what this skill does
---

# %s

Instructions for the agent to follow when this skill is activated.

## When to use

Describe when this skill should be used.

## Instructions

1. First step
2. Second step
3. Additional steps as needed
`, skillName, skillName)

	if err := os.WriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write SKILL.md: %w", err)
	}

	return skillFile, nil
}

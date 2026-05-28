package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListInstalledSkills_Empty(t *testing.T) {
	tmpDir := t.TempDir()

	skills, err := ListInstalledSkills(ListOptions{
		Global: false,
		Cwd:    tmpDir,
	})
	if err != nil {
		t.Fatalf("ListInstalledSkills failed: %v", err)
	}

	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestListInstalledSkills_Project(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock project skill inside the canonical `.agents/skills` directory
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "project-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: project-skill
description: A mock project skill
---
# project-skill
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create an invalid directory with no SKILL.md to ensure it is ignored
	invalidDir := filepath.Join(tmpDir, ".agents", "skills", "ignored-dir")
	if err := os.MkdirAll(invalidDir, 0755); err != nil {
		t.Fatalf("failed to create ignored dir: %v", err)
	}

	skills, err := ListInstalledSkills(ListOptions{
		Global: false,
		Cwd:    tmpDir,
	})
	if err != nil {
		t.Fatalf("ListInstalledSkills failed: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected exactly 1 skill, got %d", len(skills))
	}

	got := skills[0]
	if got.Name != "project-skill" {
		t.Errorf("expected skill name 'project-skill', got %q", got.Name)
	}
	if got.Description != "A mock project skill" {
		t.Errorf("expected description 'A mock project skill', got %q", got.Description)
	}
	if got.Scope != "project" {
		t.Errorf("expected scope 'project', got %q", got.Scope)
	}
}

func TestListInstalledSkills_Global(t *testing.T) {
	tmpDir := t.TempDir()
	// Set the sandboxed HOME so os.UserHomeDir resolves here
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Create a mock global skill under `~/.gemini/antigravity/skills/global-skill`
	// Since HOME=tmpDir, that resolves to `<tmpDir>/.gemini/antigravity/skills/global-skill`
	skillDir := filepath.Join(tmpDir, ".gemini", "antigravity", "skills", "global-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create global skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: global-skill
description: A mock global skill
---
# global-skill
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	skills, err := ListInstalledSkills(ListOptions{
		Global: true,
		Cwd:    tmpDir,
	})
	if err != nil {
		t.Fatalf("ListInstalledSkills failed: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected exactly 1 global skill, got %d", len(skills))
	}

	got := skills[0]
	if got.Name != "global-skill" {
		t.Errorf("expected skill name 'global-skill', got %q", got.Name)
	}
	if got.Scope != "global" {
		t.Errorf("expected scope 'global', got %q", got.Scope)
	}
}

func TestListInstalledSkills_Filter(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock skill in the canonical `.agents/skills` directory
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: test-skill
description: A mock skill
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// List with filter matching antigravity
	skills, err := ListInstalledSkills(ListOptions{
		Global:      false,
		Cwd:         tmpDir,
		AgentFilter: []AgentType{AgentAntigravity},
	})
	if err != nil {
		t.Fatalf("ListInstalledSkills with antigravity filter failed: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill matching antigravity filter, got %d", len(skills))
	}

	// List with filter for an agent that doesn't have it (or another type)
	otherSkills, err := ListInstalledSkills(ListOptions{
		Global:      false,
		Cwd:         tmpDir,
		AgentFilter: []AgentType{"non-existent-agent"},
	})
	if err != nil {
		t.Fatalf("ListInstalledSkills with dummy filter failed: %v", err)
	}

	if len(otherSkills) != 0 {
		t.Errorf("expected 0 skills matching non-existent-agent filter, got %v", otherSkills)
	}
}

func TestListInstalledSkills_AgentsMapping(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Mock antigravity installation folder
	if err := os.MkdirAll(filepath.Join(tmpDir, ".gemini", "antigravity"), 0755); err != nil {
		t.Fatalf("failed to create mock antigravity folder: %v", err)
	}

	// Create a skill inside `.agents/skills`
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "multi-agent-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: multi-agent-skill
description: A skill mapped to agents
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	skills, err := ListInstalledSkills(ListOptions{
		Global: false,
		Cwd:    tmpDir,
	})
	if err != nil {
		t.Fatalf("ListInstalledSkills failed: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	got := skills[0]
	// Should be associated with universal and antigravity as they both target `.agents/skills`
	expectedAgents := []AgentType{AgentAntigravity, AgentUniversal}
	// We check if the expectedAgents are slice-equal regardless of ordering, or sort them.
	// For simplicity, verify both are present.
	hasAntigravity := false
	hasUniversal := false
	for _, agent := range got.Agents {
		if agent == AgentAntigravity {
			hasAntigravity = true
		}
		if agent == AgentUniversal {
			hasUniversal = true
		}
	}

	if !hasAntigravity || !hasUniversal {
		t.Errorf("expected agents to include antigravity and universal, got %v (want %v)", got.Agents, expectedAgents)
	}
}

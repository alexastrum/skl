package skl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateSkill_NotExist(t *testing.T) {
	tmpDir := t.TempDir()

	err := UpdateSkill("ghost-skill", "", UpdateOptions{
		Cwd:    tmpDir,
		Global: false,
	})
	if err == nil {
		t.Fatal("expected error updating non-existent skill, got nil")
	}

	if !strings.Contains(err.Error(), "is not installed") {
		t.Errorf("expected error message to mention 'is not installed', got: %v", err)
	}
}

func TestUpdateSkill_RefreshInPlace(t *testing.T) {
	tmpDir := t.TempDir()

	// Install a mock skill first
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: test-skill
description: Old description
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Update in place (simulate integrity checking / refresh)
	err := UpdateSkill("test-skill", "", UpdateOptions{
		Cwd:    tmpDir,
		Global: false,
	})
	if err != nil {
		t.Fatalf("UpdateSkill refresh failed: %v", err)
	}

	bytes, err := os.ReadFile(skillMd)
	if err != nil {
		t.Fatalf("failed to read refreshed SKILL.md: %v", err)
	}

	if string(bytes) != content {
		t.Errorf("expected skill content to be unchanged, got %q", string(bytes))
	}
}

func TestUpdateSkill_FromSource(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Install mock skill
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "my-upgradeable-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	oldSkillMd := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(oldSkillMd, []byte("old content"), 0644); err != nil {
		t.Fatalf("failed to write old SKILL.md: %v", err)
	}

	// 2. Create updated source directory
	srcDir := filepath.Join(tmpDir, "new-source-dir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create new source dir: %v", err)
	}
	newContent := `---
name: my-upgradeable-skill
description: Newly updated description
---
# Newly updated!
`
	newSkillMd := filepath.Join(srcDir, "SKILL.md")
	if err := os.WriteFile(newSkillMd, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to write updated SKILL.md: %v", err)
	}

	// 3. Update skill from source
	err := UpdateSkill("my-upgradeable-skill", srcDir, UpdateOptions{
		Cwd:    tmpDir,
		Global: false,
	})
	if err != nil {
		t.Fatalf("UpdateSkill from source failed: %v", err)
	}

	// 4. Verify updated file contents
	bytes, err := os.ReadFile(oldSkillMd)
	if err != nil {
		t.Fatalf("failed to read updated SKILL.md: %v", err)
	}

	if !reflect.DeepEqual(bytes, []byte(newContent)) {
		t.Errorf("updated file content differs from source content")
	}
}

func TestUpdateSkill_Global(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Create a global mock skill in ~/.agents/skills/global-skill
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "global-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create global skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: global-skill
description: Global description
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write global SKILL.md: %v", err)
	}

	err := UpdateSkill("global-skill", "", UpdateOptions{
		Global: true,
	})
	if err != nil {
		t.Fatalf("global UpdateSkill failed: %v", err)
	}
}

func TestUpdateSkill_Errors(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Skill name is required
	err := UpdateSkill("", "", UpdateOptions{Cwd: tmpDir})
	if err == nil {
		t.Error("expected error for empty skill name, got nil")
	}

	// Install a mock skill first so we pass the "is installed" check for subsequent tests
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "test-skill")
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("SKILL.md"), 0644)

	// 2. Source directory does not exist
	err = UpdateSkill("test-skill", "/non/existent/src/dir", UpdateOptions{Cwd: tmpDir})
	if err == nil {
		t.Error("expected error for non-existent source directory, got nil")
	}

	// 3. Source directory is missing SKILL.md
	emptySrcDir := filepath.Join(tmpDir, "empty-src")
	_ = os.MkdirAll(emptySrcDir, 0755)
	err = UpdateSkill("test-skill", emptySrcDir, UpdateOptions{Cwd: tmpDir})
	if err == nil {
		t.Error("expected error for missing SKILL.md in source directory, got nil")
	}
}


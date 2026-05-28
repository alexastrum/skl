package skl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitSkill_WithName(t *testing.T) {
	tmpDir := t.TempDir()

	opts := InitOptions{
		Name: "my-test-skill",
		Cwd:  tmpDir,
	}

	path, err := InitSkill(opts)
	// Since we are running TDD with empty stubs, this test should compile but fail initially when stub is not implemented.
	// However, we want the test assertion to be correct so that when implemented, it succeeds.
	if err != nil {
		t.Fatalf("InitSkill failed: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, "my-test-skill", "SKILL.md")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("expected file to exist at %q", expectedPath)
	}

	bytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("failed to read created SKILL.md: %v", err)
	}

	content := string(bytes)
	if !strings.Contains(content, "name: my-test-skill") {
		t.Errorf("expected SKILL.md to contain name in frontmatter: %s", content)
	}
	if !strings.Contains(content, "# my-test-skill") {
		t.Errorf("expected SKILL.md to contain markdown header: %s", content)
	}
}

func TestInitSkill_NoName(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a subdirectory to act as the current working directory, e.g. /tmp/cwd-name
	cwd := filepath.Join(tmpDir, "some-cwd-project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	opts := InitOptions{
		Name: "",
		Cwd:  cwd,
	}

	path, err := InitSkill(opts)
	if err != nil {
		t.Fatalf("InitSkill failed: %v", err)
	}

	expectedPath := filepath.Join(cwd, "SKILL.md")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("expected file to exist at %q", expectedPath)
	}

	bytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("failed to read created SKILL.md: %v", err)
	}

	content := string(bytes)
	// Base name of Cwd is 'some-cwd-project', which should be used as the skill name
	if !strings.Contains(content, "name: some-cwd-project") {
		t.Errorf("expected SKILL.md to use cwd's base name in frontmatter: %s", content)
	}
	if !strings.Contains(content, "# some-cwd-project") {
		t.Errorf("expected SKILL.md to use cwd's base name in header: %s", content)
	}
}

func TestInitSkill_MultipleSkills(t *testing.T) {
	tmpDir := t.TempDir()

	// Initializing first skill
	_, err := InitSkill(InitOptions{Name: "skill-one", Cwd: tmpDir})
	if err != nil {
		t.Fatalf("failed to initialize first skill: %v", err)
	}

	// Initializing second skill in the same directory
	_, err = InitSkill(InitOptions{Name: "skill-two", Cwd: tmpDir})
	if err != nil {
		t.Fatalf("failed to initialize second skill: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "skill-one", "SKILL.md")); os.IsNotExist(err) {
		t.Errorf("expected skill-one/SKILL.md to exist")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "skill-two", "SKILL.md")); os.IsNotExist(err) {
		t.Errorf("expected skill-two/SKILL.md to exist")
	}
}

func TestInitSkill_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()

	opts := InitOptions{
		Name: "duplicate-skill",
		Cwd:  tmpDir,
	}

	// First initialization should succeed
	_, err := InitSkill(opts)
	if err != nil {
		t.Fatalf("first InitSkill failed: %v", err)
	}

	// Second initialization should fail
	_, err = InitSkill(opts)
	if err == nil {
		t.Fatalf("expected error on duplicate initialization, but got none")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected error message to mention 'already exists', got: %v", err)
	}

	if !errors.Is(err, ErrSkillAlreadyExists) {
		t.Errorf("expected error to be of type ErrSkillAlreadyExists, got: %v", err)
	}
}

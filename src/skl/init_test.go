package skl

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if err != nil {
		t.Fatalf("InitSkill failed: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, "my-test-skill", "SKILL.md")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
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
	cwd := filepath.Join(tmpDir, "some-cwd-project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	path, err := InitSkill(InitOptions{Name: "", Cwd: cwd})
	if err != nil {
		t.Fatalf("InitSkill failed: %v", err)
	}

	expectedPath := filepath.Join(cwd, "SKILL.md")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}

	bytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("failed to read created SKILL.md: %v", err)
	}

	content := string(bytes)
	if !strings.Contains(content, "name: some-cwd-project") {
		t.Errorf("expected SKILL.md to use cwd's base name in frontmatter: %s", content)
	}
	if !strings.Contains(content, "# some-cwd-project") {
		t.Errorf("expected SKILL.md to use cwd's base name in header: %s", content)
	}
}

func TestInitSkill_SpecialYAMLEscaping(t *testing.T) {
	specialNames := []string{
		"demo: x",
		"my skill # best",
		"true",
		"123",
		"[demo]",
		`say "hello"`,
		"it's quoted",
		"@scoped-name",
		"a & b * c",
	}

	for _, name := range specialNames {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.ContainsAny(name, `:"*`) {
				t.Skip("skipping characters disallowed in Windows directory names")
			}
			tmpDir := t.TempDir()
			skillFile, err := InitSkill(InitOptions{Name: name, Cwd: tmpDir})
			if err != nil {
				t.Fatalf("InitSkill(%q) failed: %v", name, err)
			}
			raw, err := os.ReadFile(skillFile)
			if err != nil {
				t.Fatalf("read SKILL.md: %v", err)
			}
			data, _, err := ParseFrontmatter(string(raw))
			if err != nil {
				t.Fatalf("ParseFrontmatter failed for %q: %v\ncontent:\n%s", name, err, raw)
			}
			gotName, ok := data["name"].(string)
			if !ok || gotName != name {
				t.Errorf("ParseFrontmatter().name = %#v (%T), want string %q", data["name"], data["name"], name)
			}
			if _, _, err := ValidateSkillMetadata(data); err != nil {
				t.Errorf("ValidateSkillMetadata failed for %q: %v", name, err)
			}
		})
	}

	// Also verify escaping when initializing directly inside a cwd with `#` in its directory name
	cwdWithHash := filepath.Join(t.TempDir(), "my skill # best")
	if err := os.MkdirAll(cwdWithHash, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	skillFile, err := InitSkill(InitOptions{Cwd: cwdWithHash})
	if err != nil {
		t.Fatalf("InitSkill in cwd failed: %v", err)
	}
	raw, _ := os.ReadFile(skillFile)
	data, _, err := ParseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("ParseFrontmatter failed: %v", err)
	}
	if data["name"] != "my skill # best" {
		t.Errorf("expected 'my skill # best', got %#v", data["name"])
	}
}

func TestInitSkill_MultipleSkills(t *testing.T) {
	tmpDir := t.TempDir()

	if _, err := InitSkill(InitOptions{Name: "skill-one", Cwd: tmpDir}); err != nil {
		t.Fatalf("failed to initialize first skill: %v", err)
	}
	if _, err := InitSkill(InitOptions{Name: "skill-two", Cwd: tmpDir}); err != nil {
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

	if _, err := InitSkill(opts); err != nil {
		t.Fatalf("first InitSkill failed: %v", err)
	}

	_, err := InitSkill(opts)
	if err == nil {
		t.Fatalf("expected error on duplicate initialization, but got none")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected error message to mention 'already exists', got: %v", err)
	}
	if !errors.Is(err, ErrSkillAlreadyExists) {
		t.Errorf("expected error to wrap ErrSkillAlreadyExists, got: %v", err)
	}
}

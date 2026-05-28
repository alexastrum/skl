package skl

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseRemoveOptions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSkills []string
		wantOpts   RemoveOptions
	}{
		{
			name:       "empty args",
			args:       []string{},
			wantSkills: []string{},
			wantOpts:   RemoveOptions{},
		},
		{
			name:       "skills with no flags",
			args:       []string{"skill-a", "skill-b"},
			wantSkills: []string{"skill-a", "skill-b"},
			wantOpts:   RemoveOptions{},
		},
		{
			name:       "global and yes flags",
			args:       []string{"-g", "-y", "skill-c"},
			wantSkills: []string{"skill-c"},
			wantOpts: RemoveOptions{
				Global: true,
				Yes:    true,
			},
		},
		{
			name:       "agent filter flag single",
			args:       []string{"skill-d", "-a", "antigravity"},
			wantSkills: []string{"skill-d"},
			wantOpts: RemoveOptions{
				Agent: []AgentType{AgentAntigravity},
			},
		},
		{
			name:       "all flag and multiple agents",
			args:       []string{"--all", "-a", "antigravity", "universal"},
			wantSkills: []string{},
			wantOpts: RemoveOptions{
				All:   true,
				Agent: []AgentType{AgentAntigravity, AgentUniversal},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSkills, gotOpts := ParseRemoveOptions(tt.args)
			if !reflect.DeepEqual(gotSkills, tt.wantSkills) {
				t.Errorf("ParseRemoveOptions() gotSkills = %v, want %v", gotSkills, tt.wantSkills)
			}
			if !reflect.DeepEqual(gotOpts, tt.wantOpts) {
				t.Errorf("ParseRemoveOptions() gotOpts = %+v, want %+v", gotOpts, tt.wantOpts)
			}
		})
	}
}

func TestRemoveSkills_Local(t *testing.T) {
	tmpDir := t.TempDir()

	// Create project skill inside canonical `.agents/skills`
	canonicalDir := filepath.Join(tmpDir, ".agents", "skills", "skill-to-delete")
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatalf("failed to create canonical dir: %v", err)
	}
	skillMd := filepath.Join(canonicalDir, "SKILL.md")
	if err := os.WriteFile(skillMd, []byte("---\nname: skill-to-delete\n---\n"), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create another skill that should remain
	keepDir := filepath.Join(tmpDir, ".agents", "skills", "skill-to-keep")
	if err := os.MkdirAll(keepDir, 0755); err != nil {
		t.Fatalf("failed to create keep dir: %v", err)
	}
	keepMd := filepath.Join(keepDir, "SKILL.md")
	if err := os.WriteFile(keepMd, []byte("---\nname: skill-to-keep\n---\n"), 0644); err != nil {
		t.Fatalf("failed to write keep SKILL.md: %v", err)
	}

	opts := RemoveOptions{
		Yes: true,
	}

	err := RemoveSkills([]string{"skill-to-delete"}, opts, tmpDir)
	if err != nil {
		t.Fatalf("RemoveSkills failed: %v", err)
	}

	// Verify 'skill-to-delete' is gone
	if _, err := os.Stat(canonicalDir); !os.IsNotExist(err) {
		t.Errorf("expected skill-to-delete directory to be removed, but it still exists")
	}

	// Verify 'skill-to-keep' is still present
	if _, err := os.Stat(keepDir); err != nil {
		t.Errorf("expected skill-to-keep directory to remain, but got error: %v", err)
	}
}

func TestRemoveSkills_ErrorsAndEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Remove multiple skills (and missing skill handles gracefully)
	opts := RemoveOptions{Yes: true}
	err := RemoveSkills([]string{"missing-skill-abc"}, opts, tmpDir)
	if err != nil {
		t.Errorf("expected missing skill removal to be handled gracefully, got: %v", err)
	}

	// 3. Remove all skills with --all flag
	// Create a skill to delete
	delDir := filepath.Join(tmpDir, ".agents", "skills", "delete-all-me")
	_ = os.MkdirAll(delDir, 0755)
	_ = os.WriteFile(filepath.Join(delDir, "SKILL.md"), []byte("---\nname: delete-all-me\n---\n"), 0644)

	optsAll := RemoveOptions{
		All: true,
		Yes: true,
	}
	if err := RemoveSkills(nil, optsAll, tmpDir); err != nil {
		t.Fatalf("RemoveSkills --all failed: %v", err)
	}

	if _, err := os.Stat(delDir); !os.IsNotExist(err) {
		t.Error("expected delete-all-me directory to be deleted, but it still exists")
	}
}

func TestRemoveSkills_Sanitization(t *testing.T) {
	// Test the behavior of SanitizeName directly
	tests := []struct {
		input string
		want  string
	}{
		{"MySkill", "myskill"},
		{"UPPERCASE", "uppercase"},
		{"my skill", "my-skill"},
		{"bun.sh", "bun.sh"},
		{"my_skill", "my_skill"},
		{"skill@name", "skill-name"},
		{"../etc/passwd", "etc-passwd"},
		{"../../secret", "secret"},
		{".hidden", "hidden"},
		{"skill-", "skill"},
		{"", "unnamed-skill"},
		{"---", "unnamed-skill"},
	}

	for _, tt := range tests {
		got := SanitizeName(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRemoveSkills_CanonicalProtection(t *testing.T) {
	origAgents := Agents
	defer func() { Agents = origAgents }()

	const (
		MockAgentA AgentType = "mock-agent-a"
		MockAgentB AgentType = "mock-agent-b"
	)

	// Create dynamic copy of Agents map and register mock agents
	Agents = make(map[AgentType]AgentConfig)
	for k, v := range origAgents {
		Agents[k] = v
	}
	Agents[MockAgentA] = AgentConfig{
		Name:        "mock-agent-a",
		DisplayName: "Mock Agent A",
		SkillsDir:   ".mock-agent-a/skills",
		DetectInstalled: func(home, configHome, cwd string) bool {
			_, err := os.Stat(filepath.Join(cwd, ".mock-agent-a"))
			return err == nil
		},
	}
	Agents[MockAgentB] = AgentConfig{
		Name:        "mock-agent-b",
		DisplayName: "Mock Agent B",
		SkillsDir:   ".mock-agent-b/skills",
		DetectInstalled: func(home, configHome, cwd string) bool {
			_, err := os.Stat(filepath.Join(cwd, ".mock-agent-b"))
			return err == nil
		},
	}

	tmpDir := t.TempDir()

	// Create agent dirs to mock their installation
	if err := os.MkdirAll(filepath.Join(tmpDir, ".mock-agent-a", "skills", "test-protect"), 0755); err != nil {
		t.Fatalf("failed to create agent a dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, ".mock-agent-b", "skills", "test-protect"), 0755); err != nil {
		t.Fatalf("failed to create agent b dir: %v", err)
	}

	// Create canonical dir
	canonicalDir := filepath.Join(tmpDir, ".agents", "skills", "test-protect")
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatalf("failed to create canonical dir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(canonicalDir, "SKILL.md"), []byte("---\nname: test-protect\n---\n"), 0644)

	// Remove from MockAgentA ONLY
	err := RemoveSkills([]string{"test-protect"}, RemoveOptions{Agent: []AgentType{MockAgentA}}, tmpDir)
	if err != nil {
		t.Fatalf("RemoveSkills failed: %v", err)
	}

	// MockAgentA dir should be gone
	if _, err := os.Stat(filepath.Join(tmpDir, ".mock-agent-a", "skills", "test-protect")); !os.IsNotExist(err) {
		t.Error("expected MockAgentA skill folder to be deleted")
	}

	// MockAgentB dir should still exist
	if _, err := os.Stat(filepath.Join(tmpDir, ".mock-agent-b", "skills", "test-protect")); err != nil {
		t.Error("expected MockAgentB skill folder to be preserved")
	}

	// Canonical folder must be PRESERVED (canonical protection) because MockAgentB still has it!
	if _, err := os.Stat(canonicalDir); err != nil {
		t.Error("expected canonical skill folder to be preserved due to canonical protection")
	}

	// Now remove from MockAgentB
	err = RemoveSkills([]string{"test-protect"}, RemoveOptions{Agent: []AgentType{MockAgentB}}, tmpDir)
	if err != nil {
		t.Fatalf("RemoveSkills failed: %v", err)
	}

	// MockAgentB dir should be gone
	if _, err := os.Stat(filepath.Join(tmpDir, ".mock-agent-b", "skills", "test-protect")); !os.IsNotExist(err) {
		t.Error("expected MockAgentB skill folder to be deleted")
	}

	// Canonical folder should now be DELETED because no other agents are using it!
	if _, err := os.Stat(canonicalDir); !os.IsNotExist(err) {
		t.Error("expected canonical skill folder to be deleted since no other agent uses it")
	}
}

func TestRemoveSkills_CaseInsensitivity(t *testing.T) {
	tmpDir := t.TempDir()

	canonicalDir := filepath.Join(tmpDir, ".agents", "skills", "case-insensitive-skill")
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatalf("failed to create canonical dir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(canonicalDir, "SKILL.md"), []byte("---\nname: case-insensitive-skill\n---\n"), 0644)

	// Remove passing "Case-Insensitive-Skill" (uppercase/mixed case)
	err := RemoveSkills([]string{"Case-Insensitive-Skill"}, RemoveOptions{}, tmpDir)
	if err != nil {
		t.Fatalf("RemoveSkills failed: %v", err)
	}

	// Should be successfully deleted due to case insensitivity
	if _, err := os.Stat(canonicalDir); !os.IsNotExist(err) {
		t.Error("expected case-insensitive skill folder to be deleted")
	}
}

func TestExpandHome(t *testing.T) {
	// Test without ~
	path, err := expandHome("/absolute/path")
	if err != nil || path != "/absolute/path" {
		t.Errorf("expandHome without ~ failed: got %q, err: %v", path, err)
	}

	// Test single ~
	path2, err := expandHome("~")
	if err != nil {
		t.Errorf("expandHome ~ failed: %v", err)
	}

	// Test ~/
	path3, err := expandHome("~/my/sub/path")
	if err != nil {
		t.Errorf("expandHome ~/ my/sub/path failed: %v", err)
	}

	// Test ~other
	path4, err := expandHome("~other/path")
	if err != nil {
		t.Errorf("expandHome ~other/path failed: %v", err)
	}
	if path2 == "" || path3 == "" || path4 == "" {
		t.Error("expected non-empty expanded home paths")
	}
}



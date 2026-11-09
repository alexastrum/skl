package skl

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	path := GetConfigPath()
	expectedPath := filepath.Join(tmpDir, "skills", "config.json")
	if path != expectedPath {
		t.Errorf("GetConfigPath() = %q, want %q", path, expectedPath)
	}

	if err := SetConfigKey("canonicalDir.global", "~/my-global-skills"); err != nil {
		t.Fatalf("SetConfigKey failed: %v", err)
	}

	val, err := GetConfigKey("canonicalDir.global")
	if err != nil {
		t.Fatalf("GetConfigKey failed: %v", err)
	}
	if val != "~/my-global-skills" {
		t.Errorf("GetConfigKey = %q, want %q", val, "~/my-global-skills")
	}

	home, _ := os.UserHomeDir()
	expectedResolved := filepath.Join(home, "my-global-skills")
	resolved := GetCanonicalSkillsDir(true, "")
	if resolved != expectedResolved {
		t.Errorf("GetCanonicalSkillsDir(true) = %q, want %q", resolved, expectedResolved)
	}

	if err := SetConfigKey("canonicalDir.project", "./custom-project-skills"); err != nil {
		t.Fatalf("SetConfigKey project failed: %v", err)
	}
	projCwd := filepath.Join(tmpDir, "proj")
	if got := GetCanonicalSkillsDir(false, projCwd); got != filepath.Join(projCwd, "custom-project-skills") {
		t.Errorf("GetCanonicalSkillsDir(false) = %q", got)
	}

	if err := SetConfigKey("invalid.key", "value"); err == nil {
		t.Error("expected error setting invalid config key")
	}
}

func TestSkillsShPaths(t *testing.T) {
	// 1. Normalizes and deduplicates valid entries
	got := ParseSkillsShPaths(`{"paths":["./tools/skills","./a/./b/","./tools/skills/"]}`)
	want := []string{"tools/skills", "a/b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSkillsShPaths valid = %v, want %v", got, want)
	}

	// 2. Rejects entries not starting with ./, escaping repo, containing backslashes, or pointing at root
	rejected := ParseSkillsShPaths(`{"paths":["tools/skills","/etc","../outside","./a/../../outside","./",".","./a\\..\\..",42]}`)
	if len(rejected) != 0 {
		t.Errorf("expected all invalid entries rejected, got %v", rejected)
	}

	// 3. Returns empty slice for invalid JSON or missing/non-array paths
	for _, invalid := range []string{
		`{not json`,
		`{"groupings":[]}`,
		`{"paths":"./tools/skills"}`,
		`["./tools/skills"]`,
	} {
		if res := ParseSkillsShPaths(invalid); len(res) != 0 {
			t.Errorf("ParseSkillsShPaths(%s) = %v, want []", invalid, res)
		}
	}

	// 4. GetSkillsShSearchDirs resolves declared directories against basePath
	repoDir := t.TempDir()
	if dirs := GetSkillsShSearchDirs(repoDir); len(dirs) != 0 {
		t.Errorf("expected empty search dirs when skills.sh.json missing, got %v", dirs)
	}

	_ = os.WriteFile(filepath.Join(repoDir, SkillsShConfigFile), []byte(`{"paths":["./tools/skills","./packages/ui/skills/"]}`), 0644)
	gotDirs := GetSkillsShSearchDirs(repoDir)
	wantDirs := []string{
		filepath.Join(repoDir, "tools", "skills"),
		filepath.Join(repoDir, "packages", "ui", "skills"),
	}
	if !reflect.DeepEqual(gotDirs, wantDirs) {
		t.Errorf("GetSkillsShSearchDirs() = %v, want %v", gotDirs, wantDirs)
	}
}

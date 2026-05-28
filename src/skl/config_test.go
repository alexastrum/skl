package skl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig(t *testing.T) {
	// Temporarily redirect XDG_CONFIG_HOME
	tmpDir := t.TempDir()
	origXdg := os.Getenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", origXdg)
	os.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Test GetConfigPath
	path := GetConfigPath()
	expectedPath := filepath.Join(tmpDir, "skills", "config.json")
	if path != expectedPath {
		t.Errorf("GetConfigPath() = %q, want %q", path, expectedPath)
	}

	// Test SetConfigKey and GetConfigKey
	err := SetConfigKey("canonicalDir.global", "~/my-global-skills")
	if err != nil {
		t.Fatalf("SetConfigKey failed: %v", err)
	}

	val, err := GetConfigKey("canonicalDir.global")
	if err != nil {
		t.Fatalf("GetConfigKey failed: %v", err)
	}
	if val != "~/my-global-skills" {
		t.Errorf("GetConfigKey = %q, want %q", val, "~/my-global-skills")
	}

	// Test GetCanonicalSkillsDir global with override
	home, _ := os.UserHomeDir()
	expectedResolved := filepath.Join(home, "my-global-skills")
	resolved := GetCanonicalSkillsDir(true, "")
	if resolved != expectedResolved {
		t.Errorf("GetCanonicalSkillsDir(true) = %q, want %q", resolved, expectedResolved)
	}

	// Test SetConfigKey with invalid key
	err = SetConfigKey("invalid.key", "value")
	if err == nil {
		t.Error("expected error setting invalid config key")
	}
}

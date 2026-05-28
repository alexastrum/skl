package skl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncSkills(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create mock node_modules and package
	pkgDir := filepath.Join(tmpDir, "node_modules", "mock-skill-pkg")
	err := os.MkdirAll(pkgDir, 0755)
	if err != nil {
		t.Fatalf("failed to create mock package dir: %v", err)
	}

	skillMdContent := `---
name: Mock Npm Skill
description: An npm packaged skill
agents:
  - universal
---
# Mock Npm Skill
`
	err = os.WriteFile(filepath.Join(pkgDir, "SKILL.md"), []byte(skillMdContent), 0644)
	if err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// 2. Discover skills
	discovered, err := DiscoverNodeModuleSkills(tmpDir)
	if err != nil {
		t.Fatalf("DiscoverNodeModuleSkills failed: %v", err)
	}

	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered skill, got %d", len(discovered))
	}
	if discovered[0].Name != "mock-npm-skill" || discovered[0].PackageName != "mock-skill-pkg" {
		t.Errorf("discovered skill mismatch: %+v", discovered[0])
	}

	// 3. Sync skills
	opts := SyncOptions{
		Yes:     true,
		Verbose: true,
	}
	err = SyncSkills(nil, opts, tmpDir)
	if err != nil {
		t.Fatalf("SyncSkills failed: %v", err)
	}

	// 4. Verify symlinked destination
	canonicalDest := filepath.Join(tmpDir, ".agents", "skills", "mock-npm-skill")
	_, err = os.Lstat(canonicalDest)
	if err != nil {
		t.Errorf("expected symlinked skill at %s, but stat failed: %v", canonicalDest, err)
	}

	// Verify lock file got updated
	lock, err := ReadLocalLock(tmpDir)
	if err != nil {
		t.Fatalf("ReadLocalLock failed: %v", err)
	}
	entry, ok := lock.Skills["mock-npm-skill"]
	if !ok {
		t.Fatal("expected mock-npm-skill to be in lock file")
	}
	if entry.Source != "mock-skill-pkg" || entry.SourceType != "npm" {
		t.Errorf("unexpected lock entry: %+v", entry)
	}
}

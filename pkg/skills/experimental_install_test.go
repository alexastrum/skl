package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreFromLock(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create a dummy local skill directory to be the source
	dummySource := filepath.Join(tmpDir, "dummy-source-skill")
	err := os.MkdirAll(dummySource, 0755)
	if err != nil {
		t.Fatalf("failed to create dummy source folder: %v", err)
	}

	skillMdContent := `---
name: dummy-skill
description: A dummy test skill
agents:
  - universal
---
# Dummy Skill
`
	err = os.WriteFile(filepath.Join(dummySource, "SKILL.md"), []byte(skillMdContent), 0644)
	if err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// 2. Write a mock skills-lock.json
	lockFileContent := `{
  "version": 1,
  "skills": {
    "dummy-skill": {
      "source": "` + filepath.ToSlash(dummySource) + `",
      "sourceType": "local",
      "computedHash": "somehash"
    }
  }
}`
	err = os.WriteFile(filepath.Join(tmpDir, "skills-lock.json"), []byte(lockFileContent), 0644)
	if err != nil {
		t.Fatalf("failed to write mock skills-lock.json: %v", err)
	}

	// 3. Call RestoreFromLock
	err = RestoreFromLock(nil, tmpDir)
	if err != nil {
		t.Fatalf("RestoreFromLock failed: %v", err)
	}

	// 4. Verify that the skill was installed into .agents/skills/dummy-skill
	canonicalDest := filepath.Join(tmpDir, ".agents", "skills", "dummy-skill")
	_, err = os.Stat(canonicalDest)
	if err != nil {
		t.Errorf("expected skill to be installed at %s, but stat failed: %v", canonicalDest, err)
	}

	// Verify SKILL.md exists in the destination
	_, err = os.Stat(filepath.Join(canonicalDest, "SKILL.md"))
	if err != nil {
		t.Errorf("expected SKILL.md to exist in destination: %v", err)
	}
}

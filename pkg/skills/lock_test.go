package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockFileOperations(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Test GetSkillLockPath with XDG_STATE_HOME
	origXdg := os.Getenv("XDG_STATE_HOME")
	defer os.Setenv("XDG_STATE_HOME", origXdg)

	os.Setenv("XDG_STATE_HOME", tmpDir)
	globalPath := GetSkillLockPath()
	expectedGlobalPath := filepath.Join(tmpDir, "skills", ".skill-lock.json")
	if globalPath != expectedGlobalPath {
		t.Errorf("GetSkillLockPath() = %q, want %q", globalPath, expectedGlobalPath)
	}

	// 2. Test GetLocalLockPath
	localPath := GetLocalLockPath(tmpDir)
	expectedLocalPath := filepath.Join(tmpDir, "skills-lock.json")
	if localPath != expectedLocalPath {
		t.Errorf("GetLocalLockPath() = %q, want %q", localPath, expectedLocalPath)
	}

	// 3. Test Read empty / missing global lock
	lock, err := ReadSkillLock()
	if err != nil {
		t.Fatalf("ReadSkillLock empty failed: %v", err)
	}
	if lock.Version != GlobalLockVer {
		t.Errorf("expected empty lock version %d, got %d", GlobalLockVer, lock.Version)
	}

	// 4. Test AddSkillToLock & WriteSkillLock
	entry := SkillLockEntry{
		Source:          "github.com/foo/bar",
		SourceType:      "github",
		SourceUrl:       "https://github.com/foo/bar.git",
		SkillFolderHash: "abc123hash",
	}
	err = AddSkillToLock("test-skill", entry)
	if err != nil {
		t.Fatalf("AddSkillToLock failed: %v", err)
	}

	// Read it back
	lock, err = ReadSkillLock()
	if err != nil {
		t.Fatalf("ReadSkillLock back failed: %v", err)
	}
	savedEntry, ok := lock.Skills["test-skill"]
	if !ok {
		t.Fatal("expected test-skill to be in global lock")
	}
	if savedEntry.Source != "github.com/foo/bar" || savedEntry.SkillFolderHash != "abc123hash" {
		t.Errorf("savedEntry mismatch: %+v", savedEntry)
	}

	// 5. Test RemoveSkillFromLock
	removed, err := RemoveSkillFromLock("test-skill")
	if err != nil {
		t.Fatalf("RemoveSkillFromLock failed: %v", err)
	}
	if !removed {
		t.Error("expected RemoveSkillFromLock to return true")
	}

	lock, err = ReadSkillLock()
	if err != nil {
		t.Fatalf("ReadSkillLock post-remove failed: %v", err)
	}
	if _, ok := lock.Skills["test-skill"]; ok {
		t.Error("expected test-skill to be deleted from global lock")
	}

	// 6. Test Local Lock Operations
	localLock, err := ReadLocalLock(tmpDir)
	if err != nil {
		t.Fatalf("ReadLocalLock failed: %v", err)
	}
	if localLock.Version != LocalLockVer {
		t.Errorf("expected empty local lock version %d, got %d", LocalLockVer, localLock.Version)
	}

	localEntry := LocalSkillLockEntry{
		Source:       "my-local-source",
		SourceType:   "local",
		ComputedHash: "localhash456",
	}
	err = AddSkillToLocalLock("local-skill", localEntry, tmpDir)
	if err != nil {
		t.Fatalf("AddSkillToLocalLock failed: %v", err)
	}

	// Read it back
	localLock, err = ReadLocalLock(tmpDir)
	if err != nil {
		t.Fatalf("ReadLocalLock back failed: %v", err)
	}
	savedLocal, ok := localLock.Skills["local-skill"]
	if !ok {
		t.Fatal("expected local-skill to be in local lock")
	}
	if savedLocal.ComputedHash != "localhash456" {
		t.Errorf("savedLocal mismatch: %+v", savedLocal)
	}

	// Remove local skill
	removedLocal, err := RemoveSkillFromLocalLock("local-skill", tmpDir)
	if err != nil {
		t.Fatalf("RemoveSkillFromLocalLock failed: %v", err)
	}
	if !removedLocal {
		t.Error("expected RemoveSkillFromLocalLock to return true")
	}

	// Check non-existent local removal
	removedLocal2, err := RemoveSkillFromLocalLock("non-existent", tmpDir)
	if err != nil {
		t.Fatalf("RemoveSkillFromLocalLock non-existent failed: %v", err)
	}
	if removedLocal2 {
		t.Error("expected non-existent local removal to return false")
	}

	// 7. Test ComputeSkillFolderHash
	testSkillDir := filepath.Join(tmpDir, "my-skill")
	_ = os.MkdirAll(filepath.Join(testSkillDir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(testSkillDir, "SKILL.md"), []byte("skill markdown"), 0644)
	_ = os.WriteFile(filepath.Join(testSkillDir, "sub", "helper.py"), []byte("print('hello')"), 0644)
	// Create ignored directories
	_ = os.MkdirAll(filepath.Join(testSkillDir, ".git"), 0755)
	_ = os.WriteFile(filepath.Join(testSkillDir, ".git", "config"), []byte("git config"), 0644)

	hash1, err := ComputeSkillFolderHash(testSkillDir)
	if err != nil {
		t.Fatalf("ComputeSkillFolderHash failed: %v", err)
	}
	if hash1 == "" {
		t.Error("expected non-empty folder hash")
	}

	// Writing ignored files shouldn't change the hash
	_ = os.WriteFile(filepath.Join(testSkillDir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644)
	hash2, err := ComputeSkillFolderHash(testSkillDir)
	if err != nil {
		t.Fatalf("ComputeSkillFolderHash post-git failed: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("ignored files changed folder hash: %s vs %s", hash1, hash2)
	}
}

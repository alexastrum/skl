package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SkillLockEntry represents a single installed skill entry in the global lock file.
type SkillLockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceUrl       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
	PluginName      string `json:"pluginName,omitempty"`
}

// DismissedPrompts tracks dismissed prompts in the global lock file.
type DismissedPrompts struct {
	FindSkillsPrompt bool `json:"findSkillsPrompt,omitempty"`
}

// SkillLockFile represents the global lock file.
type SkillLockFile struct {
	Version            int                       `json:"version"`
	Skills             map[string]SkillLockEntry `json:"skills"`
	Dismissed          *DismissedPrompts         `json:"dismissed,omitempty"`
	LastSelectedAgents []string                  `json:"lastSelectedAgents,omitempty"`
}

// LocalSkillLockEntry represents a single entry in the local project lock file.
type LocalSkillLockEntry struct {
	Source       string `json:"source"`
	Ref          string `json:"ref,omitempty"`
	SourceType   string `json:"sourceType"`
	SkillPath    string `json:"skillPath,omitempty"`
	ComputedHash string `json:"computedHash"`
}

// LocalSkillLockFile represents the local project lock file.
type LocalSkillLockFile struct {
	Version int                            `json:"version"`
	Skills  map[string]LocalSkillLockEntry `json:"skills"`
}

const (
	GlobalLockFile   = ".skill-lock.json"
	LocalLockFile    = "skills-lock.json"
	GlobalLockVer    = 3
	LocalLockVer     = 1
)

// GetSkillLockPath returns the path to the global skill lock file.
func GetSkillLockPath() string {
	xdgStateHome := os.Getenv("XDG_STATE_HOME")
	if xdgStateHome != "" {
		return filepath.Join(xdgStateHome, "skills", GlobalLockFile)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agents", GlobalLockFile)
}

// GetLocalLockPath returns the path to the local skill lock file.
func GetLocalLockPath(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, LocalLockFile)
}

// ReadSkillLock reads the global skill lock file.
func ReadSkillLock() (SkillLockFile, error) {
	path := GetSkillLockPath()
	return readGlobalLockPath(path)
}

func readGlobalLockPath(path string) (SkillLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return createEmptyGlobalLock(), nil
	}
	var lock SkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return createEmptyGlobalLock(), nil
	}
	if lock.Version < GlobalLockVer || lock.Skills == nil {
		return createEmptyGlobalLock(), nil
	}
	return lock, nil
}

func createEmptyGlobalLock() SkillLockFile {
	return SkillLockFile{
		Version:   GlobalLockVer,
		Skills:    make(map[string]SkillLockEntry),
		Dismissed: &DismissedPrompts{},
	}
}

// WriteSkillLock writes the global skill lock file.
func WriteSkillLock(lock SkillLockFile) error {
	path := GetSkillLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// AddSkillToLock adds or updates a skill in the global lock file.
func AddSkillToLock(skillName string, entry SkillLockEntry) error {
	lock, err := ReadSkillLock()
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	existing, ok := lock.Skills[skillName]
	if ok {
		entry.InstalledAt = existing.InstalledAt
	} else {
		entry.InstalledAt = now
	}
	entry.UpdatedAt = now
	lock.Skills[skillName] = entry
	return WriteSkillLock(lock)
}

// RemoveSkillFromLock removes a skill from the global lock file.
func RemoveSkillFromLock(skillName string) (bool, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return false, err
	}
	if _, ok := lock.Skills[skillName]; !ok {
		return false, nil
	}
	delete(lock.Skills, skillName)
	return true, WriteSkillLock(lock)
}

// ReadLocalLock reads the local project lock file.
func ReadLocalLock(cwd string) (LocalSkillLockFile, error) {
	path := GetLocalLockPath(cwd)
	data, err := os.ReadFile(path)
	if err != nil {
		return createEmptyLocalLock(), nil
	}
	var lock LocalSkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return createEmptyLocalLock(), nil
	}
	if lock.Version < LocalLockVer || lock.Skills == nil {
		return createEmptyLocalLock(), nil
	}
	return lock, nil
}

func createEmptyLocalLock() LocalSkillLockFile {
	return LocalSkillLockFile{
		Version: LocalLockVer,
		Skills:  make(map[string]LocalSkillLockEntry),
	}
}

// WriteLocalLock writes the local project lock file.
func WriteLocalLock(lock LocalSkillLockFile, cwd string) error {
	path := GetLocalLockPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// AddSkillToLocalLock adds or updates a skill in the local project lock file.
func AddSkillToLocalLock(skillName string, entry LocalSkillLockEntry, cwd string) error {
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return err
	}
	lock.Skills[skillName] = entry
	return WriteLocalLock(lock, cwd)
}

// RemoveSkillFromLocalLock removes a skill from the local project lock file.
func RemoveSkillFromLocalLock(skillName string, cwd string) (bool, error) {
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return false, err
	}
	if _, ok := lock.Skills[skillName]; !ok {
		return false, nil
	}
	delete(lock.Skills, skillName)
	return true, WriteLocalLock(lock, cwd)
}

// ComputeSkillFolderHash computes a SHA-256 hash from all files in a skill directory.
func ComputeSkillFolderHash(skillDir string) (string, error) {
	var files []struct {
		relPath string
		content []byte
	}
	err := filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files = append(files, struct {
			relPath string
			content []byte
		}{rel, content})
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})

	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.relPath))
		h.Write(f.content)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

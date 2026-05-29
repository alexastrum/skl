package skl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type UpdateOptions struct {
	Global  bool
	Cwd     string
	Verbose bool
}

// UpdateSkill updates an installed skill by either refreshing it in place or copying from a source.
func UpdateSkill(name string, srcDir string, opts UpdateOptions) error {
	if name == "" {
		return errors.New("skill name is required")
	}

	// 1. Resolve target installed directory
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	baseDir := GetCanonicalSkillsDir(opts.Global, cwd)

	skillDir := filepath.Join(baseDir, name)

	// Verify that the skill is actually installed
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill %q is not installed", name)
	}

	// 2. Overwrite / update from new source if provided
	if srcDir != "" {
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			return fmt.Errorf("source directory %q does not exist", srcDir)
		}

		// Ensure the source directory contains a valid SKILL.md
		srcSkillMd := filepath.Join(srcDir, "SKILL.md")
		if _, err := os.Stat(srcSkillMd); os.IsNotExist(err) {
			return errors.New("source directory is missing SKILL.md")
		}

		// Clean destination and copy recursively
		_ = os.RemoveAll(skillDir)
		return copyDir(srcDir, skillDir, opts.Verbose)
	}


	// 3. Otherwise, perform a simple self-integrity timestamp refresh
	skillMd := filepath.Join(skillDir, "SKILL.md")
	bytes, err := os.ReadFile(skillMd)
	if err != nil {
		return fmt.Errorf("read installed skill: %w", err)
	}

	// Write the exact same bytes back to update modification time and ensure write access
	if err := os.WriteFile(skillMd, bytes, 0644); err != nil {
		return fmt.Errorf("refresh skill: %w", err)
	}

	return nil
}

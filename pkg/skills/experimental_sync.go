package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SyncOptions contains options for the sync command.
type SyncOptions struct {
	Agent   []AgentType
	Yes     bool
	Force   bool
	Verbose bool
}

// SkillWithPkg holds information about a skill discovered inside node_modules.
type SkillWithPkg struct {
	Name        string
	PackageName string
	Path        string
}

// DiscoverNodeModuleSkills scans node_modules (including scoped packages) for skills containing a SKILL.md.
func DiscoverNodeModuleSkills(cwd string) ([]SkillWithPkg, error) {
	nodeModulesPath := filepath.Join(cwd, "node_modules")
	if _, err := os.Stat(nodeModulesPath); os.IsNotExist(err) {
		return nil, nil
	}

	var skills []SkillWithPkg
	entries, err := os.ReadDir(nodeModulesPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "@") {
			// Scoped package e.g. @org/pkg
			scopePath := filepath.Join(nodeModulesPath, name)
			subEntries, err := os.ReadDir(scopePath)
			if err != nil {
				continue
			}
			for _, subEntry := range subEntries {
				if !subEntry.IsDir() {
					continue
				}
				pkgName := name + "/" + subEntry.Name()
				pkgDir := filepath.Join(scopePath, subEntry.Name())
				found := scanPackageForSkills(pkgName, pkgDir)
				skills = append(skills, found...)
			}
		} else {
			pkgName := name
			pkgDir := filepath.Join(nodeModulesPath, name)
			found := scanPackageForSkills(pkgName, pkgDir)
			skills = append(skills, found...)
		}
	}
	return skills, nil
}

func scanPackageForSkills(pkgName string, pkgDir string) []SkillWithPkg {
	var results []SkillWithPkg
	// Scan using findSkillDirectories with fullDepth = false
	dirs := findSkillDirectories(pkgDir, false)
	for _, dir := range dirs {
		skillMdPath := filepath.Join(dir, "SKILL.md")
		bytes, err := os.ReadFile(skillMdPath)
		if err != nil {
			continue
		}
		data, _, err := ParseFrontmatter(string(bytes))
		if err != nil {
			continue
		}
		skillName, _ := data["name"].(string)
		if skillName == "" {
			skillName = filepath.Base(dir)
		}
		results = append(results, SkillWithPkg{
			Name:        SanitizeName(skillName),
			PackageName: pkgName,
			Path:        dir,
		})
	}
	return results
}

// SyncSkills syncs node_modules skills to targets and updates the lock file.
func SyncSkills(args []string, opts SyncOptions, cwd string) error {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	if opts.Verbose {
		fmt.Println("🔍 Scanning node_modules for skills...")
	}
	discovered, err := DiscoverNodeModuleSkills(cwd)
	if err != nil {
		return fmt.Errorf("failed to discover node_modules skills: %w", err)
	}

	if len(discovered) == 0 {
		if opts.Verbose {
			fmt.Println("No skills found in node_modules.")
		}
		return nil
	}

	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return fmt.Errorf("failed to read local lock: %w", err)
	}

	canonicalBase := GetCanonicalSkillsDir(false, cwd)

	var outOfDate []SkillWithPkg
	var hashes []string

	for _, skill := range discovered {
		hash, err := ComputeSkillFolderHash(skill.Path)
		if err != nil {
			continue
		}

		canonicalDir := filepath.Join(canonicalBase, skill.Name)
		_, errStat := os.Stat(canonicalDir)
		isInstalled := errStat == nil

		entry, exists := lock.Skills[skill.Name]
		if opts.Force || !exists || !isInstalled || entry.ComputedHash != hash || entry.SourceType != "npm" {
			outOfDate = append(outOfDate, skill)
			hashes = append(hashes, hash)
		}
	}

	if len(outOfDate) == 0 {
		fmt.Println("All npm skills are up-to-date.")
		return nil
	}

	fmt.Println("The following npm skills are out-of-date or new:")
	for _, skill := range outOfDate {
		fmt.Printf(" - %s (from package %s)\n", skill.Name, skill.PackageName)
	}

	if !opts.Yes {
		if !isInteractive() {
			return errors.New("non-interactive sync requires --yes to confirm")
		}
		fmt.Print("Sync these skills? [y/N]: ")
		var response string
		_, _ = fmt.Scanln(&response)
		response = strings.ToLower(strings.TrimSpace(response))
		if response != "y" && response != "yes" {
			fmt.Println("Sync cancelled.")
			return nil
		}
	}

	// Determine target agents
	targetAgents := opts.Agent
	if len(targetAgents) == 0 {
		detected := DetectInstalledAgents(cwd)
		// Universal agent is always targeted for sync
		targetAgents = append([]AgentType{AgentUniversal}, detected...)
	}

	addOpts := AddOptions{
		Global:  false, // sync is project-level
		Copy:    false, // sync uses symlink mode
		Verbose: opts.Verbose,
		Yes:     true,
	}

	for i, skill := range outOfDate {
		fmt.Printf("Syncing skill %q...\n", skill.Name)
		for _, agent := range targetAgents {
			_, err := installSkillForAgent(skill.Name, skill.Path, agent, addOpts, cwd)
			if err != nil {
				fmt.Printf("⚠️  Failed to sync skill %q to agent %q: %v\n", skill.Name, agent, err)
			}
		}

		// Update lock file
		err = AddSkillToLocalLock(skill.Name, LocalSkillLockEntry{
			Source:       skill.PackageName,
			SourceType:   "npm",
			ComputedHash: hashes[i],
		}, cwd)
		if err != nil {
			fmt.Printf("⚠️  Failed to update lock file for %q: %v\n", skill.Name, err)
		}
	}

	fmt.Println("Sync completed successfully.")
	return nil
}

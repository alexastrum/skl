package skl

import (
	"fmt"
	"os"
)

// RestoreFromLock restores all project skills from the skills-lock.json file.
func RestoreFromLock(args []string, cwd string) error {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return fmt.Errorf("read lock: %w", err)
	}

	if len(lock.Skills) == 0 {
		fmt.Println("⚠️  No project skills found in skills-lock.json")
		fmt.Println("Add project-level skills with 'skl add <package>' (without --global)")
		return nil
	}

	// Group remote skills by their source and ref
	type groupedSource struct {
		sourceType string
		skills     []string
	}
	bySource := make(map[string]*groupedSource)
	var nodeModuleSkills []string

	for name, entry := range lock.Skills {
		if entry.SourceType == "npm" || entry.SourceType == "node_modules" {
			nodeModuleSkills = append(nodeModuleSkills, name)
			continue
		}

		installSource := entry.Source
		if entry.Ref != "" {
			installSource = entry.Source + "#" + entry.Ref
		}

		existing, ok := bySource[installSource]
		if ok {
			existing.skills = append(existing.skills, name)
		} else {
			bySource[installSource] = &groupedSource{
				sourceType: entry.SourceType,
				skills:     []string{name},
			}
		}
	}

	remoteCount := len(lock.Skills) - len(nodeModuleSkills)
	if remoteCount > 0 {
		fmt.Printf("Restoring %d remote skill(s) from skills-lock.json...\n", remoteCount)
	}

	universalAgents := []AgentType{AgentUniversal}

	// Install remote skills grouped by source
	for source, group := range bySource {
		fmt.Printf("Installing skills %v from %s...\n", group.skills, source)
		addOpts := AddOptions{
			Global: false,
			Agent:  universalAgents,
			Yes:    true,
			Skill:  group.skills,
		}
		err := AddSkill(source, addOpts, cwd)
		if err != nil {
			fmt.Printf("⚠️  Failed to install from %s: %v\n", source, err)
		}
	}

	// Handle npm / node_modules skills via sync
	if len(nodeModuleSkills) > 0 {
		fmt.Printf("Syncing %d npm skill(s) from node_modules...\n", len(nodeModuleSkills))
		syncOpts := SyncOptions{
			Agent: universalAgents,
			Yes:   true,
			Force: false,
		}
		err := SyncSkills(nil, syncOpts, cwd)
		if err != nil {
			fmt.Printf("⚠️  Failed to sync node_modules skills: %v\n", err)
		}
	}

	return nil
}

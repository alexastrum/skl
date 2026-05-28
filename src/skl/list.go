package skl

import (
	"os"
	"path/filepath"
	"strings"
)

// ListInstalledSkills scans canonical and agent-specific directories to find and parse installed skills.
// It supports filtering by agent and scanning global/project scopes.
func ListInstalledSkills(opts ListOptions) ([]InstalledSkill, error) {
	var targetDir string
	scope := "project"

	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if opts.Global {
		scope = "global"
	}
	targetDir = GetCanonicalSkillsDir(opts.Global, cwd)

	// Check if directory exists
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return []InstalledSkill{}, nil
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, err
	}

	var installedSkills []InstalledSkill

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillDir := filepath.Join(targetDir, entry.Name())
		skillMdPath := filepath.Join(skillDir, "SKILL.md")

		// SKILL.md must exist
		if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
			continue
		}

		// Read and parse frontmatter
		bytes, err := os.ReadFile(skillMdPath)
		if err != nil {
			continue
		}

		data, _, err := ParseFrontmatter(string(bytes))
		if err != nil {
			continue
		}

		name := entry.Name()
		if n, ok := data["name"].(string); ok && n != "" {
			name = n
		}

		description := ""
		if d, ok := data["description"].(string); ok {
			description = d
		}

		// Determine associated agents dynamically
		var agentsList []AgentType
		detectedAgents := DetectInstalledAgents(cwd)
		if !opts.Global {
			detectedAgents = append(detectedAgents, AgentUniversal)
		}

		var agentsToCheck []AgentType
		if len(opts.AgentFilter) > 0 {
			agentsToCheck = opts.AgentFilter
		} else {
			agentsToCheck = detectedAgents
		}

		for _, agentType := range agentsToCheck {
			cfg, ok := Agents[agentType]
			if !ok {
				continue
			}

			var agentBase string
			if scope == "global" {
				if cfg.GlobalSkillsDir == "" {
					continue
				}
				agentBase = ResolvePath(cfg.GlobalSkillsDir, cwd)
			} else {
				if cfg.SkillsDir == "" {
					continue
				}
				agentBase = filepath.Join(cwd, cfg.SkillsDir)
			}

			found := false
			possibleNames := []string{entry.Name(), SanitizeName(name), name}
			nameMap := make(map[string]bool)
			var uniqueNames []string
			for _, n := range possibleNames {
				if n != "" && !nameMap[n] {
					nameMap[n] = true
					uniqueNames = append(uniqueNames, n)
				}
			}

			for _, possibleName := range uniqueNames {
				agentSkillDir := filepath.Join(agentBase, possibleName)
				if _, err := os.Stat(agentSkillDir); err == nil {
					found = true
					break
				}
			}

			if found {
				agentsList = append(agentsList, agentType)
			}
		}


		// Apply AgentFilter if provided
		if len(opts.AgentFilter) > 0 {
			matched := false
			for _, filterAgent := range opts.AgentFilter {
				for _, skillAgent := range agentsList {
					if strings.ToLower(string(skillAgent)) == strings.ToLower(string(filterAgent)) {
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
			if !matched {
				continue
			}
		}

		installedSkills = append(installedSkills, InstalledSkill{
			Name:          name,
			Description:   description,
			Path:          skillDir,
			CanonicalPath: skillDir,
			Scope:         scope,
			Agents:        agentsList,
		})
	}

	return installedSkills, nil
}

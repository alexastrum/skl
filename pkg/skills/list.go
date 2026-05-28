package skills

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

	if opts.Global {
		scope = "global"
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		// Based on agents mapping, Antigravity's global skills dir is ~/.gemini/antigravity/skills
		targetDir = filepath.Join(homeDir, ".gemini", "antigravity", "skills")
	} else {
		cwd := opts.Cwd
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return nil, err
			}
		}
		// Canonical project skills directory is .agents/skills
		targetDir = filepath.Join(cwd, ".agents", "skills")
	}

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

		// Determine associated agents
		var agentsList []AgentType
		if scope == "global" {
			agentsList = []AgentType{AgentAntigravity}
		} else {
			// Universal and Antigravity both use `.agents/skills` at the project scope
			agentsList = []AgentType{AgentAntigravity, AgentUniversal}
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

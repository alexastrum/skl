package skl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RemoveOptions contains options for the remove command.
type RemoveOptions struct {
	Global bool
	Agent  []AgentType
	Yes    bool
	All    bool
}

// ParseRemoveOptions parses command line arguments into skill names and RemoveOptions.
func ParseRemoveOptions(args []string) ([]string, RemoveOptions) {
	var options RemoveOptions
	var skills []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "-g" || arg == "--global" {
			options.Global = true
		} else if arg == "-y" || arg == "--yes" {
			options.Yes = true
		} else if arg == "--all" {
			options.All = true
		} else if arg == "-s" || arg == "--skill" {
			if i+1 < len(args) {
				i++
				parts := strings.Split(args[i], ",")
				for _, part := range parts {
					trimmed := strings.TrimSpace(part)
					if trimmed != "" {
						skills = append(skills, trimmed)
					}
				}
			}
		} else if arg == "-a" || arg == "--agent" {
			for i+1 < len(args) {
				nextArg := args[i+1]
				if strings.HasPrefix(nextArg, "-") {
					break
				}
				i++
				parts := strings.Split(nextArg, ",")
				for _, part := range parts {
					trimmed := strings.TrimSpace(part)
					if trimmed != "" {
						options.Agent = append(options.Agent, AgentType(trimmed))
					}
				}
			}
		} else if arg != "" && !strings.HasPrefix(arg, "-") {
			skills = append(skills, arg)
		}
	}

	// Avoid nil slice issues in tests for skills
	if skills == nil {
		skills = []string{}
	}

	return skills, options
}

// RemoveSkills uninstalls and deletes the specified skill directories from all scope folders.
func RemoveSkills(skillNames []string, opts RemoveOptions, cwd string) error {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Validate agent options
	if len(opts.Agent) > 0 {
		var invalidAgents []string
		for _, agt := range opts.Agent {
			if _, exists := Agents[agt]; !exists {
				invalidAgents = append(invalidAgents, string(agt))
			}
		}
		if len(invalidAgents) > 0 {
			var validKeys []string
			for k := range Agents {
				validKeys = append(validKeys, string(k))
			}
			sort.Strings(validKeys)
			return fmt.Errorf("invalid agents: %s\nValid agents: %s",
				strings.Join(invalidAgents, ", "),
				strings.Join(validKeys, ", "))
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to resolve user home: %w", err)
	}

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}

	canonicalBase := GetCanonicalSkillsDir(opts.Global, cwd)

	// Scan the installed skills in the canonical directory first
	var installedSkills []string
	if _, err := os.Stat(canonicalBase); err == nil {
		entries, err := os.ReadDir(canonicalBase)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					installedSkills = append(installedSkills, entry.Name())
				}
			}
		}
	}

	var selectedSkills []string
	if opts.All {
		selectedSkills = installedSkills
	} else {
		for _, reqName := range skillNames {
			reqSanitized := SanitizeName(reqName)
			matched := false
			for _, inst := range installedSkills {
				if strings.ToLower(inst) == reqSanitized {
					selectedSkills = append(selectedSkills, inst)
					matched = true
					break
				}
			}
			if !matched {
				// If no installed skill matched case-insensitively, still proceed with the sanitized name
				selectedSkills = append(selectedSkills, reqSanitized)
			}
		}
	}

	if len(selectedSkills) == 0 {
		return nil
	}

	// Determine which agents to target
	targetAgents := opts.Agent
	if len(targetAgents) == 0 {
		for agentType := range Agents {
			targetAgents = append(targetAgents, agentType)
		}
	}

	for _, name := range selectedSkills {
		sanitizedName := SanitizeName(name)

		// Remove from each target agent's directory
		for _, agentType := range targetAgents {
			cfg, ok := Agents[agentType]
			if !ok {
				continue
			}

			var agentSkillDir string
			if opts.Global {
				if cfg.GlobalSkillsDir == "" {
					continue
				}
				expanded, err := expandHome(cfg.GlobalSkillsDir)
				if err != nil {
					continue
				}
				agentSkillDir = filepath.Join(expanded, sanitizedName)
			} else {
				agentSkillDir = filepath.Join(cwd, cfg.SkillsDir, sanitizedName)
			}

			// Delete agent skill directory
			if _, err := os.Lstat(agentSkillDir); err == nil {
				_ = os.RemoveAll(agentSkillDir)
			}
		}

		// Remove from corresponding lockfile
		if opts.Global {
			_, _ = RemoveSkillFromLock(sanitizedName)
		} else {
			_, _ = RemoveSkillFromLocalLock(sanitizedName, cwd)
		}

		// CANONICAL PROTECTION LAYER:
		// Check if any other installed agent still has this skill symlinked/copied.
		// If so, preserve the canonical directory!
		installedAgents := detectInstalledAgents(home, configHome, cwd)

		// If we targeted all agents, we definitely want to delete the canonical directory.
		// Otherwise, filter down to remaining agents.
		var remainingAgents []AgentType
		if len(opts.Agent) > 0 {
			for _, installed := range installedAgents {
				isTargeted := false
				for _, targeted := range targetAgents {
					if installed == targeted {
						isTargeted = true
						break
					}
				}
				if !isTargeted {
					remainingAgents = append(remainingAgents, installed)
				}
			}
		}

		isStillUsed := false
		for _, remaining := range remainingAgents {
			if isUniversalAgent(remaining) {
				continue
			}
			cfg, ok := Agents[remaining]
			if !ok {
				continue
			}
			var path string
			if opts.Global {
				if cfg.GlobalSkillsDir == "" {
					continue
				}
				expanded, _ := expandHome(cfg.GlobalSkillsDir)
				path = filepath.Join(expanded, sanitizedName)
			} else {
				path = filepath.Join(cwd, cfg.SkillsDir, sanitizedName)
			}

			if _, err := os.Lstat(path); err == nil {
				isStillUsed = true
				break
			}
		}

		// Only delete the canonical folder if it exists AND is no longer used by other agents!
		if !isStillUsed {
			canonicalSkillDir := filepath.Join(canonicalBase, sanitizedName)
			if _, err := os.Lstat(canonicalSkillDir); err == nil {
				_ = os.RemoveAll(canonicalSkillDir)
			}
		}
	}

	return nil
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Join(home, path[1:]), nil
}

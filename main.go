package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexastrum/skl/src/skl"
)

const version = "1.0.0"

const helpMessage = `skl - Agent Skills CLI Manager

Usage:
  skl <command> [arguments]

Commands:
  init [name]                      Initialize a new agent skill template in Cwd or a subfolder.
  add <source>                     Install a local skill folder to project or global scope (aliases: a, i, install).
  list                             List installed skills for detected agents (alias: ls).
  remove <names...>                Uninstall and delete skill configurations (aliases: rm, r).
  find <query>                     Search/retrieve an installed skill by name or slug (aliases: search, f, s).
  update <name> [src]              Refresh installed skill(s) (aliases: check, upgrade).
  config <set|get> <key> [value]   Configure settings (e.g. canonicalDir.global, canonicalDir.project).
  experimental_sync                Sync skills from node_modules into agent directories (alias: sync).
  experimental_install             Restore all skills from skills-lock.json.
  version                          Print CLI tool version (alias: -v, --version).

Flags:
  -g, --global                     Apply action to global scope (home directory).
  -p, --project                    Apply action to project scope.
  -y, --yes                        Skip confirmation prompts (automatic non-interactive).
  --copy                           Force copy instead of symlink.
  -a, --agent <name>               Filter action by agent type.
  -v, --verbose                    Log all persistent file system changes.
`

const initHelp = `skl init - Initialize a new agent skill template

Usage:
  skl init [name]

Arguments:
  [name]               Optional name of the subdirectory to initialize the skill in.
                       If not provided, initializes in the current directory.

Description:
  Creates a basic SKILL.md template with default YAML frontmatter headers.
`

const addHelp = `skl add - Install an agent skill

Usage:
  skl add <source> [flags]

Arguments:
  <source>             Local folder path, Git repository URL, or GitHub owner/repo shorthand.

Flags:
  -g, --global         Install globally (in home directory).
  -p, --project        Install locally in the current directory (default).
  -y, --yes            Skip interactive prompts and accept defaults automatically.
  --copy               Force copying of skill files instead of creating symbolic links.
  -a, --agent <name>   Target a specific agent (default: auto-detects installed agents).
  -v, --verbose        Log all file copy and symlink changes.
`

const listHelp = `skl list - List installed agent skills

Usage:
  skl list [flags]

Flags:
  -g, --global         List global skl.
  -p, --project        List project skills (default).
  -a, --agent <name>   Filter results by agent type.
  --json               Output as structured JSON.
`

const removeHelp = `skl remove - Uninstall agent skills

Usage:
  skl remove [names...] [flags]

Arguments:
  [names...]           One or more names of the skills to remove.
                       If not provided, starts interactive selection.

Flags:
  -g, --global         Remove from global scope.
  -p, --project        Remove from project scope (default).
  -y, --yes            Skip confirmation prompt.
  --all                Uninstall all skills in the selected scope.
  -a, --agent <name>   Target a specific agent for removal.
`

const findHelp = `skl find - Search or retrieve installed skills

Usage:
  skl find <query> [flags]

Arguments:
  <query>              Name, slug, or keyword to search for.

Flags:
  -l, --local          Search only locally installed skl.
  -j, --json           Output search results in JSON format.
`

const updateHelp = `skl update - Update or refresh installed skills

Usage:
  skl update [name] [src] [flags]

Arguments:
  [name]               Name of the skill to update. If omitted, updates all skl.
  [src]                Optional new source path or URL.

Flags:
  -g, --global         Update in global scope.
  -p, --project        Update in project scope (default).
  -y, --yes            Skip confirmation prompt.
  -v, --verbose        Enable verbose output.
`

const configHelp = `skl config - Manage configuration overrides

Usage:
  skl config set <key> <value>
  skl config get <key>

Keys:
  canonicalDir.global   Override the global base directory.
  canonicalDir.project  Override the project base directory.
`

const syncHelp = `skl sync - Sync skills from node_modules

Usage:
  skl experimental_sync [flags]

Flags:
  -y, --yes            Skip confirmation prompt.
  -f, --force          Force overwrite existing skl.
  -v, --verbose        Enable verbose output.
  -a, --agent <name>   Filter target agents (comma-separated).
`

const installHelp = `skl experimental_install - Restore skills from lockfile

Usage:
  skl experimental_install
`

func hasHelp(args []string) bool {
	for _, arg := range args {
		if arg == "help" || arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}


func isInteractive() bool {
	fileInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func projectSkillsExist() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(cwd, "skills-lock.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, ".agents", "skills")); err == nil {
		return true
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(helpMessage)
		os.Exit(0)
	}

	cmd := strings.ToLower(os.Args[1])
	args := os.Args[2:]

	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(helpMessage)

	case "version", "-v", "--version":
		fmt.Printf("skl version %s\n", version)

	case "init":
		if hasHelp(args) {
			fmt.Print(initHelp)
			os.Exit(0)
		}
		var name string
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			name = args[0]
		}
		path, err := skl.InitSkill(skl.InitOptions{
			Name: name,
		})
		if err != nil {
			if errors.Is(err, skl.ErrSkillAlreadyExists) {
				fmt.Printf("⚠️  Warning: %v\n", err)
				os.Exit(0)
			}
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		displayDir := name
		if displayDir == "" {
			displayDir = "."
		}

		fmt.Println("\n\x1b[1;36m▲ Agent Skill Template Initialized Successfully!\x1b[0m")
		fmt.Println("--------------------------------------------------")
		fmt.Printf("📂 \x1b[1mWorkspace:\x1b[0m  %s\n", displayDir)
		fmt.Printf("📄 \x1b[1mCreated:\x1b[0m    %s\n", filepath.Base(path))
		fmt.Printf("📍 \x1b[1mLocation:\x1b[0m   %s\n", path)
		fmt.Println("--------------------------------------------------")
		fmt.Println("\n\x1b[1;32m👉 Next Steps:\x1b[0m")
		fmt.Printf("  1. \x1b[1mEdit:\x1b[0m   Open and describe your custom instructions:\n")
		if name != "" {
			fmt.Printf("             \x1b[33m$ code %s/SKILL.md\x1b[0m\n", name)
		} else {
			fmt.Println("             \x1b[33m$ code SKILL.md\x1b[0m")
		}
		fmt.Printf("  2. \x1b[1mLink:\x1b[0m   Register the skill locally into your workspace:\n")
		if name != "" {
			fmt.Printf("             \x1b[33m$ skl add ./%s\x1b[0m\n", name)
		} else {
			fmt.Println("             \x1b[33m$ skl add .\x1b[0m")
		}
		fmt.Println("  3. \x1b[1mShare:\x1b[0m  Push your skill repo to GitHub or deploy as static URLs!")
		fmt.Println("             To install remote skills, run:")
		fmt.Printf("             \x1b[33m$ skl add <github-username>/<repo-name>\x1b[0m\n\n")

	case "add", "i", "install", "a":
		if hasHelp(args) {
			fmt.Print(addHelp)
			os.Exit(0)
		}
		source, opts := skl.ParseAddOptions(args)
		if source == "" {
			fmt.Println("Error: source skill path is required")
			os.Exit(1)
		}
		err := skl.AddSkill(source, opts, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Successfully installed skill.")

	case "list", "ls":
		if hasHelp(args) {
			fmt.Print(listHelp)
			os.Exit(0)
		}
		var opts skl.ListOptions
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "-g" || arg == "--global" {
				opts.Global = true
			} else if arg == "-a" || arg == "--agent" {
				if i+1 < len(args) {
					i++
					opts.AgentFilter = append(opts.AgentFilter, skl.AgentType(args[i]))
				}
			}
		}

		installed, err := skl.ListInstalledSkills(opts)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(installed) == 0 {
			fmt.Println("No skills found.")
			return
		}

		// Support structured JSON output or pretty listing
		var asJson bool
		for _, arg := range args {
			if arg == "--json" {
				asJson = true
				break
			}
		}

		if asJson {
			bytes, _ := json.MarshalIndent(installed, "", "  ")
			fmt.Println(string(bytes))
		} else {
			fmt.Println("Installed Agent Skills:")
			for _, s := range installed {
				agentsStr := make([]string, len(s.Agents))
				for i, a := range s.Agents {
					agentsStr[i] = string(a)
				}
				fmt.Printf("  • %s (%s) - Scope: %s, Agents: %s\n", s.Name, s.Description, s.Scope, strings.Join(agentsStr, ", "))
			}
		}

	case "remove", "rm", "r":
		if hasHelp(args) {
			fmt.Print(removeHelp)
			os.Exit(0)
		}
		names, opts := skl.ParseRemoveOptions(args)
		if len(names) == 0 && !opts.All {
			if !opts.Yes && isInteractive() {
				// Interactive removal selection
				canonicalBase := skl.GetCanonicalSkillsDir(opts.Global, "")
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

				if len(installedSkills) == 0 {
					fmt.Println("No skills found to remove.")
					os.Exit(0)
				}

				fmt.Println("Installed skills:")
				for i, name := range installedSkills {
					fmt.Printf("  %d) %s\n", i+1, name)
				}

				reader := bufio.NewReader(os.Stdin)
				fmt.Print("Select skills to remove (comma-separated numbers, e.g. 1, 3): ")
				input, err := reader.ReadString('\n')
				if err != nil {
					fmt.Printf("Error reading input: %v\n", err)
					os.Exit(1)
				}

				input = strings.TrimSpace(input)
				parts := strings.Split(input, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if part == "" {
						continue
					}
					idx, err := strconv.Atoi(part)
					if err != nil || idx < 1 || idx > len(installedSkills) {
						fmt.Printf("Invalid selection: %q\n", part)
						os.Exit(1)
					}
					names = append(names, installedSkills[idx-1])
				}

				if len(names) == 0 {
					fmt.Println("No skills selected. Cancelled.")
					os.Exit(0)
				}
			} else {
				fmt.Println("Error: at least one skill name or --all flag is required")
				os.Exit(1)
			}
		}

		err := skl.RemoveSkills(names, opts, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Successfully removed skill(s).")

	case "find", "search", "f", "s":
		if hasHelp(args) {
			fmt.Print(findHelp)
			os.Exit(0)
		}
		if len(args) < 1 {
			fmt.Println("Error: search query is required")
			os.Exit(1)
		}
		query := args[0]
		var local bool
		var jsonOutput bool

		for _, arg := range args {
			if arg == "-l" || arg == "--local" {
				local = true
			}
			if arg == "-j" || arg == "--json" {
				jsonOutput = true
			}
		}

		res, err := skl.FindSkills(skl.FindOptions{
			Query: query,
			Local: local,
		})
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(res) == 0 {
			fmt.Printf("No skills found matching %q.\n", query)
			os.Exit(0)
		}

		if jsonOutput {
			bytes, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(bytes))
			os.Exit(0)
		}

		fmt.Println("Install remote skills with: skl add <source>")
		fmt.Println()
		for _, s := range res {
			if s.Scope == "remote" {
				installsBadge := ""
				if s.Installs > 0 {
					installsBadge = fmt.Sprintf(" (%d installs)", s.Installs)
				}
				fmt.Printf("%s@%s%s\n", s.Source, s.Name, installsBadge)
				fmt.Printf("  └ remote search result\n\n")
			} else {
				fmt.Printf("[local: %s] %s\n", s.Scope, s.Name)
				if s.Description != "" {
					fmt.Printf("  %s\n", s.Description)
				}
				fmt.Printf("  └ %s\n\n", s.Source)
			}
		}

	case "update", "check", "upgrade":
		if hasHelp(args) {
			fmt.Print(updateHelp)
			os.Exit(0)
		}
		var name string
		var src string
		var globalFlag bool
		var projectFlag bool
		var yesFlag bool
		var verboseFlag bool

		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "-g" || arg == "--global" {
				globalFlag = true
			} else if arg == "-p" || arg == "--project" {
				projectFlag = true
			} else if arg == "-y" || arg == "--yes" {
				yesFlag = true
			} else if arg == "-v" || arg == "--verbose" {
				verboseFlag = true
			} else if !strings.HasPrefix(arg, "-") {
				if name == "" {
					name = arg
				} else if src == "" {
					src = arg
				}
			}
		}

		if name != "" {
			// Single skill update
			opts := skl.UpdateOptions{
				Global:  globalFlag,
				Verbose: verboseFlag,
			}
			err := skl.UpdateSkill(name, src, opts)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			if src != "" {
				fmt.Printf("Successfully updated skill %q from source %q.\n", name, src)
			} else {
				fmt.Printf("Successfully refreshed skill %q.\n", name)
			}
		} else {
			// Bulk update
			var scope string
			if globalFlag && projectFlag {
				scope = "both"
			} else if globalFlag {
				scope = "global"
			} else if projectFlag {
				scope = "project"
			} else {
				if yesFlag || !isInteractive() {
					if projectSkillsExist() {
						scope = "project"
					} else {
						scope = "global"
					}
				} else {
					fmt.Println("Select update scope:")
					fmt.Println("  1) Project (Update skills in current directory)")
					fmt.Println("  2) Global (Update skills in home directory)")
					fmt.Println("  3) Both (Update all skills)")
					reader := bufio.NewReader(os.Stdin)
					fmt.Print("Selection (1-3): ")
					input, err := reader.ReadString('\n')
					if err != nil {
						fmt.Printf("Error reading input: %v\n", err)
						os.Exit(1)
					}
					input = strings.TrimSpace(input)
					switch input {
					case "1":
						scope = "project"
					case "2":
						scope = "global"
					case "3":
						scope = "both"
					default:
						fmt.Println("Invalid selection. Cancelled.")
						os.Exit(0)
					}
				}
			}

			var updateSkillsInScope = func(global bool) {
				canonicalBase := skl.GetCanonicalSkillsDir(global, "")
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

				if len(installedSkills) == 0 {
					scopeName := "project"
					if global {
						scopeName = "global"
					}
					fmt.Printf("No installed skills found in %s scope.\n", scopeName)
					return
				}

				scopeName := "Project"
				if global {
					scopeName = "Global"
				}
				fmt.Printf("Updating %s Skills:\n", scopeName)
				opts := skl.UpdateOptions{
					Global:  global,
					Verbose: verboseFlag,
				}
				for _, sName := range installedSkills {
					fmt.Printf("  • Updating skill %q...\n", sName)
					err := skl.UpdateSkill(sName, "", opts)
					if err != nil {
						fmt.Printf("    ⚠️ Failed: %v\n", err)
					} else {
						fmt.Printf("    ✓ Successfully updated %q\n", sName)
					}
				}
			}

			if scope == "project" || scope == "both" {
				updateSkillsInScope(false)
			}
			if scope == "global" || scope == "both" {
				updateSkillsInScope(true)
			}
		}

	case "config":
		if hasHelp(args) {
			fmt.Print(configHelp)
			os.Exit(0)
		}
		if len(args) < 2 {
			fmt.Println("Error: usage:\n  skl config set <key> <value>\n  skl config get <key>")
			os.Exit(1)
		}
		subCmd := strings.ToLower(args[0])
		if subCmd == "set" {
			if len(args) < 3 {
				fmt.Println("Error: value is required for set")
				os.Exit(1)
			}
			key := args[1]
			val := args[2]
			err := skl.SetConfigKey(key, val)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Successfully set %s = %s\n", key, val)
		} else if subCmd == "get" {
			key := args[1]
			val, err := skl.GetConfigKey(key)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(val)
		} else {
			fmt.Printf("Unknown config subcommand %q\n", subCmd)
			os.Exit(1)
		}

	case "experimental_sync", "sync":
		if hasHelp(args) {
			fmt.Print(syncHelp)
			os.Exit(0)
		}
		var opts skl.SyncOptions
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "-y" || arg == "--yes" {
				opts.Yes = true
			} else if arg == "-f" || arg == "--force" {
				opts.Force = true
			} else if arg == "-v" || arg == "--verbose" {
				opts.Verbose = true
			} else if arg == "-a" || arg == "--agent" {
				if i+1 < len(args) {
					i++
					parts := strings.Split(args[i], ",")
					for _, part := range parts {
						trimmed := strings.TrimSpace(part)
						if trimmed != "" {
							opts.Agent = append(opts.Agent, skl.AgentType(trimmed))
						}
					}
				}
			}
		}
		err := skl.SyncSkills(args, opts, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	case "experimental_install":
		if hasHelp(args) {
			fmt.Print(installHelp)
			os.Exit(0)
		}
		err := skl.RestoreFromLock(args, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Printf("Unknown command %q. Run with --help for options.\n", cmd)
		os.Exit(1)
	}
}


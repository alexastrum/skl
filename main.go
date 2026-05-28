package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexastrum/skl/pkg/skills"
)

const version = "1.0.0"

const helpMessage = `skl - Agent Skills CLI Manager

Usage:
  skl <command> [arguments]

Commands:
  init [name]        Initialize a new agent skill template in Cwd or a subfolder.
  add <source>       Install a local skill folder to project or global scope.
  list               List installed skills for detected agents.
  remove <names...>  Uninstall and delete skill configurations.
  find <query>       Search/retrieve an installed skill by name or slug.
  update <name> <src> Refresh an installed skill from its source path.
  version            Print CLI tool version.

Flags:
  -g, --global       Apply action to global scope (home directory).
  -y, --yes          Skip confirmation prompts (automatic non-interactive).
  --copy             Force copy instead of symlink.
  -a, --agent <name> Filter action by agent type.
  -v, --verbose      Log all persistent file system changes.
`

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
		var name string
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			name = args[0]
		}
		path, err := skills.InitSkill(skills.InitOptions{
			Name: name,
		})
		if err != nil {
			if errors.Is(err, skills.ErrSkillAlreadyExists) {
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

	case "add":
		source, opts := skills.ParseAddOptions(args)
		if source == "" {
			fmt.Println("Error: source skill path is required")
			os.Exit(1)
		}
		err := skills.AddSkill(source, opts, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Successfully installed skill.")

	case "list":
		var opts skills.ListOptions
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "-g" || arg == "--global" {
				opts.Global = true
			} else if arg == "-a" || arg == "--agent" {
				if i+1 < len(args) {
					i++
					opts.AgentFilter = append(opts.AgentFilter, skills.AgentType(args[i]))
				}
			}
		}

		installed, err := skills.ListInstalledSkills(opts)
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
		names, opts := skills.ParseRemoveOptions(args)
		if len(names) == 0 && !opts.All {
			fmt.Println("Error: at least one skill name or --all flag is required")
			os.Exit(1)
		}
		err := skills.RemoveSkills(names, opts, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Successfully removed skill(s).")

	case "find":
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

		res, err := skills.FindSkills(skills.FindOptions{
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

	case "update":
		if len(args) < 1 {
			fmt.Println("Error: usage: skl update <name> [src_path]")
			os.Exit(1)
		}
		name := args[0]
		var src string
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			src = args[1]
		}

		var opts skills.UpdateOptions
		for _, arg := range args {
			if arg == "-g" || arg == "--global" {
				opts.Global = true
				break
			}
		}

		err := skills.UpdateSkill(name, src, opts)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if src != "" {
			fmt.Printf("Successfully updated skill %q from source %q.\n", name, src)
		} else {
			fmt.Printf("Successfully refreshed skill %q.\n", name)
		}

	default:
		fmt.Printf("Unknown command %q. Run with --help for options.\n", cmd)
		os.Exit(1)
	}
}

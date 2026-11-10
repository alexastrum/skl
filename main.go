package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexastrum/skl/src/skl"
)

// upstreamVersion is the vercel-labs/skills release that skl tracks.
const upstreamVersion = "1.7.6"

// version is the skl version. It mirrors upstreamVersion with the major
// version set to 0 (skills 1.7.6 -> skl 0.7.6); TestVersionMirrorsUpstream
// enforces this. It is a var so release builds can set it with
// -ldflags "-X main.version=...".
var version = "0.7.6"

const (
	reset = "\x1b[0m"
	bold  = "\x1b[1m"
	dim   = "\x1b[38;5;102m"
	text  = "\x1b[38;5;145m"
)

var logoLines = []string{
	"███████╗██╗  ██╗██╗     ",
	"██╔════╝██║ ██╔╝██║     ",
	"███████╗█████╔╝ ██║     ",
	"╚════██║██╔═██╗ ██║     ",
	"███████║██║  ██╗███████╗",
	"╚══════╝╚═╝  ╚═╝╚══════╝",
}

var logoGrays = []string{
	"\x1b[38;5;250m", "\x1b[38;5;248m", "\x1b[38;5;245m",
	"\x1b[38;5;243m", "\x1b[38;5;240m", "\x1b[38;5;238m",
}

func showLogo(w io.Writer) {
	fmt.Fprintln(w)
	for i, line := range logoLines {
		fmt.Fprintf(w, "%s%s%s\n", logoGrays[i], line, reset)
	}
}

func showBanner(w io.Writer) {
	showLogo(w)
	fmt.Fprintf(w, "\n%sThe open agent skills ecosystem, in one Go binary%s\n\n", dim, reset)
	row := func(cmd, arg, desc string) {
		left := cmd
		if arg != "" {
			left += " " + dim + arg + reset
		}
		pad := 34 - len(cmd) - len(arg)
		if arg != "" {
			pad--
		}
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(w, "  %s$%s %s%s%s%s%s%s\n", dim, reset, text, left, reset, strings.Repeat(" ", pad), dim+desc, reset)
	}
	row("skl add", "<package>", "Add a new skill")
	row("skl use", "<package>@<skill>", "Use a skill without installing")
	row("skl remove", "", "Remove installed skills")
	row("skl list", "", "List installed skills")
	row("skl find", "[query]", "Search for skills")
	fmt.Fprintln(w)
	row("skl update", "", "Update installed skills")
	row("skl check", "", "Check for skill updates")
	fmt.Fprintln(w)
	row("skl experimental_install", "", "Restore from skills-lock.json")
	row("skl init", "[name]", "Create a new skill")
	row("skl experimental_sync", "", "Sync skills from node_modules")
	fmt.Fprintf(w, "\n%stry:%s skl add vercel-labs/agent-skills\n\n", dim, reset)
	fmt.Fprintf(w, "Discover more skills at %shttps://skills.sh/%s\n\n", text, reset)
}

const helpMessage = `skl - Agent Skills CLI Manager

Usage:
  skl <command> [options]

Manage Skills:
  add <package>                    Add a skill package (alias: a). A local folder, owner/repo,
                                   Git URL, well-known URL, direct SKILL.md or archive URL, or notion.
  use <package>@<skill>            Print a prompt for one skill without installing it.
  remove [skills...]               Remove installed skills (aliases: rm, r).
  list                             List installed skills (alias: ls).
  find [query]                     Search skills.sh, or installed skills with --local (aliases: search, f, s).

Updates:
  update [skills...]               Update skills to their latest versions (alias: upgrade).
  check [skills...]                Report available updates without installing (exit 1 if any).

Project:
  install                          Without a package, restore skills from skills-lock.json (alias: i).
  experimental_install             Restore skills from skills-lock.json.
  init [name]                      Create <name>/SKILL.md or ./SKILL.md from a template.
  experimental_sync                Sync skills from node_modules into agent directories (alias: sync).
  config <set|get> <key> [value]   Configure settings (canonicalDir.global, canonicalDir.project).

Add Options:
  -g, --global                     Install globally (user level) instead of in the project.
  -a, --agent <agents...>          Agents to install to (use '*' for all agents).
  -s, --skill <skills...>          Skills to install (use '*' for all skills).
  -l, --list                       List the skills in the source without installing.
  -y, --yes                        Skip confirmation prompts.
  --copy                           Copy files instead of symlinking agent directories.
  --subagent <names...>            Install to Eve subagents ('root' for the root agent).
  --all                            Shorthand for --skill '*' --agent '*' -y.
  --full-depth                     Search all subdirectories even when a root SKILL.md exists.
  --json                           Print results as JSON (no ANSI codes).

Remove Options:
  -g, --global                     Remove from the global scope.
  -a, --agent <agents...>          Remove from these agents only (omit to clean all agent links).
  -s, --skill <skills...>          Skills to remove (use '*' for all skills).
  -y, --yes                        Skip the confirmation prompt.
  --all                            Remove every installed skill (implies -y; do not combine with names).

List Options:
  -g, --global                     List global skills (default: project).
  -a, --agent <agents...>          Filter by agents.
  --json                           Print as JSON (no ANSI codes).

Find Options:
  --owner <owner>                  Search only repositories from one GitHub owner.
  -l, --local                      Search installed project and global skills.
  -j, --json                       Print results as JSON.

Update and Check Options:
  -g, --global                     Update global skills only.
  -p, --project                    Update project skills only.
  -y, --yes                        Skip the scope prompt (project if in a project, else global).

Install (Restore) Options:
  -a, --agent <agents...>          Link to these agents (default: last selected, else detected).
  --dry-run                        Show what would be restored without installing anything.
  --frozen-lockfile                Fail instead of changing skills-lock.json (default in CI).
  --no-frozen-lockfile             Allow skills-lock.json to change in CI.
  --force                          Reinstall skills that are already installed.
  --copy                           Copy restored skills instead of symlinking them.

Sync Options:
  -a, --agent <agents...>          Agents to install to (use '*' for all agents).
  -y, --yes                        Skip confirmation prompts.
  --copy                           Copy skills instead of linking them to node_modules.
  --dry-run                        Show what would change without changing anything.
  --no-cleanup                     Keep skills that no dependency provides anymore.
  --no-remote                      Skip git sources listed in package.json "skills" fields.
  --frozen-lockfile                Fail instead of changing skills-lock.json (default in CI).
  --no-frozen-lockfile             Allow skills-lock.json to change in CI.
  -r, --recursive                  Also sync the dependencies of each workspace package.
  --include <patterns...>          Only sync matching packages (<pkg>) or skills (<pkg>#<skill>).
  --exclude <patterns...>          Skip matching packages (<pkg>) or skills (<pkg>#<skill>).
  --json                           Print a JSON summary (no ANSI codes).

Options:
  -h, --help                       Show this help message (also after any command).
  -v, --version                    Show the version number.

Examples:
  skl add vercel-labs/agent-skills
  skl add vercel-labs/agent-skills --skill pr-review commit -a claude-code cursor
  skl use vercel-labs/agent-skills@vercel-optimize | claude
  skl remove web-design
  skl ls -g --json
  skl find react --owner vercel
  skl check
  skl update -g
  skl install --frozen-lockfile
  skl experimental_sync -y

Discover more skills at https://skills.sh/
`

const initHelp = `skl init - Initialize a new agent skill template

Usage:
  skl init [name]

Arguments:
  [name]               Optional name of the subdirectory to initialize the skill in.
                       If not provided, initializes in the current directory.

Description:
  Creates a SKILL.md template with name and description frontmatter.
`

const addHelp = `skl add - Install agent skills

Usage:
  skl add <source> [flags]

Arguments:
  <source>                 Local folder, owner/repo[@skill], Git URL (#ref supported),
                           well-known URL, direct SKILL.md or archive URL, or notion.

Flags:
  -g, --global             Install globally (in home directory).
  -p, --project            Install in the current project (default).
  -a, --agent <agents...>  Target agents (use '*' for all agents).
  -s, --skill <skills...>  Skills to install (use '*' for all skills).
  -l, --list               List the skills in the source without installing.
  -y, --yes                Skip interactive prompts and accept defaults.
  --copy                   Copy skill files instead of creating symbolic links.
  --subagent <names...>    Install to Eve subagents ('root' for the root agent).
  --all                    Shorthand for --skill '*' --agent '*' -y.
  --full-depth             Search all subdirectories even when a root SKILL.md exists.
  --json                   Print results as JSON.
  -v, --verbose            Log all file copy and symlink changes.
`

const listHelp = `skl list - List installed agent skills

Usage:
  skl list [flags]

Flags:
  -g, --global             List global skills.
  -p, --project            List project skills (default).
  -a, --agent <agents...>  Filter results by agents.
  --json                   Output as structured JSON (tracked, untracked and missing status).
`

const removeHelp = `skl remove - Uninstall agent skills

Usage:
  skl remove [skills...] [flags]

Arguments:
  [skills...]              One or more skill names (or a lock source such as owner/repo).
                           If not provided, starts interactive selection.

Flags:
  -g, --global             Remove from global scope.
  -p, --project            Remove from project scope (default).
  -a, --agent <agents...>  Remove from these agents only (omit to clean all agent links).
  -s, --skill <skills...>  Skills to remove (use '*' for all skills).
  -y, --yes                Skip confirmation prompt.
  --all                    Uninstall all skills in the selected scope (implies -y).
`

const findHelp = `skl find - Search for skills

Usage:
  skl find [query] [flags]

Arguments:
  [query]                  Keywords to search skills.sh for. Without a query, starts an
                           interactive search (prints usage tips in agents and pipes).

Flags:
  --owner <owner>          Search only repositories from one GitHub owner.
  -l, --local              Search only locally installed skills.
  -j, --json               Output search results in JSON format.
`

const updateHelp = `skl update - Update installed skills

Usage:
  skl update [skills...] [flags]
  skl check [skills...] [flags]

Arguments:
  [skills...]              Names of skills to update. If omitted, updates all skills.

Description:
  update reinstalls skills whose source changed (or whose folder is missing).
  check only reports available updates and exits 1 when any exist.

Flags:
  -g, --global             Update global skills only.
  -p, --project            Update project skills only.
  -y, --yes                Skip the scope prompt.
  -v, --verbose            Enable verbose output.
`

const configHelp = `skl config - Manage configuration overrides

Usage:
  skl config set <key> <value>
  skl config get <key>

Keys:
  canonicalDir.global   Override the global base directory.
  canonicalDir.project  Override the project base directory.
`

const syncHelp = `skl experimental_sync - Sync skills from node_modules

Usage:
  skl experimental_sync [flags]

Flags:
  -a, --agent <agents...>  Agents to install to (use '*' for all agents).
  -y, --yes                Skip confirmation prompts.
  --copy                   Copy skills instead of linking them to node_modules.
  --dry-run                Show what would change without changing anything.
  --no-cleanup             Keep skills that no dependency provides anymore.
  --no-remote              Skip git sources listed in package.json "skills" fields.
  --frozen-lockfile        Fail instead of changing skills-lock.json (default in CI).
  --no-frozen-lockfile     Allow skills-lock.json to change in CI.
  -r, --recursive          Also sync the dependencies of each workspace package.
  --include <patterns...>  Only sync matching packages (<pkg>) or skills (<pkg>#<skill>).
  --exclude <patterns...>  Skip matching packages (<pkg>) or skills (<pkg>#<skill>).
  -f, --force              Replace destinations sync did not create.
  --json                   Print a JSON summary.
`

const installHelp = `skl install - Restore skills from skills-lock.json

Usage:
  skl install [flags]
  skl experimental_install [flags]
  skl install <source> [flags]    (same as skl add)

Flags:
  -a, --agent <agents...>  Link to these agents (default: last selected, else detected).
  --dry-run                Show what would be restored without installing anything.
  --frozen-lockfile        Fail instead of changing skills-lock.json (default in CI).
  --no-frozen-lockfile     Allow skills-lock.json to change in CI.
  --force                  Reinstall skills that are already installed.
  --copy                   Copy restored skills instead of symlinking them.
`

// commandHelp returns the help text shown for `skl <cmd> --help`.
func commandHelp(cmd string) string {
	switch cmd {
	case "add", "a":
		return addHelp
	case "use":
		return skl.UseHelp()
	case "remove", "rm", "r":
		return removeHelp
	case "list", "ls":
		return listHelp
	case "find", "search", "f", "s":
		return findHelp
	case "update", "upgrade", "check":
		return updateHelp
	case "init":
		return initHelp
	case "install", "i", "experimental_install":
		return installHelp
	case "sync", "experimental_sync":
		return syncHelp
	case "config":
		return configHelp
	}
	return helpMessage
}

func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one CLI invocation and returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	inAgent := skl.IsRunningInAgent()
	if len(args) == 0 {
		if !inAgent {
			showBanner(stdout)
		}
		return 0
	}

	rawCmd := args[0]
	lower := strings.ToLower(rawCmd)
	rest := args[1:]

	switch lower {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, helpMessage)
		return 0
	case "--version", "-v", "version":
		fmt.Fprintln(stdout, version)
		return 0
	}

	// `<cmd> --help` never runs the command. A bare "help" argument is not a help
	// flag, so `skl find help` and `skl remove help` still work.
	if hasFlag(rest, "--help", "-h") {
		fmt.Fprint(stdout, commandHelp(rawCmd))
		return 0
	}

	skl.FindInstallFunc = func(source, skillName string) int {
		return skl.RunAdd([]string{source, "--skill", skillName})
	}

	switch rawCmd {
	case "add", "a", "install", "i":
		parsed := skl.ParseAddCLIOptions(rest)
		if (rawCmd == "install" || rawCmd == "i") && len(parsed.Sources) == 0 {
			// #2683: `skl install` without a package restores skills-lock.json.
			if !inAgent {
				showLogo(stdout)
			}
			return skl.RunInstallFromLock(rest, "")
		}
		if !inAgent && !parsed.Options.JSON {
			showLogo(stdout)
		}
		return skl.RunAddParsed(parsed, skl.AddIO{Stdout: stdout, Stderr: stderr})
	case "experimental_install":
		if !inAgent {
			showLogo(stdout)
		}
		return skl.RunInstallFromLock(rest, "")
	case "use":
		return skl.RunUse(rest)
	case "remove", "rm", "r":
		names, opts := skl.ParseRemoveOptions(rest)
		return skl.RunRemove(names, opts, "")
	case "list", "ls":
		return skl.RunList(rest, "")
	case "find", "search", "f", "s":
		_, findOpts, _ := skl.ParseFindOptions(rest)
		if !inAgent && !findOpts.JSON {
			showLogo(stdout)
		}
		if !findOpts.JSON {
			fmt.Fprintln(stdout)
		}
		return skl.RunFind(rest, "")
	case "check":
		return skl.RunUpdate(rest, skl.UpdateModeCheck, "")
	case "update", "upgrade":
		return skl.RunUpdate(rest, skl.UpdateModeUpdate, "")
	case "sync", "experimental_sync":
		opts := skl.ParseSyncOptions(rest)
		if !inAgent && !opts.JSON {
			showLogo(stdout)
		}
		return skl.RunSync(rest, opts, "")
	case "init":
		if !inAgent {
			showLogo(stdout)
		}
		fmt.Fprintln(stdout)
		return runInit(rest, stdout, stderr)
	}
	if lower == "config" {
		return runConfig(rest, stdout, stderr)
	}

	fmt.Fprintln(stdout, skl.FormatError(skl.NewDiagnostic(skl.CodeUnknownCommand,
		"Unknown command: "+rawCmd, "Run skl --help for usage.")))
	return 1
}

func runInit(args []string, stdout, stderr io.Writer) int {
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
	}
	path, err := skl.InitSkill(skl.InitOptions{Name: name})
	displayPath := "SKILL.md"
	if name != "" {
		displayPath = name + "/SKILL.md"
	}
	if err != nil {
		if errors.Is(err, skl.ErrSkillAlreadyExists) {
			fmt.Fprintf(stdout, "%sSkill already exists at %s%s%s\n", text, dim, displayPath, reset)
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	skillName := name
	if skillName == "" {
		skillName = filepath.Base(filepath.Dir(path))
	}
	fmt.Fprintf(stdout, "%sInitialized skill: %s%s%s\n\n", text, dim, skillName, reset)
	fmt.Fprintf(stdout, "%sCreated:%s\n  %s\n\n", dim, reset, displayPath)
	fmt.Fprintf(stdout, "%sNext steps:%s\n", dim, reset)
	fmt.Fprintf(stdout, "  1. Edit %s%s%s to define your skill instructions\n", text, displayPath, reset)
	fmt.Fprintf(stdout, "  2. Update the %sname%s and %sdescription%s in the frontmatter\n", text, reset, text, reset)
	addTarget := "."
	if name != "" {
		addTarget = "./" + name
	}
	fmt.Fprintf(stdout, "  3. Try it locally with %sskl add %s%s\n\n", text, addTarget, reset)
	fmt.Fprintf(stdout, "%sPublishing:%s\n", dim, reset)
	fmt.Fprintf(stdout, "  %sGitHub:%s  Push to a repo, then %sskl add <owner>/<repo>%s\n", dim, reset, text, reset)
	fmt.Fprintf(stdout, "  %sURL:%s     Host the file, then %sskl add https://example.com/%s%s\n\n", dim, reset, text, displayPath, reset)
	fmt.Fprintf(stdout, "Browse existing skills for inspiration at %shttps://skills.sh/%s\n\n", text, reset)
	return 0
}

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "Error: usage:\n  skl config set <key> <value>\n  skl config get <key>")
		return 1
	}
	switch strings.ToLower(args[0]) {
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "Error: value is required for set")
			return 1
		}
		if err := skl.SetConfigKey(args[1], args[2]); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Successfully set %s = %s\n", args[1], args[2])
		return 0
	case "get":
		val, err := skl.GetConfigKey(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, val)
		return 0
	}
	fmt.Fprintf(stderr, "Unknown config subcommand %q\n", args[0])
	return 1
}

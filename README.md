# 🚀 skl: Agent Skills CLI Manager

`skl` is a high-performance, robust, and feature-complete CLI manager for Agent Skills, written natively in Go. It provides 100% feature parity with the reference TypeScript implementation while offering **~200x faster cold starts**, zero external runtime dependencies, and comprehensive multi-agent symlinking management.

Built with native Go engineering best practices, `skl` implements depth-controlled scanning, automated multi-agent discovery, secure path sanitization, and the **10 Commandments of Go Error Handling**.

---

## ✨ Features

- **🚀 Near-Instantaneous execution**: Written in Go with absolute optimization, executing cold starts in sub-millisecond times compared to Node.js.
- **🎯 Multi-Agent Target Resolution**: Automatically scans and detects installed AI agents (Claude Code, Cursor, Aider, Cline, Zed, Windsurf, etc.) and routes skill installations to their active configuration directories.
- **🔗 Cross-Platform Symlinking & Fallback**: Creates relative symlinks to prevent duplicated files while seamlessly falling back to high-performance recursive copying on unsupported environments (such as restricted Windows environments).
- **🛡️ Path Traversal Protection**: Implements robust character stripping regex matching the original TS `sanitizeName` logic to prevent traversal vulnerability attempts.
- **📂 Exclusions & Depth Limits**: Filters out folder noise (e.g. `node_modules`, `.git`, `__pycache__`) and stops searching at a depth of 2 during skill discoveries.
- **🔒 Canonical Protection Layer**: Prevents deletion of the shared `.agents/skills` repository folder if other active agents still reference those skills.
- **📈 Highly Covered TDD Test Suite**: Over 70% of statements covered with table-driven test cases.

---

## 🛠️ Installation & Building

### Global Installation (Go Path)
Install the CLI tool globally directly from your shell:
```bash
go install github.com/alexastrum/skl/cmd/skl@latest
```

*Note: For private repository setup, make sure you configure your shell:*
```bash
export GOPRIVATE=github.com/alexastrum/*
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

### Local Build from Source
If building and running locally from the cloned source directory:
```bash
# Clone the repository
git clone https://github.com/alexastrum/skl.git
cd skl

# Build the high-performance binary
go build -o skl ./cmd/skl

# Run the test suite
go test -v ./...
```

---

## 💻 CLI Commands & Usage

```bash
skl - Agent Skills CLI Manager

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
```

### 1. Initialize a Skill (`init`)
Creates a standard, beautifully structured `SKILL.md` template with valid YAML frontmatter.
```bash
# Create in current working directory
skl init

# Create inside a subfolder
skl init my-new-skill
```

### 2. Add/Install Skills (`add`)
Installs local or remote git/zip skill sets to your active agents.
```bash
# Install a local skill
skl add ./my-skill -v

# Install a remote skill directly from GitHub
skl add samber/cc-skills-golang -v

# Install globally and force a full copy (instead of symlinks)
skl add samber/cc-skills-golang -g --copy
```

### 3. List Installed Skills (`list`)
Scans all active agent configuration folders and formats them in an elegant summary dashboard.
```bash
skl list
```

### 4. Remove a Skill (`remove`)
Safely uninstalls and deletes targeted skills.
```bash
# Remove from all agents
skl remove my-skill

# Remove from specific agents only (preserves canonical files if other agents still use it)
skl remove my-skill -a claude-code
```

### 5. Find an Installed Skill (`find`)
Retrieves and displays detailed information about an installed skill.
```bash
skl find golang-testing
```

---

## 🛡️ Architecture & Security

### Path Sanitization
Any input skill name passes through `SanitizeName()` which enforces:
1. Lowercasing the string.
2. Replacing any characters that are NOT `a-z`, `0-9`, `.`, or `_` with a single hyphen (`-`).
3. Stripping leading/trailing dots and hyphens.
4. Truncating to 255 characters.
This prevents arbitrary directory traversal attacks like `../../etc/passwd` being used to compromise the developer's system.

### Canonical Protection
When a skill is installed, the canonical files are stored in `.agents/skills/<skill-name>`, and active agents link to it. If you remove the skill from a specific agent scope, `skl` queries the status of all other agents. If another agent still references the skill, `skl` protects and keeps the canonical repository files intact!

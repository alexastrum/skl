# skl: Agent Skills CLI Manager

`skl` is a fast, dependency-free CLI tool for managing Agent Skills, written in Go.

This project is a Go port and direct rewrite of the original [`skills`](https://www.npmjs.com/package/skills) (commonly run via `npx skills`) TypeScript package. It replicates its full command set and logic, serving as a high-performance, secure replacement that is immune to NPM supply chain attacks.

## Why Go?

TypeScript is great in the browser, but Node.js is a drag for CLI tools. You get bloated dependencies, slow startup times, and supply-chain headaches.

I could have used Rust, but it felt like overkill. Rust is great for high-concurrency kernels or safety-critical engines, but fighting a borrow checker and managing lifetime annotations adds too much friction for a simple symlinking tool. Go strikes the perfect balance: it compiles in less than a second, has a rich standard library, and lets me write straightforward, synchronous code without importing half of crates.io.

### Zero dependencies
The original TypeScript CLI pulled in 127 packages just to handle basic file tasks and networking. The Go version uses none. I wrote it entirely using the Go standard library, only pulling in `yaml.v3` to parse frontmatter.

### A much smaller footprint
The physical size difference is ridiculous:
*   Original TypeScript: 10,108 lines of code across 33 files, plus a 116MB `node_modules` folder.
*   Go rewrite (`skl`): 2,864 lines of code across 11 files, compiling to a 9.2MB static binary.

Trading V8 startup routines, async task wrappers, and build tools for flat, synchronous Go code cut my line count by 71%.

### Instant startup
Bootstrapping V8, imports, and JS parsing makes Node tools feel sluggish. Go is a single compiled binary that starts near-instantly:
*   `time node skills-ts version` -> 100ms
*   `time skl version` -> 2ms

### Real error handling
Go forces you to treat errors as values, not exceptions. Wrapping them at package boundaries and handling them explicitly makes the CLI incredibly predictable.

## Installation and usage

### Global installation
Install the CLI tool globally:
```bash
go install github.com/alexastrum/skl@latest
```

For private repositories, configure your shell environment:
```bash
export GOPRIVATE=github.com/alexastrum/*
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

### Building from source
```bash
# Clone the repository
git clone https://github.com/alexastrum/skl.git
cd skl

# Build the binary
go build -o skl

# Run tests
go test -v ./...
```

## CLI commands

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

### 1. Initialize a skill (`init`)
Creates a `SKILL.md` template with YAML frontmatter:
```bash
skl init my-new-skill
```

### 2. Add skills (`add`)
Installs local or remote skills to active agents:
```bash
skl add ./my-skill -v
skl add samber/cc-skills-golang -g --copy
```

### 3. List installed skills (`list`)
Scans agent folders and displays an overview:
```bash
skl list
```

### 4. Remove a skill (`remove`)
Safely uninstalls and deletes skills:
```bash
skl remove my-skill
```

### 5. Find an installed skill (`find`)
Searches for an installed skill by name:
```bash
skl find golang-testing
```

## Features
`skl` is a feature-complete port of the original TypeScript CLI:
*   **Flexible sources**: Install skills from local folders, full GitHub/GitLab URLs, or shorthands (`owner/repo`).
*   **Zero-overhead linking**: Symlinks skills into agent active scopes by default, creating a single source of truth for easy updates (or use `--copy` to write independent physical copies).
*   **Comprehensive safety**: Enforces directory-traversal protection and filename sanitization at every step.
*   **Flexible scopes**: Targets individual projects or acts as a global system-wide registry with `-g`.

## Supported Agents (51+)
`skl` automatically detects and installs to any of these developer assistants and editors out of the box:

<details>
<summary><b>Click to expand full list of 51 supported agents</b></summary>

| Agent | `--agent` key | Project Folder | Global Folder |
| :--- | :--- | :--- | :--- |
| **AiderDesk** | `aider-desk` | `.aider-desk/skills/` | `~/.aider-desk/skills/` |
| **Antigravity** | `antigravity` | `.agents/skills/` | `~/.gemini/antigravity/skills/` |
| **Augment** | `augment` | `.augment/skills/` | `~/.augment/skills/` |
| **IBM Bob** | `bob` | `.bob/skills/` | `~/.bob/skills/` |
| **Claude Code** | `claude-code` | `.claude/skills/` | `~/.claude/skills/` |
| **OpenClaw** | `openclaw` | `skills/` | `~/.openclaw/skills/` |
| **CodeArts Agent** | `codearts-agent` | `.codeartsdoer/skills/` | `~/.codeartsdoer/skills/` |
| **CodeBuddy** | `codebuddy` | `.codebuddy/skills/` | `~/.codebuddy/skills/` |
| **Codemaker** | `codemaker` | `.codemaker/skills/` | `~/.codemaker/skills/` |
| **Code Studio** | `codestudio` | `.codestudio/skills/` | `~/.codestudio/skills/` |
| **Codex** | `codex` | `.agents/skills/` | `~/.codex/skills/` |
| **Command Code** | `command-code` | `.commandcode/skills/` | `~/.commandcode/skills/` |
| **Continue** | `continue` | `.continue/skills/` | `~/.continue/skills/` |
| **Cortex Code** | `cortex` | `.cortex/skills/` | `~/.snowflake/cortex/skills/` |
| **Crush** | `crush` | `.crush/skills/` | `~/.config/crush/skills/` |
| **Cursor** | `cursor` | `.agents/skills/` | `~/.cursor/skills/` |
| **Deep Agents** | `deepagents` | `.agents/skills/` | `~/.deepagents/agent/skills/` |
| **Devin for Terminal** | `devin` | `.devin/skills/` | `~/.config/devin/skills/` |
| **Droid** | `droid` | `.factory/skills/` | `~/.factory/skills/` |
| **Firebender** | `firebender` | `.agents/skills/` | `~/.firebender/skills/` |
| **ForgeCode** | `forgecode` | `.forge/skills/` | `~/.forge/skills/` |
| **Gemini CLI** | `gemini-cli` | `.agents/skills/` | `~/.gemini/skills/` |
| **GitHub Copilot** | `github-copilot` | `.agents/skills/` | `~/.copilot/skills/` |
| **Goose** | `goose` | `.goose/skills/` | `~/.config/goose/skills/` |
| **Hermes Agent** | `hermes-agent` | `.hermes/skills/` | `~/.hermes/skills/` |
| **Junie** | `junie` | `.junie/skills/` | `~/.junie/skills/` |
| **iFlow CLI** | `iflow-cli` | `.iflow/skills/` | `~/.iflow/skills/` |
| **Kilo Code** | `kilo` | `.kilocode/skills/` | `~/.kilocode/skills/` |
| **Kiro CLI** | `kiro-cli` | `.kiro/skills/` | `~/.kiro/skills/` |
| **Kode** | `kode` | `.kode/skills/` | `~/.kode/skills/` |
| **MCPJam** | `mcpjam` | `.mcpjam/skills/` | `~/.mcpjam/skills/` |
| **Mistral Vibe** | `mistral-vibe` | `.vibe/skills/` | `~/.vibe/skills/` |
| **Mux** | `mux` | `.mux/skills/` | `~/.mux/skills/` |
| **OpenCode** | `opencode` | `.agents/skills/` | `~/.config/opencode/skills/` |
| **OpenHands** | `openhands` | `.openhands/skills/` | `~/.openhands/skills/` |
| **Pi** | `pi` | `.pi/skills/` | `~/.pi/agent/skills/` |
| **Qoder** | `qoder` | `.qoder/skills/` | `~/.qoder/skills/` |
| **Qwen Code** | `qwen-code` | `.qwen/skills/` | `~/.qwen/skills/` |
| **Rovo Dev** | `rovodev` | `.rovodev/skills/` | `~/.rovodev/skills/` |
| **Roo Code** | `roo` | `.roo/skills/` | `~/.roo/skills/` |
| **Tabnine CLI** | `tabnine-cli` | `.tabnine/agent/skills/` | `~/.tabnine/agent/skills/` |
| **Trae / Trae CN** | `trae` / `trae-cn` | `.trae/skills/` | `~/.trae/skills/` / `~/.trae-cn/skills/` |
| **Windsurf** | `windsurf` | `.windsurf/skills/` | `~/.codeium/windsurf/skills/` |
| **Zencoder** | `zencoder` | `.zencoder/skills/` | `~/.zencoder/skills/` |
| **Neovate** | `neovate` | `.neovate/skills/` | `~/.neovate/skills/` |
| **Pochi** | `pochi` | `.pochi/skills/` | `~/.pochi/skills/` |
| **AdaL** | `adal` | `.adal/skills/` | `~/.adal/skills/` |
| **Universal / Replit** | `universal` / `replit` | `.agents/skills/` | `~/.config/agents/skills/` |

</details>

## Architecture and security

### Path sanitization
Every input skill name is sanitized using `SanitizeName()`. This replaces special characters with hyphens, lowercases the string, and truncates to 255 characters to prevent directory traversal attempts (CWE-22).

### Shared skill deduplication
To save disk space and avoid cloning identical git repositories multiple times, skills are downloaded once to a shared directory (`.agents/skills/<skill-name>`) and then symlinked into each agent's active scope. When you remove a skill from a specific agent, the CLI doesn't blindly delete the shared source files. Instead, it checks active references across all configured agents in the workspace, only deleting the physical repository files when the last agent stops using it. This prevents breaking other active agents that still depend on those symlinks.

### Agent autodetection
You don't need to manually configure which agents you are running. When you add, list, or update skills, `skl` automatically crawls standard locations to detect active environments:
*   **Project-level detection**: Scans the current working directory for active agent folders (such as `.cursor/`, `.claude/`, `.cline/`, or `.zed/`). If present, those agents are targeted for local workspace actions.
*   **Global environment detection**: Leverages platform-specific standard paths (like `~/.config`, `$XDG_CONFIG_HOME`, or `%APPDATA%` on Windows) to discover active installations of editors and developer assistants.

### Scope autodetection
When running non-interactively with `-y` or `--yes`, the CLI automatically resolves the installation scope:
*   If your current working directory contains an active project or initialized agent folders, it installs to the **project scope** (`./.agents/skills/`).
*   If no local project environment is detected, it falls back to the **global scope** (`~/.gemini/antigravity/skills/` or equivalent).

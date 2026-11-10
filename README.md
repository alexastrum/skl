# skl: Agent Skills CLI Manager

`skl` is a fast, dependency-free CLI tool for managing Agent Skills, written in Go.

This project is a Go port of the original [`skills`](https://www.npmjs.com/package/skills) TypeScript package (commonly run via `npx skills`). It tracks upstream [vercel-labs/skills](https://github.com/vercel-labs/skills) v1.7.6 command for command and flag for flag, and `skl --version` prints that upstream version. It is a single static binary, so there is no `node_modules` to audit and no NPM supply chain to worry about.

## Why Go?

TypeScript is great in the browser, but Node.js is a drag for CLI tools. You get bloated dependencies, slow startup times, and supply-chain headaches.

I could have used Rust, but it felt like overkill. Rust is great for high-concurrency kernels or safety-critical engines, but fighting a borrow checker and managing lifetime annotations adds too much friction for a simple symlinking tool. Go strikes the perfect balance: it compiles in less than a second, has a rich standard library, and lets me write straightforward, synchronous code without importing half of crates.io.

### Zero dependencies
The Go version is written against the standard library. The one exception is `yaml.v3`, which parses `SKILL.md` frontmatter. The binary needs nothing else on the machine. `git` is only used when you install from a git source, and GitHub sources can fall back to a ZIP download with `-y` when `git` is missing.

The upstream CLI at v1.7.6 declares two runtime dependencies (`tar` and `yaml`, 7 packages once resolved) on top of the Node runtime itself, and its lock file lists 156 packages for development.

### A smaller footprint to ship
The source is about the same size as the TypeScript CLI, since it ports it feature for feature. The difference is in what you ship:
*   Original TypeScript (v1.7.6): 17,205 lines of source across 41 files, plus Node.js and the `node_modules` folder.
*   Go port (`skl`): 17,427 lines of source across 22 files, compiling to one 11.8MB static binary (linux/amd64, Go 1.26).

Flat, synchronous Go code with error values instead of async task wrappers fits in about half as many files.

### Instant startup
Bootstrapping V8, imports, and JS parsing makes Node tools feel sluggish. Go is a single compiled binary that starts near-instantly. On my Linux machine `skl version` returns in about 7ms (median of 20 runs, process spawn included), which `time` rounds down to 0.00s.

### Real error handling
Go forces you to treat errors as values, not exceptions. Wrapping them at package boundaries and handling them explicitly makes the CLI predictable. Failures carry the upstream `SKILLS_*` diagnostic codes (all 96 are ported), so the message you see is the same one `npx skills` prints.

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

### Running Unsigned Release Binaries
If you install `skl` using `go install github.com/alexastrum/skl@latest` or compile it from source, the binary will be built natively on your system and runs without issues. 

However, if you download the pre-compiled release executables from GitHub, they are unsigned. Depending on your operating system, security filters may prevent them from launching.

#### macOS (Gatekeeper Quarantine)
When you download and try to execute the unsigned macOS binary, Apple Gatekeeper may quarantine the file and display an error ("'skl-darwin-arm64' is damaged and can’t be opened" or "developer cannot be verified").
* **Command Line Bypass**: To remove the quarantine attribute, run the following command in your terminal:
  ```bash
  xattr -d com.apple.quarantine ./skl-darwin-arm64
  ```
* **System Settings Bypass**: Alternatively, navigate to **System Settings > Privacy & Security**, scroll down to the Security section, and click **Allow Anyway** next to the warning for `skl`.

#### Windows (SmartScreen Filter)
When running the unsigned Windows executable (`skl-windows-amd64.exe`), Windows Defender SmartScreen may display a warning ("Windows protected your PC").
* **Manual Bypass**: Click on **More info** inside the SmartScreen dialog, then click **Run anyway**.
* **PowerShell Bypass**: Alternatively, unblock the file using PowerShell:
  ```powershell
  Unblock-File -Path .\skl-windows-amd64.exe
  ```


## CLI commands

This is the output of `skl --help`. Every command also accepts `--help` on its own.

```bash
skl - Agent Skills CLI Manager

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
```

### 1. Initialize a skill (`init`)
Creates a `SKILL.md` template with YAML frontmatter:
```bash
skl init my-new-skill
```

### 2. Add skills (`add`)
Installs local or remote skills to the agents it detects. Without `-y` it asks for the scope and the agents:
```bash
skl add ./my-skill
skl add samber/cc-skills-golang -g --copy
skl add vercel-labs/agent-skills --skill pr-review -a claude-code cursor -y
```

### 3. Use a skill without installing it (`use`)
Fetches one skill and prints a prompt that you can pipe into an agent:
```bash
skl use vercel-labs/agent-skills@web-design-guidelines | claude
```

### 4. List installed skills (`list`)
Scans agent folders and displays an overview. Each skill is marked `tracked`, `untracked`, or `missing` against `skills-lock.json`:
```bash
skl list
skl ls -g --json
```

### 5. Remove a skill (`remove`)
Removes the agent links, and the shared copy once no agent uses it:
```bash
skl remove my-skill
skl remove --all -g
```

### 6. Find a skill (`find`)
Searches [skills.sh](https://skills.sh/), or your installed skills with `--local`:
```bash
skl find react --owner vercel
skl find golang --local
```

### 7. Check for updates (`check`)
Reports which installed skills changed at their source. It writes nothing and exits 1 when an update exists, so a CI job can fail when a skill is stale:
```bash
skl check
```

### 8. Update skills (`update`)
Reinstalls skills whose source changed, or whose folder went missing:
```bash
skl update
skl update -g golang-cli
```

### 9. Restore from the lockfile (`install`)
Every `add` records the skill in `skills-lock.json`. On a fresh clone, `install` with no source (or `experimental_install`) reinstalls everything the lockfile lists. In CI the lockfile is frozen by default:
```bash
skl install
skl install --frozen-lockfile --dry-run
```

`skl install <source>` is the same as `skl add <source>`.

### 10. Sync skills from `node_modules` (`experimental_sync`)
Links the skills that your npm dependencies ship (in `skills/` or `dist/skills/`), plus the git sources named in the `skills` field of `package.json`, into your agent directories. `sync` is an alias:
```bash
skl sync -y
skl experimental_sync --dry-run -r
```

### 11. Configure the canonical directories (`config`)
By default skills live in `.agents/skills` (project) and `~/.agents/skills` (global). Both can be moved:
```bash
skl config set canonicalDir.global ~/skills
skl config get canonicalDir.global
```

The configuration file is `$XDG_CONFIG_HOME/skills/config.json` (`~/.config/skills/config.json` by default).

## Features
`skl` covers the full upstream command set:
*   **Flexible sources**: Install from local folders, `owner/repo` shorthands, GitHub, GitLab, and Azure DevOps URLs (branch, tag, or commit pinned), SSH remotes, direct `SKILL.md` links, `.zip` and `.tar.gz` archives, `.well-known/skills` endpoints, skills.sh short names, and Notion pages through the `ntn` CLI.
*   **Zero-overhead linking**: Symlinks skills into each agent's directory by default, so one copy serves every agent. Use `--copy` for independent physical copies.
*   **Lockfile**: `skills-lock.json` records every install with its source and content hash, so `install`, `check`, and `update` know what changed.
*   **Agent targeting**: Pass `-a` with one or more agents, or `*` for all of them. Skills can also be scoped to Eve subagents with `--subagent`.
*   **Machine output**: `--json` on `add`, `list`, `find`, and `sync` prints one JSON value with no ANSI codes.
*   **Safety checks**: Directory-traversal protection on every path, symlink confinement when copying skill folders, terminal-escape stripping on skill metadata before it is printed, and size limits on archives.
*   **Flexible scopes**: Targets the current project, or the whole user account with `-g`.

## Supported Agents (79)
`skl` detects and installs to any of these developer assistants and editors out of the box. Agents whose project folder is `.agents/skills/` read the shared directory directly and need no link.

<details>
<summary><b>Click to expand the full list of 79 supported agents</b></summary>

| Agent | `--agent` key | Project Folder | Global Folder |
| :--- | :--- | :--- | :--- |
| **AiderDesk** | `aider-desk` | `.aider-desk/skills/` | `~/.aider-desk/skills/` |
| **Amp** | `amp` | `.agents/skills/` | `~/.config/agents/skills/` |
| **Antigravity** | `antigravity` | `.agents/skills/` | `~/.gemini/config/skills/` |
| **Antigravity CLI** | `antigravity-cli` | `.agents/skills/` | `~/.gemini/config/skills/` |
| **AstrBot** | `astrbot` | `data/skills/` | `~/.astrbot/data/skills/` |
| **Autohand Code CLI** | `autohand-code` | `.autohand/skills/` | `~/.autohand/skills/` |
| **Augment** | `augment` | `.augment/skills/` | `~/.augment/skills/` |
| **IBM Bob** | `bob` | `.bob/skills/` | `~/.bob/skills/` |
| **Claude Code** | `claude-code` | `.claude/skills/` | `~/.claude/skills/` |
| **OpenClaw** | `openclaw` | `skills/` | `~/.openclaw/skills/` |
| **Cline** | `cline` | `.agents/skills/` | `~/.agents/skills/` |
| **CodeArts Agent** | `codearts-agent` | `.codeartsdoer/skills/` | `~/.codeartsdoer/skills/` |
| **CodeBuddy** | `codebuddy` | `.codebuddy/skills/` | `~/.codebuddy/skills/` |
| **Codemaker** | `codemaker` | `.codemaker/skills/` | `~/.codemaker/skills/` |
| **Code Studio** | `codestudio` | `.codestudio/skills/` | `~/.codestudio/skills/` |
| **Codex** | `codex` | `.agents/skills/` | `~/.agents/skills/` |
| **Command Code** | `command-code` | `.commandcode/skills/` | `~/.commandcode/skills/` |
| **Continue** | `continue` | `.continue/skills/` | `~/.continue/skills/` |
| **Cortex Code** | `cortex` | `.cortex/skills/` | `~/.snowflake/cortex/skills/` |
| **Crush** | `crush` | `.crush/skills/` | `~/.config/crush/skills/` |
| **Cursor** | `cursor` | `.agents/skills/` | `~/.cursor/skills/` |
| **Deep Agents** | `deepagents` | `.agents/skills/` | `~/.deepagents/agent/skills/` |
| **Devin for Terminal** | `devin` | `.devin/skills/` | `~/.config/devin/skills/` |
| **Dexto** | `dexto` | `.agents/skills/` | `~/.agents/skills/` |
| **Droid** | `droid` | `.agents/skills/` | `~/.factory/skills/` |
| **Eve** | `eve` | `agent/skills/` | (project only) |
| **Firebender** | `firebender` | `.agents/skills/` | `~/.firebender/skills/` |
| **ForgeCode** | `forgecode` | `.forge/skills/` | `~/.forge/skills/` |
| **fx** | `fx` | `.agents/skills/` | `~/.agents/skills/` |
| **Gemini CLI** | `gemini-cli` | `.agents/skills/` | `~/.gemini/skills/` |
| **GitHub Copilot** | `github-copilot` | `.agents/skills/` | `~/.copilot/skills/` |
| **Goose** | `goose` | `.goose/skills/` | `~/.config/goose/skills/` |
| **Grok Build** | `grok` | `.grok/skills/` | `~/.grok/skills/` |
| **Hermes Agent** | `hermes-agent` | `.hermes/skills/` | `~/.hermes/skills/` |
| **inference.sh** | `inference-sh` | `.inferencesh/skills/` | `~/.inferencesh/skills/` |
| **Jazz** | `jazz` | `.jazz/skills/` | `~/.jazz/skills/` |
| **Junie** | `junie` | `.junie/skills/` | `~/.junie/skills/` |
| **iFlow CLI** | `iflow-cli` | `.iflow/skills/` | `~/.iflow/skills/` |
| **Kilo Code** | `kilo` | `.agents/skills/` | `~/.kilo/skills/` |
| **Kimchi** | `kimchi` | `.kimchi/skills/` | `~/.config/kimchi/harness/skills/` |
| **Kimi Code CLI** | `kimi-code-cli` | `.agents/skills/` | `~/.agents/skills/` |
| **Kiro CLI** | `kiro-cli` | `.kiro/skills/` | `~/.kiro/skills/` |
| **Kode** | `kode` | `.kode/skills/` | `~/.kode/skills/` |
| **Lingma** | `lingma` | `.lingma/skills/` | `~/.lingma/skills/` |
| **Loaf** | `loaf` | `.agents/skills/` | `~/.agents/skills/` |
| **MCPJam** | `mcpjam` | `.mcpjam/skills/` | `~/.mcpjam/skills/` |
| **MiniMax Code** | `minimax-code` | `.minimax/skills/` | `~/.minimax/skills/` |
| **Mistral Vibe** | `mistral-vibe` | `.vibe/skills/` | `~/.vibe/skills/` |
| **Moxby** | `moxby` | `.moxby/skills/` | `~/.moxby/skills/` |
| **Mux** | `mux` | `.mux/skills/` | `~/.mux/skills/` |
| **OpenCode** | `opencode` | `.agents/skills/` | `~/.config/opencode/skills/` |
| **OpenHands** | `openhands` | `.openhands/skills/` | `~/.openhands/skills/` |
| **Ona** | `ona` | `.ona/skills/` | `~/.ona/skills/` |
| **Pi** | `pi` | `.agents/skills/` | `~/.agents/skills/` |
| **Posit Assistant** | `posit-assistant` | `.posit/assistant/skills/` | `~/.posit/assistant/skills/` |
| **Qoder** | `qoder` | `.qoder/skills/` | `~/.qoder/skills/` |
| **Qoder CN** | `qoder-cn` | `.qoder/skills/` | `~/.qoder-cn/skills/` |
| **Qwen Code** | `qwen-code` | `.qwen/skills/` | `~/.qwen/skills/` |
| **Replit** | `replit` | `.agents/skills/` | `~/.config/agents/skills/` |
| **Reasonix** | `reasonix` | `.reasonix/skills/` | `~/.reasonix/skills/` |
| **Rovo Dev** | `rovodev` | `.rovodev/skills/` | `~/.rovodev/skills/` |
| **Roo Code** | `roo` | `.roo/skills/` | `~/.roo/skills/` |
| **Sarvam Code** | `sarvam-code` | `.agents/skills/` | `~/.agents/skills/` |
| **Tabnine CLI** | `tabnine-cli` | `.tabnine/agent/skills/` | `~/.tabnine/agent/skills/` |
| **Terramind** | `terramind` | `.terramind/skills/` | `~/.terramind/skills/` |
| **Tinycloud** | `tinycloud` | `.tinycloud/skills/` | `~/.tinycloud/skills/` |
| **Trae** | `trae` | `.trae/skills/` | `~/.trae/skills/` |
| **Trae CN** | `trae-cn` | `.trae/skills/` | `~/.trae-cn/skills/` |
| **Warp** | `warp` | `.agents/skills/` | `~/.agents/skills/` |
| **Windsurf** | `windsurf` | `.windsurf/skills/` | `~/.codeium/windsurf/skills/` |
| **Zed** | `zed` | `.agents/skills/` | `~/.agents/skills/` |
| **ZCode** | `zcode` | `.zcode/skills/` | `~/.zcode/skills/` |
| **Zencoder** | `zencoder` | `.zencoder/skills/` | `~/.zencoder/skills/` |
| **Zenflow** | `zenflow` | `.zencoder/skills/` | `~/.zencoder/skills/` |
| **Neovate** | `neovate` | `.neovate/skills/` | `~/.neovate/skills/` |
| **Pochi** | `pochi` | `.pochi/skills/` | `~/.pochi/skills/` |
| **PromptScript** | `promptscript` | `.agents/skills/` | (project only) |
| **AdaL** | `adal` | `.adal/skills/` | `~/.adal/skills/` |
| **Universal** | `universal` | `.agents/skills/` | `~/.config/agents/skills/` |

Some global folders follow an environment variable when it is set: `CLAUDE_CONFIG_DIR`, `VIBE_HOME`, `HERMES_HOME`, `AUTOHAND_HOME`, `GROK_HOME`, `REASONIX_HOME`, and `XDG_CONFIG_HOME` for the `~/.config` entries. OpenClaw also recognizes its older `~/.clawdbot` and `~/.moltbot` homes.

</details>

## Architecture and security

### Path sanitization
Every input skill name is sanitized using `SanitizeName()`. This replaces special characters with hyphens, lowercases the string, and truncates to 255 characters to prevent directory traversal attempts (CWE-22).

### Shared skill deduplication
Skills are downloaded once to a shared canonical directory (`.agents/skills/<skill-name>` in a project, `~/.agents/skills/<skill-name>` globally) and then symlinked into each agent's own folder. Agents that already read `.agents/skills` need no link at all. When you remove a skill from one agent, the CLI checks which other configured agents still reference the shared copy, and deletes the files only when the last agent stops using it. Global installs for Antigravity are the one exception: Antigravity reads `~/.gemini/config/skills`, so that folder gets the link.

### Agent autodetection
You don't need to manually configure which agents you are running. When you add, list, or update skills, `skl` looks in the standard locations to detect active environments:
*   **Project-level detection**: Scans the current working directory for agent folders (such as `.cursor/`, `.claude/`, or `.zed/`). If present, those agents are targeted for project installs.
*   **Global environment detection**: Checks each agent's home folder (`~/.claude`, `~/.gemini/antigravity`, `~/.config/opencode`, and so on), honoring `XDG_CONFIG_HOME` on Linux and `%APPDATA%` on Windows.
*   **Running inside an agent**: When `skl` is launched by an agent (Claude Code, Codex, Cursor, Gemini CLI, Antigravity, and others set an environment variable), it drops the interactive prompts and the banner, and targets that agent by default.

### Scope selection
Without `-g` or `-p`, `add` asks whether to install into the project or globally. With `-y` it picks the project, unless you pass `-g`. `update` and `check` with `-y` pick the project when the current directory holds one, and otherwise fall back to the global scope.

# skl: Agent Skills CLI Manager

`skl` is a fast CLI tool for managing Agent Skills, written in Go.

I ported a TypeScript CLI to Go to learn the language. This repository contains the results of that experiment: a side-by-side comparison of runtime speed, binary size, dependencies, and code structure.

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

## Architecture and security

### Path sanitization
Every input skill name is sanitized using `SanitizeName()`. This replaces special characters with hyphens, lowercases the string, and truncates to 255 characters to prevent directory traversal attempts (CWE-22).

### Canonical protection
When a skill is installed, the canonical files are stored in `.agents/skills/<skill-name>`. If you remove a skill from a specific agent's scope, the CLI checks if other agents still reference it. If another active agent is using the skill, the CLI leaves the canonical files in place.

# 🚀 skl: Agent Skills CLI Manager

`skl` is a high-performance, robust CLI manager for Agent Skills, written natively in Go. 

This project started as a hands-on learning experiment to port a real-world TypeScript application—the `skills` CLI manager—entirely to Go. It documents the journey of a front-end engineer learning systems programming, exploring Go's runtime characteristics, and adopting new architectural paradigms.

## The "Why": Moving from Node.js to Go for CLI Tools

While TypeScript is unmatched for building complex client-side applications, Node.js-based CLI tools suffer from structural bloat, supply-chain vulnerabilities, and cold-start latency. 

### 1. Eliminating Transitive Dependency Risks
The original TypeScript CLI relies on **127 transitive dependencies** to perform basic filesystem operations and network requests. In Go, the same program is written using **zero third-party dependencies**, relying entirely on Go's robust standard library (with a single direct dependency on `yaml.v3` for parsing frontmatter).

### 2. Physical & Cognitive Simplification
A side-by-side complexity audit reveals a dramatic reduction in physical footprint:
*   **Original TypeScript**: 10,108 lines of code across 33 files + 116MB `node_modules` folder.
*   **Migrated Go (`skl`)**: 2,864 lines of code across 11 files + 9.2MB self-contained static binary.

By replacing V8 execution contexts, asynchronous task wrappers, and transpilation configurations with Go's flat, synchronous, compile-time checked code, the physical line count was reduced by over **71%**.

### 3. Sub-Millisecond Cold Starts
Initializing the V8 engine, resolving nested imports, and parsing JavaScript in a Node.js CLI produces a perceptible cold-start lag (80ms to 200ms). Go compiles directly to machine code, initializing in **under 1 millisecond**:
*   `$ time node skills-ts version` -> **100ms**
*   `$ time skl version` -> **2ms**

### 4. Explicit Error Handling
Adopting Go meant adopting the **10 Commandments of Go Error Handling** (treating errors as elegant, first-class values rather than throwing unexpected exceptions). By wrapping errors at package boundaries and ensuring errors never escape goroutines unheard, `skl` achieves bulletproof runtime stability.

## 🛠️ Installation & Usage

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
```bash
# Clone the repository
git clone https://github.com/alexastrum/skl.git
cd skl

# Build the binary
go build -o skl ./cmd/skl

# Run tests
go test -v ./...
```

## 💻 CLI Commands

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
Creates a structured `SKILL.md` template with valid YAML frontmatter:
```bash
skl init my-new-skill
```

### 2. Add/Install Skills (`add`)
Installs local or remote git/zip skill sets to your active agents:
```bash
skl add ./my-skill -v
skl add samber/cc-skills-golang -g --copy
```

### 3. List Installed Skills (`list`)
Scans all active agent configuration folders and formats them in an elegant summary:
```bash
skl list
```

### 4. Remove a Skill (`remove`)
Safely uninstalls and deletes targeted skills:
```bash
skl remove my-skill
```

### 5. Find an Installed Skill (`find`)
Retrieves and displays detailed information about an installed skill:
```bash
skl find golang-testing
```

## 🛡️ Architecture & Security

### Path Sanitization
Any input skill name passes through `SanitizeName()` which enforces lowercasing, strips special characters (replacing with a single hyphen), and truncates to 255 characters to prevent directory traversal attempts (`CWE-22`).

### Canonical Protection
When a skill is installed, the canonical files are stored in `.agents/skills/<skill-name>`. If you remove the skill from a specific agent scope, `skl` checks if other agents still reference it. If another active agent still uses the skill, `skl` protects and preserves the canonical repository files intact.

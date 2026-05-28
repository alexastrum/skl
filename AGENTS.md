# 🤖 Agent Configurations & Discovery in Go Skills

`go-skills` provides first-class support for **40+ Agentic AI environments**, including major IDE plugins, standalone clients, CLI agents, and development assistants. 

Each agent defines its own configuration directories, skills folder mappings, and active detection algorithms. At runtime, `go-skills` dynamically evaluates your local filesystem and active environment variables to determine which agents are installed, allowing it to route, copy, and link your skills seamlessly.

---

## 📂 Mapped Agents & Environments

The following table summarizes a selection of key agents supported by `go-skills`:

| Agent Type Key | Display Name | Project Skills Folder | Global Skills Folder | Detection Strategy |
| :--- | :--- | :--- | :--- | :--- |
| **`antigravity`** | Antigravity | `.agents/skills` | `~/.gemini/antigravity/skills` | Primary universal agent (always included) |
| **`universal`** | Universal | `.agents/skills` | *Not supported* | Fallback project-level routing (always active) |
| **`claude-code`** | Claude Code | `.claude/skills` | `~/.claude/skills` | Detects directory `~/.claude` or `CLAUDE_CONFIG_DIR` |
| **`cursor`** | Cursor | `.cursor/skills` | `~/.config/Cursor/User/skills` | Detects `.config/Cursor` or system application config directories |
| **`cline`** | Cline | `.cline/skills` | `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/skills` | Detects Code global storage folders |
| **`aider-desk`** | AiderDesk | `.aider-desk/skills` | `~/.aider-desk/skills` | Detects `~/.aider-desk` folder |
| **`zed`** | Zed | `.zed/skills` | `~/.config/zed/skills` | Detects `.config/zed` or Flatpak environment folders |
| **`copilot`** | Copilot | `.copilot/skills` | `~/.config/github-copilot/skills` | Detects `.config/github-copilot` folder |
| **`windsurf`** | Windsurf | `.windsurf/skills` | `~/.config/Windsurf/User/skills` | Detects `.config/Windsurf` folder |

---

## ⚡ Active Discovery & Detection Mechanics

In `go-skills`, discovery is handled via the `DetectInstalled` callback inside `AgentConfig`. 
At runtime, when no specific agent is targeted, `go-skills` executes the following algorithm:

```go
func detectInstalledAgents(home, configHome, cwd string) []AgentType {
	var installed []AgentType
	for agentType, config := range Agents {
		if config.DetectInstalled != nil && config.DetectInstalled(home, configHome, cwd) {
			installed = append(installed, agentType)
		}
	}
	return installed
}
```

### 1. Active Project-Level Detection
Checks if the current project workspace directory (`cwd`) contains active agent folders (e.g. `.cursor/`, `.cline/`, `.zed/`, `.aider.conf.yml`, etc.). If so, that agent is immediately considered active for the project, and any newly added skills are linked into their respective local directories.

### 2. Environment Variables & Global Detection
Leverages system environment variables to locate configuration files correctly on macOS, Linux, and Windows:
- `XDG_CONFIG_HOME` (falls back to `~/.config` on UNIX)
- `APPDATA` & `LOCALAPPDATA` (for Windows registry paths)
- `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `VIBE_HOME` for specialized developer setups.

---

## 🔗 How Symlink Mappings are Routed

When you add a skill, `go-skills` automatically resolves the correct link targets:

```mermaid
graph TD
    A[Add Skill command] --> B{Resolve Target Agents}
    B -->|User specifies -a cline| C[Target Cline]
    B -->|Automatic discovery| D[Detect Installed Agents]
    D --> E[Found: claude-code & zed]
    
    C --> F[Copy to Canonical base directory]
    E --> F
    
    F --> G[Create relative symlink from Agent dir to Canonical files]
    G --> H[.claude/skills/my-skill -> ../../.agents/skills/my-skill]
    G --> I[.zed/skills/my-skill -> ../../.agents/skills/my-skill]
```

This elegant design guarantees that files are never duplicated, updates apply universally to all agents, and resources are kept light and secure!

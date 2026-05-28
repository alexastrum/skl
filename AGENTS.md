# 🤖 Agent Configurations, Discovery, & Lock Specs in skl

> [!NOTE]
> This file is a machine-readable technical spec designed for consumption by AI Agents, LLM engines, and autonomous coders. It contains precise details of the codebase architecture, lock schema formats, and platform integration logic.

`skl` provides first-class support for **40+ Agent environments**, including major IDE plugins, standalone clients, CLI agents, and development assistants. 

Each agent defines its own configuration directories, skills folder mappings, and active detection algorithms. At runtime, `skl` dynamically evaluates your local filesystem and active environment variables to determine which agents are installed, allowing it to route, copy, and link your skills seamlessly.

---

## 📂 Mapped Agents & Environments

The following table summarizes a selection of key agents supported by `skl`:

| Agent Type Key | Display Name | Project Skills Folder | Global Skills Folder | Detection Strategy |
| :--- | :--- | :--- | :--- | :--- |
| **`antigravity`** | Antigravity | `.agents/skills` | `~/.gemini/antigravity/skills` | Primary universal agent (always included) |
| **`universal`** | Universal | `.agents/skills` | *Not supported* | Fallback project-level routing (always active) |
| **`claude-code`** | Claude Code | `.claude/skills` | `~/.claude/skills` | Detects directory `~/.claude` or `CLAUDE_CONFIG_DIR` |
| **`cursor`** | Cursor | `.cursor/skills` | `~/.config/Cursor/User/skills` | Detects `.config/Cursor` or system application config directories |
| **`cline`** | Cline | `.cline/skills` | `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/skills` | Detects Code global storage folders |
| **`aider-desk`** | AiderDesk | `.aider-desk/skills` | `~/.aider-desk/skills` | Detects `~/.aider-desk` folder |
| **`agy`** | Agy | `.agy/skills` | `~/.config/agy/skills` | Detects `.config/agy` or system application config directories |
| **`copilot`** | Copilot | `.copilot/skills` | `~/.config/github-copilot/skills` | Detects `.config/github-copilot` folder |
| **`windsurf`** | Windsurf | `.windsurf/skills` | `~/.config/Windsurf/User/skills` | Detects `.config/Windsurf` folder |

---

## ⚡ Active Discovery & Detection Mechanics

In `skl`, discovery is handled via the `DetectInstalled` callback inside `AgentConfig`. 
At runtime, when no specific agent is targeted, `skl` executes the following algorithm:

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
Checks if the current project workspace directory (`cwd`) contains active agent folders (e.g. `.cursor/`, `.cline/`, `.agy/`, `.aider.conf.yml`, etc.). If so, that agent is immediately considered active for the project, and any newly added skills are linked into their respective local directories.

### 2. Environment Variables & Global Detection
Leverages system environment variables to locate configuration files correctly on macOS, Linux, and Windows:
- `XDG_CONFIG_HOME` (falls back to `~/.config` on UNIX)
- `APPDATA` & `LOCALAPPDATA` (for Windows registry paths)
- `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `VIBE_HOME` for specialized setups.

---

## 🔗 How Symlink Mappings are Routed

When you add a skill, `skl` automatically resolves the correct link targets:

```mermaid
graph TD
    A[Add Skill command] --> B{Resolve Target Agents}
    B -->|User specifies -a cline| C[Target Cline]
    B -->|Automatic discovery| D[Detect Installed Agents]
    D --> E[Found: claude-code & agy]
    
    C --> F[Copy to Canonical base directory]
    E --> F
    
    F --> G[Create relative symlink from Agent dir to Canonical files]
    G --> H[.claude/skills/my-skill -> ../../.agents/skills/my-skill]
    G --> I[.agy/skills/my-skill -> ../../.agents/skills/my-skill]
```

This elegant design guarantees that files are never duplicated, updates apply universally to all agents, and resources are kept light and secure!

---

## 🗄️ Lock File Formats & Schema Specifications

To manage reference tracking, update detection, and installation states, `skl` implements two JSON lock files.

### 1. Global Lock File (`.skill-lock.json`)
Located at `~/.agents/.skill-lock.json` (or `$XDG_STATE_HOME/skills/.skill-lock.json`), this manages globally installed skills and tracks dismissing configurations.

**Go Definition (`pkg/skills/lock.go`):**
```go
type SkillLockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceUrl       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
	PluginName      string `json:"pluginName,omitempty"`
}

type DismissedPrompts struct {
	FindSkillsPrompt bool `json:"findSkillsPrompt,omitempty"`
}

type SkillLockFile struct {
	Version            int                       `json:"version"`
	Skills             map[string]SkillLockEntry `json:"skills"`
	Dismissed          *DismissedPrompts         `json:"dismissed,omitempty"`
	LastSelectedAgents []string                  `json:"lastSelectedAgents,omitempty"`
}
```

### 2. Local Project Lock File (`skills-lock.json`)
Located in the project repository root. It registers the skills added locally to ensure other developers can synchronize the workspace environment deterministically.

**Go Definition (`pkg/skills/lock.go`):**
```go
type LocalSkillLockEntry struct {
	Source       string `json:"source"`
	Ref          string `json:"ref,omitempty"`
	SourceType   string `json:"sourceType"`
	SkillPath    string `json:"skillPath,omitempty"`
	ComputedHash string `json:"computedHash"`
}

type LocalSkillLockFile struct {
	Version int                            `json:"version"`
	Skills  map[string]LocalSkillLockEntry `json:"skills"`
}
```

---

## 🔄 Reference Counting & Dynamic Deduplication

When a skill is uninstalled (`skl remove <skill-name>`), `skl` avoids leaving dangling files or aggressively deleting files still in use.

The removal logic executes the following checklist:
1. Loops through all configured agents (e.g. `claude-code`, `antigravity`, `agy`, etc.).
2. Checks whether a symlink exists under the agent's target skills directory for that skill name.
3. If **zero** active symlinks remain across the system/project scopes, it safely purges the canonical base files (`.agents/skills/<name>`).
4. Updates both local and global lock files by purging the respective `Skills` map entries.

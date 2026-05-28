# 🤖 Agent Configurations & Discovery

`skl` supports 40+ agent environments. It evaluates the local filesystem and environment variables to detect installed agents, routing and linking skills dynamically.

We enforce strict feature parity (commands, flags, platforms) with the original TypeScript `skills` npm package. Antigravity maps to `~/.gemini/config` (original IDE) and `~/.gemini/antigravity` (2.0).

IMPORTANT: ONLY WHEN EDITING README.md or human docs (eg --help) run /humanizer skill.

## ⚡ Discovery Mechanics

`skl` detects agents via project folders (e.g., `.cursor`, `.cline`, `.zed`) or environment variables (`XDG_CONFIG_HOME`, `APPDATA`, etc.):

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

## 🔗 Symlink Routing

Skills are mapped using relative symlinks to prevent duplication:

```mermaid
graph TD
    A[Add Skill command] --> B{Installation Scope?}
    
    B -->|Global: -g / --global| C1[Canonical Base: ~/.gemini/antigravity/skills]
    B -->|Local: Project Default| C2[Canonical Base: .agents/skills]
    
    C1 --> D1[Copy files to Canonical Global Dir]
    C2 --> D2[Copy files to Canonical Project Dir]
    
    D1 --> E1{Universal Agent?}
    D2 --> E2{Universal Agent?}
    
    E1 -->|Yes: antigravity| F1[No symlink needed]
    E1 -->|No: claude-code| F2[Create relative symlink in home dir]
    F2 --> G1[~/.claude/skills/my-skill -> ../.gemini/antigravity/skills/my-skill]
    
    E2 -->|Yes: antigravity| H1[No symlink needed]
    E2 -->|No: claude-code| H2[Create relative symlink in project dir]
    H2 --> I1[.claude/skills/my-skill -> ../../.agents/skills/my-skill]
```

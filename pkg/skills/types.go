package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nonAlphanumericRegex     = regexp.MustCompile(`[^a-z0-9._]+`)
	leadingTrailingDotHyphen = regexp.MustCompile(`^[.\-]+|[.\-]+$`)
)

// SanitizeName sanitizes a filename/directory name to prevent path traversal attacks
// and ensures it follows kebab-case convention.
func SanitizeName(name string) string {
	lower := strings.ToLower(name)
	// Replace sequence of characters that are NOT a-z, 0-9, . or _ with a single hyphen
	sanitized := nonAlphanumericRegex.ReplaceAllString(lower, "-")
	// Strip leading/trailing dots and hyphens
	sanitized = leadingTrailingDotHyphen.ReplaceAllString(sanitized, "")

	if len(sanitized) > 255 {
		sanitized = sanitized[:255]
	}

	if sanitized == "" {
		return "unnamed-skill"
	}
	return sanitized
}

type AgentType string

const (
	AgentAntigravity AgentType = "antigravity"
	AgentUniversal   AgentType = "universal"
)

// AgentConfig defines the configuration for a supported agent.
type AgentConfig struct {
	Name                string
	DisplayName         string
	SkillsDir           string
	GlobalSkillsDir     string
	ShowInUniversalList bool
	DetectInstalled     func(home, configHome, cwd string) bool
}

// Global variable mapped dynamically to match UserHomeDir and environment variables at runtime.
var Agents = make(map[AgentType]AgentConfig)

func init() {
	home, _ := os.UserHomeDir()

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}

	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}

	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}

	vibeHome := os.Getenv("VIBE_HOME")
	if vibeHome == "" {
		vibeHome = filepath.Join(home, ".vibe")
	}

	zedAppDataHome := os.Getenv("APPDATA")
	zedFlatpakConfigHome := os.Getenv("FLATPAK_XDG_CONFIG_HOME")

	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	getOpenClawGlobalSkillsDir := func() string {
		if exists(filepath.Join(home, ".openclaw")) {
			return filepath.Join(home, ".openclaw/skills")
		}
		if exists(filepath.Join(home, ".clawdbot")) {
			return filepath.Join(home, ".clawdbot/skills")
		}
		if exists(filepath.Join(home, ".moltbot")) {
			return filepath.Join(home, ".moltbot/skills")
		}
		return filepath.Join(home, ".openclaw/skills")
	}

	// 40+ agent configurations matching inspiration src/agents.ts
	agentsMap := map[AgentType]AgentConfig{
		"aider-desk": {
			Name:                "aider-desk",
			DisplayName:         "AiderDesk",
			SkillsDir:           ".aider-desk/skills",
			GlobalSkillsDir:     filepath.Join(home, ".aider-desk/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".aider-desk"))
			},
		},
		"amp": {
			Name:                "amp",
			DisplayName:         "Amp",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(configHome, "agents/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(c, "amp"))
			},
		},
		"antigravity": {
			Name:                "antigravity",
			DisplayName:         "Antigravity",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".gemini/antigravity/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".gemini/antigravity"))
			},
		},
		"augment": {
			Name:                "augment",
			DisplayName:         "Augment",
			SkillsDir:           ".augment/skills",
			GlobalSkillsDir:     filepath.Join(home, ".augment/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".augment"))
			},
		},
		"bob": {
			Name:                "bob",
			DisplayName:         "IBM Bob",
			SkillsDir:           ".bob/skills",
			GlobalSkillsDir:     filepath.Join(home, ".bob/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".bob"))
			},
		},
		"claude-code": {
			Name:                "claude-code",
			DisplayName:         "Claude Code",
			SkillsDir:           ".claude/skills",
			GlobalSkillsDir:     filepath.Join(claudeHome, "skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(claudeHome)
			},
		},
		"openclaw": {
			Name:                "openclaw",
			DisplayName:         "OpenClaw",
			SkillsDir:           "skills",
			GlobalSkillsDir:     getOpenClawGlobalSkillsDir(),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".openclaw")) ||
					exists(filepath.Join(h, ".clawdbot")) ||
					exists(filepath.Join(h, ".moltbot"))
			},
		},
		"cline": {
			Name:                "cline",
			DisplayName:         "Cline",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".agents/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".cline"))
			},
		},
		"codearts-agent": {
			Name:                "codearts-agent",
			DisplayName:         "CodeArts Agent",
			SkillsDir:           ".codeartsdoer/skills",
			GlobalSkillsDir:     filepath.Join(home, ".codeartsdoer/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".codeartsdoer"))
			},
		},
		"codebuddy": {
			Name:                "codebuddy",
			DisplayName:         "CodeBuddy",
			SkillsDir:           ".codebuddy/skills",
			GlobalSkillsDir:     filepath.Join(home, ".codebuddy/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(w, ".codebuddy")) || exists(filepath.Join(h, ".codebuddy"))
			},
		},
		"codemaker": {
			Name:                "codemaker",
			DisplayName:         "Codemaker",
			SkillsDir:           ".codemaker/skills",
			GlobalSkillsDir:     filepath.Join(home, ".codemaker/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".codemaker"))
			},
		},
		"codestudio": {
			Name:                "codestudio",
			DisplayName:         "Code Studio",
			SkillsDir:           ".codestudio/skills",
			GlobalSkillsDir:     filepath.Join(home, ".codestudio/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".codestudio"))
			},
		},
		"codex": {
			Name:                "codex",
			DisplayName:         "Codex",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(codexHome, "skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(codexHome) || exists("/etc/codex")
			},
		},
		"command-code": {
			Name:                "command-code",
			DisplayName:         "Command Code",
			SkillsDir:           ".commandcode/skills",
			GlobalSkillsDir:     filepath.Join(home, ".commandcode/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".commandcode"))
			},
		},
		"continue": {
			Name:                "continue",
			DisplayName:         "Continue",
			SkillsDir:           ".continue/skills",
			GlobalSkillsDir:     filepath.Join(home, ".continue/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(w, ".continue")) || exists(filepath.Join(h, ".continue"))
			},
		},
		"cortex": {
			Name:                "cortex",
			DisplayName:         "Cortex Code",
			SkillsDir:           ".cortex/skills",
			GlobalSkillsDir:     filepath.Join(home, ".snowflake/cortex/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".snowflake/cortex"))
			},
		},
		"crush": {
			Name:                "crush",
			DisplayName:         "Crush",
			SkillsDir:           ".crush/skills",
			GlobalSkillsDir:     filepath.Join(home, ".config/crush/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".config/crush"))
			},
		},
		"cursor": {
			Name:                "cursor",
			DisplayName:         "Cursor",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".cursor/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".cursor"))
			},
		},
		"deepagents": {
			Name:                "deepagents",
			DisplayName:         "Deep Agents",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".deepagents/agent/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".deepagents"))
			},
		},
		"devin": {
			Name:                "devin",
			DisplayName:         "Devin for Terminal",
			SkillsDir:           ".devin/skills",
			GlobalSkillsDir:     filepath.Join(configHome, "devin/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(c, "devin"))
			},
		},
		"dexto": {
			Name:                "dexto",
			DisplayName:         "Dexto",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".agents/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".dexto"))
			},
		},
		"droid": {
			Name:                "droid",
			DisplayName:         "Droid",
			SkillsDir:           ".factory/skills",
			GlobalSkillsDir:     filepath.Join(home, ".factory/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".factory"))
			},
		},
		"firebender": {
			Name:                "firebender",
			DisplayName:         "Firebender",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".firebender/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".firebender"))
			},
		},
		"forgecode": {
			Name:                "forgecode",
			DisplayName:         "ForgeCode",
			SkillsDir:           ".forge/skills",
			GlobalSkillsDir:     filepath.Join(home, ".forge/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".forge"))
			},
		},
		"gemini-cli": {
			Name:                "gemini-cli",
			DisplayName:         "Gemini CLI",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".gemini/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".gemini"))
			},
		},
		"github-copilot": {
			Name:                "github-copilot",
			DisplayName:         "GitHub Copilot",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".copilot/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".copilot"))
			},
		},
		"goose": {
			Name:                "goose",
			DisplayName:         "Goose",
			SkillsDir:           ".goose/skills",
			GlobalSkillsDir:     filepath.Join(configHome, "goose/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(c, "goose"))
			},
		},
		"hermes-agent": {
			Name:                "hermes-agent",
			DisplayName:         "Hermes Agent",
			SkillsDir:           ".hermes/skills",
			GlobalSkillsDir:     filepath.Join(home, ".hermes/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".hermes"))
			},
		},
		"junie": {
			Name:                "junie",
			DisplayName:         "Junie",
			SkillsDir:           ".junie/skills",
			GlobalSkillsDir:     filepath.Join(home, ".junie/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".junie"))
			},
		},
		"iflow-cli": {
			Name:                "iflow-cli",
			DisplayName:         "iFlow CLI",
			SkillsDir:           ".iflow/skills",
			GlobalSkillsDir:     filepath.Join(home, ".iflow/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".iflow"))
			},
		},
		"kilo": {
			Name:                "kilo",
			DisplayName:         "Kilo Code",
			SkillsDir:           ".kilocode/skills",
			GlobalSkillsDir:     filepath.Join(home, ".kilocode/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".kilocode"))
			},
		},
		"kimi-cli": {
			Name:                "kimi-cli",
			DisplayName:         "Kimi Code CLI",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".config/agents/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".kimi"))
			},
		},
		"kiro-cli": {
			Name:                "kiro-cli",
			DisplayName:         "Kiro CLI",
			SkillsDir:           ".kiro/skills",
			GlobalSkillsDir:     filepath.Join(home, ".kiro/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".kiro"))
			},
		},
		"kode": {
			Name:                "kode",
			DisplayName:         "Kode",
			SkillsDir:           ".kode/skills",
			GlobalSkillsDir:     filepath.Join(home, ".kode/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".kode"))
			},
		},
		"mcpjam": {
			Name:                "mcpjam",
			DisplayName:         "MCPJam",
			SkillsDir:           ".mcpjam/skills",
			GlobalSkillsDir:     filepath.Join(home, ".mcpjam/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".mcpjam"))
			},
		},
		"mistral-vibe": {
			Name:                "mistral-vibe",
			DisplayName:         "Mistral Vibe",
			SkillsDir:           ".vibe/skills",
			GlobalSkillsDir:     filepath.Join(vibeHome, "skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(vibeHome)
			},
		},
		"mux": {
			Name:                "mux",
			DisplayName:         "Mux",
			SkillsDir:           ".mux/skills",
			GlobalSkillsDir:     filepath.Join(home, ".mux/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".mux"))
			},
		},
		"opencode": {
			Name:                "opencode",
			DisplayName:         "OpenCode",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(configHome, "opencode/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(c, "opencode"))
			},
		},
		"openhands": {
			Name:                "openhands",
			DisplayName:         "OpenHands",
			SkillsDir:           ".openhands/skills",
			GlobalSkillsDir:     filepath.Join(home, ".openhands/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".openhands"))
			},
		},
		"pi": {
			Name:                "pi",
			DisplayName:         "Pi",
			SkillsDir:           ".pi/skills",
			GlobalSkillsDir:     filepath.Join(home, ".pi/agent/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".pi/agent"))
			},
		},
		"qoder": {
			Name:                "qoder",
			DisplayName:         "Qoder",
			SkillsDir:           ".qoder/skills",
			GlobalSkillsDir:     filepath.Join(home, ".qoder/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".qoder"))
			},
		},
		"qwen-code": {
			Name:                "qwen-code",
			DisplayName:         "Qwen Code",
			SkillsDir:           ".qwen/skills",
			GlobalSkillsDir:     filepath.Join(home, ".qwen/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".qwen"))
			},
		},
		"replit": {
			Name:                "replit",
			DisplayName:         "Replit",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(configHome, "agents/skills"),
			ShowInUniversalList: false,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(w, ".replit"))
			},
		},
		"rovodev": {
			Name:                "rovodev",
			DisplayName:         "Rovo Dev",
			SkillsDir:           ".rovodev/skills",
			GlobalSkillsDir:     filepath.Join(home, ".rovodev/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".rovodev"))
			},
		},
		"roo": {
			Name:                "roo",
			DisplayName:         "Roo Code",
			SkillsDir:           ".roo/skills",
			GlobalSkillsDir:     filepath.Join(home, ".roo/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".roo"))
			},
		},
		"tabnine-cli": {
			Name:                "tabnine-cli",
			DisplayName:         "Tabnine CLI",
			SkillsDir:           ".tabnine/agent/skills",
			GlobalSkillsDir:     filepath.Join(home, ".tabnine/agent/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".tabnine"))
			},
		},
		"trae": {
			Name:                "trae",
			DisplayName:         "Trae",
			SkillsDir:           ".trae/skills",
			GlobalSkillsDir:     filepath.Join(home, ".trae/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".trae"))
			},
		},
		"trae-cn": {
			Name:                "trae-cn",
			DisplayName:         "Trae CN",
			SkillsDir:           ".trae/skills",
			GlobalSkillsDir:     filepath.Join(home, ".trae-cn/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".trae-cn"))
			},
		},
		"warp": {
			Name:                "warp",
			DisplayName:         "Warp",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".agents/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".warp"))
			},
		},
		"windsurf": {
			Name:                "windsurf",
			DisplayName:         "Windsurf",
			SkillsDir:           ".windsurf/skills",
			GlobalSkillsDir:     filepath.Join(home, ".codeium/windsurf/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".codeium/windsurf"))
			},
		},
		"zed": {
			Name:                "zed",
			DisplayName:         "Zed",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(home, ".agents/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(c, "zed")) ||
					(zedAppDataHome != "" && exists(filepath.Join(zedAppDataHome, "Zed"))) ||
					(zedFlatpakConfigHome != "" && exists(filepath.Join(zedFlatpakConfigHome, "zed")))
			},
		},
		"zencoder": {
			Name:                "zencoder",
			DisplayName:         "Zencoder",
			SkillsDir:           ".zencoder/skills",
			GlobalSkillsDir:     filepath.Join(home, ".zencoder/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".zencoder"))
			},
		},
		"neovate": {
			Name:                "neovate",
			DisplayName:         "Neovate",
			SkillsDir:           ".neovate/skills",
			GlobalSkillsDir:     filepath.Join(home, ".neovate/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".neovate"))
			},
		},
		"pochi": {
			Name:                "pochi",
			DisplayName:         "Pochi",
			SkillsDir:           ".pochi/skills",
			GlobalSkillsDir:     filepath.Join(home, ".pochi/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".pochi"))
			},
		},
		"adal": {
			Name:                "adal",
			DisplayName:         "AdaL",
			SkillsDir:           ".adal/skills",
			GlobalSkillsDir:     filepath.Join(home, ".adal/skills"),
			ShowInUniversalList: true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(h, ".adal"))
			},
		},
		"universal": {
			Name:                "universal",
			DisplayName:         "Universal",
			SkillsDir:           ".agents/skills",
			GlobalSkillsDir:     filepath.Join(configHome, "agents/skills"),
			ShowInUniversalList: false,
			DetectInstalled:     func(h, c, w string) bool { return false },
		},
	}

	for k, v := range agentsMap {
		Agents[k] = v
	}
}

// Skill represents a skill definition parsed from a SKILL.md file.
type Skill struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Path        string                 `json:"path"`
	RawContent  string                 `json:"rawContent,omitempty"`
	PluginName  string                 `json:"pluginName,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// InstalledSkill represents a skill that has been installed in the agent directories.
type InstalledSkill struct {
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Path          string      `json:"path"`
	CanonicalPath string      `json:"canonicalPath"`
	Scope         string      `json:"scope"` // "project" or "global"
	Agents        []AgentType `json:"agents"`
}

// InitOptions contains options for the init command.
type InitOptions struct {
	Name string
	Cwd  string
}

// ListOptions contains options for listing installed skills.
type ListOptions struct {
	Global      bool
	Cwd         string
	AgentFilter []AgentType
}

// AddOptions contains options for the add command.
type AddOptions struct {
	Global  bool
	Copy    bool
	Agent   []AgentType
	Verbose bool
	Yes     bool
}

// DetectInstalledAgents checks environment and folders to find installed agents.
func DetectInstalledAgents(cwd string) []AgentType {
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	var results []AgentType
	for k, v := range Agents {
		if k == "universal" {
			continue
		}
		if v.DetectInstalled(home, configHome, cwd) {
			results = append(results, k)
		}
	}
	return results
}

// GetUniversalAgents returns agents using .agents/skills and are shown in list.
func GetUniversalAgents() []AgentType {
	var results []AgentType
	for k, v := range Agents {
		if v.SkillsDir == ".agents/skills" && v.ShowInUniversalList {
			results = append(results, k)
		}
	}
	return results
}

// GetNonUniversalAgents returns agents using custom directory mapping.
func GetNonUniversalAgents() []AgentType {
	var results []AgentType
	for k, v := range Agents {
		if v.SkillsDir != ".agents/skills" {
			results = append(results, k)
		}
	}
	return results
}

// IsUniversalAgent checks if the agent uses .agents/skills.
func IsUniversalAgent(t AgentType) bool {
	if cfg, ok := Agents[t]; ok {
		return cfg.SkillsDir == ".agents/skills"
	}
	return false
}

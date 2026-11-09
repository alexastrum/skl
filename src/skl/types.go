package skl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	nonAlphanumericRegex     = regexp.MustCompile(`[^a-z0-9._]+`)
	leadingTrailingDotHyphen = regexp.MustCompile(`^[.\-]+|[.\-]+$`)

	// Terminal escape & control sequence regexes matching upstream src/sanitize.ts (CWE-150).
	oscRegex       = regexp.MustCompile("\x1b\\][\\s\\S]*?(?:\x07|\x1b\\\\)")
	dcsPmApcRegex  = regexp.MustCompile("\x1b[P^_][\\s\\S]*?(?:\x1b\\\\)")
	csiRegex       = regexp.MustCompile("\x1b\\[[\\x30-\\x3f]*[\\x20-\\x2f]*[\\x40-\\x7e]")
	simpleEscRegex = regexp.MustCompile("\x1b[\\x20-\\x7e]")
	crlfCollapseRe = regexp.MustCompile(`[\r\n]+`)
)

// SanitizeName sanitizes a filename/directory name to prevent path traversal attacks
// and ensures it follows kebab-case convention, matching upstream sanitizeName byte-for-byte.
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

// StripTerminalEscapes strips all terminal escape sequences and dangerous control characters
// from a string (keeping \t and \n), defending against CWE-150 terminal escape injection.
func StripTerminalEscapes(str string) string {
	s := oscRegex.ReplaceAllString(str, "")
	s = dcsPmApcRegex.ReplaceAllString(s, "")
	s = csiRegex.ReplaceAllString(s, "")
	s = simpleEscRegex.ReplaceAllString(s, "")

	// Strip C1 control codes (U+0080..U+009F and standalone raw bytes 0x80..0x9F)
	// and C0/DEL control chars except \t (0x09) and \n (0x0A), while preserving valid UTF-8 runes.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			raw := s[i]
			i++
			if raw >= 0x80 && raw <= 0x9f {
				continue
			}
			b.WriteByte(raw)
			continue
		}
		i += size
		if r >= 0x80 && r <= 0x9f {
			continue
		}
		if (r >= 0x00 && r <= 0x08) || r == 0x0b || r == 0x0c || (r >= 0x0d && r <= 0x1f) || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SanitizeMetadata sanitizes a skill metadata string (name, description, etc.) for safe
// single-line terminal display by stripping terminal escapes, collapsing newlines into spaces,
// and trimming surrounding whitespace.
func SanitizeMetadata(str string) string {
	cleaned := StripTerminalEscapes(str)
	cleaned = crlfCollapseRe.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

// InstallMode specifies how skills are materialized in agent directories.
type InstallMode string

const (
	InstallModeSymlink InstallMode = "symlink"
	InstallModeCopy    InstallMode = "copy"
	InstallModeLink    InstallMode = "link"
)

type AgentType string

const (
	AgentAntigravity    AgentType = "antigravity"
	AgentAntigravityCLI AgentType = "antigravity-cli"
	AgentUniversal      AgentType = "universal"
)

// AllAgentTypes lists all supported upstream agent identifiers in canonical order.
var AllAgentTypes = []AgentType{
	"aider-desk",
	"amp",
	"antigravity",
	"antigravity-cli",
	"astrbot",
	"autohand-code",
	"augment",
	"bob",
	"claude-code",
	"openclaw",
	"cline",
	"codearts-agent",
	"codebuddy",
	"codemaker",
	"codestudio",
	"codex",
	"command-code",
	"continue",
	"cortex",
	"crush",
	"cursor",
	"deepagents",
	"devin",
	"dexto",
	"droid",
	"eve",
	"firebender",
	"forgecode",
	"fx",
	"gemini-cli",
	"github-copilot",
	"goose",
	"grok",
	"hermes-agent",
	"inference-sh",
	"jazz",
	"junie",
	"iflow-cli",
	"kilo",
	"kimchi",
	"kimi-code-cli",
	"kiro-cli",
	"kode",
	"lingma",
	"loaf",
	"mcpjam",
	"minimax-code",
	"mistral-vibe",
	"moxby",
	"mux",
	"opencode",
	"openhands",
	"ona",
	"pi",
	"posit-assistant",
	"qoder",
	"qoder-cn",
	"qwen-code",
	"replit",
	"reasonix",
	"rovodev",
	"roo",
	"sarvam-code",
	"tabnine-cli",
	"terramind",
	"tinycloud",
	"trae",
	"trae-cn",
	"warp",
	"windsurf",
	"zed",
	"zcode",
	"zencoder",
	"zenflow",
	"neovate",
	"pochi",
	"promptscript",
	"adal",
	"universal",
}

// LegacyAgentProjectSkillDirs holds project skill dirs from before agents moved to .agents/skills.
var LegacyAgentProjectSkillDirs = []string{
	".cline/skills",
	".codex/skills",
	".factory/skills",
	".github/skills",
	".kilo/skills",
	".kilocode/skills",
	".opencode/skills",
	".pi/skills",
}

// EveSubagentsDir is the relative directory under an Eve project root holding subagents.
var EveSubagentsDir = filepath.Join("agent", "subagents")

// AgentConfig defines the configuration for a supported agent.
type AgentConfig struct {
	Name                            string
	DisplayName                     string
	SkillsDir                       string
	GlobalSkillsDir                 string
	ShowInUniversalList             bool
	ShowInUniversalPrompt           bool
	GlobalReadsCanonical            bool
	CreateProjectSkillsDirByDefault bool
	DetectInstalled                 func(home, configHome, cwd string) bool
}

// AgentEnv provides environment, home directory, platform, and filesystem lookup overrides
// so agent paths and detection can be tested deterministically without relying on init-time globals.
type AgentEnv struct {
	Home       string
	ConfigHome string
	GOOS       string
	Getenv     func(string) string
	PathExists func(string) bool
}

func defaultPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DefaultAgentEnv returns an AgentEnv backed by the current OS environment and user home directory.
func DefaultAgentEnv() AgentEnv {
	home, _ := os.UserHomeDir()
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	return AgentEnv{
		Home:       home,
		ConfigHome: configHome,
		GOOS:       runtime.GOOS,
		Getenv:     os.Getenv,
		PathExists: defaultPathExists,
	}
}

func envOrDefault(getenv func(string) string, key, fallback string) string {
	if getenv != nil {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
	}
	return fallback
}

// ResolveReasonixHome resolves Reasonix's home directory per upstream #2433:
// REASONIX_HOME if set, else %APPDATA%\reasonix (or ~/AppData/Roaming/reasonix) on Windows,
// else ~/.reasonix on Linux/macOS.
func ResolveReasonixHome(home, goos string, getenv func(string) string) string {
	if getenv != nil {
		if custom := strings.TrimSpace(getenv("REASONIX_HOME")); custom != "" {
			return custom
		}
	}
	if goos == "windows" || goos == "win32" {
		var appData string
		if getenv != nil {
			appData = strings.TrimSpace(getenv("APPDATA"))
		}
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "reasonix")
	}
	return filepath.Join(home, ".reasonix")
}

// GetOpenClawGlobalSkillsDir resolves OpenClaw's global skills directory across current and legacy paths.
func GetOpenClawGlobalSkillsDir(homeDir string, pathExists func(string) bool) string {
	if pathExists == nil {
		pathExists = defaultPathExists
	}
	if pathExists(filepath.Join(homeDir, ".openclaw")) {
		return filepath.Join(homeDir, ".openclaw", "skills")
	}
	if pathExists(filepath.Join(homeDir, ".clawdbot")) {
		return filepath.Join(homeDir, ".clawdbot", "skills")
	}
	if pathExists(filepath.Join(homeDir, ".moltbot")) {
		return filepath.Join(homeDir, ".moltbot", "skills")
	}
	return filepath.Join(homeDir, ".openclaw", "skills")
}

// IsZCodeInstalled checks if ZCode is installed in home or /Applications.
func IsZCodeInstalled(homeDir string, pathExists func(string) bool) bool {
	if pathExists == nil {
		pathExists = defaultPathExists
	}
	return pathExists(filepath.Join(homeDir, ".zcode")) || pathExists("/Applications/ZCode.app")
}

// IsKimchiInstalled checks if Kimchi is installed under ~/.config/kimchi.
func IsKimchiInstalled(homeDir string, pathExists func(string) bool) bool {
	if pathExists == nil {
		pathExists = defaultPathExists
	}
	return pathExists(filepath.Join(homeDir, ".config", "kimchi"))
}

// IsMiniMaxCodeInstalled checks if MiniMax Code is installed in home or /Applications.
func IsMiniMaxCodeInstalled(homeDir string, pathExists func(string) bool) bool {
	if pathExists == nil {
		pathExists = defaultPathExists
	}
	return pathExists(filepath.Join(homeDir, ".minimax")) || pathExists("/Applications/MiniMax Code.app")
}

// IsPositAssistantInstalled checks current (~/.posit/assistant) and legacy (~/.positai) config dirs.
func IsPositAssistantInstalled(homeDir string, pathExists func(string) bool) bool {
	if pathExists == nil {
		pathExists = defaultPathExists
	}
	return pathExists(filepath.Join(homeDir, ".posit", "assistant")) || pathExists(filepath.Join(homeDir, ".positai"))
}

func packageJSONHasDependency(packageJSONPath, dependencyName string) bool {
	raw, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]interface{} `json:"dependencies"`
		DevDependencies map[string]interface{} `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return false
	}
	if pkg.Dependencies != nil {
		if v, ok := pkg.Dependencies[dependencyName]; ok && v != nil && v != "" && v != false {
			return true
		}
	}
	if pkg.DevDependencies != nil {
		if v, ok := pkg.DevDependencies[dependencyName]; ok && v != nil && v != "" && v != false {
			return true
		}
	}
	return false
}

// BuildAgents constructs the complete map of supported agent configurations for the given AgentEnv.
func BuildAgents(env AgentEnv) map[AgentType]AgentConfig {
	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	exists := env.PathExists
	if exists == nil {
		exists = defaultPathExists
	}
	goos := env.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	home := env.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	configHome := env.ConfigHome
	if configHome == "" {
		configHome = strings.TrimSpace(getenv("XDG_CONFIG_HOME"))
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
	}

	resolveH := func(h string) string {
		if h != "" {
			return h
		}
		if curHome, err := os.UserHomeDir(); err == nil && env.Home == "" {
			return curHome
		}
		return home
	}
	resolveC := func(h, c string) string {
		if c != "" {
			return c
		}
		if xdg := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); xdg != "" && env.ConfigHome == "" {
			return xdg
		}
		if env.ConfigHome != "" {
			return env.ConfigHome
		}
		return filepath.Join(resolveH(h), ".config")
	}
	resolveW := func(w string) string {
		if w != "" {
			return w
		}
		cwd, _ := os.Getwd()
		return cwd
	}

	codexHome := envOrDefault(getenv, "CODEX_HOME", filepath.Join(home, ".codex"))
	claudeHome := envOrDefault(getenv, "CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	vibeHome := envOrDefault(getenv, "VIBE_HOME", filepath.Join(home, ".vibe"))
	hermesHome := envOrDefault(getenv, "HERMES_HOME", filepath.Join(home, ".hermes"))
	autohandHome := envOrDefault(getenv, "AUTOHAND_HOME", filepath.Join(home, ".autohand"))
	grokHome := envOrDefault(getenv, "GROK_HOME", filepath.Join(home, ".grok"))
	reasonixHome := ResolveReasonixHome(home, goos, getenv)
	_ = codexHome

	return map[AgentType]AgentConfig{
		"aider-desk": {
			Name:                  "aider-desk",
			DisplayName:           "AiderDesk",
			SkillsDir:             ".aider-desk/skills",
			GlobalSkillsDir:       filepath.Join(home, ".aider-desk", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".aider-desk"))
			},
		},
		"amp": {
			Name:                  "amp",
			DisplayName:           "Amp",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(configHome, "agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveC(h, c), "amp"))
			},
		},
		"antigravity": {
			Name:                  "antigravity",
			DisplayName:           "Antigravity",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".gemini", "config", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  false,
			DetectInstalled: func(h, c, w string) bool {
				rh := resolveH(h)
				return exists(filepath.Join(rh, ".gemini", "antigravity")) ||
					exists(filepath.Join(rh, ".gemini", "config"))
			},
		},
		"antigravity-cli": {
			Name:                  "antigravity-cli",
			DisplayName:           "Antigravity CLI",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".gemini", "config", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  false,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".gemini", "antigravity-cli"))
			},
		},
		"astrbot": {
			Name:                  "astrbot",
			DisplayName:           "AstrBot",
			SkillsDir:             "data/skills",
			GlobalSkillsDir:       filepath.Join(home, ".astrbot", "data", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveW(w), "data", "skills")) ||
					exists(filepath.Join(resolveH(h), ".astrbot"))
			},
		},
		"autohand-code": {
			Name:                  "autohand-code",
			DisplayName:           "Autohand Code CLI",
			SkillsDir:             ".autohand/skills",
			GlobalSkillsDir:       filepath.Join(autohandHome, "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "AUTOHAND_HOME", filepath.Join(resolveH(h), ".autohand"))
				return exists(dir)
			},
		},
		"augment": {
			Name:                  "augment",
			DisplayName:           "Augment",
			SkillsDir:             ".augment/skills",
			GlobalSkillsDir:       filepath.Join(home, ".augment", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".augment"))
			},
		},
		"bob": {
			Name:                  "bob",
			DisplayName:           "IBM Bob",
			SkillsDir:             ".bob/skills",
			GlobalSkillsDir:       filepath.Join(home, ".bob", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".bob"))
			},
		},
		"claude-code": {
			Name:                            "claude-code",
			DisplayName:                     "Claude Code",
			SkillsDir:                       ".claude/skills",
			GlobalSkillsDir:                 filepath.Join(claudeHome, "skills"),
			ShowInUniversalList:             true,
			ShowInUniversalPrompt:           true,
			GlobalReadsCanonical:            true,
			CreateProjectSkillsDirByDefault: true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "CLAUDE_CONFIG_DIR", filepath.Join(resolveH(h), ".claude"))
				return exists(dir)
			},
		},
		"openclaw": {
			Name:                  "openclaw",
			DisplayName:           "OpenClaw",
			SkillsDir:             "skills",
			GlobalSkillsDir:       GetOpenClawGlobalSkillsDir(home, exists),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				rh := resolveH(h)
				return exists(filepath.Join(rh, ".openclaw")) ||
					exists(filepath.Join(rh, ".clawdbot")) ||
					exists(filepath.Join(rh, ".moltbot"))
			},
		},
		"cline": {
			Name:                  "cline",
			DisplayName:           "Cline",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".cline"))
			},
		},
		"codearts-agent": {
			Name:                  "codearts-agent",
			DisplayName:           "CodeArts Agent",
			SkillsDir:             ".codeartsdoer/skills",
			GlobalSkillsDir:       filepath.Join(home, ".codeartsdoer", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".codeartsdoer"))
			},
		},
		"codebuddy": {
			Name:                  "codebuddy",
			DisplayName:           "CodeBuddy",
			SkillsDir:             ".codebuddy/skills",
			GlobalSkillsDir:       filepath.Join(home, ".codebuddy", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveW(w), ".codebuddy")) ||
					exists(filepath.Join(resolveH(h), ".codebuddy"))
			},
		},
		"codemaker": {
			Name:                  "codemaker",
			DisplayName:           "Codemaker",
			SkillsDir:             ".codemaker/skills",
			GlobalSkillsDir:       filepath.Join(home, ".codemaker", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".codemaker"))
			},
		},
		"codestudio": {
			Name:                  "codestudio",
			DisplayName:           "Code Studio",
			SkillsDir:             ".codestudio/skills",
			GlobalSkillsDir:       filepath.Join(home, ".codestudio", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".codestudio"))
			},
		},
		"codex": {
			Name:                  "codex",
			DisplayName:           "Codex",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "CODEX_HOME", filepath.Join(resolveH(h), ".codex"))
				return exists(dir) || exists("/etc/codex")
			},
		},
		"command-code": {
			Name:                  "command-code",
			DisplayName:           "Command Code",
			SkillsDir:             ".commandcode/skills",
			GlobalSkillsDir:       filepath.Join(home, ".commandcode", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".commandcode"))
			},
		},
		"continue": {
			Name:                  "continue",
			DisplayName:           "Continue",
			SkillsDir:             ".continue/skills",
			GlobalSkillsDir:       filepath.Join(home, ".continue", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveW(w), ".continue")) ||
					exists(filepath.Join(resolveH(h), ".continue"))
			},
		},
		"cortex": {
			Name:                  "cortex",
			DisplayName:           "Cortex Code",
			SkillsDir:             ".cortex/skills",
			GlobalSkillsDir:       filepath.Join(home, ".snowflake", "cortex", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".snowflake", "cortex"))
			},
		},
		"crush": {
			Name:                  "crush",
			DisplayName:           "Crush",
			SkillsDir:             ".crush/skills",
			GlobalSkillsDir:       filepath.Join(home, ".config", "crush", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".config", "crush"))
			},
		},
		"cursor": {
			Name:                  "cursor",
			DisplayName:           "Cursor",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".cursor", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".cursor"))
			},
		},
		"deepagents": {
			Name:                  "deepagents",
			DisplayName:           "Deep Agents",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".deepagents", "agent", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".deepagents"))
			},
		},
		"devin": {
			Name:                  "devin",
			DisplayName:           "Devin for Terminal",
			SkillsDir:             ".devin/skills",
			GlobalSkillsDir:       filepath.Join(configHome, "devin", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveC(h, c), "devin"))
			},
		},
		"dexto": {
			Name:                  "dexto",
			DisplayName:           "Dexto",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".dexto"))
			},
		},
		"droid": {
			Name:                  "droid",
			DisplayName:           "Droid",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".factory", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".factory"))
			},
		},
		"eve": {
			Name:                  "eve",
			DisplayName:           "Eve",
			SkillsDir:             "agent/skills",
			GlobalSkillsDir:       "",
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				rw := resolveW(w)
				return exists(filepath.Join(rw, "agent")) &&
					packageJSONHasDependency(filepath.Join(rw, "package.json"), "eve")
			},
		},
		"firebender": {
			Name:                  "firebender",
			DisplayName:           "Firebender",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".firebender", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".firebender"))
			},
		},
		"forgecode": {
			Name:                  "forgecode",
			DisplayName:           "ForgeCode",
			SkillsDir:             ".forge/skills",
			GlobalSkillsDir:       filepath.Join(home, ".forge", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".forge"))
			},
		},
		"fx": {
			Name:                  "fx",
			DisplayName:           "fx",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".fx"))
			},
		},
		"gemini-cli": {
			Name:                  "gemini-cli",
			DisplayName:           "Gemini CLI",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".gemini", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".gemini"))
			},
		},
		"github-copilot": {
			Name:                  "github-copilot",
			DisplayName:           "GitHub Copilot",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".copilot", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".copilot"))
			},
		},
		"goose": {
			Name:                  "goose",
			DisplayName:           "Goose",
			SkillsDir:             ".goose/skills",
			GlobalSkillsDir:       filepath.Join(configHome, "goose", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveC(h, c), "goose"))
			},
		},
		"grok": {
			Name:                  "grok",
			DisplayName:           "Grok Build",
			SkillsDir:             ".grok/skills",
			GlobalSkillsDir:       filepath.Join(grokHome, "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "GROK_HOME", filepath.Join(resolveH(h), ".grok"))
				return exists(dir)
			},
		},
		"hermes-agent": {
			Name:                  "hermes-agent",
			DisplayName:           "Hermes Agent",
			SkillsDir:             ".hermes/skills",
			GlobalSkillsDir:       filepath.Join(hermesHome, "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "HERMES_HOME", filepath.Join(resolveH(h), ".hermes"))
				return exists(dir)
			},
		},
		"inference-sh": {
			Name:                  "inference-sh",
			DisplayName:           "inference.sh",
			SkillsDir:             ".inferencesh/skills",
			GlobalSkillsDir:       filepath.Join(home, ".inferencesh", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".inferencesh"))
			},
		},
		"jazz": {
			Name:                  "jazz",
			DisplayName:           "Jazz",
			SkillsDir:             ".jazz/skills",
			GlobalSkillsDir:       filepath.Join(home, ".jazz", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".jazz")) ||
					exists(filepath.Join(resolveW(w), ".jazz"))
			},
		},
		"junie": {
			Name:                  "junie",
			DisplayName:           "Junie",
			SkillsDir:             ".junie/skills",
			GlobalSkillsDir:       filepath.Join(home, ".junie", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".junie"))
			},
		},
		"iflow-cli": {
			Name:                  "iflow-cli",
			DisplayName:           "iFlow CLI",
			SkillsDir:             ".iflow/skills",
			GlobalSkillsDir:       filepath.Join(home, ".iflow", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".iflow"))
			},
		},
		"kilo": {
			Name:                  "kilo",
			DisplayName:           "Kilo Code",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".kilo", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				rh := resolveH(h)
				return exists(filepath.Join(rh, ".kilo")) || exists(filepath.Join(rh, ".kilocode"))
			},
		},
		"kimchi": {
			Name:                  "kimchi",
			DisplayName:           "Kimchi",
			SkillsDir:             ".kimchi/skills",
			GlobalSkillsDir:       filepath.Join(home, ".config", "kimchi", "harness", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return IsKimchiInstalled(resolveH(h), exists)
			},
		},
		"kimi-code-cli": {
			Name:                  "kimi-code-cli",
			DisplayName:           "Kimi Code CLI",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				rh := resolveH(h)
				return exists(filepath.Join(rh, ".kimi-code")) || exists(filepath.Join(rh, ".kimi"))
			},
		},
		"kiro-cli": {
			Name:                  "kiro-cli",
			DisplayName:           "Kiro CLI",
			SkillsDir:             ".kiro/skills",
			GlobalSkillsDir:       filepath.Join(home, ".kiro", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".kiro"))
			},
		},
		"kode": {
			Name:                  "kode",
			DisplayName:           "Kode",
			SkillsDir:             ".kode/skills",
			GlobalSkillsDir:       filepath.Join(home, ".kode", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".kode"))
			},
		},
		"lingma": {
			Name:                  "lingma",
			DisplayName:           "Lingma",
			SkillsDir:             ".lingma/skills",
			GlobalSkillsDir:       filepath.Join(home, ".lingma", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".lingma"))
			},
		},
		"loaf": {
			Name:                  "loaf",
			DisplayName:           "Loaf",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".loaf"))
			},
		},
		"mcpjam": {
			Name:                  "mcpjam",
			DisplayName:           "MCPJam",
			SkillsDir:             ".mcpjam/skills",
			GlobalSkillsDir:       filepath.Join(home, ".mcpjam", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".mcpjam"))
			},
		},
		"minimax-code": {
			Name:                  "minimax-code",
			DisplayName:           "MiniMax Code",
			SkillsDir:             ".minimax/skills",
			GlobalSkillsDir:       filepath.Join(home, ".minimax", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return IsMiniMaxCodeInstalled(resolveH(h), exists)
			},
		},
		"mistral-vibe": {
			Name:                  "mistral-vibe",
			DisplayName:           "Mistral Vibe",
			SkillsDir:             ".vibe/skills",
			GlobalSkillsDir:       filepath.Join(vibeHome, "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "VIBE_HOME", filepath.Join(resolveH(h), ".vibe"))
				return exists(dir)
			},
		},
		"moxby": {
			Name:                  "moxby",
			DisplayName:           "Moxby",
			SkillsDir:             ".moxby/skills",
			GlobalSkillsDir:       filepath.Join(home, ".moxby", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".moxby"))
			},
		},
		"mux": {
			Name:                  "mux",
			DisplayName:           "Mux",
			SkillsDir:             ".mux/skills",
			GlobalSkillsDir:       filepath.Join(home, ".mux", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".mux"))
			},
		},
		"opencode": {
			Name:                  "opencode",
			DisplayName:           "OpenCode",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(configHome, "opencode", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveC(h, c), "opencode"))
			},
		},
		"openhands": {
			Name:                  "openhands",
			DisplayName:           "OpenHands",
			SkillsDir:             ".openhands/skills",
			GlobalSkillsDir:       filepath.Join(home, ".openhands", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".openhands"))
			},
		},
		"ona": {
			Name:                  "ona",
			DisplayName:           "Ona",
			SkillsDir:             ".ona/skills",
			GlobalSkillsDir:       filepath.Join(home, ".ona", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".ona"))
			},
		},
		"pi": {
			Name:                  "pi",
			DisplayName:           "Pi",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".pi", "agent"))
			},
		},
		"posit-assistant": {
			Name:                  "posit-assistant",
			DisplayName:           "Posit Assistant",
			SkillsDir:             ".posit/assistant/skills",
			GlobalSkillsDir:       filepath.Join(home, ".posit", "assistant", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return IsPositAssistantInstalled(resolveH(h), exists)
			},
		},
		"qoder": {
			Name:                  "qoder",
			DisplayName:           "Qoder",
			SkillsDir:             ".qoder/skills",
			GlobalSkillsDir:       filepath.Join(home, ".qoder", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".qoder"))
			},
		},
		"qoder-cn": {
			Name:                  "qoder-cn",
			DisplayName:           "Qoder CN",
			SkillsDir:             ".qoder/skills",
			GlobalSkillsDir:       filepath.Join(home, ".qoder-cn", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".qoder-cn"))
			},
		},
		"qwen-code": {
			Name:                  "qwen-code",
			DisplayName:           "Qwen Code",
			SkillsDir:             ".qwen/skills",
			GlobalSkillsDir:       filepath.Join(home, ".qwen", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".qwen"))
			},
		},
		"replit": {
			Name:                  "replit",
			DisplayName:           "Replit",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(configHome, "agents", "skills"),
			ShowInUniversalList:   false,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveW(w), ".replit"))
			},
		},
		"reasonix": {
			Name:                  "reasonix",
			DisplayName:           "Reasonix",
			SkillsDir:             ".reasonix/skills",
			GlobalSkillsDir:       filepath.Join(reasonixHome, "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(ResolveReasonixHome(resolveH(h), goos, getenv))
			},
		},
		"rovodev": {
			Name:                  "rovodev",
			DisplayName:           "Rovo Dev",
			SkillsDir:             ".rovodev/skills",
			GlobalSkillsDir:       filepath.Join(home, ".rovodev", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".rovodev"))
			},
		},
		"roo": {
			Name:                  "roo",
			DisplayName:           "Roo Code",
			SkillsDir:             ".roo/skills",
			GlobalSkillsDir:       filepath.Join(home, ".roo", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".roo"))
			},
		},
		"sarvam-code": {
			Name:                  "sarvam-code",
			DisplayName:           "Sarvam Code",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				dir := envOrDefault(getenv, "SARVAM_HOME", filepath.Join(resolveH(h), ".sarvam"))
				return exists(dir)
			},
		},
		"tabnine-cli": {
			Name:                  "tabnine-cli",
			DisplayName:           "Tabnine CLI",
			SkillsDir:             ".tabnine/agent/skills",
			GlobalSkillsDir:       filepath.Join(home, ".tabnine", "agent", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".tabnine"))
			},
		},
		"terramind": {
			Name:                  "terramind",
			DisplayName:           "Terramind",
			SkillsDir:             ".terramind/skills",
			GlobalSkillsDir:       filepath.Join(home, ".terramind", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".terramind"))
			},
		},
		"tinycloud": {
			Name:                  "tinycloud",
			DisplayName:           "Tinycloud",
			SkillsDir:             ".tinycloud/skills",
			GlobalSkillsDir:       filepath.Join(home, ".tinycloud", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".tinycloud"))
			},
		},
		"trae": {
			Name:                  "trae",
			DisplayName:           "Trae",
			SkillsDir:             ".trae/skills",
			GlobalSkillsDir:       filepath.Join(home, ".trae", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".trae"))
			},
		},
		"trae-cn": {
			Name:                  "trae-cn",
			DisplayName:           "Trae CN",
			SkillsDir:             ".trae/skills",
			GlobalSkillsDir:       filepath.Join(home, ".trae-cn", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".trae-cn"))
			},
		},
		"warp": {
			Name:                  "warp",
			DisplayName:           "Warp",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".warp"))
			},
		},
		"windsurf": {
			Name:                  "windsurf",
			DisplayName:           "Windsurf",
			SkillsDir:             ".windsurf/skills",
			GlobalSkillsDir:       filepath.Join(home, ".codeium", "windsurf", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".codeium", "windsurf"))
			},
		},
		"zed": {
			Name:                  "zed",
			DisplayName:           "Zed",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(home, ".agents", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				appData := strings.TrimSpace(getenv("APPDATA"))
				flatpakCfg := strings.TrimSpace(getenv("FLATPAK_XDG_CONFIG_HOME"))
				return exists(filepath.Join(resolveC(h, c), "zed")) ||
					(appData != "" && exists(filepath.Join(appData, "Zed"))) ||
					(flatpakCfg != "" && exists(filepath.Join(flatpakCfg, "zed")))
			},
		},
		"zcode": {
			Name:                  "zcode",
			DisplayName:           "ZCode",
			SkillsDir:             ".zcode/skills",
			GlobalSkillsDir:       filepath.Join(home, ".zcode", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return IsZCodeInstalled(resolveH(h), exists)
			},
		},
		"zencoder": {
			Name:                  "zencoder",
			DisplayName:           "Zencoder",
			SkillsDir:             ".zencoder/skills",
			GlobalSkillsDir:       filepath.Join(home, ".zencoder", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".zencoder"))
			},
		},
		"zenflow": {
			Name:                  "zenflow",
			DisplayName:           "Zenflow",
			SkillsDir:             ".zencoder/skills",
			GlobalSkillsDir:       filepath.Join(home, ".zencoder", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".zencoder"))
			},
		},
		"neovate": {
			Name:                  "neovate",
			DisplayName:           "Neovate",
			SkillsDir:             ".neovate/skills",
			GlobalSkillsDir:       filepath.Join(home, ".neovate", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".neovate"))
			},
		},
		"pochi": {
			Name:                  "pochi",
			DisplayName:           "Pochi",
			SkillsDir:             ".pochi/skills",
			GlobalSkillsDir:       filepath.Join(home, ".pochi", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".pochi"))
			},
		},
		"promptscript": {
			Name:                  "promptscript",
			DisplayName:           "PromptScript",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       "",
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: false,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				rw := resolveW(w)
				return exists(filepath.Join(rw, ".promptscript")) ||
					exists(filepath.Join(rw, "promptscript.yaml"))
			},
		},
		"adal": {
			Name:                  "adal",
			DisplayName:           "AdaL",
			SkillsDir:             ".adal/skills",
			GlobalSkillsDir:       filepath.Join(home, ".adal", "skills"),
			ShowInUniversalList:   true,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled: func(h, c, w string) bool {
				return exists(filepath.Join(resolveH(h), ".adal"))
			},
		},
		"universal": {
			Name:                  "universal",
			DisplayName:           "Universal",
			SkillsDir:             ".agents/skills",
			GlobalSkillsDir:       filepath.Join(configHome, "agents", "skills"),
			ShowInUniversalList:   false,
			ShowInUniversalPrompt: true,
			GlobalReadsCanonical:  true,
			DetectInstalled:       func(h, c, w string) bool { return false },
		},
	}
}

// Agents holds the supported agent configurations, initialized from DefaultAgentEnv() and
// refreshable via RefreshAgents() or replaceable in tests.
var Agents = make(map[AgentType]AgentConfig)

func init() {
	RefreshAgents()
}

// RefreshAgents rebuilds the package-level Agents map from the current OS environment and home directory.
func RefreshAgents() {
	Agents = BuildAgents(DefaultAgentEnv())
}

// GetAgentConfig returns the AgentConfig for an agent type.
func GetAgentConfig(t AgentType) (AgentConfig, bool) {
	cfg, ok := Agents[t]
	return cfg, ok
}

// ResolveAgentConfigWithEnv returns the AgentConfig for an agent type resolved against the supplied AgentEnv.
func ResolveAgentConfigWithEnv(t AgentType, env AgentEnv) (AgentConfig, bool) {
	built := BuildAgents(env)
	cfg, ok := built[t]
	return cfg, ok
}

func orderedAgentTypes(agentsMap map[AgentType]AgentConfig) []AgentType {
	seen := make(map[AgentType]bool, len(agentsMap))
	order := make([]AgentType, 0, len(agentsMap))
	for _, k := range AllAgentTypes {
		if _, ok := agentsMap[k]; ok {
			seen[k] = true
			order = append(order, k)
		}
	}
	var extras []string
	for k := range agentsMap {
		if !seen[k] {
			extras = append(extras, string(k))
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		order = append(order, AgentType(k))
	}
	return order
}

// DetectInstalledAgents checks environment and folders to find installed agents.
func DetectInstalledAgents(cwd string) []AgentType {
	env := DefaultAgentEnv()
	var results []AgentType
	for _, k := range orderedAgentTypes(Agents) {
		if k == AgentUniversal {
			continue
		}
		v := Agents[k]
		if v.DetectInstalled != nil && v.DetectInstalled(env.Home, env.ConfigHome, cwd) {
			results = append(results, k)
		}
	}
	return results
}

// DetectInstalledAgentsWithEnv checks installed agents against an explicit AgentEnv.
func DetectInstalledAgentsWithEnv(cwd string, env AgentEnv) []AgentType {
	built := BuildAgents(env)
	home := env.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	configHome := env.ConfigHome
	if configHome == "" {
		getenv := env.Getenv
		if getenv == nil {
			getenv = os.Getenv
		}
		configHome = strings.TrimSpace(getenv("XDG_CONFIG_HOME"))
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
	}
	var results []AgentType
	for _, k := range AllAgentTypes {
		if k == AgentUniversal {
			continue
		}
		if cfg, ok := built[k]; ok && cfg.DetectInstalled != nil && cfg.DetectInstalled(home, configHome, cwd) {
			results = append(results, k)
		}
	}
	return results
}

// GetUniversalAgents returns agents using .agents/skills that are shown in the universal list.
func GetUniversalAgents() []AgentType {
	var results []AgentType
	for _, k := range orderedAgentTypes(Agents) {
		v := Agents[k]
		if v.SkillsDir == ".agents/skills" && v.ShowInUniversalList {
			results = append(results, k)
		}
	}
	return results
}

// GetVisibleUniversalAgents returns universal agents displayed in interactive prompts.
func GetVisibleUniversalAgents() []AgentType {
	var results []AgentType
	for _, k := range orderedAgentTypes(Agents) {
		v := Agents[k]
		if v.SkillsDir == ".agents/skills" && v.ShowInUniversalList && v.ShowInUniversalPrompt {
			results = append(results, k)
		}
	}
	return results
}

// GetNonUniversalAgents returns agents using custom agent-specific skill directories.
func GetNonUniversalAgents() []AgentType {
	var results []AgentType
	for _, k := range orderedAgentTypes(Agents) {
		v := Agents[k]
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

// ReadsCanonicalGlobally checks if an agent reads ~/.agents/skills for global installs
// (false for Antigravity and Antigravity CLI per #2566, which read ~/.gemini/config/skills).
func ReadsCanonicalGlobally(t AgentType) bool {
	cfg, ok := Agents[t]
	if !ok {
		return false
	}
	return cfg.SkillsDir == ".agents/skills" && cfg.GlobalReadsCanonical
}

// GetEveSubagents discovers sorted Eve subagent directory names under agent/subagents/ in cwd.
func GetEveSubagents(cwd string) []string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	dir := filepath.Join(cwd, EveSubagentsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if names == nil {
		return []string{}
	}
	return names
}

// GetEveSubagentSkillsDir resolves agent/subagents/<sanitized-subagent>/skills under cwd.
func GetEveSubagentSkillsDir(subagent, cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, EveSubagentsDir, SanitizeName(subagent), "skills")
}

// AgentProjectSkillDirs returns sorted unique project skill directories (excluding bare "skills")
// across all configured agents and legacy agent directories, matching AGENT_PROJECT_SKILL_DIRS.
func AgentProjectSkillDirs() []string {
	seen := make(map[string]bool)
	var dirs []string
	for _, cfg := range Agents {
		d := filepath.ToSlash(cfg.SkillsDir)
		if d != "" && d != "skills" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, d := range LegacyAgentProjectSkillDirs {
		if d != "" && d != "skills" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return dirs
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

// ParsedSource represents a parsed skill installation source (matching upstream ParsedSource).
type ParsedSource struct {
	Type        string `json:"type"` // "github" | "gitlab" | "git" | "local" | "well-known" | "download"
	URL         string `json:"url"`
	Subpath     string `json:"subpath,omitempty"`
	LocalPath   string `json:"localPath,omitempty"`
	Ref         string `json:"ref,omitempty"`
	SkillFilter string `json:"skillFilter,omitempty"`
}

// RemoteSkill represents a skill fetched from a remote host provider.
type RemoteSkill struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	Content          string                 `json:"content"`
	InstallName      string                 `json:"installName"`
	SourceURL        string                 `json:"sourceUrl"`
	ProviderID       string                 `json:"providerId"`
	SourceIdentifier string                 `json:"sourceIdentifier"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// InstalledSkill represents a skill that has been installed in the agent directories.
type InstalledSkill struct {
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Path          string      `json:"path"`
	CanonicalPath string      `json:"canonicalPath"`
	Scope         string      `json:"scope"` // "project" or "global"
	Agents        []AgentType `json:"agents"`
	Source        string      `json:"source,omitempty"`
	SourceURL     string      `json:"sourceUrl,omitempty"`
	SourceType    string      `json:"sourceType,omitempty"`
	PluginName    string      `json:"pluginName,omitempty"`
	InstallMode   string      `json:"installMode,omitempty"`
	Missing       bool        `json:"missing,omitempty"`
	Untracked     bool        `json:"untracked,omitempty"`
}

// SearchResult represents a skill returned by the skills.sh search API.
type SearchResult struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Source      string `json:"source"`
	Installs    int    `json:"installs,omitempty"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Scope       string `json:"scope,omitempty"`
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
	Agent       []AgentType
	JSON        bool
}

// AddOptions contains options for the add command.
type AddOptions struct {
	Global      bool
	Copy        bool
	Agent       []AgentType
	Verbose     bool
	Yes         bool
	Skill       []string
	List        bool
	FullDepth   bool
	All         bool
	JSON        bool
	Subagent    []string
	Metadata    string
	DryRun      bool
	Force       bool
	InstallMode InstallMode
	Cwd         string
}

// InstallFromLockOptions contains options for restoring skills from skills-lock.json.
type InstallFromLockOptions struct {
	Agent          []AgentType
	Copy           bool
	Yes            bool
	DryRun         bool
	FrozenLockfile *bool
	Force          bool
	Cwd            string
}

// UseOptions contains options for the 'use' command (ephemeral prompt generation without installing).
type UseOptions struct {
	Skill     string
	Agent     []AgentType
	FullDepth bool
	JSON      bool
	Help      bool
	Cwd       string
}

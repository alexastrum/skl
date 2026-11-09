package skl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTypesHelpers(t *testing.T) {
	if !IsUniversalAgent(AgentAntigravity) {
		t.Error("expected antigravity to be a universal agent")
	}
	if !IsUniversalAgent(AgentAntigravityCLI) {
		t.Error("expected antigravity-cli to be a universal agent")
	}
	if !IsUniversalAgent(AgentUniversal) {
		t.Error("expected universal to be a universal agent")
	}

	if len(AllAgentTypes) != 79 {
		t.Errorf("expected 79 upstream agents in AllAgentTypes, got %d", len(AllAgentTypes))
	}
	if len(Agents) != len(AllAgentTypes) {
		t.Errorf("expected Agents map len %d, got %d", len(AllAgentTypes), len(Agents))
	}
	if _, ok := Agents["kimi-code-cli"]; !ok {
		t.Error("expected kimi-code-cli in Agents")
	}
	if _, ok := Agents["kimi-cli"]; ok {
		t.Error("expected legacy kimi-cli to be absent from Agents")
	}

	univs := GetUniversalAgents()
	foundAntigravity := false
	for _, a := range univs {
		if a == AgentAntigravity {
			foundAntigravity = true
			break
		}
	}
	if !foundAntigravity {
		t.Error("expected GetUniversalAgents to include antigravity")
	}

	visibleUnivs := GetVisibleUniversalAgents()
	for _, hidden := range []AgentType{"antigravity", "antigravity-cli", "deepagents", "dexto", "firebender", "loaf", "sarvam-code", "promptscript"} {
		for _, v := range visibleUnivs {
			if v == hidden {
				t.Errorf("expected %q to be excluded from GetVisibleUniversalAgents()", hidden)
			}
		}
	}

	nonUnivs := GetNonUniversalAgents()
	if len(nonUnivs) == 0 {
		t.Error("expected at least one non-universal agent")
	}

	// Test DetectInstalledAgents with temporary mocks
	tmpDir := t.TempDir()
	origAgents := Agents
	defer func() { Agents = origAgents }()

	Agents = make(map[AgentType]AgentConfig)
	Agents["mock-installed"] = AgentConfig{
		Name:        "mock-installed",
		DisplayName: "Mock Installed",
		SkillsDir:   ".mock-installed/skills",
		DetectInstalled: func(home, configHome, cwd string) bool {
			_, err := os.Stat(filepath.Join(cwd, "marker"))
			return err == nil
		},
	}

	detected := DetectInstalledAgents(tmpDir)
	if len(detected) != 0 {
		t.Errorf("expected no detected agents, got %v", detected)
	}

	_ = os.WriteFile(filepath.Join(tmpDir, "marker"), []byte("marker"), 0644)
	detected2 := DetectInstalledAgents(tmpDir)
	if len(detected2) != 1 || detected2[0] != "mock-installed" {
		t.Errorf("expected mock-installed detected, got %v", detected2)
	}
}

func TestAntigravityAndGlobalCanonicalRouting(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	built := BuildAgents(AgentEnv{
		Home:       home,
		ConfigHome: filepath.Join(home, ".config"),
		GOOS:       "linux",
		Getenv:     func(string) string { return "" },
	})

	agy := built[AgentAntigravity]
	agyCLI := built[AgentAntigravityCLI]
	wantGlobal := filepath.Join(home, ".gemini", "config", "skills")
	if agy.GlobalSkillsDir != wantGlobal {
		t.Errorf("antigravity GlobalSkillsDir = %q, want %q", agy.GlobalSkillsDir, wantGlobal)
	}
	if agyCLI.GlobalSkillsDir != wantGlobal {
		t.Errorf("antigravity-cli GlobalSkillsDir = %q, want %q", agyCLI.GlobalSkillsDir, wantGlobal)
	}
	if agy.GlobalReadsCanonical || agyCLI.GlobalReadsCanonical {
		t.Error("expected GlobalReadsCanonical=false for antigravity and antigravity-cli")
	}
	if ReadsCanonicalGlobally(AgentAntigravity) || ReadsCanonicalGlobally(AgentAntigravityCLI) {
		t.Error("expected ReadsCanonicalGlobally=false for antigravity and antigravity-cli")
	}
	if !ReadsCanonicalGlobally("codex") || !ReadsCanonicalGlobally("github-copilot") {
		t.Error("expected ReadsCanonicalGlobally=true for codex and github-copilot")
	}

	// Verify antigravity recognizes both ~/.gemini/antigravity and ~/.gemini/config
	homeA := t.TempDir()
	_ = os.MkdirAll(filepath.Join(homeA, ".gemini", "config"), 0755)
	if !agy.DetectInstalled(homeA, "", "") {
		t.Error("expected antigravity DetectInstalled to recognize ~/.gemini/config")
	}

	homeB := t.TempDir()
	_ = os.MkdirAll(filepath.Join(homeB, ".gemini", "antigravity"), 0755)
	if !agy.DetectInstalled(homeB, "", "") {
		t.Error("expected antigravity DetectInstalled to recognize ~/.gemini/antigravity")
	}

	homeC := t.TempDir()
	_ = os.MkdirAll(filepath.Join(homeC, ".gemini", "antigravity-cli"), 0755)
	if !agyCLI.DetectInstalled(homeC, "", "") {
		t.Error("expected antigravity-cli DetectInstalled to recognize ~/.gemini/antigravity-cli")
	}
}

func TestDynamicEnvAndPlatformResolution(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	envMap := map[string]string{
		"XDG_CONFIG_HOME":         filepath.Join(root, "xdg"),
		"CODEX_HOME":              filepath.Join(root, "my-codex"),
		"CLAUDE_CONFIG_DIR":       filepath.Join(root, "my-claude"),
		"VIBE_HOME":               filepath.Join(root, "my-vibe"),
		"HERMES_HOME":             filepath.Join(root, "my-hermes"),
		"AUTOHAND_HOME":           filepath.Join(root, "my-autohand"),
		"GROK_HOME":               filepath.Join(root, "my-grok"),
		"SARVAM_HOME":             filepath.Join(root, "my-sarvam"),
		"FLATPAK_XDG_CONFIG_HOME": filepath.Join(root, "flatpak"),
	}
	for _, dir := range envMap {
		_ = os.MkdirAll(dir, 0755)
	}
	_ = os.MkdirAll(filepath.Join(envMap["FLATPAK_XDG_CONFIG_HOME"], "zed"), 0755)

	env := AgentEnv{
		Home:   home,
		GOOS:   "linux",
		Getenv: func(k string) string { return envMap[k] },
	}
	built := BuildAgents(env)

	if built["claude-code"].GlobalSkillsDir != filepath.Join(root, "my-claude", "skills") {
		t.Errorf("claude-code GlobalSkillsDir = %q", built["claude-code"].GlobalSkillsDir)
	}
	if !built["claude-code"].CreateProjectSkillsDirByDefault {
		t.Error("expected claude-code CreateProjectSkillsDirByDefault=true")
	}
	if built["mistral-vibe"].GlobalSkillsDir != filepath.Join(root, "my-vibe", "skills") {
		t.Errorf("mistral-vibe GlobalSkillsDir = %q", built["mistral-vibe"].GlobalSkillsDir)
	}
	if built["hermes-agent"].GlobalSkillsDir != filepath.Join(root, "my-hermes", "skills") {
		t.Errorf("hermes-agent GlobalSkillsDir = %q", built["hermes-agent"].GlobalSkillsDir)
	}
	if built["autohand-code"].GlobalSkillsDir != filepath.Join(root, "my-autohand", "skills") {
		t.Errorf("autohand-code GlobalSkillsDir = %q", built["autohand-code"].GlobalSkillsDir)
	}
	if built["grok"].GlobalSkillsDir != filepath.Join(root, "my-grok", "skills") {
		t.Errorf("grok GlobalSkillsDir = %q", built["grok"].GlobalSkillsDir)
	}
	if built["opencode"].GlobalSkillsDir != filepath.Join(root, "xdg", "opencode", "skills") {
		t.Errorf("opencode GlobalSkillsDir = %q", built["opencode"].GlobalSkillsDir)
	}

	detected := DetectInstalledAgentsWithEnv(root, env)
	detectedSet := make(map[AgentType]bool)
	for _, a := range detected {
		detectedSet[a] = true
	}
	for _, want := range []AgentType{"codex", "claude-code", "mistral-vibe", "hermes-agent", "autohand-code", "grok", "sarvam-code", "zed"} {
		if !detectedSet[want] {
			t.Errorf("expected %q detected via env override, got %v", want, detected)
		}
	}

	// Reasonix platform matrix (#2433)
	for _, goos := range []string{"linux", "darwin"} {
		got := ResolveReasonixHome(home, goos, func(k string) string {
			if k == "APPDATA" {
				return filepath.Join(root, "roaming")
			}
			return ""
		})
		if got != filepath.Join(home, ".reasonix") {
			t.Errorf("ResolveReasonixHome(%s) = %q, want %q", goos, got, filepath.Join(home, ".reasonix"))
		}
	}

	winRoaming := ResolveReasonixHome(home, "windows", func(k string) string {
		if k == "APPDATA" {
			return filepath.Join(root, "roaming")
		}
		return ""
	})
	if winRoaming != filepath.Join(root, "roaming", "reasonix") {
		t.Errorf("ResolveReasonixHome(windows, APPDATA) = %q", winRoaming)
	}

	winBlank := ResolveReasonixHome(home, "windows", func(k string) string {
		if k == "APPDATA" {
			return "   "
		}
		return ""
	})
	if winBlank != filepath.Join(home, "AppData", "Roaming", "reasonix") {
		t.Errorf("ResolveReasonixHome(windows, blank APPDATA) = %q", winBlank)
	}

	winOverride := ResolveReasonixHome(home, "windows", func(k string) string {
		if k == "REASONIX_HOME" {
			return filepath.Join(root, "custom-reasonix")
		}
		return filepath.Join(root, "roaming")
	})
	if winOverride != filepath.Join(root, "custom-reasonix") {
		t.Errorf("ResolveReasonixHome(REASONIX_HOME) = %q", winOverride)
	}
}

func TestSpecialAgents_EvePromptScriptOpenClawAndDesktopApps(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	built := BuildAgents(AgentEnv{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }})

	// Eve & PromptScript have unsupported/empty GlobalSkillsDir
	if built["eve"].GlobalSkillsDir != "" {
		t.Errorf("expected eve GlobalSkillsDir to be empty, got %q", built["eve"].GlobalSkillsDir)
	}
	if built["promptscript"].GlobalSkillsDir != "" {
		t.Errorf("expected promptscript GlobalSkillsDir to be empty, got %q", built["promptscript"].GlobalSkillsDir)
	}

	// Eve requires agent/ dir AND eve in package.json dependencies or devDependencies
	_ = os.MkdirAll(filepath.Join(cwd, "agent", "subagents", "reviewer"), 0755)
	_ = os.MkdirAll(filepath.Join(cwd, "agent", "subagents", "author"), 0755)
	if built["eve"].DetectInstalled(home, "", cwd) {
		t.Error("expected eve not detected without package.json dependency")
	}
	_ = os.WriteFile(filepath.Join(cwd, "package.json"), []byte(`{"devDependencies":{"eve":"1.0.0"}}`), 0644)
	if !built["eve"].DetectInstalled(home, "", cwd) {
		t.Error("expected eve detected when agent/ and devDependencies.eve exist")
	}
	if got := GetEveSubagents(cwd); !reflect.DeepEqual(got, []string{"author", "reviewer"}) {
		t.Errorf("GetEveSubagents() = %v, want [author reviewer]", got)
	}
	if got := GetEveSubagentSkillsDir("../My Subagent", cwd); got != filepath.Join(cwd, "agent", "subagents", "my-subagent", "skills") {
		t.Errorf("GetEveSubagentSkillsDir() = %q", got)
	}

	// PromptScript detects promptscript.yaml
	psDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(psDir, "promptscript.yaml"), []byte("version: 1\n"), 0644)
	if !built["promptscript"].DetectInstalled(home, "", psDir) {
		t.Error("expected promptscript detected via promptscript.yaml")
	}

	// OpenClaw fallback precedence
	existsSet := map[string]bool{filepath.Join(home, ".moltbot"): true}
	if got := GetOpenClawGlobalSkillsDir(home, func(p string) bool { return existsSet[p] }); got != filepath.Join(home, ".moltbot", "skills") {
		t.Errorf("GetOpenClawGlobalSkillsDir(.moltbot) = %q", got)
	}
	existsSet[filepath.Join(home, ".clawdbot")] = true
	if got := GetOpenClawGlobalSkillsDir(home, func(p string) bool { return existsSet[p] }); got != filepath.Join(home, ".clawdbot", "skills") {
		t.Errorf("GetOpenClawGlobalSkillsDir(.clawdbot) = %q", got)
	}
	existsSet[filepath.Join(home, ".openclaw")] = true
	if got := GetOpenClawGlobalSkillsDir(home, func(p string) bool { return existsSet[p] }); got != filepath.Join(home, ".openclaw", "skills") {
		t.Errorf("GetOpenClawGlobalSkillsDir(.openclaw) = %q", got)
	}

	// Desktop / legacy app helpers
	if !IsZCodeInstalled(home, func(p string) bool { return p == "/Applications/ZCode.app" }) {
		t.Error("expected IsZCodeInstalled true for /Applications/ZCode.app")
	}
	if !IsMiniMaxCodeInstalled(home, func(p string) bool { return p == "/Applications/MiniMax Code.app" }) {
		t.Error("expected IsMiniMaxCodeInstalled true for /Applications/MiniMax Code.app")
	}
	if !IsPositAssistantInstalled(home, func(p string) bool { return p == filepath.Join(home, ".positai") }) {
		t.Error("expected IsPositAssistantInstalled true for legacy ~/.positai")
	}

	dirs := AgentProjectSkillDirs()
	if len(dirs) == 0 {
		t.Fatal("expected non-empty AgentProjectSkillDirs()")
	}
	for _, d := range dirs {
		if d == "skills" {
			t.Error("expected bare 'skills' to be excluded from AgentProjectSkillDirs()")
		}
	}
}

func TestSanitizeNameAndTerminalEscapes(t *testing.T) {
	nameCases := []struct {
		in   string
		want string
	}{
		{"MySkill", "myskill"},
		{"UPPERCASE", "uppercase"},
		{"my skill", "my-skill"},
		{"Convex Best Practices", "convex-best-practices"},
		{"my   skill", "my-skill"},
		{"bun.sh", "bun.sh"},
		{"my_skill", "my_skill"},
		{"skill.v2_beta", "skill.v2_beta"},
		{"skill123", "skill123"},
		{"v2.0", "v2.0"},
		{"skill@name", "skill-name"},
		{"skill@#$name", "skill-name"},
		{"a!!!b", "a-b"},
		{"../etc/passwd", "etc-passwd"},
		{"../../secret", "secret"},
		{"..\\..\\secret", "secret"},
		{"/etc/passwd", "etc-passwd"},
		{"C:\\Windows\\System32", "c-windows-system32"},
		{".hidden", "hidden"},
		{"...skill", "skill"},
		{"skill..", "skill"},
		{"--skill--", "skill"},
		{".-.-skill", "skill"},
		{"-.-.skill", "skill"},
		{"", "unnamed-skill"},
		{"...", "unnamed-skill"},
		{"---", "unnamed-skill"},
		{"@#$%", "unnamed-skill"},
		{strings.Repeat("a", 300), strings.Repeat("a", 255)},
		{"skill日本語", "skill"},
		{"émoji🎉skill", "moji-skill"},
		{"vercel/next.js", "vercel-next.js"},
		{"https://example.com", "https-example.com"},
	}
	for _, tc := range nameCases {
		if got := SanitizeName(tc.in); got != tc.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Terminal escape stripping & metadata sanitization (CWE-150)
	if got := StripTerminalEscapes("\x1b[31mred text\x1b[0m"); got != "red text" {
		t.Errorf("StripTerminalEscapes CSI = %q", got)
	}
	if got := StripTerminalEscapes("\x1b]0;malicious title\x07rest"); got != "rest" {
		t.Errorf("StripTerminalEscapes OSC BEL = %q", got)
	}
	if got := StripTerminalEscapes("\x1b]0;title\x1b\\"); got != "" {
		t.Errorf("StripTerminalEscapes OSC ST = %q", got)
	}
	if got := StripTerminalEscapes("\x1b]8;;https://evil.com\x07click\x1b]8;;\x07"); got != "click" {
		t.Errorf("StripTerminalEscapes OSC 8 = %q", got)
	}
	if got := StripTerminalEscapes("\x1bPpayload\x1b\\ok"); got != "ok" {
		t.Errorf("StripTerminalEscapes DCS = %q", got)
	}
	if got := StripTerminalEscapes("\x1b7text\x1b8"); got != "text" {
		t.Errorf("StripTerminalEscapes simple ESC = %q", got)
	}
	if got := StripTerminalEscapes("hello\x07\x08\r\x00world"); got != "helloworld" {
		t.Errorf("StripTerminalEscapes control chars = %q", got)
	}
	if got := StripTerminalEscapes("hello\tworld\nnext"); got != "hello\tworld\nnext" {
		t.Errorf("StripTerminalEscapes should preserve tab/newline, got %q", got)
	}
	if got := StripTerminalEscapes("hello\x9bworld"); got != "helloworld" {
		t.Errorf("StripTerminalEscapes raw C1 = %q", got)
	}
	if got := StripTerminalEscapes("hello\u009bworld"); got != "helloworld" {
		t.Errorf("StripTerminalEscapes rune C1 = %q", got)
	}
	if got := StripTerminalEscapes("hello 日本語 🎉 world"); got != "hello 日本語 🎉 world" {
		t.Errorf("StripTerminalEscapes corrupted UTF-8: %q", got)
	}

	poc := "\x1b]0;[POC] skills output hijacked\x07\x1b[3J\x1b[2J\x1b[H\x1b[31m[POC] Terminal output injected from SKILL.md\x1b[0m\n\x1b[33mThis cleared the screen and overwrote CLI output.\x1b[0m"
	wantMeta := "[POC] Terminal output injected from SKILL.md This cleared the screen and overwrote CLI output."
	if got := SanitizeMetadata(poc); got != wantMeta {
		t.Errorf("SanitizeMetadata(poc) = %q, want %q", got, wantMeta)
	}
}

func TestAllAgentsDetectionCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	for name, agent := range Agents {
		if agent.DetectInstalled != nil {
			_ = agent.DetectInstalled(tmpDir, tmpDir, tmpDir)
		}
		if name == "openclaw" {
			_ = os.MkdirAll(filepath.Join(tmpDir, ".openclaw"), 0755)
			_ = os.MkdirAll(filepath.Join(tmpDir, ".clawdbot"), 0755)
			_ = os.MkdirAll(filepath.Join(tmpDir, ".moltbot"), 0755)
			_ = agent.DetectInstalled(tmpDir, tmpDir, tmpDir)
		}
	}
}

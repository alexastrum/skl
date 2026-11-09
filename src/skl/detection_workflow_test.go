package skl

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper to write a dummy SKILL.md file
func writeDummySkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create directory %q: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: dummy description\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}
}

// Helper function to create marker path for an agent
func createAgentMarker(t *testing.T, agent AgentType, home, configHome, cwd string) {
	t.Helper()
	switch agent {
	case "aider-desk":
		_ = os.MkdirAll(filepath.Join(home, ".aider-desk"), 0755)
	case "amp":
		_ = os.MkdirAll(filepath.Join(configHome, "amp"), 0755)
	case "antigravity":
		_ = os.MkdirAll(filepath.Join(home, ".gemini", "antigravity"), 0755)
	case "antigravity-cli":
		_ = os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0755)
	case "astrbot":
		_ = os.MkdirAll(filepath.Join(home, ".astrbot"), 0755)
	case "autohand-code":
		_ = os.MkdirAll(filepath.Join(home, ".autohand"), 0755)
	case "augment":
		_ = os.MkdirAll(filepath.Join(home, ".augment"), 0755)
	case "bob":
		_ = os.MkdirAll(filepath.Join(home, ".bob"), 0755)
	case "claude-code":
		_ = os.MkdirAll(filepath.Join(home, ".claude"), 0755)
	case "openclaw":
		_ = os.MkdirAll(filepath.Join(home, ".openclaw"), 0755)
	case "cline":
		_ = os.MkdirAll(filepath.Join(home, ".cline"), 0755)
	case "codearts-agent":
		_ = os.MkdirAll(filepath.Join(home, ".codeartsdoer"), 0755)
	case "codebuddy":
		_ = os.MkdirAll(filepath.Join(cwd, ".codebuddy"), 0755)
	case "codemaker":
		_ = os.MkdirAll(filepath.Join(home, ".codemaker"), 0755)
	case "codestudio":
		_ = os.MkdirAll(filepath.Join(home, ".codestudio"), 0755)
	case "codex":
		_ = os.MkdirAll(filepath.Join(home, ".codex"), 0755)
	case "command-code":
		_ = os.MkdirAll(filepath.Join(home, ".commandcode"), 0755)
	case "continue":
		_ = os.MkdirAll(filepath.Join(cwd, ".continue"), 0755)
	case "cortex":
		_ = os.MkdirAll(filepath.Join(home, ".snowflake", "cortex"), 0755)
	case "crush":
		_ = os.MkdirAll(filepath.Join(home, ".config", "crush"), 0755)
	case "cursor":
		_ = os.MkdirAll(filepath.Join(home, ".cursor"), 0755)
	case "deepagents":
		_ = os.MkdirAll(filepath.Join(home, ".deepagents"), 0755)
	case "devin":
		_ = os.MkdirAll(filepath.Join(configHome, "devin"), 0755)
	case "dexto":
		_ = os.MkdirAll(filepath.Join(home, ".dexto"), 0755)
	case "droid":
		_ = os.MkdirAll(filepath.Join(home, ".factory"), 0755)
	case "eve":
		_ = os.MkdirAll(filepath.Join(cwd, "agent"), 0755)
		_ = os.WriteFile(filepath.Join(cwd, "package.json"), []byte(`{"dependencies":{"eve":"^1.0.0"}}`), 0644)
	case "firebender":
		_ = os.MkdirAll(filepath.Join(home, ".firebender"), 0755)
	case "forgecode":
		_ = os.MkdirAll(filepath.Join(home, ".forge"), 0755)
	case "fx":
		_ = os.MkdirAll(filepath.Join(home, ".fx"), 0755)
	case "gemini-cli":
		_ = os.MkdirAll(filepath.Join(home, ".gemini"), 0755)
	case "github-copilot":
		_ = os.MkdirAll(filepath.Join(home, ".copilot"), 0755)
	case "goose":
		_ = os.MkdirAll(filepath.Join(configHome, "goose"), 0755)
	case "grok":
		_ = os.MkdirAll(filepath.Join(home, ".grok"), 0755)
	case "hermes-agent":
		_ = os.MkdirAll(filepath.Join(home, ".hermes"), 0755)
	case "inference-sh":
		_ = os.MkdirAll(filepath.Join(home, ".inferencesh"), 0755)
	case "jazz":
		_ = os.MkdirAll(filepath.Join(home, ".jazz"), 0755)
	case "junie":
		_ = os.MkdirAll(filepath.Join(home, ".junie"), 0755)
	case "iflow-cli":
		_ = os.MkdirAll(filepath.Join(home, ".iflow"), 0755)
	case "kilo":
		_ = os.MkdirAll(filepath.Join(home, ".kilocode"), 0755)
	case "kimchi":
		_ = os.MkdirAll(filepath.Join(home, ".config", "kimchi"), 0755)
	case "kimi-code-cli":
		_ = os.MkdirAll(filepath.Join(home, ".kimi-code"), 0755)
	case "kiro-cli":
		_ = os.MkdirAll(filepath.Join(home, ".kiro"), 0755)
	case "kode":
		_ = os.MkdirAll(filepath.Join(home, ".kode"), 0755)
	case "lingma":
		_ = os.MkdirAll(filepath.Join(home, ".lingma"), 0755)
	case "loaf":
		_ = os.MkdirAll(filepath.Join(home, ".loaf"), 0755)
	case "mcpjam":
		_ = os.MkdirAll(filepath.Join(home, ".mcpjam"), 0755)
	case "minimax-code":
		_ = os.MkdirAll(filepath.Join(home, ".minimax"), 0755)
	case "mistral-vibe":
		_ = os.MkdirAll(filepath.Join(home, ".vibe"), 0755)
	case "moxby":
		_ = os.MkdirAll(filepath.Join(home, ".moxby"), 0755)
	case "mux":
		_ = os.MkdirAll(filepath.Join(home, ".mux"), 0755)
	case "opencode":
		_ = os.MkdirAll(filepath.Join(configHome, "opencode"), 0755)
	case "openhands":
		_ = os.MkdirAll(filepath.Join(home, ".openhands"), 0755)
	case "ona":
		_ = os.MkdirAll(filepath.Join(home, ".ona"), 0755)
	case "pi":
		_ = os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0755)
	case "posit-assistant":
		_ = os.MkdirAll(filepath.Join(home, ".posit", "assistant"), 0755)
	case "qoder":
		_ = os.MkdirAll(filepath.Join(home, ".qoder"), 0755)
	case "qoder-cn":
		_ = os.MkdirAll(filepath.Join(home, ".qoder-cn"), 0755)
	case "qwen-code":
		_ = os.MkdirAll(filepath.Join(home, ".qwen"), 0755)
	case "replit":
		_ = os.MkdirAll(filepath.Join(cwd, ".replit"), 0755)
	case "reasonix":
		_ = os.MkdirAll(filepath.Join(home, ".reasonix"), 0755)
	case "rovodev":
		_ = os.MkdirAll(filepath.Join(home, ".rovodev"), 0755)
	case "roo":
		_ = os.MkdirAll(filepath.Join(home, ".roo"), 0755)
	case "sarvam-code":
		_ = os.MkdirAll(filepath.Join(home, ".sarvam"), 0755)
	case "tabnine-cli":
		_ = os.MkdirAll(filepath.Join(home, ".tabnine"), 0755)
	case "terramind":
		_ = os.MkdirAll(filepath.Join(home, ".terramind"), 0755)
	case "tinycloud":
		_ = os.MkdirAll(filepath.Join(home, ".tinycloud"), 0755)
	case "trae":
		_ = os.MkdirAll(filepath.Join(home, ".trae"), 0755)
	case "trae-cn":
		_ = os.MkdirAll(filepath.Join(home, ".trae-cn"), 0755)
	case "warp":
		_ = os.MkdirAll(filepath.Join(home, ".warp"), 0755)
	case "windsurf":
		_ = os.MkdirAll(filepath.Join(home, ".codeium", "windsurf"), 0755)
	case "zed":
		_ = os.MkdirAll(filepath.Join(configHome, "zed"), 0755)
	case "zcode":
		_ = os.MkdirAll(filepath.Join(home, ".zcode"), 0755)
	case "zencoder":
		_ = os.MkdirAll(filepath.Join(home, ".zencoder"), 0755)
	case "zenflow":
		_ = os.MkdirAll(filepath.Join(home, ".zencoder"), 0755)
	case "neovate":
		_ = os.MkdirAll(filepath.Join(home, ".neovate"), 0755)
	case "pochi":
		_ = os.MkdirAll(filepath.Join(home, ".pochi"), 0755)
	case "promptscript":
		_ = os.MkdirAll(filepath.Join(cwd, ".promptscript"), 0755)
	case "adal":
		_ = os.MkdirAll(filepath.Join(home, ".adal"), 0755)
	}
}

// 1. no agents installed: verify that it installs to antigravity and outputs the agy-cli onboarding tip.
func TestIntegration_NoAgents(t *testing.T) {
	origAgents := Agents
	defer func() { Agents = origAgents }()

	tmpDir := t.TempDir()

	Agents = make(map[AgentType]AgentConfig)
	Agents["antigravity"] = AgentConfig{
		Name:            "antigravity",
		DisplayName:     "Antigravity",
		SkillsDir:       ".agents/skills",
		GlobalSkillsDir: filepath.Join(tmpDir, ".gemini", "config", "skills"),
		DetectInstalled: func(home, configHome, cwd string) bool {
			return false
		},
	}
	Agents["universal"] = AgentConfig{
		Name:            "universal",
		DisplayName:     "Universal",
		SkillsDir:       ".agents/skills",
		GlobalSkillsDir: filepath.Join(tmpDir, "agents", "skills"),
		DetectInstalled: func(home, configHome, cwd string) bool {
			return false
		},
	}

	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	srcDir := filepath.Join(tmpDir, "my-source-skill")
	writeDummySkill(t, srcDir, "no-agent-skill")

	opts := AddOptions{
		Copy:   true,
		Global: true,
		Yes:    true,
	}

	oldStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("failed to create pipe: %v", pipeErr)
	}
	os.Stdout = w

	err := AddSkill(srcDir, opts, tmpDir)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	var buf bytes.Buffer
	if _, readErr := io.Copy(&buf, r); readErr != nil {
		t.Fatalf("failed to read from pipe: %v", readErr)
	}
	output := buf.String()

	expectedTip := "💡 No active AI agents detected. We highly recommend installing agy-cli: curl -fsSL https://antigravity.google/cli/install.sh | bash"
	if !strings.Contains(output, expectedTip) {
		t.Errorf("expected onboarding tip in stdout, but got %q", output)
	}

	destMd := filepath.Join(tmpDir, ".agents", "skills", "no-agent-skill", "SKILL.md")
	if _, err := os.Stat(destMd); os.IsNotExist(err) {
		t.Fatalf("expected skill to be installed in canonical global skills directory, but it is missing. dest: %s", destMd)
	}
}

// 2. support for all agents but one
func TestIntegration_SupportForAllAgentsButOne(t *testing.T) {
	origAgents := Agents
	defer func() { Agents = origAgents }()

	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	configHome := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "cwd")

	_ = os.MkdirAll(home, 0755)
	_ = os.MkdirAll(configHome, 0755)
	_ = os.MkdirAll(cwd, 0755)

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("VIBE_HOME", "")
	t.Setenv("HERMES_HOME", "")
	t.Setenv("AUTOHAND_HOME", "")
	t.Setenv("GROK_HOME", "")
	t.Setenv("SARVAM_HOME", "")
	t.Setenv("REASONIX_HOME", "")
	RefreshAgents()

	skippedAgent := AgentType("aider-desk")

	for agentType := range Agents {
		if agentType != skippedAgent && agentType != AgentUniversal {
			createAgentMarker(t, agentType, home, configHome, cwd)
		}
	}

	detected := detectInstalledAgents(home, configHome, cwd)
	detectedMap := make(map[AgentType]bool)
	for _, a := range detected {
		detectedMap[a] = true
	}

	if detectedMap[skippedAgent] {
		t.Errorf("expected aider-desk to be missing from detected list, but it was found")
	}

	for _, expectedAgent := range AllAgentTypes {
		if expectedAgent == skippedAgent || expectedAgent == AgentUniversal {
			continue
		}
		if !detectedMap[expectedAgent] {
			t.Errorf("expected agent %q to be detected, but it was missing", expectedAgent)
		}
	}

	srcDir := filepath.Join(tmpDir, "my-skill")
	writeDummySkill(t, srcDir, "all-but-one-skill")

	opts := AddOptions{
		Copy: true,
		Yes:  true,
	}
	err := AddSkill(srcDir, opts, cwd)
	if err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	destMd := filepath.Join(cwd, ".agents", "skills", "all-but-one-skill", "SKILL.md")
	if _, err := os.Stat(destMd); os.IsNotExist(err) {
		t.Fatalf("expected skill to be installed under universal path: %s", destMd)
	}
}

// 3. exactly one agent installed, including cases where the same path might be attributed to multiple agents
func TestIntegration_ExactlyOneAgentInstalled(t *testing.T) {
	t.Run("Singular Roo Agent", func(t *testing.T) {
		origAgents := Agents
		defer func() { Agents = origAgents }()

		tmpDir := t.TempDir()
		home := filepath.Join(tmpDir, "home")
		configHome := filepath.Join(tmpDir, "config")
		cwd := filepath.Join(tmpDir, "cwd")

		_ = os.MkdirAll(home, 0755)
		_ = os.MkdirAll(configHome, 0755)
		_ = os.MkdirAll(cwd, 0755)

		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", configHome)

		Agents = make(map[AgentType]AgentConfig)
		Agents["antigravity"] = origAgents["antigravity"]
		Agents["universal"] = origAgents["universal"]
		Agents["roo"] = origAgents["roo"]

		createAgentMarker(t, "roo", home, configHome, cwd)
		_ = os.MkdirAll(filepath.Join(cwd, ".roo"), 0755)

		detected := detectInstalledAgents(home, configHome, cwd)
		if len(detected) != 1 || detected[0] != "roo" {
			t.Fatalf("expected only 'roo' to be detected, got %v", detected)
		}

		srcDir := filepath.Join(tmpDir, "my-roo-skill")
		writeDummySkill(t, srcDir, "roo-skill")

		opts := AddOptions{
			Copy: false,
			Yes:  true,
		}
		err := AddSkill(srcDir, opts, cwd)
		if err != nil {
			t.Fatalf("AddSkill failed: %v", err)
		}

		rooSkillLink := filepath.Join(cwd, ".roo", "skills", "roo-skill")
		stat, err := os.Lstat(rooSkillLink)
		if err != nil {
			t.Fatalf("failed to locate roo skill directory/link: %v", err)
		}

		if stat.Mode()&os.ModeSymlink == 0 {
			t.Errorf("expected roo skill path to be a symlink, got mode: %v", stat.Mode())
		}
	})

	t.Run("Shared Path Resolution (Antigravity and Gemini CLI)", func(t *testing.T) {
		origAgents := Agents
		defer func() { Agents = origAgents }()

		tmpDir := t.TempDir()
		home := filepath.Join(tmpDir, "home")
		configHome := filepath.Join(tmpDir, "config")
		cwd := filepath.Join(tmpDir, "cwd")

		_ = os.MkdirAll(home, 0755)
		_ = os.MkdirAll(configHome, 0755)
		_ = os.MkdirAll(cwd, 0755)

		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", configHome)

		_ = os.MkdirAll(filepath.Join(home, ".gemini", "antigravity"), 0755)

		srcDir := filepath.Join(tmpDir, "shared-path-skill")
		writeDummySkill(t, srcDir, "shared-skill")

		opts := AddOptions{
			Copy: true,
			Yes:  true,
		}
		err := AddSkill(srcDir, opts, cwd)
		if err != nil {
			t.Fatalf("AddSkill failed: %v", err)
		}

		destMd := filepath.Join(cwd, ".agents", "skills", "shared-skill", "SKILL.md")
		if _, err := os.Stat(destMd); os.IsNotExist(err) {
			t.Fatalf("expected skill to be installed at %s", destMd)
		}
	})
}

// 4. support for env-override agents dynamically resolved without init-time freezing
func TestIntegration_SupportForDynamicEnvAgents(t *testing.T) {
	origAgents := Agents
	defer func() { Agents = origAgents }()

	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	configHome := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "cwd")
	customClaude := filepath.Join(tmpDir, "custom-claude")

	_ = os.MkdirAll(home, 0755)
	_ = os.MkdirAll(configHome, 0755)
	_ = os.MkdirAll(cwd, 0755)
	_ = os.MkdirAll(customClaude, 0755)
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0755)

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("CLAUDE_CONFIG_DIR", customClaude)

	Agents = make(map[AgentType]AgentConfig)
	Agents["claude-code"] = origAgents["claude-code"]
	Agents["antigravity"] = origAgents["antigravity"]

	detected := detectInstalledAgents(home, configHome, cwd)
	if len(detected) != 1 || detected[0] != "claude-code" {
		t.Fatalf("expected claude-code to be detected via dynamic CLAUDE_CONFIG_DIR, got %v", detected)
	}

	srcDir := filepath.Join(tmpDir, "my-claude-skill")
	writeDummySkill(t, srcDir, "claude-skill")

	opts := AddOptions{
		Copy:   false,
		Global: false,
		Yes:    true,
	}
	if err := AddSkill(srcDir, opts, cwd); err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	claudeSkillLink := filepath.Join(cwd, ".claude", "skills", "claude-skill")
	stat, err := os.Lstat(claudeSkillLink)
	if err != nil {
		t.Fatalf("failed to locate claude skill link: %v", err)
	}
	if stat.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected claude skill path to be a symlink, got mode: %v", stat.Mode())
	}
}

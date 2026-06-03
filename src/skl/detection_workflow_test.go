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
	case "augment":
		_ = os.MkdirAll(filepath.Join(home, ".augment"), 0755)
	case "bob":
		_ = os.MkdirAll(filepath.Join(home, ".bob"), 0755)
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
	case "firebender":
		_ = os.MkdirAll(filepath.Join(home, ".firebender"), 0755)
	case "forgecode":
		_ = os.MkdirAll(filepath.Join(home, ".forge"), 0755)
	case "gemini-cli":
		_ = os.MkdirAll(filepath.Join(home, ".gemini"), 0755)
	case "github-copilot":
		_ = os.MkdirAll(filepath.Join(home, ".copilot"), 0755)
	case "goose":
		_ = os.MkdirAll(filepath.Join(configHome, "goose"), 0755)
	case "hermes-agent":
		_ = os.MkdirAll(filepath.Join(home, ".hermes"), 0755)
	case "junie":
		_ = os.MkdirAll(filepath.Join(home, ".junie"), 0755)
	case "iflow-cli":
		_ = os.MkdirAll(filepath.Join(home, ".iflow"), 0755)
	case "kilo":
		_ = os.MkdirAll(filepath.Join(home, ".kilocode"), 0755)
	case "kimi-cli":
		_ = os.MkdirAll(filepath.Join(home, ".kimi"), 0755)
	case "kiro-cli":
		_ = os.MkdirAll(filepath.Join(home, ".kiro"), 0755)
	case "kode":
		_ = os.MkdirAll(filepath.Join(home, ".kode"), 0755)
	case "mcpjam":
		_ = os.MkdirAll(filepath.Join(home, ".mcpjam"), 0755)
	case "mux":
		_ = os.MkdirAll(filepath.Join(home, ".mux"), 0755)
	case "opencode":
		_ = os.MkdirAll(filepath.Join(configHome, "opencode"), 0755)
	case "openhands":
		_ = os.MkdirAll(filepath.Join(home, ".openhands"), 0755)
	case "pi":
		_ = os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0755)
	case "qoder":
		_ = os.MkdirAll(filepath.Join(home, ".qoder"), 0755)
	case "qwen-code":
		_ = os.MkdirAll(filepath.Join(home, ".qwen"), 0755)
	case "replit":
		_ = os.MkdirAll(filepath.Join(cwd, ".replit"), 0755)
	case "rovodev":
		_ = os.MkdirAll(filepath.Join(home, ".rovodev"), 0755)
	case "roo":
		_ = os.MkdirAll(filepath.Join(home, ".roo"), 0755)
	case "tabnine-cli":
		_ = os.MkdirAll(filepath.Join(home, ".tabnine"), 0755)
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
	case "zencoder":
		_ = os.MkdirAll(filepath.Join(home, ".zencoder"), 0755)
	case "neovate":
		_ = os.MkdirAll(filepath.Join(home, ".neovate"), 0755)
	case "pochi":
		_ = os.MkdirAll(filepath.Join(home, ".pochi"), 0755)
	case "adal":
		_ = os.MkdirAll(filepath.Join(home, ".adal"), 0755)
	}
}

// 1. no agents installed: verify that it installs to antigravity and outputs the agy-cli onboarding tip.
func TestIntegration_NoAgents(t *testing.T) {
	origAgents := Agents
	defer func() { Agents = origAgents }()

	tmpDir := t.TempDir()

	// Mock Agents map
	Agents = make(map[AgentType]AgentConfig)
	Agents["antigravity"] = AgentConfig{
		Name:            "antigravity",
		DisplayName:     "Antigravity",
		SkillsDir:       ".agents/skills",
		GlobalSkillsDir: filepath.Join(tmpDir, ".gemini", "antigravity", "skills"),
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

	// Sandbox HOME and XDG_CONFIG_HOME
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create a dummy source skill directory
	srcDir := filepath.Join(tmpDir, "my-source-skill")
	writeDummySkill(t, srcDir, "no-agent-skill")

	// Call AddSkill in copy/global mode
	opts := AddOptions{
		Copy:   true,
		Global: true,
		Yes:    true, // skip interactive check/prompts
	}

	// Capture stdout to verify the onboarding tip is outputted
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

	// Verify the skill was installed in the default mock agent (antigravity) global directory (canonical global skills dir)
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

	skippedAgent := AgentType("aider-desk")
	nonParameterizedAgents := map[AgentType]bool{
		"claude-code":  true,
		"codex":        true,
		"mistral-vibe": true,
		"universal":    true,
	}

	// Create marker paths for all agents except aider-desk and non-parameterized ones
	for agentType := range Agents {
		if agentType != skippedAgent && !nonParameterizedAgents[agentType] {
			createAgentMarker(t, agentType, home, configHome, cwd)
		}
	}

	// Verify detection
	detected := detectInstalledAgents(home, configHome, cwd)
	detectedMap := make(map[AgentType]bool)
	for _, a := range detected {
		detectedMap[a] = true
	}

	if detectedMap[skippedAgent] {
		t.Errorf("expected aider-desk to be missing from detected list, but it was found")
	}

	// Verify that at least a significant number of parameterized agents are detected
	expectedDetectedList := []AgentType{
		"amp", "antigravity", "augment", "bob", "openclaw", "cline",
		"codearts-agent", "codebuddy", "codemaker", "codestudio", "command-code",
		"continue", "cortex", "crush", "cursor", "deepagents", "devin", "dexto",
		"droid", "firebender", "forgecode", "gemini-cli", "github-copilot", "goose",
		"hermes-agent", "junie", "iflow-cli", "kilo", "kimi-cli", "kiro-cli", "kode",
		"mcpjam", "mux", "opencode", "openhands", "pi", "qoder", "qwen-code", "replit",
		"rovodev", "roo", "tabnine-cli", "trae", "trae-cn", "warp", "windsurf", "zed",
		"zencoder", "neovate", "pochi", "adal",
	}

	for _, expectedAgent := range expectedDetectedList {
		if !detectedMap[expectedAgent] {
			t.Errorf("expected agent %q to be detected, but it was missing", expectedAgent)
		}
	}

	// Run AddSkill integration flow
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

	// Since multiple agents are installed, it falls back to universal (.agents/skills)
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

		// Mock Agents map to only look at antigravity, universal, and roo
		Agents = make(map[AgentType]AgentConfig)
		Agents["antigravity"] = origAgents["antigravity"]
		Agents["universal"] = origAgents["universal"]
		Agents["roo"] = origAgents["roo"]

		// Create Roo Code marker in both home and cwd
		createAgentMarker(t, "roo", home, configHome, cwd)
		_ = os.MkdirAll(filepath.Join(cwd, ".roo"), 0755)

		// Verify detection only detects roo
		detected := detectInstalledAgents(home, configHome, cwd)
		if len(detected) != 1 || detected[0] != "roo" {
			t.Fatalf("expected only 'roo' to be detected, got %v", detected)
		}

		// Add skill
		srcDir := filepath.Join(tmpDir, "my-roo-skill")
		writeDummySkill(t, srcDir, "roo-skill")

		opts := AddOptions{
			Copy: false, // Symlink mode
			Yes:  true,
		}
		err := AddSkill(srcDir, opts, cwd)
		if err != nil {
			t.Fatalf("AddSkill failed: %v", err)
		}

		// Verify that roo's skills directory has a symlink pointing to the canonical path
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

		// Create .gemini/antigravity, which triggers both gemini-cli (due to .gemini) and antigravity
		_ = os.MkdirAll(filepath.Join(home, ".gemini", "antigravity"), 0755)

		// Add skill
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

		// Verify it resolves to antigravity (which defaults to installing to global canonical skills dir in copy mode)
		destMd := filepath.Join(cwd, ".agents", "skills", "shared-skill", "SKILL.md")
		if _, err := os.Stat(destMd); os.IsNotExist(err) {
			t.Fatalf("expected skill to be installed at %s", destMd)
		}
	})
}

// 4. support for non-parameterized agents
func TestIntegration_SupportForNonParameterizedAgents(t *testing.T) {
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

	// Mock Agents map
	Agents = make(map[AgentType]AgentConfig)
	
	// Define a mock claude-code (non-parameterized agent) with DetectInstalled checking our sandbox
	Agents["claude-code"] = AgentConfig{
		Name:            "claude-code",
		DisplayName:     "Claude Code",
		SkillsDir:       ".claude/skills",
		GlobalSkillsDir: filepath.Join(home, ".claude", "skills"),
		DetectInstalled: func(h, c, w string) bool {
			_, err := os.Stat(filepath.Join(h, ".claude"))
			return err == nil
		},
	}
	Agents["antigravity"] = AgentConfig{
		Name:            "antigravity",
		DisplayName:     "Antigravity",
		SkillsDir:       ".agents/skills",
		DetectInstalled: func(h, c, w string) bool {
			return false
		},
	}

	// Create marker path for claude-code in both home and cwd
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0755)
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0755)

	// Verify detection
	detected := detectInstalledAgents(home, configHome, cwd)
	if len(detected) != 1 || detected[0] != "claude-code" {
		t.Fatalf("expected claude-code to be detected, got %v", detected)
	}

	// Run AddSkill integration flow
	srcDir := filepath.Join(tmpDir, "my-claude-skill")
	writeDummySkill(t, srcDir, "claude-skill")

	opts := AddOptions{
		Copy:   false, // symlink mode
		Global: false,
		Yes:    true,
	}
	err := AddSkill(srcDir, opts, cwd)
	if err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	// Verify that it is symlinked under claude-code's skills directory
	claudeSkillLink := filepath.Join(cwd, ".claude", "skills", "claude-skill")
	stat, err := os.Lstat(claudeSkillLink)
	if err != nil {
		t.Fatalf("failed to locate claude skill link: %v", err)
	}

	if stat.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected claude skill path to be a symlink, got mode: %v", stat.Mode())
	}
}

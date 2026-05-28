package skl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTypesHelpers(t *testing.T) {
	// 1. Test IsUniversalAgent
	if !IsUniversalAgent(AgentAntigravity) {
		t.Error("expected antigravity to be a universal agent")
	}
	if !IsUniversalAgent(AgentUniversal) {
		t.Error("expected universal to be a universal agent")
	}

	// 2. Test GetUniversalAgents
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

	// 3. Test GetNonUniversalAgents
	nonUnivs := GetNonUniversalAgents()
	if len(nonUnivs) == 0 {
		t.Error("expected at least one non-universal agent")
	}

	// 4. Test DetectInstalledAgents with temporary mocks
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

	// Check initially not detected
	detected := DetectInstalledAgents(tmpDir)
	if len(detected) != 0 {
		t.Errorf("expected no detected agents, got %v", detected)
	}

	// Write marker and check again
	_ = os.WriteFile(filepath.Join(tmpDir, "marker"), []byte("marker"), 0644)
	detected2 := DetectInstalledAgents(tmpDir)
	if len(detected2) != 1 || detected2[0] != "mock-installed" {
		t.Errorf("expected mock-installed detected, got %v", detected2)
	}
}

func TestAllAgentsDetectionCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	for name, agent := range Agents {
		if agent.DetectInstalled != nil {
			// Call the detection logic with dummy sandboxed paths to ensure full coverage
			_ = agent.DetectInstalled(tmpDir, tmpDir, tmpDir)
		}
		if name == "openclaw" {
			// Trigger clawdbot and moltbot helper paths in openclaw
			_ = os.MkdirAll(filepath.Join(tmpDir, ".openclaw"), 0755)
			_ = os.MkdirAll(filepath.Join(tmpDir, ".clawdbot"), 0755)
			_ = os.MkdirAll(filepath.Join(tmpDir, ".moltbot"), 0755)
			_ = agent.DetectInstalled(tmpDir, tmpDir, tmpDir)
		}
	}
}


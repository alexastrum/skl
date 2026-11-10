package skl

import (
	"testing"
)

func mapGetenv(m map[string]string) func(string) string {
	return func(k string) string {
		return m[k]
	}
}

func noFiles(string) bool { return false }

func TestDetectAgent_CursorRefinement(t *testing.T) {
	// CURSOR_TRACE_ID alone (integrated terminal) MUST NOT be treated as an agent
	res := DetectAgentWithEnv(mapGetenv(map[string]string{
		"CURSOR_TRACE_ID": "trace-123",
	}), noFiles)
	if res.IsAgent {
		t.Errorf("expected CURSOR_TRACE_ID alone to return IsAgent=false, got %+v", res)
	}

	// CURSOR_TRACE_ID + CURSOR_AGENT=1 -> cursor-cli
	res2 := DetectAgentWithEnv(mapGetenv(map[string]string{
		"CURSOR_TRACE_ID": "trace-123",
		"CURSOR_AGENT":    "1",
	}), noFiles)
	if !res2.IsAgent || res2.Agent == nil || res2.Agent.Name != "cursor-cli" {
		t.Errorf("expected cursor-cli for CURSOR_AGENT=1, got %+v", res2)
	}

	// CURSOR_TRACE_ID + CURSOR_EXTENSION_HOST_ROLE=agent-exec -> cursor-cli
	res3 := DetectAgentWithEnv(mapGetenv(map[string]string{
		"CURSOR_TRACE_ID":            "trace-123",
		"CURSOR_EXTENSION_HOST_ROLE": "agent-exec",
	}), noFiles)
	if !res3.IsAgent || res3.Agent == nil || res3.Agent.Name != "cursor-cli" {
		t.Errorf("expected cursor-cli for agent-exec role, got %+v", res3)
	}

	// AI_AGENT=cursor without strong Cursor signal -> false
	res4 := DetectAgentWithEnv(mapGetenv(map[string]string{
		"AI_AGENT": "cursor",
	}), noFiles)
	if res4.IsAgent {
		t.Errorf("expected AI_AGENT=cursor without strong signal to return false, got %+v", res4)
	}
}

func TestDetectAgent_AllHeuristicsAndMappings(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		devinExists  bool
		wantIsAgent  bool
		wantName     string
		wantType     AgentType
		wantMappedOk bool
	}{
		{
			name:        "clean environment",
			env:         map[string]string{},
			wantIsAgent: false,
		},
		{
			name:         "Claude Code via CLAUDECODE",
			env:          map[string]string{"CLAUDECODE": "1"},
			wantIsAgent:  true,
			wantName:     "claude",
			wantType:     "claude-code",
			wantMappedOk: true,
		},
		{
			name:         "Claude Cowork via CLAUDE_CODE + CLAUDE_CODE_IS_COWORK",
			env:          map[string]string{"CLAUDE_CODE": "1", "CLAUDE_CODE_IS_COWORK": "1"},
			wantIsAgent:  true,
			wantName:     "cowork",
			wantType:     "claude-code",
			wantMappedOk: true,
		},
		{
			name:         "Codex sandbox",
			env:          map[string]string{"CODEX_SANDBOX": "seatbelt"},
			wantIsAgent:  true,
			wantName:     "codex",
			wantType:     "codex",
			wantMappedOk: true,
		},
		{
			name:         "Gemini CLI",
			env:          map[string]string{"GEMINI_CLI": "1"},
			wantIsAgent:  true,
			wantName:     "gemini",
			wantType:     "gemini-cli",
			wantMappedOk: true,
		},
		{
			name:         "Antigravity via ANTIGRAVITY_AGENT",
			env:          map[string]string{"ANTIGRAVITY_AGENT": "1"},
			wantIsAgent:  true,
			wantName:     "antigravity",
			wantType:     "antigravity",
			wantMappedOk: true,
		},
		{
			name:         "Augment via AUGMENT_AGENT",
			env:          map[string]string{"AUGMENT_AGENT": "1"},
			wantIsAgent:  true,
			wantName:     "augment-cli",
			wantType:     "augment",
			wantMappedOk: true,
		},
		{
			name:         "OpenCode via OPENCODE_CLIENT",
			env:          map[string]string{"OPENCODE_CLIENT": "cli"},
			wantIsAgent:  true,
			wantName:     "opencode",
			wantType:     "opencode",
			wantMappedOk: true,
		},
		{
			name:         "Replit via REPL_ID",
			env:          map[string]string{"REPL_ID": "abc"},
			wantIsAgent:  true,
			wantName:     "replit",
			wantType:     "replit",
			wantMappedOk: true,
		},
		{
			name:         "GitHub Copilot via COPILOT_MODEL",
			env:          map[string]string{"COPILOT_MODEL": "gpt-5"},
			wantIsAgent:  true,
			wantName:     "github-copilot",
			wantType:     "github-copilot",
			wantMappedOk: true,
		},
		{
			name:         "AI_AGENT github-copilot-cli normalized",
			env:          map[string]string{"AI_AGENT": "github-copilot-cli"},
			wantIsAgent:  true,
			wantName:     "github-copilot",
			wantType:     "github-copilot",
			wantMappedOk: true,
		},
		{
			name:         "AI_AGENT custom unmapped",
			env:          map[string]string{"AI_AGENT": "v0"},
			wantIsAgent:  true,
			wantName:     "v0",
			wantType:     "",
			wantMappedOk: false,
		},
		{
			name:         "Devin via /opt/.devin",
			env:          map[string]string{},
			devinExists:  true,
			wantIsAgent:  true,
			wantName:     "devin",
			wantType:     "universal",
			wantMappedOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pathCheck := func(p string) bool {
				return tc.devinExists && p == devinLocalPath
			}
			got := DetectAgentWithEnv(mapGetenv(tc.env), pathCheck)
			if got.IsAgent != tc.wantIsAgent {
				t.Fatalf("IsAgent = %v, want %v (res=%+v)", got.IsAgent, tc.wantIsAgent, got)
			}
			if IsRunningInAgentWithEnv(mapGetenv(tc.env), pathCheck) != tc.wantIsAgent {
				t.Fatalf("IsRunningInAgentWithEnv mismatch")
			}
			if !tc.wantIsAgent {
				return
			}
			if got.Agent == nil || got.Agent.Name != tc.wantName {
				t.Fatalf("Agent.Name = %v, want %q", got.Agent, tc.wantName)
			}
			mapped, ok := GetAgentType(got.Agent.Name)
			if ok != tc.wantMappedOk || mapped != tc.wantType {
				t.Errorf("GetAgentType(%q) = (%q, %v), want (%q, %v)", got.Agent.Name, mapped, ok, tc.wantType, tc.wantMappedOk)
			}
		})
	}
}

func TestDetectAgent_LiveProcessEnvAndCI(t *testing.T) {
	// Scrub all agent detection env vars via t.Setenv
	vars := []string{
		"AI_AGENT", "ANTIGRAVITY_AGENT", "AUGMENT_AGENT",
		"CLAUDE_CODE", "CLAUDE_CODE_IS_COWORK", "CLAUDECODE",
		"CODEX_CI", "CODEX_SANDBOX", "CODEX_THREAD_ID",
		"COPILOT_ALLOW_ALL", "COPILOT_GITHUB_TOKEN", "COPILOT_MODEL",
		"CURSOR_AGENT", "CURSOR_EXTENSION_HOST_ROLE", "CURSOR_TRACE_ID",
		"GEMINI_CLI", "OPENCODE_CLIENT", "REPL_ID",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}

	t.Setenv("GEMINI_CLI", "1")
	if !IsRunningInAgent() {
		t.Error("expected IsRunningInAgent() == true when GEMINI_CLI=1")
	}
	if got := GetDetectedAgentName(); got != "gemini" {
		t.Errorf("GetDetectedAgentName() = %q, want gemini", got)
	}

	if IsCIWithEnv(mapGetenv(map[string]string{})) {
		t.Error("expected IsCIWithEnv false on empty env")
	}
	if !IsCIWithEnv(mapGetenv(map[string]string{"GITHUB_ACTIONS": "true"})) {
		t.Error("expected IsCIWithEnv true when GITHUB_ACTIONS=true")
	}
}

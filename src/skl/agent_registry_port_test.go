package skl

import "testing"

// TestAgentRegistry_PortedFromUpstream locks in the 22 agents ported from
// upstream vercel-labs/skills' agents.ts (mgoodness/skl#6), and the rename
// of the stale "kimi-cli" slug to upstream's real "kimi-code-cli" key.
//
// Seam: the public Agents map and its AgentConfig fields (SkillsDir,
// GlobalSkillsDir, DetectInstalled) — the same seam TestAllAgentsDetectionCoverage
// already tests at, and what every other command (add/list/remove/update)
// consumes.
func TestAgentRegistry_PortedFromUpstream(t *testing.T) {
	t.Run("22 new agents exist with upstream's exact directory config", func(t *testing.T) {
		cases := []struct {
			key             string
			displayName     string
			skillsDir       string
			globalSkillsDir string
		}{
			{"antigravity-cli", "Antigravity CLI", ".agents/skills", ""},
			{"astrbot", "AstrBot", "data/skills", ""},
			{"autohand-code", "Autohand Code CLI", ".autohand/skills", ""},
			{"fx", "fx", ".fx/skills", ""},
			{"grok", "Grok Build", ".grok/skills", ""},
			{"inference-sh", "inference.sh", ".inferencesh/skills", ""},
			{"jazz", "Jazz", ".jazz/skills", ""},
			{"kimchi", "Kimchi", ".kimchi/skills", ""},
			{"lingma", "Lingma", ".lingma/skills", ""},
			{"loaf", "Loaf", ".agents/skills", ""},
			{"minimax-code", "MiniMax Code", ".minimax/skills", ""},
			{"moxby", "Moxby", ".moxby/skills", ""},
			{"ona", "Ona", ".ona/skills", ""},
			{"posit-assistant", "Posit Assistant", ".posit/assistant/skills", ""},
			{"promptscript", "PromptScript", ".agents/skills", ""}, // deliberately no global support, per upstream's `globalSkillsDir: undefined`
			{"qoder-cn", "Qoder CN", ".qoder/skills", ""},
			{"reasonix", "Reasonix", ".reasonix/skills", ""},
			{"sarvam-code", "Sarvam Code", ".agents/skills", ""},
			{"terramind", "Terramind", ".terramind/skills", ""},
			{"tinycloud", "Tinycloud", ".tinycloud/skills", ""},
			{"zcode", "ZCode", ".zcode/skills", ""},
			{"zenflow", "Zenflow", ".zencoder/skills", ""},
		}

		for _, tc := range cases {
			cfg, ok := Agents[AgentType(tc.key)]
			if !ok {
				t.Errorf("expected agent %q to exist in Agents map, but it was missing", tc.key)
				continue
			}
			if cfg.Name != tc.key {
				t.Errorf("agent %q: Name = %q, want %q", tc.key, cfg.Name, tc.key)
			}
			if cfg.DisplayName != tc.displayName {
				t.Errorf("agent %q: DisplayName = %q, want %q", tc.key, cfg.DisplayName, tc.displayName)
			}
			if cfg.SkillsDir != tc.skillsDir {
				t.Errorf("agent %q: SkillsDir = %q, want %q", tc.key, cfg.SkillsDir, tc.skillsDir)
			}
			// promptscript deliberately has no global support upstream (globalSkillsDir: undefined);
			// every other ported agent must have a non-empty global path.
			if tc.key != "promptscript" && cfg.GlobalSkillsDir == "" {
				t.Errorf("agent %q: GlobalSkillsDir is empty, want a non-empty path", tc.key)
			}
			if tc.key == "promptscript" && cfg.GlobalSkillsDir != "" {
				t.Errorf("agent %q: GlobalSkillsDir = %q, want empty (no global install support upstream)", tc.key, cfg.GlobalSkillsDir)
			}
			if cfg.DetectInstalled == nil {
				t.Errorf("agent %q: DetectInstalled is nil", tc.key)
			}
		}
	})

	t.Run("kimi-code-cli replaces the stale kimi-cli slug", func(t *testing.T) {
		if _, ok := Agents["kimi-cli"]; ok {
			t.Errorf("expected stale slug %q to be removed from Agents map, but it still exists", "kimi-cli")
		}

		cfg, ok := Agents["kimi-code-cli"]
		if !ok {
			t.Fatalf("expected agent %q to exist in Agents map, but it was missing", "kimi-code-cli")
		}
		if cfg.Name != "kimi-code-cli" {
			t.Errorf("kimi-code-cli: Name = %q, want %q", cfg.Name, "kimi-code-cli")
		}
		if cfg.DisplayName != "Kimi Code CLI" {
			t.Errorf("kimi-code-cli: DisplayName = %q, want %q", cfg.DisplayName, "Kimi Code CLI")
		}
		if cfg.SkillsDir != ".agents/skills" {
			t.Errorf("kimi-code-cli: SkillsDir = %q, want %q", cfg.SkillsDir, ".agents/skills")
		}
	})

	t.Run("eve is deliberately excluded (tracked separately in #8)", func(t *testing.T) {
		if _, ok := Agents["eve"]; ok {
			t.Errorf("expected agent %q to be absent (out of scope for this ticket, see mgoodness/skl#8), but it was found", "eve")
		}
	})
}

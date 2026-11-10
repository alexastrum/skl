package skl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// lcHermetic isolates HOME/XDG/agent/CI environment variables and rebuilds the
// Agents registry against the temporary home. It returns the temporary home.
func lcHermetic(t *testing.T) string {
	t.Helper()
	// Registered first so it runs last, after t.Setenv restores the environment.
	t.Cleanup(RefreshAgents)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	for _, k := range []string{
		"CODEX_HOME", "CLAUDE_CONFIG_DIR", "VIBE_HOME", "HERMES_HOME", "AUTOHAND_HOME",
		"GROK_HOME", "SARVAM_HOME", "REASONIX_HOME", "APPDATA", "FLATPAK_XDG_CONFIG_HOME",
		// Agent detection.
		"AI_AGENT", "CURSOR_TRACE_ID", "CURSOR_AGENT", "CURSOR_EXTENSION_HOST_ROLE", "GEMINI_CLI",
		"CODEX_SANDBOX", "CODEX_CI", "CODEX_THREAD_ID", "ANTIGRAVITY_AGENT",
		"AUGMENT_AGENT", "OPENCODE_CLIENT", "CLAUDECODE", "CLAUDE_CODE", "CLAUDE_CODE_IS_COWORK",
		"REPL_ID", "COPILOT_MODEL", "COPILOT_ALLOW_ALL", "COPILOT_GITHUB_TOKEN",
		// CI detection.
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "TRAVIS", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION",
		"SKILLS_API_URL", "INSTALL_INTERNAL_SKILLS", "GH_HOST", "GITHUB_TOKEN", "GH_TOKEN",
	} {
		t.Setenv(k, "")
	}
	RefreshAgents()
	return home
}

// lcWriteSkill writes dir/SKILL.md with the given name and description.
func lcWriteSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lcWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lcWriteLocalLock(t *testing.T, cwd string, skills map[string]LocalSkillLockEntry) {
	t.Helper()
	if err := WriteLocalLock(LocalSkillLockFile{Version: LocalLockVer, Skills: skills}, cwd); err != nil {
		t.Fatal(err)
	}
}

func lcSkillNames(skills []InstalledSkill) []string {
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	return names
}

func lcRunList(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runList(args, cwd, &out, &errb)
	return code, out.String(), errb.String()
}

func TestParseListOptions(t *testing.T) {
	tests := []struct {
		args []string
		want ListOptions
	}{
		{nil, ListOptions{}},
		{[]string{"-g", "--json"}, ListOptions{Global: true, JSON: true}},
		{[]string{"-a", "claude-code", "cursor", "-g"}, ListOptions{Global: true, AgentFilter: []AgentType{"claude-code", "cursor"}}},
		{[]string{"--agent", "claude-code,cursor"}, ListOptions{AgentFilter: []AgentType{"claude-code", "cursor"}}},
		{[]string{"-a", "a1", "-a", "a2"}, ListOptions{AgentFilter: []AgentType{"a1", "a2"}}},
		{[]string{"-g", "-p"}, ListOptions{}},
	}
	for _, tt := range tests {
		if got := ParseListOptions(tt.args); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseListOptions(%v) = %+v, want %+v", tt.args, got, tt.want)
		}
	}
}

func TestListInstalledSkills_Empty(t *testing.T) {
	lcHermetic(t)
	skills, err := ListInstalledSkills(ListOptions{Cwd: t.TempDir()})
	if err != nil || len(skills) != 0 {
		t.Fatalf("got %v, %v; want no skills", skills, err)
	}
}

func TestListInstalledSkills_Project(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "project-skill"), "project-skill", "A mock project skill")
	if err := os.MkdirAll(filepath.Join(cwd, ".agents", "skills", "ignored-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Missing description: not a valid installed skill.
	lcWriteFile(t, filepath.Join(cwd, ".agents", "skills", "no-desc", "SKILL.md"), "---\nname: no-desc\n---\n")

	skills, err := ListInstalledSkills(ListOptions{Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %v", lcSkillNames(skills))
	}
	got := skills[0]
	if got.Name != "project-skill" || got.Description != "A mock project skill" || got.Scope != "project" {
		t.Errorf("unexpected skill %+v", got)
	}
	if got.Agents == nil {
		t.Error("Agents must be a non-nil slice")
	}
}

func TestListInstalledSkills_Global(t *testing.T) {
	home := lcHermetic(t)
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "global-skill"), "global-skill", "A mock global skill")
	skills, err := ListInstalledSkills(ListOptions{Global: true, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "global-skill" || skills[0].Scope != "global" {
		t.Fatalf("unexpected global skills %+v", skills)
	}
}

func TestListInstalledSkills_Filter(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "test-skill"), "test-skill", "A mock skill")

	skills, err := ListInstalledSkills(ListOptions{Cwd: cwd, AgentFilter: []AgentType{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || !containsAgent(skills[0].Agents, "codex") {
		t.Fatalf("expected test-skill attributed to codex, got %+v", skills)
	}

	other, err := ListInstalledSkills(ListOptions{Cwd: cwd, AgentFilter: []AgentType{"non-existent-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("expected 0 skills for unknown agent filter, got %v", other)
	}
}

func TestListInstalledSkills_AgentsMapping(t *testing.T) {
	home := lcHermetic(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "multi-agent-skill"), "multi-agent-skill", "A skill mapped to agents")

	skills, err := ListInstalledSkills(ListOptions{Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %v", lcSkillNames(skills))
	}
	if !containsAgent(skills[0].Agents, "codex") {
		t.Errorf("expected codex (universal, detected) in %v", skills[0].Agents)
	}
	if containsAgent(skills[0].Agents, AgentUniversal) {
		t.Errorf("synthetic universal agent must not be listed: %v", skills[0].Agents)
	}
}

// #2597: skills installed into an undetected agent's folder are listed, and the
// fallback scan honors --agent.
func TestListInstalledSkills_UndetectedAgentFallbackHonorsFilter(t *testing.T) {
	for _, global := range []bool{false, true} {
		home := lcHermetic(t)
		cwd := t.TempDir()
		base := cwd
		if global {
			base = home
		}
		lcWriteSkill(t, filepath.Join(base, ".claude", "skills", "claude-only"), "claude-only", "d")
		lcWriteSkill(t, filepath.Join(base, ".augment", "skills", "augment-only"), "augment-only", "d")

		all, err := ListInstalledSkills(ListOptions{Global: global, Cwd: cwd})
		if err != nil {
			t.Fatal(err)
		}
		names := strings.Join(lcSkillNames(all), ",")
		if !strings.Contains(names, "claude-only") || !strings.Contains(names, "augment-only") {
			t.Errorf("global=%v: expected both copy-mode skills listed, got %s", global, names)
		}

		filtered, err := ListInstalledSkills(ListOptions{Global: global, Cwd: cwd, AgentFilter: []AgentType{"claude-code"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := lcSkillNames(filtered); !reflect.DeepEqual(got, []string{"claude-only"}) {
			t.Errorf("global=%v: -a claude-code leaked other agents' folders: %v", global, got)
		}
		if !containsAgent(filtered[0].Agents, "claude-code") {
			t.Errorf("expected claude-code attribution, got %v", filtered[0].Agents)
		}
	}
}

func TestListInstalledSkills_Symlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	lcHermetic(t)
	cwd := t.TempDir()
	target := filepath.Join(t.TempDir(), "real-skill")
	lcWriteSkill(t, target, "linked-skill", "d")
	skillsDir := filepath.Join(cwd, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(skillsDir, "linked-skill")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cwd, "missing"), filepath.Join(skillsDir, "dangling")); err != nil {
		t.Fatal(err)
	}
	lcWriteFile(t, filepath.Join(cwd, "file.txt"), "x")
	if err := os.Symlink(filepath.Join(cwd, "file.txt"), filepath.Join(skillsDir, "file-link")); err != nil {
		t.Fatal(err)
	}
	skills, err := ListInstalledSkills(ListOptions{Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if got := lcSkillNames(skills); !reflect.DeepEqual(got, []string{"linked-skill"}) {
		t.Errorf("got %v, want [linked-skill]", got)
	}
}

func TestRunList_InvalidAgent(t *testing.T) {
	lcHermetic(t)
	code, out, _ := lcRunList(t, t.TempDir(), "-a", "not-an-agent")
	if code != 1 || !strings.Contains(out, "[SKILLS_INVALID_AGENTS] Invalid agents: not-an-agent") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// #2602: JSON output reports tracked, untracked and missing skills.
func TestRunList_JSONStatus(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "tracked"), "tracked", "d")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "loose"), "loose", "d")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "ce-review"), "ce-review", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"tracked":   {Source: "owner/repo", SourceType: "github", ComputedHash: "h"},
		"ce:review": {Source: "owner/plugins", SourceType: "github", ComputedHash: "h"},
		"gone":      {Source: "owner/gone", SourceType: "github", ComputedHash: "h"},
	})

	code, out, _ := lcRunList(t, cwd, "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("JSON output must not contain ANSI escapes")
	}
	var entries []ListJSONEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	status := map[string]string{}
	for _, e := range entries {
		status[e.Name] = e.Status
		if e.Agents == nil {
			t.Errorf("%s: agents must be [] not null", e.Name)
		}
	}
	want := map[string]string{"tracked": "tracked", "loose": "untracked", "ce-review": "tracked", "gone": "missing"}
	if !reflect.DeepEqual(status, want) {
		t.Errorf("status = %v, want %v", status, want)
	}
	if !strings.Contains(out, `"path": null`) {
		t.Error("missing skill must have null path")
	}

	// An agent filter hides on-disk skills, so missing entries are not reported.
	_, out, _ = lcRunList(t, cwd, "--json", "-a", "codex")
	if strings.Contains(out, `"missing"`) {
		t.Errorf("missing skills must be suppressed with -a: %s", out)
	}
}

func TestRunList_HumanOutput(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "alpha"), "alpha", "Alpha description text")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "beta"), "beta", "d")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "gamma"), "gamma", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"alpha":   {Source: "owner/\nFORGED\x1b[31m", SourceType: "github", ComputedHash: "h"},
		"beta":    {Source: "owner/beta", SourceType: "github", ComputedHash: "h"},
		"missing": {Source: "owner/missing", SourceType: "github", ComputedHash: "h"},
	})
	code, out, _ := lcRunList(t, cwd)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Project Skills", "owner/ FORGED", "untracked", "not linked", "Missing", "(in the lock file, not on disk)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Alpha description text") {
		t.Error("human list output must not print descriptions")
	}
	if strings.Contains(out, "\x1b[31m") {
		t.Error("untrusted source escapes must be stripped")
	}
	if strings.Contains(out, "owner/\n") {
		t.Error("untrusted source must stay on one line")
	}
}

func TestRunList_GlobalPluginGrouping(t *testing.T) {
	home := lcHermetic(t)
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "alpha"), "alpha", "d")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "beta"), "beta", "d")
	if err := WriteSkillLock(SkillLockFile{Version: GlobalLockVer, Skills: map[string]SkillLockEntry{
		"alpha": {Source: "owner/repo", SourceType: "github", PluginName: "my-plugin"},
	}}); err != nil {
		t.Fatal(err)
	}
	code, out, _ := lcRunList(t, t.TempDir(), "-g")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	out = StripTerminalEscapes(out)
	for _, want := range []string{"Global Skills", "My Plugin", "General", "  alpha", "  beta", "~"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "My Plugin") > strings.Index(out, "General") {
		t.Error("plugin groups print before General")
	}
}

func TestRunList_EmptyMessages(t *testing.T) {
	lcHermetic(t)
	_, out, _ := lcRunList(t, t.TempDir())
	if !strings.Contains(out, "No project skills found.") || !strings.Contains(out, "Try listing global skills with -g") {
		t.Errorf("unexpected: %q", out)
	}
	_, out, _ = lcRunList(t, t.TempDir(), "-g")
	if !strings.Contains(out, "No global skills found.") {
		t.Errorf("unexpected: %q", out)
	}
	_, out, _ = lcRunList(t, t.TempDir(), "--json")
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("want [], got %q", out)
	}
}

func TestRunList_InvalidLocalLockWarns(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "alpha"), "alpha", "d")
	lcWriteFile(t, filepath.Join(cwd, "skills-lock.json"), "{not json")
	code, out, errOut := lcRunList(t, cwd)
	if code != 0 {
		t.Fatalf("list must degrade gracefully, got exit %d", code)
	}
	if !strings.Contains(errOut, "Warning:") || !strings.Contains(errOut, "SKILLS_INVALID_LOCAL_LOCK") {
		t.Errorf("expected warning on stderr, got %q", errOut)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected alpha listed: %q", out)
	}
}

func TestShortenPathAndFormatList(t *testing.T) {
	home := lcHermetic(t)
	if got := ShortenPath(filepath.Join(home, "x"), "/nowhere"); got != "~"+string(filepath.Separator)+"x" {
		t.Errorf("ShortenPath home = %q", got)
	}
	cwd := t.TempDir()
	if got := ShortenPath(filepath.Join(cwd, "y"), cwd); got != "."+string(filepath.Separator)+"y" && !strings.HasPrefix(got, "~") {
		t.Errorf("ShortenPath cwd = %q", got)
	}
	if got := formatList([]string{"a", "b", "c", "d", "e", "f", "g"}, 5); got != "a, b, c, d, e +2 more" {
		t.Errorf("formatList = %q", got)
	}
}

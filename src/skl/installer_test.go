package skl

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// instTestEnv points HOME and the XDG/agent env vars at a temp dir, rebuilds Agents from
// it, and restores the original Agents map afterwards. It returns (home, cwd).
func instTestEnv(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "project")
	for _, d := range []string{home, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := Agents
	t.Cleanup(func() { Agents = orig })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "VIBE_HOME", "HERMES_HOME", "AUTOHAND_HOME", "GROK_HOME", "SARVAM_HOME", "REASONIX_HOME"} {
		t.Setenv(k, "")
	}
	RefreshAgents()
	return home, cwd
}

func instWriteSkill(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test skill " + name + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func instSkipNoSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
}

func instSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}

func instExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestCopySkillDirectory_SymlinkConfinement(t *testing.T) {
	instSkipNoSymlinks(t)
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	_ = os.MkdirAll(outside, 0o755)
	_ = os.WriteFile(filepath.Join(outside, "secret.env"), []byte("TOKEN=1"), 0o600)

	skill := instWriteSkill(t, filepath.Join(root, "skill"), "confined")
	_ = os.MkdirAll(filepath.Join(skill, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(skill, "docs", "guide.md"), []byte("guide"), 0o644)
	// Escaping file and directory links.
	instSymlink(t, filepath.Join(outside, "secret.env"), filepath.Join(skill, "leak.env"))
	instSymlink(t, outside, filepath.Join(skill, "leakdir"))
	// Internal alias whose own child escapes: the nested link must be checked too.
	instSymlink(t, filepath.Join("..", "..", "outside", "secret.env"), filepath.Join(skill, "docs", "nested-leak.env"))
	instSymlink(t, "docs", filepath.Join(skill, "alias"))
	// Cycle back to the root and a broken link.
	instSymlink(t, ".", filepath.Join(skill, "docs", "loop"))
	instSymlink(t, "missing.md", filepath.Join(skill, "broken.md"))
	// Legitimate intra-skill file link is dereferenced.
	instSymlink(t, filepath.Join("docs", "guide.md"), filepath.Join(skill, "guide-link.md"))

	dest := filepath.Join(root, "dest")
	if err := CopySkillDirectory(skill, dest, "", false); err != nil {
		t.Fatalf("CopySkillDirectory: %v", err)
	}

	for _, p := range []string{"leak.env", "leakdir", "docs/nested-leak.env", "alias/nested-leak.env", "broken.md"} {
		if instExists(filepath.Join(dest, filepath.FromSlash(p))) {
			t.Errorf("%s must not be materialized", p)
		}
	}
	for _, p := range []string{"SKILL.md", "docs/guide.md", "alias/guide.md", "guide-link.md"} {
		info, err := os.Lstat(filepath.Join(dest, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("%s missing: %v", p, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s should be a real file, got a symlink", p)
		}
	}
	// docs/loop -> . points at the root, which is an ancestor of docs: the cycle is cut
	// after one level (root is already on the branch).
	if instExists(filepath.Join(dest, "docs", "loop", "docs", "loop")) {
		t.Errorf("directory cycle was not cut")
	}
	got, _ := os.ReadFile(filepath.Join(dest, "guide-link.md"))
	if string(got) != "guide" {
		t.Errorf("guide-link.md = %q, want dereferenced contents", got)
	}
}

func TestCopySkillDirectory_ModesAndExclusions(t *testing.T) {
	root := t.TempDir()
	skill := instWriteSkill(t, filepath.Join(root, "skill"), "modes")
	_ = os.MkdirAll(filepath.Join(skill, "scripts"), 0o755)
	_ = os.WriteFile(filepath.Join(skill, "scripts", "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755)
	_ = os.WriteFile(filepath.Join(skill, ".prettierrc"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(skill, "metadata.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(skill, "scripts", "metadata.json"), []byte("{}"), 0o644)
	for _, d := range []string{".git", "__pycache__", "__pypackages__"} {
		_ = os.MkdirAll(filepath.Join(skill, d), 0o755)
		_ = os.WriteFile(filepath.Join(skill, d, "x"), []byte("x"), 0o644)
	}

	dest := filepath.Join(root, "dest")
	if err := CopySkillDirectory(skill, dest, "", false); err != nil {
		t.Fatal(err)
	}
	if !instExists(filepath.Join(dest, ".prettierrc")) {
		t.Error("dotfiles must be preserved")
	}
	for _, p := range []string{"metadata.json", "scripts/metadata.json", ".git", "__pycache__", "__pypackages__"} {
		if instExists(filepath.Join(dest, filepath.FromSlash(p))) {
			t.Errorf("%s must be excluded", p)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dest, "scripts", "run.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("run.sh lost its executable bit: %v", info.Mode())
		}
	}
}

func TestInstallSkillForAgent_AntigravityGlobal(t *testing.T) {
	instSkipNoSymlinks(t)
	home, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "src"), "agy-skill")
	gemini := filepath.Join(home, ".gemini", "config", "skills", "agy-skill")
	canonical := filepath.Join(home, ".agents", "skills", "agy-skill")

	// Sole target: copy straight into ~/.gemini/config/skills (#2566).
	res := InstallSkillForAgent(Skill{Name: "agy-skill", Path: src}, AgentAntigravity, InstallOptions{Global: true, Cwd: cwd, Mode: InstallModeCopy})
	if !res.Success || res.Path != gemini {
		t.Fatalf("copy result = %+v, want path %s", res, gemini)
	}
	if info, err := os.Lstat(gemini); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected real dir at %s (err=%v)", gemini, err)
	}
	if instExists(canonical) {
		t.Errorf("copy mode must not write the canonical dir")
	}

	// Alongside other agents: canonical copy + relative link from the Antigravity dir.
	_ = os.RemoveAll(gemini)
	res = InstallSkillForAgent(Skill{Name: "agy-skill", Path: src}, AgentAntigravity, InstallOptions{Global: true, Cwd: cwd, Mode: InstallModeSymlink})
	if !res.Success || res.CanonicalPath != canonical || res.Path != gemini {
		t.Fatalf("symlink result = %+v", res)
	}
	target, err := os.Readlink(gemini)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", gemini, err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("link target should be relative, got %s", target)
	}
	want := filepath.Join("..", "..", "..", ".agents", "skills", "agy-skill")
	if target != want {
		t.Errorf("link target = %s, want %s", target, want)
	}
}

func TestInstallSkillForAgent_GlobalCanonicalReaderGetsNoExtraLink(t *testing.T) {
	home, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "src"), "cur-skill")
	res := InstallSkillForAgent(Skill{Name: "cur-skill", Path: src}, "cursor", InstallOptions{Global: true, Cwd: cwd})
	canonical := filepath.Join(home, ".agents", "skills", "cur-skill")
	if !res.Success || res.Path != canonical || res.CanonicalPath != canonical {
		t.Fatalf("result = %+v", res)
	}
	if instExists(filepath.Join(home, ".cursor", "skills", "cur-skill")) {
		t.Errorf("no redundant link expected in ~/.cursor/skills")
	}
}

func TestInstallSkillForAgent_ProjectAgentRoots(t *testing.T) {
	instSkipNoSymlinks(t)
	_, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "src"), "proj-skill")
	skill := Skill{Name: "proj-skill", Path: src}

	res := InstallSkillForAgent(skill, "windsurf", InstallOptions{Cwd: cwd})
	if !res.Success || !res.Skipped || res.SkipReason != SkipReasonMissingAgentProjectDirectory {
		t.Fatalf("windsurf without .windsurf: %+v", res)
	}
	if instExists(filepath.Join(cwd, ".windsurf")) {
		t.Error(".windsurf must not be created")
	}

	res = InstallSkillForAgent(skill, "windsurf", InstallOptions{Cwd: cwd, CreateMissingAgentRoot: true})
	if !res.Success || res.Skipped {
		t.Fatalf("explicit windsurf: %+v", res)
	}
	if info, err := os.Lstat(filepath.Join(cwd, ".windsurf", "skills", "proj-skill")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected windsurf symlink (err=%v)", err)
	}

	// claude-code creates its project skills dir by default.
	res = InstallSkillForAgent(skill, "claude-code", InstallOptions{Cwd: cwd})
	if !res.Success || res.Skipped {
		t.Fatalf("claude-code: %+v", res)
	}
	link := filepath.Join(cwd, ".claude", "skills", "proj-skill")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected claude link: %v", err)
	}
	if target != filepath.Join("..", "..", ".agents", "skills", "proj-skill") {
		t.Errorf("claude link target = %s", target)
	}
}

func TestInstallSkillForAgent_ParentSymlinkKeepsCanonical(t *testing.T) {
	instSkipNoSymlinks(t)
	_, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "src"), "parent-skill")
	_ = os.MkdirAll(filepath.Join(cwd, ".agents", "skills"), 0o755)
	_ = os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755)
	instSymlink(t, filepath.Join("..", ".agents", "skills"), filepath.Join(cwd, ".claude", "skills"))

	res := InstallSkillForAgent(Skill{Name: "parent-skill", Path: src}, "claude-code", InstallOptions{Cwd: cwd})
	if !res.Success {
		t.Fatalf("result: %+v", res)
	}
	md := filepath.Join(cwd, ".agents", "skills", "parent-skill", "SKILL.md")
	if !instExists(md) {
		t.Fatalf("canonical skill was deleted (#293)")
	}
	info, err := os.Lstat(filepath.Join(cwd, ".agents", "skills", "parent-skill"))
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("canonical dir must stay a real directory (err=%v)", err)
	}
}

func TestInstallSkillForAgent_SourceOverlapIsSkipped(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(cwd, "skills", "overlap"), "overlap")
	res := InstallSkillForAgent(Skill{Name: "overlap", Path: src}, "openclaw", InstallOptions{Cwd: cwd, Mode: InstallModeCopy})
	if !res.Success || !res.Skipped {
		t.Fatalf("result: %+v", res)
	}
	if !instExists(filepath.Join(src, "SKILL.md")) {
		t.Fatal("source skill was deleted")
	}
}

func TestInstallSkillForAgent_GlobalUnsupported(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "src"), "ps")
	res := InstallSkillForAgent(Skill{Name: "ps", Path: src}, "promptscript", InstallOptions{Global: true, Cwd: cwd})
	if res.Success || res.Error != "PromptScript does not support global skill installation" {
		t.Fatalf("result: %+v", res)
	}
}

func TestInstallSkillForAgent_EveStripsFrontmatter(t *testing.T) {
	_, cwd := instTestEnv(t)
	dir := filepath.Join(t.TempDir(), "src")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: eve-skill\ndescription: d\nallowed-tools: Bash\nversion: 2\n---\n\nBody\n"), 0o644)
	res := InstallSkillForAgent(Skill{Name: "eve-skill", Path: dir}, "eve", InstallOptions{Cwd: cwd, Mode: InstallModeCopy})
	if !res.Success {
		t.Fatalf("result: %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, "agent", "skills", "eve-skill", "SKILL.md"))
	want := "---\nname: eve-skill\ndescription: d\nversion: 2\n---\nBody\n"
	if string(got) != want {
		t.Errorf("eve SKILL.md = %q, want %q", got, want)
	}
}

func TestInstallBlobAndWellKnownSkill(t *testing.T) {
	_, cwd := instTestEnv(t)
	files := []InstallFile{
		{Path: "SKILL.md", Contents: []byte("---\nname: blob\ndescription: d\n---\n")},
		{Path: "refs/a.md", Contents: []byte("a")},
		{Path: "metadata.json", Contents: []byte("{}")},
		{Path: "../escape.md", Contents: []byte("x")},
	}
	res := InstallBlobSkillForAgent("blob", files, "codex", InstallOptions{Cwd: cwd})
	if !res.Success {
		t.Fatalf("blob: %+v", res)
	}
	dir := filepath.Join(cwd, ".agents", "skills", "blob")
	if !instExists(filepath.Join(dir, "refs", "a.md")) || instExists(filepath.Join(dir, "metadata.json")) {
		t.Errorf("blob files not written with exclusions")
	}
	if instExists(filepath.Join(cwd, ".agents", "skills", "escape.md")) {
		t.Errorf("path traversal entry was written")
	}

	res = InstallWellKnownSkillForAgent("wk", files[:3], "codex", InstallOptions{Cwd: cwd, Mode: InstallModeCopy})
	if !res.Success || !instExists(filepath.Join(cwd, ".agents", "skills", "wk", "metadata.json")) {
		t.Errorf("well-known install should write all files: %+v", res)
	}
}

func TestGetAgentBaseDirAndPaths(t *testing.T) {
	home, cwd := instTestEnv(t)
	cases := []struct {
		agent  AgentType
		global bool
		want   string
	}{
		{AgentAntigravity, false, filepath.Join(cwd, ".agents", "skills")},
		{AgentAntigravity, true, filepath.Join(home, ".gemini", "config", "skills")},
		{"cursor", true, filepath.Join(home, ".agents", "skills")},
		{"claude-code", false, filepath.Join(cwd, ".claude", "skills")},
		{"claude-code", true, filepath.Join(home, ".claude", "skills")},
		{AgentUniversal, true, filepath.Join(home, ".agents", "skills")},
	}
	for _, c := range cases {
		if got := GetAgentBaseDir(c.agent, c.global, cwd, ""); got != c.want {
			t.Errorf("GetAgentBaseDir(%s, %v) = %s, want %s", c.agent, c.global, got, c.want)
		}
	}
	if got := GetAgentBaseDir("eve", false, cwd, "research"); got != filepath.Join(cwd, "agent", "subagents", "research", "skills") {
		t.Errorf("eve subagent dir = %s", got)
	}
	if got := GetInstallPath("My Skill", "claude-code", false, cwd); got != filepath.Join(cwd, ".claude", "skills", "my-skill") {
		t.Errorf("GetInstallPath = %s", got)
	}
	if got := GetCanonicalPath("../evil", true, cwd); got != filepath.Join(home, ".agents", "skills", "evil") {
		t.Errorf("GetCanonicalPath = %s", got)
	}
	_ = os.MkdirAll(filepath.Join(cwd, ".claude", "skills", "here"), 0o755)
	if !IsSkillInstalled("here", "claude-code", false, cwd, "") || IsSkillInstalled("nope", "claude-code", false, cwd, "") {
		t.Errorf("IsSkillInstalled mismatch")
	}
	if IsSkillInstalled("here", "promptscript", true, cwd, "") {
		t.Errorf("agents without a global dir never report global installs")
	}
}

func TestSkipAgentsWithoutGlobalDir(t *testing.T) {
	instTestEnv(t)
	targets := []AgentType{"claude-code", "promptscript", "eve", "cursor"}
	var sb strings.Builder
	got := SkipAgentsWithoutGlobalDir(targets, true, false, &sb)
	if len(got) != 2 || got[0] != "claude-code" || got[1] != "cursor" {
		t.Errorf("got %v", got)
	}
	if !strings.Contains(sb.String(), "Skipping PromptScript, Eve: no global skills directory") {
		t.Errorf("notice = %q", sb.String())
	}
	if got := SkipAgentsWithoutGlobalDir(targets, true, true, io.Discard); len(got) != 4 {
		t.Errorf("explicit agents must be kept: %v", got)
	}
	if got := SkipAgentsWithoutGlobalDir(targets, false, false, io.Discard); len(got) != 4 {
		t.Errorf("project installs must be untouched: %v", got)
	}
	only := []AgentType{"promptscript"}
	if got := SkipAgentsWithoutGlobalDir(only, true, false, io.Discard); len(got) != 1 {
		t.Errorf("all-unsupported selection must be kept to report the error: %v", got)
	}
}

func TestDefaultInstallModeAndTargets(t *testing.T) {
	instTestEnv(t)
	if m := DefaultInstallMode(BuildInstallTargets([]AgentType{AgentAntigravity}, nil)); m != InstallModeCopy {
		t.Errorf("single agent: %s", m)
	}
	if m := DefaultInstallMode(BuildInstallTargets([]AgentType{AgentAntigravity, "codex"}, nil)); m != InstallModeCopy {
		t.Errorf("same skills dir: %s", m)
	}
	if m := DefaultInstallMode(BuildInstallTargets([]AgentType{AgentAntigravity, "claude-code"}, nil)); m != InstallModeSymlink {
		t.Errorf("two dirs: %s", m)
	}
	targets := BuildInstallTargets([]AgentType{"eve", "codex"}, []string{"root", "research"})
	if len(targets) != 3 || targets[0].EveSubagent != "" || targets[1].EveSubagent != "research" || targets[2].Agent != "codex" {
		t.Errorf("targets = %+v", targets)
	}
	if m := DefaultInstallMode(BuildInstallTargets([]AgentType{"eve"}, []string{"root", "research"})); m != InstallModeCopy {
		t.Errorf("all eve: %s", m)
	}
	uni, sym := SplitAgentsByType([]AgentType{AgentAntigravity, "cursor", "claude-code"}, true)
	if len(uni) != 1 || uni[0] != "cursor" || len(sym) != 2 {
		t.Errorf("global split: %v %v", uni, sym)
	}
	uni, sym = SplitAgentsByType([]AgentType{AgentAntigravity, "cursor", "claude-code"}, false)
	if len(uni) != 2 || len(sym) != 1 {
		t.Errorf("project split: %v %v", uni, sym)
	}
}

func TestLinkSkillToAgents(t *testing.T) {
	instSkipNoSymlinks(t)
	_, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "src"), "linked")
	results, err := LinkSkillToAgents(Skill{Name: "linked", Path: src}, []AgentType{"codex", "claude-code"}, InstallOptions{Cwd: cwd})
	if err != nil || len(results) != 2 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if !instExists(filepath.Join(cwd, ".agents", "skills", "linked", "SKILL.md")) || !instExists(filepath.Join(cwd, ".claude", "skills", "linked")) {
		t.Error("expected canonical copy and claude link")
	}
	_, err = LinkSkillToAgents(Skill{Name: "linked", Path: src}, []AgentType{"promptscript"}, InstallOptions{Cwd: cwd, Global: true})
	if err == nil || !strings.Contains(err.Error(), "does not support global") {
		t.Errorf("expected aggregated error, got %v", err)
	}
}

// Review #41 item 3b: when the canonical dir is itself a link (link mode), the agent link
// must point at the canonical dir, not at the source it resolves to.
func TestInstallSkillForAgent_LinkModeAgentLinkTargetsCanonical(t *testing.T) {
	instSkipNoSymlinks(t)
	_, cwd := instTestEnv(t)
	src := instWriteSkill(t, filepath.Join(t.TempDir(), "node_modules", "pkg"), "linked-src")
	res := InstallSkillForAgent(Skill{Name: "linked-src", Path: src}, "claude-code", InstallOptions{Cwd: cwd, Mode: InstallModeLink})
	if !res.Success {
		t.Fatalf("result: %+v", res)
	}
	target, err := os.Readlink(filepath.Join(cwd, ".claude", "skills", "linked-src"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("..", "..", ".agents", "skills", "linked-src"); target != want {
		t.Errorf("agent link target = %s, want %s", target, want)
	}
	if !instExists(filepath.Join(cwd, ".claude", "skills", "linked-src", "SKILL.md")) {
		t.Error("agent link must resolve to the skill")
	}
}

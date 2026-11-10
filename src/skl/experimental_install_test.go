package skl

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func lcRunInstall(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runInstallFromLock(args, cwd, &out, &errb)
	return code, StripTerminalEscapes(out.String()), StripTerminalEscapes(errb.String())
}

// lcLocalSource creates a source dir holding one skill and returns it.
func lcLocalSource(t *testing.T, name, desc string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	lcWriteSkill(t, dir, name, desc)
	return dir
}

func TestRestoreFromLock(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	src := lcLocalSource(t, "dummy-skill", "A dummy test skill")
	lcWriteFile(t, filepath.Join(cwd, "skills-lock.json"), `{
  "version": 1,
  "skills": {
    "dummy-skill": {"source": "`+filepath.ToSlash(src)+`", "sourceType": "local", "computedHash": "somehash"}
  }
}`)
	if err := RestoreFromLock(nil, cwd); err != nil {
		t.Fatal(err)
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "dummy-skill", "SKILL.md")) {
		t.Error("skill not restored into .agents/skills")
	}
}

func TestResolveRestoreAgents(t *testing.T) {
	home := lcHermetic(t)
	cwd := t.TempDir()
	got, err := ResolveRestoreAgents(nil, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got {
		if !IsUniversalAgent(a) {
			t.Errorf("with nothing detected only universal agents are used, got %s", a)
		}
	}
	if err := SaveSelectedAgents([]string{"claude-code", "bogus"}); err != nil {
		t.Fatal(err)
	}
	got, _ = ResolveRestoreAgents(nil, cwd)
	if !containsAgent(got, "claude-code") || containsAgent(got, "bogus") {
		t.Errorf("last selected agents not honored: %v", got)
	}
	if _, err := ResolveRestoreAgents([]AgentType{"nope"}, cwd); !IsDiagnostic(err, CodeInvalidAgents) {
		t.Errorf("expected invalid agents diagnostic, got %v", err)
	}
	all, _ := ResolveRestoreAgents([]AgentType{"*"}, cwd)
	if len(all) < 50 {
		t.Errorf("'*' should select every agent, got %d", len(all))
	}
	_ = home
}

// #2608: --dry-run prints the plan and writes nothing.
func TestRunInstallFromLock_DryRun(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	a := lcLocalSource(t, "alpha", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"alpha": {Source: a, SourceType: "local", ComputedHash: "h", InstallMode: "copy"},
	})
	before := lcReadFile(t, filepath.Join(cwd, "skills-lock.json"))
	code, out, errOut := lcRunInstall(t, cwd, "--dry-run", "-a", "claude-code")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"Would restore 1 skill from skills-lock.json into .agents/skills/", "Would link to Claude Code", "alpha from", "(copy)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if lcExists(filepath.Join(cwd, ".agents")) || lcExists(filepath.Join(cwd, ".claude")) {
		t.Error("dry run must not write files")
	}
	if lcReadFile(t, filepath.Join(cwd, "skills-lock.json")) != before {
		t.Error("dry run must not touch the lock")
	}
}

// #2603, #2681: installed skills are not overwritten (without --force), but get the
// agent links they lack, honoring installMode "copy".
func TestRunInstallFromLock_LinksAlreadyInstalled(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	src := lcLocalSource(t, "alpha", "upstream")
	canonical := filepath.Join(cwd, ".agents", "skills", "alpha")
	lcWriteSkill(t, canonical, "alpha", "locally edited")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "beta"), "beta", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"alpha": {Source: src, SourceType: "local", ComputedHash: "h", InstallMode: "copy"},
		"beta":  {Source: lcLocalSource(t, "beta", "upstream"), SourceType: "local", ComputedHash: "h"},
	})
	code, out, errOut := lcRunInstall(t, cwd, "-a", "claude-code")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "2 skills already installed (use --force to reinstall)") || !strings.Contains(out, "Linking to Claude Code") {
		t.Errorf("unexpected output %s", out)
	}
	if !strings.Contains(lcReadFile(t, filepath.Join(canonical, "SKILL.md")), "locally edited") {
		t.Error("already-installed skill must not be overwritten")
	}
	fi, err := os.Lstat(filepath.Join(cwd, ".claude", "skills", "alpha"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Errorf("copy-mode link should be a real dir: %v %v", fi, err)
	}
	fi, err = os.Lstat(filepath.Join(cwd, ".claude", "skills", "beta"))
	if err != nil || (runtime.GOOS != "windows" && fi.Mode()&os.ModeSymlink == 0) {
		t.Errorf("default link should be a symlink: %v %v", fi, err)
	}

	// --force reinstalls from the source.
	if code, _, errOut := lcRunInstall(t, cwd, "--force", "--no-frozen-lockfile"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(lcReadFile(t, filepath.Join(canonical, "SKILL.md")), "upstream") {
		t.Error("--force should reinstall from source")
	}
}

// #2680: a failing source only fails its own entries; the rest are restored and
// failed lock entries are left untouched.
func TestRunInstallFromLock_ContinuesPastFailures(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	good := lcLocalSource(t, "good", "d")
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"good":      {Source: good, SourceType: "local", ComputedHash: "h1"},
		"renamed":   {Source: good, SourceType: "local", ComputedHash: "h2"},
		"broken":    {Source: missingDir, SourceType: "local", ComputedHash: "h3"},
		"ambiguous": {Source: "acme/skills", SourceType: "git", ComputedHash: "h4"},
	})
	code, _, errOut := lcRunInstall(t, cwd, "--no-frozen-lockfile")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, errOut)
	}
	for _, want := range []string{
		"[SKILLS_LOCK_MISSING_SOURCE_URL] Cannot restore ambiguous",
		"renamed not found in",
		"Failed to restore broken from",
		"Restored 1 skill; failed to restore 3 skills from skills-lock.json: ambiguous",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q in:\n%s", want, errOut)
		}
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "good", "SKILL.md")) {
		t.Error("good skill should be restored")
	}
	lock, _ := ReadLocalLock(cwd)
	if lock.Skills["broken"].ComputedHash != "h3" || lock.Skills["ambiguous"].ComputedHash != "h4" || lock.Skills["renamed"].ComputedHash != "h2" {
		t.Errorf("failed entries must keep their lock entries: %+v", lock.Skills)
	}
}

// #2559: --frozen-lockfile (default in CI) restores matching skills without
// rewriting skills-lock.json and refuses drift without installing.
func TestRunInstallFromLock_FrozenLockfile(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	if code, _, errOut := lcRunInstall(t, cwd, "--frozen-lockfile"); code != 1 || !strings.Contains(errOut, "absent or has no skills") {
		t.Fatalf("absent lock: code=%d %s", code, errOut)
	}
	if code, out, _ := lcRunInstall(t, cwd); code != 0 || !strings.Contains(out, "No project skills found in skills-lock.json") {
		t.Fatalf("absent lock without frozen: code=%d %s", code, out)
	}

	src := lcLocalSource(t, "alpha", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"alpha": {Source: src, SourceType: "local", ComputedHash: "stale"}})
	before := lcReadFile(t, filepath.Join(cwd, "skills-lock.json"))
	t.Setenv("CI", "true")
	code, _, errOut := lcRunInstall(t, cwd)
	if code != 1 || !strings.Contains(errOut, "alpha has changed since skills-lock.json was written") {
		t.Fatalf("drift: code=%d %s", code, errOut)
	}
	if lcExists(filepath.Join(cwd, ".agents", "skills", "alpha")) || lcReadFile(t, filepath.Join(cwd, "skills-lock.json")) != before {
		t.Fatal("frozen restore must not install or rewrite the lock on drift")
	}

	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"alpha": {Source: src, SourceType: "local", ComputedHash: lcHash(t, src)}})
	before = lcReadFile(t, filepath.Join(cwd, "skills-lock.json"))
	if code, _, errOut := lcRunInstall(t, cwd); code != 0 {
		t.Fatalf("matching frozen restore: %d %s", code, errOut)
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "alpha", "SKILL.md")) {
		t.Error("matching skill should be restored")
	}
	if lcReadFile(t, filepath.Join(cwd, "skills-lock.json")) != before {
		t.Error("frozen restore must leave skills-lock.json byte-identical")
	}

	// --no-frozen-lockfile overrides CI.
	if err := os.RemoveAll(filepath.Join(cwd, ".agents")); err != nil {
		t.Fatal(err)
	}
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"alpha": {Source: src, SourceType: "local", ComputedHash: "stale"}})
	if code, _, errOut := lcRunInstall(t, cwd, "--no-frozen-lockfile"); code != 0 {
		t.Fatalf("--no-frozen-lockfile: %d %s", code, errOut)
	}
	if lock, _ := ReadLocalLock(cwd); lock.Skills["alpha"].ComputedHash != lcHash(t, src) {
		t.Error("non-frozen restore should refresh computedHash")
	}
}

func TestRunInstallFromLock_InvalidAgentAndLock(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	src := lcLocalSource(t, "alpha", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"alpha": {Source: src, SourceType: "local", ComputedHash: "h"}})
	if code, _, errOut := lcRunInstall(t, cwd, "-a", "bogus"); code != 1 || !strings.Contains(errOut, "SKILLS_INVALID_AGENTS") {
		t.Errorf("code=%d %s", code, errOut)
	}
	if lcExists(filepath.Join(cwd, ".agents")) {
		t.Error("invalid agents must not touch disk")
	}
	lcWriteFile(t, filepath.Join(cwd, "skills-lock.json"), "{")
	if code, _, errOut := lcRunInstall(t, cwd); code != 1 || !strings.Contains(errOut, "SKILLS_INVALID_LOCAL_LOCK") {
		t.Errorf("code=%d %s", code, errOut)
	}
}

func TestRunInstallFromLock_NodeModulesViaSync(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"p": "1"}, nil)
	pkgDir := filepath.Join(cwd, "node_modules", "p")
	lcWritePkg(t, pkgDir, map[string]interface{}{"name": "p"})
	lcWriteSkill(t, pkgDir, "s", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"s": {Source: "p", SourceType: "node_modules", SkillPath: "SKILL.md", ComputedHash: lcHash(t, pkgDir)}})
	code, out, errOut := lcRunInstall(t, cwd, "--frozen-lockfile")
	if code != 0 || !strings.Contains(out, "1 skill from node_modules") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "s", "SKILL.md")) {
		t.Error("node_modules skill should be synced")
	}
}

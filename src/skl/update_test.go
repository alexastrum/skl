package skl

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateSkill_NotExist(t *testing.T) {
	tmpDir := t.TempDir()

	err := UpdateSkill("ghost-skill", "", UpdateOptions{
		Cwd:    tmpDir,
		Global: false,
	})
	if err == nil {
		t.Fatal("expected error updating non-existent skill, got nil")
	}

	if !strings.Contains(err.Error(), "is not installed") {
		t.Errorf("expected error message to mention 'is not installed', got: %v", err)
	}
}

func TestUpdateSkill_RefreshInPlace(t *testing.T) {
	tmpDir := t.TempDir()

	// Install a mock skill first
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: test-skill
description: Old description
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Update in place (simulate integrity checking / refresh)
	err := UpdateSkill("test-skill", "", UpdateOptions{
		Cwd:    tmpDir,
		Global: false,
	})
	if err != nil {
		t.Fatalf("UpdateSkill refresh failed: %v", err)
	}

	bytes, err := os.ReadFile(skillMd)
	if err != nil {
		t.Fatalf("failed to read refreshed SKILL.md: %v", err)
	}

	if string(bytes) != content {
		t.Errorf("expected skill content to be unchanged, got %q", string(bytes))
	}
}

func TestUpdateSkill_FromSource(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Install mock skill
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "my-upgradeable-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	oldSkillMd := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(oldSkillMd, []byte("old content"), 0644); err != nil {
		t.Fatalf("failed to write old SKILL.md: %v", err)
	}

	// 2. Create updated source directory
	srcDir := filepath.Join(tmpDir, "new-source-dir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create new source dir: %v", err)
	}
	newContent := `---
name: my-upgradeable-skill
description: Newly updated description
---
# Newly updated!
`
	newSkillMd := filepath.Join(srcDir, "SKILL.md")
	if err := os.WriteFile(newSkillMd, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to write updated SKILL.md: %v", err)
	}

	// 3. Update skill from source
	err := UpdateSkill("my-upgradeable-skill", srcDir, UpdateOptions{
		Cwd:    tmpDir,
		Global: false,
	})
	if err != nil {
		t.Fatalf("UpdateSkill from source failed: %v", err)
	}

	// 4. Verify updated file contents
	bytes, err := os.ReadFile(oldSkillMd)
	if err != nil {
		t.Fatalf("failed to read updated SKILL.md: %v", err)
	}

	if !reflect.DeepEqual(bytes, []byte(newContent)) {
		t.Errorf("updated file content differs from source content")
	}
}

func TestUpdateSkill_Global(t *testing.T) {
	tmpDir := lcHermetic(t)

	// Create a global mock skill in ~/.agents/skills/global-skill
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "global-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create global skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: global-skill
description: Global description
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write global SKILL.md: %v", err)
	}

	err := UpdateSkill("global-skill", "", UpdateOptions{
		Global: true,
	})
	if err != nil {
		t.Fatalf("global UpdateSkill failed: %v", err)
	}
}

func TestUpdateSkill_Errors(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Skill name is required
	err := UpdateSkill("", "", UpdateOptions{Cwd: tmpDir})
	if err == nil {
		t.Error("expected error for empty skill name, got nil")
	}

	// Install a mock skill first so we pass the "is installed" check for subsequent tests
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "test-skill")
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("SKILL.md"), 0644)

	// 2. Source directory does not exist
	err = UpdateSkill("test-skill", "/non/existent/src/dir", UpdateOptions{Cwd: tmpDir})
	if err == nil {
		t.Error("expected error for non-existent source directory, got nil")
	}

	// 3. Source directory is missing SKILL.md
	emptySrcDir := filepath.Join(tmpDir, "empty-src")
	_ = os.MkdirAll(emptySrcDir, 0755)
	err = UpdateSkill("test-skill", emptySrcDir, UpdateOptions{Cwd: tmpDir})
	if err == nil {
		t.Error("expected error for missing SKILL.md in source directory, got nil")
	}
}

// lcFetchedFromDir builds a FetchedSource from the SKILL.md files under root.
func lcFetchedFromDir(t *testing.T, root string) *FetchedSource {
	t.Helper()
	f := &FetchedSource{RootDir: root}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		sk, perr := ParseSkillMd(p, true)
		if perr != nil || sk == nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		f.Skills = append(f.Skills, FetchedSkill{Skill: *sk, SkillPath: filepath.ToSlash(rel)})
		return nil
	})
	return f
}

// lcStubUpdateFetch replaces updateFetch; sources maps request Source -> root dir.
func lcStubUpdateFetch(t *testing.T, sources map[string]string) *[]SourceRequest {
	t.Helper()
	var reqs []SourceRequest
	orig := updateFetch
	updateFetch = func(req SourceRequest) (*FetchedSource, error) {
		reqs = append(reqs, req)
		root, ok := sources[req.Source]
		if !ok {
			return nil, os.ErrNotExist
		}
		return lcFetchedFromDir(t, root), nil
	}
	t.Cleanup(func() { updateFetch = orig })
	return &reqs
}

func lcRunUpdate(t *testing.T, cwd string, mode UpdateMode, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runUpdate(args, mode, cwd, &out, &errb, strings.NewReader(""))
	return code, StripTerminalEscapes(out.String()), errb.String()
}

func lcHash(t *testing.T, dir string) string {
	t.Helper()
	h, err := ComputeSkillFolderHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func lcReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lcProjectFixture installs skill "foo" from a fake git source into a project.
// It returns cwd, the upstream root and the upstream skill dir.
func lcProjectFixture(t *testing.T, installMode string) (string, string, string) {
	t.Helper()
	home := lcHermetic(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	upstream := t.TempDir()
	upSkill := filepath.Join(upstream, "skills", "foo")
	lcWriteSkill(t, upSkill, "foo", "v1")
	installed := filepath.Join(cwd, ".agents", "skills", "foo")
	lcWriteSkill(t, installed, "foo", "v1")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"foo": {
		Source: "acme/skills", SourceUrl: "https://git.example.com/acme/skills.git", SourceType: "git",
		SkillPath: "skills/foo/SKILL.md", ComputedHash: lcHash(t, upSkill), InstallMode: installMode,
	}})
	lcStubUpdateFetch(t, map[string]string{"https://git.example.com/acme/skills.git": upstream})
	return cwd, upstream, upSkill
}

func TestParseUpdateOptions(t *testing.T) {
	got := ParseUpdateOptions([]string{"-g", "-p", "-y", "-v", "foo", "bar", "--unknown"})
	want := UpdateCheckOptions{Global: true, Project: true, Yes: true, Verbose: true, Skills: []string{"foo", "bar"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestHasProjectSkillsAndScope(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".agents", "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if HasProjectSkills(cwd) {
		t.Error("a folder without SKILL.md is not a project skill")
	}
	opts := UpdateCheckOptions{Yes: true, Cwd: cwd}
	if s, _ := ResolveUpdateScope(&opts); s != UpdateScopeGlobal {
		t.Errorf("scope = %s, want global", s)
	}
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "x"), "x", "d")
	if !HasProjectSkills(cwd) {
		t.Error("expected project skills")
	}
	if s, _ := ResolveUpdateScope(&opts); s != UpdateScopeProject {
		t.Errorf("scope = %s, want project", s)
	}
	for _, tc := range []struct {
		opts UpdateCheckOptions
		want UpdateScope
	}{
		{UpdateCheckOptions{Global: true, Project: true}, UpdateScopeBoth},
		{UpdateCheckOptions{Global: true}, UpdateScopeGlobal},
		{UpdateCheckOptions{Project: true}, UpdateScopeProject},
		{UpdateCheckOptions{Skills: []string{"a"}}, UpdateScopeBoth},
		{UpdateCheckOptions{Skills: []string{"a"}, Global: true}, UpdateScopeGlobal},
	} {
		if got, _ := ResolveUpdateScope(&tc.opts); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.opts, got, tc.want)
		}
	}
}

// #2650: agents without a global skills dir are dropped from global updates.
func TestWithGlobalSkillsDir(t *testing.T) {
	lcHermetic(t)
	got := WithGlobalSkillsDir([]AgentType{"promptscript", "claude-code"})
	if !reflect.DeepEqual(got, []AgentType{"claude-code"}) {
		t.Errorf("got %v", got)
	}
	if got := WithGlobalSkillsDir([]AgentType{"promptscript"}); !reflect.DeepEqual(got, []AgentType{"promptscript"}) {
		t.Errorf("all-unsupported list must be returned unchanged, got %v", got)
	}
}

// #2605: check reports updates without touching disk or the lock, and exits 1.
func TestRunCheck_ReportsWithoutInstalling(t *testing.T) {
	cwd, _, upSkill := lcProjectFixture(t, "")
	code, out, _ := lcRunUpdate(t, cwd, UpdateModeCheck)
	if code != 0 || !strings.Contains(out, "All project skills are up to date") {
		t.Fatalf("current skill: code=%d out=%s", code, out)
	}

	lcWriteSkill(t, upSkill, "foo", "v2")
	lockBefore := lcReadFile(t, filepath.Join(cwd, "skills-lock.json"))
	code, out, _ = lcRunUpdate(t, cwd, UpdateModeCheck)
	if code != 1 || !strings.Contains(out, "↑ foo has an update") || !strings.Contains(out, "1 skill(s) can be updated; run skl update to install") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Contains(out, "Updated foo") {
		t.Error("check must not install")
	}
	if got := lcReadFile(t, filepath.Join(cwd, ".agents", "skills", "foo", "SKILL.md")); !strings.Contains(got, "v1") {
		t.Error("check must not modify installed files")
	}
	if lcReadFile(t, filepath.Join(cwd, "skills-lock.json")) != lockBefore {
		t.Error("check must not modify skills-lock.json")
	}
}

func TestRunUpdate_InstallsNewVersionAndRecordsHash(t *testing.T) {
	cwd, _, upSkill := lcProjectFixture(t, "")
	lcWriteSkill(t, upSkill, "foo", "v2")
	code, out, _ := lcRunUpdate(t, cwd, UpdateModeUpdate, "-y")
	if code != 0 || !strings.Contains(out, "Updated foo") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if got := lcReadFile(t, filepath.Join(cwd, ".agents", "skills", "foo", "SKILL.md")); !strings.Contains(got, "v2") {
		t.Errorf("canonical copy not updated: %s", got)
	}
	lock, _ := ReadLocalLock(cwd)
	if lock.Skills["foo"].ComputedHash != lcHash(t, upSkill) {
		t.Error("lock computedHash not updated")
	}
	// Default mode symlinks the Claude Code copy to the canonical dir.
	if fi, err := os.Lstat(filepath.Join(cwd, ".claude", "skills", "foo")); runtime.GOOS != "windows" && (err != nil || fi.Mode()&os.ModeSymlink == 0) {
		t.Errorf("expected .claude/skills/foo to be a symlink, got %v %v", fi, err)
	}
}

// #2601: a locked skill whose folder was deleted is reinstalled even when the
// upstream hash is unchanged.
func TestRunUpdate_ReinstallsMissingSkill(t *testing.T) {
	cwd, _, _ := lcProjectFixture(t, "")
	if err := os.RemoveAll(filepath.Join(cwd, ".agents", "skills", "foo")); err != nil {
		t.Fatal(err)
	}
	code, out, _ := lcRunUpdate(t, cwd, UpdateModeCheck)
	if code != 1 || !strings.Contains(out, "↑ foo has an update") {
		t.Fatalf("check: code=%d out=%s", code, out)
	}
	code, out, _ = lcRunUpdate(t, cwd, UpdateModeUpdate, "-y")
	if code != 0 || !strings.Contains(out, "Updated foo") {
		t.Fatalf("update: code=%d out=%s", code, out)
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "foo", "SKILL.md")) {
		t.Error("missing skill was not reinstalled")
	}
}

// #2607: copy installs stay real directories after update.
func TestRunUpdate_PreservesCopyInstallMode(t *testing.T) {
	cwd, _, upSkill := lcProjectFixture(t, "copy")
	claudeDir := filepath.Join(cwd, ".claude", "skills", "foo")
	lcWriteSkill(t, claudeDir, "foo", "v1")
	lcWriteSkill(t, upSkill, "foo", "v2")
	code, out, _ := lcRunUpdate(t, cwd, UpdateModeUpdate, "-y")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	fi, err := os.Lstat(claudeDir)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("expected a real directory at %s, got %v %v", claudeDir, fi, err)
	}
	if !strings.Contains(lcReadFile(t, filepath.Join(claudeDir, "SKILL.md")), "v2") {
		t.Error("copy was not refreshed")
	}
	if lock, _ := ReadLocalLock(cwd); lock.Skills["foo"].InstallMode != "copy" {
		t.Error("installMode copy must be preserved in the lock")
	}
}

func TestRunUpdate_GenericGitShorthandWithoutSourceURLFails(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"foo": {Source: "acme/skills", SourceType: "git", SkillPath: "skills/foo/SKILL.md", ComputedHash: "h"}})
	reqs := lcStubUpdateFetch(t, nil)
	code, out, _ := lcRunUpdate(t, cwd, UpdateModeUpdate, "-y")
	if code != 1 || !strings.Contains(out, "skills-lock.json is missing sourceUrl for this generic Git source") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(*reqs) != 0 {
		t.Errorf("must not fetch an ambiguous shorthand, got %v", *reqs)
	}
}

func TestRunUpdate_InvalidLocalLock(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteFile(t, filepath.Join(cwd, "skills-lock.json"), `{"version": 99, "skills": {}}`)
	code, _, errOut := lcRunUpdate(t, cwd, UpdateModeUpdate, "-p")
	if code != 1 || !strings.Contains(errOut, "SKILLS_INVALID_LOCAL_LOCK") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

// #2682: direct downloads are re-downloaded and hash-checked; a skill that is no
// longer in the download fails the run.
func TestRunUpdate_DirectDownload(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	dl := t.TempDir()
	lcWriteSkill(t, dl, "dl-skill", "v1")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "dl-skill"), "dl-skill", "v1")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"dl-skill": {Source: "https://example.com/SKILL.md", SourceUrl: "https://example.com/SKILL.md", SourceType: "download", SkillPath: "SKILL.md", ComputedHash: lcHash(t, dl)},
		"gone":     {Source: "https://example.com/pack.tgz", SourceUrl: "https://example.com/pack.tgz", SourceType: "download", SkillPath: "gone/SKILL.md", ComputedHash: "h"},
	})
	empty := t.TempDir()
	lcStubUpdateFetch(t, map[string]string{"https://example.com/SKILL.md": dl, "https://example.com/pack.tgz": empty})

	code, out, _ := lcRunUpdate(t, cwd, UpdateModeCheck)
	if code != 1 || !strings.Contains(out, "gone is no longer in this download") || strings.Contains(out, "dl-skill has an update") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	lcWriteSkill(t, dl, "dl-skill", "v2")
	_, out, _ = lcRunUpdate(t, cwd, UpdateModeCheck)
	if !strings.Contains(out, "↑ dl-skill has an update") {
		t.Errorf("expected dl-skill update, got %s", out)
	}
}

// #2682: global git checks group by (source, ref), pin github.com, and compare
// git tree hashes for GitHub entries.
func TestRunUpdate_GlobalGroupsBySourceAndRef(t *testing.T) {
	home := lcHermetic(t)
	t.Setenv("GH_HOST", "github.example.com")
	v1, v2 := t.TempDir(), t.TempDir()
	lcWriteSkill(t, filepath.Join(v1, "skills", "a"), "a", "one")
	lcWriteSkill(t, filepath.Join(v2, "skills", "b"), "b", "two")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "a"), "a", "one")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "b"), "b", "two")
	ha, _ := ComputeGitTreeHash(filepath.Join(v1, "skills", "a"))
	if err := WriteSkillLock(SkillLockFile{Version: GlobalLockVer, Skills: map[string]SkillLockEntry{
		"a": {Source: "owner/repo", SourceType: "github", SourceUrl: "https://github.com/owner/repo.git", Ref: "v1", SkillPath: "skills/a/SKILL.md", SkillFolderHash: ha},
		"b": {Source: "owner/repo", SourceType: "github", SourceUrl: "https://github.com/owner/repo.git", Ref: "v2", SkillPath: "skills/b/SKILL.md", SkillFolderHash: strings.Repeat("0", 40)},
	}}); err != nil {
		t.Fatal(err)
	}
	reqs := lcStubUpdateFetch(t, map[string]string{
		"https://github.com/owner/repo.git#v1": v1,
		"https://github.com/owner/repo.git#v2": v2,
	})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "↑ b has an update") || strings.Contains(out, "↑ a has an update") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Contains(out, "deleted upstream") {
		t.Errorf("different refs must not be reported as deleted: %s", out)
	}
	if len(*reqs) != 2 {
		t.Fatalf("expected one fetch per (source, ref), got %d", len(*reqs))
	}
	for _, r := range *reqs {
		if r.GitHubHost != "github.com" {
			t.Errorf("GitHub updates must pin github.com, got %q", r.GitHubHost)
		}
	}
}

func TestRunUpdate_GlobalSkippedAndEmpty(t *testing.T) {
	lcHermetic(t)
	lcStubUpdateFetch(t, nil)
	_, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g")
	if !strings.Contains(out, "No global skills tracked in lock file.") {
		t.Errorf("unexpected %s", out)
	}
	if err := AddSkillToLock("loc", SkillLockEntry{Source: "/some/path", SourceType: "local"}); err != nil {
		t.Fatal(err)
	}
	_, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g")
	if !strings.Contains(out, "1 skill(s) cannot be checked automatically") || !strings.Contains(out, "(Local path)") {
		t.Errorf("unexpected %s", out)
	}
}

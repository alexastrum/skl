package skl

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
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
// Update checks fetch concurrently, so the recorded requests are guarded.
func lcStubUpdateFetch(t *testing.T, sources map[string]string) *[]SourceRequest {
	t.Helper()
	var mu sync.Mutex
	var reqs []SourceRequest
	orig := updateFetch
	updateFetch = func(req SourceRequest) (*FetchedSource, error) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
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
// git tree hashes for GitHub entries. The Trees API is down here, so every
// group falls back to one clone.
func TestRunUpdate_GlobalGroupsBySourceAndRef(t *testing.T) {
	home := lcHermetic(t)
	t.Setenv("GH_HOST", "github.example.com")
	api := lcFakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
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
	if got := api.paths(); len(got) != 2 || !strings.Contains(out, "GitHub API unavailable; checking via Git clone") {
		t.Errorf("expected one Trees API call per group, then a clone; calls=%v out=%s", got, out)
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

// Review #41 item 5: a crafted name must never make UpdateSkill delete outside
// the canonical skills dir.
func TestUpdateSkill_RejectsTraversalName(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	victim := filepath.Join(cwd, "victim")
	lcWriteSkill(t, victim, "victim", "must survive")
	src := lcLocalSource(t, "replacement", "d")
	for _, name := range []string{"../../victim", "../victim", "..", "."} {
		if err := UpdateSkill(name, src, UpdateOptions{Cwd: cwd}); err == nil {
			t.Errorf("UpdateSkill(%q) should fail", name)
		}
	}
	if !lcExists(filepath.Join(victim, "SKILL.md")) {
		t.Fatal("UpdateSkill deleted a directory outside the canonical skills dir")
	}
}

// lcAPIRequest is one request the fake GitHub API received.
type lcAPIRequest struct{ uri, auth, accept, userAgent string }

// lcFakeAPI records the requests made to a fake GitHub REST API.
type lcFakeAPI struct {
	mu   sync.Mutex
	reqs []lcAPIRequest
}

func (a *lcFakeAPI) all() []lcAPIRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]lcAPIRequest(nil), a.reqs...)
}

func (a *lcFakeAPI) paths() []string {
	var out []string
	for _, r := range a.all() {
		out = append(out, r.uri)
	}
	return out
}

// lcFakeGitHubAPI points the Trees API at an httptest server running handler.
// It also hides the GitHub CLI, so a failed API call can only fall back to a
// clone.
func lcFakeGitHubAPI(t *testing.T, handler http.HandlerFunc) *lcFakeAPI {
	t.Helper()
	api := &lcFakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		api.reqs = append(api.reqs, lcAPIRequest{r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Accept"), r.Header.Get("User-Agent")})
		api.mu.Unlock()
		handler(w, r)
	}))
	oldAPI, oldGh := githubAPIBaseOverride, fpGhBinary
	githubAPIBaseOverride = srv.URL
	fpGhBinary = filepath.Join(t.TempDir(), "no-such-gh")
	ResetRepoTreeAuthState()
	t.Cleanup(func() {
		srv.Close()
		githubAPIBaseOverride, fpGhBinary = oldAPI, oldGh
		ResetRepoTreeAuthState()
	})
	return api
}

// lcTreeJSON is a git/trees?recursive=1 response body.
func lcTreeJSON(t *testing.T, sha string, truncated bool, entries ...RepoTreeEntry) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{"sha": sha, "tree": entries, "truncated": truncated})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lcSkillTree is the tree entries of a skill folder holding only SKILL.md.
func lcSkillTree(folder, sha string) []RepoTreeEntry {
	return []RepoTreeEntry{
		{Path: folder, Type: "tree", SHA: sha},
		{Path: folder + "/SKILL.md", Type: "blob", SHA: strings.Repeat("f", 40)},
	}
}

// lcServeTrees answers /repos/<owner>/<repo>/git/trees/<branch>?recursive=1
// from trees keyed "<owner>/<repo>@<branch>", and 404 for anything else.
func lcServeTrees(trees map[string][]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/repos/")
		i := strings.Index(p, "/git/trees/")
		if i < 0 || r.URL.Query().Get("recursive") != "1" {
			http.NotFound(w, r)
			return
		}
		body, ok := trees[p[:i]+"@"+p[i+len("/git/trees/"):]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func lcWriteGlobalLock(t *testing.T, skills map[string]SkillLockEntry) {
	t.Helper()
	if err := WriteSkillLock(SkillLockFile{Version: GlobalLockVer, Skills: skills}); err != nil {
		t.Fatal(err)
	}
}

func lcSHA(c string) string { return strings.Repeat(c, 40) }

func lcGitHubEntry(repo, ref, skillPath, hash string) SkillLockEntry {
	return SkillLockEntry{Source: repo, SourceType: "github", SourceUrl: "https://github.com/" + repo + ".git", Ref: ref, SkillPath: skillPath, SkillFolderHash: hash}
}

// Global GitHub checks use one Trees API call per repo (not a clone) and compare
// each skill's folder SHA with skillFolderHash; update reinstalls only the
// changed skill, fetched by its own subpath, and records the new folder SHA.
func TestRunUpdate_GlobalTreesAPIOneCallPerRepo(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, filepath.Join(up, "skills", "foo"), "foo", "v1")
	lcWriteSkill(t, filepath.Join(up, "skills", "bar"), "bar", "v2")
	for _, n := range []string{"foo", "bar"} {
		lcWriteSkill(t, filepath.Join(home, ".agents", "skills", n), n, "v1")
	}
	lcWriteGlobalLock(t, map[string]SkillLockEntry{
		"foo": lcGitHubEntry("owner/repo", "", "skills/foo/SKILL.md", lcSHA("a")),
		"bar": lcGitHubEntry("owner/repo", "", "skills/bar/SKILL.md", lcSHA("b")),
	})
	tree := lcTreeJSON(t, lcSHA("0"), false, append(lcSkillTree("skills/foo", lcSHA("a")), lcSkillTree("skills/bar", lcSHA("c"))...)...)
	api := lcFakeGitHubAPI(t, lcServeTrees(map[string][]byte{"owner/repo@HEAD": tree}))
	reqs := lcStubUpdateFetch(t, map[string]string{"owner/repo/skills/bar": up})
	lockPath := GetSkillLockPath()
	lockBefore := lcReadFile(t, lockPath)

	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "↑ bar has an update") || strings.Contains(out, "↑ foo has an update") {
		t.Fatalf("check: code=%d out=%s", code, out)
	}
	got := api.all()
	if len(got) != 1 || got[0].uri != "/repos/owner/repo/git/trees/HEAD?recursive=1" ||
		got[0].accept != "application/vnd.github.v3+json" || got[0].userAgent != "skills-cli" || got[0].auth != "" {
		t.Fatalf("expected one anonymous Trees API call for both skills, got %+v", got)
	}
	if len(*reqs) != 0 {
		t.Fatalf("a tree answer must not clone, got %+v", *reqs)
	}
	if lcReadFile(t, lockPath) != lockBefore || !strings.Contains(lcReadFile(t, filepath.Join(home, ".agents", "skills", "bar", "SKILL.md")), "v1") {
		t.Fatal("check must not modify the lock or installed skills")
	}

	code, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g", "-y")
	if code != 0 || !strings.Contains(out, "Found 1 global update(s)") || !strings.Contains(out, "Updated bar") {
		t.Fatalf("update: code=%d out=%s", code, out)
	}
	if len(api.all()) != 2 {
		t.Errorf("update should make one more Trees API call, got %v", api.paths())
	}
	if len(*reqs) != 1 {
		t.Fatalf("expected one install fetch, got %+v", *reqs)
	}
	if r := (*reqs)[0]; r.Source != "owner/repo/skills/bar" || !reflect.DeepEqual(r.Skills, []string{"bar"}) || r.GitHubHost != "github.com" || r.FullDepth {
		t.Errorf("install fetch = %+v", r)
	}
	if !strings.Contains(lcReadFile(t, filepath.Join(home, ".agents", "skills", "bar", "SKILL.md")), "v2") {
		t.Error("bar was not reinstalled")
	}
	lock, _ := ReadSkillLock()
	if lock.Skills["bar"].SkillFolderHash != lcSHA("c") || lock.Skills["foo"].SkillFolderHash != lcSHA("a") {
		t.Errorf("lock hashes: foo=%s bar=%s", lock.Skills["foo"].SkillFolderHash, lock.Skills["bar"].SkillFolderHash)
	}

	code, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 0 || !strings.Contains(out, "All global skills are up to date") {
		t.Errorf("after update: code=%d out=%s", code, out)
	}
}

// A ref-pinned entry asks the Trees API for its ref only, and installs from it.
func TestRunUpdate_GlobalTreesAPIRefPinned(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, filepath.Join(up, "skills", "b"), "b", "two")
	for _, n := range []string{"a", "b"} {
		lcWriteSkill(t, filepath.Join(home, ".agents", "skills", n), n, "old")
	}
	lcWriteGlobalLock(t, map[string]SkillLockEntry{
		"a": lcGitHubEntry("owner/repo", "v1", "skills/a/SKILL.md", lcSHA("a")),
		"b": lcGitHubEntry("owner/repo", "release/2", "skills/b/SKILL.md", lcSHA("b")),
	})
	api := lcFakeGitHubAPI(t, lcServeTrees(map[string][]byte{
		"owner/repo@v1":        lcTreeJSON(t, lcSHA("1"), false, lcSkillTree("skills/a", lcSHA("a"))...),
		"owner/repo@release/2": lcTreeJSON(t, lcSHA("2"), false, lcSkillTree("skills/b", lcSHA("c"))...),
	}))
	reqs := lcStubUpdateFetch(t, map[string]string{"owner/repo/skills/b#release/2": up})

	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "↑ b has an update") || strings.Contains(out, "↑ a has an update") || strings.Contains(out, "deleted upstream") {
		t.Fatalf("check: code=%d out=%s", code, out)
	}
	paths := api.paths()
	sort.Strings(paths)
	want := []string{"/repos/owner/repo/git/trees/release%2F2?recursive=1", "/repos/owner/repo/git/trees/v1?recursive=1"}
	if !reflect.DeepEqual(paths, want) || len(*reqs) != 0 {
		t.Fatalf("calls=%v fetches=%+v", paths, *reqs)
	}
	code, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g", "-y")
	if code != 0 || !strings.Contains(out, "Updated b") || len(*reqs) != 1 {
		t.Fatalf("update: code=%d out=%s fetches=%+v", code, out, *reqs)
	}
	if lock, _ := ReadSkillLock(); lock.Skills["b"].SkillFolderHash != lcSHA("c") || lock.Skills["b"].Ref != "release/2" {
		t.Errorf("lock b = %+v", lock.Skills["b"])
	}
}

// A rate-limited anonymous call with no token and no GitHub CLI falls back to a
// clone, and later repos skip the anonymous attempt (the per-process memo).
func TestRunUpdate_GlobalTreesAPIRateLimitedFallsBackToClone(t *testing.T) {
	home := lcHermetic(t)
	t.Setenv("SKILLS_CLONE_CONCURRENCY", "1")
	one, two := t.TempDir(), t.TempDir()
	lcWriteSkill(t, filepath.Join(one, "skills", "foo"), "foo", "same")
	lcWriteSkill(t, filepath.Join(two, "skills", "bar"), "bar", "new")
	for _, n := range []string{"foo", "bar"} {
		lcWriteSkill(t, filepath.Join(home, ".agents", "skills", n), n, "x")
	}
	fooHash, _ := ComputeGitTreeHash(filepath.Join(one, "skills", "foo"))
	lcWriteGlobalLock(t, map[string]SkillLockEntry{
		"foo": lcGitHubEntry("owner/one", "", "skills/foo/SKILL.md", fooHash),
		"bar": lcGitHubEntry("owner/two", "", "skills/bar/SKILL.md", lcSHA("0")),
	})
	api := lcFakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})
	reqs := lcStubUpdateFetch(t, map[string]string{"https://github.com/owner/one.git": one, "https://github.com/owner/two.git": two})

	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "↑ bar has an update") || strings.Contains(out, "↑ foo has an update") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Count(out, "GitHub API unavailable; checking via Git clone") != 2 || len(*reqs) != 2 {
		t.Fatalf("expected two clone fallbacks, out=%s fetches=%+v", out, *reqs)
	}
	for _, r := range *reqs {
		if !r.FullDepth || !r.NoBlob || !r.IncludeDuplicateNames || r.GitHubHost != "github.com" {
			t.Errorf("clone request = %+v", r)
		}
	}
	if got := api.paths(); len(got) != 1 {
		t.Errorf("after a rate limit the next repo must skip the anonymous call, got %v", got)
	}
}

// A private repo answers 404 to an anonymous call; GITHUB_TOKEN or GH_TOKEN
// turns it into a tree. Without a token the check clones instead.
func TestRunUpdate_GlobalTreesAPIPrivateRepo(t *testing.T) {
	for _, tc := range []struct{ name, env, token string }{
		{"GITHUB_TOKEN", "GITHUB_TOKEN", "ghp_one"},
		{"GH_TOKEN", "GH_TOKEN", "ghp_two"},
		{"no token", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := lcHermetic(t)
			if tc.env != "" {
				t.Setenv(tc.env, tc.token)
			}
			up := t.TempDir()
			lcWriteSkill(t, filepath.Join(up, "skills", "foo"), "foo", "v1")
			lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "foo"), "foo", "v1")
			hash, _ := ComputeGitTreeHash(filepath.Join(up, "skills", "foo"))
			lcWriteGlobalLock(t, map[string]SkillLockEntry{"foo": lcGitHubEntry("owner/private", "", "skills/foo/SKILL.md", hash)})
			serve := lcServeTrees(map[string][]byte{"owner/private@HEAD": lcTreeJSON(t, lcSHA("0"), false, lcSkillTree("skills/foo", hash)...)})
			api := lcFakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.token == "" || r.Header.Get("Authorization") != "Bearer "+tc.token {
					http.NotFound(w, r)
					return
				}
				serve(w, r)
			})
			reqs := lcStubUpdateFetch(t, map[string]string{"https://github.com/owner/private.git": up})
			code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
			if code != 0 || !strings.Contains(out, "All global skills are up to date") {
				t.Fatalf("code=%d out=%s", code, out)
			}
			got := api.all()
			if tc.token == "" {
				if len(got) != 1 || len(*reqs) != 1 || !strings.Contains(out, "GitHub API unavailable; checking via Git clone") {
					t.Fatalf("no token: calls=%+v fetches=%+v out=%s", got, *reqs, out)
				}
				return
			}
			if len(got) != 2 || got[0].auth != "" || got[1].auth != "Bearer "+tc.token || len(*reqs) != 0 {
				t.Fatalf("expected an anonymous call then one with the token and no clone: calls=%+v fetches=%+v", got, *reqs)
			}
		})
	}
}

// A truncated tree that lacks a locked skill path is resolved by a clone.
func TestRunUpdate_GlobalTreesAPITruncatedTreeClones(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, filepath.Join(up, "skills", "foo"), "foo", "v1")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "foo"), "foo", "v1")
	hash, _ := ComputeGitTreeHash(filepath.Join(up, "skills", "foo"))
	lcWriteGlobalLock(t, map[string]SkillLockEntry{"foo": lcGitHubEntry("owner/huge", "", "skills/foo/SKILL.md", hash)})
	api := lcFakeGitHubAPI(t, lcServeTrees(map[string][]byte{
		"owner/huge@HEAD": lcTreeJSON(t, lcSHA("0"), true, RepoTreeEntry{Path: "aaa", Type: "tree", SHA: lcSHA("1")}),
	}))
	reqs := lcStubUpdateFetch(t, map[string]string{"https://github.com/owner/huge.git": up})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 0 || !strings.Contains(out, "Skill paths changed; resolving via Git clone") || !strings.Contains(out, "All global skills are up to date") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(api.all()) != 1 || len(*reqs) != 1 {
		t.Errorf("calls=%v fetches=%+v", api.paths(), *reqs)
	}
}

// A root-level skill (skillPath "SKILL.md") compares the repo's root tree SHA
// and reinstalls from the repo itself.
func TestRunUpdate_GlobalTreesAPIRootSkill(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, up, "rooty", "v2")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "rooty"), "rooty", "v1")
	lcWriteGlobalLock(t, map[string]SkillLockEntry{"rooty": lcGitHubEntry("owner/root", "", "SKILL.md", lcSHA("a"))})
	api := lcFakeGitHubAPI(t, lcServeTrees(map[string][]byte{
		"owner/root@HEAD": lcTreeJSON(t, lcSHA("b"), false, RepoTreeEntry{Path: "SKILL.md", Type: "blob", SHA: lcSHA("f")}),
	}))
	reqs := lcStubUpdateFetch(t, map[string]string{"owner/root": up})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "↑ rooty has an update") || len(*reqs) != 0 {
		t.Fatalf("check: code=%d out=%s fetches=%+v", code, out, *reqs)
	}
	code, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g", "-y")
	if code != 0 || !strings.Contains(out, "Updated rooty") || len(*reqs) != 1 || (*reqs)[0].Source != "owner/root" {
		t.Fatalf("update: code=%d out=%s fetches=%+v", code, out, *reqs)
	}
	if lock, _ := ReadSkillLock(); lock.Skills["rooty"].SkillFolderHash != lcSHA("b") {
		t.Errorf("root skill must record the root tree SHA, got %s", lock.Skills["rooty"].SkillFolderHash)
	}
	if len(api.all()) != 2 {
		t.Errorf("calls=%v", api.paths())
	}
}

// A Windows-style skillPath is not a tree path as written, so (like upstream)
// the check clones, resolves the skill at its forward-slash path, and update
// rewrites the lock with that path. The tree-hash helper itself accepts both.
func TestRunUpdate_GlobalTreesAPIWindowsSkillPath(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, filepath.Join(up, "skills", "foo"), "foo", "v1")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "foo"), "foo", "v1")
	hash, _ := ComputeGitTreeHash(filepath.Join(up, "skills", "foo"))
	treeBody := lcTreeJSON(t, lcSHA("0"), false, lcSkillTree("skills/foo", hash)...)
	var tree RepoTree
	if err := json.Unmarshal(treeBody, &tree); err != nil {
		t.Fatal(err)
	}
	if got := GetSkillFolderHashFromTree(&tree, `skills\foo\SKILL.md`); got != hash {
		t.Fatalf("GetSkillFolderHashFromTree(backslashes) = %q, want %q", got, hash)
	}
	lcWriteGlobalLock(t, map[string]SkillLockEntry{"foo": lcGitHubEntry("owner/repo", "", `skills\foo\SKILL.md`, hash)})
	api := lcFakeGitHubAPI(t, lcServeTrees(map[string][]byte{"owner/repo@HEAD": treeBody}))
	reqs := lcStubUpdateFetch(t, map[string]string{"https://github.com/owner/repo.git": up, "owner/repo/skills/foo": up})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "Skill paths changed; resolving via Git clone") || !strings.Contains(out, "↑ foo has an update") {
		t.Fatalf("check: code=%d out=%s", code, out)
	}
	code, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g", "-y")
	if code != 0 || !strings.Contains(out, "Updated foo") {
		t.Fatalf("update: code=%d out=%s", code, out)
	}
	if lock, _ := ReadSkillLock(); lock.Skills["foo"].SkillPath != "skills/foo/SKILL.md" || lock.Skills["foo"].SkillFolderHash != hash {
		t.Errorf("lock foo = %+v", lock.Skills["foo"])
	}
	_, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if !strings.Contains(out, "All global skills are up to date") || strings.Contains(out, "Skill paths changed") {
		t.Errorf("after update the tree answers again: %s", out)
	}
	if n := len(api.all()); n != 3 || len(*reqs) != 3 {
		t.Errorf("calls=%v fetches=%+v", api.paths(), *reqs)
	}
}

// A network error tries each default branch, then clones.
func TestRunUpdate_GlobalTreesAPINetworkErrorClones(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, filepath.Join(up, "skills", "foo"), "foo", "v1")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "foo"), "foo", "v1")
	hash, _ := ComputeGitTreeHash(filepath.Join(up, "skills", "foo"))
	lcWriteGlobalLock(t, map[string]SkillLockEntry{"foo": lcGitHubEntry("owner/repo", "", "skills/foo/SKILL.md", hash)})
	api := lcFakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	reqs := lcStubUpdateFetch(t, map[string]string{"https://github.com/owner/repo.git": up})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 0 || !strings.Contains(out, "GitHub API unavailable; checking via Git clone") || len(*reqs) != 1 {
		t.Fatalf("code=%d out=%s fetches=%+v", code, out, *reqs)
	}
	want := []string{"/repos/owner/repo/git/trees/HEAD?recursive=1", "/repos/owner/repo/git/trees/main?recursive=1", "/repos/owner/repo/git/trees/master?recursive=1"}
	if got := api.paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls=%v", got)
	}
}

// Non-GitHub sources never call the Trees API.
func TestRunUpdate_GlobalGenericGitClonesWithoutAPI(t *testing.T) {
	home := lcHermetic(t)
	up := t.TempDir()
	lcWriteSkill(t, filepath.Join(up, "skills", "foo"), "foo", "v1")
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "foo"), "foo", "v1")
	lcWriteGlobalLock(t, map[string]SkillLockEntry{"foo": {
		Source: "https://git.example.com/acme/skills.git", SourceType: "git", SourceUrl: "https://git.example.com/acme/skills.git",
		SkillPath: "skills/foo/SKILL.md", SkillFolderHash: lcHash(t, filepath.Join(up, "skills", "foo")),
	}})
	api := lcFakeGitHubAPI(t, lcServeTrees(nil))
	reqs := lcStubUpdateFetch(t, map[string]string{"https://git.example.com/acme/skills.git": up})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 0 || !strings.Contains(out, "All global skills are up to date") || strings.Contains(out, "via Git clone") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(api.all()) != 0 || len(*reqs) != 1 || (*reqs)[0].GitHubHost != "" {
		t.Errorf("calls=%v fetches=%+v", api.paths(), *reqs)
	}
}

// Well-known checks read only the index (with X-Skills-Update-Check) for v0.2.0
// entries, warn about skills removed from the index, list new ones, and update
// rewrites the lock entry the way upstream's globalWellKnownLockEntry does.
func TestRunUpdate_GlobalWellKnownIndexCheck(t *testing.T) {
	home := lcHermetic(t)
	content := "---\nname: wk-a\ndescription: A\n---\n# A v2\n"
	digest := fpSHA256Digest([]byte(content))
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path+" check="+r.Header.Get("X-Skills-Update-Check"))
		mu.Unlock()
		if r.URL.Path != "/.well-known/agent-skills/index.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"$schema": DiscoverySchemaV2,
			"skills": []map[string]string{
				{"name": "wk-a", "description": "A", "type": "skill-md", "url": "/wk-a/SKILL.md", "digest": digest},
				{"name": "wk-new", "description": "N", "type": "skill-md", "url": "/wk-new/SKILL.md", "digest": "sha256:" + strings.Repeat("1", 64)},
			},
		})
	}))
	defer srv.Close()
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "wk-a"), "wk-a", "A")
	stale := "sha256:" + strings.Repeat("0", 64)
	lcWriteGlobalLock(t, map[string]SkillLockEntry{
		"wk-a":    {Source: "127.0.0.1", SourceType: "well-known", SourceUrl: srv.URL + "/wk-a/SKILL.md", SourceBaseUrl: srv.URL, WellKnownDigest: stale, InstallMode: "copy"},
		"wk-gone": {Source: "127.0.0.1", SourceType: "well-known", SourceUrl: srv.URL + "/wk-gone/SKILL.md", SourceBaseUrl: srv.URL, WellKnownDigest: stale},
	})

	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 1 || !strings.Contains(out, "↑ wk-a has an update") || !strings.Contains(out, "appear to have been deleted upstream") ||
		!strings.Contains(out, "• wk-gone") || !strings.Contains(out, "1 new skill(s) available from this source: wk-new") ||
		!strings.Contains(out, "To install: skl add "+srv.URL+" --skill wk-new -g") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	mu.Lock()
	gotHits := append([]string(nil), hits...)
	mu.Unlock()
	for _, h := range gotHits {
		if !strings.HasSuffix(h, "/index.json check=1") {
			t.Errorf("a v0.2.0 check must read only index files, marked as update checks, got %v", gotHits)
		}
	}

	staged := t.TempDir()
	lcWriteFile(t, filepath.Join(staged, "SKILL.md"), content)
	var fetches []SourceRequest
	orig := updateFetch
	updateFetch = func(req SourceRequest) (*FetchedSource, error) {
		fetches = append(fetches, req)
		return &FetchedSource{RootDir: staged, SourceType: SourceTypeWellKnown, Skills: []FetchedSkill{{
			Skill: Skill{Name: "wk-a", Description: "A", Path: staged}, WellKnownDigest: digest, InstallName: "wk-a", SourceURL: srv.URL + "/wk-a/SKILL.md",
		}}}, nil
	}
	t.Cleanup(func() { updateFetch = orig })
	code, out, _ = lcRunUpdate(t, t.TempDir(), UpdateModeUpdate, "-g", "-y")
	if code != 0 || !strings.Contains(out, "Updated wk-a") || !strings.Contains(out, "Skipping deletion in non-interactive mode.") {
		t.Fatalf("update: code=%d out=%s", code, out)
	}
	if len(fetches) != 1 || fetches[0].Source != srv.URL || !reflect.DeepEqual(fetches[0].Skills, []string{"wk-a"}) {
		t.Errorf("fetches=%+v", fetches)
	}
	lock, _ := ReadSkillLock()
	e := lock.Skills["wk-a"]
	e.InstalledAt, e.UpdatedAt = "", ""
	want := SkillLockEntry{Source: GetWellKnownSourceIdentifier(srv.URL), SourceType: "well-known", SourceUrl: srv.URL + "/wk-a/SKILL.md", SourceBaseUrl: srv.URL, WellKnownDigest: digest}
	if e != want {
		t.Errorf("lock wk-a = %+v, want %+v", e, want)
	}
	if _, ok := lock.Skills["wk-gone"]; !ok {
		t.Error("a non-interactive update must not remove skills deleted upstream")
	}
}

// Like upstream, an unreachable well-known index is reported but not counted as
// a failure.
func TestRunUpdate_GlobalWellKnownUnreachable(t *testing.T) {
	home := lcHermetic(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	lcWriteSkill(t, filepath.Join(home, ".agents", "skills", "wk-a"), "wk-a", "A")
	lcWriteGlobalLock(t, map[string]SkillLockEntry{
		"wk-a": {Source: "127.0.0.1", SourceType: "well-known", SourceBaseUrl: srv.URL, WellKnownDigest: "sha256:" + strings.Repeat("0", 64)},
	})
	code, out, _ := lcRunUpdate(t, t.TempDir(), UpdateModeCheck, "-g")
	if code != 0 || !strings.Contains(out, "✗ Failed to check skills from "+srv.URL) || strings.Contains(out, "Failed to update") {
		t.Errorf("code=%d out=%s", code, out)
	}
}

// Review #65 item 4: a project update of a well-known skill records the hash of
// the installed copy (metadata.json excluded), the same hash add writes and
// install --frozen-lockfile checks, so the lock does not drift.
func TestRunUpdate_ProjectWellKnownHashMatchesFrozenInstall(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	content := "---\nname: wk-a\ndescription: A\n---\n# A v2\n"
	digest := fpSHA256Digest([]byte(content))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent-skills/index.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"$schema": DiscoverySchemaV2,
			"skills": []map[string]string{
				{"name": "wk-a", "description": "A", "type": "skill-md", "url": "/wk-a/SKILL.md", "digest": digest},
			},
		})
	}))
	defer srv.Close()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "wk-a"), "wk-a", "A")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"wk-a": {Source: GetWellKnownSourceIdentifier(srv.URL), SourceType: "well-known", SourceUrl: srv.URL,
			ComputedHash: "stale", WellKnownDigest: "sha256:" + strings.Repeat("0", 64)},
	})

	staged := t.TempDir()
	lcWriteFile(t, filepath.Join(staged, "SKILL.md"), content)
	lcWriteFile(t, filepath.Join(staged, "metadata.json"), `{"served":"by the index"}`)
	fetched := func() *FetchedSource {
		return &FetchedSource{RootDir: staged, SourceType: SourceTypeWellKnown, Source: srv.URL, SourceURL: srv.URL, Via: "well-known",
			Skills: []FetchedSkill{{Skill: Skill{Name: "wk-a", Description: "A", Path: staged}, WellKnownDigest: digest, InstallName: "wk-a"}}}
	}
	orig := updateFetch
	updateFetch = func(SourceRequest) (*FetchedSource, error) { return fetched(), nil }
	t.Cleanup(func() { updateFetch = orig })

	code, out, _ := lcRunUpdate(t, cwd, UpdateModeUpdate, "-p", "-y")
	if code != 0 || !strings.Contains(out, "Updated wk-a") {
		t.Fatalf("update: code=%d out=%s", code, out)
	}
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		t.Fatal(err)
	}
	got := lock.Skills["wk-a"].ComputedHash
	want, err := addStagedCopyHash(staged)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || got == lcHash(t, staged) {
		t.Fatalf("computedHash = %q, want installed-copy hash %q (raw staging hash %q)", got, want, lcHash(t, staged))
	}
	if inst := lcHash(t, filepath.Join(cwd, ".agents", "skills", "wk-a")); got != inst {
		t.Errorf("computedHash = %q, installed copy hashes to %q", got, inst)
	}
	frozen := lock
	res := InstallFromSource(fetched(), InstallFromSourceOptions{Agents: []AgentType{"codex"}, FrozenLock: &frozen, Cwd: cwd})
	if res.Error != "" || len(res.Installed) != 1 {
		t.Errorf("install --frozen-lockfile after update: %+v", res)
	}
}

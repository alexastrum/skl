package skl

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// #2702: string "true" and "1" for metadata.internal are treated as internal.
func TestIsInternalSkillStringValues(t *testing.T) {
	t.Setenv("INSTALL_INTERNAL_SKILLS", "")
	for _, val := range []interface{}{true, "true", "1"} {
		if !IsInternalSkill(map[string]interface{}{"internal": val}) {
			t.Errorf("IsInternalSkill(%v) = false, want true", val)
		}
	}
	for _, val := range []interface{}{false, "false", "0", "", 1, nil} {
		if IsInternalSkill(map[string]interface{}{"internal": val}) {
			t.Errorf("IsInternalSkill(%v) = true, want false", val)
		}
	}
	if !IsInternalSkill(map[string]string{"internal": "true"}) || IsInternalSkill(map[string]string{"internal": "false"}) {
		t.Error("map[string]string mismatch")
	}

	dir := t.TempDir()
	mdPath := filepath.Join(dir, "SKILL.md")
	_ = os.WriteFile(mdPath, []byte("---\nname: s\ndescription: d\nmetadata:\n  internal: \"true\"\n---\n"), 0o644)
	if s, err := ParseSkillMd(mdPath, false); err != nil || s != nil {
		t.Errorf("expected string internal skill to be hidden by default, got %+v (%v)", s, err)
	}
	if s, err := ParseSkillMd(mdPath, true); err != nil || s == nil {
		t.Errorf("expected string internal skill with includeInternal=true, got %+v (%v)", s, err)
	}
}

// #2079, #2082: anchored GitHub/GitLab URLs and mirrored proxy URLs.
func TestParseSourceMirroredAndAnchoredURLs(t *testing.T) {
	t.Setenv("GH_HOST", "")
	cases := []struct {
		in   string
		want ParsedSource
	}{
		{
			"https://ghproxy.com/https://github.com/owner/repo",
			ParsedSource{Type: SourceTypeGit, URL: "https://ghproxy.com/https://github.com/owner/repo.git"},
		},
		{
			"https://ghproxy.com/https://github.com/owner/repo/tree/main/skills/my%20skill",
			ParsedSource{Type: SourceTypeGit, URL: "https://ghproxy.com/https://github.com/owner/repo.git", Ref: "main", Subpath: "skills/my skill"},
		},
		{
			"http://127.0.0.1:8080/mirror/https://github.com/owner/repo.git#dev@my-skill",
			ParsedSource{Type: SourceTypeGit, URL: "http://127.0.0.1:8080/mirror/https://github.com/owner/repo.git", Ref: "dev", SkillFilter: "my-skill"},
		},
		{
			"https://mirror.example.com/https://gitlab.com/group/sub/repo/-/tree/v1/skills/a",
			ParsedSource{Type: SourceTypeGit, URL: "https://mirror.example.com/https://gitlab.com/group/sub/repo.git", Ref: "v1", Subpath: "skills/a"},
		},
		{
			"https://docs.example.com/redirect?to=https://github.com/owner/repo",
			ParsedSource{Type: SourceTypeWellKnown, URL: "https://docs.example.com/redirect?to=https://github.com/owner/repo"},
		},
	}
	for _, tc := range cases {
		got, err := ParseSource(tc.in)
		if err != nil {
			t.Errorf("ParseSource(%q) error: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseSource(%q)\n got  %+v\n want %+v", tc.in, got, tc.want)
		}
	}
	if _, err := ParseSource("https://ghproxy.com/https://github.com/owner/repo/tree/main/../../etc"); !IsDiagnostic(err, CodeUnsafeSubpath) {
		t.Errorf("expected unsafe subpath diagnostic on mirror URL, got %v", err)
	}
}

// #2711, #2713: GitHub repo rename resolution and full_name validation.
func TestGitHubRepoRenameAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/old-org/old-repo":
			_, _ = w.Write([]byte(`{"private":false,"full_name":"new-org/new-repo"}`))
		case "/repos/evil/dotdot":
			_, _ = w.Write([]byte(`{"private":false,"full_name":"evil/.."}`))
		case "/repos/evil/traversal":
			_, _ = w.Write([]byte(`{"private":false,"full_name":"evil/repo/../../etc"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	oldAPI := githubAPIBaseOverride
	githubAPIBaseOverride = srv.URL
	defer func() { githubAPIBaseOverride = oldAPI }()

	info := FetchGitHubRepoInfo("old-org", "old-repo")
	if info == nil || info.Private || info.FullName != "new-org/new-repo" {
		t.Fatalf("info = %+v", info)
	}
	for _, r := range []string{"dotdot", "traversal"} {
		bad := FetchGitHubRepoInfo("evil", r)
		if bad == nil || bad.FullName != "" {
			t.Errorf("repo %q: invalid full_name must be rejected, got %+v", r, bad)
		}
	}

	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) {
		f := addFakeLocalFetch(t, src)
		f.SourceType = SourceTypeGitHub
		f.Source = "old-org/old-repo"
		f.SourceURL = "https://github.com/old-org/old-repo.git"
		f.Parsed = ParsedSource{Type: SourceTypeGitHub, URL: "https://github.com/old-org/old-repo.git"}
		return f, nil
	})
	lookupGitHubRepoFn = LookupGitHubRepo

	code, stdout, stderr := runAddForTest(t, cwd, "old-org/old-repo", "-a", "codex", "-s", "alpha", "-y")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "old-org/old-repo has moved to new-org/new-repo") {
		t.Errorf("expected rename notice in stdout:\n%s", stdout)
	}
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if e := lock.Skills["alpha"]; e.Source != "new-org/new-repo" {
		t.Errorf("lock entry source = %q, want new-org/new-repo", e.Source)
	}
}

// #2354: TryBlobInstall rejects stale snapshot files whose blob SHA mismatches the tree.
func TestTryBlobInstallRejectsStaleSnapshotSHA(t *testing.T) {
	md := provSkillMD("foo", "Foo")
	tree := provTree("skills/foo/SKILL.md", "skills/foo/ref.md")
	// Force a mismatched tree SHA on ref.md so the snapshot is detected as stale.
	for i := range tree.Tree {
		if tree.Tree[i].Path == "skills/foo/ref.md" {
			sum := gitBlobSHA1([]byte("newer-tree-content"))
			tree.Tree[i].SHA = hex.EncodeToString(sum[:])
		}
	}
	startBlobServer(t, blobFixture{
		tree:      tree,
		raw:       map[string]string{"skills/foo/SKILL.md": md},
		snapshots: map[string][]SkillSnapshotFile{"foo": snapFiles("SKILL.md", md, "ref.md", "stale-snapshot-content")},
	})
	res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
	if res != nil || err != nil {
		t.Fatalf("expected stale snapshot to return nil,nil fallback; got %+v, %v", res, err)
	}
}

// #2386: well-known skills load supporting files lazily and fail without fallback on broken files.
func TestWellKnownLazyLoadAndIncompleteRejection(t *testing.T) {
	_, cwd := instTestEnv(t)
	provIsolate(t)
	srv := newProvServer(t)
	healthyMD := provSkillMD("healthy", "Healthy skill")
	brokenMD := provSkillMD("broken", "Broken skill")
	srv.set("/.well-known/skills/healthy/SKILL.md", []byte(healthyMD))
	srv.set("/.well-known/skills/healthy/refs/guide.md", []byte("guide"))
	srv.set("/.well-known/skills/broken/SKILL.md", []byte(brokenMD))
	// broken/refs/missing.md is intentionally not registered (404).
	srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(
		map[string]interface{}{"name": "healthy", "description": "Healthy skill", "files": []string{"SKILL.md", "refs/guide.md"}},
		map[string]interface{}{"name": "broken", "description": "Broken skill", "files": []string{"SKILL.md", "refs/missing.md"}},
	))

	// 1. --list discovers skills without fetching supporting files.
	code, stdout, stderr := runAddForTest(t, cwd, srv.URL, "--list")
	if code != 0 {
		t.Fatalf("--list failed: code=%d stderr=%s", code, stderr)
	}
	if srv.hit("/.well-known/skills/healthy/refs/guide.md") != 0 || srv.hit("/.well-known/skills/broken/refs/missing.md") != 0 {
		t.Errorf("--list must not fetch supporting files")
	}
	if !strings.Contains(stdout, "healthy") || !strings.Contains(stdout, "broken") {
		t.Errorf("--list stdout = %s", stdout)
	}

	// 2. --skill healthy installs healthy without fetching broken's supporting file.
	code, _, stderr = runAddForTest(t, cwd, srv.URL, "--skill", "healthy", "-a", "codex", "-y")
	if code != 0 {
		t.Fatalf("--skill healthy failed: code=%d stderr=%s", code, stderr)
	}
	if srv.hit("/.well-known/skills/broken/refs/missing.md") != 0 {
		t.Errorf("unselected skill supporting file must not be fetched")
	}

	// 3. --skill broken fails with SKILLS_WELLKNOWN_FILE_FETCH_FAILED.
	code, _, stderr = runAddForTest(t, cwd, srv.URL, "--skill", "broken", "-a", "codex", "-y")
	if code != 1 || !strings.Contains(stderr, "SKILLS_WELLKNOWN_FILE_FETCH_FAILED") {
		t.Fatalf("expected SKILLS_WELLKNOWN_FILE_FETCH_FAILED, got code=%d stderr=%s", code, stderr)
	}

	// 4. `use` with --skill healthy succeeds; --skill broken fails with SKILLS_WELLKNOWN_FILE_FETCH_FAILED.
	var uout, uerr bytes.Buffer
	if code := RunUseWithIO([]string{srv.URL, "--skill", "healthy"}, UseIO{Stdout: &uout, Stderr: &uerr, Cwd: cwd}); code != 0 {
		t.Fatalf("use healthy failed: code=%d stderr=%s", code, uerr.String())
	}
	uout.Reset()
	uerr.Reset()
	if code := RunUseWithIO([]string{srv.URL, "--skill", "broken"}, UseIO{Stdout: &uout, Stderr: &uerr, Cwd: cwd}); code != 1 || !strings.Contains(uerr.String(), "SKILLS_WELLKNOWN_FILE_FETCH_FAILED") {
		t.Fatalf("use broken: code=%d stderr=%s", code, uerr.String())
	}
}

// #2723, #2724, #2725: category directory protection in installer and add.
func TestCategoryDirectoryGuards(t *testing.T) {
	_, cwd := instTestEnv(t)
	// Create a category directory at .claude/skills/foo containing nested skill bar/SKILL.md.
	catDir := filepath.Join(cwd, ".claude", "skills", "foo")
	instWriteSkill(t, filepath.Join(catDir, "bar"), "bar")
	if err := os.MkdirAll(filepath.Join(cwd, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "src")
	instWriteSkill(t, filepath.Join(src, "foo"), "foo")

	// Direct installer call refuses to replace the category directory.
	res := InstallSkillForAgent(Skill{Name: "foo", Description: "d", Path: filepath.Join(src, "foo")}, "claude-code", InstallOptions{Cwd: cwd, Mode: InstallModeCopy})
	if res.Success || !strings.Contains(res.Error, "Refusing to replace") {
		t.Fatalf("expected category conflict failure, got %+v", res)
	}
	if !instExists(filepath.Join(catDir, "bar", "SKILL.md")) {
		t.Fatal("nested skill in category directory was deleted")
	}

	// RunAdd skips the conflicted claude-code target, installs to cursor, and warns.
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	code, stdout, stderr := runAddForTest(t, cwd, src, "-a", "claude-code", "cursor", "-y")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "conflicts with category: Claude Code (foo/ contains bar)") {
		t.Errorf("missing conflict summary line in stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "[SKILLS_CATEGORY_DIR_CONFLICT]") {
		t.Errorf("missing category conflict diagnostic in stderr:\n%s", stderr)
	}
	if !instExists(filepath.Join(catDir, "bar", "SKILL.md")) {
		t.Error("category directory nested skill must remain intact")
	}
	if !instExists(filepath.Join(cwd, ".agents", "skills", "foo", "SKILL.md")) {
		t.Error("non-conflicted cursor (universal) target should be installed at .agents/skills/foo")
	}

	// JSON mode when all targets conflict reports skipped with reason.
	code, jsonOut, _ := runAddForTest(t, cwd, src, "-a", "claude-code", "-y", "--json")
	if code != 1 || !strings.Contains(jsonOut, `"Conflicts with a category directory"`) {
		t.Errorf("json conflict: code=%d out=%s", code, jsonOut)
	}
}

// #2685: portable v2 skill hash, v1->v2 migration, modified state in list/check/install.
func TestLockV2HashMigrationAndModifiedDetection(t *testing.T) {
	// CRLF and LF produce identical hashes; metadata.json and _private are excluded.
	h1 := HashSkillFiles([]HashedFile{
		{Path: `sub\b.txt`, Content: []byte("line1\r\nline2\r\n")},
		{Path: "SKILL.md", Content: []byte("---\nname: a\n---\n")},
	})
	h2 := HashSkillFiles([]HashedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: a\n---\n")},
		{Path: "sub/b.txt", Content: []byte("line1\nline2\n")},
	})
	if h1 != h2 {
		t.Fatalf("CRLF vs LF hash mismatch: %s != %s", h1, h2)
	}

	lcHermetic(t)
	cwd := t.TempDir()
	src := lcLocalSource(t, "alpha", "upstream")
	alphaDir := filepath.Join(cwd, ".agents", "skills", "alpha")
	lcWriteSkill(t, alphaDir, "alpha", "upstream")

	// Write a version 1 lock file with the legacy v1 hash.
	v1Hash, err := ComputeLegacySkillFolderHash(alphaDir)
	if err != nil {
		t.Fatal(err)
	}
	v1JSON := `{"version":1,"skills":{"alpha":{"source":` + strconvQuote(src) + `,"sourceType":"local","computedHash":"` + v1Hash + `"}}}`
	lcWriteFile(t, filepath.Join(cwd, "skills-lock.json"), v1JSON)

	// ReadLocalLock migrates in-memory to version 2 with the v2 hash.
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		t.Fatal(err)
	}
	v2Hash, _ := ComputeSkillFolderHash(alphaDir)
	if lock.Version != 2 || lock.Skills["alpha"].ComputedHash != v2Hash {
		t.Fatalf("migrated lock = %+v, want v2 hash %s", lock, v2Hash)
	}

	// Modify the installed skill on disk: list and check report it as modified, and install restores it.
	lcWriteSkill(t, alphaDir, "alpha", "locally tampered")
	_, listOut, _ := lcRunList(t, cwd)
	if !strings.Contains(listOut, "modified") || !strings.Contains(listOut, "(differs from the lock)") {
		t.Errorf("list human output missing modified badge:\n%s", listOut)
	}
	code, checkOut, _ := lcRunUpdate(t, cwd, UpdateModeCheck, "-p")
	if code != 1 || !strings.Contains(checkOut, "✎ alpha has local changes") || !strings.Contains(checkOut, "differ from skills-lock.json") {
		t.Errorf("check output: code=%d out=%s", code, checkOut)
	}

	code, instOut, instErr := lcRunInstall(t, cwd, "-a", "codex", "--no-frozen-lockfile")
	if code != 0 || !strings.Contains(instOut, "alpha does not match skills-lock.json; restoring the locked version") {
		t.Fatalf("install restore modified: code=%d out=%s err=%s", code, instOut, instErr)
	}
	if !strings.Contains(lcReadFile(t, filepath.Join(alphaDir, "SKILL.md")), "upstream") {
		t.Error("modified skill was not restored to upstream content")
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// #2715, #1771, #2073: remove --all only removes lock-tracked skills, and remove keeps unmanaged collision-prone dirs.
func TestRemoveUntrackedAndUnmanagedProtection(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	trackedDir := filepath.Join(cwd, ".agents", "skills", "tracked")
	untrackedDir := filepath.Join(cwd, ".agents", "skills", "hand-authored")
	lcWriteSkill(t, trackedDir, "tracked", "d")
	lcWriteSkill(t, untrackedDir, "hand-authored", "d")
	trackedHash, _ := ComputeSkillFolderHash(trackedDir)
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"tracked": {Source: "o/r", SourceType: "github", ComputedHash: trackedHash},
	})

	code, _, errOut := lcRunRemove(t, cwd, "--all")
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	if lcExists(trackedDir) {
		t.Error("tracked skill should be removed by --all")
	}
	if !lcExists(untrackedDir) {
		t.Error("untracked skill must be kept by --all")
	}
	if !strings.Contains(errOut, "[SKILLS_REMOVE_UNTRACKED_SKIPPED]") || !strings.Contains(errOut, "hand-authored") {
		t.Errorf("expected untracked warning in stderr, got %q", errOut)
	}

	// #1771, #2073: OpenClaw's `skills/<name>` directory is not a dot-directory; if its content
	// was authored/modified by the user, removing the skill must keep `skills/<name>`.
	lcWriteSkill(t, trackedDir, "tracked", "d")
	openclawSkill := filepath.Join(cwd, "skills", "tracked")
	lcWriteSkill(t, openclawSkill, "tracked", "user custom openclaw skill")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"tracked": {Source: "o/r", SourceType: "github", ComputedHash: trackedHash},
	})
	code, _, errOut = lcRunRemove(t, cwd, "tracked", "-y")
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	if !lcExists(openclawSkill) {
		t.Error("unmanaged skills/tracked directory was deleted")
	}
	if !strings.Contains(errOut, "[SKILLS_REMOVE_UNMANAGED_DIR_KEPT]") {
		t.Errorf("expected SKILLS_REMOVE_UNMANAGED_DIR_KEPT warning, got %q", errOut)
	}
}

// #2731: multi-term partial-match fallback in find.
func TestFindPartialMatchFallback(t *testing.T) {
	lcHermetic(t)
	orig := findSearchFn
	t.Cleanup(func() { findSearchFn = orig })
	findSearchFn = func(q, owner string) ([]FoundSkill, error) {
		switch q {
		case "sveltekit":
			return []FoundSkill{
				{Name: "sveltekit-a", Slug: "o/r/sveltekit-a", Source: "o/r", Installs: 100},
				{Name: "both-hit", Slug: "o/r/both-hit", Source: "o/r", Installs: 50},
			}, nil
		case "tailwind":
			return []FoundSkill{
				{Name: "both-hit", Slug: "o/r/both-hit", Source: "o/r", Installs: 50},
				{Name: "tailwind-b", Slug: "o/r/tailwind-b", Source: "o/r", Installs: 500},
			}, nil
		default:
			return nil, nil
		}
	}

	var out, errb bytes.Buffer
	code := runFind([]string{"sveltekit", "tailwind"}, t.TempDir(), &out, &errb, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, `No skills matched every term in "sveltekit tailwind". Partial matches:`) {
		t.Errorf("missing partial match header:\n%s", s)
	}
	// both-hit matched 2 subqueries so it must rank ahead of sveltekit-a and tailwind-b.
	iBoth := strings.Index(s, "both-hit")
	iSvelte := strings.Index(s, "sveltekit-a")
	iTailwind := strings.Index(s, "tailwind-b")
	if iBoth < 0 || iSvelte < 0 || iTailwind < 0 || iBoth > iSvelte || iSvelte > iTailwind {
		t.Errorf("unexpected partial match ranking:\n%s", s)
	}
}

// #2721, #2726: project update reads only locked skill paths and rejects symlinks outside the clone.
func TestUpdateProjectLockedPathsOnlyAndConfinement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	lcHermetic(t)
	cwd := t.TempDir()
	alphaInstalled := filepath.Join(cwd, ".agents", "skills", "alpha")
	lcWriteSkill(t, alphaInstalled, "alpha", "old")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"alpha": {Source: "acme/skills", SourceUrl: "https://github.com/acme/skills.git", SourceType: "github", SkillPath: "skills/alpha/SKILL.md", ComputedHash: "oldhash"},
	})

	// Clone root has a valid skills/alpha/SKILL.md and an unrelated broken SKILL.md.
	cloneDir := t.TempDir()
	lcWriteSkill(t, filepath.Join(cloneDir, "skills", "alpha"), "alpha", "new")
	lcWriteFile(t, filepath.Join(cloneDir, "skills", "broken", "SKILL.md"), "---\nname: [unterminated\n---\n")

	locked := readLockedCloneSkills(cloneDir, []string{"skills/alpha/SKILL.md"}, true)
	if len(locked) != 1 || locked[0].Name != "alpha" {
		t.Fatalf("readLockedCloneSkills = %+v", locked)
	}

	// #2726: if skills/alpha is a symlink escaping the clone root, readLockedCloneSkills returns nil.
	outside := t.TempDir()
	lcWriteSkill(t, outside, "alpha", "escaped")
	cloneEscape := t.TempDir()
	_ = os.MkdirAll(filepath.Join(cloneEscape, "skills"), 0o755)
	if err := os.Symlink(outside, filepath.Join(cloneEscape, "skills", "alpha")); err != nil {
		t.Fatal(err)
	}
	if got := readLockedCloneSkills(cloneEscape, []string{"skills/alpha/SKILL.md"}, true); got != nil {
		t.Errorf("expected nil for symlink escaping clone root, got %+v", got)
	}
}

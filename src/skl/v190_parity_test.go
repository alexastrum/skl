package skl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===========================================================================
// #2735: Pin lock entries to the installed commit

func TestV190_FetchTreeCommit(t *testing.T) {
	commit := strings.Repeat("a", 40)
	var gotPath, gotQuery string
	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	origBase := githubAPIBaseOverride
	githubAPIBaseOverride = srv.URL
	t.Cleanup(func() { githubAPIBaseOverride = origBase; ResetRepoTreeAuthState() })

	tree := &RepoTree{SHA: "deadbeef", Branch: "main"}

	// Branch tip whose tree SHA matches the fetched tree.
	ResetRepoTreeAuthState()
	body = fmt.Sprintf(`[{"sha":%q,"commit":{"tree":{"sha":"deadbeef"}}}]`, commit)
	if got := FetchTreeCommit("vercel/skills", tree, nil); got != commit {
		t.Fatalf("FetchTreeCommit = %q, want %q", got, commit)
	}
	if gotPath != "/repos/vercel/skills/commits" || gotQuery != "per_page=1&sha=main" {
		t.Errorf("request = %s?%s, want /repos/vercel/skills/commits?per_page=1&sha=main", gotPath, gotQuery)
	}

	// Trees API returns the commit SHA directly for a branch.
	ResetRepoTreeAuthState()
	body = fmt.Sprintf(`[{"sha":%q,"commit":{"tree":{"sha":"root-tree"}}}]`, commit)
	if got := FetchTreeCommit("vercel/skills", &RepoTree{SHA: commit, Branch: "main"}, nil); got != commit {
		t.Fatalf("FetchTreeCommit (commit sha) = %q, want %q", got, commit)
	}

	// Branch moved since the tree was fetched -> returns empty string.
	ResetRepoTreeAuthState()
	body = fmt.Sprintf(`[{"sha":%q,"commit":{"tree":{"sha":"newer-tree"}}}]`, commit)
	if got := FetchTreeCommit("vercel/skills", tree, nil); got != "" {
		t.Fatalf("FetchTreeCommit (moved branch) = %q, want empty", got)
	}

	// Tree came from HEAD -> omits &sha= to query the default branch.
	ResetRepoTreeAuthState()
	body = fmt.Sprintf(`[{"sha":%q,"commit":{"tree":{"sha":"deadbeef"}}}]`, commit)
	if got := FetchTreeCommit("vercel/skills", &RepoTree{SHA: "deadbeef", Branch: "HEAD"}, nil); got != commit {
		t.Fatalf("FetchTreeCommit (HEAD) = %q, want %q", got, commit)
	}
	if gotQuery != "per_page=1" {
		t.Errorf("HEAD query = %q, want per_page=1", gotQuery)
	}
}

func v190Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func v190FileURL(p string) string {
	slashed := filepath.ToSlash(p)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

func v190HermeticEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CI", "")
	for _, k := range []string{"CLAUDECODE", "CODEX_THREAD_ID", "CURSOR_AGENT", "GEMINI_CLI", "ANTIGRAVITY_AGENT"} {
		t.Setenv(k, "")
	}
	RefreshAgents()
	t.Cleanup(RefreshAgents)
}

func TestV190_LockCommitPin_ProjectAndGlobal(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	const sourceURL = "https://git.example.test/owner/skills.git"

	setup := func(t *testing.T) (root, project, repo, home string, publish func(body string) string) {
		t.Helper()
		root = t.TempDir()
		project = filepath.Join(root, "project")
		repo = filepath.Join(root, "repo")
		home = filepath.Join(root, "home")
		if err := os.MkdirAll(filepath.Join(repo, "skills", "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		v190Git(t, repo, "init", "-q", "-b", "main")
		v190HermeticEnv(t, home)
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "url."+v190FileURL(repo)+".insteadOf")
		t.Setenv("GIT_CONFIG_VALUE_0", sourceURL)

		publish = func(body string) string {
			t.Helper()
			p := filepath.Join(repo, "skills", "alpha", "SKILL.md")
			if err := os.WriteFile(p, []byte("---\nname: alpha\ndescription: alpha\n---\n"+body+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			v190Git(t, repo, "add", ".")
			v190Git(t, repo, "commit", "-q", "-m", body)
			return v190Git(t, repo, "rev-parse", "HEAD")
		}
		return
	}

	t.Run("records commit and restores it after upstream moves on", func(t *testing.T) {
		_, project, _, _, publish := setup(t)
		v1 := publish("v1")

		var out, errb bytes.Buffer
		nonInt := false
		code := RunAddWithIO([]string{sourceURL, "--skill", "alpha", "-a", "universal", "-y"}, AddIO{Stdout: &out, Stderr: &errb, Cwd: project, Interactive: &nonInt})
		if code != 0 {
			t.Fatalf("add exit %d: out=%s err=%s", code, out.String(), errb.String())
		}
		lockBytesBefore, err := os.ReadFile(filepath.Join(project, LocalLockFile))
		if err != nil {
			t.Fatal(err)
		}
		lock, err := ReadLocalLock(project)
		if err != nil {
			t.Fatal(err)
		}
		if lock.Skills["alpha"].Commit != v1 {
			t.Fatalf("lock commit = %q, want %q", lock.Skills["alpha"].Commit, v1)
		}

		publish("v2")
		_ = os.RemoveAll(filepath.Join(project, ".agents"))

		out.Reset()
		errb.Reset()
		if code := runInstallFromLock([]string{"--frozen-lockfile"}, project, &out, &errb); code != 0 {
			t.Fatalf("frozen restore exit %d: out=%s err=%s", code, out.String(), errb.String())
		}
		installed, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "alpha", "SKILL.md"))
		if err != nil || !strings.Contains(string(installed), "v1") {
			t.Fatalf("frozen restore body = %q (err=%v), want v1", string(installed), err)
		}
		lockBytesAfter, _ := os.ReadFile(filepath.Join(project, LocalLockFile))
		if string(lockBytesAfter) != string(lockBytesBefore) {
			t.Errorf("frozen restore mutated lockfile:\nbefore:\n%s\nafter:\n%s", lockBytesBefore, lockBytesAfter)
		}

		// Non-frozen restore also keeps the locked commit and does not leak SHA into ref.
		_ = os.RemoveAll(filepath.Join(project, ".agents"))
		out.Reset()
		errb.Reset()
		if code := runInstallFromLock(nil, project, &out, &errb); code != 0 {
			t.Fatalf("non-frozen restore exit %d: out=%s err=%s", code, out.String(), errb.String())
		}
		installed, err = os.ReadFile(filepath.Join(project, ".agents", "skills", "alpha", "SKILL.md"))
		if err != nil || !strings.Contains(string(installed), "v1") {
			t.Fatalf("non-frozen restore body = %q (err=%v), want v1", string(installed), err)
		}
		lockBytesAfter, _ = os.ReadFile(filepath.Join(project, LocalLockFile))
		if string(lockBytesAfter) != string(lockBytesBefore) {
			t.Errorf("non-frozen restore changed lockfile:\nbefore:\n%s\nafter:\n%s", lockBytesBefore, lockBytesAfter)
		}
	})

	t.Run("moves commit forward on project update and pins older entries", func(t *testing.T) {
		_, project, _, _, publish := setup(t)
		v1 := publish("v1")
		nonInt := false
		if code := RunAddWithIO([]string{sourceURL, "--skill", "alpha", "-a", "universal", "-y"}, AddIO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Cwd: project, Interactive: &nonInt}); code != 0 {
			t.Fatalf("add exit %d", code)
		}

		// Simulate an older lock entry without commit -> update pins it to v1.
		lock, _ := ReadLocalLock(project)
		e := lock.Skills["alpha"]
		e.Commit = ""
		lock.Skills["alpha"] = e
		if err := WriteLocalLock(lock, project); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if code := runUpdate([]string{"-p", "-y"}, UpdateModeUpdate, project, &out, &errb, strings.NewReader("")); code != 0 {
			t.Fatalf("update pin exit %d: out=%s err=%s", code, out.String(), errb.String())
		}
		lock, _ = ReadLocalLock(project)
		if lock.Skills["alpha"].Commit != v1 {
			t.Fatalf("pinned commit = %q, want %q", lock.Skills["alpha"].Commit, v1)
		}

		// Upstream moves to v2 -> update moves commit forward.
		v2 := publish("v2")
		out.Reset()
		errb.Reset()
		if code := runUpdate([]string{"-p", "-y"}, UpdateModeUpdate, project, &out, &errb, strings.NewReader("")); code != 0 {
			t.Fatalf("update v2 exit %d: out=%s err=%s", code, out.String(), errb.String())
		}
		lock, _ = ReadLocalLock(project)
		if lock.Skills["alpha"].Commit != v2 {
			t.Fatalf("updated commit = %q, want %q", lock.Skills["alpha"].Commit, v2)
		}
		installed, _ := os.ReadFile(filepath.Join(project, ".agents", "skills", "alpha", "SKILL.md"))
		if !strings.Contains(string(installed), "v2") {
			t.Fatalf("installed body = %q, want v2", string(installed))
		}
	})

	t.Run("records commit in global lock and moves it forward on update", func(t *testing.T) {
		_, project, _, _, publish := setup(t)
		v1 := publish("v1")
		nonInt := false
		if code := RunAddWithIO([]string{sourceURL, "--skill", "alpha", "-g", "-a", "universal", "-y"}, AddIO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Cwd: project, Interactive: &nonInt}); code != 0 {
			t.Fatalf("global add exit %d", code)
		}
		gl, _ := ReadSkillLock()
		if gl.Skills["alpha"].Commit != v1 {
			t.Fatalf("global commit = %q, want %q", gl.Skills["alpha"].Commit, v1)
		}

		v2 := publish("v2")
		var out, errb bytes.Buffer
		if code := runUpdate([]string{"-g", "-y"}, UpdateModeUpdate, project, &out, &errb, strings.NewReader("")); code != 0 {
			t.Fatalf("global update exit %d: out=%s err=%s", code, out.String(), errb.String())
		}
		gl, _ = ReadSkillLock()
		if gl.Skills["alpha"].Commit != v2 {
			t.Fatalf("global updated commit = %q, want %q", gl.Skills["alpha"].Commit, v2)
		}
	})
}

// ===========================================================================
// #2722: Install a skill under a new name with --rename

func TestV190_SetFrontmatterName(t *testing.T) {
	t.Run("replaces name keeping everything else byte for byte", func(t *testing.T) {
		raw := "---\nname: alpha\ndescription: d\nlicense: MIT\n---\n# Body\nname: alpha\n"
		want := "---\nname: beta\ndescription: d\nlicense: MIT\n---\n# Body\nname: alpha\n"
		if got := SetFrontmatterName(raw, "beta"); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("keeps CRLF line endings and BOM", func(t *testing.T) {
		raw := "\uFEFF---\r\nname: alpha\r\ndescription: d\r\n---\r\n# Body\r\n"
		want := "\uFEFF---\r\nname: beta\r\ndescription: d\r\n---\r\n# Body\r\n"
		if got := SetFrontmatterName(raw, "beta"); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("replaces quoted or multi-line name", func(t *testing.T) {
		if got := SetFrontmatterName("---\nname: \"Alpha Skill\"\ndescription: d\n---\n", "beta"); got != "---\nname: beta\ndescription: d\n---\n" {
			t.Errorf("quoted: got %q", got)
		}
		if got := SetFrontmatterName("---\nname: >\n  Alpha\n  Skill\ndescription: d\n---\n", "beta"); got != "---\nname: beta\ndescription: d\n---\n" {
			t.Errorf("multiline: got %q", got)
		}
	})

	t.Run("adds missing name first", func(t *testing.T) {
		if got := SetFrontmatterName("---\ndescription: d\n---\n", "beta"); got != "---\nname: beta\ndescription: d\n---\n" {
			t.Errorf("missing: got %q", got)
		}
	})

	t.Run("matches YAML 1.2 Core Schema quoting", func(t *testing.T) {
		for _, quoted := range []string{"123", "true", "false", "null", "1.5", "0o10", "0x10", "0x1234567890abcdef1234"} {
			renamed := SetFrontmatterName("---\nname: alpha\n---\n", quoted)
			want := fmt.Sprintf("---\nname: %q\n---\n", quoted)
			if renamed != want {
				t.Errorf("SetFrontmatterName(%q) = %q, want %q", quoted, renamed, want)
			}
			data, _, err := ParseFrontmatter(renamed)
			if err != nil {
				t.Fatalf("%q parse error: %v (raw=%q)", quoted, err, renamed)
			}
			if got, ok := data["name"].(string); !ok || got != quoted {
				t.Errorf("%q parsed as %T(%v), want string(%q) (raw=%q)", quoted, data["name"], data["name"], quoted, renamed)
			}
		}
		// YAML 1.2 treats YAML 1.1 booleans, underscored digits, binary literals, and ISO dates as plain strings.
		for _, plain := range []string{"on", "off", "yes", "no", "y", "n", "1_000", "0b101", "0o-7", "2024-01-01"} {
			renamed := SetFrontmatterName("---\nname: alpha\n---\n", plain)
			want := fmt.Sprintf("---\nname: %s\n---\n", plain)
			if renamed != want {
				t.Errorf("SetFrontmatterName(%q) = %q, want %q", plain, renamed, want)
			}
		}
	})

	t.Run("leaves content without frontmatter unchanged", func(t *testing.T) {
		if got := SetFrontmatterName("# Just markdown\n", "beta"); got != "# Just markdown\n" {
			t.Errorf("no frontmatter: got %q", got)
		}
	})
}

func TestV190_ResolveSkillLocations_Renamed(t *testing.T) {
	res := ResolveSkillLocationsWithNames(
		[]string{"beta"},
		map[string]string{"beta": "skills/alpha/SKILL.md"},
		map[string]string{"beta": "alpha"},
		[]DiscoveredSkillLocation{{Name: "alpha", SkillPath: "skills/moved/alpha/SKILL.md"}},
		false,
	)
	if got := res.ResolvedPaths["beta"]; got != "skills/moved/alpha/SKILL.md" {
		t.Fatalf("ResolvedPaths[beta] = %q, want skills/moved/alpha/SKILL.md", got)
	}
	if len(res.DeletedSkills) != 0 {
		t.Fatalf("DeletedSkills = %v, want empty", res.DeletedSkills)
	}
}

func TestV190_AddRename_Lifecycle(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}

	setup := func(t *testing.T) (root, repo, project, home, repoURL string, writeUpstream func(name, body string), commit func()) {
		t.Helper()
		root = t.TempDir()
		repo = filepath.Join(root, "repo")
		project = filepath.Join(root, "project")
		home = filepath.Join(root, "home")
		_ = os.MkdirAll(repo, 0o755)
		_ = os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
		_ = os.MkdirAll(home, 0o755)
		v190HermeticEnv(t, home)

		writeUpstream = func(name, body string) {
			t.Helper()
			dir := filepath.Join(repo, "skills", name)
			_ = os.MkdirAll(dir, 0o755)
			content := fmt.Sprintf("---\nname: %s\ndescription: the %s skill\nlicense: MIT\n---\n%s\n", name, name, body)
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		commit = func() {
			t.Helper()
			v190Git(t, repo, "add", ".")
			v190Git(t, repo, "commit", "-q", "-m", "skills")
		}
		writeUpstream("alpha", "# alpha v1")
		writeUpstream("gamma", "# gamma")
		v190Git(t, repo, "init", "-q")
		commit()
		repoURL = v190FileURL(repo)
		return
	}

	readText := func(t *testing.T, p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	runAdd := func(project string, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		nonInt := false
		code := RunAddWithIO(args, AddIO{Stdout: &out, Stderr: &errb, Cwd: project, Interactive: &nonInt})
		return code, out.String(), errb.String()
	}

	t.Run("installs under new name, verifies hash, lists, updates, removes", func(t *testing.T) {
		_, _, project, _, repoURL, writeUpstream, commit := setup(t)
		code, out, errb := runAdd(project, repoURL, "--skill", "alpha", "--rename", "beta", "-a", "claude-code", "-y")
		if code != 0 {
			t.Fatalf("add exit %d: out=%s err=%s", code, out, errb)
		}
		if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "alpha")); !os.IsNotExist(err) {
			t.Error("alpha directory should not exist")
		}
		claudeBeta := filepath.Join(project, ".claude", "skills", "beta", "SKILL.md")
		wantMd := "---\nname: beta\ndescription: the alpha skill\nlicense: MIT\n---\n# alpha v1\n"
		if got := readText(t, claudeBeta); got != wantMd {
			t.Fatalf("installed SKILL.md =\n%q\nwant:\n%q", got, wantMd)
		}
		lock, err := ReadLocalLock(project)
		if err != nil {
			t.Fatal(err)
		}
		if len(lock.Skills) != 1 || lock.Skills["beta"].SkillName != "alpha" || lock.Skills["beta"].SkillPath != "skills/alpha/SKILL.md" {
			t.Fatalf("unexpected lock: %+v", lock.Skills)
		}
		// Verify upstream key order: computedHash precedes skillName.
		rawLock, _ := os.ReadFile(filepath.Join(project, LocalLockFile))
		hashIdx := strings.Index(string(rawLock), `"computedHash"`)
		skillNameIdx := strings.Index(string(rawLock), `"skillName"`)
		if hashIdx == -1 || skillNameIdx == -1 || hashIdx > skillNameIdx {
			t.Errorf("expected computedHash before skillName in skills-lock.json:\n%s", string(rawLock))
		}

		// List shows (renamed from alpha) and JSON has renamedFrom + status tracked.
		var lout, lerr bytes.Buffer
		if c := runList(nil, project, &lout, &lerr); c != 0 || !strings.Contains(lout.String(), "(renamed from alpha)") {
			t.Fatalf("list exit=%d out=%q err=%q", c, lout.String(), lerr.String())
		}
		lout.Reset()
		lerr.Reset()
		if c := runList([]string{"--json"}, project, &lout, &lerr); c != 0 {
			t.Fatalf("list --json exit=%d err=%q", c, lerr.String())
		}
		var entries []ListJSONEntry
		if err := json.Unmarshal(lout.Bytes(), &entries); err != nil {
			t.Fatalf("unmarshal list json: %v\n%s", err, lout.String())
		}
		if len(entries) != 1 || entries[0].Name != "beta" || entries[0].Status != "tracked" || entries[0].RenamedFrom == nil || *entries[0].RenamedFrom != "alpha" {
			t.Fatalf("list json = %+v", entries)
		}

		// Check reports up to date (no local changes, no update).
		var cout, cerr bytes.Buffer
		if c := runUpdate([]string{"-p"}, UpdateModeCheck, project, &cout, &cerr, strings.NewReader("")); c != 0 {
			t.Fatalf("check up-to-date exit=%d out=%q err=%q", c, cout.String(), cerr.String())
		}

		// Upstream updates alpha -> check reports beta has an update, update keeps beta.
		writeUpstream("alpha", "# alpha v2")
		commit()
		cout.Reset()
		cerr.Reset()
		if c := runUpdate([]string{"-p"}, UpdateModeCheck, project, &cout, &cerr, strings.NewReader("")); c != 1 || !strings.Contains(cout.String(), "beta has an update") {
			t.Fatalf("check outdated exit=%d out=%q err=%q", c, cout.String(), cerr.String())
		}
		cout.Reset()
		cerr.Reset()
		if c := runUpdate([]string{"-p", "-y"}, UpdateModeUpdate, project, &cout, &cerr, strings.NewReader("")); c != 0 || !strings.Contains(cout.String(), "Updated beta") {
			t.Fatalf("update exit=%d out=%q err=%q", c, cout.String(), cerr.String())
		}
		updated := readText(t, claudeBeta)
		if !strings.Contains(updated, "name: beta\n") || !strings.Contains(updated, "# alpha v2") {
			t.Fatalf("updated SKILL.md = %q", updated)
		}
		if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "alpha")); !os.IsNotExist(err) {
			t.Error("alpha should still not exist after update")
		}
		lock, _ = ReadLocalLock(project)
		if lock.Skills["beta"].SkillName != "alpha" {
			t.Fatalf("lock skillName lost after update: %+v", lock.Skills["beta"])
		}

		// Second update says all up to date.
		cout.Reset()
		if c := runUpdate([]string{"-p", "-y"}, UpdateModeUpdate, project, &cout, &cerr, strings.NewReader("")); c != 0 || !strings.Contains(cout.String(), "All project skills are up to date") {
			t.Fatalf("second update exit=%d out=%q", c, cout.String())
		}

		// Remove by installed name.
		var rout, rerr bytes.Buffer
		if c := runRemove([]string{"beta"}, RemoveOptions{Yes: true}, project, &rout, &rerr, strings.NewReader("")); c != 0 {
			t.Fatalf("remove exit=%d out=%q err=%q", c, rout.String(), rerr.String())
		}
		if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "beta")); !os.IsNotExist(err) {
			t.Error("beta should be removed")
		}
		lock, _ = ReadLocalLock(project)
		if len(lock.Skills) != 0 {
			t.Fatalf("lock should be empty after remove, got %+v", lock.Skills)
		}
	})

	t.Run("symlink mode rewrites canonical copy and never touches source repo", func(t *testing.T) {
		_, repo, project, _, repoURL, _, _ := setup(t)
		code, out, errb := runAdd(project, repoURL, "--skill", "alpha", "--rename", "beta", "-a", "claude-code", "-a", "codex", "-y")
		if code != 0 {
			t.Fatalf("add exit %d: out=%s err=%s", code, out, errb)
		}
		canonical := filepath.Join(project, ".agents", "skills", "beta", "SKILL.md")
		if !strings.Contains(readText(t, canonical), "name: beta\n") {
			t.Errorf("canonical missing name: beta")
		}
		link := filepath.Join(project, ".claude", "skills", "beta")
		if runtime.GOOS != "windows" {
			if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
				t.Errorf("expected symlink at %s, err=%v", link, err)
			}
		}
		if !strings.Contains(readText(t, filepath.Join(link, "SKILL.md")), "name: beta\n") {
			t.Errorf("linked skill missing name: beta")
		}
		if !strings.Contains(readText(t, filepath.Join(repo, "skills", "alpha", "SKILL.md")), "name: alpha\n") {
			t.Error("source repo SKILL.md was modified")
		}
	})

	t.Run("restores renamed skill and coexisting as-is skill with frozen-lockfile", func(t *testing.T) {
		_, repo, project, _, _, _, _ := setup(t)
		if c, o, e := runAdd(project, repo, "--skill", "alpha", "--rename", "beta", "-a", "claude-code", "-y"); c != 0 {
			t.Fatalf("add renamed: %d %s %s", c, o, e)
		}
		if c, o, e := runAdd(project, repo, "--skill", "alpha", "-a", "claude-code", "-y"); c != 0 {
			t.Fatalf("add as-is: %d %s %s", c, o, e)
		}
		lockBefore, _ := os.ReadFile(filepath.Join(project, LocalLockFile))
		_ = os.RemoveAll(filepath.Join(project, ".claude", "skills"))
		_ = os.RemoveAll(filepath.Join(project, ".agents"))

		var out, errb bytes.Buffer
		if c := runInstallFromLock([]string{"--agent", "claude-code", "--frozen-lockfile"}, project, &out, &errb); c != 0 {
			t.Fatalf("frozen restore exit %d: out=%s err=%s", c, out.String(), errb.String())
		}
		if !strings.Contains(readText(t, filepath.Join(project, ".claude", "skills", "beta", "SKILL.md")), "name: beta\n") {
			t.Error("restored beta missing name: beta")
		}
		if !strings.Contains(readText(t, filepath.Join(project, ".claude", "skills", "alpha", "SKILL.md")), "name: alpha\n") {
			t.Error("restored alpha missing name: alpha")
		}
		lockAfter, _ := os.ReadFile(filepath.Join(project, LocalLockFile))
		if string(lockAfter) != string(lockBefore) {
			t.Errorf("frozen restore changed lock:\nbefore:\n%s\nafter:\n%s", lockBefore, lockAfter)
		}
	})

	t.Run("missing renamed skill shows renamed label in list", func(t *testing.T) {
		_, repo, project, _, _, _, _ := setup(t)
		if c, o, e := runAdd(project, repo, "--skill", "alpha", "--rename", "beta", "-a", "codex", "-y"); c != 0 {
			t.Fatalf("add: %d %s %s", c, o, e)
		}
		_ = os.RemoveAll(filepath.Join(project, ".agents"))
		var out, errb bytes.Buffer
		if c := runList(nil, project, &out, &errb); c != 0 || !strings.Contains(out.String(), "(renamed from alpha)") {
			t.Fatalf("missing list text: %d out=%q", c, out.String())
		}
		out.Reset()
		if c := runList([]string{"--json"}, project, &out, &errb); c != 0 {
			t.Fatalf("missing list json: %d", c)
		}
		var entries []ListJSONEntry
		if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Status != "missing" || entries[0].RenamedFrom == nil || *entries[0].RenamedFrom != "alpha" {
			t.Fatalf("missing list json = %+v", entries)
		}
	})

	t.Run("refuses multiple skills and invalid rename values before writing", func(t *testing.T) {
		_, _, project, _, repoURL, _, _ := setup(t)
		c, out, errb := runAdd(project, repoURL, "--all", "--rename", "beta")
		if c != 1 || !strings.Contains(out+errb, "--rename installs one skill") {
			t.Fatalf("multi-skill rename: code=%d out=%q err=%q", c, out, errb)
		}
		if _, err := os.Stat(filepath.Join(project, LocalLockFile)); !os.IsNotExist(err) {
			t.Error("lockfile should not be created on multi-skill rename failure")
		}

		c, out, errb = runAdd(project, repoURL, "--skill", "alpha", "--rename", "Bad Name", "-y")
		if c != 1 || !strings.Contains(out+errb, "--rename must be a skill name") || !strings.Contains(out+errb, "bad-name") {
			t.Fatalf("bad rename: code=%d out=%q err=%q", c, out, errb)
		}
	})
}

func TestV190_RenameGlobalUpdateAndWellKnown(t *testing.T) {
	t.Run("updates renamed global skill by source name and keeps new name", func(t *testing.T) {
		if !IsGitInstalled() {
			t.Skip("git not installed")
		}
		root := t.TempDir()
		repo := filepath.Join(root, "repo")
		project := filepath.Join(root, "project")
		home := filepath.Join(root, "home")
		_ = os.MkdirAll(filepath.Join(repo, "skills", "alpha"), 0o755)
		_ = os.MkdirAll(project, 0o755)
		_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
		v190HermeticEnv(t, home)

		const sourceURL = "https://git.example.test/owner/skills.git"
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "url."+v190FileURL(repo)+".insteadOf")
		t.Setenv("GIT_CONFIG_VALUE_0", sourceURL)

		v190Git(t, repo, "init", "-q", "-b", "main")
		writeVer := func(body string) {
			_ = os.WriteFile(filepath.Join(repo, "skills", "alpha", "SKILL.md"),
				[]byte("---\nname: alpha\ndescription: alpha\n---\n"+body+"\n"), 0o644)
			v190Git(t, repo, "add", ".")
			v190Git(t, repo, "commit", "-q", "-m", body)
		}
		writeVer("# v1")

		nonInt := false
		if c := RunAddWithIO([]string{sourceURL, "--skill", "alpha", "--rename", "beta", "-g", "-a", "claude-code", "-y"}, AddIO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Cwd: project, Interactive: &nonInt}); c != 0 {
			t.Fatalf("global add --rename exit %d", c)
		}
		gl, _ := ReadSkillLock()
		if gl.Skills["beta"].SkillName != "alpha" {
			t.Fatalf("global lock = %+v", gl.Skills)
		}

		writeVer("# v2")
		var out, errb bytes.Buffer
		if c := runUpdate([]string{"-g", "-y"}, UpdateModeUpdate, project, &out, &errb, strings.NewReader("")); c != 0 {
			t.Fatalf("global update exit %d: out=%s err=%s", c, out.String(), errb.String())
		}
		if !strings.Contains(out.String(), "Updated beta") {
			t.Fatalf("expected Updated beta, got %q", out.String())
		}
		body, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "beta", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		norm := strings.ReplaceAll(string(body), "\r\n", "\n")
		if !strings.Contains(norm, "name: beta\n") || !strings.Contains(norm, "# v2") {
			t.Fatalf("updated global SKILL.md = %q", norm)
		}
		gl, _ = ReadSkillLock()
		if gl.Skills["beta"].SkillName != "alpha" {
			t.Fatalf("global lock lost skillName: %+v", gl.Skills["beta"])
		}
	})

	t.Run("well-known install with --rename and update keeps new name", func(t *testing.T) {
		version := "v1"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/skills/index.json":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"skills":[{"name":"alpha","description":"wk skill","files":["SKILL.md"]}]}`))
			case "/.well-known/skills/alpha/SKILL.md":
				_, _ = fmt.Fprintf(w, "---\nname: alpha\ndescription: wk skill\n---\n# %s\n", version)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		root := t.TempDir()
		project := filepath.Join(root, "project")
		home := filepath.Join(root, "home")
		_ = os.MkdirAll(project, 0o755)
		_ = os.MkdirAll(home, 0o755)
		v190HermeticEnv(t, home)

		nonInt := false
		if c := RunAddWithIO([]string{srv.URL, "--skill", "alpha", "--rename", "wk-beta", "-a", "codex", "-y"}, AddIO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Cwd: project, Interactive: &nonInt}); c != 0 {
			t.Fatalf("well-known add --rename exit %d", c)
		}
		lock, _ := ReadLocalLock(project)
		if lock.Skills["wk-beta"].SkillName != "alpha" {
			t.Fatalf("well-known lock = %+v", lock.Skills)
		}

		version = "v2"
		var out, errb bytes.Buffer
		if c := runUpdate([]string{"-p", "-y"}, UpdateModeUpdate, project, &out, &errb, strings.NewReader("")); c != 0 {
			t.Fatalf("well-known update exit %d: out=%s err=%s", c, out.String(), errb.String())
		}
		body, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "wk-beta", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "name: wk-beta") || !strings.Contains(string(body), "# v2") {
			t.Fatalf("updated well-known SKILL.md = %q", string(body))
		}
		lock, _ = ReadLocalLock(project)
		if lock.Skills["wk-beta"].SkillName != "alpha" {
			t.Fatalf("well-known lock lost skillName: %+v", lock.Skills["wk-beta"])
		}
	})

	t.Run("direct download with --rename and update keeps new name", func(t *testing.T) {
		version := "v1"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/dl/SKILL.md" {
				_, _ = fmt.Fprintf(w, "---\nname: dlskill\ndescription: direct download\n---\n# %s\n", version)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		root := t.TempDir()
		project := filepath.Join(root, "project")
		home := filepath.Join(root, "home")
		_ = os.MkdirAll(project, 0o755)
		_ = os.MkdirAll(home, 0o755)
		v190HermeticEnv(t, home)

		nonInt := false
		if c := RunAddWithIO([]string{srv.URL + "/dl/SKILL.md", "--rename", "dl-beta", "-a", "codex", "-y"}, AddIO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Cwd: project, Interactive: &nonInt}); c != 0 {
			t.Fatalf("download add --rename exit %d", c)
		}
		lock, _ := ReadLocalLock(project)
		if lock.Skills["dl-beta"].SkillName != "dlskill" {
			t.Fatalf("download lock = %+v", lock.Skills)
		}

		version = "v2"
		var out, errb bytes.Buffer
		if c := runUpdate([]string{"-p", "-y"}, UpdateModeUpdate, project, &out, &errb, strings.NewReader("")); c != 0 {
			t.Fatalf("download update exit %d: out=%s err=%s", c, out.String(), errb.String())
		}
		body, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "dl-beta", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "name: dl-beta") || !strings.Contains(string(body), "# v2") {
			t.Fatalf("updated download SKILL.md = %q", string(body))
		}
		lock, _ = ReadLocalLock(project)
		if lock.Skills["dl-beta"].SkillName != "dlskill" {
			t.Fatalf("download lock lost skillName: %+v", lock.Skills["dl-beta"])
		}
	})
}

// ===========================================================================
// #2750: Serialize local lockfile updates and write atomically

func TestV190_LocalLock_ConcurrencyAndStaleRecovery(t *testing.T) {
	t.Run("retains all entries across concurrent goroutines", func(t *testing.T) {
		dir := t.TempDir()
		names := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
		var wg sync.WaitGroup
		errs := make([]error, len(names))
		for i, name := range names {
			wg.Add(1)
			go func(idx int, n string) {
				defer wg.Done()
				errs[idx] = AddSkillToLocalLock(n, LocalSkillLockEntry{
					Source:       "org/" + n,
					SourceType:   "github",
					ComputedHash: "hash-" + n,
				}, dir)
			}(i, name)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("goroutine %s failed: %v", names[i], err)
			}
		}
		lock, err := ReadLocalLock(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := keysOf(lock.Skills)
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Fatalf("keys = %v, want %v", got, names)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != LocalLockFile {
			t.Fatalf("leftover files in dir: %v", entries)
		}
	})

	t.Run("breaks stale lock file left by crashed process", func(t *testing.T) {
		dir := t.TempDir()
		mutexPath := filepath.Join(dir, LocalLockFile+".lock")
		if err := os.WriteFile(mutexPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-60 * time.Second)
		if err := os.Chtimes(mutexPath, old, old); err != nil {
			t.Fatal(err)
		}

		start := time.Now()
		err := AddSkillToLocalLock("alpha", LocalSkillLockEntry{
			Source:       "org/alpha",
			SourceType:   "github",
			ComputedHash: "hash-alpha",
		}, dir)
		if err != nil {
			t.Fatalf("AddSkillToLocalLock: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("took %v to break stale lock, want < 2s", elapsed)
		}
		lock, _ := ReadLocalLock(dir)
		if lock.Skills["alpha"].Source != "org/alpha" {
			t.Fatalf("lock = %+v", lock.Skills)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 || entries[0].Name() != LocalLockFile {
			t.Fatalf("leftover files: %v", entries)
		}
	})

	t.Run("returns SKILLS_LOCAL_LOCK_BUSY on timeout", func(t *testing.T) {
		dir := t.TempDir()
		mutexPath := filepath.Join(dir, LocalLockFile+".lock")
		if err := os.WriteFile(mutexPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		origTimeout := localLockAcquireTimeout
		localLockAcquireTimeout = 80 * time.Millisecond
		t.Cleanup(func() { localLockAcquireTimeout = origTimeout })

		err := AddSkillToLocalLock("alpha", LocalSkillLockEntry{Source: "org/alpha", SourceType: "github", ComputedHash: "h"}, dir)
		if !IsDiagnostic(err, CodeLocalLockBusy) {
			t.Fatalf("err = %v, want SKILLS_LOCAL_LOCK_BUSY", err)
		}
	})

	t.Run("retains all entries across concurrent OS processes", func(t *testing.T) {
		if os.Getenv("SKL_LOCK_WORKER_DIR") != "" {
			// Invoked as child helper below.
			return
		}
		dir := t.TempDir()
		names := []string{"alpha", "bravo", "charlie", "delta"}
		var wg sync.WaitGroup
		errs := make([]error, len(names))
		for i, name := range names {
			wg.Add(1)
			go func(idx int, n string) {
				defer wg.Done()
				cmd := exec.Command(os.Args[0], "-test.run=^TestV190_LockWorkerHelper$")
				cmd.Env = append(os.Environ(),
					"SKL_LOCK_WORKER_DIR="+dir,
					"SKL_LOCK_WORKER_NAME="+n,
				)
				if out, err := cmd.CombinedOutput(); err != nil {
					errs[idx] = fmt.Errorf("worker %s: %v\n%s", n, err, string(out))
				}
			}(i, name)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		lock, err := ReadLocalLock(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := keysOf(lock.Skills)
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Fatalf("multiproc keys = %v, want %v", got, names)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 || entries[0].Name() != LocalLockFile {
			t.Fatalf("leftover files: %v", entries)
		}
	})
}

func TestV190_LockWorkerHelper(t *testing.T) {
	dir := os.Getenv("SKL_LOCK_WORKER_DIR")
	name := os.Getenv("SKL_LOCK_WORKER_NAME")
	if dir == "" || name == "" {
		t.Skip("helper only")
	}
	if err := AddSkillToLocalLock(name, LocalSkillLockEntry{
		Source:       "org/" + name,
		SourceType:   "github",
		ComputedHash: "hash-" + name,
	}, dir); err != nil {
		t.Fatal(err)
	}
}

func TestV190_SyncPruneKeepsConcurrentLockAdditions(t *testing.T) {
	project := t.TempDir()
	_ = os.MkdirAll(filepath.Join(project, ".agents", "skills", "gone"), 0o755)
	_ = os.WriteFile(filepath.Join(project, ".agents", "skills", "gone", "SKILL.md"),
		[]byte("---\nname: gone\ndescription: removed\n---\n"), 0o644)
	if err := WriteLocalLock(LocalSkillLockFile{
		Version: LocalLockVer,
		Skills: map[string]LocalSkillLockEntry{
			"gone": {Source: "old-lib", SourceType: "node_modules", ComputedHash: "x"},
		},
	}, project); err != nil {
		t.Fatal(err)
	}

	// Read lock at start of sync, then another process adds "other" before prune runs.
	staleSnapshot, err := ReadLocalLock(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddSkillToLocalLock("other", LocalSkillLockEntry{
		Source:       "org/other",
		SourceType:   "github",
		ComputedHash: "hash-other",
	}, project); err != nil {
		t.Fatal(err)
	}

	pruned, err := PruneStaleSkills(project, map[string]bool{}, func(LocalSkillLockEntry, string) bool { return false }, &staleSnapshot, false)
	if err != nil {
		t.Fatalf("PruneStaleSkills: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "gone" {
		t.Fatalf("pruned = %v, want [gone]", pruned)
	}
	finalLock, err := ReadLocalLock(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := finalLock.Skills["gone"]; ok {
		t.Error("gone should have been pruned from lock")
	}
	if finalLock.Skills["other"].Source != "org/other" {
		t.Fatalf("concurrent addition 'other' was lost: %+v", finalLock.Skills)
	}
}

// ===========================================================================
// #2761: Warn when a GitHub rate limit forces the blob fast path to clone

func TestV190_TryBlobInstall_FallbackReason(t *testing.T) {
	var status int
	var headers map[string]string
	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	origBase := githubAPIBaseOverride
	origGh := fpGhBinary
	githubAPIBaseOverride = srv.URL
	fpGhBinary = "nonexistent-gh-binary-for-test"
	t.Cleanup(func() {
		githubAPIBaseOverride = origBase
		fpGhBinary = origGh
		ResetRepoTreeAuthState()
	})

	t.Run("rate-limited", func(t *testing.T) {
		ResetRepoTreeAuthState()
		status = 403
		headers = map[string]string{"Content-Type": "application/json", "X-RateLimit-Remaining": "0"}
		body = `{"message":"API rate limit exceeded"}`
		var got []BlobFallback
		res, _ := TryBlobInstall("vercel/skills", BlobInstallOptions{
			OnFallback: func(fb BlobFallback) { got = append(got, fb) },
		})
		if res != nil || len(got) != 1 || got[0].Reason != BlobFallbackRateLimited {
			t.Fatalf("res=%v got=%+v, want nil + rate-limited", res, got)
		}
	})

	t.Run("tree-unavailable", func(t *testing.T) {
		ResetRepoTreeAuthState()
		status = 404
		headers = map[string]string{"Content-Type": "application/json"}
		body = `{"message":"Not Found"}`
		var got []BlobFallback
		res, _ := TryBlobInstall("vercel/skills", BlobInstallOptions{
			OnFallback: func(fb BlobFallback) { got = append(got, fb) },
		})
		if res != nil || len(got) != 1 || got[0].Reason != BlobFallbackTreeUnavailable {
			t.Fatalf("res=%v got=%+v, want nil + tree-unavailable", res, got)
		}
	})

	t.Run("explicit ref stays silent", func(t *testing.T) {
		ResetRepoTreeAuthState()
		var called bool
		res, _ := TryBlobInstall("vercel/skills", BlobInstallOptions{
			Ref:        "v1.0.0",
			OnFallback: func(BlobFallback) { called = true },
		})
		if res != nil || called {
			t.Fatalf("explicit ref should stay silent: res=%v called=%v", res, called)
		}
	})

	t.Run("repo without skills stays silent", func(t *testing.T) {
		ResetRepoTreeAuthState()
		status = 200
		headers = map[string]string{"Content-Type": "application/json"}
		body = `{"sha":"deadbeef","tree":[{"path":"README.md","type":"blob","sha":"aaaa"}]}`
		var called bool
		res, _ := TryBlobInstall("vercel/skills", BlobInstallOptions{
			OnFallback: func(BlobFallback) { called = true },
		})
		if res != nil || called {
			t.Fatalf("empty skills should stay silent: res=%v called=%v", res, called)
		}
	})

	t.Run("works without callback", func(t *testing.T) {
		ResetRepoTreeAuthState()
		status = 403
		headers = map[string]string{"Content-Type": "application/json", "X-RateLimit-Remaining": "0"}
		body = `{"message":"API rate limit exceeded"}`
		if res, err := TryBlobInstall("vercel/skills", BlobInstallOptions{}); res != nil || err != nil {
			t.Fatalf("res=%v err=%v", res, err)
		}
	})
}

func TestV190_AddBlobFallback_OutputAndJSON(t *testing.T) {
	origFetch := fetchSourceFn
	origLookup := lookupGitHubRepoFn
	lookupGitHubRepoFn = func(ParsedSource) *GitHubRepoMetadata { return nil }
	t.Cleanup(func() {
		fetchSourceFn = origFetch
		lookupGitHubRepoFn = origLookup
	})

	runWithFallback := func(t *testing.T, fb *BlobFallback, extraArgs ...string) (int, string, string) {
		t.Helper()
		root := t.TempDir()
		project := filepath.Join(root, "project")
		srcDir := filepath.Join(root, "src")
		home := filepath.Join(root, "home")
		_ = os.MkdirAll(project, 0o755)
		_ = os.MkdirAll(filepath.Join(srcDir, "skills", "demo"), 0o755)
		_ = os.MkdirAll(home, 0o755)
		_ = os.WriteFile(filepath.Join(srcDir, "skills", "demo", "SKILL.md"),
			[]byte("---\nname: demo\ndescription: demo\n---\n"), 0o644)
		v190HermeticEnv(t, home)

		fetchSourceFn = func(req SourceRequest) (*FetchedSource, error) {
			if req.OnBlobFallback != nil && fb != nil {
				req.OnBlobFallback(*fb)
			}
			return &FetchedSource{
				Input:        req.Source,
				Parsed:       *req.Parsed,
				SourceType:   SourceTypeGitHub,
				Source:       "remotion-dev/skills",
				SourceURL:    "https://github.com/remotion-dev/skills.git",
				RootDir:      srcDir,
				Via:          "clone",
				BlobFallback: fb,
				Skills: []FetchedSkill{{
					Skill:     Skill{Name: "demo", Description: "demo", Path: filepath.Join(srcDir, "skills", "demo")},
					SkillPath: "skills/demo/SKILL.md",
				}},
			}, nil
		}

		args := append([]string{"remotion-dev/skills", "-y", "-a", "codex", "--copy"}, extraArgs...)
		var out, errb bytes.Buffer
		nonInt := false
		code := RunAddWithIO(args, AddIO{Stdout: &out, Stderr: &errb, Cwd: project, Interactive: &nonInt})
		return code, out.String(), errb.String()
	}

	t.Run("warns on rate limit with auth tip when no token", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("GH_TOKEN", "")
		code, out, _ := runWithFallback(t, &BlobFallback{Reason: BlobFallbackRateLimited})
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		if !strings.Contains(out, "rate limit") || !strings.Contains(out, "GITHUB_TOKEN") {
			t.Fatalf("expected rate limit warning + tip, got:\n%s", out)
		}
	})

	t.Run("skips auth tip when GITHUB_TOKEN is set", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "ghp_test")
		t.Setenv("GH_TOKEN", "")
		code, out, _ := runWithFallback(t, &BlobFallback{Reason: BlobFallbackRateLimited})
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		if !strings.Contains(out, "rate limit") {
			t.Fatalf("expected rate limit warning, got:\n%s", out)
		}
		if strings.Contains(out, "GITHUB_TOKEN") {
			t.Fatalf("should skip tip when token set, got:\n%s", out)
		}
	})

	t.Run("stays silent on tree-unavailable", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("GH_TOKEN", "")
		code, out, _ := runWithFallback(t, &BlobFallback{Reason: BlobFallbackTreeUnavailable})
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		if strings.Contains(out, "rate limit") || strings.Contains(out, "GITHUB_TOKEN") {
			t.Fatalf("unexpected warning for tree-unavailable:\n%s", out)
		}
	})

	t.Run("json includes fallback when present and omits when absent", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		code, out, _ := runWithFallback(t, &BlobFallback{Reason: BlobFallbackRateLimited}, "--json")
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		var entries []map[string]interface{}
		if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 1 {
			t.Fatalf("json parse err=%v out=%s", err, out)
		}
		fbMap, ok := entries[0]["fallback"].(map[string]interface{})
		if !ok || fbMap["reason"] != "rate-limited" {
			t.Fatalf("fallback = %#v, want {reason: rate-limited}", entries[0]["fallback"])
		}

		code, out, _ = runWithFallback(t, nil, "--json")
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		entries = nil
		if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 1 {
			t.Fatalf("json parse err=%v out=%s", err, out)
		}
		if _, has := entries[0]["fallback"]; has {
			t.Fatalf("expected fallback omitted, got %#v", entries[0])
		}
	})
}

// ===========================================================================
// #2283: Keep skill links valid when the agent skills dir is a symlink

func TestV190_Installer_SymlinkedAgentSkillsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked agent skills dir test requires POSIX symlinks")
	}

	makeSkillSource := func(t *testing.T, root, name string) string {
		t.Helper()
		dir := filepath.Join(root, "src", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte(fmt.Sprintf("---\nname: %s\ndescription: test\n---\n# %s\n", name, name)), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	for _, tc := range []struct {
		name     string
		segments []string
	}{
		{"deeper", []string{"dotfiles", "config", "claude", "skills"}},
		{"shallower", []string{"skills"}},
	} {
		t.Run("links to canonical dir when agent skills dir is symlink to "+tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			projectDir := filepath.Join(root, "project")
			_ = os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o755)
			realSkillsDir := filepath.Join(append([]string{root}, tc.segments...)...)
			_ = os.MkdirAll(realSkillsDir, 0o755)
			if err := os.Symlink(realSkillsDir, filepath.Join(projectDir, ".claude", "skills")); err != nil {
				t.Fatal(err)
			}

			skillName := "symlinked-agent-dir-skill"
			skillDir := makeSkillSource(t, root, skillName)
			res := InstallSkillForAgent(Skill{Name: skillName, Description: "test", Path: skillDir}, "claude-code", InstallOptions{
				Cwd: projectDir, Mode: InstallModeSymlink, Global: false,
			})
			if !res.Success || res.SymlinkFailed {
				t.Fatalf("install failed: %+v", res)
			}

			claudeSkill := filepath.Join(projectDir, ".claude", "skills", skillName)
			canonicalSkill := filepath.Join(projectDir, ".agents", "skills", skillName)
			if st, err := os.Lstat(claudeSkill); err != nil || st.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("expected symlink at %s: err=%v", claudeSkill, err)
			}
			realClaude, err := filepath.EvalSymlinks(claudeSkill)
			if err != nil {
				t.Fatalf("eval claude link: %v", err)
			}
			realCanonical, err := filepath.EvalSymlinks(canonicalSkill)
			if err != nil {
				t.Fatalf("eval canonical: %v", err)
			}
			if realClaude != realCanonical {
				t.Fatalf("realpath mismatch: %q != %q", realClaude, realCanonical)
			}
			if target, err := os.Readlink(claudeSkill); err != nil || filepath.IsAbs(target) {
				t.Errorf("link should be relative, got %q (err=%v)", target, err)
			}
		})
	}

	t.Run("keeps working link on reinstall", func(t *testing.T) {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		projectDir := filepath.Join(root, "project")
		_ = os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o755)
		realSkillsDir := filepath.Join(root, "dotfiles", "config", "claude", "skills")
		_ = os.MkdirAll(realSkillsDir, 0o755)
		_ = os.Symlink(realSkillsDir, filepath.Join(projectDir, ".claude", "skills"))

		skillName := "reinstalled-skill"
		skill := Skill{Name: skillName, Description: "test", Path: makeSkillSource(t, root, skillName)}
		opts := InstallOptions{Cwd: projectDir, Mode: InstallModeSymlink}
		claudeSkill := filepath.Join(projectDir, ".claude", "skills", skillName)
		canonicalSkill := filepath.Join(projectDir, ".agents", "skills", skillName)

		InstallSkillForAgent(skill, "claude-code", opts)
		_ = os.Remove(claudeSkill)
		_ = os.Symlink(canonicalSkill, claudeSkill)

		res := InstallSkillForAgent(skill, "claude-code", opts)
		if !res.Success {
			t.Fatalf("reinstall failed: %+v", res)
		}
		if got, _ := os.Readlink(claudeSkill); got != canonicalSkill {
			t.Errorf("readlink = %q, want %q (working link preserved)", got, canonicalSkill)
		}
	})

	t.Run("repairs dangling link left by earlier version", func(t *testing.T) {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		projectDir := filepath.Join(root, "project")
		_ = os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o755)
		realSkillsDir := filepath.Join(root, "dotfiles", "config", "claude", "skills")
		_ = os.MkdirAll(realSkillsDir, 0o755)
		claudeSkillsDir := filepath.Join(projectDir, ".claude", "skills")
		_ = os.Symlink(realSkillsDir, claudeSkillsDir)

		skillName := "dangling-link-skill"
		skillDir := makeSkillSource(t, root, skillName)
		claudeSkill := filepath.Join(claudeSkillsDir, skillName)
		canonicalSkill := filepath.Join(projectDir, ".agents", "skills", skillName)

		// Dangling link computed from unresolved claudeSkillsDir instead of realSkillsDir.
		badRel, _ := filepath.Rel(claudeSkillsDir, canonicalSkill)
		if err := os.Symlink(badRel, claudeSkill); err != nil {
			t.Fatal(err)
		}

		res := InstallSkillForAgent(Skill{Name: skillName, Description: "test", Path: skillDir}, "claude-code", InstallOptions{
			Cwd: projectDir, Mode: InstallModeSymlink,
		})
		if !res.Success || res.SymlinkFailed {
			t.Fatalf("reinstall failed: %+v", res)
		}
		realClaude, err := filepath.EvalSymlinks(claudeSkill)
		if err != nil {
			t.Fatalf("repaired link still dangling: %v", err)
		}
		realCanonical, _ := filepath.EvalSymlinks(canonicalSkill)
		if realClaude != realCanonical {
			t.Fatalf("realpath mismatch after repair: %q != %q", realClaude, realCanonical)
		}
	})
}

func TestV190_SyncRenamedRemoteSkills(t *testing.T) {
	project := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	_ = os.MkdirAll(home, 0o755)
	v190HermeticEnv(t, home)

	_ = os.MkdirAll(filepath.Join(project, ".agents", "skills", "beta"), 0o755)
	_ = os.WriteFile(filepath.Join(project, ".agents", "skills", "beta", "SKILL.md"),
		[]byte("---\nname: beta\ndescription: d\n---\n"), 0o644)
	req := FieldRemoteRequest{
		RemoteSkillsRequest: RemoteSkillsRequest{
			Parsed: ParsedSource{Type: SourceTypeGitHub, URL: "https://github.com/owner/repo.git"},
			Skills: []string{"alpha"},
		},
		Via:       ".",
		Declarers: []string{"."},
	}
	lock := LocalSkillLockFile{
		Version: LocalLockVer,
		Skills: map[string]LocalSkillLockEntry{
			"beta": {
				Source:       "owner/repo",
				SourceType:   "github",
				SkillName:    "alpha",
				SkillPath:    "skills/alpha/SKILL.md",
				Via:          ".",
				ComputedHash: "h",
			},
		},
	}
	if !requestsLockedSkill(req, "beta", lock.Skills["beta"]) {
		t.Error("requestsLockedSkill should match renamed skill by source name")
	}
	if !isRemoteInstalled(req, lock, project) {
		t.Error("isRemoteInstalled should recognize installed renamed skill")
	}
	sorted := keysOf(lock.Skills)
	sort.Strings(sorted)
	if len(sorted) != 1 || sorted[0] != "beta" {
		t.Errorf("unexpected lock keys: %v", sorted)
	}

	// End-to-end runSync: when a renamed remote skill's folder is missing on disk,
	// sync reinstalls it under its renamed lock key ("beta") instead of "alpha".
	_ = os.RemoveAll(filepath.Join(project, ".agents"))
	_ = os.WriteFile(filepath.Join(project, "package.json"),
		[]byte(`{"name":"app","skills":[{"source":"owner/repo","skills":["alpha"]}]}`), 0o644)
	if err := WriteLocalLock(lock, project); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(t.TempDir(), "alpha")
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: d\n---\n# body\n"), 0o644)

	origSyncFetch := syncFetch
	syncFetch = func(req SourceRequest) (*FetchedSource, error) {
		return &FetchedSource{
			Input:      req.Source,
			Parsed:     *req.Parsed,
			SourceType: SourceTypeGitHub,
			Source:     "owner/repo",
			SourceURL:  "https://github.com/owner/repo.git",
			RootDir:    filepath.Dir(srcDir),
			Via:        "clone",
			Skills: []FetchedSkill{{
				Skill:     Skill{Name: "alpha", Description: "d", Path: srcDir},
				SkillPath: "skills/alpha/SKILL.md",
			}},
		}, nil
	}
	t.Cleanup(func() { syncFetch = origSyncFetch })

	var out, errb bytes.Buffer
	frozenFalse := false
	code := runSync(SyncOptions{Yes: true, Agent: []AgentType{"codex"}, FrozenLockfile: &frozenFalse}, project, &out, &errb)
	if code != 0 {
		t.Fatalf("runSync exit %d: out=%s err=%s", code, out.String(), errb.String())
	}
	body, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "beta", "SKILL.md"))
	if err != nil {
		t.Fatalf("expected renamed skill restored at beta: %v (out=%s err=%s)", err, out.String(), errb.String())
	}
	if !strings.Contains(string(body), "name: beta") {
		t.Errorf("restored SKILL.md = %q, want name: beta", string(body))
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Error("alpha should not be created when beta is locked as renamed from alpha")
	}
}

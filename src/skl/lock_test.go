package skl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestLockFileOperations(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Test GetSkillLockPath with XDG_STATE_HOME
	t.Setenv("XDG_STATE_HOME", tmpDir)
	globalPath := GetSkillLockPath()
	expectedGlobalPath := filepath.Join(tmpDir, "skills", ".skill-lock.json")
	if globalPath != expectedGlobalPath {
		t.Errorf("GetSkillLockPath() = %q, want %q", globalPath, expectedGlobalPath)
	}

	// 2. Test GetLocalLockPath
	localPath := GetLocalLockPath(tmpDir)
	expectedLocalPath := filepath.Join(tmpDir, "skills-lock.json")
	if localPath != expectedLocalPath {
		t.Errorf("GetLocalLockPath() = %q, want %q", localPath, expectedLocalPath)
	}

	// 3. Test Read empty / missing global lock
	lock, err := ReadSkillLock()
	if err != nil {
		t.Fatalf("ReadSkillLock empty failed: %v", err)
	}
	if lock.Version != GlobalLockVer {
		t.Errorf("expected empty lock version %d, got %d", GlobalLockVer, lock.Version)
	}

	// 4. Test AddSkillToLock & WriteSkillLock with upstream v1.7.6 fields
	entry := SkillLockEntry{
		Source:          "github.com/foo/bar",
		SourceType:      "download",
		SourceUrl:       "https://downloads.example.com/skills/alpha.md?foo=1&bar=2",
		Ref:             "main",
		SkillPath:       "skills/alpha/SKILL.md",
		SkillFolderHash: "abc123hash",
		PluginName:      "my-plugin",
		SourceBaseUrl:   "https://downloads.example.com",
		WellKnownDigest: "sha256:deadbeef",
		InstallMode:     "copy",
	}
	if err := AddSkillToLock("test-skill", entry); err != nil {
		t.Fatalf("AddSkillToLock failed: %v", err)
	}

	gotSingle, err := GetSkillFromLock("test-skill")
	if err != nil || gotSingle == nil {
		t.Fatalf("GetSkillFromLock failed: %v", err)
	}
	if gotSingle.InstallMode != "copy" || gotSingle.WellKnownDigest != "sha256:deadbeef" || gotSingle.SkillPath != "skills/alpha/SKILL.md" {
		t.Errorf("saved global entry mismatch: %+v", gotSingle)
	}

	// Check GetSkillsBySource, DismissPrompt, SaveSelectedAgents
	bySrc, err := GetSkillsBySource()
	if err != nil || len(bySrc["github.com/foo/bar"].Skills) != 1 {
		t.Errorf("GetSkillsBySource mismatch: %v, %v", bySrc, err)
	}
	if IsPromptDismissed("findSkillsPrompt") {
		t.Error("expected findSkillsPrompt initially false")
	}
	if err := DismissPrompt("findSkillsPrompt"); err != nil {
		t.Fatalf("DismissPrompt failed: %v", err)
	}
	if !IsPromptDismissed("findSkillsPrompt") {
		t.Error("expected findSkillsPrompt true after DismissPrompt")
	}
	if err := SaveSelectedAgents([]string{"antigravity", "claude-code"}); err != nil {
		t.Fatalf("SaveSelectedAgents failed: %v", err)
	}
	if sel, err := GetLastSelectedAgents(); err != nil || !reflect.DeepEqual(sel, []string{"antigravity", "claude-code"}) {
		t.Errorf("GetLastSelectedAgents() = %v, %v", sel, err)
	}

	// 5. Test RemoveSkillFromLock
	removed, err := RemoveSkillFromLock("test-skill")
	if err != nil || !removed {
		t.Fatalf("RemoveSkillFromLock = (%v, %v), want (true, nil)", removed, err)
	}

	// 6. Test Local Lock Operations (portable local paths, sorting, new fields)
	localEntry := LocalSkillLockEntry{
		Source:          filepath.Join(tmpDir, ".skills"),
		SourceType:      "local",
		ComputedHash:    "localhash456",
		InstallMode:     "copy",
		WellKnownDigest: "digest123",
	}
	if err := AddSkillToLocalLock("zebra-skill", localEntry, tmpDir); err != nil {
		t.Fatalf("AddSkillToLocalLock zebra failed: %v", err)
	}
	if err := AddSkillToLocalLock("alpha-skill", LocalSkillLockEntry{
		Source:       "org/a",
		SourceUrl:    "https://gitlab.example.com/org/a.git",
		Ref:          "v1.0.0",
		SourceType:   "gitlab",
		SkillPath:    "skills/alpha/SKILL.md",
		ComputedHash: "alphahash",
		InstallMode:  "symlink",
		Subagents:    []string{"", "reviewer"},
	}, tmpDir); err != nil {
		t.Fatalf("AddSkillToLocalLock alpha failed: %v", err)
	}

	rawLocalBytes, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read raw skills-lock.json: %v", err)
	}
	rawLocalStr := string(rawLocalBytes)
	if !strings.HasSuffix(rawLocalStr, "\n") {
		t.Error("expected skills-lock.json to end with trailing newline")
	}
	alphaIdx := strings.Index(rawLocalStr, `"alpha-skill"`)
	zebraIdx := strings.Index(rawLocalStr, `"zebra-skill"`)
	if alphaIdx == -1 || zebraIdx == -1 || alphaIdx >= zebraIdx {
		t.Errorf("expected sorted keys alpha-skill before zebra-skill in:\n%s", rawLocalStr)
	}
	// Verify portable local path "./.skills" on disk
	var rawParsed map[string]interface{}
	_ = json.Unmarshal(rawLocalBytes, &rawParsed)
	skillsMap := rawParsed["skills"].(map[string]interface{})
	zebraRaw := skillsMap["zebra-skill"].(map[string]interface{})
	if zebraRaw["source"] != "./.skills" {
		t.Errorf("expected portable local source './.skills', got %v", zebraRaw["source"])
	}

	// Read back resolves relative local source to absolute path
	localLock, err := ReadLocalLock(tmpDir)
	if err != nil {
		t.Fatalf("ReadLocalLock back failed: %v", err)
	}
	if localLock.Skills["zebra-skill"].Source != filepath.Join(tmpDir, ".skills") {
		t.Errorf("expected resolved local source %q, got %q", filepath.Join(tmpDir, ".skills"), localLock.Skills["zebra-skill"].Source)
	}
	if localLock.Skills["zebra-skill"].InstallMode != "copy" {
		t.Errorf("expected installMode 'copy', got %q", localLock.Skills["zebra-skill"].InstallMode)
	}

	// Prototype-named skills safety
	protoEntry := LocalSkillLockEntry{Source: "org/proto", SourceType: "github", ComputedHash: "protohash"}
	if err := AddSkillToLocalLock("__proto__", protoEntry, tmpDir); err != nil {
		t.Fatalf("AddSkillToLocalLock __proto__ failed: %v", err)
	}
	localLock, err = ReadLocalLock(tmpDir)
	if err != nil {
		t.Fatalf("ReadLocalLock after __proto__ failed: %v", err)
	}
	if _, ok := localLock.Skills["__proto__"]; !ok {
		t.Error("expected __proto__ entry preserved in local lock")
	}
	if rem, err := RemoveSkillFromLocalLock("constructor", tmpDir); err != nil || rem {
		t.Errorf("expected removing non-existent 'constructor' to return (false, nil), got (%v, %v)", rem, err)
	}
	if rem, err := RemoveSkillFromLocalLock("__proto__", tmpDir); err != nil || !rem {
		t.Errorf("expected removing '__proto__' to return (true, nil), got (%v, %v)", rem, err)
	}

	// 7. Test ComputeSkillFolderHash
	testSkillDir := filepath.Join(tmpDir, "my-skill")
	_ = os.MkdirAll(filepath.Join(testSkillDir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(testSkillDir, "SKILL.md"), []byte("skill markdown"), 0644)
	_ = os.WriteFile(filepath.Join(testSkillDir, "sub", "helper.py"), []byte("print('hello')"), 0644)
	_ = os.MkdirAll(filepath.Join(testSkillDir, ".git"), 0755)
	_ = os.WriteFile(filepath.Join(testSkillDir, ".git", "config"), []byte("git config"), 0644)

	hash1, err := ComputeSkillFolderHash(testSkillDir)
	if err != nil || hash1 == "" {
		t.Fatalf("ComputeSkillFolderHash failed: %q, %v", hash1, err)
	}
	_ = os.WriteFile(filepath.Join(testSkillDir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644)
	hash2, err := ComputeSkillFolderHash(testSkillDir)
	if err != nil || hash1 != hash2 {
		t.Errorf("ignored files changed folder hash: %s vs %s (%v)", hash1, hash2, err)
	}
}

func TestInvalidLocalLockAndWriteFailures(t *testing.T) {
	// Invalid merge-conflicted lockfile
	conflictDir := t.TempDir()
	conflicted := "<<<<<<< HEAD\n{\"version\":1,\"skills\":{}}\n=======\n{\"version\":1,\"skills\":{}}\n>>>>>>> branch\n"
	_ = os.WriteFile(filepath.Join(conflictDir, "skills-lock.json"), []byte(conflicted), 0644)
	if _, err := ReadLocalLock(conflictDir); !errors.Is(err, ErrInvalidLocalLock) {
		t.Errorf("expected ErrInvalidLocalLock on merge conflict, got %v", err)
	}

	// Missing skills object
	missingDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(missingDir, "skills-lock.json"), []byte(`{"version":1}`), 0644)
	if _, err := ReadLocalLock(missingDir); !errors.Is(err, ErrInvalidLocalLock) {
		t.Errorf("expected ErrInvalidLocalLock on missing skills key, got %v", err)
	}

	// Unsupported lock version
	v2Dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(v2Dir, "skills-lock.json"), []byte(`{"version":2,"skills":{}}`), 0644)
	if _, err := ReadLocalLock(v2Dir); !errors.Is(err, ErrInvalidLocalLock) {
		t.Errorf("expected ErrInvalidLocalLock on version 2, got %v", err)
	}

	// Write failure (#2606) when lock file is read-only (skip if running as root)
	if os.Geteuid() != 0 {
		roDir := t.TempDir()
		roLocal := filepath.Join(roDir, "skills-lock.json")
		_ = os.WriteFile(roLocal, []byte(`{"version":1,"skills":{}}`), 0444)
		defer os.Chmod(roLocal, 0644)

		if err := AddSkillToLocalLock("alpha", LocalSkillLockEntry{
			Source: "org/a", SourceType: "github", ComputedHash: "h",
		}, roDir); err == nil {
			t.Error("expected error writing read-only skills-lock.json, got nil")
		}

		xdgState := t.TempDir()
		t.Setenv("XDG_STATE_HOME", xdgState)
		roGlobalDir := filepath.Join(xdgState, "skills")
		_ = os.MkdirAll(roGlobalDir, 0755)
		roGlobal := filepath.Join(roGlobalDir, ".skill-lock.json")
		_ = os.WriteFile(roGlobal, []byte(`{"version":3,"skills":{}}`), 0444)
		defer os.Chmod(roGlobal, 0644)

		if err := AddSkillToLock("alpha", SkillLockEntry{
			Source: "org/a", SourceType: "github", SourceUrl: "https://github.com/org/a", SkillFolderHash: "h",
		}); err == nil {
			t.Error("expected error writing read-only .skill-lock.json, got nil")
		}
	}

	// LockChangeReason & FrozenLockfileError & GetGitHubToken
	lock := LocalSkillLockFile{
		Version: 1,
		Skills: map[string]LocalSkillLockEntry{
			"same": {ComputedHash: "h1", Version: "1.0.0"},
		},
	}
	if r := LockChangeReason(lock, "same", LocalSkillLockEntry{ComputedHash: "h1", Version: "1.0.0"}); r != "" {
		t.Errorf("expected empty LockChangeReason, got %q", r)
	}
	if r := LockChangeReason(lock, "missing", LocalSkillLockEntry{ComputedHash: "h1"}); !strings.Contains(r, "not in skills-lock.json") {
		t.Errorf("unexpected LockChangeReason for missing: %q", r)
	}
	if r := LockChangeReason(lock, "same", LocalSkillLockEntry{ComputedHash: "h2", Version: "1.0.0"}); !strings.Contains(r, "has changed") {
		t.Errorf("unexpected LockChangeReason for hash diff: %q", r)
	}
	if err := FrozenLockfileError([]string{"alpha is missing"}); !strings.Contains(err.Error(), "--no-frozen-lockfile") {
		t.Errorf("unexpected FrozenLockfileError: %v", err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_primary")
	t.Setenv("GH_TOKEN", "gho_fallback")
	if got := GetGitHubToken(); got != "ghp_primary" {
		t.Errorf("GetGitHubToken() = %q, want ghp_primary", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if got := GetGitHubToken(); got != "gho_fallback" {
		t.Errorf("GetGitHubToken() fallback = %q, want gho_fallback", got)
	}
}

func TestSkillsShTreeHashAndGitTreeHash(t *testing.T) {
	// 1. Exact vector from upstream tests/pack-pointers.test.ts (lib/pack-tree-hash.ts)
	gotVec := ComputeSkillsShTreeHashFromFiles([]SkillTreeFile{
		{Path: "SKILL.md", Contents: "---\nname: demo\n---\n"},
		{Path: "kit/a.tsx", Contents: "export const a = 1;"},
		{Path: "é.md", Contents: "ünï\x00code"},
	})
	wantVec := "bdba12bbdf840b14d7670ebaa0585309bf922a4b0a070f220038cffbd90569cc"
	if gotVec != wantVec {
		t.Errorf("ComputeSkillsShTreeHashFromFiles() = %q, want %q", gotVec, wantVec)
	}

	// 2. Distinguishes trees that a NUL-joined hash cannot
	hA := ComputeSkillsShTreeHashFromFiles([]SkillTreeFile{{Path: "a.md", Contents: "x\x00b.md\x00y"}})
	hB := ComputeSkillsShTreeHashFromFiles([]SkillTreeFile{
		{Path: "a.md", Contents: "x"},
		{Path: "b.md", Contents: "y"},
	})
	if hA == hB {
		t.Error("expected unambiguous length-prefixed tree hashes to differ")
	}

	// 3. CollectSkillsShFiles & ComputeSkillTreeHash skip binaries, symlinks, and excluded dirs
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\n---\n"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "kit"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "kit", "a.tsx"), []byte("export const a = 1;"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "logo.png"), []byte{0x89, 0x50}, 0644)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "x.js"), []byte("ignored"), 0644)
	_ = os.Symlink(filepath.Join(dir, "kit", "a.tsx"), filepath.Join(dir, "link.tsx"))

	collected, err := CollectSkillsShFiles(dir)
	if err != nil {
		t.Fatalf("CollectSkillsShFiles failed: %v", err)
	}
	var paths []string
	for _, f := range collected {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"SKILL.md", "kit/a.tsx"}) {
		t.Errorf("CollectSkillsShFiles paths = %v, want [SKILL.md kit/a.tsx]", paths)
	}
	if h, err := ComputeSkillTreeHash(dir); err != nil || len(h) != 64 {
		t.Errorf("ComputeSkillTreeHash = (%q, %v)", h, err)
	}

	// 4. Verify ComputeGitTreeHash & GetGitRepoTreeHash match real `git` SHA-1 tree hashes byte-for-byte
	if _, err := exec.LookPath("git"); err == nil {
		repoDir := t.TempDir()
		skillSub := filepath.Join(repoDir, "skills", "demo")
		_ = os.MkdirAll(filepath.Join(skillSub, "scripts"), 0755)
		_ = os.WriteFile(filepath.Join(skillSub, "SKILL.md"), []byte("---\nname: demo\ndescription: d\n---\n"), 0644)
		_ = os.WriteFile(filepath.Join(skillSub, "scripts", "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0755)

		runGit := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
			cmd.Env = append(os.Environ(),
				"GIT_AUTHOR_NAME=Test",
				"GIT_AUTHOR_EMAIL=test@example.com",
				"GIT_COMMITTER_NAME=Test",
				"GIT_COMMITTER_EMAIL=test@example.com",
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v failed: %v\n%s", args, err, out)
			}
		}
		runGit("init")
		runGit("add", ".")
		runGit("commit", "-m", "init")

		wantSHA := GetGitRepoTreeHash(repoDir, "skills/demo/SKILL.md")
		if len(wantSHA) != 40 {
			t.Fatalf("expected 40-char git tree SHA from GetGitRepoTreeHash, got %q", wantSHA)
		}
		gotPureGoSHA, err := ComputeGitTreeHash(skillSub)
		if err != nil {
			t.Fatalf("ComputeGitTreeHash failed: %v", err)
		}
		if gotPureGoSHA != wantSHA {
			t.Errorf("ComputeGitTreeHash() = %q, want git rev-parse SHA %q", gotPureGoSHA, wantSHA)
		}
	}
}

// Expected values in the tests below were produced by Node 24 running the upstream logic
// (localeCompare + sha256 from src/local-lock.ts, Buffer.toString('utf8'), JSON.stringify).

func TestJSLocaleCompareMatchesNodeOrder(t *testing.T) {
	want := []string{"_meta.json", "a_b.md", "a-b.md", "a.b.md", "alpha.md", "README.md",
		"references/api.md", "scripts/run.py", "SKILL.md", "Zeta.md"}
	got := []string{"Zeta.md", "SKILL.md", "scripts/run.py", "references/api.md", "README.md",
		"alpha.md", "a.b.md", "a-b.md", "a_b.md", "_meta.json"}
	sort.SliceStable(got, func(i, j int) bool { return jsLocaleCompare(got[i], got[j]) < 0 })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("localeCompare order = %q, want %q", got, want)
	}

	pairs := []struct {
		a, b string
		want int
	}{
		{"a", "A", -1},      // case is a tertiary difference, lowercase first
		{"ab", "Aa", 1},     // primary difference beats case
		{"_", "-", -1},      // punctuation order
		{"~", "0", -1},      // punctuation < digits
		{"9", "a", -1},      // digits < letters
		{"10", "9", -1},     // no numeric collation
		{"a\x01b", "ab", 0}, // C0 controls are ignorable
		{"a b", "a\tb", 1},  // whitespace is not ignorable
		{"scripts/b", "Scripts/a", 1},
	}
	for _, p := range pairs {
		if got := jsLocaleCompare(p.a, p.b); got != p.want {
			t.Errorf("jsLocaleCompare(%q, %q) = %d, want %d", p.a, p.b, got, p.want)
		}
	}
}

func TestComputeSkillFolderHashUsesLocaleOrder(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
		// order, when set, is the expected hash input order of files.
		order []string
		// caseOnly marks fixtures with paths that differ only by case. They
		// collide on case-insensitive filesystems (APFS, NTFS), so the on-disk
		// check is skipped there; the in-memory check still runs everywhere.
		caseOnly bool
	}{
		{
			name:  "lowercase subdirectory sorts before SKILL.md",
			files: map[string]string{"SKILL.md": "a", "references/x.md": "b"},
			want:  "e232e2bdb740d26ee0164d3e6dc2a2751955f2b865ccd16eb3e53c71b75b881f",
		},
		{
			name: "punctuation, case and nested paths",
			files: map[string]string{"_meta.json": "1", "a_b.md": "2", "a-b.md": "3", "a.b.md": "4",
				"alpha.md": "5", "README.md": "6", "references/api.md": "7", "scripts/run.py": "8",
				"SKILL.md": "9", "Zeta.md": "10"},
			want: "f5c02e77b5e056d2bf53de307b65cb37998b325e29ce8b37b13e1afa22e01ac2",
		},
		{
			name: "digits, case-only directory differences and tilde",
			files: map[string]string{"SKILL.md": "# s\n", "Scripts/a.sh": "x", "scripts/b.sh": "y",
				"assets/10.txt": "t", "assets/9.txt": "n", "assets/A.txt": "u", "~notes.md": "z"},
			want: "f2b84886401697e26395c04b2cfe4d6a64a4766991d171c3024946df6231ecc4",
			order: []string{"~notes.md", "assets/10.txt", "assets/9.txt", "assets/A.txt",
				"Scripts/a.sh", "scripts/b.sh", "SKILL.md"},
			caseOnly: true,
		},
	}
	caseInsensitive := isCaseInsensitiveFS(t)
	for _, tt := range tests {
		// In memory: sort with the same comparator as ComputeSkillFolderHash and
		// hash, so ordering and expected digests are covered on every OS.
		t.Run(tt.name+"/in-memory", func(t *testing.T) {
			paths := make([]string, 0, len(tt.files))
			for rel := range tt.files {
				paths = append(paths, rel)
			}
			sort.SliceStable(paths, func(i, j int) bool {
				if c := jsLocaleCompare(paths[i], paths[j]); c != 0 {
					return c < 0
				}
				return paths[i] < paths[j]
			})
			if tt.order != nil && !reflect.DeepEqual(paths, tt.order) {
				t.Errorf("order = %q, want %q", paths, tt.order)
			}
			h := sha256.New()
			for _, rel := range paths {
				h.Write([]byte(rel))
				h.Write([]byte(tt.files[rel]))
			}
			if got := hex.EncodeToString(h.Sum(nil)); got != tt.want {
				t.Errorf("in-memory hash = %s, want %s", got, tt.want)
			}
		})
		t.Run(tt.name, func(t *testing.T) {
			if tt.caseOnly && caseInsensitive {
				t.Skip("case-only path differences collide on a case-insensitive filesystem; covered by the in-memory subtest")
			}
			dir := t.TempDir()
			for rel, content := range tt.files {
				p := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ComputeSkillFolderHash(dir)
			if err != nil {
				t.Fatalf("ComputeSkillFolderHash: %v", err)
			}
			if got != tt.want {
				t.Errorf("ComputeSkillFolderHash = %s, want %s", got, tt.want)
			}
		})
	}
}

// isCaseInsensitiveFS reports whether the filesystem backing t.TempDir()
// treats paths that differ only by case as the same file.
func isCaseInsensitiveFS(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "case-probe"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return err == nil
}

func TestDecodeUTF8WHATWG(t *testing.T) {
	tests := []struct{ in, want string }{
		{"61e28241ffff", "61efbfbd41efbfbdefbfbd"},
		{"f09080", "efbfbd"},
		{"eda08041", "efbfbdefbfbdefbfbd41"},
		{"e08080", "efbfbdefbfbdefbfbd"},
		{"f4908080", "efbfbdefbfbdefbfbdefbfbd"},
		{"c0aff09f9880f09f98", "efbfbdefbfbdf09f9880efbfbd"},
		{"f09f9880", "f09f9880"},
	}
	for _, tt := range tests {
		in, _ := hex.DecodeString(tt.in)
		if got := hex.EncodeToString([]byte(decodeUTF8WHATWG(in))); got != tt.want {
			t.Errorf("decodeUTF8WHATWG(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte{0x61, 0xe2, 0x82, 0x41, 0xff, 0xff}, 0644); err != nil {
		t.Fatal(err)
	}
	files, err := CollectSkillsShFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Contents != "a\uFFFDA\uFFFD\uFFFD" {
		t.Errorf("CollectSkillsShFiles = %q, want contents %q", files, "a\uFFFDA\uFFFD\uFFFD")
	}
}

func TestGlobalLockEntryTimestampsLastWithMilliseconds(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := AddSkillToLock("s", SkillLockEntry{Source: "o/r", SourceType: "github",
		SourceUrl: "https://github.com/o/r.git", SkillFolderHash: "h", PluginName: "p",
		InstallMode: "copy"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(GetSkillLockPath())
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`"installMode": "copy",\n      "installedAt": "\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z",\n      "updatedAt": "\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z"\n    }`)
	if !re.Match(data) {
		t.Errorf("timestamps are not last / not toISOString format:\n%s", data)
	}
	if bytes.HasSuffix(data, []byte("\n")) {
		t.Error("global lock must not end with a newline")
	}
}

func TestGlobalLockRoundTripMatchesUpstream(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := GetSkillLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	const input = `{"version":3,"futureTop":{"n":1.50,"big":1e21,"small":0.0000001,"arr":[]},"skills":{"zeta":{"source":"o/z","sourceType":"github","sourceUrl":"https://github.com/o/z.git","skillFolderHash":"z1","installedAt":"2025-01-01T00:00:00Z","updatedAt":"2025-01-02T00:00:00Z","pluginName":"p","futureField":"keep"},"10":{"source":"o/n","sourceType":"github","sourceUrl":"u","skillFolderHash":"","installedAt":"a","updatedAt":"b"},"alpha":{"source":"o/a","sourceType":"local","sourceUrl":"/x/<a>&\u2028","skillFolderHash":"a1","installedAt":"2025-01-01T00:00:00.000Z","updatedAt":"2025-01-01T00:00:00.000Z"}},"dismissed":{"otherPrompt":true}}`
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	lock, err := ReadSkillLock()
	if err != nil {
		t.Fatal(err)
	}
	lock.Skills["alpha"] = SkillLockEntry{Source: "o/a", SourceType: "local", SourceUrl: "/x/<a>&\u2028",
		SkillFolderHash: "a2", InstalledAt: "2025-01-01T00:00:00.000Z", UpdatedAt: "2026-11-09T23:27:10.123Z"}
	lock.Skills["beta"] = SkillLockEntry{Source: "o/b", SourceType: "github",
		SourceUrl: "https://github.com/o/b.git", SkillPath: "skills/b/SKILL.md", SkillFolderHash: "b1",
		InstallMode: "copy", InstalledAt: "2026-11-09T23:27:10.123Z", UpdatedAt: "2026-11-09T23:27:10.123Z"}
	lock.Dismissed.FindSkillsPrompt = true
	if err := WriteSkillLock(lock); err != nil {
		t.Fatal(err)
	}

	// Node: l = JSON.parse(input); l.skills.alpha = {...}; l.skills.beta = {...};
	// l.dismissed.findSkillsPrompt = true; JSON.stringify(l, null, 2)
	want := `{
  "version": 3,
  "futureTop": {
    "n": 1.5,
    "big": 1e+21,
    "small": 1e-7,
    "arr": []
  },
  "skills": {
    "10": {
      "source": "o/n",
      "sourceType": "github",
      "sourceUrl": "u",
      "skillFolderHash": "",
      "installedAt": "a",
      "updatedAt": "b"
    },
    "zeta": {
      "source": "o/z",
      "sourceType": "github",
      "sourceUrl": "https://github.com/o/z.git",
      "skillFolderHash": "z1",
      "installedAt": "2025-01-01T00:00:00Z",
      "updatedAt": "2025-01-02T00:00:00Z",
      "pluginName": "p",
      "futureField": "keep"
    },
    "alpha": {
      "source": "o/a",
      "sourceType": "local",
      "sourceUrl": "/x/<a>&` + "\u2028" + `",
      "skillFolderHash": "a2",
      "installedAt": "2025-01-01T00:00:00.000Z",
      "updatedAt": "2026-11-09T23:27:10.123Z"
    },
    "beta": {
      "source": "o/b",
      "sourceType": "github",
      "sourceUrl": "https://github.com/o/b.git",
      "skillPath": "skills/b/SKILL.md",
      "skillFolderHash": "b1",
      "installMode": "copy",
      "installedAt": "2026-11-09T23:27:10.123Z",
      "updatedAt": "2026-11-09T23:27:10.123Z"
    }
  },
  "dismissed": {
    "otherPrompt": true,
    "findSkillsPrompt": true
  }
}`
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("global lock mismatch\n got: %s\nwant: %s", got, want)
	}

	bySrc, err := GetSkillsBySource()
	if err != nil {
		t.Fatal(err)
	}
	if len(bySrc) != 4 || bySrc["o/z"].Entry.PluginName != "p" {
		t.Errorf("GetSkillsBySource = %+v", bySrc)
	}
}

func TestLocalLockDoesNotEscapeHTMLOrLineSeparators(t *testing.T) {
	cwd := t.TempDir()
	if err := AddSkillToLocalLock("s", LocalSkillLockEntry{Source: "o/r", SourceType: "github",
		SourceUrl: "https://x/<a>&b\u2028", ComputedHash: "h"}, cwd); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(GetLocalLockPath(cwd))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"sourceUrl\": \"https://x/<a>&b\u2028\"") {
		t.Errorf("local lock escaped characters JSON.stringify writes verbatim:\n%s", data)
	}
}

// Review #41 item 8c: GetGitRepoTreeHash must be the hardened GetGitTreeHash (timeout, fpGitBinary,
// overridden env), not a second exec.Command("git") implementation.
func TestGetGitRepoTreeHashDelegatesToGetGitTreeHash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "skills", "demo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "skills", "demo", "SKILL.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, p := range []string{"skills/demo/SKILL.md", `skills\demo\SKILL.md`, "SKILL.md"} {
		got, want := GetGitRepoTreeHash(repoDir, p), GetGitTreeHash(repoDir, p)
		if got != want || len(got) != 40 {
			t.Errorf("GetGitRepoTreeHash(%q) = %q, GetGitTreeHash = %q", p, got, want)
		}
	}

	orig := fpGitBinary
	fpGitBinary = filepath.Join(t.TempDir(), "no-such-git")
	defer func() { fpGitBinary = orig }()
	if got := GetGitRepoTreeHash(repoDir, "skills/demo/SKILL.md"); got != "" {
		t.Errorf("GetGitRepoTreeHash ignored fpGitBinary override, got %q", got)
	}
}

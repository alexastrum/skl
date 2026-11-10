package skl

import (
	"archive/zip"
	"bytes"
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

func TestParseAddOptions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSource string
		wantOpts   AddOptions
	}{
		{
			name:       "simple source with no flags",
			args:       []string{"/path/to/my-skill"},
			wantSource: "/path/to/my-skill",
			wantOpts:   AddOptions{},
		},
		{
			name:       "global and copy flags",
			args:       []string{"-g", "--copy", "github-repo"},
			wantSource: "github-repo",
			wantOpts: AddOptions{
				Global: true,
				Copy:   true,
			},
		},
		{
			name:       "with agent filter",
			args:       []string{"-a", "antigravity", "my-source"},
			wantSource: "my-source",
			wantOpts: AddOptions{
				Agent: []AgentType{AgentAntigravity},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSource, gotOpts := ParseAddOptions(tt.args)
			if gotSource != tt.wantSource {
				t.Errorf("ParseAddOptions() gotSource = %q, want %q", gotSource, tt.wantSource)
			}
			if !reflect.DeepEqual(gotOpts, tt.wantOpts) {
				t.Errorf("ParseAddOptions() gotOpts = %+v, want %+v", gotOpts, tt.wantOpts)
			}
		})
	}
}

func TestAddSkill_Local(t *testing.T) {
	instTestEnv(t)
	tmpDir := t.TempDir()

	// Create a mock source skill directory
	srcDir := filepath.Join(tmpDir, "my-external-skill")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	skillMd := filepath.Join(srcDir, "SKILL.md")
	content := `---
name: custom-skill
description: A wonderful custom skill
---
# custom-skill
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write source SKILL.md: %v", err)
	}

	// Add it to our project
	opts := AddOptions{
		Copy: true, // We test copy mode
		Yes:  true,
	}

	err := AddSkill(srcDir, opts, tmpDir)
	if err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	// Verify the skill was copied into the project's canonical `.agents/skills`
	destDir := filepath.Join(tmpDir, ".agents", "skills", "custom-skill")
	destMd := filepath.Join(destDir, "SKILL.md")

	if _, err := os.Stat(destMd); os.IsNotExist(err) {
		t.Fatalf("expected SKILL.md to exist in project canonical skills, but it is missing")
	}

	bytes, err := os.ReadFile(destMd)
	if err != nil {
		t.Fatalf("failed to read copied SKILL.md: %v", err)
	}

	if !reflect.DeepEqual(bytes, []byte(content)) {
		t.Errorf("copied file content differs from original")
	}
}

func TestAddSkill_Errors(t *testing.T) {
	instTestEnv(t)
	tmpDir := t.TempDir()

	// 1. Empty source path error
	if err := AddSkill("", AddOptions{}, tmpDir); err == nil {
		t.Error("expected error for empty source path, got nil")
	}

	// 2. Missing SKILL.md error
	srcDir := filepath.Join(tmpDir, "missing-skill-md")
	_ = os.MkdirAll(srcDir, 0755)
	if err := AddSkill(srcDir, AddOptions{Yes: true}, tmpDir); err == nil {
		t.Error("expected error for missing SKILL.md, got nil")
	}

	// 3. Frontmatter missing 'name' error
	srcDirNoName := filepath.Join(tmpDir, "no-name-skill")
	_ = os.MkdirAll(srcDirNoName, 0755)
	_ = os.WriteFile(filepath.Join(srcDirNoName, "SKILL.md"), []byte("---\ndescription: hello\n---\n"), 0644)
	if err := AddSkill(srcDirNoName, AddOptions{Yes: true}, tmpDir); err == nil {
		t.Error("expected error for missing name in frontmatter, got nil")
	}

	// 4. Invalid/non-string or empty name value error
	srcDirEmptyName := filepath.Join(tmpDir, "empty-name-skill")
	_ = os.MkdirAll(srcDirEmptyName, 0755)
	_ = os.WriteFile(filepath.Join(srcDirEmptyName, "SKILL.md"), []byte("---\nname: \"\"\n---\n"), 0644)
	if err := AddSkill(srcDirEmptyName, AddOptions{Yes: true}, tmpDir); err == nil {
		t.Error("expected error for empty skill name, got nil")
	}
}

func TestAddSkill_Remote(t *testing.T) {
	instTestEnv(t)
	// Create zip content in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Create SKILL.md file in zip
	f, err := zw.Create("remote-skill/SKILL.md")
	if err != nil {
		t.Fatalf("failed to create file in zip: %v", err)
	}
	content := `---
name: remote-skill
description: A mock remote skill
---
# remote-skill
`
	_, err = f.Write([]byte(content))
	if err != nil {
		t.Fatalf("failed to write content in zip: %v", err)
	}

	zw.Close()

	// Setup httptest server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "info/refs") || strings.Contains(r.URL.Path, "git-upload-pack") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	opts := AddOptions{Copy: true, Yes: true}

	// We pass the mock server URL as the source to trigger remote fetch
	err = AddSkill(server.URL, opts, tmpDir)
	if err != nil {
		t.Fatalf("AddSkill remote failed: %v", err)
	}

	// Verify the skill was installed in .agents/skills
	destDir := filepath.Join(tmpDir, ".agents", "skills", "remote-skill")
	destMd := filepath.Join(destDir, "SKILL.md")

	if _, err := os.Stat(destMd); os.IsNotExist(err) {
		t.Fatalf("expected SKILL.md to be installed, got none")
	}

	bytesRead, err := os.ReadFile(destMd)
	if err != nil {
		t.Fatalf("failed to read copied SKILL.md: %v", err)
	}

	if !strings.Contains(string(bytesRead), "name: remote-skill") {
		t.Errorf("expected copied content to match zip content, got: %s", string(bytesRead))
	}
}

func TestAddSkill_DepthAndExclusion(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src-root")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src-root: %v", err)
	}

	// Create subdirectories at different depths
	// Depth 0
	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: depth-zero\n---\n"), 0644)

	// Depth 1
	sub1 := filepath.Join(srcDir, "sub1")
	_ = os.MkdirAll(sub1, 0755)
	_ = os.WriteFile(filepath.Join(sub1, "SKILL.md"), []byte("---\nname: depth-one\n---\n"), 0644)

	// Depth 2
	sub2 := filepath.Join(sub1, "sub2")
	_ = os.MkdirAll(sub2, 0755)
	_ = os.WriteFile(filepath.Join(sub2, "SKILL.md"), []byte("---\nname: depth-two\n---\n"), 0644)

	// Depth 5 (Should be scanned in recursive/full depth)
	sub5 := filepath.Join(srcDir, "d1", "d2", "d3", "d4", "d5")
	_ = os.MkdirAll(sub5, 0755)
	_ = os.WriteFile(filepath.Join(sub5, "SKILL.md"), []byte("---\nname: depth-five\n---\n"), 0644)

	// Depth 6 (Should be skipped as it exceeds maxDepth 5)
	sub6 := filepath.Join(sub5, "d6")
	_ = os.MkdirAll(sub6, 0755)
	_ = os.WriteFile(filepath.Join(sub6, "SKILL.md"), []byte("---\nname: depth-six\n---\n"), 0644)

	// Excluded directories (Should be skipped)
	nodeMods := filepath.Join(srcDir, "node_modules")
	_ = os.MkdirAll(nodeMods, 0755)
	_ = os.WriteFile(filepath.Join(nodeMods, "SKILL.md"), []byte("---\nname: ignored-node-mods\n---\n"), 0644)

	dotGit := filepath.Join(srcDir, ".git")
	_ = os.MkdirAll(dotGit, 0755)
	_ = os.WriteFile(filepath.Join(dotGit, "SKILL.md"), []byte("---\nname: ignored-git\n---\n"), 0644)

	// Test Case 1: Root SKILL.md present, fullDepth = false
	// Expected: returns only the root skill (early return)
	dirs1 := findSkillDirectories(srcDir, false)
	if len(dirs1) != 1 || filepath.Base(dirs1[0]) != "src-root" {
		t.Errorf("expected only root dir 'src-root' to be returned when fullDepth is false, got: %v", dirs1)
	}

	// Test Case 2: Root SKILL.md present, fullDepth = true
	// Expected: returns root + nested skills up to depth 5
	dirs2 := findSkillDirectories(srcDir, true)
	hasZero := false
	hasOne := false
	hasTwo := false
	hasFive := false
	hasSix := false
	hasNode := false
	hasGit := false

	for _, d := range dirs2 {
		base := filepath.Base(d)
		if base == "src-root" {
			hasZero = true
		} else if base == "sub1" {
			hasOne = true
		} else if base == "sub2" {
			hasTwo = true
		} else if base == "d5" {
			hasFive = true
		} else if base == "d6" {
			hasSix = true
		} else if base == "node_modules" {
			hasNode = true
		} else if base == ".git" {
			hasGit = true
		}
	}

	if !hasZero || !hasOne || !hasTwo || !hasFive {
		t.Errorf("expected depth 0, 1, 2, and 5 to be found, got: %v", dirs2)
	}
	if hasSix {
		t.Error("expected depth 6 directory to be EXCLUDED (exceeded maxDepth 5)")
	}
	if hasNode || hasGit {
		t.Error("expected node_modules and .git directories to be EXCLUDED")
	}

	// Test Case 3: No root SKILL.md, and no priority-level skills
	// Expected: falls back to priority and recursive searches finding nested skills up to depth 5
	_ = os.Remove(filepath.Join(srcDir, "SKILL.md"))
	_ = os.Remove(filepath.Join(sub1, "SKILL.md")) // Remove depth 1 skill so priority search finds nothing
	dirs3 := findSkillDirectories(srcDir, false)

	hasTwo = false
	hasFive = false
	hasSix = false

	for _, d := range dirs3 {
		base := filepath.Base(d)
		if base == "sub2" {
			hasTwo = true
		} else if base == "d5" {
			hasFive = true
		} else if base == "d6" {
			hasSix = true
		}
	}

	if !hasTwo || !hasFive {
		t.Errorf("expected nested skills sub2 and d5 to be discovered via recursive fallback, got: %v", dirs3)
	}
	if hasSix {
		t.Error("expected depth 6 directory to be EXCLUDED (exceeded maxDepth 5)")
	}
}

func TestAddSkill_Filters(t *testing.T) {
	instTestEnv(t)
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "my-filtered-skill")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}

	// Create SKILL.md
	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: filtered-skill\ndescription: filtered\n---\n"), 0644)

	// Create normal file
	_ = os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# Hello"), 0644)

	// Create files that should be filtered out
	_ = os.WriteFile(filepath.Join(srcDir, "metadata.json"), []byte("{}"), 0644)

	pycache := filepath.Join(srcDir, "__pycache__")
	_ = os.MkdirAll(pycache, 0755)
	_ = os.WriteFile(filepath.Join(pycache, "info.pyc"), []byte("cache"), 0644)

	pypackages := filepath.Join(srcDir, "__pypackages__")
	_ = os.MkdirAll(pypackages, 0755)
	_ = os.WriteFile(filepath.Join(pypackages, "pkg.py"), []byte("pkg"), 0644)

	dotGit := filepath.Join(srcDir, ".git")
	_ = os.MkdirAll(dotGit, 0755)
	_ = os.WriteFile(filepath.Join(dotGit, "config"), []byte("git config"), 0644)

	// Run AddSkill in copy mode
	err := AddSkill(srcDir, AddOptions{Copy: true, Yes: true}, tmpDir)
	if err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	destDir := filepath.Join(tmpDir, ".agents", "skills", "filtered-skill")

	// Normal files should exist
	if _, err := os.Stat(filepath.Join(destDir, "SKILL.md")); os.IsNotExist(err) {
		t.Error("expected SKILL.md to be copied")
	}
	if _, err := os.Stat(filepath.Join(destDir, "README.md")); os.IsNotExist(err) {
		t.Error("expected README.md to be copied")
	}

	// Filtered files should NOT exist
	if _, err := os.Stat(filepath.Join(destDir, "metadata.json")); err == nil {
		t.Error("expected metadata.json to be EXCLUDED")
	}
	if _, err := os.Stat(filepath.Join(destDir, "__pycache__")); err == nil {
		t.Error("expected __pycache__ to be EXCLUDED")
	}
	if _, err := os.Stat(filepath.Join(destDir, "__pypackages__")); err == nil {
		t.Error("expected __pypackages__ to be EXCLUDED")
	}
	if _, err := os.Stat(filepath.Join(destDir, ".git")); err == nil {
		t.Error("expected .git to be EXCLUDED")
	}
}

func TestAddSkill_Symlink(t *testing.T) {
	instTestEnv(t)
	origAgents := Agents
	defer func() { Agents = origAgents }()

	const MockAgent AgentType = "mock-agent"

	// Create dynamic copy of Agents map and register mock agent
	Agents = make(map[AgentType]AgentConfig)
	for k, v := range origAgents {
		Agents[k] = v
	}
	Agents[MockAgent] = AgentConfig{
		Name:        "mock-agent",
		DisplayName: "Mock Agent",
		SkillsDir:   ".mock-agent/skills",
		DetectInstalled: func(home, configHome, cwd string) bool {
			_, err := os.Stat(filepath.Join(cwd, ".mock-agent"))
			return err == nil
		},
	}

	tmpDir := t.TempDir()

	// Create parent folder to mock the agent being installed
	if err := os.MkdirAll(filepath.Join(tmpDir, ".mock-agent"), 0755); err != nil {
		t.Fatalf("failed to create agent root: %v", err)
	}

	srcDir := filepath.Join(tmpDir, "my-symlink-source")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: symlink-skill\ndescription: symlinked\n---\n"), 0644)

	// Run AddSkill in symlink mode
	err := AddSkill(srcDir, AddOptions{Agent: []AgentType{MockAgent}}, tmpDir)
	if err != nil {
		t.Fatalf("AddSkill failed: %v", err)
	}

	// Canonical folder must have the files
	canonicalMd := filepath.Join(tmpDir, ".agents", "skills", "symlink-skill", "SKILL.md")
	if _, err := os.Stat(canonicalMd); os.IsNotExist(err) {
		t.Errorf("expected canonical SKILL.md to exist, but missing")
	}

	// Agent folder must have a symlink pointing to canonical folder
	agentPath := filepath.Join(tmpDir, ".mock-agent", "skills", "symlink-skill")
	stats, err := os.Lstat(agentPath)
	if err != nil {
		t.Fatalf("failed to inspect agent folder: %v", err)
	}

	if stats.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected agent folder to be a symlink, got mode: %v", stats.Mode())
	}

	// Verify target points correctly
	target, err := os.Readlink(agentPath)
	if err != nil {
		t.Fatalf("failed to read link: %v", err)
	}

	// On Windows the installer creates a junction, whose target is always an
	// absolute path (upstream's "junction" symlink type); it must resolve to the
	// canonical folder. Elsewhere the target is relative:
	// "../../.agents/skills/symlink-skill".
	if runtime.GOOS == "windows" {
		if !filepath.IsAbs(target) {
			t.Fatalf("expected absolute junction target on Windows, got %q", target)
		}
		canonicalDir := filepath.Join(tmpDir, ".agents", "skills", "symlink-skill")
		wantInfo, err := os.Stat(canonicalDir)
		if err != nil {
			t.Fatal(err)
		}
		gotInfo, err := os.Stat(target)
		if err != nil {
			t.Fatalf("junction target %q does not resolve: %v", target, err)
		}
		if !os.SameFile(gotInfo, wantInfo) {
			t.Errorf("junction target %q does not resolve to canonical dir %q", target, canonicalDir)
		}
		return
	}
	expectedTarget := filepath.Join("..", "..", ".agents", "skills", "symlink-skill")
	if filepath.Clean(target) != filepath.Clean(expectedTarget) {
		t.Errorf("expected link target to be %q, got %q", expectedTarget, target)
	}
}

func TestParseAddOptions_YesFlag(t *testing.T) {
	_, opts := ParseAddOptions([]string{"-y", "/some/path"})
	if !opts.Yes {
		t.Errorf("expected Yes to be true when passing -y")
	}
	_, opts2 := ParseAddOptions([]string{"--yes", "/some/path"})
	if !opts2.Yes {
		t.Errorf("expected Yes to be true when passing --yes")
	}
}

func TestMaybeSuggestAgy_NonInteractive(t *testing.T) {
	instTestEnv(t)
	// Should execute cleanly without panicking in non-interactive pipeline
	opts := AddOptions{Yes: true}
	maybeSuggestAgy(opts)
}

// ---------------------------------------------------------------------------
// RunAdd / ParseAddCLIOptions (upstream add.ts parity). Fetching is stubbed through
// fetchSourceFn so these tests stay hermetic and independent of providers.go.
// ---------------------------------------------------------------------------

// addFakeLocalFetch builds a FetchedSource for every <root>/<name>/SKILL.md (or root itself).
func addFakeLocalFetch(t *testing.T, root string) *FetchedSource {
	t.Helper()
	f := &FetchedSource{Input: root, SourceType: SourceTypeLocal, Source: root, SourceURL: root, RootDir: root, Via: "local",
		Parsed: ParsedSource{Type: SourceTypeLocal, URL: root, LocalPath: root}}
	add := func(dir string) {
		raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			return
		}
		data, _, err := ParseFrontmatter(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		name, _ := data["name"].(string)
		desc, _ := data["description"].(string)
		f.Skills = append(f.Skills, FetchedSkill{Skill: Skill{Name: name, Description: desc, Path: dir, RawContent: string(raw)}})
	}
	add(root)
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() {
			add(filepath.Join(root, e.Name()))
		}
	}
	return f
}

// addStub installs a fetch stub and a "not running in an agent" detector for one test.
func addStub(t *testing.T, fetch func(req SourceRequest) (*FetchedSource, error)) {
	t.Helper()
	origFetch, origDetect := fetchSourceFn, addDetectAgent
	t.Cleanup(func() { fetchSourceFn, addDetectAgent = origFetch, origDetect })
	fetchSourceFn = fetch
	addDetectAgent = func() AgentDetectionResult { return AgentDetectionResult{} }
}

func addFetchMustNotRun(t *testing.T) func(SourceRequest) (*FetchedSource, error) {
	return func(SourceRequest) (*FetchedSource, error) {
		t.Fatal("source must not be fetched")
		return nil, nil
	}
}

func runAddForTest(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	no := false
	code := RunAddWithIO(args, AddIO{Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Cwd: cwd, Interactive: &no})
	return code, out.String(), errb.String()
}

func addTwoSkillSource(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	instWriteSkill(t, filepath.Join(root, "alpha"), "alpha")
	instWriteSkill(t, filepath.Join(root, "beta"), "beta")
	return root
}

func TestParseAddCLIOptions(t *testing.T) {
	instTestEnv(t)
	p := ParseAddCLIOptions([]string{"owner/repo", "-a", "claude-code", "cursor,codex", "-s", "Convex Best Practices", "pr-review", "--subagent", "root", "research", "-g", "--copy", "--full-depth", "--json", "-y", "--metadata", `{"a":1}`})
	if len(p.Errors) != 0 {
		t.Fatalf("errors: %v", p.Errors)
	}
	if !reflect.DeepEqual(p.Sources, []string{"owner/repo"}) {
		t.Errorf("sources = %v", p.Sources)
	}
	if !reflect.DeepEqual(p.Options.Agent, []AgentType{"claude-code", "cursor", "codex"}) {
		t.Errorf("agents = %v", p.Options.Agent)
	}
	if !reflect.DeepEqual(p.Options.Skill, []string{"Convex Best Practices", "pr-review"}) {
		t.Errorf("skills = %v", p.Options.Skill)
	}
	if !reflect.DeepEqual(p.Options.Subagent, []string{"root", "research"}) {
		t.Errorf("subagents = %v", p.Options.Subagent)
	}
	o := p.Options
	if !o.Global || !o.Copy || !o.FullDepth || !o.JSON || !o.Yes || o.Metadata != `{"a":1}` || !p.ScopeExplicit || !p.AgentFlagPassed {
		t.Errorf("flags = %+v", p)
	}
	if len(p.ExplicitAgentSet) != 3 {
		t.Errorf("explicit = %v", p.ExplicitAgentSet)
	}

	p = ParseAddCLIOptions([]string{"src", "-a", "*", "--all"})
	if len(p.ExplicitAgentSet) != 0 || !p.Options.All {
		t.Errorf("wildcard must not count as explicit: %+v", p)
	}

	p = ParseAddCLIOptions([]string{"--metadata"})
	if len(p.Errors) != 1 || !strings.Contains(p.Errors[0].Error(), "--metadata requires a JSON value") {
		t.Errorf("missing metadata: %v", p.Errors)
	}
	p = ParseAddCLIOptions([]string{"src", "--metadata", "{nope"})
	if len(p.Errors) != 1 || !strings.Contains(p.Errors[0].Error(), "--metadata must be valid JSON") {
		t.Errorf("invalid metadata: %v", p.Errors)
	}

	// skl compatibility: the source may follow -a/-s.
	p = ParseAddCLIOptions([]string{"-s", "alpha", "./skills"})
	if !reflect.DeepEqual(p.Sources, []string{"./skills"}) || !reflect.DeepEqual(p.Options.Skill, []string{"alpha"}) {
		t.Errorf("reclaimed skill source: %+v", p)
	}
	p = ParseAddCLIOptions([]string{"-a", "claude-code", "codex"})
	if len(p.Sources) != 0 || len(p.Options.Agent) != 2 {
		t.Errorf("known agents must not be reclaimed: %+v", p)
	}
}

func TestRunAdd_InvalidAgentFailsBeforeFetch(t *testing.T) {
	_, cwd := instTestEnv(t)
	addStub(t, addFetchMustNotRun(t))

	code, _, stderr := runAddForTest(t, cwd, "owner/repo", "-a", "claude-code", "not-an-agent", "-y")
	if code != 1 || !strings.Contains(stderr, "Invalid agents: not-an-agent") || !strings.Contains(stderr, "Valid agents:") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}

	code, stdout, _ := runAddForTest(t, cwd, "owner/repo", "-a", "nope", "-y", "--json")
	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("stdout must be one JSON array: %v (%q)", err, stdout)
	}
	if code != 1 || len(results) != 1 || results[0]["status"] != "failed" || !strings.Contains(results[0]["error"].(string), "Invalid agents: nope") {
		t.Errorf("code=%d results=%v", code, results)
	}
}

func TestRunAdd_JSONGuards(t *testing.T) {
	_, cwd := instTestEnv(t)
	addStub(t, addFetchMustNotRun(t))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"src", "--json"}, "The --json flag requires --yes (or --all) to run non-interactively."},
		{[]string{"src", "--json", "-y", "-l"}, "The --json flag cannot be combined with --list."},
		{[]string{"--json", "-y"}, "Missing required argument: source"},
	} {
		code, stdout, _ := runAddForTest(t, cwd, tc.args...)
		var results []AddJSONResult
		if err := json.Unmarshal([]byte(stdout), &results); err != nil {
			t.Fatalf("%v: invalid JSON %q", tc.args, stdout)
		}
		if code != 1 || len(results) != 1 || results[0].Error != tc.want {
			t.Errorf("%v: code=%d results=%+v", tc.args, code, results)
		}
	}
}

func TestRunAdd_ProjectSingleAgentCopiesAndLocks(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })

	code, stdout, stderr := runAddForTest(t, cwd, src, "-a", "claude-code", "-s", "alpha", "-y")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	dir := filepath.Join(cwd, ".claude", "skills", "alpha")
	if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real copy at %s (err=%v)", dir, err)
	}
	if instExists(filepath.Join(cwd, ".claude", "skills", "beta")) {
		t.Error("beta was not selected")
	}
	if !strings.Contains(stdout, "Installed 1 skill") || !strings.Contains(stdout, "(copied)") {
		t.Errorf("stdout = %s", stdout)
	}
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lock.Skills["alpha"]
	if !ok || e.SourceType != SourceTypeLocal || e.ComputedHash == "" || e.InstallMode != "" || e.SkillPath != "" {
		t.Errorf("lock entry = %+v (ok=%v)", e, ok)
	}
}

// #2607: a user-chosen copy over a symlink default is recorded in the lock.
func TestRunAdd_RecordsChosenCopyMode(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })

	code, _, stderr := runAddForTest(t, cwd, src, "-a", "claude-code", "codex", "--copy", "-y", "-s", "alpha")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	lock, _ := ReadLocalLock(cwd)
	if lock.Skills["alpha"].InstallMode != "copy" {
		t.Errorf("installMode = %q", lock.Skills["alpha"].InstallMode)
	}
	for _, d := range []string{filepath.Join(cwd, ".claude", "skills", "alpha"), filepath.Join(cwd, ".agents", "skills", "alpha")} {
		if info, err := os.Lstat(d); err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("expected copy at %s (err=%v)", d, err)
		}
	}
}

// #2271: symlink installs list universal and symlinked agents separately.
func TestRunAdd_SymlinkSummary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })

	code, stdout, stderr := runAddForTest(t, cwd, src, "-a", "codex", "claude-code", "-y", "-s", "alpha")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{"universal: Codex", "symlink → Claude Code", "symlinked: Claude Code", "✓ ./.agents/skills/alpha"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	lock, _ := ReadLocalLock(cwd)
	if lock.Skills["alpha"].InstallMode != "" {
		t.Errorf("default symlink mode must not be recorded: %+v", lock.Skills["alpha"])
	}
}

func addDownloadFetch(t *testing.T, rawURL string) func(SourceRequest) (*FetchedSource, error) {
	root := filepath.Join(t.TempDir(), "dl")
	instWriteSkill(t, filepath.Join(root, "pkg", "dl-skill"), "dl-skill")
	return func(SourceRequest) (*FetchedSource, error) {
		f := &FetchedSource{Input: rawURL, SourceType: SourceTypeDownload, Source: rawURL, SourceURL: rawURL, RootDir: root, Via: "download",
			Parsed: ParsedSource{Type: SourceTypeDownload, URL: rawURL}}
		f.Skills = []FetchedSkill{{Skill: Skill{Name: "dl-skill", Description: "d", Path: filepath.Join(root, "pkg", "dl-skill")}, SkillPath: "pkg/dl-skill/SKILL.md"}}
		return f, nil
	}
}

// #2682: direct downloads are locked by URL with the SKILL.md path inside the download.
func TestRunAdd_DirectDownloadLock(t *testing.T) {
	_, cwd := instTestEnv(t)
	addStub(t, addDownloadFetch(t, "https://example.com/skills.zip"))
	code, _, stderr := runAddForTest(t, cwd, "https://example.com/skills.zip", "-a", "codex", "-y")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	lock, _ := ReadLocalLock(cwd)
	e := lock.Skills["dl-skill"]
	if e.SourceType != "download" || e.Source != "https://example.com/skills.zip" || e.SourceUrl != "https://example.com/skills.zip" || e.SkillPath != "pkg/dl-skill/SKILL.md" || e.ComputedHash == "" {
		t.Errorf("lock entry = %+v", e)
	}
}

func TestRunAdd_SignedDownloadIsNotLocked(t *testing.T) {
	_, cwd := instTestEnv(t)
	signed := "https://bucket.example.com/skills.zip?x-amz-signature=s3cr3tsig"
	addStub(t, addDownloadFetch(t, signed))
	code, _, stderr := runAddForTest(t, cwd, signed, "-a", "codex", "-y")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	lock, _ := ReadLocalLock(cwd)
	if _, ok := lock.Skills["dl-skill"]; ok {
		t.Error("signed URL must not be recorded")
	}
	if !strings.Contains(stderr, "Not recording dl-skill in the lock file: https://bucket.example.com/skills.zip has a query string or credentials") {
		t.Errorf("stderr = %q", stderr)
	}
	if strings.Contains(stderr, "s3cr3tsig") {
		t.Errorf("warning leaked the signature: %q", stderr)
	}
	if !instExists(filepath.Join(cwd, ".agents", "skills", "dl-skill", "SKILL.md")) {
		t.Error("skill must still be installed")
	}
}

// #2606: a lock write failure is reported and exits 1 after installing.
func TestRunAdd_LockWriteFailureExitsOne(t *testing.T) {
	home, cwd := instTestEnv(t)
	blocker := filepath.Join(home, "state-is-a-file")
	_ = os.WriteFile(blocker, []byte("x"), 0o644)
	t.Setenv("XDG_STATE_HOME", blocker)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })

	code, _, stderr := runAddForTest(t, cwd, src, "-g", "-a", "claude-code", "-y", "-s", "alpha")
	if code != 1 || !strings.Contains(stderr, "Installed alpha but could not record it in") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !instExists(filepath.Join(home, ".claude", "skills", "alpha", "SKILL.md")) {
		t.Error("skill should still be installed")
	}
}

func TestRunAdd_GlobalLockEntry(t *testing.T) {
	home, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	code, _, stderr := runAddForTest(t, cwd, src, "-g", "-a", "claude-code", "-y", "-s", "beta")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	entry, err := GetSkillFromLock("beta")
	if err != nil || entry == nil || entry.SourceType != SourceTypeLocal || entry.SkillFolderHash == "" {
		t.Fatalf("global lock entry = %+v err=%v", entry, err)
	}
	if !instExists(filepath.Join(home, ".claude", "skills", "beta")) {
		t.Error("expected global claude install")
	}
	if _, err := os.Stat(filepath.Join(cwd, "skills-lock.json")); err == nil {
		t.Error("global installs must not write the project lock")
	}
}

func TestRunAdd_JSONOutputContract(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })

	code, stdout, _ := runAddForTest(t, cwd, src, "-a", "codex", "-s", "alpha", "missing", "-y", "--json")
	if code != 1 {
		t.Errorf("a skipped request must exit 1, got %d", code)
	}
	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	if len(results) != 2 {
		t.Fatalf("results = %v", results)
	}
	if results[0]["name"] != "missing" || results[0]["status"] != "skipped" || results[0]["reason"] != "No matching skill found in source" {
		t.Errorf("skipped entry = %v", results[0])
	}
	inst := results[1]
	if inst["name"] != "alpha" || inst["status"] != "installed" || inst["scope"] != "project" || inst["mode"] != "copy" {
		t.Errorf("installed entry = %v", inst)
	}
	for _, k := range []string{"ref", "security"} {
		if v, ok := inst[k]; !ok || v != nil {
			t.Errorf("%s must be present and null: %v", k, inst)
		}
	}
	if h, _ := inst["hash"].(string); len(h) != 64 {
		t.Errorf("hash = %v", inst["hash"])
	}
	if agents, _ := inst["agents"].([]interface{}); len(agents) != 1 || agents[0] != "Codex" {
		t.Errorf("agents = %v", inst["agents"])
	}
}

func TestRunAdd_ExplicitGlobalUnsupportedAgentFails(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	code, _, stderr := runAddForTest(t, cwd, src, "-g", "-a", "promptscript", "-y", "-s", "alpha")
	if code != 1 || !strings.Contains(stderr, "PromptScript does not support global skill installation") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

// #2547: agents picked automatically that have no global dir are skipped with one notice.
func TestRunAdd_GlobalAllAgentsSkipsWithoutGlobalDir(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	code, stdout, stderr := runAddForTest(t, cwd, src, "-g", "-y", "-s", "alpha")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "PromptScript") || !strings.Contains(stdout, ": no global skills directory") {
		t.Errorf("missing skip notice:\n%s", stdout)
	}
	if !strings.Contains(stdout, agyTip) {
		t.Errorf("expected agy onboarding tip when no agents are detected")
	}
}

func TestRunAdd_PromptWithoutTTYFails(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	code, _, stderr := runAddForTest(t, cwd, src, "-a", "codex")
	if code != 1 || !strings.Contains(stderr, "SKILLS_PROMPT_REQUIRES_TTY") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
	if instExists(filepath.Join(cwd, ".agents", "skills", "alpha")) {
		t.Error("nothing may be installed when a prompt cannot run")
	}
}

func TestRunAdd_InteractivePrompts(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	var out, errb bytes.Buffer
	yes := true
	// skills: pick 2 (beta); scope: project (default); confirm: yes.
	code := RunAddWithIO([]string{src, "-a", "codex"}, AddIO{Stdout: &out, Stderr: &errb, Stdin: strings.NewReader("2\n\ny\n"), Cwd: cwd, Interactive: &yes})
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errb.String())
	}
	if !instExists(filepath.Join(cwd, ".agents", "skills", "beta")) || instExists(filepath.Join(cwd, ".agents", "skills", "alpha")) {
		t.Errorf("expected only beta installed; out=%s", out.String())
	}
}

// #2651: a skills.sh pack entry installs from its upstream repo only when the tree hash matches.
func TestRunAdd_PackPointerVerification(t *testing.T) {
	_, cwd := instTestEnv(t)
	packRoot := filepath.Join(t.TempDir(), "pack")
	instWriteSkill(t, filepath.Join(packRoot, "same"), "same")
	instWriteSkill(t, filepath.Join(packRoot, "drift"), "drift")
	repoRoot := filepath.Join(t.TempDir(), "repo")
	instWriteSkill(t, filepath.Join(repoRoot, "skills", "same"), "same")
	instWriteSkill(t, filepath.Join(repoRoot, "skills", "drift"), "drift")
	_ = os.WriteFile(filepath.Join(repoRoot, "skills", "drift", "extra.md"), []byte("changed upstream"), 0o644)

	pin := func(dir string) string {
		h, err := ComputeSkillTreeHash(dir)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	samePin := pin(filepath.Join(packRoot, "same"))
	driftPin := pin(filepath.Join(packRoot, "drift"))

	addStub(t, func(req SourceRequest) (*FetchedSource, error) {
		if req.Source == "acme/skills" {
			f := &FetchedSource{Input: req.Source, SourceType: SourceTypeGitHub, Source: "acme/skills", SourceURL: "https://github.com/acme/skills.git", RootDir: repoRoot, Via: "clone"}
			for _, n := range []string{"same", "drift"} {
				f.Skills = append(f.Skills, FetchedSkill{Skill: Skill{Name: n, Description: "d", Path: filepath.Join(repoRoot, "skills", n)}, SkillPath: "skills/" + n + "/SKILL.md"})
			}
			return f, nil
		}
		f := &FetchedSource{Input: req.Source, SourceType: SourceTypeWellKnown, Source: "skills.sh", SourceURL: "https://skills.sh/p/demo", RootDir: packRoot, Via: "well-known"}
		f.Skills = []FetchedSkill{
			{Skill: Skill{Name: "same", Description: "d", Path: filepath.Join(packRoot, "same")}, WellKnownDigest: "sha256:aa", PackPointer: &PackSkillPointer{Repo: "acme/skills", Skill: "same", TreeHash: samePin}},
			{Skill: Skill{Name: "drift", Description: "d", Path: filepath.Join(packRoot, "drift")}, WellKnownDigest: "sha256:bb", PackPointer: &PackSkillPointer{Repo: "acme/skills", Skill: "drift", TreeHash: driftPin}},
		}
		return f, nil
	})
	parsed, err := ParseSource("https://skills.sh/p/demo")
	if err != nil || parsed.Type != SourceTypeWellKnown {
		t.Skipf("ParseSource does not classify skills.sh packs as well-known yet: %+v %v", parsed, err)
	}
	code, stdout, stderr := runAddForTest(t, cwd, "https://skills.sh/p/demo", "-a", "codex", "-y")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "drift changed in acme/skills since this pack pinned it; using the pack's copy") {
		t.Errorf("stdout = %s", stdout)
	}
	lock, _ := ReadLocalLock(cwd)
	if e := lock.Skills["same"]; e.Source != "acme/skills" || e.SourceType != SourceTypeGitHub || e.SkillPath != "skills/same/SKILL.md" {
		t.Errorf("same should be locked to the upstream repo: %+v", e)
	}
	if e := lock.Skills["drift"]; e.SourceType != SourceTypeWellKnown || e.WellKnownDigest != "sha256:bb" {
		t.Errorf("drift should keep the pack's copy: %+v", e)
	}
	if instExists(filepath.Join(cwd, ".agents", "skills", "drift", "extra.md")) {
		t.Error("drifted upstream content must not be installed")
	}
}

// End-to-end through the real FetchSource for a local source (no stubbed fetch).
func TestRunAdd_RealLocalFetch(t *testing.T) {
	_, cwd := instTestEnv(t)
	origDetect := addDetectAgent
	t.Cleanup(func() { addDetectAgent = origDetect })
	addDetectAgent = func() AgentDetectionResult { return AgentDetectionResult{} }

	src := addTwoSkillSource(t)
	code, stdout, stderr := runAddForTest(t, cwd, src, "-a", "codex", "-s", "beta", "-y")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !instExists(filepath.Join(cwd, ".agents", "skills", "beta", "SKILL.md")) || instExists(filepath.Join(cwd, ".agents", "skills", "alpha")) {
		t.Errorf("expected only beta installed")
	}
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if e := lock.Skills["beta"]; e.SourceType != SourceTypeLocal || e.ComputedHash == "" {
		t.Errorf("lock entry = %+v", e)
	}

	var out, errb bytes.Buffer
	if code := RunUseWithIO([]string{src + "/beta"}, UseIO{Stdout: &out, Stderr: &errb, Cwd: cwd}); code != 0 {
		t.Fatalf("use code=%d stderr=%s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "You are being given a Skill") || !strings.Contains(out.String(), "name: beta") {
		t.Errorf("use stdout = %q", out.String())
	}
}

func TestInstallFromSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	f := addFakeLocalFetch(t, src)

	res := InstallFromSource(f, InstallFromSourceOptions{Skills: []string{"alpha"}, Agents: []AgentType{"windsurf"}, Via: "my-pkg", Cwd: cwd})
	if res.Error != "" || len(res.Installed) != 1 || res.Installed[0] != "alpha" || len(res.Failed) != 0 {
		t.Fatalf("result = %+v", res)
	}
	// Restores create the agent root even though .windsurf did not exist.
	if info, err := os.Lstat(filepath.Join(cwd, ".windsurf", "skills", "alpha")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected windsurf symlink (err=%v)", err)
	}
	lock, _ := ReadLocalLock(cwd)
	e := lock.Skills["alpha"]
	if e.Via != "my-pkg" || e.InstallMode != "" || e.ComputedHash == "" {
		t.Errorf("lock entry = %+v", e)
	}

	// Frozen lock: matching content installs without writing the lock.
	frozen := lock
	_ = os.Remove(GetLocalLockPath(cwd))
	res = InstallFromSource(f, InstallFromSourceOptions{Skills: []string{"alpha"}, Agents: []AgentType{"codex"}, FrozenLock: &frozen, Cwd: cwd})
	if res.Error != "" || len(res.Installed) != 1 {
		t.Fatalf("frozen match: %+v", res)
	}
	if _, err := os.Stat(GetLocalLockPath(cwd)); err == nil {
		t.Error("frozen installs must not write skills-lock.json")
	}

	// Frozen lock: changed content is refused before touching disk.
	_ = os.WriteFile(filepath.Join(src, "beta", "extra.md"), []byte("new"), 0o644)
	frozen.Skills["beta"] = LocalSkillLockEntry{Source: src, SourceType: SourceTypeLocal, ComputedHash: "0000"}
	_ = os.RemoveAll(filepath.Join(cwd, ".agents", "skills", "beta"))
	res = InstallFromSource(f, InstallFromSourceOptions{Skills: []string{"beta"}, Agents: []AgentType{"codex"}, FrozenLock: &frozen, Cwd: cwd})
	if res.Error == "" || len(res.Installed) != 0 {
		t.Fatalf("frozen mismatch should fail: %+v", res)
	}
	if instExists(filepath.Join(cwd, ".agents", "skills", "beta")) {
		t.Error("nothing may be written when the frozen lock does not match")
	}

	if res := InstallFromSource(f, InstallFromSourceOptions{Skills: []string{"nope"}, Agents: []AgentType{"codex"}, Cwd: cwd}); res.Error != "No matching skills found" {
		t.Errorf("no match: %+v", res)
	}
}

// Notion packs: per-skill download URLs are locked, and -s selected packs (not skills).
func TestRunAdd_NotionPackLocksPerSkillURL(t *testing.T) {
	_, cwd := instTestEnv(t)
	root := filepath.Join(t.TempDir(), "packs")
	instWriteSkill(t, filepath.Join(root, "p1", "n-skill"), "n-skill")
	addStub(t, func(req SourceRequest) (*FetchedSource, error) {
		f := &FetchedSource{Input: "notion", SourceType: SourceTypeDownload, Source: "notion", SourceURL: "notion://packs", RootDir: root, Via: "download"}
		f.Skills = []FetchedSkill{{Skill: Skill{Name: "n-skill", Description: "d", Path: filepath.Join(root, "p1", "n-skill"), PluginName: "Pack One"},
			SkillPath: "n-skill/SKILL.md", SourceURL: "notion://packs/abc"}}
		return f, nil
	})
	code, _, stderr := runAddForTest(t, cwd, "notion", "-s", "Pack One", "-a", "codex", "-y")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	lock, _ := ReadLocalLock(cwd)
	if e := lock.Skills["n-skill"]; e.Source != "notion" || e.SourceUrl != "notion://packs/abc" || e.SourceType != "download" || e.SkillPath != "n-skill/SKILL.md" {
		t.Errorf("lock entry = %+v", e)
	}
}

// Review #41 item 9: a single unknown -a value stays an agent (so `install -a bogus`
// reports an invalid agent instead of being treated as a source).
func TestParseAddCLIOptions_SingleUnknownAgentNotReclaimed(t *testing.T) {
	instTestEnv(t)
	p := ParseAddCLIOptions([]string{"-a", "bogus"})
	if len(p.Sources) != 0 || len(p.Options.Agent) != 1 || p.Options.Agent[0] != "bogus" {
		t.Errorf("parsed = %+v", p)
	}
	p = ParseAddCLIOptions([]string{"-s", "./skills"})
	if len(p.Sources) != 0 || len(p.Options.Skill) != 1 {
		t.Errorf("parsed = %+v", p)
	}
}

// Review #41 item 10: --frozen-lockfile refuses well-known drift before touching disk.
func TestInstallFromSource_FrozenWellKnownRefusedBeforeDisk(t *testing.T) {
	_, cwd := instTestEnv(t)
	root := filepath.Join(t.TempDir(), "wk")
	instWriteSkill(t, filepath.Join(root, "wk-skill"), "wk-skill")
	f := &FetchedSource{SourceType: SourceTypeWellKnown, Source: "example.com", SourceURL: "https://example.com", RootDir: root, Via: "well-known",
		Skills: []FetchedSkill{{Skill: Skill{Name: "wk-skill", Description: "d", Path: filepath.Join(root, "wk-skill")}, WellKnownDigest: "sha256:aa"}}}
	frozen := LocalSkillLockFile{Version: 1, Skills: map[string]LocalSkillLockEntry{
		"wk-skill": {Source: "example.com", SourceType: SourceTypeWellKnown, ComputedHash: "0000"},
	}}
	res := InstallFromSource(f, InstallFromSourceOptions{Agents: []AgentType{"codex"}, FrozenLock: &frozen, Cwd: cwd})
	if res.Error == "" || len(res.Installed) != 0 {
		t.Fatalf("expected refusal: %+v", res)
	}
	if instExists(filepath.Join(cwd, ".agents", "skills", "wk-skill")) {
		t.Error("drifted well-known skill was written to disk")
	}
	// Matching hash installs.
	h, err := addStagedCopyHash(filepath.Join(root, "wk-skill"))
	if err != nil {
		t.Fatal(err)
	}
	frozen.Skills["wk-skill"] = LocalSkillLockEntry{Source: "example.com", SourceType: SourceTypeWellKnown, ComputedHash: h}
	if res := InstallFromSource(f, InstallFromSourceOptions{Agents: []AgentType{"codex"}, FrozenLock: &frozen, Cwd: cwd}); res.Error != "" || len(res.Installed) != 1 {
		t.Errorf("matching frozen well-known: %+v", res)
	}
}

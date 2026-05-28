package skills

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	tmpDir := t.TempDir()

	// 1. Empty source path error
	if err := AddSkill("", AddOptions{}, tmpDir); err == nil {
		t.Error("expected error for empty source path, got nil")
	}

	// 2. Missing SKILL.md error
	srcDir := filepath.Join(tmpDir, "missing-skill-md")
	_ = os.MkdirAll(srcDir, 0755)
	if err := AddSkill(srcDir, AddOptions{}, tmpDir); err == nil {
		t.Error("expected error for missing SKILL.md, got nil")
	}

	// 3. Frontmatter missing 'name' error
	srcDirNoName := filepath.Join(tmpDir, "no-name-skill")
	_ = os.MkdirAll(srcDirNoName, 0755)
	_ = os.WriteFile(filepath.Join(srcDirNoName, "SKILL.md"), []byte("---\ndescription: hello\n---\n"), 0644)
	if err := AddSkill(srcDirNoName, AddOptions{}, tmpDir); err == nil {
		t.Error("expected error for missing name in frontmatter, got nil")
	}

	// 4. Invalid/non-string or empty name value error
	srcDirEmptyName := filepath.Join(tmpDir, "empty-name-skill")
	_ = os.MkdirAll(srcDirEmptyName, 0755)
	_ = os.WriteFile(filepath.Join(srcDirEmptyName, "SKILL.md"), []byte("---\nname: \"\"\n---\n"), 0644)
	if err := AddSkill(srcDirEmptyName, AddOptions{}, tmpDir); err == nil {
		t.Error("expected error for empty skill name, got nil")
	}
}

func TestAddSkill_Remote(t *testing.T) {
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

	// Depth 3 (Should be skipped)
	sub3 := filepath.Join(sub2, "sub3")
	_ = os.MkdirAll(sub3, 0755)
	_ = os.WriteFile(filepath.Join(sub3, "SKILL.md"), []byte("---\nname: depth-three\n---\n"), 0644)

	// Excluded directories (Should be skipped)
	nodeMods := filepath.Join(srcDir, "node_modules")
	_ = os.MkdirAll(nodeMods, 0755)
	_ = os.WriteFile(filepath.Join(nodeMods, "SKILL.md"), []byte("---\nname: ignored-node-mods\n---\n"), 0644)

	dotGit := filepath.Join(srcDir, ".git")
	_ = os.MkdirAll(dotGit, 0755)
	_ = os.WriteFile(filepath.Join(dotGit, "SKILL.md"), []byte("---\nname: ignored-git\n---\n"), 0644)

	dirs := findSkillDirectories(srcDir, false)

	// Verify depth-zero, depth-one, and depth-two are found, but others are excluded
	hasZero := false
	hasOne := false
	hasTwo := false
	hasThree := false
	hasNode := false
	hasGit := false

	for _, d := range dirs {
		base := filepath.Base(d)
		if base == "src-root" {
			hasZero = true
		} else if base == "sub1" {
			hasOne = true
		} else if base == "sub2" {
			hasTwo = true
		} else if base == "sub3" {
			hasThree = true
		} else if base == "node_modules" {
			hasNode = true
		} else if base == ".git" {
			hasGit = true
		}
	}

	if !hasZero {
		t.Error("expected depth 0 directory to be scanned")
	}
	if !hasOne {
		t.Error("expected depth 1 directory to be scanned")
	}
	if !hasTwo {
		t.Error("expected depth 2 directory to be scanned")
	}
	if hasThree {
		t.Error("expected depth 3 directory to be EXCLUDED (exceeded depth limit of 2)")
	}
	if hasNode {
		t.Error("expected node_modules directory to be EXCLUDED")
	}
	if hasGit {
		t.Error("expected .git directory to be EXCLUDED")
	}
}

func TestAddSkill_Filters(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "my-filtered-skill")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}

	// Create SKILL.md
	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: filtered-skill\n---\n"), 0644)
	
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
	err := AddSkill(srcDir, AddOptions{Copy: true}, tmpDir)
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
	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: symlink-skill\n---\n"), 0644)

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
	
	// Target should point to "../../.agents/skills/symlink-skill" or similar relative path
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
	// Should execute cleanly without panicking in non-interactive pipeline
	opts := AddOptions{Yes: true}
	maybeSuggestAgy(opts)
}

func TestGitRepoHelpers(t *testing.T) {
	// Test isGitRepoSource
	gitTests := []struct {
		src  string
		want bool
	}{
		{"git@github.com:foo/bar.git", true},
		{"ssh://git@github.com/foo/bar", true},
		{"http://github.com/foo/bar", true},
		{"https://github.com/foo/bar", true},
		{"foo/bar", true},
		{"/absolute/path", false},
		{"./relative/path", false},
		{"../relative/path", false},
		{"C:\\windows\\path", false},
	}

	for _, tt := range gitTests {
		got := isGitRepoSource(tt.src)
		if got != tt.want {
			t.Errorf("isGitRepoSource(%q) = %t, want %t", tt.src, got, tt.want)
		}
	}

	// Test parseRemoteSource
	url1, ref1, name1 := parseRemoteSource("foo/bar@v2")
	if url1 != "https://github.com/foo/bar.git" || ref1 != "" || name1 != "v2" {
		t.Errorf("parseRemoteSource(foo/bar@v2) = (%q, %q, %q)", url1, ref1, name1)
	}

	url2, ref2, name2 := parseRemoteSource("https://github.com/foo/bar@custom-ref")
	if url2 != "https://github.com/foo/bar" || ref2 != "" || name2 != "custom-ref" {
		t.Errorf("parseRemoteSource(https://github.com/foo/bar@custom-ref) = (%q, %q, %q)", url2, ref2, name2)
	}

	url3, ref3, name3 := parseRemoteSource("foo/bar#v1.0.0@v2")
	if url3 != "https://github.com/foo/bar.git" || ref3 != "v1.0.0" || name3 != "v2" {
		t.Errorf("parseRemoteSource(foo/bar#v1.0.0@v2) = (%q, %q, %q)", url3, ref3, name3)
	}
}





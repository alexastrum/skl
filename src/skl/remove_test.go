package skl

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func lcRunRemove(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	names, opts := ParseRemoveOptions(args)
	var out, errb bytes.Buffer
	code := runRemove(names, opts, cwd, &out, &errb, strings.NewReader(""))
	return code, out.String(), errb.String()
}

func lcExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestParseRemoveOptions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSkills []string
		wantOpts   RemoveOptions
	}{
		{"empty args", []string{}, []string{}, RemoveOptions{}},
		{"positional skills", []string{"skill-a", "skill-b"}, []string{"skill-a", "skill-b"}, RemoveOptions{}},
		{"global and yes", []string{"-g", "-y", "skill-c"}, []string{"skill-c"}, RemoveOptions{Global: true, Yes: true}},
		{"agent single", []string{"skill-d", "-a", "antigravity"}, []string{"skill-d"}, RemoveOptions{Agent: []AgentType{AgentAntigravity}}},
		// --all implies -y (upstream parse-remove-options.test.ts).
		{"all and multiple agents", []string{"--all", "-a", "antigravity", "universal"}, []string{}, RemoveOptions{All: true, Yes: true, Agent: []AgentType{AgentAntigravity, AgentUniversal}}},
		{"skill flag multiple values", []string{"-s", "one", "two", "-y"}, []string{"one", "two"}, RemoveOptions{Yes: true}},
		{"skill flag comma", []string{"--skill", "one,two"}, []string{"one", "two"}, RemoveOptions{}},
		{"agent comma", []string{"x", "--agent", "claude-code,cursor"}, []string{"x"}, RemoveOptions{Agent: []AgentType{"claude-code", "cursor"}}},
		{"unknown flags ignored", []string{"--skil", "foo", "--bogus"}, []string{"foo"}, RemoveOptions{}},
		{"project resets global", []string{"-g", "-p", "foo"}, []string{"foo"}, RemoveOptions{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSkills, gotOpts := ParseRemoveOptions(tt.args)
			if !reflect.DeepEqual(gotSkills, tt.wantSkills) {
				t.Errorf("skills = %v, want %v", gotSkills, tt.wantSkills)
			}
			if !reflect.DeepEqual(gotOpts, tt.wantOpts) {
				t.Errorf("opts = %+v, want %+v", gotOpts, tt.wantOpts)
			}
		})
	}
}

func TestResolveSkillsToRemove(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		folders   []string
		lockKeys  []string
		sources   map[string]string
		want      []string
	}{
		{"folder match case-insensitive", []string{"My-Skill"}, []string{"my-skill"}, nil, nil, []string{"my-skill"}},
		{"plugin colon lock key wins", []string{"ce-review"}, []string{"ce-review"}, []string{"ce:review"}, nil, []string{"ce:review"}},
		{"stale lock-only entry", []string{"gone"}, nil, []string{"gone"}, nil, []string{"gone"}},
		{"source expansion", []string{"mattpocock/skills"}, []string{"a", "b"}, []string{"a", "b", "c"},
			map[string]string{"a": "mattpocock/skills", "b": "other/repo", "c": "mattpocock/skills"}, []string{"a", "c"}},
		{"dedupe", []string{"a", "A", "a"}, []string{"a"}, nil, nil, []string{"a"}},
		{"no match", []string{"zzz"}, []string{"a"}, []string{"b"}, nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveSkillsToRemove(tt.requested, tt.folders, tt.lockKeys, tt.sources)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveSkills_Local(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	del := filepath.Join(cwd, ".agents", "skills", "skill-to-delete")
	keep := filepath.Join(cwd, ".agents", "skills", "skill-to-keep")
	lcWriteSkill(t, del, "skill-to-delete", "d")
	lcWriteSkill(t, keep, "skill-to-keep", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{
		"skill-to-delete": {Source: "o/r", SourceType: "github", ComputedHash: "h"},
		"skill-to-keep":   {Source: "o/r", SourceType: "github", ComputedHash: "h"},
	})
	if err := RemoveSkills([]string{"skill-to-delete"}, RemoveOptions{Yes: true}, cwd); err != nil {
		t.Fatal(err)
	}
	if lcExists(del) || !lcExists(keep) {
		t.Errorf("delete=%v keep=%v", lcExists(del), lcExists(keep))
	}
	lock, _ := ReadLocalLock(cwd)
	if _, ok := lock.Skills["skill-to-delete"]; ok {
		t.Error("lock entry should be removed")
	}
	if _, ok := lock.Skills["skill-to-keep"]; !ok {
		t.Error("other lock entry must remain")
	}
}

func TestRemoveSkills_EdgeCases(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	if err := RemoveSkills([]string{"missing-skill-abc"}, RemoveOptions{Yes: true}, cwd); err != nil {
		t.Errorf("missing skill in an empty project should be a no-op, got %v", err)
	}
	del := filepath.Join(cwd, ".agents", "skills", "delete-all-me")
	lcWriteSkill(t, del, "delete-all-me", "d")
	if err := RemoveSkills(nil, RemoveOptions{All: true, Yes: true}, cwd); err != nil {
		t.Fatal(err)
	}
	if lcExists(del) {
		t.Error("--all should delete every project skill")
	}
}

func TestRemoveSkills_CaseInsensitivity(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".agents", "skills", "case-insensitive-skill")
	lcWriteFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: case-insensitive-skill\n---\n")
	if err := RemoveSkills([]string{"Case-Insensitive-Skill"}, RemoveOptions{}, cwd); err != nil {
		t.Fatal(err)
	}
	if lcExists(dir) {
		t.Error("expected case-insensitive match to be removed")
	}
}

func TestRemoveSkills_CanonicalProtection(t *testing.T) {
	// Hermetic HOME so host-detected universal agents (e.g. a real ~/.codex) do not
	// count as remaining users of the canonical directory.
	lcHermetic(t)
	origAgents := Agents
	t.Cleanup(func() { Agents = origAgents })
	const (
		mockA AgentType = "mock-agent-a"
		mockB AgentType = "mock-agent-b"
	)
	Agents = make(map[AgentType]AgentConfig, len(origAgents)+2)
	for k, v := range origAgents {
		Agents[k] = v
	}
	for _, a := range []AgentType{mockA, mockB} {
		dirName := "." + string(a)
		Agents[a] = AgentConfig{
			Name: string(a), DisplayName: string(a), SkillsDir: dirName + "/skills",
			DetectInstalled: func(home, configHome, cwd string) bool { return lcExists(filepath.Join(cwd, dirName)) },
		}
	}

	cwd := t.TempDir()
	aDir := filepath.Join(cwd, ".mock-agent-a", "skills", "test-protect")
	bDir := filepath.Join(cwd, ".mock-agent-b", "skills", "test-protect")
	canonical := filepath.Join(cwd, ".agents", "skills", "test-protect")
	for _, d := range []string{aDir, bDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lcWriteFile(t, filepath.Join(canonical, "SKILL.md"), "---\nname: test-protect\n---\n")

	if err := RemoveSkills([]string{"test-protect"}, RemoveOptions{Agent: []AgentType{mockA}}, cwd); err != nil {
		t.Fatal(err)
	}
	if lcExists(aDir) || !lcExists(bDir) || !lcExists(canonical) {
		t.Fatalf("after removing from A: a=%v b=%v canonical=%v", lcExists(aDir), lcExists(bDir), lcExists(canonical))
	}
	if err := RemoveSkills([]string{"test-protect"}, RemoveOptions{Agent: []AgentType{mockB}}, cwd); err != nil {
		t.Fatal(err)
	}
	if lcExists(bDir) || lcExists(canonical) {
		t.Errorf("after removing from B: b=%v canonical=%v (canonical should be gone)", lcExists(bDir), lcExists(canonical))
	}
}

// Reference-counted cleanup (#287, #1718): a detected universal agent (codex) still
// reads the canonical copy, so both it and the lock entry survive `-a claude-code`.
func TestRunRemove_KeepsCanonicalAndLockForRemainingUniversalAgent(t *testing.T) {
	home := lcHermetic(t)
	for _, d := range []string{".codex", ".claude"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cwd := t.TempDir()
	canonical := filepath.Join(cwd, ".agents", "skills", "shared-skill")
	claude := filepath.Join(cwd, ".claude", "skills", "shared-skill")
	lcWriteSkill(t, canonical, "shared-skill", "d")
	lcWriteSkill(t, claude, "shared-skill", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"shared-skill": {Source: "o/r", SourceType: "github", ComputedHash: "h"}})

	if code, _, errOut := lcRunRemove(t, cwd, "shared-skill", "-a", "claude-code", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if lcExists(claude) {
		t.Error("claude-code copy should be removed")
	}
	if !lcExists(canonical) {
		t.Error("canonical copy must be kept for codex")
	}
	if lock, _ := ReadLocalLock(cwd); lock.Skills["shared-skill"].Source == "" {
		t.Error("lock entry must be kept while codex still uses the skill")
	}

	if code, _, errOut := lcRunRemove(t, cwd, "shared-skill", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if lcExists(canonical) {
		t.Error("canonical copy should be removed when all agents are targeted")
	}
	if lock, _ := ReadLocalLock(cwd); len(lock.Skills) != 0 {
		t.Errorf("lock should be empty, got %v", lock.Skills)
	}
}

func TestRunRemove_AllWithNamesFails(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".agents", "skills", "foo")
	lcWriteSkill(t, dir, "foo", "d")
	for _, args := range [][]string{{"--skill", "foo", "--all"}, {"foo", "*"}} {
		code, _, errOut := lcRunRemove(t, cwd, args...)
		if code != 1 || !strings.Contains(errOut, "[SKILLS_REMOVE_ALL_WITH_NAMES]") {
			t.Errorf("%v: code=%d stderr=%q", args, code, errOut)
		}
	}
	if !lcExists(dir) {
		t.Error("nothing may be deleted when --all is combined with names")
	}
}

func TestRunRemove_InvalidAgentBeforePrompt(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo", "d")
	code, _, errOut := lcRunRemove(t, cwd, "foo", "-a", "bogus")
	if code != 1 || !strings.Contains(errOut, "[SKILLS_INVALID_AGENTS] Invalid agents: bogus") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestRunRemove_NoMatchingSkills(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo", "d")
	code, _, errOut := lcRunRemove(t, cwd, "bar", "-y")
	if code != 0 || !strings.Contains(errOut, "[SKILLS_NO_MATCHING_SKILLS] No matching skills found for: bar") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestRunRemove_PromptRequiresTTY(t *testing.T) {
	lcHermetic(t)
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = orig })
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo", "d")
	code, _, errOut := lcRunRemove(t, cwd)
	if code != 1 || !strings.Contains(errOut, "SKILLS_PROMPT_REQUIRES_TTY") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestRunRemove_InvalidLocalLock(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo", "d")
	lcWriteFile(t, filepath.Join(cwd, "skills-lock.json"), "<<<<<<< HEAD\n")
	code, _, errOut := lcRunRemove(t, cwd, "foo", "-y")
	if code != 1 || !strings.Contains(errOut, "SKILLS_INVALID_LOCAL_LOCK") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "foo")) {
		t.Error("nothing may be removed when the lock is invalid")
	}
}

// #2592 / #2469: from $HOME, a project-scoped remove must not delete a skill that
// only the global lock tracks.
func TestRunRemove_RefusesGloballyTrackedFromHome(t *testing.T) {
	home := lcHermetic(t)
	dir := filepath.Join(home, ".agents", "skills", "foo")
	lcWriteSkill(t, dir, "foo", "d")
	if err := AddSkillToLock("foo", SkillLockEntry{Source: "o/r", SourceType: "github"}); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := lcRunRemove(t, home, "foo", "-y")
	if code != 1 || !strings.Contains(errOut, "[SKILLS_REMOVE_GLOBALLY_TRACKED] Tracked in the global lock: foo") ||
		!strings.Contains(errOut, "remove foo -g") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if !lcExists(dir) {
		t.Fatal("globally tracked skill must not be deleted")
	}

	// Tracked by $HOME/skills-lock.json too: project removal is allowed.
	lcWriteLocalLock(t, home, map[string]LocalSkillLockEntry{"foo": {Source: "o/r", SourceType: "github", ComputedHash: "h"}})
	if code, _, errOut := lcRunRemove(t, home, "foo", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if lcExists(dir) {
		t.Error("expected foo removed once tracked in the project lock")
	}
}

// #609 / #604: global --all only removes skills recorded in .skill-lock.json.
func TestRunRemove_GlobalAllOnlyRemovesLockedSkills(t *testing.T) {
	home := lcHermetic(t)
	managed := filepath.Join(home, ".agents", "skills", "managed")
	manual := filepath.Join(home, ".agents", "skills", "manual")
	lcWriteSkill(t, managed, "managed", "d")
	lcWriteSkill(t, manual, "manual", "d")
	if err := AddSkillToLock("managed", SkillLockEntry{Source: "o/r", SourceType: "github"}); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := lcRunRemove(t, t.TempDir(), "--all", "-g", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if lcExists(managed) {
		t.Error("managed skill should be removed")
	}
	if !lcExists(manual) {
		t.Error("untracked global skill must be left alone")
	}
	if gl, _ := ReadSkillLock(); len(gl.Skills) != 0 {
		t.Errorf("global lock should be empty, got %v", gl.Skills)
	}

	_, out, _ := lcRunRemove(t, t.TempDir(), "--all", "-g", "-y")
	if !strings.Contains(out, "No globally managed skills found to remove.") {
		t.Errorf("unexpected output %q", out)
	}
}

func TestScanInstalledSkillNames_Guards(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "real"), "real", "d")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", ".system"), "system", "d")
	// OpenClaw's project dir is ./skills, which is also where source repos keep
	// non-skill folders such as templates.
	if err := os.MkdirAll(filepath.Join(cwd, "skills", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	lcWriteSkill(t, filepath.Join(cwd, ".claude", "skills", "copied"), "copied", "d")
	got := ScanInstalledSkillNames(false, cwd, nil)
	if want := []string{"copied", "real"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLcRemoveSkillDir_RejectsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-sibling")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	for _, name := range []string{"", ".", "..", "../" + filepath.Base(outside), "a/b", `a\b`} {
		if err := lcRemoveSkillDir(dir, name); err == nil {
			t.Errorf("lcRemoveSkillDir(%q) should be refused", name)
		}
	}
	if !lcExists(outside) || !lcExists(dir) {
		t.Fatal("unsafe removal touched the filesystem")
	}
	lcWriteSkill(t, filepath.Join(dir, "ok"), "ok", "d")
	if err := lcRemoveSkillDir(dir, "ok"); err != nil || lcExists(filepath.Join(dir, "ok")) {
		t.Errorf("safe removal failed: %v", err)
	}
}

// Review #41 audit: crafted lock keys cannot steer removal outside the skills dirs.
func TestRemoveSkills_CraftedLockKeyStaysConfined(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	victim := filepath.Join(cwd, "victim")
	lcWriteSkill(t, victim, "victim", "must survive")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"../victim": {Source: "o/r", SourceType: "github", ComputedHash: "h"}})
	if err := RemoveSkills([]string{"../victim"}, RemoveOptions{Yes: true}, cwd); err != nil {
		t.Fatal(err)
	}
	if !lcExists(filepath.Join(victim, "SKILL.md")) {
		t.Fatal("remove deleted a directory outside the skills dirs")
	}
	if lock, _ := ReadLocalLock(cwd); len(lock.Skills) != 0 {
		t.Errorf("stale lock entry should still be removed: %v", lock.Skills)
	}
}

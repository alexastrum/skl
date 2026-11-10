package skl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Keystroke-driven tests for every converted picker call site.

func withStdinTTY(t *testing.T, tty bool) {
	t.Helper()
	// Ensure Unicode symbols are enabled on Windows too after lcHermetic clears CI,
	// matching instTestEnv/testTerm so Unicode symbol assertions are OS-independent.
	t.Setenv("WT_SESSION", "1")
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { stdinIsTTY = orig })
}

func plainOut(s string) string {
	return strings.ReplaceAll(StripTerminalEscapes(s), "\r\n", "\n")
}

// ---------------------------------------------------------------------------
// remove
// ---------------------------------------------------------------------------

func removeFixture(t *testing.T) string {
	t.Helper()
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "bar"), "bar", "d")
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo", "d")
	return cwd
}

func runRemoveKeys(t *testing.T, cwd, keys string, args ...string) (int, string, string) {
	t.Helper()
	names, opts := ParseRemoveOptions(args)
	var out, errb bytes.Buffer
	code := runRemove(names, opts, cwd, &out, &errb, strings.NewReader(keys))
	return code, plainOut(out.String()), errb.String()
}

func TestRunRemove_InteractivePicker(t *testing.T) {
	cwd := removeFixture(t)
	withStdinTTY(t, true)
	// Picker: down to foo, space, enter. Confirm: enter (Yes).
	code, out, errOut := runRemoveKeys(t, cwd, keyDownSeq+" \r\r")
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	for _, want := range []string{"◆  Select skills to remove (space to toggle)", "│  ◻ bar\n", "◇  Select skills to remove (space to toggle)\n│  foo\n",
		"◇  Are you sure you want to uninstall 1 skill(s)?\n│  Yes\n", "Successfully removed 1 skill(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if lcExists(filepath.Join(cwd, ".agents", "skills", "foo")) || !lcExists(filepath.Join(cwd, ".agents", "skills", "bar")) {
		t.Error("expected only foo removed")
	}
}

func TestRunRemove_InteractiveCancel(t *testing.T) {
	for name, keys := range map[string]string{
		"esc in picker":      "\x1b",
		"ctrl+c in picker":   "\x03",
		"picker eof":         "",
		"no at confirmation": " \rn",
		"esc at confirm":     " \r\x1b",
	} {
		t.Run(name, func(t *testing.T) {
			cwd := removeFixture(t)
			withStdinTTY(t, true)
			code, out, _ := runRemoveKeys(t, cwd, keys)
			if code != 0 || !strings.Contains(out, "Removal cancelled") {
				t.Errorf("code=%d out=%s", code, out)
			}
			if !lcExists(filepath.Join(cwd, ".agents", "skills", "bar")) {
				t.Error("nothing may be removed after a cancel")
			}
		})
	}
}

func TestRunRemove_RequiredSelection(t *testing.T) {
	cwd := removeFixture(t)
	withStdinTTY(t, true)
	// Enter with nothing selected shows clack's required error; then pick bar.
	code, out, _ := runRemoveKeys(t, cwd, "\r \r\r")
	if code != 0 || !strings.Contains(out, "Please select at least one option.") || lcExists(filepath.Join(cwd, ".agents", "skills", "bar")) {
		t.Errorf("code=%d out=%s", code, out)
	}
}

// Without a TTY, named removals keep reading a y/N line from piped stdin.
func TestRunRemove_PipedConfirmUnchanged(t *testing.T) {
	cwd := removeFixture(t)
	withStdinTTY(t, false)
	code, out, _ := runRemoveKeys(t, cwd, "y\n", "foo")
	if code != 0 || !strings.Contains(out, "Are you sure you want to uninstall 1 skill(s)? (y/N) ") || lcExists(filepath.Join(cwd, ".agents", "skills", "foo")) {
		t.Errorf("code=%d out=%s", code, out)
	}
	code, out, _ = runRemoveKeys(t, cwd, "", "bar")
	if code != 0 || !strings.Contains(out, "Removal cancelled") || !lcExists(filepath.Join(cwd, ".agents", "skills", "bar")) {
		t.Errorf("EOF must cancel: code=%d out=%s", code, out)
	}
}

// ---------------------------------------------------------------------------
// update / check
// ---------------------------------------------------------------------------

func TestResolveUpdateScope_Interactive(t *testing.T) {
	lcHermetic(t)
	withStdinTTY(t, true)
	for _, c := range []struct {
		keys string
		want UpdateScope
		ok   bool
	}{
		{"\r", UpdateScopeProject, true},
		{keyDownSeq + "\r", UpdateScopeGlobal, true},
		{keyUpSeq + "\r", UpdateScopeBoth, true},
		{"\x1b", "", false},
		{"\x03", "", false},
	} {
		var out bytes.Buffer
		opts := UpdateCheckOptions{Cwd: t.TempDir(), Stdin: strings.NewReader(c.keys), Stdout: &out}
		got, ok := ResolveUpdateScope(&opts)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %q %v", c.keys, got, ok)
		}
		plain := plainOut(out.String())
		if !strings.Contains(plain, "◆  Update scope\n│  ● Project (Update skills in current directory)\n│  ○ Global\n│  ○ Both\n") {
			t.Errorf("%q: scope picker:\n%s", c.keys, plain)
		}
		if !c.ok && !strings.HasSuffix(plain, "Cancelled\n") {
			t.Errorf("%q: cancel must print Cancelled:\n%s", c.keys, plain)
		}
	}
}

// Review #65 item 3: source and deleted names come from a possibly untrusted
// skills-lock.json and must not reach the terminal with escape sequences.
func TestPromptDeletions_SanitizesEscapes(t *testing.T) {
	lcHermetic(t)
	withStdinTTY(t, true)
	var out bytes.Buffer
	opts := UpdateCheckOptions{Cwd: t.TempDir(), Stdin: strings.NewReader("\r"), Stdout: &out}
	promptDeletions(&opts, "evil\x1b]0;pwned\x07/repo\x1b[2J", []string{"gone\x1b[31m-red\x1b[0m"}, false)
	got := out.String()
	for _, bad := range []string{"\x1b]0;", "\x1b[2J", "\x1b[31m", "\x07"} {
		if strings.Contains(got, bad) {
			t.Errorf("output contains %q:\n%q", bad, got)
		}
	}
	plain := plainOut(got)
	for _, want := range []string{"skills from evil/repo appear", "• gone-red\n", "Removing gone-red..."} {
		if !strings.Contains(plain, want) {
			t.Errorf("output lacks %q:\n%s", want, plain)
		}
	}
}

func TestPromptDeletions_Interactive(t *testing.T) {
	lcHermetic(t)
	withStdinTTY(t, true)
	run := func(keys string) string {
		var out bytes.Buffer
		opts := UpdateCheckOptions{Cwd: t.TempDir(), Stdin: strings.NewReader(keys), Stdout: &out}
		promptDeletions(&opts, "owner/repo", []string{"gone"}, false)
		return plainOut(out.String())
	}
	out := run("n")
	if !strings.Contains(out, "◇  Would you like to remove the local copies of these deleted skills?\n│  No\n") || strings.Contains(out, "Removing") {
		t.Errorf("declined:\n%s", out)
	}
	out = run("\r")
	if !strings.Contains(out, "│  Yes\n") || !strings.Contains(out, "Removing gone...") {
		t.Errorf("accepted:\n%s", out)
	}
	if out := run("\x1b"); strings.Contains(out, "Removing") {
		t.Errorf("cancel must not remove:\n%s", out)
	}
	withStdinTTY(t, false)
	if out := run("\r"); !strings.Contains(out, "Skipping deletion in non-interactive mode.") || strings.Contains(out, "Would you like") {
		t.Errorf("non-interactive:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// find
// ---------------------------------------------------------------------------

func stubFindSearch(t *testing.T, results []FoundSkill) *[]string {
	t.Helper()
	origSearch, origDebounce := findSearchFn, findDebounce
	t.Cleanup(func() { findSearchFn, findDebounce = origSearch, origDebounce })
	var (
		mu      sync.Mutex
		queries []string
	)
	findSearchFn = func(q, owner string) ([]FoundSkill, error) {
		mu.Lock()
		defer mu.Unlock()
		queries = append(queries, q+"|"+owner)
		return results, nil
	}
	findDebounce = func(string) time.Duration { return 0 }
	return &queries
}

func TestRunFind_InteractiveSearch(t *testing.T) {
	lcHermetic(t)
	withStdinTTY(t, true)
	queries := stubFindSearch(t, []FoundSkill{
		{Name: "react-one", Source: "acme/skills", Slug: "acme/skills/react-one", Installs: 1500},
		{Name: "react-two", Source: "acme/skills", Slug: "acme/skills/react-two"},
	})
	out := &syncBuffer{}
	in := &waitReader{out: out, steps: [][2]string{
		{"Start typing to search (min 2 chars)", "r"},
		{"Search skills:\x1b[0m r", "e"},
		{"react-two", keyDownSeq},
		{"\x1b[1mreact-two", "\r"},
	}}
	var errb bytes.Buffer
	code := runFind([]string{"--owner", "acme"}, t.TempDir(), out, &errb, in)
	plain := plainOut(out.String())
	if code != 0 || !strings.Contains(plain, "Installing react-two from acme/skills") || !strings.Contains(plain, "Run: skl add acme/skills --skill react-two") {
		t.Fatalf("code=%d out=%s err=%s", code, plain, errb.String())
	}
	for _, want := range []string{"  > react-one acme/skills 1.5K installs", "up/down navigate | enter select | esc cancel"} {
		if !strings.Contains(plain, want) {
			t.Errorf("output lacks %q:\n%s", want, plain)
		}
	}
	if len(*queries) == 0 || (*queries)[len(*queries)-1] != "re|acme" {
		t.Errorf("queries = %v", *queries)
	}
}

func TestRunFind_InteractiveCancel(t *testing.T) {
	lcHermetic(t)
	withStdinTTY(t, true)
	stubFindSearch(t, nil)
	for _, keys := range []string{"\x1b", "\x03", "xy\x7f\x7f\r", ""} {
		var out, errb bytes.Buffer
		code := runFind(nil, t.TempDir(), &out, &errb, strings.NewReader(keys))
		if code != 0 || !strings.Contains(plainOut(out.String()), "Search cancelled") {
			t.Errorf("%q: code=%d out=%q", keys, code, out.String())
		}
	}
}

// Review #65 item 2: Esc or Enter while a debounce timer is armed must stop it,
// so no search runs after the prompt returns (run with -race).
func TestRunFind_ExitStopsPendingSearch(t *testing.T) {
	lcHermetic(t)
	withStdinTTY(t, true)
	for _, keys := range []string{"ab\x1b", "abc\r", "ab\x03"} {
		var calls atomic.Int32
		origSearch, origDebounce := findSearchFn, findDebounce
		findSearchFn = func(string, string) ([]FoundSkill, error) {
			calls.Add(1)
			return nil, nil
		}
		findDebounce = func(string) time.Duration { return 20 * time.Millisecond }
		var out, errb bytes.Buffer
		code := runFind(nil, t.TempDir(), &out, &errb, strings.NewReader(keys))
		// Restore at once, as t.Cleanup would: a still-armed timer must not
		// read the globals.
		findSearchFn, findDebounce = origSearch, origDebounce
		if code != 0 {
			t.Errorf("%q: code=%d err=%s", keys, code, errb.String())
		}
		time.Sleep(80 * time.Millisecond)
		if n := calls.Load(); n != 0 {
			t.Errorf("%q: search ran %d time(s) after the prompt returned", keys, n)
		}
	}
}

// ---------------------------------------------------------------------------
// add
// ---------------------------------------------------------------------------

func runAddKeys(t *testing.T, cwd, keys string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	yes := true
	code := RunAddWithIO(args, AddIO{Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(keys), Cwd: cwd, Interactive: &yes})
	return code, plainOut(out.String()), errb.String()
}

func TestRunAdd_InteractiveScopeAndMethod(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	// Skills: space on Select All, enter. Scope: enter (Project). Method: down,
	// enter (Copy). Confirm: y.
	code, out, errOut := runAddKeys(t, cwd, " \r\r"+keyDownSeq+"\ry", src, "-a", "claude-code,cursor")
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	for _, want := range []string{
		"◆  Select skills to install\n│  Search:  \n",
		"│ ❯ ○ Select All (0/2)\n",
		"│  Description\n│  Select or clear all 2 skills.\n",
		"◇  Select skills to install\n│  alpha, beta\n",
		"◆  Installation scope\n│  ● Project (Install in current directory (committed with your project))\n│  ○ Global\n",
		"◆  Installation method\n│  ● Symlink (Recommended) (Single source of truth, easy updates)\n│  ○ Copy to all agents\n",
		"◇  Installation method\n│  Copy to all agents\n",
		"◇  Proceed with installation?\n│  Yes\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	fi, err := os.Lstat(filepath.Join(cwd, ".claude", "skills", "alpha"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("copy mode must write a real directory: %v %v", fi, err)
	}
}

func TestRunAdd_InteractiveGlobalScope(t *testing.T) {
	home, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
	code, out, errOut := runAddKeys(t, cwd, "\x01\r"+keyDownSeq+"\r\r", src, "-a", "codex")
	if code != 0 || !strings.Contains(out, "◇  Installation scope\n│  Global\n") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	if !instExists(filepath.Join(home, ".agents", "skills", "alpha")) || instExists(filepath.Join(cwd, ".agents", "skills", "alpha")) {
		t.Errorf("expected a global install:\n%s", out)
	}
}

func TestRunAdd_InteractiveCancel(t *testing.T) {
	for name, keys := range map[string]string{
		"esc at skills":        "\x1b",
		"ctrl+c at scope":      "\x01\r\x03",
		"no at confirmation":   "\x01\r\rn",
		"esc at confirmation":  "\x01\r\r\x1b",
		"input ends at prompt": "\x01\r",
	} {
		t.Run(name, func(t *testing.T) {
			_, cwd := instTestEnv(t)
			src := addTwoSkillSource(t)
			addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })
			code, out, _ := runAddKeys(t, cwd, keys, src, "-a", "codex")
			if code != 0 || !strings.Contains(out, "Installation cancelled") {
				t.Errorf("code=%d out=%s", code, out)
			}
			if instExists(filepath.Join(cwd, ".agents", "skills", "alpha")) {
				t.Error("nothing may be installed after a cancel")
			}
		})
	}
}

func TestRunAdd_GroupedSkillPicker(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) {
		f := addFakeLocalFetch(t, src)
		f.Skills[0].PluginName = "data-tools"
		return f, nil
	})
	// Rows: Select All, Data Tools heading, alpha, Other heading, beta.
	code, out, errOut := runAddKeys(t, cwd, keyDownSeq+" \r\r\r", src, "-a", "codex")
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	for _, want := range []string{"◆  Select skills to install (space to toggle)\n", "│ ❯ ▾ ○ Data Tools\n│   └─ ○ alpha\n│   ▾ ○ Other\n│   └─ ○ beta\n",
		"│  ↑↓ move, ←→ collapse/expand, space select, enter confirm\n", "◇  Select skills to install (space to toggle)\n│  alpha\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !instExists(filepath.Join(cwd, ".agents", "skills", "alpha")) || instExists(filepath.Join(cwd, ".agents", "skills", "beta")) {
		t.Error("expected only alpha installed")
	}
}

func TestAddSelectSkills_WellKnownPack(t *testing.T) {
	term, out := testTerm("\r")
	long := strings.Repeat("x", 70)
	skills := []FetchedSkill{{Skill: Skill{Name: "one", Description: long}}, {Skill: Skill{Name: "two", Description: "short"}}}
	got, err := addSelectSkills(term, skills, true, true)
	if err != nil || len(got) != 2 {
		t.Fatalf("packs start fully selected: %v %v", got, err)
	}
	first := frames(out.String())[0]
	if !strings.Contains(first, "│   ● one ("+strings.Repeat("x", 57)+"…)\n") || !strings.Contains(first, "│   ● two (short)\n") || strings.Contains(first, "Description") {
		t.Errorf("well-known picker:\n%s", first)
	}
}

func TestAddSelectAgents_LockedUniversal(t *testing.T) {
	instTestEnv(t)
	term, out := testTerm(append(strings.Split("claude-code", ""), " ", "\r")...)
	got, err := addSelectAgents(term, false)
	if err != nil {
		t.Fatal(err)
	}
	visible := GetVisibleUniversalAgents()
	if len(got) != len(visible)+1 || got[len(got)-1] != "claude-code" {
		t.Errorf("got %v, want visible universal agents + claude-code", got)
	}
	first := frames(out.String())[0]
	if !strings.Contains(first, "── Universal (.agents/skills) ── always included") || !strings.Contains(first, "── Additional agents") {
		t.Errorf("locked section missing:\n%s", first)
	}
	if last, _ := GetLastSelectedAgents(); len(last) != len(got) {
		t.Errorf("selection not saved: %v", last)
	}
	// The saved non-universal choice is preselected next time.
	term, _ = testTerm("\r")
	again, err := addSelectAgents(term, false)
	if err != nil || again[len(again)-1] != "claude-code" {
		t.Errorf("remembered selection: %v %v", again, err)
	}
}

func TestAddPromptForAgents_Defaults(t *testing.T) {
	instTestEnv(t)
	term, out := testTerm("\r")
	got, err := addPromptForAgents(term)
	if err != nil {
		t.Fatal(err)
	}
	var want []AgentType
	for _, a := range []AgentType{"claude-code", "opencode", "codex", "grok"} {
		if _, ok := Agents[a]; ok {
			want = append(want, a)
		}
	}
	if strings.Join(addAgentStrings(got), ",") != strings.Join(addAgentStrings(want), ",") {
		t.Errorf("got %v, want %v", got, want)
	}
	if strings.Contains(out.String(), "eve") {
		t.Error("eve must not be offered")
	}
	// Required: clearing everything blocks Enter until something is picked.
	term, _ = testTerm("\x01", "\x01", "\r", " ", "\r")
	got, err = addPromptForAgents(term)
	if err != nil || len(got) != 1 {
		t.Errorf("required selection: %v %v", got, err)
	}
}

func TestMaybeSuggestAgy_Interactive(t *testing.T) {
	term, _ := testTerm("\r")
	var w bytes.Buffer
	maybeSuggestAgyTo(&w, term, AddOptions{}, true)
	if !strings.Contains(w.String(), "No active AI agents detected on your system.") || !strings.Contains(w.String(), "Excellent choice!") {
		t.Errorf("accepted: %q", w.String())
	}
	term, _ = testTerm("n")
	w.Reset()
	maybeSuggestAgyTo(&w, term, AddOptions{}, true)
	if strings.Contains(w.String(), "Excellent choice!") {
		t.Errorf("declined: %q", w.String())
	}
}

// ---------------------------------------------------------------------------
// sync
// ---------------------------------------------------------------------------

func TestPromptSyncAgents(t *testing.T) {
	instTestEnv(t)
	visible := GetVisibleUniversalAgents()
	term, out := testTerm("\r")
	got, err := promptSyncAgents(term, []AgentType{"claude-code", "cursor"}, []AgentType{"cursor"})
	if err != nil || len(got) != len(visible)+1 || got[len(got)-1] != "cursor" {
		t.Fatalf("fallback preselects detected agents: %v %v", got, err)
	}
	if !strings.Contains(frames(out.String())[0], "│ ❯ ○ Claude Code (.claude/skills)\n│   ● Cursor") {
		t.Errorf("picker:\n%s", frames(out.String())[0])
	}
	if err := SaveSelectedAgents([]string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	term, _ = testTerm("\r")
	got, _ = promptSyncAgents(term, []AgentType{"claude-code", "cursor"}, []AgentType{"cursor"})
	if got[len(got)-1] != "claude-code" {
		t.Errorf("remembered choice wins over fallback: %v", got)
	}
	term, _ = testTerm("\x1b")
	if _, err := promptSyncAgents(term, []AgentType{"cursor"}, nil); err != ErrPromptCancel {
		t.Errorf("cancel: %v", err)
	}
}

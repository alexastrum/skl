package skl

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runUseForTest(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := RunUseWithIO(args, UseIO{Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Cwd: cwd})
	return code, out.String(), errb.String()
}

func TestParseUseOptions(t *testing.T) {
	instTestEnv(t)
	p := ParseUseOptions([]string{"owner/repo@skill", "--full-depth", "-s", "skill", "-a", "codex"})
	if len(p.Errors) != 0 || len(p.Sources) != 1 || p.Options.Skill != "skill" || !p.Options.FullDepth || len(p.Options.Agent) != 1 {
		t.Fatalf("parse = %+v", p)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"src", "--skill"}, "--skill requires a skill name"},
		{[]string{"src", "-s", "a", "-s", "b"}, "Only one --skill value can be provided"},
		{[]string{"src", "-a"}, "-a requires an agent name"},
		{[]string{"src", "-a", "*"}, "skills use --agent does not support '*'; specify exactly one agent."},
		{[]string{"src", "-a", "codex", "claude-code"}, "skills use --agent accepts exactly one agent."},
		{[]string{"src", "-a", "nope"}, "Invalid agents: nope"},
		{[]string{"src", "--bogus"}, "Unknown option: --bogus"},
	}
	for _, c := range cases {
		p := ParseUseOptions(c.args)
		found := false
		for _, e := range p.Errors {
			if strings.Contains(e.Error(), c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%v: errors %v, want %q", c.args, p.Errors, c.want)
		}
	}
	if p := ParseUseOptions([]string{"-h"}); !p.Options.Help {
		t.Error("-h must set Help")
	}
}

func TestRunUse_FailsBeforeFetch(t *testing.T) {
	_, cwd := instTestEnv(t)
	addStub(t, addFetchMustNotRun(t))

	code, stdout, stderr := runUseForTest(t, cwd, "owner/repo", "-a", "cursor")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "Running Cursor is not supported yet.") || !strings.Contains(stderr, "claude-code, codex, sarvam-code") {
		t.Errorf("unsupported agent: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runUseForTest(t, cwd, "owner/repo@alpha", "--skill", "beta")
	if code != 1 || !strings.Contains(stderr, "SKILLS_CONFLICTING_SKILL_SELECTORS") {
		t.Errorf("conflicting selectors: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runUseForTest(t, cwd)
	if code != 1 || !strings.Contains(stderr, "Missing required argument: source") || !strings.Contains(stderr, "Usage: skl use") {
		t.Errorf("missing source: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runUseForTest(t, cwd, "a/b", "c/d")
	if code != 1 || !strings.Contains(stderr, "Expected one source, received 2") {
		t.Errorf("multiple sources: code=%d stderr=%q", code, stderr)
	}
	code, stdout, _ = runUseForTest(t, cwd, "--help")
	if code != 0 || !strings.HasPrefix(stdout, "Usage: skl use") {
		t.Errorf("help: code=%d stdout=%q", code, stdout)
	}
}

func TestBuildUsePrompt(t *testing.T) {
	got := BuildUsePrompt("BODY", "/tmp/x", false)
	want := "You are being given a Skill to execute for the user's next request.\n\nUse the following SKILL.md as your instructions:\n\n<SKILL.md>\nBODY\n</SKILL.md>\n"
	if got != want {
		t.Errorf("prompt = %q", got)
	}
	got = BuildUsePrompt("BODY", "/tmp/x", true)
	if !strings.HasSuffix(got, "</SKILL.md>\n\nSupporting files for this skill were downloaded to:\n/tmp/x\n\nWhen the SKILL.md references relative paths, read them from that directory.\n") {
		t.Errorf("prompt with support dir = %q", got)
	}
}

func TestMaterializeUseSkill(t *testing.T) {
	root := t.TempDir()
	only := instWriteSkill(t, filepath.Join(root, "only"), "only")
	m, err := MaterializeUseSkill(Skill{Name: "only", Path: only}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(m.TempRoot)
	if m.HasSupportingFiles || !strings.HasPrefix(filepath.Base(m.TempRoot), "skills-use-") || filepath.Base(m.SkillDir) != "only" {
		t.Errorf("materialized = %+v", m)
	}
	if !strings.Contains(m.SkillMd, "name: only") {
		t.Errorf("SkillMd = %q", m.SkillMd)
	}

	rich := instWriteSkill(t, filepath.Join(root, "rich"), "rich")
	_ = os.MkdirAll(filepath.Join(rich, "refs"), 0o755)
	_ = os.WriteFile(filepath.Join(rich, "refs", "a.md"), []byte("a"), 0o644)
	if runtime.GOOS != "windows" {
		_ = os.WriteFile(filepath.Join(root, "secret.txt"), []byte("s"), 0o600)
		instSymlink(t, filepath.Join(root, "secret.txt"), filepath.Join(rich, "leak.txt"))
	}
	m2, err := MaterializeUseSkill(Skill{Name: "Rich Skill", Path: rich}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(m2.TempRoot)
	if !m2.HasSupportingFiles || filepath.Base(m2.SkillDir) != "rich-skill" {
		t.Errorf("materialized = %+v", m2)
	}
	if instExists(filepath.Join(m2.SkillDir, "leak.txt")) {
		t.Error("escaping symlink must not be materialized (#2652)")
	}
}

func TestRunUse_PrintsOnlyThePrompt(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) {
		if !req.NoFullDepthRetry || !req.IncludeInternal {
			t.Errorf("use must disable the full-depth retry and include internal skills for a selector: %+v", req)
		}
		return addFakeLocalFetch(t, src), nil
	})
	code, stdout, stderr := runUseForTest(t, cwd, src, "--skill", "beta")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	raw, _ := os.ReadFile(filepath.Join(src, "beta", "SKILL.md"))
	if stdout != BuildUsePrompt(string(raw), "", false) {
		t.Errorf("stdout must be exactly the prompt, got %q", stdout)
	}
	if instExists(filepath.Join(cwd, ".agents")) {
		t.Error("use must not install anything")
	}
}

func TestRunUse_SelectionErrors(t *testing.T) {
	_, cwd := instTestEnv(t)
	src := addTwoSkillSource(t)
	addStub(t, func(req SourceRequest) (*FetchedSource, error) { return addFakeLocalFetch(t, src), nil })

	code, _, stderr := runUseForTest(t, cwd, src)
	if code != 1 || !strings.Contains(stderr, "This source contains multiple skills") || !strings.Contains(stderr, "skl use "+src+"@alpha") {
		t.Errorf("multiple: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runUseForTest(t, cwd, src, "-s", "gamma")
	if code != 1 || !strings.Contains(stderr, "No matching skill found for: gamma") || !strings.Contains(stderr, "- alpha") {
		t.Errorf("no match: code=%d stderr=%q", code, stderr)
	}
}

func TestLaunchAgentInteractively(t *testing.T) {
	instTestEnv(t)
	orig := useRunCommand
	t.Cleanup(func() { useRunCommand = orig })

	var gotName string
	var gotArgs []string
	useRunCommand = func(name string, args []string, _ io.Reader, _, _ io.Writer) error {
		gotName, gotArgs = name, args
		return nil
	}
	code, err := LaunchAgentInteractively("claude-code", "PROMPT", nil, io.Discard, io.Discard)
	if err != nil || code != 0 || gotName != "claude" || len(gotArgs) != 1 || gotArgs[0] != "PROMPT" {
		t.Errorf("claude launch: code=%d err=%v name=%s args=%v", code, err, gotName, gotArgs)
	}

	useRunCommand = func(name string, args []string, _ io.Reader, _, _ io.Writer) error {
		return &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	_, err = LaunchAgentInteractively("codex", "P", nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Could not launch Codex: command not found: codex") {
		t.Errorf("missing binary: %v", err)
	}

	if runtime.GOOS != "windows" {
		useRunCommand = func(string, []string, io.Reader, io.Writer, io.Writer) error {
			return exec.Command("sh", "-c", "exit 3").Run()
		}
		code, err = LaunchAgentInteractively("sarvam-code", "P", nil, io.Discard, io.Discard)
		if err != nil || code != 3 {
			t.Errorf("exit code propagation: code=%d err=%v", code, err)
		}
	}

	if _, err := LaunchAgentInteractively("cursor", "P", nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unsupported agent: %v", err)
	}
}

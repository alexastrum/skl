package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets tests re-execute the test binary as the skl CLI.
func TestMain(m *testing.M) {
	if os.Getenv("SKL_MAIN_HELPER") == "1" {
		var args []string
		_ = json.Unmarshal([]byte(os.Getenv("SKL_MAIN_ARGS")), &args)
		os.Exit(run(args, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

type cliResult struct {
	code   int
	stdout string
	stderr string
}

// runCLI runs the CLI in a child process with a hermetic HOME/XDG/agent/CI env.
func runCLI(t *testing.T, cwd string, extraEnv []string, args ...string) cliResult {
	t.Helper()
	home := filepath.Join(cwd, ".home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Dir = cwd
	cmd.Env = append([]string{
		"SKL_MAIN_HELPER=1",
		"SKL_MAIN_ARGS=" + string(encoded),
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".state"),
		"SKILLS_API_URL=http://127.0.0.1:9", // never reach the network
		"SystemRoot=" + os.Getenv("SystemRoot"),
	}, extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Stdin = strings.NewReader("")
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return cliResult{code, out.String(), errb.String()}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+name+"\ndescription: test skill\n---\n# "+name+"\n")
}

func TestVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "version"} {
		r := runCLI(t, t.TempDir(), nil, flag)
		if r.code != 0 || r.stdout != "0.7.6\n" {
			t.Errorf("%s: code=%d stdout=%q", flag, r.code, r.stdout)
		}
	}
	// -v after a subcommand means --verbose, not version.
	r := runCLI(t, t.TempDir(), nil, "update", "-v", "-p", "-y")
	if strings.TrimSpace(r.stdout) == "0.7.6" {
		t.Error("update -v must not print the version")
	}
}

// TestVersionMirrorsUpstream keeps the skl version in step with the tracked
// upstream release: skills 1.X.Y maps to skl 0.X.Y. When syncing with a new
// upstream release, bump both upstreamVersion and version.
func TestVersionMirrorsUpstream(t *testing.T) {
	major, rest, ok := strings.Cut(upstreamVersion, ".")
	if !ok || major != "1" {
		t.Fatalf("upstreamVersion %q: expected a 1.x.y release; revisit the version mapping", upstreamVersion)
	}
	if want := "0." + rest; version != want {
		t.Errorf("version = %q, want %q (mirrors upstream %s)", version, want, upstreamVersion)
	}
}

func TestBanner(t *testing.T) {
	r := runCLI(t, t.TempDir(), nil)
	if r.code != 0 || !strings.Contains(r.stdout, "skl add") || !strings.Contains(r.stdout, "https://skills.sh/") {
		t.Errorf("banner: code=%d stdout=%q", r.code, r.stdout)
	}
	r = runCLI(t, t.TempDir(), []string{"CLAUDECODE=1"})
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("in an agent nothing is printed: %+v", r)
	}
}

func TestHelpHasNoSideEffects(t *testing.T) {
	for _, cmd := range []string{"add", "use", "update", "check", "list", "init", "find", "remove", "experimental_install", "install", "i", "sync", "experimental_sync", "config"} {
		cwd := t.TempDir()
		for _, flag := range []string{"--help", "-h"} {
			r := runCLI(t, cwd, nil, cmd, flag)
			if r.code != 0 || !strings.Contains(r.stdout, "Usage") {
				t.Errorf("%s %s: code=%d stdout=%q stderr=%q", cmd, flag, r.code, r.stdout, r.stderr)
			}
			if strings.Contains(r.stdout, "Checking for skill updates") || strings.Contains(r.stdout, "\u2014") {
				t.Errorf("%s %s: ran the command or used an em dash", cmd, flag)
			}
		}
		if _, err := os.Stat(filepath.Join(cwd, "SKILL.md")); err == nil {
			t.Errorf("%s --help created files", cmd)
		}
	}
	r := runCLI(t, t.TempDir(), nil, "--help")
	if r.code != 0 || !strings.Contains(r.stdout, "experimental_sync") || strings.Contains(r.stdout, "\u2014") {
		t.Errorf("top-level help: %+v", r)
	}
}

// #2683: `install`/`i` without a package restores skills-lock.json instead of
// failing with a missing-source error; `add` still requires a source.
func TestInstallWithoutSourceRestoresFromLock(t *testing.T) {
	for _, cmd := range []string{"install", "i"} {
		r := runCLI(t, t.TempDir(), nil, cmd)
		if r.code != 0 || !strings.Contains(r.stdout, "No project skills found in skills-lock.json") {
			t.Errorf("%s: %+v", cmd, r)
		}
		r = runCLI(t, t.TempDir(), nil, cmd, "--frozen-lockfile")
		if r.code != 1 || !strings.Contains(r.stderr, "absent or has no skills") {
			t.Errorf("%s --frozen-lockfile: %+v", cmd, r)
		}
	}
	r := runCLI(t, t.TempDir(), nil, "add")
	if r.code != 1 || !strings.Contains(r.stderr, "Missing required argument: source") {
		t.Errorf("add without source: %+v", r)
	}
}

func TestUnknownCommand(t *testing.T) {
	r := runCLI(t, t.TempDir(), nil, "Unknown-Command")
	want := "[SKILLS_UNKNOWN_COMMAND] Unknown command: Unknown-Command\n╰▶ fix: Run skl --help for usage."
	if r.code != 1 || !strings.Contains(r.stdout, want) {
		t.Errorf("code=%d stdout=%q", r.code, r.stdout)
	}
}

func TestInvalidLocalLockAcrossCommands(t *testing.T) {
	for _, args := range [][]string{{"install"}, {"experimental_install"}, {"remove", "foo", "-y"}, {"update", "-p", "-y"}, {"check", "-p"}, {"sync", "-y"}} {
		cwd := t.TempDir()
		writeFile(t, filepath.Join(cwd, "skills-lock.json"), "<<<<<<< HEAD\n")
		writeFile(t, filepath.Join(cwd, "package.json"), `{"name":"app"}`)
		writeSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo")
		r := runCLI(t, cwd, nil, args...)
		if r.code != 1 || !strings.Contains(r.stderr, "SKILLS_INVALID_LOCAL_LOCK") {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, r.code, r.stdout, r.stderr)
		}
	}
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "skills-lock.json"), "<<<<<<< HEAD\n")
	writeSkill(t, filepath.Join(cwd, ".agents", "skills", "foo"), "foo")
	r := runCLI(t, cwd, nil, "list")
	if r.code != 0 || !strings.Contains(r.stderr, "Warning:") || !strings.Contains(r.stdout, "foo") {
		t.Errorf("list degrades gracefully: %+v", r)
	}
}

func TestListAndCheckNeverShowLogo(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"ls", "--json"}, {"check", "-p"}, {"update", "-p", "-y"}, {"remove", "nothing", "-y"}} {
		r := runCLI(t, t.TempDir(), nil, args...)
		if strings.Contains(r.stdout, "███") {
			t.Errorf("%v printed the logo", args)
		}
	}
	r := runCLI(t, t.TempDir(), nil, "ls", "--json")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "[]" {
		t.Errorf("ls --json: %+v", r)
	}
}

// A bare "help" argument is a skill name, not a help flag.
func TestRemoveSkillNamedHelp(t *testing.T) {
	cwd := t.TempDir()
	writeSkill(t, filepath.Join(cwd, ".agents", "skills", "help"), "help")
	r := runCLI(t, cwd, nil, "remove", "help", "-y")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".agents", "skills", "help")); !os.IsNotExist(err) {
		t.Error("skill named help should be removed")
	}
}

func TestInitAndConfig(t *testing.T) {
	cwd := t.TempDir()
	r := runCLI(t, cwd, nil, "init", "my-skill")
	if r.code != 0 || !strings.Contains(r.stdout, "Initialized skill:") {
		t.Fatalf("init: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(cwd, "my-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	r = runCLI(t, cwd, nil, "init", "my-skill")
	if r.code != 0 || !strings.Contains(r.stdout, "Skill already exists at") {
		t.Errorf("init twice: %+v", r)
	}
	if r = runCLI(t, cwd, nil, "config", "set", "canonicalDir.project", "custom/skills"); r.code != 0 {
		t.Fatalf("config set: %+v", r)
	}
	if r = runCLI(t, cwd, nil, "config", "get", "canonicalDir.project"); r.code != 0 || strings.TrimSpace(r.stdout) != "custom/skills" {
		t.Errorf("config get: %+v", r)
	}
	if r = runCLI(t, cwd, nil, "config", "nope", "x"); r.code != 1 {
		t.Errorf("unknown config subcommand: %+v", r)
	}
	if r = runCLI(t, cwd, nil, "config"); r.code != 1 {
		t.Errorf("config without args: %+v", r)
	}
}

func TestFindEmptyQueryInPipePrintsTip(t *testing.T) {
	r := runCLI(t, t.TempDir(), nil, "find")
	if r.code != 0 || !strings.Contains(r.stdout, "Tip: if running in a coding agent") {
		t.Errorf("%+v", r)
	}
}

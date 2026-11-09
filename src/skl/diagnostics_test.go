package skl

import (
	"errors"
	"fmt"
	"testing"
)

func TestFormatErrorMatchesUpstreamFormatter(t *testing.T) {
	// Upstream src/cli.test.ts unknown-command snapshot.
	got := FormatError(ErrUnknownCommand("unknown-command"))
	want := "[SKILLS_UNKNOWN_COMMAND] Unknown command: unknown-command\n╰▶ fix: Run skills --help for usage."
	if got != want {
		t.Errorf("FormatError =\n%q\nwant\n%q", got, want)
	}
	multi := FormatError(ErrNoMatchingSkills([]string{"x"}, []string{"a", "b"}))
	wantMulti := "[SKILLS_NO_MATCHING_SKILLS] No matching skills found for: x\n╰▶ fix: Available skills:\n          - a\n          - b"
	if multi != wantMulti {
		t.Errorf("multi-line fix =\n%q\nwant\n%q", multi, wantMulti)
	}
	if FormatError(errors.New("plain")) != "plain" || FormatError(nil) != "" {
		t.Error("plain errors must render unchanged")
	}
	noFix := ErrNoMatchingSkills([]string{"x"}, nil)
	if noFix.Fix != "" || noFix.Error() != "No matching skills found for: x" {
		t.Errorf("empty fix must be omitted: %q", noFix.Error())
	}
}

func TestDiagnosticErrorTextAndMatching(t *testing.T) {
	d := ErrInvalidAgents([]string{"nope"}, []string{"claude-code", "cursor"})
	if d.Error() != "Invalid agents: nope\nValid agents: claude-code, cursor" {
		t.Errorf("Error() = %q", d.Error())
	}
	wrapped := fmt.Errorf("context: %w", d)
	if !IsDiagnostic(wrapped) || !IsDiagnostic(wrapped, CodeUnknownOption, CodeInvalidAgents) || IsDiagnostic(wrapped, CodeUnknownOption) {
		t.Error("IsDiagnostic code matching through wrap")
	}
	if DiagnosticCode(wrapped) != CodeInvalidAgents || DiagnosticCode(errors.New("x")) != "" {
		t.Error("DiagnosticCode")
	}
	if IsDiagnostic(nil) || IsDiagnostic(errors.New("x")) {
		t.Error("non-diagnostics must not match")
	}
	// Legacy lock.go sentinel counts as SKILLS_INVALID_LOCAL_LOCK.
	legacy := fmt.Errorf("%w: bad json", ErrInvalidLocalLock)
	if !IsDiagnostic(legacy, CodeInvalidLocalLock) || IsDiagnostic(legacy, CodeInvalidAgents) {
		t.Error("legacy invalid-local-lock matching")
	}
	lf := ErrInvalidLocalLockFile("skills-lock.json", "bad json")
	if !errors.Is(lf, ErrInvalidLocalLock) || lf.Error() != "Invalid skills-lock.json: bad json. Fix or delete the file, then retry." {
		t.Errorf("ErrInvalidLocalLockFile = %q", lf.Error())
	}
	// Sentinels match by code.
	if !errors.Is(ErrWellKnownDigest("s", "a", "b"), ErrWellKnownDigestMismatch) ||
		!errors.Is(ErrWellKnownScope("/s", "https://h"), ErrWellKnownScopeNotFound) ||
		errors.Is(ErrWellKnownIndex(), ErrWellKnownDigestMismatch) {
		t.Error("sentinel errors.Is")
	}
	cause := errors.New("boom")
	sf := ErrSourceFetchFailed("owner/repo", cause)
	if !errors.Is(sf, cause) || sf.Error() != "Could not fetch owner/repo: boom" {
		t.Errorf("ErrSourceFetchFailed = %q", sf.Error())
	}
}

func TestDiagnosticConstructorMessages(t *testing.T) {
	cases := map[string]error{
		"--skill requires a skill name":                                        ErrOptionRequiresValue("--skill", "a skill name"),
		"Unknown option: --bogus":                                              ErrUnknownOption("--bogus"),
		"--metadata must be valid JSON":                                        ErrInvalidOptionValue("--metadata", "valid JSON"),
		"Only one --skill value can be provided":                               ErrDuplicateOption("--skill"),
		"Missing required argument: source":                                    ErrMissingArgument("source"),
		"Expected one source, received 2: a, b":                                ErrMultipleSources([]string{"a", "b"}),
		"Unsupported Git transport: ext":                                       ErrUnsupportedGitTransport("ext"),
		"Local path does not exist: /x":                                        ErrLocalPathNotFound("/x"),
		"Skill selector \"x\" matched multiple skills.":                        ErrAmbiguousSkillSelector("x"),
		`package.json: "skills" must be an array`:                              ErrPackageSkillsNotArray(),
		"Digest mismatch for skill 's': expected sha256:a, received sha256:b.": ErrWellKnownDigest("s", "sha256:a", "sha256:b"),
		"Unsafe subpath: \"../x\" contains path traversal segments. Subpaths must not contain \"..\" components.": ErrUnsafeSubpath("../x"),
		"Conflicting skill selectors: source selects \"a\" but --skill selects \"b\". Provide one selector.":      ErrConflictingSkillSelectors("a", "b"),
	}
	for want, err := range cases {
		if err.Error() != want {
			t.Errorf("got %q want %q", err.Error(), want)
		}
	}
	m := ErrMultipleSkillsInSource("skl", "o/r", []string{"a", "b"})
	want := "This source contains multiple skills. Specify exactly one skill:\n  - a\n  - b\nExamples:\n  skl use o/r@a\n  skl use o/r --skill a"
	if m.Error() != want {
		t.Errorf("multiple skills = %q", m.Error())
	}
}

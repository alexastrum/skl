package skl

import "testing"

func TestColorEnabledAndClearLine(t *testing.T) {
	envMap := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	if !ColorEnabledWith(envMap(nil), nil, true) {
		t.Error("expected color enabled on TTY by default")
	}
	if ColorEnabledWith(envMap(nil), nil, false) {
		t.Error("expected color disabled on non-TTY without CI or FORCE_COLOR")
	}
	if !ColorEnabledWith(envMap(map[string]string{"CI": "1"}), nil, false) {
		t.Error("expected color enabled in CI on non-TTY")
	}
	if !ColorEnabledWith(envMap(map[string]string{"FORCE_COLOR": "1"}), nil, false) {
		t.Error("expected color enabled with FORCE_COLOR=1")
	}
	if !ColorEnabledWith(envMap(nil), []string{"skl", "--color"}, false) {
		t.Error("expected color enabled with --color")
	}
	if ColorEnabledWith(envMap(map[string]string{"NO_COLOR": "1", "FORCE_COLOR": "1", "CI": "1"}), []string{"skl", "--color"}, true) {
		t.Error("NO_COLOR must win over TTY, CI, FORCE_COLOR, and --color")
	}
	if ColorEnabledWith(envMap(map[string]string{"FORCE_COLOR": "1"}), []string{"skl", "--no-color"}, true) {
		t.Error("--no-color must disable color")
	}
	if ColorEnabledWith(envMap(map[string]string{"TERM": "dumb"}), nil, true) {
		t.Error("TERM=dumb must disable color on TTY")
	}
	if got := ClearLineWith(true); got != "\r\x1b[K" {
		t.Errorf("ClearLineWith(true) = %q", got)
	}
	if got := ClearLineWith(false); got != "" {
		t.Errorf("ClearLineWith(false) = %q", got)
	}
}

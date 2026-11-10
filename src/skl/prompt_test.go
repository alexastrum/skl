package skl

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// chunkReader hands out one chunk per Read, like a terminal delivering one key
// press at a time, then io.EOF.
type chunkReader struct{ chunks []string }

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	if c.chunks[0] == "" {
		c.chunks = c.chunks[1:]
	}
	return n, nil
}

// testTerm returns an interactive, uncolored, unicode 80x0 terminal fed with
// one chunk per key press.
func testTerm(keys ...string) (*promptTerm, *bytes.Buffer) {
	var out bytes.Buffer
	return &promptTerm{in: &chunkReader{chunks: keys}, out: &out, interactive: true, inFd: -1, outFd: -1,
		unicode: true, cols: 80}, &out
}

var redrawRe = regexp.MustCompile(`\r\x1b\[\d+A\x1b\[J`)

// frames splits prompt output into the frames it drew, with CR LF as LF and
// cursor show/hide removed.
func frames(out string) []string {
	out = strings.NewReplacer(ansiHideCursor, "", ansiShowCursor, "").Replace(out)
	parts := redrawRe.Split(out, -1)
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], "\r\n", "\n")
	}
	return parts
}

func lastFrame(out string) string {
	f := frames(out)
	return f[len(f)-1]
}

const (
	keyUpSeq    = "\x1b[A"
	keyDownSeq  = "\x1b[B"
	keyRightSeq = "\x1b[C"
	keyLeftSeq  = "\x1b[D"
)

// ---------------------------------------------------------------------------
// Key parsing
// ---------------------------------------------------------------------------

func TestParsePromptKeys(t *testing.T) {
	k := func(kind keyKind) promptKey { return promptKey{kind: kind} }
	r := func(c rune) promptKey { return promptKey{kind: keyRune, r: c} }
	cases := []struct {
		in   string
		want []promptKey
	}{
		{"\x1b[A\x1b[B\x1b[C\x1b[D", []promptKey{k(keyUp), k(keyDown), k(keyRight), k(keyLeft)}},
		// SS3 arrows (application cursor mode).
		{"\x1bOA\x1bOB\x1bOC\x1bOD", []promptKey{k(keyUp), k(keyDown), k(keyRight), k(keyLeft)}},
		{"\x1b[H\x1b[F\x1bOH\x1bOF", []promptKey{k(keyHome), k(keyEnd), k(keyHome), k(keyEnd)}},
		{"\x1b[1~\x1b[7~\x1b[4~\x1b[8~\x1b[3~", []promptKey{k(keyHome), k(keyHome), k(keyEnd), k(keyEnd), k(keyDelete)}},
		// Modified arrows still move.
		{"\x1b[1;5A\x1b[1;2D", []promptKey{k(keyUp), k(keyLeft)}},
		// Windows VT input sends CR for Enter and DEL or BS for Backspace.
		{"\r\n\r\n\r\x7f\x08", []promptKey{k(keyEnter), k(keyEnter), k(keyEnter), k(keyBackspace), k(keyBackspace)}},
		{"\x03\x01\x15\t ", []promptKey{k(keyCtrlC), k(keyCtrlA), k(keyCtrlU), k(keyTab), k(keySpace)}},
		{"aé日", []promptKey{r('a'), r('é'), r('日')}},
		{"\x1b", []promptKey{k(keyEscape)}},
		{"\x1b\x1b[A", []promptKey{k(keyEscape), k(keyUp)}},
		// Alt+key, bracketed paste markers and other controls are ignored.
		{"\x1bx\x1b[200~\x02z", []promptKey{r('z')}},
		// An unterminated CSI is Escape followed by its text.
		{"\x1b[", []promptKey{k(keyEscape), r('[')}},
	}
	for _, c := range cases {
		got, rest := parsePromptKeys([]byte(c.in), true)
		if len(rest) != 0 || !equalKeys(got, c.want) {
			t.Errorf("parsePromptKeys(%q) = %v rest %q, want %v", c.in, got, rest, c.want)
		}
	}
	// Without final, incomplete input waits for more bytes.
	for _, in := range []string{"\x1b", "\x1b[", "\x1b[1;", "\x1bO", "\xe6\x97"} {
		got, rest := parsePromptKeys([]byte("a"+in), false)
		if !equalKeys(got, []promptKey{r('a')}) || string(rest) != in {
			t.Errorf("parsePromptKeys(%q, false) = %v rest %q", "a"+in, got, rest)
		}
	}
}

func equalKeys(a, b []promptKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKeyReader(t *testing.T) {
	// Keys split across reads reassemble; EOF repeats.
	kr := newKeyReader(&chunkReader{chunks: []string{"\x1b[", "A", "\xe6", "\x97\xa5", "x"}}, -1)
	// Without a terminal to poll, a lone trailing ESC counts as Escape at once.
	var got []promptKey
	for i := 0; i < 6; i++ {
		got = append(got, kr.next())
	}
	want := []promptKey{{kind: keyEscape}, {kind: keyRune, r: '['}, {kind: keyRune, r: 'A'}, {kind: keyRune, r: '日'}, {kind: keyRune, r: 'x'}, {kind: keyEOF}}
	if !equalKeys(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if k := kr.next(); k.kind != keyEOF {
		t.Errorf("after EOF got %v", k)
	}
	// Keys read ahead carry over between prompts on one terminal.
	term, _ := testTerm("y\r")
	if ok, err := term.Confirm(ConfirmOptions{Message: "one", DefaultNo: true}); err != nil || !ok {
		t.Fatalf("first prompt: %v %v", ok, err)
	}
	if ok, err := term.Confirm(ConfirmOptions{Message: "two", DefaultNo: true}); err != nil || ok {
		t.Fatalf("second prompt must see the carried-over Enter: %v %v", ok, err)
	}
}

// ---------------------------------------------------------------------------
// Confirm
// ---------------------------------------------------------------------------

func TestConfirm(t *testing.T) {
	cases := []struct {
		name string
		opts ConfirmOptions
		keys []string
		want bool
		err  error
	}{
		{"enter takes the default yes", ConfirmOptions{}, []string{"\r"}, true, nil},
		{"enter takes default no", ConfirmOptions{DefaultNo: true}, []string{"\r"}, false, nil},
		{"y answers at once", ConfirmOptions{DefaultNo: true}, []string{"y"}, true, nil},
		{"N answers at once", ConfirmOptions{}, []string{"N"}, false, nil},
		{"right toggles", ConfirmOptions{}, []string{keyRightSeq, "\r"}, false, nil},
		{"ss3 left toggles twice", ConfirmOptions{}, []string{"\x1bOD", "\x1bOD", "\r"}, true, nil},
		{"vim keys toggle", ConfirmOptions{}, []string{"l", "j", "k", "\r"}, false, nil},
		{"ctrl+c cancels", ConfirmOptions{}, []string{"\x03"}, false, ErrPromptCancel},
		{"esc cancels", ConfirmOptions{}, []string{"\x1b"}, false, ErrPromptCancel},
		{"eof cancels", ConfirmOptions{}, nil, false, ErrPromptCancel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, _ := testTerm(c.keys...)
			c.opts.Message = "Proceed?"
			got, err := term.Confirm(c.opts)
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("got %v, %v; want %v, %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestConfirmRendering(t *testing.T) {
	term, out := testTerm(keyRightSeq, "\r")
	if _, err := term.Confirm(ConfirmOptions{Message: "Proceed with installation?"}); err != nil {
		t.Fatal(err)
	}
	f := frames(out.String())
	if want := "│\n◆  Proceed with installation?\n│  ● Yes / ○ No\n└\n"; f[0] != want {
		t.Errorf("active frame:\n%q\nwant\n%q", f[0], want)
	}
	if want := "│\n◆  Proceed with installation?\n│  ○ Yes / ● No\n└\n"; f[1] != want {
		t.Errorf("toggled frame:\n%q\nwant\n%q", f[1], want)
	}
	if want := "│\n◇  Proceed with installation?\n│  No\n"; f[2] != want {
		t.Errorf("submit frame:\n%q\nwant\n%q", f[2], want)
	}
	if s := out.String(); !strings.HasPrefix(s, ansiHideCursor) || !strings.HasSuffix(s, ansiShowCursor) {
		t.Errorf("cursor must be hidden during the prompt and shown after: %q", s)
	}

	term, out = testTerm("\x1b")
	_, _ = term.Confirm(ConfirmOptions{Message: "Proceed?"})
	if want := "│\n■  Proceed?\n│  Yes\n│\n"; lastFrame(out.String()) != want {
		t.Errorf("cancel frame %q, want %q", lastFrame(out.String()), want)
	}
}

func TestPromptRequiresTTY(t *testing.T) {
	term, out := testTerm("\r")
	term.interactive = false
	_, err := term.Confirm(ConfirmOptions{Message: "x"})
	if !IsDiagnostic(err, CodePromptRequiresTTY) {
		t.Errorf("Confirm err = %v", err)
	}
	_, err = term.Select(SelectOptions{Message: "x", Options: []PromptOption{{Value: "a"}}})
	if !IsDiagnostic(err, CodePromptRequiresTTY) {
		t.Errorf("Select err = %v", err)
	}
	_, err = term.MultiSelect(MultiSelectOptions{Message: "x"})
	if !IsDiagnostic(err, CodePromptRequiresTTY) {
		t.Errorf("MultiSelect err = %v", err)
	}
	_, err = term.SearchMultiSelect(SearchMultiSelectOptions{Message: "x"})
	if !IsDiagnostic(err, CodePromptRequiresTTY) {
		t.Errorf("SearchMultiSelect err = %v", err)
	}
	_, err = term.Text(TextOptions{Message: "x"})
	if !IsDiagnostic(err, CodePromptRequiresTTY) {
		t.Errorf("Text err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing may be drawn without a TTY: %q", out.String())
	}
}

func TestBeginRestoresOnPanic(t *testing.T) {
	term, out := testTerm()
	func() {
		defer func() { _ = recover() }()
		end, err := term.begin()
		if err != nil {
			t.Fatal(err)
		}
		defer end()
		panic("boom")
	}()
	if !strings.HasSuffix(out.String(), ansiShowCursor) {
		t.Errorf("cursor not restored after panic: %q", out.String())
	}
}

// ---------------------------------------------------------------------------
// Select
// ---------------------------------------------------------------------------

var scopeOptions = []PromptOption{
	{Value: "project", Label: "Project", Hint: "Install in current directory (committed with your project)"},
	{Value: "global", Label: "Global", Hint: "Install in home directory (available across all projects)"},
}

func TestSelect(t *testing.T) {
	opts3 := []PromptOption{{Value: "a"}, {Value: "b", Disabled: true}, {Value: "c"}}
	cases := []struct {
		name    string
		options []PromptOption
		initial string
		keys    []string
		want    string
		err     error
	}{
		{"enter picks the first", scopeOptions, "", []string{"\r"}, "project", nil},
		{"down", scopeOptions, "", []string{keyDownSeq, "\r"}, "global", nil},
		{"up wraps to the last", scopeOptions, "", []string{keyUpSeq, "\r"}, "global", nil},
		{"down wraps to the first", scopeOptions, "", []string{keyDownSeq, keyDownSeq, "\r"}, "project", nil},
		{"right and left move", scopeOptions, "", []string{keyRightSeq, keyRightSeq, keyLeftSeq, "\r"}, "global", nil},
		{"j and k move", scopeOptions, "", []string{"j", "k", "j", "\r"}, "global", nil},
		{"initial value", scopeOptions, "global", []string{"\r"}, "global", nil},
		{"disabled is skipped", opts3, "", []string{keyDownSeq, "\r"}, "c", nil},
		{"disabled initial moves on", opts3, "b", []string{"\r"}, "c", nil},
		{"esc cancels", scopeOptions, "", []string{keyDownSeq, "\x1b"}, "", ErrPromptCancel},
		{"ctrl+c cancels", scopeOptions, "", []string{"\x03"}, "", ErrPromptCancel},
		{"eof cancels", scopeOptions, "", nil, "", ErrPromptCancel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, _ := testTerm(c.keys...)
			got, err := term.Select(SelectOptions{Message: "Installation scope", Options: c.options, Initial: c.initial})
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("got %q, %v; want %q, %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestSelectRendering(t *testing.T) {
	term, out := testTerm(keyDownSeq, "\r")
	if _, err := term.Select(SelectOptions{Message: "Installation scope", Options: scopeOptions}); err != nil {
		t.Fatal(err)
	}
	f := frames(out.String())
	want := "│\n◆  Installation scope\n" +
		"│  ● Project (Install in current directory (committed with your project))\n" +
		"│  ○ Global\n" +
		"│  ↑/↓ to navigate • Enter: confirm\n└\n"
	if f[0] != want {
		t.Errorf("active frame:\n%s\nwant\n%s", f[0], want)
	}
	if want := "│\n◇  Installation scope\n│  Global\n"; f[2] != want {
		t.Errorf("submit frame %q, want %q", f[2], want)
	}

	// ASCII fallback, as clack does on terminals without unicode.
	term, out = testTerm("\x1b")
	term.unicode = false
	_, _ = term.Select(SelectOptions{Message: "Installation scope", Options: scopeOptions})
	f = frames(out.String())
	want = "|\n*  Installation scope\n" +
		"|  > Project (Install in current directory (committed with your project))\n" +
		"|    Global\n" +
		"|  up/down to navigate - Enter: confirm\n-\n"
	if f[0] != want {
		t.Errorf("ascii frame:\n%s\nwant\n%s", f[0], want)
	}
	if want := "|\nx  Installation scope\n|  Project\n|\n"; f[1] != want {
		t.Errorf("ascii cancel frame %q, want %q", f[1], want)
	}
}

func TestSelectColors(t *testing.T) {
	term, out := testTerm("\r")
	term.color = true
	_, _ = term.Select(SelectOptions{Message: "m", Options: scopeOptions})
	first := frames(out.String())[0]
	for _, want := range []string{"\x1b[36m◆\x1b[39m", "\x1b[32m●\x1b[39m", "\x1b[2m○\x1b[22m", "\x1b[90m│\x1b[39m"} {
		if !strings.Contains(first, want) {
			t.Errorf("frame lacks %q: %q", want, first)
		}
	}
}

func TestSelectScrollsLongLists(t *testing.T) {
	var options []PromptOption
	for _, v := range strings.Split("a b c d e f g h i j", " ") {
		options = append(options, PromptOption{Value: v})
	}
	keys := []string{}
	for i := 0; i < 7; i++ {
		keys = append(keys, keyDownSeq)
	}
	term, out := testTerm(append(keys, "\r")...)
	got, err := term.Select(SelectOptions{Message: "m", Options: options, MaxItems: 5})
	if err != nil || got != "h" {
		t.Fatalf("got %q %v", got, err)
	}
	f := frames(out.String())
	active := f[len(f)-2]
	if !strings.Contains(active, "│  ...\n│  ○ g\n│  ● h\n│  ○ i\n│  ○ j\n│  ↑/↓") {
		t.Errorf("windowed frame:\n%s", active)
	}
}

// ---------------------------------------------------------------------------
// MultiSelect
// ---------------------------------------------------------------------------

func TestMultiSelect(t *testing.T) {
	abc := []PromptOption{{Value: "a"}, {Value: "b"}, {Value: "c"}}
	cases := []struct {
		name     string
		opts     MultiSelectOptions
		keys     []string
		want     []string
		err      error
		contains string
	}{
		{"initial values", MultiSelectOptions{Options: abc, Initial: []string{"b"}}, []string{"\r"}, []string{"b"}, nil, ""},
		{"space toggles, result in option order", MultiSelectOptions{Options: abc}, []string{keyDownSeq, keyDownSeq, " ", keyUpSeq, keyUpSeq, " ", "\r"}, []string{"a", "c"}, nil, ""},
		{"space untoggles", MultiSelectOptions{Options: abc, Initial: []string{"a"}}, []string{" ", keyDownSeq, " ", "\r"}, []string{"b"}, nil, ""},
		{"a toggles all", MultiSelectOptions{Options: abc}, []string{"a", "\r"}, []string{"a", "b", "c"}, nil, ""},
		{"a again clears", MultiSelectOptions{Options: abc}, []string{"a", "a", " ", "\r"}, []string{"a"}, nil, ""},
		{"ctrl+a toggles all", MultiSelectOptions{Options: abc}, []string{"\x01", "\r"}, []string{"a", "b", "c"}, nil, ""},
		{"i inverts", MultiSelectOptions{Options: abc, Initial: []string{"a"}}, []string{"i", "\r"}, []string{"b", "c"}, nil, ""},
		{"required blocks empty submit", MultiSelectOptions{Options: abc}, []string{"\r", " ", "\r"}, []string{"a"}, nil, "Please select at least one option."},
		{"optional allows empty", MultiSelectOptions{Options: abc, Optional: true}, []string{"\r"}, []string{}, nil, ""},
		{"disabled is skipped and never toggled", MultiSelectOptions{Options: []PromptOption{{Value: "a"}, {Value: "b", Disabled: true}, {Value: "c"}}}, []string{"a", keyDownSeq, " ", "\r"}, []string{"a"}, nil, ""},
		{"cursorAt", MultiSelectOptions{Options: abc, CursorAt: "c"}, []string{" ", "\r"}, []string{"c"}, nil, ""},
		{"esc cancels", MultiSelectOptions{Options: abc}, []string{" ", "\x1b"}, nil, ErrPromptCancel, ""},
		{"ctrl+c cancels", MultiSelectOptions{Options: abc}, []string{"\x03"}, nil, ErrPromptCancel, ""},
		{"eof cancels", MultiSelectOptions{Options: abc}, nil, nil, ErrPromptCancel, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, out := testTerm(c.keys...)
			c.opts.Message = "Pick"
			got, err := term.MultiSelect(c.opts)
			if strings.Join(got, ",") != strings.Join(c.want, ",") || (got == nil) != (c.want == nil) || !errors.Is(err, c.err) {
				t.Errorf("got %q, %v; want %q, %v", got, err, c.want, c.err)
			}
			if c.contains != "" && !strings.Contains(out.String(), c.contains) {
				t.Errorf("output lacks %q", c.contains)
			}
		})
	}
}

func TestMultiSelectRendering(t *testing.T) {
	options := []PromptOption{{Value: "", Label: "Root agent", Hint: "agent/skills"}, {Value: "research", Label: "research", Hint: "agent/subagents/research/skills"}}
	term, out := testTerm(keyDownSeq, "\r")
	got, err := term.MultiSelect(MultiSelectOptions{Message: "Where should Eve skills be installed?", Options: options, Initial: []string{""}})
	if err != nil || len(got) != 1 || got[0] != "" {
		t.Fatalf("got %q %v", got, err)
	}
	f := frames(out.String())
	want := "│\n◆  Where should Eve skills be installed?\n" +
		"│  ◼ Root agent (agent/skills)\n" +
		"│  ◻ research\n" +
		"│  ↑/↓ to navigate • Space: select • Enter: confirm\n└\n"
	if f[0] != want {
		t.Errorf("active frame:\n%s\nwant\n%s", f[0], want)
	}
	if want := "│  ◼ Root agent (agent/skills)\n│  ◻ research (agent/subagents/research/skills)\n"; !strings.Contains(f[1], want) {
		t.Errorf("moved frame:\n%s", f[1])
	}
	if want := "│\n◇  Where should Eve skills be installed?\n│  Root agent\n"; f[2] != want {
		t.Errorf("submit frame %q, want %q", f[2], want)
	}

	term, out = testTerm("\r", "\x03")
	_, _ = term.MultiSelect(MultiSelectOptions{Message: "Pick", Options: options})
	f = frames(out.String())
	if !strings.Contains(f[1], "▲  Pick\n") || !strings.Contains(f[1], "└  Please select at least one option.\n   Press  space  to select,  enter  to submit\n") {
		t.Errorf("error frame:\n%s", f[1])
	}
	if want := "│\n■  Pick\n│\n"; f[2] != want {
		t.Errorf("empty cancel frame %q, want %q", f[2], want)
	}
}

// ---------------------------------------------------------------------------
// Text
// ---------------------------------------------------------------------------

func TestText(t *testing.T) {
	cases := []struct {
		name string
		opts TextOptions
		keys []string
		want string
		err  error
	}{
		{"typing", TextOptions{}, []string{"h", "i", " ", "x", "\r"}, "hi x", nil},
		{"left and backspace edit mid-line", TextOptions{}, []string{"a", "b", "c", keyLeftSeq, "\x7f", "\r"}, "ac", nil},
		{"home, end and delete", TextOptions{}, []string{"a", "b", "\x1b[H", "\x1b[3~", "\x1b[F", "c", "\r"}, "bc", nil},
		{"ctrl+u clears before the cursor", TextOptions{InitialValue: "old"}, []string{"\x15", "n", "\r"}, "n", nil},
		{"right stops at the end", TextOptions{InitialValue: "ab"}, []string{keyRightSeq, "c", "\r"}, "abc", nil},
		{"default on empty", TextOptions{DefaultValue: "dflt"}, []string{"\r"}, "dflt", nil},
		{"hjkl are text", TextOptions{}, []string{"h", "j", "k", "l", "\r"}, "hjkl", nil},
		{"esc cancels", TextOptions{}, []string{"a", "\x1b"}, "", ErrPromptCancel},
		{"eof cancels", TextOptions{}, []string{"a"}, "", ErrPromptCancel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, _ := testTerm(c.keys...)
			c.opts.Message = "Name?"
			got, err := term.Text(c.opts)
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("got %q, %v; want %q, %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestTextValidateAndRendering(t *testing.T) {
	term, out := testTerm("\r", "o", "k", keyLeftSeq, "\r")
	validate := func(s string) string {
		if s == "" {
			return "Required"
		}
		return ""
	}
	got, err := term.Text(TextOptions{Message: "Name?", Placeholder: "my-skill", Validate: validate})
	if err != nil || got != "ok" {
		t.Fatalf("got %q %v", got, err)
	}
	f := frames(out.String())
	if want := "│\n◆  Name?\n│  my-skill\n└\n"; f[0] != want {
		t.Errorf("placeholder frame %q, want %q", f[0], want)
	}
	if want := "│\n▲  Name?\n│  my-skill\n└  Required\n"; f[1] != want {
		t.Errorf("error frame %q, want %q", f[1], want)
	}
	if want := "│\n◆  Name?\n│  ok█\n└\n"; f[3] != want {
		t.Errorf("typed frame %q, want %q", f[3], want)
	}
	if want := "│\n◇  Name?\n│  ok\n"; f[len(f)-1] != want {
		t.Errorf("submit frame %q, want %q", f[len(f)-1], want)
	}
}

// ---------------------------------------------------------------------------
// SearchMultiSelect
// ---------------------------------------------------------------------------

func skillItems(names ...string) []SearchItem {
	items := make([]SearchItem, len(names))
	for i, n := range names {
		items[i] = SearchItem{Label: n, Detail: n + " does things"}
	}
	return items
}

func TestSearchMultiSelect(t *testing.T) {
	items := skillItems("alpha", "beta", "gamma")
	cases := []struct {
		name string
		opts SearchMultiSelectOptions
		keys []string
		want []int
		err  error
	}{
		{"enter keeps initial", SearchMultiSelectOptions{Initial: []int{2}}, []string{"\r"}, []int{2}, nil},
		{"space toggles under the cursor", SearchMultiSelectOptions{}, []string{keyDownSeq, " ", "\r"}, []int{1}, nil},
		{"result keeps selection order", SearchMultiSelectOptions{}, []string{keyDownSeq, keyDownSeq, " ", keyUpSeq, keyUpSeq, " ", "\r"}, []int{2, 0}, nil},
		{"typing filters", SearchMultiSelectOptions{}, []string{"g", "a", " ", "\r"}, []int{2}, nil},
		{"filter is case-insensitive on label", SearchMultiSelectOptions{}, []string{"B", "E", " ", "\r"}, []int{1}, nil},
		{"backspace widens the filter", SearchMultiSelectOptions{}, []string{"g", "x", "\x7f", " ", "\r"}, []int{2}, nil},
		{"up stops at the top", SearchMultiSelectOptions{}, []string{keyUpSeq, keyUpSeq, " ", "\r"}, []int{0}, nil},
		{"down stops at the bottom", SearchMultiSelectOptions{}, []string{keyDownSeq, keyDownSeq, keyDownSeq, keyDownSeq, " ", "\r"}, []int{2}, nil},
		{"ctrl+a selects all", SearchMultiSelectOptions{}, []string{"\x01", "\r"}, []int{0, 1, 2}, nil},
		{"ctrl+a clears when all selected", SearchMultiSelectOptions{Initial: []int{0, 1, 2}}, []string{"\x01", "\r"}, []int{}, nil},
		// #1872: Ctrl+A acts on the filtered rows only and leaves the rest alone.
		{"ctrl+a selects only filtered", SearchMultiSelectOptions{Initial: []int{0}}, []string{"m", "\x01", "\r"}, []int{0, 2}, nil},
		{"select all row toggles everything", SearchMultiSelectOptions{SelectAll: true}, []string{" ", "\r"}, []int{0, 1, 2}, nil},
		{"select all row clears", SearchMultiSelectOptions{SelectAll: true, Initial: []int{0, 1, 2}}, []string{" ", keyDownSeq, " ", "\r"}, []int{0}, nil},
		{"required blocks enter", SearchMultiSelectOptions{Required: true}, []string{"\r", " ", "\r"}, []int{0}, nil},
		{"locked items satisfy required", SearchMultiSelectOptions{Required: true, Locked: &LockedSection{Title: "U", Items: []SearchItem{{Label: "L"}}}}, []string{"\r"}, []int{}, nil},
		{"no search ignores letters", SearchMultiSelectOptions{NoSearch: true}, []string{"g", " ", "\r"}, []int{0}, nil},
		{"esc cancels", SearchMultiSelectOptions{}, []string{" ", "\x1b"}, nil, ErrPromptCancel},
		{"ctrl+c cancels", SearchMultiSelectOptions{}, []string{"\x03"}, nil, ErrPromptCancel},
		{"eof cancels", SearchMultiSelectOptions{}, nil, nil, ErrPromptCancel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, _ := testTerm(c.keys...)
			c.opts.Message = "Select skills to install"
			c.opts.Items = items
			got, err := term.SearchMultiSelect(c.opts)
			if !equalInts(got, c.want) || (got == nil) != (c.want == nil) || !errors.Is(err, c.err) {
				t.Errorf("got %v, %v; want %v, %v", got, err, c.want, c.err)
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSearchMultiSelectValueFilter(t *testing.T) {
	items := []SearchItem{{Value: "claude-code", Label: "Claude Code"}, {Value: "codex", Label: "Codex"}}
	term, _ := testTerm("c", "l", "a", "u", "d", "e", "-", " ", "\r")
	got, err := term.SearchMultiSelect(SearchMultiSelectOptions{Message: "m", Items: items})
	if err != nil || !equalInts(got, []int{0}) {
		t.Errorf("value filter: got %v %v", got, err)
	}
}

func TestSearchMultiSelectRendering(t *testing.T) {
	items := []SearchItem{{Value: "cursor", Label: "Cursor", Hint: ".cursor/skills"}, {Value: "windsurf", Label: "Windsurf", Hint: ".windsurf/skills"}}
	locked := &LockedSection{Title: "Universal (.agents/skills)", Items: []SearchItem{{Value: "codex", Label: "Codex"}}, HiddenCount: 2}
	term, out := testTerm("w", " ", "\r")
	got, err := term.SearchMultiSelect(SearchMultiSelectOptions{Message: "Which agents do you want to install to?", Items: items, Locked: locked, Initial: []int{0}})
	if err != nil || !equalInts(got, []int{0, 1}) {
		t.Fatalf("got %v %v", got, err)
	}
	f := frames(out.String())
	want := "◆  Which agents do you want to install to?\n" +
		"│\n" +
		"│  ── Universal (.agents/skills) ── always included ────────────\n" +
		"│    • Codex\n" +
		"│    …and 2 more\n" +
		"│\n" +
		"│  ── Additional agents ─────────────────────────────\n" +
		"│  Search:  \n" +
		"│  ↑↓ move, space select, ctrl+a select all, enter confirm\n" +
		"│\n" +
		"│ ❯ ● Cursor (.cursor/skills)\n" +
		"│   ○ Windsurf (.windsurf/skills)\n" +
		"│\n" +
		"│  Selected: Codex, Cursor\n" +
		"└\n"
	if f[0] != want {
		t.Errorf("initial frame:\n%s\nwant\n%s", f[0], want)
	}
	if !strings.Contains(f[1], "│  Search: w \n") || strings.Contains(f[1], "Cursor (") || !strings.Contains(f[1], "│ ❯ ○ Windsurf") {
		t.Errorf("filtered frame:\n%s", f[1])
	}
	if want := "◇  Which agents do you want to install to?\n│  Codex, Cursor, Windsurf\n"; f[len(f)-1] != want {
		t.Errorf("submit frame %q, want %q", f[len(f)-1], want)
	}

	term, out = testTerm("z", "\x1b")
	_, _ = term.SearchMultiSelect(SearchMultiSelectOptions{Message: "m", Items: items})
	f = frames(out.String())
	if !strings.Contains(f[1], "│  No matches found\n") || !strings.Contains(f[1], "Selected: (none)") {
		t.Errorf("no-match frame:\n%s", f[1])
	}
	if want := "■  m\n│  Cancelled\n"; f[2] != want {
		t.Errorf("cancel frame %q, want %q", f[2], want)
	}
}

func TestSearchMultiSelectSelectAllAndDetail(t *testing.T) {
	term, out := testTerm(keyDownSeq, " ", "\r")
	_, err := term.SearchMultiSelect(SearchMultiSelectOptions{Message: "Select skills to install", Items: skillItems("alpha", "beta"),
		Required: true, SelectAll: true, ShowDetail: true, HideSelectedSummary: true, MaxVisible: 20})
	if err != nil {
		t.Fatal(err)
	}
	f := frames(out.String())
	if want := "│ ❯ ○ Select All (0/2)\n│   ────────────────────────────────────\n│   ○ alpha\n│   ○ beta\n│\n│  Description\n│  Select or clear all 2 skills.\n│  \n└\n"; !strings.HasSuffix(f[0], want) {
		t.Errorf("select-all frame:\n%s", f[0])
	}
	if !strings.Contains(f[2], "│   ◐ Select All (1/2)") || !strings.Contains(f[2], "│ ❯ ● alpha") || !strings.Contains(f[2], "│  alpha does things\n") {
		t.Errorf("partial frame:\n%s", f[2])
	}
	if strings.Contains(f[2], "Selected:") {
		t.Errorf("summary must be hidden:\n%s", f[2])
	}
}

func TestSearchMultiSelectGroups(t *testing.T) {
	items := []SearchItem{
		{Label: "a1", Group: "Alpha", Detail: "first"},
		{Label: "a2", Group: "Alpha"},
		{Label: "lone"},
		{Label: "b1", Group: "Beta"},
	}
	// Space on the Alpha heading selects both members; left on a member collapses
	// its group and moves to the heading; right expands it again.
	term, out := testTerm(" ", keyDownSeq, keyLeftSeq, keyDownSeq, keyDownSeq, " ", keyUpSeq, keyUpSeq, keyRightSeq, "q", "\r")
	got, err := term.SearchMultiSelect(SearchMultiSelectOptions{Message: "Select skills to install", Items: items,
		NoSearch: true, SelectGroups: true, ShowDetail: true, HideSelectedSummary: true})
	if err != nil || !equalInts(got, []int{0, 1, 3}) {
		t.Fatalf("got %v %v", got, err)
	}
	f := frames(out.String())
	if want := "│ ❯ ▾ ○ Alpha\n│   ├─ ○ a1\n│   └─ ○ a2\n│   ○ lone\n│   ▾ ○ Beta\n│   └─ ○ b1\n"; !strings.Contains(f[0], want) {
		t.Errorf("grouped frame:\n%s", f[0])
	}
	if !strings.Contains(f[0], "│  Select all 2 skills in Alpha.\n") || !strings.Contains(f[0], "│  ↑↓ move, ←→ collapse/expand, space select, enter confirm\n") {
		t.Errorf("grouped footer:\n%s", f[0])
	}
	if strings.Contains(f[0], "Search:") {
		t.Errorf("search must be hidden:\n%s", f[0])
	}
	collapsed := f[3]
	if !strings.Contains(collapsed, "│ ❯ ▸ ● Alpha\n│   ○ lone\n") {
		t.Errorf("collapsed frame:\n%s", collapsed)
	}
	last := f[len(f)-2]
	if !strings.Contains(last, "│ ❯ ▾ ● Alpha\n│   ├─ ● a1\n") {
		t.Errorf("expanded frame:\n%s", last)
	}
}

func TestSearchMultiSelectFitsShortTerminals(t *testing.T) {
	var names []string
	for i := 0; i < 30; i++ {
		names = append(names, "skill-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	term, out := testTerm("\x1b")
	term.rows = 10
	_, _ = term.SearchMultiSelect(SearchMultiSelectOptions{Message: "m", Items: skillItems(names...), MaxVisible: 20, ShowDetail: true})
	first := frames(out.String())[0]
	if n := strings.Count(first, "\n"); n > 9 {
		t.Errorf("frame has %d rows on a 10-row terminal:\n%s", n, first)
	}
	if !strings.Contains(first, "↓ ") || strings.Contains(first, "Description") || !strings.Contains(first, "Selected:") {
		t.Errorf("expected a scroll marker and the summary but no detail pane:\n%s", first)
	}
}

// Ported from upstream prompts/search-multiselect.test.ts.
func TestToggleSelectAll(t *testing.T) {
	sel := newOrderedSet(nil)
	toggleSelectAll(sel, []int{0, 1, 2})
	if !equalInts(sel.order, []int{0, 1, 2}) {
		t.Errorf("selects every filtered item: %v", sel.order)
	}
	toggleSelectAll(sel, []int{0, 1, 2})
	if len(sel.order) != 0 {
		t.Errorf("deselects when all are selected: %v", sel.order)
	}
	sel = newOrderedSet([]int{9})
	toggleSelectAll(sel, []int{0, 1})
	if !equalInts(sel.order, []int{9, 0, 1}) {
		t.Errorf("leaves items filtered out alone: %v", sel.order)
	}
}

func TestFormatDetailLines(t *testing.T) {
	got := formatDetailLines("  one two\tthree   four five six ", 9, 2, "…")
	if strings.Join(got, "|") != "one two|three…" {
		t.Errorf("got %q", got)
	}
	if got := formatDetailLines("", 10, 2, "…"); strings.Join(got, "|") != "|" {
		t.Errorf("empty pads: %q", got)
	}
	if got := formatDetailLines("abcdefghij", 4, 2, "..."); strings.Join(got, "|") != "abcd|e..." {
		t.Errorf("hard break: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Width
// ---------------------------------------------------------------------------

func TestTruncation(t *testing.T) {
	if got := truncateANSI("hello world", 8, "…"); got != "hello w…" {
		t.Errorf("plain: %q", got)
	}
	if got := truncateANSI("short", 8, "…"); got != "short" {
		t.Errorf("fits: %q", got)
	}
	if got := truncateANSI("\x1b[2mhello world\x1b[22m", 6, "..."); got != "\x1b[2mhel...\x1b[0m" {
		t.Errorf("styled: %q", got)
	}
	// Wide characters count two cells.
	if got := truncateANSI("日本語テキスト", 7, "…"); got != "日本語…" || visibleWidth(got) != 7 {
		t.Errorf("wide: %q (%d)", got, visibleWidth(got))
	}
	if w := approxStringWidth("a日🦀"); w != 5 {
		t.Errorf("approxStringWidth = %d", w)
	}
}

func TestFrameTruncatesAndRedrawsInPlace(t *testing.T) {
	term, out := testTerm()
	term.cols = 20
	f := &promptFrame{t: term}
	f.draw([]string{"a line that is far too long for twenty columns", "two\nthree"})
	f.draw([]string{"x"})
	s := out.String()
	if !strings.HasPrefix(s, "a line that is far…\r\ntwo\r\nthree\r\n") {
		t.Errorf("first frame %q", s)
	}
	if !strings.HasSuffix(s, "\r\x1b[3A\x1b[Jx\r\n") {
		t.Errorf("redraw must move up over all 3 rows and clear: %q", s)
	}
	for _, line := range strings.Split(strings.TrimSuffix(frames(s)[0], "\n"), "\n") {
		if visibleWidth(line) > 19 {
			t.Errorf("line wider than cols-1: %q", line)
		}
	}
}

func TestSelectTruncatesHints(t *testing.T) {
	term, out := testTerm("\r")
	term.cols = 30
	_, _ = term.Select(SelectOptions{Message: "Installation scope", Options: scopeOptions})
	if !strings.Contains(frames(out.String())[0], "│  ● Project (Install in cur…\n") {
		t.Errorf("hint not truncated:\n%s", frames(out.String())[0])
	}
}

// ---------------------------------------------------------------------------
// Piped input
// ---------------------------------------------------------------------------

func TestConfirmOrLine(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{{"y\n", true}, {"YES\n", true}, {"n\n", false}, {"\n", false}, {"", false}} {
		var out bytes.Buffer
		term := newPromptTerm(strings.NewReader(c.in), &out, false)
		if got := confirmOrLine(term, bufioReader(c.in), "Proceed with sync?"); got != c.want {
			t.Errorf("%q: got %v", c.in, got)
		}
		if out.String() != "Proceed with sync? (y/N) " {
			t.Errorf("%q: printed %q", c.in, out.String())
		}
	}
	term, _ := testTerm("\r")
	if !confirmOrLine(term, bufioReader(""), "Proceed?") {
		t.Error("TTY path must use the Confirm widget (Enter = Yes)")
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitReader feeds each step's keys once the output contains its marker.
type waitReader struct {
	out   *syncBuffer
	steps [][2]string // {wait for, keys}
}

func (w *waitReader) Read(p []byte) (int, error) {
	if len(w.steps) == 0 {
		return 0, io.EOF
	}
	step := w.steps[0]
	w.steps = w.steps[1:]
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(w.out.String(), step[0]) {
		if time.Now().After(deadline) {
			return 0, io.EOF
		}
		time.Sleep(2 * time.Millisecond)
	}
	return copy(p, step[1]), nil
}

func bufioReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

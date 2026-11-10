package skl

// Interactive terminal prompts modeled on @clack/prompts 1.7 (select, multiselect,
// confirm, text) and upstream's prompts/search-multiselect.ts. They run on a raw
// terminal through golang.org/x/term and read keys from an injectable io.Reader,
// so tests drive them with keystroke byte streams.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrPromptCancel is returned when the user cancels a prompt with Ctrl+C or Esc,
// or when input ends while a prompt is waiting.
var ErrPromptCancel = errors.New("prompt cancelled")

// promptTerm is the terminal a command's prompts share. Keys typed ahead of one
// prompt carry over to the next, as they do with a single readline interface.
type promptTerm struct {
	in  io.Reader
	out io.Writer
	// interactive reports whether prompts may run at all (stdin is a TTY, or a
	// test says so). When false every prompt returns SKILLS_PROMPT_REQUIRES_TTY.
	interactive bool
	// inFd is the input terminal put into raw mode, or -1 (tests, pipes).
	inFd int
	// outFd is the output terminal used for size queries, or -1.
	outFd   int
	unicode bool
	color   bool
	// cols and rows override the terminal size when positive (tests).
	cols, rows int

	keysOnce sync.Once
	keyRdr   *keyReader
	writeMu  sync.Mutex
}

// newPromptTerm wraps in/out for prompting. interactive gates whether prompts may
// run; raw mode is only used when in is a real terminal.
func newPromptTerm(in io.Reader, out io.Writer, interactive bool) *promptTerm {
	t := &promptTerm{in: in, out: out, interactive: interactive, inFd: -1, outFd: -1,
		unicode: isUnicodeSupported(), color: promptColorEnabled()}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		t.inFd = int(f.Fd())
	}
	if f, ok := out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		t.outFd = int(f.Fd())
	}
	return t
}

// isUnicodeSupported mirrors the check @clack/prompts 1.7 bundles.
func isUnicodeSupported() bool {
	if runtime.GOOS != "windows" {
		return os.Getenv("TERM") != "linux"
	}
	return os.Getenv("CI") != "" ||
		os.Getenv("WT_SESSION") != "" ||
		os.Getenv("TERMINUS_SUBLIME") != "" ||
		os.Getenv("ConEmuTask") == "{cmd::Cmder}" ||
		os.Getenv("TERM_PROGRAM") == "Terminus-Sublime" ||
		os.Getenv("TERM_PROGRAM") == "vscode" ||
		os.Getenv("TERM") == "xterm-256color" ||
		os.Getenv("TERM") == "alacritty" ||
		os.Getenv("TERMINAL_EMULATOR") == "JetBrains-JediTerm"
}

func promptColorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func (t *promptTerm) keys() *keyReader {
	t.keysOnce.Do(func() { t.keyRdr = newKeyReader(t.in, t.inFd) })
	return t.keyRdr
}

func (t *promptTerm) write(s string) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, _ = io.WriteString(t.out, s)
}

// size returns the terminal width (80 when unknown) and height (0 when unknown).
func (t *promptTerm) size() (cols, rows int) {
	if t.cols > 0 {
		return t.cols, t.rows
	}
	for _, fd := range []int{t.outFd, t.inFd} {
		if fd < 0 {
			continue
		}
		if w, h, err := term.GetSize(fd); err == nil && w > 0 {
			return w, h
		}
	}
	return 80, 0
}

const (
	ansiHideCursor = "\x1b[?25l"
	ansiShowCursor = "\x1b[?25h"
)

// begin starts a prompt: raw mode on a real terminal, VT output on Windows, a
// hidden cursor, and a SIGINT/SIGTERM handler. The returned end func restores
// all of it and is safe to call more than once; callers defer it so panics
// restore the terminal too.
func (t *promptTerm) begin() (end func(), err error) {
	if !t.interactive {
		return nil, ErrPromptRequiresTTY()
	}
	var state *term.State
	if t.inFd >= 0 {
		// Without raw mode keys still arrive after Enter; prompting stays usable.
		state, _ = term.MakeRaw(t.inFd)
	}
	restoreVT := enableVTOutput(t.outFd)
	t.write(ansiHideCursor)
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	var once sync.Once
	end = func() {
		once.Do(func() {
			t.write(ansiShowCursor)
			if state != nil {
				_ = term.Restore(t.inFd, state)
			}
			restoreVT()
			signal.Stop(sigs)
			close(done)
		})
	}
	if state != nil {
		// Raw mode turns Ctrl+C into a key, but a signal sent by another process
		// must still leave the terminal usable.
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		go func() {
			select {
			case <-sigs:
				end()
				os.Exit(130)
			case <-done:
			}
		}()
	}
	return end, nil
}

// ---------------------------------------------------------------------------
// Styling
// ---------------------------------------------------------------------------

type sgr struct{ open, close string }

var (
	sgrBold      = sgr{"\x1b[1m", "\x1b[22m"}
	sgrDim       = sgr{"\x1b[2m", "\x1b[22m"}
	sgrUnderline = sgr{"\x1b[4m", "\x1b[24m"}
	sgrInverse   = sgr{"\x1b[7m", "\x1b[27m"}
	sgrHidden    = sgr{"\x1b[8m", "\x1b[28m"}
	sgrStrike    = sgr{"\x1b[9m", "\x1b[29m"}
	sgrRed       = sgr{"\x1b[31m", "\x1b[39m"}
	sgrGreen     = sgr{"\x1b[32m", "\x1b[39m"}
	sgrYellow    = sgr{"\x1b[33m", "\x1b[39m"}
	sgrCyan      = sgr{"\x1b[36m", "\x1b[39m"}
	sgrGray      = sgr{"\x1b[90m", "\x1b[39m"}
)

// paint applies styles outermost first, like styleText([a, b], s).
func (t *promptTerm) paint(s string, styles ...sgr) string {
	if !t.color || s == "" {
		return s
	}
	for i := len(styles) - 1; i >= 0; i-- {
		s = styles[i].open + s + styles[i].close
	}
	return s
}

// promptSymbols holds clack's glyphs and their ASCII fallbacks.
type promptSymbols struct {
	stepActive, stepCancel, stepError, stepSubmit string
	bar, barEnd, barH                             string
	radioActive, radioInactive                    string
	checkboxActive, checkboxSelected, checkboxOff string
	// search-multiselect glyphs.
	searchOn, searchOff, searchPartial string
	pointer, check, bullet             string
	collapsed, expanded                string
	treeMid, treeEnd                   string
	up, down, ellipsis, arrows, sep    string
	upDown, leftRight, textCursor      string
}

var unicodeSymbols = promptSymbols{
	stepActive: "◆", stepCancel: "■", stepError: "▲", stepSubmit: "◇",
	bar: "│", barEnd: "└", barH: "─",
	radioActive: "●", radioInactive: "○",
	checkboxActive: "◻", checkboxSelected: "◼", checkboxOff: "◻",
	searchOn: "●", searchOff: "○", searchPartial: "◐",
	pointer: "❯", check: "✓", bullet: "•",
	collapsed: "▸", expanded: "▾", treeMid: "├─", treeEnd: "└─",
	up: "↑", down: "↓", ellipsis: "…", arrows: "↑/↓", sep: " • ",
	upDown: "↑↓", leftRight: "←→", textCursor: "█",
}

// asciiSymbols follows clack's fallbacks, kept to plain ASCII throughout.
var asciiSymbols = promptSymbols{
	stepActive: "*", stepCancel: "x", stepError: "x", stepSubmit: "o",
	bar: "|", barEnd: "-", barH: "-",
	radioActive: ">", radioInactive: " ",
	checkboxActive: "[.]", checkboxSelected: "[+]", checkboxOff: "[ ]",
	searchOn: "(*)", searchOff: "( )", searchPartial: "(~)",
	pointer: ">", check: "+", bullet: "*",
	collapsed: "+", expanded: "-", treeMid: "|-", treeEnd: "`-",
	up: "^", down: "v", ellipsis: "...", arrows: "up/down", sep: " - ",
	upDown: "up/down", leftRight: "left/right", textCursor: "_",
}

func (t *promptTerm) sym() *promptSymbols {
	if t.unicode {
		return &unicodeSymbols
	}
	return &asciiSymbols
}

type promptState int

const (
	promptActive promptState = iota
	promptSubmit
	promptCancel
	promptError
)

// stateSymbol is clack's symbol(state).
func (t *promptTerm) stateSymbol(s promptState) string {
	y := t.sym()
	switch s {
	case promptSubmit:
		return t.paint(y.stepSubmit, sgrGreen)
	case promptCancel:
		return t.paint(y.stepCancel, sgrRed)
	case promptError:
		return t.paint(y.stepError, sgrYellow)
	}
	return t.paint(y.stepActive, sgrCyan)
}

// stateBar is clack's symbolBar(state).
func (t *promptTerm) stateBar(s promptState) string {
	y := t.sym()
	switch s {
	case promptSubmit:
		return t.paint(y.bar, sgrGreen)
	case promptCancel:
		return t.paint(y.bar, sgrRed)
	case promptError:
		return t.paint(y.bar, sgrYellow)
	}
	return t.paint(y.bar, sgrCyan)
}

// prefixLines puts first before the first line of msg and rest before the others.
func prefixLines(msg, first, rest string) []string {
	parts := strings.Split(msg, "\n")
	for i := range parts {
		if i == 0 {
			parts[i] = first + parts[i]
		} else {
			parts[i] = rest + parts[i]
		}
	}
	return parts
}

// ---------------------------------------------------------------------------
// Width handling and redraw
// ---------------------------------------------------------------------------

// approxRuneWidth ports upstream approxStringWidth for one code point.
func approxRuneWidth(r rune) int {
	c := int(r)
	switch {
	case c == 0:
		return 0
	case (c >= 0x1100 && c <= 0x115f) || (c >= 0x231a && c <= 0x231b) || (c >= 0x2329 && c <= 0x232a) ||
		(c >= 0x23e9 && c <= 0x23ec) || c == 0x23f0 || c == 0x23f3 || (c >= 0x25fd && c <= 0x25fe) ||
		(c >= 0x2614 && c <= 0x2615) || (c >= 0x2648 && c <= 0x2653) || c == 0x267f || c == 0x2693 ||
		c == 0x26a1 || (c >= 0x26aa && c <= 0x26ab) || (c >= 0x26bd && c <= 0x26be) || (c >= 0x26c4 && c <= 0x26c5) ||
		c == 0x26ce || c == 0x26d4 || c == 0x26ea || (c >= 0x26f2 && c <= 0x26f3) || c == 0x26f5 || c == 0x26fa ||
		c == 0x26fd || c == 0x2705 || (c >= 0x270a && c <= 0x270b) || c == 0x2728 || c == 0x274c || c == 0x274e ||
		(c >= 0x2753 && c <= 0x2755) || c == 0x2757 || (c >= 0x2795 && c <= 0x2797) || c == 0x27b0 || c == 0x27bf ||
		(c >= 0x2b1b && c <= 0x2b1c) || c == 0x2b50 || c == 0x2b55 || (c >= 0x2e80 && c <= 0xa4cf && c != 0x303f) ||
		(c >= 0xa960 && c <= 0xa97c) || (c >= 0xac00 && c <= 0xd7a3) || (c >= 0xf900 && c <= 0xfaff) ||
		(c >= 0xfe10 && c <= 0xfe19) || (c >= 0xfe30 && c <= 0xfe6f) || (c >= 0xff00 && c <= 0xff60) ||
		(c >= 0xffe0 && c <= 0xffe6) || (c >= 0x1f000 && c <= 0x1f9ff):
		return 2
	}
	return 1
}

// approxStringWidth is the display width of plain text (no escape sequences).
func approxStringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += approxRuneWidth(r)
	}
	return w
}

// ansiSeqLen returns the length of the escape sequence at s[0] (an ESC), or 0.
func ansiSeqLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	if s[1] != '[' {
		return 2
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

// visibleWidth is the display width of s, ignoring escape sequences.
func visibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if n := ansiSeqLen(s[i:]); n > 0 {
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w += approxRuneWidth(r)
		i += size
	}
	return w
}

// truncateToWidth cuts plain text to at most width cells (upstream truncateToWidth).
func truncateToWidth(s string, width int) string {
	w := 0
	for i, r := range s {
		rw := approxRuneWidth(r)
		if w+rw > width {
			return s[:i]
		}
		w += rw
	}
	return s
}

// truncateANSI cuts s to width display cells, keeping escape sequences, and ends
// a cut line with ellipsis and a style reset.
func truncateANSI(s string, width int, ellipsis string) string {
	if visibleWidth(s) <= width {
		return s
	}
	keep := width - approxStringWidth(ellipsis)
	if keep < 0 {
		keep, ellipsis = width, ""
	}
	var b strings.Builder
	styled := false
	w := 0
	for i := 0; i < len(s); {
		if n := ansiSeqLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			styled = true
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := approxRuneWidth(r)
		if w+rw > keep {
			break
		}
		b.WriteString(s[i : i+size])
		w += rw
		i += size
	}
	b.WriteString(ellipsis)
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// promptFrame redraws a prompt in place: it moves up over the previous frame,
// clears to the end of the screen and writes the next frame in one write, as
// upstream does to avoid flicker. Lines are truncated to the terminal width so
// every line is exactly one row and the next redraw erases all of it.
type promptFrame struct {
	t    *promptTerm
	rows int
}

func (f *promptFrame) draw(lines []string) {
	cols, _ := f.t.size()
	limit := cols - 1
	if limit < 1 {
		limit = 1
	}
	var b strings.Builder
	if f.rows > 0 {
		b.WriteString("\r\x1b[" + strconv.Itoa(f.rows) + "A\x1b[J")
	}
	n := 0
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			b.WriteString(truncateANSI(part, limit, f.t.sym().ellipsis))
			// Raw mode disables output post-processing, so end lines with CR LF.
			b.WriteString("\r\n")
			n++
		}
	}
	f.t.write(b.String())
	f.rows = n
}

// ---------------------------------------------------------------------------
// Piped (non-TTY) input
// ---------------------------------------------------------------------------

// confirmOrLine asks message with the Confirm widget on a TTY. Without one it keeps
// the piped-input behavior: print "message (y/N) " and accept a "y" or "yes" line.
// Cancelling either way counts as no.
func confirmOrLine(t *promptTerm, in *bufio.Reader, message string) bool {
	if t.interactive {
		ok, err := t.Confirm(ConfirmOptions{Message: message})
		return err == nil && ok
	}
	ans, ok := promptLine(in, t.out, message+" (y/N) ")
	return ok && (strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes"))
}

// promptLine prints prompt and reads one trimmed line; ok is false on EOF/error.
func promptLine(in *bufio.Reader, out io.Writer, prompt string) (string, bool) {
	fmt.Fprint(out, prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

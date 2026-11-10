package skl

import (
	"math"
	"strings"
)

// PromptOption is one choice in Select and MultiSelect.
type PromptOption struct {
	Value    string
	Label    string
	Hint     string
	Disabled bool
}

func (o PromptOption) label() string {
	if o.Label != "" {
		return o.Label
	}
	return o.Value
}

// findEnabled is clack's findCursor: step from i by dir, wrapping around and
// skipping disabled options.
func findEnabled(i, dir int, opts []PromptOption) int {
	any := false
	for _, o := range opts {
		if !o.Disabled {
			any = true
			break
		}
	}
	if !any || len(opts) == 0 {
		return i
	}
	for {
		i += dir
		if i < 0 {
			i = len(opts) - 1
		} else if i >= len(opts) {
			i = 0
		}
		if !opts[i].Disabled {
			return i
		}
	}
}

// limitOptions ports clack's limitOptions without soft wrapping (frames are
// truncated to the terminal width): it windows the option list around the
// cursor and marks hidden rows with "...".
func (t *promptTerm) limitOptions(n, cursor, maxItems, rowPadding int, style func(i int, active bool) string) []string {
	_, rows := t.size()
	avail := math.MaxInt
	if rows > 0 {
		avail = max(rows-rowPadding, 0)
	}
	if maxItems <= 0 {
		maxItems = math.MaxInt
	}
	a := max(min(maxItems, avail), 5)
	start := 0
	if cursor >= a-3 {
		start = max(min(cursor-a+3, n-a), 0)
	}
	top := a < n && start > 0
	bottom := a < n && start+a < n
	end := min(start+a, n)
	from, to := start, end
	if top {
		from++
	}
	if bottom {
		to--
	}
	var lines []string
	if top {
		lines = append(lines, t.paint("...", sgrDim))
	}
	for i := from; i < to; i++ {
		lines = append(lines, style(i, i == cursor))
	}
	if bottom {
		lines = append(lines, t.paint("...", sgrDim))
	}
	return lines
}

// instructionFooter is clack's formatInstructionFooter with the guide on.
func (t *promptTerm) instructionFooter(items ...string) []string {
	y := t.sym()
	return []string{t.paint(y.bar, sgrCyan) + "  " + strings.Join(items, y.sep), t.paint(y.barEnd, sgrCyan)}
}

func (t *promptTerm) dimLabel(s string, styles ...sgr) string {
	parts := strings.Split(s, "\n")
	for i := range parts {
		parts[i] = t.paint(parts[i], styles...)
	}
	return strings.Join(parts, "\n")
}

func (t *promptTerm) hint(h string) string {
	if h == "" {
		return ""
	}
	return " " + t.paint("("+h+")", sgrDim)
}

// ---------------------------------------------------------------------------
// Confirm
// ---------------------------------------------------------------------------

// ConfirmOptions configures Confirm. Like clack, the initial answer is Yes
// unless DefaultNo is set.
type ConfirmOptions struct {
	Message   string
	Active    string // default "Yes"
	Inactive  string // default "No"
	DefaultNo bool
}

// Confirm asks a yes/no question (clack confirm). Arrow keys and h/j/k/l toggle,
// y and n answer at once, Enter accepts, Ctrl+C or Esc cancels.
func (t *promptTerm) Confirm(o ConfirmOptions) (bool, error) {
	end, err := t.begin()
	if err != nil {
		return false, err
	}
	defer end()
	active, inactive := o.Active, o.Inactive
	if active == "" {
		active = "Yes"
	}
	if inactive == "" {
		inactive = "No"
	}
	value := !o.DefaultNo
	state := promptActive
	y := t.sym()
	gray := t.paint(y.bar, sgrGray)
	render := func() []string {
		lines := []string{gray}
		lines = append(lines, prefixLines(o.Message, t.stateSymbol(state)+"  ", gray+"  ")...)
		chosen := inactive
		if value {
			chosen = active
		}
		switch state {
		case promptSubmit:
			return append(lines, gray+"  "+t.paint(chosen, sgrDim))
		case promptCancel:
			return append(lines, gray+"  "+t.paint(chosen, sgrStrike, sgrDim), gray)
		}
		var yes, no string
		if value {
			yes = t.paint(y.radioActive, sgrGreen) + " " + active
			no = t.paint(y.radioInactive, sgrDim) + " " + t.paint(inactive, sgrDim)
		} else {
			yes = t.paint(y.radioInactive, sgrDim) + " " + t.paint(active, sgrDim)
			no = t.paint(y.radioActive, sgrGreen) + " " + inactive
		}
		return append(lines, t.paint(y.bar, sgrCyan)+"  "+yes+" "+t.paint("/", sgrDim)+" "+no, t.paint(y.barEnd, sgrCyan))
	}
	f := &promptFrame{t: t}
	for {
		f.draw(render())
		if state != promptActive {
			break
		}
		k := t.keys().next()
		switch {
		case k.isCancel():
			state = promptCancel
		case k.kind == keyEnter:
			state = promptSubmit
		case k.kind == keyRune && (k.r == 'y' || k.r == 'Y'):
			value, state = true, promptSubmit
		case k.kind == keyRune && (k.r == 'n' || k.r == 'N'):
			value, state = false, promptSubmit
		case k.kind == keyUp || k.kind == keyDown || k.kind == keyLeft || k.kind == keyRight,
			k.kind == keyRune && strings.ContainsRune("hjkl", k.r):
			value = !value
		}
	}
	if state == promptCancel {
		return false, ErrPromptCancel
	}
	return value, nil
}

// ---------------------------------------------------------------------------
// Select
// ---------------------------------------------------------------------------

// SelectOptions configures Select.
type SelectOptions struct {
	Message  string
	Options  []PromptOption
	Initial  string // value under the cursor at start (default: first option)
	MaxItems int
}

// Select picks one option (clack select). Up/down (also left/right, k/j, h/l)
// move with wrap-around, Enter confirms, Ctrl+C or Esc cancels.
func (t *promptTerm) Select(o SelectOptions) (string, error) {
	end, err := t.begin()
	if err != nil {
		return "", err
	}
	defer end()
	cursor := 0
	for i, opt := range o.Options {
		if opt.Value == o.Initial {
			cursor = i
			break
		}
	}
	if len(o.Options) > 0 && o.Options[cursor].Disabled {
		cursor = findEnabled(cursor, 1, o.Options)
	}
	state := promptActive
	y := t.sym()
	gray := t.paint(y.bar, sgrGray)
	style := func(i int, active bool) string {
		opt := o.Options[i]
		switch {
		case opt.Disabled:
			return t.paint(y.radioInactive, sgrGray) + " " + t.dimLabel(opt.label(), sgrGray) + t.hint(opt.Hint)
		case active:
			return t.paint(y.radioActive, sgrGreen) + " " + opt.label() + t.hint(opt.Hint)
		}
		return t.paint(y.radioInactive, sgrDim) + " " + t.dimLabel(opt.label(), sgrDim)
	}
	render := func() []string {
		lines := []string{gray}
		lines = append(lines, prefixLines(o.Message, t.stateSymbol(state)+"  ", t.stateBar(state)+"  ")...)
		label := ""
		if len(o.Options) > 0 {
			label = o.Options[cursor].label()
		}
		switch state {
		case promptSubmit:
			return append(lines, prefixLines(t.dimLabel(label, sgrDim), gray+"  ", gray+"  ")...)
		case promptCancel:
			return append(append(lines, prefixLines(t.dimLabel(label, sgrStrike, sgrDim), gray+"  ", gray+"  ")...), gray)
		}
		bar := t.paint(y.bar, sgrCyan) + "  "
		footer := t.instructionFooter(t.paint(y.arrows, sgrDim)+" to navigate", t.paint("Enter:", sgrDim)+" confirm")
		pad := len(lines) + 1 + len(footer) + 1
		for _, l := range t.limitOptions(len(o.Options), cursor, o.MaxItems, pad, style) {
			lines = append(lines, prefixLines(l, bar, bar)...)
		}
		return append(lines, footer...)
	}
	f := &promptFrame{t: t}
	for {
		f.draw(render())
		if state != promptActive {
			break
		}
		k := t.keys().next()
		switch {
		case k.isCancel():
			state = promptCancel
		case k.kind == keyEnter:
			if len(o.Options) > 0 {
				state = promptSubmit
			}
		case k.kind == keyUp || k.kind == keyLeft || k.kind == keyRune && (k.r == 'k' || k.r == 'h'):
			cursor = findEnabled(cursor, -1, o.Options)
		case k.kind == keyDown || k.kind == keyRight || k.kind == keyRune && (k.r == 'j' || k.r == 'l'):
			cursor = findEnabled(cursor, 1, o.Options)
		}
	}
	if state == promptCancel {
		return "", ErrPromptCancel
	}
	return o.Options[cursor].Value, nil
}

// ---------------------------------------------------------------------------
// MultiSelect
// ---------------------------------------------------------------------------

// MultiSelectOptions configures MultiSelect. Like clack, a selection is required
// unless Optional is set.
type MultiSelectOptions struct {
	Message  string
	Options  []PromptOption
	Initial  []string
	Optional bool
	CursorAt string
	MaxItems int
}

// MultiSelect picks any number of options (clack multiselect). Space toggles,
// a (or Ctrl+A) toggles all, i inverts, Enter confirms, Ctrl+C or Esc cancels.
// It returns the selected values in option order.
func (t *promptTerm) MultiSelect(o MultiSelectOptions) ([]string, error) {
	end, err := t.begin()
	if err != nil {
		return nil, err
	}
	defer end()
	selected := map[string]bool{}
	for _, v := range o.Initial {
		selected[v] = true
	}
	cursor := 0
	for i, opt := range o.Options {
		if opt.Value == o.CursorAt {
			cursor = i
			break
		}
	}
	if len(o.Options) > 0 && o.Options[cursor].Disabled {
		cursor = findEnabled(cursor, 1, o.Options)
	}
	enabled := func() []PromptOption {
		var out []PromptOption
		for _, opt := range o.Options {
			if !opt.Disabled {
				out = append(out, opt)
			}
		}
		return out
	}
	count := func() int {
		n := 0
		for _, opt := range o.Options {
			if selected[opt.Value] {
				n++
			}
		}
		return n
	}
	state := promptActive
	y := t.sym()
	gray := t.paint(y.bar, sgrGray)
	style := func(i int, active bool) string {
		opt := o.Options[i]
		on := selected[opt.Value]
		switch {
		case opt.Disabled:
			return t.paint(y.checkboxOff, sgrGray) + " " + t.dimLabel(opt.label(), sgrStrike, sgrGray) + t.hint(opt.Hint)
		case active && on:
			return t.paint(y.checkboxSelected, sgrGreen) + " " + opt.label() + t.hint(opt.Hint)
		case on:
			return t.paint(y.checkboxSelected, sgrGreen) + " " + t.dimLabel(opt.label(), sgrDim) + t.hint(opt.Hint)
		case active:
			return t.paint(y.checkboxActive, sgrCyan) + " " + opt.label() + t.hint(opt.Hint)
		}
		return t.paint(y.checkboxOff, sgrDim) + " " + t.dimLabel(opt.label(), sgrDim)
	}
	picked := func(styles ...sgr) string {
		var parts []string
		for _, opt := range o.Options {
			if selected[opt.Value] {
				parts = append(parts, t.dimLabel(opt.label(), styles...))
			}
		}
		return strings.Join(parts, t.paint(", ", sgrDim))
	}
	render := func() []string {
		lines := []string{gray}
		lines = append(lines, prefixLines(o.Message, t.stateSymbol(state)+"  ", t.stateBar(state)+"  ")...)
		switch state {
		case promptSubmit:
			s := picked(sgrDim)
			if s == "" {
				s = t.paint("none", sgrDim)
			}
			return append(lines, prefixLines(s, gray+"  ", gray+"  ")...)
		case promptCancel:
			s := picked(sgrStrike, sgrDim)
			if strings.TrimSpace(s) == "" {
				return append(lines, gray)
			}
			return append(append(lines, prefixLines(s, gray+"  ", gray+"  ")...), gray)
		}
		var footer []string
		bar := t.paint(y.bar, sgrCyan) + "  "
		if state == promptError {
			bar = t.paint(y.bar, sgrYellow) + "  "
			footer = []string{
				t.paint(y.barEnd, sgrYellow) + "  " + t.paint("Please select at least one option.", sgrYellow),
				"   " + t.paint("Press ", sgrDim) + t.paint(" space ", sgrInverse) + t.paint(" to select, ", sgrDim) +
					t.paint(" enter ", sgrInverse) + t.paint(" to submit", sgrDim),
			}
		} else {
			footer = t.instructionFooter(t.paint(y.arrows, sgrDim)+" to navigate", t.paint("Space:", sgrDim)+" select", t.paint("Enter:", sgrDim)+" confirm")
		}
		pad := len(lines) + 1 + len(footer) + 1
		for _, l := range t.limitOptions(len(o.Options), cursor, o.MaxItems, pad, style) {
			lines = append(lines, prefixLines(l, bar, bar)...)
		}
		return append(lines, footer...)
	}
	f := &promptFrame{t: t}
	for {
		f.draw(render())
		if state == promptSubmit || state == promptCancel {
			break
		}
		k := t.keys().next()
		if state == promptError {
			state = promptActive
		}
		switch {
		case k.isCancel():
			state = promptCancel
		case k.kind == keyEnter:
			if !o.Optional && count() == 0 {
				state = promptError
			} else {
				state = promptSubmit
			}
		case k.kind == keyUp || k.kind == keyLeft || k.kind == keyRune && (k.r == 'k' || k.r == 'h'):
			cursor = findEnabled(cursor, -1, o.Options)
		case k.kind == keyDown || k.kind == keyRight || k.kind == keyRune && (k.r == 'j' || k.r == 'l'):
			cursor = findEnabled(cursor, 1, o.Options)
		case k.kind == keySpace:
			if len(o.Options) > 0 && !o.Options[cursor].Disabled {
				v := o.Options[cursor].Value
				selected[v] = !selected[v]
			}
		case k.kind == keyCtrlA || k.kind == keyRune && (k.r == 'a' || k.r == 'A'):
			en := enabled()
			all := count() == len(en)
			selected = map[string]bool{}
			if !all {
				for _, opt := range en {
					selected[opt.Value] = true
				}
			}
		case k.kind == keyRune && (k.r == 'i' || k.r == 'I'):
			next := map[string]bool{}
			for _, opt := range enabled() {
				if !selected[opt.Value] {
					next[opt.Value] = true
				}
			}
			selected = next
		}
	}
	if state == promptCancel {
		return nil, ErrPromptCancel
	}
	out := []string{}
	for _, opt := range o.Options {
		if selected[opt.Value] {
			out = append(out, opt.Value)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Text
// ---------------------------------------------------------------------------

// TextOptions configures Text.
type TextOptions struct {
	Message      string
	Placeholder  string
	DefaultValue string
	InitialValue string
	// Validate returns an error message for invalid input, or "".
	Validate func(string) string
}

// Text reads a line of input (clack text) with cursor movement and editing.
// An empty answer becomes DefaultValue.
func (t *promptTerm) Text(o TextOptions) (string, error) {
	end, err := t.begin()
	if err != nil {
		return "", err
	}
	defer end()
	input := []rune(o.InitialValue)
	cur := len(input)
	state := promptActive
	errMsg := ""
	y := t.sym()
	gray := t.paint(y.bar, sgrGray)
	render := func() []string {
		lines := []string{gray}
		lines = append(lines, prefixLines(o.Message, t.stateSymbol(state)+"  ", "")...)
		value := string(input)
		var shown string
		switch {
		case len(input) == 0 && o.Placeholder != "":
			p := []rune(o.Placeholder)
			shown = t.paint(string(p[0]), sgrInverse) + t.paint(string(p[1:]), sgrDim)
		case len(input) == 0:
			shown = t.paint("_", sgrInverse, sgrHidden)
		case cur >= len(input):
			shown = value + y.textCursor
		default:
			shown = string(input[:cur]) + t.paint(string(input[cur]), sgrInverse) + string(input[cur+1:])
		}
		switch state {
		case promptSubmit:
			if value == "" {
				value = o.DefaultValue
			}
			l := gray
			if value != "" {
				l += "  " + t.paint(value, sgrDim)
			}
			return append(lines, l)
		case promptCancel:
			l := gray
			if value != "" {
				l += "  " + t.paint(value, sgrStrike, sgrDim)
			}
			lines = append(lines, l)
			if strings.TrimSpace(value) != "" {
				lines = append(lines, gray)
			}
			return lines
		case promptError:
			return append(lines, t.paint(y.bar, sgrYellow)+"  "+shown, t.paint(y.barEnd, sgrYellow)+"  "+t.paint(errMsg, sgrYellow))
		}
		return append(lines, t.paint(y.bar, sgrCyan)+"  "+shown, t.paint(y.barEnd, sgrCyan))
	}
	f := &promptFrame{t: t}
	for {
		f.draw(render())
		if state == promptSubmit || state == promptCancel {
			break
		}
		k := t.keys().next()
		if state == promptError {
			state = promptActive
		}
		switch k.kind {
		case keyCtrlC, keyEscape, keyEOF:
			state = promptCancel
		case keyEnter:
			if o.Validate != nil {
				if msg := o.Validate(string(input)); msg != "" {
					errMsg, state = msg, promptError
					continue
				}
			}
			state = promptSubmit
		case keyRune, keySpace:
			r := k.r
			if k.kind == keySpace {
				r = ' '
			}
			input = append(input[:cur], append([]rune{r}, input[cur:]...)...)
			cur++
		case keyBackspace:
			if cur > 0 {
				input = append(input[:cur-1], input[cur:]...)
				cur--
			}
		case keyDelete:
			if cur < len(input) {
				input = append(input[:cur], input[cur+1:]...)
			}
		case keyLeft:
			cur = max(cur-1, 0)
		case keyRight:
			cur = min(cur+1, len(input))
		case keyHome:
			cur = 0
		case keyEnd:
			cur = len(input)
		case keyCtrlU:
			input, cur = input[cur:], 0
		}
	}
	if state == promptCancel {
		return "", ErrPromptCancel
	}
	if len(input) == 0 {
		return o.DefaultValue, nil
	}
	return string(input), nil
}

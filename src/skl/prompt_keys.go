package skl

import (
	"io"
	"time"
	"unicode/utf8"
)

type keyKind int

const (
	keyNone keyKind = iota
	keyRune
	keyEnter
	keyEscape
	keyCtrlC
	keyUp
	keyDown
	keyLeft
	keyRight
	keySpace
	keyBackspace
	keyDelete
	keyTab
	keyHome
	keyEnd
	keyCtrlA
	keyCtrlU
	keyEOF
)

type promptKey struct {
	kind keyKind
	r    rune
}

// isCancel reports Ctrl+C, Esc, and end of input, which cancel every prompt.
func (k promptKey) isCancel() bool {
	return k.kind == keyCtrlC || k.kind == keyEscape || k.kind == keyEOF
}

// escapeTimeout is how long a lone ESC waits for the rest of a sequence
// (readline's escapeCodeTimeout).
const escapeTimeout = 50 * time.Millisecond

// keyReader turns raw terminal input into keys. A read normally returns whole
// escape sequences; when one is split, a real terminal is polled briefly for
// the rest before a lone ESC counts as Escape.
type keyReader struct {
	r     io.Reader
	fd    int
	buf   []byte
	queue []promptKey
	err   error
	chunk []byte
}

func newKeyReader(r io.Reader, fd int) *keyReader {
	return &keyReader{r: r, fd: fd, chunk: make([]byte, 4096)}
}

func (k *keyReader) fill() {
	n, err := k.r.Read(k.chunk)
	k.buf = append(k.buf, k.chunk[:n]...)
	if err != nil {
		k.err = err
	}
}

// next blocks until a key is available; it returns keyEOF once input ends.
func (k *keyReader) next() promptKey {
	for len(k.queue) == 0 {
		if len(k.buf) == 0 {
			if k.err != nil {
				return promptKey{kind: keyEOF}
			}
			k.fill()
			continue
		}
		keys, rest := parsePromptKeys(k.buf, false)
		if len(keys) == 0 && len(rest) > 0 && k.err == nil {
			// The rest of a UTF-8 rune always follows; an escape sequence only
			// may, so a lone ESC waits briefly on a terminal and not at all
			// elsewhere.
			if rest[0] != 0x1b || (k.fd >= 0 && inputReady(k.fd, escapeTimeout)) {
				k.fill()
				continue
			}
		}
		if len(keys) == 0 && len(rest) > 0 {
			keys, rest = parsePromptKeys(k.buf, true)
		}
		k.buf = append(k.buf[:0], rest...)
		k.queue = append(k.queue, keys...)
	}
	key := k.queue[0]
	k.queue = k.queue[1:]
	return key
}

// parsePromptKeys decodes b into keys. Unless final is set, an incomplete
// escape sequence or UTF-8 rune at the end is returned as rest.
func parsePromptKeys(b []byte, final bool) (keys []promptKey, rest []byte) {
	add := func(kind keyKind) { keys = append(keys, promptKey{kind: kind}) }
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			if i+1 >= len(b) {
				if !final {
					return keys, b[i:]
				}
				add(keyEscape)
				i++
				continue
			}
			switch b[i+1] {
			case '[', 'O':
				kind, size, ok := parseEscapeSequence(b[i:])
				if !ok {
					if !final {
						return keys, b[i:]
					}
					add(keyEscape)
					i++
					continue
				}
				if kind != keyNone {
					add(kind)
				}
				i += size
			case 0x1b:
				add(keyEscape)
				i++
			default:
				// Alt+key: ignored, as upstream ignores meta keys.
				_, size := utf8.DecodeRune(b[i+1:])
				i += 1 + size
			}
			continue
		case c == '\r':
			add(keyEnter)
			i++
			if i < len(b) && b[i] == '\n' {
				i++
			}
			continue
		case c == '\n':
			add(keyEnter)
		case c == 0x03:
			add(keyCtrlC)
		case c == 0x01:
			add(keyCtrlA)
		case c == 0x15:
			add(keyCtrlU)
		case c == 0x7f || c == 0x08:
			add(keyBackspace)
		case c == '\t':
			add(keyTab)
		case c == ' ':
			add(keySpace)
		case c < 0x20:
			// Other control keys are ignored.
		default:
			if !utf8.FullRune(b[i:]) {
				if !final {
					return keys, b[i:]
				}
				i++
				continue
			}
			r, size := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError || size > 1 {
				keys = append(keys, promptKey{kind: keyRune, r: r})
			}
			i += size
			continue
		}
		i++
	}
	return keys, nil
}

// parseEscapeSequence decodes a CSI (ESC [) or SS3 (ESC O) sequence at b[0].
// ok is false when b ends before the sequence does.
func parseEscapeSequence(b []byte) (kind keyKind, size int, ok bool) {
	if len(b) < 3 {
		return keyNone, 0, false
	}
	if b[1] == 'O' {
		return escapeFinalKey(b[2], ""), 3, true
	}
	j := 2
	for j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f {
		j++
	}
	params := string(b[2:j])
	for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
		j++
	}
	if j >= len(b) {
		return keyNone, 0, false
	}
	if b[j] < 0x40 || b[j] > 0x7e {
		// Not a valid sequence: drop the introducer and parse the rest as keys.
		return keyNone, 2, true
	}
	return escapeFinalKey(b[j], params), j + 1, true
}

func escapeFinalKey(final byte, params string) keyKind {
	switch final {
	case 'A':
		return keyUp
	case 'B':
		return keyDown
	case 'C':
		return keyRight
	case 'D':
		return keyLeft
	case 'H':
		return keyHome
	case 'F':
		return keyEnd
	case '~':
		first := params
		for i := 0; i < len(params); i++ {
			if params[i] == ';' {
				first = params[:i]
				break
			}
		}
		switch first {
		case "1", "7":
			return keyHome
		case "4", "8":
			return keyEnd
		case "3":
			return keyDelete
		}
	}
	return keyNone
}

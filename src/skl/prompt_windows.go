//go:build windows

package skl

import (
	"time"

	"golang.org/x/sys/windows"
)

// inputReady always reports false on Windows. ReadConsoleW with
// ENABLE_VIRTUAL_TERMINAL_INPUT delivers a key's whole VT sequence in one read,
// so a lone ESC is the Escape key. Waiting on the console handle is wrong: it
// is signaled by any queued INPUT_RECORD, including the Esc key-up event, which
// ReadConsoleW then discards while blocking for the next key press.
func inputReady(int, time.Duration) bool { return false }

// enableVTOutput turns on escape sequence processing for the console output
// handle fd, which older consoles leave off, and returns a func that restores
// the previous mode.
func enableVTOutput(fd int) func() {
	if fd < 0 {
		return func() {}
	}
	h := windows.Handle(fd)
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {}
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return func() {}
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}

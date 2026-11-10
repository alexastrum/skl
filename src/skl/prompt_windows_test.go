//go:build windows

package skl

import (
	"testing"
	"time"
)

// Review #65 item 1: waiting on a console input handle is signaled by key-up
// records, after which ReadConsoleW blocks for the next key press, so a lone
// Esc must count as Escape without polling.
func TestInputReadyNeverWaitsOnWindows(t *testing.T) {
	start := time.Now()
	if inputReady(0, time.Second) {
		t.Error("inputReady must report false on Windows")
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Errorf("inputReady waited %v", el)
	}
}

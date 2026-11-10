//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package skl

import (
	"os"
	"testing"
	"time"
)

func TestInputReady(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fd := int(r.Fd())
	start := time.Now()
	if inputReady(fd, 30*time.Millisecond) {
		t.Error("empty pipe reported ready")
	}
	if el := time.Since(start); el < 20*time.Millisecond || el > time.Second {
		t.Errorf("empty pipe waited %v, want about 30ms", el)
	}
	if inputReady(fd, 0) {
		t.Error("a zero timeout must report false without polling")
	}
	if _, err := w.Write([]byte("[A")); err != nil {
		t.Fatal(err)
	}
	if !inputReady(fd, 30*time.Millisecond) {
		t.Error("pipe with data reported not ready")
	}
}

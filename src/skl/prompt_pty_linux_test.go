//go:build linux

package skl

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPty opens a pseudo-terminal pair so prompts run against a real terminal.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		m.Close()
		t.Skipf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetUint32(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		m.Close()
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("open pts: %v", err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

// ptyOutput collects everything the program writes to the terminal.
type ptyOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func drainPty(master *os.File) *ptyOutput {
	o := &ptyOutput{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			o.mu.Lock()
			o.b.Write(buf[:n])
			o.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return o
}

func (o *ptyOutput) waitFor(t *testing.T, s string) {
	t.Helper()
	o.waitMatch(t, s, false)
}

func (o *ptyOutput) waitForPlain(t *testing.T, s string) {
	t.Helper()
	o.waitMatch(t, s, true)
}

func (o *ptyOutput) waitMatch(t *testing.T, s string, strip bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		o.mu.Lock()
		raw := o.b.String()
		o.mu.Unlock()
		hay := raw
		if strip {
			hay = StripTerminalEscapes(raw)
		}
		if strings.Contains(hay, s) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %q", s, raw)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func termios(t *testing.T, f *os.File) unix.Termios {
	t.Helper()
	st, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return *st
}

func sameMode(a, b unix.Termios) bool {
	return a.Iflag == b.Iflag && a.Oflag == b.Oflag && a.Cflag == b.Cflag && a.Lflag == b.Lflag
}

func TestPtyConfirmRawModeAndRestore(t *testing.T) {
	master, slave := openPty(t)
	out := drainPty(master)
	before := termios(t, slave)
	term := newPromptTerm(slave, slave, true)
	if term.inFd < 0 || term.outFd < 0 {
		t.Fatal("pty slave must be detected as a terminal")
	}
	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, err := term.Confirm(ConfirmOptions{Message: "Proceed with installation?"})
		done <- result{ok, err}
	}()
	out.waitFor(t, "Proceed with installation?")
	during := termios(t, slave)
	if during.Lflag&(unix.ECHO|unix.ICANON|unix.ISIG) != 0 {
		t.Errorf("prompt must run in raw mode, lflag=%#x", during.Lflag)
	}
	// An arrow key split across two writes is still one key: the reader waits
	// for the rest of the sequence instead of treating ESC as cancel.
	_, _ = master.Write([]byte("\x1b"))
	time.Sleep(10 * time.Millisecond)
	_, _ = master.Write([]byte("[C"))
	// Note: ansiGreen("●") + " No" puts "\x1b[39m" between "●" and " No", so
	// waitFor checks the plain-stripped buffer.
	out.waitForPlain(t, "● No")
	_, _ = master.Write([]byte("\r"))
	r := <-done
	if r.err != nil || r.ok {
		t.Errorf("got %v, %v; want false (toggled to No)", r.ok, r.err)
	}
	if after := termios(t, slave); !sameMode(before, after) {
		t.Errorf("terminal mode not restored: before %+v after %+v", before, after)
	}
	out.waitFor(t, ansiShowCursor)
}

func TestPtyLoneEscapeCancels(t *testing.T) {
	master, slave := openPty(t)
	out := drainPty(master)
	before := termios(t, slave)
	term := newPromptTerm(slave, slave, true)
	done := make(chan error, 1)
	go func() {
		_, err := term.Select(SelectOptions{Message: "Installation scope", Options: scopeOptions})
		done <- err
	}()
	out.waitFor(t, "Installation scope")
	_, _ = master.Write([]byte("\x1b"))
	if err := <-done; !errors.Is(err, ErrPromptCancel) {
		t.Errorf("lone ESC: err = %v", err)
	}
	if after := termios(t, slave); !sameMode(before, after) {
		t.Error("terminal mode not restored after cancel")
	}
}

func TestPtyRestoresOnPanic(t *testing.T) {
	_, slave := openPty(t)
	before := termios(t, slave)
	term := newPromptTerm(slave, slave, true)
	func() {
		defer func() { _ = recover() }()
		end, err := term.begin()
		if err != nil {
			t.Fatal(err)
		}
		defer end()
		if during := termios(t, slave); sameMode(before, during) {
			t.Error("begin must enter raw mode")
		}
		panic("boom")
	}()
	if after := termios(t, slave); !sameMode(before, after) {
		t.Error("terminal mode not restored after panic")
	}
}

// TestPtySigintHelper runs a prompt on its stdin terminal for TestPtyRestoresOnSigint.
func TestPtySigintHelper(t *testing.T) {
	if os.Getenv("SKL_PTY_SIGINT_HELPER") != "1" {
		t.Skip("helper process only")
	}
	term := newPromptTerm(os.Stdin, os.Stdout, true)
	_, _ = term.Confirm(ConfirmOptions{Message: "Waiting for SIGINT"})
	os.Exit(3)
}

func TestPtyRestoresOnSigint(t *testing.T) {
	master, slave := openPty(t)
	out := drainPty(master)
	before := termios(t, slave)
	cmd := exec.Command(os.Args[0], "-test.run=^TestPtySigintHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "SKL_PTY_SIGINT_HELPER=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "Waiting for SIGINT")
	if termios(t, slave).Lflag&unix.ECHO != 0 {
		t.Error("helper prompt must be in raw mode")
	}
	_ = cmd.Process.Signal(os.Interrupt)
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Errorf("exit = %v, want code 130", err)
	}
	if after := termios(t, slave); !sameMode(before, after) {
		t.Errorf("terminal mode not restored after SIGINT: before %+v after %+v", before, after)
	}
	out.waitFor(t, ansiShowCursor)
}

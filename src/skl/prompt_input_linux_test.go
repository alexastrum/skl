package skl

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Review #65 item 5: signals that interrupt poll with EINTR (such as Go's
// SIGURG preemption) must not restart the full escape timeout, or a lone Esc
// could wait indefinitely.
func TestInputReadyEINTRKeepsDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fd := int(r.Fd())

	tid := make(chan int, 1)
	done := make(chan time.Duration, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid <- unix.Gettid()
		start := time.Now()
		inputReady(fd, 50*time.Millisecond)
		done <- time.Since(start)
	}()
	pid, target := unix.Getpid(), <-tid
	var stop atomic.Bool
	go func() {
		for !stop.Load() {
			_ = unix.Tgkill(pid, target, unix.SIGURG)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	defer stop.Store(true)
	select {
	case el := <-done:
		if el > time.Second {
			t.Errorf("inputReady took %v under repeated EINTR, want about 50ms", el)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inputReady never returned under repeated EINTR")
	}
}

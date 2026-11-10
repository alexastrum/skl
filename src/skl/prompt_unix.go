//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package skl

import (
	"time"

	"golang.org/x/sys/unix"
)

// inputReady reports whether fd has input within d.
func inputReady(fd int, d time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	// A signal (such as Go's SIGURG preemption) interrupts poll with EINTR;
	// retry with the time left so repeated signals cannot extend the wait.
	deadline := time.Now().Add(d)
	for {
		ms := int(time.Until(deadline) / time.Millisecond)
		if ms <= 0 {
			return false
		}
		n, err := unix.Poll(fds, ms)
		if err == unix.EINTR {
			continue
		}
		return err == nil && n > 0
	}
}

// enableVTOutput is a no-op: Unix terminals interpret escape sequences already.
func enableVTOutput(int) func() { return func() {} }

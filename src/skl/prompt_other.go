//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package skl

import "time"

// inputReady cannot poll on this platform; a lone ESC counts as Escape at once.
func inputReady(int, time.Duration) bool { return false }

func enableVTOutput(int) func() { return func() {} }

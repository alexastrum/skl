package skl

import (
	"os"

	"golang.org/x/term"
)

// ANSI escape variables used by CLI subcommands. RefreshColors updates them from
// the current environment, command-line flags, and stdout TTY state (#2716).
var (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[38;5;102m"
	ansiText   = "\x1b[38;5;145m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
)

var stdoutIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func init() {
	RefreshColors()
}

// ColorEnabledWith mirrors upstream src/color.ts colorEnabled(env, argv, tty).
func ColorEnabledWith(getenv func(string) string, argv []string, tty bool) bool {
	if getenv("NO_COLOR") != "" || hasCLIArg(argv, "--no-color") {
		return false
	}
	return getenv("FORCE_COLOR") != "" ||
		hasCLIArg(argv, "--color") ||
		getenv("CI") != "" ||
		(tty && getenv("TERM") != "dumb")
}

// ColorEnabled reports whether ANSI color escape sequences should be emitted.
func ColorEnabled(extraArgs ...string) bool {
	argv := os.Args
	if len(extraArgs) > 0 {
		argv = append(append([]string(nil), os.Args...), extraArgs...)
	}
	return ColorEnabledWith(os.Getenv, argv, stdoutIsTTY())
}

// ClearLineWith mirrors upstream src/color.ts clearLine(tty).
func ClearLineWith(tty bool) string {
	if tty {
		return "\r\x1b[K"
	}
	return ""
}

// ClearLine returns the carriage-return + erase-in-line sequence when stdout is a TTY.
func ClearLine() string {
	return ClearLineWith(stdoutIsTTY())
}

// RefreshColors updates package-level ANSI color strings and returns whether colors are enabled.
func RefreshColors(extraArgs ...string) bool {
	if ColorEnabled(extraArgs...) {
		ansiReset = "\x1b[0m"
		ansiBold = "\x1b[1m"
		ansiDim = "\x1b[38;5;102m"
		ansiText = "\x1b[38;5;145m"
		ansiCyan = "\x1b[36m"
		ansiYellow = "\x1b[33m"
		ansiGreen = "\x1b[32m"
		ansiRed = "\x1b[31m"
		return true
	}
	ansiReset = ""
	ansiBold = ""
	ansiDim = ""
	ansiText = ""
	ansiCyan = ""
	ansiYellow = ""
	ansiGreen = ""
	ansiRed = ""
	return false
}

func hasCLIArg(argv []string, target string) bool {
	for _, a := range argv {
		if a == target {
			return true
		}
	}
	return false
}

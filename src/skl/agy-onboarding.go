package skl

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// EnableAgyOnboarding controls whether the CLI prompts/recommends installing agy-cli
// when no active AI agents are detected on the system.
// Toggle this value to easily turn the onboarding prompts on or off.
const EnableAgyOnboarding = true

// agyTip is the non-interactive onboarding tip.
const agyTip = "💡 No active AI agents detected. We highly recommend installing agy-cli: curl -fsSL https://antigravity.google/cli/install.sh | bash"

// maybeSuggestAgy handles displaying premium onboarding recommendations or prompting
// the user to install agy-cli when no active AI agents are present.
func maybeSuggestAgy(opts AddOptions) {
	maybeSuggestAgyTo(os.Stdout, bufio.NewReader(os.Stdin), opts, isInteractive())
}

// maybeSuggestAgyTo writes the agy-cli onboarding prompt (interactive, without --yes) or
// tip to w. --json runs never call it, so stdout stays a single JSON value.
func maybeSuggestAgyTo(w io.Writer, in *bufio.Reader, opts AddOptions, interactive bool) {
	if !EnableAgyOnboarding || opts.JSON {
		return
	}

	if !opts.Yes && interactive {
		fmt.Fprintf(w, "💡 No active AI agents detected on your system.\n❓ Would you like to get agy-cli (the premium Antigravity agent CLI)? [Y/n]: ")
		response, _ := in.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response == "" || response == "y" || response == "yes" {
			fmt.Fprintln(w, "\n🚀 Excellent choice! To get agy-cli, run:")
			fmt.Fprintln(w, "   curl -fsSL https://antigravity.google/cli/install.sh | bash")
			fmt.Fprintln(w, "   Or visit: https://antigravity.google/docs/cli-overview for documentation.")
			fmt.Fprintln(w)
		}
		return
	}
	fmt.Fprintln(w, agyTip)
}

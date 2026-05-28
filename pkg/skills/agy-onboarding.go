package skills

import (
	"fmt"
	"strings"
)

// EnableAgyOnboarding controls whether the CLI prompts/recommends installing agy-cli
// when no active AI agents are detected on the system.
// Toggle this value to easily turn the onboarding prompts on or off.
const EnableAgyOnboarding = true

// maybeSuggestAgy handles displaying premium onboarding recommendations or prompting
// the user to install agy-cli when no active AI agents are present.
func maybeSuggestAgy(opts AddOptions) {
	if !EnableAgyOnboarding {
		return
	}

	if !opts.Yes && isInteractive() {
		fmt.Printf("💡 No active AI agents detected on your system.\n❓ Would you like to get agy-cli (the premium Antigravity agent CLI)? [Y/n]: ")
		var response string
		_, _ = fmt.Scanln(&response)
		response = strings.TrimSpace(strings.ToLower(response))
		if response == "" || response == "y" || response == "yes" {
			fmt.Println("\n🚀 Excellent choice! To get agy-cli, run:")
			fmt.Println("   curl -fsSL https://antigravity.google/cli/install.sh | bash")
			fmt.Println("   Or visit: https://antigravity.google/docs/cli-overview for documentation.")
			fmt.Println()
		}
	} else {
		fmt.Println("💡 No active AI agents detected. We highly recommend installing agy-cli: curl -fsSL https://antigravity.google/cli/install.sh | bash")
	}
}

package skl

import (
	"os"
	"strings"
)

const devinLocalPath = "/opt/.devin"

// DetectedAgentDetails holds metadata about the active AI agent environment.
type DetectedAgentDetails struct {
	Name string `json:"name"`
}

// AgentDetectionResult describes whether the CLI is running inside an AI agent harness.
type AgentDetectionResult struct {
	IsAgent bool                  `json:"isAgent"`
	Agent   *DetectedAgentDetails `json:"agent,omitempty"`
}

// agentNameToType maps @vercel/detect-agent names to skl AgentType identifiers.
var agentNameToType = map[string]AgentType{
	"cursor":          "cursor",
	"cursor-cli":      "cursor",
	"claude":          "claude-code",
	"cowork":          "claude-code",
	"devin":           "universal",
	"replit":          "replit",
	"gemini":          "gemini-cli",
	"codex":           "codex",
	"antigravity":     "antigravity",
	"antigravity-cli": "antigravity-cli",
	"augment-cli":     "augment",
	"opencode":        "opencode",
	"github-copilot":  "github-copilot",
}

func hasStrongCursorAgentSignal(getenv func(string) string) bool {
	if strings.TrimSpace(getenv("CURSOR_AGENT")) != "" {
		return true
	}
	return getenv("CURSOR_EXTENSION_HOST_ROLE") == "agent-exec"
}

func refineAgentResult(result AgentDetectionResult, getenv func(string) string) AgentDetectionResult {
	if !result.IsAgent || result.Agent == nil {
		return result
	}
	if result.Agent.Name == "cursor" || result.Agent.Name == "cursor-cli" {
		if !hasStrongCursorAgentSignal(getenv) {
			return AgentDetectionResult{IsAgent: false}
		}
		if result.Agent.Name == "cursor" {
			return AgentDetectionResult{
				IsAgent: true,
				Agent:   &DetectedAgentDetails{Name: "cursor-cli"},
			}
		}
	}
	return result
}

func determineRawAgent(getenv func(string) string, pathExists func(string) bool) AgentDetectionResult {
	if raw := getenv("AI_AGENT"); raw != "" {
		name := strings.TrimSpace(raw)
		if name != "" {
			if name == "github-copilot" || name == "github-copilot-cli" {
				return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "github-copilot"}}
			}
			if name == "v0" {
				return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "v0"}}
			}
			return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: name}}
		}
	}

	if getenv("CURSOR_TRACE_ID") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "cursor"}}
	}
	if getenv("CURSOR_AGENT") != "" || getenv("CURSOR_EXTENSION_HOST_ROLE") == "agent-exec" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "cursor-cli"}}
	}
	if getenv("GEMINI_CLI") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "gemini"}}
	}
	if getenv("CODEX_SANDBOX") != "" || getenv("CODEX_CI") != "" || getenv("CODEX_THREAD_ID") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "codex"}}
	}
	if getenv("ANTIGRAVITY_AGENT") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "antigravity"}}
	}
	if getenv("AUGMENT_AGENT") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "augment-cli"}}
	}
	if getenv("OPENCODE_CLIENT") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "opencode"}}
	}
	if getenv("CLAUDECODE") != "" || getenv("CLAUDE_CODE") != "" {
		if getenv("CLAUDE_CODE_IS_COWORK") != "" {
			return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "cowork"}}
		}
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "claude"}}
	}
	if getenv("REPL_ID") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "replit"}}
	}
	if getenv("COPILOT_MODEL") != "" || getenv("COPILOT_ALLOW_ALL") != "" || getenv("COPILOT_GITHUB_TOKEN") != "" {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "github-copilot"}}
	}
	if pathExists != nil && pathExists(devinLocalPath) {
		return AgentDetectionResult{IsAgent: true, Agent: &DetectedAgentDetails{Name: "devin"}}
	}

	return AgentDetectionResult{IsAgent: false}
}

// DetectAgentWithEnv detects whether the CLI is running inside an AI agent using custom env/fs readers.
func DetectAgentWithEnv(getenv func(string) string, pathExists func(string) bool) AgentDetectionResult {
	if getenv == nil {
		getenv = os.Getenv
	}
	if pathExists == nil {
		pathExists = defaultPathExists
	}
	return refineAgentResult(determineRawAgent(getenv, pathExists), getenv)
}

// DetectAgent detects whether the CLI is running inside an AI agent environment.
func DetectAgent() AgentDetectionResult {
	return DetectAgentWithEnv(os.Getenv, defaultPathExists)
}

// IsRunningInAgent returns true if the CLI is running inside a detected AI agent.
func IsRunningInAgent() bool {
	return DetectAgent().IsAgent
}

// IsRunningInAgentWithEnv returns true if the supplied environment indicates an AI agent session.
func IsRunningInAgentWithEnv(getenv func(string) string, pathExists func(string) bool) bool {
	return DetectAgentWithEnv(getenv, pathExists).IsAgent
}

// GetDetectedAgentName returns the detected agent's name, or "" if not running in an agent.
func GetDetectedAgentName() string {
	res := DetectAgent()
	if res.IsAgent && res.Agent != nil {
		return res.Agent.Name
	}
	return ""
}

// GetAgentType maps a detected agent name to the corresponding skl AgentType.
// Returns ("", false) when the agent name cannot be mapped.
func GetAgentType(agentName string) (AgentType, bool) {
	t, ok := agentNameToType[agentName]
	return t, ok
}

// IsCIWithEnv reports whether the given environment represents a CI runner (matching src/ci.ts).
func IsCIWithEnv(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	ciVars := []string{
		"CI",
		"GITHUB_ACTIONS",
		"GITLAB_CI",
		"CIRCLECI",
		"TRAVIS",
		"BUILDKITE",
		"JENKINS_URL",
		"TEAMCITY_VERSION",
	}
	for _, k := range ciVars {
		if getenv(k) != "" {
			return true
		}
	}
	return false
}

// IsCI reports whether the current process is executing in a CI environment.
func IsCI() bool {
	return IsCIWithEnv(os.Getenv)
}

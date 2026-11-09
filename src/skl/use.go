package skl

// use.go ports upstream src/use.ts: `skl use <source>[@<skill>]` resolves one skill
// without installing it and prints a prompt (or launches a supported agent with it).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// UseAgentCommands maps agents supported by `skl use --agent` to the command they launch.
var UseAgentCommands = map[AgentType][]string{
	"claude-code": {"claude"},
	"codex":       {"codex"},
	"sarvam-code": {"sarvam-code"},
}

// SupportedUseAgents lists agents accepted by `skl use --agent`, in upstream order.
var SupportedUseAgents = []AgentType{"claude-code", "codex", "sarvam-code"}

// ParseUseResult is the result of parsing `skl use` arguments.
type ParseUseResult struct {
	Sources []string
	Options UseOptions
	Errors  []error
}

// UseIO overrides the streams used by RunUseWithIO.
type UseIO struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Cwd    string
}

// MaterializedUseSkill is a selected skill copied into a skills-use-* temp directory.
type MaterializedUseSkill struct {
	TempRoot           string
	SkillDir           string
	SkillMd            string
	HasSupportingFiles bool
}

// useRunCommand launches an agent command; overridable in tests.
var useRunCommand = func(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func supportedUseAgentNames() []string {
	out := make([]string, len(SupportedUseAgents))
	for i, a := range SupportedUseAgents {
		out[i] = string(a)
	}
	return out
}

func errUseAgentUnsupported(a AgentType) *DiagnosticError {
	return NewDiagnostic(CodeUseAgentUnsupported,
		fmt.Sprintf("Running %s is not supported yet.", agentLabel(a)),
		"Supported agents for skills use --agent: "+strings.Join(supportedUseAgentNames(), ", "))
}

// ParseUseOptions parses `skl use` arguments (upstream parseUseOptions).
func ParseUseOptions(args []string) ParseUseResult {
	var res ParseUseResult
	opts := &res.Options
	agentFlag := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		switch {
		case arg == "--help" || arg == "-h":
			opts.Help = true
		case arg == "--full-depth":
			opts.FullDepth = true
		case arg == "--skill" || arg == "-s":
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				res.Errors = append(res.Errors, ErrOptionRequiresValue(arg, "a skill name"))
			} else if opts.Skill != "" {
				res.Errors = append(res.Errors, ErrDuplicateOption("--skill"))
				i++
			} else {
				opts.Skill = args[i+1]
				i++
			}
		case arg == "--agent" || arg == "-a":
			agentFlag = true
			start := len(opts.Agent)
			j := i + 1
			for j < len(args) && args[j] != "" && !strings.HasPrefix(args[j], "-") {
				opts.Agent = append(opts.Agent, AgentType(args[j]))
				j++
			}
			if len(opts.Agent) == start {
				res.Errors = append(res.Errors, ErrOptionRequiresValue(arg, "an agent name"))
			}
			i = j - 1
		case strings.HasPrefix(arg, "-"):
			res.Errors = append(res.Errors, ErrUnknownOption(arg))
		default:
			res.Sources = append(res.Sources, arg)
		}
	}
	if agentFlag {
		res.Errors = append(res.Errors, validateUseAgentOption(opts.Agent)...)
	}
	return res
}

func validateUseAgentOption(values []AgentType) []error {
	if len(values) == 0 {
		return nil
	}
	var errs []error
	var invalid []string
	for _, a := range values {
		if a == "*" {
			continue
		}
		if _, ok := Agents[a]; !ok {
			invalid = append(invalid, string(a))
		}
	}
	if addHasAgent(values, "*") {
		errs = append(errs, NewDiagnostic(CodeUseAgentWildcard, "skills use --agent does not support '*'; specify exactly one agent.", ""))
	}
	if len(values) > 1 {
		errs = append(errs, NewDiagnostic(CodeUseMultipleAgents, "skills use --agent accepts exactly one agent.", ""))
	}
	if len(invalid) > 0 {
		errs = append(errs, ErrInvalidAgents(invalid, installSortedAgentNames()))
	}
	return errs
}

// BuildUsePrompt builds the prompt that hands a SKILL.md to an agent.
func BuildUsePrompt(skillMd string, supportDir string, hasSupportingFiles bool) string {
	sections := []string{
		"You are being given a Skill to execute for the user's next request.",
		"Use the following SKILL.md as your instructions:",
		fmt.Sprintf("<SKILL.md>\n%s\n</SKILL.md>", skillMd),
	}
	if hasSupportingFiles && supportDir != "" {
		sections = append(sections, fmt.Sprintf(
			"Supporting files for this skill were downloaded to:\n%s\n\nWhen the SKILL.md references relative paths, read them from that directory.",
			supportDir))
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// MaterializeUseSkill copies a skill directory into a new skills-use-* temp directory
// (with #2652 symlink confinement). The temp root is kept so the agent can read the
// supporting files; callers own its removal.
func MaterializeUseSkill(skill Skill, dirName string) (*MaterializedUseSkill, error) {
	if dirName == "" {
		dirName = skill.Name
	}
	tempRoot, err := os.MkdirTemp("", "skills-use-")
	if err != nil {
		return nil, err
	}
	skillDir := filepath.Join(tempRoot, SanitizeName(dirName))
	if !isPathSafe(tempRoot, skillDir) {
		_ = os.RemoveAll(tempRoot)
		return nil, NewDiagnostic(CodeUnsafeSkillName, "Invalid skill name: potential path traversal detected", "")
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		_ = os.RemoveAll(tempRoot)
		return nil, err
	}
	if err := CopySkillDirectory(skill.Path, skillDir, "", false); err != nil {
		_ = os.RemoveAll(tempRoot)
		return nil, err
	}
	skillMd := skill.RawContent
	if skillMd == "" {
		raw, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			_ = os.RemoveAll(tempRoot)
			return nil, err
		}
		skillMd = string(raw)
	}
	has, err := containsSupportingFiles(skillDir, skillDir)
	if err != nil {
		_ = os.RemoveAll(tempRoot)
		return nil, err
	}
	return &MaterializedUseSkill{TempRoot: tempRoot, SkillDir: skillDir, SkillMd: skillMd, HasSupportingFiles: has}, nil
}

func containsSupportingFiles(root, dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			has, err := containsSupportingFiles(root, p)
			if err != nil || has {
				return has, err
			}
			continue
		}
		rel, _ := filepath.Rel(root, p)
		if !strings.EqualFold(filepath.ToSlash(rel), "skill.md") {
			return true, nil
		}
	}
	return false, nil
}

// LaunchAgentInteractively runs the agent's command with the prompt as its argument,
// attached to the given streams, and returns the agent's exit code.
func LaunchAgentInteractively(agent AgentType, prompt string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmdline, ok := UseAgentCommands[agent]
	if !ok || len(cmdline) == 0 {
		return 1, errUseAgentUnsupported(agent)
	}
	args := append(append([]string{}, cmdline[1:]...), prompt)
	err := useRunCommand(cmdline[0], args, stdin, stdout, stderr)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return 1, NewDiagnostic(CodeUseAgentCommandNotFound,
			fmt.Sprintf("Could not launch %s: command not found: %s", agentLabel(agent), cmdline[0]), "")
	}
	return 1, err
}

// UseHelp returns the `skl use` help text.
func UseHelp() string {
	return fmt.Sprintf(`Usage: skl use <source>[@<skill>] [options]

Generate a prompt for using one skill without installing it.

Options:
  -s, --skill <skill>   Select the skill to use
  -a, --agent <agent>   Start one supported agent interactively (%s)
  --full-depth          Search nested directories like skl add --full-depth
  -h, --help            Show this help message

Examples:
  skl use vercel-labs/agent-skills@web-design-guidelines | claude
  skl use vercel-labs/agent-skills --skill web-design-guidelines --agent claude-code
  skl use vercel-labs/agent-skills@web-design-guidelines --agent codex`, strings.Join(supportedUseAgentNames(), ", "))
}

func resolveUseSelector(fromSource, fromOption string) (string, error) {
	if fromSource != "" && fromOption != "" {
		if !strings.EqualFold(fromSource, fromOption) {
			return "", ErrConflictingSkillSelectors(fromSource, fromOption)
		}
		return fromOption, nil
	}
	if fromOption != "" {
		return fromOption, nil
	}
	return fromSource, nil
}

func selectUseSkill(fetched *FetchedSource, selector, source string) (FetchedSkill, error) {
	skills := fetched.Skills
	wellKnown := fetched.SourceType == SourceTypeWellKnown
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = addSkillName(s.Skill)
		if wellKnown && s.InstallName != "" {
			names[i] = s.InstallName
		}
	}
	if len(skills) == 0 {
		if wellKnown {
			return FetchedSkill{}, ErrWellKnownIndex()
		}
		return FetchedSkill{}, ErrNoValidSkills()
	}
	if selector == "" {
		if len(skills) == 1 {
			return skills[0], nil
		}
		return FetchedSkill{}, ErrMultipleSkillsInSource("skl", source, names)
	}
	var selected []FetchedSkill
	if wellKnown {
		for _, s := range skills {
			if strings.EqualFold(s.InstallName, selector) || strings.EqualFold(s.Name, selector) {
				selected = append(selected, s)
			}
		}
	} else {
		selected = addFilterFetched(skills, []string{selector})
	}
	if len(selected) == 0 {
		return FetchedSkill{}, NewDiagnostic(CodeUseNoMatchingSkill,
			"No matching skill found for: "+selector, availableSkillsFix(names))
	}
	if len(selected) > 1 {
		return FetchedSkill{}, ErrAmbiguousSkillSelector(selector)
	}
	return selected[0], nil
}

// RunUse runs `skl use` with os streams and returns the exit code.
func RunUse(args []string) int {
	return RunUseWithIO(args, UseIO{})
}

// RunUseWithIO runs `skl use`; only the prompt (or the agent's own output) goes to stdout.
func RunUseWithIO(args []string, uio UseIO) int {
	if uio.Stdout == nil {
		uio.Stdout = os.Stdout
	}
	if uio.Stderr == nil {
		uio.Stderr = os.Stderr
	}
	if uio.Stdin == nil {
		uio.Stdin = os.Stdin
	}
	fail := func(errs ...error) int {
		for _, e := range errs {
			fmt.Fprintln(uio.Stderr, FormatError(e))
		}
		return 1
	}

	p := ParseUseOptions(args)
	opts := p.Options
	if opts.Help {
		fmt.Fprintln(uio.Stdout, UseHelp())
		return 0
	}
	if len(p.Errors) > 0 {
		return fail(p.Errors...)
	}
	if len(p.Sources) == 0 {
		fmt.Fprintln(uio.Stderr, FormatError(ErrMissingArgument("source")))
		fmt.Fprintln(uio.Stderr, "\n"+UseHelp())
		return 1
	}
	if len(p.Sources) > 1 {
		return fail(ErrMultipleSources(p.Sources))
	}
	var useAgent AgentType
	if len(opts.Agent) > 0 {
		useAgent = opts.Agent[0]
		if _, ok := UseAgentCommands[useAgent]; !ok {
			return fail(errUseAgentUnsupported(useAgent))
		}
	}

	source := p.Sources[0]
	cwd := installCwd(uio.Cwd)
	parsed, err := ParseSource(source, ParseSourceOptions{Cwd: cwd})
	if err != nil {
		return fail(err)
	}
	selector, err := resolveUseSelector(parsed.SkillFilter, opts.Skill)
	if err != nil {
		return fail(err)
	}
	if parsed.Type == SourceTypeLocal {
		if _, err := os.Stat(parsed.LocalPath); err != nil {
			return fail(ErrLocalPathNotFound(parsed.LocalPath))
		}
	}

	var requested []string
	if selector != "" {
		requested = []string{selector}
	}
	fetched, err := fetchSourceFn(SourceRequest{
		Source: source, Parsed: &parsed, Skills: requested, FullDepth: opts.FullDepth,
		NoFullDepthRetry: true, IncludeInternal: selector != "", Yes: true, Cwd: cwd,
	})
	if err != nil {
		if fetched != nil {
			_ = fetched.Cleanup()
		}
		return fail(err)
	}
	selected, err := selectUseSkill(fetched, selector, source)
	if err != nil {
		_ = fetched.Cleanup()
		return fail(err)
	}
	dirName := selected.Name
	if selected.InstallName != "" {
		dirName = selected.InstallName
	}
	materialized, err := MaterializeUseSkill(selected.Skill, dirName)
	_ = fetched.Cleanup()
	if err != nil {
		return fail(err)
	}

	prompt := BuildUsePrompt(materialized.SkillMd, materialized.SkillDir, materialized.HasSupportingFiles)
	if useAgent != "" {
		code, err := LaunchAgentInteractively(useAgent, prompt, uio.Stdin, uio.Stdout, uio.Stderr)
		if err != nil {
			return fail(err)
		}
		return code
	}
	_, _ = io.WriteString(uio.Stdout, prompt)
	return 0
}

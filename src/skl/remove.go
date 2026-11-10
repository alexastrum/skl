package skl

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RemoveOptions contains options for the remove command.
type RemoveOptions struct {
	Global bool
	Agent  []AgentType
	Yes    bool
	All    bool
}

// ParseRemoveOptions parses `skl remove` arguments into skill names and options.
// `-s/--skill` and `-a/--agent` consume every following non-flag token; `--all`
// implies `-y`; unknown flags are ignored rather than treated as skill names.
func ParseRemoveOptions(args []string) ([]string, RemoveOptions) {
	var opts RemoveOptions
	skills := []string{}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "-g", "--global":
			opts.Global = true
		case "-p", "--project":
			opts.Global = false
		case "-y", "--yes":
			opts.Yes = true
		case "--all":
			opts.All = true
			opts.Yes = true
		case "-s", "--skill":
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				for _, part := range strings.Split(args[i], ",") {
					if p := strings.TrimSpace(part); p != "" {
						skills = append(skills, p)
					}
				}
			}
		case "-a", "--agent":
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.Agent = append(opts.Agent, splitAgentList(args[i])...)
			}
		default:
			if arg != "" && !strings.HasPrefix(arg, "-") {
				skills = append(skills, arg)
			}
		}
	}
	return skills, opts
}

// ResolveSkillsToRemove maps requested names onto removal targets. Folder names and
// lock keys are indexed by sanitized name (lock keys win, so "ce:review" stays exact
// for lock removal); a request that matches nothing is tried as an exact lock source.
func ResolveSkillsToRemove(requested, folderNames, lockKeys []string, sourceByKey map[string]string) []string {
	identity := map[string]string{}
	for _, f := range folderNames {
		identity[SanitizeName(f)] = f
	}
	for _, k := range lockKeys {
		identity[SanitizeName(k)] = k
	}
	seen := map[string]bool{}
	matched := []string{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			matched = append(matched, s)
		}
	}
	for _, name := range requested {
		if hit, ok := identity[SanitizeName(name)]; ok {
			add(hit)
			continue
		}
		for _, k := range lockKeys {
			if sourceByKey != nil && sourceByKey[k] == name {
				add(k)
			}
		}
	}
	return matched
}

// ScanInstalledSkillNames returns sorted folder names (holding a SKILL.md) in the
// canonical dir and every agent dir for the scope. Dot-directories are skipped.
func ScanInstalledSkillNames(isGlobal bool, cwd string, onWarn func(string)) []string {
	set := map[string]bool{}
	scan := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) && onWarn != nil {
				onWarn(fmt.Sprintf("Could not scan directory %s: %v", dir, err))
			}
			return
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if strings.HasPrefix(e.Name(), ".") || !isDirOrSymlinkToDir(e, full) || !lcHasSkillMd(full) {
				continue
			}
			set[e.Name()] = true
		}
	}
	scan(GetCanonicalSkillsDir(isGlobal, cwd))
	for _, k := range sortedAgentKeys() {
		cfg := Agents[AgentType(k)]
		if isGlobal {
			if cfg.GlobalSkillsDir != "" {
				scan(ResolvePath(cfg.GlobalSkillsDir, cwd))
			}
		} else if cfg.SkillsDir != "" {
			scan(filepath.Join(cwd, cfg.SkillsDir))
		}
	}
	if !isGlobal {
		for _, sub := range GetEveSubagents(cwd) {
			scan(GetEveSubagentSkillsDir(sub, cwd))
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func isSameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

// lcIsStrictlyWithin reports whether target is inside base (and not base itself).
func lcIsStrictlyWithin(base, target string) bool {
	b, t := filepath.Clean(base), filepath.Clean(target)
	return b != t && lcIsPathSafe(b, t)
}

// lcRemoveSkillDir deletes dir/name, where name must be a single path element.
// Every lifecycle deletion goes through it, so a crafted lock key, skill name
// or directory entry can never make RemoveAll touch anything outside dir.
func lcRemoveSkillDir(dir, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || filepath.Base(name) != name || filepath.VolumeName(name) != "" {
		return fmt.Errorf("refusing to remove unsafe skill path %q in %s", name, dir)
	}
	target := filepath.Join(dir, name)
	if !lcIsStrictlyWithin(dir, target) {
		return fmt.Errorf("refusing to remove %s: outside %s", target, dir)
	}
	return os.RemoveAll(target)
}

// FindGloballyTrackedSkills returns the selected skills a project-scoped remove must
// refuse: when cwd's skills dir is the global one (cwd is $HOME), removing would
// delete a globally tracked skill and orphan its global lock entry (#2592, #2469).
func FindGloballyTrackedSkills(selected []string, cwd string) ([]string, error) {
	if !isSameDir(GetCanonicalSkillsDir(false, cwd), GetCanonicalSkillsDir(true, cwd)) {
		return nil, nil
	}
	local, err := ReadLocalLock(cwd)
	if err != nil {
		return nil, err
	}
	global, _ := ReadSkillLock()
	globalNames := map[string]bool{}
	for k := range global.Skills {
		globalNames[SanitizeName(k)] = true
	}
	var out []string
	for _, s := range selected {
		if _, inLocal := local.Skills[s]; !inLocal && globalNames[SanitizeName(s)] {
			out = append(out, s)
		}
	}
	return out, nil
}

// RemoveSkillResult reports the outcome of removing one skill.
type RemoveSkillResult struct {
	Skill      string
	Success    bool
	Source     string
	SourceType string
	Error      string
}

// RemoveInstalledOptions configures RemoveInstalledSkills.
type RemoveInstalledOptions struct {
	Global       bool
	Cwd          string
	TargetAgents []AgentType
	// SkipLockfile leaves lock files untouched (upstream lockfile: false).
	SkipLockfile bool
	OnWarn       func(string)
}

// RemoveInstalledSkills removes skills from the target agents without prompting.
// The canonical copy and lock entry are deleted only when no remaining detected
// agent still uses the skill (reference-counted cleanup).
func RemoveInstalledSkills(skills []string, opts RemoveInstalledOptions) []RemoveSkillResult {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	isGlobal := opts.Global
	targets := opts.TargetAgents
	if len(targets) == 0 {
		for _, k := range sortedAgentKeys() {
			targets = append(targets, AgentType(k))
		}
	}
	results := make([]RemoveSkillResult, 0, len(skills))
	for _, skillName := range skills {
		canonicalPath := GetCanonicalPath(skillName, isGlobal, cwd)
		sanitized := SanitizeName(skillName)
		for _, a := range targets {
			cfg, ok := Agents[a]
			if !ok {
				continue
			}
			dirs := []string{GetAgentBaseDir(a, isGlobal, cwd, "")}
			if isGlobal && cfg.GlobalSkillsDir != "" {
				dirs = append(dirs, ResolvePath(cfg.GlobalSkillsDir, cwd))
			} else if !isGlobal {
				dirs = append(dirs, filepath.Join(cwd, cfg.SkillsDir))
				if a == "eve" {
					for _, sub := range GetEveSubagents(cwd) {
						dirs = append(dirs, GetEveSubagentSkillsDir(sub, cwd))
					}
				}
			}
			for _, d := range dirs {
				p := filepath.Join(d, sanitized)
				if p == canonicalPath {
					continue
				}
				if _, err := os.Lstat(p); err == nil {
					if err := lcRemoveSkillDir(d, sanitized); err != nil && opts.OnWarn != nil {
						opts.OnWarn(fmt.Sprintf("Could not remove skill from %s: %v", cfg.DisplayName, err))
					}
				}
			}
		}

		stillUsed := false
		for _, a := range DetectInstalledAgents(cwd) {
			if a == AgentUniversal || containsAgent(targets, a) {
				continue
			}
			if _, err := os.Lstat(GetInstallPath(skillName, a, isGlobal, cwd)); err == nil {
				stillUsed = true
				break
			}
		}

		res := RemoveSkillResult{Skill: skillName, Success: true, Source: "local", SourceType: "local"}
		if !stillUsed {
			if err := lcRemoveSkillDir(GetCanonicalSkillsDir(isGlobal, cwd), sanitized); err != nil {
				res.Success = false
				res.Error = err.Error()
				results = append(results, res)
				continue
			}
		}
		updateLock := !opts.SkipLockfile && !stillUsed
		if isGlobal {
			gl, _ := ReadSkillLock()
			key := lockKeyFor(skillName, keysOf(gl.Skills))
			if e, ok := gl.Skills[key]; ok {
				res.Source, res.SourceType = nonEmpty(e.Source, "local"), nonEmpty(e.SourceType, "local")
			}
			if updateLock && key != "" {
				if _, err := RemoveSkillFromLock(key); err != nil {
					res.Success, res.Error = false, err.Error()
				}
			}
		} else {
			ll, err := ReadLocalLock(cwd)
			if err != nil {
				res.Success, res.Error = false, FormatError(err)
				results = append(results, res)
				continue
			}
			key := lockKeyFor(skillName, keysOf(ll.Skills))
			if e, ok := ll.Skills[key]; ok {
				res.Source, res.SourceType = nonEmpty(e.Source, "local"), nonEmpty(e.SourceType, "local")
			}
			if updateLock && key != "" {
				if _, err := RemoveSkillFromLocalLock(key, cwd); err != nil {
					res.Success, res.Error = false, err.Error()
				}
			}
		}
		results = append(results, res)
	}
	return results
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// lockKeyFor returns the exact lock key for skillName, falling back to a key with
// the same sanitized name; "" when none matches.
func lockKeyFor(skillName string, keys []string) string {
	for _, k := range keys {
		if k == skillName {
			return k
		}
	}
	s := SanitizeName(skillName)
	for _, k := range keys {
		if SanitizeName(k) == s {
			return k
		}
	}
	return ""
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// errRemoveNothing signals that nothing was selected (exit 0 with a message).
type removeNothing struct{ msg string }

func (r *removeNothing) Error() string { return r.msg }

// removeSelection resolves which skills a remove invocation targets. It returns
// the installed folder names (for interactive selection) and lock keys.
func removeSelection(skillNames []string, opts RemoveOptions, cwd string, onWarn func(string)) (selected, installed, lockKeys []string, err error) {
	installed = ScanInstalledSkillNames(opts.Global, cwd, onWarn)
	sourceByKey := map[string]string{}
	if opts.Global {
		gl, _ := ReadSkillLock()
		for k, e := range gl.Skills {
			sourceByKey[k] = e.Source
		}
		lockKeys = keysOf(gl.Skills)
	} else {
		ll, lerr := ReadLocalLock(cwd)
		if lerr != nil {
			return nil, nil, nil, lerr
		}
		for k, e := range ll.Skills {
			sourceByKey[k] = e.Source
		}
		lockKeys = keysOf(ll.Skills)
	}

	var requested []string
	switch {
	case opts.All && opts.Global:
		// Global --all only removes skills recorded in .skill-lock.json (#609).
		requested = lockKeys
	case opts.All:
		requested = append(append([]string{}, installed...), lockKeys...)
	default:
		requested = skillNames
	}
	if opts.All || len(skillNames) > 0 {
		selected = ResolveSkillsToRemove(requested, installed, lockKeys, sourceByKey)
	}
	if len(installed) == 0 && len(selected) == 0 && (opts.All || len(skillNames) > 0) {
		return nil, installed, lockKeys, &removeNothing{noSkillsMsg(opts)}
	}
	if !opts.All && len(skillNames) > 0 && len(selected) == 0 {
		return nil, installed, lockKeys, ErrNoMatchingSkills(skillNames, nil)
	}
	return selected, installed, lockKeys, nil
}

func noSkillsMsg(opts RemoveOptions) string {
	if opts.All && opts.Global {
		return "No globally managed skills found to remove."
	}
	return "No skills found to remove."
}

// validateRemoveArgs applies the wildcard, --all and --agent guards.
func validateRemoveArgs(skillNames []string, opts *RemoveOptions) ([]string, error) {
	var names []string
	for _, n := range skillNames {
		if n == "*" {
			opts.All = true
			continue
		}
		names = append(names, n)
	}
	if opts.All && len(names) > 0 {
		return nil, ErrRemoveAllWithNames(names[0])
	}
	if invalid := invalidAgentNames(opts.Agent); len(invalid) > 0 {
		return nil, ErrInvalidAgents(invalid, sortedAgentKeys())
	}
	return names, nil
}

// RemoveSkills removes skills non-interactively (the library entry point used by
// tests and other commands). Diagnostics are returned as errors; "nothing to
// remove" is not an error.
func RemoveSkills(skillNames []string, opts RemoveOptions, cwd string) error {
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	names, err := validateRemoveArgs(skillNames, &opts)
	if err != nil {
		return err
	}
	if !opts.All && len(names) == 0 {
		return ErrMissingArgument("skills")
	}
	selected, _, _, err := removeSelection(names, opts, cwd, nil)
	if err != nil {
		if _, ok := err.(*removeNothing); ok {
			return nil
		}
		return err
	}
	if !opts.Global {
		tracked, err := FindGloballyTrackedSkills(selected, cwd)
		if err != nil {
			return err
		}
		if len(tracked) > 0 {
			return ErrRemoveGloballyTracked(tracked)
		}
	}
	results := RemoveInstalledSkills(selected, RemoveInstalledOptions{Global: opts.Global, Cwd: cwd, TargetAgents: opts.Agent})
	var failed []string
	for _, r := range results {
		if !r.Success {
			failed = append(failed, fmt.Sprintf("%s: %s", r.Skill, r.Error))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to remove %d skill(s):\n  %s", len(failed), strings.Join(failed, "\n  "))
	}
	return nil
}

// stdinIsTTY reports whether stdin is an interactive terminal (overridable in tests).
var stdinIsTTY = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// promptLine prints prompt and reads one trimmed line; ok is false on EOF/error.
func promptLine(in *bufio.Reader, out io.Writer, prompt string) (string, bool) {
	fmt.Fprint(out, prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// RunRemove implements `skl remove`, returning the process exit code.
func RunRemove(skillNames []string, opts RemoveOptions, cwd string) int {
	return runRemove(skillNames, opts, cwd, os.Stdout, os.Stderr, os.Stdin)
}

func runRemove(skillNames []string, opts RemoveOptions, cwd string, stdout, stderr io.Writer, stdin io.Reader) int {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if det := DetectAgent(); det.IsAgent {
		opts.Yes = true
		name := "Agent"
		if det.Agent != nil && det.Agent.Name != "" {
			name = det.Agent.Name
		}
		fmt.Fprintf(stdout, "%s Agent detected - removing non-interactively\n", name)
	}
	warn := func(m string) { fmt.Fprintf(stderr, "%sWarning: %s%s\n", ansiYellow, m, ansiReset) }

	names, err := validateRemoveArgs(skillNames, &opts)
	if err != nil {
		fmt.Fprintln(stderr, FormatError(err))
		return 1
	}

	selected, installed, lockKeys, err := removeSelection(names, opts, cwd, warn)
	if err != nil {
		if rn, ok := err.(*removeNothing); ok {
			fmt.Fprintln(stdout, rn.msg)
			return 0
		}
		if IsDiagnostic(err, CodeNoMatchingSkills) {
			fmt.Fprintln(stderr, FormatError(err))
			return 0
		}
		fmt.Fprintf(stderr, "Error: %s\n", FormatError(err))
		return 1
	}

	reader := bufio.NewReader(stdin)
	if !opts.All && len(names) == 0 {
		if len(installed) == 0 {
			fmt.Fprintln(stdout, noSkillsMsg(opts))
			return 0
		}
		if !stdinIsTTY() {
			fmt.Fprintln(stderr, FormatError(ErrPromptRequiresTTY()))
			return 1
		}
		fmt.Fprintln(stdout, "Installed skills:")
		for i, n := range installed {
			fmt.Fprintf(stdout, "  %d) %s\n", i+1, n)
		}
		line, ok := promptLine(reader, stdout, "Select skills to remove (comma-separated numbers, e.g. 1,3): ")
		if !ok || line == "" {
			fmt.Fprintln(stdout, "Removal cancelled")
			return 0
		}
		var picked []string
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			idx, convErr := strconv.Atoi(part)
			if convErr != nil || idx < 1 || idx > len(installed) {
				fmt.Fprintf(stderr, "Invalid selection: %q\n", part)
				return 1
			}
			picked = append(picked, installed[idx-1])
		}
		selected = ResolveSkillsToRemove(picked, installed, lockKeys, nil)
	}

	if !opts.Global {
		tracked, err := FindGloballyTrackedSkills(selected, cwd)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %s\n", FormatError(err))
			return 1
		}
		if len(tracked) > 0 {
			fmt.Fprintln(stderr, FormatError(ErrRemoveGloballyTracked(tracked)))
			return 1
		}
	}
	if len(selected) == 0 {
		fmt.Fprintln(stdout, noSkillsMsg(opts))
		return 0
	}

	if !opts.Yes {
		fmt.Fprintln(stdout, "\nSkills to remove:")
		for _, s := range selected {
			fmt.Fprintf(stdout, "  %s•%s %s\n", ansiRed, ansiReset, s)
		}
		fmt.Fprintln(stdout)
		ans, ok := promptLine(reader, stdout, fmt.Sprintf("Are you sure you want to uninstall %d skill(s)? (y/N) ", len(selected)))
		if !ok || !(strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")) {
			fmt.Fprintln(stdout, "Removal cancelled")
			return 0
		}
	}

	results := RemoveInstalledSkills(selected, RemoveInstalledOptions{
		Global: opts.Global, Cwd: cwd, TargetAgents: opts.Agent, OnWarn: warn,
	})
	var ok, failed []RemoveSkillResult
	for _, r := range results {
		if r.Success {
			ok = append(ok, r)
		} else {
			failed = append(failed, r)
		}
	}
	if len(ok) > 0 {
		fmt.Fprintf(stdout, "%sSuccessfully removed %d skill(s)%s\n", ansiGreen, len(ok), ansiReset)
	}
	if len(failed) > 0 {
		fmt.Fprintf(stderr, "%sFailed to remove %d skill(s)%s\n", ansiRed, len(failed), ansiReset)
		for _, r := range failed {
			fmt.Fprintf(stderr, "  %s✗%s %s: %s\n", ansiRed, ansiReset, r.Skill, r.Error)
		}
		return 1
	}
	return 0
}

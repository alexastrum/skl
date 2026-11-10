package skl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// UpdateOptions configures the legacy single-skill UpdateSkill helper.
type UpdateOptions struct {
	Global  bool
	Cwd     string
	Verbose bool
}

// UpdateSkill refreshes an installed canonical skill in place, or replaces it with
// the contents of srcDir. Kept for library callers; `skl update` uses RunUpdate.
func UpdateSkill(name string, srcDir string, opts UpdateOptions) error {
	if name == "" {
		return errors.New("skill name is required")
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// The name is sanitized and the result confined to the canonical dir, so a
	// crafted name like "../../x" can never point RemoveAll outside it.
	base := GetCanonicalSkillsDir(opts.Global, cwd)
	skillDir := GetCanonicalPath(name, opts.Global, cwd)
	if !lcIsStrictlyWithin(base, skillDir) {
		return fmt.Errorf("refusing to update %q: resolves outside %s", name, base)
	}
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill %q is not installed", name)
	}
	if srcDir != "" {
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			return fmt.Errorf("source directory %q does not exist", srcDir)
		}
		if _, err := os.Stat(filepath.Join(srcDir, "SKILL.md")); os.IsNotExist(err) {
			return errors.New("source directory is missing SKILL.md")
		}
		if err := lcRemoveSkillDir(base, filepath.Base(skillDir)); err != nil {
			return fmt.Errorf("remove old skill: %w", err)
		}
		return CopySkillDirectory(srcDir, skillDir, "", opts.Verbose)
	}
	skillMd := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillMd)
	if err != nil {
		return fmt.Errorf("read installed skill: %w", err)
	}
	if err := os.WriteFile(skillMd, data, 0o644); err != nil {
		return fmt.Errorf("refresh skill: %w", err)
	}
	return nil
}

// UpdateMode selects between `skl update` (install) and `skl check` (report only).
type UpdateMode string

const (
	UpdateModeUpdate UpdateMode = "update"
	UpdateModeCheck  UpdateMode = "check"
)

// UpdateScope is which lock files an update covers.
type UpdateScope string

const (
	UpdateScopeProject UpdateScope = "project"
	UpdateScopeGlobal  UpdateScope = "global"
	UpdateScopeBoth    UpdateScope = "both"
)

// UpdateCheckOptions configures RunUpdate / UpdateGlobalSkills / UpdateProjectSkills.
type UpdateCheckOptions struct {
	Global  bool
	Project bool
	Yes     bool
	Verbose bool
	// Skills are optional positional skill-name filters (case-insensitive).
	Skills []string
	Cwd    string
	// ReportOutdated, when set (check mode), is called for each skill with an
	// update instead of installing it; nothing on disk or in lock files changes.
	ReportOutdated func(name string)
	Stdout         io.Writer
	Stdin          io.Reader

	term *promptTerm
}

func (o *UpdateCheckOptions) out() io.Writer {
	if o.Stdout == nil {
		return os.Stdout
	}
	return o.Stdout
}

func (o *UpdateCheckOptions) cwd() string {
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	return o.Cwd
}

// prompter returns the terminal update prompts share, reading keys from Stdin.
func (o *UpdateCheckOptions) prompter() *promptTerm {
	if o.term == nil {
		if o.Stdin == nil {
			o.Stdin = os.Stdin
		}
		o.term = newPromptTerm(o.Stdin, o.out(), stdinIsTTY())
	}
	return o.term
}

// updateFetch materializes a source for update checks (overridable in tests).
var updateFetch = FetchSource

// ParseUpdateOptions parses `skl update` / `skl check` arguments.
func ParseUpdateOptions(args []string) UpdateCheckOptions {
	var opts UpdateCheckOptions
	for _, arg := range args {
		switch arg {
		case "-g", "--global":
			opts.Global = true
		case "-p", "--project":
			opts.Project = true
		case "-y", "--yes":
			opts.Yes = true
		case "-v", "--verbose":
			opts.Verbose = true
		default:
			if arg != "" && !strings.HasPrefix(arg, "-") {
				opts.Skills = append(opts.Skills, arg)
			}
		}
	}
	return opts
}

// HasProjectSkills reports whether cwd has skills-lock.json or at least one
// .agents/skills/<dir>/SKILL.md.
func HasProjectSkills(cwd string) bool {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if _, err := os.Stat(filepath.Join(cwd, LocalLockFile)); err == nil {
		return true
	}
	dir := filepath.Join(cwd, ".agents", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
				return true
			}
		}
	}
	return false
}

// ResolveUpdateScope picks the scope from flags, auto-detects it when
// non-interactive, or prompts. ok is false when the prompt was cancelled.
func ResolveUpdateScope(opts *UpdateCheckOptions) (UpdateScope, bool) {
	if len(opts.Skills) > 0 {
		switch {
		case opts.Global:
			return UpdateScopeGlobal, true
		case opts.Project:
			return UpdateScopeProject, true
		default:
			return UpdateScopeBoth, true
		}
	}
	switch {
	case opts.Global && opts.Project:
		return UpdateScopeBoth, true
	case opts.Global:
		return UpdateScopeGlobal, true
	case opts.Project:
		return UpdateScopeProject, true
	}
	if opts.Yes || !stdinIsTTY() {
		if HasProjectSkills(opts.cwd()) {
			return UpdateScopeProject, true
		}
		return UpdateScopeGlobal, true
	}
	scope, err := opts.prompter().Select(SelectOptions{Message: "Update scope", Options: []PromptOption{
		{Value: string(UpdateScopeProject), Label: "Project", Hint: "Update skills in current directory"},
		{Value: string(UpdateScopeGlobal), Label: "Global", Hint: "Update skills in home directory"},
		{Value: string(UpdateScopeBoth), Label: "Both", Hint: "Update all skills"},
	}})
	if err != nil {
		fmt.Fprintln(opts.out(), "Cancelled")
		return "", false
	}
	return UpdateScope(scope), true
}

// WithGlobalSkillsDir drops agents without a global skills dir from a global
// reinstall, unless that would leave nothing (#2650).
func WithGlobalSkillsDir(targetAgents []AgentType) []AgentType {
	var supported []AgentType
	for _, a := range targetAgents {
		if Agents[a].GlobalSkillsDir != "" {
			supported = append(supported, a)
		}
	}
	if len(supported) > 0 {
		return supported
	}
	return targetAgents
}

// MatchesSkillFilter reports whether name matches one of filter (case-insensitive).
func MatchesSkillFilter(name string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if strings.EqualFold(f, name) {
			return true
		}
	}
	return false
}

// SkippedSkill is a global skill that cannot be checked automatically.
type SkippedSkill struct {
	Name       string
	Reason     string
	SourceURL  string
	SourceType string
	Ref        string
}

// GetSkipReason explains why a global lock entry cannot be checked.
func GetSkipReason(e SkillLockEntry) string {
	switch {
	case e.SourceType == "local":
		return "Local path"
	case e.SourceType == "git":
		return "Git URL"
	case e.SourceType == "well-known":
		return "Well-known skill"
	case e.SkillFolderHash == "":
		return "Private or deleted repo"
	case e.SkillPath == "":
		return "No skill path recorded"
	}
	return "No version tracking"
}

// GetInstallSource returns the `add` source to refresh a skipped skill manually.
func GetInstallSource(s SkippedSkill) string {
	u := s.SourceURL
	if s.SourceType == "well-known" {
		if idx := strings.Index(u, "/.well-known/"); idx != -1 {
			u = u[:idx]
		}
	}
	return FormatSourceInput(u, s.Ref)
}

func printSkippedSkills(w io.Writer, skipped []SkippedSkill) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s%d skill(s) cannot be checked automatically:%s\n", ansiDim, len(skipped), ansiReset)
	var order []string
	groups := map[string][]SkippedSkill{}
	for _, s := range skipped {
		src := GetInstallSource(s)
		if _, ok := groups[src]; !ok {
			order = append(order, src)
		}
		groups[src] = append(groups[src], s)
	}
	for _, src := range order {
		skills := groups[src]
		names := make([]string, len(skills))
		for i, s := range skills {
			names[i] = SanitizeMetadata(s.Name)
		}
		fmt.Fprintf(w, "  %s•%s %s %s(%s)%s\n", ansiText, ansiReset, strings.Join(names, ", "), ansiDim, skills[0].Reason, ansiReset)
		fmt.Fprintf(w, "    %sTo update: %sskl add %s -g -y%s\n", ansiDim, ansiText, src, ansiReset)
	}
}

// installedSkillNames returns the names of skills present on disk in a scope.
func installedSkillNames(global bool, cwd string) map[string]bool {
	out := map[string]bool{}
	skills, _ := ListInstalledSkills(ListOptions{Global: global, Cwd: cwd})
	for _, s := range skills {
		out[s.Name] = true
	}
	return out
}

// printFetching announces how many sources a check is about to fetch.
func printFetching(w io.Writer, n int) {
	if n == 0 {
		return
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	fmt.Fprintf(w, "%sFetching %d source%s...%s\n", ansiDim, n, plural, ansiReset)
}

// printCheckingSource prints the per-source progress line. Like upstream it
// returns to column 0 and clears the rest of the line.
func printCheckingSource(w io.Writer, source string) {
	fmt.Fprintf(w, "\r%sChecking skills from source: %s%s\x1b[K\n", ansiDim, source, ansiReset)
}

// fetchAllForUpdate runs updateFetch over reqs with at most FetchConcurrency()
// in flight (upstream mapConcurrent / fetchSources). Results keep input order.
func fetchAllForUpdate(reqs []SourceRequest) ([]*FetchedSource, []error) {
	out := make([]*FetchedSource, len(reqs))
	errs := make([]error, len(reqs))
	fpMapConcurrent(len(reqs), FetchConcurrency(), func(i int) {
		out[i], errs[i] = updateFetch(reqs[i])
	})
	return out, errs
}

// reinstallSkill installs skill where `add -y` would put it, keeping the recorded
// install mode (#2607) and Eve subagents. It returns per-target failures and
// whether at least one target took the skill.
func reinstallSkill(skill Skill, global bool, cwd, installMode string, subagents []string) (bool, []string) {
	agents := NonInteractiveTargetAgents(cwd)
	hasSub := len(subagents) > 0
	if hasSub && !containsAgent(agents, "eve") {
		agents = append(agents, "eve")
	}
	if global {
		agents = WithGlobalSkillsDir(agents)
	}
	var eves []string
	for _, s := range subagents {
		if s == "" {
			s = "root"
		}
		eves = append(eves, s)
	}
	targets := BuildInstallTargets(agents, eves)
	mode := DefaultInstallMode(targets)
	if installMode == string(InstallModeCopy) {
		mode = InstallModeCopy
	}
	ok := false
	var failed []string
	for _, t := range targets {
		res := InstallSkillForAgent(skill, t.Agent, InstallOptions{
			Global: global, Cwd: cwd, Mode: mode, EveSubagent: t.EveSubagent,
			CreateMissingAgentRoot: hasSub && t.Agent == "eve",
		})
		switch {
		case res.Success:
			ok = true
		case res.Skipped:
		default:
			failed = append(failed, fmt.Sprintf("%s: %s", t.Agent, res.Error))
		}
	}
	return ok, failed
}

// installFetched installs the skills named name from a fetched source (upstream
// installFetched): the fetch error when the fetch failed, "No matching skills
// found" when nothing matches, else the skills at least one target took and
// the per-target failures.
func installFetched(f *FetchedSource, fetchErr error, name string, global bool, cwd, installMode string, subagents []string) (installed []FetchedSkill, failed []string, errMsg string) {
	if fetchErr != nil || f == nil {
		return nil, nil, errText(fetchErr)
	}
	selected := FilterFetchedSkills(f.Skills, []string{name})
	if len(selected) == 0 {
		return nil, nil, "No matching skills found"
	}
	for _, s := range selected {
		ok, fl := reinstallSkill(s.Skill, global, cwd, installMode, subagents)
		for _, x := range fl {
			failed = append(failed, GetSkillDisplayName(s.Skill)+" → "+x)
		}
		if ok {
			installed = append(installed, s)
		}
	}
	return installed, failed, ""
}

func reportInstall(w io.Writer, name string, ok bool, failed []string, errMsg string) bool {
	safe := SanitizeMetadata(name)
	for _, f := range failed {
		fmt.Fprintf(w, "  %s✗ %s%s\n", ansiDim, f, ansiReset)
	}
	if ok && errMsg == "" {
		fmt.Fprintf(w, "  %s✓%s Updated %s\n", ansiText, ansiReset, safe)
		return true
	}
	suffix := ""
	if errMsg != "" {
		suffix = ": " + errMsg
	}
	fmt.Fprintf(w, "  %s✗ Failed to update %s%s%s\n", ansiDim, safe, suffix, ansiReset)
	return false
}

// promptDeletions warns about skills deleted upstream and, when interactive,
// offers to remove their local copies.
func promptDeletions(opts *UpdateCheckOptions, source string, deleted []string, global bool) {
	if len(deleted) == 0 {
		return
	}
	w := opts.out()
	// source and the names come from a lock file that may be untrusted, so
	// they are sanitized for display only.
	fmt.Fprintf(w, "\n%sWarning:%s The following skills from %s%s%s appear to have been deleted upstream:\n", ansiDim, ansiReset, ansiDim, SanitizeMetadata(source), ansiReset)
	for _, s := range deleted {
		fmt.Fprintf(w, "  %s•%s %s\n", ansiDim, ansiReset, SanitizeMetadata(s))
	}
	if opts.Yes || !stdinIsTTY() {
		fmt.Fprintf(w, "%sSkipping deletion in non-interactive mode.%s\n", ansiDim, ansiReset)
		return
	}
	confirmed, err := opts.prompter().Confirm(ConfirmOptions{Message: "Would you like to remove the local copies of these deleted skills?"})
	if err == nil && confirmed {
		for _, s := range deleted {
			fmt.Fprintf(w, "%sRemoving%s %s...\n", ansiDim, ansiReset, SanitizeMetadata(s))
			if err := RemoveSkills([]string{s}, RemoveOptions{Yes: true, Global: global}, opts.cwd()); err != nil {
				fmt.Fprintf(w, "  %s✗ %s%s\n", ansiDim, FormatError(err), ansiReset)
			}
		}
	}
}

// checkAndPromptForDeletions resolves locked skills against a fresh checkout,
// warns about ambiguous matches and offers to remove skills deleted upstream.
func checkAndPromptForDeletions(opts *UpdateCheckOptions, source string, lockedNames []string, lockedPaths map[string]string, global bool, discovered []DiscoveredSkillLocation, exact bool) SkillLocationResolution {
	w := opts.out()
	res := ResolveSkillLocations(lockedNames, lockedPaths, discovered, exact)
	if len(res.AmbiguousSkills) > 0 {
		fmt.Fprintf(w, "\n%sWarning:%s Multiple current paths match these skills from %s%s%s; skipping them rather than deleting or migrating the wrong skill:\n", ansiDim, ansiReset, ansiDim, source, ansiReset)
		for _, n := range res.AmbiguousSkills {
			fmt.Fprintf(w, "  %s•%s %s\n", ansiDim, ansiReset, SanitizeMetadata(n))
		}
	}
	promptDeletions(opts, source, res.DeletedSkills, global)
	return res
}

func discoveredLocations(f *FetchedSource) ([]DiscoveredSkillLocation, map[string]FetchedSkill) {
	locs := make([]DiscoveredSkillLocation, 0, len(f.Skills))
	byPath := map[string]FetchedSkill{}
	for _, s := range f.Skills {
		p := s.SkillPath
		if p == "" {
			p = DownloadSkillPath(f.RootDir, s.Path)
		}
		p = normalizeRelocPath(p)
		locs = append(locs, DiscoveredSkillLocation{Name: s.Name, SkillPath: p})
		byPath[p] = s
	}
	return locs, byPath
}

var gitSHA1Hex = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// downloadItem is a locked direct download and the hash it was installed at.
type downloadItem struct {
	name, url, skillPath, hash, installMode string
	subagents                               []string
}

// processDownloadUpdates re-downloads each locked URL and reinstalls skills whose
// content changed or whose folder is gone (#2682).
func processDownloadUpdates(opts *UpdateCheckOptions, items []downloadItem, global bool, record func(downloadItem, string, string) error) (success, fail, upToDate int) {
	if len(items) == 0 {
		return
	}
	w := opts.out()
	cwd := opts.cwd()
	var urls []string
	byURL := map[string][]downloadItem{}
	for _, it := range items {
		if _, ok := byURL[it.url]; !ok {
			urls = append(urls, it.url)
		}
		byURL[it.url] = append(byURL[it.url], it)
	}
	reqs := make([]SourceRequest, len(urls))
	for i, u := range urls {
		group := byURL[u]
		names := make([]string, len(group))
		for j, it := range group {
			names[j] = it.name
		}
		reqs[i] = SourceRequest{Source: u, Parsed: &ParsedSource{Type: "download", URL: u}, Skills: names, FullDepth: true, Yes: true, Cwd: cwd}
	}
	fetchedAll, errs := fetchAllForUpdate(reqs)
	onDisk := installedSkillNames(global, cwd)
	for i, u := range urls {
		group := byURL[u]
		fetched, err := fetchedAll[i], errs[i]
		printCheckingSource(w, u)
		if err != nil || fetched == nil {
			_ = fetched.Cleanup()
			fail += len(group)
			fmt.Fprintf(w, "  %s✗ Failed to check skills from %s: %s%s\n", ansiDim, u, errText(err), ansiReset)
			continue
		}
		for _, it := range group {
			skill, ok := findDownloadedSkill(fetched, it)
			if !ok {
				fail++
				fmt.Fprintf(w, "  %s✗ %s is no longer in this download%s\n", ansiDim, SanitizeMetadata(it.name), ansiReset)
				continue
			}
			hash, err := ComputeSkillFolderHash(skill.Path)
			if err != nil {
				fail++
				fmt.Fprintf(w, "  %s✗ Failed to hash %s: %v%s\n", ansiDim, SanitizeMetadata(it.name), err, ansiReset)
				continue
			}
			if hash == it.hash && onDisk[it.name] {
				upToDate++
				continue
			}
			if opts.ReportOutdated != nil {
				opts.ReportOutdated(it.name)
				continue
			}
			fmt.Fprintf(w, "%sUpdating %s...%s\n", ansiText, SanitizeMetadata(it.name), ansiReset)
			subs := it.subagents
			if global {
				subs = nil
			}
			ok2, failed := reinstallSkill(skill.Skill, global, cwd, it.installMode, subs)
			errMsg := ""
			if ok2 {
				if err := record(it, DownloadSkillPath(fetched.RootDir, skill.Path), hash); err != nil {
					errMsg = err.Error()
				}
			}
			if reportInstall(w, it.name, ok2, failed, errMsg) {
				success++
			} else {
				fail++
			}
		}
		_ = fetched.Cleanup()
	}
	return
}

func findDownloadedSkill(f *FetchedSource, it downloadItem) (FetchedSkill, bool) {
	var first *FetchedSkill
	for i := range f.Skills {
		s := f.Skills[i]
		if s.Name != it.name {
			continue
		}
		if DownloadSkillPath(f.RootDir, s.Path) == it.skillPath || (s.SkillPath != "" && s.SkillPath == it.skillPath) {
			return s, true
		}
		if first == nil {
			first = &f.Skills[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return FetchedSkill{}, false
}

func errText(err error) string {
	if err == nil {
		return "no skills found"
	}
	return FormatError(err)
}

// wellKnownItem is a locked well-known skill and its recorded digest.
type wellKnownItem struct {
	name, digest string
	subagents    []string
}

// wellKnownCheck is the outcome of checkWellKnownForUpdates.
type wellKnownCheck struct {
	// status is "error", "current" or "changed".
	status                      string
	changed, removed, newSkills []string
}

// wellKnownUpdateCheckHeaders marks index requests made by update checks.
var wellKnownUpdateCheckHeaders = map[string]string{"X-Skills-Update-Check": "1"}

// checkWellKnownForUpdates compares locked well-known skills with the source's
// index. v0.2.0 entries compare the index digest without downloading the
// skill; v0.1.0 entries are fetched and their content digest compared.
func checkWellKnownForUpdates(baseURL string, items []wellKnownItem) wellKnownCheck {
	candidates := fetchWellKnownCandidatesWithHeaders(baseURL, wellKnownUpdateCheckHeaders)
	if len(candidates) == 0 {
		return wellKnownCheck{status: "error"}
	}
	entries := candidates[0].entries
	byName := map[string]wellKnownEntry{}
	for _, e := range entries {
		byName[e.name] = e
	}
	var res wellKnownCheck
	local := map[string]bool{}
	for _, it := range items {
		local[it.name] = true
		if _, ok := byName[it.name]; !ok {
			res.removed = append(res.removed, it.name)
		}
	}
	for _, e := range entries {
		if !local[e.name] {
			res.newSkills = append(res.newSkills, e.name)
		}
	}
	var needsContent []wellKnownItem
	for _, it := range items {
		e, ok := byName[it.name]
		if !ok {
			continue
		}
		if e.version == "0.2.0" {
			if it.digest == "" || e.digest != it.digest {
				res.changed = append(res.changed, it.name)
			}
		} else {
			needsContent = append(needsContent, it)
		}
	}
	if len(needsContent) > 0 {
		tracked := map[string]bool{}
		for _, it := range needsContent {
			tracked[it.name] = true
		}
		var toFetch []wellKnownEntry
		for _, e := range entries {
			if tracked[e.name] {
				toFetch = append(toFetch, e)
			}
		}
		skills := make([]*WellKnownSkill, len(toFetch))
		fpMapConcurrent(len(toFetch), 8, func(i int) {
			if s, err := fetchWellKnownEntry(toFetch[i]); err == nil {
				skills[i] = s
			}
		})
		digests := map[string]string{}
		fetched := 0
		for _, s := range skills {
			if s != nil {
				fetched++
				digests[s.InstallName] = ComputeWellKnownSkillDigest(s)
			}
		}
		if fetched == 0 {
			return wellKnownCheck{status: "error"}
		}
		for _, it := range needsContent {
			if d, ok := digests[it.name]; !ok || it.digest == "" || d != it.digest {
				res.changed = append(res.changed, it.name)
			}
		}
	}
	if len(res.changed) == 0 && len(res.removed) == 0 {
		res.status = "current"
	} else {
		res.status = "changed"
	}
	return res
}

func printNewSkills(w io.Writer, baseURL string, newSkills []string, global bool) {
	if len(newSkills) == 0 {
		return
	}
	names := make([]string, len(newSkills))
	for i, n := range newSkills {
		names[i] = SanitizeMetadata(n)
	}
	flag := ""
	if global {
		flag = " -g"
	}
	fmt.Fprintf(w, "  %s%d new skill(s) available from this source:%s %s\n", ansiDim, len(newSkills), ansiReset, strings.Join(names, ", "))
	fmt.Fprintf(w, "    %sTo install: %sskl add %s --skill %s%s%s\n", ansiDim, ansiText, baseURL, strings.Join(names, " "), flag, ansiReset)
}

// processWellKnownUpdates re-reads each well-known index (bases in lock order)
// and reinstalls skills whose digest changed. changed is true when a reachable
// index differs from the lock. record writes the lock entry of each installed
// skill.
func processWellKnownUpdates(opts *UpdateCheckOptions, bases []string, groups map[string][]wellKnownItem, global bool, record func(baseURL string, f *FetchedSource, s FetchedSkill) error) (success, fail int, changed bool) {
	w := opts.out()
	cwd := opts.cwd()
	printFetching(w, len(bases))
	results := make([]wellKnownCheck, len(bases))
	fpMapConcurrent(len(bases), FetchConcurrency(), func(i int) {
		results[i] = checkWellKnownForUpdates(bases[i], groups[bases[i]])
	})

	type pendingSource struct {
		base    string
		items   []wellKnownItem
		changed []string
	}
	var pending []pendingSource
	for i, base := range bases {
		printCheckingSource(w, base)
		r := results[i]
		switch r.status {
		case "error":
			fmt.Fprintf(w, "  %s✗ Failed to check skills from %s%s\n", ansiDim, base, ansiReset)
			continue
		case "current":
			printNewSkills(w, base, r.newSkills, global)
			continue
		}
		changed = true
		promptDeletions(opts, base, r.removed, global)
		printNewSkills(w, base, r.newSkills, global)
		if len(r.changed) > 0 {
			if opts.ReportOutdated != nil {
				for _, name := range r.changed {
					opts.ReportOutdated(name)
				}
			} else {
				pending = append(pending, pendingSource{base, groups[base], r.changed})
			}
		}
	}

	reqs := make([]SourceRequest, len(pending))
	for i, p := range pending {
		reqs[i] = SourceRequest{Source: p.base, Skills: p.changed, Yes: true, Cwd: cwd}
	}
	fetched, errs := fetchAllForUpdate(reqs)
	for i, p := range pending {
		itemByName := map[string]wellKnownItem{}
		for _, it := range p.items {
			itemByName[it.name] = it
		}
		// One install per skill: each may record different Eve subagents.
		for _, name := range p.changed {
			fmt.Fprintf(w, "%sUpdating %s...%s\n", ansiText, SanitizeMetadata(name), ansiReset)
			var subs []string
			if !global {
				subs = itemByName[name].subagents
			}
			installed, failed, errMsg := installFetched(fetched[i], errs[i], name, global, cwd, "", subs)
			for _, s := range installed {
				if err := record(p.base, fetched[i], s); err != nil && errMsg == "" {
					errMsg = err.Error()
				}
			}
			if reportInstall(w, name, len(installed) > 0, failed, errMsg) {
				success++
			} else {
				fail++
			}
		}
		_ = fetched[i].Cleanup()
	}
	return
}

type globalUpdate struct {
	name  string
	entry SkillLockEntry
	hash  string
}

// globalCheckout is the network half of a global source check: a GitHub tree,
// a fresh checkout (with an optional note saying why it was cloned), or failed.
type globalCheckout struct {
	tree    *RepoTree
	fetched *FetchedSource
	note    string
	failed  bool
}

// fetchGlobalSource checks GitHub sources through the Trees API unless a locked
// skill path of the (source, ref) group is not a file in the tree (a rename, or
// a truncated tree), in which case the repo is cloned to relocate it. The clone
// is also the fallback when the API is unavailable (rate limit without auth,
// private repo without a working token, network error). Other sources are
// always cloned.
func fetchGlobalSource(e SkillLockEntry, lockedSkillPaths []string, cwd string) globalCheckout {
	note := ""
	if e.SourceType == "github" {
		tree, _ := FetchRepoTree(e.Source, e.Ref, GetGitHubToken)
		if tree != nil {
			blobs := map[string]bool{}
			for _, te := range tree.Tree {
				if te.Type == "blob" {
					blobs[te.Path] = true
				}
			}
			all := true
			for _, p := range lockedSkillPaths {
				if p != "" && !blobs[p] {
					all = false
					break
				}
			}
			if all {
				return globalCheckout{tree: tree}
			}
			note = "Skill paths changed; resolving via Git clone"
		} else {
			note = "GitHub API unavailable; checking via Git clone"
		}
	}
	cloneURL := nonEmpty(e.SourceUrl, e.Source)
	host := ""
	if e.SourceType == "github" {
		// Pin public GitHub shorthands so an ambient GH_HOST cannot redirect them.
		host = "github.com"
		if e.SourceUrl == "" && isBareShorthand(e.Source) {
			cloneURL = "https://github.com/" + strings.TrimSuffix(e.Source, ".git") + ".git"
		}
	}
	fetched, err := updateFetch(SourceRequest{Source: FormatSourceInput(cloneURL, e.Ref), Skills: []string{"*"}, FullDepth: true, IncludeDuplicateNames: true, NoBlob: true, Yes: true, Cwd: cwd, GitHubHost: host})
	if err != nil || fetched == nil {
		_ = fetched.Cleanup()
		return globalCheckout{failed: true}
	}
	return globalCheckout{fetched: fetched, note: note}
}

// UpdateGlobalSkills checks (and unless in check mode, reinstalls) skills tracked
// in the global lock.
func UpdateGlobalSkills(opts *UpdateCheckOptions) (success, fail, checked int) {
	w := opts.out()
	cwd := opts.cwd()
	lock, _ := ReadSkillLock()
	if len(lock.Skills) == 0 {
		if len(opts.Skills) == 0 {
			fmt.Fprintf(w, "%sNo global skills tracked in lock file.%s\n", ansiDim, ansiReset)
			fmt.Fprintf(w, "%sInstall skills with%s %sskl add <package> -g%s\n", ansiDim, ansiReset, ansiText, ansiReset)
		}
		return
	}
	// Lock file order, as upstream iterates Object.keys(lock.skills).
	names := lock.orderedSkillNames()

	var skipped []SkippedSkill
	var checkable []string
	var wkBases []string
	wkGroups := map[string][]wellKnownItem{}
	var downloads []downloadItem
	for _, name := range names {
		if !MatchesSkillFilter(name, opts.Skills) {
			continue
		}
		e := lock.Skills[name]
		switch {
		case e.SourceType == "well-known" && e.SourceBaseUrl != "" && e.WellKnownDigest != "":
			if _, ok := wkGroups[e.SourceBaseUrl]; !ok {
				wkBases = append(wkBases, e.SourceBaseUrl)
			}
			wkGroups[e.SourceBaseUrl] = append(wkGroups[e.SourceBaseUrl], wellKnownItem{name: name, digest: e.WellKnownDigest})
		case e.SourceType == "download" && e.SourceUrl != "":
			downloads = append(downloads, downloadItem{name: name, url: e.SourceUrl, skillPath: e.SkillPath, hash: e.SkillFolderHash, installMode: e.InstallMode})
		case e.SkillFolderHash == "" || e.SkillPath == "":
			skipped = append(skipped, SkippedSkill{Name: name, Reason: GetSkipReason(e), SourceURL: e.SourceUrl, SourceType: e.SourceType, Ref: e.Ref})
		default:
			checkable = append(checkable, name)
		}
	}

	wkCount := 0
	for _, g := range wkGroups {
		wkCount += len(g)
	}
	s1, f1, wkChanged := processWellKnownUpdates(opts, wkBases, wkGroups, true, func(base string, _ *FetchedSource, s FetchedSkill) error {
		if s.WellKnownDigest == "" {
			return nil
		}
		// Upstream globalWellKnownLockEntry: a fresh entry for the index.
		return AddSkillToLock(s.Name, SkillLockEntry{
			Source: GetWellKnownSourceIdentifier(base), SourceType: "well-known", SourceUrl: s.SourceURL,
			SourceBaseUrl: base, WellKnownDigest: s.WellKnownDigest,
		})
	})
	s2, f2, dlUpToDate := processDownloadUpdates(opts, downloads, true, func(it downloadItem, skillPath, hash string) error {
		e := lock.Skills[it.name]
		e.SkillPath, e.SkillFolderHash = skillPath, hash
		return AddSkillToLock(it.name, e)
	})
	success += s1 + s2
	fail += f1 + f2
	otherCount := wkCount + len(downloads)
	otherChanged := wkChanged || dlUpToDate < len(downloads)

	// Group by source AND ref so skills pinned to different refs of one repo are
	// each checked against their own ref (#2682).
	var groupKeys []string
	groups := map[string][]string{}
	for _, name := range checkable {
		e := lock.Skills[name]
		key := e.Source + "\n" + e.Ref
		if _, ok := groups[key]; !ok {
			groupKeys = append(groupKeys, key)
		}
		groups[key] = append(groups[key], name)
	}
	lockedFor := func(first SkillLockEntry) ([]string, map[string]string) {
		var lockedNames []string
		lockedPaths := map[string]string{}
		for _, n := range names {
			if e := lock.Skills[n]; e.Source == first.Source && e.Ref == first.Ref {
				lockedNames = append(lockedNames, n)
				lockedPaths[n] = e.SkillPath
			}
		}
		return lockedNames, lockedPaths
	}

	onDisk := installedSkillNames(true, cwd)
	printFetching(w, len(groupKeys))
	checkouts := make([]globalCheckout, len(groupKeys))
	fpMapConcurrent(len(groupKeys), FetchConcurrency(), func(i int) {
		first := lock.Skills[groups[groupKeys[i]][0]]
		lockedNames, lockedPaths := lockedFor(first)
		paths := make([]string, len(lockedNames))
		for j, n := range lockedNames {
			paths[j] = lockedPaths[n]
		}
		checkouts[i] = fetchGlobalSource(first, paths, cwd)
	})

	var updates []globalUpdate
	for i, key := range groupKeys {
		items := groups[key]
		first := lock.Skills[items[0]]
		source := first.Source
		co := checkouts[i]
		printCheckingSource(w, source)
		if co.failed {
			fmt.Fprintf(w, "  %s✗ Failed to check skills from %s%s\n", ansiDim, source, ansiReset)
			continue
		}
		if co.tree != nil {
			for _, name := range items {
				e := lock.Skills[name]
				latest := GetSkillFolderHashFromTree(co.tree, e.SkillPath)
				if latest != "" && (latest != e.SkillFolderHash || !onDisk[name]) {
					updates = append(updates, globalUpdate{name: name, entry: e, hash: latest})
				}
			}
			continue
		}
		if co.note != "" {
			fmt.Fprintf(w, "  %s%s%s\n", ansiDim, co.note, ansiReset)
		}
		fetched := co.fetched
		lockedNames, lockedPaths := lockedFor(first)
		locs, byPath := discoveredLocations(fetched)
		res := checkAndPromptForDeletions(opts, source, lockedNames, lockedPaths, true, locs, !ShouldUseFullDepthForUpdate(UpdateSourceFromGlobal(first)))
		deleted := map[string]bool{}
		for _, d := range res.DeletedSkills {
			deleted[d] = true
		}
		for _, name := range items {
			if deleted[name] {
				continue
			}
			e := lock.Skills[name]
			skillPath, ok := res.ResolvedPaths[name]
			if !ok {
				continue
			}
			current, ok := byPath[skillPath]
			if !ok {
				continue
			}
			var latest string
			if first.SourceType == "github" && gitSHA1Hex.MatchString(e.SkillFolderHash) {
				if current.FolderHash != "" {
					latest = current.FolderHash
				} else if latest = GetGitRepoTreeHash(fetched.RootDir, skillPath); latest == "" {
					latest, _ = ComputeGitTreeHash(current.Path)
				}
			} else {
				latest, _ = ComputeSkillFolderHash(current.Path)
			}
			relocated := skillPath != e.SkillPath
			if relocated || (latest != "" && (latest != e.SkillFolderHash || !onDisk[name])) {
				e.SkillPath = skillPath
				updates = append(updates, globalUpdate{name: name, entry: e, hash: latest})
			}
		}
		_ = fetched.Cleanup()
	}
	if len(checkable) > 0 {
		fmt.Fprint(w, "\r\x1b[K")
	}

	checked = len(checkable) + len(skipped) + otherCount
	switch {
	case len(checkable) == 0 && len(skipped) == 0 && otherCount == 0:
		if len(opts.Skills) == 0 {
			fmt.Fprintf(w, "%sNo global skills to check.%s\n", ansiDim, ansiReset)
		}
		return success, fail, 0
	case len(checkable) == 0 && len(skipped) == 0:
		if !otherChanged {
			fmt.Fprintf(w, "%s✓ All global skills are up to date%s\n", ansiText, ansiReset)
		}
		return
	case len(checkable) == 0:
		printSkippedSkills(w, skipped)
		return
	case len(updates) == 0:
		// Upstream returns here without listing the skipped skills.
		if !otherChanged {
			fmt.Fprintf(w, "%s✓ All global skills are up to date%s\n", ansiText, ansiReset)
		}
		return
	}
	if opts.ReportOutdated != nil {
		for _, u := range updates {
			opts.ReportOutdated(u.name)
		}
		printSkippedSkills(w, skipped)
		return
	}

	fmt.Fprintf(w, "%sFound %d global update(s)%s\n\n", ansiText, len(updates), ansiReset)
	// Each update is fetched on its own, with its skill folder as the subpath,
	// as `add` would, so eligible GitHub repos take the blob fast path.
	type installableUpdate struct {
		globalUpdate
		req SourceRequest
	}
	var installable []installableUpdate
	for _, u := range updates {
		src := UpdateSourceFromGlobal(u.entry)
		installURL := BuildUpdateInstallSource(src)
		if installURL == "" {
			fail++
			fmt.Fprintf(w, "  %s✗ Cannot update %s: lock file is missing sourceUrl for this generic Git source%s\n", ansiDim, SanitizeMetadata(u.name), ansiReset)
			continue
		}
		host := ""
		if u.entry.SourceType == "github" {
			host = "github.com"
		}
		installable = append(installable, installableUpdate{u, SourceRequest{
			Source: installURL, Skills: []string{u.name}, FullDepth: ShouldUseFullDepthForUpdate(src), Yes: true, Cwd: cwd, GitHubHost: host,
		}})
	}
	reqs := make([]SourceRequest, len(installable))
	for i, u := range installable {
		reqs[i] = u.req
	}
	fetched, errs := fetchAllForUpdate(reqs)
	for i, u := range installable {
		fmt.Fprintf(w, "%sUpdating %s...%s\n", ansiText, SanitizeMetadata(u.name), ansiReset)
		installed, failed, errMsg := installFetched(fetched[i], errs[i], u.name, true, cwd, u.entry.InstallMode, nil)
		_ = fetched[i].Cleanup()
		if len(installed) > 0 {
			e := u.entry
			e.SkillFolderHash = u.hash
			if err := AddSkillToLock(u.name, e); err != nil {
				errMsg = err.Error()
			}
		}
		if reportInstall(w, u.name, len(installed) > 0, failed, errMsg) {
			success++
		} else {
			fail++
		}
	}
	printSkippedSkills(w, skipped)
	return
}

type projectSkill struct {
	name  string
	entry LocalSkillLockEntry
}

// UpdateProjectSkills checks (and unless in check mode, reinstalls) skills tracked
// in skills-lock.json. It returns SKILLS_INVALID_LOCAL_LOCK errors.
func UpdateProjectSkills(opts *UpdateCheckOptions) (success, fail, found int, err error) {
	w := opts.out()
	cwd := opts.cwd()
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return 0, 0, 0, err
	}
	var skills []projectSkill
	for _, name := range keysOf(lock.Skills) {
		e := lock.Skills[name]
		if !MatchesSkillFilter(name, opts.Skills) {
			continue
		}
		if e.SourceType == "node_modules" || e.SourceType == "npm" || e.SourceType == "local" {
			continue
		}
		skills = append(skills, projectSkill{name, e})
	}
	if len(skills) == 0 {
		if len(opts.Skills) == 0 {
			fmt.Fprintf(w, "%sNo project skills to update.%s\n", ansiDim, ansiReset)
			fmt.Fprintf(w, "%sInstall project skills with%s %sskl add <package>%s\n", ansiDim, ansiReset, ansiText, ansiReset)
		}
		return 0, 0, 0, nil
	}
	found = len(skills)

	var wkBases []string
	wkGroups := map[string][]wellKnownItem{}
	var downloads []downloadItem
	var updatable, legacy []projectSkill
	for _, s := range skills {
		e := s.entry
		switch {
		case e.SourceType == "download" && e.SourceUrl != "":
			downloads = append(downloads, downloadItem{name: s.name, url: e.SourceUrl, skillPath: e.SkillPath, hash: e.ComputedHash, installMode: e.InstallMode, subagents: e.Subagents})
		case e.SourceType == "well-known" && e.SourceUrl != "" && e.WellKnownDigest != "":
			if _, ok := wkGroups[e.SourceUrl]; !ok {
				wkBases = append(wkBases, e.SourceUrl)
			}
			wkGroups[e.SourceUrl] = append(wkGroups[e.SourceUrl], wellKnownItem{name: s.name, digest: e.WellKnownDigest, subagents: e.Subagents})
		case e.SkillPath != "":
			updatable = append(updatable, s)
		default:
			legacy = append(legacy, s)
		}
	}
	wkCount := 0
	for _, g := range wkGroups {
		wkCount += len(g)
	}
	if len(updatable) == 0 && wkCount == 0 && len(downloads) == 0 {
		fmt.Fprintf(w, "%sNo project skills can be updated in place.%s\n", ansiDim, ansiReset)
		printLegacyProjectSkills(w, legacy)
		return
	}

	var targetNames []string
	hasUniversal := false
	for _, k := range orderedAgentTypes(Agents) {
		cfg := Agents[k]
		if IsUniversalAgent(k) {
			if _, err := os.Stat(filepath.Join(cwd, ".agents")); err == nil {
				hasUniversal = true
			}
			continue
		}
		root := strings.Split(filepath.ToSlash(cfg.SkillsDir), "/")[0]
		if _, err := os.Stat(filepath.Join(cwd, root)); err == nil {
			targetNames = append(targetNames, cfg.DisplayName)
		}
	}
	if hasUniversal {
		targetNames = append([]string{"Universal"}, targetNames...)
	}
	if len(targetNames) > 0 {
		fmt.Fprintf(w, "%sUpdating for: %s%s\n", ansiText, strings.Join(targetNames, ", "), ansiReset)
	}
	fmt.Fprintf(w, "%sChecking %d skill(s)...%s\n\n", ansiText, len(updatable)+wkCount+len(downloads), ansiReset)

	recordLocal := func(name string, mutate func(*LocalSkillLockEntry)) error {
		e := lock.Skills[name]
		mutate(&e)
		return AddSkillToLocalLock(name, e, cwd)
	}
	wkS, wkF, _ := processWellKnownUpdates(opts, wkBases, wkGroups, false, func(base string, f *FetchedSource, s FetchedSkill) error {
		if s.WellKnownDigest == "" {
			return nil
		}
		// Hash the skill as installed (metadata.json and caches excluded), as
		// add and install --frozen-lockfile do, so the lock does not drift.
		h, _ := addStagedCopyHash(s.Path)
		u := nonEmpty(f.Parsed.URL, base)
		// Upstream recordProjectLock -> wellKnownLockEntry: a fresh entry.
		return AddSkillToLocalLock(s.Name, LocalSkillLockEntry{
			Source: GetWellKnownSourceIdentifier(u), SourceUrl: u, SourceType: "well-known",
			ComputedHash: h, WellKnownDigest: s.WellKnownDigest,
		}, cwd)
	})
	success += wkS
	fail += wkF
	dlS, dlF, upToDate := processDownloadUpdates(opts, downloads, false, func(it downloadItem, skillPath, hash string) error {
		return recordLocal(it.name, func(e *LocalSkillLockEntry) { e.SkillPath, e.ComputedHash = skillPath, hash })
	})
	success += dlS
	fail += dlF

	// Key by source AND ref so skills from one repo pinned to different refs are
	// each checked against their own ref's tree.
	var groupKeys []string
	groups := map[string][]projectSkill{}
	for _, s := range updatable {
		src := nonEmpty(s.entry.SourceUrl, s.entry.Source)
		key := src + "\n" + s.entry.Ref
		if _, ok := groups[key]; !ok {
			groupKeys = append(groupKeys, key)
		}
		groups[key] = append(groups[key], s)
	}
	// Re-read the lock: the well-known and download updates above may have
	// changed it.
	latestLock := lock
	if l, err := ReadLocalLock(cwd); err == nil {
		latestLock = l
	}
	onDisk := installedSkillNames(false, cwd)

	cloneSources := make([]string, len(groupKeys))
	toFetch := 0
	for i, key := range groupKeys {
		cloneSources[i] = BuildLocalCloneSource(UpdateSourceFromLocal(groups[key][0].entry))
		if cloneSources[i] != "" {
			toFetch++
		}
	}
	printFetching(w, toFetch)
	clones := make([]*FetchedSource, len(groupKeys))
	cloneErrs := make([]error, len(groupKeys))
	fpMapConcurrent(len(groupKeys), FetchConcurrency(), func(i int) {
		if cloneSources[i] == "" {
			return
		}
		first := groups[groupKeys[i]][0].entry
		clones[i], cloneErrs[i] = updateFetch(SourceRequest{Source: FormatSourceInput(cloneSources[i], first.Ref), Skills: []string{"*"}, FullDepth: true, IncludeDuplicateNames: true, NoBlob: true, Yes: true, Cwd: cwd})
	})

	for i, key := range groupKeys {
		group := groups[key]
		first := group[0].entry
		source := nonEmpty(first.SourceUrl, first.Source)
		var lockedNames []string
		lockedPaths := map[string]string{}
		for _, n := range keysOf(latestLock.Skills) {
			if e := latestLock.Skills[n]; nonEmpty(e.SourceUrl, e.Source) == source && e.Ref == first.Ref {
				lockedNames = append(lockedNames, n)
				lockedPaths[n] = e.SkillPath
			}
		}
		if cloneSources[i] == "" {
			fail += len(group)
			fmt.Fprintf(w, "%s✗ Cannot update %s: skills-lock.json is missing sourceUrl for this generic Git source%s\n", ansiDim, source, ansiReset)
			continue
		}
		fetched := clones[i]
		if cloneErrs[i] != nil || fetched == nil {
			_ = fetched.Cleanup()
			fail += len(group)
			fmt.Fprintf(w, "%s✗ Failed to check for deleted skills from %s%s\n", ansiDim, source, ansiReset)
			continue
		}
		locs, byPath := discoveredLocations(fetched)
		res := checkAndPromptForDeletions(opts, source, lockedNames, lockedPaths, false, locs, !ShouldUseFullDepthForUpdate(UpdateSourceFromLocal(first)))
		deleted := map[string]bool{}
		for _, d := range res.DeletedSkills {
			deleted[d] = true
		}
		for _, s := range group {
			if deleted[s.name] {
				continue
			}
			skillPath, ok := res.ResolvedPaths[s.name]
			if !ok {
				continue
			}
			current, ok := byPath[skillPath]
			if !ok {
				continue
			}
			hash, _ := ComputeSkillFolderHash(current.Path)
			if skillPath == s.entry.SkillPath && hash == s.entry.ComputedHash && onDisk[s.name] {
				upToDate++
				continue
			}
			if opts.ReportOutdated != nil {
				opts.ReportOutdated(s.name)
				continue
			}
			fmt.Fprintf(w, "%sUpdating %s...%s\n", ansiText, SanitizeMetadata(s.name), ansiReset)
			ok, failed := reinstallSkill(current.Skill, false, cwd, s.entry.InstallMode, s.entry.Subagents)
			errMsg := ""
			if ok && hash != "" {
				if err := recordLocal(s.name, func(e *LocalSkillLockEntry) { e.SkillPath, e.ComputedHash = skillPath, hash }); err != nil {
					errMsg = err.Error()
				}
			}
			if reportInstall(w, s.name, ok, failed, errMsg) {
				success++
			} else {
				fail++
			}
		}
		_ = fetched.Cleanup()
	}

	if upToDate > 0 {
		if upToDate == len(updatable)+len(downloads) && wkS == 0 && wkF == 0 {
			fmt.Fprintf(w, "%s✓ All project skills are up to date%s\n", ansiText, ansiReset)
		} else {
			fmt.Fprintf(w, "%s%d project skill(s) already up to date%s\n", ansiDim, upToDate, ansiReset)
		}
	}
	printLegacyProjectSkills(w, legacy)
	return
}

func printLegacyProjectSkills(w io.Writer, legacy []projectSkill) {
	if len(legacy) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s%d project skill(s) cannot be updated automatically (installed before skillPath tracking):%s\n", ansiDim, len(legacy), ansiReset)
	for _, s := range legacy {
		fmt.Fprintf(w, "  %s•%s %s\n", ansiText, ansiReset, SanitizeMetadata(s.name))
		if src := BuildLocalUpdateSource(UpdateSourceFromLocal(s.entry)); src != "" {
			fmt.Fprintf(w, "    %sTo refresh: %sskl add %s -y%s\n", ansiDim, ansiText, src, ansiReset)
		} else {
			fmt.Fprintf(w, "    %sTo refresh: reinstall using the original full Git URL; this lock entry only has an ambiguous shorthand.%s\n", ansiDim, ansiReset)
		}
	}
}

// RunUpdate implements `skl update` (mode update) and `skl check` (mode check),
// returning the process exit code. check never installs anything and exits 1
// when any skill has an update (#2605).
func RunUpdate(args []string, mode UpdateMode, cwd string) int {
	return runUpdate(args, mode, cwd, os.Stdout, os.Stderr, os.Stdin)
}

func runUpdate(args []string, mode UpdateMode, cwd string, stdout, stderr io.Writer, stdin io.Reader) int {
	opts := ParseUpdateOptions(args)
	opts.Cwd, opts.Stdout, opts.Stdin = cwd, stdout, stdin
	opts.cwd()
	outdated := 0
	if mode == UpdateModeCheck {
		opts.Yes = true
		opts.ReportOutdated = func(name string) {
			outdated++
			fmt.Fprintf(stdout, "%s↑ %s has an update%s\n", ansiText, SanitizeMetadata(name), ansiReset)
		}
	}
	scope, ok := ResolveUpdateScope(&opts)
	if !ok {
		return 0
	}
	if len(opts.Skills) > 0 && mode == UpdateModeUpdate {
		fmt.Fprintf(stdout, "%sUpdating %s...%s\n\n", ansiText, strings.Join(opts.Skills, ", "), ansiReset)
	} else {
		fmt.Fprintf(stdout, "%sChecking for skill updates...%s\n\n", ansiText, ansiReset)
	}

	totalSuccess, totalFail, totalFound := 0, 0, 0
	both := scope == UpdateScopeBoth && len(opts.Skills) == 0
	if scope == UpdateScopeGlobal || scope == UpdateScopeBoth {
		if both {
			fmt.Fprintf(stdout, "%sGlobal Skills%s\n", ansiBold, ansiReset)
		}
		s, f, c := UpdateGlobalSkills(&opts)
		totalSuccess, totalFail, totalFound = totalSuccess+s, totalFail+f, totalFound+c
		if both {
			fmt.Fprintln(stdout)
		}
	}
	if scope == UpdateScopeProject || scope == UpdateScopeBoth {
		if both {
			fmt.Fprintf(stdout, "%sProject Skills%s\n", ansiBold, ansiReset)
		}
		s, f, c, err := UpdateProjectSkills(&opts)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %s\n", FormatError(err))
			return 1
		}
		totalSuccess, totalFail, totalFound = totalSuccess+s, totalFail+f, totalFound+c
	}

	if len(opts.Skills) > 0 && totalFound == 0 {
		fmt.Fprintf(stdout, "%sNo installed skills found matching: %s%s\n", ansiDim, strings.Join(opts.Skills, ", "), ansiReset)
	}
	fmt.Fprintln(stdout)
	code := 0
	if totalSuccess > 0 {
		fmt.Fprintf(stdout, "%s✓ Updated %d skill(s)%s\n", ansiText, totalSuccess, ansiReset)
	}
	if totalFail > 0 {
		fmt.Fprintf(stdout, "%sFailed to update %d skill(s)%s\n", ansiDim, totalFail, ansiReset)
		code = 1
	}
	if outdated > 0 {
		fmt.Fprintf(stdout, "%s%d skill(s) can be updated; run %sskl update%s%s to install%s\n", ansiText, outdated, ansiBold, ansiReset, ansiText, ansiReset)
		code = 1
	}
	fmt.Fprintln(stdout)
	return code
}

package skl

// add.go ports upstream src/add.ts: `skl add` option parsing, source fetching via the
// FetchSource contract, target agent resolution, installation, lock recording, and
// --json output. AddSkill remains as the backward-compatible programmatic API.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ParsedAddCLI is the result of parsing `skl add` arguments.
type ParsedAddCLI struct {
	Sources []string
	Options AddOptions
	// ExplicitAgentSet holds agents named via -a/--agent (excluding '*').
	ExplicitAgentSet map[AgentType]bool
	// AgentFlagPassed is true when -a/--agent appeared on the command line.
	AgentFlagPassed bool
	// ScopeExplicit is true when -g/--global or -p/--project was passed.
	ScopeExplicit bool
	Errors        []error
}

// AddIO overrides the streams and environment used by RunAddWithIO (for tests and embedding).
type AddIO struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Cwd is the project directory (defaults to os.Getwd()).
	Cwd string
	// Interactive overrides TTY detection on stdin.
	Interactive *bool
}

// AddJSONResult is one entry of `skl add --json` output.
type AddJSONResult struct {
	Name     string      `json:"name,omitempty"`
	Status   string      `json:"status"` // installed | skipped | failed
	Source   string      `json:"source,omitempty"`
	Ref      *string     `json:"ref"`
	Hash     *string     `json:"hash"`
	Path     string      `json:"path,omitempty"`
	Scope    string      `json:"scope,omitempty"`
	Agents   []string    `json:"agents,omitempty"`
	Mode     InstallMode `json:"mode,omitempty"`
	Security interface{} `json:"security"`
	Reason   string      `json:"reason,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// MarshalJSON emits the upstream shapes: installed entries carry explicit nulls for
// ref/hash/security, skipped and failed entries only their own fields.
func (r AddJSONResult) MarshalJSON() ([]byte, error) {
	if r.Status == "installed" {
		type installed struct {
			Name     string      `json:"name,omitempty"`
			Status   string      `json:"status"`
			Source   string      `json:"source,omitempty"`
			Ref      *string     `json:"ref"`
			Hash     *string     `json:"hash"`
			Path     string      `json:"path,omitempty"`
			Scope    string      `json:"scope,omitempty"`
			Agents   []string    `json:"agents"`
			Mode     InstallMode `json:"mode,omitempty"`
			Security interface{} `json:"security"`
		}
		agents := r.Agents
		if agents == nil {
			agents = []string{}
		}
		return json.Marshal(installed{r.Name, r.Status, r.Source, r.Ref, r.Hash, r.Path, r.Scope, agents, r.Mode, r.Security})
	}
	type other struct {
		Name   string `json:"name,omitempty"`
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	return json.Marshal(other{r.Name, r.Status, r.Reason, r.Error})
}

// addDetectAgent is overridable in tests.
var addDetectAgent = DetectAgent

// fetchSourceFn is the fetch entry point used by add and use; overridable in tests.
var fetchSourceFn = fetchSourceChecked

func fetchSourceChecked(req SourceRequest) (*FetchedSource, error) {
	f, err := FetchSource(req)
	if err == nil && f == nil {
		err = fmt.Errorf("could not fetch %s", req.Source)
	}
	return f, err
}

func addSplitCommas(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func addLooksLikeSource(tok string) bool {
	return strings.ContainsAny(tok, `/\:`) || strings.HasPrefix(tok, ".") || strings.HasPrefix(tok, "~")
}

// ParseAddCLIOptions parses `skl add` arguments with upstream semantics: -a/--agent,
// -s/--skill and --subagent consume every following non-flag token (values may also be
// comma-separated). For compatibility with `skl add -a <agent> <source>`, when no
// positional source was given and the last greedily consumed token is clearly a source
// (an unknown agent name, or a path/URL-like skill token), it is used as the source.
func ParseAddCLIOptions(args []string) ParsedAddCLI {
	res := ParsedAddCLI{ExplicitAgentSet: map[AgentType]bool{}}
	opts := &res.Options
	type lastToken struct {
		flag  string
		value string
		count int // values consumed by this flag occurrence
	}
	var last *lastToken

	consume := func(i int, flag string, add func(string)) int {
		j := i + 1
		n := 0
		for j < len(args) && args[j] != "" && !strings.HasPrefix(args[j], "-") {
			for _, v := range addSplitCommas(args[j]) {
				add(v)
				n++
				last = &lastToken{flag: flag, value: v, count: n}
			}
			j++
		}
		return j - 1
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-g", "--global":
			opts.Global = true
			res.ScopeExplicit = true
		case "-p", "--project":
			opts.Global = false
			res.ScopeExplicit = true
		case "-y", "--yes":
			opts.Yes = true
		case "-l", "--list":
			opts.List = true
		case "--all":
			opts.All = true
		case "--copy":
			opts.Copy = true
		case "--full-depth":
			opts.FullDepth = true
		case "--json":
			opts.JSON = true
		case "-v", "--verbose":
			opts.Verbose = true
		case "-a", "--agent":
			res.AgentFlagPassed = true
			i = consume(i, "agent", func(v string) { opts.Agent = append(opts.Agent, AgentType(v)) })
		case "-s", "--skill":
			i = consume(i, "skill", func(v string) { opts.Skill = append(opts.Skill, v) })
		case "--subagent":
			i = consume(i, "subagent", func(v string) { opts.Subagent = append(opts.Subagent, v) })
		case "--metadata":
			if i+1 >= len(args) {
				res.Errors = append(res.Errors, ErrOptionRequiresValue("--metadata", "a JSON value"))
			} else {
				i++
				if json.Valid([]byte(args[i])) {
					opts.Metadata = args[i]
				} else {
					res.Errors = append(res.Errors, ErrInvalidOptionValue("--metadata", "valid JSON"))
				}
			}
		default:
			if arg != "" && !strings.HasPrefix(arg, "-") {
				res.Sources = append(res.Sources, arg)
			}
		}
	}

	// Only reclaim when the flag took more than one value, so `install -a bogus` still
	// reports an invalid agent instead of treating "bogus" as a source.
	if len(res.Sources) == 0 && last != nil && last.count >= 2 {
		reclaim := false
		switch last.flag {
		case "agent":
			_, known := Agents[AgentType(last.value)]
			reclaim = !known && last.value != "*"
		case "skill", "subagent":
			reclaim = addLooksLikeSource(last.value)
		}
		if reclaim {
			res.Sources = append(res.Sources, last.value)
			switch last.flag {
			case "agent":
				opts.Agent = opts.Agent[:len(opts.Agent)-1]
				if len(opts.Agent) == 0 {
					opts.Agent = nil
				}
			case "skill":
				opts.Skill = opts.Skill[:len(opts.Skill)-1]
				if len(opts.Skill) == 0 {
					opts.Skill = nil
				}
			case "subagent":
				opts.Subagent = opts.Subagent[:len(opts.Subagent)-1]
				if len(opts.Subagent) == 0 {
					opts.Subagent = nil
				}
			}
		}
	}

	for _, a := range opts.Agent {
		if a != "*" {
			res.ExplicitAgentSet[a] = true
		}
	}
	if addHasAgent(opts.Agent, "*") {
		res.ExplicitAgentSet = map[AgentType]bool{}
	}
	return res
}

// ParseAddOptions parses `skl add` arguments and returns the first source (legacy API).
func ParseAddOptions(args []string) (string, AddOptions) {
	p := ParseAddCLIOptions(args)
	src := ""
	if len(p.Sources) > 0 {
		src = p.Sources[0]
	}
	return src, p.Options
}

func addHasAgent(list []AgentType, a AgentType) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

func addHasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func addSkillName(s Skill) string {
	if s.Name != "" {
		return s.Name
	}
	return filepath.Base(s.Path)
}

// addFilterFetched applies FilterSkills to fetched skills, preserving fetched metadata.
func addFilterFetched(skills []FetchedSkill, names []string) []FetchedSkill {
	plain := make([]Skill, len(skills))
	for i, s := range skills {
		plain[i] = s.Skill
	}
	matched := FilterSkills(plain, names)
	var out []FetchedSkill
	used := make([]bool, len(skills))
	for _, m := range matched {
		for i, s := range skills {
			if !used[i] && s.Name == m.Name && s.Path == m.Path {
				used[i] = true
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// addEnsureUniversal appends universal agents that read ~/.agents/skills globally
// (so Antigravity is never injected unless detected or requested).
func addEnsureUniversal(base []AgentType) []AgentType {
	result := append([]AgentType{}, base...)
	for _, ua := range GetUniversalAgents() {
		if ReadsCanonicalGlobally(ua) && !addHasAgent(result, ua) {
			result = append(result, ua)
		}
	}
	return result
}

func addAllAgents() []AgentType {
	return orderedAgentTypes(Agents)
}

// NonInteractiveTargetAgents returns the agents `add -y` installs to when none are named:
// the host agent (plus universal), else Eve alone, else every agent when none is detected,
// else the detected agents plus universal.
func NonInteractiveTargetAgents(cwd string) []AgentType {
	det := addDetectAgent()
	if det.IsAgent && det.Agent != nil {
		if t, ok := GetAgentType(det.Agent.Name); ok {
			return addEnsureUniversal([]AgentType{t})
		}
	}
	installed := DetectInstalledAgents(cwd)
	if addHasAgent(installed, "eve") {
		return []AgentType{"eve"}
	}
	if len(installed) == 0 {
		return addAllAgents()
	}
	return addEnsureUniversal(installed)
}

func addShortenPath(full, cwd string) string {
	home, _ := os.UserHomeDir()
	sep := string(filepath.Separator)
	if home != "" && (full == home || strings.HasPrefix(full, home+sep)) {
		return "~" + full[len(home):]
	}
	if cwd != "" && (full == cwd || strings.HasPrefix(full, cwd+sep)) {
		return "." + full[len(cwd):]
	}
	return full
}

func addFormatList(items []string, maxShow int) string {
	if len(items) <= maxShow {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(items[:maxShow], ", "), len(items)-maxShow)
}

func agentLabels(agents []AgentType) []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, agentLabel(a))
	}
	return out
}

func addTargetName(t InstallTarget) string {
	if t.EveSubagent != "" {
		return fmt.Sprintf("%s (%s)", agentLabel(t.Agent), t.EveSubagent)
	}
	return agentLabel(t.Agent)
}

func addKebabToTitle(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// isLockableDownloadURL reports whether a download URL may be written to a lock file:
// no query string and no user info, which often carry signatures or tokens.
func isLockableDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.RawQuery == "" && !u.ForceQuery && u.User == nil
}

func addRedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// DownloadSkillPath returns the forward-slash SKILL.md path of skillDir within rootDir.
func DownloadSkillPath(rootDir, skillDir string) string {
	rel, err := filepath.Rel(rootDir, skillDir)
	if err != nil || rel == "." {
		return "SKILL.md"
	}
	return filepath.ToSlash(rel) + "/SKILL.md"
}

// ---------------------------------------------------------------------------
// Prompts (line based; upstream uses clack multiselects).
// ---------------------------------------------------------------------------

var errPromptCancelled = errors.New("installation cancelled")

type addPrompter struct {
	in          *bufio.Reader
	out         io.Writer
	interactive bool
}

func (p *addPrompter) ask(question string) (string, error) {
	if !p.interactive {
		return "", ErrPromptRequiresTTY()
	}
	fmt.Fprint(p.out, question)
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return "", errPromptCancelled
	}
	return strings.TrimSpace(line), nil
}

func (p *addPrompter) confirm(question string, def bool) (bool, error) {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	ans, err := p.ask(question + suffix)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(ans) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// choose prints a numbered list and reads a selection (numbers or values, comma separated,
// '*' for all). Empty input selects defaults.
func (p *addPrompter) choose(title string, labels []string, values []string, defaults []int, multi bool) ([]int, error) {
	if !p.interactive {
		return nil, ErrPromptRequiresTTY()
	}
	fmt.Fprintln(p.out, title)
	for i, l := range labels {
		mark := " "
		for _, d := range defaults {
			if d == i {
				mark = "*"
			}
		}
		fmt.Fprintf(p.out, "  %s %2d) %s\n", mark, i+1, l)
	}
	hint := "Enter a number"
	if multi {
		hint = "Enter numbers or names separated by commas ('*' for all)"
	}
	for attempt := 0; attempt < 3; attempt++ {
		ans, err := p.ask(hint + ": ")
		if err != nil {
			return nil, err
		}
		if ans == "" && len(defaults) > 0 {
			return defaults, nil
		}
		if multi && ans == "*" {
			all := make([]int, len(labels))
			for i := range all {
				all[i] = i
			}
			return all, nil
		}
		var picked []int
		ok := true
		for _, tok := range addSplitCommas(ans) {
			if n, err := strconv.Atoi(tok); err == nil && n >= 1 && n <= len(labels) {
				picked = append(picked, n-1)
				continue
			}
			found := false
			for i, v := range values {
				if strings.EqualFold(v, tok) || strings.EqualFold(labels[i], tok) {
					picked = append(picked, i)
					found = true
					break
				}
			}
			if !found {
				ok = false
			}
		}
		if ok && len(picked) > 0 && (multi || len(picked) == 1) {
			return picked, nil
		}
		fmt.Fprintln(p.out, "Invalid selection.")
	}
	return nil, errPromptCancelled
}

// ---------------------------------------------------------------------------
// Installation core shared by RunAdd and AddSkill.
// ---------------------------------------------------------------------------

type addUnit struct {
	src   *FetchedSource
	skill FetchedSkill
}

type addPlan struct {
	units           []addUnit
	targets         []InstallTarget
	targetAgents    []AgentType
	global          bool
	mode            InstallMode
	lockedCopy      bool
	explicit        map[AgentType]bool
	cwd             string
	subagents       []string
	recordSubagents bool
	verbose         bool
	via             string
}

type unitResult struct {
	unit   addUnit
	target InstallTarget
	res    InstallResult
}

func addExecuteInstall(plan addPlan) []unitResult {
	var results []unitResult
	for _, u := range plan.units {
		for _, t := range plan.targets {
			res := InstallSkillForAgent(u.skill.Skill, t.Agent, InstallOptions{
				Global:                 plan.global,
				Cwd:                    plan.cwd,
				Mode:                   plan.mode,
				EveSubagent:            t.EveSubagent,
				CreateMissingAgentRoot: plan.explicit[t.Agent],
				Verbose:                plan.verbose,
			})
			results = append(results, unitResult{unit: u, target: t, res: res})
		}
	}
	return results
}

type lockWriteFailure struct {
	skill    string
	lockPath string
	err      error
}

func (f lockWriteFailure) String() string {
	return fmt.Sprintf("Installed %s but could not record it in %s: %v", StripTerminalEscapes(f.skill), f.lockPath, f.err)
}

func addFirstSuccessful(results []unitResult, u addUnit) *unitResult {
	for i := range results {
		r := &results[i]
		if r.unit.src == u.src && r.unit.skill.Path == u.skill.Path && r.unit.skill.Name == u.skill.Name && r.res.Success {
			return r
		}
	}
	return nil
}

// addUnitHash is the content hash recorded in the project lock: the skill folder at its
// source, or the installed copy for well-known skills (upstream installedSkillHash).
func addUnitHash(u addUnit, installed *unitResult) (string, error) {
	if u.src.Via == "blob" && u.skill.ComputedHash != "" {
		return u.skill.ComputedHash, nil
	}
	if u.src.SourceType == SourceTypeWellKnown && installed != nil {
		p := installed.res.CanonicalPath
		if p == "" {
			p = installed.res.Path
		}
		return ComputeSkillFolderHash(p)
	}
	return ComputeSkillFolderHash(u.skill.Path)
}

func addGlobalFolderHash(u addUnit) string {
	if u.skill.FolderHash != "" {
		return u.skill.FolderHash
	}
	switch u.src.SourceType {
	case SourceTypeGitHub:
		if u.src.Via == "clone" && u.skill.SkillPath != "" {
			return GetGitRepoTreeHash(u.src.RootDir, u.skill.SkillPath)
		}
		return ""
	case SourceTypeWellKnown:
		return ""
	}
	h, _ := ComputeSkillFolderHash(u.skill.Path)
	return h
}

// addRecordLocks writes global or project lock entries for every installed unit. It returns
// the per-skill hashes used and any lock write failures (#2606); download URLs with query
// strings or credentials are never recorded (#2682).
func addRecordLocks(plan addPlan, results []unitResult, warn io.Writer) (map[string]string, []lockWriteFailure) {
	hashes := map[string]string{}
	var failures []lockWriteFailure
	unlockable := map[string][]string{}
	var unlockableOrder []string

	installMode := ""
	if plan.lockedCopy {
		installMode = string(InstallModeCopy)
	}

	for _, u := range plan.units {
		installed := addFirstSuccessful(results, u)
		if installed == nil {
			continue
		}
		name := u.skill.Name
		src := u.src

		downloadURL := src.SourceURL
		if src.SourceType == SourceTypeDownload && u.skill.SourceURL != "" {
			downloadURL = u.skill.SourceURL
		}
		if src.SourceType == SourceTypeDownload && !isLockableDownloadURL(downloadURL) {
			if _, ok := unlockable[downloadURL]; !ok {
				unlockableOrder = append(unlockableOrder, downloadURL)
			}
			unlockable[downloadURL] = append(unlockable[downloadURL], name)
			if !plan.global {
				if h, err := addUnitHash(u, installed); err == nil {
					hashes[name] = h
				}
			}
			continue
		}

		if plan.global {
			entry := SkillLockEntry{
				Source:      src.Source,
				SourceType:  src.SourceType,
				SourceUrl:   src.SourceURL,
				Ref:         src.Ref,
				PluginName:  u.skill.PluginName,
				InstallMode: installMode,
			}
			switch src.SourceType {
			case SourceTypeWellKnown:
				entry.SourceBaseUrl = src.SourceURL
				if u.skill.SourceURL != "" {
					entry.SourceUrl = u.skill.SourceURL
				}
				entry.WellKnownDigest = u.skill.WellKnownDigest
				entry.Ref = ""
			case SourceTypeDownload:
				entry.SourceUrl = downloadURL
				entry.SkillPath = u.skill.SkillPath
				entry.SkillFolderHash, _ = ComputeSkillFolderHash(u.skill.Path)
				entry.Ref = ""
			case SourceTypeLocal:
				entry.SkillFolderHash = addGlobalFolderHash(u)
			default:
				entry.SkillPath = u.skill.SkillPath
				entry.SkillFolderHash = addGlobalFolderHash(u)
			}
			if h, err := addUnitHash(u, installed); err == nil {
				hashes[name] = h
			}
			if err := AddSkillToLock(name, entry); err != nil {
				failures = append(failures, lockWriteFailure{name, GetSkillLockPath(), err})
			}
			continue
		}

		hash, err := addUnitHash(u, installed)
		if err != nil {
			continue
		}
		hashes[name] = hash
		entry := LocalSkillLockEntry{
			Source:       src.Source,
			SourceType:   src.SourceType,
			ComputedHash: hash,
			InstallMode:  installMode,
		}
		switch src.SourceType {
		case SourceTypeWellKnown:
			entry.SourceUrl = src.SourceURL
			entry.WellKnownDigest = u.skill.WellKnownDigest
		case SourceTypeDownload:
			entry.SourceUrl = downloadURL
			entry.SkillPath = u.skill.SkillPath
		case SourceTypeLocal:
			// Local skills carry no repo path.
		default:
			if src.SourceType == SourceTypeGit || src.SourceType == SourceTypeGitLab {
				entry.SourceUrl = src.SourceURL
			}
			entry.Ref = src.Ref
			entry.SkillPath = u.skill.SkillPath
		}
		if plan.recordSubagents {
			entry.Subagents = append([]string{}, plan.subagents...)
		}
		entry.Via = plan.via
		if err := AddSkillToLocalLock(name, entry, plan.cwd); err != nil {
			failures = append(failures, lockWriteFailure{name, GetLocalLockPath(plan.cwd), err})
		}
	}

	for _, u := range unlockableOrder {
		fmt.Fprintln(warn, FormatError(NewDiagnostic(CodeDownloadNotLocked,
			fmt.Sprintf("Not recording %s in the lock file: %s has a query string or credentials, which may be secret",
				strings.Join(unlockable[u], ", "), addRedactURL(u)),
			"To let `skills update` fetch it again, serve it from a URL without them.")))
	}
	return hashes, failures
}

func addNormalizePointerName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// addResolvePackPointers swaps skills.sh pack entries that point at a public repo for the
// repo's copy when its skills-sh-tree-v2 hash still matches the pack's pin (#2651).
// Fetched repos are appended to *extra so the caller can clean them up.
func addResolvePackPointers(units []addUnit, cwd string, out io.Writer, extra *[]*FetchedSource) []addUnit {
	type group struct {
		repo string
		idx  []int
		ids  []string
	}
	groups := map[string]*group{}
	var order []string
	for i, u := range units {
		p := u.skill.PackPointer
		if p == nil {
			continue
		}
		key := strings.ToLower(p.Repo)
		g, ok := groups[key]
		if !ok {
			g = &group{repo: p.Repo}
			groups[key] = g
			order = append(order, key)
		}
		g.idx = append(g.idx, i)
		g.ids = append(g.ids, p.Skill)
	}
	if len(order) == 0 {
		return units
	}
	result := append([]addUnit{}, units...)
	for _, key := range order {
		g := groups[key]
		fetched, err := fetchSourceFn(SourceRequest{Source: g.repo, Skills: g.ids, Yes: true, Cwd: cwd})
		if err != nil || fetched == nil {
			continue
		}
		*extra = append(*extra, fetched)
		for _, i := range g.idx {
			pin := units[i].skill.PackPointer
			want := addNormalizePointerName(pin.Skill)
			for _, cand := range fetched.Skills {
				if addNormalizePointerName(cand.Name) != want && addNormalizePointerName(filepath.Base(cand.Path)) != want {
					continue
				}
				hash, err := ComputeSkillTreeHash(cand.Path)
				if err == nil && hash == pin.TreeHash {
					result[i] = addUnit{src: fetched, skill: cand}
					fmt.Fprintf(out, "✓ %s from %s\n", addSkillName(cand.Skill), g.repo)
				} else {
					fmt.Fprintf(out, "  %s changed in %s since this pack pinned it; using the pack's copy\n", addSkillName(cand.Skill), g.repo)
				}
				break
			}
		}
	}
	return result
}

// addStagedCopyHash hashes the copy CopySkillDirectory would install from dir.
func addStagedCopyHash(dir string) (string, error) {
	tmp, err := os.MkdirTemp("", "skills-hash-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, "skill")
	if err := CopySkillDirectory(dir, staged, "", false); err != nil {
		return "", err
	}
	return ComputeSkillFolderHash(staged)
}

// InstallFromSourceOptions configures InstallFromSource (restore from lock and sync).
type InstallFromSourceOptions struct {
	// Skills selects skills by name (FilterSkills rules); all when empty.
	Skills []string
	Agents []AgentType
	// Via is recorded on project lock entries (e.g. an npm package name).
	Via string
	// FrozenLock, when set, requires every skill to match its lock entry; the lock is
	// never written and changed content is refused before anything touches disk.
	FrozenLock *LocalSkillLockFile
	// InstallMode is "" (symlink) or copy; copy is also recorded in the lock.
	InstallMode InstallMode
	Cwd         string
}

// SourceInstallResult reports InstallFromSource.
type SourceInstallResult struct {
	Installed []string // display names
	Failed    []string // "<skill> → <agent>: <error>"
	Error     string
}

// InstallFromSource installs a fetched source at project scope into opts.Agents (creating
// their project roots) and records skills-lock.json, mirroring upstream installFromSource.
// The caller owns f and must call f.Cleanup().
func InstallFromSource(f *FetchedSource, o InstallFromSourceOptions) SourceInstallResult {
	var res SourceInstallResult
	if f == nil {
		res.Error = "No source to install"
		return res
	}
	cwd := installCwd(o.Cwd)
	selected := f.Skills
	if len(o.Skills) > 0 {
		selected = addFilterFetched(f.Skills, o.Skills)
	}
	if len(selected) == 0 {
		res.Error = "No matching skills found"
		return res
	}
	units := make([]addUnit, len(selected))
	for i, s := range selected {
		units[i] = addUnit{src: f, skill: s}
	}

	frozenChanges := func(hashOf func(u addUnit) (string, bool)) []string {
		var changes []string
		for _, u := range units {
			h, ok := hashOf(u)
			if !ok {
				continue
			}
			if reason := LockChangeReason(*o.FrozenLock, u.skill.Name, LocalSkillLockEntry{ComputedHash: h}); reason != "" {
				changes = append(changes, reason)
			}
		}
		return changes
	}
	if o.FrozenLock != nil {
		// Refuse drift before anything touches disk. Well-known skills are hashed as
		// installed, so hash a staged copy of them.
		changes := frozenChanges(func(u addUnit) (string, bool) {
			if u.src.SourceType == SourceTypeWellKnown {
				h, err := addStagedCopyHash(u.skill.Path)
				return h, err == nil
			}
			h, err := addUnitHash(u, nil)
			return h, err == nil
		})
		if len(changes) > 0 {
			res.Error = FrozenLockfileError(changes).Error()
			return res
		}
	}

	mode := o.InstallMode
	if mode == "" {
		mode = InstallModeSymlink
	}
	explicit := map[AgentType]bool{}
	for _, a := range o.Agents {
		explicit[a] = true
	}
	plan := addPlan{
		units: units, targets: BuildInstallTargets(o.Agents, nil), targetAgents: o.Agents,
		mode: mode, lockedCopy: mode == InstallModeCopy, explicit: explicit, cwd: cwd, via: o.Via,
	}
	results := addExecuteInstall(plan)
	var installedUnits []addUnit
	for _, u := range units {
		if r := addFirstSuccessful(results, u); r != nil {
			installedUnits = append(installedUnits, u)
			res.Installed = append(res.Installed, addSkillName(u.skill.Skill))
		}
	}
	for _, r := range results {
		if !r.res.Success {
			res.Failed = append(res.Failed, fmt.Sprintf("%s → %s: %s", addSkillName(r.unit.skill.Skill), addTargetName(r.target), r.res.Error))
		}
	}

	if o.FrozenLock != nil {
		changes := frozenChanges(func(u addUnit) (string, bool) {
			inst := addFirstSuccessful(results, u)
			if inst == nil {
				return "", false
			}
			h, err := addUnitHash(u, inst)
			return h, err == nil
		})
		if len(changes) > 0 {
			res.Error = FrozenLockfileError(changes).Error()
		}
		return res
	}
	plan.units = installedUnits
	var warn bytes.Buffer
	_, failures := addRecordLocks(plan, results, &warn)
	if warn.Len() > 0 {
		_, _ = os.Stderr.Write(warn.Bytes())
	}
	if len(failures) > 0 {
		msgs := make([]string, len(failures))
		for i, fl := range failures {
			msgs[i] = fl.String()
		}
		res.Error = strings.Join(msgs, "\n")
	}
	return res
}

// ---------------------------------------------------------------------------
// RunAdd
// ---------------------------------------------------------------------------

// RunAdd runs `skl add` with os streams and returns the process exit code.
func RunAdd(args []string) int {
	return RunAddWithIO(args, AddIO{})
}

// RunAddWithIO runs `skl add` against the given streams and returns the exit code.
func RunAddWithIO(args []string, aio AddIO) int {
	return RunAddParsed(ParseAddCLIOptions(args), aio)
}

type addRun struct {
	aio      AddIO
	out      io.Writer // human output (stderr in --json mode)
	errw     io.Writer
	jsonOut  io.Writer
	json     bool
	results  []AddJSONResult
	emitted  bool
	tipShown bool
}

func (r *addRun) emitJSON() {
	if !r.json || r.emitted {
		return
	}
	r.emitted = true
	results := r.results
	if results == nil {
		results = []AddJSONResult{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(results)
	_, _ = r.jsonOut.Write(buf.Bytes())
}

// fail prints msg (in json mode) and emits the JSON array, returning code.
func (r *addRun) fail(code int, msg string) int {
	if r.json {
		if msg != "" {
			fmt.Fprintln(r.errw, msg)
		}
		if code != 0 && len(r.results) == 0 {
			if msg == "" {
				msg = "Installation failed"
			}
			r.results = append(r.results, AddJSONResult{Status: "failed", Error: msg})
		}
		r.emitJSON()
	}
	return code
}

func (r *addRun) showTip() {
	if r.tipShown {
		return
	}
	r.tipShown = true
	fmt.Fprintln(r.out, "Tip: use the --yes (-y) and --global (-g) flags to install without prompts.")
}

func (r *addRun) logError(err error) {
	fmt.Fprintln(r.errw, FormatError(err))
}

// RunAddParsed runs `skl add` from already parsed options (used by `install <source>`).
func RunAddParsed(p ParsedAddCLI, aio AddIO) int {
	if aio.Stdout == nil {
		aio.Stdout = os.Stdout
	}
	if aio.Stderr == nil {
		aio.Stderr = os.Stderr
	}
	if aio.Stdin == nil {
		aio.Stdin = os.Stdin
	}
	interactive := isInteractive()
	if aio.Interactive != nil {
		interactive = *aio.Interactive
	}
	cwd := installCwd(aio.Cwd)

	r := &addRun{aio: aio, out: aio.Stdout, errw: aio.Stderr, jsonOut: aio.Stdout, json: p.Options.JSON}
	if r.json {
		r.out = aio.Stderr
	}
	prompt := &addPrompter{in: bufio.NewReader(aio.Stdin), out: r.out, interactive: interactive}
	opts := p.Options
	explicit := map[AgentType]bool{}
	for a := range p.ExplicitAgentSet {
		explicit[a] = true
	}

	if len(p.Errors) > 0 {
		for _, e := range p.Errors {
			r.logError(e)
		}
		return r.fail(1, p.Errors[0].Error())
	}
	if len(p.Sources) == 0 {
		fmt.Fprintln(r.errw, "ERROR Missing required argument: source")
		fmt.Fprintln(r.errw, "\n  Usage:\n    skl add <source> [options]\n\n  Example:\n    skl add vercel-labs/agent-skills")
		return r.fail(1, "Missing required argument: source")
	}
	if len(p.Sources) > 1 {
		err := ErrMultipleSources(p.Sources)
		r.logError(err)
		return r.fail(1, err.Error())
	}
	source := p.Sources[0]

	if opts.All {
		opts.Skill = []string{"*"}
		opts.Agent = []AgentType{"*"}
		opts.Yes = true
	}

	det := addDetectAgent()
	if det.IsAgent {
		opts.Yes = true
		if len(opts.Agent) == 0 && det.Agent != nil {
			if mapped, ok := GetAgentType(det.Agent.Name); ok {
				opts.Agent = addEnsureUniversal([]AgentType{mapped})
			}
		}
	}

	if r.json && !opts.Yes {
		return r.fail(1, "The --json flag requires --yes (or --all) to run non-interactively.")
	}
	if r.json && opts.List {
		return r.fail(1, "The --json flag cannot be combined with --list.")
	}

	if det.IsAgent && det.Agent != nil {
		fmt.Fprintf(r.out, "%s Agent detected, installing non-interactively\n", det.Agent.Name)
	} else if !interactive {
		r.showTip()
	}

	// #2623: validate --agent before touching the network or filesystem.
	if len(opts.Agent) > 0 && !addHasAgent(opts.Agent, "*") {
		var invalid []string
		for _, a := range opts.Agent {
			if _, ok := Agents[a]; !ok {
				invalid = append(invalid, string(a))
			}
		}
		if len(invalid) > 0 {
			err := ErrInvalidAgents(invalid, installSortedAgentNames())
			r.logError(err)
			return r.fail(1, err.Error())
		}
	}

	parsed, err := ParseSource(source, ParseSourceOptions{Cwd: cwd})
	if err != nil {
		r.logError(err)
		fmt.Fprintln(r.out, "Installation failed")
		return r.fail(1, err.Error())
	}
	fmt.Fprintln(r.out, addSourceLine(parsed))

	if parsed.Type == SourceTypeWellKnown && r.json {
		return r.fail(1, "--json is not yet supported for well-known skill sources.")
	}

	if parsed.SkillFilter != "" && !addHasString(opts.Skill, parsed.SkillFilter) {
		opts.Skill = append(opts.Skill, parsed.SkillFilter)
	}
	includeInternal := len(opts.Skill) > 0 && !addHasString(opts.Skill, "*")

	if parsed.Type == SourceTypeLocal {
		if _, err := os.Stat(parsed.LocalPath); err != nil {
			e := ErrLocalPathNotFound(parsed.LocalPath)
			r.logError(e)
			return r.fail(1, e.Error())
		}
	}

	fetched, err := fetchSourceFn(SourceRequest{
		Source: source, Parsed: &parsed, Skills: opts.Skill, FullDepth: opts.FullDepth,
		IncludeInternal: includeInternal, Yes: opts.Yes, Cwd: cwd,
	})
	var extra []*FetchedSource
	defer func() {
		if fetched != nil {
			_ = fetched.Cleanup()
		}
		for _, f := range extra {
			_ = f.Cleanup()
		}
	}()
	if err != nil {
		msg := err.Error()
		if IsDiagnostic(err, CodeUnsupportedGitTransport, CodeGitCloneTimeout, CodeGitAuthFailed, CodeGitSAMLSSOBlocked, CodeGitCloneFailed) {
			fmt.Fprintln(r.errw, "Failed to clone repository")
			msg = "Failed to clone repository\n" + msg
		}
		r.logError(err)
		r.showTip()
		fmt.Fprintln(r.out, "Installation failed")
		return r.fail(1, msg)
	}

	if IsNotionSource(source) || ParseNotionSkillURL(source) != "" {
		// The pack selection (or page URL) already chose the skills.
		opts.Skill = []string{"*"}
	}
	skills := fetched.Skills
	if len(skills) == 0 {
		e := ErrNoValidSkills()
		r.logError(e)
		return r.fail(1, e.Error())
	}
	fmt.Fprintf(r.out, "Found %d skill%s\n", len(skills), addPlural(len(skills)))

	if opts.List {
		addPrintAvailable(r.out, skills)
		fmt.Fprintln(r.out, "Use --skill <name> to install specific skills")
		return r.fail(0, "")
	}

	// Skill selection.
	var selected []FetchedSkill
	switch {
	case addHasString(opts.Skill, "*"):
		selected = skills
	case len(opts.Skill) > 0:
		selected = addFilterFetched(skills, opts.Skill)
		if r.json {
			for _, req := range opts.Skill {
				if len(addFilterFetched(skills, []string{req})) == 0 {
					r.results = append(r.results, AddJSONResult{Name: req, Status: "skipped", Reason: "No matching skill found in source"})
				}
			}
		}
		if len(selected) == 0 {
			names := make([]string, len(skills))
			for i, s := range skills {
				names[i] = addSkillName(s.Skill)
			}
			e := ErrNoMatchingSkills(opts.Skill, names)
			r.logError(e)
			return r.fail(1, e.Error())
		}
		names := make([]string, len(selected))
		for i, s := range selected {
			names[i] = addSkillName(s.Skill)
		}
		fmt.Fprintf(r.out, "Selected %d skill%s: %s\n", len(selected), addPlural(len(selected)), strings.Join(names, ", "))
	case len(skills) == 1 || opts.Yes:
		selected = skills
	default:
		sorted := append([]FetchedSkill{}, skills...)
		sort.SliceStable(sorted, func(i, j int) bool {
			a, b := sorted[i], sorted[j]
			if (a.PluginName != "") != (b.PluginName != "") {
				return a.PluginName != ""
			}
			if a.PluginName != b.PluginName {
				return jsLocaleCompare(a.PluginName, b.PluginName) < 0
			}
			return jsLocaleCompare(addSkillName(a.Skill), addSkillName(b.Skill)) < 0
		})
		labels := make([]string, len(sorted))
		values := make([]string, len(sorted))
		for i, s := range sorted {
			values[i] = addSkillName(s.Skill)
			labels[i] = values[i]
			if s.Description != "" {
				labels[i] += " - " + s.Description
			}
		}
		idx, err := prompt.choose("Select skills to install", labels, values, nil, true)
		if err != nil {
			return r.cancelled(err)
		}
		for _, i := range idx {
			selected = append(selected, sorted[i])
		}
	}

	units := make([]addUnit, 0, len(selected))
	for _, s := range selected {
		units = append(units, addUnit{src: fetched, skill: s})
	}

	// Target agents.
	var targetAgents []AgentType
	switch {
	case addHasAgent(opts.Agent, "*"):
		targetAgents = addAllAgents()
		fmt.Fprintf(r.out, "Installing to all %d agents\n", len(targetAgents))
	case len(opts.Agent) > 0:
		targetAgents = opts.Agent
	default:
		installed := DetectInstalledAgents(cwd)
		switch {
		case addHasAgent(installed, "eve") && (opts.Yes || !det.IsAgent):
			useEve := true
			if !opts.Yes {
				useEve, err = prompt.confirm(fmt.Sprintf("Detected an eve project. Install %s for your eve agent to use?", addFormatList(addUnitNames(units), 3)), true)
				if err != nil {
					return r.cancelled(err)
				}
			}
			if useEve {
				targetAgents = []AgentType{"eve"}
				if !opts.Yes {
					explicit["eve"] = true
				}
			} else {
				targetAgents, err = addSelectAgents(prompt, opts.Global)
				if err != nil {
					return r.cancelled(err)
				}
				for _, a := range targetAgents {
					explicit[a] = true
				}
			}
		case len(installed) == 0:
			if !r.json {
				maybeSuggestAgyTo(r.out, prompt.in, opts, interactive)
			}
			if opts.Yes {
				targetAgents = addAllAgents()
				fmt.Fprintln(r.out, "Installing to all agents")
			} else {
				targetAgents, err = addSelectAgents(prompt, opts.Global)
				if err != nil {
					return r.cancelled(err)
				}
				for _, a := range targetAgents {
					explicit[a] = true
				}
				_ = SaveSelectedAgents(addAgentStrings(targetAgents))
			}
		case len(installed) == 1 || opts.Yes:
			targetAgents = addEnsureUniversal(installed)
			fmt.Fprintf(r.out, "Installing to: %s\n", strings.Join(agentLabels(installed), ", "))
		default:
			targetAgents, err = addSelectAgents(prompt, opts.Global)
			if err != nil {
				return r.cancelled(err)
			}
			for _, a := range targetAgents {
				explicit[a] = true
			}
			_ = SaveSelectedAgents(addAgentStrings(targetAgents))
		}
	}

	if len(opts.Subagent) > 0 {
		explicit["eve"] = true
		if !addHasAgent(targetAgents, "eve") {
			targetAgents = append(targetAgents, "eve")
		}
	}
	eveSubagents := []string{""}
	if addHasAgent(targetAgents, "eve") {
		available := GetEveSubagents(cwd)
		if len(opts.Subagent) > 0 {
			eveSubagents = nil
			for _, s := range opts.Subagent {
				if s == "root" || s == "." {
					s = ""
				}
				eveSubagents = append(eveSubagents, s)
			}
		} else if len(available) > 0 && !opts.Yes {
			labels := append([]string{"Root agent (agent/skills)"}, available...)
			values := append([]string{"root"}, available...)
			idx, err := prompt.choose("Where should Eve skills be installed?", labels, values, []int{0}, true)
			if err != nil {
				return r.cancelled(err)
			}
			eveSubagents = nil
			for _, i := range idx {
				if i == 0 {
					eveSubagents = append(eveSubagents, "")
				} else {
					eveSubagents = append(eveSubagents, available[i-1])
				}
			}
		}
	}

	global := opts.Global
	supportsGlobal := false
	for _, a := range targetAgents {
		if cfg, ok := Agents[a]; ok && cfg.GlobalSkillsDir != "" {
			supportsGlobal = true
		}
	}
	if !p.ScopeExplicit && !opts.Yes && supportsGlobal {
		idx, err := prompt.choose("Installation scope", []string{
			"Project (install in current directory, committed with your project)",
			"Global (install in home directory, available across all projects)",
		}, []string{"project", "global"}, []int{0}, false)
		if err != nil {
			return r.cancelled(err)
		}
		global = idx[0] == 1
	}

	targetAgents = SkipAgentsWithoutGlobalDir(targetAgents, global, len(p.ExplicitAgentSet) > 0, r.out)
	var subTargets []string
	for _, s := range eveSubagents {
		if s == "" {
			subTargets = append(subTargets, "root")
		} else {
			subTargets = append(subTargets, s)
		}
	}
	targets := BuildInstallTargets(targetAgents, subTargets)

	// Surface an unusable project lock before touching disk.
	if !global {
		if _, err := ReadLocalLock(cwd); err != nil {
			r.logError(err)
			return r.fail(1, err.Error())
		}
	}

	defaultMode := DefaultInstallMode(targets)
	mode := defaultMode
	if opts.Copy {
		mode = InstallModeCopy
	}
	if !opts.Copy && !opts.Yes && mode == InstallModeSymlink {
		idx, err := prompt.choose("Installation method", []string{
			"Symlink (Recommended): single source of truth, easy updates",
			"Copy to all agents: independent copies for each agent",
		}, []string{"symlink", "copy"}, []int{0}, false)
		if err != nil {
			return r.cancelled(err)
		}
		if idx[0] == 1 {
			mode = InstallModeCopy
		}
	}
	lockedCopy := mode == InstallModeCopy && (opts.Copy || defaultMode == InstallModeSymlink)

	if fetched.SourceType == SourceTypeWellKnown {
		units = addResolvePackPointers(units, cwd, r.out, &extra)
	}

	recordSubagents := addHasAgent(targetAgents, "eve") && (len(eveSubagents) > 1 || (len(eveSubagents) == 1 && eveSubagents[0] != ""))
	plan := addPlan{
		units: units, targets: targets, targetAgents: targetAgents, global: global, mode: mode,
		lockedCopy: lockedCopy, explicit: explicit, cwd: cwd, subagents: eveSubagents,
		recordSubagents: recordSubagents, verbose: opts.Verbose,
	}

	addPrintSummary(r.out, plan)

	if !opts.Yes {
		ok, err := prompt.confirm("Proceed with installation?", true)
		if err != nil {
			return r.cancelled(err)
		}
		if !ok {
			return r.cancelled(errPromptCancelled)
		}
	}

	results := addExecuteInstall(plan)
	exitCode := 0
	hashes, lockFailures := addRecordLocks(plan, results, r.errw)
	for _, f := range lockFailures {
		fmt.Fprintln(r.errw, f.String())
		exitCode = 1
	}

	var failed []unitResult
	for _, res := range results {
		if !res.res.Success {
			failed = append(failed, res)
		}
	}

	if r.json {
		for _, u := range units {
			name := addSkillName(u.skill.Skill)
			var mine []unitResult
			for _, res := range results {
				if res.unit.src == u.src && res.unit.skill.Path == u.skill.Path && res.unit.skill.Name == u.skill.Name {
					mine = append(mine, res)
				}
			}
			var firstFail *unitResult
			for i := range mine {
				if !mine[i].res.Success {
					firstFail = &mine[i]
					break
				}
			}
			if firstFail != nil {
				msg := firstFail.res.Error
				if msg == "" {
					msg = "Installation failed"
				}
				r.results = append(r.results, AddJSONResult{Name: name, Status: "failed", Error: msg})
				continue
			}
			entry := AddJSONResult{Name: name, Status: "installed", Source: addJSONSource(u.src), Mode: mode}
			if u.src.Ref != "" {
				ref := u.src.Ref
				entry.Ref = &ref
			}
			if h, ok := hashes[u.skill.Name]; ok {
				hh := h
				entry.Hash = &hh
			}
			if global {
				entry.Scope = "global"
			} else {
				entry.Scope = "project"
			}
			if len(mine) > 0 {
				entry.Path = mine[0].res.CanonicalPath
				if entry.Path == "" {
					entry.Path = mine[0].res.Path
				}
				if mine[0].res.Mode != "" {
					entry.Mode = mine[0].res.Mode
				}
			}
			entry.Agents = []string{}
			for _, res := range mine {
				if !res.res.Skipped {
					entry.Agents = append(entry.Agents, addTargetName(res.target))
				}
			}
			r.results = append(r.results, entry)
		}
		r.emitJSON()
		for _, res := range r.results {
			if res.Status == "skipped" {
				exitCode = 1
			}
		}
		if len(failed) > 0 {
			exitCode = 1
		}
		return exitCode
	}

	addPrintResults(r.out, plan, results)
	if len(failed) > 0 {
		fmt.Fprintf(r.errw, "\nFailed to install %d\n", len(failed))
		for _, f := range failed {
			fmt.Fprintf(r.errw, "  ✗ %s → %s: %s\n", addSkillName(f.unit.skill.Skill), addTargetName(f.target), f.res.Error)
		}
		exitCode = 1
	}
	fmt.Fprintln(r.out, "\nDone!  Review skills before use; they run with full agent permissions.")
	return exitCode
}

// cancelled handles an aborted or impossible prompt. Without a TTY it exits 1 with
// SKILLS_PROMPT_REQUIRES_TTY; a deliberate interactive cancel exits 0.
func (r *addRun) cancelled(err error) int {
	fmt.Fprintln(r.out, "Installation cancelled")
	if IsDiagnostic(err, CodePromptRequiresTTY) {
		r.logError(err)
		return r.fail(1, err.Error())
	}
	return r.fail(0, "")
}

func addPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func addUnitNames(units []addUnit) []string {
	names := make([]string, len(units))
	for i, u := range units {
		names[i] = addSkillName(u.skill.Skill)
	}
	return names
}

func addAgentStrings(agents []AgentType) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = string(a)
	}
	return out
}

func addJSONSource(src *FetchedSource) string {
	if src.Source != "" {
		return src.Source
	}
	if src.Parsed.Type == SourceTypeLocal {
		return src.Parsed.LocalPath
	}
	return src.Parsed.URL
}

func addSourceLine(p ParsedSource) string {
	loc := p.URL
	if p.Type == SourceTypeLocal && p.LocalPath != "" {
		loc = p.LocalPath
	}
	line := "Source: " + loc
	if p.Ref != "" {
		line += " @ " + p.Ref
	}
	if p.Subpath != "" {
		line += " (" + p.Subpath + ")"
	}
	if p.SkillFilter != "" {
		line += " @" + p.SkillFilter
	}
	return line
}

// addSelectAgents prompts for target agents (universal agents are always included).
func addSelectAgents(p *addPrompter, global bool) ([]AgentType, error) {
	supports := func(a AgentType) bool {
		cfg, ok := Agents[a]
		return ok && (!global || cfg.GlobalSkillsDir != "")
	}
	var universal []AgentType
	for _, a := range GetUniversalAgents() {
		if supports(a) {
			universal = append(universal, a)
		}
	}
	var others []AgentType
	for _, a := range GetNonUniversalAgents() {
		if a != "eve" && a != AgentUniversal && supports(a) {
			others = append(others, a)
		}
	}
	if !p.interactive {
		return nil, ErrPromptRequiresTTY()
	}
	fmt.Fprintf(p.out, "Universal (.agents/skills), always included: %s\n", addFormatList(agentLabels(universal), 8))
	labels := make([]string, len(others))
	values := make([]string, len(others))
	for i, a := range others {
		cfg := Agents[a]
		hint := cfg.SkillsDir
		if global {
			hint = cfg.GlobalSkillsDir
		}
		labels[i] = fmt.Sprintf("%s (%s)", cfg.DisplayName, hint)
		values[i] = string(a)
	}
	var defaults []int
	if last, err := GetLastSelectedAgents(); err == nil {
		for _, l := range last {
			for i, v := range values {
				if v == l {
					defaults = append(defaults, i)
				}
			}
		}
	}
	idx, err := p.choose("Which agents do you want to install to?", labels, values, defaults, true)
	if err != nil {
		return nil, err
	}
	selected := append([]AgentType{}, universal...)
	for _, i := range idx {
		selected = append(selected, others[i])
	}
	return selected, nil
}

func addPrintAvailable(w io.Writer, skills []FetchedSkill) {
	fmt.Fprintln(w, "\nAvailable Skills")
	groups := map[string][]FetchedSkill{}
	var ungrouped []FetchedSkill
	for _, s := range skills {
		if s.PluginName != "" {
			groups[s.PluginName] = append(groups[s.PluginName], s)
		} else {
			ungrouped = append(ungrouped, s)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintln(w, addKebabToTitle(k))
		for _, s := range groups[k] {
			fmt.Fprintf(w, "  %s\n    %s\n", addSkillName(s.Skill), s.Description)
		}
		fmt.Fprintln(w)
	}
	if len(ungrouped) > 0 {
		if len(keys) > 0 {
			fmt.Fprintln(w, "General")
		}
		for _, s := range ungrouped {
			fmt.Fprintf(w, "  %s\n    %s\n", addSkillName(s.Skill), s.Description)
		}
	}
	fmt.Fprintln(w)
}

// addTargetSummaryLines shows universal vs symlinked agents and Eve subagents (#2271).
func addTargetSummaryLines(targets []InstallTarget, mode InstallMode, global bool) []string {
	var roots []AgentType
	var subNames []string
	for _, t := range targets {
		if t.EveSubagent != "" {
			subNames = append(subNames, addTargetName(t))
		} else {
			roots = append(roots, t.Agent)
		}
	}
	universal, symlinked := SplitAgentsByType(roots, global)
	var lines []string
	if len(universal) > 0 {
		lines = append(lines, "  universal: "+addFormatList(agentLabels(universal), 5))
	}
	if len(symlinked) > 0 {
		label := "copy →"
		if mode == InstallModeSymlink {
			label = "symlink →"
		}
		lines = append(lines, "  "+label+" "+addFormatList(agentLabels(symlinked), 5))
	}
	if len(subNames) > 0 {
		lines = append(lines, "  copy → "+addFormatList(subNames, 5))
	}
	return lines
}

func addPrintSummary(w io.Writer, plan addPlan) {
	var lines []string
	for _, u := range plan.units {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		name := u.skill.Name
		var canonical string
		if len(plan.targets) == 1 && plan.targets[0].Agent == "eve" {
			canonical = filepath.Join(GetAgentBaseDir("eve", plan.global, plan.cwd, plan.targets[0].EveSubagent), SanitizeName(name))
		} else {
			canonical = GetCanonicalPath(name, plan.global, plan.cwd)
		}
		lines = append(lines, addShortenPath(canonical, plan.cwd))
		lines = append(lines, addTargetSummaryLines(plan.targets, plan.mode, plan.global)...)
		var overwrites []string
		for _, t := range plan.targets {
			if IsSkillInstalled(name, t.Agent, plan.global, plan.cwd, t.EveSubagent) {
				overwrites = append(overwrites, addTargetName(t))
			}
		}
		if len(overwrites) > 0 {
			lines = append(lines, "  overwrites: "+addFormatList(overwrites, 5))
		}
	}
	fmt.Fprintln(w, "\nInstallation Summary")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// addResultLines splits per-target results into universal/symlinked/copied/skipped (#2271).
func addResultLines(results []unitResult, targetAgents []AgentType, global bool) []string {
	universal, symlinkAgents := SplitAgentsByType(targetAgents, global)
	uniNames := agentLabels(universal)
	isIn := func(list []string, s string) bool { return addHasString(list, s) }
	var ok, copied, skipped []string
	for _, r := range results {
		name := addTargetName(r.target)
		switch {
		case r.res.Skipped:
			if r.res.SkipReason == SkipReasonMissingAgentProjectDirectory && addHasAgent(symlinkAgents, r.target.Agent) {
				skipped = append(skipped, name)
			}
		case r.res.SymlinkFailed:
			copied = append(copied, name)
		case !isIn(uniNames, name):
			ok = append(ok, name)
		}
	}
	var lines []string
	if len(uniNames) > 0 {
		lines = append(lines, "  universal: "+addFormatList(uniNames, 5))
	}
	if len(ok) > 0 {
		lines = append(lines, "  symlinked: "+addFormatList(ok, 5))
	}
	if len(copied) > 0 {
		lines = append(lines, "  copied: "+addFormatList(copied, 5))
	}
	if len(skipped) > 0 {
		lines = append(lines, "  skipped: "+addFormatList(skipped, 5)+" (project directory not found)")
	}
	return lines
}

func addPrintResults(w io.Writer, plan addPlan, results []unitResult) {
	type perSkill struct {
		unit    addUnit
		results []unitResult
	}
	var order []*perSkill
	index := map[string]*perSkill{}
	var symlinkFailures []string
	for _, r := range results {
		if !r.res.Success {
			continue
		}
		key := r.unit.skill.Path + "\x00" + r.unit.skill.Name
		ps, ok := index[key]
		if !ok {
			ps = &perSkill{unit: r.unit}
			index[key] = ps
			order = append(order, ps)
		}
		ps.results = append(ps.results, r)
		if r.res.Mode == InstallModeSymlink && r.res.SymlinkFailed {
			symlinkFailures = append(symlinkFailures, addTargetName(r.target))
		}
	}
	if len(order) == 0 {
		return
	}
	groups := map[string][]*perSkill{}
	var ungrouped []*perSkill
	for _, ps := range order {
		if ps.unit.skill.PluginName != "" {
			groups[ps.unit.skill.PluginName] = append(groups[ps.unit.skill.PluginName], ps)
		} else {
			ungrouped = append(ungrouped, ps)
		}
	}
	var lines []string
	emit := func(list []*perSkill) {
		for _, ps := range list {
			first := ps.results[0]
			name := addSkillName(ps.unit.skill.Skill)
			if first.res.Mode == InstallModeCopy {
				lines = append(lines, fmt.Sprintf("✓ %s (copied)", name))
				seen := map[string]bool{}
				for _, r := range ps.results {
					sp := addShortenPath(r.res.Path, plan.cwd)
					if !seen[sp] {
						seen[sp] = true
						lines = append(lines, "  → "+sp)
					}
				}
				continue
			}
			if first.res.CanonicalPath != "" {
				lines = append(lines, "✓ "+addShortenPath(first.res.CanonicalPath, plan.cwd))
			} else {
				lines = append(lines, "✓ "+name)
			}
			lines = append(lines, addResultLines(ps.results, plan.targetAgents, plan.global)...)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, "", addKebabToTitle(k))
		emit(groups[k])
	}
	if len(ungrouped) > 0 {
		if len(keys) > 0 {
			lines = append(lines, "", "General")
		}
		emit(ungrouped)
	}
	fmt.Fprintf(w, "\nInstalled %d skill%s\n", len(order), addPlural(len(order)))
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	if len(symlinkFailures) > 0 {
		fmt.Fprintf(w, "Symlinks failed for: %s\n", addFormatList(symlinkFailures, 5))
		fmt.Fprintln(w, "  Files were copied instead. On Windows, enable Developer Mode for symlink support.")
	}
}

// ---------------------------------------------------------------------------
// AddSkill: backward-compatible programmatic API.
// ---------------------------------------------------------------------------

// AddSkill installs the skills found in src into cwd (or globally with opts.Global).
// It keeps skl's original target resolution: explicit opts.Agent, else Antigravity when it
// is detected, the single detected agent, or the universal canonical directory when
// several agents are detected; with no agents detected it installs for Antigravity and the
// canonical directory and prints the agy-cli onboarding tip. Symlink mode is the default
// unless opts.Copy is set. Lock write failures are returned as errors (#2606).
func AddSkill(src string, opts AddOptions, cwd string) error {
	if src == "" {
		return errors.New("source skill directory is required")
	}
	cwd = installCwd(cwd)
	if opts.Cwd == "" {
		opts.Cwd = cwd
	}

	parsed, err := ParseSource(src, ParseSourceOptions{Cwd: cwd})
	if err != nil {
		return err
	}
	if parsed.Type == SourceTypeLocal {
		if _, err := os.Stat(parsed.LocalPath); err != nil {
			return ErrLocalPathNotFound(parsed.LocalPath)
		}
	}
	requested := append([]string{}, opts.Skill...)
	if parsed.SkillFilter != "" && !addHasString(requested, parsed.SkillFilter) {
		requested = append(requested, parsed.SkillFilter)
	}
	if opts.Verbose {
		fmt.Println(addSourceLine(parsed))
	}
	fetched, err := fetchSourceFn(SourceRequest{
		Source: src, Parsed: &parsed, Skills: requested, FullDepth: opts.FullDepth,
		IncludeInternal: len(requested) > 0 && !addHasString(requested, "*"), Yes: opts.Yes, Cwd: cwd,
	})
	if err != nil {
		return fmt.Errorf("fetch source: %w", err)
	}
	defer fetched.Cleanup()

	if len(fetched.Skills) == 0 {
		return fmt.Errorf("no skills (directories containing SKILL.md) found in source %q", src)
	}
	selected := fetched.Skills
	if len(requested) > 0 && !addHasString(requested, "*") {
		selected = addFilterFetched(fetched.Skills, requested)
		if len(selected) == 0 {
			if parsed.SkillFilter != "" {
				return fmt.Errorf("skill %q not found in source", parsed.SkillFilter)
			}
			return errors.New("no valid skills found in source")
		}
	}

	if opts.List {
		for _, s := range selected {
			fmt.Printf("- %s: %s (path: %s)\n", s.Name, s.Description, s.Path)
		}
		return nil
	}

	explicit := map[AgentType]bool{}
	var targetAgents []AgentType
	if len(opts.Agent) > 0 {
		targetAgents = opts.Agent
		for _, a := range opts.Agent {
			if _, ok := Agents[a]; !ok {
				return fmt.Errorf("unknown agent type: %s", a)
			}
			explicit[a] = true
		}
	} else {
		targetAgents = addLegacyTargets(opts, cwd)
	}

	mode := InstallModeSymlink
	if opts.Copy {
		mode = InstallModeCopy
	}
	units := make([]addUnit, len(selected))
	for i, s := range selected {
		units[i] = addUnit{src: fetched, skill: s}
	}
	plan := addPlan{
		units: units, targets: BuildInstallTargets(targetAgents, nil), targetAgents: targetAgents,
		global: opts.Global, mode: mode, lockedCopy: opts.Copy, explicit: explicit, cwd: cwd, verbose: opts.Verbose,
	}
	results := addExecuteInstall(plan)
	for _, r := range results {
		if !r.res.Success {
			return fmt.Errorf("install skill %q for agent %s: %s", r.unit.skill.Name, r.target.Agent, r.res.Error)
		}
		if opts.Verbose {
			status := "Installed"
			if r.res.Skipped {
				status = "Skipped"
			} else if r.res.SymlinkFailed {
				status = "Copied (symlink failed)"
			}
			fmt.Printf("🎯 Agent %s: %s at %s\n", r.target.Agent, status, r.res.Path)
		}
	}
	_, failures := addRecordLocks(plan, results, os.Stderr)
	if len(failures) > 0 {
		msgs := make([]string, len(failures))
		for i, f := range failures {
			msgs[i] = f.String()
		}
		return errors.New(strings.Join(msgs, "\n"))
	}
	return nil
}

func addLegacyTargets(opts AddOptions, cwd string) []AgentType {
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	detected := detectInstalledAgents(home, configHome, cwd)
	hasAgy := false
	var others []AgentType
	for _, a := range detected {
		if a == AgentAntigravity {
			hasAgy = true
		} else if a != AgentUniversal {
			others = append(others, a)
		}
	}
	switch {
	case hasAgy:
		return []AgentType{AgentAntigravity}
	case len(others) == 0:
		maybeSuggestAgy(opts)
		return []AgentType{AgentAntigravity, AgentUniversal}
	case len(others) == 1:
		return []AgentType{others[0]}
	default:
		return []AgentType{AgentUniversal}
	}
}

// findSkillDirectories is the legacy discovery walk kept for callers that predate
// DiscoverSkills (e.g. experimental_sync).
func findSkillDirectories(dir string, fullDepth bool) []string {
	var results []string
	seenNames := make(map[string]bool)
	skipDirs := map[string]bool{"node_modules": true, ".git": true, "dist": true, "build": true, "__pycache__": true}

	hasSkillMd := func(path string) bool {
		_, err := os.Stat(filepath.Join(path, "SKILL.md"))
		return err == nil
	}
	tryAddSkillAt := func(skillDir string) bool {
		if !hasSkillMd(skillDir) {
			return false
		}
		raw, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			return false
		}
		data, _, err := ParseFrontmatter(string(raw))
		if err != nil {
			return false
		}
		name, _ := data["name"].(string)
		if name == "" {
			return false
		}
		name = strings.ToLower(name)
		if seenNames[name] {
			return true
		}
		seenNames[name] = true
		results = append(results, skillDir)
		return true
	}

	if hasSkillMd(dir) {
		tryAddSkillAt(dir)
		if !fullDepth {
			return results
		}
	}

	priority := []string{
		dir,
		filepath.Join(dir, "skills"),
		filepath.Join(dir, "skills", ".curated"),
		filepath.Join(dir, "skills", ".experimental"),
		filepath.Join(dir, "skills", ".system"),
	}
	seen := map[string]bool{}
	for _, p := range priority {
		seen[p] = true
	}
	for _, k := range orderedAgentTypes(Agents) {
		if sd := Agents[k].SkillsDir; sd != "" {
			p := filepath.Join(dir, sd)
			if !seen[p] {
				priority = append(priority, p)
				seen[p] = true
			}
		}
	}
	for i, pDir := range priority {
		entries, err := os.ReadDir(pDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || skipDirs[entry.Name()] {
				continue
			}
			child := filepath.Join(pDir, entry.Name())
			if tryAddSkillAt(child) || i == 0 {
				continue
			}
			grand, err := os.ReadDir(child)
			if err != nil {
				continue
			}
			for _, g := range grand {
				if g.IsDir() && !skipDirs[g.Name()] {
					tryAddSkillAt(filepath.Join(child, g.Name()))
				}
			}
		}
	}

	if len(results) == 0 || fullDepth {
		var walk func(cur string, depth int)
		walk = func(cur string, depth int) {
			if depth > 5 {
				return
			}
			if hasSkillMd(cur) {
				tryAddSkillAt(cur)
			}
			entries, err := os.ReadDir(cur)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.IsDir() && !skipDirs[e.Name()] {
					walk(filepath.Join(cur, e.Name()), depth+1)
				}
			}
		}
		walk(dir, 0)
	}
	return results
}

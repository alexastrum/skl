package skl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ANSI styles shared by the lifecycle commands (list, remove, find, update, install, sync).
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[38;5;102m"
	ansiText   = "\x1b[38;5;145m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
)

var (
	lcWhitespaceRun = regexp.MustCompile(`\s+`)
	lcLegacyUnsafe  = regexp.MustCompile(`[/\\:\x00]`)
)

// ParseListOptions parses `skl list` flags. `-a/--agent` consumes every following
// non-flag token (comma-separated values are split too).
func ParseListOptions(args []string) ListOptions {
	var opts ListOptions
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "-g", "--global":
			opts.Global = true
		case "-p", "--project":
			opts.Global = false
		case "--json":
			opts.JSON = true
		case "-a", "--agent":
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.AgentFilter = append(opts.AgentFilter, splitAgentList(args[i])...)
			}
		}
	}
	return opts
}

// splitAgentList splits a comma-separated agent token into trimmed AgentTypes.
func splitAgentList(token string) []AgentType {
	var out []AgentType
	for _, part := range strings.Split(token, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, AgentType(p))
		}
	}
	return out
}

// sortedAgentKeys returns every configured agent key in sorted order.
func sortedAgentKeys() []string {
	keys := make([]string, 0, len(Agents))
	for k := range Agents {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}

// invalidAgentNames returns the requested agents that are not configured.
func invalidAgentNames(requested []AgentType) []string {
	var invalid []string
	for _, a := range requested {
		if _, ok := Agents[a]; !ok {
			invalid = append(invalid, string(a))
		}
	}
	return invalid
}

func containsAgent(list []AgentType, a AgentType) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

// lcAgentNativeDir returns the agent's own skills directory (ignoring canonical routing).
func lcAgentNativeDir(agentType AgentType, global bool, cwd string) string {
	cfg := Agents[agentType]
	if global {
		if cfg.GlobalSkillsDir == "" {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, cfg.SkillsDir)
		}
		return ResolvePath(cfg.GlobalSkillsDir, cwd)
	}
	return filepath.Join(cwd, cfg.SkillsDir)
}

// lcIsPathSafe reports whether target stays within base.
func lcIsPathSafe(base, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// isDirOrSymlinkToDir follows symlinks; dangling links and links to files are rejected.
func isDirOrSymlinkToDir(entry os.DirEntry, fullPath string) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink != 0 {
		st, err := os.Stat(fullPath)
		return err == nil && st.IsDir()
	}
	return false
}

// lcHasSkillMd reports whether dir/SKILL.md exists as a regular file (following links).
func lcHasSkillMd(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !st.IsDir()
}

// parseInstalledSkillMd parses an installed SKILL.md, requiring name and description.
func parseInstalledSkillMd(skillMdPath string) *Skill {
	skill, err := ParseSkillMd(skillMdPath, false)
	if err != nil || skill == nil || skill.Name == "" || skill.Description == "" {
		return nil
	}
	return skill
}

type listScope struct {
	global    bool
	path      string
	agentType AgentType
}

// ListInstalledSkills scans the canonical directory plus every detected (or present
// but undetected) agent directory for installed skills, matching upstream
// installer.ts listInstalledSkills (#2597).
func ListInstalledSkills(opts ListOptions) ([]InstalledSkill, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	filter := opts.AgentFilter
	if len(filter) == 0 && len(opts.Agent) > 0 {
		filter = opts.Agent
	}
	if len(filter) > 0 {
		var valid []AgentType
		for _, a := range filter {
			if _, ok := Agents[a]; ok {
				valid = append(valid, a)
			}
		}
		if len(valid) == 0 {
			return []InstalledSkill{}, nil
		}
		filter = valid
	}

	detected := DetectInstalledAgents(cwd)
	agentsToCheck := detected
	if len(filter) > 0 {
		agentsToCheck = nil
		for _, a := range detected {
			if containsAgent(filter, a) {
				agentsToCheck = append(agentsToCheck, a)
			}
		}
		// An explicitly requested universal agent reads the canonical dir, so it is
		// attributable even when its home folder was not detected.
		for _, a := range filter {
			if IsUniversalAgent(a) && a != AgentUniversal && !containsAgent(agentsToCheck, a) {
				agentsToCheck = append(agentsToCheck, a)
			}
		}
	}

	isGlobal := opts.Global
	scopes := []listScope{{global: isGlobal, path: GetCanonicalSkillsDir(isGlobal, cwd)}}
	hasScope := func(p string) bool {
		for _, s := range scopes {
			if s.global == isGlobal && s.path == p {
				return true
			}
		}
		return false
	}
	for _, a := range agentsToCheck {
		cfg := Agents[a]
		if isGlobal && cfg.GlobalSkillsDir == "" {
			continue
		}
		dir := lcAgentNativeDir(a, isGlobal, cwd)
		if !hasScope(dir) {
			scopes = append(scopes, listScope{global: isGlobal, path: dir, agentType: a})
		}
		if a == "eve" && !isGlobal {
			for _, sub := range GetEveSubagents(cwd) {
				subDir := GetEveSubagentSkillsDir(sub, cwd)
				if !hasScope(subDir) {
					scopes = append(scopes, listScope{global: false, path: subDir, agentType: a})
				}
			}
		}
	}
	// Fallback: agents not detected but whose skills dir exists (installed with --agent).
	for _, k := range sortedAgentKeys() {
		a := AgentType(k)
		if a == AgentUniversal {
			continue
		}
		if len(filter) > 0 && !containsAgent(filter, a) {
			continue
		}
		if containsAgent(agentsToCheck, a) {
			continue
		}
		cfg := Agents[a]
		if isGlobal && cfg.GlobalSkillsDir == "" {
			continue
		}
		dir := lcAgentNativeDir(a, isGlobal, cwd)
		if hasScope(dir) {
			continue
		}
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			scopes = append(scopes, listScope{global: isGlobal, path: dir, agentType: a})
		}
	}

	var order []string
	byKey := map[string]*InstalledSkill{}
	addAgent := func(s *InstalledSkill, a AgentType) {
		if !containsAgent(s.Agents, a) {
			s.Agents = append(s.Agents, a)
		}
	}

	for _, scope := range scopes {
		entries, err := os.ReadDir(scope.path)
		if err != nil {
			continue
		}
		scopeKey := "project"
		if scope.global {
			scopeKey = "global"
		}
		for _, entry := range entries {
			skillDir := filepath.Join(scope.path, entry.Name())
			if !isDirOrSymlinkToDir(entry, skillDir) || !lcHasSkillMd(skillDir) {
				continue
			}
			skill := parseInstalledSkillMd(filepath.Join(skillDir, "SKILL.md"))
			if skill == nil {
				continue
			}
			key := scopeKey + ":" + skill.Name
			newSkill := func(agents []AgentType) {
				if agents == nil {
					agents = []AgentType{}
				}
				byKey[key] = &InstalledSkill{
					Name:          skill.Name,
					Description:   skill.Description,
					Path:          skillDir,
					CanonicalPath: skillDir,
					Scope:         scopeKey,
					Agents:        agents,
				}
				order = append(order, key)
			}

			if scope.agentType != "" {
				if existing, ok := byKey[key]; ok {
					addAgent(existing, scope.agentType)
				} else {
					newSkill([]AgentType{scope.agentType})
				}
				continue
			}

			var installedAgents []AgentType
			for _, a := range agentsToCheck {
				if scope.global && Agents[a].GlobalSkillsDir == "" {
					continue
				}
				if agentHasSkill(a, scope.global, cwd, entry.Name(), skill.Name) {
					installedAgents = append(installedAgents, a)
				}
			}
			if existing, ok := byKey[key]; ok {
				for _, a := range installedAgents {
					addAgent(existing, a)
				}
			} else {
				newSkill(installedAgents)
			}
		}
	}

	out := make([]InstalledSkill, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out, nil
}

// agentHasSkill checks whether agentType's base dir holds the skill, first by folder
// name candidates, then by scanning SKILL.md names.
func agentHasSkill(agentType AgentType, global bool, cwd, folderName, skillName string) bool {
	base := GetAgentBaseDir(agentType, global, cwd, "")
	legacy := lcLegacyUnsafe.ReplaceAllString(lcWhitespaceRun.ReplaceAllString(strings.ToLower(skillName), "-"), "")
	seen := map[string]bool{}
	for _, name := range []string{folderName, SanitizeName(skillName), legacy} {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		candidate := filepath.Join(base, name)
		if !lcIsPathSafe(base, candidate) {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return false
	}
	for _, e := range entries {
		candidate := filepath.Join(base, e.Name())
		if !isDirOrSymlinkToDir(e, candidate) || !lcIsPathSafe(base, candidate) || !lcHasSkillMd(candidate) {
			continue
		}
		if s := parseInstalledSkillMd(filepath.Join(candidate, "SKILL.md")); s != nil && s.Name == skillName {
			return true
		}
	}
	return false
}

// ShortenPath replaces the home dir with ~ and cwd with . for display.
func ShortenPath(fullPath, cwd string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(fullPath, home) {
		return "~" + fullPath[len(home):]
	}
	if cwd != "" && strings.HasPrefix(fullPath, cwd) {
		return "." + fullPath[len(cwd):]
	}
	return fullPath
}

// formatList joins items, truncating after maxShow entries.
func formatList(items []string, maxShow int) string {
	if len(items) <= maxShow {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(items[:maxShow], ", "), len(items)-maxShow)
}

// listLockEntry is the scope-independent view of a lock entry used by list.
type listLockEntry struct {
	Source     string
	SourceURL  string
	SourceType string
	PluginName string
}

// ListJSONEntry is one element of `skl list --json` output.
type ListJSONEntry struct {
	Name       string   `json:"name"`
	Path       *string  `json:"path"`
	Scope      string   `json:"scope"`
	Agents     []string `json:"agents"`
	Source     *string  `json:"source"`
	SourceURL  *string  `json:"sourceUrl"`
	SourceType *string  `json:"sourceType"`
	Status     string   `json:"status"`
}

func strPtr(s string) *string { return &s }

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func agentDisplayNames(list []AgentType) []string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		if a == AgentUniversal {
			continue
		}
		if cfg, ok := Agents[a]; ok && cfg.DisplayName != "" {
			names = append(names, cfg.DisplayName)
		} else {
			names = append(names, string(a))
		}
	}
	return names
}

func padRight(s string, n int) string {
	if l := len([]rune(s)); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}

func titleCaseKebab(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p != "" {
			r := []rune(p)
			parts[i] = strings.ToUpper(string(r[0])) + string(r[1:])
		}
	}
	return strings.Join(parts, " ")
}

// RunList implements `skl list`, returning the process exit code.
func RunList(args []string, cwd string) int {
	return runList(args, cwd, os.Stdout, os.Stderr)
}

func runList(args []string, cwd string, stdout, stderr io.Writer) int {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	opts := ParseListOptions(args)
	opts.Cwd = cwd

	if len(opts.AgentFilter) > 0 {
		if invalid := invalidAgentNames(opts.AgentFilter); len(invalid) > 0 {
			fmt.Fprintln(stdout, FormatError(ErrInvalidAgents(invalid, sortedAgentKeys())))
			return 1
		}
	}

	installed, err := ListInstalledSkills(opts)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	locked := map[string]listLockEntry{}
	var lockKeys []string
	if opts.Global {
		gl, _ := ReadSkillLock()
		for k, e := range gl.Skills {
			locked[k] = listLockEntry{e.Source, e.SourceUrl, e.SourceType, e.PluginName}
			lockKeys = append(lockKeys, k)
		}
	} else {
		ll, err := ReadLocalLock(cwd)
		if err != nil {
			if !IsDiagnostic(err, "SKILLS_INVALID_LOCAL_LOCK") {
				fmt.Fprintf(stderr, "Error: %v\n", err)
				return 1
			}
			fmt.Fprintf(stderr, "%sWarning: %s%s\n", ansiYellow, FormatError(err), ansiReset)
		}
		for k, e := range ll.Skills {
			// Project lock entries carry no plugin name (upstream local-lock schema).
			locked[k] = listLockEntry{Source: e.Source, SourceURL: e.SourceUrl, SourceType: e.SourceType}
			lockKeys = append(lockKeys, k)
		}
	}
	sort.Strings(lockKeys)
	bySanitized := map[string]listLockEntry{}
	for _, k := range lockKeys {
		bySanitized[SanitizeName(k)] = locked[k]
	}
	getLock := func(name string) (listLockEntry, bool) {
		if e, ok := locked[name]; ok {
			return e, true
		}
		e, ok := bySanitized[SanitizeName(name)]
		return e, ok
	}

	installedNames := map[string]bool{}
	for _, s := range installed {
		installedNames[SanitizeName(s.Name)] = true
	}
	var missing []string
	if len(opts.AgentFilter) == 0 {
		for _, k := range lockKeys {
			if !installedNames[SanitizeName(k)] {
				missing = append(missing, k)
			}
		}
	}

	scopeName := "project"
	scopeLabel := "Project"
	if opts.Global {
		scopeName, scopeLabel = "global", "Global"
	}

	if opts.JSON {
		out := make([]ListJSONEntry, 0, len(installed)+len(missing))
		for _, s := range installed {
			e, ok := getLock(s.Name)
			entry := ListJSONEntry{
				Name:   s.Name,
				Path:   strPtr(s.CanonicalPath),
				Scope:  s.Scope,
				Agents: agentDisplayNames(s.Agents),
				Status: "untracked",
			}
			if ok {
				entry.Source = strPtr(e.Source)
				entry.SourceURL = optStr(e.SourceURL)
				entry.SourceType = strPtr(e.SourceType)
				entry.Status = "tracked"
			}
			out = append(out, entry)
		}
		for _, k := range missing {
			e := locked[k]
			out = append(out, ListJSONEntry{
				Name:       k,
				Scope:      scopeName,
				Agents:     []string{},
				Source:     strPtr(e.Source),
				SourceURL:  optStr(e.SourceURL),
				SourceType: strPtr(e.SourceType),
				Status:     "missing",
			})
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	}

	if len(installed) == 0 && len(missing) == 0 {
		fmt.Fprintf(stdout, "%sNo %s skills found.%s\n", ansiDim, scopeName, ansiReset)
		if opts.Global {
			fmt.Fprintf(stdout, "%sTry listing project skills without -g%s\n", ansiDim, ansiReset)
		} else {
			fmt.Fprintf(stdout, "%sTry listing global skills with -g%s\n", ansiDim, ansiReset)
		}
		return 0
	}

	printGroup := func(skills []InstalledSkill, indent bool) {
		maxName, maxPath := 0, 0
		for _, s := range skills {
			if l := len([]rune(SanitizeMetadata(s.Name))); l > maxName {
				maxName = l
			}
			if l := len([]rune(ShortenPath(s.CanonicalPath, cwd))); l > maxPath {
				maxPath = l
			}
		}
		prefix := ""
		if indent {
			prefix = "  "
		}
		for _, s := range skills {
			names := agentDisplayNames(s.Agents)
			agentInfo := ansiYellow + "not linked" + ansiReset
			if len(names) > 0 {
				agentInfo = formatList(names, 5)
			}
			sourceLabel := ansiYellow + "untracked" + ansiReset
			if e, ok := getLock(s.Name); ok && e.Source != "" {
				sourceLabel = SanitizeMetadata(e.Source)
			}
			fmt.Fprintf(stdout, "%s%s%s%s %s%s%s\n", prefix, ansiCyan, padRight(SanitizeMetadata(s.Name), maxName), ansiReset,
				ansiDim, padRight(ShortenPath(s.CanonicalPath, cwd), maxPath), ansiReset)
			fmt.Fprintf(stdout, "%s  %sAgents:%s %s  %sSource:%s %s\n", prefix, ansiDim, ansiReset, agentInfo, ansiDim, ansiReset, sourceLabel)
		}
		fmt.Fprintln(stdout)
	}

	fmt.Fprintf(stdout, "%s%s Skills%s\n\n", ansiBold, scopeLabel, ansiReset)

	groups := map[string][]InstalledSkill{}
	var ungrouped []InstalledSkill
	for _, s := range installed {
		if e, ok := getLock(s.Name); ok && e.PluginName != "" {
			groups[e.PluginName] = append(groups[e.PluginName], s)
		} else {
			ungrouped = append(ungrouped, s)
		}
	}
	if len(groups) > 0 {
		names := make([]string, 0, len(groups))
		for g := range groups {
			names = append(names, g)
		}
		sort.Strings(names)
		for _, g := range names {
			fmt.Fprintf(stdout, "%s%s%s\n", ansiBold, titleCaseKebab(g), ansiReset)
			printGroup(groups[g], true)
		}
		if len(ungrouped) > 0 {
			fmt.Fprintf(stdout, "%sGeneral%s\n", ansiBold, ansiReset)
			printGroup(ungrouped, true)
		}
	} else if len(installed) > 0 {
		printGroup(installed, false)
	}

	if len(missing) > 0 {
		fmt.Fprintf(stdout, "%sMissing%s %s(in the lock file, not on disk)%s\n", ansiBold, ansiReset, ansiDim, ansiReset)
		for _, k := range missing {
			fmt.Fprintf(stdout, "  %s%s%s  %sSource:%s %s\n", ansiCyan, SanitizeMetadata(k), ansiReset, ansiDim, ansiReset, SanitizeMetadata(locked[k].Source))
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

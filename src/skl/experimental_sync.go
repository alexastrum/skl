package skl

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SyncOptions configures `skl experimental_sync` (and the node_modules part of
// `skl install` restores).
type SyncOptions struct {
	Agent   []AgentType
	Yes     bool
	Force   bool // skl extension (-f/--force): replace destinations sync does not own
	Verbose bool // skl extension (-v/--verbose)
	Copy    bool // --copy: copy instead of linking the canonical dir into node_modules
	DryRun  bool // --dry-run
	// NoCleanup keeps skills no dependency provides anymore (--no-cleanup).
	NoCleanup bool
	// NoRemote ignores remote git entries of "skills" fields (--no-remote).
	NoRemote bool
	// FrozenLockfile: true (--frozen-lockfile), false (--no-frozen-lockfile), nil = IsCI().
	FrozenLockfile *bool
	Recursive      bool     // -r/--recursive: include workspace packages
	Include        []string // --include <pkg>[#<skill>] globs
	Exclude        []string // --exclude <pkg>[#<skill>] globs
	JSON           bool     // --json: machine-readable summary on stdout
}

// ParseSyncOptions parses sync/install flags. -a/--agent, --include and --exclude
// consume every following non-flag token (agents may also be comma-separated).
func ParseSyncOptions(args []string) SyncOptions {
	var opts SyncOptions
	take := func(i *int) []string {
		var vals []string
		for *i+1 < len(args) && !strings.HasPrefix(args[*i+1], "-") {
			*i++
			vals = append(vals, args[*i])
		}
		return vals
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-y", "--yes":
			opts.Yes = true
		case "-f", "--force":
			opts.Force = true
		case "-v", "--verbose":
			opts.Verbose = true
		case "--copy":
			opts.Copy = true
		case "--dry-run":
			opts.DryRun = true
		case "--no-cleanup":
			opts.NoCleanup = true
		case "--no-remote":
			opts.NoRemote = true
		case "--frozen-lockfile":
			t := true
			opts.FrozenLockfile = &t
		case "--no-frozen-lockfile":
			f := false
			opts.FrozenLockfile = &f
		case "-r", "--recursive":
			opts.Recursive = true
		case "--json":
			opts.JSON = true
		case "-a", "--agent":
			for _, v := range take(&i) {
				opts.Agent = append(opts.Agent, splitAgentList(v)...)
			}
		case "--include":
			opts.Include = append(opts.Include, take(&i)...)
		case "--exclude":
			opts.Exclude = append(opts.Exclude, take(&i)...)
		}
	}
	return opts
}

// PackageSkill is a skill shipped by an installed npm package.
type PackageSkill struct {
	Skill
	PackageName    string
	PackageVersion string
	// SkillPath is SKILL.md relative to the package root ("skills/pdf/SKILL.md").
	SkillPath string
	// Via is the package whose "skills" field requested it ("." = the project).
	Via string
	// Depth is 0 for direct dependencies and the project's own field, +1 per npm: hop.
	Depth int
}

// FieldRemoteRequest is a remote "skills" entry with the packages declaring it.
type FieldRemoteRequest struct {
	RemoteSkillsRequest
	Via       string
	Declarers []string
}

// SyncDiscovery is the result of DiscoverNodeModuleSkills.
type SyncDiscovery struct {
	Skills   []PackageSkill
	Remote   []FieldRemoteRequest
	Warnings []error
	Errors   []error
}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Workspaces           json.RawMessage   `json:"workspaces"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	Skills               json.RawMessage   `json:"skills"`
}

func readPackageJSON(dir string) *packageJSON {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var p packageJSON
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	return &p
}

func (p *packageJSON) deps() []string {
	set := map[string]bool{}
	for _, m := range []map[string]string{p.Dependencies, p.DevDependencies, p.OptionalDependencies, p.PeerDependencies} {
		for k := range m {
			set[k] = true
		}
	}
	return keysOf(set)
}

func (p *packageJSON) workspacePatterns() []string {
	if len(p.Workspaces) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(p.Workspaces, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(p.Workspaces, &obj) == nil {
		return obj.Packages
	}
	return nil
}

// discoverPackageSkills scans an installed package: a root SKILL.md, else
// skills/*/SKILL.md and dist/skills/*/SKILL.md.
func discoverPackageSkills(pkgDir, pkgName string, depth int) []PackageSkill {
	pkg := readPackageJSON(pkgDir)
	if pkg == nil {
		return nil
	}
	if lcHasSkillMd(pkgDir) {
		if s, _ := ParseSkillMd(filepath.Join(pkgDir, "SKILL.md"), false); s != nil {
			return []PackageSkill{{Skill: *s, PackageName: pkgName, PackageVersion: pkg.Version, SkillPath: "SKILL.md", Depth: depth}}
		}
	}
	var out []PackageSkill
	for _, sub := range []string{"skills", "dist/skills"} {
		entries, err := os.ReadDir(filepath.Join(pkgDir, filepath.FromSlash(sub)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(pkgDir, filepath.FromSlash(sub), e.Name())
			if !lcHasSkillMd(dir) {
				continue
			}
			if s, _ := ParseSkillMd(filepath.Join(dir, "SKILL.md"), false); s != nil {
				out = append(out, PackageSkill{Skill: *s, PackageName: pkgName, PackageVersion: pkg.Version, SkillPath: sub + "/" + e.Name() + "/SKILL.md", Depth: depth})
			}
		}
	}
	return out
}

// findInstalledPackage mirrors Node's lookup: walk up from `from` checking
// node_modules/<name>/package.json (works for hoisted, scoped and pnpm layouts).
func findInstalledPackage(from, name string) string {
	for dir := from; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// globToRegexp converts a posix glob (*, **, ?) to an anchored regexp.
func globToRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func matchesGlob(name, pattern string) bool {
	return globToRegexp(pattern).MatchString(name)
}

// matchesSyncPattern: "<pkg>" matches every skill of a package, "<pkg>#<skill>"
// one of them; both parts are globs.
func matchesSyncPattern(name, pkgName string, patterns []string) bool {
	for _, p := range patterns {
		pkgPat, skillPat := p, "**"
		if i := strings.IndexByte(p, '#'); i != -1 {
			pkgPat, skillPat = p[:i], p[i+1:]
		}
		if matchesGlob(pkgName, pkgPat) && matchesGlob(SanitizeName(name), skillPat) {
			return true
		}
	}
	return false
}

func allowedByFilters(name, pkgName string, opts SyncOptions) bool {
	return (len(opts.Include) == 0 || matchesSyncPattern(name, pkgName, opts.Include)) &&
		!(len(opts.Exclude) > 0 && matchesSyncPattern(name, pkgName, opts.Exclude))
}

func remoteAllowsSkill(r FieldRemoteRequest, name string, opts SyncOptions) bool {
	for _, d := range r.Declarers {
		if allowedByFilters(name, d, opts) {
			return true
		}
	}
	return false
}

// filterRemoteRequests drops excluded declarers before fetching.
func filterRemoteRequests(reqs []FieldRemoteRequest, opts SyncOptions) []FieldRemoteRequest {
	var out []FieldRemoteRequest
	for _, r := range reqs {
		var declarers []string
		for _, d := range r.Declarers {
			included := len(opts.Include) == 0
			for _, p := range opts.Include {
				if matchesGlob(d, strings.SplitN(p, "#", 2)[0]) {
					included = true
				}
			}
			excluded := false
			for _, p := range opts.Exclude {
				if !strings.Contains(p, "#") && matchesGlob(d, p) {
					excluded = true
				}
			}
			if included && !excluded {
				declarers = append(declarers, d)
			}
		}
		if len(declarers) == 0 {
			continue
		}
		f := r
		f.Via, f.Declarers = declarers[0], declarers
		if len(r.Skills) == 0 {
			out = append(out, f)
			continue
		}
		var skills []string
		for _, s := range r.Skills {
			if remoteAllowsSkill(f, s, opts) {
				skills = append(skills, s)
			}
		}
		if len(skills) > 0 {
			f.Skills = skills
			out = append(out, f)
		}
	}
	return out
}

// findWorkspacePackages returns workspace package dirs declared in package.json
// "workspaces" or pnpm-workspace.yaml (with "!" negations).
func findWorkspacePackages(cwd string, pkg *packageJSON) []string {
	patterns := pkg.workspacePatterns()
	if data, err := os.ReadFile(filepath.Join(cwd, "pnpm-workspace.yaml")); err == nil {
		var pnpm struct {
			Packages []interface{} `yaml:"packages"`
		}
		if yaml.Unmarshal(data, &pnpm) == nil {
			for _, p := range pnpm.Packages {
				if s, ok := p.(string); ok {
					patterns = append(patterns, s)
				}
			}
		}
	}
	var included []*regexp.Regexp
	var negated []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			negated = append(negated, strings.TrimPrefix(p, "!"))
		} else {
			included = append(included, globToRegexp(path.Join(p, "package.json")))
		}
	}
	if len(included) == 0 {
		return nil
	}
	set := map[string]bool{}
	_ = filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "package.json" {
			return nil
		}
		rel, _ := filepath.Rel(cwd, p)
		rel = filepath.ToSlash(rel)
		for _, re := range included {
			if re.MatchString(rel) {
				dir := path.Dir(rel)
				if dir == "." {
					break
				}
				neg := false
				for _, n := range negated {
					if matchesGlob(dir, n) {
						neg = true
					}
				}
				if !neg {
					set[filepath.Join(cwd, filepath.FromSlash(dir))] = true
				}
				break
			}
		}
		return nil
	})
	return keysOf(set)
}

type syncDeclarer struct {
	name  string
	dir   string
	depth int
}

// DiscoverNodeModuleSkills finds skills shipped by the project's direct
// dependencies (and, with recursive, its workspace packages), plus skills that
// package.json "skills" fields request via npm: entries or remote git sources.
// Only declared packages are scanned (upstream sync.ts).
func DiscoverNodeModuleSkills(cwd string, recursive bool) SyncDiscovery {
	var res SyncDiscovery
	pkg := readPackageJSON(cwd)
	if pkg == nil {
		return res
	}
	seen := map[string]bool{}
	add := func(found []PackageSkill) {
		for _, s := range found {
			key, err := filepath.EvalSymlinks(s.Path)
			if err != nil {
				key = s.Path
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			res.Skills = append(res.Skills, s)
		}
	}
	type project struct {
		name  string
		dir   string
		pkg   *packageJSON
		depth int
	}
	projects := []project{{".", cwd, pkg, 0}}
	if recursive {
		for _, dir := range findWorkspacePackages(cwd, pkg) {
			wp := readPackageJSON(dir)
			if wp == nil {
				continue
			}
			name := wp.Name
			if name == "" {
				rel, _ := filepath.Rel(cwd, dir)
				name = filepath.ToSlash(rel)
			}
			projects = append(projects, project{name, dir, wp, 1})
		}
	}
	var declarers []syncDeclarer
	for _, p := range projects {
		var installed []syncDeclarer
		for _, dep := range p.pkg.deps() {
			if dir := findInstalledPackage(p.dir, dep); dir != "" {
				installed = append(installed, syncDeclarer{dep, dir, p.depth + 1})
				add(discoverPackageSkills(dir, dep, p.depth))
			}
		}
		declarers = append(declarers, syncDeclarer{p.name, p.dir, p.depth})
		declarers = append(declarers, installed...)
	}

	remoteIdx := map[string]int{}
	visited := map[string]bool{}
	for i := 0; i < len(declarers); i++ {
		d := declarers[i]
		dir, err := filepath.EvalSymlinks(d.dir)
		if err != nil || visited[dir] {
			continue
		}
		visited[dir] = true
		isRoot := d.name == "."
		report := func(e error) {
			if isRoot {
				res.Errors = append(res.Errors, e)
			} else {
				res.Warnings = append(res.Warnings, e)
			}
		}
		dp := readPackageJSON(dir)
		if dp == nil || len(dp.Skills) == 0 || string(dp.Skills) == "null" {
			continue
		}
		var field []interface{}
		if json.Unmarshal(dp.Skills, &field) != nil {
			// A dependency may use the key for something else.
			if isRoot {
				res.Errors = append(res.Errors, ErrPackageSkillsNotArray())
			}
			continue
		}
		parsed := ParseSkillsField(field, d.name)
		for _, e := range parsed.Errors {
			report(e)
		}
		for _, r := range parsed.Remote {
			keyBytes, _ := json.Marshal([]interface{}{r.Parsed.URL, r.Parsed.Ref, r.Parsed.Subpath, r.Skills})
			key := string(keyBytes)
			if idx, ok := remoteIdx[key]; ok {
				if !contains(res.Remote[idx].Declarers, d.name) {
					res.Remote[idx].Declarers = append(res.Remote[idx].Declarers, d.name)
				}
				continue
			}
			remoteIdx[key] = len(res.Remote)
			res.Remote = append(res.Remote, FieldRemoteRequest{RemoteSkillsRequest: r, Via: d.name, Declarers: []string{d.name}})
		}
		for _, r := range parsed.Npm {
			target := findInstalledPackage(dir, r.Package)
			if target == "" {
				report(ErrPackageSkillsUnresolvedNPM(d.name, r.Package))
				continue
			}
			var keep []PackageSkill
			for _, s := range discoverPackageSkills(target, r.Package, d.depth) {
				if len(r.Skills) == 0 || contains(r.Skills, filepath.Base(s.Path)) || contains(r.Skills, SanitizeName(s.Name)) {
					s.Via = d.name
					keep = append(keep, s)
				}
			}
			add(keep)
			declarers = append(declarers, syncDeclarer{r.Package, target, d.depth + 1})
		}
	}
	return res
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isUnderNodeModules(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == "node_modules" {
			return true
		}
	}
	return false
}

// installedBySync reports whether sync wrote this lock entry (shipped by a
// package, legacy skl "npm" entries, or requested by a "skills" field).
func installedBySync(e *LocalSkillLockEntry) bool {
	return e != nil && (e.SourceType == "node_modules" || e.SourceType == "npm" || e.Via != "")
}

// foreignDestination explains why dest must not be touched ("" when it is free
// or owned by sync).
func foreignDestination(dest, canonicalDir string, entry *LocalSkillLockEntry, cwd string) string {
	fi, err := os.Lstat(dest)
	if err != nil {
		return ""
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		link, _ := os.Readlink(dest)
		target := link
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(dest), link)
		}
		target = filepath.Clean(target)
		if isUnderNodeModules(target) || target == filepath.Clean(canonicalDir) {
			return ""
		}
		return fmt.Sprintf("%s is a symlink to %s", ShortenPath(dest, cwd), ShortenPath(target, cwd))
	}
	if installedBySync(entry) {
		return ""
	}
	return fmt.Sprintf("%s already exists and was not installed by sync", ShortenPath(dest, cwd))
}

func linksIntoNodeModules(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	link, err := os.Readlink(p)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(p), link)
	}
	return isUnderNodeModules(link)
}

func syncLockEntry(lock LocalSkillLockFile, name string) *LocalSkillLockEntry {
	if e, ok := lock.Skills[name]; ok {
		return &e
	}
	s := SanitizeName(name)
	for _, k := range keysOf(lock.Skills) {
		if SanitizeName(k) == s {
			e := lock.Skills[k]
			return &e
		}
	}
	return nil
}

// PruneStaleSkills removes skills that sync installed earlier and nothing provides
// anymore, returning their names. With dryRun nothing is changed.
func PruneStaleSkills(cwd string, keep map[string]bool, keepRemote func(LocalSkillLockEntry, string) bool, lock *LocalSkillLockFile, dryRun bool) ([]string, error) {
	canonicalBase := GetCanonicalSkillsDir(false, cwd)
	lockKeys := map[string]string{}
	explicit := map[string]bool{}
	for _, k := range keysOf(lock.Skills) {
		e := lock.Skills[k]
		remote := e.SourceType != "node_modules" && e.SourceType != "npm"
		if installedBySync(&e) && !(remote && keepRemote(e, k)) {
			lockKeys[SanitizeName(k)] = k
		} else {
			explicit[SanitizeName(k)] = true
		}
	}
	candidates := map[string]bool{}
	for k := range lockKeys {
		candidates[k] = true
	}
	if entries, err := os.ReadDir(canonicalBase); err == nil {
		for _, e := range entries {
			if linksIntoNodeModules(filepath.Join(canonicalBase, e.Name())) {
				candidates[e.Name()] = true
			}
		}
	}
	var stale []string
	for name := range candidates {
		if !keep[name] && !explicit[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if dryRun || len(stale) == 0 {
		return stale, nil
	}
	for _, name := range stale {
		canonicalDir := filepath.Join(canonicalBase, name)
		var entry *LocalSkillLockEntry
		key, hasKey := lockKeys[name]
		if hasKey {
			e := lock.Skills[key]
			entry = &e
		}
		// Destinations as (dir, element) pairs so removal stays confined to each dir.
		type dest struct{ dir, elem string }
		seenDest := map[string]bool{}
		dests := []dest{{canonicalBase, name}}
		for _, a := range sortedAgentKeys() {
			dests = append(dests, dest{GetAgentBaseDir(AgentType(a), false, cwd, ""), SanitizeName(name)})
		}
		for _, d := range dests {
			p := filepath.Join(d.dir, d.elem)
			if seenDest[p] {
				continue
			}
			seenDest[p] = true
			if foreignDestination(p, canonicalDir, entry, cwd) == "" {
				_ = lcRemoveSkillDir(d.dir, d.elem)
			}
		}
		if hasKey {
			delete(lock.Skills, key)
		}
	}
	return stale, WriteLocalLock(*lock, cwd)
}

// blockedReason explains why sync must not install name ("" when allowed): never
// shadow a skill installed with `skl add`, never replace a destination sync does
// not own (unless force).
func blockedReason(name string, targets []AgentType, entry *LocalSkillLockEntry, cwd string, force bool) string {
	if entry != nil && !installedBySync(entry) {
		return "installed with `skl add` from " + entry.Source
	}
	if force {
		return ""
	}
	canonicalDir := GetCanonicalPath(name, false, cwd)
	dests := []string{canonicalDir}
	for _, a := range targets {
		dests = append(dests, GetInstallPath(name, a, false, cwd))
	}
	for _, d := range dests {
		if r := foreignDestination(d, canonicalDir, entry, cwd); r != "" {
			return r
		}
	}
	return ""
}

// SyncSkipped is a shipped skill that sync did not install, and why.
type SyncSkipped struct {
	Skill  PackageSkill
	Reason string
}

// ResolveSyncConflicts applies the conflict rules: the closest package wins; two
// packages at the same depth shipping one name install neither; blockedReason.
func ResolveSyncConflicts(skills []PackageSkill, targets []AgentType, lock LocalSkillLockFile, cwd string, force bool) ([]PackageSkill, []SyncSkipped) {
	var order []string
	byName := map[string][]PackageSkill{}
	for _, s := range skills {
		n := SanitizeName(s.Name)
		if _, ok := byName[n]; !ok {
			order = append(order, n)
		}
		byName[n] = append(byName[n], s)
	}
	var install []PackageSkill
	var skipped []SyncSkipped
	for _, name := range order {
		all := byName[name]
		minDepth := all[0].Depth
		for _, s := range all {
			if s.Depth < minDepth {
				minDepth = s.Depth
			}
		}
		var cands []PackageSkill
		for _, s := range all {
			if s.Depth == minDepth {
				cands = append(cands, s)
			}
		}
		for _, s := range all {
			if s.Depth != minDepth {
				skipped = append(skipped, SyncSkipped{s, cands[0].PackageName + " is closer to the project"})
			}
		}
		if len(cands) > 1 {
			pkgs := make([]string, len(cands))
			for i, c := range cands {
				pkgs[i] = c.PackageName
			}
			for _, c := range cands {
				skipped = append(skipped, SyncSkipped{c, fmt.Sprintf("shipped by %s; drop this one with --exclude %s#%s", strings.Join(pkgs, ", "), c.PackageName, name)})
			}
			continue
		}
		if r := blockedReason(name, targets, syncLockEntry(lock, name), cwd, force); r != "" {
			skipped = append(skipped, SyncSkipped{cands[0], r})
			continue
		}
		install = append(install, cands[0])
	}
	return install, skipped
}

func projectLockSource(p ParsedSource) string {
	if s := LockSource(p.URL, GetOwnerRepo(p)); s != "" {
		return s
	}
	return p.URL
}

func isFromRequest(e LocalSkillLockEntry, r FieldRemoteRequest) bool {
	return e.Via != "" && e.Source == projectLockSource(r.Parsed) && e.Ref == r.Parsed.Ref
}

func requestsLockedSkill(r FieldRemoteRequest, name string, e LocalSkillLockEntry) bool {
	if !isFromRequest(e, r) {
		return false
	}
	if len(r.Skills) == 0 {
		return true
	}
	folder := path.Base(path.Dir(e.SkillPath))
	for _, s := range r.Skills {
		if SanitizeName(s) == SanitizeName(name) || s == folder {
			return true
		}
	}
	return false
}

func isRemoteInstalled(r FieldRemoteRequest, lock LocalSkillLockFile, cwd string) bool {
	var installed []string
	for _, k := range keysOf(lock.Skills) {
		if isFromRequest(lock.Skills[k], r) {
			if _, err := os.Stat(GetCanonicalPath(k, false, cwd)); err == nil {
				installed = append(installed, SanitizeName(k))
			}
		}
	}
	if len(r.Skills) == 0 {
		return len(installed) > 0
	}
	for _, s := range r.Skills {
		if !contains(installed, SanitizeName(s)) {
			return false
		}
	}
	return true
}

// syncFetch materializes remote "skills" entries (overridable in tests).
var syncFetch = FetchSource

// SyncJSONResult is the --json summary of a sync run.
type SyncJSONResult struct {
	Synced  []SyncJSONSkill `json:"synced"`
	Removed []string        `json:"removed"`
	Skipped []SyncJSONSkill `json:"skipped"`
	Failed  []SyncJSONSkill `json:"failed"`
}

// SyncJSONSkill is one skill in the --json summary.
type SyncJSONSkill struct {
	Name    string `json:"name"`
	Package string `json:"package,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// RunSync implements `skl experimental_sync`, returning the process exit code.
func RunSync(args []string, opts SyncOptions, cwd string) int {
	return runSync(opts, cwd, os.Stdout, os.Stderr)
}

// SyncSkills is the library form of RunSync.
func SyncSkills(args []string, opts SyncOptions, cwd string) error {
	if len(args) > 0 {
		parsed := ParseSyncOptions(args)
		parsed.Agent = append(parsed.Agent, opts.Agent...)
		parsed.Yes = parsed.Yes || opts.Yes
		parsed.Force = parsed.Force || opts.Force
		parsed.Verbose = parsed.Verbose || opts.Verbose
		opts = parsed
	}
	var errb strings.Builder
	if code := runSync(opts, cwd, io.Discard, &errb); code != 0 {
		msg := strings.TrimSpace(StripTerminalEscapes(errb.String()))
		if msg == "" {
			msg = "sync failed"
		}
		return errors.New(msg)
	}
	return nil
}

func runSync(opts SyncOptions, cwd string, stdout, stderr io.Writer) int {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	jsonOut := stdout
	result := SyncJSONResult{Synced: []SyncJSONSkill{}, Removed: []string{}, Skipped: []SyncJSONSkill{}, Failed: []SyncJSONSkill{}}
	if opts.JSON {
		stdout = io.Discard
		opts.Yes = true
	}
	finish := func(code int) int {
		if opts.JSON {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(jsonOut, string(data))
		}
		return code
	}

	if det := DetectAgent(); det.IsAgent {
		opts.Yes = true
		name := ""
		if det.Agent != nil {
			name = det.Agent.Name
		}
		if len(opts.Agent) == 0 {
			if t, ok := GetAgentType(name); ok {
				list := []AgentType{t}
				for _, ua := range GetUniversalAgents() {
					if !containsAgent(list, ua) {
						list = append(list, ua)
					}
				}
				opts.Agent = list
			}
		}
		fmt.Fprintf(stdout, "%s Agent detected - installing non-interactively\n", name)
	}

	disc := DiscoverNodeModuleSkills(cwd, opts.Recursive)
	if len(disc.Errors) > 0 {
		fmt.Fprintf(stderr, "%sInvalid skills field%s\n", ansiRed, ansiReset)
		for _, e := range disc.Errors {
			fmt.Fprintln(stderr, FormatError(e))
			result.Failed = append(result.Failed, SyncJSONSkill{Name: "package.json", Reason: FormatError(e)})
		}
		fmt.Fprintln(stderr, `Fix the "skills" field in package.json and run sync again.`)
		return finish(1)
	}
	var discovered []PackageSkill
	for _, s := range disc.Skills {
		if allowedByFilters(s.Name, s.PackageName, opts) {
			discovered = append(discovered, s)
		}
	}
	remote := filterRemoteRequests(disc.Remote, opts)
	if len(discovered) == 0 {
		fmt.Fprintf(stdout, "%sNo skills found%s\n", ansiYellow, ansiReset)
	} else {
		fmt.Fprintf(stdout, "Found %s%d%s skill%s in node_modules\n", ansiGreen, len(discovered), ansiReset, plural(len(discovered)))
	}
	for _, w := range disc.Warnings {
		fmt.Fprintf(stderr, "%sWarning: %s%s\n", ansiYellow, FormatError(w), ansiReset)
	}

	lock, err := ReadLocalLock(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %s\n", FormatError(err))
		return finish(1)
	}
	frozen := IsCI()
	if opts.FrozenLockfile != nil {
		frozen = *opts.FrozenLockfile
	}

	if !opts.NoCleanup {
		keep := map[string]bool{}
		for _, s := range discovered {
			keep[SanitizeName(s.Name)] = true
		}
		keepRemote := func(e LocalSkillLockEntry, name string) bool {
			if opts.NoRemote {
				return true
			}
			for _, r := range remote {
				if requestsLockedSkill(r, name, e) && remoteAllowsSkill(r, name, opts) {
					return true
				}
			}
			return false
		}
		stale, err := PruneStaleSkills(cwd, keep, keepRemote, &lock, frozen || opts.DryRun)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return finish(1)
		}
		if frozen && len(stale) > 0 {
			var changes []string
			for _, n := range stale {
				changes = append(changes, n+" is no longer provided by a dependency")
			}
			fmt.Fprintln(stderr, FrozenLockfileError(changes).Error())
			return finish(1)
		}
		verb := "Removed"
		if opts.DryRun {
			verb = "Would remove"
		}
		for _, n := range stale {
			fmt.Fprintf(stdout, "%s %s%s%s %s(no longer provided by a dependency)%s\n", verb, ansiCyan, n, ansiReset, ansiDim, ansiReset)
			result.Removed = append(result.Removed, n)
		}
	}

	var remoteReqs []FieldRemoteRequest
	if !opts.NoRemote {
		for _, r := range remote {
			if !isRemoteInstalled(r, lock, cwd) {
				remoteReqs = append(remoteReqs, r)
			}
		}
	}
	type cachedSkill struct {
		skill  Skill
		source string
		agents []AgentType
	}
	var cached []cachedSkill
	if !opts.NoRemote {
		shipped := map[string]bool{}
		for _, s := range discovered {
			shipped[SanitizeName(s.Name)] = true
		}
		for _, k := range keysOf(lock.Skills) {
			e := lock.Skills[k]
			if shipped[SanitizeName(k)] {
				continue
			}
			match := false
			for _, r := range remote {
				pending := false
				for _, rr := range remoteReqs {
					if rr.Parsed.URL == r.Parsed.URL && rr.Parsed.Ref == r.Parsed.Ref && rr.Via == r.Via {
						pending = true
					}
				}
				if !pending && requestsLockedSkill(r, k, e) && remoteAllowsSkill(r, k, opts) {
					match = true
				}
			}
			if !match {
				continue
			}
			if s, _ := ParseSkillMd(filepath.Join(GetCanonicalPath(k, false, cwd), "SKILL.md"), true); s != nil {
				cached = append(cached, cachedSkill{skill: *s, source: e.Source})
			}
		}
	}

	if len(discovered) == 0 && len(remoteReqs) == 0 && len(cached) == 0 {
		fmt.Fprintf(stdout, "%sNothing to sync.%s\n", ansiDim, ansiReset)
		return finish(0)
	}
	for _, s := range discovered {
		fmt.Fprintf(stdout, "%s%s%s %sfrom %s%s\n", ansiCyan, s.Name, ansiReset, ansiDim, s.PackageName, ansiReset)
		if s.Description != "" {
			fmt.Fprintf(stdout, "%s  %s%s\n", ansiDim, s.Description, ansiReset)
		}
	}

	// Select agents.
	var targets []AgentType
	universal := GetUniversalAgents()
	prompt := newPromptTerm(syncStdin, stdout, stdinIsTTY())
	switch {
	case containsAgent(opts.Agent, "*"):
		for _, k := range sortedAgentKeys() {
			if AgentType(k) != AgentUniversal {
				targets = append(targets, AgentType(k))
			}
		}
		fmt.Fprintf(stdout, "Installing to all %d agents\n", len(targets))
	case len(opts.Agent) > 0:
		if invalid := invalidAgentNames(opts.Agent); len(invalid) > 0 {
			fmt.Fprintln(stderr, FormatError(ErrInvalidAgents(invalid, sortedAgentKeys())))
			return finish(1)
		}
		targets = opts.Agent
	default:
		installed := DetectInstalledAgents(cwd)
		withUniversal := func(list []AgentType) []AgentType {
			out := append([]AgentType{}, list...)
			for _, ua := range universal {
				if !containsAgent(out, ua) {
					out = append(out, ua)
				}
			}
			return out
		}
		switch {
		case len(installed) == 0 && opts.Yes:
			targets = universal
			fmt.Fprintln(stdout, "Installing to universal agents")
		case len(installed) == 1 || opts.Yes || !stdinIsTTY():
			targets = withUniversal(installed)
		default:
			// No detected agents: offer all; several: offer the detected ones.
			var choices, fallback []AgentType
			for _, a := range GetNonUniversalAgents() {
				if a != AgentUniversal && (len(installed) == 0 || containsAgent(installed, a)) {
					choices = append(choices, a)
				}
			}
			for _, a := range installed {
				if !containsAgent(universal, a) {
					fallback = append(fallback, a)
				}
			}
			picked, err := promptSyncAgents(prompt, choices, fallback)
			if err != nil {
				fmt.Fprintln(stdout, "Sync cancelled")
				return finish(0)
			}
			targets = picked
		}
	}

	toInstall, skipped := ResolveSyncConflicts(discovered, targets, lock, cwd, opts.Force)
	for _, s := range skipped {
		fmt.Fprintf(stderr, "%sSkipped %s from %s: %s%s\n", ansiYellow, s.Skill.Name, s.Skill.PackageName, s.Reason, ansiReset)
		result.Skipped = append(result.Skipped, SyncJSONSkill{Name: s.Skill.Name, Package: s.Skill.PackageName, Reason: s.Reason})
	}
	var cachedToInstall []cachedSkill
	for _, c := range cached {
		var missing []AgentType
		for _, a := range targets {
			if _, err := os.Stat(GetInstallPath(c.skill.Name, a, false, cwd)); err != nil {
				missing = append(missing, a)
			}
		}
		if len(missing) == 0 {
			continue
		}
		if r := blockedReason(c.skill.Name, missing, syncLockEntry(lock, c.skill.Name), cwd, opts.Force); r != "" {
			fmt.Fprintf(stderr, "%sSkipped %s: %s%s\n", ansiYellow, c.skill.Name, r, ansiReset)
			continue
		}
		c.agents = missing
		cachedToInstall = append(cachedToInstall, c)
	}

	hashes := map[string]string{}
	for _, s := range toInstall {
		h, err := ComputeSkillFolderHash(s.Path)
		if err == nil {
			hashes[s.Name] = h
		}
	}
	if frozen {
		var changes []string
		for _, s := range toInstall {
			if r := LockChangeReason(lock, s.Name, LocalSkillLockEntry{ComputedHash: hashes[s.Name], Version: s.PackageVersion}); r != "" {
				changes = append(changes, r)
			}
		}
		if len(changes) > 0 {
			fmt.Fprintln(stderr, FrozenLockfileError(changes).Error())
			return finish(1)
		}
	}
	if len(toInstall) == 0 && len(remoteReqs) == 0 && len(cachedToInstall) == 0 {
		fmt.Fprintf(stdout, "%sNothing to sync.%s\n", ansiYellow, ansiReset)
		return finish(0)
	}

	mode := InstallModeLink
	if opts.Copy {
		mode = InstallModeCopy
	}
	fmt.Fprintf(stdout, "\n%sSync Summary%s\n", ansiBold, ansiReset)
	for _, s := range toInstall {
		arrow := "->"
		if mode == InstallModeCopy {
			arrow = "copied from"
		}
		fmt.Fprintf(stdout, "  %s%s%s %s<- %s%s\n", ansiCyan, s.Name, ansiReset, ansiDim, s.PackageName, ansiReset)
		fmt.Fprintf(stdout, "    %s%s %s %s%s\n", ansiDim, ShortenPath(GetCanonicalPath(s.Name, false, cwd), cwd), arrow, ShortenPath(s.Path, cwd), ansiReset)
	}
	for _, r := range remoteReqs {
		fmt.Fprintf(stdout, "  %s%s%s %s<- %s (remote)%s\n", ansiCyan, projectLockSource(r.Parsed), ansiReset, ansiDim, r.Via, ansiReset)
	}
	for _, c := range cachedToInstall {
		names := make([]string, len(c.agents))
		for i, a := range c.agents {
			names[i] = string(a)
		}
		fmt.Fprintf(stdout, "  %s%s%s %s<- %s (cached), link to %s%s\n", ansiCyan, c.skill.Name, ansiReset, ansiDim, c.source, strings.Join(names, ", "), ansiReset)
	}
	if opts.DryRun {
		fmt.Fprintf(stdout, "%sDry run, nothing changed.%s\n", ansiDim, ansiReset)
		return finish(0)
	}
	if !opts.Yes {
		if !confirmOrLine(prompt, bufio.NewReader(syncStdin), "Proceed with sync?") {
			fmt.Fprintln(stdout, "Sync cancelled")
			return finish(0)
		}
	}

	type syncResult struct {
		skill, pkg, agent, canonical, err string
		ok                                bool
	}
	var results []syncResult
	for _, c := range cachedToInstall {
		for _, a := range c.agents {
			res := InstallSkillForAgent(c.skill, a, InstallOptions{Cwd: cwd, Mode: InstallModeSymlink, CreateMissingAgentRoot: true})
			results = append(results, syncResult{c.skill.Name, c.source, Agents[a].DisplayName, res.CanonicalPath, res.Error, res.Success})
		}
	}
	for _, s := range toInstall {
		for _, a := range targets {
			m := mode
			if a == "eve" {
				m = InstallModeCopy
			}
			res := InstallSkillForAgent(s.Skill, a, InstallOptions{Cwd: cwd, Mode: m})
			results = append(results, syncResult{s.Name, s.PackageName, Agents[a].DisplayName, res.CanonicalPath, res.Error, res.Success})
		}
	}
	okNames := map[string]bool{}
	for _, r := range results {
		if r.ok {
			okNames[r.skill] = true
		}
	}
	if !frozen {
		for _, s := range toInstall {
			if !okNames[s.Name] {
				continue
			}
			_ = AddSkillToLocalLock(s.Name, LocalSkillLockEntry{
				Source: s.PackageName, SourceType: "node_modules", SkillPath: s.SkillPath,
				Version: s.PackageVersion, Via: s.Via, ComputedHash: hashes[s.Name],
			}, cwd)
		}
	}

	exit := 0
	claimed := map[string]bool{}
	for _, s := range discovered {
		claimed[SanitizeName(s.Name)] = true
	}
	var frozenLock *LocalSkillLockFile
	if frozen {
		frozenLock = &lock
	}
	for _, r := range remoteReqs {
		label := projectLockSource(r.Parsed)
		parsed := r.Parsed
		fetched, ferr := syncFetch(SourceRequest{Source: label, Parsed: &parsed, Skills: r.Skills, Yes: true, Cwd: cwd})
		if ferr != nil || fetched == nil {
			if ferr == nil {
				ferr = errors.New("no skills found")
			}
			fmt.Fprintf(stderr, "Failed to install from %s: %s\n", label, FormatError(ferr))
			result.Failed = append(result.Failed, SyncJSONSkill{Name: label, Reason: FormatError(ferr)})
			exit = 1
			continue
		}
		var names []string
		for _, s := range fetched.Skills {
			name := SanitizeName(s.Name)
			if len(r.Skills) > 0 {
				wanted := false
				for _, w := range r.Skills {
					if strings.EqualFold(w, s.Name) || SanitizeName(w) == name || w == filepath.Base(s.Path) {
						wanted = true
					}
				}
				if !wanted {
					continue
				}
			}
			if !remoteAllowsSkill(r, name, opts) {
				continue
			}
			reason := ""
			if claimed[name] {
				reason = "another source in this sync provides it"
			} else {
				reason = blockedReason(name, targets, syncLockEntry(lock, name), cwd, opts.Force)
			}
			if reason != "" {
				fmt.Fprintf(stderr, "%sSkipped %s from %s: %s%s\n", ansiYellow, name, label, reason, ansiReset)
				result.Skipped = append(result.Skipped, SyncJSONSkill{Name: name, Package: label, Reason: reason})
				continue
			}
			claimed[name] = true
			names = append(names, s.Name)
		}
		installed, failed, errMsg := syncInstallRemote(fetched, names, targets, frozenLock, r.Via, cwd)
		_ = fetched.Cleanup()
		if errMsg != "" {
			fmt.Fprintf(stderr, "Failed to install from %s: %s\n", label, errMsg)
			result.Failed = append(result.Failed, SyncJSONSkill{Name: label, Reason: errMsg})
			exit = 1
			continue
		}
		if len(installed) > 0 {
			fmt.Fprintf(stdout, "%s✓%s Installed %s from %s\n", ansiGreen, ansiReset, strings.Join(installed, ", "), label)
			for _, n := range installed {
				result.Synced = append(result.Synced, SyncJSONSkill{Name: n, Package: label})
			}
		}
		for _, f := range failed {
			fmt.Fprintln(stderr, f)
		}
		if len(failed) > 0 {
			exit = 1
		}
	}

	var failedResults []syncResult
	var okOrder []string
	byOK := map[string]syncResult{}
	for _, r := range results {
		if !r.ok {
			failedResults = append(failedResults, r)
			continue
		}
		if _, seen := byOK[r.skill]; !seen {
			okOrder = append(okOrder, r.skill)
			byOK[r.skill] = r
		}
	}
	if len(okOrder) > 0 {
		fmt.Fprintf(stdout, "\n%sSynced %d skill%s%s\n", ansiGreen, len(okOrder), plural(len(okOrder)), ansiReset)
		for _, n := range okOrder {
			r := byOK[n]
			fmt.Fprintf(stdout, "  %s✓%s %s %s<- %s%s\n", ansiGreen, ansiReset, n, ansiDim, r.pkg, ansiReset)
			if r.canonical != "" {
				fmt.Fprintf(stdout, "    %s%s%s\n", ansiDim, ShortenPath(r.canonical, cwd), ansiReset)
			}
			result.Synced = append(result.Synced, SyncJSONSkill{Name: n, Package: r.pkg})
		}
	}
	if len(failedResults) > 0 {
		exit = 1
		fmt.Fprintf(stderr, "\n%sFailed to install %d%s\n", ansiRed, len(failedResults), ansiReset)
		for _, r := range failedResults {
			fmt.Fprintf(stderr, "  %s✗%s %s -> %s: %s\n", ansiRed, ansiReset, r.skill, r.agent, r.err)
			result.Failed = append(result.Failed, SyncJSONSkill{Name: r.skill, Package: r.pkg, Agent: r.agent, Reason: r.err})
		}
	}
	fmt.Fprintf(stdout, "\n%sDone!%s %s Review skills before use; they run with full agent permissions.%s\n", ansiGreen, ansiReset, ansiDim, ansiReset)
	return finish(exit)
}

// syncInstallRemote installs remote "skills" entries and records them with via.
func syncInstallRemote(f *FetchedSource, names []string, agents []AgentType, frozen *LocalSkillLockFile, via, cwd string) ([]string, []string, string) {
	if len(names) == 0 {
		return nil, nil, ""
	}
	return restoreInstall(f, names, agents, frozen, InstallModeSymlink, via, cwd)
}

// syncStdin is where sync prompts read keys (overridable in tests).
var syncStdin io.Reader = os.Stdin

// promptSyncAgents is upstream promptForAgentChoice: universal agents are a
// locked section, choices are selectable, and the last saved choice (or
// fallback) starts selected. The selection is saved for next time.
func promptSyncAgents(t *promptTerm, choices, fallback []AgentType) ([]AgentType, error) {
	universal := GetUniversalAgents()
	visible := GetVisibleUniversalAgents()
	locked := &LockedSection{Title: "Universal (.agents/skills)", HiddenCount: len(universal) - len(visible)}
	for _, a := range visible {
		locked.Items = append(locked.Items, SearchItem{Value: string(a), Label: Agents[a].DisplayName})
	}
	items := make([]SearchItem, len(choices))
	for i, a := range choices {
		items[i] = SearchItem{Value: string(a), Label: Agents[a].DisplayName, Hint: Agents[a].SkillsDir}
	}
	var remembered, initial []int
	if last, err := GetLastSelectedAgents(); err == nil {
		for i, a := range choices {
			if contains(last, string(a)) {
				remembered = append(remembered, i)
			}
		}
	}
	initial = remembered
	if len(initial) == 0 {
		for i, a := range choices {
			if containsAgent(fallback, a) {
				initial = append(initial, i)
			}
		}
	}
	idx, err := t.SearchMultiSelect(SearchMultiSelectOptions{
		Message: "Which agents do you want to install to?", Items: items, Initial: initial, Locked: locked,
	})
	if err != nil {
		return nil, err
	}
	picked := append([]AgentType{}, visible...)
	for _, i := range idx {
		picked = append(picked, choices[i])
	}
	names := make([]string, len(picked))
	for i, a := range picked {
		names[i] = string(a)
	}
	_ = SaveSelectedAgents(names)
	return picked, nil
}

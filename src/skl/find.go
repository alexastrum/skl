package skl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SearchAPIBase is the default skills.sh search endpoint base. SKILLS_API_URL, when
// set, takes precedence (matching upstream find.ts).
var SearchAPIBase = "https://skills.sh"

// searchHTTPClient is used for skills.sh search requests.
var searchHTTPClient = &http.Client{Timeout: 10 * time.Second}

var (
	githubOwnerPattern = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,38})$`)
	ownerRepoPattern   = regexp.MustCompile(`^[^/@]+/[^/@]+$`)
)

// FindOptions configures FindSkills / `skl find`.
type FindOptions struct {
	Query string
	Cwd   string
	Local bool   // skl extension: -l/--local searches installed skills only
	Owner string // --owner <owner>
	JSON  bool   // skl extension: -j/--json
}

// FoundSkill is a local or remote search hit.
type FoundSkill struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Path        string `json:"path,omitempty"`
	Scope       string `json:"scope,omitempty"` // "project", "global", or "remote"
	Installs    int    `json:"installs,omitempty"`
}

// FindInstallFunc installs a skill picked in the interactive search (wired to the
// add command by the CLI). It returns a process exit code.
var FindInstallFunc func(source, skillName string) int

var findOptionSpec = map[string]CLIOptionSpec{
	"owner": {Type: CLIOptionString, Value: "a GitHub owner"},
	"local": {Type: CLIOptionBoolean, Short: "l"},
	"json":  {Type: CLIOptionBoolean, Short: "j"},
}

// ParseFindOptions splits `skl find` arguments into a query (all non-flag words
// joined by spaces) and options, collecting diagnostics for invalid --owner values (#2712).
func ParseFindOptions(args []string) (string, FindOptions, []error) {
	var opts FindOptions
	var errs []error
	cli := ParseCLIArgs(args, findOptionSpec)
	for _, e := range cli.Errors {
		errs = append(errs, e)
	}
	opts.Local = cli.Bools["local"]
	opts.JSON = cli.Bools["json"]
	for _, rawOwner := range cli.Strings["owner"] {
		owner := strings.ToLower(strings.TrimSpace(rawOwner))
		if !githubOwnerPattern.MatchString(owner) {
			errs = append(errs, ErrInvalidOptionValue("--owner", "a valid GitHub owner"))
			continue
		}
		opts.Owner = owner
	}
	var parts []string
	for _, p := range cli.Positionals {
		if p != "" {
			parts = append(parts, p)
		}
	}
	opts.Query = strings.Join(parts, " ")
	return opts.Query, opts, errs
}

func resolveSearchAPIBase() string {
	if env := os.Getenv("SKILLS_API_URL"); env != "" {
		return strings.TrimRight(env, "/")
	}
	return strings.TrimRight(SearchAPIBase, "/")
}

// SearchSkillsAPI queries the skills.sh search API. Results keep the API's
// relevance order (#2432) and no result limit is sent. Network or HTTP failures
// yield an empty result, like upstream.
func SearchSkillsAPI(query, owner string) ([]FoundSkill, error) {
	params := url.Values{"q": {query}}
	if owner != "" {
		params.Set("owner", owner)
	}
	resp, err := searchHTTPClient.Get(resolveSearchAPIBase() + "/api/search?" + params.Encode())
	if err != nil {
		return []FoundSkill{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return []FoundSkill{}, nil
	}
	var data struct {
		Skills []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Installs int    `json:"installs"`
			Source   string `json:"source"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data); err != nil {
		return []FoundSkill{}, nil
	}
	out := make([]FoundSkill, 0, len(data.Skills))
	for _, s := range data.Skills {
		out = append(out, FoundSkill{
			Name:     SanitizeMetadata(s.Name),
			Slug:     SanitizeMetadata(s.ID),
			Source:   SanitizeMetadata(s.Source),
			Scope:    "remote",
			Installs: s.Installs,
		})
	}
	return out, nil
}

const (
	minExactResults  = 10
	maxFallbackTerms = 6
	maxMergedResults = 25
)

// FindSearchResult holds remote search results and the count of exact matches (#2731).
type FindSearchResult struct {
	Skills     []FoundSkill
	ExactCount int
}

// SearchSkills searches the skills.sh API, falling back to N-1 term subqueries when a
// 2..6 term query returns fewer than 10 exact matches (#2731).
func SearchSkills(query, owner string) (FindSearchResult, error) {
	exact, err := findSearchFn(query, owner)
	if err != nil {
		return FindSearchResult{Skills: exact, ExactCount: len(exact)}, err
	}
	var terms []string
	seenTerm := map[string]bool{}
	for _, t := range strings.Fields(strings.TrimSpace(query)) {
		if !seenTerm[t] {
			seenTerm[t] = true
			terms = append(terms, t)
		}
	}
	if len(exact) >= minExactResults || len(terms) < 2 || len(terms) > maxFallbackTerms {
		return FindSearchResult{Skills: exact, ExactCount: len(exact)}, nil
	}

	subqueries := make([]string, len(terms))
	for i := range terms {
		var sub []string
		sub = append(sub, terms[:i]...)
		sub = append(sub, terms[i+1:]...)
		subqueries[i] = strings.Join(sub, " ")
	}
	subResults := make([][]FoundSkill, len(subqueries))
	var wg sync.WaitGroup
	for i, sq := range subqueries {
		wg.Add(1)
		go func(idx int, q string) {
			defer wg.Done()
			res, _ := findSearchFn(q, owner)
			subResults[idx] = res
		}(i, sq)
	}
	wg.Wait()

	skillKey := func(s FoundSkill) string {
		if s.Slug != "" {
			return s.Slug
		}
		return s.Name
	}
	seen := make(map[string]bool, len(exact))
	for _, s := range exact {
		seen[skillKey(s)] = true
	}
	type partialCandidate struct {
		skill    FoundSkill
		hits     int
		bestRank int
	}
	var order []string
	bySlug := map[string]*partialCandidate{}
	for _, list := range subResults {
		for rank, s := range list {
			k := skillKey(s)
			if seen[k] {
				continue
			}
			if c, ok := bySlug[k]; ok {
				c.hits++
				if rank < c.bestRank {
					c.bestRank = rank
				}
			} else {
				bySlug[k] = &partialCandidate{skill: s, hits: 1, bestRank: rank}
				order = append(order, k)
			}
		}
	}
	candidates := make([]*partialCandidate, len(order))
	for i, k := range order {
		candidates[i] = bySlug[k]
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.hits != b.hits {
			return a.hits > b.hits
		}
		if a.bestRank != b.bestRank {
			return a.bestRank < b.bestRank
		}
		return a.skill.Installs > b.skill.Installs
	})
	limit := max(0, maxMergedResults-len(exact))
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	merged := append([]FoundSkill{}, exact...)
	for _, c := range candidates {
		merged = append(merged, c.skill)
	}
	return FindSearchResult{Skills: merged, ExactCount: len(exact)}, nil
}

// FindSkills searches installed project and global skills (opts.Local) or the
// skills.sh API (otherwise).
func FindSkills(opts FindOptions) ([]FoundSkill, error) {
	if !opts.Local {
		if opts.Query == "" {
			return []FoundSkill{}, nil
		}
		res, err := SearchSkills(opts.Query, opts.Owner)
		return res.Skills, err
	}
	q := strings.ToLower(opts.Query)
	found := []FoundSkill{}
	for _, global := range []bool{false, true} {
		skills, err := ListInstalledSkills(ListOptions{Global: global, Cwd: opts.Cwd})
		if err != nil {
			continue
		}
		for _, s := range skills {
			if strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Description), q) {
				found = append(found, FoundSkill{
					Name:        s.Name,
					Description: s.Description,
					Source:      s.CanonicalPath,
					Path:        s.Path,
					Scope:       s.Scope,
				})
			}
		}
	}
	return found, nil
}

// formatInstalls renders an install count like upstream ("1.2K installs"),
// rounding half up like JavaScript's Number.prototype.toFixed(1).
func formatInstalls(n int) string {
	tenths := func(t int) string {
		s := strconv.Itoa(t/10) + "." + strconv.Itoa(t%10)
		return strings.TrimSuffix(s, ".0")
	}
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000:
		return tenths((n+50_000)/100_000) + "M installs"
	case n >= 1_000:
		return tenths((n+50)/100) + "K installs"
	case n == 1:
		return "1 install"
	default:
		return fmt.Sprintf("%d installs", n)
	}
}

const findUsage = "Usage: skl find <query> [--owner <owner>]"

// RunFind implements `skl find`, returning the process exit code.
func RunFind(args []string, cwd string) int {
	return runFind(args, cwd, os.Stdout, os.Stderr, os.Stdin)
}

func runFind(args []string, cwd string, stdout, stderr io.Writer, stdin io.Reader) int {
	RefreshColors(args...)
	query, opts, errs := ParseFindOptions(args)
	opts.Cwd = cwd
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(stderr, FormatError(e))
		}
		fmt.Fprintln(stderr, findUsage)
		return 1
	}

	printJSON := func(v []FoundSkill) int {
		if v == nil {
			v = []FoundSkill{}
		}
		data, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	}

	if opts.Local {
		res, _ := FindSkills(opts)
		if opts.JSON {
			return printJSON(res)
		}
		if len(res) == 0 {
			fmt.Fprintf(stdout, "%sNo installed skills found for %q%s\n", ansiDim, query, ansiReset)
			return 0
		}
		for _, s := range res {
			fmt.Fprintf(stdout, "%s%s%s %s(%s)%s\n", ansiText, SanitizeMetadata(s.Name), ansiReset, ansiDim, s.Scope, ansiReset)
			if s.Description != "" {
				fmt.Fprintf(stdout, "  %s\n", SanitizeMetadata(s.Description))
			}
			fmt.Fprintf(stdout, "%s└ %s%s\n\n", ansiDim, ShortenPath(s.Path, cwd), ansiReset)
		}
		return 0
	}

	if query != "" {
		res, _ := SearchSkills(query, opts.Owner)
		results := res.Skills
		if opts.JSON {
			return printJSON(results)
		}
		if len(results) == 0 {
			suffix := ""
			if opts.Owner != "" {
				suffix = fmt.Sprintf(" from owner %q", opts.Owner)
			}
			fmt.Fprintf(stdout, "%sNo skills found for %q%s%s\n", ansiDim, query, suffix, ansiReset)
			return 0
		}
		fmt.Fprintf(stdout, "%sInstall with%s skl add <owner/repo@skill>\n\n", ansiDim, ansiReset)
		for i, s := range results {
			if i == res.ExactCount {
				if res.ExactCount == 0 {
					fmt.Fprintf(stdout, "%sNo skills matched every term in %q. Partial matches:%s\n\n", ansiDim, query, ansiReset)
				} else {
					fmt.Fprintf(stdout, "%sPartial matches (some terms of %q):%s\n\n", ansiDim, query, ansiReset)
				}
			}
			printFoundSkill(stdout, s)
		}
		return 0
	}

	if opts.JSON {
		return printJSON(nil)
	}
	if !stdinIsTTY() || IsRunningInAgent() {
		fmt.Fprintf(stdout, "%sTip: if running in a coding agent, follow these steps:%s\n", ansiDim, ansiReset)
		fmt.Fprintf(stdout, "%s  1) skl find [query] [--owner <owner>]%s\n", ansiDim, ansiReset)
		fmt.Fprintf(stdout, "%s  2) skl add <owner/repo@skill>%s\n\n", ansiDim, ansiReset)
		fmt.Fprintf(stdout, "%s%s%s\n", ansiDim, findUsage, ansiReset)
		return 0
	}

	selected := runSearchPrompt(newPromptTerm(stdin, stdout, true), opts.Owner)
	if selected == nil {
		fmt.Fprintf(stdout, "%sSearch cancelled%s\n\n", ansiDim, ansiReset)
		return 0
	}
	pkg := selected.Source
	if pkg == "" {
		pkg = selected.Slug
	}
	fmt.Fprintf(stdout, "\n%sInstalling %s%s%s from %s%s%s...\n\n", ansiText, ansiBold, selected.Name, ansiReset, ansiDim, pkg, ansiReset)
	code := 0
	if FindInstallFunc != nil {
		code = FindInstallFunc(pkg, selected.Name)
	} else {
		fmt.Fprintf(stdout, "Run: skl add %s --skill %s\n", pkg, selected.Name)
	}
	fmt.Fprintln(stdout)
	if ownerRepoPattern.MatchString(pkg) && selected.Slug != "" {
		fmt.Fprintf(stdout, "%sView the skill at%s %shttps://skills.sh/%s%s\n\n", ansiDim, ansiReset, ansiText, selected.Slug, ansiReset)
	} else {
		fmt.Fprintf(stdout, "%sDiscover more skills at%s %shttps://skills.sh%s\n\n", ansiDim, ansiReset, ansiText, ansiReset)
	}
	return code
}

func printFoundSkill(w io.Writer, s FoundSkill) {
	pkg := s.Source
	if pkg == "" {
		pkg = s.Slug
	}
	badge := ""
	if inst := formatInstalls(s.Installs); inst != "" {
		badge = " " + ansiCyan + inst + ansiReset
	}
	fmt.Fprintf(w, "%s%s@%s%s%s\n", ansiText, pkg, s.Name, ansiReset, badge)
	fmt.Fprintf(w, "%s└ https://skills.sh/%s%s\n\n", ansiDim, s.Slug, ansiReset)
}

// findSearchFn and findDebounce are overridable in tests.
var (
	findSearchFn = SearchSkillsAPI
	// findDebounce is upstream's adaptive debounce: 250ms for 2 characters,
	// 50ms less per extra character, never under 150ms.
	findDebounce = func(q string) time.Duration {
		return time.Duration(max(150, 350-len([]rune(q))*50)) * time.Millisecond
	}
)

// runSearchPrompt is upstream's fzf-style search prompt: typing searches the
// skills.sh API (debounced, at least 2 characters) while up/down move through
// the first 8 results, Enter picks one, and Esc or Ctrl+C cancels (nil).
func runSearchPrompt(t *promptTerm, owner string) *FoundSkill {
	end, err := t.begin()
	if err != nil {
		return nil
	}
	defer end()
	// Read the overridable hooks once so timer goroutines never touch the
	// package globals, which tests restore when they finish.
	debounceFn := findDebounce

	var (
		mu       sync.Mutex
		results  []FoundSkill
		selected int
		query    []rune
		loading  bool
		gen      int
		timer    *time.Timer
		closed   bool
	)
	frame := &promptFrame{t: t}
	// render must be called with mu held.
	render := func() {
		lines := []string{ansiText + "Search skills:" + ansiReset + " " + string(query) + ansiBold + "_" + ansiReset, ""}
		const maxVisible = 8
		switch {
		case len(query) < 2:
			lines = append(lines, ansiDim+"Start typing to search (min 2 chars)"+ansiReset)
		case len(results) == 0 && loading:
			lines = append(lines, ansiDim+"Searching"+t.sym().ellipsis+ansiReset)
		case len(results) == 0:
			lines = append(lines, ansiDim+"No skills found"+ansiReset)
		default:
			for i, s := range results {
				if i >= maxVisible {
					break
				}
				arrow, name := " ", ansiText+s.Name+ansiReset
				if i == selected {
					arrow, name = ansiBold+">"+ansiReset, ansiBold+s.Name+ansiReset
				}
				source := ""
				if s.Source != "" {
					source = " " + ansiDim + s.Source + ansiReset
				}
				badge := ""
				if inst := formatInstalls(s.Installs); inst != "" {
					badge = " " + ansiCyan + inst + ansiReset
				}
				spin := ""
				if loading && i == 0 {
					spin = " " + ansiDim + t.sym().ellipsis + ansiReset
				}
				lines = append(lines, "  "+arrow+" "+name+source+badge+spin)
			}
		}
		lines = append(lines, "", ansiDim+"up/down navigate | enter select | esc cancel"+ansiReset)
		frame.draw(lines)
	}
	// trigger must be called with mu held.
	trigger := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		gen++
		loading = false
		if len(query) < 2 {
			results, selected = nil, 0
			render()
			return
		}
		loading = true
		render()
		q, mine := string(query), gen
		timer = time.AfterFunc(debounceFn(q), func() {
			mu.Lock()
			stale := closed || mine != gen
			mu.Unlock()
			if stale {
				return
			}
			sr, err := SearchSkills(q, owner)
			res := sr.Skills
			mu.Lock()
			defer mu.Unlock()
			if closed || mine != gen {
				return
			}
			if err != nil {
				res = nil
			}
			results, selected, loading, timer = res, 0, false, nil
			render()
		})
	}

	// On Enter or cancel, mark the prompt closed and stop a pending debounce
	// so no search runs after the prompt returns.
	defer func() {
		mu.Lock()
		closed = true
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		mu.Unlock()
	}()
	mu.Lock()
	render()
	mu.Unlock()
	for {
		k := t.keys().next()
		mu.Lock()
		switch {
		case k.isCancel():
			closed = true
			mu.Unlock()
			return nil
		case k.kind == keyEnter:
			closed = true
			var pick *FoundSkill
			if selected < len(results) {
				s := results[selected]
				pick = &s
			}
			mu.Unlock()
			return pick
		case k.kind == keyUp:
			selected = max(0, selected-1)
			render()
		case k.kind == keyDown:
			selected = min(max(0, len(results)-1), selected+1)
			render()
		case k.kind == keyBackspace:
			if len(query) > 0 {
				query = query[:len(query)-1]
				trigger()
			}
		case k.kind == keyRune && k.r >= ' ' && k.r <= '~', k.kind == keySpace:
			r := k.r
			if k.kind == keySpace {
				r = ' '
			}
			query = append(query, r)
			trigger()
		}
		mu.Unlock()
	}
}

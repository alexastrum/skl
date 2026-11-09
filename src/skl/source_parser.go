package skl

// Source parsing, GitHub host resolution, and hardened git operations.
// Ports upstream src/source-parser.ts, src/github-host.ts, src/git.ts and
// the URL helpers of src/notion-test.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Source types used in ParsedSource.Type and lock entries.
const (
	SourceTypeGitHub    = "github"
	SourceTypeGitLab    = "gitlab"
	SourceTypeGit       = "git"
	SourceTypeLocal     = "local"
	SourceTypeWellKnown = "well-known"
	SourceTypeDownload  = "download"
)

// ParseSourceOptions tunes ParseSource.
type ParseSourceOptions struct {
	// GitHubHost pins the shorthand host regardless of GH_HOST (e.g. from a lock entry).
	GitHubHost string
	// Cwd resolves relative local paths (defaults to os.Getwd()).
	Cwd string
}

// sourceAliases maps common shorthand to canonical sources.
var sourceAliases = map[string]string{
	"coinbase/agentWallet":      "coinbase/agentic-wallet-skills",
	"vercel-labs/vercel-skills": "vercel-labs/agent-skills",
}

var (
	spWindowsDriveRe   = regexp.MustCompile(`^[a-zA-Z]:[/\\]`)
	spSSHGitRe         = regexp.MustCompile(`(?i)^ssh://.+\.git(?:$|[/?])`)
	spHTTPGitRe        = regexp.MustCompile(`(?i)^https?://.+\.git(?:$|[/?])`)
	spGitHubRepoPathRe = regexp.MustCompile(`^/[^/]+/[^/]+(?:\.git)?(?:/tree/[^/]+(?:/.*)?)?/?$`)
	spGitLabRepoPathRe = regexp.MustCompile(`^/.+?/[^/]+(?:\.git)?(?:/-/tree/[^/]+(?:/.*)?)?/?$`)
	spShorthandLikeRe  = regexp.MustCompile(`^([^/]+)/([^/]+)(?:/(.+)|@(.+))?$`)
	spVersionPinRe     = regexp.MustCompile(`^v?\d+(?:\.\d+)+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	spCommitSHARe      = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	spGitHubArtifactRe = regexp.MustCompile(`^/[^/]+/[^/]+/(?:archive/|raw/|releases/(?:download/|latest/download/))`)
	spGitLabArtifactRe = regexp.MustCompile(`/-/(?:archive|raw)/`)
	spSSHTreeRe        = regexp.MustCompile(`^git@([^:]+):([^/]+)/([^/]+?)(?:\.git)?/tree/([^/]+)(?:/(.+))?$`)
	spGHTreePathRe     = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/tree/([^/]+)/(.+)`)
	spGHTreeRe         = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/tree/([^/]+)$`)
	spGHRepoRe         = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)`)
	spGLTreePathRe     = regexp.MustCompile(`^(https?)://([^/]+)/(.+?)/-/tree/([^/]+)/(.+)`)
	spGLTreeRe         = regexp.MustCompile(`^(https?)://([^/]+)/(.+?)/-/tree/([^/]+)$`)
	spGLRepoRe         = regexp.MustCompile(`gitlab\.com/(.+?)(?:\.git)?/?$`)
	spAtSkillRe        = regexp.MustCompile(`^([^/]+)/([^/@]+)@(.+)$`)
	spShorthandRe      = regexp.MustCompile(`^([^/]+)/([^/]+)(?:/(.+?))?/?$`)
	spAzureVersionRe   = regexp.MustCompile(`(?i)^(?:GB|GT)(.+)$`)
	spGitSuffixRe      = regexp.MustCompile(`\.git$`)
)

// GetGitHubHost returns the GitHub host for shorthand sources: GH_HOST when it
// is a bare hostname, otherwise "github.com".
func GetGitHubHost() string {
	configured := strings.TrimSpace(os.Getenv("GH_HOST"))
	if configured == "" {
		return "github.com"
	}
	u, err := url.Parse("https://" + configured)
	if err != nil || u.Host == "" || u.User != nil || u.Port() != "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "github.com"
	}
	return strings.ToLower(u.Hostname())
}

// IsGitHubHost reports whether host is github.com or the configured GH_HOST.
func IsGitHubHost(host string) bool {
	h := strings.ToLower(host)
	return h == "github.com" || h == GetGitHubHost()
}

// IsCommitSHA reports whether ref is a full 40-character commit SHA.
func IsCommitSHA(ref string) bool { return spCommitSHARe.MatchString(ref) }

// IsVersionPin reports whether value after owner/repo@ is a checkout pin
// (v1.2.3, 1.2, 2026.10.1, prerelease/build metadata, or a full SHA) rather
// than a skill name. A single number such as v2 stays a skill name (#2527).
func IsVersionPin(value string) bool {
	return spVersionPinRe.MatchString(value) || IsCommitSHA(value)
}

// SanitizeSubpath rejects subpaths containing ".." segments.
func SanitizeSubpath(subpath string) (string, error) {
	normalized := strings.ReplaceAll(subpath, `\`, "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", ErrUnsafeSubpath(subpath)
		}
	}
	return subpath, nil
}

func isLocalSourcePath(input string) bool {
	return filepath.IsAbs(input) ||
		strings.HasPrefix(input, "/") ||
		strings.HasPrefix(input, "./") ||
		strings.HasPrefix(input, "../") ||
		strings.HasPrefix(input, `.\`) ||
		strings.HasPrefix(input, `..\`) ||
		input == "." || input == ".." ||
		input == "~" || strings.HasPrefix(input, "~/") || strings.HasPrefix(input, `~\`) ||
		spWindowsDriveRe.MatchString(input)
}

func spIsHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func decodeFragmentValue(v string) string {
	if d, err := url.PathUnescape(v); err == nil {
		return d
	}
	return v
}

// fpEncodeURIComponent mirrors JS fpEncodeURIComponent closely enough for refs
// and skill names (escapes '@', '/', '#', '%', spaces, etc.).
func fpEncodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isSkillsShBare(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "skills.sh/") || strings.HasPrefix(l, "www.skills.sh/")
}

func looksLikeGitSource(input string) bool {
	if strings.HasPrefix(input, "github:") || strings.HasPrefix(input, "gitlab:") || strings.HasPrefix(input, "git@") {
		return true
	}
	if spSSHGitRe.MatchString(input) {
		return true
	}
	if spIsHTTPURL(input) {
		if u, err := url.Parse(input); err == nil {
			p := u.EscapedPath()
			if IsGitHubHost(u.Host) {
				return spGitHubRepoPathRe.MatchString(p)
			}
			if strings.EqualFold(u.Hostname(), "gitlab.com") {
				return spGitLabRepoPathRe.MatchString(p)
			}
			segs := spNonEmptySegments(p)
			if i := spIndexOf(segs, "_git"); i >= 0 && i < len(segs)-1 {
				return true
			}
		}
	}
	if spHTTPGitRe.MatchString(input) {
		return true
	}
	if isSkillsShBare(input) {
		return false
	}
	return !strings.Contains(input, ":") && !strings.HasPrefix(input, ".") &&
		!strings.HasPrefix(input, "/") && spShorthandLikeRe.MatchString(input)
}

func spNonEmptySegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func spIndexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

func parseFragmentRef(input string) (base, ref, skillFilter string) {
	idx := strings.IndexByte(input, '#')
	if idx < 0 {
		return input, "", ""
	}
	b := input[:idx]
	fragment := input[idx+1:]
	if fragment == "" || !looksLikeGitSource(b) {
		return input, "", ""
	}
	at := strings.IndexByte(fragment, '@')
	if at < 0 {
		return b, decodeFragmentValue(fragment), ""
	}
	if r := fragment[:at]; r != "" {
		ref = decodeFragmentValue(r)
	}
	if s := fragment[at+1:]; s != "" {
		skillFilter = decodeFragmentValue(s)
	}
	return b, ref, skillFilter
}

func appendFragmentRef(input, ref, skillFilter string) string {
	if ref == "" && skillFilter == "" {
		return input
	}
	out := input + "#"
	if ref != "" {
		out += fpEncodeURIComponent(ref)
	}
	if skillFilter != "" {
		out += "@" + fpEncodeURIComponent(skillFilter)
	}
	return out
}

func isHostedArtifactURL(input string) bool {
	u, err := url.Parse(input)
	if err != nil || !spIsHTTPURL(input) {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "raw.githubusercontent.com", "codeload.github.com", "objects.githubusercontent.com":
		return true
	case "github.com":
		return spGitHubArtifactRe.MatchString(u.EscapedPath())
	case "gitlab.com":
		return spGitLabArtifactRe.MatchString(u.EscapedPath())
	}
	return false
}

func isWellKnownURL(input string) bool {
	if !spIsHTTPURL(input) {
		return false
	}
	u, err := url.Parse(input)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "gitlab.com", "raw.githubusercontent.com":
		return false
	}
	return !strings.HasSuffix(input, ".git")
}

func azureVersionToRef(version string) string {
	if m := spAzureVersionRe.FindStringSubmatch(version); m != nil {
		return m[1]
	}
	return ""
}

func parseAzureReposSource(input, fragmentRef, fragmentSkillFilter string) (*ParsedSource, error) {
	if !spIsHTTPURL(input) {
		return nil, nil
	}
	u, err := url.Parse(input)
	if err != nil {
		return nil, nil
	}
	segs := spNonEmptySegments(u.EscapedPath())
	gi := spIndexOf(segs, "_git")
	if gi < 0 || gi == len(segs)-1 {
		return nil, nil
	}
	repo := spGitSuffixRe.ReplaceAllString(segs[gi+1], "")
	if repo == "" {
		return nil, nil
	}
	parts := append(append([]string{}, segs[:gi]...), "_git", repo)
	for i, s := range parts {
		if d, err := url.PathUnescape(s); err == nil {
			parts[i] = fpEncodeURIComponent(d)
		} else {
			parts[i] = fpEncodeURIComponent(s)
		}
	}
	cloneURL := u.Scheme + "://" + u.Host + "/" + strings.Join(parts, "/")
	q := u.Query()
	ref := azureVersionToRef(q.Get("version"))
	if ref == "" {
		ref = fragmentRef
	}
	out := &ParsedSource{Type: SourceTypeGit, URL: cloneURL, Ref: ref, SkillFilter: fragmentSkillFilter}
	if p := q.Get("path"); p != "" {
		sub, err := SanitizeSubpath(strings.TrimLeft(p, "/"))
		if err != nil {
			return nil, err
		}
		out.Subpath = sub
	}
	return out, nil
}

// ParseSource parses a user-supplied source string (upstream parseSource).
// Supported: local paths, owner/repo[/subpath][@skill|@v1.2.3][#ref[@skill]],
// github:/gitlab: prefixes, GitHub/GHE/GitLab/Azure URLs, GitHub SSH /tree/
// URLs, hosted artifact URLs, skills.sh short form, well-known URLs and
// generic git URLs.
func ParseSource(input string, opts ...ParseSourceOptions) (ParsedSource, error) {
	var o ParseSourceOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	if isLocalSourcePath(input) {
		cwd := o.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		var resolved string
		if input == "~" || strings.HasPrefix(input, "~/") || strings.HasPrefix(input, `~\`) {
			resolved = ResolvePath(input, cwd)
		} else if filepath.IsAbs(input) || spWindowsDriveRe.MatchString(input) {
			resolved = filepath.Clean(input)
		} else {
			resolved = filepath.Join(cwd, filepath.FromSlash(input))
		}
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		return ParsedSource{Type: SourceTypeLocal, URL: resolved, LocalPath: resolved}, nil
	}

	input, fragmentRef, fragmentSkillFilter := parseFragmentRef(input)

	if alias, ok := sourceAliases[input]; ok {
		input = alias
	}

	if strings.HasPrefix(input, "github:") && len(input) > len("github:") {
		return ParseSource(appendFragmentRef(input[len("github:"):], fragmentRef, fragmentSkillFilter),
			ParseSourceOptions{Cwd: o.Cwd})
	}
	if strings.HasPrefix(input, "gitlab:") && len(input) > len("gitlab:") {
		return ParseSource(appendFragmentRef("https://gitlab.com/"+input[len("gitlab:"):], fragmentRef, fragmentSkillFilter),
			ParseSourceOptions{Cwd: o.Cwd})
	}
	if isSkillsShBare(input) {
		input = "https://" + input
	}

	if isHostedArtifactURL(input) {
		return ParsedSource{Type: SourceTypeDownload, URL: input}, nil
	}

	// Explicit GitHub Enterprise URL: generic git handling.
	if GetGitHubHost() != "github.com" && spIsHTTPURL(input) {
		if u, err := url.Parse(input); err == nil && IsGitHubHost(u.Host) && !strings.EqualFold(u.Host, "github.com") {
			segs := spNonEmptySegments(u.Path)
			if len(segs) >= 2 {
				owner := segs[0]
				repo := spGitSuffixRe.ReplaceAllString(segs[1], "")
				out := ParsedSource{Type: SourceTypeGit, URL: fmt.Sprintf("%s://%s/%s/%s.git", u.Scheme, u.Host, owner, repo)}
				if len(segs) >= 4 && segs[2] == "tree" && segs[3] != "" {
					out.Ref = segs[3]
					if len(segs) > 4 {
						sub, err := SanitizeSubpath(strings.Join(segs[4:], "/"))
						if err != nil {
							return ParsedSource{}, err
						}
						out.Subpath = sub
					}
				} else if fragmentRef != "" {
					out.Ref = fragmentRef
				}
				return out, nil
			}
		}
	}

	// GitHub SSH URL with branch (#2636).
	if m := spSSHTreeRe.FindStringSubmatch(input); m != nil && IsGitHubHost(m[1]) {
		out := ParsedSource{Type: SourceTypeGit, URL: fmt.Sprintf("git@%s:%s/%s.git", m[1], m[2], m[3]), Ref: m[4]}
		if m[5] != "" {
			sub, err := SanitizeSubpath(m[5])
			if err != nil {
				return ParsedSource{}, err
			}
			out.Subpath = sub
		}
		return out, nil
	}

	if m := spGHTreePathRe.FindStringSubmatch(input); m != nil {
		sub, err := SanitizeSubpath(m[4])
		if err != nil {
			return ParsedSource{}, err
		}
		return ParsedSource{Type: SourceTypeGitHub, URL: fmt.Sprintf("https://github.com/%s/%s.git", m[1], m[2]),
			Ref: spFirstNonEmpty(m[3], fragmentRef), Subpath: sub}, nil
	}
	if m := spGHTreeRe.FindStringSubmatch(input); m != nil {
		return ParsedSource{Type: SourceTypeGitHub, URL: fmt.Sprintf("https://github.com/%s/%s.git", m[1], m[2]),
			Ref: spFirstNonEmpty(m[3], fragmentRef)}, nil
	}
	if m := spGHRepoRe.FindStringSubmatch(input); m != nil {
		repo := spGitSuffixRe.ReplaceAllString(m[2], "")
		return ParsedSource{Type: SourceTypeGitHub, URL: fmt.Sprintf("https://github.com/%s/%s.git", m[1], repo),
			Ref: fragmentRef}, nil
	}

	if m := spGLTreePathRe.FindStringSubmatch(input); m != nil && m[2] != "github.com" && m[3] != "" {
		sub, err := SanitizeSubpath(m[5])
		if err != nil {
			return ParsedSource{}, err
		}
		return ParsedSource{Type: SourceTypeGitLab,
			URL: fmt.Sprintf("%s://%s/%s.git", m[1], m[2], spGitSuffixRe.ReplaceAllString(m[3], "")),
			Ref: spFirstNonEmpty(m[4], fragmentRef), Subpath: sub}, nil
	}
	if m := spGLTreeRe.FindStringSubmatch(input); m != nil && m[2] != "github.com" && m[3] != "" {
		return ParsedSource{Type: SourceTypeGitLab,
			URL: fmt.Sprintf("%s://%s/%s.git", m[1], m[2], spGitSuffixRe.ReplaceAllString(m[3], "")),
			Ref: spFirstNonEmpty(m[4], fragmentRef)}, nil
	}
	if m := spGLRepoRe.FindStringSubmatch(input); m != nil && strings.Contains(m[1], "/") {
		return ParsedSource{Type: SourceTypeGitLab, URL: "https://gitlab.com/" + m[1] + ".git", Ref: fragmentRef}, nil
	}

	az, err := parseAzureReposSource(input, fragmentRef, fragmentSkillFilter)
	if err != nil {
		return ParsedSource{}, err
	}
	if az != nil {
		return *az, nil
	}

	githubHost := o.GitHubHost
	if githubHost == "" {
		githubHost = GetGitHubHost()
	}
	shorthandType := SourceTypeGitHub
	if githubHost != "github.com" {
		shorthandType = SourceTypeGit
	}
	shorthandOK := !strings.Contains(input, ":") && !strings.HasPrefix(input, ".") && !strings.HasPrefix(input, "/")

	if m := spAtSkillRe.FindStringSubmatch(input); m != nil && shorthandOK {
		u := fmt.Sprintf("https://%s/%s/%s.git", githubHost, m[1], m[2])
		if fragmentRef == "" && fragmentSkillFilter == "" && IsVersionPin(m[3]) {
			return ParsedSource{Type: shorthandType, URL: u, Ref: m[3]}, nil
		}
		return ParsedSource{Type: shorthandType, URL: u, Ref: fragmentRef,
			SkillFilter: spFirstNonEmpty(fragmentSkillFilter, m[3])}, nil
	}
	if m := spShorthandRe.FindStringSubmatch(input); m != nil && shorthandOK {
		out := ParsedSource{Type: shorthandType, URL: fmt.Sprintf("https://%s/%s/%s.git", githubHost, m[1], m[2]),
			Ref: fragmentRef, SkillFilter: fragmentSkillFilter}
		if m[3] != "" {
			sub, err := SanitizeSubpath(m[3])
			if err != nil {
				return ParsedSource{}, err
			}
			out.Subpath = sub
		}
		return out, nil
	}

	if isWellKnownURL(input) {
		return ParsedSource{Type: SourceTypeWellKnown, URL: input}, nil
	}

	return ParsedSource{Type: SourceTypeGit, URL: input, Ref: fragmentRef}, nil
}

func spFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// GetOwnerRepo extracts owner/repo (or group/subgroup/repo) from a parsed
// source for lock tracking. Returns "" for local/download or unparseable.
func GetOwnerRepo(parsed ParsedSource) string {
	if parsed.Type == SourceTypeLocal || parsed.Type == SourceTypeDownload {
		return ""
	}
	u := parsed.URL
	if strings.HasPrefix(u, "git@") {
		if i := strings.IndexByte(u, ':'); i >= 0 {
			p := spGitSuffixRe.ReplaceAllString(u[i+1:], "")
			if strings.Contains(p, "/") {
				return p
			}
		}
		return ""
	}
	if strings.HasPrefix(u, "ssh://") || spIsHTTPURL(u) {
		pu, err := url.Parse(u)
		if err != nil {
			return ""
		}
		p := strings.TrimPrefix(pu.Path, "/")
		p = spGitSuffixRe.ReplaceAllString(p, "")
		if strings.Contains(p, "/") {
			return p
		}
	}
	return ""
}

// ParseOwnerRepo splits "owner/repo"; ok is false for any other shape.
func ParseOwnerRepo(ownerRepo string) (owner, repo string, ok bool) {
	parts := strings.Split(ownerRepo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ---------------------------------------------------------------------------
// Hardened git operations (upstream src/git.ts).

const (
	defaultCloneTimeout  = 5 * time.Minute
	allowedGitProtocols  = "https:http:ssh:git:file"
	submoduleGitProtocol = "https:http:ssh:git"
)

var (
	extTransportRe   = regexp.MustCompile(`(?i)^ext::`)
	missingRefRes    = []*regexp.Regexp{regexp.MustCompile(`(?i)Remote branch .* not found in upstream origin`), regexp.MustCompile(`(?i)couldn't find remote ref`), regexp.MustCompile(`(?i)upload-pack: not our ref`)}
	ghSSHProtocolRe  = regexp.MustCompile(`(?i)Git operations protocol:\s+ssh`)
	ghRepoSSHRe      = regexp.MustCompile(`(?i)^git@([^:]+):([^/]+)/([^/]+?)(?:\.git)?$`)
	ghRepoHTTPPathRe = regexp.MustCompile(`^/([^/]+)/([^/]+?)(?:\.git)?/?$`)
)

// fpGitBinary is the git executable (overridable in tests).
var fpGitBinary = "git"

// fpGhBinary is the GitHub CLI executable (overridable in tests).
var fpGhBinary = "gh"

// GitConfigArgs are passed as -c flags on every git invocation: disable the
// LFS filter (checkout succeeds without git-lfs) and enable long paths on
// Windows (#2637).
var GitConfigArgs = []string{
	"-c", "filter.lfs.required=false",
	"-c", "filter.lfs.smudge=",
	"-c", "filter.lfs.clean=",
	"-c", "filter.lfs.process=",
	"-c", "core.longpaths=true",
}

// CloneTimeout returns SKILLS_CLONE_TIMEOUT_MS (default 5 minutes).
func CloneTimeout() time.Duration {
	if raw := os.Getenv("SKILLS_CLONE_TIMEOUT_MS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return defaultCloneTimeout
}

// GitEnv returns the process environment with the hardened git overrides
// applied (replacing inherited values): GIT_TERMINAL_PROMPT=0,
// GIT_ALLOW_PROTOCOL, GIT_LFS_SKIP_SMUDGE=1 and LC_ALL=C (#2506). extra
// entries ("KEY=VALUE") are applied last.
func GitEnv(extra ...string) []string {
	return fpOverrideEnv(os.Environ(), append([]string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=" + allowedGitProtocols,
		"GIT_LFS_SKIP_SMUDGE=1",
		"LC_ALL=C",
	}, extra...)...)
}

func fpOverrideEnv(base []string, overrides ...string) []string {
	keys := map[string]bool{}
	for _, kv := range overrides {
		if i := strings.IndexByte(kv, '='); i > 0 {
			keys[fpEnvKey(kv[:i])] = true
		}
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i > 0 && keys[fpEnvKey(kv[:i])] {
			continue
		}
		out = append(out, kv)
	}
	// Later overrides win over earlier ones.
	seen := map[string]int{}
	for _, kv := range overrides {
		k := fpEnvKey(kv[:strings.IndexByte(kv, '=')])
		if idx, ok := seen[k]; ok {
			out[idx] = kv
			continue
		}
		seen[k] = len(out)
		out = append(out, kv)
	}
	return out
}

func fpEnvKey(k string) string {
	if fpIsWindows() {
		return strings.ToUpper(k)
	}
	return k
}

func fpIsWindows() bool { return filepath.Separator == '\\' }

// IsMissingRefError reports whether git output means the ref does not exist
// as a branch or tag.
func IsMissingRefError(msg string) bool {
	for _, re := range missingRefRes {
		if re.MatchString(msg) {
			return true
		}
	}
	return false
}

// IsGitHubSSOAuthError reports SAML SSO enforcement failures.
func IsGitHubSSOAuthError(msg string) bool {
	l := strings.ToLower(msg)
	return strings.Contains(l, "saml sso") || strings.Contains(l, "enforced sso") ||
		strings.Contains(l, "enabled or enforced saml") || strings.Contains(l, "re-authorize the oauth application")
}

func fpIsGitAuthFailure(msg string) bool {
	return strings.Contains(msg, "Authentication failed") ||
		strings.Contains(msg, "could not read Username") ||
		strings.Contains(msg, "Permission denied") ||
		strings.Contains(msg, "Repository not found") ||
		strings.Contains(msg, "requested URL returned error: 403") ||
		IsGitHubSSOAuthError(msg)
}

type fpGitHubRepoInfo struct {
	Owner, Repo, Slug, SSHURL, Host string
}

func fpParseGitHubRepoURL(u string) *fpGitHubRepoInfo {
	if m := ghRepoSSHRe.FindStringSubmatch(u); m != nil && IsGitHubHost(m[1]) {
		return &fpGitHubRepoInfo{Owner: m[2], Repo: m[3], Slug: m[2] + "/" + m[3],
			SSHURL: fmt.Sprintf("git@%s:%s/%s.git", m[1], m[2], m[3]), Host: m[1]}
	}
	pu, err := url.Parse(u)
	if err != nil || !IsGitHubHost(pu.Host) {
		return nil
	}
	m := ghRepoHTTPPathRe.FindStringSubmatch(pu.Path)
	if m == nil {
		return nil
	}
	return &fpGitHubRepoInfo{Owner: m[1], Repo: m[2], Slug: m[1] + "/" + m[2],
		SSHURL: fmt.Sprintf("git@%s:%s/%s.git", pu.Host, m[1], m[2]), Host: pu.Host}
}

func fpIsGitHubHTTPSCloneURL(u string) bool {
	pu, err := url.Parse(u)
	return err == nil && pu.Scheme == "https" && IsGitHubHost(pu.Host)
}

// IsGitInstalled reports whether the git binary is available.
func IsGitInstalled() bool {
	_, err := exec.LookPath(fpGitBinary)
	return err == nil
}

type fpGitRunError struct {
	output   string
	timedOut bool
	err      error
}

func (e *fpGitRunError) Error() string {
	if e.timedOut {
		return "git timed out"
	}
	if s := strings.TrimSpace(e.output); s != "" {
		return s
	}
	return e.err.Error()
}

// fpRunGit runs git with the hardened config flags and environment.
func fpRunGit(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	full := append(append([]string{}, GitConfigArgs...), args...)
	cmd := exec.CommandContext(ctx, fpGitBinary, full...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = GitEnv(extraEnv...)
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		return buf.String(), &fpGitRunError{output: buf.String(), timedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), err: err}
	}
	return buf.String(), nil
}

func fpResetDir(dir string) error {
	_ = os.RemoveAll(dir)
	return os.MkdirAll(dir, 0o755)
}

func fpGitClone(ctx context.Context, cloneURL, ref, dir string, extraEnv []string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", cloneURL, dir)
	_, err := fpRunGit(ctx, "", extraEnv, args...)
	return err
}

// fpCloneAtSha fetches a single commit into an empty repo and checks it out.
func fpCloneAtSha(ctx context.Context, cloneURL, sha, dir string, extraEnv []string) error {
	if _, err := fpRunGit(ctx, dir, extraEnv, "init", "-q"); err != nil {
		return err
	}
	if _, err := fpRunGit(ctx, dir, extraEnv, "remote", "add", "origin", cloneURL); err != nil {
		return err
	}
	if _, err := fpRunGit(ctx, dir, extraEnv, "fetch", "--depth", "1", "origin", sha); err != nil {
		return err
	}
	_, err := fpRunGit(ctx, dir, extraEnv, "checkout", "-q", "FETCH_HEAD")
	return err
}

func fpTryGhClone(ctx context.Context, repo *fpGitHubRepoInfo, dir, ref string) bool {
	if _, err := exec.LookPath(fpGhBinary); err != nil {
		return false
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status := exec.CommandContext(sctx, fpGhBinary, "auth", "status", "-h", repo.Host)
	status.Env = fpOverrideEnv(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := status.CombinedOutput()
	if err != nil {
		return false
	}
	target := repo.Slug
	if ghSSHProtocolRe.Match(out) {
		target = repo.SSHURL
	}
	flags := []string{"--depth=1"}
	if ref != "" {
		flags = append(flags, "--branch", ref)
	}
	clone := exec.CommandContext(ctx, fpGhBinary, append([]string{"repo", "clone", target, dir, "--"}, flags...)...)
	clone.Env = fpOverrideEnv(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL="+allowedGitProtocols)
	clone.Stdout = io.Discard
	clone.Stderr = io.Discard
	return clone.Run() == nil
}

func fpGitAuthError(cloneURL string, repo *fpGitHubRepoInfo, msg string, cause error) *DiagnosticError {
	host := "github.com"
	if repo != nil {
		host = repo.Host
	}
	if repo != nil && IsGitHubSSOAuthError(msg) {
		return Diagnosticf(CodeGitSAMLSSOBlocked,
			"GitHub blocked HTTPS access to %s because the organization enforces SAML SSO.\n"+
				"  skills tried your existing git credentials and available fallbacks, but none succeeded.\n"+
				"  - Re-authorize your GitHub credentials/app for that org's SSO policy\n"+
				"  - Or rerun with SSH: npx skills add %s\n"+
				"  - Verify access with: gh auth status -h %s or ssh -T git@%s", cloneURL, repo.SSHURL, host, host).WithCause(cause)
	}
	if repo != nil {
		return Diagnosticf(CodeGitAuthFailed,
			"Authentication failed for %s.\n"+
				"  - For private repos, ensure you have access\n"+
				"  - Retry with SSH: npx skills add %s\n"+
				"  - Check access with: gh auth status -h %s or ssh -T git@%s", cloneURL, repo.SSHURL, host, host).WithCause(cause)
	}
	return Diagnosticf(CodeGitAuthFailed,
		"Authentication failed for %s.\n"+
			"  - For private repos, ensure you have access\n"+
			"  - For SSH: Check your keys with 'ssh -T git@github.com'\n"+
			"  - For HTTPS: Run 'gh auth login' or configure git credentials", cloneURL).WithCause(cause)
}

// fpUpdateSubmodules best-effort initializes submodules when .gitmodules
// exists. The file transport is excluded so untrusted .gitmodules cannot
// reference local repositories.
func fpUpdateSubmodules(ctx context.Context, dir string) {
	if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err != nil {
		return
	}
	env := []string{"GIT_ALLOW_PROTOCOL=" + submoduleGitProtocol}
	if _, err := fpRunGit(ctx, dir, env, "submodule", "update", "--init", "--recursive", "--depth", "1"); err != nil {
		_, _ = fpRunGit(ctx, dir, env, "submodule", "update", "--init", "--recursive")
	}
}

// CloneRepo clones cloneURL (optionally at ref: branch, tag or full commit
// SHA) into a new "skills-*" temp dir and returns it. It rejects the ext::
// transport, applies the hardened git config/env, falls back to fetching a
// commit SHA directly, and for GitHub HTTPS auth failures retries through
// `gh repo clone` and SSH.
func CloneRepo(cloneURL, ref string) (string, error) {
	if extTransportRe.MatchString(cloneURL) {
		return "", ErrUnsupportedGitTransport("ext")
	}
	if !IsGitInstalled() {
		return "", Diagnosticf(CodeGitCloneFailed, "Failed to clone %s: git is not installed or not on PATH", cloneURL)
	}
	tempDir, err := os.MkdirTemp("", "skills-")
	if err != nil {
		return "", err
	}
	timeout := CloneTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	refCanBeSha := ref != "" && IsCommitSHA(ref)
	repo := fpParseGitHubRepoURL(cloneURL)

	cloneErr := fpGitClone(ctx, cloneURL, ref, tempDir, nil)
	if cloneErr == nil {
		fpUpdateSubmodules(ctx, tempDir)
		return tempDir, nil
	}
	msg := cloneErr.Error()

	if refCanBeSha && IsMissingRefError(msg) {
		if fpResetDir(tempDir) == nil && fpCloneAtSha(ctx, cloneURL, ref, tempDir, nil) == nil {
			fpUpdateSubmodules(ctx, tempDir)
			return tempDir, nil
		}
	}

	var gre *fpGitRunError
	if errors.As(cloneErr, &gre) && gre.timedOut {
		_ = os.RemoveAll(tempDir)
		return "", Diagnosticf(CodeGitCloneTimeout,
			"Clone timed out after %ds. Common causes:\n"+
				"  - Large repository: raise the timeout with SKILLS_CLONE_TIMEOUT_MS=600000 (10m)\n"+
				"  - Slow network: retry, or clone manually and pass the local path to 'skills add'\n"+
				"  - Private repo without credentials: ensure auth is configured\n"+
				"      - For SSH: ssh-add -l (to check loaded keys)\n"+
				"      - For HTTPS: gh auth status (if using GitHub CLI)", int(timeout.Round(time.Second)/time.Second)).WithCause(cloneErr)
	}

	authFailed := fpIsGitAuthFailure(msg)
	if authFailed && repo != nil && fpIsGitHubHTTPSCloneURL(cloneURL) {
		if fpResetDir(tempDir) == nil && fpTryGhClone(ctx, repo, tempDir, ref) {
			fpUpdateSubmodules(ctx, tempDir)
			return tempDir, nil
		}
		if fpResetDir(tempDir) == nil {
			sshCmd := os.Getenv("GIT_SSH_COMMAND")
			if sshCmd == "" {
				sshCmd = "ssh -o BatchMode=yes"
			}
			sshEnv := []string{"GIT_SSH_COMMAND=" + sshCmd}
			sshErr := fpGitClone(ctx, repo.SSHURL, ref, tempDir, sshEnv)
			if sshErr != nil && refCanBeSha && IsMissingRefError(sshErr.Error()) {
				if fpResetDir(tempDir) == nil {
					sshErr = fpCloneAtSha(ctx, repo.SSHURL, ref, tempDir, sshEnv)
				}
			}
			if sshErr == nil {
				fpUpdateSubmodules(ctx, tempDir)
				return tempDir, nil
			}
		}
	}

	_ = os.RemoveAll(tempDir)
	if authFailed {
		return "", fpGitAuthError(cloneURL, repo, msg, cloneErr)
	}
	return "", Diagnosticf(CodeGitCloneFailed, "Failed to clone %s: %s", cloneURL, strings.TrimSpace(msg)).WithCause(cloneErr)
}

// GetGitTreeHash returns the git tree SHA of the folder containing skillPath
// (a SKILL.md path, or "" / "SKILL.md" for the repo root) in a cloned repo.
// This matches the folder SHA from GitHub's Trees API. Returns "" on error.
func GetGitTreeHash(repoDir, skillPath string) string {
	normalized := strings.ReplaceAll(skillPath, `\`, "/")
	segs := strings.Split(normalized, "/")
	segs = segs[:len(segs)-1]
	folder := strings.Join(segs, "/")
	rev := "HEAD^{tree}"
	if folder != "" {
		rev = "HEAD:" + folder
	}
	return fpGitRevParse(repoDir, rev)
}

// GetHeadCommit returns the checked-out commit of a cloned repo ("" on error).
func GetHeadCommit(repoDir string) string {
	return fpGitRevParse(repoDir, "HEAD^{commit}")
}

func fpGitRevParse(repoDir, rev string) string {
	ctx, cancel := context.WithTimeout(context.Background(), CloneTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, fpGitBinary, "-C", repoDir, "rev-parse", "--verify", "--end-of-options", rev)
	cmd.Env = fpOverrideEnv(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(out))
	if !IsCommitSHA(h) {
		return ""
	}
	return strings.ToLower(h)
}

// CleanupTempDir removes dir, refusing anything outside os.TempDir().
func CleanupTempDir(dir string) error {
	if dir == "" {
		return nil
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ErrCleanupOutsideTempDir()
	}
	absDir = filepath.Clean(absDir)
	if !fpIsWithinTempDir(absDir) {
		return ErrCleanupOutsideTempDir()
	}
	return os.RemoveAll(absDir)
}

func fpIsWithinTempDir(absDir string) bool {
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	candidates := []string{filepath.Clean(tmp)}
	if real, err := filepath.EvalSymlinks(tmp); err == nil {
		candidates = append(candidates, filepath.Clean(real))
	}
	dirs := []string{absDir}
	if real, err := filepath.EvalSymlinks(absDir); err == nil {
		dirs = append(dirs, filepath.Clean(real))
	}
	for _, d := range dirs {
		for _, t := range candidates {
			if d == t || strings.HasPrefix(d, t+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Notion helpers (upstream src/notion-test.ts URL handling).

var (
	notionHostRe   = regexp.MustCompile(`(?i)(^|\.)notion\.(so|com)$`)
	notionPageIDRe = regexp.MustCompile(`(?:^|-)([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
)

// IsNotionSource reports the literal "notion" source (all workspace packs).
func IsNotionSource(source string) bool { return strings.EqualFold(source, "notion") }

// ParseNotionSkillURL returns the hyphenated page UUID of a notion.so /
// notion.com page URL, or "" when source is not such a URL.
func ParseNotionSkillURL(source string) string {
	if !spIsHTTPURL(source) {
		return ""
	}
	u, err := url.Parse(source)
	if err != nil || !notionHostRe.MatchString(strings.ToLower(u.Hostname())) {
		return ""
	}
	segs := spNonEmptySegments(u.EscapedPath())
	if len(segs) == 0 {
		return ""
	}
	last, err := url.PathUnescape(segs[len(segs)-1])
	if err != nil {
		last = segs[len(segs)-1]
	}
	m := notionPageIDRe.FindStringSubmatch(strings.ToLower(last))
	if m == nil {
		return ""
	}
	id := strings.ToLower(strings.ReplaceAll(m[1], "-", ""))
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32]
}

// ---------------------------------------------------------------------------
// Notion Agent Plugins via the `ntn` CLI (upstream src/notion-test.ts,
// non-interactive parts).

const (
	notionAPIVersion       = "2026-03-11"
	ntnTimeout             = 30 * time.Second
	ntnMaxOutputBytes      = 10 * 1024 * 1024
	notionDownloadMaxBytes = 50 * 1024 * 1024
	notionExtractMaxBytes  = 100 * 1024 * 1024
	notionExtractMaxFiles  = 5000
	notionPackURLPrefix    = "notion://packs/"
)

// fpNtnBinary is the Notion CLI executable (overridable in tests).
var fpNtnBinary = "ntn"

// NotionPack is one Notion Agent Plugins pack.
type NotionPack struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	VersionID   string `json:"version_id"`
}

// NotionDirectory is a pack or skill directory with a temporary download URL.
type NotionDirectory struct {
	ID        string
	VersionID string
	URL       string
}

type fpCappedBuffer struct {
	buf      bytes.Buffer
	total    *int64
	exceeded *bool
}

func (c *fpCappedBuffer) Write(p []byte) (int, error) {
	*c.total += int64(len(p))
	if *c.total > ntnMaxOutputBytes {
		*c.exceeded = true
		return 0, errors.New("ntn output too large")
	}
	return c.buf.Write(p)
}

// RunNtnAPI runs `ntn <args>` with a 30s timeout and a 10 MiB output cap.
func RunNtnAPI(args []string) (string, error) {
	if os.Getenv("SKILLS_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[notion] ntn %s\n", strings.Join(args, " "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), ntnTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, fpNtnBinary, args...)
	var total int64
	var exceeded bool
	stdout := &fpCappedBuffer{total: &total, exceeded: &exceeded}
	stderr := &fpCappedBuffer{total: &total, exceeded: &exceeded}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if exceeded {
		return "", NewDiagnostic(CodeNotionAPIOutputTooLarge, "ntn api output exceeded 10 MiB", "")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", Diagnosticf(CodeNotionAPITimeout, "ntn api timed out after %d seconds", int(ntnTimeout/time.Second))
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimSpace(StripTerminalEscapes(stderr.buf.String()))
			if msg != "" {
				return "", Diagnosticf(CodeNotionAPIFailed, "ntn api failed: %s", msg)
			}
			return "", Diagnosticf(CodeNotionAPIFailed, "ntn api failed with exit code %d", exitErr.ExitCode())
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return "", NewDiagnostic(CodeNotionCLIMissing,
				"Notion CLI (ntn) is required. Install it from:\nhttps://developers.notion.com/cli/get-started/overview\nThen run `ntn login`.", "").WithCause(err)
		}
		return "", Diagnosticf(CodeNotionCLIStartFailed, "Unable to start ntn: %s", StripTerminalEscapes(err.Error())).WithCause(err)
	}
	return stdout.buf.String(), nil
}

func fetchNotionJSON(args []string, label string) (interface{}, error) {
	out, err := RunNtnAPI(append(append([]string{"api"}, args...), "--notion-version", notionAPIVersion))
	if err != nil {
		return nil, err
	}
	var v interface{}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, Diagnosticf(CodeNotionInvalidJSON, "ntn returned invalid JSON for %s", label).WithCause(err)
	}
	return v, nil
}

func notionMissingField(response, field string) error {
	return Diagnosticf(CodeNotionResponseMissingField, "Notion Agent Plugins %s is missing %s", response, field)
}

func notionAssertString(v interface{}, field string) (string, error) {
	s, ok := v.(string)
	if !ok || s == "" {
		return "", notionMissingField("response", field)
	}
	return s, nil
}

func parseNotionPack(v interface{}) (*NotionPack, error) {
	rec, ok := v.(map[string]interface{})
	if !ok {
		return nil, NewDiagnostic(CodeNotionInvalidPack, "Notion Agent Plugins response contains an invalid pack", "")
	}
	rawName, ok := rec["name"].(string)
	if !ok {
		return nil, notionMissingField("response", "pack.name")
	}
	name := SanitizeMetadata(rawName)
	if name == "" {
		return nil, nil
	}
	id, err := notionAssertString(rec["id"], "pack.id")
	if err != nil {
		return nil, err
	}
	desc := ""
	if d, present := rec["description"]; present && d != nil {
		ds, ok := d.(string)
		if !ok {
			return nil, Diagnosticf(CodeNotionResponseInvalidField, "Notion Agent Plugins response has an invalid %s", "pack.description")
		}
		desc = SanitizeMetadata(ds)
	}
	ver, err := notionAssertString(rec["version_id"], "pack.version_id")
	if err != nil {
		return nil, err
	}
	return &NotionPack{ID: id, Name: name, Description: desc, VersionID: ver}, nil
}

// FetchNotionPacks lists every pack in the authenticated workspace,
// following pagination cursors (rejecting repeated or missing cursors).
func FetchNotionPacks() ([]NotionPack, error) {
	var packs []NotionPack
	seen := map[string]bool{}
	cursor := ""
	for {
		args := []string{"/v1/ai/plugins", "page_size==100"}
		if cursor != "" {
			args = append(args, "start_cursor=="+cursor)
		}
		v, err := fetchNotionJSON(args, "the Notion packs list")
		if err != nil {
			return nil, err
		}
		rec, ok := v.(map[string]interface{})
		results, ok2 := rec["results"].([]interface{})
		if !ok || !ok2 {
			return nil, NewDiagnostic(CodeNotionInvalidListResponse, "Notion Agent Plugins list response is invalid", "")
		}
		hasMore, ok := rec["has_more"].(bool)
		if !ok {
			return nil, notionMissingField("list response", "has_more")
		}
		next := ""
		if nc, present := rec["next_cursor"]; present && nc != nil {
			s, ok := nc.(string)
			if !ok {
				return nil, Diagnosticf(CodeNotionResponseInvalidField, "Notion Agent Plugins %s has an invalid %s", "list response", "next_cursor")
			}
			next = s
		}
		for _, r := range results {
			p, err := parseNotionPack(r)
			if err != nil {
				return nil, err
			}
			if p != nil {
				packs = append(packs, *p)
			}
		}
		if !hasMore {
			return packs, nil
		}
		if next == "" || seen[next] {
			return nil, NewDiagnostic(CodeNotionInvalidCursor, "Notion Agent Plugins pagination returned an invalid cursor", "")
		}
		seen[next] = true
		cursor = next
	}
}

func parseNotionDirectory(v interface{}, label string) (*NotionDirectory, error) {
	rec, ok := v.(map[string]interface{})
	if !ok {
		return nil, Diagnosticf(CodeNotionInvalidDirectoryResponse, "Notion %s directory response is invalid", label)
	}
	id, err := notionAssertString(rec["id"], label+" directory id")
	if err != nil {
		return nil, err
	}
	ver, err := notionAssertString(rec["version_id"], label+" directory version_id")
	if err != nil {
		return nil, err
	}
	u, err := notionAssertString(rec["url"], label+" directory url")
	if err != nil {
		return nil, err
	}
	return &NotionDirectory{ID: id, VersionID: ver, URL: u}, nil
}

// FetchNotionPackDirectory resolves a fresh download URL for pack, failing
// if the pack changed since it was listed.
func FetchNotionPackDirectory(pack NotionPack) (*NotionDirectory, error) {
	quoted, _ := json.Marshal(pack.Name)
	v, err := fetchNotionJSON([]string{"/v1/ai/plugins/" + fpEncodeURIComponent(pack.ID)}, "Notion pack "+string(quoted))
	if err != nil {
		return nil, err
	}
	dir, err := parseNotionDirectory(v, "pack")
	if err != nil {
		return nil, err
	}
	if dir.ID != pack.ID || dir.VersionID != pack.VersionID {
		return nil, Diagnosticf(CodeNotionPackChanged, "Notion pack %s changed while preparing installation", string(quoted))
	}
	return dir, nil
}

// FetchNotionSkillDirectory resolves a fresh download URL for a skill page.
func FetchNotionSkillDirectory(pageID string) (*NotionDirectory, error) {
	v, err := fetchNotionJSON([]string{"/v1/ai/skills/" + fpEncodeURIComponent(pageID)}, "Notion skill "+pageID)
	if err != nil {
		return nil, err
	}
	return parseNotionDirectory(v, "skill")
}

// NotionPackURL is the lock URL of a pack (ntn hands out expiring URLs).
func NotionPackURL(packID string) string { return notionPackURLPrefix + fpEncodeURIComponent(packID) }

// NotionPageURL is the lock URL of a skill page (id only).
func NotionPageURL(pageID string) string {
	return "https://www.notion.so/" + strings.ReplaceAll(pageID, "-", "")
}

func parseNotionPackURL(u string) string {
	if !strings.HasPrefix(u, notionPackURLPrefix) {
		return ""
	}
	id, err := url.PathUnescape(u[len(notionPackURLPrefix):])
	if err != nil {
		return ""
	}
	return id
}

// IsNotionPackURL reports notion://packs/<id> lock URLs.
func IsNotionPackURL(u string) bool { return parseNotionPackURL(u) != "" }

func downloadNotionDirectory(u string) (*DownloadedSource, error) {
	return DownloadSource(u, DownloadSourceOptions{
		DownloadMaxBytes: notionDownloadMaxBytes, ExtractMaxBytes: notionExtractMaxBytes, ExtractMaxFiles: notionExtractMaxFiles,
	})
}

// DownloadNotionURL re-downloads a Notion pack or page recorded in a lock
// file (resolving a fresh URL through ntn). Returns (nil, nil) when u is not
// a Notion lock URL.
func DownloadNotionURL(u string) (*DownloadedSource, error) {
	if packID := parseNotionPackURL(u); packID != "" {
		packs, err := FetchNotionPacks()
		if err != nil {
			return nil, err
		}
		for _, p := range packs {
			if p.ID == packID {
				dir, err := FetchNotionPackDirectory(p)
				if err != nil {
					return nil, err
				}
				return downloadNotionDirectory(dir.URL)
			}
		}
		return nil, Diagnosticf(CodeNotionNoMatchingPacks, "No matching Notion packs found for: %s", packID)
	}
	if pageID := ParseNotionSkillURL(u); pageID != "" {
		dir, err := FetchNotionSkillDirectory(pageID)
		if err != nil {
			return nil, err
		}
		return downloadNotionDirectory(dir.URL)
	}
	return nil, nil
}

var notionSelectorRe = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeNotionSelector(v string) string {
	return strings.Trim(notionSelectorRe.ReplaceAllString(strings.ToLower(v), "-"), "-")
}

// FilterNotionPacks selects packs by name slug or id ("*" = all).
func FilterNotionPacks(packs []NotionPack, selectors []string) []NotionPack {
	want := map[string]bool{}
	for _, s := range selectors {
		if s == "*" {
			return packs
		}
		want[normalizeNotionSelector(s)] = true
	}
	var out []NotionPack
	for _, p := range packs {
		if want[normalizeNotionSelector(p.Name)] || want[strings.ToLower(p.ID)] {
			out = append(out, p)
		}
	}
	return out
}

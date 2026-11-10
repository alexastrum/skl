package skl

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestParseSourceTable(t *testing.T) {
	t.Setenv("GH_HOST", "")
	sha := "d22f72308106ef62f558cbde16dacb1f21031ae3"
	cases := []struct {
		in   string
		want ParsedSource
	}{
		// GitHub URLs.
		{"https://github.com/owner/repo", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"}},
		{"https://github.com/owner/repo.git", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"}},
		{"https://github.com/owner/repo.git#feature/install", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "feature/install"}},
		{"https://github.com/owner/repo/blob/main/README.md#L10", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"}},
		{"https://github.com/owner/repo/tree/feature-branch", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "feature-branch"}},
		{"https://github.com/owner/repo/tree/main/skills/my-skill", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "main", Subpath: "skills/my-skill"}},
		{"https://github.com/owner/repo/tree/feature/my-feature", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "feature", Subpath: "my-feature"}},
		// GitLab.
		{"https://gitlab.com/owner/repo", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/owner/repo.git"}},
		{"https://gitlab.com/owner/repo/-/tree/develop", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/owner/repo.git", Ref: "develop"}},
		{"https://gitlab.com/owner/repo/-/tree/main/src/skills", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/owner/repo.git", Ref: "main", Subpath: "src/skills"}},
		{"https://gitlab.com/owner/repo.git", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/owner/repo.git"}},
		{"https://gitlab.com/coresofthq/ai/agent-skills", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/coresofthq/ai/agent-skills.git"}},
		{"https://gitlab.com/org/team/project/repo.git", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/org/team/project/repo.git"}},
		{"https://gitlab.com/group/subgroup/repo/-/tree/main/path/to/skill", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/group/subgroup/repo.git", Ref: "main", Subpath: "path/to/skill"}},
		{"https://gitlab.com/group/subgroup/repo/", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/group/subgroup/repo.git"}},
		{"https://git.corp.com/group/subgroup/project/-/tree/main/src", ParsedSource{Type: "gitlab", URL: "https://git.corp.com/group/subgroup/project.git", Ref: "main", Subpath: "src"}},
		{"https://gitlab.example.com/org/repo/-/tree/v1.0", ParsedSource{Type: "gitlab", URL: "https://gitlab.example.com/org/repo.git", Ref: "v1.0"}},
		{"https://git.corp.com:8443/group/repo/-/tree/main", ParsedSource{Type: "gitlab", URL: "https://git.corp.com:8443/group/repo.git", Ref: "main"}},
		{"http://git.local/group/repo/-/tree/dev", ParsedSource{Type: "gitlab", URL: "http://git.local/group/repo.git", Ref: "dev"}},
		{"https://gitlab.com/~user/project/-/tree/main", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/~user/project.git", Ref: "main"}},
		{"gitlab:owner/repo", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/owner/repo.git"}},
		{"gitlab:group/subgroup/repo", ParsedSource{Type: "gitlab", URL: "https://gitlab.com/group/subgroup/repo.git"}},
		// Azure Repos.
		{"https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills"}},
		{"https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills.git", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills"}},
		{"https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills?path=/skills/web-design&version=GBmain", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills", Ref: "main", Subpath: "skills/web-design"}},
		{"http://ado.example.com:8080/tfs/DefaultCollection/MyProject/_git/skills", ParsedSource{Type: "git", URL: "http://ado.example.com:8080/tfs/DefaultCollection/MyProject/_git/skills"}},
		{"https://dev.azure.com/My%20Org/My%20Project/_git/agent-skills", ParsedSource{Type: "git", URL: "https://dev.azure.com/My%20Org/My%20Project/_git/agent-skills"}},
		{"https://dev.azure.com/fabrikam/Fiber/_git/skills?version=GTv1.2.0", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/Fiber/_git/skills", Ref: "v1.2.0"}},
		{"https://dev.azure.com/fabrikam/Fiber/_git/skills?version=GC2c732a035bae9fb21e7823d37b2b9553e272f0c6", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/Fiber/_git/skills"}},
		{"https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills#release-2026", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/FabrikamFiber/_git/skills", Ref: "release-2026"}},
		{"https://dev.azure.com/fabrikam/Fiber/_git/skills/", ParsedSource{Type: "git", URL: "https://dev.azure.com/fabrikam/Fiber/_git/skills"}},
		// Generic, well-known, downloads.
		{"https://git.mycompany.com/my-group/my-repo.git", ParsedSource{Type: "git", URL: "https://git.mycompany.com/my-group/my-repo.git"}},
		{"https://git.example.com/owner/repo.git#release-2026", ParsedSource{Type: "git", URL: "https://git.example.com/owner/repo.git", Ref: "release-2026"}},
		{"ssh://git@git.example.com:7999/owner/repo.git#release-2026", ParsedSource{Type: "git", URL: "ssh://git@git.example.com:7999/owner/repo.git", Ref: "release-2026"}},
		{"https://google.com/search/result", ParsedSource{Type: "well-known", URL: "https://google.com/search/result"}},
		{"https://internal.example.com/download?id=123", ParsedSource{Type: "well-known", URL: "https://internal.example.com/download?id=123"}},
		{"https://example.com/docs#section", ParsedSource{Type: "well-known", URL: "https://example.com/docs#section"}},
		{"https://raw.githubusercontent.com/owner/repo/main/SKILL.md", ParsedSource{Type: "download", URL: "https://raw.githubusercontent.com/owner/repo/main/SKILL.md"}},
		{"https://github.com/acme/skills/releases/download/v1/skills.tgz", ParsedSource{Type: "download", URL: "https://github.com/acme/skills/releases/download/v1/skills.tgz"}},
		{"https://github.com/acme/skills/archive/refs/heads/main.zip", ParsedSource{Type: "download", URL: "https://github.com/acme/skills/archive/refs/heads/main.zip"}},
		{"https://gitlab.com/acme/skills/-/archive/main/skills-main.tar.gz", ParsedSource{Type: "download", URL: "https://gitlab.com/acme/skills/-/archive/main/skills-main.tar.gz"}},
		// skills.sh short form routes to well-known.
		{"skills.sh/p/my-pack", ParsedSource{Type: "well-known", URL: "https://skills.sh/p/my-pack"}},
		{"www.skills.sh/acme/pack#x", ParsedSource{Type: "well-known", URL: "https://www.skills.sh/acme/pack#x"}},
		// Shorthand.
		{"owner/repo", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"}},
		{"owner/repo/", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"}},
		{"owner/repo/skills/my-skill", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Subpath: "skills/my-skill"}},
		{"owner/repo@my-skill", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", SkillFilter: "my-skill"}},
		{"vercel-labs/agent-skills#feature/install", ParsedSource{Type: "github", URL: "https://github.com/vercel-labs/agent-skills.git", Ref: "feature/install"}},
		{"owner/repo/skills/my-skill#feature/skills", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "feature/skills", Subpath: "skills/my-skill"}},
		{"owner/repo#my-branch@my-skill", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "my-branch", SkillFilter: "my-skill"}},
		{"owner/repo#@review", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", SkillFilter: "review"}},
		{"owner/repo#v1.0.0+build.5", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "v1.0.0+build.5"}},
		{"owner/repo#release%40v1@review", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "release@v1", SkillFilter: "review"}},
		{"github:owner/repo", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"}},
		{"github:owner/repo/skills/my-skill", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Subpath: "skills/my-skill"}},
		{"github:owner/repo@my-skill", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", SkillFilter: "my-skill"}},
		{"github:owner/repo#feature/install", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "feature/install"}},
		// Aliases.
		{"coinbase/agentWallet", ParsedSource{Type: "github", URL: "https://github.com/coinbase/agentic-wallet-skills.git"}},
		{"vercel-labs/vercel-skills", ParsedSource{Type: "github", URL: "https://github.com/vercel-labs/agent-skills.git"}},
		// SSH.
		{"git@github.com:owner/repo.git", ParsedSource{Type: "git", URL: "git@github.com:owner/repo.git"}},
		{"git@github.com:owner/repo.git#feature/install", ParsedSource{Type: "git", URL: "git@github.com:owner/repo.git", Ref: "feature/install"}},
		{"git@github.com:owner/repo/tree/mybranch", ParsedSource{Type: "git", URL: "git@github.com:owner/repo.git", Ref: "mybranch"}},
		{"git@github.com:owner/repo/tree/mybranch/skills/foo", ParsedSource{Type: "git", URL: "git@github.com:owner/repo.git", Ref: "mybranch", Subpath: "skills/foo"}},
		{"git@github.com:owner/repo.git/tree/mybranch", ParsedSource{Type: "git", URL: "git@github.com:owner/repo.git", Ref: "mybranch"}},
		{"git@git.example.com:group/repo/tree/mybranch.git", ParsedSource{Type: "git", URL: "git@git.example.com:group/repo/tree/mybranch.git"}},
		// Version pins (#2527).
		{"mattpocock/skills@v1.3.1", ParsedSource{Type: "github", URL: "https://github.com/mattpocock/skills.git", Ref: "v1.3.1"}},
		{"owner/repo@v1.2.3-beta.1+build.5", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: "v1.2.3-beta.1+build.5"}},
		{"owner/repo@" + sha, ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: sha}},
	}
	for _, tag := range []string{"1.2.3", "v1.2", "2026.10.1", "1.0.0-rc.1"} {
		cases = append(cases, struct {
			in   string
			want ParsedSource
		}{"owner/repo@" + tag, ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", Ref: tag}})
	}
	for _, name := range []string{"find-skills", "v2", "2", "v1.x", "web-design-guidelines"} {
		cases = append(cases, struct {
			in   string
			want ParsedSource
		}{"owner/repo@" + name, ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git", SkillFilter: name}})
	}
	for _, c := range cases {
		got, err := ParseSource(c.in)
		if err != nil {
			t.Errorf("ParseSource(%q) error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseSource(%q)\n got  %+v\n want %+v", c.in, got, c.want)
		}
	}
}

func TestParseSourcePrefixFragmentsRoundTrip(t *testing.T) {
	t.Setenv("GH_HOST", "")
	for _, frag := range []string{"#release%40v1@review", "#release%2540v1@review", "#main@code%40review", "#main@code%2520review", "#@review", "#feature/install@review"} {
		a, err1 := ParseSource("github:owner/repo" + frag)
		b, err2 := ParseSource("owner/repo" + frag)
		if err1 != nil || err2 != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("github: fragment %q: %+v (%v) vs %+v (%v)", frag, a, err1, b, err2)
		}
	}
	for _, frag := range []string{"#release%40v1", "#release%2540v1", "#feature/install"} {
		a, err1 := ParseSource("gitlab:owner/repo" + frag)
		b, err2 := ParseSource("https://gitlab.com/owner/repo" + frag)
		if err1 != nil || err2 != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("gitlab: fragment %q: %+v vs %+v", frag, a, b)
		}
	}
}

func TestParseSourceGHHost(t *testing.T) {
	t.Setenv("GH_HOST", "github.example.com")
	check := func(in string, want ParsedSource) {
		t.Helper()
		got, err := ParseSource(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseSource(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	check("acme/agent-skills", ParsedSource{Type: "git", URL: "https://github.example.com/acme/agent-skills.git"})
	check("github:acme/agent-skills@review", ParsedSource{Type: "git", URL: "https://github.example.com/acme/agent-skills.git", SkillFilter: "review"})
	check("https://github.example.com/acme/agent-skills/tree/main/skills/review",
		ParsedSource{Type: "git", URL: "https://github.example.com/acme/agent-skills.git", Ref: "main", Subpath: "skills/review"})
	check("https://github.com/owner/repo", ParsedSource{Type: "github", URL: "https://github.com/owner/repo.git"})
	check("git@github.example.com:acme/repo/tree/dev", ParsedSource{Type: "git", URL: "git@github.example.com:acme/repo.git", Ref: "dev"})

	// Pinned host from a lock entry wins over GH_HOST.
	got, _ := ParseSource("acme/agent-skills", ParseSourceOptions{GitHubHost: "github.com"})
	if got.Type != "github" || got.URL != "https://github.com/acme/agent-skills.git" {
		t.Errorf("pinned host: %+v", got)
	}
}

func TestGetGitHubHost(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "github.com",
		"  GitHub.Example.COM  ": "github.example.com",
		"user@ghe.example.com":   "github.com",
		"ghe.example.com:8443":   "github.com",
		"ghe.example.com/path":   "github.com",
		"ghe.example.com?x=1":    "github.com",
		"ghe.example.com#frag":   "github.com",
	} {
		t.Setenv("GH_HOST", in)
		if got := GetGitHubHost(); got != want {
			t.Errorf("GH_HOST=%q: got %q want %q", in, got, want)
		}
	}
	t.Setenv("GH_HOST", "ghe.example.com")
	if !IsGitHubHost("GHE.example.com") || !IsGitHubHost("github.com") || IsGitHubHost("gitlab.com") {
		t.Error("IsGitHubHost mismatch")
	}
}

func TestParseSourceLocal(t *testing.T) {
	cwd := t.TempDir()
	for _, in := range []string{"./my-skills", "../other", ".", "..", `.\win`, "~/skills"} {
		got, err := ParseSource(in, ParseSourceOptions{Cwd: cwd})
		if err != nil || got.Type != SourceTypeLocal || got.LocalPath == "" || got.URL != got.LocalPath || !filepath.IsAbs(got.LocalPath) {
			t.Errorf("ParseSource(%q) = %+v, %v", in, got, err)
		}
	}
	got, _ := ParseSource("./my-skills", ParseSourceOptions{Cwd: cwd})
	if got.LocalPath != filepath.Join(cwd, "my-skills") {
		t.Errorf("relative resolve: %q", got.LocalPath)
	}
	abs := filepath.Join(cwd, "abs")
	got, _ = ParseSource(abs)
	if got.Type != SourceTypeLocal || got.LocalPath != abs {
		t.Errorf("absolute: %+v", got)
	}
	if got, _ := ParseSource(`C:\skills`); got.Type != SourceTypeLocal {
		t.Errorf("windows drive: %+v", got)
	}
}

func TestParseSourceUnsafeSubpath(t *testing.T) {
	for _, in := range []string{
		"git@github.com:owner/repo/tree/main/../../etc",
		"owner/repo/../../etc",
		"https://github.com/owner/repo/tree/main/../x",
		"https://gitlab.com/o/r/-/tree/main/a/../../b",
		"https://dev.azure.com/o/p/_git/r?path=/../x",
	} {
		_, err := ParseSource(in)
		if !IsDiagnostic(err, CodeUnsafeSubpath) {
			t.Errorf("ParseSource(%q) err = %v; want SKILLS_UNSAFE_SUBPATH", in, err)
		}
	}
	for _, ok := range []string{".hidden", "..skill", "skill..", "a/b"} {
		if _, err := SanitizeSubpath(ok); err != nil {
			t.Errorf("SanitizeSubpath(%q) = %v", ok, err)
		}
	}
	if _, err := SanitizeSubpath(`a\..\b`); err == nil {
		t.Error("backslash traversal accepted")
	}
}

func TestGetOwnerRepo(t *testing.T) {
	t.Setenv("GH_HOST", "")
	parse := func(s string) ParsedSource { p, _ := ParseSource(s); return p }
	cases := []struct {
		p    ParsedSource
		want string
	}{
		{parse("https://github.com/owner/repo"), "owner/repo"},
		{parse("https://github.com/owner/repo/tree/main/skills/x"), "owner/repo"},
		{parse("owner/repo/skills/my-skill"), "owner/repo"},
		{parse("https://gitlab.com/owner/repo/-/tree/main/skills"), "owner/repo"},
		{parse("https://gitlab.com/coresofthq/ai/agent-skills"), "coresofthq/ai/agent-skills"},
		{parse("./my-skills"), ""},
		{parse("https://git.example.com/owner/repo.git"), "owner/repo"},
		{parse("git@github.com:owner/repo.git"), "owner/repo"},
		{parse("https://gitlab.company.com/team/repo"), "team/repo"},
		{ParsedSource{Type: "git", URL: "https://git.example.com/owner/repo?ref=main"}, "owner/repo"},
		{ParsedSource{Type: "git", URL: "https://git.example.com/owner/repo#readme"}, "owner/repo"},
		{ParsedSource{Type: "git", URL: "https://git.example.com/owner/repo.git?ref=main"}, "owner/repo"},
		{ParsedSource{Type: "git", URL: "git@gitlab.com:group/subgroup/project/repo.git"}, "group/subgroup/project/repo"},
		{ParsedSource{Type: "git", URL: "git@github.com:owner/repo"}, "owner/repo"},
		{ParsedSource{Type: "git", URL: "ssh://git@git.company.com:7999/org/team/repo.git"}, "org/team/repo"},
		{ParsedSource{Type: "git", URL: "git@github.com:repo.git"}, ""},
		{ParsedSource{Type: "download", URL: "https://github.com/o/r/archive/main.zip"}, ""},
	}
	for _, c := range cases {
		if got := GetOwnerRepo(c.p); got != c.want {
			t.Errorf("GetOwnerRepo(%+v) = %q want %q", c.p, got, c.want)
		}
	}
}

func TestNotionHelpers(t *testing.T) {
	if !IsNotionSource("Notion") || IsNotionSource("notion.so") {
		t.Error("IsNotionSource")
	}
	cases := map[string]string{
		"https://www.notion.so/acme/My-Skill-0123456789abcdef0123456789ABCDEF":     "01234567-89ab-cdef-0123-456789abcdef",
		"https://acme.notion.site/x-0123456789abcdef0123456789abcdef":              "",
		"https://notion.com/p/01234567-89ab-cdef-0123-456789abcdef":                "01234567-89ab-cdef-0123-456789abcdef",
		"https://evil.com/0123456789abcdef0123456789abcdef":                        "",
		"https://www.notion.so/no-id-here":                                         "",
		"notion.so/0123456789abcdef0123456789abcdef":                               "",
		"https://www.notion.so/acme/My%20Skill-0123456789abcdef0123456789abcdef/":  "01234567-89ab-cdef-0123-456789abcdef",
		"https://www.notion.so/acme/My%20Skill%200123456789abcdef0123456789abcdef": "",
	}
	for in, want := range cases {
		if got := ParseNotionSkillURL(in); got != want {
			t.Errorf("ParseNotionSkillURL(%q) = %q want %q", in, got, want)
		}
	}
}

// --- git hardening -------------------------------------------------------

func TestGitEnvOverridesInherited(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:fd")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	env := GitEnv("GIT_SSH_COMMAND=ssh -x")
	got := map[string][]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = append(got[k], v)
	}
	want := map[string]string{
		"LC_ALL":              "C",
		"GIT_ALLOW_PROTOCOL":  "https:http:ssh:git:file",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_LFS_SKIP_SMUDGE": "1",
		"GIT_SSH_COMMAND":     "ssh -x",
	}
	for k, v := range want {
		if len(got[k]) != 1 || got[k][0] != v {
			t.Errorf("%s = %v, want exactly [%s]", k, got[k], v)
		}
	}
	joined := strings.Join(GitConfigArgs, " ")
	for _, want := range []string{"core.longpaths=true", "filter.lfs.required=false", "filter.lfs.smudge=", "filter.lfs.process="} {
		if !strings.Contains(joined, want) {
			t.Errorf("GitConfigArgs missing %s", want)
		}
	}
}

func TestCloneRepoRejectsExtTransportBeforeSpawningGit(t *testing.T) {
	old := fpGitBinary
	fpGitBinary = filepath.Join(t.TempDir(), "no-such-git")
	defer func() { fpGitBinary = old }()
	for _, u := range []string{"ext::sh -c touch% /tmp/pwned", "EXT::whatever"} {
		_, err := CloneRepo(u, "")
		if !IsDiagnostic(err, CodeUnsupportedGitTransport) || !strings.Contains(err.Error(), "Unsupported Git transport: ext") {
			t.Errorf("CloneRepo(%q) err = %v", u, err)
		}
	}
}

func TestMissingRefAndAuthClassifiers(t *testing.T) {
	for _, m := range []string{
		"warning: Could not find remote branch x to clone.\nfatal: Remote branch x not found in upstream origin",
		"fatal: couldn't find remote ref deadbeef",
		"error: upload-pack: not our ref d22f",
	} {
		if !IsMissingRefError(m) {
			t.Errorf("IsMissingRefError(%q) = false", m)
		}
	}
	if IsMissingRefError("fatal: repository not found") {
		t.Error("false positive")
	}
	if !IsGitHubSSOAuthError("The 'acme' organization has enabled or enforced SAML SSO.") {
		t.Error("SSO not detected")
	}
	if !fpIsGitAuthFailure("remote: Repository not found.") || fpIsGitAuthFailure("fatal: early EOF") {
		t.Error("auth classifier")
	}
	if !IsCommitSHA(strings.Repeat("A", 40)) || IsCommitSHA("deadbeef") {
		t.Error("IsCommitSHA")
	}
}

// isolatedGit configures a hermetic git environment and returns a helper
// that runs git in dir.
func isolatedGit(t *testing.T) func(dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	return func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

func writeFileT(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileURL(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func TestCloneRepoLocalBranchShaAndTreeHash(t *testing.T) {
	git := isolatedGit(t)
	repo := t.TempDir()
	git(repo, "init", "-q")
	git(repo, "config", "uploadpack.allowAnySHA1InWant", "true")
	writeFileT(t, filepath.Join(repo, "skills", "a", "SKILL.md"), "---\nname: a\ndescription: first\n---\n")
	git(repo, "add", "-A")
	git(repo, "commit", "-q", "-m", "one")
	first := git(repo, "rev-parse", "HEAD")
	writeFileT(t, filepath.Join(repo, "skills", "a", "SKILL.md"), "---\nname: a\ndescription: second\n---\n")
	git(repo, "commit", "-q", "-am", "two")
	git(repo, "branch", "feature/install")

	dir, err := CloneRepo(fileURL(repo), "")
	if err != nil {
		t.Fatalf("clone default: %v", err)
	}
	defer CleanupTempDir(dir)
	b, _ := os.ReadFile(filepath.Join(dir, "skills", "a", "SKILL.md"))
	if !strings.Contains(string(b), "second") {
		t.Errorf("default clone content: %s", b)
	}
	if want := git(repo, "rev-parse", "HEAD:skills/a"); GetGitTreeHash(dir, "skills/a/SKILL.md") != want {
		t.Errorf("GetGitTreeHash = %q want %q", GetGitTreeHash(dir, "skills/a/SKILL.md"), want)
	}
	if want := git(repo, "rev-parse", "HEAD^{tree}"); GetGitTreeHash(dir, "SKILL.md") != want {
		t.Errorf("root tree hash mismatch")
	}
	if GetHeadCommit(dir) != git(repo, "rev-parse", "HEAD") {
		t.Error("GetHeadCommit")
	}

	branchDir, err := CloneRepo(fileURL(repo), "feature/install")
	if err != nil {
		t.Fatalf("clone branch with slash: %v", err)
	}
	CleanupTempDir(branchDir)

	// A full commit SHA cannot be used with --branch: the fallback fetches it directly.
	shaDir, err := CloneRepo(fileURL(repo), first)
	if err != nil {
		t.Fatalf("clone at sha: %v", err)
	}
	defer CleanupTempDir(shaDir)
	b, _ = os.ReadFile(filepath.Join(shaDir, "skills", "a", "SKILL.md"))
	if !strings.Contains(string(b), "first") {
		t.Errorf("sha clone content: %s", b)
	}

	_, err = CloneRepo(fileURL(repo), "no-such-branch")
	if !IsDiagnostic(err, CodeGitCloneFailed) {
		t.Errorf("missing branch err = %v", err)
	}
}

// Human bug report: "git-lfs filter-process: git-lfs: command not found ... Clone succeeded, but checkout failed."
func TestCloneRepoSucceedsWhenLFSFilterIsUnavailable(t *testing.T) {
	git := isolatedGit(t)
	repo := t.TempDir()
	git(repo, "init", "-q")
	writeFileT(t, filepath.Join(repo, ".gitattributes"), "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	writeFileT(t, filepath.Join(repo, "SKILL.md"), "---\nname: x\ndescription: y\n---\n")
	writeFileT(t, filepath.Join(repo, "data.bin"), "version https://git-lfs.github.com/spec/v1\n")
	git(repo, "add", "-A")
	git(repo, "commit", "-q", "-m", "lfs")
	// A user-level LFS config pointing at a missing binary breaks plain clones.
	writeFileT(t, os.Getenv("GIT_CONFIG_GLOBAL"), "[filter \"lfs\"]\n\trequired = true\n\tprocess = git-lfs-missing-binary filter-process\n\tsmudge = git-lfs-missing-binary smudge -- %f\n")
	dir, err := CloneRepo(fileURL(repo), "")
	if err != nil {
		t.Fatalf("clone with broken lfs config: %v", err)
	}
	defer CleanupTempDir(dir)
	if _, err := os.Stat(filepath.Join(dir, "data.bin")); err != nil {
		t.Errorf("checkout incomplete: %v", err)
	}
}

func TestCloneRepoFakeGitArgsAndShaFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake git")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "log")
	script := filepath.Join(tmp, "git")
	// Fails `clone --branch <sha>` like a real server, records every call.
	writeFileT(t, script, `#!/bin/sh
echo "ARGS $* LC_ALL=$LC_ALL PROTO=$GIT_ALLOW_PROTOCOL PROMPT=$GIT_TERMINAL_PROMPT SMUDGE=$GIT_LFS_SKIP_SMUDGE" >> "`+logFile+`"
for a in "$@"; do
  if [ "$a" = "--branch" ]; then echo "fatal: Remote branch abc not found in upstream origin" >&2; exit 128; fi
done
exit 0
`)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	old := fpGitBinary
	fpGitBinary = script
	defer func() { fpGitBinary = old }()
	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	sha := strings.Repeat("ab", 20)
	dir, err := CloneRepo("https://git.example.com/o/r.git", sha)
	if err != nil {
		t.Fatalf("CloneRepo: %v", err)
	}
	defer CleanupTempDir(dir)
	data, _ := os.ReadFile(logFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 5 {
		t.Fatalf("expected clone + init/remote/fetch/checkout, got:\n%s", data)
	}
	for _, l := range lines {
		if !strings.Contains(l, "-c core.longpaths=true") || !strings.Contains(l, "-c filter.lfs.smudge=") {
			t.Errorf("missing hardened -c flags: %s", l)
		}
		if !strings.Contains(l, "LC_ALL=C ") || !strings.Contains(l, "PROTO=https:http:ssh:git:file") ||
			!strings.Contains(l, "PROMPT=0") || !strings.Contains(l, "SMUDGE=1") {
			t.Errorf("missing hardened env: %s", l)
		}
	}
	if !strings.Contains(lines[0], "clone --depth 1 --branch "+sha+" -- https://git.example.com/o/r.git") || !strings.Contains(string(data), "remote add -- origin https://git.example.com/o/r.git") {
		t.Errorf("clone args: %s", lines[0])
	}
	all := string(data)
	for _, want := range []string{"fetch --depth 1 origin " + sha, "checkout -q FETCH_HEAD"} {
		if !strings.Contains(all, want) {
			t.Errorf("sha fallback missing %q in:\n%s", want, all)
		}
	}
}

func TestCleanupTempDir(t *testing.T) {
	inside, err := os.MkdirTemp("", "skills-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := CleanupTempDir(inside); err != nil {
		t.Fatalf("inside temp: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Error("not removed")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/"
	}
	err = CleanupTempDir(home)
	if !IsDiagnostic(err, CodeCleanupOutsideTempDir) {
		t.Errorf("outside temp err = %v", err)
	}
	var d *DiagnosticError
	if !errors.As(err, &d) {
		t.Error("not a diagnostic")
	}
}

// --- Notion via fake ntn ---------------------------------------------------

// fakeNtn installs a shell-script ntn that answers from a response table
// keyed by the API path (the first non-"api" argument).
func fakeNtn(t *testing.T, responses map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake ntn")
	}
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\necho \"$*\" >> \"" + filepath.Join(dir, "calls") + "\"\ncase \"$2 $3 $4\" in\n")
	for key, body := range responses {
		f := filepath.Join(dir, "resp"+strconv.Itoa(len(sb.String())))
		writeFileT(t, f, body)
		sb.WriteString("  \"" + key + "\"*) cat \"" + f + "\" ;;\n")
	}
	sb.WriteString("  *) echo \"unexpected: $*\" >&2; exit 3 ;;\nesac\n")
	script := filepath.Join(dir, "ntn")
	writeFileT(t, script, sb.String())
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	old := fpNtnBinary
	fpNtnBinary = script
	t.Cleanup(func() { fpNtnBinary = old })
}

func zipBytesT(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRunNtnAPIErrors(t *testing.T) {
	old := fpNtnBinary
	defer func() { fpNtnBinary = old }()
	fpNtnBinary = filepath.Join(t.TempDir(), "missing-ntn")
	_, err := RunNtnAPI([]string{"api", "/v1/users/me"})
	if !IsDiagnostic(err, CodeNotionCLIMissing) || !strings.Contains(err.Error(), "ntn login") {
		t.Errorf("missing ntn err = %v", err)
	}
	fakeNtn(t, map[string]string{})
	_, err = RunNtnAPI([]string{"api", "/v1/nope", "x"})
	if !IsDiagnostic(err, CodeNotionAPIFailed) || !strings.Contains(err.Error(), "unexpected") {
		t.Errorf("failing ntn err = %v", err)
	}
}

func TestFetchNotionPacksPaginationAndCursorLoop(t *testing.T) {
	fakeNtn(t, map[string]string{
		"/v1/ai/plugins page_size==100 start_cursor==c2": `{"results":[{"id":"p2","name":"Two","version_id":"v2"}],"has_more":false,"next_cursor":null}`,
		"/v1/ai/plugins page_size==100 --notion-version": `{"results":[{"id":"p1","name":"One Pack","description":"d","version_id":"v1"},{"id":"px","name":"\u001b[31m","version_id":"vx"}],"has_more":true,"next_cursor":"c2"}`,
	})
	packs, err := FetchNotionPacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 || packs[0].ID != "p1" || packs[1].Name != "Two" {
		t.Fatalf("packs = %+v", packs)
	}
	if got := FilterNotionPacks(packs, []string{"one-pack"}); len(got) != 1 || got[0].ID != "p1" {
		t.Errorf("filter by slug: %+v", got)
	}
	if got := FilterNotionPacks(packs, []string{"P2"}); len(got) != 1 {
		t.Errorf("filter by id: %+v", got)
	}

	fakeNtn(t, map[string]string{
		"/v1/ai/plugins page_size==100 start_cursor==c1": `{"results":[],"has_more":true,"next_cursor":"c1"}`,
		"/v1/ai/plugins page_size==100 --notion-version": `{"results":[],"has_more":true,"next_cursor":"c1"}`,
	})
	if _, err := FetchNotionPacks(); !IsDiagnostic(err, CodeNotionInvalidCursor) {
		t.Errorf("cursor loop err = %v", err)
	}
	fakeNtn(t, map[string]string{"/v1/ai/plugins page_size==100 --notion-version": `not json`})
	if _, err := FetchNotionPacks(); !IsDiagnostic(err, CodeNotionInvalidJSON) {
		t.Errorf("invalid json err = %v", err)
	}
	fakeNtn(t, map[string]string{"/v1/ai/plugins page_size==100 --notion-version": `{"results":[],"next_cursor":null}`})
	if _, err := FetchNotionPacks(); !IsDiagnostic(err, CodeNotionResponseMissingField) {
		t.Errorf("missing has_more err = %v", err)
	}
}

func TestFetchSourceNotionPacksAndPage(t *testing.T) {
	skillZip := zipBytesT(t, map[string]string{
		"pack/alpha/SKILL.md": "---\nname: alpha\ndescription: A\n---\n",
		"pack/beta/SKILL.md":  "---\nname: beta\ndescription: B\n---\n",
	})
	pageZip := zipBytesT(t, map[string]string{"page/SKILL.md": "---\nname: paged\ndescription: P\n---\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pack.zip":
			_, _ = w.Write(skillZip)
		case "/page.zip":
			_, _ = w.Write(pageZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	fakeNtn(t, map[string]string{
		"/v1/ai/plugins page_size==100 --notion-version":                      `{"results":[{"id":"pk1","name":"Design Pack","version_id":"v1"}],"has_more":false,"next_cursor":null}`,
		"/v1/ai/plugins/pk1 --notion-version":                                 `{"id":"pk1","version_id":"v1","url":"` + srv.URL + `/pack.zip"}`,
		"/v1/ai/skills/01234567-89ab-cdef-0123-456789abcdef --notion-version": `{"id":"s","version_id":"v","url":"` + srv.URL + `/page.zip"}`,
	})

	f, err := FetchSource(SourceRequest{Source: "notion"})
	if err != nil {
		t.Fatalf("notion packs: %v", err)
	}
	defer f.Cleanup()
	if f.SourceType != SourceTypeDownload || f.Source != "notion" || len(f.Skills) != 2 {
		t.Fatalf("fetched = %+v", f)
	}
	for _, s := range f.Skills {
		if s.SourceURL != "notion://packs/pk1" || s.PluginName != "Design Pack" || !strings.HasSuffix(s.SkillPath, "/SKILL.md") {
			t.Errorf("skill = %+v", s)
		}
		if _, err := os.Stat(filepath.Join(s.Path, "SKILL.md")); err != nil {
			t.Errorf("not materialized: %v", err)
		}
	}
	if _, err := FetchSource(SourceRequest{Source: "notion", Skills: []string{"nope"}}); !IsDiagnostic(err, CodeNotionNoMatchingPacks) {
		t.Errorf("no matching packs err = %v", err)
	}

	page, err := FetchSource(SourceRequest{Source: "https://www.notion.so/acme/Paged-0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatalf("notion page: %v", err)
	}
	defer page.Cleanup()
	if page.Source != "https://www.notion.so/0123456789abcdef0123456789abcdef" || len(page.Skills) != 1 || page.Skills[0].Name != "paged" || page.Skills[0].SkillPath != "SKILL.md" {
		t.Errorf("page = %+v", page)
	}

	// Lock URLs re-download through ntn for update/restore.
	re, err := FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeDownload, URL: "notion://packs/pk1"}})
	if err != nil {
		t.Fatalf("lock re-download: %v", err)
	}
	defer re.Cleanup()
	if re.Source != "notion" || len(re.Skills) != 2 {
		t.Errorf("re-download = %+v", re)
	}
}

func TestCloneRepoDoesNotFetchFileTransportSubmodules(t *testing.T) {
	git := isolatedGit(t)
	sub := t.TempDir()
	git(sub, "init", "-q")
	writeFileT(t, filepath.Join(sub, "secret.txt"), "local file")
	git(sub, "add", "-A")
	git(sub, "commit", "-q", "-m", "sub")
	repo := t.TempDir()
	git(repo, "init", "-q")
	writeFileT(t, filepath.Join(repo, "SKILL.md"), "---\nname: x\ndescription: y\n---\n")
	git(repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", fileURL(sub), "vendor/sub")
	git(repo, "add", "-A")
	git(repo, "commit", "-q", "-m", "with submodule")

	dir, err := CloneRepo(fileURL(repo), "")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	defer CleanupTempDir(dir)
	if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err != nil {
		t.Fatalf(".gitmodules missing: %v", err)
	}
	// Untrusted .gitmodules must not pull local repositories via file://.
	if _, err := os.Stat(filepath.Join(dir, "vendor", "sub", "secret.txt")); err == nil {
		t.Error("file:// submodule was fetched")
	}
}

// --- Review #41 regressions -------------------------------------------------

// Review #41 item 2: temp-dir confinement must resolve parent symlinks and
// never remove the temp root itself.
func TestCleanupTempDirConfinement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR and symlinks behave differently on Windows")
	}
	base := t.TempDir()
	tmpRoot := filepath.Join(base, "tmp")
	outside := filepath.Join(base, "outside")
	writeFileT(t, filepath.Join(outside, "victim", "keep.txt"), "keep")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmpRoot)
	}
	if err := os.Symlink(outside, filepath.Join(tmpRoot, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CleanupTempDir(filepath.Join(tmpRoot, "link", "victim")); !IsDiagnostic(err, CodeCleanupOutsideTempDir) {
		t.Errorf("parent-symlink escape err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "victim", "keep.txt")); err != nil {
		t.Fatalf("victim deleted through symlink: %v", err)
	}
	if err := CleanupTempDir(tmpRoot); !IsDiagnostic(err, CodeCleanupOutsideTempDir) {
		t.Errorf("temp root err = %v", err)
	}
	if err := CleanupTempDir(tmpRoot + "/"); !IsDiagnostic(err, CodeCleanupOutsideTempDir) {
		t.Errorf("temp root with slash err = %v", err)
	}
	// A leaf symlink is removed as a link, never followed.
	if err := CleanupTempDir(filepath.Join(tmpRoot, "link")); err != nil {
		t.Errorf("leaf link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tmpRoot, "link")); !os.IsNotExist(err) {
		t.Error("leaf link not removed")
	}
	if _, err := os.Stat(filepath.Join(outside, "victim", "keep.txt")); err != nil {
		t.Fatalf("leaf link target deleted: %v", err)
	}
	if err := CleanupTempDir(filepath.Join(tmpRoot, "missing", "x")); err != nil {
		t.Errorf("missing dir should be a no-op: %v", err)
	}
	sib := tmpRoot + "-sibling"
	writeFileT(t, filepath.Join(sib, "f"), "x")
	if err := CleanupTempDir(sib); !IsDiagnostic(err, CodeCleanupOutsideTempDir) {
		t.Errorf("prefix sibling err = %v", err)
	}
}

// Review #41 item 8a: URLs and refs must never be parsed as git options.
func TestCloneRepoRejectsOptionLikeURLAndRef(t *testing.T) {
	old := fpGitBinary
	fpGitBinary = filepath.Join(t.TempDir(), "no-such-git")
	defer func() { fpGitBinary = old }()
	if _, err := CloneRepo("--upload-pack=touch /tmp/pwned", ""); !IsDiagnostic(err, CodeGitCloneFailed) || !strings.Contains(err.Error(), "invalid repository URL") {
		t.Errorf("option-like URL err = %v", err)
	}
	if _, err := CloneRepo("https://git.example.com/o/r.git", "--upload-pack=x"); !IsDiagnostic(err, CodeGitCloneFailed) || !strings.Contains(err.Error(), "invalid ref") {
		t.Errorf("option-like ref err = %v", err)
	}
}

// Review #41 item 8b: the gh fallback gets the hardened env and the same
// LFS/longpaths config as direct git clones.
func TestCloneRepoGhFallbackIsHardened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fakes")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "log")
	fakeGit := filepath.Join(tmp, "git")
	writeFileT(t, fakeGit, "#!/bin/sh\necho \"git $*\" >> \""+logFile+"\"\necho 'remote: Repository not found.' >&2\nexit 128\n")
	fakeGh := filepath.Join(tmp, "gh")
	writeFileT(t, fakeGh, `#!/bin/sh
if [ "$1" = "auth" ]; then echo "Git operations protocol: https"; exit 0; fi
echo "gh $* LC_ALL=$LC_ALL PROTO=$GIT_ALLOW_PROTOCOL PROMPT=$GIT_TERMINAL_PROMPT SMUDGE=$GIT_LFS_SKIP_SMUDGE" >> "`+logFile+`"
exit 1
`)
	for _, f := range []string{fakeGit, fakeGh} {
		if err := os.Chmod(f, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldGit, oldGh := fpGitBinary, fpGhBinary
	fpGitBinary, fpGhBinary = fakeGit, fakeGh
	defer func() { fpGitBinary, fpGhBinary = oldGit, oldGh }()
	t.Setenv("GH_HOST", "")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	_, err := CloneRepo("https://github.com/acme/private.git", "main")
	if !IsDiagnostic(err, CodeGitAuthFailed) || !strings.Contains(err.Error(), "git@github.com:acme/private.git") {
		t.Errorf("err = %v", err)
	}
	data, _ := os.ReadFile(logFile)
	var ghLine string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "gh ") {
			ghLine = l
		}
	}
	for _, want := range []string{"repo clone acme/private", "--depth=1", "--config core.longpaths=true", "--config filter.lfs.smudge=", "--branch main",
		"LC_ALL=C ", "PROTO=https:http:ssh:git:file", "PROMPT=0", "SMUDGE=1"} {
		if !strings.Contains(ghLine, want) {
			t.Errorf("gh call missing %q: %s", want, ghLine)
		}
	}
	if !strings.Contains(string(data), "git@github.com:acme/private.git") {
		t.Errorf("SSH retry not attempted:\n%s", data)
	}
}

// Review #41 item 8d: github:/gitlab: prefixes keep a pinned GitHubHost.
func TestParseSourcePrefixKeepsPinnedHost(t *testing.T) {
	t.Setenv("GH_HOST", "github.example.com")
	got, err := ParseSource("github:acme/skills@review", ParseSourceOptions{GitHubHost: "github.com"})
	if err != nil || got.Type != SourceTypeGitHub || got.URL != "https://github.com/acme/skills.git" || got.SkillFilter != "review" {
		t.Errorf("pinned github: prefix = %+v, %v", got, err)
	}
}

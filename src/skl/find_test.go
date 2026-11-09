package skl

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// lcSearchServer serves the given skills from /api/search and records the last URL.
func lcSearchServer(t *testing.T, skills []map[string]interface{}) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var last atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last.Store(r.URL.String())
		if r.URL.Path != "/api/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"skills": skills})
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func lcRunFind(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runFind(args, t.TempDir(), &out, &errb, strings.NewReader(""))
	return code, out.String(), errb.String()
}

func TestParseFindOptions(t *testing.T) {
	q, opts, errs := ParseFindOptions([]string{"react", "native", "--owner", "Vercel"})
	if q != "react native" || opts.Owner != "vercel" || len(errs) != 0 {
		t.Errorf("got %q %+v %v", q, opts, errs)
	}
	q, opts, errs = ParseFindOptions([]string{"--owner=vercel-labs", "next", "-l", "--json"})
	if q != "next" || opts.Owner != "vercel-labs" || !opts.Local || !opts.JSON || len(errs) != 0 {
		t.Errorf("got %q %+v %v", q, opts, errs)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"react", "--owner"}, "--owner requires a GitHub owner"},
		{[]string{"react", "--owner="}, "--owner requires a GitHub owner"},
		{[]string{"react", "--owner", "-bad"}, "--owner requires a GitHub owner"},
		{[]string{"react", "--owner", "not/an/owner"}, "--owner must be a valid GitHub owner"},
	} {
		_, _, errs := ParseFindOptions(tc.args)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.want) {
			t.Errorf("%v: errs=%v, want %q", tc.args, errs, tc.want)
		}
	}
}

func TestFindSkills_Local(t *testing.T) {
	lcHermetic(t)
	cwd := t.TempDir()
	lcWriteSkill(t, filepath.Join(cwd, ".agents", "skills", "test-find-skill"), "FindMe", "This is a highly searchable skill")
	for query, want := range map[string]int{"findme": 1, "searchable": 1, "notexist": 0} {
		res, err := FindSkills(FindOptions{Query: query, Cwd: cwd, Local: true})
		if err != nil || len(res) != want {
			t.Errorf("%q: got %v, %v; want %d", query, res, err, want)
		}
	}
}

func TestFindSkills_RemoteMock(t *testing.T) {
	lcHermetic(t)
	srv, _ := lcSearchServer(t, []map[string]interface{}{
		{"id": "go-expert-slug", "name": "Go Expert Skill", "installs": 1337, "source": "github.com/sandu/go-expert"},
	})
	orig := SearchAPIBase
	SearchAPIBase = srv.URL
	t.Cleanup(func() { SearchAPIBase = orig })

	res, err := FindSkills(FindOptions{Query: "go-expert", Cwd: t.TempDir()})
	if err != nil || len(res) != 1 {
		t.Fatalf("got %v, %v", res, err)
	}
	want := FoundSkill{Name: "Go Expert Skill", Slug: "go-expert-slug", Source: "github.com/sandu/go-expert", Scope: "remote", Installs: 1337}
	if !reflect.DeepEqual(res[0], want) {
		t.Errorf("got %+v, want %+v", res[0], want)
	}
}

// #2432: no limit parameter, owner forwarded, API relevance order preserved.
func TestSearchSkillsAPI_OrderAndParams(t *testing.T) {
	lcHermetic(t)
	srv, last := lcSearchServer(t, []map[string]interface{}{
		{"id": "owner/repo/eval-harness", "name": "eval-harness", "installs": 100, "source": "owner/repo"},
		{"id": "owner/repo/hyperframes-registry", "name": "hyperframes-registry", "installs": 732200, "source": "owner/repo"},
	})
	t.Setenv("SKILLS_API_URL", srv.URL+"/")
	res, _ := SearchSkillsAPI("react native", "vercel")
	if got := []string{res[0].Name, res[1].Name}; !reflect.DeepEqual(got, []string{"eval-harness", "hyperframes-registry"}) {
		t.Errorf("order not preserved: %v", got)
	}
	u := last.Load().(string)
	if !strings.Contains(u, "q=react+native") || !strings.Contains(u, "owner=vercel") || strings.Contains(u, "limit") {
		t.Errorf("unexpected request URL %q", u)
	}
}

func TestSearchSkillsAPI_FailuresReturnEmpty(t *testing.T) {
	lcHermetic(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SKILLS_API_URL", srv.URL)
	if res, err := SearchSkillsAPI("x", ""); err != nil || res == nil || len(res) != 0 {
		t.Errorf("got %v, %v", res, err)
	}
}

func TestRunFind_NonInteractiveOutput(t *testing.T) {
	lcHermetic(t)
	var skills []map[string]interface{}
	for i := 1; i <= 25; i++ {
		skills = append(skills, map[string]interface{}{
			"id": "owner/repo/skill-" + itoa(i), "name": "skill-" + itoa(i), "installs": i * 1000, "source": "owner/repo",
		})
	}
	srv, _ := lcSearchServer(t, skills)
	t.Setenv("SKILLS_API_URL", srv.URL)
	code, out, _ := lcRunFind(t, "owner/repo")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	plain := StripTerminalEscapes(out)
	for _, want := range []string{"owner/repo@skill-1 1K installs", "owner/repo@skill-25 25K installs", "└ https://skills.sh/owner/repo/skill-25", "Install with skl add <owner/repo@skill>"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in:\n%s", want, plain)
		}
	}
	if strings.Index(plain, "@skill-1 ") > strings.Index(plain, "@skill-25 ") {
		t.Error("results must be printed in API order")
	}

	code, out, _ = lcRunFind(t, "owner/repo", "--json")
	var parsed []FoundSkill
	if code != 0 || json.Unmarshal([]byte(out), &parsed) != nil || len(parsed) != 25 {
		t.Errorf("json: code=%d out=%q", code, out)
	}
}

func TestRunFind_NoResults(t *testing.T) {
	lcHermetic(t)
	srv, _ := lcSearchServer(t, nil)
	t.Setenv("SKILLS_API_URL", srv.URL)
	_, out, _ := lcRunFind(t, "nothing", "--owner", "acme")
	if !strings.Contains(out, `No skills found for "nothing" from owner "acme"`) {
		t.Errorf("unexpected %q", out)
	}
}

func TestRunFind_EmptyQueryNonInteractivePrintsTip(t *testing.T) {
	lcHermetic(t)
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = orig })
	code, out, _ := lcRunFind(t)
	plain := StripTerminalEscapes(out)
	if code != 0 || !strings.Contains(plain, "Tip: if running in a coding agent") ||
		!strings.Contains(plain, "1) skl find [query] [--owner <owner>]") || !strings.Contains(plain, "2) skl add <owner/repo@skill>") {
		t.Errorf("code=%d out=%q", code, plain)
	}
}

func TestRunFind_InvalidOwner(t *testing.T) {
	lcHermetic(t)
	code, _, errOut := lcRunFind(t, "react", "--owner", "not/valid")
	if code != 1 || !strings.Contains(errOut, "SKILLS_INVALID_OPTION_VALUE") || !strings.Contains(errOut, findUsage) {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestFormatInstalls(t *testing.T) {
	for n, want := range map[int]string{0: "", -1: "", 1: "1 install", 2: "2 installs", 1000: "1K installs", 1500: "1.5K installs", 732200: "732.2K installs", 2000000: "2M installs", 1250000: "1.3M installs"} {
		if got := formatInstalls(n); got != want {
			t.Errorf("formatInstalls(%d) = %q, want %q", n, got, want)
		}
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

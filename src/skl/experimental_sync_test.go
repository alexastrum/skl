package skl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func lcWritePkg(t *testing.T, dir string, pkg map[string]interface{}) {
	t.Helper()
	data, _ := json.Marshal(pkg)
	lcWriteFile(t, filepath.Join(dir, "package.json"), string(data))
}

func lcRunSync(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runSync(ParseSyncOptions(args), cwd, &out, &errb)
	return code, StripTerminalEscapes(out.String()), StripTerminalEscapes(errb.String())
}

// lcSyncProject creates a project depending on the given packages.
func lcSyncProject(t *testing.T, deps map[string]string, extra map[string]interface{}) string {
	t.Helper()
	cwd := t.TempDir()
	pkg := map[string]interface{}{"name": "app", "dependencies": deps}
	for k, v := range extra {
		pkg[k] = v
	}
	lcWritePkg(t, cwd, pkg)
	return cwd
}

func lcDiscoveredNames(d SyncDiscovery) []string {
	var names []string
	for _, s := range d.Skills {
		names = append(names, s.PackageName+":"+s.Name)
	}
	return names
}

func TestParseSyncOptions(t *testing.T) {
	got := ParseSyncOptions([]string{"-y", "--copy", "--dry-run", "--no-cleanup", "--no-remote", "--frozen-lockfile", "-r", "--json",
		"-a", "claude-code,cursor", "codex", "--include", "@acme/*", "pkg#x", "--exclude", "bad", "-f", "-v"})
	if !got.Yes || !got.Copy || !got.DryRun || !got.NoCleanup || !got.NoRemote || got.FrozenLockfile == nil || !*got.FrozenLockfile ||
		!got.Recursive || !got.JSON || !got.Force || !got.Verbose {
		t.Errorf("flags not parsed: %+v", got)
	}
	if !reflect.DeepEqual(got.Agent, []AgentType{"claude-code", "cursor", "codex"}) ||
		!reflect.DeepEqual(got.Include, []string{"@acme/*", "pkg#x"}) || !reflect.DeepEqual(got.Exclude, []string{"bad"}) {
		t.Errorf("lists not parsed: %+v", got)
	}
	if o := ParseSyncOptions([]string{"--no-frozen-lockfile"}); o.FrozenLockfile == nil || *o.FrozenLockfile {
		t.Error("--no-frozen-lockfile must set false")
	}
}

func TestParseSkillsField(t *testing.T) {
	entries := []interface{}{
		"npm:@vueuse/skills",
		map[string]interface{}{"source": "npm:pkg", "skills": []interface{}{"a"}},
		"owner/repo@one",
		map[string]interface{}{"source": "owner/repo", "ref": "v1", "skills": []interface{}{"x"}},
		"./local",
		"https://example.com/skills",
		map[string]interface{}{"source": "owner/repo#main", "ref": "v2"},
		map[string]interface{}{"source": "npm:pkg", "ref": "v1"},
		float64(1),
		map[string]interface{}{"skills": []interface{}{"a"}},
		map[string]interface{}{"source": "owner/repo", "skills": []interface{}{float64(1)}},
		map[string]interface{}{"source": "owner/repo", "ref": float64(1)},
	}
	got := ParseSkillsField(entries, ".")
	if !reflect.DeepEqual(got.Npm, []NpmSkillsRequest{{Package: "@vueuse/skills"}, {Package: "pkg", Skills: []string{"a"}}}) {
		t.Errorf("npm = %+v", got.Npm)
	}
	if len(got.Remote) != 2 || !reflect.DeepEqual(got.Remote[0].Skills, []string{"one"}) ||
		got.Remote[1].Parsed.Ref != "v1" || !reflect.DeepEqual(got.Remote[1].Skills, []string{"x"}) {
		t.Errorf("remote = %+v", got.Remote)
	}
	var codes []string
	for _, e := range got.Errors {
		codes = append(codes, DiagnosticCode(e))
	}
	want := []string{
		"SKILLS_PACKAGE_SKILLS_NOT_GIT_SOURCE", "SKILLS_PACKAGE_SKILLS_NOT_GIT_SOURCE",
		"SKILLS_PACKAGE_SKILLS_DUPLICATE_REF", "SKILLS_PACKAGE_SKILLS_REF_NOT_ALLOWED",
		"SKILLS_PACKAGE_SKILLS_INVALID_ENTRY", "SKILLS_PACKAGE_SKILLS_INVALID_ENTRY",
		"SKILLS_PACKAGE_SKILLS_INVALID_ENTRY", "SKILLS_PACKAGE_SKILLS_INVALID_ENTRY",
	}
	if !reflect.DeepEqual(codes, want) {
		t.Errorf("error codes = %v, want %v", codes, want)
	}
}

func TestDiscoverNodeModuleSkills_DirectDependencies(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"plain": "1"}, map[string]interface{}{
		"devDependencies":      map[string]string{"@acme/tools": "1"},
		"optionalDependencies": map[string]string{"missing-optional": "1"},
	})
	nm := filepath.Join(cwd, "node_modules")
	lcWritePkg(t, filepath.Join(nm, "plain"), map[string]interface{}{"name": "plain", "version": "1.2.3"})
	lcWriteSkill(t, filepath.Join(nm, "plain"), "root-skill", "d")
	lcWriteSkill(t, filepath.Join(nm, "plain", "skills", "ignored-under-root"), "ignored", "d")
	scoped := filepath.Join(nm, "@acme", "tools")
	lcWritePkg(t, scoped, map[string]interface{}{"name": "@acme/tools"})
	lcWriteSkill(t, filepath.Join(scoped, "skills", "a"), "a", "d")
	lcWriteSkill(t, filepath.Join(scoped, "dist", "skills", "b"), "b", "d")
	lcWriteSkill(t, filepath.Join(scoped, "examples", "c"), "c", "d")
	lcWriteSkill(t, filepath.Join(scoped, ".agents", "skills", "e"), "e", "d")
	// Installed but not declared: never scanned.
	lcWritePkg(t, filepath.Join(nm, "transitive"), map[string]interface{}{"name": "transitive"})
	lcWriteSkill(t, filepath.Join(nm, "transitive"), "transitive-skill", "d")

	d := DiscoverNodeModuleSkills(cwd, false)
	if got, want := lcDiscoveredNames(d), []string{"@acme/tools:a", "@acme/tools:b", "plain:root-skill"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, s := range d.Skills {
		if s.Name == "root-skill" && (s.SkillPath != "SKILL.md" || s.PackageVersion != "1.2.3") {
			t.Errorf("root skill metadata %+v", s)
		}
		if s.Name == "b" && s.SkillPath != "dist/skills/b/SKILL.md" {
			t.Errorf("dist skill path %q", s.SkillPath)
		}
	}
	if len(DiscoverNodeModuleSkills(t.TempDir(), false).Skills) != 0 {
		t.Error("no package.json means no skills")
	}
}

func TestDiscoverNodeModuleSkills_PnpmSymlinkLayout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"foo": "1"}, nil)
	store := filepath.Join(cwd, "node_modules", ".pnpm", "foo@1.0.0", "node_modules", "foo")
	lcWritePkg(t, store, map[string]interface{}{"name": "foo", "version": "1.0.0"})
	lcWriteSkill(t, filepath.Join(store, "skills", "pnpm-skill"), "pnpm-skill", "d")
	if err := os.Symlink(store, filepath.Join(cwd, "node_modules", "foo")); err != nil {
		t.Fatal(err)
	}
	if got := lcDiscoveredNames(DiscoverNodeModuleSkills(cwd, false)); !reflect.DeepEqual(got, []string{"foo:pnpm-skill"}) {
		t.Errorf("got %v", got)
	}
}

func TestDiscoverNodeModuleSkills_RecursiveWorkspaces(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, nil, map[string]interface{}{"workspaces": []string{"packages/*", "!packages/ignored"}})
	app := filepath.Join(cwd, "packages", "app")
	lcWritePkg(t, app, map[string]interface{}{"name": "@me/app", "dependencies": map[string]string{"bar": "1"}})
	lcWritePkg(t, filepath.Join(cwd, "packages", "ignored"), map[string]interface{}{"name": "ignored", "dependencies": map[string]string{"baz": "1"}})
	lcWritePkg(t, filepath.Join(cwd, "node_modules", "bar"), map[string]interface{}{"name": "bar"})
	lcWriteSkill(t, filepath.Join(cwd, "node_modules", "bar"), "bar-skill", "d")
	lcWritePkg(t, filepath.Join(cwd, "node_modules", "baz"), map[string]interface{}{"name": "baz"})
	lcWriteSkill(t, filepath.Join(cwd, "node_modules", "baz"), "baz-skill", "d")

	if got := DiscoverNodeModuleSkills(cwd, false).Skills; len(got) != 0 {
		t.Errorf("without -r workspace deps are not scanned: %v", got)
	}
	d := DiscoverNodeModuleSkills(cwd, true)
	if got := lcDiscoveredNames(d); !reflect.DeepEqual(got, []string{"bar:bar-skill"}) {
		t.Errorf("got %v", got)
	}
	if d.Skills[0].Depth != 1 {
		t.Errorf("workspace dependency depth = %d, want 1", d.Skills[0].Depth)
	}
}

func TestDiscoverNodeModuleSkills_SkillsFieldNpm(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"a": "1"}, map[string]interface{}{"skills": []interface{}{"npm:hidden"}})
	nm := filepath.Join(cwd, "node_modules")
	lcWritePkg(t, filepath.Join(nm, "a"), map[string]interface{}{"name": "a", "skills": []interface{}{"npm:deep", float64(3)}})
	lcWritePkg(t, filepath.Join(nm, "hidden"), map[string]interface{}{"name": "hidden"})
	lcWriteSkill(t, filepath.Join(nm, "hidden", "skills", "h"), "h", "d")
	lcWritePkg(t, filepath.Join(nm, "deep"), map[string]interface{}{"name": "deep"})
	lcWriteSkill(t, filepath.Join(nm, "deep", "skills", "x"), "x", "d")
	d := DiscoverNodeModuleSkills(cwd, false)
	depth := map[string]int{}
	via := map[string]string{}
	for _, s := range d.Skills {
		depth[s.Name], via[s.Name] = s.Depth, s.Via
	}
	if depth["h"] != 0 || via["h"] != "." || depth["x"] != 1 || via["x"] != "a" {
		t.Errorf("depth=%v via=%v", depth, via)
	}
	if len(d.Errors) != 0 || len(d.Warnings) != 1 {
		t.Errorf("a dependency's invalid entry is a warning: errors=%v warnings=%v", d.Errors, d.Warnings)
	}
}

func TestSyncSkills(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"mock-skill-pkg": "1"}, nil)
	pkgDir := filepath.Join(cwd, "node_modules", "mock-skill-pkg")
	lcWritePkg(t, pkgDir, map[string]interface{}{"name": "mock-skill-pkg", "version": "2.0.0"})
	lcWriteSkill(t, pkgDir, "Mock Npm Skill", "An npm packaged skill")

	if err := SyncSkills(nil, SyncOptions{Yes: true, FrozenLockfile: new(bool)}, cwd); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(cwd, ".agents", "skills", "mock-npm-skill")
	if !lcExists(filepath.Join(canonical, "SKILL.md")) {
		t.Fatalf("expected skill at %s", canonical)
	}
	if fi, _ := os.Lstat(canonical); runtime.GOOS != "windows" && fi.Mode()&os.ModeSymlink == 0 {
		t.Error("default sync mode links the canonical dir into node_modules")
	}
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lock.Skills["Mock Npm Skill"]
	if !ok {
		t.Fatalf("lock entries %v", lock.Skills)
	}
	if e.Source != "mock-skill-pkg" || e.SourceType != "node_modules" || e.SkillPath != "SKILL.md" || e.Version != "2.0.0" || len(e.ComputedHash) != 64 {
		t.Errorf("unexpected lock entry %+v", e)
	}
}

func TestRunSync_ConflictsAndShadowing(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"a": "1", "b": "1", "c": "1"}, nil)
	nm := filepath.Join(cwd, "node_modules")
	for _, p := range []string{"a", "b"} {
		lcWritePkg(t, filepath.Join(nm, p), map[string]interface{}{"name": p})
		lcWriteSkill(t, filepath.Join(nm, p, "skills", "dup"), "dup", "d")
	}
	lcWritePkg(t, filepath.Join(nm, "c"), map[string]interface{}{"name": "c"})
	lcWriteSkill(t, filepath.Join(nm, "c", "skills", "added"), "added", "d")
	lcWriteLocalLock(t, cwd, map[string]LocalSkillLockEntry{"added": {Source: "owner/repo", SourceType: "github", ComputedHash: "h"}})

	code, _, errOut := lcRunSync(t, cwd, "-y", "--no-frozen-lockfile")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "shipped by a, b; drop this one with --exclude a#dup") {
		t.Errorf("missing same-depth conflict warning: %s", errOut)
	}
	if !strings.Contains(errOut, "installed with `skl add` from owner/repo") {
		t.Errorf("missing shadowing warning: %s", errOut)
	}
	if lcExists(filepath.Join(cwd, ".agents", "skills", "dup")) {
		t.Error("conflicting skill must not be installed")
	}

	// --exclude resolves the conflict.
	code, _, errOut = lcRunSync(t, cwd, "-y", "--no-frozen-lockfile", "--exclude", "a#dup")
	if code != 0 || !lcExists(filepath.Join(cwd, ".agents", "skills", "dup")) {
		t.Errorf("exclude should let b#dup install: code=%d %s", code, errOut)
	}
	if lock, _ := ReadLocalLock(cwd); lock.Skills["dup"].Source != "b" || lock.Skills["added"].SourceType != "github" {
		t.Errorf("lock %v", lock.Skills)
	}
}

func TestRunSync_IncludeFilter(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"@acme/x": "1", "other": "1"}, nil)
	nm := filepath.Join(cwd, "node_modules")
	lcWritePkg(t, filepath.Join(nm, "@acme", "x"), map[string]interface{}{"name": "@acme/x"})
	lcWriteSkill(t, filepath.Join(nm, "@acme", "x", "skills", "keep"), "keep", "d")
	lcWriteSkill(t, filepath.Join(nm, "@acme", "x", "skills", "drop"), "drop", "d")
	lcWritePkg(t, filepath.Join(nm, "other"), map[string]interface{}{"name": "other"})
	lcWriteSkill(t, filepath.Join(nm, "other"), "other-skill", "d")
	if code, _, errOut := lcRunSync(t, cwd, "-y", "--no-frozen-lockfile", "--include", "@acme/*#k*"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "keep")) || lcExists(filepath.Join(cwd, ".agents", "skills", "drop")) || lcExists(filepath.Join(cwd, ".agents", "skills", "other-skill")) {
		t.Error("include globs not applied")
	}
}

func TestRunSync_PrunesStaleSkills(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"p": "1"}, nil)
	pkgDir := filepath.Join(cwd, "node_modules", "p")
	lcWritePkg(t, pkgDir, map[string]interface{}{"name": "p"})
	lcWriteSkill(t, pkgDir, "gone-soon", "d")
	if code, _, errOut := lcRunSync(t, cwd, "-y", "--no-frozen-lockfile"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	lcWritePkg(t, cwd, map[string]interface{}{"name": "app"})

	_, out, _ := lcRunSync(t, cwd, "-y", "--no-frozen-lockfile", "--no-cleanup")
	if !lcExists(filepath.Join(cwd, ".agents", "skills", "gone-soon")) || strings.Contains(out, "Removed") {
		t.Fatal("--no-cleanup must keep stale skills")
	}
	code, _, errOut := lcRunSync(t, cwd, "-y", "--frozen-lockfile")
	if code != 1 || !strings.Contains(errOut, "gone-soon is no longer provided by a dependency") {
		t.Fatalf("frozen prune: code=%d %s", code, errOut)
	}
	_, out, _ = lcRunSync(t, cwd, "-y", "--no-frozen-lockfile", "--dry-run")
	if !strings.Contains(out, "Would remove gone-soon") || !lcExists(filepath.Join(cwd, ".agents", "skills", "gone-soon")) {
		t.Fatalf("dry run: %s", out)
	}
	_, out, _ = lcRunSync(t, cwd, "-y", "--no-frozen-lockfile")
	if !strings.Contains(out, "Removed gone-soon") || lcExists(filepath.Join(cwd, ".agents", "skills", "gone-soon")) {
		t.Fatalf("prune: %s", out)
	}
	if lock, _ := ReadLocalLock(cwd); len(lock.Skills) != 0 {
		t.Errorf("lock not pruned: %v", lock.Skills)
	}
}

// #2559: sync under --frozen-lockfile (default in CI) refuses lock drift and
// leaves disk and skills-lock.json untouched.
func TestRunSync_FrozenLockfile(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"p": "1"}, nil)
	pkgDir := filepath.Join(cwd, "node_modules", "p")
	lcWritePkg(t, pkgDir, map[string]interface{}{"name": "p", "version": "1.0.0"})
	lcWriteSkill(t, pkgDir, "s", "d")

	t.Setenv("CI", "true")
	code, _, errOut := lcRunSync(t, cwd, "-y")
	if code != 1 || !strings.Contains(errOut, "s is not in skills-lock.json") || !strings.Contains(errOut, "--no-frozen-lockfile") {
		t.Fatalf("code=%d %s", code, errOut)
	}
	if lcExists(filepath.Join(cwd, ".agents", "skills", "s")) || lcExists(filepath.Join(cwd, "skills-lock.json")) {
		t.Fatal("frozen sync must not write")
	}
	if code, _, errOut := lcRunSync(t, cwd, "-y", "--no-frozen-lockfile"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	lockBytes := lcReadFile(t, filepath.Join(cwd, "skills-lock.json"))
	if code, _, errOut := lcRunSync(t, cwd, "-y"); code != 0 {
		t.Fatalf("frozen sync with matching lock: exit %d: %s", code, errOut)
	}
	if lcReadFile(t, filepath.Join(cwd, "skills-lock.json")) != lockBytes {
		t.Error("frozen sync must not rewrite skills-lock.json")
	}
}

func TestRunSync_InvalidRootSkillsField(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, nil, map[string]interface{}{"skills": "not-an-array"})
	code, _, errOut := lcRunSync(t, cwd, "-y")
	if code != 1 || !strings.Contains(errOut, "SKILLS_PACKAGE_SKILLS_NOT_ARRAY") {
		t.Errorf("code=%d %s", code, errOut)
	}
}

func TestRunSync_JSON(t *testing.T) {
	lcHermetic(t)
	cwd := lcSyncProject(t, map[string]string{"p": "1"}, nil)
	pkgDir := filepath.Join(cwd, "node_modules", "p")
	lcWritePkg(t, pkgDir, map[string]interface{}{"name": "p"})
	lcWriteSkill(t, pkgDir, "s", "d")
	var out, errb bytes.Buffer
	code := runSync(ParseSyncOptions([]string{"--json", "--no-frozen-lockfile"}), cwd, &out, &errb)
	var res SyncJSONResult
	if code != 0 || json.Unmarshal(out.Bytes(), &res) != nil {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	if len(res.Synced) != 1 || res.Synced[0].Name != "s" || res.Synced[0].Package != "p" {
		t.Errorf("synced = %+v", res.Synced)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Error("JSON output must not contain ANSI escapes")
	}
}

func TestGlobMatching(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		want          bool
	}{
		{"@acme/tools", "@acme/*", true},
		{"@acme/tools", "*", false},
		{"@acme/tools", "**", true},
		{"pkg", "p?g", true},
		{"packages/a/package.json", "packages/*/package.json", true},
		{"packages/a/b/package.json", "packages/**/package.json", true},
		{"packages/package.json", "packages/**/package.json", true},
	} {
		if got := matchesGlob(tc.name, tc.pattern); got != tc.want {
			t.Errorf("matchesGlob(%q, %q) = %v", tc.name, tc.pattern, got)
		}
	}
}

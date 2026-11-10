package skl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// restoreFetch materializes a lock entry's source during restore (overridable in tests).
var restoreFetch = FetchSource

// ResolveRestoreAgents picks the agents a restore installs to without prompting:
// --agent when given ('*' = all), else the last selection saved by `add`, else
// the detected agents. Universal agents are always included (install.ts:23).
func ResolveRestoreAgents(requested []AgentType, cwd string) ([]AgentType, error) {
	valid := sortedAgentKeys()
	for _, a := range requested {
		if a == "*" {
			all := make([]AgentType, 0, len(valid))
			for _, k := range valid {
				if AgentType(k) != AgentUniversal {
					all = append(all, AgentType(k))
				}
			}
			return all, nil
		}
	}
	var chosen []AgentType
	if len(requested) > 0 {
		if invalid := invalidAgentNames(requested); len(invalid) > 0 {
			return nil, ErrInvalidAgents(invalid, valid)
		}
		chosen = requested
	} else {
		last, _ := GetLastSelectedAgents()
		for _, a := range last {
			if _, ok := Agents[AgentType(a)]; ok {
				chosen = append(chosen, AgentType(a))
			}
		}
		if len(chosen) == 0 {
			chosen = DetectInstalledAgents(cwd)
		}
	}
	seen := map[AgentType]bool{}
	var out []AgentType
	for _, a := range append(append([]AgentType{}, chosen...), GetUniversalAgents()...) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// LinkInstalledSkills gives already-installed skills the agent links they lack: a
// symlink to the canonical copy, or a copy for `installMode: "copy"` entries.
// Agent dirs that already hold the skill are left alone (#2681).
func LinkInstalledSkills(names []string, lock LocalSkillLockFile, agents []AgentType, forceCopy bool, cwd string, stderr io.Writer) bool {
	ok := true
	for _, name := range names {
		canonical := GetCanonicalPath(name, false, cwd)
		mode := InstallModeSymlink
		if forceCopy || lock.Skills[name].InstallMode == string(InstallModeCopy) {
			mode = InstallModeCopy
		}
		for _, a := range agents {
			if _, err := os.Lstat(GetInstallPath(name, a, false, cwd)); err == nil {
				continue
			}
			res := InstallSkillForAgent(Skill{Name: name, Path: canonical}, a, InstallOptions{Cwd: cwd, Mode: mode, CreateMissingAgentRoot: true})
			if !res.Success && !res.Skipped {
				fmt.Fprintf(stderr, "Failed to link %s to %s: %s\n", name, Agents[a].DisplayName, res.Error)
				ok = false
			}
		}
	}
	return ok
}

// restoreInstall installs the named skills of a fetched source at project scope
// via InstallFromSource (records skills-lock.json, or with frozen verifies the
// hashes against it and never writes the lock).
func restoreInstall(f *FetchedSource, skills []string, agents []AgentType, frozen *LocalSkillLockFile, mode InstallMode, via, cwd string) (installed, failed []string, errMsg string) {
	installMode := InstallMode("")
	if mode == InstallModeCopy {
		installMode = InstallModeCopy
	}
	res := InstallFromSource(f, InstallFromSourceOptions{Skills: skills, Agents: agents, Via: via, FrozenLock: frozen, InstallMode: installMode, Cwd: cwd})
	return res.Installed, res.Failed, res.Error
}

type restoreGroup struct {
	source   string
	copy     bool
	download bool
	skills   []string
}

// RunInstallFromLock restores project skills from skills-lock.json (`skl install`
// with no source, `skl experimental_install`). Skills already in .agents/skills
// only get missing agent links (#2603, #2681); --dry-run prints the plan
// (#2608); a failing source only fails its own entries (#2680); and
// --frozen-lockfile (default in CI) never rewrites the lock (#2559).
func RunInstallFromLock(args []string, cwd string) int {
	return runInstallFromLock(args, cwd, os.Stdout, os.Stderr)
}

func runInstallFromLock(args []string, cwd string, stdout, stderr io.Writer) int {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	opts := ParseSyncOptions(args)
	frozen := IsCI()
	if opts.FrozenLockfile != nil {
		frozen = *opts.FrozenLockfile
	}
	force := opts.Force

	lock, err := ReadLocalLock(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %s\n", FormatError(err))
		return 1
	}
	if len(lock.Skills) == 0 {
		if frozen {
			fmt.Fprintln(stderr, "Cannot install with --frozen-lockfile because skills-lock.json is absent or has no skills")
			return 1
		}
		fmt.Fprintf(stdout, "%sNo project skills found in skills-lock.json%s\n", ansiYellow, ansiReset)
		fmt.Fprintf(stdout, "Add project-level skills with %sskl add <package>%s (without %s-g%s)\n", ansiCyan, ansiReset, ansiCyan, ansiReset)
		return 0
	}

	targets, err := ResolveRestoreAgents(opts.Agent, cwd)
	if err != nil {
		fmt.Fprintln(stderr, FormatError(err))
		return 1
	}
	var linked []AgentType
	for _, a := range targets {
		if !IsUniversalAgent(a) {
			linked = append(linked, a)
		}
	}

	exit := 0
	var nodeModules, already, failedSkills []string
	var groupKeys []string
	groups := map[string]*restoreGroup{}
	for _, name := range keysOf(lock.Skills) {
		e := lock.Skills[name]
		if e.SourceType == "node_modules" || e.SourceType == "npm" {
			nodeModules = append(nodeModules, name)
			continue
		}
		if !force {
			if _, err := os.Stat(filepath.Join(GetCanonicalPath(name, false, cwd), "SKILL.md")); err == nil {
				already = append(already, name)
				continue
			}
		}
		src := BuildLocalUpdateSource(UpdateSourceFromLocal(e))
		if e.SourceType == "download" {
			src = e.SourceUrl
		}
		if src == "" {
			fmt.Fprintln(stderr, FormatError(ErrLockMissingSourceURL(name)))
			failedSkills = append(failedSkills, name)
			continue
		}
		copyMode := opts.Copy || e.InstallMode == string(InstallModeCopy)
		download := e.SourceType == "download"
		key := fmt.Sprintf("%v:%v:%s", copyMode, download, src)
		g, ok := groups[key]
		if !ok {
			g = &restoreGroup{source: src, copy: copyMode, download: download}
			groups[key] = g
			groupKeys = append(groupKeys, key)
		}
		g.skills = append(g.skills, name)
	}

	if len(already) > 0 {
		fmt.Fprintf(stdout, "%s%d%s skill%s already installed %s(use --force to reinstall)%s\n", ansiCyan, len(already), ansiReset, plural(len(already)), ansiDim, ansiReset)
	}
	verb, linkVerb := "Restoring", "Linking"
	if opts.DryRun {
		verb, linkVerb = "Would restore", "Would link"
	}
	remote := len(lock.Skills) - len(nodeModules) - len(already)
	if remote > 0 {
		fmt.Fprintf(stdout, "%s %s%d%s skill%s from skills-lock.json into %s.agents/skills/%s\n", verb, ansiCyan, remote, ansiReset, plural(remote), ansiDim, ansiReset)
	}
	if len(linked) > 0 && (remote > 0 || len(already) > 0) {
		fmt.Fprintf(stdout, "%s to %s\n", linkVerb, strings.Join(agentDisplayNames(linked), ", "))
	}
	if opts.DryRun {
		for _, k := range groupKeys {
			g := groups[k]
			suffix := ""
			if g.copy {
				suffix = " (copy)"
			}
			fmt.Fprintf(stdout, "%s %sfrom %s%s%s\n", strings.Join(g.skills, ", "), ansiDim, g.source, suffix, ansiReset)
		}
		groupKeys = nil
	} else if !LinkInstalledSkills(already, lock, linked, opts.Copy, cwd, stderr) {
		exit = 1
	}

	var frozenLock *LocalSkillLockFile
	if frozen {
		frozenLock = &lock
	}
	restored := 0
	for _, k := range groupKeys {
		g := groups[k]
		req := SourceRequest{Source: g.source, Skills: g.skills, Yes: true, Cwd: cwd}
		if g.download {
			req.Parsed = &ParsedSource{Type: "download", URL: g.source}
			req.FullDepth = true
		}
		fetched, ferr := restoreFetch(req)
		if ferr != nil || fetched == nil {
			if ferr == nil {
				ferr = errors.New("no skills found")
			}
			fmt.Fprintf(stderr, "Failed to restore %s from %s: %s\n", strings.Join(g.skills, ", "), g.source, FormatError(ferr))
			failedSkills = append(failedSkills, g.skills...)
			continue
		}
		mode := InstallModeSymlink
		if g.copy {
			mode = InstallModeCopy
		}
		installed, failedTargets, errMsg := restoreInstall(fetched, g.skills, targets, frozenLock, mode, "", cwd)
		_ = fetched.Cleanup()
		if errMsg != "" {
			fmt.Fprintf(stderr, "Failed to restore %s from %s: %s\n", strings.Join(g.skills, ", "), g.source, errMsg)
			failedSkills = append(failedSkills, g.skills...)
			continue
		}
		restored += len(installed)
		if len(installed) > 0 {
			fmt.Fprintf(stdout, "%s✓%s Restored %s\n", ansiGreen, ansiReset, strings.Join(installed, ", "))
		}
		got := map[string]bool{}
		for _, n := range installed {
			got[strings.ToLower(n)] = true
		}
		var notFound []string
		for _, n := range g.skills {
			if !got[strings.ToLower(n)] {
				notFound = append(notFound, n)
			}
		}
		if len(notFound) > 0 {
			fmt.Fprintf(stderr, "%s not found in %s\n", strings.Join(notFound, ", "), g.source)
			failedSkills = append(failedSkills, notFound...)
		}
		for _, f := range failedTargets {
			fmt.Fprintln(stderr, f)
		}
		if len(failedTargets) > 0 {
			exit = 1
		}
	}

	if len(nodeModules) > 0 {
		fmt.Fprintf(stdout, "%s%d%s skill%s from node_modules\n", ansiCyan, len(nodeModules), ansiReset, plural(len(nodeModules)))
		syncOpts := opts
		syncOpts.FrozenLockfile = &frozen
		syncOpts.Yes = true
		syncOpts.Agent = targets
		if code := runSync(syncOpts, cwd, stdout, stderr); code != 0 {
			fmt.Fprintln(stderr, "Failed to sync node_modules skills")
			failedSkills = append(failedSkills, nodeModules...)
		}
	}

	if len(failedSkills) > 0 {
		prefix := "Failed"
		if restored > 0 {
			prefix = fmt.Sprintf("Restored %d skill%s; failed", restored, plural(restored))
		}
		fmt.Fprintf(stderr, "%s to restore %d skill%s from skills-lock.json: %s\n", prefix, len(failedSkills), plural(len(failedSkills)), strings.Join(failedSkills, ", "))
		return 1
	}
	return exit
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// RestoreFromLock is the library form of RunInstallFromLock.
func RestoreFromLock(args []string, cwd string) error {
	var errb strings.Builder
	if code := runInstallFromLock(args, cwd, io.Discard, &errb); code != 0 {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = "restore failed"
		}
		return errors.New(msg)
	}
	return nil
}

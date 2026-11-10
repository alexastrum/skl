package skl

// installer.go ports upstream src/installer.ts: confined skill copies, canonical
// directory + relative symlink installs, copy installs, and install path helpers.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkipReasonMissingAgentProjectDirectory is reported when a project install skips an
// agent-specific symlink because the agent's project root (e.g. .windsurf/) does not exist.
const SkipReasonMissingAgentProjectDirectory = "missing-agent-project-directory"

// InstallOptions controls where and how a skill is materialized for one agent.
type InstallOptions struct {
	Global bool
	Cwd    string
	// Mode defaults to InstallModeSymlink when empty.
	Mode InstallMode
	// EveSubagent targets agent/subagents/<name>/skills for the eve agent.
	EveSubagent string
	// CreateMissingAgentRoot creates a missing agent-specific project root because the
	// user explicitly selected this agent.
	CreateMissingAgentRoot bool
	Verbose                bool
}

// InstallResult reports the outcome of installing one skill for one agent.
type InstallResult struct {
	Success       bool
	Path          string
	CanonicalPath string
	Mode          InstallMode
	SymlinkFailed bool
	Skipped       bool
	SkipReason    string
	Error         string
}

// InstallFile is one file of a skill that is installed from memory (blob snapshot,
// well-known index, or archive entries) rather than copied from a directory on disk.
type InstallFile struct {
	Path     string
	Contents []byte
}

// InstallTarget is one installation destination: an agent, optionally scoped to an Eve subagent.
type InstallTarget struct {
	Agent       AgentType
	EveSubagent string
}

// AgentInstallResult pairs an install target with its result.
type AgentInstallResult struct {
	Agent       AgentType
	EveSubagent string
	Result      InstallResult
}

var (
	excludedCopyFiles = map[string]bool{"metadata.json": true}
	excludedCopyDirs  = map[string]bool{".git": true, "__pycache__": true, "__pypackages__": true}
)

func isExcludedEntry(name string, isDir bool) bool {
	if excludedCopyFiles[name] {
		return true
	}
	return isDir && excludedCopyDirs[name]
}

// isExcludedInstallPath applies isExcludedEntry to every segment of a relative path,
// so in-memory installs drop the same files CopySkillDirectory skips.
func isExcludedInstallPath(rel string) bool {
	var parts []string
	for _, p := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for i, p := range parts {
		if isExcludedEntry(p, i < len(parts)-1) {
			return true
		}
	}
	return false
}

func installAbsClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// isPathSafe reports whether targetPath equals basePath or lives inside it.
func isPathSafe(basePath, targetPath string) bool {
	base := installAbsClean(basePath)
	target := installAbsClean(targetPath)
	if target == base {
		return true
	}
	return strings.HasPrefix(target, strings.TrimSuffix(base, string(filepath.Separator))+string(filepath.Separator))
}

func pathsOverlap(a, b string) bool {
	return isPathSafe(a, b) || isPathSafe(b, a)
}

func shouldSkipProjectAgentSymlink(agentType AgentType, isGlobal bool, cwd string, createMissingAgentRoot bool) bool {
	agent, ok := Agents[agentType]
	if !ok {
		return false
	}
	if isGlobal || IsUniversalAgent(agentType) || createMissingAgentRoot || agent.CreateProjectSkillsDirByDefault {
		return false
	}
	agentRoot := strings.Split(filepath.ToSlash(agent.SkillsDir), "/")[0]
	_, err := os.Stat(filepath.Join(cwd, agentRoot))
	return err != nil
}

func installCwd(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return cwd
}

// GetAgentBaseDir returns the directory an agent reads skills from. Universal agents use
// the canonical .agents/skills directory for project installs; for global installs only
// agents that read ~/.agents/skills (GlobalReadsCanonical) use it, so Antigravity resolves
// to ~/.gemini/config/skills (#2566).
func GetAgentBaseDir(agentType AgentType, global bool, cwd string, eveSubagent string) string {
	// skl's "universal" pseudo-agent always means the canonical directory.
	if agentType == AgentUniversal {
		return GetCanonicalSkillsDir(global, cwd)
	}
	if global {
		if ReadsCanonicalGlobally(agentType) {
			return GetCanonicalSkillsDir(true, cwd)
		}
	} else if IsUniversalAgent(agentType) {
		return GetCanonicalSkillsDir(false, cwd)
	}

	// Eve subagents are project scoped; the global flag does not apply.
	if agentType == "eve" && eveSubagent != "" {
		return GetEveSubagentSkillsDir(eveSubagent, cwd)
	}

	agent, ok := Agents[agentType]
	if !ok {
		return ""
	}
	if global {
		if agent.GlobalSkillsDir == "" {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, agent.SkillsDir)
		}
		return ResolvePath(agent.GlobalSkillsDir, cwd)
	}
	return filepath.Join(installCwd(cwd), agent.SkillsDir)
}

// GetInstallPath returns the agent-specific install path for a skill name.
func GetInstallPath(skillName string, agentType AgentType, global bool, cwd string) string {
	return filepath.Join(GetAgentBaseDir(agentType, global, cwd, ""), SanitizeName(skillName))
}

// GetCanonicalPath returns the canonical .agents/skills/<skill> path.
func GetCanonicalPath(skillName string, global bool, cwd string) string {
	return filepath.Join(GetCanonicalSkillsDir(global, cwd), SanitizeName(skillName))
}

// IsSkillInstalled reports whether skillName exists in the agent's skills directory.
func IsSkillInstalled(skillName string, agentType AgentType, global bool, cwd string, eveSubagent string) bool {
	agent, ok := Agents[agentType]
	if !ok {
		return false
	}
	if global && agent.GlobalSkillsDir == "" {
		return false
	}
	var base string
	switch {
	case global:
		base = ResolvePath(agent.GlobalSkillsDir, cwd)
	case agentType == "eve" && eveSubagent != "":
		base = GetEveSubagentSkillsDir(eveSubagent, cwd)
	default:
		base = filepath.Join(installCwd(cwd), agent.SkillsDir)
	}
	skillDir := filepath.Join(base, SanitizeName(skillName))
	if !isPathSafe(base, skillDir) {
		return false
	}
	_, err := os.Stat(skillDir)
	return err == nil
}

func resolveSymlinkTarget(linkPath, linkTarget string) string {
	if filepath.IsAbs(linkTarget) {
		return filepath.Clean(linkTarget)
	}
	return installAbsClean(filepath.Join(filepath.Dir(linkPath), linkTarget))
}

func cleanAndCreateDirectory(path string) error {
	_ = os.RemoveAll(path)
	return os.MkdirAll(path, 0o755)
}

// resolveParentSymlinks resolves the parent directory through symlinks while keeping the
// final path component, so ~/.claude/skills -> ~/.agents/skills aliases compare equal (#293).
func resolveParentSymlinks(p string) string {
	resolved := installAbsClean(p)
	dir := filepath.Dir(resolved)
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(realDir, filepath.Base(resolved))
	}
	return resolved
}

func installEvalOr(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// createSymlink links linkPath to target using a relative link (a directory junction
// with an absolute target on Windows when symlinks are unavailable). It returns false
// when the caller must fall back to copying.
func createSymlink(target, linkPath string) bool {
	resolvedTarget := installAbsClean(target)
	resolvedLinkPath := installAbsClean(linkPath)

	if installEvalOr(resolvedTarget) == installEvalOr(resolvedLinkPath) {
		return true
	}
	if resolveParentSymlinks(target) == resolveParentSymlinks(linkPath) {
		return true
	}

	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if existing, err := os.Readlink(linkPath); err == nil && resolveSymlinkTarget(linkPath, existing) == resolvedTarget {
				return true
			}
			if err := os.Remove(linkPath); err != nil {
				return false
			}
		} else if err := os.RemoveAll(linkPath); err != nil {
			return false
		}
	}

	linkDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return false
	}
	realLinkDir := resolveParentSymlinks(linkDir)
	if real, err := filepath.EvalSymlinks(linkDir); err == nil {
		realLinkDir = real
	}
	// Resolve only the target's parents: when the target is itself a link (link mode,
	// sync), the agent link must point at it, not at whatever it points to.
	rel, err := filepath.Rel(realLinkDir, resolveParentSymlinks(resolvedTarget))
	if err != nil {
		rel = resolvedTarget
	}

	if runtime.GOOS == "windows" {
		if err := os.Symlink(resolvedTarget, linkPath); err == nil {
			return true
		}
		// No Developer Mode: fall back to a directory junction, created through the
		// Win32 API (never through cmd.exe, which would interpret path metacharacters).
		return createJunction(linkPath, resolvedTarget) == nil
	}
	return os.Symlink(rel, linkPath) == nil
}

func installWarnf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "⚠ "+format+"\n", args...)
}

// CopySkillDirectory recursively copies a skill directory, dereferencing symlinks so the
// installed copy is self-contained. Symlinks whose real target escapes the skill root are
// skipped (#2652), broken links are skipped, directory cycles are cut, executable modes
// are preserved, and metadata.json/.git/__pycache__/__pypackages__ are excluded.
func CopySkillDirectory(src, dest string, agentType AgentType, verbose bool) error {
	return copySkillDirectory(src, dest, agentType, verbose, "", nil)
}

func copySkillDirectory(src, dest string, agentType AgentType, verbose bool, root string, ancestors map[string]bool) error {
	realSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	realSrc = installAbsClean(realSrc)
	if root == "" {
		root = realSrc
	}
	if ancestors[realSrc] {
		installWarnf("Skipping symlink that creates a directory cycle: %s", src)
		return nil
	}
	branch := make(map[string]bool, len(ancestors)+1)
	for k := range ancestors {
		branch[k] = true
	}
	branch[realSrc] = true

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if verbose {
		fmt.Printf("📂 Created folder: %s\n", dest)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if isExcludedEntry(name, entry.IsDir()) {
			continue
		}
		srcPath := filepath.Join(src, name)
		destPath := filepath.Join(dest, name)
		isDir := entry.IsDir()

		if entry.Type()&os.ModeSymlink != 0 {
			realTarget, err := filepath.EvalSymlinks(srcPath)
			if err != nil {
				installWarnf("Skipping broken symlink: %s", srcPath)
				continue
			}
			realTarget = installAbsClean(realTarget)
			if !isWithinRoot(root, realTarget) {
				installWarnf("Skipping symlink that points outside the skill directory: %s", srcPath)
				continue
			}
			info, err := os.Stat(realTarget)
			if err != nil {
				installWarnf("Skipping broken symlink: %s", srcPath)
				continue
			}
			isDir = info.IsDir()
			if isDir && isExcludedEntry(name, true) {
				continue
			}
		}

		if isDir {
			if err := copySkillDirectory(srcPath, destPath, agentType, verbose, root, branch); err != nil {
				return err
			}
			continue
		}

		if agentType == "eve" && strings.EqualFold(name, "skill.md") {
			raw, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destPath, []byte(stripIgnoredEveFrontmatter(string(raw))), 0o644); err != nil {
				return err
			}
			continue
		}

		if err := copyRegularFile(srcPath, destPath, verbose); err != nil {
			return err
		}
	}
	return nil
}

func isWithinRoot(root, target string) bool {
	return target == root || strings.HasPrefix(target, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// copyRegularFile copies the (dereferenced) contents of src to dest and preserves its
// permission bits so executable scripts stay executable.
func copyRegularFile(src, dest string, verbose bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	// Replace any existing link at dest instead of writing through it.
	if li, err := os.Lstat(dest); err == nil && li.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(dest)
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if verbose {
		fmt.Printf("📄 Created file: %s (%s)\n", dest, formatSize(info.Size()))
	}
	return os.Chmod(dest, info.Mode().Perm())
}

func formatSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
}

// stripIgnoredEveFrontmatter keeps only the frontmatter keys Eve understands.
func stripIgnoredEveFrontmatter(raw string) string {
	data, content, err := ParseFrontmatter(raw)
	if err != nil {
		return raw
	}
	body := strings.TrimPrefix(strings.TrimPrefix(content, "\r\n"), "\n")

	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value interface{}) {
		var v yaml.Node
		if err := v.Encode(value); err != nil {
			return
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &v)
	}
	if s, ok := data["name"].(string); ok {
		add("name", s)
	}
	if s, ok := data["description"].(string); ok {
		add("description", s)
	}
	if s, ok := data["license"].(string); ok {
		add("license", s)
	}
	if v, ok := data["compatibility"]; ok && v != nil {
		add("compatibility", v)
	}
	switch v := data["version"].(type) {
	case string, int, int64, float64, uint64:
		add("version", v)
	}
	if m, ok := data["metadata"].(map[string]interface{}); ok {
		add("metadata", m)
	}
	if len(root.Content) == 0 {
		return body
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return raw
	}
	_ = enc.Close()
	return "---\n" + strings.TrimRight(buf.String(), "\n\r\t ") + "\n---\n" + body
}

func unsupportedGlobalResult(agent AgentConfig, mode InstallMode) InstallResult {
	return InstallResult{
		Success: false,
		Mode:    mode,
		Error:   fmt.Sprintf("%s does not support global skill installation", agent.DisplayName),
	}
}

func traversalResult(path string, mode InstallMode) InstallResult {
	return InstallResult{Success: false, Path: path, Mode: mode, Error: "Invalid skill name: potential path traversal detected"}
}

func failResult(path string, mode InstallMode, err error) InstallResult {
	return InstallResult{Success: false, Path: path, Mode: mode, Error: err.Error()}
}

func installModeOrDefault(m InstallMode) InstallMode {
	if m == "" {
		return InstallModeSymlink
	}
	return m
}

func canonicalBaseFor(agentType AgentType, mode InstallMode, opts InstallOptions, cwd string) string {
	if agentType == "eve" && mode == InstallModeSymlink {
		return GetAgentBaseDir(agentType, opts.Global, cwd, opts.EveSubagent)
	}
	return GetCanonicalSkillsDir(opts.Global, cwd)
}

// InstallSkillForAgent installs a skill directory (skill.Path) for one agent. In symlink
// mode the skill is copied to the canonical directory and the agent directory becomes a
// relative symlink to it; in copy mode it is copied straight into the agent directory; in
// link mode the canonical directory links to the source itself.
func InstallSkillForAgent(skill Skill, agentType AgentType, opts InstallOptions) InstallResult {
	mode := installModeOrDefault(opts.Mode)
	agent, ok := Agents[agentType]
	if !ok {
		return InstallResult{Success: false, Mode: mode, Error: fmt.Sprintf("unknown agent type: %s", agentType)}
	}
	if opts.Global && agent.GlobalSkillsDir == "" {
		return unsupportedGlobalResult(agent, mode)
	}
	cwd := installCwd(opts.Cwd)

	rawName := skill.Name
	if rawName == "" {
		rawName = filepath.Base(skill.Path)
	}
	skillName := SanitizeName(rawName)

	canonicalBase := canonicalBaseFor(agentType, mode, opts, cwd)
	canonicalDir := filepath.Join(canonicalBase, skillName)
	agentBase := GetAgentBaseDir(agentType, opts.Global, cwd, opts.EveSubagent)
	agentDir := filepath.Join(agentBase, skillName)

	if !isPathSafe(canonicalBase, canonicalDir) || !isPathSafe(agentBase, agentDir) {
		return traversalResult(agentDir, mode)
	}

	// Never install onto (or inside) the source directory, e.g. `skl add ./skills --all`
	// targeting OpenClaw's project "skills" dir would otherwise delete the source.
	if pathsOverlap(skill.Path, agentDir) {
		return InstallResult{Success: true, Path: agentDir, Mode: mode, Skipped: true}
	}

	if mode == InstallModeCopy {
		if err := cleanAndCreateDirectory(agentDir); err != nil {
			return failResult(agentDir, mode, err)
		}
		if err := CopySkillDirectory(skill.Path, agentDir, agentType, opts.Verbose); err != nil {
			return failResult(agentDir, mode, err)
		}
		return InstallResult{Success: true, Path: agentDir, Mode: InstallModeCopy}
	}

	sourceIsCanonical := installAbsClean(skill.Path) == installAbsClean(canonicalDir)
	if !sourceIsCanonical && pathsOverlap(skill.Path, canonicalDir) {
		return InstallResult{Success: true, Path: canonicalDir, CanonicalPath: canonicalDir, Mode: mode, Skipped: true}
	}
	if !sourceIsCanonical {
		if mode == InstallModeLink {
			if !createSymlink(skill.Path, canonicalDir) {
				if err := cleanAndCreateDirectory(canonicalDir); err != nil {
					return failResult(agentDir, mode, err)
				}
				if err := CopySkillDirectory(skill.Path, canonicalDir, agentType, opts.Verbose); err != nil {
					return failResult(agentDir, mode, err)
				}
			}
		} else {
			if err := cleanAndCreateDirectory(canonicalDir); err != nil {
				return failResult(agentDir, mode, err)
			}
			if err := CopySkillDirectory(skill.Path, canonicalDir, agentType, opts.Verbose); err != nil {
				return failResult(agentDir, mode, err)
			}
		}
	}

	return linkCanonicalToAgent(canonicalDir, agentDir, agentType, mode, opts, cwd, true, func(dir string) error {
		return CopySkillDirectory(skill.Path, dir, agentType, opts.Verbose)
	})
}

// linkCanonicalToAgent finishes a symlink-mode install once canonicalDir is populated.
func linkCanonicalToAgent(canonicalDir, agentDir string, agentType AgentType, mode InstallMode, opts InstallOptions, cwd string, checkProjectRoot bool, materialize func(dir string) error) InstallResult {
	// Agents that read ~/.agents/skills globally need no extra link (no duplicates in
	// e.g. ~/.copilot/skills).
	if opts.Global && ReadsCanonicalGlobally(agentType) {
		return InstallResult{Success: true, Path: canonicalDir, CanonicalPath: canonicalDir, Mode: mode}
	}
	if checkProjectRoot && shouldSkipProjectAgentSymlink(agentType, opts.Global, cwd, opts.CreateMissingAgentRoot) {
		return InstallResult{
			Success: true, Path: canonicalDir, CanonicalPath: canonicalDir, Mode: mode,
			Skipped: true, SkipReason: SkipReasonMissingAgentProjectDirectory,
		}
	}
	if installAbsClean(canonicalDir) == installAbsClean(agentDir) {
		return InstallResult{Success: true, Path: agentDir, CanonicalPath: canonicalDir, Mode: mode}
	}
	if !createSymlink(canonicalDir, agentDir) {
		if opts.Verbose {
			fmt.Printf("⚠️  Symlink failed, falling back to copy: %s\n", agentDir)
		}
		if err := cleanAndCreateDirectory(agentDir); err != nil {
			return failResult(agentDir, mode, err)
		}
		if err := materialize(agentDir); err != nil {
			return failResult(agentDir, mode, err)
		}
		return InstallResult{Success: true, Path: agentDir, CanonicalPath: canonicalDir, Mode: mode, SymlinkFailed: true}
	}
	return InstallResult{Success: true, Path: agentDir, CanonicalPath: canonicalDir, Mode: mode}
}

func writeInstallFiles(targetDir string, files []InstallFile, agentType AgentType, applyExclusions bool) error {
	for _, f := range files {
		if applyExclusions && isExcludedInstallPath(f.Path) {
			continue
		}
		full := filepath.Join(targetDir, filepath.FromSlash(f.Path))
		if !isPathSafe(targetDir, full) || installAbsClean(full) == installAbsClean(targetDir) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		contents := f.Contents
		if agentType == "eve" && strings.EqualFold(filepath.Base(f.Path), "skill.md") {
			contents = []byte(stripIgnoredEveFrontmatter(string(contents)))
		}
		if err := os.WriteFile(full, contents, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func isEvePackagedSkill(files []InstallFile) bool {
	for _, f := range files {
		if strings.EqualFold(filepath.Base(f.Path), "skill.md") {
			return true
		}
	}
	return false
}

func eveFlatSkillMarkdown(files []InstallFile) string {
	for _, f := range files {
		if strings.EqualFold(filepath.Base(f.Path), "skill.md") {
			return stripIgnoredEveFrontmatter(string(f.Contents))
		}
	}
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f.Path), ".md") {
			return stripIgnoredEveFrontmatter(string(f.Contents))
		}
	}
	return ""
}

type filesInstallKind int

const (
	filesKindWellKnown filesInstallKind = iota
	filesKindBlob
	filesKindRemote
)

func installFilesForAgent(installName string, files []InstallFile, agentType AgentType, opts InstallOptions, kind filesInstallKind) InstallResult {
	mode := installModeOrDefault(opts.Mode)
	agent, ok := Agents[agentType]
	if !ok {
		return InstallResult{Success: false, Mode: mode, Error: fmt.Sprintf("unknown agent type: %s", agentType)}
	}
	if opts.Global && agent.GlobalSkillsDir == "" {
		return unsupportedGlobalResult(agent, mode)
	}
	cwd := installCwd(opts.Cwd)
	skillName := SanitizeName(installName)
	agentBase := GetAgentBaseDir(agentType, opts.Global, cwd, opts.EveSubagent)

	if kind == filesKindBlob && agentType == "eve" && !isEvePackagedSkill(files) {
		flat := filepath.Join(agentBase, skillName+".md")
		if !isPathSafe(agentBase, flat) {
			return traversalResult(flat, mode)
		}
		if err := os.MkdirAll(agentBase, 0o755); err != nil {
			return failResult(flat, mode, err)
		}
		_ = os.RemoveAll(flat)
		if err := os.WriteFile(flat, []byte(eveFlatSkillMarkdown(files)), 0o644); err != nil {
			return failResult(flat, mode, err)
		}
		return InstallResult{Success: true, Path: flat, Mode: InstallModeCopy}
	}

	canonicalBase := canonicalBaseFor(agentType, mode, opts, cwd)
	canonicalDir := filepath.Join(canonicalBase, skillName)
	agentDir := filepath.Join(agentBase, skillName)
	if !isPathSafe(canonicalBase, canonicalDir) || !isPathSafe(agentBase, agentDir) {
		return traversalResult(agentDir, mode)
	}

	write := func(dir string) error {
		return writeInstallFiles(dir, files, agentType, kind == filesKindBlob)
	}

	if mode == InstallModeCopy {
		if err := cleanAndCreateDirectory(agentDir); err != nil {
			return failResult(agentDir, mode, err)
		}
		if err := write(agentDir); err != nil {
			return failResult(agentDir, mode, err)
		}
		return InstallResult{Success: true, Path: agentDir, Mode: InstallModeCopy}
	}

	if err := cleanAndCreateDirectory(canonicalDir); err != nil {
		return failResult(agentDir, mode, err)
	}
	if err := write(canonicalDir); err != nil {
		return failResult(agentDir, mode, err)
	}
	res := linkCanonicalToAgent(canonicalDir, agentDir, agentType, InstallModeSymlink, opts, cwd, kind == filesKindBlob, write)
	return res
}

// InstallWellKnownSkillForAgent installs a multi-file skill fetched from a well-known index.
func InstallWellKnownSkillForAgent(installName string, files []InstallFile, agentType AgentType, opts InstallOptions) InstallResult {
	return installFilesForAgent(installName, files, agentType, opts, filesKindWellKnown)
}

// InstallBlobSkillForAgent installs a skill snapshot (skills.sh download API) for one agent,
// applying the same exclusions as CopySkillDirectory.
func InstallBlobSkillForAgent(installName string, files []InstallFile, agentType AgentType, opts InstallOptions) InstallResult {
	return installFilesForAgent(installName, files, agentType, opts, filesKindBlob)
}

// InstallRemoteSkillForAgent installs a single-file remote skill (SKILL.md content only).
func InstallRemoteSkillForAgent(skill RemoteSkill, agentType AgentType, opts InstallOptions) InstallResult {
	fileName := "SKILL.md"
	if agentType == "eve" {
		fileName = SanitizeName(skill.InstallName) + ".md"
	}
	return installFilesForAgent(skill.InstallName, []InstallFile{{Path: fileName, Contents: []byte(skill.Content)}}, agentType, opts, filesKindRemote)
}

// LinkSkillToAgents installs one on-disk skill for every target agent with the same options
// (used by add, update, and restore-from-lock). It returns one result per agent and an error
// summarizing failures, if any.
func LinkSkillToAgents(skill Skill, agents []AgentType, opts InstallOptions) ([]AgentInstallResult, error) {
	targets := make([]InstallTarget, 0, len(agents))
	for _, a := range agents {
		targets = append(targets, InstallTarget{Agent: a, EveSubagent: opts.EveSubagent})
	}
	return InstallSkillToTargets(skill, targets, opts)
}

// InstallSkillToTargets installs one on-disk skill for each target (agent + optional Eve subagent).
func InstallSkillToTargets(skill Skill, targets []InstallTarget, opts InstallOptions) ([]AgentInstallResult, error) {
	results := make([]AgentInstallResult, 0, len(targets))
	var failures []string
	for _, t := range targets {
		o := opts
		o.EveSubagent = t.EveSubagent
		res := InstallSkillForAgent(skill, t.Agent, o)
		results = append(results, AgentInstallResult{Agent: t.Agent, EveSubagent: t.EveSubagent, Result: res})
		if !res.Success {
			failures = append(failures, fmt.Sprintf("%s: %s", agentLabel(t.Agent), res.Error))
		}
	}
	if len(failures) > 0 {
		return results, errors.New(strings.Join(failures, "; "))
	}
	return results, nil
}

func agentLabel(a AgentType) string {
	if cfg, ok := Agents[a]; ok && cfg.DisplayName != "" {
		return cfg.DisplayName
	}
	return string(a)
}

// BuildInstallTargets expands agents into install targets; eve gets one target per
// requested subagent ("root" or "." means the root agent).
func BuildInstallTargets(agents []AgentType, eveSubagents []string) []InstallTarget {
	var targets []InstallTarget
	for _, a := range agents {
		if a == "eve" && len(eveSubagents) > 0 {
			for _, s := range eveSubagents {
				if s == "root" || s == "." || s == "" {
					targets = append(targets, InstallTarget{Agent: a})
				} else {
					targets = append(targets, InstallTarget{Agent: a, EveSubagent: s})
				}
			}
			continue
		}
		targets = append(targets, InstallTarget{Agent: a})
	}
	return targets
}

// DefaultInstallMode symlinks from a canonical copy only when targets span several project
// skills directories; a single directory, or Eve (which owns each (sub)agent dir), gets a
// plain copy (upstream defaultInstallMode).
func DefaultInstallMode(targets []InstallTarget) InstallMode {
	dirs := map[string]bool{}
	allEve := true
	for _, t := range targets {
		if t.Agent != "eve" {
			allEve = false
		}
		if t.EveSubagent != "" {
			dirs["eve:subagent:"+t.EveSubagent] = true
			continue
		}
		if cfg, ok := Agents[t.Agent]; ok {
			dirs[cfg.SkillsDir] = true
		} else {
			dirs[string(t.Agent)] = true
		}
	}
	if len(dirs) <= 1 || allEve {
		return InstallModeCopy
	}
	return InstallModeSymlink
}

// SkipAgentsWithoutGlobalDir drops agents without a global skills directory from a global
// install when they were not explicitly requested (#2547). If every target lacks one, the
// list is returned unchanged so the per-agent error is still reported.
func SkipAgentsWithoutGlobalDir(targets []AgentType, global bool, explicit bool, w io.Writer) []AgentType {
	if !global || explicit {
		return targets
	}
	var kept []AgentType
	var skipped []string
	for _, a := range targets {
		if cfg, ok := Agents[a]; ok && cfg.GlobalSkillsDir == "" {
			skipped = append(skipped, agentLabel(a))
			continue
		}
		kept = append(kept, a)
	}
	if len(skipped) == 0 || len(kept) == 0 {
		return targets
	}
	if w != nil {
		fmt.Fprintf(w, "Skipping %s: no global skills directory\n", strings.Join(skipped, ", "))
	}
	return kept
}

// SplitAgentsByType splits targets into agents served by the canonical directory
// ("universal") and agents that get a symlink, matching the add summary (#2271).
func SplitAgentsByType(agents []AgentType, global bool) (universal, symlinked []AgentType) {
	for _, a := range agents {
		if (global && ReadsCanonicalGlobally(a)) || (!global && IsUniversalAgent(a)) {
			universal = append(universal, a)
		} else {
			symlinked = append(symlinked, a)
		}
	}
	return universal, symlinked
}

// ---------------------------------------------------------------------------
// Legacy helpers kept for callers that predate the upstream-aligned installer.
// ---------------------------------------------------------------------------

// copyDir copies a skill directory with symlink confinement (legacy name).
func copyDir(src, dest string, verbose bool) error {
	return CopySkillDirectory(src, dest, "", verbose)
}

// installSkillForAgent is the legacy installer entry point (symlink unless opts.Copy).
func installSkillForAgent(skillName, srcPath string, agentType AgentType, opts AddOptions, cwd string) (InstallResult, error) {
	if _, ok := Agents[agentType]; !ok {
		return InstallResult{}, fmt.Errorf("unknown agent type: %s", agentType)
	}
	mode := InstallModeSymlink
	if opts.Copy {
		mode = InstallModeCopy
	}
	res := InstallSkillForAgent(Skill{Name: skillName, Path: srcPath}, agentType, InstallOptions{
		Global: opts.Global, Cwd: cwd, Mode: mode, Verbose: opts.Verbose,
	})
	return res, nil
}

func detectInstalledAgents(home, configHome, cwd string) []AgentType {
	var installed []AgentType
	for _, k := range orderedAgentTypes(Agents) {
		cfg := Agents[k]
		if cfg.DetectInstalled != nil && cfg.DetectInstalled(home, configHome, cwd) {
			installed = append(installed, k)
		}
	}
	return installed
}

func isUniversalAgent(agentType AgentType) bool {
	return IsUniversalAgent(agentType)
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func installSortedAgentNames() []string {
	names := make([]string, 0, len(Agents))
	for k := range Agents {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return names
}

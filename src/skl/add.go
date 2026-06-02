package skl

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ParseAddOptions parses command line arguments for the 'add' command.
func ParseAddOptions(args []string) (string, AddOptions) {
	var opts AddOptions
	var source string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-g" || arg == "--global" {
			opts.Global = true
		} else if arg == "--copy" {
			opts.Copy = true
		} else if arg == "-v" || arg == "--verbose" {
			opts.Verbose = true
		} else if arg == "-y" || arg == "--yes" {
			opts.Yes = true
		} else if arg == "-l" || arg == "--list" {
			opts.List = true
		} else if arg == "--full-depth" {
			opts.FullDepth = true
		} else if arg == "-s" || arg == "--skill" {
			if i+1 < len(args) {
				i++
				parts := strings.Split(args[i], ",")
				for _, part := range parts {
					trimmed := strings.TrimSpace(part)
					if trimmed != "" {
						opts.Skill = append(opts.Skill, trimmed)
					}
				}
			}
		} else if arg == "-a" || arg == "--agent" {
			if i+1 < len(args) {
				i++
				parts := strings.Split(args[i], ",")
				for _, part := range parts {
					trimmed := strings.TrimSpace(part)
					if trimmed != "" {
						opts.Agent = append(opts.Agent, AgentType(trimmed))
					}
				}
			}
		} else if !strings.HasPrefix(arg, "-") {
			source = arg
		}
	}

	return source, opts
}

// AddSkill copies a local skill directory recursively to the canonical `.agents/skills`
// folder in the specified cwd (or to the user's global skills directory if Global is set).
// It also supports remote sources by cloning via git or downloading via HTTP zipball fetch.
func AddSkill(src string, opts AddOptions, cwd string) error {
	if src == "" {
		return errors.New("source skill directory is required")
	}

	var localSrc = src
	var isTemp = false
	var targetSkillName = ""

	if isGitRepoSource(src) {
		repoURL, ref, skillName := parseRemoteSource(src)
		targetSkillName = skillName

		if opts.Verbose {
			fmt.Printf("🌐 Remote: %s (ref: %s, target: %s)\n", repoURL, ref, targetSkillName)
		}

		tempDir, err := downloadRemote(repoURL, ref, opts)
		if err != nil {
			return fmt.Errorf("download remote: %w", err)
		}
		localSrc = tempDir
		isTemp = true
	}

	if isTemp {
		defer func() {
			if opts.Verbose {
				fmt.Printf("🧹 Cleaned temp: %s\n", localSrc)
			}
			os.RemoveAll(localSrc)
		}()
	}

	// Find all subdirectories that contain a SKILL.md
	skillDirs := findSkillDirectories(localSrc, opts.FullDepth)
	if len(skillDirs) == 0 {
		return fmt.Errorf("no skills (directories containing SKILL.md) found in source %q", src)
	}

	// Filter down by targetSkillName or opts.Skill if provided
	var matchedDirs []string
	for _, dir := range skillDirs {
		skillMdPath := filepath.Join(dir, "SKILL.md")
		bytes, err := os.ReadFile(skillMdPath)
		if err != nil {
			continue
		}
		data, _, err := ParseFrontmatter(string(bytes))
		if err != nil {
			continue
		}
		name, _ := data["name"].(string)

		matches := false
		if targetSkillName != "" {
			if strings.ToLower(name) == strings.ToLower(targetSkillName) || strings.ToLower(filepath.Base(dir)) == strings.ToLower(targetSkillName) {
				matches = true
			}
		} else if len(opts.Skill) > 0 {
			for _, filterSkill := range opts.Skill {
				if strings.ToLower(name) == strings.ToLower(filterSkill) || strings.ToLower(filepath.Base(dir)) == strings.ToLower(filterSkill) {
					matches = true
					break
				}
			}
		} else {
			matches = true
		}

		if matches {
			matchedDirs = append(matchedDirs, dir)
		}
	}

	if len(matchedDirs) == 0 {
		if targetSkillName != "" {
			return fmt.Errorf("skill %q not found in source", targetSkillName)
		}
		return errors.New("no valid skills found in source")
	}

	if opts.List {
		for _, dir := range matchedDirs {
			skillMdPath := filepath.Join(dir, "SKILL.md")
			bytes, err := os.ReadFile(skillMdPath)
			if err != nil {
				continue
			}
			data, _, err := ParseFrontmatter(string(bytes))
			if err != nil {
				continue
			}
			name, _ := data["name"].(string)
			desc, _ := data["description"].(string)
			fmt.Printf("- %s: %s (path: %s)\n", name, desc, dir)
		}
		return nil
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	// Install each matched directory
	for _, dir := range matchedDirs {
		skillMdPath := filepath.Join(dir, "SKILL.md")
		bytes, err := os.ReadFile(skillMdPath)
		if err != nil {
			return err
		}

		data, _, err := ParseFrontmatter(string(bytes))
		if err != nil {
			return err
		}

		nameVal, ok := data["name"]
		if !ok {
			return errors.New("frontmatter is missing 'name' property")
		}
		skillName, ok := nameVal.(string)
		if !ok || skillName == "" {
			return errors.New("invalid or empty skill name in frontmatter")
		}
		skillName = SanitizeName(skillName)

		// Determine target agents
		var targetAgents []AgentType
		if len(opts.Agent) > 0 {
			targetAgents = opts.Agent
		} else {
			// Find installed agents dynamically!
			home, _ := os.UserHomeDir()
			configHome := os.Getenv("XDG_CONFIG_HOME")
			if configHome == "" {
				configHome = filepath.Join(home, ".config")
			}
			detected := detectInstalledAgents(home, configHome, cwd)

			var hasAgy bool
			var otherAgents []AgentType
			for _, a := range detected {
				if a == AgentAntigravity {
					hasAgy = true
				} else if a != AgentUniversal {
					otherAgents = append(otherAgents, a)
				}
			}

			// Resolution logic matching user requirements:
			if hasAgy || len(otherAgents) == 0 {
				targetAgents = []AgentType{AgentAntigravity}

				// If absolutely no agents are installed, maybe prompt about agy-cli
				if len(otherAgents) == 0 && !hasAgy {
					maybeSuggestAgy(opts)
				}
			} else if len(otherAgents) == 1 {
				// Single agent detected: target it directly
				targetAgents = []AgentType{otherAgents[0]}
			} else {
				// Multiple agents detected: fall back to universal
				targetAgents = []AgentType{AgentUniversal}
			}
		}

		// Install to each target agent!
		for _, agentType := range targetAgents {
			res, err := installSkillForAgent(skillName, dir, agentType, opts, cwd)
			if err != nil {
				return fmt.Errorf("install skill for agent %s: %w", agentType, err)
			}
			if !res.Success {
				return fmt.Errorf("install skill %q for agent %s: %s", skillName, agentType, res.Error)
			}
			if opts.Verbose {
				status := "Installed"
				if res.Skipped {
					status = "Skipped"
				} else if res.SymlinkFailed {
					status = "Copied (symlink failed)"
				}
				fmt.Printf("🎯 Agent %s: %s at %s\n", agentType, status, res.Path)
			}
		}

		// Add to skill lock files (Global or Local)
		canonicalBase := getCanonicalSkillsDir(opts.Global, cwd)
		canonicalDir := filepath.Join(canonicalBase, skillName)
		hash, _ := ComputeSkillFolderHash(canonicalDir)
		sourceType := "local"
		if isGitRepoSource(src) {
			if strings.Contains(src, "github.com") {
				sourceType = "github"
			} else {
				sourceType = "git"
			}
		}

		if opts.Global {
			_ = AddSkillToLock(skillName, SkillLockEntry{
				Source:          src,
				SourceType:      sourceType,
				SourceUrl:       src,
				SkillFolderHash: hash,
			})
		} else {
			_ = AddSkillToLocalLock(skillName, LocalSkillLockEntry{
				Source:       src,
				SourceType:   sourceType,
				ComputedHash: hash,
			}, cwd)
		}
	}

	return nil
}

func isGitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func isGitRepoSource(src string) bool {
	if strings.HasPrefix(src, "git@") || strings.HasPrefix(src, "ssh://") {
		return true
	}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return true
	}

	// Local paths should never be treated as git repository sources
	if strings.HasPrefix(src, "/") || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~") {
		return false
	}
	if len(src) >= 3 && src[1] == ':' && (src[2] == '/' || src[2] == '\\') {
		return false
	}

	parts := strings.Split(src, "/")
	if len(parts) >= 2 && !strings.Contains(parts[0], ".") && !strings.Contains(parts[0], ":") {
		return true
	}
	return false
}

func parseRemoteSource(src string) (repoURL string, ref string, skillName string) {
	if atIdx := strings.Index(src, "@"); atIdx > 0 {
		skillName = src[atIdx+1:]
		src = src[:atIdx]
	}

	if hashIdx := strings.Index(src, "#"); hashIdx > 0 {
		ref = src[hashIdx+1:]
		src = src[:hashIdx]
	}

	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "git@") || strings.HasPrefix(src, "ssh://") {
		repoURL = src
		return
	}

	repoURL = fmt.Sprintf("https://github.com/%s.git", src)
	return
}

func downloadRemote(repoURL string, ref string, opts AddOptions) (string, error) {
	verbose := opts.Verbose
	tmpDir, err := os.MkdirTemp("", "skills-clone-")
	if err != nil {
		return "", err
	}
	if verbose {
		fmt.Printf("📂 Created temp folder: %s\n", tmpDir)
	}

	gitInstalled := isGitInstalled()
	var cloneErr error

	if gitInstalled {
		if verbose {
			fmt.Printf("📥 Cloning: %s\n", repoURL)
		}
		var cmd *exec.Cmd
		if ref != "" {
			cmd = exec.Command("git", "clone", "--depth", "1", "--branch", ref, repoURL, tmpDir)
		} else {
			cmd = exec.Command("git", "clone", "--depth", "1", repoURL, tmpDir)
		}
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		cloneErr = cmd.Run()
		if cloneErr == nil {
			return tmpDir, nil
		}
		if verbose {
			fmt.Printf("⚠️  Git clone failed (%v)\n", cloneErr)
		}
	}

	// Git clone failed, or Git is not installed on this system.
	// Display a clear notification warning the user about the limitations of falling back to zip download.
	fmt.Println()
	if !gitInstalled {
		fmt.Println("⚠️  Git is not installed or available on this system's PATH.")
	} else {
		fmt.Printf("⚠️  Git clone failed: %v\n", cloneErr)
	}

	fmt.Println("ℹ️  Limitation: To proceed, we can attempt to download a public HTTP ZIP archive of the repository.")
	if ref != "" {
		fmt.Printf("   - Attempting to download ZIP archive for reference %q.\n", ref)
	} else {
		fmt.Println("   - This only supports standard default branches ('main' or 'master').")
	}
	fmt.Println("   - This does NOT support private repositories or custom branches (unless public ZIP is accessible).")
	fmt.Println()

	// Prompt/confirm if interactive
	if !opts.Yes && isInteractive() {
		fmt.Print("❓ Would you like to proceed with the HTTP ZIP archive fallback? [y/N]: ")
		var response string
		_, _ = fmt.Scanln(&response)
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			_ = os.RemoveAll(tmpDir)
			if !gitInstalled {
				return "", errors.New("installation aborted: git is not installed and HTTP fallback declined")
			}
			return "", fmt.Errorf("installation aborted: git clone failed (%w) and HTTP fallback declined", cloneErr)
		}
	} else if !opts.Yes {
		// Non-interactive session without --yes flag:
		// To mirror the original npx skills crash behavior precisely, we abort unless explicitly bypassed with --yes/-y.
		_ = os.RemoveAll(tmpDir)
		if !gitInstalled {
			return "", errors.New("git is not installed and session is non-interactive; run with --yes to allow HTTP zip download fallback")
		}
		return "", fmt.Errorf("git clone failed (%w) and session is non-interactive; run with --yes to allow HTTP zip download fallback", cloneErr)
	}

	// Proceed to download ZIP archive!
	zipURL := repoURL
	if strings.Contains(repoURL, "github.com") {
		cleanURL := strings.TrimSuffix(repoURL, ".git")
		if ref != "" {
			zipURL = fmt.Sprintf("%s/archive/%s.zip", cleanURL, ref)
		} else {
			zipURL = cleanURL + "/archive/refs/heads/main.zip"
		}
	}

	if verbose {
		fmt.Printf("📥 Fetching zip: %s\n", zipURL)
	}
	resp, err := http.Get(zipURL)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("fetch remote: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if ref == "" && strings.Contains(zipURL, "/refs/heads/main.zip") {
			cleanURL := strings.TrimSuffix(repoURL, ".git")
			zipURL = cleanURL + "/archive/refs/heads/master.zip"
			if verbose {
				fmt.Printf("📥 Fetching master zip: %s\n", zipURL)
			}
			resp2, err2 := http.Get(zipURL)
			if err2 == nil && resp2.StatusCode == http.StatusOK {
				defer resp2.Body.Close()
				err = extractZip(resp2.Body, tmpDir, verbose)
				if err == nil {
					return tmpDir, nil
				}
			}
		}
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("remote URL returned status %d", resp.StatusCode)
	}

	if err := extractZip(resp.Body, tmpDir, verbose); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("extract zip: %w", err)
	}

	return tmpDir, nil
}

func extractZip(body io.Reader, dest string, verbose bool) error {
	zipBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return err
	}

	for _, file := range reader.File {
		cleaned := filepath.Clean(file.Name)
		if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/") && strings.HasPrefix(cleaned, "/") {
			continue
		}

		path := filepath.Join(dest, file.Name)
		if file.FileInfo().IsDir() {
			if verbose {
				fmt.Printf("📂 Created folder: %s\n", path)
			}
			_ = os.MkdirAll(path, file.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}

		fileReader, err := file.Open()
		if err != nil {
			return err
		}

		if verbose {
			sizeStr := formatSize(file.FileInfo().Size())
			fmt.Printf("📄 Created file: %s (%s)\n", path, sizeStr)
		}
		targetFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			fileReader.Close()
			return err
		}

		_, err = io.Copy(targetFile, fileReader)
		fileReader.Close()
		targetFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func findSkillDirectories(dir string, fullDepth bool) []string {
	var results []string
	seenNames := make(map[string]bool)

	skipDirs := map[string]bool{
		"node_modules": true,
		".git":         true,
		"dist":         true,
		"build":        true,
		"__pycache__":  true,
	}

	hasSkillMd := func(path string) bool {
		_, err := os.Stat(filepath.Join(path, "SKILL.md"))
		return err == nil
	}

	tryAddSkillAt := func(skillDir string) bool {
		if !hasSkillMd(skillDir) {
			return false
		}
		// Read frontmatter to get name
		bytes, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			return false
		}
		data, _, err := ParseFrontmatter(string(bytes))
		if err != nil {
			return false
		}
		name, _ := data["name"].(string)
		if name == "" {
			return false
		}
		name = strings.ToLower(name)
		if seenNames[name] {
			return true // Return true since it is a valid skill directory structure
		}
		seenNames[name] = true
		results = append(results, skillDir)
		return true
	}

	// 1. If pointing directly at a skill, add it and return (unless fullDepth is set)
	if hasSkillMd(dir) {
		tryAddSkillAt(dir)
		if !fullDepth {
			return results
		}
	}

	// 2. Search common skill locations first
	prioritySearchDirs := []string{
		dir,
		filepath.Join(dir, "skills"),
		filepath.Join(dir, "skills", ".curated"),
		filepath.Join(dir, "skills", ".experimental"),
		filepath.Join(dir, "skills", ".system"),
	}

	// Add dynamic XDG/agent locations
	seenPriorityDirs := make(map[string]bool)
	for _, pDir := range prioritySearchDirs {
		seenPriorityDirs[pDir] = true
	}

	for _, agent := range Agents {
		if agent.SkillsDir != "" {
			pDir := filepath.Join(dir, agent.SkillsDir)
			if !seenPriorityDirs[pDir] {
				prioritySearchDirs = append(prioritySearchDirs, pDir)
				seenPriorityDirs[pDir] = true
			}
		}
	}

	for i, pDir := range prioritySearchDirs {
		walkDeep := (i > 0)
		entries, err := os.ReadDir(pDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || skipDirs[entry.Name()] {
				continue
			}
			childDir := filepath.Join(pDir, entry.Name())
			foundAtChild := tryAddSkillAt(childDir)

			if foundAtChild || !walkDeep {
				continue
			}

			// Walk one extra level for catalog layouts
			grandEntries, err := os.ReadDir(childDir)
			if err != nil {
				continue
			}
			for _, grand := range grandEntries {
				if !grand.IsDir() || skipDirs[grand.Name()] {
					continue
				}
				tryAddSkillAt(filepath.Join(childDir, grand.Name()))
			}
		}
	}

	// 3. Fall back to recursive search if nothing found, or if fullDepth is set
	if len(results) == 0 || fullDepth {
		var findSkillDirsRecursive func(currentDir string, depth int)
		findSkillDirsRecursive = func(currentDir string, depth int) {
			if depth > 5 {
				return
			}
			if hasSkillMd(currentDir) {
				tryAddSkillAt(currentDir)
			}
			entries, err := os.ReadDir(currentDir)
			if err != nil {
				return
			}
			for _, entry := range entries {
				if !entry.IsDir() || skipDirs[entry.Name()] {
					continue
				}
				findSkillDirsRecursive(filepath.Join(currentDir, entry.Name()), depth+1)
			}
		}
		findSkillDirsRecursive(dir, 0)
	}

	return results
}

// copyDir recursively copies a directory tree, ignoring .git directory and filters.
func copyDir(src, dest string, verbose bool) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if verbose {
		fmt.Printf("📂 Created folder: %s\n", dest)
	}
	if err := os.MkdirAll(dest, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == "__pycache__" || name == "__pypackages__" || name == "metadata.json" {
			continue
		}

		srcPath := filepath.Join(src, name)
		destPath := filepath.Join(dest, name)

		if entry.IsDir() {
			if err := copyDir(srcPath, destPath, verbose); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, destPath, verbose); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file from src to dest.
func copyFile(src, dest string, verbose bool) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	sizeStr := formatSize(srcInfo.Size())

	if verbose {
		fmt.Printf("📄 Created file: %s (%s)\n", dest, sizeStr)
	}

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return err
	}

	return os.Chmod(dest, srcInfo.Mode())
}

func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f KB", float64(bytes)/1024.0)
}

type InstallResult struct {
	Success       bool
	Path          string
	CanonicalPath string
	Mode          string
	SymlinkFailed bool
	Skipped       bool
	Error         string
}

func getCanonicalSkillsDir(global bool, cwd string) string {
	return GetCanonicalSkillsDir(global, cwd)
}

func getAgentBaseDir(agentType AgentType, global bool, cwd string) string {
	if isUniversalAgent(agentType) {
		return getCanonicalSkillsDir(global, cwd)
	}

	agent, ok := Agents[agentType]
	if !ok {
		return ""
	}

	baseDir := cwd
	if global {
		home, err := os.UserHomeDir()
		if err == nil {
			baseDir = home
		}
	}

	if global {
		if agent.GlobalSkillsDir == "" {
			return filepath.Join(baseDir, agent.SkillsDir)
		}
		return agent.GlobalSkillsDir
	}

	return filepath.Join(baseDir, agent.SkillsDir)
}

func isPathSafe(basePath, targetPath string) bool {
	normBase := filepath.Clean(basePath)
	normTarget := filepath.Clean(targetPath)
	if normTarget == normBase {
		return true
	}
	return strings.HasPrefix(normTarget, normBase+string(filepath.Separator))
}

func installSkillForAgent(skillName, srcPath string, agentType AgentType, opts AddOptions, cwd string) (InstallResult, error) {
	agent, ok := Agents[agentType]
	if !ok {
		return InstallResult{}, fmt.Errorf("unknown agent type: %s", agentType)
	}

	isGlobal := opts.Global
	if isGlobal && agent.GlobalSkillsDir == "" {
		return InstallResult{
			Success: false,
			Mode:    "symlink",
			Error:   fmt.Sprintf("%s does not support global skill installation", agent.DisplayName),
		}, nil
	}

	// Canonical base & canonical dir
	canonicalBase := getCanonicalSkillsDir(isGlobal, cwd)
	canonicalDir := filepath.Join(canonicalBase, skillName)

	// Agent-specific location
	agentBase := getAgentBaseDir(agentType, isGlobal, cwd)
	agentDir := filepath.Join(agentBase, skillName)

	installMode := "symlink"
	if opts.Copy {
		installMode = "copy"
	}

	// Validate paths
	if !isPathSafe(canonicalBase, canonicalDir) {
		return InstallResult{
			Success: false,
			Path:    agentDir,
			Mode:    installMode,
			Error:   "Invalid skill name: potential path traversal detected",
		}, nil
	}

	if !isPathSafe(agentBase, agentDir) {
		return InstallResult{
			Success: false,
			Path:    agentDir,
			Mode:    installMode,
			Error:   "Invalid skill name: potential path traversal detected",
		}, nil
	}

	// For copy mode, skip canonical directory and copy directly to agent location
	if installMode == "copy" {
		if err := cleanAndCreateDirectory(agentDir); err != nil {
			return InstallResult{}, err
		}
		if err := copyDir(srcPath, agentDir, opts.Verbose); err != nil {
			return InstallResult{}, err
		}
		return InstallResult{
			Success: true,
			Path:    agentDir,
			Mode:    "copy",
		}, nil
	}

	// Symlink mode: copy to canonical location and symlink to agent location
	if err := cleanAndCreateDirectory(canonicalDir); err != nil {
		return InstallResult{}, err
	}
	if err := copyDir(srcPath, canonicalDir, opts.Verbose); err != nil {
		return InstallResult{}, err
	}

	// For universal agents with global install, the skill is already in the canonical dir.
	// Skip creating a symlink to prevent duplicates.
	if isGlobal && isUniversalAgent(agentType) {
		return InstallResult{
			Success:       true,
			Path:          canonicalDir,
			CanonicalPath: canonicalDir,
			Mode:          "symlink",
		}, nil
	}

	// For project-level installs, skip creating symlinks for non-universal agents
	// whose config directory doesn't already exist in the project.
	if !isGlobal && !isUniversalAgent(agentType) {
		parts := strings.Split(filepath.ToSlash(agent.SkillsDir), "/")
		if len(parts) > 0 {
			agentRootDir := filepath.Join(cwd, parts[0])
			if _, err := os.Stat(agentRootDir); os.IsNotExist(err) {
				return InstallResult{
					Success:       true,
					Path:          canonicalDir,
					CanonicalPath: canonicalDir,
					Mode:          "symlink",
					Skipped:       true,
				}, nil
			}
		}
	}

	// Create symlink
	symlinkCreated, err := createSymlink(canonicalDir, agentDir, opts.Verbose)
	if !symlinkCreated {
		if opts.Verbose {
			fmt.Printf("⚠️  Symlink failed (%v), falling back to copy...\n", err)
		}
		if err := cleanAndCreateDirectory(agentDir); err != nil {
			return InstallResult{}, err
		}
		if err := copyDir(canonicalDir, agentDir, opts.Verbose); err != nil {
			return InstallResult{}, err
		}
		return InstallResult{
			Success:       true,
			Path:          agentDir,
			CanonicalPath: canonicalDir,
			Mode:          "symlink",
			SymlinkFailed: true,
		}, nil
	}

	return InstallResult{
		Success:       true,
		Path:          agentDir,
		CanonicalPath: canonicalDir,
		Mode:          "symlink",
	}, nil
}

func createSymlink(target, linkPath string, verbose bool) (bool, error) {
	resolvedTarget, err := filepath.Abs(target)
	if err != nil {
		resolvedTarget = target
	}
	resolvedLinkPath, err := filepath.Abs(linkPath)
	if err != nil {
		resolvedLinkPath = linkPath
	}

	realTarget, err := filepath.EvalSymlinks(resolvedTarget)
	if err != nil {
		realTarget = resolvedTarget
	}
	realLinkPath, err := filepath.EvalSymlinks(resolvedLinkPath)
	if err != nil {
		realLinkPath = resolvedLinkPath
	}

	if realTarget == realLinkPath {
		return true, nil
	}

	if stats, err := os.Lstat(linkPath); err == nil {
		if stats.Mode()&os.ModeSymlink != 0 {
			existingTarget, err := os.Readlink(linkPath)
			if err == nil {
				if resolveSymlinkTarget(linkPath, existingTarget) == resolvedTarget {
					return true, nil
				}
			}
			_ = os.Remove(linkPath)
		} else {
			_ = os.RemoveAll(linkPath)
		}
	}

	linkDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		return false, err
	}

	realLinkDir, err := filepath.EvalSymlinks(linkDir)
	if err != nil {
		realLinkDir = linkDir
	}

	relativePath, err := filepath.Rel(realLinkDir, realTarget)
	if err != nil {
		return false, err
	}

	err = os.Symlink(relativePath, linkPath)
	if err != nil {
		return false, err
	}

	return true, nil
}

func resolveSymlinkTarget(linkPath, linkTarget string) string {
	if filepath.IsAbs(linkTarget) {
		return linkTarget
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), linkTarget))
}

func cleanAndCreateDirectory(path string) error {
	_ = os.RemoveAll(path)
	return os.MkdirAll(path, 0755)
}

func detectInstalledAgents(home, configHome, cwd string) []AgentType {
	var installed []AgentType
	for agentType, config := range Agents {
		if config.DetectInstalled != nil && config.DetectInstalled(home, configHome, cwd) {
			installed = append(installed, agentType)
		}
	}
	return installed
}

func isUniversalAgent(agentType AgentType) bool {
	config, ok := Agents[agentType]
	if !ok {
		return false
	}
	return config.SkillsDir == ".agents/skills"
}

func isInteractive() bool {
	fileInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}



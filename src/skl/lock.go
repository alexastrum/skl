package skl

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// ErrInvalidLocalLock is returned when skills-lock.json exists on disk but is malformed,
// contains merge conflicts, or uses an unsupported schema version.
var ErrInvalidLocalLock = errors.New("SKILLS_INVALID_LOCAL_LOCK")

// SkillLockEntry represents a single installed skill entry in the global lock file (.skill-lock.json).
type SkillLockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceUrl       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
	PluginName      string `json:"pluginName,omitempty"`
	SourceBaseUrl   string `json:"sourceBaseUrl,omitempty"`
	WellKnownDigest string `json:"wellKnownDigest,omitempty"`
	InstallMode     string `json:"installMode,omitempty"`
}

// DismissedPrompts tracks dismissed interactive prompts in the global lock file.
type DismissedPrompts struct {
	FindSkillsPrompt bool `json:"findSkillsPrompt,omitempty"`
}

// SkillLockFile represents the global lock file (~/.agents/.skill-lock.json or $XDG_STATE_HOME/skills/.skill-lock.json).
type SkillLockFile struct {
	Version            int                       `json:"version"`
	Skills             map[string]SkillLockEntry `json:"skills"`
	Dismissed          *DismissedPrompts         `json:"dismissed,omitempty"`
	LastSelectedAgents []string                  `json:"lastSelectedAgents,omitempty"`
}

// LocalSkillLockEntry represents a single entry in the project lock file (skills-lock.json).
type LocalSkillLockEntry struct {
	Source          string   `json:"source"`
	SourceUrl       string   `json:"sourceUrl,omitempty"`
	Ref             string   `json:"ref,omitempty"`
	SourceType      string   `json:"sourceType"`
	SkillPath       string   `json:"skillPath,omitempty"`
	Version         string   `json:"version,omitempty"`
	Via             string   `json:"via,omitempty"`
	ComputedHash    string   `json:"computedHash"`
	Subagents       []string `json:"subagents,omitempty"`
	WellKnownDigest string   `json:"wellKnownDigest,omitempty"`
	InstallMode     string   `json:"installMode,omitempty"`
	PluginName      string   `json:"pluginName,omitempty"`
}

// LocalSkillLockFile represents the local project lock file (skills-lock.json).
type LocalSkillLockFile struct {
	Version int                            `json:"version"`
	Skills  map[string]LocalSkillLockEntry `json:"skills"`
}

// SkillsBySourceGroup groups global locked skill names that share the same source.
type SkillsBySourceGroup struct {
	Skills []string
	Entry  SkillLockEntry
}

// SkillTreeFile represents a single text file inside a skill folder for skills.sh tree hashing.
type SkillTreeFile struct {
	Path     string
	Contents string
}

const (
	GlobalLockFile = ".skill-lock.json"
	LocalLockFile  = "skills-lock.json"
	GlobalLockVer  = 3
	LocalLockVer   = 1
)

func marshalPrettyJSON(v interface{}, trailingNewline bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if !trailingNewline {
		out = bytes.TrimSuffix(out, []byte("\n"))
	}
	return out, nil
}

// GetSkillLockPath returns the path to the global skill lock file.
func GetSkillLockPath() string {
	xdgStateHome := os.Getenv("XDG_STATE_HOME")
	if xdgStateHome != "" {
		return filepath.Join(xdgStateHome, "skills", GlobalLockFile)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agents", GlobalLockFile)
}

// GetLocalLockPath returns the path to the local skill lock file.
func GetLocalLockPath(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, LocalLockFile)
}

// GetGitHubToken returns an explicit GitHub token from GITHUB_TOKEN or GH_TOKEN, or "".
func GetGitHubToken() string {
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	return ""
}

// ComputeContentHash computes the SHA-256 hex digest of a string.
func ComputeContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ReadSkillLock reads the global skill lock file.
func ReadSkillLock() (SkillLockFile, error) {
	path := GetSkillLockPath()
	return readGlobalLockPath(path)
}

func readGlobalLockPath(path string) (SkillLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return createEmptyGlobalLock(), nil
	}
	var lock SkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return createEmptyGlobalLock(), nil
	}
	if lock.Version < GlobalLockVer || lock.Skills == nil {
		return createEmptyGlobalLock(), nil
	}
	return lock, nil
}

func createEmptyGlobalLock() SkillLockFile {
	return SkillLockFile{
		Version:   GlobalLockVer,
		Skills:    make(map[string]SkillLockEntry),
		Dismissed: &DismissedPrompts{},
	}
}

// WriteSkillLock writes the global skill lock file, returning any filesystem error (#2606).
func WriteSkillLock(lock SkillLockFile) error {
	path := GetSkillLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create lock directory %s: %w", filepath.Dir(path), err)
	}
	if lock.Skills == nil {
		lock.Skills = make(map[string]SkillLockEntry)
	}
	data, err := marshalPrettyJSON(lock, false)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write global skill lock %s: %w", path, err)
	}
	return nil
}

// AddSkillToLock adds or updates a skill in the global lock file.
func AddSkillToLock(skillName string, entry SkillLockEntry) error {
	lock, err := ReadSkillLock()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	existing, ok := lock.Skills[skillName]
	if ok && existing.InstalledAt != "" {
		entry.InstalledAt = existing.InstalledAt
	} else if entry.InstalledAt == "" {
		entry.InstalledAt = now
	}
	entry.UpdatedAt = now
	lock.Skills[skillName] = entry
	return WriteSkillLock(lock)
}

// RemoveSkillFromLock removes a skill from the global lock file.
func RemoveSkillFromLock(skillName string) (bool, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return false, err
	}
	if _, ok := lock.Skills[skillName]; !ok {
		return false, nil
	}
	delete(lock.Skills, skillName)
	if err := WriteSkillLock(lock); err != nil {
		return false, err
	}
	return true, nil
}

// GetSkillFromLock retrieves a single skill entry from the global lock file, or nil if absent.
func GetSkillFromLock(skillName string) (*SkillLockEntry, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return nil, err
	}
	entry, ok := lock.Skills[skillName]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

// GetAllLockedSkills returns all skill entries from the global lock file.
func GetAllLockedSkills() (map[string]SkillLockEntry, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return nil, err
	}
	return lock.Skills, nil
}

// GetSkillsBySource groups global locked skills by their source identifier.
func GetSkillsBySource() (map[string]SkillsBySourceGroup, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]SkillsBySourceGroup)
	keys := make([]string, 0, len(lock.Skills))
	for k := range lock.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, skillName := range keys {
		entry := lock.Skills[skillName]
		if existing, ok := bySource[entry.Source]; ok {
			existing.Skills = append(existing.Skills, skillName)
			bySource[entry.Source] = existing
		} else {
			bySource[entry.Source] = SkillsBySourceGroup{
				Skills: []string{skillName},
				Entry:  entry,
			}
		}
	}
	return bySource, nil
}

// IsPromptDismissed checks whether a prompt key has been marked dismissed in the global lock.
func IsPromptDismissed(promptKey string) bool {
	lock, err := ReadSkillLock()
	if err != nil || lock.Dismissed == nil {
		return false
	}
	if promptKey == "findSkillsPrompt" {
		return lock.Dismissed.FindSkillsPrompt
	}
	return false
}

// DismissPrompt records a dismissed prompt in the global lock file.
func DismissPrompt(promptKey string) error {
	lock, err := ReadSkillLock()
	if err != nil {
		return err
	}
	if lock.Dismissed == nil {
		lock.Dismissed = &DismissedPrompts{}
	}
	if promptKey == "findSkillsPrompt" {
		lock.Dismissed.FindSkillsPrompt = true
	}
	return WriteSkillLock(lock)
}

// GetLastSelectedAgents returns the last selected agents stored in the global lock file.
func GetLastSelectedAgents() ([]string, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return nil, err
	}
	return lock.LastSelectedAgents, nil
}

// SaveSelectedAgents persists the selected agents list in the global lock file.
func SaveSelectedAgents(agents []string) error {
	lock, err := ReadSkillLock()
	if err != nil {
		return err
	}
	lock.LastSelectedAgents = agents
	return WriteSkillLock(lock)
}

// ReadLocalLock reads the local project lock file (skills-lock.json).
// Returns an empty lock when the file does not exist; returns an error wrapping
// ErrInvalidLocalLock when the file exists on disk but is malformed or unsupported.
func ReadLocalLock(cwd string) (LocalSkillLockFile, error) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	path := GetLocalLockPath(cwd)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createEmptyLocalLock(), nil
		}
		return LocalSkillLockFile{}, err
	}

	var rawShape map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawShape); err != nil {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: %v", ErrInvalidLocalLock, path, err)
	}
	if rawShape == nil {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: expected an object with \"version\" and \"skills\"", ErrInvalidLocalLock, path)
	}
	verRaw, hasVer := rawShape["version"]
	skillsRaw, hasSkills := rawShape["skills"]
	if !hasVer || !hasSkills {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: expected an object with \"version\" and \"skills\"", ErrInvalidLocalLock, path)
	}

	var versionNum float64
	if err := json.Unmarshal(verRaw, &versionNum); err != nil || versionNum != float64(int(versionNum)) {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: expected numeric \"version\"", ErrInvalidLocalLock, path)
	}

	var skillsObj map[string]json.RawMessage
	if err := json.Unmarshal(skillsRaw, &skillsObj); err != nil || skillsObj == nil {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: expected an object with \"version\" and \"skills\"", ErrInvalidLocalLock, path)
	}

	var lock LocalSkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: %v", ErrInvalidLocalLock, path, err)
	}

	if lock.Version != LocalLockVer {
		return LocalSkillLockFile{}, fmt.Errorf("%w: Invalid %s: unsupported lock version %d (expected %d)", ErrInvalidLocalLock, path, lock.Version, LocalLockVer)
	}
	if lock.Skills == nil {
		lock.Skills = make(map[string]LocalSkillLockEntry)
	}

	for name, entry := range lock.Skills {
		if entry.SourceType == "local" && entry.Source != "" && !filepath.IsAbs(entry.Source) {
			entry.Source = filepath.Clean(filepath.Join(cwd, filepath.FromSlash(entry.Source)))
			lock.Skills[name] = entry
		}
	}

	return lock, nil
}

func createEmptyLocalLock() LocalSkillLockFile {
	return LocalSkillLockFile{
		Version: LocalLockVer,
		Skills:  make(map[string]LocalSkillLockEntry),
	}
}

func getPortableLocalSource(source, lockDir string) string {
	absLockDir, err := filepath.Abs(lockDir)
	if err != nil {
		absLockDir = filepath.Clean(lockDir)
	}
	var absSource string
	if filepath.IsAbs(source) {
		absSource = filepath.Clean(source)
	} else {
		absSource = filepath.Clean(filepath.Join(absLockDir, filepath.FromSlash(source)))
	}

	rel, err := filepath.Rel(absLockDir, absSource)
	if err != nil || filepath.IsAbs(rel) {
		return filepath.ToSlash(absSource)
	}

	portable := filepath.ToSlash(rel)
	if portable == "" || portable == "." {
		return "."
	}
	if portable == ".." || strings.HasPrefix(portable, "../") {
		return portable
	}
	return "./" + portable
}

// WriteLocalLock writes the local project lock file with deterministically sorted skill keys
// and trailing newline, returning an error if writing fails (#2606).
func WriteLocalLock(lock LocalSkillLockFile, cwd string) error {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	path := GetLocalLockPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", filepath.Dir(path), err)
	}

	normalizedSkills := make(map[string]LocalSkillLockEntry, len(lock.Skills))
	for k, entry := range lock.Skills {
		if entry.SourceType == "local" && entry.Source != "" {
			entry.Source = getPortableLocalSource(entry.Source, cwd)
		}
		normalizedSkills[k] = entry
	}

	outLock := LocalSkillLockFile{
		Version: lock.Version,
		Skills:  normalizedSkills,
	}
	if outLock.Version == 0 {
		outLock.Version = LocalLockVer
	}

	data, err := marshalPrettyJSON(outLock, true)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write local lock %s: %w", path, err)
	}
	return nil
}

// AddSkillToLocalLock adds or updates a skill in the local project lock file.
func AddSkillToLocalLock(skillName string, entry LocalSkillLockEntry, cwd string) error {
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return err
	}
	lock.Skills[skillName] = entry
	return WriteLocalLock(lock, cwd)
}

// RemoveSkillFromLocalLock removes a skill from the local project lock file.
func RemoveSkillFromLocalLock(skillName string, cwd string) (bool, error) {
	lock, err := ReadLocalLock(cwd)
	if err != nil {
		return false, err
	}
	if _, ok := lock.Skills[skillName]; !ok {
		return false, nil
	}
	delete(lock.Skills, skillName)
	if err := WriteLocalLock(lock, cwd); err != nil {
		return false, err
	}
	return true, nil
}

// LockChangeReason explains why recording entry for skillName would mutate skills-lock.json,
// or returns "" when it matches the existing locked entry (used for --frozen-lockfile).
func LockChangeReason(lock LocalSkillLockFile, skillName string, entry LocalSkillLockEntry) string {
	locked, ok := lock.Skills[skillName]
	if !ok {
		return fmt.Sprintf("%s is not in skills-lock.json", skillName)
	}
	if locked.ComputedHash != entry.ComputedHash {
		return fmt.Sprintf("%s has changed since skills-lock.json was written", skillName)
	}
	if locked.Version != entry.Version {
		newVer := entry.Version
		if newVer == "" {
			newVer = "unknown"
		}
		oldVer := locked.Version
		if oldVer == "" {
			oldVer = "unknown"
		}
		return fmt.Sprintf("%s comes from version %s, skills-lock.json has %s", skillName, newVer, oldVer)
	}
	return ""
}

// FrozenLockfileError formats a --frozen-lockfile drift failure message matching upstream.
func FrozenLockfileError(changes []string) error {
	lines := make([]string, 0, len(changes)+2)
	lines = append(lines, "Cannot install with --frozen-lockfile because skills-lock.json is not up to date:")
	for _, c := range changes {
		lines = append(lines, "  "+c)
	}
	lines = append(lines, "Run again with --no-frozen-lockfile to update it.")
	return errors.New(strings.Join(lines, "\n"))
}

// ComputeSkillFolderHash computes a SHA-256 hash from all regular files in a skill directory.
func ComputeSkillFolderHash(skillDir string) (string, error) {
	var files []struct {
		relPath string
		content []byte
	}
	err := filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files = append(files, struct {
			relPath string
			content []byte
		}{rel, content})
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})

	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.relPath))
		h.Write(f.content)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var skillsShBinaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true,
	".mp4": true, ".mov": true, ".mp3": true, ".wav": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".wasm": true,
	".parquet": true, ".arrow": true, ".pickle": true, ".pkl": true,
	".npy": true, ".npz": true, ".model": true, ".onnx": true,
	".pt": true, ".pth": true, ".h5": true, ".hdf5": true, ".safetensors": true,
}

var skillsShExcludedSegments = map[string]bool{
	".git":         true,
	"node_modules": true,
	".next":        true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
}

func skillsShExtname(slashPath string) string {
	lower := strings.ToLower(slashPath)
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		lower = lower[idx+1:]
	}
	dot := strings.LastIndex(lower, ".")
	if dot == -1 {
		return ""
	}
	return lower[dot:]
}

func utf16CodeUnitLess(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	n := len(ua)
	if len(ub) < n {
		n = len(ub)
	}
	for i := 0; i < n; i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func collectSkillsShDir(root, dir string, out *[]SkillTreeFile) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if !skillsShExcludedSegments[entry.Name()] {
				if err := collectSkillsShDir(root, full, out); err != nil {
					return err
				}
			}
		} else if info.Mode().IsRegular() {
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			slashRel := filepath.ToSlash(rel)
			if skillsShBinaryExtensions[skillsShExtname(slashRel)] {
				continue
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				return err
			}
			*out = append(*out, SkillTreeFile{
				Path:     slashRel,
				Contents: string(raw),
			})
		}
	}
	return nil
}

// CollectSkillsShFiles collects text files in dir as skills.sh reads them (skipping symlinks,
// binary extensions, and excluded build/dependency directories), matching src/skills-sh-hash.ts.
func CollectSkillsShFiles(dir string) ([]SkillTreeFile, error) {
	files := []SkillTreeFile{}
	if err := collectSkillsShDir(dir, dir, &files); err != nil {
		return nil, err
	}
	return files, nil
}

// ComputeSkillsShTreeHashFromFiles computes the unambiguous skills-sh-tree-v2 SHA-256 tree hash
// matching computeSkillsShTreeHash in upstream src/skills-sh-hash.ts.
func ComputeSkillsShTreeHashFromFiles(files []SkillTreeFile) string {
	sorted := make([]SkillTreeFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return utf16CodeUnitLess(sorted[i].Path, sorted[j].Path)
	})

	h := sha256.New()
	fmt.Fprintf(h, "skills-sh-tree-v2\n%d\n", len(sorted))
	for _, f := range sorted {
		p := []byte(f.Path)
		c := []byte(f.Contents)
		fmt.Fprintf(h, "%d\n", len(p))
		h.Write(p)
		fmt.Fprintf(h, "%d\n", len(c))
		h.Write(c)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeSkillsShTreeHash reads dir via CollectSkillsShFiles and returns its skills-sh-tree-v2 hash.
func ComputeSkillsShTreeHash(dir string) (string, error) {
	files, err := CollectSkillsShFiles(dir)
	if err != nil {
		return "", err
	}
	return ComputeSkillsShTreeHashFromFiles(files), nil
}

// ComputeSkillTreeHash computes the skills.sh tree hash (skills-sh-tree-v2) for dir.
func ComputeSkillTreeHash(dir string) (string, error) {
	return ComputeSkillsShTreeHash(dir)
}

func gitBlobSHA1(data []byte) [20]byte {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out
}

func computeGitTreeSHA1Raw(dir string) ([20]byte, bool, error) {
	var zero [20]byte
	entries, err := os.ReadDir(dir)
	if err != nil {
		return zero, false, err
	}

	type treeItem struct {
		mode    string
		name    string
		sortKey string
		sha     [20]byte
	}
	var items []treeItem

	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := os.Lstat(full)
		if err != nil {
			return zero, false, err
		}
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return zero, false, err
			}
			items = append(items, treeItem{
				mode:    "120000",
				name:    name,
				sortKey: name,
				sha:     gitBlobSHA1([]byte(filepath.ToSlash(target))),
			})
		case info.IsDir():
			subSHA, nonEmpty, err := computeGitTreeSHA1Raw(full)
			if err != nil {
				return zero, false, err
			}
			if !nonEmpty {
				continue
			}
			items = append(items, treeItem{
				mode:    "40000",
				name:    name,
				sortKey: name + "/",
				sha:     subSHA,
			})
		case mode.IsRegular():
			data, err := os.ReadFile(full)
			if err != nil {
				return zero, false, err
			}
			gitMode := "100644"
			if mode&0111 != 0 {
				gitMode = "100755"
			}
			items = append(items, treeItem{
				mode:    gitMode,
				name:    name,
				sortKey: name,
				sha:     gitBlobSHA1(data),
			})
		}
	}

	if len(items) == 0 && dir != "" {
		// Empty tree object in Git is `tree 0\x00` when at root, or omitted as a subdirectory.
		h := sha1.New()
		h.Write([]byte("tree 0\x00"))
		var emptySHA [20]byte
		copy(emptySHA[:], h.Sum(nil))
		return emptySHA, false, nil
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].sortKey < items[j].sortKey
	})

	var body bytes.Buffer
	for _, it := range items {
		body.WriteString(it.mode)
		body.WriteByte(' ')
		body.WriteString(it.name)
		body.WriteByte(0)
		body.Write(it.sha[:])
	}

	h := sha1.New()
	fmt.Fprintf(h, "tree %d\x00", body.Len())
	h.Write(body.Bytes())
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out, true, nil
}

// ComputeGitTreeHash computes the Git tree SHA-1 hex digest of dir in pure Go,
// matching Git's native `git write-tree` / GitHub Trees API folder SHA.
func ComputeGitTreeHash(dir string) (string, error) {
	sha, _, err := computeGitTreeSHA1Raw(dir)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sha[:]), nil
}

var gitSHA1HexRegex = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// GetGitRepoTreeHash resolves HEAD:<folderPath> via git rev-parse in a cloned repository,
// matching getGitTreeHash(repoDir, skillPath) in upstream src/git.ts.
func GetGitRepoTreeHash(repoDir, skillPath string) string {
	normalized := strings.ReplaceAll(skillPath, "\\", "/")
	segments := strings.Split(normalized, "/")
	if len(segments) > 0 {
		segments = segments[:len(segments)-1]
	}
	folderPath := strings.Join(segments, "/")
	rev := "HEAD^{tree}"
	if folderPath != "" {
		rev = "HEAD:" + folderPath
	}

	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "--end-of-options", rev)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	hash := strings.TrimSpace(string(out))
	if !gitSHA1HexRegex.MatchString(hash) {
		return ""
	}
	return strings.ToLower(hash)
}

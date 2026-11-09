package skl

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrInvalidLocalLock is returned when skills-lock.json exists on disk but is malformed,
// contains merge conflicts, or uses an unsupported schema version.
var ErrInvalidLocalLock = errors.New("SKILLS_INVALID_LOCAL_LOCK")

// SkillLockEntry represents a single installed skill entry in the global lock file (.skill-lock.json).
// Field order is the JSON key order upstream writes: addSkillToLock spreads the caller's entry
// ({source, sourceType, sourceUrl, ref, skillPath, skillFolderHash, pluginName, sourceBaseUrl,
// wellKnownDigest, installMode}) and then appends installedAt and updatedAt, so the timestamps
// always come last.
type SkillLockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceUrl       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	PluginName      string `json:"pluginName,omitempty"`
	SourceBaseUrl   string `json:"sourceBaseUrl,omitempty"`
	WellKnownDigest string `json:"wellKnownDigest,omitempty"`
	InstallMode     string `json:"installMode,omitempty"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// lockTimestampFormat matches JavaScript's Date.prototype.toISOString() (UTC, milliseconds).
const lockTimestampFormat = "2006-01-02T15:04:05.000Z"

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

	// orig is the file as read from disk. WriteSkillLock uses it to keep what upstream's
	// JSON.parse/JSON.stringify round trip keeps: unknown top-level and per-entry fields,
	// key order, and the existing order of skills.
	orig *globalLockOrig
}

// globalLockOrig records the parsed on-disk global lock alongside its typed decoding.
type globalLockOrig struct {
	top    *jsonObject
	skills map[string]SkillLockEntry
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

// marshalPrettyJSON serializes v the way JavaScript's JSON.stringify(v, null, 2) does: no
// HTML or U+2028/U+2029 escaping, ECMAScript number formatting, and integer-like object keys
// first. Struct fields keep their declaration order; Go map keys are sorted.
func marshalPrettyJSON(v interface{}, trailingNewline bool) ([]byte, error) {
	compact, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	parsed, err := parseOrderedJSON(compact)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeJSONStringify(&buf, parsed, "")
	if trailingNewline {
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
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
	if parsed, err := parseOrderedJSON(data); err == nil {
		if top, ok := parsed.(*jsonObject); ok {
			typed := make(map[string]SkillLockEntry, len(lock.Skills))
			for k, v := range lock.Skills {
				typed[k] = v
			}
			lock.orig = &globalLockOrig{top: top, skills: typed}
		}
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

// origSkillsObject returns the on-disk "skills" object, or nil.
func (lock SkillLockFile) origSkillsObject() *jsonObject {
	if lock.orig == nil || lock.orig.top == nil {
		return nil
	}
	v, _ := lock.orig.top.get("skills")
	obj, _ := v.(*jsonObject)
	return obj
}

// orderedSkillNames returns the skill names in the order JSON.stringify writes them: names
// already in the file keep their position, new names are appended (sorted, for determinism),
// and integer-like names move to the front as JavaScript objects order them.
func (lock SkillLockFile) orderedSkillNames() []string {
	names := make([]string, 0, len(lock.Skills))
	seen := make(map[string]bool, len(lock.Skills))
	if obj := lock.origSkillsObject(); obj != nil {
		for _, k := range obj.keys {
			if _, ok := lock.Skills[k]; ok && !seen[k] {
				names = append(names, k)
				seen[k] = true
			}
		}
	}
	var added []string
	for k := range lock.Skills {
		if !seen[k] {
			added = append(added, k)
		}
	}
	sort.Strings(added)
	return jsOrderKeys(append(names, added...))
}

// globalLockJSON builds the ordered JSON document for lock, matching what upstream's
// writeSkillLock produces after a readSkillLock/mutate cycle.
func globalLockJSON(lock SkillLockFile) (*jsonObject, error) {
	known := map[string]interface{}{
		"version": json.Number(strconv.Itoa(lock.Version)),
	}

	skills := newJSONObject()
	origSkills := lock.origSkillsObject()
	for _, name := range lock.orderedSkillNames() {
		entry := lock.Skills[name]
		if origSkills != nil {
			if prev, ok := lock.orig.skills[name]; ok && reflect.DeepEqual(prev, entry) {
				if raw, ok := origSkills.get(name); ok {
					skills.set(name, raw)
					continue
				}
			}
		}
		v, err := toOrderedJSON(entry)
		if err != nil {
			return nil, err
		}
		skills.set(name, v)
	}
	known["skills"] = skills

	var origTop *jsonObject
	if lock.orig != nil {
		origTop = lock.orig.top
	}
	if lock.Dismissed != nil {
		dismissed := newJSONObject()
		if origTop != nil {
			if v, ok := origTop.get("dismissed"); ok {
				if obj, ok := v.(*jsonObject); ok {
					dismissed = obj.clone()
				}
			}
		}
		cur, _ := dismissed.get("findSkillsPrompt")
		if lock.Dismissed.FindSkillsPrompt && cur != true {
			dismissed.set("findSkillsPrompt", true)
		} else if !lock.Dismissed.FindSkillsPrompt && cur == true {
			dismissed.del("findSkillsPrompt")
		}
		known["dismissed"] = dismissed
	}
	if lock.LastSelectedAgents != nil {
		v, err := toOrderedJSON(lock.LastSelectedAgents)
		if err != nil {
			return nil, err
		}
		known["lastSelectedAgents"] = v
	}

	out := newJSONObject()
	if origTop != nil {
		for _, k := range origTop.keys {
			switch k {
			case "version", "skills", "dismissed", "lastSelectedAgents":
				if v, ok := known[k]; ok {
					out.set(k, v)
				}
			default:
				v, _ := origTop.get(k)
				out.set(k, v)
			}
		}
	}
	for _, k := range []string{"version", "skills", "dismissed", "lastSelectedAgents"} {
		if v, ok := known[k]; ok && !out.has(k) {
			out.set(k, v)
		}
	}
	return out, nil
}

// WriteSkillLock writes the global skill lock file, returning any filesystem error (#2606).
// Like upstream (JSON.stringify(lock, null, 2)), there is no trailing newline.
func WriteSkillLock(lock SkillLockFile) error {
	path := GetSkillLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create lock directory %s: %w", filepath.Dir(path), err)
	}
	if lock.Skills == nil {
		lock.Skills = make(map[string]SkillLockEntry)
	}
	doc, err := globalLockJSON(lock)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	writeJSONStringify(&buf, doc, "")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
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
	now := time.Now().UTC().Format(lockTimestampFormat)
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

// GetSkillsBySource groups global locked skills by their source identifier, iterating skills in
// lock-file order like upstream's Object.entries(lock.skills).
func GetSkillsBySource() (map[string]SkillsBySourceGroup, error) {
	lock, err := ReadSkillLock()
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]SkillsBySourceGroup)
	for _, skillName := range lock.orderedSkillNames() {
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

// ComputeSkillFolderHash computes a SHA-256 hash from all regular files in a skill directory,
// matching computeSkillFolderHash in upstream src/local-lock.ts: files are ordered with
// relativePath.localeCompare (ICU collation, see jsLocaleCompare), not byte order.
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

	sort.SliceStable(files, func(i, j int) bool {
		if c := jsLocaleCompare(files[i].relPath, files[j].relPath); c != 0 {
			return c < 0
		}
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
				Contents: decodeUTF8WHATWG(raw),
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

// --- JavaScript-compatible collation (String.prototype.localeCompare) ---

// icuRootASCIIOrder lists every non-ignorable ASCII character in ICU root collation order
// (CLDR root, tertiary strength, alternate=non-ignorable), the order Node's default
// localeCompare uses. Each uppercase letter shares the primary weight of the lowercase letter
// before it and differs only at the tertiary level (lowercase first). The other C0 controls
// and DEL are completely ignorable.
const icuRootASCIIOrder = "\t\n\v\f\r _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789" +
	"aAbBcCdDeEfFgGhHiIjJkKlLmMnNoOpPqQrRsStTuUvVwWxXyYzZ"

var icuASCIIPrimary, icuASCIITertiary [utf8.RuneSelf]uint16

func init() {
	var p uint16
	for i := 0; i < len(icuRootASCIIOrder); i++ {
		c := icuRootASCIIOrder[i]
		if c >= 'A' && c <= 'Z' {
			icuASCIIPrimary[c] = p
			icuASCIITertiary[c] = 1
			continue
		}
		p++
		icuASCIIPrimary[c] = p
	}
}

// collationWeights returns the primary and tertiary weights of r; ok is false when r is
// completely ignorable. Non-ASCII runes sort after all ASCII letters, by code point.
func collationWeights(r rune) (primary uint32, tertiary uint16, ok bool) {
	if r >= 0 && r < utf8.RuneSelf {
		p := icuASCIIPrimary[r]
		return uint32(p), icuASCIITertiary[r], p != 0
	}
	return 0x100 + uint32(r), 0, true
}

// nextCollationWeight advances *pos past ignorable runes in s and returns the weight of the
// next non-ignorable rune at the given level (0 = primary, 1 = tertiary).
func nextCollationWeight(s string, pos *int, level int) (uint32, bool) {
	for *pos < len(s) {
		r, n := utf8.DecodeRuneInString(s[*pos:])
		*pos += n
		p, t, ok := collationWeights(r)
		if !ok {
			continue
		}
		if level == 1 {
			return uint32(t), true
		}
		return p, true
	}
	return 0, false
}

// jsLocaleCompare returns the sign of JavaScript's a.localeCompare(b) under Node's default
// (en / ICU root) collator. It compares all primary weights first, then tertiary (case)
// weights. It is exact for ASCII strings, which covers skill file paths in practice; non-ASCII
// runes use a deterministic code-point fallback instead of full ICU data, keeping skl free of
// extra module dependencies. Strings that differ only in ignorable controls compare equal.
func jsLocaleCompare(a, b string) int {
	for level := 0; level < 2; level++ {
		i, j := 0, 0
		for {
			wa, oka := nextCollationWeight(a, &i, level)
			wb, okb := nextCollationWeight(b, &j, level)
			if !oka || !okb {
				if oka {
					return 1
				}
				if okb {
					return -1
				}
				break
			}
			if wa != wb {
				if wa < wb {
					return -1
				}
				return 1
			}
		}
	}
	return 0
}

// decodeUTF8WHATWG decodes b like Node's buffer.toString('utf8'): each maximal subpart of an
// ill-formed sequence becomes one U+FFFD (WHATWG Encoding Standard UTF-8 decoder). This
// differs from both string(b) and strings.ToValidUTF8.
func decodeUTF8WHATWG(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		need := 0
		lower, upper := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			} else if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			} else if c == 0xF4 {
				upper = 0x8F
			}
		default:
			sb.WriteString("\uFFFD")
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lower || b[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !ok {
			// The byte at j (if any) is not consumed; it starts the next sequence.
			sb.WriteString("\uFFFD")
			i = j
			continue
		}
		sb.Write(b[i:j])
		i = j
	}
	return sb.String()
}

// --- Order-preserving JSON compatible with JSON.parse / JSON.stringify ---

// jsonObject is a JSON object that keeps key insertion order. Values are nil, bool,
// json.Number, string, []interface{} or *jsonObject.
type jsonObject struct {
	keys []string
	vals map[string]interface{}
}

func newJSONObject() *jsonObject {
	return &jsonObject{vals: make(map[string]interface{})}
}

func (o *jsonObject) has(k string) bool {
	_, ok := o.vals[k]
	return ok
}

func (o *jsonObject) get(k string) (interface{}, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// set assigns k, keeping the position of an existing key (as JavaScript property assignment
// and JSON.parse with duplicate keys do).
func (o *jsonObject) set(k string, v interface{}) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsonObject) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// clone returns a shallow copy of o.
func (o *jsonObject) clone() *jsonObject {
	c := &jsonObject{keys: append([]string(nil), o.keys...), vals: make(map[string]interface{}, len(o.vals))}
	for k, v := range o.vals {
		c.vals[k] = v
	}
	return c
}

// parseOrderedJSON parses data into ordered values (see jsonObject).
func parseOrderedJSON(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrderedJSON(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("invalid JSON: unexpected data after top-level value")
	}
	return v, nil
}

func decodeOrderedJSON(dec *json.Decoder) (interface{}, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := newJSONObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := kt.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON object key %v", kt)
			}
			v, err := decodeOrderedJSON(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []interface{}{}
		for dec.More() {
			v, err := decodeOrderedJSON(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, fmt.Errorf("invalid JSON delimiter %v", delim)
}

// toOrderedJSON converts a Go value to ordered JSON values via encoding/json, so struct
// fields keep their declaration order and omitempty rules apply.
func toOrderedJSON(v interface{}) (interface{}, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return parseOrderedJSON(data)
}

// isJSArrayIndexKey reports whether k is a canonical array index ("0".."4294967294"), which
// JavaScript objects enumerate before all other string keys, in ascending numeric order.
func isJSArrayIndexKey(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < 4294967295
}

// jsOrderKeys returns keys in JavaScript property enumeration order: array-index keys first
// (ascending), then the remaining keys in insertion order.
func jsOrderKeys(keys []string) []string {
	var idx, rest []string
	for _, k := range keys {
		if isJSArrayIndexKey(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	if len(idx) == 0 {
		return keys
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 64)
		b, _ := strconv.ParseUint(idx[j], 10, 64)
		return a < b
	})
	return append(idx, rest...)
}

// writeJSONStringify writes v as JSON.stringify(v, null, 2) would, starting at indent.
func writeJSONStringify(buf *bytes.Buffer, v interface{}, indent string) {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(formatJSNumber(string(t)))
	case string:
		writeJSQuotedString(buf, t)
	case []interface{}:
		if len(t) == 0 {
			buf.WriteString("[]")
			return
		}
		inner := indent + "  "
		buf.WriteString("[\n")
		for i, item := range t {
			if i > 0 {
				buf.WriteString(",\n")
			}
			buf.WriteString(inner)
			writeJSONStringify(buf, item, inner)
		}
		buf.WriteString("\n" + indent + "]")
	case *jsonObject:
		if len(t.keys) == 0 {
			buf.WriteString("{}")
			return
		}
		inner := indent + "  "
		buf.WriteString("{\n")
		for i, k := range jsOrderKeys(t.keys) {
			if i > 0 {
				buf.WriteString(",\n")
			}
			buf.WriteString(inner)
			writeJSQuotedString(buf, k)
			buf.WriteString(": ")
			writeJSONStringify(buf, t.vals[k], inner)
		}
		buf.WriteString("\n" + indent + "}")
	default:
		buf.WriteString("null")
	}
}

// writeJSQuotedString quotes s like JSON.stringify: only '"', '\\' and C0 controls are escaped
// (U+2028, U+2029 and HTML characters are written verbatim).
func writeJSQuotedString(buf *bytes.Buffer, s string) {
	const hexDigits = "0123456789abcdef"
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hexDigits[r>>4])
				buf.WriteByte(hexDigits[r&0xF])
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// formatJSNumber formats a JSON number literal the way JavaScript's Number::toString does after
// JSON.parse (e.g. "1.0" -> "1", "1e2" -> "100", "1e21" -> "1e+21"). Out-of-range values become
// "null", as JSON.stringify writes Infinity.
func formatJSNumber(lit string) string {
	f, err := strconv.ParseFloat(lit, 64)
	if (err != nil && !errors.Is(err, strconv.ErrRange)) || math.IsInf(f, 0) || math.IsNaN(f) {
		return "null"
	}
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	ePos := strings.IndexByte(s, 'e')
	digits := strings.Replace(s[:ePos], ".", "", 1)
	exp, _ := strconv.Atoi(s[ePos+1:])
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	e := n - 1
	expSign := "+"
	if e < 0 {
		expSign = "-"
		e = -e
	}
	mant := digits[:1]
	if k > 1 {
		mant += "." + digits[1:]
	}
	return sign + mant + "e" + expSign + strconv.Itoa(e)
}

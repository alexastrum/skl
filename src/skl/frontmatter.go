package skl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const utf8BOM = "\uFEFF"

var (
	fmSetNameRe      = regexp.MustCompile(`^(\x{FEFF}?---(\r?\n))(?s:(.*?))(\r?\n---(?:\r?\n|$))`)
	fmLineSplitRe    = regexp.MustCompile(`\r?\n`)
	fmNameKeyRe      = regexp.MustCompile(`^name\s*:`)
	fmContinuationRe = regexp.MustCompile(`^[ \t]`)

	// YAML 1.2 Core Schema plain-scalar patterns matching upstream `yaml` v2 stringify({ name }).
	// yaml.v3 quotes YAML 1.1 booleans (yes/no/on/off/y/n), underscored numbers, binary/octal
	// variants, and ISO dates, and skips quoting >64-bit hex ints; matching YAML 1.2 keeps
	// rewritten SKILL.md bytes and computedHash identical to upstream (#2722).
	yaml12SafePlainRe = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]*$`)
	yaml12CoreTagRe   = regexp.MustCompile(`^(?:~|null|Null|NULL|true|True|TRUE|false|False|FALSE|[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+|[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
)

func formatYAML12NameLine(name string) string {
	if yaml12SafePlainRe.MatchString(name) {
		if yaml12CoreTagRe.MatchString(name) {
			return "name: " + strconv.Quote(name)
		}
		return "name: " + name
	}
	if marshaled, err := yaml.Marshal(map[string]string{"name": name}); err == nil {
		return strings.TrimRight(string(marshaled), "\r\n")
	}
	return "name: " + strconv.Quote(name)
}

// SetFrontmatterName returns raw with its frontmatter `name` set to name, replacing the
// existing value (including continuation lines of a multi-line scalar) or inserting it
// first. Everything outside the name line is preserved byte for byte. Without frontmatter,
// raw is returned unchanged (#2722).
func SetFrontmatterName(raw, name string) string {
	idx := fmSetNameRe.FindStringSubmatchIndex(raw)
	if idx == nil {
		return raw
	}
	wholeEnd := idx[1]
	open := raw[idx[2]:idx[3]]
	eol := raw[idx[4]:idx[5]]
	body := raw[idx[6]:idx[7]]
	closeDelim := raw[idx[8]:idx[9]]

	nameLine := formatYAML12NameLine(name)

	lines := fmLineSplitRe.Split(body, -1)
	pos := -1
	for i, line := range lines {
		if fmNameKeyRe.MatchString(line) {
			pos = i
			break
		}
	}
	if pos == -1 {
		lines = append([]string{nameLine}, lines...)
	} else {
		end := pos + 1
		for end < len(lines) && fmContinuationRe.MatchString(lines[end]) {
			end++
		}
		updated := make([]string, 0, len(lines)-(end-pos)+1)
		updated = append(updated, lines[:pos]...)
		updated = append(updated, nameLine)
		updated = append(updated, lines[end:]...)
		lines = updated
	}
	return open + strings.Join(lines, eol) + closeDelim + raw[wholeEnd:]
}

// ParseFrontmatter parses YAML frontmatter delimited by `---` from a raw markdown string.
// It matches upstream's /^\uFEFF?---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/ exactly: after an
// opening `---` line, the frontmatter ends at the first newline followed by `---`, even when the
// line continues (`----`, `--- # x`); at most one \r and one \n after that `---` are consumed.
// The closing `---` must follow a newline, so `---\n---\n` is not frontmatter (content returned
// raw), while `---\n\n---\n` is empty frontmatter.
// Leading UTF-8 BOM (\uFEFF) is stripped when matching frontmatter (#97640fb).
// JavaScript frontmatter (e.g. `---js`) is intentionally not supported to prevent RCE.
// It returns the parsed metadata map, the remaining markdown content, and any YAML parse error.
func ParseFrontmatter(raw string) (map[string]interface{}, string, error) {
	data := make(map[string]interface{})

	trimmedBOM := strings.TrimPrefix(raw, utf8BOM)

	// Opening delimiter must be exact "---" followed by \n or \r\n
	var afterOpen string
	if strings.HasPrefix(trimmedBOM, "---\r\n") {
		afterOpen = trimmedBOM[len("---\r\n"):]
	} else if strings.HasPrefix(trimmedBOM, "---\n") {
		afterOpen = trimmedBOM[len("---\n"):]
	} else {
		return data, raw, nil
	}

	// The lazy group ends at the first "\n---" (or "\r\n---", which starts one byte earlier).
	closeIdx := strings.Index(afterOpen, "\n---")
	if closeIdx < 0 {
		return data, raw, nil
	}
	fmEnd := closeIdx
	if fmEnd > 0 && afterOpen[fmEnd-1] == '\r' {
		fmEnd--
	}
	frontmatterText := afterOpen[:fmEnd]
	remainingContent := afterOpen[closeIdx+len("\n---"):]
	remainingContent = strings.TrimPrefix(remainingContent, "\r")
	remainingContent = strings.TrimPrefix(remainingContent, "\n")

	normalizedFM := strings.ReplaceAll(frontmatterText, "\r\n", "\n")
	if strings.TrimSpace(normalizedFM) == "" {
		return data, remainingContent, nil
	}

	if err := yaml.Unmarshal([]byte(normalizedFM), &data); err != nil {
		return make(map[string]interface{}), remainingContent, err
	}

	if data == nil {
		data = make(map[string]interface{})
	}

	return data, remainingContent, nil
}

// ValidateSkillMetadata validates and normalizes `name` and `description` from parsed frontmatter (#2473).
// Both fields must exist, be YAML strings, and remain non-empty after terminal-escape stripping
// and whitespace normalization via SanitizeMetadata.
func ValidateSkillMetadata(data map[string]interface{}) (string, string, error) {
	if data == nil {
		return "", "", errors.New("missing required frontmatter field(s): name, description")
	}

	rawName, hasName := data["name"]
	rawDesc, hasDesc := data["description"]

	var missing []string
	if !hasName || rawName == nil || rawName == "" {
		missing = append(missing, "name")
	}
	if !hasDesc || rawDesc == nil || rawDesc == "" {
		missing = append(missing, "description")
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("missing required frontmatter field(s): %s", strings.Join(missing, ", "))
	}

	nameStr, nameOk := rawName.(string)
	descStr, descOk := rawDesc.(string)
	if !nameOk || !descOk {
		return "", "", fmt.Errorf("frontmatter \"name\" and \"description\" must be strings (got %T and %T)", rawName, rawDesc)
	}

	name := SanitizeMetadata(nameStr)
	desc := SanitizeMetadata(descStr)

	var empty []string
	if name == "" {
		empty = append(empty, "name")
	}
	if desc == "" {
		empty = append(empty, "description")
	}
	if len(empty) > 0 {
		return "", "", fmt.Errorf("empty required frontmatter field(s): %s", strings.Join(empty, ", "))
	}

	return name, desc, nil
}

// ShouldInstallInternalSkills reports whether INSTALL_INTERNAL_SKILLS enables internal skills.
func ShouldInstallInternalSkills() bool {
	v := os.Getenv("INSTALL_INTERNAL_SKILLS")
	return v == "1" || v == "true"
}

// ParseSkillMd reads and validates a SKILL.md file from disk, matching parseSkillMd in src/skills.ts.
// Returns (nil, nil) if the skill is marked `metadata.internal: true` and includeInternal is false
// (unless INSTALL_INTERNAL_SKILLS=1/true is set).
// A SKILL.md that is a symlink resolving outside its skill directory is refused with
// SKILLS_SYMLINK_OUTSIDE_SKILL instead of being read (skl hardening, matching the #2652 copy
// confinement; upstream reads through the link).
func ParseSkillMd(skillMdPath string, includeInternal bool) (*Skill, error) {
	if err := checkSkillMdConfined(skillMdPath); err != nil {
		return nil, err
	}
	rawBytes, err := os.ReadFile(skillMdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	raw := string(rawBytes)

	data, _, err := ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	name, description, err := ValidateSkillMetadata(data)
	if err != nil {
		return nil, err
	}

	var metadata map[string]interface{}
	if m, ok := data["metadata"].(map[string]interface{}); ok {
		metadata = m
	}

	if IsInternalSkill(metadata) && !includeInternal && !ShouldInstallInternalSkills() {
		return nil, nil
	}

	return &Skill{
		Name:        name,
		Description: description,
		Path:        filepath.Dir(skillMdPath),
		RawContent:  raw,
		Metadata:    metadata,
	}, nil
}

// IsInternalSkill reports whether a skill's metadata marks it as internal (#2702).
// Accepts boolean true as well as string "true" and "1" (agentskills.io specifies
// metadata as string -> string).
func IsInternalSkill(metadata interface{}) bool {
	if metadata == nil {
		return false
	}
	var internal interface{}
	switch m := metadata.(type) {
	case map[string]interface{}:
		internal = m["internal"]
	case map[string]string:
		internal = m["internal"]
	default:
		return false
	}
	switch v := internal.(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	default:
		return false
	}
}

// checkSkillMdConfined returns SKILLS_SYMLINK_OUTSIDE_SKILL when skillMdPath is a symlink whose
// real path is outside the real path of its directory. Regular files, and links that stay inside
// the skill directory (including when the skill directory itself is a symlink), pass.
func checkSkillMdConfined(skillMdPath string) error {
	info, err := os.Lstat(skillMdPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return nil // missing files are reported by the read; regular files cannot escape
	}
	realDir, err := filepath.EvalSymlinks(filepath.Dir(skillMdPath))
	if err != nil {
		return nil
	}
	realFile, err := filepath.EvalSymlinks(skillMdPath)
	if err != nil {
		return nil // broken link: the read reports it
	}
	rel, err := filepath.Rel(realDir, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return NewDiagnostic(CodeSymlinkOutsideSkill,
			fmt.Sprintf("Skipping symlink that points outside the skill directory: %s", skillMdPath), "")
	}
	return nil
}

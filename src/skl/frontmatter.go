package skl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const utf8BOM = "\uFEFF"

// ParseFrontmatter parses YAML frontmatter delimited by `---` from a raw markdown string.
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

	// Scan line-by-line for the closing "---" delimiter while preserving exact content bytes after it.
	var frontmatterText string
	var remainingContent string
	foundClose := false

	pos := 0
	for pos <= len(afterOpen) {
		lineEnd := strings.IndexByte(afterOpen[pos:], '\n')
		var line string
		var nextPos int
		if lineEnd == -1 {
			line = afterOpen[pos:]
			nextPos = len(afterOpen) + 1
		} else {
			line = afterOpen[pos : pos+lineEnd]
			nextPos = pos + lineEnd + 1
		}

		cleanLine := strings.TrimSuffix(line, "\r")
		if cleanLine == "---" {
			frontmatterText = afterOpen[:pos]
			if nextPos <= len(afterOpen) {
				remainingContent = afterOpen[nextPos:]
			} else {
				remainingContent = ""
			}
			foundClose = true
			break
		}

		if lineEnd == -1 {
			break
		}
		pos = nextPos
	}

	if !foundClose {
		return data, raw, nil
	}

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
func ParseSkillMd(skillMdPath string, includeInternal bool) (*Skill, error) {
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

	if metadata != nil {
		if internal, ok := metadata["internal"].(bool); ok && internal {
			if !includeInternal && !ShouldInstallInternalSkills() {
				return nil, nil
			}
		}
	}

	return &Skill{
		Name:        name,
		Description: description,
		Path:        filepath.Dir(skillMdPath),
		RawContent:  raw,
		Metadata:    metadata,
	}, nil
}

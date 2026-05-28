package skl

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFrontmatter parses YAML frontmatter delimited by `---` from a raw markdown string.
// It returns the parsed metadata map, the remaining markdown content, and any error encountered.
func ParseFrontmatter(raw string) (map[string]interface{}, string, error) {
	data := make(map[string]interface{})

	// Normalize line endings
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")

	lines := strings.Split(normalized, "\n")
	if len(lines) < 2 || lines[0] != "---" {
		return data, raw, nil
	}

	closingLineIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closingLineIdx = i
			break
		}
	}

	if closingLineIdx == -1 {
		return data, raw, nil
	}

	frontmatterText := strings.Join(lines[1:closingLineIdx], "\n")
	remainingContent := strings.Join(lines[closingLineIdx+1:], "\n")

	if strings.TrimSpace(frontmatterText) == "" {
		return data, remainingContent, nil
	}

	if err := yaml.Unmarshal([]byte(frontmatterText), &data); err != nil {
		return data, remainingContent, err
	}

	if data == nil {
		data = make(map[string]interface{})
	}

	return data, remainingContent, nil
}


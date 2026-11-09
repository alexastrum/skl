package skl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantData    map[string]interface{}
		wantContent string
		wantErr     bool
	}{
		{
			name: "Standard frontmatter LF",
			input: `---
name: my-skill
description: A description
---
# Content
`,
			wantData: map[string]interface{}{
				"name":        "my-skill",
				"description": "A description",
			},
			wantContent: "# Content\n",
			wantErr:     false,
		},
		{
			name:        "CRLF frontmatter",
			input:       "---\r\nname: example\r\ndescription: Example skill\r\n---\r\n# Body\r\n",
			wantData:    map[string]interface{}{"name": "example", "description": "Example skill"},
			wantContent: "# Body\r\n",
			wantErr:     false,
		},
		{
			name:        "UTF-8 BOM and LF (#97640fb)",
			input:       "\uFEFF---\nname: example\ndescription: Example skill\n---\n# Body\n",
			wantData:    map[string]interface{}{"name": "example", "description": "Example skill"},
			wantContent: "# Body\n",
			wantErr:     false,
		},
		{
			name:        "UTF-8 BOM and CRLF (#97640fb)",
			input:       "\uFEFF---\r\nname: example\r\ndescription: Example skill\r\n---\r\n# Body\r\n",
			wantData:    map[string]interface{}{"name": "example", "description": "Example skill"},
			wantContent: "# Body\r\n",
			wantErr:     false,
		},
		{
			name: "Frontmatter with extra whitespace and fields",
			input: `---
name: test-skill
description: "another description"
pluginName: my-plugin
metadata:
  key: value
---

# Title
Body
`,
			wantData: map[string]interface{}{
				"name":        "test-skill",
				"description": "another description",
				"pluginName":  "my-plugin",
				"metadata": map[string]interface{}{
					"key": "value",
				},
			},
			wantContent: "\n# Title\nBody\n",
			wantErr:     false,
		},
		{
			name:        "No frontmatter",
			input:       "# Just content\nNo YAML here",
			wantData:    map[string]interface{}{},
			wantContent: "# Just content\nNo YAML here",
			wantErr:     false,
		},
		{
			name:        "BOM without frontmatter preserved",
			input:       "\uFEFF# Body\n",
			wantData:    map[string]interface{}{},
			wantContent: "\uFEFF# Body\n",
			wantErr:     false,
		},
		{
			name:        "JavaScript frontmatter unsupported",
			input:       "---js\n({ name: \"example\", description: \"Example skill\" })\n---\n# Body",
			wantData:    map[string]interface{}{},
			wantContent: "---js\n({ name: \"example\", description: \"Example skill\" })\n---\n# Body",
			wantErr:     false,
		},
		{
			name:        "BOM + JavaScript frontmatter unsupported",
			input:       "\uFEFF---js\n({ name: \"example\", description: \"Example skill\" })\n---\n# Body",
			wantData:    map[string]interface{}{},
			wantContent: "\uFEFF---js\n({ name: \"example\", description: \"Example skill\" })\n---\n# Body",
			wantErr:     false,
		},
		{
			name: "Missing closing delimiter",
			input: `---
name: unclosed
description: oops
# Just content
`,
			wantData:    map[string]interface{}{},
			wantContent: "---\nname: unclosed\ndescription: oops\n# Just content\n",
			wantErr:     false,
		},
		{
			name: "Empty frontmatter",
			input: `---
---
# Content
`,
			wantData:    map[string]interface{}{},
			wantContent: "# Content\n",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotData, gotContent, err := ParseFrontmatter(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseFrontmatter() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(gotData, tt.wantData) {
				t.Errorf("ParseFrontmatter() gotData = %v, want %v", gotData, tt.wantData)
			}
			if gotContent != tt.wantContent {
				t.Errorf("ParseFrontmatter() gotContent = %q, want %q", gotContent, tt.wantContent)
			}
		})
	}
}

func TestValidateSkillMetadataAndParseSkillMd(t *testing.T) {
	// Valid metadata with multiline / ANSI stripped
	name, desc, err := ValidateSkillMetadata(map[string]interface{}{
		"name":        "  \x1b[32mmy-skill\x1b[0m  ",
		"description": "Line 1\nLine 2",
	})
	if err != nil || name != "my-skill" || desc != "Line 1 Line 2" {
		t.Fatalf("ValidateSkillMetadata valid = (%q, %q, %v)", name, desc, err)
	}

	// Missing fields
	if _, _, err := ValidateSkillMetadata(map[string]interface{}{"name": "only-name"}); err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("expected missing description error, got %v", err)
	}

	// Non-string fields (e.g. YAML boolean or integer)
	if _, _, err := ValidateSkillMetadata(map[string]interface{}{"name": 123, "description": true}); err == nil || !strings.Contains(err.Error(), "must be strings") {
		t.Errorf("expected non-string error, got %v", err)
	}

	// Whitespace-only or ANSI-only after normalization (#2473)
	for _, bad := range []map[string]interface{}{
		{"name": "   \t\n  ", "description": "valid"},
		{"name": "valid", "description": "  \r\n\t "},
		{"name": "\x1b[31m\x1b[0m", "description": "valid"},
	} {
		if _, _, err := ValidateSkillMetadata(bad); err == nil || !strings.Contains(err.Error(), "empty required frontmatter") {
			t.Errorf("expected empty required frontmatter error for %+v, got %v", bad, err)
		}
	}

	// ParseSkillMd with BOM + internal skill handling
	dir := t.TempDir()
	bomSkillPath := filepath.Join(dir, "SKILL.md")
	raw := "\uFEFF---\r\nname: bom-skill\r\ndescription: Loaded with BOM\r\nmetadata:\r\n  internal: true\r\n---\r\n# Body\r\n"
	_ = os.WriteFile(bomSkillPath, []byte(raw), 0644)

	t.Setenv("INSTALL_INTERNAL_SKILLS", "")
	sk, err := ParseSkillMd(bomSkillPath, false)
	if err != nil || sk != nil {
		t.Errorf("expected internal skill skipped when includeInternal=false, got (%+v, %v)", sk, err)
	}

	sk2, err := ParseSkillMd(bomSkillPath, true)
	if err != nil || sk2 == nil || sk2.Name != "bom-skill" || sk2.RawContent != raw {
		t.Errorf("expected internal skill loaded when includeInternal=true, got (%+v, %v)", sk2, err)
	}

	t.Setenv("INSTALL_INTERNAL_SKILLS", "1")
	sk3, err := ParseSkillMd(bomSkillPath, false)
	if err != nil || sk3 == nil || sk3.Name != "bom-skill" {
		t.Errorf("expected internal skill loaded when INSTALL_INTERNAL_SKILLS=1, got (%+v, %v)", sk3, err)
	}
}

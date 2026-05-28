package skills

import (
	"reflect"
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
			name: "Standard frontmatter",
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

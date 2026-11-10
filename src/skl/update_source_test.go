package skl

import (
	"reflect"
	"testing"
)

func TestBuildLocalCloneSource(t *testing.T) {
	tests := []struct {
		e    UpdateSourceEntry
		want string
	}{
		{UpdateSourceEntry{Source: "owner/repo", SourceType: "github"}, "https://github.com/owner/repo.git"},
		{UpdateSourceEntry{Source: "owner/repo", SourceURL: "https://github.com/owner/repo.git", SourceType: "github"}, "https://github.com/owner/repo.git"},
		{UpdateSourceEntry{Source: "acme/skills", SourceURL: "https://gitlab.example.com/acme/skills.git", SourceType: "git"}, "https://gitlab.example.com/acme/skills.git"},
		{UpdateSourceEntry{Source: "acme/skills", SourceType: "git"}, ""},
	}
	for _, tt := range tests {
		if got := BuildLocalCloneSource(tt.e); got != tt.want {
			t.Errorf("BuildLocalCloneSource(%+v) = %q, want %q", tt.e, got, tt.want)
		}
	}
}

func TestFormatSourceInput(t *testing.T) {
	if got := FormatSourceInput("https://github.com/owner/repo.git", "feature/install"); got != "https://github.com/owner/repo.git#feature/install" {
		t.Error(got)
	}
	if got := FormatSourceInput("https://github.com/owner/repo.git", ""); got != "https://github.com/owner/repo.git" {
		t.Error(got)
	}
}

func TestBuildUpdateInstallSource(t *testing.T) {
	tests := []struct {
		name string
		e    UpdateSourceEntry
		want string
	}{
		{"root without trailing slash", UpdateSourceEntry{Source: "owner/repo", SourceURL: "https://github.com/owner/repo.git", Ref: "feature/install", SkillPath: "SKILL.md"}, "owner/repo#feature/install"},
		{"nested with ref", UpdateSourceEntry{Source: "owner/repo", SourceURL: "https://github.com/owner/repo.git", SourceType: "github", Ref: "feature/install", SkillPath: "skills/my-skill/SKILL.md"}, "owner/repo/skills/my-skill#feature/install"},
		{"non-GitHub uses sourceUrl", UpdateSourceEntry{Source: "acme/skills", SourceURL: "https://gitlab.example.com/acme/skills.git", SourceType: "git", Ref: "main", SkillPath: "skills/my-skill/SKILL.md"}, "https://gitlab.example.com/acme/skills.git#main"},
		{"non-GitHub missing sourceUrl fails closed", UpdateSourceEntry{Source: "acme/skills", SourceType: "git", SkillPath: "skills/my-skill/SKILL.md"}, ""},
		{"no skillPath falls back to sourceUrl", UpdateSourceEntry{Source: "owner/repo", SourceURL: "https://github.com/owner/repo.git", Ref: "feature/install"}, "https://github.com/owner/repo.git#feature/install"},
	}
	for _, tt := range tests {
		if got := BuildUpdateInstallSource(tt.e); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestBuildLocalUpdateSource(t *testing.T) {
	tests := []struct {
		name string
		e    UpdateSourceEntry
		want string
	}{
		{"folder with ref", UpdateSourceEntry{Source: "owner/repo", SourceType: "github", Ref: "main", SkillPath: "skills/my-skill/SKILL.md"}, "owner/repo/skills/my-skill#main"},
		{"folder without ref", UpdateSourceEntry{Source: "owner/repo", SourceType: "github", SkillPath: "skills/my-skill/SKILL.md"}, "owner/repo/skills/my-skill"},
		{"root skill", UpdateSourceEntry{Source: "owner/repo", SourceType: "github", SkillPath: "SKILL.md"}, "owner/repo"},
		{"no skillPath", UpdateSourceEntry{Source: "owner/repo", SourceType: "github", Ref: "main"}, "owner/repo#main"},
		{"ssh drops folder", UpdateSourceEntry{Source: "git@gitea.example.com:owner/repo.git", SkillPath: "skills/my-skill/SKILL.md"}, "git@gitea.example.com:owner/repo.git"},
		{"ssh keeps ref", UpdateSourceEntry{Source: "git@gitea.example.com:owner/repo.git", Ref: "main", SkillPath: "skills/my-skill/SKILL.md"}, "git@gitea.example.com:owner/repo.git#main"},
		{"ssh:// without .git", UpdateSourceEntry{Source: "ssh://git@gitea.example.com/owner/repo", SkillPath: "skills/my-skill/SKILL.md"}, "ssh://git@gitea.example.com/owner/repo"},
		{"self-hosted https .git", UpdateSourceEntry{Source: "https://gitea.example.com/owner/repo.git", SkillPath: "skills/my-skill/SKILL.md"}, "https://gitea.example.com/owner/repo.git"},
		{"github https appends", UpdateSourceEntry{Source: "https://github.com/owner/repo", SkillPath: "skills/my-skill/SKILL.md"}, "https://github.com/owner/repo/skills/my-skill"},
		{"self-hosted gitlab uses sourceUrl", UpdateSourceEntry{Source: "acme/skills", SourceURL: "https://gitlab.example.com/acme/skills.git", SourceType: "git", Ref: "main", SkillPath: "skills/my-skill/SKILL.md"}, "https://gitlab.example.com/acme/skills.git#main"},
		{"generic git shorthand fails closed", UpdateSourceEntry{Source: "acme/skills", SourceType: "git", SkillPath: "skills/my-skill/SKILL.md"}, ""},
		{"legacy gitlab shorthand fails closed", UpdateSourceEntry{Source: "acme/skills", SourceType: "gitlab", SkillPath: "skills/my-skill/SKILL.md"}, ""},
	}
	for _, tt := range tests {
		if got := BuildLocalUpdateSource(tt.e); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestShouldUseFullDepthForUpdate(t *testing.T) {
	deep := "plugins/example/skills/deep-skill/SKILL.md"
	tests := []struct {
		e    UpdateSourceEntry
		want bool
	}{
		{UpdateSourceEntry{Source: "git@gitea.example.com:owner/repo.git", SourceType: "git", SkillPath: deep}, true},
		{UpdateSourceEntry{Source: "ssh://git@gitea.example.com/owner/repo", SourceType: "git", SkillPath: deep}, true},
		{UpdateSourceEntry{Source: "owner/repo", SourceURL: "https://gitea.example.com/owner/repo.git", SourceType: "git", SkillPath: deep}, true},
		{UpdateSourceEntry{Source: "owner/repo", SourceType: "github", SkillPath: deep}, false},
		{UpdateSourceEntry{Source: "git@gitea.example.com:owner/repo.git", SourceType: "git"}, false},
	}
	for _, tt := range tests {
		if got := ShouldUseFullDepthForUpdate(tt.e); got != tt.want {
			t.Errorf("ShouldUseFullDepthForUpdate(%+v) = %v, want %v", tt.e, got, tt.want)
		}
	}
}

func TestResolveSkillLocations(t *testing.T) {
	const name = "swiftui-expert-skill"
	lock := func(p string) map[string]string { return map[string]string{name: p} }
	skillsPath := "skills/swiftui-expert-skill/SKILL.md"
	pluginsPath := "plugins/swiftui-expert-skill/SKILL.md"
	both := []DiscoveredSkillLocation{{name, skillsPath}, {name, pluginsPath}}

	tests := []struct {
		label     string
		names     []string
		lock      map[string]string
		found     []DiscoveredSkillLocation
		exact     bool
		resolved  map[string]string
		deleted   []string
		ambiguous []string
	}{
		{"unchanged exact path", []string{name}, lock(skillsPath), []DiscoveredSkillLocation{{name, skillsPath}}, false, map[string]string{name: skillsPath}, []string{}, []string{}},
		{"unique relocation", []string{name}, lock("swiftui-expert-skill/SKILL.md"), []DiscoveredSkillLocation{{name, skillsPath}}, false, map[string]string{name: skillsPath}, []string{}, []string{}},
		{"normalized names", []string{"SwiftUI Expert Skill"}, map[string]string{"SwiftUI Expert Skill": "old/location/SKILL.md"}, []DiscoveredSkillLocation{{"swiftui_expert_skill", "new/location/SKILL.md"}}, false, map[string]string{"SwiftUI Expert Skill": "new/location/SKILL.md"}, []string{}, []string{}},
		{"deleted", []string{name}, lock("swiftui-expert-skill/SKILL.md"), nil, false, map[string]string{}, []string{name}, []string{}},
		{"ambiguous relocation", []string{name}, lock("old/swiftui-expert-skill/SKILL.md"), both, false, map[string]string{}, []string{}, []string{name}},
		{"ambiguous even with exact match", []string{name}, lock(skillsPath), both, false, map[string]string{}, []string{}, []string{name}},
		{"exact path disambiguates", []string{name}, lock(skillsPath), both, true, map[string]string{name: skillsPath}, []string{}, []string{}},
		{"separator normalization", []string{name}, lock(`skills\swiftui-expert-skill\SKILL.md`), []DiscoveredSkillLocation{{"different-frontmatter-name", skillsPath}}, false, map[string]string{name: skillsPath}, []string{}, []string{}},
	}
	for _, tt := range tests {
		got := ResolveSkillLocations(tt.names, tt.lock, tt.found, tt.exact)
		if !reflect.DeepEqual(got.ResolvedPaths, tt.resolved) || !reflect.DeepEqual(got.DeletedSkills, tt.deleted) || !reflect.DeepEqual(got.AmbiguousSkills, tt.ambiguous) {
			t.Errorf("%s: got %+v", tt.label, got)
		}
	}
}

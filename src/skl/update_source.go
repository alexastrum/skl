package skl

import (
	"net/url"
	"regexp"
	"strings"
)

// UpdateSourceEntry is the subset of a global or project lock entry needed to
// rebuild an install source during update/restore (upstream update-source.ts).
type UpdateSourceEntry struct {
	Source     string
	SourceURL  string
	SourceType string
	Ref        string
	SkillPath  string
}

// UpdateSourceFromLocal converts a project lock entry.
func UpdateSourceFromLocal(e LocalSkillLockEntry) UpdateSourceEntry {
	return UpdateSourceEntry{Source: e.Source, SourceURL: e.SourceUrl, SourceType: e.SourceType, Ref: e.Ref, SkillPath: e.SkillPath}
}

// UpdateSourceFromGlobal converts a global lock entry.
func UpdateSourceFromGlobal(e SkillLockEntry) UpdateSourceEntry {
	return UpdateSourceEntry{Source: e.Source, SourceURL: e.SourceUrl, SourceType: e.SourceType, Ref: e.Ref, SkillPath: e.SkillPath}
}

// FormatSourceInput appends "#ref" to sourceURL when ref is set.
func FormatSourceInput(sourceURL, ref string) string {
	if ref == "" {
		return sourceURL
	}
	return sourceURL + "#" + ref
}

// deriveSkillFolder strips a trailing SKILL.md (and slash); "" for a root skill.
func deriveSkillFolder(skillPath string) string {
	folder := skillPath
	if strings.HasSuffix(folder, "/SKILL.md") {
		folder = strings.TrimSuffix(folder, "/SKILL.md")
	} else if strings.HasSuffix(folder, "SKILL.md") {
		folder = strings.TrimSuffix(folder, "SKILL.md")
	}
	return strings.TrimSuffix(folder, "/")
}

// SupportsAppendedSubpath reports whether a skill folder can be appended to source
// (owner/repo shorthand or an https URL on github.com/gitlab.com).
func SupportsAppendedSubpath(source string) bool {
	if strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "ssh://") || strings.HasSuffix(source, ".git") {
		return false
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		u, err := url.Parse(source)
		if err != nil {
			return false
		}
		host := u.Hostname()
		return host == "github.com" || host == "gitlab.com"
	}
	return true
}

func isBareShorthand(source string) bool {
	return !strings.Contains(source, ":") && !strings.HasPrefix(source, ".") && !strings.HasPrefix(source, "/")
}

// getLocalSource returns the entry's source, or "" when a legacy generic-Git or
// GitLab entry only kept an owner/repo shorthand (fail closed: never redirect it
// to GitHub).
func getLocalSource(e UpdateSourceEntry) string {
	if e.SourceURL != "" {
		return e.SourceURL
	}
	if (e.SourceType == "git" || e.SourceType == "gitlab") && isBareShorthand(e.Source) {
		return ""
	}
	return e.Source
}

// BuildLocalCloneSource returns a cloneable repository URL ("" if unresolvable).
func BuildLocalCloneSource(e UpdateSourceEntry) string {
	source := getLocalSource(e)
	if source == "" {
		return ""
	}
	if e.SourceType == "github" && isBareShorthand(source) {
		return "https://github.com/" + strings.TrimSuffix(source, ".git") + ".git"
	}
	return source
}

// ShouldUseFullDepthForUpdate reports whether an update must discover the skill
// with full depth because the source cannot target a subpath.
func ShouldUseFullDepthForUpdate(e UpdateSourceEntry) bool {
	if e.SkillPath == "" {
		return false
	}
	source := e.Source
	if e.SourceType != "" && e.SourceType != "github" {
		source = getLocalSource(e)
	}
	return source != "" && !SupportsAppendedSubpath(source)
}

func appendFolderAndRef(source, skillPath, ref string) string {
	if !SupportsAppendedSubpath(source) {
		return FormatSourceInput(source, ref)
	}
	withFolder := source
	if folder := deriveSkillFolder(skillPath); folder != "" {
		withFolder = source + "/" + folder
	}
	return FormatSourceInput(withFolder, ref)
}

// BuildUpdateInstallSource builds the `add` source used by a global update
// ("" when unresolvable).
func BuildUpdateInstallSource(e UpdateSourceEntry) string {
	nonGitHub := e.SourceType != "" && e.SourceType != "github"
	if e.SkillPath == "" {
		source := e.SourceURL
		if source == "" {
			source = e.Source
		}
		if nonGitHub {
			source = getLocalSource(e)
		}
		if source == "" {
			return ""
		}
		return FormatSourceInput(source, e.Ref)
	}
	source := e.Source
	if nonGitHub {
		source = getLocalSource(e)
	}
	if source == "" {
		return ""
	}
	return appendFolderAndRef(source, e.SkillPath, e.Ref)
}

// BuildLocalUpdateSource builds the `add` source used by project update/restore
// ("" for legacy generic-Git/GitLab shorthands without sourceUrl).
func BuildLocalUpdateSource(e UpdateSourceEntry) string {
	source := getLocalSource(e)
	if source == "" {
		return ""
	}
	if e.SkillPath == "" {
		return FormatSourceInput(source, e.Ref)
	}
	return appendFolderAndRef(source, e.SkillPath, e.Ref)
}

// DiscoveredSkillLocation is a skill found in a fresh checkout.
type DiscoveredSkillLocation struct {
	Name      string
	SkillPath string
}

// SkillLocationResolution is the result of ResolveSkillLocations.
type SkillLocationResolution struct {
	DeletedSkills   []string
	AmbiguousSkills []string
	ResolvedPaths   map[string]string
}

var (
	relocNameSep   = regexp.MustCompile(`[\s_]+`)
	relocPathSlash = regexp.MustCompile(`/+`)
)

func normalizeRelocName(name string) string {
	return relocNameSep.ReplaceAllString(strings.ToLower(name), "-")
}

func normalizeRelocPath(p string) string {
	return relocPathSlash.ReplaceAllString(strings.ReplaceAll(p, `\`, "/"), "/")
}

// ResolveSkillLocations matches locked skills to their current locations. An
// exact locked path wins when exactPathDisambiguates; a missing path relocates
// only to a single same-named candidate; multiple candidates fail closed
// (ambiguous); no candidate means the skill was deleted upstream.
func ResolveSkillLocations(lockedSkillNames []string, lockedPaths map[string]string, discovered []DiscoveredSkillLocation, exactPathDisambiguates bool) SkillLocationResolution {
	discoveredPaths := map[string]bool{}
	pathsByName := map[string][]string{}
	for _, d := range discovered {
		p := normalizeRelocPath(d.SkillPath)
		discoveredPaths[p] = true
		key := normalizeRelocName(d.Name)
		dup := false
		for _, existing := range pathsByName[key] {
			if existing == p {
				dup = true
				break
			}
		}
		if !dup {
			pathsByName[key] = append(pathsByName[key], p)
		}
	}
	res := SkillLocationResolution{DeletedSkills: []string{}, AmbiguousSkills: []string{}, ResolvedPaths: map[string]string{}}
	for _, name := range lockedSkillNames {
		locked := lockedPaths[name]
		if locked == "" {
			continue
		}
		normLocked := normalizeRelocPath(locked)
		candidates := pathsByName[normalizeRelocName(name)]
		exists := discoveredPaths[normLocked]
		switch {
		case exists && exactPathDisambiguates:
			res.ResolvedPaths[name] = normLocked
		case len(candidates) > 1:
			res.AmbiguousSkills = append(res.AmbiguousSkills, name)
		case exists:
			res.ResolvedPaths[name] = normLocked
		case len(candidates) == 1:
			res.ResolvedPaths[name] = candidates[0]
		default:
			res.DeletedSkills = append(res.DeletedSkills, name)
		}
	}
	return res
}

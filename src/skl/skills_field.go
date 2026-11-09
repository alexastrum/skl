package skl

import (
	"encoding/json"
	"strings"
)

// NpmSkillsRequest is a `npm:<package>` entry of a package.json "skills" field:
// the skills shipped by an installed package.
type NpmSkillsRequest struct {
	Package string
	// Skills are folder or sanitized skill names to keep; empty means all.
	Skills []string
}

// RemoteSkillsRequest is a git source entry of a package.json "skills" field.
type RemoteSkillsRequest struct {
	Parsed ParsedSource
	// Skills are skill names to keep; empty means all.
	Skills []string
}

// ParsedSkillsField is the result of ParseSkillsField.
type ParsedSkillsField struct {
	Npm    []NpmSkillsRequest
	Remote []RemoteSkillsRequest
	Errors []error
}

const npmSkillsPrefix = "npm:"

// skillsFieldEntry validates one raw entry: a string, or an object with a string
// "source", optional string-array "skills" and optional string "ref".
func skillsFieldEntry(raw interface{}) (source string, skills []string, ref *string, ok bool) {
	switch v := raw.(type) {
	case string:
		return v, nil, nil, true
	case map[string]interface{}:
		src, isStr := v["source"].(string)
		if !isStr {
			return "", nil, nil, false
		}
		if rawSkills, has := v["skills"]; has && rawSkills != nil {
			list, isList := rawSkills.([]interface{})
			if !isList {
				return "", nil, nil, false
			}
			for _, item := range list {
				s, isS := item.(string)
				if !isS {
					return "", nil, nil, false
				}
				skills = append(skills, s)
			}
		}
		if rawRef, has := v["ref"]; has && rawRef != nil {
			r, isS := rawRef.(string)
			if !isS {
				return "", nil, nil, false
			}
			ref = &r
		}
		return src, skills, ref, true
	}
	return "", nil, nil, false
}

// ParseSkillsField parses the entries of declarer's package.json "skills" field
// (skills-npm grammar, upstream src/skills-field.ts). Problems are returned as
// diagnostics, never thrown.
func ParseSkillsField(entries []interface{}, declarer string) ParsedSkillsField {
	var out ParsedSkillsField
	for _, raw := range entries {
		source, skills, ref, ok := skillsFieldEntry(raw)
		if !ok {
			encoded, _ := json.Marshal(raw)
			out.Errors = append(out.Errors, ErrPackageSkillsInvalidEntry(declarer, string(encoded)))
			continue
		}
		if strings.HasPrefix(source, npmSkillsPrefix) {
			if ref != nil {
				out.Errors = append(out.Errors, ErrPackageSkillsRefNotAllowed(declarer, source))
				continue
			}
			out.Npm = append(out.Npm, NpmSkillsRequest{Package: strings.TrimPrefix(source, npmSkillsPrefix), Skills: skills})
			continue
		}
		parsed, err := ParseSource(source)
		if err != nil || (parsed.Type != "github" && parsed.Type != "gitlab" && parsed.Type != "git") {
			out.Errors = append(out.Errors, ErrPackageSkillsNotGitSource(declarer, source))
			continue
		}
		if ref != nil && parsed.Ref != "" {
			out.Errors = append(out.Errors, ErrPackageSkillsDuplicateRef(declarer, source))
			continue
		}
		if ref != nil {
			parsed.Ref = *ref
		}
		if parsed.SkillFilter != "" {
			skills = append(skills, parsed.SkillFilter)
		}
		out.Remote = append(out.Remote, RemoteSkillsRequest{Parsed: parsed, Skills: skills})
	}
	return out
}

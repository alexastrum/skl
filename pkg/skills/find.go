package skills

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchAPIBase allows overriding the base URL of the search API for mocking in unit tests.
var SearchAPIBase = "https://skills.sh"

type FindOptions struct {
	Query string
	Cwd   string
	Local bool // if true, only search locally installed skills
}

type FoundSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path,omitempty"`
	Scope       string `json:"scope,omitempty"` // "project", "global", or "remote"
	Installs    int    `json:"installs,omitempty"`
}

// FindSkills searches for skills matching a query across local scopes and/or remote API.
func FindSkills(opts FindOptions) ([]FoundSkill, error) {
	var found []FoundSkill

	queryLower := strings.ToLower(opts.Query)

	// 1. Search locally installed project skills
	if opts.Query != "" || opts.Local {
		localSkills, err := ListInstalledSkills(ListOptions{Global: false, Cwd: opts.Cwd})
		if err == nil {
			for _, s := range localSkills {
				if strings.Contains(strings.ToLower(s.Name), queryLower) ||
					strings.Contains(strings.ToLower(s.Description), queryLower) {
					found = append(found, FoundSkill{
						Name:        s.Name,
						Description: s.Description,
						Source:      s.CanonicalPath,
						Path:        s.Path,
						Scope:       s.Scope,
					})
				}
			}
		}

		// 2. Search locally installed global skills
		globalSkills, err := ListInstalledSkills(ListOptions{Global: true, Cwd: opts.Cwd})
		if err == nil {
			for _, s := range globalSkills {
				if strings.Contains(strings.ToLower(s.Name), queryLower) ||
					strings.Contains(strings.ToLower(s.Description), queryLower) {
					found = append(found, FoundSkill{
						Name:        s.Name,
						Description: s.Description,
						Source:      s.CanonicalPath,
						Path:        s.Path,
						Scope:       s.Scope,
					})
				}
			}
		}
	}

	// 3. Search remote API if not restricted to local search and query is provided
	if !opts.Local && opts.Query != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		apiURL := fmt.Sprintf("%s/api/search?q=%s&limit=10", SearchAPIBase, url.QueryEscape(opts.Query))

		resp, err := client.Get(apiURL)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var result struct {
					Skills []struct {
						ID       string `json:"id"`
						Name     string `json:"name"`
						Installs int    `json:"installs"`
						Source   string `json:"source"`
					} `json:"skills"`
				}

				if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
					for _, s := range result.Skills {
						found = append(found, FoundSkill{
							Name:     s.Name,
							Source:   s.Source,
							Scope:    "remote",
							Installs: s.Installs,
						})
					}
				}
			}
		}
		// Note: We ignore API network errors to keep the search resilient when offline or in restricted environments.
	}

	return found, nil
}

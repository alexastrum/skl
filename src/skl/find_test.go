package skl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFindSkills_Local(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock local skill
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "test-find-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillMd := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: FindMe
description: This is a highly searchable skill
---
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// 1. Exact case-insensitive match on name
	res, err := FindSkills(FindOptions{
		Query: "findme",
		Cwd:   tmpDir,
		Local: true,
	})
	if err != nil {
		t.Fatalf("FindSkills failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 skill found, got %d", len(res))
	}
	if res[0].Name != "FindMe" {
		t.Errorf("expected found skill to be 'FindMe', got %q", res[0].Name)
	}

	// 2. Substring match on description
	resDesc, err := FindSkills(FindOptions{
		Query: "searchable",
		Cwd:   tmpDir,
		Local: true,
	})
	if err != nil {
		t.Fatalf("FindSkills failed: %v", err)
	}
	if len(resDesc) != 1 {
		t.Fatalf("expected 1 skill found on description, got %d", len(resDesc))
	}

	// 3. No match query
	resNone, err := FindSkills(FindOptions{
		Query: "notexist",
		Cwd:   tmpDir,
		Local: true,
	})
	if err != nil {
		t.Fatalf("FindSkills failed: %v", err)
	}
	if len(resNone) != 0 {
		t.Errorf("expected 0 skills found, got %d", len(resNone))
	}
}

func TestFindSkills_RemoteMock(t *testing.T) {
	// Setup a mock HTTP server using httptest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameter is parsed correctly
		q := r.URL.Query().Get("q")
		if q != "go-expert" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Return simulated skills search payload
		payload := map[string]interface{}{
			"skills": []map[string]interface{}{
				{
					"id":       "go-expert-slug",
					"name":     "Go Expert Skill",
					"installs": 1337,
					"source":   "github.com/sandu/go-expert",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	// Backup original API base URL and set mock URL
	originalBase := SearchAPIBase
	SearchAPIBase = server.URL
	defer func() { SearchAPIBase = originalBase }()

	tmpDir := t.TempDir()

	// Call FindSkills requesting remote API lookup
	res, err := FindSkills(FindOptions{
		Query: "go-expert",
		Cwd:   tmpDir,
		Local: false,
	})
	if err != nil {
		t.Fatalf("FindSkills failed: %v", err)
	}

	// Verify we got the mocked remote skill successfully
	if len(res) != 1 {
		t.Fatalf("expected exactly 1 skill from remote search, got %d", len(res))
	}

	got := res[0]
	if got.Name != "Go Expert Skill" {
		t.Errorf("expected name 'Go Expert Skill', got %q", got.Name)
	}
	if got.Source != "github.com/sandu/go-expert" {
		t.Errorf("expected source 'github.com/sandu/go-expert', got %q", got.Source)
	}
	if got.Scope != "remote" {
		t.Errorf("expected scope 'remote', got %q", got.Scope)
	}
	if got.Installs != 1337 {
		t.Errorf("expected installs 1337, got %d", got.Installs)
	}
}

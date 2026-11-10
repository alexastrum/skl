//go:build windows

package skl

import (
	"os"
	"path/filepath"
	"testing"
)

// Review #41 item 3: junctions are created through the Win32 API, so cmd.exe
// metacharacters in paths are harmless. Only characters that are legal in
// Windows file names are used (no | < > ") so the junction is really created.
func TestCreateJunction_MetacharacterPaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "tar&get^%PATH% (x)")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "li&nk ^calc !USERNAME! (y)")
	if err := createJunction(link, target); err != nil {
		t.Fatalf("createJunction: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil || string(got) != "x" {
		t.Fatalf("read through junction: %q %v", got, err)
	}
}

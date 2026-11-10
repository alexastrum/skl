//go:build windows

package skl

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
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
	if dest, err := os.Readlink(link); err != nil || !strings.EqualFold(filepath.Clean(dest), filepath.Clean(target)) {
		t.Errorf("junction points at %q (%v), want %q", dest, err, target)
	}
}

func TestCreateJunction_TargetMustBeDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := createJunction(link, file); err == nil {
		t.Fatal("a file target must be rejected")
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("no link may be left behind: %v", err)
	}
}

func TestMountPointReparseData(t *testing.T) {
	buf := mountPointReparseData(`C:\a`)
	sub := utf16.Encode([]rune(`\??\C:\a`))
	if tag := binary.LittleEndian.Uint32(buf[0:]); tag != windows.IO_REPARSE_TAG_MOUNT_POINT {
		t.Errorf("tag = %#x", tag)
	}
	wantLen := reparseMountPointHeader + (len(sub)+1+len(`C:\a`)+1)*2
	if got := int(binary.LittleEndian.Uint16(buf[4:])); got != wantLen || len(buf) != 8+wantLen {
		t.Errorf("data length = %d (buf %d), want %d", got, len(buf), wantLen)
	}
	if off, n := binary.LittleEndian.Uint16(buf[8:]), binary.LittleEndian.Uint16(buf[10:]); off != 0 || int(n) != len(sub)*2 {
		t.Errorf("substitute name at %d len %d", off, n)
	}
	if off, n := binary.LittleEndian.Uint16(buf[12:]), binary.LittleEndian.Uint16(buf[14:]); int(off) != len(sub)*2+2 || n != 8 {
		t.Errorf("print name at %d len %d", off, n)
	}
}

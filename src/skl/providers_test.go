package skl

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// ===========================================================================
// Test helpers

func provSkillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
}

func provWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// provWriteSkill writes root/rel/SKILL.md with the given name.
func provWriteSkill(t *testing.T, root, rel, name string) {
	t.Helper()
	provWriteFile(t, filepath.Join(root, filepath.FromSlash(rel), "SKILL.md"), provSkillMD(name, name+" description"))
}

func provWantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !IsDiagnostic(err, code) {
		t.Fatalf("err = %v (code %q), want code %s", err, DiagnosticCode(err), code)
	}
}

func provNames(skills []Skill) []string {
	out := make([]string, len(skills))
	for i, s := range skills {
		out[i] = s.Name
	}
	return out
}

func provSorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func provEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// provQuiet silences skipped-skill warnings for the test.
func provQuiet(t *testing.T) {
	t.Helper()
	old := skillWarningWriter
	skillWarningWriter = io.Discard
	t.Cleanup(func() { skillWarningWriter = old })
}

// provIsolate points TMPDIR at a fresh dir and clears env knobs that would
// change provider behavior. Returns the TMPDIR path.
func provIsolate(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, k := range []string{"INSTALL_INTERNAL_SKILLS", "SKILLS_DOWNLOAD_MAX_BYTES", "SKILLS_EXTRACT_MAX_BYTES",
		"SKILLS_EXTRACT_MAX_FILES", "GITHUB_TOKEN", "GH_TOKEN", "SKILLS_DOWNLOAD_URL"} {
		t.Setenv(k, "")
	}
	return tmp
}

func provDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func provSHA(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// ---------------------------------------------------------------------------
// Hand-crafted zip builder (full control over flags, names, extras, CRCs).

type zipSpec struct {
	name    string
	nameRaw []byte // overrides name when set
	data    []byte
	deflate bool
	flags   uint16
	mode    uint32 // unix mode stored in the external attributes
	extra   []byte // central directory extra field
	crc     *uint32
	// localName overrides the local-header name (mismatch tests).
	localName []byte
}

func buildZip(entries []zipSpec) []byte {
	le := binary.LittleEndian
	var buf, cd bytes.Buffer
	for _, e := range entries {
		name := e.nameRaw
		if name == nil {
			name = []byte(e.name)
		}
		localName := name
		if e.localName != nil {
			localName = e.localName
		}
		comp := e.data
		method := uint16(0)
		if e.deflate {
			var b bytes.Buffer
			w, _ := flate.NewWriter(&b, flate.DefaultCompression)
			_, _ = w.Write(e.data)
			_ = w.Close()
			comp = b.Bytes()
			method = 8
		}
		crc := crc32.ChecksumIEEE(e.data)
		if e.crc != nil {
			crc = *e.crc
		}
		off := uint32(buf.Len())
		h := make([]byte, 30)
		le.PutUint32(h[0:], 0x04034b50)
		le.PutUint16(h[4:], 20)
		le.PutUint16(h[6:], e.flags)
		le.PutUint16(h[8:], method)
		le.PutUint32(h[14:], crc)
		le.PutUint32(h[18:], uint32(len(comp)))
		le.PutUint32(h[22:], uint32(len(e.data)))
		le.PutUint16(h[26:], uint16(len(localName)))
		buf.Write(h)
		buf.Write(localName)
		buf.Write(comp)

		c := make([]byte, 46)
		le.PutUint32(c[0:], 0x02014b50)
		le.PutUint16(c[4:], 3<<8|20)
		le.PutUint16(c[6:], 20)
		le.PutUint16(c[8:], e.flags)
		le.PutUint16(c[10:], method)
		le.PutUint32(c[16:], crc)
		le.PutUint32(c[20:], uint32(len(comp)))
		le.PutUint32(c[24:], uint32(len(e.data)))
		le.PutUint16(c[28:], uint16(len(name)))
		le.PutUint16(c[30:], uint16(len(e.extra)))
		le.PutUint32(c[38:], e.mode<<16)
		le.PutUint32(c[42:], off)
		cd.Write(c)
		cd.Write(name)
		cd.Write(e.extra)
	}
	cdOff := buf.Len()
	buf.Write(cd.Bytes())
	end := make([]byte, 22)
	le.PutUint32(end[0:], 0x06054b50)
	le.PutUint16(end[8:], uint16(len(entries)))
	le.PutUint16(end[10:], uint16(len(entries)))
	le.PutUint32(end[12:], uint32(cd.Len()))
	le.PutUint32(end[16:], uint32(cdOff))
	buf.Write(end)
	return buf.Bytes()
}

// stdZip builds a zip with the standard library (stored or deflated).
func stdZip(t *testing.T, files [][2]string, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: f[0], Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type tarSpec struct {
	name string
	body string
	typ  byte
	link string
	mode int64
}

func buildTar(t *testing.T, specs []tarSpec, gz bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.Writer = &buf
	var gw *gzip.Writer
	if gz {
		gw = gzip.NewWriter(&buf)
		w = gw
	}
	tw := tar.NewWriter(w)
	for _, s := range specs {
		typ := s.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := s.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: s.name, Typeflag: typ, Mode: mode, Linkname: s.link, Format: tar.FormatPAX}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(s.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(s.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if gw != nil {
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// provServer is a tiny static file server that records request paths.
type provServer struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string]provResp
	hits  map[string]int
}

type provResp struct {
	status      int
	body        []byte
	contentType string
}

func newProvServer(t *testing.T) *provServer {
	t.Helper()
	ps := &provServer{files: map[string]provResp{}, hits: map[string]int{}}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.hits[r.URL.Path]++
		resp, ok := ps.files[r.URL.Path]
		ps.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if resp.contentType != "" {
			w.Header().Set("Content-Type", resp.contentType)
		}
		status := resp.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(resp.body)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *provServer) set(p string, body []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.files[p] = provResp{body: body}
}

func (ps *provServer) setJSON(t *testing.T, p string, v interface{}) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ps.set(p, b)
}

func (ps *provServer) hit(p string) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.hits[p]
}

func (ps *provServer) totalHits() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	n := 0
	for _, v := range ps.hits {
		n += v
	}
	return n
}

// ===========================================================================
// ReadZipArchive

func TestReadZipArchiveStdlibStoredAndDeflated(t *testing.T) {
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		data := stdZip(t, [][2]string{{"skill/SKILL.md", "hello"}, {"skill/dir/", ""}, {"skill/ref.md", strings.Repeat("x", 4096)}}, method)
		files, err := ReadZipArchive(data, 1<<20, 10)
		if err != nil {
			t.Fatalf("method %d: %v", method, err)
		}
		if string(files["skill/SKILL.md"]) != "hello" || len(files["skill/ref.md"]) != 4096 {
			t.Fatalf("method %d: files = %v", method, files)
		}
		if _, ok := files["skill/dir/"]; ok {
			t.Fatalf("directory entry returned as file")
		}
		if len(files) != 2 {
			t.Fatalf("method %d: got %d files", method, len(files))
		}
	}
}

func TestReadZipArchiveHandCrafted(t *testing.T) {
	files, err := ReadZipArchive(buildZip([]zipSpec{
		{name: "a.txt", data: []byte("stored")},
		{name: "b.txt", data: []byte(strings.Repeat("deflate me ", 50)), deflate: true},
		{name: "./c/./d.txt", data: []byte("dots")},
		{name: "./e/./", mode: 0o040755},
	}), 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["a.txt"]) != "stored" || string(files["b.txt"]) != strings.Repeat("deflate me ", 50) || string(files["c/d.txt"]) != "dots" {
		t.Fatalf("files = %v", files)
	}
	if len(files) != 3 {
		t.Fatalf("expected directory entries to be omitted, got keys %v", files)
	}
}

func TestReadZipArchiveRejections(t *testing.T) {
	badCRC := uint32(12345)
	cases := []struct {
		name     string
		entries  []zipSpec
		max      int64
		maxFiles int
		code     string
		msg      string
	}{
		{name: "traversal", entries: []zipSpec{{name: "../evil.txt", data: []byte("x")}}, code: CodeArchiveUnsafePath, msg: "Archive contains unsafe path: ../evil.txt"},
		{name: "nested traversal", entries: []zipSpec{{name: "a/../../evil.txt", data: []byte("x")}}, code: CodeArchiveUnsafePath},
		{name: "absolute", entries: []zipSpec{{name: "/etc/passwd", data: []byte("x")}}, code: CodeArchiveUnsafePath},
		{name: "drive prefix", entries: []zipSpec{{name: "C:/evil.txt", data: []byte("x")}}, code: CodeArchiveUnsafePath},
		{name: "backslash traversal", entries: []zipSpec{{name: `a\..\..\evil.txt`, data: []byte("x")}}, code: CodeArchiveUnsafePath},
		{name: "encrypted", entries: []zipSpec{{name: "secret.txt", data: []byte("x"), flags: 0x1}}, code: CodeArchiveEncryptedEntry},
		{name: "symlink", entries: []zipSpec{{name: "link", data: []byte("/etc/passwd"), mode: 0o120777}}, code: CodeArchiveLinkEntry},
		{name: "fifo", entries: []zipSpec{{name: "fifo", mode: 0o010644}}, code: CodeArchiveLinkEntry},
		{name: "too many entries incl dirs", entries: []zipSpec{{name: "a/"}, {name: "b/"}, {name: "c.txt", data: []byte("x")}}, maxFiles: 2, code: CodeArchiveTooManyFiles},
		{name: "size cap", entries: []zipSpec{{name: "big.txt", data: bytes.Repeat([]byte("x"), 100)}}, max: 50, code: CodeArchiveTooLarge},
		{name: "size cap across entries", entries: []zipSpec{{name: "a.txt", data: bytes.Repeat([]byte("x"), 30)}, {name: "b.txt", data: bytes.Repeat([]byte("x"), 30), deflate: true}}, max: 50, code: CodeArchiveTooLarge},
		{name: "crc mismatch", entries: []zipSpec{{name: "a.txt", data: []byte("hello"), crc: &badCRC}}, code: CodeArchiveInvalid, msg: "Zip entry checksum mismatch"},
		{name: "local/central mismatch", entries: []zipSpec{{name: "a.txt", data: []byte("hello"), localName: []byte("b.txt")}}, code: CodeArchiveInvalid, msg: "Zip local header does not match central directory"},
		{name: "invalid utf8 flag", entries: []zipSpec{{nameRaw: []byte{0xff, 0xfe}, data: []byte("x"), flags: 0x800}}, code: CodeArchiveInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			max := tc.max
			if max == 0 {
				max = 1 << 20
			}
			maxFiles := tc.maxFiles
			if maxFiles == 0 {
				maxFiles = 100
			}
			_, err := ReadZipArchive(buildZip(tc.entries), max, maxFiles)
			provWantCode(t, err, tc.code)
			if tc.msg != "" && err.Error() != tc.msg {
				t.Fatalf("message = %q, want %q", err.Error(), tc.msg)
			}
		})
	}
}

func TestReadZipArchiveGarbageAndTruncated(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("PK"), []byte("not a zip at all, definitely not")} {
		_, err := ReadZipArchive(data, 1<<20, 10)
		provWantCode(t, err, CodeArchiveInvalid)
	}
	// Central directory size that does not match the parsed entries.
	z := buildZip([]zipSpec{{name: "a.txt", data: []byte("x")}})
	end := len(z) - 22
	binary.LittleEndian.PutUint32(z[end+12:], binary.LittleEndian.Uint32(z[end+12:])-1)
	_, err := ReadZipArchive(z, 1<<20, 10)
	provWantCode(t, err, CodeArchiveInvalid)
}

func TestReadZipArchiveCP437Name(t *testing.T) {
	// 0x82 is "é" and 0x81 is "ü" in code page 437; no UTF-8 flag.
	files, err := ReadZipArchive(buildZip([]zipSpec{{nameRaw: []byte{'c', 'a', 'f', 0x82, '-', 0x81, '.', 't', 'x', 't'}, data: []byte("x")}}), 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["café-ü.txt"]; !ok {
		t.Fatalf("files = %v, want café-ü.txt", files)
	}
}

func TestReadZipArchiveUTF8Flag(t *testing.T) {
	files, err := ReadZipArchive(buildZip([]zipSpec{{name: "日本/スキル.md", data: []byte("x"), flags: 0x800}}), 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["日本/スキル.md"]; !ok {
		t.Fatalf("files = %v", files)
	}
}

func infoZipUnicodeExtra(rawName []byte, unicodeName string) []byte {
	payload := []byte{1}
	payload = binary.LittleEndian.AppendUint32(payload, crc32.ChecksumIEEE(rawName))
	payload = append(payload, unicodeName...)
	out := binary.LittleEndian.AppendUint16(nil, 0x7075)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(payload)))
	return append(out, payload...)
}

func TestReadZipArchiveInfoZipUnicodePath(t *testing.T) {
	raw := []byte("legacy.txt")
	files, err := ReadZipArchive(buildZip([]zipSpec{{nameRaw: raw, data: []byte("x"), extra: infoZipUnicodeExtra(raw, "unicodé/naïve.txt")}}), 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["unicodé/naïve.txt"]; !ok {
		t.Fatalf("files = %v, want unicode path", files)
	}
	// A stale CRC means the extra field is ignored and the raw name wins.
	stale := infoZipUnicodeExtra([]byte("other.txt"), "ignored.txt")
	files, err = ReadZipArchive(buildZip([]zipSpec{{nameRaw: raw, data: []byte("x"), extra: stale}}), 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["legacy.txt"]; !ok {
		t.Fatalf("files = %v, want legacy.txt", files)
	}
	// The unicode path is still validated for traversal.
	_, err = ReadZipArchive(buildZip([]zipSpec{{nameRaw: raw, data: []byte("x"), extra: infoZipUnicodeExtra(raw, "../evil.txt")}}), 1<<20, 10)
	provWantCode(t, err, CodeArchiveUnsafePath)
}

// ===========================================================================
// ExtractTarArchive

func TestExtractTarArchiveGzipAndPlain(t *testing.T) {
	for _, gz := range []bool{true, false} {
		dest := t.TempDir()
		data := buildTar(t, []tarSpec{
			{name: "pkg/", typ: tar.TypeDir, mode: 0o755},
			{name: "pkg/SKILL.md", body: "skill"},
			{name: "./pkg/run.sh", body: "#!/bin/sh", mode: 0o755},
			{name: "pkg/suid", body: "s", mode: 0o4755},
			{name: "pkg/fifo", typ: tar.TypeFifo},
		}, gz)
		if err := ExtractTarArchive(bytes.NewReader(data), dest, gz, 1<<20, 100); err != nil {
			t.Fatalf("gz=%v: %v", gz, err)
		}
		b, err := os.ReadFile(filepath.Join(dest, "pkg", "SKILL.md"))
		if err != nil || string(b) != "skill" {
			t.Fatalf("gz=%v: SKILL.md = %q, %v", gz, b, err)
		}
		for name, want := range map[string]os.FileMode{"SKILL.md": 0o644, "run.sh": 0o755, "suid": 0o755} {
			st, err := os.Stat(filepath.Join(dest, "pkg", name))
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != want || st.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
				t.Fatalf("gz=%v: %s mode = %v, want %v", gz, name, st.Mode(), want)
			}
		}
		if _, err := os.Lstat(filepath.Join(dest, "pkg", "fifo")); !os.IsNotExist(err) {
			t.Fatalf("gz=%v: fifo should be skipped, err=%v", gz, err)
		}
	}
}

func TestExtractTarArchiveRejections(t *testing.T) {
	cases := []struct {
		name     string
		specs    []tarSpec
		max      int64
		maxFiles int
		code     string
	}{
		{name: "traversal", specs: []tarSpec{{name: "../evil.md", body: "x"}}, code: CodeArchiveUnsafePath},
		{name: "nested traversal", specs: []tarSpec{{name: "a/../../evil.md", body: "x"}}, code: CodeArchiveUnsafePath},
		{name: "absolute", specs: []tarSpec{{name: "/etc/evil", body: "x"}}, code: CodeArchiveUnsafePath},
		{name: "drive", specs: []tarSpec{{name: "C:/evil", body: "x"}}, code: CodeArchiveUnsafePath},
		{name: "symlink", specs: []tarSpec{{name: "link", typ: tar.TypeSymlink, link: "/etc/passwd"}}, code: CodeArchiveLinkEntry},
		{name: "hardlink", specs: []tarSpec{{name: "a.md", body: "x"}, {name: "hard", typ: tar.TypeLink, link: "a.md"}}, code: CodeArchiveLinkEntry},
		{name: "dir entries count", specs: []tarSpec{{name: "a/", typ: tar.TypeDir}, {name: "b/", typ: tar.TypeDir}, {name: "c.md", body: "x"}}, maxFiles: 2, code: CodeArchiveTooManyFiles},
		{name: "size cap", specs: []tarSpec{{name: "big.md", body: strings.Repeat("x", 100)}}, max: 50, code: CodeArchiveTooLarge},
	}
	for _, tc := range cases {
		for _, gz := range []bool{true, false} {
			t.Run(tc.name, func(t *testing.T) {
				max := tc.max
				if max == 0 {
					max = 1 << 20
				}
				maxFiles := tc.maxFiles
				if maxFiles == 0 {
					maxFiles = 100
				}
				parent := t.TempDir()
				dest := filepath.Join(parent, "dest")
				if err := os.MkdirAll(dest, 0o755); err != nil {
					t.Fatal(err)
				}
				err := ExtractTarArchive(bytes.NewReader(buildTar(t, tc.specs, gz)), dest, gz, max, maxFiles)
				provWantCode(t, err, tc.code)
				if _, err := os.Lstat(filepath.Join(parent, "evil.md")); !os.IsNotExist(err) {
					t.Fatalf("file escaped the destination")
				}
			})
		}
	}
}

func TestExtractTarArchiveInvalid(t *testing.T) {
	dest := t.TempDir()
	provWantCode(t, ExtractTarArchive(bytes.NewReader([]byte("not gzip")), dest, true, 1<<20, 10), CodeArchiveInvalid)
	provWantCode(t, ExtractTarArchive(bytes.NewReader(buildTar(t, nil, false)), dest, false, 1<<20, 10), CodeArchiveInvalid)
}

// ===========================================================================
// DownloadSource

func TestDownloadSourceRawSkillMD(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/SKILL.md", []byte(provSkillMD("direct", "Direct skill")))
	dl, err := DownloadSource(srv.URL + "/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dl.TempDir)
	if dl.Kind != "skill-md" || filepath.Base(dl.RootDir) != "skill" {
		t.Fatalf("dl = %+v", dl)
	}
	b, err := os.ReadFile(filepath.Join(dl.RootDir, "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "name: direct") {
		t.Fatalf("SKILL.md = %q, %v", b, err)
	}
}

func TestDownloadSourceZipWithoutExtensionUnwrapsTopLevelDir(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/download", stdZip(t, [][2]string{
		{"my-skill/SKILL.md", provSkillMD("zipped", "Zipped skill")},
		{"my-skill/refs/guide.md", "guide"},
		{"__MACOSX/._junk", "junk"},
	}, zip.Deflate))
	dl, err := DownloadSource(srv.URL + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dl.TempDir)
	if dl.Kind != "archive" || filepath.Base(dl.RootDir) != "my-skill" {
		t.Fatalf("dl = %+v", dl)
	}
	if b, err := os.ReadFile(filepath.Join(dl.RootDir, "refs", "guide.md")); err != nil || string(b) != "guide" {
		t.Fatalf("guide = %q, %v", b, err)
	}
}

func TestDownloadSourceTgzAndMultipleTopLevel(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/skill.tgz", buildTar(t, []tarSpec{{name: "a/SKILL.md", body: provSkillMD("a", "A")}, {name: "b/SKILL.md", body: provSkillMD("b", "B")}}, true))
	srv.set("/skill.tar", buildTar(t, []tarSpec{{name: "only/SKILL.md", body: provSkillMD("only", "Only")}}, false))
	dl, err := DownloadSource(srv.URL + "/skill.tgz")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dl.TempDir)
	if dl.Kind != "archive" || filepath.Base(dl.RootDir) != "extract" {
		t.Fatalf("multiple top-level dirs should keep extract root: %+v", dl)
	}
	provEqual(t, provDirEntries(t, dl.RootDir), []string{"a", "b"})

	dl2, err := DownloadSource(srv.URL + "/skill.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dl2.TempDir)
	if filepath.Base(dl2.RootDir) != "only" {
		t.Fatalf("plain tar root = %s", dl2.RootDir)
	}
}

func TestDownloadSourceErrors(t *testing.T) {
	tmp := provIsolate(t)
	srv := newProvServer(t)
	srv.files["/missing"] = provResp{status: 404, body: []byte("nope")}
	srv.files["/server-error"] = provResp{status: 500}
	srv.set("/empty", []byte{})
	srv.set("/text", []byte("just some text, not a skill or archive"))
	srv.set("/invalid-skill", []byte("---\nname: x\n---\nno description"))
	srv.set("/unsafe.zip", buildZip([]zipSpec{{name: "../evil.md", data: []byte("x")}}))
	srv.set("/unsafe.tgz", buildTar(t, []tarSpec{{name: "../evil.md", body: "x"}}, true))
	srv.set("/many.zip", stdZip(t, [][2]string{{"a.md", "a"}, {"b.md", "b"}}, zip.Store))
	srv.set("/many-dirs.tgz", buildTar(t, []tarSpec{{name: "a/", typ: tar.TypeDir}, {name: "b/", typ: tar.TypeDir}, {name: "c.md", body: "c"}}, true))
	srv.set("/big", bytes.Repeat([]byte("x"), 64))

	cases := []struct {
		path string
		opts []DownloadSourceOptions
		env  string
		code string
		msg  string
	}{
		{path: "/missing", code: CodeDownloadHTTPError, msg: "Download failed with HTTP 404"},
		{path: "/server-error", code: CodeDownloadHTTPError},
		{path: "/empty", code: CodeDownloadEmpty},
		{path: "/text", code: CodeDownloadNotASkill},
		{path: "/invalid-skill", code: CodeDownloadNotASkill},
		{path: "/unsafe.zip", code: CodeArchiveUnsafePath, msg: "Archive contains unsafe path: ../evil.md"},
		{path: "/unsafe.tgz", code: CodeArchiveUnsafePath, msg: "Archive contains unsafe path: ../evil.md"},
		{path: "/many.zip", opts: []DownloadSourceOptions{{ExtractMaxFiles: 1}}, code: CodeArchiveTooManyFiles},
		{path: "/many-dirs.tgz", opts: []DownloadSourceOptions{{ExtractMaxFiles: 2}}, code: CodeArchiveTooManyFiles},
		{path: "/big", env: "10", code: CodeDownloadTooLarge},
		{path: "/big", opts: []DownloadSourceOptions{{DownloadMaxBytes: 10}}, code: CodeDownloadTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Setenv("SKILLS_DOWNLOAD_MAX_BYTES", tc.env)
			dl, err := DownloadSource(srv.URL+tc.path, tc.opts...)
			if dl != nil {
				t.Fatalf("dl = %+v, want nil", dl)
			}
			provWantCode(t, err, tc.code)
			if tc.msg != "" && err.Error() != tc.msg {
				t.Fatalf("message = %q, want %q", err.Error(), tc.msg)
			}
			if left := provDirEntries(t, tmp); len(left) != 0 {
				t.Fatalf("temp dirs left behind: %v", left)
			}
		})
	}
}

func TestDownloadSourceEnvOverridesOptionLimit(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/SKILL.md", []byte(provSkillMD("direct", "Direct skill")))
	t.Setenv("SKILLS_DOWNLOAD_MAX_BYTES", "1000000")
	dl, err := DownloadSource(srv.URL+"/SKILL.md", DownloadSourceOptions{DownloadMaxBytes: 5})
	if err != nil {
		t.Fatalf("env limit should win over option: %v", err)
	}
	_ = os.RemoveAll(dl.TempDir)
}

// ===========================================================================
// FindSkillMdPaths

func provTree(paths ...string) *RepoTree {
	t := &RepoTree{SHA: "root-sha", Branch: "HEAD"}
	for _, p := range paths {
		typ := "blob"
		if strings.HasSuffix(p, "/") {
			typ = "tree"
			p = strings.TrimSuffix(p, "/")
		}
		t.Tree = append(t.Tree, RepoTreeEntry{Path: p, Type: typ, SHA: "sha-" + p})
	}
	return t
}

func TestFindSkillMdPathsExactBasename(t *testing.T) {
	// #2580: only files named exactly SKILL.md (case-insensitive) count.
	tree := provTree("skills/a/SKILL.md", "skills/b/skill.md", "skills/c/NOT-SKILL.md", "skills/d/SKILL.md.bak",
		"skills/e/my-skill.md", "skills/f/", "skills/f/SKILL.md/")
	provEqual(t, FindSkillMdPaths(tree, "", nil), []string{"skills/a/SKILL.md", "skills/b/skill.md"})
	// Lookalikes are filtered inside an explicitly requested subtree too.
	provEqual(t, FindSkillMdPaths(provTree("pkg/SKILL-old.md", "pkg/SKILL.md"), "pkg", nil), []string{"pkg/SKILL.md"})
}

func TestFindSkillMdPathsPriorityAndOrder(t *testing.T) {
	tree := provTree("SKILL.md", "skills/a/SKILL.md", ".claude/skills/c/SKILL.md", ".codex/skills/d/SKILL.md",
		"skills/.curated/e/SKILL.md", "random/deep/f/SKILL.md")
	got := FindSkillMdPaths(tree, "", nil)
	provEqual(t, provSorted(got), provSorted([]string{"SKILL.md", "skills/a/SKILL.md", ".claude/skills/c/SKILL.md",
		".codex/skills/d/SKILL.md", "skills/.curated/e/SKILL.md"}))
	if got[0] != "SKILL.md" || got[1] != "skills/a/SKILL.md" {
		t.Fatalf("priority order = %q", got)
	}
}

func TestFindSkillMdPathsContainerDepthAndAncestor(t *testing.T) {
	tree := provTree(
		"skills/flat/SKILL.md",
		"skills/cat/nested/SKILL.md",
		"skills/cat/sub/deep/SKILL.md", // depth 3: included
		"skills/a/b/c/d/SKILL.md",      // depth 4: excluded
		"skills/parent/SKILL.md",
		"skills/parent/child/SKILL.md", // below a found skill: excluded
		"skills/parent/x/y/SKILL.md",   // below a found skill: excluded
		"skills/node_modules/pkg/SKILL.md",
		"skills/x/dist/y/SKILL.md",
	)
	provEqual(t, provSorted(FindSkillMdPaths(tree, "", nil)), provSorted([]string{
		"skills/flat/SKILL.md", "skills/cat/nested/SKILL.md", "skills/cat/sub/deep/SKILL.md", "skills/parent/SKILL.md",
	}))
}

func TestFindSkillMdPathsExtraContainers(t *testing.T) {
	tree := provTree("tools/skills/SKILL.md", "tools/skills/a/SKILL.md", "tools/skills/g/b/SKILL.md", "skills/c/SKILL.md")
	// Without the skills.sh.json containers only the standard locations count.
	provEqual(t, FindSkillMdPaths(tree, "", nil), []string{"skills/c/SKILL.md"})
	// Extra containers search children only (not tools/skills/SKILL.md itself), with container depth.
	provEqual(t, provSorted(FindSkillMdPaths(tree, "", []string{"tools/skills", "tools/skills"})),
		provSorted([]string{"skills/c/SKILL.md", "tools/skills/a/SKILL.md", "tools/skills/g/b/SKILL.md"}))
	// An extra container duplicating a built-in prefix keeps built-in behavior.
	provEqual(t, FindSkillMdPaths(provTree("skills/SKILL.md"), "", []string{"skills"}), []string{"skills/SKILL.md"})
}

func TestFindSkillMdPathsFallbackAndSubpath(t *testing.T) {
	tree := provTree("a/b/c/d/e/SKILL.md", "a/b/c/d/e/f/SKILL.md")
	provEqual(t, FindSkillMdPaths(tree, "", nil), []string{"a/b/c/d/e/SKILL.md"})
	if got := FindSkillMdPaths(provTree("README.md"), "", nil); len(got) != 0 {
		t.Fatalf("got %q, want none", got)
	}

	tree = provTree("packages/x/SKILL.md", "packages/x/skills/a/SKILL.md", "other/SKILL.md", "packages/xy/SKILL.md")
	provEqual(t, FindSkillMdPaths(tree, "packages/x", nil), []string{"packages/x/SKILL.md", "packages/x/skills/a/SKILL.md"})
	provEqual(t, FindSkillMdPaths(tree, "packages/x/", nil), []string{"packages/x/SKILL.md", "packages/x/skills/a/SKILL.md"})
	if got := FindSkillMdPaths(tree, "nope", nil); got != nil {
		t.Fatalf("got %q, want nil", got)
	}
}

func TestGetSkillFolderHashFromTree(t *testing.T) {
	tree := provTree("skills/foo/", "skills/foo/SKILL.md")
	if got := GetSkillFolderHashFromTree(tree, "skills/foo/SKILL.md"); got != "sha-skills/foo" {
		t.Fatalf("folder hash = %q", got)
	}
	if got := GetSkillFolderHashFromTree(tree, "SKILL.md"); got != "root-sha" {
		t.Fatalf("root hash = %q", got)
	}
	if got := GetSkillFolderHashFromTree(tree, "skills/bar/SKILL.md"); got != "" {
		t.Fatalf("missing hash = %q", got)
	}
	if got := GetSkillFolderHashFromTree(nil, "SKILL.md"); got != "" {
		t.Fatalf("nil tree hash = %q", got)
	}
}

// ===========================================================================
// TryBlobInstall

type blobFixture struct {
	tree      *RepoTree
	raw       map[string]string              // repo path -> contents
	snapshots map[string][]SkillSnapshotFile // slug -> files (absent = 404)
}

// startBlobServer serves the GitHub Trees API, raw.githubusercontent.com and
// the skills.sh download API for one repo, and points the package at it.
func startBlobServer(t *testing.T, fx blobFixture) *provServer {
	t.Helper()
	provIsolate(t)
	t.Setenv("PATH", t.TempDir()) // no gh/git: never shell out to the network
	srv := newProvServer(t)
	if fx.tree != nil {
		for _, branch := range []string{"HEAD", "main", "master"} {
			srv.setJSON(t, "/api/repos/vercel-labs/demo/git/trees/"+branch, map[string]interface{}{"sha": fx.tree.SHA, "tree": fx.tree.Tree})
		}
	}
	for p, c := range fx.raw {
		srv.set("/raw/vercel-labs/demo/HEAD/"+p, []byte(c))
	}
	for slug, files := range fx.snapshots {
		srv.setJSON(t, "/dl/api/download/vercel-labs/demo/"+slug, map[string]interface{}{"files": files, "hash": "h"})
	}
	oldAPI, oldRaw := githubAPIBaseOverride, rawGitHubBase
	githubAPIBaseOverride = srv.URL + "/api"
	rawGitHubBase = srv.URL + "/raw"
	t.Setenv("SKILLS_DOWNLOAD_URL", srv.URL+"/dl")
	ResetRepoTreeAuthState()
	t.Cleanup(func() {
		githubAPIBaseOverride, rawGitHubBase = oldAPI, oldRaw
		ResetRepoTreeAuthState()
	})
	return srv
}

func snapFiles(kv ...string) []SkillSnapshotFile {
	var out []SkillSnapshotFile
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, SkillSnapshotFile{Path: kv[i], Contents: kv[i+1]})
	}
	return out
}

func snapPaths(files []SkillSnapshotFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

func TestTryBlobInstallRefBypass(t *testing.T) {
	srv := startBlobServer(t, blobFixture{tree: provTree("SKILL.md"), raw: map[string]string{"SKILL.md": provSkillMD("root", "Root")}})
	res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{Ref: "v1.0.0"})
	if res != nil || err != nil {
		t.Fatalf("res=%+v err=%v, want nil,nil", res, err)
	}
	if n := srv.totalHits(); n != 0 {
		t.Fatalf("explicit ref should bypass the network, got %d requests", n)
	}
}

func TestTryBlobInstallRootSkillOnlySkillMD(t *testing.T) {
	md := provSkillMD("root-skill", "Root skill")
	startBlobServer(t, blobFixture{
		tree:      provTree("SKILL.md", "README.md", "src/", "src/index.ts"),
		raw:       map[string]string{"SKILL.md": md},
		snapshots: map[string][]SkillSnapshotFile{"root-skill": snapFiles("SKILL.md", md, "README.md", "readme", "src/index.ts", "code")},
	})
	res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
	if err != nil || res == nil {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if len(res.Skills) != 1 {
		t.Fatalf("skills = %d", len(res.Skills))
	}
	s := res.Skills[0]
	if s.Name != "root-skill" || s.RepoPath != "SKILL.md" || s.Description != "Root skill" || s.RawContent != md {
		t.Fatalf("skill = %+v", s)
	}
	provEqual(t, snapPaths(s.Files), []string{"SKILL.md"})
	wantHash := sha256.Sum256([]byte("SKILL.md" + md))
	if s.SnapshotHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("snapshot hash should cover only SKILL.md")
	}
	if res.Tree == nil || res.Tree.Branch != "HEAD" {
		t.Fatalf("tree = %+v", res.Tree)
	}
}

func TestTryBlobInstallNestedSnapshotCompleteness(t *testing.T) {
	md := provSkillMD("foo", "Foo")
	base := []string{"skills/", "skills/foo/", "skills/foo/SKILL.md", "skills/foo/ref.md"}

	t.Run("complete keeps supporting files", func(t *testing.T) {
		startBlobServer(t, blobFixture{
			tree:      provTree(append(base, "skills/foo/metadata.json", "skills/foo/__pycache__/x.pyc", "skills/foo/.git/HEAD")...),
			raw:       map[string]string{"skills/foo/SKILL.md": md},
			snapshots: map[string][]SkillSnapshotFile{"foo": snapFiles("SKILL.md", md, "ref.md", "ref")},
		})
		res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
		if err != nil || res == nil || len(res.Skills) != 1 {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		provEqual(t, snapPaths(res.Skills[0].Files), []string{"SKILL.md", "ref.md"})
		if res.Skills[0].RepoPath != "skills/foo/SKILL.md" {
			t.Fatalf("repo path = %q", res.Skills[0].RepoPath)
		}
	})

	t.Run("incomplete falls back (#2674)", func(t *testing.T) {
		startBlobServer(t, blobFixture{
			tree:      provTree(append(base, "skills/foo/scripts/run.sh")...),
			raw:       map[string]string{"skills/foo/SKILL.md": md},
			snapshots: map[string][]SkillSnapshotFile{"foo": snapFiles("SKILL.md", md, "ref.md", "ref")},
		})
		res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
		if res != nil || err != nil {
			t.Fatalf("res=%+v err=%v, want nil,nil", res, err)
		}
	})

	t.Run("failed snapshot falls back", func(t *testing.T) {
		startBlobServer(t, blobFixture{
			tree: provTree(base...),
			raw:  map[string]string{"skills/foo/SKILL.md": md},
		})
		res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
		if res != nil || err != nil {
			t.Fatalf("res=%+v err=%v, want nil,nil", res, err)
		}
	})

	t.Run("missing tree falls back", func(t *testing.T) {
		startBlobServer(t, blobFixture{})
		res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
		if res != nil || err != nil {
			t.Fatalf("res=%+v err=%v, want nil,nil", res, err)
		}
	})
}

func TestTryBlobInstallSkillFilterAndInternal(t *testing.T) {
	fooMD, barMD := provSkillMD("foo", "Foo"), provSkillMD("bar", "Bar")
	secretMD := "---\nname: secret\ndescription: Secret\nmetadata:\n  internal: true\n---\n"
	srv := startBlobServer(t, blobFixture{
		tree: provTree("skills/foo/SKILL.md", "skills/bar/SKILL.md", "skills/secret/SKILL.md"),
		raw:  map[string]string{"skills/foo/SKILL.md": fooMD, "skills/bar/SKILL.md": barMD, "skills/secret/SKILL.md": secretMD},
		snapshots: map[string][]SkillSnapshotFile{
			"foo": snapFiles("SKILL.md", fooMD), "bar": snapFiles("SKILL.md", barMD), "secret": snapFiles("SKILL.md", secretMD),
		},
	})
	res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{SkillFilter: "Bar"})
	if err != nil || res == nil || len(res.Skills) != 1 || res.Skills[0].Name != "bar" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if srv.hit("/raw/vercel-labs/demo/HEAD/skills/foo/SKILL.md") != 0 {
		t.Fatalf("folder-slug filter should avoid fetching other SKILL.md files")
	}

	res, err = TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
	if err != nil || res == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var names []string
	for _, s := range res.Skills {
		names = append(names, s.Name)
	}
	provEqual(t, provSorted(names), []string{"bar", "foo"})

	res, err = TryBlobInstall("vercel-labs/demo", BlobInstallOptions{IncludeInternal: true})
	if err != nil || res == nil || len(res.Skills) != 3 {
		t.Fatalf("include internal: res=%+v err=%v", res, err)
	}
}

func TestTryBlobInstallSkillsShJSON(t *testing.T) {
	aMD, bMD := provSkillMD("a", "A"), provSkillMD("b", "B")
	startBlobServer(t, blobFixture{
		tree: provTree("skills.sh.json", "tools/skills/a/SKILL.md", "skills/b/SKILL.md"),
		raw:  map[string]string{"skills.sh.json": `{"paths":["./tools/skills/"]}`, "tools/skills/a/SKILL.md": aMD, "skills/b/SKILL.md": bMD},
		snapshots: map[string][]SkillSnapshotFile{
			"a": snapFiles("SKILL.md", aMD), "b": snapFiles("SKILL.md", bMD),
		},
	})
	res, err := TryBlobInstall("vercel-labs/demo", BlobInstallOptions{})
	if err != nil || res == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var paths []string
	for _, s := range res.Skills {
		paths = append(paths, s.RepoPath)
	}
	provEqual(t, provSorted(paths), []string{"skills/b/SKILL.md", "tools/skills/a/SKILL.md"})
}

func TestIsBlobEligible(t *testing.T) {
	for in, want := range map[string]bool{
		"vercel-labs/skills": true, "Vercel/x": true, "zapier/connectors": true, "ZAPIER/Connectors": true,
		"zapier/other": false, "someone/repo": false, "vercel": false, "/repo": false,
	} {
		if got := IsBlobEligible(in); got != want {
			t.Errorf("IsBlobEligible(%q) = %v, want %v", in, got, want)
		}
	}
}

// ===========================================================================
// Well-known provider

func wkV2Index(entries ...map[string]interface{}) map[string]interface{} {
	skills := []interface{}{}
	for _, e := range entries {
		skills = append(skills, e)
	}
	return map[string]interface{}{"$schema": DiscoverySchemaV2, "skills": skills}
}

func wkV1Index(entries ...map[string]interface{}) map[string]interface{} {
	skills := []interface{}{}
	for _, e := range entries {
		skills = append(skills, e)
	}
	return map[string]interface{}{"skills": skills}
}

func TestFetchWellKnownSkillsV2SkillMD(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	md := provSkillMD("foo", "Foo skill")
	srv.set("/.well-known/agent-skills/foo/SKILL.md", []byte(md))
	srv.setJSON(t, "/.well-known/agent-skills/index.json", wkV2Index(map[string]interface{}{
		"name": "foo", "description": "Foo skill", "type": "skill-md", "url": "foo/SKILL.md", "digest": provSHA([]byte(md)),
		"source": map[string]interface{}{"repo": "o/r"},
	}))
	skills, err := FetchWellKnownSkills(srv.URL, false)
	if err != nil || len(skills) != 1 {
		t.Fatalf("skills=%v err=%v", skills, err)
	}
	s := skills[0]
	if s.Name != "foo" || s.Version != "0.2.0" || s.Digest != provSHA([]byte(md)) || s.InstallName != "foo" ||
		s.SourceURL != srv.URL+"/.well-known/agent-skills/foo/SKILL.md" || string(s.Files["SKILL.md"]) != md {
		t.Fatalf("skill = %+v", s)
	}
	if ComputeWellKnownSkillDigest(s) != s.Digest {
		t.Fatalf("v2 digest should pass through")
	}
	if _, ok := s.IndexEntry["source"]; !ok {
		t.Fatalf("index entry should keep extra fields: %v", s.IndexEntry)
	}
}

func TestFetchWellKnownSkillsV2Archives(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	tgz := buildTar(t, []tarSpec{{name: "SKILL.md", body: provSkillMD("tarred", "Tarred")}, {name: "refs/guide.md", body: "guide"}}, true)
	zz := stdZip(t, [][2]string{{"SKILL.md", provSkillMD("zipped", "Zipped")}, {"scripts/run.sh", "echo"}}, zip.Deflate)
	srv.set("/.well-known/agent-skills/tarred.tar.gz", tgz)
	srv.set("/artifacts/zipped.zip", zz)
	srv.setJSON(t, "/.well-known/agent-skills/index.json", wkV2Index(
		map[string]interface{}{"name": "tarred", "description": "T", "type": "archive", "url": "tarred.tar.gz", "digest": provSHA(tgz)},
		map[string]interface{}{"name": "zipped", "description": "Z", "type": "archive", "url": "/artifacts/zipped.zip", "digest": provSHA(zz)},
		map[string]interface{}{"name": "Bad_Name", "description": "x", "type": "archive", "url": "x.zip", "digest": provSHA(zz)},
		map[string]interface{}{"name": "nodigest", "description": "x", "type": "archive", "url": "x.zip"},
	))
	skills, err := FetchWellKnownSkills(srv.URL, false)
	if err != nil || len(skills) != 2 {
		t.Fatalf("skills=%v err=%v", skills, err)
	}
	byName := map[string]*WellKnownSkill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	if string(byName["tarred"].Files["refs/guide.md"]) != "guide" || string(byName["zipped"].Files["scripts/run.sh"]) != "echo" {
		t.Fatalf("archive files not extracted: %+v %+v", byName["tarred"], byName["zipped"])
	}
}

func TestFetchWellKnownSkillsDigestMismatchNoFallback(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	md := provSkillMD("foo", "Foo")
	srv.set("/.well-known/agent-skills/foo/SKILL.md", []byte(md))
	srv.setJSON(t, "/.well-known/agent-skills/index.json", wkV2Index(map[string]interface{}{
		"name": "foo", "description": "Foo", "type": "skill-md", "url": "foo/SKILL.md", "digest": provSHA([]byte("tampered")),
	}))
	srv.set("/.well-known/skills/foo/SKILL.md", []byte(md))
	srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(map[string]interface{}{"name": "foo", "description": "Foo", "files": []string{"SKILL.md"}}))

	skills, err := FetchWellKnownSkills(srv.URL, false)
	if skills != nil {
		t.Fatalf("skills = %v, want nil", skills)
	}
	provWantCode(t, err, CodeWellknownDigestMismatch)
	if !strings.Contains(err.Error(), "'foo'") || !strings.Contains(err.Error(), provSHA([]byte(md))) {
		t.Fatalf("message = %q", err.Error())
	}
	if srv.hit("/.well-known/skills/foo/SKILL.md") != 0 {
		t.Fatalf("digest mismatch must not fall back to the v1 index")
	}
}

func TestFetchWellKnownSkillsV2AheadOfV1(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	md := provSkillMD("foo", "Foo")
	srv.set("/.well-known/agent-skills/foo/SKILL.md", []byte(md))
	srv.setJSON(t, "/.well-known/agent-skills/index.json", wkV2Index(map[string]interface{}{
		"name": "foo", "description": "Foo", "type": "skill-md", "url": "foo/SKILL.md", "digest": provSHA([]byte(md)),
	}))
	srv.set("/.well-known/skills/foo/SKILL.md", []byte(md))
	srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(map[string]interface{}{"name": "foo", "description": "Foo", "files": []string{"SKILL.md"}}))
	skills, err := FetchWellKnownSkills(srv.URL, false)
	if err != nil || len(skills) != 1 || skills[0].Version != "0.2.0" {
		t.Fatalf("skills=%v err=%v", skills, err)
	}
}

func TestFetchWellKnownSkillsV1Files(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	md := provSkillMD("foo", "Foo")
	srv.set("/.well-known/skills/foo/SKILL.md", []byte(md))
	srv.set("/.well-known/skills/foo/refs/ref.md", []byte("ref"))
	srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(map[string]interface{}{
		"name": "foo", "description": "Foo", "files": []string{"SKILL.md", "refs/ref.md", "missing.md"},
	}))
	skills, err := FetchWellKnownSkills(srv.URL+"/", false)
	if err != nil || len(skills) != 1 {
		t.Fatalf("skills=%v err=%v", skills, err)
	}
	s := skills[0]
	if s.Version != "0.1.0" || s.Digest != "" || string(s.Files["refs/ref.md"]) != "ref" || len(s.Files) != 2 {
		t.Fatalf("skill = %+v files=%v", s, s.Files)
	}
	if s.SourceURL != srv.URL+"/.well-known/skills/foo/SKILL.md" {
		t.Fatalf("source url = %q", s.SourceURL)
	}
}

func TestFetchWellKnownSkillsInvalidIndexes(t *testing.T) {
	cases := map[string]interface{}{
		"unknown schema": map[string]interface{}{"$schema": "https://example.com/other.json",
			"skills": []interface{}{map[string]interface{}{"name": "foo", "description": "Foo", "files": []string{"SKILL.md"}}}},
		"v1 traversal file":   wkV1Index(map[string]interface{}{"name": "foo", "description": "Foo", "files": []string{"SKILL.md", "../secret"}}),
		"v1 missing SKILL.md": wkV1Index(map[string]interface{}{"name": "foo", "description": "Foo", "files": []string{"ref.md"}}),
		"v1 bad name":         wkV1Index(map[string]interface{}{"name": "Foo--bar", "description": "Foo", "files": []string{"SKILL.md"}}),
	}
	for name, index := range cases {
		t.Run(name, func(t *testing.T) {
			provIsolate(t)
			srv := newProvServer(t)
			srv.set("/.well-known/agent-skills/foo/SKILL.md", []byte(provSkillMD("foo", "Foo")))
			srv.setJSON(t, "/.well-known/agent-skills/index.json", index)
			skills, err := FetchWellKnownSkills(srv.URL, false)
			if skills != nil || err != nil {
				t.Fatalf("skills=%v err=%v, want nil,nil", skills, err)
			}
			if srv.hit("/.well-known/agent-skills/foo/SKILL.md") != 0 {
				t.Fatalf("invalid index should not fetch skill files")
			}
		})
	}
}

func TestFetchWellKnownSkillsScoped(t *testing.T) {
	setup := func(t *testing.T) *provServer {
		provIsolate(t)
		srv := newProvServer(t)
		srv.set("/.well-known/skills/rootskill/SKILL.md", []byte(provSkillMD("rootskill", "Root")))
		srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(map[string]interface{}{"name": "rootskill", "description": "Root", "files": []string{"SKILL.md"}}))
		return srv
	}

	t.Run("root only -> scope not found", func(t *testing.T) {
		srv := setup(t)
		skills, err := FetchWellKnownSkills(srv.URL+"/docs", false)
		if skills != nil {
			t.Fatalf("skills = %v", skills)
		}
		provWantCode(t, err, CodeWellknownScopeNotFound)
		if !strings.Contains(err.Error(), "'/docs'") {
			t.Fatalf("message = %q", err.Error())
		}
		if srv.hit("/.well-known/skills/rootskill/SKILL.md") != 0 {
			t.Fatalf("scoped URL must not widen to the root index")
		}
	})

	t.Run("scoped index with no skills -> scope not found", func(t *testing.T) {
		srv := setup(t)
		srv.setJSON(t, "/docs/.well-known/skills/index.json", map[string]interface{}{"skills": []interface{}{}})
		_, err := FetchWellKnownSkills(srv.URL+"/docs", false)
		provWantCode(t, err, CodeWellknownScopeNotFound)
	})

	t.Run("scoped index resolves", func(t *testing.T) {
		srv := setup(t)
		srv.set("/docs/.well-known/skills/docskill/SKILL.md", []byte(provSkillMD("docskill", "Docs")))
		srv.setJSON(t, "/docs/.well-known/skills/index.json", wkV1Index(map[string]interface{}{"name": "docskill", "description": "Docs", "files": []string{"SKILL.md"}}))
		skills, err := FetchWellKnownSkills(srv.URL+"/docs/", false)
		if err != nil || len(skills) != 1 || skills[0].Name != "docskill" {
			t.Fatalf("skills=%v err=%v", skills, err)
		}
		if srv.hit("/.well-known/skills/rootskill/SKILL.md") != 0 {
			t.Fatalf("root skills should not be fetched when the scope resolves")
		}
	})

	t.Run("no index at all", func(t *testing.T) {
		provIsolate(t)
		srv := newProvServer(t)
		skills, err := FetchWellKnownSkills(srv.URL+"/docs", false)
		if skills != nil || err != nil {
			t.Fatalf("skills=%v err=%v, want nil,nil", skills, err)
		}
		skills, err = FetchWellKnownSkills(srv.URL, false)
		if skills != nil || err != nil {
			t.Fatalf("root: skills=%v err=%v, want nil,nil", skills, err)
		}
	})
}

func TestFetchWellKnownSkillsInternalFiltering(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/.well-known/skills/pub/SKILL.md", []byte(provSkillMD("pub", "Public")))
	srv.set("/.well-known/skills/secret/SKILL.md", []byte("---\nname: secret\ndescription: Secret\nmetadata:\n  internal: true\n---\n"))
	srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(
		map[string]interface{}{"name": "pub", "description": "Public", "files": []string{"SKILL.md"}},
		map[string]interface{}{"name": "secret", "description": "Secret", "files": []string{"SKILL.md"}},
	))
	names := func(skills []*WellKnownSkill) []string {
		var out []string
		for _, s := range skills {
			out = append(out, s.Name)
		}
		sort.Strings(out)
		return out
	}
	skills, err := FetchWellKnownSkills(srv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, names(skills), []string{"pub"})
	skills, err = FetchWellKnownSkills(srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, names(skills), []string{"pub", "secret"})
	t.Setenv("INSTALL_INTERNAL_SKILLS", "1")
	skills, _ = FetchWellKnownSkills(srv.URL, false)
	provEqual(t, names(skills), []string{"pub", "secret"})
}

func TestComputeWellKnownSkillDigest(t *testing.T) {
	if got := ComputeWellKnownSkillDigest(&WellKnownSkill{Digest: "sha256:abc", Files: map[string][]byte{"SKILL.md": []byte("x")}}); got != "sha256:abc" {
		t.Fatalf("passthrough = %q", got)
	}
	s := &WellKnownSkill{Files: map[string][]byte{"b.md": []byte("B"), "SKILL.md": []byte("S"), "a.md": []byte("A")}}
	// JS default sort: "SKILL.md" < "a.md" < "b.md" (UTF-16 code units).
	want := provSHA([]byte("SKILL.md\x00S\x00a.md\x00A\x00b.md\x00B\x00"))
	got := ComputeWellKnownSkillDigest(s)
	if got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	if ComputeWellKnownSkillDigest(s) != got {
		t.Fatalf("digest not deterministic")
	}
	s.Files["a.md"] = []byte("changed")
	if ComputeWellKnownSkillDigest(s) == got {
		t.Fatalf("digest should change when contents change")
	}
	// UTF-16 ordering: U+FF5E sorts after U+1F600 (surrogate 0xD83D) in JS.
	s2 := &WellKnownSkill{Files: map[string][]byte{"\uff5e": []byte("1"), "\U0001F600": []byte("2")}}
	want2 := provSHA([]byte("\U0001F600\x002\x00\uff5e\x001\x00"))
	if got := ComputeWellKnownSkillDigest(s2); got != want2 {
		t.Fatalf("utf16 order digest = %q, want %q", got, want2)
	}
}

func TestGetPackSkillPointer(t *testing.T) {
	hash := strings.Repeat("a1", 32)
	mk := func(src interface{}) *WellKnownSkill {
		return &WellKnownSkill{IndexEntry: map[string]interface{}{"source": src}}
	}
	got := GetPackSkillPointer(mk(map[string]interface{}{"repo": "owner/repo.js", "skill": "My_Skill.v2", "treeHash": hash}))
	if got == nil || *got != (PackSkillPointer{Repo: "owner/repo.js", Skill: "My_Skill.v2", TreeHash: hash}) {
		t.Fatalf("valid pointer = %+v", got)
	}
	bad := []interface{}{
		nil,
		"owner/repo",
		map[string]interface{}{"repo": "owner/repo", "skill": "s"},
		map[string]interface{}{"repo": "owner/repo", "skill": "s", "treeHash": strings.ToUpper(hash)},
		map[string]interface{}{"repo": "owner/repo", "skill": "s", "treeHash": hash[:63]},
		map[string]interface{}{"repo": "owner/../repo", "skill": "s", "treeHash": hash},
		map[string]interface{}{"repo": "owner/repo/extra", "skill": "s", "treeHash": hash},
		map[string]interface{}{"repo": "-owner/repo", "skill": "s", "treeHash": hash},
		map[string]interface{}{"repo": "owner/repo", "skill": "../s", "treeHash": hash},
		map[string]interface{}{"repo": "owner/repo", "skill": "a..b", "treeHash": hash},
		map[string]interface{}{"repo": "owner/repo", "skill": "s/x", "treeHash": hash},
		map[string]interface{}{"repo": 1, "skill": "s", "treeHash": hash},
	}
	for i, src := range bad {
		if got := GetPackSkillPointer(mk(src)); got != nil {
			t.Errorf("case %d: got %+v, want nil", i, got)
		}
	}
	if GetPackSkillPointer(nil) != nil || GetPackSkillPointer(&WellKnownSkill{}) != nil {
		t.Fatalf("nil inputs should yield nil")
	}
}

func TestWellKnownURLHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.example.com/docs": "example.com", "https://sub.example.com": "sub.example.com", "not a url": "unknown",
	} {
		if got := GetWellKnownSourceIdentifier(in); got != want {
			t.Errorf("GetWellKnownSourceIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{
		"https://skills.sh/p/my-pack": true, "https://www.skills.sh/p/x/y": true, "https://skills.sh/p/": false,
		"https://skills.sh/owner/repo": false, "https://evil.com/p/x": false,
	} {
		if got := IsSkillsShPackURL(in); got != want {
			t.Errorf("IsSkillsShPackURL(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]string{"Web Design": "web-design", "pr_review": "pr-review", "--A!!b--": "ab", "x  y__z": "x-y-z"} {
		if got := ToSkillSlug(in); got != want {
			t.Errorf("ToSkillSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// ===========================================================================
// DiscoverSkills

func TestDiscoverSkillsRootShortCircuitVsFullDepth(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "", "root")
	provWriteSkill(t, root, "skills/a", "a")
	provWriteSkill(t, root, "deep/x/y", "deep")
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provNames(skills), []string{"root"})
	skills, err = DiscoverSkills(root, "", DiscoverSkillsOptions{FullDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provSorted(provNames(skills)), []string{"a", "deep", "root"})
	if skills[0].Name != "root" {
		t.Fatalf("root skill should come first: %q", provNames(skills))
	}
}

func TestDiscoverSkillsDuplicateNames(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "", "same")
	provWriteSkill(t, root, "skills/same", "same")
	skills, _ := DiscoverSkills(root, "", DiscoverSkillsOptions{FullDepth: true})
	if len(skills) != 1 {
		t.Fatalf("duplicates should collapse: %q", provNames(skills))
	}
	skills, _ = DiscoverSkills(root, "", DiscoverSkillsOptions{FullDepth: true, IncludeDuplicateNames: true})
	if len(skills) != 2 {
		t.Fatalf("IncludeDuplicateNames should keep both: %q", provNames(skills))
	}
}

func TestDiscoverSkillsAgentDirs(t *testing.T) {
	// #2509: every agent project dir is a priority container, including legacy ones.
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "skills/a", "a")
	provWriteSkill(t, root, ".claude/skills/claude-skill", "claude-skill")
	provWriteSkill(t, root, ".codex/skills/codex-skill", "codex-skill")
	provWriteSkill(t, root, ".agents/skills/group/agents-skill", "agents-skill")
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provSorted(provNames(skills)), []string{"a", "agents-skill", "claude-skill", "codex-skill"})
}

func TestDiscoverSkillsNestedContainers(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "skills/flat", "flat")
	provWriteSkill(t, root, "skills/product-a/one", "one")
	provWriteSkill(t, root, "skills/product-b/sub/three", "three") // depth 3
	provWriteSkill(t, root, "skills/a/b/c/four", "four")           // depth 4: not in priority walk
	provWriteSkill(t, root, "skills/outer", "outer")
	provWriteSkill(t, root, "skills/outer/inner", "inner") // below a found skill
	provWriteSkill(t, root, "skills/node_modules/inner/pkg", "pkg")
	provWriteSkill(t, root, "skills/.curated/cur", "cur")
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provSorted(provNames(skills)), []string{"cur", "flat", "one", "outer", "three"})
}

func TestDiscoverSkillsFallbackAndSkipDirs(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "some/where/deep", "deep")
	provWriteSkill(t, root, "node_modules/pkg", "nm")
	provWriteSkill(t, root, "dist/x", "dist")
	provWriteSkill(t, root, "a/.git/x", "git")
	provWriteSkill(t, root, "a/b/c/d/e/f", "too-deep")
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provNames(skills), []string{"deep"})
}

func TestDiscoverSkillsPluginManifests(t *testing.T) {
	provIsolate(t)
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	provWriteFile(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), `{
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [
    {"name": "reviewer", "source": "./rev", "skills": ["./skills/review"]},
    {"name": "evil", "source": "./../../outside", "skills": ["./skills/evil"]},
    {"name": "escape", "source": "./rev", "skills": ["./../../../../outside/skills/evil"]},
    {"name": "remote", "source": {"source": "github", "repo": "o/r"}, "skills": ["./skills/remote"]},
    {"name": "noprefix", "source": "plain", "skills": ["./skills/plain"]}
  ]
}`)
	provWriteSkill(t, root, "plugins/rev/skills/review", "review")
	provWriteSkill(t, root, "plugins/remote/skills/remote", "remote")
	provWriteSkill(t, root, "plugins/plain/skills/plain", "plain")
	provWriteSkill(t, parent, "outside/skills/evil", "evil")
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provNames(skills), []string{"review"})
	if skills[0].PluginName != "reviewer" {
		t.Fatalf("PluginName = %q", skills[0].PluginName)
	}
}

func TestDiscoverSkillsPluginJSON(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name": "solo", "skills": ["./skills/a", "skills/b"]}`)
	provWriteSkill(t, root, "skills/a", "a")
	provWriteSkill(t, root, "skills/b", "b")
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range skills {
		got[s.Name] = s.PluginName
	}
	if !reflect.DeepEqual(got, map[string]string{"a": "solo", "b": ""}) {
		t.Fatalf("plugin names = %v", got)
	}

	// Invalid manifests and a pluginRoot without "./" are ignored gracefully.
	root2 := t.TempDir()
	provWriteFile(t, filepath.Join(root2, ".claude-plugin", "marketplace.json"), `{"metadata":{"pluginRoot":"plugins"},"plugins":[{"name":"p","source":"./x","skills":["./skills/s"]}]}`)
	provWriteFile(t, filepath.Join(root2, ".claude-plugin", "plugin.json"), `{not json`)
	provWriteSkill(t, root2, "skills/s", "s")
	provWriteSkill(t, root2, "plugins/x/skills/s2", "s2")
	skills, err = DiscoverSkills(root2, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provNames(skills), []string{"s"})
	if skills[0].PluginName != "" {
		t.Fatalf("PluginName = %q", skills[0].PluginName)
	}
}

func TestDiscoverSkillsSkillsShJSON(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "skills/std", "std")
	provWriteSkill(t, root, "tools/skills/a", "tool-a")
	provWriteSkill(t, root, "tools/skills/group/b", "tool-b")
	provWriteSkill(t, root, "tools/skills", "container-itself")
	skills, _ := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	provEqual(t, provNames(skills), []string{"std"})

	provWriteFile(t, filepath.Join(root, "skills.sh.json"), `{"paths": ["./tools/skills/", "./../escape", "."]}`)
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provSorted(provNames(skills)), []string{"std", "tool-a", "tool-b"})
}

func TestDiscoverSkillsSkipsSourceLockedAgentDirSkills(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteFile(t, filepath.Join(root, "skills-lock.json"),
		`{"version": 1, "skills": {"installed": {"source": "o/r", "sourceType": "github", "computedHash": "abc"}, "kept": {"source": "o/r", "sourceType": "github", "computedHash": "abc"}}}`)
	provWriteSkill(t, root, ".claude/skills/installed", "installed")
	provWriteSkill(t, root, ".agents/skills/own", "own")
	provWriteSkill(t, root, "skills/kept", "kept") // locked but not in an agent dir
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provSorted(provNames(skills)), []string{"kept", "own"})
	skills, _ = DiscoverSkills(root, "", DiscoverSkillsOptions{FullDepth: true})
	provEqual(t, provSorted(provNames(skills)), []string{"kept", "own"})
}

func TestDiscoverSkillsInternalGating(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "skills/pub", "pub")
	provWriteFile(t, filepath.Join(root, "skills", "secret", "SKILL.md"), "---\nname: secret\ndescription: Secret\nmetadata:\n  internal: true\n---\n")
	skills, _ := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	provEqual(t, provNames(skills), []string{"pub"})
	skills, _ = DiscoverSkills(root, "", DiscoverSkillsOptions{IncludeInternal: true})
	provEqual(t, provSorted(provNames(skills)), []string{"pub", "secret"})
	t.Setenv("INSTALL_INTERNAL_SKILLS", "true")
	skills, _ = DiscoverSkills(root, "", DiscoverSkillsOptions{})
	provEqual(t, provSorted(provNames(skills)), []string{"pub", "secret"})
}

func TestDiscoverSkillsInvalidSkillWarns(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "skills/good", "good")
	provWriteFile(t, filepath.Join(root, "skills", "bad", "SKILL.md"), "---\nname: bad\n---\n")
	var buf bytes.Buffer
	old := skillWarningWriter
	skillWarningWriter = &buf
	t.Cleanup(func() { skillWarningWriter = old })
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provNames(skills), []string{"good"})
	if !strings.Contains(buf.String(), "Skipped") || !strings.Contains(buf.String(), "description") {
		t.Fatalf("warning = %q", buf.String())
	}
}

func TestDiscoverSkillsSubpath(t *testing.T) {
	provIsolate(t)
	provQuiet(t)
	root := t.TempDir()
	provWriteSkill(t, root, "packages/x/skills/a", "a")
	provWriteSkill(t, root, "skills/b", "b")
	skills, err := DiscoverSkills(root, "packages/x", DiscoverSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provEqual(t, provNames(skills), []string{"a"})
	if _, err := DiscoverSkills(root, "packages/../..", DiscoverSkillsOptions{}); !IsDiagnostic(err, CodeSubpathOutsideRepo) {
		t.Fatalf("err = %v", err)
	}
	_, err = DiscoverSkills(root, "../outside", DiscoverSkillsOptions{})
	provWantCode(t, err, CodeSubpathOutsideRepo)
	if !IsSubpathSafe(root, "a/../b") || !IsSubpathSafe(root, ".") || IsSubpathSafe(root, "a/../../b") {
		t.Fatalf("IsSubpathSafe mismatch")
	}
}

// ===========================================================================
// FilterSkills

func TestFilterSkills(t *testing.T) {
	skills := []Skill{{Name: "Web Design"}, {Name: "pr_review"}, {Name: "web-design"}, {Name: "Foo"}, {Path: "/x/unnamed"}}
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"web-design"}, []string{"web-design"}},               // exact wins over slug fallback (#2508)
		{[]string{"WEB DESIGN"}, []string{"Web Design"}},               // case-insensitive exact
		{[]string{"pr-review"}, []string{"pr_review"}},                 // slug fallback
		{[]string{"PR Review"}, []string{"pr_review"}},                 // slug fallback
		{[]string{"foo", "Web Design"}, []string{"Web Design", "Foo"}}, // source order
		{[]string{"foo", "FOO"}, []string{"Foo"}},                      // dedupe
		{[]string{"unnamed"}, []string{""}},                            // display name falls back to dir basename
		{[]string{"nope"}, nil},
	}
	for _, tc := range cases {
		got := FilterSkills(skills, tc.in)
		var names []string
		for _, s := range got {
			names = append(names, s.Name)
		}
		if !reflect.DeepEqual(names, tc.want) {
			t.Errorf("FilterSkills(%q) = %q, want %q", tc.in, names, tc.want)
		}
	}
	fetched := []FetchedSkill{{Skill: Skill{Name: "a_b"}}, {Skill: Skill{Name: "c"}}}
	if got := FilterFetchedSkills(fetched, []string{"A B"}); len(got) != 1 || got[0].Name != "a_b" {
		t.Fatalf("FilterFetchedSkills = %+v", got)
	}
	if !MissingRequestedSkill(skills, []string{"foo", "nope"}) || MissingRequestedSkill(skills, []string{"*", "foo"}) || MissingRequestedSkill(skills, nil) {
		t.Fatalf("MissingRequestedSkill mismatch")
	}
}

// ===========================================================================
// FetchSource

func TestFetchSourceLocalDir(t *testing.T) {
	provIsolate(t)
	root := t.TempDir()
	provWriteSkill(t, root, "skills/a", "a")
	provWriteSkill(t, root, "skills/b", "b")
	fs, err := FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: root, LocalPath: root}})
	if err != nil {
		t.Fatal(err)
	}
	if fs.Via != "local" || fs.TempDir != "" || fs.RootDir != root || fs.Source != root || fs.SourceType != SourceTypeLocal {
		t.Fatalf("fs = %+v", fs)
	}
	provEqual(t, provSorted(provNames(fs.SkillList())), []string{"a", "b"})
	if err := fs.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("Cleanup must not touch local sources: %v", err)
	}

	// A SKILL.md file path resolves to its directory.
	md := filepath.Join(root, "skills", "a", "SKILL.md")
	fs, err = FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: md, LocalPath: md}})
	if err != nil || len(fs.Skills) != 1 || fs.Skills[0].Name != "a" || fs.RootDir != filepath.Dir(md) {
		t.Fatalf("fs=%+v err=%v", fs, err)
	}

	missing := filepath.Join(root, "missing")
	_, err = FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: missing, LocalPath: missing}})
	provWantCode(t, err, CodeLocalPathNotFound)
	notDir := filepath.Join(root, "file.txt")
	provWriteFile(t, notDir, "x")
	_, err = FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: notDir, LocalPath: notDir}})
	provWantCode(t, err, CodeLocalPathNotFound)
}

func provGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	provWriteFile(t, cfg, "")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	provWriteSkill(t, repo, "", "root-skill")
	provWriteSkill(t, repo, "skills/nested", "nested")
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestFetchSourceLocalGitRepoRetry(t *testing.T) {
	provIsolate(t)
	repo := provGitRepo(t)
	parsed := &ParsedSource{Type: SourceTypeGit, URL: "file://" + repo}

	// #2596: root SKILL.md short-circuits discovery, but a requested nested
	// skill triggers a full-depth retry.
	fs, err := FetchSource(SourceRequest{Parsed: parsed, Skills: []string{"nested"}})
	if err != nil {
		t.Fatal(err)
	}
	if fs.Via != "clone" || fs.TempDir == "" || fs.RootDir != fs.TempDir || fs.Source == "" {
		t.Fatalf("fs = %+v", fs)
	}
	paths := map[string]string{}
	for _, s := range fs.Skills {
		paths[s.Name] = s.SkillPath
	}
	if !reflect.DeepEqual(paths, map[string]string{"root-skill": "SKILL.md", "nested": "skills/nested/SKILL.md"}) {
		t.Fatalf("skills = %v", paths)
	}
	tmp := fs.TempDir
	if err := fs.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("Cleanup left %s behind (err=%v)", tmp, err)
	}
	if fs.TempDir != "" {
		t.Fatalf("Cleanup should clear TempDir")
	}

	// NoFullDepthRetry keeps the root short-circuit.
	fs, err = FetchSource(SourceRequest{Parsed: parsed, Skills: []string{"nested"}, NoFullDepthRetry: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()
	provEqual(t, provNames(fs.SkillList()), []string{"root-skill"})

	// Requesting "*" does not trigger the retry either.
	fs2, err := FetchSource(SourceRequest{Parsed: parsed, Skills: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	defer fs2.Cleanup()
	provEqual(t, provNames(fs2.SkillList()), []string{"root-skill"})
}

func TestFetchSourceWellKnown(t *testing.T) {
	tmp := provIsolate(t)
	srv := newProvServer(t)
	md := provSkillMD("foo", "Foo")
	srv.set("/.well-known/skills/foo/SKILL.md", []byte(md))
	srv.set("/.well-known/skills/foo/refs/ref.md", []byte("ref"))
	srv.setJSON(t, "/.well-known/skills/index.json", wkV1Index(map[string]interface{}{"name": "foo", "description": "Foo", "files": []string{"SKILL.md", "refs/ref.md"}}))

	fs, err := FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeWellKnown, URL: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	if fs.Via != "well-known" || fs.SourceType != SourceTypeWellKnown || fs.Source != "127.0.0.1" || fs.TempDir == "" || fs.RootDir != fs.TempDir {
		t.Fatalf("fs = %+v", fs)
	}
	if !strings.HasPrefix(fs.TempDir, tmp) {
		t.Fatalf("TempDir %s not under TMPDIR %s", fs.TempDir, tmp)
	}
	if len(fs.Skills) != 1 {
		t.Fatalf("skills = %+v", fs.Skills)
	}
	s := fs.Skills[0]
	if s.InstallName != "foo" || s.Name != "foo" || !strings.HasPrefix(s.Path, fs.TempDir+string(filepath.Separator)) || filepath.Base(s.Path) != "foo" ||
		!strings.HasPrefix(s.WellKnownDigest, "sha256:") ||
		s.SourceURL != srv.URL+"/.well-known/skills/foo/SKILL.md" || s.PackPointer != nil {
		t.Fatalf("skill = %+v", s)
	}
	for rel, want := range map[string]string{"SKILL.md": md, "refs/ref.md": "ref"} {
		if b, err := os.ReadFile(filepath.Join(s.Path, filepath.FromSlash(rel))); err != nil || string(b) != want {
			t.Fatalf("%s = %q, %v", rel, b, err)
		}
	}
	dir := fs.TempDir
	if err := fs.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Cleanup left %s behind", dir)
	}
}

func TestFetchSourceWellKnownFallsBackToDownload(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/bundle", stdZip(t, [][2]string{{"pkg/skills/a/SKILL.md", provSkillMD("a", "A")}, {"pkg/skills/b/SKILL.md", provSkillMD("b", "B")}}, zip.Deflate))
	fs, err := FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeWellKnown, URL: srv.URL + "/bundle"}})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()
	if fs.Via != "download" || fs.SourceType != SourceTypeDownload || fs.Source != srv.URL+"/bundle" || filepath.Base(fs.RootDir) != "pkg" {
		t.Fatalf("fs = %+v", fs)
	}
	paths := map[string]string{}
	for _, s := range fs.Skills {
		paths[s.Name] = s.SkillPath
	}
	if !reflect.DeepEqual(paths, map[string]string{"a": "skills/a/SKILL.md", "b": "skills/b/SKILL.md"}) {
		t.Fatalf("skill paths = %v", paths)
	}

	// Download errors propagate unchanged.
	_, err = FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeDownload, URL: srv.URL + "/missing"}})
	provWantCode(t, err, CodeDownloadHTTPError)
}

func TestFetchSourceWellKnownDigestMismatchPropagates(t *testing.T) {
	provIsolate(t)
	srv := newProvServer(t)
	srv.set("/.well-known/agent-skills/foo/SKILL.md", []byte(provSkillMD("foo", "Foo")))
	srv.setJSON(t, "/.well-known/agent-skills/index.json", wkV2Index(map[string]interface{}{
		"name": "foo", "description": "Foo", "type": "skill-md", "url": "foo/SKILL.md", "digest": provSHA([]byte("other")),
	}))
	_, err := FetchSource(SourceRequest{Parsed: &ParsedSource{Type: SourceTypeWellKnown, URL: srv.URL}})
	provWantCode(t, err, CodeWellknownDigestMismatch)
}

func TestFetchSourceBlob(t *testing.T) {
	md := provSkillMD("foo", "Foo")
	startBlobServer(t, blobFixture{
		tree:      provTree("skills/", "skills/foo/", "skills/foo/SKILL.md", "skills/foo/ref.md"),
		raw:       map[string]string{"skills/foo/SKILL.md": md},
		snapshots: map[string][]SkillSnapshotFile{"foo": snapFiles("SKILL.md", md, "ref.md", "ref")},
	})
	parsed := &ParsedSource{Type: SourceTypeGitHub, URL: "https://github.com/vercel-labs/demo.git"}
	fs, err := FetchSource(SourceRequest{Parsed: parsed})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()
	if fs.Via != "blob" || fs.Source != "vercel-labs/demo" || fs.Tree == nil || len(fs.Skills) != 1 {
		t.Fatalf("fs = %+v", fs)
	}
	s := fs.Skills[0]
	wantHash, err := ComputeSkillFolderHash(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if s.SkillPath != "skills/foo/SKILL.md" || s.FolderHash != "sha-skills/foo" || s.ComputedHash != wantHash ||
		!strings.HasPrefix(s.Path, fs.TempDir+string(filepath.Separator)) || !strings.HasSuffix(filepath.ToSlash(s.Path), "/skills/foo") {
		t.Fatalf("skill = %+v", s)
	}
	if b, err := os.ReadFile(filepath.Join(s.Path, "ref.md")); err != nil || string(b) != "ref" {
		t.Fatalf("ref.md = %q, %v", b, err)
	}

	// A requested skill missing from the snapshot falls back to cloning; with
	// git unavailable (PATH emptied) and no --yes this fails without network.
	_, err = FetchSource(SourceRequest{Parsed: parsed, Skills: []string{"other"}})
	provWantCode(t, err, CodeGitCloneFailed)
}

func TestFetchSourcesKeepsOrder(t *testing.T) {
	provIsolate(t)
	a, b := t.TempDir(), t.TempDir()
	provWriteSkill(t, a, "", "first")
	provWriteSkill(t, b, "", "second")
	missing := filepath.Join(a, "missing")
	out, errs := FetchSources([]SourceRequest{
		{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: a, LocalPath: a}},
		{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: missing, LocalPath: missing}},
		{Parsed: &ParsedSource{Type: SourceTypeLocal, URL: b, LocalPath: b}},
	})
	if errs[0] != nil || errs[2] != nil || out[0].Skills[0].Name != "first" || out[2].Skills[0].Name != "second" {
		t.Fatalf("out=%v errs=%v", out, errs)
	}
	provWantCode(t, errs[1], CodeLocalPathNotFound)
}

// ===========================================================================
// Review #41 regressions

// rawTarHeader builds a 512-byte ustar header block with a valid checksum.
func rawTarHeader(name string, typeflag byte, size int64) []byte {
	h := make([]byte, 512)
	copy(h[0:100], name)
	copy(h[100:108], "0000644\x00")
	copy(h[108:116], "0000000\x00")
	copy(h[116:124], "0000000\x00")
	copy(h[124:136], []byte(strings.Repeat("0", 11-len(strconvOctal(size)))+strconvOctal(size)+"\x00"))
	copy(h[136:148], "00000000000\x00")
	h[156] = typeflag
	copy(h[257:263], "ustar\x00")
	copy(h[263:265], "00")
	for i := 148; i < 156; i++ {
		h[i] = ' '
	}
	var sum int64
	for _, b := range h {
		sum += int64(b)
	}
	cs := strconvOctal(sum)
	copy(h[148:156], strings.Repeat("0", 6-len(cs))+cs+"\x00 ")
	return h
}

func strconvOctal(n int64) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%8)}, out...)
		n /= 8
	}
	return string(out)
}

func padTar(b []byte) []byte {
	if r := len(b) % 512; r != 0 {
		b = append(b, make([]byte, 512-r)...)
	}
	return b
}

// Review #41 item 1: GNU long-name records are read by archive/tar before
// Next returns; a flood of them must hit the decompressed-stream cap.
func TestExtractTarArchiveCapsDecompressedStream(t *testing.T) {
	var raw []byte
	record := bytes.Repeat([]byte("a"), 600*1024)
	for i := 0; i < 4; i++ {
		raw = append(raw, rawTarHeader("././@LongLink", 'L', int64(len(record)))...)
		raw = append(raw, padTar(append([]byte{}, record...))...)
	}
	raw = append(raw, rawTarHeader("f.txt", '0', 1)...)
	raw = append(raw, padTar([]byte("x"))...)
	raw = append(raw, make([]byte, 1024)...)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	err := ExtractTarArchive(bytes.NewReader(gz.Bytes()), t.TempDir(), true, 1024, 1)
	if !IsDiagnostic(err, CodeArchiveTooLarge) {
		t.Fatalf("long-name flood err = %v; want SKILLS_ARCHIVE_TOO_LARGE", err)
	}
}

func tarBytes(t *testing.T, entries []tar.Header, data map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		h := h
		content := data[h.Name]
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(content))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(content))
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

// Review #41 item 7: drive-letter directory/file names and dot segments.
func TestExtractTarArchiveRejectsDriveAndDotPaths(t *testing.T) {
	for _, h := range []tar.Header{
		{Name: "C:/dir/", Typeflag: tar.TypeDir},
		{Name: "C:file", Typeflag: tar.TypeReg},
		{Name: ".", Typeflag: tar.TypeReg},
		{Name: "sub/.", Typeflag: tar.TypeReg},
	} {
		data := tarBytes(t, []tar.Header{h}, map[string]string{h.Name: "x"})
		err := ExtractTarArchive(bytes.NewReader(data), t.TempDir(), false, 1<<20, 10)
		if !IsDiagnostic(err, CodeArchiveUnsafePath) {
			t.Errorf("%q: err = %v; want SKILLS_ARCHIVE_UNSAFE_PATH", h.Name, err)
		}
	}
	ok := tarBytes(t, []tar.Header{{Name: "./", Typeflag: tar.TypeDir}, {Name: "./pkg/SKILL.md", Typeflag: tar.TypeReg}}, map[string]string{"./pkg/SKILL.md": "hi"})
	dest := t.TempDir()
	if err := ExtractTarArchive(bytes.NewReader(ok), dest, false, 1<<20, 10); err != nil {
		t.Fatalf("./ prefixed archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "pkg", "SKILL.md")); err != nil {
		t.Error(err)
	}
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// Review #41 item 6: well-known tar.gz entry counting, duplicates and truncation.
func TestExtractWellKnownTarGzHardening(t *testing.T) {
	var many []tar.Header
	data := map[string]string{"SKILL.md": "---\nname: a\ndescription: b\n---\n"}
	for i := 0; i < wellKnownMaxArchiveFiles+1; i++ {
		many = append(many, tar.Header{Name: "SKILL.md", Typeflag: tar.TypeReg})
	}
	if _, err := extractWellKnownTarGz(gzipBytes(tarBytes(t, many, data))); !IsDiagnostic(err, CodeWellknownArchiveTooManyFiles) {
		t.Errorf("duplicate names err = %v", err)
	}
	var dirs []tar.Header
	for i := 0; i < wellKnownMaxArchiveFiles+1; i++ {
		dirs = append(dirs, tar.Header{Name: "d" + strconvOctal(int64(i)) + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	}
	if _, err := extractWellKnownTarGz(gzipBytes(tarBytes(t, dirs, nil))); !IsDiagnostic(err, CodeWellknownArchiveTooManyFiles) {
		t.Errorf("directory entries must count, err = %v", err)
	}
	full := tarBytes(t, []tar.Header{{Name: "SKILL.md", Typeflag: tar.TypeReg}}, map[string]string{"SKILL.md": strings.Repeat("z", 2000)})
	truncated := full[:512+700]
	if _, err := extractWellKnownTarGz(gzipBytes(truncated)); !IsDiagnostic(err, CodeArchiveInvalid) {
		t.Errorf("truncated entry err = %v", err)
	}
	if files, err := extractWellKnownTarGz(gzipBytes(full)); err != nil || len(files["SKILL.md"]) != 2000 {
		t.Errorf("valid archive: %v", err)
	}
}

// Review #41 item 4: discovery must not follow a SKILL.md symlink that
// escapes its skill directory.
func TestDiscoverSkillsIgnoresEscapingSkillMdSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := t.TempDir()
	writeFileT(t, filepath.Join(outside, "SKILL.md"), "---\nname: stolen\ndescription: host file\n---\n")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "SKILL.md"), filepath.Join(root, "skills", "evil", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, filepath.Join(root, "skills", "good", "real.md"), "---\nname: good\ndescription: ok\n---\n")
	if err := os.Symlink("real.md", filepath.Join(root, "skills", "good", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	old := skillWarningWriter
	skillWarningWriter = io.Discard
	defer func() { skillWarningWriter = old }()
	skills, err := DiscoverSkills(root, "", DiscoverSkillsOptions{FullDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "good" {
		t.Fatalf("skills = %+v; want only the confined symlink skill", skills)
	}
}

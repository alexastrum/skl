package skl

// Fetch providers: archives, direct downloads, GitHub blob snapshots,
// well-known indexes, plugin manifests, skill discovery and the high-level
// FetchSource API. Ports upstream src/archive.ts, src/download-source.ts,
// src/blob.ts, src/providers/wellknown.ts, src/plugin-manifest.ts,
// src/skills.ts and src/concurrency.ts.

import (
	"archive/tar"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ===========================================================================
// Shared helpers

// DefaultSkillContainerDepth is the maximum skill-directory depth searched
// inside a known container (skills/, agent dirs, skills.sh.json paths).
const DefaultSkillContainerDepth = 3

// fpSkipDirs mirrors SKIP_DIRS in upstream src/skills.ts and src/blob.ts.
var fpSkipDirs = map[string]bool{"node_modules": true, ".git": true, "dist": true, "build": true, "__pycache__": true}

// providerHTTPClient is used for every provider request (overridable in tests).
var providerHTTPClient = &http.Client{}

// FetchConcurrency returns SKILLS_CLONE_CONCURRENCY (default 4).
func FetchConcurrency() int {
	if n, err := strconv.Atoi(os.Getenv("SKILLS_CLONE_CONCURRENCY")); err == nil && n > 0 {
		return n
	}
	return 4
}

// fpMapConcurrent runs fn over n items with at most limit in flight.
func fpMapConcurrent(n, limit int, fn func(i int)) {
	if limit <= 0 {
		limit = 1
	}
	if limit > n {
		limit = n
	}
	var next int64 = -1
	var wg sync.WaitGroup
	for w := 0; w < limit; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

func fpPositiveEnvInt64(name string, fallback int64) int64 {
	if raw := os.Getenv(name); raw != "" {
		if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// fpIsPathWithin reports whether target is base or inside base (lexically).
func fpIsPathWithin(base, target string) bool {
	b, err1 := filepath.Abs(base)
	t, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return false
	}
	b = filepath.Clean(b)
	t = filepath.Clean(t)
	return t == b || strings.HasPrefix(t, b+string(filepath.Separator))
}

func fpNormalizeSkillName(name string) string {
	return wsUnderscoreRe.ReplaceAllString(strings.ToLower(name), "-")
}

var (
	wsUnderscoreRe = regexp.MustCompile(`[\s_]+`)
	nonSlugRe      = regexp.MustCompile(`[^a-z0-9-]`)
	multiDashRe    = regexp.MustCompile(`-+`)
)

// ToSkillSlug converts a skill name to a URL-safe slug (matches the
// skills.sh server-side toSkillSlug()).
func ToSkillSlug(name string) string {
	s := strings.ToLower(name)
	s = wsUnderscoreRe.ReplaceAllString(s, "-")
	s = nonSlugRe.ReplaceAllString(s, "")
	s = multiDashRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ===========================================================================
// Archives (upstream src/archive.ts)

// ArchiveLimits bounds archive extraction.
type ArchiveLimits struct {
	DownloadMaxBytes int64 // default 10 MiB (SKILLS_DOWNLOAD_MAX_BYTES)
	ExtractMaxBytes  int64 // default 25 MiB (SKILLS_EXTRACT_MAX_BYTES)
	ExtractMaxFiles  int   // default 1000 (SKILLS_EXTRACT_MAX_FILES)
}

const (
	zipLocalFileHeader         = 0x04034b50
	zipCentralDirectoryHeader  = 0x02014b50
	zipEndOfCentralDirectory   = 0x06054b50
	zip64EndOfCentralDirectory = 0x06064b50
	zip64EndLocator            = 0x07064b50
	zipEndMinSize              = 22
	zipMaxCommentSize          = 0xffff
	maxSafeInteger             = 1<<53 - 1
)

var fpCP437High = []rune("ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■\u00a0")

func fpArchiveInvalid(reason string) *DiagnosticError {
	return NewDiagnostic(CodeArchiveInvalid, reason, "")
}

type zipBuf []byte

func (b zipBuf) ensure(off, length int64, label string) error {
	if off < 0 || length < 0 || off+length > int64(len(b)) || off > maxSafeInteger || length > maxSafeInteger {
		return fpArchiveInvalid("Invalid zip archive: " + label + " is out of bounds")
	}
	return nil
}
func (b zipBuf) u16(off int64) uint16 { return binary.LittleEndian.Uint16(b[off:]) }
func (b zipBuf) u32(off int64) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
func (b zipBuf) u64num(off int64, label string) (int64, error) {
	if err := b.ensure(off, 8, label); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint64(b[off:])
	if v > maxSafeInteger {
		return 0, fpArchiveInvalid("Invalid zip archive: " + label + " exceeds the safe integer range")
	}
	return int64(v), nil
}

func findEndOfCentralDirectory(b zipBuf) int64 {
	if len(b) < zipEndMinSize {
		return -1
	}
	minOff := int64(len(b)) - zipMaxCommentSize - zipEndMinSize
	if minOff < 0 {
		minOff = 0
	}
	for off := int64(len(b)) - zipEndMinSize; off >= minOff; off-- {
		if b.u32(off) != zipEndOfCentralDirectory {
			continue
		}
		if off+zipEndMinSize+int64(b.u16(off+20)) == int64(len(b)) {
			return off
		}
	}
	return -1
}

type zipCentralDir struct {
	entries, offset, size, trailerOffset int64
}

func readZipCentralDirectory(b zipBuf, end int64) (zipCentralDir, error) {
	diskNumber := b.u16(end + 4)
	cdDisk := b.u16(end + 6)
	entriesOnDisk := b.u16(end + 8)
	totalEntries := b.u16(end + 10)
	size := b.u32(end + 12)
	offset := b.u32(end + 16)
	usesZip64 := entriesOnDisk == 0xffff || totalEntries == 0xffff || size == 0xffffffff || offset == 0xffffffff
	if !usesZip64 {
		if diskNumber != 0 || cdDisk != 0 || entriesOnDisk != totalEntries {
			return zipCentralDir{}, NewDiagnostic(CodeArchiveMultiDiskZip, "Multi-disk zip archives are not supported", "")
		}
		return zipCentralDir{int64(totalEntries), int64(offset), int64(size), end}, nil
	}
	multi := NewDiagnostic(CodeArchiveMultiDiskZip, "Multi-disk zip archives are not supported", "")
	if diskNumber != 0 || cdDisk != 0 {
		return zipCentralDir{}, multi
	}
	loc := end - 20
	if err := b.ensure(loc, 20, "zip64 locator"); err != nil {
		return zipCentralDir{}, err
	}
	if b.u32(loc) != zip64EndLocator {
		return zipCentralDir{}, fpArchiveInvalid("Invalid zip64 locator")
	}
	if b.u32(loc+4) != 0 || b.u32(loc+16) != 1 {
		return zipCentralDir{}, multi
	}
	z64, err := b.u64num(loc+8, "zip64 end offset")
	if err != nil {
		return zipCentralDir{}, err
	}
	if err := b.ensure(z64, 56, "zip64 end of central directory"); err != nil {
		return zipCentralDir{}, err
	}
	if b.u32(z64) != zip64EndOfCentralDirectory {
		return zipCentralDir{}, fpArchiveInvalid("Invalid zip64 end of central directory")
	}
	recordSize, err := b.u64num(z64+4, "zip64 end size")
	if err != nil {
		return zipCentralDir{}, err
	}
	if recordSize < 44 {
		return zipCentralDir{}, fpArchiveInvalid("Invalid zip64 end of central directory")
	}
	if err := b.ensure(z64, recordSize+12, "zip64 end of central directory"); err != nil {
		return zipCentralDir{}, err
	}
	if z64+recordSize+12 != loc {
		return zipCentralDir{}, fpArchiveInvalid("Invalid zip64 end of central directory")
	}
	if b.u32(z64+16) != 0 || b.u32(z64+20) != 0 {
		return zipCentralDir{}, multi
	}
	onDisk, err := b.u64num(z64+24, "zip64 entries on disk")
	if err != nil {
		return zipCentralDir{}, err
	}
	total, err := b.u64num(z64+32, "zip64 total entries")
	if err != nil {
		return zipCentralDir{}, err
	}
	if onDisk != total {
		return zipCentralDir{}, multi
	}
	cdSize, err := b.u64num(z64+40, "zip64 central directory size")
	if err != nil {
		return zipCentralDir{}, err
	}
	cdOff, err := b.u64num(z64+48, "zip64 central directory offset")
	if err != nil {
		return zipCentralDir{}, err
	}
	return zipCentralDir{total, cdOff, cdSize, z64}, nil
}

// normalizeZipPath mirrors upstream normalizeArchivePath ("" + ok=false = unsafe).
func normalizeZipPath(raw string) (string, bool) {
	if raw == "" || strings.ContainsRune(raw, 0) {
		return "", false
	}
	p := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(p, "/") || (len(p) >= 2 && fpIsASCIILetter(p[0]) && p[1] == ':') {
		return "", false
	}
	parts := strings.Split(p, "/")
	var kept []string
	for _, part := range parts {
		if part == ".." {
			return "", false
		}
		if part != "" && part != "." {
			kept = append(kept, part)
		}
	}
	normalized := strings.Join(kept, "/")
	if normalized == "" && !strings.HasSuffix(p, "/") {
		return "", false
	}
	if strings.HasSuffix(p, "/") && normalized != "" {
		return normalized + "/", true
	}
	return normalized, true
}

func fpIsASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func findZipExtraField(b zipBuf, extraOff, extraLen int64, target uint16) ([]byte, error) {
	end := extraOff + extraLen
	off := extraOff
	for off < end {
		if off+4 > end {
			return nil, fpArchiveInvalid("Invalid zip extra field")
		}
		id := b.u16(off)
		size := int64(b.u16(off + 2))
		data := off + 4
		if err := b.ensure(data, size, "zip extra field"); err != nil {
			return nil, err
		}
		if data+size > end {
			return nil, fpArchiveInvalid("Invalid zip extra field")
		}
		if id == target {
			return b[data : data+size], nil
		}
		off = data + size
	}
	return nil, nil
}

func decodeZipFileName(name []byte, isUTF8 bool, unicodeExtra []byte) (string, error) {
	if isUTF8 {
		if !utf8.Valid(name) {
			return "", fpArchiveInvalid("Invalid UTF-8 zip entry name")
		}
		return string(name), nil
	}
	if len(unicodeExtra) >= 5 && unicodeExtra[0] == 1 && binary.LittleEndian.Uint32(unicodeExtra[1:]) == crc32.ChecksumIEEE(name) {
		rest := unicodeExtra[5:]
		if !utf8.Valid(rest) {
			return "", fpArchiveInvalid("Invalid UTF-8 zip entry name")
		}
		return string(rest), nil
	}
	var sb strings.Builder
	for _, c := range name {
		if c < 0x80 {
			sb.WriteByte(c)
		} else {
			sb.WriteRune(fpCP437High[c-0x80])
		}
	}
	return sb.String(), nil
}

func readZip64EntryValues(b zipBuf, extraOff, extraLen int64, comp, uncomp, localOff, diskStart int64) (int64, int64, int64, error) {
	multi := NewDiagnostic(CodeArchiveMultiDiskZip, "Multi-disk zip archives are not supported", "")
	needs := uncomp == 0xffffffff || comp == 0xffffffff || localOff == 0xffffffff || diskStart == 0xffff
	if !needs {
		if diskStart != 0 {
			return 0, 0, 0, multi
		}
		return comp, uncomp, localOff, nil
	}
	extra, err := findZipExtraField(b, extraOff, extraLen, 0x0001)
	if err != nil {
		return 0, 0, 0, err
	}
	if extra == nil {
		return 0, 0, 0, fpArchiveInvalid("Invalid zip64 extra field")
	}
	eb := zipBuf(extra)
	var vo int64
	next := func(label string) (int64, error) {
		if vo+8 > int64(len(eb)) {
			return 0, fpArchiveInvalid("Invalid zip64 extra field: missing " + label)
		}
		v, err := eb.u64num(vo, "zip64 "+label)
		vo += 8
		return v, err
	}
	if uncomp == 0xffffffff {
		if uncomp, err = next("uncompressed size"); err != nil {
			return 0, 0, 0, err
		}
	}
	if comp == 0xffffffff {
		if comp, err = next("compressed size"); err != nil {
			return 0, 0, 0, err
		}
	}
	if localOff == 0xffffffff {
		if localOff, err = next("local header offset"); err != nil {
			return 0, 0, 0, err
		}
	}
	if diskStart == 0xffff {
		if vo+4 > int64(len(eb)) {
			return 0, 0, 0, fpArchiveInvalid("Invalid zip64 extra field: missing disk start")
		}
		diskStart = int64(eb.u32(vo))
	}
	if diskStart != 0 {
		return 0, 0, 0, multi
	}
	return comp, uncomp, localOff, nil
}

// ReadZipArchive parses a zip (or .skill zip bundle) fully in memory and
// returns its regular files keyed by normalized path. It rejects multi-disk
// archives, encrypted entries, links/special files, unsafe paths, too many
// entries (directories count), oversized content, local/central header
// mismatches and CRC mismatches. Supports stored and deflated entries, Zip64,
// the UTF-8 flag, the Info-ZIP Unicode Path extra field and CP437 names.
func ReadZipArchive(data []byte, maxExtractedBytes int64, maxEntries int) (map[string][]byte, error) {
	b := zipBuf(data)
	end := findEndOfCentralDirectory(b)
	if end < 0 {
		return nil, fpArchiveInvalid("Invalid zip archive")
	}
	cd, err := readZipCentralDirectory(b, end)
	if err != nil {
		return nil, err
	}
	if cd.entries > int64(maxEntries) {
		return nil, Diagnosticf(CodeArchiveTooManyFiles, "Archive contains too many files (%d). Maximum is %d.", cd.entries, maxEntries)
	}
	if err := b.ensure(cd.offset, cd.size, "central directory"); err != nil {
		return nil, err
	}
	if cd.offset+cd.size > cd.trailerOffset {
		return nil, fpArchiveInvalid("Invalid zip archive: central directory overlaps archive trailer")
	}

	files := map[string][]byte{}
	var extracted int64
	off := cd.offset
	for i := int64(0); i < cd.entries; i++ {
		if err := b.ensure(off, 46, "central directory entry"); err != nil {
			return nil, err
		}
		if b.u32(off) != zipCentralDirectoryHeader {
			return nil, fpArchiveInvalid("Invalid zip central directory entry")
		}
		flags := b.u16(off + 8)
		method := b.u16(off + 10)
		expectedCRC := b.u32(off + 16)
		comp := int64(b.u32(off + 20))
		uncomp := int64(b.u32(off + 24))
		nameLen := int64(b.u16(off + 28))
		extraLen := int64(b.u16(off + 30))
		commentLen := int64(b.u16(off + 32))
		diskStart := int64(b.u16(off + 34))
		extAttrs := b.u32(off + 38)
		localOff := int64(b.u32(off + 42))
		variable := nameLen + extraLen + commentLen
		if err := b.ensure(off+46, variable, "central directory entry data"); err != nil {
			return nil, err
		}
		nameStart := off + 46
		extraOff := nameStart + nameLen
		comp, uncomp, localOff, err = readZip64EntryValues(b, extraOff, extraLen, comp, uncomp, localOff, diskStart)
		if err != nil {
			return nil, err
		}
		centralName := b[nameStart : nameStart+nameLen]
		unicodeExtra, err := findZipExtraField(b, extraOff, extraLen, 0x7075)
		if err != nil {
			return nil, err
		}
		rawName, err := decodeZipFileName(centralName, flags&0x800 != 0, unicodeExtra)
		if err != nil {
			return nil, err
		}
		fileName, ok := normalizeZipPath(rawName)
		if !ok {
			return nil, Diagnosticf(CodeArchiveUnsafePath, "Archive contains unsafe path: %s", rawName)
		}
		if flags&0x1 != 0 {
			return nil, NewDiagnostic(CodeArchiveEncryptedEntry, "Encrypted zip entries are not supported", "")
		}
		fileType := (extAttrs >> 16) & 0o170000
		if fileType != 0 && fileType != 0o100000 && fileType != 0o040000 {
			return nil, NewDiagnostic(CodeArchiveLinkEntry, "Archive links are not supported", "")
		}
		isDir := strings.HasSuffix(strings.ReplaceAll(rawName, `\`, "/"), "/") || fileType == 0o040000
		off += 46 + variable

		extracted += uncomp
		if extracted > maxExtractedBytes {
			return nil, Diagnosticf(CodeArchiveTooLarge, "Archive extracts to more than %d bytes.", maxExtractedBytes)
		}
		if isDir {
			continue
		}

		if err := b.ensure(localOff, 30, "local file header"); err != nil {
			return nil, err
		}
		if b.u32(localOff) != zipLocalFileHeader {
			return nil, fpArchiveInvalid("Invalid zip local file header")
		}
		localFlags := b.u16(localOff + 6)
		localMethod := b.u16(localOff + 8)
		localNameLen := int64(b.u16(localOff + 26))
		localExtraLen := int64(b.u16(localOff + 28))
		localNameOff := localOff + 30
		if err := b.ensure(localNameOff, localNameLen+localExtraLen, "local file header data"); err != nil {
			return nil, err
		}
		localName := b[localNameOff : localNameOff+localNameLen]
		if localFlags != flags || localMethod != method || !bytes.Equal(localName, centralName) {
			return nil, fpArchiveInvalid("Zip local header does not match central directory")
		}
		dataOff := localOff + 30 + localNameLen + localExtraLen
		if err := b.ensure(dataOff, comp, "file data"); err != nil {
			return nil, err
		}
		if dataOff+comp > cd.offset {
			return nil, fpArchiveInvalid("Invalid zip archive: file data overlaps central directory")
		}
		compressed := b[dataOff : dataOff+comp]
		var contents []byte
		switch method {
		case 0:
			contents = compressed
		case 8:
			fr := flate.NewReader(bytes.NewReader(compressed))
			contents, err = io.ReadAll(io.LimitReader(fr, uncomp+1))
			_ = fr.Close()
			if err != nil {
				return nil, fpArchiveInvalid("Invalid zip archive: " + err.Error())
			}
		default:
			return nil, Diagnosticf(CodeArchiveUnsupportedCompression, "Unsupported zip compression method: %d", method)
		}
		if int64(len(contents)) != uncomp {
			return nil, fpArchiveInvalid("Zip entry size mismatch")
		}
		if crc32.ChecksumIEEE(contents) != expectedCRC {
			return nil, fpArchiveInvalid("Zip entry checksum mismatch")
		}
		out := make([]byte, len(contents))
		copy(out, contents)
		files[fileName] = out
	}
	if off != cd.offset+cd.size {
		return nil, fpArchiveInvalid("Invalid zip central directory size")
	}
	return files, nil
}

// validateTarPath mirrors download-source.ts validateArchivePath. ok=false is unsafe.
func validateTarPath(p string) (string, bool) {
	n := strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
	if strings.ContainsRune(n, 0) {
		return "", false
	}
	if n == "" || strings.HasSuffix(n, "/") {
		if strings.HasPrefix(n, "/") || fpHasDotDot(n) {
			return "", false
		}
		return n, true
	}
	if strings.HasPrefix(n, "/") || (len(n) >= 3 && fpIsASCIILetter(n[0]) && n[1] == ':' && n[2] == '/') {
		return "", false
	}
	if fpHasDotDot(n) {
		return "", false
	}
	return n, true
}

func fpHasDotDot(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return true
		}
	}
	return false
}

// ExtractTarArchive extracts a (optionally gzipped) tar stream into destDir.
// Every entry (directories included) counts toward maxEntries and its size
// toward maxExtractedBytes. Unsafe paths and symlink/hardlink entries are
// rejected; other special entries are skipped. Files are written 0644, or
// 0755 when any executable bit is set (never setuid/setgid).
func ExtractTarArchive(r io.Reader, destDir string, isGzip bool, maxExtractedBytes int64, maxEntries int) error {
	if isGzip {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fpArchiveInvalid("Invalid gzip archive: " + err.Error())
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	var entries int
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			if entries == 0 {
				return fpArchiveInvalid("Empty or invalid tar archive")
			}
			return nil
		}
		if err != nil {
			return fpArchiveInvalid("Invalid tar archive: " + err.Error())
		}
		safe, ok := validateTarPath(hdr.Name)
		if !ok {
			return Diagnosticf(CodeArchiveUnsafePath, "Archive contains unsafe path: %s", hdr.Name)
		}
		target := filepath.Join(destDir, filepath.FromSlash(safe))
		if !fpIsPathWithin(destDir, target) {
			return Diagnosticf(CodeArchiveUnsafePath, "Archive contains unsafe path: %s", hdr.Name)
		}
		entries++
		if entries > maxEntries {
			return Diagnosticf(CodeArchiveTooManyFiles, "Archive contains too many files (%d). Maximum is %d. Set SKILLS_EXTRACT_MAX_FILES to override.", entries, maxEntries)
		}
		if hdr.Size < 0 {
			return fpArchiveInvalid("Invalid tar entry size")
		}
		total += hdr.Size
		if total > maxExtractedBytes {
			return Diagnosticf(CodeArchiveTooLarge, "Archive extracts to more than %d bytes. Set SKILLS_EXTRACT_MAX_BYTES to override.", maxExtractedBytes)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			return NewDiagnostic(CodeArchiveLinkEntry, "Archive links are not supported", "")
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA, tar.TypeCont: //nolint:staticcheck // TypeRegA kept for old archives
			if safe == "" || strings.HasSuffix(safe, "/") {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, hdr.Size))
			cerr := f.Close()
			if err != nil {
				return fpArchiveInvalid("Invalid tar archive: " + err.Error())
			}
			if cerr != nil {
				return cerr
			}
			if n != hdr.Size {
				return fpArchiveInvalid("Invalid tar archive: truncated entry")
			}
		default:
			// Devices, FIFOs and metadata entries are skipped.
		}
	}
}

// fpIsRejectedArchive reports a readable archive whose contents are refused
// (as opposed to bytes that are not an archive at all).
func fpIsRejectedArchive(err error) bool {
	return IsDiagnostic(err, CodeArchiveUnsafePath, CodeArchiveEncryptedEntry, CodeArchiveLinkEntry,
		CodeArchiveTooManyFiles, CodeArchiveTooLarge)
}

// ===========================================================================
// Direct downloads (upstream src/download-source.ts)

// DownloadedSource is a URL materialized into a temp dir.
type DownloadedSource struct {
	RootDir string
	TempDir string
	Kind    string // "skill-md" | "archive"
}

// DownloadSourceOptions overrides the default limits (env vars still win).
type DownloadSourceOptions struct {
	DownloadMaxBytes int64
	ExtractMaxBytes  int64
	ExtractMaxFiles  int
}

func fpDownloadLimits(opts []DownloadSourceOptions) ArchiveLimits {
	var o DownloadSourceOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	pick := func(v, def int64) int64 {
		if v > 0 {
			return v
		}
		return def
	}
	return ArchiveLimits{
		DownloadMaxBytes: fpPositiveEnvInt64("SKILLS_DOWNLOAD_MAX_BYTES", pick(o.DownloadMaxBytes, 10*1024*1024)),
		ExtractMaxBytes:  fpPositiveEnvInt64("SKILLS_EXTRACT_MAX_BYTES", pick(o.ExtractMaxBytes, 25*1024*1024)),
		ExtractMaxFiles:  int(fpPositiveEnvInt64("SKILLS_EXTRACT_MAX_FILES", pick(int64(o.ExtractMaxFiles), 1000))),
	}
}

func fpDownloadToFile(rawURL, target string, limits ArchiveLimits) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Diagnosticf(CodeDownloadHTTPError, "Download failed with HTTP %d", resp.StatusCode)
	}
	tooLarge := Diagnosticf(CodeDownloadTooLarge, "Download is larger than %d bytes. Set SKILLS_DOWNLOAD_MAX_BYTES to override.", limits.DownloadMaxBytes)
	if resp.ContentLength > limits.DownloadMaxBytes {
		return tooLarge
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limits.DownloadMaxBytes+1))
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if n > limits.DownloadMaxBytes {
		return tooLarge
	}
	return nil
}

func fpIsValidSkillMarkdown(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	fm, _, err := ParseFrontmatter(string(data))
	if err != nil || fm == nil {
		return false
	}
	_, n := fm["name"].(string)
	_, d := fm["description"].(string)
	return n && d
}

func fpTryExtractArchive(file, extractDir string, limits ArchiveLimits) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	reset := func() {
		_ = os.RemoveAll(extractDir)
		_ = os.MkdirAll(extractDir, 0o755)
	}
	var extractErr error
	switch {
	case len(data) >= 2 && data[0] == 0x50 && data[1] == 0x4b:
		var files map[string][]byte
		files, extractErr = ReadZipArchive(data, limits.ExtractMaxBytes, limits.ExtractMaxFiles)
		if extractErr == nil {
			extractErr = fpWriteArchiveFiles(extractDir, files)
		}
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		extractErr = ExtractTarArchive(bytes.NewReader(data), extractDir, true, limits.ExtractMaxBytes, limits.ExtractMaxFiles)
	default:
		extractErr = ExtractTarArchive(bytes.NewReader(data), extractDir, false, limits.ExtractMaxBytes, limits.ExtractMaxFiles)
	}
	if extractErr != nil {
		reset()
		if fpIsRejectedArchive(extractErr) {
			return false, extractErr
		}
		return false, nil
	}
	return true, nil
}

func fpWriteArchiveFiles(dir string, files map[string][]byte) error {
	for p, contents := range files {
		target := filepath.Join(dir, filepath.FromSlash(p))
		if !fpIsPathWithin(dir, target) {
			return Diagnosticf(CodeArchiveUnsafePath, "Archive contains unsafe path: %s", p)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, contents, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fpSingleTopLevelDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var visible []os.DirEntry
	for _, e := range entries {
		if e.Name() != "__MACOSX" {
			visible = append(visible, e)
		}
	}
	if len(visible) != 1 || !visible[0].IsDir() {
		return ""
	}
	return filepath.Join(dir, visible[0].Name())
}

// DownloadSource downloads rawURL into a "skills-download-*" temp dir. A
// valid SKILL.md (string name+description) is staged as <tmp>/skill/SKILL.md;
// otherwise the bytes are sniffed and extracted as zip/.skill, tar.gz/.tgz or
// tar. A single visible top-level directory becomes RootDir.
func DownloadSource(rawURL string, opts ...DownloadSourceOptions) (*DownloadedSource, error) {
	limits := fpDownloadLimits(opts)
	tempDir, err := os.MkdirTemp("", "skills-download-")
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*DownloadedSource, error) {
		_ = os.RemoveAll(tempDir)
		return nil, e
	}
	file := filepath.Join(tempDir, "source.download")
	extractDir := filepath.Join(tempDir, "extract")
	if err := fpDownloadToFile(rawURL, file, limits); err != nil {
		return fail(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return fail(err)
	}
	if len(data) == 0 {
		return fail(NewDiagnostic(CodeDownloadEmpty, "Downloaded URL is empty", ""))
	}
	if fpIsValidSkillMarkdown(data) {
		skillDir := filepath.Join(tempDir, "skill")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), data, 0o644); err != nil {
			return fail(err)
		}
		return &DownloadedSource{RootDir: skillDir, TempDir: tempDir, Kind: "skill-md"}, nil
	}
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fail(err)
	}
	ok, err := fpTryExtractArchive(file, extractDir, limits)
	if err != nil {
		return fail(err)
	}
	if ok {
		root := fpSingleTopLevelDir(extractDir)
		if root == "" {
			root = extractDir
		}
		return &DownloadedSource{RootDir: root, TempDir: tempDir, Kind: "archive"}, nil
	}
	return fail(NewDiagnostic(CodeDownloadNotASkill, "Downloaded URL is not a valid SKILL.md file or supported archive", ""))
}

// ===========================================================================
// Plugin manifests (upstream src/plugin-manifest.ts)

type pluginManifestEntry struct {
	Source json.RawMessage `json:"source"`
	Skills []string        `json:"skills"`
	Name   string          `json:"name"`
}

type marketplaceManifest struct {
	Metadata *struct {
		PluginRoot *string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []pluginManifestEntry `json:"plugins"`
}

type pluginJSONManifest struct {
	Skills []string `json:"skills"`
	Name   string   `json:"name"`
}

// pluginSource returns (source, hasSource, isLocalString).
func (p pluginManifestEntry) pluginSource() (string, bool, bool) {
	raw := bytes.TrimSpace(p.Source)
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", true, false
	}
	return s, true, true
}

func readMarketplace(basePath string) (*marketplaceManifest, string, bool) {
	data, err := os.ReadFile(filepath.Join(basePath, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return nil, "", false
	}
	var m marketplaceManifest
	if json.Unmarshal(data, &m) != nil {
		return nil, "", false
	}
	root := ""
	if m.Metadata != nil && m.Metadata.PluginRoot != nil {
		root = *m.Metadata.PluginRoot
		if !strings.HasPrefix(root, "./") {
			return nil, "", false
		}
	}
	return &m, root, true
}

func readPluginJSON(basePath string) (*pluginJSONManifest, bool) {
	data, err := os.ReadFile(filepath.Join(basePath, ".claude-plugin", "plugin.json"))
	if err != nil {
		return nil, false
	}
	var m pluginJSONManifest
	if json.Unmarshal(data, &m) != nil {
		return nil, false
	}
	return &m, true
}

// GetPluginSkillPaths returns skill container dirs declared by
// .claude-plugin/marketplace.json and plugin.json under basePath.
func GetPluginSkillPaths(basePath string) []string {
	var dirs []string
	add := func(pluginBase string, skills []string) {
		if !fpIsPathWithin(basePath, pluginBase) {
			return
		}
		for _, sp := range skills {
			if !strings.HasPrefix(sp, "./") {
				continue
			}
			skillDir := filepath.Dir(filepath.Join(pluginBase, filepath.FromSlash(sp)))
			if fpIsPathWithin(basePath, skillDir) {
				dirs = append(dirs, skillDir)
			}
		}
		dirs = append(dirs, filepath.Join(pluginBase, "skills"))
	}
	if m, root, ok := readMarketplace(basePath); ok {
		for _, p := range m.Plugins {
			src, has, local := p.pluginSource()
			if !local || (has && !strings.HasPrefix(src, "./")) {
				continue
			}
			add(filepath.Join(basePath, filepath.FromSlash(root), filepath.FromSlash(src)), p.Skills)
		}
	}
	if m, ok := readPluginJSON(basePath); ok {
		add(basePath, m.Skills)
	}
	return dirs
}

// GetPluginGroupings maps absolute skill dirs to their declaring plugin name.
func GetPluginGroupings(basePath string) map[string]string {
	groups := map[string]string{}
	if m, root, ok := readMarketplace(basePath); ok {
		for _, p := range m.Plugins {
			if p.Name == "" {
				continue
			}
			src, has, local := p.pluginSource()
			if !local || (has && !strings.HasPrefix(src, "./")) {
				continue
			}
			pluginBase := filepath.Join(basePath, filepath.FromSlash(root), filepath.FromSlash(src))
			if !fpIsPathWithin(basePath, pluginBase) {
				continue
			}
			for _, sp := range p.Skills {
				if !strings.HasPrefix(sp, "./") {
					continue
				}
				dir := filepath.Join(pluginBase, filepath.FromSlash(sp))
				if fpIsPathWithin(basePath, dir) {
					if abs, err := filepath.Abs(dir); err == nil {
						groups[filepath.Clean(abs)] = p.Name
					}
				}
			}
		}
	}
	if m, ok := readPluginJSON(basePath); ok && m.Name != "" {
		for _, sp := range m.Skills {
			if !strings.HasPrefix(sp, "./") {
				continue
			}
			dir := filepath.Join(basePath, filepath.FromSlash(sp))
			if fpIsPathWithin(basePath, dir) {
				if abs, err := filepath.Abs(dir); err == nil {
					groups[filepath.Clean(abs)] = m.Name
				}
			}
		}
	}
	return groups
}

// ===========================================================================
// Skill discovery (upstream src/skills.ts)

// DiscoverSkillsOptions tunes DiscoverSkills.
type DiscoverSkillsOptions struct {
	// IncludeInternal includes metadata.internal skills.
	IncludeInternal bool
	// FullDepth searches all subdirectories even when a root SKILL.md exists.
	FullDepth bool
	// IncludeDuplicateNames keeps duplicate skill names (relocation checks).
	IncludeDuplicateNames bool
}

// skillWarningWriter receives "skipped skill" warnings (os.Stderr by default).
var skillWarningWriter io.Writer = os.Stderr

func fpWarnSkippedSkill(skillMdPath, reason string) {
	d := Diagnosticf(CodeSkillSkipped, "⚠ Skipped %s — %s", SanitizeMetadata(skillMdPath), StripTerminalEscapes(reason))
	fmt.Fprintln(skillWarningWriter, FormatError(d))
}

// parseSkillMdWithWarning parses a SKILL.md, warning (to stderr) and
// returning nil for invalid files; internal skills are silently skipped
// unless includeInternal or INSTALL_INTERNAL_SKILLS is set.
func parseSkillMdWithWarning(skillMdPath string, includeInternal bool) *Skill {
	s, err := ParseSkillMd(skillMdPath, includeInternal)
	if err != nil {
		fpWarnSkippedSkill(skillMdPath, err.Error())
		return nil
	}
	return s
}

func fpHasSkillMd(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && st.Mode().IsRegular()
}

func fpFindSkillDirsRecursive(dir string, depth, maxDepth int) []string {
	if depth > maxDepth {
		return nil
	}
	var out []string
	if fpHasSkillMd(dir) {
		out = append(out, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && !fpSkipDirs[e.Name()] {
			out = append(out, fpFindSkillDirsRecursive(filepath.Join(dir, e.Name()), depth+1, maxDepth)...)
		}
	}
	return out
}

// IsSubpathSafe reports whether basePath/subpath stays inside basePath.
func IsSubpathSafe(basePath, subpath string) bool {
	return fpIsPathWithin(basePath, filepath.Join(basePath, filepath.FromSlash(subpath)))
}

func fpSourceLockedSkillNames(basePath string) map[string]bool {
	names := map[string]bool{}
	if _, err := os.Stat(GetLocalLockPath(basePath)); err != nil {
		return names
	}
	lock, err := ReadLocalLock(basePath)
	if err != nil {
		return names
	}
	for k := range lock.Skills {
		names[fpNormalizeSkillName(k)] = true
	}
	return names
}

// DiscoverSkills finds valid skills under basePath/subpath, following
// upstream discovery: root SKILL.md short-circuit (unless FullDepth),
// priority dirs (skills/, skills/.curated|.experimental|.system, every agent
// project dir #2509 and skills.sh.json paths, walked up to depth 3), plugin
// manifest dirs (depth 1), then a depth-5 recursive fallback. Skills that
// the source repo merely installed (listed in its skills-lock.json and
// living in an agent dir) are skipped.
func DiscoverSkills(basePath, subpath string, opts DiscoverSkillsOptions) ([]Skill, error) {
	if subpath != "" && !IsSubpathSafe(basePath, subpath) {
		return nil, ErrSubpathOutsideRepo(subpath)
	}
	var skills []Skill
	seen := map[string]bool{}
	parsedPaths := map[string]bool{}
	locked := fpSourceLockedSkillNames(basePath)
	agentDirs := AgentProjectSkillDirs()

	searchPath := basePath
	if subpath != "" {
		searchPath = filepath.Join(basePath, filepath.FromSlash(subpath))
	}
	groupings := GetPluginGroupings(searchPath)
	enhance := func(s *Skill) {
		if abs, err := filepath.Abs(s.Path); err == nil {
			if name, ok := groupings[filepath.Clean(abs)]; ok {
				s.PluginName = name
			}
		}
	}
	isInstalledProjectSkill := func(s *Skill) bool {
		if len(locked) == 0 {
			return false
		}
		rel, err := filepath.Rel(basePath, s.Path)
		if err != nil {
			return false
		}
		rel = filepath.ToSlash(rel)
		inAgentDir := false
		for _, d := range agentDirs {
			if rel == d || strings.HasPrefix(rel, d+"/") {
				inAgentDir = true
				break
			}
		}
		if !inAgentDir {
			return false
		}
		return locked[fpNormalizeSkillName(s.Name)] || locked[fpNormalizeSkillName(filepath.Base(s.Path))]
	}
	parseAt := func(dir string) *Skill {
		md, _ := filepath.Abs(filepath.Join(dir, "SKILL.md"))
		if parsedPaths[md] {
			return nil
		}
		parsedPaths[md] = true
		return parseSkillMdWithWarning(filepath.Join(dir, "SKILL.md"), opts.IncludeInternal)
	}

	if fpHasSkillMd(searchPath) {
		if s := parseAt(searchPath); s != nil && !isInstalledProjectSkill(s) {
			enhance(s)
			skills = append(skills, *s)
			seen[s.Name] = true
			if !opts.FullDepth {
				return skills, nil
			}
		}
	}

	priority := []string{
		searchPath,
		filepath.Join(searchPath, "skills"),
		filepath.Join(searchPath, "skills", ".curated"),
		filepath.Join(searchPath, "skills", ".experimental"),
		filepath.Join(searchPath, "skills", ".system"),
	}
	for _, d := range agentDirs {
		priority = append(priority, filepath.Join(searchPath, filepath.FromSlash(d)))
	}
	priority = append(priority, GetSkillsShSearchDirs(searchPath)...)
	deep := map[string]bool{}
	for _, d := range priority[1:] {
		deep[d] = true
	}
	priority = append(priority, GetPluginSkillPaths(searchPath)...)

	tryAdd := func(dir string) bool {
		if !fpHasSkillMd(dir) {
			return false
		}
		s := parseAt(dir)
		if s == nil || (!opts.IncludeDuplicateNames && seen[s.Name]) {
			return true
		}
		if isInstalledProjectSkill(s) {
			return true
		}
		enhance(s)
		skills = append(skills, *s)
		seen[s.Name] = true
		return true
	}
	var walk func(dir string, maxDepth, depth int)
	walk = func(dir string, maxDepth, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			child := filepath.Join(dir, e.Name())
			found := tryAdd(child)
			if found || depth >= maxDepth || fpSkipDirs[e.Name()] {
				continue
			}
			walk(child, maxDepth, depth+1)
		}
	}
	for _, d := range priority {
		depth := 1
		if deep[d] {
			depth = DefaultSkillContainerDepth
		}
		walk(d, depth, 1)
	}

	if len(skills) == 0 || opts.FullDepth {
		for _, dir := range fpFindSkillDirsRecursive(searchPath, 0, 5) {
			s := parseAt(dir)
			if s != nil && (opts.IncludeDuplicateNames || !seen[s.Name]) && !isInstalledProjectSkill(s) {
				enhance(s)
				skills = append(skills, *s)
				seen[s.Name] = true
			}
		}
	}
	return skills, nil
}

// GetSkillDisplayName returns the skill name, or its directory basename.
func GetSkillDisplayName(s Skill) string {
	if s.Name != "" {
		return s.Name
	}
	return filepath.Base(s.Path)
}

// filterSkillIndexes returns indexes of skills matching inputNames (exact
// case-insensitive first, then slug-normalized fallback, #2508), in source order.
func filterSkillIndexes(names []string, inputNames []string) []int {
	selected := map[int]bool{}
	for _, input := range inputNames {
		var matches []int
		for i, n := range names {
			if strings.ToLower(n) == strings.ToLower(input) {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			ni := fpNormalizeSkillName(input)
			for i, n := range names {
				if fpNormalizeSkillName(n) == ni {
					matches = append(matches, i)
				}
			}
		}
		for _, i := range matches {
			selected[i] = true
		}
	}
	var out []int
	for i := range names {
		if selected[i] {
			out = append(out, i)
		}
	}
	return out
}

// FilterSkills selects skills by name: exact case-insensitive match first,
// then slug normalization ("Web Design" matches "web-design", "pr_review"
// matches "pr-review"). Preserves source order and deduplicates.
func FilterSkills(skills []Skill, inputNames []string) []Skill {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = GetSkillDisplayName(s)
	}
	var out []Skill
	for _, i := range filterSkillIndexes(names, inputNames) {
		out = append(out, skills[i])
	}
	return out
}

// FilterFetchedSkills is FilterSkills for FetchedSkill slices.
func FilterFetchedSkills(skills []FetchedSkill, inputNames []string) []FetchedSkill {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = GetSkillDisplayName(s.Skill)
	}
	var out []FetchedSkill
	for _, i := range filterSkillIndexes(names, inputNames) {
		out = append(out, skills[i])
	}
	return out
}

// MissingRequestedSkill reports whether any requested name (other than "*")
// matches none of skills.
func MissingRequestedSkill(skills []Skill, requested []string) bool {
	for _, name := range requested {
		if name != "*" && len(FilterSkills(skills, []string{name})) == 0 {
			return true
		}
	}
	return false
}

// ===========================================================================
// GitHub Trees API + skills.sh blob snapshots (upstream src/blob.ts)

// RepoTree is a GitHub git/trees?recursive=1 response.
type RepoTree struct {
	SHA    string          `json:"sha"`
	Branch string          `json:"-"`
	Tree   []RepoTreeEntry `json:"tree"`
}

// RepoTreeEntry is one tree entry.
type RepoTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree"
	SHA  string `json:"sha"`
	Size int64  `json:"size,omitempty"`
	Mode string `json:"mode,omitempty"`
}

// SkillSnapshotFile is one file of a skills.sh snapshot.
type SkillSnapshotFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// BlobSkill is a skill resolved from a snapshot (files in memory).
type BlobSkill struct {
	Skill
	Files        []SkillSnapshotFile
	SnapshotHash string
	// RepoPath is the SKILL.md path in the repo (e.g. "skills/foo/SKILL.md").
	RepoPath string
}

// BlobInstallResult is the outcome of TryBlobInstall.
type BlobInstallResult struct {
	Skills []BlobSkill
	Tree   *RepoTree
}

// BlobInstallOptions tunes TryBlobInstall.
type BlobInstallOptions struct {
	Subpath         string
	SkillFilter     string
	Ref             string
	GetToken        func() string
	IncludeInternal bool
}

// BlobAllowedOwners may use the skills.sh blob fast path for all their repos.
var BlobAllowedOwners = []string{"vercel", "vercel-labs", "heygen-com", "remotion-dev"}

// BlobAllowedRepos self-host their snapshot downloads.
var BlobAllowedRepos = map[string]func(slug string) string{
	"zapier/connectors": func(slug string) string {
		return "https://connectors-skills.zapier.com/download/" + fpEncodeURIComponent(slug) + "/snapshot.json"
	},
}

// Endpoint overrides for tests ("" = production defaults).
var (
	githubAPIBaseOverride string
	rawGitHubBase         = "https://raw.githubusercontent.com"
)

var rateLimitedThisSession atomic.Bool

// ResetRepoTreeAuthState clears the session rate-limit memo (tests only).
func ResetRepoTreeAuthState() { rateLimitedThisSession.Store(false) }

func githubAPIBase() string {
	if githubAPIBaseOverride != "" {
		return githubAPIBaseOverride
	}
	if h := GetGitHubHost(); h != "github.com" {
		return "https://" + h + "/api/v3"
	}
	return "https://api.github.com"
}

func providerGet(ctx context.Context, rawURL string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return providerHTTPClient.Do(req)
}

type treeFetchResult struct {
	tree                       *RepoTree
	rateLimited, authRetryable bool
}

func fpFetchTreeBranch(ownerRepo, branch, token string) treeFetchResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1", githubAPIBase(), ownerRepo, fpEncodeURIComponent(branch))
	headers := map[string]string{"Accept": "application/vnd.github.v3+json", "User-Agent": "skills-cli"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	resp, err := providerGet(ctx, u, headers)
	if err != nil {
		return treeFetchResult{}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		var t RepoTree
		if json.NewDecoder(resp.Body).Decode(&t) != nil {
			return treeFetchResult{}
		}
		t.Branch = branch
		return treeFetchResult{tree: &t}
	}
	return treeFetchResult{
		rateLimited:   resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0",
		authRetryable: resp.StatusCode == 401 || resp.StatusCode == 404,
	}
}

func fetchTreeWithGitHubCLI(ownerRepo string, branches []string) *RepoTree {
	if _, err := exec.LookPath(fpGhBinary); err != nil {
		return nil
	}
	for _, branch := range branches {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		endpoint := fmt.Sprintf("repos/%s/git/trees/%s?recursive=1", ownerRepo, fpEncodeURIComponent(branch))
		cmd := exec.CommandContext(ctx, fpGhBinary, "api", endpoint, "--method", "GET", "--hostname", GetGitHubHost())
		cmd.Env = fpOverrideEnv(os.Environ(), "GH_PROMPT_DISABLED=1")
		var out bytes.Buffer
		cmd.Stdout = &fpLimitedWriter{w: &out, n: 16 * 1024 * 1024}
		err := cmd.Run()
		cancel()
		if err != nil {
			continue
		}
		var raw struct {
			SHA  *string          `json:"sha"`
			Tree *[]RepoTreeEntry `json:"tree"`
		}
		if json.Unmarshal(out.Bytes(), &raw) != nil || raw.SHA == nil || raw.Tree == nil {
			continue
		}
		return &RepoTree{SHA: *raw.SHA, Branch: branch, Tree: *raw.Tree}
	}
	return nil
}

type fpLimitedWriter struct {
	w io.Writer
	n int64
}

func (l *fpLimitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, errors.New("output too large")
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

func fetchTreeWithAuth(ownerRepo string, branches []string, getToken func() string) *RepoTree {
	if getToken != nil {
		if token := getToken(); token != "" {
			for _, b := range branches {
				if r := fpFetchTreeBranch(ownerRepo, b, token); r.tree != nil {
					return r.tree
				}
			}
		}
	}
	return fetchTreeWithGitHubCLI(ownerRepo, branches)
}

// FetchRepoTree fetches the recursive tree for a GitHub repo, trying ref (or
// HEAD, main, master). Requests are unauthenticated first; only a rate-limit
// 403 or a private-repo 401/404 retries with getToken() and then `gh api`.
// Returns (nil, nil) when the tree cannot be fetched.
func FetchRepoTree(ownerRepo, ref string, getToken func() string) (*RepoTree, error) {
	branches := []string{"HEAD", "main", "master"}
	if ref != "" {
		branches = []string{ref}
	}
	if rateLimitedThisSession.Load() {
		return fetchTreeWithAuth(ownerRepo, branches, getToken), nil
	}
	rateLimited, authRetryable := false, false
	for _, b := range branches {
		r := fpFetchTreeBranch(ownerRepo, b, "")
		if r.tree != nil {
			return r.tree, nil
		}
		if r.rateLimited {
			rateLimited = true
			break
		}
		if r.authRetryable {
			authRetryable = true
			break
		}
	}
	if !rateLimited && !authRetryable {
		return nil, nil
	}
	if rateLimited {
		rateLimitedThisSession.Store(true)
	}
	return fetchTreeWithAuth(ownerRepo, branches, getToken), nil
}

// GetSkillFolderHashFromTree returns the tree SHA of the folder holding
// skillPath (a SKILL.md path), the root tree SHA for root skills, or "".
func GetSkillFolderHashFromTree(tree *RepoTree, skillPath string) string {
	if tree == nil {
		return ""
	}
	folder := strings.ReplaceAll(skillPath, `\`, "/")
	lower := strings.ToLower(folder)
	if strings.HasSuffix(lower, "/skill.md") {
		folder = folder[:len(folder)-9]
	} else if strings.HasSuffix(lower, "skill.md") {
		folder = folder[:len(folder)-8]
	}
	folder = strings.TrimSuffix(folder, "/")
	if folder == "" {
		return tree.SHA
	}
	for _, e := range tree.Tree {
		if e.Type == "tree" && e.Path == folder {
			return e.SHA
		}
	}
	return ""
}

func blobPriorityPrefixes() []string {
	p := []string{"", "skills/", "skills/.curated/", "skills/.experimental/", "skills/.system/"}
	for _, d := range AgentProjectSkillDirs() {
		p = append(p, d+"/")
	}
	return p
}

// FindSkillMdPaths finds SKILL.md blob paths in a tree using the same
// priority rules as DiscoverSkills (exact basename match, #2580).
func FindSkillMdPaths(tree *RepoTree, subpath string, extraContainers []string) []string {
	var all []string
	for _, e := range tree.Tree {
		if e.Type == "blob" && strings.ToLower(path.Base(e.Path)) == "skill.md" {
			all = append(all, e.Path)
		}
	}
	prefix := ""
	if subpath != "" {
		prefix = subpath
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
	}
	var filtered []string
	for _, p := range all {
		if prefix == "" || strings.HasPrefix(p, prefix) || p == prefix+"SKILL.md" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	lowerSet := map[string]bool{}
	for _, p := range filtered {
		lowerSet[strings.ToLower(p)] = true
	}
	base := blobPriorityPrefixes()
	baseSet := map[string]bool{}
	for _, p := range base {
		baseSet[p] = true
	}
	extraSet := map[string]bool{}
	var extras []string
	for _, d := range extraContainers {
		p := d + "/"
		if !baseSet[p] && !extraSet[p] {
			extraSet[p] = true
			extras = append(extras, p)
		}
	}
	var results []string
	seen := map[string]bool{}
	push := func(p string) {
		if !seen[p] {
			seen[p] = true
			results = append(results, p)
		}
	}
	for _, pp := range append(append([]string{}, base...), extras...) {
		full := prefix + pp
		isContainer := pp != ""
		isExtra := extraSet[pp]
		for _, md := range filtered {
			if !strings.HasPrefix(md, full) {
				continue
			}
			rest := md[len(full):]
			if strings.ToLower(rest) == "skill.md" {
				if !isExtra {
					push(md)
				}
				continue
			}
			parts := strings.Split(rest, "/")
			if len(parts) == 2 && strings.ToLower(parts[1]) == "skill.md" {
				push(md)
				continue
			}
			dirs := parts[:len(parts)-1]
			hasAncestor := false
			for i := 0; i < len(dirs)-1; i++ {
				anc := strings.Join(dirs[:i+1], "/")
				if lowerSet[strings.ToLower(full+anc+"/SKILL.md")] {
					hasAncestor = true
					break
				}
			}
			noSkip := true
			for _, d := range dirs {
				if fpSkipDirs[d] {
					noSkip = false
					break
				}
			}
			if isContainer && len(parts) >= 3 && len(parts) <= DefaultSkillContainerDepth+1 &&
				strings.ToLower(parts[len(parts)-1]) == "skill.md" && noSkip && !hasAncestor {
				push(md)
			}
		}
	}
	if len(results) > 0 {
		return results
	}
	var fallback []string
	for _, p := range filtered {
		if len(strings.Split(p, "/")) <= 6 {
			fallback = append(fallback, p)
		}
	}
	return fallback
}

func fpFetchRawContent(ownerRepo, branch, filePath string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := providerGet(ctx, fmt.Sprintf("%s/%s/%s/%s", rawGitHubBase, ownerRepo, branch, filePath), nil)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func fetchSkillsShPathsFromTree(ownerRepo string, tree *RepoTree, subpath string) []string {
	root := ""
	if subpath != "" {
		root = strings.TrimRight(subpath, "/") + "/"
	}
	cfg := root + SkillsShConfigFile
	found := false
	for _, e := range tree.Tree {
		if e.Type == "blob" && e.Path == cfg {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	content, ok := fpFetchRawContent(ownerRepo, tree.Branch, cfg)
	if !ok {
		return nil
	}
	return ParseSkillsShPaths(content)
}

type skillDownloadResponse struct {
	Files []SkillSnapshotFile `json:"files"`
	Hash  string              `json:"hash"`
}

func skillsDownloadBase() string {
	if v := os.Getenv("SKILLS_DOWNLOAD_URL"); v != "" {
		return v
	}
	return "https://skills.sh"
}

func fetchSkillDownload(source, slug string) *skillDownloadResponse {
	owner, repo, _ := strings.Cut(source, "/")
	u := fmt.Sprintf("%s/api/download/%s/%s/%s", skillsDownloadBase(), fpEncodeURIComponent(owner), fpEncodeURIComponent(repo), fpEncodeURIComponent(slug))
	if fn, ok := BlobAllowedRepos[strings.ToLower(source)]; ok {
		u = fn(slug)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := providerGet(ctx, u, nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil
	}
	var d skillDownloadResponse
	if json.NewDecoder(resp.Body).Decode(&d) != nil {
		return nil
	}
	return &d
}

func fpSkillFolderPath(skillMdPath string) string {
	lower := strings.ToLower(skillMdPath)
	if strings.HasSuffix(lower, "/skill.md") {
		return skillMdPath[:len(skillMdPath)-9]
	}
	if lower == "skill.md" {
		return ""
	}
	return skillMdPath[:len(skillMdPath)-(1+len("SKILL.md"))]
}

var (
	snapshotExcludedFiles = map[string]bool{"metadata.json": true}
	snapshotExcludedDirs  = map[string]bool{".git": true, "__pycache__": true, "__pypackages__": true}
)

func isInstallableSnapshotPath(p string) bool {
	parts := strings.Split(p, "/")
	name := parts[len(parts)-1]
	if name == "" || snapshotExcludedFiles[name] {
		return false
	}
	for _, d := range parts[:len(parts)-1] {
		if snapshotExcludedDirs[d] {
			return false
		}
	}
	return true
}

func hasCompleteNestedSnapshot(tree *RepoTree, skillMdPath string, files []SkillSnapshotFile) bool {
	folder := fpSkillFolderPath(skillMdPath)
	if folder == "" {
		return true
	}
	prefix := folder + "/"
	have := map[string]bool{}
	for _, f := range files {
		have[f.Path] = true
	}
	for _, e := range tree.Tree {
		if e.Type != "blob" || !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		rel := e.Path[len(prefix):]
		if isInstallableSnapshotPath(rel) && !have[rel] {
			return false
		}
	}
	return true
}

// fpComputeSnapshotHash hashes sorted path+contents (matches ComputeSkillFolderHash).
func fpComputeSnapshotHash(files []SkillSnapshotFile) string {
	sorted := append([]SkillSnapshotFile{}, files...)
	sort.SliceStable(sorted, func(i, j int) bool { return jsLocaleCompare(sorted[i].Path, sorted[j].Path) < 0 })
	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Contents))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IsBlobEligible reports whether ownerRepo may use the blob fast path.
func IsBlobEligible(ownerRepo string) bool {
	owner, _, ok := strings.Cut(ownerRepo, "/")
	if !ok || owner == "" {
		return false
	}
	if _, ok := BlobAllowedRepos[strings.ToLower(ownerRepo)]; ok {
		return true
	}
	for _, o := range BlobAllowedOwners {
		if strings.EqualFold(o, owner) {
			return true
		}
	}
	return false
}

// TryBlobInstall resolves skills from skills.sh snapshots instead of
// cloning. Returns (nil, nil) on any failure so the caller falls back to a
// clone: explicit ref, missing tree, incomplete nested snapshot (#2674), or
// any failed snapshot download.
func TryBlobInstall(ownerRepo string, opts BlobInstallOptions) (*BlobInstallResult, error) {
	if opts.Ref != "" {
		return nil, nil
	}
	tree, _ := FetchRepoTree(ownerRepo, "", opts.GetToken)
	if tree == nil {
		return nil, nil
	}
	extra := fetchSkillsShPathsFromTree(ownerRepo, tree, opts.Subpath)
	mdPaths := FindSkillMdPaths(tree, opts.Subpath, extra)
	if len(mdPaths) == 0 {
		return nil, nil
	}
	if opts.SkillFilter != "" {
		slug := ToSkillSlug(opts.SkillFilter)
		var byFolder []string
		for _, p := range mdPaths {
			parts := strings.Split(p, "/")
			if len(parts) >= 2 && ToSkillSlug(parts[len(parts)-2]) == slug {
				byFolder = append(byFolder, p)
			}
		}
		if len(byFolder) > 0 {
			mdPaths = byFolder
		}
	}

	contents := make([]string, len(mdPaths))
	oks := make([]bool, len(mdPaths))
	fpMapConcurrent(len(mdPaths), 8, func(i int) {
		contents[i], oks[i] = fpFetchRawContent(ownerRepo, tree.Branch, mdPaths[i])
	})

	type parsedBlob struct {
		mdPath, name, description, content, slug string
		metadata                                 map[string]interface{}
	}
	includeInternal := opts.IncludeInternal || ShouldInstallInternalSkills()
	var parsed []parsedBlob
	for i, md := range mdPaths {
		if !oks[i] || contents[i] == "" {
			continue
		}
		data, _, err := ParseFrontmatter(contents[i])
		if err != nil || data == nil {
			continue
		}
		name, ok1 := data["name"].(string)
		desc, ok2 := data["description"].(string)
		if !ok1 || !ok2 || name == "" || desc == "" {
			continue
		}
		meta, _ := data["metadata"].(map[string]interface{})
		if internal, _ := meta["internal"].(bool); internal && !includeInternal {
			continue
		}
		safeName := SanitizeMetadata(name)
		parsed = append(parsed, parsedBlob{md, safeName, SanitizeMetadata(desc), contents[i], ToSkillSlug(safeName), meta})
	}
	if len(parsed) == 0 {
		return nil, nil
	}
	if opts.SkillFilter != "" {
		slug := ToSkillSlug(opts.SkillFilter)
		var byName []parsedBlob
		for _, p := range parsed {
			if p.slug == slug {
				byName = append(byName, p)
			}
		}
		if len(byName) > 0 {
			parsed = byName
		}
	}

	source := strings.ToLower(ownerRepo)
	downloads := make([]*skillDownloadResponse, len(parsed))
	fpMapConcurrent(len(parsed), 8, func(i int) {
		downloads[i] = fetchSkillDownload(source, parsed[i].slug)
	})
	for i, d := range downloads {
		if d == nil || !hasCompleteNestedSnapshot(tree, parsed[i].mdPath, d.Files) {
			return nil, nil
		}
	}
	result := &BlobInstallResult{Tree: tree}
	for i, p := range parsed {
		files := downloads[i].Files
		if fpSkillFolderPath(p.mdPath) == "" {
			var only []SkillSnapshotFile
			for _, f := range files {
				if strings.ToLower(f.Path) == "skill.md" {
					only = append(only, f)
				}
			}
			files = only
		}
		result.Skills = append(result.Skills, BlobSkill{
			Skill:        Skill{Name: p.name, Description: p.description, RawContent: p.content, Metadata: p.metadata},
			Files:        files,
			SnapshotHash: fpComputeSnapshotHash(files),
			RepoPath:     p.mdPath,
		})
	}
	return result, nil
}

// ===========================================================================
// Well-known skills provider (upstream src/providers/wellknown.ts)

// DiscoverySchemaV2 is the v0.2.0 well-known discovery schema URL.
const DiscoverySchemaV2 = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"

const (
	wellKnownMaxArchiveBytes = 50 * 1024 * 1024
	wellKnownMaxArchiveFiles = 1000
	wellKnownArtifactMax     = 64 * 1024 * 1024
)

var wellKnownPaths = []string{".well-known/agent-skills", ".well-known/skills"}

// WellKnownSkill is a skill with all files fetched from a well-known endpoint.
type WellKnownSkill struct {
	Name        string
	Description string
	Content     string
	InstallName string
	SourceURL   string
	Metadata    map[string]interface{}
	// Files maps relative paths to contents.
	Files map[string][]byte
	// Version is "0.1.0" or "0.2.0".
	Version string
	// Digest is the v0.2.0 index digest ("" for v0.1.0).
	Digest string
	// IndexEntry is the raw index entry (keeps extra fields like "source").
	IndexEntry map[string]interface{}
}

type wellKnownEntry struct {
	version      string
	name         string
	description  string
	files        []string
	baseURL      string
	wellKnownDir string
	entryType    string
	artifactURL  string
	digest       string
	raw          map[string]interface{}
}

type wellKnownCandidate struct {
	entries         []wellKnownEntry
	resolvedBaseURL string
	indexURL        string
}

var (
	wkSkillNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)
	wkDigestRe    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func isValidWellKnownSkillName(v interface{}) (string, bool) {
	s, ok := v.(string)
	if !ok || len(s) < 1 || len(s) > 64 || !wkSkillNameRe.MatchString(s) ||
		strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") || strings.Contains(s, "--") {
		return "", false
	}
	return s, true
}

func isSafeLegacyFilePath(v interface{}) (string, bool) {
	s, ok := v.(string)
	if !ok || s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) ||
		strings.Contains(s, "..") || strings.ContainsRune(s, 0) {
		return "", false
	}
	return s, true
}

func normalizeWellKnownIndex(raw interface{}, indexURL, wkPath string) []wellKnownEntry {
	rec, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	skills, ok := rec["skills"].([]interface{})
	if !ok {
		return nil
	}
	schema, hasSchema := rec["$schema"]
	if hasSchema && schema == DiscoverySchemaV2 {
		base, err := url.Parse(indexURL)
		if err != nil {
			return nil
		}
		var out []wellKnownEntry
		for _, item := range skills {
			e, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			name, ok := isValidWellKnownSkillName(e["name"])
			if !ok {
				continue
			}
			desc, ok := e["description"].(string)
			if !ok || desc == "" || fpUTF16Len(desc) > 1024 {
				continue
			}
			typ, _ := e["type"].(string)
			if typ != "skill-md" && typ != "archive" {
				continue
			}
			u, ok := e["url"].(string)
			if !ok || u == "" {
				continue
			}
			digest, ok := e["digest"].(string)
			if !ok || !wkDigestRe.MatchString(digest) {
				continue
			}
			ref, err := url.Parse(u)
			if err != nil {
				continue
			}
			out = append(out, wellKnownEntry{version: "0.2.0", name: name, description: desc, entryType: typ,
				artifactURL: base.ResolveReference(ref).String(), digest: digest, raw: e})
		}
		return out
	}
	if hasSchema {
		return nil
	}
	pu, err := url.Parse(indexURL)
	if err != nil {
		return nil
	}
	marker := "/" + wkPath + "/index.json"
	legacyBase := pu.Scheme + "://" + pu.Host + strings.TrimSuffix(pu.Path, marker)
	var out []wellKnownEntry
	for _, item := range skills {
		e, ok := item.(map[string]interface{})
		if !ok {
			return nil
		}
		name, ok := isValidWellKnownSkillName(e["name"])
		if !ok {
			return nil
		}
		desc, ok := e["description"].(string)
		if !ok || desc == "" {
			return nil
		}
		rawFiles, ok := e["files"].([]interface{})
		if !ok || len(rawFiles) == 0 {
			return nil
		}
		var files []string
		hasMd := false
		for _, f := range rawFiles {
			s, ok := isSafeLegacyFilePath(f)
			if !ok {
				return nil
			}
			if strings.ToLower(s) == "skill.md" {
				hasMd = true
			}
			files = append(files, s)
		}
		if !hasMd {
			return nil
		}
		out = append(out, wellKnownEntry{version: "0.1.0", name: name, description: desc, files: files,
			baseURL: legacyBase, wellKnownDir: wkPath, raw: e})
	}
	return out
}

func fpUTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func fetchWellKnownCandidates(baseURL string) []wellKnownCandidate {
	pu, err := url.Parse(baseURL)
	if err != nil || pu.Host == "" {
		return nil
	}
	basePath := strings.TrimSuffix(pu.EscapedPath(), "/")
	origin := pu.Scheme + "://" + pu.Host
	type try struct{ indexURL, base, wk string }
	var tries []try
	for _, wk := range wellKnownPaths {
		tries = append(tries, try{origin + basePath + "/" + wk + "/index.json", origin + basePath, wk})
		if basePath != "" {
			tries = append(tries, try{origin + "/" + wk + "/index.json", origin, wk})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out []wellKnownCandidate
	for _, t := range tries {
		resp, err := providerGet(ctx, t.indexURL, nil)
		if err != nil {
			continue
		}
		var raw interface{}
		ok := resp.StatusCode >= 200 && resp.StatusCode <= 299 &&
			json.NewDecoder(io.LimitReader(resp.Body, 16*1024*1024)).Decode(&raw) == nil
		resp.Body.Close()
		if !ok {
			continue
		}
		entries := normalizeWellKnownIndex(raw, t.indexURL, t.wk)
		if len(entries) == 0 {
			continue
		}
		out = append(out, wellKnownCandidate{entries: entries, resolvedBaseURL: t.base, indexURL: t.indexURL})
	}
	return out
}

func fpHTTPGetBytes(rawURL string, max int64) ([]byte, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := providerGet(ctx, rawURL, nil)
	if err != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, "", false
	}
	return data, resp.Header.Get("Content-Type"), true
}

func newWellKnownSkill(name, description, content, installName, sourceURL string, metadata interface{},
	files map[string][]byte, e wellKnownEntry) *WellKnownSkill {
	meta, _ := metadata.(map[string]interface{})
	return &WellKnownSkill{
		Name: SanitizeMetadata(name), Description: SanitizeMetadata(description), Content: content,
		InstallName: installName, SourceURL: sourceURL, Metadata: meta, Files: files,
		Version: e.version, Digest: e.digest, IndexEntry: e.raw,
	}
}

func fetchLegacyWellKnownSkill(e wellKnownEntry) *WellKnownSkill {
	skillBase := strings.TrimSuffix(e.baseURL, "/") + "/" + e.wellKnownDir + "/" + e.name
	mdURL := skillBase + "/SKILL.md"
	data, _, ok := fpHTTPGetBytes(mdURL, 16*1024*1024)
	if !ok {
		return nil
	}
	content := string(data)
	fm, _, err := ParseFrontmatter(content)
	if err != nil || fm == nil {
		return nil
	}
	name, ok1 := fm["name"].(string)
	desc, ok2 := fm["description"].(string)
	if !ok1 || !ok2 {
		return nil
	}
	files := map[string][]byte{"SKILL.md": data}
	var others []string
	for _, f := range e.files {
		if strings.ToLower(f) != "skill.md" {
			others = append(others, f)
		}
	}
	results := make([][]byte, len(others))
	fpMapConcurrent(len(others), 8, func(i int) {
		if b, _, ok := fpHTTPGetBytes(skillBase+"/"+others[i], 16*1024*1024); ok {
			results[i] = b
		}
	})
	for i, f := range others {
		if results[i] != nil {
			files[f] = results[i]
		}
	}
	return newWellKnownSkill(name, desc, content, e.name, mdURL, fm["metadata"], files, e)
}

func fpSHA256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fetchArtifactWellKnownSkill(e wellKnownEntry) (*WellKnownSkill, error) {
	data, contentType, ok := fpHTTPGetBytes(e.artifactURL, wellKnownArtifactMax)
	if !ok {
		return nil, nil
	}
	if actual := fpSHA256Digest(data); actual != e.digest {
		return nil, ErrWellKnownDigest(e.name, e.digest, actual)
	}
	if e.entryType == "skill-md" {
		content := string(data)
		fm, _, err := ParseFrontmatter(content)
		if err != nil || fm == nil {
			return nil, nil
		}
		name, ok1 := fm["name"].(string)
		desc, ok2 := fm["description"].(string)
		if !ok1 || !ok2 {
			return nil, nil
		}
		return newWellKnownSkill(name, desc, content, e.name, e.artifactURL, fm["metadata"],
			map[string][]byte{"SKILL.md": data}, e), nil
	}
	files, err := extractWellKnownArchive(data, e.artifactURL, contentType)
	if err != nil {
		return nil, nil
	}
	md, ok := files["SKILL.md"]
	if !ok {
		return nil, nil
	}
	content := string(md)
	fm, _, err := ParseFrontmatter(content)
	if err != nil || fm == nil {
		return nil, nil
	}
	name, ok1 := fm["name"].(string)
	desc, ok2 := fm["description"].(string)
	if !ok1 || !ok2 {
		return nil, nil
	}
	return newWellKnownSkill(name, desc, content, e.name, e.artifactURL, fm["metadata"], files, e), nil
}

func extractWellKnownArchive(data []byte, artifactURL, contentType string) (map[string][]byte, error) {
	lower := strings.ToLower(artifactURL)
	if strings.Contains(contentType, "application/zip") || strings.HasSuffix(lower, ".zip") ||
		(len(data) >= 2 && data[0] == 0x50 && data[1] == 0x4b) {
		return ReadZipArchive(data, wellKnownMaxArchiveBytes, wellKnownMaxArchiveFiles)
	}
	if strings.Contains(contentType, "application/gzip") || strings.Contains(contentType, "application/x-gzip") ||
		strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") ||
		(len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b) {
		return extractWellKnownTarGz(data)
	}
	return nil, NewDiagnostic(CodeArchiveUnsupportedFormat, "Unsupported archive format", "")
}

func normalizeWellKnownArchivePath(raw string) (string, bool) {
	if raw == "" || strings.ContainsRune(raw, 0) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) ||
		(len(raw) >= 2 && fpIsASCIILetter(raw[0]) && raw[1] == ':') || strings.Contains(raw, `\`) {
		return "", false
	}
	var parts []string
	for _, p := range strings.Split(raw, "/") {
		if p == "" {
			continue
		}
		if p == "." || p == ".." {
			return "", false
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "/"), true
}

func extractWellKnownTarGz(data []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fpArchiveInvalid("Invalid gzip archive")
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, wellKnownMaxArchiveBytes*2))
	files := map[string][]byte{}
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fpArchiveInvalid("Invalid tar archive: " + err.Error())
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			return nil, NewDiagnostic(CodeArchiveLinkEntry, "Archive links are not supported", "")
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck
		default:
			continue
		}
		p, ok := normalizeWellKnownArchivePath(hdr.Name)
		if !ok {
			return nil, Diagnosticf(CodeWellknownArchiveUnsafePath, "Unsafe archive path: %s", hdr.Name)
		}
		total += hdr.Size
		if total > wellKnownMaxArchiveBytes {
			return nil, NewDiagnostic(CodeWellknownArchiveTooLarge, "Archive exceeds maximum unpacked size", "")
		}
		if len(files) >= wellKnownMaxArchiveFiles {
			return nil, NewDiagnostic(CodeWellknownArchiveTooManyFiles, "Archive contains too many files", "")
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, fpArchiveInvalid("Invalid tar archive: " + err.Error())
		}
		files[p] = content
	}
	if _, ok := files["SKILL.md"]; !ok {
		return nil, NewDiagnostic(CodeArchiveMissingSkillMD, "Archive missing root SKILL.md", "")
	}
	return files, nil
}

func fetchWellKnownEntry(e wellKnownEntry) (*WellKnownSkill, error) {
	if e.version == "0.1.0" {
		return fetchLegacyWellKnownSkill(e), nil
	}
	return fetchArtifactWellKnownSkill(e)
}

// FetchWellKnownSkills fetches every skill published at rawURL's well-known
// index (/.well-known/agent-skills, then /.well-known/skills; path-relative
// first). Scoped URLs never widen to the root index
// (SKILLS_WELLKNOWN_SCOPE_NOT_FOUND) and v0.2.0 digest mismatches fail
// (SKILLS_WELLKNOWN_DIGEST_MISMATCH) without any fallback. Other failures
// yield an empty result.
func FetchWellKnownSkills(rawURL string, includeInternal bool) ([]*WellKnownSkill, error) {
	candidates := fetchWellKnownCandidates(rawURL)
	var scopePath, rootBase string
	if pu, err := url.Parse(rawURL); err == nil {
		scopePath = strings.TrimSuffix(pu.EscapedPath(), "/")
		rootBase = pu.Scheme + "://" + pu.Host
	}
	scoped := candidates
	if scopePath != "" {
		scoped = nil
		for _, c := range candidates {
			if c.resolvedBaseURL != rootBase {
				scoped = append(scoped, c)
			}
		}
	}
	includeInternal = includeInternal || ShouldInstallInternalSkills()
	for _, c := range scoped {
		results := make([]*WellKnownSkill, len(c.entries))
		errs := make([]error, len(c.entries))
		fpMapConcurrent(len(c.entries), 8, func(i int) {
			results[i], errs[i] = fetchWellKnownEntry(c.entries[i])
		})
		for _, err := range errs {
			if IsDiagnostic(err, CodeWellknownDigestMismatch) {
				return nil, err
			}
		}
		var skills []*WellKnownSkill
		for _, s := range results {
			if s == nil {
				continue
			}
			if internal, _ := s.Metadata["internal"].(bool); internal && !includeInternal {
				continue
			}
			skills = append(skills, s)
		}
		if len(skills) > 0 {
			return skills, nil
		}
	}
	if scopePath != "" && len(scoped) < len(candidates) {
		return nil, ErrWellKnownScope(scopePath, rootBase)
	}
	return nil, nil
}

// ComputeWellKnownSkillDigest returns the v0.2.0 index digest, or a sha256
// over sorted path\0content\0 pairs for v0.1.0 skills.
func ComputeWellKnownSkillDigest(s *WellKnownSkill) string {
	if s.Digest != "" {
		return s.Digest
	}
	keys := make([]string, 0, len(s.Files))
	for k := range s.Files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return jsUTF16Less(keys[i], keys[j]) })
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(s.Files[k])
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// jsUTF16Less is JavaScript's default Array.prototype.sort string order.
func jsUTF16Less(a, b string) bool {
	ua, ub := fpUTF16Units(a), fpUTF16Units(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func fpUTF16Units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// GetWellKnownSourceIdentifier is the hostname without "www." (lock source).
func GetWellKnownSourceIdentifier(rawURL string) string {
	pu, err := url.Parse(rawURL)
	if err != nil || pu.Hostname() == "" {
		return "unknown"
	}
	return strings.TrimPrefix(pu.Hostname(), "www.")
}

// IsSkillsShPackURL reports skills.sh/p/<pack> URLs.
func IsSkillsShPackURL(rawURL string) bool {
	pu, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(pu.Hostname(), "www.")
	return host == "skills.sh" && skillsShPackPathRe.MatchString(pu.Path)
}

var (
	skillsShPackPathRe = regexp.MustCompile(`^/p/[^/]+`)
	packRepoRe         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)
	packSkillRe        = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9._-]*$`)
	packTreeHashRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// GetPackSkillPointer extracts a validated skills.sh pack pointer from a
// well-known index entry's `source: {repo, skill, treeHash}`.
func GetPackSkillPointer(s *WellKnownSkill) *PackSkillPointer {
	if s == nil {
		return nil
	}
	src, ok := s.IndexEntry["source"].(map[string]interface{})
	if !ok {
		return nil
	}
	repo, ok1 := src["repo"].(string)
	skill, ok2 := src["skill"].(string)
	treeHash, ok3 := src["treeHash"].(string)
	if !ok1 || !ok2 || !ok3 || !packTreeHashRe.MatchString(treeHash) {
		return nil
	}
	if !packRepoRe.MatchString(repo) || strings.Contains(repo, "..") {
		return nil
	}
	if !packSkillRe.MatchString(skill) || strings.Contains(skill, "..") {
		return nil
	}
	return &PackSkillPointer{Repo: repo, Skill: skill, TreeHash: treeHash}
}

// ===========================================================================
// High-level fetch API

// SourceRequest describes one source to fetch.
type SourceRequest struct {
	// Source is the raw user input (e.g. "owner/repo@skill", URL, local path).
	Source string
	// Parsed optionally supplies an already parsed source (skips ParseSource).
	Parsed *ParsedSource
	// Skills are the requested skill names ("*" = all). Used only to decide
	// blob fallback and the #2596 full-depth retry; FetchSource never filters.
	Skills []string
	// FullDepth forces a full-depth discovery walk (and skips the blob path).
	FullDepth bool
	// NoFullDepthRetry disables the automatic #2596 retry (skills use).
	NoFullDepthRetry bool
	// IncludeInternal includes metadata.internal skills (also enabled when
	// Skills names anything other than "*").
	IncludeInternal bool
	// IncludeDuplicateNames keeps duplicate skill names.
	IncludeDuplicateNames bool
	// NoBlob disables the skills.sh blob snapshot fast path.
	NoBlob bool
	// Yes allows non-interactive fallbacks (zip download when git is missing).
	Yes bool
	// Cwd resolves relative local paths.
	Cwd string
	// GitHubHost pins the shorthand host (see ParseSourceOptions).
	GitHubHost string
}

// FetchedSkill is one discovered skill materialized on local disk.
type FetchedSkill struct {
	Skill
	// SkillPath is the source-relative SKILL.md path with forward slashes
	// ("SKILL.md" or "skills/foo/SKILL.md"), as recorded in lock files.
	// Empty for local sources and well-known skills.
	SkillPath string
	// FolderHash is the GitHub tree SHA of the skill folder when known (blob
	// fast path); empty otherwise.
	FolderHash string
	// ComputedHash is ComputeSkillFolderHash of the materialized folder for
	// blob skills (upstream snapshotHash); empty otherwise.
	ComputedHash string
	// WellKnownDigest is set for well-known skills ("sha256:<hex>").
	WellKnownDigest string
	// InstallName is the well-known installName (empty for others).
	InstallName string
	// SourceURL is the per-skill URL for well-known skills (SKILL.md or artifact).
	SourceURL string
	// PackPointer is set for skills.sh pack entries carrying a source pointer.
	PackPointer *PackSkillPointer
}

// PackSkillPointer is a skills.sh pack entry's upstream pointer (#2651).
type PackSkillPointer struct {
	Repo     string `json:"repo"`
	Skill    string `json:"skill"`
	TreeHash string `json:"treeHash"`
}

// FetchedSource is a source materialized into a local directory.
type FetchedSource struct {
	Input  string
	Parsed ParsedSource
	// SourceType is the lock sourceType: github|gitlab|git|local|well-known|download.
	SourceType string
	// Source is the lock "source" identifier: owner/repo for github.com,
	// the URL for SSH/other hosts, host for well-known, URL for downloads,
	// the path for local sources.
	Source string
	// SourceURL is the fetch URL (clone URL / index URL / download URL / path).
	SourceURL string
	// Ref is the git ref that was requested (empty for default branch).
	Ref string
	// RootDir is the materialized source root (repo checkout, extracted
	// archive root, staged well-known dir, or the local path itself).
	RootDir string
	// TempDir is the directory to delete on Cleanup ("" for local sources).
	TempDir string
	// Via is how it was fetched: "local"|"clone"|"blob"|"download"|"well-known".
	Via string
	// Skills are all discovered skills (unfiltered), Path pointing into RootDir.
	Skills []FetchedSkill
	// Tree is the GitHub tree when fetched via the blob path (else nil).
	Tree *RepoTree
}

// Cleanup removes the temporary directory (no-op for local sources).
func (f *FetchedSource) Cleanup() error {
	if f == nil || f.TempDir == "" {
		return nil
	}
	err := CleanupTempDir(f.TempDir)
	if err == nil {
		f.TempDir = ""
	}
	return err
}

// SkillList returns the plain Skill values.
func (f *FetchedSource) SkillList() []Skill {
	out := make([]Skill, len(f.Skills))
	for i, s := range f.Skills {
		out[i] = s.Skill
	}
	return out
}

// LockSource mirrors upstream getLockSource: SSH and non-github.com HTTP(S)
// URLs are kept verbatim; github.com URLs use ownerRepo.
func LockSource(parsedURL, ownerRepo string) string {
	if strings.HasPrefix(parsedURL, "git@") || strings.HasPrefix(parsedURL, "ssh://") {
		return parsedURL
	}
	if spIsHTTPURL(parsedURL) {
		if pu, err := url.Parse(parsedURL); err == nil {
			if pu.Hostname() != "github.com" {
				return parsedURL
			}
		} else {
			return ownerRepo
		}
	}
	return ownerRepo
}

func fpWriteSkillFiles(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for rel, contents := range files {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if !fpIsPathWithin(dir, target) || target == filepath.Clean(dir) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, contents, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fpRelSkillPath(root, skillDir string) string {
	rel, err := filepath.Rel(root, skillDir)
	if err != nil || rel == "." {
		return "SKILL.md"
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return ""
	}
	return rel + "/SKILL.md"
}

// FetchSource parses (if needed), fetches and discovers one source:
// local paths, well-known indexes (falling back to a direct download when
// the URL publishes none), direct downloads, GitHub blob snapshots for
// allowed owners, and git clones. Blob and well-known skills are written to
// disk under TempDir so every FetchedSkill.Path is a real directory. The
// caller must call Cleanup().
func FetchSource(req SourceRequest) (*FetchedSource, error) {
	var parsed ParsedSource
	if req.Parsed != nil {
		parsed = *req.Parsed
	} else {
		p, err := ParseSource(req.Source, ParseSourceOptions{GitHubHost: req.GitHubHost, Cwd: req.Cwd})
		if err != nil {
			return nil, err
		}
		parsed = p
	}
	input := req.Source
	if input == "" {
		input = parsed.URL
	}

	if IsNotionSource(input) {
		return fetchNotionPacks(input, parsed, req)
	}
	if pageID := ParseNotionSkillURL(input); pageID != "" {
		return fetchNotionPage(input, parsed, pageID, req)
	}

	requested := append([]string{}, req.Skills...)
	if parsed.SkillFilter != "" {
		requested = append(requested, parsed.SkillFilter)
	}
	includeInternal := req.IncludeInternal
	if !includeInternal && len(requested) > 0 {
		includeInternal = true
		for _, s := range requested {
			if s == "*" {
				includeInternal = false
			}
		}
	}
	dopts := DiscoverSkillsOptions{IncludeInternal: includeInternal, FullDepth: req.FullDepth, IncludeDuplicateNames: req.IncludeDuplicateNames}
	discover := func(root string) ([]Skill, error) {
		found, err := DiscoverSkills(root, parsed.Subpath, dopts)
		if err != nil {
			return nil, err
		}
		if !req.FullDepth && !req.NoFullDepthRetry && MissingRequestedSkill(found, requested) {
			o := dopts
			o.FullDepth = true
			return DiscoverSkills(root, parsed.Subpath, o)
		}
		return found, nil
	}

	out := &FetchedSource{Input: input, Parsed: parsed, SourceType: parsed.Type, Ref: parsed.Ref, SourceURL: parsed.URL}
	fail := func(err error) (*FetchedSource, error) {
		_ = out.Cleanup()
		return nil, err
	}

	switch parsed.Type {
	case SourceTypeLocal:
		st, err := os.Stat(parsed.LocalPath)
		if err != nil || !st.IsDir() {
			if err == nil {
				// A SKILL.md file path: treat its directory as the source.
				if strings.EqualFold(filepath.Base(parsed.LocalPath), "SKILL.md") {
					parsed.LocalPath = filepath.Dir(parsed.LocalPath)
					out.Parsed = parsed
				} else {
					return nil, ErrLocalPathNotFound(parsed.LocalPath)
				}
			} else {
				return nil, ErrLocalPathNotFound(parsed.LocalPath)
			}
		}
		out.Via = "local"
		out.RootDir = parsed.LocalPath
		out.Source = parsed.LocalPath
		out.SourceURL = parsed.LocalPath
		skills, err := discover(parsed.LocalPath)
		if err != nil {
			return nil, err
		}
		for _, s := range skills {
			out.Skills = append(out.Skills, FetchedSkill{Skill: s})
		}
		return out, nil

	case SourceTypeWellKnown, SourceTypeDownload:
		if parsed.Type == SourceTypeWellKnown {
			wk, err := FetchWellKnownSkills(parsed.URL, includeInternal)
			if err != nil {
				return nil, err
			}
			if len(wk) > 0 {
				return materializeWellKnown(out, parsed, wk)
			}
		}
		var dl *DownloadedSource
		var err error
		if parsed.Type == SourceTypeDownload {
			dl, err = DownloadNotionURL(parsed.URL)
			if err != nil {
				return nil, err
			}
		}
		if dl == nil {
			dl, err = DownloadSource(parsed.URL)
			if err != nil {
				return nil, err
			}
		}
		if IsNotionPackURL(parsed.URL) {
			out.Source = "notion"
		} else {
			out.Source = parsed.URL
		}
		out.TempDir = dl.TempDir
		out.RootDir = dl.RootDir
		out.Via = "download"
		out.SourceType = SourceTypeDownload
		skills, err := discover(dl.RootDir)
		if err != nil {
			return fail(err)
		}
		for _, s := range skills {
			out.Skills = append(out.Skills, FetchedSkill{Skill: s, SkillPath: fpRelSkillPath(dl.RootDir, s.Path)})
		}
		return out, nil
	}

	ownerRepo := GetOwnerRepo(parsed)
	out.Source = LockSource(parsed.URL, ownerRepo)
	if out.Source == "" {
		out.Source = parsed.URL
	}

	if parsed.Type == SourceTypeGitHub && !req.FullDepth && !req.NoBlob && IsBlobEligible(ownerRepo) {
		blob, _ := TryBlobInstall(ownerRepo, BlobInstallOptions{Subpath: parsed.Subpath, SkillFilter: parsed.SkillFilter,
			Ref: parsed.Ref, GetToken: GetGitHubToken, IncludeInternal: includeInternal})
		if blob != nil {
			plain := make([]Skill, len(blob.Skills))
			for i, b := range blob.Skills {
				plain[i] = b.Skill
			}
			if !MissingRequestedSkill(plain, requested) {
				return materializeBlob(out, blob)
			}
		}
	}

	cloneDir, err := cloneOrDownloadRepo(parsed, req.Yes)
	if err != nil {
		return nil, err
	}
	out.TempDir = cloneDir
	out.RootDir = cloneDir
	out.Via = "clone"
	skills, err := discover(cloneDir)
	if err != nil {
		return fail(err)
	}
	for _, s := range skills {
		out.Skills = append(out.Skills, FetchedSkill{Skill: s, SkillPath: fpRelSkillPath(cloneDir, s.Path)})
	}
	return out, nil
}

// cloneOrDownloadRepo clones the repo, or (skl feature) downloads a GitHub
// archive zip when git is not installed and the caller allows it.
func cloneOrDownloadRepo(parsed ParsedSource, yes bool) (string, error) {
	if IsGitInstalled() {
		return CloneRepo(parsed.URL, parsed.Ref)
	}
	repo := fpParseGitHubRepoURL(parsed.URL)
	if repo == nil || !fpIsGitHubHTTPSCloneURL(parsed.URL) {
		return "", Diagnosticf(CodeGitCloneFailed, "Failed to clone %s: git is not installed or not on PATH", parsed.URL)
	}
	if !yes {
		return "", Diagnosticf(CodeGitCloneFailed,
			"Failed to clone %s: git is not installed. Re-run with --yes to download a public ZIP archive of the repository instead (default branch or the given ref only; private repositories are not supported).", parsed.URL)
	}
	fmt.Fprintln(os.Stderr, "Git is not installed; downloading a public ZIP archive of the repository instead.")
	refs := []string{"HEAD"}
	if parsed.Ref != "" {
		refs = []string{parsed.Ref}
	}
	var lastErr error
	for _, ref := range refs {
		zipURL := fmt.Sprintf("https://%s/%s/%s/archive/%s.zip", repo.Host, repo.Owner, repo.Repo, url.PathEscape(ref))
		dl, err := DownloadSource(zipURL, DownloadSourceOptions{DownloadMaxBytes: 200 * 1024 * 1024, ExtractMaxBytes: 500 * 1024 * 1024, ExtractMaxFiles: 50000})
		if err != nil {
			lastErr = err
			continue
		}
		if dl.RootDir == dl.TempDir {
			return dl.TempDir, nil
		}
		// Move the archive root up so TempDir == repo root.
		root, err := os.MkdirTemp("", "skills-")
		if err != nil {
			_ = os.RemoveAll(dl.TempDir)
			return "", err
		}
		_ = os.Remove(root)
		if err := os.Rename(dl.RootDir, root); err != nil {
			_ = os.RemoveAll(dl.TempDir)
			return "", err
		}
		_ = os.RemoveAll(dl.TempDir)
		return root, nil
	}
	return "", Diagnosticf(CodeGitCloneFailed, "Failed to clone %s: %v", parsed.URL, lastErr).WithCause(lastErr)
}

func materializeBlob(out *FetchedSource, blob *BlobInstallResult) (*FetchedSource, error) {
	tmp, err := os.MkdirTemp("", "skills-blob-")
	if err != nil {
		return nil, err
	}
	out.TempDir = tmp
	out.RootDir = tmp
	out.Via = "blob"
	out.Tree = blob.Tree
	for _, b := range blob.Skills {
		// Root skills live in their own subtree so their folder never
		// contains the nested skills of the same repo.
		folder := fpSkillFolderPath(b.RepoPath)
		dir := filepath.Join(tmp, "root")
		if folder != "" {
			dir = filepath.Join(tmp, "tree", filepath.FromSlash(folder))
		}
		if !fpIsPathWithin(tmp, dir) {
			continue
		}
		files := map[string][]byte{}
		for _, f := range b.Files {
			files[f.Path] = []byte(f.Contents)
		}
		if err := fpWriteSkillFiles(dir, files); err != nil {
			_ = out.Cleanup()
			return nil, err
		}
		s := b.Skill
		s.Path = dir
		hash, err := ComputeSkillFolderHash(dir)
		if err != nil {
			hash = b.SnapshotHash
		}
		out.Skills = append(out.Skills, FetchedSkill{
			Skill: s, SkillPath: b.RepoPath, FolderHash: GetSkillFolderHashFromTree(blob.Tree, b.RepoPath), ComputedHash: hash,
		})
	}
	return out, nil
}

func materializeWellKnown(out *FetchedSource, parsed ParsedSource, skills []*WellKnownSkill) (*FetchedSource, error) {
	tmp, err := os.MkdirTemp("", "skills-wellknown-")
	if err != nil {
		return nil, err
	}
	out.TempDir = tmp
	out.RootDir = tmp
	out.Via = "well-known"
	out.SourceType = SourceTypeWellKnown
	out.Source = GetWellKnownSourceIdentifier(parsed.URL)
	out.SourceURL = parsed.URL
	isPack := IsSkillsShPackURL(parsed.URL)
	for _, wk := range skills {
		dir := filepath.Join(tmp, SanitizeName(wk.InstallName))
		if err := fpWriteSkillFiles(dir, wk.Files); err != nil {
			_ = out.Cleanup()
			return nil, err
		}
		fs := FetchedSkill{
			Skill:           Skill{Name: wk.InstallName, Description: wk.Description, Path: dir, RawContent: wk.Content, Metadata: wk.Metadata},
			WellKnownDigest: ComputeWellKnownSkillDigest(wk),
			InstallName:     wk.InstallName,
			SourceURL:       wk.SourceURL,
		}
		if isPack {
			fs.PackPointer = GetPackSkillPointer(wk)
		}
		out.Skills = append(out.Skills, fs)
	}
	return out, nil
}

// FetchSources fetches several sources concurrently (SKILLS_CLONE_CONCURRENCY,
// default 4). Results and errors keep input order.
func FetchSources(reqs []SourceRequest) ([]*FetchedSource, []error) {
	out := make([]*FetchedSource, len(reqs))
	errs := make([]error, len(reqs))
	fpMapConcurrent(len(reqs), FetchConcurrency(), func(i int) {
		out[i], errs[i] = FetchSource(reqs[i])
	})
	return out, errs
}

// fetchNotionPage stages a Notion skill page (via ntn) as a download source.
func fetchNotionPage(input string, parsed ParsedSource, pageID string, req SourceRequest) (*FetchedSource, error) {
	dir, err := FetchNotionSkillDirectory(pageID)
	if err != nil {
		return nil, err
	}
	dl, err := downloadNotionDirectory(dir.URL)
	if err != nil {
		return nil, err
	}
	lockURL := NotionPageURL(pageID)
	out := &FetchedSource{Input: input, Parsed: parsed, SourceType: SourceTypeDownload, Source: lockURL, SourceURL: lockURL,
		RootDir: dl.RootDir, TempDir: dl.TempDir, Via: "download"}
	skills, err := DiscoverSkills(dl.RootDir, "", DiscoverSkillsOptions{IncludeInternal: true, FullDepth: req.FullDepth})
	if err != nil {
		_ = out.Cleanup()
		return nil, err
	}
	for _, s := range skills {
		out.Skills = append(out.Skills, FetchedSkill{Skill: s, SkillPath: fpRelSkillPath(dl.RootDir, s.Path)})
	}
	return out, nil
}

// fetchNotionPacks stages the selected Notion packs (req.Skills are pack
// selectors; none or "*" selects all) under one temp dir with a generated
// .claude-plugin/marketplace.json, so skills carry their pack as
// PluginName. Each skill's SourceURL is its pack lock URL
// (notion://packs/<id>) and SkillPath is relative to the pack root.
func fetchNotionPacks(input string, parsed ParsedSource, req SourceRequest) (*FetchedSource, error) {
	packs, err := FetchNotionPacks()
	if err != nil {
		return nil, err
	}
	if len(packs) == 0 {
		return nil, NewDiagnostic(CodeNotionNoPacks, "Notion returned no packs for the authenticated workspace", "")
	}
	selected := packs
	if len(req.Skills) > 0 {
		selected = FilterNotionPacks(packs, req.Skills)
		if len(selected) == 0 {
			return nil, Diagnosticf(CodeNotionNoMatchingPacks, "No matching Notion packs found for: %s", strings.Join(req.Skills, ", "))
		}
	}
	staging, err := os.MkdirTemp("", "skills-notion-")
	if err != nil {
		return nil, err
	}
	out := &FetchedSource{Input: input, Parsed: parsed, SourceType: SourceTypeDownload, Source: "notion",
		SourceURL: "notion", RootDir: staging, TempDir: staging, Via: "download"}
	fail := func(e error) (*FetchedSource, error) {
		_ = out.Cleanup()
		return nil, e
	}
	type manifestPlugin struct {
		Name   string   `json:"name"`
		Source string   `json:"source"`
		Skills []string `json:"skills"`
	}
	var plugins []manifestPlugin
	type packRoot struct{ dir, lockURL string }
	var roots []packRoot
	for i, pack := range selected {
		quoted, _ := json.Marshal(pack.Name)
		prepare := func() error {
			dir, err := FetchNotionPackDirectory(pack)
			if err != nil {
				return err
			}
			dl, err := downloadNotionDirectory(dir.URL)
			if err != nil {
				return err
			}
			defer func() { _ = CleanupTempDir(dl.TempDir) }()
			skills, err := DiscoverSkills(dl.RootDir, "", DiscoverSkillsOptions{IncludeInternal: true, FullDepth: true})
			if err != nil {
				return err
			}
			if len(skills) == 0 {
				return NewDiagnostic(CodeNotionPackNoSkills, "pack contains no valid skills", "")
			}
			name := SanitizeName(pack.Name)
			if len(name) > 100 {
				name = name[:100]
			}
			dirName := fmt.Sprintf("pack-%d-%s", i+1, name)
			target := filepath.Join(staging, "packs", dirName)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Rename(dl.RootDir, target); err != nil {
				return err
			}
			var rels []string
			for _, s := range skills {
				rel, _ := filepath.Rel(dl.RootDir, s.Path)
				rel = filepath.ToSlash(rel)
				if rel == "." || rel == "" {
					rels = append(rels, "./.")
				} else {
					rels = append(rels, "./"+rel)
				}
			}
			plugins = append(plugins, manifestPlugin{Name: pack.Name, Source: "./" + dirName, Skills: rels})
			roots = append(roots, packRoot{dir: target, lockURL: NotionPackURL(pack.ID)})
			return nil
		}
		if err := prepare(); err != nil {
			return fail(Diagnosticf(CodeNotionPackPrepareFailed, "Failed to prepare Notion pack %s: %s", string(quoted), err.Error()).WithCause(err))
		}
	}
	manifest, _ := json.MarshalIndent(map[string]interface{}{
		"metadata": map[string]string{"pluginRoot": "./packs"},
		"plugins":  plugins,
	}, "", "  ")
	if err := os.MkdirAll(filepath.Join(staging, ".claude-plugin"), 0o755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(staging, ".claude-plugin", "marketplace.json"), append(manifest, '\n'), 0o644); err != nil {
		return fail(err)
	}
	skills, err := DiscoverSkills(staging, "", DiscoverSkillsOptions{IncludeInternal: true, FullDepth: true})
	if err != nil {
		return fail(err)
	}
	for _, s := range skills {
		fs := FetchedSkill{Skill: s}
		for _, r := range roots {
			if fpIsPathWithin(r.dir, s.Path) {
				fs.SourceURL = r.lockURL
				fs.SkillPath = fpRelSkillPath(r.dir, s.Path)
				break
			}
		}
		out.Skills = append(out.Skills, fs)
	}
	return out, nil
}

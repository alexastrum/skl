//go:build windows

package skl

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// reparseMountPointHeader is the four uint16 name offsets and lengths that
// precede PathBuffer in a mount point REPARSE_DATA_BUFFER.
const reparseMountPointHeader = 8

// createJunction creates an NTFS directory junction at link pointing to the absolute
// directory target using FSCTL_SET_REPARSE_POINT (no cmd.exe, so path characters such
// as & | < > ^ % are never interpreted).
func createJunction(link, target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return fmt.Errorf("junction target is not a directory: %s", target)
	}
	if err := os.Mkdir(link, 0o755); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(link)
		}
	}()

	buf := mountPointReparseData(target)
	name, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var returned uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		return err
	}
	ok = true
	return nil
}

// mountPointReparseData builds the REPARSE_DATA_BUFFER for a junction to the
// absolute path target: tag, data length, the name offsets and lengths, then
// the NUL-terminated substitute (\??\target) and print names.
func mountPointReparseData(target string) []byte {
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	subBytes := len(substitute) * 2
	printBytes := len(printName) * 2
	pathBuf := make([]uint16, 0, len(substitute)+len(printName)+2)
	pathBuf = append(pathBuf, substitute...)
	pathBuf = append(pathBuf, 0)
	pathBuf = append(pathBuf, printName...)
	pathBuf = append(pathBuf, 0)

	dataLen := reparseMountPointHeader + len(pathBuf)*2
	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLen))
	binary.LittleEndian.PutUint16(buf[8:], 0)                   // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:], uint16(subBytes))   // SubstituteNameLength
	binary.LittleEndian.PutUint16(buf[12:], uint16(subBytes+2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(buf[14:], uint16(printBytes)) // PrintNameLength
	for i, c := range pathBuf {
		binary.LittleEndian.PutUint16(buf[16+i*2:], c)
	}
	return buf
}

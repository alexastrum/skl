//go:build windows

package skl

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	fsctlSetReparsePoint    = 0x000900A4
	ioReparseTagMountPoint  = 0xA0000003
	reparseMountPointHeader = 8 // four uint16 name offsets/lengths
)

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

	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	subBytes := len(substitute) * 2
	printBytes := len(printName) * 2
	// Both names are NUL terminated inside PathBuffer.
	pathBuf := make([]uint16, 0, len(substitute)+len(printName)+2)
	pathBuf = append(pathBuf, substitute...)
	pathBuf = append(pathBuf, 0)
	pathBuf = append(pathBuf, printName...)
	pathBuf = append(pathBuf, 0)

	dataLen := reparseMountPointHeader + len(pathBuf)*2
	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLen))
	binary.LittleEndian.PutUint16(buf[8:], 0)                   // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:], uint16(subBytes))   // SubstituteNameLength
	binary.LittleEndian.PutUint16(buf[12:], uint16(subBytes+2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(buf[14:], uint16(printBytes)) // PrintNameLength
	for i, c := range pathBuf {
		binary.LittleEndian.PutUint16(buf[16+i*2:], c)
	}

	name, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	var returned uint32
	if err := syscall.DeviceIoControl(h, fsctlSetReparsePoint, (*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		return err
	}
	ok = true
	return nil
}

//go:build windows

// Package winlink creates, inspects and removes Windows directory junctions.
// Junction creation uses FSCTL_SET_REPARSE_POINT directly; os.Symlink would
// create a symlink and require different privileges.
package winlink

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"symlinkermanager/internal/pathutil"
	"symlinkermanager/internal/scan"
)

const reparseTagSymlink = 0xA000000C

// Inspect reports what currently occupies path without following a final
// junction or symlink.
func Inspect(path string) (scan.TargetInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return scan.TargetInfo{Kind: scan.TargetMissing}, nil
		}
		return scan.TargetInfo{}, err
	}

	if isReparsePoint(info) {
		tag, err := readReparseTag(path)
		if err != nil {
			return scan.TargetInfo{}, err
		}
		switch tag {
		case windows.IO_REPARSE_TAG_MOUNT_POINT:
			return scan.TargetInfo{Kind: scan.TargetJunction, LinkTarget: readLinkTarget(path)}, nil
		case reparseTagSymlink:
			return scan.TargetInfo{Kind: scan.TargetSymlink, LinkTarget: readLinkTarget(path)}, nil
		default:
			return scan.TargetInfo{Kind: scan.TargetOtherLink}, nil
		}
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return scan.TargetInfo{}, err
		}
		return scan.TargetInfo{Kind: scan.TargetDir, Empty: len(entries) == 0}, nil
	}
	return scan.TargetInfo{Kind: scan.TargetFile}, nil
}

// Create creates a directory junction at target pointing to source. The
// target path must not exist; its parent directory must exist.
func Create(source, target string) error {
	src, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("解析源路径失败: %w", err)
	}
	tgt, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("解析目标路径失败: %w", err)
	}
	src, tgt = filepath.Clean(src), filepath.Clean(tgt)

	if _, err := os.Lstat(tgt); err == nil {
		return fmt.Errorf("目标已存在: %s", tgt)
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Mkdir(tgt, 0o777); err != nil {
		return fmt.Errorf("创建链接占位目录失败: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tgt)
		}
	}()

	data, err := encodeMountPoint(src)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(extendedPath(tgt)),
		windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return fmt.Errorf("打开目标目录失败: %w", err)
	}
	defer windows.CloseHandle(handle)

	var returned uint32
	if err := windows.DeviceIoControl(
		handle,
		windows.FSCTL_SET_REPARSE_POINT,
		&data[0],
		uint32(len(data)),
		nil,
		0,
		&returned,
		nil,
	); err != nil {
		return fmt.Errorf("设置 junction 失败: %w", err)
	}
	ok = true
	return nil
}

// Remove deletes the junction or symlink itself. The destination directory
// and its contents are never touched.
func Remove(path string) error {
	return os.Remove(path)
}

// IsElevated reports whether the current process runs with an elevated token.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func isReparsePoint(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func readReparseTag(path string) (uint32, error) {
	buf, err := readReparseBuffer(path)
	if err != nil {
		return 0, err
	}
	if len(buf) < 4 {
		return 0, errors.New("reparse 数据过短")
	}
	return binary.LittleEndian.Uint32(buf[0:4]), nil
}

func readLinkTarget(path string) string {
	got, err := os.Readlink(path)
	if err == nil && got != "" {
		return got
	}
	// Fall back to the substitute name stored in the reparse buffer.
	buf, bufErr := readReparseBuffer(path)
	if bufErr != nil {
		return ""
	}
	target, parseErr := parseSubstituteName(buf)
	if parseErr != nil {
		return ""
	}
	return target
}

func readReparseBuffer(path string) ([]byte, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(extendedPath(path)),
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("打开重解析点失败: %w", err)
	}
	defer windows.CloseHandle(handle)

	buf := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	if err := windows.DeviceIoControl(
		handle,
		windows.FSCTL_GET_REPARSE_POINT,
		nil,
		0,
		&buf[0],
		uint32(len(buf)),
		&returned,
		nil,
	); err != nil {
		return nil, fmt.Errorf("读取重解析点失败: %w", err)
	}
	if int(returned) > len(buf) {
		return nil, errors.New("reparse 数据长度异常")
	}
	return buf[:returned], nil
}

func encodeMountPoint(target string) ([]byte, error) {
	ntTarget := ntPath(target)
	print16, err := windows.UTF16FromString(target)
	if err != nil {
		return nil, fmt.Errorf("编码目标路径失败: %w", err)
	}
	nt16, err := windows.UTF16FromString(ntTarget)
	if err != nil {
		return nil, fmt.Errorf("编码 NT 路径失败: %w", err)
	}

	// 8 bytes for the four mount-point uint16 fields, then both NUL-terminated
	// UTF-16 strings.
	dataLen := 8 + len(nt16)*2 + len(print16)*2
	buf := make([]byte, 8+dataLen)

	binary.LittleEndian.PutUint32(buf[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(dataLen))
	// buf[6:8] reserved = 0

	binary.LittleEndian.PutUint16(buf[8:10], 0) // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:12], uint16((len(nt16)-1)*2))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(len(nt16)*2))
	binary.LittleEndian.PutUint16(buf[14:16], uint16((len(print16)-1)*2))

	off := 16
	for _, u := range nt16 {
		binary.LittleEndian.PutUint16(buf[off:off+2], u)
		off += 2
	}
	for _, u := range print16 {
		binary.LittleEndian.PutUint16(buf[off:off+2], u)
		off += 2
	}
	return buf, nil
}

func parseSubstituteName(buf []byte) (string, error) {
	if len(buf) < 16 {
		return "", errors.New("reparse 数据过短")
	}
	substOff := int(binary.LittleEndian.Uint16(buf[8:10]))
	substLen := int(binary.LittleEndian.Uint16(buf[10:12]))
	start := 16 + substOff
	end := start + substLen
	if start < 16 || end > len(buf) || substLen%2 != 0 {
		return "", errors.New("reparse 目标路径越界")
	}
	u := make([]uint16, substLen/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(buf[start+2*i : start+2*i+2])
	}
	s := windows.UTF16ToString(u)
	switch {
	case strings.HasPrefix(s, `\??\UNC\`):
		return `\\` + s[len(`\??\UNC\`):], nil
	case strings.HasPrefix(s, `\??\`):
		return s[len(`\??\`):], nil
	default:
		return s, nil
	}
}

func ntPath(p string) string {
	p = filepath.Clean(p)
	switch {
	case strings.HasPrefix(p, `\\?\`):
		return `\??\` + p[4:]
	case strings.HasPrefix(p, `\\`):
		return `\??\UNC\` + p[2:]
	default:
		return `\??\` + p
	}
}

func extendedPath(p string) string {
	p = filepath.Clean(p)
	switch {
	case strings.HasPrefix(p, `\\?\`):
		return p
	case strings.HasPrefix(p, `\\`):
		return `\\?\UNC\` + p[2:]
	default:
		return `\\?\` + p
	}
}

// EqualTargets reports whether a junction's stored target matches want.
func EqualTargets(got, want string) bool {
	return pathutil.Equal(strings.TrimSuffix(got, `\`), strings.TrimSuffix(want, `\`))
}

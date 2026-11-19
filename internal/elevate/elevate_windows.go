//go:build windows

// Package elevate launches a helper copy of this executable with UAC
// elevation. Windows keeps the helper's exit code and waits for it, so the
// GUI can retry failed entries without losing its window or its single
// instance lock.
package elevate

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrCancelled reports that the user dismissed the UAC prompt.
var ErrCancelled = errors.New("已取消管理员授权")

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo mirrors SHELLEXECUTEINFOW. The union of hIcon and
// hMonitor has the size of a handle.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     unsafe.Pointer
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

// RunElevated starts exePath with args through the "runas" verb and waits
// for it to exit. It returns the child's exit code, or ErrCancelled when
// the user dismisses the UAC prompt.
func RunElevated(exePath string, args []string) (uint32, error) {
	exe16, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return 0, fmt.Errorf("编码程序路径失败: %w", err)
	}
	verb16, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 0, err
	}
	params := ""
	for i, arg := range args {
		if i > 0 {
			params += " "
		}
		params += syscall.EscapeArg(arg)
	}
	params16, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return 0, fmt.Errorf("编码参数失败: %w", err)
	}

	// ShellExecuteEx may load shell extensions that expect COM on this
	// thread. S_FALSE (1) also means COM is usable and must be released.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	} else if errno, ok := err.(syscall.Errno); ok && errno == 1 {
		defer windows.CoUninitialize()
	}

	info := shellExecuteInfo{
		cbSize:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:       verb16,
		lpFile:       exe16,
		lpParameters: params16,
		nShow:        windows.SW_SHOWNORMAL,
	}
	r1, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		if callErr == windows.ERROR_CANCELLED {
			return 0, ErrCancelled
		}
		if callErr != nil && callErr != syscall.Errno(0) {
			return 0, fmt.Errorf("请求管理员权限失败: %w", callErr)
		}
		return 0, errors.New("请求管理员权限失败")
	}
	if info.hProcess == 0 {
		return 0, errors.New("管理员进程未返回进程句柄")
	}
	defer windows.CloseHandle(info.hProcess)

	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("等待管理员进程失败: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, fmt.Errorf("读取管理员进程退出码失败: %w", err)
	}
	return code, nil
}

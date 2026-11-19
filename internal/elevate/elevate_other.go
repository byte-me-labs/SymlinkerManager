//go:build !windows

// Package elevate is Windows-only; this stub keeps the app buildable for
// future ports.
package elevate

import "errors"

// ErrCancelled reports that elevation was dismissed.
var ErrCancelled = errors.New("已取消管理员授权")

// RunElevated always fails outside Windows.
func RunElevated(string, []string) (uint32, error) {
	return 0, errors.New("提权重试仅支持 Windows")
}

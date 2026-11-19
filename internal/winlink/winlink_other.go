//go:build !windows

// Package winlink is a Windows-only implementation; this stub keeps the
// abstraction buildable elsewhere for future ports.
package winlink

import (
	"errors"

	"symlinkermanager/internal/scan"
)

var errUnsupported = errors.New("目录 junction 仅支持 Windows")

// Inspect always fails outside Windows.
func Inspect(string) (scan.TargetInfo, error) { return scan.TargetInfo{}, errUnsupported }

// Create always fails outside Windows.
func Create(string, string) error { return errUnsupported }

// Remove always fails outside Windows.
func Remove(string) error { return errUnsupported }

// IsElevated is always false outside Windows.
func IsElevated() bool { return false }

// EqualTargets compares paths case-insensitively.
func EqualTargets(a, b string) bool { return a == b }

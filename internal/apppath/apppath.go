// Package apppath resolves the portable config location: next to the
// executable, unless SYMLINKERMANAGER_CONFIG overrides it (used by dev runs).
package apppath

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigEnv is the environment variable that overrides the config path.
const ConfigEnv = "SYMLINKERMANAGER_CONFIG"

// ConfigPath returns the absolute path of config.json.
func ConfigPath() (string, error) {
	if p := os.Getenv(ConfigEnv); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("解析 %s 失败: %w", ConfigEnv, err)
		}
		return abs, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取程序路径失败: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "config.json"), nil
}

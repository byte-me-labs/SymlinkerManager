// Package config owns the portable config.json format and its validation.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"symlinkermanager/internal/pathutil"
)

// CurrentVersion is the only config schema version this build understands.
const CurrentVersion = 1

// Link is one directory junction managed by the app.
type Link struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// Config is the on-disk config.json document.
type Config struct {
	Version int    `json:"version"`
	Links   []Link `json:"links"`
}

// New returns an empty, valid config.
func New() *Config {
	return &Config{Version: CurrentVersion, Links: []Link{}}
}

// Load reads and parses a config file. A missing file is returned as
// os.ErrNotExist so the caller can decide whether to create it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if c.Links == nil {
		c.Links = []Link{}
	}
	return &c, nil
}

// LoadOrCreate loads the config, creating an empty one when the file does not
// exist. The bool reports whether a new file was written.
func LoadOrCreate(path string) (*Config, bool, error) {
	c, err := Load(path)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	c = New()
	if err := c.Save(path); err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// Save writes the config atomically (temp file in the same directory, then
// rename) so a crash never leaves a half-written config.json.
func (c *Config) Save(path string) error {
	if c.Links == nil {
		c.Links = []Link{}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("编码配置失败: %w", err)
	}

	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置失败: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return fmt.Errorf("写入临时配置失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("同步临时配置失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换配置文件失败: %w", err)
	}
	return nil
}

// Issue is one validation problem. Index is -1 for config-level issues.
type Issue struct {
	Index   int
	Field   string
	Message string
}

func (i Issue) String() string {
	if i.Index < 0 {
		return i.Message
	}
	return fmt.Sprintf("第 %d 条（%s）：%s", i.Index+1, i.Field, i.Message)
}

// Validate reports every problem that must be fixed before the config may be
// saved or applied.
func (c *Config) Validate() []Issue {
	var issues []Issue
	if c.Version != CurrentVersion {
		issues = append(issues, Issue{Index: -1, Field: "version", Message: fmt.Sprintf("不支持的配置版本 %d，期望 %d", c.Version, CurrentVersion)})
	}

	names := make(map[string]int, len(c.Links))
	targets := make([]string, len(c.Links))
	sources := make([]string, len(c.Links))

	for i, l := range c.Links {
		name := strings.TrimSpace(l.Name)
		if name == "" {
			issues = append(issues, Issue{Index: i, Field: "name", Message: "名称不能为空"})
		} else if first, ok := names[strings.ToLower(name)]; ok {
			issues = append(issues, Issue{Index: i, Field: "name", Message: fmt.Sprintf("名称与第 %d 条重复", first+1)})
		} else {
			names[strings.ToLower(name)] = i
		}

		src, err := pathutil.Normalize(l.Source)
		if err != nil {
			issues = append(issues, Issue{Index: i, Field: "source", Message: err.Error()})
		} else {
			sources[i] = src
		}

		tgt, err := pathutil.Normalize(l.Target)
		if err != nil {
			issues = append(issues, Issue{Index: i, Field: "target", Message: err.Error()})
		} else {
			targets[i] = tgt
		}

		if sources[i] != "" && targets[i] != "" {
			switch {
			case pathutil.Equal(sources[i], targets[i]):
				issues = append(issues, Issue{Index: i, Field: "target", Message: "源路径与目标路径相同"})
			case pathutil.IsWithin(sources[i], targets[i]):
				issues = append(issues, Issue{Index: i, Field: "source", Message: "源路径位于目标路径之内"})
			case pathutil.IsWithin(targets[i], sources[i]):
				issues = append(issues, Issue{Index: i, Field: "target", Message: "目标路径位于源路径之内"})
			}
		}
	}

	// Duplicate targets.
	firstTarget := make(map[string]int, len(targets))
	for i := range targets {
		if targets[i] == "" {
			continue
		}
		key := pathutil.Key(targets[i])
		if first, ok := firstTarget[key]; ok {
			issues = append(issues, Issue{Index: i, Field: "target", Message: fmt.Sprintf("目标路径与第 %d 条重复", first+1)})
		} else {
			firstTarget[key] = i
		}
	}

	// Nested targets. Any two entries can produce a junction inside another
	// junction's target. Both sides are blocked, otherwise the outer entry
	// could be applied alone and silently move the inner target inside the
	// data disk.
	for i := 0; i < len(targets); i++ {
		if targets[i] == "" {
			continue
		}
		for j := i + 1; j < len(targets); j++ {
			if targets[j] == "" {
				continue
			}
			switch {
			case pathutil.IsWithin(targets[j], targets[i]):
				issues = append(issues, Issue{Index: j, Field: "target", Message: fmt.Sprintf("目标路径位于第 %d 条的目标路径之内", i+1)})
				issues = append(issues, Issue{Index: i, Field: "target", Message: fmt.Sprintf("目标路径包含第 %d 条的目标路径", j+1)})
			case pathutil.IsWithin(targets[i], targets[j]):
				issues = append(issues, Issue{Index: i, Field: "target", Message: fmt.Sprintf("目标路径位于第 %d 条的目标路径之内", j+1)})
				issues = append(issues, Issue{Index: j, Field: "target", Message: fmt.Sprintf("目标路径包含第 %d 条的目标路径", i+1)})
			}
		}
	}

	return issues
}

// SourcePath returns the resolved absolute source path.
func (l Link) SourcePath() (string, error) {
	return pathutil.Normalize(l.Source)
}

// TargetPath returns the resolved absolute target path.
func (l Link) TargetPath() (string, error) {
	return pathutil.Normalize(l.Target)
}

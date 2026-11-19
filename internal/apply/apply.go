// Package apply executes the user-selected entries. It owns the destructive
// rules: what may be replaced automatically, how a conflicting target is
// renamed aside, and how failures are reported without stopping the rest of
// the batch.
package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"symlinkermanager/internal/config"
	"symlinkermanager/internal/scan"
)

// Intent is the per-row choice for entries that are not safe by default.
type Intent int

const (
	// IntentAuto uses the scanner's safe decision.
	IntentAuto Intent = iota
	// IntentCreateSource allows a missing source directory to be created.
	IntentCreateSource
)

// Request is one row's checkbox and intent.
type Request struct {
	Index   int
	Checked bool
	Intent  Intent
}

// Linker performs the two file-system primitives. The Windows junction
// implementation lives in winlink; tests use a fake.
type Linker interface {
	Create(source, target string) error
	Remove(path string) error
}

// Outcome is the result of one requested row.
type Outcome struct {
	Index   int
	Action  string
	Message string
	Err     error
}

// Failed reports whether the row failed to apply.
func (o Outcome) Failed() bool { return o.Err != nil }

// Options injects file-system primitives and the clock for tests.
type Options struct {
	MkdirAll   func(string, os.FileMode) error
	RemovePath func(string) error
	Rename     func(string, string) error
	Now        func() time.Time
}

func (o Options) withDefaults() Options {
	if o.MkdirAll == nil {
		o.MkdirAll = os.MkdirAll
	}
	if o.RemovePath == nil {
		o.RemovePath = os.Remove
	}
	if o.Rename == nil {
		o.Rename = os.Rename
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Run applies every checked request in order. A failure never stops the
// remaining rows. scanOne must return a fresh result for the index.
func Run(cfg *config.Config, reqs []Request, scanOne func(int) scan.Result, linker Linker, opts Options) []Outcome {
	opts = opts.withDefaults()
	out := make([]Outcome, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, runOne(cfg, req, scanOne, linker, opts))
	}
	return out
}

func runOne(cfg *config.Config, req Request, scanOne func(int) scan.Result, linker Linker, opts Options) Outcome {
	out := Outcome{Index: req.Index}
	if req.Index < 0 || req.Index >= len(cfg.Links) {
		out.Action = "跳过"
		out.Message = "条目不存在"
		return out
	}
	if !req.Checked {
		out.Action = "未选中"
		return out
	}

	res := scanOne(req.Index)
	switch res.Status {
	case scan.StatusOK:
		out.Action = "无需处理"
		out.Message = "链接已正确"
		return out
	case scan.StatusInvalid, scan.StatusError:
		out.Action = "跳过"
		out.Message = res.Status.Label() + "：" + res.Message
		return out
	case scan.StatusSourceInvalid:
		out.Action = "失败"
		out.Err = fmt.Errorf("源路径不是目录: %s", res.Source)
		return out
	case scan.StatusSourceMissing:
		if req.Intent != IntentCreateSource {
			out.Action = "跳过"
			out.Message = "源目录不存在"
			return out
		}
		if err := opts.MkdirAll(res.Source, 0o755); err != nil {
			out.Action = "失败"
			out.Err = fmt.Errorf("创建源目录失败: %w", err)
			return out
		}
		return create(out, linker, opts, res.Source, res.Target, "创建源目录并建链")
	case scan.StatusMissing:
		return create(out, linker, opts, res.Source, res.Target, "创建链接")
	case scan.StatusReplaceable:
		if err := removeExisting(res, linker, opts); err != nil {
			out.Action = "失败"
			out.Err = err
			return out
		}
		return create(out, linker, opts, res.Source, res.Target, "替换链接")
	case scan.StatusConflict:
		// Conflicting targets are handled by a separate rename action, which
		// never creates a link. Applying the row itself changes nothing.
		out.Action = "跳过"
		out.Message = "目标已存在且非空，请先重命名"
		return out
	default:
		out.Action = "跳过"
		out.Message = res.Status.Label()
		return out
	}
}

func removeExisting(res scan.Result, linker Linker, opts Options) error {
	switch res.Info.Kind {
	case scan.TargetDir:
		if err := opts.RemovePath(res.Target); err != nil {
			return fmt.Errorf("删除空目录失败: %w", err)
		}
	default:
		if err := linker.Remove(res.Target); err != nil {
			return fmt.Errorf("移除旧链接失败: %w", err)
		}
	}
	return nil
}

func create(out Outcome, linker Linker, opts Options, source, target, action string) Outcome {
	if err := opts.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		out.Action = "失败"
		out.Err = fmt.Errorf("创建目标父目录失败: %w", err)
		return out
	}
	if err := linker.Create(source, target); err != nil {
		out.Action = "失败"
		out.Err = fmt.Errorf("创建链接失败: %w", err)
		return out
	}
	out.Action = action
	out.Message = target + " -> " + source
	return out
}

// Rename moves a conflicting target aside to a unique timestamped sibling
// name and returns the new path. It deliberately does not create a link: the
// caller rescans, and the entry can be created afterwards like any other
// missing target.
func Rename(target string, opts Options) (string, error) {
	opts = opts.withDefaults()
	renamed, err := uniqueRenamePath(target, opts.Now())
	if err != nil {
		return "", fmt.Errorf("生成重命名路径失败: %w", err)
	}
	if err := opts.Rename(target, renamed); err != nil {
		return "", fmt.Errorf("重命名目标失败: %w", err)
	}
	return renamed, nil
}

// uniqueRenamePath returns a sibling name that does not exist yet. The
// timestamp keeps repeated runs from colliding, and the .bak marker makes the
// moved directory recognizable in Explorer.
func uniqueRenamePath(target string, now time.Time) (string, error) {
	base := target + ".bak-" + now.Format("20060102-150405")
	for i := 1; i <= 999; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("同名目标过多: %s", base)
}

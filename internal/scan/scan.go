// Package scan derives the per-entry status shown in the window. It never
// modifies the file system: checks only.
package scan

import (
	"fmt"
	"os"

	"symlinkermanager/internal/config"
	"symlinkermanager/internal/pathutil"
)

// TargetKind is what currently occupies the target path.
type TargetKind int

const (
	TargetMissing TargetKind = iota
	TargetDir
	TargetJunction
	TargetSymlink
	TargetFile
	TargetOtherLink
)

// TargetInfo is the inspector's answer about one target path.
type TargetInfo struct {
	Kind       TargetKind
	LinkTarget string // set for TargetJunction and TargetSymlink
	Empty      bool   // set for TargetDir
}

// Inspector inspects a path without following a final junction. The Windows
// implementation lives in the winlink package; tests use fakes.
type Inspector interface {
	Inspect(path string) (TargetInfo, error)
}

// Status is the state of one entry.
type Status int

const (
	StatusOK Status = iota
	StatusMissing
	StatusReplaceable
	StatusConflict
	StatusSourceMissing
	StatusSourceInvalid
	StatusInvalid
	StatusError
)

// Label is the Chinese text shown in the status column.
func (s Status) Label() string {
	switch s {
	case StatusOK:
		return "已正确"
	case StatusMissing:
		return "待创建"
	case StatusReplaceable:
		return "可替换"
	case StatusConflict:
		return "冲突"
	case StatusSourceMissing:
		return "源缺失"
	case StatusSourceInvalid:
		return "源不是目录"
	case StatusInvalid:
		return "配置无效"
	case StatusError:
		return "检查失败"
	default:
		return "未知"
	}
}

// Result is the scan outcome for one config entry.
type Result struct {
	Index   int
	Status  Status
	Message string
	Source  string
	Target  string
	Info    TargetInfo
}

// Scanner checks config entries against the file system.
type Scanner struct {
	inspector Inspector
	stat      func(string) (os.FileInfo, error)
}

// New returns a Scanner using the given target inspector.
func New(inspector Inspector) *Scanner {
	return &Scanner{inspector: inspector, stat: os.Stat}
}

// Scan checks every entry. Entries with validation issues are reported as
// StatusInvalid without touching the file system.
func (s *Scanner) Scan(cfg *config.Config) []Result {
	invalid := make(map[int][]string)
	for _, issue := range cfg.Validate() {
		if issue.Index >= 0 {
			invalid[issue.Index] = append(invalid[issue.Index], issue.Message)
		}
	}

	out := make([]Result, len(cfg.Links))
	for i, link := range cfg.Links {
		out[i] = Result{Index: i}
		if msgs, ok := invalid[i]; ok {
			out[i].Status = StatusInvalid
			out[i].Message = joinMessages(msgs)
			continue
		}
		out[i] = s.scanOne(i, link)
	}
	return out
}

func (s *Scanner) scanOne(index int, link config.Link) Result {
	r := Result{Index: index}

	src, err := link.SourcePath()
	if err != nil {
		return Result{Index: index, Status: StatusInvalid, Message: err.Error()}
	}
	tgt, err := link.TargetPath()
	if err != nil {
		return Result{Index: index, Status: StatusInvalid, Message: err.Error()}
	}
	r.Source, r.Target = src, tgt

	info, err := s.stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			r.Status = StatusSourceMissing
			return r
		}
		r.Status = StatusError
		r.Message = fmt.Sprintf("无法读取源目录: %v", err)
		return r
	}
	if !info.IsDir() {
		r.Status = StatusSourceInvalid
		return r
	}

	tinfo, err := s.inspector.Inspect(tgt)
	if err != nil {
		r.Status = StatusError
		r.Message = fmt.Sprintf("无法检查目标路径: %v", err)
		return r
	}
	r.Info = tinfo

	switch tinfo.Kind {
	case TargetMissing:
		r.Status = StatusMissing
	case TargetJunction:
		if tinfo.LinkTarget != "" && pathutil.Equal(tinfo.LinkTarget, src) {
			r.Status = StatusOK
		} else {
			r.Status = StatusReplaceable
			r.Message = fmt.Sprintf("旧链接指向 %s", tinfo.LinkTarget)
		}
	case TargetSymlink:
		r.Status = StatusReplaceable
		r.Message = "目标是非 junction 链接"
	case TargetDir:
		if tinfo.Empty {
			r.Status = StatusReplaceable
			r.Message = "目标是空目录"
		} else {
			r.Status = StatusConflict
			r.Message = "目标是非空目录"
		}
	case TargetFile:
		r.Status = StatusConflict
		r.Message = "目标是文件"
	case TargetOtherLink:
		r.Status = StatusConflict
		r.Message = "目标是不支持的链接类型"
	default:
		r.Status = StatusConflict
		r.Message = "目标类型未知"
	}
	return r
}

func joinMessages(msgs []string) string {
	s := ""
	for i, m := range msgs {
		if i > 0 {
			s += "；"
		}
		s += m
	}
	return s
}

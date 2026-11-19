package scan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"symlinkermanager/internal/config"
)

type fakeInspector map[string]TargetInfo

func (f fakeInspector) Inspect(path string) (TargetInfo, error) {
	info, ok := f[path]
	if !ok {
		return TargetInfo{}, errors.New("boom")
	}
	return info, nil
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(root, "file.txt")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	const target = `C:\Targets\a`

	tests := []struct {
		name      string
		link      config.Link
		info      TargetInfo
		noInspect bool
		want      Status
		message   string
	}{
		{
			name: "source missing",
			link: config.Link{Name: "a", Source: filepath.Join(root, "nope"), Target: target},
			want: StatusSourceMissing,
		},
		{
			name: "source is file",
			link: config.Link{Name: "a", Source: srcFile, Target: target},
			want: StatusSourceInvalid,
		},
		{
			name: "target missing",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetMissing},
			want: StatusMissing,
		},
		{
			name: "junction correct",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetJunction, LinkTarget: src},
			want: StatusOK,
		},
		{
			name: "junction stale",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetJunction, LinkTarget: `D:\Other`},
			want: StatusReplaceable, message: "旧链接指向",
		},
		{
			name: "symlink",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetSymlink, LinkTarget: src},
			want: StatusReplaceable, message: "非 junction",
		},
		{
			name: "empty dir",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetDir, Empty: true},
			want: StatusReplaceable, message: "空目录",
		},
		{
			name: "non-empty dir",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetDir, Empty: false},
			want: StatusConflict, message: "非空目录",
		},
		{
			name: "target is file",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetFile},
			want: StatusConflict, message: "文件",
		},
		{
			name: "other link",
			link: config.Link{Name: "a", Source: src, Target: target},
			info: TargetInfo{Kind: TargetOtherLink},
			want: StatusConflict, message: "不支持的链接",
		},
		{
			name:      "inspector error",
			link:      config.Link{Name: "a", Source: src, Target: target},
			noInspect: true,
			want:      StatusError, message: "无法检查目标路径",
		},
		{
			name: "invalid path",
			link: config.Link{Name: "a", Source: src, Target: `relative\a`},
			want: StatusInvalid, message: "不是绝对路径",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Version: config.CurrentVersion, Links: []config.Link{tt.link}}
			insp := fakeInspector{}
			if !tt.noInspect {
				insp[target] = tt.info
			}
			got := New(insp).Scan(cfg)
			if len(got) != 1 {
				t.Fatalf("expected one result, got %d", len(got))
			}
			if got[0].Status != tt.want {
				t.Fatalf("status = %v (%s), want %v (%s)", got[0].Status, got[0].Status.Label(), tt.want, tt.want.Label())
			}
			if tt.message != "" && !strings.Contains(got[0].Message, tt.message) {
				t.Errorf("message = %q, want it to contain %q", got[0].Message, tt.message)
			}
		})
	}
}

func TestScanNestedTargetsInvalid(t *testing.T) {
	root := t.TempDir()
	srcA := filepath.Join(root, "a")
	srcB := filepath.Join(root, "b")
	for _, p := range []string{srcA, srcB} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Version: config.CurrentVersion, Links: []config.Link{
		{Name: "a", Source: srcA, Target: `C:\Targets`},
		{Name: "b", Source: srcB, Target: `C:\Targets\b`},
	}}
	results := New(fakeInspector{`C:\Targets`: {Kind: TargetMissing}, `C:\Targets\b`: {Kind: TargetMissing}}).Scan(cfg)
	if results[0].Status != StatusInvalid || results[1].Status != StatusInvalid {
		t.Fatalf("expected both nested entries to be invalid, got %v and %v", results[0].Status, results[1].Status)
	}
}

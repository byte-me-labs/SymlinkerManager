package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"symlinkermanager/internal/config"
	"symlinkermanager/internal/scan"
)

func TestNearestDir(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "data", "game")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(deep, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := nearestDir(deep); !ok || got != deep {
		t.Fatalf("已存在的目录: got=%q ok=%v, 期望 %q", got, ok, deep)
	}
	missing := filepath.Join(deep, "not", "yet")
	if got, ok := nearestDir(missing); !ok || got != deep {
		t.Fatalf("缺失目录应回到最近存在的上级: got=%q ok=%v, 期望 %q", got, ok, deep)
	}
	if got, ok := nearestDir(file); !ok || got != deep {
		t.Fatalf("文件应回到所在目录: got=%q ok=%v, 期望 %q", got, ok, deep)
	}
}

func TestClickingPathOpensNearestFolder(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "data")
	missing := filepath.Join(existing, "not", "yet")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	got := ""
	old := openFolder
	openFolder = func(dir string) { got = dir }
	defer func() { openFolder = old }()

	a := newTestApp(
		[]config.Link{{Name: "源缺失", Source: missing, Target: filepath.Join(root, "targets", "x")}},
		[]scan.Result{{Index: 0, Status: scan.StatusSourceMissing, Info: scan.TargetInfo{Kind: scan.TargetMissing}}},
	)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1420, 860)

	if err := tt.Click(missing); err != nil {
		t.Fatalf("点击源路径: %v", err)
	}
	if got != existing {
		t.Fatalf("点击缺失路径应打开最近的上级: got=%q, 期望 %q", got, existing)
	}
}

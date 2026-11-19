//go:build windows

package winlink

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"symlinkermanager/internal/scan"
)

func TestCreateInspectRemove(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "数据 源")
	target := filepath.Join(root, "system", "链接")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Create(source, target); err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := Inspect(target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Kind != scan.TargetJunction {
		t.Fatalf("kind = %v, want junction", info.Kind)
	}
	if !EqualTargets(info.LinkTarget, source) {
		t.Errorf("link target = %q, want %q", info.LinkTarget, source)
	}

	data, err := os.ReadFile(filepath.Join(target, "hello.txt"))
	if err != nil {
		t.Fatalf("read through junction: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("content = %q", data)
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		t.Errorf("os.Stat through junction should report a directory: %v, %v", st, err)
	}

	if err := Remove(target); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("junction still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "hello.txt")); err != nil {
		t.Errorf("remove touched the target contents: %v", err)
	}
}

func TestInspectMissing(t *testing.T) {
	info, err := Inspect(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != scan.TargetMissing {
		t.Errorf("kind = %v, want missing", info.Kind)
	}
}

func TestInspectDirAndFile(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(empty)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != scan.TargetDir || !info.Empty {
		t.Errorf("empty dir: %+v", info)
	}

	if err := os.WriteFile(filepath.Join(empty, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = Inspect(empty)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != scan.TargetDir || info.Empty {
		t.Errorf("non-empty dir: %+v", info)
	}

	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = Inspect(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != scan.TargetFile {
		t.Errorf("file: %+v", info)
	}
}

func TestCreateExistingTargetFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, p := range []string{source, target} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := Create(source, target); err == nil {
		t.Error("Create should fail when the target already exists")
	}
}

func TestDanglingJunction(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "not-created")
	target := filepath.Join(root, "target")
	if err := Create(source, target); err != nil {
		t.Fatalf("Create dangling junction: %v", err)
	}
	info, err := Inspect(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != scan.TargetJunction || !EqualTargets(info.LinkTarget, source) {
		t.Errorf("unexpected info: %+v", info)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("os.Stat should fail for a dangling junction")
	}
	if err := Remove(target); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	target := `C:\Data\测试`
	buf, err := encodeMountPoint(target)
	if err != nil {
		t.Fatal(err)
	}
	if tag := binary.LittleEndian.Uint32(buf[0:4]); tag != windows.IO_REPARSE_TAG_MOUNT_POINT {
		t.Errorf("tag = %#x", tag)
	}
	got, err := parseSubstituteName(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("round trip = %q, want %q", got, target)
	}
}

func TestExtendedPath(t *testing.T) {
	tests := map[string]string{
		`C:\Data`:            `\\?\C:\Data`,
		`\\server\share\dir`: `\\?\UNC\server\share\dir`,
		`\\?\C:\Data`:        `\\?\C:\Data`,
	}
	for in, want := range tests {
		if got := extendedPath(in); got != want {
			t.Errorf("extendedPath(%q) = %q, want %q", in, got, want)
		}
	}
}

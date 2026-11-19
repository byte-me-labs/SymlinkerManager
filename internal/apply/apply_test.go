package apply

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"symlinkermanager/internal/config"
	"symlinkermanager/internal/scan"
)

type fakeLinker struct {
	created    [][2]string
	removed    []string
	createErr  error
	removeErr  error
	failTarget string
}

func (f *fakeLinker) Create(source, target string) error {
	f.created = append(f.created, [2]string{source, target})
	if f.createErr != nil {
		return f.createErr
	}
	if f.failTarget != "" && target == f.failTarget {
		return errors.New("boom")
	}
	return os.Mkdir(target, 0o755)
}

func (f *fakeLinker) Remove(path string) error {
	f.removed = append(f.removed, path)
	if f.removeErr != nil {
		return f.removeErr
	}
	return os.Remove(path)
}

var fixedNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func cfgFor(n int) *config.Config {
	cfg := &config.Config{Version: config.CurrentVersion}
	for i := 0; i < n; i++ {
		cfg.Links = append(cfg.Links, config.Link{Name: "x", Source: `D:\s`, Target: `C:\t`})
	}
	return cfg
}

func opts() Options {
	return Options{Now: func() time.Time { return fixedNow }}
}

func TestRunNoop(t *testing.T) {
	cfg := cfgFor(1)
	res := scan.Result{Index: 0, Status: scan.StatusOK, Source: `D:\s`, Target: `C:\t`}
	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: true}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Action != "无需处理" || out[0].Failed() {
		t.Fatalf("unexpected outcome: %+v", out[0])
	}
	if len(linker.created) != 0 || len(linker.removed) != 0 {
		t.Error("no linker calls expected")
	}
}

func TestRunUnchecked(t *testing.T) {
	cfg := cfgFor(1)
	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: false}}, func(int) scan.Result { return scan.Result{Index: 0} }, linker, opts())
	if out[0].Action != "未选中" {
		t.Fatalf("unexpected outcome: %+v", out[0])
	}
}

func TestRunMissing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "new", "deep", "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(1)
	res := scan.Result{Index: 0, Status: scan.StatusMissing, Source: source, Target: target}
	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: true}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Failed() {
		t.Fatalf("unexpected failure: %v", out[0].Err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target was not created: %v", err)
	}
	if len(linker.created) != 1 || linker.created[0] != [2]string{source, target} {
		t.Errorf("unexpected create calls: %v", linker.created)
	}
}

func TestRunReplaceableEmptyDir(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, p := range []string{source, target} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := cfgFor(1)
	res := scan.Result{Index: 0, Status: scan.StatusReplaceable, Source: source, Target: target, Info: scan.TargetInfo{Kind: scan.TargetDir, Empty: true}}
	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: true}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Failed() {
		t.Fatalf("unexpected failure: %v", out[0].Err)
	}
	if len(linker.created) != 1 {
		t.Errorf("expected one create, got %v", linker.created)
	}
}

func TestRunReplaceableJunction(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(1)
	res := scan.Result{Index: 0, Status: scan.StatusReplaceable, Source: source, Target: target, Info: scan.TargetInfo{Kind: scan.TargetJunction, LinkTarget: `D:\old`}}
	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: true}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Failed() {
		t.Fatalf("unexpected failure: %v", out[0].Err)
	}
	if len(linker.removed) != 1 || linker.removed[0] != target {
		t.Errorf("expected the old link to be removed, got %v", linker.removed)
	}
	if len(linker.created) != 1 {
		t.Errorf("expected one create, got %v", linker.created)
	}
}

func TestRunConflictWithoutIntent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(1)
	res := scan.Result{Index: 0, Status: scan.StatusConflict, Source: source, Target: target, Info: scan.TargetInfo{Kind: scan.TargetDir}}
	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: true}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Action != "跳过" || out[0].Failed() {
		t.Fatalf("unexpected outcome: %+v", out[0])
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("conflict target was modified: %v", err)
	}
	if len(linker.created) != 0 {
		t.Error("no create expected")
	}
}

func TestRenameMovesTargetAside(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	renamed, err := Rename(target, opts())
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(renamed), "target.bak-") {
		t.Errorf("unexpected renamed path: %s", renamed)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("original target should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(renamed, "keep.txt")); err != nil {
		t.Errorf("renamed directory lost its data: %v", err)
	}
}

func TestRenameMissingTargetFails(t *testing.T) {
	if _, err := Rename(filepath.Join(t.TempDir(), "missing"), opts()); err == nil {
		t.Fatal("expected an error for a missing target")
	}
}

func TestRunSourceMissing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "missing-source")
	target := filepath.Join(root, "target")
	cfg := cfgFor(1)
	res := scan.Result{Index: 0, Status: scan.StatusSourceMissing, Source: source, Target: target}

	linker := &fakeLinker{}
	out := Run(cfg, []Request{{Index: 0, Checked: true}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Action != "跳过" || len(linker.created) != 0 {
		t.Fatalf("expected a skip, got %+v", out[0])
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Error("source should not have been created without intent")
	}

	out = Run(cfg, []Request{{Index: 0, Checked: true, Intent: IntentCreateSource}}, func(int) scan.Result { return res }, linker, opts())
	if out[0].Failed() {
		t.Fatalf("unexpected failure: %v", out[0].Err)
	}
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		t.Errorf("source was not created: %v", err)
	}
	if len(linker.created) != 1 {
		t.Errorf("expected a create call, got %v", linker.created)
	}
}

func TestRunContinuesAfterFailure(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	firstTarget := filepath.Join(root, "first")
	secondTarget := filepath.Join(root, "second")
	cfg := cfgFor(2)
	results := []scan.Result{
		{Index: 0, Status: scan.StatusMissing, Source: source, Target: firstTarget},
		{Index: 1, Status: scan.StatusMissing, Source: source, Target: secondTarget},
	}
	linker := &fakeLinker{failTarget: firstTarget}
	out := Run(cfg, []Request{{Index: 0, Checked: true}, {Index: 1, Checked: true}}, func(i int) scan.Result { return results[i] }, linker, opts())
	if !out[0].Failed() {
		t.Error("expected the first row to fail")
	}
	if out[1].Failed() {
		t.Errorf("second row should still succeed: %v", out[1].Err)
	}
	if _, err := os.Stat(secondTarget); err != nil {
		t.Errorf("second target was not created: %v", err)
	}
}

func TestUniqueRenamePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	first, err := uniqueRenamePath(target, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(first, "target.bak-20261117-120000") {
		t.Errorf("unexpected renamed path: %s", first)
	}
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := uniqueRenamePath(target, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if second != first+"-2" {
		t.Errorf("expected %s-2, got %s", first, second)
	}
}

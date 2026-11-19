package apppath

import (
	"path/filepath"
	"testing"
)

func TestConfigPathOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(ConfigEnv, want)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func TestConfigPathNextToExecutable(t *testing.T) {
	t.Setenv(ConfigEnv, "")
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected an absolute path, got %q", got)
	}
	if filepath.Base(got) != "config.json" {
		t.Errorf("expected config.json, got %q", got)
	}
}

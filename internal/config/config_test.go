package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validLink(name string) Link {
	return Link{
		Name:   name,
		Source: `D:\Data\` + name,
		Target: `C:\Targets\` + name,
	}
}

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")

	c, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if !created {
		t.Error("expected created=true for a missing config")
	}
	if c.Version != CurrentVersion || len(c.Links) != 0 {
		t.Errorf("unexpected new config: %+v", c)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file was not written: %v", err)
	}

	c2, created2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if created2 {
		t.Error("expected created=false for an existing config")
	}
	if c2.Version != CurrentVersion {
		t.Errorf("unexpected version %d", c2.Version)
	}
}

func TestLoadReturnsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := New()
	c.Links = []Link{{
		Name:   "Steam 数据",
		Source: `D:\MyData\Steam\userdata`,
		Target: `%USERPROFILE%\AppData\Roaming\Steam\userdata`,
	}}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Links) != 1 || loaded.Links[0] != c.Links[0] {
		t.Errorf("round trip mismatch: %+v", loaded.Links)
	}
}

func TestSaveIsAtomicAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := New()
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	c.Links = []Link{validLink("a")}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Links) != 1 {
		t.Errorf("expected replaced content, got %+v", loaded)
	}
}

func TestLoadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected an error for corrupt JSON")
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
	  "version": 1,
	  "extra": true,
	  "links": [
	    {"name":"a","source":"D:\\Data\\a","target":"C:\\Targets\\a","enabled":true,"note":"old"}
	  ]
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// "enabled" is an old field from before the disable feature was removed;
	// it must be dropped like any other unknown field on the next save.
	for _, field := range []string{"extra", "enabled", "note"} {
		if strings.Contains(string(data), field) {
			t.Errorf("unknown field %q was not dropped: %s", field, data)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantMsg string
	}{
		{
			name: "valid",
			cfg:  &Config{Version: 1, Links: []Link{validLink("a"), validLink("b")}},
		},
		{
			name:    "bad version",
			cfg:     &Config{Version: 2, Links: []Link{validLink("a")}},
			wantMsg: "不支持的配置版本",
		},
		{
			name:    "empty name",
			cfg:     &Config{Version: 1, Links: []Link{{Name: " ", Source: `D:\Data\a`, Target: `C:\Targets\a`}}},
			wantMsg: "名称不能为空",
		},
		{
			name:    "duplicate name",
			cfg:     &Config{Version: 1, Links: []Link{validLink("a"), validLink("A")}},
			wantMsg: "名称与第 1 条重复",
		},
		{
			name: "duplicate target",
			cfg: &Config{Version: 1, Links: []Link{
				validLink("a"),
				{Name: "b", Source: `D:\Data\b`, Target: `C:\Targets\a`},
			}},
			wantMsg: "目标路径与第 1 条重复",
		},
		{
			name: "nested targets",
			cfg: &Config{Version: 1, Links: []Link{
				{Name: "a", Source: `D:\Data\a`, Target: `C:\Targets`},
				{Name: "b", Source: `D:\Data\b`, Target: `C:\Targets\b`},
			}},
			wantMsg: "目标路径位于第 1 条的目标路径之内",
		},
		{
			name: "relative source",
			cfg: &Config{Version: 1, Links: []Link{
				{Name: "a", Source: `Data\a`, Target: `C:\Targets\a`},
			}},
			wantMsg: "不是绝对路径",
		},
		{
			name: "same source and target",
			cfg: &Config{Version: 1, Links: []Link{
				{Name: "a", Source: `C:\Foo`, Target: `C:\Foo`},
			}},
			wantMsg: "源路径与目标路径相同",
		},
		{
			name: "source inside target",
			cfg: &Config{Version: 1, Links: []Link{
				{Name: "a", Source: `C:\Foo\Bar`, Target: `C:\Foo`},
			}},
			wantMsg: "源路径位于目标路径之内",
		},
		{
			name: "target inside source",
			cfg: &Config{Version: 1, Links: []Link{
				{Name: "a", Source: `C:\Foo`, Target: `C:\Foo\Bar`},
			}},
			wantMsg: "目标路径位于源路径之内",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := tt.cfg.Validate()
			if tt.wantMsg == "" {
				if len(issues) != 0 {
					t.Fatalf("expected no issues, got %v", issues)
				}
				return
			}
			var found bool
			for _, is := range issues {
				if strings.Contains(is.Message, tt.wantMsg) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected issue containing %q, got %v", tt.wantMsg, issues)
			}
		})
	}
}

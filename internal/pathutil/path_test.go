package pathutil

import (
	"path/filepath"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("SLM_TEST_ROOT", `C:\Data Disk`)
	t.Setenv("ProgramFiles(x86)", `C:\Program Files (x86)`)

	tests := []struct {
		in   string
		want string
	}{
		{`%SLM_TEST_ROOT%\data`, `C:\Data Disk\data`},
		{`%SLM_TEST_ROOT%/data`, `C:\Data Disk/data`},
		{`%ProgramFiles(x86)%\App`, `C:\Program Files (x86)\App`},
		{`$SLM_TEST_ROOT\data`, `C:\Data Disk\data`},
		{`${SLM_TEST_ROOT}\data`, `C:\Data Disk\data`},
		{`%SLM_TEST_UNKNOWN%\data`, `%SLM_TEST_UNKNOWN%\data`},
		{`C:\data$literal`, `C:\data$literal`},
		{`100%%done`, `100%%done`},
	}
	for _, tt := range tests {
		if got := ExpandEnv(tt.in); got != tt.want {
			t.Errorf("ExpandEnv(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	t.Setenv("SLM_TEST_ROOT", `C:\Data Disk`)

	got, err := Normalize(` %SLM_TEST_ROOT%\a\b\..\c\ `)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if want := `C:\Data Disk\a\c`; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}

	if _, err := Normalize(`relative\path`); err == nil {
		t.Error("Normalize accepted a relative path")
	}
	if _, err := Normalize(`   `); err == nil {
		t.Error("Normalize accepted an empty path")
	}
	if _, err := Normalize(`%SLM_TEST_UNKNOWN%\data`); err == nil {
		t.Error("Normalize accepted an unexpanded environment variable")
	}
}

func TestEqualAndKey(t *testing.T) {
	if !Equal(`C:\Foo\Bar`, `c:\foo\bar\`) {
		t.Error("Equal should compare case-insensitively and ignore trailing separators")
	}
	if Equal(`C:\Foo`, `C:\Foo\Bar`) {
		t.Error("Equal reported different paths as equal")
	}
	if Key(`C:\Foo\Bar`) != `c:\foo\bar` {
		t.Errorf("unexpected key %q", Key(`C:\Foo\Bar`))
	}
}

func TestIsWithin(t *testing.T) {
	tests := []struct {
		child, parent string
		want          bool
	}{
		{`C:\Foo\Bar`, `C:\Foo`, true},
		{`c:\foo\bar\baz`, `C:\Foo`, true},
		{`C:\Foo`, `C:\Foo`, false},
		{`C:\FooBar`, `C:\Foo`, false},
		{`C:\Foo`, `C:\Foo\Bar`, false},
		{`D:\Foo\Bar`, `C:\Foo`, false},
		{`C:\Foo\..\Bar`, `C:\Foo`, false},
	}
	for _, tt := range tests {
		if got := IsWithin(tt.child, tt.parent); got != tt.want {
			t.Errorf("IsWithin(%q, %q) = %v, want %v", tt.child, tt.parent, got, tt.want)
		}
	}
}

func TestSuggestEnv(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\SLMTestUser`)
	t.Setenv("APPDATA", `C:\Users\SLMTestUser\AppData\Roaming`)

	tests := []struct {
		in     string
		want   string
		name   string
		wantOK bool
	}{
		{`C:\Users\SLMTestUser\Documents`, `%USERPROFILE%\Documents`, "USERPROFILE", true},
		{`C:\Users\SLMTestUser`, `%USERPROFILE%`, "USERPROFILE", true},
		{`c:\users\slmtestuser\Docs`, `%USERPROFILE%\Docs`, "USERPROFILE", true},
		{`C:\Users\SLMTestUser\AppData\Roaming\Steam`, `%APPDATA%\Steam`, "APPDATA", true},
		{`C:\Users\SLMTestUserX`, ``, ``, false},
		{`D:\Data\docs`, ``, ``, false},
		{`relative\path`, ``, ``, false},
	}
	for _, tt := range tests {
		got, name, ok := SuggestEnv(tt.in)
		if ok != tt.wantOK || got != tt.want || name != tt.name {
			t.Errorf("SuggestEnv(%q) = %q, %q, %v; want %q, %q, %v",
				tt.in, got, name, ok, tt.want, tt.name, tt.wantOK)
		}
	}
}

func TestNormalizeKeepsSeparatorStyle(t *testing.T) {
	got, err := Normalize(`C:/Foo/Bar/`)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(`C:\Foo\Bar`) {
		t.Errorf("Normalize = %q", got)
	}
}

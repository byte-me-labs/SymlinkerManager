// Package pathutil provides Windows path helpers shared by the config,
// status scanner and linker layers.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandEnv expands both Windows-style %VAR% and Go-style $VAR/${VAR}
// environment variables. Unknown %VAR% tokens are left untouched so that the
// caller can report a useful validation error instead of silently producing a
// shortened path.
func ExpandEnv(s string) string {
	s = expandPercent(s)
	return os.Expand(s, os.Getenv)
}

func expandPercent(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		name := s[i+1 : i+1+j]
		if name == "" {
			// Literal "%%": keep the first percent and continue after it.
			b.WriteString(s[:i+1])
			s = s[i+1:]
			continue
		}
		b.WriteString(s[:i])
		if v, ok := os.LookupEnv(name); ok {
			b.WriteString(v)
		} else {
			// Keep the token so validation can reject the path.
			b.WriteString(s[i : i+j+2])
		}
		s = s[i+j+2:]
	}
}

// suggestionVars lists the environment variables offered when a browsed path
// lies under one of them. The most specific (longest) matching root wins, so a
// path under %LOCALAPPDATA% is offered as such rather than as %USERPROFILE%.
// The list order breaks ties between variables with the same value.
var suggestionVars = []string{
	"USERPROFILE",
	"APPDATA",
	"LOCALAPPDATA",
	"PROGRAMDATA",
	"PUBLIC",
	"ProgramFiles",
	"ProgramFiles(x86)",
	"ProgramW6432",
	"SystemRoot",
	"TEMP",
	"TMP",
}

// SuggestEnv rewrites an absolute path that lies under one of the well-known
// environment variables above into its %VAR% form, and reports the variable's
// name. Comparison is case-insensitive, matching Windows paths; the part after
// the variable keeps its original spelling.
func SuggestEnv(p string) (suggestion, name string, ok bool) {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return "", "", false
	}
	best, bestName := "", ""
	for _, candidate := range suggestionVars {
		root := os.Getenv(candidate)
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if !filepath.IsAbs(root) || !hasPathPrefix(p, root) {
			continue
		}
		if len(root) > len(best) {
			best, bestName = root, candidate
		}
	}
	if bestName == "" {
		return "", "", false
	}
	rest := strings.TrimPrefix(p[len(best):], `\`)
	if rest == "" {
		return "%" + bestName + "%", bestName, true
	}
	return "%" + bestName + "%" + `\` + rest, bestName, true
}

// hasPathPrefix reports whether p equals root or lies below it, ignoring case
// and requiring a separator boundary. root is already filepath.Clean-ed.
func hasPathPrefix(p, root string) bool {
	if len(p) < len(root) || !strings.EqualFold(p[:len(root)], root) {
		return false
	}
	if len(p) == len(root) {
		return true
	}
	// A root that already ends in a separator (a drive root such as C:\) needs
	// no boundary check; otherwise the next byte must start a new component.
	return p[len(root)] == '\\' || strings.HasSuffix(root, `\`)
}

// Normalize expands environment variables, cleans the path and verifies that
// the result is absolute. It does not touch the file system.
func Normalize(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径为空")
	}
	expanded := ExpandEnv(p)
	if expanded == "" {
		return "", fmt.Errorf("环境变量展开后路径为空: %s", p)
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("不是绝对路径: %s", p)
	}
	return filepath.Clean(expanded), nil
}

// Key returns a comparison key for Windows path equality: cleaned and
// lower-cased.
func Key(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

// Equal reports whether two absolute paths refer to the same location.
func Equal(a, b string) bool {
	return Key(a) == Key(b)
}

// IsWithin reports whether child lies strictly inside parent. Paths on
// different volumes are never within each other. Comparison is
// case-insensitive, matching Windows semantics.
func IsWithin(child, parent string) bool {
	c, p := Key(child), Key(parent)
	if c == p {
		return false
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

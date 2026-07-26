package workspacestate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func CanonicalWorkspaceIdentity(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("workspace identity is required")
	}
	if strings.Contains(value, "\x00") {
		return "", errors.New("workspace identity is invalid")
	}
	for _, component := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return "", errors.New("workspace identity traversal is invalid")
		}
	}
	abs, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	// Resolve the deepest existing ancestor as well as an existing final path.
	// EvalSymlinks on the complete path returns ENOENT for a new workspace below
	// a symlinked directory, which would otherwise give the alias a second lock.
	candidate := abs
	var suffix []string
	for {
		physical, evalErr := filepath.EvalSymlinks(candidate)
		if evalErr == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				physical = filepath.Join(physical, suffix[i])
			}
			return filepath.Clean(physical), nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", evalErr
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return filepath.Clean(abs), nil
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = parent
	}
}

func SameCanonicalWorkspacePath(a, b string) bool {
	ca, ea := CanonicalWorkspaceIdentity(a)
	cb, eb := CanonicalWorkspaceIdentity(b)
	return ea == nil && eb == nil && ca == cb
}

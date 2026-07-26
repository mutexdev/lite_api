// No Go package qualifier may appear inside a user-visible string literal.
//
// The package extraction rewrote bare type names to `types.X` across ~30,000
// lines. A regex cannot tell a Go identifier from the same characters inside a
// string, so six user-visible names were corrupted and shipped for several
// commits before a coverage sweep happened to surface one:
//
//	"Untitled Collection"  -> "Untitled types.Collection"
//	"Insomnia Collection"  -> "Insomnia types.Collection"
//	"Environment %d"       -> "types.Environment %d"
//	"Imported Environment" -> "Imported types.Environment"   (x2)
//	"Environment"          -> "types.Environment"
//
// Every one is a name shown in the sidebar after an import. Nothing failed,
// nothing logged, and the tests all passed — they assert on ids and counts, not
// on the fallback name a collection gets when its source has no title.
//
// This test is the tripwire that class of mistake needed. It scans the source
// rather than the behaviour, because the behaviour is "a label looks wrong",
// which no unit test was ever going to notice.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNoPackageQualifierLeakedIntoAStringLiteral(t *testing.T) {
	// Package names introduced by the extraction. A qualifier for one of these
	// inside a string is almost certainly a rewritten identifier.
	qualifier := regexp.MustCompile(`\b(types|scalar|interp|codegen|wsexec|cookiejar|openapisync|yamlstore|grpcexec)\.[A-Z]\w*`)

	var offenders []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", "frontend", "build", ".git", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for lineNo, literal := range stringLiteralsIn(string(data)) {
			_ = lineNo
			if qualifier.MatchString(literal.text) {
				offenders = append(offenders, path+":"+literal.line+"  "+literal.text)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, o := range offenders {
		t.Errorf("package qualifier inside a string literal: %s", o)
	}
}

type goStringLiteral struct {
	line string
	text string
}

// stringLiteralsIn extracts interpreted string literals with a real scanner
// rather than a regex. A regex over quotes matches the Go code BETWEEN two
// literals — that mistake stripped 16 working call sites during the same
// extraction, so the scanner exists for the same reason the test does.
func stringLiteralsIn(src string) map[int]goStringLiteral {
	out := map[int]goStringLiteral{}
	line, idx := 1, 0
	inLineComment, inBlockComment, inRaw := false, false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			line++
			inLineComment = false
			continue
		}
		switch {
		case inLineComment:
			continue
		case inBlockComment:
			if strings.HasPrefix(src[i:], "*/") {
				inBlockComment = false
				i++
			}
			continue
		case inRaw:
			if c == '`' {
				inRaw = false
			}
			continue
		}
		if strings.HasPrefix(src[i:], "//") {
			inLineComment = true
			continue
		}
		if strings.HasPrefix(src[i:], "/*") {
			inBlockComment = true
			i++
			continue
		}
		if c == '`' {
			inRaw = true
			continue
		}
		if c != '"' {
			continue
		}
		var sb strings.Builder
		j := i + 1
		for ; j < len(src) && src[j] != '"'; j++ {
			if src[j] == '\\' {
				j++
				continue
			}
			if src[j] == '\n' {
				break
			}
			sb.WriteByte(src[j])
		}
		idx++
		out[idx] = goStringLiteral{line: itoa(line), text: sb.String()}
		i = j
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The contract is that a declaration's byte range is exact: take every
// declaration out, put them back in order, and the bytes are unchanged.
//
// The fixture below is not arbitrary. Each construct in it is one that broke a
// text-based version of this job: a doc comment that must travel with its
// function, a grouped var block whose body must not be separated from its
// header, a signature containing interface{} whose brace is NOT the body brace,
// a generic function whose type parameters contain brackets, and a string
// literal holding braces and an import-looking line.

const fixture = `package sample

import (
	"fmt"
	"strings"
)

// Doc comment that must travel with the function below it.
func withDoc(s string) string { return strings.TrimSpace(s) }

var (
	grouped = 1
	pair    = 2
)

// A signature whose FIRST brace is inside the return type, not the body. An
// earlier extractor counted from that brace and cut the function in half.
func returnsInterface() []map[string]interface{} {
	return nil
}

func generic[T any](values []T) int { return len(values) }

const marker = "a string with { braces } and\n\t\"looks/like/an/import\"\n"

type shape struct {
	Name string
}

func (s shape) String() string { return fmt.Sprintf("%s{}", s.Name) }
`

func parseFixture(t *testing.T, src string) []decl {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return declarations(fset, file, []byte(src))
}

// THE PROPERTY THE TOOL RESTS ON. Reassembly must be byte-identical, or a move
// silently changes code.
func TestReassemblyIsByteIdentical(t *testing.T) {
	src := fixture
	decls := parseFixture(t, src)
	if len(decls) == 0 {
		t.Fatal("no declarations parsed")
	}

	var rebuilt bytes.Buffer
	rebuilt.WriteString(src[:decls[0].start])
	for i, d := range decls {
		rebuilt.WriteString(src[d.start:d.end])
		if i+1 < len(decls) {
			rebuilt.WriteString(src[d.end:decls[i+1].start])
		}
	}
	rebuilt.WriteString(src[decls[len(decls)-1].end:])

	if rebuilt.String() != src {
		t.Errorf("reassembly differs from the source:\n--- got ---\n%s\n--- want ---\n%s",
			rebuilt.String(), src)
	}
}

// A doc comment belongs to its declaration. Leaving it behind orphans the
// comment above whatever declaration happens to follow, which reads as
// documentation for the wrong thing.
func TestDocCommentsTravelWithTheirDeclaration(t *testing.T) {
	src := fixture
	for _, d := range parseFixture(t, src) {
		if d.name != "withDoc" {
			continue
		}
		text := src[d.start:d.end]
		if !strings.HasPrefix(text, "// Doc comment") {
			t.Errorf("the doc comment did not travel with withDoc; range begins:\n%.60s", text)
		}
		return
	}
	t.Fatal("withDoc was not found among the declarations")
}

// The brace that opens a body is not simply the first brace after the name.
// interface{} in a return type comes first, and counting from it truncates the
// function.
func TestASignatureContainingInterfaceIsNotTruncated(t *testing.T) {
	src := fixture
	for _, d := range parseFixture(t, src) {
		if d.name != "returnsInterface" {
			continue
		}
		text := src[d.start:d.end]
		if strings.Count(text, "{") != strings.Count(text, "}") {
			t.Errorf("unbalanced braces in the extracted range:\n%s", text)
		}
		if !strings.Contains(text, "return nil") {
			t.Errorf("the body was cut off:\n%s", text)
		}
		return
	}
	t.Fatal("returnsInterface was not found")
}

// A grouped var block moves as ONE declaration. Splitting it would reorder
// package-level initialisation, which is the single thing a pure move must not
// do.
func TestAGroupedVarBlockIsOneDeclaration(t *testing.T) {
	src := fixture
	for _, d := range parseFixture(t, src) {
		if d.name != "grouped" {
			continue
		}
		text := src[d.start:d.end]
		if !strings.Contains(text, "pair") {
			t.Errorf("the group was split; the range holds only:\n%s", text)
		}
		return
	}
	t.Fatal("the grouped var block was not found")
}

// A generic function's brackets must not be mistaken for anything else.
func TestGenericDeclarationsAreFound(t *testing.T) {
	found := false
	for _, d := range parseFixture(t, fixture) {
		if d.name == "generic" {
			found = true
		}
	}
	if !found {
		t.Error("a generic function was not recognised as a declaration")
	}
}

// A method is named for the method, not the receiver, so it can be selected by
// name like any other declaration.
func TestMethodsAreNamedForTheMethod(t *testing.T) {
	for _, d := range parseFixture(t, fixture) {
		if d.name == "String" {
			return
		}
	}
	t.Error("the method String was not found among the declarations")
}

// Imports are never treated as movable declarations: they are rebuilt for the
// destination and pruned by the compiler, not carried by name.
func TestImportsAreNotDeclarations(t *testing.T) {
	for _, d := range parseFixture(t, fixture) {
		if d.name == "fmt" || d.name == "strings" {
			t.Errorf("an import was reported as a movable declaration: %q", d.name)
		}
	}
}

// A string literal containing braces and an import-shaped line must not disturb
// the ranges around it — this is the construct that defeated the text approach.
func TestStringLiteralsDoNotDisturbTheRanges(t *testing.T) {
	src := fixture
	for _, d := range parseFixture(t, src) {
		if d.name != "marker" {
			continue
		}
		text := src[d.start:d.end]
		if !strings.Contains(text, "looks/like/an/import") {
			t.Errorf("the literal was truncated:\n%s", text)
		}
		return
	}
	t.Fatal("the const marker was not found")
}

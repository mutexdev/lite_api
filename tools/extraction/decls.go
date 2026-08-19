// decls — list, delete and move top-level Go declarations by NAME.
//
// Built for the app.go decomposition. It replaces the regex approach for one
// specific reason: a regex cannot tell a Go token from the same characters
// inside a string literal. app_test.go has 174 column-0 `}` and 62 column-0
// func/type/const/var lines sitting inside backtick-quoted JavaScript
// fixtures, so any line-walking splitter truncates a test mid-literal and
// leaves a file that still compiles. go/parser cannot make that mistake.
//
// Two rules carried over from the earlier extraction, both learned the hard
// way:
//
//   - Take NAMES, not line numbers. Line numbers shift the moment anything
//     above them moves, and the same off-by-one bug recurred three times.
//   - Refuse a non-contiguous run. If the named declarations are interleaved
//     with unnamed ones, moving them silently reorders the file.
//
// Doc comments travel with their declaration. That is the whole reason this is
// AST-based rather than line-based: an orphaned comment does not fail to
// compile, it silently re-attaches to whatever declaration now follows it and
// describes the wrong function.
//
// Usage:
//
//	go run ./tools/extraction/decls.go list   -file app.go
//	go run ./tools/extraction/decls.go delete -file app.go -names randomHex,quoteDigestValue
//	go run ./tools/extraction/decls.go move   -file app.go -to app_yaml.go -names A,B,C
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: decls <list|delete|move> -file F [-to F] [-names A,B,C]")
	}
	command := os.Args[1]

	flags := flag.NewFlagSet(command, flag.ExitOnError)
	file := flags.String("file", "", "source file to read declarations from")
	to := flags.String("to", "", "destination file (move only)")
	names := flags.String("names", "", "comma-separated declaration names")
	contiguous := flags.Bool("contiguous", true, "refuse unless the named run is contiguous")
	if err := flags.Parse(os.Args[2:]); err != nil {
		fail("%v", err)
	}
	if *file == "" {
		fail("-file is required")
	}

	source, err := os.ReadFile(*file)
	if err != nil {
		fail("read %s: %v", *file, err)
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, *file, source, parser.ParseComments)
	if err != nil {
		fail("parse %s: %v", *file, err)
	}

	found := declSpans(fset, parsed, source)

	if command == "list" {
		for _, d := range found {
			fmt.Printf("%s\t%d\t%d\n", d.name, d.line, d.endLine)
		}
		return
	}

	wanted := strings.Split(*names, ",")
	for i := range wanted {
		wanted[i] = strings.TrimSpace(wanted[i])
	}
	selected, err := selectDecls(found, wanted)
	if err != nil {
		fail("%v", err)
	}

	switch command {
	case "delete":
		out := cut(source, selected)
		writeFormatted(*file, out)
		fmt.Fprintf(os.Stderr, "deleted %d declarations from %s\n", len(selected), *file)

	case "move":
		if *to == "" {
			fail("-to is required for move")
		}
		if *contiguous {
			if err := requireContiguous(found, selected); err != nil {
				fail("%v", err)
			}
		}
		moved := extract(source, selected)
		out := cut(source, selected)
		writeFormatted(*file, out)
		appendTo(*to, parsed.Name.Name, moved)
		fmt.Fprintf(os.Stderr, "moved %d declarations %s -> %s\n", len(selected), *file, *to)

	default:
		fail("unknown command %q", command)
	}
}

type span struct {
	name          string
	start, end    int // byte offsets, start includes the doc comment
	line, endLine int
	index         int // position among all top-level declarations
}

// declSpans returns one span per top-level declaration. A grouped
// `var (...)` / `const (...)` block is one span named after its first spec —
// splitting a group is not something this tool does.
func declSpans(fset *token.FileSet, parsed *ast.File, source []byte) []span {
	var spans []span
	for i, decl := range parsed.Decls {
		start := decl.Pos()
		var doc *ast.CommentGroup
		var name string

		switch d := decl.(type) {
		case *ast.FuncDecl:
			doc = d.Doc
			name = d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = receiverName(d.Recv.List[0].Type) + "." + name
			}
		case *ast.GenDecl:
			doc = d.Doc
			name = genDeclName(d)
		default:
			continue
		}
		if doc != nil {
			start = doc.Pos()
		}

		startOffset := fset.Position(start).Offset
		endOffset := fset.Position(decl.End()).Offset
		// Take the rest of the line, so a trailing `// comment` and the
		// newline leave with the declaration rather than stranding a blank.
		for endOffset < len(source) && source[endOffset] != '\n' {
			endOffset++
		}
		if endOffset < len(source) {
			endOffset++
		}

		spans = append(spans, span{
			name:    name,
			start:   startOffset,
			end:     endOffset,
			line:    fset.Position(start).Line,
			endLine: fset.Position(decl.End()).Line,
			index:   i,
		})
	}
	return spans
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	}
	return "?"
}

func genDeclName(d *ast.GenDecl) string {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			return s.Name.Name
		case *ast.ValueSpec:
			if len(s.Names) > 0 {
				return s.Names[0].Name
			}
		case *ast.ImportSpec:
			return "import"
		}
	}
	return "?"
}

func selectDecls(all []span, wanted []string) ([]span, error) {
	byName := map[string][]span{}
	for _, d := range all {
		byName[d.name] = append(byName[d.name], d)
	}
	var out []span
	var missing, ambiguous []string
	for _, name := range wanted {
		if name == "" {
			continue
		}
		matches := byName[name]
		switch len(matches) {
		case 0:
			missing = append(missing, name)
		case 1:
			out = append(out, matches[0])
		default:
			ambiguous = append(ambiguous, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("not found: %s", strings.Join(missing, ", "))
	}
	if len(ambiguous) > 0 {
		return nil, fmt.Errorf("declared more than once, refusing to guess: %s", strings.Join(ambiguous, ", "))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out, nil
}

// requireContiguous refuses a selection with unselected declarations in the
// middle of it. Moving such a run reorders the file relative to the source,
// which turns a reviewable pure-motion diff into something nobody can check.
func requireContiguous(all, selected []span) error {
	if len(selected) < 2 {
		return nil
	}
	chosen := map[int]bool{}
	for _, d := range selected {
		chosen[d.index] = true
	}
	first, last := selected[0].index, selected[len(selected)-1].index
	var gaps []string
	for _, d := range all {
		if d.index > first && d.index < last && !chosen[d.index] {
			gaps = append(gaps, fmt.Sprintf("%s (line %d)", d.name, d.line))
		}
	}
	if len(gaps) > 0 {
		return fmt.Errorf("run is not contiguous; these sit inside it but were not named:\n  %s",
			strings.Join(gaps, "\n  "))
	}
	return nil
}

func extract(source []byte, selected []span) []byte {
	var out bytes.Buffer
	for _, d := range selected {
		out.Write(source[d.start:d.end])
		if !bytes.HasSuffix(out.Bytes(), []byte("\n\n")) {
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}

func cut(source []byte, selected []span) []byte {
	var out bytes.Buffer
	prev := 0
	for _, d := range selected {
		out.Write(source[prev:d.start])
		prev = d.end
	}
	out.Write(source[prev:])
	return out.Bytes()
}

func writeFormatted(path string, content []byte) {
	formatted, err := format.Source(content)
	if err != nil {
		// Write it anyway so the damage is inspectable rather than invisible,
		// but say so loudly — an unformatted write means the edit was wrong.
		_ = os.WriteFile(path, content, 0o644)
		fail("result does not parse (written anyway for inspection): %v", err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
}

func appendTo(path, pkg string, content []byte) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		header := fmt.Sprintf("package %s\n\n", pkg)
		writeFormatted(path, append([]byte(header), content...))
		return
	}
	if err != nil {
		fail("read %s: %v", path, err)
	}
	writeFormatted(path, append(append(existing, '\n'), content...))
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

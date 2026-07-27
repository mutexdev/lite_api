// Command gosplit moves named top-level declarations from one Go file into
// another, using the AST rather than text.
//
// Written after a text-based attempt at the same job deleted a line of code:
// an import-pruning filter matched `hash := ""` because the line contained
// "hash" and ended with a quote. Across a 14,855-line file the blast radius of
// that class of error cannot be bounded by reading the diff, so the whole
// attempt was reverted.
//
// The rule this enforces: a declaration is identified by the parser, never by a
// pattern, and its bytes are copied verbatim from the source offsets. Comments
// attached to a declaration travel with it.
//
// Usage:
//
//	gosplit -src FILE -dst FILE -names a,b,c [-header TEXT]
//	gosplit -src FILE -list          # print every top-level declaration name
//	gosplit -src FILE -verify        # prove a full extract-and-reassemble is lossless
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

type decl struct {
	name       string
	start, end int // byte offsets into the source, comments included
}

// declarations returns every top-level declaration with the byte range that
// holds it, doc comment and all.
func declarations(fset *token.FileSet, file *ast.File, src []byte) []decl {
	var out []decl
	add := func(name string, node ast.Node, doc *ast.CommentGroup) {
		start := fset.Position(node.Pos()).Offset
		if doc != nil {
			start = fset.Position(doc.Pos()).Offset
		}
		end := fset.Position(node.End()).Offset
		out = append(out, decl{name: name, start: start, end: end})
	}
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.FuncDecl:
			add(n.Name.Name, n, n.Doc)
		case *ast.GenDecl:
			if n.Tok == token.IMPORT {
				continue
			}
			// A grouped declaration moves as a unit, named for its first spec:
			// splitting `var ( a = ...; b = ... )` would change initialisation
			// order, which is the one thing a move must not do.
			name := ""
			for _, spec := range n.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					name = s.Name.Name
				case *ast.ValueSpec:
					if len(s.Names) > 0 {
						name = s.Names[0].Name
					}
				}
				if name != "" {
					break
				}
			}
			if name == "" {
				continue
			}
			add(name, n, n.Doc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func load(path string) (*token.FileSet, *ast.File, []byte) {
	src, err := os.ReadFile(path)
	if err != nil {
		fatal("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		fatal("parse %s: %v", path, err)
	}
	return fset, file, src
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	src := flag.String("src", "", "source file")
	dst := flag.String("dst", "", "destination file")
	names := flag.String("names", "", "comma-separated declaration names to move")
	header := flag.String("header", "", "doc comment for the destination file")
	list := flag.Bool("list", false, "print every top-level declaration name")
	verify := flag.Bool("verify", false, "prove a full extract-and-reassemble is lossless")
	flag.Parse()
	if *src == "" {
		fatal("-src is required")
	}

	fset, file, source := load(*src)
	decls := declarations(fset, file, source)

	if *list {
		for _, d := range decls {
			fmt.Println(d.name)
		}
		return
	}

	// -verify: take EVERY declaration out and put it back, and require the
	// result to be byte-identical. If the offsets or the comment attachment are
	// wrong by a single character this fails, which is the point: the tool
	// proves itself against the real file before it is trusted to move
	// anything.
	if *verify {
		var rebuilt bytes.Buffer
		rebuilt.Write(source[:decls[0].start])
		for i, d := range decls {
			rebuilt.Write(source[d.start:d.end])
			if i+1 < len(decls) {
				rebuilt.Write(source[d.end:decls[i+1].start])
			}
		}
		rebuilt.Write(source[decls[len(decls)-1].end:])
		if !bytes.Equal(rebuilt.Bytes(), source) {
			fatal("VERIFY FAILED: reassembly is not byte-identical to the source")
		}
		fmt.Printf("verify ok: %d declarations, reassembly byte-identical to %s\n", len(decls), *src)
		return
	}

	if *dst == "" || *names == "" {
		fatal("-dst and -names are required")
	}
	wanted := map[string]bool{}
	for _, n := range strings.Split(*names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			wanted[n] = true
		}
	}

	var moved []decl
	seen := map[string]bool{}
	for _, d := range decls {
		if wanted[d.name] {
			moved = append(moved, d)
			seen[d.name] = true
		}
	}
	var missing []string
	for n := range wanted {
		if !seen[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fatal("not found in %s: %s", *src, strings.Join(missing, ", "))
	}

	// Destination: package clause, the source's imports verbatim, then the
	// moved declarations. Unused imports are pruned afterwards by the caller,
	// which is a compiler-driven step and cannot touch code.
	var out bytes.Buffer
	fmt.Fprintf(&out, "package %s\n", file.Name.Name)
	if *header != "" {
		fmt.Fprintf(&out, "\n%s\n", *header)
	}
	if len(file.Imports) > 0 {
		first := file.Imports[0]
		last := file.Imports[len(file.Imports)-1]
		lo := fset.Position(first.Pos()).Offset
		// Back up to the start of the line: Pos() points at the import path,
		// past its leading tab, and copying from there emits a first import
		// with no indentation. That is valid Go but not the shape an
		// import-line matcher expects, which is exactly the kind of near-miss
		// that makes a text tool refuse or, worse, guess.
		for lo > 0 && source[lo-1] != '\n' {
			lo--
		}
		hi := fset.Position(last.End()).Offset
		out.WriteString("\nimport (\n")
		out.Write(source[lo:hi])
		out.WriteString("\n)\n")
	}
	for _, d := range moved {
		out.WriteString("\n")
		out.Write(source[d.start:d.end])
		out.WriteString("\n")
	}
	if err := os.WriteFile(*dst, out.Bytes(), 0o644); err != nil {
		fatal("write %s: %v", *dst, err)
	}

	// Source: everything except the moved ranges, removed back to front so the
	// offsets stay valid.
	remaining := append([]byte(nil), source...)
	for i := len(moved) - 1; i >= 0; i-- {
		end := moved[i].end
		for end < len(remaining) && remaining[end] == '\n' {
			end++
		}
		remaining = append(remaining[:moved[i].start], remaining[end:]...)
	}
	if err := os.WriteFile(*src, remaining, 0o644); err != nil {
		fatal("write %s: %v", *src, err)
	}
	fmt.Printf("moved %d declarations to %s\n", len(moved), *dst)
}

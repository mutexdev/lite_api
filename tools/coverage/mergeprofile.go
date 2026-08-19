//go:build ignore

// mergeprofile collapses a Go coverage profile so each source block appears
// once, taking the highest count any test binary recorded for it.
//
// WHY THIS IS NEEDED, and it is not obvious. With `-coverpkg` over a multi-
// package build, EVERY test binary instruments EVERY listed package and writes
// a full profile. `go test -coverprofile` concatenates them, so one source
// block appears once per binary — in this repo, 10,485 unique blocks became
// 283,457 lines across 28 binaries.
//
// `go tool cover -func` treats each occurrence as a separate block. A block
// exercised by one binary and not the other 27 therefore reads as 1 covered out
// of 28, not 1 out of 1. The reported percentage then FALLS as test binaries
// are added, which is exactly backwards, and it is why this repo has twice
// recorded coverage numbers that moved while only tests were added.
//
// Usage:
//
//	go test -count=1 -coverpkg=./... -coverprofile=raw.out ./...
//	go run tools/coverage/mergeprofile.go raw.out > merged.out
//	go tool cover -func=merged.out | tail -1
//
// -count=1 matters for a second, independent reason: a CACHED package result
// contributes no profile data at all, so a cached run silently under-reports.
package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mergeprofile <profile>")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()

	var mode string
	// Keyed by "file:startLine.startCol,endLine.endCol numStmt", which is the
	// whole line minus the count — two blocks with the same span but different
	// statement counts are different blocks and must not be merged.
	best := map[string]int{}
	order := []string{}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			if mode == "" {
				mode = line
			}
			continue
		}
		cut := strings.LastIndexByte(line, ' ')
		if cut < 0 {
			continue
		}
		key, countText := line[:cut], line[cut+1:]
		count, err := strconv.Atoi(countText)
		if err != nil {
			continue
		}
		if previous, seen := best[key]; !seen {
			best[key] = count
			order = append(order, key)
		} else if count > previous {
			best[key] = count
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Sorted so the output is byte-identical across runs, which lets a CI job
	// diff two profiles rather than only compare their totals.
	sort.Strings(order)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	if mode == "" {
		mode = "mode: set"
	}
	fmt.Fprintln(out, mode)
	for _, key := range order {
		fmt.Fprintf(out, "%s %d\n", key, best[key])
	}
}

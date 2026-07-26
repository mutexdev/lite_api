package main

// US-046 — runner data files.
//
// A CSV or JSON file drives a run: one iteration per row, with the row's
// columns available to {{var}} interpolation. This is the data half of the
// story; US-043 exposes the same rows to scripts as pm.iterationData.
//
// The rule that matters, and the reason this file exists rather than a dozen
// lines inline: a data-driven run must never execute an iteration with no row
// behind it. An iteration without data does not fail — it sends requests with
// {{userId}} unresolved, straight to the server, looking like a bug in the
// user's collection rather than in the run configuration. Every clamp below
// exists to make that unrepresentable.

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// runnerDataFileLimit bounds what a data file may be. A run holds every row in
// memory at once and stamps rows into per-iteration state, so an accidentally
// selected multi-gigabyte export must be refused with a message rather than
// discovered as an OOM.
const runnerDataFileLimit = 32 << 20 // 32 MiB

// runnerDataRowLimit bounds the row count for the same reason the iteration
// count is capped: every row becomes an iteration, and every iteration's
// results are persisted into state.json.
const runnerDataRowLimit = runnerIterationLimit

// runnerDataRows reads path and returns one map per iteration.
//
// Format is chosen by extension, not by sniffing content. Sniffing would let a
// mislabelled file parse as the wrong format and produce plausible-looking
// garbage — a JSON array read as CSV yields one column named `[` — whereas an
// unknown extension is an error the user can act on.
func runnerDataRows(path string) ([]map[string]string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, nil
	}

	info, err := os.Stat(trimmed)
	if err != nil {
		return nil, fmt.Errorf("data file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("data file: %s is a directory", trimmed)
	}
	if info.Size() > runnerDataFileLimit {
		return nil, fmt.Errorf("data file is %d bytes, over the %d byte limit", info.Size(), runnerDataFileLimit)
	}

	file, err := os.Open(trimmed) // #nosec G304 -- the path is chosen by the user in a file picker
	if err != nil {
		return nil, fmt.Errorf("data file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// LimitReader on top of the Stat check: the file can grow between the two,
	// and the size check alone would then be advisory rather than a bound.
	reader := io.LimitReader(file, runnerDataFileLimit+1)

	var rows []map[string]string
	switch strings.ToLower(filepath.Ext(trimmed)) {
	case ".csv":
		rows, err = parseRunnerDataCSV(reader)
	case ".json":
		rows, err = parseRunnerDataJSON(reader)
	default:
		return nil, fmt.Errorf("data file: unsupported extension %q, expected .csv or .json", filepath.Ext(trimmed))
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("data file contains no rows")
	}
	if len(rows) > runnerDataRowLimit {
		return nil, fmt.Errorf("data file has %d rows, over the %d row limit", len(rows), runnerDataRowLimit)
	}
	return rows, nil
}

func parseRunnerDataCSV(reader io.Reader) ([]map[string]string, error) {
	parser := csv.NewReader(reader)
	// Ragged rows are accepted deliberately: a trailing comma or a short final
	// line is common in hand-edited CSV, and refusing the whole file over it
	// helps nobody. Missing cells become empty strings, which is what the
	// header promised the column would be.
	parser.FieldsPerRecord = -1

	header, err := parser.Read()
	if err == io.EOF {
		return nil, errors.New("data file: the CSV has no header row")
	}
	if err != nil {
		return nil, fmt.Errorf("data file: %w", err)
	}
	for i, name := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))
	}

	var rows []map[string]string
	for {
		record, err := parser.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("data file: %w", err)
		}
		if isBlankCSVRecord(record) {
			continue
		}
		row := make(map[string]string, len(header))
		for i, name := range header {
			if name == "" {
				continue
			}
			if i < len(record) {
				row[name] = record[i]
			} else {
				row[name] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func isBlankCSVRecord(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

func parseRunnerDataJSON(reader io.Reader) ([]map[string]string, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("data file: %w", err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("data file: expected a JSON array of objects: %w", err)
	}
	rows := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		row := make(map[string]string, len(entry))
		for key, value := range entry {
			row[key] = runnerDataValueString(value)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// runnerDataValueString flattens a JSON value into what {{var}} substitutes.
//
// Numbers go through strconv rather than fmt's %v so that 1 stays "1" instead
// of becoming "1e+06" at scale — an id silently rewritten in scientific
// notation is a request sent to the wrong resource.
func runnerDataValueString(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		// Objects and arrays keep their JSON form, so a nested value is at
		// least usable as a body fragment rather than Go's map syntax.
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}

// runnerIterationPlan decides how many iterations run and which row backs each.
//
// The clamp is the point. With a data file the row count leads, because an
// iteration with no row sends {{userId}} unresolved to the server — a silent
// wrong request rather than an error. Asking for more iterations than there
// are rows is therefore clamped down, not padded.
func runnerIterationPlan(rows []map[string]string, requested int) int {
	if len(rows) == 0 {
		return normalizeRunnerIterations(requested)
	}
	if requested < 1 {
		return len(rows)
	}
	return min(normalizeRunnerIterations(requested), len(rows))
}

// runnerDataRowFor returns the row backing a 1-based iteration, or nil.
func runnerDataRowFor(rows []map[string]string, iteration int) map[string]string {
	if iteration < 1 || iteration > len(rows) {
		return nil
	}
	return rows[iteration-1]
}

// runnerIteration is where a request sits in a collection run.
//
// Index is 1-based and Count is 0 for a one-off send outside the runner, which
// is what lets pm.info distinguish "not in a run" from "iteration 1 of 1".
type runnerIteration struct {
	Index int
	Count int
	Data  map[string]string
}

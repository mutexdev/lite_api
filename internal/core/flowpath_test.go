package core

// The flow path evaluator, as a table.
//
// It is a table because the subset is defined by what it REFUSES as much as by
// what it resolves, and the refusals are the half that rots silently: nothing
// breaks if `$.items[*].id` starts quietly returning the first id, it just
// starts lying to whichever flow used it.

import (
	"strings"
	"testing"
)

const flowPathDocument = `{
  "data": {
    "store": {"id": "store_42", "region": "apac", "active": true, "score": 4.5},
    "stores": [
      {"id": "store_1", "tags": ["a", "b"]},
      {"id": "store_2"}
    ]
  },
  "key with spaces": "spaced",
  "count": 9007199254740993,
  "nothing": null,
  "filter": {"kind": "range", "from": 1}
}`

func TestFlowPathResolvesTheSupportedSubset(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"a nested key", "$.data.store.id", "store_42"},
		{"a string passes through unquoted", "$.data.store.region", "apac"},
		{"a boolean renders as JSON", "$.data.store.active", "true"},
		{"a fractional number renders as JSON", "$.data.store.score", "4.5"},
		{"a list index", "$.data.stores[0].id", "store_1"},
		{"a second list index", "$.data.stores[1].id", "store_2"},
		{"an index into a list of scalars", "$.data.stores[0].tags[1]", "b"},
		{"a quoted key with spaces", `$["key with spaces"]`, "spaced"},
		{"a single-quoted key", `$['key with spaces']`, "spaced"},
		// The whole reason the decoder runs with UseNumber: as a float64 this
		// comes back 9007199254740992, and the flow would carry an id one off
		// from the one the server sent.
		{"a large integer keeps every digit", "$.count", "9007199254740993"},
		{"an explicit null", "$.nothing", "null"},
		{"an object renders as compact JSON", "$.filter", `{"from":1,"kind":"range"}`},
		{"a list renders as compact JSON", "$.data.stores[0].tags", `["a","b"]`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := flowPathValue(flowPathDocument, testCase.path)
			if err != nil {
				t.Fatalf("flowPathValue(%q): %v", testCase.path, err)
			}
			if got != testCase.want {
				t.Errorf("flowPathValue(%q) = %q, want %q", testCase.path, got, testCase.want)
			}
		})
	}
}

func TestFlowPathRefusesEverythingOutsideTheSubset(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		contains string
	}{
		{"a wildcard", "$.data.stores[*].id", "unsupported subscript"},
		{"a dotted wildcard", "$.data.*", "wildcard"},
		{"recursive descent", "$..id", "recursive descent"},
		{"a filter expression", "$.data.stores[?(@.id)]", "unsupported subscript"},
		{"a slice", "$.data.stores[0:1]", "unsupported subscript"},
		{"no root", "data.store.id", "must start with $"},
		{"the bare root", "$", "selects the whole document"},
		{"an empty path", "   ", "a JSONPath is required"},
		{"an empty key", "$..", "recursive descent"},
		{"an unclosed subscript", "$.data[0", "unclosed"},
		{"a negative index", "$.data.stores[-1]", "unsupported subscript"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := parseFlowPath(testCase.path); err == nil {
				t.Fatalf("parseFlowPath(%q) was accepted", testCase.path)
			} else if !strings.Contains(err.Error(), testCase.contains) {
				t.Errorf("parseFlowPath(%q) = %v, want it to mention %q", testCase.path, err, testCase.contains)
			}
		})
	}
}

// A miss has to name the path AND say where it stopped, because "no such path"
// leaves the author guessing which of four keys was the wrong one.
func TestFlowPathMissesNameThePathAndWhereTheyStopped(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		contains []string
	}{
		{"a missing key", "$.data.store.missing", []string{"$.data.store.missing", `no key "missing" under $.data.store`}},
		{"a key under a scalar", "$.data.store.id.deeper", []string{"$.data.store.id.deeper", "$.data.store.id is not an object"}},
		{"an index past the end", "$.data.stores[9].id", []string{"$.data.stores[9].id", "$.data.stores holds 2 entries"}},
		{"an index into an object", "$.data.store[0]", []string{"$.data.store is not a list"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := flowPathValue(flowPathDocument, testCase.path)
			if err == nil {
				t.Fatalf("flowPathValue(%q) found something", testCase.path)
			}
			for _, want := range testCase.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestFlowPathSaysWhenTheBodyIsNotJSON(t *testing.T) {
	_, err := flowPathValue("<html>not json</html>", "$.data.id")
	if err == nil {
		t.Fatal("a non-JSON body resolved a path")
	}
	if !strings.Contains(err.Error(), "not JSON") || !strings.Contains(err.Error(), "$.data.id") {
		t.Errorf("error = %v, want it to name both the problem and the path", err)
	}
}

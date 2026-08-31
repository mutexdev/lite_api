package core

// The JSONPath subset flows use for extraction and body assertions.
//
// A DELIBERATELY SMALL LANGUAGE. Exactly three forms are accepted:
//
//	$.a.b              a named key, chained
//	$.a[0].b           a list index
//	$["key with spaces"]  a quoted key, for names dots cannot express
//
// and nothing else. No wildcards, no recursive descent, no filter expressions,
// no slices. That is not a placeholder for a fuller implementation: the whole
// point of a flow path is to name ONE value to carry into the next step, and
// every excluded form either names zero values or many. `$.items[*].id` has no
// single answer, so there is nothing to put in a flow variable, and accepting it
// would mean inventing a rule ("the first one") that the author never wrote.
// Rejecting it at parse time, with the offending path quoted, is the honest
// answer and keeps this file free of a dependency.
//
// AN UNRESOLVED PATH IS AN ERROR, NOT AN EMPTY STRING. A flow that silently
// carried "" from a lookup into the body of the next request would send a
// wrong request to a real API and report success. Every miss names the path and
// the step that asked for it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// flowPathSegment is one step of a parsed path: a key or a list index.
type flowPathSegment struct {
	key   string
	index int
	isKey bool
}

// parseFlowPath turns `$.a[0].b` into its segments, or explains why it cannot.
func parseFlowPath(path string) ([]flowPathSegment, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("a JSONPath is required, for example $.data.id")
	}
	if trimmed[0] != '$' {
		return nil, fmt.Errorf("path %q must start with $, for example $.data.id", path)
	}
	if strings.HasPrefix(trimmed, "$..") {
		return nil, fmt.Errorf("path %q uses recursive descent (..), which flow paths do not support; name the full path, for example $.data.store.id", path)
	}
	segments := []flowPathSegment{}
	rest := trimmed[1:]
	for len(rest) > 0 {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			name := rest
			if end >= 0 {
				name = rest[:end]
				rest = rest[end:]
			} else {
				rest = ""
			}
			if name == "" {
				return nil, fmt.Errorf("path %q has an empty key after a dot", path)
			}
			if name == "*" {
				return nil, fmt.Errorf("path %q uses a wildcard, which flow paths do not support; a flow variable holds one value, so name it exactly", path)
			}
			segments = append(segments, flowPathSegment{key: name, isKey: true})
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q has an unclosed [", path)
			}
			inner := strings.TrimSpace(rest[1:end])
			rest = rest[end+1:]
			if inner == "" {
				return nil, fmt.Errorf("path %q has an empty []", path)
			}
			if quoted, ok := unquoteFlowPathKey(inner); ok {
				segments = append(segments, flowPathSegment{key: quoted, isKey: true})
				continue
			}
			index, err := strconv.Atoi(inner)
			if err != nil || index < 0 {
				return nil, fmt.Errorf("path %q has an unsupported subscript [%s]; flow paths accept a non-negative index or a quoted key", path, inner)
			}
			segments = append(segments, flowPathSegment{index: index})
		default:
			return nil, fmt.Errorf("path %q is not a supported JSONPath; use $.a.b, $.a[0].b or $[\"key with spaces\"]", path)
		}
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("path %q selects the whole document; name a value inside it, for example $.data.id", path)
	}
	return segments, nil
}

// unquoteFlowPathKey reads a `"key"` or `'key'` subscript.
func unquoteFlowPathKey(inner string) (string, bool) {
	if len(inner) < 2 {
		return "", false
	}
	quote := inner[0]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	if inner[len(inner)-1] != quote {
		return "", false
	}
	return inner[1 : len(inner)-1], true
}

// flowPathValue resolves path against a JSON document and renders the result.
//
// NUMBERS ARE DECODED AS json.Number, not float64: an id of
// 9007199254740993 is a perfectly ordinary thing for an API to return, and
// float64 would hand the next step a different number than the one the server
// sent. Rendering a json.Number is its own source text, so the value that
// travels through the flow is byte-for-byte what arrived.
func flowPathValue(body string, path string) (string, error) {
	segments, err := parseFlowPath(path)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var document interface{}
	if err := decoder.Decode(&document); err != nil {
		return "", fmt.Errorf("the response body is not JSON, so %s cannot be read from it: %v", path, err)
	}
	current := document
	for depth, segment := range segments {
		if segment.isKey {
			object, ok := current.(map[string]interface{})
			if !ok {
				return "", fmt.Errorf("%s is not present in the response body: %s is not an object", path, flowPathPrefix(segments, depth))
			}
			value, ok := object[segment.key]
			if !ok {
				return "", fmt.Errorf("%s is not present in the response body: no key %q under %s", path, segment.key, flowPathPrefix(segments, depth))
			}
			current = value
			continue
		}
		list, ok := current.([]interface{})
		if !ok {
			return "", fmt.Errorf("%s is not present in the response body: %s is not a list", path, flowPathPrefix(segments, depth))
		}
		if segment.index >= len(list) {
			return "", fmt.Errorf("%s is not present in the response body: %s holds %d entries", path, flowPathPrefix(segments, depth), len(list))
		}
		current = list[segment.index]
	}
	return flowRenderValue(current)
}

// flowPathPrefix renders the part of a path that DID resolve, so a miss says
// where it stopped rather than only that it stopped.
func flowPathPrefix(segments []flowPathSegment, depth int) string {
	out := "$"
	for i := 0; i < depth; i++ {
		if segments[i].isKey {
			out += "." + segments[i].key
			continue
		}
		out += "[" + strconv.Itoa(segments[i].index) + "]"
	}
	return out
}

// flowRenderValue turns a decoded JSON value into the string a flow variable
// carries.
//
// Strings pass through unquoted — a token extracted from a body is meant to be
// pasted into the next request's header, not to arrive wrapped in quotes.
// Numbers and booleans render as JSON. An object or a list renders as its
// COMPACT JSON, which is what makes `$.filter` usable as a whole sub-document to
// post onward.
func flowRenderValue(value interface{}) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	}
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimRight(buffer.String(), "\n"), nil
}

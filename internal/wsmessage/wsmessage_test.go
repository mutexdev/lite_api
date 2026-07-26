package wsmessage

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// The default is "text", not an error and not "". This runs while READING A
// FILE, so an unrecognised value means a collection written by a newer build or
// edited by hand — and refusing it would cost the user every other request in
// the file.
func TestAnUnknownTypeReadsAsTextRatherThanFailing(t *testing.T) {
	for _, input := range []string{"protobuf", "msgpack", "", "   ", "???"} {
		if got := NormalizeMessageType(input); got != "text" {
			t.Errorf("NormalizeMessageType(%q) = %q, want text", input, got)
		}
	}
}

func TestKnownTypesAndTheirAliases(t *testing.T) {
	for input, want := range map[string]string{
		"json": "json", "JSON": "json", "  json  ": "json",
		"xml": "xml", "XML": "xml",
		"binary": "binary", "bin": "binary", "BIN": "binary",
		"text": "text",
	} {
		if got := NormalizeMessageType(input); got != want {
			t.Errorf("NormalizeMessageType(%q) = %q, want %q", input, got, want)
		}
	}
}

// "bin" and "binary" must land on the same mode. A reader that passes one
// through unchanged produces a message the executor frames as text, so the
// bytes are re-encoded on the way out.
func TestBinAndBinaryAgree(t *testing.T) {
	if NormalizeMessageType("bin") != NormalizeMessageType("binary") {
		t.Error("bin and binary normalize differently")
	}
}

// Every branch interpolates, including the default. A message going out with a
// literal {{token}} in it is worse than one whose mode was guessed: the server
// sees a malformed payload rather than an unexpected content type.
func TestEveryModeInterpolatesIncludingAnUnknownOne(t *testing.T) {
	vars := map[string]string{"tok": "abc"}
	for _, mode := range []string{"json", "xml", "text", "sparql", "", "protobuf"} {
		body := types.RequestBody{Mode: mode, JSON: "{{tok}}", XML: "{{tok}}", Text: "{{tok}}"}
		if got := MessageBody(body, vars); got != "abc" {
			t.Errorf("mode %q: got %q, want abc", mode, got)
		}
	}
}

// The empty mode reads the TEXT field, for the same reason NormalizeMessageType
// defaults to text: a message saved before the field existed has one.
func TestTheEmptyModeReadsTheTextField(t *testing.T) {
	body := types.RequestBody{JSON: "from json", Text: "from text"}
	if got := MessageBody(body, nil); got != "from text" {
		t.Errorf("got %q, want %q", got, "from text")
	}
}

// Each mode reads its OWN field. Reading the wrong one sends a body the user
// never typed into that editor.
func TestEachModeReadsItsOwnField(t *testing.T) {
	body := types.RequestBody{JSON: "J", XML: "X", Text: "T"}
	for mode, want := range map[string]string{"json": "J", "xml": "X", "text": "T"} {
		body.Mode = mode
		if got := MessageBody(body, nil); got != want {
			t.Errorf("mode %q read %q, want %q", mode, got, want)
		}
	}
}

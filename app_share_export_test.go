package main

import (
	"testing"
)

// These build the Postman payload a "share" produces. Both were at 0%. What
// they emit leaves this app and is read by Postman, so a wrong shape is not
// caught by anything here — the user finds out when the import fails or, worse,
// succeeds with the wrong content.

func formPart(name, value, filePath string, enabled bool) FormPart {
	return FormPart{Name: name, Value: value, FilePath: filePath, Enabled: enabled}
}

func TestSharePostmanFormDataEmitsATextPart(t *testing.T) {
	out := sharePostmanFormData([]FormPart{formPart("field", "v", "", true)})
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1", len(out))
	}
	entry := out[0]
	if entry["key"] != "field" || entry["value"] != "v" || entry["type"] != "text" {
		t.Errorf("entry = %v", entry)
	}
	if _, present := entry["disabled"]; present {
		t.Error("an enabled part was marked disabled")
	}
	if _, present := entry["src"]; present {
		t.Error("a text part carries src")
	}
}

// A FILE part uses src and MUST NOT carry value. Postman's form-data schema
// treats the two as alternatives; leaving a stale value behind next to src is
// how an import silently attaches the wrong content.
func TestSharePostmanFormDataDropsValueOnAFilePart(t *testing.T) {
	out := sharePostmanFormData([]FormPart{formPart("upload", "leftover text", "/tmp/a.bin", true)})
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1", len(out))
	}
	entry := out[0]
	if entry["type"] != "file" {
		t.Errorf("type = %v, want file", entry["type"])
	}
	if entry["src"] != "/tmp/a.bin" {
		t.Errorf("src = %v", entry["src"])
	}
	if _, present := entry["value"]; present {
		t.Errorf("a file part still carries value = %v", entry["value"])
	}
}

// The file path decides the part's kind, and it is TRIMMED first: a path of
// spaces is not a file, and treating it as one emits src:"   ", which imports
// as a part pointing at nothing.
func TestSharePostmanFormDataTreatsABlankPathAsText(t *testing.T) {
	out := sharePostmanFormData([]FormPart{formPart("field", "v", "   ", true)})
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1", len(out))
	}
	if out[0]["type"] != "text" {
		t.Errorf("type = %v, want text", out[0]["type"])
	}
	if out[0]["value"] != "v" {
		t.Errorf("value = %v, want the text value kept", out[0]["value"])
	}
}

// A disabled part is EXPORTED AND MARKED, not dropped. Dropping it would lose
// the row the user deliberately kept but switched off — an export is a copy of
// the request, not of the enabled subset.
func TestSharePostmanFormDataMarksDisabledPartsRatherThanDroppingThem(t *testing.T) {
	out := sharePostmanFormData([]FormPart{
		formPart("on", "1", "", true),
		formPart("off", "2", "", false),
	})
	if len(out) != 2 {
		t.Fatalf("got %d entries, want both", len(out))
	}
	if _, present := out[0]["disabled"]; present {
		t.Error("the enabled part was marked disabled")
	}
	if out[1]["disabled"] != true {
		t.Errorf("the disabled part was not marked: %v", out[1])
	}
	if out[1]["key"] != "off" {
		t.Errorf("the disabled part lost its key: %v", out[1])
	}
}

// A part with no NAME is dropped, because Postman keys form-data by name and a
// keyless entry has nothing to import as. This is the one case where dropping
// is right, and it is distinct from the disabled case above.
func TestSharePostmanFormDataDropsNamelessParts(t *testing.T) {
	out := sharePostmanFormData([]FormPart{
		formPart("", "orphan", "", true),
		formPart("   ", "also orphan", "", true),
		formPart("kept", "v", "", true),
	})
	if len(out) != 1 {
		t.Fatalf("got %d entries, want only the named one: %v", len(out), out)
	}
	if out[0]["key"] != "kept" {
		t.Errorf("the wrong entry survived: %v", out[0])
	}
}

// An empty result must marshal as [] rather than null: the builder seeds an
// empty slice for exactly that reason, and a null formdata array is not what
// Postman's schema describes.
func TestSharePostmanFormDataReturnsAnEmptySliceNotNil(t *testing.T) {
	for name, input := range map[string][]FormPart{
		"nil input":         nil,
		"empty input":       {},
		"all parts dropped": {formPart("", "x", "", true)},
	} {
		out := sharePostmanFormData(input)
		if out == nil {
			t.Errorf("%s: returned nil, which marshals as null", name)
		}
		if len(out) != 0 {
			t.Errorf("%s: got %d entries, want none", name, len(out))
		}
	}
}

func TestShareSelectedFileBodyEntryPicksTheSelectedFile(t *testing.T) {
	body := RequestBody{Files: []FileBodyEntry{
		{FilePath: "/tmp/a", Selected: false},
		{FilePath: "/tmp/b", Selected: true},
		{FilePath: "/tmp/c", Selected: true},
	}}
	got := shareSelectedFileBodyEntry(body)
	if got == nil {
		t.Fatal("no entry was selected")
	}
	if got.FilePath != "/tmp/b" {
		t.Errorf("FilePath = %q, want the FIRST selected entry", got.FilePath)
	}
}

// A selected row whose path is blank is not a file to export. Returning it
// would put an entry with no source into the payload.
func TestShareSelectedFileBodyEntrySkipsASelectedRowWithNoPath(t *testing.T) {
	body := RequestBody{Files: []FileBodyEntry{
		{FilePath: "  ", Selected: true},
		{FilePath: "/tmp/real", Selected: true},
	}}
	got := shareSelectedFileBodyEntry(body)
	if got == nil {
		t.Fatal("the entry with a real path was skipped too")
	}
	if got.FilePath != "/tmp/real" {
		t.Errorf("FilePath = %q", got.FilePath)
	}
}

func TestShareSelectedFileBodyEntryReportsNothingWhenNoneApply(t *testing.T) {
	for name, body := range map[string]RequestBody{
		"no files":       {},
		"none selected":  {Files: []FileBodyEntry{{FilePath: "/tmp/a"}}},
		"selected blank": {Files: []FileBodyEntry{{FilePath: "", Selected: true}}},
	} {
		if got := shareSelectedFileBodyEntry(body); got != nil {
			t.Errorf("%s: got %+v, want nil", name, got)
		}
	}
}

// The returned pointer must address a COPY. It travels into an export payload,
// and handing out a pointer into the live body would let a later edit of that
// payload reach back into the request the user is still working on.
//
// NO MUTATION CAN MAKE THIS FAIL, and that is worth stating rather than
// leaving as an untested-looking gap. The implementation writes
// `copy := file; return &copy`, but this module is on Go 1.25, where a range
// loop gives each iteration its OWN variable — so `return &file` addresses a
// copy just as much, and the explicit one is redundant. Verified directly
// rather than assumed.
//
// The test is kept because it pins the PROPERTY the caller depends on, not the
// spelling that currently provides it: if the loop were ever rewritten to index
// the slice (`&body.Files[i]`), the aliasing would return and this would catch
// it. The redundant line is left alone; deleting it is a change to working
// code that no test asked for.
func TestShareSelectedFileBodyEntryReturnsACopy(t *testing.T) {
	body := RequestBody{Files: []FileBodyEntry{{FilePath: "/tmp/a", ContentType: "text/plain", Selected: true}}}
	got := shareSelectedFileBodyEntry(body)
	if got == nil {
		t.Fatal("nothing was selected")
	}
	got.FilePath = "/tmp/mutated"
	got.ContentType = "application/octet-stream"

	if body.Files[0].FilePath != "/tmp/a" {
		t.Errorf("editing the result changed the body: FilePath = %q", body.Files[0].FilePath)
	}
	if body.Files[0].ContentType != "text/plain" {
		t.Errorf("editing the result changed the body: ContentType = %q", body.Files[0].ContentType)
	}
}

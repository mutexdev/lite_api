package bru

// A disabled row is part of the request, not an absent one.
//
// The `~name: value` syntax is what this format uses to say "present but
// unticked", and the reader has always understood it. The HTTP writer did not
// produce it for headers, query params or path params: it wrote only the
// enabled rows, so unticking a header and saving deleted it — visibly still
// there until the file was read back, and then gone with no error anywhere.

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func requestWithDisabledRows() types.RequestItem {
	return types.RequestItem{
		Name:   "Disabled rows",
		Type:   "http",
		Method: "GET",
		URL:    "https://example.test/items/{{id}}",
		Seq:    1,
		Headers: []types.KeyValue{
			{Name: "X-Enabled", Value: "on", Enabled: true},
			{Name: "X-Disabled", Value: "off", Enabled: false},
		},
		Params: []types.KeyValue{
			{Name: "page", Value: "1", Enabled: true},
			{Name: "debug", Value: "true", Enabled: false},
		},
		PathParams: []types.KeyValue{
			{Name: "id", Value: "42", Enabled: true},
			{Name: "unused", Value: "0", Enabled: false},
		},
		Body: types.RequestBody{Mode: "none"},
		Auth: types.AuthConfig{Mode: "none"},
	}
}

func TestStringifyBruWritesDisabledHeadersAndParams(t *testing.T) {
	written := StringifyBru(requestWithDisabledRows())
	for _, want := range []string{
		"  ~X-Disabled: off\n",
		"  ~debug: true\n",
		"  ~unused: 0\n",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the .bru writer dropped a disabled row (%q missing).\n---\n%s", strings.TrimSpace(want), written)
		}
	}
	for _, want := range []string{
		"  X-Enabled: on\n",
		"  page: 1\n",
		"  id: 42\n",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the .bru writer dropped an enabled row (%q missing).\n---\n%s", strings.TrimSpace(want), written)
		}
	}
}

func TestBruRoundTripKeepsDisabledHeadersAndParams(t *testing.T) {
	parsed, err := Parse(StringifyBru(requestWithDisabledRows()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	assertRow := func(label string, rows []types.KeyValue, name string, enabled bool, value string) {
		t.Helper()
		for _, row := range rows {
			if row.Name != name {
				continue
			}
			if row.Enabled != enabled {
				t.Errorf("%s %q came back Enabled=%v, want %v", label, name, row.Enabled, enabled)
			}
			if row.Value != value {
				t.Errorf("%s %q came back %q, want %q", label, name, row.Value, value)
			}
			return
		}
		t.Errorf("%s %q was lost in the round trip", label, name)
	}
	assertRow("header", parsed.Headers, "X-Enabled", true, "on")
	assertRow("header", parsed.Headers, "X-Disabled", false, "off")
	assertRow("query param", parsed.Params, "page", true, "1")
	assertRow("query param", parsed.Params, "debug", false, "true")
	assertRow("path param", parsed.PathParams, "id", true, "42")
	assertRow("path param", parsed.PathParams, "unused", false, "0")
}

// The other row types were checked at the same time and already wrote `~`.
// Pinning them here says so, and would catch the same regression arriving in a
// block that is currently correct.
func TestStringifyBruWritesDisabledFormAndVariableRows(t *testing.T) {
	item := types.RequestItem{
		Name:   "Other blocks",
		Type:   "http",
		Method: "POST",
		URL:    "https://example.test/form",
		Seq:    1,
		Body: types.RequestBody{
			Mode: "formUrlEncoded",
			FormURLEncoded: []types.KeyValue{
				{Name: "kept", Value: "1", Enabled: true},
				{Name: "dropped", Value: "0", Enabled: false},
			},
		},
		Auth: types.AuthConfig{Mode: "none"},
		Vars: types.RequestVars{
			Req: []types.Variable{
				{Name: "live", Value: "yes", Enabled: true},
				{Name: "paused", Value: "no", Enabled: false},
			},
		},
	}
	written := StringifyBru(item)
	for _, want := range []string{"  ~dropped: 0\n", "  ~paused: no\n"} {
		if !strings.Contains(written, want) {
			t.Errorf("the .bru writer dropped a disabled row (%q missing).\n---\n%s", strings.TrimSpace(want), written)
		}
	}
}

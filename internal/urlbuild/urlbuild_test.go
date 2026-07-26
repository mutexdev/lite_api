package urlbuild

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func kv(name, value string, enabled bool) types.KeyValue {
	return types.KeyValue{Name: name, Value: value, Enabled: enabled}
}

// A token with no matching parameter is left in place rather than blanked. A
// URL that still shows :id is visibly wrong; one silently collapsed to "//"
// requests a DIFFERENT RESOURCE, and the server answers it.
func TestUnmatchedPathTokenIsLeftInPlace(t *testing.T) {
	got := ApplyEnabledPathParams("https://api.test/users/:id/posts/:postID",
		[]types.KeyValue{kv("id", "7", true)}, nil)
	want := "https://api.test/users/7/posts/:postID"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A disabled parameter is not a parameter. Substituting it anyway would send a
// value the user explicitly unchecked.
func TestDisabledPathParamIsNotSubstituted(t *testing.T) {
	got := ApplyEnabledPathParams("https://api.test/users/:id", []types.KeyValue{kv("id", "7", false)}, nil)
	if got != "https://api.test/users/:id" {
		t.Errorf("a disabled path param was substituted: %q", got)
	}
}

func TestDisabledQueryParamIsNotAppended(t *testing.T) {
	got := AppendEnabledQuery("https://api.test/x", []types.KeyValue{kv("a", "1", false)}, nil)
	if got != "https://api.test/x" {
		t.Errorf("got %q", got)
	}
}

// The separator is chosen from what the URL already ends with. Getting it wrong
// produces "??" or "&&", which most servers accept and parse differently from
// what was meant.
func TestQuerySeparatorMatchesWhatTheURLAlreadyHas(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://api.test/x", "https://api.test/x?a=1"},
		{"https://api.test/x?b=2", "https://api.test/x?b=2&a=1"},
		{"https://api.test/x?", "https://api.test/x?a=1"},
		{"https://api.test/x?b=2&", "https://api.test/x?b=2&a=1"},
	} {
		if got := AppendEnabledQuery(tc.url, []types.KeyValue{kv("a", "1", true)}, nil); got != tc.want {
			t.Errorf("AppendEnabledQuery(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// Path parameters are substituted BEFORE the query is appended, so a path value
// containing "?" cannot silently become the start of the query string.
func TestPathParamsAreSubstitutedBeforeTheQueryIsAppended(t *testing.T) {
	got := RequestURLWithParams("https://api.test/s/:q",
		[]types.KeyValue{kv("page", "2", true)},
		[]types.KeyValue{kv("q", "a?b", true)}, nil)
	want := "https://api.test/s/a?b&page=2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The URL is interpolated FIRST, so a variable that expands to a path token is
// itself substitutable. Interpolating later would leave the token literal.
func TestAVariableExpandingToAPathTokenIsStillSubstituted(t *testing.T) {
	vars := map[string]string{"base": "https://api.test/users/:id"}
	got := RequestURLWithParams("{{base}}", nil, []types.KeyValue{kv("id", "9", true)}, vars)
	if got != "https://api.test/users/9" {
		t.Errorf("got %q", got)
	}
}

func TestVariablesAreInterpolatedInParamNamesAndValues(t *testing.T) {
	vars := map[string]string{"k": "page", "v": "3"}
	got := AppendEnabledQuery("https://api.test/x", []types.KeyValue{kv("{{k}}", "{{v}}", true)}, vars)
	if got != "https://api.test/x?page=3" {
		t.Errorf("got %q", got)
	}
}

func TestEmptyURLIsLeftAlone(t *testing.T) {
	if got := RequestURLWithParams("", []types.KeyValue{kv("a", "1", true)}, []types.KeyValue{kv("id", "7", true)}, nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// A parameter with no name cannot be addressed and must not produce "=value".
func TestUnnamedParamsAreSkipped(t *testing.T) {
	if got := AppendEnabledQuery("https://api.test/x", []types.KeyValue{kv("", "1", true)}, nil); got != "https://api.test/x" {
		t.Errorf("got %q", got)
	}
	if got := ApplyEnabledPathParams("https://api.test/:id", []types.KeyValue{kv("", "1", true)}, nil); got != "https://api.test/:id" {
		t.Errorf("got %q", got)
	}
}

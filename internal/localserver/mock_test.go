package localserver

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// TestMockPathIgnoresUnresolvedVariables. Example URLs are stored with
// {{baseUrl}} intact. Interpolating per request would make which mock answers
// depend on the selected environment, so only the path is compared.
func TestMockPathFromURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"{{baseUrl}}/v1/users", "/v1/users"},
		{"https://api.example.test/v1/users", "/v1/users"},
		{"https://api.example.test/v1/users/", "/v1/users"},
		{"{{protocol}}://{{host}}/a/b", "/a/b"},
		{"{{baseUrl}}/v1/users?page=2", "/v1/users"},
		{"{{baseUrl}}", "/"},
		{"", "/"},
		{"https://api.example.test", "/"},
	} {
		if got := mockPathFromURL(tc.in); got != tc.want {
			t.Errorf("mockPathFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A trailing slash is a formatting accident in a saved example, not a
// different endpoint.
func TestMockRouteKeyNormalises(t *testing.T) {
	base := mockRouteKey("GET", "/v1/users")
	for _, variant := range []struct{ method, path string }{
		{"get", "/v1/users"},
		{"GET", "v1/users"},
		{"GET", "/v1/users/"},
		{" GET ", " /v1/users "},
	} {
		if got := mockRouteKey(variant.method, variant.path); got != base {
			t.Errorf("mockRouteKey(%q, %q) = %q, want %q", variant.method, variant.path, got, base)
		}
	}
	if mockRouteKey("", "/x") != mockRouteKey("GET", "/x") {
		t.Error("an empty method should default to GET")
	}
	if mockRouteKey("POST", "/x") == mockRouteKey("GET", "/x") {
		t.Error("different methods must not collide")
	}
}

func TestSelectMockExample(t *testing.T) {
	examples := []types.ResponseExample{{Name: "first"}, {Name: "second"}}

	if got, err := selectMockExample(examples, ""); err != nil || got.Name != "first" {
		t.Errorf("no name should select the first: %v %v", got.Name, err)
	}
	if got, err := selectMockExample(examples, "SECOND"); err != nil || got.Name != "second" {
		t.Errorf("name matching should be case-insensitive: %v %v", got.Name, err)
	}
	if got, err := selectMockExample(examples, "  second  "); err != nil || got.Name != "second" {
		t.Errorf("a padded name should still match: %v %v", got.Name, err)
	}
	if _, err := selectMockExample(examples, "missing"); err == nil {
		t.Error("an unknown name must be an error, not a fallback")
	}
	if _, err := selectMockExample(nil, ""); err == nil {
		t.Error("no examples must be an error")
	}
}

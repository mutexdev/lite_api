// Package urlbuild assembles a request's final URL from its raw URL, its path
// parameters and its query parameters, interpolating variables as it goes.
//
// This lived in internal/codegen, which made every package that needed a URL
// import a code GENERATOR to get one: internal/cookiejar needed it to decide
// which jar a request belongs to, internal/wsexec to dial a socket, and
// internal/scripting to hand a URL to a user script. None of the three
// generates code, and the import said something untrue about all of them.
package urlbuild

import (
	"regexp"
	"strings"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/types"
)

// pathParamTokenPattern matches a :name path parameter placeholder.
var pathParamTokenPattern = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_-]*)`)

// RequestURLWithParams interpolates the URL, substitutes path parameters and
// then appends the query string.
//
// The order is not interchangeable. Path parameters are substituted BEFORE the
// query is appended, so a path value containing a "?" cannot silently become
// the start of the query string; and the URL is interpolated first, so a
// variable that expands to a path token is itself substitutable.
func RequestURLWithParams(rawURL string, queryParams, pathParams []types.KeyValue, vars map[string]string) string {
	targetURL := interp.Interpolate(rawURL, vars)
	targetURL = ApplyEnabledPathParams(targetURL, pathParams, vars)
	return AppendEnabledQuery(targetURL, queryParams, vars)
}

// ApplyEnabledPathParams replaces :name tokens with their enabled values.
//
// A token with no matching parameter is left in place rather than blanked. A
// URL that still shows :id is visibly wrong; one silently collapsed to //
// requests a different resource, and the server answers it.
func ApplyEnabledPathParams(rawURL string, pathParams []types.KeyValue, vars map[string]string) string {
	if rawURL == "" || len(pathParams) == 0 {
		return rawURL
	}
	values := map[string]string{}
	for _, param := range pathParams {
		if param.Enabled && param.Name != "" {
			values[param.Name] = interp.Interpolate(param.Value, vars)
		}
	}
	if len(values) == 0 {
		return rawURL
	}
	return pathParamTokenPattern.ReplaceAllStringFunc(rawURL, func(token string) string {
		name := strings.TrimPrefix(token, ":")
		if value, ok := values[name]; ok {
			return value
		}
		return token
	})
}

// AppendEnabledQuery appends the enabled query parameters.
//
// The separator is chosen from what the URL already ends with: "?" when there
// is no query yet, "&" when there is, and nothing at all when the URL already
// ends in a separator. Getting that wrong produces a URL with "??" or "&&" in
// it, which most servers accept and parse differently from what was meant.
func AppendEnabledQuery(rawURL string, params []types.KeyValue, vars map[string]string) string {
	if rawURL == "" {
		return rawURL
	}
	pairs := make([]string, 0, len(params))
	for _, param := range params {
		if param.Enabled && param.Name != "" {
			pairs = append(pairs, interp.Interpolate(param.Name, vars)+"="+interp.Interpolate(param.Value, vars))
		}
	}
	if len(pairs) == 0 {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	if strings.HasSuffix(rawURL, "?") || strings.HasSuffix(rawURL, "&") {
		separator = ""
	}
	return rawURL + separator + strings.Join(pairs, "&")
}

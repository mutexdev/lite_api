// The masking rules that keep credentials out of MCP tool output. They live
// here, in one place, because both the internal/core adapter (masking at the
// source) and this package's tests (proving nothing leaks) must agree on
// exactly one definition of "masked".
//
// The rule for headers and params: a row whose NAME is credential-shaped and
// whose value is a literal gets masked; a value carrying a {{template}} passes
// through, because a template is a reference, not a secret. The rule for auth
// rows is inverted: auth rows are credentials by construction, so literals are
// masked unless the row's name is on a short allowlist of fields that are
// addressing, not secrets (a token URL, a client id, a username).
package mcpserver

import (
	"strings"

	"github.com/mutexdev/lite_api/internal/history"
)

// MaskedValue replaces a masked literal. It is deliberately different from
// internal/history's "<redacted>" so a test that finds the wrong marker in
// tool output also tells us which layer produced it.
const MaskedValue = "<masked>"

// sensitiveNameParts marks credential-shaped names beyond the exact set
// internal/history redacts. Substring match, case-insensitive.
var sensitiveNameParts = []string{"token", "secret", "api-key", "apikey", "api_key", "password", "passwd", "credential"}

// authRowAllowlist names the auth fields that address a provider rather than
// authenticate to it. Everything else in an auth block is treated as a
// credential. Lowercase; matched case-insensitively.
var authRowAllowlist = map[string]bool{
	"username":          true,
	"realm":             true,
	"algorithm":         true,
	"qop":               true,
	"scope":             true,
	"audience":          true,
	"resource":          true,
	"granttype":         true,
	"grant_type":        true,
	"tokenurl":          true,
	"token_url":         true,
	"authurl":           true,
	"auth_url":          true,
	"authorizationurl":  true,
	"authorization_url": true,
	"accesstokenurl":    true,
	"access_token_url":  true,
	"callbackurl":       true,
	"callback_url":      true,
	"redirecturi":       true,
	"redirect_uri":      true,
	"clientid":          true,
	"client_id":         true,
	"headerprefix":      true,
	"header_prefix":     true,
	"addto":             true,
	"add_to":            true,
	"in":                true,
	"key":               true,
	"version":           true,
	"region":            true,
	"service":           true,
	"profile":           true,
	"domain":            true,
	"workstation":       true,
}

// SensitiveName reports whether a header or parameter name is
// credential-shaped: either in internal/history's exact redaction set or
// carrying a credential word.
func SensitiveName(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if history.RedactedHeaders[lowered] {
		return true
	}
	for _, part := range sensitiveNameParts {
		if strings.Contains(lowered, part) {
			return true
		}
	}
	return false
}

// containsTemplate reports whether a value carries a {{reference}} and is
// therefore already indirection rather than a literal credential.
func containsTemplate(value string) bool {
	return strings.Contains(value, "{{")
}

// RedactRows masks literal values on credential-shaped header/param rows.
// The input is not modified.
func RedactRows(rows []KeyValue) []KeyValue {
	if len(rows) == 0 {
		return rows
	}
	out := make([]KeyValue, len(rows))
	copy(out, rows)
	for index, row := range out {
		if row.Value == "" || containsTemplate(row.Value) {
			continue
		}
		if SensitiveName(row.Name) {
			out[index].Value = MaskedValue
		}
	}
	return out
}

// MaskAuthRows masks literal values on auth rows unless the field is
// addressing rather than a credential. The input is not modified.
func MaskAuthRows(rows []KeyValue) []KeyValue {
	if len(rows) == 0 {
		return rows
	}
	out := make([]KeyValue, len(rows))
	copy(out, rows)
	for index, row := range out {
		if row.Value == "" || containsTemplate(row.Value) {
			continue
		}
		if authRowAllowlist[strings.ToLower(strings.TrimSpace(row.Name))] {
			continue
		}
		out[index].Value = MaskedValue
	}
	return out
}

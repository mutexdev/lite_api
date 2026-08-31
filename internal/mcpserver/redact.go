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
	"refreshtokenurl":   true,
	"refresh_token_url": true,
	"signaturemethod":   true,
	"signature_method":  true,
	"callbackurl":       true,
	"callback_url":      true,
	"redirecturi":       true,
	"redirect_uri":      true,
	"clientid":          true,
	"client_id":         true,
	// "key" is the apikey mode's PARAMETER NAME (e.g. X-API-Key), which is
	// addressing; the credential itself travels in the row named "value",
	// which is deliberately NOT here and therefore masked. Do not "fix" this
	// by swapping them.
	"key":           true,
	"headerprefix":  true,
	"header_prefix": true,
	"addto":         true,
	"add_to":        true,
	"in":            true,
	"version":       true,
	"region":        true,
	"service":       true,
	"profile":       true,
	"domain":        true,
	"workstation":   true,
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

// RedactURLQueryLiterals masks literal values of credential-shaped query
// parameters inside a URL string as authored — the "paste a working curl URL"
// pattern, where ?api_key=sk_live_... never becomes a structured Params row
// and would otherwise ship to the agent byte-for-byte.
//
// The surgery is string-level on purpose. Parsing with net/url and
// re-encoding would rewrite the parts that are NOT masked — percent-escapes,
// ordering, and the {{templates}} a URL here is full of — and a definition
// that comes back altered is worse than one that comes back masked. Only the
// value half of a matched pair is replaced; everything else is returned
// byte-for-byte.
func RedactURLQueryLiterals(rawURL string) string {
	queryStart := strings.Index(rawURL, "?")
	if queryStart < 0 {
		return rawURL
	}
	prefix, rest := rawURL[:queryStart+1], rawURL[queryStart+1:]
	query, fragment := rest, ""
	if fragmentStart := strings.Index(rest, "#"); fragmentStart >= 0 {
		query, fragment = rest[:fragmentStart], rest[fragmentStart:]
	}
	parts := strings.Split(query, "&")
	changed := false
	for index, part := range parts {
		name, value, hasValue := strings.Cut(part, "=")
		if !hasValue || value == "" || containsTemplate(value) {
			continue
		}
		if SensitiveName(name) {
			parts[index] = name + "=" + MaskedValue
			changed = true
		}
	}
	if !changed {
		return rawURL
	}
	return prefix + strings.Join(parts, "&") + fragment
}

// MaskKnownSecretValues replaces every occurrence of the given resolved
// secret values with the mask. This is the defence for artifacts recorded
// AFTER interpolation — history URLs, bodies, and header values — where the
// name-based rules cannot help: a resolved secret sits under whatever
// parameter name the user chose, but its value is known to the process, so it
// can be matched exactly.
//
// Values shorter than 8 bytes are skipped. Masking "1234" would also mask
// every calendar year and port number in a response body, corrupting far more
// than it protects — and a secret that short is not protected by masking
// anyway.
func MaskKnownSecretValues(text string, values []string) string {
	if text == "" {
		return text
	}
	for _, value := range values {
		if len(value) < 8 {
			continue
		}
		text = strings.ReplaceAll(text, value, MaskedValue)
	}
	return text
}

// redactArgumentValue applies the read tier's masking to one decoded JSON
// argument value, so a tool's ARGUMENTS are masked by the same rules its
// RESULTS are.
//
// WHY IT LIVES HERE RATHER THAN IN protocol.go. The audit summary was the one
// agent-authored surface where a credential-shaped literal survived: an agent
// that pastes a working curl's `X-Api-Key: sk_live_...` into create_request had
// that literal written to mcp-audit.jsonl and the audit panel verbatim, in a
// form get_request/list_requests mask on the way back out. The fix has to be
// the SAME rules — a second definition of "credential-shaped" is exactly the
// drift this file's header exists to prevent — so this reuses SensitiveName,
// authRowAllowlist, containsTemplate and RedactURLQueryLiterals rather than
// adding any new heuristic.
//
// IT IS STRUCTURAL, NOT PER-TOOL. Nothing here names create_request or knows
// which arguments exist: it walks whatever JSON arrived and applies the rule
// that fits the shape it finds. A tool added later is covered without anyone
// remembering to add it, which is the only version of this that stays true.
//
//   - A ROW ARRAY (headers, params, pathParams, vars, formData — objects with
//     "name" and "value") gets RedactRows' rule: mask the value when the row's
//     NAME is credential-shaped and the value is a literal.
//   - THE AUTH OBJECT gets MaskAuthRows' INVERTED rule, because an auth block is
//     credentials by construction: mask every literal whose field is not on the
//     addressing allowlist.
//   - A PLAIN STRING is masked when its own key is credential-shaped, and
//     otherwise has its URL query literals redacted — the pasted
//     `?api_key=sk_live_...` case, which is a URL wherever it appears.
//   - BODIES ARE NOT SCANNED, exactly as rule 3 says of every other surface: a
//     body is returned as authored by get_request too, so masking it here would
//     make the audit stricter than the read tier rather than equal to it.
//
// The input is never modified; a value with nothing to mask is returned as-is.
func redactArgumentValue(name string, value any) any {
	switch typed := value.(type) {
	case string:
		return redactArgumentString(name, typed)
	case map[string]any:
		if isAuthArgumentName(name) {
			return redactAuthArgumentObject(typed)
		}
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			out[key] = redactArgumentValue(key, nested)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, element := range typed {
			out = append(out, redactArgumentRowOrValue(name, element))
		}
		return out
	default:
		return value
	}
}

// redactArgumentString masks a literal whose own key is credential-shaped, and
// otherwise applies the URL rule. Bodies are exempt: `body` is returned as
// authored everywhere else, and a body mangled in the audit panel would be a
// second, stricter rule than the one rule 3 states.
func redactArgumentString(name, value string) string {
	if value == "" || containsTemplate(value) {
		return value
	}
	if isBodyArgumentName(name) {
		return value
	}
	if SensitiveName(name) {
		return MaskedValue
	}
	return RedactURLQueryLiterals(value)
}

// redactArgumentRowOrValue handles one element of an argument array. A
// {"name":…,"value":…} object is a row and gets the row rule keyed on its OWN
// name; anything else is walked as an ordinary value.
func redactArgumentRowOrValue(name string, element any) any {
	fields, ok := element.(map[string]any)
	if !ok {
		return redactArgumentValue(name, element)
	}
	rowName, hasName := fields["name"].(string)
	rowValue, hasValue := fields["value"].(string)
	if !hasName || !hasValue {
		return redactArgumentValue(name, element)
	}
	out := make(map[string]any, len(fields))
	for key, nested := range fields {
		out[key] = nested
	}
	if rowValue != "" && !containsTemplate(rowValue) && SensitiveName(rowName) {
		out["value"] = MaskedValue
	}
	return out
}

// redactAuthArgumentObject applies MaskAuthRows' rule to an auth argument: an
// auth block's fields are credentials unless they are addressing, so the
// allowlist decides what survives. The apikey mode's `key` (the header name) is
// addressing and stays; its `value` is the credential and is masked — the same
// pairing authRowAllowlist documents.
func redactAuthArgumentObject(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		text, isString := value.(string)
		if !isString || text == "" || containsTemplate(text) {
			out[key] = value
			continue
		}
		if authRowAllowlist[strings.ToLower(strings.TrimSpace(key))] {
			out[key] = text
			continue
		}
		out[key] = MaskedValue
	}
	return out
}

// isAuthArgumentName recognises the argument that carries an auth block.
// Matched case-insensitively on the exact name rather than by substring so an
// argument merely MENTIONING auth is not silently given the inverted rule.
func isAuthArgumentName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "auth")
}

// isBodyArgumentName recognises the arguments rule 3 exempts from scanning.
func isBodyArgumentName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "body", "script", "tests":
		return true
	default:
		return false
	}
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

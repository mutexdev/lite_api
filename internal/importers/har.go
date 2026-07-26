package importers

// US-051 — HAR import.
//
// A HAR file is a RECORDING of browser or proxy traffic, which makes it
// different in kind from every other importer here. Postman, Insomnia and
// OpenAPI files are authored artefacts: what is in them is what the author
// meant to put there. A HAR is whatever happened to cross the wire, which has
// two consequences this file has to handle rather than ignore:
//
//  1. It contains CREDENTIALS. Session cookies and Authorization headers are
//     recorded verbatim. Importing them is correct — stripping them silently
//     would produce a collection where every request 401s for no visible
//     reason — but the user has to be told, because a collection is written to
//     disk and this app can commit one to git. The importer therefore warns.
//
//  2. It contains NOISE. A recorded session commonly holds the same polling
//     request fifty times. Exact duplicates are dropped, because an exact
//     duplicate carries no information the first copy does not. Anything that
//     differs at all — a different body, a different header — is kept, since
//     that difference is the reason someone recorded the session.

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

type harFile struct {
	Log harLog `json:"log"`
}

type harLog struct {
	Version string     `json:"version"`
	Entries []harEntry `json:"entries"`
}

type harEntry struct {
	StartedDateTime string     `json:"startedDateTime"`
	Request         harRequest `json:"request"`
}

type harRequest struct {
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	Headers     []harNameValue `json:"headers"`
	QueryString []harNameValue `json:"queryString"`
	PostData    *harPostData   `json:"postData"`
}

type harNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harPostData struct {
	MimeType string         `json:"mimeType"`
	Text     string         `json:"text"`
	Params   []harNameValue `json:"params"`
}

// harCredentialHeaders are the headers whose presence triggers the warning.
// Deliberately a small, explicit list rather than a heuristic: a false positive
// trains people to ignore the warning, which is worse than not having one.
var harCredentialHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"x-auth-token":        true,
	"x-csrf-token":        true,
}

// harSkippedHeaders are HTTP/2 pseudo-headers and hop-by-hop headers that
// describe the recording rather than the request. Replaying them is at best
// meaningless and at worst breaks the request: a recorded content-length that
// no longer matches an edited body makes the server read a truncated payload.
var harSkippedHeaders = map[string]bool{
	"content-length":    true,
	"connection":        true,
	"keep-alive":        true,
	"transfer-encoding": true,
	"upgrade":           true,
	"host":              true,
}

func ImportHAR(content, fallbackName string) (types.Collection, []string, error) {
	var file harFile
	if err := json.Unmarshal([]byte(content), &file); err != nil {
		return types.Collection{}, nil, fmt.Errorf("HAR document is not valid JSON: %w", err)
	}
	if len(file.Log.Entries) == 0 {
		return types.Collection{}, nil, errors.New("HAR document has no entries")
	}

	name := strings.TrimSpace(fallbackName)
	if name == "" {
		name = "Imported HAR"
	}
	collection := types.Collection{
		ID:             scalar.NewID("collection"),
		Name:           name,
		Format:         "har",
		Auth:           types.AuthConfig{Mode: "none"},
		SecurityConfig: types.CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	seen := map[string]bool{}
	credentialHeaders := map[string]bool{}
	duplicates := 0
	skipped := 0
	seq := 1

	for _, entry := range file.Log.Entries {
		if strings.TrimSpace(entry.Request.URL) == "" {
			skipped++
			continue
		}
		item, credentials := harRequestItem(entry.Request, seq)
		for header := range credentials {
			credentialHeaders[header] = true
		}

		fingerprint := harEntryFingerprint(item)
		if seen[fingerprint] {
			duplicates++
			continue
		}
		seen[fingerprint] = true

		item.Seq = seq
		seq++
		collection.Items = append(collection.Items, item)
	}

	if len(collection.Items) == 0 {
		return types.Collection{}, nil, errors.New("HAR document has no importable requests")
	}

	return collection, harImportWarnings(credentialHeaders, duplicates, skipped), nil
}

func harImportWarnings(credentialHeaders map[string]bool, duplicates, skipped int) []string {
	var warnings []string
	if len(credentialHeaders) > 0 {
		names := make([]string, 0, len(credentialHeaders))
		for header := range credentialHeaders {
			names = append(names, header)
		}
		sort.Strings(names)
		// Named explicitly rather than a generic "contains credentials": the
		// user has to know WHICH headers to review before this collection is
		// shared or committed.
		warnings = append(warnings, fmt.Sprintf(
			"the recording contained credential headers (%s); they were imported so the requests still work — review them before sharing or committing this collection",
			strings.Join(names, ", ")))
	}
	if duplicates > 0 {
		warnings = append(warnings, fmt.Sprintf("%d exactly duplicate request(s) were dropped", duplicates))
	}
	if skipped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d entr(y/ies) had no URL and were skipped", skipped))
	}
	return warnings
}

// harEntryFingerprint identifies an exact duplicate.
//
// Method, URL, body AND headers. Dropping on method+URL alone would collapse a
// login POST and a logout POST to the same endpoint with different bodies, or
// two reads of the same resource with different Accept headers — losing exactly
// the difference the recording was made to capture.
func harEntryFingerprint(item types.RequestItem) string {
	var builder strings.Builder
	builder.WriteString(item.Method)
	builder.WriteString(" ")
	builder.WriteString(item.URL)
	builder.WriteString("\n")

	params := make([]string, 0, len(item.Params))
	for _, param := range item.Params {
		params = append(params, param.Name+"="+param.Value)
	}
	sort.Strings(params)
	builder.WriteString(strings.Join(params, "&"))
	builder.WriteString("\n")

	headers := make([]string, 0, len(item.Headers))
	for _, header := range item.Headers {
		headers = append(headers, strings.ToLower(header.Name)+": "+header.Value)
	}
	sort.Strings(headers)
	builder.WriteString(strings.Join(headers, "\n"))
	builder.WriteString("\n")

	builder.WriteString(item.Body.Mode)
	builder.WriteString(item.Body.Text)
	builder.WriteString(item.Body.JSON)
	builder.WriteString(item.Body.XML)
	for _, field := range item.Body.FormURLEncoded {
		builder.WriteString(field.Name + "=" + field.Value + "&")
	}
	for _, part := range item.Body.Multipart {
		builder.WriteString(part.Name + "=" + part.Value + "&")
	}
	return builder.String()
}

func harRequestItem(request harRequest, seq int) (types.RequestItem, map[string]bool) {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "GET"
	}

	baseURL, params := harSplitURL(request.URL, request.QueryString)

	item := types.RequestItem{
		ID:     scalar.NewID("request"),
		Type:   "http",
		Method: method,
		URL:    baseURL,
		Params: params,
		Auth:   types.AuthConfig{Mode: "none"},
		Seq:    seq,
	}
	item.Name = harRequestName(method, baseURL)

	credentials := map[string]bool{}
	for _, header := range request.Headers {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		// HTTP/2 pseudo-headers (:method, :path) describe the frame, not the
		// request, and are not valid to send.
		if strings.HasPrefix(name, ":") || harSkippedHeaders[lower] {
			continue
		}
		if harCredentialHeaders[lower] {
			credentials[lower] = true
		}
		item.Headers = append(item.Headers, types.KeyValue{Name: name, Value: header.Value, Enabled: true})
	}

	item.Body = harRequestBody(request.PostData)
	return item, credentials
}

// harSplitURL separates the query string from the URL so the params land in the
// params table, where they can be toggled and edited, rather than being frozen
// into the URL text.
//
// The URL's own query is authoritative over the HAR's queryString array: the
// array is the recorder's parse of the same data, and when the two disagree
// (repeated keys, unusual encoding) the raw URL is what actually went out.
func harSplitURL(raw string, queryString []harNameValue) (string, []types.KeyValue) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		// An unparseable URL is kept verbatim rather than dropped — the user
		// can see and fix it, which beats an import that silently loses the
		// request.
		return trimmed, harQueryStringParams(queryString)
	}

	var params []types.KeyValue
	if parsed.RawQuery != "" {
		// Split manually rather than with url.ParseQuery: ParseQuery returns a
		// map, which loses both the order and any repeated key, and repeated
		// keys are common in recorded traffic.
		for _, pair := range strings.Split(parsed.RawQuery, "&") {
			if pair == "" {
				continue
			}
			name, value, _ := strings.Cut(pair, "=")
			decodedName, nameErr := url.QueryUnescape(name)
			if nameErr != nil {
				decodedName = name
			}
			decodedValue, valueErr := url.QueryUnescape(value)
			if valueErr != nil {
				decodedValue = value
			}
			params = append(params, types.KeyValue{Name: decodedName, Value: decodedValue, Enabled: true})
		}
	}
	if len(params) == 0 {
		params = harQueryStringParams(queryString)
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), params
}

func harQueryStringParams(queryString []harNameValue) []types.KeyValue {
	var params []types.KeyValue
	for _, entry := range queryString {
		if strings.TrimSpace(entry.Name) == "" {
			continue
		}
		params = append(params, types.KeyValue{Name: entry.Name, Value: entry.Value, Enabled: true})
	}
	return params
}

// harRequestName gives the request a name a human can scan in a list.
//
// The last meaningful path segment plus the method, because a HAR import is
// typically dozens of requests and "GET /" repeated thirty times is unusable.
func harRequestName(method, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return method + " " + rawURL
	}
	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		if parsed.Host != "" {
			return method + " " + parsed.Host
		}
		return method + " /"
	}
	segments := strings.Split(path, "/")
	last := segments[len(segments)-1]
	if len(segments) > 1 {
		last = segments[len(segments)-2] + "/" + last
	}
	return method + " " + last
}

func harRequestBody(postData *harPostData) types.RequestBody {
	if postData == nil {
		return types.RequestBody{Mode: "none"}
	}

	mimeType := strings.TrimSpace(postData.MimeType)
	if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = parsed
	}
	mimeType = strings.ToLower(mimeType)

	switch {
	case strings.Contains(mimeType, "json"):
		return types.RequestBody{Mode: "json", JSON: postData.Text}
	case strings.Contains(mimeType, "xml"):
		return types.RequestBody{Mode: "xml", XML: postData.Text}
	case mimeType == "application/x-www-form-urlencoded":
		fields := harFormFields(postData)
		if len(fields) == 0 {
			// A recorder that filled in text but not params still has the data;
			// falling back to text keeps the body rather than importing an
			// empty form.
			if strings.TrimSpace(postData.Text) != "" {
				return types.RequestBody{Mode: "text", Text: postData.Text}
			}
			return types.RequestBody{Mode: "none"}
		}
		return types.RequestBody{Mode: "formUrlEncoded", FormURLEncoded: fields}
	case strings.HasPrefix(mimeType, "multipart/"):
		parts := make([]types.FormPart, 0, len(postData.Params))
		for _, param := range postData.Params {
			parts = append(parts, types.FormPart{Name: param.Name, Value: param.Value, Enabled: true})
		}
		if len(parts) == 0 {
			return types.RequestBody{Mode: "none"}
		}
		return types.RequestBody{Mode: "multipartForm", Multipart: parts}
	default:
		if strings.TrimSpace(postData.Text) == "" {
			return types.RequestBody{Mode: "none"}
		}
		return types.RequestBody{Mode: "text", Text: postData.Text}
	}
}

// harFormFields prefers the parsed params and falls back to parsing the raw
// text, because recorders disagree about which they populate.
func harFormFields(postData *harPostData) []types.KeyValue {
	var fields []types.KeyValue
	for _, param := range postData.Params {
		if strings.TrimSpace(param.Name) == "" {
			continue
		}
		value := param.Value
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		fields = append(fields, types.KeyValue{Name: param.Name, Value: value, Enabled: true})
	}
	if len(fields) > 0 {
		return fields
	}
	for _, pair := range strings.Split(postData.Text, "&") {
		if pair == "" {
			continue
		}
		name, value, _ := strings.Cut(pair, "=")
		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			decodedName = name
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			decodedValue = value
		}
		if strings.TrimSpace(decodedName) == "" {
			continue
		}
		fields = append(fields, types.KeyValue{Name: decodedName, Value: decodedValue, Enabled: true})
	}
	return fields
}

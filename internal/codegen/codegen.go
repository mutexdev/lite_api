// Package codegen renders a request, or a saved response example, as code the
// user can paste elsewhere: curl, fetch and the language snippets.
//
// US-045 follow-on. The block that lived in app.go and the existing
// code_generation.go were the same concern split across two files by nothing
// more than when each was written.
package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/urlbuild"
)

func GenerateResponseExampleCode(example types.ResponseExample, language string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "curl", "c-url", "c url":
		return GenerateCurlForResponseExample(example), nil
	case "fetch", "javascript", "js", "node":
		return GenerateFetchForResponseExample(example), nil
	default:
		// US-054. The extended targets live in code_generation.go and are
		// dispatched from the same list the UI picker is built from, so a
		// language can never appear in the picker and be unsupported here.
		if code, ok := generateExtendedCode(example, language); ok {
			return code, nil
		}
		return "", fmt.Errorf("unsupported code language %q", language)
	}
}

func GenerateRequestCode(item types.RequestItem, vars map[string]string, language string) (string, error) {
	example := types.ResponseExample{
		Name:    scalar.FirstNonEmpty(item.Name, "Request"),
		Type:    scalar.FirstNonEmpty(item.Type, "http"),
		Request: ResponseExampleRequestFromItem(item, vars),
	}
	return GenerateResponseExampleCode(example, language)
}

func RequestTypeSupportsCodeGeneration(requestType string) bool {
	switch strings.ToLower(strings.TrimSpace(requestType)) {
	case "", "http", "graphql":
		return true
	default:
		return false
	}
}

func ResponseExampleRequestFromItem(item types.RequestItem, vars map[string]string) types.ResponseExampleRequest {
	bodyMode := scalar.FirstNonEmpty(item.Body.Mode, "none")
	return types.ResponseExampleRequest{
		Method:         strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet)),
		URL:            RequestURLWithParams(item.URL, nil, item.PathParams, vars),
		BodyMode:       bodyMode,
		Body:           requestCodeBodySnapshot(item, vars),
		Headers:        interpolatedKeyValues(item.Headers, vars),
		Params:         interpolatedKeyValues(item.Params, vars),
		FormURLEncoded: interpolatedKeyValues(item.Body.FormURLEncoded, vars),
		MultipartForm:  interpolatedFormParts(item.Body.Multipart, vars),
		File:           interpolatedFileBodyEntries(types.FileBodyEntriesOf(item.Body), vars),
	}
}

func requestCodeBodySnapshot(item types.RequestItem, vars map[string]string) string {
	body := item.Body
	switch NormalizedBodyMode(body.Mode) {
	case "json":
		return interp.Interpolate(body.JSON, vars)
	case "xml":
		return interp.Interpolate(body.XML, vars)
	case "graphql":
		return GraphQLRequestBodySnapshot(body, vars)
	case "formUrlEncoded":
		values := make([]string, 0, len(body.FormURLEncoded))
		for _, row := range body.FormURLEncoded {
			if row.Enabled {
				values = append(values, interp.Interpolate(row.Name, vars)+"="+interp.Interpolate(row.Value, vars))
			}
		}
		return strings.Join(values, "&")
	case "multipartForm":
		values := make([]string, 0, len(body.Multipart))
		for _, row := range body.Multipart {
			if row.Enabled {
				values = append(values, interp.Interpolate(row.Name, vars)+"="+scalar.FirstNonEmpty(interp.Interpolate(row.Value, vars), interp.Interpolate(row.FilePath, vars)))
			}
		}
		return strings.Join(values, "\n")
	case "file":
		if selected, ok := types.SelectedFileBodyEntry(body); ok {
			return interp.Interpolate(selected.FilePath, vars)
		}
		return interp.Interpolate(body.FilePath, vars)
	default:
		return interp.Interpolate(body.Text, vars)
	}
}

func GraphQLRequestBodySnapshot(body types.RequestBody, vars map[string]string) string {
	payload := map[string]interface{}{
		"query": interp.Interpolate(body.GraphQLQuery, vars),
	}
	if variablesText := strings.TrimSpace(interp.Interpolate(body.GraphQLVariables, vars)); variablesText != "" {
		var variables interface{}
		if err := json.Unmarshal([]byte(variablesText), &variables); err == nil {
			payload["variables"] = variables
		} else {
			payload["variables"] = variablesText
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return interp.Interpolate(body.GraphQLQuery, vars)
	}
	return string(encoded)
}

func interpolatedKeyValues(rows []types.KeyValue, vars map[string]string) []types.KeyValue {
	if len(rows) == 0 {
		return nil
	}
	next := make([]types.KeyValue, len(rows))
	for i, row := range rows {
		next[i] = row
		next[i].Name = interp.Interpolate(row.Name, vars)
		next[i].Value = interp.Interpolate(row.Value, vars)
		next[i].Description = interp.Interpolate(row.Description, vars)
	}
	return next
}

func interpolatedFormParts(rows []types.FormPart, vars map[string]string) []types.FormPart {
	if len(rows) == 0 {
		return nil
	}
	next := make([]types.FormPart, len(rows))
	for i, row := range rows {
		next[i] = row
		next[i].Name = interp.Interpolate(row.Name, vars)
		next[i].Value = interp.Interpolate(row.Value, vars)
		next[i].FilePath = interp.Interpolate(row.FilePath, vars)
		next[i].ContentType = interp.Interpolate(row.ContentType, vars)
	}
	return next
}

func interpolatedFileBodyEntries(rows []types.FileBodyEntry, vars map[string]string) []types.FileBodyEntry {
	if len(rows) == 0 {
		return nil
	}
	next := make([]types.FileBodyEntry, len(rows))
	for i, row := range rows {
		next[i] = row
		next[i].FilePath = interp.Interpolate(row.FilePath, vars)
		next[i].ContentType = interp.Interpolate(row.ContentType, vars)
	}
	return next
}

func GenerateCurlForResponseExample(example types.ResponseExample) string {
	req := example.Request
	method := strings.ToUpper(strings.TrimSpace(scalar.FirstNonEmpty(req.Method, http.MethodGet)))
	targetURL := RequestURLWithParams(req.URL, req.Params, nil, nil)
	lines := []string{"curl --request " + scalar.ShellSingleQuote(method) + " " + scalar.ShellSingleQuote(targetURL)}
	headers := types.EnabledKeyValues(req.Headers)
	autoContentType := ResponseExampleRequestContentType(req)
	if autoContentType != "" && !HasHeaderName(headers, "content-type") {
		headers = append(headers, types.KeyValue{Name: "Content-Type", Value: autoContentType, Enabled: true})
	}
	for _, header := range headers {
		lines = append(lines, "  --header "+scalar.ShellSingleQuote(strings.TrimSpace(header.Name)+": "+header.Value))
	}
	switch NormalizedBodyMode(req.BodyMode) {
	case "formUrlEncoded":
		body := encodedKeyValueBody(req.FormURLEncoded)
		if body != "" {
			lines = append(lines, "  --data-raw "+scalar.ShellSingleQuote(body))
		}
	case "multipartForm":
		for _, part := range req.MultipartForm {
			if !part.Enabled || strings.TrimSpace(part.Name) == "" {
				continue
			}
			lines = append(lines, "  --form "+scalar.ShellSingleQuote(curlFormPart(part)))
		}
	case "file":
		if file, ok := selectedExampleFile(req.File); ok && strings.TrimSpace(file.FilePath) != "" {
			lines = append(lines, "  --data-binary "+scalar.ShellSingleQuote("@"+file.FilePath))
		}
	default:
		if body := ResponseExampleRawBody(req); body != "" {
			lines = append(lines, "  --data-raw "+scalar.ShellSingleQuote(body))
		}
	}
	return strings.Join(lines, " \\\n")
}

func GenerateFetchForResponseExample(example types.ResponseExample) string {
	req := example.Request
	method := strings.ToUpper(strings.TrimSpace(scalar.FirstNonEmpty(req.Method, http.MethodGet)))
	targetURL := RequestURLWithParams(req.URL, req.Params, nil, nil)
	headers := types.EnabledKeyValues(req.Headers)
	autoContentType := ResponseExampleRequestContentType(req)
	if autoContentType != "" && !HasHeaderName(headers, "content-type") && NormalizedBodyMode(req.BodyMode) != "multipartForm" {
		headers = append(headers, types.KeyValue{Name: "Content-Type", Value: autoContentType, Enabled: true})
	}

	lines := []string{}
	bodyReference := ""
	switch NormalizedBodyMode(req.BodyMode) {
	case "formUrlEncoded":
		lines = append(lines, "const body = new URLSearchParams();")
		for _, field := range req.FormURLEncoded {
			if field.Enabled && strings.TrimSpace(field.Name) != "" {
				lines = append(lines, fmt.Sprintf("body.append(%s, %s);", jsString(field.Name), jsString(field.Value)))
			}
		}
		if len(lines) > 1 {
			bodyReference = "body"
		} else {
			lines = lines[:0]
		}
	case "multipartForm":
		lines = append(lines, "const body = new FormData();")
		for _, part := range req.MultipartForm {
			if !part.Enabled || strings.TrimSpace(part.Name) == "" {
				continue
			}
			if strings.TrimSpace(part.FilePath) != "" {
				filename := filepath.Base(part.FilePath)
				if filename == "." || filename == string(filepath.Separator) {
					filename = "file"
				}
				comment := fmt.Sprintf(" // %s", part.FilePath)
				if strings.TrimSpace(part.ContentType) != "" {
					comment += " (" + strings.TrimSpace(part.ContentType) + ")"
				}
				lines = append(lines, fmt.Sprintf("body.append(%s, new Blob([]), %s);%s", jsString(part.Name), jsString(filename), comment))
			} else {
				lines = append(lines, fmt.Sprintf("body.append(%s, %s);", jsString(part.Name), jsString(part.Value)))
			}
		}
		if len(lines) > 1 {
			bodyReference = "body"
		} else {
			lines = lines[:0]
		}
	case "file":
		if file, ok := selectedExampleFile(req.File); ok && strings.TrimSpace(file.FilePath) != "" {
			comment := " // " + file.FilePath
			if strings.TrimSpace(file.ContentType) != "" {
				comment += " (" + strings.TrimSpace(file.ContentType) + ")"
			}
			lines = append(lines, "const body = new Blob([]);"+comment)
			bodyReference = "body"
		}
	default:
		if body := ResponseExampleRawBody(req); body != "" {
			lines = append(lines, "const body = "+jsBodyLiteral(body, NormalizedBodyMode(req.BodyMode))+";")
			bodyReference = "body"
		}
	}

	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, fmt.Sprintf("const response = await fetch(%s, {", jsString(targetURL)))
	lines = append(lines, "  method: "+jsString(method)+",")
	if len(headers) > 0 {
		lines = append(lines, "  headers: {")
		for index, header := range headers {
			suffix := ","
			if index == len(headers)-1 {
				suffix = ""
			}
			lines = append(lines, fmt.Sprintf("    %s: %s%s", jsString(strings.TrimSpace(header.Name)), jsString(header.Value), suffix))
		}
		lines = append(lines, "  },")
	}
	if bodyReference != "" {
		lines = append(lines, "  body: "+bodyReference+",")
	}
	lines = append(lines, "});")
	return strings.Join(lines, "\n")
}

func NormalizedBodyMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "form-url-encoded", "formUrlEncoded":
		return "formUrlEncoded"
	case "multipart-form", "multipartForm", "multipart":
		return "multipartForm"
	default:
		return strings.TrimSpace(mode)
	}
}

func HasHeaderName(headers []types.KeyValue, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), name) {
			return true
		}
	}
	return false
}

func ResponseExampleRequestContentType(req types.ResponseExampleRequest) string {
	switch NormalizedBodyMode(req.BodyMode) {
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "text":
		return "text/plain"
	case "graphql":
		return "application/json"
	case "formUrlEncoded":
		if encodedKeyValueBody(req.FormURLEncoded) != "" {
			return "application/x-www-form-urlencoded"
		}
	case "file":
		if file, ok := selectedExampleFile(req.File); ok {
			contentType := strings.TrimSpace(file.ContentType)
			if contentType == "" {
				contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(file.FilePath)))
			}
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			return contentType
		}
	}
	return ""
}

func ResponseExampleRawBody(req types.ResponseExampleRequest) string {
	if NormalizedBodyMode(req.BodyMode) == "" || NormalizedBodyMode(req.BodyMode) == "none" {
		return ""
	}
	return req.Body
}

func encodedKeyValueBody(rows []types.KeyValue) string {
	values := url.Values{}
	for _, row := range rows {
		if row.Enabled && strings.TrimSpace(row.Name) != "" {
			values.Add(row.Name, row.Value)
		}
	}
	return values.Encode()
}

func selectedExampleFile(rows []types.FileBodyEntry) (types.FileBodyEntry, bool) {
	if len(rows) == 0 {
		return types.FileBodyEntry{}, false
	}
	for _, row := range rows {
		if row.Selected {
			return row, true
		}
	}
	return types.FileBodyEntry{}, false
}

func curlFormPart(part types.FormPart) string {
	name := strings.TrimSpace(part.Name)
	if strings.TrimSpace(part.FilePath) != "" {
		value := name + "=@" + strings.TrimSpace(part.FilePath)
		if contentType := strings.TrimSpace(part.ContentType); contentType != "" {
			value += ";type=" + contentType
		}
		return value
	}
	return name + "=" + part.Value
}

func jsString(value string) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return `""`
	}
	return strings.TrimSpace(b.String())
}

func jsBodyLiteral(body, mode string) string {
	if mode == "json" || mode == "graphql" {
		var parsed interface{}
		if err := json.Unmarshal([]byte(body), &parsed); err == nil {
			encoded, encodeErr := json.Marshal(parsed)
			if encodeErr == nil {
				return "JSON.stringify(" + string(encoded) + ")"
			}
		}
	}
	return jsString(body)
}

func ResponseBodyType(response types.Response) string {
	if response.PreviewMode == "json" || scalar.LooksLikeJSON(response.Body) {
		return "json"
	}
	switch response.PreviewMode {
	case "xml", "sse", "image":
		return response.PreviewMode
	default:
		return "text"
	}
}

// The URL builders live in internal/urlbuild. They are re-exported here so the
// generators can keep calling them unqualified, and so no caller had to change
// when they moved out of a code-generation package they never belonged in.
//
// Wrappers rather than `var X = urlbuild.X`: a package-level var would be a
// MUTABLE global that any package could reassign, and it would add three
// entries to this package's initialisation. A function forwards identically,
// costs nothing after inlining, and cannot be reassigned.

func RequestURLWithParams(rawURL string, queryParams, pathParams []types.KeyValue, vars map[string]string) string {
	return urlbuild.RequestURLWithParams(rawURL, queryParams, pathParams, vars)
}

func ApplyEnabledPathParams(rawURL string, pathParams []types.KeyValue, vars map[string]string) string {
	return urlbuild.ApplyEnabledPathParams(rawURL, pathParams, vars)
}

func AppendEnabledQuery(rawURL string, params []types.KeyValue, vars map[string]string) string {
	return urlbuild.AppendEnabledQuery(rawURL, params, vars)
}

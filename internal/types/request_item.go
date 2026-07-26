// A request, the central document type.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type RequestItem struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Params         []KeyValue        `json:"params"`
	PathParams     []KeyValue        `json:"pathParams"`
	Headers        []KeyValue        `json:"headers"`
	Body           RequestBody       `json:"body"`
	ProtoPath      string            `json:"protoPath"`
	GrpcMethodType string            `json:"grpcMethodType"`
	GrpcMessages   []GrpcMessage     `json:"grpcMessages"`
	WSMessages     []WSMessage       `json:"wsMessages"`
	Auth           AuthConfig        `json:"auth"`
	Vars           RequestVars       `json:"vars"`
	Assertions     []Assertion       `json:"assertions"`
	Tests          string            `json:"tests"`
	PreScript      string            `json:"preScript"`
	PostScript     string            `json:"postScript"`
	Docs           string            `json:"docs"`
	Settings       RequestSettings   `json:"settings"`
	Tags           []string          `json:"tags"`
	FolderPath     string            `json:"folderPath"`
	FilePath       string            `json:"filePath"`
	Examples       []ResponseExample `json:"examples"`
	Response       *Response         `json:"response,omitempty"`
	Timeline       []TimelineItem    `json:"timeline"`
	Draft          bool              `json:"draft"`
	Transient      bool              `json:"transient,omitempty"`
	Seq            int               `json:"seq"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

// Constructors and value helpers for the request document.
//
// US-071: these live with the type they operate on because internal/importers
// needs them -- all three formats seed an item from NewRequestItem and then
// override whatever the source actually specified.
func NewRequestItem(name, requestType string, seq int) RequestItem {
	now := time.Now()
	method := http.MethodGet
	urlValue := "{{host}}/get"
	body := RequestBody{Mode: "none"}
	if requestType == "graphql" {
		method = http.MethodPost
		body = RequestBody{Mode: "graphql", GraphQLQuery: "{ __typename }", GraphQLVariables: "{}"}
	}
	if requestType == "websocket" {
		method = "CONNECT"
		urlValue = "wss://echo.websocket.events"
		body = RequestBody{Mode: "ws"}
	}
	if requestType == "grpc" {
		method = "CALL"
		urlValue = "grpc://localhost:50051"
		body = RequestBody{Mode: "grpc"}
	}
	item := RequestItem{
		ID:         newIDLocal("request"),
		Name:       name,
		Type:       requestType,
		Method:     method,
		URL:        urlValue,
		Headers:    []KeyValue{},
		Params:     []KeyValue{},
		PathParams: []KeyValue{},
		Body:       body,
		Auth:       AuthConfig{Mode: "none", APILocation: "header"},
		Settings:   RequestSettings{TimeoutMs: 30000, FollowRedirects: true, MaxRedirects: 5, EncodeURL: true, StoreCookies: true, VerifyTLS: true},
		Tags:       []string{},
		Seq:        seq,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if requestType == "grpc" {
		item.GrpcMessages = []GrpcMessage{{Name: "message 1", Content: "{}"}}
	}
	if requestType == "websocket" {
		item.WSMessages = []WSMessage{{Name: "message 1", Type: "json", Content: "{}", Selected: true}}
	}
	return item
}

func CloneKeyValues(values []KeyValue) []KeyValue {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]KeyValue, len(values))
	copy(cloned, values)
	return cloned
}

func CloneFormParts(values []FormPart) []FormPart {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]FormPart, len(values))
	copy(cloned, values)
	return cloned
}

func CloneFileBodyEntries(values []FileBodyEntry) []FileBodyEntry {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]FileBodyEntry, len(values))
	copy(cloned, values)
	return cloned
}

func FileBodyEntriesOf(body RequestBody) []FileBodyEntry {
	if len(body.Files) > 0 {
		return body.Files
	}
	if strings.TrimSpace(body.FilePath) == "" && strings.TrimSpace(body.FileContentType) == "" {
		return nil
	}
	return []FileBodyEntry{{
		FilePath:    body.FilePath,
		ContentType: body.FileContentType,
		Selected:    true,
	}}
}

func RequestBodySnapshot(body RequestBody) string {
	switch body.Mode {
	case "json":
		return body.JSON
	case "xml":
		return body.XML
	case "graphql":
		return body.GraphQLQuery
	case "formUrlEncoded":
		values := make([]string, 0, len(body.FormURLEncoded))
		for _, row := range body.FormURLEncoded {
			if row.Enabled {
				values = append(values, row.Name+"="+row.Value)
			}
		}
		return strings.Join(values, "&")
	case "multipartForm":
		values := make([]string, 0, len(body.Multipart))
		for _, row := range body.Multipart {
			if row.Enabled {
				values = append(values, row.Name+"="+firstNonEmpty(row.Value, row.FilePath))
			}
		}
		return strings.Join(values, "\n")
	case "file":
		if selected, ok := SelectedFileBodyEntry(body); ok {
			return selected.FilePath
		}
		return body.FilePath
	default:
		return body.Text
	}
}

func SelectedFileBodyEntry(body RequestBody) (FileBodyEntry, bool) {
	if len(body.Files) > 0 {
		for i := range body.Files {
			if body.Files[i].Selected {
				return body.Files[i], true
			}
		}
		return FileBodyEntry{}, false
	}
	if strings.TrimSpace(body.FilePath) == "" && strings.TrimSpace(body.FileContentType) == "" {
		return FileBodyEntry{}, false
	}
	return FileBodyEntry{FilePath: body.FilePath, ContentType: body.FileContentType, Selected: true}, true
}

func GetKeyValue(values []KeyValue, name string) string {
	for _, value := range values {
		if strings.EqualFold(value.Name, name) {
			return value.Value
		}
	}
	return ""
}

// firstNonEmpty is duplicated from internal/scalar rather than imported: types
// is the leaf package everything else depends on, and giving it an import for
// three lines would put a package between it and the rest.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// newIDLocal mirrors scalar.NewID; see the note on firstNonEmpty for why types
// carries its own copy instead of importing.
func newIDLocal(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func SelectedFileBodyFields(entries []FileBodyEntry) (string, string) {
	body := RequestBody{Files: entries}
	if selected, ok := SelectedFileBodyEntry(body); ok {
		return selected.FilePath, selected.ContentType
	}
	return "", ""
}

func ResponseVariableRuntimeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "~")
	name = strings.TrimPrefix(name, "@")
	return strings.TrimSpace(name)
}

func NormalizeOAuth2AdditionalPlacement(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "body", "form", "formdata":
		return "body"
	case "headers", "header":
		return "headers"
	case "queryparams", "queryparam", "query", "url", "params":
		return "queryparams"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

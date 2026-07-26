// A request, the central document type.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

import "time"

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

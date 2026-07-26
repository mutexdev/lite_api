// Request bodies, their file and multipart parts, and gRPC method shape.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type GRPCMethodInfo struct {
	Path       string `json:"path"`
	Service    string `json:"service"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	InputType  string `json:"inputType"`
	OutputType string `json:"outputType"`
	Template   string `json:"template"`
}

type RequestBody struct {
	Mode             string          `json:"mode"`
	Text             string          `json:"text"`
	JSON             string          `json:"json"`
	XML              string          `json:"xml"`
	GraphQLQuery     string          `json:"graphqlQuery"`
	GraphQLVariables string          `json:"graphqlVariables"`
	FormURLEncoded   []KeyValue      `json:"formUrlEncoded"`
	Multipart        []FormPart      `json:"multipart"`
	FilePath         string          `json:"filePath"`
	FileContentType  string          `json:"fileContentType"`
	Files            []FileBodyEntry `json:"files"`
}

type FileBodyEntry struct {
	FilePath    string `json:"filePath"`
	ContentType string `json:"contentType"`
	Selected    bool   `json:"selected"`
}

type FormPart struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	FilePath    string `json:"filePath"`
	ContentType string `json:"contentType"`
	Enabled     bool   `json:"enabled"`
}

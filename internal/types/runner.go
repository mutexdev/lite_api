// Collection runner: options, per-request results and the live snapshot.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

import "time"

type RunnerSnapshot struct {
	Total     int         `json:"total"`
	Passed    int         `json:"passed"`
	Failed    int         `json:"failed"`
	Skipped   int         `json:"skipped"`
	Cancelled int         `json:"cancelled,omitempty"`
	Results   []RunResult `json:"results"`
	Finished  time.Time   `json:"finished"`
	// US-045. Iterations is what was asked for; CompletedIterations is what
	// actually ran. They differ when a run is cancelled or bails, and the gap
	// is the only way a reader can tell "10 iterations, all green" from
	// "stopped during iteration 2 of 10".
	Iterations          int `json:"iterations,omitempty"`
	CompletedIterations int `json:"completedIterations,omitempty"`
}

type RunnerOptions struct {
	SelectedItemIDs []string `json:"selectedItemIds"`
	DelayMs         int      `json:"delayMs,omitempty"`
	// US-047. Stop the run at the first failure instead of continuing.
	BailOnFailure bool `json:"bailOnFailure,omitempty"`
	// US-045. How many times to run the selection. Zero and negative mean one.
	Iterations int `json:"iterations,omitempty"`
	// US-046. Path to a .csv or .json file; one iteration per row.
	DataFile string `json:"dataFile,omitempty"`
}

type GenerateCollectionDocsOptions struct {
	EnvironmentIDs []string `json:"environmentIds"`
}

type GenerateCollectionDocsResult struct {
	FileName     string `json:"fileName"`
	HTML         string `json:"html"`
	YAML         string `json:"yaml"`
	Version      string `json:"version"`
	FolderCount  int    `json:"folderCount"`
	RequestCount int    `json:"requestCount"`
}

type RunResult struct {
	// Iteration is 1-based and omitted for single-iteration runs, so an
	// existing consumer that never asked for iterations sees the shape it
	// always saw.
	Iteration  int       `json:"iteration,omitempty"`
	ItemID     string    `json:"itemId"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Code       int       `json:"code"`
	DurationMs int64     `json:"durationMs"`
	Error      string    `json:"error"`
	At         time.Time `json:"at"`
}

type RequestPatch struct {
	Name           *string          `json:"name"`
	Type           *string          `json:"type"`
	Method         *string          `json:"method"`
	URL            *string          `json:"url"`
	Params         *[]KeyValue      `json:"params"`
	PathParams     *[]KeyValue      `json:"pathParams"`
	Headers        *[]KeyValue      `json:"headers"`
	Body           *RequestBody     `json:"body"`
	ProtoPath      *string          `json:"protoPath"`
	GrpcMethodType *string          `json:"grpcMethodType"`
	GrpcMessages   *[]GrpcMessage   `json:"grpcMessages"`
	WSMessages     *[]WSMessage     `json:"wsMessages"`
	Auth           *AuthConfig      `json:"auth"`
	Vars           *RequestVars     `json:"vars"`
	Assertions     *[]Assertion     `json:"assertions"`
	Tests          *string          `json:"tests"`
	PreScript      *string          `json:"preScript"`
	PostScript     *string          `json:"postScript"`
	Docs           *string          `json:"docs"`
	Settings       *RequestSettings `json:"settings"`
	Tags           *[]string        `json:"tags"`
}

type ImportPayload struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	GroupBy     string `json:"groupBy"`
	SourceURL   string `json:"sourceUrl"`
	OpenAPISync bool   `json:"openapiSync"`
	// US-044. Opt-in rewriting of pm.* to bru.* on Postman import. The default
	// is false: pm.* is native since US-039-043 and more faithful than any
	// textual rewrite, so translation is only for collections whose scripts
	// were already migrated by hand against the bru API.
	TranslatePostmanScripts bool `json:"translatePostmanScripts,omitempty"`
}

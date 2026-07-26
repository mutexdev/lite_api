// Responses, saved examples, test results and the timeline.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

import "time"

type Response struct {
	Status        int               `json:"status"`
	StatusText    string            `json:"statusText"`
	Headers       map[string]string `json:"headers"`
	HeaderEntries []KeyValue        `json:"headerEntries,omitempty"`
	Metadata      []KeyValue        `json:"metadata,omitempty"`
	Trailers      []KeyValue        `json:"trailers,omitempty"`
	Body          string            `json:"body"`
	BodyBase64    string            `json:"bodyBase64"`
	// US-009. BodyHandle identifies the body in the response store; BodyHead is
	// an inline prefix so a list or a collapsed view can render without a disk
	// read. Both are additive for now — Body and BodyBase64 are still populated
	// and still authoritative — so this step changes no behaviour and no
	// existing test. They become the source of truth only once step 4 moves the
	// readers, and Body/BodyBase64 are deleted last, after the migration has
	// been exercised. See .ralph/plans/US-009.md.
	//
	// omitempty on both: a state.json written before this step has neither, and
	// a response whose body was never stored should not carry empty strings
	// into every persist.
	BodyHandle string `json:"bodyHandle,omitempty"`
	BodyHead   string `json:"bodyHead,omitempty"`
	// US-058. Set by pm.visualizer.set. Rendered only inside a sandboxed
	// iframe — see visualizer.go for why the containment is three layers.
	Visualizer   *VisualizerPayload `json:"visualizer,omitempty"`
	Size         int                `json:"size"`
	DurationMs   int64              `json:"durationMs"`
	Error        string             `json:"error"`
	Cancelled    bool               `json:"cancelled,omitempty"`
	PreviewMode  string             `json:"previewMode"`
	TestResults  []TestResult       `json:"testResults"`
	ScriptLogs   []ScriptLog        `json:"scriptLogs"`
	Assertions   []Assertion        `json:"assertions"`
	RequestedURL string             `json:"requestedUrl"`
	SentAt       time.Time          `json:"sentAt"`
	Cookies      []CookieEntry      `json:"cookies"`
	Timings      ResponseTimings    `json:"timings"`
}

type ResponseExample struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"`
	Request     ResponseExampleRequest `json:"request"`
	Response    ResponseExamplePayload `json:"response"`
}

type ResponseExampleRequest struct {
	Method         string          `json:"method"`
	URL            string          `json:"url"`
	BodyMode       string          `json:"bodyMode"`
	Body           string          `json:"body"`
	Headers        []KeyValue      `json:"headers"`
	Params         []KeyValue      `json:"params"`
	FormURLEncoded []KeyValue      `json:"formUrlEncoded"`
	MultipartForm  []FormPart      `json:"multipartForm"`
	File           []FileBodyEntry `json:"file"`
}

type ResponseExamplePayload struct {
	Status     int        `json:"status"`
	StatusText string     `json:"statusText"`
	Headers    []KeyValue `json:"headers"`
	BodyType   string     `json:"bodyType"`
	Body       string     `json:"body"`
	Size       int        `json:"size"`
	DurationMs int64      `json:"durationMs,omitempty"`
}

type TestResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

type ScriptLog struct {
	Level   string   `json:"level"`
	Message string   `json:"message"`
	Args    []string `json:"args"`
}

type TimelineItem struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	EventType  string     `json:"eventType,omitempty"`
	EventName  string     `json:"eventName,omitempty"`
	Message    string     `json:"message"`
	At         time.Time  `json:"at"`
	Duration   int64      `json:"duration"`
	RequestID  string     `json:"requestId"`
	Source     string     `json:"source,omitempty"`
	Phase      string     `json:"phase,omitempty"`
	Method     string     `json:"method,omitempty"`
	URL        string     `json:"url,omitempty"`
	Status     int        `json:"status,omitempty"`
	StatusText string     `json:"statusText,omitempty"`
	Error      string     `json:"error,omitempty"`
	Payload    string     `json:"payload,omitempty"`
	Metadata   []KeyValue `json:"metadata,omitempty"`
	Trailers   []KeyValue `json:"trailers,omitempty"`
	SourceFile string     `json:"sourceFile,omitempty"`
}

// VisualizerPayload is what pm.visualizer.set stored for a response.
type VisualizerPayload struct {
	Template string `json:"template"`
	// Data is the raw JSON the script passed. Kept as text rather than a
	// decoded map so what the script set is what the template sees, without a
	// round trip through Go's type system reordering keys or turning integers
	// into floats.
	Data string `json:"data,omitempty"`
}

type CookieEntry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	Domain    string    `json:"domain"`
	Path      string    `json:"path"`
	Expires   time.Time `json:"expires"`
	Session   bool      `json:"session"`
	Secure    bool      `json:"secure"`
	HTTPOnly  bool      `json:"httpOnly"`
	SameSite  string    `json:"sameSite"`
	HostOnly  bool      `json:"hostOnly"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ResponseTimings marks observed phases explicitly; zero without its matching
// availability flag means the phase was not applicable or not exposed by Go.
type ResponseTimings struct {
	DNSMs             int64 `json:"dnsMs"`
	ConnectMs         int64 `json:"connectMs"`
	TLSMs             int64 `json:"tlsMs"`
	UploadMs          int64 `json:"uploadMs"`
	WaitMs            int64 `json:"waitMs"`
	DownloadMs        int64 `json:"downloadMs"`
	TotalMs           int64 `json:"totalMs"`
	RedirectCount     int   `json:"redirectCount"`
	ConnectionReused  bool  `json:"connectionReused"`
	DNSAvailable      bool  `json:"dnsAvailable"`
	ConnectAvailable  bool  `json:"connectAvailable"`
	TLSAvailable      bool  `json:"tlsAvailable"`
	UploadAvailable   bool  `json:"uploadAvailable"`
	WaitAvailable     bool  `json:"waitAvailable"`
	DownloadAvailable bool  `json:"downloadAvailable"`
}

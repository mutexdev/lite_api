// OpenAPI sync: options, diffs, drift and their results.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

type OpenAPISyncOptions struct {
	SourceURL         string            `json:"sourceUrl"`
	Content           string            `json:"content"`
	GroupBy           string            `json:"groupBy"`
	PreserveValues    bool              `json:"preserveValues"`
	RemoveDeleted     bool              `json:"removeDeleted"`
	EndpointDecisions map[string]string `json:"endpointDecisions,omitempty"`
}

type OpenAPISyncEndpointChange struct {
	ID              string `json:"id"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Name            string `json:"name"`
	Change          string `json:"change"`
	ItemID          string `json:"itemId,omitempty"`
	DefaultDecision string `json:"defaultDecision"`
}

type OpenAPISyncResult struct {
	SourceURL     string                      `json:"sourceUrl"`
	GroupBy       string                      `json:"groupBy"`
	SpecHash      string                      `json:"specHash"`
	Title         string                      `json:"title"`
	Version       string                      `json:"version"`
	EndpointCount int                         `json:"endpointCount"`
	Added         int                         `json:"added"`
	Updated       int                         `json:"updated"`
	Removed       int                         `json:"removed"`
	Unchanged     int                         `json:"unchanged"`
	HasChanges    bool                        `json:"hasChanges"`
	LastSyncDate  string                      `json:"lastSyncDate,omitempty"`
	Changes       []OpenAPISyncEndpointChange `json:"changes"`
}

type OpenAPISyncUpdateCheckResult struct {
	SourceURL      string `json:"sourceUrl"`
	StoredSpecHash string `json:"storedSpecHash"`
	RemoteSpecHash string `json:"remoteSpecHash"`
	HasUpdates     bool   `json:"hasUpdates"`
	CheckedAt      string `json:"checkedAt"`
}

type OpenAPISyncSpecViewResult struct {
	SourceURL    string `json:"sourceUrl"`
	Content      string `json:"content"`
	FromCache    bool   `json:"fromCache"`
	Fetched      bool   `json:"fetched"`
	NoStoredSpec bool   `json:"noStoredSpec"`
}

type OpenAPISyncSpecDiffLine struct {
	Kind      string `json:"kind"`
	OldNumber int    `json:"oldNumber,omitempty"`
	NewNumber int    `json:"newNumber,omitempty"`
	OldText   string `json:"oldText,omitempty"`
	NewText   string `json:"newText,omitempty"`
}

type OpenAPISyncSpecDiffResult struct {
	SourceURL      string                      `json:"sourceUrl"`
	StoredContent  string                      `json:"storedContent"`
	NewContent     string                      `json:"newContent"`
	NoStoredSpec   bool                        `json:"noStoredSpec"`
	StoredSpecHash string                      `json:"storedSpecHash"`
	NewSpecHash    string                      `json:"newSpecHash"`
	Added          int                         `json:"added"`
	Updated        int                         `json:"updated"`
	Removed        int                         `json:"removed"`
	Unchanged      int                         `json:"unchanged"`
	Changes        []OpenAPISyncEndpointChange `json:"changes"`
	Lines          []OpenAPISyncSpecDiffLine   `json:"lines"`
}

type OpenAPILocalDriftOptions struct {
	ResetIDs   []string `json:"resetIds"`
	RestoreIDs []string `json:"restoreIds"`
	DeleteIDs  []string `json:"deleteIds"`
}

type OpenAPILocalDriftResult struct {
	SourceURL               string                      `json:"sourceUrl"`
	GroupBy                 string                      `json:"groupBy"`
	SpecEndpointCount       int                         `json:"specEndpointCount"`
	CollectionEndpointCount int                         `json:"collectionEndpointCount"`
	Modified                int                         `json:"modified"`
	Missing                 int                         `json:"missing"`
	LocalOnly               int                         `json:"localOnly"`
	InSync                  int                         `json:"inSync"`
	HasChanges              bool                        `json:"hasChanges"`
	NoStoredSpec            bool                        `json:"noStoredSpec,omitempty"`
	LastSyncDate            string                      `json:"lastSyncDate,omitempty"`
	Changes                 []OpenAPISyncEndpointChange `json:"changes"`
}

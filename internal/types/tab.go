// Open tabs and the feature ledger.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

type OpenTab struct {
	ID             string `json:"id"`
	CollectionID   string `json:"collectionId"`
	ItemID         string `json:"itemId"`
	Kind           string `json:"kind"`
	ExampleID      string `json:"exampleId,omitempty"`
	ExampleName    string `json:"exampleName,omitempty"`
	RequestPaneTab string `json:"requestPaneTab"`
	ResponseTab    string `json:"responseTab"`
	Transient      bool   `json:"transient,omitempty"`
}

type Feature struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	Tests       []string `json:"tests"`
	SourceRefs  []string `json:"sourceRefs"`
}

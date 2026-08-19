package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveResponseTimelineFullFidelityAndDirectory(t *testing.T) {
	app, c, item := responseExportApp(t)
	app.mu.Lock()
	p, _ := findItem(collectionPointer(app, c.ID), item.ID)
	p.Response = &Response{Status: 201, StatusText: "Created", DurationMs: 42, Size: 9, Error: "", Cancelled: false}
	p.Timeline = []TimelineItem{{ID: "t", Kind: "response", Message: "done", Metadata: []KeyValue{{Name: "x", Value: "y"}}, Trailers: []KeyValue{{Name: "z", Value: "q"}}}}
	app.mu.Unlock()
	dir := t.TempDir()
	result, err := app.SaveResponseTimeline(c.ID, item.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc responseTimelineExport
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || doc.Response.Status != 201 || len(doc.Timeline) != 1 || doc.Timeline[0].Metadata[0].Value != "y" || result.Path != filepath.Join(dir, "Export Me-timeline.json") {
		t.Fatalf("bad export %#v %#v", result, doc)
	}
}

func TestSaveResponseTimelineAllowsTimelineWithoutResponse(t *testing.T) {
	app, c, item := responseExportApp(t)
	app.mu.Lock()
	p, _ := findItem(collectionPointer(app, c.ID), item.ID)
	p.Timeline = []TimelineItem{{ID: "only", Kind: "event"}}
	app.mu.Unlock()
	if _, err := app.SaveResponseTimeline(c.ID, item.ID, filepath.Join(t.TempDir(), "x.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveResponseTimeline(c.ID, "missing", filepath.Join(t.TempDir(), "x.json")); err == nil {
		t.Fatal("expected request error")
	}
}

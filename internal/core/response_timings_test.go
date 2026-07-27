package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPTimingTimelineItemsExposeMeasuredAndReuseRows(t *testing.T) {
	item := RequestItem{ID: "r"}
	response := Response{Status: 200, Timings: ResponseTimings{DNSAvailable: true, ConnectAvailable: true, TLSAvailable: true, UploadAvailable: true, WaitAvailable: true, WaitMs: 12, DownloadAvailable: true, DownloadMs: 5, ConnectionReused: true, RedirectCount: 1}}
	rows := httpTimingTimelineItems(item, response)
	phases := map[string]bool{}
	for _, row := range rows {
		phases[row.Phase] = true
		if row.Source != "network" || row.Kind != "network" {
			t.Fatalf("bad row %#v", row)
		}
	}
	for _, want := range []string{"wait", "download", "redirect", "connection-reused"} {
		if !phases[want] {
			t.Fatalf("missing %s: %#v", want, rows)
		}
	}
	ids := map[string]bool{}
	for _, row := range rows {
		if ids[row.ID] {
			t.Fatalf("duplicate timing id %q: %#v", row.ID, rows)
		}
		ids[row.ID] = true
	}
}

func TestHTTPResponseTimingsDelayedStreamRedirectReuseCancelAndExport(t *testing.T) {
	delayed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Duplicate", "first")
		w.Header().Add("X-Duplicate", "第二")
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("one"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("two"))
	}))
	defer delayed.Close()
	app, c, item := responseExportApp(t)
	setTimingURL(t, app, c.ID, item.ID, delayed.URL)
	state, err := app.SendRequest(c.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := findItemInState(state, c.ID, item.ID)
	tm := got.Response.Timings
	if !tm.WaitAvailable || !tm.DownloadAvailable || tm.WaitMs < 15 || tm.DownloadMs < 15 || tm.TotalMs < tm.WaitMs+tm.DownloadMs {
		t.Fatalf("bad delayed timings %#v", tm)
	}
	duplicates := []string{}
	for _, header := range got.Response.HeaderEntries {
		if header.Name == "X-Duplicate" {
			duplicates = append(duplicates, header.Value)
		}
	}
	if len(duplicates) != 2 || duplicates[0] != "first" || duplicates[1] != "第二" {
		t.Fatalf("header entries %#v", got.Response.HeaderEntries)
	}
	path := filepath.Join(t.TempDir(), "timeline.json")
	if _, err := app.SaveResponseTimeline(c.ID, item.ID, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc responseTimelineExport
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Response.Timings != tm {
		t.Fatalf("timings lost %#v %#v", doc.Response.Timings, tm)
	}

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/middle", http.StatusFound)
		case "/middle":
			http.Redirect(w, r, "/end", http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer redirect.Close()
	setTimingURL(t, app, c.ID, item.ID, redirect.URL)
	state, err = app.SendRequest(c.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = findItemInState(state, c.ID, item.ID)
	if got.Response.Timings.RedirectCount != 2 {
		t.Fatalf("redirects %#v", got.Response.Timings)
	}
	found := false
	for _, row := range got.Timeline {
		if row.Phase == "redirect" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing redirect row")
	}

	setTimingURL(t, app, c.ID, item.ID, delayed.URL)
	if _, err = app.SendRequest(c.ID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(c.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = findItemInState(state, c.ID, item.ID)
	if !got.Response.Timings.ConnectionReused {
		t.Fatalf("expected reuse %#v", got.Response.Timings)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := app.executeHTTP(ctx, c.ID, *collectionPointer(app, c.ID), got, map[string]string{}, nil, func(TimelineItem) {})
	if !response.Cancelled || response.Timings.TotalMs < 0 {
		t.Fatalf("cancel timing %#v", response)
	}
}

func setTimingURL(t *testing.T, app *App, collectionID, itemID, url string) {
	t.Helper()
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	p, _ := findItem(collectionPointer(app, collectionID), itemID)
	p.Draft = false
	app.mu.Unlock()
}

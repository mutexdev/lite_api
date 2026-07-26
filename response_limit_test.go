package main

// US-011 — bounded response body reads.
//
// The failure this prevents needs no attacker: io.ReadAll on a response body is
// an unbounded allocation whose size is chosen by the remote server, so one
// misconfigured endpoint streaming gigabytes takes the process down.
//
// The subtle half is the boundary. io.LimitReader alone cannot tell "the body
// was exactly the limit" from "the body was longer and we stopped", so a body
// landing exactly on the cap would be truncated and reported as complete. That
// is the silent corruption the story rules out, and it is what the
// exactly-at-limit case below exists to catch.

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseBodyReadLimitResolution(t *testing.T) {
	if got := responseBodyReadLimit(RequestPreferences{}); got != defaultResponseBodyLimit {
		t.Errorf("unset preference should give the default: got %d", got)
	}
	if got := responseBodyReadLimit(RequestPreferences{MaxResponseBytes: 4096}); got != 4096 {
		t.Errorf("explicit preference ignored: got %d", got)
	}
	// Negative means "no limit" and is honoured: a user who deliberately asked
	// for unbounded reads against a trusted local endpoint should get them.
	if got := responseBodyReadLimit(RequestPreferences{MaxResponseBytes: -1}); got != -1 {
		t.Errorf("negative preference should mean unlimited: got %d", got)
	}
}

func TestReadResponseBodyLimitedTruncatesAndReports(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		limit         int64
		wantBody      string
		wantTruncated bool
	}{
		{"under the limit", "hello", 100, "hello", false},
		// The case that a naive io.LimitReader gets wrong: a body of exactly
		// the limit is COMPLETE and must not be flagged.
		{"exactly at the limit", strings.Repeat("a", 64), 64, strings.Repeat("a", 64), false},
		{"one byte over", strings.Repeat("a", 65), 64, strings.Repeat("a", 64), true},
		{"far over", strings.Repeat("a", 5000), 64, strings.Repeat("a", 64), true},
		{"empty body", "", 64, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, truncated, err := readResponseBodyLimited(strings.NewReader(tc.body), tc.limit)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Errorf("body = %d bytes, want %d", len(body), len(tc.wantBody))
			}
			if truncated != tc.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
		})
	}
}

func TestReadResponseBodyLimitedHonoursUnlimited(t *testing.T) {
	body := strings.Repeat("z", 8192)
	got, truncated, err := readResponseBodyLimited(strings.NewReader(body), -1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Errorf("unlimited read returned %d bytes, want %d", len(got), len(body))
	}
	if truncated {
		t.Error("an unlimited read must never report truncation")
	}
}

// TestReadResponseBodyLimitedDoesNotOverAllocate is the point of the story.
// With a 1 KB cap against a 4 MB body, the read must not pull 4 MB into memory
// — proven by the returned slice's CAPACITY, not just its length, since
// io.ReadAll on the whole stream would leave a multi-megabyte backing array.
func TestReadResponseBodyLimitedDoesNotOverAllocate(t *testing.T) {
	huge := bytes.Repeat([]byte("x"), 4<<20)
	body, truncated, err := readResponseBodyLimited(bytes.NewReader(huge), 1024)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !truncated || len(body) != 1024 {
		t.Fatalf("expected a truncated 1024-byte read, got %d bytes truncated=%v", len(body), truncated)
	}
	if cap(body) > 64<<10 {
		t.Errorf("read allocated %d bytes of capacity for a 1 KB limit — the whole body was buffered", cap(body))
	}
}

// TestHTTPResponseSurfacesTruncation drives the real request path: a server
// returning more than the configured cap must produce a response the UI can
// tell is incomplete.
func TestHTTPResponseSurfacesTruncation(t *testing.T) {
	const served = 200_000
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(bytes.Repeat([]byte("q"), served))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	preferences := state.Preferences
	preferences.Request.MaxResponseBytes = 1000
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}

	collection := state.Workspaces[0].Collections[0]
	created, err := app.CreateRequest(collection.ID, "http", "truncation probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "truncation probe" {
				itemID = item.ID
			}
		}
	}
	url := server.URL
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	final, err := app.SendRequest(collection.ID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	item, ok := findItemInState(final, collection.ID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}
	if len(item.Response.Body) != 1000 {
		t.Errorf("body is %d bytes, want the 1000-byte cap", len(item.Response.Body))
	}
	if item.Response.Headers["x-liteapi-body-truncated"] != "true" {
		t.Error("truncation was not surfaced — a user cannot tell this body is incomplete")
	}
	if got := item.Response.Headers["x-liteapi-body-limit"]; got != "1000" {
		t.Errorf("limit header = %q, want %q", got, "1000")
	}
}

// TestHTTPResponseUnderLimitIsNotFlagged keeps the test above honest: an
// implementation that always set the header would satisfy it.
func TestHTTPResponseUnderLimitIsNotFlagged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"small":"payload"}`)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	created, err := app.CreateRequest(collection.ID, "http", "small response")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "small response" {
				itemID = item.ID
			}
		}
	}
	url := server.URL
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	final, err := app.SendRequest(collection.ID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	item, _ := findItemInState(final, collection.ID, itemID)
	if item.Response == nil {
		t.Fatal("no response")
	}
	if _, flagged := item.Response.Headers["x-liteapi-body-truncated"]; flagged {
		t.Error("a complete body was flagged as truncated")
	}
	if item.Response.Body != `{"small":"payload"}` {
		t.Errorf("body altered: %q", item.Response.Body)
	}
}

package core

// US-010 — tests for ReadResponseBody.
//
// The interesting assertions are about SLICE BOUNDARIES, because that is where
// a bounded read quietly corrupts data. A byte-offset slice through UTF-8 text
// splits runes; a byte-offset slice of base64 breaks quartet alignment. Both
// failures look like ordinary text until someone reads it.

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func storeBody(t *testing.T, app *App, body string) string {
	t.Helper()
	store, err := app.responseStore()
	if err != nil {
		t.Fatalf("responseStore: %v", err)
	}
	handle, err := store.Put([]byte(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return string(handle)
}

func TestReadResponseBodyReturnsWholeSmallBody(t *testing.T) {
	app := newAppForTest(t)
	body := `{"ok":true}`
	handle := storeBody(t, app, body)

	got, err := app.ReadResponseBody(handle, 0, 0)
	if err != nil {
		t.Fatalf("ReadResponseBody: %v", err)
	}
	if got.Raw != body {
		t.Errorf("Raw = %q, want %q", got.Raw, body)
	}
	if got.TotalSize != len(body) || got.Length != len(body) {
		t.Errorf("sizes wrong: length=%d total=%d want %d", got.Length, got.TotalSize, len(body))
	}
	if got.Truncated {
		t.Error("a whole body should not report Truncated")
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Base64)
	if err != nil {
		t.Fatalf("Base64 does not decode: %v", err)
	}
	if string(decoded) != body {
		t.Errorf("Base64 decodes to %q, want %q", decoded, body)
	}
}

// TestReadResponseBodyBase64AlwaysDecodes is the quartet-alignment guarantee.
// A slice taken at an arbitrary byte offset and base64-encoded by hand would
// fail this at most offsets.
func TestReadResponseBodyBase64AlwaysDecodes(t *testing.T) {
	app := newAppForTest(t)
	body := strings.Repeat("abcdefghij", 500) // 5000 bytes, not a multiple of 3
	handle := storeBody(t, app, body)

	for _, offset := range []int{0, 1, 2, 3, 7, 1000, 2999, 4999} {
		got, err := app.ReadResponseBody(handle, offset, 512)
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(got.Base64)
		if err != nil {
			t.Fatalf("offset %d: Base64 does not decode: %v", offset, err)
		}
		if string(decoded) != got.Raw {
			t.Errorf("offset %d: Base64 and Raw describe different bytes", offset)
		}
		if got.Raw != body[got.Offset:got.Offset+got.Length] {
			t.Errorf("offset %d: Raw is not the slice it claims to be", offset)
		}
	}
}

// TestReadResponseBodyNeverSplitsARune. Every slice of a multi-byte body must
// be valid UTF-8, at every offset, or chunked rendering shows U+FFFD at each
// seam.
func TestReadResponseBodyNeverSplitsARune(t *testing.T) {
	app := newAppForTest(t)
	body := strings.Repeat("世界🌍", 400) // 3- and 4-byte runes
	handle := storeBody(t, app, body)

	for offset := 0; offset < 64; offset++ {
		got, err := app.ReadResponseBody(handle, offset, 100)
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		if !utf8.ValidString(got.Raw) {
			t.Fatalf("offset %d produced invalid UTF-8", offset)
		}
	}
}

// TestReadResponseBodyPagesWithoutGapsOrRepeats walks a multi-byte body end to
// end using the returned Offset/Length, which is what a caller must do. A
// caller that advanced by what it REQUESTED would skip the bytes trimmed off a
// rune boundary.
func TestReadResponseBodyPagesWithoutGapsOrRepeats(t *testing.T) {
	app := newAppForTest(t)
	body := strings.Repeat("héllo wörld 世界🌍 ", 200)
	handle := storeBody(t, app, body)

	var rebuilt strings.Builder
	offset, guard := 0, 0
	for {
		guard++
		if guard > 10000 {
			t.Fatal("paging did not terminate — the read is not advancing")
		}
		got, err := app.ReadResponseBody(handle, offset, 97) // odd size, lands mid-rune
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		if got.Length == 0 {
			break
		}
		if got.Offset != offset {
			t.Fatalf("page start moved: asked %d, got %d", offset, got.Offset)
		}
		rebuilt.WriteString(got.Raw)
		offset = got.Offset + got.Length
		if !got.Truncated {
			break
		}
	}
	if rebuilt.String() != body {
		t.Errorf("paged read did not reconstruct the body (%d bytes vs %d)", rebuilt.Len(), len(body))
	}
}

func TestReadResponseBodyBoundsAndErrors(t *testing.T) {
	app := newAppForTest(t)
	body := "short body"
	handle := storeBody(t, app, body)

	if _, err := app.ReadResponseBody("", 0, 0); err == nil {
		t.Error("an empty handle should be an error")
	}
	if _, err := app.ReadResponseBody("0000000000000000000000000000000000000000000000000000000000000000", 0, 0); err == nil {
		t.Error("an unknown handle should be an error")
	}

	// Past the end is a valid empty tail, not a failure: a caller paging a body
	// that shrank under it should not see an error.
	past, err := app.ReadResponseBody(handle, len(body)+50, 10)
	if err != nil {
		t.Fatalf("reading past the end failed: %v", err)
	}
	if past.Length != 0 || past.Raw != "" {
		t.Errorf("past-the-end read returned %d bytes", past.Length)
	}

	// A negative offset clamps rather than panicking.
	neg, err := app.ReadResponseBody(handle, -5, 4)
	if err != nil {
		t.Fatalf("negative offset: %v", err)
	}
	if neg.Offset != 0 {
		t.Errorf("negative offset was not clamped: %d", neg.Offset)
	}
}

// TestReadResponseBodyRespectsTheCeiling. An unbounded length must not let a
// caller pull an arbitrarily large body into memory in one call.
func TestReadResponseBodyRespectsTheCeiling(t *testing.T) {
	app := newAppForTest(t)
	body := strings.Repeat("x", responseBodyReadCeiling+4096)
	handle := storeBody(t, app, body)

	got, err := app.ReadResponseBody(handle, 0, 0)
	if err != nil {
		t.Fatalf("ReadResponseBody: %v", err)
	}
	if got.Length != responseBodyReadCeiling {
		t.Errorf("length %d, want the ceiling %d", got.Length, responseBodyReadCeiling)
	}
	if !got.Truncated {
		t.Error("a body larger than the ceiling must report Truncated")
	}
	if got.TotalSize != len(body) {
		t.Errorf("TotalSize %d, want %d", got.TotalSize, len(body))
	}

	// An oversized explicit length is capped the same way.
	big, err := app.ReadResponseBody(handle, 0, len(body)*2)
	if err != nil {
		t.Fatalf("oversized length: %v", err)
	}
	if big.Length != responseBodyReadCeiling {
		t.Errorf("oversized length was not capped: %d", big.Length)
	}
}

// TestSentRequestsGetABodyHandle is US-009 step 4: a response produced by the
// live request path — not just one backfilled at load — carries a handle, and
// the stored bytes match what the user sees.
func TestSentRequestsGetABodyHandle(t *testing.T) {
	const payload = `{"served":"by the request path"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	created, err := app.CreateRequest(collection.ID, "http", "handle probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "handle probe" {
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
	if item.Response.Body != payload {
		t.Errorf("Body changed: %q", item.Response.Body)
	}
	if item.Response.BodyHandle == "" {
		t.Fatal("a sent request produced no body handle")
	}

	// And the binding must return exactly what the user is looking at — this is
	// the round trip the frontend will depend on once response.ts is rewired.
	slice, err := app.ReadResponseBody(item.Response.BodyHandle, 0, 0)
	if err != nil {
		t.Fatalf("ReadResponseBody: %v", err)
	}
	if slice.Raw != payload {
		t.Errorf("ReadResponseBody returned %q, want %q", slice.Raw, payload)
	}
}

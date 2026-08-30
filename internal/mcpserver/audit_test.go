package mcpserver

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The audit is the user's record of what an agent did through their app. These
// tests hold it to the two properties that make it worth having: every
// tools/call lands in it, including the ones that failed or were refused, and
// no entry can be made unbounded by whatever a client chose to send.

func TestAuditRecordsOneEntryPerToolCall(t *testing.T) {
	backend := newFixtureBackend()
	server, log := newAuditedServer(t, backend)

	callTool(t, server, "list_collections", nil)
	callTool(t, server, "get_history", map[string]any{"collectionId": "col_pos", "requestId": "req_create", "limit": 1})
	callTool(t, server, "run_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"})

	entries := log.all()
	if len(entries) != 3 {
		t.Fatalf("recorded %d entries, want 3: %+v", len(entries), entries)
	}
	wantTools := []string{"list_collections", "get_history", "run_request"}
	for index, entry := range entries {
		if entry.Tool != wantTools[index] {
			t.Errorf("entry %d tool = %q, want %q", index, entry.Tool, wantTools[index])
		}
		if entry.Outcome != outcomeOK {
			t.Errorf("entry %d outcome = %q, want %q", index, entry.Outcome, outcomeOK)
		}
		if entry.DurationMs < 0 {
			t.Errorf("entry %d duration = %d, want >= 0", index, entry.DurationMs)
		}
		if entry.At.IsZero() {
			t.Errorf("entry %d has no timestamp", index)
		}
		if entry.At.Location() != nil && entry.At.Location().String() != "UTC" {
			t.Errorf("entry %d timestamp is not UTC: %s", index, entry.At.Location())
		}
	}
	// The arguments are what make a row worth reading: which request was run.
	if summary := entries[2].ArgsSummary; !strings.Contains(summary, "collectionId") || !strings.Contains(summary, "req_create") {
		t.Errorf("run_request summary lost its ids: %q", summary)
	}
	if entries[0].ArgsSummary != "" {
		t.Errorf("a no-argument call summarised as %q, want empty", entries[0].ArgsSummary)
	}
}

// A validation failure and a backend failure are both "error": the split that
// matters is denial, and inventing more outcomes would only make the panel
// harder to scan.
func TestAuditRecordsFailedCallsAsErrors(t *testing.T) {
	t.Run("a validation failure", func(t *testing.T) {
		server, log := newAuditedServer(t, newFixtureBackend())
		callTool(t, server, "get_request", map[string]any{"collectionId": "col_pos"})
		entry := log.only(t)
		if entry.Tool != "get_request" || entry.Outcome != outcomeError {
			t.Fatalf("entry = %+v", entry)
		}
	})
	t.Run("a backend failure", func(t *testing.T) {
		backend := newFixtureBackend()
		backend.failWith = errors.New("collection store is locked by another write")
		server, log := newAuditedServer(t, backend)
		callTool(t, server, "list_collections", nil)
		if outcome := log.only(t).Outcome; outcome != outcomeError {
			t.Fatalf("outcome = %q, want %q", outcome, outcomeError)
		}
	})
	t.Run("a panic in a tool", func(t *testing.T) {
		backend := newFixtureBackend()
		backend.panicWith = "nil map write in the adapter"
		server, log := newAuditedServer(t, backend)
		callTool(t, server, "list_collections", nil)
		if outcome := log.only(t).Outcome; outcome != outcomeError {
			t.Fatalf("outcome = %q, want %q", outcome, outcomeError)
		}
	})
}

// An audit that only recorded the calls that reached a tool would show a clean
// history to anyone probing the endpoint, which is the opposite of the point.
func TestAuditRecordsCallsThatNeverReachedATool(t *testing.T) {
	t.Run("an unknown tool", func(t *testing.T) {
		server, log := newAuditedServer(t, newFixtureBackend())
		response := callToolRaw(t, server, "delete_everything", map[string]any{"confirm": "yes"})
		if response.Error == nil || response.Error.Code != codeInvalidParams {
			t.Fatalf("error = %+v, want code %d", response.Error, codeInvalidParams)
		}
		entry := log.only(t)
		if entry.Tool != "delete_everything" || entry.Outcome != outcomeError {
			t.Fatalf("entry = %+v", entry)
		}
		// The arguments the prober sent are the interesting part of the row.
		if !strings.Contains(entry.ArgsSummary, "confirm") {
			t.Errorf("summary lost the arguments: %q", entry.ArgsSummary)
		}
	})
	t.Run("a call with no tool name", func(t *testing.T) {
		server, log := newAuditedServer(t, newFixtureBackend())
		rpcCall(t, server, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"arguments":{"collectionId":"col_pos"}}}`, http.StatusOK)
		entry := log.only(t)
		if entry.Tool != "" || entry.Outcome != outcomeError {
			t.Fatalf("entry = %+v", entry)
		}
		if !strings.Contains(entry.ArgsSummary, "col_pos") {
			t.Errorf("summary lost the arguments: %q", entry.ArgsSummary)
		}
	})
	t.Run("params that are not an object", func(t *testing.T) {
		server, log := newAuditedServer(t, newFixtureBackend())
		rpcCall(t, server, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":"list_collections"}`, http.StatusOK)
		if outcome := log.only(t).Outcome; outcome != outcomeError {
			t.Fatalf("outcome = %q, want %q", outcome, outcomeError)
		}
	})
}

// Discovery is noise: every client makes these on connect, and burying the
// calls that touched the user's data under them would make the panel useless.
func TestAuditIgnoresDiscoveryAndProtocolTraffic(t *testing.T) {
	server, log := newAuditedServer(t, newFixtureBackend())

	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
	} {
		rpcCall(t, server, body, http.StatusOK)
	}
	// A notification gets a 202 and no response at all.
	if status, _ := rpcPost(t, server, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); status != http.StatusAccepted {
		t.Fatalf("notification status = %d", status)
	}

	if entries := log.all(); len(entries) != 0 {
		t.Fatalf("discovery traffic was recorded: %+v", entries)
	}
}

// Without a recorder the server is the Phase 1 read-only posture: it serves
// every call and records nothing, and nothing about that path may panic.
func TestAuditIsOptional(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	if result := callTool(t, server, "list_collections", nil); result.IsError {
		t.Fatalf("list_collections failed: %s", result.Content[0].Text)
	}
	if result := callTool(t, server, "run_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"}); result.IsError {
		t.Fatalf("run_request failed: %s", result.Content[0].Text)
	}
	callToolRaw(t, server, "delete_everything", map[string]any{})
	// Reaching here without a panic is the assertion.
}

func TestSummarizeArgsIsDeterministicAndCompact(t *testing.T) {
	args := toolArgs{
		"requestId":    "req_create",
		"collectionId": "col_pos",
		"limit":        float64(5),
		"variables":    map[string]any{"storeId": "str_42"},
		"dryRun":       true,
		"nothing":      nil,
	}
	summary := summarizeArgs(args)

	// Sorted key order, so the same call always produces the same row and two
	// entries can be compared by eye.
	if got := summarizeArgs(args); got != summary {
		t.Fatalf("two summaries of the same arguments differ:\n%q\n%q", summary, got)
	}
	wantOrder := []string{"collectionId=", "dryRun=", "limit=", "nothing=", "requestId=", "variables="}
	position := -1
	for _, key := range wantOrder {
		found := strings.Index(summary, key)
		if found < 0 {
			t.Fatalf("summary %q is missing %q", summary, key)
		}
		if found <= position {
			t.Fatalf("keys are not in sorted order in %q", summary)
		}
		position = found
	}
	for _, want := range []string{`collectionId="col_pos"`, "limit=5", "dryRun=true", "nothing=null", `variables={"storeId":"str_42"}`} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q is missing %q", summary, want)
		}
	}
	if summarizeArgs(nil) != "" || summarizeArgs(toolArgs{}) != "" {
		t.Error("an argument-less call should summarise as the empty string")
	}
}

// Arguments are attacker-shaped input on a loopback port: an audit that stored
// them verbatim would be a way to fill the user's disk from an MCP client.
func TestSummarizeArgsBoundsWhatOneEntryCanCost(t *testing.T) {
	t.Run("a long value is truncated", func(t *testing.T) {
		summary := summarizeArgs(toolArgs{"collectionId": strings.Repeat("é", 500)})
		if !strings.Contains(summary, truncationMarker) {
			t.Fatalf("a 500-rune value was not marked as truncated: %q", summary)
		}
		// Runes, not bytes: a multi-byte value cut by length would land in the
		// panel as mojibake.
		if runes := len([]rune(summary)); runes > maxAuditValueRunes+len("collectionId=")+len([]rune(truncationMarker)) {
			t.Fatalf("summary is %d runes, want the value capped at %d", runes, maxAuditValueRunes)
		}
		if !strings.HasPrefix(summary, `collectionId="é`) {
			t.Fatalf("summary lost its key or mangled the value: %q", summary)
		}
	})
	t.Run("many values are capped as a whole", func(t *testing.T) {
		args := toolArgs{}
		for index := 0; index < 50; index++ {
			args[string(rune('a'+index%26))+strings.Repeat("x", index)] = strings.Repeat("v", 300)
		}
		summary := summarizeArgs(args)
		if runes := len([]rune(summary)); runes > maxAuditSummaryRunes+len([]rune(truncationMarker)) {
			t.Fatalf("summary is %d runes, want at most %d", runes, maxAuditSummaryRunes)
		}
		if !strings.HasSuffix(summary, truncationMarker) {
			t.Fatalf("a capped summary is not marked as cut: %q", summary)
		}
	})
	t.Run("a short value is left alone", func(t *testing.T) {
		if summary := summarizeArgs(toolArgs{"requestId": "req_create"}); summary != `requestId="req_create"` {
			t.Fatalf("summary = %q", summary)
		}
	})
}

// The summary travels into the app's audit store and is shown to the user, so
// it has to survive the round trip as text.
func TestSummarizeArgsKeepsValuesOnOneLine(t *testing.T) {
	summary := summarizeArgs(toolArgs{"body": "line one\nline two\ttabbed"})
	if strings.ContainsAny(summary, "\n\t") {
		t.Fatalf("summary carries raw control characters: %q", summary)
	}
	if !strings.Contains(summary, `\n`) {
		t.Fatalf("the newline was dropped rather than escaped: %q", summary)
	}
}

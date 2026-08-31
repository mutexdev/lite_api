package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// These tests drive the stdio transport over in-memory pipes — no real stdin,
// no process, no port. What they pin is the framing (one JSON-RPC message per
// line, nothing else on the stream) and the claim the transport is built on:
// that a message means the same thing here as it does over HTTP.

// serveStdio runs one session to completion over the given input and returns
// the lines written to stdout. The reader is finite, so the loop sees EOF and
// returns on its own; a test that hangs here is a transport that does not stop
// when its client does.
func serveStdio(t *testing.T, server *Server, input string) []string {
	t.Helper()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.ServeStdio(ctx, strings.NewReader(input), &out, io.Discard); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	text := out.String()
	if text == "" {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("stdout does not end with a newline, so the last message is unframed: %q", text)
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// decodeStdio decodes one response line, failing loudly rather than letting a
// malformed line be asserted against as a zero value.
func decodeStdio(t *testing.T, line string) testResponse {
	t.Helper()
	var decoded testResponse
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return decoded
}

// TestStdioRoundTripsAnInitializeListAndCall is the whole session an MCP client
// makes on connect, over a pipe: handshake, notification, tool listing, tool
// call. The notification is in the middle on purpose — it must produce no line
// at all, which is only observable when something follows it.
func TestStdioRoundTripsAnInitializeListAndCall(t *testing.T) {
	backend := newFixtureBackend()
	log := &auditLog{}
	server := NewStdio(backend, WithAuditRecorder(log.record))

	lines := serveStdio(t, server, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"call-3","method":"tools/call","params":{"name":"list_collections","arguments":{}}}`,
	}, "\n")+"\n")

	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3 (the notification must produce none): %q", len(lines), lines)
	}

	initialize := decodeStdio(t, lines[0])
	if initialize.Error != nil || string(initialize.ID) != "1" {
		t.Fatalf("initialize = %+v", initialize)
	}
	var handshake initializeResult
	if err := json.Unmarshal(initialize.Result, &handshake); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if handshake.ProtocolVersion != ProtocolVersion || handshake.ServerInfo.Name != ServerName {
		t.Fatalf("handshake = %+v", handshake)
	}

	list := decodeStdio(t, lines[1])
	if list.Error != nil || !strings.Contains(string(list.Result), `"list_collections"`) {
		t.Fatalf("tools/list = %+v", list)
	}

	call := decodeStdio(t, lines[2])
	// The id is echoed byte-for-byte, string form included: JSON-RPC does not
	// let a server decide a client's id was really a number.
	if string(call.ID) != `"call-3"` {
		t.Fatalf("id = %s, want \"call-3\"", call.ID)
	}
	var result callToolResult
	if err := json.Unmarshal(call.Result, &result); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "col_pos") {
		t.Fatalf("tools/call result = %+v", result)
	}

	// The audit is the server's, not the transport's: a call served over a pipe
	// is recorded exactly as one served over the port, and discovery still is
	// not recorded.
	entry := log.only(t)
	if entry.Tool != "list_collections" || entry.Outcome != outcomeOK {
		t.Fatalf("audit entry = %+v", entry)
	}
}

// TestStdioAnswersMalformedLinesAndKeepsServing is the property that makes the
// loop usable by a client that gets one message wrong: garbage is answered with
// a JSON-RPC error and the session continues, rather than the transport
// crashing or desynchronising.
func TestStdioAnswersMalformedLinesAndKeepsServing(t *testing.T) {
	server := NewStdio(newFixtureBackend())

	lines := serveStdio(t, server, strings.Join([]string{
		`not json at all`,
		`{"jsonrpc":"2.0",`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`42`,
		`{"id":9,"method":"ping"}`,
		``, // a blank line is framing noise, not a message
		`{"jsonrpc":"2.0","id":7,"method":"ping"}`,
	}, "\n")+"\n")

	if len(lines) != 6 {
		t.Fatalf("wrote %d lines, want 6 (five faults, one ping; the blank line answers nothing): %q", len(lines), lines)
	}
	for index, want := range []int{codeParseError, codeParseError, codeInvalidRequest, codeInvalidRequest, codeInvalidRequest} {
		response := decodeStdio(t, lines[index])
		if response.Error == nil || response.Error.Code != want {
			t.Fatalf("line %d: error = %+v, want code %d", index, response.Error, want)
		}
	}
	// The one that survived the five before it, which is the point.
	survivor := decodeStdio(t, lines[5])
	if survivor.Error != nil || string(survivor.ID) != "7" {
		t.Fatalf("ping after malformed input = %+v", survivor)
	}
}

// TestStdioRefusesAnOversizeLineWithoutLosingTheStream pins that a message too
// large to keep is answered and DRAINED: the tail of that line must not be read
// back as the next message.
func TestStdioRefusesAnOversizeLineWithoutLosingTheStream(t *testing.T) {
	server := NewStdio(newFixtureBackend())
	huge := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` +
		strings.Repeat("x", maxStdioLineBytes) + `"}}`

	lines := serveStdio(t, server, huge+"\n"+`{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n")

	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2: %q", len(lines), lines)
	}
	refusal := decodeStdio(t, lines[0])
	if refusal.Error == nil || refusal.Error.Code != codeParseError || string(refusal.ID) != "null" {
		t.Fatalf("oversize refusal = %+v (id %s)", refusal.Error, refusal.ID)
	}
	next := decodeStdio(t, lines[1])
	if next.Error != nil || string(next.ID) != "2" {
		t.Fatalf("message after an oversize line = %+v", next)
	}
}

// TestStdioStopsOnCancellationWithoutDrainingTheClient pins the signal path:
// SIGINT cancels ctx, ServeStdio returns, and it does so without waiting for a
// client that has not closed its end. The reader here never reaches EOF, so a
// loop that only stopped at EOF would hang this test.
func TestStdioStopsOnCancellation(t *testing.T) {
	server := NewStdio(newFixtureBackend())
	ctx, cancel := context.WithCancel(context.Background())

	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()

	done := make(chan error, 1)
	go func() { done <- server.ServeStdio(ctx, reader, io.Discard, io.Discard) }()

	if _, err := writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")); err != nil {
		t.Fatalf("write to pipe: %v", err)
	}
	cancel()

	select {
	case err := <-done:
		// Cancellation is an ordinary end of session, not a failure.
		if err != nil {
			t.Fatalf("ServeStdio after cancellation = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeStdio did not return after its context was cancelled")
	}
}

// TestStdioAndHTTPAnswerIdenticallyForTheSameCall is the parity gate the shared
// dispatch exists for. Every message goes down both transports against
// equivalent backends, and the two replies must be the same bytes — not merely
// the same shape, because "the same shape" is what a second implementation
// drifting from the first also produces for a while.
func TestStdioAndHTTPAnswerIdenticallyForTheSameCall(t *testing.T) {
	messages := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_collections","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_requests","arguments":{"collectionId":"col_pos"}}}`,
		// A tool that FAILS: the isError split must match too, not just the
		// successful paths.
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"list_requests","arguments":{"collectionId":"nope"}}}`,
		// A tool that does not exist: a JSON-RPC error, and an audited one.
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"run_request","arguments":{"requestId":"req_create","collectionId":"col_pos"}}}`,
		`not json at all`,
		`{"jsonrpc":"2.0","id":10}`,
	}

	stdioLog, httpLog := &auditLog{}, &auditLog{}
	stdioServer := NewStdio(newFixtureBackend(), WithAuditRecorder(stdioLog.record))
	httpServer := newTestServer(t, newFixtureBackend(), WithAuditRecorder(httpLog.record))

	for _, message := range messages {
		lines := serveStdio(t, stdioServer, message+"\n")
		if len(lines) != 1 {
			t.Fatalf("%s: stdio wrote %d lines, want 1: %q", message, len(lines), lines)
		}
		// The HTTP status is the transport's own business and is asserted only
		// as "this exchange happened"; the BYTES are what has to match.
		status, body := rpcPost(t, httpServer, message)
		if status != http.StatusOK && status != http.StatusBadRequest {
			t.Fatalf("%s: unexpected HTTP status %d", message, status)
		}
		if lines[0] != body {
			t.Fatalf("%s:\n stdio = %s\n  http = %s", message, lines[0], body)
		}
	}

	// The audit must not differ either: same calls recorded, same outcomes, in
	// the same order. Durations are wall time and are not compared.
	stdioEntries, httpEntries := stdioLog.all(), httpLog.all()
	if len(stdioEntries) != len(httpEntries) {
		t.Fatalf("audit lengths differ: stdio %d, http %d", len(stdioEntries), len(httpEntries))
	}
	if len(stdioEntries) == 0 {
		t.Fatal("no tool calls were audited on either transport, so this proves nothing")
	}
	for index := range stdioEntries {
		if stdioEntries[index].Tool != httpEntries[index].Tool ||
			stdioEntries[index].Outcome != httpEntries[index].Outcome ||
			stdioEntries[index].ArgsSummary != httpEntries[index].ArgsSummary {
			t.Fatalf("audit entry %d differs:\n stdio = %+v\n  http = %+v", index, stdioEntries[index], httpEntries[index])
		}
	}
}

// TestStdioNeedsNoToken states the transport's security posture as an
// assertion: a stdio server carries no credential, and the credential check is
// the HTTP handler's alone. If a token ever becomes required here, this fails
// and the reasoning at the top of stdio.go has to be revisited rather than
// quietly worked around.
func TestStdioNeedsNoToken(t *testing.T) {
	server := NewStdio(newFixtureBackend())
	if server.token != "" {
		t.Fatalf("stdio server carries a token %q", server.token)
	}
	lines := serveStdio(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}
	if response := decodeStdio(t, lines[0]); response.Error != nil {
		t.Fatalf("tools/list over an unauthenticated pipe failed: %+v", response.Error)
	}
}

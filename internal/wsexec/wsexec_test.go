// US-064 — WebSocket send preparation.
//
// These exist because a negative control found the frame opcode untested:
// making FramePayload return websocket.TextMessage for a binary message failed
// no test at all. That is a protocol-level bug with a plausible-looking
// symptom -- the frame still goes out, the server either rejects it or reads
// base64 text as if it were the payload, and nothing on this side reports an
// error.
//
// This is also the part of a WebSocket session worth testing without a socket:
// what gets sent, and how, is decided entirely before anything is dialled.
package wsexec

import (
	"encoding/base64"
	"testing"

	"LiteAPI/internal/types"

	"github.com/gorilla/websocket"
)

func TestFramePayloadSendsBinaryAsABinaryFrame(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xff, 0xfe}
	message := OutboundMessage{Type: "binary", Content: base64.StdEncoding.EncodeToString(payload)}

	frameType, data := FramePayload(message)
	if frameType != websocket.BinaryMessage {
		t.Fatalf("binary message went out as frame type %d, want BinaryMessage (%d)", frameType, websocket.BinaryMessage)
	}
	if string(data) != string(payload) {
		t.Fatalf("payload was not base64-decoded: got %v, want %v", data, payload)
	}
}

// Undecodable base64 falls back to sending the text as-is rather than dropping
// the message: the user sees what they typed arrive, which is a far easier
// thing to diagnose than silence.
func TestFramePayloadFallsBackToTextWhenBase64IsInvalid(t *testing.T) {
	message := OutboundMessage{Type: "binary", Content: "not!valid!base64"}

	frameType, data := FramePayload(message)
	if frameType != websocket.TextMessage {
		t.Fatalf("invalid base64 should fall back to a text frame, got %d", frameType)
	}
	if string(data) != "not!valid!base64" {
		t.Fatalf("fallback should send the raw content, got %q", data)
	}
}

func TestFramePayloadSendsTextAsATextFrame(t *testing.T) {
	frameType, data := FramePayload(OutboundMessage{Type: "text", Content: `{"a":1}`})
	if frameType != websocket.TextMessage {
		t.Fatalf("got frame type %d, want TextMessage", frameType)
	}
	if string(data) != `{"a":1}` {
		t.Fatalf("got %q", data)
	}
}

func TestNormalizeMessageTypeMapsAliasesAndDefaults(t *testing.T) {
	for input, want := range map[string]string{
		"json": "json", "JSON": "json", "  xml  ": "xml",
		"binary": "binary", "bin": "binary", "BIN": "binary",
		"text": "text", "": "text", "something-else": "text",
	} {
		if got := NormalizeMessageType(input); got != want {
			t.Errorf("NormalizeMessageType(%q) = %q, want %q", input, got, want)
		}
	}
}

// A ws:// or wss:// URL must survive untouched, an http(s) one is upgraded, and
// a bare host gets a scheme -- dialling any of the other shapes fails.
func TestTargetURLNormalisesTheScheme(t *testing.T) {
	for input, want := range map[string]string{
		"ws://example.test/socket":    "ws://example.test/socket",
		"wss://example.test/socket":   "wss://example.test/socket",
		"http://example.test/socket":  "ws://example.test/socket",
		"https://example.test/socket": "wss://example.test/socket",
		"example.test/socket":         "ws://example.test/socket",
	} {
		got := TargetURL(types.RequestItem{URL: input}, nil)
		if got != want {
			t.Errorf("TargetURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTargetURLExpandsVariables(t *testing.T) {
	item := types.RequestItem{URL: "{{host}}/socket"}
	if got := TargetURL(item, map[string]string{"host": "wss://example.test"}); got != "wss://example.test/socket" {
		t.Fatalf("got %q", got)
	}
}

// Only selected messages are sent WHEN ANY is selected. If none is, all go --
// otherwise clearing every checkbox would silently send nothing.
func TestOutboundMessagesHonoursSelection(t *testing.T) {
	item := types.RequestItem{WSMessages: []types.WSMessage{
		{Name: "a", Type: "json", Content: "1", Selected: false},
		{Name: "b", Type: "json", Content: "2", Selected: true},
	}}
	got := OutboundMessages(item, nil)
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("with one selected, only it should be sent; got %+v", got)
	}

	item.WSMessages[1].Selected = false
	got = OutboundMessages(item, nil)
	if len(got) != 2 {
		t.Fatalf("with none selected, all should be sent; got %d", len(got))
	}
}

func TestOutboundMessagesNamesUnnamedOnesByPosition(t *testing.T) {
	item := types.RequestItem{WSMessages: []types.WSMessage{{Content: "x"}, {Name: "  ", Content: "y"}}}
	got := OutboundMessages(item, nil)
	if len(got) != 2 || got[0].Name != "message 1" || got[1].Name != "message 2" {
		t.Fatalf("got %+v", got)
	}
}

func TestOutboundMessagesExpandsVariablesInContent(t *testing.T) {
	item := types.RequestItem{WSMessages: []types.WSMessage{{Name: "a", Content: `{"id":"{{id}}"}`}}}
	got := OutboundMessages(item, map[string]string{"id": "42"})
	if len(got) != 1 || got[0].Content != `{"id":"42"}` {
		t.Fatalf("got %+v", got)
	}
}

func TestHeadersSkipDisabledRowsAndApplyBearerAuth(t *testing.T) {
	item := types.RequestItem{
		URL: "wss://example.test/s",
		Headers: []types.KeyValue{
			{Name: "X-On", Value: "yes", Enabled: true},
			{Name: "X-Off", Value: "no", Enabled: false},
		},
		Auth: types.AuthConfig{Mode: "bearer", Token: "{{token}}"},
	}
	headers := Headers(item, map[string]string{"token": "abc"})

	if headers.Get("X-On") != "yes" {
		t.Error("enabled header missing")
	}
	if headers.Get("X-Off") != "" {
		t.Error("disabled header was sent")
	}
	if got := headers.Get("Authorization"); got != "Bearer abc" {
		t.Fatalf("Authorization = %q, want Bearer abc", got)
	}
}

func TestKeepAliveIntervalIsZeroWhenDisabled(t *testing.T) {
	if got := KeepAliveInterval(types.RequestSettings{KeepAliveInterval: 0}); got != 0 {
		t.Fatalf("got %v, want 0", got)
	}
	if got := KeepAliveInterval(types.RequestSettings{KeepAliveInterval: 250}); got.Milliseconds() != 250 {
		t.Fatalf("got %v, want 250ms", got)
	}
}

func TestOutboundMessageAtRejectsAnOutOfRangeIndex(t *testing.T) {
	item := types.RequestItem{WSMessages: []types.WSMessage{{Name: "a", Content: "1", Selected: true}}}
	if _, err := OutboundMessageAt(item, nil, 5); err == nil {
		t.Fatal("an out-of-range index must be an error, not a silently empty frame")
	}
}

// Package wsexec prepares a WebSocket session: the target URL, the handshake
// headers, the keep-alive interval, and the outbound frames with their opcodes.
//
// US-064. Dialling and the live session stay on *App -- those hold the
// connection and the per-session goroutines. This is the part that decides
// WHAT to send, which is exactly the part worth testing without a socket.
package wsexec

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/urlbuild"
	"github.com/mutexdev/lite_api/internal/wsmessage"

	"github.com/gorilla/websocket"
)

type OutboundMessage struct {
	Name    string
	Type    string
	Content string
}

func OutboundMessages(item types.RequestItem, vars map[string]string) []OutboundMessage {
	if len(item.WSMessages) > 0 {
		hasSelected := false
		for _, message := range item.WSMessages {
			if message.Selected {
				hasSelected = true
				break
			}
		}
		result := make([]OutboundMessage, 0, len(item.WSMessages))
		for index, message := range item.WSMessages {
			if hasSelected && !message.Selected {
				continue
			}
			name := strings.TrimSpace(message.Name)
			if name == "" {
				name = fmt.Sprintf("message %d", index+1)
			}
			result = append(result, OutboundMessage{
				Name:    name,
				Type:    NormalizeMessageType(message.Type),
				Content: interp.Interpolate(message.Content, vars),
			})
		}
		return result
	}
	if message := MessageBody(item.Body, vars); message != "" {
		return []OutboundMessage{{Name: "message 1", Type: "text", Content: message}}
	}
	return nil
}

func OutboundMessageAt(item types.RequestItem, vars map[string]string, index int) (OutboundMessage, error) {
	if len(item.WSMessages) > 0 {
		if index < 0 || index >= len(item.WSMessages) {
			return OutboundMessage{}, fmt.Errorf("WebSocket message %d not found", index+1)
		}
		message := item.WSMessages[index]
		name := strings.TrimSpace(message.Name)
		if name == "" {
			name = fmt.Sprintf("message %d", index+1)
		}
		return OutboundMessage{
			Name:    name,
			Type:    NormalizeMessageType(message.Type),
			Content: interp.Interpolate(message.Content, vars),
		}, nil
	}
	if index > 0 {
		return OutboundMessage{}, fmt.Errorf("WebSocket message %d not found", index+1)
	}
	if message := MessageBody(item.Body, vars); message != "" {
		return OutboundMessage{Name: "message 1", Type: "text", Content: message}, nil
	}
	return OutboundMessage{}, errors.New("WebSocket message is empty")
}

func TargetURL(item types.RequestItem, vars map[string]string) string {
	targetURL := urlbuild.RequestURLWithParams(item.URL, item.Params, item.PathParams, vars)
	if strings.HasPrefix(targetURL, "http://") {
		targetURL = "ws://" + strings.TrimPrefix(targetURL, "http://")
	}
	if strings.HasPrefix(targetURL, "https://") {
		targetURL = "wss://" + strings.TrimPrefix(targetURL, "https://")
	}
	if !strings.HasPrefix(targetURL, "ws://") && !strings.HasPrefix(targetURL, "wss://") {
		targetURL = "ws://" + targetURL
	}
	return targetURL
}

func Headers(item types.RequestItem, vars map[string]string) http.Header {
	headers := http.Header{}
	for _, header := range item.Headers {
		if header.Enabled && header.Name != "" {
			headers.Set(interp.Interpolate(header.Name, vars), interp.Interpolate(header.Value, vars))
		}
	}
	applyAuth(headers, item.Auth, vars, TargetURL(item, vars))
	return headers
}

func KeepAliveInterval(settings types.RequestSettings) time.Duration {
	if settings.KeepAliveInterval <= 0 {
		return 0
	}
	return time.Duration(settings.KeepAliveInterval) * time.Millisecond
}

func FramePayload(message OutboundMessage) (int, []byte) {
	if message.Type == "binary" {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(message.Content)); err == nil {
			return websocket.BinaryMessage, decoded
		}
	}
	return websocket.TextMessage, []byte(message.Content)
}

func MessageTypeName(messageType int) string {
	switch messageType {
	case websocket.BinaryMessage:
		return "binary"
	case websocket.TextMessage:
		return "text"
	default:
		return strconv.Itoa(messageType)
	}
}

func applyAuth(headers http.Header, auth types.AuthConfig, vars map[string]string, targetURL string) {
	switch auth.Mode {
	case "basic":
		req, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err == nil {
			req.SetBasicAuth(interp.Interpolate(auth.Username, vars), interp.Interpolate(auth.Password, vars))
			headers.Set("Authorization", req.Header.Get("Authorization"))
		}
	case "bearer", "oauth2":
		token := interp.Interpolate(auth.Token, vars)
		if token != "" {
			headers.Set("Authorization", "Bearer "+token)
		}
	case "apikey":
		if auth.APILocation != "query" && auth.APIKey != "" {
			headers.Set(interp.Interpolate(auth.APIKey, vars), interp.Interpolate(auth.APIValue, vars))
		}
	}
}

// The message vocabulary lives in internal/wsmessage so that the file-format
// readers can use it without importing this package, which dials sockets.
//
// Wrappers rather than vars, for the same reason as in internal/codegen: a
// package-level var re-export is a mutable global and an extra init entry,
// where a function is neither.

func NormalizeMessageType(value string) string {
	return wsmessage.NormalizeMessageType(value)
}

func MessageBody(body types.RequestBody, vars map[string]string) string {
	return wsmessage.MessageBody(body, vars)
}

// Streaming messages for gRPC and WebSocket sessions.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type GrpcMessage struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type WSMessage struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Selected bool   `json:"selected"`
}

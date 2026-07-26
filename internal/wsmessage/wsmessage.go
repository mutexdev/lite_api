// Package wsmessage normalizes a WebSocket message's type and extracts its
// body.
//
// These lived in internal/wsexec, the package that actually dials sockets,
// which meant internal/store/bru and internal/store/yamlstore — two FILE FORMAT
// readers — imported a WebSocket executor in order to read a field off disk.
// Neither opens a socket. Splitting the two apart lets a format reader depend
// on the message vocabulary without depending on the transport.
package wsmessage

import (
	"strings"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/types"
)

// NormalizeMessageType maps a stored or user-entered type onto one of the four
// the executor understands.
//
// The default is "text", not an error and not the empty string. This is called
// while READING A FILE, so an unrecognised value means a collection written by
// a newer build or edited by hand — and refusing to open it would cost the user
// every other request in the file. Text is the safe reading: it sends what is
// there, byte for byte, without claiming a structure the content may not have.
func NormalizeMessageType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "json":
		return "json"
	case "xml":
		return "xml"
	case "binary", "bin":
		return "binary"
	default:
		return "text"
	}
}

// MessageBody returns the body for a mode, interpolated.
//
// Every branch interpolates, including the default. A mode this does not know
// still gets its variables expanded, because a message going out with a literal
// {{token}} in it is worse than one whose mode was guessed: the server sees a
// malformed payload rather than an unexpected content type.
//
// The empty mode reads as text for the same reason NormalizeMessageType
// defaults there — a message saved before the field existed has one.
func MessageBody(body types.RequestBody, vars map[string]string) string {
	switch body.Mode {
	case "json":
		return interp.Interpolate(body.JSON, vars)
	case "xml":
		return interp.Interpolate(body.XML, vars)
	case "text", "sparql", "":
		return interp.Interpolate(body.Text, vars)
	default:
		return interp.Interpolate(body.Text, vars)
	}
}

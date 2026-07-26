package main

// US-058 — the response Visualizer.
//
// A script calls pm.visualizer.set(template, data) and the response grows a
// Visualizer tab rendering that template. The template is authored by whoever
// wrote the collection, and the data comes from whatever the server returned —
// so this renders ATTACKER-INFLUENCED CONTENT and the containment is the
// feature, not a detail around it.
//
// Three independent layers, each of which would be sufficient on its own for
// some attacks and none of which is sufficient for all:
//
//  1. sandbox="allow-scripts" WITHOUT allow-same-origin (set on the iframe by
//     the frontend). This is the pairing that matters. Granting both puts the
//     frame in the parent's origin, where it can read localStorage, reach
//     document.cookie and script the app — the sandbox attribute then buys
//     nothing at all. Alone, allow-scripts gives an opaque origin.
//
//  2. A strict CSP inside the document: default-src 'none' means the template
//     cannot fetch, cannot load an image from a remote host, cannot open a
//     WebSocket. Without it a template could exfiltrate the very response it
//     was handed by encoding it into an image URL, and the sandbox would not
//     stop that — an opaque origin can still make requests.
//
//  3. HTML escaping in interpolation, so response data cannot close a tag and
//     inject markup even inside the frame. Containment is not a licence to let
//     the frame's own DOM be hijacked: the template author's markup and the
//     server's data are different trust levels.

import (
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/visualizer"
)

// VisualizerPayload moved to internal/types: Response embeds it, and a type
// that a moved type embeds has to move too or the packages point both ways.
type VisualizerPayload = types.VisualizerPayload

// scripting.VisualizerTemplateLimit bounds what a script may hand over. A template is
// held in memory, persisted with the response and injected into a document; an
// unbounded one is a memory and state.json problem before it is a rendering
// one.

// scripting.VisualizerDataLimit bounds the data island for the same reason.

// VisualizerDocument returns the sandboxed srcdoc for a response's visualizer,
// or "" when the response set none.
//
// Built here rather than in the frontend so the CSP and the escaping are
// covered by Go tests. The frontend supplies only the sandbox attribute, whose
// required value is visualizer.Sandbox and is asserted by a test of its own.
func (a *App) VisualizerDocument(collectionID, itemID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	item, err := a.findItemLocked(collectionID, itemID)
	if err != nil {
		return "", err
	}
	if item.Response == nil || item.Response.Visualizer == nil {
		return "", nil
	}
	return visualizer.BuildDocument(*item.Response.Visualizer), nil
}

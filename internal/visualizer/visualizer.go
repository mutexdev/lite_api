package visualizer

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"
)

// visualizerCSP is deliberately restrictive.
//
// 'unsafe-inline' appears for script and style because the whole point is that
// the template's own inline markup runs; there is no external file to point a
// nonce at. That is safe ONLY because default-src 'none' denies every
// destination — the inline script can compute, but it cannot talk to anything.
const visualizerCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; form-action 'none'; base-uri 'none'"

// Sandbox is the iframe sandbox attribute the frontend must use.
//
// Exported and asserted on by a test so the value cannot drift: adding
// allow-same-origin here would silently undo the whole containment, and it is
// the kind of change that looks harmless in a diff.
const Sandbox = "allow-scripts"

// BuildDocument returns the complete srcdoc for the iframe.
func BuildDocument(payload types.VisualizerPayload) string {
	rendered := renderVisualizerTemplate(payload.Template, decodeVisualizerData(payload.Data))

	var b strings.Builder
	b.WriteString("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"" + html.EscapeString(visualizerCSP) + "\">\n")
	// A minimal, neutral base so a template that styles nothing is still
	// legible, and so it inherits neither the app's stylesheet nor its fonts —
	// the frame has no access to either.
	b.WriteString("<style>body{font:13px/1.5 system-ui,sans-serif;margin:12px;color:#111;background:#fff}" +
		"table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:4px 8px}" +
		"@media (prefers-color-scheme:dark){body{color:#eee;background:#1b1b1b}td,th{border-color:#444}}</style>\n")
	b.WriteString("</head>\n<body>\n")
	b.WriteString(rendered)
	b.WriteString("\n</body>\n</html>")
	return b.String()
}

func decodeVisualizerData(raw string) interface{} {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		// Malformed data is not fatal: the template still renders, with the
		// placeholders empty. Failing the whole tab would hide a template that
		// is otherwise fine because one field was bad.
		return map[string]interface{}{}
	}
	return value
}

// renderVisualizerTemplate implements a BOUNDED Handlebars subset:
// {{path}}, {{{path}}}, {{#each path}}…{{/each}} and {{#if path}}…{{else}}…{{/if}},
// plus {{this}} and {{@index}} inside an each.
//
// Bounded on purpose, and the boundary is documented in the UI rather than
// guessed at: a partial Handlebars that silently ignores helpers it does not
// know renders a plausible-but-wrong page, which is worse than a visible gap.
// Anything unrecognised is left in place so the author can see it did not
// resolve.
func renderVisualizerTemplate(template string, data interface{}) string {
	return renderVisualizerNodes(template, data, data, 0)
}

// visualizerMaxDepth stops a self-referential structure from recursing forever.
const visualizerMaxDepth = 32

func renderVisualizerNodes(template string, scope, root interface{}, depth int) string {
	if depth > visualizerMaxDepth {
		return ""
	}

	var out strings.Builder
	cursor := 0
	for cursor < len(template) {
		open := strings.Index(template[cursor:], "{{")
		if open < 0 {
			out.WriteString(template[cursor:])
			break
		}
		open += cursor
		out.WriteString(template[cursor:open])

		close := strings.Index(template[open:], "}}")
		if close < 0 {
			// An unterminated tag is emitted literally rather than swallowing
			// the rest of the template.
			out.WriteString(template[open:])
			break
		}
		close += open

		// Triple-brace first: {{{x}}} must not be read as {{ {x} }}.
		if strings.HasPrefix(template[open:], "{{{") {
			if end := strings.Index(template[open:], "}}}"); end >= 0 {
				expr := strings.TrimSpace(template[open+3 : open+end])
				out.WriteString(visualizerValueString(lookupVisualizerPath(expr, scope, root)))
				cursor = open + end + 3
				continue
			}
		}

		expr := strings.TrimSpace(template[open+2 : close])
		switch {
		case strings.HasPrefix(expr, "#each "):
			body, next, ok := visualizerBlock(template, open, "each")
			if !ok {
				out.WriteString(template[open : close+2])
				cursor = close + 2
				continue
			}
			list := lookupVisualizerPath(strings.TrimSpace(strings.TrimPrefix(expr, "#each ")), scope, root)
			for index, item := range visualizerIterable(list) {
				out.WriteString(renderVisualizerNodes(visualizerWithIndex(body, index), item, root, depth+1))
			}
			cursor = next
		case strings.HasPrefix(expr, "#if "):
			body, next, ok := visualizerBlock(template, open, "if")
			if !ok {
				out.WriteString(template[open : close+2])
				cursor = close + 2
				continue
			}
			truthy, falsy := visualizerSplitElse(body)
			if visualizerTruthy(lookupVisualizerPath(strings.TrimSpace(strings.TrimPrefix(expr, "#if ")), scope, root)) {
				out.WriteString(renderVisualizerNodes(truthy, scope, root, depth+1))
			} else {
				out.WriteString(renderVisualizerNodes(falsy, scope, root, depth+1))
			}
			cursor = next
		case strings.HasPrefix(expr, "#") || strings.HasPrefix(expr, "/"):
			// An unsupported block helper is left visible rather than dropped,
			// so a template using one shows that it did not render instead of
			// quietly losing a section.
			out.WriteString(template[open : close+2])
			cursor = close + 2
		default:
			// The default escapes. Response data is not markup, and a value
			// containing </td><script> must not become markup inside the
			// frame — containment is not a licence to let the frame's own DOM
			// be hijacked.
			out.WriteString(html.EscapeString(visualizerValueString(lookupVisualizerPath(expr, scope, root))))
			cursor = close + 2
		}
	}
	return out.String()
}

// visualizerBlock finds the body of a block helper opening at `open`, matching
// nested blocks of the same name so an inner {{/each}} does not close an outer
// one.
func visualizerBlock(template string, open int, name string) (body string, next int, ok bool) {
	openTag := "{{#" + name + " "
	closeTag := "{{/" + name + "}}"

	bodyStart := strings.Index(template[open:], "}}")
	if bodyStart < 0 {
		return "", 0, false
	}
	bodyStart += open + 2

	depth := 1
	cursor := bodyStart
	for {
		nextOpen := strings.Index(template[cursor:], openTag)
		nextClose := strings.Index(template[cursor:], closeTag)
		if nextClose < 0 {
			return "", 0, false
		}
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			cursor += nextOpen + len(openTag)
			continue
		}
		depth--
		if depth == 0 {
			return template[bodyStart : cursor+nextClose], cursor + nextClose + len(closeTag), true
		}
		cursor += nextClose + len(closeTag)
	}
}

// visualizerSplitElse splits an if-body at its own {{else}}, ignoring one that
// belongs to a nested block.
func visualizerSplitElse(body string) (truthy, falsy string) {
	depth := 0
	cursor := 0
	for cursor < len(body) {
		open := strings.Index(body[cursor:], "{{")
		if open < 0 {
			break
		}
		open += cursor
		close := strings.Index(body[open:], "}}")
		if close < 0 {
			break
		}
		close += open
		expr := strings.TrimSpace(body[open+2 : close])
		switch {
		case strings.HasPrefix(expr, "#"):
			depth++
		case strings.HasPrefix(expr, "/"):
			depth--
		case expr == "else" && depth == 0:
			return body[:open], body[close+2:]
		}
		cursor = close + 2
	}
	return body, ""
}

// visualizerWithIndex substitutes {{@index}} before the body is rendered, since
// it is positional rather than a path lookup.
func visualizerWithIndex(body string, index int) string {
	if !strings.Contains(body, "{{@index}}") {
		return body
	}
	return strings.ReplaceAll(body, "{{@index}}", fmt.Sprintf("%d", index))
}

func visualizerIterable(value interface{}) []interface{} {
	switch typed := value.(type) {
	case []interface{}:
		return typed
	case nil:
		return nil
	default:
		// A non-list is iterated once, matching Handlebars' behaviour for a
		// single object, so {{#each user}} over an object is not silently empty.
		return []interface{}{typed}
	}
}

func visualizerTruthy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case []interface{}:
		return len(typed) > 0
	case map[string]interface{}:
		return len(typed) > 0
	default:
		return true
	}
}

// lookupVisualizerPath resolves a dotted path against the current scope,
// falling back to the root so a template inside an {{#each}} can still reach
// top-level values.
func lookupVisualizerPath(path string, scope, root interface{}) interface{} {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if path == "this" || path == "." {
		return scope
	}
	if value, ok := resolveVisualizerPath(path, scope); ok {
		return value
	}
	if value, ok := resolveVisualizerPath(path, root); ok {
		return value
	}
	return nil
}

func resolveVisualizerPath(path string, value interface{}) (interface{}, bool) {
	current := value
	for _, segment := range strings.Split(path, ".") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, false
		}
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func visualizerValueString(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		// Same reasoning as the runner data files: %v renders 1000000 as
		// 1e+06, and an id shown in scientific notation is wrong on screen.
		return trimFloatString(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}

func trimFloatString(value float64) string {
	text := fmt.Sprintf("%f", value)
	text = strings.TrimRight(text, "0")
	return strings.TrimSuffix(text, ".")
}

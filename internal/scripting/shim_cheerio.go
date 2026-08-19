package scripting

// The sandbox cheerio (HTML query) shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/dop251/goja"
)

func newScriptCheerioObject(runtime *goja.Runtime) goja.Value {
	module := runtime.NewObject()
	load := func(call goja.FunctionCall) goja.Value {
		if goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
			panic(runtime.NewTypeError("cheerio.load expects an HTML string"))
		}
		doc, err := xhtml.Parse(strings.NewReader(call.Argument(0).String()))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return newScriptCheerioLoadedFunction(runtime, doc)
	}
	_ = module.Set("load", load)
	_ = module.Set("default", module)
	return module
}

func newScriptCheerioLoadedFunction(runtime *goja.Runtime, doc *xhtml.Node) goja.Value {
	loaded := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		selector := ""
		if !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			selector = call.Argument(0).String()
		}
		return newScriptCheerioSelectionObject(runtime, scriptCheerioSelect(doc, selector))
	})
	object := loaded.ToObject(runtime)
	_ = object.Set("html", func() string {
		return scriptCheerioRenderNode(doc)
	})
	_ = object.Set("root", func() goja.Value {
		return newScriptCheerioSelectionObject(runtime, []*xhtml.Node{doc})
	})
	return loaded
}

func newScriptCheerioSelectionObject(runtime *goja.Runtime, nodes []*xhtml.Node) goja.Value {
	selection := runtime.NewObject()
	_ = selection.Set("length", len(nodes))
	_ = selection.Set("text", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) {
			var b strings.Builder
			for _, node := range nodes {
				scriptCheerioText(node, &b)
			}
			return runtime.ToValue(b.String())
		}
		text := call.Argument(0).String()
		for _, node := range nodes {
			scriptCheerioSetText(node, text)
		}
		return selection
	})
	_ = selection.Set("addClass", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
			return selection
		}
		classes := strings.Fields(call.Argument(0).String())
		for _, node := range nodes {
			scriptCheerioAddClass(node, classes)
		}
		return selection
	})
	_ = selection.Set("attr", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) || len(nodes) == 0 {
			return goja.Undefined()
		}
		name := call.Argument(0).String()
		if len(call.Arguments) == 1 || goja.IsUndefined(call.Argument(1)) {
			if value, ok := scriptCheerioAttr(nodes[0], name); ok {
				return runtime.ToValue(value)
			}
			return goja.Undefined()
		}
		value := call.Argument(1).String()
		for _, node := range nodes {
			scriptCheerioSetAttr(node, name, value)
		}
		return selection
	})
	_ = selection.Set("html", func() goja.Value {
		if len(nodes) == 0 {
			return goja.Null()
		}
		var b strings.Builder
		for child := nodes[0].FirstChild; child != nil; child = child.NextSibling {
			b.WriteString(scriptCheerioRenderNode(child))
		}
		return runtime.ToValue(b.String())
	})
	for index, node := range nodes {
		_ = selection.Set(strconv.Itoa(index), scriptCheerioNodeObject(runtime, node))
	}
	return selection
}

func scriptCheerioNodeObject(runtime *goja.Runtime, node *xhtml.Node) goja.Value {
	object := runtime.NewObject()
	_ = object.Set("type", strings.ToLower(node.Type.String()))
	_ = object.Set("name", node.Data)
	return object
}

type scriptCheerioSelector struct {
	tag     string
	id      string
	classes []string
}

func scriptCheerioSelect(root *xhtml.Node, selector string) []*xhtml.Node {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	parsed := parseScriptCheerioSelector(selector)
	nodes := []*xhtml.Node{}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if scriptCheerioMatches(node, parsed) {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

func parseScriptCheerioSelector(selector string) scriptCheerioSelector {
	parsed := scriptCheerioSelector{}
	rest := selector
	if hash := strings.Index(rest, "#"); hash >= 0 {
		parsed.tag = strings.TrimSpace(rest[:hash])
		rest = rest[hash+1:]
		if dot := strings.Index(rest, "."); dot >= 0 {
			parsed.id = rest[:dot]
			rest = rest[dot:]
		} else {
			parsed.id = rest
			rest = ""
		}
	}
	if parsed.tag == "" && strings.HasPrefix(rest, ".") {
		parsed.classes = strings.FieldsFunc(strings.TrimPrefix(rest, "."), func(r rune) bool { return r == '.' })
		return parsed
	}
	parts := strings.Split(rest, ".")
	if parsed.tag == "" {
		parsed.tag = strings.TrimSpace(parts[0])
	}
	for _, className := range parts[1:] {
		if strings.TrimSpace(className) != "" {
			parsed.classes = append(parsed.classes, strings.TrimSpace(className))
		}
	}
	return parsed
}

func scriptCheerioMatches(node *xhtml.Node, selector scriptCheerioSelector) bool {
	if node.Type != xhtml.ElementNode {
		return false
	}
	if selector.tag != "" && !strings.EqualFold(node.Data, selector.tag) {
		return false
	}
	if selector.id != "" {
		value, ok := scriptCheerioAttr(node, "id")
		if !ok || value != selector.id {
			return false
		}
	}
	if len(selector.classes) > 0 {
		value, ok := scriptCheerioAttr(node, "class")
		if !ok {
			return false
		}
		classes := strings.Fields(value)
		for _, required := range selector.classes {
			if !scriptStringSliceContains(classes, required) {
				return false
			}
		}
	}
	return true
}

func scriptCheerioText(node *xhtml.Node, b *strings.Builder) {
	if node == nil {
		return
	}
	if node.Type == xhtml.TextNode {
		b.WriteString(node.Data)
		return
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		scriptCheerioText(child, b)
	}
}

func scriptCheerioSetText(node *xhtml.Node, text string) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		node.RemoveChild(child)
		child = next
	}
	node.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: text})
}

func scriptCheerioAddClass(node *xhtml.Node, classes []string) {
	if node == nil || node.Type != xhtml.ElementNode || len(classes) == 0 {
		return
	}
	current, ok := scriptCheerioAttr(node, "class")
	existing := strings.Fields(current)
	for _, className := range classes {
		if className != "" && !scriptStringSliceContains(existing, className) {
			existing = append(existing, className)
		}
	}
	if ok {
		scriptCheerioSetAttr(node, "class", strings.Join(existing, " "))
		return
	}
	node.Attr = append(node.Attr, xhtml.Attribute{Key: "class", Val: strings.Join(existing, " ")})
}

func scriptCheerioAttr(node *xhtml.Node, name string) (string, bool) {
	if node == nil {
		return "", false
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val, true
		}
	}
	return "", false
}

func scriptCheerioSetAttr(node *xhtml.Node, name, value string) {
	if node == nil || node.Type != xhtml.ElementNode {
		return
	}
	for index := range node.Attr {
		if strings.EqualFold(node.Attr[index].Key, name) {
			node.Attr[index].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, xhtml.Attribute{Key: name, Val: value})
}

func scriptCheerioRenderNode(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	var b strings.Builder
	if err := xhtml.Render(&b, node); err != nil {
		return ""
	}
	return b.String()
}

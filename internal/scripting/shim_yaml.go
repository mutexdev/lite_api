package scripting

// The sandbox node:yaml shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"fmt"
	"strconv"

	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"
)

func newScriptYAMLObject(runtime *goja.Runtime) goja.Value {
	module := runtime.NewObject()
	_ = module.Set("parse", func(call goja.FunctionCall) goja.Value {
		text := scriptYAMLStringArgument(runtime, call.Argument(0), "YAML.parse")
		var raw interface{}
		if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(scriptYAMLNormalize(raw))
	})
	_ = module.Set("stringify", func(call goja.FunctionCall) goja.Value {
		value := scriptYAMLNormalize(call.Argument(0).Export())
		data, err := yaml.Marshal(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(string(data))
	})
	_ = module.Set("parseDocument", func(call goja.FunctionCall) goja.Value {
		text := scriptYAMLStringArgument(runtime, call.Argument(0), "YAML.parseDocument")
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			panic(runtime.NewGoError(err))
		}
		scriptYAMLUpdateLineCounter(runtime, call.Argument(1), text)
		return newScriptYAMLDocumentObject(runtime, text, &doc)
	})
	_ = module.Set("LineCounter", func(call goja.ConstructorCall) *goja.Object {
		return newScriptYAMLLineCounterObject(runtime)
	})
	_ = module.Set("isSeq", func(value goja.Value) bool {
		return scriptYAMLNodeKind(runtime, value) == "seq"
	})
	_ = module.Set("isMap", func(value goja.Value) bool {
		return scriptYAMLNodeKind(runtime, value) == "map"
	})
	_ = module.Set("isScalar", func(value goja.Value) bool {
		return scriptYAMLNodeKind(runtime, value) == "scalar"
	})
	_ = module.Set("default", module)
	return module
}

func scriptYAMLStringArgument(runtime *goja.Runtime, value goja.Value, name string) string {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		panic(runtime.NewTypeError(name + " expects a string"))
	}
	if text, ok := value.Export().(string); ok {
		return text
	}
	panic(runtime.NewTypeError(name + " expects a string"))
}

func scriptYAMLNormalize(value interface{}) interface{} {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]interface{}:
		out := map[string]interface{}{}
		for key, child := range typed {
			out[key] = scriptYAMLNormalize(child)
		}
		return out
	case map[interface{}]interface{}:
		out := map[string]interface{}{}
		for key, child := range typed {
			out[fmt.Sprint(key)] = scriptYAMLNormalize(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = scriptYAMLNormalize(child)
		}
		return out
	default:
		return typed
	}
}

func scriptYAMLUpdateLineCounter(runtime *goja.Runtime, options goja.Value, content string) {
	if options == nil || goja.IsUndefined(options) || goja.IsNull(options) {
		return
	}
	lineCounter := options.ToObject(runtime).Get("lineCounter")
	if lineCounter == nil || goja.IsUndefined(lineCounter) || goja.IsNull(lineCounter) {
		return
	}
	setter := lineCounter.ToObject(runtime).Get("__liteapiSetContent")
	if setter == nil || goja.IsUndefined(setter) || goja.IsNull(setter) {
		return
	}
	if fn, ok := goja.AssertFunction(setter); ok {
		if _, err := fn(lineCounter, runtime.ToValue(content)); err != nil {
			panic(err)
		}
	}
}

func newScriptYAMLLineCounterObject(runtime *goja.Runtime) *goja.Object {
	state := &scriptYAMLLineCounter{}
	object := runtime.NewObject()
	_ = object.Set("linePos", func(call goja.FunctionCall) goja.Value {
		line, col := state.linePos(int(call.Argument(0).ToInteger()))
		result := runtime.NewObject()
		_ = result.Set("line", line)
		_ = result.Set("col", col)
		return result
	})
	_ = object.DefineDataProperty("__liteapiSetContent", runtime.ToValue(func(content string) {
		state.setContent(content)
	}), goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return object
}

type scriptYAMLLineCounter struct {
	content string
	offsets []int
}

func newScriptYAMLDocumentObject(runtime *goja.Runtime, content string, doc *yaml.Node) goja.Value {
	root := scriptYAMLRootNode(doc)
	object := runtime.NewObject()
	scriptYAMLSetNodeKind(runtime, object, "document")
	_ = object.Set("contents", newScriptYAMLNodeObject(runtime, content, root))
	_ = object.Set("errors", []interface{}{})
	_ = object.Set("get", func(call goja.FunctionCall) goja.Value {
		return scriptYAMLMapGet(runtime, content, root, call.Argument(0), call.Argument(1))
	})
	_ = object.Set("getIn", func(call goja.FunctionCall) goja.Value {
		return scriptYAMLGetIn(runtime, content, root, call.Argument(0), call.Argument(1))
	})
	_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptYAMLNodeToInterface(root))
	})
	_ = object.Set("toString", func(goja.FunctionCall) goja.Value {
		data, err := yaml.Marshal(scriptYAMLNodeToInterface(root))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(string(data))
	})
	return object
}

func newScriptYAMLNodeObject(runtime *goja.Runtime, content string, node *yaml.Node) goja.Value {
	if node == nil {
		return goja.Null()
	}
	switch node.Kind {
	case yaml.DocumentNode:
		return newScriptYAMLDocumentObject(runtime, content, node)
	case yaml.MappingNode:
		object := runtime.NewObject()
		scriptYAMLSetNodeKind(runtime, object, "map")
		_ = object.Set("items", scriptYAMLMapItems(runtime, content, node))
		_ = object.Set("range", scriptYAMLNodeRange(content, node))
		_ = object.Set("get", func(call goja.FunctionCall) goja.Value {
			return scriptYAMLMapGet(runtime, content, node, call.Argument(0), call.Argument(1))
		})
		_ = object.Set("getIn", func(call goja.FunctionCall) goja.Value {
			return scriptYAMLGetIn(runtime, content, node, call.Argument(0), call.Argument(1))
		})
		_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(scriptYAMLNodeToInterface(node))
		})
		return object
	case yaml.SequenceNode:
		object := runtime.NewObject()
		scriptYAMLSetNodeKind(runtime, object, "seq")
		items := make([]goja.Value, 0, len(node.Content))
		for _, child := range node.Content {
			items = append(items, newScriptYAMLNodeObject(runtime, content, child))
		}
		_ = object.Set("items", items)
		_ = object.Set("range", scriptYAMLNodeRange(content, node))
		_ = object.Set("get", func(call goja.FunctionCall) goja.Value {
			index := int(call.Argument(0).ToInteger())
			if index < 0 || index >= len(node.Content) {
				return goja.Undefined()
			}
			return scriptYAMLNodeReturnValue(runtime, content, node.Content[index], call.Argument(1))
		})
		_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(scriptYAMLNodeToInterface(node))
		})
		return object
	case yaml.AliasNode:
		return newScriptYAMLNodeObject(runtime, content, node.Alias)
	default:
		object := runtime.NewObject()
		scriptYAMLSetNodeKind(runtime, object, "scalar")
		_ = object.Set("value", scriptYAMLNodeToInterface(node))
		_ = object.Set("range", scriptYAMLNodeRange(content, node))
		_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(scriptYAMLNodeToInterface(node))
		})
		_ = object.Set("toString", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(node.Value)
		})
		return object
	}
}

func scriptYAMLSetNodeKind(runtime *goja.Runtime, object *goja.Object, kind string) {
	_ = object.DefineDataProperty("__liteapiYamlKind", runtime.ToValue(kind), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
}

func scriptYAMLNodeKind(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	kind := value.ToObject(runtime).Get("__liteapiYamlKind")
	if kind == nil || goja.IsUndefined(kind) || goja.IsNull(kind) {
		return ""
	}
	return kind.String()
}

func scriptYAMLRootNode(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func scriptYAMLMapItems(runtime *goja.Runtime, content string, node *yaml.Node) []goja.Value {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	items := make([]goja.Value, 0, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		pair := runtime.NewObject()
		_ = pair.Set("key", newScriptYAMLNodeObject(runtime, content, node.Content[index]))
		_ = pair.Set("value", newScriptYAMLNodeObject(runtime, content, node.Content[index+1]))
		items = append(items, pair)
	}
	return items
}

func scriptYAMLMapGet(runtime *goja.Runtime, content string, node *yaml.Node, keyValue, keepNode goja.Value) goja.Value {
	if node == nil || node.Kind != yaml.MappingNode {
		return goja.Undefined()
	}
	key := keyValue.String()
	for index := 0; index+1 < len(node.Content); index += 2 {
		if fmt.Sprint(scriptYAMLNodeToInterface(node.Content[index])) == key {
			return scriptYAMLNodeReturnValue(runtime, content, node.Content[index+1], keepNode)
		}
	}
	return goja.Undefined()
}

func scriptYAMLGetIn(runtime *goja.Runtime, content string, node *yaml.Node, pathValue, keepNode goja.Value) goja.Value {
	current := node
	pathObject := pathValue.ToObject(runtime)
	length := int(pathObject.Get("length").ToInteger())
	for index := 0; index < length; index++ {
		part := pathObject.Get(strconv.Itoa(index))
		if current == nil {
			return goja.Undefined()
		}
		switch current.Kind {
		case yaml.MappingNode:
			current = scriptYAMLMapChild(current, part.String())
		case yaml.SequenceNode:
			childIndex := int(part.ToInteger())
			if childIndex < 0 || childIndex >= len(current.Content) {
				return goja.Undefined()
			}
			current = current.Content[childIndex]
		case yaml.DocumentNode:
			current = scriptYAMLRootNode(current)
			index--
		case yaml.AliasNode:
			current = current.Alias
			index--
		default:
			return goja.Undefined()
		}
	}
	return scriptYAMLNodeReturnValue(runtime, content, current, keepNode)
}

func scriptYAMLMapChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if fmt.Sprint(scriptYAMLNodeToInterface(node.Content[index])) == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func scriptYAMLNodeReturnValue(runtime *goja.Runtime, content string, node *yaml.Node, keepNode goja.Value) goja.Value {
	if node == nil {
		return goja.Undefined()
	}
	if keepNode != nil && !goja.IsUndefined(keepNode) && keepNode.ToBoolean() {
		return newScriptYAMLNodeObject(runtime, content, node)
	}
	return runtime.ToValue(scriptYAMLNodeToInterface(node))
}

func scriptYAMLNodeToInterface(node *yaml.Node) interface{} {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		return scriptYAMLNodeToInterface(scriptYAMLRootNode(node))
	case yaml.MappingNode:
		out := map[string]interface{}{}
		for index := 0; index+1 < len(node.Content); index += 2 {
			out[fmt.Sprint(scriptYAMLNodeToInterface(node.Content[index]))] = scriptYAMLNodeToInterface(node.Content[index+1])
		}
		return out
	case yaml.SequenceNode:
		out := make([]interface{}, len(node.Content))
		for index, child := range node.Content {
			out[index] = scriptYAMLNodeToInterface(child)
		}
		return out
	case yaml.AliasNode:
		return scriptYAMLNodeToInterface(node.Alias)
	default:
		var value interface{}
		if err := node.Decode(&value); err == nil {
			return scriptYAMLNormalize(value)
		}
		return node.Value
	}
}

func scriptYAMLLineOffsets(content string) []int {
	offsets := []int{0}
	for index, char := range content {
		if char == '\n' {
			offsets = append(offsets, index+1)
		}
	}
	return offsets
}

func scriptYAMLNodeRange(content string, node *yaml.Node) []int {
	start := scriptYAMLOffsetForPosition(content, node.Line, node.Column)
	end := start + len(node.Value)
	if end < start {
		end = start
	}
	if end > len(content) {
		end = len(content)
	}
	return []int{start, end}
}

func scriptYAMLOffsetForPosition(content string, line, column int) int {
	if line <= 0 {
		return 0
	}
	offsets := scriptYAMLLineOffsets(content)
	if line > len(offsets) {
		return len(content)
	}
	offset := offsets[line-1] + max(0, column-1)
	if offset > len(content) {
		return len(content)
	}
	return offset
}

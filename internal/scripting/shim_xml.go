package scripting

// The sandbox XML helpers.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

func scriptMinifyXML(runtime *goja.Runtime, value goja.Value) goja.Value {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		panic(runtime.NewGoError(errors.New("failed to minify")))
	}
	text, ok := value.Export().(string)
	if !ok {
		panic(runtime.NewTypeError("minifyXml expects a string"))
	}
	minified, err := minifyXMLString(text)
	if err != nil {
		panic(runtime.NewGoError(fmt.Errorf("failed to minify: %s", err.Error())))
	}
	return runtime.ToValue(minified)
}

func minifyXMLString(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return strings.TrimSpace(input), nil
	}
	decoder := xml.NewDecoder(strings.NewReader(input))
	var buffer bytes.Buffer
	encoder := xml.NewEncoder(&buffer)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if data, ok := token.(xml.CharData); ok && strings.TrimSpace(string(data)) == "" {
			continue
		}
		if err := encoder.EncodeToken(token); err != nil {
			return "", err
		}
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

type scriptXMLFormatterOptions struct {
	indentation   string
	lineSeparator string
}

func newScriptXMLFormatterObject(runtime *goja.Runtime) goja.Value {
	formatter := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		value := call.Argument(0)
		if goja.IsUndefined(value) || goja.IsNull(value) {
			panic(runtime.NewTypeError("xml-formatter expects a string"))
		}
		text, ok := value.Export().(string)
		if !ok {
			panic(runtime.NewTypeError("xml-formatter expects a string"))
		}
		options := scriptXMLFormatterOptions{indentation: "  ", lineSeparator: "\n"}
		if optionValue := call.Argument(1); optionValue != nil && !goja.IsUndefined(optionValue) && !goja.IsNull(optionValue) {
			optionObject := optionValue.ToObject(runtime)
			if indentation := optionObject.Get("indentation"); indentation != nil && !goja.IsUndefined(indentation) && !goja.IsNull(indentation) {
				options.indentation = indentation.String()
			}
			if lineSeparator := optionObject.Get("lineSeparator"); lineSeparator != nil && !goja.IsUndefined(lineSeparator) && !goja.IsNull(lineSeparator) {
				options.lineSeparator = lineSeparator.String()
			}
		}
		formatted, err := formatXMLString(text, options)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(formatted)
	})
	_ = formatter.ToObject(runtime).Set("default", formatter)
	return formatter
}

type scriptXML2JSOptions struct {
	ExplicitArray   bool
	ExplicitRoot    bool
	ExplicitCharKey bool
	Trim            bool
	AttrKey         string
	CharKey         string
}

type scriptXML2JSNode struct {
	Name     string
	Attrs    map[string]string
	Children []*scriptXML2JSNode
	Text     []string
}

func newScriptXML2JSObject(runtime *goja.Runtime) goja.Value {
	module := runtime.NewObject()
	_ = module.Set("parseString", func(call goja.FunctionCall) goja.Value {
		return scriptXML2JSParseString(runtime, scriptXML2JSDefaultOptions(), call)
	})
	_ = module.Set("parseStringPromise", func(call goja.FunctionCall) goja.Value {
		options := scriptXML2JSDefaultOptions()
		if optionValue := call.Argument(1); optionValue != nil && !goja.IsUndefined(optionValue) && !goja.IsNull(optionValue) {
			options = scriptXML2JSOptionsFromValue(runtime, optionValue, options)
		}
		result, err := scriptXML2JSParse(runtime, call.Argument(0), options)
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, result)
	})
	_ = module.Set("Parser", func(call goja.ConstructorCall) *goja.Object {
		return newScriptXML2JSParserObject(runtime, call.Argument(0))
	})
	_ = module.Set("defaults", map[string]interface{}{
		"0.2": map[string]interface{}{
			"explicitArray": true,
			"explicitRoot":  true,
			"attrkey":       "$",
			"charkey":       "_",
			"trim":          false,
		},
	})
	_ = module.Set("default", module)
	return module
}

func newScriptXML2JSParserObject(runtime *goja.Runtime, optionsValue goja.Value) *goja.Object {
	options := scriptXML2JSOptionsFromValue(runtime, optionsValue, scriptXML2JSDefaultOptions())
	parser := runtime.NewObject()
	_ = parser.Set("parseString", func(call goja.FunctionCall) goja.Value {
		return scriptXML2JSParseString(runtime, options, call)
	})
	_ = parser.Set("parseStringPromise", func(call goja.FunctionCall) goja.Value {
		result, err := scriptXML2JSParse(runtime, call.Argument(0), options)
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, result)
	})
	return parser
}

func scriptXML2JSDefaultOptions() scriptXML2JSOptions {
	return scriptXML2JSOptions{
		ExplicitArray: true,
		ExplicitRoot:  true,
		AttrKey:       "$",
		CharKey:       "_",
	}
}

func scriptXML2JSOptionsFromValue(runtime *goja.Runtime, value goja.Value, options scriptXML2JSOptions) scriptXML2JSOptions {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return options
	}
	object := value.ToObject(runtime)
	if explicitArray := object.Get("explicitArray"); explicitArray != nil && !goja.IsUndefined(explicitArray) && !goja.IsNull(explicitArray) {
		options.ExplicitArray = explicitArray.ToBoolean()
	}
	if explicitRoot := object.Get("explicitRoot"); explicitRoot != nil && !goja.IsUndefined(explicitRoot) && !goja.IsNull(explicitRoot) {
		options.ExplicitRoot = explicitRoot.ToBoolean()
	}
	if explicitCharKey := object.Get("explicitCharkey"); explicitCharKey != nil && !goja.IsUndefined(explicitCharKey) && !goja.IsNull(explicitCharKey) {
		options.ExplicitCharKey = explicitCharKey.ToBoolean()
	}
	if trim := object.Get("trim"); trim != nil && !goja.IsUndefined(trim) && !goja.IsNull(trim) {
		options.Trim = trim.ToBoolean()
	}
	if attrKey := object.Get("attrkey"); attrKey != nil && !goja.IsUndefined(attrKey) && !goja.IsNull(attrKey) {
		options.AttrKey = attrKey.String()
	}
	if charKey := object.Get("charkey"); charKey != nil && !goja.IsUndefined(charKey) && !goja.IsNull(charKey) {
		options.CharKey = charKey.String()
	}
	if options.AttrKey == "" {
		options.AttrKey = "$"
	}
	if options.CharKey == "" {
		options.CharKey = "_"
	}
	return options
}

func scriptXML2JSParseString(runtime *goja.Runtime, baseOptions scriptXML2JSOptions, call goja.FunctionCall) goja.Value {
	options := baseOptions
	callbackValue := call.Argument(1)
	if _, ok := goja.AssertFunction(callbackValue); !ok {
		options = scriptXML2JSOptionsFromValue(runtime, callbackValue, options)
		callbackValue = call.Argument(2)
	}
	callback, hasCallback := goja.AssertFunction(callbackValue)
	result, err := scriptXML2JSParse(runtime, call.Argument(0), options)
	if hasCallback {
		if err != nil {
			if _, callbackErr := callback(goja.Undefined(), runtime.NewGoError(err), goja.Undefined()); callbackErr != nil {
				panic(callbackErr)
			}
			return goja.Undefined()
		}
		if _, callbackErr := callback(goja.Undefined(), goja.Null(), result); callbackErr != nil {
			panic(callbackErr)
		}
		return goja.Undefined()
	}
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return result
}

func scriptXML2JSParse(runtime *goja.Runtime, value goja.Value, options scriptXML2JSOptions) (goja.Value, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return goja.Undefined(), errors.New("xml2js.parseString expects an XML string")
	}
	root, err := parseScriptXML2JS(value.String(), options)
	if err != nil {
		return goja.Undefined(), err
	}
	if root == nil {
		return runtime.ToValue(map[string]interface{}{}), nil
	}
	result := scriptXML2JSNodeValue(root, options)
	if options.ExplicitRoot {
		result = map[string]interface{}{root.Name: result}
	}
	return runtime.ToValue(result), nil
}

func parseScriptXML2JS(input string, options scriptXML2JSOptions) (*scriptXML2JSNode, error) {
	decoder := xml.NewDecoder(strings.NewReader(input))
	var root *scriptXML2JSNode
	stack := []*scriptXML2JSNode{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			node := &scriptXML2JSNode{Name: typed.Name.Local, Attrs: map[string]string{}}
			for _, attr := range typed.Attr {
				name := attr.Name.Local
				if attr.Name.Space != "" {
					name = attr.Name.Space + ":" + attr.Name.Local
				}
				node.Attrs[name] = attr.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("XML contains multiple root elements")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("unexpected closing tag %s", typed.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			text := string(typed)
			if options.Trim {
				text = strings.TrimSpace(text)
			}
			if strings.TrimSpace(text) != "" {
				stack[len(stack)-1].Text = append(stack[len(stack)-1].Text, text)
			}
		}
	}
	if len(stack) > 0 {
		return nil, errors.New("XML contains unclosed elements")
	}
	return root, nil
}

func scriptXML2JSNodeValue(node *scriptXML2JSNode, options scriptXML2JSOptions) interface{} {
	if node == nil {
		return nil
	}
	text := strings.Join(node.Text, "")
	hasAttrs := len(node.Attrs) > 0
	hasChildren := len(node.Children) > 0
	if !hasAttrs && !hasChildren && !options.ExplicitCharKey {
		return text
	}
	out := map[string]interface{}{}
	if hasAttrs {
		attrs := map[string]interface{}{}
		keys := make([]string, 0, len(node.Attrs))
		for key := range node.Attrs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			attrs[key] = node.Attrs[key]
		}
		out[options.AttrKey] = attrs
	}
	if text != "" || options.ExplicitCharKey {
		out[options.CharKey] = text
	}
	childGroups := map[string][]interface{}{}
	childOrder := []string{}
	for _, child := range node.Children {
		if _, ok := childGroups[child.Name]; !ok {
			childOrder = append(childOrder, child.Name)
		}
		childGroups[child.Name] = append(childGroups[child.Name], scriptXML2JSNodeValue(child, options))
	}
	for _, name := range childOrder {
		values := childGroups[name]
		if options.ExplicitArray || len(values) > 1 {
			out[name] = values
		} else if len(values) == 1 {
			out[name] = values[0]
		}
	}
	return out
}

func formatXMLString(input string, options scriptXMLFormatterOptions) (string, error) {
	if strings.TrimSpace(input) == "" {
		return strings.TrimSpace(input), nil
	}
	if err := validateXMLString(input); err != nil {
		return "", err
	}
	if options.indentation == "" && options.lineSeparator == "" {
		return minifyXMLTokens(input), nil
	}
	tokens := xmlFormatTokens(input)
	lines := make([]string, 0, len(tokens))
	level := 0
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if strings.TrimSpace(token) == "" {
			continue
		}
		switch {
		case isXMLClosingTag(token):
			if level > 0 {
				level--
			}
			lines = append(lines, strings.Repeat(options.indentation, level)+token)
		case isXMLOpeningTag(token):
			if index+2 < len(tokens) && !isXMLTag(tokens[index+1]) && isXMLClosingTag(tokens[index+2]) {
				lines = append(lines, strings.Repeat(options.indentation, level)+token+tokens[index+1]+tokens[index+2])
				index += 2
				continue
			}
			lines = append(lines, strings.Repeat(options.indentation, level)+token)
			level++
		default:
			lines = append(lines, strings.Repeat(options.indentation, level)+token)
		}
	}
	return strings.Join(lines, options.lineSeparator), nil
}

func validateXMLString(input string) error {
	decoder := xml.NewDecoder(strings.NewReader(input))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

func minifyXMLTokens(input string) string {
	var b strings.Builder
	for _, token := range xmlFormatTokens(input) {
		if strings.TrimSpace(token) == "" {
			continue
		}
		b.WriteString(token)
	}
	return b.String()
}

func isXMLTag(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "<")
}

func isXMLClosingTag(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "</")
}

func isXMLOpeningTag(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "<") &&
		!strings.HasPrefix(token, "</") &&
		!strings.HasPrefix(token, "<?") &&
		!strings.HasPrefix(token, "<!") &&
		!strings.HasSuffix(token, "/>")
}

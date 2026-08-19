package scripting

// The sandbox FormData and form-encoding helpers.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/dop251/goja"
)

func xmlFormatTokens(input string) []string {
	tokens := []string{}
	for len(input) > 0 {
		tagStart := strings.Index(input, "<")
		if tagStart < 0 {
			if strings.TrimSpace(input) != "" {
				tokens = append(tokens, input)
			}
			break
		}
		if tagStart > 0 {
			text := input[:tagStart]
			if strings.TrimSpace(text) != "" {
				tokens = append(tokens, text)
			}
			input = input[tagStart:]
		}
		tagEnd := strings.Index(input, ">")
		if tagEnd < 0 {
			tokens = append(tokens, input)
			break
		}
		tokens = append(tokens, strings.TrimSpace(input[:tagEnd+1]))
		input = input[tagEnd+1:]
	}
	return tokens
}

func scriptFormPair(name, value goja.Value) string {
	return scriptFormPairFromStrings(scriptFormValueString(name), scriptFormValueString(value))
}

func scriptFormPairFromStrings(name, value string) string {
	return scriptFormEncodeComponent(name) + "=" + scriptFormEncodeComponent(value)
}

func scriptFormValueString(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	exported := value.Export()
	if exported == nil {
		return ""
	}
	return fmt.Sprint(exported)
}

func scriptFormInterfaceString(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func scriptFormEncodeComponent(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

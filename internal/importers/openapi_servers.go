package importers

// Server URLs and their template variables.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"sort"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

func openAPIEventURL(expression string) (string, []types.Variable) {
	value := strings.TrimSpace(expression)
	if value == "" {
		return "{{callbackUrl}}", []types.Variable{openAPIEventURLVariable("callbackUrl", expression)}
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "{{") {
		return value, nil
	}
	if strings.HasPrefix(value, "/") {
		return "{{baseUrl}}" + convertOpenAPIPath(value), nil
	}
	if name := openAPIEventExpressionVariableName(value); name != "" {
		return "{{" + name + "}}", []types.Variable{openAPIEventURLVariable(name, expression)}
	}
	return "{{" + sanitizeVariableName(scalar.FirstNonEmpty(value, "callbackUrl")) + "}}", []types.Variable{openAPIEventURLVariable(sanitizeVariableName(scalar.FirstNonEmpty(value, "callbackUrl")), expression)}
}

func openAPIWebhookURL(name string) (string, []types.Variable) {
	value := strings.TrimSpace(name)
	if value == "" {
		return "{{baseUrl}}/webhooks/webhook", nil
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "{{") {
		return value, nil
	}
	if strings.HasPrefix(value, "/") {
		return "{{baseUrl}}" + convertOpenAPIPath(value), nil
	}
	return "{{baseUrl}}" + convertOpenAPIPath("/webhooks/"+value), nil
}

func openAPIEventExpressionVariableName(expression string) string {
	inner := strings.TrimSpace(expression)
	if strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}") {
		inner = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}"))
	}
	if inner == "" {
		return ""
	}
	if strings.Contains(inner, "#/") {
		_, pointer, _ := strings.Cut(inner, "#/")
		if name := sanitizeVariableName(lastPathToken(pointer)); name != "" {
			return name
		}
	}
	if strings.Contains(inner, ".") {
		if name := sanitizeVariableName(lastPathToken(strings.ReplaceAll(inner, ".", "/"))); name != "" {
			return name
		}
	}
	return sanitizeVariableName(inner)
}

func openAPIEventURLVariable(name, expression string) types.Variable {
	return types.Variable{
		ID:       scalar.DeterministicID("var", "openapi-event-url:"+name+":"+expression),
		Name:     name,
		Value:    strings.TrimSpace(expression),
		DataType: "string",
		Enabled:  true,
	}
}

func convertOpenAPIServerURL(rawURL string) string {
	var b strings.Builder
	for i := 0; i < len(rawURL); {
		if rawURL[i] == '{' {
			if i+1 < len(rawURL) && rawURL[i+1] == '{' {
				b.WriteByte(rawURL[i])
				i++
				continue
			}
			if endOffset := strings.IndexByte(rawURL[i+1:], '}'); endOffset >= 0 {
				end := i + 1 + endOffset
				b.WriteString("{{")
				b.WriteString(rawURL[i+1 : end])
				b.WriteString("}}")
				i = end + 1
				continue
			}
		}
		b.WriteByte(rawURL[i])
		i++
	}
	return b.String()
}

func openAPIOperationServerVariables(operation openAPIOperation, pathItem openAPIPath) []types.Variable {
	servers := operation.Servers
	if len(servers) == 0 {
		servers = pathItem.Servers
	}
	if len(servers) == 0 {
		return nil
	}
	return openAPIServerVariables(servers[0])
}

func openAPIServerVariables(server openAPIServer) []types.Variable {
	serverURL := strings.TrimRight(strings.TrimSpace(server.URL), "/")
	if serverURL == "" {
		return nil
	}
	variables := []types.Variable{openAPIServerVariableRow("baseUrl", convertOpenAPIServerURL(serverURL))}
	if len(server.Variables) == 0 {
		return variables
	}
	names := make([]string, 0, len(server.Variables))
	for name := range server.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		variables = append(variables, openAPIServerVariableRow(name, openAPIServerVariableValue(server.Variables[name])))
	}
	return variables
}

func openAPIServerVariableRow(name, value string) types.Variable {
	return types.Variable{ID: scalar.NewID("var"), Name: name, Value: value, DataType: "string", Type: "string", Enabled: true}
}

func openAPIServerVariableValue(variable openAPIServerVariable) string {
	if variable.Default != nil {
		if value, ok := openAPIStringValue(variable.Default); ok {
			return value
		}
	}
	if len(variable.Enum) > 0 {
		if value, ok := openAPIStringValue(variable.Enum[0]); ok {
			return value
		}
	}
	return ""
}

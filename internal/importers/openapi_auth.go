package importers

// Mapping OpenAPI security schemes onto the auth block of a request.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"sort"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

func openAPIAuth(operation openAPIOperation, doc OpenAPIDoc, root map[string]interface{}) (types.AuthConfig, bool) {
	security := operation.Security
	if security == nil {
		security = doc.Security
	}
	if security == nil {
		return types.AuthConfig{}, false
	}
	if len(security) == 0 {
		return types.AuthConfig{Mode: "none", APILocation: "header"}, true
	}
	for _, requirement := range security {
		if len(requirement) == 0 {
			return types.AuthConfig{Mode: "none", APILocation: "header"}, true
		}
		names := make([]string, 0, len(requirement))
		for name := range requirement {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if auth, ok := openAPISecuritySchemeAuth(name, root); ok {
				return auth, true
			}
		}
	}
	return types.AuthConfig{}, false
}

func openAPISecuritySchemeAuth(name string, root map[string]interface{}) (types.AuthConfig, bool) {
	components, ok := scalar.Map(root["components"])
	if !ok {
		return types.AuthConfig{}, false
	}
	schemes, ok := scalar.Map(components["securitySchemes"])
	if !ok {
		return types.AuthConfig{}, false
	}
	scheme, ok := scalar.Map(schemes[name])
	if !ok {
		return types.AuthConfig{}, false
	}
	switch strings.ToLower(scalar.YAMLString(scheme["type"])) {
	case "apikey":
		location := strings.ToLower(scalar.FirstNonEmpty(scalar.YAMLString(scheme["in"]), "header"))
		if location != "query" {
			location = "header"
		}
		return types.AuthConfig{Mode: "apikey", APIKey: scalar.YAMLString(scheme["name"]), APILocation: location}, true
	case "http":
		switch strings.ToLower(scalar.YAMLString(scheme["scheme"])) {
		case "bearer":
			return types.AuthConfig{Mode: "bearer", APILocation: "header"}, true
		case "basic":
			return types.AuthConfig{Mode: "basic", APILocation: "header"}, true
		case "digest":
			return types.AuthConfig{Mode: "digest", APILocation: "header"}, true
		}
	case "oauth2", "openidconnect":
		auth := types.AuthConfig{Mode: "oauth2", APILocation: "header"}
		if flows, ok := scalar.Map(scheme["flows"]); ok {
			if flow, ok := scalar.Map(scalar.FirstMapValue(flows, "clientCredentials", "client_credentials", "password", "authorizationCode", "authorization_code")); ok {
				auth.OAuth2.AccessTokenURL = scalar.FirstYAMLString(flow, "tokenUrl", "token_url")
				auth.OAuth2.Scope = strings.Join(openAPIScopeNames(flow["scopes"]), " ")
			}
		}
		return auth, true
	}
	return types.AuthConfig{}, false
}

func openAPIVisibleAuthRows(auth types.AuthConfig, headers, params []types.KeyValue) ([]types.KeyValue, []types.KeyValue) {
	if auth.Mode != "apikey" || strings.TrimSpace(auth.APIKey) == "" {
		return headers, params
	}
	row := types.KeyValue{Name: auth.APIKey, Value: auth.APIValue, Enabled: true}
	if strings.EqualFold(auth.APILocation, "query") {
		return headers, appendKeyValueIfMissing(params, row)
	}
	return appendKeyValueIfMissing(headers, row), params
}

func appendKeyValueIfMissing(rows []types.KeyValue, row types.KeyValue) []types.KeyValue {
	for _, existing := range rows {
		if strings.EqualFold(existing.Name, row.Name) {
			return rows
		}
	}
	return append(rows, row)
}

func openAPIScopeNames(raw interface{}) []string {
	scopes, ok := scalar.Map(raw)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(scopes))
	for name := range scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

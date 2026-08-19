// Postman v2.1 collections, including its script dialect.
package importers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

// Postman v2.1 collections, including its script dialect.

func ImportPostman(content, name string, translateScripts bool) (types.Collection, error) {
	var raw struct {
		Info struct {
			Name string `json:"name"`
		} `json:"info"`
		Auth     *postmanAuth      `json:"auth"`
		Variable []postmanVariable `json:"variable"`
		Event    []postmanEvent    `json:"event"`
		Item     []postmanItem     `json:"item"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return types.Collection{}, err
	}
	if raw.Info.Name != "" {
		name = raw.Info.Name
	}
	collection := types.Collection{ID: scalar.NewID("collection"), Name: name, Format: "postman", Auth: types.AuthConfig{Mode: "none"}, SecurityConfig: types.CollectionSecurityConfig{JSSandboxMode: "safe"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	collection.Variables = postmanVariables(raw.Variable)
	if auth, ok := postmanAuthConfig(raw.Auth); ok {
		collection.Auth = auth
	}
	collection.PreScript, collection.PostScript = postmanEventScripts(raw.Event, translateScripts)
	seq := 1
	appendPostmanItems(&collection, raw.Item, "", &seq, translateScripts)
	return collection, nil
}

type postmanItem struct {
	Name     string            `json:"name"`
	Auth     *postmanAuth      `json:"auth"`
	Event    []postmanEvent    `json:"event"`
	Request  *postmanRequest   `json:"request"`
	Response []postmanResponse `json:"response"`
	Item     []postmanItem     `json:"item"`
}

type postmanRequest struct {
	Method string `json:"method"`
	// US-053. Read as well as written, so a description survives a round trip
	// instead of the exporter emitting a field nothing ever reads back.
	Description interface{}     `json:"description"`
	URL         interface{}     `json:"url"`
	Header      []postmanHeader `json:"header"`
	Auth        *postmanAuth    `json:"auth"`
	Body        struct {
		Mode       string             `json:"mode"`
		Raw        string             `json:"raw"`
		URLEncoded []postmanHeader    `json:"urlencoded"`
		FormData   []postmanFormData  `json:"formdata"`
		GraphQL    postmanGraphQLBody `json:"graphql"`
		Options    postmanBodyOptions `json:"options"`
	} `json:"body"`
}

type postmanAuth struct {
	Type   string      `json:"type"`
	Basic  interface{} `json:"basic"`
	Bearer interface{} `json:"bearer"`
	APIKey interface{} `json:"apikey"`
	Digest interface{} `json:"digest"`
	AWSV4  interface{} `json:"awsv4"`
	OAuth1 interface{} `json:"oauth1"`
	OAuth2 interface{} `json:"oauth2"`
}

type postmanVariable struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

type postmanEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec interface{} `json:"exec"`
	} `json:"script"`
}

type postmanGraphQLBody struct {
	Query     string      `json:"query"`
	Variables interface{} `json:"variables"`
}

type postmanBodyOptions struct {
	Raw struct {
		Language string `json:"language"`
	} `json:"raw"`
}

type postmanFormData struct {
	Key         string      `json:"key"`
	Value       interface{} `json:"value"`
	Type        string      `json:"type"`
	Src         interface{} `json:"src"`
	Description string      `json:"description"`
	Disabled    bool        `json:"disabled"`
}

type postmanHeader struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
}

type postmanResponse struct {
	Name            string          `json:"name"`
	Status          string          `json:"status"`
	Code            int             `json:"code"`
	Header          []postmanHeader `json:"header"`
	Body            string          `json:"body"`
	PreviewLanguage string          `json:"_postman_previewlanguage"`
}

func appendPostmanItems(collection *types.Collection, entries []postmanItem, folderPath string, seq *int, translateScripts bool) {
	for _, entry := range entries {
		if entry.Request != nil {
			item := postmanRequestItem(entry, folderPath, *seq, translateScripts)
			collection.Items = append(collection.Items, item)
			*seq = *seq + 1
		}
		if len(entry.Item) > 0 {
			childFolderPath := postmanFolderPath(folderPath, entry.Name)
			appendPostmanFolder(collection, entry, childFolderPath, translateScripts)
			appendPostmanItems(collection, entry.Item, childFolderPath, seq, translateScripts)
		}
	}
}

func postmanRequestItem(entry postmanItem, folderPath string, seq int, translateScripts bool) types.RequestItem {
	item := types.NewRequestItem(entry.Name, "http", seq)
	item.FolderPath = folderPath
	item.Auth = types.AuthConfig{Mode: "inherit", APILocation: "header"}
	if entry.Request == nil {
		return item
	}
	item.Method = strings.ToUpper(scalar.FirstNonEmpty(entry.Request.Method, http.MethodGet))
	item.URL = postmanURL(entry.Request.URL)
	item.Headers = postmanKeyValues(entry.Request.Header, false)
	item.Params = postmanURLParams(entry.Request.URL)
	item.PathParams = postmanURLPathParams(entry.Request.URL)
	item.Body = postmanRequestBody(*entry.Request, item.Headers)
	if auth, ok := postmanAuthConfig(entry.Request.Auth); ok {
		item.Auth = auth
	}
	item.PreScript, item.PostScript = postmanEventScripts(entry.Event, translateScripts)
	if item.Body.Mode == "graphql" {
		item.Type = "graphql"
	}
	// Postman allows description to be either a string or an object with a
	// content field; yamlScalarString handles the scalar case and an object
	// falls through to empty rather than serialising Go map syntax into docs.
	if description, ok := entry.Request.Description.(string); ok {
		item.Docs = strings.TrimSpace(description)
	}
	item.Examples = postmanResponseExamples(entry.Response, item)
	return item
}

func appendPostmanFolder(collection *types.Collection, entry postmanItem, folderPath string, translateScripts bool) {
	if folderPath == "" {
		return
	}
	folder := types.FolderConfig{
		Path:        folderPath,
		DisplayPath: folderPath,
		Name:        scalar.FirstNonEmpty(strings.TrimSpace(entry.Name), "Untitled Folder"),
		Seq:         len(collection.Folders) + 1,
	}
	if auth, ok := postmanAuthConfig(entry.Auth); ok {
		folder.Auth = auth
	}
	folder.PreScript, folder.PostScript = postmanEventScripts(entry.Event, translateScripts)
	collection.Folders = append(collection.Folders, folder)
}

func postmanAuthConfig(auth *postmanAuth) (types.AuthConfig, bool) {
	if auth == nil {
		return types.AuthConfig{}, false
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "noauth":
		return types.AuthConfig{Mode: "none", APILocation: "header"}, true
	case "basic":
		return types.AuthConfig{Mode: "basic", Username: postmanAuthValue(auth.Basic, "username"), Password: postmanAuthValue(auth.Basic, "password"), APILocation: "header"}, true
	case "bearer":
		return types.AuthConfig{Mode: "bearer", Token: postmanAuthValue(auth.Bearer, "token"), APILocation: "header"}, true
	case "apikey":
		location := strings.ToLower(scalar.FirstNonEmpty(postmanAuthValue(auth.APIKey, "in"), "header"))
		if location != "query" {
			location = "header"
		}
		return types.AuthConfig{Mode: "apikey", APIKey: postmanAuthValue(auth.APIKey, "key"), APIValue: postmanAuthValue(auth.APIKey, "value"), APILocation: location}, true
	case "digest":
		return types.AuthConfig{Mode: "digest", Username: postmanAuthValue(auth.Digest, "username"), Password: postmanAuthValue(auth.Digest, "password"), APILocation: "header"}, true
	case "awsv4":
		return types.AuthConfig{
			Mode:        "awsv4",
			APILocation: "header",
			AWSV4: types.AWSV4Auth{
				AccessKeyID:     postmanAuthValue(auth.AWSV4, "accessKey"),
				SecretAccessKey: postmanAuthValue(auth.AWSV4, "secretKey"),
				SessionToken:    postmanAuthValue(auth.AWSV4, "sessionToken"),
				Service:         postmanAuthValue(auth.AWSV4, "service"),
				Region:          postmanAuthValue(auth.AWSV4, "region"),
			},
		}, true
	case "oauth1":
		placement := "header"
		if !postmanAuthBoolValue(auth.OAuth1, "addParamsToHeader", true) {
			placement = "query"
		}
		return types.AuthConfig{
			Mode:        "oauth1",
			APILocation: "header",
			OAuth1: types.OAuth1Auth{
				ConsumerKey:       postmanAuthValue(auth.OAuth1, "consumerKey"),
				ConsumerSecret:    postmanAuthValue(auth.OAuth1, "consumerSecret"),
				AccessToken:       postmanAuthValue(auth.OAuth1, "token"),
				AccessTokenSecret: postmanAuthValue(auth.OAuth1, "tokenSecret"),
				CallbackURL:       postmanAuthValue(auth.OAuth1, "callback"),
				Verifier:          postmanAuthValue(auth.OAuth1, "verifier"),
				SignatureMethod:   scalar.FirstNonEmpty(postmanAuthValue(auth.OAuth1, "signatureMethod"), "HMAC-SHA1"),
				PrivateKey:        postmanAuthValue(auth.OAuth1, "privateKey"),
				PrivateKeyType:    "text",
				Timestamp:         postmanAuthValue(auth.OAuth1, "timestamp"),
				Nonce:             postmanAuthValue(auth.OAuth1, "nonce"),
				Version:           scalar.FirstNonEmpty(postmanAuthValue(auth.OAuth1, "version"), "1.0"),
				Realm:             postmanAuthValue(auth.OAuth1, "realm"),
				Placement:         placement,
				IncludeBodyHash:   postmanAuthBoolValue(auth.OAuth1, "includeBodyHash", false),
			},
		}, true
	case "oauth2":
		return types.AuthConfig{Mode: "oauth2", APILocation: "header", OAuth2: postmanOAuth2Auth(auth.OAuth2)}, true
	}
	return types.AuthConfig{}, false
}

func postmanAuthValue(values interface{}, key string) string {
	raw, ok := postmanAuthRawValue(values, key)
	if !ok {
		return ""
	}
	return scalar.YAMLString(raw)
}

func postmanAuthRawValue(values interface{}, key string) (interface{}, bool) {
	if valueMap, ok := scalar.Map(values); ok {
		value, exists := valueMap[key]
		return value, exists
	}
	if valueList, ok := scalar.ListValue(values); ok {
		for _, raw := range valueList {
			valueMap, ok := scalar.Map(raw)
			if !ok {
				continue
			}
			if scalar.YAMLString(valueMap["key"]) == key {
				value, exists := valueMap["value"]
				return value, exists
			}
		}
	}
	return nil, false
}

func postmanAuthBoolValue(values interface{}, key string, fallback bool) bool {
	raw, ok := postmanAuthRawValue(values, key)
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	switch strings.ToLower(strings.TrimSpace(scalar.YAMLString(raw))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return fallback
}

func postmanOAuth2Auth(values interface{}) types.OAuth2Auth {
	postmanGrantType := postmanAuthValue(values, "grant_type")
	grantType := mapPostmanOAuth2GrantType(postmanGrantType)
	tokenPlacement := "url"
	if postmanAuthValue(values, "addTokenTo") == "header" {
		tokenPlacement = "header"
	}
	credentialsPlacement := "basic_auth_header"
	if postmanAuthValue(values, "client_authentication") == "body" {
		credentialsPlacement = "body"
	}
	auth := types.OAuth2Auth{
		GrantType:            grantType,
		AccessTokenURL:       postmanAuthValue(values, "accessTokenUrl"),
		RefreshTokenURL:      postmanAuthValue(values, "refreshTokenUrl"),
		ClientID:             postmanAuthValue(values, "clientId"),
		ClientSecret:         postmanAuthValue(values, "clientSecret"),
		Scope:                postmanAuthValue(values, "scope"),
		State:                postmanAuthValue(values, "state"),
		TokenPlacement:       tokenPlacement,
		TokenHeaderPrefix:    postmanAuthValue(values, "headerPrefix"),
		TokenQueryKey:        "access_token",
		CredentialsPlacement: credentialsPlacement,
	}
	switch postmanGrantType {
	case "authorization_code":
		auth.AuthorizationURL = postmanAuthValue(values, "authUrl")
		auth.CallbackURL = postmanAuthValue(values, "redirect_uri")
	case "authorization_code_with_pkce":
		auth.AuthorizationURL = postmanAuthValue(values, "authUrl")
		auth.CallbackURL = postmanAuthValue(values, "redirect_uri")
		auth.PKCE = true
	case "password_credentials":
		auth.Username = postmanAuthValue(values, "username")
		auth.Password = postmanAuthValue(values, "password")
	case "implicit":
		auth.AuthorizationURL = postmanAuthValue(values, "authUrl")
		auth.CallbackURL = postmanAuthValue(values, "redirect_uri")
	}
	return auth
}

func mapPostmanOAuth2GrantType(grantType string) string {
	switch grantType {
	case "authorization_code", "authorization_code_with_pkce":
		return "authorization_code"
	case "client_credentials":
		return "client_credentials"
	case "password_credentials":
		return "password"
	case "implicit":
		return "implicit"
	default:
		return "client_credentials"
	}
}

func postmanVariables(values []postmanVariable) []types.Variable {
	variables := make([]types.Variable, 0, len(values))
	for _, value := range values {
		if value.Key == "" && value.Value == nil {
			continue
		}
		name := postmanVariableName(value.Key)
		if name == "" {
			continue
		}
		variables = append(variables, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     name,
			Value:    scalar.YAMLString(value.Value),
			Type:     "string",
			DataType: "string",
			Enabled:  true,
		})
	}
	return variables
}

func postmanVariableName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func postmanEventScripts(events []postmanEvent, translate bool) (string, string) {
	preScripts := []string{}
	postScripts := []string{}
	for _, event := range events {
		script := postmanScriptText(event.Script.Exec, translate)
		if strings.TrimSpace(script) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(event.Listen)) {
		case "prerequest", "pre-request":
			preScripts = append(preScripts, script)
		case "test", "tests":
			postScripts = append(postScripts, script)
		}
	}
	return strings.Join(preScripts, "\n"), strings.Join(postScripts, "\n")
}

// postmanScriptText extracts an event's script text, translating pm.* to bru.*
// only when the importer was explicitly asked to (US-044).
//
// The default is now NO translation. Until US-039-043 there was no live pm
// object, so rewriting was the only way an imported Postman script could run at
// all. Now pm.* is native and more faithful than any textual rewrite can be —
// it keeps each variable scope distinct, reports Postman's status semantics,
// and throws where Postman throws. Translation is retained for collections
// whose scripts were already migrated by hand against the bru API, where a
// rewrite is what keeps them working.
func postmanScriptText(raw interface{}, translate bool) string {
	var script string
	switch value := raw.(type) {
	case string:
		script = value
	case []interface{}:
		lines := make([]string, 0, len(value))
		for _, line := range value {
			lines = append(lines, scalar.YAMLString(line))
		}
		script = strings.Join(lines, "\n")
	case []string:
		script = strings.Join(value, "\n")
	default:
		script = scalar.YAMLString(raw)
	}
	if !translate {
		return script
	}
	return TranslateScript(script)
}

func TranslateScript(script string) string {
	replacements := []struct {
		from string
		to   string
	}{
		// US-044. Each scope maps to ITS OWN bru function. These four
		// families all pointed at bru.getVar/setVar/deleteVar before, which
		// collapsed environment, collection, global and resolved-chain
		// variables into the single runtime scope. Nothing errored: a script
		// that wrote one scope and read another got its value back, so the
		// collapse looked like it worked until a second environment or
		// collection was supposed to see a different value.
		//
		// pm.variables is NOT translated. It reads the fully resolved chain,
		// and bru has no equivalent — bru.getVar reads only the runtime scope,
		// which is what produced half the collapse. Left alone it now runs on
		// the native pm.variables from US-040, which is exactly right. That is
		// the general rule this table follows after US-039-043: translate only
		// what maps EXACTLY, and leave everything else to the real pm object.
		{"pm.environment.get", "bru.getEnvVar"},
		{"pm.environment.set", "bru.setEnvVar"},
		{"pm.environment.unset", "bru.deleteEnvVar"},
		{"pm.environment.replaceIn", "bru.interpolate"},
		{"pm.collectionVariables.get", "bru.getCollectionVar"},
		{"pm.collectionVariables.set", "bru.setCollectionVar"},
		{"pm.collectionVariables.unset", "bru.deleteCollectionVar"},
		{"pm.collectionVariables.replaceIn", "bru.interpolate"},
		{"pm.globals.get", "bru.getGlobalEnvVar"},
		{"pm.globals.set", "bru.setGlobalEnvVar"},
		{"pm.globals.unset", "bru.deleteGlobalEnvVar"},
		{"pm.globals.replaceIn", "bru.interpolate"},
		{"pm.test", "test"},
		{"pm.expect", "expect"},
		{"pm.cookies.get", "bru.cookies.get"},
		{"pm.cookies.has", "bru.cookies.has"},
		{"pm.cookies.toObject", "bru.cookies.toObject"},
		{"pm.cookies.toString", "bru.cookies.toString"},
		{"pm.cookies.all", "bru.cookies.all"},
		{"pm.cookies.count", "bru.cookies.count"},
		{"pm.getResponseHeader", "res.getHeader"},
		{"pm.response.headers.has", "res.headerList.has"},
		{"pm.response.headers.one", "res.headerList.one"},
		{"pm.response.headers.all", "res.headerList.all"},
		{"pm.response.headers.count", "res.headerList.count"},
		{"pm.response.headers.indexOf", "res.headerList.indexOf"},
		{"pm.response.headers.find", "res.headerList.find"},
		{"pm.response.headers.filter", "res.headerList.filter"},
		{"pm.response.headers.each", "res.headerList.each"},
		{"pm.response.headers.map", "res.headerList.map"},
		{"pm.response.headers.reduce", "res.headerList.reduce"},
		{"pm.response.headers.toObject", "res.headerList.toObject"},
		{"pm.response.headers.toString", "res.headerList.toString"},
		{"pm.response.headers.toJSON", "res.headerList.toJSON"},
		{"pm.response.headers.get", "res.getHeader"},
		{"pm.response.headers", "res.headers"},
		{"pm.response.text()", "res.body"},
		{"pm.response.json()", "res.json"},
		{"pm.response.code", "res.status"},
		{"pm.response.statusText", "res.statusText"},
		{"pm.response.status", "res.statusText"},
		{"pm.request.headers.has", "req.headerList.has"},
		{"pm.request.headers.one", "req.headerList.one"},
		{"pm.request.headers.all", "req.headerList.all"},
		{"pm.request.headers.count", "req.headerList.count"},
		{"pm.request.headers.indexOf", "req.headerList.indexOf"},
		{"pm.request.headers.find", "req.headerList.find"},
		{"pm.request.headers.filter", "req.headerList.filter"},
		{"pm.request.headers.each", "req.headerList.each"},
		{"pm.request.headers.map", "req.headerList.map"},
		{"pm.request.headers.reduce", "req.headerList.reduce"},
		{"pm.request.headers.toObject", "req.headerList.toObject"},
		{"pm.request.headers.toString", "req.headerList.toString"},
		{"pm.request.headers.toJSON", "req.headerList.toJSON"},
		{"pm.request.headers.get", "req.getHeader"},
		{"pm.request.headers.remove", "req.deleteHeader"},
		{"pm.request.url", "req.getUrl()"},
		{"pm.request.method", "req.getMethod()"},
		{"pm.request.body", "req.body"},
	}
	translated := script
	for _, replacement := range replacements {
		translated = strings.ReplaceAll(translated, replacement.from, replacement.to)
	}
	translated = regexp.MustCompile(`pm\.response\.to\.have\.status\(([^)]*)\)`).ReplaceAllString(translated, `expect(res.status).to.equal($1)`)
	translated = regexp.MustCompile(`pm\.response\.to\.not\.have\.status\(([^)]*)\)`).ReplaceAllString(translated, `expect(res.status).not.to.equal($1)`)
	translated = regexp.MustCompile(`pm\.response\.to\.have\.header\(([^)]*)\)`).ReplaceAllString(translated, `expect(res.getHeader($1)).not.to.equal("")`)
	translated = regexp.MustCompile(`pm\.request\.headers\.(?:add|upsert)\(\s*\{\s*key\s*:\s*([^,}]+)\s*,\s*value\s*:\s*([^}]+)\}\s*\)`).ReplaceAllString(translated, `req.setHeader($1, $2)`)
	return translated
}

func postmanFolderPath(parent, name string) string {
	cleaned := scalar.SanitizeFilename(scalar.NormalizeWhitespace(name))
	if cleaned == "" || cleaned == "untitled" {
		return parent
	}
	if parent == "" {
		return cleaned
	}
	return parent + "/" + cleaned
}

func postmanURL(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]interface{}:
		if raw, ok := v["raw"].(string); ok {
			return raw
		}
	}
	return "{{host}}"
}

func postmanURLParams(value interface{}) []types.KeyValue {
	urlMap, ok := scalar.Map(value)
	if !ok {
		return nil
	}
	params := []types.KeyValue{}
	if query, ok := scalar.ListValue(urlMap["query"]); ok {
		params = append(params, postmanParamRows(query, false)...)
	}
	return params
}

func postmanURLPathParams(value interface{}) []types.KeyValue {
	urlMap, ok := scalar.Map(value)
	if !ok {
		return nil
	}
	params := []types.KeyValue{}
	if variables, ok := scalar.ListValue(urlMap["variable"]); ok {
		params = append(params, postmanParamRows(variables, true)...)
	}
	return params
}

func postmanParamRows(values []interface{}, forceEnabled bool) []types.KeyValue {
	rows := make([]types.KeyValue, 0, len(values))
	for _, raw := range values {
		valueMap, ok := scalar.Map(raw)
		if !ok {
			continue
		}
		name := scalar.YAMLString(scalar.FirstMapValue(valueMap, "key", "name"))
		value := scalar.YAMLString(valueMap["value"])
		if name == "" && value == "" {
			continue
		}
		enabled := !scalar.BoolValue(valueMap["disabled"], false)
		if forceEnabled {
			enabled = true
		}
		rows = append(rows, types.KeyValue{Name: name, Value: value, Description: scalar.YAMLString(valueMap["description"]), Enabled: enabled})
	}
	return rows
}

func postmanRequestBody(request postmanRequest, headers []types.KeyValue) types.RequestBody {
	body := types.RequestBody{Mode: "none"}
	switch strings.ToLower(strings.TrimSpace(request.Body.Mode)) {
	case "raw":
		mode := postmanRawBodyMode(headers, request.Body.Options.Raw.Language, request.Body.Raw)
		body.Mode = mode
		switch mode {
		case "json":
			body.JSON = request.Body.Raw
		case "xml":
			body.XML = request.Body.Raw
		default:
			body.Text = request.Body.Raw
		}
	case "urlencoded":
		body.Mode = "formUrlEncoded"
		body.FormURLEncoded = postmanKeyValues(request.Body.URLEncoded, false)
	case "formdata":
		body.Mode = "multipartForm"
		body.Multipart = postmanMultipartValues(request.Body.FormData)
	case "graphql":
		body.Mode = "graphql"
		body.GraphQLQuery = request.Body.GraphQL.Query
		body.GraphQLVariables = postmanGraphQLVariablesString(request.Body.GraphQL.Variables)
	}
	return body
}

func postmanGraphQLVariablesString(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return openAPIJSONString(value)
}

func postmanRawBodyMode(headers []types.KeyValue, language, raw string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "json":
		return "json"
	case "xml":
		return "xml"
	case "text", "html", "javascript":
		return "text"
	}
	if contentType := types.GetKeyValue(headers, "Content-Type"); contentType != "" {
		return openAPIExampleBodyType(contentType)
	}
	trimmed := strings.TrimSpace(raw)
	if json.Valid([]byte(trimmed)) {
		return "json"
	}
	if strings.HasPrefix(trimmed, "<") {
		return "xml"
	}
	return "text"
}

func postmanMultipartValues(values []postmanFormData) []types.FormPart {
	parts := make([]types.FormPart, 0, len(values))
	for _, value := range values {
		if value.Key == "" && value.Value == nil && value.Src == nil {
			continue
		}
		part := types.FormPart{Name: value.Key, Enabled: !value.Disabled}
		if strings.EqualFold(value.Type, "file") || (strings.EqualFold(value.Type, "default") && value.Src != nil) {
			part.FilePath = postmanFormDataString(value.Src)
		} else {
			part.Value = postmanFormDataString(value.Value)
		}
		parts = append(parts, part)
	}
	return parts
}

func postmanFormDataString(value interface{}) string {
	if values, ok := scalar.ListValue(value); ok {
		parts := make([]string, 0, len(values))
		for _, raw := range values {
			parts = append(parts, scalar.YAMLString(raw))
		}
		return strings.Join(parts, "")
	}
	return scalar.YAMLString(value)
}

func postmanResponseExamples(responses []postmanResponse, item types.RequestItem) []types.ResponseExample {
	examples := make([]types.ResponseExample, 0, len(responses))
	for index, response := range responses {
		headers := postmanKeyValues(response.Header, true)
		bodyType := postmanResponseBodyType(headers, response.PreviewLanguage, response.Body)
		statusText := scalar.FirstNonEmpty(response.Status, scalar.CleanStatusText(response.Code, ""))
		name := scalar.FirstNonEmpty(response.Name, strings.TrimSpace(fmt.Sprintf("%d %s", response.Code, statusText)))
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("Response %d", index+1)
		}
		examples = append(examples, types.ResponseExample{
			ID:   scalar.DeterministicID("example", item.Name+"#postman#"+strconv.Itoa(index)+"#"+name),
			Name: name,
			Type: scalar.FirstNonEmpty(item.Type, "http"),
			Request: types.ResponseExampleRequest{
				Method:         strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet)),
				URL:            item.URL,
				BodyMode:       scalar.FirstNonEmpty(item.Body.Mode, "none"),
				Body:           types.RequestBodySnapshot(item.Body),
				Headers:        types.CloneKeyValues(item.Headers),
				Params:         types.CloneKeyValues(item.Params),
				FormURLEncoded: types.CloneKeyValues(item.Body.FormURLEncoded),
				MultipartForm:  types.CloneFormParts(item.Body.Multipart),
				File:           types.CloneFileBodyEntries(types.FileBodyEntriesOf(item.Body)),
			},
			Response: types.ResponseExamplePayload{
				Status:     response.Code,
				StatusText: statusText,
				Headers:    headers,
				BodyType:   bodyType,
				Body:       response.Body,
				Size:       len([]byte(response.Body)),
			},
		})
	}
	return examples
}

func postmanKeyValues(headers []postmanHeader, responseHeaders bool) []types.KeyValue {
	rows := make([]types.KeyValue, 0, len(headers))
	for _, header := range headers {
		if header.Key == "" && header.Value == "" {
			continue
		}
		enabled := !header.Disabled
		if responseHeaders {
			enabled = true
		}
		rows = append(rows, types.KeyValue{Name: header.Key, Value: header.Value, Description: header.Description, Enabled: enabled})
	}
	return rows
}

func postmanResponseBodyType(headers []types.KeyValue, previewLanguage, body string) string {
	if contentType := types.GetKeyValue(headers, "Content-Type"); contentType != "" {
		return openAPIExampleBodyType(contentType)
	}
	switch strings.ToLower(strings.TrimSpace(previewLanguage)) {
	case "json":
		return "json"
	case "xml":
		return "xml"
	}
	if json.Valid([]byte(strings.TrimSpace(body))) {
		return "json"
	}
	if strings.HasPrefix(strings.TrimSpace(body), "<") {
		return "xml"
	}
	return "text"
}

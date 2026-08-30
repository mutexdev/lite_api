// Postman v2.1 collections, including its script dialect.
package importers

import (
	"bytes"
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
	collection, _, err := ImportPostmanWithWarnings(content, name, translateScripts)
	return collection, err
}

// ImportPostmanWithWarnings reports what the import could not read.
//
// A collection is not all-or-nothing. One item written in a shape this importer
// cannot decode used to abort the whole file with a raw Go unmarshal error —
// the user was told "json: cannot unmarshal string into Go struct field" about a
// hundred-request collection and got none of it. The unreadable item is skipped
// and named instead, and everything else imports.
func ImportPostmanWithWarnings(content, name string, translateScripts bool) (types.Collection, []string, error) {
	var raw struct {
		Info struct {
			Name        string      `json:"name"`
			Description interface{} `json:"description"`
		} `json:"info"`
		Auth     *postmanAuth      `json:"auth"`
		Variable []postmanVariable `json:"variable"`
		Event    []postmanEvent    `json:"event"`
		Item     []json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return types.Collection{}, nil, err
	}
	if raw.Info.Name != "" {
		name = raw.Info.Name
	}
	collection := types.Collection{ID: scalar.NewID("collection"), Name: name, Format: "postman", Auth: types.AuthConfig{Mode: "none"}, SecurityConfig: types.CollectionSecurityConfig{JSSandboxMode: "safe"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	collection.Docs = postmanDescription(raw.Info.Description)
	collection.Variables = postmanVariables(raw.Variable)
	if auth, ok := postmanAuthConfig(raw.Auth); ok {
		collection.Auth = auth
	}
	collection.PreScript, collection.PostScript = postmanEventScripts(raw.Event, translateScripts)
	context := &postmanImport{collection: &collection, seq: 1, translate: translateScripts, usedFolderPaths: map[string]bool{}, warningSeen: map[string]bool{}}
	context.appendItems(raw.Item, "")
	return collection, context.warnings, nil
}

// postmanImport carries the state one import needs across the item walk: the
// running sequence number, the folder paths already handed out (so two sibling
// folders can never claim one path), and the warnings the UI shows.
type postmanImport struct {
	collection      *types.Collection
	seq             int
	translate       bool
	usedFolderPaths map[string]bool
	warnings        []string
	warningSeen     map[string]bool
}

func (c *postmanImport) warn(message string) {
	if message == "" || c.warningSeen[message] {
		return
	}
	c.warningSeen[message] = true
	c.warnings = append(c.warnings, message)
}

type postmanItem struct {
	Name                    string                          `json:"name"`
	Description             interface{}                     `json:"description"`
	Auth                    *postmanAuth                    `json:"auth"`
	Event                   []postmanEvent                  `json:"event"`
	Request                 *postmanRequest                 `json:"request"`
	Response                []postmanResponse               `json:"response"`
	ProtocolProfileBehavior *postmanProtocolProfileBehavior `json:"protocolProfileBehavior"`
	// Held raw so one unreadable child cannot fail its siblings, and so an
	// empty "item": [] is distinguishable from an absent one — the first is a
	// folder with nothing in it, the second is a request.
	Item []json.RawMessage `json:"item"`
}

type postmanRequest struct {
	Method string `json:"method"`
	// US-053. Read as well as written, so a description survives a round trip
	// instead of the exporter emitting a field nothing ever reads back.
	Description interface{}       `json:"description"`
	URL         interface{}       `json:"url"`
	Header      postmanHeaderList `json:"header"`
	Auth        *postmanAuth      `json:"auth"`
	Body        struct {
		Mode       string             `json:"mode"`
		Raw        string             `json:"raw"`
		URLEncoded []postmanHeader    `json:"urlencoded"`
		FormData   []postmanFormData  `json:"formdata"`
		GraphQL    postmanGraphQLBody `json:"graphql"`
		File       postmanFileBody    `json:"file"`
		Options    postmanBodyOptions `json:"options"`
	} `json:"body"`
}

// UnmarshalJSON accepts Postman v2.0's bare-URL request as well as v2.1's
// object. The string form is a plain GET at that URL; before this it failed the
// entire import with a Go type error naming a struct field.
func (r *postmanRequest) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var endpoint string
		if err := json.Unmarshal(trimmed, &endpoint); err != nil {
			return err
		}
		*r = postmanRequest{Method: http.MethodGet, URL: endpoint}
		return nil
	}
	type rawRequest postmanRequest
	var decoded rawRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = postmanRequest(decoded)
	return nil
}

type postmanFileBody struct {
	Src         interface{} `json:"src"`
	ContentType string      `json:"contentType"`
}

type postmanProtocolProfileBehavior struct {
	// Pointers, because absence must leave the request defaults alone. A plain
	// bool would import every request without the block as strictSSL false.
	StrictSSL       *bool `json:"strictSSL"`
	FollowRedirects *bool `json:"followRedirects"`
	MaxRedirects    *int  `json:"maxRedirects"`
}

// postmanHeaderList accepts both Postman header shapes: v2.1's array and v2.0's
// single newline-separated string.
type postmanHeaderList []postmanHeader

func (h *postmanHeaderList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var block string
		if err := json.Unmarshal(trimmed, &block); err != nil {
			return err
		}
		headers := postmanHeaderList{}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			key, value, found := strings.Cut(line, ":")
			if !found {
				continue
			}
			headers = append(headers, postmanHeader{Key: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
		}
		*h = headers
		return nil
	}
	var rows []postmanHeader
	if err := json.Unmarshal(data, &rows); err != nil {
		return err
	}
	*h = rows
	return nil
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
	Key      string      `json:"key"`
	Value    interface{} `json:"value"`
	Disabled bool        `json:"disabled"`
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

func (c *postmanImport) appendItems(entries []json.RawMessage, folderPath string) {
	for index, raw := range entries {
		var entry postmanItem
		if err := json.Unmarshal(raw, &entry); err != nil {
			c.warn(fmt.Sprintf("Skipped one item that could not be read: %s.", postmanItemLocation(folderPath, index)))
			continue
		}
		if entry.Request != nil {
			item := postmanRequestItem(entry, folderPath, c.seq, c.translate)
			c.collection.Items = append(c.collection.Items, item)
			c.seq++
		}
		// An empty "item": [] is a folder with nothing in it. Dropping it lost a
		// folder the user had deliberately created.
		if entry.Item == nil || (len(entry.Item) == 0 && entry.Request != nil) {
			continue
		}
		childFolderPath := c.folderPath(folderPath, entry.Name)
		appendPostmanFolder(c.collection, entry, childFolderPath, c.translate)
		c.appendItems(entry.Item, childFolderPath)
	}
}

func postmanItemLocation(folderPath string, index int) string {
	position := fmt.Sprintf("entry %d", index+1)
	if strings.TrimSpace(folderPath) == "" {
		return position + " at the collection root"
	}
	return position + " in " + folderPath
}

// folderPath allocates ONE path per folder entry.
//
// It used to be a pure function of the parent and the name, which collapsed
// distinct folders into a single path three ways: two siblings with the same
// name, two whose names sanitise to the same string ("A/B" and "A-B" both
// become "A-B"), and a folder literally named "untitled", which was hoisted
// into its parent while still appending a FolderConfig that shadowed it. Every
// one of those merged unrelated requests into one folder, and the merge was
// invisible until someone went looking for a request that was no longer where
// they put it.
func (c *postmanImport) folderPath(parent, name string) string {
	cleaned := scalar.SanitizeFilename(scalar.NormalizeWhitespace(name))
	base := cleaned
	if parent != "" {
		base = parent + "/" + cleaned
	}
	candidate := base
	for ordinal := 2; c.usedFolderPaths[strings.ToLower(candidate)]; ordinal++ {
		candidate = fmt.Sprintf("%s-%d", base, ordinal)
	}
	c.usedFolderPaths[strings.ToLower(candidate)] = true
	return candidate
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
	// Postman writes the query BOTH ways: inside url.raw and again as url.query.
	// Keeping both sent every param twice — ?imported=true&imported=true — so
	// the structured rows win and the raw copy goes.
	if len(item.Params) > 0 {
		item.URL = trimPostmanURLQuery(item.URL)
	}
	item.PathParams = postmanURLPathParams(entry.Request.URL)
	applyPostmanProtocolProfileBehavior(&item, entry.ProtocolProfileBehavior)
	item.Body = postmanRequestBody(*entry.Request, item.Headers)
	if auth, ok := postmanAuthConfig(entry.Request.Auth); ok {
		item.Auth = auth
	}
	item.PreScript, item.PostScript = postmanEventScripts(entry.Event, translateScripts)
	if item.Body.Mode == "graphql" {
		item.Type = "graphql"
	}
	item.Docs = scalar.FirstNonEmpty(postmanDescription(entry.Request.Description), postmanDescription(entry.Description))
	item.Examples = postmanResponseExamples(entry.Response, item)
	return item
}

// postmanDescription reads Postman's description, which is either a plain
// string or an object carrying the text under "content". An object used to fall
// through to empty, losing the documentation of every collection written by the
// Postman app itself.
func postmanDescription(value interface{}) string {
	switch description := value.(type) {
	case string:
		return strings.TrimSpace(description)
	case nil:
		return ""
	}
	if valueMap, ok := scalar.Map(value); ok {
		return strings.TrimSpace(scalar.YAMLString(valueMap["content"]))
	}
	return ""
}

func applyPostmanProtocolProfileBehavior(item *types.RequestItem, behavior *postmanProtocolProfileBehavior) {
	if behavior == nil {
		return
	}
	if behavior.StrictSSL != nil {
		item.Settings.VerifyTLS = *behavior.StrictSSL
	}
	if behavior.FollowRedirects != nil {
		item.Settings.FollowRedirects = *behavior.FollowRedirects
	}
	if behavior.MaxRedirects != nil && *behavior.MaxRedirects >= 0 {
		item.Settings.MaxRedirects = *behavior.MaxRedirects
	}
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
	folder.Docs = postmanDescription(entry.Description)
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
	// Where the access token goes. Postman writes `addTokenTo` only when it is
	// not the default, so the common collection omits it entirely — and the
	// default is the HEADER:
	//
	//   postman-runtime 7.56.1 lib/authorizer/oauth2.js
	//   params.addTokenTo = params.addTokenTo || HEADER;
	//
	// This defaulted to "url" instead, which sent every imported OAuth2 request
	// as `?access_token=…` with no Authorization header. Servers that read only
	// the header see an unauthenticated call and answer with whatever they say
	// about an empty identity — a message that names the server's own authz
	// model and points nowhere near the import.
	//
	// The value for the query placement is `queryParams`, not `url`; `url` and
	// `query` are accepted too because that is LiteAPI's own vocabulary and a
	// hand-edited collection may carry either.
	tokenPlacement := "header"
	switch strings.ToLower(strings.TrimSpace(postmanAuthValue(values, "addTokenTo"))) {
	case "queryparams", "query", "url":
		tokenPlacement = "url"
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
			// A disabled collection variable that imports enabled starts
			// resolving placeholders the user had switched off.
			Enabled: !value.Disabled,
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

func postmanURL(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	urlMap, ok := scalar.Map(value)
	if !ok {
		return "{{host}}"
	}
	if raw, ok := urlMap["raw"].(string); ok && strings.TrimSpace(raw) != "" {
		return raw
	}
	// A URL object is allowed to carry no raw at all — Postman's own SDK writes
	// the parts and lets the reader join them. Without this the request imported
	// as the literal string "{{host}}" and pointed at nothing.
	if rebuilt := postmanURLFromParts(urlMap); rebuilt != "" {
		return rebuilt
	}
	return "{{host}}"
}

func postmanURLFromParts(urlMap map[string]interface{}) string {
	var builder strings.Builder
	host := postmanURLSegments(urlMap["host"], ".")
	if protocol := strings.TrimSpace(scalar.YAMLString(urlMap["protocol"])); protocol != "" && host != "" {
		builder.WriteString(protocol + "://")
	}
	builder.WriteString(host)
	if port := strings.TrimSpace(scalar.YAMLString(urlMap["port"])); port != "" && host != "" {
		builder.WriteString(":" + port)
	}
	if path := postmanURLSegments(urlMap["path"], "/"); path != "" {
		if !strings.HasPrefix(path, "/") {
			builder.WriteString("/")
		}
		builder.WriteString(path)
	}
	// The query is deliberately not rebuilt here: url.query becomes the request's
	// structured params, and repeating it in the URL is what sent every param
	// twice.
	if hash := strings.TrimSpace(scalar.YAMLString(urlMap["hash"])); hash != "" {
		builder.WriteString("#" + hash)
	}
	return builder.String()
}

func postmanURLSegments(value interface{}, separator string) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	list, ok := scalar.ListValue(value)
	if !ok {
		return strings.TrimSpace(scalar.YAMLString(value))
	}
	parts := make([]string, 0, len(list))
	for _, raw := range list {
		part := strings.TrimSpace(scalar.YAMLString(raw))
		if segment, ok := scalar.Map(raw); ok {
			part = strings.TrimSpace(scalar.YAMLString(segment["value"]))
		}
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, separator)
}

// trimPostmanURLQuery removes the query string while keeping any fragment.
func trimPostmanURLQuery(rawURL string) string {
	index := strings.Index(rawURL, "?")
	if index < 0 {
		return rawURL
	}
	trimmed := rawURL[:index]
	if hash := strings.Index(rawURL[index:], "#"); hash >= 0 {
		trimmed += rawURL[index+hash:]
	}
	return trimmed
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
	case "file":
		// Postman's binary body. With no case here the mode fell through to
		// "none" and the request imported as one that sends nothing.
		body.Mode = "file"
		if src := strings.TrimSpace(postmanFormDataString(request.Body.File.Src)); src != "" {
			body.Files = []types.FileBodyEntry{{FilePath: src, ContentType: request.Body.File.ContentType, Selected: true}}
		}
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

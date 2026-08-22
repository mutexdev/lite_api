// Postman v2.1 collections, including its script dialect.
package importers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

// Postman v2.1 collections, including its script dialect.

func ImportPostman(content, name string, translateScripts bool) (types.Collection, []string, error) {
	var raw struct {
		Info struct {
			Name        postmanFlexString `json:"name"`
			Description interface{}       `json:"description"`
		} `json:"info"`
		Auth                    *postmanAuth                   `json:"auth"`
		Variable                []postmanVariable              `json:"variable"`
		Event                   []postmanEvent                 `json:"event"`
		Item                    postmanItemList                `json:"item"`
		ProtocolProfileBehavior postmanProtocolProfileBehavior `json:"protocolProfileBehavior"`
	}
	trimmed := trimJSONPrefix(content)
	if err := postmanCollectionShapeError([]byte(trimmed)); err != nil {
		return types.Collection{}, nil, err
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return types.Collection{}, nil, err
	}
	if raw.Info.Name.String() != "" {
		name = raw.Info.Name.String()
	}
	collection := types.Collection{ID: scalar.NewID("collection"), Name: name, Format: "postman", Auth: types.AuthConfig{Mode: "none"}, SecurityConfig: types.CollectionSecurityConfig{JSSandboxMode: "safe"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	collection.Docs = postmanDescriptionText(raw.Info.Description)
	collection.Variables = postmanVariables(raw.Variable)
	if auth, ok := postmanAuthConfig(raw.Auth); ok {
		collection.Auth = auth
	}
	collection.PreScript, collection.PostScript = postmanEventScripts(raw.Event, translateScripts)
	seq := 1
	walk := &postmanImportWalk{collection: &collection, seq: &seq, translate: translateScripts, folders: map[string]bool{}}
	walk.warn("", raw.Item.Warnings)
	walk.appendItems(raw.Item.Items, "", raw.ProtocolProfileBehavior.StrictSSL)
	return collection, walk.warnings, nil
}

// postmanCollectionShapeError rejects documents that are not v2 collections.
//
// US-054. ImportPostman used to accept any JSON object: a Postman environment
// file, or a stray `{"hello":1}`, unmarshalled into a zero-valued collection
// and the apply step reported "1 imported" for an empty folder on disk. That is
// reachable from the UI whenever detection says "ambiguous" and the user
// rescues the file by choosing Postman from the kind list.
func postmanCollectionShapeError(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, hasInfo := fields["info"]
	_, hasItem := fields["item"]
	if hasInfo && hasItem {
		return nil
	}
	if _, ok := fields["_postman_variable_scope"]; ok {
		return errors.New("this is a Postman environment file, not a collection; import it as an environment")
	}
	if _, ok := fields["values"]; ok {
		if _, named := fields["name"]; named {
			return errors.New("this is a Postman environment file, not a collection; import it as an environment")
		}
	}
	if _, ok := fields["requests"]; ok {
		return errors.New("this is a Postman v1 collection; re-export it from Postman as Collection v2.1")
	}
	if _, ok := fields["collections"]; ok {
		return errors.New("this is a Postman data dump, not a single collection")
	}
	return errors.New("not a Postman collection: no \"info\" and \"item\" fields")
}

// postmanDescriptionText reads the two shapes a description takes: a string, or
// the {content, type} object Postman writes for markdown.
func postmanDescriptionText(value interface{}) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if valueMap, ok := scalar.Map(value); ok {
		return strings.TrimSpace(scalar.YAMLString(valueMap["content"]))
	}
	return ""
}

// postmanImportWalk carries the state a Postman tree walk needs: the sequence
// counter, the folder paths already claimed (so sibling folders of the same
// name do not merge), and the warnings raised along the way.
type postmanImportWalk struct {
	collection *types.Collection
	seq        *int
	translate  bool
	folders    map[string]bool
	warnings   []string
}

func (walk *postmanImportWalk) warn(folderPath string, messages []string) {
	for _, message := range messages {
		if folderPath != "" {
			message = "in folder " + strconv.Quote(folderPath) + ": " + message
		}
		walk.warnings = append(walk.warnings, message)
	}
}

func (walk *postmanImportWalk) appendItems(entries []postmanItem, folderPath string, inheritedStrictSSL *bool) {
	for _, entry := range entries {
		walk.warn(folderPath, entry.Item.Warnings)
		strictSSL := entry.ProtocolProfileBehavior.StrictSSL
		if strictSSL == nil {
			strictSSL = inheritedStrictSSL
		}
		request := entry.Request.Request()
		if request != nil {
			walk.collection.Items = append(walk.collection.Items, walk.requestItem(entry, request, folderPath, strictSSL))
			*walk.seq = *walk.seq + 1
		}
		if request != nil && len(entry.Item.Items) == 0 {
			continue
		}
		childPath, childName := walk.claimFolderPath(folderPath, entry.Name.String())
		if childPath == folderPath {
			walk.appendItems(entry.Item.Items, folderPath, strictSSL)
			continue
		}
		walk.appendFolder(entry, childPath, childName)
		walk.appendItems(entry.Item.Items, childPath, strictSSL)
	}
}

// claimFolderPath reserves a path for a folder. Postman permits two sibling
// folders with the same name; both used to map to one path, so their requests
// merged into a single directory and their per-folder auth raced.
func (walk *postmanImportWalk) claimFolderPath(parent, name string) (string, string) {
	base := postmanFolderPath(parent, name)
	if base == parent {
		return parent, ""
	}
	candidate, display := base, strings.TrimSpace(scalar.NormalizeWhitespace(name))
	for ordinal := 2; walk.folders[candidate]; ordinal++ {
		candidate = fmt.Sprintf("%s %d", base, ordinal)
		display = path.Base(candidate)
	}
	walk.folders[candidate] = true
	return candidate, display
}

func (walk *postmanImportWalk) appendFolder(entry postmanItem, folderPath, displayName string) {
	folder := types.FolderConfig{
		Path:        folderPath,
		DisplayPath: folderPath,
		Name:        scalar.FirstNonEmpty(displayName, strings.TrimSpace(entry.Name.String()), "Untitled Folder"),
		Seq:         len(walk.collection.Folders) + 1,
	}
	if auth, ok := postmanAuthConfig(entry.Auth); ok {
		folder.Auth = auth
	}
	folder.PreScript, folder.PostScript = postmanEventScripts(entry.Event, walk.translate)
	walk.collection.Folders = append(walk.collection.Folders, folder)
}

type postmanItem struct {
	Name                    postmanFlexString              `json:"name"`
	Auth                    *postmanAuth                   `json:"auth"`
	Event                   []postmanEvent                 `json:"event"`
	Request                 postmanRequestRef              `json:"request"`
	Response                []postmanResponse              `json:"response"`
	Item                    postmanItemList                `json:"item"`
	ProtocolProfileBehavior postmanProtocolProfileBehavior `json:"protocolProfileBehavior"`
}

// postmanProtocolProfileBehavior carries the per-collection, per-folder and
// per-request switches Postman keeps outside the request document. Only
// strictSSL is read: it is the reason a collection that sends fine in Postman
// answers with a certificate error here, because Postman verifies nothing
// unless told to and this app verifies unless told not to.
type postmanProtocolProfileBehavior struct {
	StrictSSL *bool `json:"strictSSL"`
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
		Raw        postmanFlexString  `json:"raw"`
		URLEncoded postmanHeaderList  `json:"urlencoded"`
		FormData   []postmanFormData  `json:"formdata"`
		File       interface{}        `json:"file"`
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
	Key      postmanFlexString `json:"key"`
	Value    interface{}       `json:"value"`
	Disabled bool              `json:"disabled"`
}

type postmanEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec interface{} `json:"exec"`
	} `json:"script"`
}

type postmanGraphQLBody struct {
	Query     postmanFlexString `json:"query"`
	Variables interface{}       `json:"variables"`
}

type postmanBodyOptions struct {
	Raw struct {
		Language string `json:"language"`
	} `json:"raw"`
}

type postmanFormData struct {
	Key         postmanFlexString `json:"key"`
	Value       interface{}       `json:"value"`
	Type        string            `json:"type"`
	Src         interface{}       `json:"src"`
	Description postmanFlexString `json:"description"`
	Disabled    bool              `json:"disabled"`
}

type postmanHeader struct {
	Key         postmanFlexString `json:"key"`
	Value       postmanFlexString `json:"value"`
	Description postmanFlexString `json:"description"`
	Disabled    bool              `json:"disabled"`
}

type postmanResponse struct {
	Name            postmanFlexString `json:"name"`
	Status          postmanFlexString `json:"status"`
	Code            postmanFlexInt    `json:"code"`
	Header          postmanHeaderList `json:"header"`
	Body            postmanFlexString `json:"body"`
	PreviewLanguage postmanFlexString `json:"_postman_previewlanguage"`
}

func (walk *postmanImportWalk) requestItem(entry postmanItem, request *postmanRequest, folderPath string, strictSSL *bool) types.RequestItem {
	item := types.NewRequestItem(entry.Name.String(), "http", *walk.seq)
	item.FolderPath = folderPath
	item.Auth = types.AuthConfig{Mode: "inherit", APILocation: "header"}
	item.Method = strings.ToUpper(scalar.FirstNonEmpty(request.Method, http.MethodGet))
	url, readable := postmanURL(request.URL)
	item.URL = url
	if !readable {
		walk.warn(folderPath, []string{"request " + strconv.Quote(entry.Name.String()) + " has no readable URL; imported as " + url})
	}
	item.Headers = postmanKeyValues(request.Header, false)
	item.Params = postmanURLParams(request.URL)
	item.PathParams = postmanURLPathParams(request.URL)
	item.Body = postmanRequestBody(*request, item.Headers)
	if auth, ok := postmanAuthConfig(request.Auth); ok {
		item.Auth = auth
	}
	item.PreScript, item.PostScript = postmanEventScripts(entry.Event, walk.translate)
	if item.Body.Mode == "graphql" {
		item.Type = "graphql"
	}
	if strictSSL != nil {
		item.Settings.VerifyTLS = *strictSSL
	}
	item.Docs = postmanDescriptionText(request.Description)
	item.Examples = postmanResponseExamples(entry.Response, item)
	return item
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
		name := postmanVariableName(value.Key.String())
		if name == "" {
			continue
		}
		variables = append(variables, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     name,
			Value:    scalar.YAMLString(value.Value),
			Type:     "string",
			DataType: "string",
			Enabled:  !value.Disabled,
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

// postmanURL reads the URL a request points at, and reports whether it could.
//
// US-054. A `url` object without `raw` used to return the literal "{{host}}",
// so every request in a collection produced by the Postman SDK, by Newman, or
// by any v2.0 exporter imported pointing at an undefined variable and said
// nothing about it. The components are reassembled instead, and the documented
// fallback is kept only for a URL with neither host nor path -- with a warning.
func postmanURL(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return v, true
		}
	case map[string]interface{}:
		if raw, ok := v["raw"].(string); ok && strings.TrimSpace(raw) != "" {
			return raw, true
		}
		if rebuilt := rebuildPostmanURL(v); rebuilt != "" {
			return rebuilt, true
		}
	}
	return "{{host}}", false
}

func rebuildPostmanURL(urlMap map[string]interface{}) string {
	host := postmanURLSegments(urlMap["host"], ".")
	requestPath := postmanURLSegments(urlMap["path"], "/")
	if host == "" && requestPath == "" {
		return ""
	}
	var builder strings.Builder
	if protocol := scalar.YAMLString(urlMap["protocol"]); protocol != "" {
		builder.WriteString(protocol + "://")
	}
	builder.WriteString(host)
	if port := scalar.YAMLString(urlMap["port"]); port != "" {
		builder.WriteString(":" + port)
	}
	if requestPath != "" {
		if !strings.HasPrefix(requestPath, "/") {
			builder.WriteString("/")
		}
		builder.WriteString(requestPath)
	}
	if query := postmanURLQuery(urlMap["query"]); query != "" {
		builder.WriteString("?" + query)
	}
	if hash := scalar.YAMLString(urlMap["hash"]); hash != "" {
		builder.WriteString("#" + hash)
	}
	return builder.String()
}

// postmanURLSegments joins a host or path, which Postman writes as a list of
// segments but which hand-edited collections carry as one string.
func postmanURLSegments(value interface{}, separator string) string {
	if text, ok := value.(string); ok {
		return text
	}
	values, ok := scalar.ListValue(value)
	if !ok {
		return ""
	}
	segments := make([]string, 0, len(values))
	for _, raw := range values {
		if segmentMap, ok := scalar.Map(raw); ok {
			segments = append(segments, scalar.YAMLString(scalar.FirstMapValue(segmentMap, "value", "key")))
			continue
		}
		segments = append(segments, scalar.YAMLString(raw))
	}
	return strings.Join(segments, separator)
}

// postmanURLQuery renders the enabled query rows, matching what Postman itself
// would have written into `raw`.
func postmanURLQuery(value interface{}) string {
	values, ok := scalar.ListValue(value)
	if !ok {
		return ""
	}
	pairs := make([]string, 0, len(values))
	for _, raw := range values {
		row, ok := scalar.Map(raw)
		if !ok || scalar.BoolValue(row["disabled"], false) {
			continue
		}
		key := scalar.YAMLString(scalar.FirstMapValue(row, "key", "name"))
		if key == "" {
			continue
		}
		if queryValue := scalar.YAMLString(row["value"]); queryValue != "" {
			pairs = append(pairs, key+"="+queryValue)
			continue
		}
		pairs = append(pairs, key)
	}
	return strings.Join(pairs, "&")
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
		raw := request.Body.Raw.String()
		mode := postmanRawBodyMode(headers, request.Body.Options.Raw.Language, raw)
		body.Mode = mode
		switch mode {
		case "json":
			body.JSON = raw
		case "xml":
			body.XML = raw
		default:
			body.Text = raw
		}
	case "urlencoded":
		body.Mode = "formUrlEncoded"
		body.FormURLEncoded = postmanKeyValues(request.Body.URLEncoded, false)
	case "formdata":
		body.Mode = "multipartForm"
		body.Multipart = postmanMultipartValues(request.Body.FormData)
	case "graphql":
		body.Mode = "graphql"
		body.GraphQLQuery = request.Body.GraphQL.Query.String()
		body.GraphQLVariables = postmanGraphQLVariablesString(request.Body.GraphQL.Variables)
	case "file":
		// US-054. Dropped entirely until now: the request imported with no body
		// at all and sent nothing, which reads as a server-side problem.
		if entries := postmanFileBodyEntries(request.Body.File); len(entries) > 0 {
			body.Mode = "file"
			body.Files = entries
			body.FilePath = entries[0].FilePath
		}
	}
	return body
}

// postmanFileBodyEntries reads {"src": "path"} and its list form.
func postmanFileBodyEntries(value interface{}) []types.FileBodyEntry {
	rows := []interface{}{value}
	if values, ok := scalar.ListValue(value); ok {
		rows = values
	}
	entries := make([]types.FileBodyEntry, 0, len(rows))
	for _, raw := range rows {
		filePath := ""
		if rowMap, ok := scalar.Map(raw); ok {
			filePath = scalar.YAMLString(scalar.FirstMapValue(rowMap, "src", "path"))
		} else {
			filePath = scalar.YAMLString(raw)
		}
		if strings.TrimSpace(filePath) == "" {
			continue
		}
		entries = append(entries, types.FileBodyEntry{FilePath: filePath, Selected: len(entries) == 0})
	}
	return entries
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
		part := types.FormPart{Name: value.Key.String(), Enabled: !value.Disabled}
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
		bodyType := postmanResponseBodyType(headers, response.PreviewLanguage.String(), response.Body.String())
		statusText := scalar.FirstNonEmpty(response.Status.String(), scalar.CleanStatusText(response.Code.Int(), ""))
		name := scalar.FirstNonEmpty(response.Name.String(), strings.TrimSpace(fmt.Sprintf("%d %s", response.Code.Int(), statusText)))
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
				Status:     response.Code.Int(),
				StatusText: statusText,
				Headers:    headers,
				BodyType:   bodyType,
				Body:       response.Body.String(),
				Size:       len([]byte(response.Body.String())),
			},
		})
	}
	return examples
}

func postmanKeyValues(headers postmanHeaderList, responseHeaders bool) []types.KeyValue {
	rows := make([]types.KeyValue, 0, len(headers))
	for _, header := range headers {
		if header.Key == "" && header.Value == "" {
			continue
		}
		enabled := !header.Disabled
		if responseHeaders {
			enabled = true
		}
		rows = append(rows, types.KeyValue{Name: header.Key.String(), Value: header.Value.String(), Description: header.Description.String(), Enabled: enabled})
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

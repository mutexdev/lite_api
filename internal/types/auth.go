// Credentials and the key/value primitives they build on.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

import "strings"

type KeyValue struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Enabled     bool   `json:"enabled"`
	Secret      bool   `json:"secret"`
	Description string `json:"description"`
}

type Variable struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Value    interface{} `json:"value"`
	Type     string      `json:"type"`
	DataType string      `json:"dataType"`
	Enabled  bool        `json:"enabled"`
	Secret   bool        `json:"secret"`
}

type RequestVars struct {
	Req []Variable `json:"req"`
	Res []Variable `json:"res"`
}

type AuthConfig struct {
	Mode        string     `json:"mode"`
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	Domain      string     `json:"domain"`
	Token       string     `json:"token"`
	APIKey      string     `json:"apiKey"`
	APIValue    string     `json:"apiValue"`
	APILocation string     `json:"apiLocation"`
	OAuth2      OAuth2Auth `json:"oauth2"`
	OAuth1      OAuth1Auth `json:"oauth1"`
	AWSV4       AWSV4Auth  `json:"awsv4"`
}

// APIKeyInQuery reports whether an apikey auth puts the key in the query string
// rather than in a header.
//
// The two send paths each compared APILocation to the bare string "query", so
// any other spelling fell through to the header branch. That is not a
// hypothetical: the folder-level auth editor stored "queryparams", so a folder
// that placed its API key in the query string sent it as a HEADER instead —
// silently, with a 401 from the server as the only symptom and nothing in the
// app disagreeing with what the user had configured.
//
// The UI now writes "query", and migrates "queryparams" the next time a
// folder's auth is saved. This exists because that migration only runs on save:
// a collection nobody edits again would keep the old value and keep failing.
// Normalising at the point of USE fixes those without requiring anyone to
// re-save anything.
//
// Deliberately permissive about which spellings mean "query" — every one listed
// has appeared in a stored collection or an importer — while anything
// unrecognised still means header, which is the safe default: a key in a header
// is at worst ignored by the server, whereas a key appended to a URL travels in
// logs and referrers.
func APIKeyInQuery(location string) bool {
	switch strings.ToLower(strings.TrimSpace(location)) {
	case "query", "queryparams", "queryparam", "url", "params":
		return true
	default:
		return false
	}
}

type OAuth1Auth struct {
	ConsumerKey       string `json:"consumerKey"`
	ConsumerSecret    string `json:"consumerSecret"`
	AccessToken       string `json:"accessToken"`
	AccessTokenSecret string `json:"accessTokenSecret"`
	CallbackURL       string `json:"callbackUrl"`
	Verifier          string `json:"verifier"`
	SignatureMethod   string `json:"signatureMethod"`
	PrivateKey        string `json:"privateKey"`
	PrivateKeyType    string `json:"privateKeyType"`
	Timestamp         string `json:"timestamp"`
	Nonce             string `json:"nonce"`
	Version           string `json:"version"`
	Realm             string `json:"realm"`
	Placement         string `json:"placement"`
	IncludeBodyHash   bool   `json:"includeBodyHash"`
}

type OAuth2Auth struct {
	GrantType                     string                  `json:"grantType"`
	CallbackURL                   string                  `json:"callbackUrl"`
	AuthorizationURL              string                  `json:"authorizationUrl"`
	AccessTokenURL                string                  `json:"accessTokenUrl"`
	RefreshTokenURL               string                  `json:"refreshTokenUrl"`
	Username                      string                  `json:"username"`
	Password                      string                  `json:"password"`
	ClientID                      string                  `json:"clientId"`
	ClientSecret                  string                  `json:"clientSecret"`
	Scope                         string                  `json:"scope"`
	State                         string                  `json:"state"`
	PKCE                          bool                    `json:"pkce"`
	CredentialsPlacement          string                  `json:"credentialsPlacement"`
	CredentialsID                 string                  `json:"credentialsId"`
	TokenSource                   string                  `json:"tokenSource"`
	TokenPlacement                string                  `json:"tokenPlacement"`
	TokenHeaderPrefix             string                  `json:"tokenHeaderPrefix"`
	TokenQueryKey                 string                  `json:"tokenQueryKey"`
	AutoFetchToken                bool                    `json:"autoFetchToken"`
	AutoRefreshToken              bool                    `json:"autoRefreshToken"`
	AuthorizationAdditionalParams []OAuth2AdditionalParam `json:"authorizationAdditionalParams"`
	TokenAdditionalParams         []OAuth2AdditionalParam `json:"tokenAdditionalParams"`
	RefreshAdditionalParams       []OAuth2AdditionalParam `json:"refreshAdditionalParams"`
	AdditionalParams              []KeyValue              `json:"additionalParams"`
}

type OAuth2AdditionalParam struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	SendIn      string `json:"sendIn"`
	Enabled     bool   `json:"enabled"`
	Secret      bool   `json:"secret"`
	Description string `json:"description"`
}

type AWSV4Auth struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
	Service         string `json:"service"`
	Region          string `json:"region"`
	ProfileName     string `json:"profileName"`
	AccessKey       string `json:"accessKey,omitempty"`
	SecretKey       string `json:"secretKey,omitempty"`
}

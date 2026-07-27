// Assertions and per-request settings.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

type Assertion struct {
	Expression string `json:"expression"`
	Operator   string `json:"operator"`
	Value      string `json:"value"`
	Enabled    bool   `json:"enabled"`
	Passed     bool   `json:"passed"`
	Message    string `json:"message"`
}

type RequestSettings struct {
	TimeoutMs                  int  `json:"timeoutMs"`
	FollowRedirects            bool `json:"followRedirects"`
	MaxRedirects               int  `json:"maxRedirects"`
	DisableParsingResponseJSON bool `json:"disableParsingResponseJson,omitempty"`
	EncodeURL                  bool `json:"encodeUrl"`
	StoreCookies               bool `json:"storeCookies"`
	VerifyTLS                  bool `json:"verifyTls"`
	KeepAliveInterval          int  `json:"keepAliveInterval"`
}

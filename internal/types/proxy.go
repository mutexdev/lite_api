// Package types holds the data shapes shared between the app's packages.
//
// US-060. Extracted from app.go, where 81 type declarations sat interleaved
// with the 266 methods that operate on them.
//
// NOTE for whoever continues this: a Go type alias left behind in internal/core
// does NOT hide the move from Wails. The generator follows the alias to its
// defining package and emits a `types` namespace, so every frontend reference
// to `main.X` for a moved type becomes `types.X`. This was measured, not
// assumed — see the US-060 entry in progress.txt.
package types

// ProxyAuthConfig carries proxy credentials.
type ProxyAuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Disabled bool   `json:"disabled,omitempty"`
}

// ProxyConfig is a single proxy definition.
type ProxyConfig struct {
	Inherit     bool            `json:"inherit"`
	Disabled    bool            `json:"disabled,omitempty"`
	Protocol    string          `json:"protocol"`
	Hostname    string          `json:"hostname"`
	Port        string          `json:"port"`
	Auth        ProxyAuthConfig `json:"auth"`
	BypassProxy string          `json:"bypassProxy"`
}

// ProxyPACConfig locates a proxy auto-config script.
type ProxyPACConfig struct {
	Source string `json:"source"`
}

// ProxyPreferences is the app-level proxy setting a request inherits from.
type ProxyPreferences struct {
	Disabled bool           `json:"disabled,omitempty"`
	Source   string         `json:"source"`
	PAC      ProxyPACConfig `json:"pac"`
	Config   ProxyConfig    `json:"config"`
}

// ClientCertificateConfig binds a client certificate to a domain.
type ClientCertificateConfig struct {
	Domain       string `json:"domain"`
	Type         string `json:"type"`
	CertFilePath string `json:"certFilePath,omitempty"`
	KeyFilePath  string `json:"keyFilePath,omitempty"`
	PFXFilePath  string `json:"pfxFilePath,omitempty"`
	Passphrase   string `json:"passphrase,omitempty"`
}

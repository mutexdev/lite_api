package transport

// Host matching for client certificates.
//
// A client certificate is a credential: matching one to the wrong host hands
// that host a signed proof of the user's identity. The matcher used to build a
// regex from the configured domain and run it against the whole request URL
// with no host terminator, so a pattern was free to end mid-hostname and an
// attacker who could register a lookalike name collected the certificate.
//
// The leak cases below are the specific URLs that matched under that
// implementation. They are the reason this file exists and must stay failing
// forever.

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func TestClientCertificateDomainDoesNotLeakToLookalikeHosts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		domain     string
		requestURL string
	}{
		// The configured host as a PREFIX of a longer registrable domain. An
		// attacker registers example.com.evil.com and receives the cert.
		{"subdomain of an attacker domain", "example.com", "https://example.com.evil.com/a"},
		// The configured host as a prefix of a longer LABEL. "example.com" is
		// a literal prefix of "example.community".
		{"longer label with the same prefix", "example.com", "https://example.community/a"},
		// The wildcard form leaked the same way: ".*" happily spanned the dot
		// into a foreign domain.
		{"wildcard into an attacker domain", "*.example.com", "https://api.example.com.evil.com/a"},

		// Neighbours of the above, to pin the boundary rather than just the
		// three measured cases.
		{"prefix of a longer label under wildcard", "*.example.com", "https://api.example.community/a"},
		{"suffix without the separating dot", "*.example.com", "https://notexample.com/a"},
		{"configured host is a suffix, not the host", "example.com", "https://evil.com/example.com"},
		{"host appears only in the path", "example.com", "https://evil.com/?u=https://example.com"},
		{"host appears only in userinfo", "example.com", "https://example.com@evil.com/a"},
		{"different host entirely", "example.com", "https://other.com/a"},

		// A wildcard is one-or-more labels: the bare domain is not covered,
		// which is the rule TLS itself uses for wildcard certificates.
		{"wildcard does not match the bare domain", "*.example.com", "https://example.com/a"},
		{"wildcard does not match an empty label", "*.example.com", "https://.example.com/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ClientCertificateDomainMatches(tc.requestURL, tc.domain) {
				t.Errorf("domain %q matched %q — the client certificate would be sent to that host", tc.domain, tc.requestURL)
			}
		})
	}
}

func TestClientCertificateDomainMatchesIntendedHosts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		domain     string
		requestURL string
	}{
		{"exact host", "example.com", "https://example.com/a"},
		{"exact host, no path", "example.com", "https://example.com"},
		{"one label under wildcard", "*.example.com", "https://api.example.com/a"},
		{"several labels under wildcard", "*.example.com", "https://a.b.example.com/a"},

		// Ports are ignored on both sides: the certificate belongs to the
		// host, and a port never changes which host is being authenticated to.
		{"port in the request URL", "example.com", "https://example.com:8443/a"},
		{"port in the configured domain", "example.com:8443", "https://example.com/a"},
		{"port on both sides", "example.com:8443", "https://example.com:8443/a"},
		{"port under wildcard", "*.example.com", "https://api.example.com:8443/a"},

		// Hostnames are case-insensitive; the old regex was case-SENSITIVE, so
		// this direction is a fix too.
		{"upper-case request host", "example.com", "https://EXAMPLE.COM/a"},
		{"upper-case configured domain", "EXAMPLE.COM", "https://example.com/a"},
		{"mixed case both sides", "ExAmPle.CoM", "https://eXaMPLE.com/a"},
		{"mixed case under wildcard", "*.Example.COM", "https://API.example.com/a"},

		// Every scheme the old pattern listed still has to work.
		{"grpc", "example.com", "grpc://example.com:50051"},
		{"grpcs", "example.com", "grpcs://example.com:50051"},
		{"ws", "example.com", "ws://example.com/socket"},
		{"wss", "example.com", "wss://example.com/socket"},
		{"http", "example.com", "http://example.com/a"},

		// Existing collections on disk hold domains typed with a scheme, and
		// URLs typed without one. Both were accepted before.
		{"configured domain carries a scheme", "https://example.com", "https://example.com/a"},
		{"configured wildcard carries a scheme", "https://*.example.com", "https://api.example.com/a"},
		{"configured domain carries a trailing slash", "example.com/", "https://example.com/a"},
		{"request URL has no scheme", "example.com", "example.com/a"},
		{"request URL has no scheme but a port", "example.com", "example.com:8443/a"},

		// localhost and literal IPs are the common development case.
		{"localhost with port", "localhost", "https://localhost:50051"},
		{"ipv4 literal", "127.0.0.1", "https://127.0.0.1:8443/a"},
		{"ipv6 literal", "[::1]", "https://[::1]:8443/a"},

		// An explicit "any host", which is what a bare "*" meant before.
		{"bare wildcard matches anything", "*", "https://anything.example.org/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !ClientCertificateDomainMatches(tc.requestURL, tc.domain) {
				t.Errorf("domain %q did not match %q, but should have", tc.domain, tc.requestURL)
			}
		})
	}
}

// Empty and malformed input must fail closed: no host, no certificate.
func TestClientCertificateDomainRejectsUnusableInput(t *testing.T) {
	for _, tc := range []struct{ name, domain, requestURL string }{
		{"empty domain", "", "https://example.com/a"},
		{"blank domain", "   ", "https://example.com/a"},
		{"empty url", "example.com", ""},
		{"blank url", "example.com", "   "},
		{"both empty", "", ""},
		{"url with no host", "example.com", "https:///just/a/path"},
		{"scheme-only domain", "https://", "https://example.com/a"},
		{"wildcard with no suffix", "*.", "https://example.com/a"},
		// A mid-host wildcard has no safe anchored reading, so it matches
		// nothing rather than matching loosely.
		{"mid-host wildcard", "api.*.com", "https://api.example.com/a"},
		{"mid-host wildcard against a lookalike", "api.*.com", "https://api.evil.com.attacker.net/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ClientCertificateDomainMatches(tc.requestURL, tc.domain) {
				t.Errorf("domain %q matched %q, want no match", tc.domain, tc.requestURL)
			}
		})
	}
}

// MatchingTLSClientCertificate is the function the executor actually calls.
// The narrow unit tests above would still pass if the host check were correct
// but never reached, so this asserts the leak is closed at the entry point —
// with a config whose files do not exist, so a match is proved by the load
// error and a non-match by the silent miss.
func TestMatchingClientCertificateAppliesHostMatchAtEntryPoint(t *testing.T) {
	certs := []types.ClientCertificateConfig{{
		Domain:       "example.com",
		Type:         "cert",
		CertFilePath: "/nonexistent/cert.pem",
		KeyFilePath:  "/nonexistent/key.pem",
	}}

	if _, ok, err := MatchingTLSClientCertificate("", certs, "https://example.com.evil.com/a", nil); ok || err != nil {
		t.Errorf("lookalike host selected the certificate: ok=%v err=%v", ok, err)
	}
	if _, ok, err := MatchingTLSClientCertificate("", certs, "https://example.community/a", nil); ok || err != nil {
		t.Errorf("longer-label host selected the certificate: ok=%v err=%v", ok, err)
	}
	// The intended host still selects it; the read failure proves selection
	// happened rather than the match being skipped.
	if _, _, err := MatchingTLSClientCertificate("", certs, "https://example.com/a", nil); err == nil {
		t.Error("intended host did not select the certificate at all")
	}
}

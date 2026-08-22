// A bypass rule must not match more hosts than it names.
//
// A rule beginning with `*` was turned into a suffix match by stripping the
// star, so `*example.com` also matched `fooexample.com` -- an unrelated host,
// sent direct instead of through the proxy it was configured to use. On a
// managed machine that is traffic leaving by a route the operator did not
// intend, which is the failure mode a bypass list exists to control.
package transport

import "testing"

func TestBypassWildcardRequiresADomainBoundary(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		url     string
		bypass  string
		proxied bool
	}{
		// The bug: a star with no dot after it became a bare substring match.
		{"unrelated host sharing a suffix", "http://fooexample.com/", "*example.com", true},
		{"unrelated host, dotted wildcard", "http://fooexample.com/", "*.example.com", true},
		{"unrelated host, leading dot", "http://fooexample.com/", ".example.com", true},

		// What the rule is actually for.
		{"the domain itself", "http://example.com/", "*example.com", false},
		{"a subdomain", "http://api.example.com/", "*example.com", false},
		{"a subdomain, dotted wildcard", "http://api.example.com/", "*.example.com", false},
		{"a deep subdomain", "http://a.b.example.com/", "*example.com", false},

		// Unchanged behaviour, kept here so a fix to the star form cannot
		// quietly change the other forms.
		{"a subdomain, leading dot", "http://api.example.com/", ".example.com", false},
		{"an exact rule", "http://example.com/", "example.com", false},
		{"an exact rule against a subdomain", "http://api.example.com/", "example.com", true},
		{"an unrelated host entirely", "http://other.test/", "*example.com", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ShouldUseManualProxy(testCase.url, testCase.bypass); got != testCase.proxied {
				t.Fatalf("ShouldUseManualProxy(%q, %q) = %v, want %v", testCase.url, testCase.bypass, got, testCase.proxied)
			}
		})
	}
}

// A port-qualified wildcard has to keep matching on the port as well, or the
// boundary fix would widen the rule it was meant to narrow.
func TestBypassWildcardStillHonoursPorts(t *testing.T) {
	if ShouldUseManualProxy("http://api.example.com:8080/", "*example.com:8080") {
		t.Fatal("a port-matched wildcard rule should bypass the proxy")
	}
	if !ShouldUseManualProxy("http://api.example.com:9090/", "*example.com:8080") {
		t.Fatal("a rule naming a different port should not bypass the proxy")
	}
	if !ShouldUseManualProxy("http://fooexample.com:8080/", "*example.com:8080") {
		t.Fatal("the boundary must still apply when a port is named")
	}
}

// PAC directive-list handling.
//
// These exist because a negative control found the "DIRECT" branch of
// pacProxyURLFromContent to be untested: deleting it left every test passing.
// A bare "DIRECT" hides the bug -- the directive has fewer than two fields, so
// the fallthrough skips it and the function returns "no proxy" either way.
//
// The branch only earns its keep when DIRECT precedes a proxy. A PAC return
// value is an ORDERED preference list, so "DIRECT; PROXY host" means go direct;
// without the explicit check the loop skips DIRECT and uses the proxy instead,
// sending traffic through a proxy the script asked it to bypass.
package transport

import "testing"

func pacScript(returns string) string {
	return "function FindProxyForURL(url, host) { return \"" + returns + "\"; }"
}

func TestPACDirectiveListPrefersDirectWhenItComesFirst(t *testing.T) {
	proxyURL, manual, err := pacProxyURLFromContent(pacScript("DIRECT; PROXY 10.0.0.1:8080"), "https://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL != nil {
		t.Fatalf("DIRECT came first, so no proxy should be used; got %s", proxyURL)
	}
	if manual {
		t.Fatal("a direct connection is not a manual proxy")
	}
}

func TestPACDirectiveListUsesProxyWhenItComesFirst(t *testing.T) {
	proxyURL, _, err := pacProxyURLFromContent(pacScript("PROXY 10.0.0.1:8080; DIRECT"), "https://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil {
		t.Fatal("PROXY came first, so it should be used")
	}
	if got := proxyURL.String(); got != "http://10.0.0.1:8080" {
		t.Fatalf("proxy URL: got %s", got)
	}
}

func TestPACBareDirectUsesNoProxy(t *testing.T) {
	proxyURL, _, err := pacProxyURLFromContent(pacScript("DIRECT"), "https://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL != nil {
		t.Fatalf("DIRECT means no proxy; got %s", proxyURL)
	}
}

func TestPACSchemesMapToProxyURLs(t *testing.T) {
	for _, tc := range []struct{ directive, want string }{
		{"PROXY 10.0.0.1:8080", "http://10.0.0.1:8080"},
		{"HTTPS 10.0.0.1:8443", "https://10.0.0.1:8443"},
		{"SOCKS 10.0.0.1:1080", "socks5://10.0.0.1:1080"},
		{"SOCKS5 10.0.0.1:1080", "socks5://10.0.0.1:1080"},
	} {
		proxyURL, _, err := pacProxyURLFromContent(pacScript(tc.directive), "https://example.com/x")
		if err != nil {
			t.Fatalf("%s: %v", tc.directive, err)
		}
		if proxyURL == nil || proxyURL.String() != tc.want {
			t.Fatalf("%s: got %v, want %s", tc.directive, proxyURL, tc.want)
		}
	}
}

// An unrecognised keyword is skipped rather than treated as a host, so a PAC
// script using a directive this build does not know still falls through to the
// next preference instead of dialling something nonsensical.
func TestPACUnknownDirectiveFallsThroughToTheNext(t *testing.T) {
	proxyURL, _, err := pacProxyURLFromContent(pacScript("QUIC 10.0.0.9:443; PROXY 10.0.0.1:8080"), "https://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil || proxyURL.String() != "http://10.0.0.1:8080" {
		t.Fatalf("got %v, want the second directive to win", proxyURL)
	}
}

// The cookie storage and sending rules.
//
// These are security rules, and their failure mode is silent in the worst way:
// a domain or path match that is too loose sends a cookie to somewhere that
// should never see it, the request succeeds, and nothing in the UI shows it
// happened. Negative control found them untested -- making cookiePathMatch
// always return true failed nothing.
package cookiejar

import (
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

func TestDomainMatchIsSuffixBoundedNotSubstring(t *testing.T) {
	cookie := types.CookieEntry{Domain: "example.com"}

	for host, want := range map[string]bool{
		"example.com":     true,
		"api.example.com": true,
		"a.b.example.com": true,

		// The ones that matter. A substring or unanchored suffix test lets a
		// cookie for example.com go to an attacker-registered lookalike.
		"notexample.com":       false,
		"example.com.evil.com": false,
		"evil-example.com":     false,
		"example.co":           false,
		"":                     false,
	} {
		if got := cookieDomainMatch(cookie, host); got != want {
			t.Errorf("cookieDomainMatch(example.com, %q) = %v, want %v", host, got, want)
		}
	}
}

// A host-only cookie was set without an explicit Domain, so it must go to that
// exact host and to no subdomain of it.
func TestHostOnlyCookieDoesNotReachSubdomains(t *testing.T) {
	cookie := types.CookieEntry{Domain: "example.com", HostOnly: true}

	if !cookieDomainMatch(cookie, "example.com") {
		t.Error("a host-only cookie must match its own host")
	}
	if cookieDomainMatch(cookie, "api.example.com") {
		t.Error("a host-only cookie must not reach a subdomain")
	}
}

func TestDomainMatchIgnoresALeadingDotAndCase(t *testing.T) {
	cookie := types.CookieEntry{Domain: ".Example.COM"}
	for _, host := range []string{"example.com", "API.example.com"} {
		if !cookieDomainMatch(cookie, host) {
			t.Errorf("%q should match .Example.COM", host)
		}
	}
}

func TestPathMatchRequiresASegmentBoundary(t *testing.T) {
	for _, tc := range []struct {
		cookiePath, requestPath string
		want                    bool
	}{
		{"/", "/anything", true},
		{"", "/anything", true},
		{"/api", "/api", true},
		{"/api", "/api/v1", true},
		{"/api/", "/api/v1", true},

		// The important negative: /api must not match /apikeys. A plain
		// HasPrefix without the boundary sends the cookie to a different path.
		{"/api", "/apikeys", false},
		{"/api", "/other", false},
		{"/api/v1", "/api", false},
	} {
		if got := cookiePathMatch(tc.cookiePath, tc.requestPath); got != tc.want {
			t.Errorf("cookiePathMatch(%q, %q) = %v, want %v", tc.cookiePath, tc.requestPath, got, tc.want)
		}
	}
}

// __Host- and __Secure- are promises the browser enforces. A jar that stores
// them without checking hands the name's guarantee to a cookie that has not
// earned it.
func TestPrefixValidEnforcesTheHostAndSecureContracts(t *testing.T) {
	for name, tc := range map[string]struct {
		cookie types.CookieEntry
		want   bool
	}{
		"__Host- fully compliant":  {types.CookieEntry{Name: "__Host-a", Secure: true, HostOnly: true, Path: "/"}, true},
		"__Host- not secure":       {types.CookieEntry{Name: "__Host-a", HostOnly: true, Path: "/"}, false},
		"__Host- not host-only":    {types.CookieEntry{Name: "__Host-a", Secure: true, Path: "/"}, false},
		"__Host- path not root":    {types.CookieEntry{Name: "__Host-a", Secure: true, HostOnly: true, Path: "/x"}, false},
		"__Secure- secure":         {types.CookieEntry{Name: "__Secure-a", Secure: true}, true},
		"__Secure- not secure":     {types.CookieEntry{Name: "__Secure-a"}, false},
		"ordinary name unaffected": {types.CookieEntry{Name: "session"}, true},
	} {
		if got := PrefixValid(tc.cookie); got != tc.want {
			t.Errorf("%s: PrefixValid = %v, want %v", name, got, tc.want)
		}
	}
}

func TestExpiredRespectsSessionAndExpiryTime(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if Expired(types.CookieEntry{Session: true}, now) {
		t.Error("a session cookie has no expiry and must not be treated as expired")
	}
	if !Expired(types.CookieEntry{Expires: now.Add(-time.Second)}, now) {
		t.Error("a cookie whose expiry has passed must be expired")
	}
	if Expired(types.CookieEntry{Expires: now.Add(time.Hour)}, now) {
		t.Error("a cookie expiring in the future must not be expired")
	}
}

// ForURL is where the rules combine, and it is what actually decides what goes
// out on the wire.
func TestForURLAppliesDomainPathAndExpiry(t *testing.T) {
	now := time.Now()
	cookies := []types.CookieEntry{
		{Name: "match", Domain: "example.com", Path: "/api", Expires: now.Add(time.Hour)},
		{Name: "wrong-domain", Domain: "other.com", Path: "/", Expires: now.Add(time.Hour)},
		{Name: "wrong-path", Domain: "example.com", Path: "/admin", Expires: now.Add(time.Hour)},
		{Name: "expired", Domain: "example.com", Path: "/", Expires: now.Add(-time.Hour)},
		{Name: "lookalike", Domain: "example.com.evil.com", Path: "/", Expires: now.Add(time.Hour)},
	}

	sent := map[string]bool{}
	for _, cookie := range ForURL(cookies, "https://api.example.com/api/v1/things") {
		sent[cookie.Name] = true
	}

	if !sent["match"] {
		t.Error("the matching cookie was not sent")
	}
	for _, name := range []string{"wrong-domain", "wrong-path", "expired", "lookalike"} {
		if sent[name] {
			t.Errorf("%q should not have been sent", name)
		}
	}
}

func TestDefaultPathStripsTheFinalSegment(t *testing.T) {
	for request, want := range map[string]string{
		"/api/v1/things": "/api/v1",
		"/things":        "/",
		"/":              "/",
		"":               "/",
	} {
		if got := DefaultPath(request); got != want {
			t.Errorf("DefaultPath(%q) = %q, want %q", request, got, want)
		}
	}
}

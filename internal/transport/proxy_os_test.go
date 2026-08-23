// Operating-system proxy settings on Windows and Linux (US-061).
//
// "System" is the default proxy mode, and on those two platforms it meant
// environment variables and nothing else -- so a corporate machine whose proxy
// is configured where corporate machines actually configure it, the Windows
// Internet Settings registry or the GNOME proxy panel, sent every request
// direct and failed.
//
// Both readers are split the way the macOS one already is: a shell-out or a
// registry read on one side, and a pure function on the other. That is what
// lets Windows behaviour be tested on Linux, and it is the only way any of this
// gets tested at all, since CI has neither a registry nor a desktop session.
package transport

import (
	"net/url"
	"testing"
)

func resolveOSProxy(t *testing.T, settings OSProxySettings, rawURL string) string {
	t.Helper()
	proxyURL, err := ProxyURLFromOSSettings(settings, rawURL)
	if err != nil {
		t.Fatalf("ProxyURLFromOSSettings: %v", err)
	}
	if proxyURL == nil {
		return ""
	}
	return proxyURL.String()
}

func TestWindowsInternetSettingsSingleProxyAppliesToEveryScheme(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "proxy.corp:8080", "", "")
	if got := resolveOSProxy(t, settings, "https://api.example.test/v1"); got != "http://proxy.corp:8080" {
		t.Fatalf("https = %q", got)
	}
	if got := resolveOSProxy(t, settings, "http://api.example.test/v1"); got != "http://proxy.corp:8080" {
		t.Fatalf("http = %q", got)
	}
}

func TestWindowsInternetSettingsPerSchemeProxies(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "http=p1.corp:8080;https=p2.corp:8443;ftp=p3.corp:21", "", "")
	if got := resolveOSProxy(t, settings, "http://a.test"); got != "http://p1.corp:8080" {
		t.Fatalf("http = %q", got)
	}
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "http://p2.corp:8443" {
		t.Fatalf("https = %q", got)
	}
}

func TestWindowsSocksOnlyConfigurationIsUsed(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "socks=socks.corp:1080", "", "")
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "socks5://socks.corp:1080" {
		t.Fatalf("socks = %q", got)
	}
}

// A configured but switched-off proxy is the single most common registry state
// on a machine that once had one. Using it would send every request to a host
// that is no longer there.
func TestWindowsProxyEnableZeroMeansNoProxy(t *testing.T) {
	settings := ParseWindowsInternetSettings(0, "proxy.corp:8080", "", "")
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "" {
		t.Fatalf("a disabled proxy was used: %q", got)
	}
}

func TestWindowsProxyOverrideBypassesListedHosts(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "proxy.corp:8080", "*.internal;10.*;localhost", "")
	for _, raw := range []string{"https://svc.internal/x", "http://localhost:8080/y"} {
		if got := resolveOSProxy(t, settings, raw); got != "" {
			t.Errorf("%s should bypass, got %q", raw, got)
		}
	}
	if got := resolveOSProxy(t, settings, "https://api.example.test"); got == "" {
		t.Error("a host outside the override list should still be proxied")
	}
}

// <local> is a Windows-specific token meaning "anything without a dot in it".
// It appears in the override list of practically every corporate machine.
func TestWindowsLocalTokenBypassesDotlessHosts(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "proxy.corp:8080", "<local>", "")
	if !settings.BypassLocal {
		t.Fatal("<local> was not recognised")
	}
	if got := resolveOSProxy(t, settings, "http://buildserver/status"); got != "" {
		t.Errorf("a dotless host should bypass, got %q", got)
	}
	if got := resolveOSProxy(t, settings, "https://api.example.test"); got == "" {
		t.Error("a dotted host should still be proxied")
	}
}

func TestWindowsAutoConfigURLIsReportedAsPAC(t *testing.T) {
	settings := ParseWindowsInternetSettings(0, "", "", "http://wpad.corp/proxy.pac")
	if settings.PACURL != "http://wpad.corp/proxy.pac" {
		t.Fatalf("PAC URL = %q", settings.PACURL)
	}
	// A PAC URL is a live proxy configuration even when the manual proxy
	// switch is off; the two settings are independent in Windows.
	if !settings.Configured() {
		t.Fatal("a PAC-only configuration reported as unconfigured")
	}
}

func TestGSettingsManualModeIsRead(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{
		Mode:       "'manual'",
		HTTPHost:   "'proxy.corp'",
		HTTPPort:   "8080",
		HTTPSHost:  "'secure.corp'",
		HTTPSPort:  "8443",
		IgnoreHost: "['localhost', '127.0.0.0/8', '.internal']",
	})
	if got := resolveOSProxy(t, settings, "http://a.test"); got != "http://proxy.corp:8080" {
		t.Fatalf("http = %q", got)
	}
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "http://secure.corp:8443" {
		t.Fatalf("https = %q", got)
	}
	if got := resolveOSProxy(t, settings, "https://svc.internal/x"); got != "" {
		t.Fatalf("ignore-hosts not honoured: %q", got)
	}
}

func TestGSettingsNoneModeMeansDirect(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'none'", HTTPHost: "'proxy.corp'", HTTPPort: "8080"})
	if settings.Configured() {
		t.Fatal("mode none reported as configured")
	}
	if got := resolveOSProxy(t, settings, "http://a.test"); got != "" {
		t.Fatalf("mode none used a proxy: %q", got)
	}
}

func TestGSettingsAutoModeReportsThePACURL(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'auto'", AutoconfigURL: "'http://wpad.corp/proxy.pac'"})
	if settings.PACURL != "http://wpad.corp/proxy.pac" {
		t.Fatalf("PAC URL = %q", settings.PACURL)
	}
}

// An 'auto' mode with no URL is what a machine using WPAD DNS discovery looks
// like. There is nothing to fetch, and inventing one would be a guess.
func TestGSettingsAutoModeWithoutURLIsNotConfigured(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'auto'", AutoconfigURL: "''"})
	if settings.Configured() {
		t.Fatal("auto mode with no PAC URL reported as configured")
	}
}

func TestGSettingsPortZeroIsNotAProxy(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'manual'", HTTPHost: "'proxy.corp'", HTTPPort: "0"})
	if got := resolveOSProxy(t, settings, "http://a.test"); got != "" {
		t.Fatalf("port 0 produced %q", got)
	}
}

func TestGSettingsEmptyHostIsNotAProxy(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'manual'", HTTPHost: "''", HTTPPort: "8080"})
	if settings.Configured() {
		t.Fatal("an empty host reported as configured")
	}
}

func TestGSettingsSocksFallbackAppliesToEveryScheme(t *testing.T) {
	settings := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'manual'", SOCKSHost: "'socks.corp'", SOCKSPort: "1080"})
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "socks5://socks.corp:1080" {
		t.Fatalf("socks = %q", got)
	}
}

func TestUnconfiguredOSSettingsResolveToDirect(t *testing.T) {
	if got := resolveOSProxy(t, OSProxySettings{}, "https://a.test"); got != "" {
		t.Fatalf("empty settings produced %q", got)
	}
}

func TestOSProxySettingsIgnoreAnUnparseableRequestURL(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "proxy.corp:8080", "", "")
	if _, err := ProxyURLFromOSSettings(settings, "::not a url::"); err != nil {
		t.Fatalf("an unreadable request URL should be direct, not an error: %v", err)
	}
}

// The environment variable is an explicit act by whoever launched the app, and
// has to win over a setting the machine was handed by its administrator.
func TestEnvironmentProxyTakesPriorityOverOSSettings(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://explicit.test:3128")
	t.Setenv("NO_PROXY", "")
	proxyURL, err := SystemProxyURLForRequest("https://api.example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil || proxyURL.Host != "explicit.test:3128" {
		t.Fatalf("environment proxy did not win: %v", proxyURL)
	}
}

func TestOSProxyHostsMayAlreadyCarryAScheme(t *testing.T) {
	// The registry and the GNOME panel both accept a value pasted with its
	// scheme still attached, and both store it verbatim.
	settings := ParseWindowsInternetSettings(1, "http://proxy.corp:8080", "", "")
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "http://proxy.corp:8080" {
		t.Fatalf("scheme-carrying host = %q", got)
	}
}

func TestOSProxyURLIsAUsableURL(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "proxy.corp:8080", "", "")
	proxyURL, err := ProxyURLFromOSSettings(settings, "https://a.test")
	if err != nil || proxyURL == nil {
		t.Fatalf("proxyURL=%v err=%v", proxyURL, err)
	}
	if _, err := url.Parse(proxyURL.String()); err != nil {
		t.Fatalf("unusable proxy URL %q: %v", proxyURL, err)
	}
	if proxyURL.Scheme != "http" || proxyURL.Host != "proxy.corp:8080" {
		t.Fatalf("proxy URL = %#v", proxyURL)
	}
}

// Per-scheme settings are honoured strictly, and this is deliberate.
//
// A machine with only the HTTP proxy filled in has no HTTPS proxy, and GNOME's
// own applications treat it that way; so does the macOS reader beside this one,
// which gates on HTTPSEnable rather than falling back. Quietly sending HTTPS
// through a proxy the user did not list for HTTPS would route traffic somewhere
// they did not ask for, which is the one mistake a proxy feature must not make.
//
// SOCKS is the exception, and also deliberate: it is protocol-agnostic, so a
// SOCKS proxy alone applies to everything.
func TestOSProxyDoesNotInventAnHTTPSProxyFromTheHTTPOne(t *testing.T) {
	httpOnly := ParseGSettingsProxy(gsettingsProxyValues{Mode: "'manual'", HTTPHost: "'proxy.corp'", HTTPPort: "8080"})
	if got := resolveOSProxy(t, httpOnly, "http://a.test"); got != "http://proxy.corp:8080" {
		t.Fatalf("http = %q", got)
	}
	if got := resolveOSProxy(t, httpOnly, "https://a.test"); got != "" {
		t.Fatalf("https borrowed the http proxy: %q", got)
	}
	// The "use the same proxy for everything" checkbox writes every row, which
	// is how a machine that means this expresses it.
	bothSet := ParseGSettingsProxy(gsettingsProxyValues{
		Mode: "'manual'", HTTPHost: "'proxy.corp'", HTTPPort: "8080", HTTPSHost: "'proxy.corp'", HTTPSPort: "8080",
	})
	if got := resolveOSProxy(t, bothSet, "https://a.test"); got != "http://proxy.corp:8080" {
		t.Fatalf("https = %q", got)
	}
}

// Windows expresses the same intent differently: a bare ProxyServer with no
// scheme prefix applies to every protocol, and must not be read as http-only.
func TestWindowsBareProxyServerCoversHTTPS(t *testing.T) {
	settings := ParseWindowsInternetSettings(1, "proxy.corp:8080", "", "")
	if got := resolveOSProxy(t, settings, "https://a.test"); got != "http://proxy.corp:8080" {
		t.Fatalf("https = %q", got)
	}
}

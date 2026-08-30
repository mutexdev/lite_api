// System-proxy discovery, and the proof that splitting it out of
// SystemProxyURLForRequest changed nothing.
//
// SystemProxyURLForRequest used to decide "which proxy" and "run this PAC
// script" in one pass, at three separate places: the LITEAPI_SYSTEM_PAC_URL
// environment variable, the macOS scutil ProxyAutoConfigURLString, and the
// Windows/GNOME PAC URL. Callers that must know a PAC is involved BEFORE a
// remote JavaScript program is fetched and run had nowhere to ask, so the
// decision was refactored into DiscoverSystemProxy and SystemProxyURLForRequest
// became discovery plus evaluation.
//
// The refactor is only safe if the two are the same algorithm, so the oracle in
// this file is the pre-refactor code itself, copied verbatim from 94b54d4 and
// left frozen. Every table row runs both and compares. If a future change to
// the real code alters a disposition -- a swallowed error becoming a returned
// one, a bypass no longer winning over a PAC -- the frozen copy stops agreeing
// and says so.
package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- the frozen pre-refactor implementations (94b54d4) ---------------------

// legacySystemProxyURLForRequest is SystemProxyURLForRequest as it stood before
// discovery was split out, platform branches and all -- so a row whose
// environment says nothing is compared on the same footing as the real code,
// including the subprocess. What the machine's own configuration happens to say
// does not matter; both sides ask it the same way.
func legacySystemProxyURLForRequest(rawURL string) (*url.URL, error) {
	if proxyURL, err := proxyURLFromEnvironment(rawURL); proxyURL != nil || err != nil {
		return proxyURL, err
	}
	if pacSource := strings.TrimSpace(os.Getenv("LITEAPI_SYSTEM_PAC_URL")); pacSource != "" {
		proxyURL, ok, err := ResolvePACProxyURL(pacSource, rawURL)
		if err != nil || !ok {
			return nil, nil
		}
		return proxyURL, nil
	}
	if goruntime.GOOS == "darwin" {
		return legacyMacOSSystemProxyURLForRequest(rawURL)
	}
	if settings, ok := readOSProxySettings(); ok {
		return legacyProxyURLFromOSSettings(settings, rawURL)
	}
	return nil, nil
}

func legacyMacOSSystemProxyURLForRequest(rawURL string) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return nil, nil
	}
	return legacyProxyURLFromMacOSScutilOutput(string(output), rawURL)
}

func legacyProxyURLFromMacOSScutilOutput(output, rawURL string) (*url.URL, error) {
	values, exceptions := parseMacOSScutilProxyOutput(output)
	if len(exceptions) > 0 && !ShouldUseManualProxy(rawURL, strings.Join(exceptions, ",")) {
		return nil, nil
	}
	if values["ProxyAutoConfigEnable"] == "1" && strings.TrimSpace(values["ProxyAutoConfigURLString"]) != "" {
		proxyURL, ok, err := ResolvePACProxyURL(values["ProxyAutoConfigURLString"], rawURL)
		if err != nil || !ok {
			return nil, nil
		}
		return proxyURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		if values["HTTPSEnable"] == "1" {
			return proxyURLFromParts("http", values["HTTPSProxy"], values["HTTPSPort"])
		}
	case "http", "ws":
		if values["HTTPEnable"] == "1" {
			return proxyURLFromParts("http", values["HTTPProxy"], values["HTTPPort"])
		}
	}
	if values["SOCKSEnable"] == "1" {
		return proxyURLFromParts("socks5", values["SOCKSProxy"], values["SOCKSPort"])
	}
	return nil, nil
}

func legacyProxyURLFromOSSettings(settings OSProxySettings, rawURL string) (*url.URL, error) {
	if !settings.Configured() {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.Scheme == "" {
		return nil, nil
	}
	if settings.BypassLocal && !strings.Contains(hostWithoutPort(parsed.Host), ".") {
		return nil, nil
	}
	if strings.TrimSpace(settings.Bypass) != "" && !ShouldUseManualProxy(rawURL, settings.Bypass) {
		return nil, nil
	}
	if pac := strings.TrimSpace(settings.PACURL); pac != "" {
		proxyURL, ok, err := ResolvePACProxyURL(pac, rawURL)
		if err != nil || !ok {
			return nil, nil
		}
		return proxyURL, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		if settings.HTTPSProxy != "" {
			return osProxyURL("http", settings.HTTPSProxy)
		}
	default:
		if settings.HTTPProxy != "" {
			return osProxyURL("http", settings.HTTPProxy)
		}
	}
	if settings.SOCKSProxy != "" {
		return osProxyURL("socks5", settings.SOCKSProxy)
	}
	return nil, nil
}

// --- comparison plumbing ---------------------------------------------------

// proxyResult is the whole observable answer of a resolution: which proxy (as
// the string a caller would dial), whether there was one at all, and the error.
type proxyResult struct {
	proxy   string
	present bool
	err     string
	failed  bool
}

func resultOf(proxyURL *url.URL, err error) proxyResult {
	result := proxyResult{present: proxyURL != nil, failed: err != nil}
	if proxyURL != nil {
		result.proxy = proxyURL.String()
	}
	if err != nil {
		result.err = err.Error()
	}
	return result
}

// clearProxyEnvironment removes every variable the environment branch reads, so
// a row states its whole world. The developer machine running these tests may
// well have HTTPS_PROXY exported.
func clearProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "LITEAPI_SYSTEM_PAC_URL",
	} {
		t.Setenv(name, "")
	}
}

// assertDiscoveryInvariants states what every caller of discovery may rely on:
// a PAC location and a static proxy are mutually exclusive, a PAC discovery
// never carries an error, and -- the point of the whole exercise -- discovery
// itself never enters the PAC load-and-evaluate path.
func assertDiscoveryInvariants(t *testing.T, name string, proxyURL *url.URL, pacURL string, err error, evaluations int64) {
	t.Helper()
	if pacURL != "" && proxyURL != nil {
		t.Errorf("%s: discovery returned both a proxy (%s) and a PAC source (%s)", name, proxyURL, pacURL)
	}
	if pacURL != "" && err != nil {
		t.Errorf("%s: discovery returned a PAC source and an error (%v)", name, err)
	}
	if evaluations != 0 {
		t.Errorf("%s: discovery evaluated %d PAC script(s); it must only report the location", name, evaluations)
	}
}

// pacDirect and pacProxy are inline PAC scripts. LoadPACSource treats a source
// that is neither a URL nor a readable file as the script itself, so these
// evaluate without touching the network -- which keeps "did anything fetch"
// answerable by the counter alone.
const (
	pacProxyScript  = `function FindProxyForURL(url, host) { return "PROXY pac.proxy.test:8080"; }`
	pacDirectScript = `function FindProxyForURL(url, host) { return "DIRECT"; }`
	pacBrokenScript = `function FindProxyForURL(url, host) { this is not javascript`
)

// --- the equivalence tables -------------------------------------------------

func TestDiscoverSystemProxyEquivalence(t *testing.T) {
	t.Run("environment and the environment PAC variable", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			env    map[string]string
			rawURL string
		}{
			{"https static proxy", map[string]string{"HTTPS_PROXY": "http://proxy.test:3128"}, "https://api.example.test/v1"},
			{"http static proxy", map[string]string{"HTTP_PROXY": "proxy.test:3128"}, "http://api.example.test/v1"},
			{"all_proxy fallback", map[string]string{"ALL_PROXY": "socks5://socks.test:1080"}, "https://api.example.test/v1"},
			{"no_proxy bypass", map[string]string{"HTTPS_PROXY": "http://proxy.test:3128", "NO_PROXY": "api.example.test"}, "https://api.example.test/v1"},
			{"unparseable proxy value", map[string]string{"HTTPS_PROXY": "http://%zz"}, "https://api.example.test/v1"},
			{"schemeless request URL", map[string]string{"HTTPS_PROXY": "http://proxy.test:3128"}, "api.example.test/v1"},
			{"pac variable selects a proxy", map[string]string{"LITEAPI_SYSTEM_PAC_URL": pacProxyScript}, "https://api.example.test/v1"},
			{"pac variable selects DIRECT", map[string]string{"LITEAPI_SYSTEM_PAC_URL": pacDirectScript}, "https://api.example.test/v1"},
			{"pac variable fails to run", map[string]string{"LITEAPI_SYSTEM_PAC_URL": pacBrokenScript}, "https://api.example.test/v1"},
			{
				"a static environment proxy outranks the pac variable",
				map[string]string{"HTTPS_PROXY": "http://proxy.test:3128", "LITEAPI_SYSTEM_PAC_URL": pacProxyScript},
				"https://api.example.test/v1",
			},
			{
				// NO_PROXY makes the environment branch decline, and declining
				// falls through to the PAC variable rather than meaning
				// "direct". Frozen because it is surprising, not because it is
				// obviously right.
				"no_proxy does not suppress the pac variable",
				map[string]string{"HTTPS_PROXY": "http://proxy.test:3128", "NO_PROXY": "api.example.test", "LITEAPI_SYSTEM_PAC_URL": pacProxyScript},
				"https://api.example.test/v1",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				clearProxyEnvironment(t)
				for name, value := range tc.env {
					t.Setenv(name, value)
				}

				resetPACEvaluations()
				proxyURL, pacURL, err := DiscoverSystemProxy(tc.rawURL)
				assertDiscoveryInvariants(t, tc.name, proxyURL, pacURL, err, pacEvaluations())

				want := resultOf(legacySystemProxyURLForRequest(tc.rawURL))
				got := resultOf(SystemProxyURLForRequest(tc.rawURL))
				if got != want {
					t.Fatalf("SystemProxyURLForRequest = %+v, pre-refactor = %+v", got, want)
				}
			})
		}
	})

	t.Run("macOS scutil output", func(t *testing.T) {
		const staticProxies = `<dictionary> {
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.example.test
  HTTPSEnable : 1
  HTTPSPort : 8443
  HTTPSProxy : secure-proxy.example.test
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : socks.example.test
}`
		const withExceptions = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.internal
    1 : localhost
  }
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.example.test
}`
		pacOutput := func(pac string) string {
			return "<dictionary> {\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : " + pac +
				"\n  HTTPEnable : 1\n  HTTPPort : 8080\n  HTTPProxy : static.example.test\n}"
		}
		pacWithExceptions := "<dictionary> {\n  ExceptionsList : <array> {\n    0 : *.internal\n  }\n" +
			"  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : " + pacProxyScript + "\n}"

		for _, tc := range []struct {
			name   string
			output string
			rawURL string
		}{
			{"http scheme takes the http proxy", staticProxies, "http://api.example.test/v1"},
			{"https scheme takes the https proxy", staticProxies, "https://api.example.test/v1"},
			{"socks is the fallback", "<dictionary> {\n  SOCKSEnable : 1\n  SOCKSPort : 1080\n  SOCKSProxy : socks.example.test\n}", "http://api.example.test/v1"},
			{"nothing enabled is direct", "<dictionary> {\n  HTTPEnable : 0\n  HTTPProxy : proxy.example.test\n}", "http://api.example.test/v1"},
			{"exceptions list bypasses", withExceptions, "http://service.internal/v1"},
			{"a host outside the exceptions is proxied", withExceptions, "http://api.example.test/v1"},
			{"pac selects a proxy", pacOutput(pacProxyScript), "https://api.example.test/v1"},
			{"pac selects DIRECT", pacOutput(pacDirectScript), "https://api.example.test/v1"},
			{"pac fails to run", pacOutput(pacBrokenScript), "https://api.example.test/v1"},
			{
				// The exceptions check runs first, so a bypassed host is direct
				// and the PAC is never even named.
				"exceptions outrank the pac", pacWithExceptions, "http://service.internal/v1",
			},
			{
				"an enabled but empty pac falls through to the static proxy",
				"<dictionary> {\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : \n  HTTPEnable : 1\n  HTTPPort : 8080\n  HTTPProxy : proxy.example.test\n}",
				"http://api.example.test/v1",
			},
			{
				"an unparseable proxy host is an error",
				"<dictionary> {\n  HTTPEnable : 1\n  HTTPProxy : %zz\n}",
				"http://api.example.test/v1",
			},
			{"an unreadable request URL is direct", staticProxies, "::not a url::"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resetPACEvaluations()
				proxyURL, pacURL, err := discoverProxyFromMacOSScutilOutput(tc.output, tc.rawURL)
				assertDiscoveryInvariants(t, tc.name, proxyURL, pacURL, err, pacEvaluations())

				want := resultOf(legacyProxyURLFromMacOSScutilOutput(tc.output, tc.rawURL))
				got := resultOf(ProxyURLFromMacOSScutilOutput(tc.output, tc.rawURL))
				if got != want {
					t.Fatalf("ProxyURLFromMacOSScutilOutput = %+v, pre-refactor = %+v", got, want)
				}
			})
		}
	})

	t.Run("Windows and GNOME settings", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			settings OSProxySettings
			rawURL   string
		}{
			{"windows single proxy", ParseWindowsInternetSettings(1, "proxy.corp:8080", "", ""), "https://api.example.test/v1"},
			{"windows per-scheme proxy", ParseWindowsInternetSettings(1, "http=p1.corp:8080;https=p2.corp:8443", "", ""), "https://a.test"},
			{"windows disabled proxy", ParseWindowsInternetSettings(0, "proxy.corp:8080", "", ""), "https://a.test"},
			{"windows override list", ParseWindowsInternetSettings(1, "proxy.corp:8080", "*.internal", ""), "https://svc.internal/x"},
			{"windows <local>", ParseWindowsInternetSettings(1, "proxy.corp:8080", "<local>", ""), "http://buildserver/status"},
			{"gnome manual", ParseGSettingsProxy(gsettingsProxyValues{Mode: "'manual'", HTTPHost: "'proxy.corp'", HTTPPort: "8080"}), "http://a.test"},
			{"gnome none", ParseGSettingsProxy(gsettingsProxyValues{Mode: "'none'", HTTPHost: "'proxy.corp'", HTTPPort: "8080"}), "http://a.test"},
			{"socks fallback", OSProxySettings{Enabled: true, SOCKSProxy: "socks.corp:1080"}, "https://a.test"},
			{"unconfigured", OSProxySettings{}, "https://a.test"},
			{"unreadable request URL", ParseWindowsInternetSettings(1, "proxy.corp:8080", "", ""), "::not a url::"},
			{"pac selects a proxy", OSProxySettings{PACURL: pacProxyScript}, "https://api.example.test/v1"},
			{"pac selects DIRECT", OSProxySettings{PACURL: pacDirectScript}, "https://api.example.test/v1"},
			{"pac fails to run", OSProxySettings{PACURL: pacBrokenScript}, "https://api.example.test/v1"},
			{
				// Both bypass rules are checked before the PAC, so a bypassed
				// host never reaches the script.
				"the bypass list outranks the pac",
				OSProxySettings{PACURL: pacProxyScript, Bypass: "*.internal"},
				"https://svc.internal/x",
			},
			{
				"<local> outranks the pac",
				OSProxySettings{PACURL: pacProxyScript, BypassLocal: true},
				"http://buildserver/status",
			},
			{
				"a pac outranks the manual proxy beside it",
				OSProxySettings{Enabled: true, HTTPProxy: "proxy.corp:8080", PACURL: pacProxyScript},
				"http://a.test",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resetPACEvaluations()
				proxyURL, pacURL, err := discoverProxyFromOSSettings(tc.settings, tc.rawURL)
				assertDiscoveryInvariants(t, tc.name, proxyURL, pacURL, err, pacEvaluations())

				want := resultOf(legacyProxyURLFromOSSettings(tc.settings, tc.rawURL))
				got := resultOf(ProxyURLFromOSSettings(tc.settings, tc.rawURL))
				if got != want {
					t.Fatalf("ProxyURLFromOSSettings = %+v, pre-refactor = %+v", got, want)
				}
			})
		}
	})
}

// A PAC script is a remote program: fetching one is a network request to
// whatever the machine names, and running it can resolve further hostnames of
// its own. Callers that must not do either need discovery to stop at the
// location, and "no proxy came back" is not evidence of that -- a PAC that ran
// and returned DIRECT looks identical. This asserts the two things that are
// evidence: zero fetches from the server the PAC URL points at, and zero
// entries into the evaluation path.
func TestDiscoverSystemProxyPerformsNoPACFetchOrEvaluation(t *testing.T) {
	var fetches atomic.Int64
	pacServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_, _ = w.Write([]byte(pacProxyScript))
	}))
	defer pacServer.Close()
	pacURL := pacServer.URL + "/proxy.pac"

	clearProxyEnvironment(t)
	t.Setenv("LITEAPI_SYSTEM_PAC_URL", pacURL)
	resetPACEvaluations()

	sources := map[string]func() (*url.URL, string, error){
		"LITEAPI_SYSTEM_PAC_URL": func() (*url.URL, string, error) {
			return DiscoverSystemProxy("https://api.example.test/v1")
		},
		"macOS scutil": func() (*url.URL, string, error) {
			return discoverProxyFromMacOSScutilOutput(
				"<dictionary> {\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : "+pacURL+"\n}",
				"https://api.example.test/v1")
		},
		"OS settings": func() (*url.URL, string, error) {
			return discoverProxyFromOSSettings(OSProxySettings{PACURL: pacURL}, "https://api.example.test/v1")
		},
	}
	for name, discover := range sources {
		proxyURL, discovered, err := discover()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if discovered != pacURL {
			t.Errorf("%s: PAC source = %q, want %q", name, discovered, pacURL)
		}
		if proxyURL != nil {
			t.Errorf("%s: a PAC discovery also produced a proxy URL %s", name, proxyURL)
		}
	}
	if got := fetches.Load(); got != 0 {
		t.Errorf("discovery fetched the PAC file %d time(s)", got)
	}
	if got := pacEvaluations(); got != 0 {
		t.Errorf("discovery entered the PAC evaluation path %d time(s)", got)
	}

	// The positive control: without it, a counter that never increments and a
	// server that is never reachable would pass the assertions above.
	proxyURL, err := SystemProxyURLForRequest("https://api.example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil || proxyURL.String() != "http://pac.proxy.test:8080" {
		t.Fatalf("evaluating the discovered PAC produced %v", proxyURL)
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("PAC fetches after one evaluation = %d, want 1", got)
	}
	if got := pacEvaluations(); got != 1 {
		t.Errorf("PAC evaluations after one evaluation = %d, want 1", got)
	}
}

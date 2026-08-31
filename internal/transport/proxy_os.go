// Operating-system proxy settings, in the one shape the resolver understands
// (US-061).
//
// "System" is this app's default proxy mode, and until now it meant environment
// variables everywhere plus `scutil` on macOS. A corporate Windows or Linux
// machine configures its proxy where the operating system keeps it -- the
// Internet Settings registry, or the GNOME proxy panel -- and neither was read,
// so every request went direct and failed against a network that has no route
// out except through the proxy.
//
// Each platform reader is split in two: the part that touches the registry or
// shells out to gsettings, which only that platform can run, and a pure
// function that turns what it found into OSProxySettings. That split is what
// makes Windows behaviour testable on Linux, and it is the same shape the macOS
// reader already had.
package transport

import (
	"net/url"
	"strconv"
	"strings"
)

// OSProxySettings is a machine's proxy configuration, normalised across the
// platforms that express the same thing three different ways.
type OSProxySettings struct {
	// Enabled reports whether the manual proxy is switched on. A configured
	// but disabled proxy is the commonest state on a machine that used to have
	// one, and using it would send every request to a host that has moved.
	Enabled bool
	// HTTPProxy, HTTPSProxy and SOCKSProxy are host:port, without a scheme.
	HTTPProxy  string
	HTTPSProxy string
	SOCKSProxy string
	// Bypass is a bypass list in the format ShouldUseManualProxy understands.
	Bypass string
	// BypassLocal is Windows's <local> token: bypass any host whose name has no
	// dot in it. It appears in the override list of most corporate machines.
	BypassLocal bool
	// PACURL is a proxy auto-config script location. It is independent of
	// Enabled: Windows and GNOME both let a PAC be active with the manual proxy
	// switched off.
	PACURL string
}

// Configured reports whether the machine says anything about a proxy at all.
func (settings OSProxySettings) Configured() bool {
	if strings.TrimSpace(settings.PACURL) != "" {
		return true
	}
	if !settings.Enabled {
		return false
	}
	return settings.HTTPProxy != "" || settings.HTTPSProxy != "" || settings.SOCKSProxy != ""
}

// ProxyURLFromOSSettings picks the proxy for one request.
//
// A PAC script wins when there is one, because that is what the machine was
// told to consult; the evaluator it goes to is the one the manual PAC mode
// already uses.
func ProxyURLFromOSSettings(settings OSProxySettings, rawURL string) (*url.URL, error) {
	proxyURL, pacURL, err := discoverProxyFromOSSettings(settings, rawURL)
	if pacURL != "" {
		return systemPACProxyURL(pacURL, rawURL)
	}
	return proxyURL, err
}

// discoverProxyFromOSSettings is ProxyURLFromOSSettings without the PAC
// evaluation: a machine configured with a PAC gets its location reported, not
// fetched and run. The bypass checks stay ahead of it, so a bypassed host is
// still direct and no PAC location is reported for it at all.
func discoverProxyFromOSSettings(settings OSProxySettings, rawURL string) (*url.URL, string, error) {
	if !settings.Configured() {
		return nil, "", nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.Scheme == "" {
		return nil, "", nil
	}
	if settings.BypassLocal && !strings.Contains(hostWithoutPort(parsed.Host), ".") {
		return nil, "", nil
	}
	if strings.TrimSpace(settings.Bypass) != "" && !ShouldUseManualProxy(rawURL, settings.Bypass) {
		return nil, "", nil
	}
	if pac := strings.TrimSpace(settings.PACURL); pac != "" {
		return nil, pac, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		if settings.HTTPSProxy != "" {
			proxyURL, err := osProxyURL("http", settings.HTTPSProxy)
			return proxyURL, "", err
		}
	default:
		if settings.HTTPProxy != "" {
			proxyURL, err := osProxyURL("http", settings.HTTPProxy)
			return proxyURL, "", err
		}
	}
	if settings.SOCKSProxy != "" {
		proxyURL, err := osProxyURL("socks5", settings.SOCKSProxy)
		return proxyURL, "", err
	}
	return nil, "", nil
}

func hostWithoutPort(host string) string {
	if index := strings.LastIndex(host, ":"); index >= 0 && !strings.Contains(host[index+1:], "]") {
		return host[:index]
	}
	return host
}

// osProxyURL builds the proxy URL. The stored value may already carry a scheme:
// both the registry and the GNOME panel accept one pasted in and keep it
// verbatim, and prefixing a second one produces a URL that parses and does not
// work.
func osProxyURL(scheme, hostPort string) (*url.URL, error) {
	value := strings.TrimSpace(hostPort)
	if value == "" {
		return nil, nil
	}
	if !strings.Contains(value, "://") {
		value = scheme + "://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Host == "" {
		return nil, nil
	}
	return parsed, nil
}

// ParseWindowsInternetSettings reads the values under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings.
//
// proxyServer takes two forms: a bare "host:port" that applies to every
// scheme, or the per-scheme "http=host:port;https=host:port;socks=host:port".
func ParseWindowsInternetSettings(proxyEnable uint64, proxyServer, proxyOverride, autoConfigURL string) OSProxySettings {
	settings := OSProxySettings{Enabled: proxyEnable != 0, PACURL: strings.TrimSpace(autoConfigURL)}
	server := strings.TrimSpace(proxyServer)
	if strings.Contains(server, "=") {
		for _, entry := range strings.Split(server, ";") {
			scheme, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch strings.ToLower(strings.TrimSpace(scheme)) {
			case "http":
				settings.HTTPProxy = value
			case "https":
				settings.HTTPSProxy = value
			case "socks":
				settings.SOCKSProxy = value
			}
		}
	} else if server != "" {
		settings.HTTPProxy, settings.HTTPSProxy = server, server
	}
	var bypass []string
	for _, entry := range strings.Split(proxyOverride, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.EqualFold(entry, "<local>") {
			settings.BypassLocal = true
			continue
		}
		bypass = append(bypass, entry)
	}
	settings.Bypass = strings.Join(bypass, ",")
	return settings
}

// gsettingsProxyValues is the raw output of the gsettings reads, still in
// GVariant syntax: strings are single-quoted, integers are bare, and a list of
// strings looks like ['a', 'b'].
type gsettingsProxyValues struct {
	Mode          string
	HTTPHost      string
	HTTPPort      string
	HTTPSHost     string
	HTTPSPort     string
	SOCKSHost     string
	SOCKSPort     string
	IgnoreHost    string
	AutoconfigURL string
}

// ParseGSettingsProxy reads the GNOME proxy configuration.
//
// Mode is authoritative: 'none' means direct however much stale host and port
// detail is left behind it, and there is always some.
func ParseGSettingsProxy(values gsettingsProxyValues) OSProxySettings {
	mode := strings.ToLower(unquoteGVariant(values.Mode))
	settings := OSProxySettings{}
	switch mode {
	case "auto":
		settings.PACURL = unquoteGVariant(values.AutoconfigURL)
		return settings
	case "manual":
		settings.Enabled = true
	default:
		return settings
	}
	settings.HTTPProxy = gsettingsHostPort(values.HTTPHost, values.HTTPPort)
	settings.HTTPSProxy = gsettingsHostPort(values.HTTPSHost, values.HTTPSPort)
	settings.SOCKSProxy = gsettingsHostPort(values.SOCKSHost, values.SOCKSPort)
	settings.Bypass = strings.Join(parseGVariantStringList(values.IgnoreHost), ",")
	return settings
}

func gsettingsHostPort(host, port string) string {
	cleanHost := strings.TrimSpace(unquoteGVariant(host))
	if cleanHost == "" {
		return ""
	}
	cleanPort := strings.TrimSpace(unquoteGVariant(port))
	number, err := strconv.Atoi(cleanPort)
	// Port 0 is what the panel leaves behind when a proxy row was cleared, and
	// it is not a port anything listens on.
	if err != nil || number <= 0 || number > 65535 {
		return ""
	}
	return cleanHost + ":" + strconv.Itoa(number)
}

func unquoteGVariant(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, "'"), "'")
	return strings.TrimSuffix(strings.TrimPrefix(trimmed, "\""), "\"")
}

func parseGVariantStringList(value string) []string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	entries := []string{}
	for _, entry := range strings.Split(trimmed, ",") {
		if cleaned := unquoteGVariant(entry); cleaned != "" {
			entries = append(entries, cleaned)
		}
	}
	return entries
}

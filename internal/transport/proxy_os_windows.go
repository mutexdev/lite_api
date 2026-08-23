//go:build windows

// Reading the Windows proxy configuration (US-061).
package transport

import (
	"golang.org/x/sys/windows/registry"
)

const windowsInternetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// readOSProxySettings reads HKCU Internet Settings, which is where the Windows
// proxy panel, Group Policy user preferences, and every corporate onboarding
// script put the machine's proxy.
//
// A missing key or value is an ordinary state -- a machine with no proxy has
// no ProxyServer -- so nothing here reports an error; it reports "nothing
// configured" and the request goes direct, exactly as it did before.
func readOSProxySettings() (OSProxySettings, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsInternetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return OSProxySettings{}, false
	}
	defer func() { _ = key.Close() }()
	proxyEnable, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil {
		proxyEnable = 0
	}
	proxyServer, _, _ := key.GetStringValue("ProxyServer")
	proxyOverride, _, _ := key.GetStringValue("ProxyOverride")
	autoConfigURL, _, _ := key.GetStringValue("AutoConfigURL")
	settings := ParseWindowsInternetSettings(proxyEnable, proxyServer, proxyOverride, autoConfigURL)
	return settings, settings.Configured()
}

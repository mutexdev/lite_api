//go:build !windows && !linux

// Platforms whose OS proxy configuration is read elsewhere or not at all
// (US-061).
//
// macOS has its own reader: scutil answers for the system proxy AND for the
// PAC URL, and that path predates this one and is tested. Everything else --
// the BSDs, anything unusual -- keeps the environment-variable behaviour it has
// always had.
package transport

func readOSProxySettings() (OSProxySettings, bool) {
	return OSProxySettings{}, false
}

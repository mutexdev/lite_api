//go:build linux

// Reading the Linux desktop proxy configuration (US-061).
package transport

import (
	"context"
	"os/exec"
	"time"
)

// gsettingsTimeout matches the macOS scutil call. A desktop settings daemon
// that does not answer promptly is not a reason to hold up a request.
const gsettingsTimeout = 2 * time.Second

// readOSProxySettings reads the GNOME proxy configuration, which Cinnamon,
// Budgie, Unity and most Ubuntu-derived desktops share, and which
// distribution-level "system proxy" tools write to.
//
// gsettings being absent is an ordinary outcome -- a KDE desktop, a container,
// a server, a Flatpak sandbox without the host session -- and produces "nothing
// configured" rather than an error.
func readOSProxySettings() (OSProxySettings, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gsettingsTimeout)
	defer cancel()
	read := func(schema, key string) string {
		output, err := exec.CommandContext(ctx, "gsettings", "get", schema, key).Output()
		if err != nil {
			return ""
		}
		return string(output)
	}
	mode := read("org.gnome.system.proxy", "mode")
	if mode == "" {
		return OSProxySettings{}, false
	}
	values := gsettingsProxyValues{
		Mode:          mode,
		IgnoreHost:    read("org.gnome.system.proxy", "ignore-hosts"),
		AutoconfigURL: read("org.gnome.system.proxy", "autoconfig-url"),
		HTTPHost:      read("org.gnome.system.proxy.http", "host"),
		HTTPPort:      read("org.gnome.system.proxy.http", "port"),
		HTTPSHost:     read("org.gnome.system.proxy.https", "host"),
		HTTPSPort:     read("org.gnome.system.proxy.https", "port"),
		SOCKSHost:     read("org.gnome.system.proxy.socks", "host"),
		SOCKSPort:     read("org.gnome.system.proxy.socks", "port"),
	}
	settings := ParseGSettingsProxy(values)
	return settings, settings.Configured()
}

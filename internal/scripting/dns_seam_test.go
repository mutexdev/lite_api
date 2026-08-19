// The parts of the DNS and client-seam helpers that do NOT need the network.
//
// I previously wrote these off as environment-bound and left them uncovered.
// That was too sweeping, and re-reading them showed it: scriptDNSSRVParts is
// pure string parsing with no lookup at all, and the two that do resolve
// VALIDATE their arguments first. The validation and the parsing are the parts a
// script can reach with bad input, and they were the parts untested.
package scripting

import (
	"net/http"
	"strings"
	"testing"
)

// SRV hostnames are _service._proto.name. Splitting them wrong sends the lookup
// to the wrong service, and the failure reads as "no such record" rather than
// as a parsing bug.
func TestDNSSRVPartsSplitsServiceProtoAndName(t *testing.T) {
	service, proto, name := scriptDNSSRVParts("_sip._tcp.example.com")
	if service != "sip" || proto != "tcp" || name != "example.com" {
		t.Fatalf("got (%q, %q, %q), want (sip, tcp, example.com)", service, proto, name)
	}
}

func TestDNSSRVPartsKeepsMultiLabelNames(t *testing.T) {
	_, _, name := scriptDNSSRVParts("_xmpp._tcp.a.b.example.com")
	if name != "a.b.example.com" {
		t.Fatalf("name = %q; the remaining labels must be rejoined, not truncated", name)
	}
}

// A trailing dot is legal in a fully-qualified name and must not shift the
// fields by one.
func TestDNSSRVPartsToleratesATrailingDot(t *testing.T) {
	service, proto, name := scriptDNSSRVParts("_sip._tcp.example.com.")
	if service != "sip" || proto != "tcp" || name != "example.com" {
		t.Fatalf("got (%q, %q, %q)", service, proto, name)
	}
}

// A plain hostname is not an SRV name. Returning it as the name with empty
// service and proto is what lets the caller tell the two apart.
func TestDNSSRVPartsPassesThroughNonSRVNames(t *testing.T) {
	for _, host := range []string{"example.com", "_tcp.example.com", "sip._tcp.example.com", ""} {
		service, proto, name := scriptDNSSRVParts(host)
		if service != "" || proto != "" {
			t.Errorf("%q was parsed as SRV: (%q, %q, %q)", host, service, proto, name)
		}
		if name != host {
			t.Errorf("%q came back as %q; a non-SRV name must pass through unchanged", host, name)
		}
	}
}

// Validation runs BEFORE any lookup, so these need no network.
func TestDNSHelpersRejectBadArgumentsWithoutResolving(t *testing.T) {
	if _, err := scriptDNSReverse("   "); err == nil {
		t.Error("a blank IP must be rejected, not sent to the resolver")
	}
	if _, err := scriptDNSLookupService("  ", 80); err == nil {
		t.Error("a blank address must be rejected")
	}
	for _, port := range []int{-1, 65536, 99999} {
		if _, err := scriptDNSLookupService("127.0.0.1", port); err == nil {
			t.Errorf("port %d is out of range and must be rejected before a lookup", port)
		}
	}
}

// The seam's nil guard. Without it, passing nil replaces the client with nil and
// every credential call — STS, SSO, OIDC — panics on the next request.
func TestSetHTTPClientIgnoresNil(t *testing.T) {
	original := httpClient
	defer func() { httpClient = original }()

	marker := &http.Client{}
	SetHTTPClient(func() *http.Client { return marker })
	if httpClient() != marker {
		t.Fatal("SetHTTPClient did not install the provided client")
	}

	SetHTTPClient(nil)
	if httpClient() != marker {
		t.Fatal("SetHTTPClient(nil) replaced the client; every credential call would panic")
	}
}

// Environment-dependent, so this asserts the invariant rather than a value: the
// OS version must never come back blank, because it is reported to scripts as
// os.version() and a blank string reads as a failed call.
func TestOSVersionIsNeverBlank(t *testing.T) {
	if strings.TrimSpace(scriptOSVersion()) == "" {
		t.Fatal("os.version() returned blank; the fallback path must produce something")
	}
}

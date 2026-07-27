// The posture of the script sandbox's outbound HTTP client.
//
// This exists because I read the seam wrong and want the next reader not to.
//
// internal/scripting exposes SetHTTPClient, and internal/core's init() wires
// awsv4.SetHTTPClient and transport.SetPACHTTPClient but NOT this one. That
// looks like an oversight — a seam production forgot to connect — and it is
// not.
//
// US-017 (internal/core/http_transport_cache.go, the "one-off clients" note)
// consolidated six one-off clients and
// says so explicitly: five exchange credentials "and the sixth is the script
// runtime's sendRequest", consolidated "WITHOUT adopting the user's proxy or
// TLS settings, and that is deliberate". Letting a collection's "disable SSL
// verification" toggle reach a script's outbound call would be a security
// regression dressed up as a refactor.
//
// The default here is &http.Client{Timeout: 30s} with a nil Transport, which IS
// http.DefaultTransport — the same object sharedCredentialHTTPClient resolves
// to for a pristine spec. So wiring the seam would change nothing observable,
// and leaving it unwired costs nothing. What follows pins that equivalence so a
// future change to the default is a failing test rather than a silent posture
// change.
package scripting

import (
	"net/http"
	"testing"
	"time"
)

func TestScriptHTTPClientUsesTheDefaultTransportPosture(t *testing.T) {
	client := httpClient()
	if client == nil {
		t.Fatal("script sandbox has no HTTP client")
	}

	// A nil Transport means http.DefaultTransport: always-verified TLS and
	// ProxyFromEnvironment. Anything else would mean the sandbox had quietly
	// adopted a posture of its own.
	if client.Transport != nil && client.Transport != http.DefaultTransport {
		t.Errorf("script client uses %#v; the sandbox must not carry its own TLS or proxy posture", client.Transport)
	}
	if client.Timeout != 30*time.Second {
		t.Errorf("timeout is %s, want 30s", client.Timeout)
	}
}

// A script that never returns must not hold a connection open forever. An
// unbounded client is the difference between one slow request and a wedged
// runtime.
func TestScriptHTTPClientAlwaysHasATimeout(t *testing.T) {
	if httpClient().Timeout <= 0 {
		t.Fatal("script sandbox client has no timeout")
	}
}

// The seam is what lets a test point the sandbox at a local server. Rejecting
// nil matters because a nil client would panic on the next request rather than
// fall back.
func TestSetHTTPClientInstallsAndRejectsNil(t *testing.T) {
	original := httpClient
	t.Cleanup(func() { httpClient = original })

	marker := &http.Client{Timeout: time.Second}
	SetHTTPClient(func() *http.Client { return marker })
	if httpClient() != marker {
		t.Fatal("SetHTTPClient did not install the client")
	}

	SetHTTPClient(nil)
	if httpClient() != marker {
		t.Fatal("SetHTTPClient(nil) replaced the installed client; every script request would panic")
	}
}

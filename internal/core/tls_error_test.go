// What a certificate failure tells the user (US-059).
//
// A request that works in Postman and fails here with
//
//	Post "https://api.internal": tls: failed to verify certificate:
//	x509: certificate signed by unknown authority
//
// is not a bug in either app: Postman ships with certificate verification OFF
// and this app verifies by default. Every remedy exists already -- the Verify
// TLS switch on the request, the SSL verification preference, the custom CA
// file -- and the message mentioned none of them, so the reasonable conclusion
// was that the app could not talk to the server at all.
package core

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sendToTestServer(t *testing.T, server *httptest.Server, prepare func(*RequestItem)) RequestItem {
	t.Helper()
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	url := server.URL
	patch := RequestPatch{URL: &url}
	if prepare != nil {
		settings := item.Settings
		probe := item
		probe.Settings = settings
		prepare(&probe)
		patch.Settings = &probe.Settings
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, patch); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	sent, ok := findItemInState(state, collection.ID, item.ID)
	if !ok {
		t.Fatal("request vanished from state")
	}
	return sent
}

func TestSelfSignedCertificateExplainsBothRemedies(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	item := sendToTestServer(t, server, nil)
	if item.Response == nil || item.Response.Error == "" {
		t.Fatal("an unverifiable certificate was accepted")
	}
	message := item.Response.Error
	lower := strings.ToLower(message)
	for _, expected := range []string{"certificate", "verify tls", "ca certificate"} {
		if !strings.Contains(lower, expected) {
			t.Errorf("message does not mention %q: %q", expected, message)
		}
	}
	if !strings.Contains(lower, "postman") {
		t.Errorf("message does not explain why Postman differs: %q", message)
	}
	if !strings.Contains(lower, "127.0.0.1") {
		t.Errorf("message does not name the host: %q", message)
	}
	// The machine detail still has to be there for a bug report.
	if !strings.Contains(lower, "x509") {
		t.Errorf("message dropped the underlying error: %q", message)
	}
}

func TestTurningVerificationOffMakesTheSameRequestSucceed(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	item := sendToTestServer(t, server, func(probe *RequestItem) { probe.Settings.VerifyTLS = false })
	if item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("the documented remedy did not work: %#v", item.Response)
	}
}

func TestCertificateDiagnosisRecognisesEachFailureKind(t *testing.T) {
	cases := map[string]error{
		"unknown authority": x509.UnknownAuthorityError{},
		"hostname":          x509.HostnameError{Host: "api.internal"},
		"expired":           x509.CertificateInvalidError{Reason: x509.Expired},
		"wrapped":           &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
	}
	for name, err := range cases {
		message, ok := describeTLSFailure(err, "https://api.internal/v1")
		if !ok {
			t.Fatalf("%s was not recognised as a certificate failure", name)
		}
		if !strings.Contains(message, "api.internal") {
			t.Errorf("%s: host missing from %q", name, message)
		}
		if !strings.Contains(strings.ToLower(message), "verify tls") {
			t.Errorf("%s: no remedy in %q", name, message)
		}
	}
}

func TestOrdinaryNetworkFailuresAreLeftAlone(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp 10.0.0.1:443: connect: connection refused"),
		errors.New("context deadline exceeded"),
	} {
		if message, ok := describeTLSFailure(err, "https://example.test"); ok {
			t.Fatalf("a non-certificate error was rewritten: %q", message)
		}
	}
}

func TestExpiredCertificateReasonIsNotSuggestedAsACAProblem(t *testing.T) {
	message, ok := describeTLSFailure(x509.CertificateInvalidError{Reason: x509.Expired}, "https://api.internal")
	if !ok {
		t.Fatal("expired certificate not recognised")
	}
	// An expired certificate is not fixed by adding a CA, and saying so sends
	// the user to install a root that will not help.
	if strings.Contains(strings.ToLower(message), "custom ca") {
		t.Fatalf("expiry blamed on the CA: %q", message)
	}
	if !strings.Contains(strings.ToLower(message), "expired") {
		t.Fatalf("expiry not named: %q", message)
	}
}

// Certificate failures, explained (US-059).
//
// The single most common "this works in Postman but not here" report is a
// certificate error, and it is not a bug in either app. Postman ships with SSL
// certificate verification switched OFF; this app verifies by default, which is
// the safer default and worth keeping. What was missing is any indication that
// the difference is a setting rather than a defect.
//
// Go's error for it reads:
//
//	Post "https://api.internal": tls: failed to verify certificate:
//	x509: certificate signed by unknown authority
//
// Every remedy already exists in the app -- Verify TLS on the request's
// Settings tab, SSL certificate verification in Preferences, and a custom CA
// file next to it -- and none of them was mentioned, so the reasonable reading
// was that the app simply could not reach the server.
package core

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// tlsFailureMarker prefixes a rewritten message so the UI can recognise one
// without re-inspecting the Go error, which it never sees.
const tlsFailureMarker = "TLS certificate verification failed"

// describeTLSFailure rewrites a certificate failure into something actionable,
// and reports whether the error was one.
//
// Anything that is not a certificate problem is left untouched: a connection
// refused or a timeout has nothing to do with verification, and offering to
// turn verification off for one would be advice that cannot work.
func describeTLSFailure(err error, requestURL string) (string, bool) {
	if err == nil {
		return "", false
	}
	reason, remedy, ok := classifyTLSFailure(err)
	if !ok {
		return "", false
	}
	host := tlsFailureHost(requestURL)
	message := fmt.Sprintf("%s for %s: %s.", tlsFailureMarker, host, reason)
	message += " " + remedy
	// Kept verbatim on its own line: a bug report needs the machine wording,
	// and a person searching the web for their exact problem will paste it.
	if detail := safeErrorText(err); detail != "" {
		message += "\n" + detail
	}
	return message, true
}

// classifyTLSFailure names the fault and the remedy that actually addresses it.
func classifyTLSFailure(err error) (string, string, bool) {
	const verificationRemedy = "The certificate is not signed by a CA this machine trusts. " +
		"Add the issuing CA under Preferences → Request → Custom CA certificate, " +
		"or switch off Verify TLS for this request on its Settings tab — " +
		"Postman verifies nothing by default, which is why the same request succeeds there."
	const trustRemedy = "Switch off Verify TLS for this request on its Settings tab if you trust this server anyway. " +
		"Postman verifies nothing by default, which is why the same request succeeds there."

	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return "the certificate is signed by an unknown authority", verificationRemedy, true
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return fmt.Sprintf("the certificate is not valid for %s", hostnameError.Host), trustRemedy, true
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		switch invalid.Reason {
		case x509.Expired:
			// Deliberately not offered a CA: adding a root does not make an
			// expired certificate valid, and suggesting it sends the user to
			// install something that cannot help.
			return "the certificate has expired or is not yet valid", trustRemedy, true
		case x509.NameMismatch:
			return "the certificate name does not match", trustRemedy, true
		default:
			return "the certificate is not valid", verificationRemedy, true
		}
	}
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return "the certificate could not be verified", verificationRemedy, true
	}
	// Older Go versions, and some proxy transports, surface the same condition
	// as an unwrapped string. Matched last so a typed error is always preferred.
	message := strings.ToLower(safeErrorText(err))
	if strings.Contains(message, "x509:") || strings.Contains(message, "tls: failed to verify certificate") {
		return "the certificate could not be verified", verificationRemedy, true
	}
	return "", "", false
}

// safeErrorText renders an error without letting a defective Error method take
// the app down. x509.HostnameError.Error dereferences its Certificate field
// unconditionally, so a transport that reports one without a certificate --
// which a proxy or a wrapped dialer can -- would panic inside what is only ever
// a diagnostic string.
func safeErrorText(err error) (text string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text = ""
		}
	}()
	return err.Error()
}

func tlsFailureHost(requestURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(requestURL))
	if err != nil || parsed == nil || parsed.Host == "" {
		return "the server"
	}
	return parsed.Host
}

// IsTLSVerificationFailure reports whether a recorded response error is the
// rewritten certificate message, so the UI can offer the remedies as controls
// rather than as prose the user has to follow by hand.
func IsTLSVerificationFailure(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), tlsFailureMarker)
}

// WS-Security UsernameToken.
//
// WHY THESE EXIST: the one test in internal/core built its expected digest by
// calling PasswordDigest, the function under test. Making PasswordDigest return
// the password unchanged failed nothing -- the fourth signer in this repo with
// that shape, after SigV4, OAuth 1.0a and Digest.
//
// Nothing below borrows the package's hashing.
package wsse

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestPasswordDigestIsBase64OfTheHexSHA1 pins what this actually emits.
//
// !! A DEVIATION FROM THE WSSE PROFILE, PINNED RATHER THAN FIXED !!
//
// The UsernameToken profile says PasswordDigest = Base64(SHA1(nonce + created +
// password)) over the RAW 20 digest bytes. This encodes the digest as hex FIRST
// and base64s that string, so the value is double-encoded and 40 bytes of hex
// rather than 20 of binary.
//
// I have pinned the existing behaviour instead of correcting it, deliberately.
// Changing it alters what goes out on the wire for every user with WSSE
// configured against a server that accepts today's format, and that is not a
// call to make silently as part of a refactor. The test documents the actual
// format so the choice is visible; whether to move to the profile form is a
// decision with a compatibility cost attached.
func TestPasswordDigestIsBase64OfTheHexSHA1(t *testing.T) {
	const (
		nonce    = "0123456789abcdef"
		created  = "2026-01-02T03:04:05.000Z"
		password = "secret"
	)
	sum := sha1.Sum([]byte(nonce + created + password))

	profileForm := base64.StdEncoding.EncodeToString(sum[:])
	actualForm := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(sum[:])))

	got := PasswordDigest(nonce, created, password)
	if got == profileForm {
		t.Fatal("PasswordDigest now matches the WSSE profile form; update this test and note the wire-format change")
	}
	if got != actualForm {
		t.Fatalf("PasswordDigest = %q, want %q (base64 of the hex digest)", got, actualForm)
	}
}

func TestPasswordDigestDependsOnEveryInput(t *testing.T) {
	base := PasswordDigest("n", "c", "p")
	for _, other := range []string{
		PasswordDigest("N", "c", "p"),
		PasswordDigest("n", "C", "p"),
		PasswordDigest("n", "c", "P"),
	} {
		if other == base {
			t.Fatal("changing an input left the digest unchanged")
		}
	}
}

func TestApplyHeaderEmitsEveryRequiredField(t *testing.T) {
	headers := http.Header{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ApplyHeader(headers, "ada", "secret", now)

	header := headers.Get("X-WSSE")
	if !strings.HasPrefix(header, "UsernameToken ") {
		t.Fatalf("X-WSSE = %q", header)
	}
	for _, field := range []string{`Username="ada"`, "PasswordDigest=", "Nonce=", `Created="2026-01-02T03:04:05.000Z"`} {
		if !strings.Contains(header, field) {
			t.Errorf("missing %s in %q", field, header)
		}
	}
}

// The timestamp must be UTC regardless of the clock handed in: a server
// comparing Created against its own window rejects a local-time value, and the
// failure looks like an intermittent auth error rather than a formatting bug.
func TestApplyHeaderNormalisesCreatedToUTC(t *testing.T) {
	headers := http.Header{}
	zone := time.FixedZone("UTC+9", 9*60*60)
	ApplyHeader(headers, "ada", "secret", time.Date(2026, 1, 2, 12, 0, 0, 0, zone))

	if !strings.Contains(headers.Get("X-WSSE"), `Created="2026-01-02T03:00:00.000Z"`) {
		t.Fatalf("Created was not converted to UTC: %q", headers.Get("X-WSSE"))
	}
}

// A quote in the username would otherwise close the attribute and let the rest
// be read as further attributes.
func TestApplyHeaderEscapesQuotesInTheUsername(t *testing.T) {
	headers := http.Header{}
	ApplyHeader(headers, `ada" Nonce="injected`, "secret", time.Now())

	header := headers.Get("X-WSSE")
	if strings.Count(header, `Nonce="`) != 1 {
		t.Fatalf("username quote was not escaped, header carries two Nonce attributes: %q", header)
	}
}

// A repeated nonce makes a captured X-WSSE header replayable within the
// server's timestamp window.
func TestNonceDiffersBetweenHeaders(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		headers := http.Header{}
		ApplyHeader(headers, "ada", "secret", time.Now())
		_, rest, _ := strings.Cut(headers.Get("X-WSSE"), `Nonce="`)
		nonce, _, _ := strings.Cut(rest, `"`)
		if nonce == "" || seen[nonce] {
			t.Fatalf("nonce %q missing or reused", nonce)
		}
		seen[nonce] = true
	}
}

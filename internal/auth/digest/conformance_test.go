// An independent check of the Digest response hash.
//
// WHY: the existing tests in internal/core build their expected value by calling
// MD5Hex -- the function under test. Proof that measured nothing: making MD5Hex
// return its input unchanged failed no test at all, so the response hash, the
// thing the whole scheme rests on, was unverified.
//
// This is the third signer in this repo with that shape, after SigV4 and
// OAuth 1.0a. Nothing below borrows the package's hashing: the digest is
// computed with crypto/md5 and checked against RFC 2617's published vector.
package digest

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func md5literal(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func headerParams(header string) map[string]string {
	params := map[string]string{}
	for _, field := range strings.Split(strings.TrimPrefix(header, "Digest "), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok {
			params[key] = strings.Trim(value, `"`)
		}
	}
	return params
}

// RFC 2617 section 3.5, the worked example. HA1 and HA2 are spelled out and the
// expected response is the literal from the RFC, so this fails if the algorithm
// drifts in any way -- wrong hash, wrong field order, wrong separator.
func TestMD5HexMatchesTheRFC2617Vector(t *testing.T) {
	const (
		username = "Mufasa"
		realm    = "testrealm@host.com"
		password = "Circle Of Life"
		nonce    = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
		uri      = "/dir/index.html"
		nc       = "00000001"
		cnonce   = "0a4f113b"
		want     = "6629fae49393a05397450978507c4ef1"
	)
	ha1 := md5literal(username + ":" + realm + ":" + password)
	ha2 := md5literal("GET:" + uri)
	got := md5literal(strings.Join([]string{ha1, nonce, nc, cnonce, "auth", ha2}, ":"))

	if got != want {
		t.Fatalf("the RFC 2617 vector does not reproduce: got %s, want %s", got, want)
	}
	// And the package's own hash must agree with crypto/md5.
	if MD5Hex(username+":"+realm+":"+password) != ha1 {
		t.Fatal("MD5Hex does not compute MD5")
	}
}

func TestMD5HexOfEmptyStringIsTheKnownDigest(t *testing.T) {
	if got := MD5Hex(""); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("MD5Hex(\"\") = %q", got)
	}
}

// The header the package produces must carry a response computed the RFC way.
// cnonce is random by design, so it is read back out of the header and fed into
// an independent computation rather than being fixed.
func TestAuthorizationHeaderResponseIsComputedPerRFC(t *testing.T) {
	const (
		username  = "Mufasa"
		password  = "Circle Of Life"
		realm     = "testrealm@host.com"
		nonce     = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
		uri       = "/dir/index.html"
		challenge = `Digest realm="testrealm@host.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="5ccc069c403ebaf9f0171e9517f40e41"`
	)
	auth := types.AuthConfig{Mode: "digest", Username: username, Password: password}

	header, err := AuthorizationHeader("GET", uri, auth, nil, challenge)
	if err != nil {
		t.Fatal(err)
	}
	params := headerParams(header)

	if params["qop"] != "auth" {
		t.Fatalf("qop = %q, want auth (the challenge offers auth and auth-int)", params["qop"])
	}
	if params["realm"] != realm || params["nonce"] != nonce {
		t.Fatalf("challenge values not echoed back: %#v", params)
	}
	if params["cnonce"] == "" || params["nc"] == "" {
		t.Fatalf("qop=auth requires cnonce and nc: %#v", params)
	}

	ha1 := md5literal(username + ":" + realm + ":" + password)
	ha2 := md5literal("GET:" + uri)
	want := md5literal(strings.Join([]string{ha1, nonce, params["nc"], params["cnonce"], "auth", ha2}, ":"))

	if params["response"] != want {
		t.Fatalf("response = %s, want %s", params["response"], want)
	}
}

// A repeated cnonce weakens the scheme in the same way a repeated OAuth nonce
// does: it makes a captured Authorization header replayable.
func TestClientNonceDiffersBetweenHeaders(t *testing.T) {
	const challenge = `Digest realm="r", qop="auth", nonce="n"`
	auth := types.AuthConfig{Mode: "digest", Username: "u", Password: "p"}

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		header, err := AuthorizationHeader("GET", "/x", auth, nil, challenge)
		if err != nil {
			t.Fatal(err)
		}
		cnonce := headerParams(header)["cnonce"]
		if seen[cnonce] {
			t.Fatalf("cnonce %q reused", cnonce)
		}
		seen[cnonce] = true
	}
}

// auth-int is not implemented, so offering only auth-int must not silently
// produce a header claiming qop=auth -- the server would reject it, or worse,
// accept a response computed over the wrong thing.
func TestChooseQopPrefersAuthAndRejectsUnknown(t *testing.T) {
	for input, want := range map[string]string{
		"auth": "auth", "auth,auth-int": "auth", " auth-int , auth ": "auth",
		"AUTH": "auth", "auth-int": "", "": "", "something": "",
	} {
		if got := ChooseQop(input); got != want {
			t.Errorf("ChooseQop(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseChallengeReadsQuotedAndBareValues(t *testing.T) {
	params := ParseChallenge(`Digest realm="a realm", qop=auth, nonce="abc", stale=false`)
	for key, want := range map[string]string{"realm": "a realm", "qop": "auth", "nonce": "abc", "stale": "false"} {
		if params[key] != want {
			t.Errorf("%s = %q, want %q", key, params[key], want)
		}
	}
}

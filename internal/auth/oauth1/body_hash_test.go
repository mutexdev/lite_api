// The oauth_body_hash parameter.
//
// A body hash binds a request's body to its signature, so a proxy cannot swap
// the body while the signature still verifies. It only does that if the digest
// matches the one the SERVER computes, and the server picks its algorithm from
// the signature method. A mismatch is not a local error: the request goes out,
// the server recomputes a different digest, and the response is a 401 that says
// nothing about which of the two sides is hashing differently.
package oauth1

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"testing"
)

func TestBodyHashFollowsTheSignatureMethodsDigest(t *testing.T) {
	body := `{"amount":100}`
	for _, method := range []string{"HMAC-SHA512", "RSA-SHA512"} {
		sum := sha512.Sum512([]byte(body))
		if got := BodyHash(body, method); got != base64.StdEncoding.EncodeToString(sum[:]) {
			t.Errorf("%s: got %s", method, got)
		}
	}
	for _, method := range []string{"HMAC-SHA256", "RSA-SHA256"} {
		sum := sha256.Sum256([]byte(body))
		if got := BodyHash(body, method); got != base64.StdEncoding.EncodeToString(sum[:]) {
			t.Errorf("%s: got %s", method, got)
		}
	}
}

// SHA-1 is the default because OAuth 1.0a's body-hash extension specifies it
// for HMAC-SHA1 and PLAINTEXT, which is what an unspecified method means. It is
// a compatibility requirement, not a security choice — the digest's job here is
// to match the server's, and the server will be using SHA-1.
func TestBodyHashDefaultsToSHA1(t *testing.T) {
	body := "grant_type=client_credentials"
	sum := sha1.Sum([]byte(body))
	want := base64.StdEncoding.EncodeToString(sum[:])
	for _, method := range []string{"HMAC-SHA1", "PLAINTEXT", "", "something-new"} {
		if got := BodyHash(body, method); got != want {
			t.Errorf("%q: got %s, want the SHA-1 digest", method, got)
		}
	}
}

// The method name is matched exactly. Lowercasing it is not a tolerance the
// spec offers, and quietly accepting "hmac-sha256" would produce a SHA-1 digest
// under a SHA-256 signature — the mismatch this whole test exists to catch.
func TestBodyHashDoesNotGuessAtMiscasedMethods(t *testing.T) {
	body := "x"
	sha1Sum := sha1.Sum([]byte(body))
	if got := BodyHash(body, "hmac-sha256"); got != base64.StdEncoding.EncodeToString(sha1Sum[:]) {
		t.Errorf("a miscased method produced %s; it should take the default rather than a guess", got)
	}
}

// An empty body still gets a hash: the digest of nothing is a real value, and
// omitting it would leave the body unbound for exactly the requests where a
// proxy could add one.
func TestBodyHashOfAnEmptyBodyIsTheDigestOfNothing(t *testing.T) {
	sum := sha1.Sum(nil)
	if got := BodyHash("", "HMAC-SHA1"); got != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Errorf("got %s", got)
	}
	if BodyHash("", "HMAC-SHA1") == "" {
		t.Error("an empty body produced no hash at all")
	}
}

// Base64, not hex: the parameter travels in an Authorization header where the
// spec says base64, and a hex digest would be rejected as a bad signature.
func TestBodyHashIsBase64(t *testing.T) {
	got := BodyHash("body", "HMAC-SHA256")
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("%s is not base64: %v", got, err)
	}
	if len(decoded) != sha256.Size {
		t.Errorf("decoded to %d bytes, want %d", len(decoded), sha256.Size)
	}
}

// Different bodies must not share a hash, which is the property the parameter
// exists for.
func TestBodyHashDistinguishesBodies(t *testing.T) {
	if BodyHash(`{"amount":100}`, "HMAC-SHA256") == BodyHash(`{"amount":900}`, "HMAC-SHA256") {
		t.Error("two different bodies hashed the same; the body is not bound to the signature")
	}
}

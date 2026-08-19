// Reading the RSA private key for an RSA-SHA* OAuth 1.0a signature.
//
// The key comes from a text box, so it arrives in whatever form the user copied
// it: PKCS#1 ("BEGIN RSA PRIVATE KEY"), PKCS#8 ("BEGIN PRIVATE KEY"), the wrong
// file entirely, or a certificate. Every rejection has to be an ERROR with a
// readable reason. The one outcome that must never happen is a nil key returned
// alongside a nil error, which the caller would sign with and panic on.
package oauth1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func pemBlock(t *testing.T, kind string, der []byte) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

func TestRSAKeyAcceptsBothPKCS1AndPKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	for name, encoded := range map[string]string{
		"pkcs1": pemBlock(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)),
		"pkcs8": pemBlock(t, "PRIVATE KEY", pkcs8),
	} {
		parsed, err := parseOAuth1RSAPrivateKey(encoded)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if parsed == nil {
			t.Fatalf("%s: nil key with nil error; the caller would sign with it and panic", name)
		}
		if parsed.N.Cmp(key.N) != 0 {
			t.Errorf("%s: parsed a different key", name)
		}
	}
}

// The PEM header is not what decides the format — openssl and several key
// managers write a PKCS#8 body under an "RSA PRIVATE KEY" label. Trusting the
// label would reject a key that is perfectly usable.
func TestRSAKeyIgnoresAMisleadingPEMLabel(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseOAuth1RSAPrivateKey(pemBlock(t, "RSA PRIVATE KEY", pkcs8)); err != nil {
		t.Errorf("a PKCS#8 body under a PKCS#1 label was rejected: %v", err)
	}
	if _, err := parseOAuth1RSAPrivateKey(pemBlock(t, "PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))); err != nil {
		t.Errorf("a PKCS#1 body under a PKCS#8 label was rejected: %v", err)
	}
}

// An EC key parses as valid PKCS#8, so the only thing standing between it and a
// nil-pointer dereference in the signer is the type assertion being checked.
func TestRSAKeyRejectsANonRSAKeyWithoutPanicking(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}

	key, err := parseOAuth1RSAPrivateKey(pemBlock(t, "PRIVATE KEY", der))
	if err == nil {
		t.Fatal("an EC key was accepted as RSA")
	}
	if key != nil {
		t.Error("a key was returned alongside the error")
	}
	if !strings.Contains(err.Error(), "not an RSA key") {
		t.Errorf("error was %q; it should say which way the key is wrong", err)
	}
}

func TestRSAKeyRejectsWhatIsNotAKey(t *testing.T) {
	for name, input := range map[string]string{
		"empty":          "",
		"plain text":     "paste your key here",
		"headers only":   "-----BEGIN PRIVATE KEY-----\n-----END PRIVATE KEY-----\n",
		"corrupt base64": "-----BEGIN PRIVATE KEY-----\nbm90IGEga2V5\n-----END PRIVATE KEY-----\n",
		"a certificate":  pemBlock(t, "CERTIFICATE", []byte("not a key")),
	} {
		key, err := parseOAuth1RSAPrivateKey(input)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		if key != nil {
			t.Errorf("%s: returned a key alongside the error", name)
		}
	}
}

// A key that is damaged and a key that is the wrong ALGORITHM are different
// problems with different fixes: re-copy it, versus generate an RSA one. An
// unchecked parse error collapses both into "is not an RSA key", which sends
// someone off to regenerate a key that was fine.
func TestRSAKeyDistinguishesCorruptFromWrongAlgorithm(t *testing.T) {
	_, err := parseOAuth1RSAPrivateKey("-----BEGIN PRIVATE KEY-----\nbm90IGEga2V5\n-----END PRIVATE KEY-----\n")
	if err == nil {
		t.Fatal("corrupt DER accepted")
	}
	if strings.Contains(err.Error(), "not an RSA key") {
		t.Errorf("error was %q; the key is unreadable, not the wrong algorithm", err)
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error was %q; it should say it could not be parsed", err)
	}
}

// "must be PEM encoded" tells the user what to do. A wrapped x509 parse error
// about ASN.1 tags does not.
func TestRSAKeyNamesPEMSpecificallyWhenThereIsNoPEMBlock(t *testing.T) {
	_, err := parseOAuth1RSAPrivateKey("paste your key here")
	if err == nil || !strings.Contains(err.Error(), "PEM") {
		t.Errorf("error was %v; it should name PEM so the user knows what is missing", err)
	}
}

// Keys pasted into a text box arrive with surrounding whitespace. pem.Decode
// wants the BEGIN marker at the start of a line, so a leading space alone used
// to produce "must be PEM encoded" for a key that visibly is one.
//
// This does NOT extend to a key indented on every line — a PEM block pasted out
// of an indented YAML document still fails, and the error names PEM.
func TestRSAKeyToleratesSurroundingWhitespace(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	padded := "\n\n  " + pemBlock(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)) + "\n  \n"
	if _, err := parseOAuth1RSAPrivateKey(padded); err != nil {
		t.Errorf("a padded key was rejected: %v", err)
	}
}

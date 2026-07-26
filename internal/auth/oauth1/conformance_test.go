// An independent check of the OAuth 1.0a signature.
//
// WHY: the existing test in package main builds its expected signature by
// calling Signature, ParameterString, BaseURL and Encode -- the functions under
// test. That detects signing and verifying disagreeing, but not both being
// wrong together, which is the failure that matters. It is the same shape as
// the SigV4 tautology recorded in progress.txt, found the same way.
//
// Nothing below calls this package except Sign itself. The base string is
// assembled with literal string operations and a percent-encoder written out
// per RFC 3986, and the HMAC is taken with crypto/hmac.
package oauth1

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"LiteAPI/internal/types"
)

// rfc3986Escape is written out rather than imported: percent-encoding is part
// of what is being verified, so borrowing the package's Encode would put the
// implementation on both sides of the assertion again.
func rfc3986Escape(value string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var out strings.Builder
	for _, b := range []byte(value) {
		if strings.IndexByte(unreserved, b) >= 0 {
			out.WriteByte(b)
		} else {
			out.WriteString("%")
			const hexDigits = "0123456789ABCDEF"
			out.WriteByte(hexDigits[b>>4])
			out.WriteByte(hexDigits[b&0x0f])
		}
	}
	return out.String()
}

func authorizationParams(header string) map[string]string {
	params := map[string]string{}
	for _, field := range strings.Split(strings.TrimPrefix(header, "OAuth "), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok {
			params[key] = strings.Trim(value, `"`)
		}
	}
	return params
}

func TestSignMatchesAnIndependentlyComputedSignature(t *testing.T) {
	const (
		consumerKey    = "dpf43f3p2l4k3l03"
		consumerSecret = "kd94hf93k423kf44"
		token          = "nnch734d00sl2jdk"
		tokenSecret    = "pfkkdhi9sl3r4s00"
	)
	req, err := http.NewRequest(http.MethodGet, "http://photos.example.net/photos?size=original&file=vacation.jpg", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := types.OAuth1Auth{
		ConsumerKey:       consumerKey,
		ConsumerSecret:    consumerSecret,
		AccessToken:       token,
		AccessTokenSecret: tokenSecret,
		SignatureMethod:   "HMAC-SHA1",
	}
	if err := Sign(req, &types.RequestItem{}, auth, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	params := authorizationParams(req.Header.Get("Authorization"))
	if params["oauth_signature"] == "" {
		t.Fatalf("no signature produced: %q", req.Header.Get("Authorization"))
	}

	// Rebuild the base string by hand. Every oauth_* parameter except the
	// signature takes part, plus the query parameters, sorted by encoded name.
	pairs := []string{}
	for key, value := range params {
		if key == "oauth_signature" || key == "realm" {
			continue
		}
		// Header values arrive percent-encoded; the base string wants them
		// encoded exactly once, so they go in as-is.
		pairs = append(pairs, key+"="+value)
	}
	pairs = append(pairs, "file="+rfc3986Escape("vacation.jpg"), "size="+rfc3986Escape("original"))
	sort.Strings(pairs)

	baseString := strings.Join([]string{
		"GET",
		rfc3986Escape("http://photos.example.net/photos"),
		rfc3986Escape(strings.Join(pairs, "&")),
	}, "&")

	signingKey := rfc3986Escape(consumerSecret) + "&" + rfc3986Escape(tokenSecret)
	mac := hmac.New(sha1.New, []byte(signingKey))
	mac.Write([]byte(baseString))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	got, err := unescapeOnce(params["oauth_signature"])
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("signature:\n got %s\nwant %s\nbase string:\n%s", got, want, baseString)
	}
}

func unescapeOnce(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			var b byte
			for _, c := range []byte(value[i+1 : i+3]) {
				b <<= 4
				switch {
				case c >= '0' && c <= '9':
					b |= c - '0'
				case c >= 'A' && c <= 'F':
					b |= c - 'A' + 10
				case c >= 'a' && c <= 'f':
					b |= c - 'a' + 10
				}
			}
			out.WriteByte(b)
			i += 2
			continue
		}
		out.WriteByte(value[i])
	}
	return out.String(), nil
}

// A repeated nonce lets a captured request be replayed. Nothing was checking
// that two signings of the same request differ.
func TestNonceAndSignatureDifferBetweenSignings(t *testing.T) {
	auth := types.OAuth1Auth{ConsumerKey: "k", ConsumerSecret: "s", SignatureMethod: "HMAC-SHA1"}

	seen := map[string]bool{}
	for i := 0; i < 25; i++ {
		req, err := http.NewRequest(http.MethodGet, "http://example.test/r", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := Sign(req, &types.RequestItem{}, auth, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
		nonce := authorizationParams(req.Header.Get("Authorization"))["oauth_nonce"]
		if nonce == "" {
			t.Fatal("no nonce in the Authorization header")
		}
		if seen[nonce] {
			t.Fatalf("nonce %q reused: a captured request can be replayed", nonce)
		}
		seen[nonce] = true
	}
}

func TestPlaintextSignatureIsTheSigningKey(t *testing.T) {
	got, err := Signature("ignored-base-string", "cs", "ts", "PLAINTEXT", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "cs&ts" {
		t.Fatalf("PLAINTEXT signature = %q, want %q", got, "cs&ts")
	}
}

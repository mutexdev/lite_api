// An independent check of the SigV4 algorithm.
//
// WHY THIS EXISTS: assertAWSV4Signature -- the assertion every other AWS test
// here relies on -- verifies a signature by RECOMPUTING it with the same
// functions the signer used. That detects an inconsistency between signing and
// verifying, but it cannot detect a wrong algorithm: change awsSigningKey's
// "AWS4" prefix to "AWS5" and both sides change together and every test still
// passes. I confirmed that by doing it.
//
// So this test shares nothing with the implementation. The canonical request is
// written out as a literal, and the signing key is derived with bare
// crypto/hmac. If the package's canonicalisation or key derivation drifts from
// the specification, this fails and nothing else does.
package awsv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"LiteAPI/internal/types"
)

func TestSignMatchesAnIndependentlyComputedSignature(t *testing.T) {
	// AWS's own documented example credentials, region and service.
	const (
		accessKeyID = "AKIDEXAMPLE"
		secretKey   = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		region      = "us-east-1"
		service     = "service"
		amzDate     = "20150830T123600Z"
		dateStamp   = "20150830"
		// SHA-256 of the empty string: this request has no body.
		emptyPayload = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	)

	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"

	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	auth := types.AWSV4Auth{AccessKeyID: accessKeyID, SecretAccessKey: secretKey, Region: region, Service: service}
	if err := Sign(req, auth, now, func(value string) string { return value }); err != nil {
		t.Fatal(err)
	}

	if got := req.Header.Get("X-Amz-Content-Sha256"); got != emptyPayload {
		t.Fatalf("payload hash for an empty body: got %s, want %s", got, emptyPayload)
	}
	if got := req.Header.Get("X-Amz-Date"); got != amzDate {
		t.Fatalf("x-amz-date: got %s, want %s", got, amzDate)
	}

	// Written out by hand, per the SigV4 specification. Nothing here calls the
	// package: the point is to fail if the package's own canonicalisation drifts.
	canonicalRequest := strings.Join([]string{
		"GET",
		"/",
		"",
		"host:example.amazonaws.com",
		"x-amz-content-sha256:" + emptyPayload,
		"x-amz-date:" + amzDate,
		"",
		"host;x-amz-content-sha256;x-amz-date",
		emptyPayload,
	}, "\n")

	hash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		dateStamp + "/" + region + "/" + service + "/aws4_request",
		hex.EncodeToString(hash[:]),
	}, "\n")

	mac := func(key []byte, value string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(value))
		return h.Sum(nil)
	}
	key := mac(mac(mac(mac([]byte("AWS4"+secretKey), dateStamp), region), service), "aws4_request")
	want := hex.EncodeToString(mac(key, stringToSign))

	header := req.Header.Get("Authorization")
	wantHeader := "AWS4-HMAC-SHA256 Credential=" + accessKeyID + "/" + dateStamp + "/" + region + "/" + service +
		"/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + want
	if header != wantHeader {
		t.Fatalf("Authorization header:\n got %s\nwant %s\ncanonical request:\n%s", header, wantHeader, canonicalRequest)
	}
}

// SigV4 requires header values to be trimmed and internal whitespace runs
// collapsed to a single space before signing. The test above cannot see that
// rule -- none of its header values contain a run of spaces -- so removing the
// normalisation left it passing. This one signs a header that does.
func TestSignCollapsesWhitespaceInSignedHeaderValues(t *testing.T) {
	const (
		accessKeyID  = "AKIDEXAMPLE"
		secretKey    = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		region       = "us-east-1"
		service      = "service"
		amzDate      = "20150830T123600Z"
		dateStamp    = "20150830"
		emptyPayload = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	)

	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"
	// Leading, trailing and repeated spaces: all three must be normalised away.
	req.Header.Set("X-Amz-Meta-Note", "  alpha   beta  ")

	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	auth := types.AWSV4Auth{AccessKeyID: accessKeyID, SecretAccessKey: secretKey, Region: region, Service: service}
	if err := Sign(req, auth, now, func(value string) string { return value }); err != nil {
		t.Fatal(err)
	}

	canonicalRequest := strings.Join([]string{
		"GET",
		"/",
		"",
		"host:example.amazonaws.com",
		"x-amz-content-sha256:" + emptyPayload,
		"x-amz-date:" + amzDate,
		"x-amz-meta-note:alpha beta", // the whole point of this test
		"",
		"host;x-amz-content-sha256;x-amz-date;x-amz-meta-note",
		emptyPayload,
	}, "\n")

	hash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		dateStamp + "/" + region + "/" + service + "/aws4_request",
		hex.EncodeToString(hash[:]),
	}, "\n")

	mac := func(key []byte, value string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(value))
		return h.Sum(nil)
	}
	key := mac(mac(mac(mac([]byte("AWS4"+secretKey), dateStamp), region), service), "aws4_request")
	want := hex.EncodeToString(mac(key, stringToSign))

	if got := signatureOf(req.Header.Get("Authorization")); got != want {
		t.Fatalf("signature over a whitespace-heavy header:\n got %s\nwant %s\ncanonical request:\n%s", got, want, canonicalRequest)
	}
}

func signatureOf(header string) string {
	_, sig, _ := strings.Cut(header, "Signature=")
	return sig
}

// The small dispatch functions behind the script crypto and encoding API.
//
// Each is a switch that maps a name a script wrote — an algorithm, an encoding,
// a compression level — onto the thing that does the work. They are the places
// where a script asking for one primitive can quietly get another.
package scripting

import (
	"compress/flate"
	"encoding/base64"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func TestJWTSigningMethodsAreTheHMACFamily(t *testing.T) {
	// Case-insensitive but not trimmed: this comes from a script's own options
	// object, not from a text box, so a stray space means the script wrote one.
	for _, algorithm := range []string{"HS256", "hs256", "HS384", "HS512"} {
		method, err := scriptJWTSigningMethod(algorithm)
		if err != nil || method == nil {
			t.Errorf("%q: %v", algorithm, err)
		}
	}
	if method, _ := scriptJWTSigningMethod("HS384"); method.Alg() != "HS384" {
		t.Errorf("HS384 produced %s", method.Alg())
	}
	// Omitted algorithm defaults to HS256 rather than erroring, since a script
	// that only supplies a secret means the ordinary case.
	method, err := scriptJWTSigningMethod("")
	if err != nil || method.Alg() != "HS256" {
		t.Errorf("default gave %v %v", method, err)
	}
}

// "alg": "none" is the original JWT forgery: a token signed with nothing
// verifies against any key. It has to be an ERROR, not a method that signs an
// empty signature. Asymmetric algorithms are likewise refused rather than
// silently HMAC'd with the public key as the secret, which is the other classic
// confusion attack.
func TestJWTSigningRefusesNoneAndAsymmetricAlgorithms(t *testing.T) {
	for _, algorithm := range []string{"none", "NONE", "RS256", "ES256", "PS512", "HS128", "garbage"} {
		method, err := scriptJWTSigningMethod(algorithm)
		if err == nil {
			t.Errorf("%q was accepted", algorithm)
		}
		if method != nil {
			t.Errorf("%q returned a signing method alongside the error", algorithm)
		}
	}
}

func TestAESKeyLengthsMatchTheirAlgorithms(t *testing.T) {
	for algorithm, want := range map[string]int{
		"aes-128-cbc":   16,
		"AES-192-CBC":   24,
		"aes256cbc":     32,
		"aes_128_cbc":   16,
		" aes-256-cbc ": 32,
	} {
		got, err := scriptAESCBCKeyLength(algorithm)
		if err != nil || got != want {
			t.Errorf("%s: got %d %v, want %d", algorithm, got, err, want)
		}
	}
}

// An unknown cipher must error rather than fall back to a length. Returning 16
// for an unrecognised name would encrypt with AES-128 under a name the script
// believed meant something stronger.
func TestAESKeyLengthRefusesUnknownCiphers(t *testing.T) {
	for _, algorithm := range []string{"aes-512-cbc", "aes-128-gcm", "des-cbc", ""} {
		if got, err := scriptAESCBCKeyLength(algorithm); err == nil {
			t.Errorf("%q was accepted as %d bytes", algorithm, got)
		}
	}
}

// The alphabet and the padding both vary in the wild, and all four combinations
// turn up: standard base64 from an API, unpadded base64url from a JWT segment.
// Failing one of them makes a decode fail on input that is entirely valid.
func TestBase64DecodingAcceptsEveryCommonVariant(t *testing.T) {
	payload := []byte{0xfb, 0xff, 0xfe, 0x01, 0x02}
	for name, encoded := range map[string]string{
		"standard padded": base64.StdEncoding.EncodeToString(payload),
		"standard raw":    base64.RawStdEncoding.EncodeToString(payload),
		"url-safe padded": base64.URLEncoding.EncodeToString(payload),
		"url-safe raw":    base64.RawURLEncoding.EncodeToString(payload),
	} {
		decoded, err := decodeScriptBase64(encoded)
		if err != nil {
			t.Errorf("%s (%s): %v", name, encoded, err)
			continue
		}
		if string(decoded) != string(payload) {
			t.Errorf("%s: round trip gave %v", name, decoded)
		}
	}
}

// A JWT segment is unpadded base64url specifically, and decoding one is the
// most common thing a script does with this.
func TestBase64DecodesAJWTSegment(t *testing.T) {
	decoded, err := decodeScriptBase64("eyJhbGciOiJIUzI1NiJ9")
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != `{"alg":"HS256"}` {
		t.Errorf("got %q", decoded)
	}
}

func TestBase64DecodingTrimsSurroundingWhitespaceAndRejectsRubbish(t *testing.T) {
	if _, err := decodeScriptBase64("  YWJj  "); err != nil {
		t.Errorf("padded whitespace rejected: %v", err)
	}
	if _, err := decodeScriptBase64("not base64!!"); err == nil {
		t.Error("invalid input was accepted")
	}
}

// A binary string is how JavaScript carries bytes in a string: one code unit per
// byte. The round trip has to be exact for every byte value, including those
// above 0x7f, which are the ones a UTF-8 conversion would mangle into multiple
// bytes.
func TestBinaryStringRoundTripsEveryByteValue(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	encoded := scriptBinaryStringFromBytes(all)
	decoded, err := scriptBytesFromBinaryString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 256 {
		t.Fatalf("round trip produced %d bytes, want 256", len(decoded))
	}
	for i, value := range decoded {
		if value != byte(i) {
			t.Fatalf("byte %d came back as %d", i, value)
		}
	}
}

// Anything above 0xff is not a byte, and accepting it would truncate silently —
// writing 0x41 for U+0141 and corrupting the data with no sign of it.
func TestBinaryStringRejectsCharactersAboveAByte(t *testing.T) {
	if _, err := scriptBytesFromBinaryString("café Ł"); err == nil {
		t.Error("a character above 0xff was accepted as a byte")
	}
}

// zlib and brotli number their levels differently, and -1 means "default" in
// zlib but is not a legal brotli level at all. Passing it through unmapped is a
// compressor error at runtime rather than a compressed body.
func TestBrotliLevelMappingClampsIntoRange(t *testing.T) {
	if got := scriptZlibBrotliLevel(flate.DefaultCompression); got != brotli.DefaultCompression {
		t.Errorf("zlib default mapped to %d", got)
	}
	if got := scriptZlibBrotliLevel(-99); got != brotli.BestSpeed {
		t.Errorf("below range gave %d, want clamped to %d", got, brotli.BestSpeed)
	}
	if got := scriptZlibBrotliLevel(99); got != brotli.BestCompression {
		t.Errorf("above range gave %d, want clamped to %d", got, brotli.BestCompression)
	}
	if got := scriptZlibBrotliLevel(5); got != 5 {
		t.Errorf("an in-range level was changed to %d", got)
	}
}

// os.type() reports the names Node reports, because scripts branch on them.
// "Windows_NT" in particular is what Node returns on Windows, and a script
// checking for it would take the wrong branch on any other spelling.
func TestOSTypeUsesNodesNames(t *testing.T) {
	got := scriptOSType()
	for _, name := range []string{"Darwin", "Linux", "Windows_NT"} {
		if got == name {
			return
		}
	}
	t.Logf("running on %s, which has no Node name to match", got)
}

func TestJWTDurationsAcceptNumbersAndUnits(t *testing.T) {
	for name, tc := range map[string]struct {
		value interface{}
		want  time.Duration
	}{
		"int seconds":    {int(60), time.Minute},
		"int64 seconds":  {int64(60), time.Minute},
		"float seconds":  {float64(60), time.Minute},
		"string seconds": {"60", time.Minute},
		"empty string":   {"", 0},
	} {
		if got := parseScriptJWTDuration(tc.value); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

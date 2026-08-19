package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xport "github.com/mutexdev/lite_api/internal/transport"
)

// Custom root CAs decide WHICH CERTIFICATE AUTHORITIES ARE TRUSTED for a
// request. Both implementations of that decision were at 0% coverage:
//
//   - applyCustomRootCAsToTLSConfig (app_execute_http.go), used by the direct
//     HTTP path and by gRPC;
//   - transport.ApplyCustomRootCAPEM, whose own comment describes it as "the
//     body, restated" for the transport cache, which reads the PEM separately
//     so the cache key reflects the file's current content.
//
// Nothing verified that the restatement stayed faithful, and it had already
// drifted — see TestBothCustomRootCAPathsAgree.

// testCA is a self-signed CA plus a leaf it signed. Generated rather than
// fixtured, and the leaf is the point: verifying it against the resulting pool
// proves the CA was installed as a TRUST ANCHOR. Counting the pool's entries
// would not — CertPool.Subjects is deprecated precisely because it reports
// nothing for a pool derived from the system store, so a count-based assertion
// silently measures the wrong thing on macOS and Windows.
type testCA struct {
	pem  []byte
	leaf *x509.Certificate
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "liteapi-custom-root-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "leaf.liteapi.test"},
		DNSNames:     []string{"leaf.liteapi.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		leaf: leaf,
	}
}

// trusts reports whether the pool accepts this CA's leaf as chaining to a
// trusted root — the actual question a RootCAs pool exists to answer.
func (ca testCA) trusts(t *testing.T, pool *x509.CertPool) bool {
	t.Helper()
	_, err := ca.leaf.Verify(x509.VerifyOptions{
		Roots:       pool,
		DNSName:     "leaf.liteapi.test",
		CurrentTime: time.Now(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err == nil
}

func writePEM(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func customCAPreferences(path string, keepDefaults bool) RequestPreferences {
	prefs := RequestPreferences{}
	prefs.CustomCaCertificate.Enabled = true
	prefs.CustomCaCertificate.FilePath = path
	prefs.KeepDefaultCaCertificates.Enabled = &keepDefaults
	return prefs
}

func TestCustomRootCABecomesATrustAnchor(t *testing.T) {
	ca := newTestCA(t)
	path := writePEM(t, t.TempDir(), "ca.pem", ca.pem)

	cfg := &tls.Config{}
	if err := applyCustomRootCAsToTLSConfig(cfg, customCAPreferences(path, false)); err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("RootCAs is nil, which means the SYSTEM pool — the custom CA was not installed")
	}
	if !ca.trusts(t, cfg.RootCAs) {
		t.Error("a leaf signed by the custom CA is not trusted by the resulting pool")
	}
	// And the converse, so the check above is not merely asserting that
	// verification succeeds for everything.
	if ca.trusts(t, x509.NewCertPool()) {
		t.Error("an empty pool trusted the leaf, so trusts() proves nothing")
	}
}

// KeepDefaultCaCertificates decides whether the custom CA is ADDED to the
// system anchors or REPLACES them. Getting this backwards either breaks every
// ordinary HTTPS request the moment one custom CA is configured, or silently
// keeps trusting the whole machine store when the user asked for exactly one
// anchor.
func TestKeepDefaultCACertificatesChoosesAddOrReplace(t *testing.T) {
	systemPool, err := x509.SystemCertPool()
	if err != nil {
		t.Skipf("no system pool on this platform: %v", err)
	}

	ca := newTestCA(t)
	path := writePEM(t, t.TempDir(), "ca.pem", ca.pem)

	kept := &tls.Config{}
	if err := applyCustomRootCAsToTLSConfig(kept, customCAPreferences(path, true)); err != nil {
		t.Fatal(err)
	}
	replaced := &tls.Config{}
	if err := applyCustomRootCAsToTLSConfig(replaced, customCAPreferences(path, false)); err != nil {
		t.Fatal(err)
	}

	// Both must anchor the custom CA, whichever way the flag goes.
	if !ca.trusts(t, kept.RootCAs) {
		t.Error("keeping the defaults dropped the custom CA")
	}
	if !ca.trusts(t, replaced.RootCAs) {
		t.Error("replacing the defaults dropped the custom CA")
	}

	// The two pools must not be the same object or the same contents, or the
	// flag is doing nothing. CertPool.Equal compares contents and, unlike
	// Subjects, is meaningful for a system-derived pool.
	if kept.RootCAs.Equal(replaced.RootCAs) {
		t.Error("keeping and replacing the default CAs produced identical pools")
	}
	// The kept pool is the system one extended; the replacing pool is not.
	if replaced.RootCAs.Equal(systemPool) {
		t.Error("the replacing pool equals the bare system pool")
	}
}

// A file that holds no certificates must be REFUSED. Accepting it would leave
// RootCAs nil, and a nil RootCAs means the SYSTEM pool — so a request the user
// configured to trust one specific CA would instead trust every CA on the
// machine.
func TestCustomRootCARefusesAFileThatHoldsNoCertificates(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string][]byte{
		"junk.pem":  []byte("not a certificate"),
		"empty.pem": nil,
		"header.pem": []byte(
			"-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----\n"),
	} {
		path := writePEM(t, dir, name, content)
		cfg := &tls.Config{}
		err := applyCustomRootCAsToTLSConfig(cfg, customCAPreferences(path, false))
		if err == nil {
			t.Errorf("%s was accepted; RootCAs set = %v", name, cfg.RootCAs != nil)
			continue
		}
		if !strings.Contains(err.Error(), "did not contain PEM certificates") {
			t.Errorf("%s: error = %v, want the no-certificates message", name, err)
		}
		if cfg.RootCAs != nil {
			t.Errorf("%s: RootCAs was set despite the error", name)
		}
	}
}

func TestCustomRootCAReportsAnUnreadableFile(t *testing.T) {
	cfg := &tls.Config{}
	err := applyCustomRootCAsToTLSConfig(cfg, customCAPreferences(filepath.Join(t.TempDir(), "absent.pem"), false))
	if err == nil {
		t.Fatal("a missing CA file was accepted")
	}
	if !strings.Contains(err.Error(), "read custom CA certificate") {
		t.Errorf("error = %v, want the read failure to be named", err)
	}
}

// Disabled, or enabled with no path, is "no custom CA" and must leave the
// config untouched rather than erroring — the setting is present in every
// preferences payload whether the user has filled it in or not.
func TestCustomRootCAIsSkippedWhenNotConfigured(t *testing.T) {
	ca := newTestCA(t)
	path := writePEM(t, t.TempDir(), "ca.pem", ca.pem)

	disabled := RequestPreferences{}
	disabled.CustomCaCertificate.Enabled = false
	disabled.CustomCaCertificate.FilePath = path

	for name, prefs := range map[string]RequestPreferences{
		"disabled":   disabled,
		"blank path": customCAPreferences("   ", false),
	} {
		cfg := &tls.Config{}
		if err := applyCustomRootCAsToTLSConfig(cfg, prefs); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if cfg.RootCAs != nil {
			t.Errorf("%s: RootCAs was set when no custom CA is configured", name)
		}
	}
}

func TestCustomRootCAToleratesANilConfig(t *testing.T) {
	ca := newTestCA(t)
	path := writePEM(t, t.TempDir(), "ca.pem", ca.pem)
	if err := applyCustomRootCAsToTLSConfig(nil, customCAPreferences(path, false)); err != nil {
		t.Errorf("a nil config should be a no-op, got %v", err)
	}
}

// THE CONTRACT TEST, and the reason the divergence below was found.
//
// transport.ApplyCustomRootCAPEM says it is applyCustomRootCAsToTLSConfig's
// body restated, with "same error strings". Two implementations of a trust
// decision, in different packages, with nothing checking they agree. This runs
// both over the same inputs and requires the same outcome from each.
//
// It FAILED when first written. With a configured but EMPTY CA file the
// restated version returned no error and left RootCAs nil — which selects the
// system pool. A request configured with KeepDefaultCaCertificates=false, i.e.
// "trust only my CA", silently trusted every CA on the machine instead, and
// only on the cached-transport path. The restated version gated its early
// return on the PEM being empty rather than on the path being unset; those two
// differ in exactly the case where a configured file holds nothing.
func TestBothCustomRootCAPathsAgree(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	good := writePEM(t, dir, "ca.pem", ca.pem)
	empty := writePEM(t, dir, "empty.pem", nil)
	junk := writePEM(t, dir, "junk.pem", []byte("not a certificate"))

	for _, keepDefaults := range []bool{true, false} {
		for _, path := range []string{good, empty, junk, ""} {
			prefs := customCAPreferences(path, keepDefaults)

			direct := &tls.Config{}
			directErr := applyCustomRootCAsToTLSConfig(direct, prefs)

			readPath, certPEM, readErr := xport.ReadCustomCACertificatePEM(prefs)
			cached := &tls.Config{}
			cachedErr := readErr
			if readErr == nil {
				cachedErr = xport.ApplyCustomRootCAPEM(cached, readPath, certPEM, keepDefaults)
			}

			label := filepath.Base(path)
			if path == "" {
				label = "(unset)"
			}
			switch {
			case (directErr == nil) != (cachedErr == nil):
				t.Errorf("keep=%v %s: direct err=%v but cached err=%v", keepDefaults, label, directErr, cachedErr)
			case directErr != nil && directErr.Error() != cachedErr.Error():
				t.Errorf("keep=%v %s: error strings differ:\n  direct %v\n  cached %v",
					keepDefaults, label, directErr, cachedErr)
			}

			// Whether the system pool is in play is the security-relevant
			// outcome, and a nil RootCAs is what selects it.
			if (direct.RootCAs == nil) != (cached.RootCAs == nil) {
				t.Errorf("keep=%v %s: direct RootCAs set=%v but cached set=%v — one of them falls back to the SYSTEM pool",
					keepDefaults, label, direct.RootCAs != nil, cached.RootCAs != nil)
			}
			if direct.RootCAs != nil && cached.RootCAs != nil {
				if !direct.RootCAs.Equal(cached.RootCAs) {
					t.Errorf("keep=%v %s: the two paths produced different trust anchors", keepDefaults, label)
				}
				if ca.trusts(t, direct.RootCAs) != ca.trusts(t, cached.RootCAs) {
					t.Errorf("keep=%v %s: the two paths disagree on whether the custom CA is trusted", keepDefaults, label)
				}
			}
		}
	}
}

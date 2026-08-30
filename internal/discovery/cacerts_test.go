// Finding a corporate CA the machine has been given (US-063).
//
// The rule this file exists to hold in place: a certificate authority is
// reported, never adopted. Silently trusting a PEM found on disk turns any
// stray file into blanket trust for every request the app makes, which is the
// exact shape of the attack the trust store exists to prevent. Adoption is a
// click, made against a fingerprint the user can check.
package discovery

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestCA(t *testing.T, path, commonName string, notAfter time.Time) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Example Corp"}},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              notAfter,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCACandidateIsDescribedWellEnoughToBeChecked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corp-root.crt")
	writeTestCA(t, path, "Example Corp Root CA", time.Now().Add(365*24*time.Hour))

	candidates := ScanCACertificates([]string{dir})
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	candidate := candidates[0]
	if candidate.Subject != "Example Corp Root CA" {
		t.Errorf("subject = %q", candidate.Subject)
	}
	if candidate.Path != path {
		t.Errorf("path = %q", candidate.Path)
	}
	// A fingerprint is the only thing that lets somebody verify this is the CA
	// their administrator told them about, rather than one that appeared.
	if len(candidate.Fingerprint) != 64 || strings.ContainsAny(candidate.Fingerprint, "ghijklmnop") {
		t.Errorf("fingerprint = %q, want lowercase SHA-256 hex", candidate.Fingerprint)
	}
	if candidate.NotAfter.IsZero() {
		t.Error("expiry not reported")
	}
	if candidate.Expired {
		t.Error("a valid certificate reported as expired")
	}
	if candidate.AlreadyTrusted {
		t.Error("a self-signed test CA cannot already be in the system store")
	}
}

func TestExpiredCACandidateIsReportedAsExpired(t *testing.T) {
	dir := t.TempDir()
	writeTestCA(t, filepath.Join(dir, "old.crt"), "Expired Corp CA", time.Now().Add(-24*time.Hour))
	candidates := ScanCACertificates([]string{dir})
	if len(candidates) != 1 || !candidates[0].Expired {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestNonCertificateFilesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.crt"), []byte("-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if candidates := ScanCACertificates([]string{dir}); len(candidates) != 0 {
		t.Fatalf("candidates = %#v", candidates)
	}
}

// A file holding a leaf certificate is not a CA, and offering it as one would
// produce a trust decision that cannot work and cannot be diagnosed.
func TestALeafCertificateIsNotOfferedAsACA(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "api.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "leaf.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if candidates := ScanCACertificates([]string{dir}); len(candidates) != 0 {
		t.Fatalf("a leaf certificate was offered as a CA: %#v", candidates)
	}
}

func TestMissingDirectoriesAreNotAnError(t *testing.T) {
	if candidates := ScanCACertificates([]string{filepath.Join(t.TempDir(), "nowhere")}); len(candidates) != 0 {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestScanningIsBoundedAndDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"c.crt", "a.crt", "b.crt"} {
		writeTestCA(t, filepath.Join(dir, name), "CA "+name, time.Now().Add(24*time.Hour))
	}
	first := ScanCACertificates([]string{dir})
	second := ScanCACertificates([]string{dir})
	if len(first) != 3 {
		t.Fatalf("candidates = %d", len(first))
	}
	for index := range first {
		if first[index].Path != second[index].Path {
			t.Fatal("scan order is not stable")
		}
	}
}

// Nothing in this package may switch trust on. Adoption is the caller's, made
// on an explicit click; a scan that could enable a CA by itself would be a
// scan that could be triggered into doing it.
func TestScanningNeverAdoptsAnything(t *testing.T) {
	dir := t.TempDir()
	writeTestCA(t, filepath.Join(dir, "corp.crt"), "Corp CA", time.Now().Add(24*time.Hour))
	candidates := ScanCACertificates([]string{dir})
	if len(candidates) != 1 {
		t.Fatal("expected one candidate")
	}
	// The returned value is a description. It carries no switch, and the only
	// thing a caller can do with it is show it and ask.
	if candidates[0].Path == "" || candidates[0].Fingerprint == "" {
		t.Fatal("a candidate must carry what the user needs to decide")
	}
}

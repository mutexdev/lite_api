// Finding a corporate CA the machine has been given (US-063).
//
// A managed machine is usually handed a certificate authority so its TLS
// interception proxy can be trusted. When that CA is installed in the operating
// system's trust store there is nothing to do -- Go uses the platform verifier,
// and requests already work. What this finds is the other case: a PEM sitting
// in one of the directories a provisioning script drops it into, which the
// system may or may not have picked up.
//
// The rule, and it is not negotiable: a CA is REPORTED, never adopted. A proxy
// can be adopted silently, and is, because curl and every browser do the same.
// Trust cannot. Silently trusting a file found on disk turns any stray PEM into
// blanket trust for every request this app will ever make, which is precisely
// the shape of the attack a trust store exists to prevent. The user adopts one
// by clicking, against a fingerprint they can check with whoever gave it to
// them.
package discovery

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// CACandidate is a certificate authority found on disk, described well enough
// for somebody to decide about it.
type CACandidate struct {
	Path    string `json:"path"`
	Subject string `json:"subject"`
	Issuer  string `json:"issuer"`
	// Fingerprint is the SHA-256 of the DER, lowercase hex: the only field that
	// lets a user confirm this is the CA they were told to expect.
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"notAfter"`
	Expired     bool      `json:"expired"`
	// AlreadyTrusted reports that the system store already contains this
	// certificate, in which case adopting it would change nothing.
	AlreadyTrusted bool `json:"alreadyTrusted"`
}

// maxCACertificateBytes bounds a candidate file. A CA bundle is a few kilobytes;
// anything larger is not one.
const maxCACertificateBytes = 1 << 20

// SystemCACertificateDirectories are the places a provisioning script puts a CA
// on this platform.
//
// macOS and Windows are deliberately sparse: their CAs live in the keychain and
// the certificate store, both of which the platform verifier already consults,
// so there is nothing there this app could usefully add.
func SystemCACertificateDirectories() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{
			"/usr/local/share/ca-certificates",
			"/etc/pki/ca-trust/source/anchors",
			"/etc/ssl/certs/java", // where several corporate installers also drop one
		}
	case "darwin":
		return []string{"/Library/Application Support/Certificates", "/usr/local/share/ca-certificates"}
	}
	return nil
}

// ScanCACertificates reads the candidate directories and describes what it
// finds. It changes nothing.
func ScanCACertificates(directories []string) []CACandidate {
	systemPool, poolErr := x509.SystemCertPool()
	candidates := []CACandidate{}
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			// A directory that does not exist is the ordinary case on a machine
			// that was never handed a CA.
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !certificateFileName(entry.Name()) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			certificate, ok := readCACertificate(path)
			if !ok {
				continue
			}
			candidate := CACandidate{
				Path:        path,
				Subject:     certificateDisplayName(certificate.Subject.CommonName, certificate.Subject.Organization),
				Issuer:      certificateDisplayName(certificate.Issuer.CommonName, certificate.Issuer.Organization),
				Fingerprint: certificateFingerprint(certificate),
				NotAfter:    certificate.NotAfter,
				Expired:     time.Now().After(certificate.NotAfter),
			}
			if poolErr == nil && systemPool != nil {
				candidate.AlreadyTrusted = certificateInPool(systemPool, certificate)
			}
			candidates = append(candidates, candidate)
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool { return candidates[left].Path < candidates[right].Path })
	return candidates
}

func certificateFileName(name string) bool {
	lower := strings.ToLower(name)
	for _, extension := range []string{".crt", ".pem", ".cer", ".cert"} {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

// readCACertificate parses the first certificate in a PEM file, and accepts it
// only if it is a CA. A leaf certificate in one of these directories is a
// mistake somebody made, and offering it would produce a trust decision that
// cannot work and cannot be diagnosed.
func readCACertificate(path string) (*x509.Certificate, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCACertificateBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil, false
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, false
		}
		if !certificate.IsCA {
			return nil, false
		}
		return certificate, true
	}
	return nil, false
}

func certificateDisplayName(commonName string, organization []string) string {
	if strings.TrimSpace(commonName) != "" {
		return commonName
	}
	if len(organization) > 0 {
		return organization[0]
	}
	return "Unnamed certificate"
}

func certificateFingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// certificateInPool compares by subject rather than by parsing the pool, which
// x509.CertPool does not expose. A subject match is enough to answer the only
// question being asked: would adopting this change anything.
func certificateInPool(pool *x509.CertPool, certificate *x509.Certificate) bool {
	for _, subject := range pool.Subjects() { //nolint:staticcheck // Subjects is the only read API a CertPool offers.
		if string(subject) == string(certificate.RawSubject) {
			return true
		}
	}
	return false
}

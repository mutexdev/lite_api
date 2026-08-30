package transport

// TLS client certificates: matching one to a host and loading its key.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"software.sslmate.com/src/go-pkcs12"
)

func WithClientCertificate(base http.RoundTripper, collectionPath string, certs []types.ClientCertificateConfig, requestURL string, vars map[string]string) (http.RoundTripper, error) {
	certificate, ok, err := MatchingTLSClientCertificate(collectionPath, certs, requestURL, vars)
	if err != nil || !ok {
		return base, err
	}
	source, ok := base.(*http.Transport)
	if !ok || source == nil {
		source, _ = http.DefaultTransport.(*http.Transport)
	}
	transport := source.Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.Certificates = append([]tls.Certificate{certificate}, tlsConfig.Certificates...)
	transport.TLSClientConfig = tlsConfig
	return transport, nil
}

func MatchingTLSClientCertificate(collectionPath string, certs []types.ClientCertificateConfig, requestURL string, vars map[string]string) (tls.Certificate, bool, error) {
	for _, certConfig := range NormalizeClientCertificates(certs) {
		domain := interp.Interpolate(certConfig.Domain, vars)
		if !ClientCertificateDomainMatches(requestURL, domain) {
			continue
		}
		certificate, err := loadTLSClientCertificate(collectionPath, certConfig, vars)
		if err != nil {
			return tls.Certificate{}, false, err
		}
		return certificate, true, nil
	}
	return tls.Certificate{}, false, nil
}

// ClientCertificateDomainMatches decides whether a configured client
// certificate belongs to a request URL.
//
// This compares HOSTS, never URL text. The previous implementation built a
// regex from the configured domain and ran it against the whole URL with a
// `^(scheme)?` prefix and NO terminator, so the pattern was free to end in the
// middle of a hostname. That leaked the certificate — and therefore the user's
// identity — to hosts an attacker controls:
//
//	domain "example.com"   matched  https://example.com.evil.com/a
//	domain "example.com"   matched  https://example.community/a
//	domain "*.example.com" matched  https://api.example.com.evil.com/a
//
// A client certificate is a credential. Sending one to the wrong host hands
// that host a signed proof of identity, so the matcher has to be exact in the
// direction that matters and the parse has to define where the host ENDS —
// which is what url.Parse does and a prefix regex cannot.
//
// Two forms are supported:
//
//	"example.com"    exact host, case-insensitive
//	"*.example.com"  one or more labels, then exactly ".example.com"
//
// "*.example.com" deliberately does NOT match the bare "example.com", matching
// the rule TLS itself uses for wildcard certificates. Ports are ignored on
// both sides.
func ClientCertificateDomainMatches(requestURL, domain string) bool {
	pattern := clientCertificateDomainPattern(domain)
	if pattern == "" {
		return false
	}
	host := clientCertificateRequestHost(requestURL)
	if host == "" {
		return false
	}
	// A bare "*" is the configured "any host", preserved from the old regex
	// where it was the natural way to spell it. It is explicit user intent
	// rather than an accident of anchoring, so it is not a leak.
	if pattern == "*" {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		if suffix == "" {
			return false
		}
		// The length test is what requires at least one label before the dot:
		// it rejects both "example.com" and a malformed ".example.com".
		return len(host) > len(suffix)+1 && strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}

// clientCertificateRequestHost extracts the hostname a request is actually
// going to, with the port and every other URL component discarded.
func clientCertificateRequestHost(requestURL string) string {
	trimmed := strings.TrimSpace(requestURL)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		// Scheme-relative, because url.Parse reads a bare "host:port/path" as
		// scheme "host" with an opaque body and loses the host entirely.
		trimmed = "//" + strings.TrimPrefix(trimmed, "//")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// clientCertificateDomainPattern reduces a configured domain to a bare
// lower-case host, optionally keeping a leading "*." wildcard.
//
// Configured values are tolerated with a scheme, a port or a trailing path
// because existing collections on disk have them — the old matcher stripped a
// scheme explicitly, and anything after the host was simply never reached by
// the prefix regex.
func clientCertificateDomainPattern(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	if index := strings.Index(domain, "://"); index >= 0 {
		domain = domain[index+3:]
	}
	if domain == "*" {
		return "*"
	}
	// "*" is not legal host syntax, so the wildcard is lifted off before the
	// parse and restored after it.
	wildcard := strings.HasPrefix(domain, "*.")
	domain = strings.TrimPrefix(domain, "*.")
	host := clientCertificateRequestHost(domain)
	if host == "" {
		return ""
	}
	// Any surviving "*" is a mid-host wildcard ("api.*.com"). Those are not
	// supported: there is no safe anchored reading of one, and matching it
	// loosely is how the original bug worked. Fail closed.
	if strings.Contains(host, "*") {
		return ""
	}
	if wildcard {
		return "*." + host
	}
	return host
}

func loadTLSClientCertificate(collectionPath string, certConfig types.ClientCertificateConfig, vars map[string]string) (tls.Certificate, error) {
	passphrase := interp.Interpolate(certConfig.Passphrase, vars)
	switch strings.ToLower(strings.TrimSpace(scalar.FirstNonEmpty(certConfig.Type, "cert"))) {
	case "cert", "pem":
		certPath := ResolveCollectionRelativePath(collectionPath, interp.Interpolate(certConfig.CertFilePath, vars))
		keyPath := ResolveCollectionRelativePath(collectionPath, interp.Interpolate(certConfig.KeyFilePath, vars))
		if strings.TrimSpace(certPath) == "" || strings.TrimSpace(keyPath) == "" {
			return tls.Certificate{}, errors.New("client certificate cert/key paths are required")
		}
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("read client certificate file: %w", err)
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("read client certificate key file: %w", err)
		}
		keyPEM, err = decryptPEMKeyIfNeeded(keyPEM, passphrase)
		if err != nil {
			return tls.Certificate{}, err
		}
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load client certificate: %w", err)
		}
		return certificate, nil
	case "pfx", "pkcs12":
		pfxPath := ResolveCollectionRelativePath(collectionPath, interp.Interpolate(certConfig.PFXFilePath, vars))
		if strings.TrimSpace(pfxPath) == "" {
			return tls.Certificate{}, errors.New("client certificate pfx path is required")
		}
		pfxData, err := os.ReadFile(pfxPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("read client certificate pfx file: %w", err)
		}
		privateKey, leaf, caCerts, err := pkcs12.DecodeChain(pfxData, passphrase)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load client certificate pfx: %w", err)
		}
		certificate := tls.Certificate{PrivateKey: privateKey, Leaf: leaf}
		if leaf != nil {
			certificate.Certificate = append(certificate.Certificate, leaf.Raw)
		}
		for _, caCert := range caCerts {
			if caCert != nil {
				certificate.Certificate = append(certificate.Certificate, caCert.Raw)
			}
		}
		return certificate, nil
	default:
		return tls.Certificate{}, fmt.Errorf("unsupported client certificate type %q", certConfig.Type)
	}
}

// decryptPEMKeyIfNeeded turns an encrypted private key on disk into one
// tls.X509KeyPair can read and, where it cannot, says so plainly.
//
// Only legacy RFC-1423 keys ("Proc-Type: 4,ENCRYPTED") are actually decrypted
// here: Go deprecated that API without shipping a replacement, and the
// standard library has never exposed PKCS#8 decryption at all.
//
// What changed is the FAILURE modes. Both used to return the key untouched and
// let tls.X509KeyPair fail afterwards with a generic parse error that mentions
// neither encryption nor a passphrase — so a user holding a perfectly good key
// was told only that their certificate would not load, with nothing pointing
// at the reason or the fix.
func decryptPEMKeyIfNeeded(keyPEM []byte, passphrase string) ([]byte, error) {
	block, rest := pem.Decode(keyPEM)
	if block == nil {
		return keyPEM, nil
	}
	// PKCS#8 encryption carries no Proc-Type header, so IsEncryptedPEMBlock
	// does not recognise it and the block used to sail straight through as if
	// it were plaintext. The block type is the only marker there is.
	if block.Type == "ENCRYPTED PRIVATE KEY" {
		return nil, errors.New("client certificate key is an encrypted PKCS#8 key, which is not supported; " +
			"convert it first with: openssl pkcs8 -topk8 -nocrypt -in key.pem -out key-decrypted.pem")
	}
	if !x509.IsEncryptedPEMBlock(block) {
		return keyPEM, nil
	}
	if passphrase == "" {
		return nil, errors.New("client certificate key is encrypted; a passphrase is required")
	}
	decrypted, err := x509.DecryptPEMBlock(block, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("decrypt client certificate key: %w", err)
	}
	next := &pem.Block{Type: block.Type, Bytes: decrypted}
	var out bytes.Buffer
	if err := pem.Encode(&out, next); err != nil {
		return nil, fmt.Errorf("encode decrypted client certificate key: %w", err)
	}
	out.Write(rest)
	return out.Bytes(), nil
}

func NormalizeClientCertificates(certs []types.ClientCertificateConfig) []types.ClientCertificateConfig {
	rows := NormalizeClientCertificateRows(certs)
	result := make([]types.ClientCertificateConfig, 0, len(certs))
	for _, cert := range rows {
		if cert.Domain == "" && cert.CertFilePath == "" && cert.KeyFilePath == "" && cert.PFXFilePath == "" && cert.Passphrase == "" {
			continue
		}
		result = append(result, cert)
	}
	return result
}

func NormalizeClientCertificateRows(certs []types.ClientCertificateConfig) []types.ClientCertificateConfig {
	result := make([]types.ClientCertificateConfig, 0, len(certs))
	for _, cert := range certs {
		cert.Domain = strings.TrimSpace(cert.Domain)
		cert.Type = strings.ToLower(strings.TrimSpace(cert.Type))
		if cert.Type == "" {
			cert.Type = "cert"
		}
		if cert.Type == "pem" {
			cert.Type = "cert"
		}
		if cert.Type == "pkcs12" {
			cert.Type = "pfx"
		}
		cert.CertFilePath = strings.TrimSpace(cert.CertFilePath)
		cert.KeyFilePath = strings.TrimSpace(cert.KeyFilePath)
		cert.PFXFilePath = strings.TrimSpace(cert.PFXFilePath)
		result = append(result, cert)
	}
	return result
}

func HasClientCertificates(certs []types.ClientCertificateConfig) bool {
	return len(NormalizeClientCertificates(certs)) > 0
}

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
	"os"
	"regexp"
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

func ClientCertificateDomainMatches(requestURL, domain string) bool {
	domain = strings.TrimSpace(domain)
	if domain == "" || strings.TrimSpace(requestURL) == "" {
		return false
	}
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "grpcs://")
	domain = strings.TrimPrefix(domain, "grpc://")
	domain = strings.TrimPrefix(domain, "wss://")
	domain = strings.TrimPrefix(domain, "ws://")
	quoted := regexp.QuoteMeta(domain)
	quoted = strings.ReplaceAll(quoted, `\*`, `.*`)
	pattern := `^(https://|grpc://|grpcs://|ws://|wss://)?` + quoted
	matched, err := regexp.MatchString(pattern, requestURL)
	return err == nil && matched
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

func decryptPEMKeyIfNeeded(keyPEM []byte, passphrase string) ([]byte, error) {
	block, rest := pem.Decode(keyPEM)
	if block == nil || !x509.IsEncryptedPEMBlock(block) {
		return keyPEM, nil
	}
	if passphrase == "" {
		return keyPEM, nil
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

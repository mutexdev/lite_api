package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type material struct {
	serverTLS                  *tls.Config
	caPool                     *x509.CertPool
	caCertPath, clientCertPath string
	clientKeyPath              string
}

func generateMaterial(outputDir string) (*material, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	now := time.Now().Add(-time.Minute)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "LiteAPI Platform Fixture CA"}, NotBefore: now, NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	caCertPath, caKeyPath := filepath.Join(outputDir, "ca-cert.pem"), filepath.Join(outputDir, "ca-key.pem")
	if err := writePublic(caCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})); err != nil {
		return nil, err
	}
	if err := writeKey(caKeyPath, caKey); err != nil {
		return nil, err
	}

	serverCert, err := signedCertificate(caCert, caKey, 2, "LiteAPI Platform Fixture Server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		return nil, err
	}
	clientCert, err := signedCertificate(caCert, caKey, 3, "LiteAPI Platform Fixture Client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, nil)
	if err != nil {
		return nil, err
	}
	serverCertPath, serverKeyPath := filepath.Join(outputDir, "server-cert.pem"), filepath.Join(outputDir, "server-key.pem")
	clientCertPath, clientKeyPath := filepath.Join(outputDir, "client-cert.pem"), filepath.Join(outputDir, "client-key.pem")
	if err := writePair(serverCertPath, serverKeyPath, serverCert); err != nil {
		return nil, err
	}
	if err := writePair(clientCertPath, clientKeyPath, clientCert); err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &material{serverTLS: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert.tls}}, caPool: pool, caCertPath: caCertPath, clientCertPath: clientCertPath, clientKeyPath: clientKeyPath}, nil
}

type signedCert struct {
	der []byte
	key *rsa.PrivateKey
	tls tls.Certificate
}

func signedCertificate(ca *x509.Certificate, caKey crypto.PrivateKey, serial int64, name string, usages []x509.ExtKeyUsage, dns []string, ips []net.IP) (*signedCert, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate %s key: %w", name, err)
	}
	now := time.Now().Add(-time.Minute)
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: now, NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: usages, DNSNames: dns, IPAddresses: ips}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create %s certificate: %w", name, err)
	}
	return &signedCert{der: der, key: key, tls: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}}, nil
}

func writePair(certPath, keyPath string, pair *signedCert) error {
	if err := writePublic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.der})); err != nil {
		return err
	}
	return writeKey(keyPath, pair.key)
}

func writeKey(path string, key *rsa.PrivateKey) error {
	return writePrivate(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}
func writePublic(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
func writePrivate(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Chmod(path, 0600)
}

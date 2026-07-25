package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestFixture(t *testing.T) *Fixture {
	t.Helper()
	fixture, err := Start(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return fixture
}

func TestDirectProxyGraphQLAndManifest(t *testing.T) {
	fixture := startTestFixture(t)
	manifestBytes, err := os.ReadFile(fixture.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.TargetURL != fixture.Manifest.TargetURL || !strings.HasPrefix(manifest.ProxyURL, "http://127.0.0.1:") {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	for _, path := range []string{manifest.CACertPath, manifest.ClientCertPath, manifest.ClientKeyPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("manifest path %s: %v", path, err)
		}
	}
	keyInfo, err := os.Stat(manifest.ClientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0600 {
		t.Fatalf("client key permissions = %o, want 600", keyInfo.Mode().Perm())
	}

	direct, err := http.Get(manifest.TargetURL + "/target")
	if err != nil {
		t.Fatal(err)
	}
	_ = direct.Body.Close()
	if direct.Header.Get("X-LiteAPI-Proxy-Marker") != "" {
		t.Fatal("direct request unexpectedly marked as proxied")
	}
	proxyURL, _ := url.Parse(manifest.ProxyURL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	proxied, err := client.Get(manifest.TargetURL + "/target")
	if err != nil {
		t.Fatal(err)
	}
	_ = proxied.Body.Close()
	if got := proxied.Header.Get("X-LiteAPI-Proxy-Marker"); got != manifest.ProxyMarker {
		t.Fatalf("proxy marker=%q want=%q", got, manifest.ProxyMarker)
	}
	log, err := os.ReadFile(manifest.ProxyLogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), manifest.ProxyMarker) {
		t.Fatalf("proxy log missing marker: %q", log)
	}

	response, err := client.Post(manifest.GraphQLURL, "application/json", strings.NewReader(`{"query":"query Echo { echo }","variables":{"n":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "query Echo") || !strings.Contains(string(body), `"n":7`) {
		t.Fatalf("GraphQL success: status=%d body=%s", response.StatusCode, body)
	}
	response, err = http.Post(manifest.GraphQLURL, "application/json", strings.NewReader(`{"query":"fixtureError"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "FIXTURE_ERROR") {
		t.Fatalf("GraphQL error: status=%d body=%s", response.StatusCode, body)
	}
}

func TestTLSAndMTLSPaths(t *testing.T) {
	fixture := startTestFixture(t)
	plainClient := &http.Client{Timeout: 2 * time.Second}
	if response, err := plainClient.Get(fixture.Manifest.HTTPSURL); err == nil {
		_ = response.Body.Close()
		t.Fatal("untrusted fixture CA unexpectedly succeeded")
	}
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(fixture.Manifest.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("could not load fixture CA")
	}
	trustedTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	trustedClient := &http.Client{Transport: trustedTransport}
	response, err := trustedClient.Get(fixture.Manifest.HTTPSURL)
	if err != nil {
		t.Fatalf("custom CA HTTPS: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS status=%d", response.StatusCode)
	}
	if response, err := trustedClient.Get(fixture.Manifest.MTLSURL); err == nil {
		_ = response.Body.Close()
		t.Fatal("mTLS without client certificate unexpectedly succeeded")
	}
	serverIdentity, err := tls.LoadX509KeyPair(filepath.Join(filepath.Dir(fixture.Manifest.CACertPath), "server-cert.pem"), filepath.Join(filepath.Dir(fixture.Manifest.CACertPath), "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	rejectedClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{serverIdentity}, MinVersion: tls.VersionTLS12}}}
	if response, err := rejectedClient.Get(fixture.Manifest.MTLSURL); err == nil {
		_ = response.Body.Close()
		t.Fatal("mTLS server-only certificate unexpectedly succeeded as a client")
	}
	clientCert, err := tls.LoadX509KeyPair(fixture.Manifest.ClientCertPath, fixture.Manifest.ClientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	mtlsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{clientCert}, MinVersion: tls.VersionTLS12}}}
	response, err = mtlsClient.Get(fixture.Manifest.MTLSURL)
	if err != nil {
		t.Fatalf("mTLS accepted client: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "platform-mtls") {
		t.Fatalf("mTLS response status=%d body=%s", response.StatusCode, body)
	}
}

func TestLoopbackOnlyAndCleanup(t *testing.T) {
	if _, err := Start(Config{OutputDir: t.TempDir(), TargetListen: "0.0.0.0:0"}); err == nil {
		t.Fatal("non-loopback address accepted")
	}
	fixture := startTestFixture(t)
	endpoint := fixture.Manifest.TargetURL
	if err := fixture.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if response, err := client.Get(endpoint + "/target"); err == nil {
		_ = response.Body.Close()
		t.Fatal("target remained reachable after cleanup")
	}
	if _, err := os.Stat(filepath.Dir(fixture.ManifestPath)); err != nil {
		t.Fatalf("cleanup should not remove caller-owned output directory: %v", err)
	}
}

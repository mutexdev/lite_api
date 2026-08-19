package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const proxyMarkerPrefix = "liteapi-platform-proxy-"

type Config struct {
	OutputDir                 string
	TargetListen, ProxyListen string
	HTTPSListen, MTLSListen   string
}

type Manifest struct {
	Version        int    `json:"version"`
	TargetURL      string `json:"targetURL"`
	GraphQLURL     string `json:"graphQLURL"`
	ProxyURL       string `json:"proxyURL"`
	HTTPSURL       string `json:"httpsURL"`
	MTLSURL        string `json:"mtlsURL"`
	ProxyMarker    string `json:"proxyMarker"`
	ProxyLogPath   string `json:"proxyLogPath"`
	CACertPath     string `json:"caCertPath"`
	ClientCertPath string `json:"clientCertPath"`
	ClientKeyPath  string `json:"clientKeyPath"`
}

type Fixture struct {
	Manifest     Manifest
	ManifestPath string
	servers      []*http.Server
	listeners    []net.Listener
	closeOnce    sync.Once
}

func Start(config Config) (*Fixture, error) {
	if config.OutputDir == "" {
		return nil, errors.New("platformfixture: -output-dir is required")
	}
	if config.TargetListen == "" {
		config.TargetListen = "127.0.0.1:0"
	}
	if config.ProxyListen == "" {
		config.ProxyListen = "127.0.0.1:0"
	}
	if config.HTTPSListen == "" {
		config.HTTPSListen = "127.0.0.1:0"
	}
	if config.MTLSListen == "" {
		config.MTLSListen = "127.0.0.1:0"
	}
	if err := os.MkdirAll(config.OutputDir, 0700); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	for _, address := range []string{config.TargetListen, config.ProxyListen, config.HTTPSListen, config.MTLSListen} {
		if err := loopbackAddress(address); err != nil {
			return nil, err
		}
	}

	material, err := generateMaterial(config.OutputDir)
	if err != nil {
		return nil, err
	}
	markerBytes := make([]byte, 12)
	if _, err := rand.Read(markerBytes); err != nil {
		return nil, fmt.Errorf("random proxy marker: %w", err)
	}
	marker := fmt.Sprintf("%s%x", proxyMarkerPrefix, markerBytes)
	proxyLogPath := filepath.Join(config.OutputDir, "proxy.log")

	target, err := net.Listen("tcp", config.TargetListen)
	if err != nil {
		return nil, fmt.Errorf("listen target: %w", err)
	}
	fixture := &Fixture{listeners: []net.Listener{target}}
	fail := func(cause error) (*Fixture, error) { _ = fixture.Close(context.Background()); return nil, cause }
	targetURL := "http://" + target.Addr().String()
	targetServer := &http.Server{Handler: targetHandler()}
	fixture.servers = append(fixture.servers, targetServer)
	// Serve always returns a non-nil error once the listener is closed at teardown.
	go func() { _ = targetServer.Serve(target) }()

	proxyListener, err := net.Listen("tcp", config.ProxyListen)
	if err != nil {
		return fail(fmt.Errorf("listen proxy: %w", err))
	}
	fixture.listeners = append(fixture.listeners, proxyListener)
	proxyServer := &http.Server{Handler: proxyHandler(targetURL, marker, proxyLogPath)}
	fixture.servers = append(fixture.servers, proxyServer)
	// Serve always returns a non-nil error once the listener is closed at teardown.
	go func() { _ = proxyServer.Serve(proxyListener) }()

	httpsListener, err := net.Listen("tcp", config.HTTPSListen)
	if err != nil {
		return fail(fmt.Errorf("listen HTTPS: %w", err))
	}
	fixture.listeners = append(fixture.listeners, httpsListener)
	httpsServer := &http.Server{Handler: secureHandler("https")}
	fixture.servers = append(fixture.servers, httpsServer)
	// Serve always returns a non-nil error once the listener is closed at teardown.
	go func() { _ = httpsServer.Serve(tls.NewListener(httpsListener, material.serverTLS)) }()

	mtlsListener, err := net.Listen("tcp", config.MTLSListen)
	if err != nil {
		return fail(fmt.Errorf("listen mTLS: %w", err))
	}
	fixture.listeners = append(fixture.listeners, mtlsListener)
	mtlsConfig := material.serverTLS.Clone()
	mtlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	mtlsConfig.ClientCAs = material.caPool
	mtlsServer := &http.Server{Handler: secureHandler("mtls")}
	fixture.servers = append(fixture.servers, mtlsServer)
	// Serve always returns a non-nil error once the listener is closed at teardown.
	go func() { _ = mtlsServer.Serve(tls.NewListener(mtlsListener, mtlsConfig)) }()

	fixture.Manifest = Manifest{Version: 1, TargetURL: targetURL, GraphQLURL: targetURL + "/graphql", ProxyURL: "http://" + proxyListener.Addr().String(), HTTPSURL: "https://" + httpsListener.Addr().String(), MTLSURL: "https://" + mtlsListener.Addr().String(), ProxyMarker: marker, ProxyLogPath: proxyLogPath, CACertPath: material.caCertPath, ClientCertPath: material.clientCertPath, ClientKeyPath: material.clientKeyPath}
	fixture.ManifestPath = filepath.Join(config.OutputDir, "manifest.json")
	encoded, err := json.MarshalIndent(fixture.Manifest, "", "  ")
	if err != nil {
		return fail(fmt.Errorf("encode manifest: %w", err))
	}
	if err := writePrivate(fixture.ManifestPath, append(encoded, '\n')); err != nil {
		return fail(fmt.Errorf("write manifest: %w", err))
	}
	return fixture, nil
}

func (f *Fixture) Close(ctx context.Context) error {
	var result error
	f.closeOnce.Do(func() {
		for _, server := range f.servers {
			if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				result = errors.Join(result, err)
			}
		}
		for _, listener := range f.listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				result = errors.Join(result, err)
			}
		}
	})
	return result
}

func loopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q must use a numeric loopback host", address)
	}
	return nil
}

func targetHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/target", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fixture":"platform-target","method":"` + r.Method + `"}`))
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Query     string          `json:"query"`
			Variables json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"errors":[{"message":"invalid JSON"}]}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "fixtureError") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"deterministic GraphQL error","extensions":{"code":"FIXTURE_ERROR"}}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"echo": map[string]any{"query": body.Query, "variables": body.Variables}}})
	})
	return mux
}

func proxyHandler(targetURL, marker, logPath string) http.Handler {
	target, _ := url.Parse(targetURL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != target.Host {
			http.Error(w, "proxy only forwards to its local target", http.StatusBadGateway)
			return
		}
		out := r.Clone(r.Context())
		out.URL.Scheme, out.URL.Host, out.Host = target.Scheme, target.Host, target.Host
		out.RequestURI = ""
		response, err := transport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = response.Body.Close() }()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.Header().Set("X-LiteAPI-Proxy-Marker", marker)
		w.WriteHeader(response.StatusCode)
		if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			_, _ = logFile.WriteString(marker + " " + r.Method + " " + r.URL.String() + "\n")
			_ = logFile.Close()
		}
		_, _ = io.Copy(w, response.Body)
	})
}

func secureHandler(kind string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fixture":"platform-` + kind + `"}`))
	})
}

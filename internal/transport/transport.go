// Package transport builds the http.RoundTripper a request executes on: TLS
// client certificates, and proxy resolution in all four flavours the app
// supports -- none, manual, system and PAC, including a goja PAC runtime.
//
// US-062 groundwork. Every function here was already free of *App, which is
// what made the region movable.
package transport

import (
	"LiteAPI/internal/interp"
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
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

func ResolveCollectionRelativePath(collectionPath, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(collectionPath, filepath.FromSlash(value))
}

func WithProxyResolution(base http.RoundTripper, resolution Resolution, requestURL string, vars map[string]string) (http.RoundTripper, error) {
	switch strings.ToLower(strings.TrimSpace(resolution.Mode)) {
	case "manual":
		return WithManualProxy(base, resolution.Config, requestURL, vars)
	case "system":
		return transportWithSystemProxy(base, requestURL)
	case "pac":
		return transportWithPACProxy(base, resolution.PACSource, requestURL, vars)
	default:
		return WithoutProxy(base), nil
	}
}

func CloneHTTPTransport(base http.RoundTripper) *http.Transport {
	source, ok := base.(*http.Transport)
	if !ok || source == nil {
		source, _ = http.DefaultTransport.(*http.Transport)
	}
	if source == nil {
		return &http.Transport{}
	}
	return source.Clone()
}

func WithoutProxy(base http.RoundTripper) http.RoundTripper {
	transport := CloneHTTPTransport(base)
	transport.Proxy = nil
	return transport
}

func WithManualProxy(base http.RoundTripper, proxy types.ProxyConfig, requestURL string, vars map[string]string) (http.RoundTripper, error) {
	transport := CloneHTTPTransport(base)
	transport.Proxy = nil
	if !ShouldUseManualProxy(requestURL, interp.Interpolate(proxy.BypassProxy, vars)) {
		return transport, nil
	}
	proxyURL, err := ManualProxyURL(proxy, vars)
	if err != nil {
		return nil, err
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport, nil
}

func transportWithSystemProxy(base http.RoundTripper, requestURL string) (http.RoundTripper, error) {
	transport := CloneHTTPTransport(base)
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		target := requestURL
		if req != nil && req.URL != nil {
			target = req.URL.String()
		}
		return SystemProxyURLForRequest(target)
	}
	return transport, nil
}

func transportWithPACProxy(base http.RoundTripper, pacSource, requestURL string, vars map[string]string) (http.RoundTripper, error) {
	transport := CloneHTTPTransport(base)
	transport.Proxy = nil
	proxyURL, ok, err := ResolvePACProxyURL(interp.Interpolate(pacSource, vars), requestURL)
	if err != nil || !ok {
		return transport, nil
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport, nil
}

func ManualProxyURL(proxy types.ProxyConfig, vars map[string]string) (*url.URL, error) {
	protocol := strings.ToLower(strings.TrimSpace(interp.Interpolate(scalar.FirstNonEmpty(proxy.Protocol, "http"), vars)))
	if protocol == "" {
		protocol = "http"
	}
	if protocol != "http" && protocol != "https" && protocol != "socks5" {
		return nil, fmt.Errorf("unsupported proxy protocol %q", protocol)
	}
	host := strings.TrimSpace(interp.Interpolate(proxy.Hostname, vars))
	if host == "" {
		return nil, errors.New("proxy hostname is required")
	}
	port := strings.TrimSpace(interp.Interpolate(proxy.Port, vars))
	hostPort := host
	if port != "" {
		hostPort = net.JoinHostPort(host, port)
	}
	proxyURL := &url.URL{Scheme: protocol, Host: hostPort}
	if !proxy.Auth.Disabled {
		username := interp.Interpolate(proxy.Auth.Username, vars)
		password := interp.Interpolate(proxy.Auth.Password, vars)
		if username != "" || password != "" {
			proxyURL.User = url.UserPassword(username, password)
		}
	}
	return proxyURL, nil
}

func SystemProxyURLForRequest(rawURL string) (*url.URL, error) {
	if proxyURL, err := proxyURLFromEnvironment(rawURL); proxyURL != nil || err != nil {
		return proxyURL, err
	}
	if pacSource := strings.TrimSpace(os.Getenv("LITEAPI_SYSTEM_PAC_URL")); pacSource != "" {
		proxyURL, ok, err := ResolvePACProxyURL(pacSource, rawURL)
		if err != nil || !ok {
			return nil, nil
		}
		return proxyURL, nil
	}
	if goruntime.GOOS == "darwin" {
		return macOSSystemProxyURLForRequest(rawURL)
	}
	return nil, nil
}

func proxyURLFromEnvironment(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return nil, nil
	}
	noProxy := scalar.FirstNonEmpty(os.Getenv("NO_PROXY"), os.Getenv("no_proxy"))
	if !ShouldUseManualProxy(rawURL, noProxy) {
		return nil, nil
	}
	var proxyValue string
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		proxyValue = scalar.FirstNonEmpty(os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy"), os.Getenv("ALL_PROXY"), os.Getenv("all_proxy"))
	default:
		proxyValue = scalar.FirstNonEmpty(os.Getenv("HTTP_PROXY"), os.Getenv("http_proxy"), os.Getenv("ALL_PROXY"), os.Getenv("all_proxy"))
	}
	return parseProxyURLValue(proxyValue)
}

func macOSSystemProxyURLForRequest(rawURL string) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return nil, nil
	}
	return ProxyURLFromMacOSScutilOutput(string(output), rawURL)
}

func ProxyURLFromMacOSScutilOutput(output, rawURL string) (*url.URL, error) {
	values, exceptions := parseMacOSScutilProxyOutput(output)
	if len(exceptions) > 0 && !ShouldUseManualProxy(rawURL, strings.Join(exceptions, ",")) {
		return nil, nil
	}
	if values["ProxyAutoConfigEnable"] == "1" && strings.TrimSpace(values["ProxyAutoConfigURLString"]) != "" {
		proxyURL, ok, err := ResolvePACProxyURL(values["ProxyAutoConfigURLString"], rawURL)
		if err != nil || !ok {
			return nil, nil
		}
		return proxyURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		if values["HTTPSEnable"] == "1" {
			return proxyURLFromParts("http", values["HTTPSProxy"], values["HTTPSPort"])
		}
	case "http", "ws":
		if values["HTTPEnable"] == "1" {
			return proxyURLFromParts("http", values["HTTPProxy"], values["HTTPPort"])
		}
	}
	if values["SOCKSEnable"] == "1" {
		return proxyURLFromParts("socks5", values["SOCKSProxy"], values["SOCKSPort"])
	}
	return nil, nil
}

func parseMacOSScutilProxyOutput(output string) (map[string]string, []string) {
	values := map[string]string{}
	var exceptions []string
	inExceptions := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "ExceptionsList") {
			inExceptions = true
			continue
		}
		if inExceptions {
			if trimmed == "}" {
				inExceptions = false
				continue
			}
			key, value, ok := strings.Cut(trimmed, ":")
			if ok && strings.TrimSpace(key) != "" {
				value = strings.Trim(strings.TrimSpace(value), "\"")
				if value != "" {
					exceptions = append(exceptions, value)
				}
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"")
		if key != "" {
			values[key] = value
		}
	}
	return values, exceptions
}

func proxyURLFromParts(scheme, host, port string) (*url.URL, error) {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" {
		return nil, nil
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return parseProxyURLValue(scheme + "://" + host)
}

func parseProxyURLValue(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	proxyURL, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL %q", value)
	}
	return proxyURL, nil
}

func ResolvePACProxyURL(pacSource, requestURL string) (*url.URL, bool, error) {
	pacSource = strings.TrimSpace(pacSource)
	if pacSource == "" {
		return nil, false, nil
	}
	content, err := LoadPACSource(pacSource)
	if err != nil {
		return nil, false, err
	}
	return pacProxyURLFromContent(content, requestURL)
}

func LoadPACSource(pacSource string) (string, error) {
	if strings.HasPrefix(strings.ToLower(pacSource), "file://") {
		parsed, err := url.Parse(pacSource)
		if err != nil {
			return "", err
		}
		path, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		return string(data), err
	}
	if strings.HasPrefix(strings.ToLower(pacSource), "http://") || strings.HasPrefix(strings.ToLower(pacSource), "https://") {
		// US-017: shared no-proxy client (a PAC fetch must not go through the
		// proxy it is being consulted to discover). Was a fresh transport clone
		// per fetch.
		res, err := pacHTTPClient().Get(pacSource)
		if err != nil {
			return "", err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return "", fmt.Errorf("fetch PAC file: %s", res.Status)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024))
		return string(data), err
	}
	if data, err := os.ReadFile(pacSource); err == nil {
		return string(data), nil
	}
	return pacSource, nil
}

func pacProxyURLFromContent(content, requestURL string) (*url.URL, bool, error) {
	directives, err := pacDirectivesForURL(content, requestURL)
	if err != nil {
		return nil, false, err
	}
	if len(directives) == 0 {
		return nil, false, nil
	}
	for _, directive := range directives {
		directive = strings.TrimSpace(directive)
		if directive == "" {
			continue
		}
		if strings.EqualFold(directive, "DIRECT") {
			return nil, false, nil
		}
		parts := strings.Fields(directive)
		if len(parts) < 2 {
			continue
		}
		kind := strings.ToUpper(parts[0])
		hostPort := parts[1]
		var scheme string
		switch kind {
		case "PROXY":
			scheme = "http"
		case "HTTPS":
			scheme = "https"
		case "SOCKS", "SOCKS5":
			scheme = "socks5"
		default:
			continue
		}
		proxyURL, err := parseProxyURLValue(scheme + "://" + hostPort)
		if err != nil {
			return nil, false, err
		}
		return proxyURL, ShouldUseManualProxy(requestURL, ""), nil
	}
	return nil, false, nil
}

func pacDirectivesForURL(content, requestURL string) ([]string, error) {
	directives, err := evaluatePACDirectives(content, requestURL)
	if err == nil && len(directives) > 0 {
		return directives, nil
	}
	fallback := pacReturnDirectives(content)
	if len(fallback) > 0 {
		return fallback, nil
	}
	return nil, err
}

func evaluatePACDirectives(content, requestURL string) ([]string, error) {
	parsed, err := url.Parse(requestURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, nil
	}
	vm := goja.New()
	installPACRuntime(vm)
	timer := time.AfterFunc(500*time.Millisecond, func() {
		vm.Interrupt("PAC execution timed out")
	})
	defer timer.Stop()
	if _, err := vm.RunString(content); err != nil {
		return nil, err
	}
	value := vm.Get("FindProxyForURL")
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, errors.New("PAC FindProxyForURL is not defined")
	}
	callable, ok := goja.AssertFunction(value)
	if !ok {
		return nil, errors.New("PAC FindProxyForURL is not callable")
	}
	out, err := callable(goja.Undefined(), vm.ToValue(requestURL), vm.ToValue(parsed.Hostname()))
	if err != nil {
		return nil, err
	}
	if goja.IsUndefined(out) || goja.IsNull(out) {
		return nil, nil
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return nil, nil
	}
	return splitPACDirectives(result), nil
}

func installPACRuntime(vm *goja.Runtime) {
	_ = vm.Set("isPlainHostName", func(host string) bool {
		return !strings.Contains(host, ".")
	})
	_ = vm.Set("dnsDomainIs", func(host, domain string) bool {
		return strings.HasSuffix(strings.ToLower(host), strings.ToLower(domain))
	})
	_ = vm.Set("localHostOrDomainIs", func(host, hostdom string) bool {
		host = strings.ToLower(host)
		hostdom = strings.ToLower(hostdom)
		return host == hostdom || (!strings.Contains(host, ".") && strings.HasPrefix(hostdom, host+"."))
	})
	_ = vm.Set("isResolvable", func(host string) bool {
		if net.ParseIP(host) != nil {
			return true
		}
		addrs, err := net.LookupHost(host)
		return err == nil && len(addrs) > 0
	})
	_ = vm.Set("dnsResolve", func(host string) string {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		addrs, err := net.LookupHost(host)
		if err != nil || len(addrs) == 0 {
			return ""
		}
		return addrs[0]
	})
	_ = vm.Set("myIpAddress", func() string {
		return pacLocalIPAddress()
	})
	_ = vm.Set("dnsDomainLevels", func(host string) int {
		return strings.Count(host, ".")
	})
	_ = vm.Set("shExpMatch", func(value, pattern string) bool {
		return pacShellExpressionMatch(value, pattern)
	})
	_ = vm.Set("isInNet", func(host, pattern, mask string) bool {
		return pacIsInNet(host, pattern, mask)
	})
	_ = vm.Set("weekdayRange", pacWeekdayRange)
	_ = vm.Set("timeRange", func(args ...int) bool {
		return pacTimeRange(time.Now(), args...)
	})
	_ = vm.Set("dateRange", func(args ...goja.Value) bool {
		return pacDateRange(time.Now(), args...)
	})
	_ = vm.Set("alert", func(args ...interface{}) {})
}

func splitPACDirectives(value string) []string {
	result := []string{}
	for _, directive := range strings.Split(value, ";") {
		directive = strings.TrimSpace(directive)
		if directive != "" {
			result = append(result, directive)
		}
	}
	return result
}

func pacReturnDirectives(content string) []string {
	matches := regexp.MustCompile(`(?is)return\s+["']([^"']+)["']`).FindAllStringSubmatch(content, -1)
	var directives []string
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		directives = append(directives, splitPACDirectives(match[1])...)
	}
	return directives
}

func pacShellExpressionMatch(value, pattern string) bool {
	var builder strings.Builder
	builder.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			builder.WriteString(".*")
		case '?':
			builder.WriteString(".")
		default:
			builder.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	builder.WriteString("$")
	ok, err := regexp.MatchString(builder.String(), value)
	return err == nil && ok
}

func pacIsInNet(host, pattern, mask string) bool {
	hostIP := net.ParseIP(host)
	if hostIP == nil {
		resolved := ""
		if addrs, err := net.LookupHost(host); err == nil && len(addrs) > 0 {
			resolved = addrs[0]
		}
		hostIP = net.ParseIP(resolved)
	}
	patternIP := net.ParseIP(pattern)
	maskIP := net.ParseIP(mask)
	if hostIP == nil || patternIP == nil || maskIP == nil {
		return false
	}
	host4 := hostIP.To4()
	pattern4 := patternIP.To4()
	mask4 := maskIP.To4()
	if host4 == nil || pattern4 == nil || mask4 == nil {
		return false
	}
	for i := 0; i < net.IPv4len; i++ {
		if host4[i]&mask4[i] != pattern4[i]&mask4[i] {
			return false
		}
	}
	return true
}

func pacLocalIPAddress() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 100*time.Millisecond)
	if err == nil {
		defer func() { _ = conn.Close() }()
		if local, ok := conn.LocalAddr().(*net.UDPAddr); ok && local.IP != nil {
			return local.IP.String()
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}

func pacWeekdayRange(args ...string) bool {
	if len(args) == 0 {
		return false
	}
	gmt := len(args) > 0 && strings.EqualFold(args[len(args)-1], "GMT")
	if gmt {
		args = args[:len(args)-1]
	}
	now := time.Now()
	if gmt {
		now = now.UTC()
	}
	current := pacWeekdayIndex(now.Weekday().String()[:3])
	if len(args) == 1 {
		return current == pacWeekdayIndex(args[0])
	}
	start := pacWeekdayIndex(args[0])
	end := pacWeekdayIndex(args[1])
	if start < 0 || end < 0 || current < 0 {
		return false
	}
	if start <= end {
		return current >= start && current <= end
	}
	return current >= start || current <= end
}

func pacWeekdayIndex(value string) int {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "SUN":
		return 0
	case "MON":
		return 1
	case "TUE":
		return 2
	case "WED":
		return 3
	case "THU":
		return 4
	case "FRI":
		return 5
	case "SAT":
		return 6
	default:
		return -1
	}
}

func pacTimeRange(now time.Time, args ...int) bool {
	if len(args) == 0 || len(args) > 6 {
		return false
	}
	seconds := now.Hour()*3600 + now.Minute()*60 + now.Second()
	point := func(values []int) int {
		hour := 0
		minute := 0
		second := 0
		if len(values) > 0 {
			hour = values[0]
		}
		if len(values) > 1 {
			minute = values[1]
		}
		if len(values) > 2 {
			second = values[2]
		}
		return hour*3600 + minute*60 + second
	}
	if len(args) <= 3 {
		return seconds == point(args)
	}
	mid := len(args) / 2
	start := point(args[:mid])
	end := point(args[mid:])
	if start <= end {
		return seconds >= start && seconds <= end
	}
	return seconds >= start || seconds <= end
}

func pacDateRange(now time.Time, args ...goja.Value) bool {
	if len(args) == 0 {
		return false
	}
	if len(args) > 0 && strings.EqualFold(args[len(args)-1].String(), "GMT") {
		now = now.UTC()
		args = args[:len(args)-1]
	}
	values := make([]interface{}, 0, len(args))
	for _, arg := range args {
		exported := arg.Export()
		values = append(values, exported)
	}
	if len(values) == 1 {
		return pacDateComponentMatches(now, values[0])
	}
	if len(values) == 2 {
		return pacDateBetween(now, values[0], values[1])
	}
	return false
}

func pacDateComponentMatches(now time.Time, value interface{}) bool {
	switch typed := value.(type) {
	case int64:
		return now.Day() == int(typed)
	case int:
		return now.Day() == typed
	case string:
		if month := pacMonthIndex(typed); month > 0 {
			return int(now.Month()) == month
		}
		if year, err := strconv.Atoi(typed); err == nil {
			return now.Year() == year
		}
	}
	return false
}

func pacDateBetween(now time.Time, startValue, endValue interface{}) bool {
	if pacDateComponentMatches(now, startValue) || pacDateComponentMatches(now, endValue) {
		return true
	}
	startMonth := pacMonthIndex(fmt.Sprint(startValue))
	endMonth := pacMonthIndex(fmt.Sprint(endValue))
	if startMonth > 0 && endMonth > 0 {
		current := int(now.Month())
		if startMonth <= endMonth {
			return current >= startMonth && current <= endMonth
		}
		return current >= startMonth || current <= endMonth
	}
	startDay, startErr := strconv.Atoi(fmt.Sprint(startValue))
	endDay, endErr := strconv.Atoi(fmt.Sprint(endValue))
	if startErr == nil && endErr == nil {
		day := now.Day()
		return day >= startDay && day <= endDay
	}
	startYear, startErr := strconv.Atoi(fmt.Sprint(startValue))
	endYear, endErr := strconv.Atoi(fmt.Sprint(endValue))
	if startErr == nil && endErr == nil && startYear > 31 && endYear > 31 {
		year := now.Year()
		return year >= startYear && year <= endYear
	}
	return false
}

func pacMonthIndex(value string) int {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "JAN":
		return 1
	case "FEB":
		return 2
	case "MAR":
		return 3
	case "APR":
		return 4
	case "MAY":
		return 5
	case "JUN":
		return 6
	case "JUL":
		return 7
	case "AUG":
		return 8
	case "SEP":
		return 9
	case "OCT":
		return 10
	case "NOV":
		return 11
	case "DEC":
		return 12
	default:
		return 0
	}
}

func ShouldUseManualProxy(rawURL, bypass string) bool {
	bypass = strings.TrimSpace(bypass)
	if bypass == "*" {
		return false
	}
	if bypass == "" {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	proto := strings.TrimSuffix(strings.ToLower(parsed.Scheme), ":")
	hostname := parsed.Host
	if parsed.Hostname() != "" {
		hostname = parsed.Hostname()
	}
	port := parsed.Port()
	if port == "" {
		switch proto {
		case "https", "wss":
			port = "443"
		default:
			port = "80"
		}
	}
	for _, rule := range strings.FieldsFunc(bypass, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		ruleHost := rule
		rulePort := ""
		if h, p, err := net.SplitHostPort(rule); err == nil {
			ruleHost, rulePort = h, p
		} else if index := strings.LastIndex(rule, ":"); index > 0 && !strings.Contains(rule[index+1:], ":") {
			if _, err := strconv.Atoi(rule[index+1:]); err == nil {
				ruleHost, rulePort = rule[:index], rule[index+1:]
			}
		}
		if rulePort != "" && rulePort != port {
			continue
		}
		if !strings.HasPrefix(ruleHost, ".") && !strings.HasPrefix(ruleHost, "*") {
			if strings.EqualFold(hostname, ruleHost) {
				return false
			}
			continue
		}
		ruleHost = strings.TrimPrefix(ruleHost, "*")
		if strings.HasSuffix(strings.ToLower(hostname), strings.ToLower(ruleHost)) {
			return false
		}
	}
	return true
}

func NormalizeProxyConfig(proxy types.ProxyConfig) types.ProxyConfig {
	proxy.Protocol = strings.ToLower(strings.TrimSpace(proxy.Protocol))
	if proxy.Protocol == "" {
		proxy.Protocol = "http"
	}
	proxy.Hostname = strings.TrimSpace(proxy.Hostname)
	proxy.Port = strings.TrimSpace(proxy.Port)
	proxy.BypassProxy = strings.TrimSpace(proxy.BypassProxy)
	return proxy
}

func ProxyConfigUnset(proxy types.ProxyConfig) bool {
	return !proxy.Inherit && !proxy.Disabled &&
		strings.TrimSpace(proxy.Protocol) == "" &&
		strings.TrimSpace(proxy.Hostname) == "" &&
		strings.TrimSpace(proxy.Port) == "" &&
		strings.TrimSpace(proxy.BypassProxy) == "" &&
		strings.TrimSpace(proxy.Auth.Username) == "" &&
		strings.TrimSpace(proxy.Auth.Password) == "" &&
		!proxy.Auth.Disabled
}

func HasProxyConfig(proxy types.ProxyConfig) bool {
	proxy = NormalizeProxyConfig(proxy)
	return proxy.Inherit || proxy.Disabled || proxy.Hostname != "" || proxy.Port != "" || proxy.Protocol != "http" ||
		proxy.BypassProxy != "" || proxy.Auth.Username != "" || proxy.Auth.Password != "" || proxy.Auth.Disabled
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

// Resolution is how a request's proxy was decided: which mode won, and the
// manual config or PAC source that goes with it.
type Resolution struct {
	Mode      string
	Config    types.ProxyConfig
	PACSource string
}

// pacHTTPClient fetches PAC files. Posture: verified TLS and NO proxy -- a PAC
// fetch must not be routed through the proxy it is being consulted to discover.
// Settable for the same reason as the interpolator: package main owns the
// shared transport cache.
var pacHTTPClient = func() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// SetPACHTTPClient installs the client used to fetch PAC files.
func SetPACHTTPClient(get func() *http.Client) {
	if get != nil {
		pacHTTPClient = get
	}
}

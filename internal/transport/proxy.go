package transport

// Choosing a proxy for a request: manual, system, or a PAC script and the little language it is written in.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

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

// DiscoverSystemProxy answers "what does this machine say about a proxy for
// this request" WITHOUT evaluating anything: a static proxy URL, or the
// location of a PAC script, or neither.
//
// It is SystemProxyURLForRequest's own body with the three PAC evaluations
// lifted out, and SystemProxyURLForRequest is now discovery plus those
// evaluations -- one body, two callers, so the two cannot drift. Every caller
// that needs to know a PAC is involved BEFORE a script is fetched and run (a
// PAC file is a remote JavaScript program with its own DNS and its own fetch)
// asks discovery; the one that wants the answer asks
// SystemProxyURLForRequest.
//
// The split is behaviour-preserving because at each of the three sites PAC,
// once selected, short-circuits: the block returns on every path and never
// falls through to the static values below it. So "a PAC source was
// discovered" and "a static proxy was discovered" are mutually exclusive, and
// a PAC discovery never carries an error.
func DiscoverSystemProxy(rawURL string) (proxyURL *url.URL, pacURL string, err error) {
	if envProxy, envErr := proxyURLFromEnvironment(rawURL); envProxy != nil || envErr != nil {
		return envProxy, "", envErr
	}
	if pacSource := strings.TrimSpace(os.Getenv("LITEAPI_SYSTEM_PAC_URL")); pacSource != "" {
		return nil, pacSource, nil
	}
	if goruntime.GOOS == "darwin" {
		return discoverMacOSSystemProxy(rawURL)
	}
	// US-061. Windows and Linux read their own settings. Reached only after the
	// environment variables above: exporting HTTPS_PROXY is a deliberate act by
	// whoever launched the app, and has to win over a value the machine was
	// handed by whoever set it up.
	if settings, ok := readOSProxySettings(); ok {
		return discoverProxyFromOSSettings(settings, rawURL)
	}
	return nil, "", nil
}

func SystemProxyURLForRequest(rawURL string) (*url.URL, error) {
	proxyURL, pacURL, err := DiscoverSystemProxy(rawURL)
	if pacURL != "" {
		return systemPACProxyURL(pacURL, rawURL)
	}
	return proxyURL, err
}

// systemPACProxyURL is the disposition all three system-proxy PAC sites shared
// before discovery was split out, kept in one place so it stays shared: a PAC
// that fails to load, fails to run, or selects DIRECT means "no proxy" rather
// than a failed request, because the machine having an unusable PAC is not a
// reason to refuse to make the call.
func systemPACProxyURL(pacSource, rawURL string) (*url.URL, error) {
	proxyURL, ok, err := ResolvePACProxyURL(pacSource, rawURL)
	if err != nil || !ok {
		return nil, nil
	}
	return proxyURL, nil
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

func discoverMacOSSystemProxy(rawURL string) (*url.URL, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return nil, "", nil
	}
	return discoverProxyFromMacOSScutilOutput(string(output), rawURL)
}

func ProxyURLFromMacOSScutilOutput(output, rawURL string) (*url.URL, error) {
	proxyURL, pacURL, err := discoverProxyFromMacOSScutilOutput(output, rawURL)
	if pacURL != "" {
		return systemPACProxyURL(pacURL, rawURL)
	}
	return proxyURL, err
}

// discoverProxyFromMacOSScutilOutput reports what `scutil --proxy` says,
// reporting a PAC script's location instead of running it. The exceptions list
// is still consulted first: a host the machine says to reach directly is
// direct whether the configuration names a PAC or a static proxy.
func discoverProxyFromMacOSScutilOutput(output, rawURL string) (*url.URL, string, error) {
	values, exceptions := parseMacOSScutilProxyOutput(output)
	if len(exceptions) > 0 && !ShouldUseManualProxy(rawURL, strings.Join(exceptions, ",")) {
		return nil, "", nil
	}
	if values["ProxyAutoConfigEnable"] == "1" && strings.TrimSpace(values["ProxyAutoConfigURLString"]) != "" {
		return nil, values["ProxyAutoConfigURLString"], nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		if values["HTTPSEnable"] == "1" {
			proxyURL, err := proxyURLFromParts("http", values["HTTPSProxy"], values["HTTPSPort"])
			return proxyURL, "", err
		}
	case "http", "ws":
		if values["HTTPEnable"] == "1" {
			proxyURL, err := proxyURLFromParts("http", values["HTTPProxy"], values["HTTPPort"])
			return proxyURL, "", err
		}
	}
	if values["SOCKSEnable"] == "1" {
		proxyURL, err := proxyURLFromParts("socks5", values["SOCKSProxy"], values["SOCKSPort"])
		return proxyURL, "", err
	}
	return nil, "", nil
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

// pacEvaluationCount counts entries into ResolvePACProxyURL -- the single door
// to loading a PAC source and running it.
//
// It exists so a test can assert a NEGATIVE that is otherwise unobservable:
// that a code path did not evaluate a PAC at all. A PAC script fetches, runs
// JavaScript and resolves its own hostnames, so "no proxy came back" is not
// evidence that nothing ran; only a count is. Kept unexported and read through
// the two helpers below, which are test-only by convention.
var pacEvaluationCount atomic.Int64

// pacEvaluations reports how many times the PAC load-and-evaluate path has been
// entered since the last reset.
func pacEvaluations() int64 { return pacEvaluationCount.Load() }

// resetPACEvaluations zeroes the counter so one test can measure one stretch.
func resetPACEvaluations() { pacEvaluationCount.Store(0) }

func ResolvePACProxyURL(pacSource, requestURL string) (*url.URL, bool, error) {
	pacEvaluationCount.Add(1)
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
	// The 1- and 2-argument forms are RANGES, not instants. timeRange(9) means
	// the whole 09:00:00-09:59:59 hour and timeRange(9, 17) means 09:00:00
	// through 17:59:59, which is how every PAC implementation reads them and the
	// only reading under which a script can express "business hours".
	//
	// This previously compared for exact equality, so timeRange(9, 17) matched
	// only at 09:17:00 -- a script using the standard form to gate on business
	// hours would essentially never match and would fall through to whatever
	// the script returned otherwise, routing traffic silently wrongly.
	switch len(args) {
	case 1:
		hour := args[0]
		return seconds >= hour*3600 && seconds <= hour*3600+3599
	case 2:
		start := args[0] * 3600
		end := args[1]*3600 + 3599
		if start <= end {
			return seconds >= start && seconds <= end
		}
		return seconds >= start || seconds <= end
	case 3:
		// h, m, s -- an instant, which is the one form equality is right for.
		return seconds == point(args)
	}
	mid := len(args) / 2
	start := point(args[:mid])
	end := point(args[mid:])
	// The end bound is inclusive of its finest unit: h1,m1,h2,m2 runs to
	// h2:m2:59, matching the 1- and 2-argument forms above.
	if len(args) == 4 {
		end += 59
	}
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
	// Days and years are told apart by magnitude, which is the only signal PAC
	// gives: dateRange(1, 15) is days, dateRange(2020, 2030) is years.
	//
	// The day branch used to return for ANY two integers, which made the year
	// branch below it unreachable — dateRange(2020, 2030) was evaluated as
	// "day >= 2020", false on every date there has ever been. A script gating
	// on a year range therefore never matched, silently.
	startNumber, startErr := strconv.Atoi(fmt.Sprint(startValue))
	endNumber, endErr := strconv.Atoi(fmt.Sprint(endValue))
	if startErr == nil && endErr == nil {
		if startNumber >= 1 && startNumber <= 31 && endNumber >= 1 && endNumber <= 31 {
			day := now.Day()
			return day >= startNumber && day <= endNumber
		}
		if startNumber > 31 && endNumber > 31 {
			year := now.Year()
			return year >= startNumber && year <= endNumber
		}
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
		ruleHost, rulePort := splitBypassRuleHostPort(rule)
		if rulePort != "" && rulePort != port {
			continue
		}
		lowerHost := normalizeBypassHost(hostname)
		if !strings.HasPrefix(ruleHost, ".") && !strings.HasPrefix(ruleHost, "*") {
			if lowerHost == normalizeBypassHost(ruleHost) {
				return false
			}
			continue
		}
		// A rule beginning with `*` names a domain, not a substring. Stripping
		// the star and matching the remainder as a suffix made `*example.com`
		// match `fooexample.com` too, sending an unrelated host direct instead
		// of through the proxy it was configured to use -- on a managed
		// machine, traffic leaving by a route the operator did not choose.
		//
		// The dot-prefixed forms already carried their own boundary in the `.`
		// and are matched unchanged.
		lowerRule := normalizeBypassHost(strings.TrimPrefix(ruleHost, "*"))
		if lowerRule == "" {
			// A bare `*`, which names every host. It is handled above when it
			// is the whole list; this is the same rule sharing a list with
			// others, and it means the same thing.
			return false
		}
		if strings.HasPrefix(lowerRule, ".") {
			if strings.HasSuffix(lowerHost, lowerRule) {
				return false
			}
			continue
		}
		if lowerHost == lowerRule || strings.HasSuffix(lowerHost, "."+lowerRule) {
			return false
		}
	}
	return true
}

// splitBypassRuleHostPort separates a bypass rule into host and port.
//
// An IPv6 literal is the awkward case. It carries colons of its own, so the
// "last colon starts the port" shortcut turned a rule of `::1` into host `:`
// and port `1`, and a bracketed `[::1]` was left with its brackets on -- which
// no URL hostname ever has, so it matched nothing. A rule is only split on a
// colon when it has exactly one and no brackets; anything else is an address.
func splitBypassRuleHostPort(rule string) (string, string) {
	if host, port, err := net.SplitHostPort(rule); err == nil {
		return host, port
	}
	if strings.HasPrefix(rule, "[") || strings.Count(rule, ":") != 1 {
		return rule, ""
	}
	index := strings.LastIndex(rule, ":")
	if index <= 0 {
		return rule, ""
	}
	if _, err := strconv.Atoi(rule[index+1:]); err != nil {
		return rule, ""
	}
	return rule[:index], rule[index+1:]
}

// normalizeBypassHost puts a hostname and a bypass rule into the same form
// before they are compared.
//
// url.Hostname() returns an IPv6 address without brackets, while people write
// bypass rules with them. A trailing dot is the fully-qualified spelling of the
// same name, so `example.com.` and `example.com` are one host; treating them as
// two means the list silently misses whichever form the user did not type.
func normalizeBypassHost(host string) string {
	host = strings.TrimSpace(host)
	if len(host) > 1 && strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	for len(host) > 1 && strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	return strings.ToLower(host)
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

// pacHTTPClient fetches PAC files. Posture: verified TLS and NO proxy -- a PAC
// fetch must not be routed through the proxy it is being consulted to discover.
// Settable for the same reason as the interpolator: internal/core owns the
// shared transport cache.
var pacHTTPClient = func() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// SetPACHTTPClient installs the client used to fetch PAC files.
func SetPACHTTPClient(get func() *http.Client) {
	if get != nil {
		pacHTTPClient = get
	}
}

// Package cookiejar holds the rules for storing and sending cookies: domain and
// path matching, the __Secure- and __Host- prefixes, SameSite, expiry, and what
// a response is allowed to set.
//
// US-061 groundwork. These are security rules -- a domain match that is too
// loose sends a cookie to a site that should never see it, and nothing in the
// UI would show that happening -- so they are worth having somewhere with their
// own tests rather than interleaved with request execution.
package cookiejar

import (
	"LiteAPI/internal/codegen"
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

func AttachHeader(item *types.RequestItem, cookies []types.CookieEntry, rawURL string) {
	matching := ForURL(cookies, rawURL)
	if len(matching) == 0 {
		return
	}
	merged := MergeHeader(types.GetKeyValue(item.Headers, "Cookie"), Header(matching))
	item.Headers = types.SetKeyValue(item.Headers, "Cookie", merged)
}

func MergeScriptJar(current, initial, runtime []types.CookieEntry) []types.CookieEntry {
	initialKeys := map[string]bool{}
	for _, cookie := range initial {
		if cookie.Name != "" && cookie.Domain != "" {
			initialKeys[Key(cookie)] = true
		}
	}
	runtimeByKey := map[string]types.CookieEntry{}
	runtimeKeys := make([]string, 0, len(runtime))
	now := time.Now()
	for _, cookie := range runtime {
		if cookie.Name == "" || cookie.Domain == "" || Expired(cookie, now) {
			continue
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		if cookie.ID == "" {
			cookie.ID = ID(cookie)
		}
		if err := ValidateForStorage(cookie, ""); err != nil {
			continue
		}
		key := Key(cookie)
		if _, exists := runtimeByKey[key]; !exists {
			runtimeKeys = append(runtimeKeys, key)
		}
		runtimeByKey[key] = cookie
	}
	next := make([]types.CookieEntry, 0, len(current)+len(runtimeByKey))
	for _, cookie := range current {
		key := Key(cookie)
		if _, replaced := runtimeByKey[key]; replaced {
			continue
		}
		if initialKeys[key] {
			continue
		}
		next = append(next, cookie)
	}
	for _, key := range runtimeKeys {
		next = append(next, runtimeByKey[key])
	}
	return next
}

func PreviewRequestURL(item types.RequestItem, vars map[string]string) string {
	return codegen.RequestURLWithParams(item.URL, item.Params, item.PathParams, vars)
}

func FromResponse(res *http.Response, rawURL string) []types.CookieEntry {
	if res == nil {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return nil
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	now := time.Now()
	result := []types.CookieEntry{}
	for _, raw := range res.Cookies() {
		domain := NormalizeDomain(raw.Domain)
		hostOnly := domain == ""
		if hostOnly {
			domain = host
		}
		cookiePath := raw.Path
		if cookiePath == "" {
			cookiePath = DefaultPath(path)
		} else if !strings.HasPrefix(cookiePath, "/") {
			cookiePath = "/"
		}
		expires := raw.Expires
		session := expires.IsZero() && raw.MaxAge <= 0
		if raw.MaxAge > 0 {
			expires = now.Add(time.Duration(raw.MaxAge) * time.Second)
			session = false
		}
		if raw.MaxAge < 0 {
			expires = now.Add(-time.Second)
			session = false
		}
		entry := types.CookieEntry{
			Name:      raw.Name,
			Value:     raw.Value,
			Domain:    domain,
			Path:      cookiePath,
			Expires:   expires,
			Session:   session,
			Secure:    raw.Secure,
			HTTPOnly:  raw.HttpOnly,
			SameSite:  SameSiteString(raw.SameSite),
			HostOnly:  hostOnly,
			CreatedAt: now,
			UpdatedAt: now,
		}
		entry.ID = ID(entry)
		if err := ValidateForStorage(entry, host); err != nil {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func ForURL(cookies []types.CookieEntry, rawURL string) []types.CookieEntry {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	secure := isPotentiallyTrustworthyCookieURL(parsed)
	now := time.Now()
	matching := []types.CookieEntry{}
	for _, cookie := range cookies {
		if Expired(cookie, now) || cookie.Name == "" || !PrefixValid(cookie) {
			continue
		}
		if err := ValidateForStorage(cookie, ""); err != nil {
			continue
		}
		if cookie.Secure && !secure {
			continue
		}
		if !cookieDomainMatch(cookie, host) || !cookiePathMatch(cookie.Path, path) {
			continue
		}
		matching = append(matching, cookie)
	}
	sort.SliceStable(matching, func(i, j int) bool {
		if len(matching[i].Path) != len(matching[j].Path) {
			return len(matching[i].Path) > len(matching[j].Path)
		}
		return matching[i].CreatedAt.Before(matching[j].CreatedAt)
	})
	return matching
}

func isPotentiallyTrustworthyCookieURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss", "file":
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func Header(cookies []types.CookieEntry) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func MergeHeader(manual, stored string) string {
	type cookiePair struct {
		name  string
		value string
	}
	order := []string{}
	values := map[string]cookiePair{}
	add := func(header string) {
		for _, part := range strings.Split(header, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			name = strings.TrimSpace(name)
			if !ok || name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, exists := values[key]; !exists {
				order = append(order, key)
			}
			values[key] = cookiePair{name: name, value: strings.TrimSpace(value)}
		}
	}
	add(manual)
	add(stored)
	parts := make([]string, 0, len(order))
	for _, key := range order {
		pair := values[key]
		parts = append(parts, pair.name+"="+pair.value)
	}
	return strings.Join(parts, "; ")
}

func NormalizeManual(input types.CookieInput) (types.CookieEntry, error) {
	name := strings.TrimSpace(input.Name)
	value := strings.TrimSpace(input.Value)
	domain := NormalizeDomain(input.Domain)
	path := strings.TrimSpace(input.Path)
	sameSite := normalizeCookieSameSite(input.SameSite)
	if name == "" {
		return types.CookieEntry{}, errors.New("cookie name is required")
	}
	if strings.ContainsAny(name, ";\r\n\t ") {
		return types.CookieEntry{}, errors.New("cookie name cannot contain whitespace or semicolons")
	}
	if domain == "" {
		return types.CookieEntry{}, errors.New("cookie domain is required")
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	expires, session, err := parseCookieExpiry(input.Expires, input.Session)
	if err != nil {
		return types.CookieEntry{}, err
	}
	cookie := types.CookieEntry{
		ID:       input.ID,
		Name:     name,
		Value:    value,
		Domain:   domain,
		Path:     path,
		Expires:  expires,
		Session:  session,
		Secure:   input.Secure,
		HTTPOnly: input.HTTPOnly,
		SameSite: sameSite,
		HostOnly: input.HostOnly,
	}
	cookie.ID = ID(cookie)
	return cookie, nil
}

func parseCookieExpiry(value string, session bool) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if session || value == "" {
		return time.Time{}, true, nil
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("cookie expires value must be RFC3339 or YYYY-MM-DD")
}

func NormalizeDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, ".")
	return strings.TrimSuffix(domain, ".")
}

func normalizeCookieSameSite(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "default", "lax", "strict", "none":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func PluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func Expired(cookie types.CookieEntry, now time.Time) bool {
	return !cookie.Session && !cookie.Expires.IsZero() && !cookie.Expires.After(now)
}

func cookieDomainMatch(cookie types.CookieEntry, host string) bool {
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	host = strings.ToLower(strings.TrimSpace(host))
	if cookie.HostOnly {
		return host == domain
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func cookiePathMatch(cookiePath, requestPath string) bool {
	if cookiePath == "" {
		cookiePath = "/"
	}
	if requestPath == "" {
		requestPath = "/"
	}
	if cookiePath == "/" {
		return true
	}
	return requestPath == cookiePath || strings.HasPrefix(requestPath, strings.TrimRight(cookiePath, "/")+"/")
}

func PrefixValid(cookie types.CookieEntry) bool {
	if strings.HasPrefix(cookie.Name, "__Host-") {
		return cookie.Secure && cookie.HostOnly && cookie.Path == "/"
	}
	if strings.HasPrefix(cookie.Name, "__Secure-") {
		return cookie.Secure
	}
	return true
}

func ValidateForStorage(cookie types.CookieEntry, sourceHost string) error {
	name := strings.TrimSpace(cookie.Name)
	domain := NormalizeDomain(cookie.Domain)
	path := strings.TrimSpace(cookie.Path)
	if !validCookieName(name) {
		return errors.New("cookie name cannot contain control characters or separators")
	}
	if domain == "" {
		return errors.New("cookie domain is required")
	}
	if !validCookieDomain(domain) {
		return fmt.Errorf("cookie domain %q is invalid", domain)
	}
	if ip := net.ParseIP(domain); ip != nil && !cookie.HostOnly {
		return fmt.Errorf("cookie domain %q must be host-only", domain)
	}
	if cookieDomainIsPublicSuffix(domain) {
		return fmt.Errorf("cookie domain %q is a public suffix", domain)
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return errors.New("cookie path must start with /")
	}
	if !PrefixValid(cookie) {
		return fmt.Errorf("cookie %s violates prefix requirements", cookie.Name)
	}
	sourceHost = NormalizeDomain(sourceHost)
	if sourceHost == "" {
		return nil
	}
	if !validCookieDomain(sourceHost) {
		return errors.New("current request URL is required for cookie writes")
	}
	if cookie.HostOnly {
		if domain != sourceHost {
			return fmt.Errorf("cookie domain %q does not match request host %q", domain, sourceHost)
		}
		return nil
	}
	if !cookieDomainMatch(cookie, sourceHost) {
		return fmt.Errorf("cookie domain %q does not match request host %q", domain, sourceHost)
	}
	return nil
}

func validCookieName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}

func validCookieDomain(domain string) bool {
	if domain == "" {
		return false
	}
	if net.ParseIP(domain) != nil {
		return true
	}
	if strings.ContainsAny(domain, " \t\r\n/:") || strings.Contains(domain, "..") {
		return false
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" {
			return false
		}
	}
	return true
}

func cookieDomainIsPublicSuffix(domain string) bool {
	if domain == "localhost" || strings.HasSuffix(domain, ".localhost") || net.ParseIP(domain) != nil {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(domain)
	if suffix != domain {
		return false
	}
	return icann || strings.Contains(domain, ".")
}

func DefaultPath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' {
		return "/"
	}
	if requestPath == "/" {
		return "/"
	}
	index := strings.LastIndex(requestPath, "/")
	if index <= 0 {
		return "/"
	}
	return requestPath[:index]
}

func ID(cookie types.CookieEntry) string {
	return scalar.DeterministicID("cookie", Key(cookie))
}

func Key(cookie types.CookieEntry) string {
	return strings.ToLower(cookie.Domain) + "|" + cookie.Path + "|" + cookie.Name
}

func SameSiteString(value http.SameSite) string {
	switch value {
	case http.SameSiteDefaultMode:
		return "default"
	case http.SameSiteLaxMode:
		return "lax"
	case http.SameSiteStrictMode:
		return "strict"
	case http.SameSiteNoneMode:
		return "none"
	default:
		return ""
	}
}

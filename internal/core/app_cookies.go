package core

// The cookie jar the app owns: storing, pruning and attaching them.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
)

func (a *App) DeleteCookie(cookieID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	next := a.state.Cookies[:0]
	removed := false
	for _, cookie := range a.state.Cookies {
		if cookie.ID == cookieID {
			removed = true
			continue
		}
		next = append(next, cookie)
	}
	if !removed {
		return AppState{}, fmt.Errorf("cookie %s not found", cookieID)
	}
	a.state.Cookies = next
	a.notify("info", "Cookie deleted")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SaveCookie(input CookieInput) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	normalized, err := cookiejar.NormalizeManual(input)
	if err != nil {
		return AppState{}, err
	}
	if !cookiejar.PrefixValid(normalized) {
		return AppState{}, fmt.Errorf("cookie %s violates prefix requirements", normalized.Name)
	}
	if err := cookiejar.ValidateForStorage(normalized, ""); err != nil {
		return AppState{}, err
	}
	now := time.Now()
	normalized.UpdatedAt = now
	next := a.state.Cookies[:0]
	for _, existing := range a.state.Cookies {
		if existing.ID == input.ID || cookiejar.Key(existing) == cookiejar.Key(normalized) {
			if !existing.CreatedAt.IsZero() && normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = existing.CreatedAt
			}
			continue
		}
		next = append(next, existing)
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	a.state.Cookies = append(next, normalized)
	a.pruneExpiredCookiesLocked()
	a.notify("success", "Cookie saved")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) AddCookieFromHeader(rawHeader, sourceURL string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if strings.TrimSpace(sourceURL) == "" {
		return AppState{}, errors.New("cookie URL is required")
	}
	if strings.TrimSpace(rawHeader) == "" {
		return AppState{}, errors.New("Set-Cookie value is required")
	}
	header := http.Header{}
	for _, line := range strings.Split(rawHeader, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			header.Add("Set-Cookie", line)
		}
	}
	cookies := cookiejar.FromResponse(&http.Response{Header: header}, sourceURL)
	if len(cookies) == 0 {
		return AppState{}, errors.New("no valid Set-Cookie values found")
	}
	a.storeResponseCookiesLocked(cookies)
	a.notify("success", fmt.Sprintf("Imported %d cookie%s", len(cookies), cookiejar.PluralSuffix(len(cookies))))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearDomainCookies(domain string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	domain = cookiejar.NormalizeDomain(domain)
	if domain == "" {
		return AppState{}, errors.New("cookie domain is required")
	}
	next := a.state.Cookies[:0]
	removed := 0
	for _, cookie := range a.state.Cookies {
		if cookiejar.NormalizeDomain(cookie.Domain) == domain {
			removed++
			continue
		}
		next = append(next, cookie)
	}
	if removed == 0 {
		return AppState{}, fmt.Errorf("no cookies found for %s", domain)
	}
	a.state.Cookies = next
	a.notify("info", fmt.Sprintf("Cleared %d cookie%s for %s", removed, cookiejar.PluralSuffix(removed), domain))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearCookies() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	a.state.Cookies = []CookieEntry{}
	a.notify("info", "Cookies cleared")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) storeResponseCookiesLocked(cookies []CookieEntry) {
	if len(cookies) == 0 {
		a.pruneExpiredCookiesLocked()
		return
	}
	now := time.Now()
	for _, cookie := range cookies {
		if cookie.Name == "" || cookie.Domain == "" {
			continue
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		if cookie.ID == "" {
			cookie.ID = cookiejar.ID(cookie)
		}
		if err := cookiejar.ValidateForStorage(cookie, ""); err != nil {
			continue
		}
		cookie.UpdatedAt = now
		if cookie.CreatedAt.IsZero() {
			cookie.CreatedAt = now
		}
		key := cookiejar.Key(cookie)
		next := a.state.Cookies[:0]
		for _, existing := range a.state.Cookies {
			if cookiejar.Key(existing) == key {
				if !existing.CreatedAt.IsZero() {
					cookie.CreatedAt = existing.CreatedAt
				}
				continue
			}
			next = append(next, existing)
		}
		if cookiejar.Expired(cookie, now) {
			a.state.Cookies = next
			continue
		}
		a.state.Cookies = append(next, cookie)
	}
	a.pruneExpiredCookiesLocked()
}

func (a *App) pruneExpiredCookiesLocked() {
	now := time.Now()
	next := a.state.Cookies[:0]
	for _, cookie := range a.state.Cookies {
		if !cookiejar.Expired(cookie, now) {
			next = append(next, cookie)
		}
	}
	a.state.Cookies = next
}

func attachCookiesToHTTPRequest(req *http.Request, cookies []CookieEntry) {
	if req == nil || req.URL == nil {
		return
	}
	matching := cookiejar.ForURL(cookies, req.URL.String())
	if len(matching) == 0 {
		return
	}
	req.Header.Set("Cookie", cookiejar.MergeHeader(req.Header.Get("Cookie"), cookiejar.Header(matching)))
}

package scripting

// Cookie jar state operations shared by the shim and the runtime.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"net/url"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/types"
)

func (j *CookieJar) Snapshot() []types.CookieEntry {
	if j == nil {
		return nil
	}
	return CloneCookieEntries(j.cookies)
}

func (j *CookieJar) matching(rawURL string) []types.CookieEntry {
	if j == nil {
		return nil
	}
	return cookiejar.ForURL(j.cookies, rawURL)
}

func (j *CookieJar) upsert(cookie types.CookieEntry, sourceHosts ...string) {
	if j == nil || cookie.Name == "" || cookie.Domain == "" {
		return
	}
	sourceHost := ""
	if len(sourceHosts) > 0 {
		sourceHost = sourceHosts[0]
	}
	now := time.Now()
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if cookie.ID == "" {
		cookie.ID = cookiejar.ID(cookie)
	}
	if err := cookiejar.ValidateForStorage(cookie, sourceHost); err != nil {
		return
	}
	if cookie.CreatedAt.IsZero() {
		cookie.CreatedAt = now
	}
	cookie.UpdatedAt = now
	key := cookiejar.Key(cookie)
	next := j.cookies[:0]
	for _, existing := range j.cookies {
		if cookiejar.Key(existing) == key {
			if !existing.CreatedAt.IsZero() {
				cookie.CreatedAt = existing.CreatedAt
			}
			continue
		}
		next = append(next, existing)
	}
	if cookiejar.Expired(cookie, now) {
		j.cookies = next
		return
	}
	j.cookies = append(next, cookie)
}

func (j *CookieJar) UpsertAll(cookies []types.CookieEntry) {
	for _, cookie := range cookies {
		j.upsert(cookie)
	}
}

func (j *CookieJar) removeMatching(rawURL, name string) {
	if j == nil || strings.TrimSpace(name) == "" {
		return
	}
	removeKeys := map[string]bool{}
	for _, cookie := range j.matching(rawURL) {
		if cookie.Name == name {
			removeKeys[cookiejar.Key(cookie)] = true
		}
	}
	if len(removeKeys) == 0 {
		return
	}
	next := j.cookies[:0]
	for _, cookie := range j.cookies {
		if !removeKeys[cookiejar.Key(cookie)] {
			next = append(next, cookie)
		}
	}
	j.cookies = next
}

func (j *CookieJar) clearMatching(rawURL string) {
	if j == nil {
		return
	}
	removeKeys := map[string]bool{}
	for _, cookie := range j.matching(rawURL) {
		removeKeys[cookiejar.Key(cookie)] = true
	}
	if len(removeKeys) == 0 {
		return
	}
	next := j.cookies[:0]
	for _, cookie := range j.cookies {
		if !removeKeys[cookiejar.Key(cookie)] {
			next = append(next, cookie)
		}
	}
	j.cookies = next
}

func cookieSourceHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

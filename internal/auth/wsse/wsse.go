// Package wsse applies WS-Security UsernameToken headers.
//
// US-065 groundwork. It landed here rather than in grpcexec because both the
// HTTP and gRPC paths apply it -- it is authentication, not a transport detail,
// and dragging its crypto into a gRPC package would have put it somewhere
// nobody would look for it.
package wsse

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func ApplyHeader(headers http.Header, username, password string, now time.Time) {
	created := now.UTC().Format("2006-01-02T15:04:05.000Z")
	nonce := RandomHex(16)
	digest := PasswordDigest(nonce, created, password)
	headers.Set("X-WSSE", `UsernameToken Username="`+QuoteDigestValue(username)+`", PasswordDigest="`+digest+`", Nonce="`+nonce+`", Created="`+created+`"`)
}

func RandomHex(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(data)
}
func PasswordDigest(nonce, created, password string) string {
	sum := sha1.Sum([]byte(nonce + created + password))
	return base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(sum[:])))
}
func QuoteDigestValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

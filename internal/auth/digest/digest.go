// Package digest implements HTTP Digest authentication: parsing the
// WWW-Authenticate challenge, choosing a qop, and building the Authorization
// header for the retry.
//
// US-065. Digest is a two-round-trip scheme -- the first request is EXPECTED to
// 401, and the header can only be built from the challenge that comes back --
// so the retry decision lives here too.
package digest

import (
	"LiteAPI/internal/auth/wsse"
	"LiteAPI/internal/interp"
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func ShouldRetry(res *http.Response, auth types.AuthConfig) bool {
	if res == nil || res.StatusCode != http.StatusUnauthorized || strings.ToLower(auth.Mode) != "digest" {
		return false
	}
	return strings.Contains(strings.ToLower(res.Header.Get("WWW-Authenticate")), "digest")
}

func CloneRequest(req *http.Request, auth types.AuthConfig, vars map[string]string, challenge string) (*http.Request, error) {
	if req == nil {
		return nil, errors.New("missing request for digest retry")
	}
	var body io.Reader
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	} else if req.ContentLength == 0 || req.Body == nil {
		body = nil
	} else {
		return nil, errors.New("digest auth retry requires a rewindable request body")
	}
	retry, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), body)
	if err != nil {
		return nil, err
	}
	retry.Header = req.Header.Clone()
	retry.ContentLength = req.ContentLength
	header, err := AuthorizationHeader(req.Method, req.URL.RequestURI(), auth, vars, challenge)
	if err != nil {
		return nil, err
	}
	retry.Header.Set("Authorization", header)
	return retry, nil
}

func AuthorizationHeader(method, uri string, auth types.AuthConfig, vars map[string]string, challenge string) (string, error) {
	params := ParseChallenge(challenge)
	if len(params) == 0 {
		return "", errors.New("missing digest challenge")
	}
	realm := params["realm"]
	nonce := params["nonce"]
	if realm == "" || nonce == "" {
		return "", errors.New("digest challenge is missing realm or nonce")
	}
	username := interp.Interpolate(auth.Username, vars)
	password := interp.Interpolate(auth.Password, vars)
	if username == "" {
		return "", errors.New("digest username is required")
	}
	algorithm := strings.ToUpper(scalar.FirstNonEmpty(params["algorithm"], "MD5"))
	if algorithm != "MD5" && algorithm != "MD5-SESS" {
		return "", fmt.Errorf("unsupported digest algorithm %s", algorithm)
	}
	qop := ChooseQop(params["qop"])
	cnonce := wsse.RandomHex(8)
	nc := "00000001"
	ha1 := MD5Hex(username + ":" + realm + ":" + password)
	if algorithm == "MD5-SESS" {
		ha1 = MD5Hex(ha1 + ":" + nonce + ":" + cnonce)
	}
	ha2 := MD5Hex(strings.ToUpper(method) + ":" + uri)
	responseSeed := ha1 + ":" + nonce + ":"
	if qop != "" {
		responseSeed += nc + ":" + cnonce + ":" + qop + ":"
	}
	response := MD5Hex(responseSeed + ha2)
	parts := []string{
		`username="` + wsse.QuoteDigestValue(username) + `"`,
		`realm="` + wsse.QuoteDigestValue(realm) + `"`,
		`nonce="` + wsse.QuoteDigestValue(nonce) + `"`,
		`uri="` + wsse.QuoteDigestValue(uri) + `"`,
		`response="` + response + `"`,
	}
	if algorithm != "" {
		parts = append(parts, `algorithm=`+algorithm)
	}
	if opaque := params["opaque"]; opaque != "" {
		parts = append(parts, `opaque="`+wsse.QuoteDigestValue(opaque)+`"`)
	}
	if qop != "" {
		parts = append(parts, `qop=`+qop, `nc=`+nc, `cnonce="`+cnonce+`"`)
	}
	return "Digest " + strings.Join(parts, ", "), nil
}

func ParseChallenge(challenge string) map[string]string {
	challenge = strings.TrimSpace(challenge)
	if strings.HasPrefix(strings.ToLower(challenge), "digest") {
		challenge = strings.TrimSpace(challenge[len("digest"):])
	}
	params := map[string]string{}
	for _, part := range SplitParts(challenge) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		if key != "" {
			params[key] = value
		}
	}
	return params
}

func SplitParts(value string) []string {
	parts := []string{}
	var b strings.Builder
	inQuotes := false
	escaped := false
	for _, r := range value {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inQuotes:
			escaped = true
			b.WriteRune(r)
		case r == '"':
			inQuotes = !inQuotes
			b.WriteRune(r)
		case r == ',' && !inQuotes:
			if strings.TrimSpace(b.String()) != "" {
				parts = append(parts, strings.TrimSpace(b.String()))
			}
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		parts = append(parts, strings.TrimSpace(b.String()))
	}
	return parts
}

func ChooseQop(value string) string {
	for _, part := range strings.Split(value, ",") {
		qop := strings.ToLower(strings.TrimSpace(part))
		if qop == "auth" {
			return "auth"
		}
	}
	return ""
}

func MD5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

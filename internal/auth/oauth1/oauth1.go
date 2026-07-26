// Package oauth1 signs requests with OAuth 1.0a: the signature base string,
// HMAC-SHA1/256 and RSA-SHA1/256 signatures, the Authorization header, and the
// body hash extension.
//
// US-066. Free functions throughout, like awsv4 -- signing never needed *App.
package oauth1

import (
	"LiteAPI/internal/auth/wsse"
	"LiteAPI/internal/interp"
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Sign(req *http.Request, item *types.RequestItem, auth types.OAuth1Auth, vars map[string]string, now time.Time) error {
	if req == nil {
		return errors.New("missing request for OAuth1 signing")
	}
	cfg := interpolateOAuth1Auth(auth, vars)
	if cfg.ConsumerKey == "" {
		return errors.New("OAuth1 consumer key is required")
	}
	method := strings.ToUpper(scalar.FirstNonEmpty(cfg.SignatureMethod, "HMAC-SHA1"))
	if cfg.Version == "" {
		cfg.Version = "1.0"
	}
	if cfg.Placement == "" {
		cfg.Placement = "header"
	}
	bodyString, err := oauth1BodyString(req)
	if err != nil {
		return err
	}
	isForm := strings.HasPrefix(strings.ToLower(req.Header.Get("Content-Type")), "application/x-www-form-urlencoded")
	hasBody := req.Method != http.MethodGet && req.Method != http.MethodHead
	dataPairs := [][2]string{}
	if (cfg.Placement == "body" || hasBody) && isForm && bodyString != "" {
		dataPairs = append(dataPairs, parseOAuth1FormPairs(bodyString)...)
	}
	if cfg.IncludeBodyHash && !isForm {
		dataPairs = append(dataPairs, [2]string{"oauth_body_hash", BodyHash(bodyString, method)})
	}
	oauthParams := map[string]string{
		"oauth_consumer_key":     cfg.ConsumerKey,
		"oauth_nonce":            scalar.FirstNonEmpty(cfg.Nonce, oauth1Nonce()),
		"oauth_signature_method": method,
		"oauth_timestamp":        scalar.FirstNonEmpty(cfg.Timestamp, strconv.FormatInt(now.Unix(), 10)),
		"oauth_version":          scalar.FirstNonEmpty(cfg.Version, "1.0"),
	}
	if cfg.AccessToken != "" {
		oauthParams["oauth_token"] = cfg.AccessToken
	}
	if cfg.CallbackURL != "" {
		oauthParams["oauth_callback"] = cfg.CallbackURL
	}
	if cfg.Verifier != "" {
		oauthParams["oauth_verifier"] = cfg.Verifier
	}
	bodyParams := [][2]string{}
	for _, pair := range dataPairs {
		if strings.HasPrefix(pair[0], "oauth_") {
			oauthParams[pair[0]] = pair[1]
			continue
		}
		bodyParams = append(bodyParams, pair)
	}
	extraParams := append(QueryPairs(req.URL.RawQuery), bodyParams...)
	baseURL := BaseURL(req.URL)
	parameterString := ParameterString(oauthParams, extraParams)
	baseString := strings.ToUpper(req.Method) + "&" + Encode(baseURL) + "&" + Encode(parameterString)
	signature, err := Signature(baseString, cfg.ConsumerSecret, cfg.AccessTokenSecret, method, cfg.PrivateKey, cfg.PrivateKeyType, item)
	if err != nil {
		return err
	}
	oauthParams["oauth_signature"] = signature
	switch cfg.Placement {
	case "header":
		req.Header.Set("Authorization", oauth1AuthorizationHeader(oauthParams, cfg.Realm))
	case "query":
		q := req.URL.Query()
		for key, value := range oauthParams {
			if value != "" {
				q.Set(key, value)
			}
		}
		req.URL.RawQuery = q.Encode()
	case "body":
		params, _ := url.ParseQuery("")
		if isForm && bodyString != "" {
			params, _ = url.ParseQuery(bodyString)
		}
		for key, value := range oauthParams {
			params.Set(key, value)
		}
		encoded := params.Encode()
		setRequestBodyString(req, encoded)
		if !isForm {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	default:
		return fmt.Errorf("unsupported OAuth1 placement %s", cfg.Placement)
	}
	return nil
}

func interpolateOAuth1Auth(auth types.OAuth1Auth, vars map[string]string) types.OAuth1Auth {
	return types.OAuth1Auth{
		ConsumerKey:       interp.Interpolate(auth.ConsumerKey, vars),
		ConsumerSecret:    interp.Interpolate(auth.ConsumerSecret, vars),
		AccessToken:       interp.Interpolate(auth.AccessToken, vars),
		AccessTokenSecret: interp.Interpolate(auth.AccessTokenSecret, vars),
		CallbackURL:       interp.Interpolate(auth.CallbackURL, vars),
		Verifier:          interp.Interpolate(auth.Verifier, vars),
		SignatureMethod:   interp.Interpolate(auth.SignatureMethod, vars),
		PrivateKey:        interp.Interpolate(auth.PrivateKey, vars),
		PrivateKeyType:    interp.Interpolate(auth.PrivateKeyType, vars),
		Timestamp:         interp.Interpolate(auth.Timestamp, vars),
		Nonce:             interp.Interpolate(auth.Nonce, vars),
		Version:           interp.Interpolate(auth.Version, vars),
		Realm:             interp.Interpolate(auth.Realm, vars),
		Placement:         interp.Interpolate(auth.Placement, vars),
		IncludeBodyHash:   auth.IncludeBodyHash,
	}
}

func oauth1BodyString(req *http.Request) (string, error) {
	if req == nil || req.Body == nil || req.ContentLength == 0 {
		return "", nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return "", err
		}
		defer func() { _ = body.Close() }()
		data, err := io.ReadAll(body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if seeker, ok := req.Body.(interface {
		io.Reader
		io.Seeker
	}); ok {
		data, err := io.ReadAll(seeker)
		if err != nil {
			return "", err
		}
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		return string(data), nil
	}
	return "", errors.New("OAuth1 signing requires a rewindable request body")
}

func setRequestBodyString(req *http.Request, value string) {
	data := []byte(value)
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.ContentLength = int64(len(data))
}

func parseOAuth1FormPairs(raw string) [][2]string {
	pairs := [][2]string{}
	if raw == "" {
		return pairs
	}
	for _, part := range strings.Split(raw, "&") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			value = ""
		}
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			decodedValue = value
		}
		pairs = append(pairs, [2]string{decodedKey, decodedValue})
	}
	return pairs
}

func QueryPairs(rawQuery string) [][2]string {
	pairs := [][2]string{}
	if rawQuery == "" {
		return pairs
	}
	safeQuery := strings.ReplaceAll(rawQuery, "+", "%2B")
	for _, part := range strings.Split(safeQuery, "&") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			value = ""
		}
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			decodedValue = value
		}
		pairs = append(pairs, [2]string{decodedKey, decodedValue})
	}
	return pairs
}

func BaseURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	isDefaultPort := (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
	if port != "" && !isDefaultPort {
		host += ":" + port
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

func ParameterString(oauthParams map[string]string, extraPairs [][2]string) string {
	pairs := make([][2]string, 0, len(oauthParams)+len(extraPairs))
	for key, value := range oauthParams {
		pairs = append(pairs, [2]string{Encode(key), Encode(value)})
	}
	for _, pair := range extraPairs {
		pairs = append(pairs, [2]string{Encode(pair[0]), Encode(pair[1])})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] == pairs[j][0] {
			return pairs[i][1] < pairs[j][1]
		}
		return pairs[i][0] < pairs[j][0]
	})
	out := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, pair[0]+"="+pair[1])
	}
	return strings.Join(out, "&")
}

func Signature(baseString, consumerSecret, tokenSecret, method, privateKey, privateKeyType string, item *types.RequestItem) (string, error) {
	signingKey := Encode(consumerSecret) + "&" + Encode(tokenSecret)
	switch method {
	case "PLAINTEXT":
		return signingKey, nil
	case "HMAC-SHA1":
		return base64.StdEncoding.EncodeToString(hmacSHA1Bytes([]byte(signingKey), baseString)), nil
	case "HMAC-SHA256":
		return base64.StdEncoding.EncodeToString(HMACSHA256Bytes([]byte(signingKey), baseString)), nil
	case "HMAC-SHA512":
		mac := hmac.New(sha512.New, []byte(signingKey))
		mac.Write([]byte(baseString))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
	case "RSA-SHA1", "RSA-SHA256", "RSA-SHA512":
		privateKeyPEM, err := oauth1PrivateKeyMaterial(privateKey, privateKeyType, item)
		if err != nil {
			return "", err
		}
		rsaKey, err := parseOAuth1RSAPrivateKey(privateKeyPEM)
		if err != nil {
			return "", err
		}
		hashType, digest, err := RSADigest(baseString, method)
		if err != nil {
			return "", err
		}
		signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, hashType, digest)
		if err != nil {
			return "", fmt.Errorf("OAuth1 RSA signing failed: %w", err)
		}
		return base64.StdEncoding.EncodeToString(signature), nil
	default:
		return "", fmt.Errorf("unsupported OAuth1 signature method %s", method)
	}
}

func oauth1PrivateKeyMaterial(privateKey, privateKeyType string, item *types.RequestItem) (string, error) {
	key := strings.TrimSpace(privateKey)
	if key == "" {
		return "", errors.New("OAuth1 RSA private key is required")
	}
	if parsedKey, parsedType := ParsePrivateKeyValue(key); parsedType == "file" {
		key = parsedKey
		privateKeyType = "file"
	}
	if strings.EqualFold(strings.TrimSpace(privateKeyType), "file") {
		path := key
		if !filepath.IsAbs(path) {
			basePath := oauth1CollectionBasePath(item)
			if basePath == "" {
				return "", fmt.Errorf("OAuth1 private key path %q is relative but request file path is unknown", key)
			}
			path = filepath.Join(basePath, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read OAuth1 private key %q: %w", path, err)
		}
		return string(data), nil
	}
	return key, nil
}

func oauth1CollectionBasePath(item *types.RequestItem) string {
	if item == nil || strings.TrimSpace(item.FilePath) == "" {
		return ""
	}
	dir := filepath.Clean(filepath.Dir(item.FilePath))
	for {
		for _, marker := range []string{"bruno.json", "collection.bru", "opencollection.yml"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Clean(filepath.Dir(item.FilePath))
}

func parseOAuth1RSAPrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	// Trimmed because pem.Decode needs the BEGIN marker at the start of a line.
	// A key pasted with a leading space is otherwise rejected as "not PEM
	// encoded", which reads as plainly wrong to someone looking at a PEM block.
	block, _ := pem.Decode([]byte(strings.TrimSpace(privateKeyPEM)))
	if block == nil {
		return nil, errors.New("OAuth1 RSA private key must be PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse OAuth1 RSA private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("OAuth1 private key is not an RSA key")
	}
	return rsaKey, nil
}

func RSADigest(baseString, method string) (crypto.Hash, []byte, error) {
	switch method {
	case "RSA-SHA1":
		sum := sha1.Sum([]byte(baseString))
		return crypto.SHA1, sum[:], nil
	case "RSA-SHA256":
		sum := sha256.Sum256([]byte(baseString))
		return crypto.SHA256, sum[:], nil
	case "RSA-SHA512":
		sum := sha512.Sum512([]byte(baseString))
		return crypto.SHA512, sum[:], nil
	default:
		return 0, nil, fmt.Errorf("unsupported OAuth1 RSA signature method %s", method)
	}
}

func oauth1AuthorizationHeader(oauthParams map[string]string, realm string) string {
	keys := make([]string, 0, len(oauthParams))
	for key := range oauthParams {
		if strings.HasPrefix(key, "oauth_") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := []string{}
	if realm != "" {
		parts = append(parts, `realm="`+strings.ReplaceAll(strings.ReplaceAll(realm, `\`, `\\`), `"`, `\"`)+`"`)
	}
	for _, key := range keys {
		parts = append(parts, Encode(key)+`="`+Encode(oauthParams[key])+`"`)
	}
	return "OAuth " + strings.Join(parts, ", ")
}

func oauth1Nonce() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return wsse.RandomHex(16)
	}
	var b strings.Builder
	for _, value := range bytes {
		b.WriteByte(chars[int(value)%len(chars)])
	}
	return b.String()
}

func BodyHash(body, method string) string {
	switch method {
	case "HMAC-SHA512", "RSA-SHA512":
		sum := sha512.Sum512([]byte(body))
		return base64.StdEncoding.EncodeToString(sum[:])
	case "HMAC-SHA256", "RSA-SHA256":
		sum := sha256.Sum256([]byte(body))
		return base64.StdEncoding.EncodeToString(sum[:])
	default:
		sum := sha1.Sum([]byte(body))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
}

func Encode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func hmacSHA1Bytes(key []byte, value string) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}
func HMACSHA256Bytes(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}
func ParsePrivateKeyValue(value string) (string, string) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@file(") && strings.HasSuffix(value, ")") {
		return strings.TrimSuffix(strings.TrimPrefix(value, "@file("), ")"), "file"
	}
	return value, "text"
}

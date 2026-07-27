package core

// Turning a stored request into an outgoing one: body, auth and assertions.
//
// Split out of app.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
	"github.com/mutexdev/lite_api/internal/auth/oauth1"
	"github.com/mutexdev/lite_api/internal/auth/wsse"
	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scripting"
)

func (t cookieCapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(req)
	if res != nil && t.jar != nil {
		sourceURL := ""
		if res.Request != nil && res.Request.URL != nil {
			sourceURL = res.Request.URL.String()
		} else if req != nil && req.URL != nil {
			sourceURL = req.URL.String()
		}
		t.jar.UpsertAll(cookiejar.FromResponse(res, sourceURL))
	}
	return res, err
}

func (a *App) effectiveRequestContextForExecution(collectionID, itemID, environmentID string) (RequestItem, Collection, map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	vars := scripting.BuildVariableMap(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, ws.Path)
	return requestCopy, collectionCopy, vars, nil
}

func buildBody(body RequestBody, vars map[string]string, basePath ...string) (io.Reader, string, error) {
	switch body.Mode {
	case "", "none":
		return nil, "", nil
	case "json":
		return strings.NewReader(interpolate(body.JSON, vars)), "application/json", nil
	case "xml":
		return strings.NewReader(interpolate(body.XML, vars)), "application/xml", nil
	case "graphql":
		return strings.NewReader(codegen.GraphQLRequestBodySnapshot(body, vars)), "application/json", nil
	case "text", "sparql":
		return strings.NewReader(interpolate(body.Text, vars)), "text/plain", nil
	case "formUrlEncoded":
		values := url.Values{}
		for _, field := range body.FormURLEncoded {
			if field.Enabled {
				values.Add(interpolate(field.Name, vars), interpolate(field.Value, vars))
			}
		}
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
	case "multipartForm":
		var builder strings.Builder
		writer := multipart.NewWriter(&builder)
		for _, part := range body.Multipart {
			if !part.Enabled {
				continue
			}
			partName := interpolate(part.Name, vars)
			contentType := strings.TrimSpace(interpolate(part.ContentType, vars))
			if part.FilePath != "" {
				filePath := resolveBodyFilePath(interpolate(part.FilePath, vars), basePath...)
				header := textproto.MIMEHeader{}
				header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": partName, "filename": filepath.Base(filePath)}))
				if contentType != "" {
					header.Set("Content-Type", contentType)
				} else {
					header.Set("Content-Type", "application/octet-stream")
				}
				w, err := writer.CreatePart(header)
				if err != nil {
					return nil, "", err
				}
				file, err := os.Open(filePath)
				if err != nil {
					return nil, "", err
				}
				_, copyErr := io.Copy(w, file)
				closeErr := file.Close()
				if copyErr != nil {
					return nil, "", copyErr
				}
				if closeErr != nil {
					return nil, "", closeErr
				}
				continue
			}
			partValue := interpolate(part.Value, vars)
			if contentType == "" {
				if err := writer.WriteField(partName, partValue); err != nil {
					return nil, "", err
				}
			} else {
				header := textproto.MIMEHeader{}
				header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": partName}))
				header.Set("Content-Type", contentType)
				w, err := writer.CreatePart(header)
				if err != nil {
					return nil, "", err
				}
				if _, err := io.WriteString(w, partValue); err != nil {
					return nil, "", err
				}
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", err
		}
		return strings.NewReader(builder.String()), writer.FormDataContentType(), nil
	case "file":
		selected, ok := selectedFileBodyEntry(body)
		contentType := strings.TrimSpace(interpolate(selected.ContentType, vars))
		if !ok || strings.TrimSpace(selected.FilePath) == "" {
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			return nil, contentType, nil
		}
		filePath := resolveBodyFilePath(interpolate(selected.FilePath, vars), basePath...)
		if contentType == "" {
			contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		file, err := os.Open(filePath)
		return file, contentType, err
	default:
		return strings.NewReader(interpolate(body.Text, vars)), "text/plain", nil
	}
}

func resolveBodyFilePath(filePath string, basePath ...string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || filepath.IsAbs(filePath) || len(basePath) == 0 || strings.TrimSpace(basePath[0]) == "" {
		return filePath
	}
	return filepath.Join(basePath[0], filepath.FromSlash(filePath))
}

func (a *App) applyAuth(req *http.Request, collectionPath string, item *RequestItem, vars map[string]string, recordTimeline func(TimelineItem)) error {
	return applyAuthWithOAuth2Fetcher(req, item, vars, func(auth OAuth2Auth, vars map[string]string) (string, error) {
		token, timelineEntries, err := a.fetchOAuth2TokenWithTimeline(auth, vars)
		if recordTimeline != nil {
			for _, entry := range timelineEntries {
				entry.ID = newID("timeline")
				entry.Kind = "oauth2"
				entry.Source = "oauth2.0"
				entry.RequestID = item.ID
				entry.SourceFile = timelineSourceFileForItem(collectionPath, *item)
				if entry.Message == "" {
					statusLabel := entry.StatusText
					if entry.Status > 0 {
						statusLabel = strconv.Itoa(entry.Status)
					}
					entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
				}
				recordTimeline(entry)
			}
		}
		return token, err
	})
}

func applyAuth(req *http.Request, item *RequestItem, vars map[string]string) error {
	return applyAuthWithOAuth2Fetcher(req, item, vars, fetchOAuth2Token)
}

func applyAuthWithOAuth2Fetcher(req *http.Request, item *RequestItem, vars map[string]string, oauth2Fetcher func(OAuth2Auth, map[string]string) (string, error)) error {
	auth := item.Auth
	switch auth.Mode {
	case "basic":
		req.SetBasicAuth(interpolate(auth.Username, vars), interpolate(auth.Password, vars))
	case "bearer":
		token := interpolate(auth.Token, vars)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "apikey":
		key := interpolate(auth.APIKey, vars)
		value := interpolate(auth.APIValue, vars)
		if key == "" {
			return nil
		}
		if auth.APILocation == "query" {
			q := req.URL.Query()
			q.Set(key, value)
			req.URL.RawQuery = q.Encode()
			return nil
		}
		req.Header.Set(key, value)
	case "oauth2":
		token := interpolate(auth.Token, vars)
		if token == "" && strings.TrimSpace(auth.OAuth2.GrantType) != "" {
			var err error
			token, err = oauth2Fetcher(auth.OAuth2, vars)
			if err != nil {
				return err
			}
		}
		if token != "" {
			applyOAuth2Token(req, auth.OAuth2, token, vars)
		}
	case "ntlm":
		username := interpolate(auth.Username, vars)
		if domain := interpolate(auth.Domain, vars); domain != "" && !strings.Contains(username, `\`) {
			username = domain + `\` + username
		}
		req.SetBasicAuth(username, interpolate(auth.Password, vars))
	case "awsv4":
		return awsv4.Sign(req, auth.AWSV4, time.Now().UTC(), func(value string) string { return interpolate(value, vars) })
	case "wsse":
		wsse.ApplyHeader(req.Header, interpolate(auth.Username, vars), interpolate(auth.Password, vars), time.Now().UTC())
	case "oauth1":
		return oauth1.Sign(req, item, auth.OAuth1, vars, time.Now().UTC())
	}
	return nil
}

func setRequestBodyString(req *http.Request, value string) {
	data := []byte(value)
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.ContentLength = int64(len(data))
}

func evaluateAssertions(assertions []Assertion, response Response) []Assertion {
	next := make([]Assertion, 0, len(assertions))
	for _, assertion := range assertions {
		if !assertion.Enabled {
			next = append(next, assertion)
			continue
		}
		actual := ""
		switch assertion.Expression {
		case "res.status":
			actual = strconv.Itoa(response.Status)
		case "res.body":
			actual = response.Body
		default:
			if strings.HasPrefix(assertion.Expression, "res.headers.") {
				actual = response.Headers[strings.TrimPrefix(assertion.Expression, "res.headers.")]
			}
		}
		assertion.Passed = compareAssertion(actual, assertion.Operator, assertion.Value)
		if assertion.Passed {
			assertion.Message = "passed"
		} else {
			assertion.Message = fmt.Sprintf("expected %q %s %q", actual, assertion.Operator, assertion.Value)
		}
		next = append(next, assertion)
	}
	return next
}

// Wrapped rather than renamed at 138 call sites in app.go alone.
func interpolate(input string, vars map[string]string) string {
	return interp.Interpolate(input, vars)
}

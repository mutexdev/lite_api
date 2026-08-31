package core

// Turning a stored request into an outgoing one: body, auth and assertions.
//
// Split out of app.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/mutexdev/lite_api/internal/mcpserver"
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
		return strings.NewReader(graphQLRequestPayload(body, vars)), "application/json", nil
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

// applyAuth attaches the request's credentials, fetching an OAuth2 token and
// resolving AWS credentials over the network when the mode calls for it.
//
// IT TAKES THE SEND'S CONTEXT because two of those modes reach the network, and
// under MCP provenance both are checkpoints (§4.3 item 2). Passing the context
// rather than reading req.Context() keeps provenance explicit at the seam
// (§4.5) and matches the ctx-aware OAuth2 chain the token checkpoint lives in.
func (a *App) applyAuth(ctx context.Context, req *http.Request, collectionPath string, item *RequestItem, vars map[string]string, recordTimeline func(TimelineItem)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return applyAuthWithOAuth2Fetcher(ctx, req, item, vars, func(auth OAuth2Auth, vars map[string]string) (string, error) {
		token, timelineEntries, err := a.fetchOAuth2TokenWithTimelineContext(ctx, auth, vars)
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

// applyAuth is the cache-less, App-less sibling. Same context contract: the
// package-level OAuth2 fetcher is the ctx-aware one, so a caller that has
// provenance keeps it and a caller that does not passes context.Background()
// and gets exactly today's behaviour.
func applyAuth(ctx context.Context, req *http.Request, item *RequestItem, vars map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return applyAuthWithOAuth2Fetcher(ctx, req, item, vars, func(auth OAuth2Auth, vars map[string]string) (string, error) {
		return fetchOAuth2TokenWithContext(ctx, auth, vars)
	})
}

func applyAuthWithOAuth2Fetcher(ctx context.Context, req *http.Request, item *RequestItem, vars map[string]string, oauth2Fetcher func(OAuth2Auth, map[string]string) (string, error)) error {
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
		return mcpClassifyAWSSigningRefusal(ctx, awsv4.Sign(mcpAWSSigningRequest(ctx, req), auth.AWSV4, time.Now().UTC(), func(value string) string { return interpolate(value, vars) }))
	case "wsse":
		wsse.ApplyHeader(req.Header, interpolate(auth.Username, vars), interpolate(auth.Password, vars), time.Now().UTC())
	case "oauth1":
		return oauth1.Sign(req, item, auth.OAuth1, vars, time.Now().UTC())
	}
	return nil
}

// mcpAWSSigningRequest hands awsv4.Sign the context its CREDENTIAL RESOLUTION
// must run under, without disturbing the context the request itself is sent
// with.
//
// Sign takes no ctx parameter by design — it reads req.Context(), because the
// request already carries the deadline and now the guard. But the outgoing
// request's own context is the main egress's, and narrowing that to kind `aws`
// would make the backstop authorize the user's API call as an AWS credential
// call. So the signing call gets a shallow copy (req.WithContext) carrying:
//
//   - egress kind `aws`, so the guard transport around the shared credential
//     client authorizes STS/SSO/OIDC traffic in the aws CLASS rather than
//     defaulting to `main` and denying an endpoint the in-package checkpoint
//     just allowed; and
//   - the EgressGuard itself, which is per-context and not process-wide
//     (awsv4.WithEgressGuard) precisely because two sends can be in flight
//     under different policies.
//
// The copy is safe to sign: WithContext copies the struct, so the Header map,
// the URL pointer and the body are the same objects Sign would have mutated on
// the original, and awsv4's payload hashing rewinds rather than replaces the
// body.
//
// With no policy on the context the original request is passed through
// unchanged, so a UI send signs byte-identically to before — including awsv4's
// long-standing fallback to literal keys when a profile fails to resolve, which
// Sign only suppresses when a guard is present.
func mcpAWSSigningRequest(ctx context.Context, req *http.Request) *http.Request {
	policy := mcpPolicyFromContext(ctx)
	if policy == nil || req == nil {
		return req
	}
	signingCtx := mcpContextWithEgressKind(ctx, egressKindAWS)
	signingCtx = awsv4.WithEgressGuard(signingCtx, mcpAWSEgressGuard(policy))
	return req.WithContext(signingCtx)
}

// mcpClassifyAWSSigningRefusal re-classes §2 row 2 on the core side.
//
// WHY IT CANNOT BE DONE WHERE IT IS DETECTED. internal/auth/awsv4 refuses
// credential_process at the only site that reaches it (profile.go), before the
// command line is even split, and it reports that with its own
// *CredentialProcessRefusedError. It cannot wrap mcpserver.ErrDenied and it
// cannot mark the policy: awsv4 imports neither package, deliberately — the
// guard it takes is an interface precisely so the auth package stays ignorant
// of what "authorized" means. So the refusal arrives here as a typed error with
// no class and no mark, and by the time executeHTTP has written
// `result.Error = err.Error()` there is nothing left for the audit layer to
// recognise. This is the seam where the guard closure was installed
// (mcpAWSSigningRequest, just above), which makes it the right place to give
// the refusal back its class.
//
// errors.As rather than a string match, for the reason mcpClassifyFlowDenial
// gives: matching on the wording breaks the first time the wording changes.
//
// UNDER A UI SEND NOTHING HAPPENS. awsv4 only refuses when a guard is on the
// context, and a UI send installs none, so this cannot fire — and if it somehow
// did, noteRefusal on a nil policy is a no-op and the error is returned as it
// arrived.
func mcpClassifyAWSSigningRefusal(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var refused *awsv4.CredentialProcessRefusedError
	if !errors.As(err, &refused) {
		return err
	}
	mcpPolicyFromContext(ctx).noteRefusal()
	if errors.Is(err, mcpserver.ErrDenied) {
		return err
	}
	// mcpDeniedRunError carries the class and leaves the message alone: awsv4
	// already wrote the §2 refusal sentence, verbatim as the doc's row 2 quotes
	// it, and prefixing "denied:" onto it would render the refusal twice.
	return mcpDeniedRunError{err: err}
}

// mcpAWSEgressGuard adapts the destination policy to awsv4's guard interface.
//
// The endpoint arrives as a FULL url — path and query included, since a
// GetRoleCredentials call carries the role in its query — and origin arithmetic
// is this side's job, which is the right split: awsv4 knows what it is about to
// call, and only internal/core knows what "authorized" means.
//
// It authorizes with the BLOCKING Authorize rather than the backstop's
// no-prompt form: credential resolution runs while the request is being built,
// long before client.Timeout starts, so there is room to ask the user.
func mcpAWSEgressGuard(policy *mcpEgressPolicy) awsv4.EgressGuard {
	return awsv4.EgressGuardFunc(func(ctx context.Context, endpointURL string) error {
		origin, ok := OriginOfURL(endpointURL)
		if !ok {
			return fmt.Errorf("%w: this run's AWS credential endpoint %q is not an http(s) destination LiteAPI can check; fix the AWS profile or run this request in the LiteAPI app",
				mcpserver.ErrDenied, endpointURL)
		}
		return policy.Authorize(ctx, origin, egressKindAWS)
	})
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

// graphQLRequestPayload encodes a GraphQL request body.
//
// Everything that needs one of these -- the body builder, the fallback in the
// executor, the Network Log -- goes through here. They used to build the
// payload themselves, and two of the three built `variables` as a Go string,
// which encodes as a JSON string holding escaped JSON rather than the object
// the spec calls for. The log copy diverging from the wire copy was the worse
// half: it meant the record of a request disagreed with the request.
func graphQLRequestPayload(body RequestBody, vars map[string]string) string {
	return codegen.GraphQLRequestBodySnapshot(body, vars)
}

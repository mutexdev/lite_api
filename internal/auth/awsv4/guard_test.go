package awsv4

// Tests for the egress guard and for CredentialEndpointOrigins.
//
// Two properties are load-bearing here and are asserted rather than assumed:
// the guard sees the exact URL that is about to be requested (so the caller's
// origin arithmetic is done on the real destination, not a reconstruction),
// and CredentialEndpointOrigins predicts exactly the set of origins the code
// then contacts. The second is checked by running each credential path twice
// -- once through the prediction, once through the network -- and comparing.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

// credentialCalls records both sides of every credential call: what the guard
// was shown, and what a server actually received. A path that contacts
// something the guard never saw shows up as a length mismatch.
type credentialCalls struct {
	mu       sync.Mutex
	guarded  []string
	served   []string
	refuseAt func(endpointURL string) error
}

func (c *credentialCalls) guard() EgressGuard {
	return EgressGuardFunc(func(_ context.Context, endpointURL string) error {
		c.mu.Lock()
		c.guarded = append(c.guarded, endpointURL)
		refuse := c.refuseAt
		c.mu.Unlock()
		if refuse != nil {
			return refuse(endpointURL)
		}
		return nil
	})
}

// record is what every test handler calls first. r.Host plus the request URI
// reconstructs what the client asked for, from the server's side of the wire.
func (c *credentialCalls) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.served = append(c.served, "http://"+r.Host+r.URL.RequestURI())
}

func (c *credentialCalls) snapshot() (guarded, served []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.guarded...), append([]string(nil), c.served...)
}

// canonicalCredentialURL puts the two sides of the recording into the same
// shape: an endpoint written without a path ("http://host:port") and the same
// endpoint as the server saw it ("http://host:port/") are one URL.
func canonicalCredentialURL(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	out := parsed.Scheme + "://" + parsed.Host + parsed.Path
	if parsed.RawQuery != "" {
		out += "?" + parsed.RawQuery
	}
	return out
}

func canonicalCredentialURLs(t *testing.T, raw []string) []string {
	t.Helper()
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		out = append(out, canonicalCredentialURL(t, value))
	}
	return out
}

func originsOf(raw []string) []string {
	var out []string
	for _, value := range raw {
		origin := credentialEndpointOrigin(value)
		if origin == "" {
			continue
		}
		seen := false
		for _, existing := range out {
			if existing == origin {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, origin)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const assumeRoleResponseXML = `<AssumeRoleResponse>
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>GUARDEDAKID</AccessKeyId>
      <SecretAccessKey>GUARDEDSECRET</SecretAccessKey>
      <SessionToken>guarded-session</SessionToken>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`

const webIdentityResponseXML = `<AssumeRoleWithWebIdentityResponse>
  <AssumeRoleWithWebIdentityResult>
    <Credentials>
      <AccessKeyId>WEBIDAKID</AccessKeyId>
      <SecretAccessKey>WEBIDSECRET</SecretAccessKey>
      <SessionToken>webid-session</SessionToken>
    </Credentials>
  </AssumeRoleWithWebIdentityResult>
</AssumeRoleWithWebIdentityResponse>`

const ssoRoleCredentialsJSON = `{"roleCredentials":{"accessKeyId":"SSOAKID","secretAccessKey":"SSOSECRET","sessionToken":"sso-session","expiration":1893456000000}}`

// credentialScenario is one of the four credential paths that leave the
// machine, set up end to end: files on disk, servers listening, and the exact
// URLs each is expected to be asked for.
type credentialScenario struct {
	name        string
	profileName string
	// build writes the fixture and returns the URLs the path should request,
	// in order.
	build func(t *testing.T, calls *credentialCalls) []string
}

func credentialScenarios() []credentialScenario {
	return []credentialScenario{
		{
			name:        "sts assume role through a source profile",
			profileName: "roleuser",
			build: func(t *testing.T, calls *credentialCalls) []string {
				sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.record(r)
					w.Header().Set("Content-Type", "text/xml")
					_, _ = w.Write([]byte(assumeRoleResponseXML))
				}))
				t.Cleanup(sts.Close)
				dir := t.TempDir()
				writeAWSFixture(t, dir, `[profile roleuser]
role_arn = arn:aws:iam::123456789012:role/Guarded
source_profile = guardsource
region = us-west-2
sts_endpoint_url = `+sts.URL+`
`, `[guardsource]
aws_access_key_id = SOURCEAKID
aws_secret_access_key = SOURCESECRET
`)
				return []string{sts.URL}
			},
		},
		{
			name:        "sts assume role with web identity",
			profileName: "webid",
			build: func(t *testing.T, calls *credentialCalls) []string {
				sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.record(r)
					w.Header().Set("Content-Type", "text/xml")
					_, _ = w.Write([]byte(webIdentityResponseXML))
				}))
				t.Cleanup(sts.Close)
				dir := t.TempDir()
				tokenPath := filepath.Join(dir, "web-identity-token")
				if err := os.WriteFile(tokenPath, []byte("web-identity-token-value\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				writeAWSFixture(t, dir, `[profile webid]
role_arn = arn:aws:iam::123456789012:role/WebIdentity
web_identity_token_file = `+tokenPath+`
region = eu-west-1
sts_endpoint_url = `+sts.URL+`
`, "")
				return []string{sts.URL}
			},
		},
		{
			name:        "sso get role credentials",
			profileName: "ssolegacy",
			build: func(t *testing.T, calls *credentialCalls) []string {
				sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.record(r)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(ssoRoleCredentialsJSON))
				}))
				t.Cleanup(sso.Close)
				const startURL = "https://legacy.awsapps.com/start"
				dir := t.TempDir()
				cacheDir := writeSSOCache(t, startURL, `{"startUrl":"`+startURL+`","region":"us-east-1","accessToken":"legacy-token","expiresAt":"2030-01-02T03:04:05UTC"}`)
				writeAWSFixture(t, dir, `[profile ssolegacy]
sso_start_url = `+startURL+`
sso_region = us-east-1
sso_account_id = 111122223333
sso_role_name = ReadOnly
sso_endpoint_url = `+sso.URL+`
`, "")
				t.Setenv("AWS_SSO_CACHE_DIR", cacheDir)
				return []string{sso.URL + "/federation/credentials?account_id=111122223333&role_name=ReadOnly"}
			},
		},
		{
			name:        "sso oidc refresh then get role credentials",
			profileName: "ssorefreshguard",
			build: func(t *testing.T, calls *credentialCalls) []string {
				oidc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.record(r)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"accessToken":"fresh-token","expiresIn":3600,"refreshToken":"next-refresh","tokenType":"Bearer"}`))
				}))
				t.Cleanup(oidc.Close)
				sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.record(r)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(ssoRoleCredentialsJSON))
				}))
				t.Cleanup(sso.Close)
				const (
					sessionName = "guarded-session-name"
					startURL    = "https://refreshguard.awsapps.com/start"
				)
				expired := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
				cacheDir := writeSSOCache(t, sessionName, `{"startUrl":"`+startURL+`","region":"us-east-2","accessToken":"stale-token","expiresAt":"`+expired+`","refreshToken":"old-refresh","clientId":"cid","clientSecret":"csecret"}`)
				dir := t.TempDir()
				writeAWSFixture(t, dir, `[profile ssorefreshguard]
sso_session = `+sessionName+`
sso_account_id = 444455556666
sso_role_name = Admin
sso_endpoint_url = `+sso.URL+`
sso_oidc_endpoint_url = `+oidc.URL+`

[sso-session `+sessionName+`]
sso_start_url = `+startURL+`
sso_region = us-east-2
`, "")
				t.Setenv("AWS_SSO_CACHE_DIR", cacheDir)
				return []string{
					oidc.URL + "/token",
					sso.URL + "/federation/credentials?account_id=444455556666&role_name=Admin",
				}
			},
		},
	}
}

// writeAWSFixture points the shared-config environment at freshly written
// files, so nothing on the developer's machine takes part.
func writeAWSFixture(t *testing.T, dir, config, credentials string) {
	t.Helper()
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(dir, "missing-credentials")
	if credentials != "" {
		credentialsPath = filepath.Join(dir, "credentials")
		if err := os.WriteFile(credentialsPath, []byte(credentials), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
}

func writeSSOCache(t *testing.T, cacheKey, payload string) string {
	t.Helper()
	cacheDir := filepath.Join(t.TempDir(), "sso-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, awsV4SSOCacheFilename(cacheKey)), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return cacheDir
}

// TestMCPAWSCredentialResolutionGated is the guard's contract: every one of the
// four credential paths that leaves the machine asks first, and asks with the
// exact URL it is about to request.
func TestMCPAWSCredentialResolutionGated(t *testing.T) {
	for _, scenario := range credentialScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			calls := &credentialCalls{}
			wantURLs := scenario.build(t, calls)

			ctx := WithEgressGuard(t.Context(), calls.guard())
			if _, err := loadAWSV4ProfileCredentials(ctx, scenario.profileName); err != nil {
				t.Fatalf("resolution under an allowing guard failed: %v", err)
			}

			guarded, served := calls.snapshot()
			want := canonicalCredentialURLs(t, wantURLs)
			if got := canonicalCredentialURLs(t, guarded); !equalStrings(got, want) {
				t.Errorf("guard saw %q, want %q", got, want)
			}
			if got := canonicalCredentialURLs(t, served); !equalStrings(got, want) {
				t.Errorf("servers received %q, want %q; an unguarded call would show up here", got, want)
			}
		})
	}
}

// A refusal has to land before the request, not after it: the point of the
// guard is that the bytes never leave.
func TestMCPAWSCredentialRefusalStopsTheRequest(t *testing.T) {
	for _, scenario := range credentialScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			refusal := errors.New("destination not allowed for this run")
			calls := &credentialCalls{refuseAt: func(string) error { return refusal }}
			wantURLs := scenario.build(t, calls)

			ctx := WithEgressGuard(t.Context(), calls.guard())
			_, err := loadAWSV4ProfileCredentials(ctx, scenario.profileName)
			if !errors.Is(err, refusal) {
				t.Fatalf("got %v, want the guard's refusal", err)
			}

			guarded, served := calls.snapshot()
			if len(served) != 0 {
				t.Errorf("a refused run still reached %q", served)
			}
			// The first endpoint is refused, so nothing after it is even asked
			// about.
			if len(guarded) != 1 {
				t.Fatalf("guard consulted %d times, want once before the first call", len(guarded))
			}
			if got, want := canonicalCredentialURL(t, guarded[0]), canonicalCredentialURL(t, wantURLs[0]); got != want {
				t.Errorf("guard saw %q, want %q", got, want)
			}
		})
	}
}

// TestCredentialEndpointOriginsMatchesWhatIsContacted is the parity the caller
// depends on: what the package says it would contact is what it contacts.
func TestCredentialEndpointOriginsMatchesWhatIsContacted(t *testing.T) {
	for _, scenario := range credentialScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			calls := &credentialCalls{}
			scenario.build(t, calls)

			auth := types.AWSV4Auth{ProfileName: "{{profile}}"}
			resolve := func(value string) string {
				if value == "{{profile}}" {
					return scenario.profileName
				}
				return value
			}
			predicted := CredentialEndpointOrigins(auth, resolve)

			ctx := WithEgressGuard(t.Context(), calls.guard())
			if _, err := loadAWSV4ProfileCredentials(ctx, scenario.profileName); err != nil {
				t.Fatalf("resolution failed: %v", err)
			}
			guarded, served := calls.snapshot()

			if contacted := originsOf(guarded); !equalStrings(predicted, contacted) {
				t.Errorf("CredentialEndpointOrigins = %q, but the guard was asked about %q", predicted, contacted)
			}
			if contacted := originsOf(served); !equalStrings(predicted, contacted) {
				t.Errorf("CredentialEndpointOrigins = %q, but the servers were contacted at %q", predicted, contacted)
			}
		})
	}
}

// Configurations that reach nothing must predict nothing, or every static-key
// request would carry a phantom AWS origin into the caller's allowlist.
func TestCredentialEndpointOriginsIsEmptyForConfigurationsThatContactNothing(t *testing.T) {
	dir := t.TempDir()
	writeAWSFixture(t, dir, `[profile staticprofile]
aws_access_key_id = STATICAKID
aws_secret_access_key = STATICSECRET

[profile processprofile]
credential_process = /bin/echo hello

[profile envprofile]
role_arn = arn:aws:iam::123456789012:role/EnvSourced
`, "")

	for name, profileName := range map[string]string{
		"no profile at all":  "",
		"static keys":        "staticprofile",
		"credential_process": "processprofile",
	} {
		t.Run(name, func(t *testing.T) {
			got := CredentialEndpointOrigins(types.AWSV4Auth{ProfileName: profileName}, nil)
			if len(got) != 0 {
				t.Errorf("got %q, want nothing", got)
			}
		})
	}

	// A role_arn does reach STS even with no source profile to get there --
	// the call is attempted and fails, so the origin is real.
	if got := CredentialEndpointOrigins(types.AWSV4Auth{ProfileName: "envprofile"}, nil); len(got) != 1 {
		t.Errorf("role_arn profile: got %q, want the STS origin", got)
	}
}

// A source_profile chain contacts the far end of the chain first. The
// prediction has to be in that order and has to include both.
func TestCredentialEndpointOriginsFollowsSourceProfileChains(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(assumeRoleResponseXML))
	}))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(assumeRoleResponseXML))
	}))
	defer outer.Close()

	dir := t.TempDir()
	writeAWSFixture(t, dir, `[profile outerrole]
role_arn = arn:aws:iam::123456789012:role/Outer
source_profile = innerrole
sts_endpoint_url = `+outer.URL+`

[profile innerrole]
role_arn = arn:aws:iam::123456789012:role/Inner
source_profile = chainbase
sts_endpoint_url = `+inner.URL+`
`, `[chainbase]
aws_access_key_id = CHAINAKID
aws_secret_access_key = CHAINSECRET
`)

	got := CredentialEndpointOrigins(types.AWSV4Auth{ProfileName: "outerrole"}, nil)
	want := []string{credentialEndpointOrigin(inner.URL), credentialEndpointOrigin(outer.URL)}
	if !equalStrings(got, want) {
		t.Fatalf("got %q, want %q (innermost first)", got, want)
	}

	calls := &credentialCalls{}
	ctx := WithEgressGuard(t.Context(), calls.guard())
	if _, err := loadAWSV4ProfileCredentials(ctx, "outerrole"); err != nil {
		t.Fatal(err)
	}
	guarded, _ := calls.snapshot()
	if contacted := originsOf(guarded); !equalStrings(got, contacted) {
		t.Fatalf("prediction %q, contacted %q", got, contacted)
	}
}

// A circular chain must not send the prediction into an infinite recursion --
// resolution refuses such a chain, and the prediction has to reach the same
// end without hanging.
func TestCredentialEndpointOriginsTerminatesOnCircularChains(t *testing.T) {
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sts.Close()
	dir := t.TempDir()
	writeAWSFixture(t, dir, `[profile loopa]
role_arn = arn:aws:iam::123456789012:role/A
source_profile = loopb
sts_endpoint_url = `+sts.URL+`

[profile loopb]
role_arn = arn:aws:iam::123456789012:role/B
source_profile = loopa
sts_endpoint_url = `+sts.URL+`
`, "")

	done := make(chan []string, 1)
	go func() { done <- CredentialEndpointOrigins(types.AWSV4Auth{ProfileName: "loopa"}, nil) }()
	select {
	case got := <-done:
		if want := []string{credentialEndpointOrigin(sts.URL)}; !equalStrings(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CredentialEndpointOrigins did not terminate on a circular source_profile chain")
	}
}

// writeCredentialProcessFixture builds a credential_process helper that leaves
// a marker file behind. The marker is the whole point: a refusal that still
// spawned the program would be no refusal at all, and an error message alone
// cannot tell the two apart.
func writeCredentialProcessFixture(t *testing.T, extraConfig string) (markerPath string) {
	t.Helper()
	dir := t.TempDir()
	markerPath = filepath.Join(dir, "spawned.marker")
	scriptPath := filepath.Join(dir, "credential-helper.sh")
	script := "#!/bin/sh\n" +
		"printf 'spawned' > " + shellQuote(markerPath) + "\n" +
		`printf '{"Version":1,"AccessKeyId":"PROCESSAKID","SecretAccessKey":"PROCESSSECRET","SessionToken":"PROCESSTOKEN"}'` + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	writeAWSFixture(t, dir, `[profile processdev]
credential_process = "`+scriptPath+`"
`+extraConfig, "")
	return markerPath
}

// shellQuote single-quotes a path for /bin/sh. Temp paths have no quotes in
// them, but writing the shell escape out beats assuming it.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// TestMCPCredentialProcessRefused: under a guard, credential_process is
// refused, and refused everywhere the resolver can reach it -- selected
// directly, or several source_profile links away.
func TestMCPCredentialProcessRefused(t *testing.T) {
	cases := []struct {
		name string
		// extraConfig takes the STS endpoint, so a chain that somehow got past
		// the refusal would hit the test server and be caught, rather than
		// reaching real AWS.
		extraConfig string
		profileName string
	}{
		{
			name:        "profile selected directly",
			profileName: "processdev",
		},
		{
			name:        "reached through one source_profile link",
			profileName: "chainone",
			extraConfig: `
[profile chainone]
role_arn = arn:aws:iam::123456789012:role/ChainOne
source_profile = processdev
sts_endpoint_url = %[1]s
`,
		},
		{
			name:        "reached through two source_profile links",
			profileName: "chaintop",
			extraConfig: `
[profile chaintop]
role_arn = arn:aws:iam::123456789012:role/ChainTop
source_profile = chainmid
sts_endpoint_url = %[1]s

[profile chainmid]
role_arn = arn:aws:iam::123456789012:role/ChainMid
source_profile = processdev
sts_endpoint_url = %[1]s
`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			calls := &credentialCalls{}
			sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.record(r)
				w.Header().Set("Content-Type", "text/xml")
				_, _ = w.Write([]byte(assumeRoleResponseXML))
			}))
			defer sts.Close()
			extraConfig := ""
			if testCase.extraConfig != "" {
				extraConfig = fmt.Sprintf(testCase.extraConfig, sts.URL)
			}
			markerPath := writeCredentialProcessFixture(t, extraConfig)

			ctx := WithEgressGuard(t.Context(), calls.guard())
			_, err := loadAWSV4ProfileCredentials(ctx, testCase.profileName)

			var refused *CredentialProcessRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("got %v, want a CredentialProcessRefusedError", err)
			}
			if refused.Profile != "processdev" {
				t.Errorf("refusal names %q, want the profile that actually carries credential_process", refused.Profile)
			}
			const want = `AWS profile "processdev" uses credential_process, which runs an external program. ` +
				`Agent-initiated runs cannot use it. Run this request in the LiteAPI app, ` +
				`or switch the profile to static keys or SSO.`
			if err.Error() != want {
				t.Errorf("message:\n got %q\nwant %q", err.Error(), want)
			}
			if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("the credential_process program ran: %v", statErr)
			}
			if _, served := calls.snapshot(); len(served) != 0 {
				t.Errorf("the refused chain still contacted %q", served)
			}
		})
	}
}

// The control for the test above, and the guarantee for the app: with no
// guard, credential_process behaves exactly as it always has.
func TestCredentialProcessRunsWithoutAGuard(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no guard in the context": context.Background(),
		"guard explicitly nil":    WithEgressGuard(context.Background(), nil),
	} {
		t.Run(name, func(t *testing.T) {
			markerPath := writeCredentialProcessFixture(t, "")
			credentials, err := loadAWSV4ProfileCredentials(ctx, "processdev")
			if err != nil {
				t.Fatal(err)
			}
			if credentials.AccessKeyID != "PROCESSAKID" || credentials.SecretAccessKey != "PROCESSSECRET" {
				t.Fatalf("unexpected credentials: %#v", credentials)
			}
			if _, statErr := os.Stat(markerPath); statErr != nil {
				t.Fatalf("the credential_process program did not run: %v", statErr)
			}
		})
	}
}

// Without a guard nothing is consulted and nothing is refused: the four
// network paths run exactly as they did before the guard existed.
func TestNilGuardLeavesEveryCredentialPathAlone(t *testing.T) {
	for _, scenario := range credentialScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			calls := &credentialCalls{}
			wantURLs := scenario.build(t, calls)

			credentials, err := loadAWSV4ProfileCredentials(context.Background(), scenario.profileName)
			if err != nil {
				t.Fatalf("unguarded resolution failed: %v", err)
			}
			if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
				t.Fatalf("unguarded resolution returned nothing usable: %#v", credentials)
			}
			guarded, served := calls.snapshot()
			if len(guarded) != 0 {
				t.Errorf("a guardless context consulted a guard: %q", guarded)
			}
			if got, want := canonicalCredentialURLs(t, served), canonicalCredentialURLs(t, wantURLs); !equalStrings(got, want) {
				t.Errorf("servers received %q, want %q", got, want)
			}
		})
	}
}

// Sign takes no context parameter, so the only way a deadline or a guard can
// reach STS is through the request it was handed. This proves it does: the
// shared credential client's own timeout is 30s, so a call that gives up in
// well under a second can only have been governed by the request's context.
func TestSignRunsCredentialResolutionUnderTheRequestContext(t *testing.T) {
	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The body has to be drained before the server will watch the
		// connection for the client going away, which is how r.Context() gets
		// cancelled -- without this the handler never wakes and Close hangs.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case reached <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer sts.Close()
	defer close(release)

	dir := t.TempDir()
	writeAWSFixture(t, dir, `[profile slowsts]
role_arn = arn:aws:iam::123456789012:role/Slow
source_profile = slowsource
region = us-west-2
sts_endpoint_url = `+sts.URL+`
`, `[slowsource]
aws_access_key_id = SLOWAKID
aws_secret_access_key = SLOWSECRET
`)

	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/thing", nil)
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	err = Sign(req, types.AWSV4Auth{ProfileName: "slowsts", Service: "s3", Region: "us-west-2"}, time.Now().UTC(), func(value string) string { return value })
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("expected the STS call to be cut short by the request context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want a deadline from the request context", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("took %s; the request context did not govern the STS call", elapsed)
	}
	select {
	case <-reached:
	default:
		t.Fatal("STS was never contacted, so this proved nothing about the context")
	}
}

// The guard reaches STS the same way the deadline does, and it reaches it even
// when Sign was also handed literal keys. Falling back to those on a refusal
// would turn "this run may not talk to that host" into "this run signed with
// something else instead".
func TestSignDoesNotFallBackToLiteralKeysWhenTheGuardRefuses(t *testing.T) {
	calls := &credentialCalls{refuseAt: func(string) error { return errors.New("nope") }}
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.record(r)
	}))
	defer sts.Close()
	dir := t.TempDir()
	writeAWSFixture(t, dir, `[profile refusedsts]
role_arn = arn:aws:iam::123456789012:role/Refused
source_profile = refusedsource
region = us-west-2
sts_endpoint_url = `+sts.URL+`
`, `[refusedsource]
aws_access_key_id = REFUSEDAKID
aws_secret_access_key = REFUSEDSECRET
`)

	auth := types.AWSV4Auth{
		AccessKeyID:     "LITERALAKID",
		SecretAccessKey: "LITERALSECRET",
		ProfileName:     "refusedsts",
		Service:         "s3",
		Region:          "us-west-2",
	}
	identity := func(value string) string { return value }

	guarded, err := http.NewRequestWithContext(WithEgressGuard(t.Context(), calls.guard()), http.MethodGet, "https://example.com/thing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Sign(guarded, auth, time.Now().UTC(), identity); err == nil {
		t.Fatal("a refused profile must not sign with the literal keys instead")
	}
	if guarded.Header.Get("Authorization") != "" {
		t.Error("a refused request was signed anyway")
	}
	if _, served := calls.snapshot(); len(served) != 0 {
		t.Errorf("the refused resolution still reached %q", served)
	}

	// Unguarded, the long-standing fallback stands: the profile fails, the
	// literal keys sign.
	fallback, err := http.NewRequest(http.MethodGet, "https://example.com/thing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Sign(fallback, auth, time.Now().UTC(), identity); err != nil {
		t.Fatalf("the UI path must still fall back to the literal keys: %v", err)
	}
	if got := fallback.Header.Get("Authorization"); !strings.Contains(got, "Credential=LITERALAKID/") {
		t.Errorf("unguarded fallback signed with %q", got)
	}
}

func TestCredentialEndpointOriginReducesToSchemeAndHost(t *testing.T) {
	for input, want := range map[string]string{
		"https://sts.us-east-1.amazonaws.com":            "https://sts.us-east-1.amazonaws.com",
		"  https://sts.us-east-1.amazonaws.com/  ":       "https://sts.us-east-1.amazonaws.com",
		"http://127.0.0.1:8081/federation/creds?a=b":     "http://127.0.0.1:8081",
		"https://portal.sso.eu-west-1.amazonaws.com:443": "https://portal.sso.eu-west-1.amazonaws.com:443",
		"":          "",
		"not a url": "not a url",
	} {
		if got := credentialEndpointOrigin(input); got != want {
			t.Errorf("credentialEndpointOrigin(%q) = %q, want %q", input, got, want)
		}
	}
}

// A guard that is present but happy must leave the resolved credentials
// untouched -- authorization is a veto, not a transformation.
func TestAllowingGuardReturnsTheSameCredentialsAsNoGuard(t *testing.T) {
	calls := &credentialCalls{}
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.record(r)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(assumeRoleResponseXML))
	}))
	defer sts.Close()
	dir := t.TempDir()
	writeAWSFixture(t, dir, fmt.Sprintf(`[profile allowed]
role_arn = arn:aws:iam::123456789012:role/Allowed
source_profile = allowedsource
sts_endpoint_url = %s
`, sts.URL), `[allowedsource]
aws_access_key_id = ALLOWEDAKID
aws_secret_access_key = ALLOWEDSECRET
`)

	plain, err := loadAWSV4ProfileCredentials(context.Background(), "allowed")
	if err != nil {
		t.Fatal(err)
	}
	guardedResult, err := loadAWSV4ProfileCredentials(WithEgressGuard(t.Context(), calls.guard()), "allowed")
	if err != nil {
		t.Fatal(err)
	}
	if plain != guardedResult {
		t.Fatalf("guarded %#v, unguarded %#v", guardedResult, plain)
	}
	if _, served := calls.snapshot(); len(served) != 2 {
		t.Fatalf("STS served %d calls, want one per resolution", len(served))
	}
}

// US-067. These moved out of app_test.go with the code they cover, and stay in
// package awsv4 rather than awsv4_test because what they assert on -- profile
// resolution, SSO token caching, credential_process -- is unexported.
package awsv4

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAWSV4ProfileCredentialsCanLoadFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(`[profile configdev]
aws_access_key_id = CONFIGAKID
aws_secret_access_key = CONFIGSECRET
aws_session_token = CONFIGTOKEN
`), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials, err := loadAWSV4ProfileCredentials("configdev")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "CONFIGAKID" || credentials.SecretAccessKey != "CONFIGSECRET" || credentials.SessionToken != "CONFIGTOKEN" {
		t.Fatalf("unexpected AWS profile credentials: %#v", credentials)
	}
}

func TestAWSV4ProfileCredentialsCanLoadFromCredentialProcess(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "credential helper.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '{"Version":1,"AccessKeyId":"PROCESSAKID","SecretAccessKey":"PROCESSSECRET","SessionToken":"PROCESSTOKEN"}'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(`[profile processdev]
credential_process = "`+scriptPath+`" --ignored
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", configPath)

	credentials, err := loadAWSV4ProfileCredentials("processdev")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "PROCESSAKID" || credentials.SecretAccessKey != "PROCESSSECRET" || credentials.SessionToken != "PROCESSTOKEN" {
		t.Fatalf("unexpected AWS credential_process credentials: %#v", credentials)
	}
}

func TestAWSV4ProfileCredentialsRequireMFATokenCode(t *testing.T) {
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials")
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(credentialsPath, []byte(`[source]
aws_access_key_id = SOURCEAKID
aws_secret_access_key = SOURCESECRET
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`[profile mfarequired]
role_arn = arn:aws:iam::123456789012:role/MFARequired
source_profile = source
mfa_serial = arn:aws:iam::123456789012:mfa/alice
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_STS_ENDPOINT_URL", "http://127.0.0.1:1")
	t.Setenv("AWS_MFA_TOKEN_CODE", "")
	t.Setenv("AWS_MFA_TOKEN", "")
	t.Setenv("AWS_MFA_TOKEN_CODE_MFAREQUIRED", "")
	t.Setenv("AWS_MFA_TOKEN_MFAREQUIRED", "")
	t.Setenv("AWS_MFA_CODE", "")
	t.Setenv("AWS_TOKEN_CODE", "")

	_, err := loadAWSV4ProfileCredentials("mfarequired")
	if err == nil || !strings.Contains(err.Error(), "requires MFA token code") {
		t.Fatalf("expected missing MFA token-code error, got %v", err)
	}
}

func TestAWSV4ProfileCredentialsCanAssumeRoleFromEnvironmentSource(t *testing.T) {
	const (
		envAccessKeyID     = "ENVAKID"
		envSecretAccessKey = "ENVSECRET"
		envSessionToken    = "env-session"
		assumedAccessKeyID = "ENVROLEAKID"
		assumedSecretKey   = "ENVROLESECRET"
		roleARN            = "arn:aws:iam::123456789012:role/EnvRole"
	)
	stsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Amz-Security-Token"); got != envSessionToken {
			t.Fatalf("STS AssumeRole should use environment session token: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("RoleArn"); got != roleARN {
			t.Fatalf("bad environment-source RoleArn: %q", got)
		}
		assertAWSV4Signature(t, r, envAccessKeyID, envSecretAccessKey, "us-west-2", "sts")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<AssumeRoleResponse>
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>` + assumedAccessKeyID + `</AccessKeyId>
      <SecretAccessKey>` + assumedSecretKey + `</SecretAccessKey>
      <SessionToken>env-role-session</SessionToken>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`))
	}))
	defer stsServer.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(`[profile envrole]
role_arn = `+roleARN+`
credential_source = Environment
role_session_name = env-role
region = us-west-2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_ACCESS_KEY_ID", envAccessKeyID)
	t.Setenv("AWS_SECRET_ACCESS_KEY", envSecretAccessKey)
	t.Setenv("AWS_SESSION_TOKEN", envSessionToken)
	t.Setenv("AWS_STS_ENDPOINT_URL", stsServer.URL)

	credentials, err := loadAWSV4ProfileCredentials("envrole")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != assumedAccessKeyID || credentials.SecretAccessKey != assumedSecretKey || credentials.SessionToken != "env-role-session" {
		t.Fatalf("unexpected environment-source assumed credentials: %#v", credentials)
	}
}

func TestAWSV4ProfileCredentialsCanLoadSSOSessionCredentials(t *testing.T) {
	const (
		sessionName     = "corp-sso"
		startURL        = "https://corp.awsapps.com/start"
		ssoAccessToken  = "session-sso-access-token"
		ssoAccessKeyID  = "SESSIONSSOAKID"
		ssoSecretKey    = "SESSIONSSOSECRET"
		ssoSessionToken = "session-sso-token"
		accountID       = "210987654321"
		roleName        = "PowerUser"
		region          = "us-east-1"
	)
	var ssoCalls int32
	ssoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ssoCalls, 1)
		if got := r.URL.Query().Get("account_id"); got != accountID {
			t.Fatalf("bad sso-session account_id: %q", got)
		}
		if got := r.URL.Query().Get("role_name"); got != roleName {
			t.Fatalf("bad sso-session role_name: %q", got)
		}
		if got := r.Header.Get("x-amz-sso_bearer_token"); got != ssoAccessToken {
			t.Fatalf("bad sso-session bearer token: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"roleCredentials":{"accessKeyId":"` + ssoAccessKeyID + `","secretAccessKey":"` + ssoSecretKey + `","sessionToken":"` + ssoSessionToken + `","expiration":1893456000000}}`))
	}))
	defer ssoServer.Close()

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, awsV4SSOCacheFilename(sessionName)), []byte(`{"startUrl":"`+startURL+`","region":"`+region+`","accessToken":"`+ssoAccessToken+`","expiresAt":"2030-01-02T03:04:05UTC"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(`[profile ssosession]
sso_session = `+sessionName+`
sso_account_id = `+accountID+`
sso_role_name = `+roleName+`
region = `+region+`

[sso-session `+sessionName+`]
sso_start_url = `+startURL+`
sso_region = `+region+`
sso_registration_scopes = sso:account:access
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SSO_CACHE_DIR", cacheDir)
	t.Setenv("AWS_SSO_ENDPOINT_URL", ssoServer.URL)

	credentials, err := loadAWSV4ProfileCredentials("ssosession")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != ssoAccessKeyID || credentials.SecretAccessKey != ssoSecretKey || credentials.SessionToken != ssoSessionToken {
		t.Fatalf("unexpected sso-session credentials: %#v", credentials)
	}
	if got := atomic.LoadInt32(&ssoCalls); got != 1 {
		t.Fatalf("expected one sso-session GetRoleCredentials call, got %d", got)
	}
}

func TestAWSV4ProfileCredentialsRefreshesSSOSessionToken(t *testing.T) {
	const (
		sessionName         = "corp-refresh-sso"
		startURL            = "https://refresh.awsapps.com/start"
		oldAccessToken      = "old-sso-access-token"
		newAccessToken      = "new-sso-access-token"
		oldRefreshToken     = "old-refresh-token"
		newRefreshToken     = "new-refresh-token"
		clientID            = "sso-client-id"
		clientSecret        = "sso-client-secret"
		ssoAccessKeyID      = "REFRESHSSOAKID"
		ssoSecretKey        = "REFRESHSSOSECRET"
		ssoSessionToken     = "refresh-sso-token"
		accountID           = "345678901234"
		roleName            = "ReadOnly"
		region              = "us-east-2"
		registrationExpires = "2031-01-02T03:04:05Z"
	)
	var oidcCalls int32
	oidcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&oidcCalls, 1)
		if r.Method != http.MethodPost {
			t.Fatalf("bad SSO OIDC method: %s", r.Method)
		}
		if r.URL.Path != "/token" {
			t.Fatalf("bad SSO OIDC token path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("bad SSO OIDC content type: %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		expected := map[string]string{
			"clientId":     clientID,
			"clientSecret": clientSecret,
			"grantType":    "refresh_token",
			"refreshToken": oldRefreshToken,
		}
		for key, value := range expected {
			if got := body[key]; got != value {
				t.Fatalf("bad SSO OIDC %s: %q", key, got)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"` + newAccessToken + `","expiresIn":3600,"refreshToken":"` + newRefreshToken + `","tokenType":"Bearer"}`))
	}))
	defer oidcServer.Close()

	var ssoCalls int32
	ssoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ssoCalls, 1)
		if got := r.URL.Query().Get("account_id"); got != accountID {
			t.Fatalf("bad refreshed sso-session account_id: %q", got)
		}
		if got := r.URL.Query().Get("role_name"); got != roleName {
			t.Fatalf("bad refreshed sso-session role_name: %q", got)
		}
		if got := r.Header.Get("x-amz-sso_bearer_token"); got != newAccessToken {
			t.Fatalf("bad refreshed sso-session bearer token: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"roleCredentials":{"accessKeyId":"` + ssoAccessKeyID + `","secretAccessKey":"` + ssoSecretKey + `","sessionToken":"` + ssoSessionToken + `","expiration":1893456000000}}`))
	}))
	defer ssoServer.Close()

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, awsV4SSOCacheFilename(sessionName))
	expiredAt := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(cachePath, []byte(`{"startUrl":"`+startURL+`","region":"`+region+`","accessToken":"`+oldAccessToken+`","expiresAt":"`+expiredAt+`","refreshToken":"`+oldRefreshToken+`","clientId":"`+clientID+`","clientSecret":"`+clientSecret+`","registrationExpiresAt":"`+registrationExpires+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(`[profile ssorefresh]
sso_session = `+sessionName+`
sso_account_id = `+accountID+`
sso_role_name = `+roleName+`
region = `+region+`

[sso-session `+sessionName+`]
sso_start_url = `+startURL+`
sso_region = `+region+`
sso_registration_scopes = sso:account:access
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SSO_CACHE_DIR", cacheDir)
	t.Setenv("AWS_SSO_ENDPOINT_URL", ssoServer.URL)
	t.Setenv("AWS_SSO_OIDC_ENDPOINT_URL", oidcServer.URL)

	credentials, err := loadAWSV4ProfileCredentials("ssorefresh")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != ssoAccessKeyID || credentials.SecretAccessKey != ssoSecretKey || credentials.SessionToken != ssoSessionToken {
		t.Fatalf("unexpected refreshed sso-session credentials: %#v", credentials)
	}
	if got := atomic.LoadInt32(&oidcCalls); got != 1 {
		t.Fatalf("expected one SSO OIDC token refresh call, got %d", got)
	}
	if got := atomic.LoadInt32(&ssoCalls); got != 1 {
		t.Fatalf("expected one refreshed sso-session GetRoleCredentials call, got %d", got)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var refreshed awsV4SSOTokenCachePayload
	if err := json.Unmarshal(data, &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != newAccessToken || refreshed.RefreshToken != newRefreshToken {
		t.Fatalf("SSO cache was not refreshed: %#v", refreshed)
	}
	if refreshed.ClientID != clientID || refreshed.ClientSecret != clientSecret || refreshed.RegistrationExpiresAt != registrationExpires {
		t.Fatalf("SSO cache registration fields were not preserved: %#v", refreshed)
	}
	expiresAt, err := parseAWSV4SSOTokenExpiry(refreshed.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.After(time.Now().UTC().Add(30 * time.Minute)) {
		t.Fatalf("SSO cache expiration was not advanced: %s", refreshed.ExpiresAt)
	}
}

func assertAWSV4Signature(t *testing.T, r *http.Request, accessKeyID, secretAccessKey, region, service string) {
	t.Helper()
	if err := VerifySignature(r, accessKeyID, secretAccessKey, region, service); err != nil {
		t.Fatal(err)
	}
}

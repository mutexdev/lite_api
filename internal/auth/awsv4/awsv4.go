// Package awsv4 signs requests with AWS Signature Version 4, and resolves the
// credentials to sign them with: shared config and credentials files, assumed
// roles, web identity, SSO, and credential_process.
//
// US-067. Extracted from app.go as free functions -- none of this ever touched
// *App, which is why it could move at all. Sign is the whole exported surface;
// everything else stays unexported and is tested from inside the package.
package awsv4

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

// Sign adds the SigV4 Authorization header to req.
//
// resolve expands any template variables in the credential fields. It is a
// function rather than the variable map the caller used to pass, because that
// map was only ever consumed by interpolation -- taking the resolver instead
// means this package needs to know nothing about how variables are stored or
// expanded, and the interpolator can move to its own package independently.
func Sign(req *http.Request, auth types.AWSV4Auth, now time.Time, resolve func(string) string) error {
	accessKeyID := resolve(firstNonEmpty(auth.AccessKeyID, auth.AccessKey))
	secretAccessKey := resolve(firstNonEmpty(auth.SecretAccessKey, auth.SecretKey))
	sessionToken := resolve(auth.SessionToken)
	service := resolve(auth.Service)
	region := resolve(auth.Region)
	profileName := strings.TrimSpace(resolve(auth.ProfileName))
	if profileName != "" {
		if credentials, err := loadAWSV4ProfileCredentials(profileName); err == nil {
			accessKeyID = credentials.AccessKeyID
			secretAccessKey = credentials.SecretAccessKey
			sessionToken = credentials.SessionToken
		} else if accessKeyID == "" || secretAccessKey == "" {
			return err
		}
	}
	if accessKeyID == "" || secretAccessKey == "" {
		return errors.New("AWS SigV4 access key id and secret access key are required")
	}
	if service == "" {
		return errors.New("AWS SigV4 service is required")
	}
	if region == "" {
		return errors.New("AWS SigV4 region is required")
	}
	payloadHash, err := requestPayloadSHA256(req)
	if err != nil {
		return err
	}
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	} else {
		req.Header.Del("X-Amz-Security-Token")
	}

	canonicalHeaders, signedHeaders := awsCanonicalHeaders(req)
	credentialScope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(req.Method),
		awsCanonicalURI(req.URL),
		awsCanonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex(canonicalRequest),
	}, "\n")
	signature := hmacSHA256Hex(awsSigningKey(secretAccessKey, dateStamp, region, service), stringToSign)
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID,
		credentialScope,
		signedHeaders,
		signature,
	))
	return nil
}

type awsV4ProfileCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func loadAWSV4ProfileCredentials(profileName string) (awsV4ProfileCredentials, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return awsV4ProfileCredentials{}, errors.New("AWS SigV4 profile name is required")
	}
	configSections := map[string]map[string]string{}
	credentialSections := map[string]map[string]string{}
	if path := awsSharedConfigPath(); path != "" {
		if sections, err := parseAWSSharedConfigFile(path); err == nil {
			configSections = sections
		} else if !errors.Is(err, os.ErrNotExist) {
			return awsV4ProfileCredentials{}, err
		}
	}
	if path := awsSharedCredentialsPath(); path != "" {
		if sections, err := parseAWSSharedConfigFile(path); err == nil {
			credentialSections = sections
		} else if !errors.Is(err, os.ErrNotExist) {
			return awsV4ProfileCredentials{}, err
		}
	}
	return resolveAWSV4ProfileCredentials(profileName, configSections, credentialSections, map[string]bool{})
}

func resolveAWSV4ProfileCredentials(profileName string, configSections, credentialSections map[string]map[string]string, seen map[string]bool) (awsV4ProfileCredentials, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return awsV4ProfileCredentials{}, errors.New("AWS SigV4 profile name is required")
	}
	if seen[profileName] {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q has a circular source_profile chain", profileName)
	}
	seen[profileName] = true
	defer delete(seen, profileName)
	profile := awsV4ProfileValues(profileName, configSections, credentialSections)
	if strings.TrimSpace(profile["web_identity_token_file"]) != "" {
		return assumeAWSV4RoleWithWebIdentity(profileName, profile)
	}
	if roleARN := strings.TrimSpace(profile["role_arn"]); roleARN != "" {
		sourceCredentials, err := awsV4RoleSourceCredentials(profileName, profile, configSections, credentialSections, seen)
		if err != nil {
			return awsV4ProfileCredentials{}, err
		}
		return assumeAWSV4Role(profileName, profile, sourceCredentials)
	}
	if awsV4ProfileUsesSSO(profile) {
		return loadAWSV4SSOProfileCredentials(profileName, profile, configSections)
	}
	if strings.EqualFold(strings.TrimSpace(profile["credential_source"]), "environment") {
		if credentials := awsV4EnvironmentCredentials(); credentials.AccessKeyID != "" && credentials.SecretAccessKey != "" {
			return credentials, nil
		}
	}
	credentials := awsCredentialsFromProfileValues(profile)
	if (credentials.AccessKeyID == "" || credentials.SecretAccessKey == "") && strings.TrimSpace(profile["credential_process"]) != "" {
		processCredentials, err := loadAWSV4CredentialProcess(profile["credential_process"])
		if err != nil {
			return awsV4ProfileCredentials{}, err
		}
		credentials = processCredentials
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q did not provide aws_access_key_id and aws_secret_access_key", profileName)
	}
	return credentials, nil
}

func awsV4ProfileValues(profileName string, configSections, credentialSections map[string]map[string]string) map[string]string {
	profile := map[string]string{}
	profile = mergeAWSProfileValues(profile, configSections, awsConfigProfileSections(profileName))
	profile = mergeAWSProfileValues(profile, credentialSections, []string{profileName})
	return profile
}

func awsV4RoleSourceCredentials(profileName string, profile map[string]string, configSections, credentialSections map[string]map[string]string, seen map[string]bool) (awsV4ProfileCredentials, error) {
	if sourceProfile := strings.TrimSpace(profile["source_profile"]); sourceProfile != "" {
		return resolveAWSV4ProfileCredentials(sourceProfile, configSections, credentialSections, seen)
	}
	if strings.EqualFold(strings.TrimSpace(profile["credential_source"]), "environment") {
		credentials := awsV4EnvironmentCredentials()
		if credentials.AccessKeyID != "" && credentials.SecretAccessKey != "" {
			return credentials, nil
		}
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q uses credential_source=Environment but environment credentials are missing", profileName)
	}
	credentials := awsCredentialsFromProfileValues(profile)
	if credentials.AccessKeyID != "" && credentials.SecretAccessKey != "" {
		return credentials, nil
	}
	return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q with role_arn requires source_profile or credential_source=Environment", profileName)
}

func awsV4EnvironmentCredentials() awsV4ProfileCredentials {
	return awsV4ProfileCredentials{
		AccessKeyID:     strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")),
		SessionToken:    strings.TrimSpace(firstNonEmpty(os.Getenv("AWS_SESSION_TOKEN"), os.Getenv("AWS_SECURITY_TOKEN"))),
	}
}

func awsSharedCredentialsPath() string {
	if path := strings.TrimSpace(os.Getenv("AWS_SHARED_CREDENTIALS_FILE")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".aws", "credentials")
}

func awsSharedConfigPath() string {
	if path := strings.TrimSpace(os.Getenv("AWS_CONFIG_FILE")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".aws", "config")
}

func awsConfigProfileSections(profileName string) []string {
	if profileName == "default" {
		return []string{"default"}
	}
	return []string{"profile " + profileName, profileName}
}

func mergeAWSProfileValues(profile map[string]string, sections map[string]map[string]string, candidates []string) map[string]string {
	if profile == nil {
		profile = map[string]string{}
	}
	for _, section := range candidates {
		values, ok := sections[section]
		if !ok {
			continue
		}
		for key, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				profile[key] = trimmed
			}
		}
	}
	return profile
}

func awsCredentialsFromProfileValues(profile map[string]string) awsV4ProfileCredentials {
	return awsV4ProfileCredentials{
		AccessKeyID:     strings.TrimSpace(profile["aws_access_key_id"]),
		SecretAccessKey: strings.TrimSpace(profile["aws_secret_access_key"]),
		SessionToken:    strings.TrimSpace(profile["aws_session_token"]),
	}
}

func assumeAWSV4Role(profileName string, profile map[string]string, source awsV4ProfileCredentials) (awsV4ProfileCredentials, error) {
	roleARN := strings.TrimSpace(profile["role_arn"])
	if roleARN == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q role_arn is required", profileName)
	}
	region := awsV4STSRegion(profile)
	endpoint := awsV4STSEndpoint(profile, region)
	form := url.Values{}
	form.Set("Action", "AssumeRole")
	form.Set("Version", "2011-06-15")
	form.Set("RoleArn", roleARN)
	form.Set("RoleSessionName", firstNonEmpty(strings.TrimSpace(profile["role_session_name"]), awsV4DefaultRoleSessionName(profileName)))
	if externalID := strings.TrimSpace(profile["external_id"]); externalID != "" {
		form.Set("ExternalId", externalID)
	}
	if serialNumber := strings.TrimSpace(profile["mfa_serial"]); serialNumber != "" {
		tokenCode, err := awsV4MFATokenCode(profileName, profile)
		if err != nil {
			return awsV4ProfileCredentials{}, err
		}
		form.Set("SerialNumber", serialNumber)
		form.Set("TokenCode", tokenCode)
	}
	if duration := strings.TrimSpace(profile["duration_seconds"]); duration != "" {
		form.Set("DurationSeconds", duration)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("create AWS STS AssumeRole request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setRequestBodyString(req, form.Encode())
	if err := Sign(req, types.AWSV4Auth{
		AccessKeyID:     source.AccessKeyID,
		SecretAccessKey: source.SecretAccessKey,
		SessionToken:    source.SessionToken,
		Service:         "sts",
		Region:          region,
	}, time.Now().UTC(), func(value string) string { return value }); err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("sign AWS STS AssumeRole request: %w", err)
	}
	// US-017: shared client, posture unchanged — AWS credential calls keep
	// verified TLS and the environment proxy, not the user's proxy settings.
	res, err := httpClient().Do(req)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("call AWS STS AssumeRole: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("read AWS STS AssumeRole response: %w", readErr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS STS AssumeRole failed with %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	credentials, err := parseAWSV4AssumeRoleCredentials(body)
	if err != nil {
		return awsV4ProfileCredentials{}, err
	}
	return credentials, nil
}

func awsV4MFATokenCode(profileName string, profile map[string]string) (string, error) {
	candidates := []string{
		profile["mfa_token_code"],
		profile["mfa_token"],
		profile["token_code"],
	}
	profileKey := awsV4MFATokenEnvSuffix(profileName)
	if profileKey != "" {
		candidates = append(candidates,
			os.Getenv("AWS_MFA_TOKEN_CODE_"+profileKey),
			os.Getenv("AWS_MFA_TOKEN_"+profileKey),
		)
	}
	candidates = append(candidates,
		os.Getenv("AWS_MFA_TOKEN_CODE"),
		os.Getenv("AWS_MFA_TOKEN"),
		os.Getenv("AWS_MFA_CODE"),
		os.Getenv("AWS_TOKEN_CODE"),
	)
	for _, candidate := range candidates {
		if code := strings.TrimSpace(candidate); code != "" {
			return code, nil
		}
	}
	return "", fmt.Errorf("AWS SigV4 profile %q requires MFA token code; set AWS_MFA_TOKEN_CODE or mfa_token_code", profileName)
}

func awsV4MFATokenEnvSuffix(profileName string) string {
	var b strings.Builder
	lastWasSeparator := false
	for _, char := range strings.ToUpper(strings.TrimSpace(profileName)) {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			b.WriteRune(char)
			lastWasSeparator = false
			continue
		}
		if b.Len() > 0 && !lastWasSeparator {
			b.WriteByte('_')
			lastWasSeparator = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func assumeAWSV4RoleWithWebIdentity(profileName string, profile map[string]string) (awsV4ProfileCredentials, error) {
	roleARN := strings.TrimSpace(profile["role_arn"])
	if roleARN == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q role_arn is required for web identity", profileName)
	}
	tokenPath := strings.TrimSpace(profile["web_identity_token_file"])
	if tokenPath == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q web_identity_token_file is required", profileName)
	}
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("read AWS web identity token file: %w", err)
	}
	token := strings.TrimSpace(string(tokenData))
	if token == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 profile %q web identity token file is empty", profileName)
	}
	region := awsV4STSRegion(profile)
	endpoint := awsV4STSEndpoint(profile, region)
	form := url.Values{}
	form.Set("Action", "AssumeRoleWithWebIdentity")
	form.Set("Version", "2011-06-15")
	form.Set("RoleArn", roleARN)
	form.Set("RoleSessionName", firstNonEmpty(strings.TrimSpace(profile["role_session_name"]), awsV4DefaultRoleSessionName(profileName)))
	form.Set("WebIdentityToken", token)
	if duration := strings.TrimSpace(profile["duration_seconds"]); duration != "" {
		form.Set("DurationSeconds", duration)
	}
	if providerID := strings.TrimSpace(profile["provider_id"]); providerID != "" {
		form.Set("ProviderId", providerID)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("create AWS STS AssumeRoleWithWebIdentity request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setRequestBodyString(req, form.Encode())
	// US-017: shared client, posture unchanged.
	res, err := httpClient().Do(req)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("call AWS STS AssumeRoleWithWebIdentity: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("read AWS STS AssumeRoleWithWebIdentity response: %w", readErr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS STS AssumeRoleWithWebIdentity failed with %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	credentials, err := parseAWSV4AssumeRoleCredentials(body)
	if err != nil {
		return awsV4ProfileCredentials{}, err
	}
	return credentials, nil
}

func awsV4STSRegion(profile map[string]string) string {
	return firstNonEmpty(
		strings.TrimSpace(profile["region"]),
		strings.TrimSpace(os.Getenv("AWS_REGION")),
		strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")),
		"us-east-1",
	)
}

func awsV4STSEndpoint(profile map[string]string, region string) string {
	if endpoint := strings.TrimSpace(profile["sts_endpoint_url"]); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(profile["endpoint_url"]); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL_STS")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_STS_ENDPOINT_URL")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL")); endpoint != "" {
		return endpoint
	}
	return "https://sts." + firstNonEmpty(region, "us-east-1") + ".amazonaws.com"
}

func awsV4DefaultRoleSessionName(profileName string) string {
	var b strings.Builder
	for _, char := range "LiteAPI-" + profileName {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '=' || char == ',' || char == '.' || char == '@' {
			b.WriteRune(char)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 64 {
			break
		}
	}
	return firstNonEmpty(b.String(), "LiteAPI")
}

type awsV4STSCredentialsXML struct {
	AccessKeyID     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
}

type awsV4AssumeRoleResponse struct {
	AssumeRoleResult struct {
		Credentials awsV4STSCredentialsXML `xml:"Credentials"`
	} `xml:"AssumeRoleResult"`
	AssumeRoleWithWebIdentityResult struct {
		Credentials awsV4STSCredentialsXML `xml:"Credentials"`
	} `xml:"AssumeRoleWithWebIdentityResult"`
}

func parseAWSV4AssumeRoleCredentials(body []byte) (awsV4ProfileCredentials, error) {
	var response awsV4AssumeRoleResponse
	if err := xml.Unmarshal(body, &response); err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("parse AWS STS AssumeRole response: %w", err)
	}
	parsed := response.AssumeRoleResult.Credentials
	if strings.TrimSpace(parsed.AccessKeyID) == "" && strings.TrimSpace(parsed.SecretAccessKey) == "" {
		parsed = response.AssumeRoleWithWebIdentityResult.Credentials
	}
	credentials := awsV4ProfileCredentials{
		AccessKeyID:     strings.TrimSpace(parsed.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(parsed.SecretAccessKey),
		SessionToken:    strings.TrimSpace(parsed.SessionToken),
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return awsV4ProfileCredentials{}, errors.New("AWS STS AssumeRole response did not include AccessKeyId and SecretAccessKey")
	}
	return credentials, nil
}

func awsV4ProfileUsesSSO(profile map[string]string) bool {
	return strings.TrimSpace(profile["sso_session"]) != "" ||
		strings.TrimSpace(profile["sso_start_url"]) != "" ||
		strings.TrimSpace(profile["sso_account_id"]) != "" ||
		strings.TrimSpace(profile["sso_role_name"]) != ""
}

type awsV4SSOProfile struct {
	StartURL  string
	Region    string
	AccountID string
	RoleName  string
	Session   string
}

func loadAWSV4SSOProfileCredentials(profileName string, profile map[string]string, configSections map[string]map[string]string) (awsV4ProfileCredentials, error) {
	ssoProfile := awsV4SSOProfile{
		StartURL:  strings.TrimSpace(profile["sso_start_url"]),
		Region:    strings.TrimSpace(profile["sso_region"]),
		AccountID: strings.TrimSpace(profile["sso_account_id"]),
		RoleName:  strings.TrimSpace(profile["sso_role_name"]),
		Session:   strings.TrimSpace(profile["sso_session"]),
	}
	if ssoProfile.Session != "" {
		sessionValues := configSections["sso-session "+ssoProfile.Session]
		if ssoProfile.StartURL == "" {
			ssoProfile.StartURL = strings.TrimSpace(sessionValues["sso_start_url"])
		}
		if ssoProfile.Region == "" {
			ssoProfile.Region = strings.TrimSpace(sessionValues["sso_region"])
		}
	}
	if ssoProfile.StartURL == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 SSO profile %q requires sso_start_url", profileName)
	}
	if ssoProfile.Region == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 SSO profile %q requires sso_region", profileName)
	}
	if ssoProfile.AccountID == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 SSO profile %q requires sso_account_id", profileName)
	}
	if ssoProfile.RoleName == "" {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SigV4 SSO profile %q requires sso_role_name", profileName)
	}
	cacheKey := ssoProfile.StartURL
	if ssoProfile.Session != "" {
		cacheKey = ssoProfile.Session
	}
	token, err := loadAWSV4SSOToken(cacheKey, ssoProfile, profile)
	if err != nil {
		return awsV4ProfileCredentials{}, err
	}
	return requestAWSV4SSORoleCredentials(ssoProfile, profile, token.AccessToken)
}

type awsV4SSOToken struct {
	AccessToken string
	ExpiresAt   time.Time
}

type awsV4SSOTokenCachePayload struct {
	StartURL              string `json:"startUrl,omitempty"`
	Region                string `json:"region,omitempty"`
	AccessToken           string `json:"accessToken,omitempty"`
	ExpiresAt             string `json:"expiresAt,omitempty"`
	RefreshToken          string `json:"refreshToken,omitempty"`
	ClientID              string `json:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty"`
	RegistrationExpiresAt string `json:"registrationExpiresAt,omitempty"`
}

const awsV4SSOTokenRefreshWindow = 5 * time.Minute

func loadAWSV4SSOToken(cacheKey string, ssoProfile awsV4SSOProfile, rawProfile map[string]string) (awsV4SSOToken, error) {
	cachePath := filepath.Join(awsV4SSOCacheDir(), awsV4SSOCacheFilename(cacheKey))
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return awsV4SSOToken{}, fmt.Errorf("read AWS SSO cached token: %w", err)
	}
	var payload awsV4SSOTokenCachePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return awsV4SSOToken{}, fmt.Errorf("parse AWS SSO cached token: %w", err)
	}
	payload.StartURL = firstNonEmpty(strings.TrimSpace(payload.StartURL), ssoProfile.StartURL)
	payload.Region = firstNonEmpty(strings.TrimSpace(payload.Region), ssoProfile.Region)
	token, err := awsV4SSOTokenFromCache(payload)
	if err != nil {
		return awsV4SSOToken{}, err
	}
	now := time.Now().UTC()
	if ssoProfile.Session != "" && awsV4SSOTokenShouldRefresh(token, now) {
		refreshedPayload, refreshedToken, err := refreshAWSV4SSOToken(payload, ssoProfile, rawProfile, now)
		if err != nil {
			return awsV4SSOToken{}, err
		}
		if err := writeAWSV4SSOTokenCache(cachePath, refreshedPayload); err != nil {
			return awsV4SSOToken{}, err
		}
		return refreshedToken, nil
	}
	if awsV4SSOTokenExpired(token, now) {
		return awsV4SSOToken{}, errors.New("AWS SSO cached token is expired")
	}
	return token, nil
}

func awsV4SSOTokenFromCache(payload awsV4SSOTokenCachePayload) (awsV4SSOToken, error) {
	token := awsV4SSOToken{AccessToken: strings.TrimSpace(payload.AccessToken)}
	if token.AccessToken == "" {
		return awsV4SSOToken{}, errors.New("AWS SSO cached token did not include accessToken")
	}
	if expiresAt := strings.TrimSpace(payload.ExpiresAt); expiresAt != "" {
		parsed, err := parseAWSV4SSOTokenExpiry(expiresAt)
		if err != nil {
			return awsV4SSOToken{}, err
		}
		token.ExpiresAt = parsed
	}
	return token, nil
}

func awsV4SSOTokenExpired(token awsV4SSOToken, now time.Time) bool {
	return !token.ExpiresAt.IsZero() && !now.Before(token.ExpiresAt)
}

func awsV4SSOTokenShouldRefresh(token awsV4SSOToken, now time.Time) bool {
	return !token.ExpiresAt.IsZero() && !now.Before(token.ExpiresAt.Add(-awsV4SSOTokenRefreshWindow))
}

func refreshAWSV4SSOToken(payload awsV4SSOTokenCachePayload, ssoProfile awsV4SSOProfile, rawProfile map[string]string, now time.Time) (awsV4SSOTokenCachePayload, awsV4SSOToken, error) {
	clientID := strings.TrimSpace(payload.ClientID)
	clientSecret := strings.TrimSpace(payload.ClientSecret)
	refreshToken := strings.TrimSpace(payload.RefreshToken)
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, errors.New("AWS SSO cached token is expired and cannot be refreshed")
	}
	endpoint := awsV4SSOOIDCEndpoint(rawProfile, ssoProfile.Region)
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("parse AWS SSO OIDC endpoint: %w", err)
	}
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/token"
	body, err := json.Marshal(struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		GrantType    string `json:"grantType"`
		RefreshToken string `json:"refreshToken"`
	}{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	})
	if err != nil {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("encode AWS SSO OIDC token refresh request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("create AWS SSO OIDC token refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// US-017: shared client, posture unchanged.
	res, err := httpClient().Do(req)
	if err != nil {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("call AWS SSO OIDC token refresh: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	responseBody, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("read AWS SSO OIDC token refresh response: %w", readErr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("AWS SSO OIDC token refresh failed with %s: %s", res.Status, strings.TrimSpace(string(responseBody)))
	}
	var response struct {
		AccessToken  string `json:"accessToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, fmt.Errorf("parse AWS SSO OIDC token refresh response: %w", err)
	}
	accessToken := strings.TrimSpace(response.AccessToken)
	if accessToken == "" {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, errors.New("AWS SSO OIDC token refresh response did not include accessToken")
	}
	if response.ExpiresIn <= 0 {
		return awsV4SSOTokenCachePayload{}, awsV4SSOToken{}, errors.New("AWS SSO OIDC token refresh response did not include expiresIn")
	}
	expiresAt := now.Add(time.Duration(response.ExpiresIn) * time.Second).UTC()
	payload.AccessToken = accessToken
	payload.ExpiresAt = expiresAt.Format(time.RFC3339)
	if nextRefreshToken := strings.TrimSpace(response.RefreshToken); nextRefreshToken != "" {
		payload.RefreshToken = nextRefreshToken
	}
	payload.ClientID = clientID
	payload.ClientSecret = clientSecret
	payload.StartURL = firstNonEmpty(strings.TrimSpace(payload.StartURL), ssoProfile.StartURL)
	payload.Region = firstNonEmpty(strings.TrimSpace(payload.Region), ssoProfile.Region)
	return payload, awsV4SSOToken{AccessToken: accessToken, ExpiresAt: expiresAt}, nil
}

func writeAWSV4SSOTokenCache(cachePath string, payload awsV4SSOTokenCachePayload) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode AWS SSO cached token: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		return fmt.Errorf("write AWS SSO cached token: %w", err)
	}
	return nil
}

func awsV4SSOCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv("AWS_SSO_CACHE_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), ".aws", "sso", "cache")
	}
	return filepath.Join(home, ".aws", "sso", "cache")
}

func awsV4SSOCacheFilename(cacheKey string) string {
	sum := sha1.Sum([]byte(cacheKey))
	return hex.EncodeToString(sum[:]) + ".json"
}

func parseAWSV4SSOTokenExpiry(value string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05UTC",
		"2006-01-02T15:04:05.000000UTC",
		"2006-01-02T15:04:05.000UTC",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse AWS SSO cached token expiration %q", value)
}

func requestAWSV4SSORoleCredentials(profile awsV4SSOProfile, rawProfile map[string]string, accessToken string) (awsV4ProfileCredentials, error) {
	endpoint := awsV4SSOEndpoint(rawProfile, profile.Region)
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("parse AWS SSO endpoint: %w", err)
	}
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/federation/credentials"
	query := requestURL.Query()
	query.Set("account_id", profile.AccountID)
	query.Set("role_name", profile.RoleName)
	requestURL.RawQuery = query.Encode()
	req, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("create AWS SSO GetRoleCredentials request: %w", err)
	}
	req.Header.Set("x-amz-sso_bearer_token", accessToken)
	// US-017: shared client, posture unchanged.
	res, err := httpClient().Do(req)
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("call AWS SSO GetRoleCredentials: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("read AWS SSO GetRoleCredentials response: %w", readErr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS SSO GetRoleCredentials failed with %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	return parseAWSV4SSORoleCredentials(body)
}

func awsV4SSOEndpoint(profile map[string]string, region string) string {
	if endpoint := strings.TrimSpace(profile["sso_endpoint_url"]); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(profile["endpoint_url"]); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL_SSO")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_SSO_ENDPOINT_URL")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL")); endpoint != "" {
		return endpoint
	}
	return "https://portal.sso." + firstNonEmpty(region, "us-east-1") + ".amazonaws.com"
}

func awsV4SSOOIDCEndpoint(profile map[string]string, region string) string {
	if endpoint := strings.TrimSpace(profile["sso_oidc_endpoint_url"]); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(profile["ssooidc_endpoint_url"]); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL_SSOOIDC")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL_SSO_OIDC")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_SSO_OIDC_ENDPOINT_URL")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL")); endpoint != "" {
		return endpoint
	}
	return "https://oidc." + firstNonEmpty(region, "us-east-1") + ".amazonaws.com"
}

type awsV4SSORoleCredentialsResponse struct {
	RoleCredentials struct {
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
		SessionToken    string `json:"sessionToken"`
	} `json:"roleCredentials"`
}

func parseAWSV4SSORoleCredentials(body []byte) (awsV4ProfileCredentials, error) {
	var response awsV4SSORoleCredentialsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("parse AWS SSO GetRoleCredentials response: %w", err)
	}
	credentials := awsV4ProfileCredentials{
		AccessKeyID:     strings.TrimSpace(response.RoleCredentials.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(response.RoleCredentials.SecretAccessKey),
		SessionToken:    strings.TrimSpace(response.RoleCredentials.SessionToken),
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return awsV4ProfileCredentials{}, errors.New("AWS SSO GetRoleCredentials response did not include accessKeyId and secretAccessKey")
	}
	return credentials, nil
}

type awsV4CredentialProcessResponse struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
}

func loadAWSV4CredentialProcess(commandLine string) (awsV4ProfileCredentials, error) {
	args, err := splitAWSCredentialProcessCommand(commandLine)
	if err != nil {
		return awsV4ProfileCredentials{}, err
	}
	if len(args) == 0 {
		return awsV4ProfileCredentials{}, errors.New("AWS credential_process command is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return awsV4ProfileCredentials{}, errors.New("AWS credential_process timed out")
	}
	if err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS credential_process failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var response awsV4CredentialProcessResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return awsV4ProfileCredentials{}, fmt.Errorf("parse AWS credential_process output: %w", err)
	}
	if response.Version != 1 {
		return awsV4ProfileCredentials{}, fmt.Errorf("AWS credential_process returned unsupported Version %d", response.Version)
	}
	credentials := awsV4ProfileCredentials{
		AccessKeyID:     strings.TrimSpace(response.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(response.SecretAccessKey),
		SessionToken:    strings.TrimSpace(response.SessionToken),
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return awsV4ProfileCredentials{}, errors.New("AWS credential_process output did not include AccessKeyId and SecretAccessKey")
	}
	return credentials, nil
}

func splitAWSCredentialProcessCommand(commandLine string) ([]string, error) {
	args := []string{}
	var current strings.Builder
	var quote rune
	escaped := false
	inToken := false
	for _, char := range commandLine {
		if escaped {
			current.WriteRune(char)
			inToken = true
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			inToken = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			current.WriteRune(char)
			inToken = true
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			inToken = true
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			if inToken {
				args = append(args, current.String())
				current.Reset()
				inToken = false
			}
			continue
		}
		current.WriteRune(char)
		inToken = true
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, errors.New("AWS credential_process command has an unterminated quote")
	}
	if inToken {
		args = append(args, current.String())
	}
	return args, nil
}

func parseAWSSharedConfigFile(path string) (map[string]map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sections := map[string]map[string]string{}
	section := ""
	for lineNumber, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				return nil, fmt.Errorf("parse AWS shared config %s:%d: missing closing bracket", path, lineNumber+1)
			}
			section = strings.TrimSpace(line[1:end])
			if section == "" {
				return nil, fmt.Errorf("parse AWS shared config %s:%d: empty section", path, lineNumber+1)
			}
			if _, ok := sections[section]; !ok {
				sections[section] = map[string]string{}
			}
			continue
		}
		if section == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			key, value, ok = strings.Cut(line, ":")
		}
		if !ok {
			continue
		}
		sections[section][strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return sections, nil
}

func requestPayloadSHA256(req *http.Request) (string, error) {
	if req == nil || req.Body == nil || req.ContentLength == 0 {
		return sha256Hex(""), nil
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
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
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
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
	}
	return "", errors.New("AWS SigV4 signing requires a rewindable request body")
}

func awsCanonicalHeaders(req *http.Request) (string, string) {
	values := map[string]string{}
	host := req.URL.Host
	if req.Host != "" {
		host = req.Host
	}
	values["host"] = host
	for name, headerValues := range req.Header {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "" || lower == "authorization" {
			continue
		}
		normalized := make([]string, 0, len(headerValues))
		for _, value := range headerValues {
			normalized = append(normalized, normalizeWhitespace(value))
		}
		values[lower] = strings.Join(normalized, ",")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		fmt.Fprintf(&canonical, "%s:%s\n", name, values[name])
	}
	return canonical.String(), strings.Join(names, ";")
}

func awsCanonicalURI(u *url.URL) string {
	if u == nil {
		return "/"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			decoded = segment
		}
		segments[i] = awsEncode(decoded)
	}
	out := strings.Join(segments, "/")
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out
}

func awsCanonicalQuery(u *url.URL) string {
	if u == nil || u.RawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return u.RawQuery
	}
	type queryPart struct {
		key   string
		value string
	}
	parts := []queryPart{}
	for key, entries := range values {
		if len(entries) == 0 {
			parts = append(parts, queryPart{key: key})
			continue
		}
		for _, value := range entries {
			parts = append(parts, queryPart{key: key, value: value})
		}
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].key == parts[j].key {
			return parts[i].value < parts[j].value
		}
		return parts[i].key < parts[j].key
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, awsEncode(part.key)+"="+awsEncode(part.value))
	}
	return strings.Join(out, "&")
}

func awsEncode(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func awsSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256Bytes([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256Bytes(kDate, region)
	kService := hmacSHA256Bytes(kRegion, service)
	return hmacSHA256Bytes(kService, "aws4_request")
}

func hmacSHA256Bytes(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}

func hmacSHA256Hex(key []byte, value string) string {
	return hex.EncodeToString(hmacSHA256Bytes(key, value))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// firstNonEmpty returns the first value that is not blank.
//
// A copy rather than an import: it is seven dependency-free lines, and having
// package main export a helper this generic just to share it would be a worse
// coupling than the duplication.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// httpClient is the client used for credential-resolution calls: STS, SSO and
// OIDC. It is a variable rather than a hard-wired client because package main
// routes these through its shared transport cache (verified TLS, inherited
// proxy), and this package must not depend on that machinery to be usable or
// testable on its own. Package main overrides it during startup.
var httpClient = func() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// SetHTTPClient installs the client used for credential-resolution calls.
func SetHTTPClient(get func() *http.Client) {
	if get != nil {
		httpClient = get
	}
}

// setRequestBodyString rewrites a request body and keeps GetBody consistent
// with it, which SigV4 needs because the payload hash is computed from a replay
// of the body. Copied rather than imported for the same reason firstNonEmpty is.
func setRequestBodyString(req *http.Request, value string) {
	data := []byte(value)
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.ContentLength = int64(len(data))
}

// normalizeWhitespace collapses runs of whitespace to single spaces, which the
// canonical-headers step of SigV4 requires.
func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// VerifySignature recomputes the SigV4 signature of a received request and
// reports whether it matches the one in the Authorization header.
//
// This is the inverse of Sign, and it lives here rather than in a test because
// the alternative was duplicating the whole canonicalisation chain -- canonical
// URI, query, headers, signing key -- into a test file in another package. A
// signing package that cannot check its own output is missing half its surface;
// anything receiving signed requests needs exactly this.
func VerifySignature(r *http.Request, accessKeyID, secretAccessKey, region, service string) error {
	parts := parseAuthorizationHeader(r.Header.Get("Authorization"))
	amzDate := r.Header.Get("X-Amz-Date")
	if len(amzDate) < 8 {
		return fmt.Errorf("missing or short x-amz-date: %q", amzDate)
	}
	dateStamp := amzDate[:8]
	credentialScope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	if got := parts["Credential"]; got != accessKeyID+"/"+credentialScope {
		return fmt.Errorf("unexpected credential: %q, want %q", got, accessKeyID+"/"+credentialScope)
	}
	signedHeaders := parts["SignedHeaders"]
	if signedHeaders == "" || parts["Signature"] == "" {
		return fmt.Errorf("missing signature fields: %#v", parts)
	}

	// Only the headers the signer listed take part, and host is derived from the
	// request rather than read back from the header map.
	filtered := http.Header{}
	for _, name := range strings.Split(signedHeaders, ";") {
		if name == "" || name == "host" {
			continue
		}
		for _, value := range r.Header.Values(name) {
			filtered.Add(name, value)
		}
	}
	clone := *r
	clone.Header = filtered
	canonicalHeaders, signedHeaderEcho := awsCanonicalHeaders(&clone)
	if signedHeaderEcho != signedHeaders {
		return fmt.Errorf("signed header mismatch: got %q, want %q", signedHeaderEcho, signedHeaders)
	}

	canonicalRequest := strings.Join([]string{
		strings.ToUpper(r.Method),
		awsCanonicalURI(r.URL),
		awsCanonicalQuery(r.URL),
		canonicalHeaders,
		signedHeaders,
		r.Header.Get("X-Amz-Content-Sha256"),
	}, "\n")
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, credentialScope, sha256Hex(canonicalRequest)}, "\n")
	expected := hmacSHA256Hex(awsSigningKey(secretAccessKey, dateStamp, region, service), stringToSign)
	if parts["Signature"] != expected {
		return fmt.Errorf("bad signature: got %s, want %s\ncanonical request:\n%s", parts["Signature"], expected, canonicalRequest)
	}
	return nil
}

// PayloadSHA256 is the hex payload hash SigV4 puts in X-Amz-Content-Sha256.
func PayloadSHA256(value string) string { return sha256Hex(value) }

// SSOCacheFilename is where the SSO token cache for a start URL or session name
// lives, relative to the SSO cache directory.
func SSOCacheFilename(cacheKey string) string { return awsV4SSOCacheFilename(cacheKey) }

// parseAuthorizationHeader splits an AWS4-HMAC-SHA256 Authorization header into
// its Credential, SignedHeaders and Signature parts.
func parseAuthorizationHeader(header string) map[string]string {
	parts := map[string]string{}
	trimmed := strings.TrimPrefix(strings.TrimSpace(header), "AWS4-HMAC-SHA256")
	for _, field := range strings.Split(trimmed, ",") {
		field = strings.TrimSpace(field)
		if key, value, ok := strings.Cut(field, "="); ok {
			parts[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return parts
}

package awsv4

// Assuming a role through STS: plain, web-identity and SSO, plus the XML responses each returns.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

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

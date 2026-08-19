package awsv4

// Resolving credentials from the shared config: profiles, SSO and credential_process.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

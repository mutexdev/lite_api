// Re-syncing an OpenAPI spec must not wipe the user's credentials.
//
// Coverage found mergeOAuth1Preserving, mergeOAuth2Preserving and
// mergeAWSV4Preserving at 0% — three functions whose entire job is to keep what
// the user typed when the spec is re-imported over their request.
//
// The failure mode is the worst kind of silent: a re-sync appears to succeed,
// the request still lists oauth2, and the client secret is simply gone. Nothing
// errors, and the user finds out when the next send returns 401.
//
// The sibling merges (headers, params, JSON bodies) were already covered — an
// earlier control showed discarding user values there failed a test. These
// three were not, which is why they are worth writing down now.
package openapisync

import (
	"testing"

	"LiteAPI/internal/types"
)

func TestOAuth2CredentialsSurviveAReSync(t *testing.T) {
	existing := types.OAuth2Auth{
		GrantType:    "authorization_code",
		ClientID:     "user-client-id",
		ClientSecret: "user-secret",
		Username:     "ada",
		Password:     "hunter2",
		Scope:        "read write",
		State:        "xyz",
	}
	// What a spec re-import supplies: URLs it knows, credentials it cannot.
	spec := types.OAuth2Auth{
		GrantType:        "client_credentials",
		AuthorizationURL: "https://spec.test/authorize",
		AccessTokenURL:   "https://spec.test/token",
	}

	got := mergeOAuth2Preserving(existing, spec)

	for name, pair := range map[string][2]string{
		"ClientID":     {got.ClientID, "user-client-id"},
		"ClientSecret": {got.ClientSecret, "user-secret"},
		"Username":     {got.Username, "ada"},
		"Password":     {got.Password, "hunter2"},
		"Scope":        {got.Scope, "read write"},
		"State":        {got.State, "xyz"},
		"GrantType":    {got.GrantType, "authorization_code"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q after re-sync, want %q — the user's value was lost", name, pair[0], pair[1])
		}
	}
	// And the spec still gets to supply what the user left blank.
	if got.AuthorizationURL != "https://spec.test/authorize" || got.AccessTokenURL != "https://spec.test/token" {
		t.Errorf("spec URLs were not adopted: %q / %q", got.AuthorizationURL, got.AccessTokenURL)
	}
}

func TestAWSV4CredentialsSurviveAReSync(t *testing.T) {
	existing := types.AWSV4Auth{
		AccessKeyID:     "AKIAUSER",
		SecretAccessKey: "user-secret-key",
		SessionToken:    "user-session",
		ProfileName:     "work",
	}
	spec := types.AWSV4Auth{Service: "execute-api", Region: "eu-west-1"}

	got := mergeAWSV4Preserving(existing, spec)

	for name, pair := range map[string][2]string{
		"AccessKeyID":     {got.AccessKeyID, "AKIAUSER"},
		"SecretAccessKey": {got.SecretAccessKey, "user-secret-key"},
		"SessionToken":    {got.SessionToken, "user-session"},
		"ProfileName":     {got.ProfileName, "work"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q after re-sync, want %q — the credential was lost", name, pair[0], pair[1])
		}
	}
	if got.Service != "execute-api" || got.Region != "eu-west-1" {
		t.Errorf("spec service/region not adopted: %q / %q", got.Service, got.Region)
	}
}

func TestOAuth1CredentialsSurviveAReSync(t *testing.T) {
	existing := types.OAuth1Auth{
		ConsumerKey:       "user-consumer",
		ConsumerSecret:    "user-consumer-secret",
		AccessToken:       "user-token",
		AccessTokenSecret: "user-token-secret",
		PrivateKey:        "-----BEGIN PRIVATE KEY-----",
	}
	spec := types.OAuth1Auth{SignatureMethod: "HMAC-SHA256", CallbackURL: "https://spec.test/cb"}

	got := mergeOAuth1Preserving(existing, spec)

	for name, pair := range map[string][2]string{
		"ConsumerKey":       {got.ConsumerKey, "user-consumer"},
		"ConsumerSecret":    {got.ConsumerSecret, "user-consumer-secret"},
		"AccessToken":       {got.AccessToken, "user-token"},
		"AccessTokenSecret": {got.AccessTokenSecret, "user-token-secret"},
		"PrivateKey":        {got.PrivateKey, "-----BEGIN PRIVATE KEY-----"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q after re-sync, want %q — the credential was lost", name, pair[0], pair[1])
		}
	}
	if got.SignatureMethod != "HMAC-SHA256" || got.CallbackURL != "https://spec.test/cb" {
		t.Errorf("spec values not adopted: %q / %q", got.SignatureMethod, got.CallbackURL)
	}
}

// An empty existing config must not blank out what the spec provides, or a
// first sync onto a fresh request would produce an auth block with nothing in it.
func TestAnEmptyExistingConfigAdoptsTheSpecWholesale(t *testing.T) {
	spec := types.OAuth2Auth{ClientID: "spec-client", AccessTokenURL: "https://spec.test/token"}
	got := mergeOAuth2Preserving(types.OAuth2Auth{}, spec)
	if got.ClientID != "spec-client" || got.AccessTokenURL != "https://spec.test/token" {
		t.Fatalf("a blank existing config must take the spec's values, got %#v", got)
	}
}

// Precedence, tested where it can actually be observed.
//
// The tests above set each credential on ONE side only, so they cannot tell
// "existing wins" from "either wins" — reversing the argument order failed none
// of them, which a control showed. When BOTH sides carry a value the direction
// becomes visible, and that is the case that matters: a spec re-import DOES
// carry a client id, and it must not overwrite the one the user entered.
func TestExistingValuesWinWhenBothSidesAreSet(t *testing.T) {
	existing := types.OAuth2Auth{
		ClientID:     "user-client",
		ClientSecret: "user-secret",
		Scope:        "user-scope",
	}
	spec := types.OAuth2Auth{
		ClientID:     "spec-client",
		ClientSecret: "spec-secret",
		Scope:        "spec-scope",
	}

	got := mergeOAuth2Preserving(existing, spec)

	for name, pair := range map[string][2]string{
		"ClientID":     {got.ClientID, "user-client"},
		"ClientSecret": {got.ClientSecret, "user-secret"},
		"Scope":        {got.Scope, "user-scope"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q — the spec overwrote a value the user had set", name, pair[0], pair[1])
		}
	}
}

func TestExistingAWSCredentialsWinWhenBothSidesAreSet(t *testing.T) {
	got := mergeAWSV4Preserving(
		types.AWSV4Auth{AccessKeyID: "AKIAUSER", SecretAccessKey: "user-secret", Region: "eu-west-1"},
		types.AWSV4Auth{AccessKeyID: "AKIASPEC", SecretAccessKey: "spec-secret", Region: "us-east-1"},
	)
	if got.AccessKeyID != "AKIAUSER" || got.SecretAccessKey != "user-secret" || got.Region != "eu-west-1" {
		t.Fatalf("spec overwrote user AWS values: %#v", got)
	}
}

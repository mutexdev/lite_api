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

// Multipart form values across a re-sync, the last of the preserving merges to
// have no tests.
//
// Same shape as the credential merges: the spec knows which parts EXIST, the
// user knows what goes IN them. A re-sync that adopts the spec wholesale wipes
// the file paths and values someone entered by hand, and the request still looks
// correct afterwards — the parts are all there, just empty.
//
// The cursor logic is what makes this worth testing rather than reading. A
// multipart body may legitimately repeat a name (several files under "file"),
// so matching by name alone would give every repeat the FIRST existing value.
func TestMultipartValuesSurviveAReSync(t *testing.T) {
	spec := []types.FormPart{
		{Name: "file", ContentType: "application/octet-stream"},
		{Name: "description"},
	}
	existing := []types.FormPart{
		{Name: "file", FilePath: "/home/ada/report.pdf", ContentType: "application/pdf", Enabled: true},
		{Name: "description", Value: "quarterly report", Enabled: true},
	}

	got := mergeFormPartListPreserving(spec, existing, true)

	if len(got) != 2 {
		t.Fatalf("got %d parts, want 2: %+v", len(got), got)
	}
	if got[0].FilePath != "/home/ada/report.pdf" {
		t.Errorf("file path was lost: %q", got[0].FilePath)
	}
	if got[0].ContentType != "application/pdf" {
		t.Errorf("the user's content type was overwritten by the spec's: %q", got[0].ContentType)
	}
	if got[1].Value != "quarterly report" {
		t.Errorf("value was lost: %q", got[1].Value)
	}
}

// Repeated names must consume their matches in order, not all take the first.
func TestRepeatedMultipartNamesTakeTheirOwnValues(t *testing.T) {
	spec := []types.FormPart{{Name: "file"}, {Name: "file"}, {Name: "file"}}
	existing := []types.FormPart{
		{Name: "file", FilePath: "/a.txt", Enabled: true},
		{Name: "file", FilePath: "/b.txt", Enabled: true},
	}

	got := mergeFormPartListPreserving(spec, existing, true)

	if len(got) != 3 {
		t.Fatalf("got %d parts, want 3", len(got))
	}
	if got[0].FilePath != "/a.txt" || got[1].FilePath != "/b.txt" {
		t.Fatalf("repeated names did not consume their matches in order: %q, %q", got[0].FilePath, got[1].FilePath)
	}
	// The third has no counterpart and must come through as the spec declared it,
	// not as a copy of an earlier one.
	if got[2].FilePath != "" {
		t.Errorf("a spec part with no existing counterpart inherited %q", got[2].FilePath)
	}
}

// preserveValues=false is the "take the spec wholesale" path, used when the user
// asked for exactly that. It must not quietly preserve anything.
func TestMultipartMergeWithoutPreserveTakesTheSpec(t *testing.T) {
	got := mergeFormPartListPreserving(
		[]types.FormPart{{Name: "file"}},
		[]types.FormPart{{Name: "file", FilePath: "/should-not-survive.txt"}},
		false,
	)
	if len(got) != 1 || got[0].FilePath != "" {
		t.Fatalf("preserveValues=false must adopt the spec unchanged, got %+v", got)
	}
}

// A part the spec no longer declares is dropped: the body must match the spec's
// shape, or a re-sync would never remove anything.
func TestMultipartPartsRemovedFromTheSpecAreDropped(t *testing.T) {
	got := mergeFormPartListPreserving(
		[]types.FormPart{{Name: "kept"}},
		[]types.FormPart{{Name: "kept", Value: "v"}, {Name: "gone", Value: "x"}},
		true,
	)
	if len(got) != 1 || got[0].Name != "kept" {
		t.Fatalf("a part absent from the spec must not survive: %+v", got)
	}
}

// The multipart drift fingerprint, the last uncovered function in this package.
//
// Local drift detection compares what the collection has on disk against what
// the spec declares, and this builds the multipart side of that comparison. It
// encodes name AND kind, so a part switching between text and file registers as
// drift — without the kind, swapping a text field for a file upload would look
// identical and the drift report would say nothing changed.
func TestDriftMultipartNamesEncodeNameAndKind(t *testing.T) {
	got := openAPILocalDriftMultipartNames([]types.FormPart{
		{Name: "title", Value: "Quarterly"},
		{Name: "doc", FilePath: "/tmp/report.pdf"},
	})
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	// SORTED, not in input order. My first draft asserted input order and the
	// code was right: a drift fingerprint has to be order-independent, or
	// reordering two form parts would report as a change to the request.
	if got[0] != "doc:file" || got[1] != "title:text" {
		t.Errorf("got %v, want the entries sorted: [doc:file title:text]", got)
	}
}

// Order-independence, stated directly since it is why the sort is there.
func TestDriftMultipartNamesAreOrderIndependent(t *testing.T) {
	a := openAPILocalDriftMultipartNames([]types.FormPart{
		{Name: "title", Value: "v"}, {Name: "doc", FilePath: "/x"},
	})
	b := openAPILocalDriftMultipartNames([]types.FormPart{
		{Name: "doc", FilePath: "/x"}, {Name: "title", Value: "v"},
	})
	if len(a) != len(b) || a[0] != b[0] || a[1] != b[1] {
		t.Fatalf("%v vs %v — reordering parts must not look like drift", a, b)
	}
}

// The property that makes it a drift signal at all: the same name with a
// different kind must not compare equal.
func TestDriftMultipartNamesDistinguishTextFromFile(t *testing.T) {
	asText := openAPILocalDriftMultipartNames([]types.FormPart{{Name: "doc", Value: "inline"}})
	asFile := openAPILocalDriftMultipartNames([]types.FormPart{{Name: "doc", FilePath: "/tmp/x"}})
	if asText[0] == asFile[0] {
		t.Fatalf("both encoded as %q — swapping a text field for a file upload would not register as drift", asText[0])
	}
}

func TestDriftMultipartNamesSkipUnnamedParts(t *testing.T) {
	got := openAPILocalDriftMultipartNames([]types.FormPart{
		{Name: "  ", Value: "v"},
		{Name: "", FilePath: "/tmp/x"},
		{Name: "kept", Value: "v"},
	})
	if len(got) != 1 || got[0] != "kept:text" {
		t.Fatalf("got %v; an unnamed part cannot be compared and must be skipped", got)
	}
}

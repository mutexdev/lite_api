package mcpserver

import "testing"

// The redaction semantics are the safety contract of the whole package, so
// the cases here pin the RULES, not the implementation: credential-shaped
// literals are masked, templates pass through, and auth rows default to
// masked with an addressing-field allowlist.

func TestRedactRowsMasksCredentialLiteralsAndKeepsTemplates(t *testing.T) {
	rows := []KeyValue{
		{Name: "Authorization", Value: "Bearer eyJhbGciOi.literal", Enabled: true},
		{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true},
		{Name: "X-Api-Key", Value: "abc123", Enabled: true},
		{Name: "Content-Type", Value: "application/json", Enabled: true},
		{Name: "X-Session-Token", Value: "", Enabled: true},
	}
	got := RedactRows(rows)
	if got[0].Value != MaskedValue {
		t.Fatalf("literal Authorization survived: %q", got[0].Value)
	}
	if got[1].Value != "Bearer {{apiToken}}" {
		t.Fatalf("templated Authorization was masked: %q", got[1].Value)
	}
	if got[2].Value != MaskedValue {
		t.Fatalf("literal X-Api-Key survived: %q", got[2].Value)
	}
	if got[3].Value != "application/json" {
		t.Fatalf("Content-Type was masked: %q", got[3].Value)
	}
	if got[4].Value != "" {
		t.Fatalf("empty value changed: %q", got[4].Value)
	}
	if rows[0].Value == MaskedValue {
		t.Fatal("RedactRows modified its input")
	}
}

func TestMaskAuthRowsDefaultsToMaskedWithAddressingAllowlist(t *testing.T) {
	rows := []KeyValue{
		{Name: "password", Value: "hunter2", Enabled: true},
		{Name: "username", Value: "svc-pos", Enabled: true},
		{Name: "clientSecret", Value: "shhh", Enabled: true},
		{Name: "clientId", Value: "pos-app", Enabled: true},
		{Name: "tokenUrl", Value: "https://id.example.test/token", Enabled: true},
		{Name: "token", Value: "{{apiToken}}", Enabled: true},
	}
	got := MaskAuthRows(rows)
	if got[0].Value != MaskedValue {
		t.Fatalf("password literal survived: %q", got[0].Value)
	}
	if got[1].Value != "svc-pos" {
		t.Fatalf("username was masked: %q", got[1].Value)
	}
	if got[2].Value != MaskedValue {
		t.Fatalf("clientSecret literal survived: %q", got[2].Value)
	}
	if got[3].Value != "pos-app" {
		t.Fatalf("clientId was masked: %q", got[3].Value)
	}
	if got[4].Value != "https://id.example.test/token" {
		t.Fatalf("tokenUrl was masked: %q", got[4].Value)
	}
	if got[5].Value != "{{apiToken}}" {
		t.Fatalf("templated token was masked: %q", got[5].Value)
	}
}

func TestSensitiveNameCoversHistorySetAndCredentialWords(t *testing.T) {
	for _, name := range []string{"Authorization", "set-cookie", "X-Auth-Token", "My-Service-ApiKey", "Client_Secret", "X-Password"} {
		if !SensitiveName(name) {
			t.Fatalf("%q should be sensitive", name)
		}
	}
	for _, name := range []string{"Content-Type", "Accept", "X-Request-Id"} {
		if SensitiveName(name) {
			t.Fatalf("%q should not be sensitive", name)
		}
	}
}

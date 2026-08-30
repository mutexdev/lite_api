package envsecrets

// A secret that cannot be decrypted must not arrive as a blank value.
//
// Hydrate skipped the failure and moved on, which left the variable holding the
// empty string ScrubValues put there before the state was written. That is
// indistinguishable from a secret the user never filled in: the request goes
// out with an empty Authorization header, fails at the server, and nothing
// anywhere says the value on disk is unreadable — which is what a rotated
// machine key, a copied data directory and a corrupted secrets file all look
// like.

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func secretEnvironmentForHydrate(name string, variableNames ...string) types.Environment {
	env := types.Environment{Name: name}
	for _, variableName := range variableNames {
		env.Variables = append(env.Variables, types.Variable{
			Name:     variableName,
			Value:    "",
			DataType: "string",
			Enabled:  true,
			Secret:   true,
		})
	}
	return env
}

func TestHydrateReportsSecretsItCouldNotDecrypt(t *testing.T) {
	dir := pinKey(t)
	environments := []types.Environment{
		secretEnvironmentForHydrate("Production", "token", "apiKey"),
		secretEnvironmentForHydrate("Staging", "token"),
	}
	stored := []EnvironmentEntry{
		{Name: "Production", Secrets: []VariableEntry{
			{Name: "token", Value: "$01:not-hexadecimal"},
			{Name: "apiKey", Value: "$99:unsupported-algorithm"},
		}},
		{Name: "Staging", Secrets: []VariableEntry{
			{Name: "token", Value: EncryptString(dir, "readable")},
		}},
	}

	err := Hydrate(dir, environments, stored)
	if err == nil {
		t.Fatal("Hydrate reported success for two secrets it could not decrypt; they arrive as blank values instead")
	}
	message := err.Error()
	for _, want := range []string{"2", "Production"} {
		if !strings.Contains(message, want) {
			t.Errorf("the aggregated error does not name %q: %s", want, message)
		}
	}
	if strings.Contains(message, "Staging") {
		t.Errorf("the aggregated error blames an environment that decrypted fine: %s", message)
	}

	// The readable secret still hydrates: failing the whole load over one bad
	// entry would take out the environments that are fine.
	if value, _ := environments[1].Variables[0].Value.(string); value != "readable" {
		t.Errorf("a readable secret was not hydrated: %#v", environments[1].Variables[0])
	}
}

func TestHydrateReportsNothingWhenEverySecretDecrypts(t *testing.T) {
	dir := pinKey(t)
	environments := []types.Environment{secretEnvironmentForHydrate("Production", "token")}
	stored := []EnvironmentEntry{{Name: "Production", Secrets: []VariableEntry{
		{Name: "token", Value: EncryptString(dir, "hunter2")},
	}}}

	if err := Hydrate(dir, environments, stored); err != nil {
		t.Fatalf("Hydrate reported a failure for a clean load: %v", err)
	}
	if value, _ := environments[0].Variables[0].Value.(string); value != "hunter2" {
		t.Fatalf("the secret was not hydrated: %#v", environments[0].Variables[0])
	}
}

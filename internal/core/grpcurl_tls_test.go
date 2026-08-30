package core

// The grpcurl command must reflect the EFFECTIVE TLS verification decision.
//
// grpcexec has its own tests for the generator honouring the bool it is given.
// This one covers the other half, which those cannot reach: that the App call
// site actually RESOLVES that bool against the app-level SSL preference rather
// than forwarding item.Settings.VerifyTLS. A generator that is correct and a
// call site that passes the wrong value still ships the bug.

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/prefs"
)

func TestGenerateGrpcurlCommandReflectsTheGlobalSSLPreference(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]

	state, err = app.CreateRequest(collection.ID, "grpc", "grpcurl tls")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]

	targetURL := "grpcs://api.example.test:50051"
	method := "helloworld.Greeter/SayHello"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Method: &method}); err != nil {
		t.Fatal(err)
	}

	// The request's own flag stays ON for the whole test — the point is that
	// the global preference alone changes the generated command.
	command, err := app.GenerateGrpcurlCommand(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(command, "-insecure") {
		t.Fatalf("verification is fully on, but the command disables it:\n%s", command)
	}

	preferences := state.Preferences
	preferences.Request.SSLVerification = prefs.BoolPtr(false)
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}

	command, err = app.GenerateGrpcurlCommand(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// The app would now dial with InsecureSkipVerify. A copied command that
	// still verifies would fail where the app succeeds, which is exactly the
	// confusion "copy as grpcurl" is supposed to remove.
	if !strings.Contains(command, "-insecure") {
		t.Errorf("the global SSL preference is off but the command still verifies:\n%s", command)
	}
}

package grpcexec

// The -insecure flag in the generated grpcurl command.
//
// "Copy as grpcurl" exists so a user can reproduce, outside the app, exactly
// what the app does. A command that verifies certificates when the app would
// not (or the reverse) is worse than no command at all: it sends someone
// debugging a handshake failure looking in the wrong place.
//
// The generator used to read item.Settings.VerifyTLS directly. That is only
// half the decision — the app-level SSL preference can turn verification off
// for a request whose own flag is on — so the effective value is now resolved
// by the caller and passed in.

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func grpcurlTestItem(t *testing.T, url string, requestVerifyTLS bool) types.RequestItem {
	t.Helper()
	item := types.NewRequestItem("grpcurl", "grpc", 1)
	item.URL = url
	item.Method = "helloworld.Greeter/SayHello"
	item.Settings.VerifyTLS = requestVerifyTLS
	return item
}

func generateGrpcurl(t *testing.T, url string, requestVerifyTLS, effectiveVerifyTLS bool) string {
	t.Helper()
	command, err := GenerateGrpcurlCommand(types.Collection{}, grpcurlTestItem(t, url, requestVerifyTLS), nil, effectiveVerifyTLS)
	if err != nil {
		t.Fatalf("GenerateGrpcurlCommand: %v", err)
	}
	return command
}

// THE REGRESSION. Global preference off, per-request flag on: the app runs
// this request with InsecureSkipVerify, so the command must say -insecure.
// The old generator saw only the request flag (true) and emitted a verifying
// command.
func TestGrpcurlHonoursTheEffectiveVerificationNotTheRequestFlag(t *testing.T) {
	command := generateGrpcurl(t, "grpcs://api.example.test:50051", true, false)
	if !strings.Contains(command, "-insecure") {
		t.Errorf("global preference off + request flag on produced a verifying command:\n%s", command)
	}
}

func TestGrpcurlInsecureFlagTracksTheResolvedValue(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		requestFlag, effective bool
		wantInsecure           bool
	}{
		// The effective value is the only input that decides the flag; the
		// request flag is listed to show it does NOT sway the outcome.
		{"both on", true, true, false},
		{"preference off, request on", true, false, true},
		{"request off (so effective off)", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := generateGrpcurl(t, "grpcs://api.example.test:50051", tc.requestFlag, tc.effective)
			if got := strings.Contains(command, "-insecure"); got != tc.wantInsecure {
				t.Errorf("-insecure present = %v, want %v:\n%s", got, tc.wantInsecure, command)
			}
		})
	}
}

// -insecure is a TLS flag and must not appear on a plaintext target, where
// grpcurl rejects it. Plaintext schemes get -plaintext instead, whatever the
// verification decision was.
func TestGrpcurlDoesNotAddInsecureToPlaintextTargets(t *testing.T) {
	for _, url := range []string{"grpc://api.example.test:50051", "api.example.test:50051"} {
		t.Run(url, func(t *testing.T) {
			command := generateGrpcurl(t, url, true, false)
			if strings.Contains(command, "-insecure") {
				t.Errorf("plaintext target got a TLS flag:\n%s", command)
			}
			if !strings.Contains(command, "-plaintext") {
				t.Errorf("plaintext target lost -plaintext:\n%s", command)
			}
		})
	}
}

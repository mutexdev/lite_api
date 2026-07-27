package core

import "testing"

// This one test stayed behind when the shared-state merge moved to
// internal/workspacestate: mergeOAuth2TokenDelta is OAuth2-specific and its
// value type is core's, so the test belongs beside the code rather than beside
// the generic merge it resembles.

func TestOAuth2DeltaKeepsOtherProcessChangesAndDeletes(t *testing.T) {
	base := map[string]oauth2TokenResponse{"a": {AccessToken: "old"}, "delete": {AccessToken: "gone"}}
	current := map[string]oauth2TokenResponse{"a": {AccessToken: "mine"}, "new": {AccessToken: "new"}}
	disk := map[string]oauth2TokenResponse{"a": {AccessToken: "other"}, "delete": {AccessToken: "gone"}, "other": {AccessToken: "keep"}}
	merged := mergeOAuth2TokenDelta(base, current, disk)
	if merged["a"].AccessToken != "mine" || merged["new"].AccessToken != "new" || merged["other"].AccessToken != "keep" {
		t.Fatalf("oauth merge lost updates: %+v", merged)
	}
	if _, ok := merged["delete"]; ok {
		t.Fatal("oauth delete resurrected")
	}
}

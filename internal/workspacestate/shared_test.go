package workspacestate

import (
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/types"
	"os"
	"strings"
	"testing"
)

const sharedStateSecretSentinel = "shared-state-secret-sentinel"
const sharedStateCookieSentinel = "shared-state-cookie-sentinel"
const sharedStateProxySentinel = "shared-state-proxy-password-sentinel"

func TestSharedAppStateScrubsSecretsEncryptsCookiesAndReadsIndependently(t *testing.T) {
	dir := t.TempDir()
	state := types.AppState{
		Preferences:   types.Preferences{Theme: "dark", Proxy: types.ProxyPreferences{Config: types.ProxyConfig{Auth: types.ProxyAuthConfig{Password: sharedStateProxySentinel}}}},
		FeatureLedger: []types.Feature{{ID: "feature", Name: "Feature"}},
		GlobalEnvironments: []types.Environment{{ID: "global", Variables: []types.Variable{
			{ID: "secret", Name: "token", Value: sharedStateSecretSentinel, Secret: true},
			{ID: "public", Name: "region", Value: "us-central"},
		}}},
		Notifications: []types.Notification{{ID: "notice", Message: "Ready"}},
		Cookies:       []types.CookieEntry{{ID: "cookie", Name: "session", Value: sharedStateCookieSentinel}},
	}

	shared, err := ProjectSharedAppState(state, dir)
	if err != nil {
		t.Fatal(err)
	}
	if shared.GlobalEnvironments[0].Variables[0].Value != "" || shared.GlobalEnvironments[0].Variables[1].Value != "us-central" || shared.Cookies[0].Value == sharedStateCookieSentinel {
		t.Fatalf("shared projection was not safely transformed: %+v", shared)
	}
	if decrypted, err := envsecrets.DecryptString(dir, shared.Cookies[0].Value); err != nil || decrypted != sharedStateCookieSentinel {
		t.Fatalf("cookie was not encrypted with canonical data dir: decrypted=%q err=%v", decrypted, err)
	}
	if err := WriteSharedAppState(dir, shared); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SharedAppStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sharedStateSecretSentinel) || strings.Contains(string(raw), sharedStateCookieSentinel) || strings.Contains(string(raw), sharedStateProxySentinel) || !strings.Contains(string(raw), "us-central") {
		t.Fatalf("shared artifact leaked plaintext: %s", raw)
	}
	stored, err := ReadSharedAppState(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored.GlobalEnvironments[0].Variables[1].Value = "changed"
	reloaded, err := ReadSharedAppState(dir)
	if err != nil || reloaded.GlobalEnvironments[0].Variables[1].Value != "us-central" || reloaded.Preferences.Proxy.Config.Auth.Password != sharedStateProxySentinel {
		t.Fatalf("shared state reader aliases persisted data: %+v err=%v", reloaded, err)
	}
	info, err := os.Stat(SharedAppStatePath(dir))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("shared artifact permissions: info=%+v err=%v", info, err)
	}
}

func TestSharedAppStateEncryptsDollarPlaintextAndRejectsBadCiphertext(t *testing.T) {
	dir := t.TempDir()
	plaintext := "$this-is-plaintext-not-ciphertext"
	state := types.AppState{Preferences: types.Preferences{Proxy: types.ProxyPreferences{Config: types.ProxyConfig{Auth: types.ProxyAuthConfig{Password: plaintext}}}}, Cookies: []types.CookieEntry{{ID: "cookie", Value: plaintext}}}
	shared, err := ProjectSharedAppState(state, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSharedAppState(dir, shared); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SharedAppStatePath(dir))
	if err != nil || strings.Contains(string(raw), plaintext) {
		t.Fatalf("dollar-prefixed plaintext leaked: %s err=%v", raw, err)
	}
	runtime, err := ReadSharedAppState(dir)
	if err != nil || runtime.Preferences.Proxy.Config.Auth.Password != plaintext || runtime.Cookies[0].Value != plaintext {
		t.Fatalf("runtime decrypt failed: %+v err=%v", runtime, err)
	}
	corrupt := strings.Replace(string(raw), "$01:", "$99:", 1)
	if err := os.WriteFile(SharedAppStatePath(dir), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSharedAppState(dir); err == nil {
		t.Fatal("invalid shared ciphertext accepted")
	}

	crafted := "$01:" + strings.Repeat("00", 16)
	state = types.AppState{Preferences: types.Preferences{Proxy: types.ProxyPreferences{Config: types.ProxyConfig{Auth: types.ProxyAuthConfig{Password: crafted}}}}, Cookies: []types.CookieEntry{{ID: "cookie", Value: crafted}}}
	shared, err = ProjectSharedAppState(state, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSharedAppState(dir, shared); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(SharedAppStatePath(dir))
	if err != nil || strings.Contains(string(raw), crafted) {
		t.Fatalf("invalid cipher-shaped plaintext leaked: %s err=%v", raw, err)
	}
	runtime, err = ReadSharedAppState(dir)
	if err != nil || runtime.Preferences.Proxy.Config.Auth.Password != crafted || runtime.Cookies[0].Value != crafted {
		t.Fatalf("invalid cipher-shaped plaintext did not round trip: %+v err=%v", runtime, err)
	}
}

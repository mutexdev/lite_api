package envsecrets

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// This package decides what leaves memory. Two properties dominate everything
// else here: a secret must ROUND TRIP, because a value that encrypts but does
// not decrypt is a value the user has permanently lost; and a secret must not
// appear in the bytes written to disk.
//
// The key is derived from the machine unless LITEAPI_SECRET_KEY is set, so
// every test pins it — otherwise these would exercise whatever identity the
// host happens to report.

func pinKey(t *testing.T) string {
	t.Helper()
	t.Setenv("LITEAPI_SECRET_KEY", "envsecrets-test-key")
	return t.TempDir()
}

func TestASecretRoundTrips(t *testing.T) {
	dir := pinKey(t)
	for _, plain := range []string{
		"hunter2",
		"",
		"a value with spaces and = signs",
		strings.Repeat("x", 4096),
		"unicode é中文 \U0001f600",
		"multi\nline\nvalue",
	} {
		encoded := EncryptString(dir, plain)
		got, err := DecryptString(dir, encoded)
		if err != nil {
			t.Errorf("decrypt(%q): %v", plain, err)
			continue
		}
		if got != plain {
			t.Errorf("round trip gave %q, want %q", got, plain)
		}
	}
}

// The stored form must not contain the plaintext. This is the whole point of
// the exercise, and it is worth asserting directly rather than inferring it
// from the fact that encryption was called.
func TestTheStoredFormDoesNotContainThePlaintext(t *testing.T) {
	dir := pinKey(t)
	const secret = "correct-horse-battery-staple"
	encoded := EncryptString(dir, secret)
	if strings.Contains(encoded, secret) {
		t.Errorf("the encrypted form still contains the plaintext: %q", encoded)
	}
	if encoded == secret {
		t.Error("the value was stored unchanged")
	}
}

// ENCRYPTION IS DETERMINISTIC, and this pins it as a known weakness rather than
// asserting the property I expected.
//
// EncryptString uses AES-CBC with an ALL-ZERO IV, so the same plaintext under
// the same key always produces the same ciphertext. An observer of the state
// file can therefore tell which entries share a value — two environments using
// the same token are visibly the same token — without decrypting anything.
//
// It is recorded rather than changed, and the reason is stronger than "the
// format is on disk". THE DETERMINISM IS LOAD-BEARING. That was established by
// building the fix and watching what broke, not by reasoning about it:
//
//   - secrets.json is only rewritten when its bytes change. With a random IV
//     the bytes change on every write, so twenty mutations that touched no
//     secret rewrote the file twenty times — churning a sensitive file and
//     waking the collection watcher each time.
//   - the workspace migration verifies its artifacts are IDEMPOTENT by
//     comparing what it wrote against what a second run produces. Random IVs
//     make that comparison fail by construction.
//
// So a "$02" with a per-value random IV is necessary but not sufficient. It
// also needs change detection that compares DECRYPTED content rather than
// ciphertext, in the persist path and in the migration's artifact check. That
// is a deliberate piece of work across three areas, not a swap of the cipher —
// which is exactly why it is not folded into a restructuring pass.
//
// The reader would keep accepting "$01" so existing files stay readable; the
// consequence to state when it happens is that values written by the new build
// cannot be read by an older one.
func TestEncryptionIsDeterministicWhichLeaksEquality(t *testing.T) {
	dir := pinKey(t)
	first := EncryptString(dir, "same value")
	second := EncryptString(dir, "same value")
	if first != second {
		t.Fatal("encryption is no longer deterministic; if that was intentional this test should be replaced by the opposite assertion, and the $01 format must still decrypt")
	}
	different := EncryptString(dir, "another value")
	if first == different {
		t.Error("two DIFFERENT values encrypted to the same bytes")
	}
	for _, encoded := range []string{first, second, different} {
		if _, err := DecryptString(dir, encoded); err != nil {
			t.Errorf("decrypt(%q): %v", encoded, err)
		}
	}
}

// A value encrypted under one key must not silently decrypt under another. A
// wrong answer here would hand a request a corrupted credential rather than an
// error the user can act on.
func TestADifferentKeyDoesNotDecrypt(t *testing.T) {
	dir := pinKey(t)
	encoded := EncryptString(dir, "hunter2")

	t.Setenv("LITEAPI_SECRET_KEY", "a-completely-different-key")
	got, err := DecryptString(dir, encoded)
	if err == nil && got == "hunter2" {
		t.Error("a value decrypted under the wrong key")
	}
}

func TestDecryptRejectsMalformedCiphertextButPassesPlaintextThrough(t *testing.T) {
	dir := pinKey(t)

	// A "$" prefix claims to be ciphertext, so a malformed one is an error.
	for _, bad := range []string{"$01:not-hex", "$99:abcd", "$nocolon"} {
		if _, err := DecryptString(dir, bad); err == nil {
			t.Errorf("%q was accepted as ciphertext", bad)
		}
	}

	// Anything WITHOUT the prefix is returned unchanged. That is the migration
	// path, not an oversight: secrets written before encryption existed are
	// plaintext in the file, and rejecting them would make those environments
	// unreadable rather than merely unprotected.
	for _, plain := range []string{"hunter2", "not base64 at all !!!", "YWJj"} {
		got, err := DecryptString(dir, plain)
		if err != nil {
			t.Errorf("unprefixed %q was rejected: %v", plain, err)
		}
		if got != plain {
			t.Errorf("unprefixed %q came back as %q", plain, got)
		}
	}
}

// ScrubValues removes secret VALUES while keeping the entries. Dropping the
// whole variable would lose the user's key names as well, and they would have
// to remember what the environment used to define.
func TestScrubValuesKeepsTheNamesAndDropsTheValues(t *testing.T) {
	environments := []types.Environment{{
		Name: "prod",
		Variables: []types.Variable{
			{Name: "token", Value: "s3cret", Secret: true, Enabled: true},
			{Name: "host", Value: "example.test", Enabled: true},
		},
	}}
	got := ScrubValues(environments)
	if len(got) != 1 || len(got[0].Variables) != 2 {
		t.Fatalf("scrub changed the shape: %+v", got)
	}
	var secret, plain types.Variable
	for _, v := range got[0].Variables {
		if v.Secret {
			secret = v
		} else {
			plain = v
		}
	}
	if secret.Name != "token" {
		t.Errorf("the secret variable lost its name: %+v", secret)
	}
	if fmt := ValueToString(secret.Value); fmt == "s3cret" {
		t.Error("the secret value survived scrubbing")
	}
	if got := ValueToString(plain.Value); got != "example.test" {
		t.Errorf("a NON-secret value was scrubbed too: %q", got)
	}
}

// The scrub must not mutate its input. The caller still holds the live state
// and goes on serving those values to requests.
func TestScrubValuesDoesNotMutateItsInput(t *testing.T) {
	environments := []types.Environment{{
		Name:      "prod",
		Variables: []types.Variable{{Name: "token", Value: "s3cret", Secret: true}},
	}}
	_ = ScrubValues(environments)
	if got := ValueToString(environments[0].Variables[0].Value); got != "s3cret" {
		t.Errorf("the input was scrubbed in place; the live state now reads %q", got)
	}
}

// Cookie values are encrypted on the way out and restored on the way back, and
// the pair must agree — a cookie that does not survive the round trip logs the
// user out of whatever it authenticated.
func TestCookieValuesRoundTrip(t *testing.T) {
	dir := pinKey(t)
	cookies := []types.CookieEntry{
		{Name: "session", Value: "abc123", Domain: "example.test"},
		{Name: "empty", Value: "", Domain: "example.test"},
	}
	stored := EncryptCookieValues(dir, cookies)
	if stored[0].Value == "abc123" {
		t.Error("the cookie value was stored in the clear")
	}
	back := DecryptCookieValues(dir, stored)
	if len(back) != len(cookies) {
		t.Fatalf("got %d cookies back, want %d", len(back), len(cookies))
	}
	for i := range cookies {
		if back[i].Value != cookies[i].Value {
			t.Errorf("cookie %q came back as %q, want %q", cookies[i].Name, back[i].Value, cookies[i].Value)
		}
		if back[i].Domain != cookies[i].Domain {
			t.Errorf("cookie %q lost its domain", cookies[i].Name)
		}
	}
}

// StateForStorage is what the workspace migration checksums and what the
// persist path writes. It must strip secrets and reset the per-instance
// revision, which is documented as never surviving a restart.
func TestStateForStorageStripsSecretsAndTheRevision(t *testing.T) {
	dir := pinKey(t)
	state := types.AppState{
		Revision: 42,
		Workspaces: []types.Workspace{{
			Name: "ws",
			Collections: []types.Collection{{
				Name: "api",
				Environments: []types.Environment{{
					Name:      "prod",
					Variables: []types.Variable{{Name: "token", Value: "s3cret", Secret: true}},
				}},
			}},
		}},
	}
	stored := StateForStorage(state, dir)

	if stored.Revision != 0 {
		t.Errorf("Revision = %d, want 0 — it is per-instance and must not survive a restart", stored.Revision)
	}
	if len(stored.Workspaces) != 1 || len(stored.Workspaces[0].Collections) != 1 {
		t.Fatalf("the state shape changed: %+v", stored)
	}
	for _, env := range stored.Workspaces[0].Collections[0].Environments {
		for _, v := range env.Variables {
			if v.Secret && ValueToString(v.Value) == "s3cret" {
				t.Error("a secret value survived into the stored state")
			}
		}
	}
	// And the caller's state is untouched.
	if state.Revision != 42 {
		t.Error("StateForStorage mutated the caller's revision")
	}
	original := state.Workspaces[0].Collections[0].Environments[0].Variables[0]
	if ValueToString(original.Value) != "s3cret" {
		t.Error("StateForStorage scrubbed the live state in place")
	}
}

func TestValueToStringHandlesTheInterfaceTypesItReceives(t *testing.T) {
	for value, want := range map[interface{}]string{
		"text": "text",
		42:     "42",
		true:   "true",
		nil:    "",
	} {
		if got := ValueToString(value); got != want {
			t.Errorf("ValueToString(%#v) = %q, want %q", value, got, want)
		}
	}
}

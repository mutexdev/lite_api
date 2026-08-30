package core

// The MCP bearer token: where it lives, that it survives, and who can read it.
//
// All three properties are load-bearing and none of them is visible from the
// call site. A token regenerated on every call would break an already-paired
// agent with an authentication failure that names nothing; a token written into
// state.json would be copied into every bug report; a token written 0644 would
// be readable by every account on a shared machine.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMCPTokenIsCreatedOnDemandAndStaysStable(t *testing.T) {
	app := newAppForTest(t)

	path := app.mcpTokenPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the token file exists before anything asked for a token (stat error: %v)", err)
	}

	first, err := app.mcpToken()
	if err != nil {
		t.Fatalf("mcpToken: %v", err)
	}
	if strings.TrimSpace(first) == "" {
		t.Fatal("the generated token is empty")
	}
	// 32 random bytes, hex-encoded.
	if len(first) != mcpTokenBytes*2 {
		t.Errorf("token is %d characters, want %d", len(first), mcpTokenBytes*2)
	}

	second, err := app.mcpToken()
	if err != nil {
		t.Fatalf("second mcpToken: %v", err)
	}
	if second != first {
		t.Errorf("the token changed between calls (%q then %q); every already-paired agent would start failing authentication", first, second)
	}

	// And across App instances over the same data directory, which is what a
	// restart is.
	restarted := newAppInDirForTest(t, app.dataDir)
	third, err := restarted.mcpToken()
	if err != nil {
		t.Fatalf("mcpToken after restart: %v", err)
	}
	if third != first {
		t.Errorf("the token changed across a restart (%q then %q)", first, third)
	}
}

// Two callers racing for a token that does not exist yet must agree on one
// value. Without the mutex both would generate, both would write, and the
// loser's token — already handed to a caller — would authenticate nobody.
func TestMCPTokenIsStableUnderConcurrentFirstUse(t *testing.T) {
	app := newAppForTest(t)

	const callers = 8
	tokens := make(chan string, callers)
	for i := 0; i < callers; i++ {
		go func() {
			token, err := app.mcpToken()
			if err != nil {
				tokens <- "error: " + err.Error()
				return
			}
			tokens <- token
		}()
	}
	first := <-tokens
	for i := 1; i < callers; i++ {
		if got := <-tokens; got != first {
			t.Fatalf("concurrent callers got different tokens: %q and %q", first, got)
		}
	}
	if strings.HasPrefix(first, "error: ") {
		t.Fatal(first)
	}
}

// The token file is owner-only, and the token is NOT in state.json.
func TestMCPTokenFileIsPrivateAndOutsideState(t *testing.T) {
	app := newAppForTest(t)

	token, err := app.mcpToken()
	if err != nil {
		t.Fatalf("mcpToken: %v", err)
	}

	info, err := os.Stat(app.mcpTokenPath())
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if runtime.GOOS != "windows" {
		// Windows does not carry POSIX mode bits; everywhere else this is the
		// difference between a credential and a public file.
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("token file mode is %04o, want 0600", mode)
		}
	}

	// The state file must not contain it. Checked as raw bytes rather than by
	// walking the struct: the point is that no field, present or added later,
	// carries the value into that file. GetState first, because a fresh App
	// has not written state.json until something asks it to be ready.
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}
	stateBytes, err := os.ReadFile(filepath.Join(app.dataDir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	if strings.Contains(string(stateBytes), token) {
		t.Error("the MCP token was written into state.json")
	}

	// Same check against the marshalled preferences, which is what a support
	// bundle or a settings export would carry.
	preferences, err := json.Marshal(app.state.Preferences)
	if err != nil {
		t.Fatalf("marshal preferences: %v", err)
	}
	if strings.Contains(string(preferences), token) {
		t.Error("the MCP token appears in the marshalled preferences")
	}
}

// A truncated or empty token file heals rather than authenticating nobody
// forever: an empty string would otherwise be returned as "the token", and
// every request carrying it would be rejected with no way for the user to tell
// why.
func TestMCPTokenRegeneratesFromAnEmptyFile(t *testing.T) {
	app := newAppForTest(t)
	if err := os.MkdirAll(app.dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(app.mcpTokenPath(), []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write empty token file: %v", err)
	}

	token, err := app.mcpToken()
	if err != nil {
		t.Fatalf("mcpToken: %v", err)
	}
	if strings.TrimSpace(token) == "" {
		t.Fatal("an empty token file produced an empty token")
	}
}

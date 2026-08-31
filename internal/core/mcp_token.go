package core

// The bearer token that authenticates MCP clients, and the one file it lives
// in.
//
// IT IS NOT IN state.json, and that is the whole point of this file existing
// rather than a field on MCPPreferences. state.json is rewritten on every
// mutation, read by the recovery tooling, pasted into bug reports, and written
// with 0644 into a directory the user is invited to sync between machines. A
// long-lived credential that grants read access to every collection — auth
// blocks, history bodies, environment names — does not belong in any of that.
// It lives beside state.json in its own 0600 file instead, written through
// atomicfile.WritePrivate, which also tightens the parent directory.
//
// Generated on demand rather than at startup: an install that never enables the
// MCP interface never creates the file, so there is no credential lying around
// for a feature nobody switched on.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

// mcpTokenBytes is the entropy of a generated token. 32 bytes is well past the
// point where guessing is the attack anyone would try against a loopback
// listener, and hex-encoding keeps it copy-pasteable into a shell one-liner
// without quoting hazards.
const mcpTokenBytes = 32

func (a *App) mcpTokenPath() string {
	return filepath.Join(a.dataDir, "mcp-token")
}

// mcpToken returns this install's bearer token, creating it on first use.
//
// Stability is the contract: the token is baked into the `claude mcp add`
// command the user pastes into their agent's configuration, so returning a new
// value on a later call would silently break an already-paired client. Every
// path here therefore either reads the existing file or writes the one value it
// generated before returning it.
func (a *App) mcpToken() (string, error) {
	a.mcpTokenMu.Lock()
	defer a.mcpTokenMu.Unlock()

	path := a.mcpTokenPath()
	data, err := os.ReadFile(path)
	if err == nil {
		// Trimmed on read: the file ends with a newline so it prints cleanly
		// from a terminal, and a user who regenerates it by hand will leave one
		// too. A token with a stray newline would fail every request against a
		// server that compares the raw bytes.
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
		// An empty or whitespace-only file is treated as absent rather than as
		// a token, so a truncated write self-heals instead of authenticating
		// nobody forever.
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	raw := make([]byte, mcpTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := atomicfile.WritePrivate(path, []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

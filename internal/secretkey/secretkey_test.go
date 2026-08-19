package secretkey

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// This derives the symmetric key protecting environment secrets and recovery
// snapshots. Two properties matter above all: it is DETERMINISTIC, because a
// key that changes between runs makes every previously written secret and
// snapshot permanently unreadable; and it is the right SHAPE for AES-256.

func TestAESKeyIsThirtyTwoBytes(t *testing.T) {
	if got := len(AESKey(t.TempDir())); got != sha256.Size {
		t.Errorf("key is %d bytes, want %d for AES-256", got, sha256.Size)
	}
}

// The same input must give the same key, every time and across calls. This is
// the property whose loss is unrecoverable: nothing written under the old key
// can be read back.
func TestAESKeyIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	first := AESKey(dir)
	for i := 0; i < 5; i++ {
		if got := AESKey(dir); !bytes.Equal(got, first) {
			t.Fatalf("call %d produced a different key; everything written under the first is now unreadable", i+2)
		}
	}
}

// The environment override wins, which is what makes the key reproducible on a
// machine that cannot report a stable id — and what lets tests pin one.
func TestTheEnvironmentOverrideDecidesTheKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LITEAPI_SECRET_KEY", "a-fixed-test-key")
	pinned := AESKey(dir)

	if got := RawKey(dir); got != "a-fixed-test-key" {
		t.Errorf("RawKey = %q, want the override", got)
	}
	// The same override must give the same key from a DIFFERENT data directory:
	// the override replaces the machine identity entirely.
	if got := AESKey(t.TempDir()); !bytes.Equal(got, pinned) {
		t.Error("the override did not fully determine the key; the data directory still influenced it")
	}
}

// A different override must give a different key, or the override would be
// decorative and every machine would share one key.
func TestADifferentOverrideGivesADifferentKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LITEAPI_SECRET_KEY", "first")
	first := AESKey(dir)
	t.Setenv("LITEAPI_SECRET_KEY", "second")
	if second := AESKey(dir); bytes.Equal(first, second) {
		t.Error("two different secret keys produced the same AES key")
	}
}

// Whitespace around the override is trimmed, so a value pasted with a trailing
// newline derives the same key as the same value typed by hand. Without this a
// user could write secrets under one key and read them back under another.
func TestTheOverrideIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LITEAPI_SECRET_KEY", "padded")
	clean := AESKey(dir)
	t.Setenv("LITEAPI_SECRET_KEY", "  padded\n")
	if padded := AESKey(dir); !bytes.Equal(clean, padded) {
		t.Error("a padded override derived a different key than the trimmed one")
	}
}

// A blank override is not a key. Falling through to the machine id is what
// keeps an exported-but-empty variable from silently pinning every install to
// the same key.
func TestABlankOverrideFallsThrough(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LITEAPI_SECRET_KEY", "   ")
	if got := RawKey(dir); got == "   " || got == "" {
		t.Errorf("RawKey = %q, want a fallback rather than the blank override", got)
	}
}

// The key is the SHA-256 of the raw key, not the raw key padded or truncated.
// Stated because the raw key is user-supplied text of any length and the
// relationship is otherwise invisible.
func TestAESKeyIsTheDigestOfTheRawKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LITEAPI_SECRET_KEY", "known-value")
	want := sha256.Sum256([]byte("known-value"))
	if got := AESKey(dir); !bytes.Equal(got, want[:]) {
		t.Errorf("AESKey is not sha256(RawKey)")
	}
}

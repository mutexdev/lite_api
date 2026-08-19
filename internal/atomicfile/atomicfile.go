// Package atomicfile writes a file so that readers never observe a partial one.
//
// Extracted from recovery_store.go, where it was defined but consumed from five
// other files — app.go's OAuth2 credential storage, the workspace registry, the
// window session file, the window lock and the workspace migration. Leaving it
// there would have made every one of those import internal/recovery for an
// atomic write, a dependency that says nothing true about the design.
package atomicfile

import (
	"os"
	"path/filepath"
)

// WritePrivate writes data to path atomically, with owner-only permissions on
// both the file and its parent directory.
//
// The sequence matters and each step is load-bearing:
//
//   - The parent is created AND re-chmodded. MkdirAll is a no-op on an existing
//     directory, so a pre-existing world-readable parent would otherwise keep
//     its mode and expose every secret written into it.
//   - The temp file is chmodded to 0600 before any bytes are written, not
//     after, so the content is never briefly readable by others.
//   - Sync before Rename. Rename is atomic with respect to readers, but on a
//     crash an un-synced temp file can be renamed into place while still
//     holding zeros — the rename is ordered, the data is not.
//   - The parent directory is synced after the rename, so the directory entry
//     itself survives a crash. That final sync is best-effort: a filesystem
//     that refuses to open a directory is not a reason to report a write that
//     did land as failed.
func WritePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Best-effort: on the success path the temp file has already been renamed away.
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

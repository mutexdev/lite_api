package atomicfile

import (
	"os"
	"path/filepath"
)

// Write writes data through a temporary file in the same directory,
// fsyncs it, then renames it into place. os.Rename within one filesystem is
// atomic, so a process killed at any point leaves either the previous file or
// the complete new one — never the truncated file a bare os.WriteFile can leave
// behind. Unlike WritePrivate it does not create or re-chmod the parent
// directory, so it can be used on directories whose mode is already meaningful.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Close is checked rather than deferred-and-ignored: US-003 found a
	// truncated copy reported as success because a write handle's Close error
	// was dropped.
	if err := f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

//go:build unix

package core

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFileExclusive takes an exclusive advisory lock on an open file and
// returns the release.
//
// Advisory, not mandatory: it coordinates LiteAPI windows with each other, not
// LiteAPI with an unrelated process. That is the whole requirement — two
// windows must not rewrite one workspace's recovery store at once.
//
// The returned release is safe to call even though closing the file already
// drops the lock, because callers defer both and the order between them is not
// worth reasoning about at every site.
func lockFileExclusive(f *os.File) (func(), error) {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return func() {}, err
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }, nil
}

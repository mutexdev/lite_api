//go:build unix

package core

import (
	"errors"

	"golang.org/x/sys/unix"
)

// workspaceWindowProcessAlive reports whether a pid is still running.
//
// Signal 0 performs the permission and existence checks without delivering
// anything. EPERM counts as alive: the process exists, it just belongs to
// another user — treating that as dead would let one user's stale lock be
// stolen from another's live window.
func workspaceWindowProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

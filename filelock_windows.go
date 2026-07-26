//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusive takes an exclusive lock on an open file and returns the
// release. See the unix build for what this is for.
//
// LockFileEx with LOCKFILE_EXCLUSIVE_LOCK and no LOCKFILE_FAIL_IMMEDIATELY
// blocks until the lock is available, matching flock(LOCK_EX). The range is the
// whole file, expressed as the maximum 64-bit length, which is the documented
// way to say "everything" — Windows locks byte ranges rather than descriptors.
//
// Unlike flock this lock is MANDATORY rather than advisory, so a second window
// is refused access rather than merely told to wait its turn. That is stricter
// than the unix behaviour, and stricter in the direction that matters here.
func lockFileExclusive(f *os.File) (func(), error) {
	handle := windows.Handle(f.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(
		handle,
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		^uint32(0),
		^uint32(0),
		overlapped,
	); err != nil {
		return func() {}, err
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
	}, nil
}

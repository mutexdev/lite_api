//go:build windows

package main

import "golang.org/x/sys/windows"

// workspaceWindowProcessAlive reports whether a pid is still running.
//
// Windows has no signal-0 equivalent, so this opens the process with the
// narrowest right that still proves existence and asks for its exit code.
// STILL_ACTIVE (259) means running.
//
// An OpenProcess failure is treated as NOT alive, which is the opposite of the
// unix EPERM case and deliberate: on Windows the common reason to be refused is
// that the pid no longer exists, and a stale lock that can never be broken is
// worse than one broken slightly early.
func workspaceWindowProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

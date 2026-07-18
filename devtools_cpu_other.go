//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package main

import "time"

func currentProcessCPUTime() (time.Duration, bool) {
	return 0, false
}

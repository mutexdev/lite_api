//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Cocoa
void liteapiInstallApplicationMenu(void);
*/
import "C"

func installNativeApplicationMenu(_ *App) {
	C.liteapiInstallApplicationMenu()
}

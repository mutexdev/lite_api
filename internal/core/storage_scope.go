package core

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// GetWebStorageScope returns an opaque, stable namespace for browser-local UI
// preferences. WebKit localStorage is shared by the app bundle identifier, so
// it must never use unscoped keys when users launch LiteAPI against distinct
// backend data directories (for example, isolated QA workspaces).
func (a *App) GetWebStorageScope() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("liteapi-web-storage/v1\x00" + filepath.Clean(a.dataDir)))
	return hex.EncodeToString(sum[:]), nil
}

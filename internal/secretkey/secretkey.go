// Package secretkey derives the symmetric key that protects data at rest:
// environment secrets and the recovery store's snapshots.
//
// It is its own package because two packages need the same key. Leaving it in
// the application package meant internal/recovery could not be extracted
// without either importing the application back — a cycle — or keeping a
// second copy of the derivation, which would silently make old snapshots
// unreadable the moment the two drifted.
package secretkey

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
)

var (
	environmentSecretMachineIDOnce  sync.Once
	environmentSecretMachineIDValue string
)

func AESKey(dataDir string) []byte {
	sum := sha256.Sum256([]byte(RawKey(dataDir)))
	return sum[:]
}

func RawKey(dataDir string) string {
	if key := strings.TrimSpace(os.Getenv("LITEAPI_SECRET_KEY")); key != "" {
		return key
	}
	if id := localMachineID(); id != "" {
		return id
	}
	if strings.TrimSpace(dataDir) != "" {
		return filepath.Clean(dataDir)
	}
	return "LiteAPI"
}

func localMachineID() string {
	environmentSecretMachineIDOnce.Do(func() {
		switch goruntime.GOOS {
		case "darwin":
			output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
			if err == nil {
				matches := regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`).FindStringSubmatch(string(output))
				if len(matches) == 2 {
					environmentSecretMachineIDValue = strings.TrimSpace(matches[1])
				}
			}
		case "linux":
			for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
				data, err := os.ReadFile(path)
				if err == nil && strings.TrimSpace(string(data)) != "" {
					environmentSecretMachineIDValue = strings.TrimSpace(string(data))
					break
				}
			}
		case "windows":
			output, err := exec.Command("wmic", "csproduct", "get", "uuid").Output()
			if err == nil {
				for _, line := range strings.Split(string(output), "\n") {
					line = strings.TrimSpace(line)
					if line != "" && !strings.EqualFold(line, "UUID") {
						environmentSecretMachineIDValue = line
						break
					}
				}
			}
		}
	})
	return environmentSecretMachineIDValue
}

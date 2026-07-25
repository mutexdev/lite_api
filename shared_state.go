package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sharedAppStateVersion = 1

// SharedAppState is the small, process-independent portion of AppState that
// every workspace window needs. Workspace content and tabs deliberately live
// in workspace-scoped state instead.
type SharedAppState struct {
	Version            int            `json:"version"`
	Preferences        Preferences    `json:"preferences"`
	FeatureLedger      []Feature      `json:"featureLedger"`
	GlobalEnvironments []Environment  `json:"globalEnvironments"`
	Notifications      []Notification `json:"notifications"`
	Cookies            []CookieEntry  `json:"cookies"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

func sharedAppStatePath(dataDir string) string {
	return filepath.Join(dataDir, "shared-state.json")
}

func ProjectSharedAppState(state AppState, dataDir string) (SharedAppState, error) {
	shared := SharedAppState{
		Version:            sharedAppStateVersion,
		Preferences:        encryptSharedPreferences(state.Preferences, dataDir),
		FeatureLedger:      state.FeatureLedger,
		GlobalEnvironments: scrubEnvironmentSecretValues(state.GlobalEnvironments),
		Notifications:      state.Notifications,
		// Cookie encryption must use the canonical application data directory;
		// using a workspace directory would make existing cookie ciphertext
		// undecryptable after the split.
		Cookies:   encryptSharedCookieValues(dataDir, state.Cookies),
		UpdatedAt: time.Now().UTC(),
	}
	return cloneSharedAppState(shared)
}

func (s SharedAppState) Validate() error {
	if s.Version != sharedAppStateVersion {
		return fmt.Errorf("unsupported shared state version %d", s.Version)
	}
	return nil
}

func WriteSharedAppState(dataDir string, shared SharedAppState) error {
	if err := shared.Validate(); err != nil {
		return err
	}
	shared.Preferences = encryptSharedPreferences(shared.Preferences, dataDir)
	shared.GlobalEnvironments = scrubEnvironmentSecretValues(shared.GlobalEnvironments)
	shared.Cookies = encryptSharedCookieValues(dataDir, shared.Cookies)
	data, err := json.MarshalIndent(shared, "", "  ")
	if err != nil {
		return err
	}
	return workspacePersistenceWriteAtomic(sharedAppStatePath(dataDir), data)
}

func ReadSharedAppState(dataDir string) (SharedAppState, error) {
	data, err := os.ReadFile(sharedAppStatePath(dataDir))
	if err != nil {
		return SharedAppState{}, err
	}
	var shared SharedAppState
	if err := json.Unmarshal(data, &shared); err != nil {
		return SharedAppState{}, fmt.Errorf("parse shared state: %w", err)
	}
	if err := shared.Validate(); err != nil {
		return SharedAppState{}, err
	}
	preferences, err := decryptSharedPreferences(shared.Preferences, dataDir)
	if err != nil {
		return SharedAppState{}, err
	}
	shared.Preferences = preferences
	cookies, err := decryptSharedCookieValues(dataDir, shared.Cookies)
	if err != nil {
		return SharedAppState{}, err
	}
	shared.Cookies = cookies
	return cloneSharedAppState(shared)
}

func encryptSharedPreferences(preferences Preferences, dataDir string) Preferences {
	password := preferences.Proxy.Config.Auth.Password
	if password != "" && !isStoredSharedSecret(dataDir, password) {
		preferences.Proxy.Config.Auth.Password = encryptEnvironmentSecretString(dataDir, password)
	}
	return preferences
}

func decryptSharedPreferences(preferences Preferences, dataDir string) (Preferences, error) {
	password := strings.TrimSpace(preferences.Proxy.Config.Auth.Password)
	if password == "" {
		return preferences, nil
	}
	if !isStoredSharedSecret(dataDir, password) {
		return Preferences{}, errors.New("shared proxy password ciphertext is invalid")
	}
	decrypted, err := decryptEnvironmentSecretString(dataDir, password)
	if err != nil {
		return Preferences{}, fmt.Errorf("decrypt shared proxy password: %w", err)
	}
	preferences.Proxy.Config.Auth.Password = decrypted
	return preferences, nil
}

func encryptSharedCookieValues(dataDir string, cookies []CookieEntry) []CookieEntry {
	if len(cookies) == 0 {
		return nil
	}
	encrypted := append([]CookieEntry(nil), cookies...)
	for i := range encrypted {
		value := strings.TrimSpace(encrypted[i].Value)
		if value != "" && !isStoredSharedSecret(dataDir, value) {
			encrypted[i].Value = encryptEnvironmentSecretString(dataDir, encrypted[i].Value)
		}
	}
	return encrypted
}

func decryptSharedCookieValues(dataDir string, cookies []CookieEntry) ([]CookieEntry, error) {
	if len(cookies) == 0 {
		return nil, nil
	}
	decrypted := append([]CookieEntry(nil), cookies...)
	for i := range decrypted {
		value := strings.TrimSpace(decrypted[i].Value)
		if value == "" {
			continue
		}
		if !isStoredSharedSecret(dataDir, value) {
			return nil, errors.New("shared cookie ciphertext is invalid")
		}
		plain, err := decryptEnvironmentSecretString(dataDir, value)
		if err != nil {
			return nil, fmt.Errorf("decrypt shared cookie: %w", err)
		}
		decrypted[i].Value = plain
	}
	return decrypted, nil
}

func isStoredSharedSecret(dataDir, value string) bool {
	algorithm, payload, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return false
	}
	switch algorithm {
	case "$00":
		return payload == ""
	case "$01":
		decoded, err := hex.DecodeString(payload)
		if err != nil || len(decoded) == 0 || len(decoded)%16 != 0 {
			return false
		}
		_, err = decryptEnvironmentSecretString(dataDir, value)
		return err == nil
	default:
		return false
	}
}

func cloneSharedAppState(shared SharedAppState) (SharedAppState, error) {
	data, err := json.Marshal(shared)
	if err != nil {
		return SharedAppState{}, errors.New("clone shared state: " + err.Error())
	}
	var cloned SharedAppState
	if err := json.Unmarshal(data, &cloned); err != nil {
		return SharedAppState{}, errors.New("clone shared state: " + err.Error())
	}
	return cloned, nil
}

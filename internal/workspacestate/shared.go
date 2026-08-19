package workspacestate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const sharedAppStateVersion = 1

// SharedAppState is the small, process-independent portion of AppState that
// every workspace window needs. Workspace content and tabs deliberately live
// in workspace-scoped state instead.
type SharedAppState struct {
	Version            int                  `json:"version"`
	Preferences        types.Preferences    `json:"preferences"`
	FeatureLedger      []types.Feature      `json:"featureLedger"`
	GlobalEnvironments []types.Environment  `json:"globalEnvironments"`
	Notifications      []types.Notification `json:"notifications"`
	Cookies            []types.CookieEntry  `json:"cookies"`
	UpdatedAt          time.Time            `json:"updatedAt"`
}

func SharedAppStatePath(dataDir string) string {
	return filepath.Join(dataDir, "shared-state.json")
}

func ProjectSharedAppState(state types.AppState, dataDir string) (SharedAppState, error) {
	shared := SharedAppState{
		Version:            sharedAppStateVersion,
		Preferences:        encryptSharedPreferences(state.Preferences, dataDir),
		FeatureLedger:      state.FeatureLedger,
		GlobalEnvironments: envsecrets.ScrubValues(state.GlobalEnvironments),
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
	shared.GlobalEnvironments = envsecrets.ScrubValues(shared.GlobalEnvironments)
	shared.Cookies = encryptSharedCookieValues(dataDir, shared.Cookies)
	data, err := json.MarshalIndent(shared, "", "  ")
	if err != nil {
		return err
	}
	return PersistenceWriteAtomic(SharedAppStatePath(dataDir), data)
}

func ReadSharedAppState(dataDir string) (SharedAppState, error) {
	data, err := os.ReadFile(SharedAppStatePath(dataDir))
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

func encryptSharedPreferences(preferences types.Preferences, dataDir string) types.Preferences {
	password := preferences.Proxy.Config.Auth.Password
	if password != "" && !isStoredSharedSecret(dataDir, password) {
		preferences.Proxy.Config.Auth.Password = envsecrets.EncryptString(dataDir, password)
	}
	return preferences
}

func decryptSharedPreferences(preferences types.Preferences, dataDir string) (types.Preferences, error) {
	password := strings.TrimSpace(preferences.Proxy.Config.Auth.Password)
	if password == "" {
		return preferences, nil
	}
	if !isStoredSharedSecret(dataDir, password) {
		return types.Preferences{}, errors.New("shared proxy password ciphertext is invalid")
	}
	decrypted, err := envsecrets.DecryptString(dataDir, password)
	if err != nil {
		return types.Preferences{}, fmt.Errorf("decrypt shared proxy password: %w", err)
	}
	preferences.Proxy.Config.Auth.Password = decrypted
	return preferences, nil
}

func encryptSharedCookieValues(dataDir string, cookies []types.CookieEntry) []types.CookieEntry {
	if len(cookies) == 0 {
		return nil
	}
	encrypted := append([]types.CookieEntry(nil), cookies...)
	for i := range encrypted {
		value := strings.TrimSpace(encrypted[i].Value)
		if value != "" && !isStoredSharedSecret(dataDir, value) {
			encrypted[i].Value = envsecrets.EncryptString(dataDir, encrypted[i].Value)
		}
	}
	return encrypted
}

func decryptSharedCookieValues(dataDir string, cookies []types.CookieEntry) ([]types.CookieEntry, error) {
	if len(cookies) == 0 {
		return nil, nil
	}
	decrypted := append([]types.CookieEntry(nil), cookies...)
	for i := range decrypted {
		value := strings.TrimSpace(decrypted[i].Value)
		if value == "" {
			continue
		}
		if !isStoredSharedSecret(dataDir, value) {
			return nil, errors.New("shared cookie ciphertext is invalid")
		}
		plain, err := envsecrets.DecryptString(dataDir, value)
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
		_, err = envsecrets.DecryptString(dataDir, value)
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

func MergeSharedDelta(base, current, existing SharedAppState) (SharedAppState, error) {
	result := existing
	preferences, err := mergePreferencesDelta(base.Preferences, current.Preferences, existing.Preferences)
	if err != nil {
		return SharedAppState{}, err
	}
	result.Preferences = preferences
	result.FeatureLedger = mergeSharedSlice(base.FeatureLedger, current.FeatureLedger, existing.FeatureLedger, func(value types.Feature) string { return value.ID })
	result.Notifications = mergeSharedSlice(base.Notifications, current.Notifications, existing.Notifications, func(value types.Notification) string { return value.ID })
	result.Cookies = mergeSharedSlice(base.Cookies, current.Cookies, existing.Cookies, func(value types.CookieEntry) string { return value.ID })
	result.GlobalEnvironments, err = mergeEnvironmentDelta(base.GlobalEnvironments, current.GlobalEnvironments, existing.GlobalEnvironments)
	if err != nil {
		return SharedAppState{}, err
	}
	result.Version = current.Version
	result.UpdatedAt = time.Now().UTC()
	return result, nil
}

func mergePreferencesDelta(base, current, disk types.Preferences) (types.Preferences, error) {
	encode := func(value types.Preferences) (map[string]any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		err = json.Unmarshal(data, &out)
		return out, err
	}
	var b, c, d map[string]any
	var err error
	if b, err = encode(base); err != nil {
		return types.Preferences{}, err
	}
	if c, err = encode(current); err != nil {
		return types.Preferences{}, err
	}
	if d, err = encode(disk); err != nil {
		return types.Preferences{}, err
	}
	data, err := json.Marshal(mergeJSONDelta(b, c, d))
	if err != nil {
		return types.Preferences{}, err
	}
	var result types.Preferences
	err = json.Unmarshal(data, &result)
	return result, err
}

func mergeSharedSlice[T any](base, current, disk []T, key func(T) string) []T {
	b := map[string]T{}
	c := map[string]T{}
	out := map[string]T{}
	for _, v := range base {
		b[key(v)] = v
	}
	for _, v := range current {
		c[key(v)] = v
	}
	for _, v := range disk {
		out[key(v)] = v
	}
	for k, v := range b {
		if next, ok := c[k]; !ok {
			delete(out, k)
		} else if !reflect.DeepEqual(v, next) {
			out[k] = next
		}
	}
	for k, v := range c {
		if _, ok := b[k]; !ok {
			out[k] = v
		}
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]T, 0, len(keys))
	for _, k := range keys {
		result = append(result, out[k])
	}
	return result
}

func mergeEnvironmentDelta(base, current, disk []types.Environment) ([]types.Environment, error) {
	merged := mergeSharedSlice(base, current, disk, func(value types.Environment) string { return scalar.FirstNonEmpty(value.ID, value.Name) })
	baseBy := map[string]types.Environment{}
	curBy := map[string]types.Environment{}
	diskBy := map[string]types.Environment{}
	for _, v := range base {
		baseBy[scalar.FirstNonEmpty(v.ID, v.Name)] = v
	}
	for _, v := range current {
		curBy[scalar.FirstNonEmpty(v.ID, v.Name)] = v
	}
	for _, v := range disk {
		diskBy[scalar.FirstNonEmpty(v.ID, v.Name)] = v
	}
	for i := range merged {
		k := scalar.FirstNonEmpty(merged[i].ID, merged[i].Name)
		b, ok := baseBy[k]
		c, cok := curBy[k]
		d, dok := diskBy[k]
		if ok && cok && dok {
			metadata, err := mergeEnvironmentMetadata(b, c, d)
			if err != nil {
				return nil, err
			}
			metadata.Variables = mergeSharedSlice(b.Variables, c.Variables, d.Variables, func(v types.Variable) string { return scalar.FirstNonEmpty(v.ID, v.Name) })
			merged[i] = metadata
		}
	}
	return merged, nil
}

func mergeJSONDelta(base, current, disk map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range disk {
		out[k] = v
	}
	keys := map[string]bool{}
	for k := range base {
		keys[k] = true
	}
	for k := range current {
		keys[k] = true
	}
	for k := range keys {
		b, bok := base[k]
		c, cok := current[k]
		d := disk[k]
		if bok && !cok {
			delete(out, k)
			continue
		}
		if !bok && cok {
			out[k] = c
			continue
		}
		bm, bmok := b.(map[string]any)
		cm, cmok := c.(map[string]any)
		dm, dmok := d.(map[string]any)
		if bmok && cmok && dmok {
			out[k] = mergeJSONDelta(bm, cm, dm)
			continue
		}
		if !reflect.DeepEqual(b, c) {
			out[k] = c
		}
	}
	return out
}

func mergeEnvironmentMetadata(base, current, disk types.Environment) (types.Environment, error) {
	encode := func(value types.Environment) (map[string]any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		delete(result, "variables")
		return result, nil
	}
	b, err := encode(base)
	if err != nil {
		return types.Environment{}, err
	}
	c, err := encode(current)
	if err != nil {
		return types.Environment{}, err
	}
	d, err := encode(disk)
	if err != nil {
		return types.Environment{}, err
	}
	data, err := json.Marshal(mergeJSONDelta(b, c, d))
	if err != nil {
		return types.Environment{}, err
	}
	var result types.Environment
	if err := json.Unmarshal(data, &result); err != nil {
		return types.Environment{}, err
	}
	return result, nil
}

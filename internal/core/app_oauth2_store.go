package core

// OAuth2 credentials at rest: the on-disk store, its encryption and the delta merge between windows.
//
// Split out of app_oauth2.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/envsecrets"
)

type oauth2CredentialsFile struct {
	Credentials []oauth2CredentialEntry `json:"credentials"`
}

type oauth2CredentialEntry struct {
	CacheKey string `json:"cacheKey"`
	Data     string `json:"data"`
}

type oauth2TokenStorage struct {
	AccessToken  string                 `json:"accessToken,omitempty"`
	IDToken      string                 `json:"idToken,omitempty"`
	RefreshToken string                 `json:"refreshToken,omitempty"`
	Values       map[string]interface{} `json:"values,omitempty"`
	CreatedAt    int64                  `json:"createdAt,omitempty"`
	ExpiresAt    int64                  `json:"expiresAt,omitempty"`
}

var oauth2CredentialsRemove = os.Remove

func (a *App) oauth2CredentialsPath() string {
	return filepath.Join(a.dataDir, "oauth2.json")
}

func (a *App) loadOAuth2Credentials() error {
	path := a.oauth2CredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var store oauth2CredentialsFile
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("parse oauth2.json: %w", err)
	}
	next := map[string]oauth2TokenResponse{}
	for _, entry := range store.Credentials {
		cacheKey := strings.TrimSpace(entry.CacheKey)
		if cacheKey == "" || strings.TrimSpace(entry.Data) == "" {
			continue
		}
		response, err := decryptOAuth2TokenResponse(a.dataDir, entry.Data)
		if err != nil {
			return fmt.Errorf("decrypt OAuth2 credential %s: %w", cacheKey, err)
		}
		next[cacheKey] = response
	}
	a.oauth2Mu.Lock()
	a.oauth2 = next
	a.oauth2Baseline = cloneOAuth2TokenMap(next)
	a.oauth2Mu.Unlock()
	return nil
}

func (a *App) storeOAuth2Credentials() error {
	a.oauth2Mu.Lock()
	local := map[string]oauth2TokenResponse{}
	for key, value := range a.oauth2 {
		local[key] = value
	}
	baseline := cloneOAuth2TokenMap(a.oauth2Baseline)
	// US-013. This function runs on every state persist, and everything below
	// is expensive: it reads oauth2.json, DECRYPTS every stored credential,
	// re-encrypts every merged credential, and writes the file. None of that
	// can change the result when this App's own OAuth2 state has not moved
	// since the last successful store, because the merge is a function of
	// (baseline, local, disk) and a no-op delta leaves disk exactly as it is.
	//
	// So the gate is on (baseline, local) alone. Deliberately not on disk: a
	// credential another window obtained is already on disk, and refusing to
	// rewrite it is the correct outcome, not a lost update.
	fingerprint, fingerprintErr := oauth2TokenMapFingerprint(baseline, local)
	unchanged := fingerprintErr == nil && fingerprint != "" && fingerprint == a.oauth2Fingerprint
	a.oauth2Mu.Unlock()
	if unchanged {
		return nil
	}
	disk := map[string]oauth2TokenResponse{}
	if data, err := os.ReadFile(a.oauth2CredentialsPath()); err == nil {
		var stored oauth2CredentialsFile
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("parse oauth2.json: %w", err)
		}
		for _, entry := range stored.Credentials {
			value, err := decryptOAuth2TokenResponse(a.dataDir, entry.Data)
			if err != nil {
				return fmt.Errorf("decrypt OAuth2 credential %s: %w", entry.CacheKey, err)
			}
			disk[entry.CacheKey] = value
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged := mergeOAuth2TokenDelta(baseline, local, disk)
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]oauth2CredentialEntry, 0, len(keys))
	for _, key := range keys {
		encoded, err := encryptOAuth2TokenResponse(a.dataDir, merged[key])
		if err != nil {
			return err
		}
		entries = append(entries, oauth2CredentialEntry{CacheKey: key, Data: encoded})
	}

	if len(entries) == 0 {
		path := a.oauth2CredentialsPath()
		if err := oauth2CredentialsRemove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else {
			directory, err := os.Open(filepath.Dir(path))
			if err != nil {
				return err
			}
			syncErr := directory.Sync()
			closeErr := directory.Close()
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	} else {
		data, err := json.MarshalIndent(oauth2CredentialsFile{Credentials: entries}, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicfile.WritePrivate(a.oauth2CredentialsPath(), data); err != nil {
			return err
		}
	}
	a.oauth2Mu.Lock()
	a.oauth2 = cloneOAuth2TokenMap(merged)
	a.oauth2Baseline = cloneOAuth2TokenMap(merged)
	// US-013. Recorded only after the write landed, and computed from the maps
	// that were just installed rather than from the ones read at entry — the
	// merge may have pulled in another window's credentials, and the gate must
	// describe the state we actually persisted.
	if next, err := oauth2TokenMapFingerprint(a.oauth2Baseline, a.oauth2); err == nil {
		a.oauth2Fingerprint = next
	} else {
		// Unhashable state simply disables the gate; the next store does the
		// full work rather than skipping on a stale fingerprint.
		a.oauth2Fingerprint = ""
	}
	a.oauth2Mu.Unlock()
	return nil
}

// oauth2TokenMapFingerprint hashes the (baseline, local) pair that determines
// whether storeOAuth2Credentials can change anything on disk.
//
// Both halves are needed: the merge is a delta of local against baseline, so
// two different baselines with the same local map produce different results —
// a credential present in baseline and absent from local is a DELETION, which
// is indistinguishable from "never existed" if only local is hashed.
func oauth2TokenMapFingerprint(baseline, local map[string]oauth2TokenResponse) (string, error) {
	digest := sha256.New()
	for _, values := range []map[string]oauth2TokenResponse{baseline, local} {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encoded, err := json.Marshal(values[key])
			if err != nil {
				return "", err
			}
			// Length-prefixed, so that a key ending where a value begins cannot
			// collide with a different split of the same bytes.
			//
			// hash.Hash.Write is documented never to return an error, which is
			// why these are discarded rather than propagated.
			_, _ = fmt.Fprintf(digest, "%d:%s%d:", len(key), key, len(encoded))
			_, _ = digest.Write(encoded)
		}
		// Separator between the baseline and local halves, so that moving a key
		// from one to the other changes the digest.
		_, _ = digest.Write([]byte{0})
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func cloneOAuth2TokenMap(values map[string]oauth2TokenResponse) map[string]oauth2TokenResponse {
	out := map[string]oauth2TokenResponse{}
	for k, v := range values {
		out[k] = v
	}
	return out
}

func mergeOAuth2TokenDelta(base, current, disk map[string]oauth2TokenResponse) map[string]oauth2TokenResponse {
	out := cloneOAuth2TokenMap(disk)
	for k, b := range base {
		c, ok := current[k]
		if !ok {
			delete(out, k)
		} else if !reflect.DeepEqual(b, c) {
			out[k] = c
		}
	}
	for k, c := range current {
		if _, ok := base[k]; !ok {
			out[k] = c
		}
	}
	return out
}

func encryptOAuth2TokenResponse(dataDir string, response oauth2TokenResponse) (string, error) {
	data, err := json.Marshal(oauth2TokenStorageFromResponse(response))
	if err != nil {
		return "", fmt.Errorf("encode OAuth2 credentials: %w", err)
	}
	return envsecrets.EncryptString(dataDir, string(data)), nil
}

func decryptOAuth2TokenResponse(dataDir, encoded string) (oauth2TokenResponse, error) {
	plain, err := envsecrets.DecryptString(dataDir, encoded)
	if err != nil {
		return oauth2TokenResponse{}, err
	}
	var storage oauth2TokenStorage
	if err := json.Unmarshal([]byte(plain), &storage); err != nil {
		return oauth2TokenResponse{}, fmt.Errorf("parse OAuth2 credentials: %w", err)
	}
	return oauth2TokenResponseFromStorage(storage), nil
}

func oauth2TokenStorageFromResponse(response oauth2TokenResponse) oauth2TokenStorage {
	values := map[string]interface{}{}
	for key, value := range response.Values {
		values[key] = value
	}
	storage := oauth2TokenStorage{
		AccessToken:  response.AccessToken,
		IDToken:      response.IDToken,
		RefreshToken: response.RefreshToken,
		Values:       values,
	}
	if !response.CreatedAt.IsZero() {
		storage.CreatedAt = response.CreatedAt.UnixMilli()
	}
	if !response.ExpiresAt.IsZero() {
		storage.ExpiresAt = response.ExpiresAt.UnixMilli()
	}
	return storage
}

func oauth2CredentialVariablesFromCache(cache map[string]oauth2TokenResponse) map[string]interface{} {
	variables := map[string]interface{}{}
	for cacheKey, response := range cache {
		credentialsID := oauth2CacheCredentialsID(cacheKey)
		for key, value := range response.credentialValues() {
			variables["$oauth2."+credentialsID+"."+key] = value
		}
	}
	return variables
}

func (a *App) oauth2CredentialVariablesSnapshot() map[string]interface{} {
	a.oauth2Mu.Lock()
	defer a.oauth2Mu.Unlock()
	return oauth2CredentialVariablesFromCache(a.oauth2)
}

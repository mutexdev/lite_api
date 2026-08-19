// Package envsecrets is environment secrets and cookie values AT REST: how they
// are encrypted, how they are stripped from state before it is written, and how
// they are put back on load.
//
// Its own package because more than one thing needs the same answers. The
// application stores and hydrates them; the workspace migration checksums the
// legacy state exactly as it would be stored, which means with secrets scrubbed
// and cookies encrypted. Leaving this in the application package is what stopped
// internal/workspacestate being extracted, and a second copy of the encryption
// would make everything already written unreadable the moment the two drifted.
package envsecrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/types"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/secretkey"
)

type WorkspaceEntry struct {
	Path         string             `json:"path"`
	Environments []EnvironmentEntry `json:"environments"`
}

type CollectionEntry struct {
	Path         string             `json:"path"`
	Environments []EnvironmentEntry `json:"environments"`
}

type File struct {
	Collections []CollectionEntry `json:"collections"`
	Workspaces  []WorkspaceEntry  `json:"workspaces,omitempty"`
}

type EnvironmentEntry struct {
	Name    string          `json:"name"`
	Secrets []VariableEntry `json:"secrets"`
}

type VariableEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func Hydrate(dataDir string, environments []types.Environment, storedEnvironments []EnvironmentEntry) {
	for envIndex := range environments {
		env := &environments[envIndex]
		var storedEnv *EnvironmentEntry
		for index := range storedEnvironments {
			if storedEnvironments[index].Name == env.Name {
				storedEnv = &storedEnvironments[index]
				break
			}
		}
		if storedEnv == nil {
			continue
		}
		secretValues := map[string]string{}
		for _, secret := range storedEnv.Secrets {
			secretValues[secret.Name] = secret.Value
		}
		for variableIndex := range env.Variables {
			variable := &env.Variables[variableIndex]
			if !variable.Secret || variable.Name == "" {
				continue
			}
			encoded, ok := secretValues[variable.Name]
			if !ok {
				continue
			}
			plain, err := DecryptString(dataDir, encoded)
			if err != nil {
				continue
			}
			variable.Value = ParseValue(plain, scalar.FirstNonEmpty(variable.DataType, variable.Type, "string"))
		}
	}
}

func UpsertWorkspace(store *File, dataDir string, workspace *types.Workspace) {
	if store == nil || workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return
	}
	workspacePath := NormalizedPath(workspace.Path)
	workspaceIndex := -1
	for index := range store.Workspaces {
		if store.Workspaces[index].Path == workspacePath {
			workspaceIndex = index
			break
		}
	}
	nextEnvironments := ForStorageList(dataDir, workspace.GlobalEnvironments)
	if len(nextEnvironments) == 0 {
		if workspaceIndex >= 0 {
			store.Workspaces = append(store.Workspaces[:workspaceIndex], store.Workspaces[workspaceIndex+1:]...)
		}
		return
	}
	entry := WorkspaceEntry{Path: workspacePath, Environments: nextEnvironments}
	if workspaceIndex >= 0 {
		store.Workspaces[workspaceIndex] = entry
		return
	}
	store.Workspaces = append(store.Workspaces, entry)
}

func UpsertCollection(store *File, dataDir string, collection *types.Collection) {
	if store == nil || collection == nil || strings.TrimSpace(collection.Path) == "" {
		return
	}
	collectionPath := NormalizedPath(collection.Path)
	collectionIndex := -1
	for index := range store.Collections {
		if store.Collections[index].Path == collectionPath {
			collectionIndex = index
			break
		}
	}
	nextEnvironments := ForStorageList(dataDir, collection.Environments)
	if len(nextEnvironments) == 0 {
		if collectionIndex >= 0 {
			store.Collections = append(store.Collections[:collectionIndex], store.Collections[collectionIndex+1:]...)
		}
		return
	}
	entry := CollectionEntry{Path: collectionPath, Environments: nextEnvironments}
	if collectionIndex >= 0 {
		store.Collections[collectionIndex] = entry
		return
	}
	store.Collections = append(store.Collections, entry)
}

func ForStorageList(dataDir string, environments []types.Environment) []EnvironmentEntry {
	nextEnvironments := make([]EnvironmentEntry, 0, len(environments))
	for _, env := range environments {
		secrets := ForStorage(dataDir, env)
		if len(secrets) == 0 {
			continue
		}
		nextEnvironments = append(nextEnvironments, EnvironmentEntry{Name: env.Name, Secrets: secrets})
	}
	return nextEnvironments
}

func ForStorage(dataDir string, env types.Environment) []VariableEntry {
	secrets := []VariableEntry{}
	for _, variable := range env.Variables {
		if !variable.Secret || strings.TrimSpace(variable.Name) == "" {
			continue
		}
		secrets = append(secrets, VariableEntry{
			Name:  variable.Name,
			Value: EncryptString(dataDir, ValueToString(variable.Value)),
		})
	}
	return secrets
}

func NormalizedPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func ValueToString(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		if reflect.TypeOf(value) != nil {
			kind := reflect.TypeOf(value).Kind()
			if kind == reflect.Map || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Struct {
				if data, err := json.Marshal(value); err == nil {
					return string(data)
				}
			}
		}
		return fmt.Sprint(value)
	}
}

func ParseValue(value, dataType string) interface{} {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "number":
		if strings.ContainsAny(value, ".eE") {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				return parsed
			}
		}
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	case "boolean":
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return value
}

func EncryptString(dataDir, plain string) string {
	block, err := aes.NewCipher(secretkey.AESKey(dataDir))
	if err != nil {
		return plain
	}
	padded := scripting.ScriptPKCS7Pad([]byte(plain), block.BlockSize())
	encrypted := make([]byte, len(padded))
	iv := make([]byte, block.BlockSize())
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	return "$01:" + hex.EncodeToString(encrypted)
}

func DecryptString(dataDir, encoded string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, "$") {
		return encoded, nil
	}
	algorithm, payload, ok := strings.Cut(encoded, ":")
	if !ok {
		return "", errors.New("invalid encrypted secret")
	}
	if algorithm == "$00" {
		return "", nil
	}
	if algorithm != "$01" {
		return "", fmt.Errorf("unsupported encrypted secret algorithm %s", algorithm)
	}
	raw, err := hex.DecodeString(payload)
	if err != nil {
		return "", err
	}
	plain, err := decryptAES256(raw, secretkey.AESKey(dataDir), make([]byte, aes.BlockSize))
	if err == nil {
		return plain, nil
	}
	key, iv := legacyAESKeyAndIV(secretkey.RawKey(dataDir))
	return decryptAES256(raw, key, iv)
}

func decryptAES256(raw, key, iv []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		return "", errors.New("invalid encrypted secret payload")
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw)
	unpadded, err := scripting.ScriptPKCS7Unpad(plain, block.BlockSize())
	if err != nil {
		return "", err
	}
	return string(unpadded), nil
}

func legacyAESKeyAndIV(password string) ([]byte, []byte) {
	derived := []byte{}
	var previous []byte
	for len(derived) < 48 {
		hasher := md5.New()
		if len(previous) > 0 {
			_, _ = hasher.Write(previous)
		}
		_, _ = hasher.Write([]byte(password))
		previous = hasher.Sum(nil)
		derived = append(derived, previous...)
	}
	return derived[:32], derived[32:48]
}

func StateForStorage(state types.AppState, dataDir string) types.AppState {
	scrubbed := stateWithoutCollectionSecrets(state)
	scrubbed = stateWithoutScratchCollections(scrubbed)
	scrubbed.Cookies = EncryptCookieValues(dataDir, scrubbed.Cookies)
	// See AppState.Revision: the counter is per-instance, so it must not
	// survive a restart or cross between windows via the shared state file.
	scrubbed.Revision = 0
	return scrubbed
}

func stateWithoutScratchCollections(state types.AppState) types.AppState {
	if len(state.Workspaces) == 0 {
		return state
	}
	scratchIDs := map[string]bool{}
	state.Workspaces = append([]types.Workspace(nil), state.Workspaces...)
	for wi := range state.Workspaces {
		workspace := &state.Workspaces[wi]
		if workspace.ScratchCollectionID != "" {
			scratchIDs[workspace.ScratchCollectionID] = true
		}
		nextCollections := make([]types.Collection, 0, len(workspace.Collections))
		for _, collection := range workspace.Collections {
			if collection.Scratch {
				scratchIDs[collection.ID] = true
				continue
			}
			nextCollections = append(nextCollections, collection)
		}
		workspace.Collections = nextCollections
		workspace.ScratchCollectionID = ""
		workspace.ScratchTempDirectory = ""
	}
	if len(state.OpenTabs) > 0 {
		nextTabs := make([]types.OpenTab, 0, len(state.OpenTabs))
		for _, tab := range state.OpenTabs {
			if tab.Transient || scratchIDs[tab.CollectionID] {
				continue
			}
			nextTabs = append(nextTabs, tab)
		}
		state.OpenTabs = nextTabs
		if len(state.OpenTabs) == 0 {
			state.ActiveTabID = ""
		} else {
			activeStillPresent := false
			for _, tab := range state.OpenTabs {
				if tab.ID == state.ActiveTabID {
					activeStillPresent = true
					break
				}
			}
			if !activeStillPresent {
				state.ActiveTabID = state.OpenTabs[len(state.OpenTabs)-1].ID
			}
		}
	}
	if len(state.ClosedTabs) > 0 {
		nextClosedTabs := make([]types.OpenTab, 0, len(state.ClosedTabs))
		for _, tab := range state.ClosedTabs {
			if tab.Transient || scratchIDs[tab.CollectionID] {
				continue
			}
			nextClosedTabs = append(nextClosedTabs, tab)
		}
		state.ClosedTabs = nextClosedTabs
	}
	return state
}

func stateWithoutCollectionSecrets(state types.AppState) types.AppState {
	scrubbed := state
	if len(state.Workspaces) == 0 {
		return scrubbed
	}
	scrubbed.Workspaces = append([]types.Workspace(nil), state.Workspaces...)
	for wi := range scrubbed.Workspaces {
		scrubbed.Workspaces[wi] = workspaceWithoutSecrets(scrubbed.Workspaces[wi])
		if len(state.Workspaces[wi].Collections) == 0 {
			continue
		}
		scrubbed.Workspaces[wi].Collections = append([]types.Collection(nil), state.Workspaces[wi].Collections...)
		for ci := range scrubbed.Workspaces[wi].Collections {
			scrubbed.Workspaces[wi].Collections[ci] = collectionWithoutSecrets(scrubbed.Workspaces[wi].Collections[ci])
		}
	}
	return scrubbed
}

func workspaceWithoutSecrets(workspace types.Workspace) types.Workspace {
	if len(workspace.GlobalEnvironments) == 0 {
		return workspace
	}
	workspace.GlobalEnvironments = ScrubValues(workspace.GlobalEnvironments)
	return workspace
}

func collectionWithoutSecrets(collection types.Collection) types.Collection {
	if len(collection.Environments) == 0 {
		return collection
	}
	collection.Environments = ScrubValues(collection.Environments)
	return collection
}

func EncryptCookieValues(dataDir string, cookies []types.CookieEntry) []types.CookieEntry {
	if len(cookies) == 0 {
		return cookies
	}
	encrypted := append([]types.CookieEntry(nil), cookies...)
	for index := range encrypted {
		if encrypted[index].Value == "" {
			continue
		}
		encrypted[index].Value = EncryptString(dataDir, encrypted[index].Value)
	}
	return encrypted
}

func DecryptCookieValues(dataDir string, cookies []types.CookieEntry) []types.CookieEntry {
	if len(cookies) == 0 {
		return cookies
	}
	decrypted := append([]types.CookieEntry(nil), cookies...)
	for index := range decrypted {
		if !strings.HasPrefix(strings.TrimSpace(decrypted[index].Value), "$") {
			continue
		}
		value, err := DecryptString(dataDir, decrypted[index].Value)
		if err == nil {
			decrypted[index].Value = value
		}
	}
	return decrypted
}

func ScrubValues(environments []types.Environment) []types.Environment {
	return bru.ScrubEnvironmentSecretValues(environments)
}

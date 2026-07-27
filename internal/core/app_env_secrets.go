package core

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/secretkey"
	"github.com/mutexdev/lite_api/internal/store/bru"
)

func (a *App) environmentSecretsPath() string {
	return filepath.Join(a.dataDir, "secrets.json")
}

func (a *App) readEnvironmentSecretsLocked() (environmentSecretsFile, error) {
	path := a.environmentSecretsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return environmentSecretsFile{}, nil
		}
		return environmentSecretsFile{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return environmentSecretsFile{}, nil
	}
	var store environmentSecretsFile
	if err := json.Unmarshal(data, &store); err != nil {
		return environmentSecretsFile{}, fmt.Errorf("parse secrets.json: %w", err)
	}
	if store.Collections == nil {
		store.Collections = []environmentSecretCollection{}
	}
	return store, nil
}

func (a *App) writeEnvironmentSecretsLocked(store environmentSecretsFile) error {
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	if store.Collections == nil {
		store.Collections = []environmentSecretCollection{}
	}
	if store.Workspaces == nil {
		store.Workspaces = []environmentSecretWorkspace{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	path := a.environmentSecretsPath()
	// US-013. Skip unchanged content. persistEnvironmentSecretsLocked runs on
	// the keystroke path, where secrets almost never change.
	//
	// The gate is an in-memory fingerprint of what this App last wrote, not a
	// re-read of the file, so the common case costs a hash rather than a read
	// plus a compare. The file is still read once — when the fingerprint is
	// empty, i.e. before this App has written it — so a process that starts up
	// and persists without touching a secret still does not rewrite the file.
	//
	// Skipping is SAFE UNDER MULTIPLE WINDOWS, and the direction matters: if
	// another window has rewritten secrets.json since our last write, our
	// fingerprint still describes content identical to what we would produce,
	// so skipping leaves their newer file intact. Writing is the operation that
	// could clobber; declining to write cannot.
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
	if a.secretsFingerprint != "" {
		if fingerprint == a.secretsFingerprint {
			return nil
		}
	} else if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, data) {
		a.secretsFingerprint = fingerprint
		return nil
	}
	// Atomic for the same reason state.json is: a half-written secrets.json is
	// every environment secret in the workspace, unrecoverable.
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		// Leave the fingerprint alone on failure. Recording it here would make
		// the next call skip a write that never landed.
		return err
	}
	a.secretsFingerprint = fingerprint
	return nil
}

func (a *App) prepareWorkspaceGlobalEnvironmentsLocked() (bool, error) {
	changed := false
	if len(a.state.Workspaces) == 0 {
		return false, nil
	}
	if len(a.state.GlobalEnvironments) > 0 {
		ws, err := a.findWorkspaceLocked(a.state.ActiveWorkspaceID)
		if err != nil {
			ws = &a.state.Workspaces[0]
		}
		if len(ws.GlobalEnvironments) == 0 {
			ws.GlobalEnvironments = append([]Environment(nil), a.state.GlobalEnvironments...)
			if ws.ActiveGlobalEnvironmentID == "" && len(ws.GlobalEnvironments) > 0 {
				ws.ActiveGlobalEnvironmentID = ws.GlobalEnvironments[0].ID
			}
		}
		a.state.GlobalEnvironments = nil
		changed = true
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		if strings.TrimSpace(ws.Path) != "" {
			loaded, err := readWorkspaceGlobalEnvironments(ws.Path)
			if err != nil {
				return changed, err
			}
			if len(loaded) > 0 {
				ws.GlobalEnvironments = bru.MergeEnvironments(ws.GlobalEnvironments, loaded)
			}
			if ws.ActiveGlobalEnvironmentID == "" || !scripting.WorkspaceHasGlobalEnvironment(*ws, ws.ActiveGlobalEnvironmentID) {
				migrated, err := migrateWorkspaceActiveGlobalEnvironmentFromConfig(ws)
				if err != nil {
					return changed, err
				}
				if migrated {
					changed = true
				}
			}
		}
		if !scripting.WorkspaceHasGlobalEnvironment(*ws, ws.ActiveGlobalEnvironmentID) {
			ws.ActiveGlobalEnvironmentID = ""
			changed = true
		}
	}
	return changed, nil
}

func (a *App) storeStateEnvironmentSecretsLocked() error {
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	for wi := range a.state.Workspaces {
		upsertWorkspaceEnvironmentSecrets(&store, a.dataDir, &a.state.Workspaces[wi])
		for ci := range a.state.Workspaces[wi].Collections {
			upsertCollectionEnvironmentSecrets(&store, a.dataDir, &a.state.Workspaces[wi].Collections[ci])
		}
	}
	return a.writeEnvironmentSecretsLocked(store)
}

func (a *App) storeCollectionEnvironmentSecretsLocked(collection *Collection) error {
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	upsertCollectionEnvironmentSecrets(&store, a.dataDir, collection)
	return a.writeEnvironmentSecretsLocked(store)
}

func (a *App) hydrateStateEnvironmentSecretsLocked() error {
	for wi := range a.state.Workspaces {
		if err := a.hydrateWorkspaceEnvironmentSecretsLocked(&a.state.Workspaces[wi]); err != nil {
			return err
		}
		for ci := range a.state.Workspaces[wi].Collections {
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&a.state.Workspaces[wi].Collections[ci]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) hydrateWorkspaceEnvironmentSecretsLocked(workspace *Workspace) error {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" || len(workspace.GlobalEnvironments) == 0 {
		return nil
	}
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	workspacePath := normalizedEnvironmentSecretPath(workspace.Path)
	var stored *environmentSecretWorkspace
	for index := range store.Workspaces {
		if store.Workspaces[index].Path == workspacePath {
			stored = &store.Workspaces[index]
			break
		}
	}
	if stored == nil {
		return nil
	}
	hydrateEnvironmentSecrets(a.dataDir, workspace.GlobalEnvironments, stored.Environments)
	return nil
}

func (a *App) hydrateCollectionEnvironmentSecretsLocked(collection *Collection) error {
	if collection == nil || strings.TrimSpace(collection.Path) == "" || len(collection.Environments) == 0 {
		return nil
	}
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	collectionPath := normalizedEnvironmentSecretPath(collection.Path)
	var stored *environmentSecretCollection
	for index := range store.Collections {
		if store.Collections[index].Path == collectionPath {
			stored = &store.Collections[index]
			break
		}
	}
	if stored == nil {
		return nil
	}
	hydrateEnvironmentSecrets(a.dataDir, collection.Environments, stored.Environments)
	return nil
}

func hydrateEnvironmentSecrets(dataDir string, environments []Environment, storedEnvironments []environmentSecretEnvironment) {
	for envIndex := range environments {
		env := &environments[envIndex]
		var storedEnv *environmentSecretEnvironment
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
			plain, err := decryptEnvironmentSecretString(dataDir, encoded)
			if err != nil {
				continue
			}
			variable.Value = parseEnvironmentSecretValue(plain, firstNonEmpty(variable.DataType, variable.Type, "string"))
		}
	}
}

func upsertWorkspaceEnvironmentSecrets(store *environmentSecretsFile, dataDir string, workspace *Workspace) {
	if store == nil || workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return
	}
	workspacePath := normalizedEnvironmentSecretPath(workspace.Path)
	workspaceIndex := -1
	for index := range store.Workspaces {
		if store.Workspaces[index].Path == workspacePath {
			workspaceIndex = index
			break
		}
	}
	nextEnvironments := environmentSecretsForStorageList(dataDir, workspace.GlobalEnvironments)
	if len(nextEnvironments) == 0 {
		if workspaceIndex >= 0 {
			store.Workspaces = append(store.Workspaces[:workspaceIndex], store.Workspaces[workspaceIndex+1:]...)
		}
		return
	}
	entry := environmentSecretWorkspace{Path: workspacePath, Environments: nextEnvironments}
	if workspaceIndex >= 0 {
		store.Workspaces[workspaceIndex] = entry
		return
	}
	store.Workspaces = append(store.Workspaces, entry)
}

func upsertCollectionEnvironmentSecrets(store *environmentSecretsFile, dataDir string, collection *Collection) {
	if store == nil || collection == nil || strings.TrimSpace(collection.Path) == "" {
		return
	}
	collectionPath := normalizedEnvironmentSecretPath(collection.Path)
	collectionIndex := -1
	for index := range store.Collections {
		if store.Collections[index].Path == collectionPath {
			collectionIndex = index
			break
		}
	}
	nextEnvironments := environmentSecretsForStorageList(dataDir, collection.Environments)
	if len(nextEnvironments) == 0 {
		if collectionIndex >= 0 {
			store.Collections = append(store.Collections[:collectionIndex], store.Collections[collectionIndex+1:]...)
		}
		return
	}
	entry := environmentSecretCollection{Path: collectionPath, Environments: nextEnvironments}
	if collectionIndex >= 0 {
		store.Collections[collectionIndex] = entry
		return
	}
	store.Collections = append(store.Collections, entry)
}

func environmentSecretsForStorageList(dataDir string, environments []Environment) []environmentSecretEnvironment {
	nextEnvironments := make([]environmentSecretEnvironment, 0, len(environments))
	for _, env := range environments {
		secrets := environmentSecretsForStorage(dataDir, env)
		if len(secrets) == 0 {
			continue
		}
		nextEnvironments = append(nextEnvironments, environmentSecretEnvironment{Name: env.Name, Secrets: secrets})
	}
	return nextEnvironments
}

func environmentSecretsForStorage(dataDir string, env Environment) []environmentSecretVariable {
	secrets := []environmentSecretVariable{}
	for _, variable := range env.Variables {
		if !variable.Secret || strings.TrimSpace(variable.Name) == "" {
			continue
		}
		secrets = append(secrets, environmentSecretVariable{
			Name:  variable.Name,
			Value: encryptEnvironmentSecretString(dataDir, environmentSecretValueToString(variable.Value)),
		})
	}
	return secrets
}

func normalizedEnvironmentSecretPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func environmentSecretValueToString(value interface{}) string {
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

func parseEnvironmentSecretValue(value, dataType string) interface{} {
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

func encryptEnvironmentSecretString(dataDir, plain string) string {
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

func decryptEnvironmentSecretString(dataDir, encoded string) (string, error) {
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
	plain, err := decryptEnvironmentSecretAES256(raw, secretkey.AESKey(dataDir), make([]byte, aes.BlockSize))
	if err == nil {
		return plain, nil
	}
	key, iv := environmentSecretLegacyAESKeyAndIV(secretkey.RawKey(dataDir))
	return decryptEnvironmentSecretAES256(raw, key, iv)
}

func decryptEnvironmentSecretAES256(raw, key, iv []byte) (string, error) {
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

func environmentSecretLegacyAESKeyAndIV(password string) ([]byte, []byte) {
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

func stateForStorage(state AppState, dataDir string) AppState {
	scrubbed := stateWithoutCollectionEnvironmentSecrets(state)
	scrubbed = stateWithoutScratchCollections(scrubbed)
	scrubbed.Cookies = encryptCookieValuesForStorage(dataDir, scrubbed.Cookies)
	// See AppState.Revision: the counter is per-instance, so it must not
	// survive a restart or cross between windows via the shared state file.
	scrubbed.Revision = 0
	return scrubbed
}

func stateWithoutScratchCollections(state AppState) AppState {
	if len(state.Workspaces) == 0 {
		return state
	}
	scratchIDs := map[string]bool{}
	state.Workspaces = append([]Workspace(nil), state.Workspaces...)
	for wi := range state.Workspaces {
		workspace := &state.Workspaces[wi]
		if workspace.ScratchCollectionID != "" {
			scratchIDs[workspace.ScratchCollectionID] = true
		}
		nextCollections := make([]Collection, 0, len(workspace.Collections))
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
		nextTabs := make([]OpenTab, 0, len(state.OpenTabs))
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
		nextClosedTabs := make([]OpenTab, 0, len(state.ClosedTabs))
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

func stateWithoutCollectionEnvironmentSecrets(state AppState) AppState {
	scrubbed := state
	if len(state.Workspaces) == 0 {
		return scrubbed
	}
	scrubbed.Workspaces = append([]Workspace(nil), state.Workspaces...)
	for wi := range scrubbed.Workspaces {
		scrubbed.Workspaces[wi] = workspaceWithoutEnvironmentSecrets(scrubbed.Workspaces[wi])
		if len(state.Workspaces[wi].Collections) == 0 {
			continue
		}
		scrubbed.Workspaces[wi].Collections = append([]Collection(nil), state.Workspaces[wi].Collections...)
		for ci := range scrubbed.Workspaces[wi].Collections {
			scrubbed.Workspaces[wi].Collections[ci] = collectionWithoutEnvironmentSecrets(scrubbed.Workspaces[wi].Collections[ci])
		}
	}
	return scrubbed
}

func workspaceWithoutEnvironmentSecrets(workspace Workspace) Workspace {
	if len(workspace.GlobalEnvironments) == 0 {
		return workspace
	}
	workspace.GlobalEnvironments = scrubEnvironmentSecretValues(workspace.GlobalEnvironments)
	return workspace
}

func collectionWithoutEnvironmentSecrets(collection Collection) Collection {
	if len(collection.Environments) == 0 {
		return collection
	}
	collection.Environments = scrubEnvironmentSecretValues(collection.Environments)
	return collection
}

func encryptCookieValuesForStorage(dataDir string, cookies []CookieEntry) []CookieEntry {
	if len(cookies) == 0 {
		return cookies
	}
	encrypted := append([]CookieEntry(nil), cookies...)
	for index := range encrypted {
		if encrypted[index].Value == "" {
			continue
		}
		encrypted[index].Value = encryptEnvironmentSecretString(dataDir, encrypted[index].Value)
	}
	return encrypted
}

func decryptCookieValuesForRuntime(dataDir string, cookies []CookieEntry) []CookieEntry {
	if len(cookies) == 0 {
		return cookies
	}
	decrypted := append([]CookieEntry(nil), cookies...)
	for index := range decrypted {
		if !strings.HasPrefix(strings.TrimSpace(decrypted[index].Value), "$") {
			continue
		}
		value, err := decryptEnvironmentSecretString(dataDir, decrypted[index].Value)
		if err == nil {
			decrypted[index].Value = value
		}
	}
	return decrypted
}

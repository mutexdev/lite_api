package core

// The on-disk cache that lets a collection reload without reparsing every file.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/prefs"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const collectionFileCacheVersion = 1

type collectionFileCacheStore struct {
	Version     int                                 `json:"version"`
	Collections map[string]collectionFileCacheEntry `json:"collections"`
}

type collectionFileCacheEntry struct {
	Fingerprint string     `json:"fingerprint"`
	Collection  Collection `json:"collection"`
	CachedAt    time.Time  `json:"cachedAt"`
}

func (a *App) readCollectionFromDiskCachedLocked(collectionPath string) (Collection, error) {
	preferences := prefs.Normalize(a.state.Preferences)
	if !preferences.Cache.File.Enabled {
		return readCollectionFromDisk(collectionPath)
	}
	collectionPath = filepath.Clean(collectionPath)
	fingerprint, err := collectionFileCacheFingerprint(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	store := a.readCollectionFileCacheLocked()
	if entry, ok := store.Collections[collectionPath]; ok && entry.Fingerprint == fingerprint {
		collection := entry.Collection
		collection.Path = collectionPath
		return collection, nil
	}
	collection, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	store.Collections[collectionPath] = collectionFileCacheEntry{
		Fingerprint: fingerprint,
		Collection:  collection,
		CachedAt:    time.Now(),
	}
	if err := a.writeCollectionFileCacheLocked(store); err != nil {
		return Collection{}, err
	}
	return collection, nil
}

func (a *App) collectionFileCachePathLocked() string {
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	return filepath.Join(a.dataDir, "cache", "collections.json")
}

func (a *App) readCollectionFileCacheLocked() collectionFileCacheStore {
	store := collectionFileCacheStore{
		Version:     collectionFileCacheVersion,
		Collections: map[string]collectionFileCacheEntry{},
	}
	data, err := os.ReadFile(a.collectionFileCachePathLocked())
	if err != nil {
		return store
	}
	if err := json.Unmarshal(data, &store); err != nil || store.Version != collectionFileCacheVersion {
		return collectionFileCacheStore{
			Version:     collectionFileCacheVersion,
			Collections: map[string]collectionFileCacheEntry{},
		}
	}
	if store.Collections == nil {
		store.Collections = map[string]collectionFileCacheEntry{}
	}
	return store
}

func (a *App) writeCollectionFileCacheLocked(store collectionFileCacheStore) error {
	store.Version = collectionFileCacheVersion
	if store.Collections == nil {
		store.Collections = map[string]collectionFileCacheEntry{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	path := a.collectionFileCachePathLocked()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) fileCacheSizeLocked() (int64, error) {
	info, err := os.Stat(a.collectionFileCachePathLocked())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir() {
		return 0, nil
	}
	return info.Size(), nil
}

func collectionFileCacheFingerprint(collectionPath string) (string, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", collectionPath)
	}
	parts := []string{}
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			base := entry.Name()
			if path != collectionPath && (base == "node_modules" || base == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		parts = append(parts, fmt.Sprintf("%s:%d:%d:%x", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), sum[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(parts)
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

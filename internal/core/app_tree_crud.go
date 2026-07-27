package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/types"
)

func (a *App) CreateRequest(collectionID, requestType, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	presets := normalizeCollectionPresets(collection.Presets)
	requestType = normalizePresetRequestType(requestType)
	if requestType == "" {
		requestType = presets.RequestType
	}
	if requestType == "" {
		requestType = "http"
	}
	if name == "" {
		name = strings.ToUpper(requestType[:1]) + requestType[1:] + " request"
	}
	item := types.NewRequestItem(name, requestType, len(collection.Items)+1)
	if strings.TrimSpace(presets.RequestURL) != "" {
		item.URL = presets.RequestURL
	}
	if collection.Scratch {
		item.Transient = true
		item.FilePath = requestFilePath(*collection, item, requestFileExtensionForCollection(*collection))
	}
	collection.Items = append(collection.Items, item)
	if !collection.Scratch && strings.TrimSpace(collection.Path) != "" {
		// File-backed request identities are derived from their eventual path on
		// disk. Establish that identity before opening the tab so an internal
		// save, watcher refresh, or restart cannot strand the tab on a temporary
		// random ID.
		ensureRequestFilePaths(collection, requestFileExtensionForCollection(*collection))
		created := &collection.Items[len(collection.Items)-1]
		created.ID = deterministicID("request", filepath.Clean(created.FilePath))
		assignExampleIDs(created)
		created.Draft = true
		created.Transient = true
		item = *created
	}
	collection.UpdatedAt = time.Now()
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.openTabLocked(collection.ID, item.ID, "request")
	a.notify("info", "Request created: "+item.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateFolder(collectionID, parentFolderPath, folderName, directoryName string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		return AppState{}, errors.New("folder name is required")
	}
	if strings.TrimSpace(directoryName) == "" {
		directoryName = name
	}
	directoryName = bru.SanitizeName(directoryName)
	if !bru.ValidateName(directoryName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, directoryName))
	}
	parentPath, parentDisplayPath, err := collectionFolderParentPaths(collection, parentFolderPath)
	if err != nil {
		return AppState{}, err
	}
	if parentPath == "" && strings.Contains(strings.ToLower(strings.TrimSpace(directoryName)), "environments") {
		return AppState{}, errors.New("the folder name \"environments\" at the root of the collection is reserved in bruno")
	}
	if collectionHasChildFolder(collection, parentPath, directoryName) {
		return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	parentDir := filepath.Join(collection.Path, filepath.FromSlash(parentPath))
	if info, statErr := os.Stat(parentDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", parentDir)
	}
	folderPath := joinCollectionFolderPath(parentPath, directoryName)
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return AppState{}, errors.New("the directory already exists")
		}
		return AppState{}, err
	}
	folder := FolderConfig{
		Path:        folderPath,
		DisplayPath: joinCollectionFolderPath(parentDisplayPath, name),
		Name:        name,
		Seq:         nextCollectionFolderSeq(*collection, parentPath),
		Auth:        AuthConfig{Mode: "inherit"},
	}
	if err := a.writeFolderConfigLocked(collection, folder); err != nil {
		return AppState{}, err
	}
	collection.Folders = append(collection.Folders, folder)
	sortFoldersLikeBruno(collection.Folders)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	a.notify("success", "New folder created!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RenameFolder(collectionID, folderPath, folderName, directoryName string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		return AppState{}, errors.New("folder name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("folder name must be 255 characters or less")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	folder := collection.Folders[folderIndex]
	oldPath := normalizeFolderPathKey(folder.Path)
	oldDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	if oldPath == "" {
		return AppState{}, errors.New("folder path is required")
	}
	if strings.TrimSpace(directoryName) == "" {
		directoryName = name
	}
	directoryName = bru.SanitizeName(directoryName)
	if !bru.ValidateName(directoryName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, directoryName))
	}
	if strings.EqualFold(directoryName, "collection") || strings.EqualFold(directoryName, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	parentPath := normalizeFolderPathKey(parentFolderDisplayPath(oldPath))
	parentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(oldDisplayPath))
	newPath := joinCollectionFolderPath(parentPath, directoryName)
	newDisplayPath := joinCollectionFolderPath(parentDisplayPath, name)
	if newPath != oldPath && collectionHasChildFolder(collection, parentPath, directoryName) {
		return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	oldDir := filepath.Join(collection.Path, filepath.FromSlash(oldPath))
	newDir := filepath.Join(collection.Path, filepath.FromSlash(newPath))
	if !pathInside(collection.Path, oldDir) || !pathInside(collection.Path, newDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(oldDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", oldDir)
	}
	if newPath != oldPath {
		if _, err := os.Stat(newDir); err == nil {
			return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return AppState{}, err
		}
	}
	renamedFolder := folder
	renamedFolder.Name = name
	renamedFolder.DisplayPath = newDisplayPath
	if err := a.writeFolderConfigLocked(collection, FolderConfig{
		Path:         oldPath,
		DisplayPath:  oldDisplayPath,
		Name:         name,
		Seq:          renamedFolder.Seq,
		Headers:      renamedFolder.Headers,
		Variables:    renamedFolder.Variables,
		ResVariables: renamedFolder.ResVariables,
		Auth:         renamedFolder.Auth,
		PreScript:    renamedFolder.PreScript,
		PostScript:   renamedFolder.PostScript,
		Tests:        renamedFolder.Tests,
		Docs:         renamedFolder.Docs,
	}); err != nil {
		return AppState{}, err
	}
	if newPath != oldPath {
		if err := os.Rename(oldDir, newDir); err != nil {
			return AppState{}, err
		}
	}
	updateCollectionFolderRenameState(collection, oldPath, newPath, oldDisplayPath, newDisplayPath, oldDir, newDir)
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	a.notify("success", "Item renamed successfully")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DeleteFolder(collectionID, folderPath string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	folder := collection.Folders[folderIndex]
	oldPath := normalizeFolderPathKey(folder.Path)
	oldDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	if oldPath == "" {
		return AppState{}, errors.New("folder path is required")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(oldPath))
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the directory does not exist")
		}
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", targetDir)
	}

	removedRequestIDs := map[string]bool{}
	remainingItems := collection.Items[:0]
	for _, item := range collection.Items {
		itemFolderPath := normalizeFolderPathKey(item.FolderPath)
		remove := folderPathHasPrefix(itemFolderPath, oldDisplayPath) || folderPathHasPrefix(itemFolderPath, oldPath)
		if !remove && strings.TrimSpace(item.FilePath) != "" {
			remove = pathInside(targetDir, item.FilePath)
		}
		if remove {
			removedRequestIDs[item.ID] = true
			continue
		}
		remainingItems = append(remainingItems, item)
	}
	collection.Items = remainingItems

	remainingFolders := collection.Folders[:0]
	for _, candidate := range collection.Folders {
		candidatePath := normalizeFolderPathKey(candidate.Path)
		candidateDisplayPath := normalizeFolderPathKey(firstNonEmpty(candidate.DisplayPath, candidate.Name, candidate.Path))
		if folderPathHasPrefix(candidatePath, oldPath) || folderPathHasPrefix(candidateDisplayPath, oldDisplayPath) {
			continue
		}
		remainingFolders = append(remainingFolders, candidate)
	}
	collection.Folders = remainingFolders
	sortFoldersLikeBruno(collection.Folders)

	if err := os.RemoveAll(targetDir); err != nil {
		return AppState{}, err
	}
	parentPath := normalizeFolderPathKey(parentFolderDisplayPath(oldPath))
	parentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(oldDisplayPath))
	if err := a.resequenceCollectionSiblingsLocked(collection, parentPath, parentDisplayPath); err != nil {
		return AppState{}, err
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.removeOpenTabsForRequestIDsLocked(collection.ID, removedRequestIDs)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	a.notify("info", "Deleted folder "+firstNonEmpty(folder.Name, pathBaseSlash(oldDisplayPath), pathBaseSlash(oldPath)))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DeleteRequest(collectionID, itemID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	if collection.Remote != "" {
		if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
			collection.NotFoundLocally = true
			return AppState{}, errors.New("collection is not cloned locally")
		} else if statErr != nil {
			return AppState{}, statErr
		}
	}
	index, err := findItemIndex(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	item := collection.Items[index]
	oldFile := requestFilePath(*collection, item, requestFileExtensionForCollection(*collection))
	if pathInside(collection.Path, item.FilePath) {
		oldFile = filepath.Clean(item.FilePath)
	}
	if !pathInside(collection.Path, oldFile) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", oldFile)
	}
	if info, statErr := os.Stat(oldFile); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the file does not exist")
		}
		return AppState{}, statErr
	} else if info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a request file", oldFile)
	}

	parentPath := normalizeFolderPathKey(item.FolderPath)
	parentDisplayPath := parentPath
	if parentDisplayPath != "" {
		if folder, folderErr := findFolderConfig(collection, parentDisplayPath); folderErr == nil {
			parentPath = normalizeFolderPathKey(folder.Path)
			parentDisplayPath = normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
		}
	}

	if err := os.Remove(oldFile); err != nil {
		return AppState{}, err
	}
	collection.Items = append(collection.Items[:index], collection.Items[index+1:]...)
	if err := a.resequenceCollectionSiblingsLocked(collection, parentPath, parentDisplayPath); err != nil {
		return AppState{}, err
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.removeOpenTabsForRequestIDsLocked(collection.ID, map[string]bool{item.ID: true})
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneFolder(collectionID, folderPath, folderName, directoryName string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		return AppState{}, errors.New("folder name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("folder name must be 255 characters or less")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	sourceFolder := collection.Folders[folderIndex]
	sourcePath := normalizeFolderPathKey(sourceFolder.Path)
	sourceDisplayPath := normalizeFolderPathKey(firstNonEmpty(sourceFolder.DisplayPath, sourceFolder.Name, sourceFolder.Path))
	if sourcePath == "" {
		return AppState{}, errors.New("folder path is required")
	}
	if strings.TrimSpace(directoryName) == "" {
		directoryName = name
	}
	directoryName = bru.SanitizeName(directoryName)
	if !bru.ValidateName(directoryName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, directoryName))
	}
	if strings.EqualFold(directoryName, "collection") || strings.EqualFold(directoryName, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	parentPath := normalizeFolderPathKey(parentFolderDisplayPath(sourcePath))
	parentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(sourceDisplayPath))
	targetPath := joinCollectionFolderPath(parentPath, directoryName)
	targetDisplayPath := joinCollectionFolderPath(parentDisplayPath, name)
	if collectionHasChildFolder(collection, parentPath, directoryName) {
		return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	sourceDir := filepath.Join(collection.Path, filepath.FromSlash(sourcePath))
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(targetPath))
	if !pathInside(collection.Path, sourceDir) || !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(sourceDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the directory does not exist")
		}
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", sourceDir)
	}
	if _, err := os.Stat(targetDir); err == nil {
		return AppState{}, fmt.Errorf("folder: %s already exists", targetDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return AppState{}, err
	}

	now := time.Now()
	clonedFolders := make([]FolderConfig, 0)
	rootSeq := nextCollectionFolderSeq(*collection, parentPath)
	for _, folder := range collection.Folders {
		folderPhysicalPath := normalizeFolderPathKey(folder.Path)
		folderDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
		if !folderPathHasPrefix(folderPhysicalPath, sourcePath) && !folderPathHasPrefix(folderDisplayPath, sourceDisplayPath) {
			continue
		}
		cloned := cloneFolderConfigForFolderClone(folder)
		cloned.Path = replaceFolderPathPrefix(folderPhysicalPath, sourcePath, targetPath)
		cloned.DisplayPath = replaceFolderPathPrefix(folderDisplayPath, sourceDisplayPath, targetDisplayPath)
		if cloned.Path == targetPath {
			cloned.Name = name
			cloned.Seq = rootSeq
		}
		clonedFolders = append(clonedFolders, cloned)
	}
	clonedItems := make([]RequestItem, 0)
	for _, item := range collection.Items {
		if !cloneFolderCopiesRequestType(item.Type) {
			continue
		}
		itemFolderPath := normalizeFolderPathKey(item.FolderPath)
		include := folderPathHasPrefix(itemFolderPath, sourceDisplayPath)
		if !include && strings.TrimSpace(item.FilePath) != "" {
			include = pathInside(sourceDir, item.FilePath)
		}
		if !include {
			continue
		}
		cloned := cloneRequestItemForFolderClone(item)
		cloned.FolderPath = replaceFolderPathPrefix(itemFolderPath, sourceDisplayPath, targetDisplayPath)
		if pathInside(sourceDir, item.FilePath) {
			rel, err := filepath.Rel(sourceDir, item.FilePath)
			if err == nil {
				cloned.FilePath = filepath.Clean(filepath.Join(targetDir, rel))
			}
		} else {
			cloned.FilePath = ""
		}
		if cloned.FilePath == "" {
			cloned.FilePath = requestFilePath(*collection, cloned, requestFileExtensionForCollection(*collection))
		}
		cloned.ID = deterministicID("request", filepath.Clean(cloned.FilePath))
		cloned.Draft = false
		cloned.CreatedAt = now
		cloned.UpdatedAt = now
		assignExampleIDs(&cloned)
		clonedItems = append(clonedItems, cloned)
	}
	if len(clonedFolders) == 0 {
		return AppState{}, fmt.Errorf("folder %s not found", folderPath)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return AppState{}, err
	}
	collection.Folders = append(collection.Folders, clonedFolders...)
	collection.Items = append(collection.Items, clonedItems...)
	sortFoldersLikeBruno(collection.Folders)
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	for _, folder := range clonedFolders {
		if err := a.writeFolderConfigLocked(collection, folder); err != nil {
			return AppState{}, err
		}
	}
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Request cloned!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneRequest(collectionID, itemID, requestName, filename string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	if collection.Remote != "" {
		if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
			collection.NotFoundLocally = true
			return AppState{}, errors.New("collection is not cloned locally")
		} else if statErr != nil {
			return AppState{}, statErr
		}
	}
	source, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	name := strings.TrimSpace(requestName)
	if name == "" {
		return AppState{}, errors.New("request name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("request name must be 255 characters or less")
	}
	filenameBase := requestCloneFilenameBase(filename, name)
	if !bru.ValidateName(filenameBase) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, filenameBase))
	}
	if strings.EqualFold(filenameBase, "collection") || strings.EqualFold(filenameBase, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	targetDir, folderPath, err := cloneRequestTargetDirectory(collection, *source)
	if err != nil {
		return AppState{}, err
	}
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetDir)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", targetDir)
	}
	targetFilename := filenameBase + requestFileExtensionForCollection(*collection)
	targetFile := filepath.Clean(filepath.Join(targetDir, targetFilename))
	if !pathInside(collection.Path, targetFile) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetFilename)
	}
	if collectionHasRequestFileSibling(collection, folderPath, targetFilename) {
		return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
	}
	if _, statErr := os.Stat(targetFile); statErr == nil {
		return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return AppState{}, statErr
	}

	now := time.Now()
	cloned := cloneRequestItemForFolderClone(*source)
	cloned.Name = name
	cloned.FolderPath = folderPath
	cloned.FilePath = targetFile
	cloned.ID = deterministicID("request", targetFile)
	cloned.Seq = nextCollectionRequestSeq(*collection, folderPath)
	cloned.Draft = false
	cloned.Transient = collection.Scratch
	cloned.CreatedAt = now
	cloned.UpdatedAt = now
	assignExampleIDs(&cloned)

	collection.Items = append(collection.Items, cloned)
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.openTabLocked(collection.ID, cloned.ID, "request")
	a.notify("success", "Request cloned!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RenameRequest(collectionID, itemID, requestName, filename string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	if collection.Remote != "" {
		if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
			collection.NotFoundLocally = true
			return AppState{}, errors.New("collection is not cloned locally")
		} else if statErr != nil {
			return AppState{}, statErr
		}
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	name := strings.TrimSpace(requestName)
	if name == "" {
		return AppState{}, errors.New("request name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("request name must be 255 characters or less")
	}
	filenameBase := requestCloneFilenameBase(filename, name)
	if !bru.ValidateName(filenameBase) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, filenameBase))
	}
	if strings.EqualFold(filenameBase, "collection") || strings.EqualFold(filenameBase, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	targetDir, folderPath, err := cloneRequestTargetDirectory(collection, *item)
	if err != nil {
		return AppState{}, err
	}
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetDir)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", targetDir)
	}
	oldFile := requestFilePath(*collection, *item, requestFileExtensionForCollection(*collection))
	if pathInside(collection.Path, item.FilePath) {
		oldFile = filepath.Clean(item.FilePath)
	}
	oldFileExists := true
	if info, statErr := os.Stat(oldFile); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) && item.Draft && item.Transient {
			oldFileExists = false
		} else if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the file does not exist")
		} else {
			return AppState{}, statErr
		}
	} else if info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a request file", oldFile)
	}
	targetFilename := filenameBase + requestFileExtensionForCollection(*collection)
	targetFile := filepath.Clean(filepath.Join(targetDir, targetFilename))
	if !pathInside(collection.Path, targetFile) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetFilename)
	}
	renamingFile := !sameFilePath(oldFile, targetFile)
	if renamingFile {
		if collectionHasRequestFileSiblingExcept(collection, folderPath, targetFilename, item.ID) {
			return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
		}
		if _, statErr := os.Stat(targetFile); statErr == nil {
			return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, statErr
		}
		if oldFileExists {
			if err := os.Rename(oldFile, targetFile); err != nil {
				return AppState{}, err
			}
		}
		item.FilePath = targetFile
	}
	now := time.Now()
	item.Name = name
	item.FolderPath = folderPath
	item.Draft = false
	item.Transient = collection.Scratch
	item.UpdatedAt = now
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.notify("success", "Item renamed successfully")
	return a.state, a.markDirty(persistScopeState)
}

func cloneFolderCopiesRequestType(requestType string) bool {
	switch strings.ToLower(strings.TrimSpace(requestType)) {
	case "", "http", "graphql", "grpc":
		return true
	default:
		return false
	}
}

func requestCloneFilenameBase(filename, fallbackName string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = strings.TrimSpace(fallbackName)
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".bru", ".yml", ".yaml":
		filename = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	return bru.SanitizeName(filename)
}

func cloneRequestTargetDirectory(collection *Collection, item RequestItem) (string, string, error) {
	folderPath := normalizeFolderPathKey(item.FolderPath)
	if pathInside(collection.Path, item.FilePath) {
		return filepath.Dir(filepath.Clean(item.FilePath)), folderPath, nil
	}
	if folderPath == "" {
		return filepath.Clean(collection.Path), "", nil
	}
	folder, err := findFolderConfig(collection, folderPath)
	if err == nil {
		return filepath.Join(collection.Path, filepath.FromSlash(folder.Path)), normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path)), nil
	}
	return filepath.Join(collection.Path, filepath.FromSlash(folderPath)), folderPath, nil
}

func collectionHasRequestFileSibling(collection *Collection, folderPath, filename string) bool {
	return collectionHasRequestFileSiblingExcept(collection, folderPath, filename, "")
}

func collectionHasRequestFileSiblingExcept(collection *Collection, folderPath, filename, exceptItemID string) bool {
	folderPath = normalizeFolderPathKey(folderPath)
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return false
	}
	for _, item := range collection.Items {
		if exceptItemID != "" && item.ID == exceptItemID {
			continue
		}
		if normalizeFolderPathKey(item.FolderPath) != folderPath {
			continue
		}
		if strings.EqualFold(requestItemFilename(*collection, item), filename) {
			return true
		}
	}
	return false
}

func sameFilePath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func requestItemFilename(collection Collection, item RequestItem) string {
	if pathInside(collection.Path, item.FilePath) {
		return filepath.Base(item.FilePath)
	}
	return sanitizeFilename(item.Name) + requestFileExtensionForCollection(collection)
}

func nextCollectionRequestSeq(collection Collection, folderPath string) int {
	folderPath = normalizeFolderPathKey(folderPath)
	count := 0
	for _, item := range collection.Items {
		if normalizeFolderPathKey(item.FolderPath) == folderPath {
			count++
		}
	}
	return count + 1
}

func (a *App) UpdateRequest(collectionID, itemID string, patch RequestPatch) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	item, err := a.updateRequestLocked(collectionID, itemID, patch)
	if item == nil {
		return AppState{}, err
	}
	return a.state, err
}

// updateRequestLocked is the body shared by UpdateRequest and its narrow
// variant (US-014). Extracted rather than duplicated so the two cannot drift:
// a patch applied one way by the wide binding and another by the narrow one is
// a bug that only shows up as the frontend and the backend disagreeing about
// what the user typed.
//
// A nil item means the mutation did not happen and err says why. A non-nil item
// with a non-nil err means the mutation DID happen and err is markDirty's
// parked background-write failure from an earlier persist.
func (a *App) updateRequestLocked(collectionID, itemID string, patch RequestPatch) (*RequestItem, error) {
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	item, err := a.findItemLocked(collectionID, itemID)
	if err != nil {
		return nil, err
	}
	applyPatch(item, patch)
	item.Draft = true
	item.UpdatedAt = time.Now()
	return item, a.markDirty(persistScopeState)
}

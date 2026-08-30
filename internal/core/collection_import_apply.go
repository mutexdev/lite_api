package core

// Applying a previewed import, and rolling the filesystem back if part of it fails.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

func (a *App) ApplyCollectionImport(request CollectionImportApplyRequest) (CollectionImportApplyResult, error) {
	sources, err := a.resolveCollectionImportSources(request.Sources)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	request.Sources = sources
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionImportApplyResult{}, err
	}
	workspace, err := a.findWorkspaceLocked(request.WorkspaceID)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	_, err = previewCollectionImport(CollectionImportPreviewRequest{Sources: request.Sources})
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	if err := validateCollectionImportSelections(request.Selections); err != nil {
		return CollectionImportApplyResult{}, err
	}
	candidates := make(map[string]collectionImportCandidate, len(request.Sources))
	for index, source := range request.Sources {
		for _, candidate := range previewCollectionImportCandidates(source, index) {
			candidates[candidate.row.CandidateID] = candidate
		}
	}
	result := CollectionImportApplyResult{State: a.state}
	before, err := cloneCollectionImportState(a.state)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	mutations := []collectionImportMutation{}
	globalEnvironmentsWritten := false
	watchFingerprints := map[string]string{}
	for key, value := range a.collectionWatchFingerprints {
		watchFingerprints[key] = value
	}
	for _, selection := range request.Selections {
		candidate, ok := candidates[selection.CandidateID]
		if !ok || candidate.row.SourceID != selection.SourceID {
			result.Errors = append(result.Errors, CollectionImportPreviewRow{SourceID: selection.SourceID, CandidateID: selection.CandidateID, Error: "selected import source is unavailable"})
			continue
		}
		if strings.TrimSpace(selection.ExpectedContentHash) == "" || selection.ExpectedContentHash != candidate.row.ContentHash {
			candidate.row.Error = "selected source changed since preview"
			result.Errors = append(result.Errors, candidate.row)
			continue
		}
		collection := candidate.collection
		// US-044. Candidates are parsed during DETECTION, before the apply
		// request — and therefore before this flag — exists. A Postman
		// candidate must be re-imported here or the opt-in would silently do
		// nothing for every normally-detected file, leaving the toggle
		// apparently working and demonstrably ineffective.
		if request.TranslatePostmanScripts && strings.EqualFold(strings.TrimSpace(candidate.row.DetectedKind), "postman") && strings.TrimSpace(selection.KindOverride) == "" {
			content, _, _, folder, readErr := readCollectionImportCandidate(candidate)
			if readErr == nil && !folder {
				if translated, translateErr := collectionFromImport(ImportPayload{
					Kind:                    "postman",
					Name:                    candidate.row.CollectionName,
					Content:                 content,
					TranslatePostmanScripts: true,
				}); translateErr == nil {
					collection = translated
				}
				// A failure here is deliberately not fatal: the untranslated
				// candidate is already valid and imports correctly, so falling
				// back to it beats failing an import over an optional rewrite.
			}
		}
		if strings.TrimSpace(selection.KindOverride) != "" {
			content, _, _, folder, readErr := readCollectionImportCandidate(candidate)
			if readErr != nil || folder {
				candidate.row.Error = "manual import kind could not read the selected source"
				result.Errors = append(result.Errors, candidate.row)
				continue
			}
			collection, err = collectionFromImport(ImportPayload{Kind: selection.KindOverride, Name: candidate.row.CollectionName, Content: content, TranslatePostmanScripts: request.TranslatePostmanScripts})
			if err != nil {
				candidate.row.Error = "manual import kind could not be parsed"
				result.Errors = append(result.Errors, candidate.row)
				continue
			}
			candidate.row.Error = ""
			candidate.row = collectionImportRow(candidate.row, collection, strings.ToLower(strings.TrimSpace(selection.KindOverride)), "manual")
		} else if candidate.row.Error != "" {
			result.Errors = append(result.Errors, candidate.row)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(candidate.row.DetectedKind), "postman-environment") {
			if err := a.applyImportedGlobalEnvironmentsLocked(workspace, collection.Environments); err != nil {
				candidate.row.Error = collectionImportDiagnostic(err)
				result.Errors = append(result.Errors, candidate.row)
			} else {
				// Recorded so a later failure in this batch can put the
				// environment files back. They are not tracked as path
				// mutations: the directory is written whole, not file by file.
				globalEnvironmentsWritten = true
				result.Applied = append(result.Applied, candidate.row)
			}
			continue
		}
		if candidate.row.ExistingFolder {
			if selection.FilterEnvironments || selection.FilterFolders || selection.FilterRequests {
				candidate.row.Error = "existing collection folders can only be opened as complete collections"
				result.Errors = append(result.Errors, candidate.row)
				continue
			}
			if err := a.applyOpenedImportFolderLocked(workspace, candidate.collection); err != nil {
				candidate.row.Error = "collection folder could not be opened"
				result.Errors = append(result.Errors, candidate.row)
			} else {
				result.Applied = append(result.Applied, candidate.row)
			}
			continue
		}
		var filterWarnings []string
		collection, filterWarnings = filterImportedCollection(collection, selection)
		candidate.row.Warnings = append(candidate.row.Warnings, filterWarnings...)
		if strings.TrimSpace(selection.OutputName) != "" {
			collection.Name = strings.TrimSpace(selection.OutputName)
		}
		root, err := collectionImportDestinationRoot(request.DestinationRoot, workspace.Path)
		if err != nil {
			candidate.row.Error = "destination root is invalid"
			result.Errors = append(result.Errors, candidate.row)
			continue
		}
		if err := a.applyImportedCollectionLocked(workspace, &collection, root, selection.ConflictAction, &mutations); err != nil {
			if errors.Is(err, errCollectionImportSkipped) {
				result.Skipped = append(result.Skipped, candidate.row)
			} else {
				restoreErr := rollbackCollectionImportMutations(a, mutations)
				a.collectionWatchFingerprints = watchFingerprints
				a.state = before
				if envErr := a.restoreGlobalEnvironmentFilesLocked(globalEnvironmentsWritten, request.WorkspaceID); envErr != nil && restoreErr == nil {
					restoreErr = envErr
				}
				return CollectionImportApplyResult{}, collectionImportCommitError(err, restoreErr)
			}
			continue
		}
		candidate.row = collectionImportRow(candidate.row, collection, candidate.row.DetectedKind, candidate.row.Confidence)
		result.Applied = append(result.Applied, candidate.row)
	}
	if len(result.Applied) > 0 {
		persist := a.persistLocked
		if a.collectionImportHooks != nil && a.collectionImportHooks.persist != nil {
			persist = func() error { return a.collectionImportHooks.persist(a) }
		}
		if err := persist(); err != nil {
			restoreErr := rollbackCollectionImportMutations(a, mutations)
			a.collectionWatchFingerprints = watchFingerprints
			a.state = before
			if envErr := a.restoreGlobalEnvironmentFilesLocked(globalEnvironmentsWritten, request.WorkspaceID); envErr != nil && restoreErr == nil {
				restoreErr = envErr
			}
			return CollectionImportApplyResult{}, collectionImportCommitError(err, restoreErr)
		}
		for _, mutation := range mutations {
			if mutation.backup != "" {
				_ = removeCollectionImportPath(a, mutation.backup)
			}
		}
	}
	notifyCollectionImportOutcome(a, result)
	result.State = a.state
	return result, nil
}

// notifyCollectionImportOutcome puts the outcome somewhere that survives the
// user navigating away from the Import view.
//
// US-055. Applying an import used to write its outcome only into an aria-live
// line inside the panel; ImportGlobalEnvironment, on the other side of the same
// app, has always raised a notification. Anyone who started an import and
// switched tabs learned nothing about how it went.
func notifyCollectionImportOutcome(app *App, result CollectionImportApplyResult) {
	parts := []string{fmt.Sprintf("%d imported", len(result.Applied))}
	if len(result.Skipped) > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", len(result.Skipped)))
	}
	if len(result.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(result.Errors)))
	}
	warnings := 0
	for _, row := range result.Applied {
		warnings += len(row.Warnings)
	}
	if warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d warning%s", warnings, map[bool]string{true: "", false: "s"}[warnings == 1]))
	}
	level := "success"
	if len(result.Errors) > 0 {
		level = "warning"
	}
	if len(result.Applied) == 0 && len(result.Errors) > 0 {
		level = "error"
	}
	message := "Import: " + strings.Join(parts, ", ")
	if len(result.Errors) > 0 && result.Errors[0].Error != "" {
		message += ". First failure: " + result.Errors[0].Error
	}
	app.notify(level, message)
}

// readCollectionImportCandidate reads the bytes a candidate was parsed from.
//
// US-058. A candidate that came out of a data dump or a ZIP cannot be re-read
// from its source path -- that path is the archive, not the document -- so the
// document travels with the candidate and is returned here instead.
func readCollectionImportCandidate(candidate collectionImportCandidate) (string, string, string, bool, error) {
	if candidate.child {
		return candidate.content, candidate.row.SourceName, candidate.row.ContentHash, false, nil
	}
	return readCollectionImportSource(candidate.source)
}

// applyImportedGlobalEnvironmentsLocked puts imported environments where a
// standalone Postman environment file belongs: the workspace's globals, the
// same destination the Environments panel's paste box has always used.
//
// Names are made unique rather than replaced. Importing the same file twice is
// a thing people do while working out whether the first one took, and silently
// overwriting the earlier one would destroy any edit made in between.
func (a *App) applyImportedGlobalEnvironmentsLocked(workspace *Workspace, environments []Environment) error {
	if len(environments) == 0 {
		return errors.New("no environments found to import")
	}
	// workspace points into a.state, so everything below is live the moment it
	// is appended. The write can still fail -- a read-only directory, a full
	// disk -- and an import that reports failure while leaving the environment
	// in memory is worse than one that fails cleanly: it shows up in the
	// Environments panel, and the next unrelated save writes it out, quietly
	// completing the import the user was told had not happened.
	restoreEnvironments := workspace.GlobalEnvironments
	restoreUpdatedAt := workspace.UpdatedAt
	// Appending to a fresh slice rather than in place, so the rollback cannot
	// be defeated by an append that wrote into shared backing array capacity.
	workspace.GlobalEnvironments = append([]Environment{}, workspace.GlobalEnvironments...)
	for _, environment := range environments {
		environment.ID = newID("global-env")
		environment.Name = scripting.UniqueEnvironmentName(workspace.GlobalEnvironments, environment.Name)
		for index := range environment.Variables {
			if environment.Variables[index].ID == "" {
				environment.Variables[index].ID = newID("var")
			}
		}
		workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, environment)
	}
	workspace.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(workspace); err != nil {
		workspace.GlobalEnvironments = restoreEnvironments
		workspace.UpdatedAt = restoreUpdatedAt
		return err
	}
	return nil
}

func cloneCollectionImportState(state AppState) (AppState, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return AppState{}, err
	}
	var clone AppState
	if err := json.Unmarshal(data, &clone); err != nil {
		return AppState{}, err
	}
	return clone, nil
}

func rollbackCollectionImportMutations(app *App, mutations []collectionImportMutation) error {
	rename := os.Rename
	if app.collectionImportHooks != nil && app.collectionImportHooks.rename != nil {
		rename = app.collectionImportHooks.rename
	}
	var restoreErr error
	for index := len(mutations) - 1; index >= 0; index-- {
		mutation := mutations[index]
		if err := removeCollectionImportPath(app, mutation.target); err != nil {
			if mutation.backup != "" && restoreErr == nil {
				restoreErr = err
			}
			continue
		}
		if mutation.backup == "" {
			continue
		}
		if err := rename(mutation.backup, mutation.target); err != nil && restoreErr == nil {
			restoreErr = err
		}
	}
	return restoreErr
}

// collectionImportCommitError explains a failed commit.
//
// US-055. This used to be a bare "selected imports could not be committed",
// which told the user neither what went wrong nor -- more seriously -- when the
// rollback had failed to put their existing collection back. A replace that
// cannot restore its backup leaves the original under a .liteapi-import-backup-
// suffix, and saying nothing about that is how a collection gets given up for
// lost.
func collectionImportCommitError(cause, restoreErr error) error {
	message := "selected imports could not be committed"
	if diagnosis := collectionImportDiagnostic(cause); diagnosis != "" {
		message += ": " + diagnosis
	}
	if restoreErr != nil {
		message += ". The previous collection could not be restored and is kept beside it with a .liteapi-import-backup- suffix"
	}
	return errors.New(message)
}

func removeCollectionImportPath(app *App, path string) error {
	remove := os.RemoveAll
	if app.collectionImportHooks != nil && app.collectionImportHooks.remove != nil {
		remove = app.collectionImportHooks.remove
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

var errCollectionImportSkipped = errors.New("collection import skipped")

func validateCollectionImportSelections(selections []CollectionImportSelection) error {
	seen := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		key := selection.SourceID + "\x00" + selection.CandidateID
		if _, exists := seen[key]; exists {
			return errors.New("each import source can be selected only once")
		}
		seen[key] = struct{}{}
		switch strings.ToLower(strings.TrimSpace(selection.ConflictAction)) {
		case "", "rename", "skip", "replace":
		default:
			return errors.New("conflict action must be rename, skip, replace, or empty")
		}
	}
	return nil
}

func collectionImportDestinationRoot(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	expanded, err := expandUserPath(value)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", errors.New("destination root must be absolute")
	}
	root := filepath.Clean(expanded)
	existing := root
	for {
		if _, statErr := os.Stat(existing); statErr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", errors.New("destination root is unavailable")
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", errors.New("destination root is unavailable")
	}
	relative, err := filepath.Rel(existing, root)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("destination root is invalid")
	}
	return filepath.Join(resolved, relative), nil
}

// filterImportedCollection narrows an import to what the user selected.
//
// Requests and folders come from INDEPENDENT selection lists, so the two can
// disagree: keeping a request while dropping the folder it lives in left the
// request pointing at a FolderPath no FolderConfig described. Nothing rejected
// that collection — it wrote to disk, opened in the sidebar, and then exported
// to Postman as nothing at all, because the export walked folders and never
// reached it.
//
// The selection is therefore reconciled here rather than repaired at each
// reader: a kept request implies its whole folder chain is kept, and the
// re-added folders are named in the returned warnings so the user is told the
// selection was widened rather than finding out later.
func filterImportedCollection(collection Collection, selection CollectionImportSelection) (Collection, []string) {
	available := append([]FolderConfig(nil), collection.Folders...)
	selected := func(ids []string) map[string]bool {
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	if selection.FilterRequests {
		keep := selected(selection.RequestIDs)
		items := []RequestItem{}
		for index, item := range collection.Items {
			if keep[collectionImportRequestSelectionID(selection.SourceID, item, index)] {
				items = append(items, item)
			}
		}
		collection.Items = items
	}
	if selection.FilterEnvironments {
		keep := selected(selection.EnvironmentIDs)
		environments := []Environment{}
		for index, environment := range collection.Environments {
			if keep[collectionImportEnvironmentSelectionID(selection.SourceID, environment, index)] {
				environments = append(environments, environment)
			}
		}
		collection.Environments = environments
	}
	if selection.FilterFolders {
		keep := selected(selection.FolderIDs)
		folders := []FolderConfig{}
		for index, folder := range collection.Folders {
			if keep[collectionImportFolderSelectionID(selection.SourceID, folder, index)] {
				folders = append(folders, folder)
			}
		}
		collection.Folders = folders
	}
	if !selection.FilterFolders {
		return collection, nil
	}
	return reconcileImportedFolderChains(collection, available)
}

// reconcileImportedFolderChains re-adds every folder a kept request or a kept
// folder still needs. A folder that has to come back is taken from the
// pre-filter list so it keeps its own scripts, auth and sequence; only a folder
// that never existed as a config is synthesised.
func reconcileImportedFolderChains(collection Collection, available []FolderConfig) (Collection, []string) {
	present := map[string]bool{}
	for _, folder := range collection.Folders {
		if key := types.NormalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Path)); key != "" {
			present[key] = true
		}
	}
	source := map[string]FolderConfig{}
	for _, folder := range available {
		if key := types.NormalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Path)); key != "" {
			if _, exists := source[key]; !exists {
				source[key] = folder
			}
		}
	}

	required := []string{}
	add := func(path string) {
		for key := types.NormalizeFolderPathKey(path); key != ""; key = types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(key)) {
			if !present[key] {
				present[key] = true
				required = append(required, key)
			}
		}
	}
	for _, item := range collection.Items {
		add(item.FolderPath)
	}
	for _, folder := range collection.Folders {
		add(types.ParentFolderDisplayPath(firstNonEmpty(folder.DisplayPath, folder.Path)))
	}
	if len(required) == 0 {
		return collection, nil
	}

	sort.Strings(required)
	warnings := make([]string, 0, len(required))
	for _, key := range required {
		folder, ok := source[key]
		if !ok {
			folder = FolderConfig{Path: key, DisplayPath: key, Name: path.Base(key)}
		}
		collection.Folders = append(collection.Folders, folder)
		warnings = append(warnings, fmt.Sprintf("Folder %q was kept because a selected request lives inside it", key))
	}
	return collection, warnings
}

func (a *App) applyOpenedImportFolderLocked(workspace *Workspace, collection Collection) error {
	now := time.Now()
	for index := range workspace.Collections {
		if filepath.Clean(workspace.Collections[index].Path) == filepath.Clean(collection.Path) {
			workspace.Collections[index] = collection
			workspace.UpdatedAt = now
			a.seedCollectionWatchFingerprintLocked(collection.Path)
			a.openFirstCollectionItemLocked(collection)
			return nil
		}
	}
	workspace.Collections = append(workspace.Collections, collection)
	workspace.UpdatedAt = now
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.openFirstCollectionItemLocked(collection)
	return nil
}

func (a *App) applyImportedCollectionLocked(workspace *Workspace, collection *Collection, root, conflictAction string, mutationTargets ...*[]collectionImportMutation) error {
	var mutations *[]collectionImportMutation
	if len(mutationTargets) > 0 {
		mutations = mutationTargets[0]
	}
	collection.Name = firstNonEmpty(strings.TrimSpace(collection.Name), "Imported Collection")
	collection.Format = "bru"
	target := filepath.Join(root, sanitizeFilename(collection.Name))
	if !pathInside(root, target) {
		return errors.New("destination escapes selected root")
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("destination cannot be a symbolic link")
		}
		switch strings.ToLower(strings.TrimSpace(conflictAction)) {
		case "skip":
			return errCollectionImportSkipped
		case "replace":
			backup := target + ".liteapi-import-backup-" + newID("backup")
			rename := os.Rename
			if a.collectionImportHooks != nil && a.collectionImportHooks.rename != nil {
				rename = a.collectionImportHooks.rename
			}
			if err := rename(target, backup); err != nil {
				return err
			}
			collection.Path = target
			if err := a.materializeImportedCollectionLocked(collection); err != nil {
				_ = rename(backup, target)
				return err
			}
			if mutations != nil {
				*mutations = append(*mutations, collectionImportMutation{target: target, backup: backup})
			} else if err := removeCollectionImportPath(a, backup); err != nil {
				return err
			}
			for index := range workspace.Collections {
				if filepath.Clean(workspace.Collections[index].Path) == filepath.Clean(target) {
					workspace.Collections[index] = *collection
					return nil
				}
			}
			workspace.Collections = append(workspace.Collections, *collection)
			workspace.UpdatedAt = time.Now()
			return nil
		default:
			for ordinal := 2; ; ordinal++ {
				candidate := filepath.Join(root, sanitizeFilename(fmt.Sprintf("%s %d", collection.Name, ordinal)))
				if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
					collection.Name, target = fmt.Sprintf("%s %d", collection.Name, ordinal), candidate
					break
				} else if err != nil {
					return err
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	collection.Path = target
	if err := a.materializeImportedCollectionLocked(collection); err != nil {
		return err
	}
	if mutations != nil {
		*mutations = append(*mutations, collectionImportMutation{target: target})
	}
	workspace.Collections = append(workspace.Collections, *collection)
	workspace.UpdatedAt = time.Now()
	return nil
}

func (a *App) materializeImportedCollectionLocked(collection *Collection) error {
	parent, base := filepath.Dir(collection.Path), filepath.Base(collection.Path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+base+".liteapi-import-")
	if err != nil {
		return err
	}
	defer func() {
		a.clearCollectionWatchFingerprintLocked(staging)
		_ = removeCollectionImportPath(a, staging)
	}()
	finalPath := collection.Path
	collection.Path = staging
	write := a.writeCollectionFilesLocked
	if a.collectionImportHooks != nil && a.collectionImportHooks.write != nil {
		write = func(next *Collection) error { return a.collectionImportHooks.write(a, next) }
	}
	if err := write(collection); err != nil {
		collection.Path = finalPath
		return err
	}
	remapImportedRequestPaths(collection, staging, finalPath)
	collection.Path = finalPath
	rename := os.Rename
	if a.collectionImportHooks != nil && a.collectionImportHooks.rename != nil {
		rename = a.collectionImportHooks.rename
	}
	if err := rename(staging, finalPath); err != nil {
		return err
	}
	a.seedCollectionWatchFingerprintLocked(finalPath)
	return nil
}

func remapImportedRequestPaths(collection *Collection, staging, destination string) {
	for index := range collection.Items {
		path := collection.Items[index].FilePath
		if path == "" || !pathInside(staging, path) {
			continue
		}
		if relative, err := filepath.Rel(staging, path); err == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			collection.Items[index].FilePath = filepath.Join(destination, relative)
		}
	}
}

// restoreGlobalEnvironmentFilesLocked rewrites a workspace's environment files
// from state that has just been rolled back, so disk agrees with memory again.
//
// The batch rollback restores a.state, which is enough for everything tracked
// as a path mutation. Global environments are not: they are written as a whole
// directory, so nothing in the mutation list describes them, and the files an
// aborted batch wrote would otherwise stay. The next workspace load merges
// whatever is in that directory back into the state, so leaving them means an
// import reported as failed reappears at the next launch, which is exactly the
// outcome the rollback exists to prevent.
func (a *App) restoreGlobalEnvironmentFilesLocked(written bool, workspaceID string) error {
	if !written {
		return nil
	}
	workspace, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return err
	}
	return a.writeWorkspaceGlobalEnvironmentFilesLocked(workspace)
}

package core

// Saved response examples: creating, renaming, cloning and finding them.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/types"
)

func (a *App) SaveResponseExample(collectionID, itemID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	if item.Response == nil {
		return AppState{}, errors.New("send the request before saving a response example")
	}
	if item.Response.Size > 5*1024*1024 {
		return AppState{}, errors.New("response examples are limited to 5 MB")
	}
	example := responseExampleFromItem(*item, strings.TrimSpace(name))
	item.Examples = append(item.Examples, example)
	item.Draft = false
	if !collection.Scratch {
		item.Transient = false
	}
	item.UpdatedAt = time.Now()
	a.openResponseExampleTabLocked(collectionID, itemID, example)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Saved response example "+example.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateResponseExample(collectionID, itemID, name, description string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AppState{}, errors.New("example name is required")
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example := blankResponseExampleFromItem(*item, name, strings.TrimSpace(description))
	item.Examples = append(item.Examples, example)
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.openResponseExampleTabLocked(collectionID, itemID, example)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Created response example "+example.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RenameResponseExample(collectionID, itemID, exampleID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AppState{}, errors.New("example name is required")
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	example.Name = name
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.syncResponseExampleTabLocked(collectionID, itemID, *example)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Renamed response example "+name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneResponseExample(collectionID, itemID, exampleID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	cloned := cloneResponseExample(*example)
	cloned.ID = newID("example")
	cloned.Name = strings.TrimSpace(firstNonEmpty(example.Name, "Example")) + " (Copy)"
	item.Examples = append(item.Examples, cloned)
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.openResponseExampleTabLocked(collectionID, itemID, cloned)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Cloned response example "+cloned.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DeleteResponseExample(collectionID, itemID, exampleID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, index, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	deletedName := example.Name
	deletedID := example.ID
	item.Examples = append(item.Examples[:index], item.Examples[index+1:]...)
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.closeResponseExampleTabLocked(collectionID, itemID, deletedID)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("info", "Deleted response example "+deletedName)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateResponseExample(collectionID, itemID, exampleID string, updated ResponseExample) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, index, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	normalized, err := normalizeResponseExampleUpdate(*example, updated, *item)
	if err != nil {
		return AppState{}, err
	}
	item.Examples[index] = normalized
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.syncResponseExampleTabLocked(collectionID, itemID, normalized)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Updated response example "+normalized.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) GenerateResponseExampleCode(collectionID, itemID, exampleID, language string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return "", err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return "", err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return "", err
	}
	return codegen.GenerateResponseExampleCode(*example, language)
}

func blankResponseExampleFromItem(item RequestItem, name, description string) ResponseExample {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	return ResponseExample{
		ID:          deterministicID("example", firstNonEmpty(item.FilePath, item.ID, item.Name)+"#example#"+strconv.Itoa(len(item.Examples))+"#"+name),
		Name:        name,
		Description: description,
		Type:        firstNonEmpty(item.Type, "http"),
		Request: ResponseExampleRequest{
			Method:         strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet)),
			URL:            item.URL,
			BodyMode:       firstNonEmpty(item.Body.Mode, "none"),
			Body:           requestBodySnapshot(item.Body),
			Headers:        cloneKeyValues(item.Headers),
			Params:         cloneKeyValues(item.Params),
			FormURLEncoded: cloneKeyValues(item.Body.FormURLEncoded),
			MultipartForm:  cloneFormParts(item.Body.Multipart),
			File:           cloneFileBodyEntries(types.FileBodyEntriesOf(item.Body)),
		},
		Response: ResponseExamplePayload{
			Status:     http.StatusOK,
			StatusText: http.StatusText(http.StatusOK),
			BodyType:   "text",
			Body:       "",
			Headers:    []KeyValue{},
			Size:       0,
		},
	}
}

func normalizeResponseExampleUpdate(existing, updated ResponseExample, item RequestItem) (ResponseExample, error) {
	next := cloneResponseExample(updated)
	next.ID = existing.ID
	next.Name = strings.TrimSpace(next.Name)
	if next.Name == "" {
		next.Name = strings.TrimSpace(existing.Name)
	}
	if next.Name == "" {
		return ResponseExample{}, errors.New("example name is required")
	}
	next.Type = strings.TrimSpace(firstNonEmpty(next.Type, existing.Type, item.Type, "http"))
	if strings.TrimSpace(next.Request.Method) == "" {
		next.Request.Method = firstNonEmpty(existing.Request.Method, item.Method, http.MethodGet)
	}
	next.Request.Method = strings.ToUpper(strings.TrimSpace(next.Request.Method))
	if strings.TrimSpace(next.Request.URL) == "" {
		next.Request.URL = firstNonEmpty(existing.Request.URL, item.URL)
	}
	if strings.TrimSpace(next.Request.BodyMode) == "" {
		next.Request.BodyMode = firstNonEmpty(existing.Request.BodyMode, "none")
	}
	next.Request.Headers = cloneKeyValues(next.Request.Headers)
	next.Request.Params = cloneKeyValues(next.Request.Params)
	next.Request.FormURLEncoded = cloneKeyValues(next.Request.FormURLEncoded)
	next.Request.MultipartForm = cloneFormParts(next.Request.MultipartForm)
	next.Request.File = cloneFileBodyEntries(next.Request.File)
	if next.Response.Status == 0 {
		next.Response.Status = firstNonZero(existing.Response.Status, http.StatusOK)
	}
	next.Response.StatusText = cleanStatusText(next.Response.Status, next.Response.StatusText)
	if next.Response.StatusText == "" {
		next.Response.StatusText = http.StatusText(next.Response.Status)
	}
	next.Response.BodyType = strings.TrimSpace(firstNonEmpty(next.Response.BodyType, existing.Response.BodyType, "text"))
	next.Response.Headers = cloneKeyValues(next.Response.Headers)
	next.Response.Size = len([]byte(next.Response.Body))
	return next, nil
}

func ensureResponseExampleIDs(item *RequestItem) {
	for index := range item.Examples {
		if strings.TrimSpace(item.Examples[index].ID) == "" {
			item.Examples[index].ID = deterministicID("example", firstNonEmpty(item.FilePath, item.ID, item.Name)+"#example#"+strconv.Itoa(index))
		}
	}
}

func findResponseExample(item *RequestItem, exampleID string) (*ResponseExample, int, error) {
	target := strings.TrimSpace(exampleID)
	if target == "" {
		return nil, -1, errors.New("example id is required")
	}
	ensureResponseExampleIDs(item)
	for index := range item.Examples {
		if item.Examples[index].ID == target {
			return &item.Examples[index], index, nil
		}
	}
	nameMatches := []int{}
	for index := range item.Examples {
		if item.Examples[index].Name == target {
			nameMatches = append(nameMatches, index)
		}
	}
	if len(nameMatches) == 1 {
		index := nameMatches[0]
		return &item.Examples[index], index, nil
	}
	if len(nameMatches) > 1 {
		return nil, -1, fmt.Errorf("response example name %q is ambiguous", target)
	}
	return nil, -1, fmt.Errorf("response example %s not found", target)
}

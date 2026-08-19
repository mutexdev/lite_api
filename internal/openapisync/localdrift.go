package openapisync

// Local drift: what the collection has that the spec does not, and the other way round.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

func BuildOpenAPILocalDriftResult(collection, specCollection types.Collection, config types.OpenAPISyncConfig) types.OpenAPILocalDriftResult {
	config = NormalizeConfig(config)
	existing := openAPIEndpointMap(collection.Items)
	specs := openAPIEndpointMap(specCollection.Items)
	result := types.OpenAPILocalDriftResult{
		SourceURL:               config.SourceURL,
		GroupBy:                 config.GroupBy,
		SpecEndpointCount:       len(specs),
		CollectionEndpointCount: len(existing),
		LastSyncDate:            config.LastSyncDate,
	}
	seenSpec := map[string]bool{}
	for _, spec := range specCollection.Items {
		id := OpenAPIEndpointID(spec)
		if id == "" || seenSpec[id] {
			continue
		}
		seenSpec[id] = true
		method, path := splitOpenAPIEndpointID(id)
		ref, ok := existing[id]
		if !ok {
			result.Missing++
			result.Changes = append(result.Changes, types.OpenAPISyncEndpointChange{
				ID:              id,
				Method:          method,
				Path:            path,
				Name:            spec.Name,
				Change:          "missing",
				DefaultDecision: "accept-incoming",
			})
			continue
		}
		if changes := openAPILocalDriftRequestChanges(spec, ref.Item); len(changes) > 0 {
			result.Modified++
			result.Changes = append(result.Changes, types.OpenAPISyncEndpointChange{
				ID:              id,
				Method:          method,
				Path:            path,
				Name:            scalar.FirstNonEmpty(ref.Item.Name, spec.Name),
				Change:          "modified",
				ItemID:          ref.Item.ID,
				DefaultDecision: "accept-incoming",
			})
			continue
		}
		result.InSync++
	}
	specIDs := map[string]bool{}
	for id := range specs {
		specIDs[id] = true
	}
	existingIDs := make([]string, 0, len(existing))
	for id := range existing {
		existingIDs = append(existingIDs, id)
	}
	sort.Strings(existingIDs)
	for _, id := range existingIDs {
		if specIDs[id] {
			continue
		}
		ref := existing[id]
		method, path := splitOpenAPIEndpointID(id)
		result.LocalOnly++
		result.Changes = append(result.Changes, types.OpenAPISyncEndpointChange{
			ID:              id,
			Method:          method,
			Path:            path,
			Name:            ref.Item.Name,
			Change:          "local-only",
			ItemID:          ref.Item.ID,
			DefaultDecision: "keep-mine",
		})
	}
	result.HasChanges = result.Modified+result.Missing+result.LocalOnly > 0
	return result
}

func ApplyOpenAPILocalDriftToCollection(collection *types.Collection, specCollection types.Collection, options types.OpenAPILocalDriftOptions) (map[string]bool, error) {
	existing := openAPIEndpointMap(collection.Items)
	specs := openAPIEndpointMap(specCollection.Items)
	resetIDs := openAPILocalDriftIDSet(options.ResetIDs)
	restoreIDs := openAPILocalDriftIDSet(options.RestoreIDs)
	deleteIDs := openAPILocalDriftIDSet(options.DeleteIDs)
	now := time.Now()
	changed := false

	for _, id := range sortedOpenAPIEndpointIDs(resetIDs) {
		ref, ok := existing[id]
		spec, specOK := specs[id]
		if !ok || !specOK {
			continue
		}
		merged := mergeOpenAPISpecIntoRequest(collection.Items[ref.Index], spec.Item, false)
		merged.ID = collection.Items[ref.Index].ID
		merged.FilePath = collection.Items[ref.Index].FilePath
		merged.FolderPath = collection.Items[ref.Index].FolderPath
		merged.Seq = collection.Items[ref.Index].Seq
		merged.CreatedAt = collection.Items[ref.Index].CreatedAt
		merged.UpdatedAt = now
		collection.Items[ref.Index] = merged
		changed = true
	}

	maxSeq := 0
	for _, item := range collection.Items {
		if item.Seq > maxSeq {
			maxSeq = item.Seq
		}
	}
	for _, id := range sortedOpenAPIEndpointIDs(restoreIDs) {
		if _, ok := existing[id]; ok {
			continue
		}
		spec, ok := specs[id]
		if !ok {
			continue
		}
		next := spec.Item
		next.ID = scalar.NewID("request")
		maxSeq++
		next.Seq = maxSeq
		next.CreatedAt = now
		next.UpdatedAt = now
		types.AssignExampleIDs(&next)
		collection.Items = append(collection.Items, next)
		changed = true
	}

	removedIDs := map[string]bool{}
	nextItems := collection.Items[:0]
	for _, item := range collection.Items {
		id := OpenAPIEndpointID(item)
		if deleteIDs[id] {
			if _, specExists := specs[id]; !specExists {
				if err := removeOpenAPILocalDriftRequestFile(collection, item); err != nil {
					return nil, err
				}
				removedIDs[item.ID] = true
				changed = true
				continue
			}
		}
		nextItems = append(nextItems, item)
	}
	collection.Items = nextItems

	if changed {
		collection.UpdatedAt = now
	}
	return removedIDs, nil
}

func openAPILocalDriftIDSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func removeOpenAPILocalDriftRequestFile(collection *types.Collection, item types.RequestItem) error {
	target := RequestFilePath(*collection, item, RequestFileExtensionForCollection(*collection))
	if scripting.PathInside(collection.Path, item.FilePath) {
		target = filepath.Clean(item.FilePath)
	}
	if !scripting.PathInside(collection.Path, target) {
		return fmt.Errorf("request path %s escapes collection", target)
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func openAPILocalDriftRequestChanges(spec, actual types.RequestItem) []string {
	var changes []string
	if !stringSlicesEqual(openAPILocalDriftKeyValueNames(spec.PathParams, false), openAPILocalDriftKeyValueNames(actual.PathParams, false)) {
		changes = append(changes, "path params")
	}
	if !stringSlicesEqual(openAPILocalDriftKeyValueNames(spec.Params, false), openAPILocalDriftKeyValueNames(actual.Params, false)) {
		changes = append(changes, "query params")
	}
	if !stringSlicesEqual(openAPILocalDriftKeyValueNames(spec.Headers, true), openAPILocalDriftKeyValueNames(actual.Headers, true)) {
		changes = append(changes, "headers")
	}
	specBodyMode := normalizeOpenAPILocalDriftMode(spec.Body.Mode, "none")
	actualBodyMode := normalizeOpenAPILocalDriftMode(actual.Body.Mode, "none")
	if specBodyMode != actualBodyMode {
		changes = append(changes, "body mode")
	} else {
		switch specBodyMode {
		case "json":
			if !stringSlicesEqual(openAPILocalDriftJSONShape(spec.Body.JSON), openAPILocalDriftJSONShape(actual.Body.JSON)) {
				changes = append(changes, "json body")
			}
		case "formurlencoded":
			if !stringSlicesEqual(openAPILocalDriftKeyValueNames(spec.Body.FormURLEncoded, false), openAPILocalDriftKeyValueNames(actual.Body.FormURLEncoded, false)) {
				changes = append(changes, "form body")
			}
		case "multipartform":
			if !stringSlicesEqual(openAPILocalDriftMultipartNames(spec.Body.Multipart), openAPILocalDriftMultipartNames(actual.Body.Multipart)) {
				changes = append(changes, "multipart body")
			}
		}
	}
	if normalizeOpenAPILocalDriftMode(spec.Auth.Mode, "none") != normalizeOpenAPILocalDriftMode(actual.Auth.Mode, "none") {
		changes = append(changes, "auth")
	}
	return changes
}

func openAPILocalDriftKeyValueNames(values []types.KeyValue, foldCase bool) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		if foldCase {
			name = strings.ToLower(name)
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func openAPILocalDriftMultipartNames(values []types.FormPart) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		kind := "text"
		if strings.TrimSpace(value.FilePath) != "" {
			kind = "file"
		}
		out = append(out, name+":"+kind)
	}
	sort.Strings(out)
	return out
}

func normalizeOpenAPILocalDriftMode(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = fallback
	}
	return strings.ReplaceAll(value, "-", "")
}

func openAPILocalDriftJSONShape(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	masked, _ := maskJSONInterpolations(input, "BRU_D")
	var value interface{}
	if err := json.Unmarshal([]byte(masked), &value); err != nil {
		return []string{"<json>"}
	}
	out := []string{}
	collectOpenAPILocalDriftJSONShape(value, "", &out)
	sort.Strings(out)
	return uniqueStrings(out)
}

func collectOpenAPILocalDriftJSONShape(value interface{}, prefix string, out *[]string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			*out = append(*out, path)
			collectOpenAPILocalDriftJSONShape(typed[key], path, out)
		}
	case []interface{}:
		arrayPath := "[]"
		if prefix != "" {
			arrayPath = prefix + "[]"
		}
		*out = append(*out, arrayPath)
		if len(typed) > 0 {
			collectOpenAPILocalDriftJSONShape(typed[0], arrayPath, out)
		}
	}
}

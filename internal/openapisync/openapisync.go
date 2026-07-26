// Package openapisync re-imports an OpenAPI spec over an existing collection
// and works out what changed.
//
// US-034 follow-on. The merge rules are the whole point: a re-sync must pick up
// the spec's structural changes WITHOUT discarding the values the user typed
// into the request. Getting that backwards silently reverts someone's work.
package openapisync

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"

	"gopkg.in/yaml.v3"
)

func OpenAPISyncSpecLooksYAML(content string) bool {
	var jsonValue interface{}
	if json.Unmarshal([]byte(content), &jsonValue) == nil {
		return false
	}
	var yamlValue interface{}
	return yaml.Unmarshal([]byte(content), &yamlValue) == nil
}

func OpenAPISyncCollectionFromContent(content, fallbackName, groupBy string) (types.Collection, string, importers.OpenAPIDoc, error) {
	hash, doc, err := ValidateOpenAPISyncSpec(content)
	if err != nil {
		return types.Collection{}, "", importers.OpenAPIDoc{}, err
	}
	collection, err := importers.ImportOpenAPI(content, fallbackName, NormalizeGroupBy(groupBy))
	if err != nil {
		return types.Collection{}, "", importers.OpenAPIDoc{}, err
	}
	return collection, hash, doc, nil
}

func ValidateOpenAPISyncSpec(content string) (string, importers.OpenAPIDoc, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return "", importers.OpenAPIDoc{}, fmt.Errorf("parse openapi: %w", err)
	}
	if _, ok := raw["swagger"]; ok {
		return "", importers.OpenAPIDoc{}, errors.New("OpenAPI sync supports OpenAPI 3.x specifications; Swagger 2.0 is not supported")
	}
	var doc importers.OpenAPIDoc
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", importers.OpenAPIDoc{}, fmt.Errorf("parse openapi: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(doc.OpenAPI), "3.") {
		return "", importers.OpenAPIDoc{}, errors.New("OpenAPI sync supports OpenAPI 3.x specifications")
	}
	if len(doc.Paths) == 0 && len(doc.Webhooks) == 0 {
		return "", importers.OpenAPIDoc{}, errors.New("OpenAPI document contains no supported operations")
	}
	canonical, err := json.Marshal(raw)
	if err != nil {
		return "", importers.OpenAPIDoc{}, err
	}
	sum := md5.Sum(canonical)
	return hex.EncodeToString(sum[:]), doc, nil
}

func BuildOpenAPISpecDiffLines(storedContent, newContent string) []types.OpenAPISyncSpecDiffLine {
	oldLines := splitOpenAPISpecDiffContent(storedContent)
	newLines := splitOpenAPISpecDiffContent(newContent)
	lines := make([]types.OpenAPISyncSpecDiffLine, 0, max(len(oldLines), len(newLines)))
	for oldIndex, newIndex := 0, 0; oldIndex < len(oldLines) || newIndex < len(newLines); {
		switch {
		case oldIndex >= len(oldLines):
			lines = append(lines, types.OpenAPISyncSpecDiffLine{Kind: "added", NewNumber: newIndex + 1, NewText: newLines[newIndex]})
			newIndex++
		case newIndex >= len(newLines):
			lines = append(lines, types.OpenAPISyncSpecDiffLine{Kind: "removed", OldNumber: oldIndex + 1, OldText: oldLines[oldIndex]})
			oldIndex++
		case oldLines[oldIndex] == newLines[newIndex]:
			lines = append(lines, types.OpenAPISyncSpecDiffLine{Kind: "same", OldNumber: oldIndex + 1, NewNumber: newIndex + 1, OldText: oldLines[oldIndex], NewText: newLines[newIndex]})
			oldIndex++
			newIndex++
		case oldIndex+1 < len(oldLines) && oldLines[oldIndex+1] == newLines[newIndex]:
			lines = append(lines, types.OpenAPISyncSpecDiffLine{Kind: "removed", OldNumber: oldIndex + 1, OldText: oldLines[oldIndex]})
			oldIndex++
		case newIndex+1 < len(newLines) && oldLines[oldIndex] == newLines[newIndex+1]:
			lines = append(lines, types.OpenAPISyncSpecDiffLine{Kind: "added", NewNumber: newIndex + 1, NewText: newLines[newIndex]})
			newIndex++
		default:
			lines = append(lines, types.OpenAPISyncSpecDiffLine{Kind: "changed", OldNumber: oldIndex + 1, NewNumber: newIndex + 1, OldText: oldLines[oldIndex], NewText: newLines[newIndex]})
			oldIndex++
			newIndex++
		}
	}
	return lines
}

func splitOpenAPISpecDiffContent(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func BuildOpenAPISyncResult(collection, specCollection types.Collection, sourceURL, groupBy, hash string, doc importers.OpenAPIDoc) types.OpenAPISyncResult {
	existing := openAPIEndpointMap(collection.Items)
	specs := openAPIEndpointMap(specCollection.Items)
	result := types.OpenAPISyncResult{
		SourceURL:     strings.TrimSpace(sourceURL),
		GroupBy:       NormalizeGroupBy(groupBy),
		SpecHash:      hash,
		Title:         doc.Info.Title,
		Version:       doc.Info.Version,
		EndpointCount: len(specs),
		LastSyncDate:  FirstConfig(collection).LastSyncDate,
	}
	seen := map[string]bool{}
	for _, spec := range specCollection.Items {
		id := OpenAPIEndpointID(spec)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		method, path := splitOpenAPIEndpointID(id)
		existingRef, ok := existing[id]
		if !ok {
			result.Added++
			result.Changes = append(result.Changes, types.OpenAPISyncEndpointChange{ID: id, Method: method, Path: path, Name: spec.Name, Change: "added", DefaultDecision: "accept-incoming"})
			continue
		}
		merged := mergeOpenAPISpecIntoRequest(existingRef.Item, spec, true)
		if openAPISyncRequestFieldsEqual(existingRef.Item, merged) {
			result.Unchanged++
			continue
		}
		result.Updated++
		result.Changes = append(result.Changes, types.OpenAPISyncEndpointChange{ID: id, Method: method, Path: path, Name: scalar.FirstNonEmpty(existingRef.Item.Name, spec.Name), Change: "updated", ItemID: existingRef.Item.ID, DefaultDecision: "accept-incoming"})
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
		result.Removed++
		result.Changes = append(result.Changes, types.OpenAPISyncEndpointChange{ID: id, Method: method, Path: path, Name: ref.Item.Name, Change: "removed", ItemID: ref.Item.ID, DefaultDecision: "accept-incoming"})
	}
	result.HasChanges = result.Added+result.Updated+result.Removed > 0
	return result
}

type openAPIEndpointRef struct {
	Index int
	Item  types.RequestItem
}

func openAPIEndpointMap(items []types.RequestItem) map[string]openAPIEndpointRef {
	out := map[string]openAPIEndpointRef{}
	for index, item := range items {
		if item.Type != "" && item.Type != "http" && item.Type != "graphql" {
			continue
		}
		id := OpenAPIEndpointID(item)
		if id == "" {
			continue
		}
		if _, exists := out[id]; !exists {
			out[id] = openAPIEndpointRef{Index: index, Item: item}
		}
	}
	return out
}

func OpenAPIEndpointID(item types.RequestItem) string {
	method := strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet))
	path := normalizeOpenAPIEndpointPath(item.URL)
	if method == "" || path == "" {
		return ""
	}
	return method + ":" + path
}

func splitOpenAPIEndpointID(id string) (string, string) {
	method, path, ok := strings.Cut(id, ":")
	if !ok {
		return id, ""
	}
	return method, path
}

func normalizeOpenAPIEndpointPath(rawURL string) string {
	value := strings.TrimSpace(rawURL)
	if value == "" {
		return ""
	}
	value = regexp.MustCompile(`\{\{[^}]+\}\}`).ReplaceAllString(value, "")
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.EscapedPath()
	}
	if cut, _, ok := strings.Cut(value, "?"); ok {
		value = cut
	}
	value = regexp.MustCompile(`\{([^}]+)\}`).ReplaceAllString(value, ":$1")
	value = strings.ReplaceAll(value, "//", "/")
	for strings.Contains(value, "//") {
		value = strings.ReplaceAll(value, "//", "/")
	}
	value = strings.TrimRight(value, "/")
	if value == "" {
		value = "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
}

func ApplyOpenAPISyncToCollection(collection *types.Collection, specCollection types.Collection, options types.OpenAPISyncOptions) map[string]bool {
	preserveValues := options.PreserveValues
	decisions := normalizeOpenAPISyncEndpointDecisions(options.EndpointDecisions)
	existing := openAPIEndpointMap(collection.Items)
	specIDs := map[string]bool{}
	maxSeq := 0
	for _, item := range collection.Items {
		if item.Seq > maxSeq {
			maxSeq = item.Seq
		}
	}
	for _, spec := range specCollection.Items {
		id := OpenAPIEndpointID(spec)
		if id == "" || specIDs[id] {
			continue
		}
		specIDs[id] = true
		if ref, ok := existing[id]; ok {
			if openAPISyncEndpointDecision(decisions, id, "accept-incoming") == "keep-mine" {
				continue
			}
			merged := mergeOpenAPISpecIntoRequest(collection.Items[ref.Index], spec, preserveValues)
			merged.ID = collection.Items[ref.Index].ID
			merged.FilePath = collection.Items[ref.Index].FilePath
			merged.FolderPath = collection.Items[ref.Index].FolderPath
			merged.Seq = collection.Items[ref.Index].Seq
			merged.CreatedAt = collection.Items[ref.Index].CreatedAt
			merged.UpdatedAt = time.Now()
			collection.Items[ref.Index] = merged
			continue
		}
		if openAPISyncEndpointDecision(decisions, id, "accept-incoming") == "keep-mine" {
			continue
		}
		next := spec
		if strings.TrimSpace(next.ID) == "" {
			next.ID = scalar.NewID("request")
		}
		maxSeq++
		next.Seq = maxSeq
		next.CreatedAt = time.Now()
		next.UpdatedAt = time.Now()
		collection.Items = append(collection.Items, next)
	}
	removedIDs := map[string]bool{}
	next := collection.Items[:0]
	for _, item := range collection.Items {
		id := OpenAPIEndpointID(item)
		if id != "" && !specIDs[id] {
			defaultDecision := "accept-incoming"
			if openAPISyncEndpointDecision(decisions, id, defaultDecision) == "accept-incoming" {
				removedIDs[item.ID] = true
				continue
			}
		}
		next = append(next, item)
	}
	collection.Items = next
	collection.Variables = mergeOpenAPIVariablesPreserving(specCollection.Variables, collection.Variables, preserveValues)
	collection.UpdatedAt = time.Now()
	return removedIDs
}

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

func sortedOpenAPIEndpointIDs(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
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

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	var previous string
	for index, value := range values {
		if index == 0 || value != previous {
			out = append(out, value)
		}
		previous = value
	}
	return out
}

func normalizeOpenAPISyncEndpointDecisions(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for id, decision := range values {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(decision)) {
		case "accept", "accept-incoming", "incoming", "add", "apply", "remove", "reset":
			out[id] = "accept-incoming"
		case "keep", "keep-mine", "mine", "skip":
			out[id] = "keep-mine"
		}
	}
	return out
}

func openAPISyncEndpointDecision(decisions map[string]string, id, fallback string) string {
	if decision, ok := decisions[id]; ok {
		return decision
	}
	switch fallback {
	case "keep-mine":
		return "keep-mine"
	default:
		return "accept-incoming"
	}
}

func mergeOpenAPISpecIntoRequest(existing, spec types.RequestItem, preserveValues bool) types.RequestItem {
	if !preserveValues {
		out := existing
		out.Method = spec.Method
		out.URL = spec.URL
		out.PathParams = append([]types.KeyValue(nil), spec.PathParams...)
		out.Params = append([]types.KeyValue(nil), spec.Params...)
		out.Headers = append([]types.KeyValue(nil), spec.Headers...)
		out.Body = spec.Body
		out.Auth = spec.Auth
		out.Tags = append([]string(nil), spec.Tags...)
		out.Docs = scalar.FirstNonEmpty(existing.Docs, spec.Docs)
		return out
	}
	out := existing
	out.URL = spec.URL
	out.PathParams = mergeKeyValueListPreserving(spec.PathParams, existing.PathParams, true)
	out.Params = mergeKeyValueListPreserving(spec.Params, existing.Params, true)
	out.Headers = mergeKeyValueListPreserving(spec.Headers, existing.Headers, true)
	out.Body = mergeRequestBodyPreserving(existing.Body, spec.Body, true)
	out.Auth = mergeAuthPreserving(existing.Auth, spec.Auth, true)
	return out
}

func openAPISyncRequestFieldsEqual(a, b types.RequestItem) bool {
	return a.URL == b.URL &&
		reflect.DeepEqual(a.PathParams, b.PathParams) &&
		reflect.DeepEqual(a.Params, b.Params) &&
		reflect.DeepEqual(a.Headers, b.Headers) &&
		reflect.DeepEqual(a.Body, b.Body) &&
		reflect.DeepEqual(a.Auth, b.Auth)
}

func mergeKeyValueListPreserving(specItems, existingItems []types.KeyValue, preserveValues bool) []types.KeyValue {
	spec := append([]types.KeyValue(nil), specItems...)
	if !preserveValues {
		return spec
	}
	cursorByName := map[string]int{}
	out := make([]types.KeyValue, 0, len(spec))
	for _, specEntry := range spec {
		matches := []types.KeyValue{}
		for _, existing := range existingItems {
			if existing.Name == specEntry.Name {
				matches = append(matches, existing)
			}
		}
		cursor := cursorByName[specEntry.Name]
		if cursor >= len(matches) {
			out = append(out, specEntry)
			continue
		}
		picked := matches[cursor]
		cursorByName[specEntry.Name] = cursor + 1
		merged := specEntry
		merged.Value = picked.Value
		merged.Enabled = picked.Enabled
		out = append(out, merged)
	}
	return out
}

func mergeFormPartListPreserving(specItems, existingItems []types.FormPart, preserveValues bool) []types.FormPart {
	spec := append([]types.FormPart(nil), specItems...)
	if !preserveValues {
		return spec
	}
	cursorByName := map[string]int{}
	out := make([]types.FormPart, 0, len(spec))
	for _, specEntry := range spec {
		matches := []types.FormPart{}
		for _, existing := range existingItems {
			if existing.Name == specEntry.Name {
				matches = append(matches, existing)
			}
		}
		cursor := cursorByName[specEntry.Name]
		if cursor >= len(matches) {
			out = append(out, specEntry)
			continue
		}
		picked := matches[cursor]
		cursorByName[specEntry.Name] = cursor + 1
		merged := specEntry
		merged.Value = picked.Value
		merged.FilePath = picked.FilePath
		merged.ContentType = picked.ContentType
		merged.Enabled = picked.Enabled
		out = append(out, merged)
	}
	return out
}

func mergeOpenAPIVariablesPreserving(specItems, existingItems []types.Variable, preserveValues bool) []types.Variable {
	spec := append([]types.Variable(nil), specItems...)
	if !preserveValues {
		return spec
	}
	existingByName := map[string]types.Variable{}
	for _, existing := range existingItems {
		existingByName[existing.Name] = existing
	}
	out := make([]types.Variable, 0, len(spec))
	for _, specEntry := range spec {
		if existing, ok := existingByName[specEntry.Name]; ok {
			merged := specEntry
			merged.ID = scalar.FirstNonEmpty(existing.ID, specEntry.ID)
			merged.Value = existing.Value
			merged.Enabled = existing.Enabled
			merged.Secret = existing.Secret
			out = append(out, merged)
			continue
		}
		out = append(out, specEntry)
	}
	return out
}

func mergeRequestBodyPreserving(existing, spec types.RequestBody, preserveValues bool) types.RequestBody {
	if !preserveValues {
		return spec
	}
	specMode := scalar.FirstNonEmpty(spec.Mode, "none")
	existingMode := scalar.FirstNonEmpty(existing.Mode, "none")
	if specMode != existingMode {
		return spec
	}
	switch specMode {
	case "json":
		return mergeJSONRequestBodyPreserving(existing, spec)
	case "formUrlEncoded":
		spec.FormURLEncoded = mergeKeyValueListPreserving(spec.FormURLEncoded, existing.FormURLEncoded, true)
		return spec
	case "multipartForm":
		spec.Multipart = mergeFormPartListPreserving(spec.Multipart, existing.Multipart, true)
		return spec
	case "graphql":
		if strings.TrimSpace(existing.GraphQLQuery) != "" || strings.TrimSpace(existing.GraphQLVariables) != "" {
			return existing
		}
		return spec
	default:
		return existing
	}
}

func mergeJSONRequestBodyPreserving(existing, spec types.RequestBody) types.RequestBody {
	if strings.TrimSpace(existing.JSON) == "" || strings.TrimSpace(spec.JSON) == "" {
		return spec
	}
	userMasked, userVars := maskJSONInterpolations(existing.JSON, "BRU_U")
	specMasked, specVars := maskJSONInterpolations(spec.JSON, "BRU_S")
	var userValue interface{}
	var specValue interface{}
	if err := json.Unmarshal([]byte(userMasked), &userValue); err != nil {
		return existing
	}
	if err := json.Unmarshal([]byte(specMasked), &specValue); err != nil {
		return existing
	}
	merged := mergeJSONValuesPreserving(userValue, specValue)
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return existing
	}
	jsonText := unmaskJSONInterpolations(string(data), userVars, "BRU_U")
	jsonText = unmaskJSONInterpolations(jsonText, specVars, "BRU_S")
	spec.JSON = jsonText
	return spec
}

func mergeJSONValuesPreserving(userValue, specValue interface{}) interface{} {
	specMap, specMapOK := specValue.(map[string]interface{})
	userMap, userMapOK := userValue.(map[string]interface{})
	if specMapOK && userMapOK {
		out := map[string]interface{}{}
		keys := make([]string, 0, len(specMap))
		for key := range specMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if userChild, ok := userMap[key]; ok {
				out[key] = mergeJSONValuesPreserving(userChild, specMap[key])
			} else {
				out[key] = specMap[key]
			}
		}
		return out
	}
	specArray, specArrayOK := specValue.([]interface{})
	userArray, userArrayOK := userValue.([]interface{})
	if specArrayOK && userArrayOK {
		if len(userArray) == 0 {
			return specArray
		}
		if len(specArray) == 0 {
			return userArray
		}
		out := make([]interface{}, 0, len(userArray))
		for _, userChild := range userArray {
			out = append(out, mergeJSONValuesPreserving(userChild, specArray[0]))
		}
		return out
	}
	if userValue == nil {
		return specValue
	}
	return userValue
}

const openAPISyncJSONSentinel = "\ue000"

func maskJSONInterpolations(input, prefix string) (string, []string) {
	var tokens []string
	var out strings.Builder
	inString := false
	for i := 0; i < len(input); {
		ch := input[i]
		if ch == '"' {
			backslashes := 0
			for j := i - 1; j >= 0 && input[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				inString = !inString
			}
			out.WriteByte(ch)
			i++
			continue
		}
		if ch == '{' && i+1 < len(input) && input[i+1] == '{' {
			if end := strings.Index(input[i+2:], "}}"); end >= 0 {
				tokenEnd := i + 2 + end + 2
				token := input[i:tokenEnd]
				index := len(tokens)
				tokens = append(tokens, token)
				if inString {
					out.WriteString(openAPISyncJSONSentinel + prefix + "_S_" + strconv.Itoa(index) + openAPISyncJSONSentinel)
				} else {
					out.WriteString(`"` + openAPISyncJSONSentinel + prefix + "_V_" + strconv.Itoa(index) + openAPISyncJSONSentinel + `"`)
				}
				i = tokenEnd
				continue
			}
		}
		out.WriteByte(ch)
		i++
	}
	return out.String(), tokens
}

func unmaskJSONInterpolations(input string, tokens []string, prefix string) string {
	for index, token := range tokens {
		valueSentinel := openAPISyncJSONSentinel + prefix + "_V_" + strconv.Itoa(index) + openAPISyncJSONSentinel
		stringSentinel := openAPISyncJSONSentinel + prefix + "_S_" + strconv.Itoa(index) + openAPISyncJSONSentinel
		input = strings.ReplaceAll(input, `"`+valueSentinel+`"`, token)
		input = strings.ReplaceAll(input, stringSentinel, token)
	}
	return input
}

func mergeAuthPreserving(existing, spec types.AuthConfig, preserveValues bool) types.AuthConfig {
	if !preserveValues {
		return spec
	}
	existingMode := scalar.FirstNonEmpty(existing.Mode, "none")
	specMode := scalar.FirstNonEmpty(spec.Mode, "none")
	if existingMode != specMode {
		return spec
	}
	if specMode == "none" || specMode == "inherit" {
		return spec
	}
	out := spec
	switch specMode {
	case "basic", "digest", "wsse":
		out.Username = scalar.FirstNonEmpty(existing.Username, spec.Username)
		out.Password = scalar.FirstNonEmpty(existing.Password, spec.Password)
	case "ntlm":
		out.Username = scalar.FirstNonEmpty(existing.Username, spec.Username)
		out.Password = scalar.FirstNonEmpty(existing.Password, spec.Password)
		out.Domain = scalar.FirstNonEmpty(existing.Domain, spec.Domain)
	case "bearer":
		out.Token = scalar.FirstNonEmpty(existing.Token, spec.Token)
	case "apikey":
		out.APIKey = scalar.FirstNonEmpty(existing.APIKey, spec.APIKey)
		out.APIValue = scalar.FirstNonEmpty(existing.APIValue, spec.APIValue)
		out.APILocation = scalar.FirstNonEmpty(existing.APILocation, spec.APILocation)
	case "oauth1":
		out.OAuth1 = mergeOAuth1Preserving(existing.OAuth1, spec.OAuth1)
	case "oauth2":
		out.OAuth2 = mergeOAuth2Preserving(existing.OAuth2, spec.OAuth2)
	case "awsv4":
		out.AWSV4 = mergeAWSV4Preserving(existing.AWSV4, spec.AWSV4)
	}
	return out
}

func mergeOAuth1Preserving(existing, spec types.OAuth1Auth) types.OAuth1Auth {
	out := spec
	out.ConsumerKey = scalar.FirstNonEmpty(existing.ConsumerKey, spec.ConsumerKey)
	out.ConsumerSecret = scalar.FirstNonEmpty(existing.ConsumerSecret, spec.ConsumerSecret)
	out.AccessToken = scalar.FirstNonEmpty(existing.AccessToken, spec.AccessToken)
	out.AccessTokenSecret = scalar.FirstNonEmpty(existing.AccessTokenSecret, spec.AccessTokenSecret)
	out.CallbackURL = scalar.FirstNonEmpty(existing.CallbackURL, spec.CallbackURL)
	out.Verifier = scalar.FirstNonEmpty(existing.Verifier, spec.Verifier)
	out.SignatureMethod = scalar.FirstNonEmpty(existing.SignatureMethod, spec.SignatureMethod)
	out.PrivateKey = scalar.FirstNonEmpty(existing.PrivateKey, spec.PrivateKey)
	out.PrivateKeyType = scalar.FirstNonEmpty(existing.PrivateKeyType, spec.PrivateKeyType)
	out.Timestamp = scalar.FirstNonEmpty(existing.Timestamp, spec.Timestamp)
	out.Nonce = scalar.FirstNonEmpty(existing.Nonce, spec.Nonce)
	out.Version = scalar.FirstNonEmpty(existing.Version, spec.Version)
	out.Realm = scalar.FirstNonEmpty(existing.Realm, spec.Realm)
	out.Placement = scalar.FirstNonEmpty(existing.Placement, spec.Placement)
	out.IncludeBodyHash = existing.IncludeBodyHash || spec.IncludeBodyHash
	return out
}

func mergeOAuth2Preserving(existing, spec types.OAuth2Auth) types.OAuth2Auth {
	out := spec
	out.GrantType = scalar.FirstNonEmpty(existing.GrantType, spec.GrantType)
	out.CallbackURL = scalar.FirstNonEmpty(existing.CallbackURL, spec.CallbackURL)
	out.AuthorizationURL = scalar.FirstNonEmpty(existing.AuthorizationURL, spec.AuthorizationURL)
	out.AccessTokenURL = scalar.FirstNonEmpty(existing.AccessTokenURL, spec.AccessTokenURL)
	out.RefreshTokenURL = scalar.FirstNonEmpty(existing.RefreshTokenURL, spec.RefreshTokenURL)
	out.Username = scalar.FirstNonEmpty(existing.Username, spec.Username)
	out.Password = scalar.FirstNonEmpty(existing.Password, spec.Password)
	out.ClientID = scalar.FirstNonEmpty(existing.ClientID, spec.ClientID)
	out.ClientSecret = scalar.FirstNonEmpty(existing.ClientSecret, spec.ClientSecret)
	out.Scope = scalar.FirstNonEmpty(existing.Scope, spec.Scope)
	out.State = scalar.FirstNonEmpty(existing.State, spec.State)
	out.CredentialsPlacement = scalar.FirstNonEmpty(existing.CredentialsPlacement, spec.CredentialsPlacement)
	out.CredentialsID = scalar.FirstNonEmpty(existing.CredentialsID, spec.CredentialsID)
	out.TokenSource = scalar.FirstNonEmpty(existing.TokenSource, spec.TokenSource)
	out.TokenPlacement = scalar.FirstNonEmpty(existing.TokenPlacement, spec.TokenPlacement)
	out.TokenHeaderPrefix = scalar.FirstNonEmpty(existing.TokenHeaderPrefix, spec.TokenHeaderPrefix)
	out.TokenQueryKey = scalar.FirstNonEmpty(existing.TokenQueryKey, spec.TokenQueryKey)
	out.PKCE = existing.PKCE || spec.PKCE
	out.AutoFetchToken = existing.AutoFetchToken || spec.AutoFetchToken
	out.AutoRefreshToken = existing.AutoRefreshToken || spec.AutoRefreshToken
	if len(existing.AuthorizationAdditionalParams) > 0 {
		out.AuthorizationAdditionalParams = append([]types.OAuth2AdditionalParam(nil), existing.AuthorizationAdditionalParams...)
	}
	if len(existing.TokenAdditionalParams) > 0 {
		out.TokenAdditionalParams = append([]types.OAuth2AdditionalParam(nil), existing.TokenAdditionalParams...)
	}
	if len(existing.RefreshAdditionalParams) > 0 {
		out.RefreshAdditionalParams = append([]types.OAuth2AdditionalParam(nil), existing.RefreshAdditionalParams...)
	}
	if len(existing.AdditionalParams) > 0 {
		out.AdditionalParams = append([]types.KeyValue(nil), existing.AdditionalParams...)
	}
	return out
}

func mergeAWSV4Preserving(existing, spec types.AWSV4Auth) types.AWSV4Auth {
	out := spec
	out.AccessKeyID = scalar.FirstNonEmpty(existing.AccessKeyID, spec.AccessKeyID)
	out.SecretAccessKey = scalar.FirstNonEmpty(existing.SecretAccessKey, spec.SecretAccessKey)
	out.SessionToken = scalar.FirstNonEmpty(existing.SessionToken, spec.SessionToken)
	out.Service = scalar.FirstNonEmpty(existing.Service, spec.Service)
	out.Region = scalar.FirstNonEmpty(existing.Region, spec.Region)
	out.ProfileName = scalar.FirstNonEmpty(existing.ProfileName, spec.ProfileName)
	out.AccessKey = scalar.FirstNonEmpty(existing.AccessKey, spec.AccessKey)
	out.SecretKey = scalar.FirstNonEmpty(existing.SecretKey, spec.SecretKey)
	return out
}

func FirstConfig(collection types.Collection) types.OpenAPISyncConfig {
	if len(collection.OpenAPI) == 0 {
		return types.OpenAPISyncConfig{GroupBy: "tag", AutoCheck: true, AutoCheckInterval: 5}
	}
	return NormalizeConfig(collection.OpenAPI[0])
}

func NormalizeConfig(config types.OpenAPISyncConfig) types.OpenAPISyncConfig {
	config.SourceURL = strings.TrimSpace(config.SourceURL)
	config.GroupBy = NormalizeGroupBy(config.GroupBy)
	config.AutoCheckInterval = NormalizeAutoCheckInterval(config.AutoCheckInterval)
	return config
}

func NormalizeGroupBy(groupBy string) string {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "path", "paths":
		return "path"
	default:
		return "tag"
	}
}

func NormalizeAutoCheckInterval(interval int) int {
	if interval <= 0 {
		return 5
	}
	return interval
}

func RequestFilePath(collection types.Collection, item types.RequestItem, defaultExt string) string {
	if scripting.PathInside(collection.Path, item.FilePath) {
		return filepath.Clean(item.FilePath)
	}
	filename := scalar.SanitizeFilename(item.Name)
	if filename == "" {
		filename = item.ID
	}
	folder := filepath.Clean(collection.Path)
	if strings.TrimSpace(item.FolderPath) != "" {
		folder = filepath.Join(folder, filepath.FromSlash(item.FolderPath))
	}
	return filepath.Join(folder, filename+defaultExt)
}

func RequestFileExtensionForCollection(collection types.Collection) string {
	if strings.EqualFold(collection.Format, "yml") || strings.EqualFold(collection.Format, "yaml") {
		return ".yml"
	}
	return ".bru"
}

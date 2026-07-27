package core

import (
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/types"
)

func yamlVariables(values []Variable) []map[string]interface{} { return bru.YAMLVariables(values) }

func requestFilePath(collection Collection, item RequestItem, defaultExt string) string {
	return openapisync.RequestFilePath(collection, item, defaultExt)
}

func requestFileExtensionForCollection(collection Collection) string {
	return openapisync.RequestFileExtensionForCollection(collection)
}

func assignExampleIDs(item *RequestItem) { types.AssignExampleIDs(item) }

func normalizeOpenAPISyncAutoCheckInterval(interval int) int {
	return openapisync.NormalizeAutoCheckInterval(interval)
}

func firstOpenAPISyncConfig(collection Collection) OpenAPISyncConfig {
	return openapisync.FirstConfig(collection)
}

func normalizeOpenAPISyncConfig(config OpenAPISyncConfig) OpenAPISyncConfig {
	return openapisync.NormalizeConfig(config)
}

func normalizeOpenAPISyncGroupBy(groupBy string) string { return openapisync.NormalizeGroupBy(groupBy) }

func compareAssertion(actual, operator, expected string) bool {
	return scripting.CompareAssertion(actual, operator, expected)
}

func pathInside(root, candidate string) bool { return scripting.PathInside(root, candidate) }

func keyValuesFromHeaders(headers map[string]string) []KeyValue {
	return scripting.KeyValuesFromHeaders(headers)
}

func previewModeFromHeaders(headers map[string]string) string {
	return scripting.PreviewModeFromHeaders(headers)
}

func normalizeJSSandboxMode(mode string) string { return scripting.NormalizeJSSandboxMode(mode) }

func timelineSourceFileForItem(collectionPath string, item RequestItem) string {
	return scripting.TimelineSourceFileForItem(collectionPath, item)
}

func intValue(raw interface{}, fallback int) int { return scalar.IntValue(raw, fallback) }

func normalizeOAuth2AdditionalPlacement(value string) string {
	return types.NormalizeOAuth2AdditionalPlacement(value)
}

func cloneFolderConfigForFolderClone(folder FolderConfig) FolderConfig {
	return types.CloneFolderConfigForFolderClone(folder)
}

func cloneRequestItemForFolderClone(item RequestItem) RequestItem {
	return types.CloneRequestItemForFolderClone(item)
}

func cloneResponseExample(example ResponseExample) ResponseExample {
	return types.CloneResponseExample(example)
}

func getKeyValue(values []KeyValue, name string) string { return types.GetKeyValue(values, name) }

func boolValueOK(raw interface{}) (bool, bool) { return scalar.BoolValueOK(raw) }

func boolValue(raw interface{}, fallback bool) bool { return scalar.BoolValue(raw, fallback) }

func listValue(raw interface{}) ([]interface{}, bool) { return scalar.ListValue(raw) }

func selectedFileBodyEntry(body RequestBody) (FileBodyEntry, bool) {
	return types.SelectedFileBodyEntry(body)
}

func cleanStatusText(status int, statusText string) string {
	return scalar.CleanStatusText(status, statusText)
}

func cloneKeyValues(values []KeyValue) []KeyValue { return types.CloneKeyValues(values) }

func cloneFormParts(values []FormPart) []FormPart { return types.CloneFormParts(values) }

func cloneFileBodyEntries(values []FileBodyEntry) []FileBodyEntry {
	return types.CloneFileBodyEntries(values)
}

func fileBodyEntries(body RequestBody) []FileBodyEntry { return types.FileBodyEntriesOf(body) }

func requestBodySnapshot(body RequestBody) string { return types.RequestBodySnapshot(body) }

func sanitizeFilename(value string) string { return scalar.SanitizeFilename(value) }

func deterministicID(prefix, input string) string { return scalar.DeterministicID(prefix, input) }

func mapValue(raw interface{}) (map[string]interface{}, bool) {
	return scalar.Map(raw)
}

func yamlScalarString(raw interface{}) string {
	return scalar.YAMLString(raw)
}

func firstNonEmpty(values ...string) string {
	return scalar.FirstNonEmpty(values...)
}

func newID(prefix string) string {
	return scalar.NewID(prefix)
}

// firstYAMLString had a second, byte-identical implementation in
// app_yaml_codec.go until a differential test confirmed the two agreed on every
// shape a YAML map takes. Delegating keeps the 36 call sites unchanged while
// leaving one implementation.
func firstYAMLString(raw map[string]interface{}, keys ...string) string {
	return scalar.FirstYAMLString(raw, keys...)
}

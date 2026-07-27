// Package scalar holds the value-coercion primitives that both the app and the
// importers need: reading a scalar out of decoded YAML or JSON of uncertain
// shape, and the two id/string helpers that go with it.
//
// US-071. These moved out of app.go because internal/importers uses YAMLString
// 33 times and FirstNonEmpty 27 times, and copying them would have left two
// definitions of "what counts as a scalar" free to drift apart.
package scalar

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func Map(raw interface{}) (map[string]interface{}, bool) {
	value, ok := raw.(map[string]interface{})
	if ok {
		return value, true
	}
	legacy, ok := raw.(map[interface{}]interface{})
	if !ok {
		return nil, false
	}
	out := map[string]interface{}{}
	for key, value := range legacy {
		out[fmt.Sprint(key)] = value
	}
	return out, true
}

func YAMLString(raw interface{}) string {
	if raw == nil {
		return ""
	}
	if valueMap, ok := Map(raw); ok {
		if data, ok := valueMap["data"]; ok {
			return YAMLString(data)
		}
	}
	switch value := raw.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		data, err := json.Marshal(value)
		if err == nil {
			return string(data)
		}
		return fmt.Sprint(value)
	}
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func NewID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "-")
	name = replacer.Replace(name)
	name = strings.Trim(name, ". ")
	if name == "" {
		return "untitled"
	}
	return name
}

func DeterministicID(prefix, input string) string {
	sum := sha1.Sum([]byte(input))
	return prefix + "-" + hex.EncodeToString(sum[:8])
}

func AppendScript(existing, next string) string {
	if strings.TrimSpace(next) == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return next
	}
	return existing + "\n" + next
}

// NormalizeWhitespace collapses runs of whitespace to single spaces.
func NormalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// FirstMapValue returns the value of the first key that is present.
//
// Formats spell the same field differently across versions -- Insomnia's v4 and
// v5 exports, Postman's raw vs formdata bodies -- so readers name every spelling
// and take whichever the document actually used.
func FirstMapValue(raw map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}
	return nil
}

// FirstYAMLString is FirstMapValue coerced to a non-blank string.
func FirstYAMLString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(YAMLString(raw[key])); value != "" {
			return value
		}
	}
	return ""
}

func CleanStatusText(status int, statusText string) string {
	statusText = strings.TrimSpace(statusText)
	code := strconv.Itoa(status)
	if strings.HasPrefix(statusText, code) {
		statusText = strings.TrimSpace(strings.TrimPrefix(statusText, code))
	}
	if statusText == "" {
		statusText = http.StatusText(status)
	}
	return statusText
}

func BoolValue(raw interface{}, fallback bool) bool {
	if value, ok := BoolValueOK(raw); ok {
		return value
	}
	return fallback
}

func ListValue(raw interface{}) ([]interface{}, bool) {
	values, ok := raw.([]interface{})
	return values, ok
}

func BoolValueOK(raw interface{}) (bool, bool) {
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return false, false
	}
}

// ShellSingleQuote moved here from internal/grpcexec; it is generic quoting.
func ShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func LooksLikeJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func IntValue(raw interface{}, fallback int) int {
	if value, ok := IntValueOK(raw); ok {
		return value
	}
	return fallback
}

func IntValueOK(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return 0, false
	}
}

// PathInside reports whether candidate lies at or below root.
//
// Here rather than in internal/scripting, where it used to live, because the
// recovery store needs the same containment check and cannot import a package
// four layers above it. scripting keeps a one-line forwarder so its own callers
// are undisturbed.
func PathInside(root, candidate string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(candidate) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

// NormalizeAbsoluteDirectory resolves an absolute directory path, returning ""
// unless it exists and is a directory. Symlinks are followed so two spellings
// of the same directory compare equal.
//
// Here rather than beside the collection importer because the preferences
// normaliser needs the same answer for the default import location, and the
// two must agree or a saved preference would fail the check that accepted it.
func NormalizeAbsoluteDirectory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err == nil {
		value = resolved
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(value)
}

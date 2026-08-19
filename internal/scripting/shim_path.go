package scripting

// The POSIX half of the sandbox node:path shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

func scriptModuleIsLocalPath(name string) bool {
	return strings.HasPrefix(name, ".") || filepath.IsAbs(name)
}

func scriptPathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func scriptPackageMainPath(packageDir string) (string, bool) {
	content, err := os.ReadFile(filepath.Join(packageDir, "package.json"))
	if err != nil {
		return "", false
	}
	var payload struct {
		Main string `json:"main"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return "", false
	}
	mainPath := strings.TrimSpace(payload.Main)
	if mainPath == "" {
		return "", false
	}
	return filepath.Clean(filepath.Join(packageDir, filepath.FromSlash(mainPath))), true
}

func resolveScriptFSPath(root, name, sandboxMode string) (string, error) {
	if NormalizeJSSandboxMode(sandboxMode) == "developer" {
		base := strings.TrimSpace(root)
		if base == "" {
			base = "."
		}
		var candidate string
		if filepath.IsAbs(name) {
			candidate = filepath.Clean(name)
		} else {
			candidate = filepath.Clean(filepath.Join(base, name))
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		return filepath.Clean(absolute), nil
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("collection path is unavailable")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootPath = filepath.Clean(rootPath)
	if evaluatedRoot, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = evaluatedRoot
	}
	var candidate string
	if filepath.IsAbs(name) {
		candidate = filepath.Clean(name)
	} else {
		candidate = filepath.Clean(filepath.Join(rootPath, name))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	candidate = filepath.Clean(candidate)
	if evaluatedCandidate, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = evaluatedCandidate
	}
	rel, err := filepath.Rel(rootPath, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("fs path %q escapes collection", name)
	}
	return candidate, nil
}

func newScriptPathObject(runtime *goja.Runtime) *goja.Object {
	if filepath.Separator == '\\' {
		return newScriptWin32PathObject(runtime)
	}
	return newScriptPosixPathObject(runtime)
}

func newScriptPosixPathObject(runtime *goja.Runtime) *goja.Object {
	pathObject := runtime.NewObject()
	_ = pathObject.Set("sep", "/")
	_ = pathObject.Set("delimiter", ":")
	_ = pathObject.Set("join", func(call goja.FunctionCall) goja.Value {
		parts := scriptCallStringArgs(call)
		if len(parts) == 0 {
			return runtime.ToValue(".")
		}
		return runtime.ToValue(pathpkg.Clean(pathpkg.Join(parts...)))
	})
	_ = pathObject.Set("resolve", func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptPosixPathResolve(scriptCallStringArgs(call)...))
	})
	_ = pathObject.Set("dirname", scriptPosixPathDirname)
	_ = pathObject.Set("basename", scriptPosixPathBasename)
	_ = pathObject.Set("extname", func(value string) string { return scriptPathExtname(value, "/") })
	_ = pathObject.Set("normalize", scriptPosixPathNormalize)
	_ = pathObject.Set("isAbsolute", func(value string) bool { return strings.HasPrefix(value, "/") })
	_ = pathObject.Set("relative", scriptPosixPathRelative)
	_ = pathObject.Set("parse", scriptPosixPathParse)
	_ = pathObject.Set("format", func(value goja.Value) string {
		return scriptPathFormat(runtime, value, "/", false)
	})
	_ = pathObject.Set("toNamespacedPath", func(value string) string { return value })
	_ = pathObject.Set("_makeLong", func(value string) string { return value })
	return pathObject
}

func scriptPosixPathCWD() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return "/"
	}
	return filepath.ToSlash(cwd)
}

func scriptPosixPathResolve(parts ...string) string {
	resolved := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "/") {
			resolved = part
		} else if resolved == "" {
			resolved = part
		} else {
			resolved += "/" + part
		}
	}
	if resolved == "" {
		resolved = scriptPosixPathCWD()
	} else if !strings.HasPrefix(resolved, "/") {
		resolved = scriptPosixPathCWD() + "/" + resolved
	}
	return pathpkg.Clean(resolved)
}

func scriptPosixPathNormalize(value string) string {
	if value == "" {
		return "."
	}
	trailing := strings.HasSuffix(value, "/")
	normalized := pathpkg.Clean(value)
	if trailing && normalized != "/" && normalized != "." {
		normalized += "/"
	}
	return normalized
}

func scriptPosixPathDirname(value string) string {
	if value == "" {
		return "."
	}
	return pathpkg.Dir(value)
}

func scriptPosixPathBasename(value string, ext ...string) string {
	base := scriptPathBasename(value, "/")
	if len(ext) > 0 && ext[0] != "" && strings.HasSuffix(base, ext[0]) {
		return strings.TrimSuffix(base, ext[0])
	}
	return base
}

func scriptPosixPathRelative(from, to string) string {
	from = scriptPosixPathResolve(from)
	to = scriptPosixPathResolve(to)
	if from == to {
		return ""
	}
	fromParts := scriptPathNonEmptyParts(strings.Split(strings.Trim(from, "/"), "/"))
	toParts := scriptPathNonEmptyParts(strings.Split(strings.Trim(to, "/"), "/"))
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	relativeParts := make([]string, 0, len(fromParts)-common+len(toParts)-common)
	for i := common; i < len(fromParts); i++ {
		relativeParts = append(relativeParts, "..")
	}
	relativeParts = append(relativeParts, toParts[common:]...)
	if len(relativeParts) == 0 {
		return ""
	}
	return strings.Join(relativeParts, "/")
}

func scriptPosixPathParse(value string) map[string]string {
	root := ""
	if strings.HasPrefix(value, "/") {
		root = "/"
	}
	base := scriptPosixPathBasename(value)
	ext := scriptPathExtname(value, "/")
	name := strings.TrimSuffix(base, ext)
	dir := scriptPosixPathDirname(value)
	if dir == "." {
		dir = ""
	}
	return map[string]string{
		"root": root,
		"dir":  dir,
		"base": base,
		"ext":  ext,
		"name": name,
	}
}

func scriptPathCleanParts(parts []string, allowAboveRoot bool) []string {
	cleaned := []string{}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(cleaned) > 0 && cleaned[len(cleaned)-1] != ".." {
				cleaned = cleaned[:len(cleaned)-1]
			} else if allowAboveRoot {
				cleaned = append(cleaned, part)
			}
			continue
		}
		cleaned = append(cleaned, part)
	}
	return cleaned
}

func scriptPathNonEmptyParts(parts []string) []string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func scriptPathBasename(value, separator string) string {
	if value == "" {
		return ""
	}
	value = strings.TrimRight(value, separator)
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, separator); index != -1 {
		return value[index+len(separator):]
	}
	return value
}

func scriptPathExtname(value, separator string) string {
	return scriptPathExtnameFromBase(scriptPathBasename(value, separator))
}

func scriptPathExtnameFromBase(base string) string {
	if base == "" || base == "." || base == ".." {
		return ""
	}
	index := strings.LastIndex(base, ".")
	if index <= 0 {
		return ""
	}
	return base[index:]
}

func scriptPathObjectString(object *goja.Object, key string) string {
	value := object.Get(key)
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
}

func scriptPathParams(params []types.KeyValue) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(params))
	for _, param := range params {
		rows = append(rows, map[string]interface{}{
			"name":        param.Name,
			"value":       param.Value,
			"type":        "path",
			"enabled":     param.Enabled,
			"description": param.Description,
		})
	}
	return rows
}

func parseJSONBodyPath(path string) []string {
	keys := []string{}
	for index := 0; index < len(path); {
		switch path[index] {
		case '.':
			index++
		case '[':
			index++
			if index < len(path) && (path[index] == '\'' || path[index] == '"') {
				quote := path[index]
				index++
				var key strings.Builder
				for index < len(path) && path[index] != quote {
					if path[index] == '\\' && index+1 < len(path) && path[index+1] == quote {
						key.WriteByte(quote)
						index += 2
						continue
					}
					key.WriteByte(path[index])
					index++
				}
				if index < len(path) {
					index++
				}
				if index < len(path) && path[index] == ']' {
					index++
				}
				keys = append(keys, key.String())
				continue
			}
			start := index
			for index < len(path) && path[index] != ']' {
				index++
			}
			keys = append(keys, path[start:index])
			if index < len(path) {
				index++
			}
		default:
			start := index
			for index < len(path) && path[index] != '.' && path[index] != '[' {
				index++
			}
			keys = append(keys, path[start:index])
		}
	}
	return keys
}

func splitURLOriginAndPath(value string) (string, string) {
	schemeEnd := strings.Index(value, "://")
	if schemeEnd <= 0 || !validURLScheme(value[:schemeEnd]) {
		return "", value
	}
	authorityStart := schemeEnd + len("://")
	authorityEnd := len(value)
	for i := authorityStart; i < len(value); i++ {
		if value[i] == '/' || value[i] == '?' || value[i] == '#' {
			authorityEnd = i
			break
		}
	}
	return value[:authorityEnd], value[authorityEnd:]
}

func encodeURLPath(path string) string {
	if path == "" {
		return ""
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = encodeURIComponent(safeDecodeURIComponent(segment))
	}
	return strings.Join(segments, "/")
}

func DotEnvScopePath(workspace *types.Workspace, collection *types.Collection, scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "workspace":
		if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
			return "", errors.New("workspace path is required")
		}
		return workspace.Path, nil
	case "collection":
		if collection == nil || strings.TrimSpace(collection.Path) == "" {
			return "", errors.New("collection path is required")
		}
		return collection.Path, nil
	default:
		return "", errors.New(".env scope must be workspace or collection")
	}
}

func PathInside(root, candidate string) bool { return scalar.PathInside(root, candidate) }

func itemFolderPhysicalPath(collection types.Collection, item types.RequestItem) string {
	if PathInside(collection.Path, item.FilePath) {
		rel, err := filepath.Rel(collection.Path, filepath.Dir(item.FilePath))
		if err == nil && rel != "." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(strings.Trim(item.FolderPath, "/"))
}

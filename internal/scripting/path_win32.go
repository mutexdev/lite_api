package scripting

// The Windows half of the sandbox node:path shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

func linkScriptPathVariants(posixPathObject, win32PathObject *goja.Object) {
	_ = posixPathObject.Set("posix", posixPathObject)
	_ = posixPathObject.Set("win32", win32PathObject)
	_ = win32PathObject.Set("posix", posixPathObject)
	_ = win32PathObject.Set("win32", win32PathObject)
}

func newScriptWin32PathObject(runtime *goja.Runtime) *goja.Object {
	pathObject := runtime.NewObject()
	_ = pathObject.Set("sep", "\\")
	_ = pathObject.Set("delimiter", ";")
	_ = pathObject.Set("join", func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptWin32PathJoin(scriptCallStringArgs(call)...))
	})
	_ = pathObject.Set("resolve", func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptWin32PathResolve(scriptCallStringArgs(call)...))
	})
	_ = pathObject.Set("dirname", scriptWin32PathDirname)
	_ = pathObject.Set("basename", scriptWin32PathBasename)
	_ = pathObject.Set("extname", func(value string) string {
		return scriptPathExtnameFromBase(scriptWin32PathBasename(value))
	})
	_ = pathObject.Set("normalize", scriptWin32PathNormalize)
	_ = pathObject.Set("isAbsolute", scriptWin32PathIsAbsolute)
	_ = pathObject.Set("relative", scriptWin32PathRelative)
	_ = pathObject.Set("parse", scriptWin32PathParse)
	_ = pathObject.Set("format", func(value goja.Value) string {
		return scriptPathFormat(runtime, value, "\\", true)
	})
	_ = pathObject.Set("toNamespacedPath", func(value string) string { return value })
	_ = pathObject.Set("_makeLong", func(value string) string { return value })
	return pathObject
}

func scriptWin32PathCWD() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return "\\"
	}
	return scriptWin32NormalizeSeparators(filepath.ToSlash(cwd))
}

func scriptWin32NormalizeSeparators(value string) string {
	return strings.ReplaceAll(value, "/", "\\")
}

func scriptWin32PathSplitRoot(value string) (root, rest string, absolute bool) {
	value = scriptWin32NormalizeSeparators(value)
	if len(value) >= 2 && value[0] == '\\' && value[1] == '\\' {
		trimmed := strings.TrimLeft(value, "\\")
		parts := strings.Split(trimmed, "\\")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			root = "\\\\" + parts[0] + "\\" + parts[1] + "\\"
			if len(parts) > 2 {
				rest = strings.Join(parts[2:], "\\")
			}
			return root, rest, true
		}
		return "\\", strings.TrimLeft(value, "\\"), true
	}
	if len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		drive := value[:2]
		if len(value) >= 3 && value[2] == '\\' {
			return drive + "\\", strings.TrimLeft(value[3:], "\\"), true
		}
		return drive, value[2:], false
	}
	if strings.HasPrefix(value, "\\") {
		return "\\", strings.TrimLeft(value, "\\"), true
	}
	return "", value, false
}

func scriptWin32PathIsAbsolute(value string) bool {
	_, _, absolute := scriptWin32PathSplitRoot(value)
	return absolute
}

func scriptWin32PathNormalize(value string) string {
	if value == "" {
		return "."
	}
	value = scriptWin32NormalizeSeparators(value)
	trailing := strings.HasSuffix(value, "\\")
	root, rest, absolute := scriptWin32PathSplitRoot(value)
	parts := scriptPathCleanParts(strings.Split(rest, "\\"), !absolute)
	body := strings.Join(parts, "\\")
	result := ""
	if root != "" {
		result = root + body
	} else {
		result = body
	}
	if result == "" {
		if root != "" {
			if strings.HasSuffix(root, ":") {
				result = root + "."
			} else {
				result = root
			}
		} else {
			result = "."
		}
	}
	if trailing && result != "." && !strings.HasSuffix(result, "\\") {
		result += "\\"
	}
	return result
}

func scriptWin32PathJoin(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) == 0 {
		return "."
	}
	return scriptWin32PathNormalize(strings.Join(filtered, "\\"))
}

func scriptWin32PathResolve(parts ...string) string {
	resolved := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		part = scriptWin32NormalizeSeparators(part)
		if scriptWin32PathIsAbsolute(part) {
			resolved = part
		} else if resolved == "" {
			resolved = part
		} else {
			resolved += "\\" + part
		}
	}
	if resolved == "" {
		resolved = scriptWin32PathCWD()
	} else if !scriptWin32PathIsAbsolute(resolved) {
		resolved = scriptWin32PathCWD() + "\\" + resolved
	}
	return scriptWin32PathNormalize(resolved)
}

func scriptWin32PathDirname(value string) string {
	if value == "" {
		return "."
	}
	value = scriptWin32TrimTrailingSeparators(scriptWin32NormalizeSeparators(value))
	root, rest, _ := scriptWin32PathSplitRoot(value)
	if rest == "" {
		if root != "" {
			return root
		}
		return "."
	}
	index := strings.LastIndex(rest, "\\")
	if index == -1 {
		if root != "" {
			return root
		}
		return "."
	}
	dirRest := rest[:index]
	if dirRest == "" {
		if root != "" {
			return root
		}
		return "."
	}
	return root + dirRest
}

func scriptWin32PathBasename(value string, ext ...string) string {
	value = scriptWin32NormalizeSeparators(value)
	_, rest, _ := scriptWin32PathSplitRoot(scriptWin32TrimTrailingSeparators(value))
	if rest == "" {
		return ""
	}
	base := scriptPathBasename(rest, "\\")
	if len(ext) > 0 && ext[0] != "" && strings.HasSuffix(base, ext[0]) {
		return strings.TrimSuffix(base, ext[0])
	}
	return base
}

func scriptWin32PathRelative(from, to string) string {
	from = scriptWin32PathResolve(from)
	to = scriptWin32PathResolve(to)
	if strings.EqualFold(from, to) {
		return ""
	}
	fromRoot, fromRest, _ := scriptWin32PathSplitRoot(from)
	toRoot, toRest, _ := scriptWin32PathSplitRoot(to)
	if !strings.EqualFold(fromRoot, toRoot) {
		return to
	}
	fromParts := scriptPathNonEmptyParts(strings.Split(fromRest, "\\"))
	toParts := scriptPathNonEmptyParts(strings.Split(toRest, "\\"))
	common := 0
	for common < len(fromParts) && common < len(toParts) && strings.EqualFold(fromParts[common], toParts[common]) {
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
	return strings.Join(relativeParts, "\\")
}

func scriptWin32PathParse(value string) map[string]string {
	value = scriptWin32NormalizeSeparators(value)
	root, _, _ := scriptWin32PathSplitRoot(value)
	base := scriptWin32PathBasename(value)
	ext := scriptPathExtnameFromBase(base)
	name := strings.TrimSuffix(base, ext)
	dir := scriptWin32PathDirname(value)
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

func scriptWin32TrimTrailingSeparators(value string) string {
	for len(value) > 1 && strings.HasSuffix(value, "\\") {
		root, rest, _ := scriptWin32PathSplitRoot(value)
		if rest == "" && root != "" {
			break
		}
		value = strings.TrimSuffix(value, "\\")
	}
	return value
}

func scriptPathFormat(runtime *goja.Runtime, value goja.Value, separator string, win32 bool) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	object := value.ToObject(runtime)
	dir := scriptPathObjectString(object, "dir")
	root := scriptPathObjectString(object, "root")
	base := scriptPathObjectString(object, "base")
	if base == "" {
		base = scriptPathObjectString(object, "name") + scriptPathObjectString(object, "ext")
	}
	prefix := dir
	if prefix == "" {
		prefix = root
	}
	if prefix == "" {
		return base
	}
	if win32 {
		prefix = scriptWin32NormalizeSeparators(prefix)
		if strings.HasSuffix(prefix, "\\") {
			return prefix + base
		}
		return prefix + "\\" + base
	}
	if strings.HasSuffix(prefix, "/") {
		return prefix + base
	}
	return prefix + "/" + base
}

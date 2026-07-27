package scripting

// require() and Node module resolution inside the sandbox.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

// Compiled-once program caches for the built-in shim sources. Every one of
// these is reached during NewScriptRuntimeWithMeta -- the eight developer-only
// entries via installScriptRequire when the collection runs in "developer"
// sandbox mode, the rest unconditionally -- so caching them removes the whole
// per-runtime parse cost. Caching changes nothing about which shims get
// installed in which sandbox mode: the mode gates remain where they were, and a
// cache slot is only consulted from the call site that already ran that source.
//
// The user script itself, require()'d user module files, and dynamically built
// assertion expressions are deliberately absent: their sources are not fixed,
// so they are not cacheable by this mechanism.
var (
	scriptConsoleModuleShim  = newScriptShimProgram("console")
	scriptBufferShim         = newScriptShimProgram("buffer")
	scriptTimersPromisesShim = newScriptShimProgram("timers/promises")
	scriptAssertShim         = newScriptShimProgram("assert")
	scriptUtilShim           = newScriptShimProgram("util")
	scriptAjvShim            = newScriptShimProgram("ajv")
	scriptAxiosShim          = newScriptShimProgram("axios")
	scriptLodashShim         = newScriptShimProgram("lodash")
	scriptQueryStringShim    = newScriptShimProgram("querystring")
	scriptZlibShim           = newScriptShimProgram("zlib")
	scriptDNSShim            = newScriptShimProgram("dns")
	scriptHTTPShim           = newScriptShimProgram("http")
	scriptEventsShim         = newScriptShimProgram("events")
	scriptStreamShim         = newScriptShimProgram("stream")
	scriptStreamPromisesShim = newScriptShimProgram("stream/promises")
	scriptURLShim            = newScriptShimProgram("url")
	scriptMomentShim         = newScriptShimProgram("moment")
	scriptCryptoJSShim       = newScriptShimProgram("crypto-js")
	scriptEventTargetShim    = newScriptShimProgram("EventTarget")
	scriptEncodingShim       = newScriptShimProgram("TextEncoder/TextDecoder")
	scriptFetchShim          = newScriptShimProgram("fetch")
)

func newScriptConsoleModuleObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const globalConsole = globalThis.console || {};

  function formatValue(value) {
    if (typeof value === "string") return value;
    if (value instanceof Error) return value.name + ": " + value.message;
    try {
      const json = JSON.stringify(value);
      if (json !== undefined) return json;
    } catch (_) {}
    return String(value);
  }

  function formatArgs(args) {
    return Array.prototype.map.call(args, formatValue).join(" ");
  }

  function writeLine(stream, args, fallbackLevel) {
    if (stream && typeof stream.write === "function") {
      stream.write(formatArgs(args) + "\n");
      return;
    }
    const fallback = typeof globalConsole[fallbackLevel] === "function" ? globalConsole[fallbackLevel] : globalConsole.log;
    if (typeof fallback === "function") {
      fallback.apply(globalConsole, args);
    }
  }

  function Console(stdout, stderr) {
    if (!(this instanceof Console)) {
      return new Console(stdout, stderr);
    }
    this._stdout = stdout || null;
    this._stderr = stderr || stdout || null;
  }

  Console.prototype.log = function () {
    writeLine(this._stdout, arguments, "log");
  };
  Console.prototype.info = function () {
    writeLine(this._stdout, arguments, "info");
  };
  Console.prototype.debug = function () {
    writeLine(this._stdout, arguments, "debug");
  };
  Console.prototype.warn = function () {
    writeLine(this._stderr, arguments, "warn");
  };
  Console.prototype.error = function () {
    writeLine(this._stderr, arguments, "error");
  };
  Console.prototype.dir = Console.prototype.log;

  const module = { Console };
  for (const level of ["log", "debug", "info", "warn", "error"]) {
    module[level] = function () {
      const fn = typeof globalConsole[level] === "function" ? globalConsole[level] : globalConsole.log;
      if (typeof fn === "function") {
        return fn.apply(globalConsole, arguments);
      }
    };
  }
  module.dir = module.log;
  module.default = module;
  return module;
})()`
	value, err := runtime.RunProgram(scriptConsoleModuleShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptRequire(runtime *goja.Runtime, collectionPath, sandboxMode string) {
	modules := map[string]goja.Value{}
	moduleCache := map[string]goja.Value{}
	developerMode := NormalizeJSSandboxMode(sandboxMode) == "developer"
	jwtObject := newScriptJWTObject(runtime)
	modules["jsonwebtoken"] = jwtObject
	modules["jwt"] = jwtObject
	lodashObject := newScriptLodashObject(runtime)
	modules["lodash"] = lodashObject
	modules["underscore"] = lodashObject
	installScriptLodashSubpathModules(runtime, modules, lodashObject)
	uuidObject := newScriptUUIDObject(runtime)
	modules["uuid"] = uuidObject
	nanoidObject := newScriptNanoIDObject(runtime)
	modules["nanoid"] = nanoidObject
	pathObject := newScriptPathObject(runtime)
	modules["path"] = pathObject
	modules["node:path"] = pathObject
	if developerMode {
		posixPathObject := pathObject
		win32PathObject := newScriptWin32PathObject(runtime)
		linkScriptPathVariants(posixPathObject, win32PathObject)
		modules["path/posix"] = posixPathObject
		modules["node:path/posix"] = posixPathObject
		modules["path/win32"] = win32PathObject
		modules["node:path/win32"] = win32PathObject
	}
	urlObject := newScriptURLObject(runtime)
	modules["url"] = urlObject
	modules["node:url"] = urlObject
	queryStringObject := newScriptQueryStringObject(runtime)
	modules["querystring"] = queryStringObject
	modules["node:querystring"] = queryStringObject
	osObject := newScriptOSObject(runtime)
	modules["os"] = osObject
	modules["node:os"] = osObject
	eventsObject := newScriptEventsObject(runtime)
	modules["events"] = eventsObject
	modules["node:events"] = eventsObject
	streamObject := newScriptStreamObject(runtime)
	modules["stream"] = streamObject
	modules["node:stream"] = streamObject
	if developerMode {
		streamPromisesObject := newScriptStreamPromisesObject(runtime, streamObject)
		_ = streamObject.ToObject(runtime).Set("promises", streamPromisesObject)
		modules["stream/promises"] = streamPromisesObject
		modules["node:stream/promises"] = streamPromisesObject
	}
	zlibObject := newScriptZlibObject(runtime)
	modules["zlib"] = zlibObject
	modules["node:zlib"] = zlibObject
	atob := runtime.ToValue(func(value string) (string, error) {
		decoded, err := decodeScriptBase64(value)
		if err != nil {
			return "", err
		}
		return scriptBinaryStringFromBytes(decoded), nil
	})
	btoa := runtime.ToValue(func(value string) (string, error) {
		bytes, err := scriptBytesFromBinaryString(value)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(bytes), nil
	})
	modules["atob"] = atob
	modules["btoa"] = btoa
	_ = runtime.Set("jwt", jwtObject)
	_ = runtime.Set("_", lodashObject)
	_ = runtime.Set("atob", atob)
	_ = runtime.Set("btoa", btoa)
	bufferModule := installScriptBuffer(runtime)
	modules["buffer"] = bufferModule
	modules["node:buffer"] = bufferModule
	if developerMode {
		processObject := runtime.Get("process")
		modules["process"] = processObject
		modules["node:process"] = processObject
		timersPromisesObject := newScriptTimersPromisesObject(runtime)
		timersObject := newScriptTimersObject(runtime, timersPromisesObject)
		modules["timers"] = timersObject
		modules["node:timers"] = timersObject
		modules["timers/promises"] = timersPromisesObject
		modules["node:timers/promises"] = timersPromisesObject
		consoleModule := newScriptConsoleModuleObject(runtime)
		modules["console"] = consoleModule
		modules["node:console"] = consoleModule
		assertObject := newScriptAssertObject(runtime)
		modules["assert"] = assertObject
		modules["node:assert"] = assertObject
		assertStrictObject := assertObject.ToObject(runtime).Get("strict")
		modules["assert/strict"] = assertStrictObject
		modules["node:assert/strict"] = assertStrictObject
		fsObject := newScriptFSObject(runtime, collectionPath, sandboxMode)
		modules["fs"] = fsObject
		modules["node:fs"] = fsObject
		fsPromisesObject := fsObject.Get("promises")
		modules["fs/promises"] = fsPromisesObject
		modules["node:fs/promises"] = fsPromisesObject
		dnsObject := newScriptDNSObject(runtime)
		modules["dns"] = dnsObject
		modules["node:dns"] = dnsObject
		dnsPromisesObject := dnsObject.ToObject(runtime).Get("promises")
		modules["dns/promises"] = dnsPromisesObject
		modules["node:dns/promises"] = dnsPromisesObject
		httpObject := newScriptHTTPObject(runtime, eventsObject, false)
		modules["http"] = httpObject
		modules["node:http"] = httpObject
		httpsObject := newScriptHTTPObject(runtime, eventsObject, true)
		modules["https"] = httpsObject
		modules["node:https"] = httpsObject
	}
	utilObject := newScriptUtilObject(runtime)
	modules["util"] = utilObject
	modules["node:util"] = utilObject
	if developerMode {
		utilTypesObject := utilObject.ToObject(runtime).Get("types")
		modules["util/types"] = utilTypesObject
		modules["node:util/types"] = utilTypesObject
	}
	cryptoObject := newScriptCryptoObject(runtime)
	modules["crypto"] = cryptoObject
	modules["node:crypto"] = cryptoObject
	_ = runtime.Set("crypto", cryptoObject)
	cryptoJSObject := newScriptCryptoJSObject(runtime)
	modules["crypto-js"] = cryptoJSObject
	_ = runtime.Set("CryptoJS", cryptoJSObject)
	xmlFormatter := newScriptXMLFormatterObject(runtime)
	modules["xml-formatter"] = xmlFormatter
	cheerioObject := newScriptCheerioObject(runtime)
	modules["cheerio"] = cheerioObject
	xml2jsObject := newScriptXML2JSObject(runtime)
	modules["xml2js"] = xml2jsObject
	yamlObject := newScriptYAMLObject(runtime)
	modules["yaml"] = yamlObject
	momentObject := newScriptMomentObject(runtime)
	modules["moment"] = momentObject
	_ = runtime.Set("moment", momentObject)
	tv4Object := newScriptTV4Object(runtime)
	modules["tv4"] = tv4Object
	_ = runtime.Set("tv4", tv4Object)
	ajvConstructor := installScriptAjv(runtime)
	modules["ajv"] = ajvConstructor
	addFormats := runtime.ToValue(func(goja.Value) goja.Value {
		return goja.Undefined()
	})
	modules["ajv-formats"] = addFormats
	_ = runtime.Set("Ajv", ajvConstructor)
	_ = runtime.Set("addFormats", addFormats)
	nodeFetchObject := newScriptNodeFetchModule(runtime)
	modules["node-fetch"] = nodeFetchObject
	modules["node-fetch/commonjs"] = nodeFetchObject
	modules["axios"] = installScriptAxios(runtime)
	chaiObject := runtime.NewObject()
	_ = chaiObject.Set("expect", runtime.Get("expect"))
	_ = chaiObject.Set("assert", runtime.Get("assert"))
	modules["chai"] = chaiObject
	loadingModules := map[string]*goja.Object{}
	var loadLocalModule func(parentDir, name string) (goja.Value, error)
	var loadNodeModule func(parentDir, name string) (goja.Value, error)
	loadLocalModule = func(parentDir, name string) (goja.Value, error) {
		modulePath, err := resolveScriptLocalModule(collectionPath, parentDir, name, sandboxMode)
		if err != nil {
			return nil, err
		}
		if cached, ok := moduleCache[modulePath]; ok {
			return cached, nil
		}
		if strings.EqualFold(filepath.Ext(modulePath), ".json") {
			content, err := os.ReadFile(modulePath)
			if err != nil {
				return nil, err
			}
			var data interface{}
			if err := json.Unmarshal(content, &data); err != nil {
				return nil, err
			}
			value := runtime.ToValue(data)
			moduleCache[modulePath] = value
			return value, nil
		}
		if loading, ok := loadingModules[modulePath]; ok {
			return loading.Get("exports"), nil
		}
		content, err := os.ReadFile(modulePath)
		if err != nil {
			return nil, err
		}
		moduleObject := runtime.NewObject()
		exportsObject := runtime.NewObject()
		_ = moduleObject.Set("exports", exportsObject)
		loadingModules[modulePath] = moduleObject
		moduleDir := filepath.Dir(modulePath)
		localRequire := func(requiredName string) goja.Value {
			if module, ok := modules[requiredName]; ok {
				return module
			}
			if developerMode {
				if module, err := loadNodeModule(moduleDir, requiredName); err == nil {
					return module
				}
			}
			if !scriptModuleIsLocalPath(requiredName) {
				panic(runtime.NewGoError(fmt.Errorf("Cannot find module %q", requiredName)))
			}
			module, err := loadLocalModule(moduleDir, requiredName)
			if err != nil {
				panic(runtime.NewGoError(fmt.Errorf("Cannot find module %q", requiredName)))
			}
			return module
		}
		wrapped, err := runtime.RunString("(function(require, module, exports, __filename, __dirname) {\n" + string(content) + "\n})")
		if err != nil {
			delete(loadingModules, modulePath)
			return nil, err
		}
		fn, ok := goja.AssertFunction(wrapped)
		if !ok {
			delete(loadingModules, modulePath)
			return nil, errors.New("local module wrapper is not callable")
		}
		if _, err := fn(goja.Undefined(), runtime.ToValue(localRequire), moduleObject, exportsObject, runtime.ToValue(modulePath), runtime.ToValue(moduleDir)); err != nil {
			delete(loadingModules, modulePath)
			return nil, err
		}
		exports := moduleObject.Get("exports")
		moduleCache[modulePath] = exports
		delete(loadingModules, modulePath)
		return exports, nil
	}
	loadNodeModule = func(parentDir, name string) (goja.Value, error) {
		if !developerMode {
			return nil, fmt.Errorf("Cannot find module %q", name)
		}
		moduleName, subpath, ok := scriptNodeModuleParts(name)
		if !ok {
			return nil, fmt.Errorf("Cannot find module %q", name)
		}
		for _, modulesDir := range scriptNodeModuleSearchDirs(collectionPath, parentDir) {
			candidate := filepath.Join(modulesDir, filepath.FromSlash(moduleName))
			if subpath != "" {
				candidate = filepath.Join(candidate, filepath.FromSlash(subpath))
			}
			modulePath, err := resolveScriptLocalModule(collectionPath, "", candidate, sandboxMode)
			if err == nil {
				return loadLocalModule(filepath.Dir(modulePath), modulePath)
			}
		}
		return nil, fmt.Errorf("Cannot find module %q", name)
	}
	_ = runtime.Set("require", func(name string) goja.Value {
		if module, ok := modules[name]; ok {
			return module
		}
		if scriptModuleIsLocalPath(name) {
			module, err := loadLocalModule(collectionPath, name)
			if err == nil {
				return module
			}
		} else if developerMode {
			module, err := loadNodeModule(collectionPath, name)
			if err == nil {
				return module
			}
		}
		panic(runtime.NewGoError(fmt.Errorf("Cannot find module %q", name)))
	})
}

func scriptNodeModuleParts(name string) (string, string, bool) {
	normalized := strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if normalized == "" || strings.HasPrefix(normalized, ".") || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "\x00") {
		return "", "", false
	}
	parts := strings.Split(normalized, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 2 || parts[0] == "@" {
			return "", "", false
		}
		moduleName := parts[0] + "/" + parts[1]
		return moduleName, strings.Join(parts[2:], "/"), true
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func scriptNodeModuleSearchDirs(collectionPath, parentDir string) []string {
	root := filepath.Clean(collectionPath)
	if root == "." || strings.TrimSpace(root) == "" {
		return nil
	}
	start := parentDir
	if strings.TrimSpace(start) == "" {
		start = root
	}
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	start = filepath.Clean(start)
	root = filepath.Clean(root)
	searchDirs := []string{}
	seen := map[string]bool{}
	for current := start; ; current = filepath.Dir(current) {
		if scriptPathWithinRoot(root, current) {
			modulesDir := filepath.Join(current, "node_modules")
			if !seen[modulesDir] {
				searchDirs = append(searchDirs, modulesDir)
				seen[modulesDir] = true
			}
		}
		if current == root || current == filepath.Dir(current) {
			break
		}
	}
	rootModules := filepath.Join(root, "node_modules")
	if !seen[rootModules] {
		searchDirs = append(searchDirs, rootModules)
	}
	return searchDirs
}

func resolveScriptLocalModule(collectionPath, parentDir, name, sandboxMode string) (string, error) {
	root := filepath.Clean(collectionPath)
	if root == "." || strings.TrimSpace(root) == "" {
		return "", errors.New("collection path is not available")
	}
	base := parentDir
	if strings.TrimSpace(base) == "" {
		base = root
	}
	var candidate string
	if filepath.IsAbs(name) {
		candidate = filepath.Clean(name)
	} else {
		candidate = filepath.Clean(filepath.Join(base, name))
	}
	modulePath, err := resolveScriptLocalModuleFile(root, candidate, sandboxMode)
	if err != nil {
		return "", err
	}
	if !scriptPathWithinRoot(root, modulePath) {
		return "", errors.New("local module path escapes collection")
	}
	return modulePath, nil
}

func resolveScriptLocalModuleFile(root, candidate, sandboxMode string) (string, error) {
	developerMode := NormalizeJSSandboxMode(sandboxMode) == "developer"
	options := []string{candidate}
	if filepath.Ext(candidate) == "" {
		options = append(options, candidate+".js", candidate+".cjs")
		if developerMode {
			options = append(options, candidate+".json")
		}
	}
	for _, option := range options {
		info, err := os.Stat(option)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if developerMode {
				if mainPath, ok := scriptPackageMainPath(option); ok {
					if resolvedMain, err := resolveScriptLocalModuleFile(root, mainPath, sandboxMode); err == nil {
						return resolvedMain, nil
					}
				}
			}
			indexCandidates := []string{filepath.Join(option, "index.js"), filepath.Join(option, "index.cjs")}
			if developerMode {
				indexCandidates = append(indexCandidates, filepath.Join(option, "index.json"))
			}
			for _, indexPath := range indexCandidates {
				if indexInfo, err := os.Stat(indexPath); err == nil && !indexInfo.IsDir() {
					return filepath.Clean(indexPath), nil
				}
			}
			continue
		}
		return filepath.Clean(option), nil
	}
	return "", errors.New("local module not found")
}

func newScriptLodashObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const objectToString = Object.prototype.toString;

  function isObject(value) {
    return value !== null && (typeof value === "object" || typeof value === "function");
  }

  function isPlainObject(value) {
    if (objectToString.call(value) !== "[object Object]") return false;
    const proto = Object.getPrototypeOf(value);
    return proto === null || proto === Object.prototype;
  }

  function parsePath(path) {
    if (Array.isArray(path)) return path.map(String);
    const text = String(path || "");
    const parts = [];
    text.replace(/[^.[\]]+|\[(?:(-?\d+(?:\.\d+)?)|(["'])((?:(?!\2)[^\\]|\\.)*?)\2)\]/g, function (match, number, quote, quoted) {
      if (quote) {
        parts.push(quoted.replace(/\\(\\)?/g, "$1"));
      } else {
        parts.push(number !== undefined ? number : match);
      }
    });
    return parts;
  }

  function get(object, path, defaultValue) {
    const parts = parsePath(path);
    let current = object;
    for (const part of parts) {
      if (current == null) return defaultValue;
      current = current[part];
    }
    return current === undefined ? defaultValue : current;
  }

  function has(object, path) {
    const parts = parsePath(path);
    let current = object;
    for (const part of parts) {
      if (current == null || !Object.prototype.hasOwnProperty.call(Object(current), part)) return false;
      current = current[part];
    }
    return true;
  }

  function set(object, path, value) {
    const parts = parsePath(path);
    if (!parts.length) return object;
    let current = object;
    for (let index = 0; index < parts.length - 1; index++) {
      const part = parts[index];
      if (!isObject(current[part])) {
        current[part] = /^\d+$/.test(parts[index + 1]) ? [] : {};
      }
      current = current[part];
    }
    current[parts[parts.length - 1]] = value;
    return object;
  }

  function unset(object, path) {
    const parts = parsePath(path);
    if (!parts.length) return true;
    let current = object;
    for (let index = 0; index < parts.length - 1; index++) {
      current = current == null ? undefined : current[parts[index]];
      if (current == null) return true;
    }
    return delete current[parts[parts.length - 1]];
  }

  function cloneDeep(value, seen) {
    if (!isObject(value)) return value;
    seen = seen || new Map();
    if (seen.has(value)) return seen.get(value);
    if (value instanceof Date) return new Date(value.getTime());
    if (value instanceof RegExp) return new RegExp(value.source, value.flags);
    if (typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value)) return new value.constructor(value);
    if (typeof ArrayBuffer !== "undefined" && value instanceof ArrayBuffer) return value.slice(0);
    const out = Array.isArray(value) ? [] : {};
    seen.set(value, out);
    for (const key of Reflect.ownKeys(value)) {
      out[key] = cloneDeep(value[key], seen);
    }
    return out;
  }

  function isEqual(a, b, seen) {
    if (Object.is(a, b)) return true;
    if (!isObject(a) || !isObject(b)) return false;
    seen = seen || new Map();
    const cached = seen.get(a);
    if (cached && cached === b) return true;
    seen.set(a, b);
    if (objectToString.call(a) !== objectToString.call(b)) return false;
    if (a instanceof Date) return a.getTime() === b.getTime();
    if (a instanceof RegExp) return String(a) === String(b);
    if (typeof ArrayBuffer !== "undefined" && (ArrayBuffer.isView(a) || ArrayBuffer.isView(b))) {
      if (!ArrayBuffer.isView(a) || !ArrayBuffer.isView(b) || a.length !== b.length) return false;
      for (let index = 0; index < a.length; index++) if (a[index] !== b[index]) return false;
      return true;
    }
    const aKeys = Reflect.ownKeys(a);
    const bKeys = Reflect.ownKeys(b);
    if (aKeys.length !== bKeys.length) return false;
    for (const key of aKeys) {
      if (!Object.prototype.hasOwnProperty.call(b, key) || !isEqual(a[key], b[key], seen)) return false;
    }
    return true;
  }

  function toArray(collection) {
    if (collection == null) return [];
    if (Array.isArray(collection)) return collection.slice();
    if (typeof collection === "string") return collection.split("");
    if (typeof collection[Symbol.iterator] === "function") return Array.from(collection);
    return Object.keys(collection).map((key) => collection[key]);
  }

  function iterate(collection, iteratee) {
    if (collection == null) return [];
    if (Array.isArray(collection) || typeof collection === "string") {
      return Array.prototype.map.call(collection, function (value, index) { return iteratee(value, index, collection); });
    }
    return Object.keys(collection).map(function (key) { return iteratee(collection[key], key, collection); });
  }

  function property(path) {
    return function (value) { return get(value, path); };
  }

  function matches(source) {
    return function (value) {
      for (const key of Object.keys(source || {})) {
        if (!isEqual(value == null ? undefined : value[key], source[key])) return false;
      }
      return true;
    };
  }

  function normalizeIteratee(iteratee) {
    if (typeof iteratee === "function") return iteratee;
    if (Array.isArray(iteratee)) return function (value) { return isEqual(get(value, iteratee[0]), iteratee[1]); };
    if (typeof iteratee === "string") return property(iteratee);
    if (iteratee && typeof iteratee === "object") return matches(iteratee);
    return function (value) { return value; };
  }

  function map(collection, iteratee) {
    return iterate(collection, normalizeIteratee(iteratee));
  }

  function forEach(collection, iteratee) {
    iterate(collection, normalizeIteratee(iteratee));
    return collection;
  }

  function filter(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    return iterate(collection, function (value, key, source) {
      return fn(value, key, source) ? value : undefined;
    }).filter(function (value) { return value !== undefined; });
  }

  function find(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    const values = toArray(collection);
    for (let index = 0; index < values.length; index++) {
      if (fn(values[index], index, collection)) return values[index];
    }
    return undefined;
  }

  function reduce(collection, iteratee, accumulator) {
    const values = toArray(collection);
    let index = 0;
    if (arguments.length < 3) {
      accumulator = values[0];
      index = 1;
    }
    for (; index < values.length; index++) {
      accumulator = iteratee(accumulator, values[index], index, collection);
    }
    return accumulator;
  }

  function includes(collection, value) {
    if (typeof collection === "string") return collection.indexOf(String(value)) !== -1;
    return toArray(collection).some(function (item) { return isEqual(item, value); });
  }

  function keys(value) {
    return value == null ? [] : Object.keys(Object(value));
  }

  function values(value) {
    return keys(value).map(function (key) { return value[key]; });
  }

  function pick(object) {
    const out = {};
    const paths = Array.prototype.slice.call(arguments, 1).flat();
    for (const path of paths) {
      if (has(object, path)) set(out, path, get(object, path));
    }
    return out;
  }

  function omit(object) {
    const out = cloneDeep(object || {});
    const paths = Array.prototype.slice.call(arguments, 1).flat();
    for (const path of paths) unset(out, path);
    return out;
  }

  function merge(target) {
    target = target || {};
    for (let sourceIndex = 1; sourceIndex < arguments.length; sourceIndex++) {
      const source = arguments[sourceIndex];
      if (!isObject(source)) continue;
      for (const key of Object.keys(source)) {
        if (isPlainObject(source[key]) && isPlainObject(target[key])) {
          merge(target[key], source[key]);
        } else if (Array.isArray(source[key])) {
          target[key] = source[key].slice();
        } else {
          target[key] = source[key];
        }
      }
    }
    return target;
  }

  function groupBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      const key = String(fn(value, index, collection));
      (out[key] || (out[key] = [])).push(value);
      return out;
    }, {});
  }

  function keyBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      out[String(fn(value, index, collection))] = value;
      return out;
    }, {});
  }

  function sortBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).map(function (value, index) {
      return { value, index, criteria: fn(value, index, collection) };
    }).sort(function (left, right) {
      if (left.criteria < right.criteria) return -1;
      if (left.criteria > right.criteria) return 1;
      return left.index - right.index;
    }).map(function (entry) { return entry.value; });
  }

  function uniq(collection) {
    const out = [];
    for (const value of toArray(collection)) {
      if (!out.some(function (existing) { return isEqual(existing, value); })) out.push(value);
    }
    return out;
  }

  function flatten(collection) {
    return toArray(collection).reduce(function (out, value) {
      return out.concat(Array.isArray(value) ? value : [value]);
    }, []);
  }

  function flattenDeep(collection) {
    return toArray(collection).reduce(function (out, value) {
      return out.concat(Array.isArray(value) ? flattenDeep(value) : [value]);
    }, []);
  }

  function chunk(collection, size) {
    const values = toArray(collection);
    const width = Math.max(1, Number(size) || 1);
    const out = [];
    for (let index = 0; index < values.length; index += width) {
      out.push(values.slice(index, index + width));
    }
    return out;
  }

  function compact(collection) {
    return toArray(collection).filter(Boolean);
  }

  function isEmpty(value) {
    if (value == null) return true;
    if (Array.isArray(value) || typeof value === "string") return value.length === 0;
    if (value instanceof Map || value instanceof Set) return value.size === 0;
    return Object.keys(Object(value)).length === 0;
  }

  function Chain(value) {
    this.__value = value;
  }
  Chain.prototype.value = function () { return this.__value; };
  Chain.prototype.map = function (iteratee) { this.__value = map(this.__value, iteratee); return this; };
  Chain.prototype.filter = function (predicate) { this.__value = filter(this.__value, predicate); return this; };
  Chain.prototype.forEach = function (iteratee) { forEach(this.__value, iteratee); return this; };
  Chain.prototype.reduce = function (iteratee, accumulator) { this.__value = reduce(this.__value, iteratee, accumulator); return this; };
  Chain.prototype.sortBy = function (iteratee) { this.__value = sortBy(this.__value, iteratee); return this; };
  Chain.prototype.uniq = function () { this.__value = uniq(this.__value); return this; };
  Chain.prototype.flatten = function () { this.__value = flatten(this.__value); return this; };
  Chain.prototype.flattenDeep = function () { this.__value = flattenDeep(this.__value); return this; };
  Chain.prototype.compact = function () { this.__value = compact(this.__value); return this; };
  Chain.prototype.get = function (path, defaultValue) { this.__value = get(this.__value, path, defaultValue); return this; };

  function chain(value) {
    return new Chain(value);
  }

  function lodash(value) {
    return chain(value);
  }

  Object.assign(lodash, {
    VERSION: "4.17.21-liteapi",
    chain,
    get,
    set,
    has,
    unset,
    property,
    cloneDeep,
    isEqual,
    isObject,
    isPlainObject,
    isArray: Array.isArray,
    isString: function (value) { return typeof value === "string" || value instanceof String; },
    isNumber: function (value) { return typeof value === "number" || value instanceof Number; },
    isBoolean: function (value) { return value === true || value === false || value instanceof Boolean; },
    isFunction: function (value) { return typeof value === "function"; },
    isNil: function (value) { return value == null; },
    isNull: function (value) { return value === null; },
    isUndefined: function (value) { return value === undefined; },
    isEmpty,
    toArray,
    keys,
    values,
    map,
    each: forEach,
    forEach,
    filter,
    find,
    reduce,
    includes,
    pick,
    omit,
    merge,
    assign: Object.assign,
    groupBy,
    keyBy,
    sortBy,
    uniq,
    flatten,
    flattenDeep,
    chunk,
    compact
  });
  lodash.default = lodash;
  return lodash;
})()`
	value, err := runtime.RunProgram(scriptLodashShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptLodashSubpathModules(runtime *goja.Runtime, modules map[string]goja.Value, lodashObject goja.Value) {
	lodash := lodashObject.ToObject(runtime)
	aliases := map[string]string{
		"chain":         "chain",
		"get":           "get",
		"set":           "set",
		"has":           "has",
		"unset":         "unset",
		"property":      "property",
		"cloneDeep":     "cloneDeep",
		"isEqual":       "isEqual",
		"isObject":      "isObject",
		"isPlainObject": "isPlainObject",
		"isArray":       "isArray",
		"isString":      "isString",
		"isNumber":      "isNumber",
		"isBoolean":     "isBoolean",
		"isFunction":    "isFunction",
		"isNil":         "isNil",
		"isNull":        "isNull",
		"isUndefined":   "isUndefined",
		"isEmpty":       "isEmpty",
		"toArray":       "toArray",
		"keys":          "keys",
		"values":        "values",
		"map":           "map",
		"each":          "each",
		"forEach":       "forEach",
		"filter":        "filter",
		"find":          "find",
		"reduce":        "reduce",
		"includes":      "includes",
		"pick":          "pick",
		"omit":          "omit",
		"merge":         "merge",
		"assign":        "assign",
		"groupBy":       "groupBy",
		"keyBy":         "keyBy",
		"sortBy":        "sortBy",
		"uniq":          "uniq",
		"flatten":       "flatten",
		"flattenDeep":   "flattenDeep",
		"chunk":         "chunk",
		"compact":       "compact",
	}
	for moduleName, propertyName := range aliases {
		value := lodash.Get(propertyName)
		if goja.IsUndefined(value) {
			continue
		}
		modules["lodash/"+moduleName] = value
		modules["lodash/"+moduleName+".js"] = value
	}
}

package scripting

// The sandbox node:fs shim and its path sandbox.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func newScriptFSObject(runtime *goja.Runtime, collectionPath, sandboxMode string) *goja.Object {
	fsObject := runtime.NewObject()
	promisesObject := runtime.NewObject()
	developerMode := NormalizeJSSandboxMode(sandboxMode) == "developer"
	readFile := func(call goja.FunctionCall) goja.Value {
		data, err := readScriptFSFile(collectionPath, call.Argument(0), sandboxMode)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptFSFileValue(runtime, data, scriptFSEncoding(runtime, call.Argument(1)))
	}
	_ = fsObject.Set("readFileSync", readFile)
	_ = fsObject.Set("existsSync", func(call goja.FunctionCall) goja.Value {
		path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
		if err != nil {
			return runtime.ToValue(false)
		}
		_, err = os.Stat(path)
		return runtime.ToValue(err == nil)
	})
	_ = fsObject.Set("statSync", func(call goja.FunctionCall) goja.Value {
		path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		info, err := os.Stat(path)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptFSStatValue(runtime, info)
	})
	_ = fsObject.Set("readdirSync", func(call goja.FunctionCall) goja.Value {
		path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return runtime.ToValue(names)
	})
	_ = promisesObject.Set("readFile", func(call goja.FunctionCall) goja.Value {
		data, err := readScriptFSFile(collectionPath, call.Argument(0), sandboxMode)
		if err != nil {
			return scriptRejectedPromise(runtime, err)
		}
		return scriptResolvedPromise(runtime, scriptFSFileValue(runtime, data, scriptFSEncoding(runtime, call.Argument(1))))
	})
	if developerMode {
		writeFileAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				return nil, err
			}
			data, err := scriptFSWriteBytes(runtime, call.Argument(1), call.Argument(2))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, data, 0o666); err != nil {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		mkdirAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				return nil, err
			}
			if scriptFSOptionBool(runtime, call.Argument(1), "recursive") {
				if err := os.MkdirAll(path, 0o777); err != nil {
					return nil, err
				}
				return goja.Undefined(), nil
			}
			if err := os.Mkdir(path, 0o777); err != nil {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		removeAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				if scriptFSOptionBool(runtime, call.Argument(1), "force") {
					return goja.Undefined(), nil
				}
				return nil, err
			}
			recursive := scriptFSOptionBool(runtime, call.Argument(1), "recursive")
			force := scriptFSOptionBool(runtime, call.Argument(1), "force")
			if !recursive {
				err = os.Remove(path)
			} else if !force {
				if _, statErr := os.Stat(path); statErr != nil {
					err = statErr
				} else {
					err = os.RemoveAll(path)
				}
			} else {
				err = os.RemoveAll(path)
			}
			ignorableMissing := force && os.IsNotExist(err)
			if err != nil && !ignorableMissing {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		unlinkAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				return nil, err
			}
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		syncAction := func(action func(goja.FunctionCall) (goja.Value, error)) func(goja.FunctionCall) goja.Value {
			return func(call goja.FunctionCall) goja.Value {
				value, err := action(call)
				if err != nil {
					panic(runtime.NewGoError(err))
				}
				return value
			}
		}
		_ = fsObject.Set("writeFileSync", syncAction(writeFileAction))
		_ = fsObject.Set("mkdirSync", syncAction(mkdirAction))
		_ = fsObject.Set("rmSync", syncAction(removeAction))
		_ = fsObject.Set("unlinkSync", syncAction(unlinkAction))
		_ = promisesObject.Set("writeFile", func(call goja.FunctionCall) goja.Value {
			if _, err := writeFileAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
		_ = promisesObject.Set("mkdir", func(call goja.FunctionCall) goja.Value {
			if _, err := mkdirAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
		_ = promisesObject.Set("rm", func(call goja.FunctionCall) goja.Value {
			if _, err := removeAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
		_ = promisesObject.Set("unlink", func(call goja.FunctionCall) goja.Value {
			if _, err := unlinkAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
	}
	_ = fsObject.Set("promises", promisesObject)
	return fsObject
}

func readScriptFSFile(collectionPath string, value goja.Value, sandboxMode string) ([]byte, error) {
	path, err := resolveScriptFSPath(collectionPath, value.String(), sandboxMode)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	return os.ReadFile(path)
}

func scriptFSWriteBytes(runtime *goja.Runtime, value, options goja.Value) ([]byte, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, errors.New("fs data must be a string, Buffer, ArrayBuffer, typed array, or byte array")
	}
	encoding := scriptFSEncoding(runtime, options)
	switch typed := value.Export().(type) {
	case string:
		return scriptFSBytesFromString(typed, encoding)
	case []byte:
		return append([]byte(nil), typed...), nil
	case []interface{}:
		return scriptFSBytesFromInterfaceSlice(typed), nil
	case []int:
		bytes := make([]byte, len(typed))
		for index, item := range typed {
			bytes[index] = byte(item)
		}
		return bytes, nil
	}
	object := value.ToObject(runtime)
	lengthValue := object.Get("length")
	if lengthValue != nil && !goja.IsUndefined(lengthValue) && !goja.IsNull(lengthValue) {
		return scriptFSBytesFromIndexedObject(object, lengthValue.ToInteger())
	}
	byteLengthValue := object.Get("byteLength")
	if byteLengthValue != nil && !goja.IsUndefined(byteLengthValue) && !goja.IsNull(byteLengthValue) {
		// A DataView has byteLength but no length, and new Uint8Array(dataView)
		// treats it as an array-like with no length at all — producing an empty
		// view whose bytes then read back as zero. Going through its buffer
		// (honouring byteOffset, since a view need not start at zero) is what
		// makes fs.writeFile(dataView) write the data rather than a file of the
		// right size full of nulls.
		view, err := scriptFSUint8ArrayOver(runtime, value, object)
		if err != nil {
			return nil, err
		}
		return scriptFSBytesFromIndexedObject(view.ToObject(runtime), byteLengthValue.ToInteger())
	}
	return nil, errors.New("fs data must be a string, Buffer, ArrayBuffer, typed array, or byte array")
}

func scriptFSBytesFromString(value, encoding string) ([]byte, error) {
	switch normalizeScriptFSEncoding(encoding) {
	case "", "utf8", "utf":
		return []byte(value), nil
	case "base64", "base64url":
		return decodeScriptBase64(value)
	case "hex":
		return hex.DecodeString(strings.TrimSpace(value))
	case "latin1", "binary", "ascii":
		return scriptBytesFromBinaryString(value)
	default:
		return nil, fmt.Errorf("unsupported fs encoding: %s", encoding)
	}
}

func scriptFSBytesFromInterfaceSlice(values []interface{}) []byte {
	bytes := make([]byte, len(values))
	for index, item := range values {
		switch typed := item.(type) {
		case int:
			bytes[index] = byte(typed)
		case int64:
			bytes[index] = byte(typed)
		case float64:
			bytes[index] = byte(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				bytes[index] = byte(parsed)
			}
		default:
			bytes[index] = byte(0)
		}
	}
	return bytes
}

func scriptFSUint8ArrayOver(runtime *goja.Runtime, value goja.Value, object *goja.Object) (*goja.Object, error) {
	uint8Array := runtime.Get("Uint8Array")
	if buffer := object.Get("buffer"); buffer != nil && !goja.IsUndefined(buffer) && !goja.IsNull(buffer) {
		offset := int64(0)
		if byteOffset := object.Get("byteOffset"); byteOffset != nil && !goja.IsUndefined(byteOffset) && !goja.IsNull(byteOffset) {
			offset = byteOffset.ToInteger()
		}
		byteLength := object.Get("byteLength").ToInteger()
		return runtime.New(uint8Array, buffer, runtime.ToValue(offset), runtime.ToValue(byteLength))
	}
	return runtime.New(uint8Array, value)
}

func scriptFSBytesFromIndexedObject(object *goja.Object, length int64) ([]byte, error) {
	if length < 0 {
		return nil, errors.New("fs data length must be non-negative")
	}
	if length > 64*1024*1024 {
		return nil, errors.New("fs data is too large")
	}
	bytes := make([]byte, int(length))
	for index := int64(0); index < length; index++ {
		value := object.Get(strconv.FormatInt(index, 10))
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		bytes[index] = byte(value.ToInteger())
	}
	return bytes, nil
}

func scriptFSOptionBool(runtime *goja.Runtime, value goja.Value, name string) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	if exported, ok := value.Export().(bool); ok {
		return exported
	}
	option := value.ToObject(runtime).Get(name)
	return option != nil && !goja.IsUndefined(option) && !goja.IsNull(option) && option.ToBoolean()
}

func scriptFSEncoding(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if encoding, ok := value.Export().(string); ok {
		return normalizeScriptFSEncoding(encoding)
	}
	object := value.ToObject(runtime)
	encoding := object.Get("encoding")
	if encoding == nil || goja.IsUndefined(encoding) || goja.IsNull(encoding) {
		return ""
	}
	return normalizeScriptFSEncoding(encoding.String())
}

func normalizeScriptFSEncoding(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), "_", ""))
}

func scriptFSFileValue(runtime *goja.Runtime, data []byte, encoding string) goja.Value {
	switch encoding {
	case "":
		return scriptBufferValue(runtime, data)
	case "utf8", "utf":
		return runtime.ToValue(string(data))
	case "base64":
		return runtime.ToValue(base64.StdEncoding.EncodeToString(data))
	case "hex":
		return runtime.ToValue(hex.EncodeToString(data))
	case "latin1", "binary", "ascii":
		return runtime.ToValue(scriptBinaryStringFromBytes(data))
	default:
		panic(runtime.NewTypeError("unsupported fs encoding: " + encoding))
	}
}

func scriptFSStatValue(runtime *goja.Runtime, info os.FileInfo) goja.Value {
	object := runtime.NewObject()
	_ = object.Set("size", info.Size())
	_ = object.Set("mtimeMs", float64(info.ModTime().UnixNano())/float64(time.Millisecond))
	_ = object.Set("isFile", func() bool { return !info.IsDir() })
	_ = object.Set("isDirectory", func() bool { return info.IsDir() })
	return object
}

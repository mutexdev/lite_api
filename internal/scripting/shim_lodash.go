package scripting

// The lodash shim and its subpath modules.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"github.com/dop251/goja"
)

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

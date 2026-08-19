package scripting

// Smaller sandbox shims: buffer, assert, util, axios and querystring.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"strings"

	"github.com/dop251/goja"
)

func installScriptBuffer(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const marker = "__liteApiBuffer";
  function normalizeEncoding(encoding) {
    return String(encoding || "utf8").toLowerCase().replace(/[-_]/g, "");
  }
  function utf8Bytes(value) {
    const encoded = encodeURIComponent(String(value));
    const out = [];
    for (let i = 0; i < encoded.length; i++) {
      const ch = encoded[i];
      if (ch === "%") {
        out.push(parseInt(encoded.slice(i + 1, i + 3), 16));
        i += 2;
      } else {
        out.push(ch.charCodeAt(0));
      }
    }
    return out;
  }
  function utf8String(bytes) {
    let encoded = "";
    for (let i = 0; i < bytes.length; i++) {
      const value = bytes[i];
      if (value < 0x80) {
        encoded += String.fromCharCode(value);
      } else {
        encoded += "%" + value.toString(16).padStart(2, "0").toUpperCase();
      }
    }
    try {
      return decodeURIComponent(encoded);
    } catch (_) {
      let fallback = "";
      for (let i = 0; i < bytes.length; i++) {
        fallback += String.fromCharCode(bytes[i]);
      }
      return fallback;
    }
  }
  function binaryString(bytes) {
    let out = "";
    for (let i = 0; i < bytes.length; i++) {
      out += String.fromCharCode(bytes[i]);
    }
    return out;
  }
  function bytesFromString(value, encoding) {
    const enc = normalizeEncoding(encoding);
    const text = String(value);
    if (enc === "hex") {
      const out = [];
      for (let i = 0; i + 1 < text.length; i += 2) {
        const parsed = parseInt(text.slice(i, i + 2), 16);
        if (Number.isNaN(parsed)) {
          break;
        }
        out.push(parsed);
      }
      return out;
    }
    if (enc === "base64" || enc === "base64url") {
      const binary = atob(text.replace(/-/g, "+").replace(/_/g, "/"));
      const out = [];
      for (let i = 0; i < binary.length; i++) {
        out.push(binary.charCodeAt(i) & 255);
      }
      return out;
    }
    if (enc === "latin1" || enc === "binary" || enc === "ascii") {
      const out = [];
      for (let i = 0; i < text.length; i++) {
        out.push(text.charCodeAt(i) & 255);
      }
      return out;
    }
    return utf8Bytes(text);
  }
  function bytesFromValue(value, encoding) {
    if (Buffer.isBuffer(value)) {
      return Array.from(value);
    }
    if (typeof value === "string") {
      return bytesFromString(value, encoding);
    }
    if (value instanceof ArrayBuffer) {
      return Array.from(new Uint8Array(value));
    }
    if (ArrayBuffer.isView(value)) {
      return Array.from(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
    }
    if (Array.isArray(value)) {
      return value.map((item) => Number(item) & 255);
    }
    throw new TypeError("Buffer.from input must be a string, array, ArrayBuffer, or typed array");
  }
  class Buffer extends Uint8Array {
    constructor(value) {
      super(value);
      Object.defineProperty(this, marker, { value: true, enumerable: false });
    }
    static from(value, encoding) {
      return new Buffer(bytesFromValue(value, encoding));
    }
    static alloc(size, fill, encoding) {
      const length = Number(size);
      if (!Number.isFinite(length) || length < 0) {
        throw new RangeError("Buffer size must be a non-negative number");
      }
      const buffer = new Buffer(Math.trunc(length));
      if (fill !== undefined) {
        if (typeof fill === "number") {
          buffer.fill(fill & 255);
        } else {
          const pattern = bytesFromValue(fill, encoding);
          if (pattern.length > 0) {
            for (let i = 0; i < buffer.length; i++) {
              buffer[i] = pattern[i % pattern.length];
            }
          }
        }
      }
      return buffer;
    }
    static concat(list, totalLength) {
      if (!Array.isArray(list)) {
        throw new TypeError("Buffer.concat list must be an array");
      }
      const chunks = list.map((item) => Buffer.isBuffer(item) ? item : Buffer.from(item));
      const length = totalLength === undefined ? chunks.reduce((sum, item) => sum + item.length, 0) : Math.trunc(Number(totalLength));
      const out = Buffer.alloc(Math.max(0, length));
      let offset = 0;
      for (const chunk of chunks) {
        const remaining = out.length - offset;
        if (remaining <= 0) {
          break;
        }
        out.set(chunk.subarray(0, remaining), offset);
        offset += Math.min(chunk.length, remaining);
      }
      return out;
    }
    static isBuffer(value) {
      return !!(value && value[marker] === true);
    }
    static byteLength(value, encoding) {
      return bytesFromValue(value, encoding).length;
    }
    toString(encoding, start, end) {
      const enc = normalizeEncoding(encoding);
      const begin = start === undefined ? 0 : Math.max(0, Math.trunc(Number(start)));
      const finish = end === undefined ? this.length : Math.min(this.length, Math.max(begin, Math.trunc(Number(end))));
      const bytes = Array.from(Uint8Array.prototype.subarray.call(this, begin, finish));
      if (enc === "hex") {
        return bytes.map((value) => value.toString(16).padStart(2, "0")).join("");
      }
      if (enc === "base64") {
        return btoa(binaryString(bytes));
      }
      if (enc === "latin1" || enc === "binary" || enc === "ascii") {
        return binaryString(bytes);
      }
      return utf8String(bytes);
    }
    subarray(start, end) {
      const length = this.length;
      let begin = start === undefined ? 0 : Math.trunc(Number(start));
      let finish = end === undefined ? length : Math.trunc(Number(end));
      if (begin < 0) {
        begin = Math.max(length + begin, 0);
      } else {
        begin = Math.min(begin, length);
      }
      if (finish < 0) {
        finish = Math.max(length + finish, 0);
      } else {
        finish = Math.min(finish, length);
      }
      if (finish < begin) {
        finish = begin;
      }
      const out = [];
      for (let i = begin; i < finish; i++) {
        out.push(this[i]);
      }
      return new Buffer(out);
    }
    slice(start, end) {
      return this.subarray(start, end);
    }
    toJSON() {
      return { type: "Buffer", data: Array.from(this) };
    }
  }
  globalThis.Buffer = Buffer;
  return { Buffer };
})()`
	value, err := runtime.RunProgram(scriptBufferShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptAssertObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function inspect(value) {
    if (typeof value === "string") return JSON.stringify(value);
    if (typeof value === "function") return "[Function" + (value.name ? ": " + value.name : "") + "]";
    if (value instanceof Error) return value.name + ": " + value.message;
    try {
      return JSON.stringify(value);
    } catch (_) {
      return String(value);
    }
  }

  function sorted(value, seen) {
    if (value === null || typeof value !== "object") return value;
    seen = seen || [];
    if (seen.indexOf(value) !== -1) return "[Circular]";
    if (typeof Buffer !== "undefined" && Buffer.isBuffer(value)) return { type: "Buffer", data: Array.from(value) };
    if (typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value)) return { type: value.constructor && value.constructor.name || "TypedArray", data: Array.from(value) };
    if (Array.isArray(value)) return value.map((item) => sorted(item, seen.concat([value])));
    if (value instanceof Date) return { type: "Date", value: value.toISOString() };
    if (value instanceof RegExp) return { type: "RegExp", value: String(value) };
    const out = {};
    for (const key of Object.keys(value).sort()) {
      out[key] = sorted(value[key], seen.concat([value]));
    }
    return out;
  }

  function deepEqual(actual, expected) {
    if (Object.is(actual, expected)) return true;
    return JSON.stringify(sorted(actual)) === JSON.stringify(sorted(expected));
  }

  class AssertionError extends Error {
    constructor(options) {
      options = options || {};
      const actual = options.actual;
      const expected = options.expected;
      const operator = options.operator || "fail";
      const message = options.message === undefined || options.message === null
        ? "Expected values to be " + operator + ":\n\n" + inspect(actual) + "\n\n" + inspect(expected)
        : String(options.message);
      super(message);
      this.name = "AssertionError";
      this.code = "ERR_ASSERTION";
      this.actual = actual;
      this.expected = expected;
      this.operator = operator;
      this.generatedMessage = options.message === undefined || options.message === null;
    }
  }

  function fail(actual, expected, message, operator) {
    if (arguments.length <= 1) {
      throw new AssertionError({ message: actual, operator: "fail" });
    }
    throw new AssertionError({ actual, expected, message, operator: operator || "fail" });
  }

  function assert(value, message) {
    if (!value) {
      throw new AssertionError({ actual: value, expected: true, message, operator: "==" });
    }
  }

  function ok(value, message) {
    return assert(value, message);
  }

  function equal(actual, expected, message) {
    if (actual != expected) {
      throw new AssertionError({ actual, expected, message, operator: "==" });
    }
  }

  function notEqual(actual, expected, message) {
    if (actual == expected) {
      throw new AssertionError({ actual, expected, message, operator: "!=" });
    }
  }

  function strictEqual(actual, expected, message) {
    if (!Object.is(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "strictEqual" });
    }
  }

  function notStrictEqual(actual, expected, message) {
    if (Object.is(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "notStrictEqual" });
    }
  }

  function deepEqualAssert(actual, expected, message) {
    if (!deepEqual(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "deepEqual" });
    }
  }

  function notDeepEqualAssert(actual, expected, message) {
    if (deepEqual(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "notDeepEqual" });
    }
  }

  function matchesExpected(error, expected) {
    if (expected === undefined || expected === null) return true;
    if (expected instanceof RegExp) return expected.test(error && (error.message || String(error)));
    if (typeof expected === "function") {
      if (error instanceof expected) return true;
      return expected(error) === true;
    }
    if (typeof expected === "object") {
      for (const key of Object.keys(expected)) {
        if (!deepEqual(error && error[key], expected[key])) return false;
      }
      return true;
    }
    return String(error && (error.message || error)).includes(String(expected));
  }

  function throws(fn, expected, message) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"fn\" argument must be of type function");
    }
    let thrown;
    try {
      fn();
    } catch (err) {
      thrown = err;
    }
    if (!thrown) {
      throw new AssertionError({ actual: undefined, expected, message, operator: "throws" });
    }
    if (!matchesExpected(thrown, expected)) {
      throw thrown;
    }
    return thrown;
  }

  function doesNotThrow(fn, expected, message) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"fn\" argument must be of type function");
    }
    try {
      fn();
    } catch (err) {
      if (!matchesExpected(err, expected)) throw err;
      throw new AssertionError({ actual: err, expected, message, operator: "doesNotThrow" });
    }
  }

  function ifError(value) {
    if (value) {
      throw value instanceof Error ? value : new AssertionError({ actual: value, expected: null, operator: "ifError" });
    }
  }

  function match(value, regexp, message) {
    if (!(regexp instanceof RegExp)) throw new TypeError("The \"regexp\" argument must be a RegExp");
    if (!regexp.test(String(value))) {
      throw new AssertionError({ actual: value, expected: regexp, message, operator: "match" });
    }
  }

  function doesNotMatch(value, regexp, message) {
    if (!(regexp instanceof RegExp)) throw new TypeError("The \"regexp\" argument must be a RegExp");
    if (regexp.test(String(value))) {
      throw new AssertionError({ actual: value, expected: regexp, message, operator: "doesNotMatch" });
    }
  }

  function rejects(promise, expected, message) {
    const source = typeof promise === "function" ? promise() : promise;
    return Promise.resolve(source).then(function () {
      throw new AssertionError({ actual: undefined, expected, message, operator: "rejects" });
    }, function (err) {
      if (!matchesExpected(err, expected)) throw err;
      return err;
    });
  }

  function doesNotReject(promise, expected, message) {
    const source = typeof promise === "function" ? promise() : promise;
    return Promise.resolve(source).catch(function (err) {
      if (!matchesExpected(err, expected)) throw err;
      throw new AssertionError({ actual: err, expected, message, operator: "doesNotReject" });
    });
  }

  Object.assign(assert, {
    AssertionError,
    fail,
    ok,
    equal,
    notEqual,
    deepEqual: deepEqualAssert,
    notDeepEqual: notDeepEqualAssert,
    deepStrictEqual: deepEqualAssert,
    notDeepStrictEqual: notDeepEqualAssert,
    strictEqual,
    notStrictEqual,
    throws,
    doesNotThrow,
    ifError,
    match,
    doesNotMatch,
    rejects,
    doesNotReject
  });

  function strict(value, message) {
    return ok(value, message);
  }
  Object.assign(strict, assert, {
    equal: strictEqual,
    deepEqual: deepEqualAssert,
    notEqual: notStrictEqual,
    notDeepEqual: notDeepEqualAssert
  });
  assert.strict = strict;
  assert.default = assert;
  return assert;
})()`
	value, err := runtime.RunProgram(scriptAssertShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptUtilObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const objectToString = Object.prototype.toString;

  function isArrayBuffer(value) {
    return objectToString.call(value) === "[object ArrayBuffer]";
  }

  function isArrayBufferView(value) {
    return value && typeof value === "object" && typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value);
  }

  function isUint8Array(value) {
    return objectToString.call(value) === "[object Uint8Array]" || (typeof Buffer !== "undefined" && Buffer.isBuffer(value));
  }

  function isTypedArray(value) {
    return isArrayBufferView(value) && !(typeof DataView !== "undefined" && value instanceof DataView);
  }

  function quoteString(value) {
    return "'" + String(value).replace(/\\/g, "\\\\").replace(/'/g, "\\'").replace(/\n/g, "\\n").replace(/\r/g, "\\r") + "'";
  }

  function inspect(value, options) {
    const maxDepth = options && typeof options.depth === "number" ? options.depth : 2;
    const seen = [];

    function inspectValue(input, depth) {
      if (input === null) {
        return "null";
      }
      if (input === undefined) {
        return "undefined";
      }
      const kind = typeof input;
      if (kind === "string") {
        return quoteString(input);
      }
      if (kind === "number" || kind === "boolean" || kind === "bigint") {
        return String(input);
      }
      if (kind === "symbol") {
        return input.toString();
      }
      if (kind === "function") {
        return "[Function" + (input.name ? ": " + input.name : "") + "]";
      }
      if (typeof Buffer !== "undefined" && Buffer.isBuffer(input)) {
        return "<Buffer " + Array.from(input).map((item) => item.toString(16).padStart(2, "0")).join(" ") + ">";
      }
      if (input instanceof Date) {
        return isNaN(input.getTime()) ? "Invalid Date" : input.toISOString();
      }
      if (input instanceof RegExp) {
        return input.toString();
      }
      if (seen.indexOf(input) !== -1) {
        return "[Circular]";
      }
      if (depth < 0) {
        return Array.isArray(input) ? "[Array]" : "[Object]";
      }
      seen.push(input);
      try {
        if (Array.isArray(input) || isTypedArray(input)) {
          const values = Array.prototype.slice.call(input).map((item) => inspectValue(item, depth - 1));
          return "[" + values.join(", ") + "]";
        }
        if (isArrayBuffer(input)) {
          return "ArrayBuffer { byteLength: " + input.byteLength + " }";
        }
        const keys = Object.keys(input);
        const entries = keys.map((key) => key + ": " + inspectValue(input[key], depth - 1));
        return "{ " + entries.join(", ") + " }";
      } finally {
        seen.pop();
      }
    }

    return inspectValue(value, maxDepth);
  }

  function jsonStringify(value) {
    const seen = [];
    try {
      return JSON.stringify(value, function (_, item) {
        if (item && typeof item === "object") {
          if (seen.indexOf(item) !== -1) {
            throw new TypeError("circular");
          }
          seen.push(item);
        }
        return item;
      });
    } catch (_) {
      return "[Circular]";
    }
  }

  function format(first) {
    const args = Array.prototype.slice.call(arguments, 1);
    if (typeof first !== "string") {
      return Array.prototype.slice.call(arguments).map((item) => inspect(item)).join(" ");
    }
    let index = 0;
    const out = first.replace(/%[sdifjoOc%]/g, function (token) {
      if (token === "%%") {
        return "%";
      }
      if (index >= args.length) {
        return token;
      }
      const value = args[index++];
      if (token === "%s") {
        return String(value);
      }
      if (token === "%d") {
        return String(Number(value));
      }
      if (token === "%i") {
        return String(parseInt(value, 10));
      }
      if (token === "%f") {
        return String(parseFloat(value));
      }
      if (token === "%j") {
        return jsonStringify(value);
      }
      if (token === "%c") {
        return "";
      }
      return inspect(value);
    });
    const extras = args.slice(index).map((item) => inspect(item));
    return extras.length > 0 ? out + " " + extras.join(" ") : out;
  }

  function isDeepStrictEqual(left, right) {
    const seen = [];

    function compare(a, b) {
      if (Object.is(a, b)) {
        return true;
      }
      if (typeof a !== typeof b || a === null || b === null) {
        return false;
      }
      if (typeof a !== "object") {
        return false;
      }
      if (a.constructor !== b.constructor) {
        return false;
      }
      if (a instanceof Date || b instanceof Date) {
        return a instanceof Date && b instanceof Date && a.getTime() === b.getTime();
      }
      if (a instanceof RegExp || b instanceof RegExp) {
        return a instanceof RegExp && b instanceof RegExp && String(a) === String(b);
      }
      if (isArrayBuffer(a) || isArrayBuffer(b)) {
        if (!isArrayBuffer(a) || !isArrayBuffer(b) || a.byteLength !== b.byteLength) {
          return false;
        }
        const aa = new Uint8Array(a);
        const bb = new Uint8Array(b);
        for (let i = 0; i < aa.length; i++) {
          if (aa[i] !== bb[i]) {
            return false;
          }
        }
        return true;
      }
      if (isArrayBufferView(a) || isArrayBufferView(b)) {
        if (!isArrayBufferView(a) || !isArrayBufferView(b) || a.constructor !== b.constructor || a.length !== b.length) {
          return false;
        }
        for (let i = 0; i < a.length; i++) {
          if (!compare(a[i], b[i])) {
            return false;
          }
        }
        return true;
      }
      const pair = [a, b];
      for (const item of seen) {
        if (item[0] === a && item[1] === b) {
          return true;
        }
      }
      seen.push(pair);
      const aKeys = Object.keys(a);
      const bKeys = Object.keys(b);
      if (aKeys.length !== bKeys.length) {
        return false;
      }
      aKeys.sort();
      bKeys.sort();
      for (let i = 0; i < aKeys.length; i++) {
        if (aKeys[i] !== bKeys[i] || !compare(a[aKeys[i]], b[bKeys[i]])) {
          return false;
        }
      }
      return true;
    }

    return compare(left, right);
  }

  function promisify(fn) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"original\" argument must be of type Function");
    }
    return function () {
      const self = this;
      const args = Array.prototype.slice.call(arguments);
      return new Promise(function (resolve, reject) {
        fn.apply(self, args.concat(function (err) {
          if (err) {
            reject(err);
            return;
          }
          const values = Array.prototype.slice.call(arguments, 1);
          resolve(values.length > 1 ? values : values[0]);
        }));
      });
    };
  }

  function callbackify(fn) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"original\" argument must be of type Function");
    }
    return function () {
      const self = this;
      const args = Array.prototype.slice.call(arguments);
      const callback = args.pop();
      if (typeof callback !== "function") {
        throw new TypeError("The last argument must be of type Function");
      }
      Promise.resolve(fn.apply(self, args)).then(function (value) {
        callback(null, value);
      }, function (err) {
        callback(err);
      });
    };
  }

  return {
    format,
    inspect,
    isDeepStrictEqual,
    promisify,
    callbackify,
    types: {
      isArrayBuffer,
      isArrayBufferView,
      isTypedArray,
      isUint8Array,
      isDate: function (value) { return value instanceof Date; },
      isRegExp: function (value) { return value instanceof RegExp; },
      isNativeError: function (value) { return value instanceof Error; },
      isMap: function (value) { return typeof Map !== "undefined" && value instanceof Map; },
      isSet: function (value) { return typeof Set !== "undefined" && value instanceof Set; },
      isPromise: function (value) { return !!(value && typeof value.then === "function"); }
    }
  };
})()`
	value, err := runtime.RunProgram(scriptUtilShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptAxios(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function cleanConfig(config) {
    if (typeof config === "string") {
      return { url: config };
    }
    if (!config) {
      return {};
    }
    return Object.assign({}, config);
  }
  function request(config) {
    return bru.sendRequest(cleanConfig(config));
  }
  function withUrl(method, url, config) {
    const next = cleanConfig(config);
    next.method = method;
    next.url = url;
    return request(next);
  }
  function withBody(method, url, data, config) {
    const next = cleanConfig(config);
    next.method = method;
    next.url = url;
    if (data !== undefined) {
      next.data = data;
    }
    return request(next);
  }
  function axios(config) {
    return request(config);
  }
  axios.request = request;
  axios.get = function(url, config) { return withUrl("GET", url, config); };
  axios.delete = function(url, config) { return withUrl("DELETE", url, config); };
  axios.head = function(url, config) { return withUrl("HEAD", url, config); };
  axios.options = function(url, config) { return withUrl("OPTIONS", url, config); };
  axios.post = function(url, data, config) { return withBody("POST", url, data, config); };
  axios.put = function(url, data, config) { return withBody("PUT", url, data, config); };
  axios.patch = function(url, data, config) { return withBody("PATCH", url, data, config); };
  axios.default = axios;
  return axios;
})()`
	value, err := runtime.RunProgram(scriptAxiosShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptQueryStringObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function escape(value) {
    return encodeURIComponent(String(value));
  }

  function unescape(value) {
    try {
      return decodeURIComponent(String(value));
    } catch (_) {
      return String(value);
    }
  }

  function parse(input, sep, eq, options) {
    const separator = sep === undefined ? "&" : String(sep);
    const equals = eq === undefined ? "=" : String(eq);
    const out = {};
    const text = String(input || "");
    if (text === "") {
      return out;
    }
    let maxKeys = 1000;
    if (options && Object.prototype.hasOwnProperty.call(options, "maxKeys")) {
      maxKeys = Number(options.maxKeys);
      if (!Number.isFinite(maxKeys) || maxKeys < 0) {
        maxKeys = 0;
      }
    }
    const parts = separator === "" ? [text] : text.split(separator);
    const limit = maxKeys === 0 ? parts.length : Math.min(parts.length, maxKeys);
    for (let index = 0; index < limit; index++) {
      const part = parts[index];
      if (part === "") {
        continue;
      }
      const eqIndex = equals === "" ? -1 : part.indexOf(equals);
      const rawKey = eqIndex === -1 ? part : part.slice(0, eqIndex);
      const rawValue = eqIndex === -1 ? "" : part.slice(eqIndex + equals.length);
      const key = unescape(rawKey.replace(/\+/g, "%20"));
      const value = unescape(rawValue.replace(/\+/g, "%20"));
      if (Object.prototype.hasOwnProperty.call(out, key)) {
        if (Array.isArray(out[key])) {
          out[key].push(value);
        } else {
          out[key] = [out[key], value];
        }
      } else {
        out[key] = value;
      }
    }
    return out;
  }

  function stringify(input, sep, eq) {
    const separator = sep === undefined ? "&" : String(sep);
    const equals = eq === undefined ? "=" : String(eq);
    if (input === undefined || input === null || typeof input !== "object") {
      return "";
    }
    const parts = [];
    for (const key of Object.keys(input)) {
      const value = input[key];
      const encodedKey = escape(key);
      if (Array.isArray(value)) {
        for (const item of value) {
          parts.push(encodedKey + equals + escape(item == null ? "" : item));
        }
      } else {
        parts.push(encodedKey + equals + escape(value == null ? "" : value));
      }
    }
    return parts.join(separator);
  }

  return { parse, decode: parse, stringify, encode: stringify, escape, unescape };
})()`
	value, err := runtime.RunProgram(scriptQueryStringShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func scriptBufferValue(runtime *goja.Runtime, data []byte) goja.Value {
	byteArray := scriptByteArrayValue(runtime, data)
	bufferValue := runtime.Get("Buffer")
	if bufferValue == nil || goja.IsUndefined(bufferValue) || goja.IsNull(bufferValue) {
		return byteArray
	}
	bufferObject := bufferValue.ToObject(runtime)
	from, ok := goja.AssertFunction(bufferObject.Get("from"))
	if !ok {
		return byteArray
	}
	value, err := from(bufferValue, byteArray)
	if err != nil {
		return byteArray
	}
	return value
}

func CompareAssertion(actual, operator, expected string) bool {
	switch operator {
	case "equals", "==":
		return actual == expected
	case "notEquals", "!=":
		return actual != expected
	case "contains":
		return strings.Contains(actual, expected)
	case "startsWith":
		return strings.HasPrefix(actual, expected)
	case "endsWith":
		return strings.HasSuffix(actual, expected)
	default:
		return false
	}
}

package scripting

// The sandbox fetch() and node-fetch module shims.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"errors"

	"github.com/dop251/goja"
)

// installScriptFetch takes the runtime meta for the same reason
// makeScriptSendRequest does: fetch() is a third entry point into
// scriptSendRequest, and an authorizer that covered bru.sendRequest and
// pm.sendRequest but not fetch() would leave the most web-idiomatic of the
// three unguarded. An earlier design round missed exactly that.
func installScriptFetch(runtime *goja.Runtime, vars map[string]string, meta ScriptRuntimeMeta) {
	_ = runtime.Set("__liteApiFetchSend", func(call goja.FunctionCall) goja.Value {
		// fetch() is the web API, not Postman's. Its `body` is a string or a
		// FormData the JS shim has already encoded, so it takes the payload
		// dialect — a `{mode: …}` object reaching here is somebody's JSON.
		responseValue, errorValue, _, err := scriptSendRequest(runtime, dialectBruno, call.Argument(0), vars, meta)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if responseValue != nil {
			return responseValue
		}
		if errorValue != nil {
			errorObject := errorValue.ToObject(runtime)
			response := errorObject.Get("response")
			if response != nil && !goja.IsUndefined(response) && !goja.IsNull(response) {
				return response
			}
			message := errorObject.Get("message")
			if message != nil && !goja.IsUndefined(message) && !goja.IsNull(message) {
				panic(runtime.NewGoError(errors.New(message.String())))
			}
		}
		panic(runtime.NewGoError(errors.New("fetch failed")))
	})
	script := `(function () {
  const send = globalThis.__liteApiFetchSend;
  const textEncoder = new TextEncoder();

  function normalizeName(name) {
    const value = String(name).toLowerCase();
    if (!value) throw new TypeError("Header name must not be empty");
    return value;
  }

  function normalizeValue(value) {
    return String(value);
  }

  class Headers {
    constructor(init) {
      Object.defineProperty(this, "_values", { value: {}, enumerable: false, writable: true });
      if (init === undefined || init === null) return;
      if (init instanceof Headers) {
        init.forEach((value, name) => this.append(name, value));
      } else if (Array.isArray(init)) {
        for (const pair of init) {
          if (!pair || pair.length < 2) throw new TypeError("Headers pair must be [name, value]");
          this.append(pair[0], pair[1]);
        }
      } else if (typeof init === "object") {
        for (const key of Object.keys(init)) this.append(key, init[key]);
      }
    }
    append(name, value) {
      const key = normalizeName(name);
      const next = normalizeValue(value);
      this._values[key] = this._values[key] ? this._values[key] + ", " + next : next;
    }
    set(name, value) {
      this._values[normalizeName(name)] = normalizeValue(value);
    }
    get(name) {
      const key = normalizeName(name);
      return Object.prototype.hasOwnProperty.call(this._values, key) ? this._values[key] : null;
    }
    has(name) {
      return Object.prototype.hasOwnProperty.call(this._values, normalizeName(name));
    }
    delete(name) {
      delete this._values[normalizeName(name)];
    }
    forEach(callback, thisArg) {
      for (const key of Object.keys(this._values).sort()) callback.call(thisArg, this._values[key], key, this);
    }
    entries() {
      return Object.keys(this._values).sort().map((key) => [key, this._values[key]])[Symbol.iterator]();
    }
    keys() {
      return Object.keys(this._values).sort()[Symbol.iterator]();
    }
    values() {
      return Object.keys(this._values).sort().map((key) => this._values[key])[Symbol.iterator]();
    }
    toObject() {
      const out = {};
      for (const key of Object.keys(this._values)) out[key] = this._values[key];
      return out;
    }
    [Symbol.iterator]() {
      return this.entries();
    }
  }

  function bytesFromPart(part) {
    if (part === undefined || part === null) return [];
    if (part instanceof Blob) return part._bytes.slice();
    if (Array.isArray(part)) return part.map((value) => Number(value) & 255);
    if (part instanceof ArrayBuffer) return Array.from(new Uint8Array(part));
    if (ArrayBuffer.isView(part)) return Array.from(new Uint8Array(part.buffer, part.byteOffset, part.byteLength));
    return Array.from(textEncoder.encode(String(part)));
  }

  function stringFromBytes(bytes) {
    let encoded = "";
    for (let i = 0; i < bytes.length; i++) {
      const value = Number(bytes[i]) & 255;
      encoded += value < 0x80 ? String.fromCharCode(value) : "%" + value.toString(16).padStart(2, "0").toUpperCase();
    }
    try {
      return decodeURIComponent(encoded);
    } catch (_) {
      return bytes.map((value) => String.fromCharCode(Number(value) & 255)).join("");
    }
  }

  function arrayBufferFromBytes(bytes) {
    const buffer = new ArrayBuffer(bytes.length);
    new Uint8Array(buffer).set(bytes);
    return buffer;
  }

  class Blob {
    constructor(parts, options) {
      const bytes = [];
      for (const part of parts || []) bytes.push.apply(bytes, bytesFromPart(part));
      Object.defineProperty(this, "_bytes", { value: bytes, enumerable: false, writable: false });
      this.size = bytes.length;
      this.type = options && options.type ? String(options.type).toLowerCase() : "";
    }
    text() {
      return Promise.resolve(stringFromBytes(this._bytes));
    }
    arrayBuffer() {
      return Promise.resolve(arrayBufferFromBytes(this._bytes));
    }
    slice(start, end, type) {
      const size = this._bytes.length;
      const begin = start === undefined ? 0 : Math.max(0, start < 0 ? size + Number(start) : Number(start));
      const finish = end === undefined ? size : Math.max(begin, end < 0 ? size + Number(end) : Number(end));
      return new Blob([new Uint8Array(this._bytes.slice(begin, Math.min(size, finish)))], { type });
    }
  }

  class FormData {
    constructor() {
      Object.defineProperty(this, "_entries", { value: [], enumerable: false, writable: true });
    }
    append(name, value) {
      this._entries.push([String(name), value]);
    }
    set(name, value) {
      this.delete(name);
      this.append(name, value);
    }
    get(name) {
      const key = String(name);
      const pair = this._entries.find((item) => item[0] === key);
      return pair ? pair[1] : null;
    }
    getAll(name) {
      const key = String(name);
      return this._entries.filter((item) => item[0] === key).map((item) => item[1]);
    }
    has(name) {
      const key = String(name);
      return this._entries.some((item) => item[0] === key);
    }
    delete(name) {
      const key = String(name);
      this._entries = this._entries.filter((item) => item[0] !== key);
    }
    entries() {
      return this._entries.slice()[Symbol.iterator]();
    }
    keys() {
      return this._entries.map((item) => item[0])[Symbol.iterator]();
    }
    values() {
      return this._entries.map((item) => item[1])[Symbol.iterator]();
    }
    forEach(callback, thisArg) {
      for (const pair of this._entries) callback.call(thisArg, pair[1], pair[0], this);
    }
    [Symbol.iterator]() {
      return this.entries();
    }
  }

  class BodyHolder {
    _initBody(body) {
      this.bodyUsed = false;
      this._bodyBytes = bytesFromPart(body === undefined ? "" : body);
    }
    _consume() {
      if (this.bodyUsed) return Promise.reject(new TypeError("Body has already been consumed"));
      this.bodyUsed = true;
      return Promise.resolve(this._bodyBytes.slice());
    }
    text() {
      return this._consume().then((bytes) => stringFromBytes(bytes));
    }
    json() {
      return this.text().then((text) => JSON.parse(text));
    }
    arrayBuffer() {
      return this._consume().then((bytes) => arrayBufferFromBytes(bytes));
    }
    blob() {
      return this._consume().then((bytes) => new Blob([new Uint8Array(bytes)], { type: this.headers && this.headers.get("content-type") || "" }));
    }
  }

  class Request extends BodyHolder {
    constructor(input, init) {
      super();
      init = init || {};
      const source = input instanceof Request ? input : null;
      this.url = source ? source.url : String(input);
      this.method = String(init.method || (source && source.method) || "GET").toUpperCase();
      this.headers = new Headers(init.headers || (source && source.headers) || undefined);
      this.signal = init.signal || (source && source.signal) || null;
      this._initBody(init.body !== undefined ? init.body : (source ? source._bodyBytes : ""));
    }
    clone() {
      return new Request(this, { body: new Uint8Array(this._bodyBytes) });
    }
  }

  class Response extends BodyHolder {
    constructor(body, init) {
      super();
      init = init || {};
      this.status = init.status === undefined ? 200 : Number(init.status);
      this.statusText = init.statusText === undefined ? "" : String(init.statusText);
      this.headers = new Headers(init.headers);
      this.ok = this.status >= 200 && this.status <= 299;
      this.url = init.url || "";
      this._initBody(body === undefined ? "" : body);
    }
    clone() {
      return new Response(new Uint8Array(this._bodyBytes), {
        status: this.status,
        statusText: this.statusText,
        headers: this.headers,
        url: this.url
      });
    }
    static json(value, init) {
      init = init || {};
      const headers = new Headers(init.headers);
      if (!headers.has("content-type")) headers.set("content-type", "application/json");
      init.headers = headers;
      return new Response(JSON.stringify(value), init);
    }
  }

  class AbortSignal extends EventTarget {
    constructor() {
      super();
      this.aborted = false;
      this.reason = undefined;
    }
    throwIfAborted() {
      if (this.aborted) throw this.reason || new Error("This operation was aborted");
    }
  }

  class AbortController {
    constructor() {
      this.signal = new AbortSignal();
    }
    abort(reason) {
      if (this.signal.aborted) return;
      this.signal.aborted = true;
      this.signal.reason = reason === undefined ? new Error("This operation was aborted") : reason;
      this.signal.dispatchEvent(new Event("abort"));
    }
  }

  function bodyForFetch(request) {
    if (!request._bodyBytes || request._bodyBytes.length === 0) return undefined;
    return stringFromBytes(request._bodyBytes);
  }

  function fetch(input, init) {
    const request = new Request(input, init);
    if (request.signal && request.signal.aborted) {
      return Promise.reject(request.signal.reason || new Error("This operation was aborted"));
    }
    const config = {
      url: request.url,
      method: request.method,
      headers: request.headers.toObject()
    };
    const body = bodyForFetch(request);
    if (body !== undefined) config.body = body;
    try {
      const res = send(config);
      return Promise.resolve(new Response(res.body || "", {
        status: Number(res.status || res.statusCode || 0),
        statusText: res.statusText || "",
        headers: res.headers || {},
        url: res.url || request.url
      }));
    } catch (err) {
      return Promise.reject(err);
    }
  }

  globalThis.Headers = Headers;
  globalThis.Request = Request;
  globalThis.Response = Response;
  globalThis.AbortController = AbortController;
  globalThis.AbortSignal = AbortSignal;
  globalThis.FormData = FormData;
  globalThis.Blob = Blob;
  globalThis.fetch = fetch;
})()`
	if _, err := runtime.RunProgram(scriptFetchShim.compiled(script)); err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiFetchSend", goja.Undefined())
}

func newScriptNodeFetchModule(runtime *goja.Runtime) goja.Value {
	fetch := runtime.Get("fetch")
	if fetch == nil || goja.IsUndefined(fetch) || goja.IsNull(fetch) {
		fetch = goja.Undefined()
	}
	fetchObject := fetch.ToObject(runtime)
	for _, name := range []string{"Headers", "Request", "Response", "AbortController", "AbortSignal", "FormData", "Blob"} {
		_ = fetchObject.Set(name, runtime.Get(name))
	}
	_ = fetchObject.Set("default", fetch)
	return fetch
}

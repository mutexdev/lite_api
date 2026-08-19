package scripting

// The sandbox URL and URLSearchParams shims.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

func newScriptURLObject(runtime *goja.Runtime) goja.Value {
	parseFunc := func(call goja.FunctionCall) goja.Value {
		parseQuery := false
		if len(call.Arguments) > 1 {
			parseQuery = call.Argument(1).ToBoolean()
		}
		return scriptURLParseValue(runtime, call.Argument(0).String(), parseQuery)
	}
	formatFunc := func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptURLFormatValue(runtime, call.Argument(0)))
	}
	resolveFunc := func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptURLResolve(call.Argument(0).String(), call.Argument(1).String()))
	}
	resolveObjectFunc := func(call goja.FunctionCall) goja.Value {
		resolved := scriptURLResolve(call.Argument(0).String(), call.Argument(1).String())
		return scriptURLParseValue(runtime, resolved, false)
	}
	_ = runtime.Set("__liteApiURLParse", parseFunc)
	_ = runtime.Set("__liteApiURLFormat", formatFunc)
	_ = runtime.Set("__liteApiURLResolve", resolveFunc)
	_ = runtime.Set("__liteApiURLResolveObject", resolveObjectFunc)
	script := `(function () {
  const parse = globalThis.__liteApiURLParse;
  const format = globalThis.__liteApiURLFormat;
  const resolve = globalThis.__liteApiURLResolve;
  const resolveObject = globalThis.__liteApiURLResolveObject;

  function decodeParam(value) {
    try {
      return decodeURIComponent(String(value).replace(/\+/g, " "));
    } catch (_) {
      return String(value);
    }
  }

  function encodeParam(value) {
    return encodeURIComponent(String(value)).replace(/%20/g, "+");
  }

  class URLSearchParams {
    constructor(init) {
      this._pairs = [];
      if (init === undefined || init === null) {
        return;
      }
      if (init instanceof URLSearchParams) {
        for (const pair of init._pairs) {
          this.append(pair[0], pair[1]);
        }
        return;
      }
      if (typeof init === "string") {
        const raw = init.charAt(0) === "?" ? init.slice(1) : init;
        if (raw.length === 0) {
          return;
        }
        for (const part of raw.split("&")) {
          if (part === "") {
            continue;
          }
          const index = part.indexOf("=");
          const key = index === -1 ? part : part.slice(0, index);
          const value = index === -1 ? "" : part.slice(index + 1);
          this.append(decodeParam(key), decodeParam(value));
        }
        return;
      }
      if (Array.isArray(init)) {
        for (const pair of init) {
          if (!pair || pair.length < 2) {
            throw new TypeError("Each query pair must be an iterable [name, value] tuple");
          }
          this.append(pair[0], pair[1]);
        }
        return;
      }
      if (typeof init === "object") {
        for (const key of Object.keys(init)) {
          this.append(key, init[key]);
        }
      }
    }

    append(name, value) {
      this._pairs.push([String(name), String(value)]);
    }

    delete(name) {
      const key = String(name);
      this._pairs = this._pairs.filter((pair) => pair[0] !== key);
    }

    get(name) {
      const key = String(name);
      const pair = this._pairs.find((item) => item[0] === key);
      return pair ? pair[1] : null;
    }

    getAll(name) {
      const key = String(name);
      return this._pairs.filter((pair) => pair[0] === key).map((pair) => pair[1]);
    }

    has(name) {
      const key = String(name);
      return this._pairs.some((pair) => pair[0] === key);
    }

    set(name, value) {
      const key = String(name);
      this.delete(key);
      this.append(key, value);
    }

    sort() {
      this._pairs.sort((left, right) => left[0] < right[0] ? -1 : left[0] > right[0] ? 1 : 0);
    }

    forEach(callback, thisArg) {
      for (const pair of this._pairs) {
        callback.call(thisArg, pair[1], pair[0], this);
      }
    }

    entries() {
      return this._pairs.map((pair) => [pair[0], pair[1]])[Symbol.iterator]();
    }

    keys() {
      return this._pairs.map((pair) => pair[0])[Symbol.iterator]();
    }

    values() {
      return this._pairs.map((pair) => pair[1])[Symbol.iterator]();
    }

    toString() {
      return this._pairs.map((pair) => encodeParam(pair[0]) + "=" + encodeParam(pair[1])).join("&");
    }

    [Symbol.iterator]() {
      return this.entries();
    }
  }

  class URL {
    constructor(input, base) {
      const raw = base === undefined ? String(input) : resolve(String(base), String(input));
      this._parts = parse(raw, false);
      this.searchParams = new URLSearchParams(this._parts.search || "");
    }

    _formatParts() {
      const parts = {};
      for (const key of Object.keys(this._parts)) {
        parts[key] = this._parts[key];
      }
      const query = this.searchParams.toString();
      parts.search = query ? "?" + query : "";
      parts.query = query;
      parts.path = (parts.pathname || "") + (parts.search || "");
      return parts;
    }

    get href() {
      return format(this._formatParts());
    }

    set href(value) {
      this._parts = parse(String(value), false);
      this.searchParams = new URLSearchParams(this._parts.search || "");
    }

    get protocol() { return this._parts.protocol || ""; }
    set protocol(value) {
      let protocol = String(value || "");
      if (protocol && !protocol.endsWith(":")) {
        protocol += ":";
      }
      this._parts.protocol = protocol;
    }

    get username() {
      const auth = this._parts.auth || "";
      const index = auth.indexOf(":");
      return index === -1 ? auth : auth.slice(0, index);
    }
    set username(value) {
      this._parts.auth = String(value || "") + (this.password ? ":" + this.password : "");
    }

    get password() {
      const auth = this._parts.auth || "";
      const index = auth.indexOf(":");
      return index === -1 ? "" : auth.slice(index + 1);
    }
    set password(value) {
      this._parts.auth = this.username + (value ? ":" + String(value) : "");
    }

    get host() { return this._parts.host || ""; }
    set host(value) {
      this._parts.host = String(value || "");
      const parts = this._parts.host.split(":");
      this._parts.hostname = parts[0] || "";
      this._parts.port = parts.length > 1 ? parts.slice(1).join(":") : "";
    }

    get hostname() { return this._parts.hostname || ""; }
    set hostname(value) {
      this._parts.hostname = String(value || "");
      this._parts.host = this._parts.hostname + (this._parts.port ? ":" + this._parts.port : "");
    }

    get port() { return this._parts.port || ""; }
    set port(value) {
      this._parts.port = String(value || "");
      this._parts.host = (this._parts.hostname || "") + (this._parts.port ? ":" + this._parts.port : "");
    }

    get pathname() { return this._parts.pathname || ""; }
    set pathname(value) { this._parts.pathname = String(value || ""); }

    get search() {
      const query = this.searchParams.toString();
      return query ? "?" + query : "";
    }
    set search(value) {
      this.searchParams = new URLSearchParams(String(value || ""));
    }

    get hash() { return this._parts.hash || ""; }
    set hash(value) {
      const text = String(value || "");
      this._parts.hash = text && text.charAt(0) !== "#" ? "#" + text : text;
    }

    get origin() {
      if (!this.protocol || !this.host) {
        return "null";
      }
      return this.protocol + "//" + this.host;
    }

    toString() { return this.href; }
    toJSON() { return this.href; }
  }

  const module = {
    parse,
    format,
    resolve,
    resolveObject,
    URL,
    URLSearchParams,
    domainToASCII: function (value) { return String(value); },
    domainToUnicode: function (value) { return String(value); }
  };
  globalThis.URL = URL;
  globalThis.URLSearchParams = URLSearchParams;
  return module;
})()`
	value, err := runtime.RunProgram(scriptURLShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiURLParse", goja.Undefined())
	_ = runtime.Set("__liteApiURLFormat", goja.Undefined())
	_ = runtime.Set("__liteApiURLResolve", goja.Undefined())
	_ = runtime.Set("__liteApiURLResolveObject", goja.Undefined())
	return value
}

func scriptURLParseValue(runtime *goja.Runtime, raw string, parseQuery bool) goja.Value {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	object := runtime.NewObject()
	setNullableString := func(name, value string) {
		if value == "" {
			_ = object.Set(name, goja.Null())
			return
		}
		_ = object.Set(name, value)
	}
	protocol := ""
	if parsed.Scheme != "" {
		protocol = parsed.Scheme + ":"
	}
	setNullableString("protocol", protocol)
	if protocol != "" && strings.HasPrefix(strings.TrimPrefix(raw, protocol), "//") {
		_ = object.Set("slashes", true)
	} else {
		_ = object.Set("slashes", goja.Null())
	}
	auth := ""
	if parsed.User != nil {
		auth = parsed.User.String()
	}
	setNullableString("auth", auth)
	setNullableString("host", parsed.Host)
	setNullableString("port", parsed.Port())
	setNullableString("hostname", parsed.Hostname())
	hash := ""
	if parsed.Fragment != "" {
		hash = "#" + parsed.Fragment
	}
	setNullableString("hash", hash)
	search := ""
	if parsed.RawQuery != "" {
		search = "?" + parsed.RawQuery
	}
	setNullableString("search", search)
	if parseQuery {
		queryObject := runtime.NewObject()
		if parsed.RawQuery != "" {
			for key, values := range parsed.Query() {
				if len(values) == 1 {
					_ = queryObject.Set(key, values[0])
				} else {
					_ = queryObject.Set(key, values)
				}
			}
		}
		_ = object.Set("query", queryObject)
	} else {
		setNullableString("query", parsed.RawQuery)
	}
	pathname := parsed.EscapedPath()
	if pathname == "" && parsed.Host != "" {
		pathname = "/"
	}
	setNullableString("pathname", pathname)
	pathValue := pathname
	if search != "" {
		pathValue += search
	}
	setNullableString("path", pathValue)
	href := parsed.String()
	if parsed.Host != "" && parsed.Path == "" {
		prefix := protocol + "//" + parsed.Host
		if parsed.User != nil {
			prefix = protocol + "//" + parsed.User.String() + "@" + parsed.Host
		}
		href = prefix + "/"
		if parsed.RawQuery != "" {
			href += "?" + parsed.RawQuery
		}
		if parsed.Fragment != "" {
			href += "#" + parsed.Fragment
		}
	}
	_ = object.Set("href", href)
	return object
}

func scriptURLFormatValue(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported, ok := value.Export().(string); ok {
		return exported
	}
	object := value.ToObject(runtime)
	protocol := scriptURLObjectString(object, "protocol")
	if protocol != "" && !strings.HasSuffix(protocol, ":") {
		protocol += ":"
	}
	auth := scriptURLObjectString(object, "auth")
	host := scriptURLObjectString(object, "host")
	hostname := scriptURLObjectString(object, "hostname")
	port := scriptURLObjectString(object, "port")
	if host == "" && hostname != "" {
		host = hostname
		if port != "" {
			host += ":" + port
		}
	}
	pathname := scriptURLObjectString(object, "pathname")
	search := scriptURLObjectString(object, "search")
	if search == "" {
		search = scriptURLQueryString(runtime, object.Get("query"))
		if search != "" {
			search = "?" + search
		}
	} else if !strings.HasPrefix(search, "?") {
		search = "?" + search
	}
	hash := scriptURLObjectString(object, "hash")
	if hash != "" && !strings.HasPrefix(hash, "#") {
		hash = "#" + hash
	}
	var builder strings.Builder
	if protocol != "" {
		builder.WriteString(protocol)
	}
	if host != "" {
		builder.WriteString("//")
		if auth != "" {
			builder.WriteString(auth)
			builder.WriteString("@")
		}
		builder.WriteString(host)
	}
	if pathname != "" {
		if host != "" && !strings.HasPrefix(pathname, "/") {
			builder.WriteString("/")
		}
		builder.WriteString(pathname)
	} else if host != "" && (search != "" || hash != "") {
		builder.WriteString("/")
	}
	builder.WriteString(search)
	builder.WriteString(hash)
	return builder.String()
}

func scriptURLObjectString(object *goja.Object, name string) string {
	value := object.Get(name)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported := value.Export(); exported != nil {
		return fmt.Sprint(exported)
	}
	return value.String()
}

func scriptURLQueryString(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported, ok := value.Export().(string); ok {
		return exported
	}
	object := value.ToObject(runtime)
	values := url.Values{}
	for _, key := range object.Keys() {
		item := object.Get(key)
		if item == nil || goja.IsUndefined(item) || goja.IsNull(item) {
			values.Add(key, "")
			continue
		}
		if exported, ok := item.Export().([]interface{}); ok {
			for _, part := range exported {
				values.Add(key, fmt.Sprint(part))
			}
			continue
		}
		values.Add(key, fmt.Sprint(item.Export()))
	}
	return values.Encode()
}

func scriptURLResolve(baseRaw, refRaw string) string {
	base, err := url.Parse(baseRaw)
	if err != nil {
		return refRaw
	}
	ref, err := url.Parse(refRaw)
	if err != nil {
		return refRaw
	}
	return base.ResolveReference(ref).String()
}

func scriptBodyIsFormURLEncoded(item types.RequestItem, headers []types.KeyValue) bool {
	if item.Body.Mode == "formUrlEncoded" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(types.GetKeyValue(headers, "Content-Type"))), "application/x-www-form-urlencoded")
}

func scriptFormURLEncodedValue(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported, ok := value.Export().(string); ok {
		return exported
	}
	object := value.ToObject(runtime)
	if scriptValueIsArray(value) {
		rows := []string{}
		length := int(object.Get("length").ToInteger())
		for index := 0; index < length; index++ {
			row := object.Get(strconv.Itoa(index))
			if row == nil || goja.IsUndefined(row) || goja.IsNull(row) {
				continue
			}
			rowObject := row.ToObject(runtime)
			rows = append(rows, scriptFormPair(rowObject.Get("name"), rowObject.Get("value")))
		}
		return strings.Join(rows, "&")
	}
	rows := []string{}
	for _, key := range object.Keys() {
		field := object.Get(key)
		if scriptValueIsArray(field) {
			fieldObject := field.ToObject(runtime)
			length := int(fieldObject.Get("length").ToInteger())
			for index := 0; index < length; index++ {
				rows = append(rows, scriptFormPair(runtime.ToValue(key), fieldObject.Get(strconv.Itoa(index))))
			}
			continue
		}
		rows = append(rows, scriptFormPair(runtime.ToValue(key), field))
	}
	return strings.Join(rows, "&")
}

func scriptFormURLEncodedBody(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []interface{}:
		rows := make([]string, 0, len(typed))
		for _, item := range typed {
			data, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			rows = append(rows, scriptFormPairFromStrings(scriptFormInterfaceString(data["name"]), scriptFormInterfaceString(data["value"])))
		}
		return strings.Join(rows, "&")
	case map[string]interface{}:
		rows := make([]string, 0, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := typed[key]
			if items, ok := value.([]interface{}); ok {
				for _, item := range items {
					rows = append(rows, scriptFormPairFromStrings(key, scriptFormInterfaceString(item)))
				}
				continue
			}
			rows = append(rows, scriptFormPairFromStrings(key, scriptFormInterfaceString(value)))
		}
		return strings.Join(rows, "&")
	default:
		return fmt.Sprint(typed)
	}
}

func EncodeRequestURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	queryIndex := strings.Index(rawURL, "?")
	originAndPath := rawURL
	queryString := ""
	if queryIndex >= 0 {
		originAndPath = rawURL[:queryIndex]
		queryString = rawURL[queryIndex+1:]
	}
	origin, path := splitURLOriginAndPath(originAndPath)
	result := origin + encodeURLPath(path)
	if queryIndex >= 0 {
		result += "?" + encodeURLQuery(queryString)
	}
	return result
}

func validURLScheme(scheme string) bool {
	if scheme == "" {
		return false
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 {
			if !isAlpha {
				return false
			}
			continue
		}
		if !isAlpha && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func encodeURLQuery(query string) string {
	if query == "" {
		return ""
	}
	pairs := strings.Split(query, "&")
	encodedPairs := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		name, value, hasValue := strings.Cut(pair, "=")
		if name == "" {
			continue
		}
		encodedName := encodeURIComponent(name)
		if hasValue {
			encodedPairs = append(encodedPairs, encodedName+"="+encodeURIComponent(value))
		} else {
			encodedPairs = append(encodedPairs, encodedName)
		}
	}
	return strings.Join(encodedPairs, "&")
}

package scripting

// The sandbox cookie jar shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

type CookieJar struct {
	cookies []types.CookieEntry
}

func NewScriptCookieJar(cookies []types.CookieEntry) *CookieJar {
	return &CookieJar{cookies: CloneCookieEntries(cookies)}
}

func newScriptCookiesObject(runtime *goja.Runtime, jar *CookieJar, currentURL func() string, interpolateValue func(string) string) *goja.Object {
	cookiesObject := runtime.NewObject()
	if interpolateValue == nil {
		interpolateValue = func(value string) string { return value }
	}
	currentRows := func() []types.CookieEntry {
		if currentURL == nil {
			return jar.Snapshot()
		}
		return jar.matching(currentURL())
	}
	_ = cookiesObject.Set("all", func() goja.Value {
		return scriptCookieArray(runtime, currentRows())
	})
	_ = cookiesObject.Set("count", func() int {
		return len(currentRows())
	})
	_ = cookiesObject.Set("get", func(name string) goja.Value {
		rows := currentRows()
		for index := len(rows) - 1; index >= 0; index-- {
			cookie := rows[index]
			if cookie.Name == name {
				return runtime.ToValue(cookie.Value)
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("has", func(name string, values ...string) bool {
		hasValue := len(values) > 0
		expected := ""
		if hasValue {
			expected = values[0]
		}
		for _, cookie := range currentRows() {
			if cookie.Name == name && (!hasValue || cookie.Value == expected) {
				return true
			}
		}
		return false
	})
	_ = cookiesObject.Set("one", func(name string) goja.Value {
		rows := currentRows()
		for index := len(rows) - 1; index >= 0; index-- {
			if rows[index].Name == name {
				return scriptCookieValue(runtime, rows[index])
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("idx", func(index int) goja.Value {
		rows := currentRows()
		if index < 0 || index >= len(rows) {
			return goja.Undefined()
		}
		return scriptCookieValue(runtime, rows[index])
	})
	_ = cookiesObject.Set("indexOf", func(call goja.FunctionCall) goja.Value {
		for index, cookie := range currentRows() {
			if scriptCookieArgumentMatches(cookie, call.Argument(0)) {
				return runtime.ToValue(index)
			}
		}
		return runtime.ToValue(-1)
	})
	_ = cookiesObject.Set("find", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		for index, cookie := range currentRows() {
			value := scriptCookieValue(runtime, cookie)
			matched, err := fn(goja.Undefined(), value, runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			if matched.ToBoolean() {
				return value
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("filter", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return runtime.NewArray()
		}
		result := []types.CookieEntry{}
		for index, cookie := range currentRows() {
			matched, err := fn(goja.Undefined(), scriptCookieValue(runtime, cookie), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			if matched.ToBoolean() {
				result = append(result, cookie)
			}
		}
		return scriptCookieArray(runtime, result)
	})
	_ = cookiesObject.Set("each", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		for index, cookie := range currentRows() {
			if _, err := fn(goja.Undefined(), scriptCookieValue(runtime, cookie), runtime.ToValue(index)); err != nil {
				panic(err)
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("map", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return runtime.NewArray()
		}
		result := []interface{}{}
		for index, cookie := range currentRows() {
			mapped, err := fn(goja.Undefined(), scriptCookieValue(runtime, cookie), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			result = append(result, mapped.Export())
		}
		return runtime.NewArray(result...)
	})
	_ = cookiesObject.Set("reduce", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		rows := currentRows()
		if len(rows) == 0 && len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		index := 0
		accumulator := call.Argument(1)
		if len(call.Arguments) < 2 || goja.IsUndefined(accumulator) {
			accumulator = scriptCookieValue(runtime, rows[0])
			index = 1
		}
		for ; index < len(rows); index++ {
			next, err := fn(goja.Undefined(), accumulator, scriptCookieValue(runtime, rows[index]), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			accumulator = next
		}
		return accumulator
	})
	_ = cookiesObject.Set("toObject", func() map[string]string {
		out := map[string]string{}
		for _, cookie := range currentRows() {
			out[cookie.Name] = cookie.Value
		}
		return out
	})
	_ = cookiesObject.Set("toString", func() string {
		return cookiejar.Header(currentRows())
	})
	_ = cookiesObject.Set("toJSON", func() goja.Value {
		return scriptCookieArray(runtime, currentRows())
	})
	upsert := func(data map[string]interface{}) goja.Value {
		cookie, err := scriptCookieFromMap(data, currentURL())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		jar.upsert(cookie, cookieSourceHost(currentURL()))
		return goja.Undefined()
	}
	_ = cookiesObject.Set("add", upsert)
	_ = cookiesObject.Set("upsert", upsert)
	remove := func(name string) goja.Value {
		jar.removeMatching(currentURL(), name)
		return goja.Undefined()
	}
	_ = cookiesObject.Set("remove", remove)
	_ = cookiesObject.Set("delete", remove)
	_ = cookiesObject.Set("clear", func() goja.Value {
		jar.clearMatching(currentURL())
		return goja.Undefined()
	})
	_ = cookiesObject.Set("jar", func() *goja.Object {
		return newScriptCookieJarObject(runtime, jar, interpolateValue)
	})
	return cookiesObject
}

func newScriptCookieJarObject(runtime *goja.Runtime, jar *CookieJar, interpolateValue func(string) string) *goja.Object {
	object := runtime.NewObject()
	cleanURL := func(rawURL string) string {
		return interpolateValue(strings.TrimSpace(rawURL))
	}
	cookieCallbackResult := func(callback goja.Value, err error, result goja.Value, fallback goja.Value) goja.Value {
		if scriptCookieInvokeCallback(runtime, callback, err, result) {
			return scriptResolvedPromise(runtime, goja.Undefined())
		}
		if err != nil {
			return scriptRejectedPromise(runtime, map[string]interface{}{"message": err.Error()})
		}
		if fallback == nil {
			fallback = goja.Undefined()
		}
		return scriptResolvedPromise(runtime, fallback)
	}
	cookiePromiseResult := func(callback goja.Value, err error, result goja.Value) goja.Value {
		if scriptCookieInvokeCallback(runtime, callback, err, result) {
			return scriptResolvedPromise(runtime, goja.Undefined())
		}
		if err != nil {
			return scriptRejectedPromise(runtime, map[string]interface{}{"message": err.Error()})
		}
		return scriptResolvedPromise(runtime, result)
	}
	_ = object.Set("getCookies", func(call goja.FunctionCall) goja.Value {
		result := scriptCookieArray(runtime, jar.matching(cleanURL(call.Argument(0).String())))
		return cookieCallbackResult(call.Argument(1), nil, result, result)
	})
	_ = object.Set("getCookie", func(call goja.FunctionCall) goja.Value {
		result := goja.Null()
		if cookie, ok := scriptCookieJarCookie(jar.matching(cleanURL(call.Argument(0).String())), call.Argument(1).String()); ok {
			result = scriptCookieValue(runtime, cookie)
		}
		return cookieCallbackResult(call.Argument(2), nil, result, result)
	})
	_ = object.Set("hasCookie", func(call goja.FunctionCall) goja.Value {
		_, ok := scriptCookieJarCookie(jar.matching(cleanURL(call.Argument(0).String())), call.Argument(1).String())
		result := runtime.ToValue(ok)
		return cookieCallbackResult(call.Argument(2), nil, result, result)
	})
	_ = object.Set("setCookie", func(call goja.FunctionCall) goja.Value {
		rawURL := call.Argument(0).String()
		nameOrCookie := call.Argument(1).Export()
		var callback goja.Value
		values := []string{}
		if data, ok := nameOrCookie.(map[string]interface{}); ok && data != nil {
			callback = call.Argument(2)
		} else if _, ok := goja.AssertFunction(call.Argument(2)); ok {
			values = append(values, "")
			callback = call.Argument(2)
		} else {
			if !goja.IsUndefined(call.Argument(2)) && !goja.IsNull(call.Argument(2)) {
				values = append(values, fmt.Sprint(call.Argument(2).Export()))
			} else {
				values = append(values, "")
			}
			callback = call.Argument(3)
		}
		cookieMap, err := scriptCookieMapFromSetCookieArgs(nameOrCookie, values...)
		if err != nil {
			return cookiePromiseResult(callback, err, goja.Undefined())
		}
		cleanedURL := cleanURL(rawURL)
		cookie, err := scriptCookieFromMap(cookieMap, cleanedURL)
		if err != nil {
			return cookiePromiseResult(callback, err, goja.Undefined())
		}
		jar.upsert(cookie, cookieSourceHost(cleanedURL))
		return cookiePromiseResult(callback, nil, goja.Undefined())
	})
	_ = object.Set("setCookies", func(call goja.FunctionCall) goja.Value {
		rawURL := call.Argument(0).String()
		cookies, err := scriptCookieMapsFromValue(call.Argument(1))
		if err != nil {
			return cookiePromiseResult(call.Argument(2), err, goja.Undefined())
		}
		for _, cookieMap := range cookies {
			cleanedURL := cleanURL(rawURL)
			cookie, err := scriptCookieFromMap(cookieMap, cleanedURL)
			if err != nil {
				return cookiePromiseResult(call.Argument(2), err, goja.Undefined())
			}
			jar.upsert(cookie, cookieSourceHost(cleanedURL))
		}
		return cookiePromiseResult(call.Argument(2), nil, goja.Undefined())
	})
	_ = object.Set("deleteCookie", func(call goja.FunctionCall) goja.Value {
		jar.removeMatching(cleanURL(call.Argument(0).String()), call.Argument(1).String())
		return cookiePromiseResult(call.Argument(2), nil, goja.Undefined())
	})
	_ = object.Set("deleteCookies", func(call goja.FunctionCall) goja.Value {
		jar.clearMatching(cleanURL(call.Argument(0).String()))
		return cookiePromiseResult(call.Argument(1), nil, goja.Undefined())
	})
	_ = object.Set("clear", func(call goja.FunctionCall) goja.Value {
		jar.cookies = []types.CookieEntry{}
		return cookiePromiseResult(call.Argument(0), nil, goja.Undefined())
	})
	return object
}

func scriptCookieInvokeCallback(runtime *goja.Runtime, callbackValue goja.Value, err error, result goja.Value) bool {
	callback, ok := goja.AssertFunction(callbackValue)
	if !ok {
		return false
	}
	errArg := goja.Null()
	resultArg := result
	if err != nil {
		errArg = runtime.NewGoError(err)
		resultArg = goja.Null()
	} else if resultArg == nil {
		resultArg = goja.Undefined()
	}
	if _, callErr := callback(goja.Undefined(), errArg, resultArg); callErr != nil {
		panic(callErr)
	}
	return true
}

func scriptCookieMapsFromValue(value goja.Value) ([]map[string]interface{}, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, errors.New("cookies array is required")
	}
	items, ok := value.Export().([]interface{})
	if !ok {
		return nil, errors.New("cookies array is required")
	}
	cookies := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		cookieMap, ok := item.(map[string]interface{})
		if !ok {
			return nil, errors.New("cookie object is required")
		}
		cookies = append(cookies, cookieMap)
	}
	return cookies, nil
}

func scriptCookieJarCookie(cookies []types.CookieEntry, name string) (types.CookieEntry, bool) {
	for index := len(cookies) - 1; index >= 0; index-- {
		if cookies[index].Name == name {
			return cookies[index], true
		}
	}
	return types.CookieEntry{}, false
}

func scriptCookieMapFromSetCookieArgs(nameOrCookie interface{}, values ...string) (map[string]interface{}, error) {
	if cookieMap, ok := nameOrCookie.(map[string]interface{}); ok {
		return cookieMap, nil
	}
	name := strings.TrimSpace(fmt.Sprint(nameOrCookie))
	if name == "" || name == "<nil>" {
		return nil, errors.New("cookie name is required")
	}
	if len(values) == 0 {
		return nil, errors.New("cookie value is required")
	}
	return map[string]interface{}{"name": name, "value": values[0]}, nil
}

func scriptCookieArray(runtime *goja.Runtime, cookies []types.CookieEntry) goja.Value {
	items := make([]interface{}, 0, len(cookies))
	for _, cookie := range cookies {
		items = append(items, scriptCookieValue(runtime, cookie))
	}
	return runtime.NewArray(items...)
}

func scriptCookieValue(runtime *goja.Runtime, cookie types.CookieEntry) goja.Value {
	object := runtime.NewObject()
	for key, value := range scriptCookieRow(cookie) {
		_ = object.Set(key, value)
	}
	return object
}

func scriptCookieArgumentMatches(cookie types.CookieEntry, value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	exported := value.Export()
	if name, ok := exported.(string); ok {
		return cookie.Name == name
	}
	data, ok := exported.(map[string]interface{})
	if !ok {
		return false
	}
	if name := scalar.FirstNonEmpty(scriptMapString(data, "name"), scriptMapString(data, "key")); name != "" && cookie.Name != name {
		return false
	}
	if rawValue, exists := data["value"]; exists && fmt.Sprint(rawValue) != cookie.Value {
		return false
	}
	if domain := cookiejar.NormalizeDomain(scriptMapString(data, "domain")); domain != "" && cookiejar.NormalizeDomain(cookie.Domain) != domain {
		return false
	}
	if path := scriptMapString(data, "path"); path != "" && cookie.Path != path {
		return false
	}
	return true
}

func scriptCookieFromMap(data map[string]interface{}, rawURL string) (types.CookieEntry, error) {
	if data == nil {
		return types.CookieEntry{}, errors.New("cookie object is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return types.CookieEntry{}, errors.New("current request URL is required for cookie writes")
	}
	name := scalar.FirstNonEmpty(scriptMapString(data, "name"), scriptMapString(data, "key"))
	domain := cookiejar.NormalizeDomain(scriptMapString(data, "domain"))
	explicitDomain := domain != ""
	hostOnly := scriptMapBool(data, "hostOnly")
	if domain == "" {
		domain = strings.ToLower(parsed.Hostname())
		hostOnly = strings.HasPrefix(name, "__Host-") || net.ParseIP(domain) != nil
	} else if explicitDomain {
		hostOnly = false
	}
	path := scriptMapString(data, "path")
	if path == "" {
		path = cookiejar.DefaultPath(parsed.EscapedPath())
	} else if !strings.HasPrefix(path, "/") {
		path = "/"
	}
	session := true
	expires := scriptMapString(data, "expires")
	if expires != "" {
		session = false
	}
	if rawSession, ok := data["session"]; ok {
		session = truthyInterface(rawSession)
	}
	cookie, err := cookiejar.NormalizeManual(types.CookieInput{
		Name:     name,
		Value:    scriptMapString(data, "value"),
		Domain:   domain,
		Path:     path,
		Expires:  expires,
		Session:  session,
		Secure:   scriptMapBool(data, "secure"),
		HTTPOnly: scriptMapBool(data, "httpOnly"),
		SameSite: scriptMapString(data, "sameSite"),
		HostOnly: hostOnly,
	})
	if err != nil {
		return types.CookieEntry{}, err
	}
	return cookie, nil
}

func CloneCookieEntries(cookies []types.CookieEntry) []types.CookieEntry {
	out := make([]types.CookieEntry, len(cookies))
	copy(out, cookies)
	return out
}

func scriptCookieRow(cookie types.CookieEntry) map[string]interface{} {
	row := map[string]interface{}{
		"key":      cookie.Name,
		"name":     cookie.Name,
		"value":    cookie.Value,
		"domain":   cookie.Domain,
		"path":     cookie.Path,
		"secure":   cookie.Secure,
		"httpOnly": cookie.HTTPOnly,
		"session":  cookie.Session,
		"sameSite": cookie.SameSite,
	}
	if !cookie.Expires.IsZero() {
		row["expires"] = cookie.Expires.Format(time.RFC3339)
	}
	return row
}

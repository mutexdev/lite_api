package scripting

// lodash: the real one, loaded only when a script asks for it.
//
// THIS FILE USED TO BE A HAND-WRITTEN SUBSET. Postman bundles the whole of
// lodash into its sandbox; LiteAPI shipped roughly eighty functions chosen by
// hand, and every function outside that set surfaced as
// "Object has no member 'x'" -- a message that names no script, no line, and
// never mentions lodash. The Restful Booker collection, Postman's own public
// teaching collection, died on _.random before sending a single request.
//
// Growing the subset only moves the wall. The subset IS the bug, so it is gone:
// internal/scripting/thirdparty/lodash/lodash.js is the unmodified npm release
// (4.17.21, verified byte-identical, MIT -- see the LICENSE beside it), and
// scripts get the complete library, _.debounce and _.template included.
//
// TWO THINGS MAKE THAT AFFORDABLE.
//
// It is compiled once per process, through the same ScriptShimProgram cache the
// other shims use, so the ~20ms parse is paid at most once no matter how many
// runtimes are built.
//
// It is EXECUTED only when a script touches it. Materialising lodash into a
// fresh goja runtime costs ~2.4ms, and NewScriptRuntimeWithMeta runs up to four
// times per request, so installing it eagerly would add ~10ms to every request
// including the majority that never mention it. Instead `_` is defined as an
// accessor that builds lodash on first read and then replaces itself with a
// plain data property. A runtime that never touches `_` pays ~9us -- which is
// less than the ~140us the old hand-written subset cost EAGERLY, every time. So
// this is faster than what it replaces for most requests, and complete for the
// rest.

import (
	_ "embed"
	"strings"

	"github.com/dop251/goja"
)

//go:embed thirdparty/lodash/lodash.js
var scriptLodashSource string

// scriptLodashProgramSource wraps the vendored file so it exports through
// CommonJS instead of assigning to the global object.
//
// lodash's UMD tail picks its export target by sniffing the scope it was
// evaluated in: given `module` and `exports` objects it does
// `(freeModule.exports = _)._ = _`, and only otherwise falls back to
// `root._ = _`. Supplying them keeps the global object clean, which matters
// here because the global `_` is an accessor -- letting lodash write to it
// would destroy the very property being installed.
func scriptLodashProgramSource() string {
	var builder strings.Builder
	builder.Grow(len(scriptLodashSource) + 128)
	builder.WriteString("(function () {\n  var module = { exports: {} }, exports = module.exports;\n")
	builder.WriteString(scriptLodashSource)
	builder.WriteString("\n  return module.exports;\n})()")
	return builder.String()
}

// scriptLodashState is the per-runtime lazy-loading state for lodash.
//
// The shadow fields exist because a script may legitimately use `_` as its own
// variable. That has to survive lodash loading afterwards, and the value cannot
// simply be read back off the global before loading: reading `_` is what
// triggers the getter, so probing it would defeat the laziness or recurse.
// Recording the assignment when it happens is the only way to know.
type scriptLodashState struct {
	runtime  *goja.Runtime
	cached   goja.Value
	shadow   goja.Value
	shadowed bool
}

// load evaluates lodash on the first call and returns the same object after.
//
// Deleting the global `_` BEFORE evaluating is load-bearing, not tidying.
// lodash captures `var oldDash = root._` during initialisation (lodash.js:1496,
// for _.noConflict). With the lazy accessor still in place that read re-enters
// this loader, which evaluates lodash, which reads root._ again -- an unbounded
// recursion that hangs the script runtime rather than failing. oldDash is left
// undefined, which only affects _.noConflict(), a browser-only API for undoing
// a global that nothing here sets.
func (state *scriptLodashState) load() goja.Value {
	if state.cached != nil {
		return state.cached
	}
	global := state.runtime.GlobalObject()
	_ = global.Delete("_")
	value, err := state.runtime.RunProgram(scriptLodashShim.compiled(scriptLodashProgramSource()))
	if err != nil {
		panic(state.runtime.NewGoError(err))
	}
	state.cached = value

	// Put a plain data property back. The delete above is what makes lodash
	// safe to evaluate, but it also removes the only definition of `_` -- so a
	// script whose first contact with lodash was require("lodash") would
	// otherwise find the global gone, destroyed by the act of loading.
	//
	// A script that assigned its own `_` first gets that value back instead:
	// asking for the library must not silently rewrite the script's variable.
	if state.shadowed {
		_ = global.Set("_", state.shadow)
	} else {
		_ = global.Set("_", value)
	}
	return value
}

// installScriptLodash defines the global `_` as a lazy accessor.
//
// Reading it loads lodash, which installs itself as a plain data property in
// place of this accessor, so a hot loop doing `_.map(...)` crosses into Go once
// rather than on every property read. The setter is there so a script that
// wants its own `_` still gets it -- without one, `_ = 5` in a non-strict
// script would silently do nothing.
func installScriptLodash(runtime *goja.Runtime, state *scriptLodashState) {
	getter := runtime.ToValue(func(goja.FunctionCall) goja.Value {
		return state.load()
	})
	setter := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		state.shadow = call.Argument(0)
		state.shadowed = true
		_ = runtime.GlobalObject().Delete("_")
		_ = runtime.GlobalObject().Set("_", state.shadow)
		return goja.Undefined()
	})
	// Configurable so the getter and setter above can replace it; non-enumerable
	// so `for (var k in globalThis)` does not drag lodash in by accident, which
	// would defeat the laziness this whole file exists for.
	if err := runtime.GlobalObject().DefineAccessorProperty("_", getter, setter, goja.FLAG_TRUE, goja.FLAG_FALSE); err != nil {
		panic(runtime.NewGoError(err))
	}
}

// scriptLodashModuleMember reports whether name is a lodash module request, and
// which member of lodash it refers to.
//
// "lodash" and "underscore" resolve to the library itself; "lodash/random" and
// "lodash/random.js" resolve to _.random, which is how the per-function modules
// are published and how scripts written against Node import them.
func scriptLodashModuleMember(name string) (string, bool) {
	switch name {
	case "lodash", "underscore":
		return "", true
	}
	for _, prefix := range []string{"lodash/", "underscore/"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		member := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".js")
		// Reject nested paths such as lodash/fp/map: that is a different build
		// with different semantics (auto-curried, iteratee-first), and resolving
		// it to the plain function would be quietly wrong rather than missing.
		if member == "" || strings.Contains(member, "/") {
			return "", false
		}
		return member, true
	}
	return "", false
}

// scriptLodashModule resolves a lodash module request against the loaded
// library, or returns nil when the name is not a lodash module or names a
// member that does not exist.
func scriptLodashModule(runtime *goja.Runtime, state *scriptLodashState, name string) goja.Value {
	member, ok := scriptLodashModuleMember(name)
	if !ok {
		return nil
	}
	lodash := state.load()
	if member == "" {
		return lodash
	}
	object := lodash.ToObject(runtime)
	if object == nil {
		return nil
	}
	value := object.Get(member)
	if value == nil || goja.IsUndefined(value) {
		return nil
	}
	return value
}

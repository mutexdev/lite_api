// The vendored lodash, and the two properties that make vendoring it viable.
//
// COMPLETENESS is the point of the change: the hand-written subset is gone, so
// anything lodash publishes has to be there, including the functions nobody
// would have hand-written (_.template compiles source, _.debounce needs timers,
// _.flow composes). Those are the ones a collection reaches for precisely
// because they are hard to reimplement.
//
// LAZINESS is what pays for it. Materialising lodash costs ~2.4ms per goja
// runtime and NewScriptRuntimeWithMeta runs up to four times per request, so if
// laziness silently broke, every request would get ~10ms slower and every test
// here would still pass. That failure is invisible by construction, which is
// why it is asserted directly rather than left to a benchmark nobody reads.
package scripting

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/mutexdev/lite_api/internal/types"
)

func lodashItem() *types.RequestItem {
	return &types.RequestItem{Name: "lodash", URL: "http://example.test", Method: "GET"}
}

// A script that never mentions lodash must not pay for it. Asserted by counting
// evaluations of the vendored program rather than by timing, so it cannot flake
// on a loaded machine.
func TestLodashIsNotEvaluatedUntilAScriptTouchesIt(t *testing.T) {
	runtime := goja.New()
	state := &scriptLodashState{runtime: runtime}
	installScriptLodash(runtime, state)
	loaded := func() bool { return state.cached != nil }

	if _, err := runtime.RunString(`var total = 1 + 1; var underscored_name = "x";`); err != nil {
		t.Fatalf("plain script failed: %v", err)
	}
	if loaded() {
		t.Fatal("lodash was built for a script that never used it")
	}

	// An identifier that merely CONTAINS an underscore must not count as a use;
	// this is the case a source-scanning implementation would get wrong.
	if value := runtime.Get("underscored_name"); value == nil || value.String() != "x" {
		t.Fatalf("unexpected script state: %v", value)
	}

	if _, err := runtime.RunString(`_.random(1);`); err != nil {
		t.Fatalf("lodash script failed: %v", err)
	}
	if !loaded() {
		t.Fatal("using _ did not build lodash")
	}
}

// Repeated use must not rebuild it, and the accessor must give way to a plain
// data property so property reads stop crossing into Go.
func TestLodashIsBuiltOnceAndBecomesADataProperty(t *testing.T) {
	runtime := goja.New()
	state := &scriptLodashState{runtime: runtime}
	installScriptLodash(runtime, state)

	if _, err := runtime.RunString(`
		var n = 0;
		for (var i = 0; i < 200; i += 1) { n += _.map([1, 2], function (x) { return x; }).length; }
		if (n !== 400) throw new Error("wrong total " + n);
		if (_ !== _) throw new Error("identity is unstable");
	`); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if state.cached == nil {
		t.Fatal("lodash was never built")
	}

	descriptor, err := runtime.RunString(`Object.getOwnPropertyDescriptor(globalThis, "_")`)
	if err != nil {
		t.Fatal(err)
	}
	object := descriptor.ToObject(runtime)
	if get := object.Get("get"); get != nil && !goja.IsUndefined(get) {
		t.Fatal("_ is still an accessor after first use, so every property read re-enters Go")
	}
}

// The functions the hand-written subset never had, and never realistically
// would: these are why the subset was abandoned rather than extended.
func TestLodashProvidesTheFullLibrary(t *testing.T) {
	script := `
if (_.VERSION !== "4.17.21") throw new Error("unexpected lodash version: " + _.VERSION);

// Compiles and runs a template - a source-generating function.
if (_.template("hi <%= name %>")({ name: "there" }) !== "hi there") throw new Error("_.template");

// Function composition and currying.
if (_.flow([function (n) { return n + 1; }, function (n) { return n * 2; }])(3) !== 8) throw new Error("_.flow");
if (_.partial(function (a, b) { return a + b; }, 1)(2) !== 3) throw new Error("_.partial");

// Timer-backed helpers must at least exist and be callable.
if (typeof _.debounce !== "function") throw new Error("_.debounce missing");
if (typeof _.throttle !== "function") throw new Error("_.throttle missing");
if (typeof _.memoize(function (n) { return n; })(1) !== "number") throw new Error("_.memoize");

// Deep merge with a customiser, and the path-based helpers in full.
var merged = _.mergeWith({ a: [1] }, { a: [2] }, function (left, right) {
  if (Array.isArray(left)) return left.concat(right);
});
if (merged.a.join() !== "1,2") throw new Error("_.mergeWith: " + merged.a.join());
if (_.get({ a: { b: [{ c: 7 }] } }, "a.b[0].c") !== 7) throw new Error("_.get deep path");

// A sample of the everyday surface, so a broken build fails loudly here.
if (_.chunk([1, 2, 3, 4], 2).length !== 2) throw new Error("_.chunk");
if (_.camelCase("--foo-bar--") !== "fooBar") throw new Error("_.camelCase");
if (_.sortBy([3, 1, 2]).join() !== "1,2,3") throw new Error("_.sortBy");
if (_.random(5) > 5) throw new Error("_.random range");
`
	if err := RunPreRequestScript(script, lodashItem(), map[string]string{}, nil); err != nil {
		t.Fatalf("full lodash is not available: %v", err)
	}
}

// require() must resolve lodash without the eager module map holding it, since
// putting it there is exactly what would defeat the laziness.
func TestLodashResolvesThroughRequireIncludingSubpaths(t *testing.T) {
	script := `
var lodash = require("lodash");
if (require("underscore") !== lodash) throw new Error("underscore is a different object");
if (globalThis._ !== lodash) throw new Error("requiring lodash left the global _ unset");

if (require("lodash/random") !== lodash.random) throw new Error("lodash/random");
if (require("lodash/cloneDeep.js") !== lodash.cloneDeep) throw new Error("lodash/cloneDeep.js");
if (require("lodash/template") !== lodash.template) throw new Error("lodash/template");

// A member that does not exist must report as a missing module rather than
// returning undefined and failing later at the call site.
var threw = false;
try { require("lodash/notARealFunction"); } catch (error) { threw = true; }
if (!threw) throw new Error("an unknown lodash member did not throw");
`
	if err := RunPreRequestScript(script, lodashItem(), map[string]string{}, nil); err != nil {
		t.Fatalf("require() does not resolve lodash: %v", err)
	}
}

// lodash/fp is a DIFFERENT build - auto-curried and iteratee-first - so
// resolving it to the plain function would be quietly wrong. Missing is the
// honest answer.
func TestLodashFpSubpathIsRefusedRatherThanMisresolved(t *testing.T) {
	if member, ok := scriptLodashModuleMember("lodash/fp/map"); ok {
		t.Fatalf("lodash/fp/map resolved to %q instead of being refused", member)
	}
	for _, name := range []string{"lodash", "underscore"} {
		member, ok := scriptLodashModuleMember(name)
		if !ok || member != "" {
			t.Fatalf("%q resolved to (%q, %v), want the library itself", name, member, ok)
		}
	}
	for _, name := range []string{"lodash/map", "lodash/map.js", "underscore/map"} {
		member, ok := scriptLodashModuleMember(name)
		if !ok || member != "map" {
			t.Fatalf("%q resolved to (%q, %v), want map", name, member, ok)
		}
	}
	for _, name := range []string{"axios", "path", "lodash/", "notlodash/map"} {
		if _, ok := scriptLodashModuleMember(name); ok {
			t.Fatalf("%q was treated as a lodash module", name)
		}
	}
}

// A script may use `_` as its own variable. The library must not fight it.
func TestAScriptMayShadowTheLodashGlobal(t *testing.T) {
	script := `
_ = 5;
if (_ !== 5) throw new Error("the script's own _ was overwritten: " + _);
// Requiring lodash afterwards must not clobber the script's value.
var lodash = require("lodash");
if (typeof lodash.map !== "function") throw new Error("lodash did not load");
if (_ !== 5) throw new Error("require(lodash) overwrote the script's _: " + _);
`
	if err := RunPreRequestScript(script, lodashItem(), map[string]string{}, nil); err != nil {
		t.Fatalf("a script could not use its own _: %v", err)
	}
}

// The vendored file must stay the untouched npm release. A local "small fix" to
// third-party source is how a dependency silently forks.
func TestVendoredLodashIsTheUnmodifiedRelease(t *testing.T) {
	if !strings.Contains(scriptLodashSource, "4.17.21") {
		t.Fatal("the vendored lodash does not declare version 4.17.21")
	}
	// The UMD tail this integration depends on: given module/exports, lodash
	// exports through CommonJS instead of writing to the global object.
	if !strings.Contains(scriptLodashSource, "freeModule") {
		t.Fatal("the vendored lodash lost its CommonJS export branch")
	}
	// The read that forces the delete-before-load in newScriptLodashLoader.
	if !strings.Contains(scriptLodashSource, "var oldDash = root._;") {
		t.Fatal("lodash no longer reads root._ during init; revisit newScriptLodashLoader's delete")
	}
}

// The two halves of the trade, side by side.
//
// Unused is the common case and the one that got FASTER: the hand-written
// subset was installed eagerly into every runtime at ~140us, and this pays
// roughly nothing instead. Used is the case that got slower, and the number is
// here so the next person weighing "just install it eagerly" can see what that
// would cost four times per request.
func BenchmarkScriptRuntimeLodashUnused(b *testing.B) {
	item, vars := benchItem(), benchVars()
	response := types.Response{Status: 200}
	jar := NewScriptCookieJar(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime, _, _, _ := NewScriptRuntimeWithMeta(item, response, vars, nil, nil, jar, ScriptRuntimeMeta{})
		if _, err := runtime.RunString(`var n = 1 + 1;`); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScriptRuntimeLodashUsed(b *testing.B) {
	item, vars := benchItem(), benchVars()
	response := types.Response{Status: 200}
	jar := NewScriptCookieJar(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime, _, _, _ := NewScriptRuntimeWithMeta(item, response, vars, nil, nil, jar, ScriptRuntimeMeta{})
		if _, err := runtime.RunString(`_.random(1);`); err != nil {
			b.Fatal(err)
		}
	}
}

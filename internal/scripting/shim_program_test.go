package scripting

import (
	"LiteAPI/internal/types"
	"strings"
	"sync"
	"testing"

	"github.com/dop251/goja"
)

// allScriptShimPrograms is every compiled-once cache slot for a built-in shim
// source. Keep it in sync with the var block beside ScriptShimProgram in app.go.
func allScriptShimPrograms() map[string]*ScriptShimProgram {
	return map[string]*ScriptShimProgram{
		"console":         scriptConsoleModuleShim,
		"buffer":          scriptBufferShim,
		"timers/promises": scriptTimersPromisesShim,
		"assert":          scriptAssertShim,
		"util":            scriptUtilShim,
		"ajv":             scriptAjvShim,
		"axios":           scriptAxiosShim,
		"lodash":          scriptLodashShim,
		"querystring":     scriptQueryStringShim,
		"zlib":            scriptZlibShim,
		"dns":             scriptDNSShim,
		"http":            scriptHTTPShim,
		"events":          scriptEventsShim,
		"stream":          scriptStreamShim,
		"stream/promises": scriptStreamPromisesShim,
		"url":             scriptURLShim,
		"moment":          scriptMomentShim,
		"crypto-js":       scriptCryptoJSShim,
		"EventTarget":     scriptEventTargetShim,
		"encoding":        scriptEncodingShim,
		"fetch":           scriptFetchShim,
	}
}

// TestScriptShimProgramCompilesOnce is the whole point of the cache: one
// goja.Compile per source for the life of the process, no matter how many
// runtimes ask for it or from how many goroutines.
func TestScriptShimProgramCompilesOnce(t *testing.T) {
	shim := newScriptShimProgram("test")
	const src = `(function () { return 1; })()`

	first := shim.compiled(src)
	if first == nil {
		t.Fatal("compiled returned a nil program")
	}

	var wg sync.WaitGroup
	results := make([]*goja.Program, 32)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = shim.compiled(src)
		}(i)
	}
	wg.Wait()

	for index, got := range results {
		if got != first {
			t.Fatalf("call %d returned a different *goja.Program; the source was recompiled", index)
		}
	}
}

// TestScriptShimProgramRunsInEveryRuntime covers the sharing claim goja.Compile
// documents: one *goja.Program, many runtimes, no cross-contamination of the
// mutable state each run creates.
func TestScriptShimProgramRunsInEveryRuntime(t *testing.T) {
	shim := newScriptShimProgram("counter")
	const src = `(function () { globalThis.__hits = (globalThis.__hits || 0) + 1; return globalThis.__hits; })()`

	for attempt := 0; attempt < 3; attempt++ {
		runtime := goja.New()
		value, err := runtime.RunProgram(shim.compiled(src))
		if err != nil {
			t.Fatalf("attempt %d: RunProgram failed: %v", attempt, err)
		}
		if got := value.ToInteger(); got != 1 {
			t.Fatalf("attempt %d: got %d, want 1 — state leaked between runtimes", attempt, got)
		}
	}
}

// TestScriptShimProgramMatchesRunStringStrictness pins the compile flags to the
// ones RunString uses. goja's Runtime.RunString(src) is RunScript("", src),
// which compiles with strict=false; compiling the shims with strict=true would
// silently change the semantics of every one of them. This asserts the
// difference is real and that ScriptShimProgram lands on the sloppy side.
func TestScriptShimProgramMatchesRunStringStrictness(t *testing.T) {
	// Legal in sloppy mode, a SyntaxError in strict mode.
	const src = `(function () { return 0777; })()`

	if _, err := goja.Compile("", src, true); err == nil {
		t.Fatal("expected strict-mode compilation to reject an octal literal; the probe no longer distinguishes the modes")
	}

	runtime := goja.New()
	want, err := runtime.RunString(src)
	if err != nil {
		t.Fatalf("RunString rejected sloppy-mode source: %v", err)
	}

	shim := newScriptShimProgram("strictness")
	got, err := goja.New().RunProgram(shim.compiled(src))
	if err != nil {
		t.Fatalf("cached program rejected sloppy-mode source: %v", err)
	}
	if got.Export() != want.Export() {
		t.Fatalf("cached program produced %v, RunString produced %v", got.Export(), want.Export())
	}
}

// TestScriptShimProgramCompileFailurePanics documents the deliberate choice to
// panic rather than return an error: the sources are compile-time constants, so
// a failure is a programmer error with no caller-recoverable outcome.
func TestScriptShimProgramCompileFailurePanics(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected a panic for an uncompilable shim source")
		}
		err, ok := recovered.(error)
		if !ok {
			t.Fatalf("expected the panic value to be an error, got %T", recovered)
		}
		if !strings.Contains(err.Error(), `"broken"`) {
			t.Fatalf("panic message %q does not identify the failing shim", err.Error())
		}
	}()

	newScriptShimProgram("broken").compiled(`(function () { return ; ;;) }`)
}

// TestScriptShimProgramsAreAllReached guards the eagerness assumption the cache
// is sized against: building one developer-mode runtime touches every cached
// shim. If a future change makes one lazy, the cache still works but this test
// records the change instead of letting it pass unnoticed.
func TestScriptShimProgramsAreAllReached(t *testing.T) {
	item := types.RequestItem{ID: "shim", Name: "shim", Type: "http", Method: "GET", URL: "https://fixture.example.test/"}
	var testResults []types.TestResult
	var scriptLogs []types.ScriptLog
	runtime, _, _, _ := NewScriptRuntimeWithMeta(
		item,
		types.Response{Status: 200, StatusText: "OK"},
		map[string]string{},
		&testResults,
		&scriptLogs,
		nil,
		ScriptRuntimeMeta{JSSandboxMode: "developer"},
	)
	if runtime == nil {
		t.Fatal("NewScriptRuntimeWithMeta returned a nil runtime")
	}

	for name, shim := range allScriptShimPrograms() {
		if shim.prog == nil {
			t.Errorf("shim %q was never compiled: a developer-mode runtime no longer installs it", name)
		}
	}
}

// TestScriptShimProgramSafeModeStillGatesDeveloperShims proves caching did not
// widen what a safe-mode sandbox exposes. The developer-only modules must still
// be unavailable, and process must still be absent.
func TestScriptShimProgramSafeModeStillGatesDeveloperShims(t *testing.T) {
	item := types.RequestItem{ID: "shim", Name: "shim", Type: "http", Method: "GET", URL: "https://fixture.example.test/"}
	var testResults []types.TestResult
	var scriptLogs []types.ScriptLog
	runtime, _, _, _ := NewScriptRuntimeWithMeta(
		item,
		types.Response{Status: 200, StatusText: "OK"},
		map[string]string{},
		&testResults,
		&scriptLogs,
		nil,
		ScriptRuntimeMeta{JSSandboxMode: "safe"},
	)
	if runtime == nil {
		t.Fatal("NewScriptRuntimeWithMeta returned a nil runtime")
	}

	if process := runtime.Get("process"); process != nil && !goja.IsUndefined(process) {
		t.Error("safe mode exposed process")
	}

	for _, name := range []string{"fs", "http", "https", "dns", "assert", "console", "timers/promises"} {
		value, err := runtime.RunString(`(function () {
  try { require(` + "`" + name + "`" + `); return "resolved"; } catch (e) { return "blocked"; }
})()`)
		if err != nil {
			t.Fatalf("probe for %q failed: %v", name, err)
		}
		if value.String() != "blocked" {
			t.Errorf("safe mode resolved developer-only module %q", name)
		}
	}
}

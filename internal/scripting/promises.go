package scripting

// Promise and async-test plumbing over goja.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

// scriptSafeTimerHolder is where the runtime parks safe mode's scheduling
// primitives for the wrapper to pick up. It exists for exactly one statement and
// is deleted before the user's first line runs, so a script never sees it.
const scriptSafeTimerHolder = "__bruTimers"

// scriptAsyncWrapper puts the user's script inside an async IIFE so top-level
// await works.
//
// Exactly TWO lines above the user's first in both sandbox modes. That is not
// cosmetic: scriptWrapperLineOffset subtracts it to turn the runtime's line
// number back into a line of the script the user typed, and a wrapper that was
// three lines in safe mode and two in developer mode reported a different line
// for the same error depending on a setting.
//
// Safe mode's prologue therefore does its whole job on one line: destructure the
// scheduling primitives into lexical bindings, then remove the holder, so the
// script has setTimeout/clearTimeout/setInterval/... by name while globalThis
// stays as clean as it was before.
func scriptAsyncWrapper(script, sandboxMode string) string {
	if NormalizeJSSandboxMode(sandboxMode) == "developer" {
		return "(async () => {\nawait bru.sleep(0);\n" + script + "\n})()"
	}
	return "(async () => {\n" +
		"const { setTimeout, clearTimeout, setInterval, clearInterval, setImmediate, clearImmediate, queueMicrotask } = globalThis." + scriptSafeTimerHolder + " || {};" +
		" try { delete globalThis." + scriptSafeTimerHolder + "; } catch (_) {} await bru.sleep(0);\n" +
		script + "\n})()"
}

func scriptPromiseFromValue(value goja.Value) (*goja.Promise, bool) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, false
	}
	promise, ok := value.Export().(*goja.Promise)
	return promise, ok
}

func scriptPromisePendingOrError(runtime *goja.Runtime, value goja.Value) (bool, error) {
	promise, ok := scriptPromiseFromValue(value)
	if !ok {
		return false, nil
	}
	switch promise.State() {
	case goja.PromiseStateRejected:
		return false, scriptPromiseRejectionError(runtime, promise.Result())
	case goja.PromiseStatePending:
		return true, nil
	default:
		return false, nil
	}
}

// scriptDrainRuntime takes deadline as a FUNCTION, not a value: a script-issued
// HTTP call pushes the deadline out while this loop is running, and a captured
// time.Time would still be enforcing the budget the call was allowed to spend.
func scriptDrainRuntime(runtime *goja.Runtime, value goja.Value, deadline func() time.Time) error {
	loop := scriptEventLoopForRuntime(runtime)
	for {
		pendingPromise, err := scriptPromisePendingOrError(runtime, value)
		if err != nil {
			return err
		}
		pendingTests := loop != nil && loop.pendingTests > 0
		if !pendingPromise && !pendingTests && loop != nil {
			// A setInterval keeps the timer map non-empty for ever. Once nothing
			// the script actually awaited is outstanding, drop the repeating
			// timers instead of spinning to the deadline and reporting a
			// "script timeout" the script did not cause — the interval must not
			// outlive the script either way.
			loop.clearRepeatingTimers()
		}
		pendingTimers := loop != nil && len(loop.timers) > 0
		if !pendingPromise && !pendingTests && !pendingTimers {
			return nil
		}
		if loop == nil || len(loop.timers) == 0 {
			if pendingPromise {
				return errors.New("script promise did not settle")
			}
			return errors.New("script async tests did not settle")
		}
		if time.Now().After(deadline()) {
			return errors.New("script timeout")
		}
		if err := loop.runNextTimer(deadline()); err != nil {
			return err
		}
	}
}

func scriptPromiseRejectionMessage(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "promise rejected"
	}
	object := value.ToObject(runtime)
	if message := object.Get("message"); message != nil && !goja.IsUndefined(message) && !goja.IsNull(message) {
		return message.String()
	}
	return value.String()
}

// scriptPromiseRejectionError is scriptPromiseRejectionMessage plus the two
// things a user needs and a bare .message does not carry.
//
// The script runs inside an async IIFE, so anything it throws arrives here as a
// rejection rather than as a goja Exception — and reading .message off it threw
// away both the error TYPE and the position. "missingFunction is not defined",
// with no "ReferenceError" and no line, was all the user got for a typo.
//
// The generic name "Error" is deliberately not prefixed: it says nothing the
// message does not, and `throw new Error("token missing")` reads better without
// it.
func scriptPromiseRejectionError(runtime *goja.Runtime, value goja.Value) error {
	message := scriptPromiseRejectionMessage(runtime, value)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return errors.New(message)
	}
	object := value.ToObject(runtime)
	if object == nil {
		return errors.New(message)
	}
	if name := object.Get("name"); name != nil && !goja.IsUndefined(name) && !goja.IsNull(name) {
		text := strings.TrimSpace(name.String())
		if text != "" && text != "Error" && !strings.HasPrefix(message, text+":") {
			message = text + ": " + message
		}
	}
	if stack := object.Get("stack"); stack != nil && !goja.IsUndefined(stack) && !goja.IsNull(stack) {
		if frame := scriptStackEvalFrameExpr.FindString(stack.String()); frame != "" {
			message += " at " + frame
		}
	}
	return errors.New(message)
}

// The first "<eval>:line:column(n)" in a goja stack — the throw site.
var scriptStackEvalFrameExpr = regexp.MustCompile(`<eval>:\d+:\d+(?:\([-\d]+\))?`)

func scriptResolvedPromise(runtime *goja.Runtime, value goja.Value) goja.Value {
	if value == nil {
		value = goja.Undefined()
	}
	promise, resolve, _ := runtime.NewPromise()
	if err := resolve(value); err != nil {
		panic(err)
	}
	return runtime.ToValue(promise)
}

func scriptRejectedPromise(runtime *goja.Runtime, reason interface{}) goja.Value {
	promise, _, reject := runtime.NewPromise()
	if err := reject(reason); err != nil {
		panic(err)
	}
	return runtime.ToValue(promise)
}

func scriptAttachResolvedThenable(runtime *goja.Runtime, object *goja.Object) {
	if object == nil {
		return
	}
	plainValue := func() goja.Value {
		clone := runtime.NewObject()
		for _, key := range object.Keys() {
			if key == "then" || key == "catch" || key == "finally" {
				continue
			}
			_ = clone.Set(key, object.Get(key))
		}
		return clone
	}
	then := func(call goja.FunctionCall) goja.Value {
		value := plainValue()
		onFulfilled, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return scriptResolvedPromise(runtime, value)
		}
		result, err := onFulfilled(goja.Undefined(), value)
		if err != nil {
			return scriptRejectedPromise(runtime, err)
		}
		return scriptResolvedPromise(runtime, result)
	}
	catchFn := func(goja.FunctionCall) goja.Value {
		return scriptResolvedPromise(runtime, plainValue())
	}
	finallyFn := func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(0)); ok {
			if _, err := callback(goja.Undefined()); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
		}
		return scriptResolvedPromise(runtime, plainValue())
	}
	_ = object.DefineDataProperty("then", runtime.ToValue(then), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	_ = object.DefineDataProperty("catch", runtime.ToValue(catchFn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	_ = object.DefineDataProperty("finally", runtime.ToValue(finallyFn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

func scriptAttachAsyncTestResult(runtime *goja.Runtime, value goja.Value, testResults *[]types.TestResult, name string) bool {
	promise, ok := scriptPromiseFromValue(value)
	if !ok {
		return false
	}
	// The pending-test registration happens even with no registry to record
	// into. It is what keeps scriptDrainRuntime waiting for the promise; the
	// early return that used to sit here meant an async pm.test in the
	// pre-request or post-response slot was abandoned mid-flight, taking any
	// side effect after its await with it.
	loop := scriptEventLoopForRuntime(runtime)
	if loop != nil {
		loop.addPendingTest()
	}
	resultIndex := -1
	if testResults != nil {
		resultIndex = len(*testResults)
		*testResults = append(*testResults, types.TestResult{Name: name, Passed: false, Message: "pending"})
	}
	setResult := func(passed bool, message string) {
		if testResults != nil && resultIndex >= 0 && resultIndex < len(*testResults) {
			(*testResults)[resultIndex] = types.TestResult{Name: name, Passed: passed, Message: message}
		}
	}
	complete := func(passed bool, message string) {
		setResult(passed, message)
		if loop != nil {
			loop.finishPendingTest()
		}
	}
	promiseObject := runtime.ToValue(promise).ToObject(runtime)
	then, ok := goja.AssertFunction(promiseObject.Get("then"))
	if !ok {
		complete(false, "test returned a non-callable promise")
		return true
	}
	onFulfilled := func(goja.FunctionCall) goja.Value {
		complete(true, "passed")
		return goja.Undefined()
	}
	onRejected := func(call goja.FunctionCall) goja.Value {
		complete(false, scriptPromiseRejectionMessage(runtime, call.Argument(0)))
		return goja.Undefined()
	}
	if _, err := then(runtime.ToValue(promise), runtime.ToValue(onFulfilled), runtime.ToValue(onRejected)); err != nil {
		complete(false, err.Error())
	}
	return true
}

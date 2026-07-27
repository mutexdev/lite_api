package scripting

// Promise and async-test plumbing over goja.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"time"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

func scriptAsyncWrapper(script, sandboxMode string) string {
	if NormalizeJSSandboxMode(sandboxMode) == "developer" {
		return "(async () => {\nawait bru.sleep(0);\n" + script + "\n})()"
	}
	return "(async () => {\nconst setTimeout = globalThis.__bruSetTimeout;\ntry { delete globalThis.__bruSetTimeout; } catch (_) {}\nawait bru.sleep(0);\n" + script + "\n})()"
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
		return false, errors.New(scriptPromiseRejectionMessage(runtime, promise.Result()))
	case goja.PromiseStatePending:
		return true, nil
	default:
		return false, nil
	}
}

func scriptDrainRuntime(runtime *goja.Runtime, value goja.Value, deadline time.Time) error {
	loop := scriptEventLoopForRuntime(runtime)
	for {
		pendingPromise, err := scriptPromisePendingOrError(runtime, value)
		if err != nil {
			return err
		}
		pendingTests := loop != nil && loop.pendingTests > 0
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
		if time.Now().After(deadline) {
			return errors.New("script timeout")
		}
		if err := loop.runNextTimer(deadline); err != nil {
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
	if testResults == nil {
		return true
	}
	loop := scriptEventLoopForRuntime(runtime)
	if loop != nil {
		loop.addPendingTest()
	}
	resultIndex := len(*testResults)
	*testResults = append(*testResults, types.TestResult{Name: name, Passed: false, Message: "pending"})
	setResult := func(passed bool, message string) {
		if resultIndex >= 0 && resultIndex < len(*testResults) {
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

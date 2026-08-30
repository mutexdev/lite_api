package scripting

// The sandbox timer shim and its event loop.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"time"

	"github.com/dop251/goja"
)

type scriptTimer struct {
	id       int64
	callback goja.Callable
	args     []goja.Value
	due      time.Time
	delay    time.Duration
	repeat   bool
	resolve  func(reason interface{}) error
	reject   func(reason interface{}) error
	promise  *goja.Promise
}

func newScriptTimersObject(runtime *goja.Runtime, promisesObject goja.Value) goja.Value {
	object := runtime.NewObject()
	for _, name := range []string{"setTimeout", "clearTimeout", "setInterval", "clearInterval", "setImmediate", "clearImmediate"} {
		_ = object.Set(name, runtime.Get(name))
	}
	_ = object.Set("promises", promisesObject)
	_ = object.Set("default", object)
	return object
}

func newScriptTimersPromisesObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const nativeSetTimeout = globalThis.setTimeout;
  const nativeClearTimeout = globalThis.clearTimeout;
  const nativeSetImmediate = globalThis.setImmediate;
  const nativeClearImmediate = globalThis.clearImmediate;

  function abortError(signal) {
    if (signal && signal.reason !== undefined) {
      return signal.reason;
    }
    const err = new Error("The operation was aborted");
    err.name = "AbortError";
    err.code = "ABORT_ERR";
    return err;
  }

  function addAbortListener(signal, onAbort) {
    if (!signal || typeof signal.addEventListener !== "function") {
      return function () {};
    }
    signal.addEventListener("abort", onAbort, { once: true });
    return function () {
      if (typeof signal.removeEventListener === "function") {
        signal.removeEventListener("abort", onAbort);
      }
    };
  }

  function applyRef(handle, options) {
    if (options && options.ref === false && handle && typeof handle.unref === "function") {
      handle.unref();
    }
  }

  function timeoutPromise(delay, value, options) {
    options = options || {};
    return new Promise(function (resolve, reject) {
      const signal = options.signal;
      if (signal && signal.aborted) {
        reject(abortError(signal));
        return;
      }
      let handle;
      let removeAbort = function () {};
      let settled = false;
      function settle(fn, result) {
        if (settled) return;
        settled = true;
        removeAbort();
        fn(result);
      }
      function onAbort() {
        if (handle !== undefined) {
          nativeClearTimeout(handle);
        }
        settle(reject, abortError(signal));
      }
      removeAbort = addAbortListener(signal, onAbort);
      handle = nativeSetTimeout(function () {
        settle(resolve, value);
      }, delay);
      applyRef(handle, options);
    });
  }

  function immediatePromise(value, options) {
    options = options || {};
    return new Promise(function (resolve, reject) {
      const signal = options.signal;
      if (signal && signal.aborted) {
        reject(abortError(signal));
        return;
      }
      let handle;
      let removeAbort = function () {};
      let settled = false;
      function settle(fn, result) {
        if (settled) return;
        settled = true;
        removeAbort();
        fn(result);
      }
      function onAbort() {
        if (handle !== undefined) {
          nativeClearImmediate(handle);
        }
        settle(reject, abortError(signal));
      }
      removeAbort = addAbortListener(signal, onAbort);
      handle = nativeSetImmediate(function () {
        settle(resolve, value);
      });
      applyRef(handle, options);
    });
  }

  function intervalIterator(delay, value, options) {
    options = options || {};
    const signal = options.signal;
    let done = false;
    function finish() {
      done = true;
      return Promise.resolve({ value: undefined, done: true });
    }
    const iterator = {
      next: function () {
        if (done) {
          return Promise.resolve({ value: undefined, done: true });
        }
        if (signal && signal.aborted) {
          done = true;
          return Promise.reject(abortError(signal));
        }
        return timeoutPromise(delay, value, options).then(function (nextValue) {
          if (done) {
            return { value: undefined, done: true };
          }
          return { value: nextValue, done: false };
        });
      },
      return: finish,
      throw: function (err) {
        done = true;
        return Promise.reject(err);
      }
    };
    if (typeof Symbol !== "undefined" && Symbol.asyncIterator) {
      iterator[Symbol.asyncIterator] = function () {
        return iterator;
      };
    }
    return iterator;
  }

  const scheduler = {
    wait: function (delay, options) {
      return timeoutPromise(delay, undefined, options);
    },
    yield: function () {
      return immediatePromise(undefined);
    }
  };
  const timersPromises = {
    setTimeout: timeoutPromise,
    setImmediate: immediatePromise,
    setInterval: intervalIterator,
    scheduler: scheduler
  };
  timersPromises.default = timersPromises;
  return timersPromises;
})()`
	value, err := runtime.RunProgram(scriptTimersPromisesShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func (loop *scriptEventLoop) scheduleTimer(callbackValue, delayValue goja.Value, repeat bool, args ...goja.Value) goja.Value {
	promise, resolve, reject := loop.runtime.NewPromise()
	callback, ok := goja.AssertFunction(callbackValue)
	if !ok {
		if err := reject(loop.runtime.NewTypeError("setTimeout callback must be a function")); err != nil {
			panic(err)
		}
		return loop.runtime.ToValue(promise)
	}
	loop.nextID++
	id := loop.nextID
	delay := scriptTimerDelay(delayValue)
	loop.timers[id] = &scriptTimer{
		id:       id,
		callback: callback,
		args:     append([]goja.Value(nil), args...),
		due:      time.Now().Add(delay),
		delay:    delay,
		repeat:   repeat,
		resolve:  resolve,
		reject:   reject,
		promise:  promise,
	}
	return loop.timerHandle(id, promise)
}

func (loop *scriptEventLoop) clearRepeatingTimers() {
	for id, timer := range loop.timers {
		if timer.repeat {
			delete(loop.timers, id)
		}
	}
}

func (loop *scriptEventLoop) clearTimer(value goja.Value) {
	id := scriptTimerID(loop.runtime, value)
	if id == 0 {
		return
	}
	delete(loop.timers, id)
}

func scriptTimerID(runtime *goja.Runtime, value goja.Value) int64 {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	if object := value.ToObject(runtime); object != nil {
		idValue := object.Get("__bruTimerID")
		if idValue != nil && !goja.IsUndefined(idValue) && !goja.IsNull(idValue) {
			return idValue.ToInteger()
		}
	}
	return value.ToInteger()
}

func scriptTimerDelay(value goja.Value) time.Duration {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	ms := value.ToInteger()
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (loop *scriptEventLoop) nextTimer() *scriptTimer {
	var next *scriptTimer
	for _, timer := range loop.timers {
		if next == nil || timer.due.Before(next.due) || (timer.due.Equal(next.due) && timer.id < next.id) {
			next = timer
		}
	}
	return next
}

func (loop *scriptEventLoop) runNextTimer(deadline time.Time) error {
	timer := loop.nextTimer()
	if timer == nil {
		return nil
	}
	if wait := time.Until(timer.due); wait > 0 {
		if remaining := time.Until(deadline); remaining <= 0 {
			return errors.New("script timeout")
		} else if wait > remaining {
			wait = remaining
		}
		time.Sleep(wait)
	}
	if time.Now().After(deadline) {
		return errors.New("script timeout")
	}
	current, ok := loop.timers[timer.id]
	if !ok {
		return nil
	}
	if !current.repeat {
		delete(loop.timers, current.id)
	}
	if _, err := current.callback(goja.Undefined(), current.args...); err != nil {
		delete(loop.timers, current.id)
		if current.reject != nil {
			if rejectErr := current.reject(err); rejectErr != nil {
				return rejectErr
			}
		}
		return err
	}
	if current.resolve != nil {
		if resolveErr := current.resolve(goja.Undefined()); resolveErr != nil {
			return resolveErr
		}
	}
	if current.repeat {
		if _, ok := loop.timers[current.id]; ok {
			current.due = time.Now().Add(current.delay)
		}
	}
	return nil
}

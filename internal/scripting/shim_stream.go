package scripting

// The sandbox node:stream and node:events shims.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"github.com/dop251/goja"
)

func newScriptEventsObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const errorMonitor = typeof Symbol === "function" ? Symbol("events.errorMonitor") : "__bruErrorMonitor";
  const captureRejectionSymbol = typeof Symbol === "function" && Symbol.for ? Symbol.for("nodejs.rejection") : "__bruCaptureRejection";

  function validateListener(listener) {
    if (typeof listener !== "function") {
      throw new TypeError("The listener must be a function");
    }
  }

  function ensureEvents(emitter) {
    if (!Object.prototype.hasOwnProperty.call(emitter, "_events") || !emitter._events) {
      Object.defineProperty(emitter, "_events", { value: Object.create(null), enumerable: false, writable: true, configurable: true });
    }
    return emitter._events;
  }

  function listenersFor(emitter, eventName, create) {
    const events = ensureEvents(emitter);
    if (!events[eventName] && create) {
      events[eventName] = [];
    }
    return events[eventName] || [];
  }

  function unwrap(listener) {
    return listener && listener.listener ? listener.listener : listener;
  }

  function wrapOnce(emitter, eventName, listener) {
    function wrapped() {
      emitter.removeListener(eventName, wrapped);
      return listener.apply(this, arguments);
    }
    Object.defineProperty(wrapped, "listener", { value: listener, enumerable: false, configurable: true });
    return wrapped;
  }

  function addListener(emitter, eventName, listener, prepend, once) {
    validateListener(listener);
    const entry = once ? wrapOnce(emitter, eventName, listener) : listener;
    if (eventName !== "newListener") {
      emitter.emit("newListener", eventName, listener);
    }
    const listeners = listenersFor(emitter, eventName, true);
    if (prepend) {
      listeners.unshift(entry);
    } else {
      listeners.push(entry);
    }
    return emitter;
  }

  class EventEmitter {
    constructor(options) {
      Object.defineProperty(this, "_events", { value: Object.create(null), enumerable: false, writable: true, configurable: true });
      Object.defineProperty(this, "_maxListeners", { value: undefined, enumerable: false, writable: true, configurable: true });
      this.captureRejections = !!(options && options.captureRejections);
    }
    setMaxListeners(n) {
      n = Number(n);
      if (!Number.isFinite(n) || n < 0) {
        throw new RangeError("n must be a non-negative number");
      }
      this._maxListeners = n;
      return this;
    }
    getMaxListeners() {
      return this._maxListeners === undefined ? EventEmitter.defaultMaxListeners : this._maxListeners;
    }
    addListener(eventName, listener) {
      return this.on(eventName, listener);
    }
    on(eventName, listener) {
      return addListener(this, eventName, listener, false, false);
    }
    prependListener(eventName, listener) {
      return addListener(this, eventName, listener, true, false);
    }
    once(eventName, listener) {
      return addListener(this, eventName, listener, false, true);
    }
    prependOnceListener(eventName, listener) {
      return addListener(this, eventName, listener, true, true);
    }
    removeListener(eventName, listener) {
      validateListener(listener);
      const listeners = listenersFor(this, eventName, false);
      for (let index = listeners.length - 1; index >= 0; index--) {
        const candidate = listeners[index];
        if (candidate === listener || candidate.listener === listener) {
          listeners.splice(index, 1);
          if (listeners.length === 0) {
            delete this._events[eventName];
          }
          if (eventName !== "removeListener") {
            this.emit("removeListener", eventName, unwrap(candidate));
          }
          break;
        }
      }
      return this;
    }
    off(eventName, listener) {
      return this.removeListener(eventName, listener);
    }
    removeAllListeners(eventName) {
      if (eventName === undefined) {
        for (const name of Reflect.ownKeys(this._events)) {
          this.removeAllListeners(name);
        }
        return this;
      }
      const listeners = listenersFor(this, eventName, false).slice();
      delete this._events[eventName];
      if (eventName !== "removeListener") {
        for (const listener of listeners) {
          this.emit("removeListener", eventName, unwrap(listener));
        }
      }
      return this;
    }
    emit(eventName) {
      const args = Array.prototype.slice.call(arguments, 1);
      if (eventName === "error") {
        const monitors = listenersFor(this, errorMonitor, false).slice();
        for (const monitor of monitors) {
          monitor.apply(this, args);
        }
      }
      const listeners = listenersFor(this, eventName, false).slice();
      if (listeners.length === 0) {
        if (eventName === "error") {
          const err = args[0];
          if (err instanceof Error) {
            throw err;
          }
          throw new Error("Unhandled error." + (err === undefined ? "" : " (" + String(err) + ")"));
        }
        return false;
      }
      for (const listener of listeners) {
        listener.apply(this, args);
      }
      return true;
    }
    eventNames() {
      return Reflect.ownKeys(this._events).filter((name) => this._events[name] && this._events[name].length > 0);
    }
    listeners(eventName) {
      return this.rawListeners(eventName).map(unwrap);
    }
    rawListeners(eventName) {
      return listenersFor(this, eventName, false).slice();
    }
    listenerCount(eventName, listener) {
      const listeners = listenersFor(this, eventName, false);
      if (listener === undefined) {
        return listeners.length;
      }
      return listeners.filter((candidate) => candidate === listener || candidate.listener === listener).length;
    }
  }

  function once(emitter, eventName) {
    return new Promise(function (resolve, reject) {
      let settled = false;
      function cleanup() {
        if (emitter && typeof emitter.removeListener === "function") {
          emitter.removeListener(eventName, handler);
          if (eventName !== "error") {
            emitter.removeListener("error", errorHandler);
          }
        } else if (emitter && typeof emitter.removeEventListener === "function") {
          emitter.removeEventListener(eventName, eventHandler);
          if (eventName !== "error") {
            emitter.removeEventListener("error", eventErrorHandler);
          }
        }
      }
      function settle(fn, value) {
        if (settled) {
          return;
        }
        settled = true;
        cleanup();
        fn(value);
      }
      function handler() {
        settle(resolve, Array.prototype.slice.call(arguments));
      }
      function errorHandler(err) {
        settle(reject, err);
      }
      function eventHandler(event) {
        settle(resolve, [event]);
      }
      function eventErrorHandler(event) {
        settle(reject, event && event.error ? event.error : event);
      }
      if (emitter && typeof emitter.once === "function") {
        emitter.once(eventName, handler);
        if (eventName !== "error") {
          emitter.once("error", errorHandler);
        }
      } else if (emitter && typeof emitter.addEventListener === "function") {
        emitter.addEventListener(eventName, eventHandler, { once: true });
        if (eventName !== "error") {
          emitter.addEventListener("error", eventErrorHandler, { once: true });
        }
      } else {
        reject(new TypeError("The emitter must be an EventEmitter or EventTarget"));
      }
    });
  }

  function on(emitter, eventName) {
    const queue = [];
    const waiters = [];
    let closed = false;
    function push(args) {
      if (waiters.length > 0) {
        waiters.shift().resolve({ value: args, done: false });
      } else {
        queue.push(args);
      }
    }
    function handler() {
      push(Array.prototype.slice.call(arguments));
    }
    function cleanup() {
      if (emitter && typeof emitter.removeListener === "function") {
        emitter.removeListener(eventName, handler);
      }
      closed = true;
      while (waiters.length > 0) {
        waiters.shift().resolve({ value: undefined, done: true });
      }
    }
    if (!emitter || typeof emitter.on !== "function") {
      throw new TypeError("The emitter must be an EventEmitter");
    }
    emitter.on(eventName, handler);
    return {
      next() {
        if (queue.length > 0) {
          return Promise.resolve({ value: queue.shift(), done: false });
        }
        if (closed) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return new Promise(function (resolve, reject) {
          waiters.push({ resolve, reject });
        });
      },
      return() {
        cleanup();
        return Promise.resolve({ value: undefined, done: true });
      },
      [Symbol.asyncIterator]() {
        return this;
      }
    };
  }

  function getEventListeners(emitter, eventName) {
    if (emitter instanceof EventEmitter) {
      return emitter.listeners(eventName);
    }
    if (emitter && emitter.__bruListeners) {
      return (emitter.__bruListeners[String(eventName)] || []).map(function (entry) { return entry.callback; });
    }
    return [];
  }

  function listenerCount(emitter, eventName) {
    if (emitter && typeof emitter.listenerCount === "function") {
      return emitter.listenerCount(eventName);
    }
    return getEventListeners(emitter, eventName).length;
  }

  function setMaxListeners(n) {
    const emitters = Array.prototype.slice.call(arguments, 1);
    for (const emitter of emitters) {
      if (emitter && typeof emitter.setMaxListeners === "function") {
        emitter.setMaxListeners(n);
      }
    }
  }

  function getMaxListeners(emitter) {
    if (emitter && typeof emitter.getMaxListeners === "function") {
      return emitter.getMaxListeners();
    }
    return EventEmitter.defaultMaxListeners;
  }

  EventEmitter.defaultMaxListeners = 10;
  EventEmitter.EventEmitter = EventEmitter;
  EventEmitter.default = EventEmitter;
  EventEmitter.once = once;
  EventEmitter.on = on;
  EventEmitter.listenerCount = listenerCount;
  EventEmitter.getEventListeners = getEventListeners;
  EventEmitter.setMaxListeners = setMaxListeners;
  EventEmitter.getMaxListeners = getMaxListeners;
  EventEmitter.errorMonitor = errorMonitor;
  EventEmitter.captureRejectionSymbol = captureRejectionSymbol;
  EventEmitter.addAbortListener = function (signal, listener) {
    validateListener(listener);
    if (signal && signal.aborted) {
      listener();
      return { dispose() {} };
    }
    if (signal && typeof signal.addEventListener === "function") {
      signal.addEventListener("abort", listener, { once: true });
      return { dispose() { signal.removeEventListener("abort", listener); } };
    }
    return { dispose() {} };
  };

  return EventEmitter;
})()`
	value, err := runtime.RunProgram(scriptEventsShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptStreamObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function callListener(listener, self, args) {
    if (typeof listener === "function") {
      listener.apply(self, args);
    }
  }

  class StreamBase {
    constructor() {
      Object.defineProperty(this, "_events", { value: {}, enumerable: false, writable: true });
    }
    on(event, listener) {
      const name = String(event);
      (this._events[name] || (this._events[name] = [])).push(listener);
      return this;
    }
    once(event, listener) {
      const self = this;
      function wrapped() {
        self.off(event, wrapped);
        return listener.apply(this, arguments);
      }
      return this.on(event, wrapped);
    }
    off(event, listener) {
      const listeners = this._events[String(event)];
      if (!listeners) return this;
      for (let index = listeners.length - 1; index >= 0; index--) {
        if (listeners[index] === listener) listeners.splice(index, 1);
      }
      return this;
    }
    removeListener(event, listener) {
      return this.off(event, listener);
    }
    emit(event) {
      const args = Array.prototype.slice.call(arguments, 1);
      const listeners = (this._events[String(event)] || []).slice();
      for (const listener of listeners) callListener(listener, this, args);
      return listeners.length > 0;
    }
    pipe(destination) {
      let chunk;
      while ((chunk = this.read()) !== null) {
        destination.write(chunk);
      }
      if (typeof destination.end === "function") destination.end();
      return destination;
    }
  }

  class Readable extends StreamBase {
    constructor(options) {
      super();
      options = options || {};
      this._readImpl = options.read;
      this._chunks = [];
      this.readable = true;
      this.writable = false;
    }
    static from(iterable) {
      const readable = new Readable();
      readable._chunks = Array.from(iterable || []);
      return readable;
    }
    read() {
      if (this._chunks.length > 0) return this._chunks.shift();
      if (typeof this._readImpl === "function") this._readImpl.call(this);
      return this._chunks.length > 0 ? this._chunks.shift() : null;
    }
    push(chunk) {
      if (chunk === null) {
        this.emit("end");
        return false;
      }
      this._chunks.push(chunk);
      this.emit("data", chunk);
      return true;
    }
  }

  class Writable extends StreamBase {
    constructor(options) {
      super();
      options = options || {};
      this._writeImpl = options.write;
      this._chunks = [];
      this.writable = true;
      this.readable = false;
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (typeof this._writeImpl === "function") {
        this._writeImpl.call(this, chunk, encoding || "utf8", callback || function () {});
      } else {
        this._chunks.push(chunk);
        if (callback) callback();
      }
      this.emit("data", chunk);
      return true;
    }
    end(chunk, encoding, callback) {
      if (typeof chunk === "function") {
        callback = chunk;
        chunk = undefined;
      }
      if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
      this.emit("finish");
      if (callback) callback();
      return this;
    }
  }

  class Duplex extends Readable {
    constructor(options) {
      super(options);
      options = options || {};
      this._writeImpl = options.write;
      this.writable = true;
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (typeof this._writeImpl === "function") {
        this._writeImpl.call(this, chunk, encoding || "utf8", callback || function () {});
      } else {
        if (callback) callback();
      }
      return true;
    }
    end(chunk, encoding, callback) {
      if (typeof chunk === "function") {
        callback = chunk;
        chunk = undefined;
      }
      if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
      this.emit("finish");
      if (callback) callback();
      return this;
    }
  }

  class Transform extends Duplex {
    constructor(options) {
      super(options);
      options = options || {};
      this._transformImpl = options.transform;
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (typeof this._transformImpl === "function") {
        const self = this;
        this._transformImpl.call(this, chunk, encoding || "utf8", function (err, data) {
          if (err) {
            self.emit("error", err);
          } else if (data !== undefined && data !== null) {
            self.push(data);
          }
          if (callback) callback(err);
        });
      } else {
        this.push(chunk);
        if (callback) callback();
      }
      return true;
    }
  }

  function pipeline() {
    const args = Array.prototype.slice.call(arguments);
    const callback = typeof args[args.length - 1] === "function" ? args.pop() : null;
    try {
      for (let index = 0; index < args.length - 1; index++) {
        if (args[index] && typeof args[index].pipe === "function") {
          args[index].pipe(args[index + 1]);
        }
      }
      if (callback) callback(null);
    } catch (err) {
      if (callback) callback(err);
      else throw err;
    }
    return args[args.length - 1];
  }

  return { Stream: StreamBase, Readable, Writable, Duplex, Transform, pipeline };
})()`
	value, err := runtime.RunProgram(scriptStreamShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptStreamPromisesObject(runtime *goja.Runtime, streamObject goja.Value) goja.Value {
	const streamGlobal = "__liteapi_stream_for_promises__"
	global := runtime.GlobalObject()
	_ = global.Set(streamGlobal, streamObject)
	// Removing a global we just set cannot meaningfully fail.
	defer func() { _ = global.Delete(streamGlobal) }()
	script := `(function (stream) {
  function cleanup(stream, listeners) {
    if (!stream) return;
    const remove = typeof stream.off === "function" ? stream.off : stream.removeListener;
    if (typeof remove !== "function") return;
    for (const item of listeners) {
      remove.call(stream, item[0], item[1]);
    }
  }

  function finished(target, options) {
    options = options || {};
    return new Promise(function (resolve, reject) {
      if (!target || typeof target.on !== "function") {
        reject(new TypeError("The \"stream\" argument must be a stream"));
        return;
      }
      let settled = false;
      const listeners = [];
      function settle(fn, value) {
        if (settled) return;
        settled = true;
        cleanup(target, listeners);
        fn(value);
      }
      function add(event, listener) {
        listeners.push([event, listener]);
        if (typeof target.once === "function") {
          target.once(event, listener);
        } else {
          target.on(event, listener);
        }
      }
      add("error", function (err) {
        settle(reject, err || new Error("Stream error"));
      });
      add("finish", function () {
        settle(resolve, undefined);
      });
      add("end", function () {
        settle(resolve, undefined);
      });
      if (options.cleanup !== false) {
        add("close", function () {
          settle(resolve, undefined);
        });
      }
    });
  }

  function pipeline() {
    const args = Array.prototype.slice.call(arguments);
    return new Promise(function (resolve, reject) {
      try {
        stream.pipeline.apply(stream, args.concat(function (err) {
          if (err) reject(err);
          else resolve(undefined);
        }));
      } catch (err) {
        reject(err);
      }
    });
  }

  const promises = { finished, pipeline };
  promises.default = promises;
  return promises;
})(globalThis.__liteapi_stream_for_promises__)`
	value, err := runtime.RunProgram(scriptStreamPromisesShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptEventTarget(runtime *goja.Runtime) {
	script := `(function () {
  if (typeof globalThis.Event === "function" && typeof globalThis.EventTarget === "function" && typeof globalThis.CustomEvent === "function") {
    return;
  }

  function eventOption(options, name) {
    if (options === undefined || options === null) {
      return false;
    }
    if (typeof options === "boolean") {
      return name === "capture" ? options : false;
    }
    return !!options[name];
  }

  function Event(type, options) {
    if (!(this instanceof Event)) {
      throw new TypeError("Class constructor Event cannot be invoked without 'new'");
    }
    options = options || {};
    this.type = String(type);
    this.bubbles = !!options.bubbles;
    this.cancelable = !!options.cancelable;
    this.composed = !!options.composed;
    this.defaultPrevented = false;
    this.eventPhase = 0;
    this.isTrusted = false;
    this.target = null;
    this.currentTarget = null;
    this.timeStamp = Date.now();
    this.cancelBubble = false;
    this._immediateStopped = false;
  }
  Event.prototype.preventDefault = function () {
    if (this.cancelable) {
      this.defaultPrevented = true;
    }
  };
  Event.prototype.stopPropagation = function () {
    this.cancelBubble = true;
  };
  Event.prototype.stopImmediatePropagation = function () {
    this.cancelBubble = true;
    this._immediateStopped = true;
  };
  Event.prototype.composedPath = function () {
    return this.target ? [this.target] : [];
  };

  function CustomEvent(type, options) {
    if (!(this instanceof CustomEvent)) {
      throw new TypeError("Class constructor CustomEvent cannot be invoked without 'new'");
    }
    Event.call(this, type, options);
    this.detail = options && Object.prototype.hasOwnProperty.call(options, "detail") ? options.detail : null;
  }
  CustomEvent.prototype = Object.create(Event.prototype);
  CustomEvent.prototype.constructor = CustomEvent;

  function EventTarget() {
    if (!(this instanceof EventTarget)) {
      throw new TypeError("Class constructor EventTarget cannot be invoked without 'new'");
    }
    Object.defineProperty(this, "__bruListeners", { value: {}, enumerable: false, configurable: false, writable: false });
  }
  EventTarget.prototype.addEventListener = function (type, callback, options) {
    if (callback === undefined || callback === null) {
      return;
    }
    type = String(type);
    const capture = eventOption(options, "capture");
    const once = eventOption(options, "once");
    const listeners = this.__bruListeners[type] || (this.__bruListeners[type] = []);
    for (const listener of listeners) {
      if (listener.callback === callback && listener.capture === capture) {
        return;
      }
    }
    listeners.push({ callback, capture, once });
  };
  EventTarget.prototype.removeEventListener = function (type, callback, options) {
    type = String(type);
    const capture = eventOption(options, "capture");
    const listeners = this.__bruListeners[type];
    if (!listeners) {
      return;
    }
    for (let index = listeners.length - 1; index >= 0; index--) {
      const listener = listeners[index];
      if (listener.callback === callback && listener.capture === capture) {
        listeners.splice(index, 1);
      }
    }
  };
  EventTarget.prototype.dispatchEvent = function (event) {
    if (!(event instanceof Event)) {
      throw new TypeError("The event must be an Event");
    }
    event.target = event.target || this;
    event.currentTarget = this;
    const listeners = (this.__bruListeners[event.type] || []).slice();
    for (const listener of listeners) {
      if (listener.once) {
        this.removeEventListener(event.type, listener.callback, { capture: listener.capture });
      }
      if (typeof listener.callback === "function") {
        listener.callback.call(this, event);
      } else if (listener.callback && typeof listener.callback.handleEvent === "function") {
        listener.callback.handleEvent.call(listener.callback, event);
      }
      if (event._immediateStopped) {
        break;
      }
    }
    event.currentTarget = null;
    return !event.defaultPrevented;
  };

  globalThis.Event = Event;
  globalThis.CustomEvent = CustomEvent;
  globalThis.EventTarget = EventTarget;
})()`
	if _, err := runtime.RunProgram(scriptEventTargetShim.compiled(script)); err != nil {
		panic(runtime.NewGoError(err))
	}
}

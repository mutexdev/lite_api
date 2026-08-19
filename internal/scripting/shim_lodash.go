package scripting

// The lodash shim and its subpath modules.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"github.com/dop251/goja"
)

func newScriptLodashObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const objectToString = Object.prototype.toString;

  function isObject(value) {
    return value !== null && (typeof value === "object" || typeof value === "function");
  }

  function isPlainObject(value) {
    if (objectToString.call(value) !== "[object Object]") return false;
    const proto = Object.getPrototypeOf(value);
    return proto === null || proto === Object.prototype;
  }

  function parsePath(path) {
    if (Array.isArray(path)) return path.map(String);
    const text = String(path || "");
    const parts = [];
    text.replace(/[^.[\]]+|\[(?:(-?\d+(?:\.\d+)?)|(["'])((?:(?!\2)[^\\]|\\.)*?)\2)\]/g, function (match, number, quote, quoted) {
      if (quote) {
        parts.push(quoted.replace(/\\(\\)?/g, "$1"));
      } else {
        parts.push(number !== undefined ? number : match);
      }
    });
    return parts;
  }

  function get(object, path, defaultValue) {
    const parts = parsePath(path);
    let current = object;
    for (const part of parts) {
      if (current == null) return defaultValue;
      current = current[part];
    }
    return current === undefined ? defaultValue : current;
  }

  function has(object, path) {
    const parts = parsePath(path);
    let current = object;
    for (const part of parts) {
      if (current == null || !Object.prototype.hasOwnProperty.call(Object(current), part)) return false;
      current = current[part];
    }
    return true;
  }

  function set(object, path, value) {
    const parts = parsePath(path);
    if (!parts.length) return object;
    let current = object;
    for (let index = 0; index < parts.length - 1; index++) {
      const part = parts[index];
      if (!isObject(current[part])) {
        current[part] = /^\d+$/.test(parts[index + 1]) ? [] : {};
      }
      current = current[part];
    }
    current[parts[parts.length - 1]] = value;
    return object;
  }

  function unset(object, path) {
    const parts = parsePath(path);
    if (!parts.length) return true;
    let current = object;
    for (let index = 0; index < parts.length - 1; index++) {
      current = current == null ? undefined : current[parts[index]];
      if (current == null) return true;
    }
    return delete current[parts[parts.length - 1]];
  }

  function cloneDeep(value, seen) {
    if (!isObject(value)) return value;
    seen = seen || new Map();
    if (seen.has(value)) return seen.get(value);
    if (value instanceof Date) return new Date(value.getTime());
    if (value instanceof RegExp) return new RegExp(value.source, value.flags);
    if (typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value)) return new value.constructor(value);
    if (typeof ArrayBuffer !== "undefined" && value instanceof ArrayBuffer) return value.slice(0);
    const out = Array.isArray(value) ? [] : {};
    seen.set(value, out);
    for (const key of Reflect.ownKeys(value)) {
      out[key] = cloneDeep(value[key], seen);
    }
    return out;
  }

  function isEqual(a, b, seen) {
    if (Object.is(a, b)) return true;
    if (!isObject(a) || !isObject(b)) return false;
    seen = seen || new Map();
    const cached = seen.get(a);
    if (cached && cached === b) return true;
    seen.set(a, b);
    if (objectToString.call(a) !== objectToString.call(b)) return false;
    if (a instanceof Date) return a.getTime() === b.getTime();
    if (a instanceof RegExp) return String(a) === String(b);
    if (typeof ArrayBuffer !== "undefined" && (ArrayBuffer.isView(a) || ArrayBuffer.isView(b))) {
      if (!ArrayBuffer.isView(a) || !ArrayBuffer.isView(b) || a.length !== b.length) return false;
      for (let index = 0; index < a.length; index++) if (a[index] !== b[index]) return false;
      return true;
    }
    const aKeys = Reflect.ownKeys(a);
    const bKeys = Reflect.ownKeys(b);
    if (aKeys.length !== bKeys.length) return false;
    for (const key of aKeys) {
      if (!Object.prototype.hasOwnProperty.call(b, key) || !isEqual(a[key], b[key], seen)) return false;
    }
    return true;
  }

  function toArray(collection) {
    if (collection == null) return [];
    if (Array.isArray(collection)) return collection.slice();
    if (typeof collection === "string") return collection.split("");
    if (typeof collection[Symbol.iterator] === "function") return Array.from(collection);
    return Object.keys(collection).map((key) => collection[key]);
  }

  function iterate(collection, iteratee) {
    if (collection == null) return [];
    if (Array.isArray(collection) || typeof collection === "string") {
      return Array.prototype.map.call(collection, function (value, index) { return iteratee(value, index, collection); });
    }
    return Object.keys(collection).map(function (key) { return iteratee(collection[key], key, collection); });
  }

  function property(path) {
    return function (value) { return get(value, path); };
  }

  function matches(source) {
    return function (value) {
      for (const key of Object.keys(source || {})) {
        if (!isEqual(value == null ? undefined : value[key], source[key])) return false;
      }
      return true;
    };
  }

  function normalizeIteratee(iteratee) {
    if (typeof iteratee === "function") return iteratee;
    if (Array.isArray(iteratee)) return function (value) { return isEqual(get(value, iteratee[0]), iteratee[1]); };
    if (typeof iteratee === "string") return property(iteratee);
    if (iteratee && typeof iteratee === "object") return matches(iteratee);
    return function (value) { return value; };
  }

  function map(collection, iteratee) {
    return iterate(collection, normalizeIteratee(iteratee));
  }

  function forEach(collection, iteratee) {
    iterate(collection, normalizeIteratee(iteratee));
    return collection;
  }

  function filter(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    return iterate(collection, function (value, key, source) {
      return fn(value, key, source) ? value : undefined;
    }).filter(function (value) { return value !== undefined; });
  }

  function find(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    const values = toArray(collection);
    for (let index = 0; index < values.length; index++) {
      if (fn(values[index], index, collection)) return values[index];
    }
    return undefined;
  }

  function reduce(collection, iteratee, accumulator) {
    const values = toArray(collection);
    let index = 0;
    if (arguments.length < 3) {
      accumulator = values[0];
      index = 1;
    }
    for (; index < values.length; index++) {
      accumulator = iteratee(accumulator, values[index], index, collection);
    }
    return accumulator;
  }

  function includes(collection, value) {
    if (typeof collection === "string") return collection.indexOf(String(value)) !== -1;
    return toArray(collection).some(function (item) { return isEqual(item, value); });
  }

  function keys(value) {
    return value == null ? [] : Object.keys(Object(value));
  }

  function values(value) {
    return keys(value).map(function (key) { return value[key]; });
  }

  function pick(object) {
    const out = {};
    const paths = Array.prototype.slice.call(arguments, 1).flat();
    for (const path of paths) {
      if (has(object, path)) set(out, path, get(object, path));
    }
    return out;
  }

  function omit(object) {
    const out = cloneDeep(object || {});
    const paths = Array.prototype.slice.call(arguments, 1).flat();
    for (const path of paths) unset(out, path);
    return out;
  }

  function merge(target) {
    target = target || {};
    for (let sourceIndex = 1; sourceIndex < arguments.length; sourceIndex++) {
      const source = arguments[sourceIndex];
      if (!isObject(source)) continue;
      for (const key of Object.keys(source)) {
        if (isPlainObject(source[key]) && isPlainObject(target[key])) {
          merge(target[key], source[key]);
        } else if (Array.isArray(source[key])) {
          target[key] = source[key].slice();
        } else {
          target[key] = source[key];
        }
      }
    }
    return target;
  }

  function groupBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      const key = String(fn(value, index, collection));
      (out[key] || (out[key] = [])).push(value);
      return out;
    }, {});
  }

  function keyBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      out[String(fn(value, index, collection))] = value;
      return out;
    }, {});
  }

  function sortBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).map(function (value, index) {
      return { value, index, criteria: fn(value, index, collection) };
    }).sort(function (left, right) {
      if (left.criteria < right.criteria) return -1;
      if (left.criteria > right.criteria) return 1;
      return left.index - right.index;
    }).map(function (entry) { return entry.value; });
  }

  function uniq(collection) {
    const out = [];
    for (const value of toArray(collection)) {
      if (!out.some(function (existing) { return isEqual(existing, value); })) out.push(value);
    }
    return out;
  }

  function flatten(collection) {
    return toArray(collection).reduce(function (out, value) {
      return out.concat(Array.isArray(value) ? value : [value]);
    }, []);
  }

  function flattenDeep(collection) {
    return toArray(collection).reduce(function (out, value) {
      return out.concat(Array.isArray(value) ? flattenDeep(value) : [value]);
    }, []);
  }

  function chunk(collection, size) {
    const values = toArray(collection);
    const width = Math.max(1, Number(size) || 1);
    const out = [];
    for (let index = 0; index < values.length; index += width) {
      out.push(values.slice(index, index + width));
    }
    return out;
  }

  function compact(collection) {
    return toArray(collection).filter(Boolean);
  }

  function isEmpty(value) {
    if (value == null) return true;
    if (Array.isArray(value) || typeof value === "string") return value.length === 0;
    if (value instanceof Map || value instanceof Set) return value.size === 0;
    return Object.keys(Object(value)).length === 0;
  }

  // ---------------------------------------------------------------------
  // Added because a real collection could not run without them.
  //
  // Postman bundles the whole of lodash; this file ships a subset, and every
  // absent function surfaces as "Object has no member 'x'" with no mention of
  // lodash at all. The functions below are the ones API scripts actually reach
  // for: randomised test data, string casing for header and field names, and
  // the everyday array and object helpers.
  //
  // Semantics follow lodash 4.17 rather than intuition, because a plausible
  // near-miss is worse than an absence: an absence throws, a near-miss silently
  // produces wrong data.
  // ---------------------------------------------------------------------

  function toStr(value) {
    if (value == null) return "";
    return typeof value === "string" ? value : String(value);
  }

  function toNumber(value) {
    if (typeof value === "number") return value;
    const parsed = Number(value);
    return parsed;
  }

  function toInteger(value) {
    const parsed = toNumber(value);
    if (!isFinite(parsed) || parsed !== parsed) return 0;
    return parsed < 0 ? Math.ceil(parsed) : Math.floor(parsed);
  }

  function baseRandom(lower, upper) {
    return lower + Math.floor(Math.random() * (upper - lower + 1));
  }

  /**
   * _.random([lower=0], [upper=1], [floating])
   *
   * ONE numeric argument is the UPPER bound with a lower bound of zero. Reading
   * it as the lower bound produces negative array indexes, which is exactly the
   * shape of bug that never throws.
   */
  function random(lower, upper, floating) {
    if (floating === undefined && typeof upper === "boolean") {
      floating = upper;
      upper = undefined;
    }
    if (floating === undefined && typeof lower === "boolean") {
      floating = lower;
      lower = undefined;
    }
    if (lower === undefined && upper === undefined) {
      lower = 0;
      upper = 1;
    } else if (upper === undefined) {
      upper = toNumber(lower);
      lower = 0;
    } else {
      lower = toNumber(lower);
      upper = toNumber(upper);
    }
    if (lower !== lower) lower = 0;
    if (upper !== upper) upper = 0;
    if (lower > upper) {
      const swap = lower;
      lower = upper;
      upper = swap;
    }
    if (floating || lower % 1 !== 0 || upper % 1 !== 0) {
      return Math.min(lower + Math.random() * (upper - lower), upper);
    }
    return baseRandom(lower, upper);
  }

  function sample(collection) {
    const values = toArray(collection);
    if (values.length === 0) return undefined;
    return values[baseRandom(0, values.length - 1)];
  }

  function shuffle(collection) {
    const values = toArray(collection).slice();
    for (let index = values.length - 1; index > 0; index -= 1) {
      const target = baseRandom(0, index);
      const held = values[index];
      values[index] = values[target];
      values[target] = held;
    }
    return values;
  }

  function sampleSize(collection, size) {
    const values = toArray(collection);
    const count = size === undefined ? 1 : Math.min(Math.max(toInteger(size), 0), values.length);
    return shuffle(values).slice(0, count);
  }

  function times(count, iteratee) {
    const total = Math.max(toInteger(count), 0);
    const fn = typeof iteratee === "function" ? iteratee : function (index) { return index; };
    const out = [];
    for (let index = 0; index < total; index += 1) out.push(fn(index));
    return out;
  }

  function range(start, end, step) {
    let from = toNumber(start) || 0;
    let to;
    if (end === undefined) {
      to = from;
      from = 0;
    } else {
      to = toNumber(end) || 0;
    }
    let stride = step === undefined ? (from < to ? 1 : -1) : toNumber(step);
    // A zero step would loop forever; lodash treats it as "repeat the start",
    // but an empty result is the safer reading inside a request script.
    if (!stride) return [];
    const out = [];
    if (stride > 0) {
      for (let value = from; value < to; value += stride) out.push(value);
    } else {
      for (let value = from; value > to; value += stride) out.push(value);
    }
    return out;
  }

  // The case family is all built on words(), so the splitter is the only part
  // that has to be right. This is lodash's ASCII pattern: a run of capitals is
  // its own word only up to the capital that starts the next one, which is what
  // turns XMLHttpRequest into XML / Http / Request.
  const wordsPattern = /[A-Z]{2,}(?=[A-Z][a-z]+[0-9]*|\b)|[A-Z]?[a-z]+[0-9]*|[A-Z]+|[0-9]+/g;

  function words(string, pattern) {
    const text = toStr(string);
    if (pattern !== undefined) return text.match(pattern) || [];
    return text.match(wordsPattern) || [];
  }

  function upperFirst(string) {
    const text = toStr(string);
    return text.charAt(0).toUpperCase() + text.slice(1);
  }

  function lowerFirst(string) {
    const text = toStr(string);
    return text.charAt(0).toLowerCase() + text.slice(1);
  }

  function capitalize(string) {
    return upperFirst(toStr(string).toLowerCase());
  }

  function camelCase(string) {
    return words(toStr(string)).map(function (word, index) {
      const lowered = word.toLowerCase();
      return index === 0 ? lowered : upperFirst(lowered);
    }).join("");
  }

  function kebabCase(string) {
    return words(toStr(string)).map(function (word) { return word.toLowerCase(); }).join("-");
  }

  function snakeCase(string) {
    return words(toStr(string)).map(function (word) { return word.toLowerCase(); }).join("_");
  }

  // startCase capitalises without lowering the rest, so "fooBar" is "Foo Bar"
  // but "XMLHttp" keeps its shouting.
  function startCase(string) {
    return words(toStr(string)).map(upperFirst).join(" ");
  }

  function escapeForCharClass(value) {
    return String(value).replace(/[\\\]^\-]/g, "\\$&");
  }

  function trim(string, chars) {
    const text = toStr(string);
    if (chars === undefined) return text.replace(/^\s+|\s+$/g, "");
    const cls = "[" + escapeForCharClass(chars) + "]";
    return text.replace(new RegExp("^" + cls + "+|" + cls + "+$", "g"), "");
  }

  function trimStart(string, chars) {
    const text = toStr(string);
    if (chars === undefined) return text.replace(/^\s+/, "");
    return text.replace(new RegExp("^[" + escapeForCharClass(chars) + "]+"), "");
  }

  function trimEnd(string, chars) {
    const text = toStr(string);
    if (chars === undefined) return text.replace(/\s+$/, "");
    return text.replace(new RegExp("[" + escapeForCharClass(chars) + "]+$"), "");
  }

  function createPadding(length, chars) {
    const filler = chars === undefined ? " " : String(chars);
    if (length < 1 || filler === "") return "";
    return filler.repeat(Math.ceil(length / filler.length)).slice(0, length);
  }

  function padStart(string, length, chars) {
    const text = toStr(string);
    return createPadding(toInteger(length) - text.length, chars) + text;
  }

  function padEnd(string, length, chars) {
    const text = toStr(string);
    return text + createPadding(toInteger(length) - text.length, chars);
  }

  function pad(string, length, chars) {
    const text = toStr(string);
    const total = toInteger(length) - text.length;
    if (total <= 0) return text;
    return createPadding(Math.floor(total / 2), chars) + text + createPadding(Math.ceil(total / 2), chars);
  }

  function repeat(string, count) {
    const total = Math.max(toInteger(count), 0);
    return total === 0 ? "" : toStr(string).repeat(total);
  }

  function startsWith(string, target, position) {
    const text = toStr(string);
    const from = position === undefined ? 0 : toInteger(position);
    return text.slice(from, from + toStr(target).length) === toStr(target);
  }

  function endsWith(string, target, position) {
    const text = toStr(string);
    const end = position === undefined ? text.length : toInteger(position);
    const suffix = toStr(target);
    return suffix === "" || text.slice(end - suffix.length, end) === suffix;
  }

  const htmlEscapes = { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" };
  const htmlUnescapes = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": "\"", "&#39;": "'" };

  function escape(string) {
    return toStr(string).replace(/[&<>"']/g, function (character) { return htmlEscapes[character]; });
  }

  function unescape(string) {
    return toStr(string).replace(/&(?:amp|lt|gt|quot|#39);/g, function (entity) { return htmlUnescapes[entity]; });
  }

  function head(collection) {
    const values = toArray(collection);
    return values.length ? values[0] : undefined;
  }

  function last(collection) {
    const values = toArray(collection);
    return values.length ? values[values.length - 1] : undefined;
  }

  function nth(collection, index) {
    const values = toArray(collection);
    const position = toInteger(index);
    return values[position < 0 ? values.length + position : position];
  }

  function take(collection, count) {
    return toArray(collection).slice(0, count === undefined ? 1 : Math.max(toInteger(count), 0));
  }

  function takeRight(collection, count) {
    const values = toArray(collection);
    const total = count === undefined ? 1 : Math.max(toInteger(count), 0);
    return total === 0 ? [] : values.slice(Math.max(values.length - total, 0));
  }

  function drop(collection, count) {
    return toArray(collection).slice(count === undefined ? 1 : Math.max(toInteger(count), 0));
  }

  function dropRight(collection, count) {
    const values = toArray(collection);
    const total = count === undefined ? 1 : Math.max(toInteger(count), 0);
    return total === 0 ? values.slice() : values.slice(0, Math.max(values.length - total, 0));
  }

  function initial(collection) {
    const values = toArray(collection);
    return values.slice(0, Math.max(values.length - 1, 0));
  }

  function tail(collection) {
    return toArray(collection).slice(1);
  }

  function difference(collection) {
    const excluded = Array.prototype.slice.call(arguments, 1).reduce(function (out, other) {
      return out.concat(toArray(other));
    }, []);
    return toArray(collection).filter(function (value) {
      return !excluded.some(function (other) { return isEqual(other, value); });
    });
  }

  function intersection(collection) {
    const others = Array.prototype.slice.call(arguments, 1).map(toArray);
    return uniq(toArray(collection)).filter(function (value) {
      return others.every(function (other) {
        return other.some(function (candidate) { return isEqual(candidate, value); });
      });
    });
  }

  function union() {
    return uniq(Array.prototype.slice.call(arguments).reduce(function (out, other) {
      return out.concat(toArray(other));
    }, []));
  }

  function without(collection) {
    const excluded = Array.prototype.slice.call(arguments, 1);
    return toArray(collection).filter(function (value) {
      return !excluded.some(function (other) { return isEqual(other, value); });
    });
  }

  function uniqBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    const seen = [];
    const out = [];
    toArray(collection).forEach(function (value, index) {
      const key = fn(value, index, collection);
      if (!seen.some(function (existing) { return isEqual(existing, key); })) {
        seen.push(key);
        out.push(value);
      }
    });
    return out;
  }

  function zip() {
    const lists = Array.prototype.slice.call(arguments).map(toArray);
    const width = lists.reduce(function (longest, list) { return Math.max(longest, list.length); }, 0);
    const out = [];
    for (let index = 0; index < width; index += 1) {
      out.push(lists.map(function (list) { return list[index]; }));
    }
    return out;
  }

  function unzip(collection) {
    return zip.apply(null, toArray(collection));
  }

  function fromPairs(collection) {
    return toArray(collection).reduce(function (out, pair) {
      if (pair != null) out[String(pair[0])] = pair[1];
      return out;
    }, {});
  }

  function toPairs(value) {
    return Object.keys(Object(value)).map(function (key) { return [key, value[key]]; });
  }

  function findIndex(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    const values = toArray(collection);
    for (let index = 0; index < values.length; index += 1) {
      if (fn(values[index], index, collection)) return index;
    }
    return -1;
  }

  function findLastIndex(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    const values = toArray(collection);
    for (let index = values.length - 1; index >= 0; index -= 1) {
      if (fn(values[index], index, collection)) return index;
    }
    return -1;
  }

  function some(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    return toArray(collection).some(function (value, index) { return Boolean(fn(value, index, collection)); });
  }

  function every(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    return toArray(collection).every(function (value, index) { return Boolean(fn(value, index, collection)); });
  }

  function reject(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    return toArray(collection).filter(function (value, index) { return !fn(value, index, collection); });
  }

  function size(value) {
    if (value == null) return 0;
    if (Array.isArray(value) || typeof value === "string") return value.length;
    if (value instanceof Map || value instanceof Set) return value.size;
    return Object.keys(Object(value)).length;
  }

  function flatMap(collection, iteratee) {
    return flatten(map(collection, iteratee));
  }

  function partition(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    const truthy = [];
    const falsy = [];
    toArray(collection).forEach(function (value, index) {
      (fn(value, index, collection) ? truthy : falsy).push(value);
    });
    return [truthy, falsy];
  }

  function countBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      const key = String(fn(value, index, collection));
      out[key] = (out[key] || 0) + 1;
      return out;
    }, {});
  }

  function orderBy(collection, iteratees, orders) {
    const list = Array.isArray(iteratees) ? iteratees : [iteratees];
    const directions = Array.isArray(orders) ? orders : (orders === undefined ? [] : [orders]);
    const fns = list.map(normalizeIteratee);
    return toArray(collection).map(function (value, index) {
      return { value, index, criteria: fns.map(function (fn) { return fn(value, index, collection); }) };
    }).sort(function (left, right) {
      for (let position = 0; position < fns.length; position += 1) {
        const a = left.criteria[position];
        const b = right.criteria[position];
        if (a !== b) {
          const descending = String(directions[position] || "asc").toLowerCase() === "desc";
          if (a < b) return descending ? 1 : -1;
          if (a > b) return descending ? -1 : 1;
        }
      }
      return left.index - right.index;
    }).map(function (entry) { return entry.value; });
  }

  function sum(collection) {
    return toArray(collection).reduce(function (total, value) { return total + (toNumber(value) || 0); }, 0);
  }

  function sumBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (total, value, index) {
      return total + (toNumber(fn(value, index, collection)) || 0);
    }, 0);
  }

  function mean(collection) {
    const values = toArray(collection);
    return values.length === 0 ? NaN : sum(values) / values.length;
  }

  function extremeBy(collection, iteratee, wantGreater) {
    const fn = normalizeIteratee(iteratee);
    let best;
    let bestCriteria;
    toArray(collection).forEach(function (value, index) {
      const criteria = fn(value, index, collection);
      if (criteria == null) return;
      if (bestCriteria === undefined || (wantGreater ? criteria > bestCriteria : criteria < bestCriteria)) {
        bestCriteria = criteria;
        best = value;
      }
    });
    return best;
  }

  function maxBy(collection, iteratee) { return extremeBy(collection, iteratee, true); }
  function minBy(collection, iteratee) { return extremeBy(collection, iteratee, false); }
  function max(collection) { return extremeBy(collection, undefined, true); }
  function min(collection) { return extremeBy(collection, undefined, false); }

  function clamp(number, lower, upper) {
    let low = lower;
    let high = upper;
    if (high === undefined) {
      high = low;
      low = undefined;
    }
    let value = toNumber(number);
    if (value !== value) return value;
    if (high !== undefined) value = Math.min(value, toNumber(high));
    if (low !== undefined) value = Math.max(value, toNumber(low));
    return value;
  }

  function inRange(number, start, end) {
    let from = start;
    let to = end;
    if (to === undefined) {
      to = from;
      from = 0;
    }
    from = toNumber(from);
    to = toNumber(to);
    const value = toNumber(number);
    return value >= Math.min(from, to) && value < Math.max(from, to);
  }

  function clone(value) {
    if (Array.isArray(value)) return value.slice();
    if (!isObject(value)) return value;
    if (value instanceof Date) return new Date(value.getTime());
    return Object.assign({}, value);
  }

  // Mutates and returns its first argument, exactly as lodash does; scripts rely
  // on that to fill a config object in place.
  function defaults(object) {
    const target = Object(object);
    Array.prototype.slice.call(arguments, 1).forEach(function (source) {
      if (source == null) return;
      Object.keys(source).forEach(function (key) {
        if (target[key] === undefined) target[key] = source[key];
      });
    });
    return target;
  }

  function invert(value) {
    return Object.keys(Object(value)).reduce(function (out, key) {
      out[String(value[key])] = key;
      return out;
    }, {});
  }

  function mapValues(value, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return Object.keys(Object(value)).reduce(function (out, key) {
      out[key] = fn(value[key], key, value);
      return out;
    }, {});
  }

  function mapKeys(value, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return Object.keys(Object(value)).reduce(function (out, key) {
      out[String(fn(value[key], key, value))] = value[key];
      return out;
    }, {});
  }

  function pickBy(value, predicate) {
    const fn = predicate === undefined ? Boolean : normalizeIteratee(predicate);
    return Object.keys(Object(value)).reduce(function (out, key) {
      if (fn(value[key], key, value)) out[key] = value[key];
      return out;
    }, {});
  }

  function omitBy(value, predicate) {
    const fn = predicate === undefined ? Boolean : normalizeIteratee(predicate);
    return Object.keys(Object(value)).reduce(function (out, key) {
      if (!fn(value[key], key, value)) out[key] = value[key];
      return out;
    }, {});
  }

  let uniqueIdCounter = 0;
  function uniqueId(prefix) {
    uniqueIdCounter += 1;
    return (prefix === undefined ? "" : String(prefix)) + uniqueIdCounter;
  }

  function Chain(value) {
    this.__value = value;
  }
  Chain.prototype.value = function () { return this.__value; };
  Chain.prototype.map = function (iteratee) { this.__value = map(this.__value, iteratee); return this; };
  Chain.prototype.filter = function (predicate) { this.__value = filter(this.__value, predicate); return this; };
  Chain.prototype.forEach = function (iteratee) { forEach(this.__value, iteratee); return this; };
  Chain.prototype.reduce = function (iteratee, accumulator) { this.__value = reduce(this.__value, iteratee, accumulator); return this; };
  Chain.prototype.sortBy = function (iteratee) { this.__value = sortBy(this.__value, iteratee); return this; };
  Chain.prototype.uniq = function () { this.__value = uniq(this.__value); return this; };
  Chain.prototype.flatten = function () { this.__value = flatten(this.__value); return this; };
  Chain.prototype.flattenDeep = function () { this.__value = flattenDeep(this.__value); return this; };
  Chain.prototype.compact = function () { this.__value = compact(this.__value); return this; };
  Chain.prototype.get = function (path, defaultValue) { this.__value = get(this.__value, path, defaultValue); return this; };

  function chain(value) {
    return new Chain(value);
  }

  function lodash(value) {
    return chain(value);
  }

  Object.assign(lodash, {
    VERSION: "4.17.21-liteapi",
    chain,
    get,
    set,
    has,
    unset,
    property,
    cloneDeep,
    isEqual,
    isObject,
    isPlainObject,
    isArray: Array.isArray,
    isString: function (value) { return typeof value === "string" || value instanceof String; },
    isNumber: function (value) { return typeof value === "number" || value instanceof Number; },
    isBoolean: function (value) { return value === true || value === false || value instanceof Boolean; },
    isFunction: function (value) { return typeof value === "function"; },
    isNil: function (value) { return value == null; },
    isNull: function (value) { return value === null; },
    isUndefined: function (value) { return value === undefined; },
    isEmpty,
    toArray,
    keys,
    values,
    map,
    each: forEach,
    forEach,
    filter,
    find,
    reduce,
    includes,
    pick,
    omit,
    merge,
    assign: Object.assign,
    groupBy,
    keyBy,
    sortBy,
    uniq,
    flatten,
    flattenDeep,
    chunk,
    compact,
    random,
    sample,
    sampleSize,
    shuffle,
    times,
    range,
    words,
    camelCase,
    kebabCase,
    snakeCase,
    startCase,
    capitalize,
    upperFirst,
    lowerFirst,
    toUpper: function (value) { return toStr(value).toUpperCase(); },
    toLower: function (value) { return toStr(value).toLowerCase(); },
    trim,
    trimStart,
    trimEnd,
    pad,
    padStart,
    padEnd,
    repeat,
    startsWith,
    endsWith,
    escape,
    unescape,
    first: head,
    head,
    last,
    nth,
    take,
    takeRight,
    drop,
    dropRight,
    initial,
    tail,
    difference,
    intersection,
    union,
    without,
    uniqBy,
    zip,
    unzip,
    fromPairs,
    toPairs,
    entries: toPairs,
    findIndex,
    findLastIndex,
    some,
    every,
    reject,
    size,
    flatMap,
    partition,
    countBy,
    orderBy,
    sum,
    sumBy,
    mean,
    max,
    min,
    maxBy,
    minBy,
    clamp,
    inRange,
    clone,
    defaults,
    invert,
    mapValues,
    mapKeys,
    pickBy,
    omitBy,
    uniqueId,
    toNumber,
    toInteger,
    toString: toStr,
    isDate: function (value) { return objectToString.call(value) === "[object Date]"; },
    isRegExp: function (value) { return objectToString.call(value) === "[object RegExp]"; },
    isInteger: Number.isInteger,
    isFinite: function (value) { return typeof value === "number" && isFinite(value); },
    isNaN: function (value) { return typeof value === "number" && value !== value; },
    identity: function (value) { return value; },
    noop: function () {},
    constant: function (value) { return function () { return value; }; }
  });
  lodash.default = lodash;
  return lodash;
})()`
	value, err := runtime.RunProgram(scriptLodashShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptLodashSubpathModules(runtime *goja.Runtime, modules map[string]goja.Value, lodashObject goja.Value) {
	lodash := lodashObject.ToObject(runtime)
	aliases := map[string]string{
		"chain":         "chain",
		"get":           "get",
		"set":           "set",
		"has":           "has",
		"unset":         "unset",
		"property":      "property",
		"cloneDeep":     "cloneDeep",
		"isEqual":       "isEqual",
		"isObject":      "isObject",
		"isPlainObject": "isPlainObject",
		"isArray":       "isArray",
		"isString":      "isString",
		"isNumber":      "isNumber",
		"isBoolean":     "isBoolean",
		"isFunction":    "isFunction",
		"isNil":         "isNil",
		"isNull":        "isNull",
		"isUndefined":   "isUndefined",
		"isEmpty":       "isEmpty",
		"toArray":       "toArray",
		"keys":          "keys",
		"values":        "values",
		"map":           "map",
		"each":          "each",
		"forEach":       "forEach",
		"filter":        "filter",
		"find":          "find",
		"reduce":        "reduce",
		"includes":      "includes",
		"pick":          "pick",
		"omit":          "omit",
		"merge":         "merge",
		"assign":        "assign",
		"groupBy":       "groupBy",
		"keyBy":         "keyBy",
		"sortBy":        "sortBy",
		"uniq":          "uniq",
		"flatten":       "flatten",
		"flattenDeep":   "flattenDeep",
		"chunk":         "chunk",
		"compact":       "compact",
		"random":        "random",
		"sample":        "sample",
		"sampleSize":    "sampleSize",
		"shuffle":       "shuffle",
		"times":         "times",
		"range":         "range",
		"words":         "words",
		"camelCase":     "camelCase",
		"kebabCase":     "kebabCase",
		"snakeCase":     "snakeCase",
		"startCase":     "startCase",
		"capitalize":    "capitalize",
		"upperFirst":    "upperFirst",
		"lowerFirst":    "lowerFirst",
		"toUpper":       "toUpper",
		"toLower":       "toLower",
		"trim":          "trim",
		"trimStart":     "trimStart",
		"trimEnd":       "trimEnd",
		"pad":           "pad",
		"padStart":      "padStart",
		"padEnd":        "padEnd",
		"repeat":        "repeat",
		"startsWith":    "startsWith",
		"endsWith":      "endsWith",
		"escape":        "escape",
		"unescape":      "unescape",
		"first":         "first",
		"head":          "head",
		"last":          "last",
		"nth":           "nth",
		"take":          "take",
		"takeRight":     "takeRight",
		"drop":          "drop",
		"dropRight":     "dropRight",
		"initial":       "initial",
		"tail":          "tail",
		"difference":    "difference",
		"intersection":  "intersection",
		"union":         "union",
		"without":       "without",
		"uniqBy":        "uniqBy",
		"zip":           "zip",
		"unzip":         "unzip",
		"fromPairs":     "fromPairs",
		"toPairs":       "toPairs",
		"entries":       "entries",
		"findIndex":     "findIndex",
		"findLastIndex": "findLastIndex",
		"some":          "some",
		"every":         "every",
		"reject":        "reject",
		"size":          "size",
		"flatMap":       "flatMap",
		"partition":     "partition",
		"countBy":       "countBy",
		"orderBy":       "orderBy",
		"sum":           "sum",
		"sumBy":         "sumBy",
		"mean":          "mean",
		"max":           "max",
		"min":           "min",
		"maxBy":         "maxBy",
		"minBy":         "minBy",
		"clamp":         "clamp",
		"inRange":       "inRange",
		"clone":         "clone",
		"defaults":      "defaults",
		"invert":        "invert",
		"mapValues":     "mapValues",
		"mapKeys":       "mapKeys",
		"pickBy":        "pickBy",
		"omitBy":        "omitBy",
		"uniqueId":      "uniqueId",
		"toNumber":      "toNumber",
		"toInteger":     "toInteger",
		"toString":      "toString",
		"isDate":        "isDate",
		"isRegExp":      "isRegExp",
		"isInteger":     "isInteger",
		"isFinite":      "isFinite",
		"isNaN":         "isNaN",
		"identity":      "identity",
		"noop":          "noop",
		"constant":      "constant",
	}
	for moduleName, propertyName := range aliases {
		value := lodash.Get(propertyName)
		if goja.IsUndefined(value) {
			continue
		}
		modules["lodash/"+moduleName] = value
		modules["lodash/"+moduleName+".js"] = value
	}
}

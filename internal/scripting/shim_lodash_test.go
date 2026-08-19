// The lodash subset, and the gap that made a real collection fail to run.
//
// Postman bundles the WHOLE of lodash into its sandbox. LiteAPI ships a curated
// subset, which is a reasonable trade — until a collection reaches for a
// function that was left out, and gets "Object has no member 'random'" with no
// file, no line, and no hint that lodash is even involved.
//
// That is not hypothetical. The Restful Booker collection — Postman's own
// long-standing public teaching collection — opens its collection-level
// pre-request script with _.random, so EVERY request in it died before sending.
// The script below is copied from that collection, and it is the reason this
// file exists: the unit tests that follow pin the semantics, but this one pins
// the actual user-visible outcome.
//
// Semantics are pinned against real lodash 4.17, not against what seemed
// reasonable. _.random in particular has a signature that is easy to get subtly
// wrong (one argument means UPPER bound, not lower; a non-integer bound or an
// explicit flag switches it to floating point), and a subtly wrong random
// generator is the kind of defect that never surfaces as a crash — it just
// quietly produces the wrong test data.
package scripting

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// runLodash evaluates source as a pre-request script and returns any error.
// Scripts assert with throw, so a nil error means every assertion held.
func runLodash(t *testing.T, source string) {
	t.Helper()
	item := &types.RequestItem{Name: "lodash", URL: "http://example.test", Method: "GET"}
	if err := RunPreRequestScript(source, item, map[string]string{}, nil); err != nil {
		t.Fatalf("script failed: %v", err)
	}
}

// THE REGRESSION. Verbatim from the Restful Booker collection's collection-level
// pre-request script. Before _.random existed this failed on line 12 with
// "Object has no member 'random'" and no request in the collection could send.
func TestLodashRunsTheRestfulBookerPreRequestScript(t *testing.T) {
	item := &types.RequestItem{Name: "booking", URL: "http://example.test", Method: "POST"}
	vars := map[string]string{}
	script := `
var firstNames = ["Emily","Michael","Jessica","Matthew","Ashley"];
var lastNames = ["Smith","Johnson","Williams","Jones","Brown"];

pm.variables.set("firstname", firstNames[_.random(firstNames.length - 1)]);
pm.variables.set("lastname", lastNames[_.random(lastNames.length - 1)]);
pm.variables.set("totalprice", _.random(50, 250));
pm.variables.set("depositpaid", (_.random(1) === 1));

const moment = require("moment");
var checkin = moment().add("days", _.random(1, 180));
pm.variables.set("checkin", checkin.format("YYYY-MM-DD"));

var checkout = checkin.add("days", _.random(1, 14));
pm.variables.set("checkout", checkout.format("YYYY-MM-DD"));

var needs = ["breakfast", "lunch", "early checkin", "late checkout", null];
pm.variables.set("additionalneeds", needs[_.random(needs.length-1)]);
`
	if err := RunPreRequestScript(script, item, vars, nil); err != nil {
		t.Fatalf("the Restful Booker pre-request script did not run: %v", err)
	}
	// The point of the script is the variables it leaves behind; an empty
	// firstname would mean _.random returned something unusable as an index.
	if strings.TrimSpace(vars["firstname"]) == "" {
		t.Fatalf("firstname was not set: %#v", vars)
	}
	if strings.TrimSpace(vars["checkin"]) == "" {
		t.Fatalf("checkin was not set: %#v", vars)
	}
}

// _.random's argument handling, which is the part worth pinning. Ranges are
// checked over many draws rather than once, because a single draw from a broken
// generator lands in range often enough to pass by luck.
func TestLodashRandomHonoursItsBounds(t *testing.T) {
	runLodash(t, `
for (let i = 0; i < 500; i += 1) {
  // No arguments: an integer 0 or 1.
  const none = _.random();
  if (none !== 0 && none !== 1) throw new Error("_.random() gave " + none);

  // ONE argument is the UPPER bound with a lower bound of 0 — not the lower
  // bound. Getting this backwards yields negative indexes into an array.
  const upper = _.random(5);
  if (!Number.isInteger(upper) || upper < 0 || upper > 5) throw new Error("_.random(5) gave " + upper);

  // Two integers: inclusive on both ends.
  const both = _.random(10, 20);
  if (!Number.isInteger(both) || both < 10 || both > 20) throw new Error("_.random(10,20) gave " + both);

  // A reversed pair is swapped rather than returning NaN.
  const swapped = _.random(20, 10);
  if (!Number.isInteger(swapped) || swapped < 10 || swapped > 20) throw new Error("_.random(20,10) gave " + swapped);
}
`)
}

func TestLodashRandomReturnsFloatsWhenAsked(t *testing.T) {
	runLodash(t, `
// An explicit floating flag.
let sawFraction = false;
for (let i = 0; i < 200; i += 1) {
  const value = _.random(0, 5, true);
  if (value < 0 || value > 5) throw new Error("out of range: " + value);
  if (!Number.isInteger(value)) sawFraction = true;
}
if (!sawFraction) throw new Error("_.random(0,5,true) never returned a fraction");

// A non-integer bound implies floating point without the flag.
sawFraction = false;
for (let i = 0; i < 200; i += 1) {
  const value = _.random(1.2, 5.2);
  if (value < 1.2 || value > 5.2) throw new Error("out of range: " + value);
  if (!Number.isInteger(value)) sawFraction = true;
}
if (!sawFraction) throw new Error("_.random(1.2,5.2) never returned a fraction");

// _.random(true) is the floating form of the no-argument call.
sawFraction = false;
for (let i = 0; i < 200; i += 1) {
  const value = _.random(true);
  if (value < 0 || value > 1) throw new Error("out of range: " + value);
  if (!Number.isInteger(value)) sawFraction = true;
}
if (!sawFraction) throw new Error("_.random(true) never returned a fraction");
`)
}

// The random-selection helpers that travel with _.random in test-data scripts.
func TestLodashSampleShuffleAndTimes(t *testing.T) {
	runLodash(t, `
const source = ["a", "b", "c", "d", "e"];

for (let i = 0; i < 100; i += 1) {
  if (!source.includes(_.sample(source))) throw new Error("_.sample left the collection");
}
if (_.sample([]) !== undefined) throw new Error("_.sample of an empty collection must be undefined");

const picked = _.sampleSize(source, 3);
if (picked.length !== 3) throw new Error("_.sampleSize length: " + picked.length);
if (new Set(picked).size !== 3) throw new Error("_.sampleSize repeated an element");
// Asking for more than exists yields everything, not padding.
if (_.sampleSize(source, 99).length !== source.length) throw new Error("_.sampleSize over-sampled");
if (_.sampleSize(source).length !== 1) throw new Error("_.sampleSize defaults to one");

const shuffled = _.shuffle(source);
if (shuffled.length !== source.length) throw new Error("_.shuffle changed the length");
if (shuffled.slice().sort().join() !== source.slice().sort().join()) throw new Error("_.shuffle changed the members");
if (source.join() !== "a,b,c,d,e") throw new Error("_.shuffle mutated its input");

if (_.times(4, (n) => n * 2).join() !== "0,2,4,6") throw new Error("_.times: " + _.times(4, (n) => n * 2));
if (_.times(3).join() !== "0,1,2") throw new Error("_.times defaults to identity");
if (_.times(0, () => 1).length !== 0) throw new Error("_.times(0) must be empty");
`)
}

func TestLodashRange(t *testing.T) {
	runLodash(t, `
if (_.range(4).join() !== "0,1,2,3") throw new Error("_.range(4)");
if (_.range(1, 5).join() !== "1,2,3,4") throw new Error("_.range(1,5)");
if (_.range(0, 20, 5).join() !== "0,5,10,15") throw new Error("_.range step");
// A negative step counts down; a zero step would otherwise spin forever.
if (_.range(4, 0, -1).join() !== "4,3,2,1") throw new Error("_.range negative step");
if (_.range(0) .length !== 0) throw new Error("_.range(0) must be empty");
`)
}

// The string-case family. Every one of these is built on _.words, so a defect in
// the splitter shows up in all of them at once.
func TestLodashStringCasing(t *testing.T) {
	runLodash(t, `
function eq(actual, expected, label) {
  if (actual !== expected) throw new Error(label + ": got " + JSON.stringify(actual) + " want " + JSON.stringify(expected));
}

eq(_.words("fred, barney, & pebbles").join("|"), "fred|barney|pebbles", "words punctuation");
eq(_.words("helloWorldAgain").join("|"), "hello|World|Again", "words camelCase");
eq(_.words("XMLHttpRequest").join("|"), "XML|Http|Request", "words acronym");

eq(_.camelCase("Foo Bar"), "fooBar", "camelCase spaces");
eq(_.camelCase("--foo-bar--"), "fooBar", "camelCase dashes");
eq(_.camelCase("__FOO_BAR__"), "fooBar", "camelCase shouting");

eq(_.kebabCase("Foo Bar"), "foo-bar", "kebabCase");
eq(_.kebabCase("fooBar"), "foo-bar", "kebabCase camel");
eq(_.snakeCase("Foo Bar"), "foo_bar", "snakeCase");
eq(_.snakeCase("fooBar"), "foo_bar", "snakeCase camel");
eq(_.startCase("--foo-bar--"), "Foo Bar", "startCase");
eq(_.startCase("fooBar"), "Foo Bar", "startCase camel");

eq(_.capitalize("FRED"), "Fred", "capitalize lowers the tail");
eq(_.upperFirst("fred"), "Fred", "upperFirst");
eq(_.upperFirst("FRED"), "FRED", "upperFirst leaves the tail alone");
eq(_.lowerFirst("Fred"), "fred", "lowerFirst");
eq(_.toUpper("--foo-bar--"), "--FOO-BAR--", "toUpper");
eq(_.toLower("--FOO-BAR--"), "--foo-bar--", "toLower");
`)
}

func TestLodashStringPaddingAndTrimming(t *testing.T) {
	runLodash(t, `
function eq(actual, expected, label) {
  if (actual !== expected) throw new Error(label + ": got " + JSON.stringify(actual) + " want " + JSON.stringify(expected));
}

eq(_.trim("  abc  "), "abc", "trim");
eq(_.trim("-_-abc-_-", "_-"), "abc", "trim with chars");
eq(_.trimStart("  abc  "), "abc  ", "trimStart");
eq(_.trimEnd("  abc  "), "  abc", "trimEnd");

eq(_.padStart("9", 3, "0"), "009", "padStart");
eq(_.padEnd("9", 3, "0"), "900", "padEnd");
// A string already at or over the length is returned unchanged.
eq(_.padStart("abcd", 3, "0"), "abcd", "padStart no-op");
eq(_.pad("abc", 7, "_"), "__abc__", "pad both sides");
eq(_.repeat("ab", 3), "ababab", "repeat");
eq(_.repeat("ab", 0), "", "repeat zero");

if (!_.startsWith("abc", "a")) throw new Error("startsWith");
if (_.startsWith("abc", "b")) throw new Error("startsWith false");
if (!_.startsWith("abc", "b", 1)) throw new Error("startsWith offset");
if (!_.endsWith("abc", "c")) throw new Error("endsWith");

eq(_.escape("fred, barney, & pebbles"), "fred, barney, &amp; pebbles", "escape");
eq(_.unescape("fred, barney, &amp; pebbles"), "fred, barney, & pebbles", "unescape");
eq(_.escape("<a href=\"x\">"), "&lt;a href=&quot;x&quot;&gt;", "escape markup");
`)
}

func TestLodashArrayHelpers(t *testing.T) {
	runLodash(t, `
function eq(actual, expected, label) {
  if (String(actual) !== String(expected)) throw new Error(label + ": got " + JSON.stringify(actual) + " want " + JSON.stringify(expected));
}

eq(_.first([1,2,3]), 1, "first");
eq(_.head([1,2,3]), 1, "head");
if (_.first([]) !== undefined) throw new Error("first of empty");
eq(_.last([1,2,3]), 3, "last");
if (_.last([]) !== undefined) throw new Error("last of empty");
eq(_.nth([1,2,3], 1), 2, "nth");
eq(_.nth([1,2,3], -1), 3, "nth from the end");

eq(_.take([1,2,3,4], 2), [1,2], "take");
eq(_.take([1,2,3,4]), [1], "take defaults to one");
eq(_.takeRight([1,2,3,4], 2), [3,4], "takeRight");
eq(_.drop([1,2,3,4], 2), [3,4], "drop");
eq(_.dropRight([1,2,3,4], 2), [1,2], "dropRight");
eq(_.initial([1,2,3]), [1,2], "initial");
eq(_.tail([1,2,3]), [2,3], "tail");

eq(_.difference([2,1,3], [2,3]), [1], "difference");
eq(_.intersection([2,1,3], [3,2]), [2,3], "intersection");
eq(_.union([2,1], [1,3]), [2,1,3], "union");
eq(_.without([1,2,1,3], 1), [2,3], "without");
eq(_.uniqBy([{n:1},{n:1},{n:2}], "n").length, 2, "uniqBy");
eq(_.zip([1,2],["a","b"]).map((p) => p.join(":")), ["1:a","2:b"], "zip");
eq(_.fromPairs([["a",1],["b",2]]).b, 2, "fromPairs");
eq(_.toPairs({a:1}).map((p) => p.join(":")), ["a:1"], "toPairs");

eq(_.findIndex([1,2,3], (n) => n === 2), 1, "findIndex");
eq(_.findIndex([1,2,3], (n) => n === 9), -1, "findIndex missing");
if (!_.some([1,2], (n) => n === 2)) throw new Error("some");
if (_.some([1,2], (n) => n === 9)) throw new Error("some false");
if (!_.every([1,2], (n) => n > 0)) throw new Error("every");
eq(_.size([1,2,3]), 3, "size of an array");
eq(_.size("abc"), 3, "size of a string");
eq(_.size({a:1,b:2}), 2, "size of an object");
eq(_.reject([1,2,3,4], (n) => n % 2), [2,4], "reject");
eq(_.flatMap([1,2], (n) => [n, n]), [1,1,2,2], "flatMap");

const parts = _.partition([1,2,3,4], (n) => n % 2);
eq(parts[0], [1,3], "partition truthy");
eq(parts[1], [2,4], "partition falsy");
eq(JSON.stringify(_.countBy([6.1, 4.2, 6.3], Math.floor)), JSON.stringify({"6":2,"4":1}), "countBy");

const ordered = _.orderBy([{a:2},{a:1},{a:3}], ["a"], ["desc"]).map((o) => o.a);
eq(ordered, [3,2,1], "orderBy desc");
eq(_.orderBy([{a:2},{a:1}], ["a"]).map((o) => o.a), [1,2], "orderBy default asc");
`)
}

func TestLodashNumberAndObjectHelpers(t *testing.T) {
	runLodash(t, `
function eq(actual, expected, label) {
  if (String(actual) !== String(expected)) throw new Error(label + ": got " + JSON.stringify(actual) + " want " + JSON.stringify(expected));
}

eq(_.sum([1,2,3]), 6, "sum");
eq(_.sumBy([{n:1},{n:2}], "n"), 3, "sumBy");
eq(_.max([1,5,2]), 5, "max");
eq(_.min([1,5,2]), 1, "min");
if (_.max([]) !== undefined) throw new Error("max of empty must be undefined");
eq(_.maxBy([{n:1},{n:5}], "n").n, 5, "maxBy");
eq(_.minBy([{n:1},{n:5}], "n").n, 1, "minBy");
eq(_.mean([1,2,3]), 2, "mean");
eq(_.clamp(15, 0, 10), 10, "clamp high");
eq(_.clamp(-5, 0, 10), 0, "clamp low");
if (!_.inRange(3, 1, 5)) throw new Error("inRange");
if (_.inRange(7, 1, 5)) throw new Error("inRange false");

eq(JSON.stringify(_.invert({a:"x"})), JSON.stringify({x:"a"}), "invert");
eq(JSON.stringify(_.mapValues({a:1}, (n) => n + 1)), JSON.stringify({a:2}), "mapValues");
eq(JSON.stringify(_.mapKeys({a:1}, (v, k) => k + "!")), JSON.stringify({"a!":1}), "mapKeys");
eq(JSON.stringify(_.pickBy({a:1,b:null}, Boolean)), JSON.stringify({a:1}), "pickBy");
eq(JSON.stringify(_.omitBy({a:1,b:null}, (v) => v === null)), JSON.stringify({a:1}), "omitBy");
eq(JSON.stringify(_.defaults({a:1}, {a:9,b:2})), JSON.stringify({a:1,b:2}), "defaults keeps existing");

// _.clone is shallow: the nested object is the SAME reference, which is the
// property that distinguishes it from cloneDeep.
const nested = {inner:{v:1}};
const shallow = _.clone(nested);
if (shallow === nested) throw new Error("clone returned the same object");
if (shallow.inner !== nested.inner) throw new Error("clone was not shallow");
if (_.cloneDeep(nested).inner === nested.inner) throw new Error("cloneDeep was not deep");

if (!_.isDate(new Date())) throw new Error("isDate");
if (_.isDate("2020-01-01")) throw new Error("isDate false");
if (!_.isRegExp(/x/)) throw new Error("isRegExp");
if (!_.isInteger(3)) throw new Error("isInteger");
if (_.isInteger(3.5)) throw new Error("isInteger false");
if (!_.isFinite(3)) throw new Error("isFinite");
if (_.isFinite(Infinity)) throw new Error("isFinite false");

eq(_.toNumber("3.5"), 3.5, "toNumber");
eq(_.toInteger("3.9"), 3, "toInteger");
eq(_.identity(7), 7, "identity");
if (_.noop() !== undefined) throw new Error("noop must return undefined");
eq(_.constant(7)(), 7, "constant");
// uniqueId must not repeat within a run.
if (_.uniqueId() === _.uniqueId()) throw new Error("uniqueId repeated");
if (_.uniqueId("x").indexOf("x") !== 0) throw new Error("uniqueId prefix");
`)
}

// Every function reachable as _.name must also be reachable as
// require("lodash/name"), which is how the subpath modules are documented.
func TestLodashSubpathModulesCoverTheNewFunctions(t *testing.T) {
	runLodash(t, `
for (const name of ["random", "sample", "shuffle", "times", "range", "camelCase", "capitalize", "first", "last", "orderBy", "sum", "clamp", "mapValues"]) {
  const loaded = require("lodash/" + name);
  if (typeof loaded !== "function") throw new Error("lodash/" + name + " did not load");
}
`)
}

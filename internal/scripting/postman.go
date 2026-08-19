package scripting

// The Postman-compatibility surface.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

// installPostmanScriptAPI puts a live `pm` object beside `bru` (US-039).
//
// pm.test and pm.expect are BOUND TO the existing globals rather than
// reimplemented. That is the whole point: two independent test registries would
// drift, and a `pm.test` whose failures did not reach the same TestResults
// slice would report a green run while its assertions failed. Binding also
// means every later fix to `test` or `expect` applies to both surfaces for
// free.
//
// This is the core only. pm.environment / pm.collectionVariables / pm.globals
// (US-040), pm.request / pm.response (US-041), pm.sendRequest / pm.cookies
// (US-042) and pm.iterationData / pm.vault (US-043) land separately.
func installPostmanScriptAPI(runtime *goja.Runtime, bruObject, reqObject, resObject *goja.Object, scriptVars *VariableContext, reqState *RequestState, response types.Response, item types.RequestItem, meta ScriptRuntimeMeta) {
	pm := runtime.NewObject()
	_ = pm.Set("test", runtime.Get("test"))
	_ = pm.Set("expect", runtime.Get("expect"))
	installPostmanVariableScopes(runtime, pm, bruObject, scriptVars)
	installPostmanRequestAPI(runtime, pm, reqObject, item)
	installPostmanSideEffects(runtime, pm, bruObject)
	installPostmanIterationData(runtime, pm, scriptVars)
	installPostmanVisualizer(runtime, pm, reqState)
	installPostmanVault(runtime, pm, bruObject)
	// pm.response is deliberately ABSENT during the pre-request phase, because
	// there is no response yet. The alternative — exposing the zero types.Response —
	// would answer pm.response.code with 0 and pm.response.text() with "",
	// which reads as a server that returned nothing rather than as a script
	// asking for something that does not exist. Left undefined, the script
	// throws where Postman also throws.
	if meta.TimelinePhase != "pre-request" {
		installPostmanResponseAPI(runtime, pm, resObject, response)
	}

	info := runtime.NewObject()
	_ = info.Set("requestName", item.Name)
	_ = info.Set("requestId", item.ID)
	_ = info.Set("eventName", PostmanEventName(meta.TimelinePhase))
	// Postman's pm.info.iteration is 0-BASED while everything user-facing here
	// counts from 1. Converting at this single boundary is what keeps a script
	// copied out of Postman working; leaking the 1-based value would make every
	// `if (pm.info.iteration === 0)` guard silently never fire.
	_ = info.Set("iteration", max(meta.IterationIndex-1, 0))
	// Outside a collection run Postman reports a single iteration, not zero.
	_ = info.Set("iterationCount", max(meta.IterationCount, 1))
	_ = pm.Set("info", info)

	_ = runtime.Set("pm", pm)
}

// installPostmanVariableScopes gives each Postman scope its OWN storage
// (US-040).
//
// This is the story's whole point, and it is the bug in the import translator
// that US-044 will demote: that table maps pm.environment.set,
// pm.collectionVariables.set, pm.globals.set AND pm.variables.set all onto
// bru.setVar, so four distinct scopes collapse into the runtime scope. Nothing
// errors. A script that writes pm.globals.set("token", ...) and later reads
// pm.environment.get("token") gets its value back, so the collapse looks like
// it works — right up to the point where a second environment, or a second
// collection, is supposed to see a different value and does not.
//
// Every method delegates to the bru function for that exact scope. Delegating
// rather than reimplementing is what makes drift impossible: the dirty-tracking
// that decides whether an environment gets written back to disk lives in those
// closures, and a parallel implementation that forgot to set EnvDirty would
// update variables that silently never persist.
func installPostmanVariableScopes(runtime *goja.Runtime, pm, bruObject *goja.Object, scriptVars *VariableContext) {
	// replaceIn is scope-independent in Postman: it interpolates against the
	// whole resolved chain regardless of which scope object it is called on.
	replaceIn := bruObject.Get("interpolate")

	scope := func(get, set, unset, has, clear, toObject string) *goja.Object {
		object := runtime.NewObject()
		_ = object.Set("get", bruObject.Get(get))
		_ = object.Set("set", bruObject.Get(set))
		_ = object.Set("unset", bruObject.Get(unset))
		_ = object.Set("has", bruObject.Get(has))
		_ = object.Set("clear", bruObject.Get(clear))
		_ = object.Set("toObject", bruObject.Get(toObject))
		_ = object.Set("replaceIn", replaceIn)
		return object
	}

	_ = pm.Set("environment", scope(
		"getEnvVar", "setEnvVar", "deleteEnvVar",
		"hasEnvVar", "deleteAllEnvVars", "getAllEnvVars"))
	_ = pm.Set("collectionVariables", scope(
		"getCollectionVar", "setCollectionVar", "deleteCollectionVar",
		"hasCollectionVar", "deleteAllCollectionVars", "getAllCollectionVars"))
	_ = pm.Set("globals", scope(
		"getGlobalEnvVar", "setGlobalEnvVar", "deleteGlobalEnvVar",
		"hasGlobalEnvVar", "deleteAllGlobalEnvVars", "getAllGlobalEnvVars"))

	// pm.variables is the odd one out and deliberately not built by scope():
	// it READS the fully resolved chain across every scope, but WRITES to the
	// runtime scope. Giving it its own storage would make a value set through
	// it invisible to {{var}} interpolation, and routing its reads to one scope
	// would silently miss variables defined anywhere else.
	variables := runtime.NewObject()
	_ = variables.Set("get", func(name string) goja.Value {
		if scriptVars == nil {
			return goja.Undefined()
		}
		if value, ok := scriptVars.CombinedInterface()[name]; ok {
			return runtime.ToValue(value)
		}
		return goja.Undefined()
	})
	_ = variables.Set("has", func(name string) bool {
		if scriptVars == nil {
			return false
		}
		_, ok := scriptVars.CombinedInterface()[name]
		return ok
	})
	_ = variables.Set("toObject", func() map[string]interface{} {
		if scriptVars == nil {
			return map[string]interface{}{}
		}
		return scriptVars.CombinedInterface()
	})
	_ = variables.Set("set", bruObject.Get("setVar"))
	_ = variables.Set("unset", bruObject.Get("deleteVar"))
	_ = variables.Set("replaceIn", replaceIn)
	_ = pm.Set("variables", variables)
}

// PostmanEventName maps this codebase's timeline phases onto the two event
// names Postman scripts test against. Both post-response phases are "test":
// Postman has no separate post-response event, and reporting one would break
// the `pm.info.eventName === "test"` guard that scripts actually write.
func PostmanEventName(timelinePhase string) string {
	if timelinePhase == "pre-request" {
		return "prerequest"
	}
	return "test"
}

func installPostmanVisualizer(runtime *goja.Runtime, pm *goja.Object, reqState *RequestState) {
	visualizer := runtime.NewObject()
	_ = visualizer.Set("set", func(call goja.FunctionCall) goja.Value {
		template := call.Argument(0).String()
		data := ""
		if len(call.Arguments) > 1 {
			value := call.Argument(1)
			if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
				if encoded, err := json.Marshal(value.Export()); err == nil {
					data = string(encoded)
				}
			}
		}
		payload, err := NormalizeVisualizerPayload(types.VisualizerPayload{Template: template, Data: data})
		if err != nil {
			// Thrown rather than silently dropped: a template over the limit
			// that vanished would leave an empty Visualizer tab and no reason.
			panic(runtime.NewGoError(err))
		}
		if reqState != nil {
			reqState.visualizer = &payload
		}
		return goja.Undefined()
	})
	_ = pm.Set("visualizer", visualizer)
}

// installPostmanIterationData exposes the runner data file's current row
// (US-043), reading the Data scope US-046 populates.
//
// Reads the Data scope specifically, NOT the resolved chain. pm.iterationData
// means "what the data file said for this iteration", and answering it from
// the merged chain would report environment and collection variables as
// iteration data — so a script guarding on
// `pm.iterationData.has("userId")` would take the data-driven branch on a run
// with no data file at all.
func installPostmanIterationData(runtime *goja.Runtime, pm *goja.Object, scriptVars *VariableContext) {
	data := func() map[string]interface{} {
		if scriptVars == nil || scriptVars.Data == nil {
			return map[string]interface{}{}
		}
		return scriptVars.Data
	}

	iterationData := runtime.NewObject()
	_ = iterationData.Set("get", func(name string) goja.Value {
		if value, ok := data()[name]; ok {
			return runtime.ToValue(value)
		}
		return goja.Undefined()
	})
	_ = iterationData.Set("has", func(name string) bool {
		_, ok := data()[name]
		return ok
	})
	_ = iterationData.Set("toObject", func() map[string]interface{} {
		// A copy: handing out the live scope would let a script mutate the
		// iteration's variables through a method Postman defines as a read.
		out := map[string]interface{}{}
		for key, value := range data() {
			out[key] = value
		}
		return out
	})
	_ = pm.Set("iterationData", iterationData)
}

// installPostmanVault maps pm.vault onto the existing secrets layer (US-043).
//
// Async on purpose: Postman's vault API returns promises, and scripts are
// written as `await pm.vault.get("key")`. Returning a bare value would work
// under await by accident and then break on `.then()`.
//
// set and unset deliberately REJECT rather than writing. The runtime has no way
// to tell a secret variable from an ordinary one — VariableContext holds
// plain maps with no Secret flag — so a pm.vault.set would land the value in
// the environment scope as an ordinary variable and get written to disk in the
// clear. A script storing a token would believe it was vaulted while leaking
// it. An explicit rejection is the honest answer; silently downgrading a
// secret to plaintext is not.
func installPostmanVault(runtime *goja.Runtime, pm, bruObject *goja.Object) {
	getSecret, hasGetSecret := goja.AssertFunction(bruObject.Get("getSecretVar"))

	resolved := func(value goja.Value) goja.Value {
		promise, resolve, _ := runtime.NewPromise()
		_ = resolve(value)
		return runtime.ToValue(promise)
	}
	rejected := func(message string) goja.Value {
		promise, _, reject := runtime.NewPromise()
		_ = reject(runtime.NewGoError(errors.New(message)))
		return runtime.ToValue(promise)
	}

	vault := runtime.NewObject()
	_ = vault.Set("get", func(name string) goja.Value {
		if !hasGetSecret {
			return resolved(goja.Undefined())
		}
		value, err := getSecret(goja.Undefined(), runtime.ToValue(name))
		if err != nil {
			return rejected(err.Error())
		}
		return resolved(value)
	})
	_ = vault.Set("has", func(name string) goja.Value {
		if !hasGetSecret {
			return resolved(runtime.ToValue(false))
		}
		value, err := getSecret(goja.Undefined(), runtime.ToValue(name))
		if err != nil {
			return rejected(err.Error())
		}
		present := value != nil && !goja.IsUndefined(value) && !goja.IsNull(value)
		return resolved(runtime.ToValue(present))
	})
	const writeMessage = "pm.vault.%s is not supported: this build cannot mark a value as secret from a script, and writing it would store the value in the environment in plain text"
	_ = vault.Set("set", func(string, goja.Value) goja.Value {
		return rejected(fmt.Sprintf(writeMessage, "set"))
	})
	_ = vault.Set("unset", func(string) goja.Value {
		return rejected(fmt.Sprintf(writeMessage, "unset"))
	})
	_ = pm.Set("vault", vault)
}

// installPostmanSideEffects wires pm's outward-facing calls to bru's (US-042).
//
// All three are pure delegation, deliberately. Each one has real machinery
// behind it that is easy to overlook when reimplementing: bru.sendRequest
// records a timeline entry for the scripted request and enforces the recursion
// depth limit, bru.cookies is bound to THIS request's jar and URL, and
// bru.setNextRequest feeds the runner's control flow. A parallel pm
// implementation would produce requests missing from the timeline, cookies
// from the wrong host, and a setNextRequest the runner never sees — none of
// which fails visibly.
func installPostmanSideEffects(runtime *goja.Runtime, pm, bruObject *goja.Object) {
	// Postman's pm.sendRequest(req, callback(err, response)) is already the
	// signature bru.sendRequest implements, so this is a straight alias rather
	// than an adapter.
	_ = pm.Set("sendRequest", bruObject.Get("sendRequest"))
	_ = pm.Set("cookies", bruObject.Get("cookies"))

	// pm.execution is where modern Postman puts run control.
	execution := runtime.NewObject()
	_ = execution.Set("setNextRequest", bruObject.Get("setNextRequest"))
	if runner := bruObject.Get("runner"); runner != nil && !goja.IsUndefined(runner) {
		if runnerObject := runner.ToObject(runtime); runnerObject != nil {
			_ = execution.Set("skipRequest", runnerObject.Get("skipRequest"))
		}
	}
	_ = pm.Set("execution", execution)
}

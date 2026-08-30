package scripting

// Which API the script called.
//
// bru.sendRequest and pm.sendRequest do the same job and used to be the same
// function object — aliased deliberately, so that a second implementation could
// not drift and quietly stop recording timeline entries. The aliasing was right
// about the machinery and wrong about the contract: the two APIs do not
// describe a request the same way.
//
//	bru.sendRequest({ data: { a: 1 } })                    // axios — JSON
//	pm.sendRequest({ body: { mode: 'urlencoded', … } })    // Postman — a DEFINITION
//
// The dialect used to be guessed from the payload's shape. The script already
// says which API it called, so it is read from the call site instead: two
// registrations over one factory, differing in a single argument. Everything
// that made the alias worth keeping — the timeline entry, the callback
// protocol, the shared HTTP client — is still written once, here.
//
// The consequence, and the reason TestPmSideEffectsAreTheSameObjectsAsBru no
// longer asserts it: `pm.sendRequest === bru.sendRequest` is now false. That
// identity was never part of either API's contract, and keeping it while the
// two behave differently would state something untrue about them. The test now
// asserts what the identity was standing in for — that both record a timeline
// entry.

import (
	"fmt"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

type scriptSendDialect int

const (
	// Bruno's shape, which is axios's: `data` or `body` is a payload to encode.
	// A body definition means nothing here, and an object that happens to carry
	// a `mode` field is JSON, the same as any other object.
	dialectBruno scriptSendDialect = iota
	// Postman's shape: `body` may be a request-body definition with a `mode`.
	dialectPostman
)

func makeScriptSendRequest(runtime *goja.Runtime, dialect scriptSendDialect, vars map[string]string, item types.RequestItem, meta ScriptRuntimeMeta) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		var responseValue, errorValue goja.Value
		var timelineEntry *types.TimelineItem
		var err error
		// The script's 2s budget is for RUNNING JavaScript. A token endpoint
		// that takes three seconds to answer is the server being slow, and
		// counting that against the budget killed the single most common thing
		// a pre-request script does.
		scriptBlockingCall(runtime, func() {
			responseValue, errorValue, timelineEntry, err = scriptSendRequest(runtime, dialect, call.Argument(0), vars)
		})
		if timelineEntry != nil && meta.RecordTimeline != nil {
			entry := *timelineEntry
			entry.ID = scalar.NewID("timeline")
			entry.Kind = "scripted-request"
			entry.Source = "sendRequest"
			entry.Phase = scalar.FirstNonEmpty(meta.TimelinePhase, "pre-request")
			entry.RequestID = item.ID
			entry.SourceFile = TimelineSourceFileForItem(meta.CollectionPath, item)
			if entry.Message == "" {
				statusLabel := entry.StatusText
				if entry.Status > 0 {
					statusLabel = fmt.Sprintf("%d", entry.Status)
				}
				entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
			}
			meta.RecordTimeline(entry)
		}
		callback, hasCallback := goja.AssertFunction(call.Argument(1))
		if hasCallback {
			if err != nil {
				_, callbackErr := callback(goja.Undefined(), runtime.NewGoError(err), goja.Null())
				if callbackErr != nil {
					panic(callbackErr)
				}
				return goja.Undefined()
			}
			if errorValue != nil {
				_, callbackErr := callback(goja.Undefined(), errorValue, goja.Null())
				if callbackErr != nil {
					panic(callbackErr)
				}
				return goja.Undefined()
			}
			_, callbackErr := callback(goja.Undefined(), goja.Null(), responseValue)
			if callbackErr != nil {
				panic(callbackErr)
			}
			return responseValue
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if errorValue != nil {
			panic(errorValue)
		}
		return responseValue
	}
}

// What does building a script runtime actually cost?
//
// US-019 proposes pooling runtimes and collapsing four into one. Both are
// correctness-sensitive: every shim closes over per-request state, so a pooled
// runtime that missed a reset would leak the previous request's url, headers or
// env into the next script. Before taking that risk it is worth knowing what is
// being bought, because US-018 already banked the large win here with the
// compiled-program cache.
package scripting

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func benchItem() types.RequestItem {
	return types.RequestItem{
		Name:   "bench",
		Method: "POST",
		URL:    "https://example.test/things/{{id}}",
		Headers: []types.KeyValue{
			{Name: "Content-Type", Value: "application/json", Enabled: true},
			{Name: "Authorization", Value: "Bearer {{token}}", Enabled: true},
		},
		Params: []types.KeyValue{{Name: "page", Value: "2", Enabled: true}},
		Body:   types.RequestBody{Mode: "json", JSON: `{"a":1}`},
	}
}

func benchVars() map[string]string {
	return map[string]string{"id": "42", "token": "abc", "host": "https://example.test"}
}

func BenchmarkNewScriptRuntime(b *testing.B) {
	item, vars := benchItem(), benchVars()
	response := types.Response{Status: 200, Body: `{"ok":true}`, Headers: map[string]string{"Content-Type": "application/json"}}
	jar := NewScriptCookieJar(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewScriptRuntimeWithMeta(item, response, vars, nil, nil, jar, ScriptRuntimeMeta{})
	}
}

// The shape US-019 would remove: three runtimes built back to back for the
// post-response phase, which is what one shared runtime would replace.
func BenchmarkPostResponsePhaseRuntimes(b *testing.B) {
	item, vars := benchItem(), benchVars()
	response := types.Response{Status: 200, Body: `{"ok":true}`, Headers: map[string]string{"Content-Type": "application/json"}}
	jar := NewScriptCookieJar(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewScriptRuntimeWithMeta(item, response, vars, nil, nil, jar, ScriptRuntimeMeta{})
		NewScriptRuntimeWithMeta(item, response, vars, nil, nil, jar, ScriptRuntimeMeta{})
		var results []types.TestResult
		NewScriptRuntimeWithMeta(item, response, vars, &results, nil, jar, ScriptRuntimeMeta{})
	}
}

// For scale: running a trivial script through a runtime, which is what the
// program cache from US-018 already optimises.
func BenchmarkRunTrivialScript(b *testing.B) {
	item, vars := benchItem(), benchVars()
	response := types.Response{Status: 200}
	jar := NewScriptCookieJar(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime, _, _, _ := NewScriptRuntimeWithMeta(item, response, vars, nil, nil, jar, ScriptRuntimeMeta{})
		_ = runGojaScript(runtime, "bru.setVar('x', 1)", "developer")
	}
}

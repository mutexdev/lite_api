package scripting

import (
	"testing"

	"github.com/dop251/goja"
)

// Where does the 8,482 allocations actually go? If bare goja.New() dominates,
// sharing one runtime across the post-response phase wins regardless of what the
// shims do. If the SHIMS dominate, sharing the runtime only helps if the shims
// are shared too — and sharing the shims is exactly the semantics change that
// needs a decision. This measurement decides which conversation to have.
func BenchmarkBareGojaRuntime(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = goja.New()
	}
}

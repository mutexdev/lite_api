package transport

import (
	"net/http"
	"testing"
)

// Three functions resolve "an arbitrary RoundTripper to the *http.Transport a
// clone would be taken from", and two of them say in comments that they match
// the third: CloneHTTPTransport (transport.go), Spec.resolvedSource and Source
// (cache.go). None of the three was covered.
//
// They do NOT all agree, and the difference is deliberate — see
// TestSourceReturnsNilWhereResolvedSourceSubstitutes. What matters is that the
// difference stays absorbed.

type notATransport struct{}

func (notATransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

// resolvedSource's result is dereferenced — cache.go clones it — so returning
// nil would panic rather than degrade.
func TestResolvedSourceIsNeverNil(t *testing.T) {
	for name, spec := range map[string]Spec{
		"no source":  {},
		"nil source": {Source: nil},
		"a source":   {Source: &http.Transport{}},
	} {
		got := spec.resolvedSource()
		if got == nil {
			t.Errorf("%s: resolvedSource returned nil, which cache.go would clone", name)
			continue
		}
		// The clone is the operation that would panic; do it here so the test
		// exercises what the caller actually does.
		if got.Clone() == nil {
			t.Errorf("%s: cloning the resolved source produced nil", name)
		}
	}
}

func TestResolvedSourcePrefersAnExplicitSource(t *testing.T) {
	explicit := &http.Transport{}
	if got := (Spec{Source: explicit}).resolvedSource(); got != explicit {
		t.Error("an explicitly set Source was not used")
	}
}

// THE PROPERTY THE CACHE DEPENDS ON. The cache key embeds the resolved source's
// POINTER (`field(fmt.Sprintf("%p", spec.resolvedSource()))`), so two lookups
// for the same spec must resolve to the same pointer. A fallback that allocated
// a fresh &http.Transport{} each time would give every lookup a different key,
// and the transport cache would never hit — it would silently become a
// transport factory, which is the opposite of why it exists.
func TestResolvedSourceIsStableAcrossCalls(t *testing.T) {
	spec := Spec{}
	first := spec.resolvedSource()
	for i := 0; i < 8; i++ {
		if got := spec.resolvedSource(); got != first {
			t.Fatalf("call %d resolved to a different pointer; every cache lookup would miss", i+2)
		}
	}
	// And the same for an equal-but-separate spec, which is the real case: the
	// caller rebuilds the spec per request.
	if (Spec{}).resolvedSource() != first {
		t.Error("two equal specs resolved to different pointers")
	}
}

// Source and resolvedSource deliberately differ when there is nothing to fall
// back to: Source reports nil, and resolvedSource substitutes an empty
// transport. That is safe only because Source's single caller stores the result
// in Spec.Source, where resolvedSource picks the nil up again.
//
// Recorded rather than "fixed": making Source return an empty transport would
// put a non-nil, freshly allocated pointer into every such Spec, and by the
// test above that is exactly what breaks the cache key.
func TestSourceReturnsNilWhereResolvedSourceSubstitutes(t *testing.T) {
	if got := Source(&http.Transport{}); got == nil {
		t.Error("Source dropped an explicit *http.Transport")
	}
	// With a base that is not an *http.Transport, both consult
	// http.DefaultTransport, which normally is one.
	fallback, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not an *http.Transport here")
	}
	if got := Source(notATransport{}); got != fallback {
		t.Errorf("Source(non-transport) = %p, want http.DefaultTransport %p", got, fallback)
	}
	if got := (Spec{}).resolvedSource(); got != fallback {
		t.Errorf("resolvedSource with no Source = %p, want http.DefaultTransport %p", got, fallback)
	}
}

// CloneHTTPTransport is the one the other two say they match. It must return a
// COPY, not the shared default — mutating http.DefaultTransport would change
// every request the process makes, including ones this app did not configure.
func TestCloneHTTPTransportReturnsACopyNotTheSharedDefault(t *testing.T) {
	fallback, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not an *http.Transport here")
	}
	for name, base := range map[string]http.RoundTripper{
		"nil base":         nil,
		"non-transport":    notATransport{},
		"a real transport": &http.Transport{},
	} {
		got := CloneHTTPTransport(base)
		if got == nil {
			t.Errorf("%s: CloneHTTPTransport returned nil", name)
			continue
		}
		if got == fallback {
			t.Errorf("%s: returned http.DefaultTransport itself; mutating it would affect the whole process", name)
		}
	}
}

// All three resolve an explicit *http.Transport to that same transport — the
// agreement the comments claim, stated where it holds.
func TestAllThreeAgreeOnAnExplicitTransport(t *testing.T) {
	explicit := &http.Transport{MaxIdleConns: 7}

	if got := Source(explicit); got != explicit {
		t.Error("Source did not resolve to the explicit transport")
	}
	if got := (Spec{Source: explicit}).resolvedSource(); got != explicit {
		t.Error("resolvedSource did not resolve to the explicit transport")
	}
	// CloneHTTPTransport resolves to it and then clones, so compare a field
	// that only the explicit transport carries.
	if got := CloneHTTPTransport(explicit); got.MaxIdleConns != explicit.MaxIdleConns {
		t.Errorf("CloneHTTPTransport cloned the wrong source: MaxIdleConns %d, want %d",
			got.MaxIdleConns, explicit.MaxIdleConns)
	}
}

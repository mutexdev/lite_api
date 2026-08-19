package grpcexec

import (
	"context"
	"testing"
)

// grpcReflectionServiceName resolves a possibly-short service name to its fully
// qualified form by asking the server to list its services.
//
// Its first branch is the one worth pinning and the only one reachable without
// a server: A NAME THAT IS ALREADY QUALIFIED RETURNS IMMEDIATELY, before any
// reflection call is made.
//
// That is not an optimisation. SERVER REFLECTION IS OPTIONAL — plenty of
// production gRPC servers have it disabled — and this short-circuit is what
// lets those servers be called at all, provided the user supplies the full
// name. Route a qualified name through the reflection request and every such
// server becomes unreachable, with an error about reflection rather than about
// the service the user asked for.
//
// A nil connection is passed deliberately: if the short-circuit ever stops
// short-circuiting, this test panics rather than quietly making a call.
func TestAQualifiedServiceNameSkipsReflectionEntirely(t *testing.T) {
	for _, name := range []string{
		"helloworld.Greeter",
		"grpc.health.v1.Health",
		"a.b.c.d.Service",
	} {
		got, err := grpcReflectionServiceName(context.Background(), nil, name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
		}
		if got != name {
			t.Errorf("%q resolved to %q; a qualified name must be returned as given", name, got)
		}
	}
}

// The test above would pass vacuously if the dot check were removed and the nil
// connection happened not to be dereferenced. It is not: an unqualified name
// with a nil connection panics, which is what proves the qualified case took a
// different path.
func TestAnUnqualifiedNameNeedsTheConnection(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("an unqualified name did not reach the reflection call; " +
				"the qualified-name test above may be passing for the wrong reason")
		}
	}()
	_, _ = grpcReflectionServiceName(context.Background(), nil, "Greeter")
}

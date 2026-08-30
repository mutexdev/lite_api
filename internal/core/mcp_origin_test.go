package core

// Origin identity — §1.1. Every case here is a bypass if it goes the other way,
// so each one names the bypass rather than restating the code.

import "testing"

func mustOrigin(t *testing.T, rawURL string) Origin {
	t.Helper()
	origin, ok := OriginOfURL(rawURL)
	if !ok {
		t.Fatalf("OriginOfURL(%q) did not resolve to an origin", rawURL)
	}
	return origin
}

// The default port must be MATERIALIZED, not left implicit: otherwise
// https://api.example.com and https://api.example.com:443 are two origins, an
// approval for the spelling in the definition would not cover the spelling on
// the wire, and every such request would prompt forever.
func TestOriginDefaultPortsAreMaterialized(t *testing.T) {
	cases := []struct {
		rawURL string
		want   Origin
	}{
		{"http://api.example.com/v1/things", Origin{Scheme: "http", Host: "api.example.com", Port: 80}},
		{"http://api.example.com:80/v1", Origin{Scheme: "http", Host: "api.example.com", Port: 80}},
		{"https://api.example.com/v1", Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
		{"https://api.example.com:443", Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
		// ws/wss normalize onto their HTTP equivalents: a handshake IS an HTTP
		// request, so a request and its websocket sibling to the same server
		// must be one origin.
		{"ws://api.example.com/socket", Origin{Scheme: "http", Host: "api.example.com", Port: 80}},
		{"wss://api.example.com/socket", Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
		{"wss://api.example.com:8443/socket", Origin{Scheme: "https", Host: "api.example.com", Port: 8443}},
		// Case in the host is not identity.
		{"HTTPS://API.Example.COM/v1", Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
	}
	for _, testCase := range cases {
		got := mustOrigin(t, testCase.rawURL)
		if got != testCase.want {
			t.Errorf("OriginOfURL(%q) = %+v, want %+v", testCase.rawURL, got, testCase.want)
		}
	}
}

// §1.4(9): localhost is not special. :3000 and :8080 are unrelated services with
// unrelated owners, and an approval for one must not carry to the other. This is
// the fix the shipped host guard (which drops the port) cannot express.
func TestOriginDistinguishesPortsOnTheSameHost(t *testing.T) {
	three := mustOrigin(t, "http://localhost:3000/api")
	eight := mustOrigin(t, "http://localhost:8080/api")
	if three == eight {
		t.Fatalf("http://localhost:3000 and http://localhost:8080 collapsed to one origin (%+v)", three)
	}
	if three.String() != "http://localhost:3000" {
		t.Errorf("String() = %q, want %q", three.String(), "http://localhost:3000")
	}
}

// A scheme downgrade is a different destination: on http anyone on the path
// reads the credential. An approval for the https origin must not authorize the
// http one.
func TestOriginTreatsAnHTTPSDowngradeAsADifferentOrigin(t *testing.T) {
	secure := mustOrigin(t, "https://api.example.com")
	plain := mustOrigin(t, "http://api.example.com")
	if secure == plain {
		t.Fatal("https:// and http:// on the same host collapsed to one origin")
	}
	// And they must not collide through the port either: 443 written explicitly
	// on http is still http.
	explicit := mustOrigin(t, "http://api.example.com:443")
	if explicit == secure {
		t.Fatal("http://host:443 compared equal to https://host")
	}
}

// Every spelling of one IPv6 address must reduce to one origin, or an approval
// for the spelling the user read would not cover the spelling the engine dials.
func TestOriginNormalizesIPv6Literals(t *testing.T) {
	canonical := mustOrigin(t, "http://[::1]:8080/")
	for _, spelling := range []string{
		"http://[::0001]:8080/",
		"http://[0:0:0:0:0:0:0:1]:8080/",
		"http://[0000:0000:0000:0000:0000:0000:0000:0001]:8080/",
	} {
		if got := mustOrigin(t, spelling); got != canonical {
			t.Errorf("OriginOfURL(%q) = %+v, want %+v", spelling, got, canonical)
		}
	}
	// Stored unbracketed, rendered bracketed: the stored form is canonical and
	// the rendered form is dialable.
	if canonical.Host != "::1" {
		t.Errorf("Host = %q, want %q", canonical.Host, "::1")
	}
	if canonical.String() != "http://[::1]:8080" {
		t.Errorf("String() = %q, want %q", canonical.String(), "http://[::1]:8080")
	}
	// The default port applies to IPv6 exactly as it does to a name.
	if got := mustOrigin(t, "https://[::1]/"); got.Port != 443 {
		t.Errorf("https://[::1]/ resolved to port %d, want 443", got.Port)
	}
}

// A destination the boundary cannot identify must not become an origin. Every
// one of these would otherwise be authorized as some other origin's equal — the
// schemeless case most dangerously, since guessing a scheme means guessing
// between two different destinations.
func TestOriginRejectsWhatItCannotIdentify(t *testing.T) {
	for _, rawURL := range []string{
		"",
		"   ",
		"api.example.com/v1",              // no scheme: the send path rejects it too
		"{{baseUrl}}/things",              // unresolved template
		"file:///etc/passwd",              // not an HTTP-family destination
		"unix:///var/run/thing.sock",      // gRPC-shaped, refused by §4.7 anyway
		"grpc://api.example.com:50051",    // gRPC origins come from §4.7, not here
		"https:///v1",                     // no host
		"http://api.example.com:notaport", // unparseable port
	} {
		if origin, ok := OriginOfURL(rawURL); ok {
			t.Errorf("OriginOfURL(%q) resolved to %+v; an unidentifiable destination must not become an origin", rawURL, origin)
		}
	}
}

// §1.1's gRPC rule: the effective port when unspecified is 443 for plaintext AND
// TLS, which is NOT the HTTP 80/443 scheme default. §4.7 owns the grammar that
// produces these; what this pins is that the shared constructor represents them
// and keeps them distinct from the HTTP defaults.
func TestOriginRepresentsGRPCEffectivePorts(t *testing.T) {
	// Plaintext gRPC on the gRPC default port: scheme http, port 443.
	plaintext, ok := newOrigin("http", "api.example.com", 443)
	if !ok {
		t.Fatal("newOrigin refused a plaintext gRPC origin")
	}
	if plaintext.String() != "http://api.example.com:443" {
		t.Errorf("String() = %q, want %q", plaintext.String(), "http://api.example.com:443")
	}
	// It must NOT equal the same host under the HTTP default port. If it did,
	// approving the collection's ordinary http://api.example.com request would
	// silently authorize the gRPC channel.
	if httpDefault := mustOrigin(t, "http://api.example.com"); plaintext == httpDefault {
		t.Fatal("a gRPC origin on port 443 compared equal to the http:// default-port origin")
	}
	// TLS gRPC is scheme https, and on 443 it IS the same origin as an https
	// request to that host — the same TCP authority with the same TLS posture.
	tlsGRPC, ok := newOrigin("https", "api.example.com", 443)
	if !ok {
		t.Fatal("newOrigin refused a TLS gRPC origin")
	}
	if tlsGRPC != mustOrigin(t, "https://api.example.com") {
		t.Error("a TLS gRPC origin on 443 did not match the https:// default-port origin for the same host")
	}
	// IPv6 goes through the same normalization from the gRPC side.
	bracketed, ok := newOrigin("http", "[::0001]", 443)
	if !ok {
		t.Fatal("newOrigin refused a bracketed IPv6 gRPC origin")
	}
	if bracketed.Host != "::1" || bracketed.String() != "http://[::1]:443" {
		t.Errorf("newOrigin bracketed IPv6 = %+v (%q)", bracketed, bracketed.String())
	}
}

func TestNewOriginRejectsUnusableParts(t *testing.T) {
	cases := []struct {
		scheme string
		host   string
		port   int
	}{
		{"grpc", "api.example.com", 443}, // schemes are normalized before this point
		{"http", "", 443},
		{"http", "api.example.com", 0},     // no implicit default: §4.7 computes its own
		{"http", "api.example.com", 70000}, // out of range
		{"http", "api.example.com", -1},
	}
	for _, testCase := range cases {
		if origin, ok := newOrigin(testCase.scheme, testCase.host, testCase.port); ok {
			t.Errorf("newOrigin(%q, %q, %d) = %+v, want refusal", testCase.scheme, testCase.host, testCase.port, origin)
		}
	}
}

// The zero Origin is never a destination: rendering it as text and asking
// whether it is valid must both say so, because an egress whose origin could not
// be determined is an egress that was not checked.
func TestZeroOriginIsNotADestination(t *testing.T) {
	var zero Origin
	if zero.valid() {
		t.Error("the zero Origin reported itself valid")
	}
	if zero.String() != "" {
		t.Errorf("zero Origin rendered as %q, want the empty string", zero.String())
	}
	if label := originLabel(zero); label != "an unresolved destination" {
		t.Errorf("originLabel(zero) = %q", label)
	}
}

// kindClass decides which approvals apply. A token-class approval must never
// authorize a request-class egress, and an unknown kind must map to NO class —
// defaulting it to "request" would let a kind added later inherit every approval
// the user has already given.
func TestKindClassSeparatesTheApprovalClasses(t *testing.T) {
	cases := map[egressKind]string{
		egressKindMain:               kindClassRequest,
		egressKindRedirect:           kindClassRequest,
		egressKindScript:             kindClassRequest,
		egressKindScriptDNS:          kindClassRequest,
		egressKindToken:              kindClassToken,
		egressKindAWS:                kindClassAWS,
		egressKindProxy:              kindClassProxy,
		egressKind("newly-invented"): "",
		egressKind(""):               "",
	}
	for kind, want := range cases {
		if got := kindClass(kind); got != want {
			t.Errorf("kindClass(%q) = %q, want %q", kind, got, want)
		}
	}
}

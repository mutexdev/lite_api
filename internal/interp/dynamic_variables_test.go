package interp

// US-050 — tests for Postman dynamic variables.
//
// Two of the story's criteria are the ones a plausible implementation fails,
// and both fail in the direction that looks like success:
//
//   * "Each resolves per occurrence, not once per request." A strings.ReplaceAll
//     implementation gives every {{$randomInt}} in a body the SAME number. A
//     user generating ten records gets ten identical ones and no error.
//   * "Unknown $ variables are left literal rather than resolving to empty." A
//     typo silently emptied looks like a deliberate blank field.

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestDynamicVariablesResolvePerOccurrence(t *testing.T) {
	// Twenty occurrences: with a single generated value substituted everywhere,
	// the probability of all twenty legitimately matching is ~1001^-19.
	input := strings.Repeat("{{$randomInt}},", 20)
	out := InterpolateDynamicVariables(input)

	parts := strings.Split(strings.TrimSuffix(out, ","), ",")
	if len(parts) != 20 {
		t.Fatalf("expected 20 values, got %d", len(parts))
	}
	distinct := map[string]struct{}{}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			t.Fatalf("value %q is not an integer", p)
		}
		distinct[p] = struct{}{}
	}
	if len(distinct) == 1 {
		t.Error("all 20 occurrences produced the same value — resolution is once per string, not per occurrence")
	}
}

func TestUnknownDynamicVariablesAreLeftLiteral(t *testing.T) {
	// A typo must survive to the wire so the user can see it. Emptying it makes
	// a blank field look deliberate.
	for _, input := range []string{
		"{{$randomEmial}}",
		"{{$notAThing}}",
		"prefix {{$nope}} suffix",
	} {
		if got := InterpolateDynamicVariables(input); got != input {
			t.Errorf("unknown variable was rewritten: %q -> %q", input, got)
		}
	}
}

func TestOrdinaryVariablesAreUntouched(t *testing.T) {
	// The pattern must only match $-prefixed names, or normal interpolation
	// would be broken by this feature.
	input := "{{host}}/{{path}} {{ $spaced }} {{}}"
	if got := InterpolateDynamicVariables(input); got != input {
		t.Errorf("non-dynamic placeholders were altered: %q", got)
	}
}

func TestDynamicVariableShapes(t *testing.T) {
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	cases := []struct {
		name  string
		check func(t *testing.T, value string)
	}{
		{"guid", func(t *testing.T, v string) {
			if !uuidRe.MatchString(v) {
				t.Errorf("not a v4 UUID: %q", v)
			}
		}},
		{"randomUUID", func(t *testing.T, v string) {
			if !uuidRe.MatchString(v) {
				t.Errorf("not a v4 UUID: %q", v)
			}
		}},
		{"randomInt", func(t *testing.T, v string) {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 1000 {
				t.Errorf("outside Postman's 0-1000 range: %q", v)
			}
		}},
		{"randomIP", func(t *testing.T, v string) {
			if ip := net.ParseIP(v); ip == nil || ip.To4() == nil {
				t.Errorf("not a valid IPv4 address: %q", v)
			}
		}},
		{"randomEmail", func(t *testing.T, v string) {
			if strings.Count(v, "@") != 1 || strings.HasPrefix(v, "@") || strings.HasSuffix(v, "@") {
				t.Errorf("not a plausible email: %q", v)
			}
			if !strings.Contains(strings.SplitN(v, "@", 2)[1], ".") {
				t.Errorf("email domain has no dot: %q", v)
			}
		}},
		{"randomUrl", func(t *testing.T, v string) {
			if !strings.HasPrefix(v, "https://") || !strings.Contains(v, ".") {
				t.Errorf("not a plausible URL: %q", v)
			}
		}},
		{"randomBoolean", func(t *testing.T, v string) {
			if v != "true" && v != "false" {
				t.Errorf("not a boolean literal: %q", v)
			}
		}},
		{"randomPassword", func(t *testing.T, v string) {
			if len(v) != 12 {
				t.Errorf("password length %d, want 12", len(v))
			}
		}},
		{"timestamp", func(t *testing.T, v string) {
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				t.Errorf("not a unix timestamp: %q", v)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Several draws: a shape assertion that only ever sees one value can
			// pass on a generator that is right once and wrong the rest of the time.
			for range 25 {
				value, ok := resolveDynamicVariable(tc.name)
				if !ok {
					t.Fatalf("%s is not recognised", tc.name)
				}
				tc.check(t, value)
			}
		})
	}
}

// TestEveryStoryNamedVariableResolves walks the exact list in US-050 so a
// missing one is a named failure rather than a silent gap.
func TestEveryStoryNamedVariableResolves(t *testing.T) {
	required := []string{
		"guid", "randomUUID", "randomInt", "randomFirstName", "randomLastName",
		"randomFullName", "randomEmail", "randomIP", "randomPhoneNumber",
		"randomCity", "randomCountry", "randomStreetAddress", "randomCompanyName",
		"randomJobTitle", "randomUserName", "randomPassword", "randomUrl",
		"randomDomainName", "randomColor", "randomBoolean",
		"timestamp", "isoTimestamp",
	}
	for _, name := range required {
		value, ok := resolveDynamicVariable(name)
		if !ok {
			t.Errorf("$%s is not implemented", name)
			continue
		}
		if strings.TrimSpace(value) == "" {
			t.Errorf("$%s resolved to an empty value", name)
		}
	}
}

// TestDynamicVariablesResolveThroughInterpolate proves the wiring, not just the
// generator: interpolate is what the URL, params, headers and body all go
// through, so resolving there is what makes the story's "works everywhere"
// criterion true.
func TestDynamicVariablesResolveThroughInterpolate(t *testing.T) {
	vars := map[string]string{"host": "https://api.example"}
	got := Interpolate("{{host}}/users/{{$randomInt}}?id={{$guid}}", vars)

	if !strings.HasPrefix(got, "https://api.example/users/") {
		t.Fatalf("ordinary variable did not resolve: %q", got)
	}
	if strings.Contains(got, "{{$") {
		t.Errorf("a dynamic variable survived interpolation: %q", got)
	}
	// The two dynamic values must be different kinds of thing, which also
	// confirms they were resolved independently rather than by one pass.
	rest := strings.TrimPrefix(got, "https://api.example/users/")
	parts := strings.SplitN(rest, "?id=", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected shape: %q", got)
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		t.Errorf("randomInt did not produce an integer: %q", parts[0])
	}
	if len(parts[1]) != 36 {
		t.Errorf("guid did not produce a UUID: %q", parts[1])
	}
}

// TestUnknownDynamicVariablesSurviveInterpolate — the literal-passthrough rule
// has to hold through the real entry point too, not just the helper.
func TestUnknownDynamicVariablesSurviveInterpolate(t *testing.T) {
	got := Interpolate("{{$definitelyNotReal}}", map[string]string{})
	if got != "{{$definitelyNotReal}}" {
		t.Errorf("unknown dynamic variable was altered by interpolate: %q", got)
	}
}

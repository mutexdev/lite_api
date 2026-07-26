// Reading OAuth2 additional parameters back out of a saved request.
//
// Coverage found parseYAMLOAuth2AdditionalGroup and its inner parser at 0%.
// These load the extra headers, query parameters and body fields a user attached
// to an OAuth2 flow — the ones a provider requires and the spec cannot know:
// audience, resource, tenant, prompt.
//
// The failure is silent in the way file loading always is. A parameter that
// loads with the wrong placement is sent in the wrong part of the token request,
// and the provider answers with a generic invalid_request that says nothing
// about which field moved. One that loads disabled is simply not sent.
package yamlstore

import (
	"testing"

	"LiteAPI/internal/types"
)

func TestOAuth2AdditionalParamsReadNamePlacementAndFlags(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{"name": "audience", "value": "https://api.test", "sendIn": "headers"},
		map[string]interface{}{"name": "resource", "value": "r1", "secret": true},
		map[string]interface{}{"name": "off", "value": "x", "enabled": false},
	}

	got := parseYAMLOAuth2AdditionalParams(raw, "body")

	if len(got) != 3 {
		t.Fatalf("got %d params, want 3: %+v", len(got), got)
	}
	if got[0].Name != "audience" || got[0].Value != "https://api.test" {
		t.Errorf("first param = %+v", got[0])
	}
	if got[0].SendIn != types.NormalizeOAuth2AdditionalPlacement("headers") {
		t.Errorf("explicit sendIn was not honoured: %q", got[0].SendIn)
	}
	// The fallback applies only where the entry did not say.
	if got[1].SendIn != types.NormalizeOAuth2AdditionalPlacement("body") {
		t.Errorf("fallback placement not applied: %q", got[1].SendIn)
	}
	if !got[1].Secret {
		t.Error("secret flag was lost — the value would render unmasked")
	}
	if got[2].Enabled {
		t.Error("a param saved disabled loaded enabled; it would be sent when the user switched it off")
	}
}

// A row with no explicit enabled/disabled key must default to ENABLED. Defaulting
// the other way silently stops sending every parameter written before the flag
// existed.
func TestOAuth2AdditionalParamsDefaultToEnabled(t *testing.T) {
	got := parseYAMLOAuth2AdditionalParams([]interface{}{
		map[string]interface{}{"name": "audience", "value": "v"},
	}, "body")
	if len(got) != 1 || !got[0].Enabled {
		t.Fatalf("a row with no enabled key must load enabled: %+v", got)
	}
}

func TestOAuth2AdditionalParamsSkipUnnamedAndMalformedRows(t *testing.T) {
	got := parseYAMLOAuth2AdditionalParams([]interface{}{
		"not a map",
		map[string]interface{}{"value": "no name"},
		map[string]interface{}{"name": "   "},
		map[string]interface{}{"name": "kept", "value": "v"},
	}, "body")
	if len(got) != 1 || got[0].Name != "kept" {
		t.Fatalf("got %+v; only the named row is usable", got)
	}
}

// The grouped form is how the file actually stores them: three named buckets,
// each implying its own placement. A bucket read with the wrong placement sends
// a header as a query parameter.
func TestOAuth2AdditionalGroupAssignsPlacementPerBucket(t *testing.T) {
	raw := map[string]interface{}{
		"headers":     []interface{}{map[string]interface{}{"name": "X-Tenant", "value": "acme"}},
		"queryparams": []interface{}{map[string]interface{}{"name": "audience", "value": "a"}},
		"body":        []interface{}{map[string]interface{}{"name": "resource", "value": "r"}},
	}

	got := parseYAMLOAuth2AdditionalGroup(raw)

	if len(got) != 3 {
		t.Fatalf("got %d params, want 3: %+v", len(got), got)
	}
	byName := map[string]types.OAuth2AdditionalParam{}
	for _, p := range got {
		byName[p.Name] = p
	}
	for name, wantPlacement := range map[string]string{
		"X-Tenant": "headers",
		"audience": "queryparams",
		"resource": "body",
	} {
		want := types.NormalizeOAuth2AdditionalPlacement(wantPlacement)
		if byName[name].SendIn != want {
			t.Errorf("%s loaded with placement %q, want %q — it would be sent in the wrong part of the token request",
				name, byName[name].SendIn, want)
		}
	}
}

// The bucket keys have alternate spellings in files written by other tools.
func TestOAuth2AdditionalGroupAcceptsAlternateBucketNames(t *testing.T) {
	got := parseYAMLOAuth2AdditionalGroup(map[string]interface{}{
		"header": []interface{}{map[string]interface{}{"name": "H"}},
		"query":  []interface{}{map[string]interface{}{"name": "Q"}},
		"form":   []interface{}{map[string]interface{}{"name": "F"}},
	})
	if len(got) != 3 {
		t.Fatalf("alternate bucket spellings were not read: %+v", got)
	}
}

// A bare list, with no buckets, falls back to body placement rather than being
// dropped.
func TestOAuth2AdditionalGroupAcceptsABareList(t *testing.T) {
	got := parseYAMLOAuth2AdditionalGroup([]interface{}{
		map[string]interface{}{"name": "resource", "value": "r"},
	})
	if len(got) != 1 || got[0].SendIn != types.NormalizeOAuth2AdditionalPlacement("body") {
		t.Fatalf("a bare list must load as body params: %+v", got)
	}
}

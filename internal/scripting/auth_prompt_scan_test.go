// Finding {{?prompt}} variables in a request's AUTH block.
//
// scanAuthPromptVariables was at 16.7%. It is fifty-odd hand-written scanText
// lines, one per credential field, and the failure mode is a field somebody
// forgot. That field's prompt is never raised, so the request is signed with the
// literal "{{?client_secret}}" as the secret — which fails authentication with a
// message about bad credentials, pointing nowhere near the missing prompt.
//
// A list of examples cannot guard a list of fields: the test would name the same
// fields the implementation already names, and a field added to BOTH lists by
// the same person on the same day is the case that goes wrong. So the main test
// walks the struct by reflection. Add a credential field to types.AuthConfig
// without a scan line and this fails, without anyone having to remember.
package scripting

import (
	"fmt"
	"reflect"
	"testing"

	"LiteAPI/internal/types"
)

// fillStrings puts a distinct marker in every string field, walking nested
// structs. Slices are left alone: those have their own tests below, and their
// element types are not part of "did someone forget a field".
func fillStrings(value reflect.Value, path string, markers map[string]string) {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		name := path + value.Type().Field(i).Name
		switch field.Kind() {
		case reflect.String:
			marker := fmt.Sprintf("{{?p%d_%s}}", len(markers), name)
			field.SetString(marker)
			markers[marker] = name
		case reflect.Struct:
			fillStrings(field, name+".", markers)
		}
	}
}

func TestAuthPromptScanReachesEveryCredentialField(t *testing.T) {
	var auth types.AuthConfig
	markers := map[string]string{}
	fillStrings(reflect.ValueOf(&auth).Elem(), "", markers)
	if len(markers) < 40 {
		t.Fatalf("only %d string fields found; the walk is not reaching the nested auth structs", len(markers))
	}

	seen := map[string]bool{}
	scanAuthPromptVariables(auth,
		func(text string) { seen[text] = true },
		func(rows []types.KeyValue) {
			for _, row := range rows {
				seen[row.Name], seen[row.Value] = true, true
			}
		})

	for marker, field := range markers {
		if !seen[marker] {
			t.Errorf("auth field %s is never scanned: a prompt there is never raised, and the request is signed with the literal placeholder", field)
		}
	}
}

func TestAuthPromptScanCoversOAuth2KeyValueParams(t *testing.T) {
	var seen []string
	scanAuthPromptVariables(
		types.AuthConfig{OAuth2: types.OAuth2Auth{
			AdditionalParams: []types.KeyValue{{Name: "audience", Value: "{{?aud}}", Enabled: true}},
		}},
		func(string) {},
		func(rows []types.KeyValue) {
			for _, row := range rows {
				seen = append(seen, row.Value)
			}
		})
	if len(seen) == 0 || seen[0] != "{{?aud}}" {
		t.Errorf("oauth2 additional params were not scanned, got %v", seen)
	}
}

// The three per-phase parameter lists (authorize, token, refresh) are separate
// fields scanned by separate calls, so one can be omitted while the others work.
func TestAuthPromptScanCoversAllThreeOAuth2ParamPhases(t *testing.T) {
	param := func(marker string) []types.OAuth2AdditionalParam {
		return []types.OAuth2AdditionalParam{{Name: "k", Value: marker, SendIn: "body", Enabled: true}}
	}
	seen := map[string]bool{}
	scanAuthPromptVariables(
		types.AuthConfig{OAuth2: types.OAuth2Auth{
			AuthorizationAdditionalParams: param("{{?authz}}"),
			TokenAdditionalParams:         param("{{?token}}"),
			RefreshAdditionalParams:       param("{{?refresh}}"),
		}},
		func(text string) { seen[text] = true },
		func([]types.KeyValue) {})

	for _, phase := range []string{"{{?authz}}", "{{?token}}", "{{?refresh}}"} {
		if !seen[phase] {
			t.Errorf("%s params were not scanned; that phase alone would send the literal placeholder", phase)
		}
	}
}

func TestOAuth2ParamScanCoversNameValueAndSendIn(t *testing.T) {
	seen := map[string]bool{}
	scanOAuth2PromptParams(
		[]types.OAuth2AdditionalParam{{Name: "{{?n}}", Value: "{{?v}}", SendIn: "{{?s}}", Enabled: true}},
		func(text string) { seen[text] = true })
	for _, want := range []string{"{{?n}}", "{{?v}}", "{{?s}}"} {
		if !seen[want] {
			t.Errorf("%s was not scanned", want)
		}
	}
}

// A disabled parameter is not sent, so prompting for it asks the user to supply
// a value that goes nowhere.
func TestOAuth2ParamScanSkipsDisabledParams(t *testing.T) {
	seen := map[string]bool{}
	scanOAuth2PromptParams(
		[]types.OAuth2AdditionalParam{{Name: "off", Value: "{{?unused}}", Enabled: false}},
		func(text string) { seen[text] = true })
	if seen["{{?unused}}"] {
		t.Error("a disabled oauth2 param was scanned; the user would be prompted for a value that is never sent")
	}
}

func TestOAuth2ParamScanHandlesNoParams(t *testing.T) {
	scanOAuth2PromptParams(nil, func(string) { t.Error("nil params scanned something") })
}

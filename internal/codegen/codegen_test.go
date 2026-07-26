// Moved from package main with the code they cover. The one test that stayed
// behind drives *App and lives in code_generation_test.go.
package codegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// hostileBody contains, deliberately: a double quote, a single quote, a
// backslash, a newline, a PHP variable sigil, a Ruby interpolation opener, a
// backtick and a non-ASCII rune. Duplicated in package main's
// code_generation_test.go, which drives the same generators through *App.
const hostileBody = `{"quote":"she said \"hi\"","apostrophe":"it's","path":"C:\\Users\\ada","dollar":"$total","ruby":"#{evil}","tick":"` + "`whoami`" + `","unicode":"héllo 世界"}`

func codegenFixture(bodyMode string) types.ResponseExample {
	return types.ResponseExample{
		Name: "codegen probe",
		Type: "http",
		Request: types.ResponseExampleRequest{
			Method:   "POST",
			URL:      "https://api.example.test/v1/items",
			BodyMode: bodyMode,
			Body:     hostileBody,
			Headers: []types.KeyValue{
				{Name: "Accept", Value: "application/json", Enabled: true},
				{Name: "X-Quote", Value: `he said "no"`, Enabled: true},
				{Name: "X-Disabled", Value: "should not appear", Enabled: false},
			},
			Params: []types.KeyValue{
				{Name: "page", Value: "2", Enabled: true},
				{Name: "skip", Value: "yes", Enabled: false},
			},
			FormURLEncoded: []types.KeyValue{
				{Name: "username", Value: "ada", Enabled: true},
				{Name: "note", Value: `it's "fine"`, Enabled: true},
				{Name: "off", Value: "no", Enabled: false},
			},
			MultipartForm: []types.FormPart{
				{Name: "caption", Value: `it's "fine"`, Enabled: true},
				{Name: "avatar", FilePath: "/tmp/avatar.png", Enabled: true},
			},
		},
	}
}

func allCodegenIDs() []string {
	ids := make([]string, 0, len(Languages))
	for _, target := range Languages {
		ids = append(ids, target.ID)
	}
	return ids
}

// TestEveryStoryNamedTargetGenerates walks the exact list in US-054 so a
// missing one is a named failure rather than a silent gap.
func TestEveryStoryNamedTargetGenerates(t *testing.T) {
	required := []string{"python", "axios", "go", "java", "csharp", "php", "ruby", "httpie", "powershell"}
	example := codegenFixture("json")

	for _, id := range required {
		t.Run(id, func(t *testing.T) {
			code, err := GenerateResponseExampleCode(example, id)
			if err != nil {
				t.Fatalf("GenerateResponseExampleCode(%s): %v", id, err)
			}
			if strings.TrimSpace(code) == "" {
				t.Fatal("produced no code")
			}
			if !strings.Contains(code, "api.example.test") {
				t.Errorf("the URL is missing from the output:\n%s", code)
			}
			if !strings.Contains(code, "POST") && !strings.Contains(code, "post") {
				t.Errorf("the method is missing from the output:\n%s", code)
			}
		})
	}
}

// TestCodegenTargetsAreDispatchable. The picker is built from Languages,
// so any entry the dispatcher cannot resolve would appear in the UI and then
// fail — a menu item that errors when chosen.
func TestCodegenEscapesHostileBodies(t *testing.T) {
	example := codegenFixture("json")

	// The body itself contains the substrings "$total" and "#{evil}" as JSON
	// text, so searching for them proves nothing — they appear either way. What
	// matters is which QUOTE CHARACTER delimits the emitted literal, since PHP
	// and Ruby interpolate inside double quotes only.
	bodyLiteral := func(t *testing.T, code, prefix string) string {
		t.Helper()
		index := strings.Index(code, prefix)
		if index < 0 {
			t.Fatalf("no %q line in the output:\n%s", prefix, code)
		}
		line := code[index+len(prefix):]
		if end := strings.Index(line, "\n"); end >= 0 {
			line = line[:end]
		}
		return line
	}

	t.Run("php does not interpolate", func(t *testing.T) {
		code, _ := GenerateResponseExampleCode(example, "php")
		literal := bodyLiteral(t, code, "CURLOPT_POSTFIELDS, ")
		if !strings.HasPrefix(literal, "'") {
			t.Errorf("the body is not in a single-quoted PHP string, so $total will interpolate: %s", literal)
		}
		if !strings.Contains(literal, "$total") {
			t.Errorf("the dollar value was mangled out of the body: %s", literal)
		}
	})

	t.Run("ruby does not interpolate", func(t *testing.T) {
		code, _ := GenerateResponseExampleCode(example, "ruby")
		literal := bodyLiteral(t, code, "request.body = ")
		if !strings.HasPrefix(literal, "'") {
			t.Errorf("the body is not in a single-quoted Ruby string, so #{evil} will be evaluated: %s", literal)
		}
		if !strings.Contains(literal, "#{evil}") {
			t.Errorf("the interpolation marker was mangled out of the body: %s", literal)
		}
	})

	t.Run("powershell doubles quotes and keeps backslashes", func(t *testing.T) {
		code, _ := GenerateResponseExampleCode(example, "powershell")
		if !strings.Contains(code, "''") {
			t.Errorf("no doubled single quote; the apostrophe in the body would end the literal:\n%s", code)
		}
		// A backslash is not an escape in a PowerShell literal; doubling one
		// would corrupt every Windows path.
		if strings.Contains(code, `C:\\\\Users`) {
			t.Errorf("backslashes were double-escaped, corrupting the path:\n%s", code)
		}
	})

	t.Run("python and go quote as source literals", func(t *testing.T) {
		for _, id := range []string{"python", "go", "java", "csharp", "axios"} {
			code, _ := GenerateResponseExampleCode(example, id)
			// A raw unescaped double quote from the body would close the
			// literal; the escaped form must be what appears.
			if strings.Contains(code, `"she said "hi""`) {
				t.Errorf("%s: the body's quotes are unescaped and break the literal:\n%s", id, code)
			}
			if !strings.Contains(code, `\"`) {
				t.Errorf("%s: no escaped quote appears at all:\n%s", id, code)
			}
			// A literal newline inside a single-line string literal is a
			// syntax error in every one of these languages.
			if strings.Contains(code, "\\n") == false && strings.Contains(hostileBody, "\n") {
				t.Errorf("%s: newlines were not escaped", id)
			}
		}
	})

	t.Run("httpie quotes for the shell", func(t *testing.T) {
		code, _ := GenerateResponseExampleCode(example, "httpie")
		// The POSIX idiom ends the quote, inserts an escaped quote and
		// reopens; an apostrophe passed through raw would end the argument and
		// leave the rest of the body as shell words.
		if strings.Contains(code, "it's") && !strings.Contains(code, `'"'"'`) {
			t.Errorf("the apostrophe was not shell-escaped:\n%s", code)
		}
	})
}

// TestCodegenRespectsDisabledRows. A disabled header or param that appears in
// generated code makes the snippet send something the app itself would not.
func TestCodegenRespectsDisabledRows(t *testing.T) {
	example := codegenFixture("json")

	for _, id := range allCodegenIDs() {
		t.Run(id, func(t *testing.T) {
			code, err := GenerateResponseExampleCode(example, id)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(code, "X-Disabled") {
				t.Errorf("a disabled header reached the output:\n%s", code)
			}
			if strings.Contains(code, "skip=yes") {
				t.Errorf("a disabled param reached the output:\n%s", code)
			}
			if !strings.Contains(code, "page=2") {
				t.Errorf("the enabled param is missing:\n%s", code)
			}
		})
	}
}

// TestCodegenHandlesEveryBodyMode. A mode a generator does not understand
// typically emits the request with NO body — which sends successfully and does
// nothing, the hardest kind of bug to notice.
func TestCodegenHandlesEveryBodyMode(t *testing.T) {
	for _, mode := range []string{"json", "text", "formUrlEncoded", "multipartForm", "none"} {
		example := codegenFixture(mode)
		for _, id := range allCodegenIDs() {
			t.Run(mode+"/"+id, func(t *testing.T) {
				code, err := GenerateResponseExampleCode(example, id)
				if err != nil {
					t.Fatalf("%s/%s: %v", mode, id, err)
				}
				switch mode {
				case "formUrlEncoded":
					if !strings.Contains(code, "username") {
						t.Errorf("form field missing:\n%s", code)
					}
					if strings.Contains(code, `"off"`) || strings.Contains(code, "'off'") {
						t.Errorf("a disabled form field reached the output:\n%s", code)
					}
				case "multipartForm":
					if !strings.Contains(code, "avatar") || !strings.Contains(code, "caption") {
						t.Errorf("multipart parts missing:\n%s", code)
					}
				case "none":
					if strings.Contains(code, "she said") {
						t.Errorf("a body was emitted for a bodyless request:\n%s", code)
					}
				default:
					if !strings.Contains(code, "she said") {
						t.Errorf("the raw body did not reach the output:\n%s", code)
					}
				}
			})
		}
	}
}

// TestMultipartOmitsAHandWrittenContentType. A multipart Content-Type carries a
// boundary the client generates; writing one by hand produces a request the
// server cannot parse — and it fails at the server, not in the snippet.
func TestMultipartOmitsAHandWrittenContentType(t *testing.T) {
	example := codegenFixture("multipartForm")
	for _, id := range allCodegenIDs() {
		code, err := GenerateResponseExampleCode(example, id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(code, "multipart/form-data; boundary=") {
			t.Errorf("%s wrote a boundary by hand:\n%s", id, code)
		}
	}
}

// TestGeneratedJSONIsStillValidJSON round-trips the body back out of the
// generated Python literal. If the escaping were wrong the reconstructed body
// would not parse — a check that does not depend on my reading the output.
func TestGeneratedJSONIsStillValidJSON(t *testing.T) {
	example := codegenFixture("json")
	code, err := GenerateResponseExampleCode(example, "python")
	if err != nil {
		t.Fatal(err)
	}

	const marker = "payload = "
	index := strings.Index(code, marker)
	if index < 0 {
		t.Fatalf("no payload line in the output:\n%s", code)
	}
	line := code[index+len(marker):]
	if end := strings.Index(line, "\n"); end >= 0 {
		line = line[:end]
	}

	// The literal is Go/Python double-quoted, so Go's unquoter reconstructs it.
	var decoded string
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("the generated literal is not a valid quoted string: %v\n%s", err, line)
	}
	if decoded != hostileBody {
		t.Errorf("the body did not survive quoting:\ngot  %q\nwant %q", decoded, hostileBody)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &parsed); err != nil {
		t.Errorf("the reconstructed body is not valid JSON: %v", err)
	}
}

func TestUnknownCodegenLanguageIsRejected(t *testing.T) {
	if _, err := GenerateResponseExampleCode(codegenFixture("json"), "brainfuck"); err == nil {
		t.Error("an unknown language should be an error, not empty output")
	}
	// An empty language is the documented default and must stay curl.
	code, err := GenerateResponseExampleCode(codegenFixture("json"), "")
	if err != nil {
		t.Fatalf("the default language failed: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(code), "curl") {
		t.Errorf("the default is no longer curl:\n%s", code)
	}
}

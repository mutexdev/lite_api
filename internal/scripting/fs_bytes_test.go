// Turning whatever a script passes to fs.writeFile into bytes.
//
// JavaScript has half a dozen ways to hold binary data — a string in some
// encoding, a Buffer, a Uint8Array, an ArrayBuffer, a plain array of numbers —
// and fs.writeFile accepts all of them. Every one takes a different path
// through this function, and a path that gets it wrong writes a FILE: corrupt,
// truncated, or holding the text of the data rather than the data.
//
// The read side has to agree with the write side, so the tests below are mostly
// round trips: what a script wrote must be what a script reads back.
package scripting

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func jsValue(t *testing.T, source string) (*goja.Runtime, goja.Value) {
	t.Helper()
	runtime := goja.New()
	value, err := runtime.RunString(source)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	return runtime, value
}

func TestFSWriteAcceptsEveryJavaScriptBinaryShape(t *testing.T) {
	for name, source := range map[string]string{
		"string":           `"abc"`,
		"array of numbers": `[97, 98, 99]`,
		"Uint8Array":       `new Uint8Array([97, 98, 99])`,
		"ArrayBuffer":      `new Uint8Array([97, 98, 99]).buffer`,
		"Int8Array":        `new Int8Array([97, 98, 99])`,
		"DataView":         `new DataView(new Uint8Array([97, 98, 99]).buffer)`,
	} {
		runtime, value := jsValue(t, source)
		got, err := scriptFSWriteBytes(runtime, value, goja.Undefined())
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(got) != "abc" {
			t.Errorf("%s: wrote %q, want \"abc\"", name, got)
		}
	}
}

// A view need not start at the beginning of its buffer. Reading from offset
// zero would write the wrong three bytes — a file that is the right size and
// the wrong contents, which nothing downstream can detect.
func TestFSWriteHonoursAViewsByteOffset(t *testing.T) {
	for name, source := range map[string]string{
		"DataView":   `new DataView(new Uint8Array([0, 0, 97, 98, 99]).buffer, 2, 3)`,
		"Uint8Array": `new Uint8Array(new Uint8Array([0, 0, 97, 98, 99]).buffer, 2, 3)`,
	} {
		runtime, value := jsValue(t, source)
		got, err := scriptFSWriteBytes(runtime, value, goja.Undefined())
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(got) != "abc" {
			t.Errorf("%s: wrote %q, want the three bytes the view covers", name, got)
		}
	}
}

// A Buffer is the shape scripts use most, because it is what fs.readFile hands
// back — so read-modify-write is the ordinary case and must survive it.
func TestFSWriteRoundTripsWhatItRead(t *testing.T) {
	runtime := goja.New()
	original := []byte{0x00, 0x7f, 0x80, 0xff, 'a'}

	for _, encoding := range []string{"", "utf8", "base64", "hex", "latin1"} {
		read := scriptFSFileValue(runtime, original, encoding)
		written, err := scriptFSWriteBytes(runtime, read, runtime.ToValue(encoding))
		if err != nil {
			t.Errorf("%q: %v", encoding, err)
			continue
		}
		if encoding == "utf8" {
			// utf8 is lossy for bytes that are not valid UTF-8: Go replaces
			// them with U+FFFD on the way out and cannot recover them. That is
			// inherent to asking for text, and the reason "" gives a Buffer.
			continue
		}
		if string(written) != string(original) {
			t.Errorf("%q: round trip gave % x, want % x", encoding, written, original)
		}
	}
}

// The encoding names come from Node, where every one of these spellings is
// accepted. Rejecting "UTF-8" because of its hyphen would fail a script that is
// written exactly as the Node docs show.
func TestFSEncodingNamesAreNormalisedLikeNodes(t *testing.T) {
	for _, spelling := range []string{"utf8", "UTF-8", " utf_8 ", "UTF8"} {
		got, err := scriptFSBytesFromString("abc", spelling)
		if err != nil || string(got) != "abc" {
			t.Errorf("%q: got %q %v", spelling, got, err)
		}
	}
	if _, err := scriptFSBytesFromString("abc", "utf16"); err == nil {
		t.Error("an unsupported encoding was accepted")
	}
}

func TestFSStringDecodingPerEncoding(t *testing.T) {
	for name, tc := range map[string]struct {
		value, encoding, want string
	}{
		"plain utf8":  {"héllo", "utf8", "héllo"},
		"base64":      {"YWJj", "base64", "abc"},
		"base64url":   {"YWJj", "base64url", "abc"},
		"hex":         {"616263", "hex", "abc"},
		"hex spaced":  {"  616263  ", "hex", "abc"},
		"latin1":      {"abc", "latin1", "abc"},
		"no encoding": {"abc", "", "abc"},
	} {
		got, err := scriptFSBytesFromString(tc.value, tc.encoding)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// Malformed input has to be an error. Writing a partial decode would leave a
// file that looks written and is silently truncated.
func TestFSStringDecodingRejectsMalformedInput(t *testing.T) {
	if _, err := scriptFSBytesFromString("zzz", "hex"); err == nil {
		t.Error("invalid hex was accepted")
	}
	if _, err := scriptFSBytesFromString("!!!!", "base64"); err == nil {
		t.Error("invalid base64 was accepted")
	}
	if _, err := scriptFSBytesFromString("Ł", "latin1"); err == nil {
		t.Error("a character above 0xff was accepted as latin1")
	}
}

// The options argument is either a bare encoding string or an object with an
// encoding property, exactly as Node accepts. Reading only one form makes half
// the scripts in circulation write UTF-8 bytes for base64 text.
func TestFSEncodingComesFromEitherAStringOrAnObject(t *testing.T) {
	runtime := goja.New()
	if got := scriptFSEncoding(runtime, runtime.ToValue("BASE64")); got != "base64" {
		t.Errorf("string form gave %q", got)
	}
	object, err := runtime.RunString(`({ encoding: "base64" })`)
	if err != nil {
		t.Fatal(err)
	}
	if got := scriptFSEncoding(runtime, object); got != "base64" {
		t.Errorf("object form gave %q", got)
	}
	empty, err := runtime.RunString(`({ flag: "w" })`)
	if err != nil {
		t.Fatal(err)
	}
	if got := scriptFSEncoding(runtime, empty); got != "" {
		t.Errorf("an options object with no encoding gave %q", got)
	}
	if got := scriptFSEncoding(runtime, goja.Undefined()); got != "" {
		t.Errorf("undefined gave %q", got)
	}
	if got := scriptFSEncoding(runtime, goja.Null()); got != "" {
		t.Errorf("null gave %q", got)
	}
}

func TestFSWriteRejectsWhatIsNotData(t *testing.T) {
	runtime := goja.New()
	for name, value := range map[string]goja.Value{
		"null":      goja.Null(),
		"undefined": goja.Undefined(),
		"nil":       nil,
	} {
		if _, err := scriptFSWriteBytes(runtime, value, goja.Undefined()); err == nil {
			t.Errorf("%s was accepted as file data", name)
		}
	}
	object, err := runtime.RunString(`({ a: 1 })`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scriptFSWriteBytes(runtime, object, goja.Undefined()); err == nil {
		t.Error("a plain object was accepted as file data")
	}
}

// The message names every shape that IS accepted, because the script author's
// next question is always "what should I have passed?".
func TestFSWriteErrorSaysWhatIsAccepted(t *testing.T) {
	runtime := goja.New()
	_, err := scriptFSWriteBytes(runtime, goja.Null(), goja.Undefined())
	if err == nil {
		t.Fatal("null accepted")
	}
	for _, shape := range []string{"string", "Buffer", "ArrayBuffer", "typed array"} {
		if !strings.Contains(err.Error(), shape) {
			t.Errorf("error %q does not mention %s", err, shape)
		}
	}
}

// 64 MB is the cap. Without it, a script that passes an object claiming a
// length of 2^40 makes the process allocate until it is killed — no error, no
// file, no message.
func TestFSWriteRefusesImplausibleLengths(t *testing.T) {
	runtime, value := jsValue(t, `({ length: 1099511627776, 0: 1 })`)
	if _, err := scriptFSWriteBytes(runtime, value, goja.Undefined()); err == nil {
		t.Fatal("a terabyte-long object was accepted")
	}
	negative, err := runtime.RunString(`({ length: -1 })`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scriptFSWriteBytes(runtime, negative, goja.Undefined()); err == nil {
		t.Error("a negative length was accepted")
	}
}

// A sparse array has holes, and a hole is not a byte. Writing zero for it keeps
// the file the length the script asked for; skipping the entry would shift
// every byte after it.
func TestFSWriteFillsHolesWithZero(t *testing.T) {
	runtime, value := jsValue(t, `(() => { const a = new Array(4); a[0] = 97; a[3] = 98; return a; })()`)
	got, err := scriptFSWriteBytes(runtime, value, goja.Undefined())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("wrote %d bytes, want 4", len(got))
	}
	if got[0] != 97 || got[1] != 0 || got[2] != 0 || got[3] != 98 {
		t.Errorf("got % x", got)
	}
}

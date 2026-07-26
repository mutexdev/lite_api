// Converting a script's byte array into bytes on disk.
//
// These two turn what a script passes to fs.writeFile — a Uint8Array, an
// ArrayBuffer view, or a plain array of numbers — into the bytes actually
// written. Coverage found both at 0%.
//
// The failure mode is data corruption at rest. A conversion that truncates,
// misreads a numeric type or silently substitutes zeroes writes a file that
// exists, has a plausible size, and contains the wrong bytes. Nothing errors,
// and the script reports success.
package scripting

import (
	"encoding/json"
	"testing"

	"github.com/dop251/goja"
)

func TestFSBytesFromInterfaceSliceHandlesEveryNumericShape(t *testing.T) {
	// goja hands numbers over as int64 or float64 depending on how they were
	// written; a decoder may hand over json.Number. All three must survive.
	got := scriptFSBytesFromInterfaceSlice([]interface{}{
		int(72), int64(101), float64(108), json.Number("108"), int64(111),
	})
	if string(got) != "Hello" {
		t.Fatalf("got %q (% x), want %q", got, got, "Hello")
	}
}

// Values above 255 wrap, which is what a byte conversion does and what the
// JS side does too — Uint8Array truncates modulo 256. Pinning it so a future
// change to clamp instead is a deliberate decision.
func TestFSBytesFromInterfaceSliceWrapsOutOfRangeValues(t *testing.T) {
	got := scriptFSBytesFromInterfaceSlice([]interface{}{int64(256), int64(257), int64(-1)})
	if got[0] != 0 || got[1] != 1 || got[2] != 255 {
		t.Fatalf("got % x, want 00 01 ff — byte conversion wraps like Uint8Array", got)
	}
}

// A non-numeric entry becomes zero rather than aborting the write. That is a
// deliberate choice and worth stating: the alternative is a script failing to
// save anything because one element was undefined.
func TestFSBytesFromInterfaceSliceZeroesUnknownEntries(t *testing.T) {
	got := scriptFSBytesFromInterfaceSlice([]interface{}{int64(65), "not a number", nil, int64(66)})
	if len(got) != 4 || got[0] != 65 || got[1] != 0 || got[2] != 0 || got[3] != 66 {
		t.Fatalf("got % x, want 41 00 00 42", got)
	}
}

func TestFSBytesFromInterfaceSliceHandlesEmpty(t *testing.T) {
	if got := scriptFSBytesFromInterfaceSlice(nil); len(got) != 0 {
		t.Fatalf("got %d bytes for a nil slice", len(got))
	}
}

func TestFSBytesFromIndexedObjectReadsByIndex(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	for i, b := range []byte("Hi!") {
		_ = obj.Set(itoaFS(i), int64(b))
	}

	got, err := scriptFSBytesFromIndexedObject(obj, 3)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Hi!" {
		t.Fatalf("got %q, want %q", got, "Hi!")
	}
}

// A hole in the array is a zero byte, not a short write — the file must be the
// length the caller asked for, or an offset-sensitive format is silently
// mangled.
func TestFSBytesFromIndexedObjectFillsHolesWithZero(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	_ = obj.Set("0", int64(65))
	_ = obj.Set("2", int64(67))

	got, err := scriptFSBytesFromIndexedObject(obj, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 65 || got[1] != 0 || got[2] != 67 {
		t.Fatalf("got % x, want 41 00 43 — a missing index must be a zero byte, not a shorter file", got)
	}
}

// The guards. A negative length would panic on make([]byte, n); an unbounded
// one would let a script allocate the process to death from a single call.
func TestFSBytesFromIndexedObjectRejectsBadLengths(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()

	if _, err := scriptFSBytesFromIndexedObject(obj, -1); err == nil {
		t.Error("a negative length must be rejected, not passed to make()")
	}
	if _, err := scriptFSBytesFromIndexedObject(obj, 64*1024*1024+1); err == nil {
		t.Error("a length past the cap must be rejected — a script could otherwise allocate unbounded memory")
	}
	if _, err := scriptFSBytesFromIndexedObject(obj, 0); err != nil {
		t.Errorf("zero length is legitimate (an empty file): %v", err)
	}
}

func itoaFS(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

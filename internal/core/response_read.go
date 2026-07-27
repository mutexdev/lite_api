package core

// US-010 — the ReadResponseBody binding.
//
// US-009 moved response bodies into a content-addressed store. This is how the
// frontend gets them back: a bounded, offset-based read, so "Render full" and
// "Load more" in the response inspector fetch the slice they need instead of
// the whole body travelling through AppState.
//
// WHY THE SLICING RULES ARE THE HARD PART. response.ts already slices bodies
// for its tiered preview limits (128 KB automatic, 512 KB embedded, 1 MB full)
// and gets two things right that a naive implementation gets wrong:
//
//   * A UTF-8 slice must not split a rune. Cutting at a byte offset mid-rune
//     produces invalid UTF-8, which JSON encoding rewrites as U+FFFD — a body
//     of CJK text or emoji comes back visibly corrupted at every chunk seam.
//   * A Base64 slice must land on a 4-character quartet. Base64 encodes 3 bytes
//     as 4 characters, so a slice taken at an arbitrary offset decodes to
//     garbage from that point on.
//
// Both are preserved here rather than left to the caller, because the caller is
// the one place that cannot check them: it asked for bytes precisely because it
// does not have them.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"unicode/utf8"
)

// ResponseBodySlice is what ReadResponseBody returns.
//
// Raw and Base64 describe THE SAME bytes in two encodings, and both are
// returned together on purpose: the inspector switches between text and
// base64/hex views without refetching, and computing base64 in the frontend
// would mean shipping the raw bytes anyway.
type ResponseBodySlice struct {
	// Raw is the requested slice as text, adjusted to a UTF-8 boundary.
	Raw string `json:"raw"`
	// Base64 is the same slice, quartet-aligned.
	Base64 string `json:"base64"`
	// Offset and Length describe what was ACTUALLY returned after boundary
	// adjustment, which may be shorter than requested. A caller paging through
	// a body must advance by these, not by what it asked for, or it will skip
	// the bytes trimmed off a rune boundary.
	Offset int `json:"offset"`
	Length int `json:"length"`
	// TotalSize is the whole body's length, so the caller can tell whether more
	// remains without a second call.
	TotalSize int `json:"totalSize"`
	// Truncated reports that bytes remain after this slice.
	Truncated bool `json:"truncated"`
}

// responseBodyReadCeiling bounds a single read regardless of what was asked
// for. 1 MiB matches response.ts's full-render tier: a caller that wants more
// pages for it, and a caller that asks for a 500 MB body in one call is a bug
// that should not be able to allocate 500 MB in this process.
const responseBodyReadCeiling = 1 << 20

// ReadResponseBody returns a bounded slice of a stored response body.
//
// offset and length are in bytes. A negative offset is clamped to zero and a
// non-positive length means "up to the ceiling", so the common call —
// ReadResponseBody(handle, 0, 0) — reads the first megabyte.
func (a *App) ReadResponseBody(handle string, offset int, length int) (ResponseBodySlice, error) {
	if handle == "" {
		return ResponseBodySlice{}, errors.New("response body handle is empty")
	}
	store, err := a.responseStore()
	if err != nil {
		return ResponseBodySlice{}, err
	}
	body, err := store.Get(responsestore.Handle(handle))
	if err != nil {
		return ResponseBodySlice{}, fmt.Errorf("read response body: %w", err)
	}

	total := len(body)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		// Past the end is not an error: a caller paging a body that shrank
		// under it should get an empty tail, not a failure.
		return ResponseBodySlice{Offset: total, TotalSize: total}, nil
	}
	if length <= 0 || length > responseBodyReadCeiling {
		length = responseBodyReadCeiling
	}
	end := offset + length
	if end > total {
		end = total
	}

	// Align the START forward to a rune boundary. Aligning backward instead
	// would re-send bytes the caller already has and, worse, could return the
	// same offset forever — a paging loop that never advances.
	for offset < end && !utf8.RuneStart(body[offset]) {
		offset++
	}
	// Align the END backward, so the slice never carries a partial rune. Only
	// when there is more to come: the final slice must include the last bytes
	// even if the body itself is not valid UTF-8.
	if end < total {
		for end > offset && !utf8.RuneStart(body[end]) {
			end--
		}
	}

	slice := body[offset:end]
	return ResponseBodySlice{
		Raw:       string(slice),
		Base64:    base64.StdEncoding.EncodeToString(slice),
		Offset:    offset,
		Length:    len(slice),
		TotalSize: total,
		Truncated: end < total,
	}, nil
}

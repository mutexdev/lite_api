package core

// US-009, step 1 — the response body store, standalone.
//
// Nothing wires this into Response yet. It is committed on its own so the
// storage behaviour can be reviewed and tested before anything about the
// persisted AppState shape changes; see .ralph/plans/US-009.md for why that
// ordering matters here specifically.
//
// WHAT IT IS FOR. Response.Body and Response.BodyBase64 live inside AppState,
// so every cached response body is re-serialised into state.json on every
// persist. The Phase 0 baseline measured that at 225.7 ms and 221 MB for a
// single persist of a 500-request workspace, ~6x amplification over the ~37 MB
// fixture, and a 5 MB response costs ~11.7 MB because it is held twice — once
// raw, once base64. This store is where those bytes go instead.
//
// THE DESIGN CONSTRAINT THAT SHAPES EVERYTHING: a body written here must be
// readable after a restart, because a cached response outlives the process that
// fetched it. So this is not a cache that may forget — memory is only a front
// for the spill directory, and every Put writes to disk before returning. The
// LRU bounds MEMORY, never durability.

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

// responseStoreMemoryBudget is how many bytes of body the in-memory front will
// hold before evicting. Eviction drops the memory copy only; the spill file
// stays.
//
// 32 MiB is chosen against the fixture rather than as a round number: the
// Phase 0 workspace holds 3 x 5 MiB cached bodies, so a budget this size keeps
// a realistic working set resident while still exercising eviction under the
// 50 MB-plus responses US-011 has to handle.
const responseStoreMemoryBudget = 32 << 20

// responseHandle identifies a stored body. It is the content hash, so identical
// bodies share one file — re-running a request that returns the same payload
// costs nothing extra on disk.
type responseHandle string

type responseStoreEntry struct {
	handle responseHandle
	body   []byte
	elem   *list.Element
}

// responseStore keeps recently used bodies in memory and all bodies on disk.
//
// The zero value is not usable; construct with newResponseStore.
type responseStore struct {
	mu       sync.Mutex
	dir      string
	entries  map[responseHandle]*responseStoreEntry
	order    *list.List // front = most recently used
	resident int
	budget   int
}

func newResponseStore(dataDir string) (*responseStore, error) {
	dir := filepath.Join(dataDir, "responses")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create response store: %w", err)
	}
	return &responseStore{
		dir:     dir,
		entries: map[responseHandle]*responseStoreEntry{},
		order:   list.New(),
		budget:  responseStoreMemoryBudget,
	}, nil
}

func (s *responseStore) path(handle responseHandle) string {
	return filepath.Join(s.dir, string(handle))
}

// Put stores a body and returns its handle.
//
// The write happens before the memory copy is recorded, so a handle is never
// returned for a body that is not on disk. Storing the same bytes twice is a
// no-op beyond refreshing recency: the handle is the content hash, so the file
// already exists and is already correct.
func (s *responseStore) Put(body []byte) (responseHandle, error) {
	sum := sha256.Sum256(body)
	handle := responseHandle(hex.EncodeToString(sum[:]))

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.path(handle)); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat response body: %w", err)
		}
		// atomicfile.Write, not os.WriteFile: a body half-written by a killed
		// process would be indistinguishable from a complete one, because the
		// filename is a hash of what the CONTENT was meant to be. The next
		// reader would then hand a user a truncated response and no error.
		if err := atomicfile.Write(s.path(handle), body, 0o600); err != nil {
			return "", fmt.Errorf("write response body: %w", err)
		}
	}

	s.rememberLocked(handle, body)
	return handle, nil
}

// Get returns a body by handle, reading it back from disk if it has been
// evicted from memory.
func (s *responseStore) Get(handle responseHandle) ([]byte, error) {
	s.mu.Lock()
	if entry, ok := s.entries[handle]; ok {
		s.order.MoveToFront(entry.elem)
		body := entry.body
		s.mu.Unlock()
		return body, nil
	}
	s.mu.Unlock()

	// Read outside the lock: a 50 MB body should not block every other
	// response read for the duration of the disk I/O.
	body, err := os.ReadFile(s.path(handle))
	if err != nil {
		return nil, fmt.Errorf("read response body %s: %w", handle, err)
	}
	s.mu.Lock()
	s.rememberLocked(handle, body)
	s.mu.Unlock()
	return body, nil
}

// rememberLocked records a body in the memory front and evicts until the
// budget is met. s.mu must be held.
func (s *responseStore) rememberLocked(handle responseHandle, body []byte) {
	if entry, ok := s.entries[handle]; ok {
		s.order.MoveToFront(entry.elem)
		return
	}
	entry := &responseStoreEntry{handle: handle, body: body}
	entry.elem = s.order.PushFront(entry)
	s.entries[handle] = entry
	s.resident += len(body)

	// A single body larger than the whole budget is kept anyway: evicting it
	// immediately would mean re-reading it from disk on the very next access,
	// which is worse than briefly exceeding the budget. The next Put of
	// anything else will evict it.
	for s.resident > s.budget && s.order.Len() > 1 {
		oldest := s.order.Back()
		if oldest == nil {
			break
		}
		victim := oldest.Value.(*responseStoreEntry)
		s.order.Remove(oldest)
		delete(s.entries, victim.handle)
		s.resident -= len(victim.body)
	}
}

// residentBytes reports the current memory footprint. Test-facing.
func (s *responseStore) residentBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resident
}

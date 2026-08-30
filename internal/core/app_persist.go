package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/responsestore"
)

// bumpRevisionLocked advances the mutation counter and stamps it into the state
// the bindings hand back. a.mu must be held.
func (a *App) bumpRevisionLocked() {
	a.revision++
	a.state.Revision = a.revision
}

// writeStateLocked is the write itself, with no revision side effect.
func (a *App) writeStateLocked() error {
	if a.workspaceRuntime != nil {
		return a.persistWorkspaceRuntimeLocked()
	}
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	if err := a.storeStateEnvironmentSecretsLocked(); err != nil {
		return err
	}
	if err := a.storeOAuth2Credentials(); err != nil {
		return err
	}
	// json.Marshal, not MarshalIndent: indentation runs a second formatting
	// pass over the whole document and roughly doubles the bytes written, and
	// nothing reads state.json by eye (improvement_v2.md §2.1.B).
	data, err := json.Marshal(envsecrets.StateForStorage(a.state, a.dataDir))
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(a.dataDir, "state.json"), data, 0o600)
}

// persistScope names the on-disk artifact a mutation invalidated. US-013 gives
// each scope an independent dirty flag so unchanged files are not rewritten or
// re-encrypted; until then every scope resolves to the single state writer, but
// call sites already record what they actually changed.
type persistScope uint8

const (
	// persistScopeState covers state.json together with the secrets and OAuth2
	// credential files persistLocked writes alongside it.
	persistScopeState persistScope = iota
)

const (
	// persistDebounceInterval is the quiet period a mark waits before the
	// background writer serialises state. Keystrokes arrive an order of
	// magnitude faster, so a burst of typing collapses into one write.
	persistDebounceInterval = 250 * time.Millisecond
	// persistRetryCeiling bounds the exponential backoff applied to a failing
	// write so a permanently unwritable data directory does not spin.
	persistRetryCeiling = 5 * time.Second
)

// markDirty records that in-memory state has diverged from disk and ensures a
// background writer is scheduled. Like persistLocked, which it replaces at
// every mutation site, it must be called with a.mu held.
//
// The expensive half of persistence — serialising the whole AppState, cached
// response bodies included, and writing it — is what gets deferred. The
// environment secret store is not deferred; see persistEnvironmentSecrets.
//
// The returned error is the *previous* background write failure, if any. A
// background write has no caller to return to, so rather than dropping the
// error (improvement_v2.md §8 risk 4) it is parked and handed to the next
// mutation, which does have a caller — the Wails binding, and through it the
// user. Reading it clears it, so a single failure is reported once.
// migrateResponseBodiesLocked gives every already-loaded response body a handle
// in the store, so that a workspace created before US-009 is on the same
// footing as a new one. a.mu is held by the caller.
//
// BEST-EFFORT ON PURPOSE, and this is the important part. At this step
// Response.Body is still populated and still authoritative, so a handle that
// fails to be written costs nothing — the body is right there. Failing the load
// instead would mean an unwritable or full disk turns "your cached responses
// are slow" into "your workspace will not open", which is a far worse outcome
// than the one this story set out to fix.
//
// The picture changes at step 5, when Body is deleted. From that point a
// missing handle IS data loss and this function must become fallible. That
// transition is the single most dangerous moment in this story and is called
// out here so it is not discovered later.
func (a *App) migrateResponseBodiesLocked() {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			for ii := range collection.Items {
				item := &collection.Items[ii]
				if item.Response == nil {
					continue
				}
				// The error is dropped rather than logged: this runs once per
				// response on every startup, and a broken store would otherwise
				// emit one line per cached response before the window appears.
				_ = a.attachResponseBody(item.Response)
			}
		}
	}
}

// defaultResponseBodyLimit bounds how much of a response body is read into
// memory when the user has not chosen otherwise.
//
// 100 MB matches the story. The number matters less than the fact that there is
// one: io.ReadAll on a response body is an unbounded allocation driven by a
// remote server, so a misconfigured endpoint streaming gigabytes takes the
// process down. That is a denial of service with no attacker required.
const defaultResponseBodyLimit = 100 << 20

// responseBodyReadLimit resolves the configured cap. A negative preference
// means "no limit" and is honoured, because a user who has deliberately asked
// for unbounded reads on a trusted local endpoint should get them; zero means
// they have expressed no preference and gets the default.
func responseBodyReadLimit(preferences RequestPreferences) int64 {
	switch {
	case preferences.MaxResponseBytes < 0:
		return -1
	case preferences.MaxResponseBytes == 0:
		return defaultResponseBodyLimit
	default:
		return int64(preferences.MaxResponseBytes)
	}
}

// readResponseBodyLimited reads at most limit bytes and reports whether the
// body was cut short.
//
// It reads limit+1 bytes rather than limit, because io.LimitReader alone cannot
// distinguish "the body was exactly the limit" from "the body was longer and we
// stopped". Getting that wrong means silently truncating a body that happened
// to land on the boundary and telling the user it was complete — which is the
// failure this story explicitly rules out ("truncation is surfaced in the UI,
// not silent").
func readResponseBodyLimited(reader io.Reader, limit int64) ([]byte, bool, error) {
	if limit < 0 {
		body, err := io.ReadAll(reader)
		return body, false, err
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return body, false, err
	}
	if int64(len(body)) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}

// responseBodyHeadLimit is how much of a body BodyHead carries inline.
//
// 8 KiB matches the story's "~8 KB inline head". It is sized to the job it
// does: rendering a collapsed row or a list preview without touching the disk.
// The response inspector's own automatic preview budget is 128 KB (see
// response.ts), so a head this size is deliberately NOT enough to render the
// full preview — that read goes through the store, which is the point.
const responseBodyHeadLimit = 8 << 10

// attachResponseBody stores a response's body and records the handle and inline
// head on it.
//
// Additive by design: Body and BodyBase64 are left exactly as they are. Nothing
// reads BodyHandle yet, so a failure here must not fail the request — a user
// who just got a 200 should not see an error because a cache write failed. The
// error is returned for the caller to log or ignore deliberately rather than
// swallowed here, but the response stays intact either way.
//
// The failure is ALSO logged here, once per session. Every caller on the send
// path writes `_ = a.attachResponseBody(...)`, and deliberately so — but the
// consequence was that a response store which could not be written (a read-only
// data directory, a full disk) produced no evidence anywhere at all. Owning the
// log here rather than at the call sites means a caller cannot opt out of it by
// discarding the error, and the once-per-session guard is what keeps
// migrateResponseBodiesLocked from emitting one line per cached response at
// startup.
func (a *App) attachResponseBody(response *Response) error {
	if response == nil || response.Body == "" || response.BodyHandle != "" {
		return nil
	}
	store, err := a.responseStore()
	if err != nil {
		return a.reportResponseStoreFailure(err)
	}
	handle, err := store.Put([]byte(response.Body))
	if err != nil {
		return a.reportResponseStoreFailure(err)
	}
	response.BodyHandle = string(handle)
	response.BodyHead = responseBodyHead(response.Body)
	return nil
}

// reportResponseStoreFailure logs the first response-store failure of the
// session and returns the error unchanged.
func (a *App) reportResponseStoreFailure(err error) error {
	if err == nil {
		return nil
	}
	a.responseStoreFailureOnce.Do(func() {
		a.logPersistError("Could not cache a response body: " + err.Error())
	})
	return err
}

// reportHistoryFailure is reportResponseStoreFailure for the send-history log.
func (a *App) reportHistoryFailure(err error) error {
	if err == nil {
		return nil
	}
	a.historyFailureOnce.Do(func() {
		a.logPersistError("Could not record send history: " + err.Error())
	})
	return err
}

// responseBodyHead returns the inline prefix, truncated on a UTF-8 boundary.
//
// Slicing a byte count out of a string can split a multi-byte rune and produce
// invalid UTF-8, which encoding/json then rewrites as U+FFFD — so a body of
// CJK text or emoji would come back subtly corrupted in the inline view. The
// backward scan is what response.ts already does for the same reason.
func responseBodyHead(body string) string {
	if len(body) <= responseBodyHeadLimit {
		return body
	}
	cut := responseBodyHeadLimit
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut]
}

// responsestore.Store returns the App's body store, creating it on first use.
//
// Returns an error rather than a nil store when the directory cannot be made:
// a caller that silently got nil would write a body nowhere and report success,
// which is the failure mode this whole story exists to remove.
func (a *App) responseStore() (*responsestore.Store, error) {
	a.responsesMu.Lock()
	defer a.responsesMu.Unlock()
	if a.responses != nil {
		return a.responses, nil
	}
	dir := a.dataDir
	if dir == "" {
		dir = defaultDataDir()
	}
	store, err := responsestore.New(dir)
	if err != nil {
		return nil, err
	}
	a.responses = store
	return store, nil
}

func (a *App) markDirty(scope persistScope) error {
	_ = scope
	// US-008. markDirty is the one call every mutation site already funnels
	// through, and reads never reach it — ensureReadyLocked mutates but does
	// not mark dirty — so bumping here satisfies "exactly once per mutation,
	// never on a read" without touching 81 call sites. a.mu is held by
	// contract, which is what makes the increment safe.
	a.bumpRevisionLocked()
	secretsErr := a.persistEnvironmentSecretsLocked()

	a.persistMu.Lock()
	a.persistDirty = true
	// A stopped App schedules nothing further: the state stays dirty and
	// flushPersist remains the way to get it to disk. Without the sticky flag a
	// mutation arriving from a straggling goroutine after stopPersistWriter
	// returned would start a fresh writer and reopen the window this closes.
	if !a.persistRunning && !a.persistStopped {
		a.persistRunning = true
		a.persistStop = make(chan struct{})
		a.persistDone = make(chan struct{})
		go a.persistWriterLoop(a.persistStop, a.persistDone)
	}
	previous := a.persistErr
	a.persistErr = nil
	a.persistMu.Unlock()

	if secretsErr != nil {
		return secretsErr
	}
	return previous
}

// persistEnvironmentSecretsLocked writes secrets.json synchronously, and is the
// one part of persistLocked that must not be deferred.
//
// Collection and workspace environment files on disk are written with their
// secret values scrubbed out; the values live only in secrets.json.
// ensureReadyLocked re-reads those files and re-hydrates from secrets.json on
// the very next binding call, so a deferred secret write does not delay the
// values reaching disk — it loses them outright. It is also cheap: it scales
// with the number of environment variables, not with cached response bodies,
// and is a rounding error against the state serialisation it now runs beside.
func (a *App) persistEnvironmentSecretsLocked() error {
	if a.workspaceRuntime != nil {
		// Multiple windows share this file, so take the same cross-process
		// guard persistWorkspaceRuntimeLocked uses.
		return withSharedWorkspacePersistenceGuard(a.dataDir, a.storeStateEnvironmentSecretsLocked)
	}
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	return a.storeStateEnvironmentSecretsLocked()
}

// persistWriterLoop is the background writer. At most one runs per App: it is
// started by the first markDirty and exits once it observes a clean state, so
// an idle App holds no goroutine.
//
// stop and done are passed in rather than read off the App so that a writer
// which already exited on its own cannot be confused with the one a later
// markDirty started: each generation owns its own pair of channels.
func (a *App) persistWriterLoop(stop <-chan struct{}, done chan<- struct{}) {
	// Closed on every exit path, including a panic in persistOnce: this is what
	// stopPersistWriter blocks on, and a writer that died without closing it
	// would hang every caller.
	defer close(done)

	delay := persistDebounceInterval
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			a.persistMu.Lock()
			a.persistRunning = false
			a.persistMu.Unlock()
			return
		case <-timer.C:
		}

		a.persistMu.Lock()
		// persistStopped is re-checked here, under persistMu and before any
		// write, because select picks at random when both the timer and stop
		// are ready. Checking the flag the stopper set under the same mutex —
		// rather than trusting which case select happened to take — is what
		// makes "no write can start after stopPersistWriter" a guarantee.
		if a.persistStopped || !a.persistDirty {
			a.persistRunning = false
			a.persistMu.Unlock()
			return
		}
		// Cleared before the state snapshot is taken, never after: a mutation
		// landing while this write is in flight re-sets the flag and is picked
		// up by the next cycle instead of being marked clean by a write that
		// predates it.
		a.persistDirty = false
		a.persistMu.Unlock()

		started := time.Now()
		err := a.persistOnce()
		elapsed := time.Since(started)

		a.persistMu.Lock()
		if err != nil {
			// Leave the state dirty so the next cycle retries. A transient
			// failure (full disk, unmounted volume, lost workspace ownership)
			// must not quietly become a lost write.
			a.persistDirty = true
			a.persistFailures++
			a.persistErr = err
			delay = persistBackoff(a.persistFailures)
		} else {
			a.persistFailures = 0
			a.persistWrites++
			// Wait out at least one debounce beyond however long the write
			// took. persistLocked holds a.mu for its whole duration, and a
			// large workspace takes longer to serialise than the debounce, so
			// a fixed interval would let the writer monopolise the mutex.
			delay = persistDebounceInterval + elapsed
		}
		a.persistMu.Unlock()

		// The timer has already fired and been drained by the select above, so
		// it is safe to reset without the Stop/drain dance.
		timer.Reset(delay)
	}
}

// stopPersistWriter stops this App's background writer and does not return
// until that goroutine has exited. Once it returns, the App cannot write to
// a.dataDir again on its own: the sticky persistStopped flag keeps markDirty
// from starting a replacement.
//
// It is idempotent, and safe on an App that never started a writer (a bare
// literal, or one that was never mutated). It must be called *without* a.mu
// held, since the writer it waits on may be inside persistOnce, which takes
// a.mu; the lock order a.mu -> persistMu is unchanged, and persistMu is still a
// leaf — it is released before the wait on persistDone.
//
// Stopping does not flush. A stop is issued when the App is finished with (test
// cleanup, process shutdown), and at that point a directory may already be
// being torn down; writing state into it is exactly the hazard this closes.
// Callers that need the pending state on disk flush first — shutdown does, and
// flushPersist keeps working afterwards because it writes synchronously on the
// caller's own goroutine rather than through the writer.
func (a *App) stopPersistWriter() {
	if a == nil {
		return
	}
	a.persistMu.Lock()
	done := a.persistDone
	if !a.persistStopped {
		a.persistStopped = true
		if a.persistStop != nil {
			close(a.persistStop)
		}
	}
	a.persistMu.Unlock()

	if done != nil {
		<-done
	}
}

func persistBackoff(failures int) time.Duration {
	delay := persistDebounceInterval
	for i := 1; i < failures && delay < persistRetryCeiling; i++ {
		delay *= 2
	}
	if delay > persistRetryCeiling {
		delay = persistRetryCeiling
	}
	return delay
}

// persistOnce performs one background write under a.mu.
func (a *App) persistOnce() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// writeStateLocked, not persistLocked: this is draining a mutation markDirty
	// already counted (US-008).
	err := a.writeStateLocked()
	if err != nil {
		a.reportPersistFailureLocked(err)
	}
	return err
}

// persistFailureReportInterval is how many consecutive background write
// failures pass between reports.
//
// The streak's FIRST failure is always reported; after that one in every
// persistFailureReportInterval is. Reporting every failure would flood the
// 20-entry notification list and evict everything else, which is why this used
// to report only once — but only once meant a data directory that became
// permanently unwritable said so a single time, hours ago, in a notification
// the user may already have marked read, while every edit since then has been
// silently lost. With the backoff ceiling at persistRetryCeiling, one report in
// ten is a reminder every few minutes rather than a flood.
const persistFailureReportInterval = 10

// reportPersistFailureLocked surfaces a background write failure everywhere it
// can be seen without a caller: the Wails log, and a notification the frontend
// already renders.
func (a *App) reportPersistFailureLocked(err error) {
	a.persistMu.Lock()
	// persistFailures counts the failures BEFORE this one — the writer loop
	// increments it after persistOnce returns — so a value of 0 is the first
	// failure of the streak.
	streak := a.persistFailures
	a.persistMu.Unlock()
	if streak%persistFailureReportInterval != 0 {
		return
	}
	message := "Could not save workspace state: " + err.Error()
	if streak > 0 {
		message = fmt.Sprintf("Could not save workspace state (%d consecutive failures): %s", streak+1, err.Error())
	}
	a.logPersistError(message)
	a.notify("error", message)
}

// logPersistError routes a persistence failure to the Wails log once the window
// exists, and to stderr before and after it does. a.ctx is the only context
// known to carry the Wails logger; a bare context.Background() does not.
func (a *App) logPersistError(message string) {
	if a.ctx != nil {
		wailsruntime.LogError(a.ctx, message)
		return
	}
	log.Printf("liteapi: %s", message)
}

// flushPersist force-writes any pending state synchronously. This is the
// explicit "the data must be on disk now" call: shutdown, beforeClose, window
// blur, and any test whose assertion is that a mutation reached disk.
func (a *App) flushPersist() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flushPersistLocked()
}

// flushPersistLocked is flushPersist for callers already holding a.mu.
//
// Because the whole flush happens under a.mu it also waits out any background
// write already in flight, and no mutation can interleave between the dirty
// check and the write — nothing is lost across a flush boundary.
func (a *App) flushPersistLocked() error {
	a.persistMu.Lock()
	dirty := a.persistDirty
	a.persistDirty = false
	pending := a.persistErr
	a.persistErr = nil
	a.persistMu.Unlock()

	if !dirty {
		return pending
	}
	// writeStateLocked, not persistLocked: see persistOnce (US-008).
	if err := a.writeStateLocked(); err != nil {
		a.persistMu.Lock()
		a.persistDirty = true
		a.persistMu.Unlock()
		return err
	}
	return pending
}

// FlushPendingWrites is the frontend-facing force-flush. The workbench calls it
// when the window loses focus, so state that is still inside the debounce
// window is on disk before the user switches away or the machine sleeps.
func (a *App) FlushPendingWrites() error {
	return a.flushPersist()
}

// persistLocked writes state.json synchronously on behalf of a mutation that
// wants durability before it returns — imports, recovery, draft-guard saves and
// the readiness normalisation in ensureReadyLocked. Because every one of those
// call sites is a real mutation, this is the second place (with markDirty) that
// owns the US-008 revision bump.
//
// Callers that merely want in-memory state flushed to disk — persistOnce and
// flushPersistLocked, which are draining a mutation markDirty has ALREADY
// counted — must call writeStateLocked instead, or the same mutation would be
// counted twice and the frontend would see a phantom revision gap.
func (a *App) persistLocked() error {
	a.bumpRevisionLocked()
	return a.writeStateLocked()
}

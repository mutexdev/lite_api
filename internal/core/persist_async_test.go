package core

// US-012 — coalesced asynchronous persistence.
//
// Two properties are under test here and they are independent:
//
//  1. Atomicity. persistLocked used to os.WriteFile state.json, which opens
//     with O_TRUNC: for the whole duration of a multi-megabyte write the file on
//     disk is zero-length or partial. A process killed there — or merely another
//     process reading — sees a state.json that does not parse. The temp-file +
//     fsync + os.Rename sequence makes the file switch from the old complete
//     contents to the new complete contents in one step.
//
//  2. Coalescing and the flush boundary. Mutations no longer write inline; a
//     background writer collapses a burst into one write, and flushPersist is
//     the synchronous boundary that guarantees the bytes are on disk. Nothing
//     may be lost across that boundary.
//
// Timing note: none of these tests sleep for a fixed duration and then assert.
// They either force a synchronous flush, or wait on a condition with a generous
// deadline. A test that needs a sleep to pass is a test that will eventually
// fail on a loaded machine.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

// newAppForTest builds an App rooted at a fresh temp directory and guarantees
// its background persist writer is stopped before that directory is removed.
//
// ORDERING — do not reorder these two lines. t.Cleanup functions run LIFO, and
// t.TempDir() registers the directory's removal at the moment it is called. The
// stop registered *after* the TempDir call therefore runs *before* the removal.
// Reversed, the writer would still be sleeping when RemoveAll walks the
// directory, and a state.json created between the walk and the final rmdir
// fails the cleanup with "directory not empty" — the US-012 flake this exists
// to prevent.
func newAppForTest(t testing.TB) *App {
	t.Helper()
	return newAppInDirForTest(t, t.TempDir())
}

// newAppInDirForTest is newAppForTest for the tests that need to choose the
// directory: the mutate-then-reload pairs that build a second App over the
// first one's data directory, and the ones that root the App in a subdirectory
// of a temp dir. Every one of those Apps runs its own writer, so every one of
// them needs its own stop.
//
// The same LIFO ordering argument applies, and it is the caller's to keep: dir
// must already exist as (or under) a t.TempDir() taken earlier in the test, so
// that this cleanup is registered after that directory's removal and therefore
// runs before it.
func newAppInDirForTest(t testing.TB, dir string) *App {
	t.Helper()
	return stopPersistWriterOnCleanup(t, NewAppWithDir(dir))
}

// stopPersistWriterOnCleanup registers the writer stop for an App that was
// built somewhere this package does not control the constructor — newProductionApp
// and newLargeWorkspaceApp both hand back a live App rooted at a temp dir.
func stopPersistWriterOnCleanup(t testing.TB, app *App) *App {
	t.Helper()
	t.Cleanup(app.stopPersistWriter)
	return app
}

// newProductionAppForTest wraps newProductionApp, which is production code:
// there is no t there to register a cleanup with, but the App it returns runs a
// writer rooted at the caller's temp directory just the same. A sweep over the
// NewAppWithDir call sites in _test.go files does not reach this one, which is
// precisely why it needs its own wrapper.
func newProductionAppForTest(t testing.TB, dataDir string, args []string) (*App, error) {
	t.Helper()
	app, err := newProductionApp(dataDir, args) // stopPersistWriterOnCleanup: this is the wrapper
	// Registered even on the error paths: app is nil there, and
	// stopPersistWriter tolerates a nil receiver, so this stays a single
	// unconditional rule rather than a condition to get wrong.
	stopPersistWriterOnCleanup(t, app)
	return app, err
}

// newLargeWorkspaceAppForTest is the same wrapper for the benchmark fixture's
// constructor, which lives in workspace_fixture.go — also outside _test.go.
func newLargeWorkspaceAppForTest(t testing.TB, dir string, opts largeWorkspaceOptions) *App {
	t.Helper()
	return stopPersistWriterOnCleanup(t, newLargeWorkspaceApp(dir, opts))
}

// TestEveryTestAppGoesThroughAWriterStoppingConstructor keeps the sweep above
// from rotting. An App built directly in a test runs a background writer that
// outlives the test by up to a debounce interval, and writes state.json into a
// directory t.TempDir() is concurrently removing — a cleanup failure that
// reproduces on roughly one run in eight and blames whichever test happens to
// be holding the directory.
//
// Rather than trust that nobody reintroduces one, this fails the build the
// moment a raw constructor appears in a _test.go file. The wrappers above are
// the only sanctioned way in, and each of them registers the stop.
func TestEveryTestAppGoesThroughAWriterStoppingConstructor(t *testing.T) {
	raw := regexp.MustCompile(`\b(NewAppWithDir|newProductionApp|newLargeWorkspaceApp)\(`)
	sanctioned := regexp.MustCompile(`\b(newAppForTest|newAppInDirForTest|newProductionAppForTest|newLargeWorkspaceAppForTest|stopPersistWriterOnCleanup)\b`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	offenders := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for index, line := range strings.Split(string(data), "\n") {
			if !raw.MatchString(line) || sanctioned.MatchString(line) {
				continue
			}
			offenders = append(offenders, fmt.Sprintf("%s:%d: %s", name, index+1, strings.TrimSpace(line)))
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these test Apps never have their background persist writer stopped, so they can write into a t.TempDir() being removed; use newAppForTest/newAppInDirForTest instead:\n%s", strings.Join(offenders, "\n"))
	}
}

// TestStopPersistWriterIsSynchronousIdempotentAndKeepsFlushWorking pins the
// three properties the cleanup ordering depends on. None of them is asserted by
// waiting: "the writer has stopped" is read off persistRunning, which the writer
// clears under persistMu before the channel stopPersistWriter blocks on is
// closed. A test that had to sleep to see this would be asserting nothing.
func TestStopPersistWriterIsSynchronousIdempotentAndKeepsFlushWorking(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)

	path := filepath.Join(dir, "state.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state.json exists before any write: %v", err)
	}

	if err := app.markDirty(persistScopeState); err != nil {
		t.Fatalf("markDirty: %v", err)
	}
	app.persistMu.Lock()
	running := app.persistRunning
	app.persistMu.Unlock()
	if !running {
		t.Fatal("markDirty did not start a background writer; the rest of this test would prove nothing")
	}

	app.stopPersistWriter()

	app.persistMu.Lock()
	running = app.persistRunning
	dirty := app.persistDirty
	app.persistMu.Unlock()
	if running {
		t.Fatal("stopPersistWriter returned while the writer was still running; it is not synchronous")
	}
	// Stopping discards nothing: the mutation is still pending, it simply has
	// no background writer left to carry it.
	if !dirty {
		t.Fatal("stopPersistWriter cleared the dirty flag; the pending mutation would be lost")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a stopped writer still wrote state.json: %v", err)
	}

	// Idempotent, and still synchronous the second time.
	app.stopPersistWriter()

	// A mutation arriving after the stop — a straggling goroutine, say — must
	// not resurrect the writer, or the temp directory is unsafe again.
	if err := app.markDirty(persistScopeState); err != nil {
		t.Fatalf("markDirty after stop: %v", err)
	}
	app.persistMu.Lock()
	running = app.persistRunning
	app.persistMu.Unlock()
	if running {
		t.Fatal("markDirty started a new writer after stopPersistWriter; the stop does not stick")
	}

	// And the synchronous path still works, which is what shutdown relies on.
	flushPersistForTest(t, app)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("flushPersist after stopPersistWriter did not write state.json: %v", err)
	}
}

// TestStopPersistWriterOnAppThatNeverStartedOne covers the bare-literal and
// never-mutated cases: the stop is registered unconditionally by the test
// helpers, so it has to be a no-op rather than a nil-channel hang.
func TestStopPersistWriterOnAppThatNeverStartedOne(t *testing.T) {
	newAppForTest(t).stopPersistWriter()
	(&App{}).stopPersistWriter()
	var absent *App
	absent.stopPersistWriter()
}

// flushPersistForTest force-writes the app's coalesced state. Every assertion
// whose meaning is "the mutation reached disk" — reading state.json, or loading
// a second App from the same directory — must go through this first.
func flushPersistForTest(t testing.TB, app *App) {
	t.Helper()
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}
}

// waitForPersistWrites blocks until the background writer has completed at
// least want writes, or fails after a deadline generous enough that only a
// genuinely broken writer trips it.
func waitForPersistWrites(t testing.TB, app *App, want uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		app.persistMu.Lock()
		got := app.persistWrites
		app.persistMu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background writer completed %d writes, want >= %d", got, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func persistedState(t testing.TB, dir string) AppState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var state AppState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse state.json: %v", err)
	}
	return state
}

// TestPersistStateJSONStaysParsableUnderConcurrentWrites is the "kill
// mid-write" acceptance test in its deterministic form. A writer republishes a
// multi-megabyte state in a tight loop while a reader parses state.json as fast
// as it can. Every observation the reader makes must be a complete document.
//
// Against the pre-US-012 implementation this fails immediately: os.WriteFile
// truncates the target before writing, so the reader observes an empty or
// half-written file within the first few iterations.
func TestPersistStateJSONStaysParsableUnderConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	// Sized so that a single republish takes long enough to have a real
	// truncate window, while still leaving the reader time for hundreds of
	// samples. A multi-megabyte state would give the reader only a dozen.
	opts := defaultLargeWorkspaceOptions()
	opts.Collections = 2
	opts.RequestsPerColl = 5
	opts.LargeResponses = 1
	opts.LargeResponseSize = 256 << 10
	app := newLargeWorkspaceAppForTest(t, dir, opts)
	// The fixture bypasses the binding layer, so publish it once before the
	// race starts. NewAppWithDir alone does not write state.json.
	app.mu.Lock()
	err := app.persistLocked()
	app.mu.Unlock()
	if err != nil {
		t.Fatalf("seed state.json: %v", err)
	}

	path := filepath.Join(dir, "state.json")
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat state.json: %v", err)
	} else if info.Size() < 256<<10 {
		t.Fatalf("fixture state.json is only %d bytes; the race window would be too narrow to mean anything", info.Size())
	}

	const rounds = 200
	var stop atomic.Bool
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stop.Store(true)
		for i := 0; i < rounds; i++ {
			app.mu.Lock()
			app.state.Workspaces[0].Name = fmt.Sprintf("Fixture Workspace %d", i)
			err := app.persistLocked()
			app.mu.Unlock()
			if err != nil {
				t.Errorf("persistLocked round %d: %v", i, err)
				return
			}
		}
	}()

	reads := 0
	for !stop.Load() {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				t.Errorf("state.json disappeared during a write; the replacement is not atomic")
				break
			}
			t.Errorf("read state.json: %v", err)
			break
		}
		reads++
		var observed AppState
		if err := json.Unmarshal(data, &observed); err != nil {
			t.Fatalf("observed a state.json that does not parse after %d clean reads (%d bytes): %v", reads, len(data), err)
		}
		if len(observed.Workspaces) == 0 {
			t.Fatalf("observed a state.json with no workspaces after %d clean reads (%d bytes)", reads, len(data))
		}
	}
	wg.Wait()

	if reads == 0 {
		t.Fatal("reader never observed the file; the test proved nothing")
	}
	t.Logf("reader parsed %d complete state.json snapshots across %d republishes", reads, rounds)
}

const persistKillHelperEnv = "LITEAPI_PERSIST_KILL_HELPER_DIR"

// TestPersistKillHelperProcess is not a test. It is the child process spawned
// by TestPersistStateJSONSurvivesSIGKILL: it republishes a large state forever
// until its parent kills it.
func TestPersistKillHelperProcess(t *testing.T) {
	dir := os.Getenv(persistKillHelperEnv)
	if dir == "" {
		t.Skip("helper process; driven by TestPersistStateJSONSurvivesSIGKILL")
	}
	opts := defaultLargeWorkspaceOptions()
	opts.Collections = 4
	opts.RequestsPerColl = 10
	opts.LargeResponses = 2
	opts.LargeResponseSize = 2 << 20
	app := newLargeWorkspaceAppForTest(t, dir, opts)
	app.mu.Lock()
	seedErr := app.persistLocked()
	app.mu.Unlock()
	if seedErr != nil {
		fmt.Fprintln(os.Stderr, "helper seed:", seedErr)
		os.Exit(2)
	}
	// Announce readiness so the parent kills during a write rather than during
	// fixture construction.
	fmt.Println("ready")
	_ = os.Stdout.Sync()
	for i := 0; ; i++ {
		app.mu.Lock()
		app.state.Workspaces[0].Name = fmt.Sprintf("Fixture Workspace %d", i)
		err := app.persistLocked()
		app.mu.Unlock()
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper persist:", err)
			os.Exit(2)
		}
	}
}

// TestPersistStateJSONSurvivesSIGKILL is the acceptance criterion taken
// literally: kill the process mid-write, then parse state.json. A child process
// republishes a multi-megabyte state in a loop and is SIGKILLed at a series of
// offsets; after every kill the file on disk must still be a complete document.
func TestPersistStateJSONSurvivesSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Offsets chosen to land inside the write rather than between writes: one
	// republish of this fixture takes tens of milliseconds.
	for round, delay := range []time.Duration{
		3 * time.Millisecond,
		7 * time.Millisecond,
		11 * time.Millisecond,
		17 * time.Millisecond,
		23 * time.Millisecond,
		31 * time.Millisecond,
		43 * time.Millisecond,
		61 * time.Millisecond,
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPersistKillHelperProcess$", "-test.v")
		cmd.Env = append(os.Environ(), persistKillHelperEnv+"="+dir)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("round %d: stdout pipe: %v", round, err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("round %d: start helper: %v", round, err)
		}

		ready := make(chan struct{})
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := stdout.Read(buf)
				if n > 0 && strings.Contains(string(buf[:n]), "ready") {
					close(ready)
					return
				}
				if err != nil {
					close(ready)
					return
				}
			}
		}()
		select {
		case <-ready:
		case <-time.After(60 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("round %d: helper never became ready", round)
		}

		time.Sleep(delay)
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
			t.Fatalf("round %d: SIGKILL: %v", round, err)
		}
		_ = cmd.Wait()

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("round %d: read state.json after SIGKILL: %v", round, err)
		}
		var state AppState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatalf("round %d: state.json (%d bytes) does not parse after SIGKILL at +%s: %v", round, len(data), delay, err)
		}
		if len(state.Workspaces) == 0 {
			t.Fatalf("round %d: state.json parsed but lost every workspace after SIGKILL at +%s", round, delay)
		}

		// A crashed write must not leave its scratch file behind for the next
		// process to trip over.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("round %d: read data dir: %v", round, err)
		}
		leftovers := 0
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".state.json.tmp-") {
				leftovers++
			}
		}
		if leftovers > 1 {
			t.Fatalf("round %d: %d orphaned temp files left behind by a single kill", round, leftovers)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".state.json.tmp-") {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
}

// TestMarkDirtyDoesNotWriteInlineAndBackgroundWriterCoalesces pins the two
// halves of the story: a mutation returns without touching the disk, and a
// burst of mutations produces one write rather than one per mutation.
func TestMarkDirtyDoesNotWriteInlineAndBackgroundWriterCoalesces(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}
	flushPersistForTest(t, app)

	path := filepath.Join(dir, "state.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state.json: %v", err)
	}

	app.persistMu.Lock()
	writesBefore := app.persistWrites
	app.persistMu.Unlock()

	// A burst comfortably larger than a human keystroke run, issued well inside
	// one debounce window.
	const burst = 50
	started := time.Now()
	for i := 0; i < burst; i++ {
		if err := app.markDirty(persistScopeState); err != nil {
			t.Fatalf("markDirty %d: %v", i, err)
		}
	}
	if elapsed := time.Since(started); elapsed >= persistDebounceInterval {
		t.Fatalf("%d marks took %s, longer than the %s debounce; they are still doing the expensive work inline", burst, elapsed, persistDebounceInterval)
	}

	// Nothing may have reached disk yet: the debounce has not elapsed.
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state.json: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("markDirty wrote state.json inline; persistence is still synchronous")
	}

	waitForPersistWrites(t, app, writesBefore+1)

	app.persistMu.Lock()
	writes := app.persistWrites - writesBefore
	app.persistMu.Unlock()
	if writes > 2 {
		t.Fatalf("%d marks produced %d background writes; they did not coalesce", burst, writes)
	}
	t.Logf("%d marks coalesced into %d background write(s)", burst, writes)
}

// TestFlushPersistLosesNoMutation covers the "no mutation is lost across a
// force-flush boundary" criterion, including the case where the flush races the
// background writer.
func TestFlushPersistLosesNoMutation(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)

	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	for i := 0; i < 50; i++ {
		preferences.Display.ZoomPercentage = 100 + i
		if _, err := app.UpdatePreferences(preferences); err != nil {
			t.Fatalf("UpdatePreferences %d: %v", i, err)
		}
	}
	flushPersistForTest(t, app)

	persisted := persistedState(t, dir)
	if persisted.Preferences.Display.ZoomPercentage != 149 {
		t.Fatalf("flush persisted zoom %d, want the last mutation 149", persisted.Preferences.Display.ZoomPercentage)
	}

	// Mutate again, then flush repeatedly: the last value must always win, and
	// a flush that finds nothing dirty must not be an error.
	preferences.Display.ZoomPercentage = 121
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		flushPersistForTest(t, app)
	}
	if got := persistedState(t, dir).Preferences.Display.ZoomPercentage; got != 121 {
		t.Fatalf("repeated flush persisted zoom %d, want 121", got)
	}

	// And a reload — the pattern the rest of the suite uses — sees it too.
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.Display.ZoomPercentage != 121 {
		t.Fatalf("reloaded zoom %d, want 121", reloadedState.Preferences.Display.ZoomPercentage)
	}
}

// TestBackgroundWriteFailureIsSurfacedAndRetried is the §8 risk 4 test: a
// background write has no caller, so a failure must not evaporate. It has to
// reach the user (notification), reach the next mutation (returned error), and
// leave the state dirty so a recovered directory is written after all.
func TestBackgroundWriteFailureIsSurfacedAndRetried(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	app := newAppInDirForTest(t, dir)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}
	flushPersistForTest(t, app)

	// Make the state.json path unwritable in a way only the deferred half of
	// persistence trips over: a non-empty directory sitting where the file
	// belongs, so os.Rename cannot replace it. The synchronous secret store
	// beside it keeps working, which is what isolates the background failure.
	statePath := filepath.Join(dir, "state.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(statePath, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := app.markDirty(persistScopeState); err != nil {
		t.Fatalf("first markDirty should not report a stale error: %v", err)
	}

	// Wait for the writer to have tried and failed.
	deadline := time.Now().Add(10 * time.Second)
	for {
		app.persistMu.Lock()
		failures := app.persistFailures
		dirty := app.persistDirty
		app.persistMu.Unlock()
		if failures > 0 {
			if !dirty {
				t.Fatal("a failed background write cleared the dirty flag; the mutation would be lost")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background writer never reported a failure against an unwritable data directory")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// The failure is parked for the next mutation rather than dropped.
	if err := app.markDirty(persistScopeState); err == nil {
		t.Fatal("markDirty returned nil after a failed background write; the error vanished")
	}

	// And it reached the user.
	app.mu.Lock()
	notifications := append([]Notification(nil), app.state.Notifications...)
	app.mu.Unlock()
	found := false
	for _, notification := range notifications {
		if notification.Level == "error" && strings.Contains(notification.Message, "Could not save workspace state") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no error notification was raised for the failed background write: %#v", notifications)
	}

	// Clear the obstruction. Nothing else mutates the app, so the state can
	// only land if the writer kept retrying the still-dirty scope on its own.
	app.persistMu.Lock()
	writesBefore := app.persistWrites
	app.persistMu.Unlock()
	if err := os.RemoveAll(statePath); err != nil {
		t.Fatal(err)
	}
	waitForPersistWrites(t, app, writesBefore+1)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state.json was never written after the obstruction cleared: %v", err)
	}
}

// TestShutdownFlushesPendingState covers the lifecycle wiring: a mutation still
// inside the debounce window must be on disk once shutdown returns.
func TestShutdownFlushesPendingState(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)

	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Display.ZoomPercentage = 133
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	if got := persistedState(t, dir).Preferences.Display.ZoomPercentage; got == 133 {
		t.Fatal("the mutation was written inline; there is nothing for shutdown to flush")
	}

	app.shutdown(context.TODO())

	if got := persistedState(t, dir).Preferences.Display.ZoomPercentage; got != 133 {
		t.Fatalf("shutdown left zoom %d on disk, want the pending 133", got)
	}
}

// TestFlushPendingWritesBindingFlushes covers the exported binding the
// workbench calls on window blur.
func TestFlushPendingWritesBindingFlushes(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)

	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Display.ZoomPercentage = 142
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	if err := app.FlushPendingWrites(); err != nil {
		t.Fatalf("FlushPendingWrites: %v", err)
	}
	if got := persistedState(t, dir).Preferences.Display.ZoomPercentage; got != 142 {
		t.Fatalf("FlushPendingWrites left zoom %d on disk, want 142", got)
	}
}

// TestWriteFileAtomicPreservesDirectoryMode guards the reason atomicfile.Write
// exists alongside writePrivateAtomic: the data directory's mode is meaningful
// to the multi-process window model and must not be tightened as a side effect
// of writing a file inside it.
func TestWriteFileAtomicPreservesDirectoryMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	if err := atomicfile.Write(path, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatalf("atomicfile.Write: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("data directory mode changed to %o", info.Mode().Perm())
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("written file mode is %o, want 600", fileInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("atomicfile.Write left %d entries behind, want only the target", len(entries))
	}
}

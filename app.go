package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
	"github.com/mutexdev/lite_api/internal/auth/oauth1"
	"github.com/mutexdev/lite_api/internal/auth/wsse"
	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
	"github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"google.golang.org/grpc"
)

var environmentSecretMachineIDOnce sync.Once
var environmentSecretMachineIDValue string

const networkLogBodyLimit = 64 * 1024
const terminalOutputLimit = 256 * 1024
const brunoOAuth2DefaultCallbackURL = "https://oauth.usebruno.com/callback"
const oauth2ProtocolCallbackHost = "app"
const oauth2ProtocolCallbackPath = "/oauth2/callback"

var devToolsNetworkDefaultColumnWidths = []int{80, 70, 180, 300, 110, 100, 80}

const devToolsDefaultDrawerHeight = 320
const closedTabHistoryLimit = 50

var devToolsTabs = map[string]bool{
	"console":     true,
	"network":     true,
	"performance": true,
	"terminal":    true,
}

var devToolsNetworkSortKeys = map[string]bool{
	"method":   true,
	"status":   true,
	"domain":   true,
	"path":     true,
	"time":     true,
	"duration": true,
	"size":     true,
}

type App struct {
	ctx context.Context
	// mu guards state and every field derived from it. It is an RWMutex, but
	// RLock is reserved for call sites whose *entire* dynamic call tree is
	// provably read-only. Note that ensureReadyLocked mutates unconditionally
	// (see its comment), so any path that calls it must take the write lock.
	mu                          sync.RWMutex
	oauth2Mu                    sync.Mutex
	websocketMu                 sync.Mutex
	grpcStreamMu                sync.Mutex
	terminalMu                  sync.Mutex
	cpuMu                       sync.Mutex
	state                       AppState
	dataDir                     string
	httpClient                  *http.Client
	tlsSessionCache             tls.ClientSessionCache
	collectionWatchFingerprints map[string]string
	// collectionFileFingerprints (US-015) records the SHA-256 of what this App
	// last wrote to each collection file, so saving one request stops
	// rewriting bruno.json, collection.bru, every environment file and every
	// other request file. Keyed by absolute path, guarded by a.mu, and lazily
	// populated — see writeCollectionFileLocked.
	collectionFileFingerprints map[string]string
	oauth2OpenURL              func(context.Context, string) error
	oauth2OpenInAppURL         func(context.Context, oauth2AuthorizationBrowserRequest) error
	revealInFolder             func(string) error
	oauth2CallbackTimeout      time.Duration
	oauth2                     map[string]oauth2TokenResponse
	oauth2Baseline             map[string]oauth2TokenResponse
	oauth2PendingMu            sync.Mutex
	oauth2Authorization        map[string]chan oauth2AuthorizationResult
	oauth2Implicit             map[string]chan oauth2ImplicitResult
	websocketSessions          map[string]*websocketSession
	grpcStreamSessions         map[string]*grpcStreamSession
	terminals                  map[string]*terminalSessionProcess
	startedAt                  time.Time
	lastCPUTime                time.Duration
	lastCPUWall                time.Time
	requests                   *requestLifecycleRegistry
	collectionRuns             *collectionRunLifecycleRegistry
	// transportCache (US-016) keys one *http.Transport per outbound security
	// posture so requests reuse connections instead of cloning an empty pool
	// per send. Its own lock is a leaf below a.mu; see http_transport_cache.go.
	// The zero value is usable, so an App built as a bare literal works too.
	transportCache         httpTransportCache
	workspaceRuntime       *workspaceWindowRuntime
	workspaceProcessStart  func(string, []string) error
	collectionImportHooks  *collectionImportHooks
	gitWorkbenchExecutable string
	gitWorkbenchPersist    func() error

	// Coalesced persistence (US-012). persistMu is a leaf lock: it is never
	// held while acquiring a.mu, so markDirty stays safe to call from the
	// keystroke path with a.mu already held.
	persistMu       sync.Mutex
	persistDirty    bool
	persistRunning  bool
	persistErr      error
	persistFailures int
	persistWrites   uint64
	// Writer lifecycle. persistStop/persistDone belong to the writer currently
	// running: stopPersistWriter closes the first and waits on the second, so
	// "the writer has stopped" is an observable fact rather than a hope.
	// persistStopped is sticky, so no later markDirty can start a replacement.
	persistStop    chan struct{}
	persistDone    chan struct{}
	persistStopped bool

	// revision (US-008) is the authoritative monotonic mutation counter, and
	// AppState.Revision is only ever a stamped copy of it. Keeping it on the
	// App rather than in the state is what makes monotonicity unconditional:
	// several paths restore a whole previous AppState — the collection-import
	// rollbacks and the OpenAPI-sync rollback at ImportCollection — and a
	// counter living inside that value would travel backwards with it.
	// Guarded by a.mu, like a.state itself.
	revision int64

	// US-009. The response body store. Lazily created on first use rather than
	// in the constructor: newAppBase is called for every test App and for the
	// multi-window runtime, and creating <dataDir>/responses/ eagerly would make
	// a directory for Apps that never store a response. Guarded by responsesMu,
	// which is a leaf lock — never held while acquiring a.mu.
	responsesMu sync.Mutex
	// US-048. History lives outside state.json; see history_store.go for why.
	historyOnce  sync.Once
	historyStore *historyStore
	// US-072. Per-collection mock listeners, guarded by their own mutex so
	// binding a socket never happens under the state lock.
	mockOnce    sync.Once
	mockMu      sync.Mutex
	mockServers map[string]*mockServer
	// US-074. Docs preview listeners, same isolation as the mocks.
	docsOnce    sync.Once
	docsMu      sync.Mutex
	docsServers map[string]*docsServer
	responses   *responseStore

	// US-013. Fingerprints of what each auxiliary file last contained, so a
	// persist that changes nothing in a file does no work for that file.
	// secretsFingerprint is guarded by a.mu; oauth2Fingerprint by a.oauth2Mu.
	secretsFingerprint string
	oauth2Fingerprint  string
}

type terminalSessionProcess struct {
	id        string
	cwd       string
	cmd       *exec.Cmd
	file      *os.File
	pid       int
	output    []byte
	exited    bool
	exitCode  int
	signal    string
	createdAt time.Time
	updatedAt time.Time
}

type websocketSession struct {
	mu             sync.Mutex
	conn           *websocket.Conn
	targetURL      string
	status         int
	statusText     string
	headers        map[string]string
	timeout        time.Duration
	keepAliveEvery time.Duration
	openedAt       time.Time
	events         []websocketSessionEvent
	closed         bool
	closeReason    string
	done           chan struct{}
	doneClosed     bool
	lastActivityAt time.Time
	// emit pushes each appended event to the frontend (US-021). nil when the
	// session was built outside a Wails context, which is every test.
	emit func(index, total int, event websocketSessionEvent)
}

type websocketSessionEvent struct {
	Direction  string    `json:"direction"`
	Name       string    `json:"name,omitempty"`
	Type       string    `json:"type,omitempty"`
	Data       string    `json:"data,omitempty"`
	DataBase64 string    `json:"dataBase64,omitempty"`
	DataHex    string    `json:"dataHex,omitempty"`
	Error      string    `json:"error,omitempty"`
	At         time.Time `json:"at"`
}

type grpcStreamSession struct {
	mu              sync.Mutex
	conn            *grpc.ClientConn
	stream          grpc.ClientStream
	binding         grpcMethodBinding
	cancel          context.CancelFunc
	targetURL       string
	streamType      string
	status          int
	statusText      string
	headers         map[string]string
	trailers        map[string]string
	timeout         time.Duration
	openedAt        time.Time
	events          []grpcStreamSessionEvent
	eventNotify     chan struct{}
	receiveDone     chan struct{}
	receiverStarted bool
	closed          bool
	ended           bool
	closeReason     string
	requestCount    int
	responseCount   int
	lastActivityAt  time.Time
	// emit pushes each appended event to the frontend (US-022). See the
	// websocketSession field of the same name.
	emit func(index, total int, event grpcStreamSessionEvent)
}

type grpcStreamSessionEvent struct {
	Direction string    `json:"direction"`
	Name      string    `json:"name,omitempty"`
	Type      string    `json:"type,omitempty"`
	Data      string    `json:"data,omitempty"`
	Error     string    `json:"error,omitempty"`
	At        time.Time `json:"at"`
}

type collectionExportFile struct {
	Name    string
	Content []byte
}

type environmentSecretsFile struct {
	Collections []environmentSecretCollection `json:"collections"`
	Workspaces  []environmentSecretWorkspace  `json:"workspaces,omitempty"`
}

type environmentSecretWorkspace struct {
	Path         string                         `json:"path"`
	Environments []environmentSecretEnvironment `json:"environments"`
}

type environmentSecretCollection struct {
	Path         string                         `json:"path"`
	Environments []environmentSecretEnvironment `json:"environments"`
}

type environmentSecretEnvironment struct {
	Name    string                      `json:"name"`
	Secrets []environmentSecretVariable `json:"secrets"`
}

type environmentSecretVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func NewApp() *App {
	dir := defaultDataDir()
	return NewAppWithDir(dir)
}

func NewAppWithDir(dir string) *App {
	app := newAppBase(dir)
	app.state = defaultState(dir)
	_ = app.loadOAuth2Credentials()
	_ = app.load()
	return app
}

func newAppBase(dir string) *App {
	app := &App{
		dataDir:                     dir,
		httpClient:                  &http.Client{Timeout: 30 * time.Second},
		collectionWatchFingerprints: map[string]string{},
		oauth2OpenURL:               defaultOAuth2OpenURL,
		oauth2OpenInAppURL:          defaultOAuth2OpenInAppURL,
		revealInFolder:              defaultRevealInFolder,
		oauth2CallbackTimeout:       2 * time.Minute,
		oauth2:                      map[string]oauth2TokenResponse{},
		oauth2Baseline:              map[string]oauth2TokenResponse{},
		oauth2Authorization:         map[string]chan oauth2AuthorizationResult{},
		oauth2Implicit:              map[string]chan oauth2ImplicitResult{},
		websocketSessions:           map[string]*websocketSession{},
		grpcStreamSessions:          map[string]*grpcStreamSession{},
		terminals:                   map[string]*terminalSessionProcess{},
		startedAt:                   time.Now(),
		workspaceProcessStart: func(name string, args []string) error {
			return exec.Command(name, args...).Start()
		},
	}
	return app
}

func defaultOAuth2OpenURL(ctx context.Context, targetURL string) error {
	if ctx != nil {
		wailsruntime.BrowserOpenURL(ctx, targetURL)
		return nil
	}
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

func defaultRevealInFolder(targetPath string) error {
	if os.Getenv("LITEAPI_REVEAL_IN_FOLDER_DRY_RUN") == "1" {
		return nil
	}
	name, args, err := revealInFolderCommand(targetPath)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

func revealInFolderCommand(targetPath string) (string, []string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return "", nil, errors.New("path is required")
	}
	absPath, err := filepath.Abs(targetPath)
	if err == nil {
		targetPath = absPath
	}
	switch goruntime.GOOS {
	case "darwin":
		return "open", []string{"-R", targetPath}, nil
	case "windows":
		return "explorer.exe", []string{"/select," + targetPath}, nil
	default:
		revealPath := targetPath
		if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
			revealPath = filepath.Dir(targetPath)
		}
		return "xdg-open", []string{revealPath}, nil
	}
}

type oauth2AuthorizationBrowserRequest struct {
	AuthorizeURL string `json:"authorizeUrl"`
	CallbackURL  string `json:"callbackUrl"`
	GrantType    string `json:"grantType"`
}

func defaultOAuth2OpenInAppURL(ctx context.Context, request oauth2AuthorizationBrowserRequest) error {
	if ctx == nil {
		return defaultOAuth2OpenURL(ctx, request.AuthorizeURL)
	}
	wailsruntime.EventsEmit(ctx, "oauth2:authorize", request)
	return nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.workspaceRuntime != nil {
		a.workspaceRuntime.startHeartbeat()
		a.workspaceRuntime.restoreGeometry(ctx)
	}
	_ = a.ensureReady()
}

func (a *App) handleOpenURL(rawURL string) {
	_, _ = a.CompleteOAuth2Callback(rawURL)
}

func (a *App) handleSecondInstanceArgs(args []string) {
	for _, arg := range args {
		if oauth2IsAppProtocolCallback(arg) {
			a.handleOpenURL(arg)
			return
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	// US-072. Before anything else: a mock listener left bound outlives the
	// process on some platforms and blocks its own port on the next launch,
	// which reads as "the mock server is broken" rather than "the last one is
	// still holding the socket".
	a.stopAllMockServers()
	a.stopAllDocsServers()
	// Retire the background writer before the flush, not after. It waits out a
	// write already in flight, so the flush below is the last write of the
	// process: nothing can land concurrently with — or after — the workspace
	// runtime release, which gives up the ownership lease
	// persistWorkspaceRuntimeLocked needs. Stopping loses nothing, because the
	// flush that follows is synchronous and writes whatever is still dirty.
	a.stopPersistWriter()
	// Force-flush: state still inside the debounce window is data loss once the
	// process exits.
	if err := a.flushPersist(); err != nil {
		a.logPersistError("Could not save workspace state on exit: " + err.Error())
	}
	if a.workspaceRuntime != nil {
		a.workspaceRuntime.captureGeometry(ctx)
		a.workspaceRuntime.release()
	}
	a.websocketMu.Lock()
	websocketSessions := make([]*websocketSession, 0, len(a.websocketSessions))
	for _, session := range a.websocketSessions {
		websocketSessions = append(websocketSessions, session)
	}
	a.websocketSessions = map[string]*websocketSession{}
	a.websocketMu.Unlock()

	for _, session := range websocketSessions {
		session.close("shutdown")
	}

	a.grpcStreamMu.Lock()
	grpcStreamSessions := make([]*grpcStreamSession, 0, len(a.grpcStreamSessions))
	for _, session := range a.grpcStreamSessions {
		grpcStreamSessions = append(grpcStreamSessions, session)
	}
	a.grpcStreamSessions = map[string]*grpcStreamSession{}
	a.grpcStreamMu.Unlock()

	for _, session := range grpcStreamSessions {
		session.close("shutdown")
	}

	a.terminalMu.Lock()
	sessions := make([]*terminalSessionProcess, 0, len(a.terminals))
	for _, session := range a.terminals {
		sessions = append(sessions, session)
	}
	a.terminals = map[string]*terminalSessionProcess{}
	a.terminalMu.Unlock()

	for _, session := range sessions {
		if session.file != nil {
			_ = session.file.Close()
		}
		if session.cmd != nil && session.cmd.Process != nil {
			_ = session.cmd.Process.Kill()
		}
	}
}

func (a *App) GetState() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	// Re-stamp rather than bump: a read must never advance the counter, but it
	// must also never report a revision older than the last mutation. The stamp
	// inside a.state can lag a.revision after a path that restores a whole
	// earlier AppState (see App.revision).
	a.state.Revision = a.revision
	return a.state, nil
}

func (a *App) GetDevToolsSnapshot() (DevToolsSnapshot, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return DevToolsSnapshot{}, err
	}
	startedAt := a.startedAt
	networkRequests := len(a.state.NetworkLog)
	consoleLogs := countScriptLogsInState(a.state)
	a.mu.Unlock()

	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	var mem goruntime.MemStats
	goruntime.ReadMemStats(&mem)
	uptimeSeconds := int64(time.Since(startedAt).Seconds())
	cpuPercent := a.sampleDevToolsCPU(time.Now())
	return DevToolsSnapshot{
		PID:             os.Getpid(),
		CPUPercent:      cpuPercent,
		UptimeSeconds:   uptimeSeconds,
		MemoryBytes:     mem.Sys,
		HeapAllocBytes:  mem.HeapAlloc,
		Goroutines:      goruntime.NumGoroutine(),
		NetworkRequests: networkRequests,
		ConsoleLogs:     consoleLogs,
		Processes: []DevToolsProcessMetric{
			{
				PID:           os.Getpid(),
				Title:         "LiteAPI",
				Type:          "main",
				CPUPercent:    cpuPercent,
				MemoryBytes:   mem.Sys,
				UptimeSeconds: uptimeSeconds,
			},
		},
		Timestamp: time.Now(),
	}, nil
}

func (a *App) sampleDevToolsCPU(now time.Time) float64 {
	cpuTime, ok := currentProcessCPUTime()
	if !ok {
		return 0
	}
	a.cpuMu.Lock()
	defer a.cpuMu.Unlock()
	percent := calculateCPUPercent(a.lastCPUTime, a.lastCPUWall, cpuTime, now, goruntime.NumCPU())
	a.lastCPUTime = cpuTime
	a.lastCPUWall = now
	return percent
}

func calculateCPUPercent(previousCPU time.Duration, previousWall time.Time, currentCPU time.Duration, currentWall time.Time, cpuCount int) float64 {
	if currentCPU < 0 || cpuCount <= 0 {
		return 0
	}
	if previousWall.IsZero() || previousCPU <= 0 || !currentWall.After(previousWall) || currentCPU < previousCPU {
		return 0
	}
	wallDelta := currentWall.Sub(previousWall)
	if wallDelta <= 0 {
		return 0
	}
	percent := (float64(currentCPU-previousCPU) / float64(wallDelta)) * 100 / float64(cpuCount)
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 {
		return 0
	}
	return math.Round(percent*10) / 10
}

func (a *App) CreateTerminalSession(cwd string) (TerminalSession, error) {
	if a.terminals == nil {
		a.terminals = map[string]*terminalSessionProcess{}
	}
	shell := defaultTerminalShell()
	sessionCWD := normalizeTerminalCWD(cwd, a.dataDir)
	cmd := exec.Command(shell)
	cmd.Dir = sessionCWD
	cmd.Env = os.Environ()
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		return TerminalSession{}, fmt.Errorf("create terminal session: %w", err)
	}
	now := time.Now()
	session := &terminalSessionProcess{
		id:        newID("terminal"),
		cwd:       sessionCWD,
		cmd:       cmd,
		file:      file,
		pid:       cmd.Process.Pid,
		createdAt: now,
		updatedAt: now,
	}
	a.terminalMu.Lock()
	a.terminals[session.id] = session
	snapshot := terminalSessionSnapshotLocked(session)
	a.terminalMu.Unlock()
	go a.readTerminalSession(session.id, file, cmd)
	return snapshot, nil
}

func (a *App) ListTerminalSessions() ([]TerminalSession, error) {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	sessions := make([]TerminalSession, 0, len(a.terminals))
	for _, session := range a.terminals {
		sessions = append(sessions, terminalSessionSnapshotLocked(session))
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt < sessions[j].CreatedAt
	})
	return sessions, nil
}

func (a *App) GetTerminalSession(sessionID string) (TerminalSession, error) {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	session, ok := a.terminals[sessionID]
	if !ok {
		return TerminalSession{}, errors.New("terminal session not found")
	}
	return terminalSessionSnapshotLocked(session), nil
}

func (a *App) WriteTerminalSession(sessionID, data string) (TerminalSession, error) {
	a.terminalMu.Lock()
	session, ok := a.terminals[sessionID]
	if !ok {
		a.terminalMu.Unlock()
		return TerminalSession{}, errors.New("terminal session not found")
	}
	if session.exited {
		snapshot := terminalSessionSnapshotLocked(session)
		a.terminalMu.Unlock()
		return snapshot, nil
	}
	file := session.file
	a.terminalMu.Unlock()

	if data != "" && file != nil {
		if _, err := file.WriteString(data); err != nil {
			return TerminalSession{}, fmt.Errorf("write terminal session: %w", err)
		}
	}
	return a.GetTerminalSession(sessionID)
}

func (a *App) ResizeTerminalSession(sessionID string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	a.terminalMu.Lock()
	session, ok := a.terminals[sessionID]
	if !ok {
		a.terminalMu.Unlock()
		return errors.New("terminal session not found")
	}
	file := session.file
	exited := session.exited
	a.terminalMu.Unlock()
	if exited || file == nil {
		return nil
	}
	return pty.Setsize(file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (a *App) KillTerminalSession(sessionID string) error {
	a.terminalMu.Lock()
	session, ok := a.terminals[sessionID]
	if !ok {
		a.terminalMu.Unlock()
		return nil
	}
	delete(a.terminals, sessionID)
	a.terminalMu.Unlock()

	if session.file != nil {
		_ = session.file.Close()
	}
	if session.cmd != nil && session.cmd.Process != nil && !session.exited {
		_ = session.cmd.Process.Kill()
	}
	return nil
}

func (a *App) readTerminalSession(sessionID string, file *os.File, cmd *exec.Cmd) {
	buf := make([]byte, 4096)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			a.terminalMu.Lock()
			if session, ok := a.terminals[sessionID]; ok {
				appendTerminalOutputLocked(session, buf[:n])
			}
			a.terminalMu.Unlock()
		}
		if err != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if waitErr != nil {
		exitCode = -1
	}
	a.terminalMu.Lock()
	if session, ok := a.terminals[sessionID]; ok {
		session.exited = true
		session.exitCode = exitCode
		appendTerminalOutputLocked(session, []byte(fmt.Sprintf("\r\n[Process exited with code %d]\r\n", exitCode)))
	}
	a.terminalMu.Unlock()
	_ = file.Close()
}

func defaultTerminalShell() string {
	if goruntime.GOOS == "windows" {
		if shell := strings.TrimSpace(os.Getenv("PWSH")); shell != "" {
			return shell
		}
		return "powershell.exe"
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	return "/bin/bash"
}

func normalizeTerminalCWD(cwd, fallback string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(os.Getenv("HOME"))
	}
	if cwd == "" {
		cwd = strings.TrimSpace(os.Getenv("USERPROFILE"))
	}
	if cwd == "" {
		cwd = fallback
	}
	if info, err := os.Stat(cwd); err == nil && info.IsDir() {
		if abs, err := filepath.Abs(cwd); err == nil {
			return abs
		}
		return cwd
	}
	if info, err := os.Stat(fallback); err == nil && info.IsDir() {
		return fallback
	}
	return "."
}

func appendTerminalOutputLocked(session *terminalSessionProcess, chunk []byte) {
	session.output = append(session.output, chunk...)
	if len(session.output) > terminalOutputLimit {
		next := make([]byte, terminalOutputLimit)
		copy(next, session.output[len(session.output)-terminalOutputLimit:])
		session.output = next
	}
	session.updatedAt = time.Now()
}

func terminalSessionSnapshotLocked(session *terminalSessionProcess) TerminalSession {
	return TerminalSession{
		ID:        session.id,
		CWD:       session.cwd,
		PID:       session.pid,
		Output:    string(session.output),
		Exited:    session.exited,
		ExitCode:  session.exitCode,
		Signal:    session.signal,
		CreatedAt: session.createdAt.Format(time.RFC3339Nano),
		UpdatedAt: session.updatedAt.Format(time.RFC3339Nano),
	}
}

func countScriptLogsInState(state AppState) int {
	total := 0
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			for _, item := range collection.Items {
				if item.Response != nil {
					total += len(item.Response.ScriptLogs)
				}
			}
		}
	}
	return total
}

func (a *App) CreateWorkspace(name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if a.workspaceRuntime != nil {
		return a.createScopedWorkspaceTargetLocked(name)
	}
	if strings.TrimSpace(name) == "" {
		return AppState{}, errors.New("workspace name is required")
	}
	now := time.Now()
	id := newID("workspace")
	wsPath := filepath.Join(a.dataDir, sanitizeFilename(name))
	ws := Workspace{
		ID:        id,
		Name:      strings.TrimSpace(name),
		Path:      wsPath,
		Docs:      "# Workspace notes\n",
		CreatedAt: now,
		UpdatedAt: now,
	}
	a.state.Workspaces = append(a.state.Workspaces, ws)
	if _, err := a.ensureWorkspaceScratchCollectionLocked(&a.state.Workspaces[len(a.state.Workspaces)-1]); err != nil {
		return AppState{}, err
	}
	a.state.ActiveWorkspaceID = id
	a.notify("info", "Workspace created: "+ws.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SetActiveWorkspace(workspaceID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return AppState{}, errors.New("workspace id is required")
	}
	if a.workspaceRuntime != nil && workspaceID != a.workspaceRuntime.intent.WorkspaceID {
		return AppState{}, errors.New("scoped workspace windows cannot switch workspaces")
	}
	workspace, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	a.state.ActiveWorkspaceID = workspace.ID
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateCollection(workspaceID, name, format string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if strings.TrimSpace(name) == "" {
		return AppState{}, errors.New("collection name is required")
	}
	if format == "" {
		format = "yml"
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	now := time.Now()
	id := newID("collection")
	collectionRoot := defaultCollectionRoot(a.state.Preferences, ws.Path)
	collection := Collection{
		ID:             id,
		Name:           strings.TrimSpace(name),
		Version:        "1",
		Path:           filepath.Join(collectionRoot, sanitizeFilename(name)),
		Format:         format,
		Variables:      []Variable{{ID: newID("var"), Name: "host", Value: "https://httpbin.org", DataType: "string", Enabled: true}},
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		Docs:           "# Collection notes\n",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, statErr := os.Lstat(collection.Path); statErr == nil {
		return AppState{}, fmt.Errorf("collection path already exists: %s", collection.Path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return AppState{}, fmt.Errorf("check collection path: %w", statErr)
	}
	// Empty collections are durable filesystem objects too. Materialise the
	// root metadata before publishing the collection into workspace state so a
	// clean relaunch never points at a directory that was never created.
	if err := a.writeCollectionFilesLocked(&collection); err != nil {
		return AppState{}, fmt.Errorf("create collection files: %w", err)
	}
	if firstScratch := firstScratchCollectionIndex(ws.Collections); firstScratch == 0 && countRegularCollections(ws.Collections) == 0 {
		ws.Collections = append(ws.Collections, Collection{})
		copy(ws.Collections[1:], ws.Collections[0:])
		ws.Collections[0] = collection
	} else {
		ws.Collections = append(ws.Collections, collection)
	}
	ws.UpdatedAt = now
	a.state.ActiveWorkspaceID = ws.ID
	a.notify("info", "Collection created: "+collection.Name)
	return a.state, a.markDirty(persistScopeState)
}

func defaultCollectionRoot(preferences Preferences, workspacePath string) string {
	preferences = normalizePreferences(preferences)
	root := strings.TrimSpace(preferences.General.DefaultLocation)
	if root == "" {
		root = strings.TrimSpace(preferences.DefaultCollectionPath)
	}
	if root == "" {
		root = workspacePath
	}
	if expanded, err := expandUserPath(root); err == nil && expanded != "" {
		root = expanded
	}
	return filepath.Clean(root)
}

func expandUserPath(pathValue string) (string, error) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", nil
	}
	if pathValue == "~" || strings.HasPrefix(pathValue, "~/") || strings.HasPrefix(pathValue, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		if pathValue == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimLeft(pathValue[1:], `/\`)), nil
	}
	return pathValue, nil
}

func (a *App) OpenCollection(workspaceID, collectionPath string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	collection, err := a.readCollectionFromDiskCachedLocked(collectionPath)
	if err != nil {
		return AppState{}, err
	}
	if err := a.hydrateCollectionEnvironmentSecretsLocked(&collection); err != nil {
		return AppState{}, err
	}
	for i := range ws.Collections {
		if filepath.Clean(ws.Collections[i].Path) == filepath.Clean(collection.Path) {
			preserveCollectionRuntimeState(ws.Collections[i], &collection)
			collection.Remote = ws.Collections[i].Remote
			collection.NotFoundLocally = false
			ws.Collections[i] = collection
			a.seedCollectionWatchFingerprintLocked(collection.Path)
			a.openFirstCollectionItemLocked(collection)
			a.notify("success", "Refreshed "+collection.Name)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	ws.Collections = append(ws.Collections, collection)
	ws.UpdatedAt = time.Now()
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.openFirstCollectionItemLocked(collection)
	a.notify("success", "Opened "+collection.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RefreshCollection(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			if a.state.Workspaces[wi].Collections[ci].ID != collectionID {
				continue
			}
			refreshed, err := a.readCollectionFromDiskCachedLocked(a.state.Workspaces[wi].Collections[ci].Path)
			if err != nil {
				if a.state.Workspaces[wi].Collections[ci].Remote != "" && errors.Is(err, os.ErrNotExist) {
					a.state.Workspaces[wi].Collections[ci].NotFoundLocally = true
					a.state.Workspaces[wi].Collections[ci].Items = nil
					a.state.Workspaces[wi].Collections[ci].Folders = nil
					a.clearCollectionWatchFingerprintLocked(a.state.Workspaces[wi].Collections[ci].Path)
					a.removeOpenTabsForCollectionLocked(collectionID)
					a.notify("warn", a.state.Workspaces[wi].Collections[ci].Name+" is not cloned locally")
					return a.state, a.markDirty(persistScopeState)
				}
				return AppState{}, err
			}
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&refreshed); err != nil {
				return AppState{}, err
			}
			preserveCollectionRuntimeState(a.state.Workspaces[wi].Collections[ci], &refreshed)
			refreshed.Remote = a.state.Workspaces[wi].Collections[ci].Remote
			refreshed.NotFoundLocally = false
			a.state.Workspaces[wi].Collections[ci] = refreshed
			a.seedCollectionWatchFingerprintLocked(refreshed.Path)
			a.openFirstCollectionItemLocked(refreshed)
			a.notify("success", "Refreshed "+refreshed.Name)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) RefreshChangedCollections() (CollectionWatchRefreshResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionWatchRefreshResult{}, err
	}
	if a.collectionWatchFingerprints == nil {
		a.collectionWatchFingerprints = map[string]string{}
	}
	result := CollectionWatchRefreshResult{State: a.state}
	now := time.Now()
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		for ci := range ws.Collections {
			collection := &ws.Collections[ci]
			if !collectionWatchCandidate(*collection) {
				continue
			}
			collectionPath := filepath.Clean(collection.Path)
			fingerprint, err := collectionWatchFingerprint(collectionPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					a.clearCollectionWatchFingerprintLocked(collectionPath)
					if strings.TrimSpace(collection.Remote) != "" {
						collection.NotFoundLocally = true
						collection.Items = nil
						collection.Folders = nil
						a.removeOpenTabsForCollectionLocked(collection.ID)
						result.Missing = append(result.Missing, collection.Name)
						result.Changed = true
					}
					continue
				}
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", collection.Name, err))
				continue
			}
			previous, seen := a.collectionWatchFingerprints[collectionPath]
			if !seen {
				// A process restart begins with an empty in-memory watch map. The
				// first observation establishes a baseline; it is not evidence of
				// an external edit and must not replace live request/tab identity.
				a.collectionWatchFingerprints[collectionPath] = fingerprint
				continue
			}
			if seen && previous == fingerprint {
				continue
			}
			if collectionHasDraftRequests(*collection) {
				a.collectionWatchFingerprints[collectionPath] = fingerprint
				result.SkippedDirty = append(result.SkippedDirty, collection.Name)
				continue
			}
			refreshed, err := a.readCollectionFromDiskCachedLocked(collectionPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", collection.Name, err))
				continue
			}
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&refreshed); err != nil {
				return CollectionWatchRefreshResult{}, err
			}
			preserveCollectionRuntimeState(*collection, &refreshed)
			removedRequestIDs := missingRequestIDs(collection.Items, refreshed.Items)
			refreshed.Remote = collection.Remote
			refreshed.NotFoundLocally = false
			ws.Collections[ci] = refreshed
			ws.UpdatedAt = now
			a.collectionWatchFingerprints[collectionPath] = fingerprint
			a.removeOpenTabsForDeletedRequestIDsLocked(refreshed.ID, removedRequestIDs)
			result.Refreshed = append(result.Refreshed, refreshed.Name)
			result.Changed = true
		}
	}
	if result.Changed {
		if err := a.markDirty(persistScopeState); err != nil {
			return CollectionWatchRefreshResult{}, err
		}
	}
	result.State = a.state
	return result, nil
}

func (a *App) RevealCollectionInFolder(collectionID string) error {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	_, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	if collection.NotFoundLocally {
		a.mu.Unlock()
		return errors.New("collection is not cloned locally")
	}
	collectionPath := collection.Path
	a.mu.Unlock()

	if strings.TrimSpace(collectionPath) == "" {
		return errors.New("collection path is required")
	}
	if _, err := os.Stat(collectionPath); err != nil {
		return fmt.Errorf("reveal collection folder: %w", err)
	}
	opener := a.revealInFolder
	if opener == nil {
		opener = defaultRevealInFolder
	}
	return opener(collectionPath)
}

func (a *App) RevealCollectionFolderInFolder(collectionID, folderPath string) error {
	targetPath, err := a.resolveCollectionFolderPath(collectionID, folderPath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(targetPath); err != nil {
		return fmt.Errorf("reveal collection folder: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", targetPath)
	}
	opener := a.revealInFolder
	if opener == nil {
		opener = defaultRevealInFolder
	}
	return opener(targetPath)
}

func (a *App) RevealRequestInFolder(collectionID, itemID string) error {
	targetPath, err := a.resolveRequestFilePath(collectionID, itemID)
	if err != nil {
		return err
	}
	if info, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("the file does not exist")
		}
		return fmt.Errorf("reveal request: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("%s is not a request file", targetPath)
	}
	opener := a.revealInFolder
	if opener == nil {
		opener = defaultRevealInFolder
	}
	return opener(targetPath)
}

func (a *App) ResolveCollectionFolderPath(collectionID, folderPath string) (string, error) {
	return a.resolveCollectionFolderPath(collectionID, folderPath)
}

func (a *App) resolveCollectionFolderPath(collectionID, folderPath string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	_, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return "", err
	}
	if collection.NotFoundLocally {
		return "", errors.New("collection is not cloned locally")
	}
	return collectionFolderFilesystemPath(collection, folderPath)
}

func (a *App) resolveRequestFilePath(collectionID, itemID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	_, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return "", err
	}
	if collection.NotFoundLocally {
		return "", errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection path is empty")
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return "", err
	}
	return collectionRequestFilesystemPath(collection, *item)
}

func (a *App) RemoveCollection(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		for ci := range ws.Collections {
			if ws.Collections[ci].ID != collectionID {
				continue
			}
			collection := ws.Collections[ci]
			if collection.Scratch {
				return AppState{}, errors.New("scratch collection cannot be removed")
			}
			if strings.TrimSpace(collection.Remote) != "" && strings.TrimSpace(collection.Path) != "" {
				if err := updateManagedGitIgnore(ws.Path, collection.Path, false); err != nil {
					return AppState{}, err
				}
			}
			a.clearCollectionWatchFingerprintLocked(collection.Path)
			ws.Collections = append(ws.Collections[:ci], ws.Collections[ci+1:]...)
			ws.UpdatedAt = time.Now()
			a.removeOpenTabsForCollectionLocked(collectionID)
			a.notify("success", "Removed collection: "+collection.Name)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) RenameCollection(collectionID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if name == "" {
		return AppState{}, errors.New("collection name is required")
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	previousName := collection.Name
	previousUpdatedAt := collection.UpdatedAt
	previousWorkspaceUpdatedAt := ws.UpdatedAt
	now := time.Now()
	collection.Name = name
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionNameMetadataLocked(collection); err != nil {
		collection.Name = previousName
		collection.UpdatedAt = previousUpdatedAt
		ws.UpdatedAt = previousWorkspaceUpdatedAt
		return AppState{}, err
	}
	a.notify("success", "Renamed collection: "+collection.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneCollection(collectionID, collectionName, collectionFolderName, collectionLocation string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if collectionName == "" {
		return AppState{}, errors.New("collection name is required")
	}
	if len([]rune(collectionName)) > 255 {
		return AppState{}, errors.New("collection name must be 255 characters or less")
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	sourcePath := filepath.Clean(collection.Path)
	if info, statErr := os.Stat(sourcePath); errors.Is(statErr, os.ErrNotExist) {
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
	} else if statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", sourcePath)
	}
	folderName := bru.SanitizeName(collectionFolderName)
	if !bru.ValidateName(folderName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collectionLocation, folderName))
	}
	location := strings.TrimSpace(collectionLocation)
	if location == "" {
		return AppState{}, errors.New("location is required")
	}
	if expanded, err := expandUserPath(location); err == nil && expanded != "" {
		location = expanded
	}
	targetPath := filepath.Clean(filepath.Join(location, folderName))
	if _, err := os.Stat(targetPath); err == nil {
		return AppState{}, fmt.Errorf("collection: %s already exists", targetPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return AppState{}, err
	}
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return AppState{}, err
	}
	cloned := *collection
	cloned.ID = deterministicID("collection", targetPath)
	cloned.Name = collectionName
	cloned.Path = targetPath
	cloned.Remote = ""
	cloned.NotFoundLocally = false
	cloned.Scratch = false
	now := time.Now()
	cloned.CreatedAt = now
	cloned.UpdatedAt = now
	if err := writeClonedCollectionRootMetadata(collection, &cloned); err != nil {
		return AppState{}, err
	}
	if err := copyCollectionFormatFiles(sourcePath, targetPath, cloned.Format); err != nil {
		return AppState{}, err
	}
	cloned, err = readCollectionFromDisk(targetPath)
	if err != nil {
		return AppState{}, err
	}
	if err := a.hydrateCollectionEnvironmentSecretsLocked(&cloned); err != nil {
		return AppState{}, err
	}
	replaced := false
	for i := range ws.Collections {
		if filepath.Clean(ws.Collections[i].Path) == targetPath {
			ws.Collections[i] = cloned
			replaced = true
			break
		}
	}
	if !replaced {
		ws.Collections = append(ws.Collections, cloned)
	}
	ws.UpdatedAt = now
	a.state.ActiveWorkspaceID = ws.ID
	a.openFirstCollectionItemLocked(cloned)
	a.notify("success", "Collection created!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ConnectCollectionGitRemote(collectionID, remoteURL string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	remote, err := normalizeGitRemoteURL(remoteURL)
	if err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
	} else if statErr != nil {
		return AppState{}, statErr
	}
	collection.Remote = remote
	collection.NotFoundLocally = false
	collection.UpdatedAt = time.Now()
	ws.UpdatedAt = collection.UpdatedAt
	if err := updateManagedGitIgnore(ws.Path, collection.Path, true); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Connected "+collection.Name+" to Git")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DisconnectCollectionGitRemote(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		for ci := range ws.Collections {
			if ws.Collections[ci].ID != collectionID {
				continue
			}
			collection := &ws.Collections[ci]
			if collection.Remote == "" {
				return AppState{}, errors.New("collection is not connected to Git")
			}
			collectionPath := collection.Path
			collectionName := collection.Name
			if err := updateManagedGitIgnore(ws.Path, collectionPath, false); err != nil {
				return AppState{}, err
			}
			if collection.NotFoundLocally {
				ws.Collections = append(ws.Collections[:ci], ws.Collections[ci+1:]...)
				a.removeOpenTabsForCollectionLocked(collectionID)
			} else {
				collection.Remote = ""
				collection.NotFoundLocally = false
				collection.UpdatedAt = time.Now()
			}
			ws.UpdatedAt = time.Now()
			a.notify("info", "Removed Git remote from "+collectionName)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) GitVersion() (string, error) {
	return gitVersion()
}

func (a *App) ScanGitCollections(rootPath string) ([]GitCollectionCandidate, error) {
	return scanBrunoCollections(rootPath)
}

func (a *App) CloneGitRepository(remoteURL, cloneRoot, repoName string) (GitCloneResult, error) {
	remote, err := normalizeGitRemoteURL(remoteURL)
	if err != nil {
		return GitCloneResult{}, err
	}
	a.emitGitCloneProgress("checking", "Checking Git", "")
	version, err := gitVersion()
	if err != nil {
		a.emitGitCloneProgress("error", err.Error(), "")
		return GitCloneResult{}, err
	}
	cloneRoot = strings.TrimSpace(cloneRoot)
	if cloneRoot == "" {
		cloneRoot = filepath.Join(a.dataDir, "Git Clones")
	}
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		repoName = deriveGitRepoName(remote)
	}
	if repoName == "" {
		return GitCloneResult{}, errors.New("repository name could not be derived")
	}
	target := filepath.Join(cloneRoot, sanitizeFilename(repoName))
	cleanTarget := filepath.Clean(target)
	a.emitGitCloneProgress("preparing", "Preparing "+cleanTarget, cleanTarget)
	if info, err := os.Stat(target); err == nil {
		if !info.IsDir() {
			a.emitGitCloneProgress("error", fmt.Sprintf("%s already exists and is not a directory", target), cleanTarget)
			return GitCloneResult{}, fmt.Errorf("%s already exists and is not a directory", target)
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			a.emitGitCloneProgress("error", err.Error(), cleanTarget)
			return GitCloneResult{}, err
		}
		if len(entries) > 0 {
			a.emitGitCloneProgress("error", fmt.Sprintf("%s already exists and is not empty", target), cleanTarget)
			return GitCloneResult{}, fmt.Errorf("%s already exists and is not empty", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		a.emitGitCloneProgress("error", err.Error(), cleanTarget)
		return GitCloneResult{}, err
	}
	if err := os.MkdirAll(cloneRoot, 0o755); err != nil {
		a.emitGitCloneProgress("error", err.Error(), cleanTarget)
		return GitCloneResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--", remote, target)
	a.emitGitCloneProgress("cloning", "Cloning "+remote, cleanTarget)
	output, err := a.runGitCloneCommand(ctx, cmd, cleanTarget)
	if err != nil {
		_ = os.RemoveAll(target)
		if ctx.Err() == context.DeadlineExceeded {
			a.emitGitCloneProgress("error", "git clone timed out", cleanTarget)
			return GitCloneResult{}, errors.New("git clone timed out")
		}
		message := strings.TrimSpace(output)
		if message == "" {
			message = err.Error()
		}
		a.emitGitCloneProgress("error", "git clone failed: "+message, cleanTarget)
		return GitCloneResult{}, fmt.Errorf("git clone failed: %s", message)
	}
	a.emitGitCloneProgress("scanning", "Scanning cloned repository", cleanTarget)
	candidates, err := scanBrunoCollections(target)
	if err != nil {
		a.emitGitCloneProgress("error", err.Error(), cleanTarget)
		return GitCloneResult{}, err
	}
	a.emitGitCloneProgress("done", fmt.Sprintf("Found %d collection%s", len(candidates), cookiejar.PluralSuffix(len(candidates))), cleanTarget)
	return GitCloneResult{
		Version:    version,
		TargetPath: cleanTarget,
		Output:     strings.TrimSpace(output),
		Candidates: candidates,
	}, nil
}

func (a *App) runGitCloneCommand(ctx context.Context, cmd *exec.Cmd, targetPath string) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	var outputMu sync.Mutex
	var output strings.Builder
	collect := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Split(scanGitProgressToken)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			outputMu.Lock()
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(line)
			outputMu.Unlock()
			a.emitGitCloneProgress("output", line, targetPath)
		}
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		collect(stdout)
	}()
	go func() {
		defer wg.Done()
		collect(stderr)
	}()
	err = cmd.Wait()
	wg.Wait()
	outputMu.Lock()
	result := output.String()
	outputMu.Unlock()
	return result, err
}

func (a *App) OpenGitCollections(workspaceID string, collectionPaths []string, remoteURL string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	remote, err := normalizeGitRemoteURL(remoteURL)
	if err != nil {
		return AppState{}, err
	}
	if len(collectionPaths) == 0 {
		return AppState{}, errors.New("select at least one collection")
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	opened := 0
	var firstOpened Collection
	haveFirstOpened := false
	for _, collectionPath := range collectionPaths {
		collectionPath = strings.TrimSpace(collectionPath)
		if collectionPath == "" {
			continue
		}
		collection, err := a.readCollectionFromDiskCachedLocked(collectionPath)
		if err != nil {
			return AppState{}, err
		}
		if err := a.hydrateCollectionEnvironmentSecretsLocked(&collection); err != nil {
			return AppState{}, err
		}
		collection.Remote = remote
		collection.NotFoundLocally = false
		replaced := false
		for i := range ws.Collections {
			if filepath.Clean(ws.Collections[i].Path) == filepath.Clean(collection.Path) {
				ws.Collections[i] = collection
				if !haveFirstOpened {
					firstOpened = collection
					haveFirstOpened = true
				}
				replaced = true
				break
			}
		}
		if !replaced {
			ws.Collections = append(ws.Collections, collection)
			if !haveFirstOpened {
				firstOpened = collection
				haveFirstOpened = true
			}
		}
		if err := updateManagedGitIgnore(ws.Path, collection.Path, true); err != nil {
			return AppState{}, err
		}
		opened++
	}
	if opened == 0 {
		return AppState{}, errors.New("select at least one collection")
	}
	ws.UpdatedAt = time.Now()
	if haveFirstOpened {
		a.openFirstCollectionItemLocked(firstOpened)
	}
	a.notify("success", fmt.Sprintf("Opened %d Git collection%s", opened, cookiejar.PluralSuffix(opened)))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateRequest(collectionID, requestType, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	presets := normalizeCollectionPresets(collection.Presets)
	requestType = normalizePresetRequestType(requestType)
	if requestType == "" {
		requestType = presets.RequestType
	}
	if requestType == "" {
		requestType = "http"
	}
	if name == "" {
		name = strings.ToUpper(requestType[:1]) + requestType[1:] + " request"
	}
	item := types.NewRequestItem(name, requestType, len(collection.Items)+1)
	if strings.TrimSpace(presets.RequestURL) != "" {
		item.URL = presets.RequestURL
	}
	if collection.Scratch {
		item.Transient = true
		item.FilePath = requestFilePath(*collection, item, requestFileExtensionForCollection(*collection))
	}
	collection.Items = append(collection.Items, item)
	if !collection.Scratch && strings.TrimSpace(collection.Path) != "" {
		// File-backed request identities are derived from their eventual path on
		// disk. Establish that identity before opening the tab so an internal
		// save, watcher refresh, or restart cannot strand the tab on a temporary
		// random ID.
		ensureRequestFilePaths(collection, requestFileExtensionForCollection(*collection))
		created := &collection.Items[len(collection.Items)-1]
		created.ID = deterministicID("request", filepath.Clean(created.FilePath))
		assignExampleIDs(created)
		created.Draft = true
		created.Transient = true
		item = *created
	}
	collection.UpdatedAt = time.Now()
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.openTabLocked(collection.ID, item.ID, "request")
	a.notify("info", "Request created: "+item.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateFolder(collectionID, parentFolderPath, folderName, directoryName string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		return AppState{}, errors.New("folder name is required")
	}
	if strings.TrimSpace(directoryName) == "" {
		directoryName = name
	}
	directoryName = bru.SanitizeName(directoryName)
	if !bru.ValidateName(directoryName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, directoryName))
	}
	parentPath, parentDisplayPath, err := collectionFolderParentPaths(collection, parentFolderPath)
	if err != nil {
		return AppState{}, err
	}
	if parentPath == "" && strings.Contains(strings.ToLower(strings.TrimSpace(directoryName)), "environments") {
		return AppState{}, errors.New("the folder name \"environments\" at the root of the collection is reserved in bruno")
	}
	if collectionHasChildFolder(collection, parentPath, directoryName) {
		return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	parentDir := filepath.Join(collection.Path, filepath.FromSlash(parentPath))
	if info, statErr := os.Stat(parentDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", parentDir)
	}
	folderPath := joinCollectionFolderPath(parentPath, directoryName)
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return AppState{}, errors.New("the directory already exists")
		}
		return AppState{}, err
	}
	folder := FolderConfig{
		Path:        folderPath,
		DisplayPath: joinCollectionFolderPath(parentDisplayPath, name),
		Name:        name,
		Seq:         nextCollectionFolderSeq(*collection, parentPath),
		Auth:        AuthConfig{Mode: "inherit"},
	}
	if err := a.writeFolderConfigLocked(collection, folder); err != nil {
		return AppState{}, err
	}
	collection.Folders = append(collection.Folders, folder)
	sortFoldersLikeBruno(collection.Folders)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	a.notify("success", "New folder created!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RenameFolder(collectionID, folderPath, folderName, directoryName string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		return AppState{}, errors.New("folder name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("folder name must be 255 characters or less")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	folder := collection.Folders[folderIndex]
	oldPath := normalizeFolderPathKey(folder.Path)
	oldDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	if oldPath == "" {
		return AppState{}, errors.New("folder path is required")
	}
	if strings.TrimSpace(directoryName) == "" {
		directoryName = name
	}
	directoryName = bru.SanitizeName(directoryName)
	if !bru.ValidateName(directoryName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, directoryName))
	}
	if strings.EqualFold(directoryName, "collection") || strings.EqualFold(directoryName, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	parentPath := normalizeFolderPathKey(parentFolderDisplayPath(oldPath))
	parentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(oldDisplayPath))
	newPath := joinCollectionFolderPath(parentPath, directoryName)
	newDisplayPath := joinCollectionFolderPath(parentDisplayPath, name)
	if newPath != oldPath && collectionHasChildFolder(collection, parentPath, directoryName) {
		return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	oldDir := filepath.Join(collection.Path, filepath.FromSlash(oldPath))
	newDir := filepath.Join(collection.Path, filepath.FromSlash(newPath))
	if !pathInside(collection.Path, oldDir) || !pathInside(collection.Path, newDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(oldDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", oldDir)
	}
	if newPath != oldPath {
		if _, err := os.Stat(newDir); err == nil {
			return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return AppState{}, err
		}
	}
	renamedFolder := folder
	renamedFolder.Name = name
	renamedFolder.DisplayPath = newDisplayPath
	if err := a.writeFolderConfigLocked(collection, FolderConfig{
		Path:         oldPath,
		DisplayPath:  oldDisplayPath,
		Name:         name,
		Seq:          renamedFolder.Seq,
		Headers:      renamedFolder.Headers,
		Variables:    renamedFolder.Variables,
		ResVariables: renamedFolder.ResVariables,
		Auth:         renamedFolder.Auth,
		PreScript:    renamedFolder.PreScript,
		PostScript:   renamedFolder.PostScript,
		Tests:        renamedFolder.Tests,
		Docs:         renamedFolder.Docs,
	}); err != nil {
		return AppState{}, err
	}
	if newPath != oldPath {
		if err := os.Rename(oldDir, newDir); err != nil {
			return AppState{}, err
		}
	}
	updateCollectionFolderRenameState(collection, oldPath, newPath, oldDisplayPath, newDisplayPath, oldDir, newDir)
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	a.notify("success", "Item renamed successfully")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DeleteFolder(collectionID, folderPath string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	folder := collection.Folders[folderIndex]
	oldPath := normalizeFolderPathKey(folder.Path)
	oldDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	if oldPath == "" {
		return AppState{}, errors.New("folder path is required")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(oldPath))
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the directory does not exist")
		}
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", targetDir)
	}

	removedRequestIDs := map[string]bool{}
	remainingItems := collection.Items[:0]
	for _, item := range collection.Items {
		itemFolderPath := normalizeFolderPathKey(item.FolderPath)
		remove := folderPathHasPrefix(itemFolderPath, oldDisplayPath) || folderPathHasPrefix(itemFolderPath, oldPath)
		if !remove && strings.TrimSpace(item.FilePath) != "" {
			remove = pathInside(targetDir, item.FilePath)
		}
		if remove {
			removedRequestIDs[item.ID] = true
			continue
		}
		remainingItems = append(remainingItems, item)
	}
	collection.Items = remainingItems

	remainingFolders := collection.Folders[:0]
	for _, candidate := range collection.Folders {
		candidatePath := normalizeFolderPathKey(candidate.Path)
		candidateDisplayPath := normalizeFolderPathKey(firstNonEmpty(candidate.DisplayPath, candidate.Name, candidate.Path))
		if folderPathHasPrefix(candidatePath, oldPath) || folderPathHasPrefix(candidateDisplayPath, oldDisplayPath) {
			continue
		}
		remainingFolders = append(remainingFolders, candidate)
	}
	collection.Folders = remainingFolders
	sortFoldersLikeBruno(collection.Folders)

	if err := os.RemoveAll(targetDir); err != nil {
		return AppState{}, err
	}
	parentPath := normalizeFolderPathKey(parentFolderDisplayPath(oldPath))
	parentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(oldDisplayPath))
	if err := a.resequenceCollectionSiblingsLocked(collection, parentPath, parentDisplayPath); err != nil {
		return AppState{}, err
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.removeOpenTabsForRequestIDsLocked(collection.ID, removedRequestIDs)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	a.notify("info", "Deleted folder "+firstNonEmpty(folder.Name, pathBaseSlash(oldDisplayPath), pathBaseSlash(oldPath)))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DeleteRequest(collectionID, itemID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	if collection.Remote != "" {
		if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
			collection.NotFoundLocally = true
			return AppState{}, errors.New("collection is not cloned locally")
		} else if statErr != nil {
			return AppState{}, statErr
		}
	}
	index, err := findItemIndex(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	item := collection.Items[index]
	oldFile := requestFilePath(*collection, item, requestFileExtensionForCollection(*collection))
	if pathInside(collection.Path, item.FilePath) {
		oldFile = filepath.Clean(item.FilePath)
	}
	if !pathInside(collection.Path, oldFile) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", oldFile)
	}
	if info, statErr := os.Stat(oldFile); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the file does not exist")
		}
		return AppState{}, statErr
	} else if info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a request file", oldFile)
	}

	parentPath := normalizeFolderPathKey(item.FolderPath)
	parentDisplayPath := parentPath
	if parentDisplayPath != "" {
		if folder, folderErr := findFolderConfig(collection, parentDisplayPath); folderErr == nil {
			parentPath = normalizeFolderPathKey(folder.Path)
			parentDisplayPath = normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
		}
	}

	if err := os.Remove(oldFile); err != nil {
		return AppState{}, err
	}
	collection.Items = append(collection.Items[:index], collection.Items[index+1:]...)
	if err := a.resequenceCollectionSiblingsLocked(collection, parentPath, parentDisplayPath); err != nil {
		return AppState{}, err
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.removeOpenTabsForRequestIDsLocked(collection.ID, map[string]bool{item.ID: true})
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneFolder(collectionID, folderPath, folderName, directoryName string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		return AppState{}, errors.New("folder name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("folder name must be 255 characters or less")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	sourceFolder := collection.Folders[folderIndex]
	sourcePath := normalizeFolderPathKey(sourceFolder.Path)
	sourceDisplayPath := normalizeFolderPathKey(firstNonEmpty(sourceFolder.DisplayPath, sourceFolder.Name, sourceFolder.Path))
	if sourcePath == "" {
		return AppState{}, errors.New("folder path is required")
	}
	if strings.TrimSpace(directoryName) == "" {
		directoryName = name
	}
	directoryName = bru.SanitizeName(directoryName)
	if !bru.ValidateName(directoryName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, directoryName))
	}
	if strings.EqualFold(directoryName, "collection") || strings.EqualFold(directoryName, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	parentPath := normalizeFolderPathKey(parentFolderDisplayPath(sourcePath))
	parentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(sourceDisplayPath))
	targetPath := joinCollectionFolderPath(parentPath, directoryName)
	targetDisplayPath := joinCollectionFolderPath(parentDisplayPath, name)
	if collectionHasChildFolder(collection, parentPath, directoryName) {
		return AppState{}, errors.New("duplicate folder names under same parent folder are not allowed")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	sourceDir := filepath.Join(collection.Path, filepath.FromSlash(sourcePath))
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(targetPath))
	if !pathInside(collection.Path, sourceDir) || !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(sourceDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the directory does not exist")
		}
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", sourceDir)
	}
	if _, err := os.Stat(targetDir); err == nil {
		return AppState{}, fmt.Errorf("folder: %s already exists", targetDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return AppState{}, err
	}

	now := time.Now()
	clonedFolders := make([]FolderConfig, 0)
	rootSeq := nextCollectionFolderSeq(*collection, parentPath)
	for _, folder := range collection.Folders {
		folderPhysicalPath := normalizeFolderPathKey(folder.Path)
		folderDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
		if !folderPathHasPrefix(folderPhysicalPath, sourcePath) && !folderPathHasPrefix(folderDisplayPath, sourceDisplayPath) {
			continue
		}
		cloned := cloneFolderConfigForFolderClone(folder)
		cloned.Path = replaceFolderPathPrefix(folderPhysicalPath, sourcePath, targetPath)
		cloned.DisplayPath = replaceFolderPathPrefix(folderDisplayPath, sourceDisplayPath, targetDisplayPath)
		if cloned.Path == targetPath {
			cloned.Name = name
			cloned.Seq = rootSeq
		}
		clonedFolders = append(clonedFolders, cloned)
	}
	clonedItems := make([]RequestItem, 0)
	for _, item := range collection.Items {
		if !cloneFolderCopiesRequestType(item.Type) {
			continue
		}
		itemFolderPath := normalizeFolderPathKey(item.FolderPath)
		include := folderPathHasPrefix(itemFolderPath, sourceDisplayPath)
		if !include && strings.TrimSpace(item.FilePath) != "" {
			include = pathInside(sourceDir, item.FilePath)
		}
		if !include {
			continue
		}
		cloned := cloneRequestItemForFolderClone(item)
		cloned.FolderPath = replaceFolderPathPrefix(itemFolderPath, sourceDisplayPath, targetDisplayPath)
		if pathInside(sourceDir, item.FilePath) {
			rel, err := filepath.Rel(sourceDir, item.FilePath)
			if err == nil {
				cloned.FilePath = filepath.Clean(filepath.Join(targetDir, rel))
			}
		} else {
			cloned.FilePath = ""
		}
		if cloned.FilePath == "" {
			cloned.FilePath = requestFilePath(*collection, cloned, requestFileExtensionForCollection(*collection))
		}
		cloned.ID = deterministicID("request", filepath.Clean(cloned.FilePath))
		cloned.Draft = false
		cloned.CreatedAt = now
		cloned.UpdatedAt = now
		assignExampleIDs(&cloned)
		clonedItems = append(clonedItems, cloned)
	}
	if len(clonedFolders) == 0 {
		return AppState{}, fmt.Errorf("folder %s not found", folderPath)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return AppState{}, err
	}
	collection.Folders = append(collection.Folders, clonedFolders...)
	collection.Items = append(collection.Items, clonedItems...)
	sortFoldersLikeBruno(collection.Folders)
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	for _, folder := range clonedFolders {
		if err := a.writeFolderConfigLocked(collection, folder); err != nil {
			return AppState{}, err
		}
	}
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Request cloned!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneRequest(collectionID, itemID, requestName, filename string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	if collection.Remote != "" {
		if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
			collection.NotFoundLocally = true
			return AppState{}, errors.New("collection is not cloned locally")
		} else if statErr != nil {
			return AppState{}, statErr
		}
	}
	source, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	name := strings.TrimSpace(requestName)
	if name == "" {
		return AppState{}, errors.New("request name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("request name must be 255 characters or less")
	}
	filenameBase := requestCloneFilenameBase(filename, name)
	if !bru.ValidateName(filenameBase) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, filenameBase))
	}
	if strings.EqualFold(filenameBase, "collection") || strings.EqualFold(filenameBase, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	targetDir, folderPath, err := cloneRequestTargetDirectory(collection, *source)
	if err != nil {
		return AppState{}, err
	}
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetDir)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", targetDir)
	}
	targetFilename := filenameBase + requestFileExtensionForCollection(*collection)
	targetFile := filepath.Clean(filepath.Join(targetDir, targetFilename))
	if !pathInside(collection.Path, targetFile) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetFilename)
	}
	if collectionHasRequestFileSibling(collection, folderPath, targetFilename) {
		return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
	}
	if _, statErr := os.Stat(targetFile); statErr == nil {
		return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return AppState{}, statErr
	}

	now := time.Now()
	cloned := cloneRequestItemForFolderClone(*source)
	cloned.Name = name
	cloned.FolderPath = folderPath
	cloned.FilePath = targetFile
	cloned.ID = deterministicID("request", targetFile)
	cloned.Seq = nextCollectionRequestSeq(*collection, folderPath)
	cloned.Draft = false
	cloned.Transient = collection.Scratch
	cloned.CreatedAt = now
	cloned.UpdatedAt = now
	assignExampleIDs(&cloned)

	collection.Items = append(collection.Items, cloned)
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.openTabLocked(collection.ID, cloned.ID, "request")
	a.notify("success", "Request cloned!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RenameRequest(collectionID, itemID, requestName, filename string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	if collection.Remote != "" {
		if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
			collection.NotFoundLocally = true
			return AppState{}, errors.New("collection is not cloned locally")
		} else if statErr != nil {
			return AppState{}, statErr
		}
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	name := strings.TrimSpace(requestName)
	if name == "" {
		return AppState{}, errors.New("request name is required")
	}
	if len([]rune(name)) > 255 {
		return AppState{}, errors.New("request name must be 255 characters or less")
	}
	filenameBase := requestCloneFilenameBase(filename, name)
	if !bru.ValidateName(filenameBase) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collection.Path, filenameBase))
	}
	if strings.EqualFold(filenameBase, "collection") || strings.EqualFold(filenameBase, "folder") {
		return AppState{}, errors.New(`the file names "collection" and "folder" are reserved in bruno`)
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return AppState{}, err
	}
	targetDir, folderPath, err := cloneRequestTargetDirectory(collection, *item)
	if err != nil {
		return AppState{}, err
	}
	if !pathInside(collection.Path, targetDir) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetDir)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", targetDir)
	}
	oldFile := requestFilePath(*collection, *item, requestFileExtensionForCollection(*collection))
	if pathInside(collection.Path, item.FilePath) {
		oldFile = filepath.Clean(item.FilePath)
	}
	oldFileExists := true
	if info, statErr := os.Stat(oldFile); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) && item.Draft && item.Transient {
			oldFileExists = false
		} else if errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, errors.New("the file does not exist")
		} else {
			return AppState{}, statErr
		}
	} else if info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a request file", oldFile)
	}
	targetFilename := filenameBase + requestFileExtensionForCollection(*collection)
	targetFile := filepath.Clean(filepath.Join(targetDir, targetFilename))
	if !pathInside(collection.Path, targetFile) {
		return AppState{}, fmt.Errorf("request path %s escapes collection", targetFilename)
	}
	renamingFile := !sameFilePath(oldFile, targetFile)
	if renamingFile {
		if collectionHasRequestFileSiblingExcept(collection, folderPath, targetFilename, item.ID) {
			return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
		}
		if _, statErr := os.Stat(targetFile); statErr == nil {
			return AppState{}, errors.New("duplicate request names are not allowed under the same folder")
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return AppState{}, statErr
		}
		if oldFileExists {
			if err := os.Rename(oldFile, targetFile); err != nil {
				return AppState{}, err
			}
		}
		item.FilePath = targetFile
	}
	now := time.Now()
	item.Name = name
	item.FolderPath = folderPath
	item.Draft = false
	item.Transient = collection.Scratch
	item.UpdatedAt = now
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.notify("success", "Item renamed successfully")
	return a.state, a.markDirty(persistScopeState)
}

func cloneFolderCopiesRequestType(requestType string) bool {
	switch strings.ToLower(strings.TrimSpace(requestType)) {
	case "", "http", "graphql", "grpc":
		return true
	default:
		return false
	}
}

func requestCloneFilenameBase(filename, fallbackName string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = strings.TrimSpace(fallbackName)
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".bru", ".yml", ".yaml":
		filename = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	return bru.SanitizeName(filename)
}

func cloneRequestTargetDirectory(collection *Collection, item RequestItem) (string, string, error) {
	folderPath := normalizeFolderPathKey(item.FolderPath)
	if pathInside(collection.Path, item.FilePath) {
		return filepath.Dir(filepath.Clean(item.FilePath)), folderPath, nil
	}
	if folderPath == "" {
		return filepath.Clean(collection.Path), "", nil
	}
	folder, err := findFolderConfig(collection, folderPath)
	if err == nil {
		return filepath.Join(collection.Path, filepath.FromSlash(folder.Path)), normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path)), nil
	}
	return filepath.Join(collection.Path, filepath.FromSlash(folderPath)), folderPath, nil
}

func collectionHasRequestFileSibling(collection *Collection, folderPath, filename string) bool {
	return collectionHasRequestFileSiblingExcept(collection, folderPath, filename, "")
}

func collectionHasRequestFileSiblingExcept(collection *Collection, folderPath, filename, exceptItemID string) bool {
	folderPath = normalizeFolderPathKey(folderPath)
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return false
	}
	for _, item := range collection.Items {
		if exceptItemID != "" && item.ID == exceptItemID {
			continue
		}
		if normalizeFolderPathKey(item.FolderPath) != folderPath {
			continue
		}
		if strings.EqualFold(requestItemFilename(*collection, item), filename) {
			return true
		}
	}
	return false
}

func sameFilePath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func requestItemFilename(collection Collection, item RequestItem) string {
	if pathInside(collection.Path, item.FilePath) {
		return filepath.Base(item.FilePath)
	}
	return sanitizeFilename(item.Name) + requestFileExtensionForCollection(collection)
}

func nextCollectionRequestSeq(collection Collection, folderPath string) int {
	folderPath = normalizeFolderPathKey(folderPath)
	count := 0
	for _, item := range collection.Items {
		if normalizeFolderPathKey(item.FolderPath) == folderPath {
			count++
		}
	}
	return count + 1
}

func (a *App) UpdateRequest(collectionID, itemID string, patch RequestPatch) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	item, err := a.updateRequestLocked(collectionID, itemID, patch)
	if item == nil {
		return AppState{}, err
	}
	return a.state, err
}

// updateRequestLocked is the body shared by UpdateRequest and its narrow
// variant (US-014). Extracted rather than duplicated so the two cannot drift:
// a patch applied one way by the wide binding and another by the narrow one is
// a bug that only shows up as the frontend and the backend disagreeing about
// what the user typed.
//
// A nil item means the mutation did not happen and err says why. A non-nil item
// with a non-nil err means the mutation DID happen and err is markDirty's
// parked background-write failure from an earlier persist.
func (a *App) updateRequestLocked(collectionID, itemID string, patch RequestPatch) (*RequestItem, error) {
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	item, err := a.findItemLocked(collectionID, itemID)
	if err != nil {
		return nil, err
	}
	applyPatch(item, patch)
	item.Draft = true
	item.UpdatedAt = time.Now()
	return item, a.markDirty(persistScopeState)
}

func (a *App) ListGRPCMethods(collectionID, itemID, environmentID string) ([]GRPCMethodInfo, error) {
	requestCopy, collectionCopy, vars, err := a.effectiveRequestContextForExecution(collectionID, itemID, environmentID)
	if err != nil {
		return nil, err
	}
	if requestCopy.Type != "grpc" {
		return nil, errors.New("active request is not gRPC")
	}
	timeout := requestTimeoutMilliseconds(requestCopy.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Millisecond)
	defer cancel()
	if grpcexec.HasProtoInputs(requestCopy, collectionCopy, vars) {
		return grpcexec.ListMethodsFromProto(ctx, requestCopy, collectionCopy, vars)
	}
	dialConfig, err := a.grpcDialConfigForRequest(collectionCopy, requestCopy, interpolate(requestCopy.URL, vars), vars)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	outgoingCtx, err := grpcexec.OutgoingContext(ctx, requestCopy, vars, a.fetchOAuth2Token)
	if err != nil {
		return nil, err
	}
	return grpcexec.ListMethodsFromReflection(outgoingCtx, conn)
}

func (a *App) GenerateGRPCMessage(collectionID, itemID, environmentID, methodPath string) (string, error) {
	requestCopy, collectionCopy, vars, err := a.effectiveRequestContextForExecution(collectionID, itemID, environmentID)
	if err != nil {
		return "", err
	}
	if requestCopy.Type != "grpc" {
		return "", errors.New("active request is not gRPC")
	}
	if strings.TrimSpace(methodPath) != "" {
		requestCopy.Method = methodPath
	}
	timeout := requestTimeoutMilliseconds(requestCopy.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Millisecond)
	defer cancel()
	var binding grpcMethodBinding
	if grpcexec.HasProtoInputs(requestCopy, collectionCopy, vars) {
		binding, err = grpcexec.CompileMethod(ctx, requestCopy, collectionCopy, vars)
	} else {
		dialConfig, targetErr := a.grpcDialConfigForRequest(collectionCopy, requestCopy, interpolate(requestCopy.URL, vars), vars)
		if targetErr != nil {
			return "", targetErr
		}
		conn, connErr := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
		if connErr != nil {
			return "", connErr
		}
		defer func() { _ = conn.Close() }()
		outgoingCtx, ctxErr := grpcexec.OutgoingContext(ctx, requestCopy, vars, a.fetchOAuth2Token)
		if ctxErr != nil {
			return "", ctxErr
		}
		binding, err = grpcexec.ReflectMethod(outgoingCtx, conn, requestCopy, vars)
	}
	if err != nil {
		return "", err
	}
	return grpcexec.TemplateForMessage(binding.Descriptor.Input())
}

func (a *App) GenerateGrpcurlCommand(collectionID, itemID, environmentID string) (string, error) {
	requestCopy, collectionCopy, vars, err := a.effectiveRequestContextForExecution(collectionID, itemID, environmentID)
	if err != nil {
		return "", err
	}
	if requestCopy.Type != "grpc" {
		return "", errors.New("active request is not gRPC")
	}
	return grpcexec.GenerateGrpcurlCommand(collectionCopy, requestCopy, vars)
}

func (a *App) GenerateRequestCode(collectionID, itemID, environmentID, language string) (string, error) {
	requestCopy, _, vars, err := a.effectiveRequestContextForExecution(collectionID, itemID, environmentID)
	if err != nil {
		return "", err
	}
	if !codegen.RequestTypeSupportsCodeGeneration(requestCopy.Type) {
		return "", errors.New("request code generation is only available for HTTP and GraphQL requests")
	}
	if strings.TrimSpace(requestCopy.URL) == "" {
		return "", errors.New("URL is required")
	}
	return codegen.GenerateRequestCode(requestCopy, vars, language)
}

func (a *App) SaveRequest(collectionID, itemID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	item.Draft = false
	if collection.Scratch {
		item.Transient = true
		if strings.TrimSpace(item.FilePath) == "" || !pathInside(collection.Path, item.FilePath) {
			item.FilePath = requestFilePath(*collection, *item, requestFileExtensionForCollection(*collection))
		}
	} else {
		item.Transient = false
	}
	item.UpdatedAt = time.Now()
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.notify("success", "Saved "+item.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SaveAllTabs(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collectionID = strings.TrimSpace(collectionID)
	now := time.Now()
	seenItems := map[string]bool{}
	collections := map[string]*Collection{}
	collectionOrder := []string{}
	saved := 0
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID == "" || tab.ItemID == "" {
			continue
		}
		if collectionID != "" && tab.CollectionID != collectionID {
			continue
		}
		key := tab.CollectionID + "\x00" + tab.ItemID
		if seenItems[key] {
			continue
		}
		collection, err := a.findCollectionLocked(tab.CollectionID)
		if err != nil {
			return AppState{}, err
		}
		item, err := findItem(collection, tab.ItemID)
		if err != nil {
			return AppState{}, err
		}
		item.Draft = false
		if collection.Scratch {
			item.Transient = true
			if strings.TrimSpace(item.FilePath) == "" || !pathInside(collection.Path, item.FilePath) {
				item.FilePath = requestFilePath(*collection, *item, requestFileExtensionForCollection(*collection))
			}
		} else {
			item.Transient = false
		}
		item.UpdatedAt = now
		seenItems[key] = true
		saved++
		if _, ok := collections[collection.ID]; !ok {
			collections[collection.ID] = collection
			collectionOrder = append(collectionOrder, collection.ID)
		}
	}
	for _, collectionID := range collectionOrder {
		collection := collections[collectionID]
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
		if collection.Scratch {
			if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
				return AppState{}, err
			}
		}
	}
	if saved > 0 {
		a.notify("success", fmt.Sprintf("Saved %d tab%s", saved, cookiejar.PluralSuffix(saved)))
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SaveResponseExample(collectionID, itemID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	if item.Response == nil {
		return AppState{}, errors.New("send the request before saving a response example")
	}
	if item.Response.Size > 5*1024*1024 {
		return AppState{}, errors.New("response examples are limited to 5 MB")
	}
	example := responseExampleFromItem(*item, strings.TrimSpace(name))
	item.Examples = append(item.Examples, example)
	item.Draft = false
	if !collection.Scratch {
		item.Transient = false
	}
	item.UpdatedAt = time.Now()
	a.openResponseExampleTabLocked(collectionID, itemID, example)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Saved response example "+example.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateResponseExample(collectionID, itemID, name, description string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AppState{}, errors.New("example name is required")
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example := blankResponseExampleFromItem(*item, name, strings.TrimSpace(description))
	item.Examples = append(item.Examples, example)
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.openResponseExampleTabLocked(collectionID, itemID, example)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Created response example "+example.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RenameResponseExample(collectionID, itemID, exampleID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AppState{}, errors.New("example name is required")
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	example.Name = name
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.syncResponseExampleTabLocked(collectionID, itemID, *example)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Renamed response example "+name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneResponseExample(collectionID, itemID, exampleID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	cloned := cloneResponseExample(*example)
	cloned.ID = newID("example")
	cloned.Name = strings.TrimSpace(firstNonEmpty(example.Name, "Example")) + " (Copy)"
	item.Examples = append(item.Examples, cloned)
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.openResponseExampleTabLocked(collectionID, itemID, cloned)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Cloned response example "+cloned.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DeleteResponseExample(collectionID, itemID, exampleID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, index, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	deletedName := example.Name
	deletedID := example.ID
	item.Examples = append(item.Examples[:index], item.Examples[index+1:]...)
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.closeResponseExampleTabLocked(collectionID, itemID, deletedID)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("info", "Deleted response example "+deletedName)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateResponseExample(collectionID, itemID, exampleID string, updated ResponseExample) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, index, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	normalized, err := normalizeResponseExampleUpdate(*example, updated, *item)
	if err != nil {
		return AppState{}, err
	}
	item.Examples[index] = normalized
	item.Draft = false
	item.UpdatedAt = time.Now()
	a.syncResponseExampleTabLocked(collectionID, itemID, normalized)
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Updated response example "+normalized.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) GenerateResponseExampleCode(collectionID, itemID, exampleID, language string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return "", err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return "", err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return "", err
	}
	return codegen.GenerateResponseExampleCode(*example, language)
}

func (a *App) DeleteCookie(cookieID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	next := a.state.Cookies[:0]
	removed := false
	for _, cookie := range a.state.Cookies {
		if cookie.ID == cookieID {
			removed = true
			continue
		}
		next = append(next, cookie)
	}
	if !removed {
		return AppState{}, fmt.Errorf("cookie %s not found", cookieID)
	}
	a.state.Cookies = next
	a.notify("info", "Cookie deleted")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SaveCookie(input CookieInput) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	normalized, err := cookiejar.NormalizeManual(input)
	if err != nil {
		return AppState{}, err
	}
	if !cookiejar.PrefixValid(normalized) {
		return AppState{}, fmt.Errorf("cookie %s violates prefix requirements", normalized.Name)
	}
	if err := cookiejar.ValidateForStorage(normalized, ""); err != nil {
		return AppState{}, err
	}
	now := time.Now()
	normalized.UpdatedAt = now
	next := a.state.Cookies[:0]
	for _, existing := range a.state.Cookies {
		if existing.ID == input.ID || cookiejar.Key(existing) == cookiejar.Key(normalized) {
			if !existing.CreatedAt.IsZero() && normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = existing.CreatedAt
			}
			continue
		}
		next = append(next, existing)
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	a.state.Cookies = append(next, normalized)
	a.pruneExpiredCookiesLocked()
	a.notify("success", "Cookie saved")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) AddCookieFromHeader(rawHeader, sourceURL string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if strings.TrimSpace(sourceURL) == "" {
		return AppState{}, errors.New("cookie URL is required")
	}
	if strings.TrimSpace(rawHeader) == "" {
		return AppState{}, errors.New("Set-Cookie value is required")
	}
	header := http.Header{}
	for _, line := range strings.Split(rawHeader, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			header.Add("Set-Cookie", line)
		}
	}
	cookies := cookiejar.FromResponse(&http.Response{Header: header}, sourceURL)
	if len(cookies) == 0 {
		return AppState{}, errors.New("no valid Set-Cookie values found")
	}
	a.storeResponseCookiesLocked(cookies)
	a.notify("success", fmt.Sprintf("Imported %d cookie%s", len(cookies), cookiejar.PluralSuffix(len(cookies))))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearDomainCookies(domain string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	domain = cookiejar.NormalizeDomain(domain)
	if domain == "" {
		return AppState{}, errors.New("cookie domain is required")
	}
	next := a.state.Cookies[:0]
	removed := 0
	for _, cookie := range a.state.Cookies {
		if cookiejar.NormalizeDomain(cookie.Domain) == domain {
			removed++
			continue
		}
		next = append(next, cookie)
	}
	if removed == 0 {
		return AppState{}, fmt.Errorf("no cookies found for %s", domain)
	}
	a.state.Cookies = next
	a.notify("info", fmt.Sprintf("Cleared %d cookie%s for %s", removed, cookiejar.PluralSuffix(removed), domain))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearCookies() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	a.state.Cookies = []CookieEntry{}
	a.notify("info", "Cookies cleared")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) MarkNotificationRead(notificationID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for i := range a.state.Notifications {
		if a.state.Notifications[i].ID == notificationID {
			a.state.Notifications[i].Read = true
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("notification %s not found", notificationID)
}

func (a *App) MarkAllNotificationsRead() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for i := range a.state.Notifications {
		a.state.Notifications[i].Read = true
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearNotifications() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	a.state.Notifications = []Notification{}
	return a.state, a.markDirty(persistScopeState)
}

func responseExampleFromItem(item RequestItem, name string) ResponseExample {
	response := item.Response
	if name == "" {
		name = fmt.Sprintf("%s example %d", item.Name, len(item.Examples)+1)
	}
	bodyType := codegen.ResponseBodyType(*response)
	requestURL := firstNonEmpty(response.RequestedURL, item.URL)
	example := ResponseExample{
		ID:          deterministicID("example", firstNonEmpty(item.FilePath, item.ID, item.Name)+"#example#"+strconv.Itoa(len(item.Examples))+"#"+name),
		Name:        name,
		Description: "Saved from live response",
		Type:        firstNonEmpty(item.Type, "http"),
		Request: ResponseExampleRequest{
			Method:         strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet)),
			URL:            requestURL,
			BodyMode:       firstNonEmpty(item.Body.Mode, "none"),
			Body:           requestBodySnapshot(item.Body),
			Headers:        cloneKeyValues(item.Headers),
			Params:         cloneKeyValues(item.Params),
			FormURLEncoded: cloneKeyValues(item.Body.FormURLEncoded),
			MultipartForm:  cloneFormParts(item.Body.Multipart),
			File:           cloneFileBodyEntries(fileBodyEntries(item.Body)),
		},
		Response: ResponseExamplePayload{
			Status:     response.Status,
			StatusText: cleanStatusText(response.Status, response.StatusText),
			Headers:    keyValuesFromHeaders(response.Headers),
			BodyType:   bodyType,
			Body:       response.Body,
			Size:       response.Size,
			DurationMs: response.DurationMs,
		},
	}
	return example
}

func blankResponseExampleFromItem(item RequestItem, name, description string) ResponseExample {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	return ResponseExample{
		ID:          deterministicID("example", firstNonEmpty(item.FilePath, item.ID, item.Name)+"#example#"+strconv.Itoa(len(item.Examples))+"#"+name),
		Name:        name,
		Description: description,
		Type:        firstNonEmpty(item.Type, "http"),
		Request: ResponseExampleRequest{
			Method:         strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet)),
			URL:            item.URL,
			BodyMode:       firstNonEmpty(item.Body.Mode, "none"),
			Body:           requestBodySnapshot(item.Body),
			Headers:        cloneKeyValues(item.Headers),
			Params:         cloneKeyValues(item.Params),
			FormURLEncoded: cloneKeyValues(item.Body.FormURLEncoded),
			MultipartForm:  cloneFormParts(item.Body.Multipart),
			File:           cloneFileBodyEntries(fileBodyEntries(item.Body)),
		},
		Response: ResponseExamplePayload{
			Status:     http.StatusOK,
			StatusText: http.StatusText(http.StatusOK),
			BodyType:   "text",
			Body:       "",
			Headers:    []KeyValue{},
			Size:       0,
		},
	}
}

func normalizeResponseExampleUpdate(existing, updated ResponseExample, item RequestItem) (ResponseExample, error) {
	next := cloneResponseExample(updated)
	next.ID = existing.ID
	next.Name = strings.TrimSpace(next.Name)
	if next.Name == "" {
		next.Name = strings.TrimSpace(existing.Name)
	}
	if next.Name == "" {
		return ResponseExample{}, errors.New("example name is required")
	}
	next.Type = strings.TrimSpace(firstNonEmpty(next.Type, existing.Type, item.Type, "http"))
	if strings.TrimSpace(next.Request.Method) == "" {
		next.Request.Method = firstNonEmpty(existing.Request.Method, item.Method, http.MethodGet)
	}
	next.Request.Method = strings.ToUpper(strings.TrimSpace(next.Request.Method))
	if strings.TrimSpace(next.Request.URL) == "" {
		next.Request.URL = firstNonEmpty(existing.Request.URL, item.URL)
	}
	if strings.TrimSpace(next.Request.BodyMode) == "" {
		next.Request.BodyMode = firstNonEmpty(existing.Request.BodyMode, "none")
	}
	next.Request.Headers = cloneKeyValues(next.Request.Headers)
	next.Request.Params = cloneKeyValues(next.Request.Params)
	next.Request.FormURLEncoded = cloneKeyValues(next.Request.FormURLEncoded)
	next.Request.MultipartForm = cloneFormParts(next.Request.MultipartForm)
	next.Request.File = cloneFileBodyEntries(next.Request.File)
	if next.Response.Status == 0 {
		next.Response.Status = firstNonZero(existing.Response.Status, http.StatusOK)
	}
	next.Response.StatusText = cleanStatusText(next.Response.Status, next.Response.StatusText)
	if next.Response.StatusText == "" {
		next.Response.StatusText = http.StatusText(next.Response.Status)
	}
	next.Response.BodyType = strings.TrimSpace(firstNonEmpty(next.Response.BodyType, existing.Response.BodyType, "text"))
	next.Response.Headers = cloneKeyValues(next.Response.Headers)
	next.Response.Size = len([]byte(next.Response.Body))
	return next, nil
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func ensureResponseExampleIDs(item *RequestItem) {
	for index := range item.Examples {
		if strings.TrimSpace(item.Examples[index].ID) == "" {
			item.Examples[index].ID = deterministicID("example", firstNonEmpty(item.FilePath, item.ID, item.Name)+"#example#"+strconv.Itoa(index))
		}
	}
}

func findResponseExample(item *RequestItem, exampleID string) (*ResponseExample, int, error) {
	target := strings.TrimSpace(exampleID)
	if target == "" {
		return nil, -1, errors.New("example id is required")
	}
	ensureResponseExampleIDs(item)
	for index := range item.Examples {
		if item.Examples[index].ID == target {
			return &item.Examples[index], index, nil
		}
	}
	nameMatches := []int{}
	for index := range item.Examples {
		if item.Examples[index].Name == target {
			nameMatches = append(nameMatches, index)
		}
	}
	if len(nameMatches) == 1 {
		index := nameMatches[0]
		return &item.Examples[index], index, nil
	}
	if len(nameMatches) > 1 {
		return nil, -1, fmt.Errorf("response example name %q is ambiguous", target)
	}
	return nil, -1, fmt.Errorf("response example %s not found", target)
}

// Request/response-example code generation moved to internal/codegen.

func (a *App) storeResponseCookiesLocked(cookies []CookieEntry) {
	if len(cookies) == 0 {
		a.pruneExpiredCookiesLocked()
		return
	}
	now := time.Now()
	for _, cookie := range cookies {
		if cookie.Name == "" || cookie.Domain == "" {
			continue
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		if cookie.ID == "" {
			cookie.ID = cookiejar.ID(cookie)
		}
		if err := cookiejar.ValidateForStorage(cookie, ""); err != nil {
			continue
		}
		cookie.UpdatedAt = now
		if cookie.CreatedAt.IsZero() {
			cookie.CreatedAt = now
		}
		key := cookiejar.Key(cookie)
		next := a.state.Cookies[:0]
		for _, existing := range a.state.Cookies {
			if cookiejar.Key(existing) == key {
				if !existing.CreatedAt.IsZero() {
					cookie.CreatedAt = existing.CreatedAt
				}
				continue
			}
			next = append(next, existing)
		}
		if cookiejar.Expired(cookie, now) {
			a.state.Cookies = next
			continue
		}
		a.state.Cookies = append(next, cookie)
	}
	a.pruneExpiredCookiesLocked()
}

func (a *App) pruneExpiredCookiesLocked() {
	now := time.Now()
	next := a.state.Cookies[:0]
	for _, cookie := range a.state.Cookies {
		if !cookiejar.Expired(cookie, now) {
			next = append(next, cookie)
		}
	}
	a.state.Cookies = next
}

// Cookie storage rules moved to internal/cookiejar.

func (a *App) SetActiveTab(tabID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.setActiveTabLocked(tabID)
	if !found {
		return AppState{}, err
	}
	return a.state, err
}

// setActiveTabLocked is shared with SetActiveTabNarrow (US-014). found=false
// means no such tab and err says so; found=true with a non-nil err is a parked
// background-write failure, not a failure to switch tabs.
func (a *App) setActiveTabLocked(tabID string) (bool, error) {
	for _, tab := range a.state.OpenTabs {
		if tab.ID == tabID {
			a.state.ActiveTabID = tabID
			return true, a.markDirty(persistScopeState)
		}
	}
	return false, fmt.Errorf("tab %s not found", tabID)
}

func (a *App) UpdateOpenTabPanes(tabID, requestPaneTab, responseTab string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.updateOpenTabPanesLocked(tabID, requestPaneTab, responseTab)
	if !found {
		return AppState{}, err
	}
	return a.state, err
}

// updateOpenTabPanesLocked is shared with UpdateOpenTabPanesNarrow (US-014).
// A rejected pane name reports found=false, because nothing was mutated.
func (a *App) updateOpenTabPanesLocked(tabID, requestPaneTab, responseTab string) (bool, error) {
	for i := range a.state.OpenTabs {
		if a.state.OpenTabs[i].ID != tabID {
			continue
		}
		if requestPaneTab != "" && !validRequestPaneTab(requestPaneTab) {
			return false, fmt.Errorf("invalid request pane tab %q", requestPaneTab)
		}
		if responseTab != "" && !validResponsePaneTab(responseTab) {
			return false, fmt.Errorf("invalid response pane tab %q", responseTab)
		}
		if requestPaneTab != "" {
			a.state.OpenTabs[i].RequestPaneTab = requestPaneTab
		}
		if responseTab != "" {
			a.state.OpenTabs[i].ResponseTab = responseTab
		}
		return true, a.markDirty(persistScopeState)
	}
	return false, fmt.Errorf("tab %s not found", tabID)
}

func (a *App) OpenRequestTab(collectionID, itemID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if _, err := findItem(collection, itemID); err != nil {
		return AppState{}, err
	}
	a.openTabLocked(collectionID, itemID, "request")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) OpenResponseExampleTab(collectionID, itemID, exampleID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	a.openResponseExampleTabLocked(collectionID, itemID, *example)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloseTab(tabID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.state.OpenTabs[:0]
	found := false
	var closed OpenTab
	for _, tab := range a.state.OpenTabs {
		if tab.ID != tabID {
			next = append(next, tab)
		} else {
			found = true
			closed = tab
		}
	}
	if !found {
		return AppState{}, fmt.Errorf("tab %s not found", tabID)
	}
	a.rememberClosedTabLocked(closed)
	a.state.OpenTabs = next
	if a.state.ActiveTabID == tabID {
		a.state.ActiveTabID = ""
		if len(a.state.OpenTabs) > 0 {
			a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
		}
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloseAllTabs() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, tab := range a.state.OpenTabs {
		a.rememberClosedTabLocked(tab)
	}
	a.state.OpenTabs = []OpenTab{}
	a.state.ActiveTabID = ""
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ReopenLastClosedTab(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collectionID = strings.TrimSpace(collectionID)
	for {
		index := a.lastClosedTabIndexLocked(collectionID)
		if index < 0 {
			break
		}
		tab := a.state.ClosedTabs[index]
		a.state.ClosedTabs = append(a.state.ClosedTabs[:index], a.state.ClosedTabs[index+1:]...)
		if !a.openTabIsRestorableLocked(tab) {
			continue
		}
		for i := range a.state.OpenTabs {
			if a.state.OpenTabs[i].ID == tab.ID {
				a.state.ActiveTabID = tab.ID
				return a.state, a.markDirty(persistScopeState)
			}
		}
		if tab.RequestPaneTab == "" {
			tab.RequestPaneTab = "params"
		}
		if tab.ResponseTab == "" {
			tab.ResponseTab = "response"
		}
		a.state.OpenTabs = append(a.state.OpenTabs, tab)
		a.state.ActiveTabID = tab.ID
		return a.state, a.markDirty(persistScopeState)
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) MoveOpenTab(tabID string, offset int) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.moveOpenTabLocked(tabID, offset)
	if !found {
		return AppState{}, err
	}
	return a.state, err
}

// moveOpenTabLocked is shared with MoveOpenTabNarrow (US-014).
//
// A no-op move — offset 0, or a move that clamps back to where the tab already
// is — reports found=true with no error and does NOT mark state dirty. That
// matches the original behaviour and is worth preserving explicitly: marking
// dirty here would bump the revision for a mutation that changed nothing, and
// the narrow callers use revision continuity to decide whether they are in
// sync.
func (a *App) moveOpenTabLocked(tabID string, offset int) (bool, error) {
	index := -1
	for i, tab := range a.state.OpenTabs {
		if tab.ID == tabID {
			index = i
			break
		}
	}
	if index < 0 {
		return false, fmt.Errorf("tab %s not found", tabID)
	}
	if offset == 0 {
		return true, nil
	}
	target := clampInt(index+offset, 0, len(a.state.OpenTabs)-1)
	if target == index {
		return true, nil
	}
	tab := a.state.OpenTabs[index]
	if target < index {
		copy(a.state.OpenTabs[target+1:index+1], a.state.OpenTabs[target:index])
	} else {
		copy(a.state.OpenTabs[index:target], a.state.OpenTabs[index+1:target+1])
	}
	a.state.OpenTabs[target] = tab
	return true, a.markDirty(persistScopeState)
}

func (a *App) CreateEnvironment(collectionID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	env := Environment{
		ID:    newID("env"),
		Name:  strings.TrimSpace(name),
		Color: "#2f8cff",
		Variables: []Variable{
			{ID: newID("var"), Name: "host", Value: "https://httpbin.org", DataType: "string", Enabled: true},
			{ID: newID("var"), Name: "token", Value: "secret-token", DataType: "string", Enabled: true, Secret: true},
		},
	}
	if env.Name == "" {
		env.Name = "Development"
	}
	collection.Environments = append(collection.Environments, env)
	collection.UpdatedAt = time.Now()
	if collection.Format != "yml" {
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.notify("success", "Environment created: "+env.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateGlobalEnvironment(workspaceID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	env := Environment{
		ID:        newID("global-env"),
		Name:      strings.TrimSpace(name),
		Color:     "#2f8cff",
		Variables: []Variable{},
	}
	if env.Name == "" {
		env.Name = "Global"
	}
	env.Name = scripting.UniqueEnvironmentName(ws.GlobalEnvironments, env.Name)
	ws.GlobalEnvironments = append(ws.GlobalEnvironments, env)
	ws.ActiveGlobalEnvironmentID = env.ID
	ws.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Global environment created: "+env.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SetActiveGlobalEnvironment(workspaceID, environmentID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	if environmentID != "" && !scripting.WorkspaceHasGlobalEnvironment(*ws, environmentID) {
		return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
	}
	ws.ActiveGlobalEnvironmentID = environmentID
	ws.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateGlobalEnvironment(workspaceID, environmentID, name, color string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	for index := range ws.GlobalEnvironments {
		if ws.GlobalEnvironments[index].ID != environmentID {
			continue
		}
		if strings.TrimSpace(name) != "" {
			ws.GlobalEnvironments[index].Name = strings.TrimSpace(name)
		}
		if strings.TrimSpace(color) != "" {
			ws.GlobalEnvironments[index].Color = strings.TrimSpace(color)
		}
		ws.UpdatedAt = time.Now()
		if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
			return AppState{}, err
		}
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) UpdateGlobalEnvironmentVariables(workspaceID, environmentID string, vars []Variable) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	for index := range ws.GlobalEnvironments {
		if ws.GlobalEnvironments[index].ID != environmentID {
			continue
		}
		ws.GlobalEnvironments[index].Variables = vars
		ws.UpdatedAt = time.Now()
		if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
			return AppState{}, err
		}
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) DeleteGlobalEnvironment(workspaceID, environmentID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	next := ws.GlobalEnvironments[:0]
	removed := false
	for _, env := range ws.GlobalEnvironments {
		if env.ID == environmentID {
			removed = true
			continue
		}
		next = append(next, env)
	}
	if !removed {
		return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
	}
	ws.GlobalEnvironments = next
	if ws.ActiveGlobalEnvironmentID == environmentID {
		ws.ActiveGlobalEnvironmentID = ""
		if len(ws.GlobalEnvironments) > 0 {
			ws.ActiveGlobalEnvironmentID = ws.GlobalEnvironments[0].ID
		}
	}
	ws.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
		return AppState{}, err
	}
	a.notify("info", "Global environment deleted")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CopyGlobalEnvironment(workspaceID, environmentID string) (AppState, error) {
	return a.CopyGlobalEnvironmentAs(workspaceID, environmentID, "")
}

func (a *App) CopyGlobalEnvironmentAs(workspaceID, environmentID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	for _, env := range ws.GlobalEnvironments {
		if env.ID != environmentID {
			continue
		}
		copyEnv := scripting.CloneEnvironmentWithNewIDs(env, "global-env")
		copyName := strings.TrimSpace(name)
		if copyName == "" {
			copyName = env.Name + " - Copy"
		}
		copyEnv.Name = scripting.UniqueEnvironmentName(ws.GlobalEnvironments, copyName)
		copyEnv.Color = ""
		ws.GlobalEnvironments = append(ws.GlobalEnvironments, copyEnv)
		ws.ActiveGlobalEnvironmentID = copyEnv.ID
		ws.UpdatedAt = time.Now()
		if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
			return AppState{}, err
		}
		a.notify("success", "Copied global environment: "+copyEnv.Name)
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) ExportGlobalEnvironment(workspaceID, environmentID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return "", err
	}
	for _, env := range ws.GlobalEnvironments {
		if env.ID == environmentID {
			return bru.StringifyBrunoEnvironmentExport(env)
		}
	}
	return "", fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) ExportGlobalEnvironments(workspaceID string, environmentIDs []string, exportFormat string) (GlobalEnvironmentExportResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return GlobalEnvironmentExportResult{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return GlobalEnvironmentExportResult{}, err
	}
	environments, err := bru.SelectedGlobalEnvironments(*ws, environmentIDs)
	if err != nil {
		return GlobalEnvironmentExportResult{}, err
	}
	if len(environments) == 0 {
		return GlobalEnvironmentExportResult{}, errors.New("no global environments selected for export")
	}
	switch strings.TrimSpace(exportFormat) {
	case "", "single-object":
		if len(environments) != 1 {
			return GlobalEnvironmentExportResult{}, errors.New("single-object export requires exactly one environment")
		}
		content, err := bru.StringifyBrunoEnvironmentExport(environments[0])
		if err != nil {
			return GlobalEnvironmentExportResult{}, err
		}
		return GlobalEnvironmentExportResult{
			Format:   "single-object",
			Filename: bru.BrunoEnvironmentExportFileName(environments[0].Name) + ".json",
			Content:  content,
		}, nil
	case "single-file":
		content, err := bru.StringifyBrunoEnvironmentExportBundle(environments)
		if err != nil {
			return GlobalEnvironmentExportResult{}, err
		}
		return GlobalEnvironmentExportResult{
			Format:   "single-file",
			Filename: "bruno-global-environments.json",
			Content:  content,
		}, nil
	case "folder":
		files, err := bru.BrunoEnvironmentExportFiles(environments)
		if err != nil {
			return GlobalEnvironmentExportResult{}, err
		}
		return GlobalEnvironmentExportResult{
			Format:   "folder",
			Filename: "bruno-global-environments",
			Files:    files,
			Content:  bru.FormatGlobalEnvironmentExportFiles(files),
		}, nil
	default:
		return GlobalEnvironmentExportResult{}, fmt.Errorf("unsupported global environment export format %q", exportFormat)
	}
}

func (a *App) SaveGlobalEnvironmentExport(workspaceID string, environmentIDs []string, exportFormat, targetPath string) (GlobalEnvironmentSaveResult, error) {
	result, err := a.ExportGlobalEnvironments(workspaceID, environmentIDs, exportFormat)
	if err != nil {
		return GlobalEnvironmentSaveResult{}, err
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		if a.ctx == nil {
			return GlobalEnvironmentSaveResult{}, errors.New("target path is required when the Wails save dialog is unavailable")
		}
		options := wailsruntime.SaveDialogOptions{
			Title:                "Save Global Environment Export",
			DefaultFilename:      result.Filename,
			CanCreateDirectories: true,
		}
		if defaultDir := a.defaultSaveDialogDirectory(); defaultDir != "" {
			options.DefaultDirectory = defaultDir
		}
		if result.Format != "folder" {
			options.Filters = []wailsruntime.FileFilter{{DisplayName: "JSON Files (*.json)", Pattern: "*.json"}}
		}
		targetPath, err = wailsruntime.SaveFileDialog(a.ctx, options)
		if err != nil {
			return GlobalEnvironmentSaveResult{}, err
		}
		if strings.TrimSpace(targetPath) == "" {
			return GlobalEnvironmentSaveResult{Format: result.Format, Cancelled: true}, nil
		}
	}
	targetPath, err = expandUserExportPath(targetPath)
	if err != nil {
		return GlobalEnvironmentSaveResult{}, err
	}
	files, err := writeGlobalEnvironmentExportResult(result, targetPath)
	if err != nil {
		return GlobalEnvironmentSaveResult{}, err
	}
	return GlobalEnvironmentSaveResult{
		Format: result.Format,
		Path:   targetPath,
		Files:  files,
	}, nil
}

func (a *App) ImportGlobalEnvironment(workspaceID, content string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	environments, err := yamlstore.ParseImportedGlobalEnvironments(content)
	if err != nil {
		return AppState{}, err
	}
	if len(environments) == 0 {
		return AppState{}, errors.New("no global environments found to import")
	}
	for _, env := range environments {
		env.ID = newID("global-env")
		env.Name = scripting.UniqueEnvironmentName(ws.GlobalEnvironments, env.Name)
		for index := range env.Variables {
			if env.Variables[index].ID == "" {
				env.Variables[index].ID = newID("var")
			}
		}
		ws.GlobalEnvironments = append(ws.GlobalEnvironments, env)
		ws.ActiveGlobalEnvironmentID = env.ID
	}
	ws.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
		return AppState{}, err
	}
	if len(environments) == 1 {
		a.notify("success", "Imported global environment: "+environments[0].Name)
	} else {
		a.notify("success", fmt.Sprintf("Imported %d global environments", len(environments)))
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ListDotEnvFiles(workspaceID, collectionID string) ([]DotEnvFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.dotEnvContextLocked(workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	return scripting.DotEnvFilesForContext(ws, collection)
}

func (a *App) ResolveProcessEnvValues(collectionID string, names []string) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return nil, err
	}
	processEnv := scripting.ProcessEnvForCollection(collection, ws.Path)
	values := map[string]string{}
	for _, requested := range names {
		key := strings.TrimSpace(requested)
		name, ok := strings.CutPrefix(key, interp.ProcessEnvPrefix)
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		values[key] = processEnv[name]
	}
	return values, nil
}

func (a *App) SaveDotEnvFile(workspaceID, collectionID, scope, name, content string) ([]DotEnvFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.dotEnvContextLocked(workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	basePath, err := scripting.DotEnvScopePath(ws, collection, scope)
	if err != nil {
		return nil, err
	}
	filename, err := scripting.NormalizeDotEnvFilename(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(basePath, filename), []byte(content), 0o600); err != nil {
		return nil, err
	}
	a.notify("success", "Saved "+filename)
	return scripting.DotEnvFilesForContext(ws, collection)
}

func (a *App) DeleteDotEnvFile(workspaceID, collectionID, scope, name string) ([]DotEnvFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.dotEnvContextLocked(workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	basePath, err := scripting.DotEnvScopePath(ws, collection, scope)
	if err != nil {
		return nil, err
	}
	filename, err := scripting.NormalizeDotEnvFilename(name)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(basePath, filename)); err != nil {
		return nil, err
	}
	a.notify("info", "Deleted "+filename)
	return scripting.DotEnvFilesForContext(ws, collection)
}

func (a *App) UpdateCollectionVariables(collectionID string, vars []Variable) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Variables = vars
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateEnvironmentVariables(collectionID, environmentID string, vars []Variable) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	for index := range collection.Environments {
		if collection.Environments[index].ID != environmentID {
			continue
		}
		collection.Environments[index].Variables = vars
		collection.UpdatedAt = time.Now()
		if collection.Format != "yml" {
			if err := a.writeCollectionFilesLocked(collection); err != nil {
				return AppState{}, err
			}
		}
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("environment %s not found", environmentID)
}

func (a *App) UpdateCollectionHeaders(collectionID string, headers []KeyValue) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Headers = headers
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionAuth(collectionID string, auth AuthConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Auth = auth
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionProxy(collectionID string, proxy ProxyConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Proxy = transport.NormalizeProxyConfig(proxy)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionSecurityConfig(collectionID string, config CollectionSecurityConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.SecurityConfig = normalizeCollectionSecurityConfig(config)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdatePreferences(preferences Preferences) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	next := normalizePreferences(preferences)
	if tlsSessionPreferencesChanged(a.state.Preferences, next) {
		a.tlsSessionCache = nil
		// The cache key already separates the old and new TLS postures, so
		// this is not what keeps them apart — it stops connections opened
		// under the previous trust settings from idling on in a pool the new
		// settings would never have authorised.
		a.transportCache.flush()
	}
	if a.state.Preferences.Proxy != next.Proxy || a.state.Preferences.ProxyMode != next.ProxyMode {
		// Same reasoning for the proxy: the key already separates postures,
		// the flush retires sockets opened through the previous proxy.
		a.transportCache.flush()
	}
	a.state.Preferences = next
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearSSLSessionCache() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	a.tlsSessionCache = nil
	// Cached transports hold the old session cache; drop them so a cleared
	// cache really means no resumption from the tickets it held.
	a.transportCache.flush()
	return a.state, nil
}

func (a *App) GetFileCacheSize() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return 0, err
	}
	return a.fileCacheSizeLocked()
}

func (a *App) ClearFileCache() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return 0, err
	}
	path := a.collectionFileCachePathLocked()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return 0, nil
}

func (a *App) SelectCustomCaCertificate() (string, error) {
	if a.ctx == nil {
		return "", errors.New("file dialog is unavailable")
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Custom CA Certificate",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Certificate Files (*.pem, *.crt, *.cer)", Pattern: "*.pem;*.crt;*.cer"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})
}

// SelectRunnerDataFile picks the CSV or JSON file that drives a data-driven
// run (US-046). The filters mirror runnerDataRows' accepted extensions, which
// are matched on extension rather than sniffed content.
func (a *App) SelectRunnerDataFile() (string, error) {
	if a.ctx == nil {
		return "", errors.New("file dialog is unavailable")
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Runner Data File",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Data Files (*.csv, *.json)", Pattern: "*.csv;*.json"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})
}

func (a *App) SelectDefaultLocation() (string, error) {
	if a.ctx == nil {
		return "", errors.New("directory dialog is unavailable")
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:                "Select Default Location",
		CanCreateDirectories: true,
	})
}

func (a *App) UpdateCollectionClientCertificates(collectionID string, certs []ClientCertificateConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.ClientCertificates = transport.NormalizeClientCertificateRows(certs)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionPresets(collectionID string, presets CollectionPresets) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Presets = normalizeCollectionPresets(presets)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionProtobuf(collectionID string, protobuf CollectionProtobufConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Protobuf = normalizeCollectionProtobuf(collection.Path, protobuf)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionDocs(collectionID string, docs string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Docs = docs
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionScripts(collectionID string, preScript, postScript, tests string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.PreScript = preScript
	collection.PostScript = postScript
	collection.Tests = tests
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateFolderSettings(collectionID, folderPath string, updated FolderConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	folder, err := findFolderConfig(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	mergeFolderSettingsUpdate(folder, updated)
	collection.UpdatedAt = time.Now()
	if err := a.writeFolderConfigLocked(collection, *folder); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Saved folder settings for "+firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ImportCollection(workspaceID string, payload ImportPayload) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	collection, err := collectionFromImport(payload)
	if err != nil {
		return AppState{}, err
	}
	before, err := cloneCollectionImportState(a.state)
	if err != nil {
		return AppState{}, err
	}
	watchFingerprints := map[string]string{}
	for key, value := range a.collectionWatchFingerprints {
		watchFingerprints[key] = value
	}
	mutations := []collectionImportMutation{}
	rollback := func() {
		rollbackCollectionImportMutations(a, mutations)
		a.state = before
		a.collectionWatchFingerprints = watchFingerprints
		if payload.OpenAPISync && strings.EqualFold(strings.TrimSpace(payload.Kind), "openapi") && collection.Path != "" {
			a.cleanupOpenAPISyncSpecLocked(collection.Path)
		}
	}
	if payload.OpenAPISync && strings.EqualFold(strings.TrimSpace(payload.Kind), "openapi") {
		hash, doc, err := openapisync.ValidateOpenAPISyncSpec(payload.Content)
		if err != nil {
			return AppState{}, err
		}
		collection.OpenAPI = []OpenAPISyncConfig{newOpenAPISyncConfig(payload.SourceURL, payload.GroupBy, hash)}
		if doc.Info.Title != "" {
			collection.Name = firstNonEmpty(collection.Name, doc.Info.Title)
		}
	}
	if err := a.applyImportedCollectionLocked(ws, &collection, ws.Path, "", &mutations); err != nil {
		rollback()
		return AppState{}, err
	}
	if payload.OpenAPISync && strings.EqualFold(strings.TrimSpace(payload.Kind), "openapi") {
		if err := a.saveOpenAPISyncSpecLocked(collection.Path, payload.SourceURL, payload.Content); err != nil {
			rollback()
			return AppState{}, err
		}
	}
	a.notify("success", "Imported "+collection.Name)
	persist := a.persistLocked
	if a.collectionImportHooks != nil && a.collectionImportHooks.persist != nil {
		persist = func() error { return a.collectionImportHooks.persist(a) }
	}
	if err := persist(); err != nil {
		rollback()
		return AppState{}, err
	}
	for _, mutation := range mutations {
		if mutation.backup != "" {
			_ = removeCollectionImportPath(a, mutation.backup)
		}
	}
	return a.state, nil
}

func (a *App) ConnectOpenAPISync(collectionID string, options OpenAPISyncOptions) (AppState, error) {
	collection, content, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return AppState{}, err
	}
	hash, _, err := openapisync.ValidateOpenAPISyncSpec(content)
	if err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	current, err := a.findCollectionLocked(collection.ID)
	if err != nil {
		return AppState{}, err
	}
	current.OpenAPI = []OpenAPISyncConfig{newOpenAPISyncConfig(sourceURL, groupBy, hash)}
	if err := a.saveOpenAPISyncSpecLocked(current.Path, sourceURL, content); err != nil {
		return AppState{}, err
	}
	if err := a.writeCollectionFilesLocked(current); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync connected")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CheckOpenAPISync(collectionID string, options OpenAPISyncOptions) (OpenAPISyncResult, error) {
	collection, content, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return OpenAPISyncResult{}, err
	}
	specCollection, hash, doc, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, groupBy)
	if err != nil {
		return OpenAPISyncResult{}, err
	}
	return openapisync.BuildOpenAPISyncResult(collection, specCollection, sourceURL, groupBy, hash, doc), nil
}

func (a *App) CheckOpenAPIUpdates(collectionID string) (OpenAPISyncUpdateCheckResult, error) {
	collection, content, sourceURL, _, err := a.openAPISyncInputs(collectionID, OpenAPISyncOptions{})
	if err != nil {
		return OpenAPISyncUpdateCheckResult{}, err
	}
	remoteHash, _, err := openapisync.ValidateOpenAPISyncSpec(content)
	if err != nil {
		return OpenAPISyncUpdateCheckResult{}, err
	}
	config := firstOpenAPISyncConfig(collection)
	return OpenAPISyncUpdateCheckResult{
		SourceURL:      sourceURL,
		StoredSpecHash: config.SpecHash,
		RemoteSpecHash: remoteHash,
		HasUpdates:     strings.TrimSpace(config.SpecHash) == "" || config.SpecHash != remoteHash,
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (a *App) GetOpenAPISyncSpec(collectionID string) (OpenAPISyncSpecViewResult, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return OpenAPISyncSpecViewResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return OpenAPISyncSpecViewResult{}, err
	}
	if len(collection.OpenAPI) == 0 {
		a.mu.Unlock()
		return OpenAPISyncSpecViewResult{}, errors.New("OpenAPI sync is not connected")
	}
	config := firstOpenAPISyncConfig(*collection)
	collectionPath := collection.Path
	sourceURL := config.SourceURL
	content, noStoredSpec, err := a.loadOpenAPISyncSpecLocked(collection.Path)
	client := a.httpClient
	a.mu.Unlock()
	if err != nil {
		return OpenAPISyncSpecViewResult{}, err
	}
	result := OpenAPISyncSpecViewResult{
		SourceURL:    sourceURL,
		Content:      content,
		FromCache:    !noStoredSpec,
		NoStoredSpec: noStoredSpec,
	}
	if !noStoredSpec {
		return result, nil
	}
	if strings.TrimSpace(sourceURL) == "" {
		return result, errors.New("spec file not found; sync your collection first")
	}
	fetched, err := fetchOpenAPISyncContent(collectionPath, sourceURL, client)
	if err != nil {
		return result, err
	}
	if _, _, err := openapisync.ValidateOpenAPISyncSpec(fetched); err != nil {
		return result, err
	}
	result.Content = fetched
	result.Fetched = true
	return result, nil
}

func (a *App) GetOpenAPISyncSpecDiff(collectionID string, options OpenAPISyncOptions) (OpenAPISyncSpecDiffResult, error) {
	collection, newContent, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return OpenAPISyncSpecDiffResult{}, err
	}
	if len(collection.OpenAPI) == 0 {
		return OpenAPISyncSpecDiffResult{}, errors.New("OpenAPI sync is not connected")
	}
	specCollection, newHash, doc, err := openapisync.OpenAPISyncCollectionFromContent(newContent, collection.Name, groupBy)
	if err != nil {
		return OpenAPISyncSpecDiffResult{}, err
	}
	a.mu.Lock()
	storedContent, noStoredSpec, err := a.loadOpenAPISyncSpecLocked(collection.Path)
	a.mu.Unlock()
	if err != nil {
		return OpenAPISyncSpecDiffResult{}, err
	}
	storedHash := firstOpenAPISyncConfig(collection).SpecHash
	if strings.TrimSpace(storedContent) != "" {
		if hash, _, err := openapisync.ValidateOpenAPISyncSpec(storedContent); err == nil {
			storedHash = hash
		}
	}
	check := openapisync.BuildOpenAPISyncResult(collection, specCollection, sourceURL, groupBy, newHash, doc)
	return OpenAPISyncSpecDiffResult{
		SourceURL:      sourceURL,
		StoredContent:  storedContent,
		NewContent:     newContent,
		NoStoredSpec:   noStoredSpec,
		StoredSpecHash: storedHash,
		NewSpecHash:    newHash,
		Added:          check.Added,
		Updated:        check.Updated,
		Removed:        check.Removed,
		Unchanged:      check.Unchanged,
		Changes:        check.Changes,
		Lines:          openapisync.BuildOpenAPISpecDiffLines(storedContent, newContent),
	}, nil
}

func (a *App) UpdateOpenAPISyncConfig(collectionID string, config OpenAPISyncConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(config.SourceURL) == "" {
		return AppState{}, errors.New("OpenAPI source URL or file path is required")
	}
	if err := validateOpenAPISyncSource(config.SourceURL); err != nil {
		return AppState{}, err
	}
	existing := firstOpenAPISyncConfig(*collection)
	existing.SourceURL = strings.TrimSpace(config.SourceURL)
	if strings.TrimSpace(config.GroupBy) != "" {
		existing.GroupBy = normalizeOpenAPISyncGroupBy(config.GroupBy)
	}
	existing.AutoCheck = config.AutoCheck
	existing.AutoCheckInterval = normalizeOpenAPISyncAutoCheckInterval(config.AutoCheckInterval)
	collection.OpenAPI = []OpenAPISyncConfig{normalizeOpenAPISyncConfig(existing)}
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync settings saved")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ApplyOpenAPISync(collectionID string, options OpenAPISyncOptions) (AppState, error) {
	collection, content, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return AppState{}, err
	}
	specCollection, hash, _, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, groupBy)
	if err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	current, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	removedIDs := openapisync.ApplyOpenAPISyncToCollection(current, specCollection, options)
	a.removeOpenTabsForRequestIDsLocked(collectionID, removedIDs)
	current.OpenAPI = []OpenAPISyncConfig{newOpenAPISyncConfigPreservingSettings(sourceURL, groupBy, hash, firstOpenAPISyncConfig(*current))}
	if err := a.saveOpenAPISyncSpecLocked(current.Path, sourceURL, content); err != nil {
		return AppState{}, err
	}
	if err := a.writeCollectionFilesLocked(current); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync applied")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CheckOpenAPILocalDrift(collectionID string) (OpenAPILocalDriftResult, error) {
	collection, content, config, noStoredSpec, err := a.openAPILocalDriftInputs(collectionID)
	if err != nil {
		return OpenAPILocalDriftResult{}, err
	}
	result := OpenAPILocalDriftResult{
		SourceURL:    config.SourceURL,
		GroupBy:      normalizeOpenAPISyncGroupBy(config.GroupBy),
		LastSyncDate: config.LastSyncDate,
		NoStoredSpec: noStoredSpec,
	}
	if noStoredSpec {
		return result, nil
	}
	specCollection, _, _, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, result.GroupBy)
	if err != nil {
		return OpenAPILocalDriftResult{}, err
	}
	return openapisync.BuildOpenAPILocalDriftResult(collection, specCollection, config), nil
}

func (a *App) ApplyOpenAPILocalDrift(collectionID string, options OpenAPILocalDriftOptions) (AppState, error) {
	collection, content, config, noStoredSpec, err := a.openAPILocalDriftInputs(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if noStoredSpec {
		return AppState{}, errors.New("OpenAPI stored spec is missing")
	}
	specCollection, _, _, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, config.GroupBy)
	if err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	current, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	removedIDs, err := openapisync.ApplyOpenAPILocalDriftToCollection(current, specCollection, options)
	if err != nil {
		return AppState{}, err
	}
	a.removeOpenTabsForRequestIDsLocked(collectionID, removedIDs)
	if err := a.writeCollectionFilesLocked(current); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI collection changes applied")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DisconnectOpenAPISync(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	a.cleanupOpenAPISyncSpecLocked(collection.Path)
	collection.OpenAPI = nil
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync disconnected")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ExportCollection(collectionID string) (string, error) {
	result, err := a.ExportCollectionWithOptions(collectionID, CollectionExportOptions{Format: "yaml"})
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func (a *App) ExportCollectionWithOptions(collectionID string, options CollectionExportOptions) (CollectionExportResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionExportResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return CollectionExportResult{}, err
	}
	snapshot := collectionShareSnapshot(*collection)
	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format == "" {
		format = "zip"
	}
	switch format {
	case "yaml", "yml":
		content, folderCount, requestCount, err := buildCollectionShareYAML(snapshot, time.Now().UTC())
		if err != nil {
			return CollectionExportResult{}, err
		}
		return CollectionExportResult{
			Format:           "yaml",
			Filename:         sanitizeFilename(snapshot.Name) + ".yml",
			Content:          content,
			MimeType:         "application/x-yaml",
			FolderCount:      folderCount,
			RequestCount:     requestCount,
			EnvironmentCount: len(snapshot.Environments),
		}, nil
	case "zip":
		files, folderCount, requestCount, err := buildCollectionZipExportFiles(snapshot)
		if err != nil {
			return CollectionExportResult{}, err
		}
		data, err := zipCollectionExportFiles(files)
		if err != nil {
			return CollectionExportResult{}, err
		}
		return CollectionExportResult{
			Format:           "zip",
			Filename:         sanitizeFilename(snapshot.Name) + ".zip",
			ContentBase64:    base64.StdEncoding.EncodeToString(data),
			MimeType:         "application/zip",
			FolderCount:      folderCount,
			RequestCount:     requestCount,
			EnvironmentCount: len(snapshot.Environments),
		}, nil
	case "postman":
		content, requestCount, skippedTypes, err := buildPostmanCollectionExport(snapshot)
		if err != nil {
			return CollectionExportResult{}, err
		}
		warning := ""
		if len(skippedTypes) > 0 {
			warning = fmt.Sprintf("Note: %s requests in this collection will not be exported", strings.Join(skippedTypes, ", "))
		}
		return CollectionExportResult{
			Format:           "postman",
			Filename:         sanitizeFilename(snapshot.Name) + ".json",
			Content:          content,
			MimeType:         "application/json",
			Warning:          warning,
			SkippedTypes:     skippedTypes,
			FolderCount:      len(snapshot.Folders),
			RequestCount:     requestCount,
			EnvironmentCount: len(snapshot.Environments),
		}, nil
	default:
		return CollectionExportResult{}, fmt.Errorf("unsupported collection export format %q", options.Format)
	}
}

func (a *App) SaveCollectionExport(collectionID string, options CollectionExportOptions, targetPath string) (CollectionSaveResult, error) {
	result, err := a.ExportCollectionWithOptions(collectionID, options)
	if err != nil {
		return CollectionSaveResult{}, err
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		if a.ctx == nil {
			return CollectionSaveResult{}, errors.New("target path is required when the Wails save dialog is unavailable")
		}
		dialogOptions := wailsruntime.SaveDialogOptions{
			Title:                "Share Collection",
			DefaultFilename:      result.Filename,
			CanCreateDirectories: true,
		}
		if defaultDir := a.defaultSaveDialogDirectory(); defaultDir != "" {
			dialogOptions.DefaultDirectory = defaultDir
		}
		switch result.Format {
		case "zip":
			dialogOptions.Filters = []wailsruntime.FileFilter{{DisplayName: "Zip Files (*.zip)", Pattern: "*.zip"}}
		case "postman":
			dialogOptions.Filters = []wailsruntime.FileFilter{{DisplayName: "JSON Files (*.json)", Pattern: "*.json"}}
		default:
			dialogOptions.Filters = []wailsruntime.FileFilter{{DisplayName: "YAML Files (*.yml)", Pattern: "*.yml"}}
		}
		targetPath, err = wailsruntime.SaveFileDialog(a.ctx, dialogOptions)
		if err != nil {
			return CollectionSaveResult{}, err
		}
		if strings.TrimSpace(targetPath) == "" {
			return CollectionSaveResult{Format: result.Format, Cancelled: true}, nil
		}
	}
	targetPath, err = expandUserExportPath(targetPath)
	if err != nil {
		return CollectionSaveResult{}, err
	}
	if info, err := os.Stat(targetPath); err == nil && info.IsDir() {
		targetPath = filepath.Join(targetPath, result.Filename)
	}
	data, err := collectionExportBytes(result)
	if err != nil {
		return CollectionSaveResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return CollectionSaveResult{}, err
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return CollectionSaveResult{}, err
	}
	return CollectionSaveResult{Format: result.Format, Path: targetPath}, nil
}

func (a *App) GenerateCollectionDocs(collectionID string, options GenerateCollectionDocsOptions) (GenerateCollectionDocsResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	snapshot := *collection
	snapshot.Items = append([]RequestItem(nil), collection.Items...)
	snapshot.Folders = append([]FolderConfig(nil), collection.Folders...)
	snapshot.Environments = append([]Environment(nil), collection.Environments...)
	yamlContent, folderCount, requestCount, err := buildCollectionDocsYAML(snapshot, options.EnvironmentIDs, time.Now().UTC())
	if err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	htmlContent, err := buildCollectionDocsHTML(snapshot.Name, yamlContent)
	if err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	return GenerateCollectionDocsResult{
		FileName:     sanitizeFilename(snapshot.Name) + "-documentation.html",
		HTML:         htmlContent,
		YAML:         yamlContent,
		Version:      collectionDisplayVersion(snapshot.Version),
		FolderCount:  folderCount,
		RequestCount: requestCount,
	}, nil
}

func (a *App) ParseBru(content string) (RequestItem, error) {
	return bru.Parse(content)
}

func (a *App) StringifyBru(item RequestItem) string {
	return bru.StringifyBru(item)
}

func (a *App) ResetDemoData() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspaceRuntime != nil {
		return AppState{}, errors.New("reset demo data is unavailable from a scoped workspace window")
	}
	a.state = defaultState(a.dataDir)
	if err := a.writeFreshDefaultCollectionFilesLocked(); err != nil {
		return AppState{}, err
	}
	a.oauth2Mu.Lock()
	a.oauth2 = map[string]oauth2TokenResponse{}
	a.oauth2Mu.Unlock()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ensureReady() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ensureReadyLocked()
}

// ensureReadyLocked requires the WRITE lock on a.mu, on every call, including
// calls against an already-initialised App. It is not a readiness *check*: the
// steady-state path still runs refreshGitCollectionAvailabilityLocked (which
// unconditionally assigns collection.NotFoundLocally for every collection) and
// pruneExpiredCookiesLocked (which unconditionally reassigns a.state.Cookies
// and rewrites its backing array in place), and the scoped-workspace path
// still calls workspaceRuntime.heartbeat, which renews the on-disk ownership
// lease. There is therefore no side-effect-free predicate that could serve as
// the fast path of a double-checked RLock, which is why GetState and every
// other caller of this function hold Lock rather than RLock.
func (a *App) ensureReadyLocked() error {
	if a.workspaceRuntime != nil {
		if len(a.state.Workspaces) != 1 || a.state.ActiveWorkspaceID != a.workspaceRuntime.intent.WorkspaceID || a.state.Workspaces[0].ID != a.workspaceRuntime.intent.WorkspaceID {
			return errors.New("scoped workspace runtime state is invalid")
		}
		if err := a.workspaceRuntime.heartbeat(); err != nil {
			return fmt.Errorf("workspace ownership was lost: %w", err)
		}
		if len(a.state.FeatureLedger) == 0 {
			a.state.FeatureLedger = bru.DefaultFeatures()
		}
		return nil
	}
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	if len(a.state.Workspaces) == 0 {
		a.state = defaultState(a.dataDir)
		if err := a.writeFreshDefaultCollectionFilesLocked(); err != nil {
			return err
		}
		if _, err := a.ensureScratchCollectionsLocked(); err != nil {
			return err
		}
		return a.persistLocked()
	}
	if len(a.state.FeatureLedger) == 0 {
		a.state.FeatureLedger = bru.DefaultFeatures()
	}
	_, stateFileErr := os.Stat(filepath.Join(a.dataDir, "state.json"))
	freshState := errors.Is(stateFileErr, os.ErrNotExist)
	if stateFileErr != nil && !freshState {
		return stateFileErr
	}
	changed := a.normalizeStateLocked()
	if freshState {
		if err := a.writeFreshDefaultCollectionFilesLocked(); err != nil {
			return err
		}
		changed = true
	}
	scratchChanged, err := a.ensureScratchCollectionsLocked()
	if err != nil {
		return err
	}
	changed = changed || scratchChanged
	envChanged, err := a.prepareWorkspaceGlobalEnvironmentsLocked()
	if err != nil {
		return err
	}
	changed = changed || envChanged
	if err := a.hydrateStateEnvironmentSecretsLocked(); err != nil {
		return err
	}
	if changed {
		if err := a.persistLocked(); err != nil {
			return err
		}
	}
	a.refreshGitCollectionAvailabilityLocked()
	a.pruneExpiredCookiesLocked()
	return nil
}

// NewAppWithDir starts with an in-memory sample so the first frame is useful.
// On a genuinely fresh state directory, materialize that sample before state
// persistence so its SAVED label and filesystem/recovery actions are truthful.
func (a *App) writeFreshDefaultCollectionFilesLocked() error {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			if collection.Scratch || collection.NotFoundLocally || strings.TrimSpace(collection.Path) == "" || !pathInside(a.dataDir, collection.Path) {
				continue
			}
			if info, err := os.Stat(collection.Path); err == nil {
				if !info.IsDir() {
					return fmt.Errorf("default collection path %s is not a directory", collection.Path)
				}
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := a.writeCollectionFilesLocked(collection); err != nil {
				return fmt.Errorf("write default collection %s: %w", collection.Name, err)
			}
			a.seedCollectionWatchFingerprintLocked(collection.Path)
		}
	}
	return nil
}

func (a *App) load() error {
	data, err := os.ReadFile(filepath.Join(a.dataDir, "state.json"))
	if err != nil {
		return err
	}
	var state AppState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if len(state.FeatureLedger) == 0 {
		state.FeatureLedger = bru.DefaultFeatures()
	}
	state.Cookies = decryptCookieValuesForRuntime(a.dataDir, state.Cookies)
	a.state = state
	// US-009 step 3. Backfill body handles for responses loaded from a
	// state.json written before the store existed. Deliberately best-effort and
	// deliberately not part of `changed`: it must not fail a load, and it must
	// not force a rewrite of state.json on every startup. See the function.
	a.migrateResponseBodiesLocked()
	changed := a.normalizeStateLocked()
	scratchChanged, err := a.ensureScratchCollectionsLocked()
	if err != nil {
		return err
	}
	changed = changed || scratchChanged
	envChanged, err := a.prepareWorkspaceGlobalEnvironmentsLocked()
	if err != nil {
		return err
	}
	changed = changed || envChanged
	if err := a.hydrateStateEnvironmentSecretsLocked(); err != nil {
		return err
	}
	if changed {
		if err := a.persistLocked(); err != nil {
			return err
		}
	}
	a.refreshGitCollectionAvailabilityLocked()
	a.pruneExpiredCookiesLocked()
	return nil
}

func (a *App) normalizeStateLocked() bool {
	changed := false
	normalizedPreferences := normalizePreferences(a.state.Preferences)
	if !reflect.DeepEqual(a.state.Preferences, normalizedPreferences) {
		a.state.Preferences = normalizedPreferences
		changed = true
	}
	normalizedNotifications := normalizeNotifications(a.state.Notifications)
	if !reflect.DeepEqual(a.state.Notifications, normalizedNotifications) {
		a.state.Notifications = normalizedNotifications
		changed = true
	}
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			normalizedSecurity := normalizeCollectionSecurityConfig(collection.SecurityConfig)
			if !reflect.DeepEqual(collection.SecurityConfig, normalizedSecurity) {
				collection.SecurityConfig = normalizedSecurity
				changed = true
			}
		}
	}
	return changed
}

func normalizeNotifications(notifications []Notification) []Notification {
	if len(notifications) == 0 {
		return []Notification{}
	}
	limit := len(notifications)
	if limit > 50 {
		limit = 50
	}
	out := make([]Notification, 0, limit)
	for _, notification := range notifications[:limit] {
		notification.ID = strings.TrimSpace(notification.ID)
		if notification.ID == "" {
			notification.ID = newID("notification")
		}
		notification.Level = strings.ToLower(strings.TrimSpace(notification.Level))
		if notification.Level == "" {
			notification.Level = "info"
		}
		notification.Message = strings.TrimSpace(notification.Message)
		notification.Title = strings.TrimSpace(notification.Title)
		notification.Description = strings.TrimSpace(notification.Description)
		notification.Type = strings.TrimSpace(notification.Type)
		notification.Color = strings.TrimSpace(notification.Color)
		if notification.Message == "" && notification.Description != "" {
			notification.Message = notification.Description
		}
		if notification.Description == "" {
			notification.Description = notification.Message
		}
		if notification.Title == "" {
			notification.Title = notificationTitle(notification.Level, notification.Message)
		}
		if notification.Type == "" {
			notification.Type = notificationType(notification.Level)
		}
		if notification.Color == "" {
			notification.Color = notificationColor(notification.Level)
		}
		if notification.At.IsZero() {
			notification.At = time.Now()
		}
		out = append(out, notification)
	}
	return out
}

func (a *App) ensureScratchCollectionsLocked() (bool, error) {
	changed := false
	for wi := range a.state.Workspaces {
		didChange, err := a.ensureWorkspaceScratchCollectionLocked(&a.state.Workspaces[wi])
		if err != nil {
			return changed, err
		}
		changed = changed || didChange
	}
	return changed, nil
}

func (a *App) ensureWorkspaceScratchCollectionLocked(workspace *Workspace) (bool, error) {
	if workspace == nil {
		return false, nil
	}
	if workspace.ScratchCollectionID != "" {
		for ci := range workspace.Collections {
			collection := &workspace.Collections[ci]
			if collection.ID != workspace.ScratchCollectionID {
				continue
			}
			changed := false
			if !collection.Scratch {
				collection.Scratch = true
				changed = true
			}
			if collection.Name == "" {
				collection.Name = "Scratch"
				changed = true
			}
			if workspace.ScratchTempDirectory == "" && collection.Path != "" {
				workspace.ScratchTempDirectory = collection.Path
				changed = true
			}
			if collection.Path == "" {
				path, err := a.newScratchDirectory(workspace)
				if err != nil {
					return changed, err
				}
				collection.Path = path
				workspace.ScratchTempDirectory = path
				changed = true
			}
			if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
				return changed, err
			}
			return changed, nil
		}
	}
	now := time.Now()
	path, err := a.newScratchDirectory(workspace)
	if err != nil {
		return false, err
	}
	collectionID := newID("scratch")
	collection := Collection{
		ID:             collectionID,
		Name:           "Scratch",
		Path:           path,
		Format:         "yml",
		Scratch:        true,
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		Docs:           "# Scratch\nTemporary requests for this workspace.\n",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	workspace.ScratchCollectionID = collectionID
	workspace.ScratchTempDirectory = path
	insertAt := scratchCollectionInsertIndex(workspace.Collections)
	workspace.Collections = append(workspace.Collections, Collection{})
	copy(workspace.Collections[insertAt+1:], workspace.Collections[insertAt:])
	workspace.Collections[insertAt] = collection
	workspace.UpdatedAt = now
	if err := a.writeScratchCollectionMetadataLocked(&workspace.Collections[insertAt]); err != nil {
		return false, err
	}
	return true, nil
}

func scratchCollectionInsertIndex(collections []Collection) int {
	if len(collections) == 0 {
		return 0
	}
	if countRegularCollections(collections) == 0 {
		for index, collection := range collections {
			if collection.Scratch {
				return index
			}
		}
		return 0
	}
	return 1
}

func countRegularCollections(collections []Collection) int {
	count := 0
	for _, collection := range collections {
		if !collection.Scratch {
			count++
		}
	}
	return count
}

func firstScratchCollectionIndex(collections []Collection) int {
	for index, collection := range collections {
		if collection.Scratch {
			return index
		}
	}
	return -1
}

func (a *App) newScratchDirectory(workspace *Workspace) (string, error) {
	base := filepath.Join(a.dataDir, "transient")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "bruno-scratch-")
}

func (a *App) writeScratchCollectionMetadataLocked(collection *Collection) error {
	if collection == nil || !collection.Scratch {
		return nil
	}
	if strings.TrimSpace(collection.Path) == "" {
		return errors.New("scratch collection path is empty")
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "opencollection.yml"), []byte(stringifyYAMLCollection(*collection)), 0o600); err != nil {
		return err
	}
	metadata := map[string]string{
		"type": "scratch",
	}
	if ws, _ := scripting.FindWorkspaceForCollection(&a.state, collection.ID); ws != nil {
		metadata["workspaceUid"] = ws.ID
		metadata["workspacePath"] = ws.Path
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(collection.Path, "metadata.json"), data, 0o600)
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
	data, err := json.Marshal(stateForStorage(a.state, a.dataDir))
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(a.dataDir, "state.json"), data, 0o600)
}

// writeFileAtomic writes data through a temporary file in the same directory,
// fsyncs it, then renames it into place. os.Rename within one filesystem is
// atomic, so a process killed at any point leaves either the previous file or
// the complete new one — never the truncated file a bare os.WriteFile can leave
// behind. Unlike writePrivateAtomic it does not create or re-chmod the parent
// directory, so it can be used on directories whose mode is already meaningful.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Close is checked rather than deferred-and-ignored: US-003 found a
	// truncated copy reported as success because a write handle's Close error
	// was dropped.
	if err := f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

// ---------------------------------------------------------------------------
// US-012 — coalesced asynchronous persistence
//
// persistLocked above is still the only thing that writes state.json; what
// changed is who calls it. Mutators call markDirty, and a single background
// writer collapses a burst of marks into one write after a quiet period.
// UpdateRequest fires on every keystroke, so the synchronous version serialised
// and wrote the entire workspace — cached response bodies included — per typed
// character, while holding the global mutex.
//
// markDirty is not free: it still writes the environment secret store, because
// that file is the only home of values the rest of the app re-reads
// immediately. See persistEnvironmentSecretsLocked. What it defers is the part
// that scales with workspace size.
// ---------------------------------------------------------------------------

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
func (a *App) attachResponseBody(response *Response) error {
	if response == nil || response.Body == "" || response.BodyHandle != "" {
		return nil
	}
	store, err := a.responseStore()
	if err != nil {
		return err
	}
	handle, err := store.Put([]byte(response.Body))
	if err != nil {
		return err
	}
	response.BodyHandle = string(handle)
	response.BodyHead = responseBodyHead(response.Body)
	return nil
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

// responseStore returns the App's body store, creating it on first use.
//
// Returns an error rather than a nil store when the directory cannot be made:
// a caller that silently got nil would write a body nowhere and report success,
// which is the failure mode this whole story exists to remove.
func (a *App) responseStore() (*responseStore, error) {
	a.responsesMu.Lock()
	defer a.responsesMu.Unlock()
	if a.responses != nil {
		return a.responses, nil
	}
	dir := a.dataDir
	if dir == "" {
		dir = defaultDataDir()
	}
	store, err := newResponseStore(dir)
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

// reportPersistFailureLocked surfaces a background write failure everywhere it
// can be seen without a caller: the Wails log, and a notification the frontend
// already renders. It fires only on the first failure of a streak so a
// permanently unwritable directory does not flood the (20-entry) notification
// list and evict everything else.
func (a *App) reportPersistFailureLocked(err error) {
	a.persistMu.Lock()
	first := a.persistFailures == 0
	a.persistMu.Unlock()
	if !first {
		return
	}
	message := "Could not save workspace state: " + err.Error()
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

// TLS client certificates and proxy resolution moved to internal/transport.

func normalizeCollectionPresets(presets CollectionPresets) CollectionPresets {
	presets.RequestType = normalizePresetRequestType(presets.RequestType)
	presets.RequestURL = strings.TrimSpace(presets.RequestURL)
	return presets
}

func normalizePresetRequestType(requestType string) string {
	switch strings.ToLower(strings.TrimSpace(requestType)) {
	case "http", "http-request":
		return "http"
	case "graphql", "graphql-request":
		return "graphql"
	case "grpc", "grpc-request":
		return "grpc"
	case "ws", "websocket", "ws-request", "websocket-request":
		return "websocket"
	default:
		return ""
	}
}

func brunoPresetRequestType(requestType string) string {
	switch normalizePresetRequestType(requestType) {
	case "websocket":
		return "ws"
	case "graphql":
		return "graphql"
	case "grpc":
		return "grpc"
	default:
		return "http"
	}
}

func hasCollectionPresets(presets CollectionPresets) bool {
	presets = normalizeCollectionPresets(presets)
	return presets.RequestURL != "" || (presets.RequestType != "" && presets.RequestType != "http")
}

func normalizeCollectionProtobuf(collectionPath string, protobuf CollectionProtobufConfig) CollectionProtobufConfig {
	result := CollectionProtobufConfig{
		ProtoFiles:  make([]CollectionProtoFile, 0, len(protobuf.ProtoFiles)),
		ImportPaths: make([]CollectionProtoImportPath, 0, len(protobuf.ImportPaths)),
	}
	seenFiles := map[string]bool{}
	for _, protoFile := range protobuf.ProtoFiles {
		protoFile.Path = strings.TrimSpace(protoFile.Path)
		protoFile.Type = strings.ToLower(strings.TrimSpace(protoFile.Type))
		if protoFile.Type == "" {
			protoFile.Type = "file"
		}
		if protoFile.Path == "" {
			continue
		}
		key := protoFile.Type + "\x00" + protoFile.Path
		if seenFiles[key] {
			continue
		}
		seenFiles[key] = true
		protoFile.Exists = collectionProtobufPathExists(collectionPath, protoFile.Path, false)
		result.ProtoFiles = append(result.ProtoFiles, protoFile)
	}
	seenImportPaths := map[string]bool{}
	for _, importPath := range protobuf.ImportPaths {
		importPath.Path = strings.TrimSpace(importPath.Path)
		if importPath.Path == "" {
			continue
		}
		if seenImportPaths[importPath.Path] {
			continue
		}
		seenImportPaths[importPath.Path] = true
		importPath.Exists = collectionProtobufPathExists(collectionPath, importPath.Path, true)
		result.ImportPaths = append(result.ImportPaths, importPath)
	}
	return result
}

func hasCollectionProtobuf(protobuf CollectionProtobufConfig) bool {
	protobuf = normalizeCollectionProtobuf("", protobuf)
	return len(protobuf.ProtoFiles) > 0 || len(protobuf.ImportPaths) > 0
}

func normalizeCollectionSecurityConfig(config CollectionSecurityConfig) CollectionSecurityConfig {
	config.JSSandboxMode = normalizeJSSandboxMode(config.JSSandboxMode)
	return config
}

func collectionJSSandboxMode(collection Collection) string {
	return normalizeJSSandboxMode(collection.SecurityConfig.JSSandboxMode)
}

func collectionProtobufPathExists(collectionPath, rawPath string, wantDir bool) bool {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return false
	}
	resolved := path
	if !filepath.IsAbs(resolved) && strings.TrimSpace(collectionPath) != "" {
		resolved = filepath.Join(collectionPath, resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	if wantDir {
		return info.IsDir()
	}
	return !info.IsDir()
}

func (t cookieCapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(req)
	if res != nil && t.jar != nil {
		sourceURL := ""
		if res.Request != nil && res.Request.URL != nil {
			sourceURL = res.Request.URL.String()
		} else if req != nil && req.URL != nil {
			sourceURL = req.URL.String()
		}
		t.jar.UpsertAll(cookiejar.FromResponse(res, sourceURL))
	}
	return res, err
}

func attachCookiesToHTTPRequest(req *http.Request, cookies []CookieEntry) {
	if req == nil || req.URL == nil {
		return
	}
	matching := cookiejar.ForURL(cookies, req.URL.String())
	if len(matching) == 0 {
		return
	}
	req.Header.Set("Cookie", cookiejar.MergeHeader(req.Header.Get("Cookie"), cookiejar.Header(matching)))
}

func (a *App) effectiveRequestContextForExecution(collectionID, itemID, environmentID string) (RequestItem, Collection, map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	vars := scripting.BuildVariableMap(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, ws.Path)
	return requestCopy, collectionCopy, vars, nil
}

// gRPC method resolution, messages, metadata and grpcurl moved to internal/grpcexec.

// randomHex, wssePasswordDigest and quoteDigestValue moved to
// internal/auth/wsse with the header they build. Wrapped because package main
// still uses them for HTTP digest auth.

// WebSocket message preparation moved to internal/wsexec.
//
// normalizeWSMessageType is wrapped rather than renamed at seven call sites.

// Dialling stays in package main:
// it reads the app's TLS settings, http client and per-collection certificates.

func buildBody(body RequestBody, vars map[string]string, basePath ...string) (io.Reader, string, error) {
	switch body.Mode {
	case "", "none":
		return nil, "", nil
	case "json":
		return strings.NewReader(interpolate(body.JSON, vars)), "application/json", nil
	case "xml":
		return strings.NewReader(interpolate(body.XML, vars)), "application/xml", nil
	case "graphql":
		return strings.NewReader(codegen.GraphQLRequestBodySnapshot(body, vars)), "application/json", nil
	case "text", "sparql":
		return strings.NewReader(interpolate(body.Text, vars)), "text/plain", nil
	case "formUrlEncoded":
		values := url.Values{}
		for _, field := range body.FormURLEncoded {
			if field.Enabled {
				values.Add(interpolate(field.Name, vars), interpolate(field.Value, vars))
			}
		}
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
	case "multipartForm":
		var builder strings.Builder
		writer := multipart.NewWriter(&builder)
		for _, part := range body.Multipart {
			if !part.Enabled {
				continue
			}
			partName := interpolate(part.Name, vars)
			contentType := strings.TrimSpace(interpolate(part.ContentType, vars))
			if part.FilePath != "" {
				filePath := resolveBodyFilePath(interpolate(part.FilePath, vars), basePath...)
				header := textproto.MIMEHeader{}
				header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": partName, "filename": filepath.Base(filePath)}))
				if contentType != "" {
					header.Set("Content-Type", contentType)
				} else {
					header.Set("Content-Type", "application/octet-stream")
				}
				w, err := writer.CreatePart(header)
				if err != nil {
					return nil, "", err
				}
				file, err := os.Open(filePath)
				if err != nil {
					return nil, "", err
				}
				_, copyErr := io.Copy(w, file)
				closeErr := file.Close()
				if copyErr != nil {
					return nil, "", copyErr
				}
				if closeErr != nil {
					return nil, "", closeErr
				}
				continue
			}
			partValue := interpolate(part.Value, vars)
			if contentType == "" {
				if err := writer.WriteField(partName, partValue); err != nil {
					return nil, "", err
				}
			} else {
				header := textproto.MIMEHeader{}
				header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": partName}))
				header.Set("Content-Type", contentType)
				w, err := writer.CreatePart(header)
				if err != nil {
					return nil, "", err
				}
				if _, err := io.WriteString(w, partValue); err != nil {
					return nil, "", err
				}
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", err
		}
		return strings.NewReader(builder.String()), writer.FormDataContentType(), nil
	case "file":
		selected, ok := selectedFileBodyEntry(body)
		contentType := strings.TrimSpace(interpolate(selected.ContentType, vars))
		if !ok || strings.TrimSpace(selected.FilePath) == "" {
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			return nil, contentType, nil
		}
		filePath := resolveBodyFilePath(interpolate(selected.FilePath, vars), basePath...)
		if contentType == "" {
			contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		file, err := os.Open(filePath)
		return file, contentType, err
	default:
		return strings.NewReader(interpolate(body.Text, vars)), "text/plain", nil
	}
}

func resolveBodyFilePath(filePath string, basePath ...string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || filepath.IsAbs(filePath) || len(basePath) == 0 || strings.TrimSpace(basePath[0]) == "" {
		return filePath
	}
	return filepath.Join(basePath[0], filepath.FromSlash(filePath))
}

func (a *App) applyAuth(req *http.Request, collectionPath string, item *RequestItem, vars map[string]string, recordTimeline func(TimelineItem)) error {
	return applyAuthWithOAuth2Fetcher(req, item, vars, func(auth OAuth2Auth, vars map[string]string) (string, error) {
		token, timelineEntries, err := a.fetchOAuth2TokenWithTimeline(auth, vars)
		if recordTimeline != nil {
			for _, entry := range timelineEntries {
				entry.ID = newID("timeline")
				entry.Kind = "oauth2"
				entry.Source = "oauth2.0"
				entry.RequestID = item.ID
				entry.SourceFile = timelineSourceFileForItem(collectionPath, *item)
				if entry.Message == "" {
					statusLabel := entry.StatusText
					if entry.Status > 0 {
						statusLabel = strconv.Itoa(entry.Status)
					}
					entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
				}
				recordTimeline(entry)
			}
		}
		return token, err
	})
}

func applyAuth(req *http.Request, item *RequestItem, vars map[string]string) error {
	return applyAuthWithOAuth2Fetcher(req, item, vars, fetchOAuth2Token)
}

func applyAuthWithOAuth2Fetcher(req *http.Request, item *RequestItem, vars map[string]string, oauth2Fetcher func(OAuth2Auth, map[string]string) (string, error)) error {
	auth := item.Auth
	switch auth.Mode {
	case "basic":
		req.SetBasicAuth(interpolate(auth.Username, vars), interpolate(auth.Password, vars))
	case "bearer":
		token := interpolate(auth.Token, vars)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "apikey":
		key := interpolate(auth.APIKey, vars)
		value := interpolate(auth.APIValue, vars)
		if key == "" {
			return nil
		}
		if auth.APILocation == "query" {
			q := req.URL.Query()
			q.Set(key, value)
			req.URL.RawQuery = q.Encode()
			return nil
		}
		req.Header.Set(key, value)
	case "oauth2":
		token := interpolate(auth.Token, vars)
		if token == "" && strings.TrimSpace(auth.OAuth2.GrantType) != "" {
			var err error
			token, err = oauth2Fetcher(auth.OAuth2, vars)
			if err != nil {
				return err
			}
		}
		if token != "" {
			applyOAuth2Token(req, auth.OAuth2, token, vars)
		}
	case "ntlm":
		username := interpolate(auth.Username, vars)
		if domain := interpolate(auth.Domain, vars); domain != "" && !strings.Contains(username, `\`) {
			username = domain + `\` + username
		}
		req.SetBasicAuth(username, interpolate(auth.Password, vars))
	case "awsv4":
		return awsv4.Sign(req, auth.AWSV4, time.Now().UTC(), func(value string) string { return interpolate(value, vars) })
	case "wsse":
		wsse.ApplyHeader(req.Header, interpolate(auth.Username, vars), interpolate(auth.Password, vars), time.Now().UTC())
	case "oauth1":
		return oauth1.Sign(req, item, auth.OAuth1, vars, time.Now().UTC())
	}
	return nil
}

// OAuth 1.0a request signing moved to internal/auth/oauth1.
//
// setRequestBodyString stayed: it was declared inside that block but is generic,
// and internal/auth/awsv4 already keeps its own copy for the same reason -- it
// rewrites a body AND keeps GetBody consistent with it, which any signer that
// hashes the payload depends on.

func setRequestBodyString(req *http.Request, value string) {
	data := []byte(value)
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.ContentLength = int64(len(data))
}

// AWS SigV4 signing and credential resolution moved to internal/auth/awsv4.
//
// hmacSHA256Bytes stays here: OAuth1 signing uses it too, so it is generic
// crypto rather than part of the AWS surface.

// HTTP Digest authentication moved to internal/auth/digest.

func evaluateAssertions(assertions []Assertion, response Response) []Assertion {
	next := make([]Assertion, 0, len(assertions))
	for _, assertion := range assertions {
		if !assertion.Enabled {
			next = append(next, assertion)
			continue
		}
		actual := ""
		switch assertion.Expression {
		case "res.status":
			actual = strconv.Itoa(response.Status)
		case "res.body":
			actual = response.Body
		default:
			if strings.HasPrefix(assertion.Expression, "res.headers.") {
				actual = response.Headers[strings.TrimPrefix(assertion.Expression, "res.headers.")]
			}
		}
		assertion.Passed = compareAssertion(actual, assertion.Operator, assertion.Value)
		if assertion.Passed {
			assertion.Message = "passed"
		} else {
			assertion.Message = fmt.Sprintf("expected %q %s %q", actual, assertion.Operator, assertion.Value)
		}
		next = append(next, assertion)
	}
	return next
}

// The scripting runtime moved to internal/scripting.

// Wrapped rather than renamed at 138 call sites in app.go alone.
func interpolate(input string, vars map[string]string) string {
	return interp.Interpolate(input, vars)
}

func defaultState(dir string) AppState {
	now := time.Now()
	wsID := newID("workspace")
	collID := newID("collection")
	req := types.NewRequestItem("Echo JSON", "http", 1)
	req.Method = http.MethodPost
	req.URL = "{{host}}/post"
	req.Body.Mode = "json"
	req.Body.JSON = "{\n  \"hello\": \"{{name}}\"\n}"
	req.Headers = []KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}}
	req.Assertions = []Assertion{{Expression: "res.status", Operator: "equals", Value: "200", Enabled: true}}
	req.Tests = "expect status equals 200"
	req.Vars.Req = []Variable{{ID: newID("var"), Name: "name", Value: "Bruno", DataType: "string", Enabled: true}}
	collection := Collection{
		ID:             collID,
		Name:           "Sample API",
		Version:        "1",
		Path:           filepath.Join(dir, "My Workspace", "Sample API"),
		Format:         "yml",
		Items:          []RequestItem{req},
		Variables:      []Variable{{ID: newID("var"), Name: "host", Value: "https://httpbin.org", DataType: "string", Enabled: true}},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		Environments: []Environment{{
			ID:    newID("env"),
			Name:  "Development",
			Color: "#2f8cff",
			Variables: []Variable{
				{ID: newID("var"), Name: "host", Value: "https://httpbin.org", DataType: "string", Enabled: true},
				{ID: newID("var"), Name: "token", Value: "secret-token", DataType: "string", Enabled: true, Secret: true},
			},
		}},
		Auth:      AuthConfig{Mode: "none"},
		Docs:      "# Sample API\nUse this collection to test request execution and variable interpolation.\n",
		CreatedAt: now,
		UpdatedAt: now,
	}
	tabID := newID("tab")
	return AppState{
		Workspaces: []Workspace{{
			ID:          wsID,
			Name:        "My Workspace",
			Path:        filepath.Join(dir, "My Workspace"),
			Collections: []Collection{collection},
			Docs:        "# My Workspace\n",
			CreatedAt:   now,
			UpdatedAt:   now,
		}},
		ActiveWorkspaceID: wsID,
		OpenTabs: []OpenTab{{
			ID:             tabID,
			CollectionID:   collID,
			ItemID:         req.ID,
			Kind:           "request",
			RequestPaneTab: "params",
			ResponseTab:    "response",
		}},
		ActiveTabID:   tabID,
		FeatureLedger: bru.DefaultFeatures(),
		Preferences: Preferences{
			Theme:                  "system",
			ThemeVariantLight:      "light",
			ThemeVariantDark:       "dark",
			KeybindingsEnabled:     boolPtr(true),
			Layout:                 normalizeLayoutPreferences(LayoutPreferences{}),
			Display:                normalizeDisplayPreferences(DisplayPreferences{}),
			Font:                   normalizeFontPreferences(FontPreferences{}, 13),
			Request:                normalizeRequestPreferences(RequestPreferences{}, true),
			General:                normalizeGeneralPreferences(GeneralPreferences{}, ""),
			AutoSave:               normalizeAutoSavePreferences(AutoSavePreferences{}, false),
			Cache:                  normalizeCachePreferences(CachePreferences{}),
			DevTools:               normalizeDevToolsPreferences(DevToolsPreferences{}),
			Autosave:               false,
			CodeFontSize:           13,
			StoreCookies:           true,
			OAuth2UseSystemBrowser: false,
			ProxyMode:              "system",
			Proxy:                  defaultProxyPreferences(),
		},
	}
}

func applyPatch(item *RequestItem, patch RequestPatch) {
	if patch.Name != nil {
		item.Name = *patch.Name
	}
	if patch.Type != nil {
		item.Type = *patch.Type
	}
	if patch.Method != nil {
		item.Method = *patch.Method
	}
	if patch.URL != nil {
		item.URL = *patch.URL
	}
	if patch.Params != nil {
		item.Params = *patch.Params
	}
	if patch.PathParams != nil {
		item.PathParams = *patch.PathParams
	}
	if patch.Headers != nil {
		item.Headers = *patch.Headers
	}
	if patch.Body != nil {
		item.Body = *patch.Body
	}
	if patch.ProtoPath != nil {
		item.ProtoPath = *patch.ProtoPath
	}
	if patch.GrpcMethodType != nil {
		item.GrpcMethodType = *patch.GrpcMethodType
	}
	if patch.GrpcMessages != nil {
		item.GrpcMessages = *patch.GrpcMessages
	}
	if patch.WSMessages != nil {
		item.WSMessages = *patch.WSMessages
	}
	if patch.Auth != nil {
		item.Auth = *patch.Auth
	}
	if patch.Vars != nil {
		item.Vars = *patch.Vars
	}
	if patch.Assertions != nil {
		item.Assertions = *patch.Assertions
	}
	if patch.Tests != nil {
		item.Tests = *patch.Tests
	}
	if patch.PreScript != nil {
		item.PreScript = *patch.PreScript
	}
	if patch.PostScript != nil {
		item.PostScript = *patch.PostScript
	}
	if patch.Docs != nil {
		item.Docs = *patch.Docs
	}
	if patch.Settings != nil {
		item.Settings = *patch.Settings
	}
	if patch.Tags != nil {
		item.Tags = *patch.Tags
	}
}

func findFolderConfig(collection *Collection, folderPath string) (*FolderConfig, error) {
	index, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return nil, err
	}
	return &collection.Folders[index], nil
}

func findFolderConfigIndex(collection *Collection, folderPath string) (int, error) {
	normalized := normalizeFolderPathKey(folderPath)
	if normalized == "" {
		return -1, errors.New("folder path is required")
	}
	for i := range collection.Folders {
		folder := &collection.Folders[i]
		if normalizeFolderPathKey(folder.Path) == normalized || normalizeFolderPathKey(folder.DisplayPath) == normalized {
			return i, nil
		}
	}
	return -1, fmt.Errorf("folder %s not found", folderPath)
}

func (a *App) ensureCollectionDirectoryForWriteLocked(collection *Collection) error {
	collectionPath := strings.TrimSpace(collection.Path)
	if collectionPath == "" {
		return errors.New("collection path is empty")
	}
	if info, err := os.Stat(collectionPath); errors.Is(err, os.ErrNotExist) {
		return a.writeCollectionFilesLocked(collection)
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", collectionPath)
	}
	return nil
}

func collectionFolderParentPaths(collection *Collection, parentFolderPath string) (string, string, error) {
	parentFolderPath = normalizeFolderPathKey(parentFolderPath)
	if parentFolderPath == "" {
		return "", "", nil
	}
	parent, err := findFolderConfig(collection, parentFolderPath)
	if err != nil {
		return "", "", err
	}
	parentPath := normalizeFolderPathKey(parent.Path)
	parentDisplayPath := normalizeFolderPathKey(firstNonEmpty(parent.DisplayPath, parent.Name, parent.Path))
	return parentPath, parentDisplayPath, nil
}

func joinCollectionFolderPath(parent, child string) string {
	parent = normalizeFolderPathKey(parent)
	child = strings.Trim(child, "/")
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return parent + "/" + child
}

func collectionHasChildFolder(collection *Collection, parentPath, directoryName string) bool {
	parentPath = normalizeFolderPathKey(parentPath)
	directoryName = strings.TrimSpace(directoryName)
	for _, folder := range collection.Folders {
		folderPath := normalizeFolderPathKey(folder.Path)
		if normalizeFolderPathKey(parentFolderDisplayPath(folderPath)) != parentPath {
			continue
		}
		if strings.TrimSpace(filepath.Base(filepath.FromSlash(folderPath))) == directoryName {
			return true
		}
	}
	return false
}

func nextCollectionFolderSeq(collection Collection, parentPath string) int {
	parentPath = normalizeFolderPathKey(parentPath)
	count := 0
	for _, folder := range collection.Folders {
		if normalizeFolderPathKey(parentFolderDisplayPath(folder.Path)) == parentPath {
			count++
		}
	}
	for _, item := range collection.Items {
		if normalizeFolderPathKey(item.FolderPath) == parentPath {
			count++
		}
	}
	return count + 1
}

type collectionSibling struct {
	kind        string
	folderIndex int
	itemIndex   int
	name        string
	seq         int
}

func (a *App) resequenceCollectionSiblingsLocked(collection *Collection, parentPath, parentDisplayPath string) error {
	parentPath = normalizeFolderPathKey(parentPath)
	parentDisplayPath = normalizeFolderPathKey(parentDisplayPath)
	siblings := []collectionSibling{}
	for i := range collection.Folders {
		folder := collection.Folders[i]
		folderParentPath := normalizeFolderPathKey(parentFolderDisplayPath(folder.Path))
		folderParentDisplayPath := normalizeFolderPathKey(parentFolderDisplayPath(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path)))
		if folderParentPath != parentPath && folderParentDisplayPath != parentDisplayPath {
			continue
		}
		siblings = append(siblings, collectionSibling{
			kind:        "folder",
			folderIndex: i,
			name:        firstNonEmpty(folder.Name, pathBaseSlash(folder.DisplayPath), pathBaseSlash(folder.Path)),
			seq:         folder.Seq,
		})
	}
	for i := range collection.Items {
		item := collection.Items[i]
		if normalizeFolderPathKey(item.FolderPath) != parentDisplayPath {
			continue
		}
		siblings = append(siblings, collectionSibling{
			kind:      "request",
			itemIndex: i,
			name:      item.Name,
			seq:       item.Seq,
		})
	}
	if len(siblings) == 0 {
		return nil
	}
	ordered := sortSequencedSiblingsLikeBruno(siblings)
	requestSeqChanged := false
	for index, entry := range ordered {
		nextSeq := index + 1
		switch entry.kind {
		case "folder":
			if collection.Folders[entry.folderIndex].Seq == nextSeq {
				continue
			}
			collection.Folders[entry.folderIndex].Seq = nextSeq
			if err := a.writeFolderConfigLocked(collection, collection.Folders[entry.folderIndex]); err != nil {
				return err
			}
		case "request":
			if collection.Items[entry.itemIndex].Seq == nextSeq {
				continue
			}
			collection.Items[entry.itemIndex].Seq = nextSeq
			collection.Items[entry.itemIndex].UpdatedAt = time.Now()
			requestSeqChanged = true
		}
	}
	sortFoldersLikeBruno(collection.Folders)
	if requestSeqChanged {
		return a.writeCollectionFilesLocked(collection)
	}
	return nil
}

func sortSequencedSiblingsLikeBruno(siblings []collectionSibling) []collectionSibling {
	alphabetical := append([]collectionSibling(nil), siblings...)
	sort.SliceStable(alphabetical, func(i, j int) bool {
		return strings.ToLower(alphabetical[i].name) < strings.ToLower(alphabetical[j].name)
	})
	withoutSeq := []collectionSibling{}
	withSeq := []collectionSibling{}
	for _, entry := range alphabetical {
		if entry.seq > 0 {
			withSeq = append(withSeq, entry)
			continue
		}
		withoutSeq = append(withoutSeq, entry)
	}
	sort.SliceStable(withSeq, func(i, j int) bool {
		return withSeq[i].seq < withSeq[j].seq
	})
	ordered := withoutSeq
	for _, entry := range withSeq {
		position := entry.seq - 1
		if position < 0 {
			position = 0
		}
		if position >= len(ordered) {
			ordered = append(ordered, entry)
			continue
		}
		ordered = append(ordered[:position+1], ordered[position:]...)
		ordered[position] = entry
	}
	return ordered
}

func updateCollectionFolderRenameState(collection *Collection, oldPath, newPath, oldDisplayPath, newDisplayPath, oldDir, newDir string) {
	oldPath = normalizeFolderPathKey(oldPath)
	newPath = normalizeFolderPathKey(newPath)
	oldDisplayPath = normalizeFolderPathKey(oldDisplayPath)
	newDisplayPath = normalizeFolderPathKey(newDisplayPath)
	for i := range collection.Folders {
		folder := &collection.Folders[i]
		folder.Path = replaceFolderPathPrefix(folder.Path, oldPath, newPath)
		folder.DisplayPath = replaceFolderPathPrefix(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path), oldDisplayPath, newDisplayPath)
		if normalizeFolderPathKey(folder.Path) == newPath {
			folder.Name = pathBaseSlash(newDisplayPath)
		}
	}
	for i := range collection.Items {
		item := &collection.Items[i]
		item.FolderPath = replaceFolderPathPrefix(item.FolderPath, oldDisplayPath, newDisplayPath)
		if pathInside(oldDir, item.FilePath) {
			rel, err := filepath.Rel(oldDir, item.FilePath)
			if err == nil {
				item.FilePath = filepath.Clean(filepath.Join(newDir, rel))
			}
		}
	}
	sortFoldersLikeBruno(collection.Folders)
}

func replaceFolderPathPrefix(value, oldPrefix, newPrefix string) string {
	value = normalizeFolderPathKey(value)
	oldPrefix = normalizeFolderPathKey(oldPrefix)
	newPrefix = normalizeFolderPathKey(newPrefix)
	if oldPrefix == "" {
		return value
	}
	if value == oldPrefix {
		return newPrefix
	}
	if strings.HasPrefix(value, oldPrefix+"/") {
		return joinCollectionFolderPath(newPrefix, strings.TrimPrefix(value, oldPrefix+"/"))
	}
	return value
}

func folderPathHasPrefix(value, prefix string) bool {
	value = normalizeFolderPathKey(value)
	prefix = normalizeFolderPathKey(prefix)
	if value == "" || prefix == "" {
		return false
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func pathBaseSlash(value string) string {
	value = normalizeFolderPathKey(value)
	if value == "" {
		return ""
	}
	return filepath.Base(filepath.FromSlash(value))
}

func normalizeFolderPathKey(value string) string {
	value = strings.TrimSpace(filepath.ToSlash(value))
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return value
	}
	return cleaned
}

func mergeFolderSettingsUpdate(folder *FolderConfig, updated FolderConfig) {
	name := strings.TrimSpace(updated.Name)
	if name == "" {
		name = folder.Name
	}
	if name == "" {
		name = filepath.Base(filepath.FromSlash(folder.Path))
	}
	folder.Name = name
	folder.Headers = updated.Headers
	folder.Variables = updated.Variables
	folder.ResVariables = updated.ResVariables
	folder.Auth = updated.Auth
	folder.PreScript = updated.PreScript
	folder.PostScript = updated.PostScript
	folder.Tests = updated.Tests
	folder.Docs = updated.Docs
}

func (a *App) dotEnvContextLocked(workspaceID, collectionID string) (*Workspace, *Collection, error) {
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if collectionID == "" {
		if len(ws.Collections) == 0 {
			return ws, nil, nil
		}
		return ws, &ws.Collections[0], nil
	}
	for ci := range ws.Collections {
		if ws.Collections[ci].ID == collectionID {
			return ws, &ws.Collections[ci], nil
		}
	}
	return nil, nil, fmt.Errorf("collection %s not found in workspace %s", collectionID, ws.ID)
}

func (a *App) refreshGitCollectionAvailabilityLocked() {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			if collection.Scratch {
				continue
			}
			if strings.TrimSpace(collection.Remote) == "" || strings.TrimSpace(collection.Path) == "" {
				collection.NotFoundLocally = false
				continue
			}
			if info, err := os.Stat(collection.Path); err == nil && info.IsDir() {
				collection.NotFoundLocally = false
				continue
			}
			collection.NotFoundLocally = true
			collection.Items = nil
			collection.Folders = nil
			a.removeOpenTabsForCollectionLocked(collection.ID)
		}
	}
}

func (a *App) removeOpenTabsForCollectionLocked(collectionID string) {
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID != collectionID {
			next = append(next, tab)
		}
	}
	a.state.OpenTabs = next
	a.removeClosedTabsForCollectionLocked(collectionID)
	if len(next) == 0 {
		a.state.ActiveTabID = ""
		return
	}
	found := false
	for _, tab := range next {
		if tab.ID == a.state.ActiveTabID {
			found = true
			break
		}
	}
	if !found {
		a.state.ActiveTabID = next[0].ID
	}
}

func (a *App) removeOpenTabsForRequestIDsLocked(collectionID string, requestIDs map[string]bool) {
	if len(requestIDs) == 0 {
		return
	}
	removedActive := false
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] && tab.Kind != "response-example" {
			if tab.ID == a.state.ActiveTabID {
				removedActive = true
			}
			continue
		}
		next = append(next, tab)
	}
	a.state.OpenTabs = next
	a.removeClosedTabsForRequestIDsLocked(collectionID, requestIDs)
	if !removedActive {
		return
	}
	a.state.ActiveTabID = ""
	if len(a.state.OpenTabs) > 0 {
		a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
	}
}

func (a *App) removeOpenTabsForDeletedRequestIDsLocked(collectionID string, requestIDs map[string]bool) {
	if len(requestIDs) == 0 {
		return
	}
	removedActive := false
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] {
			if tab.ID == a.state.ActiveTabID {
				removedActive = true
			}
			continue
		}
		next = append(next, tab)
	}
	a.state.OpenTabs = next
	nextClosed := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] {
			continue
		}
		nextClosed = append(nextClosed, tab)
	}
	a.state.ClosedTabs = nextClosed
	if !removedActive {
		return
	}
	a.state.ActiveTabID = ""
	if len(a.state.OpenTabs) > 0 {
		a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
	}
}

func (a *App) removeClosedTabsForCollectionLocked(collectionID string) {
	next := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if tab.CollectionID != collectionID {
			next = append(next, tab)
		}
	}
	a.state.ClosedTabs = next
}

func (a *App) removeClosedTabsForRequestIDsLocked(collectionID string, requestIDs map[string]bool) {
	next := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] && tab.Kind != "response-example" {
			continue
		}
		next = append(next, tab)
	}
	a.state.ClosedTabs = next
}

func (a *App) findItemLocked(collectionID, itemID string) (*RequestItem, error) {
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return nil, err
	}
	return findItem(collection, itemID)
}

func findItemIndex(collection *Collection, itemID string) (int, error) {
	for i := range collection.Items {
		if collection.Items[i].ID == itemID {
			return i, nil
		}
	}
	return -1, fmt.Errorf("request %s not found", itemID)
}

func findItem(collection *Collection, itemID string) (*RequestItem, error) {
	index, err := findItemIndex(collection, itemID)
	if err != nil {
		return nil, err
	}
	return &collection.Items[index], nil
}

func findRunRequestItem(collection *Collection, targetRef string) (*RequestItem, error) {
	target := normalizeRunRequestRef(targetRef)
	if target == "" {
		return nil, errors.New("bru.runRequest requires a request path or name")
	}
	for i := range collection.Items {
		item := &collection.Items[i]
		for _, candidate := range runRequestItemRefs(*collection, *item) {
			if candidate == target {
				return item, nil
			}
		}
	}
	return nil, fmt.Errorf("bru.runRequest: invalid request path - %s", targetRef)
}

func runRequestItemRefs(collection Collection, item RequestItem) []string {
	refs := []string{item.ID, item.Name}
	if item.FolderPath != "" && item.Name != "" {
		refs = append(refs, filepath.ToSlash(filepath.Join(item.FolderPath, item.Name)))
	}
	if item.FilePath != "" {
		refs = append(refs, item.FilePath)
		if pathInside(collection.Path, item.FilePath) {
			if rel, err := filepath.Rel(collection.Path, item.FilePath); err == nil {
				refs = append(refs, rel)
			}
		}
	}
	normalized := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = normalizeRunRequestRef(ref)
		for _, candidate := range runRequestRefVariants(ref) {
			if candidate != "" && !seen[candidate] {
				seen[candidate] = true
				normalized = append(normalized, candidate)
			}
		}
	}
	return normalized
}

func normalizeRunRequestRef(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, "/")
	return value
}

func runRequestRefVariants(value string) []string {
	value = normalizeRunRequestRef(value)
	if value == "" {
		return nil
	}
	variants := []string{value}
	ext := strings.ToLower(filepath.Ext(value))
	if ext == ".bru" || ext == ".yml" || ext == ".yaml" {
		variants = append(variants, strings.TrimSuffix(value, filepath.Ext(value)))
	} else {
		variants = append(variants, value+".bru", value+".yml", value+".yaml")
	}
	return variants
}

func findItemInState(state AppState, collectionID, itemID string) (RequestItem, bool) {
	for _, ws := range state.Workspaces {
		for _, collection := range ws.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.ID == itemID {
					return item, true
				}
			}
		}
	}
	return RequestItem{}, false
}

func validRequestPaneTab(value string) bool {
	switch value {
	case "params", "body", "headers", "auth", "vars", "script", "assert", "tests", "docs", "app", "settings":
		return true
	default:
		return false
	}
}

func validResponsePaneTab(value string) bool {
	switch value {
	case "response", "headers", "metadata", "trailers", "timeline", "console", "tests", "examples":
		return true
	default:
		return false
	}
}

func (a *App) openTabLocked(collectionID, itemID, kind string) {
	tabID := collectionID + ":" + itemID
	transient := a.isTransientRequestLocked(collectionID, itemID)
	for i := range a.state.OpenTabs {
		if a.state.OpenTabs[i].ID == tabID {
			a.state.OpenTabs[i].Transient = transient
			a.state.ActiveTabID = tabID
			return
		}
	}
	a.state.OpenTabs = append(a.state.OpenTabs, OpenTab{
		ID:             tabID,
		CollectionID:   collectionID,
		ItemID:         itemID,
		Kind:           kind,
		RequestPaneTab: "params",
		ResponseTab:    "response",
		Transient:      transient,
	})
	a.state.ActiveTabID = tabID
}

func (a *App) isTransientRequestLocked(collectionID, itemID string) bool {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			if collection.ID != collectionID {
				continue
			}
			// Tab transience is a persistence concern and belongs only to the
			// ephemeral Scratch collection. A never-saved normal request uses the
			// item Transient flag for discard/export behavior, but its tab and
			// draft must survive a crash/restart until the user resolves it.
			return collection.Scratch
		}
	}
	return false
}

func responseExampleTabID(collectionID, itemID, exampleID string) string {
	return collectionID + ":" + itemID + ":example:" + exampleID
}

func (a *App) openResponseExampleTabLocked(collectionID, itemID string, example ResponseExample) {
	tabID := responseExampleTabID(collectionID, itemID, example.ID)
	for i := range a.state.OpenTabs {
		if a.state.OpenTabs[i].ID == tabID {
			a.state.OpenTabs[i].CollectionID = collectionID
			a.state.OpenTabs[i].ItemID = itemID
			a.state.OpenTabs[i].Kind = "response-example"
			a.state.OpenTabs[i].ExampleID = example.ID
			a.state.OpenTabs[i].ExampleName = example.Name
			a.state.OpenTabs[i].ResponseTab = "examples"
			if a.state.OpenTabs[i].RequestPaneTab == "" {
				a.state.OpenTabs[i].RequestPaneTab = "params"
			}
			a.state.ActiveTabID = tabID
			return
		}
	}
	a.state.OpenTabs = append(a.state.OpenTabs, OpenTab{
		ID:             tabID,
		CollectionID:   collectionID,
		ItemID:         itemID,
		Kind:           "response-example",
		ExampleID:      example.ID,
		ExampleName:    example.Name,
		RequestPaneTab: "params",
		ResponseTab:    "examples",
	})
	a.state.ActiveTabID = tabID
}

func (a *App) rememberClosedTabLocked(tab OpenTab) {
	if tab.ID == "" || tab.Transient {
		return
	}
	next := a.state.ClosedTabs[:0]
	for _, existing := range a.state.ClosedTabs {
		if existing.ID != tab.ID {
			next = append(next, existing)
		}
	}
	next = append(next, tab)
	if len(next) > closedTabHistoryLimit {
		next = next[len(next)-closedTabHistoryLimit:]
	}
	a.state.ClosedTabs = next
}

func (a *App) lastClosedTabIndexLocked(collectionID string) int {
	for index := len(a.state.ClosedTabs) - 1; index >= 0; index-- {
		if collectionID == "" || a.state.ClosedTabs[index].CollectionID == collectionID {
			return index
		}
	}
	return -1
}

func (a *App) openTabIsRestorableLocked(tab OpenTab) bool {
	if tab.ID == "" || tab.CollectionID == "" || tab.ItemID == "" || tab.Transient {
		return false
	}
	collection, err := a.findCollectionLocked(tab.CollectionID)
	if err != nil {
		return false
	}
	item, err := findItem(collection, tab.ItemID)
	if err != nil {
		return false
	}
	if tab.Kind == "response-example" {
		_, _, err := findResponseExample(item, firstNonEmpty(tab.ExampleID, tab.ExampleName))
		return err == nil
	}
	return true
}

func (a *App) syncResponseExampleTabLocked(collectionID, itemID string, example ResponseExample) {
	tabID := responseExampleTabID(collectionID, itemID, example.ID)
	for i := range a.state.OpenTabs {
		tab := &a.state.OpenTabs[i]
		isSameExampleTab := tab.Kind == "response-example" && tab.CollectionID == collectionID && tab.ItemID == itemID && tab.ExampleID == example.ID
		if tab.ID != tabID && !isSameExampleTab {
			continue
		}
		tab.ID = tabID
		tab.CollectionID = collectionID
		tab.ItemID = itemID
		tab.Kind = "response-example"
		tab.ExampleID = example.ID
		tab.ExampleName = example.Name
		tab.ResponseTab = "examples"
		if tab.RequestPaneTab == "" {
			tab.RequestPaneTab = "params"
		}
	}
}

func (a *App) closeResponseExampleTabLocked(collectionID, itemID, exampleID string) {
	tabID := responseExampleTabID(collectionID, itemID, exampleID)
	removedActive := false
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		matches := tab.ID == tabID || (tab.Kind == "response-example" && tab.CollectionID == collectionID && tab.ItemID == itemID && tab.ExampleID == exampleID)
		if matches {
			if tab.ID == a.state.ActiveTabID {
				removedActive = true
			}
			continue
		}
		next = append(next, tab)
	}
	a.state.OpenTabs = next
	if !removedActive {
		return
	}
	a.state.ActiveTabID = ""
	if len(a.state.OpenTabs) > 0 {
		a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
	}
}

func (a *App) openFirstCollectionItemLocked(collection Collection) {
	if len(collection.Items) == 0 {
		return
	}
	a.openTabLocked(collection.ID, collection.Items[0].ID, "request")
}

func (a *App) seedCollectionWatchFingerprintLocked(collectionPath string) {
	collectionPath = strings.TrimSpace(collectionPath)
	if collectionPath == "" {
		return
	}
	if a.collectionWatchFingerprints == nil {
		a.collectionWatchFingerprints = map[string]string{}
	}
	collectionPath = filepath.Clean(collectionPath)
	fingerprint, err := collectionWatchFingerprint(collectionPath)
	if err != nil {
		return
	}
	a.collectionWatchFingerprints[collectionPath] = fingerprint
}

func (a *App) clearCollectionWatchFingerprintLocked(collectionPath string) {
	if a.collectionWatchFingerprints == nil {
		return
	}
	collectionPath = strings.TrimSpace(collectionPath)
	if collectionPath == "" {
		return
	}
	delete(a.collectionWatchFingerprints, filepath.Clean(collectionPath))
}

func collectionWatchCandidate(collection Collection) bool {
	return !collection.Scratch && !collection.NotFoundLocally && strings.TrimSpace(collection.Path) != ""
}

func collectionHasDraftRequests(collection Collection) bool {
	for _, item := range collection.Items {
		if item.Draft {
			return true
		}
	}
	return false
}

func preserveCollectionRuntimeState(previous Collection, refreshed *Collection) {
	if refreshed == nil {
		return
	}
	refreshed.SecurityConfig = normalizeCollectionSecurityConfig(previous.SecurityConfig)
	previousItems := map[string]RequestItem{}
	for _, item := range previous.Items {
		previousItems[item.ID] = item
	}
	for i := range refreshed.Items {
		if previousItem, ok := previousItems[refreshed.Items[i].ID]; ok {
			refreshed.Items[i].Response = previousItem.Response
			refreshed.Items[i].Timeline = previousItem.Timeline
		}
	}
}

func missingRequestIDs(previous, refreshed []RequestItem) map[string]bool {
	nextIDs := map[string]bool{}
	for _, item := range refreshed {
		nextIDs[item.ID] = true
	}
	missing := map[string]bool{}
	for _, item := range previous {
		if !nextIDs[item.ID] {
			missing[item.ID] = true
		}
	}
	return missing
}

func (a *App) notify(level, message string) {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		level = "info"
	}
	message = strings.TrimSpace(message)
	a.state.Notifications = append([]Notification{{
		ID:          newID("notification"),
		Level:       level,
		Type:        notificationType(level),
		Title:       notificationTitle(level, message),
		Message:     message,
		Description: message,
		Color:       notificationColor(level),
		At:          time.Now(),
	}}, a.state.Notifications...)
	if len(a.state.Notifications) > 20 {
		a.state.Notifications = a.state.Notifications[:20]
	}
}

func notificationTitle(level, message string) string {
	message = strings.TrimSpace(message)
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success":
		if message != "" {
			return message
		}
		return "Success"
	case "warning", "warn":
		if message != "" {
			return message
		}
		return "Warning"
	case "error", "danger":
		if message != "" {
			return message
		}
		return "Error"
	default:
		if message != "" {
			return message
		}
		return "Notification"
	}
}

func notificationType(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success":
		return "Success"
	case "warning", "warn":
		return "Warning"
	case "error", "danger":
		return "Error"
	default:
		return "Info"
	}
}

func notificationColor(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success":
		return "#1f7a5a"
	case "warning", "warn":
		return "#9b6b16"
	case "error", "danger":
		return "#a33b3b"
	default:
		return "#2563eb"
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// OpenAPI spec sync and its merge rules moved to internal/openapisync.

// sanitizeFilename, deterministicID, normalizeWhitespace and appendScript moved
// to internal/scalar for the same reason as the four above: internal/importers
// needs them, and two copies of "what characters are legal in a filename" would
// be free to drift.

//
// mapValue, yamlScalarString, firstNonEmpty and newID moved to internal/scalar,
// which the importers need too. Wrapped rather than renamed at 400+ call sites.

// YAML request reading moved to internal/store/yamlstore.

// Bruno .bru parsing moved to internal/store/bru, along with the YAML body
// readers it shares with the YAML request reader still here.

// .bru serialisation and environment export moved to internal/store/bru.

// installPostmanVisualizer implements pm.visualizer.set (US-058).
//
// Recorded on the request state rather than applied to the response directly:
// the pre-request script runs before a response exists, and a visualizer set
// there must still reach the response that follows.
// The Postman script API installers moved to internal/scripting.

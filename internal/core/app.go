package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
	"github.com/mutexdev/lite_api/internal/auth/oauth1"
	"github.com/mutexdev/lite_api/internal/auth/wsse"
	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/localserver"
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
	xport "github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/gorilla/websocket"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"google.golang.org/grpc"
)

const networkLogBodyLimit = 64 * 1024
const terminalOutputLimit = 256 * 1024
const brunoOAuth2DefaultCallbackURL = "https://oauth.usebruno.com/callback"
const oauth2ProtocolCallbackHost = "app"
const oauth2ProtocolCallbackPath = "/oauth2/callback"

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
	transportCache         xport.Cache
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
	//
	// Everything that reads or writes these lives in app_persist.go —
	// markDirty, persistWriterLoop, stopPersistWriter, persistOnce. They are
	// declared here because they are App state, but the invariants above are
	// only enforceable by reading that file.
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
	historyStore *history.Store
	// US-072. Per-collection mock listeners, guarded by their own mutex so
	// binding a socket never happens under the state lock.
	mockOnce    sync.Once
	mockMu      sync.Mutex
	mockServers map[string]*localserver.MockServer
	// US-074. Docs preview listeners, same isolation as the mocks.
	docsOnce    sync.Once
	docsMu      sync.Mutex
	docsServers map[string]*localserver.DocsServer
	responses   *responsestore.Store

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
			File:           cloneFileBodyEntries(types.FileBodyEntriesOf(item.Body)),
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

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

// Request/response-example code generation moved to internal/codegen.

// Cookie storage rules moved to internal/cookiejar.

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
	collection.Proxy = xport.NormalizeProxyConfig(proxy)
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
	next := prefs.Normalize(preferences)
	if tlsSessionPreferencesChanged(a.state.Preferences, next) {
		a.tlsSessionCache = nil
		// The cache key already separates the old and new TLS postures, so
		// this is not what keeps them apart — it stops connections opened
		// under the previous trust settings from idling on in a pool the new
		// settings would never have authorised.
		a.transportCache.Flush()
	}
	if a.state.Preferences.Proxy != next.Proxy || a.state.Preferences.ProxyMode != next.ProxyMode {
		// Same reasoning for the proxy: the key already separates postures,
		// the flush retires sockets opened through the previous proxy.
		a.transportCache.Flush()
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
	a.transportCache.Flush()
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
// run (US-046). The filters mirror runner.DataRows' accepted extensions, which
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
	collection.ClientCertificates = xport.NormalizeClientCertificateRows(certs)
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
	collection.Presets = types.NormalizeCollectionPresets(presets)
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
	collection.Protobuf = types.NormalizeCollectionProtobuf(collection.Path, protobuf)
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

// TLS client certificates and proxy resolution moved to internal/transport.

func normalizeCollectionSecurityConfig(config CollectionSecurityConfig) CollectionSecurityConfig {
	config.JSSandboxMode = normalizeJSSandboxMode(config.JSSandboxMode)
	return config
}

func collectionJSSandboxMode(collection Collection) string {
	return normalizeJSSandboxMode(collection.SecurityConfig.JSSandboxMode)
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
			KeybindingsEnabled:     prefs.BoolPtr(true),
			Layout:                 prefs.NormalizeLayout(LayoutPreferences{}),
			Display:                prefs.NormalizeDisplay(DisplayPreferences{}),
			Font:                   prefs.NormalizeFont(FontPreferences{}, 13),
			Request:                prefs.NormalizeRequest(RequestPreferences{}, true),
			General:                prefs.NormalizeGeneral(GeneralPreferences{}, ""),
			AutoSave:               prefs.NormalizeAutoSave(AutoSavePreferences{}, false),
			Cache:                  prefs.NormalizeCache(CachePreferences{}),
			DevTools:               prefs.NormalizeDevTools(DevToolsPreferences{}),
			Autosave:               false,
			CodeFontSize:           13,
			StoreCookies:           true,
			OAuth2UseSystemBrowser: false,
			ProxyMode:              "system",
			Proxy:                  prefs.DefaultProxy(),
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

type collectionSibling struct {
	kind        string
	folderIndex int
	itemIndex   int
	name        string
	seq         int
}

func (a *App) resequenceCollectionSiblingsLocked(collection *Collection, parentPath, parentDisplayPath string) error {
	parentPath = types.NormalizeFolderPathKey(parentPath)
	parentDisplayPath = types.NormalizeFolderPathKey(parentDisplayPath)
	siblings := []collectionSibling{}
	for i := range collection.Folders {
		folder := collection.Folders[i]
		folderParentPath := types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(folder.Path))
		folderParentDisplayPath := types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path)))
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
		if types.NormalizeFolderPathKey(item.FolderPath) != parentDisplayPath {
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
	types.SortFoldersLikeBruno(collection.Folders)
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

func pathBaseSlash(value string) string {
	value = types.NormalizeFolderPathKey(value)
	if value == "" {
		return ""
	}
	return filepath.Base(filepath.FromSlash(value))
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

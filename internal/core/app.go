package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/localserver"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/responsestore"
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
	// notifiedChannels is the last message raised on each notifyChangedLocked
	// channel, so a condition a poller re-observes every tick is reported once
	// rather than filling the 20-entry notification list. Guarded by a.mu.
	notifiedChannels map[string]string
	// notificationEmit replaces the Wails event emit for error/warn
	// notification pushes. nil in production; see pushNotification.
	notificationEmit func(Notification)
	// flowProgressEmit replaces the Wails event emit for the per-step
	// "flow:progress" pushes a flow run makes. nil in production; the test
	// seam, exactly like notificationEmit. See emitFlowProgress.
	flowProgressEmit      func(types.FlowProgress)
	oauth2OpenURL         func(context.Context, string) error
	oauth2OpenInAppURL    func(context.Context, oauth2AuthorizationBrowserRequest) error
	revealInFolder        func(string) error
	oauth2CallbackTimeout time.Duration
	oauth2                map[string]oauth2TokenResponse
	oauth2Baseline        map[string]oauth2TokenResponse
	oauth2PendingMu       sync.Mutex
	oauth2Authorization   map[string]chan oauth2AuthorizationResult
	oauth2Implicit        map[string]chan oauth2ImplicitResult
	websocketSessions     map[string]*websocketSession
	grpcStreamSessions    map[string]*grpcStreamSession
	terminals             map[string]*terminalSessionProcess
	startedAt             time.Time
	lastCPUTime           time.Duration
	lastCPUWall           time.Time
	requests              *requestLifecycleRegistry
	collectionRuns        *collectionRunLifecycleRegistry
	// transportCache (US-016) keys one *http.Transport per outbound security
	// posture so requests reuse connections instead of cloning an empty pool
	// per send. Its own lock is a leaf below a.mu; see http_transport_cache.go.
	// The zero value is usable, so an App built as a bare literal works too.
	transportCache         xport.Cache
	workspaceRuntime       *workspaceWindowRuntime
	workspaceProcessStart  func(string, []string) error
	collectionImportHooks  *collectionImportHooks
	discoveryOverride      *discoveryOverrides
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
	// Best-effort side writes report their FIRST failure of the session and
	// then stay quiet: every caller discards their error on purpose, and
	// migrateResponseBodiesLocked would otherwise log once per cached response
	// at startup. See reportResponseStoreFailure / reportHistoryFailure.
	responseStoreFailureOnce sync.Once
	historyFailureOnce       sync.Once
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

	// The MCP agent interface. mcpMu guards mcpServer and mcpLastError and
	// nothing else; like mockMu and docsMu it is taken WITHOUT a.mu held,
	// because binding a socket under the state lock is the failure mode
	// mock_server.go was restructured to avoid. mcpTokenMu is separate from
	// mcpMu so reading the token — a file read — never happens while the
	// server lock is held, and so two concurrent readers of a token that does
	// not exist yet cannot generate two different ones.
	mcpMu        sync.Mutex
	mcpServer    *mcpserver.Server
	mcpLastError string
	mcpTokenMu   sync.Mutex
	// The Phase 2 run tier. Three independent locks, none of them ever held
	// while taking a.mu, and none of them held while a request is in flight:
	//
	//   mcpApprovalMu guards the pending new-host approvals — the same waiter
	//   shape as oauth2PendingMu, because the problem is the same one (a
	//   goroutine blocked on a decision only the frontend can make).
	//
	//   mcpApprovalFileMu guards the REMEMBERED approvals and their lazy load
	//   from disk. Separate from mcpApprovalMu so persisting a remembered
	//   answer — a file write — never happens while a waiter is being resolved.
	//
	//   the audit store carries its own lock (mcp_audit.go); mcpAuditOnce only
	//   builds it.
	mcpApprovalMu sync.Mutex
	mcpApprovals  map[string]*mcpPendingApproval
	// mcpApprovalEmit replaces the Wails event emit for the approval prompt.
	// nil in production; the test seam, exactly like notificationEmit.
	mcpApprovalEmit func(types.MCPApprovalRequest)
	// mcpApprovalTimeout bounds one prompt. Zero means the 60s default; tests
	// shrink it rather than waiting out a minute to prove a timeout denies.
	mcpApprovalTimeout     time.Duration
	mcpApprovalFileMu      sync.Mutex
	mcpApprovalsRemembered []types.MCPApproval
	mcpApprovalsLoaded     bool
	mcpAuditOnce           sync.Once
	mcpAuditStore          *mcpAuditStore

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
	// After ensureReady, because the preference it reads is only settled once
	// the state has been loaded and normalised — before that the port would
	// still be a zero from an unnormalised struct. applyMCPPreferences takes no
	// state lock of its own, so it must not be called from inside ensureReady.
	a.applyMCPPreferences(a.mcpPreferencesSnapshot())
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
	// Same rationale, same position in the ordering: the MCP listener holds a
	// fixed port that the next launch will try to bind again.
	a.stopMCPServer()
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

func (a *App) ListGRPCMethods(collectionID, itemID, environmentID string) ([]GRPCMethodInfo, error) {
	requestCopy, collectionCopy, vars, err := a.effectiveRequestContextForExecution(collectionID, itemID, environmentID)
	if err != nil {
		return nil, err
	}
	if requestCopy.Type != "grpc" {
		return nil, errors.New("active request is not gRPC")
	}
	timeout := requestTimeoutMilliseconds(requestCopy.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	// §4.5: a UI binding that dials outside the send seam labels its own
	// egress. This one reflects against the user's gRPC server and may fetch an
	// OAuth2 token to do it, neither of which passes through
	// sendRequestWithControlsContext — so the provenance has to be attached
	// here or the egress is unlabeled, which strict mode refuses.
	ctx, cancel := context.WithTimeout(mcpContextWithUIProvenance(context.Background()), time.Duration(timeout)*time.Millisecond)
	defer cancel()
	if grpcexec.HasProtoInputs(requestCopy, collectionCopy, vars) {
		return grpcexec.ListMethodsFromProto(ctx, requestCopy, collectionCopy, vars)
	}
	dialConfig, err := a.grpcDialConfigForRequestContext(ctx, collectionCopy, requestCopy, interpolate(requestCopy.URL, vars), vars)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	outgoingCtx, err := grpcexec.OutgoingContext(ctx, requestCopy, vars, a.grpcOAuth2Fetcher(ctx))
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
	// §4.5, as in ListGRPCMethods above: a UI binding that dials outside the
	// send seam labels its own egress.
	ctx, cancel := context.WithTimeout(mcpContextWithUIProvenance(context.Background()), time.Duration(timeout)*time.Millisecond)
	defer cancel()
	var binding grpcMethodBinding
	if grpcexec.HasProtoInputs(requestCopy, collectionCopy, vars) {
		binding, err = grpcexec.CompileMethod(ctx, requestCopy, collectionCopy, vars)
	} else {
		dialConfig, targetErr := a.grpcDialConfigForRequestContext(ctx, collectionCopy, requestCopy, interpolate(requestCopy.URL, vars), vars)
		if targetErr != nil {
			return "", targetErr
		}
		conn, connErr := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
		if connErr != nil {
			return "", connErr
		}
		defer func() { _ = conn.Close() }()
		outgoingCtx, ctxErr := grpcexec.OutgoingContext(ctx, requestCopy, vars, a.grpcOAuth2Fetcher(ctx))
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
	// Resolved exactly as the gRPC executor resolves it (see buildGrpcDialConfig),
	// so the copied command verifies certificates if and only if the real request
	// would have. Passing requestCopy.Settings.VerifyTLS alone silently dropped
	// -insecure whenever the app-level SSL preference was the thing turning
	// verification off.
	verifyTLS := requestTLSVerificationEnabled(a.appTLSSettingsSnapshot().Request, requestCopy.Settings.VerifyTLS)
	return grpcexec.GenerateGrpcurlCommand(collectionCopy, requestCopy, vars, verifyTLS)
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
	// The save flags are cleared BEFORE the write because the .bru/.yml the
	// writer produces is a serialisation of the item as saved. If the write
	// then fails, the in-memory item must go back to being a draft: leaving
	// Draft=false on an item whose edits never reached disk tells the user
	// their work is saved, hides the unsaved-changes dot, and lets the
	// collection watcher overwrite the item from the stale file on disk.
	restore := snapshotSavedItemFlags(item)
	markItemSaved(collection, item, time.Now())
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		restore.apply(item)
		return AppState{}, err
	}
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			restore.apply(item)
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

// UpdatePreferences stores the normalised preferences and applies the ones that
// own something outside the state — currently the MCP listener.
//
// Split in two because of the lock. The body below holds a.mu for its whole
// duration, and starting or stopping a listener under the state lock is the
// failure mode mock_server.go documents; applyMCPPreferences must therefore run
// after the lock is released, which a `defer a.mu.Unlock()` in one function
// cannot express.
func (a *App) UpdatePreferences(preferences Preferences) (AppState, error) {
	state, mcp, mcpChanged, err := a.storePreferences(preferences)
	if err != nil {
		return AppState{}, err
	}
	if mcpChanged {
		a.applyMCPPreferences(mcp)
	}
	return state, nil
}

// storePreferences is the locked half of UpdatePreferences. It reports the new
// MCP preference and whether it differs from the one that was in force, so the
// caller only touches the listener when something about it actually changed.
func (a *App) storePreferences(preferences Preferences) (AppState, types.MCPPreferences, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, types.MCPPreferences{}, false, err
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
	mcpChanged := a.state.Preferences.MCP != next.MCP
	a.state.Preferences = next
	return a.state, next.MCP, mcpChanged, a.markDirty(persistScopeState)
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
	collection, importWarnings, err := collectionFromImportDetailed(payload)
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
		_ = rollbackCollectionImportMutations(a, mutations)
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
	// Raised AFTER the success so it sits above it in the (newest-first) list.
	// An import that dropped items it could not read is still an import, but
	// announcing only the success left the user with no way to learn that
	// anything was missing until they went looking for a request that was not
	// there.
	if len(importWarnings) > 0 {
		a.notify("warning", "Imported "+collection.Name+" with warnings: "+strings.Join(importWarnings, " "))
	}
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

// gRPC method resolution, messages, metadata and grpcurl moved to internal/grpcexec.

// randomHex, wssePasswordDigest and quoteDigestValue moved to
// internal/auth/wsse with the header they build. Wrapped because internal/core
// still uses them for HTTP digest auth.

// WebSocket message preparation moved to internal/wsexec.
//
// normalizeWSMessageType is wrapped rather than renamed at seven call sites.

// Dialling stays in internal/core:
// it reads the app's TLS settings, http client and per-collection certificates.

// OAuth 1.0a request signing moved to internal/auth/oauth1.
//
// setRequestBodyString stayed: it was declared inside that block but is generic,
// and internal/auth/awsv4 already keeps its own copy for the same reason -- it
// rewrites a body AND keeps GetBody consistent with it, which any signer that
// hashes the payload depends on.

// AWS SigV4 signing and credential resolution moved to internal/auth/awsv4.
//
// hmacSHA256Bytes stays here: OAuth1 signing uses it too, so it is generic
// crypto rather than part of the AWS surface.

// HTTP Digest authentication moved to internal/auth/digest.

// The scripting runtime moved to internal/scripting.

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
			// Off, but with a real port already chosen. The Settings panel shows
			// the pairing command before the toggle is flipped, and a zero there
			// would read as "no port yet" to a user who is about to copy it.
			MCP: prefs.NormalizeMCP(types.MCPPreferences{}),
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

package main

import (
	"LiteAPI/internal/auth/awsv4"
	"LiteAPI/internal/auth/digest"
	"LiteAPI/internal/auth/oauth1"
	"LiteAPI/internal/auth/wsse"
	"LiteAPI/internal/codegen"
	"LiteAPI/internal/cookiejar"
	"LiteAPI/internal/grpcexec"
	"LiteAPI/internal/importers"
	"LiteAPI/internal/interp"
	"LiteAPI/internal/openapisync"
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/scripting"
	"LiteAPI/internal/store/bru"
	"LiteAPI/internal/store/yamlstore"
	"LiteAPI/internal/transport"
	"LiteAPI/internal/types"
	"LiteAPI/internal/wsexec"
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Azure/go-ntlmssp"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"
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

// Moved to internal/types. Aliased so package main compiles unchanged.
type TerminalSession = types.TerminalSession

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

// Moved to internal/types. Aliased so package main compiles unchanged.
type AppState = types.AppState
type Workspace = types.Workspace

// Moved to internal/types. Aliased so package main compiles unchanged.
type Collection = types.Collection
type CollectionWatchRefreshResult = types.CollectionWatchRefreshResult
type FolderConfig = types.FolderConfig

// Moved to internal/types. These stay as aliases rather than being deleted:
// package main still names them in 200+ places, and an alias keeps that code
// compiling while the extraction proceeds cluster by cluster.
type ProxyConfig = types.ProxyConfig
type ProxyAuthConfig = types.ProxyAuthConfig
type ProxyPACConfig = types.ProxyPACConfig
type ProxyPreferences = types.ProxyPreferences
type ClientCertificateConfig = types.ClientCertificateConfig

// Moved to internal/types. Aliased so package main compiles unchanged.
type CollectionPresets = types.CollectionPresets
type CollectionProtobufConfig = types.CollectionProtobufConfig
type CollectionSecurityConfig = types.CollectionSecurityConfig
type CollectionProtoFile = types.CollectionProtoFile
type CollectionProtoImportPath = types.CollectionProtoImportPath
type OpenAPISyncConfig = types.OpenAPISyncConfig

// Moved to internal/types. Aliased so package main compiles unchanged.
type RequestItem = types.RequestItem

// Moved to internal/types. Aliased so package main compiles unchanged.
type GrpcMessage = types.GrpcMessage
type WSMessage = types.WSMessage

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

// Moved to internal/types. Aliased so package main compiles unchanged.
type GRPCMethodInfo = types.GRPCMethodInfo
type RequestBody = types.RequestBody
type FileBodyEntry = types.FileBodyEntry
type FormPart = types.FormPart

// Moved to internal/types. Aliased so package main compiles unchanged.
type KeyValue = types.KeyValue
type Variable = types.Variable
type RequestVars = types.RequestVars
type AuthConfig = types.AuthConfig
type OAuth1Auth = types.OAuth1Auth
type OAuth2Auth = types.OAuth2Auth
type OAuth2AdditionalParam = types.OAuth2AdditionalParam
type AWSV4Auth = types.AWSV4Auth

// Moved to internal/types. Aliased so package main compiles unchanged.
type Assertion = types.Assertion
type RequestSettings = types.RequestSettings

// Moved to internal/types. Aliased so package main compiles unchanged.
type Response = types.Response
type ResponseExample = types.ResponseExample
type ResponseExampleRequest = types.ResponseExampleRequest
type ResponseExamplePayload = types.ResponseExamplePayload
type TestResult = types.TestResult
type ScriptLog = types.ScriptLog
type TimelineItem = types.TimelineItem

// Moved to internal/types. Aliased so package main compiles unchanged.
type Environment = types.Environment
type DotEnvFile = types.DotEnvFile

// Moved to internal/types. Aliased so package main compiles unchanged.
type GlobalEnvironmentExportResult = types.GlobalEnvironmentExportResult
type GlobalEnvironmentExportFile = types.GlobalEnvironmentExportFile
type GlobalEnvironmentSaveResult = types.GlobalEnvironmentSaveResult

// Moved to internal/types. Aliased so package main compiles unchanged.
type CollectionExportOptions = types.CollectionExportOptions
type CollectionExportResult = types.CollectionExportResult
type CollectionSaveResult = types.CollectionSaveResult

// Moved to internal/types. Aliased so package main compiles unchanged.
type OpenAPISyncOptions = types.OpenAPISyncOptions
type OpenAPISyncEndpointChange = types.OpenAPISyncEndpointChange
type OpenAPISyncResult = types.OpenAPISyncResult
type OpenAPISyncUpdateCheckResult = types.OpenAPISyncUpdateCheckResult
type OpenAPISyncSpecViewResult = types.OpenAPISyncSpecViewResult
type OpenAPISyncSpecDiffLine = types.OpenAPISyncSpecDiffLine
type OpenAPISyncSpecDiffResult = types.OpenAPISyncSpecDiffResult
type OpenAPILocalDriftOptions = types.OpenAPILocalDriftOptions
type OpenAPILocalDriftResult = types.OpenAPILocalDriftResult

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

// Moved to internal/types. Aliased so package main compiles unchanged.
type OpenTab = types.OpenTab
type Feature = types.Feature

// Moved to internal/types. Aliased so package main compiles unchanged.
type DevToolsSnapshot = types.DevToolsSnapshot
type DevToolsProcessMetric = types.DevToolsProcessMetric

// Moved to internal/types. Aliased so package main compiles unchanged.
type Preferences = types.Preferences
type LayoutPreferences = types.LayoutPreferences
type DisplayPreferences = types.DisplayPreferences
type FontPreferences = types.FontPreferences
type RequestPreferences = types.RequestPreferences
type CustomCaCertificatePreferences = types.CustomCaCertificatePreferences
type KeepDefaultCaCertificatesPreferences = types.KeepDefaultCaCertificatesPreferences
type GeneralPreferences = types.GeneralPreferences
type AutoSavePreferences = types.AutoSavePreferences
type CachePreferences = types.CachePreferences
type SSLSessionCachePreferences = types.SSLSessionCachePreferences
type FileCachePreferences = types.FileCachePreferences
type DevToolsPreferences = types.DevToolsPreferences
type DevToolsNetworkPreferences = types.DevToolsNetworkPreferences
type KeyBinding = types.KeyBinding

// Moved to internal/types. Aliased so package main compiles unchanged.
type Notification = types.Notification
type NetworkLog = types.NetworkLog

type CookieEntry = types.CookieEntry

// Moved to internal/types. Aliased so package main compiles unchanged.
type CookieInput = types.CookieInput

// Moved to internal/types. Aliased so package main compiles unchanged.
type RunnerSnapshot = types.RunnerSnapshot
type RunnerOptions = types.RunnerOptions
type GenerateCollectionDocsOptions = types.GenerateCollectionDocsOptions
type GenerateCollectionDocsResult = types.GenerateCollectionDocsResult
type RunResult = types.RunResult
type RequestPatch = types.RequestPatch
type ImportPayload = types.ImportPayload

// Moved to internal/types. Aliased so package main compiles unchanged.
type GitCollectionCandidate = types.GitCollectionCandidate
type GitCloneResult = types.GitCloneResult
type GitCloneProgress = types.GitCloneProgress

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

func (a *App) SendRequest(collectionID, itemID, environmentID string) (AppState, error) {
	state, _, err := a.sendRequestWithControls(collectionID, itemID, environmentID, nil)
	return state, err
}

func (a *App) SendRequestWithPromptValues(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	state, _, err := a.sendRequestWithControls(collectionID, itemID, environmentID, promptValues)
	return state, err
}

func (a *App) sendRequestWithControls(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, scripting.Controls, error) {
	state, controls, _, err := a.sendRequestWithControlsContext(context.Background(), collectionID, itemID, environmentID, promptValues, nil, runnerIteration{})
	return state, controls, err
}

// sendRequestWithControlsContext resolves collectionID/itemID twice: once on
// entry, and again on the tail because a.mu is released across the network I/O
// and the request may have moved or gone in that window. index (US-024) is an
// optional per-run lookup hint that makes both resolutions O(1); it is verified
// against live state on every use, and nil restores the plain linear scans.
//
// The fourth return value is the *Response this call stored on the item. The
// collection runner used to re-find the item in the returned state purely to
// read it back, which was another linear scan per request.
func (a *App) sendRequestWithControlsContext(parent context.Context, collectionID, itemID, environmentID string, promptValues map[string]string, index *runnerLookupIndex, iteration runnerIteration) (AppState, scripting.Controls, *Response, error) {
	controls := scripting.Controls{}
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return AppState{}, controls, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceIndexedLocked(index, collectionID)
	if err != nil {
		a.mu.Unlock()
		return AppState{}, controls, nil, err
	}
	item, err := index.findItemIndexed(collectionID, collection, itemID)
	if err != nil {
		a.mu.Unlock()
		return AppState{}, controls, nil, err
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	// US-046. Applied after construction and before Combined is read, so the
	// row participates in the precedence chain rather than being pasted over
	// the result of it.
	scripting.ApplyIterationDataToContext(scriptVariables, iteration.Data)
	vars := scriptVariables.Combined
	scriptLogs := []ScriptLog{}
	scriptTimeline := []TimelineItem{}
	scriptCookieJar := scripting.NewScriptCookieJar(scripting.CloneCookieEntries(a.state.Cookies))
	initialCookies := scriptCookieJar.Snapshot()
	scriptRunDepth := 0
	scriptMeta := scripting.ScriptRuntimeMeta{
		CollectionName:            collection.Name,
		CollectionPath:            collection.Path,
		EnvironmentName:           scripting.SelectedEnvironmentName(collection, environmentID),
		JSSandboxMode:             collectionJSSandboxMode(*collection),
		Variables:                 scriptVariables,
		OAuth2CredentialVariables: a.oauth2CredentialVariablesSnapshot,
		ResetOAuth2Credential:     a.resetOAuth2Credential,
		IterationIndex:            iteration.Index,
		IterationCount:            iteration.Count,
	}
	scriptMeta.RecordTimeline = func(entry TimelineItem) {
		scriptTimeline = append(scriptTimeline, entry)
	}
	scriptMeta.RunRequest = func(target string) (Response, *TimelineItem, error) {
		return a.runScriptedCollectionRequest(collectionID, target, environmentID, scriptVariables, scriptCookieJar, &scriptLogs, &scriptRunDepth, scriptMeta.RecordTimeline)
	}
	scripts := scripting.MergedRuntimeScripts(*collection, requestCopy)
	preferences := normalizePreferences(a.state.Preferences)
	shouldStoreCookies := boolPtrValue(preferences.Request.StoreCookies, true) && requestCopy.Settings.StoreCookies
	shouldSendCookies := boolPtrValue(preferences.Request.SendCookies, true) && requestCopy.Settings.StoreCookies
	a.mu.Unlock()

	// Register before pre-request scripts start so the Wails SendRequest call
	// remains truthfully cancellable for its entire HTTP/GraphQL execution.
	// Scripts are not interruptible, so cancellation is observed at the
	// checkpoints below and prevents any transport that has not started.
	executionContext, finishExecution := a.startCancellableRequestWithParent(parent, collectionID, itemID, requestCopy.Type)
	defer finishExecution()

	var response Response
	preMeta := scriptMeta
	preMeta.TimelinePhase = "pre-request"
	preState := (*scripting.RequestState)(nil)
	if requestContextCancelled(executionContext) {
		response = cancelledRequestResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else {
		preState, err = scripting.RunPreRequestScriptWithJarStateMeta(scripts.Pre, &requestCopy, vars, scriptCookieJar, preMeta, &scriptLogs)
		controls.Merge(preState)
	}
	if requestContextCancelled(executionContext) {
		response = cancelledRequestResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else if err != nil {
		response = scripting.ScriptErrorResponse("pre-request script", err)
		response.ScriptLogs = scriptLogs
	} else if preState != nil && preState.SkipRequest {
		response = scripting.ScriptSkippedResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else {
		requestURL := cookiejar.PreviewRequestURL(requestCopy, vars)
		if shouldSendCookies {
			cookiejar.AttachHeader(&requestCopy, scriptCookieJar.Snapshot(), requestURL)
		}
		response = a.executeHTTP(executionContext, collectionID, collectionCopy, requestCopy, vars, preState, scriptMeta.RecordTimeline)
		controls.Merge(preState)
		if requestContextCancelled(executionContext) {
			markRequestCancelled(&response)
			response.ScriptLogs = scriptLogs
		} else {
			scriptCookieJar.UpsertAll(response.Cookies)
			postVariablesMeta := scriptMeta
			postVariablesMeta.TimelinePhase = "post-response"
			if err := scripting.RunPostResponseVariables(scripting.EffectiveResponseVariables(collectionCopy, requestCopy), requestCopy, &response, scriptVariables, scriptCookieJar, postVariablesMeta, &scriptLogs); err != nil {
				response.TestResults = append(response.TestResults, TestResult{Name: "post-response variables", Passed: false, Message: err.Error()})
			}
			if requestContextCancelled(executionContext) {
				markRequestCancelled(&response)
				response.ScriptLogs = scriptLogs
			} else {
				postMeta := scriptMeta
				postMeta.TimelinePhase = "post-response"
				postState, err := scripting.RunPostResponseScriptWithJarStateMeta(scripts.Post, requestCopy, &response, vars, scriptCookieJar, postMeta, &scriptLogs)
				controls.Merge(postState)
				if err != nil {
					response.TestResults = append(response.TestResults, TestResult{Name: "post-response script", Passed: false, Message: err.Error()})
				}
				if requestContextCancelled(executionContext) {
					markRequestCancelled(&response)
					response.ScriptLogs = scriptLogs
				} else {
					testsMeta := scriptMeta
					testsMeta.TimelinePhase = "tests"
					testResults, testState := scripting.EvaluateRuntimeTestsWithJarStateMeta(scripts.Tests, response, requestCopy, vars, scriptCookieJar, testsMeta, &scriptLogs)
					controls.Merge(testState)
					response.TestResults = append(response.TestResults, testResults...)
					response.ScriptLogs = scriptLogs
					if requestContextCancelled(executionContext) {
						markRequestCancelled(&response)
					}
				}
			}
		}
	}

	// Close the lifecycle before persistence so cancellation has a single,
	// truthful outcome: either it won and this response is cancelled, or the
	// registry was closed and CancelRequest reports false for this completion.
	if finishExecution() || requestContextCancelled(executionContext) {
		markRequestCancelled(&response)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// US-076. Re-resolve the WORKSPACE too, and use it below — do not fall back
	// to the `ws` captured before a.mu was released for the network round trip.
	//
	// ws is a *Workspace pointing into a.state.Workspaces. While the lock was
	// released, anything that appends a workspace past the slice's capacity
	// reallocates that backing array, and the captured pointer then addresses
	// memory nothing reads. scripting.ApplyScriptVariableContextToState would write the
	// script's variable changes into the dead array and report success: silent
	// corruption, not a crash, and invisible until the user notices a variable
	// that did not stick.
	//
	// This line already re-resolved the collection and discarded the workspace
	// with `_`; keeping it is the whole fix.
	liveWorkspace, collection, err := a.findCollectionWithWorkspaceIndexedLocked(index, collectionID)
	if err != nil {
		return AppState{}, controls, nil, err
	}
	item, err = index.findItemIndexed(collectionID, collection, itemID)
	if err != nil {
		return AppState{}, controls, nil, err
	}
	if shouldStoreCookies {
		a.state.Cookies = cookiejar.MergeScriptJar(a.state.Cookies, initialCookies, scriptCookieJar.Snapshot())
		a.pruneExpiredCookiesLocked()
	}
	scripting.ApplyScriptVariableContextToState(&a.state, liveWorkspace, collection, environmentID, scriptVariables)
	// US-009 step 4. Store the body and record its handle as the response lands
	// in state. Best-effort by design at this step: Body is still populated and
	// still authoritative, so a failed cache write must not fail a request the
	// user just saw succeed. See migrateResponseBodiesLocked for where that
	// contract inverts.
	if controls.Visualizer != nil {
		response.Visualizer = controls.Visualizer
	}
	_ = a.attachResponseBody(&response)
	// US-048. Best-effort and deliberately ignoring the error: a request that
	// reached the server must not be reported as failed because its history
	// line could not be written. Recorded AFTER attachResponseBody so the
	// entry carries the body handle rather than duplicating the body.
	_ = a.recordSendHistory(collectionID, requestCopy, &response)
	item.Response = &response
	item.Timeline = append(item.Timeline, scriptTimeline...)
	if controls.SkipRequest {
		item.Timeline = append(item.Timeline, TimelineItem{
			ID:         newID("timeline"),
			Kind:       "script",
			Message:    "Skipped by pre-request script",
			At:         time.Now(),
			Duration:   response.DurationMs,
			RequestID:  item.ID,
			Source:     "sendRequest",
			Phase:      "pre-request",
			StatusText: response.StatusText,
			SourceFile: timelineSourceFileForItem(collection.Path, *item),
		})
	} else {
		item.Timeline = append(item.Timeline, mainRequestTimelineItem(*item, requestCopy, response))
		if requestCopy.Type == "http" || requestCopy.Type == "graphql" {
			item.Timeline = append(item.Timeline, httpTimingTimelineItems(*item, response)...)
		}
		if requestCopy.Type == "grpc" {
			grpcTimelineRequest := requestCopy
			grpcTimelineRequest.ID = item.ID
			item.Timeline = append(item.Timeline, grpcExecutionTimelineItems(grpcTimelineRequest, response, vars)...)
		}
		a.state.NetworkLog = append([]NetworkLog{networkLogEntry(requestCopy, response, vars)}, a.state.NetworkLog...)
		if len(a.state.NetworkLog) > 100 {
			a.state.NetworkLog = a.state.NetworkLog[:100]
		}
	}
	return a.state, controls, item.Response, a.markDirty(persistScopeState)
}

func mainRequestTimelineItem(item RequestItem, requestCopy RequestItem, response Response) TimelineItem {
	entry := TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		Message:    fmt.Sprintf("%s %s -> %d", requestCopy.Method, response.RequestedURL, response.Status),
		At:         time.Now(),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "main",
		Method:     strings.ToUpper(firstNonEmpty(requestCopy.Method, http.MethodGet)),
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: cleanStatusText(response.Status, response.StatusText),
	}
	if requestCopy.Type != "grpc" {
		return entry
	}
	grpcMethod := firstNonEmpty(response.Headers["grpc-method"], requestCopy.Method)
	streamType := firstNonEmpty(response.Headers["grpc-stream"], grpcStreamTypeLabelFromStorage(requestCopy.GrpcMethodType), "unary")
	requestCount := strings.TrimSpace(response.Headers["grpc-request-count"])
	responseCount := strings.TrimSpace(response.Headers["grpc-response-count"])
	detailParts := []string{streamType + " stream"}
	if requestCount != "" {
		detailParts = append(detailParts, "sent "+requestCount)
	}
	if responseCount != "" {
		detailParts = append(detailParts, "received "+responseCount)
	}
	entry.Method = "CALL"
	entry.Message = fmt.Sprintf("CALL %s %s -> %d (%s)", strings.TrimPrefix(grpcMethod, "/"), response.RequestedURL, response.Status, strings.Join(detailParts, ", "))
	return entry
}

func grpcExecutionTimelineItems(item RequestItem, response Response, vars map[string]string) []TimelineItem {
	at := response.SentAt
	if at.IsZero() {
		at = time.Now()
	}
	rows := []TimelineItem{grpcExecutionRequestTimelineItem(item, response, vars, at)}
	messages := grpcexec.GrpcurlRequestMessages(item, vars)
	if len(messages) == 0 {
		messages = []GrpcMessage{{Name: "message 1", Content: "{}"}}
	}
	for index, message := range messages[:grpcExecutionRequestMessageCount(item, response, len(messages))] {
		rows = append(rows, grpcExecutionMessageTimelineItem(item, response, message, index, at.Add(time.Duration(index+1)*time.Millisecond)))
	}
	responseRows, lastResponseAt := grpcExecutionResponseTimelineItems(item, response, at.Add(time.Duration(len(rows)+1)*time.Millisecond))
	rows = append(rows, responseRows...)
	terminalAt := lastResponseAt
	if terminalAt.IsZero() {
		terminalAt = at.Add(time.Duration(len(rows)+1) * time.Millisecond)
	}
	if len(response.Metadata) > 0 {
		rows = append(rows, grpcStreamMetadataTimelineItem(item, response, terminalAt))
	}
	if response.Headers["grpc-status"] != "" || len(response.Trailers) > 0 || response.Error != "" {
		rows = append(rows, grpcStreamStatusTimelineItem(item, response, terminalAt))
	}
	if response.Error != "" {
		rows = append(rows, grpcExecutionErrorTimelineItem(item, response, terminalAt))
	} else {
		rows = append(rows, grpcExecutionEndTimelineItem(item, response, terminalAt))
	}
	return rows
}

func grpcExecutionRequestTimelineItem(item RequestItem, response Response, vars map[string]string, at time.Time) TimelineItem {
	methodName := grpcTimelineMethodName(item, response)
	streamType := grpcTimelineStreamType(item, response)
	payloadParts := []string{"method: " + firstNonEmpty(methodName, strings.TrimSpace(item.Method), "CALL"), "url: " + response.RequestedURL, "stream: " + streamType}
	messages := grpcexec.GrpcurlRequestMessages(item, vars)
	if streamType != "client" && streamType != "bidi" && len(messages) > 0 {
		payloadParts = append(payloadParts, "body:\n"+strings.TrimSpace(grpcexec.GrpcurlMessageContent(messages[0])))
	}
	return grpcExecutionTimelineItem(item, response, "request", "", fmt.Sprintf("gRPC request %s %s (%s stream)", methodName, response.RequestedURL, streamType), strings.Join(payloadParts, "\n"), at)
}

func grpcExecutionMessageTimelineItem(item RequestItem, response Response, message GrpcMessage, index int, at time.Time) TimelineItem {
	name := firstNonEmpty(strings.TrimSpace(message.Name), fmt.Sprintf("message %d", index+1))
	payload := strings.TrimSpace(grpcexec.GrpcurlMessageContent(message))
	if payload == "" {
		payload = "{}"
	}
	return grpcExecutionTimelineItem(item, response, "message", name, fmt.Sprintf("gRPC message %s %s", name, response.RequestedURL), payload, at)
}

func grpcExecutionResponseTimelineItems(item RequestItem, response Response, at time.Time) ([]TimelineItem, time.Time) {
	body := strings.TrimSpace(response.Body)
	if body == "" {
		return nil, time.Time{}
	}
	rawResponses := []json.RawMessage{}
	if strings.HasPrefix(body, "[") {
		if err := json.Unmarshal([]byte(body), &rawResponses); err != nil {
			rawResponses = nil
		}
	}
	if len(rawResponses) == 0 {
		rawResponses = []json.RawMessage{json.RawMessage(body)}
	}
	rows := make([]TimelineItem, 0, len(rawResponses))
	lastAt := at
	for index, raw := range rawResponses {
		rowAt := at.Add(time.Duration(index) * time.Millisecond)
		name := fmt.Sprintf("response %d", index+1)
		rows = append(rows, grpcExecutionTimelineItem(item, response, "response", name, fmt.Sprintf("Response Message #%d %s", index+1, response.RequestedURL), grpcTimelineJSONPayload(raw), rowAt))
		lastAt = rowAt
	}
	return rows, lastAt
}

func grpcExecutionEndTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	streamType := grpcTimelineStreamType(item, response)
	message := fmt.Sprintf("gRPC call ended %s", response.RequestedURL)
	if streamType != "unary" {
		message = fmt.Sprintf("Stream Ended %s", response.RequestedURL)
	}
	payloadParts := []string{}
	if requestCount := strings.TrimSpace(response.Headers["grpc-request-count"]); requestCount != "" {
		payloadParts = append(payloadParts, "sent: "+requestCount)
	}
	if responseCount := strings.TrimSpace(response.Headers["grpc-response-count"]); responseCount != "" {
		payloadParts = append(payloadParts, "received: "+responseCount)
	}
	if statusValue := strings.TrimSpace(response.Headers["grpc-status"]); statusValue != "" {
		payloadParts = append(payloadParts, "grpc-status: "+statusValue)
	}
	return grpcExecutionTimelineItem(item, response, "end", "", message, strings.Join(payloadParts, "\n"), at.Add(3*time.Millisecond))
}

func grpcExecutionErrorTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	row := grpcExecutionTimelineItem(item, response, "error", "", "gRPC error "+response.RequestedURL, firstNonEmpty(response.Error, response.StatusText), at.Add(3*time.Millisecond))
	row.Trailers = response.Trailers
	return row
}

func grpcExecutionTimelineItem(item RequestItem, response Response, eventType, eventName, message, payload string, at time.Time) TimelineItem {
	if at.IsZero() {
		at = time.Now()
	}
	errorText := ""
	if eventType == "error" {
		errorText = firstNonEmpty(response.Error, response.StatusText)
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  eventType,
		EventName:  eventName,
		Message:    message,
		At:         at,
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: cleanStatusText(response.Status, response.StatusText),
		Error:      errorText,
		Payload:    payload,
	}
}

func grpcExecutionRequestMessageCount(item RequestItem, response Response, messageCount int) int {
	if messageCount <= 0 {
		return 0
	}
	switch grpcTimelineStreamType(item, response) {
	case "client", "bidi":
	default:
		return 0
	}
	if count, ok := grpcTimelineHeaderInt(response.Headers, "grpc-request-count"); ok {
		return clampInt(count, 0, messageCount)
	}
	if response.Error != "" && response.Status == 0 {
		return 0
	}
	return messageCount
}

func grpcTimelineHeaderInt(headers map[string]string, key string) (int, bool) {
	if headers == nil {
		return 0, false
	}
	value := strings.TrimSpace(headers[key])
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func grpcTimelineMethodName(item RequestItem, response Response) string {
	return strings.TrimPrefix(firstNonEmpty(response.Headers["grpc-method"], item.Method), "/")
}

func grpcTimelineStreamType(item RequestItem, response Response) string {
	return firstNonEmpty(response.Headers["grpc-stream"], grpcStreamTypeLabelFromStorage(item.GrpcMethodType), "unary")
}

func grpcTimelineJSONPayload(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var value interface{}
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return trimmed
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return trimmed
	}
	return string(body)
}

func networkLogEntry(item RequestItem, response Response, vars map[string]string) NetworkLog {
	return NetworkLog{
		ID:              newID("net"),
		Method:          firstNonEmpty(strings.ToUpper(strings.TrimSpace(item.Method)), http.MethodGet),
		URL:             response.RequestedURL,
		Status:          response.Status,
		StatusText:      response.StatusText,
		DurationMs:      response.DurationMs,
		Size:            response.Size,
		At:              time.Now(),
		Error:           response.Error,
		RequestHeaders:  networkLogRequestHeaders(item, vars),
		RequestBody:     networkLogRequestBody(item.Body, vars),
		ResponseHeaders: cloneStringMap(response.Headers),
		ResponseBody:    truncateNetworkLogBody(response.Body),
	}
}

func networkLogRequestHeaders(item RequestItem, vars map[string]string) map[string]string {
	headers := map[string]string{}
	for _, header := range item.Headers {
		if header.Enabled && strings.TrimSpace(header.Name) != "" {
			headers[interpolate(header.Name, vars)] = interpolate(header.Value, vars)
		}
	}
	if contentType := networkLogBodyContentType(item.Body, vars); contentType != "" && !scripting.StringMapHasKey(headers, "Content-Type") {
		headers["Content-Type"] = contentType
	}
	return headers
}

func networkLogBodyContentType(body RequestBody, vars map[string]string) string {
	switch body.Mode {
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "text", "sparql":
		return "text/plain"
	case "formUrlEncoded":
		return "application/x-www-form-urlencoded"
	case "multipartForm":
		return "multipart/form-data"
	case "file":
		if selected, ok := selectedFileBodyEntry(body); ok {
			if contentType := strings.TrimSpace(interpolate(selected.ContentType, vars)); contentType != "" {
				return contentType
			}
		}
		return "application/octet-stream"
	default:
		return ""
	}
}

func networkLogRequestBody(body RequestBody, vars map[string]string) string {
	switch body.Mode {
	case "", "none":
		return ""
	case "json":
		return truncateNetworkLogBody(interpolate(body.JSON, vars))
	case "xml":
		return truncateNetworkLogBody(interpolate(body.XML, vars))
	case "text", "sparql":
		return truncateNetworkLogBody(interpolate(body.Text, vars))
	case "graphql":
		payload := map[string]string{
			"query":     interpolate(body.GraphQLQuery, vars),
			"variables": interpolate(body.GraphQLVariables, vars),
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return ""
		}
		return truncateNetworkLogBody(string(data))
	case "formUrlEncoded":
		values := url.Values{}
		for _, field := range body.FormURLEncoded {
			if field.Enabled {
				values.Add(interpolate(field.Name, vars), interpolate(field.Value, vars))
			}
		}
		return truncateNetworkLogBody(values.Encode())
	case "multipartForm":
		lines := []string{}
		for _, part := range body.Multipart {
			if part.Enabled {
				value := interpolate(part.Value, vars)
				if strings.TrimSpace(part.FilePath) != "" {
					value = "@file " + interpolate(part.FilePath, vars)
				}
				lines = append(lines, interpolate(part.Name, vars)+"="+value)
			}
		}
		return truncateNetworkLogBody(strings.Join(lines, "\n"))
	case "file":
		if selected, ok := selectedFileBodyEntry(body); ok && strings.TrimSpace(selected.FilePath) != "" {
			return "@file " + interpolate(selected.FilePath, vars)
		}
		return ""
	default:
		return truncateNetworkLogBody(interpolate(body.Text, vars))
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func truncateNetworkLogBody(value string) string {
	if len(value) <= networkLogBodyLimit {
		return value
	}
	return value[:networkLogBodyLimit] + "\n... truncated"
}

func (a *App) runScriptedCollectionRequest(collectionID, targetRef, environmentID string, parentVariables *scripting.VariableContext, jar *scripting.CookieJar, logs *[]ScriptLog, depth *int, recordTimeline func(TimelineItem)) (Response, *TimelineItem, error) {
	if strings.TrimSpace(targetRef) == "" {
		return Response{}, nil, errors.New("bru.runRequest requires a request path or name")
	}
	if depth == nil {
		localDepth := 0
		depth = &localDepth
	}
	if *depth >= 10 {
		return Response{}, nil, errors.New("bru.runRequest exceeded nested request limit")
	}
	*depth++
	defer func() { *depth-- }()

	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return Response{}, nil, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return Response{}, nil, err
	}
	item, err := findRunRequestItem(collection, targetRef)
	if err != nil {
		a.mu.Unlock()
		return Response{}, nil, err
	}
	if item.Type == "websocket" || item.Type == "grpc" {
		response := scriptRunRequestUnsupportedProtocolResponse(*item)
		timelineEntry := scriptRunRequestTimelineItem(collection.Path, *item, response, nil)
		a.mu.Unlock()
		return response, &timelineEntry, nil
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	scripts := scripting.MergedRuntimeScripts(collectionCopy, requestCopy)
	preferences := normalizePreferences(a.state.Preferences)
	shouldSendCookies := boolPtrValue(preferences.Request.SendCookies, true) && requestCopy.Settings.StoreCookies
	nestedVariables := scripting.ScriptVariableContextForItem(parentVariables, &collectionCopy, environmentID, requestCopy)
	nestedMeta := scripting.ScriptRuntimeMeta{
		CollectionName:            collectionCopy.Name,
		CollectionPath:            collectionCopy.Path,
		EnvironmentName:           scripting.SelectedEnvironmentName(&collectionCopy, environmentID),
		JSSandboxMode:             collectionJSSandboxMode(collectionCopy),
		Variables:                 nestedVariables,
		OAuth2CredentialVariables: a.oauth2CredentialVariablesSnapshot,
		ResetOAuth2Credential:     a.resetOAuth2Credential,
		RecordTimeline:            recordTimeline,
	}
	nestedMeta.RunRequest = func(target string) (Response, *TimelineItem, error) {
		return a.runScriptedCollectionRequest(collectionID, target, environmentID, nestedVariables, jar, logs, depth, recordTimeline)
	}
	a.mu.Unlock()

	var response Response
	preMeta := nestedMeta
	preMeta.TimelinePhase = "pre-request"
	preState, err := scripting.RunPreRequestScriptWithJarStateMeta(scripts.Pre, &requestCopy, nestedVariables.Combined, jar, preMeta, logs)
	if err != nil {
		response = scripting.ScriptErrorResponse("pre-request script", err)
		response.RequestedURL = cookiejar.PreviewRequestURL(requestCopy, nestedVariables.Combined)
		response.ScriptLogs = cloneScriptLogs(logs)
		scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
		timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
		return response, &timelineEntry, nil
	}
	if preState != nil && preState.SkipRequest {
		response = scripting.ScriptSkippedResponse(requestCopy, nestedVariables.Combined)
		response.ScriptLogs = cloneScriptLogs(logs)
		scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
		timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
		return response, &timelineEntry, nil
	}

	requestURL := cookiejar.PreviewRequestURL(requestCopy, nestedVariables.Combined)
	if shouldSendCookies && jar != nil {
		cookiejar.AttachHeader(&requestCopy, jar.Snapshot(), requestURL)
	}
	func() {
		executionContext, finishExecution := a.startCancellableRequest(collectionID, requestCopy.ID, requestCopy.Type)
		defer finishExecution()
		response = a.executeHTTP(executionContext, collectionID, collectionCopy, requestCopy, nestedVariables.Combined, preState, recordTimeline)
	}()
	if jar != nil {
		jar.UpsertAll(response.Cookies)
	}
	postVariablesMeta := nestedMeta
	postVariablesMeta.TimelinePhase = "post-response"
	if err := scripting.RunPostResponseVariables(scripting.EffectiveResponseVariables(collectionCopy, requestCopy), requestCopy, &response, nestedVariables, jar, postVariablesMeta, logs); err != nil {
		response.TestResults = append(response.TestResults, TestResult{Name: "post-response variables", Passed: false, Message: err.Error()})
	}
	postMeta := nestedMeta
	postMeta.TimelinePhase = "post-response"
	postState, err := scripting.RunPostResponseScriptWithJarStateMeta(scripts.Post, requestCopy, &response, nestedVariables.Combined, jar, postMeta, logs)
	_ = postState
	if err != nil {
		response.TestResults = append(response.TestResults, TestResult{Name: "post-response script", Passed: false, Message: err.Error()})
	}
	testsMeta := nestedMeta
	testsMeta.TimelinePhase = "tests"
	testResults, _ := scripting.EvaluateRuntimeTestsWithJarStateMeta(scripts.Tests, response, requestCopy, nestedVariables.Combined, jar, testsMeta, logs)
	response.TestResults = append(response.TestResults, testResults...)
	response.ScriptLogs = cloneScriptLogs(logs)
	scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
	timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
	return response, &timelineEntry, nil
}

func scriptRunRequestTimelineItem(collectionPath string, item RequestItem, response Response, vars map[string]string) TimelineItem {
	method := strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet))
	targetURL := firstNonEmpty(response.RequestedURL, cookiejar.PreviewRequestURL(item, vars), item.URL)
	statusText := cleanStatusText(response.Status, response.StatusText)
	errorText := strings.TrimSpace(response.Error)
	if scripting.ScriptRunRequestResponseIsSkipped(response) {
		errorText = response.StatusText
		statusText = "Skipped"
	} else if response.Status == 0 && errorText != "" {
		statusText = "Error"
	}
	statusLabel := statusText
	if response.Status > 0 {
		statusLabel = strconv.Itoa(response.Status)
	}
	if strings.TrimSpace(statusLabel) == "" {
		statusLabel = "-"
	}
	at := response.SentAt
	if at.IsZero() {
		at = time.Now()
	}
	return TimelineItem{
		Message:    strings.TrimSpace(fmt.Sprintf("%s %s -> %s", method, targetURL, statusLabel)),
		At:         at,
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Method:     method,
		URL:        targetURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      errorText,
		SourceFile: timelineSourceFileForItem(collectionPath, item),
	}
}

func cloneScriptLogs(logs *[]ScriptLog) []ScriptLog {
	if logs == nil || len(*logs) == 0 {
		return nil
	}
	return append([]ScriptLog(nil), (*logs)...)
}

func scriptRunRequestUnsupportedProtocolResponse(item RequestItem) Response {
	label := "protocol"
	switch item.Type {
	case "websocket":
		label = "WebSocket"
	case "grpc":
		label = "gRPC"
	}
	return Response{
		StatusText:  fmt.Sprintf("bru.runRequest does not support %s requests", label),
		Headers:     map[string]string{},
		PreviewMode: "auto",
		SentAt:      time.Now(),
	}
}

func (a *App) RunCollection(collectionID, environmentID string) (AppState, error) {
	return a.RunCollectionWithOptions(collectionID, environmentID, RunnerOptions{})
}

// appendUnrunRunResults marks every request after a bail as unrun.
//
// A distinct status from "skipped" and "cancelled", which US-047 requires and
// which matters to a reader of the results: "skipped" means the runner decided
// not to run it (wrong protocol, unresolved prompt variables), "cancelled"
// means the user stopped the run, and "unrun" means an earlier failure ended
// the run before this request had a turn. Collapsing them would leave someone
// unable to tell a failing suite from an abandoned one.
// Only the rest of the CURRENT iteration is enumerated. Exploding every
// request of every remaining iteration into rows would bury the one failure
// that matters under thousands of identical placeholders; the snapshot's
// Iterations vs CompletedIterations pair is what reports the iterations that
// never started.
// firstFailedTestResult names the first failed assertion, or "" if none did.
//
// The first rather than a count: a run result has one error line, and the name
// of the assertion that broke is what someone acts on. The full list is still
// on the response.
func firstFailedTestResult(results []TestResult) string {
	for _, result := range results {
		if !result.Passed {
			if strings.TrimSpace(result.Message) == "" {
				return fmt.Sprintf("assertion failed: %s", result.Name)
			}
			return fmt.Sprintf("assertion failed: %s: %s", result.Name, result.Message)
		}
	}
	return ""
}

func appendUnrunRunResults(results []RunResult, items []RequestItem, from, iteration, totalIterations int) []RunResult {
	now := time.Now()
	for i := from; i < len(items); i++ {
		results = append(results, stampIteration(RunResult{
			ItemID: items[i].ID,
			Name:   items[i].Name,
			Status: "unrun",
			Error:  "not run: the run stopped at an earlier failure",
			At:     now,
		}, iteration, totalIterations))
	}
	return results
}

func (a *App) RunCollectionWithOptions(collectionID, environmentID string, options RunnerOptions) (AppState, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return AppState{}, err
	}
	globalEnvs := append([]Environment(nil), scripting.ActiveGlobalEnvironmentsForWorkspace(*ws)...)
	collectionCopy := *collection
	items := filterRunnerItems(append([]RequestItem(nil), collection.Items...), options.SelectedItemIDs)
	delayMs := normalizeRunnerDelayMs(options.DelayMs)
	// US-024: one lookup hint for this run, discarded when it returns. See
	// runner_lookup_index.go for why it is scoped rather than cached on App.
	lookupIndex := newRunnerLookupIndex(&a.state)
	a.mu.Unlock()
	// items is this run's private, never-mutated copy, so a name→index map over
	// it cannot go stale and needs no verification.
	itemsByName := runnerItemNameIndex(items)
	runContext, finishRun := a.startCancellableCollectionRun(collectionID)
	defer finishRun()

	dataRows, err := runnerDataRows(options.DataFile)
	if err != nil {
		return AppState{}, err
	}
	// US-046. With a data file the row count leads: see runnerIterationPlan for
	// why asking for more iterations than rows is clamped rather than padded.
	totalIterations := runnerIterationPlan(dataRows, options.Iterations)
	results := make([]RunResult, 0, len(items)*totalIterations)
	completedIterations := 0
	// US-045. The labelled loop is what makes "stop the run" mean the RUN and
	// not just this iteration. Every break below is explicitly one or the
	// other, and the difference is behavioural: a cancelled or bailed run must
	// not quietly start iteration 3.
iterations:
	for iteration := 1; iteration <= totalIterations; iteration++ {
		iterationRow := runnerDataRowFor(dataRows, iteration)
		currentRequestIndex := 0
		// jumps resets per iteration because it guards against an infinite
		// setNextRequest cycle WITHIN one pass. Carrying it across iterations
		// would abort a legitimate long run for looping that never happened.
		jumps := 0
		for currentRequestIndex < len(items) {
			item := items[currentRequestIndex]
			if requestContextCancelled(runContext) {
				results = append(results, stampIteration(cancelledRunResult(item), iteration, totalIterations))
				break iterations
			}
			if item.Type != "http" && item.Type != "graphql" && item.Type != "grpc" {
				results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: "skipped", At: time.Now(), Error: "protocol runner is not implemented yet"}, iteration, totalIterations))
				if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
					currentRequestIndex++
					continue
				}
				currentRequestIndex++
				continue
			}
			if prompts := scripting.PromptVariablesForRequest(globalEnvs, &collectionCopy, environmentID, item); len(prompts) > 0 {
				results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: "skipped", At: time.Now(), Error: scripting.RunnerPromptVariableSkipMessage(prompts)}, iteration, totalIterations))
				if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
					currentRequestIndex++
					continue
				}
				currentRequestIndex++
				continue
			}
			state, controls, res, err := a.sendRequestWithControlsContext(runContext, collectionID, item.ID, environmentID, nil, lookupIndex, runnerIteration{
				Index: iteration,
				Count: totalIterations,
				Data:  iterationRow,
			})
			if err != nil {
				if requestContextCancelled(runContext) {
					results = append(results, stampIteration(cancelledRunResult(item), iteration, totalIterations))
					break iterations
				}
				results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: "failed", Error: err.Error(), At: time.Now()}, iteration, totalIterations))
				if options.BailOnFailure {
					results = appendUnrunRunResults(results, items, currentRequestIndex+1, iteration, totalIterations)
					break iterations
				}
				if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
					currentRequestIndex++
					continue
				}
				currentRequestIndex++
				continue
			}
			// res is the *Response sendRequestWithControlsContext just stored on this
			// item, returned directly instead of being re-found by scanning the
			// state for the item that call had already resolved.
			status := "passed"
			errText := ""
			code := 0
			duration := int64(0)
			if res != nil {
				code = res.Status
				duration = res.DurationMs
				errText = res.Error
				if res.Cancelled {
					status = "cancelled"
				} else if res.Error != "" || res.Status >= 400 {
					status = "failed"
				}
			}
			// US-047. A failed assertion is a failed request. Without this the
			// runner only ever failed on a transport error or a >=400 status,
			// so a collection whose tests all failed against a 200 response
			// reported a fully green run — and BailOnFailure, whose criterion
			// names "a failed assertion", could never trigger on one.
			if status == "passed" && res != nil {
				if failure := firstFailedTestResult(res.TestResults); failure != "" {
					status = "failed"
					errText = failure
				}
			}
			if requestContextCancelled(runContext) {
				status = "cancelled"
				errText = "collection run cancelled"
			}
			if controls.SkipRequest && status != "cancelled" {
				status = "skipped"
			}
			results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: status, Code: code, DurationMs: duration, Error: errText, At: time.Now()}, iteration, totalIterations))
			if status == "cancelled" {
				break iterations
			}
			// US-047. Deliberately AFTER the cancelled check: a cancelled run is not
			// a failure, and reporting the remaining requests as "unrun because
			// something failed" would misattribute the user's own cancellation.
			if options.BailOnFailure && status == "failed" {
				results = appendUnrunRunResults(results, items, currentRequestIndex+1, iteration, totalIterations)
				break iterations
			}
			// bru.runner.stopExecution() stops the RUN, not the iteration —
			// that is what the script author asked for.
			if controls.StopExecution {
				break iterations
			}
			if controls.NextRequestSet {
				jumps++
				if jumps > 10000 {
					return state, errors.New("too many jumps, possible infinite loop")
				}
				// setNextRequest(null) ends THIS iteration and lets the next
				// one start, matching Postman/newman. Escalating it to
				// break iterations would silently turn a per-iteration
				// early exit into a whole-run abort.
				if controls.NextRequestName == nil {
					break
				}
				nextRequestIndex := -1
				if index, ok := itemsByName[*controls.NextRequestName]; ok {
					nextRequestIndex = index
				}
				if nextRequestIndex >= 0 {
					if nextRequestIndex != currentRequestIndex {
						currentRequestIndex = nextRequestIndex
						if !sleepRunnerDelay(runContext, delayMs, 0, 2) {
							continue
						}
						continue
					}
					currentRequestIndex = nextRequestIndex
				} else {
					if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
						currentRequestIndex++
						continue
					}
					currentRequestIndex++
				}
				continue
			}
			if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
				currentRequestIndex++
				continue
			}
			currentRequestIndex++
		}
		completedIterations = iteration
	}
	if finishRun() || requestContextCancelled(runContext) {
		if len(results) == 0 || results[len(results)-1].Status != "cancelled" {
			results = append(results, RunResult{Name: "Collection run", Status: "cancelled", Error: "collection run cancelled", At: time.Now()})
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot := RunnerSnapshot{Total: len(results), Results: results, Finished: time.Now()}
	if totalIterations > 1 {
		snapshot.Iterations = totalIterations
		snapshot.CompletedIterations = completedIterations
	}
	for _, result := range results {
		switch result.Status {
		case "passed":
			snapshot.Passed++
		case "skipped":
			snapshot.Skipped++
		case "cancelled":
			snapshot.Cancelled++
		case "unrun":
			// US-047. Deliberately counted nowhere. An unrun request did not
			// pass, fail, get skipped or get cancelled — it never ran. Letting
			// it fall through to the default arm would inflate Failed, so a
			// bailed run of 50 requests would report 49 failures when one
			// request failed.
		default:
			snapshot.Failed++
		}
	}
	a.state.Runner = snapshot
	return a.state, a.markDirty(persistScopeState)
}

func filterRunnerItems(items []RequestItem, selectedItemIDs []string) []RequestItem {
	if selectedItemIDs == nil {
		return items
	}
	selected := map[string]bool{}
	for _, id := range selectedItemIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			selected[id] = true
		}
	}
	out := make([]RequestItem, 0, len(items))
	for _, item := range items {
		if selected[item.ID] {
			out = append(out, item)
		}
	}
	return out
}

// runnerIterationLimit caps a run at a size that stays workable.
//
// Every request of every iteration becomes a row in RunnerSnapshot.Results,
// which is persisted in state.json and rendered in full — so the cap is a
// memory and file-size bound, not an arbitrary number. 200 iterations of a
// 100-request collection is already 20,000 rows.
const runnerIterationLimit = 200

func normalizeRunnerIterations(iterations int) int {
	if iterations < 1 {
		return 1
	}
	if iterations > runnerIterationLimit {
		return runnerIterationLimit
	}
	return iterations
}

// stampIteration records which iteration a row belongs to.
//
// Only for multi-iteration runs: a single-iteration run leaves Iteration at
// zero so the field stays omitted and the JSON shape is unchanged for every
// consumer that never asked for iterations.
func stampIteration(result RunResult, iteration, totalIterations int) RunResult {
	if totalIterations > 1 {
		result.Iteration = iteration
	}
	return result
}

func normalizeRunnerDelayMs(delayMs int) int {
	if delayMs < 0 {
		return 0
	}
	if delayMs > 600000 {
		return 600000
	}
	return delayMs
}

func cancelledRunResult(item RequestItem) RunResult {
	return RunResult{ItemID: item.ID, Name: item.Name, Status: "cancelled", Error: "collection run cancelled", At: time.Now()}
}

func sleepRunnerDelay(ctx context.Context, delayMs, currentIndex, total int) bool {
	if delayMs <= 0 || currentIndex+1 >= total {
		return !requestContextCancelled(ctx)
	}
	timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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

func normalizePreferences(preferences Preferences) Preferences {
	preferences.Theme = normalizeThemeMode(preferences.Theme)
	preferences.ThemeVariantLight = normalizeThemeVariant(preferences.ThemeVariantLight, "light")
	preferences.ThemeVariantDark = normalizeThemeVariant(preferences.ThemeVariantDark, "dark")
	if preferences.KeybindingsEnabled == nil {
		preferences.KeybindingsEnabled = boolPtr(true)
	}
	preferences.KeyBindings = normalizeKeyBindings(preferences.KeyBindings)
	preferences.KeyBindingPreset = normalizeKeyBindingPreset(preferences.KeyBindingPreset)
	if preferences.CodeFontSize <= 0 {
		preferences.CodeFontSize = 13
	}
	preferences.Layout = normalizeLayoutPreferences(preferences.Layout)
	preferences.Display = normalizeDisplayPreferences(preferences.Display)
	preferences.Font = normalizeFontPreferences(preferences.Font, preferences.CodeFontSize)
	preferences.CodeFontSize = preferences.Font.CodeFontSize
	preferences.Request = normalizeRequestPreferences(preferences.Request, preferences.StoreCookies)
	preferences.StoreCookies = boolPtrValue(preferences.Request.StoreCookies, true)
	preferences.General = normalizeGeneralPreferences(preferences.General, preferences.DefaultCollectionPath)
	preferences.DefaultCollectionPath = preferences.General.DefaultLocation
	preferences.AutoSave = normalizeAutoSavePreferences(preferences.AutoSave, preferences.Autosave)
	preferences.Autosave = preferences.AutoSave.Enabled
	preferences.Cache = normalizeCachePreferences(preferences.Cache)
	preferences.DevTools = normalizeDevToolsPreferences(preferences.DevTools)
	preferences.Proxy = normalizeProxyPreferences(preferences.Proxy, preferences.ProxyMode)
	preferences.ProxyMode = preferenceProxyMode(preferences.Proxy)
	return preferences
}

func normalizeLayoutPreferences(preferences LayoutPreferences) LayoutPreferences {
	switch preferences.ResponsePaneOrientation {
	case "horizontal", "vertical":
	default:
		preferences.ResponsePaneOrientation = "horizontal"
	}
	return preferences
}

func normalizeDisplayPreferences(preferences DisplayPreferences) DisplayPreferences {
	if preferences.ZoomPercentage == 0 {
		preferences.ZoomPercentage = 100
	}
	if preferences.ZoomPercentage < 50 {
		preferences.ZoomPercentage = 50
	}
	if preferences.ZoomPercentage > 150 {
		preferences.ZoomPercentage = 150
	}
	return preferences
}

func normalizeFontPreferences(preferences FontPreferences, legacyCodeFontSize int) FontPreferences {
	preferences.CodeFont = strings.TrimSpace(preferences.CodeFont)
	if preferences.CodeFont == "" {
		preferences.CodeFont = "default"
	}
	if preferences.CodeFontSize == 0 && legacyCodeFontSize > 0 {
		preferences.CodeFontSize = legacyCodeFontSize
	}
	if preferences.CodeFontSize == 0 {
		preferences.CodeFontSize = 13
	}
	if preferences.CodeFontSize < 1 {
		preferences.CodeFontSize = 1
	}
	if preferences.CodeFontSize > 32 {
		preferences.CodeFontSize = 32
	}
	return preferences
}

func normalizeRequestPreferences(preferences RequestPreferences, legacyStoreCookies bool) RequestPreferences {
	if preferences.SSLVerification == nil {
		preferences.SSLVerification = boolPtr(true)
	}
	if preferences.StoreCookies == nil {
		preferences.StoreCookies = boolPtr(legacyStoreCookies)
	}
	if preferences.SendCookies == nil {
		preferences.SendCookies = boolPtr(true)
	}
	if preferences.Timeout < 0 {
		preferences.Timeout = 0
	}
	preferences.CustomCaCertificate.FilePath = strings.TrimSpace(preferences.CustomCaCertificate.FilePath)
	if preferences.KeepDefaultCaCertificates.Enabled == nil {
		preferences.KeepDefaultCaCertificates.Enabled = boolPtr(true)
	}
	return preferences
}

func normalizeGeneralPreferences(preferences GeneralPreferences, legacyDefaultCollectionPath string) GeneralPreferences {
	preferences.DefaultLocation = strings.TrimSpace(preferences.DefaultLocation)
	if preferences.DefaultLocation == "" {
		preferences.DefaultLocation = strings.TrimSpace(legacyDefaultCollectionPath)
	}
	if len(preferences.DefaultLocation) > 1024 {
		preferences.DefaultLocation = preferences.DefaultLocation[:1024]
	}
	preferences.DefaultWorkspacePath = strings.TrimSpace(preferences.DefaultWorkspacePath)
	if len(preferences.DefaultWorkspacePath) > 1024 {
		preferences.DefaultWorkspacePath = preferences.DefaultWorkspacePath[:1024]
	}
	preferences.LastImportDirectory = normalizeCollectionImportDirectory(preferences.LastImportDirectory)
	return preferences
}

func normalizeAutoSavePreferences(preferences AutoSavePreferences, legacyEnabled bool) AutoSavePreferences {
	if legacyEnabled && !preferences.Enabled {
		preferences.Enabled = true
	}
	if preferences.Interval == 0 {
		preferences.Interval = 1000
	}
	if preferences.Interval < 500 {
		preferences.Interval = 500
	}
	return preferences
}

func normalizeCachePreferences(preferences CachePreferences) CachePreferences {
	return preferences
}

func normalizeDevToolsPreferences(preferences DevToolsPreferences) DevToolsPreferences {
	if !devToolsTabs[preferences.ActiveTab] {
		preferences.ActiveTab = "console"
	}
	if preferences.DrawerHeight <= 0 {
		preferences.DrawerHeight = devToolsDefaultDrawerHeight
	}
	if preferences.DrawerHeight < 220 {
		preferences.DrawerHeight = 220
	}
	if preferences.DrawerHeight > 720 {
		preferences.DrawerHeight = 720
	}
	if preferences.DetailsPanelWidth <= 0 {
		preferences.DetailsPanelWidth = 400
	}
	if preferences.DetailsPanelWidth < 280 {
		preferences.DetailsPanelWidth = 280
	}
	if preferences.DetailsPanelWidth > 800 {
		preferences.DetailsPanelWidth = 800
	}
	preferences.Network = normalizeDevToolsNetworkPreferences(preferences.Network)
	return preferences
}

func normalizeDevToolsNetworkPreferences(preferences DevToolsNetworkPreferences) DevToolsNetworkPreferences {
	if !devToolsNetworkSortKeys[preferences.SortKey] {
		preferences.SortKey = ""
	}
	switch preferences.SortDirection {
	case "asc", "desc":
	default:
		preferences.SortDirection = ""
	}
	if preferences.SortKey == "" {
		preferences.SortDirection = ""
	}
	if preferences.SortDirection == "" {
		preferences.SortKey = ""
	}
	preferences.ColumnWidths = normalizeDevToolsNetworkColumnWidths(preferences.ColumnWidths)
	return preferences
}

func normalizeDevToolsNetworkColumnWidths(widths []int) []int {
	if len(widths) != len(devToolsNetworkDefaultColumnWidths) {
		return append([]int(nil), devToolsNetworkDefaultColumnWidths...)
	}
	normalized := make([]int, len(widths))
	for i, width := range widths {
		if width < 60 {
			width = 60
		}
		normalized[i] = width
	}
	return normalized
}

func boolPtr(value bool) *bool {
	return &value
}

func boolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func normalizeThemeMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "light", "dark", "system":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "system"
	}
}

func normalizeThemeVariant(value, mode string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "light":
		switch value {
		case "light", "light-monochrome", "light-pastel", "vscode-light", "catppuccin-latte":
			return value
		default:
			return "light"
		}
	case "dark":
		switch value {
		case "dark", "dark-monochrome", "dark-pastel", "vscode-dark", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha", "nord":
			return value
		default:
			return "dark"
		}
	default:
		return value
	}
}

// normalizeKeyBindingPreset rejects anything but a known preset id.
//
// An unknown id is coerced to "default" rather than preserved: a preset name
// this build does not recognise would otherwise be stored, resolve to no
// overrides, and leave the user looking at a selector showing a preset that
// is not in effect.
func normalizeKeyBindingPreset(value string) string {
	if strings.TrimSpace(strings.ToLower(value)) == "postman" {
		return "postman"
	}
	return ""
}

func normalizeKeyBindings(bindings map[string]KeyBinding) map[string]KeyBinding {
	if len(bindings) == 0 {
		return nil
	}
	known := defaultKeyBindingActions()
	normalized := map[string]KeyBinding{}
	for action, binding := range bindings {
		action = strings.TrimSpace(action)
		if !known[action] {
			continue
		}
		next := KeyBinding{Name: strings.TrimSpace(binding.Name)}
		if next.Name == "" {
			next.Name = knownKeyBindingName(action)
		}
		next.Mac = normalizeKeyBindingCombo(binding.Mac)
		next.Windows = normalizeKeyBindingCombo(binding.Windows)
		if next.Mac == "" && next.Windows == "" {
			continue
		}
		normalized[action] = next
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeKeyBindingCombo(value string) string {
	parts := []string{}
	for _, part := range strings.Split(strings.TrimSpace(strings.ToLower(value)), "+bind+") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !validKeyBindingToken(part) {
			return ""
		}
		parts = append(parts, part)
	}
	if len(parts) < 2 || len(parts) > 4 {
		return ""
	}
	nonModifiers := 0
	hasModifier := false
	for _, part := range parts {
		if isKeyBindingModifier(part) {
			hasModifier = true
			continue
		}
		nonModifiers++
	}
	if !hasModifier || nonModifiers != 1 {
		return ""
	}
	return strings.Join(parts, "+bind+")
}

func isKeyBindingModifier(value string) bool {
	switch value {
	case "ctrl", "command", "alt", "shift":
		return true
	default:
		return false
	}
}

func validKeyBindingToken(value string) bool {
	if isKeyBindingModifier(value) {
		return true
	}
	if len(value) == 1 {
		return (value >= "a" && value <= "z") || (value >= "0" && value <= "9") || strings.Contains("[]\\,.-=/", value)
	}
	switch value {
	case "enter", "backspace", "tab", "delete", "esc", "space", "arrowup", "arrowdown", "arrowleft", "arrowright", "pageup", "pagedown", "home", "end":
		return true
	}
	if strings.HasPrefix(value, "f") {
		number, err := strconv.Atoi(strings.TrimPrefix(value, "f"))
		return err == nil && number >= 1 && number <= 12
	}
	return false
}

func defaultKeyBindingActions() map[string]bool {
	return map[string]bool{
		"closeTab":              true,
		"closeAllTabs":          true,
		"save":                  true,
		"saveAllTabs":           true,
		"reopenLastClosedTab":   true,
		"switchToTabAtPosition": true,
		"switchToLastTab":       true,
		"switchToPreviousTab":   true,
		"switchToNextTab":       true,
		"moveTabLeft":           true,
		"moveTabRight":          true,
		"switchToTab1":          true,
		"switchToTab2":          true,
		"switchToTab3":          true,
		"switchToTab4":          true,
		"switchToTab5":          true,
		"switchToTab6":          true,
		"switchToTab7":          true,
		"switchToTab8":          true,
		"sidebarSearch":         true,
		"copyItem":              true,
		"pasteItem":             true,
		"cloneItem":             true,
		"renameItem":            true,
		"collapseSidebar":       true,
		"sendRequest":           true,
		"changeLayout":          true,
		"importCollection":      true,
		"editEnvironment":       true,
		"newRequest":            true,
		"globalSearch":          true,
		"commandPalette":        true,
		"zoomIn":                true,
		"zoomOut":               true,
		"resetZoom":             true,
		"openTerminal":          true,
		"openPreferences":       true,
		"closeBruno":            true,
	}
}

func knownKeyBindingName(action string) string {
	switch action {
	case "sendRequest":
		return "Send Request"
	case "globalSearch":
		return "Global Search"
	case "commandPalette":
		return "Command Palette"
	case "sidebarSearch":
		return "Search Sidebar"
	case "newRequest":
		return "New Request"
	case "save":
		return "Save"
	default:
		return action
	}
}

func defaultProxyPreferences() ProxyPreferences {
	return ProxyPreferences{
		Source: "inherit",
		PAC:    ProxyPACConfig{},
		Config: transport.NormalizeProxyConfig(ProxyConfig{Protocol: "http"}),
	}
}

func normalizeProxyPreferences(proxy ProxyPreferences, legacyMode string) ProxyPreferences {
	source := strings.ToLower(strings.TrimSpace(proxy.Source))
	if source == "" {
		switch strings.ToLower(strings.TrimSpace(legacyMode)) {
		case "off", "disabled":
			proxy.Disabled = true
			source = "manual"
		case "on", "manual":
			source = "manual"
		case "pac":
			source = "pac"
		default:
			source = "inherit"
		}
	}
	switch source {
	case "manual", "pac", "inherit":
		proxy.Source = source
	default:
		proxy.Source = "inherit"
	}
	proxy.PAC.Source = strings.TrimSpace(proxy.PAC.Source)
	proxy.Config = transport.NormalizeProxyConfig(proxy.Config)
	proxy.Config.Inherit = false
	proxy.Config.Disabled = false
	return proxy
}

func preferenceProxyMode(proxy ProxyPreferences) string {
	proxy = normalizeProxyPreferences(proxy, "")
	if proxy.Disabled {
		return "off"
	}
	switch proxy.Source {
	case "manual":
		return "manual"
	case "pac":
		return "pac"
	default:
		return "system"
	}
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

func (a *App) environmentSecretsPath() string {
	return filepath.Join(a.dataDir, "secrets.json")
}

func (a *App) readEnvironmentSecretsLocked() (environmentSecretsFile, error) {
	path := a.environmentSecretsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return environmentSecretsFile{}, nil
		}
		return environmentSecretsFile{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return environmentSecretsFile{}, nil
	}
	var store environmentSecretsFile
	if err := json.Unmarshal(data, &store); err != nil {
		return environmentSecretsFile{}, fmt.Errorf("parse secrets.json: %w", err)
	}
	if store.Collections == nil {
		store.Collections = []environmentSecretCollection{}
	}
	return store, nil
}

func (a *App) writeEnvironmentSecretsLocked(store environmentSecretsFile) error {
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	if store.Collections == nil {
		store.Collections = []environmentSecretCollection{}
	}
	if store.Workspaces == nil {
		store.Workspaces = []environmentSecretWorkspace{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	path := a.environmentSecretsPath()
	// US-013. Skip unchanged content. persistEnvironmentSecretsLocked runs on
	// the keystroke path, where secrets almost never change.
	//
	// The gate is an in-memory fingerprint of what this App last wrote, not a
	// re-read of the file, so the common case costs a hash rather than a read
	// plus a compare. The file is still read once — when the fingerprint is
	// empty, i.e. before this App has written it — so a process that starts up
	// and persists without touching a secret still does not rewrite the file.
	//
	// Skipping is SAFE UNDER MULTIPLE WINDOWS, and the direction matters: if
	// another window has rewritten secrets.json since our last write, our
	// fingerprint still describes content identical to what we would produce,
	// so skipping leaves their newer file intact. Writing is the operation that
	// could clobber; declining to write cannot.
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
	if a.secretsFingerprint != "" {
		if fingerprint == a.secretsFingerprint {
			return nil
		}
	} else if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, data) {
		a.secretsFingerprint = fingerprint
		return nil
	}
	// Atomic for the same reason state.json is: a half-written secrets.json is
	// every environment secret in the workspace, unrecoverable.
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		// Leave the fingerprint alone on failure. Recording it here would make
		// the next call skip a write that never landed.
		return err
	}
	a.secretsFingerprint = fingerprint
	return nil
}

func (a *App) prepareWorkspaceGlobalEnvironmentsLocked() (bool, error) {
	changed := false
	if len(a.state.Workspaces) == 0 {
		return false, nil
	}
	if len(a.state.GlobalEnvironments) > 0 {
		ws, err := a.findWorkspaceLocked(a.state.ActiveWorkspaceID)
		if err != nil {
			ws = &a.state.Workspaces[0]
		}
		if len(ws.GlobalEnvironments) == 0 {
			ws.GlobalEnvironments = append([]Environment(nil), a.state.GlobalEnvironments...)
			if ws.ActiveGlobalEnvironmentID == "" && len(ws.GlobalEnvironments) > 0 {
				ws.ActiveGlobalEnvironmentID = ws.GlobalEnvironments[0].ID
			}
		}
		a.state.GlobalEnvironments = nil
		changed = true
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		if strings.TrimSpace(ws.Path) != "" {
			loaded, err := readWorkspaceGlobalEnvironments(ws.Path)
			if err != nil {
				return changed, err
			}
			if len(loaded) > 0 {
				ws.GlobalEnvironments = bru.MergeEnvironments(ws.GlobalEnvironments, loaded)
			}
			if ws.ActiveGlobalEnvironmentID == "" || !scripting.WorkspaceHasGlobalEnvironment(*ws, ws.ActiveGlobalEnvironmentID) {
				migrated, err := migrateWorkspaceActiveGlobalEnvironmentFromConfig(ws)
				if err != nil {
					return changed, err
				}
				if migrated {
					changed = true
				}
			}
		}
		if !scripting.WorkspaceHasGlobalEnvironment(*ws, ws.ActiveGlobalEnvironmentID) {
			ws.ActiveGlobalEnvironmentID = ""
			changed = true
		}
	}
	return changed, nil
}

func (a *App) storeStateEnvironmentSecretsLocked() error {
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	for wi := range a.state.Workspaces {
		upsertWorkspaceEnvironmentSecrets(&store, a.dataDir, &a.state.Workspaces[wi])
		for ci := range a.state.Workspaces[wi].Collections {
			upsertCollectionEnvironmentSecrets(&store, a.dataDir, &a.state.Workspaces[wi].Collections[ci])
		}
	}
	return a.writeEnvironmentSecretsLocked(store)
}

func (a *App) storeCollectionEnvironmentSecretsLocked(collection *Collection) error {
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	upsertCollectionEnvironmentSecrets(&store, a.dataDir, collection)
	return a.writeEnvironmentSecretsLocked(store)
}

func (a *App) hydrateStateEnvironmentSecretsLocked() error {
	for wi := range a.state.Workspaces {
		if err := a.hydrateWorkspaceEnvironmentSecretsLocked(&a.state.Workspaces[wi]); err != nil {
			return err
		}
		for ci := range a.state.Workspaces[wi].Collections {
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&a.state.Workspaces[wi].Collections[ci]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) hydrateWorkspaceEnvironmentSecretsLocked(workspace *Workspace) error {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" || len(workspace.GlobalEnvironments) == 0 {
		return nil
	}
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	workspacePath := normalizedEnvironmentSecretPath(workspace.Path)
	var stored *environmentSecretWorkspace
	for index := range store.Workspaces {
		if store.Workspaces[index].Path == workspacePath {
			stored = &store.Workspaces[index]
			break
		}
	}
	if stored == nil {
		return nil
	}
	hydrateEnvironmentSecrets(a.dataDir, workspace.GlobalEnvironments, stored.Environments)
	return nil
}

func (a *App) hydrateCollectionEnvironmentSecretsLocked(collection *Collection) error {
	if collection == nil || strings.TrimSpace(collection.Path) == "" || len(collection.Environments) == 0 {
		return nil
	}
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	collectionPath := normalizedEnvironmentSecretPath(collection.Path)
	var stored *environmentSecretCollection
	for index := range store.Collections {
		if store.Collections[index].Path == collectionPath {
			stored = &store.Collections[index]
			break
		}
	}
	if stored == nil {
		return nil
	}
	hydrateEnvironmentSecrets(a.dataDir, collection.Environments, stored.Environments)
	return nil
}

func hydrateEnvironmentSecrets(dataDir string, environments []Environment, storedEnvironments []environmentSecretEnvironment) {
	for envIndex := range environments {
		env := &environments[envIndex]
		var storedEnv *environmentSecretEnvironment
		for index := range storedEnvironments {
			if storedEnvironments[index].Name == env.Name {
				storedEnv = &storedEnvironments[index]
				break
			}
		}
		if storedEnv == nil {
			continue
		}
		secretValues := map[string]string{}
		for _, secret := range storedEnv.Secrets {
			secretValues[secret.Name] = secret.Value
		}
		for variableIndex := range env.Variables {
			variable := &env.Variables[variableIndex]
			if !variable.Secret || variable.Name == "" {
				continue
			}
			encoded, ok := secretValues[variable.Name]
			if !ok {
				continue
			}
			plain, err := decryptEnvironmentSecretString(dataDir, encoded)
			if err != nil {
				continue
			}
			variable.Value = parseEnvironmentSecretValue(plain, firstNonEmpty(variable.DataType, variable.Type, "string"))
		}
	}
}

func upsertWorkspaceEnvironmentSecrets(store *environmentSecretsFile, dataDir string, workspace *Workspace) {
	if store == nil || workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return
	}
	workspacePath := normalizedEnvironmentSecretPath(workspace.Path)
	workspaceIndex := -1
	for index := range store.Workspaces {
		if store.Workspaces[index].Path == workspacePath {
			workspaceIndex = index
			break
		}
	}
	nextEnvironments := environmentSecretsForStorageList(dataDir, workspace.GlobalEnvironments)
	if len(nextEnvironments) == 0 {
		if workspaceIndex >= 0 {
			store.Workspaces = append(store.Workspaces[:workspaceIndex], store.Workspaces[workspaceIndex+1:]...)
		}
		return
	}
	entry := environmentSecretWorkspace{Path: workspacePath, Environments: nextEnvironments}
	if workspaceIndex >= 0 {
		store.Workspaces[workspaceIndex] = entry
		return
	}
	store.Workspaces = append(store.Workspaces, entry)
}

func upsertCollectionEnvironmentSecrets(store *environmentSecretsFile, dataDir string, collection *Collection) {
	if store == nil || collection == nil || strings.TrimSpace(collection.Path) == "" {
		return
	}
	collectionPath := normalizedEnvironmentSecretPath(collection.Path)
	collectionIndex := -1
	for index := range store.Collections {
		if store.Collections[index].Path == collectionPath {
			collectionIndex = index
			break
		}
	}
	nextEnvironments := environmentSecretsForStorageList(dataDir, collection.Environments)
	if len(nextEnvironments) == 0 {
		if collectionIndex >= 0 {
			store.Collections = append(store.Collections[:collectionIndex], store.Collections[collectionIndex+1:]...)
		}
		return
	}
	entry := environmentSecretCollection{Path: collectionPath, Environments: nextEnvironments}
	if collectionIndex >= 0 {
		store.Collections[collectionIndex] = entry
		return
	}
	store.Collections = append(store.Collections, entry)
}

func environmentSecretsForStorageList(dataDir string, environments []Environment) []environmentSecretEnvironment {
	nextEnvironments := make([]environmentSecretEnvironment, 0, len(environments))
	for _, env := range environments {
		secrets := environmentSecretsForStorage(dataDir, env)
		if len(secrets) == 0 {
			continue
		}
		nextEnvironments = append(nextEnvironments, environmentSecretEnvironment{Name: env.Name, Secrets: secrets})
	}
	return nextEnvironments
}

func environmentSecretsForStorage(dataDir string, env Environment) []environmentSecretVariable {
	secrets := []environmentSecretVariable{}
	for _, variable := range env.Variables {
		if !variable.Secret || strings.TrimSpace(variable.Name) == "" {
			continue
		}
		secrets = append(secrets, environmentSecretVariable{
			Name:  variable.Name,
			Value: encryptEnvironmentSecretString(dataDir, environmentSecretValueToString(variable.Value)),
		})
	}
	return secrets
}

func normalizedEnvironmentSecretPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func environmentSecretValueToString(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		if reflect.TypeOf(value) != nil {
			kind := reflect.TypeOf(value).Kind()
			if kind == reflect.Map || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Struct {
				if data, err := json.Marshal(value); err == nil {
					return string(data)
				}
			}
		}
		return fmt.Sprint(value)
	}
}

func parseEnvironmentSecretValue(value, dataType string) interface{} {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "number":
		if strings.ContainsAny(value, ".eE") {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				return parsed
			}
		}
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	case "boolean":
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return value
}

func encryptEnvironmentSecretString(dataDir, plain string) string {
	block, err := aes.NewCipher(environmentSecretAESKey(dataDir))
	if err != nil {
		return plain
	}
	padded := scripting.ScriptPKCS7Pad([]byte(plain), block.BlockSize())
	encrypted := make([]byte, len(padded))
	iv := make([]byte, block.BlockSize())
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	return "$01:" + hex.EncodeToString(encrypted)
}

func decryptEnvironmentSecretString(dataDir, encoded string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, "$") {
		return encoded, nil
	}
	algorithm, payload, ok := strings.Cut(encoded, ":")
	if !ok {
		return "", errors.New("invalid encrypted secret")
	}
	if algorithm == "$00" {
		return "", nil
	}
	if algorithm != "$01" {
		return "", fmt.Errorf("unsupported encrypted secret algorithm %s", algorithm)
	}
	raw, err := hex.DecodeString(payload)
	if err != nil {
		return "", err
	}
	plain, err := decryptEnvironmentSecretAES256(raw, environmentSecretAESKey(dataDir), make([]byte, aes.BlockSize))
	if err == nil {
		return plain, nil
	}
	key, iv := environmentSecretLegacyAESKeyAndIV(environmentSecretRawKey(dataDir))
	return decryptEnvironmentSecretAES256(raw, key, iv)
}

func decryptEnvironmentSecretAES256(raw, key, iv []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		return "", errors.New("invalid encrypted secret payload")
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw)
	unpadded, err := scripting.ScriptPKCS7Unpad(plain, block.BlockSize())
	if err != nil {
		return "", err
	}
	return string(unpadded), nil
}

func environmentSecretLegacyAESKeyAndIV(password string) ([]byte, []byte) {
	derived := []byte{}
	var previous []byte
	for len(derived) < 48 {
		hasher := md5.New()
		if len(previous) > 0 {
			_, _ = hasher.Write(previous)
		}
		_, _ = hasher.Write([]byte(password))
		previous = hasher.Sum(nil)
		derived = append(derived, previous...)
	}
	return derived[:32], derived[32:48]
}

func environmentSecretAESKey(dataDir string) []byte {
	sum := sha256.Sum256([]byte(environmentSecretRawKey(dataDir)))
	return sum[:]
}

func environmentSecretRawKey(dataDir string) string {
	if key := strings.TrimSpace(os.Getenv("LITEAPI_SECRET_KEY")); key != "" {
		return key
	}
	if id := localMachineID(); id != "" {
		return id
	}
	if strings.TrimSpace(dataDir) != "" {
		return filepath.Clean(dataDir)
	}
	return "LiteAPI"
}

func localMachineID() string {
	environmentSecretMachineIDOnce.Do(func() {
		switch goruntime.GOOS {
		case "darwin":
			output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
			if err == nil {
				matches := regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`).FindStringSubmatch(string(output))
				if len(matches) == 2 {
					environmentSecretMachineIDValue = strings.TrimSpace(matches[1])
				}
			}
		case "linux":
			for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
				data, err := os.ReadFile(path)
				if err == nil && strings.TrimSpace(string(data)) != "" {
					environmentSecretMachineIDValue = strings.TrimSpace(string(data))
					break
				}
			}
		case "windows":
			output, err := exec.Command("wmic", "csproduct", "get", "uuid").Output()
			if err == nil {
				for _, line := range strings.Split(string(output), "\n") {
					line = strings.TrimSpace(line)
					if line != "" && !strings.EqualFold(line, "UUID") {
						environmentSecretMachineIDValue = line
						break
					}
				}
			}
		}
	})
	return environmentSecretMachineIDValue
}

func stateForStorage(state AppState, dataDir string) AppState {
	scrubbed := stateWithoutCollectionEnvironmentSecrets(state)
	scrubbed = stateWithoutScratchCollections(scrubbed)
	scrubbed.Cookies = encryptCookieValuesForStorage(dataDir, scrubbed.Cookies)
	// See AppState.Revision: the counter is per-instance, so it must not
	// survive a restart or cross between windows via the shared state file.
	scrubbed.Revision = 0
	return scrubbed
}

func stateWithoutScratchCollections(state AppState) AppState {
	if len(state.Workspaces) == 0 {
		return state
	}
	scratchIDs := map[string]bool{}
	state.Workspaces = append([]Workspace(nil), state.Workspaces...)
	for wi := range state.Workspaces {
		workspace := &state.Workspaces[wi]
		if workspace.ScratchCollectionID != "" {
			scratchIDs[workspace.ScratchCollectionID] = true
		}
		nextCollections := make([]Collection, 0, len(workspace.Collections))
		for _, collection := range workspace.Collections {
			if collection.Scratch {
				scratchIDs[collection.ID] = true
				continue
			}
			nextCollections = append(nextCollections, collection)
		}
		workspace.Collections = nextCollections
		workspace.ScratchCollectionID = ""
		workspace.ScratchTempDirectory = ""
	}
	if len(state.OpenTabs) > 0 {
		nextTabs := make([]OpenTab, 0, len(state.OpenTabs))
		for _, tab := range state.OpenTabs {
			if tab.Transient || scratchIDs[tab.CollectionID] {
				continue
			}
			nextTabs = append(nextTabs, tab)
		}
		state.OpenTabs = nextTabs
		if len(state.OpenTabs) == 0 {
			state.ActiveTabID = ""
		} else {
			activeStillPresent := false
			for _, tab := range state.OpenTabs {
				if tab.ID == state.ActiveTabID {
					activeStillPresent = true
					break
				}
			}
			if !activeStillPresent {
				state.ActiveTabID = state.OpenTabs[len(state.OpenTabs)-1].ID
			}
		}
	}
	if len(state.ClosedTabs) > 0 {
		nextClosedTabs := make([]OpenTab, 0, len(state.ClosedTabs))
		for _, tab := range state.ClosedTabs {
			if tab.Transient || scratchIDs[tab.CollectionID] {
				continue
			}
			nextClosedTabs = append(nextClosedTabs, tab)
		}
		state.ClosedTabs = nextClosedTabs
	}
	return state
}

func stateWithoutCollectionEnvironmentSecrets(state AppState) AppState {
	scrubbed := state
	if len(state.Workspaces) == 0 {
		return scrubbed
	}
	scrubbed.Workspaces = append([]Workspace(nil), state.Workspaces...)
	for wi := range scrubbed.Workspaces {
		scrubbed.Workspaces[wi] = workspaceWithoutEnvironmentSecrets(scrubbed.Workspaces[wi])
		if len(state.Workspaces[wi].Collections) == 0 {
			continue
		}
		scrubbed.Workspaces[wi].Collections = append([]Collection(nil), state.Workspaces[wi].Collections...)
		for ci := range scrubbed.Workspaces[wi].Collections {
			scrubbed.Workspaces[wi].Collections[ci] = collectionWithoutEnvironmentSecrets(scrubbed.Workspaces[wi].Collections[ci])
		}
	}
	return scrubbed
}

func workspaceWithoutEnvironmentSecrets(workspace Workspace) Workspace {
	if len(workspace.GlobalEnvironments) == 0 {
		return workspace
	}
	workspace.GlobalEnvironments = scrubEnvironmentSecretValues(workspace.GlobalEnvironments)
	return workspace
}

func collectionWithoutEnvironmentSecrets(collection Collection) Collection {
	if len(collection.Environments) == 0 {
		return collection
	}
	collection.Environments = scrubEnvironmentSecretValues(collection.Environments)
	return collection
}

func encryptCookieValuesForStorage(dataDir string, cookies []CookieEntry) []CookieEntry {
	if len(cookies) == 0 {
		return cookies
	}
	encrypted := append([]CookieEntry(nil), cookies...)
	for index := range encrypted {
		if encrypted[index].Value == "" {
			continue
		}
		encrypted[index].Value = encryptEnvironmentSecretString(dataDir, encrypted[index].Value)
	}
	return encrypted
}

func decryptCookieValuesForRuntime(dataDir string, cookies []CookieEntry) []CookieEntry {
	if len(cookies) == 0 {
		return cookies
	}
	decrypted := append([]CookieEntry(nil), cookies...)
	for index := range decrypted {
		if !strings.HasPrefix(strings.TrimSpace(decrypted[index].Value), "$") {
			continue
		}
		value, err := decryptEnvironmentSecretString(dataDir, decrypted[index].Value)
		if err == nil {
			decrypted[index].Value = value
		}
	}
	return decrypted
}

func (a *App) executeHTTP(ctx context.Context, collectionID string, collection Collection, item RequestItem, vars map[string]string, onFailState *scripting.RequestState, recordTimeline func(TimelineItem)) Response {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	result := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "auto"}
	if item.Type == "websocket" {
		return a.executeWebSocket(collectionID, item, vars)
	}
	if item.Type == "grpc" {
		response := a.executeGRPC(ctx, collection, item, vars)
		if requestContextCancelled(ctx) {
			markRequestCancelled(&response)
		}
		return response
	}
	if item.Type != "http" && item.Type != "graphql" {
		result.Error = item.Type + " execution is registered for UI parity but not wired to a protocol client yet"
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	targetURL := codegen.RequestURLWithParams(item.URL, item.Params, item.PathParams, vars)
	if item.Settings.EncodeURL {
		targetURL = scripting.EncodeRequestURL(targetURL)
	}
	result.RequestedURL = targetURL
	method := strings.ToUpper(item.Method)
	if method == "" {
		method = http.MethodGet
	}
	bodyReader, contentType, err := buildBody(item.Body, vars, collection.Path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if item.Type == "graphql" && bodyReader == nil {
		method = http.MethodPost
		payload := map[string]string{
			"query":     interpolate(item.Body.GraphQLQuery, vars),
			"variables": interpolate(item.Body.GraphQLVariables, vars),
		}
		b, _ := json.Marshal(payload)
		bodyReader = strings.NewReader(string(b))
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	timingTrace := newResponseTimingTrace(start)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), timingTrace.trace()))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, header := range item.Headers {
		if header.Enabled && header.Name != "" {
			req.Header.Set(interpolate(header.Name, vars), interpolate(header.Value, vars))
		}
	}
	if err := a.applyAuth(req, collection.Path, &item, vars, recordTimeline); err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	client := *a.httpClient
	redirectCookies := scripting.NewScriptCookieJar(nil)
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	tlsSettings := a.appTLSSettingsSnapshot()
	verifyTLS := requestTLSVerificationEnabled(tlsSettings.Request, item.Settings.VerifyTLS)
	// US-016: one shared transport per security posture, so sequential sends
	// reuse the connection instead of handshaking into a fresh empty pool.
	var transportErr error
	baseTransport, transportErr = a.requestTransport(baseTransport, tlsSettings, verifyTLS, collectionID, targetURL, vars)
	if transportErr != nil {
		result.Error = transportErr.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	if strings.EqualFold(item.Auth.Mode, "ntlm") {
		baseTransport = ntlmssp.Negotiator{RoundTripper: baseTransport}
	}
	client.Transport = cookieCapturingTransport{base: baseTransport, jar: redirectCookies}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, tlsSettings.Request)
	client.Timeout = time.Duration(timeout) * time.Millisecond
	if !item.Settings.FollowRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			timingTrace.redirect()
			return http.ErrUseLastResponse
		}
	} else if item.Settings.MaxRedirects > 0 {
		max := item.Settings.MaxRedirects
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			timingTrace.redirect()
			if len(via) >= max {
				return http.ErrUseLastResponse
			}
			attachCookiesToHTTPRequest(req, redirectCookies.Snapshot())
			return nil
		}
	} else {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			timingTrace.redirect()
			attachCookiesToHTTPRequest(req, redirectCookies.Snapshot())
			return nil
		}
	}
	res, err := client.Do(req)
	if err != nil {
		if requestContextCancelled(ctx) {
			markRequestCancelled(&result)
		} else {
			result.Error = err.Error()
		}
		if onFailErr := scripting.RunRequestOnFail(onFailState, err); onFailErr != nil {
			result.Error = result.Error + "; onFail: " + onFailErr.Error()
		}
		result.DurationMs = time.Since(start).Milliseconds()
		result.Timings = timingTrace.finalize(time.Now())
		return result
	}
	if digest.ShouldRetry(res, item.Auth) {
		challenge := res.Header.Get("WWW-Authenticate")
		_ = res.Body.Close()
		retryReq, retryErr := digest.CloneRequest(req, item.Auth, vars, challenge)
		if retryErr != nil {
			result.Error = retryErr.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			result.Timings = timingTrace.finalize(time.Now())
			return result
		}
		attachCookiesToHTTPRequest(retryReq, redirectCookies.Snapshot())
		res, err = client.Do(retryReq)
		if err != nil {
			if requestContextCancelled(ctx) {
				markRequestCancelled(&result)
			} else {
				result.Error = err.Error()
			}
			if onFailErr := scripting.RunRequestOnFail(onFailState, err); onFailErr != nil {
				result.Error = result.Error + "; onFail: " + onFailErr.Error()
			}
			result.DurationMs = time.Since(start).Milliseconds()
			result.Timings = timingTrace.finalize(time.Now())
			return result
		}
	}
	defer func() { _ = res.Body.Close() }()
	// US-011. Bounded, not io.ReadAll: the size of this allocation is chosen by
	// the remote server, so an endpoint streaming gigabytes would otherwise take
	// the process down with it.
	body, bodyTruncated, err := readResponseBodyLimited(res.Body, responseBodyReadLimit(a.appTLSSettingsSnapshot().Request))
	if err != nil {
		if requestContextCancelled(ctx) {
			markRequestCancelled(&result)
		} else {
			result.Error = err.Error()
		}
	}
	result.Status = res.StatusCode
	result.StatusText = res.Status
	if result.Cancelled {
		result.StatusText = "Cancelled"
	}
	result.Size = len(body)
	result.Body = string(body)
	// US-011: truncation must be visible. The header is what the response
	// inspector can surface without any new plumbing, and it travels with the
	// response into saved examples and exports, so a truncated body is never
	// mistaken for a complete one later.
	if bodyTruncated {
		if result.Headers == nil {
			result.Headers = map[string]string{}
		}
		result.Headers["x-liteapi-body-truncated"] = "true"
		result.Headers["x-liteapi-body-limit"] = strconv.FormatInt(responseBodyReadLimit(a.appTLSSettingsSnapshot().Request), 10)
	}
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Timings = timingTrace.finalize(time.Now())
	for name, values := range res.Header {
		result.Headers[name] = strings.Join(values, ", ")
		for _, value := range values {
			result.HeaderEntries = append(result.HeaderEntries, KeyValue{Name: name, Value: value, Enabled: true})
		}
	}
	result.Cookies = redirectCookies.Snapshot()
	result.PreviewMode = previewModeFromHeaders(result.Headers)
	result.Assertions = evaluateAssertions(item.Assertions, result)
	return result
}

type cookieCapturingTransport struct {
	base http.RoundTripper
	jar  *scripting.CookieJar
}

type appTLSSettings struct {
	Request            RequestPreferences
	ClientSessionCache tls.ClientSessionCache
}

// appTLSSettingsSnapshot runs once per outbound request, so it is on the
// hot path of a parallel collection run. Everything it needs is read-only
// except the lazy construction of the shared TLS session cache, so it uses
// double-checked locking: the fast path reads under RLock and returns; only
// the first call (or the first after ClearSSLSessionCache) drops to the write
// lock. The slow path re-reads preferences and re-tests tlsSessionCache
// because the lock is released between the two sections and another goroutine
// may have created the cache — or disabled it — in the gap.
func (a *App) appTLSSettingsSnapshot() appTLSSettings {
	a.mu.RLock()
	preferences := normalizePreferences(a.state.Preferences)
	if !preferences.Cache.SSLSession.Enabled {
		a.mu.RUnlock()
		return appTLSSettings{Request: preferences.Request}
	}
	if cache := a.tlsSessionCache; cache != nil {
		a.mu.RUnlock()
		return appTLSSettings{Request: preferences.Request, ClientSessionCache: cache}
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	preferences = normalizePreferences(a.state.Preferences)
	var clientSessionCache tls.ClientSessionCache
	if preferences.Cache.SSLSession.Enabled {
		if a.tlsSessionCache == nil {
			a.tlsSessionCache = tls.NewLRUClientSessionCache(100)
		}
		clientSessionCache = a.tlsSessionCache
	}
	return appTLSSettings{
		Request:            preferences.Request,
		ClientSessionCache: clientSessionCache,
	}
}

func requestTLSVerificationEnabled(preferences RequestPreferences, requestVerifyTLS bool) bool {
	return requestVerifyTLS && boolPtrValue(preferences.SSLVerification, true)
}

func requestTimeoutMilliseconds(itemTimeout int, preferences RequestPreferences) int {
	if preferences.Timeout > 0 {
		return preferences.Timeout
	}
	if itemTimeout > 0 {
		return itemTimeout
	}
	return 30000
}

func transportWithAppTLSSettings(base http.RoundTripper, settings appTLSSettings, verifyTLS bool) (http.RoundTripper, error) {
	// Keep the caller's shared transport for the normal verified path so HTTP
	// keep-alive reuse remains observable by httptrace.
	if verifyTLS && !settings.Request.CustomCaCertificate.Enabled {
		return base, nil
	}
	transport := transport.CloneHTTPTransport(base)
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	if !verifyTLS {
		tlsConfig.InsecureSkipVerify = true
	} else if err := applyCustomRootCAsToTLSConfig(tlsConfig, settings.Request); err != nil {
		return nil, err
	}
	if settings.ClientSessionCache != nil {
		tlsConfig.ClientSessionCache = settings.ClientSessionCache
	}
	transport.TLSClientConfig = tlsConfig
	return transport, nil
}

func applyCustomRootCAsToTLSConfig(tlsConfig *tls.Config, preferences RequestPreferences) error {
	if tlsConfig == nil || !preferences.CustomCaCertificate.Enabled {
		return nil
	}
	filePath := strings.TrimSpace(preferences.CustomCaCertificate.FilePath)
	if filePath == "" {
		return nil
	}
	certPEM, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read custom CA certificate: %w", err)
	}
	var roots *x509.CertPool
	if boolPtrValue(preferences.KeepDefaultCaCertificates.Enabled, true) {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("load system CA certificates: %w", err)
		}
	}
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(certPEM) {
		return fmt.Errorf("custom CA certificate %q did not contain PEM certificates", filePath)
	}
	tlsConfig.RootCAs = roots
	return nil
}

func tlsSessionPreferencesChanged(previous, next Preferences) bool {
	previous = normalizePreferences(previous)
	next = normalizePreferences(next)
	if previous.Cache.SSLSession.Enabled != next.Cache.SSLSession.Enabled {
		return true
	}
	if boolPtrValue(previous.Request.SSLVerification, true) != boolPtrValue(next.Request.SSLVerification, true) {
		return true
	}
	if previous.Request.CustomCaCertificate != next.Request.CustomCaCertificate {
		return true
	}
	if boolPtrValue(previous.Request.KeepDefaultCaCertificates.Enabled, true) != boolPtrValue(next.Request.KeepDefaultCaCertificates.Enabled, true) {
		return true
	}
	return false
}

// proxyResolution moved to internal/transport with the code that consumes it.
type proxyResolution = transport.Resolution

// Read-only: findCollectionLocked only walks state, and every normalize*
// helper below takes its argument by value and returns a new value. The
// returned proxyResolution holds ProxyConfig, whose fields are all strings and
// bools, so nothing in the result aliases live state.
func (a *App) collectionProxyResolution(collectionID string) proxyResolution {
	a.mu.RLock()
	defer a.mu.RUnlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return proxyResolution{Mode: "off"}
	}
	proxy := transport.NormalizeProxyConfig(collection.Proxy)
	if proxy.Disabled {
		return proxyResolution{Mode: "off"}
	}
	if !proxy.Inherit && !transport.ProxyConfigUnset(collection.Proxy) {
		return proxyResolution{Mode: "manual", Config: proxy}
	}
	preferences := normalizeProxyPreferences(a.state.Preferences.Proxy, a.state.Preferences.ProxyMode)
	if preferences.Disabled {
		return proxyResolution{Mode: "off"}
	}
	switch preferences.Source {
	case "manual":
		return proxyResolution{Mode: "manual", Config: transport.NormalizeProxyConfig(preferences.Config)}
	case "pac":
		return proxyResolution{Mode: "pac", PACSource: preferences.PAC.Source}
	default:
		return proxyResolution{Mode: "system"}
	}
}

// Read-only: the returned slice is freshly allocated and
// ClientCertificateConfig contains only strings, so the copy is deep.
func (a *App) collectionClientCertificateConfig(collectionID string) (string, []ClientCertificateConfig, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil || len(collection.ClientCertificates) == 0 {
		return "", nil, false
	}
	return collection.Path, append([]ClientCertificateConfig(nil), collection.ClientCertificates...), true
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

// grpcMethodBinding and grpcDialConfig moved to internal/grpcexec.
type grpcMethodBinding = grpcexec.MethodBinding
type grpcDialConfig = grpcexec.DialConfig

func (a *App) executeGRPC(parent context.Context, collection Collection, item RequestItem, vars map[string]string) Response {
	start := time.Now()
	result := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "json"}
	targetURL := interpolate(item.URL, vars)
	result.RequestedURL = targetURL

	dialConfig, err := a.grpcDialConfigForRequest(collection, item, targetURL, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	defer func() { _ = conn.Close() }()

	ctx, err = grpcexec.OutgoingContext(ctx, item, vars, a.fetchOAuth2Token)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	binding, err := grpcexec.ResolveMethod(ctx, conn, item, collection, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	if binding.Descriptor.IsStreamingClient() || binding.Descriptor.IsStreamingServer() {
		executeGRPCStream(&result, conn, binding, item, vars, ctx, start)
		return result
	}

	req := dynamicpb.NewMessage(binding.Descriptor.Input())
	content := grpcexec.RequestContent(item, vars)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(content), req); err != nil {
		result.Error = "parse gRPC request JSON: " + err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	res := dynamicpb.NewMessage(binding.Descriptor.Output())
	var headers metadata.MD
	var trailers metadata.MD
	err = conn.Invoke(ctx, binding.FullMethod, req, res, grpc.Header(&headers), grpc.Trailer(&trailers))
	grpcexec.AddMetadata(result.Headers, "", headers)
	grpcexec.AddMetadata(result.Headers, "trailer-", trailers)
	result.Metadata = grpcexec.MetadataRows(headers)
	result.Trailers = grpcexec.MetadataRows(trailers)
	if err != nil {
		st := status.Convert(err)
		result.Status = int(st.Code())
		result.StatusText = st.Code().String()
		result.Error = st.Message()
		result.Headers["grpc-status"] = strconv.Itoa(int(st.Code()))
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
	if err != nil {
		result.Error = "format gRPC response JSON: " + err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	result.Status = http.StatusOK
	result.StatusText = "OK"
	result.Headers["grpc-status"] = "0"
	result.Headers["grpc-method"] = binding.FullMethod
	result.Headers["grpc-stream"] = "unary"
	result.Headers["grpc-request-count"] = "1"
	result.Headers["grpc-response-count"] = "1"
	result.Body = string(body)
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.Size = len(body)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Assertions = evaluateAssertions(item.Assertions, result)
	return result
}

func executeGRPCStream(result *Response, conn *grpc.ClientConn, binding grpcMethodBinding, item RequestItem, vars map[string]string, ctx context.Context, start time.Time) {
	requests, err := grpcexec.RequestMessages(item, binding, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return
	}
	desc := &grpc.StreamDesc{
		StreamName:    string(binding.Descriptor.Name()),
		ServerStreams: binding.Descriptor.IsStreamingServer(),
		ClientStreams: binding.Descriptor.IsStreamingClient(),
	}
	stream, err := conn.NewStream(ctx, desc, binding.FullMethod)
	if err != nil {
		applyGRPCError(result, err, start)
		return
	}

	send := func() error {
		if !binding.Descriptor.IsStreamingClient() {
			if len(requests) == 0 {
				return nil
			}
			if err := stream.SendMsg(requests[0]); err != nil {
				return err
			}
			return stream.CloseSend()
		}
		for _, req := range requests {
			if err := stream.SendMsg(req); err != nil {
				return err
			}
		}
		return stream.CloseSend()
	}
	if binding.Descriptor.IsStreamingClient() && binding.Descriptor.IsStreamingServer() {
		sendErr := make(chan error, 1)
		go func() { sendErr <- send() }()
		receiveGRPCStream(result, stream, binding, len(requests), item.Assertions, start, true)
		if err := <-sendErr; err != nil && result.Error == "" {
			applyGRPCError(result, err, start)
		}
		return
	}
	if err := send(); err != nil {
		applyGRPCError(result, err, start)
		return
	}
	receiveGRPCStream(result, stream, binding, len(requests), item.Assertions, start, binding.Descriptor.IsStreamingServer())
}

func receiveGRPCStream(result *Response, stream grpc.ClientStream, binding grpcMethodBinding, requestCount int, assertions []Assertion, start time.Time, many bool) {
	rawResponses := []json.RawMessage{}
	for {
		res := dynamicpb.NewMessage(binding.Descriptor.Output())
		err := stream.RecvMsg(res)
		if err == io.EOF {
			break
		}
		if err != nil {
			if headers, headerErr := stream.Header(); headerErr == nil {
				grpcexec.AddMetadata(result.Headers, "", headers)
				result.Metadata = grpcexec.MetadataRows(headers)
			}
			grpcexec.AddMetadata(result.Headers, "trailer-", stream.Trailer())
			result.Trailers = grpcexec.MetadataRows(stream.Trailer())
			applyGRPCError(result, err, start)
			result.Headers["grpc-method"] = binding.FullMethod
			result.Headers["grpc-stream"] = grpcStreamType(binding.Descriptor)
			result.Headers["grpc-request-count"] = strconv.Itoa(requestCount)
			result.Headers["grpc-response-count"] = strconv.Itoa(len(rawResponses))
			if len(rawResponses) > 0 {
				body, marshalErr := json.MarshalIndent(rawResponses, "", "  ")
				if marshalErr == nil {
					result.Body = string(body)
					result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
					result.Size = len(body)
					result.Assertions = evaluateAssertions(assertions, *result)
				}
			}
			return
		}
		body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
		if err != nil {
			result.Error = "format gRPC response JSON: " + err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return
		}
		rawResponses = append(rawResponses, json.RawMessage(body))
		if !many {
			break
		}
	}
	if headers, err := stream.Header(); err == nil {
		grpcexec.AddMetadata(result.Headers, "", headers)
		result.Metadata = grpcexec.MetadataRows(headers)
	}
	grpcexec.AddMetadata(result.Headers, "trailer-", stream.Trailer())
	result.Trailers = grpcexec.MetadataRows(stream.Trailer())
	body, err := json.MarshalIndent(rawResponses, "", "  ")
	if err != nil {
		result.Error = "format gRPC stream JSON: " + err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return
	}
	result.Status = http.StatusOK
	result.StatusText = "OK"
	result.Headers["grpc-status"] = "0"
	result.Headers["grpc-method"] = binding.FullMethod
	result.Headers["grpc-stream"] = grpcStreamType(binding.Descriptor)
	result.Headers["grpc-request-count"] = strconv.Itoa(requestCount)
	result.Headers["grpc-response-count"] = strconv.Itoa(len(rawResponses))
	result.Body = string(body)
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.Size = len(body)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Assertions = evaluateAssertions(assertions, *result)
}

func applyGRPCError(result *Response, err error, start time.Time) {
	st := status.Convert(err)
	result.Status = int(st.Code())
	result.StatusText = st.Code().String()
	result.Error = st.Message()
	if result.Error == "" {
		result.Error = err.Error()
	}
	result.Headers["grpc-status"] = strconv.Itoa(int(st.Code()))
	result.DurationMs = time.Since(start).Milliseconds()
}

func grpcStreamSessionKey(collectionID, itemID string) string {
	return collectionID + "\x00" + itemID
}

func (a *App) grpcStreamRequestContext(collectionID, itemID, environmentID string, promptValues map[string]string) (RequestItem, Collection, map[string]string, error) {
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
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	return requestCopy, collectionCopy, scriptVariables.Combined, nil
}

func (a *App) replaceGRPCStreamSession(key string, session *grpcStreamSession) {
	a.grpcStreamMu.Lock()
	previous := a.grpcStreamSessions[key]
	a.grpcStreamSessions[key] = session
	a.grpcStreamMu.Unlock()
	if previous != nil {
		previous.close("reconnected")
	}
}

func (a *App) popGRPCStreamSession(key string) *grpcStreamSession {
	a.grpcStreamMu.Lock()
	session := a.grpcStreamSessions[key]
	delete(a.grpcStreamSessions, key)
	a.grpcStreamMu.Unlock()
	return session
}

func (a *App) removeGRPCStreamSessionIfSame(key string, session *grpcStreamSession) {
	a.grpcStreamMu.Lock()
	if a.grpcStreamSessions[key] == session {
		delete(a.grpcStreamSessions, key)
	}
	a.grpcStreamMu.Unlock()
}

func (session *grpcStreamSession) close(reason string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.closed = true
	session.closeReason = strings.TrimSpace(reason)
	session.lastActivityAt = time.Now()
	if session.cancel != nil {
		session.cancel()
	}
	if session.conn != nil {
		_ = session.conn.Close()
	}
	session.appendEventLocked(grpcStreamSessionEvent{
		Direction: "system",
		Type:      "cancel",
		Data:      session.closeReason,
		At:        session.lastActivityAt,
	})
	if session.status == 0 || session.status == http.StatusOK {
		session.status = 1
		session.statusText = "CANCELLED"
	}
}

func (session *grpcStreamSession) responseLocked(errMessage string) Response {
	headers := cloneStringMap(session.headers)
	if headers == nil {
		headers = map[string]string{}
	}
	for name, value := range session.trailers {
		headers["trailer-"+name] = value
	}
	headers["x-grpc-stream-connected"] = strconv.FormatBool(!session.closed && !session.ended)
	headers["x-grpc-stream-ended"] = strconv.FormatBool(session.ended)
	headers["x-grpc-stream-events"] = strconv.Itoa(len(session.events))
	headers["grpc-method"] = session.binding.FullMethod
	headers["grpc-stream"] = session.streamType
	headers["grpc-request-count"] = strconv.Itoa(session.requestCount)
	headers["grpc-response-count"] = strconv.Itoa(session.responseCount)
	if session.ended && errMessage == "" && headers["grpc-status"] == "" {
		headers["grpc-status"] = "0"
	}
	if session.closeReason != "" {
		headers["x-grpc-stream-close-reason"] = session.closeReason
	}
	// US-022: see the WebSocket equivalent. x-grpc-stream-events above still
	// carries the true total.
	tail, omitted := grpcEventTail(session.events)
	if omitted > 0 {
		headers["x-grpc-stream-events-omitted"] = strconv.Itoa(omitted)
	}
	body, err := json.MarshalIndent(tail, "", "  ")
	if err != nil {
		body = []byte("[]")
		if errMessage == "" {
			errMessage = err.Error()
		}
	}
	statusCode := session.status
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	statusText := firstNonEmpty(session.statusText, "STREAMING")
	if session.ended && errMessage == "" {
		statusText = "OK"
	}
	return Response{
		Status:       statusCode,
		StatusText:   statusText,
		Headers:      headers,
		Metadata:     grpcexec.MetadataRowsFromMap(session.headers),
		Trailers:     grpcexec.MetadataRowsFromMap(session.trailers),
		Body:         string(body),
		BodyBase64:   base64.StdEncoding.EncodeToString(body),
		Size:         len(body),
		DurationMs:   time.Since(session.openedAt).Milliseconds(),
		Error:        errMessage,
		PreviewMode:  "grpc-stream",
		RequestedURL: session.targetURL,
		SentAt:       session.openedAt,
	}
}

func (session *grpcStreamSession) notifyEventLocked() {
	if session.eventNotify == nil {
		return
	}
	select {
	case session.eventNotify <- struct{}{}:
	default:
	}
}

func (session *grpcStreamSession) startReceiver() {
	session.mu.Lock()
	if session.receiverStarted {
		session.mu.Unlock()
		return
	}
	session.receiverStarted = true
	if session.receiveDone == nil {
		session.receiveDone = make(chan struct{})
	}
	done := session.receiveDone
	session.mu.Unlock()

	go func() {
		defer close(done)
		for {
			res := dynamicpb.NewMessage(session.binding.Descriptor.Output())
			err := session.stream.RecvMsg(res)
			session.mu.Lock()
			if err == io.EOF {
				if !session.ended {
					session.ended = true
					session.closed = true
					session.status = http.StatusOK
					session.statusText = "OK"
					session.lastActivityAt = time.Now()
					grpcexec.AddMetadata(session.headers, "", mustGRPCHeader(session.stream))
					grpcexec.AddMetadata(session.trailers, "", session.stream.Trailer())
					session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "end", Data: "server stream ended", At: session.lastActivityAt})
					if session.conn != nil {
						_ = session.conn.Close()
					}
					session.notifyEventLocked()
				}
				session.mu.Unlock()
				return
			}
			if err != nil {
				if session.closed && session.closeReason != "" {
					session.notifyEventLocked()
					session.mu.Unlock()
					return
				}
				st := status.Convert(err)
				session.closed = true
				session.status = int(st.Code())
				session.statusText = st.Code().String()
				session.closeReason = firstNonEmpty(st.Message(), err.Error())
				session.lastActivityAt = time.Now()
				session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: session.lastActivityAt})
				if session.conn != nil {
					_ = session.conn.Close()
				}
				session.notifyEventLocked()
				session.mu.Unlock()
				return
			}
			body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
			now := time.Now()
			if err != nil {
				session.closed = true
				session.status = int(status.Code(err))
				session.statusText = "ERROR"
				session.closeReason = "format gRPC response JSON: " + err.Error()
				session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: now})
				if session.conn != nil {
					_ = session.conn.Close()
				}
				session.notifyEventLocked()
				session.mu.Unlock()
				return
			}
			session.responseCount++
			session.lastActivityAt = now
			session.appendEventLocked(grpcStreamSessionEvent{
				Direction: "received",
				Name:      fmt.Sprintf("response %d", session.responseCount),
				Type:      "json",
				Data:      string(body),
				At:        now,
			})
			session.notifyEventLocked()
			session.mu.Unlock()
		}
	}()
}

func (session *grpcStreamSession) waitForResponseAfter(responseCount int, wait time.Duration) {
	if wait <= 0 {
		wait = 500 * time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		session.mu.Lock()
		done := session.responseCount > responseCount || session.closed || session.ended
		session.mu.Unlock()
		if done {
			return
		}
		select {
		case <-session.eventNotify:
		case <-timer.C:
			return
		}
	}
}

func grpcStreamReceiveWait(timeout time.Duration) time.Duration {
	if timeout > 0 && timeout < 500*time.Millisecond {
		return timeout
	}
	return 500 * time.Millisecond
}

func grpcStreamEndWait(timeout time.Duration) time.Duration {
	if timeout > 0 && timeout < 5*time.Second {
		return timeout
	}
	return 5 * time.Second
}

func (session *grpcStreamSession) receiveAvailableLocked() {
	for {
		res := dynamicpb.NewMessage(session.binding.Descriptor.Output())
		err := session.stream.RecvMsg(res)
		if err == io.EOF {
			session.ended = true
			session.closed = true
			session.status = http.StatusOK
			session.statusText = "OK"
			session.lastActivityAt = time.Now()
			grpcexec.AddMetadata(session.headers, "", mustGRPCHeader(session.stream))
			grpcexec.AddMetadata(session.trailers, "", session.stream.Trailer())
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "end", Data: "server stream ended", At: session.lastActivityAt})
			if session.conn != nil {
				_ = session.conn.Close()
			}
			session.notifyEventLocked()
			return
		}
		if err != nil {
			st := status.Convert(err)
			session.closed = true
			session.status = int(st.Code())
			session.statusText = st.Code().String()
			session.closeReason = firstNonEmpty(st.Message(), err.Error())
			session.lastActivityAt = time.Now()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: session.lastActivityAt})
			if session.conn != nil {
				_ = session.conn.Close()
			}
			session.notifyEventLocked()
			return
		}
		body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
		now := time.Now()
		if err != nil {
			session.closed = true
			session.status = int(status.Code(err))
			session.statusText = "ERROR"
			session.closeReason = "format gRPC response JSON: " + err.Error()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: now})
			if session.conn != nil {
				_ = session.conn.Close()
			}
			session.notifyEventLocked()
			return
		}
		session.responseCount++
		session.lastActivityAt = now
		session.appendEventLocked(grpcStreamSessionEvent{
			Direction: "received",
			Name:      fmt.Sprintf("response %d", session.responseCount),
			Type:      "json",
			Data:      string(body),
			At:        now,
		})
		session.notifyEventLocked()
	}
}

func mustGRPCHeader(stream grpc.ClientStream) metadata.MD {
	headers, err := stream.Header()
	if err != nil {
		return nil
	}
	return headers
}

func (a *App) applyGRPCStreamResponse(collectionID, itemID string, response Response, timelines ...TimelineItem) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	// US-009 step 4. Store the body and record its handle as the response lands
	// in state. Best-effort by design at this step: Body is still populated and
	// still authoritative, so a failed cache write must not fail a request the
	// user just saw succeed. See migrateResponseBodiesLocked for where that
	// contract inverts.
	_ = a.attachResponseBody(&response)
	item.Response = &response
	for _, timeline := range timelines {
		if timeline.ID != "" {
			item.Timeline = append(item.Timeline, timeline)
		}
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ConnectGRPCStream(collectionID, itemID, environmentID string) (AppState, error) {
	return a.connectGRPCStream(collectionID, itemID, environmentID, nil)
}

func (a *App) ConnectGRPCStreamWithPromptValues(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	return a.connectGRPCStream(collectionID, itemID, environmentID, promptValues)
}

func (a *App) connectGRPCStream(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	start := time.Now()
	item, collection, vars, err := a.grpcStreamRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "grpc" {
		return AppState{}, errors.New("request is not a gRPC request")
	}
	targetURL := interpolate(item.URL, vars)
	response := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "grpc-stream", RequestedURL: targetURL}
	dialConfig, err := a.grpcDialConfigForRequest(collection, item, targetURL, vars)
	if err != nil {
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	timelineEvents := []grpcStreamSessionEvent{}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		cancel()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	outgoingCtx, err := grpcexec.OutgoingContext(ctx, item, vars, a.fetchOAuth2Token)
	if err != nil {
		cancel()
		_ = conn.Close()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	binding, err := grpcexec.ResolveMethod(outgoingCtx, conn, item, collection, vars)
	if err != nil {
		cancel()
		_ = conn.Close()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	if !binding.Descriptor.IsStreamingClient() && !binding.Descriptor.IsStreamingServer() {
		cancel()
		_ = conn.Close()
		response.Error = "gRPC method is not streaming"
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	desc := &grpc.StreamDesc{
		StreamName:    string(binding.Descriptor.Name()),
		ServerStreams: binding.Descriptor.IsStreamingServer(),
		ClientStreams: binding.Descriptor.IsStreamingClient(),
	}
	stream, err := conn.NewStream(outgoingCtx, desc, binding.FullMethod)
	if err != nil {
		cancel()
		_ = conn.Close()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	session := &grpcStreamSession{
		conn:           conn,
		stream:         stream,
		binding:        binding,
		cancel:         cancel,
		targetURL:      targetURL,
		streamType:     grpcStreamType(binding.Descriptor),
		status:         http.StatusOK,
		statusText:     "STREAMING",
		headers:        map[string]string{},
		trailers:       map[string]string{},
		timeout:        time.Duration(timeout) * time.Millisecond,
		openedAt:       start,
		lastActivityAt: start,
		events:         []grpcStreamSessionEvent{{Direction: "system", Type: "start", Data: binding.FullMethod, At: start}},
		eventNotify:    make(chan struct{}, 1),
		emit:           a.grpcEventEmitter(collectionID, itemID),
	}
	if binding.Descriptor.IsStreamingServer() && !binding.Descriptor.IsStreamingClient() {
		message, req, err := grpcOutboundMessageAt(item, binding, vars, 0)
		session.mu.Lock()
		if err != nil {
			session.closed = true
			session.statusText = "ERROR"
			session.closeReason = err.Error()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: err.Error(), At: time.Now()})
		} else if err := stream.SendMsg(req); err != nil {
			session.closed = true
			session.statusText = "ERROR"
			session.closeReason = err.Error()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: err.Error(), At: time.Now()})
		} else {
			session.requestCount++
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "sent", Name: message.Name, Type: "json", Data: message.Content, At: time.Now()})
			_ = stream.CloseSend()
			session.receiveAvailableLocked()
		}
		response = session.responseLocked(session.closeReason)
		timelineEvents = append(timelineEvents, session.events...)
		session.mu.Unlock()
		if session.conn != nil {
			_ = session.conn.Close()
		}
	} else {
		if binding.Descriptor.IsStreamingClient() && binding.Descriptor.IsStreamingServer() {
			session.startReceiver()
		}
		session.mu.Lock()
		response = session.responseLocked("")
		timelineEvents = append(timelineEvents, session.events...)
		session.mu.Unlock()
		a.replaceGRPCStreamSession(grpcStreamSessionKey(collectionID, itemID), session)
	}
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "start")...)
}

func (a *App) SendGRPCStreamMessage(collectionID, itemID, environmentID string, messageIndex int) (AppState, error) {
	return a.sendGRPCStreamMessage(collectionID, itemID, environmentID, messageIndex, nil)
}

func (a *App) SendGRPCStreamMessageWithPromptValues(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	return a.sendGRPCStreamMessage(collectionID, itemID, environmentID, messageIndex, promptValues)
}

func (a *App) sendGRPCStreamMessage(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	item, _, vars, err := a.grpcStreamRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "grpc" {
		return AppState{}, errors.New("request is not a gRPC request")
	}
	key := grpcStreamSessionKey(collectionID, itemID)
	a.grpcStreamMu.Lock()
	session := a.grpcStreamSessions[key]
	a.grpcStreamMu.Unlock()
	if session == nil {
		return AppState{}, errors.New("gRPC stream is not connected")
	}

	var response Response
	session.mu.Lock()
	if session.closed || session.ended {
		response = session.responseLocked(firstNonEmpty(session.closeReason, "gRPC stream is not connected"))
		session.mu.Unlock()
		a.removeGRPCStreamSessionIfSame(key, session)
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "message"))
	}
	if !session.binding.Descriptor.IsStreamingClient() {
		response = session.responseLocked("gRPC stream does not accept client messages")
		session.mu.Unlock()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "message"))
	}
	eventStartIndex := len(session.events)
	message, req, err := grpcOutboundMessageAt(item, session.binding, vars, messageIndex)
	if err != nil {
		response = session.responseLocked(err.Error())
		session.mu.Unlock()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "message"))
	}
	now := time.Now()
	session.appendEventLocked(grpcStreamSessionEvent{Direction: "sent", Name: message.Name, Type: "json", Data: message.Content, At: now})
	session.requestCount++
	session.lastActivityAt = now
	responseCountBeforeSend := session.responseCount
	hasResponseStream := session.binding.Descriptor.IsStreamingServer()
	session.notifyEventLocked()
	if err := session.stream.SendMsg(req); err != nil {
		st := status.Convert(err)
		session.closed = true
		session.status = int(st.Code())
		session.statusText = st.Code().String()
		session.closeReason = firstNonEmpty(st.Message(), err.Error())
		session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: time.Now()})
		if session.conn != nil {
			_ = session.conn.Close()
		}
		response = session.responseLocked(session.closeReason)
		timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
		session.mu.Unlock()
		a.removeGRPCStreamSessionIfSame(key, session)
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "message")...)
	}
	session.mu.Unlock()
	if hasResponseStream {
		session.waitForResponseAfter(responseCountBeforeSend, grpcStreamReceiveWait(session.timeout))
	}
	session.mu.Lock()
	response = session.responseLocked("")
	timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
	session.mu.Unlock()
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "message")...)
}

func (a *App) EndGRPCStream(collectionID, itemID string) (AppState, error) {
	key := grpcStreamSessionKey(collectionID, itemID)
	session := a.popGRPCStreamSession(key)
	if session == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.state, nil
	}
	var receiveDone <-chan struct{}
	receiveSynchronously := false
	session.mu.Lock()
	eventStartIndex := len(session.events)
	if !session.closed && !session.ended {
		_ = session.stream.CloseSend()
		session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "end", Data: "client stream ended", At: time.Now()})
		session.notifyEventLocked()
		if session.receiverStarted {
			receiveDone = session.receiveDone
		} else {
			receiveSynchronously = true
		}
	}
	if receiveSynchronously {
		session.receiveAvailableLocked()
	}
	session.mu.Unlock()
	if receiveDone != nil {
		select {
		case <-receiveDone:
		case <-time.After(grpcStreamEndWait(session.timeout)):
			session.close("end timed out")
		}
	}
	session.mu.Lock()
	response := session.responseLocked(session.closeReason)
	timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
	session.mu.Unlock()
	item, _, _, err := a.grpcStreamRequestContext(collectionID, itemID, "", nil)
	if err != nil {
		return AppState{}, err
	}
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "end")...)
}

func (a *App) CancelGRPCStream(collectionID, itemID string) (AppState, error) {
	key := grpcStreamSessionKey(collectionID, itemID)
	session := a.popGRPCStreamSession(key)
	if session == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.state, nil
	}
	session.mu.Lock()
	eventStartIndex := len(session.events)
	session.mu.Unlock()
	session.close("cancelled")
	session.mu.Lock()
	response := session.responseLocked(firstNonEmpty(session.closeReason, "cancelled"))
	timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
	session.mu.Unlock()
	item, _, _, err := a.grpcStreamRequestContext(collectionID, itemID, "", nil)
	if err != nil {
		return AppState{}, err
	}
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "cancel")...)
}

func grpcOutboundMessageAt(item RequestItem, binding grpcMethodBinding, vars map[string]string, messageIndex int) (GrpcMessage, proto.Message, error) {
	messages := grpcexec.GrpcurlRequestMessages(item, vars)
	if len(messages) == 0 {
		messages = []GrpcMessage{{Name: "message 1", Content: "{}"}}
	}
	if messageIndex < 0 || messageIndex >= len(messages) {
		return GrpcMessage{}, nil, fmt.Errorf("gRPC message %d not found", messageIndex+1)
	}
	message := messages[messageIndex]
	req := dynamicpb.NewMessage(binding.Descriptor.Input())
	content := strings.TrimSpace(message.Content)
	if content == "" {
		content = "{}"
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(content), req); err != nil {
		return GrpcMessage{}, nil, fmt.Errorf("parse gRPC request message %d JSON: %w", messageIndex+1, err)
	}
	message.Content = content
	return message, req, nil
}

func grpcStreamTimelineItem(item RequestItem, response Response, action string) TimelineItem {
	statusText := cleanStatusText(response.Status, response.StatusText)
	message := fmt.Sprintf("gRPC stream %s %s", action, response.RequestedURL)
	if method := strings.TrimSpace(response.Headers["grpc-method"]); method != "" {
		message = fmt.Sprintf("gRPC stream %s %s %s", action, strings.TrimPrefix(method, "/"), response.RequestedURL)
	}
	if requestCount := strings.TrimSpace(response.Headers["grpc-request-count"]); requestCount != "" {
		message += " sent " + requestCount
	}
	if responseCount := strings.TrimSpace(response.Headers["grpc-response-count"]); responseCount != "" {
		message += " received " + responseCount
	}
	if response.Error != "" {
		message += " failed"
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		Message:    message,
		At:         time.Now(),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      response.Error,
	}
}

func grpcStreamTimelineItems(item RequestItem, response Response, events []grpcStreamSessionEvent, action string) []TimelineItem {
	if len(events) == 0 {
		return []TimelineItem{grpcStreamTimelineItem(item, response, action)}
	}
	rows := make([]TimelineItem, 0, len(events)+2)
	terminalAt := time.Time{}
	terminal := false
	for _, event := range events {
		if event.Direction == "system" && event.Type == "end" && strings.Contains(strings.ToLower(event.Data), "client stream ended") {
			continue
		}
		row := grpcStreamTimelineItemForEvent(item, response, event)
		if row.ID == "" {
			continue
		}
		rows = append(rows, row)
		if row.EventType == "end" || row.EventType == "error" || row.EventType == "cancel" {
			terminal = true
			terminalAt = row.At
		}
	}
	if terminal {
		if len(response.Metadata) > 0 {
			rows = append(rows, grpcStreamMetadataTimelineItem(item, response, terminalAt))
		}
		if response.Headers["grpc-status"] != "" || len(response.Trailers) > 0 || response.Error != "" {
			rows = append(rows, grpcStreamStatusTimelineItem(item, response, terminalAt))
		}
	}
	if len(rows) == 0 {
		return []TimelineItem{grpcStreamTimelineItem(item, response, action)}
	}
	return rows
}

func grpcStreamTimelineItemForEvent(item RequestItem, response Response, event grpcStreamSessionEvent) TimelineItem {
	eventType := ""
	message := ""
	payload := event.Data
	statusText := cleanStatusText(response.Status, response.StatusText)
	methodName := strings.TrimPrefix(firstNonEmpty(response.Headers["grpc-method"], item.Method), "/")
	streamType := firstNonEmpty(response.Headers["grpc-stream"], grpcStreamTypeLabelFromStorage(item.GrpcMethodType), "stream")
	switch {
	case event.Direction == "system" && event.Type == "start":
		eventType = "request"
		message = fmt.Sprintf("gRPC request %s %s (%s stream)", methodName, response.RequestedURL, streamType)
	case event.Direction == "sent":
		eventType = "message"
		message = fmt.Sprintf("gRPC message %s %s", firstNonEmpty(event.Name, "message"), response.RequestedURL)
	case event.Direction == "received":
		eventType = "response"
		message = fmt.Sprintf("Response Message #%s %s", grpcResponseNumber(event.Name), response.RequestedURL)
	case event.Direction == "system" && event.Type == "end":
		eventType = "end"
		message = fmt.Sprintf("Stream Ended %s", response.RequestedURL)
		if responseCount := strings.TrimSpace(response.Headers["grpc-response-count"]); responseCount != "" {
			message += " received " + responseCount
		}
	case event.Direction == "system" && event.Type == "cancel":
		eventType = "cancel"
		message = fmt.Sprintf("Stream Cancelled %s", response.RequestedURL)
		payload = firstNonEmpty(event.Data, event.Error, response.Error)
	case event.Direction == "system" && event.Type == "error":
		eventType = "error"
		message = fmt.Sprintf("gRPC error %s", response.RequestedURL)
		payload = firstNonEmpty(event.Error, event.Data, response.Error)
	default:
		return TimelineItem{}
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  eventType,
		EventName:  event.Name,
		Message:    message,
		At:         event.At,
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      event.Error,
		Payload:    payload,
	}
}

func grpcStreamMetadataTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	if at.IsZero() {
		at = time.Now()
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  "metadata",
		Message:    "gRPC response metadata " + response.RequestedURL,
		At:         at.Add(time.Millisecond),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: cleanStatusText(response.Status, response.StatusText),
		Metadata:   response.Metadata,
	}
}

func grpcStreamStatusTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	if at.IsZero() {
		at = time.Now()
	}
	statusValue := firstNonEmpty(response.Headers["grpc-status"], strconv.Itoa(response.Status))
	statusText := cleanStatusText(response.Status, response.StatusText)
	payload := "grpc-status: " + statusValue
	if statusText != "" {
		payload += "\nstatus-text: " + statusText
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  "status",
		Message:    "gRPC status " + statusValue + " " + response.RequestedURL,
		At:         at.Add(2 * time.Millisecond),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Payload:    payload,
		Trailers:   response.Trailers,
	}
}

func grpcResponseNumber(name string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) > 0 {
		last := fields[len(fields)-1]
		if _, err := strconv.Atoi(last); err == nil {
			return last
		}
	}
	return "1"
}

func grpcStreamType(method protoreflect.MethodDescriptor) string {
	switch {
	case method.IsStreamingClient() && method.IsStreamingServer():
		return "bidi"
	case method.IsStreamingClient():
		return "client"
	case method.IsStreamingServer():
		return "server"
	default:
		return "unary"
	}
}

func grpcStreamTypeLabelFromStorage(methodType string) string {
	switch strings.TrimSpace(strings.ToLower(methodType)) {
	case "client-streaming":
		return "client"
	case "server-streaming":
		return "server"
	case "bidi-streaming":
		return "bidi"
	default:
		return "unary"
	}
}

func grpcDialTarget(rawURL string) (grpcDialConfig, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return grpcDialConfig{}, errors.New("gRPC URL is required")
	}
	if !strings.Contains(rawURL, "://") {
		return grpcDialConfig{Target: rawURL, Credentials: insecure.NewCredentials()}, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if strings.EqualFold(parsed.Scheme, "unix") || strings.EqualFold(parsed.Scheme, "grpc+unix") {
		socketPath, err := grpcexec.UnixSocketPath(parsed)
		if err != nil {
			return grpcDialConfig{}, err
		}
		return grpcexec.UnixDialConfig(socketPath), nil
	}
	target := parsed.Host
	if target == "" {
		target = strings.TrimPrefix(parsed.Opaque, "//")
	}
	if target == "" {
		return grpcDialConfig{}, errors.New("gRPC URL host is required")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "grpc":
		return grpcDialConfig{Target: target, Credentials: insecure.NewCredentials()}, nil
	case "grpcs":
		return grpcDialConfig{Target: target, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}, nil
	default:
		return grpcDialConfig{}, fmt.Errorf("unsupported gRPC URL scheme %q", parsed.Scheme)
	}
}

func (a *App) grpcDialConfigForRequest(collection Collection, item RequestItem, targetURL string, vars map[string]string) (grpcDialConfig, error) {
	dialConfig, err := grpcDialTarget(targetURL)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if userAgent := grpcexec.UserAgentFromHeaders(item.Headers, vars); userAgent != "" {
		dialConfig.Options = append(dialConfig.Options, grpc.WithUserAgent(userAgent))
	}
	if dialConfig.TLSConfig == nil {
		return dialConfig, nil
	}
	tlsConfig := dialConfig.TLSConfig.Clone()
	tlsSettings := a.appTLSSettingsSnapshot()
	verifyTLS := requestTLSVerificationEnabled(tlsSettings.Request, item.Settings.VerifyTLS)
	if !verifyTLS {
		tlsConfig.InsecureSkipVerify = true
	} else if err := applyCustomRootCAsToTLSConfig(tlsConfig, tlsSettings.Request); err != nil {
		return grpcDialConfig{}, err
	}
	if tlsSettings.ClientSessionCache != nil {
		tlsConfig.ClientSessionCache = tlsSettings.ClientSessionCache
	}
	certificate, ok, err := transport.MatchingTLSClientCertificate(collection.Path, collection.ClientCertificates, targetURL, vars)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if ok {
		tlsConfig.Certificates = append([]tls.Certificate{certificate}, tlsConfig.Certificates...)
	}
	dialConfig.TLSConfig = tlsConfig
	return dialConfig, nil
}

// gRPC method resolution, messages, metadata and grpcurl moved to internal/grpcexec.

// randomHex, wssePasswordDigest and quoteDigestValue moved to
// internal/auth/wsse with the header they build. Wrapped because package main
// still uses them for HTTP digest auth.

func websocketSessionKey(collectionID, itemID string) string {
	return collectionID + "\x00" + itemID
}

func (a *App) websocketRequestContext(collectionID, itemID, environmentID string, promptValues map[string]string) (RequestItem, map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return RequestItem{}, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return RequestItem{}, nil, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return RequestItem{}, nil, err
	}
	requestCopy := scripting.EffectiveRequest(*collection, *item)
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	return requestCopy, scriptVariables.Combined, nil
}

func (a *App) replaceWebSocketSession(key string, session *websocketSession) {
	a.websocketMu.Lock()
	previous := a.websocketSessions[key]
	a.websocketSessions[key] = session
	a.websocketMu.Unlock()
	if previous != nil {
		previous.close("reconnected")
	}
}

func (a *App) popWebSocketSession(key string) *websocketSession {
	a.websocketMu.Lock()
	session := a.websocketSessions[key]
	delete(a.websocketSessions, key)
	a.websocketMu.Unlock()
	return session
}

func (a *App) removeWebSocketSessionIfSame(key string, session *websocketSession) {
	a.websocketMu.Lock()
	if a.websocketSessions[key] == session {
		delete(a.websocketSessions, key)
	}
	a.websocketMu.Unlock()
}

func (session *websocketSession) close(reason string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.closed = true
	session.closeReason = strings.TrimSpace(reason)
	session.lastActivityAt = time.Now()
	if session.done != nil && !session.doneClosed {
		close(session.done)
		session.doneClosed = true
	}
	session.appendEventLocked(websocketSessionEvent{
		Direction: "system",
		Type:      "close",
		Data:      session.closeReason,
		At:        session.lastActivityAt,
	})
	_ = session.conn.Close()
}

func (session *websocketSession) startKeepAlive() {
	if session.keepAliveEvery <= 0 || session.done == nil {
		return
	}
	ticker := time.NewTicker(session.keepAliveEvery)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-session.done:
				return
			case at := <-ticker.C:
				session.mu.Lock()
				if session.closed {
					session.mu.Unlock()
					return
				}
				deadline := at.Add(5 * time.Second)
				if err := session.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					session.closed = true
					session.closeReason = err.Error()
					if !session.doneClosed {
						close(session.done)
						session.doneClosed = true
					}
					_ = session.conn.Close()
					session.appendEventLocked(websocketSessionEvent{
						Direction: "system",
						Type:      "ping",
						Error:     err.Error(),
						At:        at,
					})
					session.lastActivityAt = at
					session.mu.Unlock()
					return
				}
				session.appendEventLocked(websocketSessionEvent{
					Direction: "system",
					Type:      "ping",
					Data:      "keep-alive",
					At:        at,
				})
				session.lastActivityAt = at
				session.mu.Unlock()
			}
		}
	}()
}

func (session *websocketSession) responseLocked(errMessage string) Response {
	headers := cloneStringMap(session.headers)
	connected := !session.closed
	headers["x-websocket-connected"] = strconv.FormatBool(connected)
	headers["x-websocket-events"] = strconv.Itoa(len(session.events))
	if session.keepAliveEvery > 0 {
		headers["x-websocket-keep-alive-interval"] = strconv.Itoa(int(session.keepAliveEvery / time.Millisecond))
	}
	if session.closeReason != "" {
		headers["x-websocket-close-reason"] = session.closeReason
	}
	// US-021: marshal the trailing window, not the whole log. x-websocket-events
	// still reports the true total above, so the count a caller sees is the real
	// one and this header says how much of it the body omits.
	tail, omitted := websocketEventTail(session.events)
	if omitted > 0 {
		headers["x-websocket-events-omitted"] = strconv.Itoa(omitted)
	}
	body, err := json.MarshalIndent(tail, "", "  ")
	if err != nil {
		body = []byte("[]")
		if errMessage == "" {
			errMessage = err.Error()
		}
	}
	return Response{
		Status:       session.status,
		StatusText:   session.statusText,
		Headers:      headers,
		Body:         string(body),
		BodyBase64:   base64.StdEncoding.EncodeToString(body),
		Size:         len(body),
		DurationMs:   time.Since(session.openedAt).Milliseconds(),
		SentAt:       session.openedAt,
		RequestedURL: session.targetURL,
		PreviewMode:  "websocket",
		Error:        errMessage,
	}
}

func (a *App) applyWebSocketResponse(collectionID, itemID string, response Response, timeline TimelineItem) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	// US-009 step 4. Store the body and record its handle as the response lands
	// in state. Best-effort by design at this step: Body is still populated and
	// still authoritative, so a failed cache write must not fail a request the
	// user just saw succeed. See migrateResponseBodiesLocked for where that
	// contract inverts.
	_ = a.attachResponseBody(&response)
	item.Response = &response
	if timeline.ID != "" {
		item.Timeline = append(item.Timeline, timeline)
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ConnectWebSocket(collectionID, itemID, environmentID string) (AppState, error) {
	return a.connectWebSocket(collectionID, itemID, environmentID, nil)
}

func (a *App) ConnectWebSocketWithPromptValues(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	return a.connectWebSocket(collectionID, itemID, environmentID, promptValues)
}

func (a *App) connectWebSocket(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	start := time.Now()
	item, vars, err := a.websocketRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "websocket" {
		return AppState{}, errors.New("request is not a WebSocket request")
	}
	targetURL := wsexec.TargetURL(item, vars)
	headers := wsexec.Headers(item, vars)
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	response := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "websocket", RequestedURL: targetURL}
	dialer, err := a.websocketDialer(collectionID, item, targetURL, vars, time.Duration(timeout)*time.Millisecond)
	if err != nil {
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "connect"))
	}
	conn, res, err := dialer.Dial(targetURL, headers)
	if res != nil {
		response.Status = res.StatusCode
		response.StatusText = res.Status
		for name, values := range res.Header {
			response.Headers[name] = strings.Join(values, ", ")
		}
	}
	if err != nil {
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "connect"))
	}
	session := &websocketSession{
		conn:           conn,
		targetURL:      targetURL,
		status:         response.Status,
		statusText:     response.StatusText,
		headers:        cloneStringMap(response.Headers),
		timeout:        time.Duration(timeout) * time.Millisecond,
		keepAliveEvery: wsexec.KeepAliveInterval(item.Settings),
		openedAt:       start,
		lastActivityAt: start,
		events:         []websocketSessionEvent{},
		done:           make(chan struct{}),
		emit:           a.websocketEventEmitter(collectionID, itemID),
	}
	session.mu.Lock()
	response = session.responseLocked("")
	session.mu.Unlock()
	a.replaceWebSocketSession(websocketSessionKey(collectionID, itemID), session)
	session.startKeepAlive()
	return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "connect"))
}

func (a *App) SendWebSocketMessage(collectionID, itemID, environmentID string, messageIndex int) (AppState, error) {
	return a.sendWebSocketMessage(collectionID, itemID, environmentID, messageIndex, nil)
}

func (a *App) SendWebSocketMessageWithPromptValues(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	return a.sendWebSocketMessage(collectionID, itemID, environmentID, messageIndex, promptValues)
}

func (a *App) sendWebSocketMessage(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	item, vars, err := a.websocketRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "websocket" {
		return AppState{}, errors.New("request is not a WebSocket request")
	}
	message, err := wsexec.OutboundMessageAt(item, vars, messageIndex)
	if err != nil {
		return AppState{}, err
	}
	key := websocketSessionKey(collectionID, itemID)
	a.websocketMu.Lock()
	session := a.websocketSessions[key]
	a.websocketMu.Unlock()
	if session == nil {
		return AppState{}, errors.New("WebSocket is not connected")
	}

	var response Response
	var shouldRemove bool
	session.mu.Lock()
	if session.closed {
		errMessage := firstNonEmpty(session.closeReason, "WebSocket is not connected")
		response = session.responseLocked(errMessage)
		shouldRemove = true
	} else {
		now := time.Now()
		frameType, payload := wsexec.FramePayload(message)
		sent := websocketSessionEvent{
			Direction: "sent",
			Name:      message.Name,
			Type:      wsexec.MessageTypeName(frameType),
			Data:      string(payload),
			At:        now,
		}
		if frameType == websocket.BinaryMessage {
			sent.DataBase64 = base64.StdEncoding.EncodeToString(payload)
			sent.DataHex = hex.EncodeToString(payload)
		}
		session.appendEventLocked(sent)
		session.lastActivityAt = now
		if err := session.conn.WriteMessage(frameType, payload); err != nil {
			session.closed = true
			session.closeReason = err.Error()
			_ = session.conn.Close()
			response = session.responseLocked(err.Error())
			shouldRemove = true
		} else {
			_ = session.conn.SetReadDeadline(time.Now().Add(session.timeout))
			responseType, payload, err := session.conn.ReadMessage()
			if err != nil {
				session.closed = true
				session.closeReason = err.Error()
				_ = session.conn.Close()
				response = session.responseLocked(err.Error())
				shouldRemove = true
			} else {
				received := websocketSessionEvent{
					Direction: "received",
					Name:      message.Name,
					Type:      wsexec.MessageTypeName(responseType),
					Data:      string(payload),
					At:        time.Now(),
				}
				if responseType == websocket.BinaryMessage {
					received.DataBase64 = base64.StdEncoding.EncodeToString(payload)
					received.DataHex = hex.EncodeToString(payload)
				}
				session.appendEventLocked(received)
				session.lastActivityAt = received.At
				response = session.responseLocked("")
			}
		}
	}
	session.mu.Unlock()
	if shouldRemove {
		a.removeWebSocketSessionIfSame(key, session)
	}
	return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "message"))
}

func (a *App) DisconnectWebSocket(collectionID, itemID string) (AppState, error) {
	key := websocketSessionKey(collectionID, itemID)
	session := a.popWebSocketSession(key)
	if session == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.state, nil
	}
	session.close("disconnected")
	session.mu.Lock()
	response := session.responseLocked("")
	session.mu.Unlock()
	item, _, err := a.websocketRequestContext(collectionID, itemID, "", nil)
	if err != nil {
		return AppState{}, err
	}
	return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "disconnect"))
}

func websocketTimelineItem(item RequestItem, response Response, action string) TimelineItem {
	statusText := cleanStatusText(response.Status, response.StatusText)
	message := fmt.Sprintf("WebSocket %s %s", action, response.RequestedURL)
	if response.Error != "" {
		message += " failed"
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		Message:    message,
		At:         time.Now(),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "websocket",
		Method:     "CONNECT",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      response.Error,
	}
}

func (a *App) executeWebSocket(collectionID string, item RequestItem, vars map[string]string) Response {
	start := time.Now()
	result := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "websocket"}
	targetURL := wsexec.TargetURL(item, vars)
	result.RequestedURL = targetURL

	headers := wsexec.Headers(item, vars)

	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	dialer, err := a.websocketDialer(collectionID, item, targetURL, vars, time.Duration(timeout)*time.Millisecond)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	conn, res, err := dialer.Dial(targetURL, headers)
	if res != nil {
		result.Status = res.StatusCode
		result.StatusText = res.Status
		for name, values := range res.Header {
			result.Headers[name] = strings.Join(values, ", ")
		}
	}
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	defer func() { _ = conn.Close() }()

	messages := wsexec.OutboundMessages(item, vars)
	if len(messages) == 0 {
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	events := make([]map[string]string, 0, len(messages))
	var singlePayload []byte
	var singleResponseType int
	for _, message := range messages {
		frameType, payload := wsexec.FramePayload(message)
		if err := conn.WriteMessage(frameType, payload); err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Millisecond))
		responseType, payload, err := conn.ReadMessage()
		if err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
		event := map[string]string{
			"name": message.Name,
			"type": wsexec.MessageTypeName(responseType),
			"data": string(payload),
		}
		if responseType == websocket.BinaryMessage {
			event["dataBase64"] = base64.StdEncoding.EncodeToString(payload)
			event["dataHex"] = hex.EncodeToString(payload)
		}
		events = append(events, event)
		singlePayload = payload
		singleResponseType = responseType
	}

	var body []byte
	if len(events) == 1 {
		body = singlePayload
		result.Headers["x-websocket-message-type"] = wsexec.MessageTypeName(singleResponseType)
		if singleResponseType == websocket.BinaryMessage {
			result.Headers["x-websocket-message-base64"] = base64.StdEncoding.EncodeToString(singlePayload)
			result.Headers["x-websocket-message-hex"] = hex.EncodeToString(singlePayload)
		}
	} else {
		data, err := json.MarshalIndent(events, "", "  ")
		if err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
		body = data
	}
	result.Body = string(body)
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.Size = len(body)
	result.Headers["x-websocket-messages-sent"] = strconv.Itoa(len(messages))
	result.DurationMs = time.Since(start).Milliseconds()
	result.Assertions = evaluateAssertions(item.Assertions, result)
	return result
}

// WebSocket message preparation moved to internal/wsexec.
//
// normalizeWSMessageType is wrapped rather than renamed at seven call sites.

func normalizeWSMessageType(value string) string { return wsexec.NormalizeMessageType(value) }

// Dialling stays in package main:
// it reads the app's TLS settings, http client and per-collection certificates.

func (a *App) websocketDialer(collectionID string, item RequestItem, targetURL string, vars map[string]string, timeout time.Duration) (websocket.Dialer, error) {
	baseTransport := http.RoundTripper(http.DefaultTransport)
	if a.httpClient != nil && a.httpClient.Transport != nil {
		baseTransport = a.httpClient.Transport
	}
	tlsSettings := a.appTLSSettingsSnapshot()
	verifyTLS := requestTLSVerificationEnabled(tlsSettings.Request, item.Settings.VerifyTLS)
	var tlsErr error
	baseTransport, tlsErr = transportWithAppTLSSettings(baseTransport, tlsSettings, verifyTLS)
	if tlsErr != nil {
		return websocket.Dialer{}, tlsErr
	}
	if collectionPath, certs, ok := a.collectionClientCertificateConfig(collectionID); ok {
		var certErr error
		baseTransport, certErr = transport.WithClientCertificate(baseTransport, collectionPath, certs, targetURL, vars)
		if certErr != nil {
			return websocket.Dialer{}, certErr
		}
	}
	proxyResolution := a.collectionProxyResolution(collectionID)
	var proxyErr error
	baseTransport, proxyErr = transport.WithProxyResolution(baseTransport, proxyResolution, targetURL, vars)
	if proxyErr != nil {
		return websocket.Dialer{}, proxyErr
	}
	transport := transport.CloneHTTPTransport(baseTransport)
	return websocket.Dialer{
		HandshakeTimeout: timeout,
		Proxy:            transport.Proxy,
		TLSClientConfig:  transport.TLSClientConfig,
		NetDialContext:   transport.DialContext,
	}, nil
}

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

type oauth2TokenResponse struct {
	AccessToken  string
	IDToken      string
	RefreshToken string
	Values       map[string]interface{}
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type oauth2CredentialsFile struct {
	Credentials []oauth2CredentialEntry `json:"credentials"`
}

type oauth2CredentialEntry struct {
	CacheKey string `json:"cacheKey"`
	Data     string `json:"data"`
}

type oauth2TokenStorage struct {
	AccessToken  string                 `json:"accessToken,omitempty"`
	IDToken      string                 `json:"idToken,omitempty"`
	RefreshToken string                 `json:"refreshToken,omitempty"`
	Values       map[string]interface{} `json:"values,omitempty"`
	CreatedAt    int64                  `json:"createdAt,omitempty"`
	ExpiresAt    int64                  `json:"expiresAt,omitempty"`
}

var oauth2CredentialsRemove = os.Remove

func (response oauth2TokenResponse) expired(now time.Time) bool {
	return !response.ExpiresAt.IsZero() && !now.Before(response.ExpiresAt)
}

func (a *App) oauth2CredentialsPath() string {
	return filepath.Join(a.dataDir, "oauth2.json")
}

func (a *App) loadOAuth2Credentials() error {
	path := a.oauth2CredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var store oauth2CredentialsFile
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("parse oauth2.json: %w", err)
	}
	next := map[string]oauth2TokenResponse{}
	for _, entry := range store.Credentials {
		cacheKey := strings.TrimSpace(entry.CacheKey)
		if cacheKey == "" || strings.TrimSpace(entry.Data) == "" {
			continue
		}
		response, err := decryptOAuth2TokenResponse(a.dataDir, entry.Data)
		if err != nil {
			return fmt.Errorf("decrypt OAuth2 credential %s: %w", cacheKey, err)
		}
		next[cacheKey] = response
	}
	a.oauth2Mu.Lock()
	a.oauth2 = next
	a.oauth2Baseline = cloneOAuth2TokenMap(next)
	a.oauth2Mu.Unlock()
	return nil
}

func (a *App) storeOAuth2Credentials() error {
	a.oauth2Mu.Lock()
	local := map[string]oauth2TokenResponse{}
	for key, value := range a.oauth2 {
		local[key] = value
	}
	baseline := cloneOAuth2TokenMap(a.oauth2Baseline)
	// US-013. This function runs on every state persist, and everything below
	// is expensive: it reads oauth2.json, DECRYPTS every stored credential,
	// re-encrypts every merged credential, and writes the file. None of that
	// can change the result when this App's own OAuth2 state has not moved
	// since the last successful store, because the merge is a function of
	// (baseline, local, disk) and a no-op delta leaves disk exactly as it is.
	//
	// So the gate is on (baseline, local) alone. Deliberately not on disk: a
	// credential another window obtained is already on disk, and refusing to
	// rewrite it is the correct outcome, not a lost update.
	fingerprint, fingerprintErr := oauth2TokenMapFingerprint(baseline, local)
	unchanged := fingerprintErr == nil && fingerprint != "" && fingerprint == a.oauth2Fingerprint
	a.oauth2Mu.Unlock()
	if unchanged {
		return nil
	}
	disk := map[string]oauth2TokenResponse{}
	if data, err := os.ReadFile(a.oauth2CredentialsPath()); err == nil {
		var stored oauth2CredentialsFile
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("parse oauth2.json: %w", err)
		}
		for _, entry := range stored.Credentials {
			value, err := decryptOAuth2TokenResponse(a.dataDir, entry.Data)
			if err != nil {
				return fmt.Errorf("decrypt OAuth2 credential %s: %w", entry.CacheKey, err)
			}
			disk[entry.CacheKey] = value
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged := mergeOAuth2TokenDelta(baseline, local, disk)
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]oauth2CredentialEntry, 0, len(keys))
	for _, key := range keys {
		encoded, err := encryptOAuth2TokenResponse(a.dataDir, merged[key])
		if err != nil {
			return err
		}
		entries = append(entries, oauth2CredentialEntry{CacheKey: key, Data: encoded})
	}

	if len(entries) == 0 {
		path := a.oauth2CredentialsPath()
		if err := oauth2CredentialsRemove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else {
			directory, err := os.Open(filepath.Dir(path))
			if err != nil {
				return err
			}
			syncErr := directory.Sync()
			closeErr := directory.Close()
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	} else {
		data, err := json.MarshalIndent(oauth2CredentialsFile{Credentials: entries}, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivateAtomic(a.oauth2CredentialsPath(), data); err != nil {
			return err
		}
	}
	a.oauth2Mu.Lock()
	a.oauth2 = cloneOAuth2TokenMap(merged)
	a.oauth2Baseline = cloneOAuth2TokenMap(merged)
	// US-013. Recorded only after the write landed, and computed from the maps
	// that were just installed rather than from the ones read at entry — the
	// merge may have pulled in another window's credentials, and the gate must
	// describe the state we actually persisted.
	if next, err := oauth2TokenMapFingerprint(a.oauth2Baseline, a.oauth2); err == nil {
		a.oauth2Fingerprint = next
	} else {
		// Unhashable state simply disables the gate; the next store does the
		// full work rather than skipping on a stale fingerprint.
		a.oauth2Fingerprint = ""
	}
	a.oauth2Mu.Unlock()
	return nil
}

// oauth2TokenMapFingerprint hashes the (baseline, local) pair that determines
// whether storeOAuth2Credentials can change anything on disk.
//
// Both halves are needed: the merge is a delta of local against baseline, so
// two different baselines with the same local map produce different results —
// a credential present in baseline and absent from local is a DELETION, which
// is indistinguishable from "never existed" if only local is hashed.
func oauth2TokenMapFingerprint(baseline, local map[string]oauth2TokenResponse) (string, error) {
	digest := sha256.New()
	for _, values := range []map[string]oauth2TokenResponse{baseline, local} {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encoded, err := json.Marshal(values[key])
			if err != nil {
				return "", err
			}
			// Length-prefixed, so that a key ending where a value begins cannot
			// collide with a different split of the same bytes.
			//
			// hash.Hash.Write is documented never to return an error, which is
			// why these are discarded rather than propagated.
			_, _ = fmt.Fprintf(digest, "%d:%s%d:", len(key), key, len(encoded))
			_, _ = digest.Write(encoded)
		}
		// Separator between the baseline and local halves, so that moving a key
		// from one to the other changes the digest.
		_, _ = digest.Write([]byte{0})
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func cloneOAuth2TokenMap(values map[string]oauth2TokenResponse) map[string]oauth2TokenResponse {
	out := map[string]oauth2TokenResponse{}
	for k, v := range values {
		out[k] = v
	}
	return out
}
func mergeOAuth2TokenDelta(base, current, disk map[string]oauth2TokenResponse) map[string]oauth2TokenResponse {
	out := cloneOAuth2TokenMap(disk)
	for k, b := range base {
		c, ok := current[k]
		if !ok {
			delete(out, k)
		} else if !reflect.DeepEqual(b, c) {
			out[k] = c
		}
	}
	for k, c := range current {
		if _, ok := base[k]; !ok {
			out[k] = c
		}
	}
	return out
}

func encryptOAuth2TokenResponse(dataDir string, response oauth2TokenResponse) (string, error) {
	data, err := json.Marshal(oauth2TokenStorageFromResponse(response))
	if err != nil {
		return "", fmt.Errorf("encode OAuth2 credentials: %w", err)
	}
	return encryptEnvironmentSecretString(dataDir, string(data)), nil
}

func decryptOAuth2TokenResponse(dataDir, encoded string) (oauth2TokenResponse, error) {
	plain, err := decryptEnvironmentSecretString(dataDir, encoded)
	if err != nil {
		return oauth2TokenResponse{}, err
	}
	var storage oauth2TokenStorage
	if err := json.Unmarshal([]byte(plain), &storage); err != nil {
		return oauth2TokenResponse{}, fmt.Errorf("parse OAuth2 credentials: %w", err)
	}
	return oauth2TokenResponseFromStorage(storage), nil
}

func oauth2TokenStorageFromResponse(response oauth2TokenResponse) oauth2TokenStorage {
	values := map[string]interface{}{}
	for key, value := range response.Values {
		values[key] = value
	}
	storage := oauth2TokenStorage{
		AccessToken:  response.AccessToken,
		IDToken:      response.IDToken,
		RefreshToken: response.RefreshToken,
		Values:       values,
	}
	if !response.CreatedAt.IsZero() {
		storage.CreatedAt = response.CreatedAt.UnixMilli()
	}
	if !response.ExpiresAt.IsZero() {
		storage.ExpiresAt = response.ExpiresAt.UnixMilli()
	}
	return storage
}

func oauth2TokenResponseFromStorage(storage oauth2TokenStorage) oauth2TokenResponse {
	values := map[string]interface{}{}
	for key, value := range storage.Values {
		values[key] = value
	}
	response := oauth2TokenResponse{
		AccessToken:  storage.AccessToken,
		IDToken:      storage.IDToken,
		RefreshToken: storage.RefreshToken,
		Values:       values,
	}
	if storage.CreatedAt > 0 {
		response.CreatedAt = time.UnixMilli(storage.CreatedAt)
	}
	if storage.ExpiresAt > 0 {
		response.ExpiresAt = time.UnixMilli(storage.ExpiresAt)
	}
	return response
}

func (a *App) fetchOAuth2Token(auth OAuth2Auth, vars map[string]string) (string, error) {
	token, _, err := a.fetchOAuth2TokenWithTimeline(auth, vars)
	return token, err
}

func (a *App) fetchOAuth2TokenWithTimeline(auth OAuth2Auth, vars map[string]string) (string, []TimelineItem, error) {
	cfg := interpolateOAuth2Auth(auth, vars)
	key := oauth2CacheKey(cfg)
	now := time.Now()
	a.oauth2Mu.Lock()
	if cached, ok := a.oauth2[key]; ok {
		if token := oauth2TokenValue(cached, cfg.TokenSource); token != "" && !cached.expired(now) {
			a.oauth2Mu.Unlock()
			return token, nil, nil
		}
		if cfg.AutoRefreshToken && cached.RefreshToken != "" {
			a.oauth2Mu.Unlock()
			refreshed, timelineEntry, err := requestOAuth2RefreshTokenWithTimeline(cfg, cached.RefreshToken)
			if err != nil {
				return "", optionalTimelineEntry(timelineEntry), err
			}
			a.oauth2Mu.Lock()
			a.oauth2[key] = refreshed
			a.oauth2Mu.Unlock()
			return oauth2TokenValue(refreshed, cfg.TokenSource), optionalTimelineEntry(timelineEntry), nil
		}
	}
	a.oauth2Mu.Unlock()

	if strings.TrimSpace(cfg.GrantType) == "authorization_code" {
		response, timelineEntries, err := a.requestOAuth2AuthorizationCodeTokenWithTimeline(cfg)
		if err != nil {
			return "", timelineEntries, err
		}
		a.oauth2Mu.Lock()
		a.oauth2[key] = response
		a.oauth2Mu.Unlock()
		return oauth2TokenValue(response, cfg.TokenSource), timelineEntries, nil
	}
	if strings.TrimSpace(cfg.GrantType) == "implicit" {
		response, timelineEntries, err := a.requestOAuth2ImplicitTokenWithTimeline(cfg)
		if err != nil {
			return "", timelineEntries, err
		}
		a.oauth2Mu.Lock()
		a.oauth2[key] = response
		a.oauth2Mu.Unlock()
		return oauth2TokenValue(response, cfg.TokenSource), timelineEntries, nil
	}

	response, timelineEntry, err := requestOAuth2TokenWithTimeline(cfg)
	if err != nil {
		return "", optionalTimelineEntry(timelineEntry), err
	}
	a.oauth2Mu.Lock()
	a.oauth2[key] = response
	a.oauth2Mu.Unlock()
	return oauth2TokenValue(response, cfg.TokenSource), optionalTimelineEntry(timelineEntry), nil
}

func fetchOAuth2Token(auth OAuth2Auth, vars map[string]string) (string, error) {
	cfg := interpolateOAuth2Auth(auth, vars)
	response, err := requestOAuth2Token(cfg)
	if err != nil {
		return "", err
	}
	return oauth2TokenValue(response, cfg.TokenSource), nil
}

func oauth2CacheKey(cfg OAuth2Auth) string {
	return firstNonEmpty(cfg.AccessTokenURL, cfg.AuthorizationURL) + "|" + firstNonEmpty(cfg.CredentialsID, "credentials")
}

func requestOAuth2Token(cfg OAuth2Auth) (oauth2TokenResponse, error) {
	response, _, err := requestOAuth2TokenWithGrantTimeline(cfg, strings.TrimSpace(cfg.GrantType), "")
	return response, err
}

func requestOAuth2TokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2TokenWithGrantTimeline(cfg, strings.TrimSpace(cfg.GrantType), "")
}

func requestOAuth2RefreshTokenWithTimeline(cfg OAuth2Auth, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
	response, timelineEntry, err := requestOAuth2TokenWithGrantTimeline(cfg, "refresh_token", refreshToken)
	if err != nil {
		return oauth2TokenResponse{}, timelineEntry, err
	}
	if response.RefreshToken == "" {
		response.RefreshToken = refreshToken
		if response.Values == nil {
			response.Values = map[string]interface{}{}
		}
		response.Values["refresh_token"] = refreshToken
	}
	return response, timelineEntry, nil
}

func requestOAuth2TokenWithGrantTimeline(cfg OAuth2Auth, grantType, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
	if grantType == "" {
		return oauth2TokenResponse{}, nil, nil
	}
	if grantType != "client_credentials" && grantType != "password" && grantType != "refresh_token" {
		return oauth2TokenResponse{}, nil, fmt.Errorf("OAuth2 grant type %s requires browser support and is not implemented", grantType)
	}
	tokenURL := cfg.AccessTokenURL
	if grantType == "refresh_token" {
		tokenURL = firstNonEmpty(cfg.RefreshTokenURL, cfg.AccessTokenURL)
	}
	if strings.TrimSpace(tokenURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 access token URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 client ID is required")
	}
	form := url.Values{}
	form.Set("grant_type", grantType)
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}
	if grantType == "refresh_token" {
		if refreshToken == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 refresh token is required")
		}
		form.Set("refresh_token", refreshToken)
	}
	if grantType == "password" {
		if cfg.Username == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 username is required for password grant")
		}
		if cfg.Password == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 password is required for password grant")
		}
		form.Set("username", cfg.Username)
		form.Set("password", cfg.Password)
	}
	params := oauth2AdditionalParamsForGrant(cfg, grantType)
	params = append(params, legacyOAuth2AdditionalParams(cfg.AdditionalParams)...)
	return requestOAuth2TokenFormWithTimeline(cfg, tokenURL, form, params)
}

func requestOAuth2AuthorizationCodeTokenWithTimeline(cfg OAuth2Auth, code, codeVerifier, redirectURI string) (oauth2TokenResponse, *TimelineItem, error) {
	if strings.TrimSpace(cfg.AccessTokenURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 access token URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 client ID is required")
	}
	if strings.TrimSpace(code) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 authorization code is required")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if cfg.PKCE {
		if strings.TrimSpace(codeVerifier) == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 PKCE code verifier is required")
		}
		form.Set("code_verifier", codeVerifier)
	}
	params := append([]OAuth2AdditionalParam{}, cfg.TokenAdditionalParams...)
	params = append(params, legacyOAuth2AdditionalParams(cfg.AdditionalParams)...)
	return requestOAuth2TokenFormWithTimeline(cfg, cfg.AccessTokenURL, form, params)
}

func requestOAuth2TokenFormWithTimeline(cfg OAuth2Auth, tokenURL string, form url.Values, params []OAuth2AdditionalParam) (oauth2TokenResponse, *TimelineItem, error) {
	tokenReq, err := http.NewRequest(http.MethodPost, tokenURL, nil)
	if err != nil {
		timelineEntry := oauth2TimelineItemFromRequest(tokenReq, nil, 0, err)
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenReq.Header.Set("Accept", "application/json")
	if err := applyOAuth2AdditionalParams(tokenReq, form, params); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	if cfg.CredentialsPlacement == "" {
		cfg.CredentialsPlacement = "basic_auth_header"
	}
	if cfg.CredentialsPlacement == "basic_auth_header" {
		tokenReq.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	} else {
		form.Set("client_id", cfg.ClientID)
		if strings.TrimSpace(cfg.ClientSecret) != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
	}
	setRequestBodyString(tokenReq, form.Encode())
	timelineStart := time.Now()
	// US-017: shared client. Posture unchanged (verified TLS, environment
	// proxy) — an OAuth2 client-secret exchange must not inherit the user's
	// proxy or a "disable SSL verification" toggle.
	res, err := sharedCredentialHTTPClient().Do(tokenReq)
	duration := time.Since(timelineStart).Milliseconds()
	if err != nil {
		timelineEntry := oauth2TimelineItemFromRequest(tokenReq, nil, duration, err)
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	defer func() { _ = res.Body.Close() }()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		timelineEntry := oauth2TimelineItemFromRequest(tokenReq, res, duration, readErr)
		return oauth2TokenResponse{}, &timelineEntry, readErr
	}
	timelineEntry := oauth2TimelineItemFromRequest(tokenReq, res, duration, nil)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		err := fmt.Errorf("OAuth2 token request failed with %s: %s", res.Status, strings.TrimSpace(string(body)))
		timelineEntry.Error = err.Error()
		timelineEntry.Message = fmt.Sprintf("%s %s -> %d", timelineEntry.Method, timelineEntry.URL, res.StatusCode)
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	payload := map[string]interface{}{}
	if err := json.Unmarshal(body, &payload); err != nil {
		parseErr := fmt.Errorf("parse OAuth2 token response: %w", err)
		timelineEntry.Error = parseErr.Error()
		return oauth2TokenResponse{}, &timelineEntry, parseErr
	}
	response := parseOAuth2TokenResponse(payload, time.Now())
	if response.AccessToken == "" {
		err := errors.New("OAuth2 token response did not include access_token")
		timelineEntry.Error = err.Error()
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	if oauth2TokenValue(response, cfg.TokenSource) == "" {
		err := fmt.Errorf("OAuth2 token response did not include %s", firstNonEmpty(cfg.TokenSource, "access_token"))
		timelineEntry.Error = err.Error()
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	return response, &timelineEntry, nil
}

type oauth2AuthorizationCallback struct {
	Code     string
	State    string
	Timeline []TimelineItem
}

type oauth2AuthorizationResult struct {
	callback oauth2AuthorizationCallback
	err      error
}

type oauth2AuthorizationWaiter struct {
	CallbackURL string
	Receive     func(context.Context) (oauth2AuthorizationCallback, error)
	Shutdown    func(context.Context) error
}

type oauth2ImplicitCallback struct {
	Tokens   map[string]interface{}
	Timeline []TimelineItem
}

type oauth2ImplicitResult struct {
	callback oauth2ImplicitCallback
	err      error
}

type oauth2ImplicitWaiter struct {
	CallbackURL string
	Receive     func(context.Context) (oauth2ImplicitCallback, error)
	Shutdown    func(context.Context) error
}

// Read-only: normalizePreferences copies its argument and returns a bool.
func (a *App) oauth2ShouldUseSystemBrowser() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return normalizePreferences(a.state.Preferences).OAuth2UseSystemBrowser
}

func (a *App) openOAuth2AuthorizationURL(authorizeURL, callbackURL, grantType string) error {
	if a.oauth2ShouldUseSystemBrowser() {
		opener := a.oauth2OpenURL
		if opener == nil {
			opener = defaultOAuth2OpenURL
		}
		return opener(a.ctx, authorizeURL)
	}
	if a.ctx != nil {
		opener := a.oauth2OpenInAppURL
		if opener == nil {
			opener = defaultOAuth2OpenInAppURL
		}
		return opener(a.ctx, oauth2AuthorizationBrowserRequest{
			AuthorizeURL: authorizeURL,
			CallbackURL:  callbackURL,
			GrantType:    grantType,
		})
	}
	opener := a.oauth2OpenURL
	if opener == nil {
		opener = defaultOAuth2OpenURL
	}
	return opener(a.ctx, authorizeURL)
}

func (a *App) requestOAuth2AuthorizationCodeTokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, []TimelineItem, error) {
	if strings.TrimSpace(cfg.AuthorizationURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 authorization URL is required")
	}
	if strings.TrimSpace(cfg.AccessTokenURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 access token URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 client ID is required")
	}
	codeVerifier := ""
	codeChallenge := ""
	if cfg.PKCE {
		var err error
		codeVerifier, err = oauth2CodeVerifier()
		if err != nil {
			return oauth2TokenResponse{}, nil, err
		}
		codeChallenge = oauth2CodeChallenge(codeVerifier)
	}
	waiter, err := a.startOAuth2AuthorizationWaiter(cfg.CallbackURL)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = waiter.Shutdown(ctx)
	}()
	authorizeURL, err := oauth2AuthorizationCodeURL(cfg, waiter.CallbackURL, codeChallenge)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	if err := a.openOAuth2AuthorizationURL(authorizeURL, waiter.CallbackURL, "authorization_code"); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	timeout := a.oauth2CallbackTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	baseCtx := context.Background()
	if a.ctx != nil {
		baseCtx = a.ctx
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	callback, err := waiter.Receive(ctx)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	response, tokenEntry, err := requestOAuth2AuthorizationCodeTokenWithTimeline(cfg, callback.Code, codeVerifier, waiter.CallbackURL)
	timelineEntries := append([]TimelineItem{}, callback.Timeline...)
	if tokenEntry != nil {
		timelineEntries = append(timelineEntries, *tokenEntry)
	}
	if err != nil {
		return oauth2TokenResponse{}, timelineEntries, err
	}
	return response, timelineEntries, nil
}

func (a *App) requestOAuth2ImplicitTokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, []TimelineItem, error) {
	if strings.TrimSpace(cfg.AuthorizationURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 authorization URL is required")
	}
	waiter, err := a.startOAuth2ImplicitWaiter(cfg.CallbackURL)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = waiter.Shutdown(ctx)
	}()
	authorizeURL, err := oauth2ImplicitAuthorizationURL(cfg, waiter.CallbackURL)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	if err := a.openOAuth2AuthorizationURL(authorizeURL, waiter.CallbackURL, "implicit"); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	timeout := a.oauth2CallbackTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	baseCtx := context.Background()
	if a.ctx != nil {
		baseCtx = a.ctx
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	callback, err := waiter.Receive(ctx)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	response, err := oauth2ImplicitTokenResponse(callback.Tokens, cfg)
	if err != nil {
		return oauth2TokenResponse{}, callback.Timeline, err
	}
	return response, callback.Timeline, nil
}

func oauth2CodeVerifier() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate OAuth2 PKCE verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func oauth2CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func oauth2AuthorizationCodeURL(cfg OAuth2Auth, callbackURL, codeChallenge string) (string, error) {
	authorizeURL, err := url.Parse(strings.TrimSpace(cfg.AuthorizationURL))
	if err != nil {
		return "", fmt.Errorf("parse OAuth2 authorization URL: %w", err)
	}
	query := authorizeURL.Query()
	query.Add("response_type", "code")
	query.Add("client_id", cfg.ClientID)
	if callbackURL != "" {
		query.Add("redirect_uri", callbackURL)
	}
	if cfg.Scope != "" {
		query.Add("scope", cfg.Scope)
	}
	if cfg.PKCE {
		query.Add("code_challenge", codeChallenge)
		query.Add("code_challenge_method", "S256")
	}
	if cfg.State != "" {
		query.Add("state", cfg.State)
	}
	for _, param := range cfg.AuthorizationAdditionalParams {
		if !param.Enabled || strings.TrimSpace(param.Name) == "" {
			continue
		}
		if normalizeOAuth2AdditionalPlacement(param.SendIn) == "queryparams" {
			query.Add(param.Name, param.Value)
		}
	}
	authorizeURL.RawQuery = query.Encode()
	return authorizeURL.String(), nil
}

func oauth2ImplicitAuthorizationURL(cfg OAuth2Auth, callbackURL string) (string, error) {
	authorizeURL, err := url.Parse(strings.TrimSpace(cfg.AuthorizationURL))
	if err != nil {
		return "", fmt.Errorf("parse OAuth2 authorization URL: %w", err)
	}
	query := authorizeURL.Query()
	query.Add("response_type", "token")
	query.Add("client_id", cfg.ClientID)
	if callbackURL != "" {
		query.Add("redirect_uri", callbackURL)
	}
	if cfg.Scope != "" {
		query.Add("scope", cfg.Scope)
	}
	if cfg.State != "" {
		query.Add("state", cfg.State)
	}
	for _, param := range cfg.AuthorizationAdditionalParams {
		if !param.Enabled || strings.TrimSpace(param.Name) == "" {
			continue
		}
		if normalizeOAuth2AdditionalPlacement(param.SendIn) == "queryparams" {
			query.Add(param.Name, param.Value)
		}
	}
	authorizeURL.RawQuery = query.Encode()
	return authorizeURL.String(), nil
}

func (a *App) startOAuth2AuthorizationWaiter(callbackURL string) (oauth2AuthorizationWaiter, error) {
	effectiveCallbackURL := oauth2EffectiveBrowserCallbackURL(callbackURL)
	if oauth2CallbackCanUseLoopback(effectiveCallbackURL) {
		return startOAuth2AuthorizationWaiter(effectiveCallbackURL)
	}
	effectiveURL, err := oauth2NormalizeExternalCallbackURL(effectiveCallbackURL)
	if err != nil {
		return oauth2AuthorizationWaiter{}, err
	}
	resultCh := make(chan oauth2AuthorizationResult, 1)
	a.oauth2PendingMu.Lock()
	a.oauth2Authorization[effectiveURL] = resultCh
	a.oauth2PendingMu.Unlock()
	return oauth2AuthorizationWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2AuthorizationCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2AuthorizationCallback{}, fmt.Errorf("OAuth2 authorization callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: func(context.Context) error {
			a.oauth2PendingMu.Lock()
			delete(a.oauth2Authorization, effectiveURL)
			a.oauth2PendingMu.Unlock()
			return nil
		},
	}, nil
}

func (a *App) startOAuth2ImplicitWaiter(callbackURL string) (oauth2ImplicitWaiter, error) {
	effectiveCallbackURL := oauth2EffectiveBrowserCallbackURL(callbackURL)
	if oauth2CallbackCanUseLoopback(effectiveCallbackURL) {
		return startOAuth2ImplicitWaiter(effectiveCallbackURL)
	}
	effectiveURL, err := oauth2NormalizeExternalCallbackURL(effectiveCallbackURL)
	if err != nil {
		return oauth2ImplicitWaiter{}, err
	}
	resultCh := make(chan oauth2ImplicitResult, 1)
	a.oauth2PendingMu.Lock()
	a.oauth2Implicit[effectiveURL] = resultCh
	a.oauth2PendingMu.Unlock()
	return oauth2ImplicitWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2ImplicitCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2ImplicitCallback{}, fmt.Errorf("OAuth2 implicit callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: func(context.Context) error {
			a.oauth2PendingMu.Lock()
			delete(a.oauth2Implicit, effectiveURL)
			a.oauth2PendingMu.Unlock()
			return nil
		},
	}, nil
}

func oauth2EffectiveBrowserCallbackURL(callbackURL string) string {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		return brunoOAuth2DefaultCallbackURL
	}
	return raw
}

func oauth2CallbackCanUseLoopback(callbackURL string) bool {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" {
		return false
	}
	return oauth2LoopbackHost(parsed.Hostname())
}

func oauth2NormalizeExternalCallbackURL(callbackURL string) (string, error) {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		raw = brunoOAuth2DefaultCallbackURL
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse OAuth2 hosted callback URL: %w", err)
	}
	if parsed.Scheme == "" {
		return "", errors.New("OAuth2 hosted callback URL requires a URL scheme")
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() == "" {
		return "", errors.New("OAuth2 hosted callback URL requires a host")
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (a *App) CompleteOAuth2Callback(rawURL string) (bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false, fmt.Errorf("parse OAuth2 callback URL: %w", err)
	}
	a.oauth2PendingMu.Lock()
	for callbackURL, resultCh := range a.oauth2Authorization {
		if !oauth2CallbackMatchesPending(parsed, callbackURL) {
			continue
		}
		delete(a.oauth2Authorization, callbackURL)
		result := oauth2AuthorizationResultFromURL(callbackURL, parsed)
		a.oauth2PendingMu.Unlock()
		resultCh <- result
		return true, result.err
	}
	for callbackURL, resultCh := range a.oauth2Implicit {
		if !oauth2CallbackMatchesPending(parsed, callbackURL) {
			continue
		}
		delete(a.oauth2Implicit, callbackURL)
		result := oauth2ImplicitResultFromURL(callbackURL, parsed)
		a.oauth2PendingMu.Unlock()
		resultCh <- result
		return true, result.err
	}
	a.oauth2PendingMu.Unlock()
	return false, nil
}

func oauth2CallbackMatchesPending(callback *url.URL, expected string) bool {
	return oauth2ExternalCallbackMatches(callback, expected) || oauth2IsAppProtocolCallbackURL(callback)
}

func oauth2ExternalCallbackMatches(callback *url.URL, expected string) bool {
	if callback == nil {
		return false
	}
	expectedURL, err := url.Parse(expected)
	if err != nil {
		return false
	}
	if callback.Scheme != expectedURL.Scheme || !strings.EqualFold(callback.Host, expectedURL.Host) {
		return false
	}
	return strings.HasPrefix(callback.EscapedPath(), expectedURL.EscapedPath())
}

func oauth2IsAppProtocolCallback(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return oauth2IsAppProtocolCallbackURL(parsed)
}

func oauth2IsAppProtocolCallbackURL(callback *url.URL) bool {
	if callback == nil {
		return false
	}
	scheme := strings.ToLower(callback.Scheme)
	if scheme != "bruno" && scheme != "liteapi" {
		return false
	}
	if !strings.EqualFold(callback.Host, oauth2ProtocolCallbackHost) {
		return false
	}
	return strings.TrimRight(callback.EscapedPath(), "/") == oauth2ProtocolCallbackPath
}

func oauth2AuthorizationResultFromURL(callbackURL string, callback *url.URL) oauth2AuthorizationResult {
	query := callback.Query()
	status := http.StatusOK
	statusText := http.StatusText(status)
	callbackErr := error(nil)
	code := query.Get("code")
	if oauthErr := query.Get("error"); oauthErr != "" {
		status = http.StatusBadRequest
		statusText = http.StatusText(status)
		callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, query.Get("error_description"))
	} else if strings.TrimSpace(code) == "" {
		status = http.StatusBadRequest
		statusText = http.StatusText(status)
		callbackErr = errors.New("OAuth2 callback did not include code")
	}
	timelineEntry := TimelineItem{
		At:         time.Now(),
		Method:     http.MethodGet,
		URL:        oauth2ExternalCallbackTimelineURL(callbackURL, callback, true),
		Status:     status,
		StatusText: statusText,
		Message:    fmt.Sprintf("%s %s -> %d", http.MethodGet, oauth2ExternalCallbackTimelineURL(callbackURL, callback, true), status),
	}
	if callbackErr != nil {
		timelineEntry.Error = callbackErr.Error()
	}
	return oauth2AuthorizationResult{
		callback: oauth2AuthorizationCallback{Code: code, State: query.Get("state"), Timeline: []TimelineItem{timelineEntry}},
		err:      callbackErr,
	}
}

func oauth2ImplicitResultFromURL(callbackURL string, callback *url.URL) oauth2ImplicitResult {
	values := callback.Query()
	if callback.Fragment != "" {
		fragmentValues, err := url.ParseQuery(callback.Fragment)
		if err == nil {
			values = fragmentValues
		}
	}
	status := http.StatusOK
	callbackErr := error(nil)
	if oauthErr := values.Get("error"); oauthErr != "" {
		status = http.StatusBadRequest
		callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, values.Get("error_description"))
	} else if strings.TrimSpace(values.Get("access_token")) == "" {
		status = http.StatusBadRequest
		callbackErr = errors.New("OAuth2 implicit callback did not include access_token")
	}
	timelineEntry := TimelineItem{
		At:         time.Now(),
		Method:     http.MethodGet,
		URL:        oauth2ExternalCallbackTimelineURL(callbackURL, callback, false),
		Status:     status,
		StatusText: http.StatusText(status),
		Message:    fmt.Sprintf("%s %s -> %d", http.MethodGet, oauth2ExternalCallbackTimelineURL(callbackURL, callback, false), status),
	}
	if callbackErr != nil {
		timelineEntry.Error = callbackErr.Error()
	}
	result := oauth2ImplicitResult{callback: oauth2ImplicitCallback{Timeline: []TimelineItem{timelineEntry}}, err: callbackErr}
	if callbackErr == nil {
		result.callback.Tokens = oauth2TokenPayloadFromValues(values)
	}
	return result
}

func oauth2ExternalCallbackTimelineURL(callbackURL string, callback *url.URL, includeQuery bool) string {
	if oauth2IsAppProtocolCallbackURL(callback) {
		display := *callback
		if !includeQuery {
			display.RawQuery = ""
		}
		display.Fragment = ""
		return display.String()
	}
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	if callback != nil {
		parsed.Path = callback.EscapedPath()
		if includeQuery {
			parsed.RawQuery = callback.RawQuery
		} else {
			parsed.RawQuery = ""
		}
	}
	parsed.Fragment = ""
	return parsed.String()
}

func startOAuth2AuthorizationWaiter(callbackURL string) (oauth2AuthorizationWaiter, error) {
	effectiveURL, listenAddress, callbackPath, err := oauth2CallbackListenConfig(callbackURL)
	if err != nil {
		return oauth2AuthorizationWaiter{}, err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return oauth2AuthorizationWaiter{}, fmt.Errorf("listen for OAuth2 callback: %w", err)
	}
	effectiveURL = oauth2CallbackURLWithListenerPort(effectiveURL, listener.Addr())
	resultCh := make(chan struct {
		callback oauth2AuthorizationCallback
		err      error
	}, 1)
	server := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		status := http.StatusOK
		statusText := "OK"
		query := r.URL.Query()
		callbackErr := error(nil)
		code := query.Get("code")
		if oauthErr := query.Get("error"); oauthErr != "" {
			status = http.StatusBadRequest
			statusText = http.StatusText(status)
			callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, query.Get("error_description"))
		} else if strings.TrimSpace(code) == "" {
			status = http.StatusBadRequest
			statusText = http.StatusText(status)
			callbackErr = errors.New("OAuth2 callback did not include code")
		}
		callbackURLValue := oauth2CallbackRequestURL(effectiveURL, r)
		timelineEntry := TimelineItem{
			At:         time.Now(),
			Method:     http.MethodGet,
			URL:        callbackURLValue,
			Status:     status,
			StatusText: statusText,
			Message:    fmt.Sprintf("%s %s -> %d", http.MethodGet, callbackURLValue, status),
		}
		if callbackErr != nil {
			timelineEntry.Error = callbackErr.Error()
		}
		select {
		case resultCh <- struct {
			callback oauth2AuthorizationCallback
			err      error
		}{callback: oauth2AuthorizationCallback{Code: code, State: query.Get("state"), Timeline: []TimelineItem{timelineEntry}}, err: callbackErr}:
		default:
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if callbackErr != nil {
			_, _ = io.WriteString(w, "<html><body>OAuth2 authorization failed. You can return to LiteAPI.</body></html>")
			return
		}
		_, _ = io.WriteString(w, "<html><body>OAuth2 authorization complete. You can return to LiteAPI.</body></html>")
	})
	server.Handler = mux
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case resultCh <- struct {
				callback oauth2AuthorizationCallback
				err      error
			}{err: err}:
			default:
			}
		}
	}()
	return oauth2AuthorizationWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2AuthorizationCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2AuthorizationCallback{}, fmt.Errorf("OAuth2 authorization callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: server.Shutdown,
	}, nil
}

func startOAuth2ImplicitWaiter(callbackURL string) (oauth2ImplicitWaiter, error) {
	effectiveURL, listenAddress, callbackPath, err := oauth2CallbackListenConfig(callbackURL)
	if err != nil {
		return oauth2ImplicitWaiter{}, err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return oauth2ImplicitWaiter{}, fmt.Errorf("listen for OAuth2 callback: %w", err)
	}
	effectiveURL = oauth2CallbackURLWithListenerPort(effectiveURL, listener.Addr())
	fragmentURL := oauth2ImplicitFragmentCallbackURL(effectiveURL)
	fragmentParsed, err := url.Parse(fragmentURL)
	if err != nil {
		_ = listener.Close()
		return oauth2ImplicitWaiter{}, fmt.Errorf("parse OAuth2 implicit fragment callback URL: %w", err)
	}
	fragmentPath := fragmentParsed.EscapedPath()
	if fragmentPath == "" {
		fragmentPath = "/"
	}
	resultCh := make(chan struct {
		callback oauth2ImplicitCallback
		err      error
	}, 1)
	server := &http.Server{}
	mux := http.NewServeMux()
	var timelineMu sync.Mutex
	callbackTimeline := []TimelineItem{}
	recordCallbackTimeline := func(entry TimelineItem) []TimelineItem {
		timelineMu.Lock()
		defer timelineMu.Unlock()
		callbackTimeline = append(callbackTimeline, entry)
		return append([]TimelineItem{}, callbackTimeline...)
	}
	currentCallbackTimeline := func(extra TimelineItem) []TimelineItem {
		timelineMu.Lock()
		defer timelineMu.Unlock()
		out := append([]TimelineItem{}, callbackTimeline...)
		out = append(out, extra)
		return out
	}
	complete := func(tokens map[string]interface{}, timeline []TimelineItem, err error) {
		select {
		case resultCh <- struct {
			callback oauth2ImplicitCallback
			err      error
		}{callback: oauth2ImplicitCallback{Tokens: tokens, Timeline: timeline}, err: err}:
		default:
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case callbackPath:
			query := r.URL.Query()
			status := http.StatusOK
			callbackErr := error(nil)
			tokens := map[string]interface{}(nil)
			if oauthErr := query.Get("error"); oauthErr != "" {
				status = http.StatusBadRequest
				callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, query.Get("error_description"))
			} else if query.Get("access_token") != "" {
				tokens = oauth2TokenPayloadFromValues(query)
			}
			timelineEntry := oauth2CallbackTimelineItem(effectiveURL, r, status, callbackErr, false)
			timeline := recordCallbackTimeline(timelineEntry)
			if callbackErr != nil || tokens != nil {
				complete(tokens, timeline, callbackErr)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			if callbackErr != nil {
				_, _ = io.WriteString(w, "<html><body>OAuth2 authorization failed. You can return to LiteAPI.</body></html>")
				return
			}
			if tokens != nil {
				_, _ = io.WriteString(w, "<html><body>OAuth2 authorization complete. You can return to LiteAPI.</body></html>")
				return
			}
			_, _ = io.WriteString(w, oauth2ImplicitCallbackHTML(fragmentPath))
		case fragmentPath:
			values, parseErr := oauth2ImplicitFragmentValues(r)
			status := http.StatusOK
			callbackErr := parseErr
			if callbackErr == nil {
				if oauthErr := values.Get("error"); oauthErr != "" {
					status = http.StatusBadRequest
					callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, values.Get("error_description"))
				} else if strings.TrimSpace(values.Get("access_token")) == "" {
					status = http.StatusBadRequest
					callbackErr = errors.New("OAuth2 implicit callback did not include access_token")
				}
			} else {
				status = http.StatusBadRequest
			}
			timelineEntry := oauth2CallbackTimelineItem(effectiveURL, r, status, callbackErr, false)
			timeline := currentCallbackTimeline(timelineEntry)
			if callbackErr != nil {
				complete(nil, timeline, callbackErr)
			} else {
				complete(oauth2TokenPayloadFromValues(values), timeline, nil)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			if callbackErr != nil {
				_, _ = io.WriteString(w, "OAuth2 authorization failed. You can return to LiteAPI.")
				return
			}
			_, _ = io.WriteString(w, "OAuth2 authorization complete. You can return to LiteAPI.")
		default:
			http.NotFound(w, r)
		}
	})
	server.Handler = mux
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case resultCh <- struct {
				callback oauth2ImplicitCallback
				err      error
			}{err: err}:
			default:
			}
		}
	}()
	return oauth2ImplicitWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2ImplicitCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2ImplicitCallback{}, fmt.Errorf("OAuth2 implicit callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: server.Shutdown,
	}, nil
}

func oauth2CallbackListenConfig(callbackURL string) (string, string, string, error) {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		raw = "http://127.0.0.1:0/oauth2/callback"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("parse OAuth2 callback URL: %w", err)
	}
	if parsed.Scheme != "http" {
		return "", "", "", errors.New("OAuth2 browser flow requires an http:// loopback callback URL")
	}
	host := parsed.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	if !oauth2LoopbackHost(host) {
		return "", "", "", errors.New("OAuth2 browser flow requires a localhost or 127.0.0.1 callback URL")
	}
	port := parsed.Port()
	if port == "" {
		port = "0"
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	listenHost := host
	if strings.EqualFold(listenHost, "localhost") {
		listenHost = "127.0.0.1"
	}
	parsed.Host = net.JoinHostPort(host, port)
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), net.JoinHostPort(listenHost, port), path, nil
}

func oauth2LoopbackHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), "[]")
	return normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1"
}

func oauth2CallbackURLWithListenerPort(callbackURL string, addr net.Addr) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil || port == "" {
		return callbackURL
	}
	parsed.Host = net.JoinHostPort(parsed.Hostname(), port)
	return parsed.String()
}

func oauth2CallbackRequestURL(callbackURL string, r *http.Request) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	parsed.Path = r.URL.Path
	parsed.RawQuery = r.URL.RawQuery
	parsed.Fragment = ""
	return parsed.String()
}

func oauth2CallbackRequestURLWithoutQuery(callbackURL string, r *http.Request) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	parsed.Path = r.URL.Path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func oauth2CallbackTimelineItem(callbackURL string, r *http.Request, status int, callbackErr error, includeQuery bool) TimelineItem {
	requestURL := oauth2CallbackRequestURLWithoutQuery(callbackURL, r)
	if includeQuery {
		requestURL = oauth2CallbackRequestURL(callbackURL, r)
	}
	statusText := cleanStatusText(status, fmt.Sprintf("%d %s", status, http.StatusText(status)))
	entry := TimelineItem{
		At:         time.Now(),
		Method:     strings.ToUpper(firstNonEmpty(r.Method, http.MethodGet)),
		URL:        requestURL,
		Status:     status,
		StatusText: statusText,
		Message:    fmt.Sprintf("%s %s -> %d", strings.ToUpper(firstNonEmpty(r.Method, http.MethodGet)), requestURL, status),
	}
	if callbackErr != nil {
		entry.Error = callbackErr.Error()
	}
	return entry
}

func oauth2ImplicitFragmentCallbackURL(callbackURL string) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	callbackPath := strings.TrimRight(parsed.EscapedPath(), "/")
	if callbackPath == "" {
		callbackPath = ""
	}
	parsed.Path = callbackPath + "/__liteapi_oauth2_fragment"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func oauth2ImplicitCallbackHTML(fragmentPath string) string {
	encodedPath, _ := json.Marshal(fragmentPath)
	return `<!doctype html><html><head><meta charset="utf-8"><title>OAuth2 authorization complete</title></head><body>OAuth2 authorization complete. You can return to LiteAPI.<script>
(function () {
  var payload = window.location.hash ? window.location.hash.slice(1) : (window.location.search ? window.location.search.slice(1) : "");
  fetch(` + string(encodedPath) + `, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: payload })
    .catch(function () {});
})();
</script></body></html>`
}

func oauth2ImplicitFragmentValues(r *http.Request) (url.Values, error) {
	switch r.Method {
	case http.MethodGet:
		return r.URL.Query(), nil
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("read OAuth2 implicit callback: %w", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, fmt.Errorf("parse OAuth2 implicit callback: %w", err)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("OAuth2 implicit callback method %s is not supported", r.Method)
	}
}

func oauth2TokenPayloadFromValues(values url.Values) map[string]interface{} {
	payload := map[string]interface{}{}
	for key, allValues := range values {
		if strings.TrimSpace(key) == "" || len(allValues) == 0 {
			continue
		}
		payload[key] = allValues[0]
	}
	return payload
}

func oauth2ImplicitTokenResponse(payload map[string]interface{}, cfg OAuth2Auth) (oauth2TokenResponse, error) {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if strings.TrimSpace(stringMapValue(payload, "token_type")) == "" {
		payload["token_type"] = "Bearer"
	}
	response := parseOAuth2TokenResponse(payload, time.Now())
	if response.AccessToken == "" {
		return oauth2TokenResponse{}, errors.New("no access token received from authorization server")
	}
	if oauth2TokenValue(response, cfg.TokenSource) == "" {
		return oauth2TokenResponse{}, fmt.Errorf("OAuth2 token response did not include %s", firstNonEmpty(cfg.TokenSource, "access_token"))
	}
	return response, nil
}

func optionalTimelineEntry(entry *TimelineItem) []TimelineItem {
	if entry == nil {
		return nil
	}
	return []TimelineItem{*entry}
}

func oauth2TimelineItemFromRequest(req *http.Request, res *http.Response, duration int64, requestErr error) TimelineItem {
	method := http.MethodPost
	targetURL := ""
	if req != nil {
		method = strings.ToUpper(firstNonEmpty(req.Method, http.MethodPost))
		if req.URL != nil {
			targetURL = req.URL.String()
		}
	}
	entry := TimelineItem{
		At:       time.Now(),
		Duration: duration,
		Method:   method,
		URL:      targetURL,
	}
	if res != nil {
		entry.Status = res.StatusCode
		entry.StatusText = cleanStatusText(res.StatusCode, res.Status)
	}
	if requestErr != nil {
		entry.Error = requestErr.Error()
		if entry.StatusText == "" {
			entry.StatusText = "Error"
		}
	}
	statusLabel := entry.StatusText
	if entry.Status > 0 {
		statusLabel = strconv.Itoa(entry.Status)
	}
	if strings.TrimSpace(statusLabel) == "" {
		statusLabel = "-"
	}
	entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
	return entry
}

func oauth2AdditionalParamsForGrant(cfg OAuth2Auth, grantType string) []OAuth2AdditionalParam {
	if grantType == "refresh_token" {
		return cfg.RefreshAdditionalParams
	}
	return cfg.TokenAdditionalParams
}

func legacyOAuth2AdditionalParams(params []KeyValue) []OAuth2AdditionalParam {
	out := make([]OAuth2AdditionalParam, 0, len(params))
	for _, param := range params {
		out = append(out, OAuth2AdditionalParam{
			Name:        param.Name,
			Value:       param.Value,
			SendIn:      "body",
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return out
}

func applyOAuth2AdditionalParams(req *http.Request, form url.Values, params []OAuth2AdditionalParam) error {
	for _, param := range params {
		if !param.Enabled || strings.TrimSpace(param.Name) == "" {
			continue
		}
		switch normalizeOAuth2AdditionalPlacement(param.SendIn) {
		case "headers":
			req.Header.Set(param.Name, param.Value)
		case "queryparams":
			query := req.URL.Query()
			query.Add(param.Name, param.Value)
			req.URL.RawQuery = query.Encode()
		case "body":
			form.Set(param.Name, param.Value)
		default:
			return fmt.Errorf("unsupported OAuth2 additional parameter placement %s", param.SendIn)
		}
	}
	return nil
}

func parseOAuth2TokenResponse(payload map[string]interface{}, now time.Time) oauth2TokenResponse {
	values := map[string]interface{}{}
	for key, value := range payload {
		values[key] = value
	}
	values["created_at"] = now.UnixMilli()
	response := oauth2TokenResponse{
		AccessToken:  stringMapValue(payload, "access_token"),
		IDToken:      stringMapValue(payload, "id_token"),
		RefreshToken: stringMapValue(payload, "refresh_token"),
		Values:       values,
		CreatedAt:    now,
	}
	if expiresIn, ok := numberMapValue(payload, "expires_in"); ok {
		response.ExpiresAt = now.Add(time.Duration(expiresIn * float64(time.Second)))
		response.Values["expires_at"] = response.ExpiresAt.UnixMilli()
	}
	return response
}

func (response oauth2TokenResponse) credentialValues() map[string]interface{} {
	values := map[string]interface{}{}
	for key, value := range response.Values {
		values[key] = value
	}
	if response.AccessToken != "" {
		values["access_token"] = response.AccessToken
	}
	if response.IDToken != "" {
		values["id_token"] = response.IDToken
	}
	if response.RefreshToken != "" {
		values["refresh_token"] = response.RefreshToken
	}
	if !response.CreatedAt.IsZero() {
		values["created_at"] = response.CreatedAt.UnixMilli()
	}
	if !response.ExpiresAt.IsZero() {
		values["expires_at"] = response.ExpiresAt.UnixMilli()
	}
	return values
}

func oauth2CacheCredentialsID(cacheKey string) string {
	index := strings.LastIndex(cacheKey, "|")
	if index < 0 {
		return "credentials"
	}
	return firstNonEmpty(cacheKey[index+1:], "credentials")
}

func oauth2CredentialVariablesFromCache(cache map[string]oauth2TokenResponse) map[string]interface{} {
	variables := map[string]interface{}{}
	for cacheKey, response := range cache {
		credentialsID := oauth2CacheCredentialsID(cacheKey)
		for key, value := range response.credentialValues() {
			variables["$oauth2."+credentialsID+"."+key] = value
		}
	}
	return variables
}

func (a *App) oauth2CredentialVariablesSnapshot() map[string]interface{} {
	a.oauth2Mu.Lock()
	defer a.oauth2Mu.Unlock()
	return oauth2CredentialVariablesFromCache(a.oauth2)
}

func (a *App) resetOAuth2Credential(credentialsID string) error {
	credentialsID = strings.TrimSpace(credentialsID)
	if credentialsID == "" {
		return errors.New("credentialId must be a non-empty string")
	}
	a.oauth2Mu.Lock()
	defer a.oauth2Mu.Unlock()
	for cacheKey := range a.oauth2 {
		if oauth2CacheCredentialsID(cacheKey) == credentialsID {
			delete(a.oauth2, cacheKey)
		}
	}
	return nil
}

func oauth2TokenValue(response oauth2TokenResponse, source string) string {
	if source == "id_token" {
		return response.IDToken
	}
	return response.AccessToken
}

func stringMapValue(values map[string]interface{}, key string) string {
	if raw, ok := values[key]; ok {
		switch value := raw.(type) {
		case string:
			return value
		case fmt.Stringer:
			return value.String()
		default:
			return fmt.Sprint(value)
		}
	}
	return ""
}

func numberMapValue(values map[string]interface{}, key string) (float64, bool) {
	raw, ok := values[key]
	if !ok {
		return 0, false
	}
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func interpolateOAuth2Auth(auth OAuth2Auth, vars map[string]string) OAuth2Auth {
	out := OAuth2Auth{
		GrantType:            interpolate(auth.GrantType, vars),
		CallbackURL:          interpolate(auth.CallbackURL, vars),
		AuthorizationURL:     interpolate(auth.AuthorizationURL, vars),
		AccessTokenURL:       interpolate(auth.AccessTokenURL, vars),
		RefreshTokenURL:      interpolate(auth.RefreshTokenURL, vars),
		Username:             interpolate(auth.Username, vars),
		Password:             interpolate(auth.Password, vars),
		ClientID:             interpolate(auth.ClientID, vars),
		ClientSecret:         interpolate(auth.ClientSecret, vars),
		Scope:                interpolate(auth.Scope, vars),
		State:                interpolate(auth.State, vars),
		PKCE:                 auth.PKCE,
		CredentialsPlacement: interpolate(auth.CredentialsPlacement, vars),
		CredentialsID:        interpolate(auth.CredentialsID, vars),
		TokenSource:          interpolate(auth.TokenSource, vars),
		TokenPlacement:       interpolate(auth.TokenPlacement, vars),
		TokenHeaderPrefix:    interpolate(auth.TokenHeaderPrefix, vars),
		TokenQueryKey:        interpolate(auth.TokenQueryKey, vars),
		AutoFetchToken:       auth.AutoFetchToken,
		AutoRefreshToken:     auth.AutoRefreshToken,
	}
	out.AuthorizationAdditionalParams = interpolateOAuth2AdditionalParams(auth.AuthorizationAdditionalParams, vars)
	out.TokenAdditionalParams = interpolateOAuth2AdditionalParams(auth.TokenAdditionalParams, vars)
	out.RefreshAdditionalParams = interpolateOAuth2AdditionalParams(auth.RefreshAdditionalParams, vars)
	for _, param := range auth.AdditionalParams {
		out.AdditionalParams = append(out.AdditionalParams, KeyValue{
			Name:        interpolate(param.Name, vars),
			Value:       interpolate(param.Value, vars),
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return out
}

func interpolateOAuth2AdditionalParams(params []OAuth2AdditionalParam, vars map[string]string) []OAuth2AdditionalParam {
	out := make([]OAuth2AdditionalParam, 0, len(params))
	for _, param := range params {
		out = append(out, OAuth2AdditionalParam{
			Name:        interpolate(param.Name, vars),
			Value:       interpolate(param.Value, vars),
			SendIn:      interpolate(param.SendIn, vars),
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return out
}

func applyOAuth2Token(req *http.Request, auth OAuth2Auth, token string, vars map[string]string) {
	placement := strings.TrimSpace(interpolate(auth.TokenPlacement, vars))
	if placement == "" {
		placement = "header"
	}
	if placement == "url" || placement == "query" {
		key := firstNonEmpty(interpolate(auth.TokenQueryKey, vars), "access_token")
		q := req.URL.Query()
		q.Set(key, token)
		req.URL.RawQuery = q.Encode()
		return
	}
	prefix := interpolate(auth.TokenHeaderPrefix, vars)
	if prefix == "" {
		prefix = "Bearer"
	}
	req.Header.Set("Authorization", strings.TrimSpace(prefix+" "+token))
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

// writeCollectionFileLocked writes one collection file, skipping the write when
// the bytes on disk already match (US-015).
//
// Saving a single request previously rewrote bruno.json, collection.bru, every
// environment file and every request file in the collection — 4.16 ms/op for
// one 50-request collection at the Phase 0 baseline, on every save.
//
// The gate is a fingerprint of what THIS App last wrote, falling back to
// reading the file when it has no record. Two consequences worth stating:
//
//   - The first save after startup pays one read per file and then nothing.
//   - A file edited outside LiteAPI is left alone when our content has not
//     changed. That is a deliberate improvement, not an oversight: the previous
//     behaviour silently clobbered an external edit whenever the user saved any
//     unrelated request. The collection watcher is what brings external edits
//     back into the app, and it is unaffected — skipping a write leaves the
//     bytes on disk exactly as the watcher last saw them.
//
// a.mu must be held.
func (a *App) writeCollectionFileLocked(path string, data []byte) error {
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
	if a.collectionFileFingerprints == nil {
		a.collectionFileFingerprints = map[string]string{}
	}
	if known, ok := a.collectionFileFingerprints[path]; ok {
		if known == fingerprint {
			return nil
		}
	} else if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		a.collectionFileFingerprints[path] = fingerprint
		return nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		// Do not record on failure; the next save must retry rather than
		// conclude the file is already correct.
		return err
	}
	a.collectionFileFingerprints[path] = fingerprint
	return nil
}

func (a *App) writeCollectionFilesLocked(collection *Collection) error {
	if collection.Path == "" {
		return errors.New("collection path is empty")
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		return err
	}
	ensureRequestFilePaths(collection, requestFileExtensionForCollection(*collection))
	if err := a.storeCollectionEnvironmentSecretsLocked(collection); err != nil {
		return err
	}
	if collection.Format == "yml" {
		if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "opencollection.yml"), []byte(stringifyYAMLCollection(*collection))); err != nil {
			return err
		}
		for _, item := range collection.Items {
			content, err := stringifyYAMLRequest(item)
			if err != nil {
				return err
			}
			target := requestFilePath(*collection, item, ".yml")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := a.writeCollectionFileLocked(target, []byte(content)); err != nil {
				return err
			}
		}
		a.seedCollectionWatchFingerprintLocked(collection.Path)
		return nil
	}
	config := map[string]interface{}{
		"name":   collection.Name,
		"type":   "collection",
		"ignore": []string{"node_modules", ".git"},
	}
	config["version"] = firstNonEmpty(collection.Version, "1")
	if transport.HasProxyConfig(collection.Proxy) {
		config["proxy"] = jsonProxyConfig(collection.Proxy)
	}
	if transport.HasClientCertificates(collection.ClientCertificates) {
		config["clientCertificates"] = jsonClientCertificates(collection.ClientCertificates)
	}
	if hasCollectionPresets(collection.Presets) {
		config["presets"] = jsonCollectionPresets(collection.Presets)
	}
	if hasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = jsonCollectionProtobuf(collection.Protobuf)
	}
	if len(collection.OpenAPI) > 0 {
		config["openapi"] = jsonOpenAPISyncConfigs(collection.OpenAPI)
	}
	configData, _ := json.MarshalIndent(config, "", "  ")
	if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "bruno.json"), configData); err != nil {
		return err
	}
	if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "collection.bru"), []byte(bru.StringifyBruCollection(*collection))); err != nil {
		return err
	}
	if len(collection.Environments) > 0 {
		envPath := filepath.Join(collection.Path, "environments")
		if err := os.MkdirAll(envPath, 0o755); err != nil {
			return err
		}
		for _, env := range collection.Environments {
			filename := sanitizeFilename(env.Name)
			if filename == "" {
				filename = env.ID
			}
			if err := a.writeCollectionFileLocked(filepath.Join(envPath, filename+".bru"), []byte(bru.StringifyBruEnvironment(env))); err != nil {
				return err
			}
		}
	}
	for _, item := range collection.Items {
		content := bru.StringifyBru(item)
		target := requestFilePath(*collection, item, ".bru")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := a.writeCollectionFileLocked(target, []byte(content)); err != nil {
			return err
		}
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	return nil
}

func (a *App) writeCollectionNameMetadataLocked(collection *Collection) error {
	if collection.Path == "" {
		return errors.New("collection path is empty")
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		return err
	}
	var err error
	if strings.EqualFold(collection.Format, "yml") || strings.EqualFold(collection.Format, "yaml") || fileExists(filepath.Join(collection.Path, "opencollection.yml")) {
		err = writeYAMLCollectionNameMetadata(collection)
	} else {
		err = writeBruCollectionNameMetadata(collection)
	}
	if err == nil {
		a.seedCollectionWatchFingerprintLocked(collection.Path)
	}
	return err
}

func writeYAMLCollectionNameMetadata(collection *Collection) error {
	target := filepath.Join(collection.Path, "opencollection.yml")
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(target, []byte(stringifyYAMLCollection(*collection)), 0o600)
		}
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return os.WriteFile(target, []byte(stringifyYAMLCollection(*collection)), 0o600)
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse opencollection.yml: %w", err)
	}
	if root == nil {
		root = map[string]interface{}{}
	}
	info, _ := root["info"].(map[string]interface{})
	if info == nil {
		info = map[string]interface{}{}
		root["info"] = info
	}
	info["name"] = collection.Name
	if _, ok := info["version"]; !ok {
		info["version"] = firstNonEmpty(collection.Version, "1")
	}
	if _, ok := root["opencollection"]; !ok {
		root["opencollection"] = "1.0.0"
	}
	updated, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(target, updated, 0o600)
}

func writeBruCollectionNameMetadata(collection *Collection) error {
	target := filepath.Join(collection.Path, "bruno.json")
	config := map[string]interface{}{}
	if data, err := os.ReadFile(target); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse bruno.json: %w", err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	config["name"] = collection.Name
	if _, ok := config["type"]; !ok {
		config["type"] = "collection"
	}
	if _, ok := config["version"]; !ok {
		config["version"] = firstNonEmpty(collection.Version, "1")
	}
	if _, ok := config["ignore"]; !ok {
		config["ignore"] = []string{"node_modules", ".git"}
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, configData, 0o600)
}

func writeClonedCollectionRootMetadata(source, cloned *Collection) error {
	if strings.EqualFold(cloned.Format, "yml") || strings.EqualFold(cloned.Format, "yaml") || fileExists(filepath.Join(source.Path, "opencollection.yml")) {
		cloned.Format = "yml"
		return writeClonedYAMLCollectionRootMetadata(source, cloned)
	}
	cloned.Format = "bru"
	return writeClonedBruCollectionRootMetadata(source, cloned)
}

func writeClonedYAMLCollectionRootMetadata(source, cloned *Collection) error {
	sourceConfigPath := filepath.Join(source.Path, "opencollection.yml")
	targetConfigPath := filepath.Join(cloned.Path, "opencollection.yml")
	data, err := os.ReadFile(sourceConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(targetConfigPath, []byte(stringifyYAMLCollection(*cloned)), 0o600)
		}
		return err
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse opencollection.yml: %w", err)
	}
	if root == nil {
		root = map[string]interface{}{}
	}
	info, _ := root["info"].(map[string]interface{})
	if info == nil {
		info = map[string]interface{}{}
		root["info"] = info
	}
	info["name"] = cloned.Name
	if _, ok := info["version"]; !ok {
		info["version"] = firstNonEmpty(cloned.Version, "1")
	}
	if _, ok := root["opencollection"]; !ok {
		root["opencollection"] = "1.0.0"
	}
	updated, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(targetConfigPath, updated, 0o600)
}

func writeClonedBruCollectionRootMetadata(source, cloned *Collection) error {
	sourceConfigPath := filepath.Join(source.Path, "bruno.json")
	targetConfigPath := filepath.Join(cloned.Path, "bruno.json")
	config := map[string]interface{}{}
	if data, err := os.ReadFile(sourceConfigPath); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse bruno.json: %w", err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	config["name"] = cloned.Name
	if _, ok := config["type"]; !ok {
		config["type"] = "collection"
	}
	if _, ok := config["version"]; !ok {
		config["version"] = firstNonEmpty(cloned.Version, "1")
	}
	if _, ok := config["ignore"]; !ok {
		config["ignore"] = []string{"node_modules", ".git"}
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(targetConfigPath, configData, 0o600)
}

func copyCollectionFormatFiles(sourcePath, targetPath, format string) error {
	ext := ".bru"
	rootConfigName := "bruno.json"
	if strings.EqualFold(format, "yml") || strings.EqualFold(format, "yaml") || fileExists(filepath.Join(sourcePath, "opencollection.yml")) {
		ext = ".yml"
		rootConfigName = "opencollection.yml"
	}
	return filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != sourcePath && pathInside(targetPath, path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ext {
			return nil
		}
		if filepath.Dir(path) == sourcePath && filepath.Base(path) == rootConfigName {
			return nil
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		return copyCollectionFile(path, filepath.Join(targetPath, rel))
	})
}

func copyCollectionFile(sourcePath, targetPath string) (err error) {
	info, statErr := os.Stat(sourcePath)
	if statErr != nil {
		return statErr
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	// Read-only handle: a failed close cannot lose data.
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	// The write handle's close error must surface: it is where a deferred
	// write failure (ENOSPC, EDQUOT, a failing network filesystem) is reported,
	// and dropping it would report a truncated collection copy as a success.
	defer func() {
		if closeErr := target.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(target, source)
	return err
}

func (a *App) writeFolderConfigLocked(collection *Collection, folder FolderConfig) error {
	if strings.TrimSpace(collection.Path) == "" {
		return errors.New("collection path is empty")
	}
	folderPath := normalizeFolderPathKey(folder.Path)
	if folderPath == "" {
		return errors.New("folder path is required")
	}
	if strings.HasPrefix(folderPath, "../") || folderPath == ".." || filepath.IsAbs(folderPath) {
		return fmt.Errorf("invalid folder path %s", folder.Path)
	}
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	if !pathInside(collection.Path, targetDir) {
		return fmt.Errorf("folder path %s escapes collection", folder.Path)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	target := folderMetadataWritePath(*collection, targetDir)
	var content string
	if collection.Format == "yml" {
		content = stringifyYAMLFolder(folder)
	} else {
		content = bru.StringifyBruFolder(folder)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return err
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	return nil
}

func folderMetadataWritePath(collection Collection, targetDir string) string {
	if collection.Format == "yml" {
		for _, name := range []string{"folder.yml", "folder.yaml"} {
			path := filepath.Join(targetDir, name)
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return filepath.Join(targetDir, "folder.yml")
	}
	return filepath.Join(targetDir, "folder.bru")
}

func (a *App) writeWorkspaceGlobalEnvironmentFilesLocked(workspace *Workspace) error {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return nil
	}
	envPath := filepath.Join(workspace.Path, "environments")
	if err := os.MkdirAll(envPath, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(envPath)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext == ".yml" || ext == ".yaml" {
				if err := os.Remove(filepath.Join(envPath, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
	}
	for _, env := range workspace.GlobalEnvironments {
		filename := sanitizeFilename(env.Name)
		if filename == "" {
			filename = env.ID
		}
		if err := os.WriteFile(filepath.Join(envPath, filename+".yml"), []byte(bru.StringifyYAMLEnvironment(env)), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func collectionFolderFilesystemPath(collection *Collection, folderPath string) (string, error) {
	if collection == nil {
		return "", errors.New("collection is required")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection path is empty")
	}
	folder, err := findFolderConfig(collection, folderPath)
	if err != nil {
		return "", err
	}
	physicalPath := normalizeFolderPathKey(firstNonEmpty(folder.Path, folder.DisplayPath, folder.Name))
	if physicalPath == "" {
		return "", errors.New("folder path is required")
	}
	targetPath := filepath.Clean(filepath.Join(collection.Path, filepath.FromSlash(physicalPath)))
	if !pathInside(collection.Path, targetPath) {
		return "", fmt.Errorf("folder path %s escapes collection", physicalPath)
	}
	return targetPath, nil
}

func collectionRequestFilesystemPath(collection *Collection, item RequestItem) (string, error) {
	if collection == nil {
		return "", errors.New("collection is required")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection path is empty")
	}
	if pathInside(collection.Path, item.FilePath) {
		return filepath.Clean(item.FilePath), nil
	}
	filename := sanitizeFilename(item.Name)
	if filename == "" {
		filename = item.ID
	}
	folder := filepath.Clean(collection.Path)
	if folderPath := normalizeFolderPathKey(item.FolderPath); folderPath != "" {
		if folderConfig, err := findFolderConfig(collection, folderPath); err == nil {
			folderPath = normalizeFolderPathKey(firstNonEmpty(folderConfig.Path, folderConfig.DisplayPath, folderConfig.Name))
		}
		folder = filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	}
	targetPath := filepath.Clean(filepath.Join(folder, filename+requestFileExtensionForCollection(*collection)))
	if !pathInside(collection.Path, targetPath) {
		return "", fmt.Errorf("request path %s escapes collection", targetPath)
	}
	return targetPath, nil
}

func ensureRequestFilePaths(collection *Collection, defaultExt string) {
	used := map[string]bool{}
	for i := range collection.Items {
		target := ""
		if pathInside(collection.Path, collection.Items[i].FilePath) {
			target = filepath.Clean(collection.Items[i].FilePath)
			if used[target] {
				target = ""
			}
		}
		if target == "" {
			target = uniqueRequestFilePath(*collection, collection.Items[i], defaultExt, used)
		}
		collection.Items[i].FilePath = target
		used[target] = true
	}
}

func uniqueRequestFilePath(collection Collection, item RequestItem, defaultExt string, used map[string]bool) string {
	filename := sanitizeFilename(item.Name)
	if filename == "" {
		filename = item.ID
	}
	folder := filepath.Clean(collection.Path)
	if strings.TrimSpace(item.FolderPath) != "" {
		folder = filepath.Join(folder, filepath.FromSlash(item.FolderPath))
	}
	for index := 0; ; index++ {
		candidateName := filename
		if index > 0 {
			candidateName = fmt.Sprintf("%s %d", filename, index+1)
		}
		candidate := filepath.Clean(filepath.Join(folder, candidateName+defaultExt))
		if !used[candidate] {
			return candidate
		}
	}
}

func normalizeGitRemoteURL(raw string) (string, error) {
	remote := strings.TrimSpace(raw)
	if remote == "" {
		return "", errors.New("git remote URL is required")
	}
	if strings.ContainsAny(remote, "\r\n\t ") {
		return "", errors.New("git remote URL cannot contain whitespace")
	}
	if strings.Contains(remote, "://") {
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("invalid Git remote URL: %w", err)
		}
		switch parsed.Scheme {
		case "https", "http", "ssh", "git":
			if parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" {
				return "", errors.New("git remote URL must include host and repository path")
			}
		case "file":
			if parsed.Path == "" || parsed.Path == "/" {
				return "", errors.New("file Git remote URL must include a repository path")
			}
		default:
			return "", fmt.Errorf("unsupported Git remote URL scheme %q", parsed.Scheme)
		}
		if parsed.User != nil {
			return "", errors.New("git remote URL must not embed credentials")
		}
		return remote, nil
	}
	if strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, "./") || strings.HasPrefix(remote, "../") {
		return "", errors.New("local Git remotes must use file:// URLs")
	}
	colon := strings.Index(remote, ":")
	if colon <= 0 || colon == len(remote)-1 {
		return "", errors.New("git remote URL must be https://, ssh://, file://, or git@host:path")
	}
	userHost := remote[:colon]
	repoPath := remote[colon+1:]
	at := strings.Index(userHost, "@")
	if at <= 0 || at == len(userHost)-1 || strings.Contains(repoPath, ":") || strings.HasPrefix(repoPath, "/") {
		return "", errors.New("git remote URL must be https://, ssh://, file://, or git@host:path")
	}
	return remote, nil
}

func gitVersion() (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed or not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "--version").CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.New("git --version timed out")
		}
		return "", fmt.Errorf("git --version failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (a *App) emitGitCloneProgress(stage, message, targetPath string) {
	if a == nil || a.ctx == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "git:clone:progress", GitCloneProgress{
		Stage:      stage,
		Message:    message,
		TargetPath: targetPath,
		At:         time.Now().Format(time.RFC3339Nano),
	})
}

func scanGitProgressToken(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for index, b := range data {
		if b == '\n' || b == '\r' {
			return index + 1, bytes.TrimSpace(data[:index]), nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), bytes.TrimSpace(data), nil
	}
	return 0, nil, nil
}

func deriveGitRepoName(remote string) string {
	remote = strings.TrimSpace(remote)
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" {
		base := strings.TrimSuffix(pathBase(parsed.Path), ".git")
		return sanitizeFilename(base)
	}
	if colon := strings.LastIndex(remote, ":"); colon >= 0 && colon < len(remote)-1 {
		return sanitizeFilename(strings.TrimSuffix(pathBase(remote[colon+1:]), ".git"))
	}
	return sanitizeFilename(strings.TrimSuffix(pathBase(remote), ".git"))
}

func pathBase(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	index := strings.LastIndex(value, "/")
	if index >= 0 {
		return value[index+1:]
	}
	return value
}

func scanBrunoCollections(rootPath string) ([]GitCollectionCandidate, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, errors.New("scan path is required")
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", rootPath)
	}
	rootPath = filepath.Clean(rootPath)
	candidates := []GitCollectionCandidate{}
	err = filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if path != rootPath && (base == ".git" || base == "node_modules" || base == "environments") {
			return filepath.SkipDir
		}
		if !looksLikeCollectionDir(path) {
			return nil
		}
		collection, err := readCollectionFromDisk(path)
		if err != nil {
			return err
		}
		candidates = append(candidates, GitCollectionCandidate{
			Name:         collection.Name,
			Path:         filepath.Clean(path),
			Format:       collection.Format,
			RequestCount: len(collection.Items),
		})
		if path != rootPath {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].Path) < strings.ToLower(candidates[j].Path)
	})
	return candidates, nil
}

func looksLikeCollectionDir(path string) bool {
	for _, name := range []string{"bruno.json", "opencollection.yml", "opencollection.yaml"} {
		if info, err := os.Stat(filepath.Join(path, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func updateManagedGitIgnore(workspacePath, collectionPath string, add bool) error {
	return updateManagedGitIgnoreSecure(workspacePath, collectionPath, add)
}

func managedGitIgnoreEntries(content string) map[string]bool {
	entries := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# LiteAPI managed Git-backed collections":
			inBlock = true
			continue
		case "# End LiteAPI managed Git-backed collections":
			inBlock = false
			continue
		}
		if inBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			entries[trimmed] = true
		}
	}
	return entries
}

func replaceManagedGitIgnoreBlock(content string, entries map[string]bool) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# LiteAPI managed Git-backed collections" {
			inBlock = true
			continue
		}
		if trimmed == "# End LiteAPI managed Git-backed collections" {
			inBlock = false
			continue
		}
		if !inBlock {
			kept = append(kept, line)
		}
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	keys := make([]string, 0, len(entries))
	for entry := range entries {
		keys = append(keys, entry)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, "# LiteAPI managed Git-backed collections")
		kept = append(kept, keys...)
		kept = append(kept, "# End LiteAPI managed Git-backed collections")
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func collectionFromImport(payload ImportPayload) (Collection, error) {
	now := time.Now()
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "Imported Collection"
	}
	collection := Collection{
		ID:             newID("collection"),
		Name:           name,
		Format:         "json",
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	kind := strings.ToLower(payload.Kind)
	switch kind {
	case "bruno-json", "json", "bruno":
		var imported Collection
		if err := json.Unmarshal([]byte(payload.Content), &imported); err != nil {
			return Collection{}, err
		}
		imported.ID = collection.ID
		if imported.Name == "" {
			imported.Name = name
		}
		imported.SecurityConfig = normalizeCollectionSecurityConfig(imported.SecurityConfig)
		imported.CreatedAt = now
		imported.UpdatedAt = now
		return imported, nil
	case "bru":
		item, err := bru.Parse(payload.Content)
		if err != nil {
			return Collection{}, err
		}
		collection.Items = []RequestItem{item}
		return collection, nil
	case "postman":
		return importers.ImportPostman(payload.Content, name, payload.TranslatePostmanScripts)
	case "har":
		collection, _, err := importHAR(payload.Content, name)
		return collection, err
	case "insomnia":
		return importers.ImportInsomnia(payload.Content, name)
	case "swagger-2", "swagger2", "swagger":
		converted, err := convertSwagger2ToOpenAPI3(payload.Content)
		if err != nil {
			return Collection{}, err
		}
		return importers.ImportOpenAPI(converted, name, payload.GroupBy)
	case "openapi":
		return importers.ImportOpenAPI(payload.Content, name, payload.GroupBy)
	case "curl":
		collection, _, err := collectionFromCurlImport(payload.Content, name)
		return collection, err
	default:
		return Collection{}, fmt.Errorf("unsupported import kind %q", payload.Kind)
	}
}

func (a *App) openAPISyncInputs(collectionID string, options OpenAPISyncOptions) (Collection, string, string, string, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return Collection{}, "", "", "", err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return Collection{}, "", "", "", err
	}
	collectionCopy := *collection
	defaultConfig := firstOpenAPISyncConfig(collectionCopy)
	sourceURL := strings.TrimSpace(options.SourceURL)
	if sourceURL == "" {
		sourceURL = defaultConfig.SourceURL
	}
	groupBy := strings.TrimSpace(options.GroupBy)
	if groupBy == "" {
		groupBy = defaultConfig.GroupBy
	}
	groupBy = normalizeOpenAPISyncGroupBy(groupBy)
	content := options.Content
	client := a.httpClient
	a.mu.Unlock()
	if strings.TrimSpace(content) == "" {
		fetched, err := fetchOpenAPISyncContent(collectionCopy.Path, sourceURL, client)
		if err != nil {
			return Collection{}, "", "", "", err
		}
		content = fetched
	}
	return collectionCopy, content, sourceURL, groupBy, nil
}

func (a *App) openAPILocalDriftInputs(collectionID string) (Collection, string, OpenAPISyncConfig, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return Collection{}, "", OpenAPISyncConfig{}, false, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return Collection{}, "", OpenAPISyncConfig{}, false, err
	}
	if len(collection.OpenAPI) == 0 {
		return Collection{}, "", OpenAPISyncConfig{}, false, errors.New("OpenAPI sync is not connected")
	}
	collectionCopy := *collection
	collectionCopy.Items = append([]RequestItem(nil), collection.Items...)
	collectionCopy.Folders = append([]FolderConfig(nil), collection.Folders...)
	collectionCopy.Variables = append([]Variable(nil), collection.Variables...)
	config := firstOpenAPISyncConfig(collectionCopy)
	content, noStoredSpec, err := a.loadOpenAPISyncSpecLocked(collection.Path)
	if err != nil {
		return Collection{}, "", OpenAPISyncConfig{}, false, err
	}
	return collectionCopy, content, config, noStoredSpec, nil
}

func newOpenAPISyncConfig(sourceURL, groupBy, hash string) OpenAPISyncConfig {
	return OpenAPISyncConfig{
		SourceURL:         strings.TrimSpace(sourceURL),
		GroupBy:           normalizeOpenAPISyncGroupBy(groupBy),
		LastSyncDate:      time.Now().UTC().Format(time.RFC3339),
		SpecHash:          hash,
		AutoCheck:         true,
		AutoCheckInterval: 5,
	}
}

func newOpenAPISyncConfigPreservingSettings(sourceURL, groupBy, hash string, existing OpenAPISyncConfig) OpenAPISyncConfig {
	next := newOpenAPISyncConfig(sourceURL, groupBy, hash)
	existing = normalizeOpenAPISyncConfig(existing)
	next.AutoCheck = existing.AutoCheck
	next.AutoCheckInterval = normalizeOpenAPISyncAutoCheckInterval(existing.AutoCheckInterval)
	return next
}

func validateOpenAPISyncSource(sourceURL string) error {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return errors.New("OpenAPI source URL or file path is required")
	}
	if parsed, err := url.Parse(sourceURL); err == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "file":
			return nil
		default:
			return errors.New("invalid source: only http/https URLs and local file paths are allowed")
		}
	}
	return nil
}

func fetchOpenAPISyncContent(collectionPath, sourceURL string, client *http.Client) (string, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return "", errors.New("OpenAPI source URL or file path is required")
	}
	if err := validateOpenAPISyncSource(sourceURL); err != nil {
		return "", err
	}
	if parsed, err := url.Parse(sourceURL); err == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			if client == nil {
				client = http.DefaultClient
			}
			fetchURL := sourceURL
			separator := "?"
			if strings.Contains(fetchURL, "?") {
				separator = "&"
			}
			fetchURL += separator + "_=" + strconv.FormatInt(time.Now().UnixMilli(), 10)
			req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
			if err != nil {
				return "", err
			}
			req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
			req.Header.Set("Pragma", "no-cache")
			res, err := client.Do(req)
			if err != nil {
				return "", fmt.Errorf("could not reach %s: %w", sourceURL, err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode < 200 || res.StatusCode >= 300 {
				return "", fmt.Errorf("failed to fetch spec: %d %s", res.StatusCode, http.StatusText(res.StatusCode))
			}
			data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
			if err != nil {
				return "", err
			}
			return string(data), nil
		case "file":
			sourceURL = parsed.Path
		default:
			return "", errors.New("invalid source: only http/https URLs and local file paths are allowed")
		}
	}
	path := sourceURL
	if !filepath.IsAbs(path) && strings.TrimSpace(collectionPath) != "" {
		path = filepath.Join(collectionPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("spec file not found at: %s", sourceURL)
		}
		return "", err
	}
	return string(data), nil
}

type openAPISpecMetadataEntry struct {
	Filename  string `json:"filename"`
	SourceURL string `json:"sourceUrl"`
}

func (a *App) openAPISyncSpecsDirLocked() string {
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	return filepath.Join(a.dataDir, "specs")
}

func (a *App) openAPISyncSpecMetadataPathLocked() string {
	return filepath.Join(a.openAPISyncSpecsDirLocked(), "metadata.json")
}

func (a *App) readOpenAPISyncSpecMetadataLocked() map[string][]openAPISpecMetadataEntry {
	path := a.openAPISyncSpecMetadataPathLocked()
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]openAPISpecMetadataEntry{}
	}
	var meta map[string][]openAPISpecMetadataEntry
	if err := json.Unmarshal(data, &meta); err != nil || meta == nil {
		return map[string][]openAPISpecMetadataEntry{}
	}
	return meta
}

func (a *App) writeOpenAPISyncSpecMetadataLocked(meta map[string][]openAPISpecMetadataEntry) error {
	if meta == nil {
		meta = map[string][]openAPISpecMetadataEntry{}
	}
	path := a.openAPISyncSpecMetadataPathLocked()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) saveOpenAPISyncSpecLocked(collectionPath, sourceURL, content string) error {
	if strings.TrimSpace(collectionPath) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	specsDir := a.openAPISyncSpecsDirLocked()
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return err
	}
	meta := a.readOpenAPISyncSpecMetadataLocked()
	key := filepath.Clean(collectionPath)
	entry := openAPISpecMetadataEntry{}
	if entries := meta[key]; len(entries) > 0 {
		entry = entries[0]
	}
	if strings.TrimSpace(entry.Filename) == "" {
		ext := ".json"
		if openapisync.OpenAPISyncSpecLooksYAML(content) {
			ext = ".yaml"
		}
		entry.Filename = newID("spec") + ext
	}
	entry.SourceURL = strings.TrimSpace(sourceURL)
	meta[key] = []openAPISpecMetadataEntry{entry}
	if err := os.WriteFile(filepath.Join(specsDir, entry.Filename), []byte(content), 0o600); err != nil {
		return err
	}
	return a.writeOpenAPISyncSpecMetadataLocked(meta)
}

func (a *App) loadOpenAPISyncSpecLocked(collectionPath string) (string, bool, error) {
	if strings.TrimSpace(collectionPath) == "" {
		return "", true, nil
	}
	meta := a.readOpenAPISyncSpecMetadataLocked()
	entries := meta[filepath.Clean(collectionPath)]
	if len(entries) == 0 || strings.TrimSpace(entries[0].Filename) == "" {
		return "", true, nil
	}
	specsDir := a.openAPISyncSpecsDirLocked()
	target := filepath.Clean(filepath.Join(specsDir, entries[0].Filename))
	if !pathInside(specsDir, target) {
		return "", true, nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", true, nil
		}
		return "", false, err
	}
	return string(data), false, nil
}

func (a *App) cleanupOpenAPISyncSpecLocked(collectionPath string) {
	meta := a.readOpenAPISyncSpecMetadataLocked()
	key := filepath.Clean(collectionPath)
	for _, entry := range meta[key] {
		if strings.TrimSpace(entry.Filename) != "" {
			_ = os.Remove(filepath.Join(a.openAPISyncSpecsDirLocked(), entry.Filename))
		}
	}
	delete(meta, key)
	_ = a.writeOpenAPISyncSpecMetadataLocked(meta)
}

// OpenAPI spec sync and its merge rules moved to internal/openapisync.

const collectionFileCacheVersion = 1

type collectionFileCacheStore struct {
	Version     int                                 `json:"version"`
	Collections map[string]collectionFileCacheEntry `json:"collections"`
}

type collectionFileCacheEntry struct {
	Fingerprint string     `json:"fingerprint"`
	Collection  Collection `json:"collection"`
	CachedAt    time.Time  `json:"cachedAt"`
}

func (a *App) readCollectionFromDiskCachedLocked(collectionPath string) (Collection, error) {
	preferences := normalizePreferences(a.state.Preferences)
	if !preferences.Cache.File.Enabled {
		return readCollectionFromDisk(collectionPath)
	}
	collectionPath = filepath.Clean(collectionPath)
	fingerprint, err := collectionFileCacheFingerprint(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	store := a.readCollectionFileCacheLocked()
	if entry, ok := store.Collections[collectionPath]; ok && entry.Fingerprint == fingerprint {
		collection := entry.Collection
		collection.Path = collectionPath
		return collection, nil
	}
	collection, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	store.Collections[collectionPath] = collectionFileCacheEntry{
		Fingerprint: fingerprint,
		Collection:  collection,
		CachedAt:    time.Now(),
	}
	if err := a.writeCollectionFileCacheLocked(store); err != nil {
		return Collection{}, err
	}
	return collection, nil
}

func (a *App) collectionFileCachePathLocked() string {
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	return filepath.Join(a.dataDir, "cache", "collections.json")
}

func (a *App) readCollectionFileCacheLocked() collectionFileCacheStore {
	store := collectionFileCacheStore{
		Version:     collectionFileCacheVersion,
		Collections: map[string]collectionFileCacheEntry{},
	}
	data, err := os.ReadFile(a.collectionFileCachePathLocked())
	if err != nil {
		return store
	}
	if err := json.Unmarshal(data, &store); err != nil || store.Version != collectionFileCacheVersion {
		return collectionFileCacheStore{
			Version:     collectionFileCacheVersion,
			Collections: map[string]collectionFileCacheEntry{},
		}
	}
	if store.Collections == nil {
		store.Collections = map[string]collectionFileCacheEntry{}
	}
	return store
}

func (a *App) writeCollectionFileCacheLocked(store collectionFileCacheStore) error {
	store.Version = collectionFileCacheVersion
	if store.Collections == nil {
		store.Collections = map[string]collectionFileCacheEntry{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	path := a.collectionFileCachePathLocked()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) fileCacheSizeLocked() (int64, error) {
	info, err := os.Stat(a.collectionFileCachePathLocked())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir() {
		return 0, nil
	}
	return info.Size(), nil
}

func collectionFileCacheFingerprint(collectionPath string) (string, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", collectionPath)
	}
	parts := []string{}
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			base := entry.Name()
			if path != collectionPath && (base == "node_modules" || base == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		parts = append(parts, fmt.Sprintf("%s:%d:%d:%x", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), sum[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(parts)
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func collectionWatchFingerprint(collectionPath string) (string, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", collectionPath)
	}
	ignorePatterns := collectionIgnorePatterns(collectionPath)
	parts := []string{}
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != collectionPath && collectionPathIgnored(collectionPath, path, ignorePatterns) {
				return filepath.SkipDir
			}
			return nil
		}
		if collectionPathIgnored(collectionPath, path, ignorePatterns) {
			return nil
		}
		if !collectionWatchFileAffectsTree(collectionPath, path) {
			return nil
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		parts = append(parts, fmt.Sprintf("%s:%d:%d:%x", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), sum[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(parts)
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func collectionIgnorePatterns(collectionPath string) []string {
	patterns := []string{"node_modules", ".git"}
	if ymlPath, ok := firstExistingCollectionYAMLPath(collectionPath); ok {
		if root, err := parseYAMLMapFile(ymlPath); err == nil {
			if extensions, ok := mapValue(root["extensions"]); ok {
				if bruno, ok := mapValue(extensions["bruno"]); ok {
					patterns = append(patterns, collectionIgnoreStringList(bruno["ignore"])...)
				}
			}
		}
		return normalizeCollectionIgnorePatterns(patterns)
	}
	configPath := filepath.Join(collectionPath, "bruno.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return normalizeCollectionIgnorePatterns(patterns)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return normalizeCollectionIgnorePatterns(patterns)
	}
	patterns = append(patterns, collectionIgnoreStringList(config["ignore"])...)
	return normalizeCollectionIgnorePatterns(patterns)
}

func firstExistingCollectionYAMLPath(collectionPath string) (string, bool) {
	for _, name := range []string{"opencollection.yml", "opencollection.yaml"} {
		path := filepath.Join(collectionPath, name)
		if fileExists(path) {
			return path, true
		}
	}
	return "", false
}

func collectionIgnoreStringList(raw interface{}) []string {
	values, ok := listValue(raw)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(yamlScalarString(value)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func normalizeCollectionIgnorePatterns(patterns []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
		pattern = strings.TrimPrefix(pattern, "./")
		pattern = strings.TrimPrefix(pattern, "/")
		pattern = strings.TrimRight(pattern, "/")
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	return out
}

func collectionPathIgnored(collectionPath, path string, patterns []string) bool {
	rel, err := filepath.Rel(collectionPath, path)
	if err != nil || rel == "." {
		return false
	}
	rel = filepath.ToSlash(rel)
	if collectionPathHasDefaultIgnoredSegment(rel) {
		return true
	}
	for _, pattern := range patterns {
		if rel == pattern || strings.HasPrefix(rel, pattern) {
			return true
		}
	}
	return false
}

func collectionPathHasDefaultIgnoredSegment(rel string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == "node_modules" || segment == ".git" {
			return true
		}
	}
	return false
}

func collectionWatchFileAffectsTree(collectionPath, path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return false
	}
	rel, err := filepath.Rel(collectionPath, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	switch base {
	case "bruno.json", "collection.bru", "opencollection.yml", "opencollection.yaml", "folder.bru", "folder.yml", "folder.yaml":
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if strings.HasPrefix(rel, "environments/") {
		return ext == ".yml" || ext == ".yaml"
	}
	return ext == ".bru" || ext == ".yml" || ext == ".yaml"
}

func readCollectionFromDisk(collectionPath string) (Collection, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	if !info.IsDir() {
		return Collection{}, fmt.Errorf("%s is not a directory", collectionPath)
	}
	configName := filepath.Join(collectionPath, "bruno.json")
	name := filepath.Base(collectionPath)
	rootConfigHasName := false
	version := "1"
	format := "bru"
	if configData, err := os.ReadFile(configName); err == nil {
		var config map[string]interface{}
		if err := json.Unmarshal(configData, &config); err != nil {
			return Collection{}, fmt.Errorf("parse bruno.json: %w", err)
		}
		if configValue, ok := config["name"].(string); ok && strings.TrimSpace(configValue) != "" {
			name = configValue
			rootConfigHasName = true
		}
		if configValue := strings.TrimSpace(yamlScalarString(config["version"])); configValue != "" {
			version = configValue
		}
	}
	openCollectionConfig := filepath.Join(collectionPath, "opencollection.yml")
	if _, err := os.Stat(openCollectionConfig); err != nil {
		openCollectionConfig = filepath.Join(collectionPath, "opencollection.yaml")
	}
	if _, err := os.Stat(openCollectionConfig); err == nil {
		format = "yml"
		if root, err := parseYAMLMapFile(openCollectionConfig); err == nil {
			if value, ok := nestedString(root, "info", "name"); ok {
				name = value
				rootConfigHasName = true
			} else if value, ok := nestedString(root, "info", "title"); ok {
				name = value
				rootConfigHasName = true
			} else if value, ok := root["name"].(string); ok && strings.TrimSpace(value) != "" {
				name = value
				rootConfigHasName = true
			}
		}
	}
	collection := Collection{
		ID:             deterministicID("collection", filepath.Clean(collectionPath)),
		Name:           name,
		Version:        version,
		Path:           filepath.Clean(collectionPath),
		Format:         format,
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      info.ModTime(),
		UpdatedAt:      time.Now(),
	}
	ignorePatterns := collectionIgnorePatterns(collectionPath)
	if configData, err := os.ReadFile(configName); err == nil {
		var config map[string]interface{}
		if err := json.Unmarshal(configData, &config); err == nil {
			if proxy, ok := parseJSONProxyConfig(config["proxy"]); ok {
				collection.Proxy = transport.NormalizeProxyConfig(proxy)
			}
			if certs, ok := parseJSONClientCertificates(config["clientCertificates"]); ok {
				collection.ClientCertificates = transport.NormalizeClientCertificates(certs)
			}
			if presets, ok := parseCollectionPresets(config["presets"]); ok {
				collection.Presets = normalizeCollectionPresets(presets)
			}
			if protobuf, ok := parseCollectionProtobuf(config["protobuf"]); ok {
				collection.Protobuf = normalizeCollectionProtobuf(collection.Path, protobuf)
			}
			if openAPI := parseOpenAPISyncConfigs(config["openapi"]); len(openAPI) > 0 {
				collection.OpenAPI = openAPI
			}
		}
	}
	if format == "yml" {
		if err := hydrateYAMLCollectionMetadata(&collection, openCollectionConfig); err != nil {
			return Collection{}, err
		}
	}
	if rootPath := filepath.Join(collectionPath, "collection.bru"); !collectionPathIgnored(collectionPath, rootPath, ignorePatterns) {
		if content, err := os.ReadFile(rootPath); err == nil {
			rootName := collection.Name
			if err := bru.ParseCollectionMetadata(&collection, string(content)); err != nil {
				return Collection{}, err
			}
			if rootConfigHasName {
				collection.Name = rootName
			}
		}
	}
	if environments, err := readCollectionEnvironments(collectionPath, ignorePatterns); err != nil {
		return Collection{}, err
	} else if len(environments) > 0 {
		collection.Environments = bru.MergeEnvironments(collection.Environments, environments)
	}
	folderMap, folders := readFolderConfigs(collectionPath, ignorePatterns)
	collection.Folders = folders
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != collectionPath && collectionPathIgnored(collectionPath, path, ignorePatterns) {
				return filepath.SkipDir
			}
			base := entry.Name()
			if base == "environments" {
				return filepath.SkipDir
			}
			return nil
		}
		if collectionPathIgnored(collectionPath, path, ignorePatterns) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		base := strings.ToLower(filepath.Base(path))
		if ext == ".bru" && base == "collection.bru" {
			return nil
		}
		if (ext == ".yml" || ext == ".yaml") && base == "opencollection.yml" {
			return nil
		}
		if isFolderMetadataFile(base) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var item RequestItem
		switch ext {
		case ".bru":
			item, err = bru.Parse(string(content))
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		case ".yml", ".yaml":
			item, err = yamlstore.ParseRequest(string(content))
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		default:
			return nil
		}
		if item.Type == "" || item.Type == "http" || item.Type == "graphql" || item.Type == "websocket" || item.Type == "grpc" {
			item.ID = deterministicID("request", filepath.Clean(path))
			item.FilePath = filepath.Clean(path)
			item.FolderPath = folderDisplayPath(collectionPath, path, folderMap)
			assignExampleIDs(&item)
			item.CreatedAt = info.ModTime()
			item.UpdatedAt = info.ModTime()
			collection.Items = append(collection.Items, item)
		}
		return nil
	})
	if err != nil {
		return Collection{}, err
	}
	sort.SliceStable(collection.Items, func(i, j int) bool {
		if collection.Items[i].Seq != collection.Items[j].Seq {
			return collection.Items[i].Seq < collection.Items[j].Seq
		}
		return strings.ToLower(collection.Items[i].Name) < strings.ToLower(collection.Items[j].Name)
	})
	return collection, nil
}

func readFolderConfigs(collectionPath string, ignorePatterns []string) (map[string]FolderConfig, []FolderConfig) {
	folderMap := map[string]FolderConfig{"": {Path: "", DisplayPath: ""}}
	folders := []FolderConfig{}
	_ = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path != collectionPath && collectionPathIgnored(collectionPath, path, ignorePatterns) {
			return filepath.SkipDir
		}
		base := entry.Name()
		if base == "environments" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		parentRel := filepath.ToSlash(filepath.Dir(rel))
		if parentRel == "." {
			parentRel = ""
		}
		config := readFolderConfig(path)
		if config.Name == "" {
			config.Name = filepath.Base(path)
		}
		config.Path = rel
		parentDisplay := folderMap[parentRel].DisplayPath
		if parentDisplay == "" {
			config.DisplayPath = config.Name
		} else {
			config.DisplayPath = parentDisplay + "/" + config.Name
		}
		folderMap[rel] = config
		folders = append(folders, config)
		return nil
	})
	sort.SliceStable(folders, func(i, j int) bool {
		return folders[i].DisplayPath < folders[j].DisplayPath
	})
	return folderMap, folders
}

func readFolderConfig(folderPath string) FolderConfig {
	config := FolderConfig{Auth: AuthConfig{}}
	if content, err := os.ReadFile(filepath.Join(folderPath, "folder.bru")); err == nil {
		temp := Collection{Auth: AuthConfig{}}
		_ = bru.ParseCollectionMetadata(&temp, string(content))
		config.Headers = temp.Headers
		config.Variables = temp.Variables
		config.ResVariables = temp.ResVariables
		config.Auth = temp.Auth
		config.PreScript = temp.PreScript
		config.PostScript = temp.PostScript
		config.Tests = temp.Tests
		config.Docs = temp.Docs
		if meta, ok := bru.ParseSections(string(content))["meta"]; ok {
			values := bru.ParseScalarMap(meta)
			config.Name = strings.TrimSpace(values["name"])
			if seq, err := strconv.Atoi(values["seq"]); err == nil && seq > 0 {
				config.Seq = seq
			}
		}
		return config
	}
	for _, name := range []string{"folder.yml", "folder.yaml"} {
		path := filepath.Join(folderPath, name)
		if root, err := parseYAMLMapFile(path); err == nil {
			if value, ok := nestedString(root, "info", "name"); ok {
				config.Name = value
			} else if value := strings.TrimSpace(yamlScalarString(root["name"])); value != "" {
				config.Name = value
			}
			if info, ok := mapValue(root["info"]); ok {
				config.Seq = intValue(info["seq"], 0)
			}
			applyYAMLFolderDefaults(&config, root)
			return config
		}
	}
	return config
}

func applyYAMLFolderDefaults(config *FolderConfig, root map[string]interface{}) {
	request, _ := mapValue(root["request"])
	if headers, ok := request["headers"]; ok {
		config.Headers = parseYAMLKeyValues(headers, false)
	}
	if variables, ok := request["variables"]; ok {
		config.Variables = yamlstore.ParseVariables(variables)
	}
	config.ResVariables = append(config.ResVariables, yamlstore.ParsePostResponseActions(request["actions"])...)
	if auth, ok := request["auth"]; ok {
		config.Auth = yamlstore.ParseAuth(auth, config.Auth)
	}
	if scripts, ok := listValue(request["scripts"]); ok {
		for _, scriptValue := range scripts {
			script, ok := mapValue(scriptValue)
			if !ok {
				continue
			}
			code := yamlScalarString(script["code"])
			switch strings.ToLower(yamlScalarString(script["type"])) {
			case "before-request", "pre-request":
				config.PreScript = scalar.AppendScript(config.PreScript, code)
			case "after-response", "post-response":
				config.PostScript = scalar.AppendScript(config.PostScript, code)
			case "tests", "test":
				config.Tests = scalar.AppendScript(config.Tests, code)
			}
		}
	}
	if docsMap, ok := mapValue(root["docs"]); ok {
		config.Docs = yamlScalarString(docsMap["content"])
	} else if docs := yamlScalarString(root["docs"]); strings.TrimSpace(docs) != "" {
		config.Docs = docs
	}
}

func folderDisplayPath(collectionPath, requestPath string, folders map[string]FolderConfig) string {
	relDir, err := filepath.Rel(collectionPath, filepath.Dir(requestPath))
	if err != nil || relDir == "." {
		return ""
	}
	relDir = filepath.ToSlash(relDir)
	if config, ok := folders[relDir]; ok && config.DisplayPath != "" {
		return config.DisplayPath
	}
	return relDir
}

func isFolderMetadataFile(base string) bool {
	return base == "folder.bru" || base == "folder.yml" || base == "folder.yaml"
}

func hydrateYAMLCollectionMetadata(collection *Collection, path string) error {
	root, err := parseYAMLMapFile(path)
	if err != nil {
		return err
	}
	request, _ := mapValue(root["request"])
	if version, ok := nestedString(root, "info", "version"); ok && strings.TrimSpace(version) != "" {
		collection.Version = version
	}
	if headers, ok := request["headers"]; ok {
		collection.Headers = parseYAMLKeyValues(headers, false)
	}
	if variables, ok := request["variables"]; ok {
		collection.Variables = yamlstore.ParseVariables(variables)
	}
	collection.ResVariables = append(collection.ResVariables, yamlstore.ParsePostResponseActions(request["actions"])...)
	if auth, ok := request["auth"]; ok {
		collection.Auth = yamlstore.ParseAuth(auth, collection.Auth)
	}
	if scripts, ok := listValue(request["scripts"]); ok {
		for _, scriptValue := range scripts {
			script, ok := mapValue(scriptValue)
			if !ok {
				continue
			}
			code := yamlScalarString(script["code"])
			switch strings.ToLower(yamlScalarString(script["type"])) {
			case "before-request", "pre-request":
				collection.PreScript = scalar.AppendScript(collection.PreScript, code)
			case "after-response", "post-response":
				collection.PostScript = scalar.AppendScript(collection.PostScript, code)
			case "tests", "test":
				collection.Tests = scalar.AppendScript(collection.Tests, code)
			}
		}
	}
	if docsMap, ok := mapValue(root["docs"]); ok {
		collection.Docs = yamlScalarString(docsMap["content"])
	} else if docs := yamlScalarString(root["docs"]); strings.TrimSpace(docs) != "" {
		collection.Docs = docs
	}
	config, _ := mapValue(root["config"])
	if environments, ok := config["environments"]; ok {
		collection.Environments = yamlstore.ParseEnvironments(environments)
	}
	if proxy, ok := parseYAMLProxyConfig(config["proxy"]); ok {
		collection.Proxy = transport.NormalizeProxyConfig(proxy)
	}
	if certs, ok := parseYAMLClientCertificates(config["clientCertificates"]); ok {
		collection.ClientCertificates = transport.NormalizeClientCertificates(certs)
	}
	if presets, ok := parseCollectionPresets(config["presets"]); ok {
		collection.Presets = normalizeCollectionPresets(presets)
	}
	if protobuf, ok := parseCollectionProtobuf(config["protobuf"]); ok {
		collection.Protobuf = normalizeCollectionProtobuf(collection.Path, protobuf)
	}
	if extensions, ok := mapValue(root["extensions"]); ok {
		if bruno, ok := mapValue(extensions["bruno"]); ok {
			if openAPI := parseOpenAPISyncConfigs(bruno["openapi"]); len(openAPI) > 0 {
				collection.OpenAPI = openAPI
			}
		}
	}
	return nil
}

func parseYAMLMapFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func nestedString(raw map[string]interface{}, path ...string) (string, bool) {
	var current interface{} = raw
	for _, key := range path {
		asMap, ok := current.(map[string]interface{})
		if !ok {
			return "", false
		}
		current, ok = asMap[key]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func scrubEnvironmentSecretValues(environments []Environment) []Environment {
	return bru.ScrubEnvironmentSecretValues(environments)
}

func yamlVariables(values []Variable) []map[string]interface{} { return bru.YAMLVariables(values) }

func requestFilePath(collection Collection, item RequestItem, defaultExt string) string {
	return openapisync.RequestFilePath(collection, item, defaultExt)
}

func requestFileExtensionForCollection(collection Collection) string {
	return openapisync.RequestFileExtensionForCollection(collection)
}

func assignExampleIDs(item *RequestItem) { types.AssignExampleIDs(item) }

func normalizeOpenAPISyncAutoCheckInterval(interval int) int {
	return openapisync.NormalizeAutoCheckInterval(interval)
}

func firstOpenAPISyncConfig(collection Collection) OpenAPISyncConfig {
	return openapisync.FirstConfig(collection)
}

func normalizeOpenAPISyncConfig(config OpenAPISyncConfig) OpenAPISyncConfig {
	return openapisync.NormalizeConfig(config)
}

func normalizeOpenAPISyncGroupBy(groupBy string) string { return openapisync.NormalizeGroupBy(groupBy) }

func compareAssertion(actual, operator, expected string) bool {
	return scripting.CompareAssertion(actual, operator, expected)
}

func pathInside(root, candidate string) bool { return scripting.PathInside(root, candidate) }

func keyValuesFromHeaders(headers map[string]string) []KeyValue {
	return scripting.KeyValuesFromHeaders(headers)
}

func previewModeFromHeaders(headers map[string]string) string {
	return scripting.PreviewModeFromHeaders(headers)
}

func normalizeJSSandboxMode(mode string) string { return scripting.NormalizeJSSandboxMode(mode) }

func timelineSourceFileForItem(collectionPath string, item RequestItem) string {
	return scripting.TimelineSourceFileForItem(collectionPath, item)
}

func intValue(raw interface{}, fallback int) int { return scalar.IntValue(raw, fallback) }

func normalizeOAuth2AdditionalPlacement(value string) string {
	return types.NormalizeOAuth2AdditionalPlacement(value)
}

func cloneFolderConfigForFolderClone(folder FolderConfig) FolderConfig {
	return types.CloneFolderConfigForFolderClone(folder)
}

func cloneRequestItemForFolderClone(item RequestItem) RequestItem {
	return types.CloneRequestItemForFolderClone(item)
}

func cloneResponseExample(example ResponseExample) ResponseExample {
	return types.CloneResponseExample(example)
}

func getKeyValue(values []KeyValue, name string) string { return types.GetKeyValue(values, name) }

func boolValueOK(raw interface{}) (bool, bool) { return scalar.BoolValueOK(raw) }

func boolValue(raw interface{}, fallback bool) bool { return scalar.BoolValue(raw, fallback) }

func listValue(raw interface{}) ([]interface{}, bool) { return scalar.ListValue(raw) }

func selectedFileBodyEntry(body RequestBody) (FileBodyEntry, bool) {
	return types.SelectedFileBodyEntry(body)
}

func cleanStatusText(status int, statusText string) string {
	return scalar.CleanStatusText(status, statusText)
}

func cloneKeyValues(values []KeyValue) []KeyValue { return types.CloneKeyValues(values) }

func cloneFormParts(values []FormPart) []FormPart { return types.CloneFormParts(values) }

func cloneFileBodyEntries(values []FileBodyEntry) []FileBodyEntry {
	return types.CloneFileBodyEntries(values)
}

func fileBodyEntries(body RequestBody) []FileBodyEntry { return types.FileBodyEntriesOf(body) }

func requestBodySnapshot(body RequestBody) string { return types.RequestBodySnapshot(body) }

// sanitizeFilename, deterministicID, normalizeWhitespace and appendScript moved
// to internal/scalar for the same reason as the four above: internal/importers
// needs them, and two copies of "what characters are legal in a filename" would
// be free to drift.

func sanitizeFilename(value string) string { return scalar.SanitizeFilename(value) }

func deterministicID(prefix, input string) string { return scalar.DeterministicID(prefix, input) }

//
// mapValue, yamlScalarString, firstNonEmpty and newID moved to internal/scalar,
// which the importers need too. Wrapped rather than renamed at 400+ call sites.

func mapValue(raw interface{}) (map[string]interface{}, bool) {
	return scalar.Map(raw)
}

func yamlScalarString(raw interface{}) string {
	return scalar.YAMLString(raw)
}

func firstNonEmpty(values ...string) string {
	return scalar.FirstNonEmpty(values...)
}

func newID(prefix string) string {
	return scalar.NewID(prefix)
}

// YAML request reading moved to internal/store/yamlstore.

func collectionShareSnapshot(collection Collection) Collection {
	snapshot := collection
	snapshot.Remote = ""
	snapshot.NotFoundLocally = false
	snapshot.RuntimeVariables = nil
	snapshot.Items = make([]RequestItem, 0, len(collection.Items))
	for _, item := range collection.Items {
		if item.Transient {
			continue
		}
		item.Response = nil
		item.Timeline = nil
		item.Draft = false
		item.Transient = false
		item.FilePath = ""
		snapshot.Items = append(snapshot.Items, item)
	}
	snapshot.Folders = append([]FolderConfig(nil), collection.Folders...)
	snapshot.Environments = make([]Environment, 0, len(collection.Environments))
	for _, env := range collection.Environments {
		env.Variables = append([]Variable(nil), env.Variables...)
		for index := range env.Variables {
			if env.Variables[index].Secret {
				env.Variables[index].Value = ""
			}
		}
		snapshot.Environments = append(snapshot.Environments, env)
	}
	return snapshot
}

func buildCollectionShareYAML(collection Collection, generatedAt time.Time) (string, int, int, error) {
	content, folderCount, requestCount, err := buildCollectionDocsYAML(collection, nil, generatedAt)
	if err != nil {
		return "", 0, 0, err
	}
	root, err := yamlMapFromString(content)
	if err != nil {
		return "", 0, 0, err
	}
	root["bundled"] = true
	data, err := yaml.Marshal(root)
	if err != nil {
		return "", 0, 0, err
	}
	return string(data), folderCount, requestCount, nil
}

func buildCollectionZipExportFiles(collection Collection) ([]collectionExportFile, int, int, error) {
	files := []collectionExportFile{}
	used := map[string]bool{}
	exportRoot := filepath.Join(os.TempDir(), "liteapi-export-root")
	collection.Path = exportRoot
	format := strings.ToLower(strings.TrimSpace(collection.Format))
	if format == "" {
		format = "yml"
	}
	if format == "yml" || format == "yaml" {
		root, err := yamlMapFromString(stringifyYAMLCollection(collection))
		if err != nil {
			return nil, 0, 0, err
		}
		root["bundled"] = false
		extensions, _ := mapValue(root["extensions"])
		if extensions == nil {
			extensions = map[string]interface{}{}
		}
		bruno, _ := mapValue(extensions["bruno"])
		if bruno == nil {
			bruno = map[string]interface{}{}
		}
		bruno["exportedAt"] = time.Now().UTC().Format(time.RFC3339)
		bruno["exportedUsing"] = "LiteAPI"
		extensions["bruno"] = bruno
		root["extensions"] = extensions
		data, err := yaml.Marshal(root)
		if err != nil {
			return nil, 0, 0, err
		}
		addCollectionExportFile(&files, used, "opencollection.yml", data)
		for _, folder := range collection.Folders {
			if folderPath := exportFolderPath(folder); folderPath != "" {
				addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join(filepath.FromSlash(folderPath), "folder.yml")), []byte(stringifyYAMLFolder(folder)))
			}
		}
		ensureRequestFilePaths(&collection, ".yml")
		for _, item := range collection.Items {
			content, err := stringifyYAMLRequest(item)
			if err != nil {
				return nil, 0, 0, err
			}
			rel, ok := exportRelativePath(collection.Path, item.FilePath)
			if !ok {
				continue
			}
			addCollectionExportFile(&files, used, rel, []byte(content))
		}
		return files, len(collection.Folders), len(collection.Items), nil
	}

	config := map[string]interface{}{
		"name":    collection.Name,
		"type":    "collection",
		"version": firstNonEmpty(collection.Version, "1"),
		"ignore":  []string{"node_modules", ".git"},
	}
	if transport.HasProxyConfig(collection.Proxy) {
		config["proxy"] = jsonProxyConfig(collection.Proxy)
	}
	if transport.HasClientCertificates(collection.ClientCertificates) {
		config["clientCertificates"] = jsonClientCertificates(collection.ClientCertificates)
	}
	if hasCollectionPresets(collection.Presets) {
		config["presets"] = jsonCollectionPresets(collection.Presets)
	}
	if hasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = jsonCollectionProtobuf(collection.Protobuf)
	}
	if len(collection.OpenAPI) > 0 {
		config["openapi"] = jsonOpenAPISyncConfigs(collection.OpenAPI)
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, 0, 0, err
	}
	addCollectionExportFile(&files, used, "bruno.json", configData)
	addCollectionExportFile(&files, used, "collection.bru", []byte(bru.StringifyBruCollection(collection)))
	for _, env := range collection.Environments {
		name := sanitizeFilename(env.Name)
		if name == "" {
			name = env.ID
		}
		addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join("environments", name+".bru")), []byte(bru.StringifyBruEnvironment(env)))
	}
	for _, folder := range collection.Folders {
		if folderPath := exportFolderPath(folder); folderPath != "" {
			addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join(filepath.FromSlash(folderPath), "folder.bru")), []byte(bru.StringifyBruFolder(folder)))
		}
	}
	ensureRequestFilePaths(&collection, ".bru")
	for _, item := range collection.Items {
		rel, ok := exportRelativePath(collection.Path, item.FilePath)
		if !ok {
			continue
		}
		addCollectionExportFile(&files, used, rel, []byte(bru.StringifyBru(item)))
	}
	return files, len(collection.Folders), len(collection.Items), nil
}

func addCollectionExportFile(files *[]collectionExportFile, used map[string]bool, name string, content []byte) {
	name = cleanExportArchivePath(name)
	if name == "" {
		return
	}
	name = uniqueCollectionExportPath(name, used)
	used[name] = true
	*files = append(*files, collectionExportFile{Name: name, Content: append([]byte(nil), content...)})
}

func cleanExportArchivePath(name string) string {
	name = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(name))))
	if name == "." || name == "" || strings.HasPrefix(name, "../") || name == ".." || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return ""
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == ".git" || part == "node_modules" {
			return ""
		}
	}
	return name
}

func uniqueCollectionExportPath(name string, used map[string]bool) string {
	if !used[name] {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s %d%s", base, index, ext)
		if !used[candidate] {
			return candidate
		}
	}
}

func exportFolderPath(folder FolderConfig) string {
	folderPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Path))
	if folderPath == "" {
		return ""
	}
	return cleanExportArchivePath(folderPath)
}

func exportRelativePath(root, target string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return "", false
	}
	clean := cleanExportArchivePath(rel)
	return clean, clean != ""
}

func zipCollectionExportFiles(files []collectionExportFile) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, file := range files {
		name := cleanExportArchivePath(file.Name)
		if name == "" {
			continue
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if _, err := writer.Write(file.Content); err != nil {
			_ = archive.Close()
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func collectionExportBytes(result CollectionExportResult) ([]byte, error) {
	if strings.TrimSpace(result.ContentBase64) != "" {
		return base64.StdEncoding.DecodeString(result.ContentBase64)
	}
	return []byte(result.Content), nil
}

func buildPostmanCollectionExport(collection Collection) (string, int, []string, error) {
	skipped := []string{}
	skippedSeen := map[string]bool{}
	items, count := postmanCollectionItems(collection, "", &skipped, skippedSeen)
	payload := map[string]interface{}{
		"info": map[string]interface{}{
			"name":   collection.Name,
			"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		"item": items,
	}
	// US-053. Collection-level state was previously dropped entirely, so an
	// export round trip silently lost every collection variable, the
	// collection auth every request inherits, and the collection scripts that
	// run before each one. The result imported cleanly and behaved differently.
	if events := sharePostmanEvents(collection.PreScript, collection.PostScript, ""); len(events) > 0 {
		payload["event"] = events
	}
	if auth := sharePostmanAuth(collection.Auth); auth != nil {
		payload["auth"] = auth
	}
	if variables := sharePostmanVariables(collection.Variables); len(variables) > 0 {
		payload["variable"] = variables
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", 0, nil, err
	}
	return string(data), count, skipped, nil
}

func postmanCollectionItems(collection Collection, parentPath string, skipped *[]string, skippedSeen map[string]bool) ([]interface{}, int) {
	out := []interface{}{}
	count := 0
	for _, folder := range collectionDocsChildFolders(collection.Folders, parentPath) {
		children, childCount := postmanCollectionItems(collection, folder.DisplayPath, skipped, skippedSeen)
		entry := map[string]interface{}{
			"name": firstNonEmpty(folder.Name, filepath.Base(filepath.FromSlash(folder.DisplayPath)), filepath.Base(filepath.FromSlash(folder.Path))),
			"item": children,
		}
		if events := sharePostmanEvents(folder.PreScript, folder.PostScript, ""); len(events) > 0 {
			entry["event"] = events
		}
		if auth := sharePostmanAuth(folder.Auth); auth != nil {
			entry["auth"] = auth
		}
		out = append(out, entry)
		count += childCount
	}
	for _, item := range collectionDocsChildRequests(collection.Items, parentPath) {
		switch item.Type {
		case "", "http", "graphql":
			out = append(out, sharePostmanRequestItem(item))
			count++
		case "grpc":
			addSkippedCollectionExportType(skipped, skippedSeen, "gRPC")
		case "websocket":
			addSkippedCollectionExportType(skipped, skippedSeen, "WebSocket")
		}
	}
	return out, count
}

func addSkippedCollectionExportType(skipped *[]string, seen map[string]bool, label string) {
	if seen[label] {
		return
	}
	seen[label] = true
	*skipped = append(*skipped, label)
}

func sharePostmanRequestItem(item RequestItem) map[string]interface{} {
	method := strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet))
	request := map[string]interface{}{
		"method": method,
		"url":    sharePostmanURL(item),
	}
	if headers := sharePostmanKeyValues(item.Headers, "key"); len(headers) > 0 {
		request["header"] = headers
	}
	if body := sharePostmanBody(item); body != nil {
		request["body"] = body
	}
	if auth := sharePostmanAuth(item.Auth); auth != nil {
		request["auth"] = auth
	}
	if description := strings.TrimSpace(item.Docs); description != "" {
		request["description"] = description
	}
	entry := map[string]interface{}{
		"name":    item.Name,
		"request": request,
	}
	if events := sharePostmanEvents(item.PreScript, item.PostScript, item.Tests); len(events) > 0 {
		entry["event"] = events
	}
	return entry
}

// sharePostmanEvents builds the event blocks a Postman collection carries.
//
// Postman has TWO events, prerequest and test, while this model has THREE
// script slots: PreScript, PostScript and Tests. PostScript and Tests are
// therefore joined into the single test event.
//
// That merge is lossless for the collections that matter here. The importer
// maps Postman's test event onto PostScript and never populates Tests, so a
// Postman-origin collection round-trips byte-for-byte. A natively authored
// collection using both slots collapses them into one on the way out — and
// once collapsed it stays collapsed, which is exactly what makes
// import -> export -> import idempotent rather than drifting on every cycle.
func sharePostmanEvents(preScript, postScript, tests string) []interface{} {
	var events []interface{}
	add := func(listen, script string) {
		if strings.TrimSpace(script) == "" {
			return
		}
		events = append(events, map[string]interface{}{
			"listen": listen,
			"script": map[string]interface{}{
				"type": "text/javascript",
				// exec is a line array, which is how Postman writes it. A
				// single string is accepted by most readers but diffs as one
				// enormous line, making an exported collection unreviewable in
				// version control.
				"exec": strings.Split(script, "\n"),
			},
		})
	}
	add("prerequest", preScript)

	post := strings.TrimRight(postScript, "\n")
	if strings.TrimSpace(tests) != "" {
		if strings.TrimSpace(post) != "" {
			post += "\n"
		}
		post += tests
	}
	add("test", post)
	return events
}

// sharePostmanVariables exports collection variables.
func sharePostmanVariables(variables []Variable) []interface{} {
	out := make([]interface{}, 0, len(variables))
	for _, variable := range variables {
		name := strings.TrimSpace(variable.Name)
		if name == "" {
			continue
		}
		entry := map[string]interface{}{
			"key":   name,
			"value": scripting.ScriptVariableString(variable.Value),
		}
		if !variable.Enabled {
			entry["disabled"] = true
		}
		out = append(out, entry)
	}
	return out
}

func sharePostmanURL(item RequestItem) map[string]interface{} {
	url := map[string]interface{}{"raw": item.URL}
	if len(item.Params) > 0 {
		query := []map[string]interface{}{}
		for _, param := range item.Params {
			if strings.TrimSpace(param.Name) == "" {
				continue
			}
			entry := map[string]interface{}{"key": param.Name, "value": param.Value}
			if !param.Enabled {
				entry["disabled"] = true
			}
			query = append(query, entry)
		}
		if len(query) > 0 {
			url["query"] = query
		}
	}
	// US-053. Path params are a separate Postman key from query params, and
	// omitting them meant a :id placeholder round-tripped with no value — the
	// request imported looking complete and sent a literal ":id" to the server.
	if len(item.PathParams) > 0 {
		variables := []map[string]interface{}{}
		for _, param := range item.PathParams {
			if strings.TrimSpace(param.Name) == "" {
				continue
			}
			variables = append(variables, map[string]interface{}{"key": param.Name, "value": param.Value})
		}
		if len(variables) > 0 {
			url["variable"] = variables
		}
	}
	return url
}

func sharePostmanBody(item RequestItem) map[string]interface{} {
	if item.Type == "graphql" {
		return map[string]interface{}{
			"mode": "graphql",
			"graphql": map[string]interface{}{
				"query":     item.Body.GraphQLQuery,
				"variables": item.Body.GraphQLVariables,
			},
		}
	}
	switch item.Body.Mode {
	case "json":
		return sharePostmanRawBody(item.Body.JSON, "json")
	case "xml":
		return sharePostmanRawBody(item.Body.XML, "xml")
	case "text", "sparql":
		return sharePostmanRawBody(item.Body.Text, "text")
	case "formUrlEncoded":
		return map[string]interface{}{"mode": "urlencoded", "urlencoded": sharePostmanKeyValues(item.Body.FormURLEncoded, "key")}
	case "multipartForm":
		return map[string]interface{}{"mode": "formdata", "formdata": sharePostmanFormData(item.Body.Multipart)}
	case "file":
		if file := shareSelectedFileBodyEntry(item.Body); file != nil {
			return map[string]interface{}{"mode": "file", "file": map[string]interface{}{"src": file.FilePath}}
		}
	}
	return nil
}

func sharePostmanRawBody(body, language string) map[string]interface{} {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return map[string]interface{}{
		"mode": "raw",
		"raw":  body,
		"options": map[string]interface{}{
			"raw": map[string]interface{}{"language": language},
		},
	}
}

func sharePostmanKeyValues(values []KeyValue, keyName string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" {
			continue
		}
		entry := map[string]interface{}{keyName: value.Name, "value": value.Value}
		if !value.Enabled {
			entry["disabled"] = true
		}
		out = append(out, entry)
	}
	return out
}

func sharePostmanFormData(values []FormPart) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" {
			continue
		}
		entry := map[string]interface{}{
			"key":   value.Name,
			"value": value.Value,
			"type":  "text",
		}
		if strings.TrimSpace(value.FilePath) != "" {
			entry["type"] = "file"
			entry["src"] = value.FilePath
			delete(entry, "value")
		}
		if !value.Enabled {
			entry["disabled"] = true
		}
		out = append(out, entry)
	}
	return out
}

func shareSelectedFileBodyEntry(body RequestBody) *FileBodyEntry {
	for _, file := range fileBodyEntries(body) {
		if file.Selected && strings.TrimSpace(file.FilePath) != "" {
			copy := file
			return &copy
		}
	}
	return nil
}

func sharePostmanAuth(auth AuthConfig) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(auth.Mode)) {
	case "basic":
		return map[string]interface{}{"type": "basic", "basic": []map[string]interface{}{
			{"key": "username", "value": auth.Username, "type": "string"},
			{"key": "password", "value": auth.Password, "type": "string"},
		}}
	case "bearer":
		return map[string]interface{}{"type": "bearer", "bearer": []map[string]interface{}{
			{"key": "token", "value": auth.Token, "type": "string"},
		}}
	case "apikey":
		return map[string]interface{}{"type": "apikey", "apikey": []map[string]interface{}{
			{"key": "key", "value": auth.APIKey, "type": "string"},
			{"key": "value", "value": auth.APIValue, "type": "string"},
			{"key": "in", "value": firstNonEmpty(auth.APILocation, "header"), "type": "string"},
		}}
	default:
		return nil
	}
}

func buildCollectionDocsYAML(collection Collection, selectedEnvironmentIDs []string, generatedAt time.Time) (string, int, int, error) {
	root, err := yamlMapFromString(stringifyYAMLCollection(collection))
	if err != nil {
		return "", 0, 0, err
	}
	info, _ := mapValue(root["info"])
	if info == nil {
		info = map[string]interface{}{}
	}
	info["name"] = collection.Name
	if strings.TrimSpace(collection.Version) != "" {
		info["version"] = strings.TrimSpace(collection.Version)
	}
	root["info"] = info

	items, folderCount, requestCount, err := collectionDocsItems(collection, "")
	if err != nil {
		return "", 0, 0, err
	}
	root["items"] = items

	config, _ := mapValue(root["config"])
	if config == nil {
		config = map[string]interface{}{}
	}
	config["environments"] = yamlDocsEnvironments(collection.Environments, selectedEnvironmentIDs)
	root["config"] = config

	extensions, _ := mapValue(root["extensions"])
	if extensions == nil {
		extensions = map[string]interface{}{}
	}
	bruno, _ := mapValue(extensions["bruno"])
	if bruno == nil {
		bruno = map[string]interface{}{}
	}
	bruno["exportedAt"] = generatedAt.Format(time.RFC3339)
	bruno["exportedUsing"] = "LiteAPI"
	extensions["bruno"] = bruno
	root["extensions"] = extensions

	data, err := yaml.Marshal(root)
	if err != nil {
		return "", 0, 0, err
	}
	return string(data), folderCount, requestCount, nil
}

func buildCollectionDocsHTML(collectionName, yamlContent string) (string, error) {
	encoded, err := json.Marshal(yamlContent)
	if err != nil {
		return "", err
	}
	escapedYAML := strings.ReplaceAll(string(encoded), `</`, `<\/`)
	title := html.EscapeString(collectionName)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s - API Documentation</title>
    <style>
        body { margin: 0; padding: 0; }
        #opencollection-container { width: 100vw; height: 100vh; }
    </style>
    <link rel="stylesheet" href="https://cdn.opencollection.com/docs.css">
    <script src="https://cdn.opencollection.com/docs.js"></script>
</head>
<body>
    <div id="opencollection-container"></div>
    <script>
        const collectionData = %s;
        new window.OpenCollection({
            target: document.getElementById('opencollection-container'),
            opencollection: collectionData,
            theme: 'light'
        });
    </script>
</body>
</html>`, title, escapedYAML), nil
}

func yamlMapFromString(content string) (map[string]interface{}, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		raw = map[string]interface{}{}
	}
	return raw, nil
}

func collectionDocsItems(collection Collection, parentPath string) ([]map[string]interface{}, int, int, error) {
	folders := collectionDocsChildFolders(collection.Folders, parentPath)
	requests := collectionDocsChildRequests(collection.Items, parentPath)
	items := make([]map[string]interface{}, 0, len(folders)+len(requests))
	folderCount := 0
	requestCount := 0
	for _, folder := range folders {
		node, err := yamlMapFromString(stringifyYAMLFolder(folder))
		if err != nil {
			return nil, 0, 0, err
		}
		children, childFolders, childRequests, err := collectionDocsItems(collection, folder.DisplayPath)
		if err != nil {
			return nil, 0, 0, err
		}
		if len(children) > 0 {
			node["items"] = children
		}
		items = append(items, node)
		folderCount += 1 + childFolders
		requestCount += childRequests
	}
	for _, request := range requests {
		content, err := stringifyYAMLRequest(request)
		if err != nil {
			return nil, 0, 0, err
		}
		node, err := yamlMapFromString(content)
		if err != nil {
			return nil, 0, 0, err
		}
		items = append(items, node)
		requestCount++
	}
	return items, folderCount, requestCount, nil
}

func collectionDocsChildFolders(folders []FolderConfig, parentPath string) []FolderConfig {
	parentPath = normalizeFolderPathKey(parentPath)
	children := make([]FolderConfig, 0)
	for _, folder := range folders {
		displayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Path))
		if displayPath == "" {
			continue
		}
		if normalizeFolderPathKey(parentFolderDisplayPath(displayPath)) == parentPath {
			if folder.DisplayPath == "" {
				folder.DisplayPath = displayPath
			}
			children = append(children, folder)
		}
	}
	sortFoldersLikeBruno(children)
	return children
}

func collectionDocsChildRequests(items []RequestItem, parentPath string) []RequestItem {
	parentPath = normalizeFolderPathKey(parentPath)
	children := make([]RequestItem, 0)
	for _, item := range items {
		if item.Transient {
			continue
		}
		if !collectionDocsRequestIsExportable(item) {
			continue
		}
		if normalizeFolderPathKey(item.FolderPath) == parentPath {
			children = append(children, item)
		}
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].Seq != children[j].Seq {
			return children[i].Seq < children[j].Seq
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	return children
}

func collectionDocsRequestIsExportable(item RequestItem) bool {
	return item.Type == "" || item.Type == "http" || item.Type == "graphql" || item.Type == "websocket" || item.Type == "grpc"
}

func sortFoldersLikeBruno(folders []FolderConfig) {
	sort.SliceStable(folders, func(i, j int) bool {
		leftValid := folders[i].Seq > 0
		rightValid := folders[j].Seq > 0
		if leftValid && rightValid && folders[i].Seq != folders[j].Seq {
			return folders[i].Seq < folders[j].Seq
		}
		if leftValid != rightValid {
			return leftValid
		}
		return strings.ToLower(firstNonEmpty(folders[i].Name, folders[i].DisplayPath, folders[i].Path)) < strings.ToLower(firstNonEmpty(folders[j].Name, folders[j].DisplayPath, folders[j].Path))
	})
}

func parentFolderDisplayPath(path string) string {
	path = normalizeFolderPathKey(path)
	parent := filepath.ToSlash(filepath.Dir(path))
	if parent == "." {
		return ""
	}
	return parent
}

func yamlDocsEnvironments(environments []Environment, selectedEnvironmentIDs []string) []map[string]interface{} {
	includeAll := selectedEnvironmentIDs == nil
	selected := map[string]bool{}
	for _, id := range selectedEnvironmentIDs {
		selected[id] = true
	}
	out := make([]map[string]interface{}, 0, len(environments))
	for _, env := range environments {
		if !includeAll && !selected[env.ID] {
			continue
		}
		out = append(out, map[string]interface{}{
			"name":      env.Name,
			"color":     env.Color,
			"variables": yamlVariables(env.Variables),
		})
	}
	return out
}

func collectionDisplayVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" {
		return "v1.0.0"
	}
	parts := strings.SplitN(version, "-", 2)
	numbers := strings.Split(parts[0], ".")
	for len(numbers) < 3 {
		numbers = append(numbers, "0")
	}
	for index := range numbers[:3] {
		if _, err := strconv.Atoi(numbers[index]); err != nil {
			return "v1.0.0"
		}
	}
	normalized := strings.Join(numbers[:3], ".")
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		normalized += "-" + strings.TrimSpace(parts[1])
	}
	return "v" + normalized
}

func stringifyYAMLCollection(collection Collection) string {
	brunoExtensions := map[string]interface{}{
		"ignore": []string{"node_modules", ".git"},
	}
	if len(collection.OpenAPI) > 0 {
		brunoExtensions["openapi"] = yamlOpenAPISyncConfigs(collection.OpenAPI)
	}
	root := map[string]interface{}{
		"opencollection": "1.0.0",
		"info": map[string]interface{}{
			"name":    collection.Name,
			"version": firstNonEmpty(collection.Version, "1"),
		},
		"extensions": map[string]interface{}{
			"bruno": brunoExtensions,
		},
	}
	request := map[string]interface{}{}
	if len(collection.Headers) > 0 {
		request["headers"] = yamlKeyValues(collection.Headers)
	}
	if len(collection.Variables) > 0 {
		request["variables"] = yamlVariables(collection.Variables)
	}
	if len(collection.ResVariables) > 0 {
		request["actions"] = yamlPostResponseActions(collection.ResVariables)
	}
	if collection.Auth.Mode != "" && collection.Auth.Mode != "none" {
		request["auth"] = yamlAuth(collection.Auth)
	}
	scripts := []map[string]interface{}{}
	if collection.PreScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "before-request", "code": collection.PreScript})
	}
	if collection.PostScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "after-response", "code": collection.PostScript})
	}
	if collection.Tests != "" {
		scripts = append(scripts, map[string]interface{}{"type": "tests", "code": collection.Tests})
	}
	if len(scripts) > 0 {
		request["scripts"] = scripts
	}
	if len(request) > 0 {
		root["request"] = request
	}
	if strings.TrimSpace(collection.Docs) != "" {
		root["docs"] = map[string]interface{}{"content": collection.Docs}
	}
	config := map[string]interface{}{}
	if len(collection.Environments) > 0 {
		envs := make([]map[string]interface{}, 0, len(collection.Environments))
		for _, env := range collection.Environments {
			envs = append(envs, map[string]interface{}{
				"name":      env.Name,
				"color":     env.Color,
				"variables": yamlVariables(env.Variables),
			})
		}
		config["environments"] = envs
	}
	if transport.HasProxyConfig(collection.Proxy) {
		config["proxy"] = yamlProxyConfig(collection.Proxy)
	}
	if transport.HasClientCertificates(collection.ClientCertificates) {
		config["clientCertificates"] = yamlClientCertificates(collection.ClientCertificates)
	}
	if hasCollectionPresets(collection.Presets) {
		config["presets"] = yamlCollectionPresets(collection.Presets)
	}
	if hasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = yamlCollectionProtobuf(collection.Protobuf)
	}
	if len(config) > 0 {
		root["config"] = config
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

func stringifyYAMLFolder(folder FolderConfig) string {
	info := map[string]interface{}{
		"name": firstNonEmpty(folder.Name, filepath.Base(filepath.FromSlash(folder.Path))),
		"type": "folder",
	}
	if folder.Seq > 0 {
		info["seq"] = folder.Seq
	}
	root := map[string]interface{}{"info": info}
	request := map[string]interface{}{}
	if len(folder.Headers) > 0 {
		request["headers"] = yamlKeyValues(folder.Headers)
	}
	if len(folder.Variables) > 0 {
		request["variables"] = yamlVariables(folder.Variables)
	}
	if len(folder.ResVariables) > 0 {
		request["actions"] = yamlPostResponseActions(folder.ResVariables)
	}
	if folder.Auth.Mode != "" {
		request["auth"] = yamlAuth(folder.Auth)
	}
	scripts := []map[string]interface{}{}
	if folder.PreScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "before-request", "code": folder.PreScript})
	}
	if folder.PostScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "after-response", "code": folder.PostScript})
	}
	if folder.Tests != "" {
		scripts = append(scripts, map[string]interface{}{"type": "tests", "code": folder.Tests})
	}
	if len(scripts) > 0 {
		request["scripts"] = scripts
	}
	if len(request) > 0 {
		root["request"] = request
	}
	if strings.TrimSpace(folder.Docs) != "" {
		root["docs"] = map[string]interface{}{"content": folder.Docs}
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

func stringifyYAMLRequest(item RequestItem) (string, error) {
	requestType := item.Type
	if requestType == "" {
		requestType = "http"
	}
	root := map[string]interface{}{
		"info": map[string]interface{}{
			"name": item.Name,
			"type": requestType,
			"seq":  item.Seq,
		},
	}
	switch requestType {
	case "graphql":
		section := map[string]interface{}{
			"method": item.Method,
			"url":    item.URL,
			"body": map[string]interface{}{
				"query":     item.Body.GraphQLQuery,
				"variables": item.Body.GraphQLVariables,
			},
		}
		addCommonYAMLRequestFields(section, item)
		root["graphql"] = section
	case "websocket":
		section := map[string]interface{}{
			"url": item.URL,
		}
		if message := yamlWSMessage(item); message != nil {
			section["message"] = message
		}
		addCommonYAMLRequestFields(section, item)
		root["websocket"] = section
	case "grpc":
		section := map[string]interface{}{
			"url":    item.URL,
			"method": item.Method,
		}
		if strings.TrimSpace(item.GrpcMethodType) != "" {
			section["methodType"] = item.GrpcMethodType
		}
		if strings.TrimSpace(item.ProtoPath) != "" {
			section["protoFilePath"] = item.ProtoPath
		}
		if len(item.Headers) > 0 {
			section["metadata"] = yamlGrpcMetadata(item.Headers)
		}
		if len(item.GrpcMessages) > 0 {
			section["message"] = yamlGrpcMessages(item.GrpcMessages)
		}
		if item.Auth.Mode != "" && item.Auth.Mode != "none" {
			section["auth"] = yamlAuth(item.Auth)
		}
		root["grpc"] = section
	default:
		section := map[string]interface{}{
			"method": item.Method,
			"url":    item.URL,
		}
		addCommonYAMLRequestFields(section, item)
		if body := yamlBody(item.Body); len(body) > 0 {
			section["body"] = body
		}
		root["http"] = section
	}
	if len(item.Vars.Req) > 0 || len(item.Vars.Res) > 0 || item.PreScript != "" || item.PostScript != "" || item.Tests != "" {
		runtime := map[string]interface{}{}
		if len(item.Vars.Req) > 0 {
			runtime["variables"] = yamlVariables(item.Vars.Req)
		}
		if len(item.Vars.Res) > 0 {
			runtime["actions"] = yamlPostResponseActions(item.Vars.Res)
		}
		scripts := []map[string]interface{}{}
		if item.PreScript != "" {
			scripts = append(scripts, map[string]interface{}{"type": "before-request", "code": item.PreScript})
		}
		if item.PostScript != "" {
			scripts = append(scripts, map[string]interface{}{"type": "after-response", "code": item.PostScript})
		}
		if item.Tests != "" {
			scripts = append(scripts, map[string]interface{}{"type": "tests", "code": item.Tests})
		}
		if len(scripts) > 0 {
			runtime["scripts"] = scripts
		}
		root["runtime"] = runtime
	}
	root["settings"] = map[string]interface{}{
		"encodeUrl":         item.Settings.EncodeURL,
		"timeout":           item.Settings.TimeoutMs,
		"followRedirects":   item.Settings.FollowRedirects,
		"maxRedirects":      item.Settings.MaxRedirects,
		"storeCookies":      item.Settings.StoreCookies,
		"verifyTls":         item.Settings.VerifyTLS,
		"keepAliveInterval": item.Settings.KeepAliveInterval,
	}
	if strings.TrimSpace(item.Docs) != "" {
		root["docs"] = item.Docs
	}
	data, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func addCommonYAMLRequestFields(section map[string]interface{}, item RequestItem) {
	if len(item.Headers) > 0 {
		section["headers"] = yamlKeyValues(item.Headers)
	}
	if len(item.Params) > 0 || len(item.PathParams) > 0 {
		section["params"] = yamlParams(item.Params, item.PathParams)
	}
	if item.Auth.Mode != "" && item.Auth.Mode != "none" {
		section["auth"] = yamlAuth(item.Auth)
	}
}

func yamlBody(body RequestBody) map[string]interface{} {
	mode := yamlBodyType(body.Mode)
	if mode == "" || mode == "none" {
		return nil
	}
	result := map[string]interface{}{"type": mode}
	switch body.Mode {
	case "formUrlEncoded":
		result["data"] = yamlKeyValues(body.FormURLEncoded)
	case "multipartForm":
		result["data"] = yamlMultipart(body.Multipart)
	case "file":
		result["data"] = yamlFileBody(body)
	default:
		result["data"] = yamlBodyText(body)
	}
	return result
}

func yamlFileBody(body RequestBody) []map[string]interface{} {
	entries := fileBodyEntries(body)
	if len(entries) == 0 {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(entries))
	for _, file := range entries {
		if strings.TrimSpace(file.FilePath) == "" && strings.TrimSpace(file.ContentType) == "" {
			continue
		}
		entry := map[string]interface{}{
			"filePath": file.FilePath,
			"selected": file.Selected,
		}
		if strings.TrimSpace(file.ContentType) != "" {
			entry["contentType"] = file.ContentType
		}
		result = append(result, entry)
	}
	return result
}

func yamlBodyText(body RequestBody) string {
	switch body.Mode {
	case "json":
		return body.JSON
	case "xml":
		return body.XML
	default:
		return body.Text
	}
}

func yamlBodyType(mode string) string {
	switch mode {
	case "formUrlEncoded":
		return "form-urlencoded"
	case "multipartForm":
		return "multipart-form"
	case "":
		return "none"
	default:
		return mode
	}
}

func yamlWSMessage(item RequestItem) interface{} {
	messages := bru.WsMessagesForStorage(item)
	if len(messages) == 0 {
		return nil
	}
	if len(messages) == 1 {
		message := messages[0]
		if strings.TrimSpace(message.Name) == "" && !message.Selected {
			return map[string]interface{}{
				"type": normalizeWSMessageType(message.Type),
				"data": message.Content,
			}
		}
	}
	return yamlWSMessages(messages)
}

func yamlWSMessages(messages []WSMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.Name) == "" && strings.TrimSpace(message.Content) == "" {
			continue
		}
		title := message.Name
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("message %d", index+1)
		}
		result = append(result, map[string]interface{}{
			"title":    title,
			"selected": message.Selected,
			"message": map[string]interface{}{
				"type": normalizeWSMessageType(message.Type),
				"data": message.Content,
			},
		})
	}
	return result
}

func yamlKeyValues(values []KeyValue) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		entry := map[string]interface{}{
			"name":    value.Name,
			"value":   value.Value,
			"enabled": value.Enabled,
		}
		if value.Secret {
			entry["secret"] = true
		}
		if value.Description != "" {
			entry["description"] = value.Description
		}
		result = append(result, entry)
	}
	return result
}

func yamlGrpcMetadata(values []KeyValue) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		entry := map[string]interface{}{
			"name":  value.Name,
			"value": value.Value,
		}
		if !value.Enabled {
			entry["disabled"] = true
		}
		if value.Description != "" {
			entry["description"] = value.Description
		}
		result = append(result, entry)
	}
	return result
}

func yamlGrpcMessages(messages []GrpcMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.Name) == "" && strings.TrimSpace(message.Content) == "" {
			continue
		}
		title := message.Name
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("message %d", index+1)
		}
		result = append(result, map[string]interface{}{
			"title":   title,
			"message": message.Content,
		})
	}
	return result
}

func yamlOAuth2AdditionalParameters(auth OAuth2Auth) map[string]interface{} {
	result := map[string]interface{}{}
	if rows := yamlOAuth2AdditionalParams(auth.AuthorizationAdditionalParams); len(rows) > 0 {
		result["authorization"] = rows
	}
	if rows := yamlOAuth2AdditionalParams(auth.TokenAdditionalParams); len(rows) > 0 {
		result["token"] = rows
	}
	if rows := yamlOAuth2AdditionalParams(auth.RefreshAdditionalParams); len(rows) > 0 {
		result["refresh"] = rows
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func yamlOAuth2AdditionalParams(params []OAuth2AdditionalParam) []map[string]interface{} {
	result := []map[string]interface{}{}
	for _, param := range params {
		if strings.TrimSpace(param.Name) == "" {
			continue
		}
		entry := map[string]interface{}{
			"name":    param.Name,
			"value":   param.Value,
			"sendIn":  normalizeOAuth2AdditionalPlacement(param.SendIn),
			"enabled": param.Enabled,
		}
		if param.Secret {
			entry["secret"] = true
		}
		if param.Description != "" {
			entry["description"] = param.Description
		}
		result = append(result, entry)
	}
	return result
}

func parseYAMLProxyConfig(raw interface{}) (ProxyConfig, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return ProxyConfig{}, false
	}
	proxy := ProxyConfig{
		Inherit:  boolValue(valueMap["inherit"], false),
		Disabled: boolValue(valueMap["disabled"], false),
	}
	configMap, _ := mapValue(valueMap["config"])
	if configMap == nil {
		configMap = valueMap
	}
	proxy.Protocol = firstNonEmpty(firstYAMLString(configMap, "protocol"), "http")
	proxy.Hostname = firstYAMLString(configMap, "hostname", "host")
	proxy.Port = strings.TrimSpace(yamlScalarString(configMap["port"]))
	proxy.BypassProxy = firstYAMLString(configMap, "bypassProxy", "bypass_proxy", "noProxy", "no_proxy")
	if authMap, ok := mapValue(configMap["auth"]); ok {
		proxy.Auth.Username = firstYAMLString(authMap, "username", "user")
		proxy.Auth.Password = firstYAMLString(authMap, "password", "pass")
		proxy.Auth.Disabled = boolValue(authMap["disabled"], false)
		if enabled, ok := boolValueOK(authMap["enabled"]); ok {
			proxy.Auth.Disabled = !enabled
		}
	}
	return transport.NormalizeProxyConfig(proxy), true
}

func parseJSONProxyConfig(raw interface{}) (ProxyConfig, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return ProxyConfig{}, false
	}
	if _, hasNewConfig := valueMap["config"]; hasNewConfig {
		return parseYAMLProxyConfig(valueMap)
	}
	proxy := ProxyConfig{
		Inherit:     false,
		Disabled:    !boolValue(valueMap["enabled"], false),
		Protocol:    firstNonEmpty(firstYAMLString(valueMap, "protocol"), "http"),
		Hostname:    firstYAMLString(valueMap, "hostname", "host"),
		Port:        strings.TrimSpace(yamlScalarString(valueMap["port"])),
		BypassProxy: firstYAMLString(valueMap, "bypassProxy", "bypass_proxy", "noProxy", "no_proxy"),
	}
	if mode := strings.ToLower(firstYAMLString(valueMap, "mode", "source")); mode != "" {
		switch mode {
		case "off", "disabled":
			proxy.Disabled = true
		case "system", "inherit":
			proxy.Inherit = true
			proxy.Disabled = false
		case "on", "manual":
			proxy.Inherit = false
			proxy.Disabled = false
		}
	}
	if authMap, ok := mapValue(valueMap["auth"]); ok {
		proxy.Auth.Username = firstYAMLString(authMap, "username", "user")
		proxy.Auth.Password = firstYAMLString(authMap, "password", "pass")
		proxy.Auth.Disabled = !boolValue(authMap["enabled"], true)
		if disabled, ok := boolValueOK(authMap["disabled"]); ok {
			proxy.Auth.Disabled = disabled
		}
	}
	return transport.NormalizeProxyConfig(proxy), true
}

func yamlProxyConfig(proxy ProxyConfig) map[string]interface{} {
	proxy = transport.NormalizeProxyConfig(proxy)
	config := map[string]interface{}{
		"protocol":    firstNonEmpty(proxy.Protocol, "http"),
		"hostname":    proxy.Hostname,
		"port":        proxyPortValue(proxy.Port),
		"auth":        map[string]interface{}{"username": proxy.Auth.Username, "password": proxy.Auth.Password},
		"bypassProxy": proxy.BypassProxy,
	}
	if proxy.Auth.Disabled {
		config["auth"].(map[string]interface{})["disabled"] = true
	}
	result := map[string]interface{}{"inherit": proxy.Inherit, "config": config}
	if proxy.Disabled {
		result["disabled"] = true
	}
	return result
}

func jsonProxyConfig(proxy ProxyConfig) map[string]interface{} {
	return yamlProxyConfig(proxy)
}

func parseYAMLClientCertificates(raw interface{}) ([]ClientCertificateConfig, bool) {
	values, ok := listValue(raw)
	if !ok {
		return nil, false
	}
	certs := make([]ClientCertificateConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := mapValue(value)
		if !ok {
			continue
		}
		cert := ClientCertificateConfig{
			Domain:     firstYAMLString(valueMap, "domain"),
			Type:       firstYAMLString(valueMap, "type"),
			Passphrase: firstYAMLString(valueMap, "passphrase"),
		}
		switch strings.ToLower(strings.TrimSpace(cert.Type)) {
		case "pem", "cert", "":
			cert.Type = "cert"
			cert.CertFilePath = firstYAMLString(valueMap, "certificateFilePath", "certFilePath", "cert")
			cert.KeyFilePath = firstYAMLString(valueMap, "privateKeyFilePath", "keyFilePath", "key")
		case "pkcs12", "pfx":
			cert.Type = "pfx"
			cert.PFXFilePath = firstYAMLString(valueMap, "pkcs12FilePath", "pfxFilePath", "pfx")
		default:
			cert.CertFilePath = firstYAMLString(valueMap, "certificateFilePath", "certFilePath", "cert")
			cert.KeyFilePath = firstYAMLString(valueMap, "privateKeyFilePath", "keyFilePath", "key")
			cert.PFXFilePath = firstYAMLString(valueMap, "pkcs12FilePath", "pfxFilePath", "pfx")
		}
		certs = append(certs, cert)
	}
	return transport.NormalizeClientCertificates(certs), true
}

func parseJSONClientCertificates(raw interface{}) ([]ClientCertificateConfig, bool) {
	valueMap, ok := mapValue(raw)
	if ok {
		return parseYAMLBrunoClientCertificateList(valueMap["certs"])
	}
	return parseYAMLBrunoClientCertificateList(raw)
}

func parseYAMLBrunoClientCertificateList(raw interface{}) ([]ClientCertificateConfig, bool) {
	values, ok := listValue(raw)
	if !ok {
		return nil, false
	}
	certs := make([]ClientCertificateConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := mapValue(value)
		if !ok {
			continue
		}
		certs = append(certs, ClientCertificateConfig{
			Domain:       firstYAMLString(valueMap, "domain"),
			Type:         firstNonEmpty(firstYAMLString(valueMap, "type"), "cert"),
			CertFilePath: firstYAMLString(valueMap, "certFilePath", "certificateFilePath", "cert"),
			KeyFilePath:  firstYAMLString(valueMap, "keyFilePath", "privateKeyFilePath", "key"),
			PFXFilePath:  firstYAMLString(valueMap, "pfxFilePath", "pkcs12FilePath", "pfx"),
			Passphrase:   firstYAMLString(valueMap, "passphrase"),
		})
	}
	return transport.NormalizeClientCertificates(certs), true
}

func yamlClientCertificates(certs []ClientCertificateConfig) []map[string]interface{} {
	normalized := transport.NormalizeClientCertificates(certs)
	result := make([]map[string]interface{}, 0, len(normalized))
	for _, cert := range normalized {
		entry := map[string]interface{}{
			"domain": cert.Domain,
		}
		if cert.Type == "pfx" {
			entry["type"] = "pkcs12"
			entry["pkcs12FilePath"] = cert.PFXFilePath
		} else {
			entry["type"] = "pem"
			entry["certificateFilePath"] = cert.CertFilePath
			entry["privateKeyFilePath"] = cert.KeyFilePath
		}
		if cert.Passphrase != "" {
			entry["passphrase"] = cert.Passphrase
		}
		result = append(result, entry)
	}
	return result
}

func jsonClientCertificates(certs []ClientCertificateConfig) map[string]interface{} {
	normalized := transport.NormalizeClientCertificates(certs)
	entries := make([]map[string]interface{}, 0, len(normalized))
	for _, cert := range normalized {
		entry := map[string]interface{}{
			"domain": cert.Domain,
			"type":   firstNonEmpty(cert.Type, "cert"),
		}
		if cert.Type == "pfx" {
			entry["pfxFilePath"] = cert.PFXFilePath
		} else {
			entry["certFilePath"] = cert.CertFilePath
			entry["keyFilePath"] = cert.KeyFilePath
		}
		if cert.Passphrase != "" {
			entry["passphrase"] = cert.Passphrase
		}
		entries = append(entries, entry)
	}
	return map[string]interface{}{"enabled": true, "certs": entries}
}

func parseCollectionPresets(raw interface{}) (CollectionPresets, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return CollectionPresets{}, false
	}
	presets := CollectionPresets{
		RequestType: firstYAMLString(valueMap, "requestType", "request_type", "type"),
		RequestURL:  firstYAMLString(valueMap, "requestUrl", "requestURL", "request_url", "url"),
	}
	return normalizeCollectionPresets(presets), true
}

func yamlCollectionPresets(presets CollectionPresets) map[string]interface{} {
	return jsonCollectionPresets(presets)
}

func jsonCollectionPresets(presets CollectionPresets) map[string]interface{} {
	normalized := normalizeCollectionPresets(presets)
	return map[string]interface{}{
		"requestType": brunoPresetRequestType(normalized.RequestType),
		"requestUrl":  normalized.RequestURL,
	}
}

func parseCollectionProtobuf(raw interface{}) (CollectionProtobufConfig, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return CollectionProtobufConfig{}, false
	}
	result := CollectionProtobufConfig{}
	if values, ok := listValue(valueMap["protoFiles"]); ok {
		result.ProtoFiles = make([]CollectionProtoFile, 0, len(values))
		for _, value := range values {
			if valueMap, ok := mapValue(value); ok {
				path := firstYAMLString(valueMap, "path", "filePath", "protoFilePath", "proto_file_path")
				if path == "" {
					continue
				}
				result.ProtoFiles = append(result.ProtoFiles, CollectionProtoFile{
					Path:   path,
					Type:   firstNonEmpty(firstYAMLString(valueMap, "type"), "file"),
					Exists: boolValue(valueMap["exists"], false),
				})
				continue
			}
			if path := strings.TrimSpace(yamlScalarString(value)); path != "" {
				result.ProtoFiles = append(result.ProtoFiles, CollectionProtoFile{Path: path, Type: "file"})
			}
		}
	}
	if values, ok := listValue(valueMap["importPaths"]); ok {
		result.ImportPaths = make([]CollectionProtoImportPath, 0, len(values))
		for _, value := range values {
			if valueMap, ok := mapValue(value); ok {
				path := firstYAMLString(valueMap, "path", "directoryPath", "directory", "dir")
				if path == "" {
					continue
				}
				enabled := true
				if parsed, ok := boolValueOK(valueMap["enabled"]); ok {
					enabled = parsed
				} else if disabled, ok := boolValueOK(valueMap["disabled"]); ok {
					enabled = !disabled
				}
				result.ImportPaths = append(result.ImportPaths, CollectionProtoImportPath{
					Path:    path,
					Enabled: enabled,
					Exists:  boolValue(valueMap["exists"], false),
				})
				continue
			}
			if path := strings.TrimSpace(yamlScalarString(value)); path != "" {
				result.ImportPaths = append(result.ImportPaths, CollectionProtoImportPath{Path: path, Enabled: true})
			}
		}
	}
	return result, true
}

func yamlCollectionProtobuf(protobuf CollectionProtobufConfig) map[string]interface{} {
	normalized := normalizeCollectionProtobuf("", protobuf)
	result := map[string]interface{}{}
	if len(normalized.ProtoFiles) > 0 {
		protoFiles := make([]map[string]interface{}, 0, len(normalized.ProtoFiles))
		for _, protoFile := range normalized.ProtoFiles {
			entry := map[string]interface{}{
				"path": protoFile.Path,
				"type": firstNonEmpty(protoFile.Type, "file"),
			}
			protoFiles = append(protoFiles, entry)
		}
		result["protoFiles"] = protoFiles
	}
	if len(normalized.ImportPaths) > 0 {
		importPaths := make([]map[string]interface{}, 0, len(normalized.ImportPaths))
		for _, importPath := range normalized.ImportPaths {
			importPaths = append(importPaths, map[string]interface{}{
				"path":    importPath.Path,
				"enabled": importPath.Enabled,
			})
		}
		result["importPaths"] = importPaths
	}
	return result
}

func jsonCollectionProtobuf(protobuf CollectionProtobufConfig) map[string]interface{} {
	return yamlCollectionProtobuf(protobuf)
}

func parseOpenAPISyncConfigs(raw interface{}) []OpenAPISyncConfig {
	values, ok := listValue(raw)
	if !ok {
		if valueMap, mapOK := mapValue(raw); mapOK {
			values = []interface{}{valueMap}
			ok = true
		}
	}
	if !ok {
		return nil
	}
	configs := make([]OpenAPISyncConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := mapValue(value)
		if !ok {
			continue
		}
		config := OpenAPISyncConfig{
			SourceURL:         firstYAMLString(valueMap, "sourceUrl", "sourceURL", "source_url", "url"),
			GroupBy:           firstYAMLString(valueMap, "groupBy", "group_by"),
			LastSyncDate:      firstYAMLString(valueMap, "lastSyncDate", "last_sync_date"),
			SpecHash:          firstYAMLString(valueMap, "specHash", "spec_hash"),
			AutoCheck:         boolValue(valueMap["autoCheck"], true),
			AutoCheckInterval: intValue(valueMap["autoCheckInterval"], 5),
		}
		config = normalizeOpenAPISyncConfig(config)
		if strings.TrimSpace(config.SourceURL) == "" && strings.TrimSpace(config.SpecHash) == "" {
			continue
		}
		configs = append(configs, config)
	}
	return configs
}

func yamlOpenAPISyncConfigs(configs []OpenAPISyncConfig) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(configs))
	for _, config := range configs {
		normalized := normalizeOpenAPISyncConfig(config)
		entry := map[string]interface{}{
			"sourceUrl":         normalized.SourceURL,
			"groupBy":           normalized.GroupBy,
			"autoCheck":         normalized.AutoCheck,
			"autoCheckInterval": normalized.AutoCheckInterval,
		}
		if strings.TrimSpace(normalized.LastSyncDate) != "" {
			entry["lastSyncDate"] = normalized.LastSyncDate
		}
		if strings.TrimSpace(normalized.SpecHash) != "" {
			entry["specHash"] = normalized.SpecHash
		}
		result = append(result, entry)
	}
	return result
}

func jsonOpenAPISyncConfigs(configs []OpenAPISyncConfig) []map[string]interface{} {
	return yamlOpenAPISyncConfigs(configs)
}

func proxyPortValue(port string) interface{} {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	if parsed, err := strconv.Atoi(port); err == nil {
		return parsed
	}
	return port
}

func yamlParams(queryParams, pathParams []KeyValue) []map[string]interface{} {
	result := yamlParamsOfType(queryParams, "query")
	result = append(result, yamlParamsOfType(pathParams, "path")...)
	return result
}

func yamlParamsOfType(values []KeyValue, paramType string) []map[string]interface{} {
	result := yamlKeyValues(values)
	for _, value := range result {
		value["type"] = paramType
	}
	return result
}

func yamlMultipart(values []FormPart) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, part := range values {
		entry := map[string]interface{}{
			"name":    part.Name,
			"enabled": part.Enabled,
		}
		if part.FilePath != "" {
			entry["type"] = "file"
			entry["value"] = part.FilePath
		} else {
			entry["type"] = "text"
			entry["value"] = part.Value
		}
		if strings.TrimSpace(part.ContentType) != "" {
			entry["contentType"] = part.ContentType
		}
		result = append(result, entry)
	}
	return result
}

func yamlPostResponseActions(values []Variable) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		action := map[string]interface{}{
			"type":  "set-variable",
			"phase": "after-response",
			"selector": map[string]interface{}{
				"expression": fmt.Sprint(value.Value),
				"method":     "jsonq",
			},
			"variable": map[string]interface{}{
				"name":  value.Name,
				"scope": "runtime",
			},
		}
		if !value.Enabled {
			action["disabled"] = true
		}
		if value.Secret {
			action["variable"].(map[string]interface{})["secret"] = true
		}
		result = append(result, action)
	}
	return result
}

func yamlAuth(auth AuthConfig) interface{} {
	switch auth.Mode {
	case "inherit", "none":
		return auth.Mode
	case "basic", "digest", "wsse":
		return map[string]interface{}{"mode": auth.Mode, "username": auth.Username, "password": auth.Password}
	case "ntlm":
		return map[string]interface{}{"mode": "ntlm", "username": auth.Username, "password": auth.Password, "domain": auth.Domain}
	case "bearer", "oauth2":
		if auth.Mode == "oauth2" {
			result := map[string]interface{}{
				"mode":                 "oauth2",
				"token":                auth.Token,
				"grantType":            auth.OAuth2.GrantType,
				"callbackUrl":          auth.OAuth2.CallbackURL,
				"authorizationUrl":     auth.OAuth2.AuthorizationURL,
				"accessTokenUrl":       auth.OAuth2.AccessTokenURL,
				"refreshTokenUrl":      auth.OAuth2.RefreshTokenURL,
				"username":             auth.OAuth2.Username,
				"password":             auth.OAuth2.Password,
				"clientId":             auth.OAuth2.ClientID,
				"clientSecret":         auth.OAuth2.ClientSecret,
				"scope":                auth.OAuth2.Scope,
				"state":                auth.OAuth2.State,
				"pkce":                 auth.OAuth2.PKCE,
				"credentialsPlacement": auth.OAuth2.CredentialsPlacement,
				"credentialsId":        auth.OAuth2.CredentialsID,
				"tokenSource":          auth.OAuth2.TokenSource,
				"tokenPlacement":       auth.OAuth2.TokenPlacement,
				"tokenHeaderPrefix":    auth.OAuth2.TokenHeaderPrefix,
				"tokenQueryKey":        auth.OAuth2.TokenQueryKey,
				"autoFetchToken":       auth.OAuth2.AutoFetchToken,
				"autoRefreshToken":     auth.OAuth2.AutoRefreshToken,
			}
			if additional := yamlOAuth2AdditionalParameters(auth.OAuth2); len(additional) > 0 {
				result["additionalParameters"] = additional
			}
			return result
		}
		return map[string]interface{}{"mode": auth.Mode, "token": auth.Token}
	case "apikey":
		return map[string]interface{}{"mode": "apikey", "key": auth.APIKey, "value": auth.APIValue, "location": auth.APILocation}
	case "awsv4":
		return map[string]interface{}{
			"mode":            "awsv4",
			"accessKeyId":     firstNonEmpty(auth.AWSV4.AccessKeyID, auth.AWSV4.AccessKey),
			"secretAccessKey": firstNonEmpty(auth.AWSV4.SecretAccessKey, auth.AWSV4.SecretKey),
			"sessionToken":    auth.AWSV4.SessionToken,
			"service":         auth.AWSV4.Service,
			"region":          auth.AWSV4.Region,
			"profileName":     auth.AWSV4.ProfileName,
		}
	case "oauth1":
		return map[string]interface{}{
			"mode":              "oauth1",
			"consumerKey":       auth.OAuth1.ConsumerKey,
			"consumerSecret":    auth.OAuth1.ConsumerSecret,
			"accessToken":       auth.OAuth1.AccessToken,
			"accessTokenSecret": auth.OAuth1.AccessTokenSecret,
			"callbackUrl":       auth.OAuth1.CallbackURL,
			"verifier":          auth.OAuth1.Verifier,
			"signatureMethod":   auth.OAuth1.SignatureMethod,
			"privateKey":        auth.OAuth1.PrivateKey,
			"privateKeyType":    auth.OAuth1.PrivateKeyType,
			"timestamp":         auth.OAuth1.Timestamp,
			"nonce":             auth.OAuth1.Nonce,
			"version":           auth.OAuth1.Version,
			"realm":             auth.OAuth1.Realm,
			"placement":         auth.OAuth1.Placement,
			"includeBodyHash":   auth.OAuth1.IncludeBodyHash,
		}
	default:
		return auth.Mode
	}
}

func firstYAMLString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(yamlScalarString(raw[key])); value != "" {
			return value
		}
	}
	return ""
}

// Bruno .bru parsing moved to internal/store/bru, along with the YAML body
// readers it shares with the YAML request reader still here.

func parseYAMLKeyValues(raw interface{}, queryOnly bool) []KeyValue {
	return bru.ParseYAMLKeyValues(raw, queryOnly)
}

func readCollectionEnvironments(collectionPath string, ignorePatterns []string) ([]Environment, error) {
	envPath := filepath.Join(collectionPath, "environments")
	info, err := os.Stat(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(envPath)
	if err != nil {
		return nil, err
	}
	environments := []Environment{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(envPath, entry.Name())
		if collectionPathIgnored(collectionPath, path, ignorePatterns) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".bru":
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			environments = append(environments, parseBruEnvironmentFile(strings.TrimSuffix(entry.Name(), ext), string(content)))
		case ".yml", ".yaml":
			env, err := parseYAMLEnvironmentFile(path)
			if err != nil {
				return nil, err
			}
			environments = append(environments, env)
		}
	}
	sort.SliceStable(environments, func(i, j int) bool {
		return strings.ToLower(environments[i].Name) < strings.ToLower(environments[j].Name)
	})
	return environments, nil
}

func readWorkspaceGlobalEnvironments(workspacePath string) ([]Environment, error) {
	envPath := filepath.Join(workspacePath, "environments")
	info, err := os.Stat(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(envPath)
	if err != nil {
		return nil, err
	}
	environments := []Environment{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(envPath, entry.Name())
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".bru":
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			env := parseBruEnvironmentFile(strings.TrimSuffix(entry.Name(), ext), string(content))
			env.ID = deterministicID("global-env", filepath.ToSlash(path))
			environments = append(environments, env)
		case ".yml", ".yaml":
			env, err := parseYAMLEnvironmentFile(path)
			if err != nil {
				return nil, err
			}
			env.ID = deterministicID("global-env", filepath.ToSlash(path))
			environments = append(environments, env)
		}
	}
	sort.SliceStable(environments, func(i, j int) bool {
		return strings.ToLower(environments[i].Name) < strings.ToLower(environments[j].Name)
	})
	return environments, nil
}

func migrateWorkspaceActiveGlobalEnvironmentFromConfig(workspace *Workspace) (bool, error) {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return false, nil
	}
	configPath := filepath.Join(workspace.Path, "workspace.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, nil
	}
	root := yamlDocumentMappingRoot(&doc)
	legacyUID, ok := yamlMappingScalar(root, "activeEnvironmentUid")
	if !ok || strings.TrimSpace(legacyUID) == "" {
		return false, nil
	}
	if environmentID, ok, err := workspaceGlobalEnvironmentIDForBrunoUID(workspace.Path, strings.TrimSpace(legacyUID)); err != nil {
		return false, err
	} else if ok {
		workspace.ActiveGlobalEnvironmentID = environmentID
	}
	if yamlRemoveMappingKey(root, "activeEnvironmentUid") {
		updated, err := yaml.Marshal(&doc)
		if err != nil {
			return false, err
		}
		if err := os.WriteFile(configPath, updated, 0o600); err != nil {
			return false, err
		}
	}
	return true, nil
}

func workspaceGlobalEnvironmentIDForBrunoUID(workspacePath, uid string) (string, bool, error) {
	envPath := filepath.Join(workspacePath, "environments")
	entries, err := os.ReadDir(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(envPath, entry.Name())
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".bru" && ext != ".yml" && ext != ".yaml" {
			continue
		}
		if bru.BrunoWorkspaceEnvironmentUIDForPath(path) == uid {
			return deterministicID("global-env", filepath.ToSlash(path)), true, nil
		}
	}
	return "", false, nil
}

func yamlDocumentMappingRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

func yamlMappingScalar(root *yaml.Node, key string) (string, bool) {
	if root == nil || root.Kind != yaml.MappingNode {
		return "", false
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			return yamlScalarString(root.Content[index+1].Value), true
		}
	}
	return "", false
}

func yamlRemoveMappingKey(root *yaml.Node, key string) bool {
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			root.Content = append(root.Content[:index], root.Content[index+2:]...)
			return true
		}
	}
	return false
}

func parseBruEnvironmentFile(name, content string) Environment {
	sections := bru.ParseSections(content)
	env := Environment{ID: newID("env"), Name: name, Color: bru.ParseBruTopLevelScalar(content, "color"), Variables: []Variable{}}
	if vars, ok := sections["vars"]; ok {
		env.Variables = bru.ParseVariables(vars, false)
	}
	if secrets, ok := sections["vars:secret"]; ok {
		env.Variables = bru.MergeSecretVariables(env.Variables, bru.ParseVariables(secrets, true))
	}
	return env
}

func parseYAMLEnvironmentFile(path string) (Environment, error) {
	root, err := parseYAMLMapFile(path)
	if err != nil {
		return Environment{}, err
	}
	return yamlstore.EnvironmentFromYAMLMap(root, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))), nil
}

func writeGlobalEnvironmentExportResult(result GlobalEnvironmentExportResult, targetPath string) ([]string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, errors.New("export target path is required")
	}
	targetPath = filepath.Clean(targetPath)
	if result.Format == "folder" {
		if len(result.Files) == 0 {
			return nil, errors.New("folder export has no files")
		}
		if err := os.MkdirAll(targetPath, 0o755); err != nil {
			return nil, err
		}
		written := make([]string, 0, len(result.Files))
		for _, file := range result.Files {
			name := filepath.Base(strings.TrimSpace(file.Name))
			if name == "" || name == "." || name == string(filepath.Separator) {
				return nil, errors.New("folder export contains an invalid file name")
			}
			path := filepath.Join(targetPath, name)
			if err := os.WriteFile(path, []byte(file.Content), 0o600); err != nil {
				return nil, err
			}
			written = append(written, path)
		}
		return written, nil
	}
	if result.Content == "" {
		return nil, errors.New("global environment export content is empty")
	}
	if parent := filepath.Dir(targetPath); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(targetPath, []byte(result.Content), 0o600); err != nil {
		return nil, err
	}
	return []string{targetPath}, nil
}

func expandUserExportPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("export target path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
	}
	return filepath.Clean(path), nil
}

func (a *App) defaultSaveDialogDirectory() string {
	if a == nil || strings.TrimSpace(a.dataDir) == "" {
		return ""
	}
	info, err := os.Stat(a.dataDir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return a.dataDir
}

// .bru serialisation and environment export moved to internal/store/bru.

func defaultDataDir() string {
	if fromEnv := os.Getenv("LITEAPI_DATA_DIR"); fromEnv != "" {
		return fromEnv
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "LiteAPI")
}

// installPostmanVisualizer implements pm.visualizer.set (US-058).
//
// Recorded on the request state rather than applied to the response directly:
// the pre-request script runs before a response exists, and a visualizer set
// there must still reach the response that follows.
// The Postman script API installers moved to internal/scripting.

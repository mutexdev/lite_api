<script lang="ts">
	  import { onDestroy, onMount, tick } from 'svelte'
	  import FileBodyTable from './lib/FileBodyTable.svelte'
	  import KeyValueTable from './lib/KeyValueTable.svelte'
	  import MultipartTable from './lib/MultipartTable.svelte'
	  import OAuth2AdditionalParams from './lib/OAuth2AdditionalParams.svelte'
  import VariableTextOverlay from './lib/VariableTextOverlay.svelte'
  import RequestCommandStrip from './lib/workbench/RequestCommandStrip.svelte'
  import ResponseInspector from './lib/workbench/ResponseInspector.svelte'
  import {
    applyLiveSessionPush,
    emptyLiveSessionLog,
    liveSessionKey,
    type LiveSessionLog,
    type LiveSessionPush,
  } from './lib/liveSessionEvents'
  import { applyRequestMutation, applyTabsMutation, type MergeOutcome } from './lib/narrowMutations'
  import { PatchCoalescer } from './lib/patchQueue'
  // US-036: the lazy wrapper, not CodeEditor itself — importing the real one
  // here is what pulled all of CodeMirror into the initial chunk.
  import CodeEditor from './lib/workbench/LazyCodeEditor.svelte'
  import RequestSettingsPanel from './lib/workbench/RequestSettingsPanel.svelte'
  import ProtocolRequestLine from './lib/workbench/ProtocolRequestLine.svelte'
  import WorkspaceCommandBar from './lib/workbench/WorkspaceCommandBar.svelte'
  import WorkspaceWindowPicker from './lib/workbench/WorkspaceWindowPicker.svelte'
  import { hasReplaceImportSelection, selectedImportRows } from './lib/importPlanning'
  import { canPushGitBranch, canStageGitSelection, canSwitchGitBranch, canUnstageGitSelection, reconcileGitRemoteBranch, reconcileGitSelection } from './lib/gitWorkbench'
  import type { RequestCommandState } from './lib/workbench/types'
  import {
    commandPaletteCommandIDs,
    workbenchCommandMetadata,
    type WorkbenchCommandID
  } from './lib/workbench/workbenchCommands'
  import { planUnsavedClose, requestDeletionAction, type LifecycleOpenTab, type LifecycleRequest, type UnsavedRequestTab } from './lib/workbench/tabLifecycle'
  import {
    AddCookieFromHeader,
    CancelCollectionRun,
    CancelGRPCStream,
    CancelRequest,
    ClearFileCache,
    ClearSSLSessionCache,
    ClearCookies,
    ClearDomainCookies,
    CloseAllTabs,
    CloseTab,
		CheckoutCollectionGitBranch,
    ClearNotifications,
    CloneGitRepository,
    CloneCollection,
    CloneFolder,
    CloneRequest,
	    CloneResponseExample,
    ApplyOpenAPILocalDrift,
	    ApplyOpenAPISync,
	    CheckOpenAPILocalDrift,
	    CheckOpenAPIUpdates,
	    CheckOpenAPISync,
	    CompleteOAuth2Callback,
	    ConnectCollectionGitRemote,
    ConnectOpenAPISync,
    ConnectGRPCStream,
    ConnectGRPCStreamWithPromptValues,
	    ConnectWebSocket,
    ConnectWebSocketWithPromptValues,
		CommitCollectionGit,
    CreateCollection,
    CreateEnvironment,
    CreateFolder,
    CreateGlobalEnvironment,
    CreateTerminalSession,
    CreateResponseExample,
    CreateRequest,
    CreateWorkspace,
		CreateCollectionGitBranch,
    CopyGlobalEnvironment,
    CopyGlobalEnvironmentAs,
    DeleteCookie,
    DeleteDotEnvFile,
    DeleteFolderRecoverable,
    DeleteGlobalEnvironment,
    DeleteRequestRecoverable,
    DeleteResponseExample,
    DiscardRecoveryEntry,
    DiscardRequestDraft,
    DisconnectCollectionGitRemote,
    DisconnectOpenAPISync,
	    DisconnectWebSocket,
    EndGRPCStream,
    ExportCollectionWithOptions,
    ExportGlobalEnvironment,
    ExportGlobalEnvironments,
    GenerateGrpcurlCommand,
	    GenerateRequestCode,
	    GenerateResponseExampleCode,
	    GenerateGRPCMessage,
	    GenerateCollectionDocs,
	    GetOpenAPISyncSpec,
	    GetOpenAPISyncSpecDiff,
	    GetDevToolsSnapshot,
		GetCollectionGitDiff,
		GetCollectionGitStatus,
    GetFileCacheSize,
    GetState,
	    GetWebStorageScope,
    GetTerminalSession,
    GitVersion,
		InitializeCollectionGit,
    ImportCollection,
	    ApplyCollectionImport,
	    ChooseCollectionImportFiles,
	    ChooseCollectionImportFolder,
	    PreviewCollectionImport,
    ImportGlobalEnvironment,
    ListDotEnvFiles,
    ListGRPCMethods,
    ListRecoveryEntries,
    ListTerminalSessions,
    ListWorkspaceWindowTargets,
    MarkAllNotificationsRead,
    MarkNotificationRead,
    MoveOpenTab,
    OpenCollection,
    OpenGitCollections,
    OpenNewWindow,
    OpenResponseExampleTab,
    OpenRequestTab,
    OpenWorkspaceInNewWindow,
		PullCollectionGit,
		PushCollectionGit,
    ReopenLastClosedTab,
    RevealCollectionInFolder,
    RevealCollectionFolderInFolder,
    RevealRequestInFolder,
    RemoveCollectionRecoverable,
    ResolveCollectionFolderPath,
    ResetDemoData,
    RefreshChangedCollections,
    RefreshCollection,
    RenameCollection,
    RenameFolder,
    RenameRequest,
    RenameResponseExample,
    ResolveProcessEnvValues,
    RestoreRecoveryEntry,
    ResizeTerminalSession,
    RunCollectionWithOptions,
    SaveResponseExample,
		SetCollectionGitRemote,
    SaveAllTabs,
    SaveCookie,
    SaveDotEnvFile,
		StageCollectionGitPaths,
		UnstageCollectionGitPaths,
		FetchCollectionGit,
    FlushPendingWrites,
    MoveOpenTabNarrow,
    SetActiveTabNarrow,
    UpdateOpenTabPanesNarrow,
    UpdateRequestNarrow,
    SaveGlobalEnvironmentExport,
    SaveRequest,
    SaveResponseBody,
    SaveResponseTimeline,
    SelectCustomCaCertificate,
    SelectDefaultLocation,
    SendGRPCStreamMessage,
    SendGRPCStreamMessageWithPromptValues,
    SendRequest,
    SendRequestWithPromptValues,
	    SendWebSocketMessage,
	    SendWebSocketMessageWithPromptValues,
    SetActiveGlobalEnvironment,
    SetActiveTab,
    SetActiveWorkspace,
    KillTerminalSession,
    ScanGitCollections,
    UpdateCollectionAuth,
    UpdateCollectionDocs,
    UpdateCollectionHeaders,
    UpdateCollectionClientCertificates,
    UpdateCollectionPresets,
    UpdateCollectionProtobuf,
    UpdateCollectionProxy,
    UpdateCollectionSecurityConfig,
    UpdateCollectionScripts,
    UpdateCollectionVariables,
    UpdateEnvironmentVariables,
    UpdateFolderSettings,
	    UpdateGlobalEnvironment,
	    UpdateGlobalEnvironmentVariables,
	    UpdateOpenTabPanes,
	    UpdateOpenAPISyncConfig,
	    UpdatePreferences,
	    UpdateRequest,
    WriteTerminalSession,
    UpdateResponseExample
  } from '../wailsjs/go/main/App'
  import type { main } from '../wailsjs/go/models'
  import { BrowserOpenURL, EventsOn, OnFileDrop, OnFileDropOff, Quit } from '../wailsjs/runtime/runtime'

  type View = 'request' | 'collection' | 'git' | 'runner' | 'environments' | 'import' | 'features' | 'network' | 'cookies' | 'preferences' | 'devtools'
  type ResponsePaneOrientation = 'horizontal' | 'vertical'
  type DevToolsTab = 'console' | 'network' | 'performance' | 'terminal'
  type DevToolsNetworkSortKey = 'method' | 'status' | 'domain' | 'path' | 'time' | 'duration' | 'size'
  type DevToolsNetworkSortDirection = '' | 'asc' | 'desc'
  type DevToolsNetworkDetailTab = 'request' | 'response' | 'network'
  type RequestPaneTab = 'params' | 'body' | 'headers' | 'auth' | 'vars' | 'script' | 'assert' | 'tests' | 'docs' | 'app' | 'settings'
  type ResponseTab = 'response' | 'headers' | 'metadata' | 'trailers' | 'timeline' | 'console' | 'tests' | 'examples'
  type CollectionTab = 'overview' | 'folders' | 'headers' | 'vars' | 'auth' | 'presets' | 'proxy' | 'clientCert' | 'protobuf' | 'script' | 'tests'
  type FolderSettingsTab = 'headers' | 'vars' | 'auth' | 'script' | 'tests' | 'docs'
  type EnvironmentVariableTab = 'variables' | 'secrets'
	  type ImportSourceMode = 'files' | 'url' | 'paste' | 'git'
	  type ImportDecision = {
	    selected: boolean
	    environments: string[]
	    folders: string[]
	    requests: string[]
	    outputName: string
	    kindOverride: string
	    conflictAction: string
	  }
  type OAuth2ParamBucket = 'authorizationAdditionalParams' | 'tokenAdditionalParams' | 'refreshAdditionalParams'
  type OAuth2ParamField = 'name' | 'value' | 'enabled'
  type OAuth2ParamSendIn = 'headers' | 'queryparams' | 'body'
  type DotEnvEditorMode = 'table' | 'raw'
  type NotificationTab = 'all' | 'unread'
  type ThemeMode = 'system' | 'light' | 'dark'
  type JSSandboxMode = 'safe' | 'developer'
  type TabLifecycleAction = 'close-active' | 'close-all' | 'quit'
  type TabLifecycleDialog = {
    action: TabLifecycleAction
    affected: UnsavedRequestTab[]
    targetTabIDs: string[]
    returnFocus: HTMLElement | null
  }
  type CollectionItemInfoTarget =
    | { kind: 'folder'; collection: main.Collection; folder: main.FolderConfig }
    | { kind: 'request'; collection: main.Collection; request: main.RequestItem }
  type RequestCodeTarget = {
    collectionId: string
    itemId: string
    environmentId: string
  }
  type ThemeVariant = {
    id: string
    name: string
    mode: 'light' | 'dark'
    preview: {
      background: string
      sidebar: string
      accent: string
    }
  }
  type KeyBindingOS = 'mac' | 'windows'
  type KeyBindingDefinition = {
    name: string
    mac?: string
    windows?: string
    readOnly?: boolean
    hidden?: boolean
    displayValue?: Partial<Record<KeyBindingOS, string>>
  }
  type KeyBindingSection = {
    heading: string
    bindings: Record<string, KeyBindingDefinition>
  }
  type BodyTextField = 'json' | 'xml' | 'text' | 'graphqlQuery' | 'graphqlVariables'
  type VariableTooltipSource = 'global' | 'collection' | 'environment' | 'folder' | 'request' | 'runtime' | 'process' | 'path' | 'missing' | 'invalid'
  type IndexedVariable = {
    variable: main.Variable
    index: number
  }
  type TooltipResolution = {
    value: string
    containsSecret: boolean
  }
  type VariableTooltipInfo = {
    name: string
    scope: string
    rawValue: string
    resolvedValue: string
    secret: boolean
    readOnly: boolean
    found: boolean
    editable: boolean
    validName: boolean
    source: VariableTooltipSource
    index: number
    environmentId?: string
    globalEnvironmentId?: string
  }
	  type URLVariableSegment =
	    | {
	      key: string
	      text: string
	      variable: false
	      prompt: false
	    }
	    | {
	      key: string
	      text: string
	      variable: false
	      prompt: true
	      name: string
	    }
	    | {
	      key: string
	      text: string
	      variable: true
	      prompt: false
	      path?: boolean
	      name: string
	      info: VariableTooltipInfo
	    }
  type GlobalSearchResult = {
    id: string
    type: 'collection' | 'folder' | 'request'
    collectionId: string
    itemId?: string
    name: string
    subtitle: string
    meta: string
    rank: number
  }
  type GitCloneProgress = {
    stage?: string
    message?: string
    targetPath?: string
    at?: string
  }
  type DotEnvRow = {
    lineIndex: number
    name: string
    value: string
  }
  type CookieForm = {
    id: string
    name: string
    value: string
    domain: string
    path: string
    expires: string
    session: boolean
    secure: boolean
    httpOnly: boolean
    sameSite: string
    hostOnly: boolean
  }
  type ScriptLog = {
    level: string
    message: string
    args?: string[]
  }
  type DevToolsConsoleLog = ScriptLog & {
    collectionName: string
    requestName: string
    source: string
  }
	  type PromptDialogState = {
	    prompts: string[]
	    values: Record<string, string>
	    resolve: (values: Record<string, string> | null) => void
	  }
	  type OAuth2AuthorizationBrowserRequest = {
	    authorizeUrl: string
	    callbackUrl: string
	    grantType: string
	  }

	  let state: main.AppState | null = null
  let activeView: View = 'request'
  let requestPaneTab: RequestPaneTab = 'params'
  let responseTab: ResponseTab = 'response'
  let responsePaneOrientation: ResponsePaneOrientation = 'horizontal'
  let compactWorkbench = false
  let compactWorkbenchMedia: MediaQueryList | undefined
  let removeCompactWorkbenchListener: (() => void) | undefined
  let removeFlushOnBlurListeners: (() => void) | undefined
  // US-021/US-022. Live WebSocket and gRPC events are pushed one at a time
  // rather than re-sent as a whole re-marshalled log on every call, so the
  // accumulated log lives here. Keyed by collection+request because several
  // requests can hold live sessions at once.
  let liveSessionLogs: Record<string, LiveSessionLog> = {}
  let stopWebSocketEvents: (() => void) | undefined
  let stopGrpcEvents: (() => void) | undefined
  let collectionTab: CollectionTab = 'overview'
  let responseView: 'pretty' | 'raw' | 'base64' | 'hex' = 'pretty'
  let tabLifecycleDialog: TabLifecycleDialog | null = null
  let tabLifecycleDecisionBusy = false
  let tabLifecycleCancelButton: HTMLButtonElement | null = null
  let recoveryEntries: main.RecoveryEntry[] = []
  let recoveryBusyEntryID = ''
  let workspaceWindowTargets: main.WorkspaceWindowTarget[] = []
  let workspaceWindowPickerOpen = false
  let workspaceWindowPickerBusy = false
  let workspaceWindowPickerBusyAction: 'loading' | 'opening' | 'creating' | '' = ''
  let workspaceWindowPickerError = ''
  let nativeWindowBusy = false
  let nativeWindowError = ''
  let hydratedActiveTabID = ''
  let selectedEnvironmentId = ''
  let loading = true
  let loadingStatus = 'Opening workspace'
  let busy = ''
  let activeActions = new Map<number, string>()
  let nextActionID = 0
  let error = ''
  let activeHTTPTransport: { collectionId: string; requestId: string } | undefined
  let httpCancellationRequested = false
  let activeCollectionRun: { collectionId: string; collectionName: string } | undefined
  let collectionRunCancellationRequested = false
  let lastCollectionRunCancelled = false
  let collectionName = 'New Collection'
  let requestName = 'New Request'
  let requestType = 'http'
  let lastPresetKey = ''
  let environmentName = 'Staging'
  let globalEnvironmentName = 'Global'
  let globalEnvironmentPayload = ''
  let globalEnvironmentCopyName = ''
  let globalEnvironmentExportFormat = 'single-object'
  let globalEnvironmentExportAll = false
  let globalEnvironmentExportFilename = ''
  let globalEnvironmentExportPath = ''
  let globalEnvironmentVariableTab: EnvironmentVariableTab = 'variables'
  let environmentVariableTab: EnvironmentVariableTab = 'variables'
  let globalEnvironmentVariableSearch = ''
  let environmentVariableSearch = ''
  let dotEnvFiles: main.DotEnvFile[] = []
  let selectedDotEnvKey = ''
  let dotEnvScope = 'workspace'
  let dotEnvName = '.env'
  let dotEnvContent = ''
  let dotEnvDirty = false
  let dotEnvContextKey = ''
  let dotEnvRefreshTimer: ReturnType<typeof window.setInterval> | undefined
  let collectionWatchPollTimer: ReturnType<typeof window.setInterval> | undefined
  let collectionWatchRefreshInFlight = false
  let dotEnvEditorMode: DotEnvEditorMode = 'table'
  let systemThemeMode: 'light' | 'dark' = 'light'
  let systemThemeQuery: MediaQueryList | undefined
  let removeSystemThemeListener: (() => void) | undefined
  let exportText = ''
	  let importSourceMode: ImportSourceMode = 'files'
	  let importSources: main.CollectionImportSource[] = []
	  let importPreview: main.CollectionImportPreview | undefined
	  let importDecisions: Record<string, ImportDecision> = {}
	  let importExpanded: Record<string, boolean> = {}
	  let importApplyResult: main.CollectionImportApplyResult | undefined
	  let importDestinationRoot = ''
	  let importDestinationWorkspaceID = ''
	  let importURL = ''
	  let importPasteName = 'Pasted import'
	  let importStatus = ''
	  let importPickerButton: HTMLButtonElement | null = null
	  let importReadyRows: main.CollectionImportPreviewRow[] = []
	  let importApplyButton: HTMLButtonElement | null = null
	  let importReplaceConfirmationOpen = false
	  let importReplaceConfirmationCancelButton: HTMLButtonElement | null = null
	  let importReplaceConfirmationReturnFocus: HTMLElement | null = null
	  let importApplyInFlight = false
  let openAPISyncCollectionId = ''
  let openAPISyncSourceURL = ''
  let openAPISyncGroupBy = 'tag'
  let openAPISyncContent = ''
  let openAPISyncPreserveValues = true
	  let openAPISyncEndpointDecisions: Record<string, string> = {}
	  let openAPISyncResult: main.OpenAPISyncResult | undefined
	  let openAPILocalDriftResult: main.OpenAPILocalDriftResult | undefined
	  let openAPISyncSettingsOpen = false
	  let openAPISyncSettingsSourceURL = ''
	  let openAPISyncSettingsAutoCheck = true
	  let openAPISyncSettingsInterval = 5
	  let openAPISyncAutoCheckLastRun: Record<string, number> = {}
		  let openAPISyncUpdateStatus: Record<string, main.OpenAPISyncUpdateCheckResult> = {}
		  let openAPISyncUpdateErrors: Record<string, string> = {}
		  let openAPISyncPollTimer: ReturnType<typeof window.setInterval> | undefined
			  let openAPISyncInitialPollTimer: ReturnType<typeof window.setTimeout> | undefined
			  let openAPISpecViewerOpen = false
			  let openAPISpecViewerResult: main.OpenAPISyncSpecViewResult | undefined
			  let openAPISpecDiffOpen = false
			  let openAPISpecDiffResult: main.OpenAPISyncSpecDiffResult | undefined
			  let openAPISpecDiffActiveChangeIndex = 0
			  let requestSearch = ''
  let requestSearchInput: HTMLInputElement | undefined
  let requestURLInput: HTMLInputElement | undefined
  let sidebarCollapsed = false
  let sidebarWidth = 312
  let responseSplit = 0.52
	let workbenchStorageScope = ''
  let creationOpen = false
  let creationReturnFocus: HTMLElement | null = null
  let commandPaletteOpen = false
  let commandPaletteQuery = ''
  let commandPaletteInput: HTMLInputElement | null = null
  let commandPaletteReturnFocus: HTMLElement | null = null
  let commandPaletteActiveIndex = 0
  let runnerConfigCollectionId = ''
  let runnerSelectedItemIds: string[] = []
  let runnerConfigItems: main.RequestItem[] = []
  let runnerSelectedCount = 0
  let runnerDelayMs = 0
  let appZoomPercentage = 100
  let codeFont = 'default'
  let codeFontSize = 13
  let fileCacheSize: number | undefined
  let autoSaveTimer: ReturnType<typeof window.setTimeout> | undefined
  let autoSaveTarget: { collectionId: string; requestId: string } | undefined
  let requestPreferencesSaveQueue = Promise.resolve()
  let recordingKeybindingAction = ''
  let keybindingDraft = ''
  let keybindingError = ''
  let cookieSearch = ''
  let cookieForm: CookieForm = emptyCookieForm()
  let rawCookieHeader = 'session=abc123; Path=/; HttpOnly'
  let rawCookieURL = 'http://127.0.0.1/'
  let selectedCollectionId = ''
  let selectedFolderPath = ''
  let folderSettingsTab: FolderSettingsTab = 'headers'
  let folderSettingDrafts: Record<string, main.FolderConfig> = {}
  let folderSettingsSaveQueue = Promise.resolve()
  let gitRemoteURL = ''
  let gitRemoteCollectionId = ''
  let gitCloneURL = ''
  let gitCloneRoot = ''
  let gitCloneName = ''
  let gitVersionText = ''
  let gitCloneOutput = ''
  let gitCloneInProgress = false
  let showShareCollectionModal = false
  let shareCollectionFormat = 'zip'
  let shareCollectionResult: main.CollectionExportResult | undefined
  let shareCollectionUnsupportedTypes: string[] = []
  let showGenerateDocsModal = false
  let renameCollectionTarget: main.Collection | undefined
  let renameCollectionDraft = ''
  let cloneCollectionTarget: main.Collection | undefined
  let cloneCollectionNameDraft = ''
  let cloneCollectionFolderDraft = ''
  let cloneCollectionLocationDraft = ''
  let cloneCollectionFolderEditing = false
  let newFolderTarget: main.Collection | undefined
  let newFolderParentPath = ''
  let newFolderNameDraft = ''
  let newFolderDirectoryDraft = ''
  let newFolderDirectoryEditing = false
  let newFolderShowFilesystemName = false
  let renameFolderTarget: { collection: main.Collection; folder: main.FolderConfig } | undefined
  let renameFolderNameDraft = ''
  let renameFolderDirectoryDraft = ''
  let renameFolderDirectoryEditing = false
  let renameFolderShowFilesystemName = false
  let cloneFolderTarget: { collection: main.Collection; folder: main.FolderConfig } | undefined
  let cloneFolderNameDraft = ''
  let cloneFolderDirectoryDraft = ''
  let cloneFolderDirectoryEditing = false
  let cloneFolderShowFilesystemName = false
  let itemInfoTarget: CollectionItemInfoTarget | undefined
  let cloneRequestTarget: { collection: main.Collection; request: main.RequestItem } | undefined
  let cloneRequestNameDraft = ''
  let cloneRequestFilenameDraft = ''
  let cloneRequestFilenameEditing = false
  let cloneRequestShowFilesystemName = false
  let renameRequestTarget: { collection: main.Collection; request: main.RequestItem } | undefined
  let renameRequestNameDraft = ''
  let renameRequestFilenameDraft = ''
  let renameRequestFilenameEditing = false
  let renameRequestShowFilesystemName = false
  let deleteRequestTarget: { collection: main.Collection; request: main.RequestItem } | undefined
  let deleteFolderTarget: { collection: main.Collection; folder: main.FolderConfig } | undefined
  let removeCollectionTarget: main.Collection | undefined
  let generateDocsDeselectedEnvIds: string[] = []
  let generateDocsResult: main.GenerateCollectionDocsResult | undefined
  let generateDocsSelectAllInput: HTMLInputElement | undefined
  let generateDocsEnvironments: main.Environment[] = []
  let generateDocsSelectedEnvIds: string[] = []
  let generateDocsSelectedCount = 0
  let generateDocsFolderCount = 0
  let generateDocsRequestCount = 0
  let gitCloneProgress: GitCloneProgress[] = []
  let gitNotFoundMessage = ''
	let gitWorkbenchSnapshot: main.CollectionGitSnapshot | undefined
	let gitWorkbenchCollectionID = ''
	let gitWorkbenchLoading = false
	let gitWorkbenchBusy = ''
	let gitWorkbenchStatus = ''
	let gitWorkbenchError = ''
	let gitWorkbenchSelectedPaths: string[] = []
	let gitWorkbenchDiff: main.CollectionGitDiff | undefined
	let gitWorkbenchBranch = ''
	let gitWorkbenchNewBranch = ''
	let gitWorkbenchCheckoutNewBranch = true
	let gitWorkbenchCommitMessage = ''
	let gitWorkbenchRemoteName = 'origin'
	let gitWorkbenchRemoteURL = ''
	let gitWorkbenchRemoteBranch = ''
	let gitWorkbenchSetUpstream = false
	let gitWorkbenchHeading: HTMLHeadingElement | null = null
  let gitCandidates: main.GitCollectionCandidate[] = []
  let selectedGitCollectionPaths: string[] = []
  let openCollectionPath = '/Users/mou/Documents/LiteAPI/Sample API Collection'
  let grpcMethods: main.GRPCMethodInfo[] = []
  let grpcMethodsRequestId = ''
  let grpcMethodMessage = ''
  let revealedVariableTooltips: Record<string, boolean> = {}
  let copiedVariableTooltips: Record<string, boolean> = {}
  let copiedVariableTooltipTimers: Record<string, ReturnType<typeof window.setTimeout>> = {}
  let activeVariableTooltip = ''
  let editingVariableTooltip = ''
  let variableTooltipDraft = ''
  let urlInputScrollLeft = 0
  let bodyTextScrollLeft = 0
  let bodyTextScrollTop = 0
  let processEnvTooltipKey = ''
	  let processEnvTooltipValues: Record<string, string> = {}
	  let promptDialog: PromptDialogState | null = null
	  let oauth2AuthorizationRequest: OAuth2AuthorizationBrowserRequest | null = null
	  let oauth2CallbackURLInput = ''
	  let oauth2CallbackMessage = ''
	  let oauth2FrameKey = 0
	  let creatingResponseExample = false
  let createResponseExampleName = ''
  let createResponseExampleDescription = ''
  let createResponseExampleInput: HTMLInputElement | null = null
  let editingResponseExampleID = ''
  let responseExampleNameDraft = ''
  let deletingResponseExampleID = ''
  let editingResponseExampleDetailsID = ''
  let generatingResponseExampleID = ''
  let responseExampleCodeLanguage = 'curl'
  let responseExampleGeneratedCode = ''
  let requestCodeTarget: RequestCodeTarget | undefined
  let requestCodeLanguage = 'curl'
  let requestGeneratedCode = ''
  let generatedGrpcurlCommand = ''
  let responseExampleDrafts: Record<string, main.ResponseExample> = {}
  let globalSearchOpen = false
  let globalSearchQuery = ''
  let globalSearchIndex = 0
  let globalSearchInput: HTMLInputElement | null = null
  let notificationsOpen = false
  let notificationTab: NotificationTab = 'all'
  let selectedNotificationID = ''
  let pinnedUnreadNotificationIDs: Set<string> | null = null
  let devToolsOpen = false
  let devToolsTab: DevToolsTab = 'console'
  let devToolsSnapshot: main.DevToolsSnapshot | null = null
  let devToolsPerformanceView = 'cumulative'
  let devToolsDrawerHeight = 320
  let devToolsDetailsPanelWidth = 400
  let devToolsNetworkFilters: Record<string, boolean> = {
    GET: true,
    POST: true,
    PUT: true,
    DELETE: true,
    PATCH: true,
    HEAD: true,
    OPTIONS: true
  }
  let devToolsNetworkSortKey: DevToolsNetworkSortKey | '' = ''
  let devToolsNetworkSortDirection: DevToolsNetworkSortDirection = ''
  let selectedDevToolsNetworkLogID = ''
  let devToolsNetworkDetailTab: DevToolsNetworkDetailTab = 'request'
  let devToolsNetworkColumnWidths = [80, 70, 180, 300, 110, 100, 80]
  let devToolsNetworkResizingColumn = -1
  let devToolsNetworkPreferencesKey = ''
  let terminalSessions: main.TerminalSession[] = []
  let terminalActiveSessionId = ''
  let terminalOutput = ''
  let terminalInput = ''
  let terminalError = ''
  let terminalBusy = false
  let terminalPollTimer: number | undefined
  let revealCollectionMessage = ''
	  let importContent = ''

  const requestTabs: { id: RequestPaneTab; label: string }[] = [
    { id: 'params', label: 'Params' },
    { id: 'body', label: 'Body' },
    { id: 'headers', label: 'Headers' },
    { id: 'auth', label: 'Auth' },
    { id: 'vars', label: 'Vars' },
    { id: 'script', label: 'Script' },
    { id: 'assert', label: 'Assert' },
    { id: 'tests', label: 'Tests' },
    { id: 'docs', label: 'Docs' },
    { id: 'app', label: 'App' },
    { id: 'settings', label: 'Settings' }
  ]

  const responseTabs: { id: ResponseTab; label: string }[] = [
    { id: 'response', label: 'Response' },
    { id: 'headers', label: 'Headers' },
    { id: 'metadata', label: 'Metadata' },
    { id: 'trailers', label: 'Trailers' },
    { id: 'timeline', label: 'Timeline' },
    { id: 'console', label: 'Console' },
    { id: 'tests', label: 'Tests' },
    { id: 'examples', label: 'Examples' }
  ]

  const devToolsTabs: { id: DevToolsTab; label: string }[] = [
    { id: 'console', label: 'Console' },
    { id: 'network', label: 'Network' },
    { id: 'performance', label: 'Performance' },
    { id: 'terminal', label: 'Terminal' }
  ]
  const devToolsNetworkMethods = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS']
  const devToolsNetworkColumns: { key: DevToolsNetworkSortKey; label: string }[] = [
    { key: 'method', label: 'Method' },
    { key: 'status', label: 'Status' },
    { key: 'domain', label: 'Domain' },
    { key: 'path', label: 'Path' },
    { key: 'time', label: 'Time' },
    { key: 'duration', label: 'Duration' },
    { key: 'size', label: 'Size' }
  ]
  const devToolsNetworkSortKeys: DevToolsNetworkSortKey[] = devToolsNetworkColumns.map((column) => column.key)
  const devToolsNetworkDetailTabs: { id: DevToolsNetworkDetailTab; label: string }[] = [
    { id: 'request', label: 'Request' },
    { id: 'response', label: 'Response' },
    { id: 'network', label: 'Network' }
  ]
  const responseExampleBodyTypes = ['json', 'text', 'xml', 'html', 'binary']

  const collectionTabs: { id: CollectionTab; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'folders', label: 'Folder Settings' },
    { id: 'headers', label: 'Headers' },
    { id: 'vars', label: 'Vars' },
    { id: 'auth', label: 'Auth' },
    { id: 'presets', label: 'Presets' },
    { id: 'proxy', label: 'Proxy' },
    { id: 'clientCert', label: 'Client Certificates' },
    { id: 'protobuf', label: 'Protobuf' },
    { id: 'script', label: 'Script' },
    { id: 'tests', label: 'Tests' }
  ]
  const folderSettingsTabs: { id: FolderSettingsTab; label: string }[] = [
    { id: 'headers', label: 'Headers' },
    { id: 'script', label: 'Script' },
    { id: 'tests', label: 'Test' },
    { id: 'vars', label: 'Vars' },
    { id: 'auth', label: 'Auth' },
    { id: 'docs', label: 'Docs' }
  ]
  const environmentVariableTabs: { id: EnvironmentVariableTab; label: string }[] = [
    { id: 'variables', label: 'Variables' },
    { id: 'secrets', label: 'Secrets' }
  ]

  const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'TRACE']
  const bodyModes = ['none', 'json', 'text', 'xml', 'formUrlEncoded', 'multipartForm', 'file', 'graphql']
  const authModes = ['none', 'inherit', 'basic', 'bearer', 'apikey', 'oauth2', 'awsv4', 'digest', 'ntlm', 'oauth1', 'wsse']
  const oauth2GrantTypes = ['client_credentials', 'password', 'authorization_code', 'implicit']
  const oauth2CredentialPlacements = ['basic_auth_header', 'body']
  const oauth2TokenPlacements = ['header', 'url']
  const oauth2TokenSources = ['access_token', 'id_token']
  const oauth1SignatureMethods = ['HMAC-SHA1', 'HMAC-SHA256', 'HMAC-SHA512', 'RSA-SHA1', 'RSA-SHA256', 'RSA-SHA512', 'PLAINTEXT']
  const oauth1Placements = ['header', 'query', 'body']
  const invalidCollectionFolderCharacters = /[<>:"/\\|?*\x00-\x1F]/g
  const reservedCollectionFolderNames = /^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$/i
  const collectionFolderFirstCharacter = /^[^\s\-<>:"/\\|?*\x00-\x1F]/
  const collectionFolderMiddleCharacters = /^[^<>:"/\\|?*\x00-\x1F]*$/
  const collectionFolderLastCharacter = /[^.\s<>:"/\\|?*\x00-\x1F]$/

  function sanitizeCollectionFolderName(name: string) {
    return name
      .replace(invalidCollectionFolderCharacters, '-')
      .replace(/^[\s-]+/, '')
      .replace(/[.\s]+$/, '')
  }

  function collectionFolderNameIsValid(name: string) {
    if (!name || name.length > 255) return false
    if (reservedCollectionFolderNames.test(name)) return false
    return collectionFolderFirstCharacter.test(name) && collectionFolderMiddleCharacters.test(name) && collectionFolderLastCharacter.test(name)
  }

  function cloneCollectionDefaultLocation() {
    const preferred = state?.preferences?.general?.defaultLocation || state?.preferences?.defaultCollectionPath || ''
    return preferred || activeWorkspace?.path || ''
  }

  function oauth2AuthWithDefaults(auth: main.OAuth2Auth | undefined, updates: Partial<main.OAuth2Auth> = {}) {
    const merged = { ...(auth ?? {}), ...updates } as main.OAuth2Auth
    return {
      ...merged,
      grantType: merged.grantType || 'client_credentials',
      credentialsPlacement: merged.credentialsPlacement || 'basic_auth_header',
      tokenSource: merged.tokenSource || 'access_token',
      tokenPlacement: merged.tokenPlacement || 'header',
      tokenHeaderPrefix: merged.tokenHeaderPrefix || 'Bearer',
      tokenQueryKey: merged.tokenQueryKey || 'access_token'
    } as main.OAuth2Auth
  }

  function authWithOAuth2Defaults(auth: main.AuthConfig | undefined, updates: Partial<main.AuthConfig> = {}) {
    const next = { ...(auth ?? {}), ...updates } as main.AuthConfig
    if (next.mode === 'oauth2' || updates.oauth2 !== undefined) {
      next.oauth2 = oauth2AuthWithDefaults(auth?.oauth2, updates.oauth2)
    }
    return next
  }

  const grpcMethodTypes = ['', 'unary', 'client-streaming', 'server-streaming', 'bidi-streaming']
  const wsMessageTypes = ['json', 'xml', 'text', 'binary']
  const themeModes: { id: ThemeMode; label: string }[] = [
    { id: 'light', label: 'Light' },
    { id: 'dark', label: 'Dark' },
    { id: 'system', label: 'System' }
  ]
  const lightThemeVariants: ThemeVariant[] = [
    { id: 'light', name: 'Light', mode: 'light', preview: { background: '#f7f9fb', sidebar: '#19232e', accent: '#1f7a5a' } },
    { id: 'light-monochrome', name: 'Light Monochrome', mode: 'light', preview: { background: '#f4f5f6', sidebar: '#24272c', accent: '#64748b' } },
    { id: 'light-pastel', name: 'Light Pastel', mode: 'light', preview: { background: '#fff7ed', sidebar: '#26354f', accent: '#c08497' } },
    { id: 'catppuccin-latte', name: 'Catppuccin Latte', mode: 'light', preview: { background: '#eff1f5', sidebar: '#e6e9ef', accent: '#8839ef' } },
    { id: 'vscode-light', name: 'VS Code Light', mode: 'light', preview: { background: '#ffffff', sidebar: '#f3f3f3', accent: '#007acc' } }
  ]
  const darkThemeVariants: ThemeVariant[] = [
    { id: 'dark', name: 'Dark', mode: 'dark', preview: { background: '#111820', sidebar: '#0f1720', accent: '#4da37f' } },
    { id: 'dark-monochrome', name: 'Dark Monochrome', mode: 'dark', preview: { background: '#18181b', sidebar: '#111113', accent: '#a1a1aa' } },
    { id: 'dark-pastel', name: 'Dark Pastel', mode: 'dark', preview: { background: '#1f1d2b', sidebar: '#191724', accent: '#ebbcba' } },
    { id: 'catppuccin-frappe', name: 'Catppuccin Frappe', mode: 'dark', preview: { background: '#303446', sidebar: '#292c3c', accent: '#ca9ee6' } },
    { id: 'catppuccin-macchiato', name: 'Catppuccin Macchiato', mode: 'dark', preview: { background: '#24273a', sidebar: '#1e2030', accent: '#c6a0f6' } },
    { id: 'catppuccin-mocha', name: 'Catppuccin Mocha', mode: 'dark', preview: { background: '#1e1e2e', sidebar: '#181825', accent: '#cba6f7' } },
    { id: 'nord', name: 'Nord', mode: 'dark', preview: { background: '#2e3440', sidebar: '#242933', accent: '#88c0d0' } },
    { id: 'vscode-dark', name: 'VS Code Dark', mode: 'dark', preview: { background: '#1e1e1e', sidebar: '#252526', accent: '#3794ff' } }
  ]
	  const keyBindingSeparator = '+bind+'
	  const zoomDefaultPercentage = 100
	  const openAPISyncCheckIntervals = [5, 15, 30, 60]
	  const collectionWatchPollMs = 2_000
	  const openAPISyncInitialPollMs = 10_000
	  const openAPISyncGlobalPollMs = 5 * 60_000
	  const zoomMinPercentage = 50
  const zoomMaxPercentage = 150
  const zoomStepPercentage = 10
  const defaultCodeFont = 'default'
  const defaultCodeFontSize = 13
  const defaultCodeFontFamily = '"SFMono-Regular", Consolas, "Liberation Mono", monospace'
  const zoomPercentages = Array.from(
    { length: (zoomMaxPercentage - zoomMinPercentage) / zoomStepPercentage + 1 },
    (_, index) => zoomMinPercentage + index * zoomStepPercentage
  )
  const keyBindingSections: KeyBindingSection[] = [
    {
      heading: 'Tabs',
      bindings: {
        closeTab: { mac: 'command+bind+w', windows: 'ctrl+bind+w', name: 'Close Tab' },
        closeAllTabs: { mac: 'command+bind+shift+bind+w', windows: 'ctrl+bind+shift+bind+w', name: 'Close All Tabs' },
        save: { mac: 'command+bind+s', windows: 'ctrl+bind+s', name: 'Save' },
        saveAllTabs: { mac: 'command+bind+shift+bind+s', windows: 'ctrl+bind+shift+bind+s', name: 'Save All Tabs' },
        reopenLastClosedTab: { mac: 'command+bind+shift+bind+t', windows: 'ctrl+bind+shift+bind+t', name: 'Reopen Last Closed Tab' },
        switchToTabAtPosition: {
          mac: 'command+bind+1+bind+command+bind+8',
          windows: 'ctrl+bind+1+bind+ctrl+bind+8',
          name: 'Switch to Tab at Position',
          readOnly: true,
          displayValue: { mac: 'command+bind+1 - command+bind+8', windows: 'ctrl+bind+1 - ctrl+bind+8' }
        },
        switchToLastTab: { mac: 'command+bind+9', windows: 'ctrl+bind+9', name: 'Switch to Last Tab' },
        switchToPreviousTab: { mac: 'shift+bind+command+bind+[', windows: 'shift+bind+ctrl+bind+[', name: 'Switch to Previous Tab' },
        switchToNextTab: { mac: 'shift+bind+command+bind+]', windows: 'shift+bind+ctrl+bind+]', name: 'Switch to Next Tab' },
        moveTabLeft: { mac: 'command+bind+[', windows: 'ctrl+bind+[', name: 'Move Tab Left' },
        moveTabRight: { mac: 'command+bind+]', windows: 'ctrl+bind+]', name: 'Move Tab Right' },
        switchToTab1: { mac: 'command+bind+1', windows: 'ctrl+bind+1', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab2: { mac: 'command+bind+2', windows: 'ctrl+bind+2', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab3: { mac: 'command+bind+3', windows: 'ctrl+bind+3', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab4: { mac: 'command+bind+4', windows: 'ctrl+bind+4', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab5: { mac: 'command+bind+5', windows: 'ctrl+bind+5', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab6: { mac: 'command+bind+6', windows: 'ctrl+bind+6', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab7: { mac: 'command+bind+7', windows: 'ctrl+bind+7', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
        switchToTab8: { mac: 'command+bind+8', windows: 'ctrl+bind+8', name: 'Switch to Tab at Position', readOnly: true, hidden: true }
      }
    },
    {
      heading: 'Sidebar',
      bindings: {
        sidebarSearch: { mac: 'command+bind+f', windows: 'ctrl+bind+f', name: 'Search Sidebar' },
        copyItem: { mac: 'command+bind+c', windows: 'ctrl+bind+c', name: 'Copy Item' },
        pasteItem: { mac: 'command+bind+v', windows: 'ctrl+bind+v', name: 'Paste Item' },
        cloneItem: { mac: 'command+bind+d', windows: 'ctrl+bind+d', name: 'Clone Item' },
        renameItem: { mac: 'command+bind+r', windows: 'ctrl+bind+r', name: 'Rename Item' },
        collapseSidebar: { mac: 'command+bind+\\', windows: 'ctrl+bind+\\', name: 'Collapse Sidebar' }
      }
    },
    {
      heading: 'Requests',
      bindings: {
        sendRequest: { mac: 'command+bind+enter', windows: 'ctrl+bind+enter', name: 'Send Request' },
        changeLayout: { mac: 'command+bind+j', windows: 'ctrl+bind+j', name: 'Change Orientation' }
      }
    },
    {
      heading: 'Collections & Environment',
      bindings: {
        importCollection: { mac: 'command+bind+o', windows: 'ctrl+bind+o', name: 'Import Collection' },
        editEnvironment: { mac: 'command+bind+e', windows: 'ctrl+bind+e', name: 'Edit Environment' },
        newRequest: { mac: 'command+bind+n', windows: 'ctrl+bind+n', name: 'New Request' }
      }
    },
    { heading: 'Search', bindings: { globalSearch: { mac: 'command+bind+k', windows: 'ctrl+bind+k', name: 'Global Search' } } },
    {
      heading: 'View',
      bindings: {
        zoomIn: { mac: 'command+bind+=', windows: 'ctrl+bind+=', name: 'Zoom In' },
        zoomOut: { mac: 'command+bind+-', windows: 'ctrl+bind+-', name: 'Zoom Out' },
        resetZoom: { mac: 'command+bind+0', windows: 'ctrl+bind+0', name: 'Reset Zoom' }
      }
    },
    { heading: 'Developer Tool', bindings: { openTerminal: { mac: 'command+bind+t', windows: 'ctrl+bind+t', name: 'Open in Terminal' } } },
    {
      heading: 'Others',
      bindings: {
        openPreferences: { mac: 'command+bind+,', windows: 'ctrl+bind+,', name: 'Open Preferences' },
        closeBruno: { mac: 'command+bind+q', windows: 'ctrl+bind+shift+bind+q', name: 'Close LiteAPI' }
      }
    }
  ]
	  const promptTokenPattern = /\{\{\?([^{}\s](?:[^{}]*?[^{}\s])?)\}\}/g
	  const promptVariableTextPattern = /^\?([^{}\s](?:[^{}]*?[^{}\s])?)$/
	  const invalidVariableWarning = 'Invalid variable name! Variables must only contain alpha-numeric characters, "-", "_", "."'

  function normalizePresetRequestType(value: string | undefined) {
    if (value === 'ws') return 'websocket'
    if (value === 'http' || value === 'graphql' || value === 'grpc' || value === 'websocket') return value
    return ''
  }

  function normalizedResponsePaneOrientation(value: string | undefined): ResponsePaneOrientation {
    return value === 'vertical' ? 'vertical' : 'horizontal'
  }

  function normalizedZoomPercentage(value: number | undefined) {
    const numeric = Number.isFinite(value) ? Number(value) : zoomDefaultPercentage
    return Math.min(Math.max(numeric || zoomDefaultPercentage, zoomMinPercentage), zoomMaxPercentage)
  }

  function normalizedCodeFont(value: string | undefined) {
    const trimmed = value?.trim()
    return trimmed || defaultCodeFont
  }

  function normalizedCodeFontSize(value: number | undefined) {
    if (value === undefined || value === null || !Number.isFinite(Number(value)) || Number(value) === 0) {
      return defaultCodeFontSize
    }
    return Math.min(Math.max(Number(value), 1), 32)
  }

  function normalizedAutoSaveInterval(value: number | undefined) {
    if (value === undefined || value === null || !Number.isFinite(Number(value)) || Number(value) <= 0) {
      return 1000
    }
    return Math.max(Math.round(Number(value)), 500)
  }

  function normalizedRequestTimeout(value: number | undefined) {
    if (value === undefined || value === null || !Number.isFinite(Number(value)) || Number(value) < 0) {
      return 0
    }
    return Math.round(Number(value))
  }

  function customCaFileName(filePath: string | undefined) {
    const trimmed = filePath?.trim() ?? ''
    if (!trimmed) return ''
    const parts = trimmed.split(/[\\/]/)
    return parts[parts.length - 1] || trimmed
  }

  function codeFontFamilyFor(value: string) {
    const font = normalizedCodeFont(value)
    if (font === defaultCodeFont) return defaultCodeFontFamily
    const escaped = font.replace(/["\\]/g, '\\$&')
    return `"${escaped}", ${defaultCodeFontFamily}`
  }

  $: activeWorkspace = state?.workspaces?.find((workspace) => workspace.id === state?.activeWorkspaceId) ?? state?.workspaces?.[0]
  $: activeTab = state?.openTabs?.find((tab) => tab.id === state?.activeTabId)
  $: if (activeTab?.id && activeTab.id !== hydratedActiveTabID) {
    hydratedActiveTabID = activeTab.id
    if (requestTabs.some((tab) => tab.id === activeTab.requestPaneTab)) requestPaneTab = activeTab.requestPaneTab as RequestPaneTab
    if (activeTab.kind === 'response-example') {
      responseTab = 'examples'
    } else if (responseTabs.some((tab) => tab.id === activeTab.responseTab)) {
      responseTab = activeTab.responseTab as ResponseTab
    }
  }
  $: selectedCollection = activeWorkspace?.collections?.find((collection) => collection.id === selectedCollectionId)
  $: activeCollection = selectedCollection ?? activeWorkspace?.collections?.find((collection) => collection.id === activeTab?.collectionId) ?? activeWorkspace?.collections?.[0]
	$: if (activeView === 'git' && activeCollection?.id && gitWorkbenchCollectionID !== activeCollection.id && !gitWorkbenchLoading) {
		gitWorkbenchSnapshot = undefined
		gitWorkbenchSelectedPaths = []
		gitWorkbenchDiff = undefined
		gitWorkbenchRemoteURL = ''
		void refreshGitWorkbench()
	}
  $: activeRequest = activeCollection?.items?.find((item) => item.id === activeTab?.itemId) ?? activeCollection?.items?.[0]
  $: shareCollectionUnsupportedTypes = collectionShareUnsupportedTypes(activeCollection)
  $: if ((activeCollection?.id ?? '') !== openAPISyncCollectionId) {
    openAPISyncCollectionId = activeCollection?.id ?? ''
    const config = activeCollection?.openapi?.[0]
    openAPISyncSourceURL = config?.sourceUrl ?? ''
    openAPISyncGroupBy = config?.groupBy || 'tag'
    openAPISyncContent = ''
    openAPISyncPreserveValues = true
	    openAPISyncEndpointDecisions = {}
		    openAPISyncResult = undefined
		    openAPILocalDriftResult = undefined
		    openAPISyncSettingsOpen = false
		    openAPISpecViewerOpen = false
		    openAPISpecViewerResult = undefined
		    openAPISpecDiffOpen = false
		    openAPISpecDiffResult = undefined
		    openAPISpecDiffActiveChangeIndex = 0
		  }
  $: openAPISpecDiffChangeLineIndexes = (openAPISpecDiffResult?.lines ?? []).reduce<number[]>((indexes, line, index) => {
    if ((line.kind ?? 'same') !== 'same') indexes.push(index)
    return indexes
  }, [])
  $: openAPISpecDiffChangeCount = openAPISpecDiffChangeLineIndexes.length
  $: if (openAPISpecDiffChangeCount === 0 && openAPISpecDiffActiveChangeIndex !== 0) {
    openAPISpecDiffActiveChangeIndex = 0
  } else if (openAPISpecDiffChangeCount > 0 && openAPISpecDiffActiveChangeIndex >= openAPISpecDiffChangeCount) {
    openAPISpecDiffActiveChangeIndex = openAPISpecDiffChangeCount - 1
  }
  $: generateDocsEnvironments = activeCollection?.environments ?? []
  $: generateDocsDeselectedEnvIds = generateDocsDeselectedEnvIds.filter((id) => generateDocsEnvironments.some((env) => env.id === id))
  $: generateDocsSelectedEnvIds = generateDocsEnvironments.filter((env) => !generateDocsDeselectedEnvIds.includes(env.id)).map((env) => env.id)
  $: generateDocsSelectedCount = generateDocsSelectedEnvIds.length
  $: generateDocsFolderCount = activeCollection?.folders?.length ?? 0
  $: generateDocsRequestCount = (activeCollection?.items ?? []).filter(collectionDocsRequestIsExportable).length
  $: if (generateDocsSelectAllInput) generateDocsSelectAllInput.indeterminate = generateDocsSelectedCount > 0 && generateDocsSelectedCount < generateDocsEnvironments.length
  $: runnerConfigItems = runnerSelectableItems(activeCollection)
  $: runnerSelectedCount = runnerSelectedItemIds.filter((id) => runnerConfigItems.some((item) => item.id === id)).length
  $: if ((activeCollection?.id ?? '') !== runnerConfigCollectionId) {
    runnerConfigCollectionId = activeCollection?.id ?? ''
    runnerSelectedItemIds = runnerConfigItems.map((item) => item.id)
    runnerDelayMs = 0
  }
  $: responsePaneOrientation = normalizedResponsePaneOrientation(state?.preferences?.layout?.responsePaneOrientation)
  $: appZoomPercentage = normalizedZoomPercentage(state?.preferences?.display?.zoomPercentage)
  $: codeFont = normalizedCodeFont(state?.preferences?.font?.codeFont)
  $: codeFontSize = normalizedCodeFontSize(state?.preferences?.font?.codeFontSize ?? state?.preferences?.codeFontSize)
  $: activeResponseTabs = activeRequest?.type === 'grpc' ? responseTabs : responseTabs.filter((tab) => tab.id !== 'metadata' && tab.id !== 'trailers')
  $: if (activeRequest?.type !== 'grpc' && (responseTab === 'metadata' || responseTab === 'trailers')) responseTab = 'headers'
  $: if (activeCollection && selectedFolderPath && !(activeCollection.folders ?? []).some((folder) => folder.path === selectedFolderPath)) {
    selectedFolderPath = ''
  }
  $: activeFolder = activeCollection?.folders?.find((folder) => folder.path === selectedFolderPath) ?? activeCollection?.folders?.[0]
  $: editableFolder = activeFolder ? folderSettingDrafts[activeFolder.path] ?? activeFolder : undefined
  $: selectedEnvironment = activeCollection?.environments?.find((env) => env.id === selectedEnvironmentId) ?? activeCollection?.environments?.[0]
  $: activeGlobalEnvironment = activeWorkspace?.globalEnvironments?.find((env) => env.id === activeWorkspace?.activeGlobalEnvironmentId)
  $: selectedGlobalEnvironment = activeGlobalEnvironment ?? activeWorkspace?.globalEnvironments?.[0]
  $: selectedDotEnvFile = dotEnvFiles.find((file) => dotEnvFileKey(file) === selectedDotEnvKey)
  $: globalEnvironmentVariableQuery = normalizedSearch(globalEnvironmentVariableSearch)
  $: environmentVariableQuery = normalizedSearch(environmentVariableSearch)
  $: visibleGlobalEnvironmentRows = visibleEnvironmentVariables(selectedGlobalEnvironment?.variables, globalEnvironmentVariableTab, globalEnvironmentVariableQuery)
  $: visibleEnvironmentRows = visibleEnvironmentVariables(selectedEnvironment?.variables, environmentVariableTab, environmentVariableQuery)
  $: dotEnvRows = parseDotEnvRows(dotEnvContent)
  $: doneFeatures = state?.featureLedger?.filter((feature) => feature.status === 'done').length ?? 0
  $: partialFeatures = state?.featureLedger?.filter((feature) => feature.status === 'partial').length ?? 0
  $: totalFeatures = state?.featureLedger?.length ?? 0
  $: websocketConnected = activeRequest?.type === 'websocket' && responseHeaderValue(activeRequest.response, 'x-websocket-connected') === 'true'
  $: websocketEventCount = responseHeaderValue(activeRequest?.response, 'x-websocket-events')
  $: grpcStreamConnected = activeRequest?.type === 'grpc' && responseHeaderValue(activeRequest.response, 'x-grpc-stream-connected') === 'true'
  $: grpcStreamEnded = activeRequest?.type === 'grpc' && responseHeaderValue(activeRequest.response, 'x-grpc-stream-ended') === 'true'
  $: grpcStreamEventCount = responseHeaderValue(activeRequest?.response, 'x-grpc-stream-events')
  $: grpcStreamingRequest = activeRequest?.type === 'grpc' && ['client-streaming', 'server-streaming', 'bidi-streaming'].includes(activeRequest.grpcMethodType ?? '')
  $: grpcClientStreamingRequest = activeRequest?.type === 'grpc' && ['client-streaming', 'bidi-streaming'].includes(activeRequest.grpcMethodType ?? '')
  $: httpTransportInFlight = Boolean(
    activeHTTPTransport
      && activeHTTPTransport.collectionId === activeCollection?.id
      && activeHTTPTransport.requestId === activeRequest?.id
  )
  $: hasActiveHTTPTransport = Boolean(activeHTTPTransport)
  $: backgroundHTTPTransport = activeHTTPTransport && !httpTransportInFlight
    ? {
      requestName: requestNameForTransport(activeHTTPTransport),
      pending: httpCancellationRequested
    }
    : undefined
  $: runnerCancelledCount = runnerCancellationCount(state?.runner)
  $: runnerCompletedCancelled = runnerCancelledCount > 0 || lastCollectionRunCancelled
  $: requestCommand = requestCommandState(
    activeRequest,
    activeCollection,
    selectedEnvironment?.name,
    busy,
    websocketConnected,
    grpcStreamConnected,
    state?.preferences,
    httpTransportInFlight,
    httpTransportInFlight && httpCancellationRequested,
    backgroundHTTPTransport
  )
  $: activeScriptLogs = responseScriptLogs(activeRequest?.response)
  $: activeTimelineEntries = sortedTimelineEntries(activeRequest?.timeline ?? [])
  // US-021/US-022. Both operands are named inside the statement so this really
  // does re-run — a `$:` that referenced only a helper function would track
  // nothing and go stale, which is the ResponseInspector bug US-004 found.
  $: activeLiveSessionLog =
    activeCollection && activeRequest
      ? liveSessionLogs[liveSessionKey(activeCollection.id, activeRequest.id)]
      : undefined
  $: devToolsConsoleRows = devToolsConsoleLogs(activeWorkspace)
  $: rawDevToolsNetworkRows = state?.networkLog ?? []
  $: if (state && devToolsNetworkPreferencesKeyFor(state.preferences?.devTools?.network) !== devToolsNetworkPreferencesKey) {
    applyDevToolsNetworkPreferences(state.preferences?.devTools?.network)
  }
  $: devToolsNetworkMethodCounts = Object.fromEntries(devToolsNetworkMethods.map((method) => [method, rawDevToolsNetworkRows.filter((row) => normalizedNetworkMethod(row) === method).length])) as Record<string, number>
  $: devToolsNetworkActiveFilterCount = devToolsNetworkMethods.filter((method) => devToolsNetworkFilters[method]).length
  $: devToolsNetworkRows = sortedDevToolsNetworkRows(filteredDevToolsNetworkRows(rawDevToolsNetworkRows, devToolsNetworkFilters), devToolsNetworkSortKey, devToolsNetworkSortDirection)
  $: devToolsNetworkSortLabels = Object.fromEntries(devToolsNetworkSortKeys.map((key) => [key, devToolsNetworkSortLabel(key, devToolsNetworkSortKey, devToolsNetworkSortDirection)])) as Record<DevToolsNetworkSortKey, string>
  $: devToolsNetworkAriaSort = Object.fromEntries(devToolsNetworkSortKeys.map((key) => [key, devToolsNetworkSortAriaValue(key, devToolsNetworkSortKey, devToolsNetworkSortDirection)])) as Record<DevToolsNetworkSortKey, 'ascending' | 'descending' | 'none'>
  $: devToolsNetworkTableWidth = devToolsNetworkColumnWidths.reduce((total, width) => total + width, 0)
  $: selectedDevToolsNetworkRow = devToolsNetworkRows.find((row) => row.id === selectedDevToolsNetworkLogID) ?? devToolsNetworkRows[0]
  $: activeTerminalSession = terminalSessions.find((session) => session.id === terminalActiveSessionId)
  $: devToolsPerformanceProcesses = devToolsSnapshot?.processes ?? []
  $: if (devToolsPerformanceView !== 'cumulative' && !devToolsPerformanceProcesses.some((process) => String(process.pid) === devToolsPerformanceView)) devToolsPerformanceView = 'cumulative'
  $: selectedDevToolsPerformanceProcess = devToolsPerformanceProcesses.find((process) => String(process.pid) === devToolsPerformanceView)
  $: displayedDevToolsCPUPercent = selectedDevToolsPerformanceProcess?.cpuPercent ?? devToolsSnapshot?.cpuPercent
  $: displayedDevToolsMemoryBytes = selectedDevToolsPerformanceProcess?.memoryBytes ?? devToolsSnapshot?.memoryBytes
  $: displayedDevToolsUptimeSeconds = selectedDevToolsPerformanceProcess?.uptimeSeconds ?? devToolsSnapshot?.uptimeSeconds
  $: displayedDevToolsPID = selectedDevToolsPerformanceProcess?.pid ?? devToolsSnapshot?.pid
  $: if (devToolsNetworkRows.length > 0 && (!selectedDevToolsNetworkLogID || !devToolsNetworkRows.some((row) => row.id === selectedDevToolsNetworkLogID))) {
    selectedDevToolsNetworkLogID = devToolsNetworkRows[0].id
    devToolsNetworkDetailTab = 'request'
  }
  $: if (devToolsNetworkRows.length === 0 && selectedDevToolsNetworkLogID) selectedDevToolsNetworkLogID = ''
  $: requestVariableNames = activeRequest ? variableNamesForRequest(activeRequest) : []
  $: requestProcessEnvNames = requestVariableNames.filter((name) => name.startsWith('process.env.'))
  $: requestVariableTooltips = activeWorkspace && activeCollection && activeRequest
    ? variableTooltipsForRequest(activeWorkspace, activeCollection, activeRequest, selectedEnvironmentId, processEnvTooltipValues)
    : []
  $: searchQuery = normalizedSearch(requestSearch)
  $: globalSearchResults = buildGlobalSearchResults(activeWorkspace, globalSearchQuery)
  $: visibleNotifications = notificationsForDisplay(state?.notifications ?? [])
  $: unreadNotificationCount = visibleNotifications.filter((notification) => !notification.read).length
  $: listedNotifications = notificationTab === 'unread'
    ? pinnedUnreadNotificationIDs
      ? visibleNotifications.filter((notification) => pinnedUnreadNotificationIDs?.has(notification.id))
      : visibleNotifications.filter((notification) => !notification.read)
    : visibleNotifications
  $: selectedNotification = listedNotifications.find((notification) => notification.id === selectedNotificationID) ?? listedNotifications[0]
  $: visibleSidebarCollections = sidebarCollections(activeWorkspace, searchQuery)
  $: sidebarSearchCount = sidebarRequestCount(activeWorkspace, searchQuery)
  $: cookieSearchQuery = normalizedSearch(cookieSearch)
  $: visibleCookieGroups = cookieGroups(state?.cookies ?? [], cookieSearchQuery)
  $: selectedThemeMode = normalizedThemeMode(state?.preferences?.theme)
  $: displayedThemeMode = selectedThemeMode === 'system' ? systemThemeMode : selectedThemeMode
  $: selectedThemeVariant = displayedThemeMode === 'dark'
    ? normalizedThemeVariant(state?.preferences?.themeVariantDark, darkThemeVariants)
    : normalizedThemeVariant(state?.preferences?.themeVariantLight, lightThemeVariants)
  $: applyThemeToDocument(displayedThemeMode, selectedThemeVariant)
  $: applyZoomToDocument(appZoomPercentage)
  $: applyCodeFontToDocument(codeFont, codeFontSize)
  $: if (globalSearchIndex >= globalSearchResults.length) {
    globalSearchIndex = Math.max(0, globalSearchResults.length - 1)
  }
  $: {
    const presetKey = activeCollection ? `${activeCollection.id}:${activeCollection.presets?.requestType ?? ''}` : ''
    if (presetKey !== lastPresetKey) {
      lastPresetKey = presetKey
      requestType = normalizePresetRequestType(activeCollection?.presets?.requestType) || 'http'
    }
  }
  $: visibleCookieCount = visibleCookieGroups.reduce((total, group) => total + group.cookies.length, 0)
  $: if ((activeCollection?.id ?? '') !== gitRemoteCollectionId) {
    gitRemoteCollectionId = activeCollection?.id ?? ''
    gitRemoteURL = activeCollection?.remote ?? ''
  }
  $: if (activeWorkspace?.path && !gitCloneRoot) gitCloneRoot = activeWorkspace.path
  $: if ((activeRequest?.id ?? '') !== grpcMethodsRequestId) {
    grpcMethodsRequestId = activeRequest?.id ?? ''
    grpcMethods = []
    grpcMethodMessage = ''
  }
  $: {
    const nextDotEnvContextKey = activeView === 'environments' && activeWorkspace ? `${activeWorkspace.id}:${activeCollection?.id ?? ''}` : ''
    if (nextDotEnvContextKey && nextDotEnvContextKey !== dotEnvContextKey) {
      stopDotEnvRefresh()
      dotEnvContextKey = nextDotEnvContextKey
      void loadDotEnvFiles()
      dotEnvRefreshTimer = window.setInterval(() => {
        void loadDotEnvFiles()
      }, 2000)
    } else if (!nextDotEnvContextKey && dotEnvContextKey) {
      dotEnvContextKey = ''
      stopDotEnvRefresh()
    }
  }
  $: {
    const nextProcessEnvTooltipKey = activeCollection && requestProcessEnvNames.length > 0
      ? `${activeCollection.id}:${[...requestProcessEnvNames].sort().join('|')}`
      : ''
    if (nextProcessEnvTooltipKey !== processEnvTooltipKey) {
      processEnvTooltipKey = nextProcessEnvTooltipKey
      if (activeCollection && requestProcessEnvNames.length > 0) {
        void loadProcessEnvTooltipValues(activeCollection.id, requestProcessEnvNames, nextProcessEnvTooltipKey)
      } else {
        processEnvTooltipValues = {}
      }
    }
  }
  $: {
    if (devToolsOpen && devToolsTab === 'terminal') {
      startTerminalPolling()
    } else {
      stopTerminalPolling()
    }
  }

	  let stopGitCloneProgress: (() => void) | undefined
	  let stopOAuth2Authorize: (() => void) | undefined
	  let stopNativeMenuCommands: (() => void) | undefined

	  onMount(() => {
	    compactWorkbenchMedia = window.matchMedia('(max-width: 960px)')
	    const updateCompactWorkbench = () => {
	      compactWorkbench = compactWorkbenchMedia?.matches ?? false
	      if (compactWorkbench) sidebarCollapsed = true
	    }
	    updateCompactWorkbench()
	    compactWorkbenchMedia.addEventListener('change', updateCompactWorkbench)
	    removeCompactWorkbenchListener = () => compactWorkbenchMedia?.removeEventListener('change', updateCompactWorkbench)
    // US-012 wanted a force-flush on window blur, and the Go side has had
    // FlushPendingWrites all along — but the Wails binding for it was never
    // regenerated, so the frontend half was silently missing. Persistence is
    // now deferred behind a 250 ms debounce, so without this a user who
    // switches away (or whose machine sleeps) within that window loses the
    // last edit. visibilitychange covers the sleep/hide case that blur misses.
    const flushPendingWrites = () => {
      // Drain the in-memory patch queue before asking Go to write: otherwise
      // the write lands without the character typed in the last 120 ms.
      void flushPendingRequestPatch().then(() => FlushPendingWrites()).catch(() => {
        // A failed background write is surfaced to the user by the Go side's
        // notification path on the next mutation; there is nothing useful to
        // do from a blur handler, and throwing here would be unhandled.
      })
    }
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'hidden') flushPendingWrites()
    }
    window.addEventListener('blur', flushPendingWrites)
    document.addEventListener('visibilitychange', handleVisibilityChange)
    removeFlushOnBlurListeners = () => {
      window.removeEventListener('blur', flushPendingWrites)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
	    stopGitCloneProgress = EventsOn('git:clone:progress', (event: GitCloneProgress) => {
	      gitCloneProgress = [...gitCloneProgress, event].slice(-24)
	    })
	    stopOAuth2Authorize = EventsOn('oauth2:authorize', (request: OAuth2AuthorizationBrowserRequest) => {
	      oauth2AuthorizationRequest = request
	      oauth2CallbackURLInput = ''
	      oauth2CallbackMessage = ''
	      oauth2FrameKey += 1
	    })
	    const applyLiveSessionEvent = (push: LiveSessionPush) => {
      const key = liveSessionKey(push.collectionId, push.itemId)
      const current = liveSessionLogs[key] ?? emptyLiveSessionLog()
      const next = applyLiveSessionPush(current, push)
      // Reassign the map, not just the entry: Svelte's legacy reactivity
      // tracks assignment to `liveSessionLogs`, and mutating a value inside it
      // would leave the inspector showing a stale log.
      if (next !== current) liveSessionLogs = { ...liveSessionLogs, [key]: next }
    }
    stopWebSocketEvents = EventsOn('ws:event', (push: LiveSessionPush) => applyLiveSessionEvent(push))
    stopGrpcEvents = EventsOn('grpc:event', (push: LiveSessionPush) => applyLiveSessionEvent(push))
    stopNativeMenuCommands = EventsOn('liteapi:menu-command', (command: string) => {
	      void handleNativeMenuCommand(command)
	    })
	    OnFileDrop((_x, _y, paths) => {
	      if (activeView === 'import' && importSourceMode === 'files' && paths.length > 0) {
	        void previewImportPaths(paths)
	      }
	    }, true)
	    systemThemeQuery = window.matchMedia('(prefers-color-scheme: dark)')
    const updateSystemTheme = () => {
      systemThemeMode = systemThemeQuery?.matches ? 'dark' : 'light'
    }
    updateSystemTheme()
	    systemThemeQuery.addEventListener('change', updateSystemTheme)
	    removeSystemThemeListener = () => systemThemeQuery?.removeEventListener('change', updateSystemTheme)
	    startCollectionWatchPolling()
	    startOpenAPISyncPolling()
	  })

	  onDestroy(() => {
	    stopDotEnvRefresh()
	    stopTerminalPolling()
	    stopCollectionWatchPolling()
	    stopOpenAPISyncPolling()
	    removeCompactWorkbenchListener?.()
	    removeFlushOnBlurListeners?.()
	    stopGitCloneProgress?.()
	    stopWebSocketEvents?.()
	    stopGrpcEvents?.()
	    stopOAuth2Authorize?.()
	    stopNativeMenuCommands?.()
	    OnFileDropOff()
	    removeSystemThemeListener?.()
    clearAutoSaveTimer()
    for (const timer of Object.values(copiedVariableTooltipTimers)) {
      window.clearTimeout(timer)
    }
  })

  function normalizedThemeMode(value: string | undefined): ThemeMode {
    return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
  }

  function normalizedThemeVariant(value: string | undefined, variants: ThemeVariant[]) {
    return variants.some((variant) => variant.id === value) ? value ?? variants[0].id : variants[0].id
  }

  function applyThemeToDocument(mode: 'light' | 'dark', variant: string) {
    if (typeof document === 'undefined') return
    document.documentElement.dataset.theme = mode
    document.documentElement.dataset.themeVariant = variant
  }

  function applyZoomToDocument(percentage: number) {
    if (typeof document === 'undefined') return
    const zoom = normalizedZoomPercentage(percentage)
    document.documentElement.dataset.zoomPercentage = String(zoom)
    document.documentElement.style.setProperty('--app-zoom', String(zoom / 100))
  }

  function applyCodeFontToDocument(font: string, size: number) {
    if (typeof document === 'undefined') return
    const normalizedFont = normalizedCodeFont(font)
    const normalizedSize = normalizedCodeFontSize(size)
    document.documentElement.dataset.codeFont = normalizedFont
    document.documentElement.dataset.codeFontSize = String(normalizedSize)
    document.documentElement.style.setProperty('--code-font-family', codeFontFamilyFor(normalizedFont))
    document.documentElement.style.setProperty('--code-font-size', `${normalizedSize}px`)
  }

  function currentKeyBindingOS(): KeyBindingOS {
    if (typeof navigator !== 'undefined' && navigator.platform.toLowerCase().includes('mac')) return 'mac'
    return 'windows'
  }

  function keybindingsAreEnabled(preferences: main.Preferences | undefined) {
    return preferences?.keybindingsEnabled !== false
  }

  function keyBindingDefaultsByAction() {
    const defaults: Record<string, KeyBindingDefinition> = {}
    for (const section of keyBindingSections) {
      for (const [action, binding] of Object.entries(section.bindings)) {
        defaults[action] = binding
      }
    }
    return defaults
  }

  function mergedKeyBinding(action: string): KeyBindingDefinition | undefined {
    const defaults = keyBindingDefaultsByAction()
    const base = defaults[action]
    if (!base) return undefined
    const override = state?.preferences?.keyBindings?.[action] as main.KeyBinding | undefined
    return {
      ...base,
      ...(override ?? {}),
      name: override?.name || base.name
    }
  }

  function keyBindingValue(action: string, os: KeyBindingOS = currentKeyBindingOS()) {
    const binding = mergedKeyBinding(action)
    return (binding?.[os] as string | undefined) || ''
  }

  function keyBindingDisplayValue(action: string, os: KeyBindingOS = currentKeyBindingOS()) {
    const binding = mergedKeyBinding(action)
    const display = binding?.displayValue?.[os]
    return display || keyBindingValue(action, os)
  }

  function keyBindingParts(value: string) {
    return value.split(keyBindingSeparator).map((part) => part.trim()).filter(Boolean)
  }

  function isKeyBindingModifier(value: string) {
    return value === 'ctrl' || value === 'command' || value === 'alt' || value === 'shift'
  }

  function keyBindingSignature(value: string) {
    const order = ['ctrl', 'command', 'alt', 'shift']
    const parts = keyBindingParts(value.toLowerCase())
    const modifiers = parts.filter(isKeyBindingModifier).sort((left, right) => order.indexOf(left) - order.indexOf(right))
    const keys = parts.filter((part) => !isKeyBindingModifier(part))
    return [...modifiers, ...keys].join(keyBindingSeparator)
  }

  function normalizeEventKey(event: KeyboardEvent) {
    if (event.key === ' ') return 'space'
    if (event.key === 'Escape') return 'esc'
    if (event.key === 'Enter') return 'enter'
    if (event.key === 'Backspace') return 'backspace'
    if (event.key === 'Tab') return 'tab'
    if (event.key === 'Delete') return 'delete'
    if (event.key === 'Control') return 'ctrl'
    if (event.key === 'Meta') return 'command'
    if (event.key === 'Alt') return 'alt'
    if (event.key === 'Shift') return 'shift'
    if (event.code?.startsWith('Key')) return event.code.slice(3).toLowerCase()
    if (event.code?.startsWith('Digit')) return event.code.slice(5)
    if (event.key.length === 1) return event.key.toLowerCase()
    return event.key.toLowerCase()
  }

  function keyBindingComboFromEvent(event: KeyboardEvent) {
    const parts: string[] = []
    if (event.ctrlKey) parts.push('ctrl')
    if (event.metaKey) parts.push('command')
    if (event.altKey) parts.push('alt')
    if (event.shiftKey) parts.push('shift')
    const key = normalizeEventKey(event)
    if (key && !isKeyBindingModifier(key)) parts.push(key)
    return parts.join(keyBindingSeparator)
  }

  function keyBindingEventMatches(event: KeyboardEvent, action: string) {
    const combo = keyBindingComboFromEvent(event)
    const value = keyBindingValue(action)
    if (!combo || !value) return false
    return keyBindingSignature(combo) === keyBindingSignature(value)
  }

  function formatKeyBindingToken(token: string) {
    const os = currentKeyBindingOS()
    const labels: Record<string, string> = os === 'mac'
      ? { command: 'Cmd', ctrl: 'Ctrl', alt: 'Opt', shift: 'Shift', enter: 'Enter', esc: 'Esc', space: 'Space', arrowup: 'Up', arrowdown: 'Down', arrowleft: 'Left', arrowright: 'Right' }
      : { command: 'Win', ctrl: 'Ctrl', alt: 'Alt', shift: 'Shift', enter: 'Enter', esc: 'Esc', space: 'Space', arrowup: 'Up', arrowdown: 'Down', arrowleft: 'Left', arrowright: 'Right' }
    return labels[token] || token.toUpperCase()
  }

  function formatKeyBinding(value: string) {
    if (!value) return ''
    return value
      .split(/\s+-\s+/)
      .map((part) => keyBindingParts(part).map(formatKeyBindingToken).join(' + '))
      .join(' - ')
  }

  function keyBindingIsCustomized(action: string) {
    return Boolean(state?.preferences?.keyBindings?.[action])
  }

  function keyBindingCanEdit(action: string) {
    const binding = mergedKeyBinding(action)
    return Boolean(binding && !binding.readOnly)
  }

  function visibleKeyBindingEntries(section: KeyBindingSection) {
    return Object.entries(section.bindings).filter(([, binding]) => !binding.hidden)
  }

  function validateKeyBinding(action: string, combo: string) {
    const parts = keyBindingParts(combo)
    const nonModifiers = parts.filter((part) => !isKeyBindingModifier(part))
    if (parts.length < 2 || parts.length > 4 || nonModifiers.length !== 1) {
      return 'Use one key plus at least one modifier.'
    }
    if (!parts.some(isKeyBindingModifier)) {
      return 'Use at least one modifier.'
    }
    const signature = keyBindingSignature(combo)
    const os = currentKeyBindingOS()
    for (const section of keyBindingSections) {
      for (const otherAction of Object.keys(section.bindings)) {
        if (otherAction === action) continue
        const other = keyBindingValue(otherAction, os)
        if (other && keyBindingSignature(other) === signature) {
          return 'This shortcut is already in use.'
        }
      }
    }
    return ''
  }

  function stopDotEnvRefresh() {
    if (dotEnvRefreshTimer === undefined) return
    window.clearInterval(dotEnvRefreshTimer)
    dotEnvRefreshTimer = undefined
  }

  function visibleEnvironmentVariables(vars: main.Variable[] | undefined, tab: EnvironmentVariableTab, query: string): IndexedVariable[] {
    return (vars ?? [])
      .map((variable, index) => ({ variable, index }))
      .filter(({ variable }) => (tab === 'secrets' ? Boolean(variable.secret) : !variable.secret))
      .filter(({ variable }) => environmentVariableMatches(variable, query))
  }

  function environmentVariableMatches(variable: main.Variable, query: string) {
    if (!query) return true
    return [variable.name, variable.value, variable.type, variable.dataType].some((value) => searchHit(value, query))
  }

  function environmentVariableAddLabel(tab: EnvironmentVariableTab) {
    return tab === 'secrets' ? 'Add secret' : 'Add variable'
  }

  async function loadProcessEnvTooltipValues(collectionId: string, names: string[], key: string) {
    try {
      const values = await ResolveProcessEnvValues(collectionId, names)
      if (processEnvTooltipKey === key) {
        processEnvTooltipValues = values
      }
    } catch {
      if (processEnvTooltipKey === key) {
        processEnvTooltipValues = {}
      }
    }
  }

  function variableTooltipsForRequest(workspace: main.Workspace, collection: main.Collection, request: main.RequestItem, environmentId: string, processEnvValues: Record<string, string>): VariableTooltipInfo[] {
    return variableNamesForRequest(request).map((name) => resolveVariableTooltip(name, workspace, collection, request, environmentId, processEnvValues))
  }

  function urlVariableSegments(value: string, infos: VariableTooltipInfo[], pathParams?: main.KeyValue[]): URLVariableSegment[] {
    const segments: URLVariableSegment[] = []
    const infoByName = new Map(infos.map((info) => [info.name, info]))
    const tokenPattern = pathParams
      ? /\{\{([^{}]+?)\}\}|\/:([^/?&=]+)/g
      : /\{\{([^{}]+?)\}\}/g
    let cursor = 0
    let match: RegExpExecArray | null
    while ((match = tokenPattern.exec(value)) !== null) {
      if (match.index > cursor) {
        segments.push({ key: `text:${cursor}`, text: value.slice(cursor, match.index), variable: false, prompt: false })
      }
      if (match[2] !== undefined) {
        const name = match[2]
        segments.push({
          key: `path:${match.index}:${name}`,
          text: match[0],
          variable: true,
          prompt: false,
          path: true,
          name,
          info: pathParamTooltipInfo(name, pathParams ?? [])
        })
        cursor = match.index + match[0].length
        continue
      }
      const rawName = match[1] ?? ''
      const name = rawName.trim()
      if (promptVariableTextPattern.test(rawName)) {
        segments.push({
          key: `prompt:${match.index}:${rawName}`,
          text: match[0],
          variable: false,
          prompt: true,
          name: rawName.slice(1)
        })
        cursor = match.index + match[0].length
        continue
      }
      segments.push({
        key: `var:${match.index}:${name}`,
        text: match[0],
        variable: true,
        prompt: false,
        name,
        info: infoByName.get(name) ?? fallbackVariableTooltipInfo(name)
      })
      cursor = match.index + match[0].length
    }
    if (cursor < value.length) {
      segments.push({ key: `text:${cursor}`, text: value.slice(cursor), variable: false, prompt: false })
    }
    return segments
  }

  function isValidURLVariableSegment(segment: URLVariableSegment) {
    return segment.variable && segment.info.found && segment.info.validName
  }

  function fallbackVariableTooltipInfo(name: string): VariableTooltipInfo {
    const validName = isValidVariableName(name)
    return {
      name,
      scope: 'Request',
      rawValue: '',
      resolvedValue: '',
      secret: false,
      readOnly: !validName,
      found: false,
      editable: validName,
      validName,
      source: validName ? 'missing' : 'invalid',
      index: -1
    }
  }

  function pathParamTooltipInfo(name: string, pathParams: main.KeyValue[]): VariableTooltipInfo {
    const validName = isValidVariableName(name)
    const index = pathParams.findIndex((param) => param.name === name)
    const row = index >= 0 ? pathParams[index] : undefined
    const rawValue = String(row?.value ?? '')
    return {
      name,
      scope: 'Path Param',
      rawValue,
      resolvedValue: rawValue,
      secret: false,
      readOnly: !validName,
      found: Boolean(row && row.enabled !== false && rawValue.trim() !== ''),
      editable: validName && Boolean(row),
      validName,
      source: validName ? 'path' : 'invalid',
      index
    }
  }

  function pathParamNamesFromURL(rawURL: string) {
    if (!rawURL) return []
    let pathSource = rawURL
    try {
      const url = new URL(rawURL.startsWith('http://') || rawURL.startsWith('https://') ? rawURL : `http://${rawURL}`)
      pathSource = url.pathname
    } catch {
      pathSource = rawURL.split(/[?#]/, 1)[0] ?? rawURL
    }
    const names: string[] = []
    const seen = new Set<string>()
    const add = (name: string) => {
      if (!name || seen.has(name)) return
      seen.add(name)
      names.push(name)
    }
    for (const segment of pathSource.split('/')) {
      if (segment.startsWith(':')) {
        add(segment.slice(1))
        continue
      }
      if (!/^[A-Za-z0-9_.-]+\([^)]*\)$/.test(segment)) continue
      const regex = /[:]([a-zA-Z_]\w*)/g
      let match: RegExpExecArray | null
      while ((match = regex.exec(segment)) !== null) {
        add((match[1] ?? '').replace(/[')"`]+$/, '').replace(/^[('"`]+/, ''))
      }
    }
    return names
  }

  function syncPathParamsForURL(rawURL: string, currentRows: main.KeyValue[] = []) {
    return pathParamNamesFromURL(rawURL).map((name) => {
      const existing = currentRows.find((row) => row.name === name)
      return existing ?? ({ name, value: '', enabled: true, secret: false, description: '' } as main.KeyValue)
    })
  }

  function queryParamsForURL(rawURL: string, currentRows: main.KeyValue[] = []) {
    const query = rawURL.split('#')[0]?.split('?').slice(1).join('?') ?? ''
    const disabledRows = currentRows.filter((row) => row.enabled === false).map((row) => ({ ...row }))
    if (!query) return disabledRows
    const rows: main.KeyValue[] = []
    const decode = (value: string) => {
      try {
        return decodeURIComponent(value.replace(/\+/g, ' '))
      } catch {
        return value
      }
    }
    for (const pair of query.split('&')) {
      if (!pair) continue
      const [rawName, ...rawValueParts] = pair.split('=')
      const name = decode(rawName ?? '')
      const value = decode(rawValueParts.join('='))
      const existing = currentRows.find((row) => row.enabled !== false && row.name === name)
      rows.push({ ...(existing ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue)), name, value, enabled: true } as main.KeyValue)
    }
    return [...rows, ...disabledRows]
  }

  function variableNamesForRequest(request: main.RequestItem) {
    const names = new Set<string>()
    const scan = (value: unknown) => collectVariableNames(value, names)
    const scanRows = (rows: main.KeyValue[] | undefined) => {
      for (const row of rows ?? []) {
        if (row.enabled === false) continue
        scan(row.name)
        scan(row.value)
      }
    }
    scan(request.url)
    scanRows(request.params)
    scanRows(request.pathParams)
    scanRows(request.headers)
    scanBodyVariables(request.body, scan, scanRows)
    return Array.from(names)
  }

  function collectVariableNames(value: unknown, names: Set<string>) {
    if (value === null || value === undefined) return
    const pattern = /\{\{\s*([^{}]+?)\s*\}\}/g
    const text = String(value)
    let match: RegExpExecArray | null
    while ((match = pattern.exec(text)) !== null) {
      const name = match[1]?.trim()
      if (!name || name.startsWith('?')) continue
      names.add(name)
    }
  }

  function scanBodyVariables(body: main.RequestBody | undefined, scan: (value: unknown) => void, scanRows: (rows: main.KeyValue[] | undefined) => void) {
    if (!body) return
    scan(body.json)
    scan(body.xml)
    scan(body.text)
    scan(body.graphqlQuery)
    scan(body.graphqlVariables)
    scanRows(body.formUrlEncoded)
	    for (const part of body.multipart ?? []) {
	      if (part.enabled === false) continue
	      scan(part.name)
	      scan(part.value)
	      scan(part.filePath)
	      scan(part.contentType)
	    }
	    scan(body.filePath)
	    scan(body.fileContentType)
	    for (const file of fileBodyRows(body)) {
	      scan(file.filePath)
	      scan(file.contentType)
	    }
	  }

  function resolveVariableTooltip(name: string, workspace: main.Workspace, collection: main.Collection, request: main.RequestItem, environmentId: string, processEnvValues: Record<string, string>): VariableTooltipInfo {
    const validName = isValidVariableName(name)
    if (!validName) {
      return {
        name,
        scope: 'Request',
        rawValue: '',
        resolvedValue: '',
        secret: false,
        readOnly: true,
        found: false,
        editable: false,
        validName: false,
        source: 'invalid',
        index: -1
      }
    }

    if (name.startsWith('process.env.')) {
      const loaded = Object.prototype.hasOwnProperty.call(processEnvValues, name)
      return {
        name,
        scope: 'Process Env',
        rawValue: '',
        resolvedValue: loaded ? processEnvValues[name] : 'Loading...',
        secret: false,
        readOnly: true,
        found: true,
        editable: false,
        validName,
        source: 'process',
        index: -1
      }
    }

    const match = findTooltipVariable(name, workspace, collection, request, environmentId)
    if (!match) {
      return {
        name,
        scope: 'Request',
        rawValue: '',
        resolvedValue: '',
        secret: false,
        readOnly: false,
        found: false,
        editable: true,
        validName,
        source: 'missing',
        index: -1
      }
    }
    const readOnly = match.source === 'folder' || match.source === 'runtime'
    const rawValue = String(match.variable.value ?? '')
    const resolution = resolveTooltipValue(rawValue, workspace, collection, request, environmentId, processEnvValues, new Set([name]))
    return {
      name,
      scope: match.scope,
      rawValue,
      resolvedValue: resolution.value,
      secret: Boolean(match.variable.secret) || resolution.containsSecret,
      readOnly,
      found: true,
      editable: !readOnly,
      validName,
      source: match.source,
      index: match.index,
      environmentId: match.environmentId,
      globalEnvironmentId: match.globalEnvironmentId
    }
  }

  function findTooltipVariable(name: string, workspace: main.Workspace, collection: main.Collection, request: main.RequestItem, environmentId: string) {
    let found: { variable: main.Variable; scope: string; source: VariableTooltipSource; index: number; environmentId?: string; globalEnvironmentId?: string } | undefined
    const consider = (variables: main.Variable[] | undefined, scope: string, source: VariableTooltipSource, sourceId = '') => {
      for (const [index, variable] of (variables ?? []).entries()) {
        if (variable.enabled === false || variable.name !== name) continue
        found = {
          variable,
          scope,
          source,
          index,
          environmentId: source === 'environment' ? sourceId : undefined,
          globalEnvironmentId: source === 'global' ? sourceId : undefined
        }
      }
    }
    const activeGlobal = workspace.globalEnvironments?.find((env) => env.id === workspace.activeGlobalEnvironmentId)
    consider(activeGlobal?.variables, 'Global', 'global', activeGlobal?.id)
    consider(collection.variables, 'Collection', 'collection')
    const environment = collection.environments?.find((env) => env.id === environmentId)
    consider(environment?.variables, 'Environment', 'environment', environment?.id)
    for (const folder of folderChainForRequest(collection, request)) {
      consider(folder.variables, 'Folder', 'folder', folder.path)
    }
    consider(request.vars?.req, 'Request', 'request')
    consider(collection.runtimeVariables, 'Runtime', 'runtime')
    return found
  }

  function isValidVariableName(name: string) {
    return /^[\w.-]+$/.test(name)
  }

  function resolveTooltipValue(value: string, workspace: main.Workspace, collection: main.Collection, request: main.RequestItem, environmentId: string, processEnvValues: Record<string, string>, seen: Set<string>): TooltipResolution {
    let containsSecret = false
    const resolvedValue = value.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (_match, rawName: string) => {
      const name = rawName.trim()
      if (!name || name.startsWith('?') || seen.has(name)) return ''
      if (name.startsWith('process.env.')) return processEnvValues[name] ?? ''
      const match = findTooltipVariable(name, workspace, collection, request, environmentId)
      if (!match) return ''
      containsSecret = containsSecret || Boolean(match.variable.secret)
      seen.add(name)
      const resolved = resolveTooltipValue(String(match.variable.value ?? ''), workspace, collection, request, environmentId, processEnvValues, seen)
      seen.delete(name)
      containsSecret = containsSecret || resolved.containsSecret
      return resolved.value
    })
    return { value: resolvedValue, containsSecret }
  }

  function displayTooltipValue(info: VariableTooltipInfo, revealed: boolean) {
    if (!info.found && info.source !== 'path') return 'Not defined'
    if (info.secret && !revealed) return '********'
    return info.resolvedValue
  }

  function toggleTooltipSecret(name: string) {
    revealedVariableTooltips = { ...revealedVariableTooltips, [name]: !revealedVariableTooltips[name] }
  }

  async function copyVariableTooltipValue(info: VariableTooltipInfo) {
    const latestInfo = info.source === 'path'
      ? pathParamTooltipInfo(info.name, activeRequest?.pathParams ?? [])
      : requestVariableTooltips.find((candidate) => candidate.name === info.name) ?? info
    if (!latestInfo.found || !latestInfo.validName || copiedVariableTooltips[latestInfo.name]) return
    try {
      await navigator.clipboard.writeText(latestInfo.resolvedValue ?? '')
      if (copiedVariableTooltipTimers[latestInfo.name]) {
        window.clearTimeout(copiedVariableTooltipTimers[latestInfo.name])
      }
      copiedVariableTooltips = { ...copiedVariableTooltips, [latestInfo.name]: true }
      copiedVariableTooltipTimers = {
        ...copiedVariableTooltipTimers,
        [latestInfo.name]: window.setTimeout(() => {
          copiedVariableTooltips = { ...copiedVariableTooltips, [latestInfo.name]: false }
          const { [latestInfo.name]: _removed, ...remaining } = copiedVariableTooltipTimers
          copiedVariableTooltipTimers = remaining
        }, 1200)
      }
    } catch {
      error = 'Clipboard is unavailable'
    }
  }

  function toggleActiveVariableTooltip(name: string) {
    activeVariableTooltip = activeVariableTooltip === name ? '' : name
  }

  function handleInlineVariableTokenKey(event: KeyboardEvent, name: string) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      toggleActiveVariableTooltip(name)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      activeVariableTooltip = ''
    }
  }

  function closeVariableTooltipOnOutside(event: MouseEvent) {
    const target = event.target as HTMLElement | null
    if (!target) return
    if (!target.closest('.request-actions')) closeRequestActionMenus()
    if (target.closest('.variable-chip-wrapper, .url-variable-token-wrapper, .inline-variable-token-wrapper, .CodeMirror-brunoVarInfo, .variable-tooltip')) return
    activeVariableTooltip = ''
  }

  function closeRequestActionMenus() {
    document.querySelectorAll<HTMLDetailsElement>('details.request-actions[open]').forEach((menu) => {
      menu.open = false
    })
  }

  function patchURLField(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    urlInputScrollLeft = input.scrollLeft
    patchRequestWithURL(input.value)
  }

  function syncURLInputScroll(event: Event) {
    urlInputScrollLeft = (event.currentTarget as HTMLInputElement).scrollLeft
  }

  function updateBodyText(field: BodyTextField, event: Event) {
    const textarea = event.currentTarget as HTMLTextAreaElement
    bodyTextScrollLeft = textarea.scrollLeft
    bodyTextScrollTop = textarea.scrollTop
    updateBody({ [field]: textarea.value } as Partial<main.RequestBody>)
  }

  function syncBodyTextScroll(event: Event) {
    const textarea = event.currentTarget as HTMLTextAreaElement
    bodyTextScrollLeft = textarea.scrollLeft
    bodyTextScrollTop = textarea.scrollTop
  }

  function beginVariableTooltipEdit(info: VariableTooltipInfo) {
    if (!info.editable) return
    activeVariableTooltip = info.name
    editingVariableTooltip = info.name
    variableTooltipDraft = info.found ? info.rawValue : ''
    copiedVariableTooltips = { ...copiedVariableTooltips, [info.name]: false }
  }

  function cancelVariableTooltipEdit() {
    editingVariableTooltip = ''
    variableTooltipDraft = ''
  }

  function handleVariableTooltipEditorKey(event: KeyboardEvent, info: VariableTooltipInfo) {
    if (event.key === 'Escape') {
      event.preventDefault()
      cancelVariableTooltipEdit()
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void saveVariableTooltipEdit(info, (event.currentTarget as HTMLTextAreaElement).value)
    }
  }

  function handleVariableTooltipEditorBlur(event: FocusEvent, info: VariableTooltipInfo) {
    const nextTarget = event.relatedTarget as HTMLElement | null
    if (nextTarget?.closest('.variable-tooltip')) return
    void saveVariableTooltipEdit(info, (event.currentTarget as HTMLTextAreaElement).value)
  }

  async function saveVariableTooltipEdit(info: VariableTooltipInfo, nextValue = variableTooltipDraft) {
    if (!activeWorkspace || !activeCollection || !activeRequest || !info.editable) return
    const value = nextValue
    await runAction('save variable', async () => {
      if (info.source === 'global') {
        const environment = activeWorkspace.globalEnvironments?.find((candidate) => candidate.id === info.globalEnvironmentId)
        if (!environment || info.index < 0) throw new Error(`Global variable ${info.name} was not found`)
        const vars = [...(environment.variables ?? [])]
        vars[info.index] = { ...vars[info.index], value }
        state = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, environment.id, vars)
      } else if (info.source === 'collection') {
        if (info.index < 0) throw new Error(`Collection variable ${info.name} was not found`)
        const vars = [...(activeCollection.variables ?? [])]
        vars[info.index] = { ...vars[info.index], value }
        state = await UpdateCollectionVariables(activeCollection.id, vars)
      } else if (info.source === 'environment') {
        const environment = activeCollection.environments?.find((candidate) => candidate.id === info.environmentId)
        if (!environment || info.index < 0) throw new Error(`Environment variable ${info.name} was not found`)
        const vars = [...(environment.variables ?? [])]
        vars[info.index] = { ...vars[info.index], value }
        state = await UpdateEnvironmentVariables(activeCollection.id, environment.id, vars)
      } else if (info.source === 'path') {
        if (info.index < 0) throw new Error(`Path parameter ${info.name} was not found`)
        const pathParams = [...(activeRequest.pathParams ?? [])]
        pathParams[info.index] = { ...pathParams[info.index], value, enabled: true }
        state = await UpdateRequest(activeCollection.id, activeRequest.id, { pathParams } as unknown as main.RequestPatch)
        state = await SaveRequest(activeCollection.id, activeRequest.id)
      } else if (info.source === 'request' || info.source === 'missing') {
        const req = [...(activeRequest.vars?.req ?? [])]
        if (info.source === 'request' && info.index >= 0) {
          req[info.index] = { ...req[info.index], value }
        } else {
          req.push({ id: `req-var-${Date.now()}`, name: info.name, value, type: 'text', dataType: 'string', enabled: true, secret: false })
        }
        const collectionId = activeCollection.id
        const requestId = activeRequest.id
        state = await UpdateRequest(collectionId, requestId, { vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as main.RequestPatch)
        state = await SaveRequest(collectionId, requestId)
      }
      editingVariableTooltip = ''
      variableTooltipDraft = ''
      activeVariableTooltip = info.name
    })
  }

  async function load() {
    loading = true
    loadingStatus = 'Opening workspace'
    await runAction('load', async () => {
      state = await GetState()
	    workbenchStorageScope = await GetWebStorageScope()
	    restoreWorkbenchLayout()
      loadingStatus = 'Checking recovery'
      recoveryEntries = (await ListRecoveryEntries()) ?? []
      loadingStatus = 'Measuring local cache'
      await refreshFileCacheSize()
      loadingStatus = 'Preparing workbench'
      applyDevToolsShellPreferences(state.preferences?.devTools)
      if (devToolsOpen) await refreshDevToolsSnapshot()
      selectedEnvironmentId = activeCollection?.environments?.[0]?.id ?? ''
    })
    loading = false
  }

  async function refreshRecoveryEntries() {
    recoveryEntries = (await ListRecoveryEntries()) ?? []
  }

  function recoveryExpiryLabel(entry: main.RecoveryEntry) {
    const value = new Date(entry.expiresAt)
    return Number.isNaN(value.getTime()) ? 'expiry unavailable' : `expires ${value.toLocaleString()}`
  }

  async function restoreRecoveryEntry(entry: main.RecoveryEntry) {
    if (recoveryBusyEntryID) return
    recoveryBusyEntryID = entry.id
    let restored = false
    await runAction('restore recovery entry', async () => {
      state = await RestoreRecoveryEntry(entry.id)
      restored = true
      await refreshRecoveryEntries()
    })
    if (!restored) await refreshRecoveryEntries().catch(() => undefined)
    recoveryBusyEntryID = ''
  }

  async function discardRecoveryEntry(entry: main.RecoveryEntry) {
    if (recoveryBusyEntryID) return
    recoveryBusyEntryID = entry.id
    await runAction('discard recovery entry', async () => {
      const discarded = await DiscardRecoveryEntry(entry.id)
      if (!discarded) throw new Error('Recovery entry was not removed')
      await refreshRecoveryEntries()
    })
    recoveryBusyEntryID = ''
  }

  async function runAction(label: string, action: () => Promise<void>) {
    const actionID = ++nextActionID
    activeActions.set(actionID, label)
    busy = label
    error = ''
    try {
      await action()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      activeActions.delete(actionID)
      busy = Array.from(activeActions.values()).at(-1) ?? ''
    }
  }

  function collectPromptNames(collection: main.Collection, request: main.RequestItem, environmentId: string) {
    const prompts = new Set<string>()
    const scanText = (value: unknown) => {
      if (value === null || value === undefined) return
      promptTokenPattern.lastIndex = 0
      let match: RegExpExecArray | null
      const text = String(value)
      while ((match = promptTokenPattern.exec(text)) !== null) {
        const name = match[1]?.trim()
        if (name) prompts.add(name)
      }
    }
    const scanObject = (value: unknown) => {
      if (value === null || value === undefined) return
      if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
        scanText(value)
        return
      }
      if (Array.isArray(value)) {
        value.forEach(scanObject)
        return
      }
      if (typeof value === 'object') {
        Object.values(value as Record<string, unknown>).forEach(scanObject)
      }
    }
    const scanKeyValues = (rows: main.KeyValue[] | undefined) => {
      for (const row of rows ?? []) {
        if (row.enabled === false) continue
        scanText(row.name)
        scanText(row.value)
      }
    }
    const scanVariables = (rows: main.Variable[] | undefined) => {
      for (const variable of rows ?? []) {
        if (variable.enabled === false) continue
        scanText(variable.name)
        scanText(variable.value)
      }
    }

    scanText(request.url)
    scanText(request.method)
    scanText(request.protoPath)
    scanKeyValues(request.params)
    scanKeyValues(request.pathParams)
    scanKeyValues(request.headers)
    scanVariables(request.vars?.req)
    scanBodyPrompts(request.body, scanText, scanKeyValues)
    for (const message of request.grpcMessages ?? []) {
      scanText(message.name)
      scanText(message.content)
    }
    for (const message of request.wsMessages ?? []) {
      if (message.selected === false) continue
      scanText(message.name)
      scanText(message.content)
    }
    scanObject(request.auth)

    scanVariables(activeGlobalEnvironment?.variables)
    scanKeyValues(collection.headers)
    scanVariables(collection.variables)
    scanObject(collection.auth)

    for (const folder of folderChainForRequest(collection, request)) {
      scanKeyValues(folder.headers)
      scanVariables(folder.variables)
      scanObject(folder.auth)
    }

    const environment = collection.environments?.find((candidate) => candidate.id === environmentId)
    scanVariables(environment?.variables)
    return Array.from(prompts)
  }

  function scanBodyPrompts(body: main.RequestBody | undefined, scanText: (value: unknown) => void, scanKeyValues: (rows: main.KeyValue[] | undefined) => void) {
    if (!body) return
    if (body.mode === 'json') {
      scanText(body.json)
    } else if (body.mode === 'xml') {
      scanText(body.xml)
    } else if (body.mode === 'graphql') {
      scanText(body.graphqlQuery)
      scanText(body.graphqlVariables)
    } else if (body.mode === 'text' || body.mode === 'sparql') {
      scanText(body.text)
    } else if (body.mode === 'formUrlEncoded') {
      scanKeyValues(body.formUrlEncoded)
    } else if (body.mode === 'multipartForm') {
      for (const part of body.multipart ?? []) {
        if (part.enabled === false) continue
	        scanText(part.name)
	        scanText(part.value)
	        scanText(part.filePath)
	        scanText(part.contentType)
	      }
	    } else if (body.mode === 'file') {
	      scanText(body.filePath)
	      scanText(body.fileContentType)
	      for (const file of fileBodyRows(body)) {
	        scanText(file.filePath)
	        scanText(file.contentType)
	      }
	    }
  }

  function folderChainForRequest(collection: main.Collection, request: main.RequestItem) {
    const folderPath = request.folderPath ?? ''
    if (!folderPath) return []
    return [...(collection.folders ?? [])]
      .filter((folder) => folder.path === folderPath || folderPath.startsWith(`${folder.path}/`))
      .sort((left, right) => left.path.length - right.path.length)
  }

  function promptForVariables(prompts: string[]) {
    return new Promise<Record<string, string> | null>((resolve) => {
      promptDialog = {
        prompts,
        values: Object.fromEntries(prompts.map((prompt) => [prompt, ''])),
        resolve
      }
      window.setTimeout(() => document.querySelector<HTMLInputElement>('.prompt-dialog input')?.focus(), 0)
    })
  }

  function updatePromptValue(prompt: string, value: string) {
    if (!promptDialog) return
    promptDialog = {
      ...promptDialog,
      values: { ...promptDialog.values, [prompt]: value }
    }
  }

  function submitPromptDialog() {
    if (!promptDialog) return
    const { values, resolve } = promptDialog
    promptDialog = null
    resolve({ ...values })
  }

  function cancelPromptDialog() {
    if (!promptDialog) return
    const { resolve } = promptDialog
    promptDialog = null
    resolve(null)
  }

  async function createCollection() {
    if (!activeWorkspace) return
    await runAction('create collection', async () => {
      state = await CreateCollection(activeWorkspace.id, collectionName, 'yml')
    })
  }

  async function createRequest() {
    if (!activeCollection) return
    await runAction('create request', async () => {
      state = await CreateRequest(activeCollection.id, requestType, requestName)
      selectedCollectionId = activeCollection.id
      activeView = 'request'
    })
  }

  async function saveRequest() {
    if (!activeCollection || !activeRequest) return
    await runAction('save request', async () => {
      state = await SaveRequest(activeCollection.id, activeRequest.id)
    })
  }

  async function saveAllOpenTabs() {
    await runAction('save all tabs', async () => {
      state = await SaveAllTabs(activeTab?.collectionId ?? activeCollection?.id ?? '')
    })
  }

  async function saveResponseExample() {
    if (!activeCollection || !activeRequest?.response) return
    await runAction('save response example', async () => {
      state = await SaveResponseExample(activeCollection.id, activeRequest.id, '')
      responseTab = 'examples'
    })
  }

  function suggestedResponseExampleName() {
    const existing = new Set((activeRequest?.examples ?? []).map((example) => example.name).filter(Boolean))
    if (!existing.has('example')) return 'example'
    let index = 1
    while (existing.has(`example (${index})`)) index += 1
    return `example (${index})`
  }

  async function beginCreateResponseExample() {
    createResponseExampleName = suggestedResponseExampleName()
    createResponseExampleDescription = ''
    creatingResponseExample = true
    deletingResponseExampleID = ''
    editingResponseExampleID = ''
    editingResponseExampleDetailsID = ''
    await tick()
    createResponseExampleInput?.focus()
    createResponseExampleInput?.select()
  }

  function cancelCreateResponseExample() {
    creatingResponseExample = false
    createResponseExampleName = ''
    createResponseExampleDescription = ''
  }

  async function createResponseExample() {
    if (!activeCollection || !activeRequest) return
    const name = createResponseExampleName.trim()
    if (!name) return
    await runAction('create response example', async () => {
      state = await CreateResponseExample(activeCollection.id, activeRequest.id, name, createResponseExampleDescription.trim())
      responseTab = 'examples'
      cancelCreateResponseExample()
    })
  }

  function responseExampleIdentifier(example: main.ResponseExample) {
    return example.id || example.name
  }

  function responseExampleForTab(tab: main.OpenTab | undefined) {
    if (!tab || tab.kind !== 'response-example') return undefined
    const collection = activeWorkspace?.collections?.find((candidate) => candidate.id === tab.collectionId)
    const item = collection?.items?.find((candidate) => candidate.id === tab.itemId)
    const target = tab.exampleId || tab.exampleName || ''
    return item?.examples?.find((example) => responseExampleIdentifier(example) === target || example.name === target)
  }

  function tabLabel(tab: main.OpenTab) {
    const collection = activeWorkspace?.collections?.find((candidate) => candidate.id === tab.collectionId)
    const item = collection?.items?.find((candidate) => candidate.id === tab.itemId)
    if (tab.kind === 'response-example') {
      return responseExampleForTab(tab)?.name || tab.exampleName || 'Example'
    }
    return item?.name ?? (tab.transient ? 'Scratch request' : 'Request')
  }

  function methodLabel(method: string) {
    const upper = (method || '').toUpperCase()
    if (upper === 'DELETE') return 'DEL'
    if (upper === 'OPTIONS') return 'OPT'
    return upper
  }

  function tabMethod(tab: main.OpenTab) {
    if (tab.kind === 'response-example') return ''
    const collection = activeWorkspace?.collections?.find((candidate) => candidate.id === tab.collectionId)
    const item = collection?.items?.find((candidate) => candidate.id === tab.itemId)
    return item?.method ?? ''
  }

  let collapsedSidebarCollections: Record<string, boolean> = {}
  let collapsedSidebarFolders: Record<string, boolean> = {}

  function sidebarFolderKey(collectionId: string, folder: string) {
    return `${collectionId}\u0000${folder}`
  }

  function toggleSidebarCollection(collectionId: string) {
    collapsedSidebarCollections = { ...collapsedSidebarCollections, [collectionId]: !collapsedSidebarCollections[collectionId] }
  }

  function toggleSidebarFolder(collectionId: string, folder: string) {
    const key = sidebarFolderKey(collectionId, folder)
    collapsedSidebarFolders = { ...collapsedSidebarFolders, [key]: !collapsedSidebarFolders[key] }
  }

  function collectionIsScratch(collection: main.Collection | undefined) {
    return Boolean(collection?.scratch || (collection && activeWorkspace?.scratchCollectionId === collection.id))
  }

  function requestIsTransient(collection: main.Collection | undefined, item: main.RequestItem | undefined) {
    return Boolean(item?.transient || collectionIsScratch(collection))
  }

  function beginRenameResponseExample(example: main.ResponseExample) {
    editingResponseExampleID = responseExampleIdentifier(example)
    responseExampleNameDraft = example.name
    deletingResponseExampleID = ''
  }

  function cancelRenameResponseExample() {
    editingResponseExampleID = ''
    responseExampleNameDraft = ''
  }

  async function renameResponseExample(example: main.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    const name = responseExampleNameDraft.trim()
    if (!name) return
    await runAction('rename response example', async () => {
      state = await RenameResponseExample(activeCollection.id, activeRequest.id, responseExampleIdentifier(example), name)
      editingResponseExampleID = ''
      responseExampleNameDraft = ''
      responseTab = 'examples'
    })
  }

  async function cloneResponseExample(example: main.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    await runAction('clone response example', async () => {
      state = await CloneResponseExample(activeCollection.id, activeRequest.id, responseExampleIdentifier(example))
      responseTab = 'examples'
      deletingResponseExampleID = ''
    })
  }

  function requestDeleteResponseExample(example: main.ResponseExample) {
    deletingResponseExampleID = responseExampleIdentifier(example)
    editingResponseExampleID = ''
  }

  async function deleteResponseExample(example: main.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    const id = responseExampleIdentifier(example)
    await runAction('delete response example', async () => {
      state = await DeleteResponseExample(activeCollection.id, activeRequest.id, id)
      deletingResponseExampleID = ''
      if (editingResponseExampleDetailsID === id) editingResponseExampleDetailsID = ''
      const { [id]: _deleted, ...remainingDrafts } = responseExampleDrafts
      responseExampleDrafts = remainingDrafts
      responseTab = 'examples'
    })
  }

  function cloneResponseExampleDraft(example: main.ResponseExample) {
    return JSON.parse(JSON.stringify(example)) as main.ResponseExample
  }

  function responseExampleDraft(example: main.ResponseExample) {
    return responseExampleDrafts[responseExampleIdentifier(example)] ?? example
  }

  function beginEditResponseExampleDetails(example: main.ResponseExample) {
    const id = responseExampleIdentifier(example)
    editingResponseExampleDetailsID = id
    responseExampleDrafts = { ...responseExampleDrafts, [id]: cloneResponseExampleDraft(example) }
    deletingResponseExampleID = ''
  }

  function cancelEditResponseExampleDetails(example: main.ResponseExample) {
    const id = responseExampleIdentifier(example)
    if (editingResponseExampleDetailsID === id) editingResponseExampleDetailsID = ''
    const { [id]: _removed, ...remainingDrafts } = responseExampleDrafts
    responseExampleDrafts = remainingDrafts
  }

  function updateResponseExampleDraft(example: main.ResponseExample, updater: (draft: main.ResponseExample) => main.ResponseExample) {
    const id = responseExampleIdentifier(example)
    const draft = cloneResponseExampleDraft(responseExampleDrafts[id] ?? example)
    responseExampleDrafts = { ...responseExampleDrafts, [id]: updater(draft) }
  }

  function updateResponseExampleDescription(example: main.ResponseExample, value: string) {
    updateResponseExampleDraft(example, (draft) => {
      draft.description = value
      return draft
    })
  }

  function prettifyJSON(value: string) {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }

  function responseExampleBodyTypeForContentType(contentType = '') {
    const normalized = contentType.toLowerCase()
    if (normalized.includes('application/json')) return 'json'
    if (normalized.includes('text/xml') || normalized.includes('application/xml')) return 'xml'
    if (normalized.includes('text/html')) return 'html'
    return 'text'
  }

  function updateResponseExampleResponseField(example: main.ResponseExample, field: keyof main.ResponseExamplePayload, value: string | number) {
    updateResponseExampleDraft(example, (draft) => {
      const response = { ...(draft.response ?? {}) } as main.ResponseExamplePayload
      if (field === 'status') {
        response.status = Number.parseInt(String(value), 10) || 0
      } else if (field === 'size') {
        response.size = Number.parseInt(String(value), 10) || 0
      } else {
        response[field] = value as never
      }
      draft.response = response
      return draft
    })
  }

  function updateResponseExampleRequestField(example: main.ResponseExample, field: keyof main.ResponseExampleRequest, value: string) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      if (field === 'method') {
        request.method = value.toUpperCase()
      } else {
        request[field] = value as never
      }
      if (field === 'url') {
        request.params = queryParamsForURL(value, request.params ?? [])
      }
      if (field === 'bodyMode' && value === 'formUrlEncoded') {
        request.formUrlEncoded = request.formUrlEncoded ?? []
      }
      if (field === 'bodyMode' && value === 'multipartForm') {
        request.multipartForm = request.multipartForm ?? []
      }
      if (field === 'bodyMode' && value === 'file') {
        request.file = request.file ?? []
      }
      draft.request = request
      return draft
    })
  }

  function prettifyResponseExampleRequestBody(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.body = prettifyJSON(request.body ?? '')
      draft.request = request
      return draft
    })
  }

  function prettifyResponseExampleResponseBody(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const response = { ...(draft.response ?? {}) } as main.ResponseExamplePayload
      response.body = prettifyJSON(response.body ?? '')
      draft.response = response
      return draft
    })
  }

  function movedRows<T>(rows: T[] | undefined, index: number, direction: -1 | 1) {
    const next = [...(rows ?? [])]
    const target = index + direction
    if (index < 0 || target < 0 || index >= next.length || target >= next.length) return next
    const [row] = next.splice(index, 1)
    next.splice(target, 0, row)
    return next
  }

  function reorderedRows<T>(rows: T[] | undefined, from: number, to: number) {
    const next = [...(rows ?? [])]
    if (from < 0 || to < 0 || from >= next.length || to >= next.length || from === to) return next
    const [row] = next.splice(from, 1)
    next.splice(Math.min(to, next.length), 0, row)
    return next
  }

  function normalizeBulkKeyValueRows(rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    return rows.map((row) => ({
      name: row.name,
      value: row.value,
      enabled: row.enabled,
      secret: row.secret ?? false,
      description: row.description ?? ''
    }) as main.KeyValue)
  }

  function addResponseExampleRequestParam(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.params = [...(request.params ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestParam(example: main.ResponseExample, index: number, field: keyof main.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.params ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue)
      rows[index] = { ...current, [field]: value } as main.KeyValue
      request.params = rows
      draft.request = request
      return draft
    })
  }

  function replaceResponseExampleRequestParams(example: main.ResponseExample, rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.params = normalizeBulkKeyValueRows(rows)
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestParam(example: main.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.params = movedRows(request.params, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestParam(example: main.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.params = reorderedRows(request.params, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestParam(example: main.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.params ?? [])]
      rows.splice(index, 1)
      request.params = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestFormRow(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.formUrlEncoded = [...(request.formUrlEncoded ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestFormRow(example: main.ResponseExample, index: number, field: keyof main.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.formUrlEncoded ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue)
      rows[index] = { ...current, [field]: value } as main.KeyValue
      request.formUrlEncoded = rows
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestFormRow(example: main.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.formUrlEncoded = movedRows(request.formUrlEncoded, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestFormRow(example: main.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.formUrlEncoded = reorderedRows(request.formUrlEncoded, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestFormRow(example: main.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.formUrlEncoded ?? [])]
      rows.splice(index, 1)
      request.formUrlEncoded = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestMultipartRow(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.multipartForm = [...(request.multipartForm ?? []), { name: '', value: '', filePath: '', contentType: '', enabled: true } as main.FormPart]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestMultipartRow(example: main.ResponseExample, index: number, field: keyof main.FormPart, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.multipartForm ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', filePath: '', contentType: '', enabled: true } as main.FormPart)
      rows[index] = { ...current, [field]: value } as main.FormPart
      request.multipartForm = rows
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestMultipartRow(example: main.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.multipartForm = movedRows(request.multipartForm, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestMultipartRow(example: main.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.multipartForm = reorderedRows(request.multipartForm, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestMultipartRow(example: main.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.multipartForm ?? [])]
      rows.splice(index, 1)
      request.multipartForm = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestFileRow(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.file ?? [])]
      rows.push({ filePath: '', contentType: '', selected: rows.length === 0 } as main.FileBodyEntry)
      request.file = rows
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestFileRow(example: main.ResponseExample, index: number, field: keyof main.FileBodyEntry, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.file ?? [])]
      const current = rows[index] ?? ({ filePath: '', contentType: '', selected: rows.length === 0 } as main.FileBodyEntry)
      rows[index] = { ...current, [field]: value } as main.FileBodyEntry
      if (field === 'filePath') {
        rows[index].contentType = contentTypeForFilePath(String(value))
      }
      if (field === 'selected' && value === true) {
        for (let i = 0; i < rows.length; i += 1) {
          rows[i].selected = i === index
        }
      }
      request.file = rows
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestFileRow(example: main.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.file = movedRows(request.file, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestFileRow(example: main.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.file = reorderedRows(request.file, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestFileRow(example: main.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.file ?? [])]
      const removedSelected = rows[index]?.selected
      rows.splice(index, 1)
      if (rows.length > 0 && (removedSelected || !rows.some((row) => row.selected))) {
        rows[0].selected = true
      }
      request.file = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestHeader(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.headers = [...(request.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestHeader(example: main.ResponseExample, index: number, field: keyof main.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.headers ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue)
      rows[index] = { ...current, [field]: value } as main.KeyValue
      request.headers = rows
      draft.request = request
      return draft
    })
  }

  function replaceResponseExampleRequestHeaders(example: main.ResponseExample, rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.headers = normalizeBulkKeyValueRows(rows)
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestHeader(example: main.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.headers = movedRows(request.headers, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestHeader(example: main.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      request.headers = reorderedRows(request.headers, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestHeader(example: main.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as main.ResponseExampleRequest
      const rows = [...(request.headers ?? [])]
      rows.splice(index, 1)
      request.headers = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleHeader(example: main.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = [...(draft.response.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue]
      return draft
    })
  }

  function updateResponseExampleHeader(example: main.ResponseExample, index: number, field: keyof main.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const rows = [...(draft.response.headers ?? [])]
      const oldContentTypeHeader = rows.find((row) => row.name?.toLowerCase() === 'content-type')
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue)
      rows[index] = { ...current, [field]: value } as main.KeyValue
      const contentTypeHeader = rows.find((row) => row.name?.toLowerCase() === 'content-type')
      if (contentTypeHeader && oldContentTypeHeader && contentTypeHeader.value !== oldContentTypeHeader.value) {
        const nextBodyType = responseExampleBodyTypeForContentType(contentTypeHeader.value ?? '')
        if (nextBodyType !== (draft.response.bodyType || 'text')) {
          draft.response.bodyType = nextBodyType
        }
      }
      draft.response.headers = rows
      return draft
    })
  }

  function replaceResponseExampleHeaders(example: main.ResponseExample, rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = normalizeBulkKeyValueRows(rows)
      return draft
    })
  }

  function moveResponseExampleHeader(example: main.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = movedRows(draft.response.headers, index, direction)
      return draft
    })
  }

  function reorderResponseExampleHeader(example: main.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = reorderedRows(draft.response.headers, from, to)
      return draft
    })
  }

  function removeResponseExampleHeader(example: main.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const rows = [...(draft.response.headers ?? [])]
      rows.splice(index, 1)
      draft.response.headers = rows
      return draft
    })
  }

  async function saveResponseExampleDetails(example: main.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    const id = responseExampleIdentifier(example)
    const draft = responseExampleDrafts[id]
    if (!draft) return
    await runAction('update response example', async () => {
      state = await UpdateResponseExample(activeCollection.id, activeRequest.id, id, draft)
      editingResponseExampleDetailsID = ''
      const { [id]: _saved, ...remainingDrafts } = responseExampleDrafts
      responseExampleDrafts = remainingDrafts
      responseTab = 'examples'
    })
  }

  async function loadResponseExampleCode(example: main.ResponseExample, language = responseExampleCodeLanguage) {
    if (!activeCollection || !activeRequest) return
    await runAction('generate response example code', async () => {
      responseExampleGeneratedCode = await GenerateResponseExampleCode(
        activeCollection.id,
        activeRequest.id,
        responseExampleIdentifier(example),
        language
      )
    })
  }

  async function beginGenerateResponseExampleCode(example: main.ResponseExample) {
    generatingResponseExampleID = responseExampleIdentifier(example)
    responseExampleCodeLanguage = 'curl'
    responseExampleGeneratedCode = ''
    await loadResponseExampleCode(example, responseExampleCodeLanguage)
  }

  async function changeResponseExampleCodeLanguage(example: main.ResponseExample, language: string) {
    responseExampleCodeLanguage = language
    await loadResponseExampleCode(example, language)
  }

  function closeResponseExampleCode() {
    generatingResponseExampleID = ''
    responseExampleGeneratedCode = ''
  }

  async function copyResponseExampleCode() {
    await copyText(responseExampleGeneratedCode)
  }

  function requestSupportsGenerateCode(item: main.RequestItem | undefined) {
    return item?.type === 'http' || item?.type === 'graphql'
  }

  function requestCodeEnvironmentId(collection: main.Collection) {
    if (collection.id === activeCollection?.id) return selectedEnvironmentId
    return collection.environments?.[0]?.id ?? ''
  }

  async function loadRequestCode(target = requestCodeTarget, language = requestCodeLanguage) {
    if (!target) return
    await runAction('generate request code', async () => {
      requestGeneratedCode = await GenerateRequestCode(target.collectionId, target.itemId, target.environmentId, language)
    })
  }

  async function beginGenerateRequestCode(collection: main.Collection, item: main.RequestItem) {
    if (!requestSupportsGenerateCode(item)) return
    if (!item.url?.trim()) {
      error = 'URL is required'
      return
    }
    const target = {
      collectionId: collection.id,
      itemId: item.id,
      environmentId: requestCodeEnvironmentId(collection)
    }
    requestCodeTarget = target
    requestCodeLanguage = 'curl'
    requestGeneratedCode = ''
    await loadRequestCode(target, requestCodeLanguage)
  }

  async function changeRequestCodeLanguage(language: string) {
    requestCodeLanguage = language
    await loadRequestCode(requestCodeTarget, language)
  }

  function closeRequestCode() {
    requestCodeTarget = undefined
    requestGeneratedCode = ''
  }

  async function copyRequestCode() {
    await copyText(requestGeneratedCode)
  }

  async function beginGenerateGrpcurlCommand() {
    if (!activeCollection || !activeRequest) return
    await runAction('generate grpcurl command', async () => {
      generatedGrpcurlCommand = await GenerateGrpcurlCommand(activeCollection.id, activeRequest.id, selectedEnvironmentId)
    })
  }

  function closeGrpcurlCommand() {
    generatedGrpcurlCommand = ''
  }

  async function copyGrpcurlCommand() {
    await copyText(generatedGrpcurlCommand)
  }

  async function promptValuesForGrpcStreamMessage(index: number | null) {
    if (!activeCollection || !activeRequest) return null
    const message = index === null ? undefined : activeRequest.grpcMessages?.[index]
    const requestForPrompts = {
      ...activeRequest,
      grpcMessages: message ? [message] : []
    } as main.RequestItem
    const promptNames = collectPromptNames(activeCollection, requestForPrompts, selectedEnvironmentId)
    return promptNames.length > 0 ? await promptForVariables(promptNames) : {}
  }

  async function connectActiveGrpcStream() {
    if (!activeCollection || !activeRequest) return
    const promptValues = await promptValuesForGrpcStreamMessage(null)
    if (promptValues === null) return
    await runAction('start gRPC stream', async () => {
      state = Object.keys(promptValues).length > 0
        ? await ConnectGRPCStreamWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
        : await ConnectGRPCStream(activeCollection.id, activeRequest.id, selectedEnvironmentId)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function endActiveGrpcStream() {
    if (!activeCollection || !activeRequest) return
    await runAction('end gRPC stream', async () => {
      state = await EndGRPCStream(activeCollection.id, activeRequest.id)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function cancelActiveGrpcStream() {
    if (!activeCollection || !activeRequest) return
    await runAction('cancel gRPC stream', async () => {
      state = await CancelGRPCStream(activeCollection.id, activeRequest.id)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function sendGrpcStreamMessage(index: number) {
    if (!activeCollection || !activeRequest) return
    const promptValues = await promptValuesForGrpcStreamMessage(index)
    if (promptValues === null) return
    await runAction('send gRPC stream message', async () => {
      const hasPromptValues = Object.keys(promptValues).length > 0
      try {
        state = hasPromptValues
          ? await SendGRPCStreamMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendGRPCStreamMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      } catch (err) {
        if (!String(err).includes('gRPC stream is not connected')) throw err
        state = hasPromptValues
          ? await ConnectGRPCStreamWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
          : await ConnectGRPCStream(activeCollection.id, activeRequest.id, selectedEnvironmentId)
        state = hasPromptValues
          ? await SendGRPCStreamMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendGRPCStreamMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      }
      activeView = 'request'
      responseTab = 'response'
    })
  }

  function selectedWSMessageIndex(request: main.RequestItem) {
    const messages = request.wsMessages ?? []
    if (messages.length === 0) return 0
    const selected = messages.findIndex((message) => message.selected)
    return selected >= 0 ? selected : 0
  }

  async function promptValuesForWebSocketMessage(index: number) {
    if (!activeCollection || !activeRequest) return null
    const messages = (activeRequest.wsMessages ?? []).map((message, messageIndex) => ({
      ...message,
      selected: messageIndex === index
    })) as main.WSMessage[]
    const requestForPrompts = { ...activeRequest, wsMessages: messages } as main.RequestItem
    const promptNames = collectPromptNames(activeCollection, requestForPrompts, selectedEnvironmentId)
    return promptNames.length > 0 ? await promptForVariables(promptNames) : {}
  }

  // US-021/US-022. A new session restarts the backend's event indices at zero,
  // so the previous session's accumulated log has to go with it. Keeping it
  // would leave the log non-contiguous for the whole of the new session and
  // permanently fall back to the response body's trailing window.
  function resetLiveSessionLog(collectionId: string, itemId: string) {
    const key = liveSessionKey(collectionId, itemId)
    if (!(key in liveSessionLogs)) return
    const { [key]: _discarded, ...rest } = liveSessionLogs
    liveSessionLogs = rest
  }

  async function connectActiveWebSocket() {
    if (!activeCollection || !activeRequest) return
    const promptValues = await promptValuesForWebSocketMessage(selectedWSMessageIndex(activeRequest))
    if (promptValues === null) return
    resetLiveSessionLog(activeCollection.id, activeRequest.id)
    await runAction('connect WebSocket', async () => {
      state = Object.keys(promptValues).length > 0
        ? await ConnectWebSocketWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
        : await ConnectWebSocket(activeCollection.id, activeRequest.id, selectedEnvironmentId)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function disconnectActiveWebSocket() {
    if (!activeCollection || !activeRequest) return
    await runAction('disconnect WebSocket', async () => {
      state = await DisconnectWebSocket(activeCollection.id, activeRequest.id)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function sendWSMessage(index: number) {
    if (!activeCollection || !activeRequest) return
    const promptValues = await promptValuesForWebSocketMessage(index)
    if (promptValues === null) return
    await runAction('send WebSocket message', async () => {
      const hasPromptValues = Object.keys(promptValues).length > 0
      try {
        state = hasPromptValues
          ? await SendWebSocketMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendWebSocketMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      } catch (err) {
        if (!String(err).includes('WebSocket is not connected')) throw err
        state = hasPromptValues
          ? await ConnectWebSocketWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
          : await ConnectWebSocket(activeCollection.id, activeRequest.id, selectedEnvironmentId)
        state = hasPromptValues
          ? await SendWebSocketMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendWebSocketMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      }
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function sendRequest() {
    if (!activeCollection || !activeRequest) return
    if (activeRequest.type === 'websocket') {
      await sendWSMessage(selectedWSMessageIndex(activeRequest))
      return
    }
    const collection = activeCollection
    const request = activeRequest
    const environmentId = selectedEnvironmentId
    const tabId = activeTab?.id
    const cancellableTransport = request.type === 'http' || request.type === 'graphql'
    if (cancellableTransport && (busy !== '' || activeHTTPTransport)) return
    const promptNames = collectPromptNames(collection, request, environmentId)
    const promptValues = promptNames.length > 0 ? await promptForVariables(promptNames) : null
    if (promptNames.length > 0 && promptValues === null) return
    if (cancellableTransport && activeHTTPTransport) return
    const collectionId = collection.id
    const requestId = request.id
    // US-035: drain any patch still inside the debounce window, so Send uses
    // what the user actually typed rather than the last flushed value.
    await flushPendingRequestPatch()
    await runAction('send request', async () => {
      if (cancellableTransport) {
        activeHTTPTransport = { collectionId, requestId }
        httpCancellationRequested = false
      }
      try {
        state = promptValues
          ? await SendRequestWithPromptValues(collectionId, requestId, environmentId, promptValues)
          : await SendRequest(collectionId, requestId, environmentId)
        if (state.activeTabId === tabId) {
          activeView = 'request'
          responseTab = 'response'
        }
      } finally {
        if (activeHTTPTransport?.collectionId === collectionId && activeHTTPTransport.requestId === requestId) {
          activeHTTPTransport = undefined
          httpCancellationRequested = false
        }
      }
    })
  }

  async function cancelActiveRequest() {
    if (httpTransportInFlight) {
      await cancelHTTPTransport()
      return
    }
    if (activeHTTPTransport) {
      await cancelHTTPTransport()
      return
    }
    if (activeCollectionRun) {
      await cancelCollectionRun()
      return
    }
    if (activeRequest?.type === 'websocket' && websocketConnected) {
      await disconnectActiveWebSocket()
      return
    }
    if (activeRequest?.type === 'grpc' && grpcStreamConnected) {
      await cancelActiveGrpcStream()
      return
    }
  }

  async function cancelHTTPTransport() {
    const target = activeHTTPTransport
    if (!target || httpCancellationRequested) return
    httpCancellationRequested = true
    try {
      const accepted = await CancelRequest(target.collectionId, target.requestId)
      if (!accepted && activeHTTPTransport?.collectionId === target.collectionId && activeHTTPTransport.requestId === target.requestId) {
        httpCancellationRequested = false
      }
    } catch (err) {
      if (activeHTTPTransport?.collectionId === target.collectionId && activeHTTPTransport.requestId === target.requestId) {
        httpCancellationRequested = false
      }
      error = err instanceof Error ? err.message : String(err)
    }
  }

  function runnerItemIsRunnable(item: main.RequestItem) {
    return !item.type || item.type === 'http' || item.type === 'graphql' || item.type === 'grpc'
  }

  function runnerSelectableItems(collection: main.Collection | undefined) {
    return (collection?.items ?? []).filter(runnerItemIsRunnable)
  }

  function runnerItemSelected(itemId: string) {
    return runnerSelectedItemIds.includes(itemId)
  }

  function setRunnerItemSelected(itemId: string, selected: boolean) {
    if (selected) {
      const ids = new Set(runnerSelectedItemIds)
      ids.add(itemId)
      runnerSelectedItemIds = runnerConfigItems.filter((item) => ids.has(item.id)).map((item) => item.id)
    } else {
      runnerSelectedItemIds = runnerSelectedItemIds.filter((id) => id !== itemId)
    }
  }

  function toggleRunnerSelectAll() {
    if (runnerSelectedCount === runnerConfigItems.length) {
      runnerSelectedItemIds = []
    } else {
      runnerSelectedItemIds = runnerConfigItems.map((item) => item.id)
    }
  }

  function resetRunnerConfiguration() {
    runnerSelectedItemIds = runnerConfigItems.map((item) => item.id)
    runnerDelayMs = 0
  }

  function normalizedRunnerDelayMs(value: number) {
    const delay = Math.floor(Number(value) || 0)
    if (delay < 0) return 0
    if (delay > 600000) return 600000
    return delay
  }

  async function runCollection() {
    if (!activeCollection || activeCollectionRun || busy !== '') return
    const collection = activeCollection
    const selectedItemIds = runnerSelectedItemIds.filter((id) => runnerConfigItems.some((item) => item.id === id))
    if (selectedItemIds.length === 0) return
    const environmentId = selectedEnvironmentId
    const viewAtStart = activeView
    await runAction('run collection', async () => {
      let completedRunState: main.AppState | undefined
      activeCollectionRun = { collectionId: collection.id, collectionName: collection.name || 'collection' }
      collectionRunCancellationRequested = false
      lastCollectionRunCancelled = false
      try {
        completedRunState = await RunCollectionWithOptions(collection.id, environmentId, {
          selectedItemIds,
          delayMs: normalizedRunnerDelayMs(runnerDelayMs)
        } as main.RunnerOptions)
        state = completedRunState
        if (activeView === viewAtStart && activeCollection?.id === collection.id) activeView = 'runner'
      } finally {
        if (activeCollectionRun?.collectionId === collection.id) {
          lastCollectionRunCancelled = collectionRunCancellationRequested || runnerCancellationCount(completedRunState?.runner) > 0
          activeCollectionRun = undefined
          collectionRunCancellationRequested = false
        }
      }
    })
  }

  async function cancelCollectionRun() {
    const target = activeCollectionRun
    if (!target || collectionRunCancellationRequested) return
    collectionRunCancellationRequested = true
    try {
      const accepted = await CancelCollectionRun(target.collectionId)
      if (!accepted && activeCollectionRun?.collectionId === target.collectionId) {
        collectionRunCancellationRequested = false
      }
    } catch (err) {
      if (activeCollectionRun?.collectionId === target.collectionId) {
        collectionRunCancellationRequested = false
      }
      error = err instanceof Error ? err.message : String(err)
    }
  }

  async function createEnvironment() {
    if (!activeCollection) return
    await runAction('create environment', async () => {
      state = await CreateEnvironment(activeCollection.id, environmentName)
      selectedEnvironmentId = activeCollection?.environments?.at(-1)?.id ?? selectedEnvironmentId
      environmentVariableTab = 'variables'
      activeView = 'environments'
    })
  }

  async function createGlobalEnvironment() {
    if (!activeWorkspace) return
    await runAction('create global environment', async () => {
      state = await CreateGlobalEnvironment(activeWorkspace.id, globalEnvironmentName)
      globalEnvironmentVariableTab = 'variables'
      activeView = 'environments'
    })
  }

  async function setActiveGlobalEnvironment(environmentId: string) {
    if (!activeWorkspace) return
    state = await SetActiveGlobalEnvironment(activeWorkspace.id, environmentId)
  }

  async function setActiveWorkspace(workspaceId: string) {
    if (!state || !workspaceId || workspaceId === state.activeWorkspaceId) return
    await runAction('switch workspace', async () => {
      const nextState = await SetActiveWorkspace(workspaceId)
      const workspace = nextState.workspaces?.find((candidate) => candidate.id === workspaceId)
      state = nextState
      selectedCollectionId = workspace?.collections?.[0]?.id ?? ''
      selectedEnvironmentId = workspace?.collections?.[0]?.environments?.[0]?.id ?? ''
    })
  }

  function nativeWindowErrorMessage(prefix: string, err: unknown) {
    const detail = err instanceof Error ? err.message : String(err)
    return detail && detail !== '[object Object]' ? `${prefix}: ${detail}` : prefix
  }

  async function openNativeNewWindow() {
    if (nativeWindowBusy) return
    nativeWindowBusy = true
    nativeWindowError = ''
    try {
      await OpenNewWindow()
    } catch (err) {
      nativeWindowError = nativeWindowErrorMessage('Couldn’t open a new window', err)
    } finally {
      nativeWindowBusy = false
    }
  }

  async function showWorkspaceWindowPicker() {
    if (workspaceWindowPickerBusy) return
    workspaceWindowPickerOpen = true
    workspaceWindowPickerBusy = true
    workspaceWindowPickerBusyAction = 'loading'
    workspaceWindowPickerError = ''
    workspaceWindowTargets = []
    try {
      workspaceWindowTargets = await ListWorkspaceWindowTargets()
    } catch (err) {
      workspaceWindowPickerError = nativeWindowErrorMessage('Couldn’t load available workspaces', err)
    } finally {
      workspaceWindowPickerBusy = false
      workspaceWindowPickerBusyAction = ''
    }
  }

  function closeWorkspaceWindowPicker() {
    workspaceWindowPickerOpen = false
    workspaceWindowPickerError = ''
  }

  async function openSelectedWorkspaceInNewWindow(target: main.WorkspaceWindowTarget) {
    if (workspaceWindowPickerBusy || target.id === state?.activeWorkspaceId) return
    workspaceWindowPickerBusy = true
    workspaceWindowPickerBusyAction = 'opening'
    workspaceWindowPickerError = ''
    try {
      await OpenWorkspaceInNewWindow(target.id)
      workspaceWindowPickerOpen = false
    } catch (err) {
      workspaceWindowPickerError = nativeWindowErrorMessage(`Couldn’t open ${target.name || 'the workspace'}`, err)
    } finally {
      workspaceWindowPickerBusy = false
      workspaceWindowPickerBusyAction = ''
    }
  }

  async function createWorkspaceForNewWindow(name: string) {
    const trimmedName = name.trim()
    if (!trimmedName || workspaceWindowPickerBusy) return
    if (workspaceWindowTargets.some((target) => target.name.trim().toLocaleLowerCase() === trimmedName.toLocaleLowerCase())) {
      workspaceWindowPickerError = `A workspace named “${trimmedName}” already exists.`
      return
    }

    const existingTargetIds = new Set(workspaceWindowTargets.map((target) => target.id))
    let workspaceCreated = false
    workspaceWindowPickerBusy = true
    workspaceWindowPickerBusyAction = 'creating'
    workspaceWindowPickerError = ''
    try {
      await CreateWorkspace(trimmedName)
      workspaceCreated = true
      const refreshedTargets = await ListWorkspaceWindowTargets()
      workspaceWindowTargets = refreshedTargets
      const newlyRegistered = refreshedTargets.filter((target) => !existingTargetIds.has(target.id))
      const normalizedName = trimmedName.toLocaleLowerCase()
      const createdTarget = newlyRegistered.find((target) => target.name.trim().toLocaleLowerCase() === normalizedName)
        ?? (newlyRegistered.length === 1 ? newlyRegistered[0] : undefined)
        ?? refreshedTargets.find((target) => target.id !== state?.activeWorkspaceId && target.name.trim().toLocaleLowerCase() === normalizedName)
      if (!createdTarget) {
        workspaceWindowPickerError = 'The workspace was created, but it isn’t available in the window list yet. Close and reopen the picker to refresh.'
        return
      }
      return createdTarget
    } catch (err) {
      workspaceWindowPickerError = nativeWindowErrorMessage(
        workspaceCreated ? 'The workspace was created, but the window list couldn’t be refreshed' : 'Couldn’t create the workspace',
        err
      )
    } finally {
      workspaceWindowPickerBusy = false
      workspaceWindowPickerBusyAction = ''
    }
  }

  async function updateGlobalEnvironmentMetadata(field: 'name' | 'color', value: string) {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const name = field === 'name' ? value : selectedGlobalEnvironment.name
    const color = field === 'color' ? value : selectedGlobalEnvironment.color
    state = await UpdateGlobalEnvironment(activeWorkspace.id, selectedGlobalEnvironment.id, name, color)
  }

  function dotEnvFileKey(file: Pick<main.DotEnvFile, 'scope' | 'name'>) {
    return `${file.scope}:${file.name}`
  }

  function parseDotEnvRows(content: string): DotEnvRow[] {
    return content.split('\n').flatMap((line, lineIndex) => {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#')) return []
      const exported = trimmed.startsWith('export ') ? trimmed.slice(7).trimStart() : trimmed
      const equalIndex = exported.indexOf('=')
      if (equalIndex <= 0) return []
      return [{
        lineIndex,
        name: exported.slice(0, equalIndex).trim(),
        value: exported.slice(equalIndex + 1).trim()
      }]
    })
  }

  function setDotEnvLine(lineIndex: number, name: string, value: string) {
    const lines = dotEnvContent.split('\n')
    lines[lineIndex] = `${name}=${value}`
    dotEnvContent = lines.join('\n')
    dotEnvDirty = true
  }

  function updateDotEnvRow(row: DotEnvRow, field: 'name' | 'value', value: string) {
    setDotEnvLine(row.lineIndex, field === 'name' ? value : row.name, field === 'value' ? value : row.value)
  }

  function addDotEnvRow() {
    const prefix = dotEnvContent === '' || dotEnvContent.endsWith('\n') ? '' : '\n'
    dotEnvContent = `${dotEnvContent}${prefix}NEW_VAR=`
    dotEnvDirty = true
  }

  function removeDotEnvRow(row: DotEnvRow) {
    const lines = dotEnvContent.split('\n')
    lines.splice(row.lineIndex, 1)
    dotEnvContent = lines.join('\n')
    dotEnvDirty = true
  }

  function selectDotEnvFile(file: main.DotEnvFile) {
    selectedDotEnvKey = dotEnvFileKey(file)
    dotEnvScope = file.scope
    dotEnvName = file.name
    dotEnvContent = file.content ?? ''
    dotEnvDirty = false
  }

  function newDotEnvFile() {
    selectedDotEnvKey = ''
    dotEnvName = '.env'
    dotEnvContent = ''
    dotEnvDirty = false
  }

  async function loadDotEnvFiles(force = false) {
    if (!activeWorkspace) return
    try {
      const files = await ListDotEnvFiles(activeWorkspace.id, activeCollection?.id ?? '')
      dotEnvFiles = files
      const selected = files.find((file) => dotEnvFileKey(file) === selectedDotEnvKey) ?? files[0]
      if (selected) {
        if (force || !dotEnvDirty || selectedDotEnvKey !== dotEnvFileKey(selected)) {
          selectDotEnvFile(selected)
        }
      } else {
        selectedDotEnvKey = ''
        dotEnvName = '.env'
        dotEnvContent = ''
        dotEnvDirty = false
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  async function saveDotEnvFile() {
    if (!activeWorkspace) return
    await runAction('save .env', async () => {
      const files = await SaveDotEnvFile(activeWorkspace.id, activeCollection?.id ?? '', dotEnvScope, dotEnvName, dotEnvContent)
      dotEnvFiles = files
      const key = `${dotEnvScope}:${dotEnvName.trim()}`
      const saved = files.find((file) => dotEnvFileKey(file) === key) ?? files[0]
      if (saved) selectDotEnvFile(saved)
    })
  }

  async function deleteDotEnvFile() {
    if (!activeWorkspace || !selectedDotEnvFile) return
    await runAction('delete .env', async () => {
      const files = await DeleteDotEnvFile(activeWorkspace.id, activeCollection?.id ?? '', selectedDotEnvFile.scope, selectedDotEnvFile.name)
      dotEnvFiles = files
      const next = files[0]
      if (next) {
        selectDotEnvFile(next)
      } else {
        selectedDotEnvKey = ''
        dotEnvName = '.env'
        dotEnvContent = ''
      }
    })
  }

  async function exportCollection() {
    if (!activeCollection) return
    await runAction('export collection', async () => {
      const result = await ExportCollectionWithOptions(activeCollection.id, { format: 'yaml' } as main.CollectionExportOptions)
      exportText = result.content ?? ''
      activeView = 'import'
    })
  }

  function importDecisionFor(row: main.CollectionImportPreviewRow): ImportDecision {
    const current = importDecisions[row.candidateId]
    return current ?? {
      selected: row.defaultSelect && !row.error,
      environments: (row.environments ?? []).map((entry) => entry.selectionId),
      folders: (row.folders ?? []).map((entry) => entry.selectionId),
      requests: (row.requests ?? []).map((entry) => entry.selectionId),
      outputName: row.collectionName ?? '',
      kindOverride: '',
      conflictAction: row.conflict === 'exists' || row.conflict === 'already-open' ? 'rename' : ''
    }
  }

  function updateImportDecision(candidateID: string, update: Partial<ImportDecision>) {
    const row = importPreview?.rows?.find((entry) => entry.candidateId === candidateID)
    if (!row) return
    importDecisions = { ...importDecisions, [candidateID]: { ...importDecisionFor(row), ...update } }
  }

	  $: if (activeView === 'import' && !importDestinationWorkspaceID && activeWorkspace) importDestinationWorkspaceID = activeWorkspace.id

	  function selectImportSourceMode(mode: ImportSourceMode) {
	    importSourceMode = mode
	    importSources = []
	    importPreview = undefined
	    importDecisions = {}
	    importExpanded = {}
	    clearImportAttemptResults()
	  }

	  function clearImportAttemptResults() {
	    importApplyResult = undefined
	    importStatus = ''
	  }

	  async function previewImportSources(sources: main.CollectionImportSource[], focusFirst = false, resetDecisions = false) {
	    if (!importDestinationWorkspaceID || sources.length === 0) return
	    const priorDecisions = importDecisions
	    importSources = []
	    importPreview = undefined
	    importDecisions = {}
	    importExpanded = {}
	    clearImportAttemptResults()
	    let previewSucceeded = false
	    await runAction('preview import', async () => {
	      const preview = await PreviewCollectionImport({ workspaceId: importDestinationWorkspaceID, destinationRoot: importDestinationRoot, sources } as main.CollectionImportPreviewRequest)
      importSources = sources
      importPreview = preview
      const next: Record<string, ImportDecision> = {}
      for (const row of preview.rows ?? []) {
	        const source = sources.find((entry) => entry.id === row.sourceId)
	        const kindOverride = source?.kindOverride ?? ''
	        const prior = resetDecisions ? undefined : priorDecisions[row.candidateId]
	        const compatible = prior && !row.error && prior.kindOverride === kindOverride
	        next[row.candidateId] = compatible ? {
	          ...prior,
	          selected: prior.selected && row.conflict !== 'unavailable',
	          environments: prior.environments.filter((id) => (row.environments ?? []).some((entry) => entry.selectionId === id)),
	          folders: prior.folders.filter((id) => (row.folders ?? []).some((entry) => entry.selectionId === id)),
	          requests: prior.requests.filter((id) => (row.requests ?? []).some((entry) => entry.selectionId === id))
	        } : {
	          selected: row.defaultSelect && !row.error && row.conflict !== 'unavailable',
          environments: (row.environments ?? []).map((entry) => entry.selectionId),
          folders: (row.folders ?? []).map((entry) => entry.selectionId),
          requests: (row.requests ?? []).map((entry) => entry.selectionId),
          outputName: row.collectionName ?? '',
	          kindOverride,
          conflictAction: row.conflict === 'exists' || row.conflict === 'already-open' ? 'rename' : ''
        }
      }
      importDecisions = next
      importStatus = `${(preview.rows ?? []).length} source${(preview.rows ?? []).length === 1 ? '' : 's'} previewed`
	      previewSucceeded = true
      if (focusFirst) await tick().then(() => document.querySelector<HTMLElement>('[data-import-preview-row]')?.focus())
    })
	    if (!previewSucceeded) importStatus = 'Import preview could not be prepared. Check the source and try again.'
  }

  async function previewImportPaths(paths: string[], focusFirst = true) {
    const sources = paths.map((path, index) => ({ id: `path-${Date.now()}-${index}`, path } as main.CollectionImportSource))
    await previewImportSources(sources, focusFirst)
  }

  async function chooseImportFiles() {
    const returnFocus = document.activeElement as HTMLElement | null
    let result: main.CollectionImportPickerResult | undefined
    await runAction('choose import files', async () => { result = await ChooseCollectionImportFiles() })
    if (!result) {
      importStatus = 'File picker could not be opened. Try again.'
      returnFocus?.focus({ preventScroll: true })
      return
    }
    if (result.cancelled) {
      importStatus = 'File selection cancelled'
      returnFocus?.focus({ preventScroll: true })
      return
    }
    await previewImportPaths(result.paths ?? [])
  }

  async function chooseImportFolder() {
    const returnFocus = document.activeElement as HTMLElement | null
    let result: main.CollectionImportPickerResult | undefined
    await runAction('choose import folder', async () => { result = await ChooseCollectionImportFolder() })
    if (!result) {
      importStatus = 'Folder picker could not be opened. Try again.'
      returnFocus?.focus({ preventScroll: true })
      return
    }
    if (result.cancelled) {
      importStatus = 'Folder selection cancelled'
      returnFocus?.focus({ preventScroll: true })
      return
    }
    await previewImportPaths(result.paths ?? [])
  }

  async function previewURLImport() {
    if (!importURL.trim()) return
    await previewImportSources([{ id: `url-${Date.now()}`, url: importURL.trim() } as main.CollectionImportSource], true)
  }

  async function previewPasteImport() {
    if (!importContent.trim()) return
    await previewImportSources([{ id: `paste-${Date.now()}`, name: importPasteName.trim() || 'Pasted import', content: importContent } as main.CollectionImportSource], true)
  }

  async function updateImportOverride(row: main.CollectionImportPreviewRow, kindOverride: string) {
    const source = importSources.find((entry) => entry.id === row.sourceId)
    if (!source) return
    const nextSources = importSources.map((entry) => entry.id === row.sourceId ? { ...entry, kindOverride } as main.CollectionImportSource : entry)
    await previewImportSources(nextSources, false, true)
  }

  function selectedImportIDs(row: main.CollectionImportPreviewRow, kind: 'environments' | 'folders' | 'requests') {
    return importDecisionFor(row)[kind]
  }

  function toggleImportChild(row: main.CollectionImportPreviewRow, kind: 'environments' | 'folders' | 'requests', id: string, checked: boolean) {
    const current = selectedImportIDs(row, kind)
    updateImportDecision(row.candidateId, { [kind]: checked ? [...new Set([...current, id])] : current.filter((entry) => entry !== id) } as Partial<ImportDecision>)
  }

  function importSelectionFor(row: main.CollectionImportPreviewRow): main.CollectionImportSelection {
    const decision = importDecisionFor(row)
    const all = (kind: 'environments' | 'folders' | 'requests', entries: { selectionId: string }[] | undefined) => (entries ?? []).map((entry) => entry.selectionId)
    const filtered = (kind: 'environments' | 'folders' | 'requests', entries: { selectionId: string }[] | undefined) => decision[kind].length !== all(kind, entries).length
    return {
      sourceId: row.sourceId,
      candidateId: row.candidateId,
      expectedContentHash: row.contentHash,
      environmentIds: decision.environments,
      folderIds: decision.folders,
      requestIds: decision.requests,
      outputName: decision.outputName,
      kindOverride: decision.kindOverride,
      conflictAction: decision.conflictAction,
      filterEnvironments: filtered('environments', row.environments),
      filterFolders: filtered('folders', row.folders),
      filterRequests: filtered('requests', row.requests)
    } as main.CollectionImportSelection
  }

  // Keep both dependencies visible to Svelte: changes to a row checkbox must
  // immediately update the footer count and the selections passed to Apply.
  $: importReadyRows = selectedImportRows(importPreview?.rows ?? [], importDecisions)

  function requestPlannedImport() {
    if (importApplyInFlight || !importDestinationWorkspaceID || importReadyRows.length === 0) return
    if (hasReplaceImportSelection(importPreview?.rows ?? [], importDecisions)) {
      importReplaceConfirmationReturnFocus = importApplyButton ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null)
      importReplaceConfirmationOpen = true
      void tick().then(() => importReplaceConfirmationCancelButton?.focus({ preventScroll: true }))
      return
    }
    void applyPlannedImport()
  }

  async function cancelImportReplaceConfirmation() {
    if (!importReplaceConfirmationOpen) return
    const returnFocus = importReplaceConfirmationReturnFocus
    importReplaceConfirmationOpen = false
    importReplaceConfirmationReturnFocus = null
    await tick()
    if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
  }

  async function confirmImportReplace() {
    if (!importReplaceConfirmationOpen || importApplyInFlight) return
    const returnFocus = importReplaceConfirmationReturnFocus
    importReplaceConfirmationOpen = false
    importReplaceConfirmationReturnFocus = null
    await tick()
    if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
    await applyPlannedImport()
  }

  function handleImportReplaceConfirmationKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      void cancelImportReplaceConfirmation()
      return
    }
    containModalTab(event)
  }

	  async function applyPlannedImport() {
	    if (importApplyInFlight || !importDestinationWorkspaceID || importReadyRows.length === 0) return
	  importApplyInFlight = true
	  clearImportAttemptResults()
	  let applySucceeded = false
    try {
      await runAction('apply import', async () => {
	        const result = await ApplyCollectionImport({ workspaceId: importDestinationWorkspaceID, destinationRoot: importDestinationRoot, sources: importSources, selections: importReadyRows.map(importSelectionFor) } as main.CollectionImportApplyRequest)
        importApplyResult = result
	        state = result.state
        const completed = new Set([...(result.applied ?? []), ...(result.skipped ?? [])].map((row) => row.candidateId))
	        importDecisions = Object.fromEntries(Object.entries(importDecisions).map(([id, decision]) => [id, completed.has(id) ? { ...decision, selected: false } : decision]))
        importStatus = `${result.applied?.length ?? 0} imported, ${result.skipped?.length ?? 0} skipped, ${result.errors?.length ?? 0} errors`
	        applySucceeded = true
      })
    } finally {
	    importApplyInFlight = false
    }
	  if (!applySucceeded) importStatus = 'Import could not be applied. Review the current preview and try again.'
  }

	  function openAPISyncOptions(): main.OpenAPISyncOptions {
	    return {
	      sourceUrl: openAPISyncSourceURL,
      content: openAPISyncContent,
      groupBy: openAPISyncGroupBy,
      preserveValues: openAPISyncPreserveValues,
      removeDeleted: false,
      endpointDecisions: openAPISyncEndpointDecisions
	    } as main.OpenAPISyncOptions
	  }

	  function openAPISyncConfigFor(collection: main.Collection | undefined) {
	    return collection?.openapi?.[0]
	  }

	  function openAPISyncIntervalMinutes(config: main.OpenAPISyncConfig | undefined) {
	    const minutes = Number(config?.autoCheckInterval || 5)
	    return Number.isFinite(minutes) && minutes > 0 ? minutes : 5
	  }

	  function openAPISyncAutoCheckEnabled(config: main.OpenAPISyncConfig | undefined) {
	    return Boolean(config?.sourceUrl && config.autoCheck !== false)
	  }

	  function normalizedOpenAPISyncSettingsInterval(value: number | undefined) {
	    const minutes = Number(value || 5)
	    return openAPISyncCheckIntervals.includes(minutes) ? minutes : 5
	  }

	  function openOpenAPISyncSettings() {
	    if (!activeCollection) return
	    const config = openAPISyncConfigFor(activeCollection)
	    openAPISyncSettingsSourceURL = config?.sourceUrl || openAPISyncSourceURL
	    openAPISyncSettingsAutoCheck = config?.autoCheck !== false
	    openAPISyncSettingsInterval = normalizedOpenAPISyncSettingsInterval(config?.autoCheckInterval)
	    openAPISyncSettingsOpen = true
	  }

	  function cancelOpenAPISyncSettings() {
	    openAPISyncSettingsOpen = false
	  }

	  async function saveOpenAPISyncSettings() {
	    if (!activeCollection) return
	    const collectionId = activeCollection.id
	    const existing = openAPISyncConfigFor(activeCollection)
	    const sourceUrl = openAPISyncSettingsSourceURL.trim()
	    const groupBy = existing?.groupBy || openAPISyncGroupBy || 'tag'
	    await runAction('save OpenAPI sync settings', async () => {
	      state = await UpdateOpenAPISyncConfig(collectionId, {
	        sourceUrl,
	        groupBy,
	        lastSyncDate: existing?.lastSyncDate,
	        specHash: existing?.specHash,
	        autoCheck: openAPISyncSettingsAutoCheck,
	        autoCheckInterval: openAPISyncSettingsInterval
	      } as main.OpenAPISyncConfig)
	      openAPISyncSourceURL = sourceUrl
	      openAPISyncGroupBy = groupBy
	      openAPISyncSettingsOpen = false
	      openAPISyncResult = undefined
	      openAPILocalDriftResult = undefined
	      await checkOpenAPIUpdatesForCollection(collectionId, true)
	    })
	  }

	  async function checkOpenAPIUpdatesForCollection(collectionId: string, force = false) {
	    const now = Date.now()
	    if (!force) {
	      const collection = activeWorkspace?.collections?.find((candidate) => candidate.id === collectionId)
	      const intervalMs = openAPISyncIntervalMinutes(openAPISyncConfigFor(collection)) * 60_000
	      if (now - (openAPISyncAutoCheckLastRun[collectionId] ?? 0) < intervalMs) return
	    }
	    openAPISyncAutoCheckLastRun = { ...openAPISyncAutoCheckLastRun, [collectionId]: now }
	    try {
	      const result = await CheckOpenAPIUpdates(collectionId)
	      openAPISyncUpdateStatus = { ...openAPISyncUpdateStatus, [collectionId]: result }
	      const nextErrors = { ...openAPISyncUpdateErrors }
	      delete nextErrors[collectionId]
	      openAPISyncUpdateErrors = nextErrors
	    } catch (err) {
	      openAPISyncUpdateErrors = {
	        ...openAPISyncUpdateErrors,
	        [collectionId]: err instanceof Error ? err.message : String(err)
	      }
	    }
	  }

	  async function runOpenAPISyncAutoCheck(force = false) {
	    const collections = activeWorkspace?.collections ?? []
	    for (const collection of collections) {
	      const config = openAPISyncConfigFor(collection)
	      if (!config?.sourceUrl) continue
	      if (!force && !openAPISyncAutoCheckEnabled(config)) continue
	      await checkOpenAPIUpdatesForCollection(collection.id, force)
	    }
	  }

	  async function refreshChangedCollectionsQuietly() {
	    if (!state || collectionWatchRefreshInFlight) return
	    collectionWatchRefreshInFlight = true
	    try {
	      const result = await RefreshChangedCollections()
	      if (result.changed) {
	        state = result.state
	      }
	    } catch {
	      // Collection files can be briefly unreadable while external editors are writing them.
	    } finally {
	      collectionWatchRefreshInFlight = false
	    }
	  }

	  function startCollectionWatchPolling() {
	    stopCollectionWatchPolling()
	    collectionWatchPollTimer = window.setInterval(() => {
	      void refreshChangedCollectionsQuietly()
	    }, collectionWatchPollMs)
	  }

	  function stopCollectionWatchPolling() {
	    if (collectionWatchPollTimer === undefined) return
	    window.clearInterval(collectionWatchPollTimer)
	    collectionWatchPollTimer = undefined
	  }

	  function startOpenAPISyncPolling() {
	    stopOpenAPISyncPolling()
	    openAPISyncInitialPollTimer = window.setTimeout(() => {
	      void runOpenAPISyncAutoCheck()
	    }, openAPISyncInitialPollMs)
	    openAPISyncPollTimer = window.setInterval(() => {
	      void runOpenAPISyncAutoCheck()
	    }, openAPISyncGlobalPollMs)
	  }

	  function stopOpenAPISyncPolling() {
	    if (openAPISyncInitialPollTimer) {
	      window.clearTimeout(openAPISyncInitialPollTimer)
	      openAPISyncInitialPollTimer = undefined
	    }
	    if (openAPISyncPollTimer) {
	      window.clearInterval(openAPISyncPollTimer)
	      openAPISyncPollTimer = undefined
	    }
	  }

	  function formatOpenAPISyncCheckedAt(value: string | undefined) {
	    if (!value) return ''
	    const date = new Date(value)
	    if (Number.isNaN(date.getTime())) return value
	    return date.toLocaleTimeString()
	  }

		  function openAPISyncAutoCheckStatusLine(collection: main.Collection | undefined) {
		    const config = openAPISyncConfigFor(collection)
		    if (!config?.sourceUrl) return ''
		    const cadence = openAPISyncAutoCheckEnabled(config)
		      ? `Auto Check for Updates: Every ${openAPISyncIntervalMinutes(config)} min`
	      : 'Auto Check for Updates: Disabled'
	    if (!collection) return cadence
	    if (!openAPISyncAutoCheckEnabled(config)) return cadence
	    const errorMessage = openAPISyncUpdateErrors[collection.id]
	    if (errorMessage) return `${cadence} · Last check failed`
	    const status = openAPISyncUpdateStatus[collection.id]
	    if (!status?.checkedAt) return cadence
	    const checkedAt = formatOpenAPISyncCheckedAt(status.checkedAt)
	    const updateState = status.hasUpdates ? 'Updates found' : 'No updates'
		    return `${cadence} · ${updateState}${checkedAt ? ` ${checkedAt}` : ''}`
		  }

	  function formattedOpenAPISpecContent(content: string | undefined) {
	    const value = content ?? ''
	    if (value.trimStart().startsWith('{')) {
	      try {
	        return JSON.stringify(JSON.parse(value), null, 2)
	      } catch {
	        return value
	      }
	    }
	    return value
	  }

		  function defaultOpenAPISyncDecision(change: main.OpenAPISyncEndpointChange) {
		    return change.defaultDecision || 'accept-incoming'
		  }

  function reconcileOpenAPISyncEndpointDecisions(result: main.OpenAPISyncResult | undefined) {
    const changes = result?.changes ?? []
    if (changes.length === 0) {
      openAPISyncEndpointDecisions = {}
      return
    }
    const next: Record<string, string> = {}
    for (const change of changes) {
      const existing = openAPISyncEndpointDecisions[change.id]
      next[change.id] = existing === 'accept-incoming' || existing === 'keep-mine' ? existing : defaultOpenAPISyncDecision(change)
    }
    openAPISyncEndpointDecisions = next
  }

  function setOpenAPISyncEndpointDecision(id: string, decision: string) {
    openAPISyncEndpointDecisions = { ...openAPISyncEndpointDecisions, [id]: decision }
  }

  function setOpenAPISyncAllEndpointDecisions(decision: string) {
    const changes = openAPISyncResult?.changes ?? []
    const next: Record<string, string> = {}
    for (const change of changes) {
      next[change.id] = decision
    }
    openAPISyncEndpointDecisions = next
  }

  function openAPILocalDriftIDs(changeType: string) {
    return (openAPILocalDriftResult?.changes ?? []).filter((change) => change.change === changeType).map((change) => change.id)
  }

  function openAPILocalDriftLabel(changeType: string) {
    if (changeType === 'missing') return 'deleted'
    if (changeType === 'local-only') return 'added'
    return changeType
  }

  async function checkOpenAPILocalDrift() {
    if (!activeCollection) return
    await runAction('check OpenAPI collection changes', async () => {
      openAPILocalDriftResult = await CheckOpenAPILocalDrift(activeCollection.id)
    })
  }

  async function applyOpenAPILocalDrift(resetIds: string[] = [], restoreIds: string[] = [], deleteIds: string[] = []) {
    if (!activeCollection) return
    if (resetIds.length + restoreIds.length + deleteIds.length === 0) return
    const collectionId = activeCollection.id
    await runAction('apply OpenAPI collection changes', async () => {
      state = await ApplyOpenAPILocalDrift(collectionId, {
        resetIds,
        restoreIds,
        deleteIds
      } as main.OpenAPILocalDriftOptions)
      openAPILocalDriftResult = await CheckOpenAPILocalDrift(collectionId)
    })
  }

  async function resetOpenAPILocalDrift(id?: string) {
    await applyOpenAPILocalDrift(id ? [id] : openAPILocalDriftIDs('modified'), [], [])
  }

  async function restoreOpenAPILocalDrift(id?: string) {
    await applyOpenAPILocalDrift([], id ? [id] : openAPILocalDriftIDs('missing'), [])
  }

  async function deleteOpenAPILocalDrift(id?: string) {
    await applyOpenAPILocalDrift([], [], id ? [id] : openAPILocalDriftIDs('local-only'))
  }

  async function revertAllOpenAPILocalDrift() {
    await applyOpenAPILocalDrift(openAPILocalDriftIDs('modified'), openAPILocalDriftIDs('missing'), openAPILocalDriftIDs('local-only'))
  }

  async function connectOpenAPISync() {
    if (!activeCollection) return
    await runAction('connect OpenAPI sync', async () => {
      state = await ConnectOpenAPISync(activeCollection.id, openAPISyncOptions())
      openAPISyncResult = await CheckOpenAPISync(activeCollection.id, openAPISyncOptions())
      reconcileOpenAPISyncEndpointDecisions(openAPISyncResult)
      openAPILocalDriftResult = undefined
    })
  }

  async function checkOpenAPISync() {
    if (!activeCollection) return
    await runAction('check OpenAPI sync', async () => {
      openAPISyncResult = await CheckOpenAPISync(activeCollection.id, openAPISyncOptions())
      reconcileOpenAPISyncEndpointDecisions(openAPISyncResult)
    })
  }

	  async function applyOpenAPISync() {
	    if (!activeCollection) return
	    await runAction('apply OpenAPI sync', async () => {
	      state = await ApplyOpenAPISync(activeCollection.id, openAPISyncOptions())
	      openAPISyncResult = await CheckOpenAPISync(activeCollection.id, openAPISyncOptions())
      reconcileOpenAPISyncEndpointDecisions(openAPISyncResult)
	      openAPILocalDriftResult = undefined
	    })
	  }

	  async function viewOpenAPISyncSpec() {
	    if (!activeCollection) return
	    const collectionId = activeCollection.id
	    await runAction('view OpenAPI spec', async () => {
	      openAPISpecViewerResult = await GetOpenAPISyncSpec(collectionId)
	      openAPISpecViewerOpen = true
	    })
	  }

	  function closeOpenAPISyncSpecViewer() {
	    openAPISpecViewerOpen = false
	  }

		  async function copyOpenAPISyncSpec() {
		    await copyText(openAPISpecViewerResult?.content ?? '')
		  }

		  async function viewOpenAPISyncSpecDiff() {
		    if (!activeCollection) return
		    const collectionId = activeCollection.id
		    await runAction('view OpenAPI spec diff', async () => {
		      openAPISpecDiffResult = await GetOpenAPISyncSpecDiff(collectionId, openAPISyncOptions())
		      openAPISpecDiffActiveChangeIndex = 0
		      openAPISpecDiffOpen = true
		      await scrollOpenAPISpecDiffChangeIntoView()
		    })
		  }

		  function closeOpenAPISyncSpecDiff() {
		    openAPISpecDiffOpen = false
		    openAPISpecDiffActiveChangeIndex = 0
		  }

		  function openAPISyncSpecDiffSummary(result: main.OpenAPISyncSpecDiffResult | undefined) {
		    if (!result) return ''
		    return `${result.added ?? 0} added · ${result.updated ?? 0} updated · ${result.removed ?? 0} removed · ${result.unchanged ?? 0} unchanged`
		  }

		  async function scrollOpenAPISpecDiffChangeIntoView() {
		    await tick()
		    const lineIndex = openAPISpecDiffChangeLineIndexes[openAPISpecDiffActiveChangeIndex]
		    if (lineIndex === undefined) return
		    document.querySelector<HTMLElement>(`[data-openapi-spec-diff-line-index="${lineIndex}"]`)?.scrollIntoView({ block: 'center', inline: 'nearest' })
		  }

		  function goOpenAPISpecDiffChange(offset: number) {
		    if (openAPISpecDiffChangeCount === 0) return
		    openAPISpecDiffActiveChangeIndex = Math.max(0, Math.min(openAPISpecDiffActiveChangeIndex + offset, openAPISpecDiffChangeCount - 1))
		    void scrollOpenAPISpecDiffChangeIntoView()
		  }

		  function openAPISpecDiffLineIsActive(lineIndex: number, line: main.OpenAPISyncSpecDiffLine) {
		    return (line.kind ?? 'same') !== 'same' && openAPISpecDiffChangeLineIndexes[openAPISpecDiffActiveChangeIndex] === lineIndex
		  }

		  async function openOpenAPILocalDriftRequest(change: main.OpenAPISyncEndpointChange) {
		    if (!activeCollection || !change.itemId) return
		    await openRequestTab(activeCollection.id, change.itemId)
		  }

		  async function disconnectOpenAPISync() {
		    if (!activeCollection) return
		    await runAction('disconnect OpenAPI sync', async () => {
		      state = await DisconnectOpenAPISync(activeCollection.id)
	      openAPISyncResult = undefined
	      openAPISyncSourceURL = ''
	      openAPISyncContent = ''
		      openAPISyncEndpointDecisions = {}
		      openAPILocalDriftResult = undefined
		      openAPISyncSettingsOpen = false
		      openAPISpecViewerOpen = false
		      openAPISpecViewerResult = undefined
		      openAPISpecDiffOpen = false
		      openAPISpecDiffResult = undefined
		      openAPISpecDiffActiveChangeIndex = 0
		    })
		  }

  async function openCollection() {
    if (!activeWorkspace) return
    await runAction('open collection', async () => {
      state = await OpenCollection(activeWorkspace.id, openCollectionPath)
      selectedCollectionId = state.workspaces?.find((workspace) => workspace.id === activeWorkspace.id)?.collections?.at(-1)?.id ?? selectedCollectionId
      activeView = 'request'
    })
  }

  async function refreshCollection() {
    if (!activeCollection) return
    await runAction('refresh collection', async () => {
      state = await RefreshCollection(activeCollection.id)
    })
  }

  async function resetDemoData() {
    await runAction('reset demo data', async () => {
      state = await ResetDemoData()
      selectedEnvironmentId = activeCollection?.environments?.[0]?.id ?? ''
      activeView = 'request'
    })
  }

  async function deleteCookie(cookieId: string) {
    await runAction('delete cookie', async () => {
      state = await DeleteCookie(cookieId)
      if (cookieForm.id === cookieId) cookieForm = emptyCookieForm()
    })
  }

  async function clearCookies() {
    await runAction('clear cookies', async () => {
      state = await ClearCookies()
      cookieForm = emptyCookieForm()
    })
  }

  async function clearDomainCookies(domain: string) {
    await runAction('clear domain cookies', async () => {
      state = await ClearDomainCookies(domain)
      if (cookieForm.domain.toLowerCase() === domain.toLowerCase()) cookieForm = emptyCookieForm()
    })
  }

  async function saveCookieForm() {
    await runAction('save cookie', async () => {
      state = await SaveCookie(cookieForm as unknown as main.CookieInput)
      cookieForm = emptyCookieForm()
    })
  }

  async function importRawCookie() {
    await runAction('import cookie', async () => {
      state = await AddCookieFromHeader(rawCookieHeader, rawCookieURL)
    })
  }

  function editCookie(cookie: main.CookieEntry) {
    cookieForm = {
      id: cookie.id,
      name: cookie.name,
      value: cookie.value,
      domain: cookie.domain,
      path: cookie.path || '/',
      expires: cookie.session ? '' : cookieExpiresInput(cookie),
      session: cookie.session,
      secure: cookie.secure,
      httpOnly: cookie.httpOnly,
      sameSite: cookie.sameSite || '',
      hostOnly: cookie.hostOnly
    }
  }

  function resetCookieForm() {
    cookieForm = emptyCookieForm()
  }

  async function selectRequestPaneTab(tabId: RequestPaneTab) {
    requestPaneTab = tabId
    if (!activeTab) return
    await runAction('update tab pane', async () => {
      const result = await UpdateOpenTabPanesNarrow(activeTab.id, tabId, responseTab)
      await applyNarrow((current, held) => applyTabsMutation(current, held, result))
    })
  }

  function requestTabKeydown(event: KeyboardEvent) {
    const tabs = requestTabs
    const index = tabs.findIndex((tab) => tab.id === requestPaneTab)
    if (index < 0) return
    let next = index
    if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
    else if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length
    else if (event.key === 'Home') next = 0
    else if (event.key === 'End') next = tabs.length - 1
    else return
    event.preventDefault()
    void selectRequestPaneTab(tabs[next].id)
    void tick().then(() => document.querySelector<HTMLButtonElement>(`[data-request-tab="${tabs[next].id}"]`)?.focus())
  }

  async function selectResponsePaneTab(tabId: ResponseTab) {
    responseTab = tabId
    if (!activeTab) return
    await runAction('update tab pane', async () => {
      const result = await UpdateOpenTabPanesNarrow(activeTab.id, requestPaneTab, tabId)
      await applyNarrow((current, held) => applyTabsMutation(current, held, result))
    })
  }

  async function setActiveTab(tabId: string) {
    await flushPendingRequestPatch()
    await runAction('switch tab', async () => {
      const result = await SetActiveTabNarrow(tabId)
      await applyNarrow((current, held) => applyTabsMutation(current, held, result))
      // Read the tab out of the mutation result rather than back out of state:
      // it is the authoritative list the backend just produced, and it is
      // correct even on the path where applyNarrow had to refetch.
      const nextTab = result.openTabs?.find((tab) => tab.id === tabId)
      selectedCollectionId = nextTab?.collectionId ?? selectedCollectionId
      activeView = 'request'
      if (nextTab?.kind === 'response-example') responseTab = 'examples'
    })
  }

  function activeOpenTabIndex() {
    return state?.openTabs?.findIndex((tab) => tab.id === state?.activeTabId) ?? -1
  }

  async function switchToOpenTabAt(index: number) {
    const tab = state?.openTabs?.[index]
    if (!tab) return
    await setActiveTab(tab.id)
  }

  async function switchToRelativeOpenTab(offset: number) {
    const tabs = state?.openTabs ?? []
    if (tabs.length === 0) return
    const activeIndex = activeOpenTabIndex()
    const currentIndex = activeIndex >= 0 ? activeIndex : 0
    const nextIndex = (currentIndex + offset + tabs.length) % tabs.length
    await setActiveTab(tabs[nextIndex].id)
  }

  function lifecycleRequests(): LifecycleRequest[] {
    const requests: LifecycleRequest[] = []
    for (const workspace of state?.workspaces ?? []) {
      for (const collection of workspace.collections ?? []) {
        for (const request of collection.items ?? []) {
          requests.push({
            collectionId: collection.id,
            id: request.id,
            name: request.name,
            draft: request.draft,
            transient: request.transient
          })
        }
      }
    }
    return requests
  }

  function lifecycleTabsFor(action: TabLifecycleAction, targetTabID = ''): LifecycleOpenTab[] {
    if (action === 'close-active') {
      const target = targetTabID ? (state?.openTabs ?? []).find((tab) => tab.id === targetTabID) : activeTab
      return target ? [target] : []
    }
    return state?.openTabs ?? []
  }

  function beginTabLifecycleAction(action: TabLifecycleAction, targetTabID = '') {
    if (tabLifecycleDialog || tabLifecycleDecisionBusy) return
    const tabs = lifecycleTabsFor(action, targetTabID)
    const plan = planUnsavedClose(tabs, lifecycleRequests())
    if (!plan.requiresConfirmation) {
      void completeTabLifecycleAction(action, tabs.map((tab) => tab.id))
      return
    }
    tabLifecycleDialog = {
      action,
      affected: plan.affected,
      targetTabIDs: tabs.map((tab) => tab.id),
      returnFocus: document.activeElement instanceof HTMLElement ? document.activeElement : null
    }
    void tick().then(() => tabLifecycleCancelButton?.focus({ preventScroll: true }))
  }

  async function dismissTabLifecycleDialog() {
    const returnFocus = tabLifecycleDialog?.returnFocus
    tabLifecycleDialog = null
    tabLifecycleDecisionBusy = false
    await tick()
    if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
  }

  function refreshLifecycleDialogAffected(dialog: TabLifecycleDialog) {
    const originalTargets = new Set(dialog.affected.map((request) => `${request.collectionId}\u0000${request.requestId}`))
    const plan = planUnsavedClose(lifecycleTabsFor(dialog.action, dialog.targetTabIDs[0] ?? ''), lifecycleRequests())
    tabLifecycleDialog = {
      ...dialog,
      affected: plan.affected.filter((request) => originalTargets.has(`${request.collectionId}\u0000${request.requestId}`))
    }
  }

  async function discardAndCompleteTabLifecycle() {
    const dialog = tabLifecycleDialog
    if (!dialog || tabLifecycleDecisionBusy) return
    tabLifecycleDecisionBusy = true
    let discarded = false
    await runAction('discard changes', async () => {
      for (const request of dialog.affected) {
        state = await DiscardRequestDraft(request.collectionId, request.requestId)
      }
      discarded = true
    })
    if (!discarded) {
      refreshLifecycleDialogAffected(dialog)
      tabLifecycleDecisionBusy = false
      return
    }
    tabLifecycleDialog = null
    tabLifecycleDecisionBusy = false
    await completeTabLifecycleAction(dialog.action, dialog.targetTabIDs)
  }

  async function saveAndCompleteTabLifecycle() {
    const dialog = tabLifecycleDialog
    if (!dialog || tabLifecycleDecisionBusy) return
    tabLifecycleDecisionBusy = true
    let saved = false
    await runAction('save and close', async () => {
      for (const request of dialog.affected) {
        state = await SaveRequest(request.collectionId, request.requestId)
      }
      saved = true
    })
    if (!saved) {
      refreshLifecycleDialogAffected(dialog)
      tabLifecycleDecisionBusy = false
      return
    }
    tabLifecycleDialog = null
    tabLifecycleDecisionBusy = false
    await completeTabLifecycleAction(dialog.action, dialog.targetTabIDs)
  }

  function handleTabLifecycleDialogKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      void dismissTabLifecycleDialog()
      return
    }
    if (event.key !== 'Tab') return
    const dialog = event.currentTarget as HTMLElement
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(
      'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'
    )).filter((element) => element.offsetParent !== null)
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

  async function completeTabLifecycleAction(action: TabLifecycleAction, targetTabIDs: string[] = []) {
    if (action === 'close-active') {
      await closeActiveTabDirect(targetTabIDs[0])
      return
    }
    if (action === 'close-all') {
      await closeAllOpenTabsDirect()
      return
    }
    closeApplicationDirect()
  }

  async function closeActiveTabDirect(targetTabID = activeTab?.id) {
    const tabID = targetTabID
    if (!tabID || !(state?.openTabs ?? []).some((tab) => tab.id === tabID)) return
    await runAction('close tab', async () => {
      const nextState = await CloseTab(tabID)
      state = nextState
      const nextTab = nextState.openTabs?.find((tab) => tab.id === nextState.activeTabId)
      selectedCollectionId = nextTab?.collectionId ?? selectedCollectionId
      activeView = 'request'
      if (nextTab?.kind === 'response-example') responseTab = 'examples'
    })
  }

  function closeActiveTab() {
    beginTabLifecycleAction('close-active')
  }

  async function closeAllOpenTabsDirect() {
    await runAction('close all tabs', async () => {
      state = await CloseAllTabs()
      activeView = 'request'
    })
  }

  function closeAllOpenTabs() {
    beginTabLifecycleAction('close-all')
  }

  async function reopenLastClosedTab() {
    await runAction('reopen last closed tab', async () => {
      const nextState = await ReopenLastClosedTab(activeTab?.collectionId ?? '')
      state = nextState
      const nextTab = nextState.openTabs?.find((tab) => tab.id === nextState.activeTabId)
      selectedCollectionId = nextTab?.collectionId ?? selectedCollectionId
      activeView = 'request'
      if (nextTab?.kind === 'response-example') responseTab = 'examples'
    })
  }

  async function moveActiveTab(offset: number) {
    if (!activeTab) return
    const tabID = activeTab.id
    await runAction('move tab', async () => {
      const result = await MoveOpenTabNarrow(tabID, offset)
      await applyNarrow((current, held) => applyTabsMutation(current, held, result))
    })
  }

  async function openRequestTab(collectionId: string, itemId: string) {
    await runAction('open request', async () => {
      state = await OpenRequestTab(collectionId, itemId)
      selectedCollectionId = collectionId
      activeView = 'request'
    })
  }

  function responseExampleIsActive(collectionId: string, itemId: string, example: main.ResponseExample) {
    if (!activeTab || activeTab.kind !== 'response-example') return false
    const target = activeTab.exampleId || activeTab.exampleName || ''
    return activeTab.collectionId === collectionId
      && activeTab.itemId === itemId
      && (target === responseExampleIdentifier(example) || target === example.name || activeTab.exampleName === example.name)
  }

  async function openResponseExampleTabFor(collectionId: string, itemId: string, example: main.ResponseExample) {
    await runAction('open response example', async () => {
      state = await OpenResponseExampleTab(collectionId, itemId, responseExampleIdentifier(example))
      selectedCollectionId = collectionId
      activeView = 'request'
      responseTab = 'examples'
    })
  }

  async function openResponseExampleTab(example: main.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    await openResponseExampleTabFor(activeCollection.id, activeRequest.id, example)
  }

  async function connectGitRemote() {
    if (!activeCollection) return
    await runAction('connect git remote', async () => {
      state = await ConnectCollectionGitRemote(activeCollection.id, gitRemoteURL)
      selectedCollectionId = activeCollection.id
    })
  }

  async function disconnectGitRemote(collectionId = activeCollection?.id ?? '') {
    if (!collectionId) return
    await runAction('remove git remote', async () => {
      state = await DisconnectCollectionGitRemote(collectionId)
      if (selectedCollectionId === collectionId) selectedCollectionId = ''
    })
  }

  function selectCollection(collectionId: string) {
    selectedCollectionId = collectionId
    activeView = 'collection'
  }

  function selectFolderSettings(collection: main.Collection, folderPath: string) {
    selectedCollectionId = collection.id
    selectedFolderPath = collection.folders?.find((folder) => folder.path === folderPath || folder.displayPath === folderPath)?.path ?? folderPath
    collectionTab = 'folders'
    activeView = 'collection'
  }

  function collectionDocsRequestIsExportable(item: main.RequestItem) {
    return !item.type || item.type === 'http' || item.type === 'graphql' || item.type === 'websocket' || item.type === 'grpc'
  }

  function collectionShareUnsupportedTypes(collection: main.Collection | undefined) {
    const labels: string[] = []
    for (const item of collection?.items ?? []) {
      const label = item.type === 'grpc' ? 'gRPC' : item.type === 'websocket' ? 'WebSocket' : ''
      if (label && !labels.includes(label)) labels.push(label)
    }
    return labels
  }

  function formattedCollectionVersion(version: string | undefined) {
    const trimmed = (version || '').trim().replace(/^v/i, '')
    if (!trimmed) return 'v1.0.0'
    const [core, suffix] = trimmed.split('-', 2)
    const parts = core.split('.')
    while (parts.length < 3) parts.push('0')
    if (parts.slice(0, 3).some((part) => !/^\d+$/.test(part))) return 'v1.0.0'
    return `v${parts.slice(0, 3).join('.')}${suffix ? `-${suffix}` : ''}`
  }

  function openShareCollectionModal() {
    shareCollectionFormat = 'zip'
    shareCollectionResult = undefined
    showShareCollectionModal = true
  }

  function cancelShareCollectionModal() {
    showShareCollectionModal = false
    shareCollectionResult = undefined
  }

  function openGenerateDocsModal() {
    generateDocsDeselectedEnvIds = []
    generateDocsResult = undefined
    showGenerateDocsModal = true
  }

  function cancelGenerateDocsModal() {
    showGenerateDocsModal = false
    generateDocsResult = undefined
  }

  function openRenameCollectionModal() {
    if (!activeCollection || activeCollection.notFoundLocally) return
    renameCollectionTarget = activeCollection
    renameCollectionDraft = activeCollection.name
  }

  function cancelRenameCollectionModal() {
    renameCollectionTarget = undefined
    renameCollectionDraft = ''
  }

  async function confirmRenameCollection() {
    if (!renameCollectionTarget || renameCollectionDraft === '') return
    const collectionID = renameCollectionTarget.id
    await runAction('rename collection', async () => {
      state = await RenameCollection(collectionID, renameCollectionDraft)
      selectedCollectionId = collectionID
      renameCollectionTarget = undefined
      renameCollectionDraft = ''
    })
  }

  function openCloneCollectionModal() {
    if (!activeCollection || activeCollection.notFoundLocally) return
    const name = `${activeCollection.name} copy`
    cloneCollectionTarget = activeCollection
    cloneCollectionNameDraft = name
    cloneCollectionFolderDraft = sanitizeCollectionFolderName(name)
    cloneCollectionLocationDraft = cloneCollectionDefaultLocation()
    cloneCollectionFolderEditing = false
  }

  function cancelCloneCollectionModal() {
    cloneCollectionTarget = undefined
    cloneCollectionNameDraft = ''
    cloneCollectionFolderDraft = ''
    cloneCollectionLocationDraft = ''
    cloneCollectionFolderEditing = false
  }

  function updateCloneCollectionName(value: string) {
    cloneCollectionNameDraft = value
    if (!cloneCollectionFolderEditing) {
      cloneCollectionFolderDraft = sanitizeCollectionFolderName(value)
    }
  }

  async function browseCloneCollectionLocation() {
    const location = await SelectDefaultLocation()
    if (location) {
      cloneCollectionLocationDraft = location
    }
  }

  async function confirmCloneCollection() {
    if (!cloneCollectionTarget || cloneCollectionNameDraft === '' || !cloneCollectionLocationDraft || !collectionFolderNameIsValid(cloneCollectionFolderDraft)) return
    const sourceID = cloneCollectionTarget.id
    const previousIDs = new Set((activeWorkspace?.collections ?? []).map((collection) => collection.id))
    await runAction('clone collection', async () => {
      const nextState = await CloneCollection(sourceID, cloneCollectionNameDraft, cloneCollectionFolderDraft, cloneCollectionLocationDraft)
      const cloned = nextState.workspaces
        .flatMap((workspace) => workspace.collections ?? [])
        .find((collection) => !previousIDs.has(collection.id))
      state = nextState
      selectedCollectionId = cloned?.id ?? sourceID
      selectedFolderPath = ''
      cancelCloneCollectionModal()
    })
  }

  function openNewFolderModal(parentPath = '', collection = activeCollection) {
    if (!collection || collection.notFoundLocally) return
    selectedCollectionId = collection.id
    newFolderTarget = collection
    newFolderParentPath = parentPath
    newFolderNameDraft = ''
    newFolderDirectoryDraft = ''
    newFolderDirectoryEditing = false
    newFolderShowFilesystemName = false
  }

  function cancelNewFolderModal() {
    newFolderTarget = undefined
    newFolderParentPath = ''
    newFolderNameDraft = ''
    newFolderDirectoryDraft = ''
    newFolderDirectoryEditing = false
    newFolderShowFilesystemName = false
  }

  function updateNewFolderName(value: string) {
    newFolderNameDraft = value
    if (!newFolderDirectoryEditing) {
      newFolderDirectoryDraft = sanitizeCollectionFolderName(value)
    }
  }

  function newFolderDirectoryIsReservedRoot() {
    return newFolderParentPath === '' && newFolderDirectoryDraft.trim().toLowerCase().includes('environments')
  }

  function newFolderDirectoryNameIsValid() {
    return collectionFolderNameIsValid(newFolderDirectoryDraft) && !newFolderDirectoryIsReservedRoot()
  }

  function newFolderExpectedPath() {
    const directoryName = sanitizeCollectionFolderName(newFolderDirectoryDraft)
    return [newFolderParentPath, directoryName].filter(Boolean).join('/')
  }

  function slashPathBase(value: string | undefined) {
    const parts = (value ?? '').replaceAll('\\', '/').split('/').filter(Boolean)
    return parts.at(-1) ?? ''
  }

  function slashPathParent(value: string | undefined) {
    const parts = (value ?? '').split('/').filter(Boolean)
    parts.pop()
    return parts.join('/')
  }

  function joinSlashPath(parent: string, child: string) {
    return [parent, child].filter(Boolean).join('/')
  }

  function slashPathHasPrefix(value: string | undefined, prefix: string | undefined) {
    const normalizedValue = (value ?? '').split('/').filter(Boolean).join('/')
    const normalizedPrefix = (prefix ?? '').split('/').filter(Boolean).join('/')
    return normalizedValue !== '' && normalizedPrefix !== '' && (normalizedValue === normalizedPrefix || normalizedValue.startsWith(`${normalizedPrefix}/`))
  }

  function collectionRequestExtension(collection: main.Collection) {
    return collection.format === 'yml' || collection.format === 'yaml' ? '.yml' : '.bru'
  }

  function requestInfoFilename(collection: main.Collection, request: main.RequestItem) {
    const fileName = slashPathBase(request.filePath)
    if (fileName) return fileName
    return `${sanitizeCollectionFolderName(request.name || 'Request')}${collectionRequestExtension(collection)}`
  }

  function folderInfoFilename(folder: main.FolderConfig) {
    return slashPathBase(folder.path) || folder.name || slashPathBase(folder.displayPath) || 'folder'
  }

  function itemInfoDisplayName(target: CollectionItemInfoTarget) {
    if (target.kind === 'folder') {
      return target.folder.name || slashPathBase(target.folder.displayPath || target.folder.path)
    }
    return target.request.name || 'Request'
  }

  function itemInfoFilesystemName(target: CollectionItemInfoTarget) {
    if (target.kind === 'folder') {
      return folderInfoFilename(target.folder)
    }
    return requestInfoFilename(target.collection, target.request)
  }

  function openFolderInfoModal(collection: main.Collection, folderPath: string) {
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    selectedCollectionId = collection.id
    itemInfoTarget = { kind: 'folder', collection, folder }
  }

  function openRequestInfoModal(collection: main.Collection, request: main.RequestItem) {
    selectedCollectionId = collection.id
    itemInfoTarget = { kind: 'request', collection, request }
  }

  function closeItemInfoModal() {
    itemInfoTarget = undefined
  }

  async function confirmNewFolder() {
    if (!newFolderTarget || newFolderNameDraft.trim() === '' || !newFolderDirectoryNameIsValid()) return
    const collectionID = newFolderTarget.id
    const expectedPath = newFolderExpectedPath()
    await runAction('new folder', async () => {
      const nextState = await CreateFolder(collectionID, newFolderParentPath, newFolderNameDraft, newFolderDirectoryDraft)
      const nextCollection = nextState.workspaces
        .flatMap((workspace) => workspace.collections ?? [])
        .find((collection) => collection.id === collectionID)
      const createdFolder = (nextCollection?.folders ?? []).find((folder) => folder.path === expectedPath)
      state = nextState
      selectedCollectionId = collectionID
      selectedFolderPath = createdFolder?.path ?? expectedPath
      collectionTab = 'folders'
      activeView = 'collection'
      cancelNewFolderModal()
    })
  }

  function openRenameFolderModal(collection: main.Collection, folderPath: string) {
    if (collection.notFoundLocally) return
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    selectedCollectionId = collection.id
    renameFolderTarget = { collection, folder }
    renameFolderNameDraft = folder.name || slashPathBase(folder.displayPath || folder.path)
    renameFolderDirectoryDraft = sanitizeCollectionFolderName(slashPathBase(folder.path))
    renameFolderDirectoryEditing = false
    renameFolderShowFilesystemName = false
  }

  function cancelRenameFolderModal() {
    renameFolderTarget = undefined
    renameFolderNameDraft = ''
    renameFolderDirectoryDraft = ''
    renameFolderDirectoryEditing = false
    renameFolderShowFilesystemName = false
  }

  function updateRenameFolderName(value: string) {
    renameFolderNameDraft = value
    if (!renameFolderDirectoryEditing) {
      renameFolderDirectoryDraft = sanitizeCollectionFolderName(value)
    }
  }

  function renameFolderDirectoryIsReserved() {
    const value = renameFolderDirectoryDraft.trim().toLowerCase()
    return value === 'collection' || value === 'folder'
  }

  function renameFolderDirectoryNameIsValid() {
    return collectionFolderNameIsValid(renameFolderDirectoryDraft) && !renameFolderDirectoryIsReserved()
  }

  function renamedFolderExpectedDisplayPath() {
    const folder = renameFolderTarget?.folder
    if (!folder) return ''
    return joinSlashPath(slashPathParent(folder.displayPath || folder.path), renameFolderNameDraft.trim())
  }

  async function confirmRenameFolder() {
    if (!renameFolderTarget || renameFolderNameDraft.trim() === '' || !renameFolderDirectoryNameIsValid()) return
    const collectionID = renameFolderTarget.collection.id
    const sourcePath = renameFolderTarget.folder.path
    const expectedDisplayPath = renamedFolderExpectedDisplayPath()
    await runAction('rename folder', async () => {
      const nextState = await RenameFolder(collectionID, sourcePath, renameFolderNameDraft, renameFolderDirectoryDraft)
      const nextCollection = nextState.workspaces
        .flatMap((workspace) => workspace.collections ?? [])
        .find((collection) => collection.id === collectionID)
      const renamedFolder = (nextCollection?.folders ?? []).find((folder) => folder.displayPath === expectedDisplayPath)
      state = nextState
      selectedCollectionId = collectionID
      selectedFolderPath = renamedFolder?.path ?? sourcePath
      collectionTab = 'folders'
      activeView = 'collection'
      cancelRenameFolderModal()
    })
  }

  function openCloneFolderModal(collection: main.Collection, folderPath: string) {
    if (collection.notFoundLocally) return
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    const sourceName = folder.name || slashPathBase(folder.displayPath || folder.path)
    const cloneName = `${sourceName} copy`
    selectedCollectionId = collection.id
    cloneFolderTarget = { collection, folder }
    cloneFolderNameDraft = cloneName
    cloneFolderDirectoryDraft = `${sanitizeCollectionFolderName(sourceName)} copy`
    cloneFolderDirectoryEditing = false
    cloneFolderShowFilesystemName = false
  }

  function cancelCloneFolderModal() {
    cloneFolderTarget = undefined
    cloneFolderNameDraft = ''
    cloneFolderDirectoryDraft = ''
    cloneFolderDirectoryEditing = false
    cloneFolderShowFilesystemName = false
  }

  function updateCloneFolderName(value: string) {
    cloneFolderNameDraft = value
    if (!cloneFolderDirectoryEditing) {
      cloneFolderDirectoryDraft = sanitizeCollectionFolderName(value)
    }
  }

  function cloneFolderDirectoryIsReserved() {
    const value = cloneFolderDirectoryDraft.trim().toLowerCase()
    return value === 'collection' || value === 'folder'
  }

  function cloneFolderDirectoryNameIsValid() {
    return collectionFolderNameIsValid(cloneFolderDirectoryDraft) && !cloneFolderDirectoryIsReserved()
  }

  async function confirmCloneFolder() {
    if (!cloneFolderTarget || cloneFolderNameDraft.trim() === '' || !cloneFolderDirectoryNameIsValid()) return
    const collectionID = cloneFolderTarget.collection.id
    const sourcePath = cloneFolderTarget.folder.path
    await runAction('clone folder', async () => {
      const nextState = await CloneFolder(collectionID, sourcePath, cloneFolderNameDraft, cloneFolderDirectoryDraft)
      state = nextState
      selectedCollectionId = collectionID
      cancelCloneFolderModal()
    })
  }

  function openCloneRequestModal(collection: main.Collection, request: main.RequestItem) {
    if (collection.notFoundLocally) return
    const sourceName = request.name || 'Request'
    selectedCollectionId = collection.id
    cloneRequestTarget = { collection, request }
    cloneRequestNameDraft = `${sourceName} copy`
    cloneRequestFilenameDraft = `${sanitizeCollectionFolderName(sourceName)} copy`
    cloneRequestFilenameEditing = false
    cloneRequestShowFilesystemName = false
  }

  function cancelCloneRequestModal() {
    cloneRequestTarget = undefined
    cloneRequestNameDraft = ''
    cloneRequestFilenameDraft = ''
    cloneRequestFilenameEditing = false
    cloneRequestShowFilesystemName = false
  }

  function updateCloneRequestName(value: string) {
    cloneRequestNameDraft = value
    if (!cloneRequestFilenameEditing) {
      cloneRequestFilenameDraft = sanitizeCollectionFolderName(value)
    }
  }

  function cloneRequestFilenameIsReserved() {
    const value = cloneRequestFilenameDraft.trim().toLowerCase()
    return value === 'collection' || value === 'folder'
  }

  function cloneRequestFilenameIsValid() {
    return collectionFolderNameIsValid(cloneRequestFilenameDraft) && !cloneRequestFilenameIsReserved()
  }

  async function confirmCloneRequest() {
    if (!cloneRequestTarget || cloneRequestNameDraft.trim() === '' || !cloneRequestFilenameIsValid()) return
    const collectionID = cloneRequestTarget.collection.id
    const requestID = cloneRequestTarget.request.id
    await runAction('clone request', async () => {
      const nextState = await CloneRequest(collectionID, requestID, cloneRequestNameDraft, cloneRequestFilenameDraft)
      state = nextState
      selectedCollectionId = collectionID
      activeView = 'request'
      cancelCloneRequestModal()
    })
  }

  function requestFilesystemBaseName(request: main.RequestItem) {
    const filePath = request.filePath ?? ''
    const fileName = slashPathBase(filePath)
    const withoutExtension = fileName.replace(/\.(bru|ya?ml)$/i, '')
    return sanitizeCollectionFolderName(withoutExtension || request.name || 'Request')
  }

  function openRenameRequestModal(collection: main.Collection, request: main.RequestItem) {
    if (collection.notFoundLocally) return
    selectedCollectionId = collection.id
    renameRequestTarget = { collection, request }
    renameRequestNameDraft = request.name || 'Request'
    renameRequestFilenameDraft = requestFilesystemBaseName(request)
    renameRequestFilenameEditing = false
    renameRequestShowFilesystemName = false
  }

  function cancelRenameRequestModal() {
    renameRequestTarget = undefined
    renameRequestNameDraft = ''
    renameRequestFilenameDraft = ''
    renameRequestFilenameEditing = false
    renameRequestShowFilesystemName = false
  }

  function updateRenameRequestName(value: string) {
    renameRequestNameDraft = value
    if (!renameRequestFilenameEditing) {
      renameRequestFilenameDraft = sanitizeCollectionFolderName(value)
    }
  }

  function renameRequestFilenameIsReserved() {
    const value = renameRequestFilenameDraft.trim().toLowerCase()
    return value === 'collection' || value === 'folder'
  }

  function renameRequestFilenameIsValid() {
    return collectionFolderNameIsValid(renameRequestFilenameDraft) && !renameRequestFilenameIsReserved()
  }

  async function confirmRenameRequest() {
    if (!renameRequestTarget || renameRequestNameDraft.trim() === '' || !renameRequestFilenameIsValid()) return
    const collectionID = renameRequestTarget.collection.id
    const requestID = renameRequestTarget.request.id
    await runAction('rename request', async () => {
      const nextState = await RenameRequest(collectionID, requestID, renameRequestNameDraft, renameRequestFilenameDraft)
      state = nextState
      selectedCollectionId = collectionID
      activeView = 'request'
      cancelRenameRequestModal()
    })
  }

  function openDeleteRequestModal(collection: main.Collection, request: main.RequestItem) {
    if (collection.notFoundLocally) return
    selectedCollectionId = collection.id
    deleteRequestTarget = { collection, request }
  }

  function cancelDeleteRequestModal() {
    deleteRequestTarget = undefined
  }

  async function confirmDeleteRequest() {
    if (!deleteRequestTarget) return
    const collectionID = deleteRequestTarget.collection.id
    const requestID = deleteRequestTarget.request.id
    await runAction('delete request', async () => {
      if (requestDeletionAction(deleteRequestTarget!.request) === 'discard-draft') {
        state = await DiscardRequestDraft(collectionID, requestID)
      } else {
        const result = await DeleteRequestRecoverable(collectionID, requestID)
        state = result.state
        await refreshRecoveryEntries()
      }
      selectedCollectionId = collectionID
      activeView = 'request'
      cancelDeleteRequestModal()
    })
  }

  function openDeleteFolderModal(collection: main.Collection, folderPath: string) {
    if (collection.notFoundLocally) return
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    selectedCollectionId = collection.id
    deleteFolderTarget = { collection, folder }
  }

  function cancelDeleteFolderModal() {
    deleteFolderTarget = undefined
  }

  async function confirmDeleteFolder() {
    if (!deleteFolderTarget) return
    const collectionID = deleteFolderTarget.collection.id
    const sourcePath = deleteFolderTarget.folder.path
    await runAction('delete folder', async () => {
      const result = await DeleteFolderRecoverable(collectionID, sourcePath)
      const nextState = result.state
      const nextCollection = nextState.workspaces
        .flatMap((workspace) => workspace.collections ?? [])
        .find((collection) => collection.id === collectionID)
      state = nextState
      await refreshRecoveryEntries()
      selectedCollectionId = collectionID
      if (slashPathHasPrefix(selectedFolderPath, sourcePath)) {
        selectedFolderPath = ''
      }
      collectionTab = (nextCollection?.folders ?? []).length > 0 ? 'folders' : 'overview'
      activeView = 'collection'
      cancelDeleteFolderModal()
    })
  }

  function openRemoveCollectionModal() {
    if (!activeCollection || activeCollection.scratch) return
    removeCollectionTarget = activeCollection
  }

  function cancelRemoveCollectionModal() {
    removeCollectionTarget = undefined
  }

  async function confirmRemoveCollection() {
    if (!removeCollectionTarget) return
    const collectionID = removeCollectionTarget.id
    await runAction('remove collection', async () => {
      const result = await RemoveCollectionRecoverable(collectionID)
      state = result.state
      await refreshRecoveryEntries()
      if (selectedCollectionId === collectionID) selectedCollectionId = ''
      removeCollectionTarget = undefined
      activeView = 'collection'
    })
  }

  function toggleGenerateDocsEnvironment(envId: string, selected: boolean) {
    if (selected) {
      generateDocsDeselectedEnvIds = generateDocsDeselectedEnvIds.filter((id) => id !== envId)
    } else if (!generateDocsDeselectedEnvIds.includes(envId)) {
      generateDocsDeselectedEnvIds = [...generateDocsDeselectedEnvIds, envId]
    }
  }

  function toggleGenerateDocsSelectAll(selected: boolean) {
    generateDocsDeselectedEnvIds = selected ? [] : generateDocsEnvironments.map((env) => env.id)
  }

  function downloadBlob(fileName: string, blob: Blob) {
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = fileName
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  }

  function downloadTextFile(fileName: string, content: string, type: string) {
    downloadBlob(fileName, new Blob([content], { type }))
  }

  function bytesFromBase64(value: string) {
    const binary = atob(value)
    const bytes = new Uint8Array(binary.length)
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
    return bytes
  }

  function downloadCollectionExport(result: main.CollectionExportResult) {
    if (result.contentBase64) {
      downloadBlob(result.filename, new Blob([bytesFromBase64(result.contentBase64)], { type: result.mimeType || 'application/octet-stream' }))
    } else {
      downloadTextFile(result.filename, result.content ?? '', result.mimeType || 'text/plain')
    }
  }

  async function shareCollectionProceed() {
    if (!activeCollection) return
    await runAction('share collection', async () => {
      const result = await ExportCollectionWithOptions(activeCollection.id, {
        format: shareCollectionFormat
      } as main.CollectionExportOptions)
      shareCollectionResult = result
      if (result.content) exportText = result.content
      downloadCollectionExport(result)
      showShareCollectionModal = false
    })
  }

  async function generateCollectionDocs() {
    if (!activeCollection) return
    await runAction('generate documentation', async () => {
      const result = await GenerateCollectionDocs(activeCollection.id, {
        environmentIds: generateDocsSelectedEnvIds
      } as main.GenerateCollectionDocsOptions)
      generateDocsResult = result
      exportText = result.html
      downloadTextFile(result.fileName, result.html, 'text/html')
      showGenerateDocsModal = false
    })
  }

  async function copyText(value: string): Promise<boolean> {
    if (!value) return false
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      return false
    }
  }

  async function saveActiveResponseTimeline() {
    if (!activeCollection || !activeRequest) return
    await runAction('save response timeline', async () => { await SaveResponseTimeline(activeCollection.id, activeRequest.id, '') })
  }

  async function saveActiveResponseBody() {
    if (!activeCollection || !activeRequest) return
    await runAction('save response body', async () => {
      await SaveResponseBody(activeCollection.id, activeRequest.id, '')
    })
  }

  function responseTabKeydown(event: KeyboardEvent) {
    const tabs = activeResponseTabs
    const index = tabs.findIndex((tab) => tab.id === responseTab)
    if (index < 0) return
    let next = index
    if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
    else if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length
    else if (event.key === 'Home') next = 0
    else if (event.key === 'End') next = tabs.length - 1
    else return
    event.preventDefault()
    void selectResponsePaneTab(tabs[next].id)
    void tick().then(() => document.querySelector<HTMLButtonElement>(`[data-response-tab="${tabs[next].id}"]`)?.focus())
  }

  async function checkGitVersion() {
    await runGitAction('check git', async () => {
      gitVersionText = await GitVersion()
    })
  }

  async function scanGitCollections() {
    await runAction('scan git collections', async () => {
      gitCandidates = await ScanGitCollections(gitCloneRoot)
      selectedGitCollectionPaths = gitCandidates.map((candidate) => candidate.path)
    })
  }

  async function cloneGitRepository() {
    gitCloneInProgress = true
    gitCloneProgress = [{ stage: 'queued', message: 'Preparing clone' }]
    gitCloneOutput = ''
    gitCandidates = []
    selectedGitCollectionPaths = []
    await runGitAction('clone git repository', async () => {
      const result = await CloneGitRepository(gitCloneURL, gitCloneRoot, gitCloneName)
      gitVersionText = result.version
      gitCloneOutput = result.output || `Cloned to ${result.targetPath}`
      gitCandidates = result.candidates ?? []
      selectedGitCollectionPaths = gitCandidates.map((candidate) => candidate.path)
    })
    gitCloneInProgress = false
  }

  async function runGitAction(label: string, action: () => Promise<void>) {
    busy = label
    error = ''
    gitNotFoundMessage = ''
    try {
      await action()
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      error = message
      if (isGitMissingError(message)) {
        gitNotFoundMessage = message
      }
    } finally {
      busy = ''
    }
  }

  function applyGitWorkbenchSnapshot(snapshot: main.CollectionGitSnapshot) {
		const previousBranch = gitWorkbenchSnapshot?.branch
    gitWorkbenchSnapshot = snapshot
		if (!snapshot.available) gitNotFoundMessage = 'Git is not installed or not on PATH.'
    gitWorkbenchSelectedPaths = reconcileGitSelection(gitWorkbenchSelectedPaths, snapshot.files ?? [])
    if (!gitWorkbenchBranch || !(snapshot.branches ?? []).includes(gitWorkbenchBranch)) gitWorkbenchBranch = snapshot.branch ?? ''
		gitWorkbenchRemoteBranch = reconcileGitRemoteBranch(gitWorkbenchRemoteBranch, previousBranch, snapshot.branch)
    const selectedRemote = (snapshot.remotes ?? []).find((remote) => remote.name === gitWorkbenchRemoteName) ?? snapshot.remotes?.[0]
    if (selectedRemote) {
      gitWorkbenchRemoteName = selectedRemote.name
      gitWorkbenchRemoteURL = selectedRemote.url
    } else {
      gitWorkbenchRemoteURL = ''
    }
  }

  function selectGitWorkbenchRemote(name: string) {
    gitWorkbenchRemoteName = name
    gitWorkbenchRemoteURL = (gitWorkbenchSnapshot?.remotes ?? []).find((remote) => remote.name === name)?.url ?? ''
  }

  async function refreshGitWorkbench(announce = true) {
    const collection = activeCollection
    if (!collection) {
      gitWorkbenchSnapshot = undefined
      gitWorkbenchCollectionID = ''
      if (announce) gitWorkbenchStatus = 'Select a local collection to use Git.'
      return
    }
    gitWorkbenchLoading = true
    try {
      const snapshot = await GetCollectionGitStatus(collection.id)
      gitWorkbenchCollectionID = collection.id
      applyGitWorkbenchSnapshot(snapshot)
      if (announce) gitWorkbenchStatus = 'Git status refreshed.'
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      if (announce) {
        gitWorkbenchError = message
        gitWorkbenchStatus = 'Git status could not be refreshed.'
      }
      if (isGitMissingError(message)) gitNotFoundMessage = message
    } finally {
      gitWorkbenchLoading = false
    }
  }

  async function openGitWorkbench() {
    gitWorkbenchError = ''
    gitWorkbenchStatus = ''
    gitWorkbenchDiff = undefined
    activeView = 'git'
    await refreshGitWorkbench()
    await tick()
    gitWorkbenchHeading?.focus({ preventScroll: true })
  }

  async function runGitWorkbenchAction(label: string, action: () => Promise<main.CollectionGitOperationResult>) {
    if (!activeCollection) {
      gitWorkbenchStatus = 'Select a local collection before using Git.'
      return
    }
    gitWorkbenchBusy = label
    gitWorkbenchError = ''
    try {
      const result = await action()
      applyGitWorkbenchSnapshot(result.snapshot)
      gitWorkbenchStatus = result.message || `${label} completed.`
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      gitWorkbenchError = message
      gitWorkbenchStatus = `${label} did not complete.`
      if (isGitMissingError(message)) gitNotFoundMessage = message
      await refreshGitWorkbench(false)
    } finally {
      gitWorkbenchBusy = ''
    }
  }

  function toggleGitWorkbenchPath(path: string, selected: boolean) {
    gitWorkbenchSelectedPaths = selected
      ? Array.from(new Set([...gitWorkbenchSelectedPaths, path]))
      : gitWorkbenchSelectedPaths.filter((candidate) => candidate !== path)
  }

  async function initializeGitWorkbench() {
    if (!activeCollection) return
    await runGitWorkbenchAction('Initialize Git', () => InitializeCollectionGit(activeCollection!.id))
  }

  async function viewGitWorkbenchDiff(staged: boolean) {
    if (!activeCollection || gitWorkbenchSelectedPaths.length !== 1) {
      gitWorkbenchError = 'Select exactly one file to view its diff.'
      return
    }
    gitWorkbenchBusy = staged ? 'View staged diff' : 'View unstaged diff'
    gitWorkbenchError = ''
    try {
      gitWorkbenchDiff = await GetCollectionGitDiff(activeCollection.id, gitWorkbenchSelectedPaths[0], staged)
      gitWorkbenchStatus = `${staged ? 'Staged' : 'Unstaged'} diff loaded for ${gitWorkbenchDiff.path}.`
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      gitWorkbenchError = message
      gitWorkbenchStatus = 'Diff could not be loaded.'
      if (isGitMissingError(message)) gitNotFoundMessage = message
      await refreshGitWorkbench(false)
    } finally {
      gitWorkbenchBusy = ''
    }
  }

  async function stageGitWorkbenchSelection() {
    if (!activeCollection) return
    await runGitWorkbenchAction('Stage selected', () => StageCollectionGitPaths(activeCollection!.id, gitWorkbenchSelectedPaths))
  }

  async function unstageGitWorkbenchSelection() {
    if (!activeCollection) return
    await runGitWorkbenchAction('Unstage selected', () => UnstageCollectionGitPaths(activeCollection!.id, gitWorkbenchSelectedPaths))
  }

  async function commitGitWorkbench() {
    if (!activeCollection) return
    if (!gitWorkbenchCommitMessage.trim()) {
      gitWorkbenchError = 'Enter a commit message before committing staged changes.'
      return
    }
    await runGitWorkbenchAction('Commit staged', () => CommitCollectionGit(activeCollection!.id, gitWorkbenchCommitMessage.trim()))
    if (!gitWorkbenchError) gitWorkbenchCommitMessage = ''
  }

  async function createGitWorkbenchBranch() {
    if (!activeCollection) return
    if (!gitWorkbenchNewBranch.trim()) {
      gitWorkbenchError = 'Enter a branch name.'
      return
    }
    await runGitWorkbenchAction('Create branch', () => CreateCollectionGitBranch(activeCollection!.id, gitWorkbenchNewBranch.trim(), gitWorkbenchCheckoutNewBranch))
    if (!gitWorkbenchError) gitWorkbenchNewBranch = ''
  }

  async function checkoutGitWorkbenchBranch() {
    if (!activeCollection || !gitWorkbenchBranch) return
    await runGitWorkbenchAction('Switch branch', () => CheckoutCollectionGitBranch(activeCollection!.id, gitWorkbenchBranch))
  }

  async function setGitWorkbenchRemote() {
    if (!activeCollection || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteURL.trim()) {
      gitWorkbenchError = 'Enter a remote name and credential-free remote URL.'
      return
    }
    gitWorkbenchRemoteName = gitWorkbenchRemoteName.trim()
    gitWorkbenchRemoteURL = gitWorkbenchRemoteURL.trim()
    await runGitWorkbenchAction('Set remote', () => SetCollectionGitRemote(activeCollection!.id, gitWorkbenchRemoteName, gitWorkbenchRemoteURL))
  }

  async function fetchGitWorkbench() {
    if (!activeCollection || !gitWorkbenchRemoteName.trim()) return
    await runGitWorkbenchAction('Fetch', () => FetchCollectionGit(activeCollection!.id, gitWorkbenchRemoteName.trim()))
  }

  async function pullGitWorkbench() {
    if (!activeCollection || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteBranch.trim()) return
    await runGitWorkbenchAction('Pull fast-forward only', () => PullCollectionGit(activeCollection!.id, gitWorkbenchRemoteName.trim(), gitWorkbenchRemoteBranch.trim()))
  }

  async function pushGitWorkbench() {
    if (!activeCollection || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteBranch.trim()) return
    const setUpstream = gitWorkbenchSetUpstream
    await runGitWorkbenchAction('Push', () => PushCollectionGit(activeCollection!.id, gitWorkbenchRemoteName.trim(), gitWorkbenchRemoteBranch.trim(), setUpstream))
    if (!gitWorkbenchError && setUpstream) gitWorkbenchSetUpstream = false
  }

  function isGitMissingError(message: string) {
    const normalized = message.toLowerCase()
    return normalized.includes('git is not installed') || normalized.includes('git --version failed') || normalized.includes('git --version timed out')
  }

  async function openSelectedGitCollections() {
    if (!activeWorkspace) return
    await runAction('open git collections', async () => {
      state = await OpenGitCollections(activeWorkspace.id, selectedGitCollectionPaths, gitCloneURL)
      selectedCollectionId = state.workspaces
        ?.find((workspace) => workspace.id === activeWorkspace.id)
        ?.collections?.find((collection) => selectedGitCollectionPaths.includes(collection.path))?.id ?? selectedCollectionId
      activeView = 'request'
    })
  }

  function toggleGitCandidate(path: string, selected: boolean) {
    selectedGitCollectionPaths = selected
      ? Array.from(new Set([...selectedGitCollectionPaths, path]))
      : selectedGitCollectionPaths.filter((candidatePath) => candidatePath !== path)
  }

  // US-014. Applies a narrow mutator result, or refetches the whole AppState
  // when the revision says we missed an update.
  //
  // `state.revision` is read directly rather than through a `$:` derivation
  // because a reactive statement does not run until Svelte's next update tick,
  // so two mutations dispatched inside one tick would both compare against the
  // same stale revision — making the second look like a gap and refetch for
  // nothing.
  async function applyNarrow(merge: (current: main.AppState, held: number) => MergeOutcome): Promise<void> {
    if (!state) {
      // Nothing to patch onto. Only reachable before the boot fetch has landed.
      state = await GetState()
      return
    }
    const outcome = merge(state, state.revision ?? 0)
    if (outcome.kind === 'applied') {
      state = outcome.state
      return
    }
    // Not silent. A refetch means something mutated state behind our back, and
    // while recovery is automatic, a run of these is a real signal that some
    // mutator still needs migrating.
    console.warn(`[US-014] refetching full state: ${outcome.reason}`)
    state = await GetState()
  }

  // US-035. The keystroke path. US-014 made each round trip 511x smaller
  // (4,348,018 -> 8,515 bytes on the 500-request fixture); this stops most of
  // them happening at all, by coalescing a burst of typing into one call.
  //
  // The backend call is deferred, but the UI is NOT: applyOptimisticPatch below
  // updates local state synchronously, so the input never lags behind the
  // keyboard. The authoritative result overwrites it when the flush lands.
  const requestPatchCoalescer = new PatchCoalescer<main.RequestPatch>(
    async ({ collectionId, itemId }, patch) => {
      const result = await UpdateRequestNarrow(collectionId, itemId, patch)
      await applyNarrow((current, held) => applyRequestMutation(current, held, result))
      // The authoritative result describes the request as of when this call was
      // MADE. Anything typed while it was on the wire is queued but not in it,
      // so applying the result alone rewinds the input and silently drops those
      // characters — measured in the browser as 36 typed becoming
      // "coalesed.example/abcefghij". Re-apply what is still queued on top.
      const stillQueued = requestPatchCoalescer.pendingPatch
      const queuedFor = requestPatchCoalescer.pendingTarget
      if (stillQueued && queuedFor?.collectionId === collectionId && queuedFor.itemId === itemId) {
        applyOptimisticPatch(collectionId, itemId, stillQueued)
      }
      scheduleRequestAutoSave(collectionId, itemId)
    },
  )

  // Applies a patch to the local copy without touching the revision: no server
  // mutation has happened yet, so claiming one would desynchronise US-014's gap
  // detection and make the next real result look like a missed update.
  function applyOptimisticPatch(collectionId: string, itemId: string, patch: main.RequestPatch) {
    if (!state) return
    state = {
      ...state,
      workspaces: state.workspaces.map((workspace) => ({
        ...workspace,
        collections: (workspace.collections ?? []).map((collection) =>
          collection.id !== collectionId
            ? collection
            : {
                ...collection,
                items: (collection.items ?? []).map((item) =>
                  item.id !== itemId ? item : ({ ...item, ...patch, draft: true } as main.RequestItem),
                ),
              },
        ),
      })),
    } as main.AppState
  }

  function patchRequest(patch: main.RequestPatch) {
    if (!activeCollection || !activeRequest) return
    const collectionId = activeCollection.id
    const requestId = activeRequest.id
    applyOptimisticPatch(collectionId, requestId, patch)
    void requestPatchCoalescer.queue({ collectionId, itemId: requestId }, patch)
  }

  // Every path that reads server-side request state has to drain the queue
  // first and WAIT for it, or it races the edit the user just made. A
  // fire-and-forget flush would be worse than none: it looks correct and loses
  // the last keystroke exactly when the user is watching for it.
  function flushPendingRequestPatch(): Promise<void> {
    return requestPatchCoalescer.flush()
  }

  function patchRequestWithURL(url: string) {
    if (!activeRequest) return
    patchRequest({
      url,
      pathParams: syncPathParamsForURL(url, activeRequest.pathParams ?? [])
    } as unknown as main.RequestPatch)
  }

  async function loadGrpcMethods() {
    if (!activeCollection || !activeRequest) return
    await runAction('load gRPC methods', async () => {
      grpcMethods = await ListGRPCMethods(activeCollection.id, activeRequest.id, selectedEnvironmentId)
      grpcMethodMessage = `${grpcMethods.length} method${grpcMethods.length === 1 ? '' : 's'}`
    })
  }

  async function selectGrpcMethod(path: string) {
    if (!activeRequest || !path) return
    const method = grpcMethods.find((candidate) => candidate.path === path)
    const messages = [...(activeRequest.grpcMessages ?? [])]
    const first = messages[0] ?? ({ name: 'message 1', content: '{}' } as main.GrpcMessage)
    const content = method?.template || first.content || '{}'
    messages[0] = { ...first, name: first.name || 'message 1', content } as main.GrpcMessage
    await patchRequest({
      method: path,
      grpcMethodType: method?.type ?? activeRequest.grpcMethodType,
      grpcMessages: messages
    } as main.RequestPatch)
  }

  async function regenerateGrpcMessage(index: number) {
    if (!activeCollection || !activeRequest) return
    const methodPath = activeRequest.method === 'CALL' ? '' : activeRequest.method
    if (!methodPath) return
    await runAction('generate gRPC message', async () => {
      const content = await GenerateGRPCMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, methodPath)
      const rows = [...(activeRequest.grpcMessages ?? [])]
      const current = rows[index] ?? ({ name: `message ${index + 1}`, content: '{}' } as main.GrpcMessage)
      rows[index] = { ...current, name: current.name || `message ${index + 1}`, content } as main.GrpcMessage
      await patchRequest({ grpcMessages: rows } as main.RequestPatch)
    })
  }

  function patchField(field: keyof main.RequestPatch, value: unknown) {
    patchRequest({ [field]: value } as unknown as main.RequestPatch)
  }

	  function updateBody(updates: Partial<main.RequestBody>) {
	    if (!activeRequest) return
	    patchRequest({ body: { ...activeRequest.body, ...updates } } as main.RequestPatch)
	  }

	  function updateFormUrlEncodedRow(index: number, field: keyof main.KeyValue, value: string | boolean) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.formUrlEncoded ?? [])]
	    const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue)
	    rows[index] = { ...current, [field]: value } as main.KeyValue
	    updateBody({ formUrlEncoded: rows } as Partial<main.RequestBody>)
	  }

	  function addFormUrlEncodedRow() {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.formUrlEncoded ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as main.KeyValue]
	    updateBody({ formUrlEncoded: rows } as Partial<main.RequestBody>)
	  }

	  function removeFormUrlEncodedRow(index: number) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.formUrlEncoded ?? [])]
	    rows.splice(index, 1)
	    updateBody({ formUrlEncoded: rows } as Partial<main.RequestBody>)
	  }

	  function updateMultipartRow(index: number, field: keyof main.FormPart, value: string | boolean) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.multipart ?? [])]
	    const current = rows[index] ?? ({ name: '', value: '', filePath: '', contentType: '', enabled: true } as main.FormPart)
	    rows[index] = { ...current, [field]: value } as main.FormPart
	    updateBody({ multipart: rows } as Partial<main.RequestBody>)
	  }

	  function addMultipartRow() {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.multipart ?? []), { name: '', value: '', filePath: '', contentType: '', enabled: true } as main.FormPart]
	    updateBody({ multipart: rows } as Partial<main.RequestBody>)
	  }

	  function removeMultipartRow(index: number) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.multipart ?? [])]
	    rows.splice(index, 1)
	    updateBody({ multipart: rows } as Partial<main.RequestBody>)
	  }

	  function contentTypeForFilePath(filePath: string) {
	    const ext = filePath.trim().toLowerCase().split('?')[0].split('#')[0].match(/\.([a-z0-9]+)$/)?.[1] ?? ''
	    const types: Record<string, string> = {
	      json: 'application/json',
	      txt: 'text/plain; charset=utf-8',
	      text: 'text/plain; charset=utf-8',
	      xml: 'application/xml',
	      csv: 'text/csv; charset=utf-8',
	      html: 'text/html; charset=utf-8',
	      htm: 'text/html; charset=utf-8',
	      css: 'text/css; charset=utf-8',
	      js: 'text/javascript; charset=utf-8',
	      mjs: 'text/javascript; charset=utf-8',
	      png: 'image/png',
	      jpg: 'image/jpeg',
	      jpeg: 'image/jpeg',
	      gif: 'image/gif',
	      svg: 'image/svg+xml',
	      pdf: 'application/pdf',
	      zip: 'application/zip'
	    }
	    return types[ext] ?? ''
	  }

	  function fileBodyRows(body: main.RequestBody | undefined) {
	    const rows = (body?.files?.length ? body.files : body?.filePath || body?.fileContentType ? [{ filePath: body.filePath ?? '', contentType: body.fileContentType ?? '', selected: true } as main.FileBodyEntry] : []).map((row) => ({ ...row }))
	    if (rows.length > 0 && !rows.some((row) => row.selected)) {
	      rows[0].selected = true
	    }
	    return rows
	  }

	  function fileBodyUpdate(rows: main.FileBodyEntry[]) {
	    const selected = rows.find((row) => row.selected) ?? rows[0]
	    updateBody({
	      files: rows,
	      filePath: selected?.filePath ?? '',
	      fileContentType: selected?.contentType ?? ''
	    } as Partial<main.RequestBody>)
	  }

	  function updateFileBodyRow(index: number, field: keyof main.FileBodyEntry, value: string | boolean) {
	    if (!activeRequest) return
	    const rows = fileBodyRows(activeRequest.body)
	    const current = rows[index] ?? ({ filePath: '', contentType: '', selected: rows.length === 0 } as main.FileBodyEntry)
	    rows[index] = { ...current, [field]: value } as main.FileBodyEntry
	    if (field === 'filePath') {
	      rows[index].contentType = contentTypeForFilePath(String(value))
	    }
	    if (field === 'selected' && value === true) {
	      for (let i = 0; i < rows.length; i += 1) {
	        rows[i].selected = i === index
	      }
	    }
	    fileBodyUpdate(rows)
	  }

	  function addFileBodyRow() {
	    if (!activeRequest) return
	    const rows = fileBodyRows(activeRequest.body)
	    rows.push({ filePath: '', contentType: '', selected: rows.length === 0 } as main.FileBodyEntry)
	    fileBodyUpdate(rows)
	  }

	  function removeFileBodyRow(index: number) {
	    if (!activeRequest) return
	    const rows = fileBodyRows(activeRequest.body)
	    const removedSelected = rows[index]?.selected
	    rows.splice(index, 1)
	    if (rows.length > 0 && (removedSelected || !rows.some((row) => row.selected))) {
	      rows[0].selected = true
	    }
	    fileBodyUpdate(rows)
	  }

  function updateAuth(updates: Partial<main.AuthConfig>) {
    if (!activeRequest) return
    patchRequest({ auth: authWithOAuth2Defaults(activeRequest.auth, updates) } as main.RequestPatch)
  }

  function updateAWSV4Auth(updates: Partial<main.AWSV4Auth>) {
    if (!activeRequest) return
    updateAuth({ awsv4: { ...(activeRequest.auth.awsv4 ?? {}), ...updates } as main.AWSV4Auth })
  }

  function updateOAuth1Auth(updates: Partial<main.OAuth1Auth>) {
    if (!activeRequest) return
    updateAuth({ oauth1: { ...(activeRequest.auth.oauth1 ?? {}), ...updates } as main.OAuth1Auth })
  }

  function updateOAuth2Auth(updates: Partial<main.OAuth2Auth>) {
    if (!activeRequest) return
    updateAuth({ oauth2: { ...(activeRequest.auth.oauth2 ?? {}), ...updates } as main.OAuth2Auth })
  }

  function oauth2ParamList(auth: main.OAuth2Auth | undefined, bucket: OAuth2ParamBucket) {
    return [...(((auth ?? {}) as Record<OAuth2ParamBucket, main.OAuth2AdditionalParam[] | undefined>)[bucket] ?? [])]
  }

  function updateOAuth2ParamList(auth: main.OAuth2Auth | undefined, bucket: OAuth2ParamBucket, index: number, sendIn: OAuth2ParamSendIn, field: OAuth2ParamField, value: string | boolean) {
    const rows = oauth2ParamList(auth, bucket)
    const current = rows[index] ?? ({ name: '', value: '', sendIn, enabled: true, secret: false, description: '' } as main.OAuth2AdditionalParam)
    rows[index] = { ...current, sendIn: current.sendIn || sendIn, enabled: current.enabled ?? true, [field]: value } as main.OAuth2AdditionalParam
    return rows
  }

  function addOAuth2ParamList(auth: main.OAuth2Auth | undefined, bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn) {
    return [...oauth2ParamList(auth, bucket), { name: '', value: '', sendIn, enabled: true, secret: false, description: '' } as main.OAuth2AdditionalParam]
  }

  function removeOAuth2ParamList(auth: main.OAuth2Auth | undefined, bucket: OAuth2ParamBucket, index: number) {
    const rows = oauth2ParamList(auth, bucket)
    rows.splice(index, 1)
    return rows
  }

  function updateRequestOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn, index: number, field: OAuth2ParamField, value: string | boolean) {
    if (!activeRequest) return
    updateOAuth2Auth({ [bucket]: updateOAuth2ParamList(activeRequest.auth.oauth2, bucket, index, sendIn, field, value) } as Partial<main.OAuth2Auth>)
  }

  function addRequestOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn) {
    if (!activeRequest) return
    updateOAuth2Auth({ [bucket]: addOAuth2ParamList(activeRequest.auth.oauth2, bucket, sendIn) } as Partial<main.OAuth2Auth>)
  }

  function removeRequestOAuth2AdditionalParam(bucket: OAuth2ParamBucket, index: number) {
    if (!activeRequest) return
    updateOAuth2Auth({ [bucket]: removeOAuth2ParamList(activeRequest.auth.oauth2, bucket, index) } as Partial<main.OAuth2Auth>)
  }

  function updateSettings(updates: Partial<main.RequestSettings>) {
    if (!activeRequest) return
    patchRequest({ settings: { ...activeRequest.settings, ...updates } } as main.RequestPatch)
  }

  function updateKeyValue(kind: 'params' | 'pathParams' | 'headers', index: number, field: keyof main.KeyValue, value: string | boolean) {
    if (!activeRequest) return
    const rows = [...(activeRequest[kind] ?? [])]
    rows[index] = { ...rows[index], [field]: value }
    patchRequest({ [kind]: rows } as unknown as main.RequestPatch)
  }

  function addKeyValue(kind: 'params' | 'headers') {
    if (!activeRequest) return
    const rows = [...(activeRequest[kind] ?? []), { name: '', value: '', enabled: true, secret: false, description: '' }]
    patchRequest({ [kind]: rows } as unknown as main.RequestPatch)
  }

  function removeKeyValue(kind: 'params' | 'headers', index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest[kind] ?? [])]
    rows.splice(index, 1)
    patchRequest({ [kind]: rows } as unknown as main.RequestPatch)
  }

  function updateGrpcMessage(index: number, field: keyof main.GrpcMessage, value: string) {
    if (!activeRequest) return
    const rows = [...(activeRequest.grpcMessages ?? [])]
    const current = rows[index] ?? ({ name: '', content: '' } as main.GrpcMessage)
    rows[index] = { ...current, [field]: value }
    patchRequest({ grpcMessages: rows } as main.RequestPatch)
  }

  function addGrpcMessage() {
    if (!activeRequest) return
    const nextIndex = (activeRequest.grpcMessages?.length ?? 0) + 1
    const rows = [...(activeRequest.grpcMessages ?? []), { name: `message ${nextIndex}`, content: '{}' } as main.GrpcMessage]
    patchRequest({ grpcMessages: rows } as main.RequestPatch)
  }

  function removeGrpcMessage(index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest.grpcMessages ?? [])]
    rows.splice(index, 1)
    patchRequest({ grpcMessages: rows } as main.RequestPatch)
  }

  function updateWSMessage(index: number, field: keyof main.WSMessage, value: string | boolean) {
    if (!activeRequest) return
    const rows = [...(activeRequest.wsMessages ?? [])]
    const current = rows[index] ?? ({ name: `message ${index + 1}`, type: 'text', content: '', selected: false } as main.WSMessage)
    rows[index] = { ...current, [field]: value } as main.WSMessage
    patchRequest({ wsMessages: rows } as main.RequestPatch)
  }

  function addWSMessage() {
    if (!activeRequest) return
    const nextIndex = (activeRequest.wsMessages?.length ?? 0) + 1
    const rows = [...(activeRequest.wsMessages ?? []), { name: `message ${nextIndex}`, type: 'json', content: '{}', selected: true } as main.WSMessage]
    patchRequest({ wsMessages: rows } as main.RequestPatch)
  }

  function removeWSMessage(index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest.wsMessages ?? [])]
    rows.splice(index, 1)
    patchRequest({ wsMessages: rows } as main.RequestPatch)
  }

  function updateAssertion(index: number, field: keyof main.Assertion, value: string | boolean) {
    if (!activeRequest) return
    const rows = [...(activeRequest.assertions ?? [])]
    rows[index] = { ...rows[index], [field]: value }
    patchRequest({ assertions: rows } as main.RequestPatch)
  }

  function addAssertion() {
    if (!activeRequest) return
    const rows = [...(activeRequest.assertions ?? []), { expression: 'res.status', operator: 'equals', value: '200', enabled: true, passed: false, message: '' }]
    patchRequest({ assertions: rows } as main.RequestPatch)
  }

  function removeAssertion(index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest.assertions ?? [])]
    rows.splice(index, 1)
    patchRequest({ assertions: rows } as main.RequestPatch)
  }

  function updateRequestVariable(index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) {
    if (!activeRequest) return
    const req = [...(activeRequest.vars?.req ?? [])]
    req[index] = { ...req[index], [field]: value }
    patchRequest({ vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as main.RequestPatch)
  }

  function addRequestVariable() {
    if (!activeRequest) return
    const req = [
      ...(activeRequest.vars?.req ?? []),
      { id: `req-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: false }
    ]
    patchRequest({ vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as main.RequestPatch)
  }

  function removeRequestVariable(index: number) {
    if (!activeRequest) return
    const req = [...(activeRequest.vars?.req ?? [])]
    req.splice(index, 1)
    patchRequest({ vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as main.RequestPatch)
  }

  async function updateCollectionVariable(index: number, field: keyof main.Variable, value: string | boolean) {
    if (!activeCollection) return
    const vars = [...(activeCollection.variables ?? [])]
    vars[index] = { ...vars[index], [field]: value }
    state = await UpdateCollectionVariables(activeCollection.id, vars)
  }

  async function addCollectionVariable() {
    if (!activeCollection) return
    const vars = [...(activeCollection.variables ?? []), { id: `ui-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: false }]
    state = await UpdateCollectionVariables(activeCollection.id, vars)
  }

  async function updateEnvironmentVariable(index: number, field: keyof main.Variable, value: string | boolean) {
    if (!activeCollection || !selectedEnvironment) return
    const vars = [...(selectedEnvironment.variables ?? [])]
    vars[index] = field === 'dataType' ? { ...vars[index], dataType: String(value), type: String(value) } : { ...vars[index], [field]: value }
    state = await UpdateEnvironmentVariables(activeCollection.id, selectedEnvironment.id, vars)
  }

  async function addEnvironmentVariable() {
    if (!activeCollection || !selectedEnvironment) return
    const vars = [
      ...(selectedEnvironment.variables ?? []),
      { id: `ui-env-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: environmentVariableTab === 'secrets' }
    ]
    state = await UpdateEnvironmentVariables(activeCollection.id, selectedEnvironment.id, vars)
  }

  async function removeEnvironmentVariable(index: number) {
    if (!activeCollection || !selectedEnvironment) return
    const vars = [...(selectedEnvironment.variables ?? [])]
    vars.splice(index, 1)
    state = await UpdateEnvironmentVariables(activeCollection.id, selectedEnvironment.id, vars)
  }

  async function updateGlobalEnvironmentVariable(index: number, field: keyof main.Variable, value: string | boolean) {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const vars = [...(selectedGlobalEnvironment.variables ?? [])]
    vars[index] = field === 'dataType' ? { ...vars[index], dataType: String(value), type: String(value) } : { ...vars[index], [field]: value }
    state = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, selectedGlobalEnvironment.id, vars)
  }

  async function addGlobalEnvironmentVariable() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const vars = [
      ...(selectedGlobalEnvironment.variables ?? []),
      { id: `ui-global-env-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: globalEnvironmentVariableTab === 'secrets' }
    ]
    state = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, selectedGlobalEnvironment.id, vars)
  }

  async function removeGlobalEnvironmentVariable(index: number) {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const vars = [...(selectedGlobalEnvironment.variables ?? [])]
    vars.splice(index, 1)
    state = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, selectedGlobalEnvironment.id, vars)
  }

  async function deleteGlobalEnvironment() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    await runAction('delete global environment', async () => {
      state = await DeleteGlobalEnvironment(activeWorkspace.id, selectedGlobalEnvironment.id)
    })
  }

  async function copyGlobalEnvironment() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    await runAction('copy global environment', async () => {
      const copyName = globalEnvironmentCopyName.trim()
      state = copyName
        ? await CopyGlobalEnvironmentAs(activeWorkspace.id, selectedGlobalEnvironment.id, copyName)
        : await CopyGlobalEnvironment(activeWorkspace.id, selectedGlobalEnvironment.id)
      globalEnvironmentCopyName = ''
    })
  }

  function selectedGlobalEnvironmentExportIds() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return []
    return globalEnvironmentExportAll
      ? (activeWorkspace.globalEnvironments ?? []).map((env) => env.id)
      : [selectedGlobalEnvironment.id]
  }

  async function exportGlobalEnvironment() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    await runAction('export global environment', async () => {
      if (globalEnvironmentExportFormat === 'single-object' && !globalEnvironmentExportAll) {
        globalEnvironmentPayload = await ExportGlobalEnvironment(activeWorkspace.id, selectedGlobalEnvironment.id)
        globalEnvironmentExportFilename = `${selectedGlobalEnvironment.name || 'environment'}.json`
        return
      }
      const ids = selectedGlobalEnvironmentExportIds()
      const result = await ExportGlobalEnvironments(activeWorkspace.id, ids, globalEnvironmentExportFormat)
      globalEnvironmentPayload = result.content || ''
      globalEnvironmentExportFilename = result.filename || ''
    })
  }

  async function saveGlobalEnvironmentExport() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    await runAction('save global environment export', async () => {
      const result = await SaveGlobalEnvironmentExport(
        activeWorkspace.id,
        selectedGlobalEnvironmentExportIds(),
        globalEnvironmentExportFormat,
        globalEnvironmentExportPath.trim()
      )
      globalEnvironmentExportFilename = result.cancelled ? 'Save cancelled' : result.path || result.files?.join(', ') || ''
    })
  }

  async function importGlobalEnvironment() {
    if (!activeWorkspace || !globalEnvironmentPayload.trim()) return
    await runAction('import global environment', async () => {
      state = await ImportGlobalEnvironment(activeWorkspace.id, globalEnvironmentPayload)
    })
  }

  async function updateCollectionHeader(index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) {
    if (!activeCollection) return
    const headers = [...(activeCollection.headers ?? [])]
    headers[index] = { ...headers[index], [field]: value }
    state = await UpdateCollectionHeaders(activeCollection.id, headers)
  }

  async function addCollectionHeader() {
    if (!activeCollection) return
    const headers = [...(activeCollection.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' }]
    state = await UpdateCollectionHeaders(activeCollection.id, headers)
  }

  async function removeCollectionHeader(index: number) {
    if (!activeCollection) return
    const headers = [...(activeCollection.headers ?? [])]
    headers.splice(index, 1)
    state = await UpdateCollectionHeaders(activeCollection.id, headers)
  }

  async function updateCollectionAuth(updates: Partial<main.AuthConfig>) {
    if (!activeCollection) return
    state = await UpdateCollectionAuth(activeCollection.id, authWithOAuth2Defaults(activeCollection.auth, updates))
  }

  function isProxyConfigUnset(proxy: main.ProxyConfig | undefined) {
    if (!proxy) return true
    return !proxy.inherit
      && !proxy.disabled
      && !proxy.protocol
      && !proxy.hostname
      && !proxy.port
      && !proxy.bypassProxy
      && !proxy.auth?.username
      && !proxy.auth?.password
      && !proxy.auth?.disabled
  }

  function normalizedCollectionProxy(overrides: Partial<main.ProxyConfig> = {}) {
    const current = activeCollection?.proxy ?? ({} as main.ProxyConfig)
    const currentAuth = current.auth ?? ({} as main.ProxyAuthConfig)
    const unset = isProxyConfigUnset(current)
    return {
      inherit: unset ? true : (current.inherit ?? true),
      disabled: current.disabled ?? false,
      protocol: current.protocol || 'http',
      hostname: current.hostname || '',
      port: current.port || '',
      bypassProxy: current.bypassProxy || '',
      ...overrides,
      auth: {
        username: currentAuth.username || '',
        password: currentAuth.password || '',
        disabled: currentAuth.disabled ?? false,
        ...(overrides.auth ?? {})
      }
    } as main.ProxyConfig
  }

  function collectionProxyMode(proxy: main.ProxyConfig | undefined) {
    if (isProxyConfigUnset(proxy)) return 'inherit'
    if (proxy?.disabled) return 'off'
    if (proxy?.inherit ?? true) return 'inherit'
    return 'manual'
  }

  async function updateCollectionProxy(updates: Partial<main.ProxyConfig>) {
    if (!activeCollection) return
    state = await UpdateCollectionProxy(activeCollection.id, normalizedCollectionProxy(updates))
  }

  async function updateCollectionProxyMode(mode: string) {
    if (mode === 'manual') {
      await updateCollectionProxy({ inherit: false, disabled: false })
    } else if (mode === 'off') {
      await updateCollectionProxy({ inherit: false, disabled: true })
    } else {
      await updateCollectionProxy({ inherit: true, disabled: false })
    }
  }

  function collectionSandboxMode(collection: main.Collection | undefined = activeCollection): JSSandboxMode {
    return collection?.securityConfig?.jsSandboxMode === 'developer' ? 'developer' : 'safe'
  }

  async function updateCollectionSandboxMode(mode: JSSandboxMode) {
    if (!activeCollection || collectionSandboxMode(activeCollection) === mode) return
    state = await UpdateCollectionSecurityConfig(activeCollection.id, { jsSandboxMode: mode } as main.CollectionSecurityConfig)
  }

  async function updateCollectionProxyAuth(updates: Partial<main.ProxyAuthConfig>) {
    if (!activeCollection) return
    await updateCollectionProxy({ auth: { ...(activeCollection.proxy?.auth ?? {}), ...updates } as main.ProxyAuthConfig })
  }

  function proxyConfigWithDefaults(config: main.ProxyConfig | undefined, overrides: Partial<main.ProxyConfig> = {}) {
    const auth = config?.auth ?? ({} as main.ProxyAuthConfig)
    return {
      inherit: false,
      disabled: false,
      protocol: config?.protocol || 'http',
      hostname: config?.hostname || '',
      port: config?.port || '',
      bypassProxy: config?.bypassProxy || '',
      ...overrides,
      auth: {
        username: auth.username || '',
        password: auth.password || '',
        disabled: auth.disabled ?? false,
        ...(overrides.auth ?? {})
      }
    } as main.ProxyConfig
  }

  function proxyPreferencesWithDefaults(overrides: Partial<main.ProxyPreferences> = {}) {
    const current = state?.preferences?.proxy ?? ({} as main.ProxyPreferences)
    const pac = { source: current.pac?.source || '', ...(overrides.pac ?? {}) }
    const config = proxyConfigWithDefaults(current.config, overrides.config ?? {})
    return {
      disabled: current.disabled ?? false,
      source: current.source || (state?.preferences?.proxyMode === 'pac' ? 'pac' : state?.preferences?.proxyMode === 'manual' ? 'manual' : 'inherit'),
      ...overrides,
      pac,
      config
    } as main.ProxyPreferences
  }

  function preferencesProxyMode(preferences: main.Preferences | undefined) {
    const proxy = preferences?.proxy
    if (proxy?.disabled) return 'off'
    if (proxy?.source === 'pac') return 'pac'
    if (proxy?.source === 'manual') return 'manual'
    return 'inherit'
  }

  function proxyModeLabel(mode: string) {
    if (mode === 'off') return 'Off'
    if (mode === 'manual') return 'On'
    if (mode === 'pac') return 'PAC'
    return 'System Proxy'
  }

  function preferenceProxyModeValue(proxy: main.ProxyPreferences) {
    if (proxy.disabled) return 'off'
    if (proxy.source === 'manual') return 'manual'
    if (proxy.source === 'pac') return 'pac'
    return 'system'
  }

  async function updatePreferencesProxy(updates: Partial<main.ProxyPreferences>) {
    if (!state) return
    const proxy = proxyPreferencesWithDefaults(updates)
    const preferences = {
      ...state.preferences,
      proxy,
      proxyMode: preferenceProxyModeValue(proxy)
    } as main.Preferences
    state = await UpdatePreferences(preferences)
  }

  async function updatePreferencesProxyMode(mode: string) {
    if (mode === 'off') {
      await updatePreferencesProxy({ disabled: true, source: 'manual' })
    } else if (mode === 'manual') {
      await updatePreferencesProxy({ disabled: false, source: 'manual' })
    } else if (mode === 'pac') {
      await updatePreferencesProxy({ disabled: false, source: 'pac' })
    } else {
      await updatePreferencesProxy({ disabled: false, source: 'inherit' })
    }
  }

  async function updatePreferencesProxyConfig(updates: Partial<main.ProxyConfig>) {
    const current = state?.preferences?.proxy?.config
    await updatePreferencesProxy({ config: proxyConfigWithDefaults(current, updates) })
  }

  async function updatePreferencesProxyAuth(updates: Partial<main.ProxyAuthConfig>) {
    const current = state?.preferences?.proxy?.config
    await updatePreferencesProxyConfig({ auth: { ...(current?.auth ?? {}), ...updates } as main.ProxyAuthConfig })
  }

  async function updateAppearancePreferences(updates: Partial<main.Preferences>) {
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      ...updates
    } as main.Preferences)
  }

  async function updateThemeMode(mode: ThemeMode) {
    await updateAppearancePreferences({ theme: mode })
  }

  async function updateThemeVariant(mode: 'light' | 'dark', variant: string) {
    if (mode === 'dark') {
      await updateAppearancePreferences({ themeVariantDark: variant })
    } else {
      await updateAppearancePreferences({ themeVariantLight: variant })
    }
  }

  async function setResponsePaneOrientation(orientation: ResponsePaneOrientation) {
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      layout: {
        ...(state.preferences.layout ?? {}),
        responsePaneOrientation: orientation
      } as main.LayoutPreferences
    } as main.Preferences)
  }

  async function toggleResponsePaneOrientation() {
    await setResponsePaneOrientation(responsePaneOrientation === 'horizontal' ? 'vertical' : 'horizontal')
  }

  async function setZoomPercentage(percentage: number) {
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      display: {
        ...(state.preferences.display ?? {}),
        zoomPercentage: normalizedZoomPercentage(percentage)
      } as main.DisplayPreferences
    } as main.Preferences)
  }

  async function incrementZoomPercentage(delta: number) {
    await setZoomPercentage(appZoomPercentage + delta)
  }

  async function resetZoomPercentage() {
    await setZoomPercentage(zoomDefaultPercentage)
  }

  async function updateFontPreferences(updates: Partial<main.FontPreferences>) {
    if (!state) return
    const nextFont = {
      ...(state.preferences.font ?? {}),
      ...updates
    } as main.FontPreferences
    const nextSize = normalizedCodeFontSize(nextFont.codeFontSize ?? state.preferences.codeFontSize)
    state = await UpdatePreferences({
      ...state.preferences,
      font: {
        ...nextFont,
        codeFont: normalizedCodeFont(nextFont.codeFont),
        codeFontSize: nextSize
      } as main.FontPreferences,
      codeFontSize: nextSize
    } as main.Preferences)
  }

  async function updateCodeFont(value: string) {
    await updateFontPreferences({ codeFont: value })
  }

  async function updateCodeFontSize(value: number) {
    await updateFontPreferences({ codeFontSize: normalizedCodeFontSize(value) })
  }

  async function updateRequestPreferences(updates: Partial<main.RequestPreferences>) {
    requestPreferencesSaveQueue = requestPreferencesSaveQueue.catch(() => {}).then(async () => {
      if (!state) return
      const current = state.preferences.request ?? ({} as main.RequestPreferences)
      const next = {
        sslVerification: updates.sslVerification ?? (current.sslVerification !== false),
        customCaCertificate: {
          enabled: current.customCaCertificate?.enabled ?? false,
          filePath: current.customCaCertificate?.filePath ?? '',
          ...(updates.customCaCertificate ?? {})
        } as main.CustomCaCertificatePreferences,
        keepDefaultCaCertificates: {
          enabled: current.keepDefaultCaCertificates?.enabled !== false,
          ...(updates.keepDefaultCaCertificates ?? {})
        } as main.KeepDefaultCaCertificatesPreferences,
        storeCookies: updates.storeCookies ?? current.storeCookies ?? state.preferences.storeCookies ?? true,
        sendCookies: updates.sendCookies ?? current.sendCookies ?? true,
        timeout: normalizedRequestTimeout(updates.timeout ?? current.timeout)
      } as main.RequestPreferences
      state = await UpdatePreferences({
        ...state.preferences,
        request: next,
        storeCookies: next.storeCookies ?? true
      } as main.Preferences)
    })
    await requestPreferencesSaveQueue
  }

  async function browseCustomCaCertificate() {
    if (!state) return
    const filePath = await SelectCustomCaCertificate()
    if (!filePath) return
    await updateRequestPreferences({
      customCaCertificate: {
        ...(state.preferences.request?.customCaCertificate ?? {}),
        enabled: true,
        filePath
      } as main.CustomCaCertificatePreferences
    })
  }

  async function clearCustomCaCertificate() {
    await updateRequestPreferences({
      customCaCertificate: {
        ...(state?.preferences?.request?.customCaCertificate ?? {}),
        filePath: ''
      } as main.CustomCaCertificatePreferences
    })
  }

  async function updateGeneralPreferences(updates: Partial<main.GeneralPreferences>) {
    if (!state) return
    const next = {
      ...(state.preferences.general ?? {}),
      ...updates
    } as main.GeneralPreferences
    state = await UpdatePreferences({
      ...state.preferences,
      general: next,
      defaultCollectionPath: next.defaultLocation ?? ''
    } as main.Preferences)
  }

  async function browseDefaultLocation() {
    const location = await SelectDefaultLocation()
    if (!location) return
    await updateGeneralPreferences({ defaultLocation: location })
  }

  async function clearDefaultLocation() {
    await updateGeneralPreferences({ defaultLocation: '' })
  }

  async function updateAutoSavePreferences(updates: Partial<main.AutoSavePreferences>) {
    if (!state) return
    const current = state.preferences.autoSave ?? ({} as main.AutoSavePreferences)
    const next = {
      enabled: updates.enabled ?? current.enabled ?? state.preferences.autosave ?? false,
      interval: normalizedAutoSaveInterval(updates.interval ?? current.interval)
    } as main.AutoSavePreferences
    state = await UpdatePreferences({
      ...state.preferences,
      autoSave: next,
      autosave: next.enabled
    } as main.Preferences)
    if (!next.enabled) {
      clearAutoSaveTimer()
    } else if (activeCollection && activeRequest?.draft) {
      scheduleRequestAutoSave(activeCollection.id, activeRequest.id)
    }
  }

  async function updateSSLSessionCache(enabled: boolean) {
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      cache: {
        ...(state.preferences.cache ?? {}),
        sslSession: { enabled } as main.SSLSessionCachePreferences
      } as main.CachePreferences
    } as main.Preferences)
    if (!enabled) {
      state = await ClearSSLSessionCache()
    }
  }

  async function clearSSLSessionCache() {
    state = await ClearSSLSessionCache()
  }

  async function refreshFileCacheSize() {
    try {
      fileCacheSize = await GetFileCacheSize()
    } catch {
      fileCacheSize = undefined
    }
  }

  async function updateFileCache(enabled: boolean) {
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      cache: {
        ...(state.preferences.cache ?? {}),
        file: { enabled } as main.FileCachePreferences
      } as main.CachePreferences
    } as main.Preferences)
    await refreshFileCacheSize()
  }

  async function clearFileCache() {
    fileCacheSize = await ClearFileCache()
  }

  function autoSaveIsEnabled() {
    return state?.preferences?.autoSave?.enabled ?? state?.preferences?.autosave ?? false
  }

  function autoSaveDelay() {
    return normalizedAutoSaveInterval(state?.preferences?.autoSave?.interval)
  }

  function clearAutoSaveTimer() {
    if (!autoSaveTimer) return
    window.clearTimeout(autoSaveTimer)
    autoSaveTimer = undefined
    autoSaveTarget = undefined
  }

  function scheduleRequestAutoSave(collectionId: string, requestId: string) {
    if (!autoSaveIsEnabled()) {
      clearAutoSaveTimer()
      return
    }
    clearAutoSaveTimer()
    autoSaveTarget = { collectionId, requestId }
    autoSaveTimer = window.setTimeout(async () => {
      const target = autoSaveTarget
      clearAutoSaveTimer()
      if (!target) return
      try {
        state = await SaveRequest(target.collectionId, target.requestId)
      } catch (err) {
        error = err instanceof Error ? err.message : String(err)
      }
    }, autoSaveDelay())
  }

  function closeApplicationDirect() {
    try {
      Quit()
    } catch {
      window.close()
    }
  }

  function closeApplication() {
    beginTabLifecycleAction('quit')
  }

  function toggleSidebarCollapse() {
    sidebarCollapsed = !sidebarCollapsed
  }

  function clampSidebarWidth(value: number) {
    return Math.max(220, Math.min(420, Math.round(value)))
  }

  function clampResponseSplit(value: number) {
    return Math.max(0.3, Math.min(0.7, value))
  }

  function persistWorkbenchLayout() {
	  const sidebarKey = workbenchStorageKey('sidebar-width')
	  const splitKey = workbenchStorageKey('response-split')
	  if (!sidebarKey || !splitKey) return
    try {
      localStorage.setItem(sidebarKey, String(sidebarWidth))
      localStorage.setItem(splitKey, String(responseSplit))
    } catch {
      // Layout persistence is an enhancement; a restrictive WebView must not block API work.
    }
  }

  function restoreWorkbenchLayout() {
	  const sidebarKey = workbenchStorageKey('sidebar-width')
	  const splitKey = workbenchStorageKey('response-split')
	  if (!sidebarKey || !splitKey) return
    try {
      const storedSidebar = localStorage.getItem(sidebarKey)
      const storedSplit = localStorage.getItem(splitKey)
      const savedSidebar = storedSidebar === null ? Number.NaN : Number(storedSidebar)
      const savedSplit = storedSplit === null ? Number.NaN : Number(storedSplit)
      if (Number.isFinite(savedSidebar)) sidebarWidth = clampSidebarWidth(savedSidebar)
      if (Number.isFinite(savedSplit)) responseSplit = clampResponseSplit(savedSplit)
    } catch {
      // Use safe defaults when local storage is unavailable or corrupt.
    }
  }

	function workbenchStorageKey(name: 'sidebar-width' | 'response-split') {
	  return workbenchStorageScope ? `liteapi.workbench.v3.${workbenchStorageScope}.${name}` : ''
	}

  function startSidebarResize(event: MouseEvent) {
    event.preventDefault()
    const startX = event.clientX
    const startWidth = sidebarWidth
    const move = (next: MouseEvent) => { sidebarWidth = clampSidebarWidth(startWidth + next.clientX - startX) }
    const finish = () => { persistWorkbenchLayout(); window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', finish) }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', finish)
  }

  function startResponseSplitResize(event: MouseEvent) {
    event.preventDefault()
    const workbench = (event.currentTarget as HTMLElement).closest('.request-workbench')
    const bounds = workbench?.getBoundingClientRect()
    if (!bounds) return
    // Compact CSS always stacks the panes, regardless of the persisted wide-layout preference.
    const isVertical = compactWorkbench || responsePaneOrientation === 'vertical'
    const move = (next: MouseEvent) => {
      const fraction = isVertical ? (next.clientY - bounds.top) / bounds.height : (next.clientX - bounds.left) / bounds.width
      responseSplit = clampResponseSplit(fraction)
    }
    const finish = () => { persistWorkbenchLayout(); window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', finish) }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', finish)
  }

  function openCreationFlow(invoker: HTMLElement | null = document.activeElement instanceof HTMLElement ? document.activeElement : null) {
    creationReturnFocus = invoker
    creationOpen = true
    void tick().then(() => document.querySelector<HTMLInputElement>('[data-new-request-name]')?.focus())
  }

  async function closeCreationFlow() {
    creationOpen = false
    await tick()
    if (creationReturnFocus?.isConnected) creationReturnFocus.focus({ preventScroll: true })
    creationReturnFocus = null
  }

  function containModalTab(event: KeyboardEvent) {
    if (event.key !== 'Tab') return false
    const dialog = event.currentTarget as HTMLElement
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(
      'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'
    )).filter((element) => element.offsetParent !== null)
    if (focusable.length === 0) return false
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
    return true
  }

  function handleCreationDialogKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      void closeCreationFlow()
      return
    }
    containModalTab(event)
  }

  async function submitCreationFlow() {
    if (!requestName.trim()) requestName = 'Untitled request'
    await createRequest()
    await closeCreationFlow()
  }

  async function openKeyboardShortcuts() {
    activeView = 'preferences'
    await tick()
    const disclosure = document.querySelector<HTMLDetailsElement>('details.keybindings-disclosure')
    if (!disclosure) return
    disclosure.open = true
    disclosure.scrollIntoView({ block: 'start' })
    disclosure.querySelector<HTMLElement>('summary')?.focus({ preventScroll: true })
  }

  async function runWorkbenchCommand(command: WorkbenchCommandID, invoker: HTMLElement | null = null) {
    switch (command) {
      case 'new-request':
        openCreationFlow(invoker)
        return
      case 'new-http':
      case 'new-graphql':
      case 'new-grpc':
      case 'new-websocket':
        requestType = command.replace('new-', '')
        requestName = 'New Request'
        openCreationFlow(invoker)
        return
      case 'new-folder':
        if (activeCollection && !activeCollection.notFoundLocally) openNewFolderModal('', activeCollection)
        return
      case 'new-collection':
        await createCollection()
        return
      case 'send-request':
        await sendRequest()
        return
      case 'save-request':
        await saveRequest()
        return
      case 'open-workspace':
        await showWorkspaceWindowPicker()
        return
      case 'workspace-search':
        openGlobalSearch()
        return
      case 'command-palette':
        openCommandPalette(invoker)
        return
      case 'toggle-sidebar':
        toggleSidebarCollapse()
        return
      case 'open-request':
        activeView = 'request'
        return
      case 'open-collection-settings':
        activeView = 'collection'
        return
		case 'open-git-workbench':
			await openGitWorkbench()
			return
      case 'open-environments':
        activeView = 'environments'
        return
      case 'import':
        activeView = 'import'
        return
      case 'open-network':
        activeView = 'network'
        return
      case 'open-cookies':
        activeView = 'cookies'
        return
      case 'toggle-devtools':
        if (devToolsOpen) await closeDevTools()
        else await openDevTools()
        return
      case 'open-capabilities':
        activeView = 'features'
        return
      case 'open-runner':
        activeView = 'runner'
        return
      case 'open-preferences':
        activeView = 'preferences'
        return
      case 'open-keyboard-shortcuts':
        await openKeyboardShortcuts()
        return
      case 'change-orientation':
        await toggleResponsePaneOrientation()
        return
      case 'open-notifications':
        await openNotifications()
        return
      case 'cancel-run':
        await cancelCollectionRun()
    }
  }

  const commandPaletteActions = commandPaletteCommandIDs.map((id) => ({ id, ...workbenchCommandMetadata(id) }))

  $: visibleCommandPaletteActions = commandPaletteActions.filter((action) => action.label.toLowerCase().includes(commandPaletteQuery.trim().toLowerCase()))
  $: if (commandPaletteActiveIndex >= visibleCommandPaletteActions.length) commandPaletteActiveIndex = Math.max(0, visibleCommandPaletteActions.length - 1)

  function openCommandPalette(invoker: HTMLElement | null = document.activeElement instanceof HTMLElement ? document.activeElement : null) {
    commandPaletteReturnFocus = invoker
    commandPaletteOpen = true
    commandPaletteQuery = ''
    commandPaletteActiveIndex = 0
    void tick().then(() => commandPaletteInput?.focus())
  }

  async function closeCommandPalette() {
    commandPaletteOpen = false
    commandPaletteQuery = ''
    await tick()
    if (commandPaletteReturnFocus?.isConnected) commandPaletteReturnFocus.focus({ preventScroll: true })
    commandPaletteReturnFocus = null
  }

  function runCommandPaletteAction(action: typeof commandPaletteActions[number]) {
    const returnFocus = commandPaletteReturnFocus
    commandPaletteOpen = false
    commandPaletteQuery = ''
    commandPaletteReturnFocus = null
    void runWorkbenchCommand(action.id, returnFocus)
  }

  function handleCommandPaletteKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      void closeCommandPalette()
      return
    }
    if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && event.target === commandPaletteInput) {
      if (visibleCommandPaletteActions.length === 0) return
      event.preventDefault()
      const direction = event.key === 'ArrowDown' ? 1 : -1
      commandPaletteActiveIndex = (commandPaletteActiveIndex + direction + visibleCommandPaletteActions.length) % visibleCommandPaletteActions.length
      return
    }
    if (event.key === 'Enter' && event.target === commandPaletteInput && visibleCommandPaletteActions[commandPaletteActiveIndex]) {
      event.preventDefault()
      runCommandPaletteAction(visibleCommandPaletteActions[commandPaletteActiveIndex])
      return
    }
    containModalTab(event)
  }

  async function updateKeybindingsEnabled(enabled: boolean) {
    await updateAppearancePreferences({ keybindingsEnabled: enabled })
  }

  async function updateKeyBinding(action: string, combo: string) {
    if (!state) return
    const os = currentKeyBindingOS()
    const binding = mergedKeyBinding(action)
    if (!binding) return
    const nextBindings = {
      ...(state.preferences.keyBindings ?? {}),
      [action]: {
        ...(state.preferences.keyBindings?.[action] ?? {}),
        name: binding.name,
        [os]: keyBindingSignature(combo)
      } as main.KeyBinding
    }
    await updateAppearancePreferences({ keyBindings: nextBindings })
  }

  async function resetKeyBinding(action: string) {
    if (!state) return
    const nextBindings = { ...(state.preferences.keyBindings ?? {}) }
    delete nextBindings[action]
    await updateAppearancePreferences({ keyBindings: nextBindings })
  }

  async function resetAllKeyBindings() {
    await updateAppearancePreferences({ keyBindings: {} })
  }

  function normalizedDevToolsTab(value: string | undefined): DevToolsTab {
    return devToolsTabs.some((tab) => tab.id === value) ? (value as DevToolsTab) : 'console'
  }

  function normalizedDevToolsDetailsPanelWidth(value: number | undefined) {
    const width = Math.round(Number(value) || 400)
    return Math.max(280, Math.min(800, width))
  }

  function normalizedDevToolsDrawerHeight(value: number | undefined) {
    const height = Math.round(Number(value) || 320)
    return Math.max(220, Math.min(720, height))
  }

  function applyDevToolsShellPreferences(preferences: main.DevToolsPreferences | undefined) {
    devToolsOpen = preferences?.open ?? false
    devToolsTab = normalizedDevToolsTab(preferences?.activeTab)
    devToolsDrawerHeight = normalizedDevToolsDrawerHeight(preferences?.drawerHeight)
    devToolsDetailsPanelWidth = normalizedDevToolsDetailsPanelWidth(preferences?.detailsPanelWidth)
  }

  async function updateDevToolsShellPreferences(updates: Partial<main.DevToolsPreferences>) {
    const next = {
      ...(state?.preferences?.devTools ?? {}),
      ...updates,
      activeTab: normalizedDevToolsTab(updates.activeTab ?? state?.preferences?.devTools?.activeTab ?? devToolsTab),
      drawerHeight: normalizedDevToolsDrawerHeight(updates.drawerHeight ?? state?.preferences?.devTools?.drawerHeight ?? devToolsDrawerHeight),
      detailsPanelWidth: normalizedDevToolsDetailsPanelWidth(updates.detailsPanelWidth ?? state?.preferences?.devTools?.detailsPanelWidth ?? devToolsDetailsPanelWidth),
      network: state?.preferences?.devTools?.network ?? devToolsNetworkPreferencePayload(devToolsNetworkSortKey, devToolsNetworkSortDirection, devToolsNetworkColumnWidths)
    } as main.DevToolsPreferences
    devToolsOpen = next.open ?? false
    devToolsTab = normalizedDevToolsTab(next.activeTab)
    devToolsDrawerHeight = normalizedDevToolsDrawerHeight(next.drawerHeight)
    devToolsDetailsPanelWidth = normalizedDevToolsDetailsPanelWidth(next.detailsPanelWidth)
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      devTools: next
    } as main.Preferences)
  }

  async function recordKeyBinding(action: string, event: KeyboardEvent) {
    if (!keyBindingCanEdit(action) || !keybindingsAreEnabled(state?.preferences)) return
    event.preventDefault()
    event.stopPropagation()
    const combo = keyBindingComboFromEvent(event)
    keybindingDraft = combo
    const validation = validateKeyBinding(action, combo)
    keybindingError = validation
    if (validation) return
    await updateKeyBinding(action, combo)
    recordingKeybindingAction = ''
    keybindingDraft = ''
  }

  function beginRecordKeyBinding(action: string) {
    if (!keyBindingCanEdit(action) || !keybindingsAreEnabled(state?.preferences)) return
    recordingKeybindingAction = action
    keybindingDraft = keyBindingValue(action)
    keybindingError = ''
  }

  function stopRecordKeyBinding(action: string) {
    if (recordingKeybindingAction !== action) return
    recordingKeybindingAction = ''
    keybindingDraft = ''
    keybindingError = ''
  }

  async function updateCollectionClientCertificate(index: number, field: keyof main.ClientCertificateConfig, value: string) {
    if (!activeCollection) return
    const rows = [...(activeCollection.clientCertificates ?? [])]
    rows[index] = { ...rows[index], [field]: value }
    if (field === 'type') {
      if (value === 'pfx') {
        rows[index].certFilePath = ''
        rows[index].keyFilePath = ''
      } else {
        rows[index].pfxFilePath = ''
      }
    }
    state = await UpdateCollectionClientCertificates(activeCollection.id, rows)
  }

  async function updateCollectionPresets(updates: Partial<main.CollectionPresets>) {
    if (!activeCollection) return
    state = await UpdateCollectionPresets(activeCollection.id, { ...(activeCollection.presets ?? {}), ...updates } as main.CollectionPresets)
  }

  async function updateCollectionProtobuf(protobuf: main.CollectionProtobufConfig) {
    if (!activeCollection) return
    state = await UpdateCollectionProtobuf(activeCollection.id, protobuf)
  }

  function collectionProtobufConfig() {
    return activeCollection?.protobuf ?? ({ protoFiles: [], importPaths: [] } as unknown as main.CollectionProtobufConfig)
  }

  function asCollectionProtobufConfig(value: unknown) {
    return value as main.CollectionProtobufConfig
  }

  async function addCollectionClientCertificate() {
    if (!activeCollection) return
    const rows = [
      ...(activeCollection.clientCertificates ?? []),
      { domain: '', type: 'cert', certFilePath: '', keyFilePath: '', pfxFilePath: '', passphrase: '' } as main.ClientCertificateConfig
    ]
    state = await UpdateCollectionClientCertificates(activeCollection.id, rows)
  }

  async function removeCollectionClientCertificate(index: number) {
    if (!activeCollection) return
    const rows = [...(activeCollection.clientCertificates ?? [])]
    rows.splice(index, 1)
    state = await UpdateCollectionClientCertificates(activeCollection.id, rows)
  }

  async function updateCollectionProtoFile(index: number, field: keyof main.CollectionProtoFile, value: string) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.protoFiles ?? [])]
    rows[index] = { ...(rows[index] ?? { path: '', type: 'file' }), [field]: value } as main.CollectionProtoFile
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, protoFiles: rows }))
  }

  async function addCollectionProtoFile() {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    await updateCollectionProtobuf(asCollectionProtobufConfig({
      ...protobuf,
      protoFiles: [...(protobuf.protoFiles ?? []), { path: 'protos/service.proto', type: 'file', exists: false } as main.CollectionProtoFile]
    }))
  }

  async function removeCollectionProtoFile(index: number) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.protoFiles ?? [])]
    rows.splice(index, 1)
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, protoFiles: rows }))
  }

  async function updateCollectionProtoImportPath(index: number, field: keyof main.CollectionProtoImportPath, value: string | boolean) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.importPaths ?? [])]
    rows[index] = { ...(rows[index] ?? { path: '', enabled: true }), [field]: value } as main.CollectionProtoImportPath
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, importPaths: rows }))
  }

  async function addCollectionProtoImportPath() {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    await updateCollectionProtobuf(asCollectionProtobufConfig({
      ...protobuf,
      importPaths: [...(protobuf.importPaths ?? []), { path: 'protos', enabled: true, exists: false } as main.CollectionProtoImportPath]
    }))
  }

  async function removeCollectionProtoImportPath(index: number) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.importPaths ?? [])]
    rows.splice(index, 1)
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, importPaths: rows }))
  }

  async function updateCollectionAWSV4Auth(updates: Partial<main.AWSV4Auth>) {
    if (!activeCollection) return
    await updateCollectionAuth({ awsv4: { ...(activeCollection.auth?.awsv4 ?? {}), ...updates } as main.AWSV4Auth })
  }

  async function updateCollectionOAuth1Auth(updates: Partial<main.OAuth1Auth>) {
    if (!activeCollection) return
    await updateCollectionAuth({ oauth1: { ...(activeCollection.auth?.oauth1 ?? {}), ...updates } as main.OAuth1Auth })
  }

  async function updateCollectionOAuth2Auth(updates: Partial<main.OAuth2Auth>) {
    if (!activeCollection) return
    await updateCollectionAuth({ oauth2: { ...(activeCollection.auth?.oauth2 ?? {}), ...updates } as main.OAuth2Auth })
  }

  async function updateCollectionOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn, index: number, field: OAuth2ParamField, value: string | boolean) {
    if (!activeCollection) return
    await updateCollectionOAuth2Auth({ [bucket]: updateOAuth2ParamList(activeCollection.auth?.oauth2, bucket, index, sendIn, field, value) } as Partial<main.OAuth2Auth>)
  }

  async function addCollectionOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn) {
    if (!activeCollection) return
    await updateCollectionOAuth2Auth({ [bucket]: addOAuth2ParamList(activeCollection.auth?.oauth2, bucket, sendIn) } as Partial<main.OAuth2Auth>)
  }

  async function removeCollectionOAuth2AdditionalParam(bucket: OAuth2ParamBucket, index: number) {
    if (!activeCollection) return
    await updateCollectionOAuth2Auth({ [bucket]: removeOAuth2ParamList(activeCollection.auth?.oauth2, bucket, index) } as Partial<main.OAuth2Auth>)
  }

  async function updateCollectionDocs(value: string) {
    if (!activeCollection) return
    state = await UpdateCollectionDocs(activeCollection.id, value)
  }

  async function updateCollectionScript(field: 'preScript' | 'postScript' | 'tests', value: string) {
    if (!activeCollection) return
    state = await UpdateCollectionScripts(
      activeCollection.id,
      field === 'preScript' ? value : activeCollection.preScript,
      field === 'postScript' ? value : activeCollection.postScript,
      field === 'tests' ? value : activeCollection.tests
    )
  }

  function folderAuthWithDefaults(updates: Partial<main.AuthConfig> = {}) {
    const current = editableFolder?.auth ?? activeFolder?.auth ?? ({} as main.AuthConfig)
    const base = {
      mode: current.mode || '',
      username: current.username || '',
      password: current.password || '',
      domain: current.domain || '',
      token: current.token || '',
      apiKey: current.apiKey || '',
      apiValue: current.apiValue || '',
      apiLocation: current.apiLocation || 'header',
      oauth2: oauth2AuthWithDefaults(current.oauth2),
      oauth1: current.oauth1 ?? ({} as main.OAuth1Auth),
      awsv4: current.awsv4 ?? ({} as main.AWSV4Auth),
      ...updates
    } as main.AuthConfig
    return authWithOAuth2Defaults(base, updates)
  }

  async function saveFolderSettings(updates: Partial<main.FolderConfig>) {
    if (!activeCollection || !activeFolder) return
    const currentFolder = editableFolder ?? activeFolder
    const nextFolder = {
      ...currentFolder,
      headers: currentFolder.headers ?? [],
      variables: currentFolder.variables ?? [],
      resVariables: currentFolder.resVariables ?? [],
      auth: folderAuthWithDefaults(),
      preScript: currentFolder.preScript ?? '',
      postScript: currentFolder.postScript ?? '',
      tests: currentFolder.tests ?? '',
      docs: currentFolder.docs ?? '',
      ...updates
    } as main.FolderConfig
    const targetPath = currentFolder.path
    folderSettingDrafts = { ...folderSettingDrafts, [nextFolder.path]: nextFolder }
    folderSettingsSaveQueue = folderSettingsSaveQueue
      .catch(() => {})
      .then(async () => {
        state = await UpdateFolderSettings(activeCollection.id, targetPath, nextFolder)
        selectedFolderPath = nextFolder.path
        folderSettingDrafts = { ...folderSettingDrafts, [nextFolder.path]: nextFolder }
      })
    await folderSettingsSaveQueue
  }

  async function updateFolderHeader(index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const headers = [...(folder.headers ?? [])]
    headers[index] = { ...headers[index], [field]: value }
    await saveFolderSettings({ headers })
  }

  async function addFolderHeader() {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    await saveFolderSettings({ headers: [...(folder.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' }] })
  }

  async function removeFolderHeader(index: number) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const headers = [...(folder.headers ?? [])]
    headers.splice(index, 1)
    await saveFolderSettings({ headers })
  }

  async function updateFolderVariable(bucket: 'variables' | 'resVariables', index: number, field: keyof main.Variable, value: string | boolean) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const vars = [...(bucket === 'variables' ? folder.variables ?? [] : folder.resVariables ?? [])]
    vars[index] = field === 'dataType' ? { ...vars[index], dataType: String(value), type: String(value) } : { ...vars[index], [field]: value }
    await saveFolderSettings({ [bucket]: vars } as Partial<main.FolderConfig>)
  }

  async function addFolderVariable(bucket: 'variables' | 'resVariables') {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const vars = [...(bucket === 'variables' ? folder.variables ?? [] : folder.resVariables ?? [])]
    vars.push({ id: `ui-folder-var-${Date.now()}`, name: '', value: '', type: 'string', dataType: 'string', enabled: true, secret: false })
    await saveFolderSettings({ [bucket]: vars } as Partial<main.FolderConfig>)
  }

  async function removeFolderVariable(bucket: 'variables' | 'resVariables', index: number) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const vars = [...(bucket === 'variables' ? folder.variables ?? [] : folder.resVariables ?? [])]
    vars.splice(index, 1)
    await saveFolderSettings({ [bucket]: vars } as Partial<main.FolderConfig>)
  }

  async function updateFolderAuth(updates: Partial<main.AuthConfig>) {
    await saveFolderSettings({ auth: folderAuthWithDefaults(updates) })
  }

  async function updateFolderAWSV4Auth(updates: Partial<main.AWSV4Auth>) {
    await updateFolderAuth({ awsv4: { ...(editableFolder?.auth?.awsv4 ?? activeFolder?.auth?.awsv4 ?? {}), ...updates } as main.AWSV4Auth })
  }

  async function updateFolderOAuth1Auth(updates: Partial<main.OAuth1Auth>) {
    await updateFolderAuth({ oauth1: { ...(editableFolder?.auth?.oauth1 ?? activeFolder?.auth?.oauth1 ?? {}), ...updates } as main.OAuth1Auth })
  }

  async function updateFolderOAuth2Auth(updates: Partial<main.OAuth2Auth>) {
    await updateFolderAuth({ oauth2: { ...(editableFolder?.auth?.oauth2 ?? activeFolder?.auth?.oauth2 ?? {}), ...updates } as main.OAuth2Auth })
  }

  async function updateFolderScript(field: 'preScript' | 'postScript' | 'tests', value: string) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    await saveFolderSettings({
      preScript: field === 'preScript' ? value : folder.preScript,
      postScript: field === 'postScript' ? value : folder.postScript,
      tests: field === 'tests' ? value : folder.tests
    })
  }

  async function updateFolderDocs(value: string) {
    await saveFolderSettings({ docs: value })
  }

  function formatResponse(body: string, headers: Record<string, string>) {
    const contentType = Object.entries(headers ?? {}).find(([key]) => key.toLowerCase() === 'content-type')?.[1] ?? ''
    if (contentType.includes('json') || looksLikeJson(body)) {
      try {
        return JSON.stringify(JSON.parse(body), null, 2)
      } catch {
        return body
      }
    }
    return body
  }

  function responseHeaderValue(response: main.Response | undefined, name: string) {
    const headers = response?.headers ?? {}
    const exact = headers[name]
    if (exact !== undefined) return String(exact)
    const match = Object.entries(headers).find(([key]) => key.toLowerCase() === name.toLowerCase())
    return match ? String(match[1]) : ''
  }

  function responseScriptLogs(response: main.Response | undefined): ScriptLog[] {
    return ((response as unknown as { scriptLogs?: ScriptLog[] })?.scriptLogs ?? [])
  }

  function timelineTimestamp(entry: main.TimelineItem) {
    if (!entry.at) return 0
    const value = new Date(entry.at)
    return Number.isNaN(value.getTime()) ? 0 : value.getTime()
  }

  function sortedTimelineEntries(entries: main.TimelineItem[]) {
    return [...entries].sort((left, right) => timelineTimestamp(right) - timelineTimestamp(left))
  }

  function devToolsConsoleLogs(workspace: main.Workspace | undefined): DevToolsConsoleLog[] {
    const rows: DevToolsConsoleLog[] = []
    for (const collection of workspace?.collections ?? []) {
      for (const item of collection.items ?? []) {
        for (const log of responseScriptLogs(item.response)) {
          rows.push({
            ...log,
            collectionName: collection.name,
            requestName: item.name,
            source: [collection.name, item.folderPath, item.name].filter(Boolean).join(' / ')
          })
        }
      }
    }
    return rows
  }

  function normalizedNetworkMethod(row: main.NetworkLog) {
    return (row.method || 'GET').toUpperCase()
  }

  function normalizedDevToolsNetworkSortKey(value: string | undefined): DevToolsNetworkSortKey | '' {
    return devToolsNetworkSortKeys.includes(value as DevToolsNetworkSortKey) ? (value as DevToolsNetworkSortKey) : ''
  }

  function normalizedDevToolsNetworkSortDirection(value: string | undefined): DevToolsNetworkSortDirection {
    return value === 'asc' || value === 'desc' ? value : ''
  }

  function defaultDevToolsNetworkColumnWidths() {
    return [80, 70, 180, 300, 110, 100, 80]
  }

  function normalizedDevToolsNetworkColumnWidths(widths: number[] | undefined) {
    const defaults = defaultDevToolsNetworkColumnWidths()
    if (!widths || widths.length !== defaults.length) return defaults
    return widths.map((width) => Math.max(60, Math.round(Number(width) || 0)))
  }

  function devToolsNetworkPreferencePayload(sortKey: DevToolsNetworkSortKey | '', sortDirection: DevToolsNetworkSortDirection, columnWidths: number[]) {
    const normalizedSortKey = normalizedDevToolsNetworkSortKey(sortKey)
    const normalizedDirection = normalizedSortKey ? normalizedDevToolsNetworkSortDirection(sortDirection) : ''
    return {
      sortKey: normalizedDirection ? normalizedSortKey : '',
      sortDirection: normalizedDirection,
      columnWidths: normalizedDevToolsNetworkColumnWidths(columnWidths)
    }
  }

  function devToolsNetworkPreferencesKeyFor(preferences: main.DevToolsNetworkPreferences | undefined) {
    const payload = devToolsNetworkPreferencePayload(normalizedDevToolsNetworkSortKey(preferences?.sortKey), normalizedDevToolsNetworkSortDirection(preferences?.sortDirection), preferences?.columnWidths ?? defaultDevToolsNetworkColumnWidths())
    return JSON.stringify(payload)
  }

  function applyDevToolsNetworkPreferences(preferences: main.DevToolsNetworkPreferences | undefined) {
    const payload = devToolsNetworkPreferencePayload(normalizedDevToolsNetworkSortKey(preferences?.sortKey), normalizedDevToolsNetworkSortDirection(preferences?.sortDirection), preferences?.columnWidths ?? defaultDevToolsNetworkColumnWidths())
    devToolsNetworkSortKey = payload.sortKey
    devToolsNetworkSortDirection = payload.sortDirection
    devToolsNetworkColumnWidths = payload.columnWidths
    devToolsNetworkPreferencesKey = JSON.stringify(payload)
  }

  async function updateDevToolsNetworkPreferences(updates: Partial<main.DevToolsNetworkPreferences>) {
    const payload = devToolsNetworkPreferencePayload(normalizedDevToolsNetworkSortKey(updates.sortKey ?? devToolsNetworkSortKey), normalizedDevToolsNetworkSortDirection(updates.sortDirection ?? devToolsNetworkSortDirection), updates.columnWidths ?? devToolsNetworkColumnWidths)
    devToolsNetworkSortKey = payload.sortKey
    devToolsNetworkSortDirection = payload.sortDirection
    devToolsNetworkColumnWidths = payload.columnWidths
    devToolsNetworkPreferencesKey = JSON.stringify(payload)
    if (!state) return
    state = await UpdatePreferences({
      ...state.preferences,
      devTools: {
        ...(state.preferences.devTools ?? {}),
        network: payload
      }
    } as main.Preferences)
  }

  function filteredDevToolsNetworkRows(rows: main.NetworkLog[], filters: Record<string, boolean>) {
    return rows.filter((row) => filters[normalizedNetworkMethod(row)] === true)
  }

  function sortedDevToolsNetworkRows(rows: main.NetworkLog[], key: DevToolsNetworkSortKey | '', direction: DevToolsNetworkSortDirection) {
    if (!key || !direction) return rows
    const multiplier = direction === 'asc' ? 1 : -1
    return [...rows].sort((left, right) => {
      const leftValue = devToolsNetworkSortValue(left, key)
      const rightValue = devToolsNetworkSortValue(right, key)
      if (typeof leftValue === 'number' && typeof rightValue === 'number') {
        return (leftValue - rightValue) * multiplier
      }
      return String(leftValue).localeCompare(String(rightValue)) * multiplier
    })
  }

  function devToolsNetworkSortValue(row: main.NetworkLog, key: DevToolsNetworkSortKey) {
    if (key === 'method') return normalizedNetworkMethod(row)
    if (key === 'status') return row.status ?? 0
    if (key === 'domain') return devToolsNetworkDomain(row)
    if (key === 'path') return devToolsNetworkPath(row)
    if (key === 'time') return networkLogTimestamp(row)
    if (key === 'duration') return row.durationMs ?? 0
    return row.size ?? 0
  }

  function cycleDevToolsNetworkSort(key: DevToolsNetworkSortKey) {
    let nextKey: DevToolsNetworkSortKey | '' = key
    let nextDirection: DevToolsNetworkSortDirection = 'asc'
    if (devToolsNetworkSortKey !== key) {
      nextKey = key
      nextDirection = 'asc'
    } else if (devToolsNetworkSortDirection === 'asc') {
      nextKey = key
      nextDirection = 'desc'
    } else if (devToolsNetworkSortDirection === 'desc') {
      nextKey = ''
      nextDirection = ''
    } else {
      nextKey = key
      nextDirection = 'asc'
    }
    void updateDevToolsNetworkPreferences({ sortKey: nextKey, sortDirection: nextDirection })
  }

  function devToolsNetworkSortLabel(key: DevToolsNetworkSortKey, activeKey: DevToolsNetworkSortKey | '', direction: DevToolsNetworkSortDirection) {
    if (activeKey !== key || !direction) return ''
    return direction === 'asc' ? 'ascending' : 'descending'
  }

  function devToolsNetworkSortAriaValue(key: DevToolsNetworkSortKey, activeKey: DevToolsNetworkSortKey | '', direction: DevToolsNetworkSortDirection): 'ascending' | 'descending' | 'none' {
    if (activeKey !== key || !direction) return 'none'
    return direction === 'asc' ? 'ascending' : 'descending'
  }

  function setDevToolsNetworkFilter(method: string, enabled: boolean) {
    devToolsNetworkFilters = { ...devToolsNetworkFilters, [method]: enabled }
  }

  function setAllDevToolsNetworkFilters(enabled: boolean) {
    devToolsNetworkFilters = Object.fromEntries(devToolsNetworkMethods.map((method) => [method, enabled])) as Record<string, boolean>
  }

  function startDevToolsNetworkColumnResize(index: number, event: MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    const startX = event.clientX
    const startWidths = [...devToolsNetworkColumnWidths]
    let latestWidths = startWidths
    devToolsNetworkResizingColumn = index
    const handleMove = (moveEvent: MouseEvent) => {
      const delta = moveEvent.clientX - startX
      const clampedDelta = Math.max(-(startWidths[index] - 60), Math.min(startWidths[index + 1] - 60, delta))
      latestWidths = [...startWidths]
      latestWidths[index] = startWidths[index] + clampedDelta
      latestWidths[index + 1] = startWidths[index + 1] - clampedDelta
      devToolsNetworkColumnWidths = latestWidths
    }
    const cleanup = () => {
      window.removeEventListener('mousemove', handleMove)
      window.removeEventListener('mouseup', handleUp)
      devToolsNetworkResizingColumn = -1
    }
    const handleUp = () => {
      cleanup()
      void updateDevToolsNetworkPreferences({ columnWidths: latestWidths })
    }
    window.addEventListener('mousemove', handleMove)
    window.addEventListener('mouseup', handleUp)
  }

  function startDevToolsDetailsPanelResize(event: MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    const startX = event.clientX
    const startWidth = devToolsDetailsPanelWidth
    let latestWidth = startWidth
    const handleMove = (moveEvent: MouseEvent) => {
      latestWidth = normalizedDevToolsDetailsPanelWidth(startWidth - (moveEvent.clientX - startX))
      devToolsDetailsPanelWidth = latestWidth
    }
    const cleanup = () => {
      window.removeEventListener('mousemove', handleMove)
      window.removeEventListener('mouseup', handleUp)
    }
    const handleUp = () => {
      cleanup()
      void updateDevToolsShellPreferences({ open: devToolsOpen, activeTab: devToolsTab, detailsPanelWidth: latestWidth })
    }
    window.addEventListener('mousemove', handleMove)
    window.addEventListener('mouseup', handleUp)
  }

  function startDevToolsDrawerResize(event: MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    const startY = event.clientY
    const startHeight = devToolsDrawerHeight
    let latestHeight = startHeight
    const handleMove = (moveEvent: MouseEvent) => {
      latestHeight = normalizedDevToolsDrawerHeight(startHeight - (moveEvent.clientY - startY))
      devToolsDrawerHeight = latestHeight
    }
    const cleanup = () => {
      window.removeEventListener('mousemove', handleMove)
      window.removeEventListener('mouseup', handleUp)
    }
    const handleUp = () => {
      cleanup()
      void updateDevToolsShellPreferences({ open: devToolsOpen, activeTab: devToolsTab, drawerHeight: latestHeight })
    }
    window.addEventListener('mousemove', handleMove)
    window.addEventListener('mouseup', handleUp)
  }

  function selectDevToolsNetworkRow(row: main.NetworkLog) {
    selectedDevToolsNetworkLogID = row.id
    devToolsNetworkDetailTab = 'request'
  }

  async function openDevTools(tab: DevToolsTab = devToolsTab) {
    devToolsOpen = true
    devToolsTab = tab
    await updateDevToolsShellPreferences({ open: true, activeTab: tab })
    await refreshDevToolsSnapshot()
  }

  async function closeDevTools() {
    devToolsOpen = false
    await updateDevToolsShellPreferences({ open: false, activeTab: devToolsTab })
  }

  async function selectDevToolsTab(tab: DevToolsTab) {
    devToolsTab = tab
    await updateDevToolsShellPreferences({ open: true, activeTab: tab })
    if (tab === 'performance') await refreshDevToolsSnapshot()
  }

  async function refreshDevToolsSnapshot() {
    try {
      devToolsSnapshot = await GetDevToolsSnapshot()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  function startTerminalPolling() {
    if (terminalPollTimer !== undefined) return
    void refreshTerminalSessions()
    terminalPollTimer = window.setInterval(() => {
      void refreshTerminalSessions()
    }, 500)
  }

  function stopTerminalPolling() {
    if (terminalPollTimer === undefined) return
    window.clearInterval(terminalPollTimer)
    terminalPollTimer = undefined
  }

  function terminalDefaultCwd() {
    return activeCollection?.path || activeWorkspace?.path || ''
  }

  async function refreshTerminalSessions() {
    try {
      terminalError = ''
      const sessions = await ListTerminalSessions()
      terminalSessions = sessions
      if (sessions.length === 0) {
        terminalActiveSessionId = ''
        terminalOutput = ''
        return
      }
      if (!terminalActiveSessionId || !sessions.some((session) => session.id === terminalActiveSessionId)) {
        terminalActiveSessionId = sessions[0].id
      }
      if (terminalActiveSessionId) {
        const latest = await GetTerminalSession(terminalActiveSessionId)
        terminalOutput = latest.output ?? ''
        terminalSessions = sessions.map((session) => session.id === latest.id ? latest : session)
      }
    } catch (err) {
      terminalError = err instanceof Error ? err.message : String(err)
    }
  }

  async function createTerminalSessionAt(cwd: string) {
    if (terminalBusy) return
    terminalBusy = true
    terminalError = ''
    try {
      const session = await CreateTerminalSession(cwd)
      terminalActiveSessionId = session.id
      terminalOutput = session.output ?? ''
      await ResizeTerminalSession(session.id, 80, 24)
      await refreshTerminalSessions()
    } catch (err) {
      terminalError = err instanceof Error ? err.message : String(err)
    } finally {
      terminalBusy = false
    }
  }

  async function createTerminalSession() {
    await createTerminalSessionAt(terminalDefaultCwd())
  }

  async function openCollectionInTerminal() {
    if (!activeCollection || activeCollection.notFoundLocally) return
    const cwd = activeCollection.path
    await runAction('open collection terminal', async () => {
      await openDevTools('terminal')
      await createTerminalSessionAt(cwd)
    })
  }

  async function openFolderInTerminal(collection: main.Collection, folderPath: string) {
    if (!collection || collection.notFoundLocally) return
    await runAction('open folder terminal', async () => {
      const cwd = await ResolveCollectionFolderPath(collection.id, folderPath)
      await openDevTools('terminal')
      await createTerminalSessionAt(cwd)
    })
  }

  function revealInFolderLabel() {
    const platform = typeof navigator === 'undefined' ? '' : navigator.platform.toLowerCase()
    if (platform.includes('mac')) return 'Reveal in Finder'
    if (platform.includes('win')) return 'Reveal in File Explorer'
    return 'Reveal in File Manager'
  }

  async function revealCollectionInFolder() {
    if (!activeCollection || activeCollection.notFoundLocally) return
    const collectionName = activeCollection.name
    await runAction('reveal collection folder', async () => {
      await RevealCollectionInFolder(activeCollection.id)
      revealCollectionMessage = `${revealInFolderLabel()} opened for ${collectionName}`
    })
  }

  async function revealFolderInFolder(collection: main.Collection, folderPath: string) {
    if (!collection || collection.notFoundLocally) return
    await runAction('reveal folder', async () => {
      await RevealCollectionFolderInFolder(collection.id, folderPath)
      revealCollectionMessage = `${revealInFolderLabel()} opened for ${slashPathBase(folderPath)}`
    })
  }

  async function revealRequestInFolder(collection: main.Collection, item: main.RequestItem) {
    if (!collection || collection.notFoundLocally) return
    await runAction('reveal request', async () => {
      await RevealRequestInFolder(collection.id, item.id)
      revealCollectionMessage = `${revealInFolderLabel()} opened for ${item.name}`
    })
  }

  async function selectTerminalSession(sessionId: string) {
    terminalActiveSessionId = sessionId
    try {
      const session = await GetTerminalSession(sessionId)
      terminalOutput = session.output ?? ''
      await ResizeTerminalSession(sessionId, 80, 24)
    } catch (err) {
      terminalError = err instanceof Error ? err.message : String(err)
    }
  }

  async function sendTerminalInput() {
    const command = terminalInput
    if (!terminalActiveSessionId || !command.trim()) return
    terminalInput = ''
    terminalError = ''
    try {
      const session = await WriteTerminalSession(terminalActiveSessionId, `${command}\r`)
      terminalOutput = session.output ?? terminalOutput
      await refreshTerminalSessions()
    } catch (err) {
      terminalError = err instanceof Error ? err.message : String(err)
    }
  }

  async function closeTerminalSession(sessionId: string) {
    terminalError = ''
    try {
      await KillTerminalSession(sessionId)
      if (terminalActiveSessionId === sessionId) {
        terminalActiveSessionId = ''
        terminalOutput = ''
      }
      await refreshTerminalSessions()
    } catch (err) {
      terminalError = err instanceof Error ? err.message : String(err)
    }
  }

  function terminalSessionLabel(session: main.TerminalSession) {
    const parts = (session.cwd || '').split(/[\\/]/).filter(Boolean)
    const leaf = parts[parts.length - 1]
    return leaf || 'Terminal'
  }

  function terminalSessionStatus(session: main.TerminalSession) {
    if (session.exited) return `Exited ${session.exitCode}`
    return session.pid ? `PID ${session.pid}` : 'Starting'
  }

  function terminalDisplayOutput(output: string) {
    return output
      .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
      .replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, '')
      .replace(/\x1B[()#][0-9A-Za-z]/g, '')
      .replace(/\x1B[=>]/g, '')
      .replace(/\r(?!\n)/g, '\n')
      .trimStart()
  }

  function devToolsNetworkDomain(row: main.NetworkLog) {
    try {
      const parsed = new URL(row.url)
      return parsed.host || row.url || '-'
    } catch {
      return row.url || '-'
    }
  }

  function devToolsNetworkPath(row: main.NetworkLog) {
    try {
      const parsed = new URL(row.url)
      return `${parsed.pathname || '/'}${parsed.search}`
    } catch {
      return row.url || '-'
    }
  }

  function networkLogTime(row: main.NetworkLog) {
    if (!row.at) return '-'
    const value = new Date(row.at)
    if (Number.isNaN(value.getTime())) return '-'
    return value.toLocaleTimeString()
  }

  function networkLogTimestamp(row: main.NetworkLog) {
    if (!row.at) return 0
    const value = new Date(row.at)
    return Number.isNaN(value.getTime()) ? 0 : value.getTime()
  }

  function statusDisplay(status: number | undefined) {
    return status ? String(status) : '-'
  }

  function formatNetworkSize(value: number | undefined) {
    return formatRuntimeBytes(value)
  }

  function networkHeaderRows(headers: Record<string, string> | undefined) {
    return Object.entries(headers ?? {}).sort(([left], [right]) => left.localeCompare(right))
  }

  function networkLogBody(value: string | undefined) {
    return value?.trim() ? value : ''
  }

  function networkLogLines(row: main.NetworkLog | undefined) {
    if (!row) return []
    return [
      `Started: ${networkLogTime(row)}`,
      `Duration: ${row.durationMs ?? 0} ms`,
      `Size: ${formatNetworkSize(row.size)}`,
      row.error ? `Error: ${row.error}` : ''
    ].filter(Boolean)
  }

  function formatRuntimeBytes(value: number | undefined) {
    if (!value) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB']
    let amount = value
    let unitIndex = 0
    while (amount >= 1024 && unitIndex < units.length - 1) {
      amount /= 1024
      unitIndex += 1
    }
    const precision = amount >= 10 || unitIndex === 0 ? 0 : 1
    return `${amount.toFixed(precision)} ${units[unitIndex]}`
  }

  function requestCommandState(
    request: main.RequestItem | undefined,
    collection: main.Collection | undefined,
    environmentName: string | undefined,
    action: string,
    webSocketConnected: boolean,
    grpcConnected: boolean,
    preferences: main.Preferences | undefined,
    httpInFlight: boolean,
    cancellationPending: boolean,
    backgroundCancellation: RequestCommandState['backgroundCancellation']
  ): RequestCommandState {
    const response = request?.response
    const status = response?.cancelled ? 'Cancelled' : response?.status ? String(response.status) : 'Idle'
    const tone = response?.cancelled ? 'warning' : !response?.status ? 'idle' : response.status < 300 ? 'success' : response.status < 400 ? 'warning' : 'danger'
    const transient = requestIsTransient(collection, request)
    const collectionProxy = collectionProxyMode(collection?.proxy)
    const preferencesProxy = preferencesProxyMode(preferences)
    const proxyCue = collectionProxy === 'off'
      ? 'Proxy off'
      : collectionProxy === 'manual'
        ? 'Proxy: collection'
        : preferencesProxy === 'off'
          ? 'Proxy off'
          : preferencesProxy === 'manual'
            ? 'Proxy: manual'
            : preferencesProxy === 'pac'
              ? 'Proxy: PAC'
              : 'Proxy: system'
    const tlsVerificationEnabled = request?.settings?.verifyTls !== false && preferences?.request?.sslVerification !== false
    return {
      protocol: request?.type === 'grpc' ? 'gRPC' : request?.type === 'websocket' ? 'WebSocket' : request?.type === 'graphql' ? 'GraphQL' : 'HTTP',
      environmentName: environmentName || 'No environment',
      saveLabel: transient ? 'Save temp' : 'Save',
      dirty: transient || Boolean(request?.draft),
      runningLabel: action,
      canCancel: httpInFlight || webSocketConnected || grpcConnected,
      cancelLabel: httpInFlight ? 'Cancel request' : request?.type === 'websocket' ? 'Disconnect' : 'Cancel stream',
      cancelDuringBusy: httpInFlight,
      cancellationPending,
      backgroundCancellation,
      transportCues: [tlsVerificationEnabled ? 'TLS verify' : 'TLS off', proxyCue],
      response: {
        status,
        statusText: response?.cancelled ? 'Request cancelled' : response?.statusText || (response?.error ? 'Request failed' : 'No response yet'),
        duration: `${response?.durationMs ?? 0} ms`,
        size: formatRuntimeBytes(response?.size),
        tone
      }
    }
  }

  function requestNameForTransport(target: { collectionId: string; requestId: string }) {
    for (const workspace of state?.workspaces ?? []) {
      const collection = workspace.collections?.find((candidate) => candidate.id === target.collectionId)
      const request = collection?.items?.find((candidate) => candidate.id === target.requestId)
      if (request) return request.name?.trim() || 'request'
    }
    return 'request'
  }

  function runnerCancellationCount(snapshot: main.RunnerSnapshot | undefined) {
    const count = snapshot?.cancelled
    return typeof count === 'number' && Number.isFinite(count) && count > 0 ? count : 0
  }

  function formatCPUPercent(value: number | undefined) {
    const percent = Number(value ?? 0)
    if (!Number.isFinite(percent) || percent < 0) return '0.0%'
    return `${percent.toFixed(1)}%`
  }

  function formatUptime(seconds: number | undefined) {
    const totalSeconds = Math.max(0, Math.floor(seconds ?? 0))
    const hours = Math.floor(totalSeconds / 3600)
    const minutes = Math.floor((totalSeconds % 3600) / 60)
    const remainder = totalSeconds % 60
    if (hours > 0) return `${hours}h ${minutes}m`
    if (minutes > 0) return `${minutes}m ${remainder}s`
    return `${remainder}s`
  }

  function hexFromBase64(value: string) {
    try {
      const binary = atob(value)
      return Array.from(binary, (char) => char.charCodeAt(0).toString(16).padStart(2, '0')).join(' ')
    } catch {
      return ''
    }
  }

  function looksLikeJson(value: string) {
    const trimmed = value.trim()
    return trimmed.startsWith('{') || trimmed.startsWith('[')
  }

  function statusClass(status?: string) {
    if (status === 'done') return 'done'
    if (status === 'partial') return 'partial'
    return 'todo'
  }

  function groupedItems(collection: main.Collection, query = '') {
    const groups: { folder: string; items: main.RequestItem[] }[] = []
    const indexByFolder = new Map<string, number>()
    const addGroup = (folder: string) => {
      let index = indexByFolder.get(folder)
      if (index === undefined) {
        index = groups.length
        indexByFolder.set(folder, index)
        groups.push({ folder, items: [] })
      }
      return index
    }
    for (const folder of filteredFolders(collection, query)) {
      addGroup(folder.displayPath || folder.path)
    }
    for (const item of filteredItems(collection, query)) {
      const folder = item.folderPath || ''
      const index = addGroup(folder)
      groups[index].items.push(item)
    }
    return groups
  }

  function filteredFolders(collection: main.Collection, query = '') {
    const folders = collection.folders ?? []
    if (!query.trim()) return folders
    return folders.filter((folder) => folderMatches(folder, query))
  }

  function sidebarCollections(workspace: main.Workspace | undefined, query: string) {
    const collections = workspace?.collections ?? []
    if (!query) return collections
    return collections.filter((collection) => collectionMatches(collection, query) || filteredFolders(collection, query).length > 0 || filteredItems(collection, query).length > 0)
  }

  function sidebarRequestCount(workspace: main.Workspace | undefined, query: string) {
    return (workspace?.collections ?? []).reduce((total, collection) => total + filteredItems(collection, query).length, 0)
  }

  function buildGlobalSearchResults(workspace: main.Workspace | undefined, query: string): GlobalSearchResult[] {
    const collections = workspace?.collections ?? []
    const normalized = normalizeGlobalSearchQuery(query)
    if (!normalized) {
      const collectionResults = collections
        .map((collection) => ({
          id: `collection:${collection.id}`,
          type: 'collection' as const,
          collectionId: collection.id,
          name: collection.name,
          subtitle: collection.path || `${collection.items?.length ?? 0} requests`,
          meta: collection.format || 'collection',
          rank: 1
        }))
        .sort(sortGlobalSearchResults)
      return collectionResults
    }
    if (!isValidGlobalSearchQuery(normalized)) return []
    const terms = normalized.split(/[\s/]+/).filter(Boolean)
    const enablePathMatch = normalized.includes('/')
    const results: GlobalSearchResult[] = []
    for (const collection of collections) {
      if (globalSearchTermsMatch([collection.name, collection.path, collection.format], terms)) {
        results.push({
          id: `collection:${collection.id}`,
          type: 'collection',
          collectionId: collection.id,
          name: collection.name,
          subtitle: collection.path || `${collection.items?.length ?? 0} requests`,
          meta: collection.format || 'collection',
          rank: 0
        })
      }

      const folders = new Set((collection.items ?? []).map((item) => item.folderPath).filter(Boolean))
      for (const folder of folders) {
        const folderPath = `${collection.name}/${folder}`
        if (globalSearchTermsMatch([folder, enablePathMatch ? folderPath : ''], terms)) {
          results.push({
            id: `folder:${collection.id}:${folder}`,
            type: 'folder',
            collectionId: collection.id,
            name: folder,
            subtitle: collection.name,
            meta: 'folder',
            rank: 1
          })
        }
      }

      for (const item of collection.items ?? []) {
        const itemPath = globalSearchItemPath(collection, item)
        const nameMatch = globalSearchTermsMatch([item.name], terms)
        const urlMatch = globalSearchTermsMatch([item.url], terms)
        const pathMatch = enablePathMatch && globalSearchTermsMatch([itemPath], terms)
        const methodMatch = globalSearchTermsMatch([item.method, item.type], terms)
        if (nameMatch || urlMatch || pathMatch || methodMatch) {
          results.push({
            id: `request:${collection.id}:${item.id}`,
            type: 'request',
            collectionId: collection.id,
            itemId: item.id,
            name: item.name,
            subtitle: item.folderPath ? `${collection.name} / ${item.folderPath}` : collection.name,
            meta: item.method || item.type || 'request',
            rank: nameMatch ? 2 : urlMatch ? 3 : 4
          })
        }
      }
    }

    return results.sort(sortGlobalSearchResults)
  }

  function filteredItems(collection: main.Collection, query: string) {
    const items = collection.items ?? []
    if (!query || collectionMatches(collection, query)) return items
    return items.filter((item) => requestMatches(collection, item, query))
  }

  function normalizedSearch(value: string) {
    return value.trim().toLowerCase()
  }

  function normalizeGlobalSearchQuery(value: string) {
    return value.trim().replace(/\/+/g, '/').toLowerCase()
  }

  function isValidGlobalSearchQuery(value: string) {
    return Boolean(value && value !== '/' && !(value.length === 1 && !/[a-z0-9]/i.test(value)))
  }

  function globalSearchTermsMatch(values: unknown[], terms: string[]) {
    const haystack = values.map((value) => String(value ?? '').toLowerCase()).join(' ')
    return terms.every((term) => haystack.includes(term))
  }

  function globalSearchItemPath(collection: main.Collection, item: main.RequestItem) {
    return [collection.name, item.folderPath, item.name].filter(Boolean).join('/')
  }

  function sortGlobalSearchResults(a: GlobalSearchResult, b: GlobalSearchResult) {
    return a.rank - b.rank || a.type.localeCompare(b.type) || a.name.localeCompare(b.name)
  }

  function openGlobalSearch() {
    globalSearchOpen = true
    globalSearchQuery = ''
    globalSearchIndex = 0
    void tick().then(() => globalSearchInput?.focus())
  }

  function closeGlobalSearch() {
    globalSearchOpen = false
    globalSearchQuery = ''
    globalSearchIndex = 0
  }

  async function selectGlobalSearchResult(result: GlobalSearchResult) {
    closeGlobalSearch()
    if (result.type === 'request' && result.itemId) {
      await openRequestTab(result.collectionId, result.itemId)
    } else if (result.type === 'folder') {
      selectedCollectionId = result.collectionId
      requestSearch = result.name
      activeView = 'request'
    } else {
      selectedCollectionId = result.collectionId
      activeView = 'collection'
    }
  }

  function handleGlobalSearchKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      closeGlobalSearch()
    } else if (event.key === 'ArrowDown') {
      event.preventDefault()
      globalSearchIndex = globalSearchResults.length ? (globalSearchIndex + 1) % globalSearchResults.length : 0
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      globalSearchIndex = globalSearchResults.length ? (globalSearchIndex - 1 + globalSearchResults.length) % globalSearchResults.length : 0
    } else if (event.key === 'Enter') {
      event.preventDefault()
      const result = globalSearchResults[globalSearchIndex]
      if (result) void selectGlobalSearchResult(result)
    }
  }

  function collectionMatches(collection: main.Collection, query: string) {
    return [collection.name, collection.format, collection.path].some((value) => searchHit(value, query))
  }

  function folderMatches(folder: main.FolderConfig, query: string) {
    return [folder.displayPath, folder.path, folder.name].some((value) => searchHit(value, query))
  }

  function requestMatches(collection: main.Collection, item: main.RequestItem, query: string) {
    const exampleValues = (item.examples ?? []).flatMap((example) => [example.name, example.description, example.request?.url])
    return [collection.name, item.folderPath, item.name, item.method, item.type, item.url, ...exampleValues].some((value) => searchHit(value, query))
  }

  function searchHit(value: unknown, query: string) {
    return String(value ?? '').toLowerCase().includes(query)
  }

  function emptyCookieForm(): CookieForm {
    return {
      id: '',
      name: '',
      value: '',
      domain: '',
      path: '/',
      expires: '',
      session: true,
      secure: false,
      httpOnly: false,
      sameSite: '',
      hostOnly: true
    }
  }

  function cookieGroups(cookies: main.CookieEntry[], query: string) {
    const groups = new Map<string, main.CookieEntry[]>()
    for (const cookie of cookies ?? []) {
      if (query && !cookieMatches(cookie, query)) continue
      const domain = cookie.domain || '(no domain)'
      groups.set(domain, [...(groups.get(domain) ?? []), cookie])
    }
    return Array.from(groups.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([domain, groupCookies]) => ({
        domain,
        cookies: groupCookies.sort((a, b) => (a.path || '/').localeCompare(b.path || '/') || a.name.localeCompare(b.name)),
        header: cookieHeaderPreview(groupCookies)
      }))
  }

  function cookieMatches(cookie: main.CookieEntry, query: string) {
    return [cookie.name, cookie.value, cookie.domain, cookie.path, cookie.sameSite, cookieFlags(cookie)].some((value) => searchHit(value, query))
  }

  function cookieHeaderPreview(cookies: main.CookieEntry[]) {
    return cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join('; ')
  }

  function cookieFlags(cookie: main.CookieEntry) {
    const flags = []
    if (cookie.secure) flags.push('secure')
    if (cookie.httpOnly) flags.push('httpOnly')
    if (cookie.sameSite) flags.push(`sameSite=${cookie.sameSite}`)
    if (cookie.hostOnly) flags.push('hostOnly')
    return flags.join(', ') || 'none'
  }

  function cookieExpiresInput(cookie: main.CookieEntry) {
    if (!cookie.expires) return ''
    const value = new Date(cookie.expires)
    return Number.isNaN(value.getTime()) || value.getFullYear() <= 1 ? '' : value.toISOString()
  }

  function cookieExpiry(cookie: main.CookieEntry) {
    if (cookie.session) return 'session'
    if (!cookie.expires) return 'session'
    const value = new Date(cookie.expires)
    if (Number.isNaN(value.getTime()) || value.getFullYear() <= 1) return 'session'
    return value.toLocaleString()
  }

  function notificationsForDisplay(notifications: main.Notification[]) {
    return [...notifications].sort((a, b) => notificationTime(b) - notificationTime(a))
  }

  function notificationTime(notification: main.Notification) {
    const value = new Date(notification.at)
    return Number.isNaN(value.getTime()) ? 0 : value.getTime()
  }

  function notificationTitle(notification: main.Notification | undefined) {
    return notification?.title || notification?.message || 'Notification'
  }

  function notificationDescription(notification: main.Notification | undefined) {
    return notification?.description || notification?.message || ''
  }

  function notificationType(notification: main.Notification | undefined) {
    return notification?.type || notification?.level || 'Info'
  }

  function notificationDate(notification: main.Notification | undefined) {
    if (!notification?.at) return ''
    const value = new Date(notification.at)
    if (Number.isNaN(value.getTime())) return ''
    return value.toLocaleString()
  }

  function notificationLevelClass(notification: main.Notification | undefined) {
    const level = (notification?.level || '').toLowerCase()
    if (level === 'success') return 'success'
    if (level === 'warning' || level === 'warn') return 'warning'
    if (level === 'error' || level === 'danger') return 'danger'
    return 'info'
  }

  async function openNotifications() {
    notificationsOpen = true
    notificationTab = 'all'
    pinnedUnreadNotificationIDs = null
    const first = visibleNotifications[0]
    selectedNotificationID = first?.id ?? ''
    if (first && !first.read) {
      await selectNotification(first)
    }
  }

  function closeNotifications() {
    notificationsOpen = false
    notificationTab = 'all'
    selectedNotificationID = ''
    pinnedUnreadNotificationIDs = null
  }

  async function selectNotification(notification: main.Notification) {
    selectedNotificationID = notification.id
    if (!notification.read) {
      state = await MarkNotificationRead(notification.id)
    }
  }

  function setNotificationTab(tab: NotificationTab) {
    notificationTab = tab
    let nextListed = visibleNotifications
    if (tab === 'unread') {
      const unread = visibleNotifications.filter((notification) => !notification.read)
      pinnedUnreadNotificationIDs = new Set(unread.map((notification) => notification.id))
      nextListed = unread
    } else {
      pinnedUnreadNotificationIDs = null
    }
    selectedNotificationID = nextListed[0]?.id ?? ''
  }

  async function markAllNotificationsRead() {
    await runAction('mark notifications read', async () => {
      state = await MarkAllNotificationsRead()
      if (notificationTab === 'unread') pinnedUnreadNotificationIDs = null
    })
  }

  async function clearNotifications() {
    await runAction('clear notifications', async () => {
      state = await ClearNotifications()
      selectedNotificationID = ''
      pinnedUnreadNotificationIDs = null
    })
  }

	  function openExternalLink(url: string) {
	    try {
	      BrowserOpenURL(url)
	    } catch {
	      window.open(url, '_blank', 'noopener,noreferrer')
	    }
	  }

	  function closeOAuth2Authorization() {
	    oauth2AuthorizationRequest = null
	    oauth2CallbackURLInput = ''
	    oauth2CallbackMessage = ''
	  }

	  function openOAuth2AuthorizationInSystemBrowser() {
	    if (!oauth2AuthorizationRequest?.authorizeUrl) return
	    openExternalLink(oauth2AuthorizationRequest.authorizeUrl)
	  }

	  async function submitOAuth2CallbackURL() {
	    const callbackURL = oauth2CallbackURLInput.trim()
	    if (!callbackURL) return
	    try {
	      const accepted = await CompleteOAuth2Callback(callbackURL)
	      oauth2CallbackMessage = accepted ? 'Callback received. Completing OAuth...' : 'No pending OAuth callback matched this URL.'
	      if (accepted) {
	        window.setTimeout(closeOAuth2Authorization, 700)
	      }
	    } catch (err) {
	      oauth2CallbackMessage = err instanceof Error ? err.message : String(err)
	    }
	  }

  function responseStatusClass(status?: number) {
    if (!status) return 'muted'
    if (status < 300) return 'ok'
    if (status < 400) return 'warn'
    return 'bad'
  }

  async function handleNativeMenuCommand(command: string) {
    switch (command) {
      case 'new-window':
        await openNativeNewWindow()
        return
      case 'open-workspace-in-new-window':
        await runWorkbenchCommand('open-workspace')
        return
      case 'new-request':
        await runWorkbenchCommand('new-request')
        return
      case 'save':
        await saveRequest()
        return
      case 'save-all':
        await saveAllOpenTabs()
        return
      case 'close-tab':
        await closeActiveTab()
        return
      case 'reopen-tab':
        await reopenLastClosedTab()
        return
      case 'import':
      case 'open-collection':
        await runWorkbenchCommand('import')
        return
      case 'command-palette':
        await runWorkbenchCommand('command-palette')
        return
      case 'workspace-search':
        await runWorkbenchCommand('workspace-search')
        return
      case 'toggle-sidebar':
        await runWorkbenchCommand('toggle-sidebar')
        return
      case 'toggle-devtools':
        await runWorkbenchCommand('toggle-devtools')
        return
      case 'change-orientation':
        await runWorkbenchCommand('change-orientation')
        return
      case 'send-or-start':
        if (activeView === 'runner') await runCollection()
        else await sendRequest()
        return
      case 'cancel-active':
        await cancelActiveRequest()
        return
      case 'open-runner':
        await runWorkbenchCommand('open-runner')
        return
      case 'new-collection':
        await runWorkbenchCommand('new-collection')
        return
      case 'open-environments':
        await runWorkbenchCommand('open-environments')
        return
      case 'open-git':
		await runWorkbenchCommand('open-git-workbench')
        return
      case 'open-preferences':
        await runWorkbenchCommand('open-preferences')
        return
      case 'open-network':
        await runWorkbenchCommand('open-network')
        return
      case 'open-cookies':
        await runWorkbenchCommand('open-cookies')
        return
      case 'open-capabilities':
        await runWorkbenchCommand('open-capabilities')
        return
      case 'open-keyboard-shortcuts':
        await runWorkbenchCommand('open-keyboard-shortcuts')
    }
  }

  function shortcut(event: KeyboardEvent) {
    if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === 'p') {
      event.preventDefault()
      openCommandPalette()
      return
    }
    if (event.key === 'Escape' && commandPaletteOpen) {
      event.preventDefault()
      closeCommandPalette()
      return
    }
    if (event.key === 'Escape' && document.querySelector('details.request-actions[open]')) {
      event.preventDefault()
      closeRequestActionMenus()
      return
    }
    const hasCommandModifier = event.metaKey || event.ctrlKey
    if (hasCommandModifier && event.key.toLowerCase() === 'l' && activeView === 'request') {
      event.preventDefault()
      requestURLInput?.focus()
      requestURLInput?.select()
      return
    }
    if (event.key === 'Escape' && (requestCommand.canCancel || hasActiveHTTPTransport || activeCollectionRun)) {
      if (document.querySelector('[role="dialog"][aria-modal="true"]')) return
      event.preventDefault()
      void cancelActiveRequest()
      return
    }
    if (!state || !keybindingsAreEnabled(state.preferences)) return
    if (keyBindingEventMatches(event, 'globalSearch')) {
      event.preventDefault()
      openGlobalSearch()
      return
    }
    if (keyBindingEventMatches(event, 'sidebarSearch')) {
      event.preventDefault()
      requestSearchInput?.focus()
      requestSearchInput?.select()
      return
    }
    if (keyBindingEventMatches(event, 'collapseSidebar')) {
      event.preventDefault()
      toggleSidebarCollapse()
      return
    }
    if (keyBindingEventMatches(event, 'closeAllTabs')) {
      event.preventDefault()
      void closeAllOpenTabs()
      return
    }
    if (keyBindingEventMatches(event, 'reopenLastClosedTab')) {
      event.preventDefault()
      void reopenLastClosedTab()
      return
    }
    if (keyBindingEventMatches(event, 'closeTab')) {
      event.preventDefault()
      void closeActiveTab()
      return
    }
    if (keyBindingEventMatches(event, 'switchToPreviousTab')) {
      event.preventDefault()
      void switchToRelativeOpenTab(-1)
      return
    }
    if (keyBindingEventMatches(event, 'switchToNextTab')) {
      event.preventDefault()
      void switchToRelativeOpenTab(1)
      return
    }
    if (keyBindingEventMatches(event, 'switchToLastTab')) {
      event.preventDefault()
      void switchToOpenTabAt((state.openTabs?.length ?? 0) - 1)
      return
    }
    for (let index = 1; index <= 8; index += 1) {
      if (keyBindingEventMatches(event, `switchToTab${index}`)) {
        event.preventDefault()
        void switchToOpenTabAt(index - 1)
        return
      }
    }
    if (keyBindingEventMatches(event, 'moveTabLeft')) {
      event.preventDefault()
      void moveActiveTab(-1)
      return
    }
    if (keyBindingEventMatches(event, 'moveTabRight')) {
      event.preventDefault()
      void moveActiveTab(1)
      return
    }
    if (keyBindingEventMatches(event, 'newRequest')) {
      event.preventDefault()
      createRequest()
      return
    }
    if (keyBindingEventMatches(event, 'importCollection')) {
      event.preventDefault()
      activeView = 'import'
      return
    }
    if (keyBindingEventMatches(event, 'editEnvironment')) {
      event.preventDefault()
      activeView = 'environments'
      return
    }
    if (keyBindingEventMatches(event, 'openPreferences')) {
      event.preventDefault()
      activeView = 'preferences'
      return
    }
    if (keyBindingEventMatches(event, 'openTerminal')) {
      event.preventDefault()
      void openDevTools('terminal')
      return
    }
    if (keyBindingEventMatches(event, 'sendRequest')) {
      event.preventDefault()
      sendRequest()
      return
    }
    if (keyBindingEventMatches(event, 'changeLayout')) {
      event.preventDefault()
      void toggleResponsePaneOrientation()
      return
    }
    if (keyBindingEventMatches(event, 'zoomIn')) {
      event.preventDefault()
      void incrementZoomPercentage(zoomStepPercentage)
      return
    }
    if (keyBindingEventMatches(event, 'zoomOut')) {
      event.preventDefault()
      void incrementZoomPercentage(-zoomStepPercentage)
      return
    }
    if (keyBindingEventMatches(event, 'resetZoom')) {
      event.preventDefault()
      void resetZoomPercentage()
      return
    }
    if (keyBindingEventMatches(event, 'closeBruno')) {
      event.preventDefault()
      closeApplication()
      return
    }
    if (keyBindingEventMatches(event, 'save')) {
      event.preventDefault()
      saveRequest()
      return
    }
    if (keyBindingEventMatches(event, 'saveAllTabs')) {
      event.preventDefault()
      void saveAllOpenTabs()
    }
  }

  load()
</script>

<svelte:window on:keydown={shortcut} on:click={closeVariableTooltipOnOutside} />

{#if loading}
  <main class="boot">
    <div class="loader"></div>
    <p>{loadingStatus}</p>
  </main>
{:else if state}
  <main class="app-shell" class:sidebar-collapsed={sidebarCollapsed} style={`--sidebar-width: ${sidebarWidth}px;`} inert={creationOpen || commandPaletteOpen}>
    <aside class="workspace-rail" aria-label="Collections sidebar">
      <div class="brand">
        <div class="brand-mark">LA</div>
        <div>
          <h1>LiteAPI</h1>
          <p>Local-first API workbench</p>
        </div>
      </div>

      <section class="rail-section rail-create">
        <button class="primary new-request-button" type="button" on:click={(event) => openCreationFlow(event.currentTarget as HTMLElement)} aria-haspopup="dialog">
          <span aria-hidden="true">+</span> New
          <kbd>⌘N</kbd>
        </button>
        <small>Create a scratch request in the active collection.</small>
      </section>

      <section class="rail-section search-section">
        <span class="field-label">Search</span>
        <div class="search-box">
          <input aria-label="Search requests" placeholder="Find requests" bind:this={requestSearchInput} bind:value={requestSearch} />
          {#if requestSearch}
            <button class="icon-button ghost" title="Clear search" on:click={() => (requestSearch = '')}>x</button>
          {/if}
        </div>
        {#if requestSearch.trim()}
          <small>{sidebarSearchCount} matching requests</small>
        {/if}
      </section>

      <section class="collections">
        {#if visibleSidebarCollections.length === 0}
          <div class="sidebar-empty">No matching requests</div>
        {/if}
        {#each visibleSidebarCollections as collection (collection.id)}
          {@const groups = groupedItems(collection, searchQuery)}
          {@const collectionCollapsed = !searchQuery && Boolean(collapsedSidebarCollections[collection.id])}
          <article class:active={collection.id === activeCollection?.id}>
            <header>
              <button
                class="tree-chevron"
                class:collapsed={collectionCollapsed}
                type="button"
                aria-expanded={!collectionCollapsed}
                aria-label={`${collectionCollapsed ? 'Expand' : 'Collapse'} ${collection.name}`}
                on:click={() => toggleSidebarCollection(collection.id)}
              >▾</button>
              <button class="collection-title" on:click={() => selectCollection(collection.id)}>{collection.name}</button>
              <span class="collection-badges">
                {#if collectionIsScratch(collection)}<small>Scratch</small>{/if}
                {#if collection.remote}<small>Git</small>{/if}
                {#if collection.notFoundLocally}<small>Not cloned</small>{/if}
                <small>{collection.format}</small>
              </span>
            </header>
            {#if collection.notFoundLocally}
              <div class="ghost-row">
                <code>{collection.remote}</code>
                <div class="button-row compact">
                  <button on:click={() => selectCollection(collection.id)}>Details</button>
                  <button on:click={() => copyText(collection.remote ?? '')}>Copy URL</button>
                  <button on:click={() => disconnectGitRemote(collection.id)}>Remove</button>
                </div>
              </div>
            {:else if groups.length === 0 && !collectionCollapsed}
              <div class="sidebar-empty">No requests</div>
            {/if}
            {#if !collection.notFoundLocally && !collectionCollapsed}
              {#each groups as group (group.folder)}
                {@const folderCollapsed = Boolean(group.folder) && !searchQuery && Boolean(collapsedSidebarFolders[sidebarFolderKey(collection.id, group.folder)])}
                {#if group.folder}
                  <div class="folder-row-shell">
                    <button
                      class="tree-chevron"
                      class:collapsed={folderCollapsed}
                      type="button"
                      aria-expanded={!folderCollapsed}
                      aria-label={`${folderCollapsed ? 'Expand' : 'Collapse'} folder ${group.folder}`}
                      on:click={() => toggleSidebarFolder(collection.id, group.folder)}
                    >▾</button>
                    <button class="folder-row" title={`${group.folder} settings`} on:click={() => selectFolderSettings(collection, group.folder)}>{group.folder}</button>
                    <button
                      class="folder-action"
                      type="button"
                      title={revealInFolderLabel()}
                      aria-label={`${revealInFolderLabel()} ${group.folder}`}
                      data-testid="collection-item-menu-show-in-folder"
                      on:click={() => revealFolderInFolder(collection, group.folder)}
                    >F</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Info"
                      aria-label="Info"
                      data-testid="collection-item-menu-info"
                      on:click={() => openFolderInfoModal(collection, group.folder)}
                    >i</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Open in Terminal"
                      aria-label={`Open ${group.folder} in Terminal`}
                      data-testid="collection-item-menu-open-terminal"
                      on:click={() => openFolderInTerminal(collection, group.folder)}
                    >T</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="New Folder"
                      data-testid="collection-item-menu-new-folder"
                      on:click={() => openNewFolderModal(group.folder, collection)}
                    >+</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Rename"
                      aria-label="Rename"
                      data-testid="collection-item-menu-rename"
                      on:click={() => openRenameFolderModal(collection, group.folder)}
                    >✎</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Clone"
                      aria-label="Clone"
                      data-testid="collection-item-menu-clone"
                      on:click={() => openCloneFolderModal(collection, group.folder)}
                    >C</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Delete"
                      aria-label="Delete"
                      data-testid="collection-item-menu-delete"
                      on:click={() => openDeleteFolderModal(collection, group.folder)}
                    >x</button>
                  </div>
                {/if}
                {#if !folderCollapsed}
                {#each group.items as item (item.id)}
                  <div class="request-row-shell" class:in-folder={Boolean(group.folder)}>
                    <button
                      class="request-row"
                      class:item-active={item.id === activeRequest?.id}
                      title={group.folder ? `${group.folder} · ${item.url}` : item.url}
                      on:click={() => openRequestTab(collection.id, item.id)}
                    >
                      <span class="method" data-method={item.method}>{methodLabel(item.method)}</span>
                      <span>{item.name}</span>
                      {#if requestIsTransient(collection, item)}<em>temp</em>{/if}
                      {#if item.draft}<em>draft</em>{/if}
                    </button>
                    <details class="request-actions" data-testid="request-actions-menu">
                      <summary data-testid="request-actions-menu-toggle" aria-label={`More actions for ${item.name}`} title={`More actions for ${item.name}`}>More</summary>
                    <button
                      class="request-action"
                      type="button"
                      title={revealInFolderLabel()}
                      aria-label={`${revealInFolderLabel()} ${item.name}`}
                      data-testid="collection-item-menu-show-in-folder"
                      on:click={() => { closeRequestActionMenus(); void revealRequestInFolder(collection, item) }}
                    >Reveal</button>
                    {#if requestSupportsGenerateCode(item)}
                      <button
                        class="request-action"
                        type="button"
                        title="Generate Code"
                        aria-label={`Generate Code ${item.name}`}
                        data-testid="collection-item-menu-generate-code"
                        on:click={() => { closeRequestActionMenus(); void beginGenerateRequestCode(collection, item) }}
                      >Code</button>
                    {/if}
                    <button
                      class="request-action"
                      type="button"
                      title="Info"
                      aria-label={`Info ${item.name}`}
                      data-testid="collection-item-menu-info"
                      on:click={() => { closeRequestActionMenus(); openRequestInfoModal(collection, item) }}
                    >Info</button>
                    <button
                      class="request-action"
                      type="button"
                      title="Rename"
                      aria-label={`Rename ${item.name}`}
                      data-testid="collection-item-menu-rename"
                      on:click={() => { closeRequestActionMenus(); openRenameRequestModal(collection, item) }}
                    >Rename</button>
                    <button
                      class="request-action"
                      type="button"
                      title="Clone"
                      aria-label={`Clone ${item.name}`}
                      data-testid="collection-item-menu-clone"
                      on:click={() => { closeRequestActionMenus(); openCloneRequestModal(collection, item) }}
                    >Clone</button>
                    <button
                      class="request-action danger-inline"
                      type="button"
                      title="Delete"
                      aria-label={`Delete ${item.name}`}
                      data-testid="collection-item-menu-delete"
                      on:click={() => { closeRequestActionMenus(); openDeleteRequestModal(collection, item) }}
                    >Delete</button>
                    </details>
                  </div>
                  {#if (item.examples ?? []).length > 0}
                    <div class="sidebar-examples" aria-label={`Response examples for ${item.name}`}>
                      {#each item.examples ?? [] as example (responseExampleIdentifier(example))}
                        <button
                          class="sidebar-example-row"
                          class:item-active={responseExampleIsActive(collection.id, item.id, example)}
                          title={example.description || example.request?.url || example.name}
                          on:click={() => openResponseExampleTabFor(collection.id, item.id, example)}
                        >
                          <span class="example-glyph">Ex</span>
                          <span>{example.name}</span>
                          <small class={responseStatusClass(example.response.status)}>{example.response.status}</small>
                        </button>
                      {/each}
                    </div>
                  {/if}
                {/each}
                {/if}
              {/each}
            {/if}
          </article>
        {/each}
      </section>

    </aside>
    <input
      type="range"
      class="sidebar-resizer"
      aria-label="Resize collection sidebar"
      min="220"
      max="420"
      value={sidebarWidth}
      title="Drag to resize sidebar; double-click to reset"
      on:mousedown={startSidebarResize}
      on:dblclick={() => { sidebarWidth = 312; persistWorkbenchLayout() }}
      on:input={(event) => (sidebarWidth = clampSidebarWidth(Number(event.currentTarget.value)))}
      on:change={persistWorkbenchLayout}
    />

    <section class="main-pane">
      <header class="topbar">
        <WorkspaceCommandBar
          {sidebarCollapsed}
          workspaceName={activeWorkspace?.name ?? 'Workspace'}
          workspaceOptions={state.workspaces.map((workspace) => ({ id: workspace.id, name: workspace.name }))}
          workspaceValue={state.activeWorkspaceId}
          collectionName={activeCollection?.name ?? 'No collection'}
          requestName={activeRequest?.name ?? 'No request'}
          {activeView}
          globalEnvironmentOptions={(activeWorkspace?.globalEnvironments ?? []).map((environment) => ({ id: environment.id, name: environment.name }))}
          environmentOptions={(activeCollection?.environments ?? []).map((environment) => ({ id: environment.id, name: environment.name }))}
          globalEnvironmentValue={activeWorkspace?.activeGlobalEnvironmentId ?? ''}
          environmentValue={selectedEnvironmentId}
          globalEnvironmentName={activeGlobalEnvironment?.name ?? 'none'}
          environmentName={selectedEnvironmentId ? (selectedEnvironment?.name ?? 'No environment') : 'No environment'}
          notificationCount={unreadNotificationCount}
          gitConnected={Boolean(activeCollection?.remote)}
          runningCollectionName={activeCollectionRun?.collectionName ?? ''}
          cancellingRun={collectionRunCancellationRequested}
          canCreateRequest={Boolean(activeCollection)}
          canCreateFolder={Boolean(activeCollection && !activeCollection.notFoundLocally)}
          onCommand={runWorkbenchCommand}
          onWorkspaceChange={setActiveWorkspace}
          onGlobalEnvironmentChange={setActiveGlobalEnvironment}
          onEnvironmentChange={(environmentID) => { selectedEnvironmentId = environmentID }}
        >
          <svelte:fragment slot="recovery">
            {#if recoveryEntries.length > 0}
              <details class="recovery-center" aria-live="polite">
                <summary aria-label={`${recoveryEntries.length} recoverable deletion${recoveryEntries.length === 1 ? '' : 's'}`}>Recovery ({recoveryEntries.length})</summary>
                <div class="recovery-center-list" aria-label="Recoverable deletions">
                  {#each recoveryEntries as entry (entry.id)}
                    <article>
                      <div>
                        <strong>{entry.displayName}</strong>
                        <span>{entry.kind} · {recoveryExpiryLabel(entry)}</span>
                      </div>
                      <div class="button-row compact">
                        <button type="button" aria-label={`Restore ${entry.kind} ${entry.displayName}`} on:click={() => void restoreRecoveryEntry(entry)} disabled={recoveryBusyEntryID !== ''}>Restore</button>
                        <button type="button" class="danger-button" aria-label={`Remove recovery copy for ${entry.displayName}`} on:click={() => void discardRecoveryEntry(entry)} disabled={recoveryBusyEntryID !== ''}>Remove recovery copy</button>
                      </div>
                    </article>
                  {/each}
                </div>
              </details>
            {/if}
          </svelte:fragment>
        </WorkspaceCommandBar>
        {#if (state.openTabs ?? []).length > 0}
          <nav class="tabs" aria-label="Open tabs">
            {#each state.openTabs as tab (tab.id)}
              <div class="tab" class:active={tab.id === state.activeTabId}>
                <button class="tab-select" title={tabLabel(tab)} on:click={() => setActiveTab(tab.id)}>
                  {#if tabMethod(tab)}
                    <span class="tab-method" data-method={tabMethod(tab)}>{methodLabel(tabMethod(tab))}</span>
                  {/if}
                  <span class="tab-name">{tabLabel(tab)}</span>
                </button>
                <button
                  class="tab-close"
                  type="button"
                  aria-label={`Close tab ${tabLabel(tab)}`}
                  title="Close tab"
                  on:click={() => beginTabLifecycleAction('close-active', tab.id)}
                >×</button>
              </div>
            {/each}
          </nav>
        {/if}
      </header>

      {#if nativeWindowError}
        <div class="error-banner" role="alert" aria-live="assertive">{nativeWindowError}</div>
      {/if}

      {#if error}
        <div class="error-banner" role="alert" aria-live="assertive">{error}</div>
      {/if}

      {#snippet devToolsPanel()}
        <section class="panel devtools-panel" aria-label="Dev Tools">
          <header class="panel-header">
            <div>
              <h2>Dev Tools</h2>
              <p class="panel-subtitle">Console · Network · Performance · Terminal</p>
            </div>
            <div class="runner-summary">
              <span>{devToolsConsoleRows.length} logs</span>
              <span>{devToolsNetworkRows.length} requests</span>
              <button type="button" on:click={refreshDevToolsSnapshot}>Refresh</button>
              <button type="button" aria-label="Close console" on:click={closeDevTools}>Close</button>
            </div>
          </header>
          <nav class="devtools-tabs" aria-label="Dev Tools tabs">
            {#each devToolsTabs as tab (tab.id)}
              <button type="button" class:active={devToolsTab === tab.id} on:click={() => selectDevToolsTab(tab.id)}>{tab.label}</button>
            {/each}
          </nav>
          <div class="devtools-content">
            {#if devToolsTab === 'console'}
              {#if devToolsConsoleRows.length === 0}
                <div class="empty-state devtools-empty">
                  <strong>No logs to display</strong>
                  <span>Logs will appear here as your application runs</span>
                </div>
              {:else}
                <div class="console-log-list devtools-console-list" aria-label="DevTools console logs">
                  {#each devToolsConsoleRows as log, index (index)}
                    <div class={`console-row ${log.level}`}>
                      <span>{log.level}</span>
                      <div>
                        <code>{log.message}</code>
                        <small>{log.source}</small>
                      </div>
                    </div>
                  {/each}
                </div>
              {/if}
            {:else if devToolsTab === 'network'}
              <div class="network-filter-bar" aria-label="Filter requests by method">
                <div>
                  <strong>Filter by Method</strong>
                  <span>{devToolsNetworkActiveFilterCount === devToolsNetworkMethods.length ? 'All' : `${devToolsNetworkActiveFilterCount}/${devToolsNetworkMethods.length}`}</span>
                </div>
                <div class="button-row compact">
                  <button type="button" on:click={() => setAllDevToolsNetworkFilters(false)}>Hide All</button>
                  <button type="button" on:click={() => setAllDevToolsNetworkFilters(true)}>Show All</button>
                </div>
                <div class="method-filter-list">
                  {#each devToolsNetworkMethods as method (method)}
                    <label>
                      <input type="checkbox" checked={devToolsNetworkFilters[method]} on:change={(event) => setDevToolsNetworkFilter(method, event.currentTarget.checked)} />
                      <span>{method} {devToolsNetworkMethodCounts[method] ?? 0}</span>
                    </label>
                  {/each}
                </div>
              </div>
              {#if devToolsNetworkRows.length === 0}
                <div class="empty-state devtools-empty">
                  <strong>No network requests</strong>
                  <span>Requests will appear here as you make API calls</span>
                </div>
              {:else}
                <div class="network-layout" style={`--network-details-width: ${devToolsDetailsPanelWidth}px;`}>
                  <div class="table-scroll network-table-scroll" class:resizing={devToolsNetworkResizingColumn >= 0}>
                    <table class="devtools-network-table" style={`min-width: ${devToolsNetworkTableWidth}px;`}>
                      <colgroup>
                        {#each devToolsNetworkColumnWidths as width, index (index)}
                          <col style={`width: ${width}px;`} />
                        {/each}
                      </colgroup>
                      <thead>
                        <tr>
                          {#each devToolsNetworkColumns as column, index (column.key)}
                            <th aria-sort={devToolsNetworkAriaSort[column.key]}>
                              <button type="button" class="network-sort-button" on:click={() => cycleDevToolsNetworkSort(column.key)}>{column.label} {devToolsNetworkSortLabels[column.key]}</button>
                              {#if index < devToolsNetworkColumns.length - 1}
                                <button
                                  type="button"
                                  class="column-resizer"
                                  class:active={devToolsNetworkResizingColumn === index}
                                  aria-label={`Resize ${column.label} column`}
                                  on:mousedown={(event) => startDevToolsNetworkColumnResize(index, event)}
                                ></button>
                              {/if}
                            </th>
                          {/each}
                        </tr>
                      </thead>
                      <tbody>
                        {#each devToolsNetworkRows as row (row.id)}
                          <tr class:selected={selectedDevToolsNetworkRow?.id === row.id}>
                            <td><button class="table-link" type="button" on:click={() => selectDevToolsNetworkRow(row)}>{normalizedNetworkMethod(row)}</button></td>
                            <td>{statusDisplay(row.status)}</td>
                            <td>{devToolsNetworkDomain(row)}</td>
                            <td><code>{devToolsNetworkPath(row)}</code></td>
                            <td>{networkLogTime(row)}</td>
                            <td>{row.durationMs} ms</td>
                            <td>{formatNetworkSize(row.size)}</td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  </div>
                  {#if selectedDevToolsNetworkRow}
                    <section class="network-details-panel" aria-label="Request Details">
                      <button
                        type="button"
                        class="details-panel-resizer"
                        aria-label="Resize request details"
                        on:mousedown={startDevToolsDetailsPanelResize}
                      ></button>
                    <header>
                      <h3>Request Details</h3>
                      <div class="subtabs">
                        {#each devToolsNetworkDetailTabs as detailTab (detailTab.id)}
                          <button type="button" class:active={devToolsNetworkDetailTab === detailTab.id} on:click={() => (devToolsNetworkDetailTab = detailTab.id)}>{detailTab.label}</button>
                        {/each}
                      </div>
                    </header>
                    {#if devToolsNetworkDetailTab === 'request'}
                      <div class="network-detail-content">
                        <h4>General</h4>
                        <dl class="detail-list">
                          <div><dt>Request URL:</dt><dd>{selectedDevToolsNetworkRow.url}</dd></div>
                          <div><dt>Request Method:</dt><dd>{normalizedNetworkMethod(selectedDevToolsNetworkRow)}</dd></div>
                        </dl>
                        <h4>Request Headers</h4>
                        {#if networkHeaderRows(selectedDevToolsNetworkRow.requestHeaders).length === 0}
                          <div class="empty-state compact">No headers</div>
                        {:else}
                          <table class="details-table">
                            <thead><tr><th>Name</th><th>Value</th></tr></thead>
                            <tbody>
                              {#each networkHeaderRows(selectedDevToolsNetworkRow.requestHeaders) as [name, value] (name)}
                                <tr><td>{name}</td><td><code>{value}</code></td></tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        <h4>Request Body</h4>
                        {#if networkLogBody(selectedDevToolsNetworkRow.requestBody)}
                          <pre class="network-body">{networkLogBody(selectedDevToolsNetworkRow.requestBody)}</pre>
                        {:else}
                          <div class="empty-state compact">No body</div>
                        {/if}
                      </div>
                    {:else if devToolsNetworkDetailTab === 'response'}
                      <div class="network-detail-content">
                        <h4>Response Headers</h4>
                        {#if networkHeaderRows(selectedDevToolsNetworkRow.responseHeaders).length === 0}
                          <div class="empty-state compact">No headers</div>
                        {:else}
                          <table class="details-table">
                            <thead><tr><th>Name</th><th>Value</th></tr></thead>
                            <tbody>
                              {#each networkHeaderRows(selectedDevToolsNetworkRow.responseHeaders) as [name, value] (name)}
                                <tr><td>{name}</td><td><code>{value}</code></td></tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        <h4>Response Body</h4>
                        {#if networkLogBody(selectedDevToolsNetworkRow.responseBody)}
                          <pre class="network-body">{networkLogBody(selectedDevToolsNetworkRow.responseBody)}</pre>
                        {:else}
                          <div class="empty-state compact">No response data</div>
                        {/if}
                      </div>
                    {:else}
                      <div class="network-detail-content">
                        <h4>Network Logs</h4>
                        {#if networkLogLines(selectedDevToolsNetworkRow).length === 0}
                          <div class="empty-state compact">No network logs available</div>
                        {:else}
                          <div class="progress-log">
                            {#each networkLogLines(selectedDevToolsNetworkRow) as line, index (index)}
                              <div class="progress-row"><span>net</span><code>{line}</code></div>
                            {/each}
                          </div>
                        {/if}
                      </div>
                    {/if}
                    </section>
                  {/if}
                </div>
              {/if}
            {:else if devToolsTab === 'performance'}
              <div class="performance-toolbar">
                <label>
                  <span>View:</span>
                  <select aria-label="Performance process view" bind:value={devToolsPerformanceView}>
                    <option value="cumulative">Cumulative (All Processes)</option>
                    {#each devToolsPerformanceProcesses as process (process.pid)}
                      <option value={String(process.pid)}>PID {process.pid} - {process.title || 'LiteAPI'} ({process.type || 'main'})</option>
                    {/each}
                  </select>
                </label>
                <button type="button" on:click={refreshDevToolsSnapshot}>Refresh</button>
              </div>
              <h3>System Resources</h3>
              <div class="resource-cards">
                <article>
                  <span>CPU Usage</span>
                  <strong>{formatCPUPercent(displayedDevToolsCPUPercent)}</strong>
                  <small>{selectedDevToolsPerformanceProcess ? 'Current CPU usage' : 'Total CPU usage'}</small>
                </article>
                <article>
                  <span>Memory Usage</span>
                  <strong>{formatRuntimeBytes(displayedDevToolsMemoryBytes)}</strong>
                  <small>{selectedDevToolsPerformanceProcess ? 'Current memory usage' : 'Total memory usage'}</small>
                </article>
                <article>
                  <span>Uptime</span>
                  <strong>{formatUptime(displayedDevToolsUptimeSeconds)}</strong>
                  <small>Process runtime</small>
                </article>
                <article>
                  <span>Process ID</span>
                  <strong>{displayedDevToolsPID ?? '-'}</strong>
                  <small>{selectedDevToolsPerformanceProcess ? 'Process PID' : 'Main process PID'}</small>
                </article>
                <article>
                  <span>Heap Alloc</span>
                  <strong>{formatRuntimeBytes(devToolsSnapshot?.heapAllocBytes)}</strong>
                  <small>Go heap allocation</small>
                </article>
                <article>
                  <span>Goroutines</span>
                  <strong>{devToolsSnapshot?.goroutines ?? '-'}</strong>
                  <small>Runtime workers</small>
                </article>
              </div>
            {:else}
              <div class="terminal-shell">
                <header>
                  <h3>Terminal</h3>
                  <button type="button" on:click={createTerminalSession} disabled={terminalBusy}>
                    {terminalBusy ? 'Starting...' : 'New Terminal Session'}
                  </button>
                </header>
                <div class="terminal-body">
                  <aside>
                    <strong>Sessions</strong>
                    {#if terminalSessions.length === 0}
                      <div class="empty-state">No active sessions</div>
                    {:else}
                      <div class="terminal-session-list">
                        {#each terminalSessions as session (session.id)}
                          <div class:active={terminalActiveSessionId === session.id} class="terminal-session-row">
                            <button type="button" class="terminal-session-button" on:click={() => selectTerminalSession(session.id)}>
                              <strong>{terminalSessionLabel(session)}</strong>
                              <span>{terminalSessionStatus(session)}</span>
                              <small>{session.cwd}</small>
                            </button>
                            <button type="button" class="icon-button subtle" title="Close terminal session" aria-label="Close terminal session" on:click={() => closeTerminalSession(session.id)}>×</button>
                          </div>
                        {/each}
                      </div>
                    {/if}
                  </aside>
                  <section>
                    {#if activeTerminalSession}
                      <div class="terminal-status">
                        <span>{activeTerminalSession.cwd}</span>
                        <strong>{terminalSessionStatus(activeTerminalSession)}</strong>
                      </div>
                      <pre class="terminal-output" aria-label="Terminal output">{terminalDisplayOutput(terminalOutput) || ' '}</pre>
                      <form class="terminal-input-row" on:submit|preventDefault={sendTerminalInput}>
                        <input aria-label="Terminal input" bind:value={terminalInput} disabled={activeTerminalSession.exited} placeholder="Type a command and press Enter" />
                        <button type="submit" disabled={activeTerminalSession.exited || !terminalInput.trim()}>Send</button>
                      </form>
                      {#if terminalError}
                        <p class="error-text">{terminalError}</p>
                      {/if}
                    {:else}
                      <div class="empty-state">No terminal session selected</div>
                      {#if terminalError}
                        <p class="error-text">{terminalError}</p>
                      {/if}
                    {/if}
                  </section>
                </div>
              </div>
            {/if}
          </div>
        </section>
      {/snippet}

      {#if activeView === 'request' && activeRequest && activeCollection}
        <section
          class="request-workbench"
          class:vertical-layout={responsePaneOrientation === 'vertical'}
          data-orientation={responsePaneOrientation}
          style={`--request-pane-size: ${Math.round(responseSplit * 100)}%;`}
        >
          <RequestCommandStrip
            command={requestCommand}
            actions={{
              onSave: saveRequest,
              onSend: sendRequest,
              onRun: runCollection,
              onCancel: cancelActiveRequest,
              onCancelBackground: cancelHTTPTransport,
              onToggleOrientation: toggleResponsePaneOrientation
            }}
            disabled={busy !== '' || hasActiveHTTPTransport}
            orientation={responsePaneOrientation}
          >
            <svelte:fragment slot="request-line">
              {#snippet variableURLField()}
                <div class="url-variable-editor">
                  <input
                    class="url-variable-input"
                    aria-label={activeRequest.type === 'grpc' ? 'gRPC server URL' : activeRequest.type === 'websocket' ? 'WebSocket URL' : 'URL'}
                    bind:this={requestURLInput}
                    value={activeRequest.url}
                    on:input={patchURLField}
                    on:scroll={syncURLInputScroll}
                    on:keyup={syncURLInputScroll}
                    on:mouseup={syncURLInputScroll}
                  />
                  <div class="url-variable-overlay">
                    <span class="url-variable-overlay-content" style={`transform: translateX(-${urlInputScrollLeft}px);`}>
                      {#each urlVariableSegments(activeRequest.url, requestVariableTooltips, activeRequest.pathParams ?? []) as segment (segment.key)}
                        {#if segment.variable}
                          <span
                            class="url-variable-token-wrapper"
                            class:open={activeVariableTooltip === segment.name}
                          >
                            <span
                              role="button"
                              tabindex="0"
                              class:cm-variable-valid={isValidURLVariableSegment(segment)}
                              class:cm-variable-invalid={!isValidURLVariableSegment(segment)}
                              on:click={() => toggleActiveVariableTooltip(segment.name)}
                              on:keydown={(event) => handleInlineVariableTokenKey(event, segment.name)}
                            >{segment.text}</span>
                            {#if segment.info}
                              <div class="CodeMirror-brunoVarInfo inline-var-tooltip" role="tooltip">
                                <div class="variable-tooltip-title">
                                  <strong class="var-name">{segment.info.name}</strong>
                                  <span class="var-scope-badge">{segment.info.scope}</span>
                                </div>
                                {#if !segment.info.validName}
                                  <small class="var-warning-note">{invalidVariableWarning}</small>
                                {:else if editingVariableTooltip === segment.info.name}
                                  <textarea
                                    class="var-value-editor"
                                    aria-label={'Edit variable ' + segment.info.name}
                                    bind:value={variableTooltipDraft}
                                    on:keydown={(event) => handleVariableTooltipEditorKey(event, segment.info)}
                                    on:blur={(event) => handleVariableTooltipEditorBlur(event, segment.info)}
                                  ></textarea>
                                  <div class="button-row compact">
                                    <button class="var-save-button" on:click|stopPropagation={() => saveVariableTooltipEdit(segment.info)} disabled={busy !== ''}>Save</button>
                                    <button on:click|stopPropagation={cancelVariableTooltipEdit}>Cancel</button>
                                  </div>
                                {:else if segment.info.editable}
                                  <button type="button" class="var-value-editable-display" on:click|stopPropagation={() => beginVariableTooltipEdit(segment.info)}>
                                    {displayTooltipValue(segment.info, Boolean(revealedVariableTooltips[segment.info.name]))}
                                  </button>
                                {:else}
                                  <div class="var-value-editable-display">{displayTooltipValue(segment.info, Boolean(revealedVariableTooltips[segment.info.name]))}</div>
                                {/if}
                                {#if segment.info.readOnly}
                                  <small class="var-readonly-note">read-only</small>
                                {/if}
                                <div class="button-row compact">
                                  <button
                                    class="copy-button"
                                    class:copy-success={copiedVariableTooltips[segment.info.name]}
                                    on:click|stopPropagation={() => copyVariableTooltipValue(segment.info)}
                                    disabled={!segment.info.found || !segment.info.validName || copiedVariableTooltips[segment.info.name]}
                                  >
                                    {copiedVariableTooltips[segment.info.name] ? 'Copied' : 'Copy'}
                                  </button>
                                  {#if segment.info.secret}
                                    <button class="secret-toggle-button" on:click|stopPropagation={() => toggleTooltipSecret(segment.info.name)}>
                                      {revealedVariableTooltips[segment.info.name] ? 'Hide' : 'Show'}
                                    </button>
                                  {/if}
                                </div>
                              </div>
                            {/if}
                          </span>
	                        {:else if segment.prompt}
	                          <span class="cm-variable-prompt">{segment.text}</span>
	                        {:else}
	                          <span>{segment.text}</span>
	                        {/if}
                      {/each}
                    </span>
                  </div>
                </div>
              {/snippet}
              <ProtocolRequestLine
                protocol={activeRequest.type}
                method={activeRequest.method}
                {methods}
                {busy}
                urlField={variableURLField}
                onMethodChange={(method) => patchField('method', method)}
                onGenerateGrpcurl={beginGenerateGrpcurlCommand}
              />
            </svelte:fragment>
          </RequestCommandStrip>
          <div class="request-side">
            <div class="request-variable-region">
            {#if requestVariableTooltips.length > 0}
              <div class="variable-inspector" aria-label="Variable inspector">
                {#each requestVariableTooltips as variableInfo (variableInfo.name)}
                  <div class="variable-chip-wrapper" class:invalid={!variableInfo.found} class:open={activeVariableTooltip === variableInfo.name}>
                    <button type="button" class="variable-chip" on:click={() => toggleActiveVariableTooltip(variableInfo.name)}>
                      <span class="var-token">{'{{' + variableInfo.name + '}}'}</span>
                    </button>
                    <div class="variable-tooltip" role="tooltip">
                      <div class="variable-tooltip-title">
                        <strong class="var-name">{variableInfo.name}</strong>
                        <span class="var-scope-badge">{variableInfo.scope}</span>
                      </div>
                      {#if !variableInfo.validName}
                        <small class="var-warning-note">{invalidVariableWarning}</small>
                      {:else if editingVariableTooltip === variableInfo.name}
                        <textarea
                          class="var-value-editor"
                          aria-label={'Edit variable ' + variableInfo.name}
                          bind:value={variableTooltipDraft}
                          on:keydown={(event) => handleVariableTooltipEditorKey(event, variableInfo)}
                          on:blur={(event) => handleVariableTooltipEditorBlur(event, variableInfo)}
                        ></textarea>
                        <div class="button-row compact">
                          <button class="var-save-button" on:click|stopPropagation={() => saveVariableTooltipEdit(variableInfo)} disabled={busy !== ''}>Save</button>
                          <button on:click|stopPropagation={cancelVariableTooltipEdit}>Cancel</button>
                        </div>
                      {:else if variableInfo.editable}
                        <button type="button" class="var-value-editable-display" on:click|stopPropagation={() => beginVariableTooltipEdit(variableInfo)}>
                          {displayTooltipValue(variableInfo, Boolean(revealedVariableTooltips[variableInfo.name]))}
                        </button>
                      {:else}
                        <div class="var-value-editable-display">{displayTooltipValue(variableInfo, Boolean(revealedVariableTooltips[variableInfo.name]))}</div>
                      {/if}
                      {#if variableInfo.readOnly}
                        <small class="var-readonly-note">read-only</small>
                      {/if}
                      <div class="button-row compact">
                        <button
                          class="copy-button"
                          class:copy-success={copiedVariableTooltips[variableInfo.name]}
                          on:click|stopPropagation={() => copyVariableTooltipValue(variableInfo)}
                          disabled={!variableInfo.found || !variableInfo.validName || copiedVariableTooltips[variableInfo.name]}
                        >
                          {copiedVariableTooltips[variableInfo.name] ? 'Copied' : 'Copy'}
                        </button>
                        {#if variableInfo.secret}
                          <button class="secret-toggle-button" on:click|stopPropagation={() => toggleTooltipSecret(variableInfo.name)}>
                            {revealedVariableTooltips[variableInfo.name] ? 'Hide' : 'Show'}
                          </button>
                        {/if}
                      </div>
                    </div>
                  </div>
                {/each}
              </div>
            {/if}
            </div>

            <div class="subtabs" role="tablist" aria-label="Request sections" tabindex="-1" on:keydown={requestTabKeydown}>
              {#each requestTabs as tab (tab.id)}
                <button
                  class:active={requestPaneTab === tab.id}
                  id={`request-tab-${tab.id}`}
                  data-request-tab={tab.id}
                  role="tab"
                  aria-selected={requestPaneTab === tab.id}
                  aria-controls={`request-panel-${tab.id}`}
                  tabindex={requestPaneTab === tab.id ? 0 : -1}
                  on:click={() => selectRequestPaneTab(tab.id)}
                >
                  {tab.label}
                </button>
              {/each}
            </div>

	            <div class="editor-surface" id={`request-panel-${requestPaneTab}`} role="tabpanel" aria-labelledby={`request-tab-${requestPaneTab}`} tabindex="0">
	              {#if requestPaneTab === 'params'}
	                <div class="param-section-title">Query</div>
	                <KeyValueTable
	                  rows={activeRequest.params}
	                  variableOverlay={true}
                  {activeVariableTooltip}
                  {editingVariableTooltip}
                  bind:variableTooltipDraft
                  {revealedVariableTooltips}
                  {copiedVariableTooltips}
                  {busy}
                  valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
                  {displayTooltipValue}
                  onToggleActive={toggleActiveVariableTooltip}
                  onBeginEdit={beginVariableTooltipEdit}
                  onEditorKey={handleVariableTooltipEditorKey}
                  onEditorBlur={handleVariableTooltipEditorBlur}
                  onSave={saveVariableTooltipEdit}
                  onCancel={cancelVariableTooltipEdit}
                  onCopy={copyVariableTooltipValue}
                  onToggleSecret={toggleTooltipSecret}
                  onAdd={() => addKeyValue('params')}
	                  onChange={(index, field, value) => updateKeyValue('params', index, field, value)}
	                  onRemove={(index) => removeKeyValue('params', index)}
	                />
	                {#if activeRequest.pathParams?.length}
	                  <div class="param-section-title">Path</div>
	                  <KeyValueTable
	                    rows={activeRequest.pathParams}
	                    variableOverlay={true}
	                    readonlyNames={true}
	                    showEnabled={false}
	                    showActions={false}
	                    showAddRow={false}
	                    {activeVariableTooltip}
	                    {editingVariableTooltip}
	                    bind:variableTooltipDraft
	                    {revealedVariableTooltips}
	                    {copiedVariableTooltips}
	                    {busy}
	                    valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
	                    {displayTooltipValue}
	                    onToggleActive={toggleActiveVariableTooltip}
	                    onBeginEdit={beginVariableTooltipEdit}
	                    onEditorKey={handleVariableTooltipEditorKey}
	                    onEditorBlur={handleVariableTooltipEditorBlur}
	                    onSave={saveVariableTooltipEdit}
	                    onCancel={cancelVariableTooltipEdit}
	                    onCopy={copyVariableTooltipValue}
	                    onToggleSecret={toggleTooltipSecret}
	                    onChange={(index, field, value) => updateKeyValue('pathParams', index, field, value)}
	                  />
	                {/if}
	              {:else if requestPaneTab === 'headers'}
                <KeyValueTable
                  rows={activeRequest.headers}
                  variableOverlay={true}
                  {activeVariableTooltip}
                  {editingVariableTooltip}
                  bind:variableTooltipDraft
                  {revealedVariableTooltips}
                  {copiedVariableTooltips}
                  {busy}
                  valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
                  {displayTooltipValue}
                  onToggleActive={toggleActiveVariableTooltip}
                  onBeginEdit={beginVariableTooltipEdit}
                  onEditorKey={handleVariableTooltipEditorKey}
                  onEditorBlur={handleVariableTooltipEditorBlur}
                  onSave={saveVariableTooltipEdit}
                  onCancel={cancelVariableTooltipEdit}
                  onCopy={copyVariableTooltipValue}
                  onToggleSecret={toggleTooltipSecret}
                  onAdd={() => addKeyValue('headers')}
                  onChange={(index, field, value) => updateKeyValue('headers', index, field, value)}
                  onRemove={(index) => removeKeyValue('headers', index)}
                />
              {:else if requestPaneTab === 'body'}
                {#if activeRequest.type === 'grpc'}
                  <div class="grpc-editor">
                    <div class="grpc-method-controls">
                      <button on:click={loadGrpcMethods} disabled={busy !== ''}>Load methods</button>
                      <select aria-label="Discovered gRPC methods" value={activeRequest.method === 'CALL' ? '' : activeRequest.method} on:change={(e) => selectGrpcMethod(e.currentTarget.value)} disabled={grpcMethods.length === 0}>
                        <option value="">Select method</option>
                        {#each grpcMethods as method (method.path)}
                          <option value={method.path}>{method.path} · {method.type || 'unary'}</option>
                        {/each}
                      </select>
                      <button on:click={() => regenerateGrpcMessage(0)} disabled={busy !== '' || !activeRequest.method || activeRequest.method === 'CALL'}>Generate</button>
                    </div>
                    {#if grpcMethodMessage}
                      <small>{grpcMethodMessage}</small>
                    {/if}
                    <div class="field-grid">
                      <span class="field-label">Method type</span>
                      <select value={activeRequest.grpcMethodType} on:change={(e) => patchField('grpcMethodType', e.currentTarget.value)}>
                        {#each grpcMethodTypes as methodType (methodType)}
                          <option value={methodType}>{methodType || 'unspecified'}</option>
                        {/each}
                      </select>
                      <span class="field-label">Proto path</span>
                      <input value={activeRequest.protoPath} placeholder="protos/service.proto" on:change={(e) => patchField('protoPath', e.currentTarget.value)} />
                    </div>

                    {#if grpcStreamingRequest}
                      <div class="ws-live-controls">
                        <div class="ws-live-status" class:connected={grpcStreamConnected}>
                          <span>{grpcStreamConnected ? 'Streaming' : grpcStreamEnded ? 'Ended' : 'Stopped'}</span>
                          {#if grpcStreamEventCount}
                            <small>{grpcStreamEventCount} events</small>
                          {/if}
                        </div>
                        <div class="button-row compact">
                          <button on:click={connectActiveGrpcStream} disabled={busy !== '' || grpcStreamConnected}>Start stream</button>
                          <button on:click={endActiveGrpcStream} disabled={busy !== '' || !grpcStreamConnected}>End</button>
                          <button on:click={cancelActiveGrpcStream} disabled={busy !== '' || !grpcStreamConnected}>Cancel</button>
                        </div>
                      </div>
                    {/if}

                    <table class="grpc-messages">
                      <thead>
                        <tr><th>Name</th><th>Content</th><th></th></tr>
                      </thead>
                      <tbody>
                        {#each activeRequest.grpcMessages ?? [] as message, index (index)}
                          <tr>
                            <td><input value={message.name} on:input={(e) => updateGrpcMessage(index, 'name', e.currentTarget.value)} /></td>
                            <td><textarea class="short" spellcheck="false" value={message.content} on:input={(e) => updateGrpcMessage(index, 'content', e.currentTarget.value)}></textarea></td>
                            <td>
                              <div class="button-row compact">
                                {#if grpcClientStreamingRequest}
                                  <button class="icon-button" title="Send message" on:click={() => sendGrpcStreamMessage(index)} disabled={busy !== '' || !grpcStreamConnected}>Send</button>
                                {/if}
                                <button class="icon-button" title="Generate sample" on:click={() => regenerateGrpcMessage(index)}>Gen</button>
                                <button class="icon-button" title="Remove message" on:click={() => removeGrpcMessage(index)}>x</button>
                              </div>
                            </td>
                          </tr>
                        {/each}
                      </tbody>
	                    </table>
	                    <button on:click={addGrpcMessage}>Add message</button>
	                  </div>
	                {:else if activeRequest.type === 'websocket'}
	                  <div class="ws-editor">
	                    <div class="ws-live-controls">
	                      <div class="ws-live-status" class:connected={websocketConnected}>
	                        <span>{websocketConnected ? 'Connected' : 'Disconnected'}</span>
	                        {#if websocketEventCount}
	                          <small>{websocketEventCount} events</small>
	                        {/if}
	                      </div>
	                      <div class="button-row compact">
	                        <button on:click={connectActiveWebSocket} disabled={busy !== ''}>Connect</button>
	                        <button on:click={() => sendWSMessage(selectedWSMessageIndex(activeRequest))} disabled={busy !== ''}>Send selected</button>
	                        <button on:click={disconnectActiveWebSocket} disabled={busy !== '' || !websocketConnected}>Disconnect</button>
	                      </div>
	                    </div>
	                    {#if (activeRequest.wsMessages ?? []).length === 0}
	                      <div class="empty-state">No WebSocket messages</div>
	                    {:else}
	                      <table class="ws-messages">
	                        <thead>
	                          <tr><th>Send</th><th>Name</th><th>Type</th><th>Content</th><th></th></tr>
	                        </thead>
	                        <tbody>
	                          {#each activeRequest.wsMessages ?? [] as message, index (index)}
	                            <tr>
	                              <td><input type="checkbox" checked={message.selected} on:change={(e) => updateWSMessage(index, 'selected', e.currentTarget.checked)} /></td>
	                              <td><input value={message.name} on:change={(e) => updateWSMessage(index, 'name', e.currentTarget.value)} /></td>
	                              <td>
	                                <select value={message.type || 'text'} on:change={(e) => updateWSMessage(index, 'type', e.currentTarget.value)}>
	                                  {#each wsMessageTypes as messageType (messageType)}
	                                    <option value={messageType}>{messageType}</option>
	                                  {/each}
	                                </select>
	                              </td>
	                              <td><textarea class="short" spellcheck="false" value={message.content} on:change={(e) => updateWSMessage(index, 'content', e.currentTarget.value)}></textarea></td>
	                              <td>
	                                <div class="button-row compact">
	                                  <button class="icon-button" title="Send message" on:click={() => sendWSMessage(index)} disabled={busy !== ''}>Send</button>
	                                  <button class="icon-button" title="Remove message" on:click={() => removeWSMessage(index)}>x</button>
	                                </div>
	                              </td>
	                            </tr>
	                          {/each}
	                        </tbody>
	                      </table>
	                    {/if}
	                    <button on:click={addWSMessage}>Add message</button>
	                  </div>
	                {:else}
	                  <div class="field-row">
                    <span class="field-label">Body mode</span>
                    <select value={activeRequest.body.mode} on:change={(e) => updateBody({ mode: e.currentTarget.value })}>
                      {#each bodyModes as mode (mode)}
                        <option value={mode}>{mode}</option>
                      {/each}
                    </select>
                  </div>
                  {#if activeRequest.body.mode === 'json'}
                    <CodeEditor editorKey={`${activeRequest.id}:body-json`} value={activeRequest.body.json ?? ''} language="json" ariaLabel="JSON request body" testId="request-body-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => updateBody({ json: value })} />
                  {:else if activeRequest.body.mode === 'xml'}
                    <CodeEditor editorKey={`${activeRequest.id}:body-xml`} value={activeRequest.body.xml ?? ''} language="xml" ariaLabel="XML request body" testId="request-body-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => updateBody({ xml: value })} />
                  {:else if activeRequest.body.mode === 'graphql'}
                    <CodeEditor editorKey={`${activeRequest.id}:body-graphql-query`} value={activeRequest.body.graphqlQuery ?? ''} language="graphql" ariaLabel="GraphQL query" testId="request-body-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => updateBody({ graphqlQuery: value })} />
                    <CodeEditor editorKey={`${activeRequest.id}:body-graphql-variables`} value={activeRequest.body.graphqlVariables ?? ''} language="json" ariaLabel="GraphQL variables" testId="request-body-variables-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => updateBody({ graphqlVariables: value })} />
                  {:else if activeRequest.body.mode === 'text' || activeRequest.body.mode === 'sparql'}
                    <CodeEditor editorKey={`${activeRequest.id}:body-text`} value={activeRequest.body.text ?? ''} language="text" ariaLabel="Text request body" testId="request-body-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => updateBody({ text: value })} />
	                  {:else if activeRequest.body.mode === 'formUrlEncoded'}
	                    <KeyValueTable
	                      rows={activeRequest.body.formUrlEncoded ?? []}
	                      variableOverlay={true}
	                      multilineValues={true}
	                      {activeVariableTooltip}
	                      {editingVariableTooltip}
	                      bind:variableTooltipDraft
	                      {revealedVariableTooltips}
	                      {copiedVariableTooltips}
	                      {busy}
	                      valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
	                      {displayTooltipValue}
	                      onToggleActive={toggleActiveVariableTooltip}
	                      onBeginEdit={beginVariableTooltipEdit}
	                      onEditorKey={handleVariableTooltipEditorKey}
	                      onEditorBlur={handleVariableTooltipEditorBlur}
	                      onSave={saveVariableTooltipEdit}
	                      onCancel={cancelVariableTooltipEdit}
	                      onCopy={copyVariableTooltipValue}
	                      onToggleSecret={toggleTooltipSecret}
	                      onAdd={addFormUrlEncodedRow}
	                      onChange={updateFormUrlEncodedRow}
	                      onRemove={removeFormUrlEncodedRow}
	                    />
	                  {:else if activeRequest.body.mode === 'multipartForm'}
	                    <MultipartTable
	                      rows={activeRequest.body.multipart ?? []}
	                      {activeVariableTooltip}
	                      {editingVariableTooltip}
	                      bind:variableTooltipDraft
	                      {revealedVariableTooltips}
	                      {copiedVariableTooltips}
	                      {busy}
	                      valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
	                      {displayTooltipValue}
	                      onToggleActive={toggleActiveVariableTooltip}
	                      onBeginEdit={beginVariableTooltipEdit}
	                      onEditorKey={handleVariableTooltipEditorKey}
	                      onEditorBlur={handleVariableTooltipEditorBlur}
	                      onSave={saveVariableTooltipEdit}
	                      onCancel={cancelVariableTooltipEdit}
	                      onCopy={copyVariableTooltipValue}
	                      onToggleSecret={toggleTooltipSecret}
	                      onAdd={addMultipartRow}
	                      onChange={updateMultipartRow}
	                      onRemove={removeMultipartRow}
	                    />
	                  {:else if activeRequest.body.mode === 'file'}
	                    <FileBodyTable
	                      rows={fileBodyRows(activeRequest.body)}
	                      onAdd={addFileBodyRow}
	                      onChange={updateFileBodyRow}
	                      onRemove={removeFileBodyRow}
	                    />
	                  {:else}
                    <div class="empty-state">No body payload</div>
                  {/if}
                {/if}
              {:else if requestPaneTab === 'auth'}
                <div class="field-grid">
                  <span class="field-label">Mode</span>
                  <select value={activeRequest.auth.mode} on:change={(e) => updateAuth({ mode: e.currentTarget.value })}>
                    {#each authModes as mode (mode)}
                      <option value={mode}>{mode}</option>
                    {/each}
                  </select>
	                  {#if activeRequest.auth.mode === 'basic' || activeRequest.auth.mode === 'digest' || activeRequest.auth.mode === 'wsse' || activeRequest.auth.mode === 'ntlm'}
                    <span class="field-label">Username</span>
                    <input value={activeRequest.auth.username} on:input={(e) => updateAuth({ username: e.currentTarget.value })} />
                    <span class="field-label">Password</span>
                    <input type="password" value={activeRequest.auth.password} on:input={(e) => updateAuth({ password: e.currentTarget.value })} />
                    {#if activeRequest.auth.mode === 'ntlm'}
                      <span class="field-label">Domain</span>
                      <input value={activeRequest.auth.domain} on:change={(e) => updateAuth({ domain: e.currentTarget.value })} />
                    {/if}
                  {:else if activeRequest.auth.mode === 'bearer'}
                    <span class="field-label">Token</span>
                    <input type="password" value={activeRequest.auth.token} on:input={(e) => updateAuth({ token: e.currentTarget.value })} />
                  {:else if activeRequest.auth.mode === 'oauth2'}
                    <span class="field-label">Grant</span>
                    <select value={activeRequest.auth.oauth2?.grantType || 'client_credentials'} on:change={(e) => updateOAuth2Auth({ grantType: e.currentTarget.value })}>
                      {#each oauth2GrantTypes as grant (grant)}
                        <option value={grant}>{grant}</option>
                      {/each}
                    </select>
                    {#if activeRequest.auth.oauth2?.grantType === 'authorization_code' || activeRequest.auth.oauth2?.grantType === 'implicit'}
                      <span class="field-label">Callback URL</span>
                      <input value={activeRequest.auth.oauth2?.callbackUrl ?? ''} on:change={(e) => updateOAuth2Auth({ callbackUrl: e.currentTarget.value })} />
                      <span class="field-label">Authorization URL</span>
                      <input value={activeRequest.auth.oauth2?.authorizationUrl ?? ''} on:change={(e) => updateOAuth2Auth({ authorizationUrl: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Access token URL</span>
                    <input value={activeRequest.auth.oauth2?.accessTokenUrl ?? ''} on:change={(e) => updateOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                    <span class="field-label">Client ID</span>
                    <input value={activeRequest.auth.oauth2?.clientId ?? ''} on:change={(e) => updateOAuth2Auth({ clientId: e.currentTarget.value })} />
                    <span class="field-label">Client secret</span>
                    <input type="password" value={activeRequest.auth.oauth2?.clientSecret ?? ''} on:change={(e) => updateOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                    {#if activeRequest.auth.oauth2?.grantType === 'password'}
                      <span class="field-label">Username</span>
                      <input value={activeRequest.auth.oauth2?.username ?? ''} on:change={(e) => updateOAuth2Auth({ username: e.currentTarget.value })} />
                      <span class="field-label">Password</span>
                      <input type="password" value={activeRequest.auth.oauth2?.password ?? ''} on:change={(e) => updateOAuth2Auth({ password: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Scope</span>
                    <input value={activeRequest.auth.oauth2?.scope ?? ''} on:change={(e) => updateOAuth2Auth({ scope: e.currentTarget.value })} />
                    {#if activeRequest.auth.oauth2?.grantType === 'authorization_code' || activeRequest.auth.oauth2?.grantType === 'implicit'}
                      <span class="field-label">State</span>
                      <input value={activeRequest.auth.oauth2?.state ?? ''} on:change={(e) => updateOAuth2Auth({ state: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Credentials</span>
                    <select value={activeRequest.auth.oauth2?.credentialsPlacement || 'basic_auth_header'} on:change={(e) => updateOAuth2Auth({ credentialsPlacement: e.currentTarget.value })}>
                      {#each oauth2CredentialPlacements as placement (placement)}
                        <option value={placement}>{placement}</option>
                      {/each}
                    </select>
                    {#if activeRequest.auth.oauth2?.grantType === 'authorization_code'}
                      <span class="field-label">PKCE</span>
                      <input type="checkbox" checked={activeRequest.auth.oauth2?.pkce ?? false} on:change={(e) => updateOAuth2Auth({ pkce: e.currentTarget.checked })} />
                    {/if}
                    <span class="field-label">Token source</span>
                    <select value={activeRequest.auth.oauth2?.tokenSource || 'access_token'} on:change={(e) => updateOAuth2Auth({ tokenSource: e.currentTarget.value })}>
                      {#each oauth2TokenSources as source (source)}
                        <option value={source}>{source}</option>
                      {/each}
                    </select>
                    <span class="field-label">Token placement</span>
                    <select value={activeRequest.auth.oauth2?.tokenPlacement || 'header'} on:change={(e) => updateOAuth2Auth({ tokenPlacement: e.currentTarget.value })}>
                      {#each oauth2TokenPlacements as placement (placement)}
                        <option value={placement}>{placement}</option>
                      {/each}
                    </select>
                    {#if (activeRequest.auth.oauth2?.tokenPlacement || 'header') === 'header'}
                      <span class="field-label">Header prefix</span>
                      <input value={activeRequest.auth.oauth2?.tokenHeaderPrefix || 'Bearer'} on:change={(e) => updateOAuth2Auth({ tokenHeaderPrefix: e.currentTarget.value })} />
                    {:else}
                      <span class="field-label">Query key</span>
                      <input value={activeRequest.auth.oauth2?.tokenQueryKey || 'access_token'} on:change={(e) => updateOAuth2Auth({ tokenQueryKey: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Static token</span>
                    <input type="password" value={activeRequest.auth.token} on:input={(e) => updateAuth({ token: e.currentTarget.value })} />
                    <div class="oauth2-extra-stack">
                      <OAuth2AdditionalParams
                        title="Authorization request params"
                        params={activeRequest.auth.oauth2?.authorizationAdditionalParams ?? []}
                        onAdd={(sendIn) => addRequestOAuth2AdditionalParam('authorizationAdditionalParams', sendIn)}
                        onChange={(index, field, value) => updateRequestOAuth2AdditionalParam('authorizationAdditionalParams', 'body', index, field, value)}
                        onRemove={(index) => removeRequestOAuth2AdditionalParam('authorizationAdditionalParams', index)}
                      />
                      <OAuth2AdditionalParams
                        title="Access token request params"
                        params={activeRequest.auth.oauth2?.tokenAdditionalParams ?? []}
                        onAdd={(sendIn) => addRequestOAuth2AdditionalParam('tokenAdditionalParams', sendIn)}
                        onChange={(index, field, value) => updateRequestOAuth2AdditionalParam('tokenAdditionalParams', 'body', index, field, value)}
                        onRemove={(index) => removeRequestOAuth2AdditionalParam('tokenAdditionalParams', index)}
                      />
                      <OAuth2AdditionalParams
                        title="Refresh token request params"
                        params={activeRequest.auth.oauth2?.refreshAdditionalParams ?? []}
                        onAdd={(sendIn) => addRequestOAuth2AdditionalParam('refreshAdditionalParams', sendIn)}
                        onChange={(index, field, value) => updateRequestOAuth2AdditionalParam('refreshAdditionalParams', 'body', index, field, value)}
                        onRemove={(index) => removeRequestOAuth2AdditionalParam('refreshAdditionalParams', index)}
                      />
                    </div>
                  {:else if activeRequest.auth.mode === 'apikey'}
                    <span class="field-label">Key</span>
                    <input value={activeRequest.auth.apiKey} on:change={(e) => updateAuth({ apiKey: e.currentTarget.value })} />
                    <span class="field-label">Value</span>
                    <input type="password" value={activeRequest.auth.apiValue} on:change={(e) => updateAuth({ apiValue: e.currentTarget.value })} />
                    <span class="field-label">Send in</span>
	                    <select value={activeRequest.auth.apiLocation} on:change={(e) => updateAuth({ apiLocation: e.currentTarget.value })}>
	                      <option value="header">Header</option>
	                      <option value="query">Query</option>
	                    </select>
	                  {:else if activeRequest.auth.mode === 'awsv4'}
	                    <span class="field-label">Access key ID</span>
	                    <input value={activeRequest.auth.awsv4?.accessKeyId ?? ''} on:change={(e) => updateAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
	                    <span class="field-label">Secret access key</span>
	                    <input type="password" value={activeRequest.auth.awsv4?.secretAccessKey ?? ''} on:change={(e) => updateAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
	                    <span class="field-label">Session token</span>
	                    <input type="password" value={activeRequest.auth.awsv4?.sessionToken ?? ''} on:change={(e) => updateAWSV4Auth({ sessionToken: e.currentTarget.value })} />
	                    <span class="field-label">Service</span>
	                    <input value={activeRequest.auth.awsv4?.service ?? ''} placeholder="execute-api" on:change={(e) => updateAWSV4Auth({ service: e.currentTarget.value })} />
	                    <span class="field-label">Region</span>
	                    <input value={activeRequest.auth.awsv4?.region ?? ''} placeholder="us-east-1" on:change={(e) => updateAWSV4Auth({ region: e.currentTarget.value })} />
		                    <span class="field-label">Profile</span>
		                    <input value={activeRequest.auth.awsv4?.profileName ?? ''} on:change={(e) => updateAWSV4Auth({ profileName: e.currentTarget.value })} />
		                  {:else if activeRequest.auth.mode === 'oauth1'}
		                    <span class="field-label">Consumer key</span>
		                    <input value={activeRequest.auth.oauth1?.consumerKey ?? ''} on:change={(e) => updateOAuth1Auth({ consumerKey: e.currentTarget.value })} />
		                    <span class="field-label">Consumer secret</span>
		                    <input type="password" value={activeRequest.auth.oauth1?.consumerSecret ?? ''} on:change={(e) => updateOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
		                    <span class="field-label">Token</span>
		                    <input value={activeRequest.auth.oauth1?.accessToken ?? ''} on:change={(e) => updateOAuth1Auth({ accessToken: e.currentTarget.value })} />
		                    <span class="field-label">Token secret</span>
		                    <input type="password" value={activeRequest.auth.oauth1?.accessTokenSecret ?? ''} on:change={(e) => updateOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
		                    <span class="field-label">Signature</span>
		                    <select value={activeRequest.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} on:change={(e) => updateOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
		                      {#each oauth1SignatureMethods as method (method)}
		                        <option value={method}>{method}</option>
		                      {/each}
		                    </select>
		                    <span class="field-label">Add params to</span>
		                    <select value={activeRequest.auth.oauth1?.placement || 'header'} on:change={(e) => updateOAuth1Auth({ placement: e.currentTarget.value })}>
		                      {#each oauth1Placements as placement (placement)}
		                        <option value={placement}>{placement}</option>
		                      {/each}
		                    </select>
		                    <span class="field-label">Callback URL</span>
		                    <input value={activeRequest.auth.oauth1?.callbackUrl ?? ''} on:change={(e) => updateOAuth1Auth({ callbackUrl: e.currentTarget.value })} />
		                    <span class="field-label">Verifier</span>
		                    <input value={activeRequest.auth.oauth1?.verifier ?? ''} on:change={(e) => updateOAuth1Auth({ verifier: e.currentTarget.value })} />
		                    <span class="field-label">Timestamp</span>
		                    <input value={activeRequest.auth.oauth1?.timestamp ?? ''} on:change={(e) => updateOAuth1Auth({ timestamp: e.currentTarget.value })} />
		                    <span class="field-label">Nonce</span>
		                    <input value={activeRequest.auth.oauth1?.nonce ?? ''} on:change={(e) => updateOAuth1Auth({ nonce: e.currentTarget.value })} />
		                    <span class="field-label">Version</span>
		                    <input value={activeRequest.auth.oauth1?.version ?? ''} placeholder="1.0" on:change={(e) => updateOAuth1Auth({ version: e.currentTarget.value })} />
		                    <span class="field-label">Realm</span>
		                    <input value={activeRequest.auth.oauth1?.realm ?? ''} on:change={(e) => updateOAuth1Auth({ realm: e.currentTarget.value })} />
		                    <span class="field-label">Private key</span>
		                    <textarea class="short" spellcheck="false" value={activeRequest.auth.oauth1?.privateKey ?? ''} on:change={(e) => updateOAuth1Auth({ privateKey: e.currentTarget.value })}></textarea>
		                    <span class="field-label">Private key type</span>
		                    <select value={activeRequest.auth.oauth1?.privateKeyType || 'text'} on:change={(e) => updateOAuth1Auth({ privateKeyType: e.currentTarget.value })}>
		                      <option value="text">text</option>
		                      <option value="file">file</option>
		                    </select>
		                    <span class="field-label">Body hash</span>
		                    <input type="checkbox" checked={activeRequest.auth.oauth1?.includeBodyHash ?? false} on:change={(e) => updateOAuth1Auth({ includeBodyHash: e.currentTarget.checked })} />
		                  {:else if activeRequest.auth.mode !== 'none'}
                    <div class="empty-state wide">This auth mode is marked partial until its full backend signer is implemented.</div>
	                  {/if}
                </div>
              {:else if requestPaneTab === 'vars'}
                <KeyValueTable
                  rows={(activeRequest.vars?.req ?? []).map((v) => ({ name: v.name, value: String(v.value ?? ''), enabled: v.enabled, secret: v.secret, description: v.dataType }))}
                  onAdd={addRequestVariable}
                  onChange={updateRequestVariable}
                  onRemove={removeRequestVariable}
                />
              {:else if requestPaneTab === 'script'}
                <span class="field-label">Pre-request</span>
                <CodeEditor editorKey={`${activeRequest.id}:pre-script`} value={activeRequest.preScript} language="javascript" ariaLabel="Pre-request script" testId="pre-request-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => patchField('preScript', value)} />
                <span class="field-label">Post-response</span>
                <CodeEditor editorKey={`${activeRequest.id}:post-script`} value={activeRequest.postScript} language="javascript" ariaLabel="Post-response script" testId="post-response-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => patchField('postScript', value)} />
              {:else if requestPaneTab === 'assert'}
                <table>
                  <thead>
                    <tr><th></th><th>Expression</th><th>Operator</th><th>Value</th><th></th></tr>
                  </thead>
                  <tbody>
                    {#each activeRequest.assertions ?? [] as row, index (index)}
                      <tr>
                        <td><input type="checkbox" checked={row.enabled} on:change={(e) => updateAssertion(index, 'enabled', e.currentTarget.checked)} /></td>
                        <td><input value={row.expression} on:change={(e) => updateAssertion(index, 'expression', e.currentTarget.value)} /></td>
                        <td><input value={row.operator} on:change={(e) => updateAssertion(index, 'operator', e.currentTarget.value)} /></td>
                        <td><input value={row.value} on:change={(e) => updateAssertion(index, 'value', e.currentTarget.value)} /></td>
                        <td><button class="icon-button" on:click={() => removeAssertion(index)}>x</button></td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
                <button on:click={addAssertion}>Add assertion</button>
              {:else if requestPaneTab === 'tests'}
                <CodeEditor editorKey={`${activeRequest.id}:tests`} value={activeRequest.tests} language="javascript" ariaLabel="Request tests" testId="request-tests-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => patchField('tests', value)} />
              {:else if requestPaneTab === 'docs'}
                <CodeEditor editorKey={`${activeRequest.id}:docs`} value={activeRequest.docs} language="markdown" ariaLabel="Request documentation" testId="request-docs-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => patchField('docs', value)} />
              {:else if requestPaneTab === 'app'}
                <div class="empty-state">Request app runtime surface</div>
              {:else if requestPaneTab === 'settings'}
                <RequestSettingsPanel requestType={activeRequest.type} settings={activeRequest.settings} onChange={updateSettings} />
              {/if}
            </div>
          </div>

          <input
            type="range"
            class="response-splitter"
            aria-label="Resize request and response panes"
            min="30"
            max="70"
            value={Math.round(responseSplit * 100)}
            title="Drag to resize panes; double-click to reset"
            on:mousedown={startResponseSplitResize}
            on:dblclick={() => { responseSplit = 0.52; persistWorkbenchLayout() }}
            on:input={(event) => (responseSplit = clampResponseSplit(Number(event.currentTarget.value) / 100))}
            on:change={persistWorkbenchLayout}
          />
          <div class="response-side">
            <div class="response-summary">
              <div class={`response-summary-status ${requestCommand.response.tone}`} aria-live="polite">
                <strong>{requestCommand.response.status}</strong>
                <span>{requestCommand.response.statusText}</span>
                <span>{requestCommand.response.duration}</span>
                <span>{requestCommand.response.size}</span>
              </div>
              <button title="Save response as example" on:click={saveResponseExample} disabled={!activeRequest.response || busy !== ''}>Example</button>
            </div>
            <div class="subtabs" role="tablist" aria-label="Response sections" tabindex="-1" on:keydown={responseTabKeydown}>
              {#each activeResponseTabs as tab (tab.id)}
                <button
                  class:active={responseTab === tab.id}
                  id={`response-tab-${tab.id}`}
                  data-response-tab={tab.id}
                  role="tab"
                  aria-selected={responseTab === tab.id}
                  aria-controls={`response-panel-${tab.id}`}
                  tabindex={responseTab === tab.id ? 0 : -1}
                  on:click={() => selectResponsePaneTab(tab.id)}
                >
                  {tab.label}
                  {#if tab.id === 'metadata' && (activeRequest.response?.metadata?.length ?? 0) > 0}
                    <span>{activeRequest.response?.metadata?.length}</span>
                  {:else if tab.id === 'trailers' && (activeRequest.response?.trailers?.length ?? 0) > 0}
                    <span>{activeRequest.response?.trailers?.length}</span>
                  {/if}
                </button>
              {/each}
            </div>
            <div class="response-content" id={`response-panel-${responseTab}`} role="tabpanel" aria-labelledby={`response-tab-${responseTab}`} tabindex="0">
              {#if responseTab !== 'examples'}
                <ResponseInspector
                  request={activeRequest}
                  selectedTab={responseTab}
                  selectedView={responseView}
                  timeline={activeTimelineEntries}
                  scriptLogs={activeScriptLogs}
                  onViewChange={(view) => (responseView = view as typeof responseView)}
                  onCopy={copyText}
                  liveLog={activeLiveSessionLog}
                  onDownloadBody={saveActiveResponseBody}
                  onExportTimeline={saveActiveResponseTimeline}
                />
              {:else}
                <div class="examples-toolbar">
                  <button class="primary" type="button" on:click={beginCreateResponseExample} disabled={busy !== ''}>New example</button>
                </div>
                {#if (activeRequest.examples ?? []).length === 0}
                  <div class="empty-state">No response examples</div>
                {:else}
                  <div class="examples-list">
                    {#each activeRequest.examples ?? [] as example (responseExampleIdentifier(example))}
                      <article>
                        <header>
                          {#if editingResponseExampleID === responseExampleIdentifier(example)}
                            <div class="example-name-editor">
                              <input aria-label="Response example name" bind:value={responseExampleNameDraft} on:keydown={(event) => {
                                if (event.key === 'Escape') cancelRenameResponseExample()
                                if (event.key === 'Enter') void renameResponseExample(example)
                              }} />
                              <button on:click={() => renameResponseExample(example)} disabled={busy !== '' || !responseExampleNameDraft.trim()}>Rename</button>
                              <button on:click={cancelRenameResponseExample}>Cancel</button>
                            </div>
                          {:else}
                            <strong>{example.name}</strong>
                          {/if}
                          <span class={responseStatusClass(example.response.status)}>{example.response.status}</span>
                        </header>
                        <small>{example.request.method} {example.request.url}</small>
                        <div class="example-actions">
                          <button on:click={() => openResponseExampleTab(example)} disabled={busy !== ''}>Open tab</button>
                          <button on:click={() => beginRenameResponseExample(example)} disabled={busy !== ''}>Rename</button>
                          <button on:click={() => beginEditResponseExampleDetails(example)} disabled={busy !== ''}>Edit details</button>
                          <button on:click={() => beginGenerateResponseExampleCode(example)} disabled={busy !== ''}>Generate Code</button>
                          <button on:click={() => cloneResponseExample(example)} disabled={busy !== ''}>Clone</button>
                          <button class="danger-button" on:click={() => requestDeleteResponseExample(example)} disabled={busy !== ''}>Delete</button>
                        </div>
                        {#if deletingResponseExampleID === responseExampleIdentifier(example)}
                          <div class="example-delete-confirm">
                            <span>Delete example <strong>{example.name}</strong>?</span>
                            <button class="danger-button" on:click={() => deleteResponseExample(example)} disabled={busy !== ''}>Delete</button>
                            <button on:click={() => (deletingResponseExampleID = '')}>Cancel</button>
                          </div>
                        {/if}
                        {#if editingResponseExampleDetailsID === responseExampleIdentifier(example)}
                          {@const draft = responseExampleDrafts[responseExampleIdentifier(example)] ?? example}
                          <div class="example-editor">
                            <div class="param-section-title">Example details</div>
                            <div class="field-grid example-editor-grid">
                              <span class="field-label">Description</span>
                              <textarea aria-label="Example description" class="short" spellcheck="false" value={draft.description ?? ''} on:input={(event) => updateResponseExampleDescription(example, event.currentTarget.value)}></textarea>
                            </div>
                            <div class="param-section-title">Request snapshot</div>
                            <div class="field-grid example-editor-grid">
                              <span class="field-label">Method</span>
                              <select aria-label="Example request method" value={draft.request?.method || 'GET'} on:change={(event) => updateResponseExampleRequestField(example, 'method', event.currentTarget.value)}>
                                {#each methods as method (method)}
                                  <option value={method}>{method}</option>
                                {/each}
                              </select>
                              <span class="field-label">URL</span>
                              <input aria-label="Example request URL" value={draft.request?.url ?? ''} on:input={(event) => updateResponseExampleRequestField(example, 'url', event.currentTarget.value)} />
                              <span class="field-label">Body mode</span>
                              <select aria-label="Example request body mode" value={draft.request?.bodyMode || 'none'} on:change={(event) => updateResponseExampleRequestField(example, 'bodyMode', event.currentTarget.value)}>
                                {#each bodyModes as mode (mode)}
                                  <option value={mode}>{mode}</option>
                                {/each}
                              </select>
                              {#if draft.request?.bodyMode !== 'formUrlEncoded'}
                                <span class="field-label">Body</span>
                                <textarea aria-label="Example request body" spellcheck="false" value={draft.request?.body ?? ''} on:input={(event) => updateResponseExampleRequestField(example, 'body', event.currentTarget.value)}></textarea>
                              {/if}
                            </div>
                            {#if draft.request?.bodyMode === 'formUrlEncoded'}
                              <div class="param-section-title">Request form body</div>
                              <KeyValueTable
                                rows={draft.request?.formUrlEncoded ?? []}
                                multilineValues={true}
                                showMove={true}
	                                onAdd={() => addResponseExampleRequestFormRow(example)}
	                                onChange={(index, field, value) => updateResponseExampleRequestFormRow(example, index, field, value)}
	                                onMove={(index, direction) => moveResponseExampleRequestFormRow(example, index, direction)}
	                                onReorder={(from, to) => reorderResponseExampleRequestFormRow(example, from, to)}
	                                onRemove={(index) => removeResponseExampleRequestFormRow(example, index)}
	                              />
                            {:else if draft.request?.bodyMode === 'multipartForm'}
                              <div class="param-section-title">Request multipart body</div>
                              <MultipartTable
                                rows={draft.request?.multipartForm ?? []}
                                showMove={true}
	                                onAdd={() => addResponseExampleRequestMultipartRow(example)}
	                                onChange={(index, field, value) => updateResponseExampleRequestMultipartRow(example, index, field, value)}
	                                onMove={(index, direction) => moveResponseExampleRequestMultipartRow(example, index, direction)}
	                                onReorder={(from, to) => reorderResponseExampleRequestMultipartRow(example, from, to)}
	                                onRemove={(index) => removeResponseExampleRequestMultipartRow(example, index)}
	                              />
                            {:else if draft.request?.bodyMode === 'file'}
                              <div class="param-section-title">Request file body</div>
                              <FileBodyTable
                                rows={draft.request?.file ?? []}
                                showMove={true}
	                                onAdd={() => addResponseExampleRequestFileRow(example)}
	                                onChange={(index, field, value) => updateResponseExampleRequestFileRow(example, index, field, value)}
	                                onMove={(index, direction) => moveResponseExampleRequestFileRow(example, index, direction)}
	                                onReorder={(from, to) => reorderResponseExampleRequestFileRow(example, from, to)}
	                                onRemove={(index) => removeResponseExampleRequestFileRow(example, index)}
	                              />
                            {:else if draft.request?.bodyMode === 'json'}
                              <div class="button-row compact">
                                <button on:click={() => prettifyResponseExampleRequestBody(example)} disabled={busy !== ''}>Prettify request JSON</button>
                              </div>
                            {/if}
                            <div data-example-section="request-params">
                              <div class="param-section-title">Request params</div>
                              <KeyValueTable
                                rows={draft.request?.params ?? []}
                                showMove={true}
                                showBulkEdit={true}
                                bulkLabel="Example request params bulk edit"
	                                onAdd={() => addResponseExampleRequestParam(example)}
	                                onChange={(index, field, value) => updateResponseExampleRequestParam(example, index, field, value)}
	                                onMove={(index, direction) => moveResponseExampleRequestParam(example, index, direction)}
	                                onReorder={(from, to) => reorderResponseExampleRequestParam(example, from, to)}
	                                onBulkChange={(rows) => replaceResponseExampleRequestParams(example, rows)}
	                                onRemove={(index) => removeResponseExampleRequestParam(example, index)}
                              />
                            </div>
                            <div class="param-section-title">Request headers</div>
                            <KeyValueTable
                              rows={draft.request?.headers ?? []}
                              showMove={true}
                              showBulkEdit={true}
                              bulkLabel="Example request headers bulk edit"
	                              onAdd={() => addResponseExampleRequestHeader(example)}
	                              onChange={(index, field, value) => updateResponseExampleRequestHeader(example, index, field, value)}
	                              onMove={(index, direction) => moveResponseExampleRequestHeader(example, index, direction)}
	                              onReorder={(from, to) => reorderResponseExampleRequestHeader(example, from, to)}
	                              onBulkChange={(rows) => replaceResponseExampleRequestHeaders(example, rows)}
	                              onRemove={(index) => removeResponseExampleRequestHeader(example, index)}
                            />
                            <div class="param-section-title">Response snapshot</div>
                            <div class="field-grid example-editor-grid">
                              <span class="field-label">Status</span>
                              <input aria-label="Example response status" type="number" value={draft.response.status} on:input={(event) => updateResponseExampleResponseField(example, 'status', event.currentTarget.value)} />
                              <span class="field-label">Status text</span>
                              <input aria-label="Example response status text" value={draft.response.statusText} on:input={(event) => updateResponseExampleResponseField(example, 'statusText', event.currentTarget.value)} />
                              <span class="field-label">Body type</span>
                              <select aria-label="Example response body type" value={draft.response.bodyType} on:change={(event) => updateResponseExampleResponseField(example, 'bodyType', event.currentTarget.value)}>
                                {#each responseExampleBodyTypes as bodyType (bodyType)}
                                  <option value={bodyType}>{bodyType}</option>
                                {/each}
                              </select>
                              <span class="field-label">Body</span>
                              <textarea aria-label="Example response body" spellcheck="false" value={draft.response.body} on:input={(event) => updateResponseExampleResponseField(example, 'body', event.currentTarget.value)}></textarea>
                            </div>
                            {#if draft.response.bodyType === 'json'}
                              <div class="button-row compact">
                                <button on:click={() => prettifyResponseExampleResponseBody(example)} disabled={busy !== ''}>Prettify response JSON</button>
                              </div>
                            {/if}
                            <div class="param-section-title">Response headers</div>
	                            <KeyValueTable
	                              rows={draft.response.headers ?? []}
	                              showEnabled={false}
	                              showMove={true}
	                              showBulkEdit={true}
                              bulkLabel="Example response headers bulk edit"
	                              onAdd={() => addResponseExampleHeader(example)}
	                              onChange={(index, field, value) => updateResponseExampleHeader(example, index, field, value)}
	                              onMove={(index, direction) => moveResponseExampleHeader(example, index, direction)}
	                              onReorder={(from, to) => reorderResponseExampleHeader(example, from, to)}
	                              onBulkChange={(rows) => replaceResponseExampleHeaders(example, rows)}
                              onRemove={(index) => removeResponseExampleHeader(example, index)}
                            />
                            <div class="example-actions">
                              <button on:click={() => saveResponseExampleDetails(example)} disabled={busy !== ''}>Save details</button>
                              <button on:click={() => cancelEditResponseExampleDetails(example)} disabled={busy !== ''}>Cancel</button>
                            </div>
                          </div>
                        {:else}
                          <pre>{formatResponse(example.response.body, Object.fromEntries((example.response.headers ?? []).map((row) => [row.name, row.value])))}</pre>
                        {/if}
                      </article>
                    {/each}
                  </div>
                {/if}
              {/if}
            </div>
          </div>
        </section>
      {:else if activeView === 'collection' && activeCollection}
        <section class="panel collection-panel">
          <header class="panel-header">
            <div>
              <h2>{activeCollection.name}</h2>
              <p class="panel-subtitle">{activeCollection.format.toUpperCase()} · {activeCollection.items?.length ?? 0} requests{activeCollection.remote ? ' · Git' : ''}{activeCollection.notFoundLocally ? ' · Not cloned' : ''}</p>
            </div>
            <button on:click={refreshCollection}>Refresh active</button>
          </header>
          <nav class="subtabs">
            {#each collectionTabs as tab (tab.id)}
              <button class:active={collectionTab === tab.id} on:click={() => (collectionTab = tab.id)}>
                {tab.label}
              </button>
            {/each}
          </nav>

          <div class="collection-settings-surface">
            {#if collectionTab === 'overview'}
              <div class="field-grid collection-overview">
                <span class="field-label">Path</span>
                <code>{activeCollection.path}</code>
                <span class="field-label">Format</span>
                <code>{activeCollection.format}</code>
                <span class="field-label">Status</span>
                <code>{activeCollection.notFoundLocally ? 'Not cloned locally' : 'Local'}</code>
                <span class="field-label">JavaScript Sandbox</span>
                <div class="collection-doc-actions sandbox-mode-panel" data-testid="sandbox-mode-panel">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Mode</span>
                      <span class="version-value" data-testid="sandbox-mode-status">{collectionSandboxMode(activeCollection) === 'developer' ? 'Developer Mode' : 'Safe Mode'}</span>
                    </div>
                  </div>
                  <div class="segmented compact" role="radiogroup" aria-label="JavaScript sandbox mode">
                    <button
                      type="button"
                      class:active={collectionSandboxMode(activeCollection) === 'safe'}
                      aria-pressed={collectionSandboxMode(activeCollection) === 'safe'}
                      data-testid="sandbox-mode-safe"
                      on:click={() => updateCollectionSandboxMode('safe')}
                      disabled={busy !== '' || activeCollection.notFoundLocally}
                    >
                      Safe Mode
                    </button>
                    <button
                      type="button"
                      class:active={collectionSandboxMode(activeCollection) === 'developer'}
                      aria-pressed={collectionSandboxMode(activeCollection) === 'developer'}
                      data-testid="sandbox-mode-developer"
                      on:click={() => updateCollectionSandboxMode('developer')}
                      disabled={busy !== '' || activeCollection.notFoundLocally}
                    >
                      Developer Mode
                    </button>
                  </div>
                </div>
                <span class="field-label">Name</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection name</span>
                    </div>
                    <p class="version-summary">{activeCollection.name}</p>
                  </div>
                  <button type="button" data-testid="collection-actions-clone" on:click={openCloneCollectionModal} disabled={busy !== '' || activeCollection.notFoundLocally}>Clone</button>
                  <button type="button" data-testid="collection-actions-rename" on:click={openRenameCollectionModal} disabled={busy !== '' || activeCollection.notFoundLocally}>Rename</button>
                </div>
                <span class="field-label">Folders</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection folders</span>
                    </div>
                    <p class="version-summary">{activeCollection.folders?.length ?? 0} configured</p>
                  </div>
                  <button type="button" data-testid="collection-actions-new-folder" on:click={() => openNewFolderModal()} disabled={busy !== '' || activeCollection.notFoundLocally}>New Folder</button>
                </div>
                <span class="field-label">Documentation</span>
                <div class="collection-doc-actions">
                  <div class="version-info" data-testid="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection Version:</span>
                      <span class="version-value" data-testid="version-value">{formattedCollectionVersion(activeCollection.version)}</span>
                    </div>
                    <p class="version-summary" data-testid="version-summary">{generateDocsFolderCount} {generateDocsFolderCount === 1 ? 'Folder' : 'Folders'} • {generateDocsRequestCount} {generateDocsRequestCount === 1 ? 'request' : 'requests'}</p>
                  </div>
                  <button type="button" data-testid="generate-docs-overview-button" on:click={openGenerateDocsModal}>Generate Docs</button>
                </div>
                <span class="field-label">Share</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection export</span>
                    </div>
                    <p class="version-summary">ZIP, YAML, or Postman</p>
                  </div>
                  <button type="button" data-testid="collection-actions-share" on:click={openShareCollectionModal}>Share Collection</button>
                </div>
                <span class="field-label">OpenAPI Sync</span>
                <div class="openapi-sync-panel" data-testid="openapi-sync-panel">
                  <div class="openapi-sync-fields">
                    <label>
                      <span>Source URL or file</span>
                      <input
                        aria-label="OpenAPI sync source"
                        data-testid="openapi-sync-source"
                        placeholder="https://example.com/openapi.yml"
                        value={openAPISyncSourceURL}
                        on:input={(event) => (openAPISyncSourceURL = event.currentTarget.value)}
                      />
                    </label>
                    <label>
                      <span>Group by</span>
                      <select aria-label="OpenAPI sync grouping" data-testid="openapi-sync-group-by" bind:value={openAPISyncGroupBy}>
                        <option value="tag">Tags</option>
                        <option value="path">Paths</option>
                      </select>
                    </label>
                    <label class="checkbox-line">
                      <input type="checkbox" data-testid="openapi-sync-preserve-values" bind:checked={openAPISyncPreserveValues} />
                      <span>Preserve values</span>
                    </label>
                  </div>
                  <label class="openapi-sync-content">
                    <span>Spec content override</span>
                    <textarea
                      aria-label="OpenAPI sync spec content"
                      data-testid="openapi-sync-content"
                      spellcheck="false"
                      placeholder="Paste a spec to check/apply without fetching the source"
                      value={openAPISyncContent}
                      on:input={(event) => (openAPISyncContent = event.currentTarget.value)}
                    ></textarea>
                  </label>
                  <div class="openapi-sync-actions">
                    <button type="button" data-testid="openapi-sync-connect" on:click={connectOpenAPISync} disabled={busy !== '' || activeCollection.notFoundLocally}>Connect</button>
	                    <button type="button" data-testid="openapi-sync-check" on:click={checkOpenAPISync} disabled={busy !== '' || activeCollection.notFoundLocally}>Check Updates</button>
			                    <button type="button" class="primary" data-testid="openapi-sync-apply" on:click={applyOpenAPISync} disabled={busy !== '' || activeCollection.notFoundLocally}>Apply Sync</button>
			                    {#if activeCollection.openapi?.length}
			                      <button type="button" data-testid="openapi-sync-view-spec" on:click={viewOpenAPISyncSpec} disabled={busy !== ''}>View spec</button>
			                      <button type="button" data-testid="openapi-sync-view-spec-diff-main" on:click={viewOpenAPISyncSpecDiff} disabled={busy !== '' || activeCollection.notFoundLocally}>View Spec Diff</button>
			                      <button type="button" data-testid="openapi-sync-settings-open" on:click={openOpenAPISyncSettings} disabled={busy !== ''}>Settings</button>
			                      <button type="button" data-testid="openapi-sync-disconnect" on:click={disconnectOpenAPISync} disabled={busy !== ''}>Disconnect</button>
			                    {/if}
	                  </div>
	                  <div class="openapi-sync-status" data-testid="openapi-sync-status">
	                    {#if openAPISyncResult}
	                      <strong>{openAPISyncResult.hasChanges ? 'Updates available' : 'In sync'}</strong>
	                      <span data-testid="openapi-sync-counts">{openAPISyncResult.added} added · {openAPISyncResult.updated} updated · {openAPISyncResult.removed} removed · {openAPISyncResult.unchanged} unchanged</span>
	                      <small>{openAPISyncResult.title || activeCollection.name}{openAPISyncResult.version ? ` v${openAPISyncResult.version}` : ''} · {openAPISyncResult.endpointCount} endpoints</small>
	                      {#if activeCollection.openapi?.[0]}
	                        <small data-testid="openapi-sync-auto-check-status">{openAPISyncAutoCheckStatusLine(activeCollection)}</small>
	                      {/if}
	                    {:else if activeCollection.openapi?.[0]}
	                      <strong>Connected</strong>
	                      <span>{activeCollection.openapi[0].sourceUrl}</span>
	                      <small>{activeCollection.openapi[0].lastSyncDate ? `Last sync ${activeCollection.openapi[0].lastSyncDate}` : 'No sync check in this session'}</small>
	                      <small data-testid="openapi-sync-auto-check-status">{openAPISyncAutoCheckStatusLine(activeCollection)}</small>
	                    {:else}
                      <strong>Not connected</strong>
                      <span>Track a source spec and apply changes while preserving request scripts, tests, and values.</span>
                    {/if}
                  </div>
                  {#if openAPISyncResult?.changes?.length}
	                    <div class="openapi-sync-review" data-testid="openapi-sync-review">
	                      <div class="openapi-sync-review-toolbar">
	                        <h3>Review Changes</h3>
	                        <div>
	                          <button type="button" data-testid="openapi-sync-view-spec-diff" on:click={viewOpenAPISyncSpecDiff}>View Spec Diff</button>
	                          <button type="button" data-testid="openapi-sync-skip-all" on:click={() => setOpenAPISyncAllEndpointDecisions('keep-mine')}>Skip All</button>
	                          <button type="button" data-testid="openapi-sync-accept-all" on:click={() => setOpenAPISyncAllEndpointDecisions('accept-incoming')}>Accept All</button>
	                        </div>
	                      </div>
                      <div class="openapi-sync-review-header">
                        <span>Change</span>
                        <span>Method</span>
                        <span>Endpoint</span>
                        <span>Decision</span>
                      </div>
                      {#each openAPISyncResult.changes as change, index (index)}
                        <div class="openapi-sync-change-row" data-testid="openapi-sync-change-row">
                          <span class="openapi-sync-change-type">{change.change}</span>
                          <span class="openapi-sync-method">{change.method}</span>
                          <span class="openapi-sync-path" title={`${change.name || change.path} ${change.path}`}>
                            <strong>{change.name || change.path}</strong>
                            <small>{change.path}</small>
                          </span>
                          <select
                            aria-label={`OpenAPI sync decision ${change.method} ${change.path}`}
                            data-testid="openapi-sync-decision"
                            value={openAPISyncEndpointDecisions[change.id] ?? defaultOpenAPISyncDecision(change)}
                            on:change={(event) => setOpenAPISyncEndpointDecision(change.id, event.currentTarget.value)}
                          >
                            {#if change.change === 'removed'}
                              <option value="keep-mine">Keep</option>
                              <option value="accept-incoming">Delete</option>
                            {:else if change.change === 'added'}
                              <option value="accept-incoming">Add</option>
                              <option value="keep-mine">Skip</option>
                            {:else}
                              <option value="accept-incoming">Update</option>
                              <option value="keep-mine">Keep Current</option>
                            {/if}
                          </select>
                        </div>
                      {/each}
                    </div>
                  {/if}
                  {#if activeCollection.openapi?.length}
                    <div class="openapi-sync-review openapi-local-drift" data-testid="openapi-local-drift-panel">
                      <div class="openapi-sync-review-toolbar">
                        <h3>Collection Changes</h3>
                        <div>
                          <button type="button" data-testid="openapi-local-drift-check" on:click={checkOpenAPILocalDrift} disabled={busy !== '' || activeCollection.notFoundLocally}>Check Collection</button>
                          {#if openAPILocalDriftResult?.hasChanges}
                            <button type="button" class="primary" data-testid="openapi-local-drift-revert-all" on:click={revertAllOpenAPILocalDrift} disabled={busy !== '' || activeCollection.notFoundLocally}>Revert All to Spec</button>
                          {/if}
                        </div>
                      </div>
                      {#if openAPILocalDriftResult}
                        <div class="openapi-sync-status" data-testid="openapi-local-drift-status">
                          {#if openAPILocalDriftResult.noStoredSpec}
                            <strong>Stored spec missing</strong>
                            <span>Reconnect OpenAPI sync to restore collection change tracking.</span>
                          {:else}
                            <strong>{openAPILocalDriftResult.hasChanges ? 'Collection has changes since last sync' : 'Collection matches last sync'}</strong>
                            <span data-testid="openapi-local-drift-counts">{openAPILocalDriftResult.modified} modified · {openAPILocalDriftResult.missing} deleted · {openAPILocalDriftResult.localOnly} added</span>
                            <small>{openAPILocalDriftResult.specEndpointCount} spec endpoints · {openAPILocalDriftResult.collectionEndpointCount} collection endpoints</small>
                          {/if}
                        </div>
                        {#if openAPILocalDriftResult.changes?.length}
                          <div class="openapi-local-drift-actions">
                            {#if openAPILocalDriftResult.modified}
                              <button type="button" data-testid="openapi-local-drift-reset-all" on:click={() => resetOpenAPILocalDrift()} disabled={busy !== ''}>Reset All</button>
                            {/if}
                            {#if openAPILocalDriftResult.missing}
                              <button type="button" data-testid="openapi-local-drift-restore-all" on:click={() => restoreOpenAPILocalDrift()} disabled={busy !== ''}>Restore All</button>
                            {/if}
                            {#if openAPILocalDriftResult.localOnly}
                              <button type="button" data-testid="openapi-local-drift-delete-all" on:click={() => deleteOpenAPILocalDrift()} disabled={busy !== ''}>Delete All</button>
                            {/if}
                          </div>
                          <div class="openapi-sync-review-header openapi-local-drift-header">
                            <span>Change</span>
                            <span>Method</span>
                            <span>Endpoint</span>
                            <span>Action</span>
                          </div>
                          {#each openAPILocalDriftResult.changes as change, index (index)}
                            <div class="openapi-sync-change-row" data-testid="openapi-local-drift-row">
                              <span class="openapi-sync-change-type">{openAPILocalDriftLabel(change.change)}</span>
                              <span class="openapi-sync-method">{change.method}</span>
                              <span class="openapi-sync-path" title={`${change.name || change.path} ${change.path}`}>
                                <strong>{change.name || change.path}</strong>
                                <small>{change.path}</small>
                                {#if (change.change === 'modified' || change.change === 'local-only') && change.itemId}
                                  <button type="button" class="link-button openapi-sync-open-request" data-testid="openapi-local-drift-open" on:click={() => openOpenAPILocalDriftRequest(change)} disabled={busy !== ''}>Open</button>
                                {/if}
                              </span>
                              {#if change.change === 'modified'}
                                <button type="button" data-testid="openapi-local-drift-reset" on:click={() => resetOpenAPILocalDrift(change.id)} disabled={busy !== ''}>Reset</button>
                              {:else if change.change === 'missing'}
                                <button type="button" data-testid="openapi-local-drift-restore" on:click={() => restoreOpenAPILocalDrift(change.id)} disabled={busy !== ''}>Restore</button>
                              {:else}
                                <button type="button" data-testid="openapi-local-drift-delete" on:click={() => deleteOpenAPILocalDrift(change.id)} disabled={busy !== ''}>Delete</button>
                              {/if}
                            </div>
                          {/each}
                        {/if}
                      {/if}
                    </div>
                  {/if}
                </div>
                <span class="field-label">Terminal</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection directory</span>
                    </div>
                    <p class="version-summary">Start a Dev Tools session in this collection</p>
                  </div>
                  <button type="button" data-testid="collection-actions-open-terminal" on:click={openCollectionInTerminal} disabled={busy !== '' || terminalBusy || activeCollection.notFoundLocally}>Open in Terminal</button>
                </div>
                <span class="field-label">Folder</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Local files</span>
                    </div>
                    <p class="version-summary" data-testid="collection-reveal-status">{revealCollectionMessage || activeCollection.path}</p>
                  </div>
                  <button type="button" data-testid="collection-actions-show-in-folder" on:click={revealCollectionInFolder} disabled={busy !== '' || activeCollection.notFoundLocally}>{revealInFolderLabel()}</button>
                </div>
                <span class="field-label">Remove</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Workspace mount</span>
                    </div>
                    <p class="version-summary">Remove this collection from the workspace without deleting files</p>
                  </div>
                  <button type="button" data-testid="collection-actions-remove" on:click={openRemoveCollectionModal} disabled={busy !== '' || activeCollection.scratch}>Remove Collection</button>
                </div>
                <span class="field-label">Git</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line"><span class="version-label">Local Git workbench</span></div>
                    <p class="version-summary">Review scoped changes, commits, branches, and remotes without opening a terminal.</p>
                  </div>
                  <button type="button" data-testid="collection-actions-open-git" on:click={openGitWorkbench} disabled={busy !== '' || activeCollection.notFoundLocally}>Open Git Workbench</button>
                </div>
                <span class="field-label">Docs</span>
                <textarea spellcheck="false" value={activeCollection.docs} on:change={(e) => updateCollectionDocs(e.currentTarget.value)}></textarea>
              </div>
            {:else if collectionTab === 'folders'}
              {#if (activeCollection.folders ?? []).length === 0}
                <div class="empty-state">No folders in this collection</div>
              {:else if editableFolder}
                <div class="settings-stack folder-settings-panel">
                  <div class="field-grid folder-picker">
                    <span class="field-label">Folder</span>
                    <select aria-label="Folder settings folder" value={editableFolder.path} on:change={(e) => (selectedFolderPath = e.currentTarget.value)}>
                      {#each activeCollection.folders ?? [] as folder (folder.path)}
                        <option value={folder.path}>{folder.displayPath || folder.path}</option>
                      {/each}
                    </select>
                    <span class="field-label">Path</span>
                    <code>{editableFolder.path}</code>
                  </div>

                  <nav class="subtabs compact" aria-label="Folder settings tabs">
                    {#each folderSettingsTabs as tab (tab.id)}
                      <button type="button" class:active={folderSettingsTab === tab.id} on:click={() => (folderSettingsTab = tab.id)}>{tab.label}</button>
                    {/each}
                  </nav>

                  {#if folderSettingsTab === 'headers'}
                    <KeyValueTable
                      rows={editableFolder.headers ?? []}
                      onAdd={addFolderHeader}
                      onChange={updateFolderHeader}
                      onRemove={removeFolderHeader}
                    />
                  {:else if folderSettingsTab === 'vars'}
                    <div class="settings-section-header">
                      <h3>Pre Request</h3>
                      <button on:click={() => addFolderVariable('variables')}>Add variable</button>
                    </div>
                    <div class="table-scroll">
                      <table>
                        <thead><tr><th>On</th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                        <tbody>
                          {#each editableFolder.variables ?? [] as variable, index (variable.id)}
                            <tr>
                              <td><input type="checkbox" checked={variable.enabled} on:change={(e) => updateFolderVariable('variables', index, 'enabled', e.currentTarget.checked)} /></td>
                              <td><input aria-label="Folder pre-request variable name" value={variable.name} on:change={(e) => updateFolderVariable('variables', index, 'name', e.currentTarget.value)} /></td>
                              <td><input aria-label="Folder pre-request variable value" value={String(variable.value ?? '')} on:change={(e) => updateFolderVariable('variables', index, 'value', e.currentTarget.value)} /></td>
                              <td>
                                <select aria-label="Folder pre-request variable type" value={variable.dataType || variable.type || 'string'} on:change={(e) => updateFolderVariable('variables', index, 'dataType', e.currentTarget.value)}>
                                  <option value="string">string</option>
                                  <option value="number">number</option>
                                  <option value="boolean">boolean</option>
                                  <option value="object">object</option>
                                </select>
                              </td>
                              <td><input aria-label="Folder pre-request variable secret" type="checkbox" checked={variable.secret} on:change={(e) => updateFolderVariable('variables', index, 'secret', e.currentTarget.checked)} /></td>
                              <td><button on:click={() => removeFolderVariable('variables', index)}>Remove</button></td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    </div>
                    {#if (editableFolder.variables ?? []).length === 0}
                      <div class="empty-state">No pre-request variables</div>
                    {/if}

                    <div class="settings-section-header">
                      <h3>Post Response</h3>
                      <button on:click={() => addFolderVariable('resVariables')}>Add variable</button>
                    </div>
                    <div class="table-scroll">
                      <table>
                        <thead><tr><th>On</th><th>Name</th><th>Expression</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                        <tbody>
                          {#each editableFolder.resVariables ?? [] as variable, index (variable.id)}
                            <tr>
                              <td><input type="checkbox" checked={variable.enabled} on:change={(e) => updateFolderVariable('resVariables', index, 'enabled', e.currentTarget.checked)} /></td>
                              <td><input aria-label="Folder post-response variable name" value={variable.name} on:change={(e) => updateFolderVariable('resVariables', index, 'name', e.currentTarget.value)} /></td>
                              <td><input aria-label="Folder post-response variable expression" value={String(variable.value ?? '')} on:change={(e) => updateFolderVariable('resVariables', index, 'value', e.currentTarget.value)} /></td>
                              <td>
                                <select aria-label="Folder post-response variable type" value={variable.dataType || variable.type || 'string'} on:change={(e) => updateFolderVariable('resVariables', index, 'dataType', e.currentTarget.value)}>
                                  <option value="string">string</option>
                                  <option value="number">number</option>
                                  <option value="boolean">boolean</option>
                                  <option value="object">object</option>
                                </select>
                              </td>
                              <td><input aria-label="Folder post-response variable secret" type="checkbox" checked={variable.secret} on:change={(e) => updateFolderVariable('resVariables', index, 'secret', e.currentTarget.checked)} /></td>
                              <td><button on:click={() => removeFolderVariable('resVariables', index)}>Remove</button></td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    </div>
                    {#if (editableFolder.resVariables ?? []).length === 0}
                      <div class="empty-state">No post-response variables</div>
                    {/if}
                  {:else if folderSettingsTab === 'auth'}
                    <div class="field-grid auth-grid">
                      <span class="field-label">Mode</span>
                      <select aria-label="Folder auth mode" value={editableFolder.auth?.mode || ''} on:change={(e) => updateFolderAuth({ mode: e.currentTarget.value })}>
                        <option value="">Unset</option>
                        {#each authModes as mode (mode)}
                          <option value={mode}>{mode}</option>
                        {/each}
                      </select>
                      {#if editableFolder.auth?.mode === 'basic' || editableFolder.auth?.mode === 'digest' || editableFolder.auth?.mode === 'wsse' || editableFolder.auth?.mode === 'ntlm'}
                        <span class="field-label">Username</span>
                        <input value={editableFolder.auth.username ?? ''} on:change={(e) => updateFolderAuth({ username: e.currentTarget.value })} />
                        <span class="field-label">Password</span>
                        <input type="password" value={editableFolder.auth.password ?? ''} on:change={(e) => updateFolderAuth({ password: e.currentTarget.value })} />
                        {#if editableFolder.auth?.mode === 'ntlm'}
                          <span class="field-label">Domain</span>
                          <input value={editableFolder.auth.domain ?? ''} on:change={(e) => updateFolderAuth({ domain: e.currentTarget.value })} />
                        {/if}
                      {:else if editableFolder.auth?.mode === 'bearer'}
                        <span class="field-label">Token</span>
                        <input type="password" value={editableFolder.auth.token ?? ''} on:change={(e) => updateFolderAuth({ token: e.currentTarget.value })} />
                      {:else if editableFolder.auth?.mode === 'apikey'}
                        <span class="field-label">Key</span>
                        <input value={editableFolder.auth.apiKey ?? ''} on:change={(e) => updateFolderAuth({ apiKey: e.currentTarget.value })} />
                        <span class="field-label">Value</span>
                        <input type="password" value={editableFolder.auth.apiValue ?? ''} on:change={(e) => updateFolderAuth({ apiValue: e.currentTarget.value })} />
                        <span class="field-label">Placement</span>
                        <select value={editableFolder.auth.apiLocation || 'header'} on:change={(e) => updateFolderAuth({ apiLocation: e.currentTarget.value })}>
                          <option value="header">Header</option>
                          <option value="queryparams">Query params</option>
                        </select>
                      {:else if editableFolder.auth?.mode === 'awsv4'}
                        <span class="field-label">Access key</span>
                        <input value={editableFolder.auth.awsv4?.accessKeyId ?? ''} on:change={(e) => updateFolderAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
                        <span class="field-label">Secret key</span>
                        <input type="password" value={editableFolder.auth.awsv4?.secretAccessKey ?? ''} on:change={(e) => updateFolderAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
                        <span class="field-label">Service</span>
                        <input value={editableFolder.auth.awsv4?.service ?? ''} placeholder="execute-api" on:change={(e) => updateFolderAWSV4Auth({ service: e.currentTarget.value })} />
                        <span class="field-label">Region</span>
                        <input value={editableFolder.auth.awsv4?.region ?? ''} placeholder="us-east-1" on:change={(e) => updateFolderAWSV4Auth({ region: e.currentTarget.value })} />
                      {:else if editableFolder.auth?.mode === 'oauth1'}
                        <span class="field-label">Consumer key</span>
                        <input value={editableFolder.auth.oauth1?.consumerKey ?? ''} on:change={(e) => updateFolderOAuth1Auth({ consumerKey: e.currentTarget.value })} />
                        <span class="field-label">Consumer secret</span>
                        <input type="password" value={editableFolder.auth.oauth1?.consumerSecret ?? ''} on:change={(e) => updateFolderOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
                        <span class="field-label">Access token</span>
                        <input value={editableFolder.auth.oauth1?.accessToken ?? ''} on:change={(e) => updateFolderOAuth1Auth({ accessToken: e.currentTarget.value })} />
                        <span class="field-label">Token secret</span>
                        <input type="password" value={editableFolder.auth.oauth1?.accessTokenSecret ?? ''} on:change={(e) => updateFolderOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
                        <span class="field-label">Signature</span>
                        <select value={editableFolder.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} on:change={(e) => updateFolderOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
                          {#each oauth1SignatureMethods as method (method)}
                            <option value={method}>{method}</option>
                          {/each}
                        </select>
                      {:else if editableFolder.auth?.mode === 'oauth2'}
                        <span class="field-label">Grant type</span>
                        <select value={editableFolder.auth.oauth2?.grantType || 'client_credentials'} on:change={(e) => updateFolderOAuth2Auth({ grantType: e.currentTarget.value })}>
                          {#each oauth2GrantTypes as grantType (grantType)}
                            <option value={grantType}>{grantType}</option>
                          {/each}
                        </select>
                        <span class="field-label">Access token URL</span>
                        <input value={editableFolder.auth.oauth2?.accessTokenUrl ?? ''} on:change={(e) => updateFolderOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                        <span class="field-label">Client ID</span>
                        <input value={editableFolder.auth.oauth2?.clientId ?? ''} on:change={(e) => updateFolderOAuth2Auth({ clientId: e.currentTarget.value })} />
                        <span class="field-label">Client secret</span>
                        <input type="password" value={editableFolder.auth.oauth2?.clientSecret ?? ''} on:change={(e) => updateFolderOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                        <span class="field-label">Scope</span>
                        <input value={editableFolder.auth.oauth2?.scope ?? ''} on:change={(e) => updateFolderOAuth2Auth({ scope: e.currentTarget.value })} />
                        <span class="field-label">Token</span>
                        <input type="password" value={editableFolder.auth.token ?? ''} on:change={(e) => updateFolderAuth({ token: e.currentTarget.value })} />
                      {/if}
                    </div>
                  {:else if folderSettingsTab === 'script'}
                    <span class="field-label">Pre-request</span>
                    <textarea class="short" spellcheck="false" value={editableFolder.preScript ?? ''} on:change={(e) => updateFolderScript('preScript', e.currentTarget.value)}></textarea>
                    <span class="field-label">Post-response</span>
                    <textarea class="short" spellcheck="false" value={editableFolder.postScript ?? ''} on:change={(e) => updateFolderScript('postScript', e.currentTarget.value)}></textarea>
                  {:else if folderSettingsTab === 'tests'}
                    <textarea spellcheck="false" value={editableFolder.tests ?? ''} on:change={(e) => updateFolderScript('tests', e.currentTarget.value)}></textarea>
                  {:else if folderSettingsTab === 'docs'}
                    <textarea spellcheck="false" value={editableFolder.docs ?? ''} on:change={(e) => updateFolderDocs(e.currentTarget.value)}></textarea>
                  {/if}
                </div>
              {/if}
            {:else if collectionTab === 'headers'}
              <KeyValueTable
                rows={activeCollection.headers ?? []}
                onAdd={addCollectionHeader}
                onChange={updateCollectionHeader}
                onRemove={removeCollectionHeader}
              />
            {:else if collectionTab === 'vars'}
              <table>
                <thead>
                  <tr><th></th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th></tr>
                </thead>
                <tbody>
                  {#each activeCollection.variables ?? [] as variable, index (variable.id)}
                    <tr>
                      <td><input type="checkbox" checked={variable.enabled} on:change={(e) => updateCollectionVariable(index, 'enabled', e.currentTarget.checked)} /></td>
                      <td><input value={variable.name} on:change={(e) => updateCollectionVariable(index, 'name', e.currentTarget.value)} /></td>
                      <td><input value={String(variable.value ?? '')} on:change={(e) => updateCollectionVariable(index, 'value', e.currentTarget.value)} /></td>
                      <td>
                        <select value={variable.dataType || 'string'} on:change={(e) => updateCollectionVariable(index, 'dataType', e.currentTarget.value)}>
                          <option value="string">string</option>
                          <option value="number">number</option>
                          <option value="boolean">boolean</option>
                          <option value="object">object</option>
                        </select>
                      </td>
                      <td><input type="checkbox" checked={variable.secret} on:change={(e) => updateCollectionVariable(index, 'secret', e.currentTarget.checked)} /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              <button on:click={addCollectionVariable}>Add variable</button>
            {:else if collectionTab === 'auth'}
              <div class="field-grid auth-grid">
                <span class="field-label">Mode</span>
                <select value={activeCollection.auth?.mode ?? 'none'} on:change={(e) => updateCollectionAuth({ mode: e.currentTarget.value })}>
                  {#each authModes as mode (mode)}
                    <option value={mode}>{mode}</option>
                  {/each}
                </select>
	                {#if activeCollection.auth?.mode === 'basic' || activeCollection.auth?.mode === 'digest' || activeCollection.auth?.mode === 'wsse' || activeCollection.auth?.mode === 'ntlm'}
                  <span class="field-label">Username</span>
                  <input value={activeCollection.auth.username} on:change={(e) => updateCollectionAuth({ username: e.currentTarget.value })} />
                  <span class="field-label">Password</span>
                  <input type="password" value={activeCollection.auth.password} on:change={(e) => updateCollectionAuth({ password: e.currentTarget.value })} />
                  {#if activeCollection.auth?.mode === 'ntlm'}
                    <span class="field-label">Domain</span>
                    <input value={activeCollection.auth.domain} on:change={(e) => updateCollectionAuth({ domain: e.currentTarget.value })} />
                  {/if}
                {:else if activeCollection.auth?.mode === 'bearer'}
                  <span class="field-label">Token</span>
                  <input type="password" value={activeCollection.auth.token} on:change={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
                {:else if activeCollection.auth?.mode === 'oauth2'}
                  <span class="field-label">Grant</span>
                  <select value={activeCollection.auth.oauth2?.grantType || 'client_credentials'} on:change={(e) => updateCollectionOAuth2Auth({ grantType: e.currentTarget.value })}>
                    {#each oauth2GrantTypes as grant (grant)}
                      <option value={grant}>{grant}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">Callback URL</span>
                    <input value={activeCollection.auth.oauth2?.callbackUrl ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ callbackUrl: e.currentTarget.value })} />
                    <span class="field-label">Authorization URL</span>
                    <input value={activeCollection.auth.oauth2?.authorizationUrl ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ authorizationUrl: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Access token URL</span>
                  <input value={activeCollection.auth.oauth2?.accessTokenUrl ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                  <span class="field-label">Client ID</span>
                  <input value={activeCollection.auth.oauth2?.clientId ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ clientId: e.currentTarget.value })} />
                  <span class="field-label">Client secret</span>
                  <input type="password" value={activeCollection.auth.oauth2?.clientSecret ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'password'}
                    <span class="field-label">Username</span>
                    <input value={activeCollection.auth.oauth2?.username ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ username: e.currentTarget.value })} />
                    <span class="field-label">Password</span>
                    <input type="password" value={activeCollection.auth.oauth2?.password ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ password: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Scope</span>
                  <input value={activeCollection.auth.oauth2?.scope ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ scope: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">State</span>
                    <input value={activeCollection.auth.oauth2?.state ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ state: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Credentials</span>
                  <select value={activeCollection.auth.oauth2?.credentialsPlacement || 'basic_auth_header'} on:change={(e) => updateCollectionOAuth2Auth({ credentialsPlacement: e.currentTarget.value })}>
                    {#each oauth2CredentialPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code'}
                    <span class="field-label">PKCE</span>
                    <input type="checkbox" checked={activeCollection.auth.oauth2?.pkce ?? false} on:change={(e) => updateCollectionOAuth2Auth({ pkce: e.currentTarget.checked })} />
                  {/if}
                  <span class="field-label">Token source</span>
                  <select value={activeCollection.auth.oauth2?.tokenSource || 'access_token'} on:change={(e) => updateCollectionOAuth2Auth({ tokenSource: e.currentTarget.value })}>
                    {#each oauth2TokenSources as source (source)}
                      <option value={source}>{source}</option>
                    {/each}
                  </select>
                  <span class="field-label">Token placement</span>
                  <select value={activeCollection.auth.oauth2?.tokenPlacement || 'header'} on:change={(e) => updateCollectionOAuth2Auth({ tokenPlacement: e.currentTarget.value })}>
                    {#each oauth2TokenPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if (activeCollection.auth.oauth2?.tokenPlacement || 'header') === 'header'}
                    <span class="field-label">Header prefix</span>
                    <input value={activeCollection.auth.oauth2?.tokenHeaderPrefix || 'Bearer'} on:change={(e) => updateCollectionOAuth2Auth({ tokenHeaderPrefix: e.currentTarget.value })} />
                  {:else}
                    <span class="field-label">Query key</span>
                    <input value={activeCollection.auth.oauth2?.tokenQueryKey || 'access_token'} on:change={(e) => updateCollectionOAuth2Auth({ tokenQueryKey: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Static token</span>
                  <input type="password" value={activeCollection.auth.token} on:change={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
                  <div class="oauth2-extra-stack">
                    <OAuth2AdditionalParams
                      title="Authorization request params"
                      params={activeCollection.auth.oauth2?.authorizationAdditionalParams ?? []}
                      onAdd={(sendIn) => addCollectionOAuth2AdditionalParam('authorizationAdditionalParams', sendIn)}
                      onChange={(index, field, value) => updateCollectionOAuth2AdditionalParam('authorizationAdditionalParams', 'body', index, field, value)}
                      onRemove={(index) => removeCollectionOAuth2AdditionalParam('authorizationAdditionalParams', index)}
                    />
                    <OAuth2AdditionalParams
                      title="Access token request params"
                      params={activeCollection.auth.oauth2?.tokenAdditionalParams ?? []}
                      onAdd={(sendIn) => addCollectionOAuth2AdditionalParam('tokenAdditionalParams', sendIn)}
                      onChange={(index, field, value) => updateCollectionOAuth2AdditionalParam('tokenAdditionalParams', 'body', index, field, value)}
                      onRemove={(index) => removeCollectionOAuth2AdditionalParam('tokenAdditionalParams', index)}
                    />
                    <OAuth2AdditionalParams
                      title="Refresh token request params"
                      params={activeCollection.auth.oauth2?.refreshAdditionalParams ?? []}
                      onAdd={(sendIn) => addCollectionOAuth2AdditionalParam('refreshAdditionalParams', sendIn)}
                      onChange={(index, field, value) => updateCollectionOAuth2AdditionalParam('refreshAdditionalParams', 'body', index, field, value)}
                      onRemove={(index) => removeCollectionOAuth2AdditionalParam('refreshAdditionalParams', index)}
                    />
                  </div>
                {:else if activeCollection.auth?.mode === 'apikey'}
                  <span class="field-label">Key</span>
                  <input value={activeCollection.auth.apiKey} on:change={(e) => updateCollectionAuth({ apiKey: e.currentTarget.value })} />
                  <span class="field-label">Value</span>
                  <input type="password" value={activeCollection.auth.apiValue} on:change={(e) => updateCollectionAuth({ apiValue: e.currentTarget.value })} />
                  <span class="field-label">Send in</span>
	                  <select value={activeCollection.auth.apiLocation || 'header'} on:change={(e) => updateCollectionAuth({ apiLocation: e.currentTarget.value })}>
	                    <option value="header">Header</option>
	                    <option value="query">Query</option>
	                  </select>
	                {:else if activeCollection.auth?.mode === 'awsv4'}
	                  <span class="field-label">Access key ID</span>
	                  <input value={activeCollection.auth.awsv4?.accessKeyId ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
	                  <span class="field-label">Secret access key</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.secretAccessKey ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
	                  <span class="field-label">Session token</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.sessionToken ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ sessionToken: e.currentTarget.value })} />
	                  <span class="field-label">Service</span>
	                  <input value={activeCollection.auth.awsv4?.service ?? ''} placeholder="execute-api" on:change={(e) => updateCollectionAWSV4Auth({ service: e.currentTarget.value })} />
	                  <span class="field-label">Region</span>
	                  <input value={activeCollection.auth.awsv4?.region ?? ''} placeholder="us-east-1" on:change={(e) => updateCollectionAWSV4Auth({ region: e.currentTarget.value })} />
		                  <span class="field-label">Profile</span>
		                  <input value={activeCollection.auth.awsv4?.profileName ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ profileName: e.currentTarget.value })} />
		                {:else if activeCollection.auth?.mode === 'oauth1'}
		                  <span class="field-label">Consumer key</span>
		                  <input value={activeCollection.auth.oauth1?.consumerKey ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ consumerKey: e.currentTarget.value })} />
		                  <span class="field-label">Consumer secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.consumerSecret ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
		                  <span class="field-label">Token</span>
		                  <input value={activeCollection.auth.oauth1?.accessToken ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ accessToken: e.currentTarget.value })} />
		                  <span class="field-label">Token secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.accessTokenSecret ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
		                  <span class="field-label">Signature</span>
		                  <select value={activeCollection.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} on:change={(e) => updateCollectionOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
		                    {#each oauth1SignatureMethods as method (method)}
		                      <option value={method}>{method}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Add params to</span>
		                  <select value={activeCollection.auth.oauth1?.placement || 'header'} on:change={(e) => updateCollectionOAuth1Auth({ placement: e.currentTarget.value })}>
		                    {#each oauth1Placements as placement (placement)}
		                      <option value={placement}>{placement}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Callback URL</span>
		                  <input value={activeCollection.auth.oauth1?.callbackUrl ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ callbackUrl: e.currentTarget.value })} />
		                  <span class="field-label">Verifier</span>
		                  <input value={activeCollection.auth.oauth1?.verifier ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ verifier: e.currentTarget.value })} />
		                  <span class="field-label">Timestamp</span>
		                  <input value={activeCollection.auth.oauth1?.timestamp ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ timestamp: e.currentTarget.value })} />
		                  <span class="field-label">Nonce</span>
		                  <input value={activeCollection.auth.oauth1?.nonce ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ nonce: e.currentTarget.value })} />
		                  <span class="field-label">Version</span>
		                  <input value={activeCollection.auth.oauth1?.version ?? ''} placeholder="1.0" on:change={(e) => updateCollectionOAuth1Auth({ version: e.currentTarget.value })} />
		                  <span class="field-label">Realm</span>
		                  <input value={activeCollection.auth.oauth1?.realm ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ realm: e.currentTarget.value })} />
		                  <span class="field-label">Private key</span>
		                  <textarea class="short" spellcheck="false" value={activeCollection.auth.oauth1?.privateKey ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ privateKey: e.currentTarget.value })}></textarea>
		                  <span class="field-label">Private key type</span>
		                  <select value={activeCollection.auth.oauth1?.privateKeyType || 'text'} on:change={(e) => updateCollectionOAuth1Auth({ privateKeyType: e.currentTarget.value })}>
		                    <option value="text">text</option>
		                    <option value="file">file</option>
		                  </select>
		                  <span class="field-label">Body hash</span>
		                  <input type="checkbox" checked={activeCollection.auth.oauth1?.includeBodyHash ?? false} on:change={(e) => updateCollectionOAuth1Auth({ includeBodyHash: e.currentTarget.checked })} />
		                {:else if activeCollection.auth?.mode !== 'none'}
		                  <div class="empty-state wide">This collection auth mode is marked partial until its backend signer is implemented.</div>
		                {/if}
              </div>
            {:else if collectionTab === 'presets'}
              <div class="field-grid">
                <span class="field-label">Request Type</span>
                <select aria-label="Preset request type" value={normalizePresetRequestType(activeCollection.presets?.requestType) || 'http'} on:change={(e) => updateCollectionPresets({ requestType: e.currentTarget.value })}>
                  <option value="http">HTTP</option>
                  <option value="graphql">GraphQL</option>
                  <option value="grpc">gRPC</option>
                  <option value="websocket">WebSocket</option>
                </select>
                <span class="field-label">Base URL</span>
                <input aria-label="Preset base URL" placeholder="https://api.example.com" value={activeCollection.presets?.requestUrl ?? ''} on:change={(e) => updateCollectionPresets({ requestUrl: e.currentTarget.value })} />
              </div>
	            {:else if collectionTab === 'proxy'}
	              <div class="field-grid">
	                <span class="field-label">Mode</span>
	                <select aria-label="Collection proxy mode" value={collectionProxyMode(activeCollection.proxy)} on:change={(e) => updateCollectionProxyMode(e.currentTarget.value)}>
	                  <option value="inherit">Inherit</option>
	                  <option value="manual">Enabled</option>
	                  <option value="off">Disabled</option>
	                </select>
	              </div>
	              {#if collectionProxyMode(activeCollection.proxy) === 'manual'}
	                <div class="field-grid">
	                  <span class="field-label">Protocol</span>
	                  <select aria-label="Collection proxy protocol" value={activeCollection.proxy?.protocol || 'http'} on:change={(e) => updateCollectionProxy({ protocol: e.currentTarget.value })}>
	                    <option value="http">HTTP</option>
	                    <option value="https">HTTPS</option>
	                    <option value="socks5">SOCKS5</option>
	                  </select>
	                  <span class="field-label">Host</span>
	                  <input aria-label="Collection proxy host" value={activeCollection.proxy?.hostname ?? ''} on:input={(e) => updateCollectionProxy({ hostname: e.currentTarget.value })} />
	                  <span class="field-label">Port</span>
	                  <input aria-label="Collection proxy port" value={activeCollection.proxy?.port ?? ''} on:input={(e) => updateCollectionProxy({ port: e.currentTarget.value })} />
	                  <span class="field-label">Bypass</span>
	                  <input aria-label="Collection proxy bypass" value={activeCollection.proxy?.bypassProxy ?? ''} on:input={(e) => updateCollectionProxy({ bypassProxy: e.currentTarget.value })} />
	                  <span class="field-label">Auth enabled</span>
	                  <input aria-label="Collection proxy auth enabled" type="checkbox" checked={!(activeCollection.proxy?.auth?.disabled ?? false)} on:change={(e) => updateCollectionProxyAuth({ disabled: !e.currentTarget.checked })} />
	                  <span class="field-label">Username</span>
	                  <input aria-label="Collection proxy username" value={activeCollection.proxy?.auth?.username ?? ''} on:input={(e) => updateCollectionProxyAuth({ username: e.currentTarget.value })} />
	                  <span class="field-label">Password</span>
	                  <input aria-label="Collection proxy password" type="password" value={activeCollection.proxy?.auth?.password ?? ''} on:input={(e) => updateCollectionProxyAuth({ password: e.currentTarget.value })} />
	                </div>
	              {/if}
            {:else if collectionTab === 'clientCert'}
              <div class="table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Domain</th>
                      <th>Type</th>
                      <th>Cert file</th>
                      <th>Key file</th>
                      <th>PFX file</th>
                      <th>Passphrase</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each activeCollection.clientCertificates ?? [] as certificate, index (index)}
                      <tr>
                        <td><input aria-label="Client certificate domain" placeholder="example.org" value={certificate.domain ?? ''} on:input={(e) => updateCollectionClientCertificate(index, 'domain', e.currentTarget.value)} /></td>
                        <td>
                          <select value={certificate.type || 'cert'} on:change={(e) => updateCollectionClientCertificate(index, 'type', e.currentTarget.value)}>
                            <option value="cert">Cert</option>
                            <option value="pfx">PFX</option>
                          </select>
                        </td>
                        <td><input aria-label="Client certificate cert file" disabled={(certificate.type || 'cert') === 'pfx'} value={certificate.certFilePath ?? ''} on:input={(e) => updateCollectionClientCertificate(index, 'certFilePath', e.currentTarget.value)} /></td>
                        <td><input aria-label="Client certificate key file" disabled={(certificate.type || 'cert') === 'pfx'} value={certificate.keyFilePath ?? ''} on:input={(e) => updateCollectionClientCertificate(index, 'keyFilePath', e.currentTarget.value)} /></td>
                        <td><input aria-label="Client certificate pfx file" disabled={(certificate.type || 'cert') !== 'pfx'} value={certificate.pfxFilePath ?? ''} on:input={(e) => updateCollectionClientCertificate(index, 'pfxFilePath', e.currentTarget.value)} /></td>
                        <td><input aria-label="Client certificate passphrase" type="password" value={certificate.passphrase ?? ''} on:input={(e) => updateCollectionClientCertificate(index, 'passphrase', e.currentTarget.value)} /></td>
                        <td><button on:click={() => removeCollectionClientCertificate(index)}>Remove</button></td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
              <button on:click={addCollectionClientCertificate}>Add client certificate</button>
            {:else if collectionTab === 'protobuf'}
              <div class="settings-stack">
                <div class="settings-section-header">
                  <h3>Proto Files</h3>
                  <button on:click={addCollectionProtoFile}>Add proto file</button>
                </div>
                <div class="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>Path</th>
                        <th>Type</th>
                        <th>Status</th>
                        <th></th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each activeCollection.protobuf?.protoFiles ?? [] as protoFile, index (protoFile.path)}
                        <tr>
                          <td><input aria-label="Proto file path" placeholder="protos/service.proto" value={protoFile.path ?? ''} on:change={(e) => updateCollectionProtoFile(index, 'path', e.currentTarget.value)} /></td>
                          <td>
                            <select aria-label="Proto file type" value={protoFile.type || 'file'} on:change={(e) => updateCollectionProtoFile(index, 'type', e.currentTarget.value)}>
                              <option value="file">file</option>
                            </select>
                          </td>
                          <td><span class:ok={protoFile.exists} class:bad={!protoFile.exists}>{protoFile.exists ? 'Found' : 'Missing'}</span></td>
                          <td><button on:click={() => removeCollectionProtoFile(index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
                {#if (activeCollection.protobuf?.protoFiles?.length ?? 0) === 0}
                  <div class="empty-state">No proto files</div>
                {/if}

                <div class="settings-section-header">
                  <h3>Import Paths</h3>
                  <button on:click={addCollectionProtoImportPath}>Add import path</button>
                </div>
                <div class="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>Enabled</th>
                        <th>Path</th>
                        <th>Status</th>
                        <th></th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each activeCollection.protobuf?.importPaths ?? [] as importPath, index (importPath.path)}
                        <tr>
                          <td><input aria-label="Enable proto import path" type="checkbox" checked={importPath.enabled} on:change={(e) => updateCollectionProtoImportPath(index, 'enabled', e.currentTarget.checked)} /></td>
                          <td><input aria-label="Proto import path" placeholder="protos" value={importPath.path ?? ''} on:change={(e) => updateCollectionProtoImportPath(index, 'path', e.currentTarget.value)} /></td>
                          <td><span class:ok={importPath.exists} class:bad={!importPath.exists}>{importPath.exists ? 'Found' : 'Missing'}</span></td>
                          <td><button on:click={() => removeCollectionProtoImportPath(index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
                {#if (activeCollection.protobuf?.importPaths?.length ?? 0) === 0}
                  <div class="empty-state">No import paths</div>
                {/if}
              </div>
            {:else if collectionTab === 'script'}
              <span class="field-label">Pre-request</span>
              <textarea class="short" spellcheck="false" value={activeCollection.preScript} on:change={(e) => updateCollectionScript('preScript', e.currentTarget.value)}></textarea>
              <span class="field-label">Post-response</span>
              <textarea class="short" spellcheck="false" value={activeCollection.postScript} on:change={(e) => updateCollectionScript('postScript', e.currentTarget.value)}></textarea>
            {:else if collectionTab === 'tests'}
              <textarea spellcheck="false" value={activeCollection.tests} on:change={(e) => updateCollectionScript('tests', e.currentTarget.value)}></textarea>
            {/if}
          </div>
        </section>
      {:else if activeView === 'git'}
        <section class="panel git-workbench-panel" aria-labelledby="git-workbench-title" data-testid="git-workbench">
          <header class="panel-header">
            <div>
              <h2 id="git-workbench-title" tabindex="-1" bind:this={gitWorkbenchHeading}>Git Workbench</h2>
              <p class="panel-subtitle">Safe, collection-scoped Git actions for {activeCollection?.name ?? 'the active collection'}.</p>
            </div>
            <button type="button" on:click={() => refreshGitWorkbench()} disabled={gitWorkbenchLoading || gitWorkbenchBusy !== ''}>Refresh</button>
          </header>

          <div class="git-workbench-feedback" aria-live="polite" aria-atomic="true">
            {#if gitWorkbenchStatus}<p class="git-status-message">{gitWorkbenchStatus}</p>{/if}
            {#if gitWorkbenchError}<p class="error-text">{gitWorkbenchError}</p>{/if}
          </div>

          {#if !activeCollection}
            <div class="empty-state wide">Select a local collection before opening the Git workbench.</div>
          {:else if gitWorkbenchLoading && !gitWorkbenchSnapshot}
            <div class="empty-state wide">Loading Git status…</div>
          {:else if gitWorkbenchSnapshot && !gitWorkbenchSnapshot.available}
            <div class="empty-state wide">Git is required for this workbench. Install Git, then refresh.</div>
          {:else if gitWorkbenchSnapshot && !gitWorkbenchSnapshot.initialized}
            <div class="git-workbench-empty">
              <h3>Initialize this collection</h3>
              <p>This creates only local Git metadata. It does not stage, commit, or share any files.</p>
              <button class="primary" type="button" on:click={initializeGitWorkbench} disabled={gitWorkbenchBusy !== ''}>Initialize Git</button>
            </div>
          {:else if gitWorkbenchSnapshot}
            <div class="git-summary" aria-label="Repository summary">
              <div><span>Repository</span><strong>{gitWorkbenchSnapshot.rootLabel || 'Local repository'}</strong></div>
              <div><span>Branch</span><strong>{gitWorkbenchSnapshot.detached ? 'Detached HEAD' : gitWorkbenchSnapshot.branch || 'Unborn branch'}</strong></div>
              <div><span>Upstream</span><strong>{gitWorkbenchSnapshot.upstream || 'Not configured'}</strong></div>
              <div><span>Sync</span><strong>{gitWorkbenchSnapshot.ahead} ahead · {gitWorkbenchSnapshot.behind} behind</strong></div>
              <div class:conflict={gitWorkbenchSnapshot.conflicts}><span>Status</span><strong>{gitWorkbenchSnapshot.conflicts ? 'Conflicts need manual resolution' : gitWorkbenchSnapshot.clean ? 'Clean' : 'Scoped changes'}</strong></div>
            </div>

            <section class="git-workbench-section" aria-labelledby="git-files-title">
              <div class="git-section-heading">
                <div><h3 id="git-files-title">Changes</h3><p>{gitWorkbenchSelectedPaths.length} selected · only active collection files are shown.</p></div>
                <div class="button-row compact">
                  <button type="button" on:click={() => viewGitWorkbenchDiff(false)} disabled={gitWorkbenchBusy !== '' || gitWorkbenchSelectedPaths.length !== 1}>View unstaged diff</button>
                  <button type="button" on:click={() => viewGitWorkbenchDiff(true)} disabled={gitWorkbenchBusy !== '' || gitWorkbenchSelectedPaths.length !== 1}>View staged diff</button>
                  <button type="button" on:click={stageGitWorkbenchSelection} disabled={gitWorkbenchBusy !== '' || !canStageGitSelection(gitWorkbenchSelectedPaths, gitWorkbenchSnapshot.files ?? [])}>Stage selected</button>
                  <button type="button" on:click={unstageGitWorkbenchSelection} disabled={gitWorkbenchBusy !== '' || !canUnstageGitSelection(gitWorkbenchSelectedPaths, gitWorkbenchSnapshot.files ?? [])}>Unstage selected</button>
                </div>
              </div>
              {#if (gitWorkbenchSnapshot.files ?? []).length}
                <div class="git-file-table" role="table" aria-label="Collection Git changes">
                  <div class="git-file-row git-file-header" role="row"><span role="columnheader">Select</span><span role="columnheader">File</span><span role="columnheader">Status</span></div>
                  {#each gitWorkbenchSnapshot.files ?? [] as file (file.path)}
                    <div class="git-file-row" class:selected={gitWorkbenchSelectedPaths.includes(file.path)} role="row">
                      <span role="cell"><input type="checkbox" aria-label={`Select ${file.path}`} checked={gitWorkbenchSelectedPaths.includes(file.path)} on:change={(event) => toggleGitWorkbenchPath(file.path, event.currentTarget.checked)} /></span>
                      <span role="cell"><code>{file.path}</code></span>
                      <span class="git-file-badges" role="cell">
                        {#if file.conflicted}<span class="git-badge conflict">Conflict</span>{/if}
                        {#if file.staged}<span class="git-badge staged">Staged {file.index}</span>{/if}
                        {#if file.untracked}<span class="git-badge untracked">Untracked</span>{/if}
                        {#if file.worktree && !file.untracked}<span class="git-badge">Working {file.worktree}</span>{/if}
                        {#if file.binary}<span class="git-badge">Binary</span>{/if}
                      </span>
                    </div>
                  {/each}
                </div>
              {:else}
                <div class="empty-state compact">No scoped changes.</div>
              {/if}
              {#if gitWorkbenchDiff}
                <article class="git-diff-viewer" aria-label={`Git diff ${gitWorkbenchDiff.path}`}>
                  <header><strong>{gitWorkbenchDiff.staged ? 'Staged' : 'Unstaged'} diff · {gitWorkbenchDiff.path}</strong><button type="button" on:click={() => (gitWorkbenchDiff = undefined)}>Close diff</button></header>
                  {#if gitWorkbenchDiff.binary}<p>This file is binary; Git does not provide a text diff.</p>{:else}<pre>{gitWorkbenchDiff.text || 'No text diff is available.'}</pre>{/if}
                  {#if gitWorkbenchDiff.truncated}<p class="muted">This diff is bounded for safe display and was truncated.</p>{/if}
                </article>
              {/if}
            </section>

            <div class="git-workbench-grid">
              <section class="git-workbench-section" aria-labelledby="git-commit-title">
                <h3 id="git-commit-title">Commit staged changes</h3>
                <label>Message<textarea aria-label="Git commit message" bind:value={gitWorkbenchCommitMessage} placeholder="Describe this intentional change" disabled={gitWorkbenchBusy !== ''}></textarea></label>
                <button class="primary" type="button" on:click={commitGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchCommitMessage.trim()}>Commit staged</button>
              </section>

              <section class="git-workbench-section" aria-labelledby="git-branch-title">
                <h3 id="git-branch-title">Branches</h3>
                <label>Current or target branch<select aria-label="Git branch" bind:value={gitWorkbenchBranch} disabled={gitWorkbenchBusy !== ''}>{#each gitWorkbenchSnapshot.branches ?? [] as branch (branch)}<option value={branch}>{branch}</option>{/each}</select></label>
                {#if !canSwitchGitBranch(gitWorkbenchSnapshot)}<p class="muted">Switching is disabled until this collection’s scoped changes and conflicts are resolved.</p>{/if}
                <button type="button" on:click={checkoutGitWorkbenchBranch} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchBranch || !canSwitchGitBranch(gitWorkbenchSnapshot)}>Switch branch</button>
                <label>New branch<input aria-label="New Git branch" bind:value={gitWorkbenchNewBranch} placeholder="feature/name" disabled={gitWorkbenchBusy !== ''} /></label>
                <label class="checkbox-line"><input type="checkbox" bind:checked={gitWorkbenchCheckoutNewBranch} disabled={gitWorkbenchBusy !== ''} />Switch to the new branch after creation</label>
                <button type="button" on:click={createGitWorkbenchBranch} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchNewBranch.trim() || (gitWorkbenchCheckoutNewBranch && !canSwitchGitBranch(gitWorkbenchSnapshot))}>Create branch</button>
              </section>
            </div>

            <section class="git-workbench-section" aria-labelledby="git-remote-title">
              <div class="git-section-heading"><div><h3 id="git-remote-title">Remote sync</h3><p>Use credential-free URLs. Pull is fast-forward only; push never forces.</p></div></div>
              {#if (gitWorkbenchSnapshot.remotes ?? []).length}<div class="git-remote-list" aria-label="Configured Git remotes">{#each gitWorkbenchSnapshot.remotes ?? [] as remote (remote.name)}<span><strong>{remote.name}</strong><code>{remote.url}</code></span>{/each}</div>{/if}
              <div class="git-remote-fields">
                <label>Name<input aria-label="Git remote name" value={gitWorkbenchRemoteName} on:input={(event) => selectGitWorkbenchRemote(event.currentTarget.value)} placeholder="origin" disabled={gitWorkbenchBusy !== ''} /></label>
                <label>Credential-free URL<input aria-label="Git remote URL" bind:value={gitWorkbenchRemoteURL} placeholder="https://host/org/repository.git or file:///…" disabled={gitWorkbenchBusy !== ''} /></label>
                <label>Branch<input aria-label="Git remote branch" bind:value={gitWorkbenchRemoteBranch} placeholder={gitWorkbenchSnapshot.branch || 'main'} disabled={gitWorkbenchBusy !== ''} /></label>
              </div>
              <div class="button-row compact">
                <button type="button" on:click={setGitWorkbenchRemote} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteURL.trim()}>Set / update remote</button>
                <button type="button" on:click={fetchGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim()}>Fetch</button>
                <button type="button" on:click={pullGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteBranch.trim()}>Pull ff-only</button>
                <label class="checkbox-line"><input type="checkbox" bind:checked={gitWorkbenchSetUpstream} disabled={gitWorkbenchBusy !== ''} />Set upstream</label>
                <button class="primary" type="button" on:click={pushGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteBranch.trim() || !canPushGitBranch(gitWorkbenchSnapshot.upstream, gitWorkbenchRemoteName.trim(), gitWorkbenchRemoteBranch.trim(), gitWorkbenchSetUpstream)}>Push</button>
              </div>
              {#if !canPushGitBranch(gitWorkbenchSnapshot.upstream, gitWorkbenchRemoteName.trim(), gitWorkbenchRemoteBranch.trim(), gitWorkbenchSetUpstream)}<p class="muted">Push is disabled until this exact remote/branch is upstream, or you explicitly choose Set upstream.</p>{/if}
            </section>
          {/if}
        </section>
      {:else if activeView === 'runner'}
        <section class="panel">
          <header class="panel-header">
            <h2>Runner</h2>
            <div class="runner-header-actions">
              {#if activeCollectionRun}
                <div class="runner-live-status" role="status" aria-live="polite" aria-atomic="true">
                  <span>{collectionRunCancellationRequested ? 'Cancelling run' : 'Running'}: {activeCollectionRun.collectionName}</span>
                  <button
                    type="button"
                    class="command-cancel"
                    data-testid="runner-cancel-button"
                    aria-label={collectionRunCancellationRequested
                      ? `Cancelling collection run: ${activeCollectionRun.collectionName}`
                      : `Cancel collection run: ${activeCollectionRun.collectionName}`}
                    on:click={() => void cancelCollectionRun()}
                    disabled={collectionRunCancellationRequested}
                  >
                    {collectionRunCancellationRequested ? 'Cancelling run…' : 'Cancel run'}
                  </button>
                </div>
              {/if}
              <button data-testid="runner-run-button" on:click={runCollection} disabled={runnerSelectedCount === 0 || busy !== '' || Boolean(activeCollectionRun)}>
              Run {runnerSelectedCount} Request{runnerSelectedCount === 1 ? '' : 's'}
              </button>
            </div>
          </header>
          <div class="runner-workbench">
            <aside class="runner-config-panel" data-testid="runner-config-panel">
              <div class="runner-config-header">
                <strong data-testid="runner-config-counter">{runnerSelectedCount} of {runnerConfigItems.length} selected</strong>
                <div class="button-row compact">
                  <button type="button" data-testid="runner-select-all" on:click={toggleRunnerSelectAll}>
                    {runnerSelectedCount === runnerConfigItems.length ? 'Deselect All' : 'Select All'}
                  </button>
                  <button type="button" data-testid="runner-config-reset" on:click={resetRunnerConfiguration}>Reset</button>
                </div>
              </div>
              <label class="runner-delay-field">
                <span class="field-label">Delay between requests (ms)</span>
                <input
                  data-testid="runner-delay-input"
                  type="number"
                  min="0"
                  max="600000"
                  value={runnerDelayMs}
                  on:input={(event) => (runnerDelayMs = normalizedRunnerDelayMs(Number(event.currentTarget.value)))}
                />
              </label>
              <div class="runner-request-list">
                {#if runnerConfigItems.length === 0}
                  <div class="empty-state compact">No runnable requests</div>
                {:else}
                  {#each runnerConfigItems as item (item.id)}
                    <label class="runner-request-item" data-testid="runner-request-item">
                      <span class="checkbox-container"><input type="checkbox" checked={runnerItemSelected(item.id)} on:change={(event) => setRunnerItemSelected(item.id, event.currentTarget.checked)} /></span>
                      <span>
                        <strong>{item.name}</strong>
                        <small>{item.method || 'GET'} {item.folderPath || 'Collection'}</small>
                      </span>
                    </label>
                  {/each}
                {/if}
              </div>
            </aside>
            <div class="runner-results">
              <div class="runner-summary">
                <span>Total {state.runner.total}</span>
                <span class="ok">Passed {state.runner.passed}</span>
                <span class="bad">Failed {state.runner.failed}</span>
                {#if activeCollectionRun}
                  <span class="warning">{collectionRunCancellationRequested ? 'Cancellation requested' : 'Run active'}</span>
                {:else if runnerCompletedCancelled}
                  <span class="warning">Cancelled {runnerCancelledCount || 'run'}</span>
                {/if}
                <span>Skipped {state.runner.skipped}</span>
              </div>
              <table>
                <thead><tr><th>Name</th><th>Status</th><th>Code</th><th>Time</th><th>Error</th></tr></thead>
                <tbody>
                  {#each state.runner.results ?? [] as result, index (index)}
                    <tr class:runner-result-cancelled={result.status === 'cancelled'}><td>{result.name}</td><td>{result.status === 'cancelled' ? 'Cancelled' : result.status}</td><td>{result.code}</td><td>{result.durationMs} ms</td><td>{result.error}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </div>
        </section>
      {:else if activeView === 'environments'}
        <section class="panel">
          <header class="panel-header">
            <h2>Environments</h2>
            <div class="split">
              <input aria-label="Global environment name" bind:value={globalEnvironmentName} />
              <button on:click={createGlobalEnvironment}>Create global</button>
            </div>
            <div class="split">
              <input aria-label="Collection environment name" bind:value={environmentName} />
              <button on:click={createEnvironment}>Create</button>
            </div>
          </header>
          <div class="env-grid">
            <article>
              <h3>Global Environment</h3>
              {#if selectedGlobalEnvironment && activeWorkspace}
                <div class="field-grid">
                  <span class="field-label">Active</span>
                  <select aria-label="Active global environment" value={activeWorkspace.activeGlobalEnvironmentId ?? ''} on:change={(e) => setActiveGlobalEnvironment(e.currentTarget.value)}>
                    <option value="">No global environment</option>
                    {#each activeWorkspace.globalEnvironments ?? [] as env (env.id)}
                      <option value={env.id}>{env.name}</option>
                    {/each}
                  </select>
                  <span class="field-label">Name</span>
                  <input aria-label="Global environment editor name" value={selectedGlobalEnvironment.name} on:input={(e) => updateGlobalEnvironmentMetadata('name', e.currentTarget.value)} />
                  <span class="field-label">Color</span>
                  <input aria-label="Global environment color" type="color" value={selectedGlobalEnvironment.color || '#2f8cff'} on:input={(e) => updateGlobalEnvironmentMetadata('color', e.currentTarget.value)} />
                  <span class="field-label">Copy name</span>
                  <input aria-label="Global environment copy name" placeholder={`${selectedGlobalEnvironment.name} - Copy`} bind:value={globalEnvironmentCopyName} />
                  <span class="field-label">Export format</span>
                  <div class="split">
                    <select aria-label="Global environment export format" bind:value={globalEnvironmentExportFormat}>
                      <option value="single-object">Single environment JSON</option>
                      <option value="single-file">Single JSON file</option>
                      <option value="folder">Separate files</option>
                    </select>
                    <label class="checkbox-label">
                      <input aria-label="Export all global environments" type="checkbox" bind:checked={globalEnvironmentExportAll} />
                      All globals
                    </label>
                  </div>
                  <span class="field-label">Export path</span>
                  <input aria-label="Global environment export path" placeholder="Optional file or folder path" bind:value={globalEnvironmentExportPath} />
                </div>
                <nav class="subtabs compact" aria-label="Global environment variable tabs">
                  {#each environmentVariableTabs as tab (tab.id)}
                    <button type="button" class:active={globalEnvironmentVariableTab === tab.id} on:click={() => (globalEnvironmentVariableTab = tab.id)}>{tab.label}</button>
                  {/each}
                </nav>
                <div class="search-box env-search">
                  <input aria-label="Search global environment variables" placeholder="Search variables" bind:value={globalEnvironmentVariableSearch} />
                  {#if globalEnvironmentVariableSearch}
                    <button class="icon-button ghost" title="Clear global environment variable search" on:click={() => (globalEnvironmentVariableSearch = '')}>x</button>
                  {/if}
                </div>
                {#if visibleGlobalEnvironmentRows.length > 0}
                  <table>
                    <thead><tr><th>On</th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                    <tbody>
                      {#each visibleGlobalEnvironmentRows as row (row.variable.id)}
                        <tr>
                          <td><input type="checkbox" checked={row.variable.enabled} on:change={(e) => updateGlobalEnvironmentVariable(row.index, 'enabled', e.currentTarget.checked)} /></td>
                          <td><input aria-label="Global environment variable name" value={row.variable.name} on:input={(e) => updateGlobalEnvironmentVariable(row.index, 'name', e.currentTarget.value)} /></td>
                          <td><input aria-label="Global environment variable value" value={String(row.variable.value ?? '')} on:input={(e) => updateGlobalEnvironmentVariable(row.index, 'value', e.currentTarget.value)} /></td>
                          <td>
                            <select aria-label="Global environment variable type" value={row.variable.dataType || row.variable.type || 'string'} on:change={(e) => updateGlobalEnvironmentVariable(row.index, 'dataType', e.currentTarget.value)}>
                              <option value="string">string</option>
                              <option value="number">number</option>
                              <option value="boolean">boolean</option>
                              <option value="object">object</option>
                            </select>
                          </td>
                          <td><input aria-label="Global environment variable secret" type="checkbox" checked={row.variable.secret} on:change={(e) => updateGlobalEnvironmentVariable(row.index, 'secret', e.currentTarget.checked)} /></td>
                          <td><button on:click={() => removeGlobalEnvironmentVariable(row.index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}
                  <div class="empty-state">{globalEnvironmentVariableQuery ? 'No results found' : `No ${globalEnvironmentVariableTab}`}</div>
                {/if}
                <div class="toolbar">
                  <button on:click={addGlobalEnvironmentVariable}>{environmentVariableAddLabel(globalEnvironmentVariableTab)}</button>
                  <button on:click={copyGlobalEnvironment}>Copy</button>
                  <button on:click={exportGlobalEnvironment} disabled={globalEnvironmentExportAll && globalEnvironmentExportFormat === 'single-object'}>Export</button>
                  <button on:click={saveGlobalEnvironmentExport} disabled={globalEnvironmentExportAll && globalEnvironmentExportFormat === 'single-object'}>Save export</button>
                  <button on:click={importGlobalEnvironment} disabled={!globalEnvironmentPayload.trim()}>Import</button>
                  <button on:click={deleteGlobalEnvironment}>Delete global</button>
                </div>
                {#if globalEnvironmentExportFilename}
                  <div class="muted">Export: {globalEnvironmentExportFilename}</div>
                {/if}
                <textarea
                  class="short"
                  aria-label="Global environment import export"
                  spellcheck="false"
                  bind:value={globalEnvironmentPayload}
                ></textarea>
              {:else}
                <div class="empty-state">Create a global environment</div>
              {/if}
            </article>
            <article>
              <h3>{selectedEnvironment?.name ?? 'No environment'} Variables</h3>
              {#if selectedEnvironment}
                <nav class="subtabs compact" aria-label="Environment variable tabs">
                  {#each environmentVariableTabs as tab (tab.id)}
                    <button type="button" class:active={environmentVariableTab === tab.id} on:click={() => (environmentVariableTab = tab.id)}>{tab.label}</button>
                  {/each}
                </nav>
                <div class="search-box env-search">
                  <input aria-label="Search environment variables" placeholder="Search variables" bind:value={environmentVariableSearch} />
                  {#if environmentVariableSearch}
                    <button class="icon-button ghost" title="Clear environment variable search" on:click={() => (environmentVariableSearch = '')}>x</button>
                  {/if}
                </div>
                {#if visibleEnvironmentRows.length > 0}
                  <table>
                    <thead><tr><th>On</th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                    <tbody>
                      {#each visibleEnvironmentRows as row (row.variable.id)}
                        <tr>
                          <td><input type="checkbox" checked={row.variable.enabled} on:change={(e) => updateEnvironmentVariable(row.index, 'enabled', e.currentTarget.checked)} /></td>
                          <td><input aria-label="Environment variable name" value={row.variable.name} on:input={(e) => updateEnvironmentVariable(row.index, 'name', e.currentTarget.value)} /></td>
                          <td><input aria-label="Environment variable value" value={String(row.variable.value ?? '')} on:input={(e) => updateEnvironmentVariable(row.index, 'value', e.currentTarget.value)} /></td>
                          <td>
                            <select aria-label="Environment variable type" value={row.variable.dataType || row.variable.type || 'string'} on:change={(e) => updateEnvironmentVariable(row.index, 'dataType', e.currentTarget.value)}>
                              <option value="string">string</option>
                              <option value="number">number</option>
                              <option value="boolean">boolean</option>
                              <option value="object">object</option>
                            </select>
                          </td>
                          <td><input aria-label="Environment variable secret" type="checkbox" checked={row.variable.secret} on:change={(e) => updateEnvironmentVariable(row.index, 'secret', e.currentTarget.checked)} /></td>
                          <td><button on:click={() => removeEnvironmentVariable(row.index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}
                  <div class="empty-state">{environmentVariableQuery ? 'No results found' : `No ${environmentVariableTab}`}</div>
                {/if}
                <button on:click={addEnvironmentVariable}>{environmentVariableAddLabel(environmentVariableTab)}</button>
              {:else}
                <div class="empty-state">Create or select an environment</div>
              {/if}
            </article>
            <article>
              <h3>.env Files</h3>
              <div class="field-grid">
                <span class="field-label">Scope</span>
                <select aria-label=".env file scope" bind:value={dotEnvScope}>
                  <option value="workspace">Workspace</option>
                  <option value="collection">Collection</option>
                </select>
                <span class="field-label">Name</span>
                <input aria-label=".env file name" placeholder=".env" bind:value={dotEnvName} />
              </div>
              <div class="toolbar">
                <button on:click={newDotEnvFile}>New</button>
                <button on:click={saveDotEnvFile}>Save</button>
                <button on:click={() => loadDotEnvFiles(true)}>Reload</button>
                <button on:click={deleteDotEnvFile} disabled={!selectedDotEnvFile}>Delete</button>
              </div>
              <div class="tabs compact">
                <button class:active={dotEnvEditorMode === 'table'} on:click={() => (dotEnvEditorMode = 'table')}>Table</button>
                <button class:active={dotEnvEditorMode === 'raw'} on:click={() => (dotEnvEditorMode = 'raw')}>Raw</button>
              </div>
              {#if dotEnvFiles.length > 0}
                <table>
                  <thead><tr><th>Scope</th><th>File</th><th>Runtime</th></tr></thead>
                  <tbody>
                    {#each dotEnvFiles as file (file.path)}
                      <tr class:active={dotEnvFileKey(file) === selectedDotEnvKey}>
                        <td>{file.scope}</td>
                        <td><button on:click={() => selectDotEnvFile(file)}>{file.name}</button></td>
                        <td>{file.runtime ? 'yes' : ''}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              {:else}
                <div class="empty-state">No .env files</div>
              {/if}
              {#if dotEnvEditorMode === 'table'}
                {#if dotEnvRows.length > 0}
                  <table>
                    <thead><tr><th>Name</th><th>Value</th><th></th></tr></thead>
                    <tbody>
                      {#each dotEnvRows as row, index (index)}
                        <tr>
                          <td><input aria-label=".env variable name" value={row.name} on:input={(e) => updateDotEnvRow(row, 'name', e.currentTarget.value)} /></td>
                          <td><input aria-label=".env variable value" value={row.value} on:input={(e) => updateDotEnvRow(row, 'value', e.currentTarget.value)} /></td>
                          <td><button on:click={() => removeDotEnvRow(row)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}
                  <div class="empty-state">No .env variables</div>
                {/if}
                <button on:click={addDotEnvRow}>Add variable</button>
              {:else}
                <textarea class="short" aria-label=".env file content" spellcheck="false" bind:value={dotEnvContent} on:input={() => (dotEnvDirty = true)}></textarea>
              {/if}
            </article>
            <article>
              <h3>Collection Variables</h3>
              <table>
                <tbody>
                  {#each activeCollection?.variables ?? [] as variable, index (variable.id)}
                    <tr>
                      <td><input type="checkbox" checked={variable.enabled} on:change={(e) => updateCollectionVariable(index, 'enabled', e.currentTarget.checked)} /></td>
                      <td><input value={variable.name} on:change={(e) => updateCollectionVariable(index, 'name', e.currentTarget.value)} /></td>
                      <td><input value={String(variable.value ?? '')} on:change={(e) => updateCollectionVariable(index, 'value', e.currentTarget.value)} /></td>
                      <td><input type="checkbox" checked={variable.secret} on:change={(e) => updateCollectionVariable(index, 'secret', e.currentTarget.checked)} /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              <button on:click={addCollectionVariable}>Add variable</button>
            </article>
            <article>
              <h3>Collection Headers</h3>
              <KeyValueTable
                rows={activeCollection?.headers ?? []}
                onAdd={addCollectionHeader}
                onChange={updateCollectionHeader}
                onRemove={removeCollectionHeader}
              />
            </article>
            <article>
              <h3>Collection Auth</h3>
              <div class="field-grid">
                <span class="field-label">Mode</span>
                <select value={activeCollection?.auth?.mode ?? 'none'} on:change={(e) => updateCollectionAuth({ mode: e.currentTarget.value })}>
                  {#each authModes as mode (mode)}
                    <option value={mode}>{mode}</option>
                  {/each}
                </select>
	                {#if activeCollection?.auth?.mode === 'basic' || activeCollection?.auth?.mode === 'digest' || activeCollection?.auth?.mode === 'wsse' || activeCollection?.auth?.mode === 'ntlm'}
                  <span class="field-label">Username</span>
                  <input value={activeCollection.auth.username} on:change={(e) => updateCollectionAuth({ username: e.currentTarget.value })} />
                  <span class="field-label">Password</span>
                  <input type="password" value={activeCollection.auth.password} on:change={(e) => updateCollectionAuth({ password: e.currentTarget.value })} />
                  {#if activeCollection?.auth?.mode === 'ntlm'}
                    <span class="field-label">Domain</span>
                    <input value={activeCollection.auth.domain} on:change={(e) => updateCollectionAuth({ domain: e.currentTarget.value })} />
                  {/if}
                {:else if activeCollection?.auth?.mode === 'bearer'}
                  <span class="field-label">Token</span>
                  <input type="password" value={activeCollection.auth.token} on:change={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
                {:else if activeCollection?.auth?.mode === 'oauth2'}
                  <span class="field-label">Grant</span>
                  <select value={activeCollection.auth.oauth2?.grantType || 'client_credentials'} on:change={(e) => updateCollectionOAuth2Auth({ grantType: e.currentTarget.value })}>
                    {#each oauth2GrantTypes as grant (grant)}
                      <option value={grant}>{grant}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">Callback URL</span>
                    <input value={activeCollection.auth.oauth2?.callbackUrl ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ callbackUrl: e.currentTarget.value })} />
                    <span class="field-label">Authorization URL</span>
                    <input value={activeCollection.auth.oauth2?.authorizationUrl ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ authorizationUrl: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Access token URL</span>
                  <input value={activeCollection.auth.oauth2?.accessTokenUrl ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                  <span class="field-label">Client ID</span>
                  <input value={activeCollection.auth.oauth2?.clientId ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ clientId: e.currentTarget.value })} />
                  <span class="field-label">Client secret</span>
                  <input type="password" value={activeCollection.auth.oauth2?.clientSecret ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'password'}
                    <span class="field-label">Username</span>
                    <input value={activeCollection.auth.oauth2?.username ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ username: e.currentTarget.value })} />
                    <span class="field-label">Password</span>
                    <input type="password" value={activeCollection.auth.oauth2?.password ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ password: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Scope</span>
                  <input value={activeCollection.auth.oauth2?.scope ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ scope: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">State</span>
                    <input value={activeCollection.auth.oauth2?.state ?? ''} on:change={(e) => updateCollectionOAuth2Auth({ state: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Credentials</span>
                  <select value={activeCollection.auth.oauth2?.credentialsPlacement || 'basic_auth_header'} on:change={(e) => updateCollectionOAuth2Auth({ credentialsPlacement: e.currentTarget.value })}>
                    {#each oauth2CredentialPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code'}
                    <span class="field-label">PKCE</span>
                    <input type="checkbox" checked={activeCollection.auth.oauth2?.pkce ?? false} on:change={(e) => updateCollectionOAuth2Auth({ pkce: e.currentTarget.checked })} />
                  {/if}
                  <span class="field-label">Token source</span>
                  <select value={activeCollection.auth.oauth2?.tokenSource || 'access_token'} on:change={(e) => updateCollectionOAuth2Auth({ tokenSource: e.currentTarget.value })}>
                    {#each oauth2TokenSources as source (source)}
                      <option value={source}>{source}</option>
                    {/each}
                  </select>
                  <span class="field-label">Token placement</span>
                  <select value={activeCollection.auth.oauth2?.tokenPlacement || 'header'} on:change={(e) => updateCollectionOAuth2Auth({ tokenPlacement: e.currentTarget.value })}>
                    {#each oauth2TokenPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if (activeCollection.auth.oauth2?.tokenPlacement || 'header') === 'header'}
                    <span class="field-label">Header prefix</span>
                    <input value={activeCollection.auth.oauth2?.tokenHeaderPrefix || 'Bearer'} on:change={(e) => updateCollectionOAuth2Auth({ tokenHeaderPrefix: e.currentTarget.value })} />
                  {:else}
                    <span class="field-label">Query key</span>
                    <input value={activeCollection.auth.oauth2?.tokenQueryKey || 'access_token'} on:change={(e) => updateCollectionOAuth2Auth({ tokenQueryKey: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Static token</span>
                  <input type="password" value={activeCollection.auth.token} on:change={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
                  <div class="oauth2-extra-stack">
                    <OAuth2AdditionalParams
                      title="Authorization request params"
                      params={activeCollection.auth.oauth2?.authorizationAdditionalParams ?? []}
                      onAdd={(sendIn) => addCollectionOAuth2AdditionalParam('authorizationAdditionalParams', sendIn)}
                      onChange={(index, field, value) => updateCollectionOAuth2AdditionalParam('authorizationAdditionalParams', 'body', index, field, value)}
                      onRemove={(index) => removeCollectionOAuth2AdditionalParam('authorizationAdditionalParams', index)}
                    />
                    <OAuth2AdditionalParams
                      title="Access token request params"
                      params={activeCollection.auth.oauth2?.tokenAdditionalParams ?? []}
                      onAdd={(sendIn) => addCollectionOAuth2AdditionalParam('tokenAdditionalParams', sendIn)}
                      onChange={(index, field, value) => updateCollectionOAuth2AdditionalParam('tokenAdditionalParams', 'body', index, field, value)}
                      onRemove={(index) => removeCollectionOAuth2AdditionalParam('tokenAdditionalParams', index)}
                    />
                    <OAuth2AdditionalParams
                      title="Refresh token request params"
                      params={activeCollection.auth.oauth2?.refreshAdditionalParams ?? []}
                      onAdd={(sendIn) => addCollectionOAuth2AdditionalParam('refreshAdditionalParams', sendIn)}
                      onChange={(index, field, value) => updateCollectionOAuth2AdditionalParam('refreshAdditionalParams', 'body', index, field, value)}
                      onRemove={(index) => removeCollectionOAuth2AdditionalParam('refreshAdditionalParams', index)}
                    />
                  </div>
	                {:else if activeCollection?.auth?.mode === 'apikey'}
	                  <span class="field-label">Key</span>
	                  <input value={activeCollection.auth.apiKey} on:change={(e) => updateCollectionAuth({ apiKey: e.currentTarget.value })} />
	                  <span class="field-label">Value</span>
	                  <input type="password" value={activeCollection.auth.apiValue} on:change={(e) => updateCollectionAuth({ apiValue: e.currentTarget.value })} />
	                {:else if activeCollection?.auth?.mode === 'awsv4'}
	                  <span class="field-label">Access key ID</span>
	                  <input value={activeCollection.auth.awsv4?.accessKeyId ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
	                  <span class="field-label">Secret access key</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.secretAccessKey ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
	                  <span class="field-label">Session token</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.sessionToken ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ sessionToken: e.currentTarget.value })} />
	                  <span class="field-label">Service</span>
	                  <input value={activeCollection.auth.awsv4?.service ?? ''} placeholder="execute-api" on:change={(e) => updateCollectionAWSV4Auth({ service: e.currentTarget.value })} />
	                  <span class="field-label">Region</span>
	                  <input value={activeCollection.auth.awsv4?.region ?? ''} placeholder="us-east-1" on:change={(e) => updateCollectionAWSV4Auth({ region: e.currentTarget.value })} />
		                  <span class="field-label">Profile</span>
		                  <input value={activeCollection.auth.awsv4?.profileName ?? ''} on:change={(e) => updateCollectionAWSV4Auth({ profileName: e.currentTarget.value })} />
		                {:else if activeCollection?.auth?.mode === 'oauth1'}
		                  <span class="field-label">Consumer key</span>
		                  <input value={activeCollection.auth.oauth1?.consumerKey ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ consumerKey: e.currentTarget.value })} />
		                  <span class="field-label">Consumer secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.consumerSecret ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
		                  <span class="field-label">Token</span>
		                  <input value={activeCollection.auth.oauth1?.accessToken ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ accessToken: e.currentTarget.value })} />
		                  <span class="field-label">Token secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.accessTokenSecret ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
		                  <span class="field-label">Signature</span>
		                  <select value={activeCollection.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} on:change={(e) => updateCollectionOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
		                    {#each oauth1SignatureMethods as method (method)}
		                      <option value={method}>{method}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Add params to</span>
		                  <select value={activeCollection.auth.oauth1?.placement || 'header'} on:change={(e) => updateCollectionOAuth1Auth({ placement: e.currentTarget.value })}>
		                    {#each oauth1Placements as placement (placement)}
		                      <option value={placement}>{placement}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Callback URL</span>
		                  <input value={activeCollection.auth.oauth1?.callbackUrl ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ callbackUrl: e.currentTarget.value })} />
		                  <span class="field-label">Verifier</span>
		                  <input value={activeCollection.auth.oauth1?.verifier ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ verifier: e.currentTarget.value })} />
		                  <span class="field-label">Timestamp</span>
		                  <input value={activeCollection.auth.oauth1?.timestamp ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ timestamp: e.currentTarget.value })} />
		                  <span class="field-label">Nonce</span>
		                  <input value={activeCollection.auth.oauth1?.nonce ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ nonce: e.currentTarget.value })} />
		                  <span class="field-label">Version</span>
		                  <input value={activeCollection.auth.oauth1?.version ?? ''} placeholder="1.0" on:change={(e) => updateCollectionOAuth1Auth({ version: e.currentTarget.value })} />
		                  <span class="field-label">Realm</span>
		                  <input value={activeCollection.auth.oauth1?.realm ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ realm: e.currentTarget.value })} />
		                  <span class="field-label">Private key</span>
		                  <textarea class="short" spellcheck="false" value={activeCollection.auth.oauth1?.privateKey ?? ''} on:change={(e) => updateCollectionOAuth1Auth({ privateKey: e.currentTarget.value })}></textarea>
		                  <span class="field-label">Private key type</span>
		                  <select value={activeCollection.auth.oauth1?.privateKeyType || 'text'} on:change={(e) => updateCollectionOAuth1Auth({ privateKeyType: e.currentTarget.value })}>
		                    <option value="text">text</option>
		                    <option value="file">file</option>
		                  </select>
		                  <span class="field-label">Body hash</span>
		                  <input type="checkbox" checked={activeCollection.auth.oauth1?.includeBodyHash ?? false} on:change={(e) => updateCollectionOAuth1Auth({ includeBodyHash: e.currentTarget.checked })} />
		                {/if}
              </div>
            </article>
          </div>
        </section>
      {:else if activeView === 'import'}
        <section class="panel import-panel">
          <header class="panel-header">
            <div>
              <h2>Import</h2>
              <p class="panel-subtitle">Choose local files first, review the plan, then import the selected valid sources.</p>
            </div>
            <div class="toolbar">
              <button on:click={exportCollection}>Export active</button>
            </div>
          </header>
          <nav class="import-source-tabs" aria-label="Import source">
            {#each [['files', 'Files'], ['url', 'URL'], ['paste', 'Paste'], ['git', 'Git repository']] as [mode, label] (mode)}
              <button type="button" aria-pressed={importSourceMode === mode} class:active={importSourceMode === mode} on:click={() => selectImportSourceMode(mode as ImportSourceMode)}>{label}</button>
            {/each}
          </nav>
          <div class="import-planning">
            {#if importSourceMode === 'files'}
              <div class="import-file-actions">
                <button class="primary" bind:this={importPickerButton} type="button" on:click={chooseImportFiles} disabled={busy !== ''}>Choose files…</button>
                <button type="button" on:click={chooseImportFolder} disabled={busy !== ''}>Choose collection folder…</button>
              </div>
              <button type="button" class="import-drop-target" style="--wails-drop-target: drop" aria-label="Drop collection import files or folders here" on:click={chooseImportFiles}>
                Drop files or a Bruno/OpenCollection folder here. Press Enter to choose files.
              </button>
            {:else if importSourceMode === 'url'}
              <div class="import-inline-form">
                <label for="import-url">HTTP or HTTPS URL</label>
                <input id="import-url" bind:value={importURL} placeholder="https://example.com/collection.json" />
                <button class="primary" type="button" on:click={previewURLImport} disabled={busy !== '' || !importURL.trim()}>Preview URL</button>
              </div>
              <p class="panel-subtitle">URL credentials are rejected. Signed query URLs are fetched by LiteAPI but never shown in the plan.</p>
            {:else if importSourceMode === 'paste'}
              <div class="import-inline-form paste-import-form">
                <label for="import-paste-name">Safe display name</label>
                <input id="import-paste-name" bind:value={importPasteName} />
                <label for="import-paste-content">Raw import or cURL command</label>
                <textarea id="import-paste-content" spellcheck="false" bind:value={importContent} placeholder="Paste Postman, OpenAPI, Bruno JSON, or a cURL command"></textarea>
                <button class="primary" type="button" on:click={previewPasteImport} disabled={busy !== '' || !importContent.trim()}>Preview pasted import</button>
              </div>
            {:else}
              <div class="import-stack git-import-stack">
                <h3>Git Repository</h3>
                <label for="git-import-url">Remote URL</label>
                <input id="git-import-url" placeholder="https://github.com/org/repo.git" bind:value={gitCloneURL} />
                <label for="git-import-root">Clone location</label>
                <input id="git-import-root" bind:value={gitCloneRoot} />
                <div class="button-row"><button on:click={checkGitVersion} disabled={busy !== ''}>Check Git</button><button on:click={scanGitCollections} disabled={busy !== ''}>Scan path</button><button class="primary" on:click={cloneGitRepository} disabled={busy !== '' || !gitCloneURL.trim()}>{gitCloneInProgress ? 'Cloning' : 'Clone'}</button></div>
              </div>
            {/if}

            {#if importPreview && importSourceMode !== 'git'}
              <div class="import-destination">
                <label for="import-workspace">Destination workspace</label>
                <select id="import-workspace" bind:value={importDestinationWorkspaceID} on:change={() => void previewImportSources(importSources)}>
                  {#each state.workspaces ?? [] as workspace (workspace.id)}<option value={workspace.id}>{workspace.name}</option>{/each}
                </select>
                <label for="import-destination-root">Advanced destination root (absolute, optional)</label>
                <input id="import-destination-root" bind:value={importDestinationRoot} placeholder="Uses the workspace folder" on:change={() => void previewImportSources(importSources)} />
              </div>
              <div class="import-preview" aria-label="Import preview">
                {#each importPreview.rows ?? [] as row (row.candidateId)}
                  {@const decision = importDecisionFor(row)}
                  <article class:error={Boolean(row.error)} class:warning={(row.warnings?.length ?? 0) > 0} class="import-preview-row" data-import-preview-row tabindex="-1">
                    <header>
                      <label><input type="checkbox" checked={decision.selected} disabled={Boolean(row.error) || row.conflict === 'unavailable'} on:change={(event) => updateImportDecision(row.candidateId, { selected: event.currentTarget.checked })} /> Import</label>
                      <strong>{row.sourceName || 'Import source'}</strong>
                      <span>{row.detectedKind} · {row.confidence}</span>
                      <button type="button" aria-label={`Toggle ${row.sourceName} details`} on:click={() => importExpanded = { ...importExpanded, [row.candidateId]: !importExpanded[row.candidateId] }}>{importExpanded[row.candidateId] ? 'Hide details' : 'Details'}</button>
                    </header>
                    <p class="import-source-path">{row.sourcePath || 'Pasted content'}</p>
                    <div class="import-row-summary"><span>{row.collectionName || 'No collection detected'}</span><span>{row.destinationPath || 'Destination pending'}</span><span>{row.conflict || 'ready'}</span>{#if row.openSemantics}<span>{row.openSemantics}</span>{/if}</div>
                    {#if row.error}<p class="import-row-error" role="alert">{row.error}</p>{/if}
                    {#if (row.warnings?.length ?? 0) > 0 || (row.losses?.length ?? 0) > 0}<p class="import-row-warning">{[...(row.warnings ?? []), ...(row.losses ?? [])].join(' · ')}</p>{/if}
                    {#if importExpanded[row.candidateId]}
                      <div class="import-row-details">
                        <label>Manual format override <select value={decision.kindOverride} on:change={(event) => { updateImportDecision(row.candidateId, { kindOverride: event.currentTarget.value }); void updateImportOverride(row, event.currentTarget.value) }}><option value="">Automatic</option><option value="postman">Postman</option><option value="insomnia">Insomnia</option><option value="bruno-json">Bruno JSON</option><option value="bru">BRU</option><option value="openapi">OpenAPI</option><option value="curl">cURL</option></select></label>
                        {#if !row.error}
                          <label>Output name <input value={decision.outputName} on:input={(event) => updateImportDecision(row.candidateId, { outputName: event.currentTarget.value })} /></label>
                          <label>Conflict action <select value={decision.conflictAction} on:change={(event) => updateImportDecision(row.candidateId, { conflictAction: event.currentTarget.value })}><option value="">Default safe rename</option><option value="rename">Rename</option><option value="skip">Skip</option><option value="replace">Replace existing</option></select></label>
                        {#if row.existingFolder}<p>This is an existing collection folder. It opens as one whole collection; child filtering is unavailable.</p>{:else}
                          <div class="import-hierarchy"><strong>Contents</strong>
                            <div>Environments <button type="button" on:click={() => updateImportDecision(row.candidateId, { environments: (row.environments ?? []).map((entry) => entry.selectionId) })}>All</button> <button type="button" on:click={() => updateImportDecision(row.candidateId, { environments: [] })}>None</button></div>
                            {#each row.environments ?? [] as entry, index (index)}<label><input type="checkbox" checked={decision.environments.includes(entry.selectionId)} on:change={(event) => toggleImportChild(row, 'environments', entry.selectionId, event.currentTarget.checked)} /> {entry.name}</label>{/each}
                            <div>Folders <button type="button" on:click={() => updateImportDecision(row.candidateId, { folders: (row.folders ?? []).map((entry) => entry.selectionId) })}>All</button> <button type="button" on:click={() => updateImportDecision(row.candidateId, { folders: [] })}>None</button></div>
                            {#each row.folders ?? [] as entry, index (index)}<label><input type="checkbox" checked={decision.folders.includes(entry.selectionId)} on:change={(event) => toggleImportChild(row, 'folders', entry.selectionId, event.currentTarget.checked)} /> {entry.path || entry.name}</label>{/each}
                            <div>Requests <button type="button" on:click={() => updateImportDecision(row.candidateId, { requests: (row.requests ?? []).map((entry) => entry.selectionId) })}>All</button> <button type="button" on:click={() => updateImportDecision(row.candidateId, { requests: [] })}>None</button></div>
                            {#each row.requests ?? [] as entry, index (index)}<label><input type="checkbox" checked={decision.requests.includes(entry.selectionId)} on:change={(event) => toggleImportChild(row, 'requests', entry.selectionId, event.currentTarget.checked)} /> {entry.folderPath ? `${entry.folderPath} / ` : ''}{entry.method || entry.type} {entry.name}</label>{/each}
                          </div>
                        {/if}
                        {/if}
                      </div>
                    {/if}
                  </article>
                {/each}
              </div>
              <footer class="import-action-footer"><span>{importReadyRows.length} valid source{importReadyRows.length === 1 ? '' : 's'} selected</span><button class="primary" bind:this={importApplyButton} data-testid="import-apply-selected" type="button" on:click={requestPlannedImport} disabled={busy !== '' || importApplyInFlight || importReadyRows.length === 0}>Apply selected imports</button></footer>
            {/if}
            {#if importApplyResult}<div class="import-results" aria-live="polite"><strong>{importStatus}</strong>{#each [...(importApplyResult.applied ?? []), ...(importApplyResult.skipped ?? []), ...(importApplyResult.errors ?? [])] as row, index (index)}<p>{row.sourceName}: {row.error || (importApplyResult.skipped?.some((entry) => entry.candidateId === row.candidateId) ? 'Skipped' : 'Imported')}</p>{/each}</div>{/if}
            <p class="import-live" aria-live="polite">{importStatus}</p>
          </div>
        </section>
      {:else if activeView === 'devtools'}
        {@render devToolsPanel()}
      {:else if activeView === 'network'}
        <section class="panel">
          <header class="panel-header"><h2>Network Log</h2></header>
          <table>
            <thead><tr><th>Method</th><th>URL</th><th>Status</th><th>Time</th><th>Error</th></tr></thead>
            <tbody>
              {#each state.networkLog ?? [] as row (row.id)}
                <tr><td>{row.method}</td><td>{row.url}</td><td>{row.status}</td><td>{row.durationMs} ms</td><td>{row.error}</td></tr>
              {/each}
            </tbody>
          </table>
        </section>
	      {:else if activeView === 'cookies'}
	        <section class="panel">
	          <header class="panel-header">
	            <div>
              <h2>Cookies</h2>
              <p class="panel-subtitle">{visibleCookieCount}/{state.cookies?.length ?? 0} stored cookies</p>
            </div>
            <div class="runner-summary">
              <input aria-label="Search cookies" placeholder="Search cookies" bind:value={cookieSearch} />
              <button on:click={clearCookies} disabled={(state.cookies?.length ?? 0) === 0 || busy !== ''}>Clear all</button>
            </div>
          </header>
          <div class="cookie-manager">
            <div class="cookie-editor">
              <section>
                <h3>{cookieForm.id ? 'Edit cookie' : 'Add cookie'}</h3>
                <div class="field-grid cookie-form">
                  <span class="field-label">Name</span>
                  <input aria-label="Cookie name" bind:value={cookieForm.name} />
                  <span class="field-label">Value</span>
                  <input aria-label="Cookie value" bind:value={cookieForm.value} />
                  <span class="field-label">Domain</span>
                  <input aria-label="Cookie domain" placeholder="api.example.com" bind:value={cookieForm.domain} />
                  <span class="field-label">Path</span>
                  <input aria-label="Cookie path" bind:value={cookieForm.path} />
                  <span class="field-label">Expires</span>
                  <input aria-label="Cookie expires" placeholder="2026-06-30T12:00:00Z" bind:value={cookieForm.expires} disabled={cookieForm.session} />
                  <span class="field-label">SameSite</span>
                  <select aria-label="Cookie same site" bind:value={cookieForm.sameSite}>
                    <option value="">None</option>
                    <option value="lax">Lax</option>
                    <option value="strict">Strict</option>
                    <option value="default">Default</option>
                  </select>
                </div>
                <div class="cookie-checks">
                  <label><input type="checkbox" bind:checked={cookieForm.session} /> Session</label>
                  <label><input type="checkbox" bind:checked={cookieForm.secure} /> Secure</label>
                  <label><input type="checkbox" bind:checked={cookieForm.httpOnly} /> HTTP only</label>
                  <label><input type="checkbox" bind:checked={cookieForm.hostOnly} /> Host only</label>
                </div>
                <div class="button-row">
                  <button class="primary" on:click={saveCookieForm} disabled={busy !== ''}>Save cookie</button>
                  <button on:click={resetCookieForm}>Reset</button>
                </div>
              </section>

              <section>
                <h3>Raw Set-Cookie</h3>
                <div class="field-grid cookie-form">
                  <span class="field-label">URL</span>
                  <input aria-label="Raw cookie URL" bind:value={rawCookieURL} />
                  <span class="field-label">Header</span>
                  <textarea class="short" aria-label="Raw Set-Cookie header" spellcheck="false" bind:value={rawCookieHeader}></textarea>
                </div>
                <button on:click={importRawCookie} disabled={busy !== ''}>Import header</button>
              </section>
            </div>

            <div class="cookie-domains">
              {#if (state.cookies?.length ?? 0) === 0}
                <div class="empty-state">No stored cookies</div>
              {:else if visibleCookieGroups.length === 0}
                <div class="empty-state">No matching cookies</div>
              {:else}
                {#each visibleCookieGroups as group (group.domain)}
                  <article>
                    <header>
                      <div>
                        <h3>{group.domain}</h3>
                        <p class="panel-subtitle">{group.cookies.length} cookie{group.cookies.length === 1 ? '' : 's'}</p>
                      </div>
                      <button on:click={() => clearDomainCookies(group.domain)} disabled={busy !== ''}>Clear domain</button>
                    </header>
                    <small>{group.header}</small>
                    <table>
                      <thead><tr><th>Name</th><th>Value</th><th>Path</th><th>Expires</th><th>Flags</th><th></th></tr></thead>
                      <tbody>
                        {#each group.cookies as cookie (cookie.id)}
                          <tr>
                            <td>{cookie.name}</td>
                            <td>{cookie.value}</td>
                            <td>{cookie.path}</td>
                            <td>{cookieExpiry(cookie)}</td>
                            <td>{cookieFlags(cookie)}</td>
                            <td>
                              <div class="button-row compact">
                                <button on:click={() => editCookie(cookie)}>Edit</button>
                                <button class="icon-button" title="Delete cookie" on:click={() => deleteCookie(cookie.id)}>x</button>
                              </div>
                            </td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  </article>
                {/each}
              {/if}
	            </div>
	          </div>
	        </section>
	      {:else if activeView === 'preferences'}
	        <section class="panel preferences-panel">
	          <header class="panel-header">
	            <div>
	              <h2>Preferences</h2>
	              <p class="panel-subtitle">Theme {selectedThemeMode} · Proxy {proxyModeLabel(preferencesProxyMode(state.preferences))}</p>
	            </div>
	          </header>
	          <div class="settings-stack">
	            <section>
	              <div class="settings-section-header">
	                <h3>Appearance</h3>
	              </div>
	              <div class="theme-mode-selector" aria-label="Theme mode">
	                {#each themeModes as mode (mode.id)}
	                  <button
	                    class:selected={selectedThemeMode === mode.id}
	                    aria-pressed={selectedThemeMode === mode.id}
	                    on:click={() => updateThemeMode(mode.id)}
	                  >
	                    {mode.label}
	                  </button>
	                {/each}
	              </div>

	              {#if selectedThemeMode === 'light' || selectedThemeMode === 'system'}
	                <div class="theme-variant-section">
	                  <span class="field-label">Light Theme</span>
	                  <div class="theme-variants">
	                    {#each lightThemeVariants as variant (variant.id)}
	                      <button
	                        class="theme-variant-card"
	                        class:selected={(state.preferences.themeVariantLight || 'light') === variant.id}
	                        aria-label={`Light theme ${variant.name}`}
	                        aria-pressed={(state.preferences.themeVariantLight || 'light') === variant.id}
	                        on:click={() => updateThemeVariant('light', variant.id)}
	                      >
	                        <span class="theme-preview" style={`--preview-bg: ${variant.preview.background}; --preview-sidebar: ${variant.preview.sidebar}; --preview-accent: ${variant.preview.accent};`}>
	                          <span class="theme-preview-sidebar"></span>
	                          <span class="theme-preview-main">
	                            <span></span>
	                            <span></span>
	                            <span></span>
	                          </span>
	                        </span>
	                        <span>{variant.name}</span>
	                      </button>
	                    {/each}
	                  </div>
	                </div>
	              {/if}

	              {#if selectedThemeMode === 'dark' || selectedThemeMode === 'system'}
	                <div class="theme-variant-section">
	                  <span class="field-label">Dark Theme</span>
	                  <div class="theme-variants">
	                    {#each darkThemeVariants as variant (variant.id)}
	                      <button
	                        class="theme-variant-card"
	                        class:selected={(state.preferences.themeVariantDark || 'dark') === variant.id}
	                        aria-label={`Dark theme ${variant.name}`}
	                        aria-pressed={(state.preferences.themeVariantDark || 'dark') === variant.id}
	                        on:click={() => updateThemeVariant('dark', variant.id)}
	                      >
	                        <span class="theme-preview" style={`--preview-bg: ${variant.preview.background}; --preview-sidebar: ${variant.preview.sidebar}; --preview-accent: ${variant.preview.accent};`}>
	                          <span class="theme-preview-sidebar"></span>
	                          <span class="theme-preview-main">
	                            <span></span>
	                            <span></span>
	                            <span></span>
	                          </span>
	                        </span>
	                        <span>{variant.name}</span>
	                      </button>
	                    {/each}
	                  </div>
	                </div>
	              {/if}
		            </section>

		            <section>
		              <div class="settings-section-header">
		                <h3>Display</h3>
		                <span class="preference-value" data-testid="zoom-percentage-value">{appZoomPercentage}%</span>
		              </div>
		              <div class="font-preference-grid">
		                <label class="field-label" for="code-font-input">Code Editor Font</label>
		                <label class="field-label" for="code-font-size-input">Font Size</label>
		                <input
		                  id="code-font-input"
		                  data-testid="code-font-input"
		                  aria-label="Code Editor Font"
		                  value={codeFont}
		                  autocapitalize="off"
		                  autocomplete="off"
		                  autocorrect="off"
		                  spellcheck="false"
		                  on:input={(event) => updateCodeFont(event.currentTarget.value)}
		                />
		                <input
		                  id="code-font-size-input"
		                  data-testid="code-font-size-input"
		                  aria-label="Font Size"
		                  type="number"
		                  min="1"
		                  max="32"
		                  inputmode="numeric"
		                  value={codeFontSize}
		                  on:input={(event) => updateCodeFontSize(Number(event.currentTarget.value))}
		                />
		              </div>
		              <div class="zoom-preference-row">
		                <label class="field-label" for="zoom-percentage">Zoom</label>
		                <select
		                  id="zoom-percentage"
		                  data-testid="zoom-percentage-select"
		                  aria-label="App zoom"
		                  value={appZoomPercentage}
		                  on:change={(event) => setZoomPercentage(Number(event.currentTarget.value))}
		                >
		                  {#each zoomPercentages as percentage (percentage)}
		                    <option value={percentage}>{percentage}%</option>
		                  {/each}
		                </select>
		                <button
		                  data-testid="zoom-reset-btn"
		                  on:click={resetZoomPercentage}
		                  disabled={appZoomPercentage === zoomDefaultPercentage}
		                >
		                  Reset
		                </button>
		              </div>
		            </section>

		            <section>
		              <div class="settings-section-header">
		                <h3>General Settings</h3>
		              </div>
		              <div class="general-preferences-grid">
		                <label class="inline-toggle">
		                  <input
		                    id="sslVerification"
		                    data-testid="ssl-verification-toggle"
		                    type="checkbox"
		                    checked={state.preferences.request?.sslVerification !== false}
		                    on:change={(event) => updateRequestPreferences({ sslVerification: event.currentTarget.checked } as Partial<main.RequestPreferences>)}
		                  />
		                  SSL/TLS Certificate Verification
		                </label>
		                <label class="inline-toggle">
		                  <input
		                    id="customCaCertificateEnabled"
		                    data-testid="custom-ca-enabled-toggle"
		                    type="checkbox"
		                    checked={state.preferences.request?.customCaCertificate?.enabled ?? false}
		                    on:change={(event) => updateRequestPreferences({
		                      customCaCertificate: {
		                        ...(state?.preferences.request?.customCaCertificate ?? {}),
		                        enabled: event.currentTarget.checked
		                      } as main.CustomCaCertificatePreferences
		                    })}
		                  />
		                  Use Custom CA Certificate
		                </label>
		                <div class:settings-disabled={!(state.preferences.request?.customCaCertificate?.enabled ?? false)} class="path-picker-row">
		                  {#if state.preferences.request?.customCaCertificate?.filePath}
		                    <span class="selected-path-chip" data-testid="custom-ca-file-name">
		                      {customCaFileName(state.preferences.request.customCaCertificate.filePath)}
		                      <button type="button" aria-label="Remove custom CA certificate" on:click={clearCustomCaCertificate}>x</button>
		                    </span>
		                  {:else}
		                    <button
		                      type="button"
		                      data-testid="custom-ca-select-btn"
		                      on:click={browseCustomCaCertificate}
		                      disabled={!(state.preferences.request?.customCaCertificate?.enabled ?? false)}
		                    >
		                      Select File
		                    </button>
		                  {/if}
		                </div>
		                <label class:settings-disabled={!((state.preferences.request?.customCaCertificate?.enabled ?? false) && state.preferences.request?.customCaCertificate?.filePath)} class="inline-toggle">
		                  <input
		                    id="keepDefaultCaCertificatesEnabled"
		                    data-testid="keep-default-ca-toggle"
		                    type="checkbox"
		                    checked={state.preferences.request?.keepDefaultCaCertificates?.enabled !== false}
		                    disabled={!((state.preferences.request?.customCaCertificate?.enabled ?? false) && state.preferences.request?.customCaCertificate?.filePath)}
		                    on:change={(event) => updateRequestPreferences({
		                      keepDefaultCaCertificates: { enabled: event.currentTarget.checked } as main.KeepDefaultCaCertificatesPreferences
		                    })}
		                  />
		                  Keep Default CA Certificates
		                </label>
		                <label class="inline-toggle">
		                  <input
		                    id="storeCookies"
		                    data-testid="store-cookies-toggle"
		                    type="checkbox"
		                    checked={state.preferences.request?.storeCookies ?? state.preferences.storeCookies ?? true}
		                    on:change={(event) => updateRequestPreferences({ storeCookies: event.currentTarget.checked } as Partial<main.RequestPreferences>)}
		                  />
		                  Store Cookies automatically
		                </label>
		                <label class="inline-toggle">
		                  <input
		                    id="sendCookies"
		                    data-testid="send-cookies-toggle"
		                    type="checkbox"
		                    checked={state.preferences.request?.sendCookies ?? true}
		                    on:change={(event) => updateRequestPreferences({ sendCookies: event.currentTarget.checked } as Partial<main.RequestPreferences>)}
		                  />
		                  Send Cookies automatically
		                </label>
		                <div class="field-grid compact-preference-grid">
		                  <label class="field-label" for="requestTimeout">Request Timeout (in ms)</label>
		                  <input
		                    id="requestTimeout"
		                    data-testid="request-timeout-input"
		                    value={state.preferences.request?.timeout ?? 0}
		                    inputmode="numeric"
		                    on:input={(event) => updateRequestPreferences({ timeout: Number(event.currentTarget.value) } as Partial<main.RequestPreferences>)}
		                  />
		                </div>
		                <label class="inline-toggle">
		                  <input
		                    id="autoSaveEnabled"
		                    data-testid="autosave-enabled-toggle"
		                    type="checkbox"
		                    checked={state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false}
		                    on:change={(event) => updateAutoSavePreferences({ enabled: event.currentTarget.checked } as Partial<main.AutoSavePreferences>)}
		                  />
		                  Enable Auto Save
		                </label>
		                <div class:settings-disabled={!(state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false)} class="field-grid compact-preference-grid">
		                  <label class="field-label" for="autoSaveInterval">Save Delay (in ms)</label>
		                  <input
		                    id="autoSaveInterval"
		                    data-testid="autosave-interval-input"
		                    value={state.preferences.autoSave?.interval ?? 1000}
		                    disabled={!(state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false)}
		                    inputmode="numeric"
		                    on:input={(event) => updateAutoSavePreferences({ interval: Number(event.currentTarget.value) } as Partial<main.AutoSavePreferences>)}
		                  />
		                </div>
		                <div class="field-grid default-location-grid">
		                  <label class="field-label" for="defaultLocation">Default Location</label>
		                  <div class="default-location-control">
		                    <input
		                      id="defaultLocation"
		                      data-testid="default-location-input"
		                      class="default-location-input"
		                      readonly
		                      value={state.preferences.general?.defaultLocation ?? state.preferences.defaultCollectionPath ?? ''}
		                      placeholder="Click to browse for default location"
		                      on:click={browseDefaultLocation}
		                    />
		                    <button type="button" data-testid="default-location-browse-btn" on:click={browseDefaultLocation}>Browse</button>
		                    <button
		                      type="button"
		                      data-testid="default-location-clear-btn"
		                      on:click={clearDefaultLocation}
		                      disabled={!((state.preferences.general?.defaultLocation ?? state.preferences.defaultCollectionPath ?? '').trim())}
		                    >
		                      Clear
		                    </button>
		                  </div>
		                </div>
		              </div>
		            </section>
	
		            <section>
		              <div class="settings-section-header">
		                <h3>OAuth2</h3>
		              </div>
		              <label class="inline-toggle oauth2-browser-toggle">
		                <input
		                  type="checkbox"
		                  checked={state.preferences.oauth2UseSystemBrowser ?? false}
		                  on:change={(e) => updateAppearancePreferences({ oauth2UseSystemBrowser: e.currentTarget.checked } as Partial<main.Preferences>)}
		                />
		                Use system browser for OAuth
		              </label>
		            </section>
	
	            <section class="keybindings-preference-section">
	              <details class="keybindings-disclosure">
	                <summary>
	                  <span class="keybindings-summary-title">Keybindings</span>
	                  <span class="keybindings-summary-status">
	                    {#if keybindingsAreEnabled(state.preferences)}
	                      {Object.keys(state.preferences.keyBindings ?? {}).length === 0
	                        ? 'Enabled · defaults'
	                        : `Enabled · ${Object.keys(state.preferences.keyBindings ?? {}).length} customized`}
	                    {:else}
	                      Disabled
	                    {/if}
	                  </span>
	                </summary>
	                <div class:settings-disabled={!keybindingsAreEnabled(state.preferences)} class="keybindings-table-wrap">
	                <table class="keybindings-table">
	                  <thead>
	                    <tr>
	                      <th>Command</th>
	                      <th>Keybinding</th>
	                      <th></th>
	                    </tr>
	                  </thead>
	                  <tbody>
	                    {#each keyBindingSections as section (section.heading)}
	                      <tr class="keybinding-section-row">
	                        <td colspan="3">{section.heading}</td>
	                      </tr>
	                      {#each visibleKeyBindingEntries(section) as [action, binding] (action)}
	                        {@const value = recordingKeybindingAction === action ? keybindingDraft : keyBindingDisplayValue(action)}
	                        {@const canEdit = keyBindingCanEdit(action) && keybindingsAreEnabled(state.preferences)}
	                        <tr>
	                          <td>
	                            <span>{binding.name}</span>
	                          </td>
	                          <td>
	                            <input
	                              class="keybinding-input"
	                              class:error={recordingKeybindingAction === action && Boolean(keybindingError)}
	                              aria-label={`Keybinding ${binding.name}`}
	                              readonly
	                              disabled={!canEdit}
	                              value={formatKeyBinding(value)}
	                              placeholder="Press shortcut"
	                              on:focus={() => beginRecordKeyBinding(action)}
	                              on:keydown={(e) => recordKeyBinding(action, e)}
	                              on:blur={() => stopRecordKeyBinding(action)}
	                            />
	                            {#if recordingKeybindingAction === action && keybindingError}
	                              <small class="keybinding-error">{keybindingError}</small>
	                            {/if}
	                          </td>
	                          <td>
	                            {#if binding.readOnly}
	                              <span class="muted">Locked</span>
	                            {:else}
	                              <button on:click={() => resetKeyBinding(action)} disabled={!keyBindingIsCustomized(action)}>Reset</button>
	                            {/if}
	                          </td>
	                        </tr>
	                      {/each}
	                    {/each}
	                  </tbody>
	                </table>
	                </div>
	              </details>
	              <div class="settings-section-actions keybindings-section-actions">
	                <label class="inline-toggle">
	                  <input
	                    type="checkbox"
	                    aria-label="Enable keybindings"
	                    checked={keybindingsAreEnabled(state.preferences)}
	                    on:change={(e) => updateKeybindingsEnabled(e.currentTarget.checked)}
	                  />
	                  Enabled
	                </label>
	                <button aria-label="Reset all keybindings to defaults" on:click={resetAllKeyBindings} disabled={Object.keys(state.preferences.keyBindings ?? {}).length === 0}>Reset Default</button>
	              </div>
	            </section>

	            <section>
	              <div class="settings-section-header">
	                <h3>Proxy Settings</h3>
	              </div>
	              <div class="field-grid">
	                <span class="field-label">Mode</span>
	                <select aria-label="App proxy mode" value={preferencesProxyMode(state.preferences)} on:change={(e) => updatePreferencesProxyMode(e.currentTarget.value)}>
	                  <option value="off">Off</option>
	                  <option value="manual">On</option>
	                  <option value="inherit">System Proxy</option>
	                  <option value="pac">PAC</option>
	                </select>
	              </div>

	              {#if preferencesProxyMode(state.preferences) === 'manual'}
	                <div class="field-grid">
	                  <span class="field-label">Protocol</span>
	                  <select aria-label="App proxy protocol" value={state.preferences.proxy?.config?.protocol || 'http'} on:change={(e) => updatePreferencesProxyConfig({ protocol: e.currentTarget.value })}>
	                    <option value="http">HTTP</option>
	                    <option value="https">HTTPS</option>
	                    <option value="socks5">SOCKS5</option>
	                  </select>
	                  <span class="field-label">Host</span>
	                  <input aria-label="App proxy host" value={state.preferences.proxy?.config?.hostname ?? ''} on:input={(e) => updatePreferencesProxyConfig({ hostname: e.currentTarget.value })} />
	                  <span class="field-label">Port</span>
	                  <input aria-label="App proxy port" value={state.preferences.proxy?.config?.port ?? ''} on:input={(e) => updatePreferencesProxyConfig({ port: e.currentTarget.value })} />
	                  <span class="field-label">Bypass</span>
	                  <input aria-label="App proxy bypass" value={state.preferences.proxy?.config?.bypassProxy ?? ''} on:input={(e) => updatePreferencesProxyConfig({ bypassProxy: e.currentTarget.value })} />
	                  <span class="field-label">Auth enabled</span>
	                  <input aria-label="App proxy auth enabled" type="checkbox" checked={!(state.preferences.proxy?.config?.auth?.disabled ?? false)} on:change={(e) => updatePreferencesProxyAuth({ disabled: !e.currentTarget.checked })} />
	                  <span class="field-label">Username</span>
	                  <input aria-label="App proxy username" value={state.preferences.proxy?.config?.auth?.username ?? ''} on:input={(e) => updatePreferencesProxyAuth({ username: e.currentTarget.value })} />
	                  <span class="field-label">Password</span>
	                  <input aria-label="App proxy password" type="password" value={state.preferences.proxy?.config?.auth?.password ?? ''} on:input={(e) => updatePreferencesProxyAuth({ password: e.currentTarget.value })} />
	                </div>
	              {:else if preferencesProxyMode(state.preferences) === 'pac'}
	                <div class="field-grid">
	                  <span class="field-label">PAC Source</span>
	                  <input aria-label="PAC source" placeholder="https://example.com/proxy.pac or file:///path/proxy.pac" value={state.preferences.proxy?.pac?.source ?? ''} on:change={(e) => updatePreferencesProxy({ pac: { source: e.currentTarget.value } as main.ProxyPACConfig })} />
	                </div>
	              {/if}
	            </section>

	            <section>
	              <div class="settings-section-header">
	                <h3>Cache</h3>
	              </div>
	              <div class="cache-preference-card">
	                <div>
	                  <strong>File cache <span class="beta-badge">Beta</span></strong>
	                  <p>Loads your workspace faster by caching opened collections. Clearing it won't affect your original files.</p>
	                  <p class="cache-size">Cache size <strong>{fileCacheSize === undefined ? '-' : formatRuntimeBytes(fileCacheSize)}</strong></p>
	                </div>
	                <label class="inline-toggle">
	                  <input
	                    data-testid="cache.file.enabled"
	                    type="checkbox"
	                    checked={state.preferences.cache?.file?.enabled ?? false}
	                    on:change={(event) => updateFileCache(event.currentTarget.checked)}
	                  />
	                  Enabled
	                </label>
	                <button type="button" data-testid="file-cache-clear-btn" disabled={!fileCacheSize} on:click={clearFileCache}>Clear cache</button>
	              </div>
	              <div class="cache-preference-card">
	                <div>
	                  <strong>SSL session cache</strong>
	                  <p>Reuses TLS sessions and connections across requests for faster handshakes.</p>
	                </div>
	                <label class="inline-toggle">
	                  <input
	                    data-testid="sslSession.enabled"
	                    type="checkbox"
	                    checked={state.preferences.cache?.sslSession?.enabled ?? false}
	                    on:change={(event) => updateSSLSessionCache(event.currentTarget.checked)}
	                  />
	                  Enabled
	                </label>
	                <button type="button" data-testid="ssl-session-clear-btn" on:click={clearSSLSessionCache}>Clear cache</button>
	              </div>
	            </section>

	          </div>
	        </section>
	      {:else if activeView === 'features'}
	        <section class="panel">
	          <header class="panel-header">
            <h2>Local Capabilities</h2>
            <div class="runner-summary">
              <span>{doneFeatures}/{totalFeatures} done</span>
              <span>{partialFeatures} partial</span>
              <button on:click={resetDemoData}>Reset demo data</button>
            </div>
          </header>
          <div class="feature-grid">
            {#each state.featureLedger ?? [] as feature (feature.id)}
              <article>
                <header>
                  <strong>{feature.name}</strong>
                  <span class={statusClass(feature.status)}>{feature.status}</span>
                </header>
                <p>{feature.description}</p>
                <small>{feature.category}</small>
              </article>
            {/each}
          </div>
        </section>
      {/if}
      {#if devToolsOpen}
        <div class="devtools-drawer" style={`--devtools-drawer-height: ${devToolsDrawerHeight}px;`} aria-label="Dev Tools drawer">
          <button
            type="button"
            class="devtools-drawer-resizer"
            aria-label="Resize Dev Tools drawer"
            on:mousedown={startDevToolsDrawerResize}
          ></button>
          {@render devToolsPanel()}
        </div>
      {/if}
    </section>
  </main>
{:else}
  <main class="boot">
    <p>{error || 'LiteAPI could not load state.'}</p>
    <button on:click={load}>Retry</button>
  </main>
{/if}

{#if importReplaceConfirmationOpen}
  <div class="prompt-backdrop">
    <div
      class="prompt-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="import-replace-confirmation-title"
      aria-describedby="import-replace-confirmation-description"
      tabindex="-1"
      data-testid="import-replace-confirmation-modal"
      on:keydown={handleImportReplaceConfirmationKeydown}
    >
      <header>
        <h2 id="import-replace-confirmation-title">Replace existing collections?</h2>
      </header>
      <p id="import-replace-confirmation-description">Replace the selected existing collection folders? Backups are retained until import persistence succeeds.</p>
      <div class="button-row modal-footer">
        <button type="button" bind:this={importReplaceConfirmationCancelButton} data-testid="import-replace-confirmation-cancel" on:click={cancelImportReplaceConfirmation}>Cancel</button>
        <button class="danger-button" type="button" data-testid="import-replace-confirmation-confirm" on:click={confirmImportReplace}>Replace collections</button>
      </div>
    </div>
  </div>
{/if}

{#if workspaceWindowPickerOpen}
  <WorkspaceWindowPicker
    targets={workspaceWindowTargets}
    currentWorkspaceId={state?.activeWorkspaceId ?? ''}
    busy={workspaceWindowPickerBusy}
    busyAction={workspaceWindowPickerBusyAction}
    error={workspaceWindowPickerError}
    onOpen={openSelectedWorkspaceInNewWindow}
    onCreate={createWorkspaceForNewWindow}
    onCancel={closeWorkspaceWindowPicker}
  />
{/if}

{#if creationOpen}
  <div class="prompt-backdrop">
    <div class="prompt-dialog compact-create-dialog" role="dialog" aria-modal="true" aria-labelledby="new-request-title" tabindex="-1" on:keydown={handleCreationDialogKeydown}>
    <form on:submit|preventDefault={submitCreationFlow}>
      <header>
        <div>
          <h2 id="new-request-title">New request</h2>
          <p>Create a local scratch request in {activeCollection?.name ?? 'the active collection'}.</p>
        </div>
        <button type="button" class="icon-button" aria-label="Close new request" title="Close" on:click={() => void closeCreationFlow()}>×</button>
      </header>
      <label>
        <span>Name</span>
        <input data-new-request-name aria-label="New request name" bind:value={requestName} placeholder="Untitled request" />
      </label>
      <label>
        <span>Protocol</span>
        <select aria-label="New request protocol" bind:value={requestType}>
          <option value="http">HTTP</option>
          <option value="graphql">GraphQL</option>
          <option value="websocket">WebSocket</option>
          <option value="grpc">gRPC</option>
        </select>
      </label>
      <footer class="button-row">
        <button type="button" on:click={() => void closeCreationFlow()}>Cancel</button>
        <button class="primary" type="submit" disabled={!activeCollection}>Create request</button>
      </footer>
    </form>
    </div>
  </div>
{/if}

{#if commandPaletteOpen}
  <div class="prompt-backdrop">
    <div class="global-search-modal command-palette" role="dialog" aria-modal="true" aria-labelledby="command-palette-title" tabindex="-1" on:keydown={handleCommandPaletteKeydown}>
      <header>
        <div><h2 id="command-palette-title">Command palette</h2></div>
        <button type="button" class="icon-button" aria-label="Close command palette" title="Close" on:click={() => void closeCommandPalette()}>×</button>
      </header>
      <input class="global-search-input" bind:this={commandPaletteInput} bind:value={commandPaletteQuery} on:input={() => (commandPaletteActiveIndex = 0)} aria-label="Filter commands" aria-controls="command-palette-commands" aria-activedescendant={visibleCommandPaletteActions[commandPaletteActiveIndex] ? `command-palette-option-${visibleCommandPaletteActions[commandPaletteActiveIndex].id}` : undefined} placeholder="Type a command" />
      <div id="command-palette-commands" class="global-search-results" role="listbox" aria-label="Commands">
        {#each visibleCommandPaletteActions as action, index (action.id)}
          <button id={`command-palette-option-${action.id}`} type="button" role="option" aria-selected={index === commandPaletteActiveIndex} class:active={index === commandPaletteActiveIndex} on:mouseenter={() => (commandPaletteActiveIndex = index)} on:click={() => runCommandPaletteAction(action)}>
            <span class="global-search-main"><strong>{action.label}</strong></span>
            {#if action.shortcut}<kbd>{action.shortcut}</kbd>{/if}
          </button>
        {:else}
          <div class="empty-state">No commands match.</div>
        {/each}
      </div>
    </div>
  </div>
{/if}

{#if globalSearchOpen}
  <div class="prompt-backdrop">
    <div class="global-search-modal" role="dialog" aria-modal="true" aria-labelledby="global-search-title" tabindex="-1">
      <header>
        <div>
          <h2 id="global-search-title">Global Search</h2>
        </div>
        <button type="button" class="icon-button" title="Close" on:click={closeGlobalSearch}>x</button>
      </header>
      <input
        class="global-search-input"
        aria-label="Global search"
        placeholder="Search collections and requests"
        bind:this={globalSearchInput}
        bind:value={globalSearchQuery}
        on:keydown={handleGlobalSearchKeydown}
      />
      {#if globalSearchResults.length === 0}
        <div class="empty-state">No results found</div>
      {:else}
        <div class="global-search-results">
          {#each globalSearchResults as result, index (result.id)}
            <button
              type="button"
              class:active={index === globalSearchIndex}
              on:mousemove={() => (globalSearchIndex = index)}
              on:click={() => selectGlobalSearchResult(result)}
            >
              <span class="global-search-type">{result.type}</span>
              <span class="global-search-main">
                <strong>{result.name}</strong>
                <small>{result.subtitle}</small>
              </span>
              <span class="global-search-meta">{result.meta}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{/if}

{#if notificationsOpen}
  <div class="prompt-backdrop">
    <div class="notification-modal" role="dialog" aria-modal="true" aria-labelledby="notifications-title" tabindex="-1">
      <header>
        <div>
          <h2 id="notifications-title">Notifications</h2>
          <p>{unreadNotificationCount} unread</p>
        </div>
        <button type="button" class="icon-button" title="Close notifications" on:click={closeNotifications}>x</button>
      </header>
      <div class="notification-tabs">
        <div class="segmented compact">
          <button type="button" class:active={notificationTab === 'all'} on:click={() => setNotificationTab('all')}>All</button>
          <button type="button" class:active={notificationTab === 'unread'} on:click={() => setNotificationTab('unread')}>
            Unread
            {#if unreadNotificationCount > 0}
              <span class="notification-tab-count">{unreadNotificationCount}</span>
            {/if}
          </button>
        </div>
        <div class="button-row compact">
          <button type="button" on:click={markAllNotificationsRead} disabled={unreadNotificationCount === 0 || busy !== ''}>Mark all as read</button>
          <button type="button" on:click={clearNotifications} disabled={visibleNotifications.length === 0 || busy !== ''}>Clear all</button>
        </div>
      </div>
      <div class="notification-content">
        <div class="notification-list" aria-label="Notifications list">
          {#if listedNotifications.length === 0}
            <div class="notification-empty">No notifications to show.</div>
          {:else}
            {#each listedNotifications as notification (notification.id)}
              <button
                type="button"
                class:active={selectedNotification?.id === notification.id}
                class:unread={!notification.read}
                on:click={() => selectNotification(notification)}
              >
                <span class="notification-list-title">{notificationTitle(notification)}</span>
                <span>{notificationDate(notification)}</span>
              </button>
            {/each}
          {/if}
        </div>
        <article class="notification-detail">
          {#if selectedNotification}
            <div class="notification-detail-meta">
              <span class={`notification-type ${notificationLevelClass(selectedNotification)}`}>{notificationType(selectedNotification)}</span>
              <span>{notificationDate(selectedNotification)}</span>
            </div>
            <h3>{notificationTitle(selectedNotification)}</h3>
            <p>{notificationDescription(selectedNotification)}</p>
          {:else}
            <div class="notification-empty">You are all caught up!</div>
          {/if}
        </article>
      </div>
    </div>
  </div>
{/if}

{#if oauth2AuthorizationRequest}
  <div class="prompt-backdrop oauth2-auth-backdrop">
    <div class="prompt-dialog oauth2-auth-dialog" role="dialog" aria-modal="true" aria-labelledby="oauth2-auth-title">
      <header>
        <h2 id="oauth2-auth-title">OAuth2 Authorization</h2>
        <button type="button" class="icon-button" title="Close" on:click={closeOAuth2Authorization}>x</button>
      </header>
      <div class="oauth2-auth-frame-wrap">
        {#key oauth2FrameKey}
          <iframe
            class="oauth2-auth-frame"
            title="OAuth2 Authorization"
            src={oauth2AuthorizationRequest.authorizeUrl}
            sandbox="allow-forms allow-modals allow-popups allow-same-origin allow-scripts"
          ></iframe>
        {/key}
      </div>
      <div class="oauth2-auth-controls">
        <button type="button" on:click={openOAuth2AuthorizationInSystemBrowser}>Open in System Browser</button>
        <label>
          <span>Callback URL</span>
          <input
            aria-label="OAuth2 callback URL"
            value={oauth2CallbackURLInput}
            on:input={(event) => (oauth2CallbackURLInput = event.currentTarget.value)}
          />
        </label>
        <button type="button" class="primary" on:click={submitOAuth2CallbackURL} disabled={!oauth2CallbackURLInput.trim()}>Submit Callback</button>
      </div>
      {#if oauth2CallbackMessage}
        <p class="oauth2-auth-message">{oauth2CallbackMessage}</p>
      {/if}
    </div>
  </div>
{/if}

{#if creatingResponseExample && activeRequest}
  <div class="prompt-backdrop">
    <div class="prompt-dialog create-example-dialog" role="dialog" aria-modal="true" aria-labelledby="create-example-title">
      <form on:submit|preventDefault={createResponseExample}>
        <header>
          <h2 id="create-example-title">Create Response Example</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelCreateResponseExample}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Example Name</span>
            <input
              bind:this={createResponseExampleInput}
              aria-label="Create example name"
              value={createResponseExampleName}
              on:input={(event) => (createResponseExampleName = event.currentTarget.value)}
            />
          </label>
          <label>
            <span>Description</span>
            <textarea
              aria-label="Create example description"
              rows="3"
              value={createResponseExampleDescription}
              on:input={(event) => (createResponseExampleDescription = event.currentTarget.value)}
            ></textarea>
          </label>
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelCreateResponseExample}>Cancel</button>
          <button class="primary" type="submit" disabled={busy !== '' || !createResponseExampleName.trim()}>Create Example</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if showShareCollectionModal && activeCollection}
  <div class="prompt-backdrop">
    <div class="prompt-dialog share-collection-dialog" role="dialog" aria-modal="true" aria-labelledby="share-collection-title" data-testid="share-collection-modal">
      <form on:submit|preventDefault={shareCollectionProceed}>
        <header>
          <h2 id="share-collection-title">Share Collection</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelShareCollectionModal}>x</button>
        </header>
        <div class="share-collection-content">
          <p>Bruno-compatible collection exports use <a href="https://opencollection.com" target="_blank" rel="noreferrer">OpenCollection</a>, an open format for API collections.</p>

          <div class="share-section-title">Bruno-compatible format</div>
          <div class="share-format-grid">
            <button
              type="button"
              class:selected={shareCollectionFormat === 'zip'}
              class="share-format-card"
              data-testid="share-format-zip"
              aria-pressed={shareCollectionFormat === 'zip'}
              on:click={() => (shareCollectionFormat = 'zip')}
            >
              <div class="share-card-header">
                <span>Bruno Collection (ZIP)</span>
                <em>Recommended</em>
              </div>
              <p>OpenCollection format organized as folders and files</p>
              <ul>
                <li>Folder structure with individual .yml files</li>
                <li>Collaborate with your team via pull requests</li>
                <li>Extract and open directly in Bruno</li>
              </ul>
              <small>Best for: Team collaboration, version control, publishing</small>
            </button>

            <button
              type="button"
              class:selected={shareCollectionFormat === 'yaml'}
              class="share-format-card"
              data-testid="share-format-yaml"
              aria-pressed={shareCollectionFormat === 'yaml'}
              on:click={() => (shareCollectionFormat = 'yaml')}
            >
              <div class="share-card-header">
                <span>Single File (YAML)</span>
              </div>
              <p>OpenCollection format bundled into one .yml file</p>
              <ul>
                <li>Everything in a single YAML file</li>
                <li>Paste in a gist or attach to an issue</li>
              </ul>
              <small>Best for: Quick sharing as a single file</small>
            </button>
          </div>

          <div class="share-section-title">Other Format</div>
          <button
            type="button"
            class:selected={shareCollectionFormat === 'postman'}
            class="share-other-format"
            data-testid="share-format-postman"
            aria-pressed={shareCollectionFormat === 'postman'}
            on:click={() => (shareCollectionFormat = 'postman')}
          >
            <strong>Postman</strong>
            <span>Export for Postman</span>
          </button>

          {#if shareCollectionFormat === 'postman' && shareCollectionUnsupportedTypes.length > 0}
            <div class="share-warning" data-testid="share-postman-warning">
              Note: {shareCollectionUnsupportedTypes.join(', ')} requests in this collection will not be exported
            </div>
          {/if}
        </div>
        <div class="button-row modal-footer">
          <button type="button" on:click={cancelShareCollectionModal}>Cancel</button>
          <button class="primary" type="submit" data-testid="share-collection-proceed" disabled={busy !== ''}>{busy === 'share collection' ? 'Exporting...' : 'Proceed'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if newFolderTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="new-folder-title" data-testid="new-folder-modal">
      <form on:submit|preventDefault={confirmNewFolder}>
        <header>
          <h2 id="new-folder-title">New Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelNewFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Folder Name</span>
            <input
              id="folder-name"
              name="folderName"
              aria-label="Folder Name"
              data-testid="new-folder-input"
              value={newFolderNameDraft}
              on:input={(event) => updateNewFolderName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="new-folder-options-toggle" on:click={() => (newFolderShowFilesystemName = !newFolderShowFilesystemName)}>
              {newFolderShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if newFolderShowFilesystemName}
            <label>
              <span>Folder Name (on filesystem)</span>
              <div class="inline-field-action">
                {#if newFolderDirectoryEditing}
                  <input
                    id="file-name"
                    name="directoryName"
                    aria-label="Folder Name on filesystem"
                    data-testid="new-folder-directory-name"
                    value={newFolderDirectoryDraft}
                    on:input={(event) => (newFolderDirectoryDraft = event.currentTarget.value)}
                  />
                {:else}
                  <input
                    aria-label="Folder Name on filesystem"
                    data-testid="new-folder-directory-name"
                    readonly
                    value={newFolderDirectoryDraft}
                  />
                {/if}
                <button
                  type="button"
                  data-testid="new-folder-directory-toggle"
                  on:click={() => {
                    newFolderDirectoryEditing = !newFolderDirectoryEditing
                    if (!newFolderDirectoryEditing) newFolderDirectoryDraft = sanitizeCollectionFolderName(newFolderNameDraft)
                  }}
                >{newFolderDirectoryEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if newFolderDirectoryDraft && !collectionFolderNameIsValid(newFolderDirectoryDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
          {#if newFolderDirectoryIsReservedRoot()}
            <p class="field-error">The folder name "environments" at the root is reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" data-testid="new-folder-cancel" on:click={cancelNewFolderModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== '' || newFolderNameDraft.trim() === '' || !newFolderDirectoryNameIsValid()}
          >{busy === 'new folder' ? 'Creating...' : 'Create'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if renameFolderTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="rename-folder-title" data-testid="rename-folder-modal">
      <form on:submit|preventDefault={confirmRenameFolder}>
        <header>
          <h2 id="rename-folder-title">Rename Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelRenameFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Folder Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Rename folder name"
              data-testid="rename-folder-name"
              value={renameFolderNameDraft}
              on:input={(event) => updateRenameFolderName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="rename-folder-options-toggle" on:click={() => (renameFolderShowFilesystemName = !renameFolderShowFilesystemName)}>
              {renameFolderShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if renameFolderShowFilesystemName}
            <label>
              <span>Folder Name (on filesystem)</span>
              <div class="inline-field-action">
                {#if renameFolderDirectoryEditing}
                  <input
                    id="file-name"
                    name="filename"
                    aria-label="Rename folder filesystem name"
                    data-testid="rename-folder-directory-name"
                    value={renameFolderDirectoryDraft}
                    on:input={(event) => (renameFolderDirectoryDraft = event.currentTarget.value)}
                  />
                {:else}
                  <input
                    aria-label="Rename folder filesystem name"
                    data-testid="rename-folder-directory-name"
                    readonly
                    value={renameFolderDirectoryDraft}
                  />
                {/if}
                <button
                  type="button"
                  data-testid="rename-folder-directory-toggle"
                  on:click={() => {
                    renameFolderDirectoryEditing = !renameFolderDirectoryEditing
                    if (!renameFolderDirectoryEditing) renameFolderDirectoryDraft = sanitizeCollectionFolderName(renameFolderNameDraft)
                  }}
                >{renameFolderDirectoryEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if renameFolderDirectoryDraft && !collectionFolderNameIsValid(renameFolderDirectoryDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
          {#if renameFolderDirectoryIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" data-testid="rename-folder-cancel" on:click={cancelRenameFolderModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="rename-item-button"
            disabled={busy !== '' || renameFolderNameDraft.trim() === '' || !renameFolderDirectoryNameIsValid()}
          >{busy === 'rename folder' ? 'Renaming...' : 'Rename'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if cloneFolderTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="clone-folder-title">
      <form on:submit|preventDefault={confirmCloneFolder}>
        <header>
          <h2 id="clone-folder-title">Clone Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelCloneFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Folder Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Clone folder name"
              data-testid="clone-folder-name"
              value={cloneFolderNameDraft}
              on:input={(event) => updateCloneFolderName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="clone-folder-options-toggle" on:click={() => (cloneFolderShowFilesystemName = !cloneFolderShowFilesystemName)}>
              {cloneFolderShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if cloneFolderShowFilesystemName}
            <label>
              <span>Folder Name (on filesystem)</span>
              <div class="inline-field-action">
                {#if cloneFolderDirectoryEditing}
                  <input
                    id="file-name"
                    name="filename"
                    aria-label="Clone folder filesystem name"
                    data-testid="clone-folder-directory-name"
                    value={cloneFolderDirectoryDraft}
                    on:input={(event) => (cloneFolderDirectoryDraft = event.currentTarget.value)}
                  />
                {:else}
                  <input
                    aria-label="Clone folder filesystem name"
                    data-testid="clone-folder-directory-name"
                    readonly
                    value={cloneFolderDirectoryDraft}
                  />
                {/if}
                <button
                  type="button"
                  data-testid="clone-folder-directory-toggle"
                  on:click={() => {
                    cloneFolderDirectoryEditing = !cloneFolderDirectoryEditing
                    if (!cloneFolderDirectoryEditing) cloneFolderDirectoryDraft = sanitizeCollectionFolderName(cloneFolderNameDraft)
                  }}
                >{cloneFolderDirectoryEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if cloneFolderDirectoryDraft && !collectionFolderNameIsValid(cloneFolderDirectoryDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
          {#if cloneFolderDirectoryIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelCloneFolderModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="clone-item-button"
            disabled={busy !== '' || cloneFolderNameDraft.trim() === '' || !cloneFolderDirectoryNameIsValid()}
          >{busy === 'clone folder' ? 'Cloning...' : 'Clone'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if renameRequestTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="rename-request-title">
      <form on:submit|preventDefault={confirmRenameRequest}>
        <header>
          <h2 id="rename-request-title">Rename Request</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelRenameRequestModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Request Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Rename request name"
              data-testid="rename-request-name"
              value={renameRequestNameDraft}
              on:input={(event) => updateRenameRequestName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="rename-request-options-toggle" on:click={() => (renameRequestShowFilesystemName = !renameRequestShowFilesystemName)}>
              {renameRequestShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if renameRequestShowFilesystemName}
            <label>
              <span>File Name (on filesystem)</span>
              <div class="inline-field-action">
                <div class="filename-with-extension">
                  {#if renameRequestFilenameEditing}
                    <input
                      id="file-name"
                      name="filename"
                      aria-label="Rename request filesystem name"
                      data-testid="rename-request-file-name"
                      value={renameRequestFilenameDraft}
                      on:input={(event) => (renameRequestFilenameDraft = event.currentTarget.value)}
                    />
                  {:else}
                    <input
                      aria-label="Rename request filesystem name"
                      data-testid="rename-request-file-name"
                      readonly
                      value={renameRequestFilenameDraft}
                    />
                  {/if}
                  <span>{renameRequestTarget.collection.format === 'yml' || renameRequestTarget.collection.format === 'yaml' ? '.yml' : '.bru'}</span>
                </div>
                <button
                  type="button"
                  data-testid="rename-request-edit-icon"
                  on:click={() => {
                    renameRequestFilenameEditing = !renameRequestFilenameEditing
                    if (!renameRequestFilenameEditing) renameRequestFilenameDraft = sanitizeCollectionFolderName(renameRequestNameDraft)
                  }}
                >{renameRequestFilenameEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if renameRequestFilenameDraft && !collectionFolderNameIsValid(renameRequestFilenameDraft)}
            <p class="field-error">File name is not valid.</p>
          {/if}
          {#if renameRequestFilenameIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelRenameRequestModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="rename-item-button"
            disabled={busy !== '' || renameRequestNameDraft.trim() === '' || !renameRequestFilenameIsValid()}
          >{busy === 'rename request' ? 'Renaming...' : 'Rename'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if cloneRequestTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="clone-request-title">
      <form on:submit|preventDefault={confirmCloneRequest}>
        <header>
          <h2 id="clone-request-title">Clone Request</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelCloneRequestModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Request Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Clone request name"
              data-testid="clone-request-name"
              value={cloneRequestNameDraft}
              on:input={(event) => updateCloneRequestName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="clone-request-options-toggle" on:click={() => (cloneRequestShowFilesystemName = !cloneRequestShowFilesystemName)}>
              {cloneRequestShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if cloneRequestShowFilesystemName}
            <label>
              <span>File Name (on filesystem)</span>
              <div class="inline-field-action">
                <div class="filename-with-extension">
                  {#if cloneRequestFilenameEditing}
                    <input
                      id="file-name"
                      name="filename"
                      aria-label="Clone request filesystem name"
                      data-testid="clone-request-file-name"
                      value={cloneRequestFilenameDraft}
                      on:input={(event) => (cloneRequestFilenameDraft = event.currentTarget.value)}
                    />
                  {:else}
                    <input
                      aria-label="Clone request filesystem name"
                      data-testid="clone-request-file-name"
                      readonly
                      value={cloneRequestFilenameDraft}
                    />
                  {/if}
                  <span>{cloneRequestTarget.collection.format === 'yml' || cloneRequestTarget.collection.format === 'yaml' ? '.yml' : '.bru'}</span>
                </div>
                <button
                  type="button"
                  data-testid="clone-request-filename-toggle"
                  on:click={() => {
                    cloneRequestFilenameEditing = !cloneRequestFilenameEditing
                    if (!cloneRequestFilenameEditing) cloneRequestFilenameDraft = sanitizeCollectionFolderName(cloneRequestNameDraft)
                  }}
                >{cloneRequestFilenameEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if cloneRequestFilenameDraft && !collectionFolderNameIsValid(cloneRequestFilenameDraft)}
            <p class="field-error">File name is not valid.</p>
          {/if}
          {#if cloneRequestFilenameIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelCloneRequestModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="clone-item-button"
            disabled={busy !== '' || cloneRequestNameDraft.trim() === '' || !cloneRequestFilenameIsValid()}
          >{busy === 'clone request' ? 'Cloning...' : 'Clone'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if itemInfoTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog item-info-dialog" role="dialog" aria-modal="true" aria-labelledby="item-info-title">
      <header>
        <h2 id="item-info-title">Info</h2>
        <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={closeItemInfoModal}>x</button>
      </header>
      <div class="prompt-fields">
        <table class="item-info-table">
          <tbody>
            <tr>
              <td class="item-info-label">{itemInfoTarget.kind === 'folder' ? 'Folder Name' : 'Request Name'}</td>
              <td class="item-info-value" title={itemInfoDisplayName(itemInfoTarget)}>
                <span class="item-info-colon">:</span>{itemInfoDisplayName(itemInfoTarget)}
              </td>
            </tr>
            <tr>
              <td class="item-info-label">
                {itemInfoTarget.kind === 'folder' ? 'Folder Name' : 'File Name'}
                <small>(on filesystem)</small>
              </td>
              <td class="item-info-value break-all" title={itemInfoFilesystemName(itemInfoTarget)}>
                <span class="item-info-colon">:</span>{itemInfoFilesystemName(itemInfoTarget)}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
{/if}

{#if deleteRequestTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="delete-request-title">
      <form on:submit|preventDefault={confirmDeleteRequest}>
        <header>
          <h2 id="delete-request-title">Delete Request</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelDeleteRequestModal}>x</button>
        </header>
        <div class="prompt-fields">
          <p>Delete <span class="font-medium">{deleteRequestTarget.request.name}</span>? {deleteRequestTarget.request.transient ? 'This unsaved request will be discarded without a recovery copy.' : 'A temporary recovery copy will be kept so it can be restored.'}</p>
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelDeleteRequestModal}>Cancel</button>
          <button
            class="danger-button"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== ''}
          >{busy === 'delete request' ? 'Deleting...' : 'Delete'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if deleteFolderTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="delete-folder-title">
      <form on:submit|preventDefault={confirmDeleteFolder}>
        <header>
          <h2 id="delete-folder-title">Delete Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelDeleteFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <p>Delete <span class="font-medium">{deleteFolderTarget.folder.name || slashPathBase(deleteFolderTarget.folder.displayPath || deleteFolderTarget.folder.path)}</span>? A temporary recovery copy will be kept so it can be restored.</p>
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelDeleteFolderModal}>Cancel</button>
          <button
            class="danger-button"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== ''}
          >{busy === 'delete folder' ? 'Deleting...' : 'Delete'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if cloneCollectionTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="clone-collection-title" data-testid="clone-collection-modal">
      <form on:submit|preventDefault={confirmCloneCollection}>
        <header>
          <h2 id="clone-collection-title">Clone Collection</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelCloneCollectionModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Name</span>
            <input
              id="collection-name"
              name="collectionName"
              aria-label="Clone collection name"
              data-testid="clone-collection-name"
              value={cloneCollectionNameDraft}
              on:input={(event) => updateCloneCollectionName(event.currentTarget.value)}
            />
          </label>
          <label>
            <span>Location</span>
            <div class="inline-field-action">
              <input
                id="collection-location"
                name="collectionLocation"
                aria-label="Clone collection location"
                data-testid="clone-collection-location"
                readonly
                value={cloneCollectionLocationDraft}
                on:click={browseCloneCollectionLocation}
              />
              <button type="button" data-testid="clone-collection-browse" on:click={browseCloneCollectionLocation}>Browse</button>
            </div>
          </label>
          <label>
            <span>Folder Name</span>
            <div class="inline-field-action">
              {#if cloneCollectionFolderEditing}
                <input
                  id="collection-folder-name"
                  name="collectionFolderName"
                  aria-label="Clone collection folder name"
                  data-testid="clone-collection-folder-name"
                  value={cloneCollectionFolderDraft}
                  on:input={(event) => (cloneCollectionFolderDraft = event.currentTarget.value)}
                />
              {:else}
                <input
                  aria-label="Clone collection folder name"
                  data-testid="clone-collection-folder-name"
                  readonly
                  value={cloneCollectionFolderDraft}
                />
              {/if}
              <button type="button" data-testid="clone-collection-folder-toggle" on:click={() => (cloneCollectionFolderEditing = !cloneCollectionFolderEditing)}>{cloneCollectionFolderEditing ? 'Reset' : 'Edit'}</button>
            </div>
          </label>
          {#if cloneCollectionFolderDraft && !collectionFolderNameIsValid(cloneCollectionFolderDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" data-testid="clone-collection-cancel" on:click={cancelCloneCollectionModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== '' || cloneCollectionNameDraft === '' || !cloneCollectionLocationDraft || !collectionFolderNameIsValid(cloneCollectionFolderDraft)}
          >{busy === 'clone collection' ? 'Creating...' : 'Create'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if renameCollectionTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="rename-collection-title" data-testid="rename-collection-modal">
      <form on:submit|preventDefault={confirmRenameCollection}>
        <header>
          <h2 id="rename-collection-title">Rename Collection</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelRenameCollectionModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Name</span>
            <input
              aria-label="Rename collection name"
              data-testid="rename-collection-name"
              value={renameCollectionDraft}
              on:input={(event) => (renameCollectionDraft = event.currentTarget.value)}
            />
          </label>
        </div>
        <div class="button-row">
          <button type="button" data-testid="rename-collection-cancel" on:click={cancelRenameCollectionModal}>Cancel</button>
          <button class="primary" type="submit" data-testid="rename-collection-confirm" disabled={busy !== '' || renameCollectionDraft === ''}>{busy === 'rename collection' ? 'Renaming...' : 'Rename'}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if removeCollectionTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="remove-collection-title" data-testid="remove-collection-modal">
      <header>
        <h2 id="remove-collection-title">Remove Collection</h2>
        <button type="button" class="icon-button" title="Cancel" on:click={cancelRemoveCollectionModal}>x</button>
      </header>
      <p>Remove {removeCollectionTarget.name} from this workspace. A temporary recovery copy will be kept so it can be restored.</p>
      <code>{removeCollectionTarget.path}</code>
      <div class="button-row">
        <button type="button" data-testid="remove-collection-cancel" on:click={cancelRemoveCollectionModal}>Cancel</button>
        <button class="primary" type="button" data-testid="remove-collection-confirm" on:click={confirmRemoveCollection} disabled={busy !== ''}>{busy === 'remove collection' ? 'Removing...' : 'Remove'}</button>
      </div>
    </div>
  </div>
{/if}

	{#if showGenerateDocsModal && activeCollection}
	  <div class="prompt-backdrop">
	    <div class="prompt-dialog generate-docs-dialog" role="dialog" aria-modal="true" aria-labelledby="generate-docs-title" data-testid="generate-docs-modal">
      <form on:submit|preventDefault={generateCollectionDocs}>
        <header>
          <h2 id="generate-docs-title">Generate Documentation</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelGenerateDocsModal}>x</button>
        </header>
        <div class="generate-docs-content">
          <h3 data-testid="generate-docs-heading">Interactive API Documentation</h3>
          <p>Generate a standalone HTML file that can be hosted anywhere or shared with your team.</p>
          <ul class="generate-docs-features">
            <li>Standalone HTML file - no server required</li>
            <li>Interactive API playground</li>
            <li>Host on any static file server</li>
          </ul>
          <div class="generate-docs-card">
            <div class="version-info" data-testid="version-info">
              <div class="version-line">
                <span class="version-label">Collection Version:</span>
                <span class="version-value" data-testid="version-value">{formattedCollectionVersion(activeCollection.version)}</span>
              </div>
              <p class="version-summary" data-testid="version-summary">{generateDocsFolderCount} {generateDocsFolderCount === 1 ? 'Folder' : 'Folders'} • {generateDocsRequestCount} {generateDocsRequestCount === 1 ? 'request' : 'requests'}</p>
            </div>
            {#if generateDocsEnvironments.length > 0}
              <div class="card-divider"></div>
              <div class="env-section-header">
                <div class="env-section-heading">
                  <h4 class="env-section-title" data-testid="env-section-title">Environments to include</h4>
                  <span class="env-section-count" data-testid="env-selected-count">({generateDocsSelectedCount}/{generateDocsEnvironments.length} selected)</span>
                </div>
                <label class="env-select-all">
                  <input
                    bind:this={generateDocsSelectAllInput}
                    type="checkbox"
                    data-testid="env-select-all"
                    checked={generateDocsSelectedCount === generateDocsEnvironments.length}
                    on:change={(event) => toggleGenerateDocsSelectAll(event.currentTarget.checked)}
                  />
                  <span data-testid="env-select-all-label">Select All</span>
                </label>
              </div>
              <div class="env-list">
                {#each generateDocsEnvironments as env (env.id)}
                  <label class="env-row" data-testid="env-row">
                    <input
                      type="checkbox"
                      data-testid={`env-select-${env.id}`}
                      checked={generateDocsSelectedEnvIds.includes(env.id)}
                      on:change={(event) => toggleGenerateDocsEnvironment(env.id, event.currentTarget.checked)}
                    />
                    <span class="env-color" style={`background: ${env.color || '#64748b'}`}></span>
                    <span>{env.name}</span>
                  </label>
                {/each}
              </div>
            {/if}
          </div>
          <p class="generate-docs-note">The generated file loads OpenCollection's JavaScript and CSS files from a CDN, which requires an internet connection.</p>
        </div>
        <div class="button-row">
          <button type="button" data-testid="generate-docs-cancel" on:click={cancelGenerateDocsModal}>Cancel</button>
          <button class="primary" type="submit" data-testid="generate-docs-generate" disabled={busy !== ''}>Generate</button>
        </div>
      </form>
    </div>
	  </div>
		{/if}

		{#if openAPISpecViewerOpen && openAPISpecViewerResult}
		  <div class="prompt-backdrop">
		    <div class="prompt-dialog openapi-spec-dialog" role="dialog" aria-modal="true" aria-labelledby="openapi-spec-title" data-testid="openapi-spec-viewer-modal">
		      <header>
		        <h2 id="openapi-spec-title">API Spec</h2>
		        <button type="button" class="icon-button" title="Close" on:click={closeOpenAPISyncSpecViewer}>x</button>
		      </header>
		      <div class="openapi-spec-meta">
		        {#if openAPISpecViewerResult.sourceUrl}
		          <span data-testid="openapi-spec-viewer-source">{openAPISpecViewerResult.sourceUrl}</span>
		        {/if}
		        {#if openAPISpecViewerResult.fetched}
		          <small data-testid="openapi-spec-viewer-origin">Showing spec file from source.</small>
		        {:else}
		          <small data-testid="openapi-spec-viewer-origin">Stored spec from last sync.</small>
		        {/if}
		      </div>
		      <pre class="openapi-spec-viewer" aria-label="OpenAPI spec content" data-testid="openapi-spec-viewer-content">{formattedOpenAPISpecContent(openAPISpecViewerResult.content)}</pre>
		      <div class="button-row">
		        <button type="button" data-testid="openapi-spec-viewer-close" on:click={closeOpenAPISyncSpecViewer}>Close</button>
		        <button class="primary" type="button" data-testid="openapi-spec-viewer-copy" on:click={copyOpenAPISyncSpec}>Copy</button>
		      </div>
		    </div>
		  </div>
		{/if}

		{#if openAPISpecDiffOpen && openAPISpecDiffResult}
		  <div class="prompt-backdrop">
		    <div class="prompt-dialog openapi-spec-diff-dialog" role="dialog" aria-modal="true" aria-labelledby="openapi-spec-diff-title" data-testid="openapi-spec-diff-modal">
		      <header>
		        <h2 id="openapi-spec-diff-title">Spec Diff</h2>
		        <button type="button" class="icon-button" title="Close" on:click={closeOpenAPISyncSpecDiff}>x</button>
		      </header>
		      <div class="openapi-spec-meta">
		        {#if openAPISpecDiffResult.sourceUrl}
		          <span data-testid="openapi-spec-diff-source">{openAPISpecDiffResult.sourceUrl}</span>
		        {/if}
		        <small data-testid="openapi-spec-diff-summary">{openAPISyncSpecDiffSummary(openAPISpecDiffResult)}</small>
		        {#if openAPISpecDiffResult.noStoredSpec}
		          <small>No stored spec file found. Comparing against an empty current spec.</small>
		        {/if}
		      </div>
		      <div class="openapi-spec-diff-badges" aria-label="Endpoint change summary">
		        <span class="openapi-spec-diff-badge added">{openAPISpecDiffResult.added ?? 0} New in Spec</span>
		        <span class="openapi-spec-diff-badge changed">{openAPISpecDiffResult.updated ?? 0} Updated in Spec</span>
		        <span class="openapi-spec-diff-badge removed">{openAPISpecDiffResult.removed ?? 0} Removed from Spec</span>
		      </div>
		      <div class="openapi-spec-diff-toolbar" data-testid="openapi-spec-diff-navigation">
		        <button type="button" data-testid="openapi-spec-diff-previous" on:click={() => goOpenAPISpecDiffChange(-1)} disabled={openAPISpecDiffChangeCount === 0 || openAPISpecDiffActiveChangeIndex === 0}>Previous</button>
		        <span data-testid="openapi-spec-diff-change-counter">
		          {openAPISpecDiffChangeCount > 0 ? `${openAPISpecDiffActiveChangeIndex + 1} / ${openAPISpecDiffChangeCount} changes` : '0 changes'}
		        </span>
		        <button type="button" data-testid="openapi-spec-diff-next" on:click={() => goOpenAPISpecDiffChange(1)} disabled={openAPISpecDiffChangeCount === 0 || openAPISpecDiffActiveChangeIndex >= openAPISpecDiffChangeCount - 1}>Next</button>
		      </div>
		      <div class="openapi-spec-diff-grid" data-testid="openapi-spec-diff-content">
		        <div class="openapi-spec-diff-heading">Current Spec</div>
		        <div class="openapi-spec-diff-heading">Updated Spec</div>
		        {#each openAPISpecDiffResult.lines ?? [] as line, lineIndex (lineIndex)}
		          <div class={`openapi-spec-diff-cell ${line.kind}`} class:active-change={openAPISpecDiffLineIsActive(lineIndex, line)} data-testid="openapi-spec-diff-current-line" data-openapi-spec-diff-line-index={lineIndex}>
		            <span class="openapi-spec-diff-line-number">{line.oldNumber || ''}</span>
		            <code>{line.oldText ?? ''}</code>
		          </div>
		          <div class={`openapi-spec-diff-cell ${line.kind}`} class:active-change={openAPISpecDiffLineIsActive(lineIndex, line)} data-testid="openapi-spec-diff-updated-line" data-openapi-spec-diff-line-index={lineIndex}>
		            <span class="openapi-spec-diff-line-number">{line.newNumber || ''}</span>
		            <code>{line.newText ?? ''}</code>
		          </div>
		        {/each}
		      </div>
		      <div class="button-row">
		        <button type="button" data-testid="openapi-spec-diff-close" on:click={closeOpenAPISyncSpecDiff}>Close</button>
		      </div>
		    </div>
		  </div>
		{/if}

		{#if openAPISyncSettingsOpen && activeCollection}
		  <div class="prompt-backdrop">
		    <div class="prompt-dialog openapi-settings-dialog" role="dialog" aria-modal="true" aria-labelledby="openapi-settings-title" data-testid="openapi-sync-settings-modal">
		      <form on:submit|preventDefault={saveOpenAPISyncSettings}>
	        <header>
	          <h2 id="openapi-settings-title">Connection Settings</h2>
	          <button type="button" class="icon-button" title="Cancel" on:click={cancelOpenAPISyncSettings}>x</button>
	        </header>
	        <div class="prompt-fields">
	          <label>
	            <span>Spec Source</span>
	            <input
	              aria-label="OpenAPI sync settings source"
	              data-testid="openapi-sync-settings-source"
	              placeholder="https://api.example.com/openapi.json"
	              bind:value={openAPISyncSettingsSourceURL}
	            />
	          </label>
	          <label class="checkbox-line openapi-settings-toggle">
	            <input type="checkbox" data-testid="openapi-sync-settings-auto-check" bind:checked={openAPISyncSettingsAutoCheck} />
	            <span>Auto-check for updates</span>
	          </label>
	          {#if openAPISyncSettingsAutoCheck}
	            <div class="openapi-settings-intervals">
	              <span>Check interval</span>
	              <div class="segmented compact" data-testid="openapi-sync-settings-intervals">
	                {#each openAPISyncCheckIntervals as minutes (minutes)}
	                  <button
	                    type="button"
	                    class:active={openAPISyncSettingsInterval === minutes}
	                    data-testid={`openapi-sync-settings-interval-${minutes}`}
	                    on:click={() => (openAPISyncSettingsInterval = minutes)}
	                  >
	                    {minutes} min
	                  </button>
	                {/each}
	              </div>
	            </div>
	          {/if}
	        </div>
	        <div class="button-row modal-footer">
	          <button type="button" class="danger-button" data-testid="openapi-sync-settings-disconnect" on:click={disconnectOpenAPISync} disabled={busy !== ''}>Disconnect sync</button>
	          <button type="button" data-testid="openapi-sync-settings-cancel" on:click={cancelOpenAPISyncSettings}>Cancel</button>
	          <button class="primary" type="submit" data-testid="openapi-sync-settings-save" disabled={busy !== ''}>Save</button>
	        </div>
	      </form>
	    </div>
	  </div>
	{/if}

	{#if tabLifecycleDialog}
	  <div class="prompt-backdrop">
	    <div
	      class="prompt-dialog unsaved-tabs-dialog"
	      role="dialog"
	      aria-modal="true"
	      aria-labelledby="unsaved-tabs-title"
	      aria-describedby="unsaved-tabs-description"
	      aria-busy={tabLifecycleDecisionBusy}
	      tabindex="-1"
	      on:keydown={handleTabLifecycleDialogKeydown}
	    >
	      <header>
	        <h2 id="unsaved-tabs-title">Unsaved changes</h2>
	      </header>
	      <p id="unsaved-tabs-description">
	        {#if tabLifecycleDialog.action === 'quit'}
	          Save or discard these drafts before quitting LiteAPI.
	        {:else if tabLifecycleDialog.action === 'close-all'}
	          Save or discard these drafts before closing all tabs.
	        {:else}
	          Save or discard this draft before closing the tab.
	        {/if}
	      </p>
	      <ul class="unsaved-tabs-list" aria-label="Affected unsaved requests">
	        {#each tabLifecycleDialog.affected as request (request.collectionId + request.requestId)}
	          <li>
	            <strong>{request.requestName}</strong>
	            <span>{request.transient ? 'Scratch draft' : 'Unsaved changes'}</span>
	          </li>
	        {/each}
	      </ul>
	      <div class="button-row modal-footer">
	        <button
	          type="button"
	          class="danger-button"
	          on:click={discardAndCompleteTabLifecycle}
	          disabled={tabLifecycleDecisionBusy}
	        >Discard &amp; Close</button>
	        <button
	          type="button"
	          bind:this={tabLifecycleCancelButton}
	          on:click={dismissTabLifecycleDialog}
	          disabled={tabLifecycleDecisionBusy}
	        >Cancel</button>
	        <button
	          type="button"
	          class="primary"
	          on:click={saveAndCompleteTabLifecycle}
	          disabled={tabLifecycleDecisionBusy}
	        >Save &amp; Close</button>
	      </div>
	    </div>
	  </div>
	{/if}

	{#if promptDialog}
	  <div class="prompt-backdrop">
	    <div class="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="prompt-dialog-title">
      <form on:submit|preventDefault={submitPromptDialog}>
        <header>
          <h2 id="prompt-dialog-title">Input Required</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelPromptDialog}>x</button>
        </header>
        <div class="prompt-fields">
          {#each promptDialog.prompts as prompt, index (index)}
            <label>
              <span>{prompt}</span>
              <input value={promptDialog.values[prompt] ?? ''} on:input={(event) => updatePromptValue(prompt, event.currentTarget.value)} />
            </label>
          {/each}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelPromptDialog}>Cancel</button>
          <button class="primary" type="submit">Continue</button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if gitNotFoundMessage}
  <div class="prompt-backdrop">
    <div class="prompt-dialog git-not-found-dialog" role="dialog" aria-modal="true" aria-labelledby="git-not-found-title">
      <header>
        <h2 id="git-not-found-title">Git Required</h2>
        <button type="button" class="icon-button" title="Close" on:click={() => (gitNotFoundMessage = '')}>x</button>
      </header>
      <p>{gitNotFoundMessage}</p>
      <div class="button-row">
        <button class="primary" type="button" on:click={() => (gitNotFoundMessage = '')}>Close</button>
      </div>
    </div>
  </div>
{/if}

{#if generatedGrpcurlCommand}
  <div class="prompt-backdrop">
    <div class="prompt-dialog code-generator-dialog" role="dialog" aria-modal="true" aria-labelledby="grpcurl-code-title">
      <header>
        <h2 id="grpcurl-code-title">Generate grpcurl Command</h2>
        <button type="button" class="icon-button" title="Close" on:click={closeGrpcurlCommand}>x</button>
      </header>
      <pre class="generated-code" aria-label="Generated grpcurl command">{generatedGrpcurlCommand}</pre>
      <div class="button-row">
        <button type="button" on:click={closeGrpcurlCommand}>Close</button>
        <button class="primary" type="button" on:click={copyGrpcurlCommand}>Copy</button>
      </div>
    </div>
  </div>
{/if}

{#if requestCodeTarget}
  <div class="prompt-backdrop">
    <div class="prompt-dialog code-generator-dialog" role="dialog" aria-modal="true" aria-labelledby="request-code-title">
      <header>
        <h2 id="request-code-title">Generate Code</h2>
        <button type="button" class="icon-button" title="Close" on:click={closeRequestCode}>x</button>
      </header>
      <div class="field-grid code-generator-controls">
        <span class="field-label">Language</span>
        <select
          aria-label="Request code language"
          data-testid="request-code-language"
          value={requestCodeLanguage}
          on:change={(event) => changeRequestCodeLanguage(event.currentTarget.value)}
        >
          <option value="curl">cURL</option>
          <option value="fetch">JavaScript fetch</option>
        </select>
      </div>
      <pre class="generated-code" aria-label="Generated request code">{requestGeneratedCode}</pre>
      <div class="button-row">
        <button type="button" on:click={closeRequestCode}>Close</button>
        <button class="primary" type="button" on:click={copyRequestCode} disabled={!requestGeneratedCode}>Copy</button>
      </div>
    </div>
  </div>
{/if}

{#if generatingResponseExampleID && activeRequest}
  {@const generatedExample = (activeRequest.examples ?? []).find((example) => responseExampleIdentifier(example) === generatingResponseExampleID)}
  {#if generatedExample}
    <div class="prompt-backdrop">
      <div class="prompt-dialog code-generator-dialog" role="dialog" aria-modal="true" aria-labelledby="response-example-code-title">
        <header>
          <h2 id="response-example-code-title">Generate Code - {generatedExample.name}</h2>
          <button type="button" class="icon-button" title="Close" on:click={closeResponseExampleCode}>x</button>
        </header>
        <div class="field-grid code-generator-controls">
          <span class="field-label">Language</span>
          <select
            aria-label="Response example code language"
            value={responseExampleCodeLanguage}
            on:change={(event) => changeResponseExampleCodeLanguage(generatedExample, event.currentTarget.value)}
          >
            <option value="curl">cURL</option>
            <option value="fetch">JavaScript fetch</option>
          </select>
        </div>
        <pre class="generated-code" aria-label="Generated response example code">{responseExampleGeneratedCode}</pre>
        <div class="button-row">
          <button type="button" on:click={closeResponseExampleCode}>Close</button>
          <button class="primary" type="button" on:click={copyResponseExampleCode} disabled={!responseExampleGeneratedCode}>Copy</button>
        </div>
      </div>
    </div>
  {/if}
{/if}

{#if busy}
  <div class="busy">{busy}</div>
{/if}

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
  import { filterCommands } from './lib/commandPalette'
  import { memoized, KeyedMemo, type Memo } from './lib/memo'
  import {
    computeWindow,
    sidebarGroupOffset as sidebarGroupOffsetOf,
    sidebarGroupWindow
  } from './lib/virtualList'
  import {
    networkSortAriaValue as devToolsNetworkSortAriaValue,
    networkSortLabel as devToolsNetworkSortLabel,
    nextNetworkSort,
    networkDomain as devToolsNetworkDomain,
    networkLogTimestamp,
    networkPath as devToolsNetworkPath,
    normalizedNetworkMethod,
    sortNetworkRows as sortedDevToolsNetworkRows,
    DEFAULT_NETWORK_COLUMN_WIDTHS,
    normalizedNetworkColumnWidths,
    normalizedNetworkSortDirection as normalizedDevToolsNetworkSortDirectionOf,
    normalizedNetworkSortKey as normalizedDevToolsNetworkSortKeyOf,
    networkSortPreference,
    resizeAdjacentColumns
  } from './lib/networkSort'
  import {
    DEFAULT_RESPONSE_SPLIT,
    DEFAULT_SIDEBAR_WIDTH,
    clampResponseSplit,
    clampSidebarWidth,
    readWorkbenchLayout,
    splitFractionAt,
    writeWorkbenchLayout
  } from './lib/workbench/layout'
  import {
    OPENAPI_SYNC_CHECK_INTERVALS,
    allEndpointDecisions,
    defaultOpenAPISyncDecision,
    formatOpenAPISyncCheckedAt,
    formattedOpenAPISpecContent,
    normalizedOpenAPISyncSettingsInterval,
    openAPILocalDriftLabel,
    openAPILocalDriftIDs as openAPILocalDriftIDsOf,
    openAPISyncAutoCheckEnabled,
    openAPISyncAutoCheckStatusLine as openAPISyncAutoCheckStatusLineOf,
    openAPISyncConfigFor,
    openAPISyncIntervalMinutes,
    openAPISyncSpecDiffSummary,
    reconcileEndpointDecisions
  } from './lib/openApiSync'
  import {
    DEFAULT_CODE_FONT,
    DEFAULT_CODE_FONT_FAMILY,
    DEFAULT_CODE_FONT_SIZE,
    codeFontFamilyFor,
    ZOOM_DEFAULT_PERCENTAGE,
    ZOOM_MAX_PERCENTAGE,
    ZOOM_MIN_PERCENTAGE,
    normalizePresetRequestType,
    normalizedAutoSaveInterval,
    normalizedCodeFont,
    normalizedCodeFontSize,
    normalizedDevToolsDetailsPanelWidth,
    normalizedDevToolsDrawerHeight,
    normalizedRequestTimeout,
    normalizedResponsePaneOrientation,
    normalizedRunnerDelayMs,
    normalizedRunnerIterations,
    normalizedTabID,
    normalizedThemeMode,
    normalizedThemeVariant as normalizedThemeVariantOf,
    normalizedZoomPercentage,
    sizeFromTrailingEdgeDrag
  } from './lib/preferences'
  import {
    collectionFolderNameIsValid,
    filesystemNameIsValid,
    isReservedMetadataName,
    isReservedRootDirectory,
    requestFilesystemBaseName as requestFilesystemBaseNameOf,
    sanitizeCollectionFolderName,
    slashPathBase
  } from './lib/filesystemNames'
  import {
    collectionProxyWithDefaults,
    preferenceProxyModeValue,
    proxyModeLabel,
    proxyModeOverrides,
    proxyPreferencesWithDefaults as proxyPreferencesWithDefaultsOf
  } from './lib/authDefaults'
  import { certificateFileName as customCaFileName } from './lib/filesystemNames'
  import {
    environmentVariableAddLabel,
    environmentVariableMatches,
    visibleEnvironmentVariables
  } from './lib/environmentVariables'
  import { resolveNativeMenuCommand } from './lib/nativeMenu'
  import { historyEntryExists, withOptimisticPatch } from './lib/optimisticPatch'
  import {
    runnerSelectableItems,
    runnerSelectedCount as runnerSelectedCountOf,
    setRunnerItemSelected as setRunnerSelection,
    toggleRunnerSelectAll as toggleRunnerSelectAllOf
  } from './lib/runnerSelection'
  import {
    collectionIsScratch as isScratchCollection,
    findResponseExampleForTab,
    methodLabel,
    responseExampleIdentifier,
    sidebarFolderKey,
    tabLabel as tabLabelFor,
    tabMethod as tabMethodFor
  } from './lib/workbench/tabPresentation'
  import { movedRows, normalizeBulkKeyValueRows, reorderedRows } from './lib/rowEdits'
  import { resolveShortcut, shortcutTabNumber } from './lib/shortcuts'
  import { workspaceStore } from './lib/stores/workspaceStore.svelte'
  import { variableTooltips } from './lib/stores/variableTooltipStore.svelte'
  import {
    currentKeyBindingOS,
    keyBindingCanEdit as canEditKeyBinding,
    keyBindingComboFromEvent,
    keyBindingDefaultsByAction,
    keyBindingDisplayValueFor,
    keyBindingSignature,
    keyBindingValueFor,
    keybindingsAreEnabled as keybindingsEnabledFor,
    formatKeyBinding as formatKeyBindingFor,
    mergeKeyBinding,
    validateKeyBinding as validateKeyBindingRule,
    visibleKeyBindingEntries,
    keyBindingSections,
    keyBindingPresets,
    normalizeKeyBindingPreset,
    effectiveKeyBindings,
    type KeyBindingPresetID
  } from './lib/keybindings'
  import { PatchCoalescer } from './lib/patchQueue'
  // US-036: the lazy wrapper, not CodeEditor itself — importing the real one
  // here is what pulled all of CodeMirror into the initial chunk.
  import CodeEditor from './lib/workbench/LazyCodeEditor.svelte'
  import Modal from './lib/modals/Modal.svelte'
  import SidebarHeader from './lib/SidebarHeader.svelte'
  import SidebarSearch from './lib/SidebarSearch.svelte'
  import RequestSettingsPanel from './lib/workbench/RequestSettingsPanel.svelte'
  import ProtocolRequestLine from './lib/workbench/ProtocolRequestLine.svelte'
  import WorkspaceCommandBar from './lib/workbench/WorkspaceCommandBar.svelte'
  import WorkspaceWindowPicker from './lib/workbench/WorkspaceWindowPicker.svelte'
  import {
    defaultImportDecision,
    hasReplaceImportSelection,
    importSelectionFor as importSelectionOf,
    reconcileImportDecision,
    selectedImportRows,
    toggleImportChildID
  } from './lib/importPlanning'
  import { canPushGitBranch, canStageGitSelection, canSwitchGitBranch, canUnstageGitSelection, reconcileGitBranch, reconcileGitRemoteBranch, reconcileGitRemoteSelection, reconcileGitSelection } from './lib/gitWorkbench'
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
    CodeGenerationTargets,
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
    StartMockServer,
    StopMockServer,
    StartDocsServer,
    StopDocsServer,
    DocsServerStatusFor,
    MockServerStatusFor,
    RefreshMockServer,
    ListHistory,
    ClearHistory,
    CreateRequestFromHistory,
    VisualizerDocument,
    SelectRunnerDataFile,
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
  import type { gitworkbench, history, localserver, main, types } from '../wailsjs/go/models'
  import {
    displayTooltipValue,
    findTooltipVariable,
    folderChainForRequest,
    isValidVariableName,
    pathParamTooltipInfo,
    resolveTooltipValue,
    resolveVariableTooltip,
    type VariableTooltipInfo,
    type VariableTooltipSource
  } from './lib/variableResolution'
  import {
    collectPromptNames,
    collectVariableNames,
    fileBodyRows,
    pathParamNamesFromURL,
    queryParamsForURL,
    scanBodyPrompts,
    scanBodyVariables,
    syncPathParamsForURL,
    variableNamesForRequest
  } from './lib/requestScanning'
  import {
    buildGlobalSearchResults,
    isValidGlobalSearchQuery,
    normalizeGlobalSearchQuery,
    type GlobalSearchResult
  } from './lib/globalSearch'
  import {
    fallbackVariableTooltipInfo,
    urlVariableSegments,
    type URLVariableSegment
  } from './lib/urlSegments'
  import {
    collectionProxyMode,
    formatRuntimeBytes,
    isProxyConfigUnset,
    preferencesProxyMode,
    requestCommandState as buildRequestCommandState,
    requestIsTransient as isRequestTransient
  } from './lib/workbench/commandState'
  import {
    collectionMatches,
    computeGroupedItems,
    filteredFolders,
    filteredItems,
    folderMatches,
    normalizedSearch,
    requestMatches,
    searchHit
  } from './lib/sidebarFilter'
  import {
    cookieFlags,
    cookieGroups,
    cookieHeaderPreview,
    cookieMatches,
    cookieExpiresInput,
    cookieExpiry,
    cookieFormFor,
    emptyCookieForm,
    type CookieForm
  } from './lib/cookieView'
  import {
    devToolsConsoleLogs,
    responseScriptLogs,
    type DevToolsConsoleLog,
    type ScriptLog
  } from './lib/devToolsConsole'
  import {
    notificationDate,
    notificationDescription,
    notificationLevelClass,
    notificationTitle,
    notificationType,
    notificationsForDisplay
  } from './lib/notificationView'
  import { parseDotEnvRows, type DotEnvRow } from './lib/dotEnv'
  import {
    authWithOAuth2Defaults,
    oauth2AuthWithDefaults,
    proxyConfigWithDefaults
  } from './lib/authDefaults'
  import { contentTypeForFilePath, responseExampleBodyTypeForContentType } from './lib/contentTypes'
  import {
    applyResponseExampleFileRow,
    applyResponseExampleHeader,
    applyResponseExampleRequestField,
    applyResponseExampleResponseField,
    prettifyJSON,
    removeResponseExampleFileRow,
    suggestedResponseExampleName as suggestedResponseExampleNameOf
  } from './lib/responseExampleEdits'
  import { BrowserOpenURL, EventsOn, OnFileDrop, OnFileDropOff, Quit } from '../wailsjs/runtime/runtime'

  type View = 'request' | 'collection' | 'git' | 'runner' | 'environments' | 'import' | 'features' | 'network' | 'cookies' | 'history' | 'preferences' | 'devtools'
  type ResponsePaneOrientation = 'horizontal' | 'vertical'
  type DevToolsTab = 'console' | 'network' | 'performance' | 'terminal'
  type DevToolsNetworkSortKey = 'method' | 'status' | 'domain' | 'path' | 'time' | 'duration' | 'size'
  type DevToolsNetworkSortDirection = '' | 'asc' | 'desc'
  type DevToolsNetworkDetailTab = 'request' | 'response' | 'network'
  type RequestPaneTab = 'params' | 'body' | 'headers' | 'auth' | 'vars' | 'script' | 'assert' | 'tests' | 'docs' | 'app' | 'settings'
  type ResponseTab = 'response' | 'headers' | 'metadata' | 'trailers' | 'timeline' | 'console' | 'tests' | 'visualizer' | 'examples'
  type CollectionTab = 'overview' | 'folders' | 'headers' | 'vars' | 'auth' | 'presets' | 'mock' | 'docs' | 'proxy' | 'clientCert' | 'protobuf' | 'script' | 'tests'
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
    | { kind: 'folder'; collection: types.Collection; folder: types.FolderConfig }
    | { kind: 'request'; collection: types.Collection; request: types.RequestItem }
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
  // KeyBindingDefinition / KeyBindingSection now live in lib/keybindings.ts
  // alongside the default table, so the table can be tested against the same
  // collision rule the UI enforces.
  type KeyBindingDefinition = import('./lib/keybindings').KeyBindingDefinition
  type KeyBindingSection = import('./lib/keybindings').KeyBindingSection
  type BodyTextField = 'json' | 'xml' | 'text' | 'graphqlQuery' | 'graphqlVariables'
  type IndexedVariable = {
    variable: types.Variable
    index: number
  }
  type GitCloneProgress = {
    stage?: string
    message?: string
    targetPath?: string
    at?: string
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

  // US-026. The state now lives in workspaceStore so components can read it
  // without being handed pieces of it as props. This alias keeps every read in
  // this file unchanged; assignments go to the store, and because a $derived
  // cannot be assigned to, the compiler finds any that were missed.
  const appState = $derived(workspaceStore.appState)
  let activeView = $state<View>('request')
  let requestPaneTab = $state<RequestPaneTab>('params')
  let responseTab = $state<ResponseTab>('response')
  let compactWorkbench = $state(false)
  let compactWorkbenchMedia: MediaQueryList | undefined
  let removeCompactWorkbenchListener: (() => void) | undefined
  let removeFlushOnBlurListeners: (() => void) | undefined
  // US-021/US-022. Live WebSocket and gRPC events are pushed one at a time
  // rather than re-sent as a whole re-marshalled log on every call, so the
  // accumulated log lives here. Keyed by collection+request because several
  // requests can hold live sessions at once.
  let liveSessionLogs = $state<Record<string, LiveSessionLog>>({})
  let stopWebSocketEvents: (() => void) | undefined
  let stopGrpcEvents: (() => void) | undefined
  let collectionTab = $state<CollectionTab>('overview')
  let responseView = $state<'pretty' | 'raw' | 'base64' | 'hex'>('pretty')
  let tabLifecycleDialog = $state<TabLifecycleDialog | null>(null)
  let tabLifecycleDecisionBusy = $state(false)
  let tabLifecycleCancelButton = $state<HTMLButtonElement | null>(null)
  let recoveryEntries = $state<main.RecoveryEntry[]>([])
  let recoveryBusyEntryID = $state('')
  let workspaceWindowTargets = $state<main.WorkspaceWindowTarget[]>([])
  let workspaceWindowPickerOpen = $state(false)
  let workspaceWindowPickerBusy = $state(false)
  let workspaceWindowPickerBusyAction = $state<'loading' | 'opening' | 'creating' | ''>('')
  let workspaceWindowPickerError = $state('')
  let nativeWindowBusy = $state(false)
  let nativeWindowError = $state('')
  let hydratedActiveTabID = $state('')
  const selectedEnvironmentId = $derived(workspaceStore.selectedEnvironmentId)
  let loading = $state(true)
  let loadingStatus = $state('Opening workspace')
  let busy = $state('')
  let activeActions = $state(new Map<number, string>())
  let nextActionID = $state(0)
  let error = $state('')
  let activeHTTPTransport: { collectionId: string; requestId: string } | undefined
  let httpCancellationRequested = $state(false)
  let activeCollectionRun = $state<{ collectionId: string; collectionName: string } | undefined>()
  let collectionRunCancellationRequested = $state(false)
  let lastCollectionRunCancelled = $state(false)
  let collectionName = $state('New Collection')
  let requestName = $state('New Request')
  let requestType = $state('http')
  let lastPresetKey = $state('')
  let environmentName = $state('Staging')
  let globalEnvironmentName = $state('Global')
  let globalEnvironmentPayload = $state('')
  let globalEnvironmentCopyName = $state('')
  let globalEnvironmentExportFormat = $state('single-object')
  let globalEnvironmentExportAll = $state(false)
  let globalEnvironmentExportFilename = $state('')
  let globalEnvironmentExportPath = $state('')
  let globalEnvironmentVariableTab = $state<EnvironmentVariableTab>('variables')
  let environmentVariableTab = $state<EnvironmentVariableTab>('variables')
  let globalEnvironmentVariableSearch = $state('')
  let environmentVariableSearch = $state('')
  let dotEnvFiles = $state<types.DotEnvFile[]>([])
  let selectedDotEnvKey = $state('')
  let dotEnvScope = $state('workspace')
  let dotEnvName = $state('.env')
  let dotEnvContent = $state('')
  let dotEnvDirty = $state(false)
  let dotEnvContextKey = $state('')
  let dotEnvRefreshTimer: ReturnType<typeof window.setInterval> | undefined
  let collectionWatchPollTimer: ReturnType<typeof window.setInterval> | undefined
  let collectionWatchRefreshInFlight = $state(false)
  let dotEnvEditorMode = $state<DotEnvEditorMode>('table')
  let systemThemeMode = $state<'light' | 'dark'>('light')
  let systemThemeQuery: MediaQueryList | undefined
  let removeSystemThemeListener: (() => void) | undefined
  let exportText = $state('')
	  let importSourceMode = $state<ImportSourceMode>('files')
	  let importSources = $state<main.CollectionImportSource[]>([])
	  let importPreview = $state<main.CollectionImportPreview | undefined>()
	  let importDecisions = $state<Record<string, ImportDecision>>({})
	  let importExpanded = $state<Record<string, boolean>>({})
	  let importApplyResult = $state<main.CollectionImportApplyResult | undefined>()
	  // US-044. Off by default: pm.* runs natively now, so an imported Postman
  // script works as written. Translation is for collections whose scripts were
  // already migrated by hand against the bru API.
  let importTranslatePostmanScripts = $state(false)
  let importDestinationRoot = $state('')
	  let importDestinationWorkspaceID = $state('')
	  let importURL = $state('')
	  let importPasteName = $state('Pasted import')
	  let importStatus = $state('')
	  let importPickerButton = $state<HTMLButtonElement | null>(null)
	  let importApplyButton = $state<HTMLButtonElement | null>(null)
	  let importReplaceConfirmationOpen = $state(false)
	  let importReplaceConfirmationCancelButton = $state<HTMLButtonElement | null>(null)
	  let importReplaceConfirmationReturnFocus = $state<HTMLElement | null>(null)
	  let importApplyInFlight = $state(false)
  let openAPISyncCollectionId = $state('')
  let openAPISyncSourceURL = $state('')
  let openAPISyncGroupBy = $state('tag')
  let openAPISyncContent = $state('')
  let openAPISyncPreserveValues = $state(true)
	  let openAPISyncEndpointDecisions = $state<Record<string, string>>({})
	  let openAPISyncResult = $state<types.OpenAPISyncResult | undefined>()
	  let openAPILocalDriftResult = $state<types.OpenAPILocalDriftResult | undefined>()
	  let openAPISyncSettingsOpen = $state(false)
	  let openAPISyncSettingsSourceURL = $state('')
	  let openAPISyncSettingsAutoCheck = $state(true)
	  let openAPISyncSettingsInterval = $state(5)
	  let openAPISyncAutoCheckLastRun = $state<Record<string, number>>({})
		  let openAPISyncUpdateStatus = $state<Record<string, types.OpenAPISyncUpdateCheckResult>>({})
		  let openAPISyncUpdateErrors = $state<Record<string, string>>({})
		  let openAPISyncPollTimer: ReturnType<typeof window.setInterval> | undefined
			  let openAPISyncInitialPollTimer: ReturnType<typeof window.setTimeout> | undefined
			  let openAPISpecViewerOpen = $state(false)
			  let openAPISpecViewerResult = $state<types.OpenAPISyncSpecViewResult | undefined>()
			  let openAPISpecDiffOpen = $state(false)
			  let openAPISpecDiffResult = $state<types.OpenAPISyncSpecDiffResult | undefined>()
			  let openAPISpecDiffActiveChangeIndex = $state(0)
			  let requestSearch = $state('')
  let requestSearchInput = $state<HTMLInputElement | undefined>()
  let requestURLInput = $state<HTMLInputElement | undefined>()
  let sidebarCollapsed = $state(false)
  let sidebarWidth = $state(DEFAULT_SIDEBAR_WIDTH)
  let responseSplit = $state(DEFAULT_RESPONSE_SPLIT)
	let workbenchStorageScope = $state('')
  let creationOpen = $state(false)
  let creationReturnFocus = $state<HTMLElement | null>(null)
  let commandPaletteOpen = $state(false)
  let commandPaletteQuery = $state('')
  let commandPaletteInput = $state<HTMLInputElement | null>(null)
  let commandPaletteReturnFocus = $state<HTMLElement | null>(null)
  let commandPaletteActiveIndex = $state(0)
  let runnerConfigCollectionId = $state('')
  let runnerSelectedItemIds = $state<string[]>([])
  let runnerDelayMs = $state(0)
  let runnerBailOnFailure = $state(false)
  let runnerIterations = $state(1)
  let runnerDataFile = $state('')
  let fileCacheSize = $state<number | undefined>()
  let autoSaveTimer: ReturnType<typeof window.setTimeout> | undefined
  let autoSaveTarget: { collectionId: string; requestId: string } | undefined
  let requestPreferencesSaveQueue = $state(Promise.resolve())
  let recordingKeybindingAction = $state('')
  let keybindingDraft = $state('')
  let keybindingError = $state('')
  let cookieSearch = $state('')
  let cookieForm = $state<CookieForm>(emptyCookieForm())
  let rawCookieHeader = $state('session=abc123; Path=/; HttpOnly')
  let rawCookieURL = $state('http://127.0.0.1/')
  const selectedCollectionId = $derived(workspaceStore.selectedCollectionId)
  let selectedFolderPath = $state('')
  let folderSettingsTab = $state<FolderSettingsTab>('headers')
  let folderSettingDrafts = $state<Record<string, types.FolderConfig>>({})
  let folderSettingsSaveQueue = $state(Promise.resolve())
  let gitRemoteURL = $state('')
  let gitRemoteCollectionId = $state('')
  let gitCloneURL = $state('')
  let gitCloneRoot = $state('')
  let gitCloneName = $state('')
  let gitVersionText = $state('')
  let gitCloneOutput = $state('')
  let gitCloneInProgress = $state(false)
  let showShareCollectionModal = $state(false)
  let shareCollectionFormat = $state('zip')
  let shareCollectionResult: types.CollectionExportResult | undefined
  let showGenerateDocsModal = $state(false)
  let renameCollectionTarget = $state<types.Collection | undefined>()
  let renameCollectionDraft = $state('')
  let cloneCollectionTarget = $state<types.Collection | undefined>()
  let cloneCollectionNameDraft = $state('')
  let cloneCollectionFolderDraft = $state('')
  let cloneCollectionLocationDraft = $state('')
  let cloneCollectionFolderEditing = $state(false)
  let newFolderTarget = $state<types.Collection | undefined>()
  let newFolderParentPath = $state('')
  let newFolderNameDraft = $state('')
  let newFolderDirectoryDraft = $state('')
  let newFolderDirectoryEditing = $state(false)
  let newFolderShowFilesystemName = $state(false)
  let renameFolderTarget = $state<{ collection: types.Collection; folder: types.FolderConfig } | undefined>()
  let renameFolderNameDraft = $state('')
  let renameFolderDirectoryDraft = $state('')
  let renameFolderDirectoryEditing = $state(false)
  let renameFolderShowFilesystemName = $state(false)
  let cloneFolderTarget = $state<{ collection: types.Collection; folder: types.FolderConfig } | undefined>()
  let cloneFolderNameDraft = $state('')
  let cloneFolderDirectoryDraft = $state('')
  let cloneFolderDirectoryEditing = $state(false)
  let cloneFolderShowFilesystemName = $state(false)
  let itemInfoTarget = $state<CollectionItemInfoTarget | undefined>()
  let cloneRequestTarget = $state<{ collection: types.Collection; request: types.RequestItem } | undefined>()
  let cloneRequestNameDraft = $state('')
  let cloneRequestFilenameDraft = $state('')
  let cloneRequestFilenameEditing = $state(false)
  let cloneRequestShowFilesystemName = $state(false)
  let renameRequestTarget = $state<{ collection: types.Collection; request: types.RequestItem } | undefined>()
  let renameRequestNameDraft = $state('')
  let renameRequestFilenameDraft = $state('')
  let renameRequestFilenameEditing = $state(false)
  let renameRequestShowFilesystemName = $state(false)
  let deleteRequestTarget = $state<{ collection: types.Collection; request: types.RequestItem } | undefined>()
  let deleteFolderTarget = $state<{ collection: types.Collection; folder: types.FolderConfig } | undefined>()
  let removeCollectionTarget = $state<types.Collection | undefined>()
  let generateDocsDeselectedEnvIds = $state<string[]>([])
  let generateDocsResult: types.GenerateCollectionDocsResult | undefined
  let generateDocsSelectAllInput = $state<HTMLInputElement | undefined>()
  let gitCloneProgress = $state<GitCloneProgress[]>([])
  let gitNotFoundMessage = $state('')
	let gitWorkbenchSnapshot = $state<gitworkbench.CollectionGitSnapshot | undefined>()
	let gitWorkbenchCollectionID = $state('')
	let gitWorkbenchLoading = $state(false)
	let gitWorkbenchBusy = $state('')
	let gitWorkbenchStatus = $state('')
	let gitWorkbenchError = $state('')
	let gitWorkbenchSelectedPaths = $state<string[]>([])
	let gitWorkbenchDiff = $state<gitworkbench.CollectionGitDiff | undefined>()
	let gitWorkbenchBranch = $state('')
	let gitWorkbenchNewBranch = $state('')
	let gitWorkbenchCheckoutNewBranch = $state(true)
	let gitWorkbenchCommitMessage = $state('')
	let gitWorkbenchRemoteName = $state('origin')
	let gitWorkbenchRemoteURL = $state('')
	let gitWorkbenchRemoteBranch = $state('')
	let gitWorkbenchSetUpstream = $state(false)
	let gitWorkbenchHeading = $state<HTMLHeadingElement | null>(null)
  let gitCandidates = $state<types.GitCollectionCandidate[]>([])
  let selectedGitCollectionPaths = $state<string[]>([])
  let openCollectionPath = $state('/Users/mou/Documents/LiteAPI/Sample API Collection')
  let grpcMethods = $state<types.GRPCMethodInfo[]>([])
  let grpcMethodsRequestId = $state('')
  let grpcMethodMessage = $state('')

  let copiedVariableTooltipTimers = $state<Record<string, ReturnType<typeof window.setTimeout>>>({})

  let urlInputScrollLeft = $state(0)
  let bodyTextScrollLeft = $state(0)
  let bodyTextScrollTop = $state(0)
  let processEnvTooltipKey = $state('')
	  let processEnvTooltipValues = $state<Record<string, string>>({})
  // US-034. processEnvTooltipValues is rebuilt as an object, so its identity
  // changes even when the contents do not. A signature over the contents is
  // what keeps the tooltip memo from missing on every render while still
  // invalidating when a value genuinely changes.
  const processEnvTooltipSignature = $derived(Object.keys(processEnvTooltipValues)
    .sort()
    .map((name) => `${name}=${processEnvTooltipValues[name]}`)
    .join('|'))
	  let promptDialog = $state<PromptDialogState | null>(null)
	  let oauth2AuthorizationRequest = $state<OAuth2AuthorizationBrowserRequest | null>(null)
	  let oauth2CallbackURLInput = $state('')
	  let oauth2CallbackMessage = $state('')
	  let oauth2FrameKey = $state(0)
	  let creatingResponseExample = $state(false)
  let createResponseExampleName = $state('')
  let createResponseExampleDescription = $state('')
  let createResponseExampleInput = $state<HTMLInputElement | null>(null)
  let editingResponseExampleID = $state('')
  let responseExampleNameDraft = $state('')
  let deletingResponseExampleID = $state('')
  let editingResponseExampleDetailsID = $state('')
  let generatingResponseExampleID = $state('')
  let responseExampleCodeLanguage = $state('curl')
  let responseExampleGeneratedCode = $state('')
  let requestCodeTarget = $state<RequestCodeTarget | undefined>()
  let requestCodeLanguage = $state('curl')
  let requestGeneratedCode = $state('')
  let generatedGrpcurlCommand = $state('')
  let responseExampleDrafts = $state<Record<string, types.ResponseExample>>({})
  let globalSearchOpen = $state(false)
  let globalSearchQuery = $state('')
  let globalSearchIndex = $state(0)
  let globalSearchInput = $state<HTMLInputElement | null>(null)
  let notificationsOpen = $state(false)
  let notificationTab = $state<NotificationTab>('all')
  let selectedNotificationID = $state('')
  let pinnedUnreadNotificationIDs = $state<Set<string> | null>(null)
  let devToolsOpen = $state(false)
  let devToolsTab = $state<DevToolsTab>('console')
  let devToolsSnapshot = $state<types.DevToolsSnapshot | null>(null)
  let devToolsPerformanceView = $state('cumulative')
  let devToolsDrawerHeight = $state(320)
  let devToolsDetailsPanelWidth = $state(400)
  let devToolsNetworkFilters = $state<Record<string, boolean>>({
    GET: true,
    POST: true,
    PUT: true,
    DELETE: true,
    PATCH: true,
    HEAD: true,
    OPTIONS: true
  })
  let devToolsNetworkSortKey = $state<DevToolsNetworkSortKey | ''>('')
  let devToolsNetworkSortDirection = $state<DevToolsNetworkSortDirection>('')
  let selectedDevToolsNetworkLogID = $state('')
  let devToolsNetworkDetailTab = $state<DevToolsNetworkDetailTab>('request')
  let devToolsNetworkColumnWidths = $state([80, 70, 180, 300, 110, 100, 80])
  let devToolsNetworkResizingColumn = $state(-1)
  let devToolsNetworkPreferencesKey = $state('')
  let terminalSessions = $state<types.TerminalSession[]>([])
  let terminalActiveSessionId = $state('')
  let terminalOutput = $state('')
  let terminalInput = $state('')
  let terminalError = $state('')
  let terminalBusy = $state(false)
  let terminalPollTimer: number | undefined
  let revealCollectionMessage = $state('')
	  let importContent = $state('')

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
    { id: 'visualizer', label: 'Visualizer' },
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
    { id: 'mock', label: 'Mock Server' },
    { id: 'docs', label: 'Docs' },
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
  function cloneCollectionDefaultLocation() {
    const preferred = appState?.preferences?.general?.defaultLocation || appState?.preferences?.defaultCollectionPath || ''
    return preferred || activeWorkspace?.path || ''
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
	  const zoomDefaultPercentage = ZOOM_DEFAULT_PERCENTAGE
	  const openAPISyncCheckIntervals = [...OPENAPI_SYNC_CHECK_INTERVALS]
	  const collectionWatchPollMs = 2_000
	  const openAPISyncInitialPollMs = 10_000
	  const openAPISyncGlobalPollMs = 5 * 60_000
	  const zoomMinPercentage = ZOOM_MIN_PERCENTAGE
  const zoomMaxPercentage = ZOOM_MAX_PERCENTAGE
  const zoomStepPercentage = 10
  const defaultCodeFont = DEFAULT_CODE_FONT
  const defaultCodeFontSize = DEFAULT_CODE_FONT_SIZE
  const defaultCodeFontFamily = DEFAULT_CODE_FONT_FAMILY
  const zoomPercentages = Array.from(
    { length: (zoomMaxPercentage - zoomMinPercentage) / zoomStepPercentage + 1 },
    (_, index) => zoomMinPercentage + index * zoomStepPercentage
  )
	  const promptTokenPattern = /\{\{\?([^{}\s](?:[^{}]*?[^{}\s])?)\}\}/g
	  const invalidVariableWarning = 'Invalid variable name! Variables must only contain alpha-numeric characters, "-", "_", "."'

  const activeWorkspace = $derived(workspaceStore.activeWorkspace)
  const activeTab = $derived(workspaceStore.activeTab)
  $effect(() => {
  if (activeTab?.id && activeTab.id !== hydratedActiveTabID) {
      hydratedActiveTabID = activeTab.id
      if (requestTabs.some((tab) => tab.id === activeTab.requestPaneTab)) requestPaneTab = activeTab.requestPaneTab as RequestPaneTab
      if (activeTab.kind === 'response-example') {
        responseTab = 'examples'
      } else if (responseTabs.some((tab) => tab.id === activeTab.responseTab)) {
        responseTab = activeTab.responseTab as ResponseTab
      }
    }
  })
  const selectedCollection = $derived(workspaceStore.selectedCollection)
  const activeCollection = $derived(workspaceStore.activeCollection)
	$effect(() => {
  if (activeView === 'git' && activeCollection?.id && gitWorkbenchCollectionID !== activeCollection.id && !gitWorkbenchLoading) {
  		gitWorkbenchSnapshot = undefined
  		gitWorkbenchSelectedPaths = []
  		gitWorkbenchDiff = undefined
  		gitWorkbenchRemoteURL = ''
  		void refreshGitWorkbench()
  	}
	})
  const activeRequest = $derived(workspaceStore.activeRequest)
  const shareCollectionUnsupportedTypes = $derived(collectionShareUnsupportedTypes(activeCollection))
  $effect(() => {
  if ((activeCollection?.id ?? '') !== openAPISyncCollectionId) {
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
  })
  const openAPISpecDiffChangeLineIndexes = $derived((openAPISpecDiffResult?.lines ?? []).reduce<number[]>((indexes, line, index) => {
    if ((line.kind ?? 'same') !== 'same') indexes.push(index)
    return indexes
  }, []))
  const openAPISpecDiffChangeCount = $derived(openAPISpecDiffChangeLineIndexes.length)
  $effect(() => {
  if (openAPISpecDiffChangeCount === 0 && openAPISpecDiffActiveChangeIndex !== 0) {
      openAPISpecDiffActiveChangeIndex = 0
    } else if (openAPISpecDiffChangeCount > 0 && openAPISpecDiffActiveChangeIndex >= openAPISpecDiffChangeCount) {
      openAPISpecDiffActiveChangeIndex = openAPISpecDiffChangeCount - 1
    }
  })
  const generateDocsEnvironments = $derived(activeCollection?.environments ?? [])
  // THE self-referential statement the story flags as non-mechanical. It reads
  // and writes the same variable, so it cannot be a $derived — a derivation
  // cannot depend on itself — and the variable is genuinely mutated elsewhere
  // (the Generate Docs dialog toggles it), so it stays $state with a pruning
  // effect.
  //
  // The length guard is essential. filter returns a NEW array every pass, so an
  // unguarded assignment would invalidate this effect on its own write and loop
  // forever. Legacy `$:` got away with it because Svelte compared the reference
  // and stopped; an effect writing $state has no such stop.
  $effect(() => {
    const pruned = generateDocsDeselectedEnvIds.filter((id) =>
      generateDocsEnvironments.some((env) => env.id === id)
    )
    if (pruned.length !== generateDocsDeselectedEnvIds.length) {
      generateDocsDeselectedEnvIds = pruned
    }
  })
  const generateDocsSelectedEnvIds = $derived(generateDocsEnvironments.filter((env) => !generateDocsDeselectedEnvIds.includes(env.id)).map((env) => env.id))
  const generateDocsSelectedCount = $derived(generateDocsSelectedEnvIds.length)
  const generateDocsFolderCount = $derived(activeCollection?.folders?.length ?? 0)
  const generateDocsRequestCount = $derived((activeCollection?.items ?? []).filter(collectionDocsRequestIsExportable).length)
  $effect(() => {
  if (generateDocsSelectAllInput) generateDocsSelectAllInput.indeterminate = generateDocsSelectedCount > 0 && generateDocsSelectedCount < generateDocsEnvironments.length
  })
  const runnerConfigItems = $derived(runnerSelectableItems(activeCollection))
  const runnerSelectedCount = $derived(runnerSelectedCountOf(runnerSelectedItemIds, runnerConfigItems))
  $effect(() => {
  if ((activeCollection?.id ?? '') !== runnerConfigCollectionId) {
      runnerConfigCollectionId = activeCollection?.id ?? ''
      runnerSelectedItemIds = runnerConfigItems.map((item) => item.id)
      runnerDelayMs = 0
      runnerBailOnFailure = false
      runnerIterations = 1
      runnerDataFile = ''
    }
  })
  const responsePaneOrientation = $derived(normalizedResponsePaneOrientation(appState?.preferences?.layout?.responsePaneOrientation))
  const appZoomPercentage = $derived(normalizedZoomPercentage(appState?.preferences?.display?.zoomPercentage))
  const codeFont = $derived(normalizedCodeFont(appState?.preferences?.font?.codeFont))
  const codeFontSize = $derived(normalizedCodeFontSize(appState?.preferences?.font?.codeFontSize ?? appState?.preferences?.codeFontSize))
  const activeResponseTabs = $derived(
    (activeRequest?.type === 'grpc'
      ? responseTabs
      : responseTabs.filter((tab) => tab.id !== 'metadata' && tab.id !== 'trailers')
    )
      // US-058. Shown only when a script set a visualizer. A tab that is always
      // there and always empty teaches people to ignore it.
      .filter((tab) => tab.id !== 'visualizer' || Boolean(activeRequest?.response?.visualizer))
  )

  // Fetched from the backend rather than assembled here so the CSP and the
  // escaping stay under Go's tests; the frontend supplies only the sandbox.
  // Mirrors VisualizerSandbox in visualizer.go, which a Go test pins. Declared
  // once here so a change has a single place to happen.
  const visualizerSandboxAttribute = 'allow-scripts'
  let visualizerDocument = $state('')

  // US-073 — mock server controls. Port 0 means "let the OS choose", which is
  // the default because a fixed port collides with whatever else is running and
  // fails at bind time with an error the user then has to diagnose.
  let mockServerStatus = $state<localserver.MockServerStatus | undefined>()
  let mockServerPort = $state(0)

  // US-074 — docs preview. Same shape and the same reasoning as the mock
  // controls: loopback only, port 0 lets the OS choose.
  let docsServerStatus = $state<localserver.DocsServerStatus | undefined>()
  let docsServerPort = $state(0)

  async function refreshDocsServerStatus(collectionID: string | undefined) {
    if (!collectionID) {
      docsServerStatus = undefined
      return
    }
    try {
      docsServerStatus = await DocsServerStatusFor(collectionID)
    } catch (err) {
      error = String(err)
    }
  }

  async function startDocsServer(collectionID: string) {
    await runAction('start docs preview', async () => {
      docsServerStatus = await StartDocsServer(
        collectionID,
        Math.max(0, Math.floor(Number(docsServerPort) || 0)),
        { environmentIds: [] } as types.GenerateCollectionDocsOptions
      )
    })
  }

  async function stopDocsServer(collectionID: string) {
    await runAction('stop docs preview', async () => {
      docsServerStatus = await StopDocsServer(collectionID)
    })
  }

  $effect(() => {
  void refreshDocsServerStatus(collectionTab === 'docs' ? activeCollection?.id : undefined)
  })

  async function refreshMockServerStatus(collectionID: string | undefined) {
    if (!collectionID) {
      mockServerStatus = undefined
      return
    }
    try {
      mockServerStatus = await MockServerStatusFor(collectionID)
    } catch (err) {
      error = String(err)
    }
  }

  async function startMockServer(collectionID: string) {
    await runAction('start mock server', async () => {
      mockServerStatus = await StartMockServer(collectionID, Math.max(0, Math.floor(Number(mockServerPort) || 0)))
    })
  }

  async function stopMockServer(collectionID: string) {
    await runAction('stop mock server', async () => {
      mockServerStatus = await StopMockServer(collectionID)
    })
  }

  // Applies collection edits to a running mock without rebinding, so the port
  // every open client is pointed at survives saving an example.
  async function refreshMockServerRoutes(collectionID: string) {
    await runAction('refresh mock server', async () => {
      mockServerStatus = await RefreshMockServer(collectionID)
    })
  }

  $effect(() => {
  void refreshMockServerStatus(collectionTab === 'mock' ? activeCollection?.id : undefined)
  })

  // US-032. The network log is virtualised: only the rows near the viewport are
  // put in the DOM. Spacer rows above and below carry the height of everything
  // skipped, so the scrollbar stays exactly as long as the full list — see
  // lib/virtualList.ts for why that invariant is the load-bearing one.
  //
  // The row height is measured from the DOM rather than hard-coded, because it
  // follows the app's font-size and density settings. Until it is measured the
  // fallback keeps the window sane rather than dividing by zero.
  const devToolsNetworkRowFallbackHeight = 28
  let devToolsNetworkScrollTop = 0
  let devToolsNetworkViewportHeight = 0
  let devToolsNetworkMeasuredRowHeight = 0

  const devToolsNetworkRowHeight = $derived(devToolsNetworkMeasuredRowHeight || devToolsNetworkRowFallbackHeight)

  function measureDevToolsNetworkViewport(node: HTMLElement) {
    const update = () => {
      devToolsNetworkViewportHeight = node.clientHeight
      // Measured from a real rendered row so density and font-size changes are
      // picked up; a hard-coded height would drift from the stylesheet.
      const row = node.querySelector<HTMLElement>('tbody tr[data-network-row]')
      if (row && row.offsetHeight > 0) devToolsNetworkMeasuredRowHeight = row.offsetHeight
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(node)
    return {
      destroy() {
        observer.disconnect()
      }
    }
  }

  // US-049 — history surface appState. The filter is applied SERVER-SIDE through
  // HistoryQuery rather than by pulling the whole log and filtering here: each
  // entry carries its headers, so a client-side filter would move hundreds of
  // kilobytes across the binding on every keystroke to render a screenful.
  let historyEntries = $state<history.HistoryEntry[]>([])
  let historyQuery = $state('')
  let historyOnlyFailures = $state(false)
  let historyMethodFilter = $state('')
  let historySaveTargetID = $state('')
  let historySearchTimer: number | undefined

  async function refreshHistory() {
    try {
      historyEntries = await ListHistory({
        text: historyQuery,
        method: historyMethodFilter,
        onlyFailures: historyOnlyFailures
      } as history.HistoryQuery)
    } catch (err) {
      error = String(err)
    }
  }

  // Debounced so typing does not issue one binding call per character.
  function scheduleHistorySearch() {
    if (historySearchTimer !== undefined) window.clearTimeout(historySearchTimer)
    historySearchTimer = window.setTimeout(() => {
      historySearchTimer = undefined
      void refreshHistory()
    }, 150)
  }

  // The original request may have been renamed, moved or deleted since the
  // send, so this is checked against live appState rather than assumed.
  function historyEntryStillExists(entry: history.HistoryEntry) {
    return historyEntryExists(appState, entry.collectionId, entry.itemId)
  }

  async function openHistoryEntryInTab(entry: history.HistoryEntry) {
    // Narrowed locally: historyEntryStillExists already proves both are set,
    // but that guarantee does not cross the function boundary for the checker.
    const collectionId = entry.collectionId
    const itemId = entry.itemId
    if (!collectionId || !itemId || !historyEntryStillExists(entry)) return
    await runAction('open history request', async () => {
      workspaceStore.appState = await OpenRequestTab(collectionId, itemId)
      activeView = 'request'
    })
  }

  async function saveHistoryEntryToCollection(entry: history.HistoryEntry) {
    if (!historySaveTargetID) return
    await runAction('save history request', async () => {
      workspaceStore.appState = await CreateRequestFromHistory(historySaveTargetID, entry.id)
    })
  }

  async function clearHistory() {
    await runAction('clear history', async () => {
      await ClearHistory()
      await refreshHistory()
    })
  }

  const historyCollections = $derived((appState?.workspaces ?? []).flatMap((workspace) =>
    (workspace.collections ?? []).map((collection) => ({ id: collection.id, name: collection.name }))
  ))
  $effect(() => {
  if (!historySaveTargetID && historyCollections.length > 0) historySaveTargetID = historyCollections[0].id
  })
  $effect(() => {
  void loadVisualizerDocument(activeCollection?.id, activeRequest?.id, activeRequest?.response?.visualizer)
  })

  async function loadVisualizerDocument(collectionID: string | undefined, itemID: string | undefined, payload: types.VisualizerPayload | undefined) {
    if (!collectionID || !itemID || !payload) {
      visualizerDocument = ''
      return
    }
    try {
      visualizerDocument = await VisualizerDocument(collectionID, itemID)
    } catch {
      visualizerDocument = ''
    }
  }
  $effect(() => {
  if (activeRequest?.type !== 'grpc' && (responseTab === 'metadata' || responseTab === 'trailers')) responseTab = 'headers'
  })
  $effect(() => {
  if (activeCollection && selectedFolderPath && !(activeCollection.folders ?? []).some((folder) => folder.path === selectedFolderPath)) {
      selectedFolderPath = ''
    }
  })
  const activeFolder = $derived(activeCollection?.folders?.find((folder) => folder.path === selectedFolderPath) ?? activeCollection?.folders?.[0])
  const editableFolder = $derived(activeFolder ? folderSettingDrafts[activeFolder.path] ?? activeFolder : undefined)
  const selectedEnvironment = $derived(workspaceStore.selectedEnvironment)
  const activeGlobalEnvironment = $derived(workspaceStore.activeGlobalEnvironment)
  const selectedGlobalEnvironment = $derived(activeGlobalEnvironment ?? activeWorkspace?.globalEnvironments?.[0])
  const selectedDotEnvFile = $derived(dotEnvFiles.find((file) => dotEnvFileKey(file) === selectedDotEnvKey))
  const globalEnvironmentVariableQuery = $derived(normalizedSearch(globalEnvironmentVariableSearch))
  const environmentVariableQuery = $derived(normalizedSearch(environmentVariableSearch))
  const visibleGlobalEnvironmentRows = $derived(visibleEnvironmentVariables(selectedGlobalEnvironment?.variables, globalEnvironmentVariableTab, globalEnvironmentVariableQuery))
  const visibleEnvironmentRows = $derived(visibleEnvironmentVariables(selectedEnvironment?.variables, environmentVariableTab, environmentVariableQuery))
  const dotEnvRows = $derived(parseDotEnvRows(dotEnvContent))
  const doneFeatures = $derived(appState?.featureLedger?.filter((feature) => feature.status === 'done').length ?? 0)
  const partialFeatures = $derived(appState?.featureLedger?.filter((feature) => feature.status === 'partial').length ?? 0)
  const totalFeatures = $derived(appState?.featureLedger?.length ?? 0)
  const websocketConnected = $derived(activeRequest?.type === 'websocket' && responseHeaderValue(activeRequest.response, 'x-websocket-connected') === 'true')
  const websocketEventCount = $derived(responseHeaderValue(activeRequest?.response, 'x-websocket-events'))
  const grpcStreamConnected = $derived(activeRequest?.type === 'grpc' && responseHeaderValue(activeRequest.response, 'x-grpc-stream-connected') === 'true')
  const grpcStreamEnded = $derived(activeRequest?.type === 'grpc' && responseHeaderValue(activeRequest.response, 'x-grpc-stream-ended') === 'true')
  const grpcStreamEventCount = $derived(responseHeaderValue(activeRequest?.response, 'x-grpc-stream-events'))
  const grpcStreamingRequest = $derived(activeRequest?.type === 'grpc' && ['client-streaming', 'server-streaming', 'bidi-streaming'].includes(activeRequest.grpcMethodType ?? ''))
  const grpcClientStreamingRequest = $derived(activeRequest?.type === 'grpc' && ['client-streaming', 'bidi-streaming'].includes(activeRequest.grpcMethodType ?? ''))
  const httpTransportInFlight = $derived(Boolean(
    activeHTTPTransport
      && activeHTTPTransport.collectionId === activeCollection?.id
      && activeHTTPTransport.requestId === activeRequest?.id
  ))
  const hasActiveHTTPTransport = $derived(Boolean(activeHTTPTransport))
  const backgroundHTTPTransport = $derived(activeHTTPTransport && !httpTransportInFlight
    ? {
      requestName: requestNameForTransport(activeHTTPTransport),
      pending: httpCancellationRequested
    }
    : undefined)
  const runnerCancelledCount = $derived(runnerCancellationCount(appState?.runner))
  const runnerCompletedCancelled = $derived(runnerCancelledCount > 0 || lastCollectionRunCancelled)
  const requestCommand = $derived(buildRequestCommandState(
    activeRequest,
    activeCollection,
    selectedEnvironment?.name,
    busy,
    websocketConnected,
    grpcStreamConnected,
    appState?.preferences,
    httpTransportInFlight,
    httpTransportInFlight && httpCancellationRequested,
    backgroundHTTPTransport,
    activeWorkspace?.scratchCollectionId
  ))
  const activeScriptLogs = $derived(responseScriptLogs(activeRequest?.response))
  const activeTimelineEntries = $derived(sortedTimelineEntries(activeRequest?.timeline ?? []))
  // US-021/US-022. Both operands are named inside the statement so this really
  // does re-run — a `$:` that referenced only a helper function would track
  // nothing and go stale, which is the ResponseInspector bug US-004 found.
  const activeLiveSessionLog = $derived(
    activeCollection && activeRequest
      ? liveSessionLogs[liveSessionKey(activeCollection.id, activeRequest.id)]
      : undefined
  )
  const devToolsConsoleRows = $derived(devToolsConsoleLogs(activeWorkspace))
  const rawDevToolsNetworkRows = $derived(appState?.networkLog ?? [])
  $effect(() => {
  if (appState && devToolsNetworkPreferencesKeyFor(appState.preferences?.devTools?.network) !== devToolsNetworkPreferencesKey) {
      applyDevToolsNetworkPreferences(appState.preferences?.devTools?.network)
    }
  })
  const devToolsNetworkMethodCounts = $derived(Object.fromEntries(devToolsNetworkMethods.map((method) => [method, rawDevToolsNetworkRows.filter((row) => normalizedNetworkMethod(row) === method).length])) as Record<string, number>)
  const devToolsNetworkActiveFilterCount = $derived(devToolsNetworkMethods.filter((method) => devToolsNetworkFilters[method]).length)
  const devToolsNetworkRows = $derived(sortedDevToolsNetworkRows(filteredDevToolsNetworkRows(rawDevToolsNetworkRows, devToolsNetworkFilters), devToolsNetworkSortKey, devToolsNetworkSortDirection))
  // US-032. Moved below devToolsNetworkRows during the US-029 runes migration.
  // Legacy `$:` statements were topologically sorted by the compiler, so their
  // order in the file did not matter. $derived is a const and obeys the
  // temporal dead zone, so a derivation must now appear after everything it
  // reads — this pair used to sit a couple of hundred lines above its input.
  const devToolsNetworkWindow = $derived(computeWindow({
    total: devToolsNetworkRows.length,
    rowHeight: devToolsNetworkRowHeight,
    viewportHeight: devToolsNetworkViewportHeight,
    scrollTop: devToolsNetworkScrollTop
  }))
  const devToolsNetworkVisibleRows = $derived(devToolsNetworkRows.slice(
    devToolsNetworkWindow.startIndex,
    devToolsNetworkWindow.endIndex
  ))
  const devToolsNetworkSortLabels = $derived(Object.fromEntries(devToolsNetworkSortKeys.map((key) => [key, devToolsNetworkSortLabel(key, devToolsNetworkSortKey, devToolsNetworkSortDirection)])) as Record<DevToolsNetworkSortKey, string>)
  const devToolsNetworkAriaSort = $derived(Object.fromEntries(devToolsNetworkSortKeys.map((key) => [key, devToolsNetworkSortAriaValue(key, devToolsNetworkSortKey, devToolsNetworkSortDirection)])) as Record<DevToolsNetworkSortKey, 'ascending' | 'descending' | 'none'>)
  const devToolsNetworkTableWidth = $derived(devToolsNetworkColumnWidths.reduce((total, width) => total + width, 0))
  const selectedDevToolsNetworkRow = $derived(devToolsNetworkRows.find((row) => row.id === selectedDevToolsNetworkLogID) ?? devToolsNetworkRows[0])
  const activeTerminalSession = $derived(terminalSessions.find((session) => session.id === terminalActiveSessionId))
  const devToolsPerformanceProcesses = $derived(devToolsSnapshot?.processes ?? [])
  $effect(() => {
  if (devToolsPerformanceView !== 'cumulative' && !devToolsPerformanceProcesses.some((process) => String(process.pid) === devToolsPerformanceView)) devToolsPerformanceView = 'cumulative'
  })
  const selectedDevToolsPerformanceProcess = $derived(devToolsPerformanceProcesses.find((process) => String(process.pid) === devToolsPerformanceView))
  const displayedDevToolsCPUPercent = $derived(selectedDevToolsPerformanceProcess?.cpuPercent ?? devToolsSnapshot?.cpuPercent)
  const displayedDevToolsMemoryBytes = $derived(selectedDevToolsPerformanceProcess?.memoryBytes ?? devToolsSnapshot?.memoryBytes)
  const displayedDevToolsUptimeSeconds = $derived(selectedDevToolsPerformanceProcess?.uptimeSeconds ?? devToolsSnapshot?.uptimeSeconds)
  const displayedDevToolsPID = $derived(selectedDevToolsPerformanceProcess?.pid ?? devToolsSnapshot?.pid)
  $effect(() => {
  if (devToolsNetworkRows.length > 0 && (!selectedDevToolsNetworkLogID || !devToolsNetworkRows.some((row) => row.id === selectedDevToolsNetworkLogID))) {
      selectedDevToolsNetworkLogID = devToolsNetworkRows[0].id
      devToolsNetworkDetailTab = 'request'
    }
  })
  $effect(() => {
  if (devToolsNetworkRows.length === 0 && selectedDevToolsNetworkLogID) selectedDevToolsNetworkLogID = ''
  })
  const requestVariableNames = $derived(activeRequest ? variableNamesForRequest(activeRequest) : [])
  const requestProcessEnvNames = $derived(requestVariableNames.filter((name) => name.startsWith('process.env.')))
  // US-034. This statement re-ran whenever ANY of its dependencies was
  // invalidated, and it walks every variable scope to resolve each name. Keyed
  // on the request id, the revision, the environment and the process-env
  // signature — every input the resolution actually reads. Leaving one out
  // would return tooltips resolved against the previous environment, which
  // renders perfectly and is simply wrong.
  let tooltipMemo: Memo<string, VariableTooltipInfo[]> = null
  const requestVariableTooltips = $derived((() => {
    if (!activeWorkspace || !activeCollection || !activeRequest) return []
    const workspace = activeWorkspace
    const collection = activeCollection
    const request = activeRequest
    const key = `${request.id}:${appState?.revision ?? 0}:${selectedEnvironmentId}:${processEnvTooltipSignature}`
    const result = memoized(tooltipMemo, key, () =>
      variableTooltipsForRequest(workspace, collection, request, selectedEnvironmentId, processEnvTooltipValues)
    )
    tooltipMemo = result.memo
    return result.value
  })())
  const searchQuery = $derived(normalizedSearch(requestSearch))
  const globalSearchResults = $derived(buildGlobalSearchResults(activeWorkspace, globalSearchQuery))
  const visibleNotifications = $derived(notificationsForDisplay(appState?.notifications ?? []))
  const unreadNotificationCount = $derived(visibleNotifications.filter((notification) => !notification.read).length)
  const listedNotifications = $derived(notificationTab === 'unread'
    ? pinnedUnreadNotificationIDs
      ? visibleNotifications.filter((notification) => pinnedUnreadNotificationIDs?.has(notification.id))
      : visibleNotifications.filter((notification) => !notification.read)
    : visibleNotifications)
  const selectedNotification = $derived(listedNotifications.find((notification) => notification.id === selectedNotificationID) ?? listedNotifications[0])
  const visibleSidebarCollections = $derived(sidebarCollections(activeWorkspace, searchQuery))
  const sidebarSearchCount = $derived(sidebarRequestCount(activeWorkspace, searchQuery))
  const cookieSearchQuery = $derived(normalizedSearch(cookieSearch))
  const visibleCookieGroups = $derived(cookieGroups(appState?.cookies ?? [], cookieSearchQuery))
  const selectedThemeMode = $derived(normalizedThemeMode(appState?.preferences?.theme))
  const displayedThemeMode = $derived(selectedThemeMode === 'system' ? systemThemeMode : selectedThemeMode)
  const selectedThemeVariant = $derived(displayedThemeMode === 'dark'
    ? normalizedThemeVariant(appState?.preferences?.themeVariantDark, darkThemeVariants)
    : normalizedThemeVariant(appState?.preferences?.themeVariantLight, lightThemeVariants))
  $effect(() => {
  applyThemeToDocument(displayedThemeMode, selectedThemeVariant)
  })
  $effect(() => {
  applyZoomToDocument(appZoomPercentage)
  })
  $effect(() => {
  applyCodeFontToDocument(codeFont, codeFontSize)
  })
  $effect(() => {
  if (globalSearchIndex >= globalSearchResults.length) {
      globalSearchIndex = Math.max(0, globalSearchResults.length - 1)
    }
  })
  $effect(() => {
  {
      const presetKey = activeCollection ? `${activeCollection.id}:${activeCollection.presets?.requestType ?? ''}` : ''
      if (presetKey !== lastPresetKey) {
        lastPresetKey = presetKey
        requestType = normalizePresetRequestType(activeCollection?.presets?.requestType) || 'http'
      }
    }
  })
  const visibleCookieCount = $derived(visibleCookieGroups.reduce((total, group) => total + group.cookies.length, 0))
  $effect(() => {
  if ((activeCollection?.id ?? '') !== gitRemoteCollectionId) {
      gitRemoteCollectionId = activeCollection?.id ?? ''
      gitRemoteURL = activeCollection?.remote ?? ''
    }
  })
  $effect(() => {
  if (activeWorkspace?.path && !gitCloneRoot) gitCloneRoot = activeWorkspace.path
  })
  $effect(() => {
  if ((activeRequest?.id ?? '') !== grpcMethodsRequestId) {
      grpcMethodsRequestId = activeRequest?.id ?? ''
      grpcMethods = []
      grpcMethodMessage = ''
    }
  })
  $effect(() => {
  {
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
  })
  $effect(() => {
  {
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
  })
  $effect(() => {
  {
      if (devToolsOpen && devToolsTab === 'terminal') {
        startTerminalPolling()
      } else {
        stopTerminalPolling()
      }
    }
  })

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

  function normalizedThemeVariant(value: string | undefined, variants: ThemeVariant[]) {
    return normalizedThemeVariantOf(value, variants)
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

  function keybindingsAreEnabled(preferences: types.Preferences | undefined) {
    return keybindingsEnabledFor(preferences?.keybindingsEnabled)
  }

  // US-057. The preset sits between the defaults and the user's overrides, and
  // the order is the point: a shortcut somebody deliberately set must not be
  // silently replaced by switching preset.
  const activeKeyBindingPreset = $derived(normalizeKeyBindingPreset(appState?.preferences?.keyBindingPreset) as KeyBindingPresetID)
  const presetKeyBindings = $derived(effectiveKeyBindings(keyBindingSections, keyBindingPresets[activeKeyBindingPreset]))

  function mergedKeyBinding(action: string): KeyBindingDefinition | undefined {
    const override = appState?.preferences?.keyBindings?.[action] as types.KeyBinding | undefined
    return mergeKeyBinding(presetKeyBindings[action], override)
  }

  function keyBindingValue(action: string, os: KeyBindingOS = currentKeyBindingOS()) {
    return keyBindingValueFor(mergedKeyBinding(action), os)
  }

  function keyBindingDisplayValue(action: string, os: KeyBindingOS = currentKeyBindingOS()) {
    return keyBindingDisplayValueFor(mergedKeyBinding(action), os)
  }





  function keyBindingEventMatches(event: KeyboardEvent, action: string) {
    const combo = keyBindingComboFromEvent(event)
    const value = keyBindingValue(action)
    if (!combo || !value) return false
    return keyBindingSignature(combo) === keyBindingSignature(value)
  }

  function formatKeyBinding(value: string) {
    return formatKeyBindingFor(value, currentKeyBindingOS())
  }

  function keyBindingIsCustomized(action: string) {
    return Boolean(appState?.preferences?.keyBindings?.[action])
  }

  function keyBindingCanEdit(action: string) {
    return canEditKeyBinding(mergedKeyBinding(action))
  }

  // Delegates to the module, supplying the resolved bindings and the current
  // OS. The merge with the user's overrides depends on app state, which is why
  // that half stays here and only the rule lives in lib/keybindings.
  function validateKeyBinding(action: string, combo: string) {
    const resolved: Record<string, KeyBindingDefinition> = {}
    for (const section of keyBindingSections) {
      for (const otherAction of Object.keys(section.bindings)) {
        const merged = mergedKeyBinding(otherAction)
        if (merged) resolved[otherAction] = merged
      }
    }
    return validateKeyBindingRule(action, combo, resolved, currentKeyBindingOS())
  }

  function stopDotEnvRefresh() {
    if (dotEnvRefreshTimer === undefined) return
    window.clearInterval(dotEnvRefreshTimer)
    dotEnvRefreshTimer = undefined
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

  function variableTooltipsForRequest(workspace: types.Workspace, collection: types.Collection, request: types.RequestItem, environmentId: string, processEnvValues: Record<string, string>): VariableTooltipInfo[] {
    return variableNamesForRequest(request).map((name) => resolveVariableTooltip(name, workspace, collection, request, environmentId, processEnvValues))
  }


  function isValidURLVariableSegment(segment: URLVariableSegment) {
    return segment.variable && segment.info.found && segment.info.validName
  }













  function toggleTooltipSecret(name: string) {
    variableTooltips.toggleRevealed(name)
  }

  async function copyVariableTooltipValue(info: VariableTooltipInfo) {
    const latestInfo = info.source === 'path'
      ? pathParamTooltipInfo(info.name, activeRequest?.pathParams ?? [])
      : requestVariableTooltips.find((candidate) => candidate.name === info.name) ?? info
    if (!latestInfo.found || !latestInfo.validName || variableTooltips.isCopied(latestInfo.name)) return
    try {
      await navigator.clipboard.writeText(latestInfo.resolvedValue ?? '')
      if (copiedVariableTooltipTimers[latestInfo.name]) {
        window.clearTimeout(copiedVariableTooltipTimers[latestInfo.name])
      }
      variableTooltips.markCopied(latestInfo.name, true)
      copiedVariableTooltipTimers = {
        ...copiedVariableTooltipTimers,
        [latestInfo.name]: window.setTimeout(() => {
          variableTooltips.markCopied(latestInfo.name, false)
          const { [latestInfo.name]: _removed, ...remaining } = copiedVariableTooltipTimers
          copiedVariableTooltipTimers = remaining
        }, 1200)
      }
    } catch {
      error = 'Clipboard is unavailable'
    }
  }

  function toggleActiveVariableTooltip(name: string) {
    variableTooltips.toggleActive(name)
  }

  function handleInlineVariableTokenKey(event: KeyboardEvent, name: string) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      toggleActiveVariableTooltip(name)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      variableTooltips.close()
    }
  }

  function closeVariableTooltipOnOutside(event: MouseEvent) {
    const target = event.target as HTMLElement | null
    if (!target) return
    if (!target.closest('.request-actions')) closeRequestActionMenus()
    if (target.closest('.variable-chip-wrapper, .url-variable-token-wrapper, .inline-variable-token-wrapper, .CodeMirror-brunoVarInfo, .variable-tooltip')) return
    variableTooltips.close()
  }

  function closeRequestActionMenus() {
    document.querySelectorAll<HTMLDetailsElement>('details.request-actions[open]').forEach((menu) => {
      menu.open = false
    })
    // Clearing the record as well, rather than trusting the toggle event to
    // propagate back through bind:open. The record is what the {#if} reads, so
    // a menu whose buttons stayed mounted after closing would quietly undo the
    // saving this change exists for.
    openRequestMenus = {}
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
    updateBody({ [field]: textarea.value } as Partial<types.RequestBody>)
  }

  function syncBodyTextScroll(event: Event) {
    const textarea = event.currentTarget as HTMLTextAreaElement
    bodyTextScrollLeft = textarea.scrollLeft
    bodyTextScrollTop = textarea.scrollTop
  }

  function beginVariableTooltipEdit(info: VariableTooltipInfo) {
    if (!info.editable) return
    variableTooltips.beginEdit(info.name, info.rawValue, info.found, info.editable)
  }

  function cancelVariableTooltipEdit() {
    variableTooltips.cancelEdit()
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

  async function saveVariableTooltipEdit(info: VariableTooltipInfo, nextValue = variableTooltips.draft) {
    if (!activeWorkspace || !activeCollection || !activeRequest || !info.editable) return
    const value = nextValue
    await runAction('save variable', async () => {
      if (info.source === 'global') {
        const environment = activeWorkspace.globalEnvironments?.find((candidate) => candidate.id === info.globalEnvironmentId)
        if (!environment || info.index < 0) throw new Error(`Global variable ${info.name} was not found`)
        const vars = [...(environment.variables ?? [])]
        vars[info.index] = { ...vars[info.index], value }
        workspaceStore.appState = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, environment.id, vars)
      } else if (info.source === 'collection') {
        if (info.index < 0) throw new Error(`Collection variable ${info.name} was not found`)
        const vars = [...(activeCollection.variables ?? [])]
        vars[info.index] = { ...vars[info.index], value }
        workspaceStore.appState = await UpdateCollectionVariables(activeCollection.id, vars)
      } else if (info.source === 'environment') {
        const environment = activeCollection.environments?.find((candidate) => candidate.id === info.environmentId)
        if (!environment || info.index < 0) throw new Error(`Environment variable ${info.name} was not found`)
        const vars = [...(environment.variables ?? [])]
        vars[info.index] = { ...vars[info.index], value }
        workspaceStore.appState = await UpdateEnvironmentVariables(activeCollection.id, environment.id, vars)
      } else if (info.source === 'path') {
        if (info.index < 0) throw new Error(`Path parameter ${info.name} was not found`)
        const pathParams = [...(activeRequest.pathParams ?? [])]
        pathParams[info.index] = { ...pathParams[info.index], value, enabled: true }
        workspaceStore.appState = await UpdateRequest(activeCollection.id, activeRequest.id, { pathParams } as unknown as types.RequestPatch)
        workspaceStore.appState = await SaveRequest(activeCollection.id, activeRequest.id)
      } else if (info.source === 'request' || info.source === 'missing') {
        const req = [...(activeRequest.vars?.req ?? [])]
        if (info.source === 'request' && info.index >= 0) {
          req[info.index] = { ...req[info.index], value }
        } else {
          req.push({ id: `req-var-${Date.now()}`, name: info.name, value, type: 'text', dataType: 'string', enabled: true, secret: false })
        }
        const collectionId = activeCollection.id
        const requestId = activeRequest.id
        workspaceStore.appState = await UpdateRequest(collectionId, requestId, { vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as types.RequestPatch)
        workspaceStore.appState = await SaveRequest(collectionId, requestId)
      }
      variableTooltips.cancelEdit()
      variableTooltips.cancelEdit()
      variableTooltips.toggleActive(info.name)
    })
  }

  async function load() {
    loading = true
    loadingStatus = 'Opening workspace'
    await runAction('load', async () => {
      workspaceStore.appState = await GetState()
	    workbenchStorageScope = await GetWebStorageScope()
	    restoreWorkbenchLayout()
      loadingStatus = 'Checking recovery'
      recoveryEntries = (await ListRecoveryEntries()) ?? []
      loadingStatus = 'Measuring local cache'
      await refreshFileCacheSize()
      loadingStatus = 'Preparing workbench'
      applyDevToolsShellPreferences(appState?.preferences?.devTools)
      if (devToolsOpen) await refreshDevToolsSnapshot()
      workspaceStore.selectedEnvironmentId = activeCollection?.environments?.[0]?.id ?? ''
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
      workspaceStore.appState = await RestoreRecoveryEntry(entry.id)
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
      workspaceStore.appState = await CreateCollection(activeWorkspace.id, collectionName, 'yml')
    })
  }

  async function createRequest() {
    if (!activeCollection) return
    await runAction('create request', async () => {
      workspaceStore.appState = await CreateRequest(activeCollection.id, requestType, requestName)
      workspaceStore.selectedCollectionId = activeCollection.id
      activeView = 'request'
    })
  }

  async function saveRequest() {
    if (!activeCollection || !activeRequest) return
    await runAction('save request', async () => {
      workspaceStore.appState = await SaveRequest(activeCollection.id, activeRequest.id)
    })
  }

  async function saveAllOpenTabs() {
    await runAction('save all tabs', async () => {
      workspaceStore.appState = await SaveAllTabs(activeTab?.collectionId ?? activeCollection?.id ?? '')
    })
  }

  async function saveResponseExample() {
    if (!activeCollection || !activeRequest?.response) return
    await runAction('save response example', async () => {
      workspaceStore.appState = await SaveResponseExample(activeCollection.id, activeRequest.id, '')
      responseTab = 'examples'
    })
  }

  function suggestedResponseExampleName() {
    return suggestedResponseExampleNameOf((activeRequest?.examples ?? []).map((example) => example.name))
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
      workspaceStore.appState = await CreateResponseExample(activeCollection.id, activeRequest.id, name, createResponseExampleDescription.trim())
      responseTab = 'examples'
      cancelCreateResponseExample()
    })
  }

  function responseExampleForTab(tab: types.OpenTab | undefined) {
    return findResponseExampleForTab(tab, activeWorkspace?.collections)
  }

  function tabLabel(tab: types.OpenTab) {
    return tabLabelFor(tab, activeWorkspace?.collections)
  }

  function tabMethod(tab: types.OpenTab) {
    return tabMethodFor(tab, activeWorkspace?.collections)
  }

  let collapsedSidebarCollections = $state<Record<string, boolean>>({})
  let collapsedSidebarFolders = $state<Record<string, boolean>>({})

  function toggleSidebarCollection(collectionId: string) {
    collapsedSidebarCollections = { ...collapsedSidebarCollections, [collectionId]: !collapsedSidebarCollections[collectionId] }
  }

  function toggleSidebarFolder(collectionId: string, folder: string) {
    const key = sidebarFolderKey(collectionId, folder)
    collapsedSidebarFolders = { ...collapsedSidebarFolders, [key]: !collapsedSidebarFolders[key] }
  }

  function requestIsTransient(collection: types.Collection | undefined, item: types.RequestItem | undefined) {
    return isRequestTransient(collection, item, activeWorkspace?.scratchCollectionId)
  }

  function collectionIsScratch(collection: types.Collection | undefined) {
    return isScratchCollection(collection, activeWorkspace?.scratchCollectionId)
  }


  function beginRenameResponseExample(example: types.ResponseExample) {
    editingResponseExampleID = responseExampleIdentifier(example)
    responseExampleNameDraft = example.name
    deletingResponseExampleID = ''
  }

  function cancelRenameResponseExample() {
    editingResponseExampleID = ''
    responseExampleNameDraft = ''
  }

  async function renameResponseExample(example: types.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    const name = responseExampleNameDraft.trim()
    if (!name) return
    await runAction('rename response example', async () => {
      workspaceStore.appState = await RenameResponseExample(activeCollection.id, activeRequest.id, responseExampleIdentifier(example), name)
      editingResponseExampleID = ''
      responseExampleNameDraft = ''
      responseTab = 'examples'
    })
  }

  async function cloneResponseExample(example: types.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    await runAction('clone response example', async () => {
      workspaceStore.appState = await CloneResponseExample(activeCollection.id, activeRequest.id, responseExampleIdentifier(example))
      responseTab = 'examples'
      deletingResponseExampleID = ''
    })
  }

  function requestDeleteResponseExample(example: types.ResponseExample) {
    deletingResponseExampleID = responseExampleIdentifier(example)
    editingResponseExampleID = ''
  }

  async function deleteResponseExample(example: types.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    const id = responseExampleIdentifier(example)
    await runAction('delete response example', async () => {
      workspaceStore.appState = await DeleteResponseExample(activeCollection.id, activeRequest.id, id)
      deletingResponseExampleID = ''
      if (editingResponseExampleDetailsID === id) editingResponseExampleDetailsID = ''
      const { [id]: _deleted, ...remainingDrafts } = responseExampleDrafts
      responseExampleDrafts = remainingDrafts
      responseTab = 'examples'
    })
  }

  function cloneResponseExampleDraft(example: types.ResponseExample) {
    return JSON.parse(JSON.stringify(example)) as types.ResponseExample
  }

  function responseExampleDraft(example: types.ResponseExample) {
    return responseExampleDrafts[responseExampleIdentifier(example)] ?? example
  }

  function beginEditResponseExampleDetails(example: types.ResponseExample) {
    const id = responseExampleIdentifier(example)
    editingResponseExampleDetailsID = id
    responseExampleDrafts = { ...responseExampleDrafts, [id]: cloneResponseExampleDraft(example) }
    deletingResponseExampleID = ''
  }

  function cancelEditResponseExampleDetails(example: types.ResponseExample) {
    const id = responseExampleIdentifier(example)
    if (editingResponseExampleDetailsID === id) editingResponseExampleDetailsID = ''
    const { [id]: _removed, ...remainingDrafts } = responseExampleDrafts
    responseExampleDrafts = remainingDrafts
  }

  function updateResponseExampleDraft(example: types.ResponseExample, updater: (draft: types.ResponseExample) => types.ResponseExample) {
    const id = responseExampleIdentifier(example)
    const draft = cloneResponseExampleDraft(responseExampleDrafts[id] ?? example)
    responseExampleDrafts = { ...responseExampleDrafts, [id]: updater(draft) }
  }

  function updateResponseExampleDescription(example: types.ResponseExample, value: string) {
    updateResponseExampleDraft(example, (draft) => {
      draft.description = value
      return draft
    })
  }


  function updateResponseExampleResponseField(example: types.ResponseExample, field: keyof types.ResponseExamplePayload, value: string | number) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response = applyResponseExampleResponseField(draft.response, field, value)
      return draft
    })
  }

  function updateResponseExampleRequestField(example: types.ResponseExample, field: keyof types.ResponseExampleRequest, value: string) {
    updateResponseExampleDraft(example, (draft) => {
      draft.request = applyResponseExampleRequestField(draft.request, field, value)
      return draft
    })
  }

  function prettifyResponseExampleRequestBody(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.body = prettifyJSON(request.body ?? '')
      draft.request = request
      return draft
    })
  }

  function prettifyResponseExampleResponseBody(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const response = { ...(draft.response ?? {}) } as types.ResponseExamplePayload
      response.body = prettifyJSON(response.body ?? '')
      draft.response = response
      return draft
    })
  }

  function addResponseExampleRequestParam(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.params = [...(request.params ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestParam(example: types.ResponseExample, index: number, field: keyof types.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.params ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue)
      rows[index] = { ...current, [field]: value } as types.KeyValue
      request.params = rows
      draft.request = request
      return draft
    })
  }

  function replaceResponseExampleRequestParams(example: types.ResponseExample, rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.params = normalizeBulkKeyValueRows(rows)
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestParam(example: types.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.params = movedRows(request.params, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestParam(example: types.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.params = reorderedRows(request.params, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestParam(example: types.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.params ?? [])]
      rows.splice(index, 1)
      request.params = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestFormRow(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.formUrlEncoded = [...(request.formUrlEncoded ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestFormRow(example: types.ResponseExample, index: number, field: keyof types.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.formUrlEncoded ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue)
      rows[index] = { ...current, [field]: value } as types.KeyValue
      request.formUrlEncoded = rows
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestFormRow(example: types.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.formUrlEncoded = movedRows(request.formUrlEncoded, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestFormRow(example: types.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.formUrlEncoded = reorderedRows(request.formUrlEncoded, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestFormRow(example: types.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.formUrlEncoded ?? [])]
      rows.splice(index, 1)
      request.formUrlEncoded = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestMultipartRow(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.multipartForm = [...(request.multipartForm ?? []), { name: '', value: '', filePath: '', contentType: '', enabled: true } as types.FormPart]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestMultipartRow(example: types.ResponseExample, index: number, field: keyof types.FormPart, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.multipartForm ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', filePath: '', contentType: '', enabled: true } as types.FormPart)
      rows[index] = { ...current, [field]: value } as types.FormPart
      request.multipartForm = rows
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestMultipartRow(example: types.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.multipartForm = movedRows(request.multipartForm, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestMultipartRow(example: types.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.multipartForm = reorderedRows(request.multipartForm, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestMultipartRow(example: types.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.multipartForm ?? [])]
      rows.splice(index, 1)
      request.multipartForm = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleRequestFileRow(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.file ?? [])]
      rows.push({ filePath: '', contentType: '', selected: rows.length === 0 } as types.FileBodyEntry)
      request.file = rows
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestFileRow(example: types.ResponseExample, index: number, field: keyof types.FileBodyEntry, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      draft.request = applyResponseExampleFileRow(draft.request, index, field, value)
      return draft
    })
  }

  function moveResponseExampleRequestFileRow(example: types.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.file = movedRows(request.file, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestFileRow(example: types.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.file = reorderedRows(request.file, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestFileRow(example: types.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      draft.request = removeResponseExampleFileRow(draft.request, index)
      return draft
    })
  }

  function addResponseExampleRequestHeader(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.headers = [...(request.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue]
      draft.request = request
      return draft
    })
  }

  function updateResponseExampleRequestHeader(example: types.ResponseExample, index: number, field: keyof types.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.headers ?? [])]
      const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue)
      rows[index] = { ...current, [field]: value } as types.KeyValue
      request.headers = rows
      draft.request = request
      return draft
    })
  }

  function replaceResponseExampleRequestHeaders(example: types.ResponseExample, rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.headers = normalizeBulkKeyValueRows(rows)
      draft.request = request
      return draft
    })
  }

  function moveResponseExampleRequestHeader(example: types.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.headers = movedRows(request.headers, index, direction)
      draft.request = request
      return draft
    })
  }

  function reorderResponseExampleRequestHeader(example: types.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      request.headers = reorderedRows(request.headers, from, to)
      draft.request = request
      return draft
    })
  }

  function removeResponseExampleRequestHeader(example: types.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const request = { ...(draft.request ?? {}) } as types.ResponseExampleRequest
      const rows = [...(request.headers ?? [])]
      rows.splice(index, 1)
      request.headers = rows
      draft.request = request
      return draft
    })
  }

  function addResponseExampleHeader(example: types.ResponseExample) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = [...(draft.response.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue]
      return draft
    })
  }

  function updateResponseExampleHeader(example: types.ResponseExample, index: number, field: keyof types.KeyValue, value: string | boolean) {
    updateResponseExampleDraft(example, (draft) => {
      const result = applyResponseExampleHeader(draft.response.headers, draft.response.bodyType, index, field, value)
      draft.response.headers = result.headers
      if (result.bodyType !== undefined) draft.response.bodyType = result.bodyType
      return draft
    })
  }

  function replaceResponseExampleHeaders(example: types.ResponseExample, rows: Array<{ name: string; value: string; enabled: boolean; secret?: boolean; description?: string }>) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = normalizeBulkKeyValueRows(rows)
      return draft
    })
  }

  function moveResponseExampleHeader(example: types.ResponseExample, index: number, direction: -1 | 1) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = movedRows(draft.response.headers, index, direction)
      return draft
    })
  }

  function reorderResponseExampleHeader(example: types.ResponseExample, from: number, to: number) {
    updateResponseExampleDraft(example, (draft) => {
      draft.response.headers = reorderedRows(draft.response.headers, from, to)
      return draft
    })
  }

  function removeResponseExampleHeader(example: types.ResponseExample, index: number) {
    updateResponseExampleDraft(example, (draft) => {
      const rows = [...(draft.response.headers ?? [])]
      rows.splice(index, 1)
      draft.response.headers = rows
      return draft
    })
  }

  async function saveResponseExampleDetails(example: types.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    const id = responseExampleIdentifier(example)
    const draft = responseExampleDrafts[id]
    if (!draft) return
    await runAction('update response example', async () => {
      workspaceStore.appState = await UpdateResponseExample(activeCollection.id, activeRequest.id, id, draft)
      editingResponseExampleDetailsID = ''
      const { [id]: _saved, ...remainingDrafts } = responseExampleDrafts
      responseExampleDrafts = remainingDrafts
      responseTab = 'examples'
    })
  }

  async function loadResponseExampleCode(example: types.ResponseExample, language = responseExampleCodeLanguage) {
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

  async function beginGenerateResponseExampleCode(example: types.ResponseExample) {
    generatingResponseExampleID = responseExampleIdentifier(example)
    responseExampleCodeLanguage = 'curl'
    responseExampleGeneratedCode = ''
    await loadResponseExampleCode(example, responseExampleCodeLanguage)
  }

  async function changeResponseExampleCodeLanguage(example: types.ResponseExample, language: string) {
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

  function requestSupportsGenerateCode(item: types.RequestItem | undefined) {
    return item?.type === 'http' || item?.type === 'graphql'
  }

  function requestCodeEnvironmentId(collection: types.Collection) {
    if (collection.id === activeCollection?.id) return selectedEnvironmentId
    return collection.environments?.[0]?.id ?? ''
  }

  async function loadRequestCode(target = requestCodeTarget, language = requestCodeLanguage) {
    if (!target) return
    await runAction('generate request code', async () => {
      requestGeneratedCode = await GenerateRequestCode(target.collectionId, target.itemId, target.environmentId, language)
    })
  }

  let codeGenerationTargets = $state<{ id: string; label: string }[]>([
    { id: 'curl', label: 'cURL' },
    { id: 'fetch', label: 'JavaScript (fetch)' }
  ])

  async function beginGenerateRequestCode(collection: types.Collection, item: types.RequestItem) {
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
    // Loaded lazily on first open rather than at startup: the list is static
    // for the process, and fetching it eagerly would add a binding call to
    // every launch for a dialog most sessions never open.
    if (codeGenerationTargets.length <= 2) {
      try {
        codeGenerationTargets = await CodeGenerationTargets()
      } catch {
        // The hard-coded curl/fetch pair remains usable, so a failure here
        // narrows the menu rather than breaking the dialog.
      }
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
    } as types.RequestItem
    const promptNames = collectPromptNames(activeCollection, requestForPrompts, selectedEnvironmentId, activeGlobalEnvironment)
    return promptNames.length > 0 ? await promptForVariables(promptNames) : {}
  }

  async function connectActiveGrpcStream() {
    if (!activeCollection || !activeRequest) return
    const promptValues = await promptValuesForGrpcStreamMessage(null)
    if (promptValues === null) return
    await runAction('start gRPC stream', async () => {
      workspaceStore.appState = Object.keys(promptValues).length > 0
        ? await ConnectGRPCStreamWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
        : await ConnectGRPCStream(activeCollection.id, activeRequest.id, selectedEnvironmentId)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function endActiveGrpcStream() {
    if (!activeCollection || !activeRequest) return
    await runAction('end gRPC stream', async () => {
      workspaceStore.appState = await EndGRPCStream(activeCollection.id, activeRequest.id)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function cancelActiveGrpcStream() {
    if (!activeCollection || !activeRequest) return
    await runAction('cancel gRPC stream', async () => {
      workspaceStore.appState = await CancelGRPCStream(activeCollection.id, activeRequest.id)
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
        workspaceStore.appState = hasPromptValues
          ? await SendGRPCStreamMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendGRPCStreamMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      } catch (err) {
        if (!String(err).includes('gRPC stream is not connected')) throw err
        workspaceStore.appState = hasPromptValues
          ? await ConnectGRPCStreamWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
          : await ConnectGRPCStream(activeCollection.id, activeRequest.id, selectedEnvironmentId)
        workspaceStore.appState = hasPromptValues
          ? await SendGRPCStreamMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendGRPCStreamMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      }
      activeView = 'request'
      responseTab = 'response'
    })
  }

  function selectedWSMessageIndex(request: types.RequestItem) {
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
    })) as types.WSMessage[]
    const requestForPrompts = { ...activeRequest, wsMessages: messages } as types.RequestItem
    const promptNames = collectPromptNames(activeCollection, requestForPrompts, selectedEnvironmentId, activeGlobalEnvironment)
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
      workspaceStore.appState = Object.keys(promptValues).length > 0
        ? await ConnectWebSocketWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
        : await ConnectWebSocket(activeCollection.id, activeRequest.id, selectedEnvironmentId)
      activeView = 'request'
      responseTab = 'response'
    })
  }

  async function disconnectActiveWebSocket() {
    if (!activeCollection || !activeRequest) return
    await runAction('disconnect WebSocket', async () => {
      workspaceStore.appState = await DisconnectWebSocket(activeCollection.id, activeRequest.id)
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
        workspaceStore.appState = hasPromptValues
          ? await SendWebSocketMessageWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, index, promptValues)
          : await SendWebSocketMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, index)
      } catch (err) {
        if (!String(err).includes('WebSocket is not connected')) throw err
        workspaceStore.appState = hasPromptValues
          ? await ConnectWebSocketWithPromptValues(activeCollection.id, activeRequest.id, selectedEnvironmentId, promptValues)
          : await ConnectWebSocket(activeCollection.id, activeRequest.id, selectedEnvironmentId)
        workspaceStore.appState = hasPromptValues
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
    const promptNames = collectPromptNames(collection, request, environmentId, activeGlobalEnvironment)
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
        workspaceStore.appState = promptValues
          ? await SendRequestWithPromptValues(collectionId, requestId, environmentId, promptValues)
          : await SendRequest(collectionId, requestId, environmentId)
        if (appState?.activeTabId === tabId) {
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

  function runnerItemSelected(itemId: string) {
    return runnerSelectedItemIds.includes(itemId)
  }

  function setRunnerItemSelected(itemId: string, selected: boolean) {
    runnerSelectedItemIds = setRunnerSelection(runnerSelectedItemIds, runnerConfigItems, itemId, selected)
  }

  function toggleRunnerSelectAll() {
    runnerSelectedItemIds = toggleRunnerSelectAllOf(runnerSelectedCount, runnerConfigItems)
  }

  function resetRunnerConfiguration() {
    runnerSelectedItemIds = runnerConfigItems.map((item) => item.id)
    runnerDelayMs = 0
    runnerBailOnFailure = false
    runnerIterations = 1
    runnerDataFile = ''
  }

  async function chooseRunnerDataFile() {
    // A cancelled dialog returns an empty path, which must leave the current
    // selection alone rather than silently clearing it.
    const chosen = await SelectRunnerDataFile()
    if (chosen) runnerDataFile = chosen
  }

  async function runCollection() {
    if (!activeCollection || activeCollectionRun || busy !== '') return
    const collection = activeCollection
    const selectedItemIds = runnerSelectedItemIds.filter((id) => runnerConfigItems.some((item) => item.id === id))
    if (selectedItemIds.length === 0) return
    const environmentId = selectedEnvironmentId
    const viewAtStart = activeView
    await runAction('run collection', async () => {
      let completedRunState: types.AppState | undefined
      activeCollectionRun = { collectionId: collection.id, collectionName: collection.name || 'collection' }
      collectionRunCancellationRequested = false
      lastCollectionRunCancelled = false
      try {
        completedRunState = await RunCollectionWithOptions(collection.id, environmentId, {
          selectedItemIds,
          delayMs: normalizedRunnerDelayMs(runnerDelayMs),
          bailOnFailure: runnerBailOnFailure,
          iterations: normalizedRunnerIterations(runnerIterations),
          dataFile: runnerDataFile
        } as types.RunnerOptions)
        workspaceStore.appState = completedRunState
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
      workspaceStore.appState = await CreateEnvironment(activeCollection.id, environmentName)
      workspaceStore.selectedEnvironmentId = activeCollection?.environments?.at(-1)?.id ?? selectedEnvironmentId
      environmentVariableTab = 'variables'
      activeView = 'environments'
    })
  }

  async function createGlobalEnvironment() {
    if (!activeWorkspace) return
    await runAction('create global environment', async () => {
      workspaceStore.appState = await CreateGlobalEnvironment(activeWorkspace.id, globalEnvironmentName)
      globalEnvironmentVariableTab = 'variables'
      activeView = 'environments'
    })
  }

  async function setActiveGlobalEnvironment(environmentId: string) {
    if (!activeWorkspace) return
    workspaceStore.appState = await SetActiveGlobalEnvironment(activeWorkspace.id, environmentId)
  }

  async function setActiveWorkspace(workspaceId: string) {
    if (!appState || !workspaceId || workspaceId === appState.activeWorkspaceId) return
    await runAction('switch workspace', async () => {
      const nextState = await SetActiveWorkspace(workspaceId)
      const workspace = nextState.workspaces?.find((candidate) => candidate.id === workspaceId)
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = workspace?.collections?.[0]?.id ?? ''
      workspaceStore.selectedEnvironmentId = workspace?.collections?.[0]?.environments?.[0]?.id ?? ''
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
    if (workspaceWindowPickerBusy || target.id === appState?.activeWorkspaceId) return
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
        ?? refreshedTargets.find((target) => target.id !== appState?.activeWorkspaceId && target.name.trim().toLocaleLowerCase() === normalizedName)
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
    workspaceStore.appState = await UpdateGlobalEnvironment(activeWorkspace.id, selectedGlobalEnvironment.id, name, color)
  }

  function dotEnvFileKey(file: Pick<types.DotEnvFile, 'scope' | 'name'>) {
    return `${file.scope}:${file.name}`
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

  function selectDotEnvFile(file: types.DotEnvFile) {
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
      const result = await ExportCollectionWithOptions(activeCollection.id, { format: 'yaml' } as types.CollectionExportOptions)
      exportText = result.content ?? ''
      activeView = 'import'
    })
  }

  function importDecisionFor(row: main.CollectionImportPreviewRow): ImportDecision {
    return importDecisions[row.candidateId] ?? defaultImportDecision(row)
  }

  function updateImportDecision(candidateID: string, update: Partial<ImportDecision>) {
    const row = importPreview?.rows?.find((entry) => entry.candidateId === candidateID)
    if (!row) return
    importDecisions = { ...importDecisions, [candidateID]: { ...importDecisionFor(row), ...update } }
  }

	  $effect(() => {
  if (activeView === 'import' && !importDestinationWorkspaceID && activeWorkspace) importDestinationWorkspaceID = activeWorkspace.id
	  })

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
	        next[row.candidateId] = reconcileImportDecision(prior, row, kindOverride)
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
    const next = toggleImportChildID(selectedImportIDs(row, kind), id, checked)
    updateImportDecision(row.candidateId, { [kind]: next } as Partial<ImportDecision>)
  }

  function importSelectionFor(row: main.CollectionImportPreviewRow): main.CollectionImportSelection {
    return importSelectionOf(row, importDecisionFor(row)) as main.CollectionImportSelection
  }

  // Keep both dependencies visible to Svelte: changes to a row checkbox must
  // immediately update the footer count and the selections passed to Apply.
  const importReadyRows = $derived(selectedImportRows(importPreview?.rows ?? [], importDecisions))

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


	  async function applyPlannedImport() {
	    if (importApplyInFlight || !importDestinationWorkspaceID || importReadyRows.length === 0) return
	  importApplyInFlight = true
	  clearImportAttemptResults()
	  let applySucceeded = false
    try {
      await runAction('apply import', async () => {
	        const result = await ApplyCollectionImport({ workspaceId: importDestinationWorkspaceID, destinationRoot: importDestinationRoot, sources: importSources, selections: importReadyRows.map(importSelectionFor), translatePostmanScripts: importTranslatePostmanScripts } as main.CollectionImportApplyRequest)
        importApplyResult = result
	        workspaceStore.appState = result.state
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

	  function openAPISyncOptions(): types.OpenAPISyncOptions {
	    return {
	      sourceUrl: openAPISyncSourceURL,
      content: openAPISyncContent,
      groupBy: openAPISyncGroupBy,
      preserveValues: openAPISyncPreserveValues,
      removeDeleted: false,
      endpointDecisions: openAPISyncEndpointDecisions
	    } as types.OpenAPISyncOptions
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
	      workspaceStore.appState = await UpdateOpenAPISyncConfig(collectionId, {
	        sourceUrl,
	        groupBy,
	        lastSyncDate: existing?.lastSyncDate,
	        specHash: existing?.specHash,
	        autoCheck: openAPISyncSettingsAutoCheck,
	        autoCheckInterval: openAPISyncSettingsInterval
	      } as types.OpenAPISyncConfig)
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
	    if (!appState || collectionWatchRefreshInFlight) return
	    collectionWatchRefreshInFlight = true
	    try {
	      const result = await RefreshChangedCollections()
	      if (result.changed) {
	        workspaceStore.appState = result.state
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

		  function openAPISyncAutoCheckStatusLine(collection: types.Collection | undefined) {
		    return openAPISyncAutoCheckStatusLineOf({
		      config: openAPISyncConfigFor(collection),
		      hasCollection: Boolean(collection),
		      errorMessage: collection ? openAPISyncUpdateErrors[collection.id] : undefined,
		      status: collection ? openAPISyncUpdateStatus[collection.id] : undefined
		    })
		  }

  function reconcileOpenAPISyncEndpointDecisions(result: types.OpenAPISyncResult | undefined) {
    openAPISyncEndpointDecisions = reconcileEndpointDecisions(result?.changes, openAPISyncEndpointDecisions)
  }

  function setOpenAPISyncEndpointDecision(id: string, decision: string) {
    openAPISyncEndpointDecisions = { ...openAPISyncEndpointDecisions, [id]: decision }
  }

  function setOpenAPISyncAllEndpointDecisions(decision: string) {
    openAPISyncEndpointDecisions = allEndpointDecisions(openAPISyncResult?.changes, decision)
  }

  function openAPILocalDriftIDs(changeType: string) {
    return openAPILocalDriftIDsOf(openAPILocalDriftResult, changeType)
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
      workspaceStore.appState = await ApplyOpenAPILocalDrift(collectionId, {
        resetIds,
        restoreIds,
        deleteIds
      } as types.OpenAPILocalDriftOptions)
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
      workspaceStore.appState = await ConnectOpenAPISync(activeCollection.id, openAPISyncOptions())
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
	      workspaceStore.appState = await ApplyOpenAPISync(activeCollection.id, openAPISyncOptions())
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

		  function openAPISpecDiffLineIsActive(lineIndex: number, line: types.OpenAPISyncSpecDiffLine) {
		    return (line.kind ?? 'same') !== 'same' && openAPISpecDiffChangeLineIndexes[openAPISpecDiffActiveChangeIndex] === lineIndex
		  }

		  async function openOpenAPILocalDriftRequest(change: types.OpenAPISyncEndpointChange) {
		    if (!activeCollection || !change.itemId) return
		    await openRequestTab(activeCollection.id, change.itemId)
		  }

		  async function disconnectOpenAPISync() {
		    if (!activeCollection) return
		    await runAction('disconnect OpenAPI sync', async () => {
		      workspaceStore.appState = await DisconnectOpenAPISync(activeCollection.id)
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
      workspaceStore.appState = await OpenCollection(activeWorkspace.id, openCollectionPath)
      workspaceStore.selectedCollectionId = appState?.workspaces?.find((workspace) => workspace.id === activeWorkspace.id)?.collections?.at(-1)?.id ?? selectedCollectionId
      activeView = 'request'
    })
  }

  async function refreshCollection() {
    if (!activeCollection) return
    await runAction('refresh collection', async () => {
      workspaceStore.appState = await RefreshCollection(activeCollection.id)
    })
  }

  async function resetDemoData() {
    await runAction('reset demo data', async () => {
      workspaceStore.appState = await ResetDemoData()
      workspaceStore.selectedEnvironmentId = activeCollection?.environments?.[0]?.id ?? ''
      activeView = 'request'
    })
  }

  async function deleteCookie(cookieId: string) {
    await runAction('delete cookie', async () => {
      workspaceStore.appState = await DeleteCookie(cookieId)
      if (cookieForm.id === cookieId) cookieForm = emptyCookieForm()
    })
  }

  async function clearCookies() {
    await runAction('clear cookies', async () => {
      workspaceStore.appState = await ClearCookies()
      cookieForm = emptyCookieForm()
    })
  }

  async function clearDomainCookies(domain: string) {
    await runAction('clear domain cookies', async () => {
      workspaceStore.appState = await ClearDomainCookies(domain)
      if (cookieForm.domain.toLowerCase() === domain.toLowerCase()) cookieForm = emptyCookieForm()
    })
  }

  async function saveCookieForm() {
    await runAction('save cookie', async () => {
      workspaceStore.appState = await SaveCookie(cookieForm as unknown as types.CookieInput)
      cookieForm = emptyCookieForm()
    })
  }

  async function importRawCookie() {
    await runAction('import cookie', async () => {
      workspaceStore.appState = await AddCookieFromHeader(rawCookieHeader, rawCookieURL)
    })
  }

  function editCookie(cookie: types.CookieEntry) {
    cookieForm = cookieFormFor(cookie)
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
      // Read the tab out of the mutation result rather than back out of appState:
      // it is the authoritative list the backend just produced, and it is
      // correct even on the path where applyNarrow had to refetch.
      const nextTab = result.openTabs?.find((tab) => tab.id === tabId)
      workspaceStore.selectedCollectionId = nextTab?.collectionId ?? selectedCollectionId
      activeView = 'request'
      if (nextTab?.kind === 'response-example') responseTab = 'examples'
    })
  }

  function activeOpenTabIndex() {
    return appState?.openTabs?.findIndex((tab) => tab.id === appState?.activeTabId) ?? -1
  }

  async function switchToOpenTabAt(index: number) {
    const tab = appState?.openTabs?.[index]
    if (!tab) return
    await setActiveTab(tab.id)
  }

  async function switchToRelativeOpenTab(offset: number) {
    const tabs = appState?.openTabs ?? []
    if (tabs.length === 0) return
    const activeIndex = activeOpenTabIndex()
    const currentIndex = activeIndex >= 0 ? activeIndex : 0
    const nextIndex = (currentIndex + offset + tabs.length) % tabs.length
    await setActiveTab(tabs[nextIndex].id)
  }

  function lifecycleRequests(): LifecycleRequest[] {
    const requests: LifecycleRequest[] = []
    for (const workspace of appState?.workspaces ?? []) {
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
      const target = targetTabID ? (appState?.openTabs ?? []).find((tab) => tab.id === targetTabID) : activeTab
      return target ? [target] : []
    }
    return appState?.openTabs ?? []
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
        workspaceStore.appState = await DiscardRequestDraft(request.collectionId, request.requestId)
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
        workspaceStore.appState = await SaveRequest(request.collectionId, request.requestId)
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
    if (!tabID || !(appState?.openTabs ?? []).some((tab) => tab.id === tabID)) return
    await runAction('close tab', async () => {
      const nextState = await CloseTab(tabID)
      workspaceStore.appState = nextState
      const nextTab = nextState.openTabs?.find((tab) => tab.id === nextState.activeTabId)
      workspaceStore.selectedCollectionId = nextTab?.collectionId ?? selectedCollectionId
      activeView = 'request'
      if (nextTab?.kind === 'response-example') responseTab = 'examples'
    })
  }

  function closeActiveTab() {
    beginTabLifecycleAction('close-active')
  }

  async function closeAllOpenTabsDirect() {
    await runAction('close all tabs', async () => {
      workspaceStore.appState = await CloseAllTabs()
      activeView = 'request'
    })
  }

  function closeAllOpenTabs() {
    beginTabLifecycleAction('close-all')
  }

  async function reopenLastClosedTab() {
    await runAction('reopen last closed tab', async () => {
      const nextState = await ReopenLastClosedTab(activeTab?.collectionId ?? '')
      workspaceStore.appState = nextState
      const nextTab = nextState.openTabs?.find((tab) => tab.id === nextState.activeTabId)
      workspaceStore.selectedCollectionId = nextTab?.collectionId ?? selectedCollectionId
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
      workspaceStore.appState = await OpenRequestTab(collectionId, itemId)
      workspaceStore.selectedCollectionId = collectionId
      activeView = 'request'
    })
  }

  function responseExampleIsActive(collectionId: string, itemId: string, example: types.ResponseExample) {
    if (!activeTab || activeTab.kind !== 'response-example') return false
    const target = activeTab.exampleId || activeTab.exampleName || ''
    return activeTab.collectionId === collectionId
      && activeTab.itemId === itemId
      && (target === responseExampleIdentifier(example) || target === example.name || activeTab.exampleName === example.name)
  }

  async function openResponseExampleTabFor(collectionId: string, itemId: string, example: types.ResponseExample) {
    await runAction('open response example', async () => {
      workspaceStore.appState = await OpenResponseExampleTab(collectionId, itemId, responseExampleIdentifier(example))
      workspaceStore.selectedCollectionId = collectionId
      activeView = 'request'
      responseTab = 'examples'
    })
  }

  async function openResponseExampleTab(example: types.ResponseExample) {
    if (!activeCollection || !activeRequest) return
    await openResponseExampleTabFor(activeCollection.id, activeRequest.id, example)
  }

  async function connectGitRemote() {
    if (!activeCollection) return
    await runAction('connect git remote', async () => {
      workspaceStore.appState = await ConnectCollectionGitRemote(activeCollection.id, gitRemoteURL)
      workspaceStore.selectedCollectionId = activeCollection.id
    })
  }

  async function disconnectGitRemote(collectionId = activeCollection?.id ?? '') {
    if (!collectionId) return
    await runAction('remove git remote', async () => {
      workspaceStore.appState = await DisconnectCollectionGitRemote(collectionId)
      if (selectedCollectionId === collectionId) workspaceStore.selectedCollectionId = ''
    })
  }

  function selectCollection(collectionId: string) {
    workspaceStore.selectedCollectionId = collectionId
    activeView = 'collection'
  }

  function selectFolderSettings(collection: types.Collection, folderPath: string) {
    workspaceStore.selectedCollectionId = collection.id
    selectedFolderPath = collection.folders?.find((folder) => folder.path === folderPath || folder.displayPath === folderPath)?.path ?? folderPath
    collectionTab = 'folders'
    activeView = 'collection'
  }

  function collectionDocsRequestIsExportable(item: types.RequestItem) {
    return !item.type || item.type === 'http' || item.type === 'graphql' || item.type === 'websocket' || item.type === 'grpc'
  }

  function collectionShareUnsupportedTypes(collection: types.Collection | undefined) {
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
      workspaceStore.appState = await RenameCollection(collectionID, renameCollectionDraft)
      workspaceStore.selectedCollectionId = collectionID
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
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = cloned?.id ?? sourceID
      selectedFolderPath = ''
      cancelCloneCollectionModal()
    })
  }

  function openNewFolderModal(parentPath = '', collection = activeCollection) {
    if (!collection || collection.notFoundLocally) return
    workspaceStore.selectedCollectionId = collection.id
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
    return isReservedRootDirectory(newFolderParentPath, newFolderDirectoryDraft)
  }

  function newFolderDirectoryNameIsValid() {
    return collectionFolderNameIsValid(newFolderDirectoryDraft) && !newFolderDirectoryIsReservedRoot()
  }

  function newFolderExpectedPath() {
    const directoryName = sanitizeCollectionFolderName(newFolderDirectoryDraft)
    return [newFolderParentPath, directoryName].filter(Boolean).join('/')
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

  function collectionRequestExtension(collection: types.Collection) {
    return collection.format === 'yml' || collection.format === 'yaml' ? '.yml' : '.bru'
  }

  function requestInfoFilename(collection: types.Collection, request: types.RequestItem) {
    const fileName = slashPathBase(request.filePath)
    if (fileName) return fileName
    return `${sanitizeCollectionFolderName(request.name || 'Request')}${collectionRequestExtension(collection)}`
  }

  function folderInfoFilename(folder: types.FolderConfig) {
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

  function openFolderInfoModal(collection: types.Collection, folderPath: string) {
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    workspaceStore.selectedCollectionId = collection.id
    itemInfoTarget = { kind: 'folder', collection, folder }
  }

  function openRequestInfoModal(collection: types.Collection, request: types.RequestItem) {
    workspaceStore.selectedCollectionId = collection.id
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
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = collectionID
      selectedFolderPath = createdFolder?.path ?? expectedPath
      collectionTab = 'folders'
      activeView = 'collection'
      cancelNewFolderModal()
    })
  }

  function openRenameFolderModal(collection: types.Collection, folderPath: string) {
    if (collection.notFoundLocally) return
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    workspaceStore.selectedCollectionId = collection.id
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
    return isReservedMetadataName(renameFolderDirectoryDraft)
  }

  function renameFolderDirectoryNameIsValid() {
    return filesystemNameIsValid(renameFolderDirectoryDraft)
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
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = collectionID
      selectedFolderPath = renamedFolder?.path ?? sourcePath
      collectionTab = 'folders'
      activeView = 'collection'
      cancelRenameFolderModal()
    })
  }

  function openCloneFolderModal(collection: types.Collection, folderPath: string) {
    if (collection.notFoundLocally) return
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    const sourceName = folder.name || slashPathBase(folder.displayPath || folder.path)
    const cloneName = `${sourceName} copy`
    workspaceStore.selectedCollectionId = collection.id
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
    return isReservedMetadataName(cloneFolderDirectoryDraft)
  }

  function cloneFolderDirectoryNameIsValid() {
    return filesystemNameIsValid(cloneFolderDirectoryDraft)
  }

  async function confirmCloneFolder() {
    if (!cloneFolderTarget || cloneFolderNameDraft.trim() === '' || !cloneFolderDirectoryNameIsValid()) return
    const collectionID = cloneFolderTarget.collection.id
    const sourcePath = cloneFolderTarget.folder.path
    await runAction('clone folder', async () => {
      const nextState = await CloneFolder(collectionID, sourcePath, cloneFolderNameDraft, cloneFolderDirectoryDraft)
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = collectionID
      cancelCloneFolderModal()
    })
  }

  function openCloneRequestModal(collection: types.Collection, request: types.RequestItem) {
    if (collection.notFoundLocally) return
    const sourceName = request.name || 'Request'
    workspaceStore.selectedCollectionId = collection.id
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
    return isReservedMetadataName(cloneRequestFilenameDraft)
  }

  function cloneRequestFilenameIsValid() {
    return filesystemNameIsValid(cloneRequestFilenameDraft)
  }

  async function confirmCloneRequest() {
    if (!cloneRequestTarget || cloneRequestNameDraft.trim() === '' || !cloneRequestFilenameIsValid()) return
    const collectionID = cloneRequestTarget.collection.id
    const requestID = cloneRequestTarget.request.id
    await runAction('clone request', async () => {
      const nextState = await CloneRequest(collectionID, requestID, cloneRequestNameDraft, cloneRequestFilenameDraft)
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = collectionID
      activeView = 'request'
      cancelCloneRequestModal()
    })
  }

  function requestFilesystemBaseName(request: types.RequestItem) {
    return requestFilesystemBaseNameOf(request.filePath, request.name)
  }

  function openRenameRequestModal(collection: types.Collection, request: types.RequestItem) {
    if (collection.notFoundLocally) return
    workspaceStore.selectedCollectionId = collection.id
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
    return isReservedMetadataName(renameRequestFilenameDraft)
  }

  function renameRequestFilenameIsValid() {
    return filesystemNameIsValid(renameRequestFilenameDraft)
  }

  async function confirmRenameRequest() {
    if (!renameRequestTarget || renameRequestNameDraft.trim() === '' || !renameRequestFilenameIsValid()) return
    const collectionID = renameRequestTarget.collection.id
    const requestID = renameRequestTarget.request.id
    await runAction('rename request', async () => {
      const nextState = await RenameRequest(collectionID, requestID, renameRequestNameDraft, renameRequestFilenameDraft)
      workspaceStore.appState = nextState
      workspaceStore.selectedCollectionId = collectionID
      activeView = 'request'
      cancelRenameRequestModal()
    })
  }

  function openDeleteRequestModal(collection: types.Collection, request: types.RequestItem) {
    if (collection.notFoundLocally) return
    workspaceStore.selectedCollectionId = collection.id
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
        workspaceStore.appState = await DiscardRequestDraft(collectionID, requestID)
      } else {
        const result = await DeleteRequestRecoverable(collectionID, requestID)
        workspaceStore.appState = result.state
        await refreshRecoveryEntries()
      }
      workspaceStore.selectedCollectionId = collectionID
      activeView = 'request'
      cancelDeleteRequestModal()
    })
  }

  function openDeleteFolderModal(collection: types.Collection, folderPath: string) {
    if (collection.notFoundLocally) return
    const folder = collection.folders?.find((candidate) => candidate.path === folderPath || candidate.displayPath === folderPath)
    if (!folder) return
    workspaceStore.selectedCollectionId = collection.id
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
      workspaceStore.appState = nextState
      await refreshRecoveryEntries()
      workspaceStore.selectedCollectionId = collectionID
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
      workspaceStore.appState = result.state
      await refreshRecoveryEntries()
      if (selectedCollectionId === collectionID) workspaceStore.selectedCollectionId = ''
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

  function downloadCollectionExport(result: types.CollectionExportResult) {
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
      } as types.CollectionExportOptions)
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
      } as types.GenerateCollectionDocsOptions)
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

  function applyGitWorkbenchSnapshot(snapshot: gitworkbench.CollectionGitSnapshot) {
		const previousBranch = gitWorkbenchSnapshot?.branch
    gitWorkbenchSnapshot = snapshot
		if (!snapshot.available) gitNotFoundMessage = 'Git is not installed or not on PATH.'
    gitWorkbenchSelectedPaths = reconcileGitSelection(gitWorkbenchSelectedPaths, snapshot.files ?? [])
    gitWorkbenchBranch = reconcileGitBranch(gitWorkbenchBranch, snapshot.branches, snapshot.branch)
		gitWorkbenchRemoteBranch = reconcileGitRemoteBranch(gitWorkbenchRemoteBranch, previousBranch, snapshot.branch)
    const remote = reconcileGitRemoteSelection(gitWorkbenchRemoteName, snapshot.remotes)
    gitWorkbenchRemoteName = remote.name
    gitWorkbenchRemoteURL = remote.url
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

  async function runGitWorkbenchAction(label: string, action: () => Promise<gitworkbench.CollectionGitOperationResult>) {
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
      workspaceStore.appState = await OpenGitCollections(activeWorkspace.id, selectedGitCollectionPaths, gitCloneURL)
      workspaceStore.selectedCollectionId = appState?.workspaces
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
  // `appState.revision` is read directly rather than through a `$:` derivation
  // because a reactive statement does not run until Svelte's next update tick,
  // so two mutations dispatched inside one tick would both compare against the
  // same stale revision — making the second look like a gap and refetch for
  // nothing.
  async function applyNarrow(merge: (current: types.AppState, held: number) => MergeOutcome): Promise<void> {
    if (!appState) {
      // Nothing to patch onto. Only reachable before the boot fetch has landed.
      workspaceStore.appState = await GetState()
      return
    }
    const outcome = merge(appState, appState.revision ?? 0)
    if (outcome.kind === 'applied') {
      workspaceStore.appState = outcome.state
      return
    }
    // Not silent. A refetch means something mutated appState behind our back, and
    // while recovery is automatic, a run of these is a real signal that some
    // mutator still needs migrating.
    console.warn(`[US-014] refetching full appState: ${outcome.reason}`)
    workspaceStore.appState = await GetState()
  }

  // US-035. The keystroke path. US-014 made each round trip 511x smaller
  // (4,348,018 -> 8,515 bytes on the 500-request fixture); this stops most of
  // them happening at all, by coalescing a burst of typing into one call.
  //
  // The backend call is deferred, but the UI is NOT: applyOptimisticPatch below
  // updates local appState synchronously, so the input never lags behind the
  // keyboard. The authoritative result overwrites it when the flush lands.
  const requestPatchCoalescer = new PatchCoalescer<types.RequestPatch>(
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
  function applyOptimisticPatch(collectionId: string, itemId: string, patch: types.RequestPatch) {
    if (!appState) return
    workspaceStore.appState = withOptimisticPatch(appState, collectionId, itemId, patch)
  }

  function patchRequest(patch: types.RequestPatch) {
    if (!activeCollection || !activeRequest) return
    const collectionId = activeCollection.id
    const requestId = activeRequest.id
    applyOptimisticPatch(collectionId, requestId, patch)
    void requestPatchCoalescer.queue({ collectionId, itemId: requestId }, patch)
  }

  // Every path that reads server-side request appState has to drain the queue
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
    } as unknown as types.RequestPatch)
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
    const first = messages[0] ?? ({ name: 'message 1', content: '{}' } as types.GrpcMessage)
    const content = method?.template || first.content || '{}'
    messages[0] = { ...first, name: first.name || 'message 1', content } as types.GrpcMessage
    await patchRequest({
      method: path,
      grpcMethodType: method?.type ?? activeRequest.grpcMethodType,
      grpcMessages: messages
    } as types.RequestPatch)
  }

  async function regenerateGrpcMessage(index: number) {
    if (!activeCollection || !activeRequest) return
    const methodPath = activeRequest.method === 'CALL' ? '' : activeRequest.method
    if (!methodPath) return
    await runAction('generate gRPC message', async () => {
      const content = await GenerateGRPCMessage(activeCollection.id, activeRequest.id, selectedEnvironmentId, methodPath)
      const rows = [...(activeRequest.grpcMessages ?? [])]
      const current = rows[index] ?? ({ name: `message ${index + 1}`, content: '{}' } as types.GrpcMessage)
      rows[index] = { ...current, name: current.name || `message ${index + 1}`, content } as types.GrpcMessage
      await patchRequest({ grpcMessages: rows } as types.RequestPatch)
    })
  }

  function patchField(field: keyof types.RequestPatch, value: unknown) {
    patchRequest({ [field]: value } as unknown as types.RequestPatch)
  }

	  function updateBody(updates: Partial<types.RequestBody>) {
	    if (!activeRequest) return
	    patchRequest({ body: { ...activeRequest.body, ...updates } } as types.RequestPatch)
	  }

	  function updateFormUrlEncodedRow(index: number, field: keyof types.KeyValue, value: string | boolean) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.formUrlEncoded ?? [])]
	    const current = rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue)
	    rows[index] = { ...current, [field]: value } as types.KeyValue
	    updateBody({ formUrlEncoded: rows } as Partial<types.RequestBody>)
	  }

	  function addFormUrlEncodedRow() {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.formUrlEncoded ?? []), { name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue]
	    updateBody({ formUrlEncoded: rows } as Partial<types.RequestBody>)
	  }

	  function removeFormUrlEncodedRow(index: number) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.formUrlEncoded ?? [])]
	    rows.splice(index, 1)
	    updateBody({ formUrlEncoded: rows } as Partial<types.RequestBody>)
	  }

	  function updateMultipartRow(index: number, field: keyof types.FormPart, value: string | boolean) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.multipart ?? [])]
	    const current = rows[index] ?? ({ name: '', value: '', filePath: '', contentType: '', enabled: true } as types.FormPart)
	    rows[index] = { ...current, [field]: value } as types.FormPart
	    updateBody({ multipart: rows } as Partial<types.RequestBody>)
	  }

	  function addMultipartRow() {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.multipart ?? []), { name: '', value: '', filePath: '', contentType: '', enabled: true } as types.FormPart]
	    updateBody({ multipart: rows } as Partial<types.RequestBody>)
	  }

	  function removeMultipartRow(index: number) {
	    if (!activeRequest) return
	    const rows = [...(activeRequest.body.multipart ?? [])]
	    rows.splice(index, 1)
	    updateBody({ multipart: rows } as Partial<types.RequestBody>)
	  }



	  function fileBodyUpdate(rows: types.FileBodyEntry[]) {
	    const selected = rows.find((row) => row.selected) ?? rows[0]
	    updateBody({
	      files: rows,
	      filePath: selected?.filePath ?? '',
	      fileContentType: selected?.contentType ?? ''
	    } as Partial<types.RequestBody>)
	  }

	  function updateFileBodyRow(index: number, field: keyof types.FileBodyEntry, value: string | boolean) {
	    if (!activeRequest) return
	    const rows = fileBodyRows(activeRequest.body)
	    const current = rows[index] ?? ({ filePath: '', contentType: '', selected: rows.length === 0 } as types.FileBodyEntry)
	    rows[index] = { ...current, [field]: value } as types.FileBodyEntry
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
	    rows.push({ filePath: '', contentType: '', selected: rows.length === 0 } as types.FileBodyEntry)
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

  function updateAuth(updates: Partial<types.AuthConfig>) {
    if (!activeRequest) return
    patchRequest({ auth: authWithOAuth2Defaults(activeRequest.auth, updates) } as types.RequestPatch)
  }

  function updateAWSV4Auth(updates: Partial<types.AWSV4Auth>) {
    if (!activeRequest) return
    updateAuth({ awsv4: { ...(activeRequest.auth.awsv4 ?? {}), ...updates } as types.AWSV4Auth })
  }

  function updateOAuth1Auth(updates: Partial<types.OAuth1Auth>) {
    if (!activeRequest) return
    updateAuth({ oauth1: { ...(activeRequest.auth.oauth1 ?? {}), ...updates } as types.OAuth1Auth })
  }

  function updateOAuth2Auth(updates: Partial<types.OAuth2Auth>) {
    if (!activeRequest) return
    updateAuth({ oauth2: { ...(activeRequest.auth.oauth2 ?? {}), ...updates } as types.OAuth2Auth })
  }

  function oauth2ParamList(auth: types.OAuth2Auth | undefined, bucket: OAuth2ParamBucket) {
    return [...(((auth ?? {}) as Record<OAuth2ParamBucket, types.OAuth2AdditionalParam[] | undefined>)[bucket] ?? [])]
  }

  function updateOAuth2ParamList(auth: types.OAuth2Auth | undefined, bucket: OAuth2ParamBucket, index: number, sendIn: OAuth2ParamSendIn, field: OAuth2ParamField, value: string | boolean) {
    const rows = oauth2ParamList(auth, bucket)
    const current = rows[index] ?? ({ name: '', value: '', sendIn, enabled: true, secret: false, description: '' } as types.OAuth2AdditionalParam)
    rows[index] = { ...current, sendIn: current.sendIn || sendIn, enabled: current.enabled ?? true, [field]: value } as types.OAuth2AdditionalParam
    return rows
  }

  function addOAuth2ParamList(auth: types.OAuth2Auth | undefined, bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn) {
    return [...oauth2ParamList(auth, bucket), { name: '', value: '', sendIn, enabled: true, secret: false, description: '' } as types.OAuth2AdditionalParam]
  }

  function removeOAuth2ParamList(auth: types.OAuth2Auth | undefined, bucket: OAuth2ParamBucket, index: number) {
    const rows = oauth2ParamList(auth, bucket)
    rows.splice(index, 1)
    return rows
  }

  function updateRequestOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn, index: number, field: OAuth2ParamField, value: string | boolean) {
    if (!activeRequest) return
    updateOAuth2Auth({ [bucket]: updateOAuth2ParamList(activeRequest.auth.oauth2, bucket, index, sendIn, field, value) } as Partial<types.OAuth2Auth>)
  }

  function addRequestOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn) {
    if (!activeRequest) return
    updateOAuth2Auth({ [bucket]: addOAuth2ParamList(activeRequest.auth.oauth2, bucket, sendIn) } as Partial<types.OAuth2Auth>)
  }

  function removeRequestOAuth2AdditionalParam(bucket: OAuth2ParamBucket, index: number) {
    if (!activeRequest) return
    updateOAuth2Auth({ [bucket]: removeOAuth2ParamList(activeRequest.auth.oauth2, bucket, index) } as Partial<types.OAuth2Auth>)
  }

  function updateSettings(updates: Partial<types.RequestSettings>) {
    if (!activeRequest) return
    patchRequest({ settings: { ...activeRequest.settings, ...updates } } as types.RequestPatch)
  }

  function updateKeyValue(kind: 'params' | 'pathParams' | 'headers', index: number, field: keyof types.KeyValue, value: string | boolean) {
    if (!activeRequest) return
    const rows = [...(activeRequest[kind] ?? [])]
    rows[index] = { ...rows[index], [field]: value }
    patchRequest({ [kind]: rows } as unknown as types.RequestPatch)
  }

  // US-056. Bulk edit replaces the whole list in one patch rather than
  // diffing row by row: the text form has no stable row identity, so a
  // per-row diff would have to guess which line replaced which row and would
  // reorder or drop rows on any edit that changes their count.
  function replaceKeyValues(kind: 'params' | 'pathParams' | 'headers', rows: types.KeyValue[]) {
    if (!activeRequest) return
    patchRequest({ [kind]: rows } as unknown as types.RequestPatch)
  }

  function addKeyValue(kind: 'params' | 'headers') {
    if (!activeRequest) return
    const rows = [...(activeRequest[kind] ?? []), { name: '', value: '', enabled: true, secret: false, description: '' }]
    patchRequest({ [kind]: rows } as unknown as types.RequestPatch)
  }

  function removeKeyValue(kind: 'params' | 'headers', index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest[kind] ?? [])]
    rows.splice(index, 1)
    patchRequest({ [kind]: rows } as unknown as types.RequestPatch)
  }

  function updateGrpcMessage(index: number, field: keyof types.GrpcMessage, value: string) {
    if (!activeRequest) return
    const rows = [...(activeRequest.grpcMessages ?? [])]
    const current = rows[index] ?? ({ name: '', content: '' } as types.GrpcMessage)
    rows[index] = { ...current, [field]: value }
    patchRequest({ grpcMessages: rows } as types.RequestPatch)
  }

  function addGrpcMessage() {
    if (!activeRequest) return
    const nextIndex = (activeRequest.grpcMessages?.length ?? 0) + 1
    const rows = [...(activeRequest.grpcMessages ?? []), { name: `message ${nextIndex}`, content: '{}' } as types.GrpcMessage]
    patchRequest({ grpcMessages: rows } as types.RequestPatch)
  }

  function removeGrpcMessage(index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest.grpcMessages ?? [])]
    rows.splice(index, 1)
    patchRequest({ grpcMessages: rows } as types.RequestPatch)
  }

  function updateWSMessage(index: number, field: keyof types.WSMessage, value: string | boolean) {
    if (!activeRequest) return
    const rows = [...(activeRequest.wsMessages ?? [])]
    const current = rows[index] ?? ({ name: `message ${index + 1}`, type: 'text', content: '', selected: false } as types.WSMessage)
    rows[index] = { ...current, [field]: value } as types.WSMessage
    patchRequest({ wsMessages: rows } as types.RequestPatch)
  }

  function addWSMessage() {
    if (!activeRequest) return
    const nextIndex = (activeRequest.wsMessages?.length ?? 0) + 1
    const rows = [...(activeRequest.wsMessages ?? []), { name: `message ${nextIndex}`, type: 'json', content: '{}', selected: true } as types.WSMessage]
    patchRequest({ wsMessages: rows } as types.RequestPatch)
  }

  function removeWSMessage(index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest.wsMessages ?? [])]
    rows.splice(index, 1)
    patchRequest({ wsMessages: rows } as types.RequestPatch)
  }

  function updateAssertion(index: number, field: keyof types.Assertion, value: string | boolean) {
    if (!activeRequest) return
    const rows = [...(activeRequest.assertions ?? [])]
    rows[index] = { ...rows[index], [field]: value }
    patchRequest({ assertions: rows } as types.RequestPatch)
  }

  function addAssertion() {
    if (!activeRequest) return
    const rows = [...(activeRequest.assertions ?? []), { expression: 'res.status', operator: 'equals', value: '200', enabled: true, passed: false, message: '' }]
    patchRequest({ assertions: rows } as types.RequestPatch)
  }

  function removeAssertion(index: number) {
    if (!activeRequest) return
    const rows = [...(activeRequest.assertions ?? [])]
    rows.splice(index, 1)
    patchRequest({ assertions: rows } as types.RequestPatch)
  }

  function updateRequestVariable(index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) {
    if (!activeRequest) return
    const req = [...(activeRequest.vars?.req ?? [])]
    req[index] = { ...req[index], [field]: value }
    patchRequest({ vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as types.RequestPatch)
  }

  function addRequestVariable() {
    if (!activeRequest) return
    const req = [
      ...(activeRequest.vars?.req ?? []),
      { id: `req-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: false }
    ]
    patchRequest({ vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as types.RequestPatch)
  }

  function removeRequestVariable(index: number) {
    if (!activeRequest) return
    const req = [...(activeRequest.vars?.req ?? [])]
    req.splice(index, 1)
    patchRequest({ vars: { ...(activeRequest.vars ?? { req: [], res: [] }), req } } as unknown as types.RequestPatch)
  }

  async function updateCollectionVariable(index: number, field: keyof types.Variable, value: string | boolean) {
    if (!activeCollection) return
    const vars = [...(activeCollection.variables ?? [])]
    vars[index] = { ...vars[index], [field]: value }
    workspaceStore.appState = await UpdateCollectionVariables(activeCollection.id, vars)
  }

  async function addCollectionVariable() {
    if (!activeCollection) return
    const vars = [...(activeCollection.variables ?? []), { id: `ui-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: false }]
    workspaceStore.appState = await UpdateCollectionVariables(activeCollection.id, vars)
  }

  async function updateEnvironmentVariable(index: number, field: keyof types.Variable, value: string | boolean) {
    if (!activeCollection || !selectedEnvironment) return
    const vars = [...(selectedEnvironment.variables ?? [])]
    vars[index] = field === 'dataType' ? { ...vars[index], dataType: String(value), type: String(value) } : { ...vars[index], [field]: value }
    workspaceStore.appState = await UpdateEnvironmentVariables(activeCollection.id, selectedEnvironment.id, vars)
  }

  async function addEnvironmentVariable() {
    if (!activeCollection || !selectedEnvironment) return
    const vars = [
      ...(selectedEnvironment.variables ?? []),
      { id: `ui-env-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: environmentVariableTab === 'secrets' }
    ]
    workspaceStore.appState = await UpdateEnvironmentVariables(activeCollection.id, selectedEnvironment.id, vars)
  }

  async function removeEnvironmentVariable(index: number) {
    if (!activeCollection || !selectedEnvironment) return
    const vars = [...(selectedEnvironment.variables ?? [])]
    vars.splice(index, 1)
    workspaceStore.appState = await UpdateEnvironmentVariables(activeCollection.id, selectedEnvironment.id, vars)
  }

  async function updateGlobalEnvironmentVariable(index: number, field: keyof types.Variable, value: string | boolean) {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const vars = [...(selectedGlobalEnvironment.variables ?? [])]
    vars[index] = field === 'dataType' ? { ...vars[index], dataType: String(value), type: String(value) } : { ...vars[index], [field]: value }
    workspaceStore.appState = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, selectedGlobalEnvironment.id, vars)
  }

  async function addGlobalEnvironmentVariable() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const vars = [
      ...(selectedGlobalEnvironment.variables ?? []),
      { id: `ui-global-env-var-${Date.now()}`, name: '', value: '', type: 'text', dataType: 'string', enabled: true, secret: globalEnvironmentVariableTab === 'secrets' }
    ]
    workspaceStore.appState = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, selectedGlobalEnvironment.id, vars)
  }

  async function removeGlobalEnvironmentVariable(index: number) {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    const vars = [...(selectedGlobalEnvironment.variables ?? [])]
    vars.splice(index, 1)
    workspaceStore.appState = await UpdateGlobalEnvironmentVariables(activeWorkspace.id, selectedGlobalEnvironment.id, vars)
  }

  async function deleteGlobalEnvironment() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    await runAction('delete global environment', async () => {
      workspaceStore.appState = await DeleteGlobalEnvironment(activeWorkspace.id, selectedGlobalEnvironment.id)
    })
  }

  async function copyGlobalEnvironment() {
    if (!activeWorkspace || !selectedGlobalEnvironment) return
    await runAction('copy global environment', async () => {
      const copyName = globalEnvironmentCopyName.trim()
      workspaceStore.appState = copyName
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
      workspaceStore.appState = await ImportGlobalEnvironment(activeWorkspace.id, globalEnvironmentPayload)
    })
  }

  async function updateCollectionHeader(index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) {
    if (!activeCollection) return
    const headers = [...(activeCollection.headers ?? [])]
    headers[index] = { ...headers[index], [field]: value }
    workspaceStore.appState = await UpdateCollectionHeaders(activeCollection.id, headers)
  }

  async function addCollectionHeader() {
    if (!activeCollection) return
    const headers = [...(activeCollection.headers ?? []), { name: '', value: '', enabled: true, secret: false, description: '' }]
    workspaceStore.appState = await UpdateCollectionHeaders(activeCollection.id, headers)
  }

  async function removeCollectionHeader(index: number) {
    if (!activeCollection) return
    const headers = [...(activeCollection.headers ?? [])]
    headers.splice(index, 1)
    workspaceStore.appState = await UpdateCollectionHeaders(activeCollection.id, headers)
  }

  async function updateCollectionAuth(updates: Partial<types.AuthConfig>) {
    if (!activeCollection) return
    workspaceStore.appState = await UpdateCollectionAuth(activeCollection.id, authWithOAuth2Defaults(activeCollection.auth, updates))
  }


  function normalizedCollectionProxy(overrides: Partial<types.ProxyConfig> = {}) {
    const current = activeCollection?.proxy ?? ({} as types.ProxyConfig)
    return collectionProxyWithDefaults(current, isProxyConfigUnset(current), overrides)
  }


  async function updateCollectionProxy(updates: Partial<types.ProxyConfig>) {
    if (!activeCollection) return
    workspaceStore.appState = await UpdateCollectionProxy(activeCollection.id, normalizedCollectionProxy(updates))
  }

  async function updateCollectionProxyMode(mode: string) {
    await updateCollectionProxy(proxyModeOverrides(mode))
  }

  function collectionSandboxMode(collection: types.Collection | undefined = activeCollection): JSSandboxMode {
    return collection?.securityConfig?.jsSandboxMode === 'developer' ? 'developer' : 'safe'
  }

  async function updateCollectionSandboxMode(mode: JSSandboxMode) {
    if (!activeCollection || collectionSandboxMode(activeCollection) === mode) return
    workspaceStore.appState = await UpdateCollectionSecurityConfig(activeCollection.id, { jsSandboxMode: mode } as types.CollectionSecurityConfig)
  }

  async function updateCollectionProxyAuth(updates: Partial<types.ProxyAuthConfig>) {
    if (!activeCollection) return
    await updateCollectionProxy({ auth: { ...(activeCollection.proxy?.auth ?? {}), ...updates } as types.ProxyAuthConfig })
  }


  function proxyPreferencesWithDefaults(overrides: Partial<types.ProxyPreferences> = {}) {
    return proxyPreferencesWithDefaultsOf(
      appState?.preferences?.proxy,
      appState?.preferences?.proxyMode,
      overrides
    )
  }

  async function updatePreferencesProxy(updates: Partial<types.ProxyPreferences>) {
    if (!appState) return
    const proxy = proxyPreferencesWithDefaults(updates)
    const preferences = {
      ...appState.preferences,
      proxy,
      proxyMode: preferenceProxyModeValue(proxy)
    } as types.Preferences
    workspaceStore.appState = await UpdatePreferences(preferences)
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

  async function updatePreferencesProxyConfig(updates: Partial<types.ProxyConfig>) {
    const current = appState?.preferences?.proxy?.config
    await updatePreferencesProxy({ config: proxyConfigWithDefaults(current, updates) })
  }

  async function updatePreferencesProxyAuth(updates: Partial<types.ProxyAuthConfig>) {
    const current = appState?.preferences?.proxy?.config
    await updatePreferencesProxyConfig({ auth: { ...(current?.auth ?? {}), ...updates } as types.ProxyAuthConfig })
  }

  async function updateAppearancePreferences(updates: Partial<types.Preferences>) {
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      ...updates
    } as types.Preferences)
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
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      layout: {
        ...(appState.preferences.layout ?? {}),
        responsePaneOrientation: orientation
      } as types.LayoutPreferences
    } as types.Preferences)
  }

  async function toggleResponsePaneOrientation() {
    await setResponsePaneOrientation(responsePaneOrientation === 'horizontal' ? 'vertical' : 'horizontal')
  }

  async function setZoomPercentage(percentage: number) {
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      display: {
        ...(appState.preferences.display ?? {}),
        zoomPercentage: normalizedZoomPercentage(percentage)
      } as types.DisplayPreferences
    } as types.Preferences)
  }

  async function incrementZoomPercentage(delta: number) {
    await setZoomPercentage(appZoomPercentage + delta)
  }

  async function resetZoomPercentage() {
    await setZoomPercentage(zoomDefaultPercentage)
  }

  async function updateFontPreferences(updates: Partial<types.FontPreferences>) {
    if (!appState) return
    const nextFont = {
      ...(appState.preferences.font ?? {}),
      ...updates
    } as types.FontPreferences
    const nextSize = normalizedCodeFontSize(nextFont.codeFontSize ?? appState.preferences.codeFontSize)
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      font: {
        ...nextFont,
        codeFont: normalizedCodeFont(nextFont.codeFont),
        codeFontSize: nextSize
      } as types.FontPreferences,
      codeFontSize: nextSize
    } as types.Preferences)
  }

  async function updateCodeFont(value: string) {
    await updateFontPreferences({ codeFont: value })
  }

  async function updateCodeFontSize(value: number) {
    await updateFontPreferences({ codeFontSize: normalizedCodeFontSize(value) })
  }

  async function updateRequestPreferences(updates: Partial<types.RequestPreferences>) {
    requestPreferencesSaveQueue = requestPreferencesSaveQueue.catch(() => {}).then(async () => {
      if (!appState) return
      const current = appState.preferences.request ?? ({} as types.RequestPreferences)
      const next = {
        sslVerification: updates.sslVerification ?? (current.sslVerification !== false),
        customCaCertificate: {
          enabled: current.customCaCertificate?.enabled ?? false,
          filePath: current.customCaCertificate?.filePath ?? '',
          ...(updates.customCaCertificate ?? {})
        } as types.CustomCaCertificatePreferences,
        keepDefaultCaCertificates: {
          enabled: current.keepDefaultCaCertificates?.enabled !== false,
          ...(updates.keepDefaultCaCertificates ?? {})
        } as types.KeepDefaultCaCertificatesPreferences,
        storeCookies: updates.storeCookies ?? current.storeCookies ?? appState.preferences.storeCookies ?? true,
        sendCookies: updates.sendCookies ?? current.sendCookies ?? true,
        timeout: normalizedRequestTimeout(updates.timeout ?? current.timeout)
      } as types.RequestPreferences
      workspaceStore.appState = await UpdatePreferences({
        ...appState.preferences,
        request: next,
        storeCookies: next.storeCookies ?? true
      } as types.Preferences)
    })
    await requestPreferencesSaveQueue
  }

  async function browseCustomCaCertificate() {
    if (!appState) return
    const filePath = await SelectCustomCaCertificate()
    if (!filePath) return
    await updateRequestPreferences({
      customCaCertificate: {
        ...(appState.preferences.request?.customCaCertificate ?? {}),
        enabled: true,
        filePath
      } as types.CustomCaCertificatePreferences
    })
  }

  async function clearCustomCaCertificate() {
    await updateRequestPreferences({
      customCaCertificate: {
        ...(appState?.preferences?.request?.customCaCertificate ?? {}),
        filePath: ''
      } as types.CustomCaCertificatePreferences
    })
  }

  async function updateGeneralPreferences(updates: Partial<types.GeneralPreferences>) {
    if (!appState) return
    const next = {
      ...(appState.preferences.general ?? {}),
      ...updates
    } as types.GeneralPreferences
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      general: next,
      defaultCollectionPath: next.defaultLocation ?? ''
    } as types.Preferences)
  }

  async function browseDefaultLocation() {
    const location = await SelectDefaultLocation()
    if (!location) return
    await updateGeneralPreferences({ defaultLocation: location })
  }

  async function clearDefaultLocation() {
    await updateGeneralPreferences({ defaultLocation: '' })
  }

  async function updateAutoSavePreferences(updates: Partial<types.AutoSavePreferences>) {
    if (!appState) return
    const current = appState.preferences.autoSave ?? ({} as types.AutoSavePreferences)
    const next = {
      enabled: updates.enabled ?? current.enabled ?? appState.preferences.autosave ?? false,
      interval: normalizedAutoSaveInterval(updates.interval ?? current.interval)
    } as types.AutoSavePreferences
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      autoSave: next,
      autosave: next.enabled
    } as types.Preferences)
    if (!next.enabled) {
      clearAutoSaveTimer()
    } else if (activeCollection && activeRequest?.draft) {
      scheduleRequestAutoSave(activeCollection.id, activeRequest.id)
    }
  }

  async function updateSSLSessionCache(enabled: boolean) {
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      cache: {
        ...(appState.preferences.cache ?? {}),
        sslSession: { enabled } as types.SSLSessionCachePreferences
      } as types.CachePreferences
    } as types.Preferences)
    if (!enabled) {
      workspaceStore.appState = await ClearSSLSessionCache()
    }
  }

  async function clearSSLSessionCache() {
    workspaceStore.appState = await ClearSSLSessionCache()
  }

  async function refreshFileCacheSize() {
    try {
      fileCacheSize = await GetFileCacheSize()
    } catch {
      fileCacheSize = undefined
    }
  }

  async function updateFileCache(enabled: boolean) {
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      cache: {
        ...(appState.preferences.cache ?? {}),
        file: { enabled } as types.FileCachePreferences
      } as types.CachePreferences
    } as types.Preferences)
    await refreshFileCacheSize()
  }

  async function clearFileCache() {
    fileCacheSize = await ClearFileCache()
  }

  function autoSaveIsEnabled() {
    return appState?.preferences?.autoSave?.enabled ?? appState?.preferences?.autosave ?? false
  }

  function autoSaveDelay() {
    return normalizedAutoSaveInterval(appState?.preferences?.autoSave?.interval)
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
        workspaceStore.appState = await SaveRequest(target.collectionId, target.requestId)
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

  function persistWorkbenchLayout() {
    writeWorkbenchLayout(workbenchStorageScope, { sidebarWidth, responseSplit })
  }

  function restoreWorkbenchLayout() {
    const restored = readWorkbenchLayout(workbenchStorageScope, { sidebarWidth, responseSplit })
    sidebarWidth = restored.sidebarWidth
    responseSplit = restored.responseSplit
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
      responseSplit = splitFractionAt(bounds, next, isVertical, responseSplit)
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
      case 'open-history':
        activeView = 'history'
        await refreshHistory()
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
  const commandPaletteActionsByID = Object.fromEntries(commandPaletteActions.map((action) => [action.id, action])) as Record<string, typeof commandPaletteActions[number]>

  // US-055. Ranked rather than a bare substring filter. `includes` cannot tell
  // an exact title from an incidental containment, so typing "send request"
  // left the ordering to however the command list happened to be declared —
  // and the first row is what Enter runs.
  const visibleCommandPaletteActions = $derived(filterCommands(
    commandPaletteActions.map((action) => ({ id: action.id, title: action.label, section: 'Commands', shortcut: action.shortcut })),
    commandPaletteQuery
  ).map((match) => commandPaletteActionsByID[match.command.id]))
  $effect(() => {
  if (commandPaletteActiveIndex >= visibleCommandPaletteActions.length) commandPaletteActiveIndex = Math.max(0, visibleCommandPaletteActions.length - 1)
  })

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


  async function updateKeyBindingPreset(preset: string) {
    await updateAppearancePreferences({ keyBindingPreset: normalizeKeyBindingPreset(preset) })
  }

  async function updateKeybindingsEnabled(enabled: boolean) {
    await updateAppearancePreferences({ keybindingsEnabled: enabled })
  }

  async function updateKeyBinding(action: string, combo: string) {
    if (!appState) return
    const os = currentKeyBindingOS()
    const binding = mergedKeyBinding(action)
    if (!binding) return
    const nextBindings = {
      ...(appState.preferences.keyBindings ?? {}),
      [action]: {
        ...(appState.preferences.keyBindings?.[action] ?? {}),
        name: binding.name,
        [os]: keyBindingSignature(combo)
      } as types.KeyBinding
    }
    await updateAppearancePreferences({ keyBindings: nextBindings })
  }

  async function resetKeyBinding(action: string) {
    if (!appState) return
    const nextBindings = { ...(appState.preferences.keyBindings ?? {}) }
    delete nextBindings[action]
    await updateAppearancePreferences({ keyBindings: nextBindings })
  }

  async function resetAllKeyBindings() {
    await updateAppearancePreferences({ keyBindings: {} })
  }

  function normalizedDevToolsTab(value: string | undefined): DevToolsTab {
    return normalizedTabID(value, devToolsTabs, 'console')
  }

  function applyDevToolsShellPreferences(preferences: types.DevToolsPreferences | undefined) {
    devToolsOpen = preferences?.open ?? false
    devToolsTab = normalizedDevToolsTab(preferences?.activeTab)
    devToolsDrawerHeight = normalizedDevToolsDrawerHeight(preferences?.drawerHeight)
    devToolsDetailsPanelWidth = normalizedDevToolsDetailsPanelWidth(preferences?.detailsPanelWidth)
  }

  async function updateDevToolsShellPreferences(updates: Partial<types.DevToolsPreferences>) {
    const next = {
      ...(appState?.preferences?.devTools ?? {}),
      ...updates,
      activeTab: normalizedDevToolsTab(updates.activeTab ?? appState?.preferences?.devTools?.activeTab ?? devToolsTab),
      drawerHeight: normalizedDevToolsDrawerHeight(updates.drawerHeight ?? appState?.preferences?.devTools?.drawerHeight ?? devToolsDrawerHeight),
      detailsPanelWidth: normalizedDevToolsDetailsPanelWidth(updates.detailsPanelWidth ?? appState?.preferences?.devTools?.detailsPanelWidth ?? devToolsDetailsPanelWidth),
      network: appState?.preferences?.devTools?.network ?? devToolsNetworkPreferencePayload(devToolsNetworkSortKey, devToolsNetworkSortDirection, devToolsNetworkColumnWidths)
    } as types.DevToolsPreferences
    devToolsOpen = next.open ?? false
    devToolsTab = normalizedDevToolsTab(next.activeTab)
    devToolsDrawerHeight = normalizedDevToolsDrawerHeight(next.drawerHeight)
    devToolsDetailsPanelWidth = normalizedDevToolsDetailsPanelWidth(next.detailsPanelWidth)
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      devTools: next
    } as types.Preferences)
  }

  async function recordKeyBinding(action: string, event: KeyboardEvent) {
    if (!keyBindingCanEdit(action) || !keybindingsAreEnabled(appState?.preferences)) return
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
    if (!keyBindingCanEdit(action) || !keybindingsAreEnabled(appState?.preferences)) return
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

  async function updateCollectionClientCertificate(index: number, field: keyof types.ClientCertificateConfig, value: string) {
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
    workspaceStore.appState = await UpdateCollectionClientCertificates(activeCollection.id, rows)
  }

  async function updateCollectionPresets(updates: Partial<types.CollectionPresets>) {
    if (!activeCollection) return
    workspaceStore.appState = await UpdateCollectionPresets(activeCollection.id, { ...(activeCollection.presets ?? {}), ...updates } as types.CollectionPresets)
  }

  async function updateCollectionProtobuf(protobuf: types.CollectionProtobufConfig) {
    if (!activeCollection) return
    workspaceStore.appState = await UpdateCollectionProtobuf(activeCollection.id, protobuf)
  }

  function collectionProtobufConfig() {
    return activeCollection?.protobuf ?? ({ protoFiles: [], importPaths: [] } as unknown as types.CollectionProtobufConfig)
  }

  function asCollectionProtobufConfig(value: unknown) {
    return value as types.CollectionProtobufConfig
  }

  async function addCollectionClientCertificate() {
    if (!activeCollection) return
    const rows = [
      ...(activeCollection.clientCertificates ?? []),
      { domain: '', type: 'cert', certFilePath: '', keyFilePath: '', pfxFilePath: '', passphrase: '' } as types.ClientCertificateConfig
    ]
    workspaceStore.appState = await UpdateCollectionClientCertificates(activeCollection.id, rows)
  }

  async function removeCollectionClientCertificate(index: number) {
    if (!activeCollection) return
    const rows = [...(activeCollection.clientCertificates ?? [])]
    rows.splice(index, 1)
    workspaceStore.appState = await UpdateCollectionClientCertificates(activeCollection.id, rows)
  }

  async function updateCollectionProtoFile(index: number, field: keyof types.CollectionProtoFile, value: string) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.protoFiles ?? [])]
    rows[index] = { ...(rows[index] ?? { path: '', type: 'file' }), [field]: value } as types.CollectionProtoFile
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, protoFiles: rows }))
  }

  async function addCollectionProtoFile() {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    await updateCollectionProtobuf(asCollectionProtobufConfig({
      ...protobuf,
      protoFiles: [...(protobuf.protoFiles ?? []), { path: 'protos/service.proto', type: 'file', exists: false } as types.CollectionProtoFile]
    }))
  }

  async function removeCollectionProtoFile(index: number) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.protoFiles ?? [])]
    rows.splice(index, 1)
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, protoFiles: rows }))
  }

  async function updateCollectionProtoImportPath(index: number, field: keyof types.CollectionProtoImportPath, value: string | boolean) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.importPaths ?? [])]
    rows[index] = { ...(rows[index] ?? { path: '', enabled: true }), [field]: value } as types.CollectionProtoImportPath
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, importPaths: rows }))
  }

  async function addCollectionProtoImportPath() {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    await updateCollectionProtobuf(asCollectionProtobufConfig({
      ...protobuf,
      importPaths: [...(protobuf.importPaths ?? []), { path: 'protos', enabled: true, exists: false } as types.CollectionProtoImportPath]
    }))
  }

  async function removeCollectionProtoImportPath(index: number) {
    if (!activeCollection) return
    const protobuf = collectionProtobufConfig()
    const rows = [...(protobuf.importPaths ?? [])]
    rows.splice(index, 1)
    await updateCollectionProtobuf(asCollectionProtobufConfig({ ...protobuf, importPaths: rows }))
  }

  async function updateCollectionAWSV4Auth(updates: Partial<types.AWSV4Auth>) {
    if (!activeCollection) return
    await updateCollectionAuth({ awsv4: { ...(activeCollection.auth?.awsv4 ?? {}), ...updates } as types.AWSV4Auth })
  }

  async function updateCollectionOAuth1Auth(updates: Partial<types.OAuth1Auth>) {
    if (!activeCollection) return
    await updateCollectionAuth({ oauth1: { ...(activeCollection.auth?.oauth1 ?? {}), ...updates } as types.OAuth1Auth })
  }

  async function updateCollectionOAuth2Auth(updates: Partial<types.OAuth2Auth>) {
    if (!activeCollection) return
    await updateCollectionAuth({ oauth2: { ...(activeCollection.auth?.oauth2 ?? {}), ...updates } as types.OAuth2Auth })
  }

  async function updateCollectionOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn, index: number, field: OAuth2ParamField, value: string | boolean) {
    if (!activeCollection) return
    await updateCollectionOAuth2Auth({ [bucket]: updateOAuth2ParamList(activeCollection.auth?.oauth2, bucket, index, sendIn, field, value) } as Partial<types.OAuth2Auth>)
  }

  async function addCollectionOAuth2AdditionalParam(bucket: OAuth2ParamBucket, sendIn: OAuth2ParamSendIn) {
    if (!activeCollection) return
    await updateCollectionOAuth2Auth({ [bucket]: addOAuth2ParamList(activeCollection.auth?.oauth2, bucket, sendIn) } as Partial<types.OAuth2Auth>)
  }

  async function removeCollectionOAuth2AdditionalParam(bucket: OAuth2ParamBucket, index: number) {
    if (!activeCollection) return
    await updateCollectionOAuth2Auth({ [bucket]: removeOAuth2ParamList(activeCollection.auth?.oauth2, bucket, index) } as Partial<types.OAuth2Auth>)
  }

  async function updateCollectionDocs(value: string) {
    if (!activeCollection) return
    workspaceStore.appState = await UpdateCollectionDocs(activeCollection.id, value)
  }

  async function updateCollectionScript(field: 'preScript' | 'postScript' | 'tests', value: string) {
    if (!activeCollection) return
    workspaceStore.appState = await UpdateCollectionScripts(
      activeCollection.id,
      field === 'preScript' ? value : activeCollection.preScript,
      field === 'postScript' ? value : activeCollection.postScript,
      field === 'tests' ? value : activeCollection.tests
    )
  }

  function folderAuthWithDefaults(updates: Partial<types.AuthConfig> = {}) {
    const current = editableFolder?.auth ?? activeFolder?.auth ?? ({} as types.AuthConfig)
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
      oauth1: current.oauth1 ?? ({} as types.OAuth1Auth),
      awsv4: current.awsv4 ?? ({} as types.AWSV4Auth),
      ...updates
    } as types.AuthConfig
    return authWithOAuth2Defaults(base, updates)
  }

  async function saveFolderSettings(updates: Partial<types.FolderConfig>) {
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
    } as types.FolderConfig
    const targetPath = currentFolder.path
    folderSettingDrafts = { ...folderSettingDrafts, [nextFolder.path]: nextFolder }
    folderSettingsSaveQueue = folderSettingsSaveQueue
      .catch(() => {})
      .then(async () => {
        workspaceStore.appState = await UpdateFolderSettings(activeCollection.id, targetPath, nextFolder)
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

  async function updateFolderVariable(bucket: 'variables' | 'resVariables', index: number, field: keyof types.Variable, value: string | boolean) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const vars = [...(bucket === 'variables' ? folder.variables ?? [] : folder.resVariables ?? [])]
    vars[index] = field === 'dataType' ? { ...vars[index], dataType: String(value), type: String(value) } : { ...vars[index], [field]: value }
    await saveFolderSettings({ [bucket]: vars } as Partial<types.FolderConfig>)
  }

  async function addFolderVariable(bucket: 'variables' | 'resVariables') {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const vars = [...(bucket === 'variables' ? folder.variables ?? [] : folder.resVariables ?? [])]
    vars.push({ id: `ui-folder-var-${Date.now()}`, name: '', value: '', type: 'string', dataType: 'string', enabled: true, secret: false })
    await saveFolderSettings({ [bucket]: vars } as Partial<types.FolderConfig>)
  }

  async function removeFolderVariable(bucket: 'variables' | 'resVariables', index: number) {
    const folder = editableFolder ?? activeFolder
    if (!folder) return
    const vars = [...(bucket === 'variables' ? folder.variables ?? [] : folder.resVariables ?? [])]
    vars.splice(index, 1)
    await saveFolderSettings({ [bucket]: vars } as Partial<types.FolderConfig>)
  }

  async function updateFolderAuth(updates: Partial<types.AuthConfig>) {
    await saveFolderSettings({ auth: folderAuthWithDefaults(updates) })
  }

  async function updateFolderAWSV4Auth(updates: Partial<types.AWSV4Auth>) {
    await updateFolderAuth({ awsv4: { ...(editableFolder?.auth?.awsv4 ?? activeFolder?.auth?.awsv4 ?? {}), ...updates } as types.AWSV4Auth })
  }

  async function updateFolderOAuth1Auth(updates: Partial<types.OAuth1Auth>) {
    await updateFolderAuth({ oauth1: { ...(editableFolder?.auth?.oauth1 ?? activeFolder?.auth?.oauth1 ?? {}), ...updates } as types.OAuth1Auth })
  }

  async function updateFolderOAuth2Auth(updates: Partial<types.OAuth2Auth>) {
    await updateFolderAuth({ oauth2: { ...(editableFolder?.auth?.oauth2 ?? activeFolder?.auth?.oauth2 ?? {}), ...updates } as types.OAuth2Auth })
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

  function responseHeaderValue(response: types.Response | undefined, name: string) {
    const headers = response?.headers ?? {}
    const exact = headers[name]
    if (exact !== undefined) return String(exact)
    const match = Object.entries(headers).find(([key]) => key.toLowerCase() === name.toLowerCase())
    return match ? String(match[1]) : ''
  }


  function timelineTimestamp(entry: types.TimelineItem) {
    if (!entry.at) return 0
    const value = new Date(entry.at)
    return Number.isNaN(value.getTime()) ? 0 : value.getTime()
  }

  function sortedTimelineEntries(entries: types.TimelineItem[]) {
    return [...entries].sort((left, right) => timelineTimestamp(right) - timelineTimestamp(left))
  }



  function normalizedDevToolsNetworkSortKey(value: string | undefined): DevToolsNetworkSortKey | '' {
    return normalizedDevToolsNetworkSortKeyOf(value, devToolsNetworkSortKeys)
  }

  function normalizedDevToolsNetworkSortDirection(value: string | undefined): DevToolsNetworkSortDirection {
    return normalizedDevToolsNetworkSortDirectionOf(value)
  }

  function defaultDevToolsNetworkColumnWidths() {
    return [...DEFAULT_NETWORK_COLUMN_WIDTHS]
  }

  function normalizedDevToolsNetworkColumnWidths(widths: number[] | undefined) {
    return normalizedNetworkColumnWidths(widths)
  }

  function devToolsNetworkPreferencePayload(sortKey: DevToolsNetworkSortKey | '', sortDirection: DevToolsNetworkSortDirection, columnWidths: number[]) {
    const sort = networkSortPreference(sortKey, sortDirection, devToolsNetworkSortKeys)
    return {
      sortKey: sort.key,
      sortDirection: sort.direction,
      columnWidths: normalizedDevToolsNetworkColumnWidths(columnWidths)
    }
  }

  function devToolsNetworkPreferencesKeyFor(preferences: types.DevToolsNetworkPreferences | undefined) {
    const payload = devToolsNetworkPreferencePayload(normalizedDevToolsNetworkSortKey(preferences?.sortKey), normalizedDevToolsNetworkSortDirection(preferences?.sortDirection), preferences?.columnWidths ?? defaultDevToolsNetworkColumnWidths())
    return JSON.stringify(payload)
  }

  function applyDevToolsNetworkPreferences(preferences: types.DevToolsNetworkPreferences | undefined) {
    const payload = devToolsNetworkPreferencePayload(normalizedDevToolsNetworkSortKey(preferences?.sortKey), normalizedDevToolsNetworkSortDirection(preferences?.sortDirection), preferences?.columnWidths ?? defaultDevToolsNetworkColumnWidths())
    devToolsNetworkSortKey = payload.sortKey
    devToolsNetworkSortDirection = payload.sortDirection
    devToolsNetworkColumnWidths = payload.columnWidths
    devToolsNetworkPreferencesKey = JSON.stringify(payload)
  }

  async function updateDevToolsNetworkPreferences(updates: Partial<types.DevToolsNetworkPreferences>) {
    const payload = devToolsNetworkPreferencePayload(normalizedDevToolsNetworkSortKey(updates.sortKey ?? devToolsNetworkSortKey), normalizedDevToolsNetworkSortDirection(updates.sortDirection ?? devToolsNetworkSortDirection), updates.columnWidths ?? devToolsNetworkColumnWidths)
    devToolsNetworkSortKey = payload.sortKey
    devToolsNetworkSortDirection = payload.sortDirection
    devToolsNetworkColumnWidths = payload.columnWidths
    devToolsNetworkPreferencesKey = JSON.stringify(payload)
    if (!appState) return
    workspaceStore.appState = await UpdatePreferences({
      ...appState.preferences,
      devTools: {
        ...(appState.preferences.devTools ?? {}),
        network: payload
      }
    } as types.Preferences)
  }

  function filteredDevToolsNetworkRows(rows: types.NetworkLog[], filters: Record<string, boolean>) {
    return rows.filter((row) => filters[normalizedNetworkMethod(row)] === true)
  }



  function cycleDevToolsNetworkSort(key: DevToolsNetworkSortKey) {
    const next = nextNetworkSort(devToolsNetworkSortKey, devToolsNetworkSortDirection, key)
    void updateDevToolsNetworkPreferences({ sortKey: next.key, sortDirection: next.direction })
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
      latestWidths = resizeAdjacentColumns(startWidths, index, moveEvent.clientX - startX)
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
      latestWidth = sizeFromTrailingEdgeDrag(startWidth, moveEvent.clientX - startX, normalizedDevToolsDetailsPanelWidth)
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
      latestHeight = sizeFromTrailingEdgeDrag(startHeight, moveEvent.clientY - startY, normalizedDevToolsDrawerHeight)
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

  function selectDevToolsNetworkRow(row: types.NetworkLog) {
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

  async function openFolderInTerminal(collection: types.Collection, folderPath: string) {
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

  async function revealFolderInFolder(collection: types.Collection, folderPath: string) {
    if (!collection || collection.notFoundLocally) return
    await runAction('reveal folder', async () => {
      await RevealCollectionFolderInFolder(collection.id, folderPath)
      revealCollectionMessage = `${revealInFolderLabel()} opened for ${slashPathBase(folderPath)}`
    })
  }

  async function revealRequestInFolder(collection: types.Collection, item: types.RequestItem) {
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

  function terminalSessionLabel(session: types.TerminalSession) {
    const parts = (session.cwd || '').split(/[\\/]/).filter(Boolean)
    const leaf = parts[parts.length - 1]
    return leaf || 'Terminal'
  }

  function terminalSessionStatus(session: types.TerminalSession) {
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



  function networkLogTime(row: types.NetworkLog) {
    if (!row.at) return '-'
    const value = new Date(row.at)
    if (Number.isNaN(value.getTime())) return '-'
    return value.toLocaleTimeString()
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

  function networkLogLines(row: types.NetworkLog | undefined) {
    if (!row) return []
    return [
      `Started: ${networkLogTime(row)}`,
      `Duration: ${row.durationMs ?? 0} ms`,
      `Size: ${formatNetworkSize(row.size)}`,
      row.error ? `Error: ${row.error}` : ''
    ].filter(Boolean)
  }



  function requestNameForTransport(target: { collectionId: string; requestId: string }) {
    for (const workspace of appState?.workspaces ?? []) {
      const collection = workspace.collections?.find((candidate) => candidate.id === target.collectionId)
      const request = collection?.items?.find((candidate) => candidate.id === target.requestId)
      if (request) return request.name?.trim() || 'request'
    }
    return 'request'
  }

  function runnerCancellationCount(snapshot: types.RunnerSnapshot | undefined) {
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

  // US-034. Called from the template inside {#each visibleSidebarCollections},
  // so it rebuilt every collection's folder grouping on EVERY render —
  // including renders caused by something with no bearing on it at all.
  //
  // Keyed on collection id + revision + query. The revision is the one value
  // the backend bumps on every mutation, so it is what guarantees the key
  // changes whenever anything the grouping reads has changed. Keying on the
  // collection object alone would go stale on an in-place edit; keying on its
  // item count would miss a rename.
  // US-031: sidebar row windowing.
  //
  // The tree markup stays nested (collection -> folder -> request) because
  // flattening it would mean rewriting ~200 lines of headers, drag targets and
  // menus. Only the innermost request loop is windowed, using each group's
  // offset in a FLAT row coordinate so the arithmetic matches what the user
  // actually scrolls past.
  //
  // Rows outside the window are replaced by two spacer divs, so the scrollbar
  // stays the size it would be if everything were rendered. Getting that wrong
  // is visible immediately: the scrollbar jumps as you scroll.
  let sidebarScrollTop = $state(0)
  let sidebarViewportHeight = $state(0)
  let sidebarRowHeight = $state(28)

  function measureSidebarViewport(node: HTMLElement) {
    const sync = () => {
      sidebarScrollTop = node.scrollTop
      sidebarViewportHeight = node.clientHeight
      const row = node.querySelector<HTMLElement>('.request-row-shell')
      // A row measures 0 before layout; keeping the previous value avoids a
      // divide-by-zero window that would render nothing.
      if (row && row.offsetHeight > 0) sidebarRowHeight = row.offsetHeight
    }
    sync()
    node.addEventListener('scroll', sync, { passive: true })
    const observer = new ResizeObserver(sync)
    observer.observe(node)
    return {
      destroy() {
        node.removeEventListener('scroll', sync)
        observer.disconnect()
      }
    }
  }

  /**
   * The flat row index of a group's first request.
   *
   * The walk lives in lib/virtualList beside the window arithmetic it feeds,
   * where it is tested — including the rule that a collection whose directory
   * is missing contributes only its header.
   */
  function sidebarGroupOffset(targetCollectionId: string, targetFolder: string): number {
    return sidebarGroupOffsetOf(
      {
        collections: visibleSidebarCollections,
        groupsFor: (id) => {
          const collection = visibleSidebarCollections.find((candidate) => candidate.id === id)
          return collection ? groupedItems(collection, searchQuery) : []
        },
        collapsedCollections: collapsedSidebarCollections,
        collapsedFolders: collapsedSidebarFolders,
        searchQuery,
        folderKey: sidebarFolderKey
      },
      targetCollectionId,
      targetFolder
    )
  }

  /** Which of a group's items fall inside the viewport, plus the padding. */
  // The offset and the viewport metrics are reactive; the arithmetic is not,
  // and lives in lib/virtualList beside computeWindow.
  function sidebarItemWindow(collectionId: string, folder: string, count: number) {
    return sidebarGroupWindow(
      count,
      sidebarGroupOffset(collectionId, folder),
      sidebarRowHeight,
      sidebarViewportHeight,
      sidebarScrollTop
    )
  }

  // US-031: which per-row disclosure menus are open, keyed collection:item.
  // Only an open menu renders its buttons; see the note at the <details>.
  let openRequestMenus = $state<Record<string, boolean>>({})

  const groupedItemsMemo = new KeyedMemo<{ folder: string; items: types.RequestItem[] }[]>()

  function groupedItems(collection: types.Collection, query = '') {
    const revision = appState?.revision ?? 0
    return groupedItemsMemo.get(`${collection.id}:${revision}:${query}`, () => computeGroupedItems(collection, query))
  }



  function sidebarCollections(workspace: types.Workspace | undefined, query: string) {
    const collections = workspace?.collections ?? []
    if (!query) return collections
    return collections.filter((collection) => collectionMatches(collection, query) || filteredFolders(collection, query).length > 0 || filteredItems(collection, query).length > 0)
  }

  function sidebarRequestCount(workspace: types.Workspace | undefined, query: string) {
    return (workspace?.collections ?? []).reduce((total, collection) => total + filteredItems(collection, query).length, 0)
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
      workspaceStore.selectedCollectionId = result.collectionId
      requestSearch = result.name
      activeView = 'request'
    } else {
      workspaceStore.selectedCollectionId = result.collectionId
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

  async function selectNotification(notification: types.Notification) {
    selectedNotificationID = notification.id
    if (!notification.read) {
      workspaceStore.appState = await MarkNotificationRead(notification.id)
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
      workspaceStore.appState = await MarkAllNotificationsRead()
      if (notificationTab === 'unread') pinnedUnreadNotificationIDs = null
    })
  }

  async function clearNotifications() {
    await runAction('clear notifications', async () => {
      workspaceStore.appState = await ClearNotifications()
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
    const resolved = resolveNativeMenuCommand(command, { activeView })
    if (!resolved) return
    if (resolved.kind === 'workbench') {
      await runWorkbenchCommand(resolved.command)
      return
    }
    switch (resolved.action) {
      case 'open-native-new-window': await openNativeNewWindow(); return
      case 'save-request': await saveRequest(); return
      case 'save-all-tabs': await saveAllOpenTabs(); return
      case 'close-active-tab': await closeActiveTab(); return
      case 'reopen-last-closed-tab': await reopenLastClosedTab(); return
      case 'cancel-active-request': await cancelActiveRequest(); return
      case 'send-request': await sendRequest(); return
      case 'run-collection': await runCollection(); return
    }
  }

  function shortcut(event: KeyboardEvent) {
    const action = resolveShortcut(event, {
      commandPaletteOpen,
      // Getters, not values: the dispatcher runs on every keystroke including
      // ordinary typing, and these two selectors only matter on Escape. Reading
      // them eagerly would put two document-wide queries in the path of every
      // character the user types.
      get requestActionMenuOpen() { return Boolean(document.querySelector('details.request-actions[open]')) },
      get modalOpen() { return Boolean(document.querySelector('[role="dialog"][aria-modal="true"]')) },
      activeView,
      canCancel: requestCommand.canCancel || hasActiveHTTPTransport || Boolean(activeCollectionRun),
      keybindingsEnabled: Boolean(appState) && keybindingsAreEnabled(appState?.preferences),
      matches: (candidate) => keyBindingEventMatches(event, candidate)
    })
    if (!action) return
    event.preventDefault()
    const tabNumber = shortcutTabNumber(action)
    if (tabNumber !== undefined) {
      void switchToOpenTabAt(tabNumber - 1)
      return
    }
    switch (action) {
      case 'closeCommandPalette': closeCommandPalette(); return
      case 'closeRequestActionMenus': closeRequestActionMenus(); return
      case 'focusURL':
        requestURLInput?.focus()
        requestURLInput?.select()
        return
      case 'cancelActiveRequest': void cancelActiveRequest(); return
      case 'commandPalette': openCommandPalette(); return
      case 'globalSearch': openGlobalSearch(); return
      case 'sidebarSearch':
        requestSearchInput?.focus()
        requestSearchInput?.select()
        return
      case 'collapseSidebar': toggleSidebarCollapse(); return
      case 'closeAllTabs': void closeAllOpenTabs(); return
      case 'reopenLastClosedTab': void reopenLastClosedTab(); return
      case 'closeTab': void closeActiveTab(); return
      case 'switchToPreviousTab': void switchToRelativeOpenTab(-1); return
      case 'switchToNextTab': void switchToRelativeOpenTab(1); return
      case 'switchToLastTab': void switchToOpenTabAt((appState?.openTabs?.length ?? 0) - 1); return
      case 'moveTabLeft': void moveActiveTab(-1); return
      case 'moveTabRight': void moveActiveTab(1); return
      case 'newRequest': createRequest(); return
      case 'importCollection': activeView = 'import'; return
      case 'editEnvironment': activeView = 'environments'; return
      case 'openPreferences': activeView = 'preferences'; return
      case 'openTerminal': void openDevTools('terminal'); return
      case 'sendRequest': sendRequest(); return
      case 'changeLayout': void toggleResponsePaneOrientation(); return
      case 'zoomIn': void incrementZoomPercentage(zoomStepPercentage); return
      case 'zoomOut': void incrementZoomPercentage(-zoomStepPercentage); return
      case 'resetZoom': void resetZoomPercentage(); return
      case 'closeBruno': closeApplication(); return
      case 'save': saveRequest(); return
      case 'saveAllTabs': void saveAllOpenTabs(); return
    }
  }

  load()
</script>

<svelte:window onkeydown={shortcut} onclick={closeVariableTooltipOnOutside} />

{#if loading}
  <main class="boot">
    <div class="loader"></div>
    <p>{loadingStatus}</p>
  </main>
{:else if appState}
  <main class="app-shell" class:sidebar-collapsed={sidebarCollapsed} style={`--sidebar-width: ${sidebarWidth}px;`} >
    <aside class="workspace-rail" aria-label="Collections sidebar">
      <SidebarHeader onNew={openCreationFlow} />

      <SidebarSearch bind:value={requestSearch} bind:input={requestSearchInput} matchCount={sidebarSearchCount} />

      <section class="collections" use:measureSidebarViewport>
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
                onclick={() => toggleSidebarCollection(collection.id)}
              >▾</button>
              <button class="collection-title" onclick={() => selectCollection(collection.id)}>{collection.name}</button>
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
                  <button onclick={() => selectCollection(collection.id)}>Details</button>
                  <button onclick={() => copyText(collection.remote ?? '')}>Copy URL</button>
                  <button onclick={() => disconnectGitRemote(collection.id)}>Remove</button>
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
                      onclick={() => toggleSidebarFolder(collection.id, group.folder)}
                    >▾</button>
                    <button class="folder-row" title={`${group.folder} settings`} onclick={() => selectFolderSettings(collection, group.folder)}>{group.folder}</button>
                    <button
                      class="folder-action"
                      type="button"
                      title={revealInFolderLabel()}
                      aria-label={`${revealInFolderLabel()} ${group.folder}`}
                      data-testid="collection-item-menu-show-in-folder"
                      onclick={() => revealFolderInFolder(collection, group.folder)}
                    >F</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Info"
                      aria-label="Info"
                      data-testid="collection-item-menu-info"
                      onclick={() => openFolderInfoModal(collection, group.folder)}
                    >i</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Open in Terminal"
                      aria-label={`Open ${group.folder} in Terminal`}
                      data-testid="collection-item-menu-open-terminal"
                      onclick={() => openFolderInTerminal(collection, group.folder)}
                    >T</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="New Folder"
                      data-testid="collection-item-menu-new-folder"
                      onclick={() => openNewFolderModal(group.folder, collection)}
                    >+</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Rename"
                      aria-label="Rename"
                      data-testid="collection-item-menu-rename"
                      onclick={() => openRenameFolderModal(collection, group.folder)}
                    >✎</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Clone"
                      aria-label="Clone"
                      data-testid="collection-item-menu-clone"
                      onclick={() => openCloneFolderModal(collection, group.folder)}
                    >C</button>
                    <button
                      class="folder-action"
                      type="button"
                      title="Delete"
                      aria-label="Delete"
                      data-testid="collection-item-menu-delete"
                      onclick={() => openDeleteFolderModal(collection, group.folder)}
                    >x</button>
                  </div>
                {/if}
                {#if !folderCollapsed}
                {@const win = sidebarItemWindow(collection.id, group.folder, group.items.length)}
                {#if win.padTop > 0}<div class="sidebar-spacer" style={`height:${win.padTop}px`} aria-hidden="true"></div>{/if}
                {#each group.items.slice(win.start, win.end) as item (item.id)}
                  <div class="request-row-shell" class:in-folder={Boolean(group.folder)}>
                    <button
                      class="request-row"
                      class:item-active={item.id === activeRequest?.id}
                      title={group.folder ? `${group.folder} · ${item.url}` : item.url}
                      onclick={() => openRequestTab(collection.id, item.id)}
                    >
                      <span class="method" data-method={item.method}>{methodLabel(item.method)}</span>
                      <span>{item.name}</span>
                      {#if requestIsTransient(collection, item)}<em>temp</em>{/if}
                      {#if item.draft}<em>draft</em>{/if}
                    </button>
                    <!-- US-031: the menu body renders only while the disclosure is
                         open. <details> keeps its children in the DOM when closed, so a
                         500-request collection was carrying ~3,000 buttons nobody could
                         see. bind:open is what makes the {#if} track the real state. -->
                    <details class="request-actions" data-testid="request-actions-menu" bind:open={openRequestMenus[`${collection.id}:${item.id}`]}>
                      <summary data-testid="request-actions-menu-toggle" aria-label={`More actions for ${item.name}`} title={`More actions for ${item.name}`}>More</summary>
                    {#if openRequestMenus[`${collection.id}:${item.id}`]}
                    <button
                      class="request-action"
                      type="button"
                      title={revealInFolderLabel()}
                      aria-label={`${revealInFolderLabel()} ${item.name}`}
                      data-testid="collection-item-menu-show-in-folder"
                      onclick={() => { closeRequestActionMenus(); void revealRequestInFolder(collection, item) }}
                    >Reveal</button>
                    {#if requestSupportsGenerateCode(item)}
                      <button
                        class="request-action"
                        type="button"
                        title="Generate Code"
                        aria-label={`Generate Code ${item.name}`}
                        data-testid="collection-item-menu-generate-code"
                        onclick={() => { closeRequestActionMenus(); void beginGenerateRequestCode(collection, item) }}
                      >Code</button>
                    {/if}
                    <button
                      class="request-action"
                      type="button"
                      title="Info"
                      aria-label={`Info ${item.name}`}
                      data-testid="collection-item-menu-info"
                      onclick={() => { closeRequestActionMenus(); openRequestInfoModal(collection, item) }}
                    >Info</button>
                    <button
                      class="request-action"
                      type="button"
                      title="Rename"
                      aria-label={`Rename ${item.name}`}
                      data-testid="collection-item-menu-rename"
                      onclick={() => { closeRequestActionMenus(); openRenameRequestModal(collection, item) }}
                    >Rename</button>
                    <button
                      class="request-action"
                      type="button"
                      title="Clone"
                      aria-label={`Clone ${item.name}`}
                      data-testid="collection-item-menu-clone"
                      onclick={() => { closeRequestActionMenus(); openCloneRequestModal(collection, item) }}
                    >Clone</button>
                    <button
                      class="request-action danger-inline"
                      type="button"
                      title="Delete"
                      aria-label={`Delete ${item.name}`}
                      data-testid="collection-item-menu-delete"
                      onclick={() => { closeRequestActionMenus(); openDeleteRequestModal(collection, item) }}
                    >Delete</button>
                    {/if}
                    </details>
                  </div>
                  {#if (item.examples ?? []).length > 0}
                    <div class="sidebar-examples" aria-label={`Response examples for ${item.name}`}>
                      {#each item.examples ?? [] as example (responseExampleIdentifier(example))}
                        <button
                          class="sidebar-example-row"
                          class:item-active={responseExampleIsActive(collection.id, item.id, example)}
                          title={example.description || example.request?.url || example.name}
                          onclick={() => openResponseExampleTabFor(collection.id, item.id, example)}
                        >
                          <span class="example-glyph">Ex</span>
                          <span>{example.name}</span>
                          <small class={responseStatusClass(example.response.status)}>{example.response.status}</small>
                        </button>
                      {/each}
                    </div>
                  {/if}
                {/each}
                {#if win.padBottom > 0}<div class="sidebar-spacer" style={`height:${win.padBottom}px`} aria-hidden="true"></div>{/if}
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
      onmousedown={startSidebarResize}
      ondblclick={() => { sidebarWidth = DEFAULT_SIDEBAR_WIDTH; persistWorkbenchLayout() }}
      oninput={(event) => (sidebarWidth = clampSidebarWidth(Number(event.currentTarget.value)))}
      onchange={persistWorkbenchLayout}
    />

    <section class="main-pane">
      <header class="topbar">
        <WorkspaceCommandBar
          {sidebarCollapsed}
          {activeView}
          notificationCount={unreadNotificationCount}
          runningCollectionName={activeCollectionRun?.collectionName ?? ''}
          cancellingRun={collectionRunCancellationRequested}
          onCommand={runWorkbenchCommand}
          onWorkspaceChange={setActiveWorkspace}
          onGlobalEnvironmentChange={setActiveGlobalEnvironment}
          onEnvironmentChange={(environmentID) => { workspaceStore.selectedEnvironmentId = environmentID }}
        >
          {#snippet recovery()}
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
                        <button type="button" aria-label={`Restore ${entry.kind} ${entry.displayName}`} onclick={() => void restoreRecoveryEntry(entry)} disabled={recoveryBusyEntryID !== ''}>Restore</button>
                        <button type="button" class="danger-button" aria-label={`Remove recovery copy for ${entry.displayName}`} onclick={() => void discardRecoveryEntry(entry)} disabled={recoveryBusyEntryID !== ''}>Remove recovery copy</button>
                      </div>
                    </article>
                  {/each}
                </div>
              </details>
            {/if}
          {/snippet}
        </WorkspaceCommandBar>
        {#if (appState.openTabs ?? []).length > 0}
          <nav class="tabs" aria-label="Open tabs">
            {#each appState.openTabs as tab (tab.id)}
              <div class="tab" class:active={tab.id === appState.activeTabId}>
                <button class="tab-select" title={tabLabel(tab)} onclick={() => setActiveTab(tab.id)}>
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
                  onclick={() => beginTabLifecycleAction('close-active', tab.id)}
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
              <button type="button" onclick={refreshDevToolsSnapshot}>Refresh</button>
              <button type="button" aria-label="Close console" onclick={closeDevTools}>Close</button>
            </div>
          </header>
          <nav class="devtools-tabs" aria-label="Dev Tools tabs">
            {#each devToolsTabs as tab (tab.id)}
              <button type="button" class:active={devToolsTab === tab.id} onclick={() => selectDevToolsTab(tab.id)}>{tab.label}</button>
            {/each}
          </nav>
          <div class="devtools-content">
            {#if devToolsTab === 'console'}
              {#await import('./lib/views/devtools/ConsoleTab.svelte') then ConsoleTab}
                {@const ConsoleTabComponent = ConsoleTab.default}
                <ConsoleTabComponent {devToolsConsoleRows} />
              {/await}
            {:else if devToolsTab === 'network'}
              <div class="network-filter-bar" aria-label="Filter requests by method">
                <div>
                  <strong>Filter by Method</strong>
                  <span>{devToolsNetworkActiveFilterCount === devToolsNetworkMethods.length ? 'All' : `${devToolsNetworkActiveFilterCount}/${devToolsNetworkMethods.length}`}</span>
                </div>
                <div class="button-row compact">
                  <button type="button" onclick={() => setAllDevToolsNetworkFilters(false)}>Hide All</button>
                  <button type="button" onclick={() => setAllDevToolsNetworkFilters(true)}>Show All</button>
                </div>
                <div class="method-filter-list">
                  {#each devToolsNetworkMethods as method (method)}
                    <label>
                      <input type="checkbox" checked={devToolsNetworkFilters[method]} onchange={(event) => setDevToolsNetworkFilter(method, event.currentTarget.checked)} />
                      <span>{method} {devToolsNetworkMethodCounts[method] ?? 0}</span>
                    </label>
                  {/each}
                </div>
              </div>
              {#if devToolsNetworkRows.length === 0}
                <div class="empty-appState devtools-empty">
                  <strong>No network requests</strong>
                  <span>Requests will appear here as you make API calls</span>
                </div>
              {:else}
                <div class="network-layout" style={`--network-details-width: ${devToolsDetailsPanelWidth}px;`}>
                  <div
                    class="table-scroll network-table-scroll"
                    class:resizing={devToolsNetworkResizingColumn >= 0}
                    use:measureDevToolsNetworkViewport
                    onscroll={(event) => (devToolsNetworkScrollTop = event.currentTarget.scrollTop)}
                  >
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
                              <button type="button" class="network-sort-button" onclick={() => cycleDevToolsNetworkSort(column.key)}>{column.label} {devToolsNetworkSortLabels[column.key]}</button>
                              {#if index < devToolsNetworkColumns.length - 1}
                                <button
                                  type="button"
                                  class="column-resizer"
                                  class:active={devToolsNetworkResizingColumn === index}
                                  aria-label={`Resize ${column.label} column`}
                                  onmousedown={(event) => startDevToolsNetworkColumnResize(index, event)}
                                ></button>
                              {/if}
                            </th>
                          {/each}
                        </tr>
                      </thead>
                      <tbody>
                        <!--
                          Spacer rows rather than a transform or absolute
                          positioning: this is a real <table>, and anything that
                          takes rows out of flow breaks the colgroup widths the
                          resizable columns depend on.
                        -->
                        {#if devToolsNetworkWindow.topPadding > 0}
                          <tr aria-hidden="true" class="network-spacer"><td colspan={devToolsNetworkColumns.length} style={`height: ${devToolsNetworkWindow.topPadding}px; padding: 0; border: none;`}></td></tr>
                        {/if}
                        {#each devToolsNetworkVisibleRows as row (row.id)}
                          <tr data-network-row class:selected={selectedDevToolsNetworkRow?.id === row.id}>
                            <td><button class="table-link" type="button" onclick={() => selectDevToolsNetworkRow(row)}>{normalizedNetworkMethod(row)}</button></td>
                            <td>{statusDisplay(row.status)}</td>
                            <td>{devToolsNetworkDomain(row)}</td>
                            <td><code>{devToolsNetworkPath(row)}</code></td>
                            <td>{networkLogTime(row)}</td>
                            <td>{row.durationMs} ms</td>
                            <td>{formatNetworkSize(row.size)}</td>
                          </tr>
                        {/each}
                        {#if devToolsNetworkWindow.bottomPadding > 0}
                          <tr aria-hidden="true" class="network-spacer"><td colspan={devToolsNetworkColumns.length} style={`height: ${devToolsNetworkWindow.bottomPadding}px; padding: 0; border: none;`}></td></tr>
                        {/if}
                      </tbody>
                    </table>
                  </div>
                  {#if selectedDevToolsNetworkRow}
              {#await import('./lib/views/devtools/RequestDetailsPanel.svelte') then RequestDetailsPanel}
                {@const RequestDetailsPanelComponent = RequestDetailsPanel.default}
                <RequestDetailsPanelComponent
                  {selectedDevToolsNetworkRow}
                  {devToolsNetworkDetailTab}
                  {devToolsNetworkDetailTabs}
                  {networkHeaderRows}
                  {networkLogBody}
                  {networkLogLines}
                  {normalizedNetworkMethod}
                  {startDevToolsDetailsPanelResize}
                />
              {/await}
                  {/if}
                </div>
              {/if}
            {:else if devToolsTab === 'performance'}
          {#await import('./lib/views/devtools/PerformanceTab.svelte') then PerformanceTab}
            {@const PerformanceTabComponent = PerformanceTab.default}
            <PerformanceTabComponent
              {devToolsSnapshot}
              {devToolsPerformanceProcesses}
              {displayedDevToolsCPUPercent}
              {displayedDevToolsMemoryBytes}
              {displayedDevToolsUptimeSeconds}
              {devToolsPerformanceView}
              {displayedDevToolsPID}
              {selectedDevToolsPerformanceProcess}
              {formatCPUPercent}
              {formatRuntimeBytes}
              {formatUptime}
              {refreshDevToolsSnapshot}
            />
          {/await}
            {:else}
              {#await import('./lib/views/devtools/TerminalTab.svelte') then TerminalTab}
                {@const TerminalTabComponent = TerminalTab.default}
                <TerminalTabComponent
                  bind:terminalInput
                  {terminalSessions}
                  {terminalActiveSessionId}
                  {activeTerminalSession}
                  {terminalBusy}
                  {terminalError}
                  {terminalDisplayOutput}
                  {terminalOutput}
                  {terminalSessionLabel}
                  {terminalSessionStatus}
                  {createTerminalSession}
                  {selectTerminalSession}
                  {closeTerminalSession}
                  {sendTerminalInput}
                />
              {/await}
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
            {#snippet requestLine()}
              {#snippet variableURLField()}
                <div class="url-variable-editor">
                  <input
                    class="url-variable-input"
                    aria-label={activeRequest.type === 'grpc' ? 'gRPC server URL' : activeRequest.type === 'websocket' ? 'WebSocket URL' : 'URL'}
                    bind:this={requestURLInput}
                    value={activeRequest.url}
                    oninput={patchURLField}
                    onscroll={syncURLInputScroll}
                    onkeyup={syncURLInputScroll}
                    onmouseup={syncURLInputScroll}
                  />
                  <div class="url-variable-overlay">
                    <span class="url-variable-overlay-content" style={`transform: translateX(-${urlInputScrollLeft}px);`}>
                      {#each urlVariableSegments(activeRequest.url, requestVariableTooltips, activeRequest.pathParams ?? []) as segment (segment.key)}
                        {#if segment.variable}
                          <span
                            class="url-variable-token-wrapper"
                            class:open={variableTooltips.active === segment.name}
                          >
                            <span
                              role="button"
                              tabindex="0"
                              class:cm-variable-valid={isValidURLVariableSegment(segment)}
                              class:cm-variable-invalid={!isValidURLVariableSegment(segment)}
                              onclick={() => toggleActiveVariableTooltip(segment.name)}
                              onkeydown={(event) => handleInlineVariableTokenKey(event, segment.name)}
                            >{segment.text}</span>
                            {#if segment.info}
                              <div class="CodeMirror-brunoVarInfo inline-var-tooltip" role="tooltip">
                                <div class="variable-tooltip-title">
                                  <strong class="var-name">{segment.info.name}</strong>
                                  <span class="var-scope-badge">{segment.info.scope}</span>
                                </div>
                                {#if !segment.info.validName}
                                  <small class="var-warning-note">{invalidVariableWarning}</small>
                                {:else if variableTooltips.editing === segment.info.name}
                                  <textarea
                                    class="var-value-editor"
                                    aria-label={'Edit variable ' + segment.info.name}
                                    bind:value={variableTooltips.draft}
                                    onkeydown={(event) => handleVariableTooltipEditorKey(event, segment.info)}
                                    onblur={(event) => handleVariableTooltipEditorBlur(event, segment.info)}
                                  ></textarea>
                                  <div class="button-row compact">
                                    <button class="var-save-button" onclick={(event) => { event.stopPropagation(); saveVariableTooltipEdit(segment.info) }} disabled={busy !== ''}>Save</button>
                                    <button onclick={(event) => { event.stopPropagation(); cancelVariableTooltipEdit() }}>Cancel</button>
                                  </div>
                                {:else if segment.info.editable}
                                  <button type="button" class="var-value-editable-display" onclick={(event) => { event.stopPropagation(); beginVariableTooltipEdit(segment.info) }}>
                                    {displayTooltipValue(segment.info, variableTooltips.isRevealed(segment.info.name))}
                                  </button>
                                {:else}
                                  <div class="var-value-editable-display">{displayTooltipValue(segment.info, variableTooltips.isRevealed(segment.info.name))}</div>
                                {/if}
                                {#if segment.info.readOnly}
                                  <small class="var-readonly-note">read-only</small>
                                {/if}
                                <div class="button-row compact">
                                  <button
                                    class="copy-button"
                                    class:copy-success={variableTooltips.isCopied(segment.info.name)}
                                    onclick={(event) => { event.stopPropagation(); copyVariableTooltipValue(segment.info) }}
                                    disabled={!segment.info.found || !segment.info.validName || variableTooltips.isCopied(segment.info.name)}
                                  >
                                    {variableTooltips.isCopied(segment.info.name) ? 'Copied' : 'Copy'}
                                  </button>
                                  {#if segment.info.secret}
                                    <button class="secret-toggle-button" onclick={(event) => { event.stopPropagation(); toggleTooltipSecret(segment.info.name) }}>
                                      {variableTooltips.isRevealed(segment.info.name) ? 'Hide' : 'Show'}
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
            {/snippet}
          </RequestCommandStrip>
          <div class="request-side">
            <div class="request-variable-region">
            {#if requestVariableTooltips.length > 0}
              <div class="variable-inspector" aria-label="Variable inspector">
                {#each requestVariableTooltips as variableInfo (variableInfo.name)}
                  <div class="variable-chip-wrapper" class:invalid={!variableInfo.found} class:open={variableTooltips.active === variableInfo.name}>
                    <button type="button" class="variable-chip" onclick={() => toggleActiveVariableTooltip(variableInfo.name)}>
                      <span class="var-token">{'{{' + variableInfo.name + '}}'}</span>
                    </button>
                    <div class="variable-tooltip" role="tooltip">
                      <div class="variable-tooltip-title">
                        <strong class="var-name">{variableInfo.name}</strong>
                        <span class="var-scope-badge">{variableInfo.scope}</span>
                      </div>
                      {#if !variableInfo.validName}
                        <small class="var-warning-note">{invalidVariableWarning}</small>
                      {:else if variableTooltips.editing === variableInfo.name}
                        <textarea
                          class="var-value-editor"
                          aria-label={'Edit variable ' + variableInfo.name}
                          bind:value={variableTooltips.draft}
                          onkeydown={(event) => handleVariableTooltipEditorKey(event, variableInfo)}
                          onblur={(event) => handleVariableTooltipEditorBlur(event, variableInfo)}
                        ></textarea>
                        <div class="button-row compact">
                          <button class="var-save-button" onclick={(event) => { event.stopPropagation(); saveVariableTooltipEdit(variableInfo) }} disabled={busy !== ''}>Save</button>
                          <button onclick={(event) => { event.stopPropagation(); cancelVariableTooltipEdit() }}>Cancel</button>
                        </div>
                      {:else if variableInfo.editable}
                        <button type="button" class="var-value-editable-display" onclick={(event) => { event.stopPropagation(); beginVariableTooltipEdit(variableInfo) }}>
                          {displayTooltipValue(variableInfo, variableTooltips.isRevealed(variableInfo.name))}
                        </button>
                      {:else}
                        <div class="var-value-editable-display">{displayTooltipValue(variableInfo, variableTooltips.isRevealed(variableInfo.name))}</div>
                      {/if}
                      {#if variableInfo.readOnly}
                        <small class="var-readonly-note">read-only</small>
                      {/if}
                      <div class="button-row compact">
                        <button
                          class="copy-button"
                          class:copy-success={variableTooltips.isCopied(variableInfo.name)}
                          onclick={(event) => { event.stopPropagation(); copyVariableTooltipValue(variableInfo) }}
                          disabled={!variableInfo.found || !variableInfo.validName || variableTooltips.isCopied(variableInfo.name)}
                        >
                          {variableTooltips.isCopied(variableInfo.name) ? 'Copied' : 'Copy'}
                        </button>
                        {#if variableInfo.secret}
                          <button class="secret-toggle-button" onclick={(event) => { event.stopPropagation(); toggleTooltipSecret(variableInfo.name) }}>
                            {variableTooltips.isRevealed(variableInfo.name) ? 'Hide' : 'Show'}
                          </button>
                        {/if}
                      </div>
                    </div>
                  </div>
                {/each}
              </div>
            {/if}
            </div>

            <div class="subtabs" role="tablist" aria-label="Request sections" tabindex="-1" onkeydown={requestTabKeydown}>
              {#each requestTabs as tab (tab.id)}
                <button
                  class:active={requestPaneTab === tab.id}
                  id={`request-tab-${tab.id}`}
                  data-request-tab={tab.id}
                  role="tab"
                  aria-selected={requestPaneTab === tab.id}
                  aria-controls={`request-panel-${tab.id}`}
                  tabindex={requestPaneTab === tab.id ? 0 : -1}
                  onclick={() => selectRequestPaneTab(tab.id)}
                >
                  {tab.label}
                </button>
              {/each}
            </div>

	            <div class="editor-surface" id={`request-panel-${requestPaneTab}`} role="tabpanel" aria-labelledby={`request-tab-${requestPaneTab}`} tabindex="0">
	              {#if requestPaneTab === 'params'}
	                <div class="param-section-title">Query</div>
	                <KeyValueTable
	                  showBulkEdit={true}
	                  bulkLabel="Request params bulk edit"
	                  rows={activeRequest.params}
	                  variableOverlay={true}
                  {busy}
                  valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
                  {displayTooltipValue}
                  onEditorKey={handleVariableTooltipEditorKey}
                  onEditorBlur={handleVariableTooltipEditorBlur}
                  onSave={saveVariableTooltipEdit}
                  onCopy={copyVariableTooltipValue}
                  onAdd={() => addKeyValue('params')}
	                  onChange={(index, field, value) => updateKeyValue('params', index, field, value)}
	                  onBulkChange={(rows) => replaceKeyValues('params', rows as unknown as types.KeyValue[])}
	                  onRemove={(index) => removeKeyValue('params', index)}
	                />
	                {#if activeRequest.pathParams?.length}
	                  <div class="param-section-title">Path</div>
	                  <KeyValueTable
	                    showBulkEdit={true}
	                    bulkLabel="Request path params bulk edit"
	                    rows={activeRequest.pathParams}
	                    variableOverlay={true}
	                    readonlyNames={true}
	                    showEnabled={false}
	                    showActions={false}
	                    showAddRow={false}
	                    {busy}
	                    valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
	                    {displayTooltipValue}
	                    onEditorKey={handleVariableTooltipEditorKey}
	                    onEditorBlur={handleVariableTooltipEditorBlur}
	                    onSave={saveVariableTooltipEdit}
	                    onCopy={copyVariableTooltipValue}
	                    onChange={(index, field, value) => updateKeyValue('pathParams', index, field, value)}
                  onBulkChange={(rows) => replaceKeyValues('pathParams', rows as unknown as types.KeyValue[])}
	                  />
	                {/if}
	              {:else if requestPaneTab === 'headers'}
                <KeyValueTable
                  showBulkEdit={true}
                  bulkLabel="Request headers bulk edit"
                  rows={activeRequest.headers}
                  variableOverlay={true}
                  {busy}
                  valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
                  {displayTooltipValue}
                  onEditorKey={handleVariableTooltipEditorKey}
                  onEditorBlur={handleVariableTooltipEditorBlur}
                  onSave={saveVariableTooltipEdit}
                  onCopy={copyVariableTooltipValue}
                  onAdd={() => addKeyValue('headers')}
                  onChange={(index, field, value) => updateKeyValue('headers', index, field, value)}
                  onBulkChange={(rows) => replaceKeyValues('headers', rows as unknown as types.KeyValue[])}
                  onRemove={(index) => removeKeyValue('headers', index)}
                />
              {:else if requestPaneTab === 'body'}
                {#if activeRequest.type === 'grpc'}
                  <div class="grpc-editor">
                    <div class="grpc-method-controls">
                      <button onclick={loadGrpcMethods} disabled={busy !== ''}>Load methods</button>
                      <select aria-label="Discovered gRPC methods" value={activeRequest.method === 'CALL' ? '' : activeRequest.method} onchange={(e) => selectGrpcMethod(e.currentTarget.value)} disabled={grpcMethods.length === 0}>
                        <option value="">Select method</option>
                        {#each grpcMethods as method (method.path)}
                          <option value={method.path}>{method.path} · {method.type || 'unary'}</option>
                        {/each}
                      </select>
                      <button onclick={() => regenerateGrpcMessage(0)} disabled={busy !== '' || !activeRequest.method || activeRequest.method === 'CALL'}>Generate</button>
                    </div>
                    {#if grpcMethodMessage}
                      <small>{grpcMethodMessage}</small>
                    {/if}
                    <div class="field-grid">
                      <span class="field-label">Method type</span>
                      <select value={activeRequest.grpcMethodType} onchange={(e) => patchField('grpcMethodType', e.currentTarget.value)}>
                        {#each grpcMethodTypes as methodType (methodType)}
                          <option value={methodType}>{methodType || 'unspecified'}</option>
                        {/each}
                      </select>
                      <span class="field-label">Proto path</span>
                      <input value={activeRequest.protoPath} placeholder="protos/service.proto" onchange={(e) => patchField('protoPath', e.currentTarget.value)} />
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
                          <button onclick={connectActiveGrpcStream} disabled={busy !== '' || grpcStreamConnected}>Start stream</button>
                          <button onclick={endActiveGrpcStream} disabled={busy !== '' || !grpcStreamConnected}>End</button>
                          <button onclick={cancelActiveGrpcStream} disabled={busy !== '' || !grpcStreamConnected}>Cancel</button>
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
                            <td><input value={message.name} oninput={(e) => updateGrpcMessage(index, 'name', e.currentTarget.value)} /></td>
                            <td><textarea class="short" spellcheck="false" value={message.content} oninput={(e) => updateGrpcMessage(index, 'content', e.currentTarget.value)}></textarea></td>
                            <td>
                              <div class="button-row compact">
                                {#if grpcClientStreamingRequest}
                                  <button class="icon-button" title="Send message" onclick={() => sendGrpcStreamMessage(index)} disabled={busy !== '' || !grpcStreamConnected}>Send</button>
                                {/if}
                                <button class="icon-button" title="Generate sample" onclick={() => regenerateGrpcMessage(index)}>Gen</button>
                                <button class="icon-button" title="Remove message" onclick={() => removeGrpcMessage(index)}>x</button>
                              </div>
                            </td>
                          </tr>
                        {/each}
                      </tbody>
	                    </table>
	                    <button onclick={addGrpcMessage}>Add message</button>
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
	                        <button onclick={connectActiveWebSocket} disabled={busy !== ''}>Connect</button>
	                        <button onclick={() => sendWSMessage(selectedWSMessageIndex(activeRequest))} disabled={busy !== ''}>Send selected</button>
	                        <button onclick={disconnectActiveWebSocket} disabled={busy !== '' || !websocketConnected}>Disconnect</button>
	                      </div>
	                    </div>
	                    {#if (activeRequest.wsMessages ?? []).length === 0}
	                      <div class="empty-appState">No WebSocket messages</div>
	                    {:else}
	                      <table class="ws-messages">
	                        <thead>
	                          <tr><th>Send</th><th>Name</th><th>Type</th><th>Content</th><th></th></tr>
	                        </thead>
	                        <tbody>
	                          {#each activeRequest.wsMessages ?? [] as message, index (index)}
	                            <tr>
	                              <td><input type="checkbox" checked={message.selected} onchange={(e) => updateWSMessage(index, 'selected', e.currentTarget.checked)} /></td>
	                              <td><input value={message.name} onchange={(e) => updateWSMessage(index, 'name', e.currentTarget.value)} /></td>
	                              <td>
	                                <select value={message.type || 'text'} onchange={(e) => updateWSMessage(index, 'type', e.currentTarget.value)}>
	                                  {#each wsMessageTypes as messageType (messageType)}
	                                    <option value={messageType}>{messageType}</option>
	                                  {/each}
	                                </select>
	                              </td>
	                              <td><textarea class="short" spellcheck="false" value={message.content} onchange={(e) => updateWSMessage(index, 'content', e.currentTarget.value)}></textarea></td>
	                              <td>
	                                <div class="button-row compact">
	                                  <button class="icon-button" title="Send message" onclick={() => sendWSMessage(index)} disabled={busy !== ''}>Send</button>
	                                  <button class="icon-button" title="Remove message" onclick={() => removeWSMessage(index)}>x</button>
	                                </div>
	                              </td>
	                            </tr>
	                          {/each}
	                        </tbody>
	                      </table>
	                    {/if}
	                    <button onclick={addWSMessage}>Add message</button>
	                  </div>
	                {:else}
	                  <div class="field-row">
                    <span class="field-label">Body mode</span>
                    <select value={activeRequest.body.mode} onchange={(e) => updateBody({ mode: e.currentTarget.value })}>
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
	                      {busy}
	                      valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
	                      {displayTooltipValue}
	                      onEditorKey={handleVariableTooltipEditorKey}
	                      onEditorBlur={handleVariableTooltipEditorBlur}
	                      onSave={saveVariableTooltipEdit}
	                      onCopy={copyVariableTooltipValue}
	                      onAdd={addFormUrlEncodedRow}
	                      onChange={updateFormUrlEncodedRow}
	                      onRemove={removeFormUrlEncodedRow}
	                    />
	                  {:else if activeRequest.body.mode === 'multipartForm'}
	                    <MultipartTable
	                      rows={activeRequest.body.multipart ?? []}
	                      {busy}
	                      valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
	                      {displayTooltipValue}
	                      onEditorKey={handleVariableTooltipEditorKey}
	                      onEditorBlur={handleVariableTooltipEditorBlur}
	                      onSave={saveVariableTooltipEdit}
	                      onCopy={copyVariableTooltipValue}
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
                    <div class="empty-appState">No body payload</div>
                  {/if}
                {/if}
              {:else if requestPaneTab === 'auth'}
                <div class="field-grid">
                  <span class="field-label">Mode</span>
                  <select value={activeRequest.auth.mode} onchange={(e) => updateAuth({ mode: e.currentTarget.value })}>
                    {#each authModes as mode (mode)}
                      <option value={mode}>{mode}</option>
                    {/each}
                  </select>
	                  {#if activeRequest.auth.mode === 'basic' || activeRequest.auth.mode === 'digest' || activeRequest.auth.mode === 'wsse' || activeRequest.auth.mode === 'ntlm'}
                    <span class="field-label">Username</span>
                    <input value={activeRequest.auth.username} oninput={(e) => updateAuth({ username: e.currentTarget.value })} />
                    <span class="field-label">Password</span>
                    <input type="password" value={activeRequest.auth.password} oninput={(e) => updateAuth({ password: e.currentTarget.value })} />
                    {#if activeRequest.auth.mode === 'ntlm'}
                      <span class="field-label">Domain</span>
                      <input value={activeRequest.auth.domain} onchange={(e) => updateAuth({ domain: e.currentTarget.value })} />
                    {/if}
                  {:else if activeRequest.auth.mode === 'bearer'}
                    <span class="field-label">Token</span>
                    <input type="password" value={activeRequest.auth.token} oninput={(e) => updateAuth({ token: e.currentTarget.value })} />
                  {:else if activeRequest.auth.mode === 'oauth2'}
                    <span class="field-label">Grant</span>
                    <select value={activeRequest.auth.oauth2?.grantType || 'client_credentials'} onchange={(e) => updateOAuth2Auth({ grantType: e.currentTarget.value })}>
                      {#each oauth2GrantTypes as grant (grant)}
                        <option value={grant}>{grant}</option>
                      {/each}
                    </select>
                    {#if activeRequest.auth.oauth2?.grantType === 'authorization_code' || activeRequest.auth.oauth2?.grantType === 'implicit'}
                      <span class="field-label">Callback URL</span>
                      <input value={activeRequest.auth.oauth2?.callbackUrl ?? ''} onchange={(e) => updateOAuth2Auth({ callbackUrl: e.currentTarget.value })} />
                      <span class="field-label">Authorization URL</span>
                      <input value={activeRequest.auth.oauth2?.authorizationUrl ?? ''} onchange={(e) => updateOAuth2Auth({ authorizationUrl: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Access token URL</span>
                    <input value={activeRequest.auth.oauth2?.accessTokenUrl ?? ''} onchange={(e) => updateOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                    <span class="field-label">Client ID</span>
                    <input value={activeRequest.auth.oauth2?.clientId ?? ''} onchange={(e) => updateOAuth2Auth({ clientId: e.currentTarget.value })} />
                    <span class="field-label">Client secret</span>
                    <input type="password" value={activeRequest.auth.oauth2?.clientSecret ?? ''} onchange={(e) => updateOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                    {#if activeRequest.auth.oauth2?.grantType === 'password'}
                      <span class="field-label">Username</span>
                      <input value={activeRequest.auth.oauth2?.username ?? ''} onchange={(e) => updateOAuth2Auth({ username: e.currentTarget.value })} />
                      <span class="field-label">Password</span>
                      <input type="password" value={activeRequest.auth.oauth2?.password ?? ''} onchange={(e) => updateOAuth2Auth({ password: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Scope</span>
                    <input value={activeRequest.auth.oauth2?.scope ?? ''} onchange={(e) => updateOAuth2Auth({ scope: e.currentTarget.value })} />
                    {#if activeRequest.auth.oauth2?.grantType === 'authorization_code' || activeRequest.auth.oauth2?.grantType === 'implicit'}
                      <span class="field-label">State</span>
                      <input value={activeRequest.auth.oauth2?.state ?? ''} onchange={(e) => updateOAuth2Auth({ state: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Credentials</span>
                    <select value={activeRequest.auth.oauth2?.credentialsPlacement || 'basic_auth_header'} onchange={(e) => updateOAuth2Auth({ credentialsPlacement: e.currentTarget.value })}>
                      {#each oauth2CredentialPlacements as placement (placement)}
                        <option value={placement}>{placement}</option>
                      {/each}
                    </select>
                    {#if activeRequest.auth.oauth2?.grantType === 'authorization_code'}
                      <span class="field-label">PKCE</span>
                      <input type="checkbox" checked={activeRequest.auth.oauth2?.pkce ?? false} onchange={(e) => updateOAuth2Auth({ pkce: e.currentTarget.checked })} />
                    {/if}
                    <span class="field-label">Token source</span>
                    <select value={activeRequest.auth.oauth2?.tokenSource || 'access_token'} onchange={(e) => updateOAuth2Auth({ tokenSource: e.currentTarget.value })}>
                      {#each oauth2TokenSources as source (source)}
                        <option value={source}>{source}</option>
                      {/each}
                    </select>
                    <span class="field-label">Token placement</span>
                    <select value={activeRequest.auth.oauth2?.tokenPlacement || 'header'} onchange={(e) => updateOAuth2Auth({ tokenPlacement: e.currentTarget.value })}>
                      {#each oauth2TokenPlacements as placement (placement)}
                        <option value={placement}>{placement}</option>
                      {/each}
                    </select>
                    {#if (activeRequest.auth.oauth2?.tokenPlacement || 'header') === 'header'}
                      <span class="field-label">Header prefix</span>
                      <input value={activeRequest.auth.oauth2?.tokenHeaderPrefix || 'Bearer'} onchange={(e) => updateOAuth2Auth({ tokenHeaderPrefix: e.currentTarget.value })} />
                    {:else}
                      <span class="field-label">Query key</span>
                      <input value={activeRequest.auth.oauth2?.tokenQueryKey || 'access_token'} onchange={(e) => updateOAuth2Auth({ tokenQueryKey: e.currentTarget.value })} />
                    {/if}
                    <span class="field-label">Static token</span>
                    <input type="password" value={activeRequest.auth.token} oninput={(e) => updateAuth({ token: e.currentTarget.value })} />
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
                    <input value={activeRequest.auth.apiKey} onchange={(e) => updateAuth({ apiKey: e.currentTarget.value })} />
                    <span class="field-label">Value</span>
                    <input type="password" value={activeRequest.auth.apiValue} onchange={(e) => updateAuth({ apiValue: e.currentTarget.value })} />
                    <span class="field-label">Send in</span>
	                    <select value={activeRequest.auth.apiLocation} onchange={(e) => updateAuth({ apiLocation: e.currentTarget.value })}>
	                      <option value="header">Header</option>
	                      <option value="query">Query</option>
	                    </select>
	                  {:else if activeRequest.auth.mode === 'awsv4'}
	                    <span class="field-label">Access key ID</span>
	                    <input value={activeRequest.auth.awsv4?.accessKeyId ?? ''} onchange={(e) => updateAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
	                    <span class="field-label">Secret access key</span>
	                    <input type="password" value={activeRequest.auth.awsv4?.secretAccessKey ?? ''} onchange={(e) => updateAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
	                    <span class="field-label">Session token</span>
	                    <input type="password" value={activeRequest.auth.awsv4?.sessionToken ?? ''} onchange={(e) => updateAWSV4Auth({ sessionToken: e.currentTarget.value })} />
	                    <span class="field-label">Service</span>
	                    <input value={activeRequest.auth.awsv4?.service ?? ''} placeholder="execute-api" onchange={(e) => updateAWSV4Auth({ service: e.currentTarget.value })} />
	                    <span class="field-label">Region</span>
	                    <input value={activeRequest.auth.awsv4?.region ?? ''} placeholder="us-east-1" onchange={(e) => updateAWSV4Auth({ region: e.currentTarget.value })} />
		                    <span class="field-label">Profile</span>
		                    <input value={activeRequest.auth.awsv4?.profileName ?? ''} onchange={(e) => updateAWSV4Auth({ profileName: e.currentTarget.value })} />
		                  {:else if activeRequest.auth.mode === 'oauth1'}
		                    <span class="field-label">Consumer key</span>
		                    <input value={activeRequest.auth.oauth1?.consumerKey ?? ''} onchange={(e) => updateOAuth1Auth({ consumerKey: e.currentTarget.value })} />
		                    <span class="field-label">Consumer secret</span>
		                    <input type="password" value={activeRequest.auth.oauth1?.consumerSecret ?? ''} onchange={(e) => updateOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
		                    <span class="field-label">Token</span>
		                    <input value={activeRequest.auth.oauth1?.accessToken ?? ''} onchange={(e) => updateOAuth1Auth({ accessToken: e.currentTarget.value })} />
		                    <span class="field-label">Token secret</span>
		                    <input type="password" value={activeRequest.auth.oauth1?.accessTokenSecret ?? ''} onchange={(e) => updateOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
		                    <span class="field-label">Signature</span>
		                    <select value={activeRequest.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} onchange={(e) => updateOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
		                      {#each oauth1SignatureMethods as method (method)}
		                        <option value={method}>{method}</option>
		                      {/each}
		                    </select>
		                    <span class="field-label">Add params to</span>
		                    <select value={activeRequest.auth.oauth1?.placement || 'header'} onchange={(e) => updateOAuth1Auth({ placement: e.currentTarget.value })}>
		                      {#each oauth1Placements as placement (placement)}
		                        <option value={placement}>{placement}</option>
		                      {/each}
		                    </select>
		                    <span class="field-label">Callback URL</span>
		                    <input value={activeRequest.auth.oauth1?.callbackUrl ?? ''} onchange={(e) => updateOAuth1Auth({ callbackUrl: e.currentTarget.value })} />
		                    <span class="field-label">Verifier</span>
		                    <input value={activeRequest.auth.oauth1?.verifier ?? ''} onchange={(e) => updateOAuth1Auth({ verifier: e.currentTarget.value })} />
		                    <span class="field-label">Timestamp</span>
		                    <input value={activeRequest.auth.oauth1?.timestamp ?? ''} onchange={(e) => updateOAuth1Auth({ timestamp: e.currentTarget.value })} />
		                    <span class="field-label">Nonce</span>
		                    <input value={activeRequest.auth.oauth1?.nonce ?? ''} onchange={(e) => updateOAuth1Auth({ nonce: e.currentTarget.value })} />
		                    <span class="field-label">Version</span>
		                    <input value={activeRequest.auth.oauth1?.version ?? ''} placeholder="1.0" onchange={(e) => updateOAuth1Auth({ version: e.currentTarget.value })} />
		                    <span class="field-label">Realm</span>
		                    <input value={activeRequest.auth.oauth1?.realm ?? ''} onchange={(e) => updateOAuth1Auth({ realm: e.currentTarget.value })} />
		                    <span class="field-label">Private key</span>
		                    <textarea class="short" spellcheck="false" value={activeRequest.auth.oauth1?.privateKey ?? ''} onchange={(e) => updateOAuth1Auth({ privateKey: e.currentTarget.value })}></textarea>
		                    <span class="field-label">Private key type</span>
		                    <select value={activeRequest.auth.oauth1?.privateKeyType || 'text'} onchange={(e) => updateOAuth1Auth({ privateKeyType: e.currentTarget.value })}>
		                      <option value="text">text</option>
		                      <option value="file">file</option>
		                    </select>
		                    <span class="field-label">Body hash</span>
		                    <input type="checkbox" checked={activeRequest.auth.oauth1?.includeBodyHash ?? false} onchange={(e) => updateOAuth1Auth({ includeBodyHash: e.currentTarget.checked })} />
		                  {:else if activeRequest.auth.mode !== 'none'}
                    <div class="empty-appState wide">This auth mode is marked partial until its full backend signer is implemented.</div>
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
                        <td><input type="checkbox" checked={row.enabled} onchange={(e) => updateAssertion(index, 'enabled', e.currentTarget.checked)} /></td>
                        <td><input value={row.expression} onchange={(e) => updateAssertion(index, 'expression', e.currentTarget.value)} /></td>
                        <td><input value={row.operator} onchange={(e) => updateAssertion(index, 'operator', e.currentTarget.value)} /></td>
                        <td><input value={row.value} onchange={(e) => updateAssertion(index, 'value', e.currentTarget.value)} /></td>
                        <td><button class="icon-button" onclick={() => removeAssertion(index)}>x</button></td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
                <button onclick={addAssertion}>Add assertion</button>
              {:else if requestPaneTab === 'tests'}
                <CodeEditor editorKey={`${activeRequest.id}:tests`} value={activeRequest.tests} language="javascript" ariaLabel="Request tests" testId="request-tests-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => patchField('tests', value)} />
              {:else if requestPaneTab === 'docs'}
                <CodeEditor editorKey={`${activeRequest.id}:docs`} value={activeRequest.docs} language="markdown" ariaLabel="Request documentation" testId="request-docs-editor" fontSize={codeFontSize} variableInfo={requestVariableTooltips} onChange={(value) => patchField('docs', value)} />
              {:else if requestPaneTab === 'app'}
                <div class="empty-appState">Request app runtime surface</div>
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
            onmousedown={startResponseSplitResize}
            ondblclick={() => { responseSplit = DEFAULT_RESPONSE_SPLIT; persistWorkbenchLayout() }}
            oninput={(event) => (responseSplit = clampResponseSplit(Number(event.currentTarget.value) / 100))}
            onchange={persistWorkbenchLayout}
          />
          <div class="response-side">
            <div class="response-summary">
              <div class={`response-summary-status ${requestCommand.response.tone}`} aria-live="polite">
                <strong>{requestCommand.response.status}</strong>
                <span>{requestCommand.response.statusText}</span>
                <span>{requestCommand.response.duration}</span>
                <span>{requestCommand.response.size}</span>
              </div>
              <button title="Save response as example" onclick={saveResponseExample} disabled={!activeRequest.response || busy !== ''}>Example</button>
            </div>
            <div class="subtabs" role="tablist" aria-label="Response sections" tabindex="-1" onkeydown={responseTabKeydown}>
              {#each activeResponseTabs as tab (tab.id)}
                <button
                  class:active={responseTab === tab.id}
                  id={`response-tab-${tab.id}`}
                  data-response-tab={tab.id}
                  role="tab"
                  aria-selected={responseTab === tab.id}
                  aria-controls={`response-panel-${tab.id}`}
                  tabindex={responseTab === tab.id ? 0 : -1}
                  onclick={() => selectResponsePaneTab(tab.id)}
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
                  {visualizerDocument}
                  visualizerSandbox={visualizerSandboxAttribute}
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
                  <button class="primary" type="button" onclick={beginCreateResponseExample} disabled={busy !== ''}>New example</button>
                </div>
                {#if (activeRequest.examples ?? []).length === 0}
                  <div class="empty-appState">No response examples</div>
                {:else}
                  <div class="examples-list">
                    {#each activeRequest.examples ?? [] as example (responseExampleIdentifier(example))}
                      <article>
                        <header>
                          {#if editingResponseExampleID === responseExampleIdentifier(example)}
                            <div class="example-name-editor">
                              <input aria-label="Response example name" bind:value={responseExampleNameDraft} onkeydown={(event) => {
                                if (event.key === 'Escape') cancelRenameResponseExample()
                                if (event.key === 'Enter') void renameResponseExample(example)
                              }} />
                              <button onclick={() => renameResponseExample(example)} disabled={busy !== '' || !responseExampleNameDraft.trim()}>Rename</button>
                              <button onclick={cancelRenameResponseExample}>Cancel</button>
                            </div>
                          {:else}
                            <strong>{example.name}</strong>
                          {/if}
                          <span class={responseStatusClass(example.response.status)}>{example.response.status}</span>
                        </header>
                        <small>{example.request.method} {example.request.url}</small>
                        <div class="example-actions">
                          <button onclick={() => openResponseExampleTab(example)} disabled={busy !== ''}>Open tab</button>
                          <button onclick={() => beginRenameResponseExample(example)} disabled={busy !== ''}>Rename</button>
                          <button onclick={() => beginEditResponseExampleDetails(example)} disabled={busy !== ''}>Edit details</button>
                          <button onclick={() => beginGenerateResponseExampleCode(example)} disabled={busy !== ''}>Generate Code</button>
                          <button onclick={() => cloneResponseExample(example)} disabled={busy !== ''}>Clone</button>
                          <button class="danger-button" onclick={() => requestDeleteResponseExample(example)} disabled={busy !== ''}>Delete</button>
                        </div>
                        {#if deletingResponseExampleID === responseExampleIdentifier(example)}
                          <div class="example-delete-confirm">
                            <span>Delete example <strong>{example.name}</strong>?</span>
                            <button class="danger-button" onclick={() => deleteResponseExample(example)} disabled={busy !== ''}>Delete</button>
                            <button onclick={() => (deletingResponseExampleID = '')}>Cancel</button>
                          </div>
                        {/if}
                        {#if editingResponseExampleDetailsID === responseExampleIdentifier(example)}
                          {@const draft = responseExampleDrafts[responseExampleIdentifier(example)] ?? example}
                          <div class="example-editor">
                            <div class="param-section-title">Example details</div>
                            <div class="field-grid example-editor-grid">
                              <span class="field-label">Description</span>
                              <textarea aria-label="Example description" class="short" spellcheck="false" value={draft.description ?? ''} oninput={(event) => updateResponseExampleDescription(example, event.currentTarget.value)}></textarea>
                            </div>
                            <div class="param-section-title">Request snapshot</div>
                            <div class="field-grid example-editor-grid">
                              <span class="field-label">Method</span>
                              <select aria-label="Example request method" value={draft.request?.method || 'GET'} onchange={(event) => updateResponseExampleRequestField(example, 'method', event.currentTarget.value)}>
                                {#each methods as method (method)}
                                  <option value={method}>{method}</option>
                                {/each}
                              </select>
                              <span class="field-label">URL</span>
                              <input aria-label="Example request URL" value={draft.request?.url ?? ''} oninput={(event) => updateResponseExampleRequestField(example, 'url', event.currentTarget.value)} />
                              <span class="field-label">Body mode</span>
                              <select aria-label="Example request body mode" value={draft.request?.bodyMode || 'none'} onchange={(event) => updateResponseExampleRequestField(example, 'bodyMode', event.currentTarget.value)}>
                                {#each bodyModes as mode (mode)}
                                  <option value={mode}>{mode}</option>
                                {/each}
                              </select>
                              {#if draft.request?.bodyMode !== 'formUrlEncoded'}
                                <span class="field-label">Body</span>
                                <textarea aria-label="Example request body" spellcheck="false" value={draft.request?.body ?? ''} oninput={(event) => updateResponseExampleRequestField(example, 'body', event.currentTarget.value)}></textarea>
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
                                <button onclick={() => prettifyResponseExampleRequestBody(example)} disabled={busy !== ''}>Prettify request JSON</button>
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
                              <input aria-label="Example response status" type="number" value={draft.response.status} oninput={(event) => updateResponseExampleResponseField(example, 'status', event.currentTarget.value)} />
                              <span class="field-label">Status text</span>
                              <input aria-label="Example response status text" value={draft.response.statusText} oninput={(event) => updateResponseExampleResponseField(example, 'statusText', event.currentTarget.value)} />
                              <span class="field-label">Body type</span>
                              <select aria-label="Example response body type" value={draft.response.bodyType} onchange={(event) => updateResponseExampleResponseField(example, 'bodyType', event.currentTarget.value)}>
                                {#each responseExampleBodyTypes as bodyType (bodyType)}
                                  <option value={bodyType}>{bodyType}</option>
                                {/each}
                              </select>
                              <span class="field-label">Body</span>
                              <textarea aria-label="Example response body" spellcheck="false" value={draft.response.body} oninput={(event) => updateResponseExampleResponseField(example, 'body', event.currentTarget.value)}></textarea>
                            </div>
                            {#if draft.response.bodyType === 'json'}
                              <div class="button-row compact">
                                <button onclick={() => prettifyResponseExampleResponseBody(example)} disabled={busy !== ''}>Prettify response JSON</button>
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
                              <button onclick={() => saveResponseExampleDetails(example)} disabled={busy !== ''}>Save details</button>
                              <button onclick={() => cancelEditResponseExampleDetails(example)} disabled={busy !== ''}>Cancel</button>
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
            <button onclick={refreshCollection}>Refresh active</button>
          </header>
          <nav class="subtabs">
            {#each collectionTabs as tab (tab.id)}
              <button class:active={collectionTab === tab.id} onclick={() => (collectionTab = tab.id)}>
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
                      onclick={() => updateCollectionSandboxMode('safe')}
                      disabled={busy !== '' || activeCollection.notFoundLocally}
                    >
                      Safe Mode
                    </button>
                    <button
                      type="button"
                      class:active={collectionSandboxMode(activeCollection) === 'developer'}
                      aria-pressed={collectionSandboxMode(activeCollection) === 'developer'}
                      data-testid="sandbox-mode-developer"
                      onclick={() => updateCollectionSandboxMode('developer')}
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
                  <button type="button" data-testid="collection-actions-clone" onclick={openCloneCollectionModal} disabled={busy !== '' || activeCollection.notFoundLocally}>Clone</button>
                  <button type="button" data-testid="collection-actions-rename" onclick={openRenameCollectionModal} disabled={busy !== '' || activeCollection.notFoundLocally}>Rename</button>
                </div>
                <span class="field-label">Folders</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection folders</span>
                    </div>
                    <p class="version-summary">{activeCollection.folders?.length ?? 0} configured</p>
                  </div>
                  <button type="button" data-testid="collection-actions-new-folder" onclick={() => openNewFolderModal()} disabled={busy !== '' || activeCollection.notFoundLocally}>New Folder</button>
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
                  <button type="button" data-testid="generate-docs-overview-button" onclick={openGenerateDocsModal}>Generate Docs</button>
                </div>
                <span class="field-label">Share</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Collection export</span>
                    </div>
                    <p class="version-summary">ZIP, YAML, or Postman</p>
                  </div>
                  <button type="button" data-testid="collection-actions-share" onclick={openShareCollectionModal}>Share Collection</button>
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
                        oninput={(event) => (openAPISyncSourceURL = event.currentTarget.value)}
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
                      oninput={(event) => (openAPISyncContent = event.currentTarget.value)}
                    ></textarea>
                  </label>
                  <div class="openapi-sync-actions">
                    <button type="button" data-testid="openapi-sync-connect" onclick={connectOpenAPISync} disabled={busy !== '' || activeCollection.notFoundLocally}>Connect</button>
	                    <button type="button" data-testid="openapi-sync-check" onclick={checkOpenAPISync} disabled={busy !== '' || activeCollection.notFoundLocally}>Check Updates</button>
			                    <button type="button" class="primary" data-testid="openapi-sync-apply" onclick={applyOpenAPISync} disabled={busy !== '' || activeCollection.notFoundLocally}>Apply Sync</button>
			                    {#if activeCollection.openapi?.length}
			                      <button type="button" data-testid="openapi-sync-view-spec" onclick={viewOpenAPISyncSpec} disabled={busy !== ''}>View spec</button>
			                      <button type="button" data-testid="openapi-sync-view-spec-diff-main" onclick={viewOpenAPISyncSpecDiff} disabled={busy !== '' || activeCollection.notFoundLocally}>View Spec Diff</button>
			                      <button type="button" data-testid="openapi-sync-settings-open" onclick={openOpenAPISyncSettings} disabled={busy !== ''}>Settings</button>
			                      <button type="button" data-testid="openapi-sync-disconnect" onclick={disconnectOpenAPISync} disabled={busy !== ''}>Disconnect</button>
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
	                          <button type="button" data-testid="openapi-sync-view-spec-diff" onclick={viewOpenAPISyncSpecDiff}>View Spec Diff</button>
	                          <button type="button" data-testid="openapi-sync-skip-all" onclick={() => setOpenAPISyncAllEndpointDecisions('keep-mine')}>Skip All</button>
	                          <button type="button" data-testid="openapi-sync-accept-all" onclick={() => setOpenAPISyncAllEndpointDecisions('accept-incoming')}>Accept All</button>
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
                            onchange={(event) => setOpenAPISyncEndpointDecision(change.id, event.currentTarget.value)}
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
                          <button type="button" data-testid="openapi-local-drift-check" onclick={checkOpenAPILocalDrift} disabled={busy !== '' || activeCollection.notFoundLocally}>Check Collection</button>
                          {#if openAPILocalDriftResult?.hasChanges}
                            <button type="button" class="primary" data-testid="openapi-local-drift-revert-all" onclick={revertAllOpenAPILocalDrift} disabled={busy !== '' || activeCollection.notFoundLocally}>Revert All to Spec</button>
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
                              <button type="button" data-testid="openapi-local-drift-reset-all" onclick={() => resetOpenAPILocalDrift()} disabled={busy !== ''}>Reset All</button>
                            {/if}
                            {#if openAPILocalDriftResult.missing}
                              <button type="button" data-testid="openapi-local-drift-restore-all" onclick={() => restoreOpenAPILocalDrift()} disabled={busy !== ''}>Restore All</button>
                            {/if}
                            {#if openAPILocalDriftResult.localOnly}
                              <button type="button" data-testid="openapi-local-drift-delete-all" onclick={() => deleteOpenAPILocalDrift()} disabled={busy !== ''}>Delete All</button>
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
                                  <button type="button" class="link-button openapi-sync-open-request" data-testid="openapi-local-drift-open" onclick={() => openOpenAPILocalDriftRequest(change)} disabled={busy !== ''}>Open</button>
                                {/if}
                              </span>
                              {#if change.change === 'modified'}
                                <button type="button" data-testid="openapi-local-drift-reset" onclick={() => resetOpenAPILocalDrift(change.id)} disabled={busy !== ''}>Reset</button>
                              {:else if change.change === 'missing'}
                                <button type="button" data-testid="openapi-local-drift-restore" onclick={() => restoreOpenAPILocalDrift(change.id)} disabled={busy !== ''}>Restore</button>
                              {:else}
                                <button type="button" data-testid="openapi-local-drift-delete" onclick={() => deleteOpenAPILocalDrift(change.id)} disabled={busy !== ''}>Delete</button>
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
                  <button type="button" data-testid="collection-actions-open-terminal" onclick={openCollectionInTerminal} disabled={busy !== '' || terminalBusy || activeCollection.notFoundLocally}>Open in Terminal</button>
                </div>
                <span class="field-label">Folder</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Local files</span>
                    </div>
                    <p class="version-summary" data-testid="collection-reveal-status">{revealCollectionMessage || activeCollection.path}</p>
                  </div>
                  <button type="button" data-testid="collection-actions-show-in-folder" onclick={revealCollectionInFolder} disabled={busy !== '' || activeCollection.notFoundLocally}>{revealInFolderLabel()}</button>
                </div>
                <span class="field-label">Remove</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line">
                      <span class="version-label">Workspace mount</span>
                    </div>
                    <p class="version-summary">Remove this collection from the workspace without deleting files</p>
                  </div>
                  <button type="button" data-testid="collection-actions-remove" onclick={openRemoveCollectionModal} disabled={busy !== '' || activeCollection.scratch}>Remove Collection</button>
                </div>
                <span class="field-label">Git</span>
                <div class="collection-doc-actions">
                  <div class="version-info">
                    <div class="version-line"><span class="version-label">Local Git workbench</span></div>
                    <p class="version-summary">Review scoped changes, commits, branches, and remotes without opening a terminal.</p>
                  </div>
                  <button type="button" data-testid="collection-actions-open-git" onclick={openGitWorkbench} disabled={busy !== '' || activeCollection.notFoundLocally}>Open Git Workbench</button>
                </div>
                <span class="field-label">Docs</span>
                <textarea spellcheck="false" value={activeCollection.docs} onchange={(e) => updateCollectionDocs(e.currentTarget.value)}></textarea>
              </div>
            {:else if collectionTab === 'folders'}
              {#if (activeCollection.folders ?? []).length === 0}
                <div class="empty-appState">No folders in this collection</div>
              {:else if editableFolder}
                <div class="settings-stack folder-settings-panel">
                  <div class="field-grid folder-picker">
                    <span class="field-label">Folder</span>
                    <select aria-label="Folder settings folder" value={editableFolder.path} onchange={(e) => (selectedFolderPath = e.currentTarget.value)}>
                      {#each activeCollection.folders ?? [] as folder (folder.path)}
                        <option value={folder.path}>{folder.displayPath || folder.path}</option>
                      {/each}
                    </select>
                    <span class="field-label">Path</span>
                    <code>{editableFolder.path}</code>
                  </div>

                  <nav class="subtabs compact" aria-label="Folder settings tabs">
                    {#each folderSettingsTabs as tab (tab.id)}
                      <button type="button" class:active={folderSettingsTab === tab.id} onclick={() => (folderSettingsTab = tab.id)}>{tab.label}</button>
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
                      <button onclick={() => addFolderVariable('variables')}>Add variable</button>
                    </div>
                    <div class="table-scroll">
                      <table>
                        <thead><tr><th>On</th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                        <tbody>
                          {#each editableFolder.variables ?? [] as variable, index (variable.id)}
                            <tr>
                              <td><input type="checkbox" checked={variable.enabled} onchange={(e) => updateFolderVariable('variables', index, 'enabled', e.currentTarget.checked)} /></td>
                              <td><input aria-label="Folder pre-request variable name" value={variable.name} onchange={(e) => updateFolderVariable('variables', index, 'name', e.currentTarget.value)} /></td>
                              <td><input aria-label="Folder pre-request variable value" value={String(variable.value ?? '')} onchange={(e) => updateFolderVariable('variables', index, 'value', e.currentTarget.value)} /></td>
                              <td>
                                <select aria-label="Folder pre-request variable type" value={variable.dataType || variable.type || 'string'} onchange={(e) => updateFolderVariable('variables', index, 'dataType', e.currentTarget.value)}>
                                  <option value="string">string</option>
                                  <option value="number">number</option>
                                  <option value="boolean">boolean</option>
                                  <option value="object">object</option>
                                </select>
                              </td>
                              <td><input aria-label="Folder pre-request variable secret" type="checkbox" checked={variable.secret} onchange={(e) => updateFolderVariable('variables', index, 'secret', e.currentTarget.checked)} /></td>
                              <td><button onclick={() => removeFolderVariable('variables', index)}>Remove</button></td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    </div>
                    {#if (editableFolder.variables ?? []).length === 0}
                      <div class="empty-appState">No pre-request variables</div>
                    {/if}

                    <div class="settings-section-header">
                      <h3>Post Response</h3>
                      <button onclick={() => addFolderVariable('resVariables')}>Add variable</button>
                    </div>
                    <div class="table-scroll">
                      <table>
                        <thead><tr><th>On</th><th>Name</th><th>Expression</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                        <tbody>
                          {#each editableFolder.resVariables ?? [] as variable, index (variable.id)}
                            <tr>
                              <td><input type="checkbox" checked={variable.enabled} onchange={(e) => updateFolderVariable('resVariables', index, 'enabled', e.currentTarget.checked)} /></td>
                              <td><input aria-label="Folder post-response variable name" value={variable.name} onchange={(e) => updateFolderVariable('resVariables', index, 'name', e.currentTarget.value)} /></td>
                              <td><input aria-label="Folder post-response variable expression" value={String(variable.value ?? '')} onchange={(e) => updateFolderVariable('resVariables', index, 'value', e.currentTarget.value)} /></td>
                              <td>
                                <select aria-label="Folder post-response variable type" value={variable.dataType || variable.type || 'string'} onchange={(e) => updateFolderVariable('resVariables', index, 'dataType', e.currentTarget.value)}>
                                  <option value="string">string</option>
                                  <option value="number">number</option>
                                  <option value="boolean">boolean</option>
                                  <option value="object">object</option>
                                </select>
                              </td>
                              <td><input aria-label="Folder post-response variable secret" type="checkbox" checked={variable.secret} onchange={(e) => updateFolderVariable('resVariables', index, 'secret', e.currentTarget.checked)} /></td>
                              <td><button onclick={() => removeFolderVariable('resVariables', index)}>Remove</button></td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    </div>
                    {#if (editableFolder.resVariables ?? []).length === 0}
                      <div class="empty-appState">No post-response variables</div>
                    {/if}
                  {:else if folderSettingsTab === 'auth'}
                    <div class="field-grid auth-grid">
                      <span class="field-label">Mode</span>
                      <select aria-label="Folder auth mode" value={editableFolder.auth?.mode || ''} onchange={(e) => updateFolderAuth({ mode: e.currentTarget.value })}>
                        <option value="">Unset</option>
                        {#each authModes as mode (mode)}
                          <option value={mode}>{mode}</option>
                        {/each}
                      </select>
                      {#if editableFolder.auth?.mode === 'basic' || editableFolder.auth?.mode === 'digest' || editableFolder.auth?.mode === 'wsse' || editableFolder.auth?.mode === 'ntlm'}
                        <span class="field-label">Username</span>
                        <input value={editableFolder.auth.username ?? ''} onchange={(e) => updateFolderAuth({ username: e.currentTarget.value })} />
                        <span class="field-label">Password</span>
                        <input type="password" value={editableFolder.auth.password ?? ''} onchange={(e) => updateFolderAuth({ password: e.currentTarget.value })} />
                        {#if editableFolder.auth?.mode === 'ntlm'}
                          <span class="field-label">Domain</span>
                          <input value={editableFolder.auth.domain ?? ''} onchange={(e) => updateFolderAuth({ domain: e.currentTarget.value })} />
                        {/if}
                      {:else if editableFolder.auth?.mode === 'bearer'}
                        <span class="field-label">Token</span>
                        <input type="password" value={editableFolder.auth.token ?? ''} onchange={(e) => updateFolderAuth({ token: e.currentTarget.value })} />
                      {:else if editableFolder.auth?.mode === 'apikey'}
                        <span class="field-label">Key</span>
                        <input value={editableFolder.auth.apiKey ?? ''} onchange={(e) => updateFolderAuth({ apiKey: e.currentTarget.value })} />
                        <span class="field-label">Value</span>
                        <input type="password" value={editableFolder.auth.apiValue ?? ''} onchange={(e) => updateFolderAuth({ apiValue: e.currentTarget.value })} />
                        <span class="field-label">Placement</span>
                        <select value={editableFolder.auth.apiLocation || 'header'} onchange={(e) => updateFolderAuth({ apiLocation: e.currentTarget.value })}>
                          <option value="header">Header</option>
                          <option value="queryparams">Query params</option>
                        </select>
                      {:else if editableFolder.auth?.mode === 'awsv4'}
                        <span class="field-label">Access key</span>
                        <input value={editableFolder.auth.awsv4?.accessKeyId ?? ''} onchange={(e) => updateFolderAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
                        <span class="field-label">Secret key</span>
                        <input type="password" value={editableFolder.auth.awsv4?.secretAccessKey ?? ''} onchange={(e) => updateFolderAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
                        <span class="field-label">Service</span>
                        <input value={editableFolder.auth.awsv4?.service ?? ''} placeholder="execute-api" onchange={(e) => updateFolderAWSV4Auth({ service: e.currentTarget.value })} />
                        <span class="field-label">Region</span>
                        <input value={editableFolder.auth.awsv4?.region ?? ''} placeholder="us-east-1" onchange={(e) => updateFolderAWSV4Auth({ region: e.currentTarget.value })} />
                      {:else if editableFolder.auth?.mode === 'oauth1'}
                        <span class="field-label">Consumer key</span>
                        <input value={editableFolder.auth.oauth1?.consumerKey ?? ''} onchange={(e) => updateFolderOAuth1Auth({ consumerKey: e.currentTarget.value })} />
                        <span class="field-label">Consumer secret</span>
                        <input type="password" value={editableFolder.auth.oauth1?.consumerSecret ?? ''} onchange={(e) => updateFolderOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
                        <span class="field-label">Access token</span>
                        <input value={editableFolder.auth.oauth1?.accessToken ?? ''} onchange={(e) => updateFolderOAuth1Auth({ accessToken: e.currentTarget.value })} />
                        <span class="field-label">Token secret</span>
                        <input type="password" value={editableFolder.auth.oauth1?.accessTokenSecret ?? ''} onchange={(e) => updateFolderOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
                        <span class="field-label">Signature</span>
                        <select value={editableFolder.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} onchange={(e) => updateFolderOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
                          {#each oauth1SignatureMethods as method (method)}
                            <option value={method}>{method}</option>
                          {/each}
                        </select>
                      {:else if editableFolder.auth?.mode === 'oauth2'}
                        <span class="field-label">Grant type</span>
                        <select value={editableFolder.auth.oauth2?.grantType || 'client_credentials'} onchange={(e) => updateFolderOAuth2Auth({ grantType: e.currentTarget.value })}>
                          {#each oauth2GrantTypes as grantType (grantType)}
                            <option value={grantType}>{grantType}</option>
                          {/each}
                        </select>
                        <span class="field-label">Access token URL</span>
                        <input value={editableFolder.auth.oauth2?.accessTokenUrl ?? ''} onchange={(e) => updateFolderOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                        <span class="field-label">Client ID</span>
                        <input value={editableFolder.auth.oauth2?.clientId ?? ''} onchange={(e) => updateFolderOAuth2Auth({ clientId: e.currentTarget.value })} />
                        <span class="field-label">Client secret</span>
                        <input type="password" value={editableFolder.auth.oauth2?.clientSecret ?? ''} onchange={(e) => updateFolderOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                        <span class="field-label">Scope</span>
                        <input value={editableFolder.auth.oauth2?.scope ?? ''} onchange={(e) => updateFolderOAuth2Auth({ scope: e.currentTarget.value })} />
                        <span class="field-label">Token</span>
                        <input type="password" value={editableFolder.auth.token ?? ''} onchange={(e) => updateFolderAuth({ token: e.currentTarget.value })} />
                      {/if}
                    </div>
                  {:else if folderSettingsTab === 'script'}
                    <span class="field-label">Pre-request</span>
                    <textarea class="short" spellcheck="false" value={editableFolder.preScript ?? ''} onchange={(e) => updateFolderScript('preScript', e.currentTarget.value)}></textarea>
                    <span class="field-label">Post-response</span>
                    <textarea class="short" spellcheck="false" value={editableFolder.postScript ?? ''} onchange={(e) => updateFolderScript('postScript', e.currentTarget.value)}></textarea>
                  {:else if folderSettingsTab === 'tests'}
                    <textarea spellcheck="false" value={editableFolder.tests ?? ''} onchange={(e) => updateFolderScript('tests', e.currentTarget.value)}></textarea>
                  {:else if folderSettingsTab === 'docs'}
                    <textarea spellcheck="false" value={editableFolder.docs ?? ''} onchange={(e) => updateFolderDocs(e.currentTarget.value)}></textarea>
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
                      <td><input type="checkbox" checked={variable.enabled} onchange={(e) => updateCollectionVariable(index, 'enabled', e.currentTarget.checked)} /></td>
                      <td><input value={variable.name} onchange={(e) => updateCollectionVariable(index, 'name', e.currentTarget.value)} /></td>
                      <td><input value={String(variable.value ?? '')} onchange={(e) => updateCollectionVariable(index, 'value', e.currentTarget.value)} /></td>
                      <td>
                        <select value={variable.dataType || 'string'} onchange={(e) => updateCollectionVariable(index, 'dataType', e.currentTarget.value)}>
                          <option value="string">string</option>
                          <option value="number">number</option>
                          <option value="boolean">boolean</option>
                          <option value="object">object</option>
                        </select>
                      </td>
                      <td><input type="checkbox" checked={variable.secret} onchange={(e) => updateCollectionVariable(index, 'secret', e.currentTarget.checked)} /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              <button onclick={addCollectionVariable}>Add variable</button>
            {:else if collectionTab === 'auth'}
              <div class="field-grid auth-grid">
                <span class="field-label">Mode</span>
                <select value={activeCollection.auth?.mode ?? 'none'} onchange={(e) => updateCollectionAuth({ mode: e.currentTarget.value })}>
                  {#each authModes as mode (mode)}
                    <option value={mode}>{mode}</option>
                  {/each}
                </select>
	                {#if activeCollection.auth?.mode === 'basic' || activeCollection.auth?.mode === 'digest' || activeCollection.auth?.mode === 'wsse' || activeCollection.auth?.mode === 'ntlm'}
                  <span class="field-label">Username</span>
                  <input value={activeCollection.auth.username} onchange={(e) => updateCollectionAuth({ username: e.currentTarget.value })} />
                  <span class="field-label">Password</span>
                  <input type="password" value={activeCollection.auth.password} onchange={(e) => updateCollectionAuth({ password: e.currentTarget.value })} />
                  {#if activeCollection.auth?.mode === 'ntlm'}
                    <span class="field-label">Domain</span>
                    <input value={activeCollection.auth.domain} onchange={(e) => updateCollectionAuth({ domain: e.currentTarget.value })} />
                  {/if}
                {:else if activeCollection.auth?.mode === 'bearer'}
                  <span class="field-label">Token</span>
                  <input type="password" value={activeCollection.auth.token} onchange={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
                {:else if activeCollection.auth?.mode === 'oauth2'}
                  <span class="field-label">Grant</span>
                  <select value={activeCollection.auth.oauth2?.grantType || 'client_credentials'} onchange={(e) => updateCollectionOAuth2Auth({ grantType: e.currentTarget.value })}>
                    {#each oauth2GrantTypes as grant (grant)}
                      <option value={grant}>{grant}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">Callback URL</span>
                    <input value={activeCollection.auth.oauth2?.callbackUrl ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ callbackUrl: e.currentTarget.value })} />
                    <span class="field-label">Authorization URL</span>
                    <input value={activeCollection.auth.oauth2?.authorizationUrl ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ authorizationUrl: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Access token URL</span>
                  <input value={activeCollection.auth.oauth2?.accessTokenUrl ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                  <span class="field-label">Client ID</span>
                  <input value={activeCollection.auth.oauth2?.clientId ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ clientId: e.currentTarget.value })} />
                  <span class="field-label">Client secret</span>
                  <input type="password" value={activeCollection.auth.oauth2?.clientSecret ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'password'}
                    <span class="field-label">Username</span>
                    <input value={activeCollection.auth.oauth2?.username ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ username: e.currentTarget.value })} />
                    <span class="field-label">Password</span>
                    <input type="password" value={activeCollection.auth.oauth2?.password ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ password: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Scope</span>
                  <input value={activeCollection.auth.oauth2?.scope ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ scope: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">State</span>
                    <input value={activeCollection.auth.oauth2?.state ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ state: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Credentials</span>
                  <select value={activeCollection.auth.oauth2?.credentialsPlacement || 'basic_auth_header'} onchange={(e) => updateCollectionOAuth2Auth({ credentialsPlacement: e.currentTarget.value })}>
                    {#each oauth2CredentialPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code'}
                    <span class="field-label">PKCE</span>
                    <input type="checkbox" checked={activeCollection.auth.oauth2?.pkce ?? false} onchange={(e) => updateCollectionOAuth2Auth({ pkce: e.currentTarget.checked })} />
                  {/if}
                  <span class="field-label">Token source</span>
                  <select value={activeCollection.auth.oauth2?.tokenSource || 'access_token'} onchange={(e) => updateCollectionOAuth2Auth({ tokenSource: e.currentTarget.value })}>
                    {#each oauth2TokenSources as source (source)}
                      <option value={source}>{source}</option>
                    {/each}
                  </select>
                  <span class="field-label">Token placement</span>
                  <select value={activeCollection.auth.oauth2?.tokenPlacement || 'header'} onchange={(e) => updateCollectionOAuth2Auth({ tokenPlacement: e.currentTarget.value })}>
                    {#each oauth2TokenPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if (activeCollection.auth.oauth2?.tokenPlacement || 'header') === 'header'}
                    <span class="field-label">Header prefix</span>
                    <input value={activeCollection.auth.oauth2?.tokenHeaderPrefix || 'Bearer'} onchange={(e) => updateCollectionOAuth2Auth({ tokenHeaderPrefix: e.currentTarget.value })} />
                  {:else}
                    <span class="field-label">Query key</span>
                    <input value={activeCollection.auth.oauth2?.tokenQueryKey || 'access_token'} onchange={(e) => updateCollectionOAuth2Auth({ tokenQueryKey: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Static token</span>
                  <input type="password" value={activeCollection.auth.token} onchange={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
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
                  <input value={activeCollection.auth.apiKey} onchange={(e) => updateCollectionAuth({ apiKey: e.currentTarget.value })} />
                  <span class="field-label">Value</span>
                  <input type="password" value={activeCollection.auth.apiValue} onchange={(e) => updateCollectionAuth({ apiValue: e.currentTarget.value })} />
                  <span class="field-label">Send in</span>
	                  <select value={activeCollection.auth.apiLocation || 'header'} onchange={(e) => updateCollectionAuth({ apiLocation: e.currentTarget.value })}>
	                    <option value="header">Header</option>
	                    <option value="query">Query</option>
	                  </select>
	                {:else if activeCollection.auth?.mode === 'awsv4'}
	                  <span class="field-label">Access key ID</span>
	                  <input value={activeCollection.auth.awsv4?.accessKeyId ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
	                  <span class="field-label">Secret access key</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.secretAccessKey ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
	                  <span class="field-label">Session token</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.sessionToken ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ sessionToken: e.currentTarget.value })} />
	                  <span class="field-label">Service</span>
	                  <input value={activeCollection.auth.awsv4?.service ?? ''} placeholder="execute-api" onchange={(e) => updateCollectionAWSV4Auth({ service: e.currentTarget.value })} />
	                  <span class="field-label">Region</span>
	                  <input value={activeCollection.auth.awsv4?.region ?? ''} placeholder="us-east-1" onchange={(e) => updateCollectionAWSV4Auth({ region: e.currentTarget.value })} />
		                  <span class="field-label">Profile</span>
		                  <input value={activeCollection.auth.awsv4?.profileName ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ profileName: e.currentTarget.value })} />
		                {:else if activeCollection.auth?.mode === 'oauth1'}
		                  <span class="field-label">Consumer key</span>
		                  <input value={activeCollection.auth.oauth1?.consumerKey ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ consumerKey: e.currentTarget.value })} />
		                  <span class="field-label">Consumer secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.consumerSecret ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
		                  <span class="field-label">Token</span>
		                  <input value={activeCollection.auth.oauth1?.accessToken ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ accessToken: e.currentTarget.value })} />
		                  <span class="field-label">Token secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.accessTokenSecret ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
		                  <span class="field-label">Signature</span>
		                  <select value={activeCollection.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} onchange={(e) => updateCollectionOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
		                    {#each oauth1SignatureMethods as method (method)}
		                      <option value={method}>{method}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Add params to</span>
		                  <select value={activeCollection.auth.oauth1?.placement || 'header'} onchange={(e) => updateCollectionOAuth1Auth({ placement: e.currentTarget.value })}>
		                    {#each oauth1Placements as placement (placement)}
		                      <option value={placement}>{placement}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Callback URL</span>
		                  <input value={activeCollection.auth.oauth1?.callbackUrl ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ callbackUrl: e.currentTarget.value })} />
		                  <span class="field-label">Verifier</span>
		                  <input value={activeCollection.auth.oauth1?.verifier ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ verifier: e.currentTarget.value })} />
		                  <span class="field-label">Timestamp</span>
		                  <input value={activeCollection.auth.oauth1?.timestamp ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ timestamp: e.currentTarget.value })} />
		                  <span class="field-label">Nonce</span>
		                  <input value={activeCollection.auth.oauth1?.nonce ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ nonce: e.currentTarget.value })} />
		                  <span class="field-label">Version</span>
		                  <input value={activeCollection.auth.oauth1?.version ?? ''} placeholder="1.0" onchange={(e) => updateCollectionOAuth1Auth({ version: e.currentTarget.value })} />
		                  <span class="field-label">Realm</span>
		                  <input value={activeCollection.auth.oauth1?.realm ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ realm: e.currentTarget.value })} />
		                  <span class="field-label">Private key</span>
		                  <textarea class="short" spellcheck="false" value={activeCollection.auth.oauth1?.privateKey ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ privateKey: e.currentTarget.value })}></textarea>
		                  <span class="field-label">Private key type</span>
		                  <select value={activeCollection.auth.oauth1?.privateKeyType || 'text'} onchange={(e) => updateCollectionOAuth1Auth({ privateKeyType: e.currentTarget.value })}>
		                    <option value="text">text</option>
		                    <option value="file">file</option>
		                  </select>
		                  <span class="field-label">Body hash</span>
		                  <input type="checkbox" checked={activeCollection.auth.oauth1?.includeBodyHash ?? false} onchange={(e) => updateCollectionOAuth1Auth({ includeBodyHash: e.currentTarget.checked })} />
		                {:else if activeCollection.auth?.mode !== 'none'}
		                  <div class="empty-appState wide">This collection auth mode is marked partial until its backend signer is implemented.</div>
		                {/if}
              </div>
            {:else if collectionTab === 'docs'}
              {#if activeCollection}
                <section class="panel-section">
                  <h3>Docs Preview</h3>
                  <p class="muted">
                    Serves this collection's generated documentation over 127.0.0.1 so it can be opened in a browser. Docs are regenerated on every request, so a refresh shows your latest edit.
                  </p>
                  <div class="field-grid">
                    <label class="field-label" for="docs-port">Port</label>
                    <input
                      id="docs-port"
                      data-testid="docs-port"
                      type="number"
                      min="0"
                      max="65535"
                      bind:value={docsServerPort}
                      disabled={docsServerStatus?.running}
                    />
                    <span class="muted">0 lets the operating system pick a free port.</span>
                  </div>
                  <div class="button-row">
                    {#if docsServerStatus?.running}
                      <button type="button" data-testid="docs-stop" onclick={() => stopDocsServer(activeCollection.id)} disabled={busy !== ''}>Stop</button>
                    {:else}
                      <button class="primary" type="button" data-testid="docs-start" onclick={() => startDocsServer(activeCollection.id)} disabled={busy !== ''}>Start</button>
                    {/if}
                  </div>
                  {#if docsServerStatus?.running}
                    <p data-testid="docs-status">
                      Serving at <code>{docsServerStatus.url}</code> — YAML at <code>{docsServerStatus.url}/collection.yaml</code>.
                    </p>
                    <p class="muted">
                      The preview describes every request, header and example in this collection. It is reachable only from this machine.
                    </p>
                  {:else}
                    <p class="muted" data-testid="docs-status">Not running.</p>
                  {/if}
                </section>
              {/if}
            {:else if collectionTab === 'mock'}
              {#if activeCollection}
                <section class="panel-section">
                  <h3>Mock Server</h3>
                  <p class="muted">
                    Answers requests from this collection's saved response examples. Binds to 127.0.0.1 only — a mock replays recorded traffic, which often contains credentials.
                  </p>
                  <div class="field-grid">
                    <label class="field-label" for="mock-port">Port</label>
                    <input
                      id="mock-port"
                      data-testid="mock-port"
                      type="number"
                      min="0"
                      max="65535"
                      bind:value={mockServerPort}
                      disabled={mockServerStatus?.running}
                    />
                    <span class="muted">0 lets the operating system pick a free port.</span>
                  </div>
                  <div class="button-row">
                    {#if mockServerStatus?.running}
                      <button type="button" data-testid="mock-stop" onclick={() => stopMockServer(activeCollection.id)} disabled={busy !== ''}>Stop</button>
                      <button type="button" data-testid="mock-refresh" onclick={() => refreshMockServerRoutes(activeCollection.id)} disabled={busy !== ''}>Reload examples</button>
                    {:else}
                      <button class="primary" type="button" data-testid="mock-start" onclick={() => startMockServer(activeCollection.id)} disabled={busy !== ''}>Start</button>
                    {/if}
                  </div>
                  {#if mockServerStatus?.running}
                    <p data-testid="mock-status">
                      Running at <code>{mockServerStatus.url}</code> — {mockServerStatus.routes} route{mockServerStatus.routes === 1 ? '' : 's'}.
                    </p>
                    <p class="muted">
                      Send <code>x-mock-response-name</code> to choose a specific example. Calls appear in Dev Tools → Network.
                    </p>
                  {:else}
                    <p class="muted" data-testid="mock-status">Not running.</p>
                  {/if}
                </section>
              {/if}
            {:else if collectionTab === 'presets'}
              <div class="field-grid">
                <span class="field-label">Request Type</span>
                <select aria-label="Preset request type" value={normalizePresetRequestType(activeCollection.presets?.requestType) || 'http'} onchange={(e) => updateCollectionPresets({ requestType: e.currentTarget.value })}>
                  <option value="http">HTTP</option>
                  <option value="graphql">GraphQL</option>
                  <option value="grpc">gRPC</option>
                  <option value="websocket">WebSocket</option>
                </select>
                <span class="field-label">Base URL</span>
                <input aria-label="Preset base URL" placeholder="https://api.example.com" value={activeCollection.presets?.requestUrl ?? ''} onchange={(e) => updateCollectionPresets({ requestUrl: e.currentTarget.value })} />
              </div>
	            {:else if collectionTab === 'proxy'}
	              <div class="field-grid">
	                <span class="field-label">Mode</span>
	                <select aria-label="Collection proxy mode" value={collectionProxyMode(activeCollection.proxy)} onchange={(e) => updateCollectionProxyMode(e.currentTarget.value)}>
	                  <option value="inherit">Inherit</option>
	                  <option value="manual">Enabled</option>
	                  <option value="off">Disabled</option>
	                </select>
	              </div>
	              {#if collectionProxyMode(activeCollection.proxy) === 'manual'}
	                <div class="field-grid">
	                  <span class="field-label">Protocol</span>
	                  <select aria-label="Collection proxy protocol" value={activeCollection.proxy?.protocol || 'http'} onchange={(e) => updateCollectionProxy({ protocol: e.currentTarget.value })}>
	                    <option value="http">HTTP</option>
	                    <option value="https">HTTPS</option>
	                    <option value="socks5">SOCKS5</option>
	                  </select>
	                  <span class="field-label">Host</span>
	                  <input aria-label="Collection proxy host" value={activeCollection.proxy?.hostname ?? ''} oninput={(e) => updateCollectionProxy({ hostname: e.currentTarget.value })} />
	                  <span class="field-label">Port</span>
	                  <input aria-label="Collection proxy port" value={activeCollection.proxy?.port ?? ''} oninput={(e) => updateCollectionProxy({ port: e.currentTarget.value })} />
	                  <span class="field-label">Bypass</span>
	                  <input aria-label="Collection proxy bypass" value={activeCollection.proxy?.bypassProxy ?? ''} oninput={(e) => updateCollectionProxy({ bypassProxy: e.currentTarget.value })} />
	                  <span class="field-label">Auth enabled</span>
	                  <input aria-label="Collection proxy auth enabled" type="checkbox" checked={!(activeCollection.proxy?.auth?.disabled ?? false)} onchange={(e) => updateCollectionProxyAuth({ disabled: !e.currentTarget.checked })} />
	                  <span class="field-label">Username</span>
	                  <input aria-label="Collection proxy username" value={activeCollection.proxy?.auth?.username ?? ''} oninput={(e) => updateCollectionProxyAuth({ username: e.currentTarget.value })} />
	                  <span class="field-label">Password</span>
	                  <input aria-label="Collection proxy password" type="password" value={activeCollection.proxy?.auth?.password ?? ''} oninput={(e) => updateCollectionProxyAuth({ password: e.currentTarget.value })} />
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
                        <td><input aria-label="Client certificate domain" placeholder="example.org" value={certificate.domain ?? ''} oninput={(e) => updateCollectionClientCertificate(index, 'domain', e.currentTarget.value)} /></td>
                        <td>
                          <select value={certificate.type || 'cert'} onchange={(e) => updateCollectionClientCertificate(index, 'type', e.currentTarget.value)}>
                            <option value="cert">Cert</option>
                            <option value="pfx">PFX</option>
                          </select>
                        </td>
                        <td><input aria-label="Client certificate cert file" disabled={(certificate.type || 'cert') === 'pfx'} value={certificate.certFilePath ?? ''} oninput={(e) => updateCollectionClientCertificate(index, 'certFilePath', e.currentTarget.value)} /></td>
                        <td><input aria-label="Client certificate key file" disabled={(certificate.type || 'cert') === 'pfx'} value={certificate.keyFilePath ?? ''} oninput={(e) => updateCollectionClientCertificate(index, 'keyFilePath', e.currentTarget.value)} /></td>
                        <td><input aria-label="Client certificate pfx file" disabled={(certificate.type || 'cert') !== 'pfx'} value={certificate.pfxFilePath ?? ''} oninput={(e) => updateCollectionClientCertificate(index, 'pfxFilePath', e.currentTarget.value)} /></td>
                        <td><input aria-label="Client certificate passphrase" type="password" value={certificate.passphrase ?? ''} oninput={(e) => updateCollectionClientCertificate(index, 'passphrase', e.currentTarget.value)} /></td>
                        <td><button onclick={() => removeCollectionClientCertificate(index)}>Remove</button></td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
              <button onclick={addCollectionClientCertificate}>Add client certificate</button>
            {:else if collectionTab === 'protobuf'}
              <div class="settings-stack">
                <div class="settings-section-header">
                  <h3>Proto Files</h3>
                  <button onclick={addCollectionProtoFile}>Add proto file</button>
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
                          <td><input aria-label="Proto file path" placeholder="protos/service.proto" value={protoFile.path ?? ''} onchange={(e) => updateCollectionProtoFile(index, 'path', e.currentTarget.value)} /></td>
                          <td>
                            <select aria-label="Proto file type" value={protoFile.type || 'file'} onchange={(e) => updateCollectionProtoFile(index, 'type', e.currentTarget.value)}>
                              <option value="file">file</option>
                            </select>
                          </td>
                          <td><span class:ok={protoFile.exists} class:bad={!protoFile.exists}>{protoFile.exists ? 'Found' : 'Missing'}</span></td>
                          <td><button onclick={() => removeCollectionProtoFile(index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
                {#if (activeCollection.protobuf?.protoFiles?.length ?? 0) === 0}
                  <div class="empty-appState">No proto files</div>
                {/if}

                <div class="settings-section-header">
                  <h3>Import Paths</h3>
                  <button onclick={addCollectionProtoImportPath}>Add import path</button>
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
                          <td><input aria-label="Enable proto import path" type="checkbox" checked={importPath.enabled} onchange={(e) => updateCollectionProtoImportPath(index, 'enabled', e.currentTarget.checked)} /></td>
                          <td><input aria-label="Proto import path" placeholder="protos" value={importPath.path ?? ''} onchange={(e) => updateCollectionProtoImportPath(index, 'path', e.currentTarget.value)} /></td>
                          <td><span class:ok={importPath.exists} class:bad={!importPath.exists}>{importPath.exists ? 'Found' : 'Missing'}</span></td>
                          <td><button onclick={() => removeCollectionProtoImportPath(index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
                {#if (activeCollection.protobuf?.importPaths?.length ?? 0) === 0}
                  <div class="empty-appState">No import paths</div>
                {/if}
              </div>
            {:else if collectionTab === 'script'}
              <span class="field-label">Pre-request</span>
              <textarea class="short" spellcheck="false" value={activeCollection.preScript} onchange={(e) => updateCollectionScript('preScript', e.currentTarget.value)}></textarea>
              <span class="field-label">Post-response</span>
              <textarea class="short" spellcheck="false" value={activeCollection.postScript} onchange={(e) => updateCollectionScript('postScript', e.currentTarget.value)}></textarea>
            {:else if collectionTab === 'tests'}
              <textarea spellcheck="false" value={activeCollection.tests} onchange={(e) => updateCollectionScript('tests', e.currentTarget.value)}></textarea>
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
            <button type="button" onclick={() => refreshGitWorkbench()} disabled={gitWorkbenchLoading || gitWorkbenchBusy !== ''}>Refresh</button>
          </header>

          <div class="git-workbench-feedback" aria-live="polite" aria-atomic="true">
            {#if gitWorkbenchStatus}<p class="git-status-message">{gitWorkbenchStatus}</p>{/if}
            {#if gitWorkbenchError}<p class="error-text">{gitWorkbenchError}</p>{/if}
          </div>

          {#if !activeCollection}
            <div class="empty-appState wide">Select a local collection before opening the Git workbench.</div>
          {:else if gitWorkbenchLoading && !gitWorkbenchSnapshot}
            <div class="empty-appState wide">Loading Git status…</div>
          {:else if gitWorkbenchSnapshot && !gitWorkbenchSnapshot.available}
            <div class="empty-appState wide">Git is required for this workbench. Install Git, then refresh.</div>
          {:else if gitWorkbenchSnapshot && !gitWorkbenchSnapshot.initialized}
            <div class="git-workbench-empty">
              <h3>Initialize this collection</h3>
              <p>This creates only local Git metadata. It does not stage, commit, or share any files.</p>
              <button class="primary" type="button" onclick={initializeGitWorkbench} disabled={gitWorkbenchBusy !== ''}>Initialize Git</button>
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
                  <button type="button" onclick={() => viewGitWorkbenchDiff(false)} disabled={gitWorkbenchBusy !== '' || gitWorkbenchSelectedPaths.length !== 1}>View unstaged diff</button>
                  <button type="button" onclick={() => viewGitWorkbenchDiff(true)} disabled={gitWorkbenchBusy !== '' || gitWorkbenchSelectedPaths.length !== 1}>View staged diff</button>
                  <button type="button" onclick={stageGitWorkbenchSelection} disabled={gitWorkbenchBusy !== '' || !canStageGitSelection(gitWorkbenchSelectedPaths, gitWorkbenchSnapshot.files ?? [])}>Stage selected</button>
                  <button type="button" onclick={unstageGitWorkbenchSelection} disabled={gitWorkbenchBusy !== '' || !canUnstageGitSelection(gitWorkbenchSelectedPaths, gitWorkbenchSnapshot.files ?? [])}>Unstage selected</button>
                </div>
              </div>
              {#if (gitWorkbenchSnapshot.files ?? []).length}
                <div class="git-file-table" role="table" aria-label="Collection Git changes">
                  <div class="git-file-row git-file-header" role="row"><span role="columnheader">Select</span><span role="columnheader">File</span><span role="columnheader">Status</span></div>
                  {#each gitWorkbenchSnapshot.files ?? [] as file (file.path)}
                    <div class="git-file-row" class:selected={gitWorkbenchSelectedPaths.includes(file.path)} role="row">
                      <span role="cell"><input type="checkbox" aria-label={`Select ${file.path}`} checked={gitWorkbenchSelectedPaths.includes(file.path)} onchange={(event) => toggleGitWorkbenchPath(file.path, event.currentTarget.checked)} /></span>
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
                <div class="empty-appState compact">No scoped changes.</div>
              {/if}
              {#if gitWorkbenchDiff}
                <article class="git-diff-viewer" aria-label={`Git diff ${gitWorkbenchDiff.path}`}>
                  <header><strong>{gitWorkbenchDiff.staged ? 'Staged' : 'Unstaged'} diff · {gitWorkbenchDiff.path}</strong><button type="button" onclick={() => (gitWorkbenchDiff = undefined)}>Close diff</button></header>
                  {#if gitWorkbenchDiff.binary}<p>This file is binary; Git does not provide a text diff.</p>{:else}<pre>{gitWorkbenchDiff.text || 'No text diff is available.'}</pre>{/if}
                  {#if gitWorkbenchDiff.truncated}<p class="muted">This diff is bounded for safe display and was truncated.</p>{/if}
                </article>
              {/if}
            </section>

            <div class="git-workbench-grid">
              <section class="git-workbench-section" aria-labelledby="git-commit-title">
                <h3 id="git-commit-title">Commit staged changes</h3>
                <label>Message<textarea aria-label="Git commit message" bind:value={gitWorkbenchCommitMessage} placeholder="Describe this intentional change" disabled={gitWorkbenchBusy !== ''}></textarea></label>
                <button class="primary" type="button" onclick={commitGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchCommitMessage.trim()}>Commit staged</button>
              </section>

              <section class="git-workbench-section" aria-labelledby="git-branch-title">
                <h3 id="git-branch-title">Branches</h3>
                <label>Current or target branch<select aria-label="Git branch" bind:value={gitWorkbenchBranch} disabled={gitWorkbenchBusy !== ''}>{#each gitWorkbenchSnapshot.branches ?? [] as branch (branch)}<option value={branch}>{branch}</option>{/each}</select></label>
                {#if !canSwitchGitBranch(gitWorkbenchSnapshot)}<p class="muted">Switching is disabled until this collection’s scoped changes and conflicts are resolved.</p>{/if}
                <button type="button" onclick={checkoutGitWorkbenchBranch} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchBranch || !canSwitchGitBranch(gitWorkbenchSnapshot)}>Switch branch</button>
                <label>New branch<input aria-label="New Git branch" bind:value={gitWorkbenchNewBranch} placeholder="feature/name" disabled={gitWorkbenchBusy !== ''} /></label>
                <label class="checkbox-line"><input type="checkbox" bind:checked={gitWorkbenchCheckoutNewBranch} disabled={gitWorkbenchBusy !== ''} />Switch to the new branch after creation</label>
                <button type="button" onclick={createGitWorkbenchBranch} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchNewBranch.trim() || (gitWorkbenchCheckoutNewBranch && !canSwitchGitBranch(gitWorkbenchSnapshot))}>Create branch</button>
              </section>
            </div>

            <section class="git-workbench-section" aria-labelledby="git-remote-title">
              <div class="git-section-heading"><div><h3 id="git-remote-title">Remote sync</h3><p>Use credential-free URLs. Pull is fast-forward only; push never forces.</p></div></div>
              {#if (gitWorkbenchSnapshot.remotes ?? []).length}<div class="git-remote-list" aria-label="Configured Git remotes">{#each gitWorkbenchSnapshot.remotes ?? [] as remote (remote.name)}<span><strong>{remote.name}</strong><code>{remote.url}</code></span>{/each}</div>{/if}
              <div class="git-remote-fields">
                <label>Name<input aria-label="Git remote name" value={gitWorkbenchRemoteName} oninput={(event) => selectGitWorkbenchRemote(event.currentTarget.value)} placeholder="origin" disabled={gitWorkbenchBusy !== ''} /></label>
                <label>Credential-free URL<input aria-label="Git remote URL" bind:value={gitWorkbenchRemoteURL} placeholder="https://host/org/repository.git or file:///…" disabled={gitWorkbenchBusy !== ''} /></label>
                <label>Branch<input aria-label="Git remote branch" bind:value={gitWorkbenchRemoteBranch} placeholder={gitWorkbenchSnapshot.branch || 'main'} disabled={gitWorkbenchBusy !== ''} /></label>
              </div>
              <div class="button-row compact">
                <button type="button" onclick={setGitWorkbenchRemote} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteURL.trim()}>Set / update remote</button>
                <button type="button" onclick={fetchGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim()}>Fetch</button>
                <button type="button" onclick={pullGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteBranch.trim()}>Pull ff-only</button>
                <label class="checkbox-line"><input type="checkbox" bind:checked={gitWorkbenchSetUpstream} disabled={gitWorkbenchBusy !== ''} />Set upstream</label>
                <button class="primary" type="button" onclick={pushGitWorkbench} disabled={gitWorkbenchBusy !== '' || !gitWorkbenchRemoteName.trim() || !gitWorkbenchRemoteBranch.trim() || !canPushGitBranch(gitWorkbenchSnapshot.upstream, gitWorkbenchRemoteName.trim(), gitWorkbenchRemoteBranch.trim(), gitWorkbenchSetUpstream)}>Push</button>
              </div>
              {#if !canPushGitBranch(gitWorkbenchSnapshot.upstream, gitWorkbenchRemoteName.trim(), gitWorkbenchRemoteBranch.trim(), gitWorkbenchSetUpstream)}<p class="muted">Push is disabled until this exact remote/branch is upstream, or you explicitly choose Set upstream.</p>{/if}
            </section>
          {/if}
        </section>
      {:else if activeView === 'runner'}
        {#await import('./lib/views/RunnerPanel.svelte') then RunnerPanel}
          {@const RunnerPanelComponent = RunnerPanel.default}
          <RunnerPanelComponent
            bind:runnerDelayMs
            bind:runnerBailOnFailure
            bind:runnerIterations
            bind:runnerDataFile
            {chooseRunnerDataFile}
            {normalizedRunnerIterations}
            state={appState}
            {busy}
            {activeCollectionRun}
            {collectionRunCancellationRequested}
            {runnerCancelledCount}
            {runnerCompletedCancelled}
            {runnerConfigItems}
            {runnerSelectedCount}
            {runnerItemSelected}
            {setRunnerItemSelected}
            {toggleRunnerSelectAll}
            {normalizedRunnerDelayMs}
            {resetRunnerConfiguration}
            {runCollection}
            {cancelCollectionRun}
          />
        {/await}
      {:else if activeView === 'environments'}
        <section class="panel">
          <header class="panel-header">
            <h2>Environments</h2>
            <div class="split">
              <input aria-label="Global environment name" bind:value={globalEnvironmentName} />
              <button onclick={createGlobalEnvironment}>Create global</button>
            </div>
            <div class="split">
              <input aria-label="Collection environment name" bind:value={environmentName} />
              <button onclick={createEnvironment}>Create</button>
            </div>
          </header>
          <div class="env-grid">
            <article>
              <h3>Global Environment</h3>
              {#if selectedGlobalEnvironment && activeWorkspace}
                <div class="field-grid">
                  <span class="field-label">Active</span>
                  <select aria-label="Active global environment" value={activeWorkspace.activeGlobalEnvironmentId ?? ''} onchange={(e) => setActiveGlobalEnvironment(e.currentTarget.value)}>
                    <option value="">No global environment</option>
                    {#each activeWorkspace.globalEnvironments ?? [] as env (env.id)}
                      <option value={env.id}>{env.name}</option>
                    {/each}
                  </select>
                  <span class="field-label">Name</span>
                  <input aria-label="Global environment editor name" value={selectedGlobalEnvironment.name} oninput={(e) => updateGlobalEnvironmentMetadata('name', e.currentTarget.value)} />
                  <span class="field-label">Color</span>
                  <input aria-label="Global environment color" type="color" value={selectedGlobalEnvironment.color || '#2f8cff'} oninput={(e) => updateGlobalEnvironmentMetadata('color', e.currentTarget.value)} />
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
                    <button type="button" class:active={globalEnvironmentVariableTab === tab.id} onclick={() => (globalEnvironmentVariableTab = tab.id)}>{tab.label}</button>
                  {/each}
                </nav>
                <div class="search-box env-search">
                  <input aria-label="Search global environment variables" placeholder="Search variables" bind:value={globalEnvironmentVariableSearch} />
                  {#if globalEnvironmentVariableSearch}
                    <button class="icon-button ghost" title="Clear global environment variable search" onclick={() => (globalEnvironmentVariableSearch = '')}>x</button>
                  {/if}
                </div>
                {#if visibleGlobalEnvironmentRows.length > 0}
                  <table>
                    <thead><tr><th>On</th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                    <tbody>
                      {#each visibleGlobalEnvironmentRows as row (row.variable.id)}
                        <tr>
                          <td><input type="checkbox" checked={row.variable.enabled} onchange={(e) => updateGlobalEnvironmentVariable(row.index, 'enabled', e.currentTarget.checked)} /></td>
                          <td><input aria-label="Global environment variable name" value={row.variable.name} oninput={(e) => updateGlobalEnvironmentVariable(row.index, 'name', e.currentTarget.value)} /></td>
                          <td><input aria-label="Global environment variable value" value={String(row.variable.value ?? '')} oninput={(e) => updateGlobalEnvironmentVariable(row.index, 'value', e.currentTarget.value)} /></td>
                          <td>
                            <select aria-label="Global environment variable type" value={row.variable.dataType || row.variable.type || 'string'} onchange={(e) => updateGlobalEnvironmentVariable(row.index, 'dataType', e.currentTarget.value)}>
                              <option value="string">string</option>
                              <option value="number">number</option>
                              <option value="boolean">boolean</option>
                              <option value="object">object</option>
                            </select>
                          </td>
                          <td><input aria-label="Global environment variable secret" type="checkbox" checked={row.variable.secret} onchange={(e) => updateGlobalEnvironmentVariable(row.index, 'secret', e.currentTarget.checked)} /></td>
                          <td><button onclick={() => removeGlobalEnvironmentVariable(row.index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}
                  <div class="empty-appState">{globalEnvironmentVariableQuery ? 'No results found' : `No ${globalEnvironmentVariableTab}`}</div>
                {/if}
                <div class="toolbar">
                  <button onclick={addGlobalEnvironmentVariable}>{environmentVariableAddLabel(globalEnvironmentVariableTab)}</button>
                  <button onclick={copyGlobalEnvironment}>Copy</button>
                  <button onclick={exportGlobalEnvironment} disabled={globalEnvironmentExportAll && globalEnvironmentExportFormat === 'single-object'}>Export</button>
                  <button onclick={saveGlobalEnvironmentExport} disabled={globalEnvironmentExportAll && globalEnvironmentExportFormat === 'single-object'}>Save export</button>
                  <button onclick={importGlobalEnvironment} disabled={!globalEnvironmentPayload.trim()}>Import</button>
                  <button onclick={deleteGlobalEnvironment}>Delete global</button>
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
                <div class="empty-appState">Create a global environment</div>
              {/if}
            </article>
            <article>
              <h3>{selectedEnvironment?.name ?? 'No environment'} Variables</h3>
              {#if selectedEnvironment}
                <nav class="subtabs compact" aria-label="Environment variable tabs">
                  {#each environmentVariableTabs as tab (tab.id)}
                    <button type="button" class:active={environmentVariableTab === tab.id} onclick={() => (environmentVariableTab = tab.id)}>{tab.label}</button>
                  {/each}
                </nav>
                <div class="search-box env-search">
                  <input aria-label="Search environment variables" placeholder="Search variables" bind:value={environmentVariableSearch} />
                  {#if environmentVariableSearch}
                    <button class="icon-button ghost" title="Clear environment variable search" onclick={() => (environmentVariableSearch = '')}>x</button>
                  {/if}
                </div>
                {#if visibleEnvironmentRows.length > 0}
                  <table>
                    <thead><tr><th>On</th><th>Name</th><th>Value</th><th>Type</th><th>Secret</th><th></th></tr></thead>
                    <tbody>
                      {#each visibleEnvironmentRows as row (row.variable.id)}
                        <tr>
                          <td><input type="checkbox" checked={row.variable.enabled} onchange={(e) => updateEnvironmentVariable(row.index, 'enabled', e.currentTarget.checked)} /></td>
                          <td><input aria-label="Environment variable name" value={row.variable.name} oninput={(e) => updateEnvironmentVariable(row.index, 'name', e.currentTarget.value)} /></td>
                          <td><input aria-label="Environment variable value" value={String(row.variable.value ?? '')} oninput={(e) => updateEnvironmentVariable(row.index, 'value', e.currentTarget.value)} /></td>
                          <td>
                            <select aria-label="Environment variable type" value={row.variable.dataType || row.variable.type || 'string'} onchange={(e) => updateEnvironmentVariable(row.index, 'dataType', e.currentTarget.value)}>
                              <option value="string">string</option>
                              <option value="number">number</option>
                              <option value="boolean">boolean</option>
                              <option value="object">object</option>
                            </select>
                          </td>
                          <td><input aria-label="Environment variable secret" type="checkbox" checked={row.variable.secret} onchange={(e) => updateEnvironmentVariable(row.index, 'secret', e.currentTarget.checked)} /></td>
                          <td><button onclick={() => removeEnvironmentVariable(row.index)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}
                  <div class="empty-appState">{environmentVariableQuery ? 'No results found' : `No ${environmentVariableTab}`}</div>
                {/if}
                <button onclick={addEnvironmentVariable}>{environmentVariableAddLabel(environmentVariableTab)}</button>
              {:else}
                <div class="empty-appState">Create or select an environment</div>
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
                <button onclick={newDotEnvFile}>New</button>
                <button onclick={saveDotEnvFile}>Save</button>
                <button onclick={() => loadDotEnvFiles(true)}>Reload</button>
                <button onclick={deleteDotEnvFile} disabled={!selectedDotEnvFile}>Delete</button>
              </div>
              <div class="tabs compact">
                <button class:active={dotEnvEditorMode === 'table'} onclick={() => (dotEnvEditorMode = 'table')}>Table</button>
                <button class:active={dotEnvEditorMode === 'raw'} onclick={() => (dotEnvEditorMode = 'raw')}>Raw</button>
              </div>
              {#if dotEnvFiles.length > 0}
                <table>
                  <thead><tr><th>Scope</th><th>File</th><th>Runtime</th></tr></thead>
                  <tbody>
                    {#each dotEnvFiles as file (file.path)}
                      <tr class:active={dotEnvFileKey(file) === selectedDotEnvKey}>
                        <td>{file.scope}</td>
                        <td><button onclick={() => selectDotEnvFile(file)}>{file.name}</button></td>
                        <td>{file.runtime ? 'yes' : ''}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              {:else}
                <div class="empty-appState">No .env files</div>
              {/if}
              {#if dotEnvEditorMode === 'table'}
                {#if dotEnvRows.length > 0}
                  <table>
                    <thead><tr><th>Name</th><th>Value</th><th></th></tr></thead>
                    <tbody>
                      {#each dotEnvRows as row, index (index)}
                        <tr>
                          <td><input aria-label=".env variable name" value={row.name} oninput={(e) => updateDotEnvRow(row, 'name', e.currentTarget.value)} /></td>
                          <td><input aria-label=".env variable value" value={row.value} oninput={(e) => updateDotEnvRow(row, 'value', e.currentTarget.value)} /></td>
                          <td><button onclick={() => removeDotEnvRow(row)}>Remove</button></td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}
                  <div class="empty-appState">No .env variables</div>
                {/if}
                <button onclick={addDotEnvRow}>Add variable</button>
              {:else}
                <textarea class="short" aria-label=".env file content" spellcheck="false" bind:value={dotEnvContent} oninput={() => (dotEnvDirty = true)}></textarea>
              {/if}
            </article>
            <article>
              <h3>Collection Variables</h3>
              <table>
                <tbody>
                  {#each activeCollection?.variables ?? [] as variable, index (variable.id)}
                    <tr>
                      <td><input type="checkbox" checked={variable.enabled} onchange={(e) => updateCollectionVariable(index, 'enabled', e.currentTarget.checked)} /></td>
                      <td><input value={variable.name} onchange={(e) => updateCollectionVariable(index, 'name', e.currentTarget.value)} /></td>
                      <td><input value={String(variable.value ?? '')} onchange={(e) => updateCollectionVariable(index, 'value', e.currentTarget.value)} /></td>
                      <td><input type="checkbox" checked={variable.secret} onchange={(e) => updateCollectionVariable(index, 'secret', e.currentTarget.checked)} /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              <button onclick={addCollectionVariable}>Add variable</button>
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
                <select value={activeCollection?.auth?.mode ?? 'none'} onchange={(e) => updateCollectionAuth({ mode: e.currentTarget.value })}>
                  {#each authModes as mode (mode)}
                    <option value={mode}>{mode}</option>
                  {/each}
                </select>
	                {#if activeCollection?.auth?.mode === 'basic' || activeCollection?.auth?.mode === 'digest' || activeCollection?.auth?.mode === 'wsse' || activeCollection?.auth?.mode === 'ntlm'}
                  <span class="field-label">Username</span>
                  <input value={activeCollection.auth.username} onchange={(e) => updateCollectionAuth({ username: e.currentTarget.value })} />
                  <span class="field-label">Password</span>
                  <input type="password" value={activeCollection.auth.password} onchange={(e) => updateCollectionAuth({ password: e.currentTarget.value })} />
                  {#if activeCollection?.auth?.mode === 'ntlm'}
                    <span class="field-label">Domain</span>
                    <input value={activeCollection.auth.domain} onchange={(e) => updateCollectionAuth({ domain: e.currentTarget.value })} />
                  {/if}
                {:else if activeCollection?.auth?.mode === 'bearer'}
                  <span class="field-label">Token</span>
                  <input type="password" value={activeCollection.auth.token} onchange={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
                {:else if activeCollection?.auth?.mode === 'oauth2'}
                  <span class="field-label">Grant</span>
                  <select value={activeCollection.auth.oauth2?.grantType || 'client_credentials'} onchange={(e) => updateCollectionOAuth2Auth({ grantType: e.currentTarget.value })}>
                    {#each oauth2GrantTypes as grant (grant)}
                      <option value={grant}>{grant}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">Callback URL</span>
                    <input value={activeCollection.auth.oauth2?.callbackUrl ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ callbackUrl: e.currentTarget.value })} />
                    <span class="field-label">Authorization URL</span>
                    <input value={activeCollection.auth.oauth2?.authorizationUrl ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ authorizationUrl: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Access token URL</span>
                  <input value={activeCollection.auth.oauth2?.accessTokenUrl ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ accessTokenUrl: e.currentTarget.value })} />
                  <span class="field-label">Client ID</span>
                  <input value={activeCollection.auth.oauth2?.clientId ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ clientId: e.currentTarget.value })} />
                  <span class="field-label">Client secret</span>
                  <input type="password" value={activeCollection.auth.oauth2?.clientSecret ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ clientSecret: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'password'}
                    <span class="field-label">Username</span>
                    <input value={activeCollection.auth.oauth2?.username ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ username: e.currentTarget.value })} />
                    <span class="field-label">Password</span>
                    <input type="password" value={activeCollection.auth.oauth2?.password ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ password: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Scope</span>
                  <input value={activeCollection.auth.oauth2?.scope ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ scope: e.currentTarget.value })} />
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code' || activeCollection.auth.oauth2?.grantType === 'implicit'}
                    <span class="field-label">State</span>
                    <input value={activeCollection.auth.oauth2?.state ?? ''} onchange={(e) => updateCollectionOAuth2Auth({ state: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Credentials</span>
                  <select value={activeCollection.auth.oauth2?.credentialsPlacement || 'basic_auth_header'} onchange={(e) => updateCollectionOAuth2Auth({ credentialsPlacement: e.currentTarget.value })}>
                    {#each oauth2CredentialPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if activeCollection.auth.oauth2?.grantType === 'authorization_code'}
                    <span class="field-label">PKCE</span>
                    <input type="checkbox" checked={activeCollection.auth.oauth2?.pkce ?? false} onchange={(e) => updateCollectionOAuth2Auth({ pkce: e.currentTarget.checked })} />
                  {/if}
                  <span class="field-label">Token source</span>
                  <select value={activeCollection.auth.oauth2?.tokenSource || 'access_token'} onchange={(e) => updateCollectionOAuth2Auth({ tokenSource: e.currentTarget.value })}>
                    {#each oauth2TokenSources as source (source)}
                      <option value={source}>{source}</option>
                    {/each}
                  </select>
                  <span class="field-label">Token placement</span>
                  <select value={activeCollection.auth.oauth2?.tokenPlacement || 'header'} onchange={(e) => updateCollectionOAuth2Auth({ tokenPlacement: e.currentTarget.value })}>
                    {#each oauth2TokenPlacements as placement (placement)}
                      <option value={placement}>{placement}</option>
                    {/each}
                  </select>
                  {#if (activeCollection.auth.oauth2?.tokenPlacement || 'header') === 'header'}
                    <span class="field-label">Header prefix</span>
                    <input value={activeCollection.auth.oauth2?.tokenHeaderPrefix || 'Bearer'} onchange={(e) => updateCollectionOAuth2Auth({ tokenHeaderPrefix: e.currentTarget.value })} />
                  {:else}
                    <span class="field-label">Query key</span>
                    <input value={activeCollection.auth.oauth2?.tokenQueryKey || 'access_token'} onchange={(e) => updateCollectionOAuth2Auth({ tokenQueryKey: e.currentTarget.value })} />
                  {/if}
                  <span class="field-label">Static token</span>
                  <input type="password" value={activeCollection.auth.token} onchange={(e) => updateCollectionAuth({ token: e.currentTarget.value })} />
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
	                  <input value={activeCollection.auth.apiKey} onchange={(e) => updateCollectionAuth({ apiKey: e.currentTarget.value })} />
	                  <span class="field-label">Value</span>
	                  <input type="password" value={activeCollection.auth.apiValue} onchange={(e) => updateCollectionAuth({ apiValue: e.currentTarget.value })} />
	                {:else if activeCollection?.auth?.mode === 'awsv4'}
	                  <span class="field-label">Access key ID</span>
	                  <input value={activeCollection.auth.awsv4?.accessKeyId ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ accessKeyId: e.currentTarget.value })} />
	                  <span class="field-label">Secret access key</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.secretAccessKey ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ secretAccessKey: e.currentTarget.value })} />
	                  <span class="field-label">Session token</span>
	                  <input type="password" value={activeCollection.auth.awsv4?.sessionToken ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ sessionToken: e.currentTarget.value })} />
	                  <span class="field-label">Service</span>
	                  <input value={activeCollection.auth.awsv4?.service ?? ''} placeholder="execute-api" onchange={(e) => updateCollectionAWSV4Auth({ service: e.currentTarget.value })} />
	                  <span class="field-label">Region</span>
	                  <input value={activeCollection.auth.awsv4?.region ?? ''} placeholder="us-east-1" onchange={(e) => updateCollectionAWSV4Auth({ region: e.currentTarget.value })} />
		                  <span class="field-label">Profile</span>
		                  <input value={activeCollection.auth.awsv4?.profileName ?? ''} onchange={(e) => updateCollectionAWSV4Auth({ profileName: e.currentTarget.value })} />
		                {:else if activeCollection?.auth?.mode === 'oauth1'}
		                  <span class="field-label">Consumer key</span>
		                  <input value={activeCollection.auth.oauth1?.consumerKey ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ consumerKey: e.currentTarget.value })} />
		                  <span class="field-label">Consumer secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.consumerSecret ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ consumerSecret: e.currentTarget.value })} />
		                  <span class="field-label">Token</span>
		                  <input value={activeCollection.auth.oauth1?.accessToken ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ accessToken: e.currentTarget.value })} />
		                  <span class="field-label">Token secret</span>
		                  <input type="password" value={activeCollection.auth.oauth1?.accessTokenSecret ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ accessTokenSecret: e.currentTarget.value })} />
		                  <span class="field-label">Signature</span>
		                  <select value={activeCollection.auth.oauth1?.signatureMethod || 'HMAC-SHA1'} onchange={(e) => updateCollectionOAuth1Auth({ signatureMethod: e.currentTarget.value })}>
		                    {#each oauth1SignatureMethods as method (method)}
		                      <option value={method}>{method}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Add params to</span>
		                  <select value={activeCollection.auth.oauth1?.placement || 'header'} onchange={(e) => updateCollectionOAuth1Auth({ placement: e.currentTarget.value })}>
		                    {#each oauth1Placements as placement (placement)}
		                      <option value={placement}>{placement}</option>
		                    {/each}
		                  </select>
		                  <span class="field-label">Callback URL</span>
		                  <input value={activeCollection.auth.oauth1?.callbackUrl ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ callbackUrl: e.currentTarget.value })} />
		                  <span class="field-label">Verifier</span>
		                  <input value={activeCollection.auth.oauth1?.verifier ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ verifier: e.currentTarget.value })} />
		                  <span class="field-label">Timestamp</span>
		                  <input value={activeCollection.auth.oauth1?.timestamp ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ timestamp: e.currentTarget.value })} />
		                  <span class="field-label">Nonce</span>
		                  <input value={activeCollection.auth.oauth1?.nonce ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ nonce: e.currentTarget.value })} />
		                  <span class="field-label">Version</span>
		                  <input value={activeCollection.auth.oauth1?.version ?? ''} placeholder="1.0" onchange={(e) => updateCollectionOAuth1Auth({ version: e.currentTarget.value })} />
		                  <span class="field-label">Realm</span>
		                  <input value={activeCollection.auth.oauth1?.realm ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ realm: e.currentTarget.value })} />
		                  <span class="field-label">Private key</span>
		                  <textarea class="short" spellcheck="false" value={activeCollection.auth.oauth1?.privateKey ?? ''} onchange={(e) => updateCollectionOAuth1Auth({ privateKey: e.currentTarget.value })}></textarea>
		                  <span class="field-label">Private key type</span>
		                  <select value={activeCollection.auth.oauth1?.privateKeyType || 'text'} onchange={(e) => updateCollectionOAuth1Auth({ privateKeyType: e.currentTarget.value })}>
		                    <option value="text">text</option>
		                    <option value="file">file</option>
		                  </select>
		                  <span class="field-label">Body hash</span>
		                  <input type="checkbox" checked={activeCollection.auth.oauth1?.includeBodyHash ?? false} onchange={(e) => updateCollectionOAuth1Auth({ includeBodyHash: e.currentTarget.checked })} />
		                {/if}
              </div>
            </article>
          </div>
        </section>
      {:else if activeView === 'import'}
        {#await import('./lib/views/ImportPanel.svelte') then ImportPanel}
          {@const ImportPanelComponent = ImportPanel.default}
          <ImportPanelComponent
            bind:importSourceMode
            bind:importTranslatePostmanScripts
            bind:importURL
            bind:importContent
            bind:importPasteName
            bind:importDestinationWorkspaceID
            bind:importDestinationRoot
            bind:importExpanded
            bind:gitCloneURL
            bind:gitCloneRoot
            bind:importApplyButton
            bind:importPickerButton
            state={appState}
            {busy}
            {importStatus}
            {importReadyRows}
            {importPreview}
            {importSources}
            {importApplyResult}
            {importApplyInFlight}
            {gitCloneInProgress}
            {importDecisionFor}
            {selectImportSourceMode}
            {chooseImportFiles}
            {chooseImportFolder}
            {previewImportSources}
            {previewURLImport}
            {previewPasteImport}
            {requestPlannedImport}
            {updateImportDecision}
            {updateImportOverride}
            {toggleImportChild}
            {exportCollection}
            {scanGitCollections}
            {cloneGitRepository}
            {checkGitVersion}
          />
        {/await}
      {:else if activeView === 'devtools'}
        {@render devToolsPanel()}
      {:else if activeView === 'network'}
        <section class="panel">
          <header class="panel-header"><h2>Network Log</h2></header>
          <table>
            <thead><tr><th>Method</th><th>URL</th><th>Status</th><th>Time</th><th>Error</th></tr></thead>
            <tbody>
              {#each appState.networkLog ?? [] as row (row.id)}
                <tr><td>{row.method}</td><td>{row.url}</td><td>{row.status}</td><td>{row.durationMs} ms</td><td>{row.error}</td></tr>
              {/each}
            </tbody>
          </table>
        </section>
      {:else if activeView === 'history'}
        {#await import('./lib/views/HistoryPanel.svelte') then HistoryPanel}
          {@const HistoryPanelComponent = HistoryPanel.default}
          <HistoryPanelComponent
            entries={historyEntries}
            bind:query={historyQuery}
            bind:onlyFailures={historyOnlyFailures}
            bind:methodFilter={historyMethodFilter}
            bind:saveTargetCollectionID={historySaveTargetID}
            collections={historyCollections}
            {busy}
            onSearch={scheduleHistorySearch}
            onOpenInTab={openHistoryEntryInTab}
            onSaveToCollection={saveHistoryEntryToCollection}
            onClear={clearHistory}
            canOpenInTab={historyEntryStillExists}
          />
        {/await}
	      {:else if activeView === 'cookies'}
	        <section class="panel">
	          <header class="panel-header">
	            <div>
              <h2>Cookies</h2>
              <p class="panel-subtitle">{visibleCookieCount}/{appState.cookies?.length ?? 0} stored cookies</p>
            </div>
            <div class="runner-summary">
              <input aria-label="Search cookies" placeholder="Search cookies" bind:value={cookieSearch} />
              <button onclick={clearCookies} disabled={(appState.cookies?.length ?? 0) === 0 || busy !== ''}>Clear all</button>
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
                  <button class="primary" onclick={saveCookieForm} disabled={busy !== ''}>Save cookie</button>
                  <button onclick={resetCookieForm}>Reset</button>
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
                <button onclick={importRawCookie} disabled={busy !== ''}>Import header</button>
              </section>
            </div>

            <div class="cookie-domains">
              {#if (appState.cookies?.length ?? 0) === 0}
                <div class="empty-appState">No stored cookies</div>
              {:else if visibleCookieGroups.length === 0}
                <div class="empty-appState">No matching cookies</div>
              {:else}
                {#each visibleCookieGroups as group (group.domain)}
                  <article>
                    <header>
                      <div>
                        <h3>{group.domain}</h3>
                        <p class="panel-subtitle">{group.cookies.length} cookie{group.cookies.length === 1 ? '' : 's'}</p>
                      </div>
                      <button onclick={() => clearDomainCookies(group.domain)} disabled={busy !== ''}>Clear domain</button>
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
                                <button onclick={() => editCookie(cookie)}>Edit</button>
                                <button class="icon-button" title="Delete cookie" onclick={() => deleteCookie(cookie.id)}>x</button>
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
	              <p class="panel-subtitle">Theme {selectedThemeMode} · Proxy {proxyModeLabel(preferencesProxyMode(appState.preferences))}</p>
	            </div>
	          </header>
	          <div class="settings-stack">
          {#await import('./lib/views/preferences/AppearanceSection.svelte') then AppearanceSection}
            {@const AppearanceSectionComponent = AppearanceSection.default}
            <AppearanceSectionComponent
              state={appState}
              {selectedThemeMode}
              {themeModes}
              {lightThemeVariants}
              {darkThemeVariants}
              {updateThemeMode}
              {updateThemeVariant}
            />
          {/await}

          {#await import('./lib/views/preferences/DisplaySection.svelte') then DisplaySection}
            {@const DisplaySectionComponent = DisplaySection.default}
            <DisplaySectionComponent
              {appZoomPercentage}
              {zoomPercentages}
              {zoomDefaultPercentage}
              {codeFont}
              {codeFontSize}
              {resetZoomPercentage}
              {setZoomPercentage}
              {updateCodeFont}
              {updateCodeFontSize}
            />
          {/await}

          {#await import('./lib/views/preferences/GeneralSection.svelte') then GeneralSection}
            {@const GeneralSectionComponent = GeneralSection.default}
            <GeneralSectionComponent
              state={appState}
              {customCaFileName}
              {browseDefaultLocation}
              {clearDefaultLocation}
              {browseCustomCaCertificate}
              {clearCustomCaCertificate}
              {updateAutoSavePreferences}
              {updateRequestPreferences}
            />
          {/await}
	
          {#await import('./lib/views/preferences/OAuth2Section.svelte') then OAuth2Section}
            {@const OAuth2SectionComponent = OAuth2Section.default}
            <OAuth2SectionComponent
              state={appState}
              {updateAppearancePreferences}
            />
          {/await}
	
          {#await import('./lib/views/preferences/KeybindingsSection.svelte') then KeybindingsSection}
            {@const KeybindingsSectionComponent = KeybindingsSection.default}
            <KeybindingsSectionComponent
              state={appState}
              {keyBindingSections}
              keyBindingPreset={activeKeyBindingPreset}
              {updateKeyBindingPreset}
              {visibleKeyBindingEntries}
              {keyBindingDisplayValue}
              {keyBindingCanEdit}
              {keyBindingIsCustomized}
              {keybindingDraft}
              {keybindingsAreEnabled}
              {keybindingError}
              {recordingKeybindingAction}
              {formatKeyBinding}
              {beginRecordKeyBinding}
              {recordKeyBinding}
              {stopRecordKeyBinding}
              {resetKeyBinding}
              {resetAllKeyBindings}
              {updateKeybindingsEnabled}
            />
          {/await}

          {#await import('./lib/views/preferences/ProxySection.svelte') then ProxySection}
            {@const ProxySectionComponent = ProxySection.default}
            <ProxySectionComponent
              state={appState}
              {preferencesProxyMode}
              {updatePreferencesProxy}
              {updatePreferencesProxyAuth}
              {updatePreferencesProxyConfig}
              {updatePreferencesProxyMode}
            />
          {/await}

          {#await import('./lib/views/preferences/CacheSection.svelte') then CacheSection}
            {@const CacheSectionComponent = CacheSection.default}
            <CacheSectionComponent
              state={appState}
              {fileCacheSize}
              {formatRuntimeBytes}
              {updateFileCache}
              {updateSSLSessionCache}
              {clearFileCache}
              {clearSSLSessionCache}
            />
          {/await}

	          </div>
	        </section>
	      {:else if activeView === 'features'}
	        <section class="panel">
	          <header class="panel-header">
            <h2>Local Capabilities</h2>
            <div class="runner-summary">
              <span>{doneFeatures}/{totalFeatures} done</span>
              <span>{partialFeatures} partial</span>
              <button onclick={resetDemoData}>Reset demo data</button>
            </div>
          </header>
          <div class="feature-grid">
            {#each appState.featureLedger ?? [] as feature (feature.id)}
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
            onmousedown={startDevToolsDrawerResize}
          ></button>
          {@render devToolsPanel()}
        </div>
      {/if}
    </section>
  </main>
{:else}
  <main class="boot">
    <p>{error || 'LiteAPI could not load appState.'}</p>
    <button onclick={load}>Retry</button>
  </main>
{/if}

{#if importReplaceConfirmationOpen}
  {#await import('./lib/modals/confirm/ImportReplaceModal.svelte') then ImportReplaceModal}
    {@const ImportReplaceModalComponent = ImportReplaceModal.default}
    <ImportReplaceModalComponent
      bind:importReplaceConfirmationCancelButton
      {cancelImportReplaceConfirmation}
      {confirmImportReplace}
    />
  {/await}
{/if}

{#if workspaceWindowPickerOpen}
  <WorkspaceWindowPicker
    targets={workspaceWindowTargets}
    currentWorkspaceId={appState?.activeWorkspaceId ?? ''}
    busy={workspaceWindowPickerBusy}
    busyAction={workspaceWindowPickerBusyAction}
    error={workspaceWindowPickerError}
    onOpen={openSelectedWorkspaceInNewWindow}
    onCreate={createWorkspaceForNewWindow}
    onCancel={closeWorkspaceWindowPicker}
  />
{/if}

{#if creationOpen}
  {#await import('./lib/modals/confirm/NewRequestModal.svelte') then NewRequestModal}
    {@const NewRequestModalComponent = NewRequestModal.default}
    <NewRequestModalComponent
      bind:requestName
      bind:requestType
      {activeCollection}
      {submitCreationFlow}
      {closeCreationFlow}
    />
  {/await}
{/if}

{#if commandPaletteOpen}
  {#await import('./lib/modals/search/CommandPaletteModal.svelte') then CommandPaletteModal}
    {@const CommandPaletteModalComponent = CommandPaletteModal.default}
    <CommandPaletteModalComponent
      bind:commandPaletteQuery
      bind:commandPaletteActiveIndex
      bind:commandPaletteInput
      {visibleCommandPaletteActions}
      {runCommandPaletteAction}
      {closeCommandPalette}
    />
  {/await}
{/if}

{#if globalSearchOpen}
  {#await import('./lib/modals/search/GlobalSearchModal.svelte') then GlobalSearchModal}
    {@const GlobalSearchModalComponent = GlobalSearchModal.default}
    <GlobalSearchModalComponent
      bind:globalSearchQuery
      bind:globalSearchIndex
      bind:globalSearchInput
      {globalSearchResults}
      {handleGlobalSearchKeydown}
      {selectGlobalSearchResult}
      {closeGlobalSearch}
    />
  {/await}
{/if}

{#if notificationsOpen}
  {#await import('./lib/modals/NotificationsModal.svelte') then NotificationsModal}
    {@const NotificationsModalComponent = NotificationsModal.default}
    <NotificationsModalComponent
      {unreadNotificationCount}
      {notificationTab}
      {visibleNotifications}
      {listedNotifications}
      {selectedNotification}
      {busy}
      {setNotificationTab}
      {markAllNotificationsRead}
      {clearNotifications}
      {selectNotification}
      {notificationTitle}
      {notificationDate}
      {notificationType}
      {notificationDescription}
      {notificationLevelClass}
      {closeNotifications}
    />
  {/await}
{/if}

{#if oauth2AuthorizationRequest}
  {#await import('./lib/modals/confirm/OAuth2AuthorizationModal.svelte') then OAuth2AuthorizationModal}
    {@const OAuth2AuthorizationModalComponent = OAuth2AuthorizationModal.default}
    <OAuth2AuthorizationModalComponent
      bind:oauth2CallbackURLInput
      {oauth2AuthorizationRequest}
      {oauth2FrameKey}
      {oauth2CallbackMessage}
      {openOAuth2AuthorizationInSystemBrowser}
      {submitOAuth2CallbackURL}
      {closeOAuth2Authorization}
    />
  {/await}
{/if}

{#if creatingResponseExample && activeRequest}
  {#await import('./lib/modals/confirm/CreateExampleModal.svelte') then CreateExampleModal}
    {@const CreateExampleModalComponent = CreateExampleModal.default}
    <CreateExampleModalComponent
      bind:createResponseExampleName
      bind:createResponseExampleDescription
      bind:createResponseExampleInput
      {busy}
      {createResponseExample}
      {cancelCreateResponseExample}
    />
  {/await}
{/if}

{#if showShareCollectionModal && activeCollection}
  {#await import('./lib/modals/collection/ShareCollectionModal.svelte') then ShareCollectionModal}
    {@const ShareCollectionModalComponent = ShareCollectionModal.default}
    <ShareCollectionModalComponent
      bind:shareCollectionFormat
      {shareCollectionUnsupportedTypes}
      {busy}
      {shareCollectionProceed}
      {cancelShareCollectionModal}
    />
  {/await}
{/if}

{#if newFolderTarget}
  {#await import('./lib/modals/collection/NewFolderModal.svelte') then NewFolderModal}
    {@const NewFolderModalComponent = NewFolderModal.default}
    <NewFolderModalComponent
      bind:newFolderDirectoryDraft
      bind:newFolderDirectoryEditing
      bind:newFolderShowFilesystemName
      {newFolderNameDraft}
      {busy}
      {newFolderDirectoryNameIsValid}
      {collectionFolderNameIsValid}
      {newFolderDirectoryIsReservedRoot}
      {sanitizeCollectionFolderName}
      {updateNewFolderName}
      {confirmNewFolder}
      {cancelNewFolderModal}
    />
  {/await}
{/if}

{#if renameFolderTarget}
  {#await import('./lib/modals/collection/RenameFolderModal.svelte') then RenameFolderModal}
    {@const RenameFolderModalComponent = RenameFolderModal.default}
    <RenameFolderModalComponent
      bind:renameFolderDirectoryDraft
      bind:renameFolderDirectoryEditing
      bind:renameFolderShowFilesystemName
      {renameFolderNameDraft}
      {busy}
      {renameFolderDirectoryNameIsValid}
      {renameFolderDirectoryIsReserved}
      {collectionFolderNameIsValid}
      {sanitizeCollectionFolderName}
      {updateRenameFolderName}
      {confirmRenameFolder}
      {cancelRenameFolderModal}
    />
  {/await}
{/if}

{#if cloneFolderTarget}
  {#await import('./lib/modals/collection/CloneFolderModal.svelte') then CloneFolderModal}
    {@const CloneFolderModalComponent = CloneFolderModal.default}
    <CloneFolderModalComponent
      bind:cloneFolderDirectoryDraft
      bind:cloneFolderDirectoryEditing
      bind:cloneFolderShowFilesystemName
      {cloneFolderNameDraft}
      {busy}
      {cloneFolderDirectoryNameIsValid}
      {cloneFolderDirectoryIsReserved}
      {collectionFolderNameIsValid}
      {sanitizeCollectionFolderName}
      {updateCloneFolderName}
      {confirmCloneFolder}
      {cancelCloneFolderModal}
    />
  {/await}
{/if}

{#if renameRequestTarget}
  {#await import('./lib/modals/collection/RenameRequestModal.svelte') then RenameRequestModal}
    {@const RenameRequestModalComponent = RenameRequestModal.default}
    <RenameRequestModalComponent
      bind:renameRequestFilenameDraft
      bind:renameRequestFilenameEditing
      bind:renameRequestShowFilesystemName
      {renameRequestNameDraft}
      {renameRequestTarget}
      {busy}
      {renameRequestFilenameIsValid}
      {renameRequestFilenameIsReserved}
      {collectionFolderNameIsValid}
      {sanitizeCollectionFolderName}
      {updateRenameRequestName}
      {confirmRenameRequest}
      {cancelRenameRequestModal}
    />
  {/await}
{/if}

{#if cloneRequestTarget}
  {#await import('./lib/modals/collection/CloneRequestModal.svelte') then CloneRequestModal}
    {@const CloneRequestModalComponent = CloneRequestModal.default}
    <CloneRequestModalComponent
      bind:cloneRequestFilenameDraft
      bind:cloneRequestFilenameEditing
      bind:cloneRequestShowFilesystemName
      {cloneRequestNameDraft}
      {cloneRequestTarget}
      {busy}
      {cloneRequestFilenameIsValid}
      {cloneRequestFilenameIsReserved}
      {collectionFolderNameIsValid}
      {sanitizeCollectionFolderName}
      {updateCloneRequestName}
      {confirmCloneRequest}
      {cancelCloneRequestModal}
    />
  {/await}
{/if}

{#if itemInfoTarget}
  {#await import('./lib/modals/confirm/ItemInfoModal.svelte') then ItemInfoModal}
    {@const ItemInfoModalComponent = ItemInfoModal.default}
    <ItemInfoModalComponent
      {itemInfoTarget}
      {itemInfoDisplayName}
      {itemInfoFilesystemName}
      {closeItemInfoModal}
    />
  {/await}
{/if}

{#if deleteRequestTarget}
  {#await import('./lib/modals/confirm/DeleteRequestModal.svelte') then DeleteRequestModal}
    {@const DeleteRequestModalComponent = DeleteRequestModal.default}
    <DeleteRequestModalComponent
      {deleteRequestTarget}
      {busy}
      {confirmDeleteRequest}
      {cancelDeleteRequestModal}
    />
  {/await}
{/if}

{#if deleteFolderTarget}
  {#await import('./lib/modals/confirm/DeleteFolderModal.svelte') then DeleteFolderModal}
    {@const DeleteFolderModalComponent = DeleteFolderModal.default}
    <DeleteFolderModalComponent
      {deleteFolderTarget}
      {busy}
      {slashPathBase}
      {confirmDeleteFolder}
      {cancelDeleteFolderModal}
    />
  {/await}
{/if}

{#if cloneCollectionTarget}
  {#await import('./lib/modals/collection/CloneCollectionModal.svelte') then CloneCollectionModal}
    {@const CloneCollectionModalComponent = CloneCollectionModal.default}
    <CloneCollectionModalComponent
      bind:cloneCollectionFolderDraft
      bind:cloneCollectionFolderEditing
      {cloneCollectionNameDraft}
      {cloneCollectionLocationDraft}
      {busy}
      {collectionFolderNameIsValid}
      {updateCloneCollectionName}
      {browseCloneCollectionLocation}
      {confirmCloneCollection}
      {cancelCloneCollectionModal}
    />
  {/await}
{/if}

{#if renameCollectionTarget}
  {#await import('./lib/modals/collection/RenameCollectionModal.svelte') then RenameCollectionModal}
    {@const RenameCollectionModalComponent = RenameCollectionModal.default}
    <RenameCollectionModalComponent
      bind:renameCollectionDraft
      {busy}
      {confirmRenameCollection}
      {cancelRenameCollectionModal}
    />
  {/await}
{/if}

{#if removeCollectionTarget}
  {#await import('./lib/modals/confirm/RemoveCollectionModal.svelte') then RemoveCollectionModal}
    {@const RemoveCollectionModalComponent = RemoveCollectionModal.default}
    <RemoveCollectionModalComponent
      {removeCollectionTarget}
      {busy}
      {confirmRemoveCollection}
      {cancelRemoveCollectionModal}
    />
  {/await}
{/if}

	{#if showGenerateDocsModal && activeCollection}
	  {#await import('./lib/modals/collection/GenerateDocsModal.svelte') then GenerateDocsModal}
	    {@const GenerateDocsModalComponent = GenerateDocsModal.default}
	    <GenerateDocsModalComponent
	      {activeCollection}
	      {generateDocsFolderCount}
	      {generateDocsRequestCount}
	      {generateDocsEnvironments}
	      {generateDocsSelectedEnvIds}
	      {generateDocsSelectedCount}
	      {busy}
	      {formattedCollectionVersion}
	      {toggleGenerateDocsSelectAll}
	      {toggleGenerateDocsEnvironment}
	      {generateCollectionDocs}
	      {cancelGenerateDocsModal}
	      bind:selectAllInput={generateDocsSelectAllInput}
	    />
	  {/await}
		{/if}

		{#if openAPISpecViewerOpen && openAPISpecViewerResult}
  {#await import('./lib/modals/openapi/SpecViewerModal.svelte') then SpecViewerModal}
    {@const SpecViewerModalComponent = SpecViewerModal.default}
    <SpecViewerModalComponent
      {openAPISpecViewerResult}
      {formattedOpenAPISpecContent}
      {copyOpenAPISyncSpec}
      {closeOpenAPISyncSpecViewer}
    />
  {/await}
		{/if}

		{#if openAPISpecDiffOpen && openAPISpecDiffResult}
		  {#await import('./lib/modals/openapi/SpecDiffModal.svelte') then SpecDiffModal}
		    {@const SpecDiffModalComponent = SpecDiffModal.default}
		    <SpecDiffModalComponent
		      {openAPISpecDiffResult}
		      {openAPISpecDiffChangeCount}
		      {openAPISpecDiffActiveChangeIndex}
		      {openAPISyncSpecDiffSummary}
		      {openAPISpecDiffLineIsActive}
		      {goOpenAPISpecDiffChange}
		      {closeOpenAPISyncSpecDiff}
		    />
		  {/await}
		{/if}

		{#if openAPISyncSettingsOpen && activeCollection}
		  {#await import('./lib/modals/openapi/SyncSettingsModal.svelte') then SyncSettingsModal}
		    {@const SyncSettingsModalComponent = SyncSettingsModal.default}
		    <SyncSettingsModalComponent
		      bind:openAPISyncSettingsSourceURL
		      bind:openAPISyncSettingsAutoCheck
		      bind:openAPISyncSettingsInterval
		      {openAPISyncCheckIntervals}
		      {busy}
		      {saveOpenAPISyncSettings}
		      {cancelOpenAPISyncSettings}
		      {disconnectOpenAPISync}
		    />
		  {/await}
	{/if}

	{#if tabLifecycleDialog}
  {#await import('./lib/modals/confirm/UnsavedTabsModal.svelte') then UnsavedTabsModal}
    {@const UnsavedTabsModalComponent = UnsavedTabsModal.default}
    <UnsavedTabsModalComponent
      bind:tabLifecycleCancelButton
      {tabLifecycleDialog}
      {tabLifecycleDecisionBusy}
      {discardAndCompleteTabLifecycle}
      {saveAndCompleteTabLifecycle}
      {dismissTabLifecycleDialog}
    />
  {/await}
	{/if}

	{#if promptDialog}
  {#await import('./lib/modals/confirm/PromptDialogModal.svelte') then PromptDialogModal}
    {@const PromptDialogModalComponent = PromptDialogModal.default}
    <PromptDialogModalComponent
      {promptDialog}
      {updatePromptValue}
      {submitPromptDialog}
      {cancelPromptDialog}
    />
  {/await}
{/if}

{#if gitNotFoundMessage}
  {#await import('./lib/modals/confirm/GitNotFoundModal.svelte') then GitNotFoundModal}
    {@const GitNotFoundModalComponent = GitNotFoundModal.default}
    <GitNotFoundModalComponent bind:gitNotFoundMessage />
  {/await}
{/if}

{#if generatedGrpcurlCommand}
  {#await import('./lib/modals/codegen/GrpcurlCommandModal.svelte') then GrpcurlCommandModal}
    {@const GrpcurlCommandModalComponent = GrpcurlCommandModal.default}
    <GrpcurlCommandModalComponent
      {generatedGrpcurlCommand}
      {copyGrpcurlCommand}
      {closeGrpcurlCommand}
    />
  {/await}
{/if}

{#if requestCodeTarget}
  {#await import('./lib/modals/codegen/RequestCodeModal.svelte') then RequestCodeModal}
    {@const RequestCodeModalComponent = RequestCodeModal.default}
    <RequestCodeModalComponent
      {requestCodeLanguage}
      {codeGenerationTargets}
      {requestGeneratedCode}
      {changeRequestCodeLanguage}
      {copyRequestCode}
      {closeRequestCode}
    />
  {/await}
{/if}

{#if generatingResponseExampleID && activeRequest}
  {@const generatedExample = (activeRequest.examples ?? []).find((example) => responseExampleIdentifier(example) === generatingResponseExampleID)}
  {#if generatedExample}
  {#await import('./lib/modals/codegen/ResponseExampleCodeModal.svelte') then ResponseExampleCodeModal}
    {@const ResponseExampleCodeModalComponent = ResponseExampleCodeModal.default}
    <ResponseExampleCodeModalComponent
      {generatedExample}
      {responseExampleCodeLanguage}
      {responseExampleGeneratedCode}
      {changeResponseExampleCodeLanguage}
      {copyResponseExampleCode}
      {closeResponseExampleCode}
    />
  {/await}
  {/if}
{/if}

{#if busy}
  <div class="busy">{busy}</div>
{/if}

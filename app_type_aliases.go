package main

import "github.com/mutexdev/lite_api/internal/types"

// Moved to internal/types. Aliased so package main compiles unchanged.
type TerminalSession = types.TerminalSession

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

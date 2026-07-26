export namespace codegen {
	
	export class Target {
	    id: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new Target(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	    }
	}

}

export namespace main {
	
	export class CollectionGitDiff {
	    path: string;
	    staged: boolean;
	    text: string;
	    truncated: boolean;
	    binary: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionGitDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.staged = source["staged"];
	        this.text = source["text"];
	        this.truncated = source["truncated"];
	        this.binary = source["binary"];
	    }
	}
	export class CollectionGitFile {
	    path: string;
	    index: string;
	    worktree: string;
	    staged: boolean;
	    untracked: boolean;
	    conflicted: boolean;
	    binary: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionGitFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.index = source["index"];
	        this.worktree = source["worktree"];
	        this.staged = source["staged"];
	        this.untracked = source["untracked"];
	        this.conflicted = source["conflicted"];
	        this.binary = source["binary"];
	    }
	}
	export class CollectionGitRemote {
	    name: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionGitRemote(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	    }
	}
	export class CollectionGitSnapshot {
	    available: boolean;
	    initialized: boolean;
	    rootLabel?: string;
	    branch?: string;
	    detached: boolean;
	    upstream?: string;
	    ahead: number;
	    behind: number;
	    clean: boolean;
	    conflicts: boolean;
	    remotes: CollectionGitRemote[];
	    branches: string[];
	    files: CollectionGitFile[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionGitSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.initialized = source["initialized"];
	        this.rootLabel = source["rootLabel"];
	        this.branch = source["branch"];
	        this.detached = source["detached"];
	        this.upstream = source["upstream"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.clean = source["clean"];
	        this.conflicts = source["conflicts"];
	        this.remotes = this.convertValues(source["remotes"], CollectionGitRemote);
	        this.branches = source["branches"];
	        this.files = this.convertValues(source["files"], CollectionGitFile);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CollectionGitOperationResult {
	    snapshot: CollectionGitSnapshot;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionGitOperationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.snapshot = this.convertValues(source["snapshot"], CollectionGitSnapshot);
	        this.message = source["message"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class CollectionImportSelection {
	    sourceId: string;
	    candidateId: string;
	    environmentIds?: string[];
	    folderIds?: string[];
	    requestIds?: string[];
	    outputName?: string;
	    kindOverride?: string;
	    conflictAction?: string;
	    expectedContentHash: string;
	    filterEnvironments?: boolean;
	    filterFolders?: boolean;
	    filterRequests?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceId = source["sourceId"];
	        this.candidateId = source["candidateId"];
	        this.environmentIds = source["environmentIds"];
	        this.folderIds = source["folderIds"];
	        this.requestIds = source["requestIds"];
	        this.outputName = source["outputName"];
	        this.kindOverride = source["kindOverride"];
	        this.conflictAction = source["conflictAction"];
	        this.expectedContentHash = source["expectedContentHash"];
	        this.filterEnvironments = source["filterEnvironments"];
	        this.filterFolders = source["filterFolders"];
	        this.filterRequests = source["filterRequests"];
	    }
	}
	export class CollectionImportSource {
	    id?: string;
	    path?: string;
	    url?: string;
	    name?: string;
	    content?: string;
	    kindOverride?: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.url = source["url"];
	        this.name = source["name"];
	        this.content = source["content"];
	        this.kindOverride = source["kindOverride"];
	    }
	}
	export class CollectionImportApplyRequest {
	    workspaceId: string;
	    destinationRoot?: string;
	    sources: CollectionImportSource[];
	    selections: CollectionImportSelection[];
	    translatePostmanScripts?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportApplyRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspaceId = source["workspaceId"];
	        this.destinationRoot = source["destinationRoot"];
	        this.sources = this.convertValues(source["sources"], CollectionImportSource);
	        this.selections = this.convertValues(source["selections"], CollectionImportSelection);
	        this.translatePostmanScripts = source["translatePostmanScripts"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CollectionImportRequestPreview {
	    selectionId: string;
	    name: string;
	    folderPath?: string;
	    method?: string;
	    type?: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportRequestPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.selectionId = source["selectionId"];
	        this.name = source["name"];
	        this.folderPath = source["folderPath"];
	        this.method = source["method"];
	        this.type = source["type"];
	    }
	}
	export class CollectionImportFolderPreview {
	    selectionId: string;
	    name: string;
	    path: string;
	    parentPath?: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportFolderPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.selectionId = source["selectionId"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.parentPath = source["parentPath"];
	    }
	}
	export class CollectionImportEnvironmentPreview {
	    selectionId: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportEnvironmentPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.selectionId = source["selectionId"];
	        this.name = source["name"];
	    }
	}
	export class CollectionImportPreviewRow {
	    sourceId: string;
	    candidateId: string;
	    sourceName: string;
	    sourcePath?: string;
	    detectedKind: string;
	    confidence: string;
	    collectionName?: string;
	    collectionId?: string;
	    environmentIds?: string[];
	    folderIds?: string[];
	    requestIds?: string[];
	    environments?: CollectionImportEnvironmentPreview[];
	    folders?: CollectionImportFolderPreview[];
	    requests?: CollectionImportRequestPreview[];
	    warnings?: string[];
	    losses?: string[];
	    error?: string;
	    defaultSelect: boolean;
	    contentHash?: string;
	    conflict?: string;
	    destinationPath?: string;
	    openSemantics?: string;
	    existingFolder?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportPreviewRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceId = source["sourceId"];
	        this.candidateId = source["candidateId"];
	        this.sourceName = source["sourceName"];
	        this.sourcePath = source["sourcePath"];
	        this.detectedKind = source["detectedKind"];
	        this.confidence = source["confidence"];
	        this.collectionName = source["collectionName"];
	        this.collectionId = source["collectionId"];
	        this.environmentIds = source["environmentIds"];
	        this.folderIds = source["folderIds"];
	        this.requestIds = source["requestIds"];
	        this.environments = this.convertValues(source["environments"], CollectionImportEnvironmentPreview);
	        this.folders = this.convertValues(source["folders"], CollectionImportFolderPreview);
	        this.requests = this.convertValues(source["requests"], CollectionImportRequestPreview);
	        this.warnings = source["warnings"];
	        this.losses = source["losses"];
	        this.error = source["error"];
	        this.defaultSelect = source["defaultSelect"];
	        this.contentHash = source["contentHash"];
	        this.conflict = source["conflict"];
	        this.destinationPath = source["destinationPath"];
	        this.openSemantics = source["openSemantics"];
	        this.existingFolder = source["existingFolder"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CollectionImportApplyResult {
	    state: types.AppState;
	    applied?: CollectionImportPreviewRow[];
	    skipped?: CollectionImportPreviewRow[];
	    errors?: CollectionImportPreviewRow[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportApplyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = this.convertValues(source["state"], types.AppState);
	        this.applied = this.convertValues(source["applied"], CollectionImportPreviewRow);
	        this.skipped = this.convertValues(source["skipped"], CollectionImportPreviewRow);
	        this.errors = this.convertValues(source["errors"], CollectionImportPreviewRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class CollectionImportPickerResult {
	    paths: string[];
	    cancelled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportPickerResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.cancelled = source["cancelled"];
	    }
	}
	export class CollectionImportPreview {
	    rows: CollectionImportPreviewRow[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rows = this.convertValues(source["rows"], CollectionImportPreviewRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CollectionImportPreviewRequest {
	    workspaceId?: string;
	    destinationRoot?: string;
	    sources: CollectionImportSource[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionImportPreviewRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspaceId = source["workspaceId"];
	        this.destinationRoot = source["destinationRoot"];
	        this.sources = this.convertValues(source["sources"], CollectionImportSource);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	export class DocsServerStatus {
	    collectionId: string;
	    running: boolean;
	    port: number;
	    url?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new DocsServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.collectionId = source["collectionId"];
	        this.running = source["running"];
	        this.port = source["port"];
	        this.url = source["url"];
	        this.error = source["error"];
	    }
	}
	export class HistoryEntry {
	    id: string;
	    // Go type: time
	    at: any;
	    collectionId?: string;
	    itemId?: string;
	    name?: string;
	    method: string;
	    url: string;
	    status?: number;
	    statusText?: string;
	    durationMs?: number;
	    size?: number;
	    error?: string;
	    requestHeaders?: types.KeyValue[];
	    responseHeaders?: types.KeyValue[];
	    redacted?: boolean;
	    bodyHandle?: string;
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.at = this.convertValues(source["at"], null);
	        this.collectionId = source["collectionId"];
	        this.itemId = source["itemId"];
	        this.name = source["name"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.status = source["status"];
	        this.statusText = source["statusText"];
	        this.durationMs = source["durationMs"];
	        this.size = source["size"];
	        this.error = source["error"];
	        this.requestHeaders = this.convertValues(source["requestHeaders"], types.KeyValue);
	        this.responseHeaders = this.convertValues(source["responseHeaders"], types.KeyValue);
	        this.redacted = source["redacted"];
	        this.bodyHandle = source["bodyHandle"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HistoryQuery {
	    text?: string;
	    collectionId?: string;
	    method?: string;
	    onlyFailures?: boolean;
	    limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.collectionId = source["collectionId"];
	        this.method = source["method"];
	        this.onlyFailures = source["onlyFailures"];
	        this.limit = source["limit"];
	    }
	}
	export class MockServerStatus {
	    collectionId: string;
	    running: boolean;
	    port: number;
	    url?: string;
	    routes: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new MockServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.collectionId = source["collectionId"];
	        this.running = source["running"];
	        this.port = source["port"];
	        this.url = source["url"];
	        this.routes = source["routes"];
	        this.error = source["error"];
	    }
	}
	export class RecoveryEntry {
	    id: string;
	    kind: string;
	    displayName: string;
	    workspaceId: string;
	    collectionId: string;
	    // Go type: time
	    deletedAt: any;
	    // Go type: time
	    expiresAt: any;
	    restorable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RecoveryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.displayName = source["displayName"];
	        this.workspaceId = source["workspaceId"];
	        this.collectionId = source["collectionId"];
	        this.deletedAt = this.convertValues(source["deletedAt"], null);
	        this.expiresAt = this.convertValues(source["expiresAt"], null);
	        this.restorable = source["restorable"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RecoverableDeleteResult {
	    state: types.AppState;
	    entry: RecoveryEntry;
	
	    static createFrom(source: any = {}) {
	        return new RecoverableDeleteResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = this.convertValues(source["state"], types.AppState);
	        this.entry = this.convertValues(source["entry"], RecoveryEntry);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RequestMutation {
	    revision: number;
	    collectionId: string;
	    item: types.RequestItem;
	
	    static createFrom(source: any = {}) {
	        return new RequestMutation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.revision = source["revision"];
	        this.collectionId = source["collectionId"];
	        this.item = this.convertValues(source["item"], types.RequestItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResponseBodySaveResult {
	    filename: string;
	    path?: string;
	    byteCount: number;
	    contentType?: string;
	    cancelled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResponseBodySaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filename = source["filename"];
	        this.path = source["path"];
	        this.byteCount = source["byteCount"];
	        this.contentType = source["contentType"];
	        this.cancelled = source["cancelled"];
	    }
	}
	export class ResponseBodySlice {
	    raw: string;
	    base64: string;
	    offset: number;
	    length: number;
	    totalSize: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResponseBodySlice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.raw = source["raw"];
	        this.base64 = source["base64"];
	        this.offset = source["offset"];
	        this.length = source["length"];
	        this.totalSize = source["totalSize"];
	        this.truncated = source["truncated"];
	    }
	}
	export class ResponseTimelineSaveResult {
	    filename: string;
	    path?: string;
	    byteCount: number;
	    cancelled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResponseTimelineSaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filename = source["filename"];
	        this.path = source["path"];
	        this.byteCount = source["byteCount"];
	        this.cancelled = source["cancelled"];
	    }
	}
	export class TabsMutation {
	    revision: number;
	    openTabs: types.OpenTab[];
	    activeTabId: string;
	
	    static createFrom(source: any = {}) {
	        return new TabsMutation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.revision = source["revision"];
	        this.openTabs = this.convertValues(source["openTabs"], types.OpenTab);
	        this.activeTabId = source["activeTabId"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UnsavedDraft {
	    collectionId: string;
	    itemId: string;
	    name: string;
	    transient: boolean;
	    scratch: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UnsavedDraft(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.collectionId = source["collectionId"];
	        this.itemId = source["itemId"];
	        this.name = source["name"];
	        this.transient = source["transient"];
	        this.scratch = source["scratch"];
	    }
	}
	export class WorkspaceWindowTarget {
	    id: string;
	    name: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceWindowTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	    }
	}

}

export namespace types {
	
	export class AWSV4Auth {
	    accessKeyId: string;
	    secretAccessKey: string;
	    sessionToken: string;
	    service: string;
	    region: string;
	    profileName: string;
	    accessKey?: string;
	    secretKey?: string;
	
	    static createFrom(source: any = {}) {
	        return new AWSV4Auth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accessKeyId = source["accessKeyId"];
	        this.secretAccessKey = source["secretAccessKey"];
	        this.sessionToken = source["sessionToken"];
	        this.service = source["service"];
	        this.region = source["region"];
	        this.profileName = source["profileName"];
	        this.accessKey = source["accessKey"];
	        this.secretKey = source["secretKey"];
	    }
	}
	export class RunResult {
	    iteration?: number;
	    itemId: string;
	    name: string;
	    status: string;
	    code: number;
	    durationMs: number;
	    error: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new RunResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.iteration = source["iteration"];
	        this.itemId = source["itemId"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.code = source["code"];
	        this.durationMs = source["durationMs"];
	        this.error = source["error"];
	        this.at = this.convertValues(source["at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunnerSnapshot {
	    total: number;
	    passed: number;
	    failed: number;
	    skipped: number;
	    cancelled?: number;
	    results: RunResult[];
	    // Go type: time
	    finished: any;
	    iterations?: number;
	    completedIterations?: number;
	
	    static createFrom(source: any = {}) {
	        return new RunnerSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.passed = source["passed"];
	        this.failed = source["failed"];
	        this.skipped = source["skipped"];
	        this.cancelled = source["cancelled"];
	        this.results = this.convertValues(source["results"], RunResult);
	        this.finished = this.convertValues(source["finished"], null);
	        this.iterations = source["iterations"];
	        this.completedIterations = source["completedIterations"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NetworkLog {
	    id: string;
	    source?: string;
	    method: string;
	    url: string;
	    status: number;
	    statusText: string;
	    durationMs: number;
	    size: number;
	    // Go type: time
	    at: any;
	    error: string;
	    requestHeaders?: Record<string, string>;
	    requestBody?: string;
	    responseHeaders?: Record<string, string>;
	    responseBody?: string;
	
	    static createFrom(source: any = {}) {
	        return new NetworkLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.status = source["status"];
	        this.statusText = source["statusText"];
	        this.durationMs = source["durationMs"];
	        this.size = source["size"];
	        this.at = this.convertValues(source["at"], null);
	        this.error = source["error"];
	        this.requestHeaders = source["requestHeaders"];
	        this.requestBody = source["requestBody"];
	        this.responseHeaders = source["responseHeaders"];
	        this.responseBody = source["responseBody"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Notification {
	    id: string;
	    level: string;
	    type?: string;
	    title?: string;
	    message: string;
	    description?: string;
	    color?: string;
	    read: boolean;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new Notification(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.level = source["level"];
	        this.type = source["type"];
	        this.title = source["title"];
	        this.message = source["message"];
	        this.description = source["description"];
	        this.color = source["color"];
	        this.read = source["read"];
	        this.at = this.convertValues(source["at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProxyPACConfig {
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxyPACConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	    }
	}
	export class ProxyPreferences {
	    disabled?: boolean;
	    source: string;
	    pac: ProxyPACConfig;
	    config: ProxyConfig;
	
	    static createFrom(source: any = {}) {
	        return new ProxyPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.disabled = source["disabled"];
	        this.source = source["source"];
	        this.pac = this.convertValues(source["pac"], ProxyPACConfig);
	        this.config = this.convertValues(source["config"], ProxyConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DevToolsNetworkPreferences {
	    sortKey?: string;
	    sortDirection?: string;
	    columnWidths?: number[];
	
	    static createFrom(source: any = {}) {
	        return new DevToolsNetworkPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sortKey = source["sortKey"];
	        this.sortDirection = source["sortDirection"];
	        this.columnWidths = source["columnWidths"];
	    }
	}
	export class DevToolsPreferences {
	    open: boolean;
	    activeTab?: string;
	    drawerHeight?: number;
	    detailsPanelWidth?: number;
	    network: DevToolsNetworkPreferences;
	
	    static createFrom(source: any = {}) {
	        return new DevToolsPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.open = source["open"];
	        this.activeTab = source["activeTab"];
	        this.drawerHeight = source["drawerHeight"];
	        this.detailsPanelWidth = source["detailsPanelWidth"];
	        this.network = this.convertValues(source["network"], DevToolsNetworkPreferences);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FileCachePreferences {
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileCachePreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	    }
	}
	export class SSLSessionCachePreferences {
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SSLSessionCachePreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	    }
	}
	export class CachePreferences {
	    sslSession: SSLSessionCachePreferences;
	    file: FileCachePreferences;
	
	    static createFrom(source: any = {}) {
	        return new CachePreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sslSession = this.convertValues(source["sslSession"], SSLSessionCachePreferences);
	        this.file = this.convertValues(source["file"], FileCachePreferences);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AutoSavePreferences {
	    enabled: boolean;
	    interval?: number;
	
	    static createFrom(source: any = {}) {
	        return new AutoSavePreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.interval = source["interval"];
	    }
	}
	export class GeneralPreferences {
	    defaultLocation?: string;
	    defaultWorkspacePath?: string;
	    lastImportDirectory?: string;
	
	    static createFrom(source: any = {}) {
	        return new GeneralPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.defaultLocation = source["defaultLocation"];
	        this.defaultWorkspacePath = source["defaultWorkspacePath"];
	        this.lastImportDirectory = source["lastImportDirectory"];
	    }
	}
	export class KeepDefaultCaCertificatesPreferences {
	    enabled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new KeepDefaultCaCertificatesPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	    }
	}
	export class CustomCaCertificatePreferences {
	    enabled: boolean;
	    filePath?: string;
	
	    static createFrom(source: any = {}) {
	        return new CustomCaCertificatePreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.filePath = source["filePath"];
	    }
	}
	export class RequestPreferences {
	    sslVerification?: boolean;
	    customCaCertificate: CustomCaCertificatePreferences;
	    keepDefaultCaCertificates: KeepDefaultCaCertificatesPreferences;
	    storeCookies?: boolean;
	    sendCookies?: boolean;
	    timeout?: number;
	    maxResponseBytes?: number;
	
	    static createFrom(source: any = {}) {
	        return new RequestPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sslVerification = source["sslVerification"];
	        this.customCaCertificate = this.convertValues(source["customCaCertificate"], CustomCaCertificatePreferences);
	        this.keepDefaultCaCertificates = this.convertValues(source["keepDefaultCaCertificates"], KeepDefaultCaCertificatesPreferences);
	        this.storeCookies = source["storeCookies"];
	        this.sendCookies = source["sendCookies"];
	        this.timeout = source["timeout"];
	        this.maxResponseBytes = source["maxResponseBytes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FontPreferences {
	    codeFont?: string;
	    codeFontSize?: number;
	
	    static createFrom(source: any = {}) {
	        return new FontPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.codeFont = source["codeFont"];
	        this.codeFontSize = source["codeFontSize"];
	    }
	}
	export class DisplayPreferences {
	    zoomPercentage?: number;
	
	    static createFrom(source: any = {}) {
	        return new DisplayPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.zoomPercentage = source["zoomPercentage"];
	    }
	}
	export class LayoutPreferences {
	    responsePaneOrientation?: string;
	
	    static createFrom(source: any = {}) {
	        return new LayoutPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.responsePaneOrientation = source["responsePaneOrientation"];
	    }
	}
	export class KeyBinding {
	    name?: string;
	    mac?: string;
	    windows?: string;
	
	    static createFrom(source: any = {}) {
	        return new KeyBinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.mac = source["mac"];
	        this.windows = source["windows"];
	    }
	}
	export class Preferences {
	    theme: string;
	    themeVariantLight?: string;
	    themeVariantDark?: string;
	    keybindingsEnabled?: boolean;
	    keyBindings?: Record<string, KeyBinding>;
	    keyBindingPreset?: string;
	    layout: LayoutPreferences;
	    display: DisplayPreferences;
	    font: FontPreferences;
	    request: RequestPreferences;
	    general: GeneralPreferences;
	    autoSave: AutoSavePreferences;
	    cache: CachePreferences;
	    devTools: DevToolsPreferences;
	    autosave: boolean;
	    defaultCollectionPath: string;
	    codeFontSize: number;
	    storeCookies: boolean;
	    oauth2UseSystemBrowser: boolean;
	    proxyMode: string;
	    proxy: ProxyPreferences;
	
	    static createFrom(source: any = {}) {
	        return new Preferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.themeVariantLight = source["themeVariantLight"];
	        this.themeVariantDark = source["themeVariantDark"];
	        this.keybindingsEnabled = source["keybindingsEnabled"];
	        this.keyBindings = this.convertValues(source["keyBindings"], KeyBinding, true);
	        this.keyBindingPreset = source["keyBindingPreset"];
	        this.layout = this.convertValues(source["layout"], LayoutPreferences);
	        this.display = this.convertValues(source["display"], DisplayPreferences);
	        this.font = this.convertValues(source["font"], FontPreferences);
	        this.request = this.convertValues(source["request"], RequestPreferences);
	        this.general = this.convertValues(source["general"], GeneralPreferences);
	        this.autoSave = this.convertValues(source["autoSave"], AutoSavePreferences);
	        this.cache = this.convertValues(source["cache"], CachePreferences);
	        this.devTools = this.convertValues(source["devTools"], DevToolsPreferences);
	        this.autosave = source["autosave"];
	        this.defaultCollectionPath = source["defaultCollectionPath"];
	        this.codeFontSize = source["codeFontSize"];
	        this.storeCookies = source["storeCookies"];
	        this.oauth2UseSystemBrowser = source["oauth2UseSystemBrowser"];
	        this.proxyMode = source["proxyMode"];
	        this.proxy = this.convertValues(source["proxy"], ProxyPreferences);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Feature {
	    id: string;
	    name: string;
	    category: string;
	    status: string;
	    description: string;
	    tests: string[];
	    sourceRefs: string[];
	
	    static createFrom(source: any = {}) {
	        return new Feature(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.status = source["status"];
	        this.description = source["description"];
	        this.tests = source["tests"];
	        this.sourceRefs = source["sourceRefs"];
	    }
	}
	export class OpenTab {
	    id: string;
	    collectionId: string;
	    itemId: string;
	    kind: string;
	    exampleId?: string;
	    exampleName?: string;
	    requestPaneTab: string;
	    responseTab: string;
	    transient?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenTab(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.collectionId = source["collectionId"];
	        this.itemId = source["itemId"];
	        this.kind = source["kind"];
	        this.exampleId = source["exampleId"];
	        this.exampleName = source["exampleName"];
	        this.requestPaneTab = source["requestPaneTab"];
	        this.responseTab = source["responseTab"];
	        this.transient = source["transient"];
	    }
	}
	export class OpenAPISyncConfig {
	    sourceUrl: string;
	    groupBy: string;
	    lastSyncDate?: string;
	    specHash?: string;
	    autoCheck: boolean;
	    autoCheckInterval: number;
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.groupBy = source["groupBy"];
	        this.lastSyncDate = source["lastSyncDate"];
	        this.specHash = source["specHash"];
	        this.autoCheck = source["autoCheck"];
	        this.autoCheckInterval = source["autoCheckInterval"];
	    }
	}
	export class CollectionSecurityConfig {
	    jsSandboxMode: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionSecurityConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.jsSandboxMode = source["jsSandboxMode"];
	    }
	}
	export class CollectionProtoImportPath {
	    path: string;
	    enabled: boolean;
	    exists?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionProtoImportPath(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.enabled = source["enabled"];
	        this.exists = source["exists"];
	    }
	}
	export class CollectionProtoFile {
	    path: string;
	    type?: string;
	    exists?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionProtoFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.type = source["type"];
	        this.exists = source["exists"];
	    }
	}
	export class CollectionProtobufConfig {
	    protoFiles: CollectionProtoFile[];
	    importPaths: CollectionProtoImportPath[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionProtobufConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.protoFiles = this.convertValues(source["protoFiles"], CollectionProtoFile);
	        this.importPaths = this.convertValues(source["importPaths"], CollectionProtoImportPath);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CollectionPresets {
	    requestType: string;
	    requestUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionPresets(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestType = source["requestType"];
	        this.requestUrl = source["requestUrl"];
	    }
	}
	export class ClientCertificateConfig {
	    domain: string;
	    type: string;
	    certFilePath?: string;
	    keyFilePath?: string;
	    pfxFilePath?: string;
	    passphrase?: string;
	
	    static createFrom(source: any = {}) {
	        return new ClientCertificateConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domain = source["domain"];
	        this.type = source["type"];
	        this.certFilePath = source["certFilePath"];
	        this.keyFilePath = source["keyFilePath"];
	        this.pfxFilePath = source["pfxFilePath"];
	        this.passphrase = source["passphrase"];
	    }
	}
	export class ProxyAuthConfig {
	    username: string;
	    password: string;
	    disabled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProxyAuthConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.password = source["password"];
	        this.disabled = source["disabled"];
	    }
	}
	export class ProxyConfig {
	    inherit: boolean;
	    disabled?: boolean;
	    protocol: string;
	    hostname: string;
	    port: string;
	    auth: ProxyAuthConfig;
	    bypassProxy: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxyConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inherit = source["inherit"];
	        this.disabled = source["disabled"];
	        this.protocol = source["protocol"];
	        this.hostname = source["hostname"];
	        this.port = source["port"];
	        this.auth = this.convertValues(source["auth"], ProxyAuthConfig);
	        this.bypassProxy = source["bypassProxy"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Environment {
	    id: string;
	    name: string;
	    color: string;
	    variables: Variable[];
	
	    static createFrom(source: any = {}) {
	        return new Environment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.color = source["color"];
	        this.variables = this.convertValues(source["variables"], Variable);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FolderConfig {
	    path: string;
	    displayPath: string;
	    name: string;
	    seq: number;
	    headers: KeyValue[];
	    variables: Variable[];
	    resVariables: Variable[];
	    auth: AuthConfig;
	    preScript: string;
	    postScript: string;
	    tests: string;
	    docs: string;
	
	    static createFrom(source: any = {}) {
	        return new FolderConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.displayPath = source["displayPath"];
	        this.name = source["name"];
	        this.seq = source["seq"];
	        this.headers = this.convertValues(source["headers"], KeyValue);
	        this.variables = this.convertValues(source["variables"], Variable);
	        this.resVariables = this.convertValues(source["resVariables"], Variable);
	        this.auth = this.convertValues(source["auth"], AuthConfig);
	        this.preScript = source["preScript"];
	        this.postScript = source["postScript"];
	        this.tests = source["tests"];
	        this.docs = source["docs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TimelineItem {
	    id: string;
	    kind: string;
	    eventType?: string;
	    eventName?: string;
	    message: string;
	    // Go type: time
	    at: any;
	    duration: number;
	    requestId: string;
	    source?: string;
	    phase?: string;
	    method?: string;
	    url?: string;
	    status?: number;
	    statusText?: string;
	    error?: string;
	    payload?: string;
	    metadata?: KeyValue[];
	    trailers?: KeyValue[];
	    sourceFile?: string;
	
	    static createFrom(source: any = {}) {
	        return new TimelineItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.eventType = source["eventType"];
	        this.eventName = source["eventName"];
	        this.message = source["message"];
	        this.at = this.convertValues(source["at"], null);
	        this.duration = source["duration"];
	        this.requestId = source["requestId"];
	        this.source = source["source"];
	        this.phase = source["phase"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.status = source["status"];
	        this.statusText = source["statusText"];
	        this.error = source["error"];
	        this.payload = source["payload"];
	        this.metadata = this.convertValues(source["metadata"], KeyValue);
	        this.trailers = this.convertValues(source["trailers"], KeyValue);
	        this.sourceFile = source["sourceFile"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResponseTimings {
	    dnsMs: number;
	    connectMs: number;
	    tlsMs: number;
	    uploadMs: number;
	    waitMs: number;
	    downloadMs: number;
	    totalMs: number;
	    redirectCount: number;
	    connectionReused: boolean;
	    dnsAvailable: boolean;
	    connectAvailable: boolean;
	    tlsAvailable: boolean;
	    uploadAvailable: boolean;
	    waitAvailable: boolean;
	    downloadAvailable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResponseTimings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dnsMs = source["dnsMs"];
	        this.connectMs = source["connectMs"];
	        this.tlsMs = source["tlsMs"];
	        this.uploadMs = source["uploadMs"];
	        this.waitMs = source["waitMs"];
	        this.downloadMs = source["downloadMs"];
	        this.totalMs = source["totalMs"];
	        this.redirectCount = source["redirectCount"];
	        this.connectionReused = source["connectionReused"];
	        this.dnsAvailable = source["dnsAvailable"];
	        this.connectAvailable = source["connectAvailable"];
	        this.tlsAvailable = source["tlsAvailable"];
	        this.uploadAvailable = source["uploadAvailable"];
	        this.waitAvailable = source["waitAvailable"];
	        this.downloadAvailable = source["downloadAvailable"];
	    }
	}
	export class CookieEntry {
	    id: string;
	    name: string;
	    value: string;
	    domain: string;
	    path: string;
	    // Go type: time
	    expires: any;
	    session: boolean;
	    secure: boolean;
	    httpOnly: boolean;
	    sameSite: string;
	    hostOnly: boolean;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new CookieEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.value = source["value"];
	        this.domain = source["domain"];
	        this.path = source["path"];
	        this.expires = this.convertValues(source["expires"], null);
	        this.session = source["session"];
	        this.secure = source["secure"];
	        this.httpOnly = source["httpOnly"];
	        this.sameSite = source["sameSite"];
	        this.hostOnly = source["hostOnly"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ScriptLog {
	    level: string;
	    message: string;
	    args: string[];
	
	    static createFrom(source: any = {}) {
	        return new ScriptLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.level = source["level"];
	        this.message = source["message"];
	        this.args = source["args"];
	    }
	}
	export class TestResult {
	    name: string;
	    passed: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.passed = source["passed"];
	        this.message = source["message"];
	    }
	}
	export class VisualizerPayload {
	    template: string;
	    data?: string;
	
	    static createFrom(source: any = {}) {
	        return new VisualizerPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.template = source["template"];
	        this.data = source["data"];
	    }
	}
	export class Response {
	    status: number;
	    statusText: string;
	    headers: Record<string, string>;
	    headerEntries?: KeyValue[];
	    metadata?: KeyValue[];
	    trailers?: KeyValue[];
	    body: string;
	    bodyBase64: string;
	    bodyHandle?: string;
	    bodyHead?: string;
	    visualizer?: VisualizerPayload;
	    size: number;
	    durationMs: number;
	    error: string;
	    cancelled?: boolean;
	    previewMode: string;
	    testResults: TestResult[];
	    scriptLogs: ScriptLog[];
	    assertions: Assertion[];
	    requestedUrl: string;
	    // Go type: time
	    sentAt: any;
	    cookies: CookieEntry[];
	    timings: ResponseTimings;
	
	    static createFrom(source: any = {}) {
	        return new Response(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.statusText = source["statusText"];
	        this.headers = source["headers"];
	        this.headerEntries = this.convertValues(source["headerEntries"], KeyValue);
	        this.metadata = this.convertValues(source["metadata"], KeyValue);
	        this.trailers = this.convertValues(source["trailers"], KeyValue);
	        this.body = source["body"];
	        this.bodyBase64 = source["bodyBase64"];
	        this.bodyHandle = source["bodyHandle"];
	        this.bodyHead = source["bodyHead"];
	        this.visualizer = this.convertValues(source["visualizer"], VisualizerPayload);
	        this.size = source["size"];
	        this.durationMs = source["durationMs"];
	        this.error = source["error"];
	        this.cancelled = source["cancelled"];
	        this.previewMode = source["previewMode"];
	        this.testResults = this.convertValues(source["testResults"], TestResult);
	        this.scriptLogs = this.convertValues(source["scriptLogs"], ScriptLog);
	        this.assertions = this.convertValues(source["assertions"], Assertion);
	        this.requestedUrl = source["requestedUrl"];
	        this.sentAt = this.convertValues(source["sentAt"], null);
	        this.cookies = this.convertValues(source["cookies"], CookieEntry);
	        this.timings = this.convertValues(source["timings"], ResponseTimings);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResponseExamplePayload {
	    status: number;
	    statusText: string;
	    headers: KeyValue[];
	    bodyType: string;
	    body: string;
	    size: number;
	    durationMs?: number;
	
	    static createFrom(source: any = {}) {
	        return new ResponseExamplePayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.statusText = source["statusText"];
	        this.headers = this.convertValues(source["headers"], KeyValue);
	        this.bodyType = source["bodyType"];
	        this.body = source["body"];
	        this.size = source["size"];
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResponseExampleRequest {
	    method: string;
	    url: string;
	    bodyMode: string;
	    body: string;
	    headers: KeyValue[];
	    params: KeyValue[];
	    formUrlEncoded: KeyValue[];
	    multipartForm: FormPart[];
	    file: FileBodyEntry[];
	
	    static createFrom(source: any = {}) {
	        return new ResponseExampleRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.url = source["url"];
	        this.bodyMode = source["bodyMode"];
	        this.body = source["body"];
	        this.headers = this.convertValues(source["headers"], KeyValue);
	        this.params = this.convertValues(source["params"], KeyValue);
	        this.formUrlEncoded = this.convertValues(source["formUrlEncoded"], KeyValue);
	        this.multipartForm = this.convertValues(source["multipartForm"], FormPart);
	        this.file = this.convertValues(source["file"], FileBodyEntry);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResponseExample {
	    id: string;
	    name: string;
	    description: string;
	    type: string;
	    request: ResponseExampleRequest;
	    response: ResponseExamplePayload;
	
	    static createFrom(source: any = {}) {
	        return new ResponseExample(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.type = source["type"];
	        this.request = this.convertValues(source["request"], ResponseExampleRequest);
	        this.response = this.convertValues(source["response"], ResponseExamplePayload);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RequestSettings {
	    timeoutMs: number;
	    followRedirects: boolean;
	    maxRedirects: number;
	    disableParsingResponseJson?: boolean;
	    encodeUrl: boolean;
	    storeCookies: boolean;
	    verifyTls: boolean;
	    keepAliveInterval: number;
	
	    static createFrom(source: any = {}) {
	        return new RequestSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timeoutMs = source["timeoutMs"];
	        this.followRedirects = source["followRedirects"];
	        this.maxRedirects = source["maxRedirects"];
	        this.disableParsingResponseJson = source["disableParsingResponseJson"];
	        this.encodeUrl = source["encodeUrl"];
	        this.storeCookies = source["storeCookies"];
	        this.verifyTls = source["verifyTls"];
	        this.keepAliveInterval = source["keepAliveInterval"];
	    }
	}
	export class Assertion {
	    expression: string;
	    operator: string;
	    value: string;
	    enabled: boolean;
	    passed: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Assertion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.expression = source["expression"];
	        this.operator = source["operator"];
	        this.value = source["value"];
	        this.enabled = source["enabled"];
	        this.passed = source["passed"];
	        this.message = source["message"];
	    }
	}
	export class Variable {
	    id: string;
	    name: string;
	    value: any;
	    type: string;
	    dataType: string;
	    enabled: boolean;
	    secret: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Variable(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.value = source["value"];
	        this.type = source["type"];
	        this.dataType = source["dataType"];
	        this.enabled = source["enabled"];
	        this.secret = source["secret"];
	    }
	}
	export class RequestVars {
	    req: Variable[];
	    res: Variable[];
	
	    static createFrom(source: any = {}) {
	        return new RequestVars(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.req = this.convertValues(source["req"], Variable);
	        this.res = this.convertValues(source["res"], Variable);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OAuth1Auth {
	    consumerKey: string;
	    consumerSecret: string;
	    accessToken: string;
	    accessTokenSecret: string;
	    callbackUrl: string;
	    verifier: string;
	    signatureMethod: string;
	    privateKey: string;
	    privateKeyType: string;
	    timestamp: string;
	    nonce: string;
	    version: string;
	    realm: string;
	    placement: string;
	    includeBodyHash: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OAuth1Auth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.consumerKey = source["consumerKey"];
	        this.consumerSecret = source["consumerSecret"];
	        this.accessToken = source["accessToken"];
	        this.accessTokenSecret = source["accessTokenSecret"];
	        this.callbackUrl = source["callbackUrl"];
	        this.verifier = source["verifier"];
	        this.signatureMethod = source["signatureMethod"];
	        this.privateKey = source["privateKey"];
	        this.privateKeyType = source["privateKeyType"];
	        this.timestamp = source["timestamp"];
	        this.nonce = source["nonce"];
	        this.version = source["version"];
	        this.realm = source["realm"];
	        this.placement = source["placement"];
	        this.includeBodyHash = source["includeBodyHash"];
	    }
	}
	export class OAuth2AdditionalParam {
	    name: string;
	    value: string;
	    sendIn: string;
	    enabled: boolean;
	    secret: boolean;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new OAuth2AdditionalParam(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.value = source["value"];
	        this.sendIn = source["sendIn"];
	        this.enabled = source["enabled"];
	        this.secret = source["secret"];
	        this.description = source["description"];
	    }
	}
	export class OAuth2Auth {
	    grantType: string;
	    callbackUrl: string;
	    authorizationUrl: string;
	    accessTokenUrl: string;
	    refreshTokenUrl: string;
	    username: string;
	    password: string;
	    clientId: string;
	    clientSecret: string;
	    scope: string;
	    state: string;
	    pkce: boolean;
	    credentialsPlacement: string;
	    credentialsId: string;
	    tokenSource: string;
	    tokenPlacement: string;
	    tokenHeaderPrefix: string;
	    tokenQueryKey: string;
	    autoFetchToken: boolean;
	    autoRefreshToken: boolean;
	    authorizationAdditionalParams: OAuth2AdditionalParam[];
	    tokenAdditionalParams: OAuth2AdditionalParam[];
	    refreshAdditionalParams: OAuth2AdditionalParam[];
	    additionalParams: KeyValue[];
	
	    static createFrom(source: any = {}) {
	        return new OAuth2Auth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.grantType = source["grantType"];
	        this.callbackUrl = source["callbackUrl"];
	        this.authorizationUrl = source["authorizationUrl"];
	        this.accessTokenUrl = source["accessTokenUrl"];
	        this.refreshTokenUrl = source["refreshTokenUrl"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.clientId = source["clientId"];
	        this.clientSecret = source["clientSecret"];
	        this.scope = source["scope"];
	        this.state = source["state"];
	        this.pkce = source["pkce"];
	        this.credentialsPlacement = source["credentialsPlacement"];
	        this.credentialsId = source["credentialsId"];
	        this.tokenSource = source["tokenSource"];
	        this.tokenPlacement = source["tokenPlacement"];
	        this.tokenHeaderPrefix = source["tokenHeaderPrefix"];
	        this.tokenQueryKey = source["tokenQueryKey"];
	        this.autoFetchToken = source["autoFetchToken"];
	        this.autoRefreshToken = source["autoRefreshToken"];
	        this.authorizationAdditionalParams = this.convertValues(source["authorizationAdditionalParams"], OAuth2AdditionalParam);
	        this.tokenAdditionalParams = this.convertValues(source["tokenAdditionalParams"], OAuth2AdditionalParam);
	        this.refreshAdditionalParams = this.convertValues(source["refreshAdditionalParams"], OAuth2AdditionalParam);
	        this.additionalParams = this.convertValues(source["additionalParams"], KeyValue);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AuthConfig {
	    mode: string;
	    username: string;
	    password: string;
	    domain: string;
	    token: string;
	    apiKey: string;
	    apiValue: string;
	    apiLocation: string;
	    oauth2: OAuth2Auth;
	    oauth1: OAuth1Auth;
	    awsv4: AWSV4Auth;
	
	    static createFrom(source: any = {}) {
	        return new AuthConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.domain = source["domain"];
	        this.token = source["token"];
	        this.apiKey = source["apiKey"];
	        this.apiValue = source["apiValue"];
	        this.apiLocation = source["apiLocation"];
	        this.oauth2 = this.convertValues(source["oauth2"], OAuth2Auth);
	        this.oauth1 = this.convertValues(source["oauth1"], OAuth1Auth);
	        this.awsv4 = this.convertValues(source["awsv4"], AWSV4Auth);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WSMessage {
	    name: string;
	    type: string;
	    content: string;
	    selected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WSMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.content = source["content"];
	        this.selected = source["selected"];
	    }
	}
	export class GrpcMessage {
	    name: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new GrpcMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.content = source["content"];
	    }
	}
	export class FileBodyEntry {
	    filePath: string;
	    contentType: string;
	    selected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileBodyEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filePath = source["filePath"];
	        this.contentType = source["contentType"];
	        this.selected = source["selected"];
	    }
	}
	export class FormPart {
	    name: string;
	    value: string;
	    filePath: string;
	    contentType: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FormPart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.value = source["value"];
	        this.filePath = source["filePath"];
	        this.contentType = source["contentType"];
	        this.enabled = source["enabled"];
	    }
	}
	export class RequestBody {
	    mode: string;
	    text: string;
	    json: string;
	    xml: string;
	    graphqlQuery: string;
	    graphqlVariables: string;
	    formUrlEncoded: KeyValue[];
	    multipart: FormPart[];
	    filePath: string;
	    fileContentType: string;
	    files: FileBodyEntry[];
	
	    static createFrom(source: any = {}) {
	        return new RequestBody(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.text = source["text"];
	        this.json = source["json"];
	        this.xml = source["xml"];
	        this.graphqlQuery = source["graphqlQuery"];
	        this.graphqlVariables = source["graphqlVariables"];
	        this.formUrlEncoded = this.convertValues(source["formUrlEncoded"], KeyValue);
	        this.multipart = this.convertValues(source["multipart"], FormPart);
	        this.filePath = source["filePath"];
	        this.fileContentType = source["fileContentType"];
	        this.files = this.convertValues(source["files"], FileBodyEntry);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KeyValue {
	    name: string;
	    value: string;
	    enabled: boolean;
	    secret: boolean;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new KeyValue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.value = source["value"];
	        this.enabled = source["enabled"];
	        this.secret = source["secret"];
	        this.description = source["description"];
	    }
	}
	export class RequestItem {
	    id: string;
	    name: string;
	    type: string;
	    method: string;
	    url: string;
	    params: KeyValue[];
	    pathParams: KeyValue[];
	    headers: KeyValue[];
	    body: RequestBody;
	    protoPath: string;
	    grpcMethodType: string;
	    grpcMessages: GrpcMessage[];
	    wsMessages: WSMessage[];
	    auth: AuthConfig;
	    vars: RequestVars;
	    assertions: Assertion[];
	    tests: string;
	    preScript: string;
	    postScript: string;
	    docs: string;
	    settings: RequestSettings;
	    tags: string[];
	    folderPath: string;
	    filePath: string;
	    examples: ResponseExample[];
	    response?: Response;
	    timeline: TimelineItem[];
	    draft: boolean;
	    transient?: boolean;
	    seq: number;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new RequestItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.params = this.convertValues(source["params"], KeyValue);
	        this.pathParams = this.convertValues(source["pathParams"], KeyValue);
	        this.headers = this.convertValues(source["headers"], KeyValue);
	        this.body = this.convertValues(source["body"], RequestBody);
	        this.protoPath = source["protoPath"];
	        this.grpcMethodType = source["grpcMethodType"];
	        this.grpcMessages = this.convertValues(source["grpcMessages"], GrpcMessage);
	        this.wsMessages = this.convertValues(source["wsMessages"], WSMessage);
	        this.auth = this.convertValues(source["auth"], AuthConfig);
	        this.vars = this.convertValues(source["vars"], RequestVars);
	        this.assertions = this.convertValues(source["assertions"], Assertion);
	        this.tests = source["tests"];
	        this.preScript = source["preScript"];
	        this.postScript = source["postScript"];
	        this.docs = source["docs"];
	        this.settings = this.convertValues(source["settings"], RequestSettings);
	        this.tags = source["tags"];
	        this.folderPath = source["folderPath"];
	        this.filePath = source["filePath"];
	        this.examples = this.convertValues(source["examples"], ResponseExample);
	        this.response = this.convertValues(source["response"], Response);
	        this.timeline = this.convertValues(source["timeline"], TimelineItem);
	        this.draft = source["draft"];
	        this.transient = source["transient"];
	        this.seq = source["seq"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Collection {
	    id: string;
	    name: string;
	    version?: string;
	    path: string;
	    format: string;
	    remote?: string;
	    notFoundLocally?: boolean;
	    scratch?: boolean;
	    items: RequestItem[];
	    folders: FolderConfig[];
	    environments: Environment[];
	    variables: Variable[];
	    runtimeVariables?: Variable[];
	    resVariables: Variable[];
	    headers: KeyValue[];
	    auth: AuthConfig;
	    proxy: ProxyConfig;
	    clientCertificates: ClientCertificateConfig[];
	    presets: CollectionPresets;
	    protobuf: CollectionProtobufConfig;
	    securityConfig: CollectionSecurityConfig;
	    openapi?: OpenAPISyncConfig[];
	    preScript: string;
	    postScript: string;
	    tests: string;
	    docs: string;
	    tags: string[];
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Collection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.path = source["path"];
	        this.format = source["format"];
	        this.remote = source["remote"];
	        this.notFoundLocally = source["notFoundLocally"];
	        this.scratch = source["scratch"];
	        this.items = this.convertValues(source["items"], RequestItem);
	        this.folders = this.convertValues(source["folders"], FolderConfig);
	        this.environments = this.convertValues(source["environments"], Environment);
	        this.variables = this.convertValues(source["variables"], Variable);
	        this.runtimeVariables = this.convertValues(source["runtimeVariables"], Variable);
	        this.resVariables = this.convertValues(source["resVariables"], Variable);
	        this.headers = this.convertValues(source["headers"], KeyValue);
	        this.auth = this.convertValues(source["auth"], AuthConfig);
	        this.proxy = this.convertValues(source["proxy"], ProxyConfig);
	        this.clientCertificates = this.convertValues(source["clientCertificates"], ClientCertificateConfig);
	        this.presets = this.convertValues(source["presets"], CollectionPresets);
	        this.protobuf = this.convertValues(source["protobuf"], CollectionProtobufConfig);
	        this.securityConfig = this.convertValues(source["securityConfig"], CollectionSecurityConfig);
	        this.openapi = this.convertValues(source["openapi"], OpenAPISyncConfig);
	        this.preScript = source["preScript"];
	        this.postScript = source["postScript"];
	        this.tests = source["tests"];
	        this.docs = source["docs"];
	        this.tags = source["tags"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Workspace {
	    id: string;
	    name: string;
	    path: string;
	    scratchCollectionId?: string;
	    scratchTempDirectory?: string;
	    collections: Collection[];
	    globalEnvironments: Environment[];
	    activeGlobalEnvironmentId: string;
	    docs: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.scratchCollectionId = source["scratchCollectionId"];
	        this.scratchTempDirectory = source["scratchTempDirectory"];
	        this.collections = this.convertValues(source["collections"], Collection);
	        this.globalEnvironments = this.convertValues(source["globalEnvironments"], Environment);
	        this.activeGlobalEnvironmentId = source["activeGlobalEnvironmentId"];
	        this.docs = source["docs"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AppState {
	    workspaces: Workspace[];
	    activeWorkspaceId: string;
	    openTabs: OpenTab[];
	    closedTabs?: OpenTab[];
	    activeTabId: string;
	    featureLedger: Feature[];
	    globalEnvironments: Environment[];
	    preferences: Preferences;
	    notifications: Notification[];
	    networkLog: NetworkLog[];
	    runner: RunnerSnapshot;
	    cookies: CookieEntry[];
	    revision: number;
	
	    static createFrom(source: any = {}) {
	        return new AppState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspaces = this.convertValues(source["workspaces"], Workspace);
	        this.activeWorkspaceId = source["activeWorkspaceId"];
	        this.openTabs = this.convertValues(source["openTabs"], OpenTab);
	        this.closedTabs = this.convertValues(source["closedTabs"], OpenTab);
	        this.activeTabId = source["activeTabId"];
	        this.featureLedger = this.convertValues(source["featureLedger"], Feature);
	        this.globalEnvironments = this.convertValues(source["globalEnvironments"], Environment);
	        this.preferences = this.convertValues(source["preferences"], Preferences);
	        this.notifications = this.convertValues(source["notifications"], Notification);
	        this.networkLog = this.convertValues(source["networkLog"], NetworkLog);
	        this.runner = this.convertValues(source["runner"], RunnerSnapshot);
	        this.cookies = this.convertValues(source["cookies"], CookieEntry);
	        this.revision = source["revision"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	
	export class CollectionExportOptions {
	    format: string;
	
	    static createFrom(source: any = {}) {
	        return new CollectionExportOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	    }
	}
	export class CollectionExportResult {
	    format: string;
	    filename: string;
	    content?: string;
	    contentBase64?: string;
	    mimeType: string;
	    warning?: string;
	    skippedTypes?: string[];
	    folderCount: number;
	    requestCount: number;
	    environmentCount: number;
	
	    static createFrom(source: any = {}) {
	        return new CollectionExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.filename = source["filename"];
	        this.content = source["content"];
	        this.contentBase64 = source["contentBase64"];
	        this.mimeType = source["mimeType"];
	        this.warning = source["warning"];
	        this.skippedTypes = source["skippedTypes"];
	        this.folderCount = source["folderCount"];
	        this.requestCount = source["requestCount"];
	        this.environmentCount = source["environmentCount"];
	    }
	}
	
	
	
	
	export class CollectionSaveResult {
	    format: string;
	    path: string;
	    cancelled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CollectionSaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.path = source["path"];
	        this.cancelled = source["cancelled"];
	    }
	}
	
	export class CollectionWatchRefreshResult {
	    state: AppState;
	    changed: boolean;
	    refreshed?: string[];
	    skippedDirty?: string[];
	    missing?: string[];
	    errors?: string[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionWatchRefreshResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = this.convertValues(source["state"], AppState);
	        this.changed = source["changed"];
	        this.refreshed = source["refreshed"];
	        this.skippedDirty = source["skippedDirty"];
	        this.missing = source["missing"];
	        this.errors = source["errors"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class CookieInput {
	    id: string;
	    name: string;
	    value: string;
	    domain: string;
	    path: string;
	    expires: string;
	    session: boolean;
	    secure: boolean;
	    httpOnly: boolean;
	    sameSite: string;
	    hostOnly: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CookieInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.value = source["value"];
	        this.domain = source["domain"];
	        this.path = source["path"];
	        this.expires = source["expires"];
	        this.session = source["session"];
	        this.secure = source["secure"];
	        this.httpOnly = source["httpOnly"];
	        this.sameSite = source["sameSite"];
	        this.hostOnly = source["hostOnly"];
	    }
	}
	
	
	
	export class DevToolsProcessMetric {
	    pid: number;
	    title: string;
	    type: string;
	    cpuPercent: number;
	    memoryBytes: number;
	    uptimeSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new DevToolsProcessMetric(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.title = source["title"];
	        this.type = source["type"];
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryBytes = source["memoryBytes"];
	        this.uptimeSeconds = source["uptimeSeconds"];
	    }
	}
	export class DevToolsSnapshot {
	    pid: number;
	    cpuPercent: number;
	    uptimeSeconds: number;
	    memoryBytes: number;
	    heapAllocBytes: number;
	    goroutines: number;
	    networkRequests: number;
	    consoleLogs: number;
	    processes: DevToolsProcessMetric[];
	    // Go type: time
	    timestamp: any;
	
	    static createFrom(source: any = {}) {
	        return new DevToolsSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.cpuPercent = source["cpuPercent"];
	        this.uptimeSeconds = source["uptimeSeconds"];
	        this.memoryBytes = source["memoryBytes"];
	        this.heapAllocBytes = source["heapAllocBytes"];
	        this.goroutines = source["goroutines"];
	        this.networkRequests = source["networkRequests"];
	        this.consoleLogs = source["consoleLogs"];
	        this.processes = this.convertValues(source["processes"], DevToolsProcessMetric);
	        this.timestamp = this.convertValues(source["timestamp"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class DotEnvFile {
	    scope: string;
	    name: string;
	    path: string;
	    content: string;
	    runtime: boolean;
	    size: number;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new DotEnvFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scope = source["scope"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.content = source["content"];
	        this.runtime = source["runtime"];
	        this.size = source["size"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	
	
	export class GRPCMethodInfo {
	    path: string;
	    service: string;
	    name: string;
	    type: string;
	    inputType: string;
	    outputType: string;
	    template: string;
	
	    static createFrom(source: any = {}) {
	        return new GRPCMethodInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.service = source["service"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.inputType = source["inputType"];
	        this.outputType = source["outputType"];
	        this.template = source["template"];
	    }
	}
	
	export class GenerateCollectionDocsOptions {
	    environmentIds: string[];
	
	    static createFrom(source: any = {}) {
	        return new GenerateCollectionDocsOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.environmentIds = source["environmentIds"];
	    }
	}
	export class GenerateCollectionDocsResult {
	    fileName: string;
	    html: string;
	    yaml: string;
	    version: string;
	    folderCount: number;
	    requestCount: number;
	
	    static createFrom(source: any = {}) {
	        return new GenerateCollectionDocsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileName = source["fileName"];
	        this.html = source["html"];
	        this.yaml = source["yaml"];
	        this.version = source["version"];
	        this.folderCount = source["folderCount"];
	        this.requestCount = source["requestCount"];
	    }
	}
	export class GitCollectionCandidate {
	    name: string;
	    path: string;
	    format: string;
	    requestCount: number;
	
	    static createFrom(source: any = {}) {
	        return new GitCollectionCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.format = source["format"];
	        this.requestCount = source["requestCount"];
	    }
	}
	export class GitCloneResult {
	    version: string;
	    targetPath: string;
	    output: string;
	    candidates: GitCollectionCandidate[];
	
	    static createFrom(source: any = {}) {
	        return new GitCloneResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.targetPath = source["targetPath"];
	        this.output = source["output"];
	        this.candidates = this.convertValues(source["candidates"], GitCollectionCandidate);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class GlobalEnvironmentExportFile {
	    name: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new GlobalEnvironmentExportFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.content = source["content"];
	    }
	}
	export class GlobalEnvironmentExportResult {
	    format: string;
	    filename: string;
	    content: string;
	    files: GlobalEnvironmentExportFile[];
	
	    static createFrom(source: any = {}) {
	        return new GlobalEnvironmentExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.filename = source["filename"];
	        this.content = source["content"];
	        this.files = this.convertValues(source["files"], GlobalEnvironmentExportFile);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GlobalEnvironmentSaveResult {
	    format: string;
	    path: string;
	    files: string[];
	    cancelled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GlobalEnvironmentSaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.path = source["path"];
	        this.files = source["files"];
	        this.cancelled = source["cancelled"];
	    }
	}
	
	export class ImportPayload {
	    kind: string;
	    name: string;
	    content: string;
	    groupBy: string;
	    sourceUrl: string;
	    openapiSync: boolean;
	    translatePostmanScripts?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.content = source["content"];
	        this.groupBy = source["groupBy"];
	        this.sourceUrl = source["sourceUrl"];
	        this.openapiSync = source["openapiSync"];
	        this.translatePostmanScripts = source["translatePostmanScripts"];
	    }
	}
	
	
	
	
	
	
	
	
	
	export class OpenAPILocalDriftOptions {
	    resetIds: string[];
	    restoreIds: string[];
	    deleteIds: string[];
	
	    static createFrom(source: any = {}) {
	        return new OpenAPILocalDriftOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.resetIds = source["resetIds"];
	        this.restoreIds = source["restoreIds"];
	        this.deleteIds = source["deleteIds"];
	    }
	}
	export class OpenAPISyncEndpointChange {
	    id: string;
	    method: string;
	    path: string;
	    name: string;
	    change: string;
	    itemId?: string;
	    defaultDecision: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncEndpointChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.method = source["method"];
	        this.path = source["path"];
	        this.name = source["name"];
	        this.change = source["change"];
	        this.itemId = source["itemId"];
	        this.defaultDecision = source["defaultDecision"];
	    }
	}
	export class OpenAPILocalDriftResult {
	    sourceUrl: string;
	    groupBy: string;
	    specEndpointCount: number;
	    collectionEndpointCount: number;
	    modified: number;
	    missing: number;
	    localOnly: number;
	    inSync: number;
	    hasChanges: boolean;
	    noStoredSpec?: boolean;
	    lastSyncDate?: string;
	    changes: OpenAPISyncEndpointChange[];
	
	    static createFrom(source: any = {}) {
	        return new OpenAPILocalDriftResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.groupBy = source["groupBy"];
	        this.specEndpointCount = source["specEndpointCount"];
	        this.collectionEndpointCount = source["collectionEndpointCount"];
	        this.modified = source["modified"];
	        this.missing = source["missing"];
	        this.localOnly = source["localOnly"];
	        this.inSync = source["inSync"];
	        this.hasChanges = source["hasChanges"];
	        this.noStoredSpec = source["noStoredSpec"];
	        this.lastSyncDate = source["lastSyncDate"];
	        this.changes = this.convertValues(source["changes"], OpenAPISyncEndpointChange);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class OpenAPISyncOptions {
	    sourceUrl: string;
	    content: string;
	    groupBy: string;
	    preserveValues: boolean;
	    removeDeleted: boolean;
	    endpointDecisions?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.content = source["content"];
	        this.groupBy = source["groupBy"];
	        this.preserveValues = source["preserveValues"];
	        this.removeDeleted = source["removeDeleted"];
	        this.endpointDecisions = source["endpointDecisions"];
	    }
	}
	export class OpenAPISyncResult {
	    sourceUrl: string;
	    groupBy: string;
	    specHash: string;
	    title: string;
	    version: string;
	    endpointCount: number;
	    added: number;
	    updated: number;
	    removed: number;
	    unchanged: number;
	    hasChanges: boolean;
	    lastSyncDate?: string;
	    changes: OpenAPISyncEndpointChange[];
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.groupBy = source["groupBy"];
	        this.specHash = source["specHash"];
	        this.title = source["title"];
	        this.version = source["version"];
	        this.endpointCount = source["endpointCount"];
	        this.added = source["added"];
	        this.updated = source["updated"];
	        this.removed = source["removed"];
	        this.unchanged = source["unchanged"];
	        this.hasChanges = source["hasChanges"];
	        this.lastSyncDate = source["lastSyncDate"];
	        this.changes = this.convertValues(source["changes"], OpenAPISyncEndpointChange);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OpenAPISyncSpecDiffLine {
	    kind: string;
	    oldNumber?: number;
	    newNumber?: number;
	    oldText?: string;
	    newText?: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncSpecDiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.oldNumber = source["oldNumber"];
	        this.newNumber = source["newNumber"];
	        this.oldText = source["oldText"];
	        this.newText = source["newText"];
	    }
	}
	export class OpenAPISyncSpecDiffResult {
	    sourceUrl: string;
	    storedContent: string;
	    newContent: string;
	    noStoredSpec: boolean;
	    storedSpecHash: string;
	    newSpecHash: string;
	    added: number;
	    updated: number;
	    removed: number;
	    unchanged: number;
	    changes: OpenAPISyncEndpointChange[];
	    lines: OpenAPISyncSpecDiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncSpecDiffResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.storedContent = source["storedContent"];
	        this.newContent = source["newContent"];
	        this.noStoredSpec = source["noStoredSpec"];
	        this.storedSpecHash = source["storedSpecHash"];
	        this.newSpecHash = source["newSpecHash"];
	        this.added = source["added"];
	        this.updated = source["updated"];
	        this.removed = source["removed"];
	        this.unchanged = source["unchanged"];
	        this.changes = this.convertValues(source["changes"], OpenAPISyncEndpointChange);
	        this.lines = this.convertValues(source["lines"], OpenAPISyncSpecDiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OpenAPISyncSpecViewResult {
	    sourceUrl: string;
	    content: string;
	    fromCache: boolean;
	    fetched: boolean;
	    noStoredSpec: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncSpecViewResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.content = source["content"];
	        this.fromCache = source["fromCache"];
	        this.fetched = source["fetched"];
	        this.noStoredSpec = source["noStoredSpec"];
	    }
	}
	export class OpenAPISyncUpdateCheckResult {
	    sourceUrl: string;
	    storedSpecHash: string;
	    remoteSpecHash: string;
	    hasUpdates: boolean;
	    checkedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenAPISyncUpdateCheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceUrl = source["sourceUrl"];
	        this.storedSpecHash = source["storedSpecHash"];
	        this.remoteSpecHash = source["remoteSpecHash"];
	        this.hasUpdates = source["hasUpdates"];
	        this.checkedAt = source["checkedAt"];
	    }
	}
	
	
	
	
	
	
	
	
	export class RequestPatch {
	    name?: string;
	    type?: string;
	    method?: string;
	    url?: string;
	    params?: KeyValue[];
	    pathParams?: KeyValue[];
	    headers?: KeyValue[];
	    body?: RequestBody;
	    protoPath?: string;
	    grpcMethodType?: string;
	    grpcMessages?: GrpcMessage[];
	    wsMessages?: WSMessage[];
	    auth?: AuthConfig;
	    vars?: RequestVars;
	    assertions?: Assertion[];
	    tests?: string;
	    preScript?: string;
	    postScript?: string;
	    docs?: string;
	    settings?: RequestSettings;
	    tags?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RequestPatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.params = this.convertValues(source["params"], KeyValue);
	        this.pathParams = this.convertValues(source["pathParams"], KeyValue);
	        this.headers = this.convertValues(source["headers"], KeyValue);
	        this.body = this.convertValues(source["body"], RequestBody);
	        this.protoPath = source["protoPath"];
	        this.grpcMethodType = source["grpcMethodType"];
	        this.grpcMessages = this.convertValues(source["grpcMessages"], GrpcMessage);
	        this.wsMessages = this.convertValues(source["wsMessages"], WSMessage);
	        this.auth = this.convertValues(source["auth"], AuthConfig);
	        this.vars = this.convertValues(source["vars"], RequestVars);
	        this.assertions = this.convertValues(source["assertions"], Assertion);
	        this.tests = source["tests"];
	        this.preScript = source["preScript"];
	        this.postScript = source["postScript"];
	        this.docs = source["docs"];
	        this.settings = this.convertValues(source["settings"], RequestSettings);
	        this.tags = source["tags"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	
	
	
	
	export class RunnerOptions {
	    selectedItemIds: string[];
	    delayMs?: number;
	    bailOnFailure?: boolean;
	    iterations?: number;
	    dataFile?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunnerOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.selectedItemIds = source["selectedItemIds"];
	        this.delayMs = source["delayMs"];
	        this.bailOnFailure = source["bailOnFailure"];
	        this.iterations = source["iterations"];
	        this.dataFile = source["dataFile"];
	    }
	}
	
	
	
	export class TerminalSession {
	    id: string;
	    cwd: string;
	    pid: number;
	    output: string;
	    exited: boolean;
	    exitCode: number;
	    signal: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TerminalSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.cwd = source["cwd"];
	        this.pid = source["pid"];
	        this.output = source["output"];
	        this.exited = source["exited"];
	        this.exitCode = source["exitCode"];
	        this.signal = source["signal"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	
	
	
	
	

}


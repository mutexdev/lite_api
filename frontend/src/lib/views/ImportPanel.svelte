<script lang="ts">
  // US-036 — the Import panel. The last surface in this story.
  //
  // Import does NOT decompose the way Preferences and DevTools did: the whole
  // branch is one <section class="panel import-panel"> whose only internal
  // heading sits inside an {:else} arm of a conditional over importSourceMode.
  // So this is a single deliberate pass.
  //
  // Every prop type below was READ from its declaration in App.svelte, not
  // inferred from the name. Two earlier attempts failed by guessing: the first
  // missed prop names, the second got all 35 names right and all 37 signatures
  // wrong. importReadyRows, for instance, reads like a count and is a
  // CollectionImportPreviewRow[].
  import type { main } from '../../../wailsjs/go/models'

  // Mirrors App.svelte's local types; neither is exported from an importable
  // module (checked — unlike WorkbenchCommandID, which is).
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

  // Bindable: every form field on this panel writes back to App.svelte.
  export let importSourceMode: ImportSourceMode
  export let importTranslatePostmanScripts: boolean
  export let importURL: string
  export let importContent: string
  export let importPasteName: string
  export let importDestinationWorkspaceID: string
  export let importDestinationRoot: string
  export let importExpanded: Record<string, boolean>
  export let gitCloneURL: string
  export let gitCloneRoot: string
  export let importApplyButton: HTMLButtonElement | null = null
  export let importPickerButton: HTMLButtonElement | null = null

  export let state: main.AppState
  export let busy: string
  export let importStatus: string
  export let importReadyRows: main.CollectionImportPreviewRow[]
  export let importPreview: main.CollectionImportPreview | undefined
  export let importSources: main.CollectionImportSource[]
  export let importApplyResult: main.CollectionImportApplyResult | undefined
  export let importApplyInFlight: boolean
  export let gitCloneInProgress: boolean
  export let importDecisionFor: (row: main.CollectionImportPreviewRow) => ImportDecision
  export let selectImportSourceMode: (mode: ImportSourceMode) => void
  export let chooseImportFiles: () => void
  export let chooseImportFolder: () => void
  export let previewImportSources: (sources: main.CollectionImportSource[], focusFirst?: boolean, resetDecisions?: boolean) => void
  export let previewURLImport: () => void
  export let previewPasteImport: () => void
  export let requestPlannedImport: () => void
  export let updateImportDecision: (candidateID: string, update: Partial<ImportDecision>) => void
  export let updateImportOverride: (row: main.CollectionImportPreviewRow, kindOverride: string) => void
  export let toggleImportChild: (row: main.CollectionImportPreviewRow, kind: 'folders' | 'environments' | 'requests', id: string, selected: boolean) => void
  export let exportCollection: () => void
  export let scanGitCollections: () => void
  export let cloneGitRepository: () => void
  export let checkGitVersion: () => void
</script>

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
              <footer class="import-action-footer"><span>{importReadyRows.length} valid source{importReadyRows.length === 1 ? '' : 's'} selected</span>
                <label class="checkbox-line" title="Off by default: pm.* runs natively. Enable only for collections whose scripts were already rewritten against the bru API.">
                  <input type="checkbox" data-testid="import-translate-postman" bind:checked={importTranslatePostmanScripts} />
                  Rewrite pm.* to bru.*
                </label><button class="primary" bind:this={importApplyButton} data-testid="import-apply-selected" type="button" on:click={requestPlannedImport} disabled={busy !== '' || importApplyInFlight || importReadyRows.length === 0}>Apply selected imports</button></footer>
            {/if}
            {#if importApplyResult}<div class="import-results" aria-live="polite"><strong>{importStatus}</strong>{#each [...(importApplyResult.applied ?? []), ...(importApplyResult.skipped ?? []), ...(importApplyResult.errors ?? [])] as row, index (index)}<p>{row.sourceName}: {row.error || (importApplyResult.skipped?.some((entry) => entry.candidateId === row.candidateId) ? 'Skipped' : 'Imported')}</p>{/each}</div>{/if}
            <p class="import-live" aria-live="polite">{importStatus}</p>
          </div>
        </section>

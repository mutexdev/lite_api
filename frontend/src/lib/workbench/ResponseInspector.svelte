<script module lang="ts">
  const responseScrollCache = new Map<string, { top: number; left: number }>()
  const responseScrollLimit = 100
</script>

<script lang="ts">
  import type { types } from '../../../wailsjs/go/models'
  import { automaticPreviewLimit, base64ByteLength, compareHeaders, compareJsonStructure, contentDispositionFilename, contentType, embeddedPreviewLimit, findMatches, formatResponseBody, fullRenderLimit, lineDiff, normalizeResponseView, previewKind, responseTextForView, sliceBase64Bytes, sliceUtf8, utf8ByteLength } from './response'
  import { resolveLiveSessionEvents, type LiveSessionLog } from '../liveSessionEvents'
  import { parseTLSFailure } from '../tlsErrors'
  import { JSON_TREE_BUDGET, JSON_TREE_MAX_ENTRIES, boundedJsonTree } from './jsonTree'

  // US-028 — runes. None of these are bound by the parent.
  type Props = {
    request: types.RequestItem
    selectedTab?: string
    selectedView?: string
    timeline?: types.TimelineItem[]
    scriptLogs?: Array<{ level: string; message: string }>
    // US-058. The document is built in Go so the CSP and escaping are covered
    // by Go tests; this component supplies only the sandbox attribute.
    visualizerDocument?: string
    visualizerSandbox?: string
    // US-021/US-022. The accumulated live-session log, pushed event by event.
    // The response body now carries only a trailing window, so this is what
    // makes a long session's full history visible while it is open.
    liveLog?: LiveSessionLog
    onViewChange: (view: string) => void
    onCopy: (value: string) => Promise<boolean>
    onDownloadBody: () => void | Promise<void>
    onExportTimeline: () => void | Promise<void>
    // US-059. A certificate failure is the one send error with a remedy inside
    // the app, so the pane offers it rather than describing where to find it.
    onDisableTLSVerification?: () => void | Promise<void>
    onOpenRequestPreferences?: () => void
  }

  let {
    request,
    selectedTab = 'response',
    selectedView = 'pretty',
    timeline = [],
    scriptLogs = [],
    visualizerDocument = '',
    visualizerSandbox = 'allow-scripts',
    liveLog = undefined,
    onViewChange,
    onCopy,
    onDownloadBody,
    onExportTimeline,
    onDisableTLSVerification = undefined,
    onOpenRequestPreferences = undefined
  }: Props = $props()

  // US-059. Split so the explanation reads as a sentence and the Go error sits
  // underneath it, where it is available for a bug report without being the
  // first thing the eye meets.
  const tlsFailure = $derived(parseTLSFailure(request.response?.error))

  // US-028 — every one of these is written from a handler or an effect and read
  // by the template, so all of them must be $state. As plain lets the search
  // box, the view toggles, the comparison selector and the scroll restoration
  // would each silently stop responding while the component kept rendering.
  let renderFull = $state(false)
  let visibleLimit = $state(automaticPreviewLimit)
  let jsonTreeMode = $state(false)
  let search = $state('')
  let matchIndex = $state(0)
  let timelineSearch = $state('')
  let timelineFilter = $state('all')
  let compareId = $state('current')
  let expandedTimelineID = $state('')
  let responseIdentity = $state('')
  let previewViewIdentity = $state('')
  let bodyElement = $state<HTMLElement | null>(null)
  let copyStatus = $state('')
  let showUnchanged = $state(false)
  let headerSearch = $state('')
  let responseScrollKey = $state('')
  let restoredScrollKey = $state('')

  const response = $derived(request.response)
  const headers = $derived(response?.headers ?? {})
  const rawBody = $derived(response?.body ?? '')
  const rawBase64 = $derived(response?.bodyBase64 ?? '')
  const retainedBase64Bytes = $derived(base64ByteLength(rawBase64))
  const bytes = $derived(
    (response?.size ?? 0) > 0
      ? response?.size ?? 0
      : rawBase64
        ? retainedBase64Bytes
        : utf8ByteLength(rawBody)
  )
  const boundedBody = $derived(sliceUtf8(rawBody, fullRenderLimit))
  const boundedBase64 = $derived(sliceBase64Bytes(rawBase64, automaticPreviewLimit))
  const pretty = $derived(bytes <= fullRenderLimit ? formatResponseBody(rawBody, headers) : formatResponseBody(boundedBody, headers))
  const renderableFull = $derived(bytes <= fullRenderLimit && (!rawBase64 || retainedBase64Bytes >= bytes))
  const visibleBase64 = $derived(renderFull && renderableFull ? rawBase64 : boundedBase64)
  const display = $derived(responseTextForView(boundedBody, visibleBase64, selectedView, pretty))
  const isLarge = $derived(bytes > automaticPreviewLimit)
  const byteEncodedView = $derived(selectedView === 'base64' || selectedView === 'hex')
  const safeDisplay = $derived(byteEncodedView ? display : sliceUtf8(display, renderFull && renderableFull ? fullRenderLimit : Math.min(visibleLimit, fullRenderLimit)))
  // `size` is a byte count while JavaScript strings are UTF-16. Truncation is
  // therefore determined from actual preview/source clipping, never by comparing
  // those incomparable lengths. `safeDisplay` and `display` are valid UTF-8
  // prefixes, so this also remains correct for multi-byte text such as “héllo”.
  // Any response within the automatic preview byte budget is fully available;
  // this avoids falsely flagging UTF-8 text whose byte count exceeds its JS
  // UTF-16 length. Larger responses remain governed by the existing bounded
  // preview/full-render safeguards.
  const bodyTruncated = $derived(bytes > automaticPreviewLimit && (!renderFull || !renderableFull))
  const matches = $derived(findMatches(safeDisplay, search))
  // An EFFECT, not a derivation: it writes matchIndex. A search that narrows
  // the result list must pull the cursor back inside it, or the highlight
  // points past the end and Next wraps to nothing.
  $effect(() => {
    if (matchIndex >= matches.length) matchIndex = Math.max(0, matches.length - 1)
  })
  const kind = $derived(previewKind(headers))
  const canIncrementTextPreview = $derived((selectedView === 'pretty' || selectedView === 'raw') && (kind === 'text' || kind === 'json' || kind === 'xml'))
  const canRenderFull = $derived(isLarge && !renderFull && (canIncrementTextPreview || byteEncodedView))
  const binaryFilename = $derived(contentDispositionFilename(headers))
  const headerEntries = $derived(response?.headerEntries)
  const headerRows = $derived(headerEntries?.length ? headerEntries.map((entry) => [entry.name, entry.value] as [string, string]) : Object.entries(headers))
  const filteredHeaders = $derived(headerRows.filter(([name, value]) => `${name} ${value}`.toLowerCase().includes(headerSearch.trim().toLowerCase())))
  // `liveLog` is named inside each derivation so both really do track it; see
  // the note on websocketEvents below about reactive tracking and function
  // arguments. US-028 kept this shape: $derived tracks what it READS, so a
  // value reached only through a closure is still not a dependency.
  const wsEvents = $derived(resolveLiveSessionEvents(liveLog, websocketEvents(response, bytes)))
  const grpcEventsParsed = $derived(resolveLiveSessionEvents(liveLog, grpcEvents(response, bytes)))
  const jsonValue = $derived(parsedJson(response, bytes))
  const jsonTree = $derived(boundedJsonTree(jsonValue))
  const canEmbed = $derived(bytes <= embeddedPreviewLimit)
  const compareOptions = $derived([{ id: 'current', name: 'Current response', body: display, status: response?.status ?? 0, duration: response?.durationMs ?? 0, headers }, ...(request.examples ?? []).map((example) => ({ id: example.id || example.name, name: example.name, body: example.response?.body ?? '', status: example.response?.status ?? 0, duration: example.response?.durationMs ?? 0, headers: Object.fromEntries((example.response?.headers ?? []).map((header) => [header.name, header.value])) }))])
  const compareTarget = $derived(compareOptions.find((option) => option.id === compareId) ?? compareOptions[0])
  const diff = $derived(compareId === 'current' ? { rows: [], truncated: false } : lineDiff(display.slice(0, fullRenderLimit), (compareTarget?.body ?? '').slice(0, fullRenderLimit)))
  const headerComparison = $derived(compareId === 'current' ? [] : compareHeaders(headers, compareTarget?.headers ?? {}))
  const jsonComparison = $derived(compareId === 'current' ? null : compareJsonStructure(rawBody, compareTarget?.body ?? ''))
  const timelineFilterCounts = $derived(Object.fromEntries(['all', 'pre', 'post', 'oauth', 'main'].map((filter) => [filter, timeline.filter((entry) => timelineMatchesFilter(entry, filter)).length])))
  const filteredTimeline = $derived(timeline.filter((entry) => timelineMatchesFilter(entry, timelineFilter) && (!timelineSearch.trim() || timelineSearchText(entry).includes(timelineSearch.trim().toLowerCase()))))
  // Resets the view when a NEW response arrives. Silent if lost: the next
  // response would inherit the previous one's full-render decision, search term
  // and comparison target.
  $effect(() => {
    const identity = `${request.id}:${response?.sentAt ?? ''}:${response?.size ?? 0}`
    if (identity !== responseIdentity) {
      responseIdentity = identity
      renderFull = false
      visibleLimit = automaticPreviewLimit
      search = ''
      matchIndex = 0
      compareId = 'current'
      jsonTreeMode = false
    }
  })
  const currentScrollKey = $derived(`${request.id}:${response?.sentAt ?? ''}:${selectedView}`)
  // A response view is always opened at its automatic bounded preview. This
  // prevents a Base64/Hex view from inheriting a full-render decision made in a
  // different representation of the same response.
  $effect(() => {
    const identity = `${responseIdentity}:${selectedView}`
    if (identity !== previewViewIdentity) {
      previewViewIdentity = identity
      renderFull = false
      visibleLimit = automaticPreviewLimit
    }
  })
  $effect(() => {
    if (responseScrollKey !== currentScrollKey) {
      rememberResponseScroll()
      responseScrollKey = currentScrollKey
      restoredScrollKey = ''
    }
  })
  $effect(() => {
    if (bodyElement && restoredScrollKey !== responseScrollKey) {
      restoredScrollKey = responseScrollKey
      requestAnimationFrame(() => restoreResponseScroll(responseScrollKey))
    }
  })

  function nextMatch(direction: number) {
    if (matches.length === 0) return
    matchIndex = (matchIndex + direction + matches.length) % matches.length
    requestAnimationFrame(() => bodyElement?.querySelector<HTMLElement>('mark.current-match')?.scrollIntoView({ block: 'center', behavior: 'smooth' }))
  }

  async function copyResponse(value: string) {
    copyStatus = await onCopy(value) ? 'Copied' : 'Clipboard unavailable'
  }

  function searchKeydown(event: KeyboardEvent) {
    if (event.key !== 'Enter') return
    event.preventDefault()
    nextMatch(event.shiftKey ? -1 : 1)
  }

  function selectResponseView(event: Event) {
    const next = normalizeResponseView((event.currentTarget as HTMLSelectElement).value)
    // Update local prop first so a native WebView select pop-up immediately
    // repaints even before the parent runs its state propagation.
    selectedView = next
    onViewChange(next)
  }

  function markedParts(value: string) {
    if (!search.trim()) return [{ text: value, match: false, index: -1 }]
    const parts: Array<{ text: string; match: boolean; index: number }> = []
    let cursor = 0
    for (const index of matches) {
      if (index > cursor) parts.push({ text: value.slice(cursor, index), match: false, index: -1 })
      parts.push({ text: value.slice(index, index + search.trim().length), match: true, index: parts.filter((part) => part.match).length })
      cursor = index + search.trim().length
    }
    if (cursor < value.length) parts.push({ text: value.slice(cursor), match: false, index: -1 })
    return parts
  }

  function htmlPreview(body: string) {
    return `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline';"><base href="about:blank">${body}`
  }

  // `response` and `size` are taken as parameters (not closed over) so the
  // derivations calling these functions declare real reactive dependencies.
  // Closing over them made the statements depend only on the immutable function
  // bindings, so they ran once at mount and went stale on every subsequent
  // response (svelte/no-immutable-reactive-statements).
  //
  // US-028 did not change this. $derived tracks values READ during evaluation,
  // and a value reached through a closure rather than an argument is read
  // inside the function body — which is tracked — but the original bug was that
  // the ARGUMENT list was empty, so the statement had no dependency to
  // invalidate on. Passing them explicitly is still what makes it correct.
  function websocketEvents(response: types.Response | undefined, size: number) {
    if (size > fullRenderLimit || response?.previewMode !== 'websocket' || !response.body) return [] as Array<Record<string, string>>
    try { return JSON.parse(response.body) as Array<Record<string, string>> } catch { return [] }
  }

  function grpcEvents(response: types.Response | undefined, size: number) {
    if (size > fullRenderLimit || response?.previewMode !== 'grpc-stream' || !response.body) return [] as Array<Record<string, string>>
    try { return JSON.parse(response.body) as Array<Record<string, string>> } catch { return [] }
  }

  function parsedJson(response: types.Response | undefined, size: number) {
    if (size > fullRenderLimit) return null
    try { return JSON.parse(response?.body ?? '') as unknown } catch { return null }
  }


  function timelineMatchesFilter(entry: types.TimelineItem, filter: string) {
    return filter === 'all' || `${entry.phase || ''} ${entry.kind || ''} ${entry.source || ''}`.toLowerCase().includes(filter)
  }

  function timelineSearchText(entry: types.TimelineItem) {
    const rows = (items: types.KeyValue[] | undefined) => (items ?? []).map((item) => `${item.name} ${item.value}`).join(' ')
    return `${entry.phase} ${entry.kind} ${entry.source} ${entry.sourceFile} ${entry.method} ${entry.url} ${entry.message} ${entry.status} ${entry.statusText} ${entry.error} ${entry.payload} ${rows(entry.metadata)} ${rows(entry.trailers)}`.toLowerCase()
  }

  function timelinePhase(entry: types.TimelineItem) {
    const value = `${entry.phase ?? ''} ${entry.kind ?? ''} ${entry.source ?? ''} ${entry.message ?? ''}`.toLowerCase()
    for (const phase of ['dns', 'connect', 'tls', 'upload', 'wait', 'download', 'redirect', 'oauth', 'script', 'nested']) {
      if (value.includes(phase)) return phase === 'script' ? 'scripts' : phase
    }
    return entry.phase || 'phase unavailable'
  }

  function rememberResponseScroll() {
    if (!bodyElement || !responseScrollKey) return
    responseScrollCache.delete(responseScrollKey)
    responseScrollCache.set(responseScrollKey, { top: bodyElement.scrollTop, left: bodyElement.scrollLeft })
    if (responseScrollCache.size > responseScrollLimit) responseScrollCache.delete(responseScrollCache.keys().next().value as string)
  }

  function restoreResponseScroll(key: string) {
    const saved = responseScrollCache.get(key)
    if (!bodyElement || !saved || key !== responseScrollKey) return
    bodyElement.scrollTop = saved.top
    bodyElement.scrollLeft = saved.left
  }
</script>

<div class="response-inspector">
  {#if selectedTab === 'response'}
    {#if response?.error}
      {#if tlsFailure}
        <div class="response-warning response-tls-warning" role="alert">
          <p>{tlsFailure.summary}</p>
          <div class="response-tls-actions">
            {#if onDisableTLSVerification}
              <button type="button" data-testid="response-disable-tls" onclick={() => void onDisableTLSVerification?.()}>
                Turn off Verify TLS for this request and resend
              </button>
            {/if}
            {#if onOpenRequestPreferences && tlsFailure.suggestsCustomCA}
              <button type="button" data-testid="response-open-tls-preferences" onclick={() => onOpenRequestPreferences?.()}>
                Open request preferences
              </button>
            {/if}
          </div>
          {#if tlsFailure.detail}<pre class="response-tls-detail">{tlsFailure.detail}</pre>{/if}
        </div>
      {:else}
        <div class="response-warning" role="alert">{response.error}</div>
      {/if}
    {:else if response?.cancelled}<div class="response-warning" role="status">Request cancelled.</div>{/if}
    {#if !response}
      <section class="response-empty-state" aria-live="polite">
        <strong>Ready for a response</strong>
        <p>Send the request to inspect its status, headers, body, timeline, and downloadable payload.</p>
      </section>
    {:else}
    <div class="response-inspector-toolbar">
      <select aria-label="Response view" data-testid="response-view-select" value={selectedView} oninput={selectResponseView} onchange={selectResponseView}>
        <option value="pretty">Pretty</option><option value="raw">Raw</option><option value="base64">Base64</option><option value="hex">Hex</option>
      </select>
      <span aria-live="polite">{bytes.toLocaleString()} bytes{bodyTruncated ? ' · preview truncated' : ''}</span>
      <button type="button" aria-label="Copy visible response preview" onclick={() => void copyResponse(safeDisplay)}>Copy preview</button><span aria-live="polite">{copyStatus}</span>
      <button type="button" aria-label="Download exact response body" onclick={() => void onDownloadBody()}>Download</button>
      {#if canRenderFull}
        <button type="button" onclick={() => (renderFull = true)} disabled={!renderableFull} title={!renderableFull ? 'This response exceeds the 1 MB safe render limit; download it instead.' : undefined}>Render full</button>
      {/if}
      {#if canIncrementTextPreview && bodyTruncated && !renderFull && visibleLimit < fullRenderLimit}<button type="button" onclick={() => (visibleLimit = Math.min(fullRenderLimit, visibleLimit + automaticPreviewLimit))}>Load more</button>{/if}
      {#if selectedView === 'pretty' && kind === 'json' && jsonValue}<button type="button" aria-pressed={jsonTreeMode} onclick={() => (jsonTreeMode = !jsonTreeMode)}>JSON tree</button>{/if}
    </div>
    {#if bytes > fullRenderLimit}
      <div class="response-warning">Large response: automatic rendering is bounded to {Math.round(automaticPreviewLimit / 1024)} KB. Download for the full payload.</div>
    {/if}
    <div class="response-search">
      <input aria-label="Search response body" bind:value={search} onkeydown={searchKeydown} placeholder="Search response" />
      <span aria-live="polite">{matches.length === 0 ? 'No matches' : `${matchIndex + 1} of ${matches.length}`}</span>
      <button type="button" aria-label="Previous response match" onclick={() => nextMatch(-1)}>Previous</button>
      <button type="button" aria-label="Next response match" onclick={() => nextMatch(1)}>Next</button>
    </div>
    {#if selectedView === 'pretty' && kind === 'image' && canEmbed && response?.bodyBase64}
      <img class="response-media" alt="Response preview" src={`data:${contentType(headers)};base64,${response.bodyBase64}`} />
    {:else if selectedView === 'pretty' && kind === 'html' && canEmbed}
      <iframe class="response-html" title="Sandboxed HTML response preview" sandbox="" srcdoc={htmlPreview(response?.body ?? '')}></iframe>
    {:else if selectedView === 'pretty' && kind === 'pdf' && canEmbed && response?.bodyBase64}
      <iframe class="response-pdf" title="PDF response preview" sandbox="allow-same-origin" src={`data:application/pdf;base64,${response.bodyBase64}`}></iframe>
    {:else if selectedView === 'pretty' && kind === 'binary'}
      <section class="binary-response-card" aria-label="Binary response">
        <strong>Binary response</strong>
        <span>{contentType(headers) || 'application/octet-stream'} · {bytes.toLocaleString()} exact bytes</span>
        {#if binaryFilename}<span>Filename: {binaryFilename}</span>{/if}
        <p>Preview is intentionally unavailable to avoid treating binary data as text. Use Hex for a bounded inspection or Download for the exact payload.</p>
      </section>
    {:else if selectedView === 'pretty' && (kind === 'image' || kind === 'html' || kind === 'pdf') && !canEmbed}
      <div class="response-warning">This {kind.toUpperCase()} response is too large for an embedded preview. Download it to inspect the full file.</div>
    {:else if selectedView === 'pretty' && wsEvents.length > 0}
      <div class="ws-event-log" data-testid="ws-event-log">{#each wsEvents as event, index (index)}<article class="ws-event-row" data-testid="ws-event-row"><strong>{event.direction || 'system'} · {event.type || 'message'} {event.name || ''} {event.at || ''}</strong><code data-testid={`ws-event-payload-${index}`}>{event.error || event.data || event.dataHex || event.dataBase64 || ''}</code></article>{/each}</div>
    {:else if selectedView === 'pretty' && grpcEventsParsed.length > 0}
      <div class="ws-event-log" data-testid="grpc-stream-event-log">{#each grpcEventsParsed as event, index (index)}<article class="ws-event-row" data-testid="grpc-stream-event-row"><strong>{event.direction || 'system'} · {event.type || 'message'} {event.name || ''} {event.at || ''}</strong><code data-testid={`grpc-stream-event-payload-${index}`}>{event.error || event.data || ''}</code></article>{/each}</div>
    {:else if selectedView === 'pretty' && kind === 'json' && jsonTreeMode && jsonValue}
      <div class="json-tree" aria-label="Bounded JSON tree">
        {#each jsonTree.entries as entry (entry.name)}
          <details><summary>{entry.name} <small>{Array.isArray(entry.value) ? `Array (${entry.value.length})` : typeof entry.value}</small></summary><pre>{entry.text}</pre></details>
        {/each}
        {#if jsonTree.entries.length === 0}<small>This response has no fields to expand.</small>{/if}
        {#if jsonTree.truncated}<small>Tree render is bounded to {JSON_TREE_MAX_ENTRIES} root items and {Math.round(JSON_TREE_BUDGET / 1024)} KB.</small>{/if}
      </div>
    {:else}
      <pre class="response-body" bind:this={bodyElement} data-match-index={matches[matchIndex] ?? -1}>{#each markedParts(safeDisplay) as part, index (index)}<span>{#if part.match}<mark class:current-match={part.index === matchIndex}>{part.text}</mark>{:else}{part.text}{/if}</span>{/each}</pre>
    {/if}
    {#if bodyTruncated}<small>Preview is truncated. {renderFull && bytes > fullRenderLimit ? 'Download the body to inspect all content.' : ''}</small>{/if}
    {#if (request.examples ?? []).length > 0}
      <section class="response-compare" aria-label="Response comparison">
        <label>Compare with <select aria-label="Compare response with" bind:value={compareId}>{#each compareOptions as option (option.id)}<option value={option.id}>{option.name}</option>{/each}</select></label>
        {#if compareId !== 'current'}
          <div class="compare-summary"><span>Status: {response?.status ?? '-'} → {compareTarget?.status ?? '-'} ({response?.status === compareTarget?.status ? 'unchanged' : 'changed'})</span><span>Timing: {response?.durationMs ?? 0} ms → {compareTarget?.duration ? `${compareTarget.duration} ms` : 'unavailable'} ({compareTarget?.duration ? response?.durationMs === compareTarget.duration ? 'unchanged' : 'changed' : 'unavailable'})</span></div>
          <section class="compare-section" aria-label="Header changes"><h4>Headers</h4><table><thead><tr><th>Header</th><th>Current</th><th>Selected</th><th>Change</th></tr></thead><tbody>{#each headerComparison.filter((row) => showUnchanged || row.change !== 'unchanged') as row, index (index)}<tr><td>{row.name}</td><td>{row.current}</td><td>{row.selected}</td><td><span class={`compare-badge ${row.change}`}>{row.change}</span></td></tr>{/each}</tbody></table></section>
          <section class="compare-section" aria-label="JSON structure changes"><h4>JSON structure</h4>{#if jsonComparison?.available}<p>Root type: {jsonComparison.root}</p><p>Keys added: {jsonComparison.added.join(', ') || 'none'} · removed: {jsonComparison.removed.join(', ') || 'none'} · changed type: {jsonComparison.changed.join(', ') || 'none'}</p>{:else}<p>Structure comparison unavailable: {jsonComparison?.reason}</p>{/if}</section>
          <label class="compare-toggle"><input type="checkbox" bind:checked={showUnchanged} /> Show unchanged body/header rows</label>
          <div class="response-diff">{#each diff.rows.filter((row) => showUnchanged || row.changed) as row, index (index)}<div class:changed={row.changed}><code>{row.left}</code><code>{row.right}</code></div>{/each}</div>
          {#if diff.truncated}<small>Diff limited to 2,400 lines.</small>{/if}
        {/if}
      </section>
    {/if}
    {/if}
  {:else if selectedTab === 'headers'}
    <div class="response-search"><input aria-label="Search headers" bind:value={headerSearch} placeholder="Search headers" /><span aria-live="polite">{filteredHeaders.length === 0 ? 'No matching headers' : `${filteredHeaders.length} of ${headerRows.length}`}</span><button type="button" onclick={() => void copyResponse(filteredHeaders.map(([name, value]) => `${name}: ${value}`).join('\n'))}>Copy headers</button><span aria-live="polite">{copyStatus}</span></div>
    {#if filteredHeaders.length === 0}<div class="empty-state">No headers match this search.</div>{:else}<table><tbody>{#each filteredHeaders as [name, value] (name)}<tr><td>{name}</td><td>{value}</td></tr>{/each}</tbody></table>{/if}
  {:else if selectedTab === 'metadata' || selectedTab === 'trailers'}
    {@const rows = selectedTab === 'metadata' ? response?.metadata ?? [] : response?.trailers ?? []}
    <div class="response-search"><span>{rows.length} {selectedTab}</span><button type="button" onclick={() => void copyResponse(rows.map((row) => `${row.name}: ${row.value}`).join('\n'))} disabled={rows.length === 0}>Copy {selectedTab}</button><span aria-live="polite">{copyStatus}</span></div>
    {#if rows.length === 0}<div class="empty-state">No {selectedTab}</div>{:else}<table><tbody>{#each rows as row, index (index)}<tr><td>{row.name}</td><td>{row.value}</td></tr>{/each}</tbody></table>{/if}
  {:else if selectedTab === 'timeline'}
    <div class="timeline-tools"><input aria-label="Search timeline" bind:value={timelineSearch} placeholder="Search phase, kind, source, payload, metadata…" /><select aria-label="Timeline phase filter" bind:value={timelineFilter}><option value="all">All ({timelineFilterCounts.all})</option><option value="pre">Pre-request ({timelineFilterCounts.pre})</option><option value="post">Post-response ({timelineFilterCounts.post})</option><option value="oauth">OAuth ({timelineFilterCounts.oauth})</option><option value="main">Request ({timelineFilterCounts.main})</option></select><span aria-live="polite">{filteredTimeline.length} of {timeline.length}</span><button type="button" onclick={() => void copyResponse(JSON.stringify(filteredTimeline, null, 2))}>Copy</button><button type="button" onclick={() => void onExportTimeline()}>Export</button></div>
    {#if filteredTimeline.length === 0}
      <div class="empty-state">No timeline entries match this filter.</div>
    {:else}
      <div class="timeline">{#each filteredTimeline as entry, index (`${entry.id}:${entry.phase ?? ''}:${entry.at ?? ''}:${index}`)}<article class="timeline-entry"><button type="button" aria-expanded={expandedTimelineID === entry.id} onclick={() => expandedTimelineID = expandedTimelineID === entry.id ? '' : entry.id}><span>{entry.status || entry.statusText || '-'}</span><strong>{entry.method || entry.kind}</strong><span>{entry.url || entry.message}</span><small>{timelinePhase(entry)} · {entry.duration || 0} ms</small></button>{#if expandedTimelineID === entry.id}<div class="timeline-detail">{#if entry.sourceFile}<small>Source: {entry.sourceFile}</small>{/if}{#if entry.source}<small>Kind/source: {entry.kind} · {entry.source}</small>{/if}<code>{entry.error || entry.payload || entry.message}</code>{#if (entry.metadata?.length ?? 0) > 0}<table data-testid="timeline-grpc-metadata"><tbody>{#each entry.metadata ?? [] as row, index (index)}<tr><td>{row.name}</td><td>{row.value}</td></tr>{/each}</tbody></table>{/if}{#if (entry.trailers?.length ?? 0) > 0}<table data-testid="timeline-grpc-trailers"><tbody>{#each entry.trailers ?? [] as row, index (index)}<tr><td>{row.name}</td><td>{row.value}</td></tr>{/each}</tbody></table>{/if}</div>{/if}</article>{/each}</div>
    {/if}
  {:else if selectedTab === 'visualizer'}
    {#if !visualizerDocument}
      <div class="empty-state">No visualizer. Call pm.visualizer.set(template, data) in a script.</div>
    {:else}
      <!--
        sandbox WITHOUT allow-same-origin is the containment. Adding it would
        put this frame in the app's origin, where it could read localStorage
        and script the parent — and the attribute would still look like a
        sandbox in the markup. The CSP inside the document denies the network.
      -->
      <iframe
        class="visualizer-frame"
        data-testid="response-visualizer"
        title="Response visualizer"
        sandbox={visualizerSandbox}
        srcdoc={visualizerDocument}
      ></iframe>
    {/if}
  {:else if selectedTab === 'console'}
    {#if scriptLogs.length === 0}<div class="empty-state">No console output</div>{:else}<div class="console-log-list">{#each scriptLogs as log, index (index)}<div class={`console-row ${log.level || 'log'}`}><span>{log.level || 'log'}</span><code>{log.message}</code></div>{/each}</div>{/if}
  {:else if selectedTab === 'tests'}
    <div class="results">{#each response?.assertions ?? [] as assertion, index (index)}<div class:passed={assertion.passed} class:failed={!assertion.passed}>{assertion.expression} {assertion.message}</div>{/each}{#each response?.testResults ?? [] as test, index (index)}<div class:passed={test.passed} class:failed={!test.passed}>{test.name} {test.message}</div>{/each}</div>
  {/if}
</div>

<style>
.response-inspector { min-width: 0; }
.response-empty-state { display:grid; place-content:center; min-height:220px; gap:7px; padding:24px; text-align:center; color:var(--muted); }
.response-empty-state strong { color:var(--text); font-size:14px; }
.response-empty-state p { max-width:360px; margin:0; line-height:1.5; }
  .response-inspector-toolbar, .response-search, .timeline-tools { display:flex; flex-wrap:wrap; align-items:center; gap:7px; padding:8px 10px; border-bottom:1px solid var(--border-subtle); }
  .response-inspector-toolbar span, .response-search span { color:var(--muted); font-size:11px; }
.response-body { max-height:clamp(390px, 52vh, 520px); overflow:auto; margin:0; padding:12px; white-space:pre-wrap; overflow-wrap:anywhere; }
  .response-warning { margin:10px; padding:8px 10px; border:1px solid var(--warning-border); border-radius:6px; background:var(--warning-bg-soft); color:var(--warning-strong); font-size:12px; }
.binary-response-card { display:grid; gap:7px; margin:10px; padding:12px; border:1px solid var(--border); border-radius:7px; background:var(--surface-raised, var(--surface-soft)); color:var(--text); }
  .binary-response-card span, .binary-response-card p { margin:0; color:var(--muted); font-size:12px; overflow-wrap:anywhere; }
.response-media, .response-html, .response-pdf { display:block; width:calc(100% - 20px); max-height:clamp(240px, 48vh, 440px); min-height:240px; margin:10px; border:1px solid var(--border); border-radius:6px; object-fit:contain; }
.response-media { background:var(--surface-raised, var(--surface-soft)); }
.response-html, .response-pdf { background:#fff; }
.ws-event-log { display:grid; gap:6px; padding:10px; max-height:clamp(360px, 54vh, 480px); overflow:auto; }
.ws-event-row { display:grid; gap:5px; padding:8px; border:1px solid var(--border); border-radius:6px; background:var(--surface-raised, var(--surface-soft)); }
  .ws-event-row code, .timeline-entry code { overflow-wrap:anywhere; white-space:pre-wrap; }
.response-compare { margin:10px; border:1px solid var(--border); border-radius:6px; padding:8px; background:var(--surface-raised, var(--surface-soft)); }
.response-diff { max-height:clamp(260px, 38vh, 440px); overflow:auto; font-size:11px; }
  .compare-summary { display:flex; flex-wrap:wrap; gap:8px; margin:8px 0; color:var(--muted); font-size:11px; }
  .compare-section { margin:8px 0; overflow:auto; }
  .compare-section h4 { margin:0 0 5px; font-size:12px; }
  .compare-section p { margin:4px 0; color:var(--muted); font-size:11px; }
  .compare-section table { min-width:520px; font-size:11px; }
  .compare-section td { overflow-wrap:anywhere; }
  .compare-badge { font-size:10px; font-weight:800; text-transform:uppercase; }
  .compare-badge.added { color:var(--accent-strong); } .compare-badge.removed { color:var(--danger); } .compare-badge.changed { color:var(--warning-strong); }
  .compare-toggle { display:flex; align-items:center; gap:6px; margin:8px 0; color:var(--muted); font-size:11px; }
  .response-diff > div { display:grid; grid-template-columns:minmax(0,1fr) minmax(0,1fr); gap:8px; padding:3px 5px; border-bottom:1px solid var(--border-subtle); }
  .response-diff > div.changed { background:var(--warning-bg-soft); }
  .response-diff code { overflow-wrap:anywhere; white-space:pre-wrap; }
  .timeline-entry > button { display:grid; grid-template-columns:60px 80px minmax(0,1fr) auto; width:100%; text-align:left; }
  .timeline-entry > code { display:block; padding:8px; }
.json-tree { max-height:clamp(360px, 54vh, 480px); overflow:auto; padding:10px; }
  .json-tree details { margin-bottom:5px; border:1px solid var(--border-subtle); border-radius:5px; padding:5px; }
  .json-tree summary { cursor:pointer; }
.json-tree pre { max-height:clamp(180px, 28vh, 300px); overflow:auto; white-space:pre-wrap; overflow-wrap:anywhere; }
@media (min-width:1200px) and (min-height:820px) {
  .response-body { max-height:clamp(520px, 68vh, 760px); }
  .response-media, .response-html, .response-pdf { max-height:clamp(320px, 64vh, 680px); }
  .ws-event-log, .json-tree { max-height:clamp(480px, 65vh, 720px); }
  .response-diff { max-height:clamp(360px, 50vh, 600px); }
  .json-tree pre { max-height:clamp(220px, 38vh, 400px); }
}
  @media (max-width: 720px) { .timeline-entry > button { grid-template-columns:54px minmax(0,1fr); } .timeline-entry > button small { grid-column:1 / -1; } }
  .response-tls-warning {
    display: grid;
    gap: 8px;
  }

  .response-tls-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .response-tls-detail {
    margin: 0;
    font-size: 0.78rem;
    opacity: 0.75;
    white-space: pre-wrap;
    word-break: break-word;
  }
</style>

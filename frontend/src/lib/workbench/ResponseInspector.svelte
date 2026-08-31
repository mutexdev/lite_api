<script module lang="ts">
  const responseScrollCache = new Map<string, { top: number; left: number }>()
  const responseScrollLimit = 100
</script>

<script lang="ts">
  import type { types } from '../../../wailsjs/go/models'
  import { automaticPreviewLimit, base64ByteLength, compareHeaders, compareJsonStructure, contentDispositionFilename, contentType, embeddedPreviewLimit, findMatches, formatResponseBody, fullRenderLimit, lineDiff, normalizeResponseView, previewKind, responseTextForView, sliceBase64Bytes, sliceUtf8, utf8ByteLength } from './response'
  import { resolveLiveSessionEvents, type LiveSessionLog } from '../liveSessionEvents'
  import { parseTLSFailure } from '../tlsErrors'
  import { boundedJsonTree, countJsonTreeMatches } from './jsonTree'
  import { highlightLanguage, highlightSegments } from './bodyHighlight'
  import { currentKeyBindingOS, formatKeyBinding, keyBindingComboFromEvent } from '../keybindings'
  import FindBar from '../ui/FindBar.svelte'
  import IconButton from '../ui/IconButton.svelte'
  import PaneToolbar from '../ui/PaneToolbar.svelte'
  import SegmentedControl from '../ui/SegmentedControl.svelte'
  import JsonTreeView from './JsonTreeView.svelte'
  import ResponseNotice from './ResponseNotice.svelte'

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
    /**
     * Non-empty while an export's native save dialog is open.
     *
     * Scoped to these two buttons on purpose. The parent used to mark the whole
     * app busy for the duration, which disabled Send, Save and Run everywhere
     * until the file picker was dismissed.
     */
    exportBusy?: string
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
    exportBusy = '',
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
  /**
   * Whether the find bar is showing.
   *
   * It used to be a permanent second toolbar row, so every response opened with
   * two bars of chrome above it and the body pushed down — for a search almost
   * nobody had asked for yet. Now it is behind the toolbar's search button, the
   * same way the editor's is.
   */
  let searchOpen = $state(false)
  let searchBar = $state<ReturnType<typeof FindBar> | null>(null)
  // The other two find bars are held for the same reason the body's is: the
  // pane-find shortcut has to reach whichever one belongs to the sub-view the
  // user is actually looking at. See findInPane below.
  let headerBar = $state<ReturnType<typeof FindBar> | null>(null)
  let timelineBar = $state<ReturnType<typeof FindBar> | null>(null)
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
  /**
   * The byte budget currently being rendered.
   *
   * This is what the label was missing. "preview truncated" is a constant, so
   * pressing Load more visibly grew the body while the text above it said
   * exactly the same thing — leaving no way to tell whether the button had done
   * anything, or how much of the response was still unseen.
   *
   * Derived from the same expression `safeDisplay` slices with, so the number
   * shown and the number applied cannot drift apart. Byte-encoded views bound
   * on the source rather than the rendered text, hence the separate branch.
   */
  const renderedLimit = $derived(
    renderFull && renderableFull
      ? fullRenderLimit
      : byteEncodedView
        ? automaticPreviewLimit
        : Math.min(visibleLimit, fullRenderLimit)
  )
  const renderedBytes = $derived(Math.min(bytes, renderedLimit))
  /**
   * Why "Render full" is unavailable, in words.
   *
   * Lived only in a `title` attribute, which is invisible to touch, invisible
   * to keyboard users, and invisible to anyone who does not think to hover a
   * button that is already greyed out — precisely the people wondering why it
   * is greyed out. The limit is read from the constant rather than spelled "1
   * MB" in prose, so raising fullRenderLimit cannot leave the sentence lying.
   */
  const renderFullDisabledReason = $derived(
    renderableFull
      ? ''
      : `Too large to render: over the ${Math.round(fullRenderLimit / 1024 / 1024)} MB safe limit. Download it instead.`
  )
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
  /**
   * Built only while the tree is being looked at.
   *
   * It used to be built for every JSON response whether or not anyone opened
   * the toggle, which was affordable when it was a list of root keys and is not
   * now that it walks the document. The toggle is off by default and reset on
   * every new response, so the common case is that this never runs at all.
   */
  const jsonTree = $derived(jsonTreeMode ? boundedJsonTree(jsonValue) : { entries: [], truncated: false, nodes: 0 })
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
      searchOpen = false
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

  /**
   * Whether the TREE is what is on screen, not merely what the toggle says.
   *
   * Written once and used both by the template branch and by the find bar,
   * because the two disagreeing is a specific, silent bug: a websocket or gRPC
   * stream whose content type is JSON takes an earlier branch, so the tree
   * toggle could be lit, the stream log could be showing, and the find bar
   * could be counting fields of a tree nobody can see.
   */
  const showingTree = $derived(
    selectedView === 'pretty'
      && kind === 'json'
      && jsonTreeMode
      && Boolean(jsonValue)
      && wsEvents.length === 0
      && grpcEventsParsed.length === 0
  )
  const treeMatches = $derived(showingTree ? countJsonTreeMatches(jsonTree.entries, search) : 0)

  /**
   * The pane-find shortcut, and why it is not ⌘F.
   *
   * ⌘F is spoken for twice over: globally it is Search Sidebar, and inside a
   * CodeMirror editor it is that editor's own find — a conflict `shortcuts.ts`
   * resolved in the editor's favour and documented as deliberate
   * (`editorOwnedShortcutActions`). Binding a third meaning to it would undo
   * that decision from a component, which is the worst place to undo it from,
   * because nothing in `shortcuts.ts` would say so.
   *
   * ⇧⌘F is unclaimed by both: `keybindings.ts` uses ⇧⌘W, ⇧⌘S, ⇧⌘T and ⇧⌘P and
   * nothing else with Shift, and CodeMirror's `searchKeymap` binds Mod-f,
   * Mod-Alt-f, Mod-g, Mod-d and F3 — never Mod-Shift-f. It also reads correctly:
   * ⌘F is "find where the caret is", ⇧⌘F is the wider find, which is the same
   * relationship editors give the pair.
   *
   * It is matched through `keyBindingComboFromEvent` rather than by reading
   * `event.key` and the modifier flags directly, so the one place in the app
   * that decides what a keydown IS stays the one place.
   */
  const findInPaneBinding = { mac: 'command+bind+shift+bind+f', windows: 'ctrl+bind+shift+bind+f' }
  const findShortcutLabel = $derived(formatKeyBinding(findInPaneBinding[currentKeyBindingOS()], currentKeyBindingOS()))

  /**
   * Focuses the find bar of the sub-view being looked at.
   *
   * One shortcut for all three rather than one per tab. The tabs are alternate
   * views of a single response, so "find in this" should not become a different
   * key depending on which of them is open — that is the "different app in each
   * section" complaint restated as a keyboard.
   */
  function findInPane(event: KeyboardEvent) {
    if (keyBindingComboFromEvent(event) !== findInPaneBinding[currentKeyBindingOS()]) return
    // A modal owns the keyboard while it is open, exactly as shortcuts.ts
    // treats it. Focusing a bar behind a dialog would move focus out of the
    // dialog and leave the user typing into something they cannot see.
    if (typeof document !== 'undefined' && document.querySelector('[role="dialog"][aria-modal="true"]')) return
    if (selectedTab === 'headers') {
      event.preventDefault()
      headerBar?.focus()
      return
    }
    if (selectedTab === 'timeline') {
      event.preventDefault()
      timelineBar?.focus()
      return
    }
    if (selectedTab !== 'response' || !response) return
    event.preventDefault()
    // Opens if closed, focuses if already open. Deliberately NOT the toggle:
    // pressing the find shortcut twice must not be how a search gets discarded.
    searchOpen = true
    queueMicrotask(() => searchBar?.focus())
  }

  function nextMatch(direction: number) {
    if (matches.length === 0) return
    matchIndex = (matchIndex + direction + matches.length) % matches.length
    requestAnimationFrame(() => bodyElement?.querySelector<HTMLElement>('mark.current-match')?.scrollIntoView({ block: 'center', behavior: 'smooth' }))
  }

  async function copyResponse(value: string) {
    copyStatus = await onCopy(value) ? 'Copied' : 'Clipboard unavailable'
  }

  /**
   * Shows or hides the find bar.
   *
   * Closing CLEARS the query rather than merely hiding it. A hidden non-empty
   * search would leave the body full of highlighted matches with nothing on
   * screen explaining why — and the same rule is what the editor's find bar
   * follows, so the two behave alike.
   */
  function toggleSearch() {
    searchOpen = !searchOpen
    if (!searchOpen) {
      search = ''
      matchIndex = 0
      return
    }
    queueMicrotask(() => searchBar?.focus())
  }

  function selectResponseView(next: string) {
    // Update local prop first so the pane repaints immediately, before the
    // parent runs its state propagation. (Carried over from the `<select>` this
    // replaced, where a native WebView pop-up made the lag visible.)
    selectedView = normalizeResponseView(next)
    onViewChange(selectedView)
  }

  /**
   * The rendered body: syntax colours and search hits in one pass.
   *
   * Replaces `markedParts`, which split the text on search matches only. The
   * body had no colouring at all — a JSON payload was grey text — while the
   * request editor directly above it was fully highlighted by CodeMirror. Same
   * document, same screen, two completely different treatments; the single
   * loudest instance of the "different app in each section" complaint.
   *
   * Merged rather than layered because a hit routinely lands INSIDE a token:
   * searching `ate` in `"created_at"` has to keep the key colour and gain the
   * highlight. See bodyHighlight.ts.
   *
   * Only the Pretty and Raw views are painted. Base64 and Hex are not the
   * document — colouring them by JSON rules would invent structure that the
   * bytes on screen do not have.
   */
  const bodyLanguage = $derived(
    byteEncodedView ? 'plain' as const : highlightLanguage(contentType(headers), safeDisplay)
  )
  const bodySegments = $derived(
    highlightSegments(safeDisplay, bodyLanguage, matches, search.trim().length)
  )

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

<!--
  A pane-scoped shortcut on window rather than on the pane's own element,
  because the thing it opens is the only focusable part of the pane: a
  keydown listener on the container would need focus to already be inside
  the response, and the reason to press the shortcut is that it is not.

  Safe as a window listener because App.svelte mounts exactly one of these,
  for the active request only, and it claims a combo nothing else binds.
-->
<svelte:window onkeydown={findInPane} />

<div class="response-inspector">
  {#if selectedTab === 'response'}
    {#if response?.error}
      {#if tlsFailure}
        <ResponseNotice tone="error" title={tlsFailure.summary} role="alert">
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
        </ResponseNotice>
      {:else}
        <ResponseNotice tone="error" title={response.error} role="alert" />
      {/if}
    {:else if response?.cancelled}<ResponseNotice tone="warning" title="Request cancelled." role="status" />{/if}
    {#if !response}
      <!--
        The resting state, in the app's shared empty-state box rather than a
        fifth container of its own. Nothing has gone wrong here, so it carries
        no tone and no aria-live: a request that has not been sent is not an
        event to announce, and this used to announce itself every time the user
        switched between response tabs.
      -->
      <div class="empty-state response-resting">
        <strong>Ready for a response</strong>
        <p>Send the request to inspect its status, headers, body, timeline, and downloadable payload.</p>
      </div>
    {:else}
    <!--
      The body toolbar, restructured.

      Was one crowded row: a `<select>`, three `aria-live` spans, and up to five
      long-text buttons ("Copy preview", "Download", "Render full", "Load more",
      "JSON tree") wrapping onto a second line at narrow widths — followed by a
      PERMANENT second row holding the search box, so the body itself started
      two bars down whether or not anyone was searching.

      Now the rule the whole app follows: what am I looking at on the left,
      what can I do to it on the right, as icons. The truncation controls moved
      out entirely, down to the truncation notice that explains them — see the
      note there.
    -->
    <PaneToolbar ariaLabel="Response body" testId="response-toolbar">
      {#snippet left()}
        <SegmentedControl
          options={[{ value: 'pretty', label: 'Pretty' }, { value: 'raw', label: 'Raw' }, { value: 'base64', label: 'Base64' }, { value: 'hex', label: 'Hex' }]}
          value={selectedView}
          ariaLabel="Response view"
          testId="response-view-select"
          onChange={selectResponseView}
        />
        {#if selectedView === 'pretty' && kind === 'json' && jsonValue}
          <IconButton icon="tree" label="Show as tree" pressed={jsonTreeMode} testId="response-tree-toggle" onclick={() => (jsonTreeMode = !jsonTreeMode)} />
        {/if}
      {/snippet}
      {#snippet middle()}
        <!--
          ONE size statement. The same fact used to be told three times in three
          wordings — "showing X of Y bytes" here, a "Large response" banner
          below, and "Showing the first X of Y bytes" under the body — so a
          reader could not tell whether they were three facts or one.
        -->
        <span aria-live="polite">{bodyTruncated ? `${renderedBytes.toLocaleString()} of ${bytes.toLocaleString()} bytes` : `${bytes.toLocaleString()} bytes`}</span>
      {/snippet}
      {#snippet right()}
        <span class="response-copy-status" aria-live="polite">{copyStatus}</span>
        <IconButton icon="copy" label="Copy visible response preview" testId="response-copy" onclick={() => void copyResponse(safeDisplay)} />
        <!--
          The shortcut is IN the label, so the tooltip and the accessible name
          both carry it. A shortcut that exists only in a keymap the user has
          to go looking for is one nobody finds — and this button was, until
          now, the only way into the response's search at all.
        -->
        <IconButton icon="search" label={`Find in response (${findShortcutLabel})`} pressed={searchOpen} testId="response-search-toggle" onclick={toggleSearch} />
        <IconButton icon="download" label={exportBusy ? 'Saving…' : 'Download exact response body'} disabled={Boolean(exportBusy)} testId="response-download" onclick={() => void onDownloadBody()} />
      {/snippet}
    </PaneToolbar>
    {#if searchOpen}
      <!--
        One find bar, two shapes, because there are two things it can be
        searching. Over the flat body it is steppable and the counter is a
        cursor ("3 of 12"). Over the TREE there is no linear order to step
        through, so it becomes a filter counter ("12 fields") and the branches
        holding hits open themselves.

        What it must never do is report the body's match count while the tree
        is on screen: those matches are offsets into a document the user is not
        looking at, so the number would name hits nothing on screen shows.
      -->
      <div class="response-find">
        <FindBar
          bind:this={searchBar}
          value={search}
          onChange={(next) => { search = next; matchIndex = 0 }}
          ariaLabel="Search response body"
          placeholder={showingTree ? 'Find in fields' : 'Find in response'}
          total={search.trim() ? (showingTree ? treeMatches : matches.length) : undefined}
          activeMatch={showingTree ? undefined : matchIndex}
          onStep={showingTree ? undefined : nextMatch}
          noun={showingTree ? 'fields' : 'matches'}
          testId="response-find-bar"
        />
      </div>
    {/if}
    {#if selectedView === 'pretty' && kind === 'image' && canEmbed && response?.bodyBase64}
      <img class="response-media" alt="Response preview" src={`data:${contentType(headers)};base64,${response.bodyBase64}`} />
    {:else if selectedView === 'pretty' && kind === 'html' && canEmbed}
      <iframe class="response-html" title="Sandboxed HTML response preview" sandbox="" srcdoc={htmlPreview(response?.body ?? '')}></iframe>
    {:else if selectedView === 'pretty' && kind === 'pdf' && canEmbed && response?.bodyBase64}
      <iframe class="response-pdf" title="PDF response preview" sandbox="allow-same-origin" src={`data:application/pdf;base64,${response.bodyBase64}`}></iframe>
    {:else if selectedView === 'pretty' && kind === 'binary'}
      <ResponseNotice title="Binary response" ariaLabel="Binary response">
        <p>{contentType(headers) || 'application/octet-stream'} · {bytes.toLocaleString()} exact bytes</p>
        {#if binaryFilename}<p>Filename: {binaryFilename}</p>{/if}
        <p>Preview is intentionally unavailable to avoid treating binary data as text. Use Hex for a bounded inspection or Download for the exact payload.</p>
      </ResponseNotice>
    {:else if selectedView === 'pretty' && (kind === 'image' || kind === 'html' || kind === 'pdf') && !canEmbed}
      <ResponseNotice tone="warning" title={`This ${kind.toUpperCase()} response is too large to preview`}>
        <p>Download it to inspect the full file.</p>
      </ResponseNotice>
    {:else if selectedView === 'pretty' && wsEvents.length > 0}
      <div class="ws-event-log" data-testid="ws-event-log">{#each wsEvents as event, index (index)}<article class="ws-event-row" data-testid="ws-event-row"><strong>{event.direction || 'system'} · {event.type || 'message'} {event.name || ''} {event.at || ''}</strong><code data-testid={`ws-event-payload-${index}`}>{event.error || event.data || event.dataHex || event.dataBase64 || ''}</code></article>{/each}</div>
    {:else if selectedView === 'pretty' && grpcEventsParsed.length > 0}
      <div class="ws-event-log" data-testid="grpc-stream-event-log">{#each grpcEventsParsed as event, index (index)}<article class="ws-event-row" data-testid="grpc-stream-event-row"><strong>{event.direction || 'system'} · {event.type || 'message'} {event.name || ''} {event.at || ''}</strong><code data-testid={`grpc-stream-event-payload-${index}`}>{event.error || event.data || ''}</code></article>{/each}</div>
    {:else if showingTree}
      <JsonTreeView tree={jsonTree} query={search} testId="response-json-tree" />
    {:else}
      <pre class="response-body" bind:this={bodyElement} data-match-index={matches[matchIndex] ?? -1}>{#each bodySegments as segment, index (index)}{#if segment.match}<span class={segment.kind === 'plain' ? undefined : `response-token-${segment.kind}`}><mark class:current-match={segment.matchIndex === matchIndex}>{segment.text}</mark></span>{:else if segment.kind === 'plain'}{segment.text}{:else}<span class={`response-token-${segment.kind}`}>{segment.text}</span>{/if}{/each}</pre>
    {/if}
    <!--
      The truncation notice, and the controls that act on it, in one place.

      Three separate elements used to state overlapping versions of this: a
      count in the toolbar, a "Large response" banner above the body, and this
      line below it — while the buttons that DO something about it ("Render
      full", "Load more") sat in the toolbar, far from the sentence explaining
      why they existed. Reading the app, it was not obvious those were three
      views of one fact rather than three different problems.
    -->
    {#if bodyTruncated}
      <div class="response-truncation" role="status">
        <span>Showing the first {renderedBytes.toLocaleString()} of {bytes.toLocaleString()} bytes.</span>
        {#if canIncrementTextPreview && !renderFull && visibleLimit < fullRenderLimit}
          <button type="button" onclick={() => (visibleLimit = Math.min(fullRenderLimit, visibleLimit + automaticPreviewLimit))}>Load more</button>
        {/if}
        {#if canRenderFull}
          <button type="button" onclick={() => (renderFull = true)} disabled={!renderableFull}>Render full</button>
        {/if}
        {#if renderFullDisabledReason}
          <span class="response-truncation-reason">{renderFullDisabledReason}</span>
        {:else if renderFull && bytes > fullRenderLimit}
          <span class="response-truncation-reason">Download the body to inspect all content.</span>
        {/if}
      </div>
    {/if}
    {#if (request.examples ?? []).length > 0}
      <section class="response-compare" aria-label="Response comparison">
        <!--
          The comparison picker was a bare `<label><select>` followed by ad hoc
          `<h4>` sections — the last sub-view in this file still arranging its
          own chrome. It is a toolbar like every other: what am I looking at on
          the left, what can I do to it on the right.

          The target stays a `<select>` rather than becoming a SegmentedControl:
          the option list is every saved example of the request, so it is
          unbounded, and a segmented control of unbounded width is the thing
          `PaneToolbar` exists to keep off the left edge.
        -->
        <PaneToolbar ariaLabel="Response comparison" testId="response-compare-toolbar">
          {#snippet left()}
            <label class="compare-picker">Compare with <select aria-label="Compare response with" bind:value={compareId}>{#each compareOptions as option (option.id)}<option value={option.id}>{option.name}</option>{/each}</select></label>
          {/snippet}
          {#snippet middle()}
            {#if compareId !== 'current'}
              <span>{diff.rows.filter((row) => row.changed).length} changed lines · {headerComparison.filter((row) => row.change !== 'unchanged').length} changed headers</span>
            {/if}
          {/snippet}
          {#snippet right()}
            {#if compareId !== 'current'}
              <!--
                A toggle, not a checkbox in the body of the panel: it changes
                what the tables below SHOW, which is a view control, and view
                controls live on the bar for the view.
              -->
              <IconButton icon="list" label="Show unchanged body and header rows" pressed={showUnchanged} testId="response-compare-unchanged" onclick={() => (showUnchanged = !showUnchanged)} />
            {/if}
          {/snippet}
        </PaneToolbar>
        {#if compareId !== 'current'}
          <div class="compare-summary"><span>Status: {response?.status ?? '-'} → {compareTarget?.status ?? '-'} ({response?.status === compareTarget?.status ? 'unchanged' : 'changed'})</span><span>Timing: {response?.durationMs ?? 0} ms → {compareTarget?.duration ? `${compareTarget.duration} ms` : 'unavailable'} ({compareTarget?.duration ? response?.durationMs === compareTarget.duration ? 'unchanged' : 'changed' : 'unavailable'})</span></div>
          <section class="compare-section" aria-label="Header changes"><h4>Headers</h4><table><thead><tr><th>Header</th><th>Current</th><th>Selected</th><th>Change</th></tr></thead><tbody>{#each headerComparison.filter((row) => showUnchanged || row.change !== 'unchanged') as row, index (index)}<tr><td>{row.name}</td><td>{row.current}</td><td>{row.selected}</td><td><span class={`compare-badge ${row.change}`}>{row.change}</span></td></tr>{/each}</tbody></table></section>
          <section class="compare-section" aria-label="JSON structure changes"><h4>JSON structure</h4>{#if jsonComparison?.available}<p>Root type: {jsonComparison.root}</p><p>Keys added: {jsonComparison.added.join(', ') || 'none'} · removed: {jsonComparison.removed.join(', ') || 'none'} · changed type: {jsonComparison.changed.join(', ') || 'none'}</p>{:else}<p>Structure comparison unavailable: {jsonComparison?.reason}</p>{/if}</section>
          <div class="response-diff">{#each diff.rows.filter((row) => showUnchanged || row.changed) as row, index (index)}<div class:changed={row.changed}><code>{row.left}</code><code>{row.right}</code></div>{/each}</div>
          {#if diff.truncated}<small>Diff limited to 2,400 lines.</small>{/if}
        {/if}
      </section>
    {/if}
    {/if}
  {:else if selectedTab === 'headers'}
    <!--
      The headers view, brought in line with the body view above it and with
      the DevTools table that shows the same data: the shared FindBar rather
      than a bespoke input with its own counter wording, Copy as an icon on the
      right, a real header row, and monospace values — a header value is data.
    -->
    <PaneToolbar ariaLabel="Response headers">
      {#snippet left()}
        <FindBar
          bind:this={headerBar}
          value={headerSearch}
          onChange={(next) => (headerSearch = next)}
          ariaLabel="Search headers"
          placeholder="Find in headers"
          total={headerSearch.trim() ? filteredHeaders.length : undefined}
          noun="headers"
          testId="response-header-find-bar"
        />
      {/snippet}
      {#snippet middle()}
        <span>{filteredHeaders.length} of {headerRows.length}</span>
      {/snippet}
      {#snippet right()}
        <span class="response-copy-status" aria-live="polite">{copyStatus}</span>
        <IconButton icon="copy" label="Copy headers" onclick={() => void copyResponse(filteredHeaders.map(([name, value]) => `${name}: ${value}`).join('\n'))} />
      {/snippet}
    </PaneToolbar>
    {#if filteredHeaders.length === 0}<div class="empty-state">{headerSearch.trim() ? 'No headers match this search.' : 'This response carried no headers.'}</div>{:else}<table class="response-kv-table"><thead><tr><th>Header</th><th>Value</th></tr></thead><tbody>{#each filteredHeaders as [name, value] (name)}<tr><td><code>{name}</code></td><td><code>{value}</code></td></tr>{/each}</tbody></table>{/if}
  {:else if selectedTab === 'metadata' || selectedTab === 'trailers'}
    {@const rows = selectedTab === 'metadata' ? response?.metadata ?? [] : response?.trailers ?? []}
    <PaneToolbar ariaLabel={`Response ${selectedTab}`}>
      {#snippet middle()}
        <span>{rows.length} {selectedTab}</span>
      {/snippet}
      {#snippet right()}
        <span class="response-copy-status" aria-live="polite">{copyStatus}</span>
        <IconButton icon="copy" label={`Copy ${selectedTab}`} disabled={rows.length === 0} onclick={() => void copyResponse(rows.map((row) => `${row.name}: ${row.value}`).join('\n'))} />
      {/snippet}
    </PaneToolbar>
    {#if rows.length === 0}<div class="empty-state">This response carried no {selectedTab}.</div>{:else}<table class="response-kv-table"><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>{#each rows as row, index (index)}<tr><td><code>{row.name}</code></td><td><code>{row.value}</code></td></tr>{/each}</tbody></table>{/if}
  {:else if selectedTab === 'timeline'}
    <!--
      The timeline toolbar, brought onto the same bar as the body and headers.

      It was the most crowded row in the pane — a bare search input, a phase
      `<select>`, a live count and two text buttons, five controls of four
      shapes on one line, one tab away from the body's two-icon bar. The phase
      filter is a fixed set of five, so it is a segmented control rather than a
      dropdown that hides four of the five; the counts move into each segment's
      tooltip, and the visible count moves to the toolbar's status slot where
      every other sub-view already puts it.

      The count also loses its `aria-live`. It was recomputed on every keystroke
      of the search box, so a screen reader user typing "oauth" heard five
      separate announcements of a number that was still changing. FindBar
      announces from a wrapper that exists from first render, which is the fix
      the body's bar already carries.
    -->
    <PaneToolbar ariaLabel="Response timeline" testId="response-timeline-toolbar">
      {#snippet left()}
        <FindBar
          bind:this={timelineBar}
          value={timelineSearch}
          onChange={(next) => (timelineSearch = next)}
          ariaLabel="Search timeline"
          placeholder="Find in timeline"
          total={timelineSearch.trim() ? filteredTimeline.length : undefined}
          noun="entries"
          testId="response-timeline-find-bar"
        />
        <SegmentedControl
          options={[
            { value: 'all', label: 'All', title: `All (${timelineFilterCounts.all})` },
            { value: 'pre', label: 'Pre', title: `Pre-request (${timelineFilterCounts.pre})` },
            { value: 'post', label: 'Post', title: `Post-response (${timelineFilterCounts.post})` },
            { value: 'oauth', label: 'OAuth', title: `OAuth (${timelineFilterCounts.oauth})` },
            { value: 'main', label: 'Request', title: `Request (${timelineFilterCounts.main})` }
          ]}
          value={timelineFilter}
          ariaLabel="Timeline phase filter"
          testId="response-timeline-filter"
          onChange={(next) => (timelineFilter = next)}
        />
      {/snippet}
      {#snippet middle()}
        <span>{filteredTimeline.length} of {timeline.length}</span>
      {/snippet}
      {#snippet right()}
        <span class="response-copy-status" aria-live="polite">{copyStatus}</span>
        <IconButton icon="copy" label="Copy timeline" onclick={() => void copyResponse(JSON.stringify(filteredTimeline, null, 2))} />
        <IconButton icon="download" label={exportBusy ? 'Saving…' : 'Export timeline'} disabled={Boolean(exportBusy)} testId="response-timeline-export" onclick={() => void onExportTimeline()} />
      {/snippet}
    </PaneToolbar>
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
  /*
    The resting state. `.empty-state` is the app's shared box and supplies the
    border, radius, padding and colour; this adds only the centring and the
    reading measure, which are the two things that make it a placeholder for a
    whole pane rather than for a table cell.
  */
  .response-resting { display: grid; place-content: center; min-height: 220px; gap: var(--space-7); margin: var(--space-10); text-align: center; }
  .response-resting strong { color: var(--text); font-size: var(--font-size-14); }
  .response-resting p { max-width: 360px; margin: 0; line-height: 1.5; }
  .response-find { padding: var(--space-6) var(--space-10); border-bottom: 1px solid var(--border-subtle); background: var(--surface-alt); }
  .response-copy-status { color: var(--muted); font-size: var(--font-size-11); }
  /* A header name and its value are data, so they read as data. The DevTools
     table showing the same fields has always done this. */
  .response-kv-table code { color: var(--text); }
  /*
    Row feedback, on the same terms the DevTools network table already uses.

    That table was the only one in the app that acknowledged the pointer, so
    every other table — these included — read as a static printout of something
    rather than a surface. The tint is the same `color-mix` expression, so the
    two agree by construction rather than by two hand-picked colours that happen
    to look alike today.

    No `cursor: pointer` and no selected state: these rows do nothing when
    clicked, and dressing them as though they did is a worse lie than no
    feedback at all. Hover here means "this is the row under your pointer",
    which is all a reader wants while tracking a value across two columns.
  */
  .response-kv-table tbody tr:hover td,
  .compare-section tbody tr:hover td,
  .timeline-detail tbody tr:hover td { background: color-mix(in srgb, var(--selected-bg) 55%, transparent); }
  /* The truncation fact and the two controls that change it, on one line. */
  .response-truncation { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-8); padding: var(--space-6) var(--space-10); color: var(--muted); font-size: var(--font-size-11); }
  .response-truncation-reason { color: var(--warning-strong); }
  .response-body { max-height: clamp(390px, 52vh, 520px); overflow: auto; margin: 0; padding: var(--space-12); white-space: pre-wrap; overflow-wrap: anywhere; }
  /*
    The find highlight, and a claim that was not true until now.

    `<mark>` had no rule ANYWHERE — not here, not in style.css — so every search
    hit in a response body rendered in the browser's own yellow-on-black, which
    overrode the syntax colour of whatever it wrapped and was the one thing in
    the pane belonging to no theme. `.current-match` had no rule either, so the
    hit the Previous/Next buttons were moving between looked exactly like the
    twenty that were not: the counter said "3 of 12" and nothing on screen said
    which one was 3.

    The values are not chosen here. CodeEditor.svelte:303 already describes its
    own `.cm-find-match`/`.cm-find-current` as "deliberately the SAME pairing
    the response pane uses for `<mark>` — a warning-tinted hit, an accent-tinted
    current hit" — a comment written against a pairing that did not exist. These
    are those four tokens, so the sentence is now accurate, and
    responseInspector.test.mts asserts the two stay equal rather than trusting
    the comment to keep them so.
  */
  .response-tls-actions { display: flex; flex-wrap: wrap; gap: var(--space-8); }
  .response-tls-detail { margin: 0; font-size: var(--font-size-11); opacity: 0.75; white-space: pre-wrap; word-break: break-word; }
  .response-media, .response-html, .response-pdf { display: block; width: calc(100% - var(--space-20)); max-height: clamp(240px, 48vh, 440px); min-height: 240px; margin: var(--space-10); border: 1px solid var(--border); border-radius: var(--radius-6); object-fit: contain; }
  .response-media { background: var(--surface-raised); }
  .response-html, .response-pdf { background: #fff; }
  .ws-event-log { display: grid; gap: var(--space-6); padding: var(--space-10); max-height: clamp(360px, 54vh, 480px); overflow: auto; }
  .ws-event-row { display: grid; gap: var(--space-5); padding: var(--space-8); border: 1px solid var(--border); border-radius: var(--radius-6); background: var(--surface-raised); }
  .ws-event-row code, .timeline-entry code { overflow-wrap: anywhere; white-space: pre-wrap; }
  .response-compare { margin: var(--space-10); border: 1px solid var(--border); border-radius: var(--radius-6); background: var(--surface-raised); overflow: hidden; }
  .compare-picker { display: flex; align-items: center; gap: var(--space-6); font-size: var(--font-size-11); }
  .response-diff { max-height: clamp(260px, 38vh, 440px); overflow: auto; font-size: var(--font-size-11); }
  .compare-summary { display: flex; flex-wrap: wrap; gap: var(--space-8); margin: var(--space-8); color: var(--muted); font-size: var(--font-size-11); }
  .compare-section { margin: var(--space-8); overflow: auto; }
  .compare-section h4 { margin: 0 0 var(--space-5); font-size: var(--font-size-12); }
  .compare-section p { margin: var(--space-4) 0; color: var(--muted); font-size: var(--font-size-11); }
  .compare-section table { min-width: 520px; font-size: var(--font-size-11); }
  .compare-section td { overflow-wrap: anywhere; }
  .compare-badge { font-size: var(--font-size-10); font-weight: 800; text-transform: uppercase; }
  .compare-badge.added { color: var(--accent-strong); } .compare-badge.removed { color: var(--danger); } .compare-badge.changed { color: var(--warning-strong); }
  .response-diff > div { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--space-8); padding: var(--space-3) var(--space-5); border-bottom: 1px solid var(--border-subtle); }
  .response-diff > div.changed { background: var(--warning-bg-soft); }
  .response-diff code { overflow-wrap: anywhere; white-space: pre-wrap; }
  /*
    The status column is fixed-width but its content is not: entry.statusText is
    "Cancelled" whenever a request is cancelled, which is wider than the track.
    With no min-width or overflow rule the span simply drew past its column and
    collided with the method beside it, at every window width. Each cell now
    clips inside its own track; only the URL column is allowed to take the
    leftover space.

    `gap` matters as much as the widths — without it the columns touch, so even
    an ellipsis reads as though it runs into the next value. And the status
    track is sized to fit "Cancelled" rather than truncating the app's most
    common non-numeric status to "Cancel…".
  */
  .timeline-entry > button { display: grid; grid-template-columns: minmax(72px, auto) minmax(0, 80px) minmax(0, 1fr) auto; gap: var(--space-8); align-items: baseline; width: 100%; text-align: left; }
  .timeline-entry > button > span, .timeline-entry > button > strong, .timeline-entry > button > small { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .timeline-entry > code { display: block; padding: var(--space-8); }
  @media (min-width: 1200px) and (min-height: 820px) {
    .response-body { max-height: clamp(520px, 68vh, 760px); }
    .response-media, .response-html, .response-pdf { max-height: clamp(320px, 64vh, 680px); }
    .ws-event-log { max-height: clamp(480px, 65vh, 720px); }
    .response-diff { max-height: clamp(360px, 50vh, 600px); }
  }
  /* Narrow: the duration drops to its own row rather than being squeezed
     against the URL, and the status keeps room for its longest real value. */
  @media (max-width: 720px) { .timeline-entry > button { grid-template-columns: minmax(72px, auto) minmax(0, 1fr); } .timeline-entry > button strong { grid-column: 1 / -1; } .timeline-entry > button small { grid-column: 1 / -1; } }
</style>

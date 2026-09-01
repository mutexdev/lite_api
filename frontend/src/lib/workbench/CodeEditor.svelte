<script lang="ts">
  import type { Snippet } from 'svelte'
  import { isLargeDocument, utf8ExceedsLimit, variableSignature, type DocumentSizeMemo, type SignatureMemo } from './documentSize'
  import { onDestroy, onMount } from 'svelte'
  import { Compartment, EditorSelection, EditorState, Prec, RangeSetBuilder } from '@codemirror/state'
  import { Decoration, EditorView, ViewPlugin, keymap, type DecorationSet } from '@codemirror/view'
  import { bracketMatching, indentOnInput, syntaxHighlighting } from '@codemirror/language'
  import { lintGutter, linter, type Diagnostic } from '@codemirror/lint'
  import { json, jsonLanguage, jsonParseLinter } from '@codemirror/lang-json'
  import { xml } from '@codemirror/lang-xml'
  import { javascript } from '@codemirror/lang-javascript'
  import { markdown } from '@codemirror/lang-markdown'
  import { basicSetup } from 'codemirror'
  import { liteApiHighlightStyle } from './syntaxHighlight'
  import { findMatches } from './response'
  import FindBar from '../ui/FindBar.svelte'
  import IconButton from '../ui/IconButton.svelte'
  import PaneToolbar from '../ui/PaneToolbar.svelte'

  type Language = 'json' | 'xml' | 'javascript' | 'markdown' | 'text' | 'graphql'
  type VariableInfo = { name: string; scope: string; resolvedValue: string; secret: boolean; found: boolean; validName: boolean }
  type EditorVariable = VariableInfo & { token: string; state: 'valid' | 'missing' | 'invalid' }
  type RestoreState = { length: number; fingerprint: string; ranges: { anchor: number; head: number }[]; scrollTop: number; scrollLeft: number }
  const restoration = new Map<string, RestoreState>()
  const restorationLimit = 100
  const largeDocumentBytes = 1024 * 1024
  const configurationCompartment = new Compartment()
  // Separate from the configuration compartment on purpose: the find highlight
  // changes on every keystroke of the QUERY, and folding it into the other one
  // would rebuild the language, linter and variable extensions along with it.
  const findCompartment = new Compartment()

  // US-028 — runes.
  type Props = {
    value?: string
    editorKey?: string
    language?: Language
    ariaLabel?: string
    testId?: string
    fontSize?: number
    onChange: (value: string) => void
    variableInfo?: VariableInfo[]
    /**
     * The left end of the toolbar, filled by the pane that owns this editor.
     *
     * The request body puts its format picker (JSON / XML / Text) here, which
     * is what makes the toolbar read as "what am I editing" on the left and
     * "what can I do to it" on the right. The script and docs editors pass
     * nothing and the slot simply collapses.
     */
    toolbarStart?: Snippet
  }

  let {
    value = '',
    editorKey = '',
    language = 'text',
    ariaLabel = 'Code editor',
    testId = 'code-editor',
    fontSize = 13,
    onChange,
    variableInfo = [],
    toolbarStart = undefined
  }: Props = $props()

  let host: HTMLDivElement
  let view: EditorView | null = null
  let appliedKey = ''
  let appliedValue = ''
  let emittedValue = ''
  let suppressChange = false
  // $state: these three are read by the template. As plain lets the validation
  // message and the variable chips would freeze at their initial values while
  // the editor kept working — visible only as stale UI.
  let validation = $state('Empty')
  let valid = $state(true)
  let editorVariables = $state<EditorVariable[]>([])
  let configuredKey = ''


  // US-033. Both of these ran on every keystroke: the encode allocated a
  // Uint8Array the size of the whole document — half a megabyte per keystroke
  // on a 500 KB body — and the signature re-serialised every variable. Neither
  // result was needed exactly; both are threshold/identity questions.
  let documentSizeMemo: DocumentSizeMemo | null = null
  let signatureMemo: SignatureMemo | null = null

  // $derived.by, not $derived: these need a function body to thread the memo
  // through. The memos are plain lets rather than $state precisely so writing
  // them here is a cache update and not a reactive mutation — assigning to
  // $state inside a derivation is what Svelte rejects.
  const large = $derived.by(() => {
    const result = isLargeDocument(value, largeDocumentBytes, documentSizeMemo)
    documentSizeMemo = result.memo
    return result.large
  })

  const currentVariableSignature = $derived.by(() => {
    const result = variableSignature(variableInfo, signatureMemo)
    signatureMemo = result.memo
    return result.signature
  })

  const configurationKey = $derived(
    `${language}:${large}:${fontSize}:${ariaLabel}:${currentVariableSignature}`
  )

  // --- inline find -------------------------------------------------------
  //
  // CodeMirror ships a search panel and `basicSetup` binds Mod-f to it. Two
  // problems, both reported by the audit. First, the panel is styled by
  // CodeMirror, not by this app: it floats in over the document in a visual
  // language shared with nothing else on screen — the "search opens a separate
  // window" complaint. Second, its Mod-f binding collides with the app's own
  // global ⌘F, which had no "focus is inside an editable field" guard, so the
  // two fired together.
  //
  // So the panel is gone and the search is a FindBar in this editor's own
  // toolbar — the same component the response pane uses, over the same
  // `findMatches` helper, so the two searches behave identically rather than
  // merely looking similar.
  let findOpen = $state(false)
  let findQuery = $state('')
  let findIndex = $state(0)
  let findBar = $state<ReturnType<typeof FindBar> | null>(null)

  // Bounded by findMatches itself (500 hits), and skipped entirely on large
  // documents where every other language feature is already off.
  const findHits = $derived(large || !findOpen ? [] : findMatches(value, findQuery))

  // An EFFECT: it writes findIndex. Narrowing a search has to pull the cursor
  // back inside the result list, or Next steps to a match that is not there.
  $effect(() => {
    if (findIndex >= findHits.length) findIndex = Math.max(0, findHits.length - 1)
  })

  // Reconfiguring on the hits themselves would rebuild the decoration set on
  // every keystroke of the DOCUMENT too, not just the query. The identity is
  // what the highlight actually depends on.
  const findSignature = $derived(`${findOpen}:${findQuery}:${findIndex}:${findHits.length}`)
  let configuredFind = ''
  $effect(() => {
    if (view && findSignature !== configuredFind) {
      configuredFind = findSignature
      view.dispatch({ effects: findCompartment.reconfigure(findExtensions()) })
    }
  })

  function openFind() {
    findOpen = true
    // The bar has to exist before it can take focus; on the frame it is created
    // the binding is still null.
    queueMicrotask(() => findBar?.focus())
  }

  function closeFind() {
    findOpen = false
    findQuery = ''
    findIndex = 0
    view?.focus()
  }

  function stepFind(direction: 1 | -1) {
    if (!findHits.length) return
    findIndex = (findIndex + direction + findHits.length) % findHits.length
    revealMatch()
  }

  /** Puts the caret on the current hit and scrolls it into view. */
  function revealMatch() {
    const start = findHits[findIndex]
    if (!view || start === undefined) return
    const end = Math.min(view.state.doc.length, start + findQuery.trim().length)
    view.dispatch({ selection: EditorSelection.single(start, end), effects: EditorView.scrollIntoView(start, { y: 'center' }) })
  }

  function findDecorations() {
    return ViewPlugin.fromClass(class {
      decorations: DecorationSet
      constructor() { this.decorations = buildFindDecorations() }
      update() { this.decorations = buildFindDecorations() }
    }, { decorations: (plugin) => plugin.decorations })
  }

  function buildFindDecorations() {
    const length = findQuery.trim().length
    if (!findOpen || !length || !findHits.length) return Decoration.none
    const builder = new RangeSetBuilder<Decoration>()
    for (const [position, start] of findHits.entries()) {
      builder.add(start, start + length, Decoration.mark({ class: position === findIndex ? 'cm-find-match cm-find-current' : 'cm-find-match' }))
    }
    return builder.finish()
  }

  function findExtensions() {
    return [findDecorations()]
  }

  // These three are SIDE EFFECTS, not derivations: they reach into CodeMirror
  // rather than producing a value. Converting them to $derived would silently
  // never run them — a derivation is only evaluated when something reads it,
  // and nothing reads these.
  //
  // configureEditor sets configuredKey, which the guard reads, so the effect
  // settles after one pass rather than looping.
  $effect(() => {
    if (view && configurationKey !== configuredKey) configureEditor()
  })
  $effect(() => {
    if (view) synchronizeEditor()
  })
  $effect(() => {
    if (!view) updateLocalPresentation(value)
  })

  function makeState(doc: string) {
    return EditorState.create({ doc, extensions: editorExtensions() })
  }

  function editorExtensions() {
    return [
      basicSetup,
      bracketMatching(),
      indentOnInput(),
      // Prec.highest, not merely "listed later". CodeMirror resolves a tag
      // against the HIGHEST-PRECEDENCE highlighter that has a rule for it, and
      // `basicSetup` brings its own defaultHighlightStyle along. Appended
      // normally this style would sit below that one and never paint a single
      // token — the change would look applied and do nothing.
      Prec.highest(syntaxHighlighting(liteApiHighlightStyle, { fallback: true })),
      // Prec.highest again, and for the same structural reason: basicSetup's
      // searchKeymap also claims Mod-f, and an appended keymap would lose to
      // it and open the floating panel this replaces. `stopPropagation` is the
      // other half — returning true stops CodeMirror, but the app's global ⌘F
      // listens further up the tree and would still fire the sidebar search on
      // top of the find bar.
      Prec.highest(keymap.of([
        { key: 'Mod-f', run: () => { openFind(); return true }, stopPropagation: true },
        { key: 'Mod-g', run: () => { stepFind(1); return true }, stopPropagation: true },
        { key: 'Shift-Mod-g', run: () => { stepFind(-1); return true }, stopPropagation: true }
      ])),
      // Escape is deliberately NOT in the keymap above.
      //
      // CodeMirror merges every binding for a key into ONE record and ORs their
      // `stopPropagation` flags (view/dist/index.js:9122), then applies the
      // merged flag whenever ANY command in that record succeeds
      // (index.js:9166). So a `{ key: 'Escape', stopPropagation: true }` entry
      // whose own `run` DECLINES still muzzles the binding that accepted:
      // pressing Escape with a selection and the find bar closed ran
      // basicSetup's `simplifySelection`, and our flag then stopped the event
      // before the app's global handler could cancel an in-flight request. The
      // request kept running and a second Escape was needed — which review
      // caught and which no test could have, since both halves are correct in
      // isolation.
      //
      // A DOM handler gets the event itself, so propagation is stopped only on
      // the path that actually handled it.
      Prec.highest(EditorView.domEventHandlers({
        keydown: (event) => {
          if (event.key !== 'Escape' || !findOpen) return false
          closeFind()
          event.preventDefault()
          event.stopPropagation()
          return true
        }
      })),
      lintGutter(),
      configurationCompartment.of(configurationExtensions()),
      findCompartment.of(findExtensions()),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged || suppressChange) return
        const next = update.state.doc.toString()
        appliedValue = next
        emittedValue = next
        updateLocalPresentation(next)
        onChange(next)
      })
    ]
  }

  function configurationExtensions() {
    return [languageExtensions(), EditorView.contentAttributes.of({ role: 'textbox', 'aria-label': ariaLabel, 'aria-multiline': 'true' }), EditorView.theme(editorTheme())]
  }

  function languageExtensions() {
    if (large) return []
    const variableExtension = variableDecorations()
    if (language === 'json') return [json(), linter(jsonParseLinter()), variableExtension]
    if (language === 'xml') return [xml(), linter((current) => xmlDiagnostics(current.state.doc.toString())), variableExtension]
    if (language === 'javascript') return [javascript(), variableExtension]
    if (language === 'markdown') return [markdown(), variableExtension]
    // GraphQL is intentionally plain text until a GraphQL grammar is bundled.
    return [variableExtension]
  }

  function editorTheme() {
    return {
      '&': { color: 'var(--text)', backgroundColor: 'var(--surface)', fontSize: `${fontSize}px`, minHeight: '160px' },
      '.cm-content': { fontFamily: 'var(--code-font-family)', caretColor: 'var(--text)' },
      '.cm-gutters': { backgroundColor: 'var(--surface-raised, var(--surface-soft))', color: 'var(--muted)', borderRight: '1px solid var(--border)' },
      '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'var(--surface-hover, var(--accent-tint))' },
      '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': { backgroundColor: 'var(--selection-bg, var(--focus-ring-strong))' },
      // The find bar's own highlight. Deliberately the SAME pairing the
      // response pane uses for `<mark>` — a warning-tinted hit, an accent-tinted
      // current hit — so a user who has searched a response body already knows
      // what the editor is telling them.
      '.cm-find-match': { backgroundColor: 'var(--warning-bg-soft)', outline: '1px solid var(--warning-border)', borderRadius: '2px' },
      '.cm-find-current': { backgroundColor: 'var(--accent-soft)', outline: '1px solid var(--accent)' },
      '.cm-tooltip': { backgroundColor: 'var(--surface-raised, var(--surface-soft))', color: 'var(--text)', border: '1px solid var(--border)' },
      // The variable chip is styled ONCE, globally, in style.css — not here.
      //
      // These four lines were the source of every divergence the audit
      // measured: a hardcoded 2px radius where the rest of the app uses the
      // token scale, --accent-soft where every other surface uses
      // --accent-tint, a wavy red underline that appears nowhere else, and a
      // dotted-underline secret treatment unique to this editor. Worse, they
      // OVERLAPPED style.css's own .cm-variable-valid rule, so the editor chip
      // took its radius from one and its fill from the other.
      //
      // The decoration still emits `.cm-variable`/`.cm-variable-*`; those class
      // names are now matched by the same global rules the plain-text overlays
      // and the inspector strip use, so all three surfaces agree by
      // construction rather than by three people remembering to.
      '@media (prefers-contrast: more)': { '.cm-content': { textDecorationThickness: '2px' }, '.cm-focused': { outline: '2px solid var(--accent)' } },
      '@media (prefers-reduced-motion: reduce)': { '.cm-scroller': { scrollBehavior: 'auto' } }
    }
  }

  function configureEditor() {
    if (!view) return
    configuredKey = configurationKey
    view.dispatch({ effects: configurationCompartment.reconfigure(configurationExtensions()) })
  }

  function synchronizeEditor() {
    if (!view) return
    if (editorKey !== appliedKey) {
      rememberEditorState(appliedKey)
      appliedKey = editorKey
      appliedValue = value
      emittedValue = value
      configuredKey = configurationKey
      suppressChange = true
      view.setState(makeState(value))
      suppressChange = false
      restoreEditorState(editorKey, value)
      updateLocalPresentation(value)
      return
    }
    if (value === appliedValue || value === emittedValue) return
    appliedValue = value
    emittedValue = value
    suppressChange = true
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } })
    suppressChange = false
    updateLocalPresentation(value)
  }

  function updateLocalPresentation(next: string) {
    // Same threshold, same non-allocating path. This runs on the local
    // presentation update, which is also per-keystroke when no view exists.
    const nextLarge = utf8ExceedsLimit(next, largeDocumentBytes)
    editorVariables = nextLarge ? [] : variablesIn(next)
    const state = validationFor(next, language, nextLarge)
    validation = state.message
    valid = state.valid
  }

  function validationFor(next: string, kind: Language, isLarge: boolean) {
    if (!next.trim()) return { valid: true, message: 'Empty' }
    if (isLarge) return { valid: true, message: 'Large document: basic editing mode' }
    if (kind === 'json') {
      try { JSON.parse(next); return { valid: true, message: 'Valid JSON' } } catch (error) { return { valid: false, message: jsonErrorMessage(next, error) } }
    }
    if (kind === 'xml') {
      const diagnostics = xmlDiagnostics(next)
      return diagnostics.length ? { valid: false, message: diagnostics[0].message } : { valid: true, message: 'Valid XML' }
    }
    return { valid: true, message: 'Ready' }
  }

  function jsonErrorMessage(text: string, error: unknown) {
    const message = error instanceof Error ? error.message : 'Invalid JSON'
    const match = message.match(/position\s+(\d+)/i)
    const position = match ? Number(match[1]) : firstJsonErrorOffset(text)
    const before = text.slice(0, position)
    return `Invalid JSON at line ${before.split('\n').length}, column ${position - before.lastIndexOf('\n')}: ${message}`
  }

  function firstJsonErrorOffset(text: string) {
    const cursor = jsonLanguage.parser.parse(text).cursor()
    do {
      if (cursor.type.isError) return cursor.from
    } while (cursor.next())
    return 0
  }

  function xmlDiagnostics(text: string): Diagnostic[] {
    if (!text.trim()) return []
    const error = new DOMParser().parseFromString(text, 'application/xml').querySelector('parsererror')
    if (!error) return []
    const message = error.textContent?.replace(/\s+/g, ' ').trim() || 'Invalid XML'
    const position = message.match(/line\s*(\d+).*?(?:column|col)\s*(\d+)/i)
    const line = position ? Number(position[1]) : 1
    const column = position ? Number(position[2]) : 1
    const from = offsetAt(text, line, column)
    return [{ from, to: Math.min(text.length, from + 1), severity: 'error', message: `Invalid XML at line ${line}, column ${column}: ${message}` }]
  }

  function offsetAt(text: string, line: number, column: number) {
    let offset = 0
    for (let current = 1; current < line && offset < text.length; current += 1) offset = text.indexOf('\n', offset) + 1 || text.length
    return Math.min(text.length, offset + Math.max(0, column - 1))
  }

  function variableDecorations() {
    return ViewPlugin.fromClass(class {
      decorations: DecorationSet
      constructor(current: EditorView) { this.decorations = buildDecorations(current.state.doc.toString()) }
      update(update: { docChanged: boolean; state: EditorState }) { if (update.docChanged) this.decorations = buildDecorations(update.state.doc.toString()) }
    }, { decorations: (plugin) => plugin.decorations })
  }

  function buildDecorations(text: string) {
    // US-033. Called from the CodeMirror update listener on every docChanged,
    // so this was the third per-keystroke full-document allocation.
    if (utf8ExceedsLimit(text, largeDocumentBytes)) return Decoration.none
    const builder = new RangeSetBuilder<Decoration>()
    const infoByName = new Map(variableInfo.map((item) => [item.name, item]))
    const pattern = /\{\{([^{}]*)\}\}/g
    for (let match = pattern.exec(text); match; match = pattern.exec(text)) {
      const name = match[1].trim()
      const info = infoByName.get(name)
      const state = info ? !info.validName ? 'invalid' : info.found ? 'valid' : 'missing' : isVariableName(name) ? 'missing' : 'invalid'
      const secret = Boolean(info?.secret)
      const source = info?.scope || (state === 'invalid' ? 'invalid token' : 'missing source')
      const title = secret ? `Secret variable from ${source}` : `Variable from ${source}`
      builder.add(match.index, match.index + match[0].length, Decoration.mark({ class: `cm-variable cm-variable-${state}${secret ? ' cm-variable-secret' : ''}`, attributes: { title, 'aria-label': `${match[0]} (${secret ? 'secret ' : ''}${state}, ${source})` } }))
    }
    return builder.finish()
  }

  function variablesIn(text: string) {
    const found = new Set<string>()
    const infoByName = new Map(variableInfo.map((item) => [item.name, item]))
    const variables: EditorVariable[] = []
    for (const match of text.matchAll(/\{\{([^{}]*)\}\}/g)) {
      const name = match[1].trim()
      if (!name || found.has(name)) continue
      found.add(name)
      const info = infoByName.get(name)
      const validName = info?.validName ?? isVariableName(name)
      const state: EditorVariable['state'] = !validName ? 'invalid' : info?.found ? 'valid' : 'missing'
      variables.push({ name, token: match[0], scope: info?.scope || (state === 'invalid' ? 'invalid' : 'missing'), resolvedValue: info?.resolvedValue || '', secret: Boolean(info?.secret), found: Boolean(info?.found), validName, state })
    }
    return variables
  }

  function isVariableName(name: string) { return /^(?:process\.env\.)?[A-Za-z_][A-Za-z0-9_.-]*$/.test(name) }
  function fingerprint(doc: string) { return `${doc.length}:${doc.slice(0, 96)}:${doc.slice(-96)}` }

  function rememberEditorState(key: string) {
    if (!view || !key) return
    const doc = view.state.doc.toString()
    restoration.delete(key)
    restoration.set(key, { length: doc.length, fingerprint: fingerprint(doc), ranges: view.state.selection.ranges.map((range) => ({ anchor: range.anchor, head: range.head })), scrollTop: view.scrollDOM.scrollTop, scrollLeft: view.scrollDOM.scrollLeft })
    if (restoration.size > restorationLimit) restoration.delete(restoration.keys().next().value as string)
  }

  function restoreEditorState(key: string, doc: string) {
    const saved = restoration.get(key)
    if (!view || !saved || saved.length !== doc.length || saved.fingerprint !== fingerprint(doc)) return
    const ranges = saved.ranges.filter((range) => range.anchor <= doc.length && range.head <= doc.length)
    if (ranges.length) view.dispatch({ selection: EditorSelection.create(ranges.map((range) => EditorSelection.range(range.anchor, range.head))) })
    requestAnimationFrame(() => { if (view) { view.scrollDOM.scrollTop = saved.scrollTop; view.scrollDOM.scrollLeft = saved.scrollLeft } })
  }

  function format() {
    if (!valid || large) return
    if (language === 'json') emitTransform(JSON.stringify(JSON.parse(value), null, 2))
    if (language === 'xml') emitTransform(prettyXml(value))
  }
  function minify() {
    if (!valid || large) return
    if (language === 'json') emitTransform(JSON.stringify(JSON.parse(value)))
    if (language === 'xml') emitTransform(value.replace(/>\s+</g, '><').trim())
  }
  function emitTransform(next: string) { if (view && next !== value) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: next } }) }
  function prettyXml(value: string) {
    const tokens = value.replace(/>\s+</g, '><').trim().match(/<[^>]+>|[^<]+/g) ?? []
    let depth = 0
    const lines: string[] = []
    for (const token of tokens) {
      const item = token.trim()
      if (!item) continue
      if (item.startsWith('</')) depth = Math.max(0, depth - 1)
      lines.push(`${'  '.repeat(depth)}${item}`)
      if (item.startsWith('<') && !item.startsWith('</') && !item.startsWith('<?') && !item.startsWith('<!') && !item.endsWith('/>')) depth += 1
    }
    return lines.join('\n')
  }

  onMount(() => {
    appliedKey = editorKey
    appliedValue = value
    emittedValue = value
    configuredKey = configurationKey
    updateLocalPresentation(value)
    view = new EditorView({ state: makeState(value), parent: host })
    restoreEditorState(editorKey, value)
  })
  onDestroy(() => { rememberEditorState(editorKey); view?.destroy() })
</script>

  <div class="code-editor" data-testid={testId}>
  <PaneToolbar ariaLabel="Editor actions" testId="editor-toolbar">
    {#snippet left()}{@render toolbarStart?.()}{/snippet}
    {#snippet middle()}
      <!--
        aria-live moved OFF the message and onto a wrapper that is always
        present. On the message itself the whole element was replaced on every
        keystroke, so a screen reader re-announced "Valid JSON" for each
        character typed.
      -->
      <span aria-live="polite"><span data-testid="editor-validation" class:invalid={!valid}>{validation}</span></span>
    {/snippet}
    {#snippet right()}
      {#if language === 'json' || language === 'xml'}
        <IconButton icon="format" label="Format" testId="editor-format-control" disabled={!valid || large || validation === 'Empty'} onclick={format} />
        <IconButton icon="minify" label="Minify" testId="editor-minify-control" disabled={!valid || large || validation === 'Empty'} onclick={minify} />
      {/if}
      <IconButton icon="search" label="Find in editor" testId="editor-search-control" pressed={findOpen} onclick={() => (findOpen ? closeFind() : openFind())} />
    {/snippet}
  </PaneToolbar>
  {#if findOpen}
    <div class="code-editor-find">
      <FindBar
        bind:this={findBar}
        value={findQuery}
        onChange={(next) => { findQuery = next; findIndex = 0; if (next.trim()) queueMicrotask(revealMatch) }}
        ariaLabel="Find in editor"
        placeholder="Find in editor"
        total={findQuery.trim() ? findHits.length : undefined}
        activeMatch={findIndex}
        onStep={stepFind}
        noun="matches"
        testId="editor-find-bar"
      />
    </div>
  {/if}
  {#if large}<div class="editor-large" data-testid="editor-large-mode">Large document: syntax parsing, variable marking, and format controls are disabled; full content remains editable and searchable.</div>{/if}
  <div bind:this={host} data-testid={`${testId}-surface`}></div>
  {#if editorVariables.length}<details class="editor-variables"><summary>Variables in this editor ({editorVariables.length})</summary>{#each editorVariables as variable, index (index)}<div class={`editor-variable ${variable.state}`}><strong>{variable.token}</strong> · {variable.scope} · {variable.secret ? 'secret value hidden' : variable.state === 'valid' ? variable.resolvedValue : variable.state}</div>{/each}</details>{/if}
</div>

<style>
  .code-editor{border:1px solid var(--border);border-radius:var(--radius-6);overflow:hidden}
  .code-editor-find{padding:var(--space-6) var(--space-10);border-bottom:1px solid var(--border-subtle);background:var(--surface-alt)}
  .invalid{color:var(--danger)!important}.editor-large{padding:6px 8px;color:var(--warning-strong);font-size:11px;background:var(--warning-bg-soft)}.editor-variables{padding:6px 8px;font-size:11px;border-top:1px solid var(--border)}.editor-variable.missing,.editor-variable.invalid{color:var(--danger)}
</style>

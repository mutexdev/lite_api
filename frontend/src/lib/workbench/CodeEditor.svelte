<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { Compartment, EditorSelection, EditorState, RangeSetBuilder } from '@codemirror/state'
  import { Decoration, EditorView, ViewPlugin, type DecorationSet } from '@codemirror/view'
  import { bracketMatching, indentOnInput, syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language'
  import { lintGutter, linter, type Diagnostic } from '@codemirror/lint'
  import { json, jsonLanguage, jsonParseLinter } from '@codemirror/lang-json'
  import { xml } from '@codemirror/lang-xml'
  import { javascript } from '@codemirror/lang-javascript'
  import { markdown } from '@codemirror/lang-markdown'
  import { openSearchPanel } from '@codemirror/search'
  import { basicSetup } from 'codemirror'

  type Language = 'json' | 'xml' | 'javascript' | 'markdown' | 'text' | 'graphql'
  type VariableInfo = { name: string; scope: string; resolvedValue: string; secret: boolean; found: boolean; validName: boolean }
  type EditorVariable = VariableInfo & { token: string; state: 'valid' | 'missing' | 'invalid' }
  type RestoreState = { length: number; fingerprint: string; ranges: { anchor: number; head: number }[]; scrollTop: number; scrollLeft: number }
  const restoration = new Map<string, RestoreState>()
  const restorationLimit = 100
  const largeDocumentBytes = 1024 * 1024
  const configurationCompartment = new Compartment()

  export let value = ''
  export let editorKey = ''
  export let language: Language = 'text'
  export let ariaLabel = 'Code editor'
  export let testId = 'code-editor'
  export let fontSize = 13
  export let onChange: (value: string) => void
  export let variableInfo: VariableInfo[] = []

  let host: HTMLDivElement
  let view: EditorView | null = null
  let appliedKey = ''
  let appliedValue = ''
  let emittedValue = ''
  let suppressChange = false
  let validation = 'Empty'
  let valid = true
  let editorVariables: EditorVariable[] = []
  let configuredKey = ''

  $: byteLength = new TextEncoder().encode(value).byteLength
  $: large = byteLength > largeDocumentBytes
  $: configurationKey = `${language}:${large}:${fontSize}:${ariaLabel}:${variableSignature(variableInfo)}`
  $: if (view && configurationKey !== configuredKey) configureEditor()
  $: if (view) synchronizeEditor()
  $: if (!view) updateLocalPresentation(value)

  function makeState(doc: string) {
    return EditorState.create({ doc, extensions: editorExtensions() })
  }

  function editorExtensions() {
    return [
      basicSetup,
      bracketMatching(),
      indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      lintGutter(),
      configurationCompartment.of(configurationExtensions()),
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
      '.cm-searchMatch': { backgroundColor: 'var(--warning-bg-soft)', outline: '1px solid var(--warning-border)' },
      '.cm-searchMatch-selected': { backgroundColor: 'var(--accent-soft)', outline: '1px solid var(--accent)' },
      '.cm-tooltip': { backgroundColor: 'var(--surface-raised, var(--surface-soft))', color: 'var(--text)', border: '1px solid var(--border)' },
      '.cm-variable': { borderRadius: '2px' },
      '.cm-variable-valid': { backgroundColor: 'var(--accent-soft)' },
      '.cm-variable-missing, .cm-variable-invalid': { backgroundColor: 'var(--danger-bg-soft)', textDecoration: 'wavy underline var(--danger)' },
      '.cm-variable-secret': { borderBottom: '1px dotted var(--warning-strong)' },
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
    const nextLarge = new TextEncoder().encode(next).byteLength > largeDocumentBytes
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
    if (new TextEncoder().encode(text).byteLength > largeDocumentBytes) return Decoration.none
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
  function variableSignature(items: VariableInfo[]) { return items.map((item) => `${item.name}:${item.scope}:${item.secret}:${item.found}:${item.validName}`).join('|') }
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
  <div class="code-editor-toolbar"><button type="button" data-testid="editor-search-control" on:click={() => view && openSearchPanel(view)}>Search</button>{#if language === 'json' || language === 'xml'}<button type="button" data-testid="editor-format-control" disabled={!valid || large || validation === 'Empty'} on:click={format}>Format</button><button type="button" data-testid="editor-minify-control" disabled={!valid || large || validation === 'Empty'} on:click={minify}>Minify</button>{/if}<span>{fontSize}px</span><span data-testid="editor-validation" aria-live="polite" class:invalid={!valid}>{validation}</span></div>
  {#if large}<div class="editor-large" data-testid="editor-large-mode">Large document: syntax parsing, variable marking, and format controls are disabled; full content remains editable and searchable.</div>{/if}
  <div bind:this={host} data-testid={`${testId}-surface`}></div>
  {#if editorVariables.length}<details class="editor-variables"><summary>Variables in this editor ({editorVariables.length})</summary>{#each editorVariables as variable}<div class={`editor-variable ${variable.state}`}><strong>{variable.token}</strong> · {variable.scope} · {variable.secret ? 'secret value hidden' : variable.state === 'valid' ? variable.resolvedValue : variable.state}</div>{/each}</details>{/if}
</div>

<style>
  .code-editor{border:1px solid var(--border);border-radius:6px;overflow:hidden}.code-editor-toolbar{display:flex;gap:6px;align-items:center;padding:5px;border-bottom:1px solid var(--border)}.code-editor-toolbar span{font-size:11px;color:var(--muted)}.invalid{color:var(--danger)!important}.editor-large{padding:6px 8px;color:var(--warning-strong);font-size:11px;background:var(--warning-bg-soft)}.editor-variables{padding:6px 8px;font-size:11px;border-top:1px solid var(--border)}.editor-variable.missing,.editor-variable.invalid{color:var(--danger)}
</style>

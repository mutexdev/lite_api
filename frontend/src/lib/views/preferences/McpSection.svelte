<script lang="ts">
  // The AI access (MCP) section of the Preferences panel, extracted like its
  // siblings so its markup is not in the initial chunk.
  //
  // WHY THIS ONE FETCHES. Every other section renders preferences and nothing
  // else. This one has to show whether the listener actually came up, and that
  // is not a preference — the toggle can be on while the port is taken, or
  // while this process is a secondary workspace window that deliberately
  // declined to bind. GetMCPStatus is the only thing that knows, so the section
  // calls it on mount and again after each write round-trips. Re-fetching after
  // rather than before the write is what makes the line agree with the toggle
  // the user just flipped.
  //
  // WHY THIS FILE IS STILL PRE-RUNES while its seven siblings were converted
  // alongside the SettingRow migration: four source-text tests pin the exact
  // spelling of the wiring in here, and one of them matches the literal
  // `maskedCommand = maskToken(pairingCommand)`, which `$derived` cannot
  // produce. Converting the file means editing test/mcpSection.test.mts in the
  // same change, and the two are owned by different passes. The conversion is
  // recorded as a follow-up rather than done half-way — a file that is runes in
  // its markup and legacy in its state is the worst of both.
  import { onDestroy, onMount } from 'svelte'
  import type { types } from '../../../../wailsjs/go/models'
  import { GetMCPAuditLog, GetMCPStatus } from '../../../../wailsjs/go/core/App'
  import {
    DEFAULT_MCP_PORT,
    MCP_AUDIT_LIMIT,
    maskToken,
    mcpAuditRows,
    mcpStatusSummary,
    normalizeMcpPort,
  } from '../../mcpSettings'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  export let state: types.AppState
  export let onUpdateMcp: (patch: Partial<types.MCPPreferences>) => Promise<void> | void
  export let onCopyCommand: (value: string) => Promise<boolean> | boolean

  let status: types.MCPStatus | undefined
  let copied = false
  let copyResetTimer: ReturnType<typeof setTimeout> | undefined

  $: mcp = state.preferences.mcp
  $: summary = mcpStatusSummary(status)
  // The unmasked command is what the Copy button hands to the clipboard; the
  // masked one is only ever rendered. Keeping them in two named variables is
  // what stops a later edit from copying what is on screen.
  $: pairingCommand = status?.command ?? ''
  $: maskedCommand = maskToken(pairingCommand)

  async function refreshStatus(): Promise<void> {
    try {
      status = await GetMCPStatus()
    } catch {
      // A status that cannot be read is reported as "Off" rather than as an
      // error banner: the section is still usable, and the toggle below is
      // still the truth about what was asked for.
      status = undefined
    }
  }

  /*
   * A6-11's equivalent here. `applyMcp` is the slowest write in the whole panel
   * and the only one that was completely silent: turning the toggle on saves
   * the preference, then asks Go for the listener state, which means binding a
   * socket. Until both legs land, `state.preferences.mcp` still holds the old
   * value and the Status row still shows the old line, so the checkbox springs
   * back and the section reads as if the click did nothing — on the one setting
   * in this panel where "did that take effect?" actually matters.
   *
   * Named per field rather than a boolean, because the three rows that call
   * applyMcp must not all show "Starting..." when one of them was changed.
   */
  let applying: '' | 'enabled' | 'port' | 'writeTierEnabled' = ''

  /** Writes the preference, then re-reads what the backend made of it. */
  async function applyMcp(patch: Partial<types.MCPPreferences>): Promise<void> {
    const field = (Object.keys(patch)[0] ?? '') as typeof applying
    applying = field
    try {
      await onUpdateMcp(patch)
      await refreshStatus()
    } finally {
      // Cleared in `finally` so a backend that refuses the write does not leave
      // the row saying it is still working on it.
      applying = ''
    }
  }

  async function copyPairingCommand(): Promise<void> {
    if (!pairingCommand) return
    copied = await onCopyCommand(pairingCommand)
    if (!copied) return
    clearTimeout(copyResetTimer)
    copyResetTimer = setTimeout(() => { copied = false }, 1500)
  }

  // --- recent activity ----------------------------------------------------
  //
  // WHY A LOG BELONGS IN THIS PANEL. Everything above describes what an agent is
  // ALLOWED to do. Nothing above says what one actually did — and an interface
  // that grants a capability without ever showing its use is one the user has to
  // take on trust. This is the only surface where "did something connect, and
  // what did it ask for?" has an answer.
  //
  // The entries arrive newest first from mcpAuditStore.List, so nothing here
  // sorts them.

  /** The rendered rows. `undefined` until the first fetch settles. */
  let auditEntries: types.MCPAuditEntry[] | undefined
  let auditError = ''
  let auditLoading = false
  let auditPollTimer: ReturnType<typeof setInterval> | undefined

  // Recomputed on a tick so relative-to-today timestamps do not go stale in a
  // panel left open across midnight.
  let auditNow = new Date()

  $: auditRows = mcpAuditRows(auditEntries, auditNow)

  /**
   * Polls at a cadence set by how the list is read, not by how fast it changes.
   *
   * Every call re-reads the whole JSONL file on the Go side, and nobody watches
   * this panel for a live feed — they open it after an agent did something. Five
   * seconds is fast enough that a run finishing while the panel is open appears
   * without the user reaching for Refresh, and slow enough to be free.
   */
  const AUDIT_POLL_MS = 5000

  async function refreshAudit(): Promise<void> {
    // Overlapping fetches would let a slow call land after a fast one and
    // display the older list.
    if (auditLoading) return
    auditLoading = true
    try {
      auditEntries = await GetMCPAuditLog(MCP_AUDIT_LIMIT)
      auditNow = new Date()
      auditError = ''
    } catch (err) {
      // Reported in place rather than through the app's notification channel: a
      // background poll that raised a toast every five seconds would bury the
      // notices that matter. The previously loaded rows stay on screen — they
      // were true when they were read.
      auditError = err instanceof Error ? err.message : String(err ?? 'unknown error')
    } finally {
      auditLoading = false
    }
  }

  onMount(refreshStatus)

  // A SECOND onMount, deliberately. The first is asserted on by
  // test/mcpSection.test.mts as the proof that status is fetched when the
  // section appears; splitting the audit lifecycle out keeps that assertion
  // about one thing.
  onMount(() => {
    void refreshAudit()
    auditPollTimer = setInterval(() => {
      // The panel is only mounted while Preferences is open — the settings stack
      // lives inside the `activeView === 'preferences'` branch — so mounting is
      // already the visibility gate. document.hidden covers the rest: a
      // minimised window has nobody reading this.
      if (typeof document !== 'undefined' && document.hidden) return
      void refreshAudit()
    }, AUDIT_POLL_MS)
  })

  onDestroy(() => {
    clearTimeout(copyResetTimer)
    clearInterval(auditPollTimer)
  })
</script>

<SettingSection title="AI access (MCP)">
  <SettingRow
    label="Let AI tools connect to LiteAPI"
    description="Serves your collections, requests, flows and environment variable names to MCP clients such as Claude Code. Secret values never cross the boundary — templates stay unresolved and credential-bearing response headers are redacted — and the server listens on 127.0.0.1 only, behind a token generated for this install."
    checkboxId="mcpEnabled"
    data-testid="mcp-enabled-toggle"
    busy={applying === 'enabled'}
    busyLabel={mcp?.enabled ? 'Stopping…' : 'Starting…'}
    checked={mcp?.enabled ?? false}
    onCheckedChange={(value) => applyMcp({ enabled: value })}
  />

  <SettingRow
    label="Port"
    labelFor="mcpPort"
    busy={applying === 'port'}
    busyLabel="Rebinding…"
    description="The port is written into the pairing command below, so after changing it you have to re-add LiteAPI in your agent. An agent left on the old command keeps the stale URL and reports a connection failure that says nothing about the port having moved."
  >
    {#snippet control()}
      <input
        id="mcpPort"
        data-testid="mcp-port-input"
        type="number"
        min="1"
        max="65535"
        value={mcp?.port ?? DEFAULT_MCP_PORT}
        on:change={(event) => applyMcp({ port: normalizeMcpPort(event.currentTarget.value) })}
      />
    {/snippet}
  </SettingRow>

  <SettingRow label="Status">
    {#snippet control()}
      <!--
        WHAT WENT WRONG. This line carried `data-tone` — running, warning or off
        — and `grep data-tone style.css` returned nothing, so the attribute was
        written for tests and read by no stylesheet at all. The listener state
        rendered as identical grey text in all three cases, three lines above
        badges that colour-code exactly the same kind of information. The state
        that most needed to stand out is the middle one: the toggle reads ON
        while nothing is listening, and grey text is how that looked like
        success.
      -->
      <span class="status-tone" data-testid="mcp-status" data-tone={summary.tone}>
        {summary.stateLabel}{#if summary.lastError} — {summary.lastError}{/if}
      </span>
    {/snippet}
  </SettingRow>

  {#if pairingCommand}
    <SettingRow
      label="Pairing command"
      description="Run this once in a terminal where Claude Code is installed. The token is shortened here for display; Copy puts the full command on the clipboard."
    >
      {#snippet control()}
        <span class="selected-path-chip" data-testid="mcp-pairing-command">{maskedCommand}</span>
        <button
          type="button"
          class="copy-button"
          class:copy-success={copied}
          data-testid="mcp-copy-command-btn"
          on:click={copyPairingCommand}
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      {/snippet}
    </SettingRow>
  {/if}

  <SettingRow
    label="Allow AI tools to create and edit requests"
    description="Off by default. Switched on, an agent can add and edit requests and Flows in your collections — the same files your own edits write. Three things stay impossible either way: it can never read or define a secret value (only reference one by name), it can never write or change a pre-request script, post-response script or test, and pointing a secret at a host your collections have never used still stops here for your approval."
    checkboxId="mcpWriteTier"
    data-testid="mcp-write-tier-toggle"
    busy={applying === 'writeTierEnabled'}
    checked={mcp?.writeTierEnabled ?? false}
    onCheckedChange={(value) => applyMcp({ writeTierEnabled: value })}
  />
</SettingSection>

<SettingSection
  title="Recent activity"
  note="The last {MCP_AUDIT_LIMIT} tool calls an AI tool made, newest first. Denied is kept apart from failed: a denial is a rule holding, not something going wrong."
>
  {#snippet status()}
    <button
      type="button"
      data-testid="mcp-audit-refresh-btn"
      disabled={auditLoading}
      on:click={refreshAudit}
    >{auditLoading ? 'Refreshing…' : 'Refresh'}</button>
  {/snippet}

  {#if auditError}
    <p class="mcp-note mcp-audit-error" data-testid="mcp-audit-error">
      The activity log could not be read — {auditError}
    </p>
  {/if}

  {#if auditRows.length === 0}
    <p class="mcp-note" data-testid="mcp-audit-empty">
      {auditEntries === undefined && !auditError
        ? 'Loading recent activity…'
        : 'No agent activity recorded yet.'}
    </p>
  {:else}
    <ul class="mcp-audit-list" data-testid="mcp-audit-list">
      {#each auditRows as row (row.key)}
        <li class="mcp-audit-row">
          <div class="mcp-audit-line">
            <span class="mcp-audit-time">{row.time}</span>
            <span class="mcp-audit-tool">{row.tool}</span>
            <span
              class="status-tone badge"
              data-tone={row.outcome}
              data-testid="mcp-audit-outcome"
            >{row.outcomeLabel}</span>
            <span class="mcp-audit-duration">{row.duration}</span>
          </div>
          {#if row.argsSummary}
            <p class="mcp-audit-args">{row.argsSummary}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</SettingSection>

<style>
  /* Matches a SettingRow description exactly, for the two paragraphs in this
     section that belong to no row — the log's error and its empty state. No
     custom property is defined here, because a new `--` name would have to be
     added to all 12 theme blocks to mean anything. */
  .mcp-note {
    max-width: 62ch;
    margin: 0;
    color: var(--muted);
    font-size: var(--font-size-12);
    line-height: 1.5;
  }

  .mcp-audit-error {
    color: var(--danger-strong);
  }

  .mcp-audit-list {
    display: grid;
    gap: var(--space-4);
    /* Capped rather than unbounded: 50 rows would push every section below this
       one off the settings page. */
    max-height: 260px;
    overflow-y: auto;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-6);
  }

  .mcp-audit-row {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-6) var(--space-8);
    border-bottom: 1px solid var(--border-subtle);
  }

  .mcp-audit-row:last-child {
    border-bottom: none;
  }

  .mcp-audit-line {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: var(--space-8);
    font-size: var(--font-size-12);
  }

  .mcp-audit-time {
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .mcp-audit-tool {
    flex: 1 1 auto;
    min-width: 0;
    color: var(--text);
    font-family: var(--code-font-family);
    font-weight: 700;
    overflow-wrap: anywhere;
  }

  .mcp-audit-duration {
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  /* ONE BADGE FOR EVERY "HOW DID THAT GO" IN THIS SECTION. The listener state
     and the per-call outcomes are the same kind of information — a severity —
     so they are the same element with the same six-value tone vocabulary, and a
     reader learns the palette once. Splitting them was how one of them ended up
     with no palette at all. */
  .status-tone {
    display: inline-block;
    max-width: 100%;
    padding: var(--space-1) var(--space-6);
    border: 1px solid var(--border);
    border-radius: var(--radius-pill);
    color: var(--muted-strong);
    font-size: var(--font-size-11);
    font-weight: 700;
    overflow-wrap: anywhere;
  }

  /* One vocabulary, two form factors. A per-call outcome is one word repeated
     down a list, so it is set small and uppercase to read as a marker rather
     than as text. The listener status is a sentence — it can carry a backend
     error after its state word — so it keeps sentence case and is allowed to
     wrap. Same tones, same shapes, different amount to say. */
  .status-tone.badge {
    font-size: var(--font-size-10);
    letter-spacing: 0.02em;
    text-transform: uppercase;
    white-space: nowrap;
  }

  /* THE TONES ARE FOUR DIFFERENT STATEMENTS, and the palette says which.
     "ok" and "running" stay quiet — between them they are almost every line
     this section ever renders, and a page where everything is coloured has
     nothing highlighted. "denied" and "warning" wear the warning colours
     because a boundary held or a listener did not come up: things to notice,
     not things that broke. "error" is the only one on the danger palette.
     Folding denied into that red is what would make a refusal indistinguishable
     from a fault, which is the exact question this list is opened to answer.

     "off" is the deliberate absence of all of it: nothing is running because
     nobody asked for anything to run. */
  .status-tone[data-tone='ok'],
  .status-tone[data-tone='running'] {
    border-color: color-mix(in srgb, var(--success) 40%, transparent);
    background: var(--success-bg);
    color: var(--success);
  }

  .status-tone[data-tone='denied'],
  .status-tone[data-tone='warning'] {
    border-color: var(--warning-border);
    background: var(--warning-bg-soft);
    color: var(--warning-text);
  }

  .status-tone[data-tone='error'] {
    border-color: var(--danger-border);
    background: var(--danger-bg);
    color: var(--danger-strong);
  }

  .status-tone[data-tone='off'] {
    color: var(--muted);
  }

  /* Monospace and muted: the summary is evidence, not prose. `anywhere` rather
     than a nowrap ellipsis so a long one wraps inside its row instead of
     stretching the settings page sideways. */
  .mcp-audit-args {
    margin: 0;
    color: var(--muted);
    font-family: var(--code-font-family);
    font-size: var(--font-size-11);
    line-height: 1.45;
    overflow-wrap: anywhere;
  }
</style>

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
  import { onDestroy, onMount } from 'svelte'
  import type { types } from '../../../../wailsjs/go/models'
  import { GetMCPStatus } from '../../../../wailsjs/go/core/App'
  import { DEFAULT_MCP_PORT, maskToken, mcpStatusSummary, normalizeMcpPort } from '../../mcpSettings'

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

  /** Writes the preference, then re-reads what the backend made of it. */
  async function applyMcp(patch: Partial<types.MCPPreferences>): Promise<void> {
    await onUpdateMcp(patch)
    await refreshStatus()
  }

  async function copyPairingCommand(): Promise<void> {
    if (!pairingCommand) return
    copied = await onCopyCommand(pairingCommand)
    if (!copied) return
    clearTimeout(copyResetTimer)
    copyResetTimer = setTimeout(() => { copied = false }, 1500)
  }

  onMount(refreshStatus)
  onDestroy(() => clearTimeout(copyResetTimer))
</script>

            <section>
              <div class="settings-section-header">
                <h3>AI access (MCP)</h3>
              </div>

              <label class="inline-toggle">
                <input
                  id="mcpEnabled"
                  data-testid="mcp-enabled-toggle"
                  type="checkbox"
                  checked={mcp?.enabled ?? false}
                  on:change={(event) => applyMcp({ enabled: event.currentTarget.checked })}
                />
                Let AI tools connect to LiteAPI
              </label>
              <p class="settings-hint">
                Serves your collections, requests, flows and environment variable names to MCP
                clients such as Claude Code. Secret values never cross the boundary — templates
                stay unresolved and credential-bearing response headers are redacted — and the
                server listens on 127.0.0.1 only, behind a token generated for this install.
              </p>

              <div class="field-grid compact-preference-grid">
                <label class="field-label" for="mcpPort">Port</label>
                <input
                  id="mcpPort"
                  data-testid="mcp-port-input"
                  type="number"
                  min="1"
                  max="65535"
                  value={mcp?.port ?? DEFAULT_MCP_PORT}
                  on:change={(event) => applyMcp({ port: normalizeMcpPort(event.currentTarget.value) })}
                />
              </div>
              <p class="settings-hint">
                The port is written into the pairing command below, so after changing it you have
                to re-add LiteAPI in your agent. An agent left on the old command keeps the stale
                URL and reports a connection failure that says nothing about the port having moved.
              </p>

              <p class="settings-hint" data-testid="mcp-status" data-tone={summary.tone}>
                {summary.stateLabel}{#if summary.lastError} — {summary.lastError}{/if}
              </p>

              {#if pairingCommand}
                <div class="button-row">
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
                </div>
                <p class="settings-hint">
                  Run this once in a terminal where Claude Code is installed. The token is shortened
                  here for display; Copy puts the full command on the clipboard.
                </p>
              {/if}

              <label class="inline-toggle">
                <input
                  id="mcpWriteTier"
                  data-testid="mcp-write-tier-toggle"
                  type="checkbox"
                  checked={mcp?.writeTierEnabled ?? false}
                  on:change={(event) => applyMcp({ writeTierEnabled: event.currentTarget.checked })}
                />
                Allow AI tools to create and edit requests
              </label>
              <p class="settings-hint">
                Off by default. It unlocks the authoring tools in a later phase; until then nothing
                reads it. Even switched on, an agent can reference a secret variable by name but can
                never read or set its value.
              </p>
            </section>

<style>
  /* Matches GeneralSection's hint styling. Scoped styles are per-component, so
     this is a copy of that rule rather than a shared class — and no custom
     property is defined here, because a new `--` name would have to be added to
     all 12 theme blocks to mean anything. */
  .settings-hint {
    grid-column: 1 / -1;
    margin: -4px 0 4px;
    max-width: 62ch;
    font-size: 0.8rem;
    opacity: 0.8;
  }
</style>

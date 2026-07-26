<script lang="ts">
  // US-036 — the Terminal tab of the Dev Tools panel.
  //
  // Last of the small DevTools tabs. Network remains and is the largest, with
  // its own nested request-details panel.
  import type { types } from '../../../../wailsjs/go/models'

  // Bindable: the input line writes back to App.svelte.
  export let terminalInput: string

  export let terminalSessions: types.TerminalSession[]
  export let terminalActiveSessionId: string
  export let activeTerminalSession: types.TerminalSession | undefined
  export let terminalBusy: boolean
  export let terminalError: string
  // Another function-shaped name read as a value — the sixth this session.
  export let terminalDisplayOutput: (output: string) => string
  export let terminalOutput: string
  export let terminalSessionLabel: (session: types.TerminalSession) => string
  export let terminalSessionStatus: (session: types.TerminalSession) => string
  export let createTerminalSession: () => void
  export let selectTerminalSession: (id: string) => void
  export let closeTerminalSession: (id: string) => void
  export let sendTerminalInput: () => void
</script>

              <div class="terminal-shell">
                <header>
                  <h3>Terminal</h3>
                  <button type="button" on:click={createTerminalSession} disabled={terminalBusy}>
                    {terminalBusy ? 'Starting...' : 'New Terminal Session'}
                  </button>
                </header>
                <div class="terminal-body">
                  <aside>
                    <strong>Sessions</strong>
                    {#if terminalSessions.length === 0}
                      <div class="empty-state">No active sessions</div>
                    {:else}
                      <div class="terminal-session-list">
                        {#each terminalSessions as session (session.id)}
                          <div class:active={terminalActiveSessionId === session.id} class="terminal-session-row">
                            <button type="button" class="terminal-session-button" on:click={() => selectTerminalSession(session.id)}>
                              <strong>{terminalSessionLabel(session)}</strong>
                              <span>{terminalSessionStatus(session)}</span>
                              <small>{session.cwd}</small>
                            </button>
                            <button type="button" class="icon-button subtle" title="Close terminal session" aria-label="Close terminal session" on:click={() => closeTerminalSession(session.id)}>×</button>
                          </div>
                        {/each}
                      </div>
                    {/if}
                  </aside>
                  <section>
                    {#if activeTerminalSession}
                      <div class="terminal-status">
                        <span>{activeTerminalSession.cwd}</span>
                        <strong>{terminalSessionStatus(activeTerminalSession)}</strong>
                      </div>
                      <pre class="terminal-output" aria-label="Terminal output">{terminalDisplayOutput(terminalOutput) || ' '}</pre>
                      <form class="terminal-input-row" on:submit|preventDefault={sendTerminalInput}>
                        <input aria-label="Terminal input" bind:value={terminalInput} disabled={activeTerminalSession.exited} placeholder="Type a command and press Enter" />
                        <button type="submit" disabled={activeTerminalSession.exited || !terminalInput.trim()}>Send</button>
                      </form>
                      {#if terminalError}
                        <p class="error-text">{terminalError}</p>
                      {/if}
                    {:else}
                      <div class="empty-state">No terminal session selected</div>
                      {#if terminalError}
                        <p class="error-text">{terminalError}</p>
                      {/if}
                    {/if}
                  </section>
                </div>
              </div>

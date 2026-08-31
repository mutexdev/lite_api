<script lang="ts">
  // US-036 — the Terminal tab of the Dev Tools panel.
  //
  // Last of the small DevTools tabs. Network remains and is the largest, with
  // its own nested request-details panel.
  import type { types } from '../../../../wailsjs/go/models'
  import IconButton from '../../ui/IconButton.svelte'

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
                    <!--
                      `.empty-state` with no modifier, in a 200px-wide rail.
                      The class carries 24px of padding and a dashed border, so
                      the sentence wrapped to three lines inside a box taller
                      than the session rows it was standing in for. Every other
                      empty state inside a DevTools sub-panel already uses
                      `compact` (see RequestDetailsPanel); this one had simply
                      never been given a modifier.
                    -->
                    {#if terminalSessions.length === 0}
                      <div class="empty-state compact">No active sessions</div>
                    {:else}
                      <div class="terminal-session-list">
                        {#each terminalSessions as session (session.id)}
                          <div class:active={terminalActiveSessionId === session.id} class="terminal-session-row">
                            <button type="button" class="terminal-session-button" on:click={() => selectTerminalSession(session.id)}>
                              <strong>{terminalSessionLabel(session)}</strong>
                              <span>{terminalSessionStatus(session)}</span>
                              <small>{session.cwd}</small>
                            </button>
                            <!--
                              `class="icon-button subtle"` — `.subtle` has no
                              rule anywhere in the stylesheet, the same
                              class-name-that-does-nothing that left 24 empty
                              states unstyled. Nothing is lost by dropping it,
                              because nothing was ever applied. The `×` it held
                              was a glyph in the text font sitting beside SVG
                              icons everywhere else in the app.
                            -->
                            <IconButton icon="close" label="Close terminal session" onclick={() => closeTerminalSession(session.id)} />
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
                      <!--
                        The tab-level "nothing here yet" state, which the
                        Console tab renders as a centred headline plus a line
                        explaining what would fill it. This one was a bare
                        sentence in a dashed box pinned to the top of the pane,
                        so the two DevTools tabs disagreed about what an empty
                        tab looks like — the only two places in the panel where
                        the question comes up.
                      -->
                      <div class="empty-state devtools-empty">
                        <strong>No terminal session selected</strong>
                        <span>Start a session to run commands alongside your requests</span>
                      </div>
                      {#if terminalError}
                        <p class="error-text">{terminalError}</p>
                      {/if}
                    {/if}
                  </section>
                </div>
              </div>

<script lang="ts">
  // The new-host approval prompt: the app-side face of the guard in
  // internal/core/mcp_guard.go.
  //
  // WHAT THIS DIALOG ACTUALLY IS. An agent asked LiteAPI to run a request, that
  // request resolves a secret variable, and the host it points at is one this
  // workspace has never sent that secret to. A Go goroutine is blocked on the
  // answer and denies after 60 seconds. So the dialog has one job: state which
  // credential is about to travel where, plainly enough that the user can say no
  // without reading it twice.
  //
  // Runes, not `export let`, for the reason DiscoveryModal.svelte documents: in
  // legacy mode a derived expression only re-runs for the variables it names,
  // and the countdown here is driven by a timer in the parent — a stale prompt
  // on screen is exactly the bug that mode produces.
  //
  // DENY IS NOT DECORATED. It sits first, at full size, as a plain button. The
  // temptation in a security prompt is to paint the safe answer red and make it
  // shout, and that is how users learn to click past it: an alarming dialog is a
  // dialog you dismiss. "Allow once" carries the .primary class because it is
  // the answer that unblocks the agent with the smallest possible grant, and
  // "Allow and remember" is deliberately the plainest of the three — it is the
  // only one whose consequences outlive this run.
  //
  // FOCUS GOES TO DENY, though. Visual default and keyboard default are not the
  // same decision: this dialog can appear while the user is mid-keystroke, and a
  // stray Return has to land on the recoverable answer. Denying costs a retry;
  // approving cannot be taken back. That is also the backend's own default, so
  // Escape, the timeout and the Return key all mean the same thing.
  //
  // There is no × in the header, unlike the app's other dialogs. Every way out
  // of this one has to be an answer, and a close affordance that sits apart from
  // the three buttons invites the reading "neither" — which does not exist.
  import Modal from '../../modals/Modal.svelte'
  import { approvalSecretsLabel, type McpApprovalPrompt } from '../../mcpSettings'

  type Props = {
    prompt: McpApprovalPrompt
    /** How many more prompts are waiting behind this one. */
    queued: number
    /** Whole seconds left before the backend's own deadline. */
    secondsRemaining: number
    busy: boolean
    onResolve: (id: string, approve: boolean, remember: boolean) => void | Promise<void>
  }

  let { prompt, queued, secondsRemaining, busy, onResolve }: Props = $props()

  const secrets = $derived(approvalSecretsLabel(prompt.secretNames))
  // The backend sends the request's name; a request saved without one, or a
  // transient tab, arrives blank. "A request" is honest about what is known.
  const requestLabel = $derived(prompt.requestName || 'A request')
  const hostLabel = $derived(prompt.host || 'an unrecognised host')
</script>

<Modal
  labelledBy="mcp-approval-title"
  describedBy="mcp-approval-body"
  onClose={() => onResolve(prompt.id, false, false)}
  testId="mcp-approval-dialog"
  {busy}
  closeOnBackdrop={false}
>
  <header>
    <h2 id="mcp-approval-title">Send a secret to a new host?</h2>
  </header>

  <div class="prompt-fields" id="mcp-approval-body">
    <p class="mcp-approval-lede">
      An AI tool asked to run <strong data-testid="mcp-approval-request">{requestLabel}</strong>.
      {#if secrets}
        Running it would send
        <strong data-testid="mcp-approval-secrets">{secrets}</strong>
        to <strong data-testid="mcp-approval-host">{hostLabel}</strong>, which no request in this
        workspace has sent {prompt.secretNames.length > 1 ? 'them' : 'it'} to before.
      {:else}
        Running it would send a secret to
        <strong data-testid="mcp-approval-host">{hostLabel}</strong>, which no request in this
        workspace has sent one to before.
      {/if}
    </p>

    <!-- Only when there is more than one. For a single secret the sentence
         above already names it, and repeating it in a box adds emphasis without
         adding information — which is how a prompt starts looking like an alarm
         to be dismissed rather than a question to be read. -->
    {#if prompt.secretNames.length > 1}
      <ul class="mcp-approval-secrets" aria-label="Secrets this would send">
        {#each prompt.secretNames as name (name)}
          <li><span class="mcp-approval-secret-name">{name}</span> → {hostLabel}</li>
        {/each}
      </ul>
    {/if}

    <p class="mcp-approval-note">
      Only the name is shown. LiteAPI never gives an AI tool the value of a secret — this asks
      whether the request it wants to run may carry that value to this host.
    </p>
  </div>

  <div class="button-row mcp-approval-actions">
    <button
      type="button"
      data-modal-autofocus
      data-testid="mcp-approval-deny"
      disabled={busy}
      onclick={() => onResolve(prompt.id, false, false)}
    >Deny</button>
    <button
      type="button"
      class="primary"
      data-testid="mcp-approval-allow-once"
      disabled={busy}
      onclick={() => onResolve(prompt.id, true, false)}
    >Allow once</button>
    <button
      type="button"
      data-testid="mcp-approval-allow-remember"
      disabled={busy}
      onclick={() => onResolve(prompt.id, true, true)}
    >Allow and remember</button>
  </div>

  <!-- No aria-live on the countdown. It changes every second, and a live region
       would make a screen reader interrupt itself sixty times while the user is
       trying to read the question. -->
  <p class="mcp-approval-footnote" data-testid="mcp-approval-footnote">
    <span>Denied automatically in {secondsRemaining}s.</span>
    {#if queued > 0}
      <span data-testid="mcp-approval-queued">
        {queued} more {queued === 1 ? 'request is' : 'requests are'} waiting.
      </span>
    {/if}
  </p>
</Modal>

<style>
  /* No `--name` is defined here: a custom property declared in one component
     resolves in none of the 12 theme blocks in style.css. Everything below
     reaches for tokens that already exist.

     Nothing styles the dialog box itself either — that element belongs to
     Modal.svelte, so a scoped rule for it would never match, and the shared
     .prompt-dialog width is what every other confirmation here is. */
  .mcp-approval-lede {
    margin: 0;
    color: var(--text);
    font-size: var(--font-size-13);
    line-height: 1.5;
  }

  /* The warning surface the app already uses for "this needs your attention,
     nothing has gone wrong yet" — the same pair as .unsaved-tabs-list. Not the
     danger palette: nothing has failed, and a red dialog is one people learn to
     click through. */
  .mcp-approval-secrets {
    display: grid;
    gap: var(--space-4);
    margin: 0;
    padding: var(--space-8) var(--space-10);
    list-style: none;
    border: 1px solid color-mix(in srgb, var(--warning-strong) 40%, transparent);
    border-radius: var(--radius-6);
    background: var(--warning-bg);
    color: var(--warning-text);
    font-size: var(--font-size-12);
    overflow-wrap: anywhere;
  }

  .mcp-approval-secret-name {
    font-family: var(--code-font-family);
    font-weight: 700;
  }

  .mcp-approval-note {
    margin: 0;
    max-width: 62ch;
    color: var(--muted-strong);
    font-size: var(--font-size-12);
    line-height: 1.5;
  }

  /* Deny sits at the left edge of the row rather than being pushed away from
     the allow buttons: it must be the easiest target to hit deliberately, and a
     button banished to the far side is one the hand has to travel to. */
  .mcp-approval-actions {
    justify-content: flex-start;
  }

  .mcp-approval-footnote {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-8);
    margin: var(--space-12) 0 0;
    color: var(--muted);
    font-size: var(--font-size-11);
  }
</style>

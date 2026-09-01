<script lang="ts">
  // US-036 — the OAuth2 authorization dialog, lifted out of App.svelte so its
  // markup is not in the initial chunk. Imported dynamically from inside the
  // {#if} that gates it.
  //
  // The last of the 29 dialogs. It hosts a sandboxed iframe, which is why it
  // was deferred to the end — but nothing about the extraction differs: the
  // iframe is plain markup and its {#key} wrapper moves with it, so the frame
  // still remounts when oauth2FrameKey changes.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  // Bindable: the callback URL field writes back to App.svelte.
  export let oauth2CallbackURLInput: string

  export let oauth2AuthorizationRequest: { authorizeUrl: string }
  export let oauth2FrameKey: number
  export let oauth2CallbackMessage: string
  export let openOAuth2AuthorizationInSystemBrowser: () => void
  export let submitOAuth2CallbackURL: () => void
  export let closeOAuth2Authorization: () => void
</script>

<Modal
    labelledBy="oauth2-auth-title"
    onClose={closeOAuth2Authorization}
    dialogClass="prompt-dialog oauth2-auth-dialog"
    backdropClass="prompt-backdrop oauth2-auth-backdrop"
    closeOnBackdrop={false}
    size="xlarge"
  >
      <header>
        <h2 id="oauth2-auth-title">OAuth2 Authorization</h2>
        <IconButton icon="close" label="Close" onclick={closeOAuth2Authorization} />
      </header>
      <div class="oauth2-auth-frame-wrap">
        {#key oauth2FrameKey}
          <iframe
            class="oauth2-auth-frame"
            title="OAuth2 Authorization"
            src={oauth2AuthorizationRequest.authorizeUrl}
            sandbox="allow-forms allow-modals allow-popups allow-same-origin allow-scripts"
          ></iframe>
        {/key}
      </div>
      <!--
        A <form>, so Enter in the callback field submits it.

        This was the one text-entry dialog in the app where Enter did nothing:
        the user pastes a redirect URL they have just copied out of a browser,
        presses Return the way they do in every Rename, Clone and New dialog
        here, and nothing happens — the only way on is to find the mouse. Sixteen
        other dialogs already got this for free by wrapping their fields in a
        form; this one wrapped its fields in a <div>.

        The element changes and nothing else does: .oauth2-auth-controls styles
        the grid by class, not by tag, and the dialog's own grid-template-rows
        still sees four children. "Open in System Browser" stays type="button" —
        it must not become the implicit submit.
      -->
      <form class="oauth2-auth-controls" on:submit|preventDefault={submitOAuth2CallbackURL}>
        <button type="button" on:click={openOAuth2AuthorizationInSystemBrowser}>Open in System Browser</button>
        <label>
          <span>Callback URL</span>
          <input
            aria-label="OAuth2 callback URL"
            value={oauth2CallbackURLInput}
            on:input={(event) => (oauth2CallbackURLInput = event.currentTarget.value)}
          />
        </label>
        <button type="submit" class="primary" disabled={!oauth2CallbackURLInput.trim()}>Submit Callback</button>
      </form>
      {#if oauth2CallbackMessage}
        <p class="oauth2-auth-message">{oauth2CallbackMessage}</p>
      {/if}
</Modal>

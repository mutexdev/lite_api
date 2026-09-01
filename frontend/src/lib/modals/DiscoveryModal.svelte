<script lang="ts">
  // The first-run offer (US-064).
  //
  // Everything that reads another application's data, or changes what this one
  // trusts, starts unticked. The user ticks what they want; nothing here
  // happens because a dialog appeared.
  //
  // The proxy section is informational on purpose. There is nothing to adopt --
  // "system" is already the default mode and already reads the machine's
  // settings -- and someone whose requests fail behind a corporate proxy needs
  // to be told that before they go looking for a switch.

  import Modal from './Modal.svelte'
  import IconButton from '../ui/IconButton.svelte'
  import type { core } from '../../../wailsjs/go/models'

  // Runes, not `export let`. In legacy mode the compiler tracks the variables
  // an expression names, so `disabled={chosenFor(...).length === 0}` never
  // re-evaluated when the tick box that feeds chosenFor changed -- the Import
  // button stayed disabled after selecting a collection. Found by driving the
  // real component in a browser; no type check or unit test would have said so.
  type Props = {
    report: core.DiscoveryReport
    collectionsByClient?: Record<string, core.DiscoveredCollection[]>
    busy?: boolean
    error?: string
    /** Reads one client's collections. Called when its section is expanded. */
    onLoadCollections: (client: string) => void | Promise<void>
    onImport: (client: string, names: string[]) => void | Promise<void>
    onAdoptCA: (path: string) => void | Promise<void>
    onClose: () => void
  }

  let {
    report,
    collectionsByClient = {},
    busy = false,
    error = '',
    onLoadCollections,
    onImport,
    onAdoptCA,
    onClose
  }: Props = $props()

  let expanded = $state<Record<string, boolean>>({})
  let selected = $state<Record<string, boolean>>({})


  function toggleClient(client: string) {
    expanded = { ...expanded, [client]: !expanded[client] }
    if (expanded[client] && !collectionsByClient[client]) void onLoadCollections(client)
  }

  // Returns ids, not names. Two of another client's collections can share a
  // name -- two Insomnia workspaces both left at the default is the ordinary
  // case -- and selecting by name meant one tick box drove both, importing a
  // collection the user never chose.
  function chosenFor(client: string): string[] {
    return (collectionsByClient[client] ?? [])
      .map((entry) => entry.id)
      .filter((id) => selected[id])
  }

  function formatExpiry(value: string): string {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleDateString()
  }
</script>

<Modal
  labelledBy="discovery-title"
  describedBy="discovery-description"
  onClose={onClose}
  testId="discovery-modal"
  busy={busy}
  size="medium"
>
  <header>
    <h2 id="discovery-title">Bring Your Setup Across</h2>
    <IconButton icon="close" label="Close" onclick={onClose} />
  </header>

  <p id="discovery-description">
    This looks at what is installed on this machine. Nothing is read or changed until you choose it.
  </p>

  {#if error}<p class="import-row-error" role="alert">{error}</p>{/if}

  {#if report.installations.length > 0}
    <section class="discovery-section">
      <h3>API Clients</h3>
      {#each report.installations as installation (installation.client)}
        <article class="discovery-client">
          <header>
            <strong>{installation.displayName}</strong>
            {#if installation.readable}
              <button type="button" data-testid={`discovery-expand-${installation.client}`} onclick={() => toggleClient(installation.client)}>
                {expanded[installation.client] ? 'Hide Collections' : 'Show Collections'}
              </button>
            {/if}
          </header>
          <p class="discovery-path">{installation.path}</p>

          {#if !installation.readable}
            <p class="discovery-guidance">{installation.guidance}</p>
          {:else if expanded[installation.client]}
            {#if !collectionsByClient[installation.client]}
              <p class="discovery-guidance">Reading…</p>
            {:else if collectionsByClient[installation.client].length === 0}
              <p class="discovery-guidance">No collections found.</p>
            {:else}
              <ul class="discovery-collections">
                {#each collectionsByClient[installation.client] as entry (entry.id)}
                  <li>
                    <label>
                      <input
                        type="checkbox"
                        checked={selected[entry.id] ?? false}
                        onchange={(event) =>
                          (selected = { ...selected, [entry.id]: event.currentTarget.checked })}
                      />
                      {entry.name}
                      <span class="discovery-count">{entry.requestCount} request{entry.requestCount === 1 ? '' : 's'}</span>
                    </label>
                    {#each entry.warnings ?? [] as warning, warningIndex (warningIndex)}
                      <p class="import-row-warning">{warning}</p>
                    {/each}
                  </li>
                {/each}
              </ul>
              <button
                class="primary"
                type="button"
                data-testid={`discovery-import-${installation.client}`}
                disabled={busy || chosenFor(installation.client).length === 0}
                onclick={() => void onImport(installation.client, chosenFor(installation.client))}
              >
                Import Selected
              </button>
            {/if}
          {/if}
        </article>
      {/each}
    </section>
  {/if}

  {#if report.caCertificates.length > 0}
    <section class="discovery-section">
      <h3>Certificate Authority</h3>
      <p class="discovery-guidance">
        Check the fingerprint against what your administrator gave you before trusting one. Your system's
        existing certificates are kept either way.
      </p>
      {#each report.caCertificates as candidate (candidate.path)}
        <article class="discovery-ca">
          <strong>{candidate.subject}</strong>
          <p class="discovery-path">{candidate.path}</p>
          <p class="discovery-fingerprint">SHA-256 {candidate.fingerprint}</p>
          {#if candidate.alreadyTrusted}
            <p class="discovery-guidance">Your system already trusts this, so there is nothing to do.</p>
          {:else if candidate.expired}
            <p class="discovery-guidance">Expired on {formatExpiry(candidate.notAfter)}; trusting it would not help.</p>
          {:else}
            <button type="button" data-testid="discovery-adopt-ca" disabled={busy} onclick={() => void onAdoptCA(candidate.path)}>
              Trust This Authority
            </button>
          {/if}
        </article>
      {/each}
    </section>
  {/if}

  {#if report.proxy.detected}
    <section class="discovery-section">
      <h3>Proxy</h3>
      <p class="discovery-guidance">
        This machine is configured to reach the internet through <code>{report.proxy.description}</code>.
        {#if report.proxy.inUse}
          Requests already use it.
        {:else}
          Requests are not using it, because the proxy mode is not set to System. Change that in Preferences → Proxy.
        {/if}
      </p>
    </section>
  {/if}

  <div class="button-row">
    <button type="button" data-testid="discovery-dismiss" onclick={onClose}>Not Now</button>
  </div>
</Modal>

<style>
  /* TOKENS, NOT NEW NUMBERS. This block was the last local <style> in the modals
     tree still inventing its own values: 0.9rem, 0.8rem and 0.75rem for type,
     bare px for spacing, and `var(--border, rgba(0, 0, 0, 0.12))` for the rule
     above each section.

     None of the three type sizes was on the scale. 0.9rem is 14.4px and 0.8rem
     is 12.8px, so section headings and guidance text sat a fraction off every
     other heading and every other caption in the app — the kind of difference
     nobody can name and everybody can see, and precisely the "different app in
     each section" complaint this campaign started from. They are 14px and 13px
     now, the nearest steps on the closed scale in style.css.

     The border fallback is gone as well: --border is defined in :root and in
     all 12 theme blocks, so the rgba() was dead weight that would have painted
     a light-mode grey if it ever fired inside a dark theme.

     The two opacity rules became colours for the same reason. Faded text is a
     value nothing else in the app derives its greys from; --muted is what
     McpApprovalModal and every other dialog reach for, and it is redefined per
     theme where an opacity is not. */
  .discovery-section {
    margin-top: var(--space-12);
    border-top: 1px solid var(--border);
    padding-top: var(--space-12);
  }

  .discovery-section h3 {
    margin: 0 0 var(--space-8);
    font-size: var(--font-size-14);
  }

  .discovery-client,
  .discovery-ca {
    margin-bottom: var(--space-10);
  }

  .discovery-client header {
    display: flex;
    align-items: center;
    gap: var(--space-8);
  }

  .discovery-path,
  .discovery-fingerprint {
    margin: var(--space-2) 0;
    color: var(--muted);
    font-size: var(--font-size-12);
    word-break: break-all;
  }

  .discovery-guidance {
    margin: var(--space-4) 0;
    font-size: var(--font-size-13);
    max-width: 68ch;
  }

  .discovery-collections {
    list-style: none;
    margin: var(--space-6) 0;
    padding: 0;
  }

  .discovery-count {
    margin-left: var(--space-6);
    color: var(--muted);
    font-size: var(--font-size-12);
  }
</style>

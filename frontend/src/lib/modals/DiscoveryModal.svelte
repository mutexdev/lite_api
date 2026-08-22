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

  const selectionKey = (client: string, name: string) => `${client}:${name}`

  function toggleClient(client: string) {
    expanded = { ...expanded, [client]: !expanded[client] }
    if (expanded[client] && !collectionsByClient[client]) void onLoadCollections(client)
  }

  function chosenFor(client: string): string[] {
    return (collectionsByClient[client] ?? [])
      .map((entry) => entry.name)
      .filter((name) => selected[selectionKey(client, name)])
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
  dialogClass="prompt-dialog discovery-dialog"
  testId="discovery-modal"
  busy={busy}
>
  <header>
    <h2 id="discovery-title">Bring your setup across</h2>
    <button type="button" class="icon-button" title="Close" onclick={onClose}>x</button>
  </header>

  <p id="discovery-description">
    This looks at what is installed on this machine. Nothing is read or changed until you choose it.
  </p>

  {#if error}<p class="import-row-error" role="alert">{error}</p>{/if}

  {#if report.installations.length > 0}
    <section class="discovery-section">
      <h3>API clients</h3>
      {#each report.installations as installation (installation.client)}
        <article class="discovery-client">
          <header>
            <strong>{installation.displayName}</strong>
            {#if installation.readable}
              <button type="button" data-testid={`discovery-expand-${installation.client}`} onclick={() => toggleClient(installation.client)}>
                {expanded[installation.client] ? 'Hide collections' : 'Show collections'}
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
                {#each collectionsByClient[installation.client] as entry, index (index)}
                  <li>
                    <label>
                      <input
                        type="checkbox"
                        checked={selected[selectionKey(installation.client, entry.name)] ?? false}
                        onchange={(event) =>
                          (selected = {
                            ...selected,
                            [selectionKey(installation.client, entry.name)]: event.currentTarget.checked
                          })}
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
                Import selected
              </button>
            {/if}
          {/if}
        </article>
      {/each}
    </section>
  {/if}

  {#if report.caCertificates.length > 0}
    <section class="discovery-section">
      <h3>Certificate authority</h3>
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
              Trust this authority
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
    <button type="button" data-testid="discovery-dismiss" onclick={onClose}>Not now</button>
  </div>
</Modal>

<style>
  .discovery-section {
    margin-top: 12px;
    border-top: 1px solid var(--border, rgba(0, 0, 0, 0.12));
    padding-top: 12px;
  }

  .discovery-section h3 {
    margin: 0 0 8px;
    font-size: 0.9rem;
  }

  .discovery-client,
  .discovery-ca {
    margin-bottom: 10px;
  }

  .discovery-client header {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .discovery-path,
  .discovery-fingerprint {
    margin: 2px 0;
    font-size: 0.75rem;
    opacity: 0.7;
    word-break: break-all;
  }

  .discovery-guidance {
    margin: 4px 0;
    font-size: 0.8rem;
    max-width: 68ch;
  }

  .discovery-collections {
    list-style: none;
    margin: 6px 0;
    padding: 0;
  }

  .discovery-count {
    opacity: 0.65;
    font-size: 0.75rem;
    margin-left: 6px;
  }
</style>

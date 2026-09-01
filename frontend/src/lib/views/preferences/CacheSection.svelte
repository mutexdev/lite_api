<script lang="ts">
  // US-036 — the Cache section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  //
  // WHAT WENT WRONG HERE SPECIFICALLY. These two settings were bordered cards —
  // a bold title, a paragraph and a checkbox floating in a middle column beside
  // an action button — while the identical kind of setting in General was a
  // plain checkbox with its text next to it. A card is a container, and a
  // container says "this is a different kind of thing". They are not different
  // kinds of thing: they are two booleans with an explanation and a button, so
  // they are now two SettingRows with a description and a button, and the card
  // is gone.
  import type { types } from '../../../../wailsjs/go/models'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  type Props = {
    state: types.AppState
    fileCacheSize: number | undefined
    formatRuntimeBytes: (bytes: number) => string
    updateFileCache: (enabled: boolean) => Promise<void> | void
    updateSSLSessionCache: (enabled: boolean) => Promise<void> | void
    // Widened from `() => void`. Both of App.svelte's handlers are `async` and
    // always were — the narrower type simply discarded the promise, which is
    // why there was nothing here to wait on. Widening accepts exactly what the
    // call site already passes, so no App.svelte change is needed for it.
    clearFileCache: () => Promise<void> | void
    clearSSLSessionCache: () => Promise<void> | void
  }

  /*
   * The `state` prop is destructured under a different LOCAL name on purpose.
   * Svelte reads `$state` as a store subscription to a variable called `state`
   * whenever one is in scope, so with a prop of that name the rune below does
   * not compile. Renaming the binding rather than the prop keeps App.svelte's
   * `state={appState}` call site untouched.
   */
  let {
    state: appState,
    fileCacheSize,
    formatRuntimeBytes,
    updateFileCache,
    updateSSLSessionCache,
    clearFileCache,
    clearSSLSessionCache,
  }: Props = $props()

  const fileCacheSizeLabel = $derived(fileCacheSize === undefined ? '-' : formatRuntimeBytes(fileCacheSize))

  /*
   * A6-11, the Preferences twin. Every control in this panel writes instantly
   * and none of them said so, which is fine for a value that lands in the same
   * frame and wrong for these four:
   *
   *   - the two toggles save the preference and THEN go back to Go, one to
   *     re-measure the cache directory and one to tear the TLS session cache
   *     down. Until that returns, `state.preferences` still holds the old
   *     value, so the checkbox the user just clicked renders unchecked again
   *     and the panel looks like it refused the click.
   *   - the two Clear buttons walk a directory. On a large cache that is
   *     visibly slow, and the only evidence anything happened was the size
   *     readout eventually changing to 0 B.
   *
   * Tracked per action rather than as one section-wide flag: clearing the file
   * cache must not grey out the SSL row, which is a different setting that is
   * not busy.
   */
  let busy = $state({ file: false, ssl: false, clearFile: false, clearSsl: false })

  async function run(key: keyof typeof busy, action: () => Promise<void> | void) {
    if (busy[key]) return
    busy[key] = true
    try {
      await action()
    } finally {
      // `finally`, so a rejected backend call clears the flag too. A failed
      // clear that left the button reading "Clearing…" forever would be a
      // worse lie than the silence this replaces.
      busy[key] = false
    }
  }
</script>

<SettingSection title="Cache">
  <SettingRow
    label="File cache"
    badge="Beta"
    description="Loads your workspace faster by caching opened collections. Clearing it won't affect your original files."
    checked={appState.preferences.cache?.file?.enabled ?? false}
    data-testid="cache.file.enabled"
    busy={busy.file}
    onCheckedChange={(value) => void run('file', () => updateFileCache(value))}
  >
    {#snippet control()}
      <!--
        The measured size sits with the button that acts on it, because "Clear
        cache" with no number beside it gives no reason to press it. It is the
        one piece of the old card worth keeping.
      -->
      <span class="cache-size-readout" data-testid="file-cache-size">{fileCacheSizeLabel}</span>
      <button
        type="button"
        data-testid="file-cache-clear-btn"
        disabled={!fileCacheSize || busy.clearFile}
        onclick={() => void run('clearFile', clearFileCache)}
      >{busy.clearFile ? 'Clearing…' : 'Clear cache'}</button>
    {/snippet}
  </SettingRow>

  <SettingRow
    label="SSL session cache"
    description="Reuses TLS sessions and connections across requests for faster handshakes."
    checked={appState.preferences.cache?.sslSession?.enabled ?? false}
    data-testid="sslSession.enabled"
    busy={busy.ssl}
    onCheckedChange={(value) => void run('ssl', () => updateSSLSessionCache(value))}
  >
    {#snippet control()}
      <button
        type="button"
        data-testid="ssl-session-clear-btn"
        disabled={busy.clearSsl}
        onclick={() => void run('clearSsl', clearSSLSessionCache)}
      >{busy.clearSsl ? 'Clearing…' : 'Clear cache'}</button>
    {/snippet}
  </SettingRow>
</SettingSection>

<style>
  /*
    Tabular figures so the size does not jitter horizontally as it changes while
    the panel is left open.
  */
  .cache-size-readout {
    color: var(--muted);
    font-size: var(--font-size-12);
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
</style>

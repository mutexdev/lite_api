<script module lang="ts">
  import type { ComponentProps } from 'svelte'
  import type AppearanceSection from './AppearanceSection.svelte'
  import type CacheSection from './CacheSection.svelte'
  import type DisplaySection from './DisplaySection.svelte'
  import type GeneralSection from './GeneralSection.svelte'
  import type KeybindingsSection from './KeybindingsSection.svelte'
  import type McpSection from './McpSection.svelte'
  import type OAuth2Section from './OAuth2Section.svelte'
  import type ProxySection from './ProxySection.svelte'

  /**
   * The Preferences panel: its header, its section index, and the one place its
   * eight sections are loaded.
   *
   * ── A7-06, THE HALF THE AUDIT GOT RIGHT ──────────────────────────────────
   *
   * There was no navigation. Eight sections rendered as one uninterrupted
   * scroll from Appearance down to MCP, so reaching Proxy meant scrolling past
   * five other sections with no index, no anchors and no way back. That is the
   * finding, and the sticky strip below is the fix.
   *
   * ── A7-06, THE HALF THE AUDIT GOT WRONG ──────────────────────────────────
   *
   * The audit described "8 sequential dynamic-import waterfalls". There was no
   * waterfall, and it was not sequential. Both halves were checked rather than
   * assumed:
   *
   *   - NOT SEQUENTIAL. `{#await import(...)}` compiles to `$.await(node, () =>
   *     import(...), …)`; `$.await` calls that thunk inside `block()`, and
   *     `block()` creates a BLOCK_EFFECT that runs as the parent fragment is
   *     built. All eight `import()` calls were therefore issued in one
   *     synchronous pass, in parallel.
   *   - NOT A WATERFALL EITHER. Each section chunk statically imports
   *     SettingRow/SettingSection, which the bundler emits as a shared chunk —
   *     but Vite compiles every dynamic import to `__vitePreload(…,
   *     __vite__mapDeps([…]))` carrying the full TRANSITIVE dep list, so the
   *     shared chunk is preloaded in the same tick as the section that needs
   *     it. Verified in the built output, not inferred.
   *
   * What IS true is the audit's own phrase "repopulates from a blank state".
   * Eight await blocks settle independently, so the stack painted in up to
   * eight passes in whatever order the chunks arrived, each insertion moving
   * the scroll height under the pointer — and because each block re-enters its
   * pending branch on every mount, the panel flashed empty on EVERY reopen, not
   * just the first, even though the modules were long since resolved.
   *
   * Loading once at module scope closes both:
   *
   *   - ONE `Promise.all` and one await block, so the eight sections arrive as
   *     one result and the stack paints in a single pass.
   *   - `settled` caches the components on the MODULE, not the instance, so the
   *     second and every later open reads them synchronously — no promise, no
   *     await block, no empty frame.
   */

  /** The section index, in the order the panel stacks them. */
  export const PREFERENCE_SECTIONS = [
    { id: 'appearance', label: 'Appearance' },
    { id: 'display', label: 'Display' },
    { id: 'general', label: 'General' },
    { id: 'oauth2', label: 'OAuth2' },
    { id: 'keybindings', label: 'Keybindings' },
    { id: 'proxy', label: 'Proxy' },
    { id: 'cache', label: 'Cache' },
    { id: 'mcp', label: 'AI access' },
  ] as const

  export type PreferenceSectionId = (typeof PREFERENCE_SECTIONS)[number]['id']

  /** The id of the scroll anchor wrapping one section. */
  export function preferenceSectionAnchorId(id: PreferenceSectionId): string {
    return `preferences-section-${id}`
  }

  type LoadedSections = {
    Appearance: typeof AppearanceSection
    Display: typeof DisplaySection
    General: typeof GeneralSection
    OAuth2: typeof OAuth2Section
    Keybindings: typeof KeybindingsSection
    Proxy: typeof ProxySection
    Cache: typeof CacheSection
    Mcp: typeof McpSection
  }

  let pending: Promise<LoadedSections> | undefined
  let settled: LoadedSections | undefined

  /**
   * Resolves every section component, once per session.
   *
   * `??=` rather than a re-entrancy flag: two mounts racing in the same tick
   * must share ONE promise, or the second gets its own eight imports and its
   * own settle, which is the multi-pass paint back again.
   *
   * The shared row primitives are deliberately NOT listed here. Adding them
   * looks like it would flatten the import graph — and does nothing, because
   * Vite already emits each section's transitive dependencies in its preload
   * list. Two imports that buy nothing, kept because they read as an
   * optimisation, are how this codebase acquired its dead rules.
   */
  export function loadPreferenceSections(): Promise<LoadedSections> {
    pending ??= Promise.all([
      import('./AppearanceSection.svelte'),
      import('./DisplaySection.svelte'),
      import('./GeneralSection.svelte'),
      import('./OAuth2Section.svelte'),
      import('./KeybindingsSection.svelte'),
      import('./ProxySection.svelte'),
      import('./CacheSection.svelte'),
      import('./McpSection.svelte'),
    ]).then(([appearance, display, general, oauth2, keybindings, proxy, cache, mcp]) => {
      settled = {
        Appearance: appearance.default,
        Display: display.default,
        General: general.default,
        OAuth2: oauth2.default,
        Keybindings: keybindings.default,
        Proxy: proxy.default,
        Cache: cache.default,
        Mcp: mcp.default,
      }
      return settled
    })
    return pending
  }

  /** The already-resolved components, or undefined on the very first open. */
  export function loadedPreferenceSections(): LoadedSections | undefined {
    return settled
  }
</script>

<script lang="ts">
  type Props = {
    /**
     * The eight sections' props, one bag each, typed as the section itself
     * declares them.
     *
     * WHY BAGS AND NOT ~55 FLAT PROPS. The panel forwards every one of them
     * untouched, and restating them here would be a second copy of eight prop
     * lists to keep in step with the sections — the same duplication that
     * produced most of the A7 findings in the first place. `ComponentProps`
     * makes each bag track its section's declaration automatically, so adding a
     * setting is an edit to one file, not three. Spreading a single flat bag
     * into all eight was the other option and is worse: Svelte warns at runtime
     * for every prop a component was handed and does not declare, which here
     * would be roughly fifty warnings per section on every open.
     */
    appearance: ComponentProps<typeof AppearanceSection>
    display: ComponentProps<typeof DisplaySection>
    general: ComponentProps<typeof GeneralSection>
    oauth2: ComponentProps<typeof OAuth2Section>
    keybindings: ComponentProps<typeof KeybindingsSection>
    proxy: ComponentProps<typeof ProxySection>
    cache: ComponentProps<typeof CacheSection>
    mcp: ComponentProps<typeof McpSection>

    /**
     * A7-08. The three facts the subtitle summarises, resolved by the caller
     * because two of them are formatted by helpers that live in App.svelte.
     *
     * WHAT WENT WRONG. The subtitle read `Theme {mode} · Proxy {label}` — the
     * two settings that happened to be wired up first. Theme is the one piece
     * of state a user can already see (they are looking at it), and proxy is
     * off for almost everyone. The panel's one genuinely non-obvious, genuinely
     * consequential state — whether AI tools are allowed to reach into this
     * workspace — was the one thing it did not say.
     *
     * `mcpEnabled` is deliberately the stored preference and the subtitle
     * deliberately says "enabled", not "running". Whether the listener actually
     * came up is only known to GetMCPStatus, which the MCP section fetches on
     * mount; a header that claimed "AI access on" while the port was taken
     * would be exactly the lie that section's status line exists to prevent.
     */
    mcpEnabled: boolean
    themeModeLabel: string
    proxyLabel: string
  }

  let { appearance, display, general, oauth2, keybindings, proxy, cache, mcp, mcpEnabled, themeModeLabel, proxyLabel }: Props = $props()

  // Read once, at construction: when the components are already in hand — which
  // is every open after the first — the stack renders synchronously and the
  // returning user sees the panel complete in the frame it mounts, with no
  // await block and no pending state in between.
  const alreadyLoaded = loadedPreferenceSections()

  let panel = $state<HTMLElement | undefined>(undefined)
  let nav = $state<HTMLElement | undefined>(undefined)
  let currentSection = $state<string>(PREFERENCE_SECTIONS[0].id)
  let scrollFrame = 0

  function anchorFor(id: string): HTMLElement | null {
    return panel?.querySelector<HTMLElement>(`#${CSS.escape(preferenceSectionAnchorId(id as PreferenceSectionId))}`) ?? null
  }

  function goToSection(id: string) {
    const target = anchorFor(id)
    if (!target) return
    // `scroll-margin-top` on the anchor is what keeps the heading clear of the
    // sticky index; without it scrollIntoView parks the title underneath it.
    target.scrollIntoView({ block: 'start', behavior: 'smooth' })
    currentSection = id
    // Moving focus as well as the viewport: a keyboard user who activates an
    // index entry and then presses Tab should continue inside the section they
    // asked for, not back at the next index entry.
    const focusable = target.querySelector<HTMLElement>('input, select, button, summary, [tabindex]')
    focusable?.focus({ preventScroll: true })
  }

  /**
   * Marks the section the reader is in.
   *
   * Measured against the bottom of the sticky index rather than the top of the
   * viewport, because everything above that line is covered. Read in a rAF and
   * only one at a time: this runs on every scroll event, and `getBoundingClientRect`
   * forces layout, so an unthrottled version would be a synchronous reflow per
   * wheel tick down a long panel.
   */
  function syncCurrentSection() {
    if (scrollFrame) return
    scrollFrame = requestAnimationFrame(() => {
      scrollFrame = 0
      if (!panel) return
      const line = (nav?.getBoundingClientRect().bottom ?? panel.getBoundingClientRect().top) + 1
      let seen = PREFERENCE_SECTIONS[0].id as string
      for (const section of PREFERENCE_SECTIONS) {
        const anchor = anchorFor(section.id)
        if (anchor && anchor.getBoundingClientRect().top <= line) seen = section.id
      }
      currentSection = seen
    })
  }
</script>

<section class="panel preferences-panel" bind:this={panel} onscroll={syncCurrentSection}>
  <header class="panel-header">
    <div>
      <h2>Preferences</h2>
      <p class="panel-subtitle" data-testid="preferences-subtitle">
        AI access {mcpEnabled ? 'enabled' : 'off'} · Theme {themeModeLabel} · Proxy {proxyLabel}
      </p>
    </div>
  </header>

  <!--
    A7-06's navigation. A sticky strip above the column rather than a rail
    beside it, on purpose: this panel has already been fixed once for a 1024×768
    overflow, and a left rail takes 180-200px out of the content column at
    exactly the widths where that bug lived. A strip costs height, which this
    panel has, instead of width, which it does not.
  -->
  <nav class="preferences-nav" aria-label="Preferences sections" bind:this={nav}>
    {#each PREFERENCE_SECTIONS as section (section.id)}
      <button
        type="button"
        class:current={currentSection === section.id}
        aria-current={currentSection === section.id ? 'true' : undefined}
        data-testid={`preferences-nav-${section.id}`}
        onclick={() => goToSection(section.id)}
      >{section.label}</button>
    {/each}
  </nav>

  <div class="settings-stack">
    {#if alreadyLoaded}
      {@render stack(alreadyLoaded)}
    {:else}
      {#await loadPreferenceSections()}
        <p class="preferences-loading" aria-live="polite">Loading preferences…</p>
      {:then loaded}
        {@render stack(loaded)}
      {/await}
    {/if}
  </div>
</section>

{#snippet stack(loaded: NonNullable<ReturnType<typeof loadedPreferenceSections>>)}
  <!--
    Each section sits in an anchor wrapper so the index has something to scroll
    to. The wrapper restates the `min-width: 0; max-width: 100%` that
    `style.css`'s `.preferences-panel .settings-stack > section` gave the
    sections when they were direct children — without it a long path or pairing
    command widens the column and the horizontal scroll comes back — and carries
    the stack's own gap, because the MCP section renders two sibling `<section>`
    elements inside one anchor.
  -->
  <div class="preferences-section" id={preferenceSectionAnchorId('appearance')}>
    <loaded.Appearance {...appearance} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('display')}>
    <loaded.Display {...display} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('general')}>
    <loaded.General {...general} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('oauth2')}>
    <loaded.OAuth2 {...oauth2} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('keybindings')}>
    <loaded.Keybindings {...keybindings} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('proxy')}>
    <loaded.Proxy {...proxy} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('cache')}>
    <loaded.Cache {...cache} />
  </div>
  <div class="preferences-section" id={preferenceSectionAnchorId('mcp')}>
    <loaded.Mcp {...mcp} />
  </div>
{/snippet}

<style>
  /*
    Sticky against `.panel`, which is the scroll container — it is the element
    carrying `overflow: auto` in style.css. Negative inline margins and matching
    padding let the strip's background reach the panel's edges while its buttons
    stay on the content's left edge, so it reads as a bar rather than as a row
    of chips floating in the middle of the page.
  */
  .preferences-nav {
    position: sticky;
    z-index: 2;
    top: calc(-1 * var(--space-16));
    display: flex;
    gap: var(--space-3);
    overflow-x: auto;
    margin: calc(-1 * var(--space-8)) calc(-1 * var(--space-16)) var(--space-14);
    padding: var(--space-6) var(--space-16);
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface);
    scrollbar-width: thin;
  }

  .preferences-nav button {
    flex: 0 0 auto;
    min-height: 26px;
    padding: var(--space-3) var(--space-8);
    border-color: transparent;
    background: transparent;
    color: var(--muted-strong);
    font-size: var(--font-size-12);
    font-weight: 800;
    white-space: nowrap;
  }

  .preferences-nav button:hover {
    border-color: var(--border);
    background: var(--surface-soft);
    color: var(--text);
  }

  /*
    The current entry is marked by fill AND colour, not colour alone — the same
    rule IconButton's pressed state follows, and for the same reason.
  */
  .preferences-nav button.current {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--accent-strong);
  }

  .preferences-section {
    display: grid;
    gap: var(--space-14);
    min-width: 0;
    max-width: 100%;
    /* Clears the sticky index, which would otherwise cover the heading the
       reader just asked to be taken to. */
    scroll-margin-top: var(--space-32);
  }

  .preferences-loading {
    margin: 0;
    color: var(--muted);
    font-size: var(--font-size-12);
  }
</style>

<script lang="ts">
  // US-025 — the shared modal shell.
  //
  // WHAT MEASUREMENT FOUND, and why this component exists in this form. The
  // story asks that focus traps, inert app-shell blocking, returnFocus with
  // preventScroll, aria-modal and aria-labelledby remain "intact" through the
  // extraction. Measured against the running app before any change, 27 of the
  // 29 dialogs had NO keydown handler at all, and `inert` was applied to the
  // app shell for exactly two of them. With the notifications dialog open:
  //
  //     app shell inert ................ false
  //     focusable controls behind it ... 41
  //     focus escaped the modal ........ true  (landed on "+ New ⌘N")
  //     background clickable ........... true
  //     Escape closed it ............... false
  //
  // So there was almost nothing to keep intact. Every one of those dialogs
  // declares aria-modal="true", which tells assistive technology the rest of
  // the page is inert — a promise the app did not keep. Extraction alone would
  // have carried that bug into 29 tidy new files.
  //
  // This component therefore IMPLEMENTS the four guarantees rather than
  // preserving them, in one place where they can be tested once:
  //
  //   1. inert on the app shell while open, so background controls cannot be
  //      focused, clicked, or reached by a screen reader.
  //   2. A focus trap: Tab and Shift+Tab wrap within the dialog.
  //   3. Escape closes.
  //   4. Focus returns to the element that opened it, with preventScroll so the
  //      page does not jump.

  import { onDestroy, onMount, tick } from 'svelte'

  /** Wired to aria-labelledby; must match the id of the dialog's heading. */
  export let labelledBy: string
  export let onClose: () => void
  /** Extra classes for the dialog box, e.g. "prompt-dialog code-generator-dialog". */
  export let dialogClass = 'prompt-dialog'
  export let backdropClass = 'prompt-backdrop'
  export let testId: string | undefined = undefined
  /** Set false for dialogs whose dismissal must be deliberate (destructive confirms). */
  export let closeOnBackdrop = true
  /** id of a description element, for dialogs that carry explanatory prose. */
  export let describedBy: string | undefined = undefined
  /** Mirrors aria-busy for dialogs that run an async decision in place. */
  export let busy: boolean | undefined = undefined
  /**
   * How wide the dialog is, from a four-step scale.
   *
   * WHY THIS IS A PROP AND NOT A CLASS. Width was the one part of the box
   * treatment no dialog inherited: `.prompt-dialog` gives 460px and every
   * dialog that wanted anything else declared its own rule in style.css. The
   * audit counted twelve distinct pixel values between 420 and 1120, and they
   * were not twelve considered decisions — Info (a two-row table) was 560,
   * wider than Rename Folder's whole form at 460, and the command palette's
   * single search input landed on exactly 720 by coincidence with Share
   * Collection's card grid. Every new dialog picked a thirteenth number by
   * copying whichever neighbour it was cloned from.
   *
   * The scale is the audit's: 420 / 460 / 720 / 1060. A caller names a step; it
   * cannot name a pixel.
   *
   * DELIBERATELY OPT-IN, defaulting to undefined rather than to 'medium'. The
   * scoped rules below outrank a plain `.workspace-picker-dialog` in style.css
   * — one class each, but Svelte's scoping adds a second — so a default of
   * 'medium' would silently narrow every dialog that has not migrated yet from
   * whatever its own rule says to 460px. `.workspace-picker-dialog` is 580px
   * and its option rows need it. Absent this prop, nothing changes.
   */
  export let size: 'small' | 'medium' | 'large' | 'xlarge' | undefined = undefined

  const focusableSelector = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled]):not([type="hidden"])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
  ].join(',')

  let dialog: HTMLElement | null = null
  let returnFocus: HTMLElement | null = null
  let appShell: HTMLElement | null = null

  function focusableItems(): HTMLElement[] {
    if (!dialog) return []
    // offsetParent filters out anything hidden, which would otherwise become a
    // dead stop in the tab cycle.
    //
    // tabIndex >= 0 filters out the other kind of dead stop, and it started
    // mattering when the workspace picker moved onto this shell. A roving
    // tabindex list makes every option a `<button tabindex="-1">` except the
    // selected one — those match `button:not([disabled])` but the browser will
    // never Tab to them, so the trap computed its first and last stops from
    // elements the user cannot reach and the wrap landed nowhere.
    return [...dialog.querySelectorAll<HTMLElement>(focusableSelector)].filter(
      (el) => (el.offsetParent !== null || el === document.activeElement) && el.tabIndex >= 0,
    )
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation()
      event.preventDefault()
      onClose()
      return
    }
    if (event.key !== 'Tab') return
    const items = focusableItems()
    if (items.length === 0) {
      // Nothing focusable inside: keep focus on the dialog rather than letting
      // Tab walk out into the inert background.
      event.preventDefault()
      dialog?.focus({ preventScroll: true })
      return
    }
    const first = items[0]
    const last = items[items.length - 1]
    const active = document.activeElement as HTMLElement | null
    if (event.shiftKey && (active === first || active === dialog || !dialog?.contains(active))) {
      event.preventDefault()
      last.focus({ preventScroll: true })
    } else if (!event.shiftKey && active === last) {
      event.preventDefault()
      first.focus({ preventScroll: true })
    }
  }

  onMount(async () => {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    appShell = document.querySelector<HTMLElement>('main.app-shell')
    // Set imperatively rather than via a Svelte binding: the shell lives in
    // App.svelte, and several modals can be open in sequence. A counter is not
    // needed because only one modal is ever open at a time here, but the
    // teardown checks for another open dialog before clearing, so a stacked
    // case degrades to "still inert" rather than "wrongly interactive".
    appShell?.setAttribute('inert', '')
    await tick()
    const items = focusableItems()
    // A DIALOG MAY NAME ITS OWN FIRST FIELD. Without this the initial focus is
    // simply the first focusable element in DOM order, and in every dialog here
    // that is the header's close button — the heading and its × sit above the
    // form. So the command palette opened with focus on ×: typing went nowhere
    // and the arrow keys never reached the command list, which read as the
    // palette ignoring the keyboard entirely.
    //
    // Opt-in and falling back to the old behaviour, so the 29 dialogs that do
    // not mark a field keep focusing exactly what they focused before.
    const preferred = dialog?.querySelector<HTMLElement>('[data-modal-autofocus]')
    ;(preferred ?? items[0] ?? dialog)?.focus({ preventScroll: true })
  })

  onDestroy(() => {
    // Another dialog may have opened on top of this one; only lift inert when
    // this was the last.
    const anotherOpen = document.querySelectorAll('[role="dialog"][aria-modal="true"]').length > 1
    if (!anotherOpen) appShell?.removeAttribute('inert')
    // preventScroll: returning focus must not yank the page back to wherever
    // the trigger happened to be scrolled.
    if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
  })
</script>

<svelte:window on:keydown={handleKeydown} />

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div
  class={backdropClass}
  on:click={(event) => {
    if (closeOnBackdrop && event.target === event.currentTarget) onClose()
  }}
>
  <div
    class={dialogClass}
    class:modal-small={size === 'small'}
    class:modal-medium={size === 'medium'}
    class:modal-large={size === 'large'}
    class:modal-xlarge={size === 'xlarge'}
    role="dialog"
    aria-modal="true"
    aria-labelledby={labelledBy}
    aria-describedby={describedBy}
    aria-busy={busy}
    data-testid={testId}
    tabindex="-1"
    bind:this={dialog}
  >
    <slot />
  </div>
</div>


<style>
  /* THE SIZE SCALE. These live here rather than in style.css on purpose: the
     dialog box is this component's own element, so a scoped rule reaches it and
     nothing else, and the width stops being a per-dialog class a new dialog can
     forget to write.

     Scoping is also what makes them win. Svelte appends its component hash to
     the selector, so `.modal-large` is two classes to `.code-generator-dialog`'s
     one and the migrated dialog takes its width from the step it named rather
     than from a leftover rule. That is deliberate — those per-dialog width rules
     are now dead and are listed for deletion in the handoff — but it is also why
     `size` has no default: an unmigrated dialog must keep its own rule, not be
     quietly resized by one.

     Written as four `class:` directives rather than one interpolated class name
     because Svelte prunes scoped CSS it cannot statically match, and a selector
     built from `class="… modal-size-{size}"` is exactly the kind it cannot. */
  .modal-small {
    width: min(420px, 100%);
  }

  .modal-medium {
    width: min(460px, 100%);
  }

  .modal-large {
    width: min(720px, 100%);
  }

  .modal-xlarge {
    width: min(1060px, 100%);
  }
</style>

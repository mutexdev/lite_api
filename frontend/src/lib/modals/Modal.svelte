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
    return [...dialog.querySelectorAll<HTMLElement>(focusableSelector)].filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
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
    ;(items[0] ?? dialog)?.focus({ preventScroll: true })
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
    role="dialog"
    aria-modal="true"
    aria-labelledby={labelledBy}
    data-testid={testId}
    tabindex="-1"
    bind:this={dialog}
  >
    <slot />
  </div>
</div>

<script lang="ts">
  import Icon, { type IconName } from './Icon.svelte'

  /**
   * A square, icon-only button with a real accessible name.
   *
   * The class `.icon-button` already existed and was already 32px square — but
   * the audit found it wrapping literal text (`x`, `Send`, `^`, `v`), which is
   * how a 32px square button ends up with a word squeezed into it. This makes
   * the icon the content and the label a requirement of the type, so the two
   * cannot come apart.
   *
   * `label` becomes BOTH the accessible name and the tooltip. They are the same
   * string on purpose: a tooltip that says something the screen reader does not
   * is a second, invisible vocabulary.
   */
  type Props = {
    icon: IconName
    label: string
    onclick: (event: MouseEvent) => void
    disabled?: boolean
    /** Renders the button as a toggle and reports its state. */
    pressed?: boolean | undefined
    tone?: 'default' | 'danger'
    size?: 'default' | 'small'
    testId?: string
  }

  let { icon, label, onclick, disabled = false, pressed = undefined, tone = 'default', size = 'default', testId = undefined }: Props = $props()
</script>

<button
  type="button"
  class="icon-button ui-icon-button"
  class:small={size === 'small'}
  class:danger={tone === 'danger'}
  class:pressed={pressed === true}
  aria-label={label}
  aria-pressed={pressed === undefined ? undefined : pressed}
  title={label}
  {disabled}
  data-testid={testId}
  {onclick}
>
  <Icon name={icon} size={size === 'small' ? 14 : 16} />
</button>

<style>
  .ui-icon-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    height: 28px;
    width: 28px;
    min-width: 28px;
    border-radius: var(--radius-6);
    color: var(--muted-strong);
  }
  .ui-icon-button.small { height: 24px; width: 24px; min-width: 24px; }
  .ui-icon-button:hover:not(:disabled) { color: var(--text); }
  /*
    A toggle has to read as ON without relying on colour alone, so the pressed
    state changes background AND border, not just the glyph colour.
  */
  .ui-icon-button.pressed { background: var(--accent-soft); border-color: var(--accent-border); color: var(--accent-strong); }
  .ui-icon-button.danger:hover:not(:disabled) { color: var(--danger); border-color: var(--danger-border); }
  .ui-icon-button:disabled { opacity: 0.45; }
</style>

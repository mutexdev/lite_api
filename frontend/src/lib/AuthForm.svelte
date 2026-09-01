<script lang="ts">
  /**
   * The auth form. One of them, for every level auth can be configured at.
   *
   * A5-03/A5-04/A5-11/A5-13. There were four of these: the request pane, the
   * folder settings tab, the collection tab and the collection settings view.
   * Four hand-copied forms, and by the time the audit ran they had drifted so
   * far apart that the folder one was a different, smaller feature wearing the
   * same name — OAuth2 down fourteen fields, OAuth1 down nine, AWSv4 down two,
   * with nothing on screen admitting it. Hoisting a working request-level
   * config up to a folder discarded most of it in silence.
   *
   * The drift was not only cosmetic. Folder level persisted `queryparams` where
   * the other three persisted `query`, for the same API key placement chosen in
   * what looked like the same dropdown, so which screen you used decided what
   * landed on disk.
   *
   * The fix has to be ONE FILE, not four tidier ones. A comment saying "keep
   * these in step" is what the previous four had, and they did not stay in step.
   * So the field set is data (`authFields.ts`), this renders it, and
   * `authFields.test.mts` asserts that every surface in App.svelte calls this
   * rather than spelling fields out again.
   *
   * ── WHAT THE LEVELS ARE ACTUALLY ALLOWED TO DIFFER IN ───────────────────────
   *
   * Two things, and nothing else:
   *
   *   * where the update goes. Each level has its own persistence path, so the
   *     four update callbacks are props;
   *   * whether "Unset" is offered. A folder that has never had auth set is not
   *     the same as one explicitly set to `none`, because unset means "ask my
   *     parent" during resolution. Request and collection level have no such
   *     state.
   *
   * Everything else — which fields exist, what they are called, what they store
   * — is now identical by construction.
   */
  import type { types } from '../../wailsjs/go/models'
  import OAuth2AdditionalParams from './OAuth2AdditionalParams.svelte'
  import SecretInput from './SecretInput.svelte'
  import {
    authFieldsFor,
    authModeNotes,
    authModes,
    normalizeApiKeyPlacement,
    oauth2TokenPlacementField,
    type AuthField
  } from './authFields'
  import { oauth2TokenStatus, staticTokenHelp, type OAuth2TokenRecord } from './oauth2TokenStatus'

  type ParamBucket = 'authorizationAdditionalParams' | 'tokenAdditionalParams' | 'refreshAdditionalParams'
  type ParamField = 'name' | 'value' | 'enabled'
  type ParamSendIn = 'headers' | 'queryparams' | 'body'

  type Props = {
    auth: types.AuthConfig | undefined
    onAuth: (updates: Partial<types.AuthConfig>) => void | Promise<void>
    onOAuth2: (updates: Partial<types.OAuth2Auth>) => void | Promise<void>
    onOAuth1: (updates: Partial<types.OAuth1Auth>) => void | Promise<void>
    onAWSV4: (updates: Partial<types.AWSV4Auth>) => void | Promise<void>
    /** Accessible name for the mode picker; the only per-level copy left. */
    modeLabel: string
    /** Folder level only: distinguishes "never set" from "explicitly none". */
    allowUnset?: boolean
    onParamAdd?: (bucket: ParamBucket, sendIn: ParamSendIn) => void | Promise<void>
    onParamChange?: (bucket: ParamBucket, index: number, field: ParamField, value: string | boolean) => void | Promise<void>
    onParamRemove?: (bucket: ParamBucket, index: number) => void | Promise<void>
    /**
     * The OAuth2 token as the app knows it. Undefined today at every call site:
     * nothing on the Go side exports the token store. See oauth2TokenStatus.ts —
     * the status line says so honestly rather than claiming no token exists.
     */
    tokenRecord?: OAuth2TokenRecord | undefined
    /** Rendered only when supplied, so the form never offers an action it cannot perform. */
    onFetchToken?: (() => void | Promise<void>) | undefined
    busy?: boolean
  }

  let {
    auth,
    onAuth,
    onOAuth2,
    onOAuth1,
    onAWSV4,
    modeLabel,
    allowUnset = false,
    onParamAdd = undefined,
    onParamChange = undefined,
    onParamRemove = undefined,
    tokenRecord = undefined,
    onFetchToken = undefined,
    busy = false
  }: Props = $props()

  const mode = $derived(auth?.mode ?? (allowUnset ? '' : 'none'))
  const oauth2 = $derived((auth?.oauth2 ?? {}) as Record<string, unknown>)

  const fields = $derived.by(() => {
    const list = [...authFieldsFor(mode, String(oauth2.grantType ?? ''))]
    // The token-placement follow-up depends on a second stored value, so it is
    // appended here rather than threaded through the schema's signature.
    if (mode === 'oauth2') list.push(oauth2TokenPlacementField(String(oauth2.tokenPlacement ?? '')))
    return list
  })

  const note = $derived(mode === '' ? 'No auth is set at this level, so requests use their parent’s.' : authModeNotes[mode] ?? '')

  const tokenStatus = $derived(
    mode === 'oauth2' ? oauth2TokenStatus(auth?.oauth2 as never, tokenRecord, Date.now()) : undefined
  )

  /** The container a field's value lives in: the config itself, or one of its three sub-objects. */
  function container(field: AuthField): Record<string, unknown> {
    if (field.group === '') return (auth ?? {}) as unknown as Record<string, unknown>
    return ((auth as unknown as Record<string, unknown>)?.[field.group] ?? {}) as Record<string, unknown>
  }

  function textValue(field: AuthField): string {
    // API key placement is read through the normalizer, so a folder that
    // already stored `queryparams` shows as Query rather than silently as
    // Header. Every other field is stored verbatim.
    if (field.name === 'apiLocation' && field.group === '') {
      return normalizeApiKeyPlacement(String(container(field)[field.name] ?? ''))
    }
    const stored = container(field)[field.name]
    const value = stored === undefined || stored === null ? '' : String(stored)
    return value === '' && field.fallback ? field.fallback : value
  }

  function checkedValue(field: AuthField): boolean {
    return Boolean(container(field)[field.name])
  }

  function write(field: AuthField, value: string | boolean) {
    if (field.group === 'oauth2') return void onOAuth2({ [field.name]: value } as Partial<types.OAuth2Auth>)
    if (field.group === 'oauth1') return void onOAuth1({ [field.name]: value } as Partial<types.OAuth1Auth>)
    if (field.group === 'awsv4') return void onAWSV4({ [field.name]: value } as Partial<types.AWSV4Auth>)
    return void onAuth({ [field.name]: value } as Partial<types.AuthConfig>)
  }

  /**
   * A stable id per field so every label is a real `for=` rather than a bare
   * span. The four levels can render simultaneously — a collection settings
   * view behind an open folder settings pane — so the level's own name is part
   * of the id or the labels would point at each other's inputs.
   */
  const controlId = (group: string, name: string) =>
    `auth-${modeLabel.replace(/\W+/g, '-').toLowerCase()}-${group || 'root'}-${name}`
  const fieldId = (field: AuthField) => controlId(field.group, field.name)

  const paramBuckets: { bucket: ParamBucket; title: string }[] = [
    { bucket: 'authorizationAdditionalParams', title: 'Authorization request params' },
    { bucket: 'tokenAdditionalParams', title: 'Access token request params' },
    { bucket: 'refreshAdditionalParams', title: 'Refresh token request params' }
  ]
</script>

<div class="field-grid auth-grid auth-form">
  <label class="field-label" for={controlId('', 'mode')}>Mode</label>
  <select
    id={controlId('', 'mode')}
    aria-label={modeLabel}
    value={mode}
    onchange={(event) => onAuth({ mode: event.currentTarget.value })}
  >
    {#if allowUnset}
      <option value="">Unset</option>
    {/if}
    {#each authModes as authMode (authMode)}
      <option value={authMode}>{authMode}</option>
    {/each}
  </select>

  {#each fields as field (field.group + '.' + field.name)}
    <label class="field-label" for={fieldId(field)}>
      {field.label}{#if field.required}<span class="auth-required" title="Required for this mode">*</span><span class="sr-only"> (required)</span>{/if}
    </label>
    <div class="auth-field">
      {#if field.kind === 'secret'}
        <SecretInput
          id={fieldId(field)}
          ariaLabel={`${modeLabel} ${field.label}`}
          placeholder={field.placeholder}
          value={textValue(field)}
          onChange={(value) => write(field, value)}
        />
      {:else if field.kind === 'select'}
        <select
          id={fieldId(field)}
          aria-label={`${modeLabel} ${field.label}`}
          value={textValue(field)}
          onchange={(event) => write(field, event.currentTarget.value)}
        >
          {#each field.options ?? [] as option (option.value)}
            <option value={option.value}>{option.label}</option>
          {/each}
        </select>
      {:else if field.kind === 'checkbox'}
        <input
          id={fieldId(field)}
          type="checkbox"
          aria-label={`${modeLabel} ${field.label}`}
          checked={checkedValue(field)}
          onchange={(event) => write(field, event.currentTarget.checked)}
        />
      {:else if field.kind === 'textarea'}
        <textarea
          id={fieldId(field)}
          class="short"
          spellcheck="false"
          aria-label={`${modeLabel} ${field.label}`}
          placeholder={field.placeholder}
          value={textValue(field)}
          onchange={(event) => write(field, event.currentTarget.value)}
        ></textarea>
      {:else}
        <input
          id={fieldId(field)}
          aria-label={`${modeLabel} ${field.label}`}
          placeholder={field.placeholder}
          value={textValue(field)}
          onchange={(event) => write(field, event.currentTarget.value)}
        />
      {/if}
      {#if field.help}
        <small class="auth-field-help">{field.help}</small>
      {/if}
    </div>
  {/each}

  {#if mode === 'oauth2' && tokenStatus}
    <!--
      The token the twenty fields above exist to obtain, which until now the
      form said nothing about at all. See oauth2TokenStatus.ts for why the
      default state is "Not visible here" rather than "No token".
    -->
    <span class="field-label">Token</span>
    <div class="auth-field">
      <div class="auth-token-status" data-testid="oauth2-token-status">
        <span class="auth-token-state" data-tone={tokenStatus.tone}>{tokenStatus.summary}</span>
        {#if onFetchToken}
          <button type="button" onclick={() => onFetchToken?.()} disabled={busy || !tokenStatus.canFetch}>
            Get new access token
          </button>
        {/if}
      </div>
      {#if tokenStatus.detail}
        <small class="auth-field-help">{tokenStatus.detail}</small>
      {/if}
    </div>

    <label class="field-label" for={controlId('', 'token')}>Static token</label>
    <div class="auth-field">
      <SecretInput
        id={controlId('', 'token')}
        ariaLabel={`${modeLabel} static token`}
        value={auth?.token ?? ''}
        onChange={(value) => onAuth({ token: value })}
      />
      <small class="auth-field-help">{staticTokenHelp(auth?.oauth2 as never, auth?.token)}</small>
    </div>

    {#if onParamAdd && onParamChange && onParamRemove}
      <div class="oauth2-extra-stack auth-form-wide">
        {#each paramBuckets as entry (entry.bucket)}
          <OAuth2AdditionalParams
            title={entry.title}
            params={(oauth2[entry.bucket] as types.OAuth2AdditionalParam[] | undefined) ?? []}
            onAdd={(sendIn) => onParamAdd?.(entry.bucket, sendIn)}
            onChange={(index, paramField, value) => onParamChange?.(entry.bucket, index, paramField, value)}
            onRemove={(index) => onParamRemove?.(entry.bucket, index)}
          />
        {/each}
      </div>
    {/if}
  {/if}

  {#if note}
    <div class="empty-state wide auth-form-wide">{note}</div>
  {/if}
</div>

<style>
  /*
    A5-11. The measure is set here, once, instead of by whether the call site
    remembered to add `auth-grid` beside `field-grid` — two of the four did,
    which is why the same form was 620px wide on two screens and 680px on the
    other two.
  */
  .auth-form {
    max-width: 680px;
  }

  /*
    Anything that is not a label/control pair has to opt out of the two-column
    grid explicitly, or it lands in the label column and gets 140px.
  */
  .auth-form-wide {
    grid-column: 1 / -1;
  }

  .auth-field {
    display: grid;
    gap: var(--space-4);
    min-width: 0;
  }

  .auth-field-help {
    color: var(--muted);
    font-size: var(--font-size-11);
    line-height: 1.4;
  }

  /*
    A5-13. Not a colour on the label: `--danger` on a field that is merely
    unfilled reads as an error the user has already made. The marker is the
    accent, and the screen-reader text beside it carries the same fact for
    anyone who cannot see the glyph.
  */
  .auth-required {
    color: var(--accent);
    margin-left: var(--space-2);
  }

  .auth-token-status {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    flex-wrap: wrap;
  }

  .auth-token-state {
    font-size: var(--font-size-12);
    font-weight: 700;
  }

  /* The same four tones every other graded surface uses. See statusTone.ts. */
  .auth-token-state[data-tone='idle'] { color: var(--muted); }
  .auth-token-state[data-tone='success'] { color: var(--success); }
  .auth-token-state[data-tone='warning'] { color: var(--warning-strong); }
  .auth-token-state[data-tone='danger'] { color: var(--danger-strong); }
</style>

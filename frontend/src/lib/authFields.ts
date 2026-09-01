// The one description of what an auth mode is made of.
//
// There were four hand-copied auth forms — request, folder, collection tab,
// collection settings — and they had drifted, badly and silently. Folder-level
// OAuth2 offered six fields where the request offered twenty; folder OAuth1
// offered five where the request offered fifteen; folder AWSv4 dropped the
// session token and the profile. Nothing on screen said a field was missing, so
// hoisting a working request-level config up to the folder quietly discarded
// most of it and the requests underneath started failing for a reason that was
// no longer visible anywhere.
//
// Worse than the missing fields was a divergence that changed what got WRITTEN:
// request and collection level stored `query` for "send the API key in the
// query string", folder level stored `queryparams`, for the same choice made in
// what looked like the same dropdown. Two vocabularies for one concept, chosen
// by which screen you happened to be on.
//
// So the field set moved here, into data, and the markup that renders it moved
// into one component. A form that is a list cannot drift from another form that
// is the same list, and the persisted vocabulary is now decided in exactly one
// place — `apiKeyPlacementOptions` below — rather than four times over.
//
// This module is deliberately free of Svelte and of the generated Wails types:
// it describes fields, it does not read or write them, which is what lets the
// whole field set be asserted in a test instead of eyeballed across four
// screens.

/** How a field is edited. `secret` is `text` plus masking and a reveal toggle. */
export type AuthFieldKind = 'text' | 'secret' | 'select' | 'checkbox' | 'textarea'

/**
 * Which sub-object of `AuthConfig` a field lives on.
 *
 * The empty string means the top-level config (`username`, `token`, `apiKey`…).
 * The form uses this to pick which update callback to call, which is the only
 * thing the four levels genuinely differ in.
 */
export type AuthFieldGroup = '' | 'oauth2' | 'oauth1' | 'awsv4'

export type AuthFieldOption = { value: string; label: string }

export type AuthField = {
  /** Property name within its group. `group` + `name` is the storage path. */
  name: string
  group: AuthFieldGroup
  label: string
  kind: AuthFieldKind
  /**
   * Required for the mode (and grant type) currently selected.
   *
   * Advisory, not enforced: a half-filled auth config is a legitimate
   * work-in-progress and refusing to store it would make the form unusable.
   * It marks the field so a first-time filler knows which ones are not
   * optional, which no auth form in the app did.
   */
  required?: boolean
  /** Shown when empty. An example value, never a restatement of the label. */
  placeholder?: string
  /** One short line under the field, only where the field is genuinely unobvious. */
  help?: string
  options?: AuthFieldOption[]
  /**
   * Value used when nothing is stored.
   *
   * Only for fields the protocol requires a value for — a token request with no
   * grant type is a request the server rejects. Mirrors `authDefaults.ts`, which
   * writes the same values at edit time; this one is what the control DISPLAYS
   * before any edit has happened.
   */
  fallback?: string
}

export const oauth2GrantTypes: AuthFieldOption[] = [
  { value: 'client_credentials', label: 'Client credentials' },
  { value: 'password', label: 'Password' },
  { value: 'authorization_code', label: 'Authorization code' },
  { value: 'implicit', label: 'Implicit' }
]

export const oauth2CredentialPlacements: AuthFieldOption[] = [
  { value: 'basic_auth_header', label: 'Basic auth header' },
  { value: 'body', label: 'Request body' }
]

export const oauth2TokenPlacements: AuthFieldOption[] = [
  { value: 'header', label: 'Header' },
  { value: 'url', label: 'Query' }
]

export const oauth2TokenSources: AuthFieldOption[] = [
  { value: 'access_token', label: 'access_token' },
  { value: 'id_token', label: 'id_token' }
]

export const oauth1SignatureMethods: AuthFieldOption[] = [
  'HMAC-SHA1',
  'HMAC-SHA256',
  'HMAC-SHA512',
  'RSA-SHA1',
  'RSA-SHA256',
  'RSA-SHA512',
  'PLAINTEXT'
].map((method) => ({ value: method, label: method }))

export const oauth1Placements: AuthFieldOption[] = [
  { value: 'header', label: 'Header' },
  { value: 'query', label: 'Query' },
  { value: 'body', label: 'Body' }
]

/**
 * The API key placement vocabulary — one set of stored values, app-wide.
 *
 * `query` wins over folder level's `queryparams` because three of the four
 * surfaces already wrote it and the Go signer reads it. Anything already
 * written as `queryparams` is read back as `query` by
 * `normalizeApiKeyPlacement` and rewritten the first time the user touches the
 * control, so existing folders keep working and stop being the odd one out.
 */
export const apiKeyPlacementOptions: AuthFieldOption[] = [
  { value: 'header', label: 'Header' },
  { value: 'query', label: 'Query' }
]

/**
 * Reads a stored API key placement into the canonical vocabulary.
 *
 * Accepts the folder-level spelling (`queryparams`) and the two spellings
 * OAuth2 additional params happen to use for the same idea (`url`, `params`),
 * because a value that reached disk through any of those paths still means "in
 * the query string" and showing it as Header would be a silent, wrong answer.
 */
export function normalizeApiKeyPlacement(stored: string | undefined): string {
  const value = (stored ?? '').trim().toLowerCase()
  if (value === 'query' || value === 'queryparams' || value === 'queryparam' || value === 'url' || value === 'params') {
    return 'query'
  }
  return 'header'
}

/** Modes with no fields of their own, and what the form says instead. */
export const authModeNotes: Record<string, string> = {
  none: 'No authentication is applied to this request.',
  inherit:
    "Uses the auth configured on the nearest parent — the closest enclosing folder that sets one, otherwise the collection's own auth. Nothing to configure here."
}

/** The mode list, in the order the picker shows it. */
export const authModes = [
  'none',
  'inherit',
  'basic',
  'bearer',
  'apikey',
  'oauth2',
  'awsv4',
  'digest',
  'ntlm',
  'oauth1',
  'wsse'
]

const basicFields = (mode: string): AuthField[] => [
  { name: 'username', group: '', label: 'Username', kind: 'text', required: true, placeholder: 'api-user' },
  { name: 'password', group: '', label: 'Password', kind: 'secret', required: true },
  ...(mode === 'ntlm'
    ? [{ name: 'domain', group: '' as const, label: 'Domain', kind: 'text' as const, placeholder: 'CORP' }]
    : [])
]

const bearerFields: AuthField[] = [
  {
    name: 'token',
    group: '',
    label: 'Token',
    kind: 'secret',
    required: true,
    help: 'Sent as `Authorization: Bearer <token>`.'
  }
]

const apiKeyFields: AuthField[] = [
  { name: 'apiKey', group: '', label: 'Key', kind: 'text', required: true, placeholder: 'X-API-Key' },
  { name: 'apiValue', group: '', label: 'Value', kind: 'secret', required: true },
  {
    name: 'apiLocation',
    group: '',
    label: 'Send in',
    kind: 'select',
    options: apiKeyPlacementOptions,
    fallback: 'header'
  }
]

const awsv4Fields: AuthField[] = [
  { name: 'accessKeyId', group: 'awsv4', label: 'Access key ID', kind: 'text', required: true, placeholder: 'AKIA…' },
  { name: 'secretAccessKey', group: 'awsv4', label: 'Secret access key', kind: 'secret', required: true },
  {
    name: 'sessionToken',
    group: 'awsv4',
    label: 'Session token',
    kind: 'secret',
    help: 'Only for temporary (STS) credentials.'
  },
  { name: 'service', group: 'awsv4', label: 'Service', kind: 'text', required: true, placeholder: 'execute-api' },
  { name: 'region', group: 'awsv4', label: 'Region', kind: 'text', required: true, placeholder: 'us-east-1' },
  { name: 'profileName', group: 'awsv4', label: 'Profile', kind: 'text', placeholder: 'default' }
]

const oauth1Fields: AuthField[] = [
  { name: 'consumerKey', group: 'oauth1', label: 'Consumer key', kind: 'text', required: true },
  { name: 'consumerSecret', group: 'oauth1', label: 'Consumer secret', kind: 'secret', required: true },
  { name: 'accessToken', group: 'oauth1', label: 'Token', kind: 'text' },
  { name: 'accessTokenSecret', group: 'oauth1', label: 'Token secret', kind: 'secret' },
  {
    name: 'signatureMethod',
    group: 'oauth1',
    label: 'Signature',
    kind: 'select',
    options: oauth1SignatureMethods,
    fallback: 'HMAC-SHA1'
  },
  {
    name: 'placement',
    group: 'oauth1',
    label: 'Add params to',
    kind: 'select',
    options: oauth1Placements,
    fallback: 'header'
  },
  { name: 'callbackUrl', group: 'oauth1', label: 'Callback URL', kind: 'text', placeholder: 'https://example.com/callback' },
  { name: 'verifier', group: 'oauth1', label: 'Verifier', kind: 'text' },
  { name: 'timestamp', group: 'oauth1', label: 'Timestamp', kind: 'text', help: 'Left blank, one is generated per request.' },
  { name: 'nonce', group: 'oauth1', label: 'Nonce', kind: 'text', help: 'Left blank, one is generated per request.' },
  { name: 'version', group: 'oauth1', label: 'Version', kind: 'text', placeholder: '1.0' },
  { name: 'realm', group: 'oauth1', label: 'Realm', kind: 'text' },
  { name: 'privateKey', group: 'oauth1', label: 'Private key', kind: 'textarea', help: 'Required by the RSA signature methods.' },
  {
    name: 'privateKeyType',
    group: 'oauth1',
    label: 'Private key type',
    kind: 'select',
    options: [
      { value: 'text', label: 'Text' },
      { value: 'file', label: 'File path' }
    ],
    fallback: 'text'
  },
  { name: 'includeBodyHash', group: 'oauth1', label: 'Body hash', kind: 'checkbox' }
]

/**
 * OAuth2's field set, which depends on the grant type.
 *
 * The conditional fields are the reason this is a function rather than a
 * constant: an implicit grant has no token endpoint and a client-credentials
 * grant has no callback, and showing either would invite a value that is
 * silently never sent.
 */
function oauth2Fields(grantType: string): AuthField[] {
  const grant = grantType || 'client_credentials'
  const interactive = grant === 'authorization_code' || grant === 'implicit'
  const fields: AuthField[] = [
    {
      name: 'grantType',
      group: 'oauth2',
      label: 'Grant type',
      kind: 'select',
      options: oauth2GrantTypes,
      fallback: 'client_credentials'
    }
  ]
  if (interactive) {
    fields.push(
      {
        name: 'authorizationUrl',
        group: 'oauth2',
        label: 'Authorization URL',
        kind: 'text',
        required: true,
        placeholder: 'https://auth.example.com/authorize'
      },
      {
        name: 'callbackUrl',
        group: 'oauth2',
        label: 'Callback URL',
        kind: 'text',
        required: true,
        placeholder: 'http://localhost:8080/callback'
      }
    )
  }
  // The implicit grant returns the token straight from the authorization
  // endpoint, so it has no token URL to fill in at all.
  if (grant !== 'implicit') {
    fields.push({
      name: 'accessTokenUrl',
      group: 'oauth2',
      label: 'Access token URL',
      kind: 'text',
      required: true,
      placeholder: 'https://auth.example.com/oauth/token'
    })
  }
  fields.push(
    { name: 'clientId', group: 'oauth2', label: 'Client ID', kind: 'text', required: true, placeholder: 'my-client-id' },
    {
      name: 'clientSecret',
      group: 'oauth2',
      label: 'Client secret',
      kind: 'secret',
      required: grant !== 'implicit'
    }
  )
  if (grant === 'password') {
    fields.push(
      { name: 'username', group: 'oauth2', label: 'Username', kind: 'text', required: true },
      { name: 'password', group: 'oauth2', label: 'Password', kind: 'secret', required: true }
    )
  }
  fields.push({ name: 'scope', group: 'oauth2', label: 'Scope', kind: 'text', placeholder: 'read write' })
  if (interactive) {
    fields.push({
      name: 'state',
      group: 'oauth2',
      label: 'State',
      kind: 'text',
      help: 'Left blank, one is generated and checked for you.'
    })
  }
  if (grant !== 'implicit') {
    fields.push({
      name: 'credentialsPlacement',
      group: 'oauth2',
      label: 'Credentials',
      kind: 'select',
      options: oauth2CredentialPlacements,
      fallback: 'basic_auth_header'
    })
  }
  if (grant === 'authorization_code') {
    fields.push({
      name: 'pkce',
      group: 'oauth2',
      label: 'PKCE',
      kind: 'checkbox',
      help: 'Adds a code challenge — required by most public clients.'
    })
  }
  fields.push(
    {
      name: 'refreshTokenUrl',
      group: 'oauth2',
      label: 'Refresh token URL',
      kind: 'text',
      placeholder: 'Defaults to the access token URL'
    },
    {
      name: 'tokenSource',
      group: 'oauth2',
      label: 'Token source',
      kind: 'select',
      options: oauth2TokenSources,
      fallback: 'access_token',
      help: 'Which field of the token response is sent with the request.'
    },
    {
      name: 'tokenPlacement',
      group: 'oauth2',
      label: 'Token placement',
      kind: 'select',
      options: oauth2TokenPlacements,
      fallback: 'header'
    }
  )
  return fields
}

/**
 * The fields for one mode, in display order.
 *
 * `grantType` is the only piece of stored state this needs, and it is passed in
 * rather than read off a config object so the schema can be exercised without
 * constructing one.
 */
export function authFieldsFor(mode: string, grantType = ''): AuthField[] {
  switch (mode) {
    case 'basic':
    case 'digest':
    case 'wsse':
    case 'ntlm':
      return basicFields(mode)
    case 'bearer':
      return bearerFields
    case 'apikey':
      return apiKeyFields
    case 'awsv4':
      return awsv4Fields
    case 'oauth1':
      return oauth1Fields
    case 'oauth2':
      return oauth2Fields(grantType)
    default:
      return []
  }
}

/**
 * The token-placement follow-up field.
 *
 * Split out of `oauth2Fields` because it depends on a second stored value
 * (`tokenPlacement`) and keeping it inline would have made the grant-type
 * signature take two arguments for one conditional field.
 */
export function oauth2TokenPlacementField(tokenPlacement: string | undefined): AuthField {
  return (tokenPlacement || 'header') === 'header'
    ? {
        name: 'tokenHeaderPrefix',
        group: 'oauth2',
        label: 'Header prefix',
        kind: 'text',
        fallback: 'Bearer',
        placeholder: 'Bearer'
      }
    : {
        name: 'tokenQueryKey',
        group: 'oauth2',
        label: 'Query key',
        kind: 'text',
        fallback: 'access_token',
        placeholder: 'access_token'
      }
}

/** Every mode that has at least one field to fill in. */
export function authModeHasFields(mode: string): boolean {
  return authFieldsFor(mode, 'authorization_code').length > 0
}

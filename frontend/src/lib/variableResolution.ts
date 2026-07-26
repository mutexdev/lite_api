// Resolving a {{variable}} to its value, its scope and whether it is a secret.
//
// Extracted from App.svelte, where it was ~200 lines of the trickiest pure
// logic in the frontend with no tests at all. Nothing here is reactive; all of
// it is state-in, value-out, which is exactly the shape that belongs in a plain
// module the way sidebarTree.ts and patchQueue.ts already are.
//
// Three things make this worth testing rather than eyeballing:
//
//   * PRECEDENCE. Six scopes can define the same name, and the winner decides
//     what the request actually sends. Get the order wrong and the tooltip
//     shows one value while the request uses another — the user is told a
//     falsehood by the feature whose entire job is to explain the value.
//   * SECRET PROPAGATION. A variable is secret if it is marked secret OR if
//     anything it interpolates is. Miss the second half and a masked value
//     leaks in cleartext through a variable that merely references it.
//   * CYCLES. {{a}} -> {{b}} -> {{a}} must terminate. The guard is a `seen`
//     set that is added to before recursing and removed after, so a diamond
//     ({{a}} using {{c}} twice) still resolves both times.

import type { types } from '../../wailsjs/go/models'

export type VariableTooltipSource =
  | 'global'
  | 'collection'
  | 'environment'
  | 'folder'
  | 'request'
  | 'runtime'
  | 'process'
  | 'path'
  | 'missing'
  | 'invalid'

export type TooltipResolution = {
  value: string
  containsSecret: boolean
}

export type VariableTooltipInfo = {
  name: string
  scope: string
  rawValue: string
  resolvedValue: string
  secret: boolean
  readOnly: boolean
  found: boolean
  editable: boolean
  validName: boolean
  source: VariableTooltipSource
  index: number
  environmentId?: string
  globalEnvironmentId?: string
}

/** A name Bruno and LiteAPI will accept. Anything else is not addressable. */
export function isValidVariableName(name: string) {
  return /^[\w.-]+$/.test(name)
}

/**
 * The folders enclosing a request, outermost first.
 *
 * Sorted by path length so a nested folder's variables are considered after
 * its parent's and therefore win — the same "closer to the request wins" rule
 * the rest of the chain follows.
 */
export function folderChainForRequest(collection: types.Collection, request: types.RequestItem) {
  const folderPath = request.folderPath ?? ''
  if (!folderPath) return []
  return [...(collection.folders ?? [])]
    .filter((folder) => folder.path === folderPath || folderPath.startsWith(`${folder.path}/`))
    .sort((left, right) => left.path.length - right.path.length)
}

/**
 * Finds which scope defines a name.
 *
 * The order below IS the precedence, and it is last-write-wins: every scope is
 * considered and each match overwrites the previous, so the LAST one listed is
 * the one that takes effect. Runtime variables come last because a script that
 * has just set a value must beat everything the user configured.
 */
export function findTooltipVariable(
  name: string,
  workspace: types.Workspace,
  collection: types.Collection,
  request: types.RequestItem,
  environmentId: string
) {
  let found:
    | {
        variable: types.Variable
        scope: string
        source: VariableTooltipSource
        index: number
        environmentId?: string
        globalEnvironmentId?: string
      }
    | undefined
  const consider = (
    variables: types.Variable[] | undefined,
    scope: string,
    source: VariableTooltipSource,
    sourceId = ''
  ) => {
    for (const [index, variable] of (variables ?? []).entries()) {
      if (variable.enabled === false || variable.name !== name) continue
      found = {
        variable,
        scope,
        source,
        index,
        environmentId: source === 'environment' ? sourceId : undefined,
        globalEnvironmentId: source === 'global' ? sourceId : undefined
      }
    }
  }
  const activeGlobal = workspace.globalEnvironments?.find((env) => env.id === workspace.activeGlobalEnvironmentId)
  consider(activeGlobal?.variables, 'Global', 'global', activeGlobal?.id)
  consider(collection.variables, 'Collection', 'collection')
  const environment = collection.environments?.find((env) => env.id === environmentId)
  consider(environment?.variables, 'Environment', 'environment', environment?.id)
  for (const folder of folderChainForRequest(collection, request)) {
    consider(folder.variables, 'Folder', 'folder', folder.path)
  }
  consider(request.vars?.req, 'Request', 'request')
  consider(collection.runtimeVariables, 'Runtime', 'runtime')
  return found
}

/**
 * Expands {{...}} inside a variable's own value, recursively.
 *
 * `seen` guards against cycles. A name is added before recursing and removed
 * after, which is deliberate: removing it means a value that references the
 * same variable twice in sequence still resolves both times, while a genuine
 * loop still terminates because the name is present on the path back to itself.
 *
 * An unresolvable reference becomes empty rather than being left as literal
 * {{name}} text — showing the braces would suggest the request will send them,
 * and it will not.
 */
export function resolveTooltipValue(
  value: string,
  workspace: types.Workspace,
  collection: types.Collection,
  request: types.RequestItem,
  environmentId: string,
  processEnvValues: Record<string, string>,
  seen: Set<string>
): TooltipResolution {
  let containsSecret = false
  const resolvedValue = value.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (_match, rawName: string) => {
    const name = rawName.trim()
    // A leading '?' marks a prompt variable, which has no value until the user
    // is asked for one at send time.
    if (!name || name.startsWith('?') || seen.has(name)) return ''
    if (name.startsWith('process.env.')) return processEnvValues[name] ?? ''
    const match = findTooltipVariable(name, workspace, collection, request, environmentId)
    if (!match) return ''
    containsSecret = containsSecret || Boolean(match.variable.secret)
    seen.add(name)
    const resolved = resolveTooltipValue(
      String(match.variable.value ?? ''),
      workspace,
      collection,
      request,
      environmentId,
      processEnvValues,
      seen
    )
    seen.delete(name)
    containsSecret = containsSecret || resolved.containsSecret
    return resolved.value
  })
  return { value: resolvedValue, containsSecret }
}

/**
 * Everything the tooltip needs about one variable.
 *
 * Folder and runtime variables are read-only: a folder's belong to the folder
 * rather than this request, and a runtime value is whatever a script last set,
 * so editing either here would write somewhere the user is not looking.
 */
export function resolveVariableTooltip(
  name: string,
  workspace: types.Workspace,
  collection: types.Collection,
  request: types.RequestItem,
  environmentId: string,
  processEnvValues: Record<string, string>
): VariableTooltipInfo {
  const validName = isValidVariableName(name)
  if (!validName) {
    return {
      name,
      scope: 'Request',
      rawValue: '',
      resolvedValue: '',
      secret: false,
      readOnly: true,
      found: false,
      editable: false,
      validName: false,
      source: 'invalid',
      index: -1
    }
  }

  if (name.startsWith('process.env.')) {
    const loaded = Object.prototype.hasOwnProperty.call(processEnvValues, name)
    return {
      name,
      scope: 'Process Env',
      rawValue: '',
      // "Loading..." rather than empty: the values arrive from the backend
      // asynchronously, and an empty string would read as "defined but blank".
      resolvedValue: loaded ? processEnvValues[name] : 'Loading...',
      secret: false,
      readOnly: true,
      found: true,
      editable: false,
      validName,
      source: 'process',
      index: -1
    }
  }

  const match = findTooltipVariable(name, workspace, collection, request, environmentId)
  if (!match) {
    return {
      name,
      scope: 'Request',
      rawValue: '',
      resolvedValue: '',
      secret: false,
      readOnly: false,
      found: false,
      editable: true,
      validName,
      source: 'missing',
      index: -1
    }
  }
  const readOnly = match.source === 'folder' || match.source === 'runtime'
  const rawValue = String(match.variable.value ?? '')
  const resolution = resolveTooltipValue(
    rawValue,
    workspace,
    collection,
    request,
    environmentId,
    processEnvValues,
    new Set([name])
  )
  return {
    name,
    scope: match.scope,
    rawValue,
    resolvedValue: resolution.value,
    // Either the variable is marked secret, or something it interpolates is.
    secret: Boolean(match.variable.secret) || resolution.containsSecret,
    readOnly,
    found: true,
    editable: !readOnly,
    validName,
    source: match.source,
    index: match.index,
    environmentId: match.environmentId,
    globalEnvironmentId: match.globalEnvironmentId
  }
}

/**
 * Path parameters are their own scope: they live on the request rather than in
 * any variable list, and a blank one is "not filled in yet" rather than "set to
 * empty", because sending an empty path segment produces a different URL.
 */
export function pathParamTooltipInfo(name: string, pathParams: types.KeyValue[]): VariableTooltipInfo {
  const validName = isValidVariableName(name)
  const index = pathParams.findIndex((param) => param.name === name)
  const row = index >= 0 ? pathParams[index] : undefined
  const rawValue = String(row?.value ?? '')
  return {
    name,
    scope: 'Path Param',
    rawValue,
    resolvedValue: rawValue,
    secret: false,
    readOnly: !validName,
    found: Boolean(row && row.enabled !== false && rawValue.trim() !== ''),
    editable: validName && Boolean(row),
    validName,
    source: validName ? 'path' : 'invalid',
    index
  }
}

/** What the tooltip shows, with secrets masked unless the user revealed them. */
export function displayTooltipValue(info: VariableTooltipInfo, revealed: boolean) {
  if (!info.found && info.source !== 'path') return 'Not defined'
  if (info.secret && !revealed) return '********'
  return info.resolvedValue
}

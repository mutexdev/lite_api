// Scanning a request for the names it references.
//
// Two scans that look alike and deliberately are not:
//
//   collectVariableNames / scanBodyVariables  ->  every {{name}}, EVERY body
//                                                 field regardless of mode
//   collectPromptNames   / scanBodyPrompts    ->  every {{?name}}, only the
//                                                 fields the ACTIVE mode sends
//
// The asymmetry is the point. The variable list drives a tooltip, so keeping a
// name that lives in the JSON body while the user is looking at the XML tab
// costs nothing and survives switching back. Prompts drive a DIALOG at send
// time: asking someone to fill in a value from a body that is not being sent
// would block the request on a question that does not matter.
//
// scanBodyPrompts is the frontend twin of scanBodyPromptVariables in
// internal/scripting. Both dispatch per body mode, and both fail the same silent
// way — a mode whose fields go unscanned means the user is never asked, and the
// request goes out with a literal {{?token}} in it.

import type { types } from '../../wailsjs/go/models'
import { folderChainForRequest } from './variableResolution.ts'

const promptTokenPattern = /\{\{\?([^{}\s](?:[^{}]*?[^{}\s])?)\}\}/g

export function collectVariableNames(value: unknown, names: Set<string>) {
  if (value === null || value === undefined) return
  const pattern = /\{\{\s*([^{}]+?)\s*\}\}/g
  const text = String(value)
  let match: RegExpExecArray | null
  while ((match = pattern.exec(text)) !== null) {
    const name = match[1]?.trim()
    if (!name || name.startsWith('?')) continue
    names.add(name)
  }
}

export   function fileBodyRows(body: types.RequestBody | undefined) {
    const rows = (body?.files?.length ? body.files : body?.filePath || body?.fileContentType ? [{ filePath: body.filePath ?? '', contentType: body.fileContentType ?? '', selected: true } as types.FileBodyEntry] : []).map((row) => ({ ...row }))
    if (rows.length > 0 && !rows.some((row) => row.selected)) {
      rows[0].selected = true
    }
    return rows
  }

export function scanBodyVariables(body: types.RequestBody | undefined, scan: (value: unknown) => void, scanRows: (rows: types.KeyValue[] | undefined) => void) {
  if (!body) return
  scan(body.json)
  scan(body.xml)
  scan(body.text)
  scan(body.graphqlQuery)
  scan(body.graphqlVariables)
  scanRows(body.formUrlEncoded)
    for (const part of body.multipart ?? []) {
      if (part.enabled === false) continue
      scan(part.name)
      scan(part.value)
      scan(part.filePath)
      scan(part.contentType)
    }
    scan(body.filePath)
    scan(body.fileContentType)
    for (const file of fileBodyRows(body)) {
      scan(file.filePath)
      scan(file.contentType)
    }
  }






export function variableNamesForRequest(request: types.RequestItem) {
  const names = new Set<string>()
  const scan = (value: unknown) => collectVariableNames(value, names)
  const scanRows = (rows: types.KeyValue[] | undefined) => {
    for (const row of rows ?? []) {
      if (row.enabled === false) continue
      scan(row.name)
      scan(row.value)
    }
  }
  scan(request.url)
  scanRows(request.params)
  scanRows(request.pathParams)
  scanRows(request.headers)
  scanBodyVariables(request.body, scan, scanRows)
  return Array.from(names)
}

export function scanBodyPrompts(body: types.RequestBody | undefined, scanText: (value: unknown) => void, scanKeyValues: (rows: types.KeyValue[] | undefined) => void) {
  if (!body) return
  if (body.mode === 'json') {
    scanText(body.json)
  } else if (body.mode === 'xml') {
    scanText(body.xml)
  } else if (body.mode === 'graphql') {
    scanText(body.graphqlQuery)
    scanText(body.graphqlVariables)
  } else if (body.mode === 'text' || body.mode === 'sparql') {
    scanText(body.text)
  } else if (body.mode === 'formUrlEncoded') {
    scanKeyValues(body.formUrlEncoded)
  } else if (body.mode === 'multipartForm') {
    for (const part of body.multipart ?? []) {
      if (part.enabled === false) continue
        scanText(part.name)
        scanText(part.value)
        scanText(part.filePath)
        scanText(part.contentType)
      }
    } else if (body.mode === 'file') {
      scanText(body.filePath)
      scanText(body.fileContentType)
      for (const file of fileBodyRows(body)) {
        scanText(file.filePath)
        scanText(file.contentType)
      }
    }
}

export function collectPromptNames(
  collection: types.Collection,
  request: types.RequestItem,
  environmentId: string,
  activeGlobalEnvironment: types.Environment | undefined
) {
  const prompts = new Set<string>()
  const scanText = (value: unknown) => {
    if (value === null || value === undefined) return
    promptTokenPattern.lastIndex = 0
    let match: RegExpExecArray | null
    const text = String(value)
    while ((match = promptTokenPattern.exec(text)) !== null) {
      const name = match[1]?.trim()
      if (name) prompts.add(name)
    }
  }
  const scanObject = (value: unknown) => {
    if (value === null || value === undefined) return
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
      scanText(value)
      return
    }
    if (Array.isArray(value)) {
      value.forEach(scanObject)
      return
    }
    if (typeof value === 'object') {
      Object.values(value as Record<string, unknown>).forEach(scanObject)
    }
  }
  const scanKeyValues = (rows: types.KeyValue[] | undefined) => {
    for (const row of rows ?? []) {
      if (row.enabled === false) continue
      scanText(row.name)
      scanText(row.value)
    }
  }
  const scanVariables = (rows: types.Variable[] | undefined) => {
    for (const variable of rows ?? []) {
      if (variable.enabled === false) continue
      scanText(variable.name)
      scanText(variable.value)
    }
  }

  scanText(request.url)
  scanText(request.method)
  scanText(request.protoPath)
  scanKeyValues(request.params)
  scanKeyValues(request.pathParams)
  scanKeyValues(request.headers)
  scanVariables(request.vars?.req)
  scanBodyPrompts(request.body, scanText, scanKeyValues)
  for (const message of request.grpcMessages ?? []) {
    scanText(message.name)
    scanText(message.content)
  }
  for (const message of request.wsMessages ?? []) {
    if (message.selected === false) continue
    scanText(message.name)
    scanText(message.content)
  }
  scanObject(request.auth)

  scanVariables(activeGlobalEnvironment?.variables)
  scanKeyValues(collection.headers)
  scanVariables(collection.variables)
  scanObject(collection.auth)

  for (const folder of folderChainForRequest(collection, request)) {
    scanKeyValues(folder.headers)
    scanVariables(folder.variables)
    scanObject(folder.auth)
  }

  const environment = collection.environments?.find((candidate) => candidate.id === environmentId)
  scanVariables(environment?.variables)
  return Array.from(prompts)
}

export function pathParamNamesFromURL(rawURL: string) {
  if (!rawURL) return []
  let pathSource = rawURL
  try {
    const url = new URL(rawURL.startsWith('http://') || rawURL.startsWith('https://') ? rawURL : `http://${rawURL}`)
    pathSource = url.pathname
  } catch {
    pathSource = rawURL.split(/[?#]/, 1)[0] ?? rawURL
  }
  const names: string[] = []
  const seen = new Set<string>()
  const add = (name: string) => {
    if (!name || seen.has(name)) return
    seen.add(name)
    names.push(name)
  }
  for (const segment of pathSource.split('/')) {
    if (segment.startsWith(':')) {
      add(segment.slice(1))
      continue
    }
    if (!/^[A-Za-z0-9_.-]+\([^)]*\)$/.test(segment)) continue
    const regex = /[:]([a-zA-Z_]\w*)/g
    let match: RegExpExecArray | null
    while ((match = regex.exec(segment)) !== null) {
      add((match[1] ?? '').replace(/[')"`]+$/, '').replace(/^[('"`]+/, ''))
    }
  }
  return names
}

export function queryParamsForURL(rawURL: string, currentRows: types.KeyValue[] = []) {
  const query = rawURL.split('#')[0]?.split('?').slice(1).join('?') ?? ''
  const disabledRows = currentRows.filter((row) => row.enabled === false).map((row) => ({ ...row }))
  if (!query) return disabledRows
  const rows: types.KeyValue[] = []
  const decode = (value: string) => {
    try {
      return decodeURIComponent(value.replace(/\+/g, ' '))
    } catch {
      return value
    }
  }
  for (const pair of query.split('&')) {
    if (!pair) continue
    const [rawName, ...rawValueParts] = pair.split('=')
    const name = decode(rawName ?? '')
    const value = decode(rawValueParts.join('='))
    const existing = currentRows.find((row) => row.enabled !== false && row.name === name)
    rows.push({ ...(existing ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue)), name, value, enabled: true } as types.KeyValue)
  }
  return [...rows, ...disabledRows]
}

// Editing a saved response example.
//
// Each of these takes the current value and returns the next one. The mutable
// draft plumbing stays in App.svelte; what lives here is the part that DECIDES
// something, and each decision is a small piece of behaviour a user would
// notice going wrong.
//
// A response example is what "copy as curl" and the mock server both read, so
// an edit that quietly loses a field produces a mock that answers differently
// from the request that was saved.

import type { types } from '../../wailsjs/go/models'
import { queryParamsForURL } from './requestScanning.ts'
import { contentTypeForFilePath, responseExampleBodyTypeForContentType } from './contentTypes.ts'

/**
 * Applies one field edit to an example's request.
 *
 * Three fields do more than store their value:
 *
 *   method   is upper-cased, because "get" and "GET" are the same verb and a
 *            lower-case one renders wrong everywhere it is displayed.
 *   url      re-derives the query-parameter table, so the params panel and the
 *            URL cannot disagree about what will be sent.
 *   bodyMode initialises the collection that mode needs. Without it the editor
 *            renders a table bound to undefined and the first row typed is lost.
 */
export function applyResponseExampleRequestField(
  request: types.ResponseExampleRequest | undefined,
  field: keyof types.ResponseExampleRequest,
  value: string
): types.ResponseExampleRequest {
  const next = { ...(request ?? {}) } as types.ResponseExampleRequest
  if (field === 'method') {
    next.method = value.toUpperCase()
  } else {
    next[field] = value as never
  }
  if (field === 'url') {
    next.params = queryParamsForURL(value, next.params ?? [])
  }
  if (field === 'bodyMode' && value === 'formUrlEncoded') {
    next.formUrlEncoded = next.formUrlEncoded ?? []
  }
  if (field === 'bodyMode' && value === 'multipartForm') {
    next.multipartForm = next.multipartForm ?? []
  }
  if (field === 'bodyMode' && value === 'file') {
    next.file = next.file ?? []
  }
  return next
}

/**
 * Applies one field edit to a file-body row.
 *
 * `selected` behaves as a radio rather than a checkbox: exactly one attachment
 * is sent, so selecting one must clear the rest. Leaving two selected would
 * make which file is sent depend on iteration order.
 *
 * Editing `filePath` re-derives the content type, because the previous value
 * described the previous file.
 */
export function applyResponseExampleFileRow(
  request: types.ResponseExampleRequest | undefined,
  index: number,
  field: keyof types.FileBodyEntry,
  value: string | boolean
): types.ResponseExampleRequest {
  const next = { ...(request ?? {}) } as types.ResponseExampleRequest
  const rows = [...(next.file ?? [])]
  const current = rows[index] ?? ({ filePath: '', contentType: '', selected: rows.length === 0 } as types.FileBodyEntry)
  rows[index] = { ...current, [field]: value } as types.FileBodyEntry
  if (field === 'filePath') {
    rows[index].contentType = contentTypeForFilePath(String(value))
  }
  if (field === 'selected' && value === true) {
    for (let i = 0; i < rows.length; i += 1) {
      rows[i].selected = i === index
    }
  }
  next.file = rows
  return next
}

export type HeaderEditResult = {
  headers: types.KeyValue[]
  /** Present only when the edit implies a new body type. */
  bodyType?: string
}

/**
 * Applies one field edit to a response header row, and re-derives the body type
 * when the Content-Type header's VALUE changed.
 *
 * Only on a change, and only of the value: retyping the same content type must
 * not clobber a body type the user picked by hand, and renaming an unrelated
 * header is not a statement about the body.
 */
export function applyResponseExampleHeader(
  headers: types.KeyValue[] | undefined,
  currentBodyType: string | undefined,
  index: number,
  field: keyof types.KeyValue,
  value: string | boolean
): HeaderEditResult {
  const rows = [...(headers ?? [])]
  const contentTypeHeaderName = (row: types.KeyValue) => row.name?.toLowerCase() === 'content-type'
  const before = rows.find(contentTypeHeaderName)
  const current =
    rows[index] ?? ({ name: '', value: '', enabled: true, secret: false, description: '' } as types.KeyValue)
  rows[index] = { ...current, [field]: value } as types.KeyValue

  const after = rows.find(contentTypeHeaderName)
  if (after && before && after.value !== before.value) {
    const nextBodyType = responseExampleBodyTypeForContentType(after.value ?? '')
    if (nextBodyType !== (currentBodyType || 'text')) {
      return { headers: rows, bodyType: nextBodyType }
    }
  }
  return { headers: rows }
}

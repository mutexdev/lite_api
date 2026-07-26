// Deriving a content type from a filename, and a body type from a content type.
//
// Both run while someone is typing, and both are guesses that get WRITTEN into
// the request rather than merely displayed. A wrong guess is saved and sent.
//
// The filename lookup deliberately strips a query and fragment before reading
// the extension: file paths pasted from a browser carry them, and
// "report.pdf?download=1" is still a PDF. It returns "" for an unknown or
// absent extension rather than guessing application/octet-stream — an empty
// content type lets the server sniff, while a wrong one overrides it.

export   function contentTypeForFilePath(filePath: string) {
    const ext = filePath.trim().toLowerCase().split('?')[0].split('#')[0].match(/\.([a-z0-9]+)$/)?.[1] ?? ''
    const types: Record<string, string> = {
      json: 'application/json',
      txt: 'text/plain; charset=utf-8',
      text: 'text/plain; charset=utf-8',
      xml: 'application/xml',
      csv: 'text/csv; charset=utf-8',
      html: 'text/html; charset=utf-8',
      htm: 'text/html; charset=utf-8',
      css: 'text/css; charset=utf-8',
      js: 'text/javascript; charset=utf-8',
      mjs: 'text/javascript; charset=utf-8',
      png: 'image/png',
      jpg: 'image/jpeg',
      jpeg: 'image/jpeg',
      gif: 'image/gif',
      svg: 'image/svg+xml',
      pdf: 'application/pdf',
      zip: 'application/zip'
    }
    return types[ext] ?? ''
  }

export function responseExampleBodyTypeForContentType(contentType = '') {
  const normalized = contentType.toLowerCase()
  if (normalized.includes('application/json')) return 'json'
  if (normalized.includes('text/xml') || normalized.includes('application/xml')) return 'xml'
  if (normalized.includes('text/html')) return 'html'
  return 'text'
}

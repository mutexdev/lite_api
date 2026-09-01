export type CommandTone = 'idle' | 'success' | 'warning' | 'danger'

export type RequestResponseSummary = {
  status: string
  statusText: string
  duration: string
  size: string
  tone: CommandTone
}

export type RequestCommandState = {
  protocol: string
  environmentName: string
  saveLabel: string
  dirty: boolean
  runningLabel: string
  canCancel: boolean
  cancelLabel: string
  cancelDuringBusy: boolean
  cancellationPending: boolean
  backgroundCancellation?: {
    requestName: string
    pending: boolean
  }
  transportCues: string[]
  response: RequestResponseSummary
}

// D4 — `onRun` is gone from here, not from the app: "Run collection" is a
// collection-scoped command and now lives on the collection's `⋯` menu, the
// runner page and the palette. It sat between Save and Send — two per-request
// commands — with the bare word "Run" on it, which is the mismatch the strip
// spent a tooltip apologising for.
export type RequestCommandActions = {
  onSave: () => void | Promise<void>
  onSend: () => void | Promise<void>
  onCancel?: () => void | Promise<void>
  onCancelBackground?: () => void | Promise<void>
  onToggleOrientation: () => void | Promise<void>
}

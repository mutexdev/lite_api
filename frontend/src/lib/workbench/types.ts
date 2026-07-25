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

export type RequestCommandActions = {
  onSave: () => void | Promise<void>
  onSend: () => void | Promise<void>
  onRun: () => void | Promise<void>
  onCancel?: () => void | Promise<void>
  onCancelBackground?: () => void | Promise<void>
  onToggleOrientation: () => void | Promise<void>
}

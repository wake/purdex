// spa/src/lib/workbook/types.ts — the session workbook on the wire (spec §9; daemon internal/module/workbook/api.go).
// Every time is unix milliseconds. Capabilities: `workbook.v1` (entries, status) and `workbook.v2` (WA-1b).

export const WORKBOOK_V1_CAPABILITY = 'workbook.v1'
export const WORKBOOK_V2_CAPABILITY = 'workbook.v2'

export const WORKBOOK_PROVIDER = 'claude'

export type EntryState = 'pending' | 'ok' | 'failed' | 'skipped'

export interface WorkbookEntry {
  id: number
  convKey: string
  sessionId: string
  turnId: string
  turnAt: number
  state: EntryState
  reason: string
  thing: string
  push: string
  entry: string
  thingDone: boolean
  createdAt: number
  updatedAt: number
}

export interface ConversationPage {
  convKey: string
  status: string
  statusAt: number
  entries: WorkbookEntry[]
}

export interface EntryEvent { convKey: string; sessionId: string; entry: WorkbookEntry }
export interface StatusEvent { convKey: string; sessionId: string; status: string; updatedAt: number }

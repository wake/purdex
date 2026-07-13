import type { Tab, Workspace } from '../../types/tab'
import type { Session } from '../host-api'

export interface SessionMeta {
  hostId: string; sessionCode: string; name: string
  mode: 'terminal' | 'stream'
  cwd?: string; currentCommand?: string
  restorable: boolean
  captureError?: 'host-unreachable' | 'session-dead-at-capture'
}
export interface WorkspaceSnapshot {
  version: 1; capturedAt: number
  tabs: Record<string, Tab>; tabOrder: string[]; activeTabId: string | null
  workspaces: Workspace[]; activeWorkspaceId: string | null
  sessionMeta: Record<string, Record<string, SessionMeta>>   // [hostId][sessionCode]
}
export interface CaptureResult { total: number; resolved: number; unresolved: number }

export type RemapEntry =
  | { status: 'reattached'; newCode: string; session: Session }
  | { status: 'rebuilt';    newCode: string; session: Session }
  | { status: 'failed' }
export type Remap = Record<string, Record<string, RemapEntry>>  // [hostId][oldCode]
export interface EnsureReport { reattached: number; rebuilt: number; failed: number }
export interface RestoreReport extends EnsureReport {
  rebuiltButUnattached: Array<{ hostId: string; name: string; cwd: string }>
}
// 失敗契約：三動作成功 resolve RestoreReport；replaceTabSnapshot throw 時包成
// RestoreError（帶已收集含 rebuiltButUnattached 的 report）reject，UI catch 後讀 e.report。
export class RestoreError extends Error {
  constructor(public report: RestoreReport, public cause?: unknown) { super('restore failed') }
}

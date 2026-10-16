// Prototype-only model for the workbook journal v4 (spec §5). Fixture-shaped; not the daemon's real types.
export type Kind = 'request' | 'task_new' | 'task_done' | 'exec' | 'exec_done' | 'comm' | 'confirm'
export type By = 'user' | 'leader' | 'member'

export interface ConfirmItem { t: string; done: boolean }

export interface Entry {
  id: string
  date: string // YYYY-MM-DD
  time: string // HH:mm
  kind: Kind
  by?: By // request only
  actor: string // `A → B` for request/comm/confirm, a seat name otherwise
  text: string
  refs: string[]
  taskId?: string // task_new / task_done
  items?: ConfirmItem[] // confirm only
  detail?: string // confirm only: the original question, as written
  pinned: boolean
}

export type TaskState = 'doing' | 'paused' | 'todo' | 'done' | 'dropped'
export interface Task { id: string; no: string; title: string; owner: string; state: TaskState; refs?: string[] }

export type Stage = '未開始' | '進行中' | '審查中' | '完成' | '暫停'
export interface Feature { name: string; rows: { stage: string; desc: string; status: Stage }[] }

export type Filter = 'all' | 'exec' | 'task' | 'talk' | 'confirm'
// TODO(i18n): plain Chinese strings for the prototype.
export const FILTERS: { id: Filter; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'exec', label: '執行' },
  { id: 'task', label: '任務' },
  { id: 'talk', label: '請求與溝通' },
  { id: 'confirm', label: '待確認' },
]
export const matchesFilter = (e: Entry, f: Filter): boolean => {
  switch (f) {
    case 'all': return true
    case 'exec': return e.kind === 'exec' || e.kind === 'exec_done'
    case 'task': return e.kind === 'task_new' || e.kind === 'task_done'
    case 'talk': return e.kind === 'request' || e.kind === 'comm'
    case 'confirm': return e.kind === 'confirm'
  }
}

export const openItems = (e: Entry): number => (e.items ?? []).filter((i) => !i.done).length
export const isResolved = (e: Entry): boolean => e.kind === 'confirm' && openItems(e) === 0

// 待確認 is yellow (the app's needs-attention family); the rest differ in hue and in lightness.
export const KIND_COLOR: Record<Kind, string> = {
  exec: '#5b8def', // blue, mid-dark
  exec_done: '#2f9e6b', // green, dark
  task_new: '#f08a4b', // orange, mid
  task_done: '#b5e07a', // lime, light
  comm: '#bb9af7', // violet, mid-light
  request: '#f2647f', // coral, mid-dark
  confirm: '#ffd23f', // yellow, lightest
}
export const kindLabel = (e: Entry): string => {
  if (e.kind === 'request') return e.by === 'user' ? '使用者請求' : e.by === 'leader' ? 'leader 請求' : 'member 請求'
  return { task_new: '任務建立', task_done: '任務完成', exec: '執行', exec_done: '執行完成', comm: '溝通', confirm: '待確認' }[e.kind]
}

export const STAGE_COLOR: Record<Stage, string> = {
  未開始: '#8b93a7', 進行中: '#5b8def', 審查中: '#bb9af7', 完成: '#2f9e6b', 暫停: '#f08a4b',
}
export const TASK_STATE: Record<TaskState, { label: string; color: string }> = {
  doing: { label: '進行中', color: '#5b8def' },
  paused: { label: '暫停中', color: '#f08a4b' },
  todo: { label: '未開始', color: '#8b93a7' },
  done: { label: '完成', color: '#2f9e6b' },
  dropped: { label: '已放棄', color: '#8b93a7' },
}

// spa/src/components/journal/journal-types.ts — the journal's event model (prototype; fixture-shaped).
export type JournalKind = 'exec' | 'done' | 'task' | 'taskdone' | 'comm' | 'req'

export interface JournalEvent {
  date: string // YYYY-MM-DD
  time: string // HH:mm
  kind: JournalKind
  kindLabel: string
  /** `A → B` for messages and requests; a single name for a seat's own event. */
  actor: string
  title: string
  body: string
  refs: string[]
  /** A request that waits for the user's answer. */
  pending?: boolean
}

export type JournalFilter = 'all' | 'exec' | 'task' | 'talk' | 'wait'

// TODO(i18n): plain Chinese strings for the prototype.
export const FILTERS: { id: JournalFilter; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'exec', label: '執行' },
  { id: 'task', label: '任務' },
  { id: 'talk', label: '溝通與請求' },
  { id: 'wait', label: '等我回覆' },
]

export const matchesFilter = (e: JournalEvent, f: JournalFilter): boolean => {
  switch (f) {
    case 'all': return true
    case 'exec': return e.kind === 'exec' || e.kind === 'done'
    case 'task': return e.kind === 'task' || e.kind === 'taskdone'
    case 'talk': return e.kind === 'comm' || e.kind === 'req'
    case 'wait': return e.pending === true
  }
}

/** Each kind has its own hue AND its own lightness step, so they also differ in greyscale. */
export const KIND_STYLE: Record<JournalKind, { color: string }> = {
  exec: { color: '#7aa2f7' }, // blue, mid
  done: { color: '#2f9e6b' }, // deep green, dark
  task: { color: '#e8b45a' }, // amber, light
  taskdone: { color: '#b5e07a' }, // lime, lightest
  comm: { color: '#bb9af7' }, // violet, mid-light
  req: { color: '#f2647f' }, // coral red, mid-dark
}

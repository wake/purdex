// spa/src/lib/conversations/find-memory.ts — the ⌘F search of a pane's deck / chat (#2665): whether the bar is open, what it looks for and which
// match it stands on. The deck and the chat unmount with their tab (CLAUDE.md tab-hosted rule), so this lives outside the component, keyed by
// pane AND session (a pane that moves to another session — /clear, a relay — starts a fresh search) and shared by the pane's two views: a
// switch between them keeps the word and the place (the units differ, `relocate` re-seats the match). Memory only.
import { useSyncExternalStore } from 'react'
import type { MatchIdentity, UnitAnchor } from '../nex/transcript-search'
import { ANCHOR_START } from '../nex/transcript-search'

export interface FindState {
  open: boolean
  query: string
  /** The match the reader stands on (null: none yet). */
  sel: MatchIdentity | null
  /** Where the kept matches are centred when there are more than the limit. */
  win: UnitAnchor
  /** Bumped by every ⌘F: the bar takes the focus (and selects its text) again. */
  focus: number
}

const CLOSED: FindState = { open: false, query: '', sel: null, win: ANCHOR_START, focus: 0 }

const states = new Map<string, FindState>()
const listeners = new Map<string, Set<() => void>>()

export const findKey = (paneId: string, sessionId: string): string => `${paneId}\0${sessionId}`

export function readFind(key: string): FindState {
  return states.get(key) ?? CLOSED
}

function write(key: string, next: FindState): void {
  states.set(key, next)
  listeners.get(key)?.forEach((fn) => fn())
}

export function patchFind(key: string, patch: Partial<FindState>): void {
  write(key, { ...readFind(key), ...patch })
}

/** ⌘F: open the bar, or when it is open already, ask it to take the focus again. */
export function requestFind(key: string): void {
  const cur = readFind(key)
  write(key, { ...cur, open: true, focus: cur.focus + 1 })
}

/** Esc / the close button: the bar goes, and so does the search (a new ⌘F starts empty, as a browser's does). */
export function closeFind(key: string): void {
  if (!states.has(key)) return
  states.delete(key)
  listeners.get(key)?.forEach((fn) => fn())
}

/** Every search of a pane, whatever session it was on (the pane closed). `keep` spares the keys it returns true for. */
export function forgetFindOfPane(paneId: string, keep?: (key: string) => boolean): void {
  for (const key of [...states.keys()]) if (key.startsWith(`${paneId}\0`) && !keep?.(key)) closeFind(key)
}

function subscribe(key: string, fn: () => void): () => void {
  let set = listeners.get(key)
  if (!set) { set = new Set(); listeners.set(key, set) }
  set.add(fn)
  return () => { set.delete(fn); if (set.size === 0) listeners.delete(key) }
}

export function useFindState(key: string): FindState {
  return useSyncExternalStore((fn) => subscribe(key, fn), () => readFind(key))
}

export function clearAllFinds(): void {
  for (const key of [...states.keys()]) closeFind(key)
}

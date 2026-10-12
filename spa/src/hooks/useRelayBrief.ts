// spa/src/hooks/useRelayBrief.ts — the work report on a relay card (UP-5a): the protagonist session's last three finished
// workbook entries and its open todos. Both halves are best effort: loading, an error, a 404 and an empty answer all read
// as "nothing to show", and nothing here ever gates the decision. An answer for a session the card no longer names is
// dropped (the effect's `cancelled` flag), so a late response cannot land on the next card.
import { useEffect, useState } from 'react'
import { fetchConversation, fetchTodos } from '../lib/workbook/api'
import { selectWorkbookSupport, useWorkbookStore } from '../stores/useWorkbookStore'
import { WORKBOOK_PROVIDER, type WorkbookEntry } from '../lib/workbook/types'

export const BRIEF_ENTRIES = 3
/** Asked for more than shown: a pending / failed / refresh row among the newest must not eat the quota. */
const BRIEF_FETCH = 10
const BRIEF_TODOS = 20

export interface RelayBrief { entries: string[]; todos: string[] }

/** A finished turn's one line: the same text the 紀錄 shows (`entry`, else its `thing`); pending, failed, skipped and refresh rows say nothing. */
function entryLine(e: WorkbookEntry): string {
  if (e.state !== 'ok' || e.kind !== 'turn') return ''
  return (e.entry !== '' ? e.entry : e.thing).trim()
}

const NONE: string[] = []

/** Each half is committed on its own, under the key of the session and support it was asked for: a half that hangs or fails
 *  never hides the other, and an answer for a key the card no longer has is never read. */
function useHalf(key: string, active: boolean, load: () => Promise<string[]>): string[] {
  const [got, setGot] = useState<{ key: string; lines: string[] } | null>(null)
  useEffect(() => {
    if (!active) return
    let cancelled = false
    void load().then((lines) => { if (!cancelled) setGot({ key, lines }) }, () => {})
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `load` closes over exactly the pieces `key` is made of
  }, [key, active])
  return active && got !== null && got.key === key ? got.lines : NONE
}

export function useRelayBrief(hostId: string, sessionId: string): RelayBrief {
  const v1 = useWorkbookStore((s) => selectWorkbookSupport(hostId, s).v1)
  const v2 = useWorkbookStore((s) => selectWorkbookSupport(hostId, s).v2)
  const base = `${hostId}\u0000${sessionId}`
  const entries = useHalf(`${base}\u0000e`, sessionId !== '' && v1, async () => {
    const r = await fetchConversation(hostId, WORKBOOK_PROVIDER, sessionId, { limit: BRIEF_FETCH })
    return r.kind === 'ok' ? [...r.page.entries].sort((a, b) => b.id - a.id).map(entryLine).filter((l) => l !== '').slice(0, BRIEF_ENTRIES) : []
  })
  const todos = useHalf(`${base}\u0000t`, sessionId !== '' && v2, async () => {
    const r = await fetchTodos(hostId, sessionId, { state: 'open', limit: BRIEF_TODOS })
    return r.kind === 'ok' ? r.todos.map((t) => t.title.trim()).filter((l) => l !== '').slice(0, BRIEF_TODOS) : []
  })
  return { entries, todos }
}

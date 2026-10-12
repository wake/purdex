// spa/src/hooks/useRelayBrief.ts — the work report on a relay card (UP-5a): the protagonist session's last three finished
// workbook entries and its open todos. Both halves are best effort: loading, an error, a 404 and an empty answer all read
// as "nothing to show", and nothing here ever gates the decision. An answer for a session the card no longer names is
// dropped (the effect's `cancelled` flag), so a late response cannot land on the next card.
import { useEffect, useState } from 'react'
import { fetchConversation, fetchTodos } from '../lib/workbook/api'
import { selectWorkbookSupport, useWorkbookStore } from '../stores/useWorkbookStore'
import { WORKBOOK_PROVIDER, type WorkbookEntry } from '../lib/workbook/types'

export const BRIEF_ENTRIES = 3

export interface RelayBrief { entries: string[]; todos: string[] }

/** A finished turn's one line: the same text the 紀錄 shows (`entry`, else its `thing`); pending, failed, skipped and refresh rows say nothing. */
function entryLine(e: WorkbookEntry): string {
  if (e.state !== 'ok' || e.kind !== 'turn') return ''
  return (e.entry !== '' ? e.entry : e.thing).trim()
}

const EMPTY: RelayBrief = { entries: [], todos: [] }

export function useRelayBrief(hostId: string, sessionId: string): RelayBrief {
  const v1 = useWorkbookStore((s) => selectWorkbookSupport(hostId, s).v1)
  const v2 = useWorkbookStore((s) => selectWorkbookSupport(hostId, s).v2)
  const [got, setGot] = useState<{ key: string; brief: RelayBrief } | null>(null)
  const key = `${hostId}\u0000${sessionId}\u0000${v1}\u0000${v2}`

  useEffect(() => {
    if (sessionId === '' || (!v1 && !v2)) return
    let cancelled = false
    const entries = v1
      ? fetchConversation(hostId, WORKBOOK_PROVIDER, sessionId, { limit: BRIEF_ENTRIES }).then(
          (r) => r.kind === 'ok'
            ? [...r.page.entries].sort((a, b) => b.id - a.id).map(entryLine).filter((l) => l !== '').slice(0, BRIEF_ENTRIES)
            : [],
          () => [] as string[],
        )
      : Promise.resolve([] as string[])
    const todos = v2
      ? fetchTodos(hostId, sessionId, { state: 'open' }).then(
          (r) => r.kind === 'ok' ? r.todos.map((t) => t.title.trim()).filter((l) => l !== '') : [],
          () => [] as string[],
        )
      : Promise.resolve([] as string[])
    void Promise.all([entries, todos]).then(([e, t]) => {
      if (!cancelled) setGot({ key, brief: { entries: e, todos: t } })
    })
    return () => { cancelled = true }
  }, [hostId, sessionId, v1, v2, key])

  return got !== null && got.key === key ? got.brief : EMPTY
}

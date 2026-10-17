// spa/src/lib/journal/__tests__/journal-fixture.ts — seeds the journal store and cc tabs for the J-2b UI tests (the loader and the
// API are covered in J-2a; here the store already holds what they would have loaded).
import { useJournalStore, sessionKey, type UnitState } from '../../../stores/useJournalStore'
import type { JournalUnit } from '../types'
import type { Tab } from '../../../types/tab'

/** A tab whose first pane is a tmux session with a recorded Claude Code conversation `sessionId` (and the code the roster knows it by). */
export const ccTabOf = (id: string, hostId: string, tmuxName: string, sessionId: string): Tab => ({
  id, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: {
    kind: 'tmux-session', hostId, sessionCode: `code-${tmuxName}`, mode: 'terminal', cachedName: tmuxName, tmuxInstance: 'i',
    rebuild: { sessionName: tmuxName, tmuxInstance: 'i', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1 },
  } } },
})

export const setJournalHost = (hostId: string, yes: boolean | null): void => {
  useJournalStore.setState((s) => ({
    sticky: yes === null ? Object.fromEntries(Object.entries(s.sticky).filter(([k]) => k !== hostId)) : { ...s.sticky, [hostId]: yes },
    support: yes === null ? s.support : { ...s.support, [hostId]: yes },
  }))
}

/** The unit `sessionId` of `hostId` names, as if its first page had been read. */
export function seedUnit(hostId: string, sessionId: string, unit: JournalUnit, over: Partial<UnitState> = {}): string {
  const key = `${unit.kind}:${unit.id}`
  const u: UnitState = {
    unit, project: { name: 'purdex', remoteUrl: '' }, personName: 'Wake', entries: [], confirms: [], more: false, openConfirms: 0,
    tasks: null, roadmap: null, staleAt: { entries: 0, tasks: 0, roadmap: 0 }, readAt: { entries: 1, tasks: 1, roadmap: 1 }, touched: 1, ...over,
  }
  useJournalStore.setState((s) => {
    const h = s.byHost[hostId] ?? { units: {}, unitOfSession: {} }
    return {
      byHost: { ...s.byHost, [hostId]: { units: { ...h.units, [key]: u }, unitOfSession: { ...h.unitOfSession, [sessionId]: key } } },
      routes: { ...s.routes, [sessionKey(hostId, sessionId)]: { kind: 'direct' } },
    }
  })
  return key
}

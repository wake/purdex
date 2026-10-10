// spa/src/stores/useSessionViewStore.ts — which view a session pane shows: the terminal, the deck (指揮台) or the chat
// (U3 plan D1). Device-local: it is how THIS device looks at a pane, so it lives here and never in the pane's content
// (pane content travels with Profile Sync). Keyed by tab + pane; the value carries the session the choice was made for,
// so a pane rebound to another session starts at the terminal again. The terminal is the absence of a record.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { findPane } from '../lib/pane-tree'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import { useTabStore } from './useTabStore'

export type SessionView = 'terminal' | 'deck' | 'chat'
export type ConversationView = Exclude<SessionView, 'terminal'>

export interface SessionViewRecord {
  view: ConversationView
  sessionCode: string
}

const VIEWS: readonly string[] = ['deck', 'chat']

/** `${tabId}\0${paneId}` — a NUL cannot appear in either id. */
export const viewKey = (tabId: string, paneId: string): string => `${tabId}\0${paneId}`

interface SessionViewState {
  byPane: Record<string, SessionViewRecord>
  setView: (tabId: string, paneId: string, sessionCode: string, view: SessionView) => void
}

export const useSessionViewStore = create<SessionViewState>()(
  persist(
    (set) => ({
      byPane: {},
      setView: (tabId, paneId, sessionCode, view) =>
        set((s) => {
          const key = viewKey(tabId, paneId)
          if (view === 'terminal') {
            if (!(key in s.byPane)) return s
            const byPane = { ...s.byPane }
            delete byPane[key]
            return { byPane }
          }
          const cur = s.byPane[key]
          if (cur && cur.view === view && cur.sessionCode === sessionCode) return s
          return { byPane: { ...s.byPane, [key]: { view, sessionCode } } }
        }),
    }),
    {
      name: STORAGE_KEYS.SESSION_VIEW,
      storage: purdexStorage,
      partialize: (s) => ({ byPane: s.byPane }),
      merge: (persisted, current) => {
        const raw = (persisted as { byPane?: unknown } | undefined)?.byPane
        const byPane: Record<string, SessionViewRecord> = {}
        if (raw && typeof raw === 'object') {
          for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
            const r = v as Partial<SessionViewRecord> | null
            if (r && typeof r === 'object' && typeof r.view === 'string' && VIEWS.includes(r.view)
              && typeof r.sessionCode === 'string') {
              byPane[k] = { view: r.view as ConversationView, sessionCode: r.sessionCode }
            }
          }
        }
        return { ...current, byPane }
      },
    },
  ),
)

/** The view of a pane now showing `sessionCode`: the recorded one if it was chosen for this session, else the terminal. */
export function selectSessionView(tabId: string, paneId: string, sessionCode: string) {
  return (s: Pick<SessionViewState, 'byPane'>): SessionView => {
    const r = s.byPane[viewKey(tabId, paneId)]
    return r && r.sessionCode === sessionCode ? r.view : 'terminal'
  }
}

let uninstallCleanup: (() => void) | null = null

/**
 * The cleanup point (D1): drop the record of every pane that is no longer in the tab world. A subscription on the tab
 * store, not a hook in its close path, because the tab world can also be replaced wholesale (cross-window rehydrate,
 * Profile Sync apply) without going through `closeTab` / `closePane`. An empty tab world is acted on too (the last
 * tab closed), except before the tab store has hydrated, when the records must survive.
 *
 * Called once below; calling it again replaces the subscription. Returns the uninstall function.
 */
export function installSessionViewCleanup(): () => void {
  uninstallCleanup?.()
  const unsubscribe = useTabStore.subscribe((next, prev) => {
    if (next.tabs === prev.tabs) return
    // An empty tab world is real after the last tab closed, but also what the store looks like before it has hydrated.
    if (Object.keys(next.tabs).length === 0 && !useTabStore.persist.hasHydrated()) return
    const { byPane } = useSessionViewStore.getState()
    let gone: string[] | null = null
    for (const key of Object.keys(byPane)) {
      const [tabId, paneId] = key.split('\0')
      const tab = Object.hasOwn(next.tabs, tabId) ? next.tabs[tabId] : undefined
      if (!tab || !findPane(tab.layout, paneId)) (gone ??= []).push(key)
    }
    if (!gone) return
    const kept = { ...byPane }
    for (const key of gone) delete kept[key]
    useSessionViewStore.setState({ byPane: kept })
  })
  const uninstall = () => {
    unsubscribe()
    if (uninstallCleanup === uninstall) uninstallCleanup = null
  }
  uninstallCleanup = uninstall
  return uninstall
}

installSessionViewCleanup()

// HMR: a hot reload of this module must not leave the old subscription running against the old store.
if (import.meta.hot) {
  import.meta.hot.dispose(() => uninstallCleanup?.())
}

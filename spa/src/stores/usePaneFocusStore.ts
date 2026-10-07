// spa/src/stores/usePaneFocusStore.ts — which panes of each tab the user focused most recently (shell cleanup spec
// §8.1, rule F). Read through `lib/pane-focus.ts`: `focusTargetOf` (which pane takes focus when the tab is shown) and
// `statusTargetOf` (which pane the status bar shows).
//
// Memory only: no persist and no syncManager. After a reload every tab starts from its primary pane.
//
// Writers are the pane leaf wrappers in `PaneLayoutRenderer` (pointerdown / focus inside a leaf) and `requestFocus`
// (a notification click, which also posts the one-shot `focusRequest`). Ids of panes that
// were closed or swapped away may linger in a live tab's list; every reader filters against the live leaves.
import { create } from 'zustand'
import { useTabStore } from './useTabStore'

/** Most panes remembered per tab. */
export const PANE_FOCUS_CAP = 16

/** A one-shot request that a pane take focus now (#1840 review A1). Only the newest request is kept. */
export interface PaneFocusRequest {
  tabId: string
  paneId: string
  /** Grows with every request, so asking for the same pane again is a new request. */
  nonce: number
  /** Some `useActivationFocus` of that pane took it (`takeFocusRequest`): it is used up. */
  taken: boolean
}

interface PaneFocusState {
  /** tab id → pane ids, most recent first, no duplicates, at most `PANE_FOCUS_CAP`. */
  recent: Record<string, string[]>
  /** Record that the user focused `paneId` in `tabId`: move it to the front. */
  touch: (tabId: string, paneId: string) => void
  forgetTab: (tabId: string) => void
  focusRequest: PaneFocusRequest | null
  /**
   * Ask `paneId` of `tabId` to take focus once, also when its tab is already on screen, where no activation focuses
   * anything (`useActivationFocus`). Makes it the tab's most recent pane (`touch`) and posts a new request; that
   * pane's `useActivationFocus` takes it and calls its focus function — or the tab's activation does, when the tab is
   * being shown anyway.
   */
  requestFocus: (tabId: string, paneId: string) => void
  /** Claim request `nonce`: true once, false when it is used up or no longer the current request. */
  takeFocusRequest: (nonce: number) => boolean
  /** Undo a claim whose focus never ran (its frame was cancelled), so a re-run can claim it again. */
  releaseFocusRequest: (nonce: number) => void
}

let lastRequestNonce = 0

export const usePaneFocusStore = create<PaneFocusState>()((set, get) => ({
  recent: {},
  touch: (tabId, paneId) =>
    set((s) => {
      const list = s.recent[tabId]
      // Repeated clicks in the same pane: no write, so no subscriber re-runs.
      if (list?.[0] === paneId) return s
      const next = [paneId, ...(list ?? []).filter((id) => id !== paneId)].slice(0, PANE_FOCUS_CAP)
      return { recent: { ...s.recent, [tabId]: next } }
    }),
  focusRequest: null,
  requestFocus: (tabId, paneId) => {
    get().touch(tabId, paneId)
    set({ focusRequest: { tabId, paneId, nonce: ++lastRequestNonce, taken: false } })
  },
  takeFocusRequest: (nonce) => {
    const r = get().focusRequest
    if (!r || r.nonce !== nonce || r.taken) return false
    set({ focusRequest: { ...r, taken: true } })
    return true
  },
  releaseFocusRequest: (nonce) => {
    const r = get().focusRequest
    if (r && r.nonce === nonce && r.taken) set({ focusRequest: { ...r, taken: false } })
  },
  forgetTab: (tabId) =>
    set((s) => {
      if (!(tabId in s.recent)) return s
      const { [tabId]: _dropped, ...rest } = s.recent
      return { recent: rest }
    }),
}))

let uninstallCleanup: (() => void) | null = null

/**
 * The ONLY cleanup point (spec §8.1): drop the record of every tab that is no longer in `useTabStore.tabs`. A
 * subscription, not a hook in `closeTab`, because the tab world can also be replaced wholesale (cross-window
 * rehydrate, Profile Sync apply, standalone adoption) without going through `closeTab`.
 *
 * Called once below. Calling it again replaces the subscription rather than stacking a second one. Returns the
 * uninstall function.
 */
export function installPaneFocusCleanup(): () => void {
  uninstallCleanup?.()
  const unsubscribe = useTabStore.subscribe((next, prev) => {
    // Most tab-store writes (active tab, visit history) keep the `tabs` object.
    if (next.tabs === prev.tabs) return
    // Walk the record, not the tabs: it only holds tabs the user focused a pane in, so this stays cheap on layout
    // writes (resize, split) that replace `tabs` without removing any.
    const { recent } = usePaneFocusStore.getState()
    let gone: string[] | null = null
    for (const tabId in recent) {
      if (!Object.hasOwn(next.tabs, tabId)) (gone ??= []).push(tabId)
    }
    if (!gone) return
    const kept = { ...recent }
    for (const tabId of gone) delete kept[tabId]
    usePaneFocusStore.setState({ recent: kept })
  })
  const uninstall = () => {
    unsubscribe()
    if (uninstallCleanup === uninstall) uninstallCleanup = null
  }
  uninstallCleanup = uninstall
  return uninstall
}

installPaneFocusCleanup()

// HMR: a hot reload of this module must not leave the old subscription running against the old store.
if (import.meta.hot) {
  import.meta.hot.dispose(() => uninstallCleanup?.())
}

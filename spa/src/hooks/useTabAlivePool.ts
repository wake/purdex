import { useEffect, useRef, useMemo } from 'react'
import { useUISettingsStore } from '../stores/useUISettingsStore'

// Light tabs are cheap relative to terminals but NOT free (each keeps a Monaco /
// Tiptap instance, image/PDF Blob URL, etc.). Keep the most-recently-visited N
// alive — enough that the working set survives tab switches without a remount —
// while bounding memory instead of retaining every light tab ever opened.
export const LIGHT_KEEP_ALIVE_MAX = 8

interface MinimalTab {
  id: string
  pinned: boolean
  // Light tabs (no terminal/browser pane) bypass keepAliveCount and are kept
  // alive up to LIGHT_KEEP_ALIVE_MAX (most-recent-first) so their state survives
  // tab switches without a remount. Absent → treated as heavy (preserves
  // callers/tests that don't classify their tabs).
  light?: boolean
}

export function useTabAlivePool(activeTabId: string | null, tabs: MinimalTab[]) {
  const keepAliveCount = useUISettingsStore((s) => s.keepAliveCount)
  const keepAlivePinned = useUISettingsStore((s) => s.keepAlivePinned)
  const settingsVersion = useUISettingsStore((s) => s.terminalSettingsVersion)

  // LRU history: most-recent-first, full visit order
  const historyRef = useRef<string[]>([])
  const prevVersionRef = useRef(settingsVersion)
  const prevKeepAliveRef = useRef(keepAliveCount)

  // Clear history on settings version bump or keepAliveCount change
  // (synchronous, before useMemo to ensure fresh data)
  if (settingsVersion !== prevVersionRef.current) {
    historyRef.current = []
    prevVersionRef.current = settingsVersion
  }
  if (keepAliveCount !== prevKeepAliveRef.current) {
    historyRef.current = []
    prevKeepAliveRef.current = keepAliveCount
  }

  // Remove closed tabs from history (#6: no backfill for closed tabs)
  const validIds = useMemo(() => new Set(tabs.map((t) => t.id)), [tabs])
  historyRef.current = historyRef.current.filter((id) => validIds.has(id))

  // Mutate history during render (not useEffect) so useMemo reads fresh data.
  // Guards ensure idempotency across React StrictMode double-render.
  if (activeTabId) {
    const h = historyRef.current
    const idx = h.indexOf(activeTabId)
    if (idx !== 0) {
      if (idx > 0) h.splice(idx, 1)
      h.unshift(activeTabId)
    }
  }

  const tabMap = useMemo(() => new Map(tabs.map((t) => [t.id, t])), [tabs])

  const aliveIds = useMemo(() => {
    const h = historyRef.current

    // Heavy tabs (terminal/browser) follow the keepAliveCount LRU exactly as
    // before. Light tabs are excluded from that bound and appended afterwards,
    // so with no light tabs the result is byte-identical to the original.
    let heavyAlive: string[]
    if (keepAliveCount === 0) {
      heavyAlive = activeTabId && !tabMap.get(activeTabId)?.light ? [activeTabId] : []
    } else {
      const alive: string[] = []
      const pinnedAlive: string[] = []
      // keepAliveCount is the number of INACTIVE heavy tabs kept. The active tab
      // only claims one of those slots when it is itself heavy; when the active
      // tab is light it is kept alive separately and must not shrink the heavy
      // budget. (With no light tabs the active tab is heavy → +1 → byte-identical
      // to the original.)
      const activeIsHeavy = activeTabId ? !tabMap.get(activeTabId)?.light : false
      const maxNormal = keepAliveCount + (activeIsHeavy ? 1 : 0)
      let normalCount = 0

      for (const id of h) {
        const tab = tabMap.get(id)
        if (!tab || tab.light) continue
        if (keepAlivePinned && tab.pinned) {
          // Pinned tabs kept alive separately, don't count toward normal limit
          pinnedAlive.push(id)
        } else if (normalCount < maxNormal) {
          alive.push(id)
          normalCount++
        }
      }
      heavyAlive = [...pinnedAlive, ...alive]
    }

    // Light tabs bypass keepAliveCount but are LRU-bounded to
    // LIGHT_KEEP_ALIVE_MAX (most-recent-first via history) so retention is not
    // unbounded. The active tab is always the head of history, so if it is light
    // it is always within the budget → always alive.
    const seen = new Set(heavyAlive)
    const result = [...heavyAlive]
    let lightCount = 0
    for (const id of h) {
      if (lightCount >= LIGHT_KEEP_ALIVE_MAX) break
      const tab = tabMap.get(id)
      if (!tab || !tab.light || seen.has(id)) continue
      seen.add(id)
      result.push(id)
      lightCount++
    }
    return result
    // historyRef.current is mutated synchronously above, not a reactive dep
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTabId, keepAliveCount, keepAlivePinned, tabMap, settingsVersion])

  // Trim history to prevent unbounded growth WITHOUT dropping any tab that could
  // still be kept alive. A plain length cap is wrong here: light and heavy tabs
  // share one history, so after enough heavy visits a still-recent light tab
  // would slide past the cut and be forgotten (then remount, losing its state).
  // Instead keep, newest-first, the LIGHT_KEEP_ALIVE_MAX most-recent light tabs,
  // a generous heavy buffer, and all pinned tabs; drop the rest.
  useEffect(() => {
    const h = historyRef.current
    const heavyBudget = Math.max(keepAliveCount + 10, 20)
    let lightKept = 0
    let heavyKept = 0
    const kept: string[] = []
    for (const id of h) {
      const tab = tabMap.get(id)
      if (!tab) continue // closed tab (already filtered at render, defensive)
      if (keepAlivePinned && tab.pinned) {
        kept.push(id)
      } else if (tab.light) {
        if (lightKept < LIGHT_KEEP_ALIVE_MAX) { kept.push(id); lightKept++ }
      } else if (heavyKept < heavyBudget) {
        kept.push(id); heavyKept++
      }
    }
    if (kept.length !== h.length) historyRef.current = kept
  }, [activeTabId, keepAliveCount, keepAlivePinned, tabMap])

  return { aliveIds, poolVersion: settingsVersion }
}

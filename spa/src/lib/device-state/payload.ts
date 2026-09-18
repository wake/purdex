import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { scanPaneTree } from '../pane-tree'
import { sha256Hex } from '../crypto-hash'
import type { SessionMeta, WorkspaceSnapshot } from '../snapshot/types'
import type { Tab } from '../../types/tab'

/**
 * Tabs in iteration order: `tabOrder` first, then any tab present in `tabs`
 * but missing from `tabOrder` (insertion order). Each tab appears once.
 */
function orderedTabs(tabs: Record<string, Tab>, tabOrder: string[]): Tab[] {
  const seen = new Set<string>()
  const out: Tab[] = []
  for (const id of tabOrder) {
    const t = tabs[id]
    if (t && !seen.has(id)) {
      seen.add(id)
      out.push(t)
    }
  }
  for (const [id, t] of Object.entries(tabs)) {
    if (!seen.has(id)) {
      seen.add(id)
      out.push(t)
    }
  }
  return out
}

/**
 * Build the device-state backup payload from the current tab + workspace
 * stores (spec §3.3). Synchronous and network-free — it runs on every debounce
 * tick, so unlike `buildSnapshot` it never calls `listSessions` /
 * `fetchSessionCwd`. `sessionMeta` is structure-only (`restorable: false`).
 */
export function buildDeviceStatePayload(now: number): WorkspaceSnapshot {
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()

  const sessionMeta: Record<string, Record<string, SessionMeta>> = {}
  for (const t of orderedTabs(tabs, tabOrder)) {
    scanPaneTree(t.layout, (pane) => {
      if (pane.content.kind !== 'tmux-session') return
      const { hostId, sessionCode, cachedName, rebuild } = pane.content
      const perHost = (sessionMeta[hostId] ??= {})
      if (perHost[sessionCode]) return // first pane wins
      const meta: SessionMeta = { hostId, sessionCode, name: cachedName, mode: 'terminal', restorable: false }
      const cwd = rebuild?.cwd
      if (typeof cwd === 'string' && cwd !== '') meta.cwd = cwd
      perHost[sessionCode] = meta
    })
  }

  return {
    version: 1,
    capturedAt: now,
    tabs,
    tabOrder,
    activeTabId,
    workspaces,
    activeWorkspaceId,
    sessionMeta,
  }
}

function sortKeysDeep(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeysDeep)
  if (v !== null && typeof v === 'object') {
    const src = v as Record<string, unknown>
    const out: Record<string, unknown> = {}
    for (const k of Object.keys(src).sort()) out[k] = sortKeysDeep(src[k])
    return out
  }
  return v
}

/**
 * Stable JSON of the payload: object keys sorted recursively (arrays keep
 * their order), top-level `capturedAt` excluded — so two payloads that differ
 * only in capture time or key insertion order produce the same key.
 */
export function structuralKey(snap: WorkspaceSnapshot): string {
  const { capturedAt: _capturedAt, ...rest } = snap
  return JSON.stringify(sortKeysDeep(rest))
}

/** SHA-256 hex (64 lowercase chars) of `structuralKey(snap)`. */
export async function hashPayload(snap: WorkspaceSnapshot): Promise<string> {
  return sha256Hex(new TextEncoder().encode(structuralKey(snap)))
}

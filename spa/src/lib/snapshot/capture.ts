import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { listSessions } from '../host-api'
import { scanPaneTree } from '../pane-tree'
import { writeSnapshot } from './storage'
import type { CaptureResult, SessionMeta, WorkspaceSnapshot } from './types'
import type { Tab } from '../../types/tab'

interface CapturedPaneRef {
  hostId: string
  sessionCode: string
  mode: 'terminal' | 'stream'
  cachedName: string
}

function collectTmuxPanesByHost(tabs: Record<string, Tab>): Map<string, CapturedPaneRef[]> {
  const byHost = new Map<string, CapturedPaneRef[]>()
  for (const t of Object.values(tabs)) {
    scanPaneTree(t.layout, (pane) => {
      if (pane.content.kind !== 'tmux-session') return
      const { hostId, sessionCode, mode, cachedName } = pane.content
      const refs = byHost.get(hostId)
      if (refs) {
        refs.push({ hostId, sessionCode, mode, cachedName })
      } else {
        byHost.set(hostId, [{ hostId, sessionCode, mode, cachedName }])
      }
    })
  }
  return byHost
}

/**
 * Build a WorkspaceSnapshot from the current tab + workspace store state, plus
 * one `listSessions` call per host to resolve name/cwd/restorable for each
 * captured tmux-session pane.
 *
 * B1: this function MUST NOT write to storage — callers decide when/where to
 * persist the resulting snapshot (see captureSnapshot below, and T7's
 * pre-restore `-prev` backup which must build without touching the primary key).
 */
export async function buildSnapshot(now: number): Promise<WorkspaceSnapshot> {
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()

  const byHost = collectTmuxPanesByHost(tabs)
  const sessionMeta: Record<string, Record<string, SessionMeta>> = {}

  for (const [hostId, refs] of byHost) {
    const perHost: Record<string, SessionMeta> = {}
    try {
      const sessions = await listSessions(hostId)
      for (const ref of refs) {
        const live = sessions.find((s) => s.code === ref.sessionCode)
        perHost[ref.sessionCode] =
          live && live.cwd
            ? {
                hostId,
                sessionCode: ref.sessionCode,
                name: live.name,
                mode: ref.mode,
                cwd: live.cwd,
                currentCommand: live.current_command,
                restorable: true,
              }
            : live
              ? {
                  // Live but no usable cwd: keep structure only, not restorable
                  // (spec line 97 — cwd unknown must not feed createSession).
                  // cwd stays undefined (spec line 76: not-captured means
                  // undefined, never empty string); restorable stays false.
                  hostId,
                  sessionCode: ref.sessionCode,
                  name: live.name,
                  mode: ref.mode,
                  cwd: undefined,
                  currentCommand: live.current_command,
                  restorable: false,
                }
              : {
                  hostId,
                  sessionCode: ref.sessionCode,
                  name: ref.cachedName,
                  mode: ref.mode,
                  cwd: undefined,
                  restorable: false,
                  captureError: 'session-dead-at-capture',
                }
      }
    } catch {
      for (const ref of refs) {
        perHost[ref.sessionCode] = {
          hostId,
          sessionCode: ref.sessionCode,
          name: ref.cachedName,
          mode: ref.mode,
          cwd: undefined,
          restorable: false,
          captureError: 'host-unreachable',
        }
      }
    }
    sessionMeta[hostId] = perHost
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

/** buildSnapshot + write the primary snapshot key + return capture stats. */
export async function captureSnapshot(now: number): Promise<CaptureResult> {
  const snap = await buildSnapshot(now)
  writeSnapshot(snap)

  let total = 0
  let resolved = 0
  let unresolved = 0
  for (const perHost of Object.values(snap.sessionMeta)) {
    for (const meta of Object.values(perHost)) {
      total++
      if (meta.restorable) resolved++
      else unresolved++
    }
  }
  return { total, resolved, unresolved }
}

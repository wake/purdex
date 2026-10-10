// Shared seed helpers for the StatusBar.*.test.tsx files (split from StatusBar.test.tsx, #2119).
// `vi.mock` is hoisted per test file and cannot be shared through an import, so every test file that uses
// these helpers declares `vi.mock('../lib/copy-text', ...)` itself; `copyTextMock` below relies on it.
import { vi } from 'vitest'
import { createTab } from '../types/tab'
import type { Tab, PaneContent } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { emptyPeerHostEntry, usePeerStore, type PeerHostEntry, type PeerRow } from '../stores/usePeerStore'
import { emptySessionCwdEntry, useSessionCwdStore, type SessionCwdEntry } from '../stores/useSessionCwdStore'
import { copyText } from '../lib/copy-text'

export const copyTextMock = vi.mocked(copyText)

export const HOST_ID = 'test-host'
/**
 * The tmux server generation. The pane's own generation comes from
 * `Session.tmux_instance`, and only a peer row stamped with the same one is
 * this pane's — tmux reuses `$N`, and a session code is `$N` re-encoded.
 */
export const GEN = '6901:1789205013'

// The peer stores are stubbed for *every* test in this file, not only the new
// ones: `usePeerInfo` runs on every render of a session status bar, so an
// un-stubbed store would put a real `fetch` behind the existing tests.
export const peerRefresh = vi.fn(async (_hostId: string) => {})
export const cwdRefresh = vi.fn(async (_hostId: string, _code: string) => {})

// Pre-populate stores for tests
export function setupStores() {
  useSessionStore.setState({
    sessions: {
      [HOST_ID]: [
        { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', tmux_instance: GEN },
      ],
    },
    activeHostId: HOST_ID,
    activeCode: null,
  })
  useHostStore.setState({
    hosts: {
      [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
    },
    hostOrder: [HOST_ID],
    runtime: {
      [HOST_ID]: { status: 'connected' as const },
    },
    activeHostId: HOST_ID,
  })
  useAgentStore.setState({ agentTypes: {}, oscTitles: {}, models: {}, lastEvents: {}, statuses: {}, unread: {}, subagents: {} })
  useUISettingsStore.setState({ showAgentTitleInStatusBar: false })
  peerRefresh.mockReset()
  cwdRefresh.mockReset()
  copyTextMock.mockClear()
  copyTextMock.mockResolvedValue(undefined)
  usePeerStore.setState({ byHost: {}, refresh: peerRefresh })
  useSessionCwdStore.setState({ byHost: {}, refresh: cwdRefresh })
  useShownHostsStore.setState({ ids: [HOST_ID] }) // shown in this workbench (H2d-4)
}

export const PEER_ROW: PeerRow = {
  address: 'mlab/purdex-b0',
  ref: '_q34psn',
  // A v4 title is free text, routes nothing, and is usually unset. The row is
  // identified by its name and its ref, so the fixture leaves it empty; the one
  // test that cares about a title sets it.
  title: '',
  titleSource: '',
  deliverable: true,
  reason: '',
  tmuxInstance: GEN,
  // Peer Address v5: the address carries the conversation's virtual name
  // (`purdex-b0`); `peerName` is Claude Code's own session name, which
  // changes on every start and routes nothing, so it differs on purpose.
  agent: { type: 'cc', peerName: 'ai-chat-story-3a', status: 'idle' },
}

/** Seed one host as already answered, so nothing in the policy has work left. */
export function seedPeers(entry: Partial<PeerHostEntry> = {}, row: PeerRow | null = PEER_ROW) {
  usePeerStore.setState({
    byHost: {
      [HOST_ID]: {
        ...emptyPeerHostEntry(),
        rows: row ? { dev001: row } : {},
        fetchedAt: Date.now(),
        ...entry,
      },
    },
  })
}

export function seedCwd(cwd: string, entry: Partial<SessionCwdEntry> = {}) {
  useSessionCwdStore.setState({
    byHost: { [HOST_ID]: { dev001: { ...emptySessionCwdEntry(), cwd, fetchedAt: Date.now(), ...entry } } },
  })
}

export function sessionTab(id = 't1'): Tab {
  return makeTab(id, { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
}

export function makeTab(id: string, content: PaneContent): Tab {
  const tab = createTab(content)
  return { ...tab, id }
}

// spa/src/lib/rebuild/ws-sessions.test.ts — the WS `sessions` frame, ordered by
// its version (#1255 SPA spec §3.3). Real stores and the real reconciliation:
// "not reconciled" is asserted on what it would have written.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { Session } from '../host-api'
import type { HostEvent } from '../host-events'
import type { Tab, TmuxSessionContent } from '../../types/tab'
import { closeAttachGate, openAttachGate } from './attach-gate'
import { __resetForTests, heldVersion, note } from './session-version'
import { handleSessionsFrame } from './ws-sessions'

vi.mock('./cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('./provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))
// The recovery path itself is refresh-sessions.test.ts's; here only "was it asked for".
const { recoverHostSessions } = vi.hoisted(() => ({ recoverHostSessions: vi.fn(async () => {}) }))
vi.mock('./refresh-sessions', () => ({ recoverHostSessions }))

const H = 'h1'
const E1 = '9f3c1a0b7d2e4c61'
const E2 = '0123456789abcdef'
const S: Session = { code: 'sss111', name: 'dev', cwd: '', mode: 'terminal', tmux_instance: '111:1000' }

function frame(sessions: Session[], version?: { epoch: string; seq: number }): HostEvent {
  return { type: 'sessions', session: '', value: JSON.stringify(sessions), ...version }
}

function seedLivePane(): void {
  const content: TmuxSessionContent = {
    kind: 'tmux-session', hostId: H, sessionCode: 'sss111', mode: 'terminal',
    cachedName: 'dev', tmuxInstance: '111:1000',
  }
  const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: 'p1', content } } }
  useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
}

function pane(): TmuxSessionContent {
  const layout = useTabStore.getState().tabs.t1.layout
  if (layout.type !== 'leaf' || layout.pane.content.kind !== 'tmux-session') throw new Error('fixture')
  return layout.pane.content
}

const realReplaceHost = useSessionStore.getState().replaceHost

const attachReady = () => useHostStore.getState().runtime[H]?.attachReady

beforeEach(() => {
  __resetForTests()
  recoverHostSessions.mockClear()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: {}, activeHostId: H,
  })
  useSessionStore.setState({ sessions: {}, replaceHost: realReplaceHost })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  seedLivePane()
})

afterEach(() => { useHostStore.getState().reset() })

describe('handleSessionsFrame', () => {
  it('a versioned frame is reconciled, then its version is held', () => {
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 5 }))
    expect(useSessionStore.getState().sessions[H]).toEqual([S])
    expect(attachReady()).toBe(true)
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 5 })
  })

  it('an older frame after a newer one is not reconciled (gate open)', () => {
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 9 }))
    const before = useSessionStore.getState().sessions[H]
    expect(attachReady()).toBe(true)

    handleSessionsFrame(H, frame([], { epoch: E1, seq: 8 })) // would close S
    expect(pane().terminated).toBeUndefined()
    expect(useSessionStore.getState().sessions[H]).toBe(before)
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 9 })
  })

  it('an equal-seq frame is the same read: not reconciled again', () => {
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 9 }))
    handleSessionsFrame(H, frame([], { epoch: E1, seq: 9 }))
    expect(pane().terminated).toBeUndefined()
  })

  it('a stale frame with the gate closed is dropped and the gate stays closed (codex #2)', () => {
    note(H, { epoch: E1, seq: 9 })
    closeAttachGate(H)

    handleSessionsFrame(H, frame([], { epoch: E1, seq: 3 }))
    expect(pane().terminated).toBeUndefined()
    expect(useSessionStore.getState().sessions[H]).toBeUndefined()
    expect(attachReady()).toBe(false)
  })

  it('a newer frame closes the session it no longer lists', () => {
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 9 }))
    handleSessionsFrame(H, frame([], { epoch: E1, seq: 10 }))
    expect(pane().terminated).toBe('session-closed')
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 10 })
  })

  it('a frame of another daemon process (different epoch) on the current socket is applied', () => {
    note(H, { epoch: E1, seq: 90 })
    handleSessionsFrame(H, frame([S], { epoch: E2, seq: 1 }))
    expect(useSessionStore.getState().sessions[H]).toEqual([S])
    expect(heldVersion(H)).toEqual({ epoch: E2, seq: 1 })
  })

  // Claim before apply (codex adversarial F2): the reconciliation is not
  // transactional — it may have written part of the list before it threw — so
  // an older list must never get in after it, and a fresh refresh recovers.
  it('a reconcile that throws still holds its version, leaves the gate closed and asks for a recovery refresh', () => {
    note(H, { epoch: E1, seq: 4 })
    closeAttachGate(H)
    useSessionStore.setState({ replaceHost: () => { throw new Error('quota') } } as never)

    expect(() => handleSessionsFrame(H, frame([S], { epoch: E1, seq: 5 }))).not.toThrow()
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 5 })
    expect(attachReady()).toBe(false)
    expect(recoverHostSessions).toHaveBeenCalledTimes(1)
    expect(recoverHostSessions).toHaveBeenCalledWith(H)
  })

  it('after seq 6 threw mid-way, a late seq 5 is not reconciled; seq 7 is', () => {
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 4 }))
    useSessionStore.setState({ replaceHost: () => { throw new Error('quota') } } as never)
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 6 }))
    useSessionStore.setState({ replaceHost: realReplaceHost })

    handleSessionsFrame(H, frame([], { epoch: E1, seq: 5 })) // older than the claimed 6: would close S
    expect(pane().terminated).toBeUndefined()
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 6 })

    handleSessionsFrame(H, frame([], { epoch: E1, seq: 7 }))
    expect(pane().terminated).toBe('session-closed')
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 7 })
  })

  it('a reconcile that succeeds asks for no recovery', () => {
    handleSessionsFrame(H, frame([S], { epoch: E1, seq: 4 }))
    expect(recoverHostSessions).not.toHaveBeenCalled()
  })

  it('an unversioned frame is reconciled as today and clears held', () => {
    note(H, { epoch: E1, seq: 40 })
    handleSessionsFrame(H, frame([]))
    expect(pane().terminated).toBe('session-closed')
    expect(heldVersion(H)).toBeNull()
  })

  // codex adversarial F4: an unversioned frame clears `held` only once it has
  // proved to be a list AND been reconciled.
  it.each([['{}'], ['null'], ['"x"'], ['42'], ['{"sessions":[]}']])(
    'an unversioned frame whose value is not an array (%s) is ignored: held kept, nothing reconciled',
    (value) => {
      note(H, { epoch: E1, seq: 4 })
      handleSessionsFrame(H, { type: 'sessions', session: '', value })
      expect(heldVersion(H)).toEqual({ epoch: E1, seq: 4 })
      expect(useSessionStore.getState().sessions[H]).toBeUndefined()
      expect(attachReady()).toBeUndefined()
      expect(recoverHostSessions).not.toHaveBeenCalled()
    },
  )

  it('a versioned frame whose value is not an array is ignored: nothing claimed, nothing reconciled', () => {
    note(H, { epoch: E1, seq: 4 })
    handleSessionsFrame(H, { type: 'sessions', session: '', value: '{}', epoch: E1, seq: 5 })
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 4 })
    expect(useSessionStore.getState().sessions[H]).toBeUndefined()
  })

  it('an unversioned frame whose reconcile throws keeps held and asks for a recovery refresh', () => {
    note(H, { epoch: E1, seq: 40 })
    useSessionStore.setState({ replaceHost: () => { throw new Error('quota') } } as never)
    expect(() => handleSessionsFrame(H, frame([]))).not.toThrow()
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 40 })
    expect(recoverHostSessions).toHaveBeenCalledWith(H)
  })

  it('a frame whose value cannot be parsed changes nothing', () => {
    openAttachGate(H)
    note(H, { epoch: E1, seq: 4 })
    handleSessionsFrame(H, { type: 'sessions', session: '', value: 'not-json', epoch: E1, seq: 5 })
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 4 })
    expect(useSessionStore.getState().sessions[H]).toBeUndefined()
  })
})

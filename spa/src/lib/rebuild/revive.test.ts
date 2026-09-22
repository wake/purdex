// spa/src/lib/rebuild/revive.test.ts — decideRevive, reviveAllowed and the
// pass that applies them (spec §3.1 / §3.2).
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { decideRevive, reviveAllowed, runRevivePass, noteReconciledSessions } from './revive'
import { STORAGE_KEYS } from '../storage/keys'
import type { ReviveCandidate } from './revive'
import type { Session } from '../host-api'
import { useRebuildStore, type RebuildBinding, type RebuildOperation } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneRebuildRecord, Tab, TmuxSessionContent } from '../../types/tab'

/** A full `Session`, so the dep-injected fakes stay type-checked (copied from engine.test.ts:18). */
function session(over: Partial<Session>): Session {
  return {
    code: 'c', name: 'n', cwd: '', mode: 'terminal',
    ...over,
  }
}

function candidate(over: Partial<ReviveCandidate> = {}): ReviveCandidate {
  return {
    hostId: 'h1', tabId: 't1', paneId: 'p1',
    sessionCode: 'old', tmuxInstance: '111:1000', cachedName: 'dev',
    ...over,
  }
}

function op(over: Partial<RebuildOperation> & { binding: RebuildBinding }): RebuildOperation {
  return {
    paneId: 'p1', tabId: 't', hostId: 'h1',
    plan: { createSession: true, applyCwd: true, runResume: true },
    resumeCommand: '', status: 'done',
    report: {
      hostId: 'h1',
      steps: { create: { status: 'skipped' }, resume: { status: 'skipped' }, repoint: { status: 'skipped' } },
      repointed: false,
    },
    startedAt: 0,
    ...over,
  }
}

describe('decideRevive', () => {
  it('S1: revives when a live session shares the cached name', () => {
    const cand = candidate()
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' })
    const decisions = decideRevive('h1', [live], [cand])
    expect(decisions).toHaveLength(1)
    expect(decisions[0]).toEqual({
      tabId: 't1',
      paneId: 'p1',
      binding: { hostId: 'h1', sessionCode: 'old', tmuxInstance: '111:1000' },
      session: live,
    })
    expect(decisions[0].session).toBe(live)
  })

  it('S1c: revives even when the live instance matches the candidate\'s own', () => {
    const cand = candidate({ tmuxInstance: '111:1000' })
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '111:1000' })
    const decisions = decideRevive('h1', [live], [cand])
    expect(decisions).toHaveLength(1)
  })

  it('S4: rejects when the live instance is empty', () => {
    const cand = candidate()
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '' })
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it('S4: rejects when the instance key is absent', () => {
    const cand = candidate()
    const live = session({ code: 'abc123', name: 'dev' })
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it.each([
    ['a number', 123],
    ['a boolean', true],
    ['an object', {}],
  ])('S21: rejects when the live instance is %s on the wire', (_label, tmux_instance) => {
    const cand = candidate()
    const live = { ...session({ code: 'abc123', name: 'dev' }), tmux_instance } as unknown as Session
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it.each([
    ['null', { code: null }],
    ['absent', {}],
    ['empty', { code: '' }],
    ['a number', { code: 123 }],
  ])('S22: rejects when the live code is %s on the wire', (_label, code) => {
    const cand = candidate()
    const { code: _code, ...rest } = session({ name: 'dev', tmux_instance: '222:2000' })
    const live = { ...rest, ...code } as unknown as Session
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it('S5: rejects when only a differently-named session is live', () => {
    const cand = candidate()
    const live = session({ code: 'abc123', name: 'dev-2', tmux_instance: '222:2000' })
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it('S6: revives when a pre-P-D.2 daemon still reports the live session as stream (codex F1)', () => {
    const cand = candidate()
    // Legacy input: a pre-P-D.2 daemon still reports 'stream'.
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000', mode: 'stream' })
    expect(decideRevive('h1', [live], [cand])).toHaveLength(1)
  })

  it('S6: revives when mode is absent from the payload', () => {
    const cand = candidate()
    const { mode: _mode, ...rest } = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' })
    const live = rest as unknown as Session
    expect(decideRevive('h1', [live], [cand])).toHaveLength(1)
  })

  it('S6: revives when mode is present but null — mode is not part of the binding (codex F1)', () => {
    const cand = candidate()
    const live = { ...session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }), mode: null } as unknown as Session
    expect(decideRevive('h1', [live], [cand])).toHaveLength(1)
  })

  it('S13: ignores candidates on a different host', () => {
    const cand = candidate({ hostId: 'h2' })
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' })
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it('S14: two candidates with the same cached name both revive against the same session', () => {
    const cand1 = candidate({ paneId: 'p1', tabId: 't1' })
    const cand2 = candidate({ paneId: 'p2', tabId: 't2' })
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' })
    const decisions = decideRevive('h1', [live], [cand1, cand2])
    expect(decisions).toHaveLength(2)
    expect(decisions[0].session).toBe(live)
    expect(decisions[1].session).toBe(live)
  })

  it('returns empty for empty candidates', () => {
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' })
    expect(decideRevive('h1', [live], [])).toEqual([])
  })

  it('returns empty for empty sessions', () => {
    expect(decideRevive('h1', [], [candidate()])).toEqual([])
  })

  it('name beats code: matches by name even when a different session holds the old code', () => {
    const cand = candidate({ sessionCode: 'old', cachedName: 'dev' })
    const stale = session({ code: 'old', name: 'other', tmux_instance: '222:2000' })
    const fresh = session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })
    const decisions = decideRevive('h1', [stale, fresh], [cand])
    expect(decisions).toHaveLength(1)
    expect(decisions[0].session).toBe(fresh)
  })
})

describe('reviveAllowed', () => {
  const binding: RebuildBinding = { hostId: 'h1', sessionCode: 'old', tmuxInstance: '111:1000' }
  const otherBinding: RebuildBinding = { hostId: 'h1', sessionCode: 'old', tmuxInstance: '999:9999' }

  it.each([
    ['no operation for the pane', {}, true],
    ['operation binding differs (earlier cycle)', { p1: op({ binding: otherBinding }) }, true],
    ['operation running on the same binding', { p1: op({ binding, status: 'running' }) }, false],
    ['operation done with a createdSession', { p1: op({ binding, status: 'done', createdSession: session({}) }) }, false],
    ['operation done without a createdSession', { p1: op({ binding, status: 'done' }) }, true],
  ])('%s -> %s', (_label, operations, expected) => {
    expect(reviveAllowed('p1', binding, operations as Record<string, RebuildOperation>)).toBe(expected)
  })
})

// ---------------------------------------------------------------------------
// runRevivePass — the pass over the stores (spec §3.2)
// ---------------------------------------------------------------------------

const dead: RebuildBinding = { hostId: 'h1', sessionCode: 'old111', tmuxInstance: '111:1000' }
const live = session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })

/** One leaf tab holding a `tmux-restarted` pane (shape copied from engine.test.ts `seedPane`). */
function seedPane(tabId: string, paneId: string, record: Partial<PaneRebuildRecord> | null = {}, hostId = 'h1') {
  const tab: Tab = {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: paneId, content: {
      kind: 'tmux-session', hostId, sessionCode: 'old111', mode: 'terminal',
      cachedName: 'dev', tmuxInstance: '111:1000', terminated: 'tmux-restarted',
      rebuild: record === null ? undefined : {
        sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1,
        agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
        ...record,
      },
    } } },
  }
  const prev = useTabStore.getState()
  useTabStore.setState({ tabs: { ...prev.tabs, [tabId]: tab }, tabOrder: [...prev.tabOrder, tabId], activeTabId: tabId })
}

function paneContent(tabId: string, paneId: string): TmuxSessionContent {
  const layout = useTabStore.getState().tabs[tabId].layout
  if (layout.type !== 'leaf' || layout.pane.id !== paneId) throw new Error('fixture is a leaf')
  const c = layout.pane.content
  if (c.kind !== 'tmux-session') throw new Error('fixture is a tmux pane')
  return c
}

const deadContent = { sessionCode: 'old111', tmuxInstance: '111:1000', terminated: 'tmux-restarted' }
const revivedContent = { sessionCode: 'new1', tmuxInstance: '222:2000', cachedName: 'dev' }

describe('runRevivePass', () => {
  beforeEach(() => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, token: null, order: 0 } },
      hostOrder: ['h1'], activeHostId: 'h1',
      runtime: { h1: { status: 'connected', attachReady: true } },
    })
    // The handler writes both; the pass reads only the snapshot.
    useSessionStore.setState({ sessions: { h1: [live] }, activeHostId: null, activeCode: null })
    noteReconciledSessions('h1', [live])
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  })

  it('reads the reconciled snapshot, not the session store', () => {
    seedPane('t1', 'p1')
    // A late `fetchHost` response: the store now names a stale session `dev`.
    useSessionStore.setState({ sessions: { h1: [session({ code: 'stale', name: 'dev', tmux_instance: '333:3000' })] } })
    noteReconciledSessions('h1', [live])
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(revivedContent)
  })

  it('does nothing when the store has the match but no payload was ever reconciled for the host', () => {
    useHostStore.getState().setRuntime('h2', { status: 'connected', attachReady: true })
    useSessionStore.setState({ sessions: { h2: [live] } })
    seedPane('t1', 'p1', {}, 'h2')
    runRevivePass('h2')
    expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
  })

  it('S1: revives a tmux-restarted pane onto the live session of the same name', () => {
    seedPane('t1', 'p1')
    runRevivePass('h1')
    const c = paneContent('t1', 'p1')
    expect(c).toMatchObject(revivedContent)
    expect(c.terminated).toBeUndefined()
    expect(c.rebuild).toMatchObject({
      sessionName: 'dev', tmuxInstance: '222:2000', cwd: '/w',
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
    })
  })

  it('S16: does nothing while the host attach gate is closed', () => {
    seedPane('t1', 'p1')
    useHostStore.getState().setRuntime('h1', { attachReady: false })
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
  })

  it('S7: does nothing while any operation holds the lock, even for a pane with no op', () => {
    seedPane('t1', 'p1')
    useRebuildStore.setState({ lockedBy: 'rebuild:p9' })
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
  })

  it('S8: leaves a pane whose done operation created a session (its panel shows the report)', () => {
    seedPane('t1', 'p1')
    useRebuildStore.setState({ operations: { p1: op({ paneId: 'p1', binding: dead, createdSession: live }) } })
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
  })

  it('S9: revives a pane whose done operation created nothing', () => {
    seedPane('t1', 'p1')
    useRebuildStore.setState({ operations: { p1: op({ paneId: 'p1', binding: dead }) } })
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(revivedContent)
  })

  it('S15: a second pass over the same evidence rewrites nothing', () => {
    seedPane('t1', 'p1')
    runRevivePass('h1')
    const c = paneContent('t1', 'p1')
    expect(c).toMatchObject(revivedContent)
    const record = c.rebuild
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toBe(c)
    expect(paneContent('t1', 'p1').rebuild).toBe(record)
  })

  it('S22: a null code is no binding; the next payload with a real code revives', () => {
    seedPane('t1', 'p1')
    const nullCode = { ...session({ name: 'dev', tmux_instance: '222:2000' }), code: null } as unknown as Session
    noteReconciledSessions('h1', [nullCode])
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(deadContent)

    noteReconciledSessions('h1', [live])
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject(revivedContent)
  })

  it('S18: a same-generation revive keeps the pane\'s old code in the session list', () => {
    // `old111` is now somebody else's live session at the very generation the
    // pane died on; the engine's sync would evict it, the revive must not.
    const sameGen = [
      session({ code: 'old111', name: 'other', tmux_instance: '111:1000' }),
      session({ code: 'new1', name: 'dev', tmux_instance: '111:1000' }),
    ]
    useSessionStore.setState({ sessions: { h1: sameGen } })
    noteReconciledSessions('h1', sameGen)
    seedPane('t1', 'p1')
    runRevivePass('h1')
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'new1', tmuxInstance: '111:1000', cachedName: 'dev' })
    expect(useSessionStore.getState().sessions.h1.map((s) => s.code)).toEqual(['old111', 'new1'])
  })

  it('S20: a write that throws leaves that pane terminated and the rest of the pass runs', () => {
    seedPane('t1', 'p1')
    seedPane('t2', 'p2')
    // `repointPane` reads `getState()` on every call, so the replacement has
    // to live in the store; the persisted store's own throw is a quota error.
    const original = useTabStore.getState().setPaneContent
    let thrown = false
    useTabStore.setState({ setPaneContent: (...args) => {
      if (!thrown) { thrown = true; throw new Error('QuotaExceededError') }
      original(...args)
    } })
    try {
      runRevivePass('h1')
    } finally {
      useTabStore.setState({ setPaneContent: original })
    }
    expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
    expect(paneContent('t2', 'p2')).toMatchObject(revivedContent)
  })

  // #1255 SPA spec §3.5 (codex plan review #1): the snapshot is bound to the
  // world it was reconciled for — the world-epoch fence at that moment.
  describe('bound to the world (G6)', () => {
    const setFence = (n: number) => localStorage.setItem(STORAGE_KEYS.WORLD_EPOCH, String(n))
    beforeEach(() => localStorage.removeItem(STORAGE_KEYS.WORLD_EPOCH))
    afterEach(() => localStorage.removeItem(STORAGE_KEYS.WORLD_EPOCH))

    it('a snapshot noted before the world changed is ignored by runRevivePass', () => {
      noteReconciledSessions('h1', [live]) // no fence yet: a device that never switched
      setFence(1_700_000_000_000_000)      // the first switch ever
      seedPane('t1', 'p1')
      runRevivePass('h1')
      expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
      runRevivePass('h1')
      expect(paneContent('t1', 'p1')).toMatchObject(deadContent)
    })

    it('…and a list reconciled for the new world revives again', () => {
      setFence(10)
      noteReconciledSessions('h1', [live])
      setFence(20)
      seedPane('t1', 'p1')
      runRevivePass('h1')
      expect(paneContent('t1', 'p1')).toMatchObject(deadContent)

      noteReconciledSessions('h1', [live])
      runRevivePass('h1')
      expect(paneContent('t1', 'p1')).toMatchObject(revivedContent)
    })

    it('same world: unchanged', () => {
      setFence(10)
      noteReconciledSessions('h1', [live])
      seedPane('t1', 'p1')
      runRevivePass('h1')
      expect(paneContent('t1', 'p1')).toMatchObject(revivedContent)
    })
  })

  it('revives a pane that carries no rebuild record, and leaves it without one', () => {
    seedPane('t1', 'p1', null)
    runRevivePass('h1')
    const c = paneContent('t1', 'p1')
    expect(c).toMatchObject(revivedContent)
    expect(c.terminated).toBeUndefined()
    expect(c.rebuild).toBeUndefined()
  })
})

// spa/src/lib/rebuild/revive.test.ts — decideRevive and reviveAllowed (spec §3.1 / §3.2).
import { describe, it, expect } from 'vitest'
import { decideRevive, reviveAllowed } from './revive'
import type { ReviveCandidate } from './revive'
import type { Session } from '../host-api'
import type { RebuildBinding, RebuildOperation } from '../../stores/useRebuildStore'

/** A full `Session`, so the dep-injected fakes stay type-checked (copied from engine.test.ts:18). */
function session(over: Partial<Session>): Session {
  return {
    code: 'c', name: 'n', cwd: '', mode: 'terminal',
    cc_session_id: '', cc_model: '', has_relay: false, ...over,
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

  it('S5: rejects when only a differently-named session is live', () => {
    const cand = candidate()
    const live = session({ code: 'abc123', name: 'dev-2', tmux_instance: '222:2000' })
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it('S6: rejects when the live session mode is not terminal', () => {
    const cand = candidate()
    const live = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000', mode: 'stream' })
    expect(decideRevive('h1', [live], [cand])).toEqual([])
  })

  it('S6: revives when mode is absent from the payload', () => {
    const cand = candidate()
    const { mode: _mode, ...rest } = session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' })
    const live = rest as unknown as Session
    expect(decideRevive('h1', [live], [cand])).toHaveLength(1)
  })

  it('S6: rejects when mode is present but null (wire shape)', () => {
    const cand = candidate()
    const live = { ...session({ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }), mode: null } as unknown as Session
    expect(decideRevive('h1', [live], [cand])).toEqual([])
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

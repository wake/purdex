// spa/src/lib/nex/handoff-gate.test.ts — P-C.3b task 3: the pure "may this
// pane be handed to nex?" gate (plan Task 3; precedence from codex review 5).
import { describe, it, expect } from 'vitest'
import type { PaneContent, TmuxSessionContent } from '../../types/tab'
import { isHandoffCandidate, type HandoffGateDeps } from './handoff-gate'

function terminal(over: Partial<TmuxSessionContent> = {}): TmuxSessionContent {
  return {
    kind: 'tmux-session',
    hostId: 'h1',
    sessionCode: 'zk16vd',
    mode: 'terminal',
    cachedName: 'purdex',
    tmuxInstance: 'inst-1',
    ...over,
  }
}

function withRebuildAgent(type: string): TmuxSessionContent {
  return terminal({
    rebuild: {
      sessionName: 'purdex',
      tmuxInstance: 'inst-1',
      agent: { type, updatedAt: 1 },
      capturedAt: 1,
    },
  })
}

const ready: HandoffGateDeps = { agentType: undefined, handoffReady: true }

describe('isHandoffCandidate — agent sources, each alone', () => {
  it('live agentType "cc" alone passes', () => {
    expect(isHandoffCandidate(terminal(), { ...ready, agentType: 'cc' })).toBe(true)
  })

  it('rebuild.agent.type "cc" alone passes (even when unverified — the daemon re-checks)', () => {
    const content = withRebuildAgent('cc')
    content.rebuild!.unverified = true
    expect(isHandoffCandidate(content, ready)).toBe(true)
  })

  it('no information at all → hidden (P-D.3b: the daemon session row is no longer a source)', () => {
    // Until P-D.3b a relay id on the daemon's session row was the third
    // fallback; the daemon stopped sending it in alpha.396, and the deps no
    // longer carry a session row at all — so nothing else can say "cc".
    expect(isHandoffCandidate(terminal(), ready)).toBe(false)
  })
})

describe('isHandoffCandidate — precedence', () => {
  it('a live agentType that is not "cc" hides even with a stale cc rebuild record', () => {
    const content = withRebuildAgent('cc')
    expect(isHandoffCandidate(content, { ...ready, agentType: 'codex' })).toBe(false)
  })

  it('a live agentType "cc" wins over a rebuild record that says codex', () => {
    expect(isHandoffCandidate(withRebuildAgent('codex'), { ...ready, agentType: 'cc' })).toBe(true)
  })

  it('a rebuild record that says codex hides', () => {
    expect(isHandoffCandidate(withRebuildAgent('codex'), ready)).toBe(false)
  })

  it('an empty-string agentType counts as no live information (falls through)', () => {
    expect(isHandoffCandidate(withRebuildAgent('cc'), { ...ready, agentType: '' })).toBe(true)
  })
})

describe('isHandoffCandidate — structural checks', () => {
  const cc: HandoffGateDeps = { ...ready, agentType: 'cc' }

  it('terminated pane → hidden', () => {
    expect(isHandoffCandidate(terminal({ terminated: 'session-closed' }), cc)).toBe(false)
  })

  it('host not handoff-ready → hidden', () => {
    expect(isHandoffCandidate(terminal(), { ...cc, handoffReady: false })).toBe(false)
  })

  it('non tmux-session content → hidden', () => {
    const exec: PaneContent = { kind: 'execution', executionId: 'x' }
    expect(isHandoffCandidate(exec, cc)).toBe(false)
    expect(isHandoffCandidate({ kind: 'dashboard' }, cc)).toBe(false)
  })
})

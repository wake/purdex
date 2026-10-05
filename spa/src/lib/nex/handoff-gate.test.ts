// spa/src/lib/nex/handoff-gate.test.ts — P-C.3b task 3: the pure "may this
// pane be handed to nex?" gate (plan Task 3; precedence from codex review 5).
import { describe, it, expect } from 'vitest'
import type { PaneContent, TmuxSessionContent } from '../../types/tab'
import { handoffBlockReason, isHandoffCandidate, type HandoffGateDeps } from './handoff-gate'

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

// P6 re-review: Claude Code's exit clears the live type (`clearSession`) and marks the record `agentExited`
// (`writeExitRecord`) — so the record is the only source left, and an exited one must not keep the gate open.
describe('isHandoffCandidate — an exited agent record', () => {
  function exitedCc(): TmuxSessionContent {
    const content = withRebuildAgent('cc')
    content.rebuild!.agentExited = { at: 7_000, reason: 'session-end' }
    return content
  }

  it('a cc record with no live type and no exit → candidate (unchanged)', () => {
    expect(isHandoffCandidate(withRebuildAgent('cc'), ready)).toBe(true)
  })

  it('the same record marked exited → not a candidate, not_agent', () => {
    expect(isHandoffCandidate(exitedCc(), ready)).toBe(false)
    expect(handoffBlockReason(exitedCc(), ready)).toBe('not_agent')
    expect(handoffBlockReason(exitedCc(), { ...ready, agentType: '' })).toBe('not_agent')
  })

  it('a process-dead exit (the pid sweep) blocks the same way', () => {
    const content = exitedCc()
    content.rebuild!.agentExited = { at: 7_000, reason: 'process-dead' }
    expect(handoffBlockReason(content, ready)).toBe('not_agent')
  })

  it('a live cc still wins over an exited record (the live branch is unchanged)', () => {
    expect(handoffBlockReason(exitedCc(), { ...ready, agentType: 'cc' })).toBeNull()
  })

  it('a live non-cc still blocks', () => {
    expect(handoffBlockReason(exitedCc(), { ...ready, agentType: 'codex' })).toBe('not_agent')
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

// Shell cleanup P6 (spec §9.3): the status bar's worker / chat buttons are disabled with a title that says why, so the
// gate names its reason. `isHandoffCandidate` is exactly "no reason".
describe('handoffBlockReason', () => {
  const cc: HandoffGateDeps = { ...ready, agentType: 'cc' }

  it('a candidate → null', () => {
    expect(handoffBlockReason(terminal(), cc)).toBeNull()
    expect(handoffBlockReason(withRebuildAgent('cc'), ready)).toBeNull()
  })

  it('non tmux-session content → not_session', () => {
    expect(handoffBlockReason({ kind: 'execution', executionId: 'x' }, cc)).toBe('not_session')
    expect(handoffBlockReason({ kind: 'dashboard' }, cc)).toBe('not_session')
  })

  it('a terminated pane → terminated, before anything about its agent or host', () => {
    expect(handoffBlockReason(terminal({ terminated: 'session-closed' }), { agentType: 'codex', handoffReady: false })).toBe('terminated')
  })

  it('no Claude Code (live codex, a codex record, or nothing known) → not_agent', () => {
    expect(handoffBlockReason(withRebuildAgent('cc'), { ...ready, agentType: 'codex' })).toBe('not_agent')
    expect(handoffBlockReason(withRebuildAgent('codex'), ready)).toBe('not_agent')
    expect(handoffBlockReason(terminal(), ready)).toBe('not_agent')
  })

  it('a plain shell on a host that is not ready says not_agent: Nexen being ready would not help it', () => {
    expect(handoffBlockReason(terminal(), { agentType: undefined, handoffReady: false })).toBe('not_agent')
  })

  it('Claude Code on a host that is not handoff-ready → nex_not_ready', () => {
    expect(handoffBlockReason(terminal(), { ...cc, handoffReady: false })).toBe('nex_not_ready')
    expect(handoffBlockReason(withRebuildAgent('cc'), { agentType: '', handoffReady: false })).toBe('nex_not_ready')
  })

  it('isHandoffCandidate is exactly "no reason" over a grid of inputs', () => {
    const exited = withRebuildAgent('cc')
    exited.rebuild!.agentExited = { at: 1, reason: 'session-end' }
    const contents: PaneContent[] = [
      terminal(), withRebuildAgent('cc'), withRebuildAgent('codex'), exited, terminal({ terminated: 'tmux-restarted' }),
      { kind: 'dashboard' }, { kind: 'execution', executionId: 'x' },
    ]
    for (const content of contents) {
      for (const agentType of [undefined, '', 'cc', 'codex']) {
        for (const handoffReady of [true, false]) {
          const deps = { agentType, handoffReady }
          expect(isHandoffCandidate(content, deps)).toBe(handoffBlockReason(content, deps) === null)
        }
      }
    }
  })
})

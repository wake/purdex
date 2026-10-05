// spa/src/hooks/useHandoffCandidate.test.ts — the shared "Hand to nex" gate (shell cleanup spec §9.4): the pure
// `isHandoffCandidate` fed by the live agent type and the host's readiness, behind the hidden-host gate.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useHandoffCandidate, useHandoffGate } from './useHandoffCandidate'
import { useAgentStore } from '../stores/useAgentStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { compositeKey } from '../lib/composite-key'
import type { PaneContent, TmuxSessionContent } from '../types/tab'

const H = 'h1'
const CODE = 'zk16vd'
const tmux = (over: Partial<TmuxSessionContent> = {}): PaneContent => ({
  kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: 'purdex', tmuxInstance: 'inst-1', ...over,
})
const recordedCc = { rebuild: { sessionName: 'purdex', tmuxInstance: 'inst-1', agent: { type: 'cc', updatedAt: 1 }, capturedAt: 1 } }

function seedReady(ready = true) {
  useNexHostStore.setState({
    byHost: {
      [H]: {
        info: null,
        capabilities: { delegate: { resume_session_id: true }, sandbox_profiles: ready ? ['default', 'handoff'] : ['default'] } as never,
        phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f',
      },
    },
  })
}
const liveAgent = (type: string) => useAgentStore.setState({ agentTypes: { [compositeKey(H, CODE)]: type } })

let ensure: ReturnType<typeof vi.fn>

beforeEach(() => {
  ensure = vi.fn().mockResolvedValue(undefined)
  useNexHostStore.setState({ byHost: {}, ensure } as never)
  useAgentStore.setState({ agentTypes: {} })
  useShownHostsStore.setState({ ids: [H] })
})

describe('useHandoffCandidate', () => {
  it('a tmux pane whose live agent is cc, on a ready host → true', () => {
    seedReady()
    liveAgent('cc')
    expect(renderHook(() => useHandoffCandidate(tmux())).result.current).toBe(true)
  })

  it('the live agent decides over the rebuild record: codex → false', () => {
    seedReady()
    liveAgent('codex')
    expect(renderHook(() => useHandoffCandidate(tmux(recordedCc))).result.current).toBe(false)
  })

  it('no live agent → the rebuild record answers', () => {
    seedReady()
    expect(renderHook(() => useHandoffCandidate(tmux(recordedCc))).result.current).toBe(true)
    expect(renderHook(() => useHandoffCandidate(tmux())).result.current).toBe(false)
  })

  it('a host that is not handoff-ready → false', () => {
    seedReady(false)
    liveAgent('cc')
    expect(renderHook(() => useHandoffCandidate(tmux())).result.current).toBe(false)
  })

  it('a terminated session → false', () => {
    seedReady()
    liveAgent('cc')
    expect(renderHook(() => useHandoffCandidate(tmux({ terminated: 'session-closed' }))).result.current).toBe(false)
  })

  it('a non-session pane, or none → false, and no ensure', () => {
    seedReady()
    expect(renderHook(() => useHandoffCandidate({ kind: 'dashboard' })).result.current).toBe(false)
    expect(renderHook(() => useHandoffCandidate({ kind: 'execution', executionId: 'e1', host: H })).result.current).toBe(false)
    expect(renderHook(() => useHandoffCandidate(null)).result.current).toBe(false)
    expect(ensure).not.toHaveBeenCalled()
  })

  it('ensures the nex host for a tmux pane', () => {
    renderHook(() => useHandoffCandidate(tmux()))
    expect(ensure).toHaveBeenCalledWith(H)
  })

  it('a pane on a host hidden in this workbench → false and no ensure (the pane gate, H2d-4)', () => {
    useShownHostsStore.setState({ ids: [] })
    seedReady()
    liveAgent('cc')
    expect(renderHook(() => useHandoffCandidate(tmux())).result.current).toBe(false)
    expect(ensure).not.toHaveBeenCalled()
  })

  it('is live: it follows the agent store, the host readiness and the shown list', () => {
    const { result } = renderHook(() => useHandoffCandidate(tmux()))
    expect(result.current).toBe(false)
    act(() => { seedReady() })
    expect(result.current).toBe(false)
    act(() => { liveAgent('cc') })
    expect(result.current).toBe(true)
    act(() => { useShownHostsStore.setState({ ids: [] }) })
    expect(result.current).toBe(false)
    act(() => { useShownHostsStore.setState({ ids: [H] }) })
    expect(result.current).toBe(true)
  })
})

// Shell cleanup P6 (spec §9.3): the same gate, saying why it is closed — the status bar's worker / chat buttons are
// disabled with a title naming the reason.
describe('useHandoffGate', () => {
  const gate = (content: PaneContent | null) => renderHook(() => useHandoffGate(content)).result.current

  it('a candidate → ok, no reason', () => {
    seedReady()
    liveAgent('cc')
    expect(gate(tmux())).toEqual({ ok: true, reason: null })
  })

  it('a non-session pane, or none → not_session', () => {
    seedReady()
    expect(gate({ kind: 'execution', executionId: 'e1', host: H })).toEqual({ ok: false, reason: 'not_session' })
    expect(gate(null)).toEqual({ ok: false, reason: 'not_session' })
  })

  it('a pane on a host hidden in this workbench → host_hidden, whatever else holds', () => {
    useShownHostsStore.setState({ ids: [] })
    seedReady()
    liveAgent('cc')
    expect(gate(tmux())).toEqual({ ok: false, reason: 'host_hidden' })
    expect(gate(tmux({ terminated: 'session-closed' }))).toEqual({ ok: false, reason: 'host_hidden' })
  })

  it('a terminated session → terminated', () => {
    seedReady()
    liveAgent('cc')
    expect(gate(tmux({ terminated: 'session-closed' }))).toEqual({ ok: false, reason: 'terminated' })
  })

  it('no Claude Code → not_agent; Claude Code on a host that is not ready → nex_not_ready', () => {
    seedReady(false)
    expect(gate(tmux())).toEqual({ ok: false, reason: 'not_agent' })
    liveAgent('cc')
    expect(gate(tmux())).toEqual({ ok: false, reason: 'nex_not_ready' })
  })

  it('is live, and useHandoffCandidate is its ok', () => {
    const { result } = renderHook(() => ({ gate: useHandoffGate(tmux()), candidate: useHandoffCandidate(tmux()) }))
    expect(result.current.gate.reason).toBe('not_agent')
    act(() => { liveAgent('cc') })
    expect(result.current.gate.reason).toBe('nex_not_ready')
    expect(result.current.candidate).toBe(false)
    act(() => { seedReady() })
    expect(result.current.gate).toEqual({ ok: true, reason: null })
    expect(result.current.candidate).toBe(true)
  })
})

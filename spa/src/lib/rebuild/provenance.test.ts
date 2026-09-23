import { describe, it, expect } from 'vitest'
import { parseExit, parseProvenance } from './provenance'

const envelope = {
  owner_session_start: true, agent_type: 'codex', session_id: 'S1',
  cwd: '/w/p', tmux_pane_id: '%2', tmux_instance: '222:2000', frame_id: 'F1',
}

describe('parseProvenance', () => {
  it('parses a well-formed envelope', () => {
    expect(parseProvenance({ pdx_provenance: envelope })).toEqual({
      agentType: 'codex', sessionId: 'S1', cwd: '/w/p',
      tmuxPaneId: '%2', tmuxInstance: '222:2000', frameId: 'F1',
    })
  })

  it('returns null when the flag is absent or false', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: false } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: 'true' } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: 1 } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { agent_type: 'cc', tmux_instance: '1:1' } })).toBeNull()
    expect(parseProvenance({ agent_type: 'cc', session_id: 'S1' })).toBeNull()
    expect(parseProvenance(undefined)).toBeNull()
  })

  it('returns null when the generation is unknown', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, tmux_instance: '' } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, tmux_instance: undefined } })).toBeNull()
  })

  it('returns null when the agent type is unknown', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, agent_type: '' } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, agent_type: 7 } })).toBeNull()
  })

  it('ignores a non-object envelope', () => {
    expect(parseProvenance({ pdx_provenance: 'yes' })).toBeNull()
    expect(parseProvenance({ pdx_provenance: null })).toBeNull()
    expect(parseProvenance({ pdx_provenance: [envelope] })).toBeNull()
  })

  it('defaults the optional fields rather than trusting their types', () => {
    expect(parseProvenance({
      pdx_provenance: { owner_session_start: true, agent_type: 'cc', tmux_instance: '1:1' },
    })).toEqual({ agentType: 'cc', sessionId: '', cwd: '', tmuxPaneId: '', tmuxInstance: '1:1', frameId: '' })
    expect(parseProvenance({
      pdx_provenance: { ...envelope, session_id: 42, cwd: {}, tmux_pane_id: false, frame_id: 9 },
    })).toEqual({ agentType: 'codex', sessionId: '', cwd: '', tmuxPaneId: '', tmuxInstance: '222:2000', frameId: '' })
  })
})

// The exit envelope (agent-last-state spec §1). Unlike provenance it is only
// useful with a frame id — the match key — so one without is dropped whole.
const exitEnvelope = {
  agent_type: 'cc', session_id: 'S1', tmux_pane_id: '%2', tmux_instance: '222:2000',
  frame_id: 'F1', reason: 'session-end', at: 1_788_740_123_456,
}

describe('parseExit', () => {
  it('parses a well-formed envelope', () => {
    expect(parseExit({ pdx_exit: exitEnvelope })).toEqual({
      agentType: 'cc', sessionId: 'S1', tmuxPaneId: '%2', tmuxInstance: '222:2000',
      frameId: 'F1', reason: 'session-end', at: 1_788_740_123_456,
    })
    expect(parseExit({ pdx_exit: { ...exitEnvelope, reason: 'process-dead' } })?.reason).toBe('process-dead')
  })

  it('returns null without a frame id or a generation', () => {
    expect(parseExit({ pdx_exit: { ...exitEnvelope, frame_id: '' } })).toBeNull()
    expect(parseExit({ pdx_exit: { ...exitEnvelope, frame_id: 3 } })).toBeNull()
    expect(parseExit({ pdx_exit: { ...exitEnvelope, tmux_instance: '' } })).toBeNull()
  })

  it('returns null for an unknown reason or a time that is not a positive number', () => {
    expect(parseExit({ pdx_exit: { ...exitEnvelope, reason: 'crashed' } })).toBeNull()
    expect(parseExit({ pdx_exit: { ...exitEnvelope, at: '1788740123456' } })).toBeNull()
    expect(parseExit({ pdx_exit: { ...exitEnvelope, at: 0 } })).toBeNull()
    expect(parseExit({ pdx_exit: { ...exitEnvelope, at: Number.NaN } })).toBeNull()
  })

  it('ignores a missing or non-object envelope', () => {
    expect(parseExit(undefined)).toBeNull()
    expect(parseExit({})).toBeNull()
    expect(parseExit({ pdx_exit: 'yes' })).toBeNull()
    expect(parseExit({ pdx_exit: null })).toBeNull()
    expect(parseExit({ pdx_exit: [exitEnvelope] })).toBeNull()
  })
})

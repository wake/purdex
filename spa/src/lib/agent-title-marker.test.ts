import { describe, it, expect } from 'vitest'
import { AGENT_TITLE_MARKERS, stripAgentTitleMarker } from './agent-title-marker'

describe('stripAgentTitleMarker', () => {
  it('removes the leading "✳ " Claude Code writes (measured 2026-09-18: U+2733 + space)', () => {
    expect(stripAgentTitleMarker('✳ Host color lab', 'cc')).toBe('Host color lab')
  })
  it('tolerates the emoji presentation selector and extra spaces', () => {
    expect(stripAgentTitleMarker('✳️  plan review', 'cc')).toBe('plan review')
  })
  it('removes only a leading marker', () => {
    expect(stripAgentTitleMarker('fix ✳ later', 'cc')).toBe('fix ✳ later')
  })
  it('returns an empty string when the title is only the marker', () => {
    expect(stripAgentTitleMarker('✳ ', 'cc')).toBe('')
  })
  it('leaves other agents and unknown types alone', () => {
    expect(stripAgentTitleMarker('✳ x', 'codex')).toBe('✳ x')
    expect(stripAgentTitleMarker('✳ x', undefined)).toBe('✳ x')
  })
  it('only cc has a marker today', () => {
    expect(Object.keys(AGENT_TITLE_MARKERS)).toEqual(['cc'])
  })
})

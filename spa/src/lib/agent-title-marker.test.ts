import { describe, it, expect } from 'vitest'
import { AGENT_TITLE_MARKERS, stripAgentTitleMarker, stripAnyKnownAgentTitleMarker } from './agent-title-marker'

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

describe('stripAnyKnownAgentTitleMarker (review finding A2: agentType may be unknown)', () => {
  it('strips a known marker even without an agentType to key the lookup by', () => {
    expect(stripAnyKnownAgentTitleMarker('✳ fix-login')).toBe('fix-login')
  })
  it('tolerates the emoji presentation selector and extra spaces, like the typed path', () => {
    expect(stripAnyKnownAgentTitleMarker('✳️  plan review')).toBe('plan review')
  })
  it('leaves an ordinary title with no marker unchanged', () => {
    expect(stripAnyKnownAgentTitleMarker('fix-login')).toBe('fix-login')
  })
  it('leaves a title untouched when the marker only appears mid-string, not leading', () => {
    expect(stripAnyKnownAgentTitleMarker('fix ✳ later')).toBe('fix ✳ later')
  })
  it('an empty title stays empty', () => {
    expect(stripAnyKnownAgentTitleMarker('')).toBe('')
  })
})

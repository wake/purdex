import { describe, it, expect } from 'vitest'
import { contentMatches } from './pane-utils'
import type { PaneContent } from '../types/tab'

describe('contentMatches', () => {
  it('returns false when kinds differ', () => {
    const a: PaneContent = { kind: 'dashboard' }
    const b: PaneContent = { kind: 'history' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for session kind (sessions are never singletons)', () => {
    const a: PaneContent = { kind: 'session', sessionCode: 'dev001', mode: 'terminal' }
    const b: PaneContent = { kind: 'session', sessionCode: 'dev001', mode: 'terminal' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for session kind even with different codes', () => {
    const a: PaneContent = { kind: 'session', sessionCode: 'dev001', mode: 'terminal' }
    const b: PaneContent = { kind: 'session', sessionCode: 'dev002', mode: 'stream' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns true for matching settings with global scope', () => {
    const a: PaneContent = { kind: 'settings', scope: 'global' }
    const b: PaneContent = { kind: 'settings', scope: 'global' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns true for matching settings with same workspace scope', () => {
    const a: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws-1' } }
    const b: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws-1' } }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for settings with different scopes', () => {
    const a: PaneContent = { kind: 'settings', scope: 'global' }
    const b: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws-1' } }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for settings with different workspace ids', () => {
    const a: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws-1' } }
    const b: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws-2' } }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns true for dashboard kind', () => {
    const a: PaneContent = { kind: 'dashboard' }
    const b: PaneContent = { kind: 'dashboard' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns true for history kind', () => {
    const a: PaneContent = { kind: 'history' }
    const b: PaneContent = { kind: 'history' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns true for new-tab kind', () => {
    const a: PaneContent = { kind: 'new-tab' }
    const b: PaneContent = { kind: 'new-tab' }
    expect(contentMatches(a, b)).toBe(true)
  })
})

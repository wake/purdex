import { describe, it, expect } from 'vitest'
import { deriveTabState } from './tab-state'
import type { PaneContent } from '../types/tab'
import type { HostRuntime } from '../stores/useHostStore'

describe('deriveTabState', () => {
  it('returns "active" for non-tmux-session kinds', () => {
    expect(deriveTabState({ kind: 'dashboard' })).toBe('active')
    expect(deriveTabState({ kind: 'browser', url: 'http://x' })).toBe('active')
  })

  it('returns "terminated" when terminated field is set', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
      terminated: 'session-closed',
    }
    const runtime: HostRuntime = { status: 'reconnecting' }
    expect(deriveTabState(content, runtime)).toBe('terminated')
  })

  it('returns "reconnecting" when runtime is reconnecting', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
    }
    expect(deriveTabState(content, { status: 'reconnecting' })).toBe('reconnecting')
  })

  it('returns "active" for connected tmux-session', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
    }
    expect(deriveTabState(content, { status: 'connected' })).toBe('active')
  })

  it('returns "active" when runtime is undefined', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
    }
    expect(deriveTabState(content)).toBe('active')
  })
})

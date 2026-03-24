import { describe, it, expect } from 'vitest'
import { getPaneLabel, getPaneIcon } from './pane-labels'
import type { PaneContent } from '../types/tab'

describe('getPaneLabel', () => {
  const mockSessionStore = {
    getByCode: (code: string) =>
      code === 'abc123' ? { name: 'dev-server' } : undefined,
  }
  const mockWorkspaceStore = {
    getById: (id: string) =>
      id === 'ws0001' ? { name: 'My Project' } : undefined,
  }

  it('returns New Tab for new-tab', () => {
    expect(getPaneLabel({ kind: 'new-tab' }, mockSessionStore, mockWorkspaceStore)).toBe('New Tab')
  })

  it('returns session name for session content', () => {
    const c: PaneContent = { kind: 'session', sessionCode: 'abc123', mode: 'terminal' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('dev-server')
  })

  it('falls back to sessionCode if session not found', () => {
    const c: PaneContent = { kind: 'session', sessionCode: 'zzz999', mode: 'terminal' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('zzz999')
  })

  it('returns Dashboard for dashboard', () => {
    expect(getPaneLabel({ kind: 'dashboard' }, mockSessionStore, mockWorkspaceStore)).toBe('Dashboard')
  })

  it('returns History for history', () => {
    expect(getPaneLabel({ kind: 'history' }, mockSessionStore, mockWorkspaceStore)).toBe('History')
  })

  it('returns Settings for global settings', () => {
    const c: PaneContent = { kind: 'settings', scope: 'global' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('Settings')
  })

  it('returns workspace name for workspace settings', () => {
    const c: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws0001' } }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('Settings — My Project')
  })

  it('falls back to workspace id if not found', () => {
    const c: PaneContent = { kind: 'settings', scope: { workspaceId: 'zzzzzz' } }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('Settings — zzzzzz')
  })
})

describe('getPaneIcon', () => {
  it('returns Plus for new-tab', () => {
    expect(getPaneIcon({ kind: 'new-tab' })).toBe('Plus')
  })

  it('returns TerminalWindow for terminal session', () => {
    expect(getPaneIcon({ kind: 'session', sessionCode: 'x', mode: 'terminal' })).toBe('TerminalWindow')
  })

  it('returns ChatCircleDots for stream session', () => {
    expect(getPaneIcon({ kind: 'session', sessionCode: 'x', mode: 'stream' })).toBe('ChatCircleDots')
  })

  it('returns House for dashboard', () => {
    expect(getPaneIcon({ kind: 'dashboard' })).toBe('House')
  })

  it('returns ClockCounterClockwise for history', () => {
    expect(getPaneIcon({ kind: 'history' })).toBe('ClockCounterClockwise')
  })

  it('returns GearSix for settings', () => {
    expect(getPaneIcon({ kind: 'settings', scope: 'global' })).toBe('GearSix')
  })
})

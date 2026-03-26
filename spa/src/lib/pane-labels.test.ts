import { describe, it, expect } from 'vitest'
import { getPaneLabel, getPaneIcon } from './pane-labels'
import type { TFunction } from './pane-labels'
import type { PaneContent } from '../types/tab'

const mockT: TFunction = (key, params) => {
  if (params) {
    return key.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params[k] ?? ''))
  }
  return key
}

describe('getPaneLabel', () => {
  const mockSessionStore = {
    getByCode: (code: string) =>
      code === 'abc123' ? { name: 'dev-server' } : undefined,
  }
  const mockWorkspaceStore = {
    getById: (id: string) =>
      id === 'ws0001' ? { name: 'My Project' } : undefined,
  }

  it('returns i18n key for new-tab', () => {
    expect(getPaneLabel({ kind: 'new-tab' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.new_tab')
  })

  it('returns session name for session content', () => {
    const c: PaneContent = { kind: 'session', sessionCode: 'abc123', mode: 'terminal' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('dev-server')
  })

  it('falls back to sessionCode if session not found', () => {
    const c: PaneContent = { kind: 'session', sessionCode: 'zzz999', mode: 'terminal' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('zzz999')
  })

  it('returns i18n key for dashboard', () => {
    expect(getPaneLabel({ kind: 'dashboard' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.dashboard')
  })

  it('returns i18n key for history', () => {
    expect(getPaneLabel({ kind: 'history' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.history')
  })

  it('returns i18n key for global settings', () => {
    const c: PaneContent = { kind: 'settings', scope: 'global' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.settings')
  })

  it('returns interpolated workspace name for workspace settings', () => {
    const c: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws0001' } }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.settings_ws')
  })

  it('falls back to workspace id if not found', () => {
    const c: PaneContent = { kind: 'settings', scope: { workspaceId: 'zzzzzz' } }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.settings_ws')
  })

  it('returns hostname for browser pane', () => {
    const content: PaneContent = { kind: 'browser', url: 'https://example.com/path' }
    expect(getPaneLabel(content, mockSessionStore, mockWorkspaceStore, mockT)).toBe('example.com')
  })

  it('returns raw url for browser pane with invalid url', () => {
    const content: PaneContent = { kind: 'browser', url: 'not-a-url' }
    expect(getPaneLabel(content, mockSessionStore, mockWorkspaceStore, mockT)).toBe('not-a-url')
  })

  it('returns i18n key for memory-monitor', () => {
    expect(getPaneLabel({ kind: 'memory-monitor' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('monitor.title')
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

  it('returns Globe icon for browser pane', () => {
    const content: PaneContent = { kind: 'browser', url: 'https://example.com' }
    expect(getPaneIcon(content)).toBe('Globe')
  })

  it('returns ChartBar for memory-monitor', () => {
    expect(getPaneIcon({ kind: 'memory-monitor' })).toBe('ChartBar')
  })
})

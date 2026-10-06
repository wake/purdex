import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { getPaneLabel, getPaneIcon } from './pane-labels'
import type { TFunction } from './pane-labels'
import type { PaneContent, Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'
import { useTabStore } from '../stores/useTabStore'
import { findPane } from './pane-tree'
import { repointPane } from './rebuild/engine'
import type { ConversationRow } from './nex/conversations-api'
import { conversationRebuildContent } from './nex/open-conversation-rebuild'

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
    const c: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('dev-server')
  })

  it('falls back to cachedName if session not found', () => {
    const c: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'zzz999', mode: 'terminal', cachedName: 'my-session', tmuxInstance: '' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore, mockT)).toBe('my-session')
  })

  it('falls back to sessionCode if session not found and cachedName empty', () => {
    const c: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'zzz999', mode: 'terminal', cachedName: '', tmuxInstance: '' }
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

  it('returns i18n key for hosts', () => {
    expect(getPaneLabel({ kind: 'hosts' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('page.pane.hosts')
  })

  it('returns i18n key for memory-monitor', () => {
    expect(getPaneLabel({ kind: 'memory-monitor' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('performance_monitor.title')
  })

  it('A2-2: returns i18n key for editor-buffers', () => {
    expect(getPaneLabel({ kind: 'editor-buffers' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('editor.buffers.tab_title')
  })
})

describe('getPaneIcon', () => {
  it('returns Plus for new-tab', () => {
    expect(getPaneIcon({ kind: 'new-tab' })).toBe('Plus')
  })

  it('returns TerminalWindow for terminal session', () => {
    expect(getPaneIcon({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'x', mode: 'terminal', cachedName: '', tmuxInstance: '' })).toBe('TerminalWindow')
  })

  it('returns House for dashboard', () => {
    expect(getPaneIcon({ kind: 'dashboard' })).toBe('House')
  })

  it('returns ClockCounterClockwise for history', () => {
    expect(getPaneIcon({ kind: 'history' })).toBe('ClockCounterClockwise')
  })

  it('returns Sliders for settings', () => {
    expect(getPaneIcon({ kind: 'settings', scope: 'global' })).toBe('Sliders')
  })

  it('returns HardDrives for hosts', () => {
    expect(getPaneIcon({ kind: 'hosts' })).toBe('HardDrives')
  })

  it('returns Globe icon for browser pane', () => {
    const content: PaneContent = { kind: 'browser', url: 'https://example.com' }
    expect(getPaneIcon(content)).toBe('Globe')
  })

  it('returns ChartBar for memory-monitor', () => {
    expect(getPaneIcon({ kind: 'memory-monitor' })).toBe('ChartBar')
  })

  it('returns TextAlignLeft for editor', () => {
    expect(getPaneIcon({ kind: 'editor', filePath: '/x.ts', source: { type: 'local' } })).toBe('TextAlignLeft')
  })

  it('returns GitDiff for editor diff mode', () => {
    expect(getPaneIcon({ kind: 'editor', filePath: '/x.ts', source: { type: 'local' }, diff: { against: 'saved' } })).toBe('GitDiff')
  })

  it('A2-3: returns Stack for editor-buffers', () => {
    expect(getPaneIcon({ kind: 'editor-buffers' })).toBe('Stack')
  })

  it('T3: returns file icon for inapp editor pane by extension', () => {
    expect(getPaneIcon({ kind: 'editor', filePath: '/buffer/x.md', source: { type: 'inapp' } })).toBe('FileMd')
  })

  it('T3: returns file icon for inapp image-preview pane', () => {
    expect(getPaneIcon({ kind: 'image-preview', filePath: '/buffer/p.png', source: { type: 'inapp' } })).toBe('FilePng')
  })

  it('T3: returns file icon for inapp pdf-preview pane', () => {
    expect(getPaneIcon({ kind: 'pdf-preview', filePath: '/buffer/d.pdf', source: { type: 'inapp' } })).toBe('FilePdf')
  })

  it('T3: leaves daemon editor pane unchanged', () => {
    expect(getPaneIcon({ kind: 'editor', filePath: '/buffer/x.md', source: { type: 'daemon', hostId: 'h1' } })).toBe('TextAlignLeft')
  })

  it('T3: leaves daemon image-preview pane unchanged', () => {
    expect(getPaneIcon({ kind: 'image-preview', filePath: '/buffer/p.png', source: { type: 'daemon', hostId: 'h1' } })).toBe('Image')
  })

  it('T3: leaves inapp editor diff mode as GitDiff', () => {
    expect(getPaneIcon({ kind: 'editor', filePath: '/buffer/x.md', source: { type: 'inapp' }, diff: { against: 'saved' } })).toBe('GitDiff')
  })
})

// The closed-terminal label and the conversation rebuild tab's label (conversation entity spec §13.4): one suffix,
// in the UI language; a conversation-ended pane is named by its conversation title.
describe('getPaneLabel — closed terminal panes', () => {
  const sessions = { getByCode: (code: string) => (code === 'new1' ? { name: 'proj-2' } : undefined) }
  const workspaces = { getById: () => undefined }
  const S = 'aaaaaaaa-1111-2222-3333-444444444444'
  const row = (title: string): ConversationRow => ({
    session_id: S, title, title_source: 'ai', cwd: '/w/proj', cwd_exists: true, last_activity_at: 5, last_in: 'terminal',
  })
  const closed: PaneContent = { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c1', mode: 'terminal', cachedName: 'my-session', tmuxInstance: 'i', terminated: 'session-closed' }
  const label = (c: PaneContent) => getPaneLabel(c, sessions, workspaces, useI18nStore.getState().t)

  beforeEach(() => useI18nStore.getState().setLocale('en'))
  afterEach(() => useI18nStore.getState().setLocale('en'))

  it('an ordinary closed pane: its session name with the suffix, in each language', () => {
    expect(label(closed)).toBe('my-session（Terminated）')
    useI18nStore.getState().setLocale('zh-TW')
    expect(label(closed)).toBe('my-session（已結束）')
  })

  it('a conversation-ended pane: the conversation title with the same suffix', () => {
    const c = conversationRebuildContent('h1', row('Fix the login bug'), 'proj-2', 'i', 1)
    expect(label(c)).toBe('Fix the login bug（Terminated）')
    useI18nStore.getState().setLocale('zh-TW')
    expect(label(c)).toBe('Fix the login bug（已結束）')
  })

  it('a long title: one line, not length-capped — the tab truncates it by CSS, as it does every label', () => {
    const long = 'x'.repeat(300)
    expect(label(conversationRebuildContent('h1', row(`  ${long}  \nsecond line`), 'proj-2', 'i', 1))).toBe(`${long}（Terminated）`)
  })

  it('a title with nothing on its first line falls back to the session name', () => {
    expect(label(conversationRebuildContent('h1', row('   '), 'proj-2', 'i', 1))).toBe('proj-2（Terminated）')
  })

  it('after a rebuild the pane is an ordinary terminal pane: the conversation is gone and the label is the session name', () => {
    const content = conversationRebuildContent('h1', row('Fix the login bug'), 'proj-2', 'i', 1)
    const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: 'p1', content } } }
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })

    repointPane('t1', 'p1', { code: 'new1', name: 'proj-2', cwd: '/w/proj', mode: 'terminal', tmux_instance: 'i2' })

    const after = findPane(useTabStore.getState().tabs.t1.layout, 'p1')!.content
    expect(after.kind === 'tmux-session' && after.conversation).toBeUndefined()
    expect(label(after)).toBe('proj-2')
  })
})

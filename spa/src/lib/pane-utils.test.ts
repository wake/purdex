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
    const a: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    const b: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for session kind even with different codes', () => {
    const a: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    const b: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev002', mode: 'stream', cachedName: '', tmuxInstance: '' }
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

  it('returns false for browser kind (browser panes are never singletons)', () => {
    const a: PaneContent = { kind: 'browser', url: 'https://a.com' }
    const b: PaneContent = { kind: 'browser', url: 'https://a.com' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for browser kind with different urls', () => {
    const a: PaneContent = { kind: 'browser', url: 'https://a.com' }
    const b: PaneContent = { kind: 'browser', url: 'https://b.com' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns true for hosts kind (singleton)', () => {
    const a: PaneContent = { kind: 'hosts' }
    const b: PaneContent = { kind: 'hosts' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns true for memory-monitor kind (singleton)', () => {
    const a: PaneContent = { kind: 'memory-monitor' }
    const b: PaneContent = { kind: 'memory-monitor' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for memory-monitor vs dashboard (different kinds)', () => {
    const a: PaneContent = { kind: 'memory-monitor' }
    const b: PaneContent = { kind: 'dashboard' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns true for editor with same inapp source and filePath', () => {
    const a: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/buffer/test.txt' }
    const b: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/buffer/test.txt' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for editor with different filePath', () => {
    const a: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.txt' }
    const b: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/b.txt' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for editor with different source types', () => {
    const a: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/test.txt' }
    const b: PaneContent = { kind: 'editor', source: { type: 'local' }, filePath: '/test.txt' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns true for editor with same daemon source and filePath', () => {
    const a: PaneContent = { kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/test.txt' }
    const b: PaneContent = { kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/test.txt' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for editor with same daemon filePath but different hostId', () => {
    const a: PaneContent = { kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/test.txt' }
    const b: PaneContent = { kind: 'editor', source: { type: 'daemon', hostId: 'h2' }, filePath: '/test.txt' }
    expect(contentMatches(a, b)).toBe(false)
  })

  // image-preview
  it('returns true for image-preview with same inapp source and filePath', () => {
    const a: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/img/photo.png' }
    const b: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/img/photo.png' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for image-preview with different filePath', () => {
    const a: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/a.png' }
    const b: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/b.png' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for image-preview with different source types', () => {
    const a: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/test.png' }
    const b: PaneContent = { kind: 'image-preview', source: { type: 'local' }, filePath: '/test.png' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns true for image-preview with same daemon source and filePath and hostId', () => {
    const a: PaneContent = { kind: 'image-preview', source: { type: 'daemon', hostId: 'h1' }, filePath: '/img.jpg' }
    const b: PaneContent = { kind: 'image-preview', source: { type: 'daemon', hostId: 'h1' }, filePath: '/img.jpg' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for image-preview with same daemon filePath but different hostId', () => {
    const a: PaneContent = { kind: 'image-preview', source: { type: 'daemon', hostId: 'h1' }, filePath: '/img.jpg' }
    const b: PaneContent = { kind: 'image-preview', source: { type: 'daemon', hostId: 'h2' }, filePath: '/img.jpg' }
    expect(contentMatches(a, b)).toBe(false)
  })

  // pdf-preview
  it('returns true for pdf-preview with same inapp source and filePath', () => {
    const a: PaneContent = { kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/docs/report.pdf' }
    const b: PaneContent = { kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/docs/report.pdf' }
    expect(contentMatches(a, b)).toBe(true)
  })

  it('returns false for pdf-preview with different filePath', () => {
    const a: PaneContent = { kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/a.pdf' }
    const b: PaneContent = { kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/b.pdf' }
    expect(contentMatches(a, b)).toBe(false)
  })

  it('returns false for pdf-preview with same daemon filePath but different hostId', () => {
    const a: PaneContent = { kind: 'pdf-preview', source: { type: 'daemon', hostId: 'h1' }, filePath: '/doc.pdf' }
    const b: PaneContent = { kind: 'pdf-preview', source: { type: 'daemon', hostId: 'h2' }, filePath: '/doc.pdf' }
    expect(contentMatches(a, b)).toBe(false)
  })
})

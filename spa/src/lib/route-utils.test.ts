import { describe, it, expect } from 'vitest'
import { parseRoute, tabToUrl } from './route-utils'

describe('parseRoute', () => {
  it('parses / as dashboard', () => {
    expect(parseRoute('/')).toEqual({ kind: 'dashboard' })
  })

  it('parses /history', () => {
    expect(parseRoute('/history')).toEqual({ kind: 'history' })
  })

  it('parses /settings', () => {
    expect(parseRoute('/settings')).toEqual({ kind: 'settings', scope: 'global' })
  })

  it('parses /t/:tabId/:mode', () => {
    expect(parseRoute('/t/abc123/terminal')).toEqual({
      kind: 'session-tab', tabId: 'abc123', mode: 'terminal',
    })
  })

  it('parses /t/:tabId/stream', () => {
    expect(parseRoute('/t/abc123/stream')).toEqual({
      kind: 'session-tab', tabId: 'abc123', mode: 'stream',
    })
  })

  it('invalid mode falls back to terminal', () => {
    expect(parseRoute('/t/abc123/invalid')).toEqual({
      kind: 'session-tab', tabId: 'abc123', mode: 'terminal',
    })
  })

  it('parses /w/:workspaceId', () => {
    expect(parseRoute('/w/ws0001')).toEqual({
      kind: 'workspace', workspaceId: 'ws0001',
    })
  })

  it('parses /w/:workspaceId/settings', () => {
    expect(parseRoute('/w/ws0001/settings')).toEqual({
      kind: 'workspace-settings', workspaceId: 'ws0001',
    })
  })

  it('parses /w/:workspaceId/t/:tabId/:mode', () => {
    expect(parseRoute('/w/ws0001/t/abc123/stream')).toEqual({
      kind: 'workspace-session-tab', workspaceId: 'ws0001', tabId: 'abc123', mode: 'stream',
    })
  })

  it('returns null for unknown routes', () => {
    expect(parseRoute('/unknown/path')).toBeNull()
  })
})

describe('tabToUrl', () => {
  it('generates session tab URL', () => {
    expect(tabToUrl('abc123', { kind: 'session', sessionCode: 'x', mode: 'terminal' }))
      .toBe('/t/abc123/terminal')
  })

  it('generates dashboard URL', () => {
    expect(tabToUrl('abc123', { kind: 'dashboard' })).toBe('/')
  })

  it('generates history URL', () => {
    expect(tabToUrl('abc123', { kind: 'history' })).toBe('/history')
  })

  it('generates global settings URL', () => {
    expect(tabToUrl('abc123', { kind: 'settings', scope: 'global' })).toBe('/settings')
  })

  it('generates workspace settings URL', () => {
    expect(tabToUrl('abc123', { kind: 'settings', scope: { workspaceId: 'ws0001' } }))
      .toBe('/w/ws0001/settings')
  })

  it('generates session tab URL within workspace', () => {
    expect(tabToUrl('abc123', { kind: 'session', sessionCode: 'x', mode: 'terminal' }, 'ws0001'))
      .toBe('/w/ws0001/t/abc123/terminal')
  })

  it('generates new-tab fallback URL', () => {
    expect(tabToUrl('abc123', { kind: 'new-tab' })).toBe('/t/abc123/terminal')
  })
})

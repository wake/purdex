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

  describe('ID format validation', () => {
    it('rejects tabId with uppercase letters', () => {
      expect(parseRoute('/t/ABC123/terminal')).toBeNull()
    })

    it('rejects tabId with wrong length', () => {
      expect(parseRoute('/t/abc12/terminal')).toBeNull()
      expect(parseRoute('/t/abc1234/terminal')).toBeNull()
    })

    it('rejects tabId with special characters', () => {
      expect(parseRoute('/t/abc-23/terminal')).toBeNull()
    })

    it('rejects workspaceId with invalid format', () => {
      expect(parseRoute('/w/INVALID')).toBeNull()
      expect(parseRoute('/w/ws000/settings')).toBeNull()
    })

    it('rejects workspace-session-tab with invalid IDs', () => {
      expect(parseRoute('/w/BADID!/t/abc123/terminal')).toBeNull()
      expect(parseRoute('/w/abc123/t/BADID!/terminal')).toBeNull()
    })

    it('accepts valid base36 6-char IDs', () => {
      expect(parseRoute('/t/a1b2c3/terminal')).toEqual({
        kind: 'session-tab', tabId: 'a1b2c3', mode: 'terminal',
      })
      expect(parseRoute('/w/x9y8z7')).toEqual({
        kind: 'workspace', workspaceId: 'x9y8z7',
      })
    })
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

  it('generates new-tab URL as dashboard', () => {
    expect(tabToUrl('abc123', { kind: 'new-tab' })).toBe('/')
  })
})

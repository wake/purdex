import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { parseRoute, tabToUrl } from './route-utils'
import { clearContributions } from './settings-contribution-registry'
import { clearModuleRegistry } from './module-registry'
import { registerBuiltinModules } from './register-modules'

beforeEach(() => {
  clearContributions()
  clearModuleRegistry()
  registerBuiltinModules()
})

afterEach(() => {
  clearContributions()
  clearModuleRegistry()
})

describe('parseRoute', () => {
  it('/ returns null (no-op, preserves persisted state)', () => {
    expect(parseRoute('/')).toBeNull()
  })

  it('parses /history', () => {
    expect(parseRoute('/history')).toEqual({ kind: 'history' })
  })

  it('parses /hosts', () => {
    expect(parseRoute('/hosts')).toEqual({ kind: 'hosts' })
  })

  it('parses /hosts/ as bare hosts', () => {
    expect(parseRoute('/hosts/')).toEqual({ kind: 'hosts' })
  })

  it('parses /hosts/:hostId/:subPage with a valid sub page', () => {
    expect(parseRoute('/hosts/test-host/sessions')).toEqual({
      kind: 'hosts',
      hostId: 'test-host',
      subPage: 'sessions',
    })
  })

  it('decodes encoded host ids in host deep links', () => {
    expect(parseRoute('/hosts/host%2Fwith%3Fhash%23value/logs')).toEqual({
      kind: 'hosts',
      hostId: 'host/with?hash#value',
      subPage: 'logs',
    })
  })

  it('parses /hosts/:hostId/:subPage with an invalid sub page', () => {
    expect(parseRoute('/hosts/test-host/not-a-page')).toEqual({
      kind: 'hosts-invalid',
      hostId: 'test-host',
      subPage: 'not-a-page',
    })
  })

  it('parses /hosts/:hostId/:subPage/:extra as invalid even when sub page is known', () => {
    expect(parseRoute('/hosts/test-host/sessions/extra')).toEqual({
      kind: 'hosts-invalid',
      hostId: 'test-host',
      subPage: 'sessions',
    })
  })

  it('parses /hosts/:hostId as invalid', () => {
    expect(parseRoute('/hosts/test-host')).toEqual({
      kind: 'hosts-invalid',
      hostId: 'test-host',
    })
  })

  it('parses /settings', () => {
    expect(parseRoute('/settings')).toEqual({ kind: 'settings', scope: 'global' })
  })

  it('parses /settings/appearance with section field', () => {
    expect(parseRoute('/settings/appearance')).toEqual({
      kind: 'settings', scope: 'global', section: 'appearance',
    })
  })

  it('parses /settings/terminal with section field', () => {
    expect(parseRoute('/settings/terminal')).toEqual({
      kind: 'settings', scope: 'global', section: 'terminal',
    })
  })

  it('parses /settings/sync with section field', () => {
    expect(parseRoute('/settings/sync')).toEqual({
      kind: 'settings', scope: 'global', section: 'sync',
    })
  })

  it('rejects invalid section names, falls back to no section', () => {
    expect(parseRoute('/settings/bad..name')).toEqual({
      kind: 'settings', scope: 'global',
    })
    expect(parseRoute('/settings/BAD')).toEqual({
      kind: 'settings', scope: 'global',
    })
    expect(parseRoute('/settings/has spaces')).toEqual({
      kind: 'settings', scope: 'global',
    })
  })

  it('accepts valid section ids (a-z, 0-9, hyphen, max 32)', () => {
    expect(parseRoute('/settings/dev-env-123')).toEqual({
      kind: 'settings', scope: 'global', section: 'dev-env-123',
    })
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
    expect(tabToUrl('abc123', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'x', mode: 'terminal', cachedName: '', tmuxInstance: '' }))
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
    expect(tabToUrl('abc123', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'x', mode: 'terminal', cachedName: '', tmuxInstance: '' }, 'ws0001'))
      .toBe('/w/ws0001/t/abc123/terminal')
  })

  it('generates hosts URL', () => {
    expect(tabToUrl('abc123', { kind: 'hosts' })).toBe('/hosts')
  })

  it('generates new-tab URL as dashboard', () => {
    expect(tabToUrl('abc123', { kind: 'new-tab' })).toBe('/')
  })
})

describe('parseRoute settings subsection', () => {
  it('recognises /settings/sync/history as section=sync, subsection=history', () => {
    const r = parseRoute('/settings/sync/history')
    expect(r).toEqual({ kind: 'settings', scope: 'global', section: 'sync', subsection: 'history' })
  })

  it('still parses /settings/sync as section only', () => {
    const r = parseRoute('/settings/sync')
    expect(r).toEqual({ kind: 'settings', scope: 'global', section: 'sync' })
  })

  it('rejects 3-level /settings/a/b/c', () => {
    const r = parseRoute('/settings/a/b/c')
    expect(r).toEqual({ kind: 'settings', scope: 'global' })
  })
})

import { describe, it, expect } from 'vitest'
import { parseHash } from './hash-routing'

describe('parseHash', () => {
  it('returns null tabId for empty hash', () => {
    window.location.hash = ''
    expect(parseHash().tabId).toBeNull()
    expect(parseHash().viewMode).toBeNull()
  })

  it('parses #/tab/{id}', () => {
    window.location.hash = '#/tab/abc-123'
    expect(parseHash()).toEqual({ tabId: 'abc-123', viewMode: null })
  })

  it('parses #/tab/{id}/{viewMode}', () => {
    window.location.hash = '#/tab/abc-123/stream'
    expect(parseHash()).toEqual({ tabId: 'abc-123', viewMode: 'stream' })
  })

  it('parses #/tab/{id}/terminal', () => {
    window.location.hash = '#/tab/abc-123/terminal'
    expect(parseHash()).toEqual({ tabId: 'abc-123', viewMode: 'terminal' })
  })

  it('returns null tabId for #/tab/ with empty id', () => {
    window.location.hash = '#/tab/'
    expect(parseHash().tabId).toBeNull()
  })

  it('returns null for unknown format', () => {
    window.location.hash = '#/something/else'
    expect(parseHash().tabId).toBeNull()
  })
})

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { DEFAULT_QUICK_REPLIES, effectiveQuickReplies, useQuickReplies, validateQuickReplyText } from './quick-replies'
import { emptyHostConfigEntry, useHostConfigStore, type HostConfigEntry } from '../stores/useHostConfigStore'
import type { QuickReply } from './host-config-api'

function ready(quickReplies: QuickReply[], revision: number, supported = true): HostConfigEntry {
  const e = emptyHostConfigEntry('ready')
  return { ...e, quickReplies, quickRepliesSupported: supported, revisions: { ...e.revisions, quickReplies: revision } }
}

beforeEach(() => useHostConfigStore.setState({ byHost: {} }))

describe('effectiveQuickReplies', () => {
  it('the defaults are the three from Q3, frozen', () => {
    expect(DEFAULT_QUICK_REPLIES.map((r) => [r.id, r.text])).toEqual([
      ['continue', 'continue'],
      ['run-tests', 'run the tests'],
      ['explain', 'explain that'],
    ])
    expect(Object.isFrozen(DEFAULT_QUICK_REPLIES)).toBe(true)
  })

  it('defaults when never written', () => {
    expect(effectiveQuickReplies(ready([], 0))).toBe(DEFAULT_QUICK_REPLIES)
  })

  // Q1 (a tap sends at once) + Q3 (an emptied list shows no dock): a list the
  // host is not known to hold must never be the defaults, or a stray
  // `continue` appears — and one tap sends it.
  it('error after ready keeps the stored list', () => {
    const items = [{ id: 'go', text: 'go on' }]
    expect(effectiveQuickReplies({ ...ready(items, 4), status: 'error', error: 'offline' })).toEqual(items)
    // …and a never-written collection still reads as the defaults.
    expect(effectiveQuickReplies({ ...ready([], 0), status: 'error', error: 'offline' })).toBe(DEFAULT_QUICK_REPLIES)
  })

  it('error after ready keeps an emptied list empty', () => {
    expect(effectiveQuickReplies({ ...ready([], 2), status: 'error', error: 'offline' })).toEqual([])
  })

  it('loading shows nothing', () => {
    expect(effectiveQuickReplies(undefined)).toEqual([])
    expect(effectiveQuickReplies(emptyHostConfigEntry('idle'))).toEqual([])
    expect(effectiveQuickReplies(emptyHostConfigEntry('loading'))).toEqual([])
    // A refresh of a host that loaded before keeps its list.
    expect(effectiveQuickReplies({ ...ready([{ id: 'a', text: 'a' }], 3), status: 'loading' })).toEqual([{ id: 'a', text: 'a' }])
  })

  it('never-loaded error shows nothing', () => {
    expect(effectiveQuickReplies({ ...emptyHostConfigEntry('error'), error: 'offline' })).toEqual([])
  })

  it('an old daemon still gets the defaults', () => {
    expect(effectiveQuickReplies(ready([], 0, false))).toBe(DEFAULT_QUICK_REPLIES)
    expect(effectiveQuickReplies(emptyHostConfigEntry('unsupported'))).toBe(DEFAULT_QUICK_REPLIES)
  })

  it('defaults when the daemon has no collection', () => {
    expect(effectiveQuickReplies(ready([], 0, false))).toBe(DEFAULT_QUICK_REPLIES)
    expect(effectiveQuickReplies(emptyHostConfigEntry('unsupported'))).toBe(DEFAULT_QUICK_REPLIES)
  })

  it('stored items win', () => {
    const items = [{ id: 'go', text: 'go on' }]
    expect(effectiveQuickReplies(ready(items, 1))).toEqual(items)
  })

  it('an emptied list stays empty', () => {
    expect(effectiveQuickReplies(ready([], 2))).toEqual([])
  })
})

describe('validateQuickReplyText', () => {
  it('follows the daemon: non-empty, at most 1000 bytes, no NUL', () => {
    expect(validateQuickReplyText('go')).toBeNull()
    expect(validateQuickReplyText('')).toBe('hosts.quick_replies.error_empty')
    expect(validateQuickReplyText('a\0b')).toBe('hosts.quick_replies.error_invalid')
    expect(validateQuickReplyText('字'.repeat(333))).toBeNull() // 999 bytes
    expect(validateQuickReplyText('字'.repeat(334))).toBe('hosts.quick_replies.error_invalid')
  })
})

describe('useQuickReplies', () => {
  it('asks the store to load the host and repaints on change', () => {
    const ensureLoaded = vi.fn(async () => {})
    useHostConfigStore.setState({ ensureLoaded })
    const { result } = renderHook(() => useQuickReplies('h1'))
    expect(ensureLoaded).toHaveBeenCalledWith('h1')
    expect(result.current).toEqual([])
    act(() => useHostConfigStore.setState({ byHost: { h1: ready([{ id: 'go', text: 'go on' }], 1) } }))
    expect(result.current).toEqual([{ id: 'go', text: 'go on' }])
  })
})

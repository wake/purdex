import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { DEFAULT_QUICK_REPLIES, effectiveQuickReplies, useQuickReplies } from './quick-replies'
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
    // Not loaded at all, or still loading, also answers from the defaults.
    expect(effectiveQuickReplies(undefined)).toBe(DEFAULT_QUICK_REPLIES)
    expect(effectiveQuickReplies({ ...ready([{ id: 'a', text: 'a' }], 3), status: 'loading' })).toBe(DEFAULT_QUICK_REPLIES)
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

describe('useQuickReplies', () => {
  it('asks the store to load the host and repaints on change', () => {
    const ensureLoaded = vi.fn(async () => {})
    useHostConfigStore.setState({ ensureLoaded })
    const { result } = renderHook(() => useQuickReplies('h1'))
    expect(ensureLoaded).toHaveBeenCalledWith('h1')
    expect(result.current).toBe(DEFAULT_QUICK_REPLIES)
    act(() => useHostConfigStore.setState({ byHost: { h1: ready([{ id: 'go', text: 'go on' }], 1) } }))
    expect(result.current).toEqual([{ id: 'go', text: 'go on' }])
  })
})

// spa/src/components/team/seat-workbook.test.tsx — what the team panel reads of a seat's workbook (WA-2b-1).
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { firstSentence, useSeatWorkbook } from './seat-workbook'
import { useWorkbookStore, type ConvState } from '../../stores/useWorkbookStore'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'

describe('firstSentence', () => {
  it('cuts at the first full stop of either width', () => {
    expect(firstSentence('做完登入頁。接著寫測試')).toBe('做完登入頁')
    expect(firstSentence('Fix the login. Then test it')).toBe('Fix the login')
    expect(firstSentence('Done! More')).toBe('Done')
    expect(firstSentence('是嗎？不是')).toBe('是嗎')
  })
  it('cuts at a line break', () => {
    expect(firstSentence('first line\nsecond line')).toBe('first line')
  })
  it('keeps a dot that is not a sentence end (version numbers, file names)', () => {
    expect(firstSentence('upgrade to 1.2.3 today')).toBe('upgrade to 1.2.3 today')
  })
  it('a status without a stop is whole; blank is empty', () => {
    expect(firstSentence('  just this  ')).toBe('just this')
    expect(firstSentence('   ')).toBe('')
  })
})

describe('useSeatWorkbook', () => {
  beforeEach(() => useWorkbookStore.getState().reset())

  it('has a workbook when the host lists v1 and the session maps to a conversation', () => {
    seedWorkbook('h1', 'S', { status: 'working' })
    const { result } = renderHook(() => useSeatWorkbook('h1', 'S'))
    expect(result.current.has).toBe(true)
    expect(result.current.conv?.status).toBe('working')
  })
  it('has none without workbook.v1', () => {
    seedWorkbook('h1', 'S', { status: 'working' }, { v1: false })
    expect(renderHook(() => useSeatWorkbook('h1', 'S')).result.current.has).toBe(false)
  })
  it('has none for a host this Mac lacks (empty host id), an unknown session, or a missing (404) conversation', () => {
    seedWorkbook('h1', 'S', { status: 'working' })
    expect(renderHook(() => useSeatWorkbook('', 'S')).result.current.has).toBe(false)
    expect(renderHook(() => useSeatWorkbook('h1', 'other')).result.current.has).toBe(false)
    seedWorkbook('h1', 'M', { missing: true } as Partial<ConvState>)
    expect(renderHook(() => useSeatWorkbook('h1', 'M')).result.current.has).toBe(false)
  })
})

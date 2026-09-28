import { describe, it, expect } from 'vitest'
import { forgetScrollMemo, readScrollMemo, writeScrollMemo } from './transcript-scroll-memory'

describe('transcript scroll memory', () => {
  it('keeps one memo per pane until forgotten', () => {
    expect(readScrollMemo('pa')).toBeUndefined()
    writeScrollMemo('pa', { scrollTop: 120, atBottom: false, view: 'room', firstTurn: 2 })
    writeScrollMemo('pb', { scrollTop: 0, atBottom: true, view: 'chat', firstTurn: null })
    expect(readScrollMemo('pa')).toEqual({ scrollTop: 120, atBottom: false, view: 'room', firstTurn: 2 })
    writeScrollMemo('pa', { scrollTop: 300, atBottom: false, view: 'chat', firstTurn: 3 })
    expect(readScrollMemo('pa')?.scrollTop).toBe(300)
    forgetScrollMemo('pa')
    expect(readScrollMemo('pa')).toBeUndefined()
    expect(readScrollMemo('pb')?.atBottom).toBe(true)
    forgetScrollMemo('pb')
  })
})

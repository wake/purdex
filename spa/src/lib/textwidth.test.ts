// spa/src/lib/textwidth.test.ts — the App's weight table against the daemon's: both read testdata/textwidth/cases.json.
import { describe, it, expect } from 'vitest'
import casesRaw from '../../../testdata/textwidth/cases.json?raw'
import { cellWidth } from './textwidth'

const cases: { name: string; s: string; width: number }[] = JSON.parse(casesRaw)

describe('textwidth', () => {
  it('reads the shared fixture', () => {
    expect(cases.length).toBeGreaterThan(60)
  })
  it.each(cases)('$name', ({ s, width }) => {
    expect(cellWidth(s)).toBe(width)
  })
  it('counts code points, never UTF-16 units', () => {
    expect('😀'.length).toBe(2)
    expect(cellWidth('😀')).toBe(2)
    expect(cellWidth('é'.normalize('NFD'))).toBe(1)
  })
})

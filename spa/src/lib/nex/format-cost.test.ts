// spa/src/lib/nex/format-cost.test.ts
import { describe, it, expect } from 'vitest'
import { formatUsd, formatTokens } from './format-cost'

describe('formatUsd', () => {
  it('defaults to four decimals with a $ prefix', () => {
    expect(formatUsd(0.0299658)).toBe('$0.0300')
    expect(formatUsd(0.2576558)).toBe('$0.2577')
  })

  it('zero → $0.0000', () => {
    expect(formatUsd(0)).toBe('$0.0000')
  })

  it('honours the dp argument', () => {
    expect(formatUsd(1.5, 2)).toBe('$1.50')
    expect(formatUsd(1.5, 0)).toBe('$2')
  })

  it('negative / non-finite → $—', () => {
    expect(formatUsd(-1)).toBe('$—')
    expect(formatUsd(-0.0001)).toBe('$—')
    expect(formatUsd(Number.NaN)).toBe('$—')
    expect(formatUsd(Number.POSITIVE_INFINITY)).toBe('$—')
    expect(formatUsd(Number.NEGATIVE_INFINITY)).toBe('$—')
  })
})

describe('formatTokens', () => {
  it('< 1000 → integer as is', () => {
    expect(formatTokens(0)).toBe('0')
    expect(formatTokens(364)).toBe('364')
    expect(formatTokens(999)).toBe('999')
  })

  it('thousands → one decimal k, trailing .0 dropped', () => {
    expect(formatTokens(1000)).toBe('1k')
    expect(formatTokens(4478)).toBe('4.5k')
    expect(formatTokens(11244)).toBe('11.2k')
    expect(formatTokens(80127)).toBe('80.1k')
    expect(formatTokens(999_949)).toBe('999.9k')
  })

  it('millions → one decimal M, trailing .0 dropped', () => {
    expect(formatTokens(1_000_000)).toBe('1M')
    expect(formatTokens(1_234_567)).toBe('1.2M')
    expect(formatTokens(12_345_678)).toBe('12.3M')
  })

  it('999950–999999: k rounds to 1000.0 and is promoted to 1M (never "1000k")', () => {
    expect(formatTokens(999_950)).toBe('1M')
    expect(formatTokens(999_999)).toBe('1M')
  })

  it('non-finite / negative → —', () => {
    expect(formatTokens(Number.NaN)).toBe('—')
    expect(formatTokens(Number.POSITIVE_INFINITY)).toBe('—')
    expect(formatTokens(-5)).toBe('—')
  })
})

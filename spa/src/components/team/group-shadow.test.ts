// spa/src/components/team/group-shadow.test.ts — the group shadow (TI-6, spec §4.2, §5): V0 only, the trial ended 2026-10-10.
import { describe, it, expect } from 'vitest'
import { groupShadow, shadowBase } from './group-shadow'

const C = '#a78bfa'
const mix = (base: string, n: number) => `color-mix(in oklab, ${base} ${n}%, transparent)`

describe('groupShadow', () => {
  it('dark: the crisp top-right 1px at 70 %', () => {
    expect(groupShadow(C, 'dark')).toBe(`1px -1px 0 ${mix(C, 70)}`)
  })
  it('light theme darkens the colour first (§5)', () => {
    const base = `color-mix(in oklab, ${C}, black 25%)`
    expect(shadowBase(C, 'light')).toBe(base)
    expect(shadowBase(C, 'dark')).toBe(C)
    expect(shadowBase(C, 'nord')).toBe(C)
    expect(groupShadow(C, 'light')).toBe(`1px -1px 0 ${mix(base, 70)}`)
  })
})

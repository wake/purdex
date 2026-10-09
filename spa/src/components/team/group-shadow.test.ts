// spa/src/components/team/group-shadow.test.ts — the group shadow of each trial variant (TI-6, spec §4.2 Shadow trial, §5).
import { describe, it, expect } from 'vitest'
import { groupShadow, shadowBase } from './group-shadow'

const C = '#a78bfa'
const mix = (base: string, n: number) => `color-mix(in oklab, ${base} ${n}%, transparent)`

describe('groupShadow (dark)', () => {
  it('V0: today\'s crisp top-right 1px at 70 %', () => {
    expect(groupShadow('v0', C, 'dark')).toBe(`1px -1px 0 ${mix(C, 70)}`)
  })
  it('V1: V0 + a soft halo at 30 %', () => {
    expect(groupShadow('v1', C, 'dark')).toBe(`1px -1px 0 ${mix(C, 70)}, 0 0 4px ${mix(C, 30)}`)
  })
  it('V2: V0 + a bottom-left spread at 40 %', () => {
    expect(groupShadow('v2', C, 'dark')).toBe(`1px -1px 0 ${mix(C, 70)}, -1px 1px 3px ${mix(C, 40)}`)
  })
  it('V3: a fuller lift, 60 % + 35 %', () => {
    expect(groupShadow('v3', C, 'dark')).toBe(`1px -1px 0 ${mix(C, 60)}, -2px 2px 5px ${mix(C, 35)}`)
  })
})

describe('groupShadow (light theme darkens the colour first, §5)', () => {
  const base = `color-mix(in oklab, ${C}, black 25%)`
  it('the base is darkened only in the light theme', () => {
    expect(shadowBase(C, 'light')).toBe(base)
    expect(shadowBase(C, 'dark')).toBe(C)
    expect(shadowBase(C, 'nord')).toBe(C)
  })
  it.each([
    ['v0', `1px -1px 0 ${mix(base, 70)}`],
    ['v1', `1px -1px 0 ${mix(base, 70)}, 0 0 4px ${mix(base, 30)}`],
    ['v2', `1px -1px 0 ${mix(base, 70)}, -1px 1px 3px ${mix(base, 40)}`],
    ['v3', `1px -1px 0 ${mix(base, 60)}, -2px 2px 5px ${mix(base, 35)}`],
  ] as const)('%s', (variant, expected) => {
    expect(groupShadow(variant, C, 'light')).toBe(expected)
  })
})

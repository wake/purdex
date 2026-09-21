import { describe, it, expect } from 'vitest'
import { canTintProfile, defaultSlaveName, COLOR_NEEDS_ICON } from './profile-rules'

describe('canTintProfile — the colour tints an icon, and the logo is a bitmap', () => {
  it('no icon chosen (the logo) → the colour has nothing to tint', () => {
    expect(COLOR_NEEDS_ICON).toBe(true)
    expect(canTintProfile({})).toBe(false)
    expect(canTintProfile({ color: '#ef4444' })).toBe(false)
  })

  it('an icon chosen → the colour applies', () => {
    expect(canTintProfile({ icon: 'House' })).toBe(true)
    expect(canTintProfile({ icon: 'House', color: '#ef4444' })).toBe(true)
  })
})

describe('defaultSlaveName — the device name, never equal to a name already there', () => {
  it('the device name as is when nobody has it', () => {
    expect(defaultSlaveName('Mini', [])).toBe('Mini')
    expect(defaultSlaveName('Mini', ['Air', 'Mini 2'])).toBe('Mini')
  })

  it('the next free number when it is taken', () => {
    expect(defaultSlaveName('Mini', ['Mini'])).toBe('Mini 2')
    expect(defaultSlaveName('Mini', ['Mini', 'Mini 2', 'Mini 3'])).toBe('Mini 4')
  })

  it('compares what the store would keep: a device name is normalised first', () => {
    expect(defaultSlaveName('  Mini  ', ['Mini'])).toBe('Mini 2')
  })

  it('a name at the 64 code point limit keeps its number: the base gives way', () => {
    const long = 'x'.repeat(64)
    const next = defaultSlaveName(long, [long])
    expect(Array.from(next)).toHaveLength(64)
    expect(next.endsWith(' 2')).toBe(true)
    expect(next).not.toBe(long)
  })

  it('a blank device name still yields a name', () => {
    expect(defaultSlaveName('   ', [])).not.toBe('')
  })
})

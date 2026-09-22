import { describe, it, expect } from 'vitest'
import { canTintProfile, defaultSlaveName, COLOR_NEEDS_ICON, sotActionStillValid, sotDeleteBlocked, sotScopeOf, wizardSotScopeOf } from './profile-rules'

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

describe('sotActionStillValid — an action on a SOT profile belongs to the master it was opened under', () => {
  const rows = [{ id: 'p1' }, { id: 'p2' }]
  const under = sotScopeOf('h1', 'p1')

  it('the same host, the same attached profile, the profile still listed → it may be sent', () => {
    expect(sotActionStillValid(under, sotScopeOf('h1', 'p1'), 'p2', rows)).toBe(true)
  })

  it('ANOTHER HOST now — even though it lists a profile of the same id → not sent', () => {
    expect(sotActionStillValid(under, sotScopeOf('h2', 'p1'), 'p2', rows)).toBe(false)
  })

  it('another profile attached now → not sent (what may be deleted was decided under the old one)', () => {
    expect(sotActionStillValid(under, sotScopeOf('h1', 'p2'), 'p2', rows)).toBe(false)
  })

  it('the profile is not in the list any more, or there is no list → not sent', () => {
    expect(sotActionStillValid(under, under, 'p3', rows)).toBe(false)
    expect(sotActionStillValid(under, under, 'p2', null)).toBe(false)
  })

  it('a host id with the separator in it cannot pass for another pair', () => {
    expect(sotScopeOf('a|b', 'c')).not.toBe(sotScopeOf('a', 'b|c'))
  })
})

describe('sotDeleteBlocked — delete is offered only where the FETCHED index shows nobody attached', () => {
  const row = (id: string, attached: number) => ({ id, attachments: Array.from({ length: attached }, () => ({})) })

  it('nobody attached, not the one this device syncs with → offered', () => {
    expect(sotDeleteBlocked(row('p2', 0), 'p1')).toBeNull()
    expect(sotDeleteBlocked(row('p2', 0), null)).toBeNull()
  })

  it('anybody attached → blocked as attached', () => {
    expect(sotDeleteBlocked(row('p2', 1), 'p1')).toBe('attached')
    expect(sotDeleteBlocked(row('p2', 2), null)).toBe('attached')
  })

  it('the one this device syncs with → blocked as current, even when the index lists nobody', () => {
    expect(sotDeleteBlocked(row('p1', 0), 'p1')).toBe('current')
    expect(sotDeleteBlocked(row('p1', 3), 'p1')).toBe('current')
  })
})

describe('wizardSotScopeOf — a delete in the wizard belongs to the host AND the step it was opened on', () => {
  it('the same host on the profile step → the same scope', () => {
    expect(wizardSotScopeOf('h1', 'sot')).toBe(wizardSotScopeOf('h1', 'sot'))
  })

  it('another host, another step, or no host → another scope', () => {
    expect(wizardSotScopeOf('h2', 'sot')).not.toBe(wizardSotScopeOf('h1', 'sot'))
    expect(wizardSotScopeOf('h1', 'local')).not.toBe(wizardSotScopeOf('h1', 'sot'))
    expect(wizardSotScopeOf(null, 'sot')).not.toBe(wizardSotScopeOf('h1', 'sot'))
  })

  it('is never a Settings scope', () => {
    expect(wizardSotScopeOf('h1', 'sot')).not.toBe(sotScopeOf('h1', 'sot'))
  })
})

import { describe, it, expect } from 'vitest'
import { pluralKey } from './plural'

describe('pluralKey', () => {
  it('picks _one for count === 1', () => {
    expect(pluralKey('settings.sync.conflict.banner', 1)).toBe('settings.sync.conflict.banner_one')
  })

  it('picks _other for count === 0', () => {
    expect(pluralKey('settings.sync.conflict.banner', 0)).toBe('settings.sync.conflict.banner_other')
  })

  it('picks _other for count > 1', () => {
    expect(pluralKey('settings.sync.conflict.banner', 2)).toBe('settings.sync.conflict.banner_other')
    expect(pluralKey('settings.sync.conflict.banner', 42)).toBe('settings.sync.conflict.banner_other')
  })
})

import { describe, it, expect } from 'vitest'
import { countUnits, validatePersona, PERSONA_LIMITS } from './persona-limits'
import { makePersona } from './__fixtures__/personas'

describe('countUnits', () => {
  it('counts runes for zh and words for en', () => {
    expect(countUnits('冷靜的人', 'zh')).toBe(4)
    expect(countUnits('😀😀', 'zh')).toBe(2)
    expect(countUnits('calm and  kind', 'en')).toBe(3)
    expect(countUnits('  ', 'en')).toBe(0)
  })
})

describe('validatePersona', () => {
  const fields = (p: ReturnType<typeof makePersona>) => validatePersona(p).map((i) => i.field)

  it('accepts a well-formed persona', () => {
    expect(validatePersona(makePersona('hiro'))).toEqual([])
  })

  it('checks the code rule', () => {
    for (const bad of ['ab', 'abcdefghijklm', 'Hiro', 'hi-ro', 'hi1']) {
      expect(fields(makePersona(bad))).toContain('code')
    }
  })

  it('checks the display name: 1..24 runes after trimming', () => {
    expect(fields(makePersona('hiro', { name: '   ' }))).toContain('name')
    expect(fields(makePersona('hiro', { name: ' Hiro ' }))).toContain('name') // the wire shape is already trimmed
    expect(fields(makePersona('hiro', { name: 'x'.repeat(PERSONA_LIMITS.name + 1) }))).toContain('name')
    expect(fields(makePersona('hiro', { name: '字'.repeat(PERSONA_LIMITS.name) }))).not.toContain('name')
    expect(fields(makePersona('hiro', { name: 'a\u0007b' }))).toContain('name')
  })

  it('checks the text limits per language', () => {
    const long = makePersona('hiro', { personality: { zh: '字'.repeat(51), en: 'w '.repeat(51) } })
    expect(validatePersona(long).map((i) => i.field)).toEqual(['personality.zh', 'personality.en'])
    const interests = makePersona('hiro', { interests: { zh: '字'.repeat(11), en: 'a b c d e f g h i j k' } })
    expect(validatePersona(interests).map((i) => i.field)).toEqual(['interests.zh', 'interests.en'])
  })

  it('checks the stored avatar: type, and 32 KB as a string', () => {
    expect(fields(makePersona('hiro', { avatar_data: 'http://x/y.png' }))).toContain('avatar_data')
    expect(fields(makePersona('hiro', { avatar_data: 'data:image/webp;base64,' + 'A'.repeat(PERSONA_LIMITS.avatarData) }))).toContain('avatar_data')
    expect(fields(makePersona('hiro', { avatar_data: 'data:image/png;base64,AA==' }))).not.toContain('avatar_data')
  })

  it('checks the avatar recipe against 2 KB', () => {
    const big = makePersona('hiro', { avatar: { source: 'micah', options: { x: 'y'.repeat(PERSONA_LIMITS.recipe) } } })
    expect(fields(big)).toContain('avatar')
    // 700 CJK characters are 700 UTF-16 units but about 2.1 KB in UTF-8
    const cjk = makePersona('hiro', { avatar: { source: 'micah', seed: '字'.repeat(700) } })
    expect(fields(cjk)).toContain('avatar')
  })
})

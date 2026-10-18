import { describe, it, expect } from 'vitest'
import { countUnits, validatePersona, PERSONA_LIMITS } from './persona-limits'
import { makePersona } from './__fixtures__/personas'

const PNG = 'data:image/png;base64,iVBORw0KGgo='
const JPEG = 'data:image/jpeg;base64,/9j/4AAQSkZJRg=='

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

  it('checks each nationality and template text against 200 runes, as the daemon does', () => {
    const at = (n: number) => '字'.repeat(n)
    expect(validatePersona(makePersona('hiro', { nationality: { code: 'JP', zh: at(200), en: 'x'.repeat(200) } }))).toEqual([])
    const over = makePersona('hiro', {
      nationality: { code: at(201), zh: at(201), en: at(201) },
      template: { name: at(201), name_zh: at(201), field_zh: at(201), field_en: at(201) },
    })
    expect(validatePersona(over).map((i) => i.field)).toEqual([
      'nationality.code', 'nationality.zh', 'nationality.en',
      'template.name', 'template.name_zh', 'template.field_zh', 'template.field_en',
    ])
  })

  it('counts runes, not UTF-16 units, for those texts', () => {
    const emoji = '😀'.repeat(200) // 400 UTF-16 units, 200 runes
    expect(validatePersona(makePersona('hiro', { nationality: { code: '', zh: emoji, en: '' } }))).toEqual([])
    expect(validatePersona(makePersona('hiro', { nationality: { code: '', zh: emoji + '😀', en: '' } })).map((i) => i.field)).toEqual(['nationality.zh'])
  })

  it('checks the stored avatar: type, and 32 KB as a string', () => {
    expect(fields(makePersona('hiro', { avatar_data: 'http://x/y.png' }))).toContain('avatar_data')
    expect(fields(makePersona('hiro', { avatar_data: 'data:image/webp;base64,' + 'A'.repeat(PERSONA_LIMITS.avatarData) }))).toContain('avatar_data')
    expect(fields(makePersona('hiro', { avatar_data: PNG }))).not.toContain('avatar_data')
    expect(fields(makePersona('hiro', { avatar_data: 'data:image/png;base64,garbage!' }))).toContain('avatar_data')
    expect(fields(makePersona('hiro', { avatar_data: 'data:image/png;base64,AAAAAAAAAAAA' }))).toContain('avatar_data') // not a PNG
    expect(fields(makePersona('hiro', { avatar_data: JPEG.replace('jpeg', 'png') }))).toContain('avatar_data') // type and bytes disagree
    expect(fields(makePersona('hiro', { avatar_data: JPEG }))).not.toContain('avatar_data')
  })

  it('checks the avatar recipe against 2 KB', () => {
    const big = makePersona('hiro', { avatar: { source: 'micah', options: { x: 'y'.repeat(PERSONA_LIMITS.recipe) } } })
    expect(fields(big)).toContain('avatar')
    // 700 CJK characters are 700 UTF-16 units but about 2.1 KB in UTF-8
    const cjk = makePersona('hiro', { avatar: { source: 'micah', seed: '字'.repeat(700) } })
    expect(fields(cjk)).toContain('avatar')
  })
})

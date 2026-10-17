// spa/src/lib/journal/person-name.test.ts — splitting a text around the person's name (J-2c, spec §2 item 23).
import { describe, it, expect } from 'vitest'
import { splitByName } from './person-name'

const hits = (text: string, name: string) => splitByName(text, name).filter((p) => p.hit).map((p) => p.text)
const joined = (text: string, name: string) => splitByName(text, name).map((p) => p.text).join('')

describe('splitByName', () => {
  it('marks every occurrence, keeps the text between, and loses nothing', () => {
    const parts = splitByName('Kenji → Wake: Wake, ok?', 'Wake')
    expect(parts).toEqual([
      { text: 'Kenji → ', hit: false }, { text: 'Wake', hit: true }, { text: ': ', hit: false }, { text: 'Wake', hit: true }, { text: ', ok?', hit: false },
    ])
  })
  it('works with a Unicode name', () => {
    expect(hits('小明請你看一下，小明', '小明')).toEqual(['小明', '小明'])
    expect(joined('Émile → Zoë 的 Émile', 'Émile')).toBe('Émile → Zoë 的 Émile')
    expect(hits('Émile → Zoë 的 Émile', 'Émile')).toEqual(['Émile', 'Émile'])
  })
  it('escapes regex characters in the name (no throw, literal match only)', () => {
    expect(hits('a.b and axb and (a.b)', 'a.b')).toEqual(['a.b', 'a.b'])
    expect(hits('x (Wake) y', '(Wake)')).toEqual(['(Wake)'])
    expect(() => splitByName('text', '[')).not.toThrow()
    expect(hits('[ok]', '[')).toEqual(['['])
  })
  it('a Latin name is a whole word: Al is not marked inside Alert or Walnut (M: no word boundary)', () => {
    expect(hits('Alert: Walnut and Alan', 'Al')).toEqual([])
    expect(hits('Wake and Wakefield', 'Wake')).toEqual(['Wake'])
    expect(hits('ask Al, (Al) or al', 'Al')).toEqual(['Al', 'Al', 'al'])
    expect(hits('Al', 'Al')).toEqual(['Al'])
    expect(hits('Al is here; so is Al', 'Al')).toEqual(['Al', 'Al'])
    expect(hits('R2D2 and R2', 'R2')).toEqual(['R2'])
    expect(joined('ask Al, (Al) or al', 'Al')).toBe('ask Al, (Al) or al')
  })
  it('a CJK name is found inside a sentence, with no word boundary', () => {
    expect(hits('請王小明看一下', '王小明')).toEqual(['王小明'])
  })
  it('an empty or missing name marks nothing', () => {
    expect(splitByName('Wake is here', '')).toEqual([{ text: 'Wake is here', hit: false }])
    expect(splitByName('Wake is here', '   ')).toEqual([{ text: 'Wake is here', hit: false }])
    expect(splitByName('Wake is here', undefined)).toEqual([{ text: 'Wake is here', hit: false }])
  })
  it('an empty text yields nothing to draw', () => {
    expect(splitByName('', 'Wake')).toEqual([])
  })
  it('a name that does not occur leaves the text whole', () => {
    expect(splitByName('Kenji → Adam', 'Wake')).toEqual([{ text: 'Kenji → Adam', hit: false }])
  })
})

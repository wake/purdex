import { describe, it, expect } from 'vitest'
import { parsePersonasJson, planImport } from './persona-import'
import { makePersona } from './__fixtures__/personas'

const raw = (id: string, over: Record<string, unknown> = {}) => ({
  id, name: id.toUpperCase(), sex: 'female', nationality: { code: 'XX', zh: '測', en: 'T' },
  personality: { zh: '靜', en: 'calm' }, method: { zh: '法', en: 'way' }, interests: { zh: '茶', en: 'tea' }, ...over,
})

describe('parsePersonasJson', () => {
  it('reads an array, or an object holding items/personas', () => {
    expect(parsePersonasJson(JSON.stringify([raw('hiro')]))).toHaveLength(1)
    expect(parsePersonasJson(JSON.stringify({ items: [raw('hiro')] }))).toHaveLength(1)
    expect(parsePersonasJson(JSON.stringify({ personas: [raw('hiro'), raw('emma')] }))).toHaveLength(2)
  })
  it('throws on text that is not a list of personas', () => {
    expect(() => parsePersonasJson('not json')).toThrow()
    expect(() => parsePersonasJson('{"a":1}')).toThrow()
  })
})

describe('planImport', () => {
  it('maps id to code and counts new, changed, unchanged and skipped', () => {
    const existing = [makePersona('hiro', { name: 'Hiro' }), makePersona('emma', { name: 'EMMA' })]
    const plan = planImport(existing, [
      raw('olga'), // new
      raw('hiro', { name: 'Hiro Two' }), // changed
      raw('emma'), // same name as existing: unchanged on the imported fields? differs (fields differ), see below
      raw('X!'), // bad code
      raw('olga'), // duplicate in the file
    ])
    expect(plan.added.map((p) => p.code)).toEqual(['olga'])
    expect(plan.changed.map((p) => p.code)).toContain('hiro')
    expect(plan.skipped.map((s) => [s.index, s.reason])).toEqual([[3, 'code'], [4, 'duplicate']])
    expect(plan.result.map((p) => p.code)).toEqual(['hiro', 'emma', 'olga'])
  })

  it('keeps the stored avatar and retired mark of a persona it changes', () => {
    const existing = [makePersona('hiro', { retired: true, avatar_data: 'data:image/png;base64,iVBORw0KGgo=', avatar: { source: 'micah', seed: 'x' } })]
    const plan = planImport(existing, [raw('hiro', { name: 'Hiro Two' })])
    expect(plan.result[0]).toMatchObject({ name: 'Hiro Two', retired: true, avatar_data: 'data:image/png;base64,iVBORw0KGgo=', avatar: { source: 'micah' } })
  })

  it('keeps the existing value of a field the file entry leaves out', () => {
    const existing = [makePersona('hiro', { lead: true, sex: 'male', nationality: { code: 'JP', zh: '日本', en: 'Japan' } })]
    const plan = planImport(existing, [{ id: 'hiro', name: 'Hiro Two' }])
    expect(plan.result[0]).toMatchObject({
      name: 'Hiro Two', lead: true, sex: 'male', nationality: { code: 'JP' },
      personality: { en: 'calm' }, template: { name: 'Tester' },
    })
  })

  it('treats a nested object field by field the same way', () => {
    const existing = [makePersona('hiro', { nationality: { code: 'JP', zh: '日本', en: 'Japan' } })]
    const plan = planImport(existing, [{ id: 'hiro', name: 'Hiro', nationality: { zh: '東瀛' } }])
    expect(plan.result[0].nationality).toEqual({ code: 'JP', zh: '東瀛', en: 'Japan' })
  })

  it('skips what would pass 64 personas, with a reason', () => {
    const existing = Array.from({ length: 63 }, (_, i) => makePersona(`aa${String.fromCharCode(97 + (i % 26))}${String.fromCharCode(97 + Math.floor(i / 26))}`))
    const plan = planImport(existing, [raw('newa'), raw('newb')])
    expect(plan.added).toHaveLength(1)
    expect(plan.skipped).toEqual([{ index: 1, code: 'newb', reason: 'full' }])
  })

  it('skips an entry whose fields break the limits', () => {
    const plan = planImport([], [raw('hiro', { name: 'x'.repeat(40) })])
    expect(plan.skipped).toEqual([{ index: 0, code: 'hiro', reason: 'invalid', detail: 'name' }])
  })

  it('leaves identical entries out of both added and changed', () => {
    const existing = [planImport([], [raw('hiro')]).result[0]]
    const plan = planImport(existing, [raw('hiro')])
    expect(plan.added).toEqual([])
    expect(plan.changed).toEqual([])
    expect(plan.unchanged).toBe(1)
  })
})

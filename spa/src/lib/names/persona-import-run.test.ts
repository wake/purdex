import { describe, it, expect, vi } from 'vitest'
import { planImport } from './persona-import'
import { redrawForImport } from './persona-import-run'
import { makePersona } from './__fixtures__/personas'

const WEBP = 'data:image/webp;base64,UklGRgAAAABXRUJQ'
const recipe = (seed: string) => ({ source: 'lorelei', seed })
const entry = (id: string, seed: string) => ({ id, name: id.toUpperCase(), avatar: recipe(seed) })

describe('redrawForImport', () => {
  it('draws each recipe the plan marked and puts the image next to it', async () => {
    const existing = [makePersona('hiro', { avatar: recipe('old'), avatar_data: WEBP })]
    const plan = planImport(existing, [entry('hiro', 'new'), entry('emma', 'e')])
    const draw = vi.fn(async () => WEBP.replace('UklG', 'UklH'))
    const out = await redrawForImport(plan, existing, draw)
    expect(draw).toHaveBeenCalledTimes(2)
    expect(out.failed).toEqual([])
    expect(out.result.find((p) => p.code === 'hiro')).toMatchObject({ avatar: recipe('new'), avatar_data: WEBP.replace('UklG', 'UklH') })
    expect(out.result.find((p) => p.code === 'emma')!.avatar).toEqual(recipe('e'))
  })

  it('leaves alone a persona the plan did not mark', async () => {
    const existing = [makePersona('hiro', { avatar: recipe('same'), avatar_data: WEBP })]
    const plan = planImport(existing, [entry('hiro', 'same')])
    const draw = vi.fn(async () => 'x')
    const out = await redrawForImport(plan, existing, draw)
    expect(draw).not.toHaveBeenCalled()
    expect(out.result).toEqual(plan.result)
  })

  it('keeps the old recipe and image together when one draw fails, names the code, and still draws the others', async () => {
    const existing = [makePersona('hiro', { avatar: recipe('old'), avatar_data: WEBP })]
    const plan = planImport(existing, [entry('hiro', 'new'), entry('emma', 'e'), entry('olga', 'o')])
    const draw = vi.fn(async (r: { seed?: string }) => {
      if (r.seed === 'new' || r.seed === 'e') throw new Error('cannot draw')
      return WEBP
    })
    const out = await redrawForImport(plan, existing, draw)
    expect(out.failed.sort()).toEqual(['emma', 'hiro'])
    // an existing persona goes back to what it had, so the recipe never claims an image that was not drawn
    expect(out.result.find((p) => p.code === 'hiro')).toMatchObject({ avatar: recipe('old'), avatar_data: WEBP })
    // a new one starts with initials
    expect(out.result.find((p) => p.code === 'emma')).toMatchObject({ avatar: { source: '' }, avatar_data: '' })
    expect(out.result.find((p) => p.code === 'olga')).toMatchObject({ avatar: recipe('o'), avatar_data: WEBP })
  })

  it('gives a recipe with no source no image, without drawing', async () => {
    const existing = [makePersona('hiro', { avatar: recipe('old'), avatar_data: WEBP })]
    const plan = planImport(existing, [{ id: 'hiro', name: 'HIRO', avatar: { source: '' } }])
    const draw = vi.fn(async () => 'x')
    const out = await redrawForImport(plan, existing, draw)
    expect(draw).not.toHaveBeenCalled()
    expect(out.result.find((p) => p.code === 'hiro')).toMatchObject({ avatar: { source: '' }, avatar_data: '' })
  })

  it('draws one at a time, so a large file does not start dozens of renders at once', async () => {
    let running = 0
    let peak = 0
    const draw = async () => {
      running++; peak = Math.max(peak, running)
      await new Promise((r) => setTimeout(r, 1))
      running--
      return WEBP
    }
    const plan = planImport([], ['aaa', 'bbb', 'ccc', 'ddd'].map((id) => entry(id, id)))
    await redrawForImport(plan, [], draw)
    expect(peak).toBe(1)
  })

  it('does not change the plan it was given', async () => {
    const plan = planImport([], [entry('emma', 'e')])
    const before = structuredClone(plan)
    await redrawForImport(plan, [], async () => WEBP)
    expect(plan).toEqual(before)
  })
})

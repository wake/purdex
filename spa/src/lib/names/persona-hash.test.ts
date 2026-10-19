import { describe, it, expect } from 'vitest'
import { canonical, itemsHash } from './persona-hash'
import { makeCollection } from './__fixtures__/personas'

describe('canonical', () => {
  it('sorts object keys at every depth and keeps array order', () => {
    expect(canonical({ b: 1, a: { d: [2, 1], c: null } })).toBe('{"a":{"c":null,"d":[2,1]},"b":1}')
  })
})

describe('itemsHash', () => {
  const items = makeCollection(['hiro', 'olga', 'emma']).items

  it('is the same for the same items in any order', async () => {
    expect(await itemsHash(items)).toBe(await itemsHash([...items].reverse()))
  })

  it('differs when any field of any item differs', async () => {
    const edited = items.map((p, i) => (i === 1 ? { ...p, name: `${p.name}!` } : p))
    expect(await itemsHash(edited)).not.toBe(await itemsHash(items))
  })

  it('is 64 lowercase hex characters', async () => {
    expect(await itemsHash(items)).toMatch(/^[0-9a-f]{64}$/)
  })
})

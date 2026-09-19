import { describe, it, expect } from 'vitest'
import { structuralKey, hashSection } from './hash'

function deepFreeze<T>(v: T): T {
  if (v !== null && typeof v === 'object') {
    for (const child of Object.values(v as Record<string, unknown>)) deepFreeze(child)
    Object.freeze(v)
  }
  return v
}

/** A payload with every JSON kind in it: nesting, arrays, unicode, numbers, booleans, null. */
function sample(): Record<string, unknown> {
  return {
    hosts: {
      h2: { name: '工作站 — air', port: 7860, order: 1, tags: ['dev', 'ティ', '🚀'] },
      h1: { name: 'mlab', port: 7860.5, order: 0, enabled: true, icon: null },
    },
    order: ['h1', 'h2'],
    nested: [[1, 2], [{ b: 2, a: 1 }], []],
    empty: {},
    negative: -12,
    exp: 1e21,
    off: false,
    text: 'quote " backslash \\ newline \n',
  }
}

describe('structuralKey', () => {
  it('ignores object key order', () => {
    expect(structuralKey({ a: 1, b: 2 })).toBe(structuralKey({ b: 2, a: 1 }))
  })

  it('sorts keys at every depth', () => {
    const a = { outer: { y: [{ n: 1, m: 2 }], x: 1 }, first: true }
    const b = { first: true, outer: { x: 1, y: [{ m: 2, n: 1 }] } }
    expect(structuralKey(a)).toBe(structuralKey(b))
    expect(structuralKey(a)).toBe('{"first":true,"outer":{"x":1,"y":[{"m":2,"n":1}]}}')
  })

  it('sorts integer-like keys as strings, like every other key', () => {
    expect(structuralKey({ 10: 'a', 9: 'b', b: 1, a: 2 })).toBe('{"10":"a","9":"b","a":2,"b":1}')
  })

  it('keeps array order', () => {
    expect(structuralKey([1, 2])).not.toBe(structuralKey([2, 1]))
    expect(structuralKey({ order: ['a', 'b'] })).not.toBe(structuralKey({ order: ['b', 'a'] }))
  })

  it('treats an undefined member as absent', () => {
    expect(structuralKey({ name: 'x', icon: undefined })).toBe(structuralKey({ name: 'x' }))
    expect(structuralKey({ icon: undefined })).toBe('{}')
    expect(structuralKey({ h: { icon: undefined } })).toBe('{"h":{}}')
  })

  it('does not treat null as absent', () => {
    expect(structuralKey({ icon: null })).toBe('{"icon":null}')
    expect(structuralKey({ icon: null })).not.toBe(structuralKey({}))
  })

  it('serialises primitives the way JSON does', () => {
    expect(structuralKey(null)).toBe('null')
    expect(structuralKey(true)).toBe('true')
    expect(structuralKey(1.5)).toBe('1.5')
    expect(structuralKey('中文 "q"')).toBe(JSON.stringify('中文 "q"'))
    expect(structuralKey([])).toBe('[]')
    expect(structuralKey({})).toBe('{}')
  })

  it('serialises -0 as 0', () => {
    expect(structuralKey(-0)).toBe('0')
    expect(structuralKey({ n: -0 })).toBe(structuralKey({ n: 0 }))
  })

  it('keeps an own "__proto__" key as data', () => {
    const parsed: unknown = JSON.parse('{"__proto__":{"a":1},"b":2}')
    expect(structuralKey(parsed)).toBe('{"__proto__":{"a":1},"b":2}')
  })

  it('accepts null-prototype objects', () => {
    const o = Object.create(null) as Record<string, unknown>
    o.b = 1
    o.a = 2
    expect(structuralKey(o)).toBe('{"a":2,"b":1}')
  })

  it('matches JSON.stringify of the JSON round-trip for an already-sorted value', () => {
    const v = { a: [1, { b: null, c: 'x' }], d: { e: false } }
    expect(structuralKey(v)).toBe(JSON.stringify(v))
  })

  it('does not mutate its input', () => {
    const input = deepFreeze(sample())
    const before = JSON.stringify(input)
    expect(() => structuralKey(input)).not.toThrow()
    expect(JSON.stringify(input)).toBe(before)
    expect(Object.keys(input.hosts as object)).toEqual(['h2', 'h1'])
  })

  describe('rejects what cannot round-trip JSON, naming the path', () => {
    it('top-level undefined', () => {
      expect(() => structuralKey(undefined)).toThrow(/\(root\).*undefined/)
    })

    it('undefined inside an array', () => {
      expect(() => structuralKey({ tabs: ['a', undefined] })).toThrow(/tabs\[1\].*undefined/)
    })

    it('a hole in a sparse array', () => {
      const sparse: unknown[] = []
      sparse[2] = 'x'
      expect(() => structuralKey({ tabs: sparse })).toThrow(/tabs\[0\].*undefined/)
    })

    it('NaN', () => {
      expect(() => structuralKey({ hosts: { h1: { port: NaN } } })).toThrow(/hosts\.h1\.port.*NaN/)
    })

    it('Infinity and -Infinity', () => {
      expect(() => structuralKey({ hosts: { h1: { port: Infinity } } })).toThrow(
        /hosts\.h1\.port.*Infinity/,
      )
      expect(() => structuralKey({ sizes: [50, -Infinity] })).toThrow(/sizes\[1\].*-Infinity/)
    })

    it('function', () => {
      expect(() => structuralKey({ a: { onClick: () => 1 } })).toThrow(/a\.onClick.*function/)
    })

    it('symbol', () => {
      expect(() => structuralKey({ a: [Symbol('s')] })).toThrow(/a\[0\].*symbol/)
    })

    it('bigint', () => {
      expect(() => structuralKey({ big: { n: 10n } })).toThrow(/big\.n.*bigint/)
    })

    it('Date', () => {
      expect(() => structuralKey({ at: new Date(0) })).toThrow(/at.*Date/)
    })

    it('Map and Set', () => {
      expect(() => structuralKey({ m: new Map() })).toThrow(/m.*Map/)
      expect(() => structuralKey({ s: new Set() })).toThrow(/s.*Set/)
    })

    it('class instance', () => {
      class Host {
        name = 'x'
      }
      expect(() => structuralKey({ hosts: [new Host()] })).toThrow(/hosts\[0\].*Host/)
    })

    it('a non-plain value at the top level', () => {
      expect(() => structuralKey(new Date(0))).toThrow(/\(root\).*Date/)
    })

    it('a circular reference', () => {
      const a: Record<string, unknown> = { name: 'x' }
      a.self = { back: a }
      expect(() => structuralKey({ a })).toThrow(/a\.self\.back.*circular/)
    })

    it('accepts the same object referenced twice without a cycle', () => {
      const shared = { n: 1 }
      expect(structuralKey({ x: shared, y: [shared] })).toBe('{"x":{"n":1},"y":[{"n":1}]}')
    })

    it('quotes a key that is not a plain identifier in the path', () => {
      expect(() => structuralKey({ 'tabs.ws-1': { n: NaN } })).toThrow(/\["tabs\.ws-1"\]\.n/)
    })
  })
})

describe('hashSection', () => {
  it('hashes {} to the known SHA-256 of "{}"', async () => {
    expect(await hashSection({})).toBe(
      '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a',
    )
  })

  it('returns 64 lowercase hex chars', async () => {
    expect(await hashSection(sample())).toMatch(/^[0-9a-f]{64}$/)
  })

  it('is independent of key order but not of array order', async () => {
    expect(await hashSection({ a: 1, b: [1, 2] })).toBe(await hashSection({ b: [1, 2], a: 1 }))
    expect(await hashSection({ a: 1, b: [1, 2] })).not.toBe(await hashSection({ a: 1, b: [2, 1] }))
  })

  it('treats an undefined member as absent, and null as present', async () => {
    expect(await hashSection({ icon: undefined })).toBe(await hashSection({}))
    expect(await hashSection({ icon: null })).not.toBe(await hashSection({}))
  })

  it('hashes -0 and 0 the same', async () => {
    expect(await hashSection({ n: -0 })).toBe(await hashSection({ n: 0 }))
  })

  it('survives a JSON round-trip unchanged', async () => {
    const x = { ...sample(), gone: undefined }
    const roundTripped: unknown = JSON.parse(JSON.stringify(x))
    expect(await hashSection(roundTripped)).toBe(await hashSection(x))
  })

  it('rejects instead of hashing an unrepresentable payload', async () => {
    await expect(hashSection({ hosts: { h1: { port: NaN } } })).rejects.toThrow(/hosts\.h1\.port/)
    await expect(hashSection(undefined)).rejects.toThrow(/\(root\)/)
  })

  it('does not mutate its input', async () => {
    const input = deepFreeze(sample())
    const before = JSON.stringify(input)
    await hashSection(input)
    expect(JSON.stringify(input)).toBe(before)
  })
})

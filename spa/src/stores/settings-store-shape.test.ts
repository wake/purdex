import { describe, it, expect } from 'vitest'
import { deepFreeze, sanitizeFlatModuleMap, sanitizeScopedModuleMap } from './settings-store-shape'

// Finding C: malformed persisted JSON like `{"__proto__":{"x":1}}` parses into
// a regular object where `__proto__` / `constructor` / `prototype` are *own*
// enumerable properties. Copying those keys back onto a fresh `{}` with
// bracket notation triggers the proto setter (prototype pollution) or
// overwrites the constructor slot, producing phantom settings entries via
// prototype-chain lookups (`hostId in state.hosts`, `state.hosts[hostId]`).

// Phantom lookups are the real attack: after heal the *healed* object's chain
// must still be Object.prototype, and none of the injected payload keys may
// be observable via bracket access on healed.

describe('sanitizeFlatModuleMap — prototype pollution hardening', () => {
  it('rejects __proto__ key and leaves healed prototype unchanged', () => {
    const malformed = JSON.parse('{"__proto__":{"polluted":"yes"}}')
    const healed = sanitizeFlatModuleMap(malformed)
    expect(Object.getPrototypeOf(healed)).toBe(Object.prototype)
    expect((healed as Record<string, unknown>).polluted).toBeUndefined()
    expect(Object.prototype.hasOwnProperty.call(healed, '__proto__')).toBe(false)
  })

  it('rejects constructor key without replacing the constructor slot', () => {
    const malformed = JSON.parse('{"constructor":{"prototype":{"polluted":"yes"}}}')
    const healed = sanitizeFlatModuleMap(malformed)
    expect(healed.constructor).toBe(Object)
    expect(Object.prototype.hasOwnProperty.call(healed, 'constructor')).toBe(false)
  })

  it('rejects prototype key', () => {
    const malformed = JSON.parse('{"prototype":{"polluted":"yes"}}')
    const healed = sanitizeFlatModuleMap(malformed)
    expect(Object.prototype.hasOwnProperty.call(healed, 'prototype')).toBe(false)
    expect((healed as Record<string, unknown>).prototype).toBeUndefined()
  })

  it('preserves normal module ids alongside rejected dangerous keys', () => {
    const malformed = JSON.parse(
      '{"__proto__":{"polluted":"yes"},"editor":{"tabSize":2},"constructor":{"x":1}}',
    )
    const healed = sanitizeFlatModuleMap(malformed)
    expect(healed.editor).toEqual({ tabSize: 2 })
    expect(Object.getPrototypeOf(healed)).toBe(Object.prototype)
    expect(Object.prototype.hasOwnProperty.call(healed, '__proto__')).toBe(false)
    expect(Object.prototype.hasOwnProperty.call(healed, 'constructor')).toBe(false)
    expect((healed as Record<string, unknown>).polluted).toBeUndefined()
  })
})

describe('sanitizeScopedModuleMap — prototype pollution hardening', () => {
  it('rejects __proto__ at the outer (scope id) level', () => {
    const malformed = JSON.parse('{"__proto__":{"editor":{"polluted":"yes"}}}')
    const healed = sanitizeScopedModuleMap(malformed)
    expect(Object.getPrototypeOf(healed)).toBe(Object.prototype)
    expect((healed as Record<string, unknown>).editor).toBeUndefined()
    expect(Object.prototype.hasOwnProperty.call(healed, '__proto__')).toBe(false)
  })

  it('rejects __proto__ at the inner (module id) level', () => {
    const malformed = JSON.parse(
      '{"host-1":{"__proto__":{"polluted":"yes"},"editor":{"tabSize":4}}}',
    )
    const healed = sanitizeScopedModuleMap(malformed)
    const inner = healed['host-1']
    expect(Object.getPrototypeOf(inner)).toBe(Object.prototype)
    expect(inner).toEqual({ editor: { tabSize: 4 } })
    expect(Object.prototype.hasOwnProperty.call(inner, '__proto__')).toBe(false)
    expect((inner as Record<string, unknown>).polluted).toBeUndefined()
  })

  it('preserves normal scope ids alongside rejected dangerous keys', () => {
    const malformed = JSON.parse(
      '{"host-1":{"editor":{"tabSize":2}},"__proto__":{"editor":{"x":1}},"host-2":{"constructor":{"y":2},"editor":{"tabSize":4}}}',
    )
    const healed = sanitizeScopedModuleMap(malformed)
    expect(Object.prototype.hasOwnProperty.call(healed, '__proto__')).toBe(false)
    expect(healed['host-1']).toEqual({ editor: { tabSize: 2 } })
    expect(healed['host-2']).toEqual({ editor: { tabSize: 4 } })
    expect(Object.prototype.hasOwnProperty.call(healed['host-2'], 'constructor')).toBe(false)
  })
})

describe('deepFreeze', () => {
  it('returns primitives and null untouched', () => {
    expect(deepFreeze(null)).toBe(null)
    expect(deepFreeze(undefined)).toBe(undefined)
    expect(deepFreeze(42)).toBe(42)
    expect(deepFreeze('x')).toBe('x')
  })

  it('freezes the top-level object', () => {
    const frozen = deepFreeze({ a: 1 })
    expect(Object.isFrozen(frozen)).toBe(true)
  })

  it('freezes nested objects so deep mutation throws in strict mode', () => {
    const frozen = deepFreeze({ outer: { inner: { x: 1 } } })
    expect(Object.isFrozen(frozen.outer)).toBe(true)
    expect(Object.isFrozen(frozen.outer.inner)).toBe(true)
    expect(() => {
      ;(frozen.outer.inner as { x: number }).x = 999
    }).toThrow()
  })

  it('detaches the returned graph from the input (no ref sharing)', () => {
    const source = { nested: { x: 1 } }
    const frozen = deepFreeze(source)
    expect(frozen).not.toBe(source)
    expect(frozen.nested).not.toBe(source.nested)
    // Source stays mutable (we only freeze the returned copy)
    source.nested.x = 2
    expect(source.nested.x).toBe(2)
    expect(frozen.nested.x).toBe(1)
  })

  it('freezes arrays and array items', () => {
    const frozen = deepFreeze({ list: [{ a: 1 }, { a: 2 }] })
    expect(Object.isFrozen(frozen.list)).toBe(true)
    expect(Object.isFrozen(frozen.list[0])).toBe(true)
    expect(() => {
      ;(frozen.list as unknown as { a: number }[])[0].a = 999
    }).toThrow()
  })

  it('still deep-freezes a shallow-frozen container (no short-circuit on Object.isFrozen)', () => {
    // Regression (R2 codex): prior impl bailed early on Object.isFrozen(input),
    // leaving any mutable nested refs intact and still shared with the caller.
    const shallow = Object.freeze({ nested: { x: 1 } })
    const result = deepFreeze(shallow)
    expect(Object.isFrozen(result.nested)).toBe(true)
    expect(result.nested).not.toBe(shallow.nested)
    expect(() => {
      ;(result.nested as { x: number }).x = 999
    }).toThrow()
  })

  it('survives cyclic inputs without stack overflow', () => {
    type Node = { name: string; self?: Node }
    const a: Node = { name: 'root' }
    a.self = a
    const frozen = deepFreeze(a)
    expect(Object.isFrozen(frozen)).toBe(true)
    expect(frozen.name).toBe('root')
    // Cycle resolves to the frozen clone (self-reference preserved + frozen)
    expect(frozen.self).toBe(frozen)
    expect(Object.isFrozen(frozen.self!)).toBe(true)
  })

  it('aliased subgraphs resolve to the same frozen clone, not the mutable original (R3 codex)', () => {
    const shared = { count: 1 }
    const input = { a: shared, b: shared }
    const frozen = deepFreeze(input)
    // Both slots point to the SAME frozen clone — no mutable original leak
    expect(frozen.a).toBe(frozen.b)
    expect(Object.isFrozen(frozen.a)).toBe(true)
    expect(frozen.a).not.toBe(shared)
    expect(() => {
      ;(frozen.b as { count: number }).count = 999
    }).toThrow()
    // The original `shared` stays mutable (we only froze the clone)
    expect(shared.count).toBe(1)
  })
})

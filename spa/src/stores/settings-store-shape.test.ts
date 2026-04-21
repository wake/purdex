import { describe, it, expect } from 'vitest'
import { sanitizeFlatModuleMap, sanitizeScopedModuleMap } from './settings-store-shape'

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

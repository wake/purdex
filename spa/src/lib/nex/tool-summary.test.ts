// spa/src/lib/nex/tool-summary.test.ts
import { describe, it, expect } from 'vitest'
import { getSummary, toolSummary, previewValue, SUMMARY_LIMIT } from './tool-summary'

describe('getSummary (client table, moved from ToolCallBlock)', () => {
  it('Bash → command', () => {
    expect(getSummary('Bash', { command: 'ls -la' })).toBe('ls -la')
    expect(getSummary('Bash', {})).toBe('')
  })

  it('Read / Write / Edit → file_path', () => {
    expect(getSummary('Read', { file_path: '/tmp/a.txt' })).toBe('/tmp/a.txt')
    expect(getSummary('Write', { file_path: '/tmp/b.txt', content: 'x' })).toBe('/tmp/b.txt')
    expect(getSummary('Edit', { file_path: '/tmp/c.txt', old_string: 'a', new_string: 'b' })).toBe('/tmp/c.txt')
    expect(getSummary('Read', {})).toBe('')
  })

  it('WebFetch → url', () => {
    expect(getSummary('WebFetch', { url: 'https://example.com', prompt: 'p' })).toBe('https://example.com')
  })

  it('Grep / Glob → pattern', () => {
    expect(getSummary('Grep', { pattern: 'foo', path: '/x' })).toBe('foo')
    expect(getSummary('Glob', { pattern: '**/*.ts' })).toBe('**/*.ts')
  })

  it('Agent → description', () => {
    expect(getSummary('Agent', { description: 'Find files', prompt: 'long' })).toBe('Find files')
  })

  it('default → JSON of input sliced to 80 chars', () => {
    const input = { alpha: 'x'.repeat(100) }
    const json = JSON.stringify(input)
    expect(getSummary('SomethingElse', input)).toBe(json.slice(0, 80))
    expect(getSummary('SomethingElse', input)).toHaveLength(80)
    expect(getSummary('Other', {})).toBe('{}')
  })
})

describe('toolSummary (spec §4.4 R1)', () => {
  it('primaryArg present → value verbatim, not truncated even past 80 chars', () => {
    const value = '/very/long/' + 'segment/'.repeat(20) + 'file.txt'
    expect(value.length).toBeGreaterThan(80)
    expect(toolSummary('Read', { file_path: '/other' }, { primaryArg: { key: 'file_path', value } })).toBe(value)
  })

  it('primaryArg wins over the client table', () => {
    expect(toolSummary('Bash', { command: 'ls' }, { primaryArg: { key: 'command', value: 'from-n2' }, known: true })).toBe('from-n2')
  })

  it('known: false → first three own keys as `key: value`, scalars verbatim, objects as JSON', () => {
    expect(toolSummary('mcp__x__y', { a: 1, b: 'x', c: { d: 1 }, e: 2 }, { known: false })).toBe('a: 1, b: x, c: {"d":1}')
  })

  it('known: false → arrays / null / booleans stringified per rule', () => {
    expect(toolSummary('custom', { list: [1, 2], nothing: null, flag: true }, { known: false })).toBe('list: [1,2], nothing: null, flag: true')
  })

  it('known: false with empty input → empty string', () => {
    expect(toolSummary('custom', {}, { known: false })).toBe('')
  })

  it('known: false ignores prototype keys (own keys only)', () => {
    const proto = { inherited: 'no' }
    const input = Object.create(proto) as Record<string, unknown>
    input.own = 'yes'
    expect(toolSummary('custom', input, { known: false })).toBe('own: yes')
  })

  it('known: true + primaryArg: null (F9) → client table', () => {
    const input = { todos: [{ content: 'x' }] }
    expect(toolSummary('TodoWrite', input, { known: true, primaryArg: null })).toBe(getSummary('TodoWrite', input))
    expect(toolSummary('Bash', { command: 'echo hi' }, { known: true, primaryArg: null })).toBe('echo hi')
  })

  it('no entry → client table', () => {
    expect(toolSummary('Bash', { command: 'echo hi' })).toBe('echo hi')
    expect(toolSummary('Read', { file_path: '/tmp/a' }, undefined)).toBe('/tmp/a')
  })

  it('raw-only entry (no primaryArg / known fields) → client table', () => {
    expect(toolSummary('Grep', { pattern: 'foo' }, {})).toBe('foo')
  })

  it('known: true without primaryArg field → client table', () => {
    expect(toolSummary('Glob', { pattern: '*.ts' }, { known: true })).toBe('*.ts')
  })
})

// codex R2 A3: the R10 fallback must never run `JSON.stringify` over an
// arbitrarily large input value — serialisation is bounded by the limit.
describe('previewValue (bounded R10 serialisation)', () => {
  it('SUMMARY_LIMIT is the renderer truncation width (80)', () => {
    expect(SUMMARY_LIMIT).toBe(80)
  })

  it('scalars verbatim; null / undefined spelled out', () => {
    expect(previewValue('plain text', 80)).toBe('plain text')
    expect(previewValue(42, 80)).toBe('42')
    expect(previewValue(true, 80)).toBe('true')
    expect(previewValue(null, 80)).toBe('null')
    expect(previewValue(undefined, 80)).toBe('undefined')
  })

  it('a top-level string is returned as-is (the caller truncates)', () => {
    const s = 'x'.repeat(500)
    expect(previewValue(s, 80)).toBe(s)
  })

  it('small objects / arrays serialise exactly like JSON.stringify', () => {
    expect(previewValue({ d: 1 }, 80)).toBe('{"d":1}')
    expect(previewValue([1, 2], 80)).toBe('[1,2]')
    expect(previewValue({ a: 'x', b: [true, null], c: { d: 'y' } }, 80)).toBe('{"a":"x","b":[true,null],"c":{"d":"y"}}')
    expect(previewValue({}, 80)).toBe('{}')
    expect(previewValue([], 80)).toBe('[]')
    expect(previewValue({ q: 'he said "hi"\n' }, 80)).toBe(JSON.stringify({ q: 'he said "hi"\n' }))
  })

  it('a 100 000-element array stops at the limit and appends an ellipsis', () => {
    const big = Array.from({ length: 100_000 }, (_, i) => i)
    const t0 = performance.now()
    const out = previewValue(big, 80)
    const elapsed = performance.now() - t0
    expect(out.endsWith('…')).toBe(true)
    expect(out).toHaveLength(80 + 1) // exactly limit + ellipsis: the last token is cut to fit
    expect(out.startsWith('[0,1,2,3')).toBe(true)
    // Bounded walk: a full JSON.stringify of 100k elements would be ~590 KB;
    // the loose bound only guards against an accidental O(n) regression.
    expect(elapsed).toBeLessThan(50)
  })

  it('a huge string inside an object is sliced before being escaped', () => {
    const out = previewValue({ blob: 'z'.repeat(1_000_000) }, 80)
    expect(out.startsWith('{"blob":"zzz')).toBe(true)
    expect(out.endsWith('…')).toBe(true)
    expect(out).toHaveLength(80 + 1)
  })

  it('output at exactly the limit is returned whole, without an ellipsis', () => {
    const exact = { k: 'v'.repeat(80 - '{"k":""}'.length) }
    expect(JSON.stringify(exact)).toHaveLength(80)
    expect(previewValue(exact, 80)).toBe(JSON.stringify(exact))
  })

  it('cycles render as [Circular] instead of throwing', () => {
    const o: Record<string, unknown> = { a: 1 }
    o.self = o
    expect(() => previewValue(o, 200)).not.toThrow()
    expect(previewValue(o, 200)).toBe('{"a":1,"self":[Circular]}')
    const arr: unknown[] = [1]
    arr.push(arr)
    expect(previewValue(arr, 200)).toBe('[1,[Circular]]')
  })

  it('a throwing getter yields [unserializable]', () => {
    const bad = { get boom(): number { throw new Error('nope') } }
    expect(previewValue(bad, 200)).toBe('[unserializable]')
    expect(previewValue({ outer: bad }, 200)).toBe('[unserializable]')
  })

  it('nesting deeper than 4 levels collapses to an ellipsis', () => {
    const five = { l1: { l2: { l3: { l4: { l5: 1 } } } } }
    expect(previewValue(five, 200)).toBe('{"l1":{"l2":{"l3":{"l4":…}}}}')
    const four = { l1: { l2: { l3: { l4: 1 } } } }
    expect(previewValue(four, 200)).toBe('{"l1":{"l2":{"l3":{"l4":1}}}}')
    const arr5 = [[[[[1]]]]]
    expect(previewValue(arr5, 200)).toBe('[[[[…]]]]')
  })

  it('toolSummary R10 branch goes through previewValue (bounded)', () => {
    const big = Array.from({ length: 100_000 }, (_, i) => i)
    const out = toolSummary('custom', { list: big, next: 'never reached' }, { known: false })
    expect(out.startsWith('list: [0,1,2')).toBe(true)
    expect(out.length).toBeLessThan(3 * SUMMARY_LIMIT)
  })
})

describe('previewValue: wide objects (codex re-review P2)', () => {
  it('an object with 200 000 keys stops after the budget without enumerating every key', () => {
    const wide: Record<string, number> = {}
    for (let i = 0; i < 200_000; i++) wide[`k${i}`] = i
    const t0 = performance.now()
    const out = previewValue(wide, 80)
    expect(out.length).toBeLessThanOrEqual(81)
    expect(out.startsWith('{"k0":0,"k1":1')).toBe(true)
    expect(performance.now() - t0).toBeLessThan(50)
  })

  it('inherited enumerable keys are skipped', () => {
    const proto = { inherited: 1 }
    const child = Object.create(proto) as Record<string, unknown>
    child.own = 2
    expect(previewValue(child, 80)).toBe('{"own":2}')
  })
})

// spa/src/lib/nex/tool-summary.test.ts
import { describe, it, expect } from 'vitest'
import { getSummary, toolSummary } from './tool-summary'

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

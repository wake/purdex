// spa/src/lib/nex/tool-result-facts.test.ts — P-B3.2 spec §4.4 R4 / R6.
import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import { toolResultFacts, type ToolResultFacts } from './tool-result-facts'

// Stub with the real en strings + the store's `{{n}}` interpolation, so the
// tests lock the shipped copy rather than the key names.
const strings = en as Record<string, string>
const t = (key: string, params?: Record<string, string | number>): string => {
  const value = strings[key]
  if (value === undefined) throw new Error(`missing en key: ${key}`)
  return value.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params?.[k] ?? ''))
}

const output = (o: Partial<NonNullable<ToolResultFacts['output']>>): NonNullable<ToolResultFacts['output']> => ({
  totalLines: 1, totalBytes: 10, truncated: false, hasNonText: false, ...o,
})
const diff = (added: number, removed: number): NonNullable<ToolResultFacts['diff']> => ({
  path: '/x', added, removed, hunks: [], truncated: false,
})

describe('toolResultFacts', () => {
  it('undefined / empty facts → []', () => {
    expect(toolResultFacts(undefined, t)).toEqual([])
    expect(toolResultFacts({}, t)).toEqual([])
    expect(toolResultFacts({ status: 'denied' }, t)).toEqual([])
  })

  it('file.lines → "N lines"', () => {
    expect(toolResultFacts({ file: { path: '/a', lines: 4 } }, t)).toEqual(['4 lines'])
  })

  it('diff → "+N −M" with U+2212 minus', () => {
    const out = toolResultFacts({ diff: diff(1, 1) }, t)
    expect(out).toEqual(['+1 −1'])
    expect(out[0]).toContain('−')
    expect(out[0]).not.toContain('-')
  })

  it('diff with zero counts and hunks: [] still → "+0 −0"', () => {
    expect(toolResultFacts({ diff: diff(0, 0) }, t)).toEqual(['+0 −0'])
  })

  it('file + diff → lines first, then diff', () => {
    expect(toolResultFacts({ file: { path: '/a', lines: 12 }, diff: diff(3, 2) }, t)).toEqual(['12 lines', '+3 −2'])
  })

  it('output.totalLines > 1 alone → "N lines"', () => {
    expect(toolResultFacts({ output: output({ totalLines: 3 }) }, t)).toEqual(['3 lines'])
  })

  it('output.totalLines 1 alone → []', () => {
    expect(toolResultFacts({ output: output({ totalLines: 1 }) }, t)).toEqual([])
    expect(toolResultFacts({ output: output({ totalLines: 0 }) }, t)).toEqual([])
  })

  it('file + output.totalLines → lines only once (file wins)', () => {
    expect(toolResultFacts({ file: { path: '/a', lines: 4 }, output: output({ totalLines: 9 }) }, t)).toEqual(['4 lines'])
  })

  it('diff + output.totalLines → no output line count', () => {
    expect(toolResultFacts({ diff: diff(1, 0), output: output({ totalLines: 9 }) }, t)).toEqual(['+1 −0'])
  })

  it('output.truncated → appends "truncated"', () => {
    expect(toolResultFacts({ output: output({ totalLines: 5, truncated: true }) }, t)).toEqual(['5 lines', 'truncated'])
    expect(toolResultFacts({ output: output({ totalLines: 1, truncated: true }) }, t)).toEqual(['truncated'])
  })

  it('output.hasNonText → appends "non-text"', () => {
    expect(toolResultFacts({ output: output({ totalLines: 1, hasNonText: true }) }, t)).toEqual(['non-text'])
  })

  it('order is file → diff → truncated → non_text', () => {
    expect(
      toolResultFacts(
        { file: { path: '/a', lines: 2 }, diff: diff(1, 1), output: output({ totalLines: 7, truncated: true, hasNonText: true }) },
        t,
      ),
    ).toEqual(['2 lines', '+1 −1', 'truncated', 'non-text'])
  })

  it('uses the i18n keys (not hard-coded copy)', () => {
    const keys: string[] = []
    const spy = (key: string, params?: Record<string, string | number>) => {
      keys.push(key)
      return t(key, params)
    }
    toolResultFacts({ file: { path: '/a', lines: 2 }, output: output({ truncated: true, hasNonText: true }) }, spy)
    expect(keys).toEqual(['execution.tool.lines', 'execution.tool.truncated', 'execution.tool.non_text'])
  })
})

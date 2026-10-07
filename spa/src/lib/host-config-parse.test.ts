import { describe, it, expect, beforeEach, afterEach, vi, type MockInstance } from 'vitest'
import {
  parseCommands,
  parseHostConfig,
  parseProjects,
  parseQuickReplies,
  parseRelay,
  parseResumeTemplates,
} from './host-config-parse'

const H = 'h1'
const P1 = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const P2 = { id: 'p2', name: 'Ploom', slug: 'ploom', path: '/w/ploom' }
const C1 = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
const C2 = { id: 'c2', name: 'Logs', command: 'tail -f log', icon: { kind: 'phosphor', value: 'Rocket' } }
const Q1 = { id: 'q1', text: 'go on' }
const CC = { exact: 'cld --resume {id}', fallback: 'cld -c' }

let warn: MockInstance<typeof console.warn>
beforeEach(() => {
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})
afterEach(() => { warn.mockRestore() })

describe('parseProjects', () => {
  it('a valid collection passes through unchanged, no problem, no warning', () => {
    const p = parseProjects({ items: [P1, P2], revision: 3 }, H)
    expect(p).toEqual({ items: [P1, P2], revision: 3, problem: null })
    expect(p.items[0]).toBe(P1)
    expect(warn).not.toHaveBeenCalled()
  })

  it('items that is not an array reads as empty with a shape problem; the revision is kept', () => {
    expect(parseProjects({ items: {}, revision: 2 }, H)).toEqual({ items: [], revision: 2, problem: { kind: 'shape' } })
    expect(warn).toHaveBeenCalledTimes(1)
    expect(String(warn.mock.calls[0][0])).toMatch(/h1.*projects/)
  })

  it('a malformed row is skipped and counted; the good rows keep their order', () => {
    const rows = [P1, { id: 'x', slug: 'x', path: '/x' }, 'nope', null, { ...P2, path: 7 }, P2]
    const p = parseProjects({ items: rows, revision: 4 }, H)
    expect(p.items).toEqual([P1, P2])
    expect(p.items[1]).toBe(P2)
    expect(p.problem).toEqual({ kind: 'rows', count: 4 })
    // One warning per skipped row, each naming the host, the collection and the row.
    expect(warn).toHaveBeenCalledTimes(4)
    expect(String(warn.mock.calls[0][0])).toMatch(/h1.*projects.*1.*name/)
  })

  it('a missing collection reads as empty with a shape problem', () => {
    expect(parseProjects(undefined, H)).toEqual({ items: [], revision: 0, problem: { kind: 'shape' } })
    expect(parseProjects(null, H)).toEqual({ items: [], revision: 0, problem: { kind: 'shape' } })
    expect(parseProjects([P1], H)).toEqual({ items: [], revision: 0, problem: { kind: 'shape' } })
  })

  it('a revision that is not an integer >= 0 reads as 0', () => {
    for (const revision of [-1, 1.5, '3', null, undefined, Number.NaN]) {
      expect(parseProjects({ items: [P1], revision }, H).revision).toBe(0)
    }
    expect(parseProjects({ items: [P1], revision: 0 }, H).revision).toBe(0)
  })
})

describe('parseCommands', () => {
  it('a valid collection passes through unchanged', () => {
    const p = parseCommands({ items: [C1, C2], revision: 1 }, H)
    expect(p).toEqual({ items: [C1, C2], revision: 1, problem: null })
    expect(p.items[1]).toBe(C2)
  })

  it('a row the list cannot draw is skipped: no icon, a bad icon kind, an unknown agent icon, a missing name', () => {
    const rows = [
      C1,
      { id: 'a', name: 'A', command: 'a' },
      { id: 'b', name: 'B', command: 'b', icon: { kind: 'emoji', value: 'x' } },
      { id: 'c', name: 'C', command: 'c', icon: { kind: 'agent', value: 'nope' } },
      { id: 'd', name: 'D', command: 'd', icon: { kind: 'phosphor', value: 3 } },
      { id: 'e', command: 'e', icon: C1.icon },
      C2,
    ]
    const p = parseCommands({ items: rows, revision: 5 }, H)
    expect(p.items).toEqual([C1, C2])
    expect(p.problem).toEqual({ kind: 'rows', count: 5 })
  })

  it('a wrong container reads as empty with a shape problem', () => {
    expect(parseCommands({ items: 'c1', revision: 1 }, H)).toEqual({ items: [], revision: 1, problem: { kind: 'shape' } })
  })
})

describe('parseQuickReplies', () => {
  it('a valid collection passes through unchanged', () => {
    expect(parseQuickReplies({ items: [Q1], revision: 2 }, H)).toEqual({ items: [Q1], revision: 2, problem: null })
  })

  it('a reply whose text is not a string, or with no id, is skipped and counted', () => {
    const p = parseQuickReplies({ items: [{ id: 'n', text: 42 }, Q1, { text: 'x' }], revision: 2 }, H)
    expect(p.items).toEqual([Q1])
    expect(p.problem).toEqual({ kind: 'rows', count: 2 })
  })

  it('a wrong container reads as empty with a shape problem', () => {
    expect(parseQuickReplies({ items: {}, revision: 2 }, H)).toEqual({ items: [], revision: 2, problem: { kind: 'shape' } })
  })
})

describe('parseResumeTemplates', () => {
  it('a valid map passes through unchanged', () => {
    const p = parseResumeTemplates({ items: { cc: CC }, revision: 1 }, H)
    expect(p).toEqual({ items: { cc: CC }, revision: 1, problem: null })
    expect(p.items.cc).toBe(CC)
  })

  it('an agent whose value is not an exact/fallback pair is dropped and counted', () => {
    const items = { cc: CC, codex: 'oops', opencode: { exact: 5, fallback: 'oc -c' }, gemini: null }
    const p = parseResumeTemplates({ items, revision: 1 }, H)
    expect(p.items).toEqual({ cc: CC })
    expect(p.problem).toEqual({ kind: 'rows', count: 3 })
  })

  it('a map that is not a plain object reads as empty with a shape problem', () => {
    for (const items of [[CC], null, 'cc']) {
      expect(parseResumeTemplates({ items, revision: 1 }, H)).toEqual({ items: {}, revision: 1, problem: { kind: 'shape' } })
    }
    expect(parseResumeTemplates(undefined, H)).toEqual({ items: {}, revision: 0, problem: { kind: 'shape' } })
  })

  it('a stored `__proto__` agent stays an own key, never the prototype', () => {
    const items = JSON.parse('{"__proto__":{"exact":"x {id}","fallback":"x"},"codex":1}') as unknown
    const p = parseResumeTemplates({ items, revision: 1 }, H)
    expect(Object.getPrototypeOf(p.items)).toBe(Object.prototype)
    expect(Object.keys(p.items)).toEqual(['__proto__'])
  })
})

// The daemon's READ path (`relay.go` RelaySwitches → normalizeRelay): a value it
// cannot read is an error there, and the team module refuses self relay (503).
describe('parseRelay', () => {
  it('both switches stored → as stored', () => {
    expect(parseRelay({ items: { self_solo: false, self_lead: true }, revision: 2 }, H))
      .toEqual({ items: { self_solo: false, self_lead: true }, revision: 2, problem: null })
  })

  it('a missing switch defaults to on', () => {
    expect(parseRelay({ items: { self_lead: false }, revision: 2 }, H).items).toEqual({ self_solo: true, self_lead: false })
    expect(parseRelay({ items: {}, revision: 2 }, H).items).toEqual({ self_solo: true, self_lead: true })
  })

  it.each([
    ['an unknown key', { self_solo: true, selfLead: true }],
    ['a null switch', { self_solo: null }],
    ['a non-boolean switch', { self_lead: 'true' }],
    ['an array', [true, true]],
    ['null', null],
  ])('%s fails closed: both off, with a relay problem', (_name, items) => {
    expect(parseRelay({ items, revision: 2 }, H))
      .toEqual({ items: { self_solo: false, self_lead: false }, revision: 2, problem: { kind: 'relay' } })
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('a collection that is not an object fails closed too', () => {
    expect(parseRelay(null, H)).toEqual({ items: { self_solo: false, self_lead: false }, revision: 0, problem: { kind: 'relay' } })
  })
})

describe('parseHostConfig', () => {
  const good = {
    projects: { items: [P1], revision: 3 },
    commands: { items: [C1], revision: 1 },
    resumeTemplates: { items: { cc: CC }, revision: 1 },
    quickReplies: { items: [Q1], revision: 2 },
    relay: { items: { self_solo: true, self_lead: false }, revision: 1 },
  }

  it('one bad collection leaves the others intact', () => {
    const p = parseHostConfig({ ...good, commands: { items: {}, revision: 9 } }, H)
    expect(p.commands).toEqual({ items: [], revision: 9, problem: { kind: 'shape' } })
    expect(p.projects).toEqual({ items: [P1], revision: 3, problem: null })
    expect(p.resumeTemplates.problem).toBeNull()
    expect(p.quickReplies).toEqual({ items: [Q1], revision: 2, problem: null })
    expect(p.relay?.problem).toBeNull()
  })

  it('a missing required collection is empty with a shape problem; a missing optional one is simply absent', () => {
    const p = parseHostConfig({ commands: good.commands }, H)
    expect(p.projects.problem).toEqual({ kind: 'shape' })
    expect(p.resumeTemplates).toEqual({ items: {}, revision: 0, problem: { kind: 'shape' } })
    expect(p.quickReplies).toBeUndefined()
    expect(p.relay).toBeUndefined()
  })

  it('a body that is not a plain object is a load error', () => {
    for (const body of [null, [], 'config', 3]) {
      expect(() => parseHostConfig(body, H)).toThrow(/not a JSON object/)
    }
  })
})

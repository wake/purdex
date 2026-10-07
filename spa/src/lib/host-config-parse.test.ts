import { describe, it, expect, beforeEach, afterEach, vi, type MockInstance } from 'vitest'
import {
  parseCommands,
  parseHostConfig,
  parseProjects,
  parseQuickReplies,
  parseRelay,
  parseResumeTemplates,
} from './host-config-parse'
import { checkRelayPromptBody } from './relay-prompt-check'

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

  // `__proto__` fails the agent-type pattern (it starts with `_`), so it is now dropped like any bad key; what
  // still matters is that reading it never touches the prototype.
  it('a stored `__proto__` agent is dropped and counted, never the prototype', () => {
    const items = JSON.parse('{"__proto__":{"exact":"x {id}","fallback":"x"},"codex":1}') as unknown
    const p = parseResumeTemplates({ items, revision: 1 }, H)
    expect(Object.getPrototypeOf(p.items)).toBe(Object.prototype)
    expect(Object.keys(p.items)).toEqual([])
    expect(p.problem).toEqual({ kind: 'rows', count: 2 })
  })

  // The daemon's PUT (`normalizeResumeTemplates`) refuses the WHOLE map over one of these, and the section shows
  // only the known agents but saves the whole map back: one such entry would make every edit a 400.
  it.each([
    ['a space', 'Bad Agent'],
    ['an upper-case letter', 'Codex'],
    ['a leading dash', '-cc'],
    ['a leading underscore', '_cc'],
    ['33 characters', 'a'.repeat(33)],
    ['nothing', ''],
  ])('an agent key with %s is dropped and counted', (_name, agent) => {
    const p = parseResumeTemplates({ items: { [agent]: CC, cc: CC }, revision: 1 }, H)
    expect(p.items).toEqual({ cc: CC })
    expect(p.problem).toEqual({ kind: 'rows', count: 1 })
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('an agent key the pattern allows is kept', () => {
    const items = { a: CC, '0x': CC, 'my_agent-2': CC, ['a'.repeat(32)]: CC }
    expect(parseResumeTemplates({ items, revision: 1 }, H)).toEqual({ items, revision: 1, problem: null })
  })

  it.each([
    ['exact over 4096 bytes', { exact: 'a'.repeat(4097), fallback: 'x' }],
    ['fallback over 4096 bytes', { exact: 'x {id}', fallback: 'a'.repeat(4097) }],
    ['4096 bytes of three-byte runes plus one byte', { exact: '接'.repeat(1365) + 'ab', fallback: 'x' }],
    ['a NUL in exact', { exact: 'x\0 {id}', fallback: 'x' }],
    ['a NUL in fallback', { exact: 'x {id}', fallback: 'x\0' }],
  ])('a pair with %s is dropped and counted', (_name, pair) => {
    const p = parseResumeTemplates({ items: { codex: pair, cc: CC }, revision: 1 }, H)
    expect(p.items).toEqual({ cc: CC })
    expect(p.problem).toEqual({ kind: 'rows', count: 1 })
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('a pair of exactly 4096 bytes each is kept, multi-byte runes counted as UTF-8 bytes', () => {
    const pair = { exact: 'a'.repeat(4096), fallback: '接'.repeat(1365) + 'a' }
    expect(parseResumeTemplates({ items: { cc: pair }, revision: 1 }, H)).toEqual({ items: { cc: pair }, revision: 1, problem: null })
  })

  it('32 agents are kept; from the 33rd on, in object order, each is dropped and counted', () => {
    const items = Object.fromEntries(Array.from({ length: 34 }, (_, i) => [`agent-${i}`, CC]))
    const p = parseResumeTemplates({ items, revision: 1 }, H)
    expect(Object.keys(p.items)).toEqual(Object.keys(items).slice(0, 32))
    expect(p.problem).toEqual({ kind: 'rows', count: 2 })
    expect(warn).toHaveBeenCalledTimes(2)
  })

  it('the 32-agent cap counts only the agents left after the bad ones are dropped', () => {
    const items = { 'Bad Agent': CC, ...Object.fromEntries(Array.from({ length: 32 }, (_, i) => [`agent-${i}`, CC])) }
    const p = parseResumeTemplates({ items, revision: 1 }, H)
    expect(Object.keys(p.items)).toHaveLength(32)
    expect(p.items['Bad Agent']).toBeUndefined()
    expect(p.problem).toEqual({ kind: 'rows', count: 1 })
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

  // The row also holds the three relay prompt bodies (P9a-1, `relayFields`): known keys, kept as stored so a switch
  // toggle (which PUTs the whole object) never wipes them.
  it('the prompt bodies are known keys, kept as stored', () => {
    const items = { self_solo: false, self_lead: true, prompt_write: 'write it', prompt_fix: '', prompt_seed: '  seed  ' }
    expect(parseRelay({ items, revision: 2 }, H)).toEqual({ items, revision: 2, problem: null })
    expect(warn).not.toHaveBeenCalled()
  })

  // RelaySwitches() decodes only the switches, so a bad stored body never turns self relay off: the switches read,
  // the body is dropped from the SPA's copy and counted — a switch toggle PUTs the whole row, and the daemon's
  // normalizeRelay would refuse a bad body (400), locking the switches; without it the write stores the default.
  it.each([
    ['null', null],
    ['a number', 3],
    ['an object', { text: 'x' }],
    ['over 16 KiB', 'a'.repeat(16 * 1024 + 1)],
    ['16 KiB of three-byte runes plus one byte', '接'.repeat((16 * 1024) / 3) + 'ab'],
    ['holding a control character', 'a\u0007b'],
    ['holding a C1 control character', 'a\u0085b'],
    ['holding the machine tag', 'see [pdx-relay write]'],
    // One check for both callers (#1913): the daemon would store U+FFFD for it, not the text the row holds.
    ['holding an unpaired surrogate', 'a\ud83db'],
  ])('a prompt body that is %s is dropped and counted; the switches still read', (_name, body) => {
    expect(parseRelay({ items: { self_solo: false, prompt_fix: body, prompt_seed: 'kept' }, revision: 2 }, H))
      .toEqual({ items: { self_solo: false, self_lead: true, prompt_seed: 'kept' }, revision: 2, problem: { kind: 'rows', count: 1 } })
    expect(warn).toHaveBeenCalledTimes(1)
  })

  // The daemon's ValidateRelayPromptBody bounds, and its blank rule (TrimSpace → "" = the default) come first.
  it.each([
    ['exactly 16 KiB', 'a'.repeat(16 * 1024)],
    ['newline and tab', 'a\nb\tc'],
    ['a near-miss of the tag', '[pdx relay] pdx-relay'],
    ['blank (Go whitespace only, incl. NEL)', ' \u0085　 '],
    ['an emoji (a surrogate pair)', 'go 😀'],
  ])('a prompt body with %s is kept', (_name, body) => {
    expect(parseRelay({ items: { prompt_write: body }, revision: 2 }, H))
      .toEqual({ items: { self_solo: true, self_lead: true, prompt_write: body }, revision: 2, problem: null })
    expect(warn).not.toHaveBeenCalled()
  })

  // #1913: the parser and the Hosts › 接力 editor answer with one rule set.
  it.each([['fine'], ['a'.repeat(16385)], ['a\u0007b'], ['x [pdx-relay y'], ['a\ude00'], ['ok 😀']])(
    'drops %j exactly when the editor\'s check refuses it', (body) => {
      const kept = parseRelay({ items: { prompt_write: body }, revision: 2 }, H).items.prompt_write === body
      expect(kept).toBe(checkRelayPromptBody(body) === null)
    })
})

// #1889: the daemon's GET (and a PUT's answer, a 409's current) now sends only rows its PUT would take, and says
// what it left out. The SPA shows its notice from those markers and keeps its own checks for an older daemon.
describe("the daemon's markers", () => {
  const off = { self_solo: false, self_lead: false }

  it('`invalid: true` is a shape problem; the items are read as sent (the empty value)', () => {
    expect(parseProjects({ items: [], revision: 3, invalid: true }, H)).toEqual({ items: [], revision: 3, problem: { kind: 'shape' } })
    expect(parseCommands({ items: [], revision: 1, invalid: true }, H).problem).toEqual({ kind: 'shape' })
    expect(parseQuickReplies({ items: [], revision: 1, invalid: true }, H).problem).toEqual({ kind: 'shape' })
    expect(parseResumeTemplates({ items: {}, revision: 2, invalid: true }, H)).toEqual({ items: {}, revision: 2, problem: { kind: 'shape' } })
  })

  it('relay `invalid: true` is a relay problem, with the switches as sent (both off)', () => {
    expect(parseRelay({ items: off, revision: 4, invalid: true }, H)).toEqual({ items: off, revision: 4, problem: { kind: 'relay' } })
  })

  it("`dropped.count` is a rows problem of the daemon's count; the rows sent are kept", () => {
    const dropped = { count: 2, reasons: ['item 1: invalid slug "BAD"', 'item 2: duplicate id "p1"'] }
    expect(parseProjects({ items: [P1, P2], revision: 3, dropped }, H)).toEqual({ items: [P1, P2], revision: 3, problem: { kind: 'rows', count: 2 } })
    expect(parseResumeTemplates({ items: { cc: CC }, revision: 1, dropped: { count: 1, reasons: [] } }, H).problem).toEqual({ kind: 'rows', count: 1 })
    expect(parseRelay({ items: { self_solo: false }, revision: 2, dropped: { count: 1, reasons: [] } }, H))
      .toEqual({ items: { self_solo: false, self_lead: true }, revision: 2, problem: { kind: 'rows', count: 1 } })
    // One line naming the host, the collection, the count and the daemon's reasons.
    expect(String(warn.mock.calls[0][0])).toMatch(/h1.*projects.*2.*invalid slug "BAD"/)
  })

  it("a row the SPA still drops itself adds to the daemon's count", () => {
    expect(parseCommands({ items: [C1, { id: 'x', command: 'x' }], revision: 1, dropped: { count: 2 } }, H).problem)
      .toEqual({ kind: 'rows', count: 3 })
    expect(parseRelay({ items: { prompt_fix: 3 }, revision: 2, dropped: { count: 1 } }, H).problem).toEqual({ kind: 'rows', count: 2 })
  })

  it.each([
    ['`invalid` a string', { invalid: 'true' }],
    ['`invalid` a number', { invalid: 1 }],
    ['`invalid` false', { invalid: false }],
    ['`dropped` not an object', { dropped: 2 }],
    ['`dropped.count` negative', { dropped: { count: -1 } }],
    ['`dropped.count` not an integer', { dropped: { count: 1.5 } }],
    ['`dropped.count` a string', { dropped: { count: '2' } }],
    ['`dropped.count` zero', { dropped: { count: 0 } }],
  ])('a malformed or empty marker is ignored: %s', (_name, marker) => {
    expect(parseProjects({ items: [P1], revision: 3, ...marker }, H)).toEqual({ items: [P1], revision: 3, problem: null })
    expect(parseRelay({ items: off, revision: 3, ...marker }, H)).toEqual({ items: off, revision: 3, problem: null })
    expect(warn).not.toHaveBeenCalled()
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

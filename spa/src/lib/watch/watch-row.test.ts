import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchHostConfig = vi.fn()
vi.mock('../host-config-api', () => ({ fetchHostConfig: (...a: unknown[]) => fetchHostConfig(...a) }))
import { modelOf, parseWatchRow, readWatchRow } from './watch-row'

describe('modelOf', () => {
  it('takes --model <x>', () => expect(modelOf('claude --dangerously-skip-permissions --model opus')).toBe('opus'))
  it('takes --model=<x>', () => expect(modelOf('claude --model=sonnet --foo')).toBe('sonnet'))
  it('takes the first one', () => expect(modelOf('claude --model a --model b')).toBe('a'))
  it('is nothing without one', () => {
    expect(modelOf('claude --dangerously-skip-permissions')).toBeUndefined()
    expect(modelOf('')).toBeUndefined()
  })
  it('is nothing when --model ends the command', () => expect(modelOf('claude --model')).toBeUndefined())
  it('does not take a longer flag that starts with --model', () => expect(modelOf('claude --model-x y')).toBeUndefined())
})

describe('parseWatchRow', () => {
  it('reads agent_command and workdir and keeps every other field for a whole-row write', () => {
    const r = parseWatchRow({ items: { enabled: true, agent_command: 'claude --model opus', workdir: '~/w', future_field: { a: 1 } }, revision: 7 })
    expect(r).toMatchObject({ agentCommand: 'claude --model opus', workdir: '~/w', revision: 7 })
    expect(r?.row).toEqual({ enabled: true, agent_command: 'claude --model opus', workdir: '~/w', future_field: { a: 1 } })
  })
  it('reads the old `model` spelling as the command it stood for', () => {
    const r = parseWatchRow({ items: { model: 'opus', workdir: '~/w' }, revision: 1 })
    expect(r?.agentCommand).toBe('claude --dangerously-skip-permissions --model opus')
  })
  it('is null for an unreadable field', () => {
    expect(parseWatchRow(undefined)).toBeNull()
    expect(parseWatchRow({ items: 'x', revision: 1 })).toBeNull()
    expect(parseWatchRow({ items: {} })).toBeNull()
  })
  it('missing strings read as empty', () => {
    expect(parseWatchRow({ items: {}, revision: 0 })).toMatchObject({ agentCommand: '', workdir: '' })
  })
})

describe('readWatchRow', () => {
  beforeEach(() => fetchHostConfig.mockReset())
  it('reads the watch field of /api/hostconfig', async () => {
    fetchHostConfig.mockResolvedValue({ watch: { items: { agent_command: 'c', workdir: 'w' }, revision: 3 }, projects: {} })
    expect(await readWatchRow('h1')).toMatchObject({ agentCommand: 'c', workdir: 'w', revision: 3 })
  })
  it('is null on a failure or a body without the field', async () => {
    fetchHostConfig.mockRejectedValueOnce(new Error('x'))
    expect(await readWatchRow('h1')).toBeNull()
    fetchHostConfig.mockResolvedValueOnce({})
    expect(await readWatchRow('h1')).toBeNull()
  })
})

import { describe, it, expect, vi, afterEach } from 'vitest'
import { parseLaunchPlan, postLaunch, fetchLaunchPlan, previewLine, LaunchError } from './launch-plan'
import { useHostStore } from '../stores/useHostStore'

const full = {
  agent_running: false, tmux_instance: '1:2',
  dir: { path: '/w/p', state: 'move' },
  resume: { line: 'claude --resume x' }, fresh: { line: 'claude' }, busy: null,
}

describe('parseLaunchPlan', () => {
  it('reads a full plan', () => {
    expect(parseLaunchPlan(full)).toEqual({
      agentRunning: false, tmuxInstance: '1:2', dir: { path: '/w/p', state: 'move' },
      resume: { line: 'claude --resume x' }, fresh: { line: 'claude' }, busy: null,
    })
  })
  it('a missing / bad field is unavailable, not a guess', () => {
    const p = parseLaunchPlan({ agent_running: false, dir: { path: 5, state: 'move' }, resume: { line: null, reason: 'weird' }, fresh: 3 })!
    expect(p.dir).toBeNull()
    expect(p.resume).toEqual({ line: null, reason: 'no_record' })
    expect(p.fresh).toBeNull()
    expect(p.busy).toBeNull() // a missing busy is not a busy pane
  })
  it('keeps each resume reason', () => {
    for (const reason of ['no_record', 'no_template', 'empty_command'] as const) {
      expect(parseLaunchPlan({ ...full, resume: { line: null, reason } })!.resume).toEqual({ line: null, reason })
    }
  })
  it('refuses a body without a boolean agent_running', () => {
    expect(parseLaunchPlan({})).toBeNull()
    expect(parseLaunchPlan(null)).toBeNull()
    expect(parseLaunchPlan({ agent_running: 'no' })).toBeNull()
  })
})

describe('parseLaunchPlan busy', () => {
  it('null and missing are not busy', () => {
    expect(parseLaunchPlan({ ...full, busy: null })!.busy).toBeNull()
    const { busy: _b, ...noBusy } = full
    expect(parseLaunchPlan(noBusy)!.busy).toBeNull()
  })
  it('an object with a non-empty command is busy', () => {
    expect(parseLaunchPlan({ ...full, busy: { command: 'npm' } })!.busy).toEqual({ command: 'npm' })
  })
  it.each([['a string', 'npm'], ['a number', 3], ['an empty object', {}], ['an empty command', { command: '' }], ['a non-string command', { command: 3 }], ['an array', ['npm']]])(
    'busy as %s makes the whole plan unusable', (_n, busy) => {
      expect(parseLaunchPlan({ ...full, busy })).toBeNull()
    })
})

describe('previewLine', () => {
  it('single-quotes the path, escaping each single quote as \'\\\'\'', () => {
    const d = (path: string) => ({ path, state: 'move' as const })
    expect(previewLine('claude', d('/a'), true)).toBe("cd '/a' && claude")
    expect(previewLine('claude', d('/a b/c'), true)).toBe("cd '/a b/c' && claude")
    expect(previewLine('claude', d("/a b/it's"), true)).toBe("cd '/a b/it'\\''s' && claude")
    expect(previewLine('claude', d('/x/$HOME;rm&y'), true)).toBe("cd '/x/$HOME;rm&y' && claude")
  })
  it('prefixes cd only when the box is on and the directory is a move', () => {
    expect(previewLine('claude', { path: '/a', state: 'move' }, true)).toBe("cd '/a' && claude")
    expect(previewLine('claude', { path: '/a', state: 'move' }, false)).toBe('claude')
    expect(previewLine('claude', { path: '/a', state: 'here' }, true)).toBe('claude')
    expect(previewLine(null, null, true)).toBe('')
  })
})

describe('calls', () => {
  afterEach(() => vi.restoreAllMocks())
  const setup = () => useHostStore.setState({
    hosts: { h: { id: 'h', name: 'm', ip: '1.2.3.4', port: 7860, order: 0 } }, hostOrder: ['h'], activeHostId: 'h', runtime: {},
  })
  const body = { kind: 'fresh' as const, cd: false, expectedTmuxInstance: 'i' }
  it('GETs the plan and POSTs the choice with the generation', async () => {
    setup()
    const f = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify(full), { status: 200 }))
      .mockResolvedValueOnce(new Response('{"line":"claude"}', { status: 200 }))
    await expect(fetchLaunchPlan('h', 'abc')).resolves.toMatchObject({ agentRunning: false })
    expect(String(f.mock.calls[0][0])).toContain('/api/sessions/abc/launch-plan')
    await postLaunch('h', 'abc', { kind: 'fresh', cd: true, expectedTmuxInstance: '1:2' })
    expect(String(f.mock.calls[1][0])).toContain('/api/sessions/abc/launch')
    expect(JSON.parse(String((f.mock.calls[1][1] as RequestInit).body))).toEqual({ kind: 'fresh', cd: true, expected_tmux_instance: '1:2' })
  })
  it('maps a 409 body code, a 404, and anything else', async () => {
    setup()
    const f = vi.spyOn(globalThis, 'fetch')
    f.mockResolvedValueOnce(new Response('{"code":"busy"}', { status: 409, statusText: 'Conflict' }))
    await expect(postLaunch('h', 'a', body)).rejects.toMatchObject({ code: 'busy', status: 409 })
    f.mockResolvedValueOnce(new Response('', { status: 404 }))
    await expect(postLaunch('h', 'a', body)).rejects.toMatchObject({ code: 'not_found' })
    f.mockResolvedValueOnce(new Response('nope', { status: 500 }))
    const e = await postLaunch('h', 'a', body).catch((x) => x)
    expect(e).toBeInstanceOf(LaunchError)
    expect(e).toMatchObject({ code: 'other', status: 500 })
    f.mockResolvedValueOnce(new Response('{"code":"surprise"}', { status: 409 }))
    await expect(postLaunch('h', 'a', body)).rejects.toMatchObject({ code: 'other', status: 409 })
  })
})

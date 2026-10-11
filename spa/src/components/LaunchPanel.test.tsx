// NA-1: the no-agent launch panel renders the daemon's plan and posts a choice. Fixtures stand in for the daemon (NA-0b).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { LaunchError, type LaunchPlan } from '../lib/launch-plan'
import { LaunchPanel } from './LaunchPanel'
import { resetLaunchLatch } from '../lib/launch-latch'

const post = vi.hoisted(() => vi.fn())
vi.mock('../lib/launch-plan', async (orig) => ({ ...(await orig<typeof import('../lib/launch-plan')>()), postLaunch: post }))

const plan = (over: Partial<LaunchPlan> = {}): LaunchPlan => ({
  agentRunning: false, tmuxInstance: '1:2',
  dir: { path: '/w/proj', state: 'move' },
  resume: { line: 'claude --resume abc' }, fresh: { line: 'claude' }, busy: null, ...over,
})
const onRefresh = vi.fn()
const onSwitchToTerminal = vi.fn()
const mount = (p: LaunchPlan, extra: { branch?: string; tmuxInstance?: string } = {}) =>
  render(<LaunchPanel hostId="h" sessionCode="abc" tmuxInstance={extra.tmuxInstance ?? '1:2'} branch={extra.branch} plan={p} onRefresh={onRefresh} onSwitchToTerminal={onSwitchToTerminal} />)

beforeEach(() => { cleanup(); resetLaunchLatch(); post.mockReset(); onRefresh.mockReset(); onSwitchToTerminal.mockReset() })

describe('rows', () => {
  it('in tmux is a ticked, disabled box', () => {
    mount(plan())
    const box = screen.getByTestId('launch-in-tmux') as HTMLInputElement
    expect(box.checked).toBe(true)
    expect(box.disabled).toBe(true)
  })
  it('move: box on by default, path and branch shown', () => {
    mount(plan(), { branch: 'main' })
    const box = screen.getByTestId('launch-cd') as HTMLInputElement
    expect(box.checked).toBe(true)
    expect(box.disabled).toBe(false)
    expect(screen.getByTestId('launch-dir').textContent).toContain('/w/proj')
    expect(screen.getByTestId('launch-dir').textContent).toContain('main')
  })
  it('here: box disabled with 「already here」', () => {
    mount(plan({ dir: { path: '/w/proj', state: 'here' } }))
    expect((screen.getByTestId('launch-cd') as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByTestId('launch-cd-row').textContent).toContain('Already in this directory')
  })
  it('no dir: the row is hidden', () => {
    mount(plan({ dir: null }))
    expect(screen.queryByTestId('launch-cd')).toBeNull()
  })
  it('both buttons enabled when the plan has lines', () => {
    mount(plan())
    expect((screen.getByTestId('launch-resume') as HTMLButtonElement).disabled).toBe(false)
    expect((screen.getByTestId('launch-fresh') as HTMLButtonElement).disabled).toBe(false)
  })
  it.each([
    ['no_record', 'No record of a previous conversation'],
    ['no_template', 'This agent has no resume command'],
    ['empty_command', 'The resume command is empty'],
  ] as const)('resume reason %s disables the button and says why', (reason, text) => {
    mount(plan({ resume: { line: null, reason } }))
    expect((screen.getByTestId('launch-resume') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('launch-resume-hint').textContent).toContain(text)
    expect((screen.getByTestId('launch-fresh') as HTMLButtonElement).disabled).toBe(false)
  })
})

describe('tooltips preview the line', () => {
  it('with the cd prefix while the box is on, without it once off', () => {
    mount(plan())
    expect(screen.getByTestId('launch-resume').getAttribute('title')).toBe("cd '/w/proj' && claude --resume abc")
    expect(screen.getByTestId('launch-fresh').getAttribute('title')).toBe("cd '/w/proj' && claude")
    fireEvent.click(screen.getByTestId('launch-cd'))
    expect(screen.getByTestId('launch-resume').getAttribute('title')).toBe('claude --resume abc')
  })
})

describe('busy', () => {
  it('disables both buttons, names the command and emphasises the terminal link', () => {
    mount(plan({ busy: { command: 'npm' } }))
    expect((screen.getByTestId('launch-resume') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('launch-fresh') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('launch-busy').textContent).toContain('npm')
    expect(screen.getByTestId('launch-to-terminal').getAttribute('data-emphasis')).toBe('true')
  })
  it('the terminal link is not emphasised otherwise', () => {
    mount(plan())
    expect(screen.getByTestId('launch-to-terminal').getAttribute('data-emphasis')).toBe('false')
  })
})

describe('actions', () => {
  it('to-terminal and refresh call their handlers; the panel never switches by itself', () => {
    mount(plan())
    expect(onSwitchToTerminal).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('launch-to-terminal'))
    expect(onSwitchToTerminal).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('launch-refresh'))
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })
  it('posts the choice with the session generation and the box state', async () => {
    post.mockResolvedValue(undefined)
    mount(plan())
    fireEvent.click(screen.getByTestId('launch-cd')) // off
    fireEvent.click(screen.getByTestId('launch-resume'))
    await waitFor(() => expect(post).toHaveBeenCalledWith('h', 'abc', { kind: 'resume', cd: false, expectedTmuxInstance: '1:2' }))
    expect(screen.queryByTestId('launch-error')).toBeNull() // success: no message
  })
  it('the cd box is sent as on for a move and as off for here', async () => {
    post.mockResolvedValue(undefined)
    mount(plan({ dir: { path: '/w/proj', state: 'here' } }))
    fireEvent.click(screen.getByTestId('launch-fresh'))
    await waitFor(() => expect(post).toHaveBeenCalledWith('h', 'abc', { kind: 'fresh', cd: false, expectedTmuxInstance: '1:2' }))
  })
  it('falls back to the plan generation when the pane has none', async () => {
    post.mockResolvedValue(undefined)
    mount(plan(), { tmuxInstance: '' })
    fireEvent.click(screen.getByTestId('launch-fresh'))
    await waitFor(() => expect(post).toHaveBeenCalledWith('h', 'abc', expect.objectContaining({ expectedTmuxInstance: '1:2' })))
  })
  it('both buttons are disabled while the request is in flight', async () => {
    let done: () => void = () => {}
    post.mockReturnValue(new Promise<void>((r) => { done = r }))
    mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    await waitFor(() => expect((screen.getByTestId('launch-resume') as HTMLButtonElement).disabled).toBe(true))
    expect((screen.getByTestId('launch-fresh') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByTestId('launch-resume'))
    expect(post).toHaveBeenCalledTimes(1)
    await act(async () => { done() })
    // success: still latched (until the claim lifetime ends), and no message
    expect((screen.getByTestId('launch-fresh') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.queryByTestId('launch-error')).toBeNull()
  })
})

describe('launch latch', () => {
  const btns = () => [screen.getByTestId('launch-resume'), screen.getByTestId('launch-fresh')] as HTMLButtonElement[]
  const flush = () => act(async () => { await Promise.resolve() })

  it('success then click again: exactly one POST; enabled again after 15 s', async () => {
    vi.useFakeTimers()
    try {
      post.mockResolvedValue(undefined)
      mount(plan())
      fireEvent.click(screen.getByTestId('launch-fresh'))
      await flush()
      expect(btns().every((b) => b.disabled)).toBe(true)
      fireEvent.click(screen.getByTestId('launch-fresh'))
      fireEvent.click(screen.getByTestId('launch-resume'))
      expect(post).toHaveBeenCalledTimes(1)
      act(() => { vi.advanceTimersByTime(14_900) })
      expect(btns().every((b) => b.disabled)).toBe(true)
      act(() => { vi.advanceTimersByTime(200) })
      expect(btns().every((b) => !b.disabled)).toBe(true)
    } finally { vi.useRealTimers() }
  })
  it('a failed POST frees the buttons at once and shows the error', async () => {
    post.mockRejectedValue(new LaunchError(500, 'other'))
    mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    await screen.findByTestId('launch-error')
    expect(btns().every((b) => !b.disabled)).toBe(true)
  })
  it('an unknown outcome (fetch rejected) keeps the latch for 15 s from the error, then frees it', async () => {
    vi.useFakeTimers()
    try {
      post.mockRejectedValue(new TypeError('Failed to fetch'))
      mount(plan())
      fireEvent.click(screen.getByTestId('launch-fresh'))
      await flush()
      expect(screen.getByTestId('launch-error').textContent).toContain('Not sure whether it was sent')
      expect(onRefresh).toHaveBeenCalledTimes(1)
      expect(btns().every((b) => b.disabled)).toBe(true)
      fireEvent.click(screen.getByTestId('launch-fresh'))
      fireEvent.click(screen.getByTestId('launch-resume'))
      expect(post).toHaveBeenCalledTimes(1)
      act(() => { vi.advanceTimersByTime(14_900) })
      expect(btns().every((b) => b.disabled)).toBe(true)
      act(() => { vi.advanceTimersByTime(200) })
      expect(btns().every((b) => !b.disabled)).toBe(true)
    } finally { vi.useRealTimers() }
  })
  it('a panel mounted during an in-flight POST (tab switch) starts disabled, and follows its outcome', async () => {
    let done: () => void = () => {}
    post.mockReturnValue(new Promise<void>((r) => { done = r }))
    const first = mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    first.unmount()
    mount(plan())
    expect(btns().every((b) => b.disabled)).toBe(true)
    await act(async () => { done() })
    expect(btns().every((b) => b.disabled)).toBe(true) // settled: still latched for the claim lifetime
  })
  it('an in-flight POST that fails frees a panel mounted meanwhile', async () => {
    let fail: (e: unknown) => void = () => {}
    post.mockReturnValue(new Promise<void>((_, rej) => { fail = rej }))
    const first = mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    first.unmount()
    mount(plan())
    await act(async () => { fail(new LaunchError(500, 'other')) })
    expect(btns().every((b) => !b.disabled)).toBe(true)
  })
  it('a different tmux instance is not latched', async () => {
    post.mockReturnValue(new Promise<void>(() => {}))
    const first = mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    first.unmount()
    mount(plan({ tmuxInstance: '1:9' }), { tmuxInstance: '1:9' })
    expect(btns().every((b) => !b.disabled)).toBe(true)
  })
})

describe('errors by code', () => {
  const fail = async (err: LaunchError) => {
    post.mockRejectedValue(err)
    mount(plan({ busy: null }))
    fireEvent.click(screen.getByTestId('launch-fresh'))
    return screen.findByTestId('launch-error')
  }
  it('generation_conflict', async () => {
    const el = await fail(new LaunchError(409, 'generation_conflict'))
    expect(el.textContent).toContain('This session has been replaced')
    expect(onRefresh).not.toHaveBeenCalled()
  })
  it('busy: the busy line, and the plan is re-read', async () => {
    const el = await fail(new LaunchError(409, 'busy'))
    expect(el.getAttribute('data-code')).toBe('busy')
    expect(el.textContent).toContain('in the terminal first')
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })
  it('already_launching', async () => {
    const el = await fail(new LaunchError(409, 'already_launching'))
    expect(el.textContent).toContain('Already launching')
  })
  it('agent_running: the plan is re-read (the panel goes away)', async () => {
    post.mockRejectedValue(new LaunchError(409, 'agent_running'))
    mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    await waitFor(() => expect(onRefresh).toHaveBeenCalledTimes(1))
    expect(screen.queryByTestId('launch-error')).toBeNull()
  })
  it('404', async () => {
    const el = await fail(new LaunchError(404, 'not_found'))
    expect(el.textContent).toContain('no longer exists')
  })
  it('anything else shows the status number, never a status text', async () => {
    const err = new LaunchError(502, 'other')
    err.message = 'Bad Gateway'
    const el = await fail(err)
    expect(el.textContent).toContain('Launch failed (502)')
    expect(el.textContent).not.toContain('Bad Gateway')
  })
  it('a transport failure is an unknown outcome: says so, and the plan is re-read', async () => {
    post.mockRejectedValue(new TypeError('Failed to fetch'))
    mount(plan())
    fireEvent.click(screen.getByTestId('launch-fresh'))
    const el = await screen.findByTestId('launch-error')
    expect(el.textContent).toContain('Not sure whether it was sent')
    expect(el.textContent).not.toContain('Failed to fetch')
    expect(el.textContent).not.toContain('Launch failed')
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })
  it('a coded other error keeps 「Launch failed (status)」 and does not re-read', async () => {
    const el = await fail(new LaunchError(500, 'other'))
    expect(el.textContent).toContain('Launch failed (500)')
    expect(onRefresh).not.toHaveBeenCalled()
  })
})

describe('no generation to send', () => {
  it('both empty: buttons disabled, a refresh hint is shown, nothing is posted', () => {
    mount(plan({ tmuxInstance: '' }), { tmuxInstance: '' })
    const resume = screen.getByTestId('launch-resume') as HTMLButtonElement
    const fresh = screen.getByTestId('launch-fresh') as HTMLButtonElement
    expect(resume.disabled).toBe(true)
    expect(fresh.disabled).toBe(true)
    expect(screen.getByTestId('launch-no-generation').textContent).toContain('refresh')
    fireEvent.click(resume)
    fireEvent.click(fresh)
    expect(post).not.toHaveBeenCalled()
  })
})

describe('layout', () => {
  it('is one centred block whose rows are left-aligned', () => {
    mount(plan({ busy: { command: 'vim' } }), { branch: 'main' })
    const panel = screen.getByTestId('launch-panel')
    expect(panel.className).toContain('items-center')
    expect(panel.className).toContain('justify-center')
    const block = screen.getByTestId('launch-block')
    expect(block.parentElement).toBe(panel)
    expect(panel.children).toHaveLength(1)
    expect(block.className).toContain('items-start')
    expect(block.className).toContain('text-left')
    for (const id of ['launch-in-tmux', 'launch-cd-row', 'launch-resume', 'launch-busy', 'launch-refresh']) {
      let el: HTMLElement | null = screen.getByTestId(id)
      while (el && el.parentElement !== block) el = el.parentElement
      expect(el, id).not.toBeNull()
    }
    expect(screen.getByTestId('launch-dir').className).toContain('truncate')
    expect(screen.getByTestId('launch-dir').className).toContain('min-w-0')
  })
})

describe('the cd choice belongs to the plan it was made for', () => {
  const el = (p: LaunchPlan) => (
    <LaunchPanel hostId="h" sessionCode="abc" tmuxInstance="1:2" plan={p} onRefresh={onRefresh} onSwitchToTerminal={onSwitchToTerminal} />
  )
  const box = () => screen.getByTestId('launch-cd') as HTMLInputElement
  const A = plan({ dir: { path: '/repo-a', state: 'move' } })
  const hereA = plan({ dir: { path: '/repo-a', state: 'here' } })
  const B = plan({ dir: { path: '/repo-b', state: 'move' } })

  it('move A unchecked -> here A -> move A: checked again, and the POST carries cd:true', async () => {
    post.mockResolvedValue(undefined)
    const { rerender } = render(el(A))
    fireEvent.click(box())
    expect(box().checked).toBe(false)
    rerender(el(hereA))
    rerender(el(A))
    expect(box().checked).toBe(true)
    fireEvent.click(screen.getByTestId('launch-fresh'))
    await waitFor(() => expect(post).toHaveBeenCalledWith('h', 'abc', { kind: 'fresh', cd: true, expectedTmuxInstance: '1:2' }))
  })
  it('move A unchecked -> move B: checked', () => {
    const { rerender } = render(el(A))
    fireEvent.click(box())
    rerender(el(B))
    expect(box().checked).toBe(true)
  })
  it('a different generation resets it too', () => {
    const { rerender } = render(el(A))
    fireEvent.click(box())
    rerender(el({ ...A, tmuxInstance: '1:3' }))
    expect(box().checked).toBe(true)
  })
  it('the same plan read again keeps the unchecked choice', () => {
    const { rerender } = render(el(A))
    fireEvent.click(box())
    rerender(el({ ...A, dir: { ...A.dir! } }))
    expect(box().checked).toBe(false)
  })
})

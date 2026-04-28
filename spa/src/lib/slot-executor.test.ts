import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { runWorkspaceSlot } from './slot-executor'
import { useUndoToast } from '../stores/useUndoToast'

vi.mock('./host-api', async () => {
  const actual = await vi.importActual<typeof import('./host-api')>('./host-api')
  return {
    ...actual,
    createSession: vi.fn(),
  }
})

vi.mock('./execute-command', () => ({
  executeCommand: vi.fn(),
}))

import { createSession } from './host-api'
import { executeCommand } from './execute-command'

describe('runWorkspaceSlot', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useUndoToast.setState({ toast: null })
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('happy path — creates session, sends keys, switches focus', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn() // not called when ctx.hostId is non-null
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1', cwd: '/tmp' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    expect(resolveHostId).not.toHaveBeenCalled()
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '/tmp', 'terminal')
    expect(executeCommand).toHaveBeenCalledWith('h1', 'sess-1', 'echo hi')
    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-1')
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('hostId null → invokes resolveHostId; user picks → continues with that hostId', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn().mockResolvedValue('h-picked')
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-2',
      name: 'A',
      cwd: '~',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: null, workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    expect(resolveHostId).toHaveBeenCalledTimes(1)
    expect(createSession).toHaveBeenCalledWith('h-picked', expect.any(String), '~', 'terminal')
    expect(executeCommand).toHaveBeenCalledWith('h-picked', 'sess-2', 'echo hi')
    expect(switchFocus).toHaveBeenCalledWith('h-picked', 'sess-2')
  })

  it('hostId null → user cancels picker → no API call, no toast', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn().mockResolvedValue(null)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: null, workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    expect(resolveHostId).toHaveBeenCalledTimes(1)
    expect(createSession).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    expect(switchFocus).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('createSession failure — toast WITHOUT action button (codex round-1 B4)', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('500 Internal'))

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    expect(switchFocus).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Failed to start session/i)
    // codex round-1 B4 — no action button on create-session failure
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
  })

  it('send-keys failure — STILL switches focus + toast carries Retry action', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error('send-keys failed: 503'),
    )

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-1')
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Session created.*command failed/i)
    // codex round-1 B4 — send-keys failure DOES carry an action (retry)
    expect(toast!.action).toBeTypeOf('function')
    expect(toast!.actionLabel).toBeDefined()
    expect(toast!.actionLabel).toMatch(/retry/i)
    // action = retry — calling it should trigger executeCommand again
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined)
    toast!.action!()
    // retry is sync invocation; await microtask
    await Promise.resolve()
    expect(executeCommand).toHaveBeenCalledTimes(2)
  })

  it('switchToSession failure — toast WITHOUT action button (codex round-1 B4)', async () => {
    const switchFocus = vi.fn().mockImplementation(() => {
      throw new Error('switch failed')
    })
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/could not switch/i)
    // codex round-1 B4 — no action button on switch failure
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
  })

  // codex round-4 — workspace liveness gate. If the surrounding context is
  // destroyed while createSession is in flight, executeCommand MUST NOT run
  // (destructive commands like `rm` would otherwise still ship to the
  // orphaned session). switch_failed toast surfaces; no retry.
  it('assertContextLive returning false after createSession aborts before executeCommand', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    const assertContextLive = vi.fn().mockReturnValue(false)
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'rm -rf /' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive },
    )

    expect(createSession).toHaveBeenCalledTimes(1) // session was created server-side
    expect(assertContextLive).toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled() // critical: command did NOT ship
    expect(switchFocus).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined() // no retry — context is gone
  })

  it('assertContextLive returning true allows the full flow (negative control)', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    const assertContextLive = vi.fn().mockReturnValue(true)
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive },
    )

    expect(assertContextLive).toHaveBeenCalled()
    expect(executeCommand).toHaveBeenCalled()
    expect(switchFocus).toHaveBeenCalled()
  })

  // codex round-2 — combined send-keys + switch failure: switch failure takes
  // precedence over send-keys retry. Otherwise the user sees a Retry button
  // for a session they cannot navigate to (orphan session, retry impotent).
  it('send-keys AND switch BOTH fail — surfaces switch_failed (no retry action), not send_keys_failed retry', async () => {
    const switchFocus = vi.fn().mockImplementation(() => {
      throw new Error('switch failed')
    })
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error('send-keys failed'),
    )

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive: () => true },
    )

    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    // Precedence: switch failure wins
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.message).not.toMatch(/command failed/i)
    // No retry — retry would re-send to a session the user cannot see
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
  })

  // #690 / spec §3.3.1 — type-level enforcement: Deps.assertContextLive is required.
  // Future workspace-context callers (e.g. Phase 1b' Plus hover popover) cannot
  // silently regress the round-4 destructive-command guard by omitting the probe.
  // If we ever add `assertContextLive` to the literal below (or revert the type
  // to optional), this @ts-expect-error directive becomes "unused" and TS will
  // fail the build — surfacing the regression loudly.
  it('requires assertContextLive at type level (#690 / spec §3.3.1)', () => {
    // @ts-expect-error - assertContextLive is required (#690)
    const deps: Parameters<typeof runWorkspaceSlot>[2] = {
      switchToSession: () => {},
      resolveHostId: async () => null,
    }
    expect(deps).toBeDefined()
  })
})

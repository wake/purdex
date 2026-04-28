import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { runWorkspaceSlot, runHostSlot, type HostSlotContext } from './slot-executor'
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

  // #690 round-2 A2 — defense in depth: probe that throws should fail-closed
  // (no executeCommand, no switch, switch_failed toast, no retry). Type-level
  // required guarantees probe shape under normal use, but cast-bypassed or
  // genuinely buggy probes must not let destructive commands ship.
  it('assertContextLive throwing fails closed — no executeCommand, switch_failed toast', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    const assertContextLive = vi.fn().mockImplementation(() => {
      throw new Error('probe blew up')
    })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'rm -rf /' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId, assertContextLive },
    )

    expect(createSession).toHaveBeenCalledTimes(1)
    expect(executeCommand).not.toHaveBeenCalled()
    expect(switchFocus).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined()
  })

  // #690 round-2 A2 — cast-bypassed probe (non-function value) must also fail
  // closed. Type-level required catches honest callers but `as any` / spread
  // tricks can still slip through; the runtime `typeof === 'function'` check
  // is the second line of defense.
  it('assertContextLive that is not a function fails closed (cast-bypass simulation)', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan',
      name: 'A',
      cwd: '/tmp',
      mode: 'terminal',
    })

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'rm -rf /' },
      { hostId: 'h1', workspaceId: 'w1' },
      // Simulate a cast-bypass (`Object.assign(base, { assertContextLive: undefined } as any)`)
      // by passing a non-function value through the type system.
      {
        switchToSession: switchFocus,
        resolveHostId,
        assertContextLive: undefined as unknown as () => boolean,
      },
    )

    expect(executeCommand).not.toHaveBeenCalled()
    expect(switchFocus).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined()
  })

  // #690 round-2 A1 / F1 — type-level invariants for spec §3.3.1.
  //
  // IMPORTANT: This test's pass/fail is determined by the TypeScript compiler
  // (`tsc -b`, run via `pnpm run build`), NOT by the Vitest runtime. Vitest
  // does not type-check by default; `pnpm vitest run lib/slot-executor` will
  // report this as passing even if the type contract has regressed. The
  // protection only fires during `tsc -b` — see `pnpm run build`.
  //
  // This guards against three regressions that an `@ts-expect-error` directive
  // would miss (round-1 directive could be silently consumed by an unrelated
  // missing field if `Deps` ever grows):
  //  1. The third parameter of `runWorkspaceSlot` collapses to `any`.
  //  2. `Deps.assertContextLive` reverts to optional.
  //  3. `WorkspaceSlotContext.workspaceId` reverts to optional / nullable
  //     (round-2 D1 — Phase 1c HOST_ACTIONS must NOT be allowed to share this
  //     entry by passing `{ hostId }` alone with a dummy `() => true` probe).
  //
  // If any invariant breaks, the `: true` assignments below become TS errors.
  it('type-level invariants verified by tsc -b — Deps required, ctx requires workspaceId (#690 / spec §3.3.1)', () => {
    type Deps = Parameters<typeof runWorkspaceSlot>[2]
    type Ctx = Parameters<typeof runWorkspaceSlot>[1]

    // IsAny<T> trick: `0 extends 1 & T` is true only when T is `any`.
    type IsAny<T> = 0 extends 1 & T ? true : false
    type DepsIsNotAny = IsAny<Deps> extends true ? false : true

    // {} extends Pick<T, K> is true only when K is optional in T; we require
    // it to be false → key is required.
    type AssertContextLiveRequired = object extends Pick<Deps, 'assertContextLive'> ? false : true
    type WorkspaceIdRequired = object extends Pick<Ctx, 'workspaceId'> ? false : true

    // null assignability check — ctx.workspaceId must reject null/undefined.
    type WorkspaceIdRejectsNull = null extends Ctx['workspaceId'] ? false : true

    const _depsNotAny: DepsIsNotAny = true
    const _assertRequired: AssertContextLiveRequired = true
    const _workspaceIdRequired: WorkspaceIdRequired = true
    const _workspaceIdRejectsNull: WorkspaceIdRejectsNull = true

    expect(_depsNotAny && _assertRequired && _workspaceIdRequired && _workspaceIdRejectsNull).toBe(true)
  })
})

describe('runHostSlot', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useUndoToast.setState({ toast: null })
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  // Default deps factory — every test threads through assertHostLive so a
  // forgotten parameter shows up immediately as a TS error rather than a
  // silently-bypassed probe (mirrors the runWorkspaceSlot suite pattern).
  const live = (): boolean => true

  it('happy path — creates session with host default cwd, asserts host live, sends keys, switches focus', async () => {
    const switchFocus = vi.fn()
    const assertHostLive = vi.fn().mockReturnValue(true)
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h',
      name: 'A',
      cwd: '~',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive },
    )

    // host default cwd ('~') applied when ctx.cwd is undefined
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '~', 'terminal')
    expect(assertHostLive).toHaveBeenCalledWith('h1')
    expect(executeCommand).toHaveBeenCalledWith('h1', 'sess-h', 'echo h')
    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-h')
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('respects ctx.cwd override when provided', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '/srv', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1', cwd: '/srv' },
      { switchToSession: switchFocus, assertHostLive: live },
    )
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '/srv', 'terminal')
  })

  it('createSession failure — toast WITHOUT action button', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('500'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive: live },
    )

    expect(switchFocus).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Failed to start session/i)
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
  })

  // Finding 6 — empty / whitespace sessionCode treated as create failure.
  // Otherwise downstream executeCommand / switchToSession get a code that
  // resolves to 404 and surfaces a confusing send-keys / switch error.
  it('createSession returns blank session.code — treated as create failure (Finding 6)', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: '   ', name: 'A', cwd: '~', mode: 'terminal',
    })

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive: live },
    )

    expect(executeCommand).not.toHaveBeenCalled()
    expect(switchFocus).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/Failed to start session/i)
    expect(toast!.action).toBeUndefined()
  })

  // Finding 1 (high) — host liveness guard. createSession resolves; user
  // deletes the host in Settings before executeCommand fires; assertHostLive
  // returns false → fail closed, no executeCommand, no switchToSession,
  // switch_failed toast (no retry — retry can't bring host back).
  it('assertHostLive returning false after createSession aborts before executeCommand (Finding 1)', async () => {
    const switchFocus = vi.fn()
    const assertHostLive = vi.fn().mockReturnValue(false)
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'rm -rf /' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive },
    )

    expect(createSession).toHaveBeenCalledTimes(1)
    expect(assertHostLive).toHaveBeenCalledWith('h1')
    expect(executeCommand).not.toHaveBeenCalled() // critical: command did NOT ship to fallback host
    expect(switchFocus).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined()
  })

  // Finding 1 — defense in depth: probe that throws should fail-closed.
  // Type-level required guarantees probe shape under normal use, but a
  // cast-bypassed or buggy probe must not let destructive commands ship.
  it('assertHostLive throwing fails closed (defense in depth)', async () => {
    const switchFocus = vi.fn()
    const assertHostLive = vi.fn().mockImplementation(() => { throw new Error('probe blew up') })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan', name: 'A', cwd: '~', mode: 'terminal',
    })

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'rm -rf /' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive },
    )

    expect(executeCommand).not.toHaveBeenCalled()
    expect(switchFocus).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined()
  })

  it('send-keys failure — STILL switches focus + toast carries Retry action', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('503'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive: live },
    )

    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-h')
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Session created.*command failed/i)
    expect(toast!.action).toBeTypeOf('function')
    expect(toast!.actionLabel).toMatch(/retry/i)

    // retry triggers executeCommand again (host still live)
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined)
    toast!.action!()
    await Promise.resolve()
    expect(executeCommand).toHaveBeenCalledTimes(2)
  })

  // Finding 1 — retry action MUST re-check assertHostLive. The toast can sit
  // for several seconds and the user can delete the host between toast show
  // and retry click; retry without re-check would ship the command to a
  // fallback host (same root cause as the createSession-time check).
  it('retry action re-checks assertHostLive — host deleted between toast and retry → retry no-op', async () => {
    const switchFocus = vi.fn()
    const stillLive = { current: true }
    const assertHostLive = vi.fn().mockImplementation(() => stillLive.current)
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('503'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive },
    )

    const toast = useUndoToast.getState().toast
    expect(toast!.action).toBeTypeOf('function')
    expect(executeCommand).toHaveBeenCalledTimes(1)

    // User deletes host between toast and retry click
    stillLive.current = false
    toast!.action!()
    await Promise.resolve()
    // No second executeCommand call — retry detected dead host
    expect(executeCommand).toHaveBeenCalledTimes(1)
  })

  it('switchToSession failure — toast WITHOUT action button', async () => {
    const switchFocus = vi.fn().mockImplementation(() => { throw new Error('switch failed') })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive: live },
    )

    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined()
  })

  it('send-keys AND switch BOTH fail — surfaces switch_failed (no retry)', async () => {
    const switchFocus = vi.fn().mockImplementation(() => { throw new Error('switch failed') })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('send-keys failed'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus, assertHostLive: live },
    )

    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.message).not.toMatch(/command failed/i)
    expect(toast!.action).toBeUndefined()
  })

  // Type-level invariants verified by `tsc -b` (pnpm run build), NOT by vitest
  // runtime — same caveat as the runWorkspaceSlot type-level test above.
  // Phase 1c contract:
  //   - HostSlotContext.hostId is required non-null string
  //   - HostDeps has REQUIRED switchToSession + assertHostLive
  //   - HostDeps does NOT carry workspace probes (assertContextLive / resolveHostId)
  it('type-level invariants verified by tsc -b — HostSlotContext.hostId required, HostDeps shape locked', () => {
    type Deps = Parameters<typeof runHostSlot>[2]
    type Ctx = Parameters<typeof runHostSlot>[1]
    type IsAny<T> = 0 extends 1 & T ? true : false

    type DepsIsNotAny = IsAny<Deps> extends true ? false : true
    type HostIdRequired = object extends Pick<Ctx, 'hostId'> ? false : true
    type HostIdRejectsNull = null extends Ctx['hostId'] ? false : true
    // Positive — host probe is required in HostDeps
    type AssertHostLiveRequired = object extends Pick<Deps, 'assertHostLive'> ? false : true
    // Negative — workspace-side probes must NEVER appear in HostDeps. If a
    // future change adds them, these assertions fail and force a redesign
    // discussion (cross-shape probes mean a caller could mistakenly route a
    // workspace-shaped flow through the host executor).
    type HostDepsHasNoWorkspaceProbe = 'assertContextLive' extends keyof Deps ? false : true
    type HostDepsHasNoResolve = 'resolveHostId' extends keyof Deps ? false : true

    const _depsNotAny: DepsIsNotAny = true
    const _hostIdRequired: HostIdRequired = true
    const _hostIdRejectsNull: HostIdRejectsNull = true
    const _hostLiveRequired: AssertHostLiveRequired = true
    const _noWorkspaceProbe: HostDepsHasNoWorkspaceProbe = true
    const _noResolve: HostDepsHasNoResolve = true

    // Reference HostSlotContext type so the import isn't tree-shaken
    type _CtxAlias = HostSlotContext
    const _ctxCheck: _CtxAlias = { hostId: 'h1' }
    expect(_ctxCheck.hostId).toBe('h1')

    expect(
      _depsNotAny && _hostIdRequired && _hostIdRejectsNull
        && _hostLiveRequired && _noWorkspaceProbe && _noResolve,
    ).toBe(true)
  })
})

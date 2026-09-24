// spa/src/lib/shown-hosts.test.ts — the enabled-host selector and the pane matcher (host ownership spec §4.5, plan
// H2d-1 T3, §0.21 / §0.23).
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useShownHostsStore, type ShownHosts } from '../stores/useShownHostsStore'
import { syncIdOfSync } from './profile/host-identity'
import type { PaneContent } from '../types/tab'
import {
  hostRefOf,
  isHostRefEnabledNow,
  isHostShown,
  isPaneHostEnabled,
  isRefEnabled,
  useIsHostShown,
  usePaneHostEnabled,
  useShownHostFilter,
  wireOfRef,
} from './shown-hosts'

const DAEMON = 'air-lab:26aaaa'
const WIRE = syncIdOfSync(DAEMON)
const FAR = syncIdOfSync('nowhere:000000') // a daemon no host of this device has
const LOCAL = 'loc001' // has DAEMON
const PLAIN = 'plain1' // no daemonId

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...over })
const HOSTS: Record<string, HostConfig> = { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), [PLAIN]: host(PLAIN, { order: 1 }) }
const ORDER = [LOCAL, PLAIN]
const tmux = (hostId: string, over: object = {}): PaneContent => ({ kind: 'tmux-session', hostId, sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', ...over })
const exec = (hostRef?: string): PaneContent => ({ kind: 'execution', executionId: 'e', ...(hostRef === undefined ? {} : { host: hostRef }) })
const list = (...ids: string[]): ShownHosts => ({ ids })

beforeEach(() => {
  localStorage.clear()
  useHostStore.setState({ hosts: HOSTS, hostOrder: ORDER, activeHostId: LOCAL })
  useShownHostsStore.setState({ ids: [] })
})

describe('isHostShown (the "open a tab" surfaces — local hosts)', () => {
  it('a list → by wireIdOfHost: a d1_ id shows the local host of that daemon; a no-daemonId host by its local id', () => {
    expect(isHostShown(LOCAL, HOSTS, list(WIRE))).toBe(true)
    expect(isHostShown(LOCAL, HOSTS, list(LOCAL))).toBe(false) // its wire id is its d1_, not its local id
    expect(isHostShown(PLAIN, HOSTS, list(PLAIN))).toBe(true)
    expect(isHostShown(PLAIN, HOSTS, list(WIRE))).toBe(false)
  })

  it('an id that is not a local host → shown (navigation to it is not this module\'s business)', () => {
    expect(isHostShown(FAR, HOSTS, list(WIRE))).toBe(true)
    expect(isHostShown('gone', HOSTS, list())).toBe(true)
  })
})

describe('useIsHostShown / useShownHostFilter', () => {
  it('useIsHostShown follows a shown-hosts write and a daemonId learned', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    useHostStore.setState({ hosts: { ...HOSTS, [LOCAL]: host(LOCAL) } }) // daemonId not known yet
    const { result } = renderHook(() => useIsHostShown(LOCAL))
    expect(result.current).toBe(false)
    act(() => useHostStore.setState({ hosts: HOSTS }))
    expect(result.current).toBe(true)
    act(() => useShownHostsStore.setState({ ids: [] }))
    expect(result.current).toBe(false)
  })

  it('useShownHostFilter answers per host from the current stores, stable while they are', () => {
    useShownHostsStore.setState({ ids: [PLAIN] })
    const { result, rerender } = renderHook(() => useShownHostFilter())
    const first = result.current
    expect(first(LOCAL)).toBe(false)
    expect(first(PLAIN)).toBe(true)
    rerender()
    expect(result.current).toBe(first)
    act(() => useShownHostsStore.getState().show(WIRE))
    expect(result.current(LOCAL)).toBe(true)
  })
})

describe('hostRefOf (the host-bearing leaves, §0.21 / §0.22 (a))', () => {
  it('tmux → hostId, terminated included', () => {
    expect(hostRefOf(tmux(LOCAL), ORDER)).toBe(LOCAL)
    expect(hostRefOf(tmux(WIRE, { terminated: 'session-closed' }), ORDER)).toBe(WIRE)
  })

  it('execution → its host; hostless → hostOrder[0]; hostless with no host at all → null', () => {
    expect(hostRefOf(exec(PLAIN), ORDER)).toBe(PLAIN)
    expect(hostRefOf(exec(), ORDER)).toBe(LOCAL)
    expect(hostRefOf(exec(''), ORDER)).toBe(LOCAL) // an empty hint is no hint (resolveExecutionHostId)
    expect(hostRefOf(exec(), [])).toBeNull()
  })

  it('a daemon-source editor, a new tab, and every other kind → null (not host-bearing)', () => {
    expect(hostRefOf({ kind: 'editor', source: { type: 'daemon', hostId: LOCAL }, filePath: '/a' }, ORDER)).toBeNull()
    expect(hostRefOf({ kind: 'new-tab' }, ORDER)).toBeNull()
    expect(hostRefOf({ kind: 'browser', url: 'https://x' }, ORDER)).toBeNull()
  })
})

describe('wireOfRef / isRefEnabled / isPaneHostEnabled (wire space)', () => {
  it('wireOfRef: a local host → its wire id; any other ref → itself', () => {
    expect(wireOfRef(LOCAL, HOSTS)).toBe(WIRE)
    expect(wireOfRef(PLAIN, HOSTS)).toBe(PLAIN)
    expect(wireOfRef(FAR, HOSTS)).toBe(FAR)
    expect(wireOfRef('toString', HOSTS)).toBe('toString') // own keys only
  })

  it('a pane on an unresolved d1_X with ids lacking d1_X → false — where isHostShown(d1_X) says true', () => {
    const pane = tmux(FAR)
    const shown = list(WIRE)
    expect(isPaneHostEnabled(pane, HOSTS, ORDER, shown)).toBe(false)
    expect(isHostShown(FAR, HOSTS, shown)).toBe(true) // side by side: the two are different rules
    expect(isPaneHostEnabled(pane, HOSTS, ORDER, list(FAR))).toBe(true)
  })

  it('a pane on a local host with a daemonId → by its d1_ id', () => {
    expect(isPaneHostEnabled(tmux(LOCAL), HOSTS, ORDER, list(WIRE))).toBe(true)
    expect(isPaneHostEnabled(tmux(LOCAL), HOSTS, ORDER, list(LOCAL))).toBe(false)
    expect(isPaneHostEnabled(tmux(WIRE), HOSTS, ORDER, list(WIRE))).toBe(true) // the d1_ form of a host here
  })

  it('the hostless execution follows hostOrder[0]', () => {
    expect(isPaneHostEnabled(exec(), HOSTS, ORDER, list(PLAIN))).toBe(false)
    expect(isPaneHostEnabled(exec(), HOSTS, [PLAIN, LOCAL], list(PLAIN))).toBe(true)
  })

  it('a non-host-bearing pane → true', () => {
    expect(isPaneHostEnabled({ kind: 'new-tab' }, HOSTS, ORDER, list())).toBe(true)
    expect(isPaneHostEnabled(exec(), HOSTS, [], list())).toBe(true) // no host at all: nothing to disable
  })

  it('isRefEnabled is the ref-level rule', () => {
    expect(isRefEnabled(LOCAL, HOSTS, list(WIRE))).toBe(true)
    expect(isRefEnabled(FAR, HOSTS, list(WIRE))).toBe(false)
  })
})

describe('usePaneHostEnabled / isHostRefEnabledNow (live)', () => {
  it('re-renders on a shown-hosts write', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    const { result } = renderHook(() => usePaneHostEnabled(tmux(PLAIN)))
    expect(result.current).toBe(false)
    act(() => useShownHostsStore.getState().show(PLAIN))
    expect(result.current).toBe(true)
    act(() => useShownHostsStore.getState().toggle(PLAIN))
    expect(result.current).toBe(false)
  })

  it('re-renders on a daemonId learned (the host\'s wire id moves)', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    useHostStore.setState({ hosts: { ...HOSTS, [LOCAL]: host(LOCAL) } })
    const content = tmux(LOCAL)
    const { result } = renderHook(() => usePaneHostEnabled(content))
    expect(result.current).toBe(false)
    act(() => useHostStore.setState({ hosts: HOSTS }))
    expect(result.current).toBe(true)
  })

  it('re-renders on hostOrder for a hostless execution', () => {
    useShownHostsStore.setState({ ids: [PLAIN] })
    const content = exec()
    const { result } = renderHook(() => usePaneHostEnabled(content))
    expect(result.current).toBe(false)
    act(() => useHostStore.setState({ hostOrder: [PLAIN, LOCAL] }))
    expect(result.current).toBe(true)
  })

  it('isHostRefEnabledNow reads both stores now', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    expect(isHostRefEnabledNow(LOCAL)).toBe(true)
    expect(isHostRefEnabledNow(PLAIN)).toBe(false)
    expect(isHostRefEnabledNow(FAR)).toBe(false)
    useShownHostsStore.getState().show(FAR)
    expect(isHostRefEnabledNow(FAR)).toBe(true)
  })
})

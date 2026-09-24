// spa/src/lib/shown-hosts.not-filtered.fallbacks.test.tsx — hidden ≠ absent, fallbacks and direct navigation (host
// ownership plan H2d-5 T3, §0.21 table, last row). With air26 as `activeHostId` / `hostOrder[0]`, a host hidden in the
// workbench is still the one every fallback resolves to: `resolveExecutionHostId` for a hostless id, the fs backends
// (the host-bound resolver and the active-host proxy), the backup auto-trigger's target, the Hosts page at
// `/hosts/<air26>/overview` (and bare `/hosts`), and the device settings' development-host picker. Each case observes
// air26 HIDDEN (`ids: [<mlab wire>]`) and SHOWN (`ids: [<mlab wire>, <air26 wire>]`) and requires the two observations
// to be identical.
//
// (What a HIDDEN host's execution opener does with that fallback — land on the Hosts page instead of opening a tab — is
// H2d-3's and is not re-tested here: the fallback itself is unchanged, the opener's rule sits on top of it.)
vi.mock('./host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./host-api')>()),
  listSessions: vi.fn().mockResolvedValue([]),
}))
vi.mock('../components/hosts/HostSidebar', () => ({
  HostSidebar: (props: { selectedHostId: string; selectedSubPage: string }) => (
    <div data-testid="host-sidebar" data-host={props.selectedHostId} data-subpage={props.selectedSubPage} />
  ),
}))
vi.mock('../components/hosts/OverviewSection', () => ({
  OverviewSection: (props: { hostId: string }) => <div data-testid="overview-section" data-host={props.hostId} />,
}))
vi.mock('../components/hosts/SessionsSection', () => ({
  SessionsSection: (props: { hostId: string }) => <div data-testid="sessions-section" data-host={props.hostId} />,
}))

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useBackupStore } from '../stores/useBackupStore'
import { HostPage, resetLastHostSelection } from '../components/HostPage'
import { DevEnvironmentSection } from '../components/settings/DevEnvironmentSection'
import { clearContributions } from './settings-contribution-registry'
import { clearModuleRegistry } from './module-registry'
import { registerBuiltinModules } from './register-modules'
import { registerBuiltinFsBackends } from './register-modules/fs-backends'
import { clearFsBackendRegistry, getFsBackend, registerFsBackend, type FsBackend } from './fs-backend'
import { startBackupAutoTrigger } from './storage-backup/backup-auto-trigger'
import { resolveExecutionHostId } from './nex/resolve-host'
import { syncIdOfSync } from './profile/host-identity'
import { isRefShownNow } from './shown-hosts'
import type { PlatformCapabilities } from './platform'

const MLAB = 'h-mlab'
const AIR = 'h-air26'
const MLAB_WIRE = syncIdOfSync('mini-lab:27bbbb')
const AIR_WIRE = syncIdOfSync('air-lab:26aaaa')
const AIR_BASE = 'http://100.64.0.4:7860'

type Setting = 'hidden' | 'shown'
const IDS: Record<Setting, string[]> = { hidden: [MLAB_WIRE], shown: [MLAB_WIRE, AIR_WIRE] }

const host = (id: string, ip: string, daemonId: string, order: number): HostConfig =>
  ({ id, name: id, ip, port: 7860, token: 'tok', order, daemonId })

const fetchMock = vi.fn(async (url: string | URL) => {
  const href = String(url)
  if (href.endsWith('/api/dev/daemon/check')) {
    return new Response(JSON.stringify({ current_hash: 'abc1234', latest_hash: 'abc1234', available: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }
  return new Response(JSON.stringify({ size: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } })
})

/** air26 is FIRST in `hostOrder` and the active host; the setting hides or shows it. */
function arrange(setting: Setting): void {
  localStorage.clear()
  cleanup()
  fetchMock.mockClear()
  useHostStore.setState({
    hosts: { [AIR]: host(AIR, '100.64.0.4', 'air-lab:26aaaa', 0), [MLAB]: host(MLAB, '100.64.0.2', 'mini-lab:27bbbb', 1) },
    hostOrder: [AIR, MLAB],
    activeHostId: AIR,
    devHostId: AIR,
    runtime: {},
  })
  useShownHostsStore.setState({ ids: IDS[setting] })
  // The premise: the setting really hides / shows air26; mlab is shown in both.
  expect(isRefShownNow(AIR)).toBe(setting === 'shown')
  expect(isRefShownNow(MLAB)).toBe(true)
}

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.useRealTimers()
  clearFsBackendRegistry()
  clearContributions()
  clearModuleRegistry()
  delete (window as unknown as Record<string, unknown>).electronAPI
  useShownHostsStore.setState({ ids: [] })
  useHostStore.getState().reset()
})

async function both<T>(observe: (setting: Setting) => T | Promise<T>): Promise<{ hidden: T; shown: T }> {
  const hidden = await observe('hidden')
  const shown = await observe('shown')
  return { hidden, shown }
}

describe('hidden ≠ absent — fallbacks and direct navigation (H2d-5 T3)', () => {
  it('`resolveExecutionHostId` returns air26 (hostOrder[0]) for a hostless id — identical hidden and shown', async () => {
    const { hidden, shown } = await both((setting) => {
      arrange(setting)
      return [resolveExecutionHostId(), resolveExecutionHostId(''), resolveExecutionHostId(AIR)]
    })
    expect(hidden).toEqual([AIR, AIR, AIR])
    expect(hidden).toEqual(shown)
  })

  it('the fs backends resolve air26: the host-bound resolver and the active-host proxy address its daemon — identical hidden and shown', async () => {
    const { hidden, shown } = await both(async (setting) => {
      arrange(setting)
      clearFsBackendRegistry()
      registerBuiltinFsBackends({ hasLocalFilesystem: false } as PlatformCapabilities)
      const bound = getFsBackend({ type: 'daemon', hostId: AIR })
      const active = getFsBackend({ type: 'daemon', hostId: '' })
      await bound?.stat('/p/a.md')
      await active?.stat('/p/b.md')
      return {
        bound: bound?.available() ?? null,
        active: active?.available() ?? null,
        urls: fetchMock.mock.calls.map(([url]) => String(url)),
      }
    })
    expect(hidden).toEqual({ bound: true, active: true, urls: [`${AIR_BASE}/api/fs/stat`, `${AIR_BASE}/api/fs/stat`] })
    expect(hidden).toEqual(shown)
  })

  it('the backup auto-trigger targets air26 — the boot check and a debounced mutation — identical hidden and shown', async () => {
    const { hidden, shown } = await both((setting) => {
      arrange(setting)
      vi.useFakeTimers()
      const listeners = new Set<() => void>()
      const inapp = { onMutation: (cb: () => void) => { listeners.add(cb); return () => listeners.delete(cb) } }
      clearFsBackendRegistry()
      registerFsBackend('inapp', inapp as unknown as FsBackend)
      const backupNow = vi.fn(async () => null)
      useBackupStore.setState({ backupNow } as never)
      const trigger = startBackupAutoTrigger()
      listeners.forEach((cb) => cb())
      vi.advanceTimersByTime(2000)
      trigger?.dispose()
      vi.useRealTimers()
      return { started: trigger !== null, targets: backupNow.mock.calls.map(([id]) => id) }
    })
    expect(hidden).toEqual({ started: true, targets: [AIR, AIR] })
    expect(hidden).toEqual(shown)
  })

  it('the Hosts page at /hosts/<air26>/overview renders OverviewSection for air26; bare /hosts falls back to it — identical hidden and shown', async () => {
    const { hidden, shown } = await both((setting) => {
      arrange(setting)
      resetLastHostSelection()
      clearContributions()
      clearModuleRegistry()
      registerBuiltinModules()
      const view = (path: string) => {
        const mem = memoryLocation({ path, record: true })
        const r = render(<Router hook={mem.hook}><HostPage pane={{ id: 'pane-hosts', content: { kind: 'hosts' } }} isActive /></Router>)
        const out = {
          sidebar: screen.getByTestId('host-sidebar').getAttribute('data-host'),
          overview: screen.queryByTestId('overview-section')?.getAttribute('data-host') ?? null,
          path: mem.history[mem.history.length - 1],
        }
        r.unmount()
        return out
      }
      const direct = view(`/hosts/${AIR}/overview`)
      resetLastHostSelection()
      const bare = view('/hosts')
      return { direct, bare }
    })
    expect(hidden.direct).toEqual({ sidebar: AIR, overview: AIR, path: `/hosts/${AIR}/overview` })
    expect(hidden.bare.sidebar).toBe(AIR)
    expect(hidden).toEqual(shown)
  })

  it('the development-host picker lists air26 and keeps it selected — identical hidden and shown', async () => {
    const { hidden, shown } = await both(async (setting) => {
      arrange(setting)
      window.electronAPI = {
        ...window.electronAPI!,
        getAppInfo: vi.fn().mockResolvedValue({ version: '1.0.0', electronHash: 'abc1234', spaHash: 'def5678', devUpdateEnabled: true }),
        streamCheck: vi.fn(() => () => {}),
        applyUpdate: vi.fn(),
        forceLoadSPA: vi.fn().mockResolvedValue(undefined),
      } as typeof window.electronAPI
      let view: ReturnType<typeof render> | undefined
      await act(async () => { view = render(<DevEnvironmentSection />) })
      const select = screen.getByLabelText('Development host') as HTMLSelectElement
      const out = { value: select.value, options: [...select.options].map((o) => o.value) }
      view?.unmount()
      return out
    })
    expect(hidden).toEqual({ value: AIR, options: ['', AIR, MLAB] })
    expect(hidden).toEqual(shown)
  })
})

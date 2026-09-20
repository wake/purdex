// spa/src/lib/profile/start.reload.integration.test.ts — what a RELOAD sees.
// Every "window" here is a fresh module graph (`vi.resetModules()` + dynamic
// imports), so `useProfileStore` is really rebuilt from what `localStorage`
// holds — stopping and restarting `startProfileSync()` on a live store proves
// nothing about that. Only the network (`./api`) is replaced; everything else is
// the real thing.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'

vi.mock('./api', () => ({
  listProfiles: vi.fn(),
  getSection: vi.fn(),
  putSection: vi.fn(),
  deleteSection: vi.fn(),
  putAttachment: vi.fn(),
  deleteAttachment: vi.fn(),
}))

vi.mock('../client-identity', () => ({ getClientId: () => 'c_aaaaaaaaaaaa', isClientIdPersisted: () => true }))

const M = 'host-master'
const PROFILE = 'p_0123456789ab'

let stop: () => void = () => {}

/** A window opening: module state is gone, storage is what it was. The master host is connected at `ip`. */
async function openWindow(ip: string) {
  vi.resetModules()
  const api = vi.mocked(await import('./api'))
  const calls = [api.listProfiles, api.getSection, api.putSection, api.deleteSection, api.putAttachment, api.deleteAttachment]
  for (const fn of calls) (fn as ReturnType<typeof vi.fn>).mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
  const { useHostStore } = await import('../../stores/useHostStore')
  const { useProfileStore, selectMaster } = await import('../../stores/useProfileStore')
  const start = await import('./start')
  useHostStore.setState({
    hosts: { [M]: { id: M, name: M, ip, port: 7860, token: 'tok', order: 0 } },
    hostOrder: [M],
    activeHostId: M,
    runtime: { [M]: { status: 'connected' } },
  })
  stop = start.startProfileSync()
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
  await vi.advanceTimersByTimeAsync(120_000)
  return { api, calls, start, master: () => selectMaster(useProfileStore.getState()), useProfileStore }
}

function persisted(state: Record<string, unknown>): void {
  localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ state, version: 1 }))
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  stop()
  stop = () => {}
  vi.restoreAllMocks()
  vi.useRealTimers()
  vi.resetModules()
})

describe('a window opens on what storage holds', () => {
  it('a master WITHOUT an endpoint (attached before the field existed): no master, nothing built, not one request', async () => {
    persisted({ masterHostId: M, masterProfileId: PROFILE, autoSync: true, pendingDirection: 'pull', attachGeneration: 3 })
    const w = await openWindow('10.9.9.9') // wherever the host points today — nobody knows where the bases came from
    expect(w.master()).toBeNull()
    expect(w.useProfileStore.getState().pendingDirection).toBeNull()
    expect(w.start.profileSyncState()).toMatchObject({ master: null, leader: false, blocked: null, status: null })
    for (const fn of w.calls) expect(fn).not.toHaveBeenCalled()
    expect(localStorage.getItem(STORAGE_KEYS.PROFILE_LEADER)).toBeNull()
  })

  it('the host was re-pointed and the window reloaded: the STORED endpoint is the home — still blocked, not one request', async () => {
    persisted({ masterHostId: M, masterProfileId: PROFILE, masterEndpoint: '10.0.0.1:7860', autoSync: true, pendingDirection: null, attachGeneration: 3 })
    const w = await openWindow('10.9.9.9')
    expect(w.master()).toEqual({ hostId: M, profileId: PROFILE })
    expect(w.start.profileSyncState().blocked).toBe('master-endpoint-changed')
    for (const fn of w.calls) expect(fn).not.toHaveBeenCalled()
  })

  it('control: the same record with the host where it was → the driver starts (the attachment goes out)', async () => {
    persisted({ masterHostId: M, masterProfileId: PROFILE, masterEndpoint: '10.0.0.1:7860', autoSync: true, pendingDirection: null, attachGeneration: 3 })
    const w = await openWindow('10.0.0.1')
    expect(w.start.profileSyncState().blocked).toBeNull()
    expect(w.api.putAttachment).toHaveBeenCalled()
  })
})

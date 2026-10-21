// spa/src/main.test.tsx — what the app does at boot, for the parts a test can see. Every starter and the render
// are stubbed; the module is imported once, exactly as the browser does.
import { beforeAll, describe, it, expect, vi } from 'vitest'

vi.mock('react-dom/client', () => ({ createRoot: vi.fn(() => ({ render: vi.fn() })) }))
vi.mock('./App.tsx', () => ({ default: () => null }))
vi.mock('./lib/register-locales', () => ({ registerBuiltinLocales: vi.fn() }))
vi.mock('./lib/register-themes', () => ({ registerBuiltinThemes: vi.fn() }))
vi.mock('./lib/register-modules', () => ({ registerBuiltinModules: vi.fn() }))
vi.mock('./lib/storage-backup/backup-auto-trigger', () => ({ startBackupAutoTrigger: vi.fn() }))
vi.mock('./lib/host-config-loader', () => ({ startHostConfigLoader: vi.fn() }))
vi.mock('./lib/names/persona-loader', () => ({ startPersonaLoader: vi.fn() }))
vi.mock('./lib/host-lifecycle', () => ({ startPeerCacheInvalidation: vi.fn() }))
// The store itself stays real: the watcher area's start-up subscribes to it (the jobs tab appears once a daemon announces `jobs.v1`).
vi.mock('./stores/useNexHostStore', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./stores/useNexHostStore')>()),
  startNexHostInvalidation: vi.fn(),
}))
vi.mock('./stores/useExecutionListStore', () => ({ startExecutionListInvalidation: vi.fn() }))
vi.mock('./lib/nex/worker-title-prefetch', () => ({ startWorkerTitlePrefetch: vi.fn() }))
vi.mock('./lib/profile/start', () => ({ startProfileSync: vi.fn() }))
// The pool hosts' look copy waits for the pool and the fleet; the boot-order tests below control when it returns.
vi.mock('./lib/pool/pool-look-migration', () => ({ bootPoolLooks: vi.fn(() => Promise.resolve()) }))
// The global 主機格指示 copy (MON-9a-3) is synchronous; the boot-order test below watches when it runs.
vi.mock('./lib/pool/indicator-migration', () => ({ migrateGlobalIndicator: vi.fn() }))
vi.mock('./lib/pending-revocation-retry', () => ({ startPendingRevocationRetry: vi.fn() }))
vi.mock('./features/workspace/lib/adopt-standalone', () => ({ startStandaloneAdoption: vi.fn() }))
vi.mock('./lib/legacy-residue-cleanup', () => ({ scheduleLegacyResidueCleanup: vi.fn() }))
vi.mock('./lib/journal/journal-loader', () => ({ startJournalLoader: vi.fn() }))
vi.mock('./lib/relay/fresh-sync', () => ({ startRelayFresh: vi.fn() }))
vi.mock('./stores/useDeviceNameStore', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./stores/useDeviceNameStore')>()),
  ensureDefaultDeviceName: vi.fn(() => Promise.resolve()),
}))

describe('boot', () => {
  // The first `import('./main')` loads the whole SPA start-up chain (several seconds under host load) and used to be
  // charged to whichever test ran first, against its 5 s budget (#3027). Do it once here, in the hook's own budget
  // (30 s: measured 5-9 s cold on a loaded host, so the 10 s default leaves no margin);
  // the tests' `import('./main')` then resolve from the module cache, exactly as before, and each test still asserts
  // that this one boot called its starter exactly once.
  beforeAll(async () => {
    await import('./main')
  }, 30_000)

  // The default device name used to be resolved by the device-state uploader as a side effect. Settings › Profile's
  // name field and the profile wizard read the store, so without this they show the "Browser" fallback.
  it('resolves the default device name', async () => {
    const { ensureDefaultDeviceName } = await import('./stores/useDeviceNameStore')
    await import('./main')
    expect(ensureDefaultDeviceName).toHaveBeenCalledTimes(1)
  })

  // #1303: the removed Sync / device-state / workspace-snapshot features' leftovers. Scheduled, never awaited.
  it('schedules the legacy residue cleanup once', async () => {
    const { scheduleLegacyResidueCleanup } = await import('./lib/legacy-residue-cleanup')
    await import('./main')
    expect(scheduleLegacyResidueCleanup).toHaveBeenCalledTimes(1)
  })

  // #1557: an unopened worker tab with neither a live summary nor a list row gets its title — app lifetime, once.
  it('starts the worker title prefetch once', async () => {
    const { startWorkerTitlePrefetch } = await import('./lib/nex/worker-title-prefetch')
    await import('./main')
    expect(startWorkerTitlePrefetch).toHaveBeenCalledTimes(1)
  })

  // QR pairing QP-3: a revocation a host could not take is retried when that host connects — app lifetime, once.
  it('starts the pending revocation retry once', async () => {
    const { startPendingRevocationRetry } = await import('./lib/pending-revocation-retry')
    await import('./main')
    expect(startPendingRevocationRetry).toHaveBeenCalledTimes(1)
  })

  // J-2a T-2a.6: the active tab's journal unit is kept loaded whether or not the card is open — app lifetime, once.
  it('starts the journal loader once', async () => {
    const { startJournalLoader } = await import('./lib/journal/journal-loader')
    await import('./main')
    expect(startJournalLoader).toHaveBeenCalledTimes(1)
  })

  // RA-3 (relay v2 contract 5): the per-host list of written handoff files is kept current for the app's lifetime — once.
  it('starts the relay fresh sync once', async () => {
    const { startRelayFresh } = await import('./lib/relay/fresh-sync')
    await import('./main')
    expect(startRelayFresh).toHaveBeenCalledTimes(1)
  })

  // H2c-2 (plan §0.16): the host-look migration runs after BOTH the host store and the look store hydrated, and
  // Profile Sync starts only after it — the collector exists only inside `startProfileSync`, so no `settings` build
  // or report can precede the migration.
  it('starts Profile Sync only after both stores hydrated and the host-look migration ran', async () => {
    vi.resetModules()
    const { useHostStore } = await import('./stores/useHostStore')
    const { useHostLookStore } = await import('./stores/useHostLookStore')
    const { STORAGE_KEYS } = await import('./lib/storage')
    const { startProfileSync } = await import('./lib/profile/start')
    localStorage.removeItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED)
    vi.mocked(startProfileSync).mockClear() // the mock outlives `resetModules`: the earlier boots called it

    // Hold both hydrations back: not hydrated, and every `onFinishHydration` listener captured for the test to fire.
    interface Held {
      getState: () => unknown
      persist: { hasHydrated: () => boolean; onFinishHydration: (fn: (state: never) => void) => () => void }
    }
    function holdHydration(store: Held) {
      let hydrated = false
      const listeners: ((state: never) => void)[] = []
      vi.spyOn(store.persist, 'hasHydrated').mockImplementation(() => hydrated)
      vi.spyOn(store.persist, 'onFinishHydration').mockImplementation((fn: (state: never) => void) => {
        listeners.push(fn)
        return () => {}
      })
      return () => {
        hydrated = true
        for (const fn of listeners) fn(store.getState() as never)
      }
    }
    const hydrateHosts = holdHydration(useHostStore as unknown as Held)
    const hydrateLooks = holdHydration(useHostLookStore as unknown as Held)

    const order: string[] = []
    const lookWrites = vi.fn(() => order.push('migration'))
    useHostLookStore.subscribe(lookWrites)
    vi.mocked(startProfileSync).mockImplementation(() => {
      order.push(`startProfileSync (marker ${localStorage.getItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED)})`)
      return () => {}
    })
    const flush = async () => {
      for (let i = 0; i < 10; i++) await Promise.resolve()
    }

    await import('./main')
    await flush()
    expect(startProfileSync).not.toHaveBeenCalled()
    expect(lookWrites).not.toHaveBeenCalled()
    expect(localStorage.getItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED)).toBeNull()

    hydrateHosts()
    await flush()
    expect(startProfileSync).not.toHaveBeenCalled()
    expect(lookWrites).not.toHaveBeenCalled()

    hydrateLooks()
    await flush()
    expect(startProfileSync).toHaveBeenCalledTimes(1)
    expect(order).toEqual(['migration', 'startProfileSync (marker 1)'])
  })
  // Monitoring tabs MON-2b (A3): the pool hosts' look copy runs after the host-look migration and before Profile Sync, so the first
  // `settings` build already holds what it copied.
  it('starts Profile Sync only after the pool hosts look copy returned, which itself runs after the host-look migration', async () => {
    vi.resetModules()
    const { startProfileSync } = await import('./lib/profile/start')
    const { bootPoolLooks } = await import('./lib/pool/pool-look-migration')
    const { usePoolHostsStore } = await import('./stores/usePoolHostsStore')
    const { STORAGE_KEYS } = await import('./lib/storage')
    localStorage.removeItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED)
    vi.mocked(startProfileSync).mockClear()
    vi.mocked(bootPoolLooks).mockClear()
    const order: string[] = []
    let release!: () => void
    vi.mocked(bootPoolLooks).mockImplementation(
      () => new Promise<void>((resolve) => { order.push(`bootPoolLooks (host-look marker ${localStorage.getItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED)})`); release = () => { usePoolHostsStore.setState({ order: ['82b11c521df16c12'] }); resolve() } }),
    )
    vi.mocked(startProfileSync).mockImplementation(() => {
      order.push(`startProfileSync (copied ${usePoolHostsStore.getState().order.length})`)
      return () => {}
    })
    const flush = async () => {
      for (let i = 0; i < 10; i++) await Promise.resolve()
    }
    await import('./main')
    await flush()
    expect(bootPoolLooks).toHaveBeenCalledTimes(1)
    expect(startProfileSync).not.toHaveBeenCalled()
    release()
    await flush()
    expect(order).toEqual(['bootPoolLooks (host-look marker 1)', 'startProfileSync (copied 1)'])
  })
  // Monitoring tabs MON-9a-3 (W7): the global 主機格指示 is copied into each host after the pool look copy and before Profile Sync, so the first `settings` build already holds the copied indicators.
  it('copies the global indicator after the pool look copy and before Profile Sync starts', async () => {
    vi.resetModules()
    const { startProfileSync } = await import('./lib/profile/start')
    const { bootPoolLooks } = await import('./lib/pool/pool-look-migration')
    const { migrateGlobalIndicator } = await import('./lib/pool/indicator-migration')
    const { usePoolHostsStore } = await import('./stores/usePoolHostsStore')
    vi.mocked(startProfileSync).mockClear()
    vi.mocked(bootPoolLooks).mockClear()
    vi.mocked(migrateGlobalIndicator).mockClear()
    const order: string[] = []
    let release!: () => void
    vi.mocked(bootPoolLooks).mockImplementation(() => new Promise<void>((resolve) => { order.push('bootPoolLooks'); release = resolve }))
    vi.mocked(migrateGlobalIndicator).mockImplementation(() => { order.push('migrateGlobalIndicator'); usePoolHostsStore.setState({ order: ['82b11c521df16c12'] }) })
    vi.mocked(startProfileSync).mockImplementation(() => {
      order.push(`startProfileSync (indicators copied: ${usePoolHostsStore.getState().order.length === 1})`)
      return () => {}
    })
    const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve() }
    await import('./main')
    await flush()
    expect(migrateGlobalIndicator).not.toHaveBeenCalled() // the pool look copy has not returned
    release()
    await flush()
    expect(order).toEqual(['bootPoolLooks', 'migrateGlobalIndicator', 'startProfileSync (indicators copied: true)'])
  })
})

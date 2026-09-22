// spa/src/main.test.tsx — what the app does at boot, for the parts a test can see. Every starter and the render
// are stubbed; the module is imported once, exactly as the browser does.
import { describe, it, expect, vi } from 'vitest'

vi.mock('react-dom/client', () => ({ createRoot: vi.fn(() => ({ render: vi.fn() })) }))
vi.mock('./App.tsx', () => ({ default: () => null }))
vi.mock('./lib/register-locales', () => ({ registerBuiltinLocales: vi.fn() }))
vi.mock('./lib/register-themes', () => ({ registerBuiltinThemes: vi.fn() }))
vi.mock('./lib/register-modules', () => ({ registerBuiltinModules: vi.fn() }))
vi.mock('./lib/storage-backup/backup-auto-trigger', () => ({ startBackupAutoTrigger: vi.fn() }))
vi.mock('./lib/host-config-loader', () => ({ startHostConfigLoader: vi.fn() }))
vi.mock('./lib/host-lifecycle', () => ({ startPeerCacheInvalidation: vi.fn() }))
vi.mock('./stores/useNexHostStore', () => ({ startNexHostInvalidation: vi.fn() }))
vi.mock('./stores/useExecutionListStore', () => ({ startExecutionListInvalidation: vi.fn() }))
vi.mock('./lib/profile/start', () => ({ startProfileSync: vi.fn() }))
vi.mock('./features/workspace/lib/adopt-standalone', () => ({ startStandaloneAdoption: vi.fn() }))
vi.mock('./stores/useDeviceNameStore', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./stores/useDeviceNameStore')>()),
  ensureDefaultDeviceName: vi.fn(() => Promise.resolve()),
}))

describe('boot', () => {
  // The default device name used to be resolved by the device-state uploader as a side effect. Settings › Profile's
  // name field and the profile wizard read the store, so without this they show the "Browser" fallback.
  it('resolves the default device name', async () => {
    const { ensureDefaultDeviceName } = await import('./stores/useDeviceNameStore')
    await import('./main')
    expect(ensureDefaultDeviceName).toHaveBeenCalledTimes(1)
  })
})

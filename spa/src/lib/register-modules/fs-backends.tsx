import type { PlatformCapabilities } from '../platform'
import { registerFsBackend, registerFsBackendResolver, getFsBackend, type FsBackend } from '../fs-backend'
import { InAppBackend } from '../fs-backend-inapp'
import { DaemonBackend, createDaemonBackendForHost } from '../fs-backend-daemon'
import { LocalBackend } from '../fs-backend-local'
import { useHostStore } from '../../stores/useHostStore'

export function registerBuiltinFsBackends(caps: PlatformCapabilities): void {
  // InApp FS backend (singleton — 避免熱重載時資料遺失)
  if (!getFsBackend({ type: 'inapp' })) {
    registerFsBackend('inapp', new InAppBackend())
  }

  // DaemonBackend (lazy proxy — creates a new DaemonBackend per call,
  // resolving active host at invocation time. This is intentional: the active
  // host can change at any time and DaemonBackend is stateless. If
  // DaemonBackend gains internal state, switch to a memoized-by-hostId pattern.)
  if (!getFsBackend({ type: 'daemon', hostId: '' })) {
    // Host-bound resolution: a daemon file belongs to `source.hostId`, so it
    // must be read/written on THAT host even while another one is active.
    //
    // One instance per host, cached: `getFsBackend` is called during RENDER by
    // the preview panes — ImagePreviewPane compares the resolved backend object
    // to detect a session change, and PdfPreviewPane keys its read effect on it
    // — so handing back a fresh object per call turns them into infinite
    // re-render / re-download loops (verified: "Too many re-renders" without
    // this cache). The instances stay correct across host edits because
    // `createDaemonBackendForHost` re-reads `useHostStore` on every call; the
    // cache's lifetime is this registration (a `clearFsBackendRegistry()` +
    // re-register starts a fresh one).
    const daemonByHost = new Map<string, FsBackend>()
    registerFsBackendResolver('daemon', (source) => {
      // Decline (→ flat registry / active-host proxy) only for the hostId-less
      // probe. A source that names a host is answered here or nowhere.
      if (source.type !== 'daemon' || !source.hostId) return undefined
      // The host is gone: REFUSE (`null`), never decline. `getDaemonBase` treats
      // an unknown host as "use the active one", so any backend handed back here
      // would read — and write — the same path on a different machine, which is
      // precisely the wrong-host write this resolver exists to prevent. `null`
      // reaches EditorPane as "no backend" → the T1.2b error state.
      if (!useHostStore.getState().hosts[source.hostId]) {
        // Drop the cached instance too, so a deleted host cannot keep an entry
        // alive in the map for the lifetime of this registration.
        daemonByHost.delete(source.hostId)
        return null
      }
      let backend = daemonByHost.get(source.hostId)
      if (!backend) {
        backend = createDaemonBackendForHost(source.hostId)
        daemonByHost.set(source.hostId, backend)
      }
      return backend
    })

    const getDaemon = (): DaemonBackend => {
      const state = useHostStore.getState()
      const hostId = state.activeHostId ?? state.hostOrder[0] ?? ''
      return new DaemonBackend(
        state.getDaemonBase(hostId),
        () => state.getAuthHeaders(hostId),
      )
    }

    registerFsBackend('daemon', {
      id: 'daemon',
      label: 'Remote Host',
      available: () => !!useHostStore.getState().activeHostId,
      read: (path) => getDaemon().read(path),
      write: (path, content) => getDaemon().write(path, content),
      stat: (path) => getDaemon().stat(path),
      list: (path) => getDaemon().list(path),
      mkdir: (path, recursive) => getDaemon().mkdir(path, recursive),
      delete: (path, recursive) => getDaemon().delete(path, recursive),
      rename: (from, to) => getDaemon().rename(from, to),
    })
  }

  // LocalBackend (Electron IPC — local filesystem access)
  if (caps.hasLocalFilesystem && !getFsBackend({ type: 'local' })) {
    registerFsBackend('local', new LocalBackend())
  }
}

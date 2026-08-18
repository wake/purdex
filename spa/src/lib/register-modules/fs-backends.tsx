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
      if (source.type !== 'daemon' || !source.hostId) return undefined
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

import type { PlatformCapabilities } from '../platform'
import { registerFsBackend, getFsBackend } from '../fs-backend'
import { InAppBackend } from '../fs-backend-inapp'
import { DaemonBackend } from '../fs-backend-daemon'
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
      createUnique: (dir, baseName, ext) => getDaemon().createUnique(dir, baseName, ext),
      mkdirUnique: (dir, baseName) => getDaemon().mkdirUnique(dir, baseName),
    })
  }

  // LocalBackend (Electron IPC — local filesystem access)
  if (caps.hasLocalFilesystem && !getFsBackend({ type: 'local' })) {
    registerFsBackend('local', new LocalBackend())
  }
}

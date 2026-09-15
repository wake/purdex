// spa/src/lib/device-state/uploader.ts — automatic device-state upload to the
// Development host (spec §3.6).
//
// Watches the structural stores; a changed projection schedules one trailing
// debounce. A tick uploads only when the payload hash or the device name
// differs from the last *successful* upload to that host. Upload bookkeeping is
// uploader-local (never persisted), so a reload re-uploads once — harmless.
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { selectDevHostId, useHostStore } from '../../stores/useHostStore'
import { effectiveDeviceName, normalizeDeviceName, useDeviceStateStore } from '../../stores/useDeviceStateStore'
import { useSyncStore } from '../sync/use-sync-store'
import { putDeviceState } from './api'
import { resolveDefaultDeviceName } from './device-name'
import { buildDeviceStatePayload, hashPayload } from './payload'

export interface UploaderDeps {
  debounceMs?: number
  now?: () => number
  getAppVersion?: () => Promise<string>
}

export async function resolveAppVersion(): Promise<string> {
  try {
    return (await window.electronAPI?.getAppInfo?.())?.version ?? ''
  } catch {
    return ''
  }
}

type HostSnapshot = ReturnType<typeof useHostStore.getState>

function hostProjection(state: HostSnapshot): [string | null, string | undefined] {
  const target = selectDevHostId(state)
  return [target, target ? state.runtime[target]?.status : undefined]
}

export function startDeviceStateUploader(deps: UploaderDeps = {}): () => void {
  const debounceMs = deps.debounceMs ?? 5000
  const now = deps.now ?? Date.now
  const getAppVersion = deps.getAppVersion ?? resolveAppVersion

  const lastUploadedHash: Record<string, string> = {}
  const lastUploadedDeviceName: Record<string, string> = {}
  let timer: ReturnType<typeof setTimeout> | null = null
  let inFlight = false
  let rerun = false
  let stopped = false

  const setStatus = useDeviceStateStore.getState().setStatus

  function schedule(): void {
    if (stopped) return
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(() => {
      timer = null
      void tick()
    }, debounceMs)
  }

  async function tick(): Promise<void> {
    if (stopped) return
    if (inFlight) {
      rerun = true
      return
    }
    const hostState = useHostStore.getState()
    const target = selectDevHostId(hostState)
    if (target === null) {
      setStatus({ kind: 'no-target' })
      return
    }
    if (hostState.runtime[target]?.status !== 'connected') {
      setStatus({ kind: 'offline', hostId: target })
      return
    }

    inFlight = true
    try {
      const capturedAt = now()
      const payload = buildDeviceStatePayload(capturedAt)
      const deviceName = effectiveDeviceName(useDeviceStateStore.getState())
      const hash = await hashPayload(payload)
      if (stopped) return
      if (hash === lastUploadedHash[target] && deviceName === lastUploadedDeviceName[target]) return

      setStatus({ kind: 'uploading', hostId: target })
      const appVersion = await getAppVersion()
      if (stopped) return
      const clientId = useSyncStore.getState().getClientId()
      try {
        await putDeviceState(target, clientId, { deviceName, appVersion, capturedAt, payload })
      } catch (err) {
        if (!stopped) {
          setStatus({ kind: 'error', hostId: target, message: err instanceof Error ? err.message : String(err) })
        }
        return
      }
      if (stopped) return
      lastUploadedHash[target] = hash
      lastUploadedDeviceName[target] = deviceName
      setStatus({ kind: 'ok', at: now(), hostId: target })
    } finally {
      inFlight = false
      if (rerun && !stopped) {
        rerun = false
        void tick()
      }
    }
  }

  const unsubscribers = [
    useTabStore.subscribe((next, prev) => {
      if (next.tabs !== prev.tabs || next.tabOrder !== prev.tabOrder || next.activeTabId !== prev.activeTabId) {
        schedule()
      }
    }),
    useWorkspaceStore.subscribe((next, prev) => {
      if (next.workspaces !== prev.workspaces || next.activeWorkspaceId !== prev.activeWorkspaceId) {
        schedule()
      }
    }),
    useHostStore.subscribe((next, prev) => {
      const [nextTarget, nextStatus] = hostProjection(next)
      const [prevTarget, prevStatus] = hostProjection(prev)
      if (nextTarget !== prevTarget || nextStatus !== prevStatus) schedule()
    }),
    useDeviceStateStore.subscribe((next, prev) => {
      if (effectiveDeviceName(next) !== effectiveDeviceName(prev)) schedule()
    }),
  ]

  void resolveDefaultDeviceName().then(
    (defaultDeviceName) => {
      if (!stopped) useDeviceStateStore.setState({ defaultDeviceName: normalizeDeviceName(defaultDeviceName) ?? 'Browser' })
    },
    () => {
      // keep the store's fallback name
    },
  )

  schedule()

  return () => {
    stopped = true
    rerun = false
    if (timer !== null) clearTimeout(timer)
    timer = null
    for (const unsubscribe of unsubscribers) unsubscribe()
  }
}

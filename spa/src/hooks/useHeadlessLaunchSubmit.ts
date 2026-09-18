// spa/src/hooks/useHeadlessLaunchSubmit.ts — the submit path of the Headless
// launcher (P-C spec §4.2): the daemon-liveness gate, the busy flag,
// `delegateExecution` and the outcome mapping (rejected / 400 / 503 /
// network / success). The form owns its fields and decides *what* to send;
// this hook owns *sending it* and what the answer means.
import { useCallback, useEffect, useRef, useState } from 'react'
import { delegateExecution } from '../lib/nex/nex-api'
import { NexApiError, type NexCapabilities } from '../lib/nex/types'
import { isHostDaemonLive } from '../lib/host-live'
import type { PaneContent } from '../types/tab'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHeadlessLauncherMemoryStore } from '../stores/useHeadlessLauncherMemoryStore'
import { useI18nStore } from '../stores/useI18nStore'

export interface HeadlessLaunchInput {
  brief: string
  /** Already joined: root + validated sub-path. */
  cwd: string
  /** Remembered per host on success, alongside `profile`. */
  root: string
  profile: string
}

export interface HeadlessLaunchSubmit {
  busy: boolean
  error: string
  submit: (input: HeadlessLaunchInput) => Promise<void>
}

export function useHeadlessLaunchSubmit(
  hostId: string,
  caps: NexCapabilities,
  onSelect: (content: PaneContent) => void,
): HeadlessLaunchSubmit {
  const t = useI18nStore((s) => s.t)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const busyRef = useRef(false)
  const aliveRef = useRef(true)

  useEffect(() => {
    aliveRef.current = true
    return () => { aliveRef.current = false }
  }, [])

  const submit = useCallback(async ({ brief, cwd, root, profile }: HeadlessLaunchInput) => {
    if (busyRef.current) return
    // Daemon-only gate: a delegation runs inside Nexen, not in a tmux pane.
    if (!isHostDaemonLive(hostId)) { setError(t('newtab.headless.offline')); return }
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      const result = await delegateExecution(hostId, {
        brief,
        cwd,
        profile,
        labels: { source: 'purdex' },
        origin: `purdex://host/${hostId}/newtab`,
      }, caps)
      if (!aliveRef.current) return
      if (result.state === 'rejected') {
        setError(t('newtab.headless.rejected', { reason: result.reject_reason ?? result.state }))
        return
      }
      useHeadlessLauncherMemoryStore.getState().remember(hostId, { root, profile })
      onSelect({ kind: 'execution', executionId: result.id, host: hostId })
    } catch (err) {
      if (!aliveRef.current) return
      if (err instanceof NexApiError && err.status === 400) {
        setError(t('newtab.headless.bad_request', { code: err.code }))
        return
      }
      setError(t('newtab.headless.unavailable', { error: err instanceof Error ? err.message : String(err) }))
      if (err instanceof NexApiError && (err.status === 503 || err.code === 'network')) {
        void useNexHostStore.getState().invalidate(hostId)
      }
    } finally {
      busyRef.current = false
      if (aliveRef.current) setBusy(false)
    }
  }, [hostId, caps, onSelect, t])

  return { busy, error, submit }
}

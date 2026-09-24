import { useState, useEffect, useCallback, useRef } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore, selectDevHostId } from '../../stores/useHostStore'
import { hostLabel, hostLookOf } from '../../lib/host-look'
import { DevBuildLogPanel } from './DevBuildLogPanel'
import { LocalDaemonSection } from './LocalDaemonSection'

type UpdateStatus = 'idle' | 'checking' | 'building' | 'up_to_date' | 'update_available' | 'error'

type DaemonPhase = 'idle' | 'checking' | 'rebuilding' | 'restarting' | 'error'

interface DaemonCheck {
  current_hash: string
  latest_hash: string
  available: boolean
}

interface DaemonEvent {
  type: 'log' | 'error' | 'success' | 'restarting'
  line?: string
  message?: string
  new_hash?: string
}

interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
}

type RemoteInfo = ElectronRemoteVersionInfo

export function DevEnvironmentSection() {
  const t = useI18nStore((s) => s.t)
  const devHostId = useHostStore(selectDevHostId)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const setDevHost = useHostStore((s) => s.setDevHost)
  // null = no dev host chosen: no dev requests at all, no fallback (spec D2).
  const daemonBase: string | null = useHostStore((s) => {
    const id = selectDevHostId(s)
    return id ? s.getDaemonBase(id) : null
  })
  const token = useHostStore((s) => {
    const id = selectDevHostId(s)
    return id ? (s.hosts[id]?.token ?? undefined) : undefined
  })

  const spaSource: 'dev' | 'bundled' = window.location.protocol === 'app:' ? 'bundled' : 'dev'

  const [appInfo, setAppInfo] = useState<AppInfo | null>(null)
  const appInfoRef = useRef<AppInfo | null>(null)
  const [remoteInfo, setRemoteInfo] = useState<RemoteInfo | null>(null)
  const [status, setStatus] = useState<UpdateStatus>('idle')
  const [updating, setUpdating] = useState(false)
  const [updateStep, setUpdateStep] = useState<string | null>(null)
  const [updateError, setUpdateError] = useState<string | null>(null)
  const [buildEvents, setBuildEvents] = useState<ElectronStreamCheckEvent[]>([])
  const [streaming, setStreaming] = useState(false)

  const streamCloseRef = useRef<(() => void) | null>(null)
  // Spec §2.2: every dev request belongs to a "source generation". A source
  // change (host / token) bumps it; anything that started under an older
  // generation drops its result instead of painting the new host's view.
  const sourceGenRef = useRef(0)
  const daemonTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Daemon rebuild state
  const [daemonCheck, setDaemonCheck] = useState<DaemonCheck | null>(null)
  const [daemonLog, setDaemonLog] = useState<string[]>([])
  const [daemonPhase, setDaemonPhase] = useState<DaemonPhase>('idle')
  const [daemonError, setDaemonError] = useState<string | null>(null)

  useEffect(() => { appInfoRef.current = appInfo }, [appInfo])

  const daemonAuthHeaders = useCallback((): HeadersInit => {
    return token ? { Authorization: `Bearer ${token}` } : {}
  }, [token])

  const checkDaemon = useCallback(async () => {
    if (!daemonBase) return
    const gen = sourceGenRef.current
    setDaemonPhase('checking')
    setDaemonError(null)
    try {
      const res = await fetch(`${daemonBase}/api/dev/daemon/check`, { headers: daemonAuthHeaders() })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as DaemonCheck
      if (gen !== sourceGenRef.current) return
      setDaemonCheck(data)
      setDaemonPhase('idle')
    } catch (err) {
      if (gen !== sourceGenRef.current) return
      setDaemonError(err instanceof Error ? err.message : String(err))
      setDaemonPhase('error')
    }
  }, [daemonBase, daemonAuthHeaders])

  const rebuildDaemon = useCallback(async () => {
    if (!daemonBase) return
    const gen = sourceGenRef.current
    setDaemonPhase('rebuilding')
    setDaemonLog([])
    setDaemonError(null)

    let encounteredError = false

    try {
      const res = await fetch(`${daemonBase}/api/dev/daemon/rebuild`, {
        method: 'POST',
        headers: daemonAuthHeaders(),
      })
      if (gen !== sourceGenRef.current) { void res.body?.cancel().catch(() => {}); return }
      if (res.status === 409) {
        setDaemonError('Rebuild already in progress')
        setDaemonPhase('error')
        return
      }
      if (!res.ok || !res.body) {
        setDaemonError(`HTTP ${res.status}`)
        setDaemonPhase('error')
        return
      }

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      while (true) {
        const { done, value } = await reader.read()
        if (gen !== sourceGenRef.current) { void reader.cancel().catch(() => {}); return }
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const chunks = buffer.split('\n\n')
        buffer = chunks.pop() ?? ''
        for (const chunk of chunks) {
          if (!chunk.startsWith('data: ')) continue
          let ev: DaemonEvent
          try {
            ev = JSON.parse(chunk.slice(6)) as DaemonEvent
          } catch {
            continue
          }
          switch (ev.type) {
            case 'log':
              if (ev.line) setDaemonLog((prev) => [...prev, ev.line!])
              break
            case 'error':
              setDaemonLog((prev) => [...prev, `ERROR: ${ev.message ?? ''}`])
              setDaemonError(ev.message ?? 'build failed')
              setDaemonPhase('error')
              encounteredError = true
              break
            case 'success': {
              const hashSuffix = ev.new_hash ? ` (${ev.new_hash})` : ''
              setDaemonLog((prev) => [...prev, `✓ ${t('settings.dev.daemon.build_complete')}${hashSuffix}`])
              break
            }
            case 'restarting':
              setDaemonPhase('restarting')
              break
          }
        }
      }
      // Stream ended. If we saw 'restarting', the daemon is exec'ing itself; WS will disconnect.
      // After a brief delay, re-check to confirm new hash.
      if (!encounteredError) {
        daemonTimerRef.current = setTimeout(() => {
          daemonTimerRef.current = null
          if (gen === sourceGenRef.current) void checkDaemon()
        }, 3000)
      }
    } catch (err) {
      if (gen !== sourceGenRef.current) return
      setDaemonError(err instanceof Error ? err.message : String(err))
      setDaemonPhase('error')
    }
  }, [daemonBase, daemonAuthHeaders, checkDaemon, t])

  const closeStream = useCallback(() => {
    streamCloseRef.current?.()
    streamCloseRef.current = null
    setStreaming(false)
  }, [])

  // Source change: wipe everything the previous host produced before the
  // check effects below fire (spec §2.2 steps 1–3).
  useEffect(() => {
    sourceGenRef.current += 1
    if (daemonTimerRef.current) { clearTimeout(daemonTimerRef.current); daemonTimerRef.current = null }
    closeStream()
    setRemoteInfo(null)
    setStatus('idle')
    setUpdateError(null)
    setBuildEvents([])
    setDaemonCheck(null)
    setDaemonLog([])
    setDaemonError(null)
    setDaemonPhase('idle')
  }, [daemonBase, token, closeStream])

  // Load daemon status on mount / host change
  useEffect(() => {
    void checkDaemon()
  }, [checkDaemon])

  const resolveFinalStatus = useCallback((check: RemoteInfo) => {
    if (check.buildError) {
      setStatus('error')
      setUpdateError(check.buildError)
      return
    }
    const ai = appInfoRef.current
    if (ai && (check.electronHash !== ai.electronHash || check.spaHash !== ai.spaHash)) {
      setStatus('update_available')
    } else {
      setStatus('up_to_date')
    }
  }, [])

  const checkUpdate = useCallback(() => {
    if (!daemonBase) return
    const gen = sourceGenRef.current
    closeStream()
    setStatus('checking')
    setUpdateError(null)
    setBuildEvents([])
    setStreaming(true)

    const close = window.electronAPI!.streamCheck(daemonBase, token, (ev) => {
      if (gen !== sourceGenRef.current) return
      switch (ev.type) {
        case 'check':
          if (!ev.check) return
          setRemoteInfo(ev.check)
          setStatus(ev.check.building ? 'building' : 'checking')
          return
        case 'phase':
        case 'stdout':
        case 'stderr':
          setBuildEvents((prev) => [...prev, ev])
          return
        case 'error':
          setBuildEvents((prev) => [...prev, ev])
          setStatus('error')
          setUpdateError(ev.error ?? 'stream error')
          setStreaming(false)
          streamCloseRef.current = null
          return
        case 'done':
          if (ev.check) {
            setRemoteInfo(ev.check)
            resolveFinalStatus(ev.check)
          }
          setStreaming(false)
          streamCloseRef.current = null
          return
      }
    })
    streamCloseRef.current = close
  }, [daemonBase, token, closeStream, resolveFinalStatus])

  const checkUpdateRef = useRef(checkUpdate)
  useEffect(() => { checkUpdateRef.current = checkUpdate }, [checkUpdate])

  // Mount: load app info. Separate effect below re-runs the check whenever
  // the daemon host changes (daemonBase or token), which closes any stale
  // stream pointing at the previous host.
  useEffect(() => {
    window.electronAPI?.getAppInfo().then((info) => {
      setAppInfo(info)
      appInfoRef.current = info
    })
  }, [])

  useEffect(() => {
    if (!appInfo || !daemonBase) return
    checkUpdateRef.current()
  }, [appInfo, daemonBase, token])

  useEffect(() => () => closeStream(), [closeStream])
  useEffect(() => () => {
    if (daemonTimerRef.current) clearTimeout(daemonTimerRef.current)
    sourceGenRef.current += 1
  }, [])

  useEffect(() => {
    if (!window.electronAPI?.onUpdateProgress) return
    return window.electronAPI.onUpdateProgress((step) => setUpdateStep(step))
  }, [])

  const handleUpdate = () => {
    if (!daemonBase) return
    setUpdating(true)
    setUpdateStep(null)
    setUpdateError(null)
    window.electronAPI!.applyUpdate(daemonBase, token).catch((err) => {
      setUpdating(false)
      setUpdateStep(null)
      setUpdateError(err instanceof Error ? err.message : String(err))
    })
  }

  const stepLabels: Record<string, string> = {
    downloading: 'Downloading update…',
    extracting: 'Extracting…',
    applying: 'Applying update…',
  }

  const hasElectronUpdate = remoteInfo && appInfo && remoteInfo.electronHash !== appInfo.electronHash
  const hasSPAUpdate = remoteInfo && appInfo && remoteInfo.spaHash !== appInfo.spaHash
  const showLogPanel = buildEvents.length > 0 || status === 'building'
  // Spec §2.2 step 4: an app update / daemon rebuild cannot be cancelled, so
  // the source must not change mid-way.
  const sourceLocked = updating || daemonPhase === 'rebuilding' || daemonPhase === 'restarting'

  const statusText: Record<UpdateStatus, string> = {
    idle: '',
    checking: t('settings.dev.status.checking'),
    building: t('settings.dev.status.building'),
    up_to_date: t('settings.dev.status.up_to_date'),
    update_available: t('settings.dev.status.update_available'),
    error: t('settings.dev.status.error'),
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg text-text-primary">{t('settings.dev.title')}</h2>
        <p className="text-xs text-text-secondary mb-6">{t('settings.dev.desc')}</p>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label htmlFor="dev-host-picker" className="text-sm text-text-primary">{t('settings.dev.host.label')}</label>
          <select
            id="dev-host-picker"
            value={devHostId ?? ''}
            onChange={(e) => setDevHost(e.target.value || null)}
            disabled={sourceLocked}
            className="text-xs rounded bg-surface-input border border-border-default text-text-primary px-2 py-1 disabled:opacity-50"
          >
            <option value="">{t('settings.dev.host.none')}</option>
            {hostOrder.map((id) => {
              const h = hosts[id]
              return h ? <option key={id} value={id}>{`${hostLabel(id, hostLookOf(id, hosts))} (${h.ip}:${h.port})`}</option> : null
            })}
          </select>
        </div>
        {devHostId === null && (
          <div className="text-xs text-status-warning border border-status-warning/40 bg-status-warning/10 rounded p-2">
            {t('settings.dev.host.required')}
          </div>
        )}
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.spa_source')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-secondary font-mono">
              {spaSource === 'dev' ? 'Dev Server' : 'Bundled'}
            </span>
            <button
              onClick={() => {
                const target = spaSource === 'dev' ? 'bundled' : 'dev'
                window.electronAPI?.forceLoadSPA(target)?.catch((err: unknown) => {
                  const detail = typeof err === 'string' ? err : (err instanceof Error ? err.message : String(err))
                  setUpdateError(target === 'dev'
                    ? `${t('settings.dev.error.dev_unreachable')}: ${detail}`
                    : `${t('settings.dev.error.bundled_failed')}: ${detail}`)
                  setStatus('error')
                })
              }}
              className="px-2 py-0.5 text-xs rounded bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover cursor-pointer"
            >
              {spaSource === 'dev' ? t('settings.dev.btn.switch_bundled') : t('settings.dev.btn.switch_dev')}
            </button>
          </div>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.app_version')}</span>
          <span className="text-xs text-text-secondary font-mono">{appInfo?.version ?? '...'}</span>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.spa_hash')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-secondary font-mono">{appInfo?.spaHash ?? '...'}</span>
            {hasSPAUpdate && <span className="text-xs text-status-warning font-mono">→ {remoteInfo.spaHash}</span>}
          </div>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.electron_hash')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-secondary font-mono">{appInfo?.electronHash ?? '...'}</span>
            {hasElectronUpdate && <span className="text-xs text-status-warning font-mono">→ {remoteInfo.electronHash}</span>}
          </div>
        </div>
      </div>

      {remoteInfo?.requiresFullRebuild && (
        <div className="text-xs text-status-warning border border-status-warning/40 bg-status-warning/10 rounded p-2">
          {t('settings.dev.full_rebuild_hint')}
          {remoteInfo.fullRebuildReason && (
            <span className="block text-text-secondary font-mono mt-1">{remoteInfo.fullRebuildReason}</span>
          )}
        </div>
      )}

      {status !== 'idle' && (
        <div className={`text-sm ${status === 'error' ? 'text-status-error' : status === 'building' ? 'text-accent' : status === 'update_available' ? 'text-status-warning' : 'text-text-secondary'}`}>
          {status === 'error' && updateError ? updateError : statusText[status]}
        </div>
      )}

      {showLogPanel && (
        <DevBuildLogPanel events={buildEvents} streaming={streaming} />
      )}

      {updating && updateStep && (
        <div className="text-sm text-accent font-mono">
          {stepLabels[updateStep] ?? updateStep}
        </div>
      )}

      <div className="flex gap-2">
        <button
          onClick={checkUpdate}
          disabled={!appInfo || !daemonBase || status === 'checking' || status === 'building'}
          className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
        >
          {t('settings.dev.btn.check')}
        </button>
        {(hasElectronUpdate || hasSPAUpdate) && (
          <button
            onClick={handleUpdate}
            disabled={updating || !daemonBase}
            className="px-3 py-1.5 text-xs rounded-md bg-accent text-text-inverse hover:bg-accent-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
          >
            {updating ? t('settings.dev.btn.updating') : t('settings.dev.btn.update_app')}
          </button>
        )}
        {hasSPAUpdate && !hasElectronUpdate && (
          <button
            onClick={() => window.electronAPI?.reloadSPA() ?? window.location.reload()}
            className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover cursor-pointer"
          >
            {t('settings.dev.btn.reload_spa')}
          </button>
        )}
      </div>

      <div className="pt-6 border-t border-border-default">
        <h3 className="text-sm font-semibold text-text-primary mb-3">{t('settings.dev.daemon.heading')}</h3>
        {daemonCheck && (
          <div className="space-y-1 mb-3 text-xs text-text-secondary">
            <div className="flex items-center justify-between">
              <span>{t('settings.dev.daemon.current_hash')}</span>
              <span className="font-mono text-text-primary">{daemonCheck.current_hash}</span>
            </div>
            <div className="flex items-center justify-between">
              <span>{t('settings.dev.daemon.latest_hash')}</span>
              <div className="flex items-center gap-2">
                <span className="font-mono">{daemonCheck.latest_hash || '-'}</span>
                {daemonCheck.available && (
                  <span className="text-status-warning">{t('settings.dev.daemon.update_available')}</span>
                )}
              </div>
            </div>
          </div>
        )}

        {daemonPhase === 'error' && daemonError && (
          <div className="text-xs text-status-error mb-3">{daemonError}</div>
        )}
        {daemonPhase === 'restarting' && (
          <div className="text-xs text-accent mb-3">{t('settings.dev.daemon.restarting')}</div>
        )}

        {daemonLog.length > 0 && (
          <pre className="bg-surface-input border border-border-default rounded p-2 mb-3 text-xs font-mono max-h-60 overflow-y-auto whitespace-pre-wrap">
            {daemonLog.join('\n')}
          </pre>
        )}

        <div className="flex gap-2">
          <button
            onClick={() => void checkDaemon()}
            disabled={!daemonBase || daemonPhase === 'checking' || daemonPhase === 'rebuilding' || daemonPhase === 'restarting'}
            className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
          >
            {t('settings.dev.daemon.check')}
          </button>
          <button
            onClick={() => void rebuildDaemon()}
            disabled={!daemonBase || daemonPhase === 'rebuilding' || daemonPhase === 'restarting'}
            className="px-3 py-1.5 text-xs rounded-md bg-accent text-text-inverse hover:bg-accent-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
          >
            {t('settings.dev.daemon.rebuild')}
          </button>
        </div>
      </div>

      <LocalDaemonSection daemonBase={daemonBase} token={token} latestHash={daemonCheck?.latest_hash ?? null} refreshKey={daemonCheck} />
    </div>
  )
}

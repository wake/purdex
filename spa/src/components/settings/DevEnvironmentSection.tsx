import { useState, useEffect, useCallback, useRef } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'

type UpdateStatus = 'idle' | 'checking' | 'building' | 'up_to_date' | 'update_available' | 'error'

interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
}

type RemoteInfo = Awaited<ReturnType<NonNullable<typeof window.electronAPI>['checkUpdate']>>

export function DevEnvironmentSection() {
  const t = useI18nStore((s) => s.t)
  const firstHostId = useHostStore((s) => s.hostOrder[0] ?? '')
  const daemonBase = useHostStore((s) => s.getDaemonBase(firstHostId))
  const token = useHostStore((s) => firstHostId ? s.hosts[firstHostId]?.token : undefined)

  const spaSource: 'dev' | 'bundled' = window.location.protocol === 'app:' ? 'bundled' : 'dev'

  const [appInfo, setAppInfo] = useState<AppInfo | null>(null)
  const appInfoRef = useRef<AppInfo | null>(null)
  const [remoteInfo, setRemoteInfo] = useState<RemoteInfo | null>(null)
  const [status, setStatus] = useState<UpdateStatus>('idle')
  const [updating, setUpdating] = useState(false)
  const [updateStep, setUpdateStep] = useState<string | null>(null)
  const [updateError, setUpdateError] = useState<string | null>(null)

  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  useEffect(() => () => stopPolling(), [stopPolling])

  useEffect(() => { appInfoRef.current = appInfo }, [appInfo])

  const processCheckResult = useCallback(
    (remote: RemoteInfo) => {
      setRemoteInfo(remote)
      if (remote.buildError) {
        setUpdateError(remote.buildError)
        setStatus('error')
        return
      }
      const ai = appInfoRef.current
      if (remote.electronHash !== ai?.electronHash || remote.spaHash !== ai?.spaHash) {
        setStatus('update_available')
      } else {
        setStatus('up_to_date')
      }
    },
    [], // deps now empty — reads appInfo from ref
  )

  const checkUpdate = useCallback(async () => {
    setStatus('checking')
    setUpdateError(null)
    try {
      const remote = await window.electronAPI!.checkUpdate(daemonBase, token)
      if (remote.building) {
        setStatus('building')
        if (!pollRef.current) {
          pollRef.current = setInterval(async () => {
            try {
              const r = await window.electronAPI!.checkUpdate(daemonBase, token)
              if (!r.building) {
                stopPolling()
                processCheckResult(r)
              }
            } catch {
              stopPolling()
              setStatus('error')
            }
          }, 3000)
        }
      } else {
        stopPolling()
        processCheckResult(remote)
      }
    } catch {
      setStatus('error')
    }
  }, [daemonBase, token, stopPolling, processCheckResult])

  // Keep ref in sync so the mount effect can call the latest version
  const checkUpdateRef = useRef(checkUpdate)
  useEffect(() => { checkUpdateRef.current = checkUpdate }, [checkUpdate])

  // Fetch appInfo on mount, then auto-check for updates (event-driven, not effect cascade)
  useEffect(() => {
    window.electronAPI?.getAppInfo().then((info) => {
      setAppInfo(info)
      appInfoRef.current = info
      checkUpdateRef.current()
    })
  }, [])

  useEffect(() => {
    if (!window.electronAPI?.onUpdateProgress) return
    return window.electronAPI.onUpdateProgress((step) => setUpdateStep(step))
  }, [])

  const handleUpdate = () => {
    setUpdating(true)
    setUpdateStep(null)
    setUpdateError(null)
    // Fire and forget — app.exit(0) kills the process before the promise resolves
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
    restarting: 'Restarting…',
  }

  const hasElectronUpdate = remoteInfo && appInfo && remoteInfo.electronHash !== appInfo.electronHash
  const hasSPAUpdate = remoteInfo && appInfo && remoteInfo.spaHash !== appInfo.spaHash

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

      {status !== 'idle' && (
        <div className={`text-sm ${status === 'error' ? 'text-status-error' : status === 'building' ? 'text-accent' : status === 'update_available' ? 'text-status-warning' : 'text-text-secondary'}`}>
          {status === 'error' && updateError ? updateError : statusText[status]}
        </div>
      )}

      {updating && updateStep && (
        <div className="text-sm text-accent font-mono">
          {stepLabels[updateStep] ?? updateStep}
        </div>
      )}

      <div className="flex gap-2">
        <button
          onClick={checkUpdate}
          disabled={!appInfo || status === 'checking' || status === 'building'}
          className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
        >
          {t('settings.dev.btn.check')}
        </button>
        {(hasElectronUpdate || hasSPAUpdate) && (
          <button
            onClick={handleUpdate}
            disabled={updating}
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
    </div>
  )
}

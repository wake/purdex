import { useState, useEffect, useCallback } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'

type UpdateStatus = 'idle' | 'checking' | 'up_to_date' | 'update_available' | 'error'

interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
}

interface RemoteInfo {
  version: string
  spaHash: string
  electronHash: string
}

export function DevEnvironmentSection() {
  const t = useI18nStore((s) => s.t)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const daemonBase = getDaemonBase('local')

  const [appInfo, setAppInfo] = useState<AppInfo | null>(null)
  const [remoteInfo, setRemoteInfo] = useState<RemoteInfo | null>(null)
  const [status, setStatus] = useState<UpdateStatus>('idle')
  const [updating, setUpdating] = useState(false)
  const [updateStep, setUpdateStep] = useState<string | null>(null)
  const [updateError, setUpdateError] = useState<string | null>(null)

  useEffect(() => {
    window.electronAPI?.getAppInfo().then(setAppInfo)
  }, [])

  const checkUpdate = useCallback(async () => {
    setStatus('checking')
    try {
      const remote = await window.electronAPI!.checkUpdate(daemonBase)
      setRemoteInfo(remote)
      if (remote.electronHash !== appInfo?.electronHash || remote.spaHash !== appInfo?.spaHash) {
        setStatus('update_available')
      } else {
        setStatus('up_to_date')
      }
    } catch {
      setStatus('error')
    }
  }, [daemonBase, appInfo])

  useEffect(() => {
    if (appInfo) checkUpdate()
  }, [appInfo, checkUpdate])

  useEffect(() => {
    if (!window.electronAPI?.onUpdateProgress) return
    return window.electronAPI.onUpdateProgress((step) => setUpdateStep(step))
  }, [])

  const handleUpdate = () => {
    setUpdating(true)
    setUpdateStep(null)
    setUpdateError(null)
    // Fire and forget — app.exit(0) kills the process before the promise resolves
    window.electronAPI!.applyUpdate(daemonBase).catch((err) => {
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
        <div className={`text-xs ${status === 'error' ? 'text-status-error' : status === 'update_available' ? 'text-status-warning' : 'text-text-secondary'}`}>
          {statusText[status]}
        </div>
      )}

      {updating && updateStep && (
        <div className="text-xs text-text-secondary font-mono">
          {stepLabels[updateStep] ?? updateStep}
        </div>
      )}

      {updateError && (
        <div className="text-xs text-status-error">
          Error: {updateError}
        </div>
      )}

      <div className="flex gap-2">
        <button
          onClick={checkUpdate}
          disabled={!appInfo || status === 'checking'}
          className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
        >
          {t('settings.dev.btn.check')}
        </button>
        {hasElectronUpdate && (
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

import { useEffect, useState } from 'react'
import { ArrowsClockwise, Trash, File } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { fetchUploadStats, fetchUploadFiles, deleteUploadFile, deleteUploadSession, deleteAllUploads } from '../../lib/host-api'

interface Props {
  hostId: string
}

interface UploadStats {
  dir: string
  total_size: number
  file_count: number
}

interface UploadFile {
  session: string
  name: string
  size: number
  modified: string
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function UploadSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const host = useHostStore((s) => s.hosts[hostId])
  const isOffline = runtime != null && runtime.status !== 'connected'
  const [stats, setStats] = useState<UploadStats | null>(null)
  const [files, setFiles] = useState<UploadFile[]>([])
  const [loading, setLoading] = useState(true)
  const [confirmClearAll, setConfirmClearAll] = useState(false)

  const refresh = async () => {
    setLoading(true)
    try {
      const [statsRes, filesRes] = await Promise.all([
        fetchUploadStats(hostId),
        fetchUploadFiles(hostId),
      ])
      if (statsRes.ok) setStats(await statsRes.json())
      if (filesRes.ok) setFiles(await filesRes.json())
    } catch { /* ignore */ }
    setLoading(false)
  }

  useEffect(() => {
    let cancelled = false
    Promise.all([fetchUploadStats(hostId), fetchUploadFiles(hostId)])
      .then(([statsRes, filesRes]) =>
        Promise.all([
          statsRes.ok ? statsRes.json() : null,
          filesRes.ok ? filesRes.json() : null,
        ]),
      )
      .then(([statsData, filesData]) => {
        if (cancelled) return
        if (statsData) setStats(statsData)
        if (filesData) setFiles(filesData)
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [hostId])

  if (!host) return null

  const handleDeleteFile = async (session: string, filename: string) => {
    try {
      await deleteUploadFile(hostId, session, filename)
    } catch { /* ignore */ }
    await refresh()
  }

  const handleDeleteSession = async (session: string) => {
    try {
      await deleteUploadSession(hostId, session)
    } catch { /* ignore */ }
    await refresh()
  }

  const handleClearAll = async () => {
    try {
      await deleteAllUploads(hostId)
    } catch { /* ignore */ }
    setConfirmClearAll(false)
    await refresh()
  }

  // Group files by session
  const grouped = files.reduce<Record<string, UploadFile[]>>((acc, f) => {
    ;(acc[f.session] ??= []).push(f)
    return acc
  }, {})

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.uploads')}</h2>
        <div className="flex items-center gap-2">
          <button
            onClick={refresh}
            disabled={isOffline || loading}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
          >
            <ArrowsClockwise size={14} className={loading ? 'animate-spin' : ''} />
            {t('hosts.refresh')}
          </button>
          {files.length > 0 && (
            <button
              onClick={() => setConfirmClearAll(true)}
              disabled={isOffline}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-red-500/10 text-red-400 border border-red-500/30 cursor-pointer disabled:opacity-50"
            >
              <Trash size={14} />
              {t('hosts.clear_all')}
            </button>
          )}
        </div>
      </div>

      {confirmClearAll && (
        <div className="mb-4 p-3 bg-red-500/10 border border-red-500/30 rounded">
          <p className="text-xs text-red-400 mb-2">{t('hosts.confirm_clear_all')}</p>
          <div className="flex gap-2">
            <button
              onClick={handleClearAll}
              className="px-3 py-1 rounded text-xs bg-red-500 text-white cursor-pointer"
            >
              {t('hosts.clear_all')}
            </button>
            <button
              onClick={() => setConfirmClearAll(false)}
              className="px-3 py-1 rounded text-xs bg-surface-secondary text-text-secondary cursor-pointer"
            >
              {t('common.cancel')}
            </button>
          </div>
        </div>
      )}

      {/* Stats overview */}
      {stats && (
        <div className="p-4 bg-surface-secondary rounded-lg border border-border-subtle mb-4">
          <div className="grid grid-cols-3 gap-4 text-center">
            <div>
              <p className="text-xs text-text-muted">{t('hosts.upload_dir')}</p>
              <p className="text-sm text-text-primary font-mono truncate">{stats.dir}</p>
            </div>
            <div>
              <p className="text-xs text-text-muted">{t('hosts.total_size')}</p>
              <p className="text-sm text-text-primary">{formatBytes(stats.total_size)}</p>
            </div>
            <div>
              <p className="text-xs text-text-muted">{t('hosts.file_count')}</p>
              <p className="text-sm text-text-primary">{stats.file_count}</p>
            </div>
          </div>
        </div>
      )}

      {/* Files grouped by session */}
      {Object.keys(grouped).length === 0 ? (
        <p className="text-sm text-text-muted">{t('hosts.no_uploads')}</p>
      ) : (
        <div className="space-y-3">
          {Object.entries(grouped).map(([session, sessionFiles]) => (
            <div key={session} className="border border-border-subtle rounded-lg overflow-hidden">
              <div className="flex items-center justify-between px-3 py-2 bg-surface-tertiary">
                <span className="text-xs font-semibold text-text-secondary">{session}</span>
                <button
                  onClick={() => handleDeleteSession(session)}
                  disabled={isOffline}
                  className="text-xs text-red-400 hover:text-red-300 cursor-pointer disabled:opacity-50"
                >
                  {t('hosts.clear_session')}
                </button>
              </div>
              {sessionFiles.map((file) => (
                <div key={file.name} className="flex items-center gap-3 px-3 py-2 border-t border-border-subtle hover:bg-surface-secondary/30">
                  <File size={14} className="text-text-muted shrink-0" />
                  <span className="text-sm text-text-primary truncate flex-1">{file.name}</span>
                  <span className="text-xs text-text-muted shrink-0">{formatBytes(file.size)}</span>
                  <button
                    onClick={() => handleDeleteFile(session, file.name)}
                    disabled={isOffline}
                    className="p-1 rounded hover:bg-surface-tertiary text-text-muted hover:text-red-400 cursor-pointer disabled:opacity-50"
                  >
                    <Trash size={12} />
                  </button>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

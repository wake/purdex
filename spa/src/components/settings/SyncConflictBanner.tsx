import { useMemo, useState } from 'react'
import { Warning, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ConflictItem, SyncBundle, ResolvedFields } from '../../lib/sync/types'

interface Props {
  conflicts: ConflictItem[]
  remoteBundle: SyncBundle
  pendingAt: number
  onResolve: (resolved: ResolvedFields) => void
  onDismiss: () => void
}

const STALE_MS = 24 * 60 * 60 * 1000

function stringify(v: unknown): string {
  if (v === undefined) return 'undefined'
  try { return JSON.stringify(v) } catch { return String(v) }
}

export function SyncConflictBanner({ conflicts, remoteBundle, pendingAt, onResolve, onDismiss }: Props) {
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState(false)
  const [choices, setChoices] = useState<Record<string, 'local' | 'remote'>>({})

  const [now] = useState<number>(() => Date.now())
  const stale = useMemo(() => now - pendingAt > STALE_MS, [now, pendingAt])
  const total = conflicts.length
  const selected = conflicts.filter((c) => choices[`${c.contributor}::${c.field}`]).length
  const allDone = selected === total && total > 0

  const rowKey = (c: ConflictItem) => `${c.contributor}::${c.field}`

  const selectRow = (key: string, choice: 'local' | 'remote') => {
    setChoices((prev) => ({ ...prev, [key]: choice }))
  }

  const selectAll = (choice: 'local' | 'remote') => {
    const next: Record<string, 'local' | 'remote'> = {}
    for (const c of conflicts) next[rowKey(c)] = choice
    setChoices(next)
  }

  const remoteTime = useMemo(
    () => new Date(remoteBundle.timestamp).toLocaleString(),
    [remoteBundle.timestamp],
  )

  const handleApply = () => {
    const resolved: ResolvedFields = {}
    for (const c of conflicts) {
      const choice = choices[rowKey(c)]
      if (choice) resolved[c.field] = choice
    }
    onResolve(resolved)
  }

  if (!expanded) {
    return (
      <div className="mb-4 rounded border border-yellow-500/40 bg-yellow-500/10 text-xs text-text-primary">
        <div className="flex items-center gap-2 px-3 py-2">
          <Warning size={14} className="text-yellow-500 shrink-0" />
          <span className="flex-1">{t('settings.sync.conflict.banner', { count: total })}</span>
          <button
            className="px-2 py-1 rounded text-yellow-600 hover:bg-yellow-500/20"
            onClick={() => setExpanded(true)}
          >
            {t('settings.sync.conflict.viewDetails')}
          </button>
        </div>
        {stale && (
          <div className="px-3 pb-2 text-text-secondary">
            {t('settings.sync.conflict.stale')}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="mb-4 rounded border border-yellow-500/40 bg-yellow-500/5 text-xs text-text-primary">
      <div className="flex items-center gap-2 px-3 py-2 border-b border-yellow-500/30">
        <Warning size={14} className="text-yellow-500 shrink-0" />
        <span className="flex-1">{t('settings.sync.conflict.banner', { count: total })}</span>
        <button
          className="p-1 rounded hover:bg-yellow-500/10"
          onClick={() => setExpanded(false)}
          title={t('settings.sync.conflict.collapse')}
        >
          <X size={12} />
        </button>
      </div>

      {stale && (
        <div className="px-3 py-2 text-text-secondary border-b border-yellow-500/20">
          {t('settings.sync.conflict.stale')}
        </div>
      )}

      <div className="px-3 py-2 flex flex-col gap-3">
        {conflicts.map((c) => {
          const key = rowKey(c)
          const current = choices[key]
          return (
            <div key={key} className="flex flex-col gap-1">
              <div className="font-mono text-text-secondary">
                {c.contributor}.{c.field}
              </div>
              <div className="text-text-secondary">
                {t('settings.sync.conflict.lastSynced', {
                  value: stringify(c.lastSynced),
                  device: remoteBundle.device,
                  time: remoteTime,
                })}
              </div>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name={key}
                  checked={current === 'local'}
                  onChange={() => selectRow(key, 'local')}
                />
                <span>{t('settings.sync.conflict.local')}:</span>
                <code className="text-text-primary">{stringify(c.local)}</code>
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name={key}
                  checked={current === 'remote'}
                  onChange={() => selectRow(key, 'remote')}
                />
                <span>{t('settings.sync.conflict.remote', { device: c.remote.device })}:</span>
                <code className="text-text-primary">{stringify(c.remote.value)}</code>
              </label>
            </div>
          )
        })}
      </div>

      <div className="px-3 py-2 flex items-center gap-2 border-t border-yellow-500/20">
        <button
          className="px-2 py-1 rounded border border-border-default hover:border-border-active text-text-secondary"
          onClick={() => selectAll('local')}
        >
          {t('settings.sync.conflict.keepAllLocal')}
        </button>
        <button
          className="px-2 py-1 rounded border border-border-default hover:border-border-active text-text-secondary"
          onClick={() => selectAll('remote')}
        >
          {t('settings.sync.conflict.useAllRemote')}
        </button>
      </div>

      <div className="px-3 py-2 flex items-center justify-end gap-2 border-t border-yellow-500/20">
        <button
          className="px-3 py-1 rounded text-text-secondary hover:text-text-primary"
          onClick={onDismiss}
        >
          {t('settings.sync.conflict.cancel')}
        </button>
        <button
          className="px-3 py-1 rounded bg-yellow-500/20 text-yellow-700 font-medium disabled:opacity-40 disabled:cursor-not-allowed"
          onClick={handleApply}
          disabled={!allDone}
        >
          {t('settings.sync.conflict.apply', { selected, total })}
        </button>
      </div>
    </div>
  )
}

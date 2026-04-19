import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { useStatuslineInstall } from '../../hooks/useStatuslineInstall'
import { StatuslineConflictDialog } from './StatuslineConflictDialog'

interface Props {
  hostId: string
  extensionId: 'statusline'
}

export function AgentExtensionRow({ hostId, extensionId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const isOffline = !runtime || runtime.status !== 'connected'
  const { state, phase, error, install, remove } = useStatuslineInstall(hostId)
  const [showConflict, setShowConflict] = useState(false)

  // Gate button by mode, not by installed flag:
  // pdx/wrapped → managed by us → show Remove
  // none/unmanaged → show Install (unmanaged opens conflict dialog)
  const isManaged = state.mode === 'pdx' || state.mode === 'wrapped'

  const handleInstall = () => {
    if (state.mode === 'unmanaged') {
      setShowConflict(true)
    } else {
      void install('pdx')
    }
  }

  const handleWrap = () => {
    setShowConflict(false)
    void install('wrap', state.rawCommand ?? state.innerCommand ?? '')
  }

  const handleRemove = () => {
    if (window.confirm(t('hosts.extensions.confirm_remove'))) void remove()
  }

  const badge = (() => {
    switch (state.mode) {
      case 'pdx':
        return t('hosts.extensions.installed')
      case 'wrapped':
        return t('hosts.extensions.installed_wrap')
      case 'unmanaged':
        return t('hosts.extensions.unmanaged')
      default:
        return t('hosts.extensions.not_installed')
    }
  })()

  return (
    <>
      <div className="flex items-center justify-between py-2">
        <div className="flex items-center gap-2">
          <span className="text-sm">{t(`hosts.extensions.${extensionId}`)}</span>
          <span className="text-xs text-text-muted">{badge}</span>
        </div>
        <div className="flex gap-2">
          {isManaged ? (
            <button
              onClick={handleRemove}
              disabled={isOffline || phase === 'loading'}
              className="px-3 py-1 rounded text-xs bg-red-500/10 text-red-400 border border-red-500/30 cursor-pointer disabled:opacity-50"
            >
              {t('hosts.extensions.remove')}
            </button>
          ) : (
            <button
              onClick={handleInstall}
              disabled={isOffline || phase === 'loading'}
              className="px-3 py-1 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
            >
              {t('hosts.extensions.install')}
            </button>
          )}
        </div>
      </div>
      {error && <p className="text-xs text-red-400">{error}</p>}
      {showConflict && (
        <StatuslineConflictDialog
          existingCommand={state.rawCommand ?? state.innerCommand ?? ''}
          onWrap={handleWrap}
          onCancel={() => setShowConflict(false)}
        />
      )}
    </>
  )
}

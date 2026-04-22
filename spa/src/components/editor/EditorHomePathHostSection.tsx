import { useState, useEffect, useId } from 'react'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props {
  ctx: SettingsContextFor<'host'>
}

export function EditorHomePathHostSection({ ctx }: Props) {
  if (ctx.scope !== 'host') return null
  return <Body hostId={ctx.hostId} />
}

function Body({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const stored = useHostSettingsStore((s) => s.hosts[hostId]?.editor?.homePath)
  const storedStr = typeof stored === 'string' ? stored : ''
  const [draft, setDraft] = useState(storedStr)
  const inputId = useId()

  useEffect(() => { setDraft(storedStr) }, [storedStr])

  const commit = () => {
    const trimmed = draft.trim()
    // Reflect normalization in the input even when the trimmed value matches
    // the stored one — otherwise trailing whitespace lingers in the UI while
    // persisted state is clean.
    if (trimmed !== draft) setDraft(trimmed)
    if (trimmed === storedStr) return
    if (trimmed === '') {
      useHostSettingsStore.getState().clearModule(hostId, 'editor')
      return
    }
    useHostSettingsStore.getState().set(hostId, 'editor', { homePath: trimmed })
  }

  const clear = () => {
    setDraft('')
    useHostSettingsStore.getState().clearModule(hostId, 'editor')
  }

  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-text-muted">
        {t('editor.settings.home_path.description')}
      </p>
      <div className="flex gap-2">
        <input
          id={inputId}
          type="text"
          aria-label={t('editor.settings.home_path.host')}
          placeholder={t('editor.settings.home_path.placeholder')}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }}
          className="flex-1 px-3 py-2 rounded-md bg-surface-muted text-text-primary border border-border-subtle focus:border-accent focus:outline-none text-sm"
        />
        <button
          type="button"
          onClick={clear}
          disabled={storedStr === ''}
          className="px-3 py-2 rounded-md border border-border-subtle text-sm text-text-secondary hover:bg-surface-muted disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        >
          {t('editor.settings.home_path.clear')}
        </button>
      </div>
    </div>
  )
}

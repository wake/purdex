import { useState, useEffect, useId } from 'react'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props {
  ctx: SettingsContextFor<'workspace'>
}

export function EditorHomePathWorkspaceSection({ ctx }: Props) {
  if (ctx.scope !== 'workspace') return null
  return <Body workspaceId={ctx.workspaceId} />
}

function Body({ workspaceId }: { workspaceId: string }) {
  const t = useI18nStore((s) => s.t)
  const stored = useWorkspaceSettingsStore((s) => s.workspaces[workspaceId]?.editor?.homePath)
  const storedStr = typeof stored === 'string' ? stored : ''
  const [draft, setDraft] = useState(storedStr)
  const inputId = useId()

  useEffect(() => { setDraft(storedStr) }, [storedStr])

  const commit = () => {
    const trimmed = draft.trim()
    if (trimmed === storedStr) return
    if (trimmed === '') {
      useWorkspaceSettingsStore.getState().clearModule(workspaceId, 'editor')
      return
    }
    useWorkspaceSettingsStore.getState().set(workspaceId, 'editor', { homePath: trimmed })
  }

  const clear = () => {
    setDraft('')
    useWorkspaceSettingsStore.getState().clearModule(workspaceId, 'editor')
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
          aria-label={t('editor.settings.home_path.workspace')}
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

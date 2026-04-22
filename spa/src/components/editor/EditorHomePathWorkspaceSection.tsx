import { useState, useEffect, useId, useRef } from 'react'
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
  const focusedRef = useRef(false)

  // R2 codex: skip the sync while the user is editing so an external store
  // update (e.g. BroadcastChannel from another window) can't clobber the
  // in-progress draft. Focused edits reconcile against the latest store
  // value in commit(), which re-reads getState() at blur.
  useEffect(() => {
    if (focusedRef.current) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(storedStr)
  }, [storedStr])

  const commit = () => {
    const trimmed = draft.trim()
    // Reflect normalization in the input even when the trimmed value matches
    // the stored one — otherwise trailing whitespace lingers in the UI while
    // persisted state is clean.
    if (trimmed !== draft) setDraft(trimmed)
    // Re-read the latest stored value — the snapshot captured at render can
    // be stale if the store changed during editing (R2 codex).
    const live = useWorkspaceSettingsStore.getState().workspaces[workspaceId]?.editor?.homePath
    const liveStr = typeof live === 'string' ? live : ''
    if (trimmed === liveStr) return
    if (trimmed === '') {
      // R2 codex: only drop the homePath key — `clearModule` would wipe any
      // sibling editor settings (wrap / tabSize / ...) stored in the same bucket.
      useWorkspaceSettingsStore.getState().removeKey(workspaceId, 'editor', 'homePath')
      return
    }
    useWorkspaceSettingsStore.getState().set(workspaceId, 'editor', { homePath: trimmed })
  }

  const clear = () => {
    setDraft('')
    useWorkspaceSettingsStore.getState().removeKey(workspaceId, 'editor', 'homePath')
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
          onFocus={() => { focusedRef.current = true }}
          onBlur={() => { focusedRef.current = false; commit() }}
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

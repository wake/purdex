import { useState, useEffect, useId, useRef } from 'react'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkspaceSettingsDraft } from '../../features/workspace/components/WorkspaceSettingsDraftContext'
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
  const draftContext = useWorkspaceSettingsDraft()
  const stored = useWorkspaceSettingsStore((s) => s.workspaces[workspaceId]?.editor?.homePath)
  const storedStr = typeof stored === 'string' ? stored : ''
  const [draft, setDraft] = useState(storedStr)
  const inputId = useId()
  const draftFieldId = `workspace:${workspaceId}:editor:homePath`
  const draftRef = useRef(draft)
  const focusedRef = useRef(false)
  const dirtyRef = useRef(false)

  // R2 codex: skip the sync while the user is editing so an external store
  // update (e.g. BroadcastChannel from another window) can't clobber the
  // in-progress draft. Save reconciles against the latest store value.
  useEffect(() => {
    if (focusedRef.current) return
    setDraft(storedStr)
  }, [storedStr])

  useEffect(() => {
    draftRef.current = draft
    draftContext?.setDirty(draftFieldId, draft !== storedStr)
  }, [draft, storedStr, draftContext, draftFieldId])

  const handleBlur = () => {
    focusedRef.current = false
    if (!dirtyRef.current) {
      // R3 codex: focus-without-edit must not push the stale draft back to
      // the store. If the store changed during the focus window (e.g.
      // another window wrote via BroadcastChannel), pull the latest value
      // into the input instead of clobbering it.
      const live = useWorkspaceSettingsStore.getState().workspaces[workspaceId]?.editor?.homePath
      setDraft(typeof live === 'string' ? live : '')
    }
  }

  const commit = () => {
    const currentDraft = draftRef.current
    const trimmed = currentDraft.trim()
    // Reflect normalization in the input even when the trimmed value matches
    // the stored one — otherwise trailing whitespace lingers in the UI while
    // persisted state is clean.
    if (trimmed !== currentDraft) setDraft(trimmed)
    // Re-read the latest stored value — the snapshot captured at render can
    // be stale if the store changed during editing (R2 codex).
    const live = useWorkspaceSettingsStore.getState().workspaces[workspaceId]?.editor?.homePath
    const liveStr = typeof live === 'string' ? live : ''
    if (trimmed === liveStr) {
      dirtyRef.current = false
      draftContext?.setDirty(draftFieldId, false)
      return
    }
    if (trimmed === '') {
      // R2 codex: only drop the homePath key — `clearModule` would wipe any
      // sibling editor settings (wrap / tabSize / ...) stored in the same bucket.
      useWorkspaceSettingsStore.getState().removeKey(workspaceId, 'editor', 'homePath')
      dirtyRef.current = false
      draftContext?.setDirty(draftFieldId, false)
      return
    }
    useWorkspaceSettingsStore.getState().set(workspaceId, 'editor', { homePath: trimmed })
    dirtyRef.current = false
    draftContext?.setDirty(draftFieldId, false)
  }

  const cancel = () => {
    const live = useWorkspaceSettingsStore.getState().workspaces[workspaceId]?.editor?.homePath
    setDraft(typeof live === 'string' ? live : '')
    dirtyRef.current = false
    draftContext?.setDirty(draftFieldId, false)
  }

  useEffect(() => {
    return draftContext?.register(draftFieldId, {
      save: commit,
      cancel,
    })
  // The registered functions read refs so they do not need to be rebound on every draft update.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftContext, draftFieldId])

  const clear = () => {
    setDraft('')
    dirtyRef.current = false
    useWorkspaceSettingsStore.getState().removeKey(workspaceId, 'editor', 'homePath')
  }

  return (
    <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(280px,420px)] gap-4 md:gap-8 py-6 items-start border-y border-border-subtle">
      <div>
        <label htmlFor={inputId} className="text-sm font-semibold text-text-primary">
          {t('editor.settings.home_path.workspace')}
        </label>
        <p className="text-sm text-text-secondary mt-1">
          {t('editor.settings.home_path.description')}
        </p>
      </div>
      <div className="flex gap-2">
        <input
          id={inputId}
          type="text"
          aria-label={t('editor.settings.home_path.workspace')}
          placeholder={t('editor.settings.home_path.placeholder')}
          value={draft}
          onChange={(e) => { setDraft(e.target.value); dirtyRef.current = true }}
          onFocus={() => { focusedRef.current = true }}
          onBlur={handleBlur}
          onKeyDown={(e) => { if (e.key === 'Enter' && !e.nativeEvent.isComposing) commit() }}
          className="min-w-0 flex-1 px-3 py-2 rounded-md bg-surface-primary text-text-primary border border-border-default focus:border-accent focus:outline-none text-sm"
        />
        {!draftContext && (
          <>
            <button
              type="button"
              onClick={commit}
              disabled={draft === storedStr}
              className="px-3 py-2 rounded-md bg-accent text-white text-sm font-medium hover:bg-accent/90 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
            >
              {t('common.save')}
            </button>
            <button
              type="button"
              onClick={cancel}
              disabled={draft === storedStr}
              className="px-3 py-2 rounded-md border border-border-subtle text-sm text-text-secondary hover:bg-surface-muted disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
            >
              {t('common.cancel')}
            </button>
          </>
        )}
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

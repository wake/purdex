import { useState, useEffect, useId, useRef } from 'react'
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
  const focusedRef = useRef(false)
  const dirtyRef = useRef(false)

  // R2 codex: skip the sync while the user is editing so an external store
  // update (e.g. BroadcastChannel from another window) can't clobber the
  // in-progress draft. Focused edits reconcile against the latest store
  // value in commit(), which re-reads getState() at blur.
  useEffect(() => {
    if (focusedRef.current) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(storedStr)
  }, [storedStr])

  const handleBlur = () => {
    focusedRef.current = false
    if (!dirtyRef.current) {
      // R3 codex: focus-without-edit must not push the stale draft back to
      // the store. If the store changed during the focus window (e.g.
      // another window wrote via BroadcastChannel), pull the latest value
      // into the input instead of clobbering it.
      const live = useHostSettingsStore.getState().hosts[hostId]?.editor?.homePath
      setDraft(typeof live === 'string' ? live : '')
      return
    }
    dirtyRef.current = false
    commit()
  }

  const commit = () => {
    const trimmed = draft.trim()
    // Reflect normalization in the input even when the trimmed value matches
    // the stored one — otherwise trailing whitespace lingers in the UI while
    // persisted state is clean.
    if (trimmed !== draft) setDraft(trimmed)
    // Re-read the latest stored value — the snapshot captured at render can
    // be stale if the store changed during editing (R2 codex).
    const live = useHostSettingsStore.getState().hosts[hostId]?.editor?.homePath
    const liveStr = typeof live === 'string' ? live : ''
    if (trimmed === liveStr) return
    if (trimmed === '') {
      // R2 codex: only drop the homePath key — `clearModule` would wipe any
      // sibling editor settings (wrap / tabSize / ...) stored in the same bucket.
      useHostSettingsStore.getState().removeKey(hostId, 'editor', 'homePath')
      return
    }
    useHostSettingsStore.getState().set(hostId, 'editor', { homePath: trimmed })
  }

  const clear = () => {
    setDraft('')
    dirtyRef.current = false
    useHostSettingsStore.getState().removeKey(hostId, 'editor', 'homePath')
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
          onChange={(e) => { setDraft(e.target.value); dirtyRef.current = true }}
          onFocus={() => { focusedRef.current = true }}
          onBlur={handleBlur}
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

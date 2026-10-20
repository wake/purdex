// spa/src/components/watch/WatchSettings.tsx — the 守門員 section of a daemon host's 設定 page (watcher area spec §6.2 / §6.3, MON-9a-1): the host's `watch` row,
// read with lib/watch/watch-row and written whole with its revision. Only the start settings are drawn (MON-9a-1: the trigger fields are WWD-9's to remove); the fields
// not drawn stay in the stored row and are written back as they were. The page around it is `host/SettingsTab`.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigApiError, HostConfigConflictError } from '../../lib/host-config-api'
import { forgetWatchDraft, readWatchDraft, watchDraftKey, writeWatchDraft } from '../../lib/watch/draft-memory'
import { parseWatchRow, readWatchRow, type WatchRow } from '../../lib/watch/watch-row'
import { isSet, putWatchRow, rowWith, valueOf, type WatchKey } from '../../lib/watch/watch-row-write'
import { SegmentControl } from '../settings/SegmentControl'
import { SettingItem } from '../settings/SettingItem'
import { ToggleSwitch } from '../settings/ToggleSwitch'

type Kind = 'bool' | 'int' | 'text' | 'textarea' | 'mode'
const FIELDS: { key: WatchKey; kind: Kind }[] = [
  { key: 'enabled', kind: 'bool' },
  { key: 'mode', kind: 'mode' }, { key: 'patrol_idle_min', kind: 'int' },
  { key: 'auto_wake', kind: 'bool' },
  { key: 'agent_command', kind: 'text' }, { key: 'workdir', kind: 'text' },
  { key: 'start_prompt', kind: 'textarea' }, { key: 'wake_prompt', kind: 'textarea' },
]

const INPUT = 'w-64 px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary disabled:opacity-50'

function show(v: unknown): string {
  return typeof v === 'string' || typeof v === 'number' ? String(v) : ''
}

/** A text-like control: edited locally, committed on blur / Enter, put back to the stored value when the write is refused. */
function TextControl({ k, kind, stored, onCommit, label, draftKey }: {
  k: WatchKey; kind: Kind; stored: string; onCommit: (raw: string) => Promise<boolean>; label: string; draftKey: string
}) {
  // The unsent text lives in `lib/watch/draft-memory`, not only here: the tab unmounts on a switch (checklist).
  const [draft, setDraftState] = useState(() => readWatchDraft(draftKey) ?? stored)
  const setDraft = (text: string) => { writeWatchDraft(draftKey, text); setDraftState(text) }
  const settle = (text: string) => { forgetWatchDraft(draftKey); setDraftState(text) }
  const t = useI18nStore((s) => s.t)
  const [stale, setStale] = useState(false)
  // The row is written whole, so a write by ANOTHER field can bring this field a new stored value (a 409's copy). With text of the
  // reader's own in the box that text stays, marked; the reader chooses to send it or to take the host's value. A box with no draft
  // just follows the stored value.
  const seen = useRef(stored)
  useEffect(() => {
    if (seen.current === stored) return
    seen.current = stored
    const typed = readWatchDraft(draftKey)
    if (typed !== undefined && typed !== stored) { setStale(true); return }
    settle(stored)
    setStale(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- settle only closes over the key
  }, [stored])
  const commit = async () => {
    if (draft === stored) { forgetWatchDraft(draftKey); return }
    // The text is on its way: from here it is the write's, not a draft. Sent, refused (400) or reloaded (409), the draft has ended.
    forgetWatchDraft(draftKey)
    setStale(false)
    const ok = await onCommit(draft)
    if (!ok) setDraftState(stored) // only the field that was written is put back
  }
  // 「改用主機上的值」 must not send the text it is about to drop. A press on it does not take focus from the box (mousedown is
  // cancelled), and a Tab onto it blurs the box towards the button (relatedTarget), which is not a "send" either.
  const useBtn = useRef<HTMLButtonElement>(null)
  const onBoxBlur = (e: React.FocusEvent) => {
    if (useBtn.current !== null && e.relatedTarget === useBtn.current) return
    void commit()
  }
  const control = kind === 'textarea' ? (
    <textarea aria-label={label} data-testid={`watch-set-${k}`} rows={3} className={`${INPUT} w-96 font-mono`} value={draft}
      onChange={(e) => setDraft(e.target.value)} onBlur={onBoxBlur} />
  ) : (
    <input aria-label={label} data-testid={`watch-set-${k}`} className={`${INPUT} ${kind === 'text' ? 'w-96 font-mono' : ''}`} value={draft}
      onChange={(e) => setDraft(e.target.value)} onBlur={onBoxBlur} onKeyDown={(e) => { if (e.key === 'Enter') e.currentTarget.blur() }} />
  )
  return (
    <>
      {control}
      {stale && (
        <span data-testid={`watch-set-stale-${k}`} className="flex items-center gap-2 text-xs text-status-warning-text">
          {t('watch.set.stale')}
          <button type="button" ref={useBtn} data-testid={`watch-set-stale-use-${k}`} onMouseDown={(e) => e.preventDefault()} onClick={() => { settle(stored); setStale(false) }}
            className="px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('watch.set.stale_use')}</button>
        </span>
      )}
    </>
  )
}

/** 守門員: one daemon host's `watch` row. `hostId` only keys the drafts; `offline` is the App host not being connected. */
export function GuardianSection({ hostConfigId, hostId, offline }: { hostConfigId: string; hostId: string; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  const [state, setState] = useState<{ status: 'loading' } | { status: 'error' } | { status: 'ready'; w: WatchRow }>({ status: 'loading' })
  const [errors, setErrors] = useState<Partial<Record<WatchKey, string>>>({})
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const rowRef = useRef<WatchRow | null>(null)

  useEffect(() => {
    if (offline) return
    let live = true
    void readWatchRow(hostConfigId).then((w) => {
      if (!live) return
      rowRef.current = w
      setState(w ? { status: 'ready', w } : { status: 'error' })
    })
    return () => { live = false }
  }, [hostConfigId, offline])

  const write = useCallback(async (key: WatchKey, value: unknown): Promise<boolean> => {
    const base = rowRef.current
    if (!base) return false
    setErrors((e) => ({ ...e, [key]: undefined }))
    setNotice(null)
    setBusy(true)
    const adopt = (w: WatchRow) => { rowRef.current = w; setState({ status: 'ready', w }) }
    try {
      adopt(await putWatchRow(hostConfigId, rowWith(base, key, value), base.revision))
      return true
    } catch (e) {
      if (e instanceof HostConfigConflictError) {
        // The daemon's copy wins: show it, and say so; the reader makes the change again on top of it.
        // Same shape as the GET (see putWatchRow); a copy that does not read is a failure of this write.
        const fresh = parseWatchRow(e.current)
        if (!fresh) { setErrors((er) => ({ ...er, [key]: t('watch.set.write_failed') })); return false }
        adopt(fresh)
        setNotice(t('watch.set.conflict'))
      } else {
        const msg = e instanceof HostConfigApiError ? e.message : t('watch.set.write_failed')
        setErrors((er) => ({ ...er, [key]: msg }))
      }
      return false
    } finally {
      setBusy(false)
    }
  }, [hostConfigId, t])

  if (offline && state.status !== 'ready') return <p data-testid="watch-set-offline" className="text-sm text-text-muted">{t('watch.set.offline')}</p>
  if (state.status === 'loading') return <p data-testid="watch-set-loading" className="text-sm text-text-muted">{t('watch.set.loading')}</p>
  if (state.status === 'error') return <p data-testid="watch-set-unreadable" className="text-sm text-status-warning-text">{t('watch.set.unreadable')}</p>

  const w = state.w
  const locked = offline || busy

  const commitText = (f: { key: WatchKey; kind: Kind }) => async (raw: string): Promise<boolean> => {
    let value: unknown = raw
    if (f.kind === 'int') {
      if (!/^\d+$/.test(raw.trim())) { setErrors((e) => ({ ...e, [f.key]: t('watch.set.not_int') })); return false }
      value = Number(raw)
    }
    return write(f.key, value)
  }

  return (
    <div data-testid="watch-set-guardian">
      {offline && <p data-testid="watch-set-offline" className="mb-2 text-xs text-text-muted">{t('watch.set.offline')}</p>}
      {notice && <p data-testid="watch-set-conflict" role="status" className="mb-2 text-xs text-status-warning-text">{notice}</p>}
      {FIELDS.map((f) => {
        const label = t(`watch.set.f.${f.key}`)
        const err = errors[f.key]
        const source = isSet(w, f.key) ? t('watch.set.source_set') : t('watch.set.source_default')
        const v = valueOf(w, f.key)
        return (
          <div key={f.key} data-testid={`watch-set-row-${f.key}`}>
            <SettingItem label={label} disabled={locked} description={t(`watch.set.d.${f.key}`)}>
              <div data-testid={`watch-set-cell-${f.key}`} className="flex flex-col items-end gap-1">
                {f.kind === 'bool' ? (
                  <ToggleSwitch label={label} checked={v === true} testId={`watch-set-${f.key}`} onChange={(c) => void write(f.key, c)} />
                ) : f.kind === 'mode' ? (
                  <SegmentControl options={[{ value: 'resident', label: t('watch.set.mode.resident') }, { value: 'patrol', label: t('watch.set.mode.patrol') }]}
                    value={v === 'resident' ? 'resident' : 'patrol'} onChange={(m) => void write('mode', m)} />
                ) : (
                  <TextControl k={f.key} kind={f.kind} label={label} stored={show(v)} draftKey={watchDraftKey(hostConfigId, hostId, f.key)} onCommit={commitText(f)} />
                )}
                <span className="text-[11px] text-text-muted" data-testid={`watch-set-source-${f.key}`}>{source}</span>
                {err && <p data-testid={`watch-set-error-${f.key}`} role="alert" className="max-w-96 text-right text-xs text-status-error-text whitespace-pre-wrap">{err}</p>}
              </div>
            </SettingItem>
          </div>
        )
      })}
    </div>
  )
}

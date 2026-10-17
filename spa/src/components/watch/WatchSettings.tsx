// spa/src/components/watch/WatchSettings.tsx — a host tab's 設定 page (watcher area spec §6.2 / §6.3, plan §9): for a daemon host the
// 守門員 section (the host's `watch` row, read with lib/watch/watch-row and written whole with its revision); for a probe host the
// note and F-2a2's probe-host editor. The gate settings (設定組, 資源管制, slots) are W5c2.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigApiError, HostConfigConflictError } from '../../lib/host-config-api'
import { forgetWatchDraft, readWatchDraft, watchDraftKey, writeWatchDraft } from '../../lib/watch/draft-memory'
import { parseWatchRow, readWatchRow, type WatchRow } from '../../lib/watch/watch-row'
import { isSet, putWatchRow, rowWith, valueOf, type WatchKey } from '../../lib/watch/watch-row-write'
import { Section } from '../hosts/form-fields'
import { ProbeHostEditor } from '../hosts/ProbeHostEditor'
import { SegmentControl } from '../settings/SegmentControl'
import { SettingItem } from '../settings/SettingItem'
import { ToggleSwitch } from '../settings/ToggleSwitch'

type Kind = 'bool' | 'int' | 'float' | 'text' | 'targets' | 'textarea' | 'mode'
const FIELDS: { key: WatchKey; kind: Kind }[] = [
  { key: 'enabled', kind: 'bool' },
  { key: 'x_cores', kind: 'float' }, { key: 'y_mb', kind: 'int' }, { key: 't_s', kind: 'int' },
  { key: 'pressure_level', kind: 'int' }, { key: 'pressure_t_s', kind: 'int' },
  { key: 'page_rate', kind: 'float' }, { key: 'page_t_s', kind: 'int' },
  { key: 'targets', kind: 'targets' },
  { key: 'mode', kind: 'mode' }, { key: 'patrol_idle_min', kind: 'int' },
  { key: 'auto_wake', kind: 'bool' },
  { key: 'agent_command', kind: 'text' }, { key: 'workdir', kind: 'text' },
  { key: 'start_prompt', kind: 'textarea' }, { key: 'wake_prompt', kind: 'textarea' },
]

const INPUT = 'w-64 px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary disabled:opacity-50'

function show(kind: Kind, v: unknown): string {
  if (kind === 'targets') return Array.isArray(v) ? v.join(', ') : ''
  return typeof v === 'string' || typeof v === 'number' ? String(v) : ''
}

function targetsOf(w: WatchRow): string[] {
  const v = valueOf(w, 'targets')
  return Array.isArray(v) ? v.filter((a): a is string => typeof a === 'string') : []
}

/** A text-like control: edited locally, committed on blur / Enter, put back to the stored value when the write is refused. */
function TextControl({ k, kind, stored, onCommit, label, draftKey }: {
  k: WatchKey; kind: Kind; stored: string; onCommit: (raw: string) => Promise<boolean>; label: string; draftKey: string
}) {
  // The unsent text lives in `lib/watch/draft-memory`, not only here: the tab unmounts on a switch (checklist).
  const [draft, setDraftState] = useState(() => readWatchDraft(draftKey) ?? stored)
  const setDraft = (text: string) => { writeWatchDraft(draftKey, text); setDraftState(text) }
  const settle = (text: string) => { forgetWatchDraft(draftKey); setDraftState(text) }
  // A stored value that changed under the box (a write landed, a 409 reloaded) replaces whatever was typed.
  const seen = useRef(stored)
  useEffect(() => {
    if (seen.current !== stored) { seen.current = stored; settle(stored) }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- settle only closes over the key
  }, [stored])
  const commit = async () => {
    if (draft === stored) { forgetWatchDraft(draftKey); return }
    const ok = await onCommit(draft)
    forgetWatchDraft(draftKey) // sent, refused (400) or reloaded (409): the draft has ended either way
    if (!ok) setDraftState(stored)
  }
  if (kind === 'textarea') {
    return <textarea aria-label={label} data-testid={`watch-set-${k}`} rows={3} className={`${INPUT} w-96 font-mono`} value={draft}
      onChange={(e) => setDraft(e.target.value)} onBlur={() => void commit()} />
  }
  return <input aria-label={label} data-testid={`watch-set-${k}`} className={`${INPUT} ${kind === 'text' ? 'w-96 font-mono' : ''}`} value={draft}
    onChange={(e) => setDraft(e.target.value)} onBlur={() => void commit()} onKeyDown={(e) => { if (e.key === 'Enter') e.currentTarget.blur() }} />
}

/** 守門員: one daemon host's `watch` row. `peerIds` are the other daemon hosts, read only to compare `targets`. */
function GuardianSection({ hostConfigId, fleetHostId, offline, peerIds }: { hostConfigId: string; fleetHostId: string; offline: boolean; peerIds: string[] }) {
  const t = useI18nStore((s) => s.t)
  const [state, setState] = useState<{ status: 'loading' } | { status: 'error' } | { status: 'ready'; w: WatchRow }>({ status: 'loading' })
  const [errors, setErrors] = useState<Partial<Record<WatchKey, string>>>({})
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [peerTargets, setPeerTargets] = useState<string[][]>([])
  const rowRef = useRef<WatchRow | null>(null)
  const peerKey = peerIds.join('\n')

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

  useEffect(() => {
    if (offline || !peerKey) { setPeerTargets([]); return }
    let live = true
    void Promise.all(peerKey.split('\n').map(readWatchRow)).then((rows) => {
      if (live) setPeerTargets(rows.filter((r): r is WatchRow => r !== null).map(targetsOf))
    })
    return () => { live = false }
  }, [peerKey, offline])

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
  const mine = targetsOf(w)
  const differs = peerTargets.some((p) => p.join('\n') !== mine.join('\n'))
  const locked = offline || busy

  const commitText = (f: { key: WatchKey; kind: Kind }) => async (raw: string): Promise<boolean> => {
    let value: unknown = raw
    if (f.kind === 'int') {
      if (!/^\d+$/.test(raw.trim())) { setErrors((e) => ({ ...e, [f.key]: t('watch.set.not_int') })); return false }
      value = Number(raw)
    } else if (f.kind === 'float') {
      const n = Number(raw)
      if (raw.trim() === '' || !Number.isFinite(n)) { setErrors((e) => ({ ...e, [f.key]: t('watch.set.not_number') })); return false }
      value = n
    } else if (f.kind === 'targets') {
      value = raw.split(/[,\s]+/).filter(Boolean)
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
              {f.kind === 'bool' ? (
                <ToggleSwitch label={label} checked={v === true} testId={`watch-set-${f.key}`} onChange={(c) => void write(f.key, c)} />
              ) : f.kind === 'mode' ? (
                <SegmentControl options={[{ value: 'resident', label: t('watch.set.mode.resident') }, { value: 'patrol', label: t('watch.set.mode.patrol') }]}
                  value={v === 'resident' ? 'resident' : 'patrol'} onChange={(m) => void write('mode', m)} />
              ) : (
                <TextControl k={f.key} kind={f.kind} label={label} stored={show(f.kind, v)} draftKey={watchDraftKey(hostConfigId, fleetHostId, f.key)} onCommit={commitText(f)} />
              )}
            </SettingItem>
            <div className="-mt-2 mb-1 text-[11px] text-text-muted" data-testid={`watch-set-source-${f.key}`}>{source}</div>
            {err && <p data-testid={`watch-set-error-${f.key}`} role="alert" className="mb-1 text-xs text-status-error-text whitespace-pre-wrap">{err}</p>}
            {f.key === 'targets' && differs && <p data-testid="watch-set-targets-differ" className="mb-1 text-xs text-status-warning-text">{t('watch.set.targets_differ')}</p>}
          </div>
        )
      })}
    </div>
  )
}

/** The 設定 page of one fleet host. `hostConfigId`: the App's id for a daemon host (absent for one the App has no config for). */
export function WatchSettings({ kind, fleetHostId, hostConfigId, offline, peerIds }: {
  kind: 'daemon' | 'probe'; fleetHostId: string; hostConfigId?: string; offline: boolean; peerIds: string[]
}) {
  const t = useI18nStore((s) => s.t)
  if (kind === 'probe') {
    return (
      <div data-testid="watch-set-probe">
        <p className="mb-4 text-xs text-text-secondary">{t('watch.set.probe_note')}</p>
        <ProbeHostEditor key={fleetHostId} probeId={fleetHostId} />
      </div>
    )
  }
  return (
    <Section title={t('watch.set.guardian')}>
      {hostConfigId
        ? <GuardianSection hostConfigId={hostConfigId} fleetHostId={fleetHostId} offline={offline} peerIds={peerIds} />
        : <p data-testid="watch-set-unmapped" className="text-sm text-text-muted">{t('watch.set.unmapped')}</p>}
    </Section>
  )
}

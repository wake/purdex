// spa/src/components/watch/WorkerExceptions.tsx — 「暫停」 on a pool host's 設定 page (plan "WWD-7 design", Worker settings; user Q6): the pauses now on the host,
// each with 結束, and the ways to add one that ends by itself — 1 / 3 / 8 小時, or a custom end time (the last few used are offered again). The word 例外 never
// appears. The list is the host's facts (`exceptions[]`: name, end time); what a pause holds (and a pid it follows) comes from `GET …/exceptions` when the
// host has it — an older host answers 404 and its holds read as unknown. After a change the row waits for the next facts, like the worker settings do.
import { useEffect, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolStore } from '../../lib/pool/store'
import { controlDenied, controlSource, deletePause, fetchPauses, PoolChangeError, postPause } from '../../lib/pool/control-api'
import { forgetWorkerDraft, readWorkerDraft, workerDraftKey, writeWorkerDraft } from '../../lib/pool/worker-draft-memory'
import { allowOf, holdsOf, loadRecentClocks, parseClock, pauseName, PAUSE_CLASSES, PAUSE_FLAGS, PAUSE_HOURS, rememberClock, saveRecentClocks, untilClock, untilInHours } from '../../lib/pool/pause'
import type { PoolExceptionDetail, PoolHold } from '../../lib/pool/types'
import { PENDING_NOTICE_MS } from './WorkerSettings'
import { Section } from '../hosts/form-fields'
import { SettingItem } from '../settings/SettingItem'

const BTN = 'px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary text-xs cursor-pointer disabled:opacity-50'
const pickedOf = (draft: string | undefined): string[] => (draft ? draft.split(',').filter(Boolean) : [])
const pad = (n: number) => String(n).padStart(2, '0')

/** `hh:mm`, with the date in front when it is not today. */
function clockText(ms: number, now: number): string {
  const d = new Date(ms)
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return d.toDateString() === new Date(now).toDateString() ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`
}

interface Waiting { kind: 'add' | 'end'; name: string; at: number; until: number; holds: PoolHold[] }

/** The pauses of one pool host, by its alias. */
export function WorkerExceptions({ alias }: { alias: string }) {
  const t = useI18nStore((s) => s.t)
  const hosts = usePoolStore((s) => s.hosts)
  const poolSource = usePoolStore((s) => s.sourceHostId)
  useNexHostStore((s) => s.byHost)
  useHostStore((s) => s.runtime)
  const host = hosts.find((h) => h.alias === alias)
  const source = controlSource(poolSource)
  const readFrom = poolSource ?? source

  const facts = host?.facts?.exceptions
  const factNames = (facts ?? []).map((e) => e.name).join('\n')
  const [details, setDetails] = useState<PoolExceptionDetail[] | null>(null) // null: not known (an older host, or the read failed)
  const [waiting, setWaiting] = useState<Waiting[]>([])
  const [late, setLate] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<{ name: string | null; text: string } | null>(null)
  const [recent, setRecent] = useState<string[]>(loadRecentClocks)
  const [, redraw] = useState(0)
  const clockKey = workerDraftKey(alias, 'pause_clock')
  const lightKey = workerDraftKey(alias, 'pause_light')
  const [clock, setClockState] = useState(() => readWorkerDraft(clockKey) ?? '')
  const [alsoLight, setLightState] = useState(() => readWorkerDraft(lightKey) === '1')
  const allowKey = workerDraftKey(alias, 'pause_allow')
  const flagsKey = workerDraftKey(alias, 'pause_flags')
  const [allowed, setAllowedState] = useState(() => pickedOf(readWorkerDraft(allowKey)))
  const [marked, setMarkedState] = useState(() => pickedOf(readWorkerDraft(flagsKey)))
  const toggle = (list: string[], v: string, key: string, set: (l: string[]) => void) => {
    const next = list.includes(v) ? list.filter((x) => x !== v) : [...list, v]
    writeWorkerDraft(key, next.join(','))
    set(next)
  }

  useEffect(() => {
    if (readFrom === null) return
    let live = true
    void fetchPauses(readFrom, alias).then((d) => { if (live) setDetails(d) })
    return () => { live = false }
  }, [readFrom, alias, factNames])

  // A change is settled when the facts show it: an added pause is listed, an ended one is gone.
  useEffect(() => {
    const listedNames = factNames === '' ? [] : factNames.split('\n')
    setWaiting((w) => {
      const rest = w.filter((x) => (x.kind === 'add' ? !listedNames.includes(x.name) : listedNames.includes(x.name)))
      return rest.length === w.length ? w : rest
    })
  }, [factNames])
  useEffect(() => {
    if (waiting.length === 0) return
    const left = Math.min(...waiting.map((w) => w.at)) + PENDING_NOTICE_MS - Date.now()
    const id = setTimeout(() => setLate(true), Math.max(0, left))
    return () => clearTimeout(id)
  }, [waiting])

  if (!host || facts === undefined) return null
  const hasPsi = host.facts?.mem.psi != null
  const readOnly = source === null || controlDenied(source)
  const now = Date.now()

  const run = async (name: string | null, call: (target: string) => Promise<void>, after: Waiting): Promise<void> => {
    const target = controlSource(usePoolStore.getState().sourceHostId)
    if (target === null) return
    setFailure(null)
    setBusy(true)
    try {
      await call(target)
      setWaiting((w) => [...w.filter((x) => x.name !== after.name), after])
      setLate(false)
      void usePoolStore.getState().readOnce()
    } catch (e) {
      const err = e instanceof PoolChangeError ? e : null
      if (err?.poolError.action === 'read_only') redraw((n) => n + 1)
      else if (err?.poolError.code === 'wwd_rejected') setFailure({ name, text: err.detail || t('pool.worker.rejected') })
      else if (err?.poolError.code === 'bad_request') setFailure({ name, text: t('pool.worker.bad_request') })
      else setFailure({ name, text: t('pool.worker.no_answer', { alias, reason: err?.detail ?? '' }) })
    } finally {
      setBusy(false)
    }
  }
  const add = (until: number) => {
    const at = Date.now()
    const name = pauseName(at)
    const holds = holdsOf(alsoLight)
    const allow = allowOf(PAUSE_CLASSES.filter((c) => allowed.includes(c)))
    const flags = PAUSE_FLAGS.filter((f) => marked.includes(f) && (f !== 'expect_pressure' || hasPsi))
    void run(null, (target) => postPause(target, alias, name, until, holds, { allow, flags }), { kind: 'add', name, at, until, holds })
  }
  const end = (name: string) => void run(name, (target) => deletePause(target, alias, name), { kind: 'end', name, at: Date.now(), until: 0, holds: [] })
  const addClock = (hhmm: string) => {
    const until = untilClock(Date.now(), hhmm)
    if (until === null) { setFailure({ name: null, text: t('pool.pause.clock_bad') }); return }
    const next = rememberClock(recent, hhmm)
    setRecent(next)
    saveRecentClocks(next)
    forgetWorkerDraft(clockKey)
    setClockState('')
    add(until)
  }

  // the host's list, plus the pauses just added that its facts do not carry yet
  const listed = facts.map((e) => ({ name: e.name, until: e.until, add: false }))
  for (const w of waiting) if (w.kind === 'add' && !listed.some((l) => l.name === w.name)) listed.push({ name: w.name, until: w.until, add: true })

  return (
    <Section title={t('pool.pause.title')}>
      <div data-testid="pool-pause">
        {readOnly && source !== null && <p data-testid="pool-pause-readonly" role="status" className="mb-2 text-xs text-status-warning-text">{t('pool.worker.read_only')}</p>}
        {late && waiting.length > 0 && <p data-testid="pool-pause-late" role="status" className="mb-2 text-xs text-status-warning-text">{t('pool.worker.late')}</p>}
        {listed.length === 0
          ? <p data-testid="pool-pause-none" className="py-2 text-sm text-text-muted">{t('pool.pause.none')}</p>
          : listed.map((e) => {
            const d = details?.find((x) => x.name === e.name)
            const until = e.until ?? d?.until ?? null
            const pid = d?.pid ?? null
            const ending = waiting.some((w) => w.kind === 'end' && w.name === e.name)
            const label = until !== null ? t('pool.pause.until', { time: clockText(until, now) })
              : pid !== null ? t('pool.pause.pid', { pid }) : t('pool.pause.follows')
            const holdsText = d === undefined ? t('pool.pause.holds_unknown') : d.holds.includes('light') ? t('pool.pause.holds_both') : t('pool.pause.holds_heavy')
            const extras = [
              d?.allow && d.allow.classes.length > 0 ? t('pool.pause.allows', { classes: d.allow.classes.join(', ') }) : '',
              d?.flags && d.flags.length > 0 ? t('pool.pause.marks', { flags: d.flags.map((f) => t(`pool.pause.flag_short.${f}`)).join(', ') }) : '',
            ].filter(Boolean)
            const holds = [holdsText, ...extras].join(' · ')
            return (
              <div key={e.name} data-testid={`pool-pause-row-${e.name}`}>
                <SettingItem label={label} description={`${e.name} · ${holds}`}>
                  <div className="flex flex-col items-end gap-1">
                    <button type="button" className={BTN} data-testid={`pool-pause-end-${e.name}`} disabled={readOnly || busy || ending} onClick={() => end(e.name)}>{t('pool.pause.end')}</button>
                    {(ending || e.add) && <span data-testid={`pool-pause-pending-${e.name}`} className="text-[11px] text-text-muted">{t('pool.worker.pending')}</span>}
                    {failure?.name === e.name && <p data-testid={`pool-pause-error-${e.name}`} role="alert" className="max-w-96 text-right text-xs text-status-error-text whitespace-pre-wrap">{failure.text}</p>}
                  </div>
                </SettingItem>
              </div>
            )
          })}

        <SettingItem label={t('pool.pause.add')} description={t('pool.pause.add_d')} disabled={readOnly}>
          <div className="flex flex-col items-end gap-2">
            <span className="flex flex-wrap justify-end gap-2">
              {PAUSE_HOURS.map((h) => (
                <button key={h} type="button" className={BTN} data-testid={`pool-pause-add-${h}h`} disabled={busy} onClick={() => add(untilInHours(Date.now(), h))}>{t('pool.pause.hours', { n: h })}</button>
              ))}
            </span>
            <span className="flex items-center gap-2">
              <input type="time" aria-label={t('pool.pause.custom')} data-testid="pool-pause-clock" className="px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary"
                value={clock} onChange={(e) => { writeWorkerDraft(clockKey, e.target.value); setClockState(e.target.value) }} />
              <button type="button" className={BTN} data-testid="pool-pause-add-clock" disabled={busy || parseClock(clock) === null} onClick={() => addClock(clock)}>{t('pool.pause.custom_go')}</button>
            </span>
            {recent.length > 0 && (
              <span data-testid="pool-pause-recent" className="flex flex-wrap justify-end gap-1">
                {recent.map((r) => <button key={r} type="button" className={BTN} data-testid={`pool-pause-recent-${r}`} disabled={busy} onClick={() => addClock(r)}>{r}</button>)}
              </span>
            )}
            <label className="flex items-center gap-1 text-xs text-text-secondary">
              <input type="checkbox" data-testid="pool-pause-light" checked={alsoLight}
                onChange={(e) => { writeWorkerDraft(lightKey, e.target.checked ? '1' : '0'); setLightState(e.target.checked) }} />
              {t('pool.pause.also_light')}
            </label>
            <span className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-text-secondary" data-testid="pool-pause-allow">
              {t('pool.pause.allow')}
              {PAUSE_CLASSES.map((c) => (
                <label key={c} className="flex items-center gap-1">
                  <input type="checkbox" data-testid={`pool-pause-allow-${c}`} checked={allowed.includes(c)} onChange={() => toggle(allowed, c, allowKey, setAllowedState)} />
                  {c}
                </label>
              ))}
            </span>
            <span className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-text-secondary" data-testid="pool-pause-flags">
              {t('pool.pause.flags')}
              {PAUSE_FLAGS.filter((f) => f !== 'expect_pressure' || hasPsi).map((f) => (
                <label key={f} className="flex items-center gap-1">
                  <input type="checkbox" data-testid={`pool-pause-flag-${f}`} checked={marked.includes(f)} onChange={() => toggle(marked, f, flagsKey, setMarkedState)} />
                  {t(`pool.pause.flag.${f}`)}
                </label>
              ))}
            </span>
            {failure?.name === null && <p data-testid="pool-pause-error" role="alert" className="max-w-96 text-right text-xs text-status-error-text whitespace-pre-wrap">{failure.text}</p>}
          </div>
        </SettingItem>
      </div>
    </Section>
  )
}

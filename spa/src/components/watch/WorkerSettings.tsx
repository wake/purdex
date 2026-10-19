// spa/src/components/watch/WorkerSettings.tsx — the top of a pool host's 設定 page (plan "WWD-7 design", Worker settings; `pool.control.v1`).
// Rows: 接工作, heavy / light slots, the owner rule (only when the host reports an owner), the labels (read only). A change is one whole `PUT`;
// the value stays "pending" until the next facts carry it, and after 15 s says so. A refusal puts the value back and says why under the row.
// A device token cannot change anything (403): the whole section turns read only with one notice.
import { useEffect, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolStore } from '../../lib/pool/store'
import { controlDenied, controlSource, PoolChangeError, putWorker } from '../../lib/pool/control-api'
import { forgetWorkerDraft, readWorkerDraft, workerDraftKey, writeWorkerDraft } from '../../lib/pool/worker-draft-memory'
import { ownerMinutes, ownerRuleOf, parseMinutes, sameWorker } from '../../lib/pool/worker-rule'
import type { PoolHost, PoolWorker } from '../../lib/pool/types'
import { Section } from '../hosts/form-fields'
import { SettingItem } from '../settings/SettingItem'
import { ToggleSwitch } from '../settings/ToggleSwitch'

/** After this long without facts that carry the new value, the page says the host has not reported it. */
export const PENDING_NOTICE_MS = 15_000

/** What an empty owner rule means on the host (wwd `ParseOwnerRule`: 5 minutes). */
const DEFAULT_OWNER_MINUTES = 5

type Field = 'enabled' | 'heavy' | 'light' | 'owner_rule'
type Patch = Partial<Pick<PoolWorker, 'enabled' | 'owner_rule'>> & { slots?: Partial<PoolWorker['slots']> }
/** `patch` is kept for a retry: it is applied to whatever the settings are at the click, never to the ones at the failure. */
interface Failure { field: Field; text: string; patch?: Patch }

const INPUT = 'w-20 px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary disabled:opacity-50'
const BTN = 'px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary text-xs cursor-pointer disabled:opacity-50'

function Stepper({ value, onChange, label, testId, disabled }: { value: number; onChange: (n: number) => void; label: string; testId: string; disabled: boolean }) {
  return (
    <span className="flex items-center gap-2" data-testid={testId}>
      <button type="button" className={BTN} aria-label={`${label} −`} data-testid={`${testId}-dec`} disabled={disabled || value <= 0} onClick={() => onChange(value - 1)}>−</button>
      <span className="w-6 text-center text-sm text-text-primary" data-testid={`${testId}-value`}>{value}</span>
      <button type="button" className={BTN} aria-label={`${label} +`} data-testid={`${testId}-inc`} disabled={disabled} onClick={() => onChange(value + 1)}>+</button>
    </span>
  )
}

/** The owner rule: `hid<N>m` (and the default, "") as a minutes box; any other text as it is. The unsent text outlives the tab (worker-draft-memory). */
function OwnerRuleBox({ alias, rule, disabled, onCommit }: { alias: string; rule: string; disabled: boolean; onCommit: (rule: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const minutes = ownerMinutes(rule)
  const asMinutes = rule === '' || minutes !== null
  const stored = minutes !== null ? String(minutes) : rule
  const key = workerDraftKey(alias, 'owner_rule')
  const [draft, setDraftState] = useState(() => readWorkerDraft(key) ?? stored)
  const [bad, setBad] = useState(false)
  // A new stored value (the facts moved on) replaces a box that holds no draft of the reader's.
  useEffect(() => {
    if (readWorkerDraft(key) === undefined) setDraftState(stored)
  }, [stored, key])
  const setDraft = (text: string) => { writeWorkerDraft(key, text); setDraftState(text); setBad(false) }
  const commit = () => {
    if (draft === stored) { forgetWorkerDraft(key); return }
    let next: string
    if (asMinutes) {
      if (draft.trim() === '') next = ''
      else {
        const n = parseMinutes(draft)
        if (n === null) { setBad(true); return }
        next = ownerRuleOf(n)
      }
    } else next = draft
    forgetWorkerDraft(key)
    if (next === rule) { setDraftState(stored); return }
    onCommit(next)
  }
  // the stepper moves from the stored minutes (the host's default, 5, when the rule is empty) and sends at once; a half-typed value is dropped for it
  const step = (d: number) => {
    forgetWorkerDraft(key)
    onCommit(ownerRuleOf(Math.min(9999, Math.max(1, (minutes ?? DEFAULT_OWNER_MINUTES) + d))))
  }
  return (
    <span className="flex items-center gap-2">
      {asMinutes && <button type="button" className={BTN} aria-label={`${t('pool.worker.owner_rule')} −`} data-testid="pool-worker-owner-rule-dec" disabled={disabled || (minutes ?? DEFAULT_OWNER_MINUTES) <= 1} onClick={() => step(-1)}>−</button>}
      <input aria-label={t('pool.worker.owner_rule')} data-testid="pool-worker-owner-rule" className={`${INPUT} ${asMinutes ? '' : 'w-40 font-mono'}`} disabled={disabled}
        inputMode={asMinutes ? 'numeric' : undefined} placeholder={asMinutes ? t('pool.worker.owner_default') : undefined} value={draft}
        onChange={(e) => setDraft(e.target.value)} onBlur={commit} onKeyDown={(e) => { if (e.key === 'Enter') e.currentTarget.blur() }} />
      {asMinutes && <button type="button" className={BTN} aria-label={`${t('pool.worker.owner_rule')} +`} data-testid="pool-worker-owner-rule-inc" disabled={disabled || (minutes ?? DEFAULT_OWNER_MINUTES) >= 9999} onClick={() => step(1)}>+</button>}
      {asMinutes && <span className="text-xs text-text-muted">{t('pool.worker.minutes_unit')}</span>}
      {bad && <span data-testid="pool-worker-owner-rule-bad" role="alert" className="text-xs text-status-error-text">{t('pool.worker.owner_bad')}</span>}
    </span>
  )
}

/** The settings of one pool host, by its alias. Renders nothing the reader can act on until a connected daemon announces `pool.control.v1`. */
export function WorkerSettings({ alias }: { alias: string }) {
  const t = useI18nStore((s) => s.t)
  const hosts = usePoolStore((s) => s.hosts)
  const poolSource = usePoolStore((s) => s.sourceHostId)
  // the connection state and the capabilities decide who can be asked to change anything: stay subscribed to both
  useNexHostStore((s) => s.byHost)
  useHostStore((s) => s.runtime)
  const host: PoolHost | undefined = hosts.find((h) => h.alias === alias)
  const source = controlSource(poolSource)

  const [pending, setPending] = useState<{ sent: PoolWorker; base: PoolWorker; at: number; baseAt: number | null } | null>(null)
  const [late, setLate] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [confirmOff, setConfirmOff] = useState(false)
  const [, redraw] = useState(0) // a 403 is remembered per daemon (control-api); this only draws it

  const current: PoolWorker | null = host ? (host.facts?.worker ?? host.worker) : null

  // Facts that carry the value it was sent as settle it — facts read after the ones the page showed when it sent (`baseAt`, the host's own
  // clock, never ours): an older answer still on its way can hold the same value without the host having applied anything.
  const factsAt = host?.facts?.at ?? 0
  useEffect(() => {
    if (pending && current && sameWorker(current, pending.sent) && (pending.baseAt === null || factsAt > pending.baseAt)) { setPending(null); setLate(false) }
  }, [pending, current, factsAt])
  useEffect(() => {
    if (!pending) return
    const left = pending.at + PENDING_NOTICE_MS - Date.now()
    const id = setTimeout(() => setLate(true), Math.max(0, left))
    return () => clearTimeout(id)
  }, [pending])

  if (!host || !current) return null
  if (source === null) {
    return (
      <Section title={t('pool.worker.title')}>
        <p data-testid="pool-worker-unsupported" className="text-sm text-text-muted">{t('pool.worker.unsupported')}</p>
      </Section>
    )
  }

  const readOnly = controlDenied(source)
  const shown: PoolWorker = pending?.sent ?? current
  const locked = busy || readOnly
  const running = host.facts?.worker.running ?? { heavy: 0, light: 0 }

  const change = async (field: Field, patch: Patch): Promise<void> => {
    const base = pending?.sent ?? current
    const next: PoolWorker = { ...base, ...patch, slots: { ...base.slots, ...patch.slots } }
    setFailure(null)
    setBusy(true)
    try {
      await putWorker(source, alias, next)
      setPending({ sent: next, base, at: Date.now(), baseAt: host.facts?.at ?? null })
      setLate(false)
      void usePoolStore.getState().readOnce() // the facts that carry it, sooner than the next poll
    } catch (e) {
      const err = e instanceof PoolChangeError ? e : null
      const action = err?.poolError.action
      if (action === 'read_only') { redraw((n) => n + 1); return }
      if (err?.poolError.code === 'wwd_rejected') setFailure({ field, text: err.detail || t('pool.worker.rejected') })
      else if (err?.poolError.code === 'bad_request') setFailure({ field, text: t('pool.worker.bad_request') })
      else setFailure({ field, text: t('pool.worker.no_answer', { alias, reason: err?.detail ?? '' }), patch })
    } finally {
      setBusy(false)
    }
  }

  const rowError = (field: Field) => {
    if (failure?.field !== field) return null
    const retry = failure.patch
    return (
      <p data-testid={`pool-worker-error-${field}`} role="alert" className="max-w-96 text-right text-xs text-status-error-text whitespace-pre-wrap">
        {failure.text}
        {retry && <button type="button" className={`${BTN} ml-2`} data-testid={`pool-worker-retry-${field}`} onClick={() => void change(field, retry)}>{t('pool.worker.retry')}</button>}
      </p>
    )
  }

  const owner = host.facts?.owner ?? null
  const onToggle = (on: boolean) => {
    if (!on && running.heavy + running.light > 0) { setConfirmOff(true); return }
    void change('enabled', { enabled: on })
  }
  // the rows the sent change touched wait for the facts (a row is marked by what was sent, not by how the facts compare: they may hold it already)
  const pendingMark = (touched: boolean) => touched && pending !== null && (
    <span data-testid="pool-worker-pending" className="text-[11px] text-text-muted">{t('pool.worker.pending')}</span>
  )

  return (
    <Section title={t('pool.worker.title')}>
      <div data-testid="pool-worker-settings">
        {readOnly && <p data-testid="pool-worker-readonly" role="status" className="mb-2 text-xs text-status-warning-text">{t('pool.worker.read_only')}</p>}
        {pending && late && <p data-testid="pool-worker-late" role="status" className="mb-2 text-xs text-status-warning-text">{t('pool.worker.late')}</p>}

        <SettingItem label={t('pool.worker.enabled')} description={t('pool.worker.enabled_d')} disabled={locked}>
          <div className="flex flex-col items-end gap-1">
            <ToggleSwitch label={t('pool.worker.enabled')} checked={shown.enabled} testId="pool-worker-enabled" onChange={onToggle} />
            {pendingMark(pending !== null && pending.sent.enabled !== pending.base.enabled)}
            {confirmOff && (
              <span data-testid="pool-worker-confirm-off" role="alertdialog" className="flex items-center gap-2 text-xs text-text-secondary">
                {t('pool.worker.confirm_off', { n: running.heavy + running.light })}
                <button type="button" className={BTN} data-testid="pool-worker-confirm-yes" onClick={() => { setConfirmOff(false); void change('enabled', { enabled: false }) }}>{t('pool.worker.confirm_yes')}</button>
                <button type="button" className={BTN} data-testid="pool-worker-confirm-no" onClick={() => setConfirmOff(false)}>{t('common.cancel')}</button>
              </span>
            )}
            {rowError('enabled')}
          </div>
        </SettingItem>

        {(['heavy', 'light'] as const).map((k) => (
          <SettingItem key={k} label={t(`pool.worker.slots_${k}`)} description={t(`pool.worker.slots_${k}_d`, { running: running[k] })} disabled={locked}>
            <div className="flex flex-col items-end gap-1">
              <Stepper value={shown.slots[k]} label={t(`pool.worker.slots_${k}`)} testId={`pool-worker-slots-${k}`} disabled={locked}
                onChange={(n) => void change(k, { slots: { [k]: n } })} />
              {pendingMark(pending !== null && pending.sent.slots[k] !== pending.base.slots[k])}
              {rowError(k)}
            </div>
          </SettingItem>
        ))}

        {owner !== null && (
          <SettingItem label={t('pool.worker.owner_rule')} description={t('pool.worker.owner_rule_d')} disabled={locked}>
            <div className="flex flex-col items-end gap-1">
              <OwnerRuleBox alias={alias} rule={shown.owner_rule} disabled={locked} onCommit={(rule) => void change('owner_rule', { owner_rule: rule })} />
              {pendingMark(pending !== null && pending.sent.owner_rule !== pending.base.owner_rule)}
              <span data-testid="pool-worker-owner-state" className="text-[11px] text-text-muted">
                {owner.active ? t('pool.worker.owner_active', { reason: owner.reason }) : t('pool.worker.owner_idle', { min: Math.floor(owner.idle_s / 60) })}
              </span>
              {rowError('owner_rule')}
            </div>
          </SettingItem>
        )}

        <SettingItem label={t('pool.worker.labels')} description={t('pool.worker.labels_d')}>
          <span data-testid="pool-worker-labels" className="flex flex-wrap justify-end gap-1">
            {host.labels.length === 0
              ? <span className="text-xs text-text-muted">{t('pool.worker.labels_none')}</span>
              : host.labels.map((l) => <span key={l} className="px-1.5 py-0.5 rounded bg-surface-tertiary text-xs text-text-secondary">{l}</span>)}
          </span>
        </SettingItem>
      </div>
    </Section>
  )
}

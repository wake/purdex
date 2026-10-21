// spa/src/components/watch/WorkerSettings.tsx — the top of a pool host's 設定 page (plan "WWD-7 design", Worker settings; `pool.control.v1`).
// Rows: 接工作, heavy / light slots, the owner rule (only when the host reports an owner), the labels (read only). A change is one whole `PUT`;
// the value stays "pending" until the next facts carry it, and after 15 s says so. A refusal puts the value back and says why under the row.
// A device token cannot change anything (403): the whole section turns read only with one notice.
import { useEffect, useRef, useState } from 'react'
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
type Fields = Partial<Pick<PoolWorker, 'enabled' | 'owner_rule'>> & { slots?: Partial<PoolWorker['slots']> }
/** What to change: fields, or a function of the settings the change lands on — a stepper press is "one more", whatever the settings are by then. */
type Patch = Fields | ((on: PoolWorker) => Fields)
/** `patch` is kept for a retry: it is applied to whatever the settings are at the click, never to the ones at the failure. */
interface Failure { field: Field; text: string; patch?: Patch }

const INPUT = 'w-20 px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary disabled:opacity-50'
const BTN = 'px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary text-xs cursor-pointer disabled:opacity-50'

function Stepper({ value, onStep, label, testId, disabled }: { value: number; onStep: (delta: number) => void; label: string; testId: string; disabled: boolean }) {
  return (
    <span className="flex items-center gap-2" data-testid={testId}>
      <button type="button" className={BTN} aria-label={`${label} −`} data-testid={`${testId}-dec`} disabled={disabled || value <= 0} onClick={() => onStep(-1)}>−</button>
      <span className="w-6 text-center text-sm text-text-primary" data-testid={`${testId}-value`}>{value}</span>
      <button type="button" className={BTN} aria-label={`${label} +`} data-testid={`${testId}-inc`} disabled={disabled} onClick={() => onStep(1)}>+</button>
    </span>
  )
}

/** The owner rule: `hid<N>m` (and the default, "") as a minutes box; any other text as it is. The unsent text outlives the tab (worker-draft-memory). */
function OwnerRuleBox({ alias, rule, disabled, onCommit, onStep }: { alias: string; rule: string; disabled: boolean; onCommit: (rule: string) => void; onStep: (delta: number) => void }) {
  const t = useI18nStore((s) => s.t)
  const minutes = ownerMinutes(rule)
  const asMinutes = rule === '' || minutes !== null
  const stored = minutes !== null ? String(minutes) : rule
  const key = workerDraftKey(alias, 'owner_rule')
  // Only what the reader typed is state: a box with nothing typed follows the stored value (so new facts show through at once).
  const [typed, setTyped] = useState<string | null>(() => readWorkerDraft(key) ?? null)
  const [bad, setBad] = useState(false)
  const draft = typed ?? stored
  const setDraft = (text: string) => { writeWorkerDraft(key, text); setTyped(text); setBad(false) }
  const commit = () => {
    if (draft === stored) { forgetWorkerDraft(key); setTyped(null); return }
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
    setTyped(null)
    if (next === rule) return
    onCommit(next)
  }
  // the stepper moves from the stored minutes (the host's default, 5, when the rule is empty) and sends at once; a half-typed value is dropped for it
  const step = (d: number) => {
    forgetWorkerDraft(key)
    setTyped(null)
    onStep(d)
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

/** What `to` changed against `from`, field by field. */
function changedFields(from: PoolWorker, to: PoolWorker): Fields {
  const out: Fields = {}
  if (to.enabled !== from.enabled) out.enabled = to.enabled
  if (to.owner_rule !== from.owner_rule) out.owner_rule = to.owner_rule
  const slots: Partial<PoolWorker['slots']> = {}
  if (to.slots.heavy !== from.slots.heavy) slots.heavy = to.slots.heavy
  if (to.slots.light !== from.slots.light) slots.light = to.slots.light
  if (Object.keys(slots).length > 0) out.slots = slots
  return out
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
  // Latest wins: while a PUT is out, further presses only move `wanted` (the whole settings the reader is asking for, drawn at once); when the
  // PUT in the air is answered, one more PUT carries the latest `wanted`. So PUTs never overlap, none is dropped, and the one left on the host is the last.
  const [wanted, setWanted] = useState<PoolWorker | null>(null)
  const wantedRef = useRef<PoolWorker | null>(null)
  const inFlight = useRef(false)
  const lastField = useRef<Field>('enabled') // the row of the latest press: a PUT that fails is shown under the row that asked for it last
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
  const shown: PoolWorker = wanted ?? pending?.sent ?? current
  const locked = readOnly
  const running = host.facts?.worker.running ?? { heavy: 0, light: 0 }

  const change = async (field: Field, patch: Patch): Promise<void> => {
    const base = wantedRef.current ?? pending?.sent ?? current
    const fields = typeof patch === 'function' ? patch(base) : patch
    const next: PoolWorker = { ...base, ...fields, slots: { ...base.slots, ...fields.slots } }
    wantedRef.current = next
    lastField.current = field
    setWanted(next)
    setFailure(null)
    if (inFlight.current) return // the PUT in the air is followed by one with whatever is wanted by then
    inFlight.current = true
    const chainBase = pending?.base ?? current // what the rows were before this run of changes: they are marked as touched against it
    const baseAt = host.facts?.at ?? null
    // the PUT in the air: what a failure is about (its row, and what it changed against what it was built on, which is what a retry asks again)
    let attempt: { settings: PoolWorker; built: PoolWorker; field: Field } | null = null
    let built: PoolWorker = pending?.sent ?? current
    try {
      for (;;) {
        const sending: PoolWorker | null = wantedRef.current
        const target = controlSource(usePoolStore.getState().sourceHostId)
        if (sending === null || target === null) break
        attempt = { settings: sending, built, field: lastField.current }
        await putWorker(target, alias, sending)
        built = sending
        setPending({ sent: sending, base: chainBase, at: Date.now(), baseAt })
        setLate(false)
        void usePoolStore.getState().readOnce() // the facts that carry it, sooner than the next poll
        if (wantedRef.current === sending) break // nothing newer was asked while it was out
      }
    } catch (e) {
      const err = e instanceof PoolChangeError ? e : null
      const action = err?.poolError.action
      const failed = attempt ?? { settings: next, built: base, field }
      const again = (): Fields => changedFields(failed.built, failed.settings) // laid on the settings as they are at the click
      if (action === 'read_only') redraw((n) => n + 1)
      else if (err?.poolError.code === 'wwd_rejected') setFailure({ field: failed.field, text: err.detail || t('pool.worker.rejected') })
      else if (err?.poolError.code === 'bad_request') setFailure({ field: failed.field, text: t('pool.worker.bad_request') })
      else setFailure({ field: failed.field, text: t('pool.worker.no_answer', { alias, reason: err?.detail ?? '' }), patch: again })
    } finally {
      // what was wanted and not sent goes back to what the host has (the refusal is shown under the row)
      inFlight.current = false
      wantedRef.current = null
      setWanted(null)
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

  // a row is marked while a change that touched it is out or sent and not yet reported (against what the rows were before the change)
  const moved = <T,>(get: (w: PoolWorker) => T): boolean => {
    const was = get(pending?.base ?? current)
    return (pending !== null && get(pending.sent) !== was) || (wanted !== null && get(wanted) !== was)
  }
  const owner = host.facts?.owner ?? null
  const onToggle = (on: boolean) => {
    if (!on && running.heavy + running.light > 0) { setConfirmOff(true); return }
    void change('enabled', { enabled: on })
  }
  // the rows the sent change touched wait for the facts (a row is marked by what was sent, not by how the facts compare: they may hold it already)
  const pendingMark = (touched: boolean) => touched && (
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
            {pendingMark(moved((w) => w.enabled))}
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
                onStep={(d) => void change(k, (on) => ({ slots: { [k]: Math.max(0, on.slots[k] + d) } }))} />
              {pendingMark(moved((w) => w.slots[k]))}
              {rowError(k)}
            </div>
          </SettingItem>
        ))}

        {owner !== null && (
          <SettingItem label={t('pool.worker.owner_rule')} description={t('pool.worker.owner_rule_d')} disabled={locked}>
            <div className="flex flex-col items-end gap-1">
              <OwnerRuleBox alias={alias} rule={shown.owner_rule} disabled={locked} onCommit={(rule) => void change('owner_rule', { owner_rule: rule })}
                onStep={(d) => void change('owner_rule', (on) => ({ owner_rule: ownerRuleOf(Math.min(9999, Math.max(1, (ownerMinutes(on.owner_rule) ?? DEFAULT_OWNER_MINUTES) + d))) }))} />
              {pendingMark(moved((w) => w.owner_rule))}
              <span data-testid="pool-worker-owner-state" className="text-[11px] text-text-muted">
                {owner.active ? t('pool.worker.owner_active', { reason: owner.reason }) : t('pool.worker.owner_idle', { min: Math.floor(owner.idle_s / 60) })}
              </span>
              {rowError('owner_rule')}
            </div>
          </SettingItem>
        )}

        <SettingItem stacked label={t('pool.worker.labels')} description={t('pool.worker.labels_d')}>
          <span data-testid="pool-worker-labels" className="flex flex-wrap gap-1">
            {host.labels.length === 0
              ? <span className="text-xs text-text-muted">{t('pool.worker.labels_none')}</span>
              : host.labels.map((l) => <span key={l} className="px-1.5 py-0.5 rounded bg-surface-tertiary text-xs text-text-secondary max-w-full break-all">{l}</span>)}
          </span>
        </SettingItem>
      </div>
    </Section>
  )
}

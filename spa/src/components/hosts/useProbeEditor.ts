// spa/src/components/hosts/useProbeEditor.ts — what the probe-host page (and the overview's short list) share: the daemon hosts
// the list is written to, the merged entries, why writing is off, and the one write path (`run`) with its 409 / 400 words.
// The draft, the confirmations, the delete prompt and the request state live in `useFleetDraftStore` (a tab switch unmounts).
import { useEffect, useMemo } from 'react'
import { useFleetStore } from '../../lib/fleet/store'
import { useFleetDaemons }from '../../lib/fleet/use-fleet-daemons'
import { canon, mergedProbeEntries, missingAliases, useFleetConfigStore, type DaemonOutcome, type MissingAlias, type ProbeBase, type ProbeEdit } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { useI18nStore } from '../../stores/useI18nStore'
import { readAllFleets } from '../../lib/fleet/reread'
import { hostLabel, hostLookOf } from '../../lib/host-look'

export type T = (k: string, p?: Record<string, string>) => string
export const MAX_PROBE_HOSTS = 16
export const labelOf = (id: string) => hostLabel(id, hostLookOf(id))
export const labels = (ids: string[]) => ids.map(labelOf).join(', ')

/** A fresh id that no row (on either daemon) already uses. */
export function newProbeId(taken: Set<string>): string {
  for (;;) {
    const b = new Uint8Array(4)
    crypto.getRandomValues(b)
    const id = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
    if (!taken.has(id)) return id
  }
}

/** What each daemon answered to a write, in words: where it was written, where it was not and why. */
export function outcomeText(t: T, outcomes: DaemonOutcome[]): string {
  const lines: string[] = []
  const saved = outcomes.filter((o) => o.ok).map((o) => o.hostId)
  const conflicted = outcomes.filter((o) => o.conflict).map((o) => o.hostId)
  if (saved.length) lines.push(t('fleet_hosts.probe.outcome_saved', { hosts: labels(saved) }))
  if (conflicted.length) lines.push(t('fleet_hosts.probe.outcome_conflict', { hosts: labels(conflicted) }))
  for (const o of outcomes) if (!o.ok && !o.conflict) lines.push(t('fleet_hosts.probe.outcome_failed', { host: labelOf(o.hostId), text: o.text ?? '' }))
  if (saved.length && saved.length < outcomes.length) lines.push(t('fleet_hosts.probe.outcome_diverged'))
  return lines.join('\n')
}

export function missingText(t: T, rows: MissingAlias[]): string {
  return rows.map((r) => t('fleet_hosts.probe.only_on', { name: r.name || r.id, hosts: labels(r.presentOn), missing: labels(r.missing) })).join('\n')
}

export interface RunOpts { confirmDropped?: boolean; confirmChanged?: boolean; base?: ProbeBase }

export function useProbeEditor() {
  const t = useI18nStore((s) => s.t) as T
  const { known, reachable: hostIds } = useFleetDaemons()
  const key = hostIds.join('|')
  const byHost = useFleetConfigStore((s) => s.byHost)
  const entries = useMemo(() => mergedProbeEntries(hostIds, byHost), [hostIds, byHost])
  const ready = hostIds.length > 0 && hostIds.every((id) => byHost[id]?.status === 'ready')
  const unreachable = known.filter((id) => !hostIds.includes(id))
  const failed = hostIds.filter((id) => byHost[id]?.status === 'error')
  const writable = ready && unreachable.length === 0
  const halfRows = useMemo(() => new Map(missingAliases(hostIds, entries).map((m) => [m.id, m])), [hostIds, entries])

  useEffect(() => {
    if (hostIds.length) void useFleetConfigStore.getState().loadMany(hostIds)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` is the host id list
  }, [key])

  const loadError = failed.length > 0
    ? t('fleet_hosts.probe.load_error', { hosts: labels(failed), text: failed.map((id) => byHost[id]?.error ?? '').filter(Boolean).join('; ') })
    : null
  /** Why nothing can be written right now, in words (null: it can). */
  const blocked = unreachable.length > 0
    ? t('fleet_hosts.probe.unreachable', { hosts: labels(unreachable) })
    : loadError ?? (!ready ? t('fleet_hosts.probe.loading') : null)

  /** The one write path. `from` says where the failure text is shown (the form, or the page's list-level line). */
  const run = async (edit: ProbeEdit, from: 'form' | 'page', opts: RunOpts = {}): Promise<boolean> => {
    const drafts = useFleetDraftStore.getState
    if (!writable || drafts().ui.busy) return false
    const ui = drafts().setUi
    ui({ busy: true, dialogError: null, listError: null, changed: null })
    const show = (text: string) => ui(from === 'form' ? { dialogError: text } : { listError: text })
    try {
      const r = await useFleetConfigStore.getState().saveProbe(hostIds, edit, opts)
      if (r.kind === 'saved') { readAllFleets(); drafts().endProbe(); drafts().setProbeConfirm(null); drafts().setDeleting(null); return true }
      else if (r.kind === 'confirm') drafts().setProbeConfirm({ edit, notices: r.notices, base: opts.base })
      else if (r.kind === 'missing_alias') show(missingText(t, r.rows))
      else if (r.kind === 'changed') { ui({ changed: r.hosts }); show(t('fleet_hosts.probe.changed_on', { hosts: labels(r.hosts) })) }
      // The form (and its draft) stays: both daemons were read again, and the text says what each one took.
      else if (r.kind === 'conflict') { drafts().setProbeConfirm(null); show(`${t('fleet_hosts.probe.conflict')}\n${outcomeText(t, r.outcomes)}`) }
      else show(r.outcomes.length ? outcomeText(t, r.outcomes) : r.text)
      return false
    } finally {
      ui({ busy: false })
    }
  }

  return { t, known, hostIds, byHost, entries, ready, unreachable, failed, writable, halfRows, loadError, blocked, run }
}

/**
 * Every way out of a probe host's draft (another probe host, a paired host, the paired phones, `+`): if the draft has unsaved
 * changes (compared by content, not by key order) ask first (「捨棄未儲存的變更？」). Leaving ends the draft, so a NEW unsaved
 * probe host leaves no row behind. `nextId`: the probe host being opened, null for anything else.
 */
export function leaveProbeDraft(nextId: string | null): boolean {
  const drafts = useFleetDraftStore.getState()
  const p = drafts.probe
  if (!p || (nextId !== null && p.initial.id === nextId)) return true
  const dirty = JSON.stringify(canon(p.draft)) !== JSON.stringify(canon(p.initial))
  if (dirty && !window.confirm(useI18nStore.getState().t('fleet_hosts.entry.discard_confirm'))) return false
  drafts.endProbe()
  drafts.setUi({ dialogError: null, changed: null })
  return true
}

/** Select a probe host's page, unless the reader keeps an unsaved draft of another one. */
export function selectProbeGuarded(sel: { id: string; isNew: boolean }): boolean {
  if (!leaveProbeDraft(sel.id)) return false
  useFleetDraftStore.getState().selectProbe(sel)
  return true
}

/** The sidebar's `+` and the overview's 「新增觀察主機」: select a fresh id as a new probe host; its page starts the blank draft. */
export function startNewProbe(): boolean {
  const drafts = useFleetDraftStore.getState()
  const cfg = useFleetConfigStore.getState().byHost
  const taken = new Set(mergedProbeEntries(Object.keys(cfg), cfg).map((e) => e.id))
  const snap = useFleetStore.getState().snapshot
  if (snap) for (const id of snap.order) taken.add(id)
  const id = newProbeId(taken)
  if (!leaveProbeDraft(id)) return false
  drafts.selectProbe({ id, isNew: true })
  return true
}

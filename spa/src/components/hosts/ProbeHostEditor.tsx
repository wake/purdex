import { useEffect } from 'react'
import { WarningCircle } from '@phosphor-icons/react'
import { probeBaseOf, type ProbeEdit, type ProbeEntry } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { ProbeHostForm } from './ProbeHostForm'
import { ProbeCheck } from './ProbeCheck'
import { labelOf, labels, useProbeEditor } from './useProbeEditor'

/**
 * One probe host's editing part (fleet spec §8.2): the warnings, 「樣式與連線」 (the form, written to BOTH daemons) and 「連線檢查」.
 * The Hosts page (`ProbeHostPage`) wraps it with the host's header and 刪除; the watcher area's 設定 page shows it alone.
 * The draft and the confirmations live in `useFleetDraftStore`, so a tab switch keeps them. A new probe host (the sidebar's `+`)
 * starts as a blank draft here.
 */
export function ProbeHostEditor({ probeId }: { probeId: string }) {
  const { t, hostIds, byHost, entries, ready, unreachable, writable, halfRows, loadError, blocked, run } = useProbeEditor()
  const sel = useFleetDraftStore((s) => s.selectedProbe)
  const session = useFleetDraftStore((s) => s.probe)
  const confirm = useFleetDraftStore((s) => s.probeConfirm)
  const { busy, dialogError, listError, changed } = useFleetDraftStore((s) => s.ui)
  const drafts = useFleetDraftStore.getState
  const entry = entries.find((e) => e.id === probeId)
  const mine = session && session.initial.id === probeId ? session : null
  const isNewHere = sel?.id === probeId && sel.isNew

  // Start the draft once the daemons are read: from the stored entry, or blank for a new probe host.
  useEffect(() => {
    if (mine || !ready) return
    if (entry) drafts().beginProbe(entry, false, probeBaseOf(hostIds, byHost, entry.id))
    else if (isNewHere) drafts().beginProbe({ id: probeId, name: '', icon: '', icon_weight: '', aliases: {} }, true, probeBaseOf(hostIds, byHost, null))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- begin when the inputs below change; `drafts` is a getter
  }, [mine, ready, entry, isNewHere, probeId])

  const finish = (edit: ProbeEdit, ok: boolean) => { if (ok && edit.kind === 'remove') drafts().selectProbe(null) }
  const save = async (e: ProbeEntry, confirmChanged = false) => {
    const ok = await run({ kind: 'upsert', entry: e }, 'form', { base: mine?.base, confirmChanged })
    // Saved: it is stored now, so the selection is no longer "new" (the begin-draft effect would open a second blank draft).
    if (ok && isNewHere) drafts().selectProbe({ id: probeId, isNew: false })
  }
  const cancel = () => {
    drafts().endProbe()
    drafts().setUi({ dialogError: null, changed: null })
    if (mine?.isNew) drafts().selectProbe(null)
  }
  const half = halfRows.get(probeId)
  const notFound = ready && !entry && !isNewHere && !mine

  return (
    <>
      {unreachable.length > 0 && <p data-testid="probe-unreachable" className="mb-3 text-xs text-status-warning-text">{t('fleet_hosts.probe.unreachable', { hosts: labels(unreachable) })}</p>}
      {loadError && <p data-testid="probe-load-error" className="mb-3 text-xs text-status-warning-text whitespace-pre-wrap">{loadError}</p>}
      {listError && <p data-testid="probe-list-error" className="mb-3 text-xs text-status-warning-text whitespace-pre-wrap">{listError}</p>}
      {half && (
        <p data-testid={`probe-half-${probeId}`} className="mb-3 text-xs text-status-warning-text">
          {t('fleet_hosts.probe.only_on', { name: half.name || half.id, hosts: labels(half.presentOn), missing: labels(half.missing) })}
        </p>
      )}
      {entry?.mismatch && (
        <p data-testid={`probe-mismatch-${probeId}`} className="mb-3 flex items-center gap-2 text-xs text-status-warning-text">
          {t('fleet_hosts.probe.mismatch')}
          <button type="button" data-testid={`probe-rewrite-${probeId}`} disabled={!writable || busy || !!half}
            onClick={() => void run({ kind: 'rewrite', id: probeId }, 'page')}
            className="px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary cursor-pointer disabled:opacity-50">{t('fleet_hosts.probe.rewrite')}</button>
        </p>
      )}
      {confirm && (
        <div data-testid="probe-dropped-confirm" className="mb-3 p-3 rounded border border-border-default bg-surface-secondary text-xs">
          <p className="flex items-center gap-1.5 font-medium text-text-primary"><WarningCircle size={14} />{t('fleet_hosts.dropped.title')}</p>
          <ul className="mt-1 text-text-secondary list-disc pl-5">
            {confirm.notices.map((n) => (
              <li key={n.hostId}>
                {t('fleet_hosts.dropped.on', { host: labelOf(n.hostId), count: String(n.count) })}
                {n.invalid ? ` ${t('fleet_hosts.dropped.invalid')}` : ''}
                {n.reasons.length ? ` — ${n.reasons.join('; ')}` : ''}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-text-muted">{t('fleet_hosts.dropped.warn')}</p>
          <div className="flex gap-2 mt-2">
            <button type="button" data-testid="probe-dropped-save" disabled={busy || !writable}
              onClick={() => void run(confirm.edit, 'page', { confirmDropped: true, base: confirm.base }).then((ok) => finish(confirm.edit, ok))}
              className="px-3 py-1 rounded bg-accent text-white cursor-pointer disabled:opacity-50">{t('fleet_hosts.dropped.save_anyway')}</button>
            <button type="button" data-testid="probe-dropped-cancel" onClick={() => drafts().setProbeConfirm(null)}
              className="px-3 py-1 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
          </div>
        </div>
      )}

      {notFound ? (
        <p data-testid="probe-page-missing" className="text-sm text-text-muted">{t('fleet_hosts.entry.not_found')}</p>
      ) : !mine ? (
        <p data-testid="probe-state" className="text-sm text-text-muted">{t('fleet_hosts.probe.loading')}</p>
      ) : (
        <section>
          <h3 className="text-sm font-semibold mb-2">{t('fleet_hosts.entry.section_style')}</h3>
          <ProbeHostForm key={mine.initial.id} draft={mine.draft} hostIds={hostIds} isNew={mine.isNew} busy={busy}
            blocked={writable ? null : blocked} error={dialogError} onChange={(fn) => drafts().setProbeDraft(fn)} onSave={(e) => void save(e)} onCancel={cancel}>
            {changed && (
              <button type="button" data-testid="probe-overwrite" disabled={busy || !writable} onClick={() => void save(mine.draft, true)}
                className="px-3 py-1.5 rounded text-xs bg-status-warning text-text-inverse cursor-pointer disabled:opacity-50">{t('fleet_hosts.probe.overwrite')}</button>
            )}
          </ProbeHostForm>
        </section>
      )}

      {entry && <ProbeCheck aliases={entry.aliases} />}
    </>
  )
}

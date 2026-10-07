// spa/src/components/hosts/RelaySection.tsx — Hosts › 接力 (lead-team-relay spec §8.7 (a)): the two per-host
// self-relay switches the daemon reads when a session asks to relay, and the one line that says a member has
// none. Same gate and queued CAS saves as the other host config sections; a toggle is one PUT of the whole
// `relay` object, so two quick clicks serialize through the queue instead of racing on the revision.
import { useCallback, useState } from 'react'
import { HostConfigConflictError, type RelaySwitches } from '../../lib/host-config-api'
import { hostConfigQueueKey, queueHostConfigSave } from '../../lib/host-config-queue'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ToggleSwitch } from '../settings/ToggleSwitch'
import { HostConfigNotice, useHostConfigGate } from './HostConfigNotice'

export function RelaySection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const known = entry.status === 'ready' || entry.status === 'unsupported'
  const unsupported = known && !entry.relaySupported

  const toggle = useCallback((field: keyof RelaySwitches) => {
    setPending(true)
    setSaveError(null)
    // A click is a TOGGLE of the value stored when its task RUNS (PR #1742 R1): two quick clicks queue two tasks
    // that both captured the same rendered `checked`; flipping the store's current value makes the second undo
    // the first, as the person meant, instead of writing the same value twice.
    void queueHostConfigSave(hostConfigQueueKey(hostId, 'relay'), async () => {
      try {
        const current = useHostConfigStore.getState().byHost[hostId]?.relay
        if (!current) return
        await useHostConfigStore.getState().saveRelay(hostId, { ...current, [field]: !current[field] })
      } catch (err) {
        setSaveError(err instanceof HostConfigConflictError
          ? t('host_config.conflict')
          : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
      } finally {
        setPending(false)
      }
    })
  }, [hostId, t])

  // Not locked while a save is in flight: the queue serializes a second toggle behind the first (and the task reads
  // the store when it runs), so a fast second click is kept, not dropped.
  const locked = !editable

  return (
    <div className="max-w-3xl" data-testid="relay-section" aria-busy={pending || undefined}>
      <h2 className="text-lg font-semibold mb-4">{t('hosts.relay')}</h2>
      <HostConfigNotice notice={notice} />
      {unsupported ? (
        <p data-testid="relay-unsupported" className="text-xs text-text-muted">{t('hosts.relay.unsupported')}</p>
      ) : (
        <>
          <p className="mb-4 text-xs text-text-muted">{t('hosts.relay.desc')}</p>
          <div className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.self_solo')}</span>
              <span className={locked ? 'opacity-50 pointer-events-none' : ''}>
                <ToggleSwitch testId="relay-self-solo" label={t('hosts.relay.self_solo')} checked={entry.relay.self_solo}
                  onChange={() => { if (!locked) toggle('self_solo') }} />
              </span>
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.self_lead')}</span>
              <span className={locked ? 'opacity-50 pointer-events-none' : ''}>
                <ToggleSwitch testId="relay-self-lead" label={t('hosts.relay.self_lead')} checked={entry.relay.self_lead}
                  onChange={() => { if (!locked) toggle('self_lead') }} />
              </span>
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.member')}</span>
              <span data-testid="relay-member-note" className="text-xs text-text-muted">{t('hosts.relay.member_note')}</span>
            </div>
          </div>
          {saveError && <p data-testid="relay-save-error" className="mt-3 text-xs text-status-warning whitespace-pre-wrap">{saveError}</p>}
        </>
      )}
    </div>
  )
}

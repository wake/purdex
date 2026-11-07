// spa/src/components/hosts/CompactionSection.tsx — Hosts › 壓縮: the host's compaction defaults for solo sessions and
// for teams (auto compaction, its threshold, idle compaction). One `compaction` row with one revision: every action is
// one PUT of the whole object on the one queue key, built from the store when its task RUNS, so two quick actions
// serialize instead of racing on the revision.
import { useCallback, useState } from 'react'
import { HostConfigConflictError, type CompactionSet } from '../../lib/host-config-api'
import { hostConfigQueueKey, queueHostConfigSave } from '../../lib/host-config-queue'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { clampThreshold, ThresholdStepper } from '../settings/ThresholdStepper'
import { ToggleSwitch } from '../settings/ToggleSwitch'
import { HostConfigNotice, HostConfigProblemNotice, useHostConfigGate } from './HostConfigNotice'

type Who = 'solo' | 'team'
const ROW = 'flex items-center justify-between gap-4 px-3 py-2'

export function CompactionSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const [saveError, setSaveError] = useState<string | null>(null)
  const unsupported = (entry.status === 'ready' || entry.status === 'unsupported') && !entry.compactionSupported
  const locked = !editable

  const change = useCallback((who: Who, patch: (s: CompactionSet) => CompactionSet) => {
    void queueHostConfigSave(hostConfigQueueKey(hostId, 'compaction'), async () => {
      try {
        const current = useHostConfigStore.getState().byHost[hostId]?.compaction
        if (!current) return
        await useHostConfigStore.getState().saveCompaction(hostId, { ...current, [who]: patch(current[who]) })
        setSaveError(null) // a later success clears an earlier failure's message
      } catch (err) {
        setSaveError(err instanceof HostConfigConflictError
          ? t('host_config.conflict')
          : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
      }
    })
  }, [hostId, t])

  const lock = locked ? 'opacity-50 pointer-events-none' : ''
  const row = (who: Who) => {
    const set = entry.compaction[who]
    const name = t(`hosts.compaction.${who}`)
    return (
      <div key={who} className={ROW} data-testid={`compaction-${who}`}>
        <span className="text-sm text-text-primary">{name}</span>
        <span className={`flex items-center gap-4 ${lock}`}>
          <span className="flex items-center gap-2 text-xs text-text-muted">
            {t('hosts.compaction.auto')}
            <ToggleSwitch testId={`compaction-${who}-auto`} label={`${name} ${t('hosts.compaction.auto')}`} checked={set.auto}
              onChange={() => { if (!locked) change(who, (s) => ({ ...s, auto: !s.auto })) }} />
          </span>
          <span className="flex items-center gap-2 text-xs text-text-muted">
            {t('hosts.compaction.threshold')}
            <ThresholdStepper testId={`compaction-${who}-threshold`} label={`${name} ${t('hosts.compaction.threshold')}`}
              value={set.threshold} disabled={locked || !set.auto}
              onStep={(d) => change(who, (s) => ({ ...s, threshold: clampThreshold(s.threshold + d) }))} />
          </span>
          <span className="flex items-center gap-2 text-xs text-text-muted">
            {t('hosts.compaction.idle')}
            <ToggleSwitch testId={`compaction-${who}-idle`} label={`${name} ${t('hosts.compaction.idle')}`} checked={set.idle}
              onChange={() => { if (!locked) change(who, (s) => ({ ...s, idle: !s.idle })) }} />
          </span>
        </span>
      </div>
    )
  }

  return (
    <div className="max-w-3xl" data-testid="compaction-section">
      <h2 className="text-lg font-semibold mb-4">{t('hosts.compaction')}</h2>
      <HostConfigNotice notice={notice} />
      <HostConfigProblemNotice problem={entry.problems.compaction} title="hosts.compaction" />
      {unsupported ? (
        <p data-testid="compaction-unsupported" className="text-xs text-text-muted">{t('hosts.compaction.unsupported')}</p>
      ) : (
        <>
          <p className="mb-4 text-xs text-text-muted">{t('hosts.compaction.desc')}</p>
          <div className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
            {row('solo')}
            {row('team')}
          </div>
          <p className="mt-3 text-xs text-text-muted">{t('hosts.compaction.note')}</p>
          {saveError && <p data-testid="compaction-save-error" className="mt-3 text-xs text-status-warning-text whitespace-pre-wrap">{saveError}</p>}
        </>
      )}
    </div>
  )
}

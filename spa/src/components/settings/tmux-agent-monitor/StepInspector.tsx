import type { AgentMonitorProjectionSummary, AgentMonitorStep } from '../../../lib/host-api'
import { useI18nStore } from '../../../stores/useI18nStore'

interface StepInspectorProps {
  step: AgentMonitorStep | null
  projection: AgentMonitorProjectionSummary | null
}

export function StepInspector({ step, projection }: StepInspectorProps) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-border-default bg-surface-secondary overflow-hidden">
        <div className="border-b border-border-default px-4 py-3 text-sm font-medium text-text-primary">
          {t('settings.monitor.projection')}
        </div>
        <div className="space-y-2 px-4 py-3 text-sm text-text-secondary">
          <div>{t('settings.monitor.projection.session')}: {projection?.tmux_session || '-'}</div>
          <div>{t('settings.monitor.projection.pane')}: {projection?.pane_id || '-'}</div>
          <div>{t('settings.monitor.projection.primary')}: {projection?.primary_frame_id || '-'}</div>
          <div>{t('settings.monitor.projection.top')}: {projection?.top_frame_id || '-'}</div>
          <div>{t('settings.monitor.projection.agent')}: {projection?.top_agent_type || '-'}</div>
          <div>{t('settings.monitor.projection.latest_chain')}: {projection?.latest_chain_id || '-'}</div>
        </div>
      </div>

      <div className="rounded-lg border border-border-default bg-surface-secondary overflow-hidden">
        <div className="border-b border-border-default px-4 py-3 text-sm font-medium text-text-primary">
          {t('settings.monitor.inspect')}
        </div>
        {!step && (
          <div className="px-4 py-6 text-sm text-text-secondary">{t('settings.monitor.no_step_selected')}</div>
        )}
        {step && (
          <div className="space-y-4 px-4 py-3">
            <div className="text-xs font-medium uppercase tracking-wide text-text-secondary">
              {t('settings.monitor.selected_step')}
            </div>
            <div className="space-y-1 text-sm text-text-secondary">
              <div>{t('settings.monitor.step.kind')}: {step.kind}</div>
              <div>{t('settings.monitor.step.decision')}: {step.decision}</div>
              <div>{t('settings.monitor.step.reason')}: {step.reason}</div>
              <div>{t('settings.monitor.step.agent')}: {step.agent_type || '-'}</div>
              <div>{t('settings.monitor.step.frame')}: {step.frame_id || '-'}</div>
            </div>

            <InspectorBlock label={t('settings.monitor.step.payload')} value={step.payload_json} />
            <InspectorBlock label={t('settings.monitor.step.before')} value={step.before_json} />
            <InspectorBlock label={t('settings.monitor.step.after')} value={step.after_json} />
          </div>
        )}
      </div>
    </div>
  )
}

function InspectorBlock({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="mb-1 text-xs uppercase tracking-wide text-text-secondary">{label}</div>
      <pre className="overflow-auto rounded-md bg-surface-primary p-3 text-xs text-text-primary whitespace-pre-wrap">
        {value}
      </pre>
    </div>
  )
}

// spa/src/components/watch/host/SettingsTab.tsx — the host page's 設定 tab (monitoring tabs spec M46–M52; plan MON-9a-1). One layout for every host, with or without a daemon: the worker
// settings (slots, owner rule, on/off), the pauses, then — added by the later MON-9 pieces — alerts, push and the sidebar look, and the guardian's start settings last, collapsed, only on a
// host that has a daemon. Everything is read from the pool by alias; the guardian row is the one thing read from the App host that fronts the pool host.
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostStore } from '../../../stores/useHostStore'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { Section } from '../../hosts/form-fields'
import { AlertSettings } from './AlertSettings'
import { LookSettings } from './LookSettings'
import { GuardianSection } from '../WatchSettings'
import { WorkerExceptions } from '../WorkerExceptions'
import { WorkerSettings } from '../WorkerSettings'

export function SettingsTab({ host }: { host: MonitorHost }) {
  const t = useI18nStore((s) => s.t)
  const daemon = host.daemonHostId
  // The guardian settings are the App host's: a disconnected one cannot be read or written.
  const offline = useHostStore((s) => daemon !== null && s.runtime[daemon]?.status !== 'connected')
  return (
    // Keyed by the pool host: a pane can change host in place, and what the sections hold in state (picks, the guardian's row and revision) is the previous host's.
    <div key={host.hostId} data-testid="settings-tab">
      <WorkerSettings alias={host.alias} />
      <WorkerExceptions alias={host.alias} />
      <AlertSettings host={host} />
      <LookSettings host={host} />
      {daemon !== null && (
        <Section title={t('watch.set.guardian')} defaultOpen={false}>
          <GuardianSection hostConfigId={daemon} hostId={host.hostId} offline={offline} />
        </Section>
      )}
    </div>
  )
}

import { useHostStore } from '../../stores/useHostStore'
import { DaemonLogBlock } from './DaemonLogBlock'
import { CrashLogsBlock } from './CrashLogsBlock'

interface Props {
  hostId: string
}

export function LogsSection({ hostId }: Props) {
  const host = useHostStore((s) => s.hosts[hostId])

  if (!host) return null

  return (
    <div className="max-w-2xl space-y-6">
      <h2 className="text-lg font-semibold">{host.name}</h2>
      <DaemonLogBlock hostId={hostId} />
      <CrashLogsBlock hostId={hostId} />
    </div>
  )
}

import { useHostStore } from '../../stores/useHostStore'
import { useHostLook } from '../../lib/host-look'
import { DaemonLogBlock } from './DaemonLogBlock'
import { CrashLogsBlock } from './CrashLogsBlock'

interface Props {
  hostId: string
}

export function LogsSection({ hostId }: Props) {
  const host = useHostStore((s) => s.hosts[hostId])
  const look = useHostLook(hostId)

  if (!host) return null

  return (
    <div className="max-w-2xl space-y-6">
      <h2 className="text-lg font-semibold">{look.name}</h2>
      <DaemonLogBlock hostId={hostId} />
      <CrashLogsBlock hostId={hostId} />
    </div>
  )
}

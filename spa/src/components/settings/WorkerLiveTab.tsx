// spa/src/components/settings/WorkerLiveTab.tsx — Settings → Worker → Workers: one host's live workers.
import { HostWorkerRows } from '../HostWorkerRows'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'

export function WorkerLiveTab({ hostId }: { hostId?: string }) {
  if (!hostId) return null
  return (
    <HostWorkerRows
      hostId={hostId}
      testIdPrefix="worker-settings-live"
      onOpen={(id) => { openWorkerTab({ kind: 'execution', executionId: id, host: hostId }) }}
    />
  )
}

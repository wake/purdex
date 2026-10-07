// spa/src/components/settings/WorkerLiveTab.tsx — Settings → Worker → Workers: one host's live workers.
// On a daemon with `conversations.scope.v1` the test-cwd workers live in 測試用 instead; on an older one every row stays.
import { HostWorkerRows } from '../HostWorkerRows'
import { selectConversationsScope, useNexHostStore } from '../../stores/useNexHostStore'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'

export function WorkerLiveTab({ hostId }: { hostId?: string }) {
  if (!hostId) return null
  return <LiveList hostId={hostId} />
}

function LiveList({ hostId }: { hostId: string }) {
  const scoped = useNexHostStore(selectConversationsScope(hostId))
  // Whether the daemon's capabilities are known yet: until then (no entry, or still loading) the safe side is to hide
  // test rows, not to treat the host as an older daemon — they would show up here and vanish a moment later.
  const phase = useNexHostStore((s) => s.byHost[hostId]?.phase)
  const unknown = phase === undefined || phase === 'unknown' || phase === 'loading'
  return (
    <HostWorkerRows
      hostId={hostId}
      testIdPrefix="worker-settings-live"
      filter={scoped || unknown ? 'normal' : undefined}
      onOpen={(id) => { openWorkerTab({ kind: 'execution', executionId: id, host: hostId }) }}
    />
  )
}

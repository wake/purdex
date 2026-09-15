// spa/src/components/hosts/nex/NexHostSection.tsx — Host → "Nex" sub-page
// (P-B.3 Task 7). Fetches `/api/info` (→ info.nex) and `/api/config`
// (→ config.nex) and composes the three Nex cards built in Tasks 4-6:
// NexEngineStatus, NexConfigForm, NexExecutionsTable.
//
// See spa-context.md "Host reconnect refetch" + ruling G for the
// disconnected→connected refetch/remount contract this section must honor.
import { useEffect, useState } from 'react'
import { useHostStore } from '../../../stores/useHostStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { hostFetch, fetchInfo } from '../../../lib/host-api'
import type { ConfigData, NexConfig, NexInfo } from '../../../lib/host-api'
import NexEngineStatus from './NexEngineStatus'
import NexConfigForm from './NexConfigForm'
import NexExecutionsTable from './NexExecutionsTable'

interface Props {
  hostId: string
}

// Fetches both `/api/info` and `/api/config` for `hostId` and hands the
// `nex` slice of each to the given setters. `isCancelled` is checked right
// before each write so a response for a torn-down effect (unmount, hostId
// change, or a StrictMode dev double-invoke) never lands — this is a plain
// closed-over `let cancelled = false` per effect run, never a persistent
// "mounted" ref (a ref set to false in a cleanup with no matching `= true`
// in the effect body would stay false forever under StrictMode's
// mount→cleanup→mount and silently drop every future write).
function loadNexData(
  hostId: string,
  isCancelled: () => boolean,
  setInfo: (info: NexInfo | null) => void,
  setConfig: (config: NexConfig | undefined) => void,
) {
  fetchInfo(hostId)
    .then((r) => (r.ok ? r.json() : null))
    .then((data) => {
      if (isCancelled() || !data) return
      setInfo(data.nex ?? null)
    })
    .catch(() => {})

  hostFetch(hostId, '/api/config')
    .then((r) => (r.ok ? r.json() : null))
    .then((data: ConfigData | null) => {
      if (isCancelled() || !data) return
      setConfig(data.nex)
    })
    .catch(() => {})
}

export function NexHostSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])

  const [info, setInfo] = useState<NexInfo | null>(null)
  const [config, setConfig] = useState<NexConfig | undefined>(undefined)
  // Bumped only on a disconnected→connected transition (see below). Doubles
  // as the fetch effect's retrigger and as <NexEngineStatus key={generation}>
  // so the card remounts and re-requests /v1/host + /v1/capabilities even
  // when `info.ready` itself did not change value across the reconnect
  // (ruling G).
  const [generation, setGeneration] = useState(0)

  // Host reconnect detector, using React's documented "adjusting state
  // during rendering" idiom (not an effect) to react to the runtime status
  // transition: https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes.
  // OverviewSection.tsx:20-39's ref+effect version of this same idiom trips
  // `react-hooks/set-state-in-effect` when isolated the way this section
  // needs it (verified against an isolated repro), so this uses the
  // render-time variant of the same disconnected→connected transition
  // detection instead.
  const [prevStatus, setPrevStatus] = useState(runtime?.status)
  if (runtime?.status !== prevStatus) {
    const prev = prevStatus
    setPrevStatus(runtime?.status)
    if (prev !== 'connected' && runtime?.status === 'connected') {
      setGeneration((g) => g + 1)
    }
  }

  // Initial load, reload on `hostId` change (switching hosts), and reload
  // again whenever `generation` is bumped above by a reconnect.
  useEffect(() => {
    let cancelled = false
    loadNexData(hostId, () => cancelled, setInfo, setConfig)
    return () => {
      cancelled = true
    }
  }, [hostId, generation])

  const isOffline = !runtime || runtime.status !== 'connected'
  if (isOffline && info === null) {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.load_failed')}</p>
      </div>
    )
  }

  // NexEngineStatus already refetches its own Nexen host/capabilities data
  // when its own Refresh button is clicked (it bumps an internal `tick`);
  // this handler only refreshes the daemon-side /api/info.nex data feeding
  // the badge/effective-config state, so the two refreshes don't duplicate
  // the same request.
  const handleRefresh = () => {
    fetchInfo(hostId)
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data) setInfo(data.nex ?? null)
      })
      .catch(() => {})
  }

  const handleConfigSaved = (cfg: ConfigData) => {
    setConfig(cfg.nex)
  }

  return (
    <div className="max-w-2xl space-y-6">
      <NexEngineStatus key={generation} hostId={hostId} info={info} onRefresh={handleRefresh} />
      <NexConfigForm hostId={hostId} config={config} info={info} onSaved={handleConfigSaved} />
      <NexExecutionsTable hostId={hostId} enabled={info?.ready ?? false} />
    </div>
  )
}

// spa/src/components/hosts/nex/NexHostSection.tsx — Host → "Nex" sub-page
// (spec §4.4.3). Fetches `/api/info` (→ info.nex) and `/api/config`
// (→ config.nex) and composes the three Nex cards built in Tasks 4-6:
// NexEngineStatus, NexConfigForm, NexExecutionsTable. Reloads on a host
// disconnected→connected transition (spec §4.4.3 "refetch on host reconnect").
import { useEffect, useState } from 'react'
import { useHostStore } from '../../../stores/useHostStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { hostFetch, fetchInfo } from '../../../lib/host-api'
import type { ConfigData, NexConfig, NexInfo } from '../../../lib/host-api'
import NexEngineStatus from './NexEngineStatus'
import NexConfigForm from './NexConfigForm'
import NexExecutionsTable from './NexExecutionsTable'
import { normalizeNexConfig } from './nex-config-diff'

interface Props {
  hostId: string
}

// `config` starts `undefined` — indistinguishable from "the daemon genuinely
// has no [nex] section" — so until `/api/config` has actually resolved, the
// form must not render at all: rendering it against `emptyNexConfig()` and
// letting the user Save would PUT an empty section over the host's real one.
// `loading` also covers the badge: before /api/info resolves, `info` is
// `null`, which `NexEngineStatus` would otherwise read
// as "Disabled" rather than "not loaded yet". This is only the *initial*
// (or Retry-restarted) load's status — a failed manual Refresh
// is tracked separately via `refreshError` below and must NOT flip this
// back to `'failed'`, or a transient network blip on Refresh would tear
// down cards that are already showing good data.
type LoadStatus = 'loading' | 'loaded' | 'failed'

// Fetches both `/api/info` and `/api/config` for `hostId`, hands the `nex`
// slice of each to the given setters, and records per-request success/
// failure via the status setters — a non-OK response is treated the same as
// a network rejection (both used to be silently swallowed as "no data",
// which is what let a failed /api/config load fall through to rendering the
// form against an empty draft). `isCancelled` is checked right before each
// write so a response for a torn-down effect (unmount, hostId/generation
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
  setInfoStatus: (status: LoadStatus) => void,
  setConfigStatus: (status: LoadStatus) => void,
) {
  fetchInfo(hostId)
    .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`/api/info: ${r.status}`))))
    .then((data) => {
      if (isCancelled()) return
      setInfo(data.nex ?? null)
      setInfoStatus('loaded')
    })
    .catch(() => {
      if (isCancelled()) return
      setInfoStatus('failed')
    })

  hostFetch(hostId, '/api/config')
    .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`/api/config: ${r.status}`))))
    .then((data: ConfigData) => {
      if (isCancelled()) return
      setConfig(data.nex ? normalizeNexConfig(data.nex) : undefined)
      setConfigStatus('loaded')
    })
    .catch(() => {
      if (isCancelled()) return
      setConfigStatus('failed')
    })
}

export function NexHostSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])

  const [info, setInfo] = useState<NexInfo | null>(null)
  const [config, setConfig] = useState<NexConfig | undefined>(undefined)
  const [infoStatus, setInfoStatus] = useState<LoadStatus>('loading')
  const [configStatus, setConfigStatus] = useState<LoadStatus>('loading')
  // A failed manual Refresh (the status card's own button, wired to
  // `handleRefresh` below) — deliberately independent of `infoStatus` so it
  // never re-triggers the failed-load gate and tears down cards that are
  // already showing good data. Shown as an inline error line
  // above the cards; cleared by the next successful Refresh, and also by a
  // full reload (hostId/generation change) since that supersedes it.
  const [refreshError, setRefreshError] = useState(false)
  // Bumped on a disconnected→connected transition (see the reconnect
  // detector below) and by the Retry button in the failed-load gate. Both
  // are "restart the load" triggers, so they share one counter.
  // Also keys <NexEngineStatus>: redundant today (every bump already remounts it).
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

  // Reset both statuses to 'loading' (and clear any stale refresh error)
  // the moment `hostId` or `generation` changes — same render-time-
  // adjustment idiom as the reconnect detector above (and for the same
  // reason: doing this inside the fetch effect's body, even before the
  // async calls, is a synchronous setState-in-effect that
  // `react-hooks/set-state-in-effect` flags). The effect below is then left
  // to do only the actual fetch, calling back into state exclusively from
  // its `.then`/`.catch` callbacks, which the rule accepts.
  const fetchKey = `${hostId}:${generation}`
  const [prevFetchKey, setPrevFetchKey] = useState(fetchKey)
  if (fetchKey !== prevFetchKey) {
    setPrevFetchKey(fetchKey)
    setInfoStatus('loading')
    setConfigStatus('loading')
    setRefreshError(false)
  }

  // Initial load, reload on `hostId` change (switching hosts), and reload
  // again whenever `generation` is bumped above by a reconnect or Retry.
  useEffect(() => {
    let cancelled = false
    loadNexData(hostId, () => cancelled, setInfo, setConfig, setInfoStatus, setConfigStatus)
    return () => {
      cancelled = true
    }
  }, [hostId, generation])

  // Offline always wins, even when both endpoints loaded successfully
  // earlier: a disconnected host can't take a config Save or an execution
  // action, so stale cards would be actively misleading (controller ruling
  // I). The reconnect path above reloads and re-renders them.
  const isOffline = !runtime || runtime.status !== 'connected'
  if (isOffline) {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.load_failed')}</p>
      </div>
    )
  }

  // Initial (or Retry-restarted) load failure: no cards have ever
  // successfully rendered for this generation, so there is nothing to
  // preserve — show the failure with a way back.
  if (infoStatus === 'failed' || configStatus === 'failed') {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.load_failed')}</p>
        <button
          type="button"
          data-testid="nex-retry"
          onClick={() => setGeneration((g) => g + 1)}
          className="mt-2 px-3 py-1.5 rounded-md bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-xs text-text-secondary cursor-pointer"
        >
          {t('hosts.nex.retry')}
        </button>
      </div>
    )
  }

  if (infoStatus !== 'loaded' || configStatus !== 'loaded') {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.loading')}</p>
      </div>
    )
  }

  // NexEngineStatus already refetches its own Nexen host/capabilities data
  // when its own Refresh button is clicked (it bumps an internal `tick`);
  // this handler only refreshes the daemon-side /api/info.nex data feeding
  // the badge/effective-config state, so the two refreshes don't duplicate
  // the same request. A failure here must NOT touch `infoStatus` — doing so would flip the gate above and tear down all three cards
  // over a transient Refresh blip. It only sets `refreshError`, shown as an
  // inline line above the still-rendered cards; a subsequent successful
  // refresh (or any full reload) clears it. Deliberately no unmount/stale-
  // hostId guard here.
  const handleRefresh = () => {
    fetchInfo(hostId)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`/api/info: ${r.status}`))))
      .then((data) => {
        setInfo(data.nex ?? null)
        setRefreshError(false)
      })
      .catch(() => {
        setRefreshError(true)
      })
  }

  // A save changes the daemon-computed `restart_required` (spec §4.4.2), so
  // refetch /api/info the same way Refresh does — without the loading gate,
  // so the cards (and the form's "Saved") stay on screen.
  const handleConfigSaved = (cfg: ConfigData) => {
    setConfig(cfg.nex ? normalizeNexConfig(cfg.nex) : undefined)
    handleRefresh()
  }

  return (
    <div className="max-w-2xl space-y-6">
      {refreshError && (
        <p data-testid="nex-refresh-error" className="text-xs text-red-400">{t('hosts.load_failed')}</p>
      )}
      <NexEngineStatus key={generation} hostId={hostId} info={info} onRefresh={handleRefresh} />
      <NexConfigForm hostId={hostId} config={config} info={info} onSaved={handleConfigSaved} />
      <NexExecutionsTable hostId={hostId} enabled={info?.ready ?? false} />
    </div>
  )
}

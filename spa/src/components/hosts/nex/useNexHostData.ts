// spa/src/components/hosts/nex/useNexHostData.ts — data layer of the Host →
// "Nex" sub-page (spec §4.4.3). `info` (→ /api/info.nex) is read from
// useNexHostStore — the one truth for "is Nexen ready here" (spec §4.1) —
// while this hook owns `/api/config` (→ config.nex) for one host, with the
// offline rule, reconnect reload, Retry, manual Refresh and the refetch
// after a save.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { hostFetch } from '../../../lib/host-api'
import type { ConfigData, NexConfig, NexInfo } from '../../../lib/host-api'
import { normalizeNexConfig } from './nex-config-diff'

// Per-request load status of the initial (or Retry/reconnect-restarted)
// `/api/config` load.
type LoadStatus = 'loading' | 'loaded' | 'failed'

// What the page should render:
// - offline: the host is not connected — show load_failed only, even when
//   data loaded earlier (a disconnected host takes no Save or execution
//   action, so stale cards would mislead);
// - failed: the config load failed, or the store's first `/api/info` fetch
//   for this host failed before any info was seen — show Retry. A store
//   `unavailable` with info present (capabilities 503) is NOT this: the
//   cards render and the engine card degrades on its own;
// - loading: the store has no info for the host yet, or config is pending.
//   `config` starts undefined, which is indistinguishable from "no [nex]
//   section", so the form must not render (a Save would PUT an empty
//   section over the real one), and a null `info` would read as "Disabled";
// - ready: store info present and config loaded.
export type NexHostPhase = 'offline' | 'failed' | 'loading' | 'ready'

export interface NexHostData {
  phase: NexHostPhase
  info: NexInfo | null
  config: NexConfig | undefined
  refreshError: boolean
  retry: () => void
  refresh: () => void
  onConfigSaved: (cfg: ConfigData) => void
}

function readJson(r: Response, path: string) {
  return r.ok ? r.json() : Promise.reject(new Error(`${path}: ${r.status}`))
}

export function useNexHostData(hostId: string): NexHostData {
  const status = useHostStore((s) => s.runtime[hostId]?.status)
  const entry = useNexHostStore((s) => s.byHost[hostId])
  const ensure = useNexHostStore((s) => s.ensure)
  const invalidate = useNexHostStore((s) => s.invalidate)

  const [config, setConfig] = useState<NexConfig | undefined>(undefined)
  const [configStatus, setConfigStatus] = useState<LoadStatus>('loading')
  // Bumped on a reconnect and by Retry: both restart the config load.
  const [generation, setGeneration] = useState(0)

  // Request token for `/api/config`: the load takes the next token and a
  // response is applied only while its token is still the latest. The load
  // effect's cleanup also advances it, so nothing lands after unmount or a
  // host change. Tokens are compared, never reset, so StrictMode's
  // mount→cleanup→mount cannot leave them stuck.
  const configToken = useRef(0)

  // Reconnect detector: a transition into 'connected' restarts the config
  // load (the store's own watcher refetches readiness). Render-time state
  // adjustment
  // (https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes)
  // rather than an effect, which `react-hooks/set-state-in-effect` flags.
  const [prevStatus, setPrevStatus] = useState(status)
  if (status !== prevStatus) {
    setPrevStatus(status)
    if (prevStatus !== 'connected' && status === 'connected') {
      setGeneration((g) => g + 1)
    }
  }

  // Reset to 'loading' the moment the host or generation changes — same
  // idiom, so the effect below only fetches and writes state from its
  // async callbacks.
  const fetchKey = `${hostId}:${generation}`
  const [prevFetchKey, setPrevFetchKey] = useState(fetchKey)
  if (fetchKey !== prevFetchKey) {
    setPrevFetchKey(fetchKey)
    setConfigStatus('loading')
  }

  // The last info the store produced for this fetchKey. A failed refetch
  // commits `info: null` to the store; the page keeps showing the info it
  // already had (with `refreshError`) rather than tearing down cards that
  // showed good data a moment ago — a transient blip is not a load failure.
  const storeInfo = entry?.info ?? null
  const [lastInfo, setLastInfo] = useState<{ key: string; info: NexInfo } | null>(null)
  if (storeInfo !== null && (lastInfo?.key !== fetchKey || lastInfo.info !== storeInfo)) {
    setLastInfo({ key: fetchKey, info: storeInfo })
  }
  const seenInfo = lastInfo?.key === fetchKey ? lastInfo.info : null

  useEffect(() => {
    const token = configToken
    const mine = ++token.current

    void ensure(hostId)

    hostFetch(hostId, '/api/config')
      .then((r) => readJson(r, '/api/config'))
      .then((data: ConfigData) => {
        if (token.current !== mine) return
        setConfig(data.nex ? normalizeNexConfig(data.nex) : undefined)
        setConfigStatus('loaded')
      })
      .catch(() => {
        if (token.current !== mine) return
        setConfigStatus('failed')
      })

    return () => {
      token.current += 1
    }
  }, [hostId, generation, ensure])

  // Refetches readiness only (`/api/info` + capabilities, via the store);
  // `/api/config` is left alone. A failure surfaces as `refreshError`.
  const refresh = useCallback(() => {
    void invalidate(hostId)
  }, [hostId, invalidate])

  // A save changes the daemon-computed `restart_required` (spec §4.4.2), so
  // refetch readiness the way Refresh does — without the loading gate, so
  // the cards (and the form's "Saved") stay on screen.
  const onConfigSaved = useCallback((cfg: ConfigData) => {
    setConfig(cfg.nex ? normalizeNexConfig(cfg.nex) : undefined)
    void invalidate(hostId)
  }, [hostId, invalidate])

  const retry = useCallback(() => {
    setGeneration((g) => g + 1)
    void invalidate(hostId)
  }, [hostId, invalidate])

  const info = storeInfo ?? seenInfo
  // The store settled a fetch that produced no info: a failed `/api/info`.
  const infoFetchFailed = entry !== undefined && entry.fetchedAt > 0 && entry.info === null

  let phase: NexHostPhase
  if (status !== 'connected') phase = 'offline'
  else if (configStatus === 'failed' || (info === null && infoFetchFailed)) phase = 'failed'
  else if (configStatus !== 'loaded' || info === null) phase = 'loading'
  else phase = 'ready'

  const refreshError = seenInfo !== null && storeInfo === null

  return { phase, info, config, refreshError, retry, refresh, onConfigSaved }
}

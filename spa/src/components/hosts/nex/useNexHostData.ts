// spa/src/components/hosts/nex/useNexHostData.ts — data layer of the Host →
// "Nex" sub-page (spec §4.4.3): `/api/info` (→ info.nex) and `/api/config`
// (→ config.nex) for one host, with the offline rule, reconnect reload,
// Retry, manual Refresh and the refetch after a save.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useHostStore } from '../../../stores/useHostStore'
import { hostFetch, fetchInfo } from '../../../lib/host-api'
import type { ConfigData, NexConfig, NexInfo } from '../../../lib/host-api'
import { normalizeNexConfig } from './nex-config-diff'

// Per-request load status of the initial (or Retry/reconnect-restarted)
// load. A failed manual Refresh is tracked separately (`refreshError`) and
// never flips this to 'failed', so a transient blip does not tear down
// cards that already show good data.
type LoadStatus = 'loading' | 'loaded' | 'failed'

// What the page should render:
// - offline: the host is not connected — show load_failed only, even when
//   data loaded earlier (a disconnected host takes no Save or execution
//   action, so stale cards would mislead);
// - failed: the initial load of either endpoint failed — show Retry;
// - loading: either endpoint is still pending. `config` starts undefined,
//   which is indistinguishable from "no [nex] section", so the form must
//   not render (a Save would PUT an empty section over the real one), and
//   a null `info` would read as "Disabled";
// - ready: both loaded.
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

  const [info, setInfo] = useState<NexInfo | null>(null)
  const [config, setConfig] = useState<NexConfig | undefined>(undefined)
  const [infoStatus, setInfoStatus] = useState<LoadStatus>('loading')
  const [configStatus, setConfigStatus] = useState<LoadStatus>('loading')
  const [refreshError, setRefreshError] = useState(false)
  // Bumped on a reconnect and by Retry: both restart the load.
  const [generation, setGeneration] = useState(0)

  // Request tokens, one per endpoint. Every request that writes `info`
  // (load, Refresh, refetch after a save) takes the next info token, and
  // the load takes the next config token; a response is applied only while
  // its token is still the latest. The load effect's cleanup also advances
  // both, so nothing lands after unmount or a host change, and a newer
  // load/refresh always supersedes an older one. Tokens are compared, never
  // reset, so StrictMode's mount→cleanup→mount cannot leave them stuck.
  const infoToken = useRef(0)
  const configToken = useRef(0)

  // Reconnect detector: a transition into 'connected' restarts the load.
  // Render-time state adjustment
  // (https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes)
  // rather than an effect, which `react-hooks/set-state-in-effect` flags.
  const [prevStatus, setPrevStatus] = useState(status)
  if (status !== prevStatus) {
    setPrevStatus(status)
    if (prevStatus !== 'connected' && status === 'connected') {
      setGeneration((g) => g + 1)
    }
  }

  // Reset to 'loading' (and clear a stale refresh error) the moment the
  // host or generation changes — same idiom, so the effect below only
  // fetches and writes state from its async callbacks.
  const fetchKey = `${hostId}:${generation}`
  const [prevFetchKey, setPrevFetchKey] = useState(fetchKey)
  if (fetchKey !== prevFetchKey) {
    setPrevFetchKey(fetchKey)
    setInfoStatus('loading')
    setConfigStatus('loading')
    setRefreshError(false)
  }

  useEffect(() => {
    const tokens = { info: infoToken, config: configToken }
    const myInfo = ++tokens.info.current
    const myConfig = ++tokens.config.current

    fetchInfo(hostId)
      .then((r) => readJson(r, '/api/info'))
      .then((data) => {
        if (tokens.info.current !== myInfo) return
        setInfo(data.nex ?? null)
        setInfoStatus('loaded')
      })
      .catch(() => {
        if (tokens.info.current !== myInfo) return
        setInfoStatus('failed')
      })

    hostFetch(hostId, '/api/config')
      .then((r) => readJson(r, '/api/config'))
      .then((data: ConfigData) => {
        if (tokens.config.current !== myConfig) return
        setConfig(data.nex ? normalizeNexConfig(data.nex) : undefined)
        setConfigStatus('loaded')
      })
      .catch(() => {
        if (tokens.config.current !== myConfig) return
        setConfigStatus('failed')
      })

    return () => {
      tokens.info.current += 1
      tokens.config.current += 1
    }
  }, [hostId, generation])

  // Refetches only `/api/info` (the status card refetches its own Nexen
  // host/capabilities). A failure sets `refreshError` and leaves the loaded
  // data and `infoStatus` alone.
  const refresh = useCallback(() => {
    const mine = ++infoToken.current
    fetchInfo(hostId)
      .then((r) => readJson(r, '/api/info'))
      .then((data) => {
        if (infoToken.current !== mine) return
        setInfo(data.nex ?? null)
        setRefreshError(false)
      })
      .catch(() => {
        if (infoToken.current !== mine) return
        setRefreshError(true)
      })
  }, [hostId])

  // A save changes the daemon-computed `restart_required` (spec §4.4.2), so
  // refetch `/api/info` the way Refresh does — without the loading gate, so
  // the cards (and the form's "Saved") stay on screen.
  const onConfigSaved = useCallback((cfg: ConfigData) => {
    setConfig(cfg.nex ? normalizeNexConfig(cfg.nex) : undefined)
    refresh()
  }, [refresh])

  const retry = useCallback(() => setGeneration((g) => g + 1), [])

  let phase: NexHostPhase
  if (status !== 'connected') phase = 'offline'
  else if (infoStatus === 'failed' || configStatus === 'failed') phase = 'failed'
  else if (infoStatus !== 'loaded' || configStatus !== 'loaded') phase = 'loading'
  else phase = 'ready'

  return { phase, info, config, refreshError, retry, refresh, onConfigSaved }
}

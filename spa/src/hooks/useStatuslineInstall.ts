import { useCallback, useEffect, useState } from 'react'
import { hostFetch } from '../lib/host-api'

export interface StatuslineState {
  mode: 'none' | 'pdx' | 'wrapped' | 'unmanaged'
  installed: boolean
  innerCommand?: string
  rawCommand?: string
  settingsPath: string
}

export type StatuslinePhase = 'idle' | 'loading' | 'ready' | 'error'

export function useStatuslineInstall(hostId: string) {
  const [state, setState] = useState<StatuslineState>({ mode: 'none', installed: false, settingsPath: '' })
  const [phase, setPhase] = useState<StatuslinePhase>('idle')
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setPhase('loading')
    setError(null)
    try {
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/status')
      if (!res.ok) throw new Error(`${res.status}`)
      setState(await res.json())
      setPhase('ready')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPhase('error')
    }
  }, [hostId])

  const install = useCallback(
    async (mode: 'pdx' | 'wrap', inner?: string) => {
      setPhase('loading')
      setError(null)
      const body = JSON.stringify({ action: 'install', mode, inner })
      try {
        const res = await hostFetch(hostId, '/api/agent/cc/statusline/setup', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body,
        })
        if (!res.ok) {
          setError(`${res.status}`)
          setPhase('error')
          return
        }
        setState(await res.json())
        setPhase('ready')
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
        setPhase('error')
      }
    },
    [hostId],
  )

  const remove = useCallback(async () => {
    setPhase('loading')
    setError(null)
    try {
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'remove' }),
      })
      if (!res.ok) {
        const msg = res.status === 409 ? 'Cannot remove unmanaged statusLine' : `${res.status}`
        setError(msg)
        setPhase('error')
        return
      }
      setState(await res.json())
      setPhase('ready')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPhase('error')
    }
  }, [hostId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return { state, phase, error, install, remove, refresh }
}

// spa/src/hooks/useConversations.ts — one host's ended or gone conversations (GET /api/nex/conversations).
// R-4-12: fetched on mount and on refetch() only; no live-list coupling (the daemon reuses a snapshot for 5 s).
import { useCallback, useEffect, useRef, useState } from 'react'
import { HandoffApiError } from '../lib/nex/handoff-api'
import { listConversations, type ConversationsPage, type ConversationState } from '../lib/nex/conversations-api'

export interface UseConversations {
  page: ConversationsPage | null
  phase: 'loading' | 'ready' | 'error'
  /** HandoffApiError code (message when it is not one). */
  error: string | null
  /** 404: Nexen is disabled on this host (R-4-11). */
  unavailable: boolean
  refetch: () => void
}

interface State {
  key: string
  page: ConversationsPage | null
  phase: UseConversations['phase']
  error: string | null
  unavailable: boolean
}

const fresh = (key: string): State => ({ key, page: null, phase: 'loading', error: null, unavailable: false })

export function useConversations(hostId: string, state: ConversationState): UseConversations {
  const key = `${hostId}\u0000${state}`
  const [st, setSt] = useState<State>(() => fresh(key))
  // token changes with the key; inFlight belongs to the current token, so there is one request per (host, state).
  const run = useRef({ token: 0, inFlight: false })

  const start = useCallback(() => {
    const r = run.current
    if (r.inFlight) return
    r.inFlight = true
    const mine = r.token
    listConversations(hostId, state)
      .then((page) => {
        if (run.current.token !== mine) return
        r.inFlight = false
        setSt({ key, page, phase: 'ready', error: null, unavailable: false })
      })
      .catch((err: unknown) => {
        if (run.current.token !== mine) return
        r.inFlight = false
        const code = err instanceof HandoffApiError ? err.code : err instanceof Error ? err.message : String(err)
        const unavailable = err instanceof HandoffApiError && err.status === 404
        setSt((cur) => ({ ...(cur.key === key ? cur : fresh(key)), phase: 'error', error: code, unavailable }))
      })
  }, [hostId, state, key])

  useEffect(() => {
    const r = run.current
    r.token += 1
    r.inFlight = false
    start()
    return () => { r.token += 1 }
  }, [start])

  const refetch = useCallback(() => {
    if (run.current.inFlight) return
    setSt((cur) => (cur.key === key ? { ...cur, phase: 'loading', error: null, unavailable: false } : cur))
    start()
  }, [key, start])

  // A different host's or state's rows never show here, even for the render before its effect runs.
  const cur = st.key === key ? st : fresh(key)
  return { page: cur.page, phase: cur.phase, error: cur.error, unavailable: cur.unavailable, refetch }
}

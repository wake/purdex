// spa/src/hooks/useConversations.ts — one host's ended or gone conversations (GET /api/nex/conversations).
// `scope` (test | normal) is part of the identity. R-4-12: fetched on mount and on refetch() only; no live-list coupling (the daemon reuses a snapshot for 5 s).
import { useCallback, useEffect, useRef, useState } from 'react'
import { HandoffApiError } from '../lib/nex/handoff-api'
import { listConversations, type ConversationScope, type ConversationsPage, type ConversationState } from '../lib/nex/conversations-api'

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

// One pending request per (host, state, scope), shared by every hook instance, StrictMode's double mount and refetch;
// removed when it settles, so a later refetch starts a new one.
const pending = new Map<string, Promise<ConversationsPage>>()

function sharedList(key: string, hostId: string, state: ConversationState, scope?: ConversationScope): Promise<ConversationsPage> {
  const cur = pending.get(key)
  if (cur) return cur
  const p = listConversations(hostId, state, scope)
  pending.set(key, p)
  const clear = () => { if (pending.get(key) === p) pending.delete(key) }
  p.then(clear, clear)
  return p
}

export function useConversations(hostId: string, state: ConversationState, scope?: ConversationScope): UseConversations {
  const key = `${hostId}\u0000${state}\u0000${scope ?? ''}`
  const [st, setSt] = useState<State>(() => fresh(key))
  // token changes with the key and on unmount; a response for an older token is dropped.
  const run = useRef({ token: 0 })

  const start = useCallback(() => {
    const r = run.current
    const mine = r.token
    sharedList(key, hostId, state, scope)
      .then((page) => {
        if (run.current.token !== mine) return
        setSt({ key, page, phase: 'ready', error: null, unavailable: false })
      })
      .catch((err: unknown) => {
        if (run.current.token !== mine) return
        const code = err instanceof HandoffApiError ? err.code : err instanceof Error ? err.message : String(err)
        const unavailable = err instanceof HandoffApiError && err.status === 404
        setSt((cur) => ({ ...(cur.key === key ? cur : fresh(key)), phase: 'error', error: code, unavailable }))
      })
  }, [hostId, state, scope, key])

  useEffect(() => {
    const r = run.current
    r.token += 1
    start()
    return () => { r.token += 1 }
  }, [start])

  const refetch = useCallback(() => {
    setSt((cur) => (cur.key === key ? { ...cur, phase: 'loading', error: null, unavailable: false } : cur))
    start()
  }, [key, start])

  // A different host's or state's rows never show here, even for the render before its effect runs.
  const cur = st.key === key ? st : fresh(key)
  return { page: cur.page, phase: cur.phase, error: cur.error, unavailable: cur.unavailable, refetch }
}

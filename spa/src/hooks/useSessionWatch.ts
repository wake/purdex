import { useEffect } from 'react'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'

let refCount = 0
let intervalId: ReturnType<typeof setInterval> | null = null

function start() {
  if (intervalId !== null) return
  // Fetch immediately on first mount
  fetchAll()
  intervalId = setInterval(fetchAll, 1000)
}

function stop() {
  if (intervalId !== null) {
    clearInterval(intervalId)
    intervalId = null
  }
}

function fetchAll() {
  const { hostOrder } = useHostStore.getState()
  const runtime = useHostStore.getState().runtime
  const { fetchHost } = useSessionStore.getState()
  for (const hostId of hostOrder) {
    if (runtime[hostId]?.status === 'connected') {
      fetchHost(hostId).catch(() => {})
    }
  }
}

export function useSessionWatch(): void {
  useEffect(() => {
    refCount++
    if (refCount === 1) start()
    return () => {
      refCount--
      if (refCount === 0) stop()
    }
  }, [])
}

// For testing: reset internal state
export function __resetSessionWatch(): void {
  stop()
  refCount = 0
}

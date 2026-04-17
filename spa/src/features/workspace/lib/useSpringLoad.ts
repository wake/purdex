import { useCallback, useEffect, useRef } from 'react'

export interface SpringLoadHook {
  schedule: (key: string, onExpire: () => void) => void
  cancel: (key?: string) => void
}

export function useSpringLoad(delayMs: number): SpringLoadHook {
  const timerRef = useRef<{ key: string; id: ReturnType<typeof setTimeout> } | null>(null)

  const cancel = useCallback((key?: string) => {
    if (!timerRef.current) return
    if (key !== undefined && timerRef.current.key !== key) return
    clearTimeout(timerRef.current.id)
    timerRef.current = null
  }, [])

  const schedule = useCallback(
    (key: string, onExpire: () => void) => {
      if (timerRef.current) {
        clearTimeout(timerRef.current.id)
        timerRef.current = null
      }
      const id = setTimeout(() => {
        timerRef.current = null
        onExpire()
      }, delayMs)
      timerRef.current = { key, id }
    },
    [delayMs],
  )

  useEffect(() => {
    return () => {
      if (timerRef.current) {
        clearTimeout(timerRef.current.id)
        timerRef.current = null
      }
    }
  }, [])

  return { schedule, cancel }
}

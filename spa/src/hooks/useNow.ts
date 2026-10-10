// spa/src/hooks/useNow.ts — `Date.now()` as state, ticking once a second while `on` (an elapsed-time label); no timer while off.
import { useEffect, useState } from 'react'

export function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!on) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [on])
  return now
}

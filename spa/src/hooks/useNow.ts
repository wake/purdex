// spa/src/hooks/useNow.ts — `Date.now()` as state, ticking once a second while `on` (an elapsed-time label); no timer while off.
import { useEffect, useState } from 'react'

export function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!on) return
    // a turn-on after a long idle starts from the present, not from when this component mounted (a timeout, not a synchronous setState)
    const first = setTimeout(() => setNow(Date.now()), 0)
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => { clearTimeout(first); clearInterval(id) }
  }, [on])
  return now
}

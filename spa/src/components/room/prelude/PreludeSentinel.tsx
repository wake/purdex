// spa/src/components/room/prelude/PreludeSentinel.tsx — the prelude's top
// edge (spec §5.4). Seen → ask for the next older page. `generation` re-arms
// the observer after each page: an IntersectionObserver reports only
// changes, and a page too short to push the sentinel out of view must still
// ask again (the viewport keeps filling until done).
import { useEffect, useRef } from 'react'

export default function PreludeSentinel({ onVisible, generation }: { onVisible: () => void; generation: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const latest = useRef(onVisible)
  useEffect(() => { latest.current = onVisible }, [onVisible])
  useEffect(() => {
    const el = ref.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) latest.current()
    })
    io.observe(el)
    return () => io.disconnect()
  }, [generation])
  return <div ref={ref} data-testid="prelude-sentinel" aria-hidden className="h-px" />
}

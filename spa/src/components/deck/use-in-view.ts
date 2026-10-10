// spa/src/components/deck/use-in-view.ts — true once the element has come within reach of the viewport (and stays true). Without
// IntersectionObserver (jsdom, very old webviews) it is true at once: nothing is deferred, which is the old behaviour.
import { useEffect, useRef, useState } from 'react'

const NEAR_PX = 300

export function useInView<T extends Element>(): [React.RefObject<T | null>, boolean] {
  const ref = useRef<T | null>(null)
  const [seen, setSeen] = useState(() => typeof IntersectionObserver === 'undefined')
  useEffect(() => {
    const el = ref.current
    if (seen || !el || typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver((records) => {
      if (records.some((r) => r.isIntersecting)) { setSeen(true); io.disconnect() }
    }, { rootMargin: `${NEAR_PX}px` })
    io.observe(el)
    return () => io.disconnect()
  }, [seen])
  return [ref, seen]
}

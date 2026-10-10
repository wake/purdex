// spa/src/components/deck/StatusOverlay.tsx — the one-line notes at the top of a transcript (「載入較早的內容…」, 「已到對話開頭」) as
// an overlay: a zero-height sticky row that is always in the flow (so its siblings never move), whose note floats over the
// first lines. A note that took room in the flow would push the reader's turns down by its height each time it came or went.
import { Children, type ReactNode } from 'react'

export function StatusOverlay({ children }: { children?: ReactNode }) {
  return (
    <div className="pointer-events-none sticky top-0 z-10 h-0 overflow-visible">
      {Children.toArray(children).length > 0 && <div className="inline-block rounded bg-surface-primary/80 px-1 text-xs text-text-muted">{children}</div>}
    </div>
  )
}

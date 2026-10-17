// spa/src/components/journal/RefChips.tsx — an entry's refs: a link to the project's issue / PR where the remote is known, else plain text.
import { openExternal, refUrl } from '../../lib/journal/refs'

const CHIP = 'px-1 rounded bg-surface-hover text-[10px] font-mono'

export function RefChips({ refs, remoteUrl }: { refs: readonly string[]; remoteUrl: string }) {
  if (refs.length === 0) return null
  return (
    <div className="mt-1 flex flex-wrap gap-1">
      {refs.map((r) => {
        const url = refUrl(r, remoteUrl)
        return url === null ? (
          <span key={r} className={`${CHIP} text-text-secondary`}>{r}</span>
        ) : (
          <a
            key={r}
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            className={`${CHIP} text-accent underline decoration-dotted underline-offset-2 cursor-pointer`}
            onClick={(e) => { e.preventDefault(); e.stopPropagation(); openExternal(url) }}
          >
            {r}
          </a>
        )
      })}
    </div>
  )
}

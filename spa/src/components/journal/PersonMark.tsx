// spa/src/components/journal/PersonMark.tsx — a text with the person's name highlighted wherever it appears (spec §2 item 23, §11).
import { splitByName } from '../../lib/journal/person-name'

export function PersonMark({ text, name }: { text: string; name: string | undefined }) {
  return (
    <>
      {splitByName(text, name).map((p, i) => p.hit
        ? <span key={i} data-testid="journal-person" className="px-1 rounded font-semibold bg-status-warning/20 text-journal-warning-text">{p.text}</span>
        : p.text)}
    </>
  )
}

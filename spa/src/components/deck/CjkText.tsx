// spa/src/components/deck/CjkText.tsx — plain text with its Chinese runs wrapped for 1px letter-spacing (lib/conversations/cjk.ts).
// The wrapping adds no text, so the string a reader selects or searches is the same one.
import { splitCjk } from '../../lib/conversations/cjk'

export function CjkText({ text }: { text: string }) {
  return <>{splitCjk(text).map((p, i) => (p.cjk ? <span key={i} className="cjk">{p.text}</span> : p.text))}</>
}

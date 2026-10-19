// spa/src/components/Avatar.tsx — a persona's face (name pool v2 spec §9.2). The stored raster when there is one,
// otherwise the name's first letter: on the persona's own colour, or (no persona) on a neutral disc.
import { useState } from 'react'
import { personaColor } from '../lib/names/persona-color'

/** 16–44 are the places an avatar sits beside text (22 the lead name card's pills); 32 is the persona list row and 96 the persona form's preview. */
/** The disc behind a picture: the persona drawings are transparent, so without it the circle (and the face on a dark theme) is lost. The NA-0 design's `.av`. */
export const AVATAR_BACKING = '#dfe4ea'

export type AvatarSize = 14 | 16 | 18 | 22 | 24 | 32 | 44 | 96

interface Props {
  size: AvatarSize
  code: string
  name?: string
  /** `Seat.avatar`: a `data:image/…` URL, or `""`/absent for initials. */
  src?: string
  className?: string
  /** The name sits right beside the face: it adds nothing for a screen reader (no role, no label) rather than saying the name twice. */
  decorative?: boolean
}

export function Avatar({ size, code, name, src, className = '', decorative = false }: Props) {
  const label = name || code
  const [failed, setFailed] = useState<string | null>(null)
  const box = { width: size, height: size }
  if (src && failed !== src) {
    return (
      <img
        {...(decorative ? { alt: '', 'aria-hidden': true } : { role: 'img', alt: label })}
        src={src}
        draggable={false}
        onError={() => setFailed(src)}
        style={{ ...box, backgroundColor: AVATAR_BACKING }}
        className={`shrink-0 rounded-full object-cover ring-1 ring-border-subtle ${className}`}
      />
    )
  }
  // A persona's initial sits on its own colour, the same wherever it is drawn. No persona (empty code) is a neutral disc on the theme's surface:
  // a host colour here would read as a status (green is "running").
  const persona = code !== ''
  return (
    <span
      {...(decorative ? { 'aria-hidden': true } : { role: 'img', 'aria-label': label })}
      style={{ ...box, ...(persona ? { backgroundColor: personaColor(code) } : {}), fontSize: Math.round(size * 0.5) }}
      className={`inline-flex shrink-0 items-center justify-center rounded-full font-medium leading-none ring-1 ring-border-subtle ${persona ? 'text-white' : 'bg-surface-secondary text-text-secondary'} ${className}`}
    >
      {Array.from(label)[0]?.toUpperCase() ?? ''}
    </span>
  )
}

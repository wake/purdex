// spa/src/components/Avatar.tsx — a persona's face (name pool v2 spec §9.2). The stored raster when there is one,
// otherwise the name's first letter on the host colour.
import { useState } from 'react'

export type AvatarSize = 16 | 18 | 22 | 24 | 44

interface Props {
  size: AvatarSize
  code: string
  name?: string
  /** `Seat.avatar`: a `data:image/…` URL, or `""`/absent for initials. */
  src?: string
  /** The host colour the initials sit on. */
  fallbackColor: string
  className?: string
}

export function Avatar({ size, code, name, src, fallbackColor, className = '' }: Props) {
  const label = name || code
  const [failed, setFailed] = useState<string | null>(null)
  const box = { width: size, height: size }
  if (src && failed !== src) {
    return (
      <img
        role="img"
        alt={label}
        src={src}
        draggable={false}
        onError={() => setFailed(src)}
        style={box}
        className={`shrink-0 rounded-full object-cover ring-1 ring-border-subtle ${className}`}
      />
    )
  }
  return (
    <span
      role="img"
      aria-label={label}
      style={{ ...box, backgroundColor: fallbackColor, fontSize: Math.round(size * 0.5) }}
      className={`inline-flex shrink-0 items-center justify-center rounded-full font-medium leading-none text-white ring-1 ring-border-subtle ${className}`}
    >
      {Array.from(label)[0]?.toUpperCase() ?? ''}
    </span>
  )
}

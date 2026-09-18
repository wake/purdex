/** Pure color conversions for the host badge. `#rrggbb` in, numbers out. */

const HEX6_RE = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i

export function hexToRgb(hex: string): { r: number; g: number; b: number } | null {
  const m = HEX6_RE.exec(hex)
  if (!m) return null
  return { r: parseInt(m[1], 16), g: parseInt(m[2], 16), b: parseInt(m[3], 16) }
}

/** `rgba(r, g, b, a)` with `a` = alphaPct / 100 (0–1). Null for an invalid hex. */
export function rgbaString(hex: string, alphaPct: number): string | null {
  const rgb = hexToRgb(hex)
  if (!rgb) return null
  const a = Math.min(1, Math.max(0, alphaPct / 100))
  return `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${a})`
}

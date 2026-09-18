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

const clamp01 = (n: number) => Math.min(1, Math.max(0, n))

export interface Hsv {
  /** 0–360 */
  h: number
  /** 0–100 */
  s: number
  /** 0–100 */
  v: number
}

export function hexToHsv(hex: string): Hsv | null {
  const rgb = hexToRgb(hex)
  if (!rgb) return null
  const r = rgb.r / 255, g = rgb.g / 255, b = rgb.b / 255
  const max = Math.max(r, g, b), min = Math.min(r, g, b)
  const d = max - min
  const v = max
  const s = max === 0 ? 0 : d / max
  let h = 0
  if (d !== 0) {
    if (max === r) h = ((g - b) / d) % 6
    else if (max === g) h = (b - r) / d + 2
    else h = (r - g) / d + 4
    h *= 60
    if (h < 0) h += 360
  }
  return { h, s: s * 100, v: v * 100 }
}

export function hsvToHex({ h, s, v }: Hsv): string {
  const hh = ((h % 360) + 360) % 360
  const ss = clamp01(s / 100), vv = clamp01(v / 100)
  const c = vv * ss
  const x = c * (1 - Math.abs(((hh / 60) % 2) - 1))
  const m = vv - c
  let r = 0, g = 0, b = 0
  if (hh < 60) [r, g, b] = [c, x, 0]
  else if (hh < 120) [r, g, b] = [x, c, 0]
  else if (hh < 180) [r, g, b] = [0, c, x]
  else if (hh < 240) [r, g, b] = [0, x, c]
  else if (hh < 300) [r, g, b] = [x, 0, c]
  else [r, g, b] = [c, 0, x]
  const toHex = (n: number) => Math.round((n + m) * 255).toString(16).padStart(2, '0')
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`
}

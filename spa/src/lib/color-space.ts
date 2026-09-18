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

export interface Hsl {
  /** 0–360 */
  h: number
  /** 0–100 */
  s: number
  /** 0–100 */
  l: number
}

const clamp01 = (n: number) => Math.min(1, Math.max(0, n))

/** Floats, not rounded: keeping precision makes hex → hsl → hex exact for every preset. */
export function hexToHsl(hex: string): Hsl | null {
  const rgb = hexToRgb(hex)
  if (!rgb) return null
  const r = rgb.r / 255
  const g = rgb.g / 255
  const b = rgb.b / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const l = (max + min) / 2
  if (max === min) return { h: 0, s: 0, l: l * 100 }
  const d = max - min
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
  let h: number
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0)
  else if (max === g) h = (b - r) / d + 2
  else h = (r - g) / d + 4
  return { h: h * 60, s: s * 100, l: l * 100 }
}

function hueToRgb(p: number, q: number, t: number): number {
  if (t < 0) t += 1
  if (t > 1) t -= 1
  if (t < 1 / 6) return p + (q - p) * 6 * t
  if (t < 1 / 2) return q
  if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6
  return p
}

/** Lowercase `#rrggbb`. `h` wraps modulo 360; `s` / `l` clamp to 0–100. */
export function hslToHex({ h, s, l }: Hsl): string {
  const hh = (((h % 360) + 360) % 360) / 360
  const ss = clamp01(s / 100)
  const ll = clamp01(l / 100)
  let r: number, g: number, b: number
  if (ss === 0) {
    r = g = b = ll
  } else {
    const q = ll < 0.5 ? ll * (1 + ss) : ll + ss - ll * ss
    const p = 2 * ll - q
    r = hueToRgb(p, q, hh + 1 / 3)
    g = hueToRgb(p, q, hh)
    b = hueToRgb(p, q, hh - 1 / 3)
  }
  const toHex = (v: number) => Math.round(v * 255).toString(16).padStart(2, '0')
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`
}

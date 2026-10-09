// spa/src/components/QrCode.tsx — an inline SVG QR code (QP-3). Built from the module matrix as React elements:
// nothing is injected as HTML and nothing is fetched. The colours are fixed (black on white) on purpose — a scanner
// needs contrast whatever the app theme is.

import qrcode from 'qrcode-generator'

const MARGIN = 4 // quiet zone, in modules (the QR spec's minimum)
const LIGHT = '#ffffff'
const DARK = '#000000'

interface Props {
  value: string
  /** Rendered edge in CSS pixels. */
  size?: number
  label?: string
}

/** One path: a horizontal run of dark modules per `M x y h n v1 h-n z`. Null when `value` cannot be encoded. */
function pathOf(value: string): { d: string; count: number } | null {
  try {
    const qr = qrcode(0, 'M')
    qr.addData(value)
    qr.make()
    const count = qr.getModuleCount()
    let d = ''
    for (let y = 0; y < count; y++) {
      let x = 0
      while (x < count) {
        if (!qr.isDark(y, x)) {
          x++
          continue
        }
        let n = 1
        while (x + n < count && qr.isDark(y, x + n)) n++
        d += `M${x + MARGIN} ${y + MARGIN}h${n}v1h-${n}z`
        x += n
      }
    }
    return { d, count }
  } catch {
    return null
  }
}

export function QrCode({ value, size = 224, label }: Props) {
  const qr = pathOf(value)
  if (!qr) {
    return (
      <div data-testid="qr-error" role="alert" className="text-xs text-red-400">
        {label ?? 'QR'}
      </div>
    )
  }
  const span = qr.count + MARGIN * 2
  return (
    <svg
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`0 0 ${span} ${span}`}
      shapeRendering="crispEdges"
      style={{ background: LIGHT }}
    >
      <rect width={span} height={span} fill={LIGHT} />
      <path d={qr.d} fill={DARK} />
    </svg>
  )
}

// spa/src/components/team/ModelIcon.tsx — model shape (◆ Opus ● Sonnet ▲ Haiku ★ Fable) and the context ring.
//
// Monochrome on purpose: color belongs to the lights. (Ported from the prototype at f61748aa.)

import { ringGeometry, ringTransform, type UsageTone } from '../../lib/usage-display'
import { MODEL_LABEL, type ModelFamily } from './model-family'

// Optical centring (r4b): the ring draws each shape by the point the eye reads as its middle, placed on the ring's centre
// (6,6 of the 12x12 box). Bjango, "Formulas for optical adjustments": a triangle in a circle looks right when its CENTROID
// (not its bbox centre) is on the circle's centre; a star likewise by its circumcentre. A bbox-centred triangle looks too
// high, because most of its mass is in the lower part.
//  - opus (diamond) and sonnet (circle) are symmetric: bbox centre = centre.
//  - haiku: the mean of its 3 vertices = (6, (-.4+9.2+9.2)/3) = (6, 6). The apex pokes .4 above the box, so the standalone
//    ModelIcon's svg is overflow-visible.
//  - fable: the mean of its 5 outer vertices (= a regular star's circumcentre) = (6, (.08+4.18+10.78+10.78+4.18)/5) = (6, 6).
const PATHS: Record<ModelFamily, string> = {
  opus: 'M6 .6 11.4 6 6 11.4.6 6Z',
  sonnet: 'M6 1.2a4.8 4.8 0 1 1 0 9.6a4.8 4.8 0 1 1 0-9.6Z',
  haiku: 'M6 -.4 11.2 9.2 .8 9.2Z',
  fable: 'M6 .08 7.6 3.88 11.6 4.18 8.6 6.78 9.5 10.78 6 8.68 2.5 10.78 3.4 6.78 .4 4.18 4.4 3.88Z',
}

export function ModelIcon({ model, size = 11 }: { model: ModelFamily | undefined; size?: number }) {
  if (!model) {
    return (
      <span
        data-testid="model-icon-unknown"
        className="inline-grid place-items-center rounded-full border border-dashed border-current text-[8px] leading-none flex-shrink-0"
        style={{ width: size, height: size }}
      >
        ?
      </span>
    )
  }
  return (
    <svg data-testid={`model-icon-${model}`} width={size} height={size} viewBox="0 0 12 12" fill="currentColor" overflow="visible" className="flex-shrink-0" aria-label={MODEL_LABEL[model]}>
      <path d={PATHS[model]} />
    </svg>
  )
}

const TONE_CLASS: Record<UsageTone, string> = { ok: 'stroke-status-success', warn: 'stroke-status-warning', danger: 'stroke-status-error' }

/**
 * Context usage as a USED ring (grows counterclockwise from 12 o'clock, coloured by used %); the model shape sits in the
 * middle. `symbolColor` (a CSS colour) paints the shape — the one-line cell passes the seat host's main colour; absent, it
 * inherits the text colour.
 */
export function ContextRing({ pct, model, size = 22, symbolColor }: { pct: number | undefined; model: ModelFamily | undefined; size?: number; symbolColor?: string }) {
  const r = size / 2 - 2
  const c = 2 * Math.PI * r
  const geo = pct === undefined ? null : ringGeometry(pct, 'used')
  // The shape lives in the ring's own coordinate system: its 12x12 box (g wide, NOT rounded to an integer) is centred on
  // (size/2, size/2) by arithmetic, so no DOM-level pixel snapping can push it off the ring's middle.
  const g = size * 0.42
  const o = (size - g) / 2
  const half = size / 2
  return (
    <span className="relative inline-grid flex-shrink-0" style={{ width: size, height: size, color: symbolColor }} data-testid="context-ring">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="absolute inset-0 block">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border-default)" strokeWidth={2.5} />
        {geo && (
          <circle
            data-testid="context-ring-arc"
            data-shown={geo.sharePct}
            data-tone={geo.tone}
            data-direction={geo.direction}
            className={TONE_CLASS[geo.tone]}
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            strokeWidth={2.5}
            strokeLinecap="round"
            strokeDasharray={`${(geo.sharePct / 100 * c).toFixed(2)} ${c.toFixed(2)}`}
            transform={ringTransform(size, geo.direction)}
          />
        )}
        {model ? (
          <g data-testid={`model-icon-${model}`} fill="currentColor" role="img" aria-label={MODEL_LABEL[model]}>
            <path d={PATHS[model]} transform={`translate(${o.toFixed(4)}, ${o.toFixed(4)}) scale(${(g / 12).toFixed(5)})`} />
          </g>
        ) : (
          <g data-testid="model-icon-unknown" fill="currentColor" stroke="currentColor">
            <circle cx={half} cy={half} r={g / 2} fill="none" strokeWidth={0.8} strokeDasharray="1.2 1" />
            <text x={half} y={half} textAnchor="middle" dominantBaseline="central" stroke="none" fontSize={g * 1.1}>?</text>
          </g>
        )}
      </svg>
    </span>
  )
}

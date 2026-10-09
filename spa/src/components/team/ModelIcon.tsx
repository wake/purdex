// spa/src/components/team/ModelIcon.tsx — model shape (◆ Opus ● Sonnet ▲ Haiku ★ Fable) and the context ring.
//
// Monochrome on purpose: color belongs to the lights. (Ported from the prototype at f61748aa.)

import { ringGeometry, ringTransform, type UsageTone } from '../../lib/usage-display'
import { MODEL_LABEL, type ModelFamily } from './model-family'

const PATHS: Record<ModelFamily, string> = {
  opus: 'M6 .6 11.4 6 6 11.4.6 6Z',
  sonnet: 'M6 1.2a4.8 4.8 0 1 1 0 9.6a4.8 4.8 0 1 1 0-9.6Z',
  haiku: 'M6 1 11.2 10.6H.8Z',
  fable: 'M6 .5 7.6 4.3 11.6 4.6 8.6 7.2 9.5 11.2 6 9.1 2.5 11.2 3.4 7.2.4 4.6 4.4 4.3Z',
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
    <svg data-testid={`model-icon-${model}`} width={size} height={size} viewBox="0 0 12 12" fill="currentColor" className="flex-shrink-0" aria-label={MODEL_LABEL[model]}>
      <path d={PATHS[model]} />
    </svg>
  )
}

const TONE_CLASS: Record<UsageTone, string> = { ok: 'stroke-status-success', warn: 'stroke-status-warning', danger: 'stroke-status-error' }

/** Context usage as a USED ring (grows counterclockwise from 12 o'clock, coloured by used %); the model shape sits in the middle. */
export function ContextRing({ pct, model, size = 22 }: { pct: number | undefined; model: ModelFamily | undefined; size?: number }) {
  const r = size / 2 - 2
  const c = 2 * Math.PI * r
  const geo = pct === undefined ? null : ringGeometry(pct, 'used')
  return (
    <span className="relative inline-grid place-items-center flex-shrink-0" style={{ width: size, height: size }} data-testid="context-ring">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="absolute inset-0">
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
      </svg>
      <ModelIcon model={model} size={Math.round(size * 0.42)} />
    </span>
  )
}

// spa/src/components/team/model-family.ts — the model families the team panel shows.

export type ModelFamily = 'opus' | 'sonnet' | 'haiku' | 'fable'

export const MODEL_LABEL: Record<ModelFamily, string> = {
  opus: 'Opus',
  sonnet: 'Sonnet',
  haiku: 'Haiku',
  fable: 'Fable',
}

/** The family a roster model string belongs to (`claude-opus-5-5`, `Sonnet 5.5`, ...); undefined when it names none. */
export function familyOf(model: string | undefined): ModelFamily | undefined {
  if (!model) return undefined
  const m = model.toLowerCase()
  for (const f of Object.keys(MODEL_LABEL) as ModelFamily[]) if (m.includes(f)) return f
  return undefined
}

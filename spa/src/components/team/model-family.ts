// spa/src/components/team/model-family.ts — the model families the team panel shows.

export type ModelFamily = 'opus' | 'sonnet' | 'haiku' | 'fable'

export const MODEL_LABEL: Record<ModelFamily, string> = {
  opus: 'Opus',
  sonnet: 'Sonnet',
  haiku: 'Haiku',
  fable: 'Fable',
}

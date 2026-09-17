// spa/src/lib/nex/content-blocks.ts — shape guards for provider payloads
// shared by the partial assembly and the tool-activity rules.

export function obj(v: unknown): Record<string, unknown> | null {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

export function contentBlocks(p: Record<string, unknown>): Record<string, unknown>[] {
  const content = obj(p.message)?.content
  return Array.isArray(content) ? content.map(obj).filter((b): b is Record<string, unknown> => b !== null) : []
}

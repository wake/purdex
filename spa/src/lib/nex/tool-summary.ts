// spa/src/lib/nex/tool-summary.ts
// Header summary for a tool call (spec §4.4 R1).
//
// Decision order: the N2 `primary_arg` from the daemon wins; a tool the
// daemon does not know (`known: false`) falls back to the first three input
// keys (R10); everything else — no entry, raw-only entry, or a known tool
// with no primary key (F9, `primaryArg: null`) — uses the pre-N2 client
// table `getSummary`. Truncation is the renderer's job (contract rule 11).
import type { ToolActivity } from './tool-activity'

export function getSummary(tool: string, input: Record<string, unknown>): string {
  switch (tool) {
    case 'Bash':
      return (input.command as string) ?? ''
    case 'Read':
    case 'Write':
    case 'Edit':
      return (input.file_path as string) ?? ''
    case 'WebFetch':
      return (input.url as string) ?? ''
    case 'Grep':
    case 'Glob':
      return (input.pattern as string) ?? ''
    case 'Agent':
      return (input.description as string) ?? ''
    default:
      return JSON.stringify(input).slice(0, 80)
  }
}

const R10_KEYS = 3

function r10Value(v: unknown): string {
  switch (typeof v) {
    case 'string':
    case 'number':
    case 'boolean':
      return String(v)
    default:
      return JSON.stringify(v)
  }
}

/** R10: `key: value` for the first three own keys of `input`, `''` when empty. */
function unknownToolSummary(input: Record<string, unknown>): string {
  return Object.keys(input)
    .slice(0, R10_KEYS)
    .map((k) => `${k}: ${r10Value(input[k])}`)
    .join(', ')
}

export function toolSummary(
  tool: string,
  input: Record<string, unknown>,
  entry?: Pick<ToolActivity, 'primaryArg' | 'known'>,
): string {
  const primary = entry?.primaryArg
  if (primary !== null && primary !== undefined) return primary.value
  if (entry?.known === false) return unknownToolSummary(input)
  return getSummary(tool, input)
}

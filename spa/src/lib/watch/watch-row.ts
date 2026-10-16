// spa/src/lib/watch/watch-row.ts — the holder's `watch` host-config row (plan T-a3.2): `GET /api/hostconfig`, field `watch`.
// Read leniently. Every field is kept in `row` so W5c1's 守門員 section can write the whole row back without losing one this
// App does not know yet. The daemon's GET does not re-validate a stored row, so nothing here is trusted beyond its type.
import { fetchHostConfig } from '../host-config-api'

export interface WatchRow {
  revision: number
  /** The row as the daemon sent it, unknown fields included. */
  row: Record<string, unknown>
  agentCommand: string
  workdir: string
}

/** The command a row written with the old `model` field stood for (internal/watchsettings `LegacyModelCommand`). */
const legacyCommand = (model: string): string => `claude --dangerously-skip-permissions --model ${model}`

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

export function parseWatchRow(field: unknown): WatchRow | null {
  if (!isObj(field) || !isObj(field.items) || typeof field.revision !== 'number') return null
  const row = field.items
  const command = typeof row.agent_command === 'string' && row.agent_command !== ''
    ? row.agent_command
    : typeof row.model === 'string' && row.model !== '' ? legacyCommand(row.model) : ''
  return { revision: field.revision, row, agentCommand: command, workdir: typeof row.workdir === 'string' ? row.workdir : '' }
}

/** The `--model <x>` or `--model=<x>` of an agent command, else nothing. */
export function modelOf(agentCommand: string): string | undefined {
  const m = /(?:^|\s)--model(?:=|\s+)(\S+)/.exec(agentCommand)
  return m ? m[1] : undefined
}

/** null on any failure: the panel then simply shows no command. */
export async function readWatchRow(hostId: string): Promise<WatchRow | null> {
  try {
    const body = await fetchHostConfig(hostId)
    return isObj(body) ? parseWatchRow(body.watch) : null
  } catch {
    return null
  }
}

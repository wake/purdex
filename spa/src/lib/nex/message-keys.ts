// spa/src/lib/nex/message-keys.ts — how a transcript names its messages in
// keys (worker prelude spec §5.3). The live list names them by position —
// it only grows at the end, so a position never changes. The prelude grows
// at the front, so it names them by a stable id (`p<pos>`). Every key a
// block owns — React row, fold, search anchor, operation pairing — goes
// through here.
import { blockKey, type BlockKey } from './operations'

export type MessageIdOf = (m: number) => string

export function keyAt(ctx: { idOf?: MessageIdOf }, i: number, j: number): BlockKey {
  return blockKey(ctx.idOf ? ctx.idOf(i) : i, j)
}

export function rowKey(ctx: { idOf?: MessageIdOf; keyPrefix: string }, i: number): string {
  return `${ctx.keyPrefix}-${ctx.idOf ? ctx.idOf(i) : i}`
}

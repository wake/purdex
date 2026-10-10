// spa/src/components/team/seat-workbook.ts — what the team panel reads of a seat's workbook (WA-2b-1). READ ONLY: the
// store is filled by the WA-1 loader (workbook-loader.ts: first appearance + reconnect) and by events, so nothing here
// fetches. A seat's `hostId` is the App host its session lives on (X5-App-a resolved it; '' = this Mac lacks the host),
// so a remote seat reads that host's store, and a host this Mac lacks has no workbook.
import { useWorkbookStore, selectConv, type ConvState } from '../../stores/useWorkbookStore'

/** The first sentence of a status: up to the first full stop (。 ！ ？ or . ! ? before a space / the end) or line break. */
export function firstSentence(status: string): string {
  const text = status.trim()
  const cut = text.search(/[。！？\n]|[.!?](?=\s|$)/)
  return (cut < 0 ? text : text.slice(0, cut)).trim()
}

export interface SeatWorkbook {
  /** The host lists `workbook.v1` and the daemon has (or has told us of) a workbook for this seat's conversation. */
  has: boolean
  convKey: string | null
  conv: ConvState | undefined
}

const NONE: SeatWorkbook = { has: false, convKey: null, conv: undefined }

export function useSeatWorkbook(hostId: string, sessionId: string): SeatWorkbook {
  const v1 = useWorkbookStore((s) => hostId !== '' && s.support[hostId]?.v1 === true)
  const convKey = useWorkbookStore((s) => (hostId === '' ? undefined : s.convOfSession[hostId]?.[sessionId]))
  const conv = useWorkbookStore((s) => (convKey === undefined ? undefined : selectConv(s, hostId, convKey)))
  if (!v1 || convKey === undefined || conv === undefined || conv.missing) return NONE
  return { has: true, convKey, conv }
}

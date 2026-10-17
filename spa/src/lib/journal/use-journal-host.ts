// spa/src/lib/journal/use-journal-host.ts — "this host shows the journal, not the workbook view" (J-2b). The host's STICKY journal.v1
// answer: the last yes / no, kept through a disconnect so a journal host never flashes back to the workbook view; a host that never
// answered (or '' = a seat this App has no host for) uses the old UI. The workbook view's close points all ask this one question.
import { useJournalStore } from '../../stores/useJournalStore'

export const useIsJournalHost = (hostId: string): boolean => useJournalStore((s) => hostId !== '' && s.sticky[hostId] === true)

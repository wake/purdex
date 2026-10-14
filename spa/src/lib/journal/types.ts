// spa/src/lib/journal/types.ts — the workbook journal wire shapes (daemon plan §8, J-1e) in ONE place, so that matching
// the real J-1e answer later means editing this file and parse.ts only. J-1e is not built yet: every shape below is the
// contract as the plans state it, and the fixtures in the tests are written from it.
export const JOURNAL_CAPABILITY = 'journal.v1'

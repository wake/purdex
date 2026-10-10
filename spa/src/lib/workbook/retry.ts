// spa/src/lib/workbook/retry.ts — the one bounded backoff the workbook's first loads share (the tab's own conversation in
// own-workbook.ts, the roster seats in workbook-loader.ts): a failed ask (network / 5xx) is retried after 1s, 2s, 4s, at most
// 3 times; a 404 or a success is an answer and is never retried.
export const WORKBOOK_MAX_RETRIES = 3
export const WORKBOOK_RETRY_BASE_MS = 1000

/** The wait before retry number `tries` (0-based): 1s, 2s, 4s. */
export const workbookRetryDelay = (tries: number): number => WORKBOOK_RETRY_BASE_MS * 2 ** tries

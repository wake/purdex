// spa/src/lib/workbook/fixtures.ts — wire-shaped entries for tests.
export const wireEntry = (over: Record<string, unknown> = {}) => ({
  id: 7, conv_key: 'c1', session_id: 's1', turn_id: 't1', turn_at: 1_760_000_000_000, state: 'ok', reason: '',
  thing: 'fix', push: 'p', entry: 'e', thing_done: false, created_at: 1_760_000_000_100, updated_at: 1_760_000_000_200, ...over,
})

/** A v2 todo as the daemon sends it. */
export const wireTodo = (over: Record<string, unknown> = {}) => ({
  id: 1, title: 't', detail: 'd', state: 'open', closed_by: '', created_at: 1_760_000_000_000, closed_at: 0, added_entry_id: 3, closed_entry_id: 0, ...over,
})

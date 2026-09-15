// spa/src/lib/host-config-queue.ts — one save at a time per host config
// collection.
//
// Every write to a host's config is a compare-and-set against a revision the
// cache holds, so two writes to the same collection in flight together have a
// loser by construction: its PUT is refused, the 409 reload replaces the local
// copy, and whatever the user asked for in that action is gone. The UI cannot
// prevent the overlap with a React flag — a second click in the same tick
// reads the pre-render value — so the ordering lives here instead, as a plain
// promise chain in module scope.
//
// The queue is not a lock the caller can forget to release: a task is enqueued
// and runs when its turn comes, whether the one before it resolved or threw.

/** Chain head per key; dropped once nothing is waiting on it. */
const queues = new Map<string, Promise<unknown>>()

/** A collection's queue key. `collection` is the daemon's name for it. */
export function hostConfigQueueKey(hostId: string, collection: string): string {
  return `${hostId}:${collection}`
}

/**
 * Run `task` after everything already queued under `key`, and resolve with its
 * result. A task that throws still lets the next one run.
 */
export function queueHostConfigSave<T>(key: string, task: () => Promise<T>): Promise<T> {
  const chained = (queues.get(key) ?? Promise.resolve()).then(task, task)
  queues.set(key, chained)
  void chained.catch(() => {}).then(() => { if (queues.get(key) === chained) queues.delete(key) })
  return chained
}

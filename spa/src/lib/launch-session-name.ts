// The session name a project launch uses when the user typed none
// (host-launcher spec §5.2): `{slug}-{N}`.

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * N = 1 + the number of live sessions named exactly `slug` or `slug-<digits>`;
 * if that name is taken, N increments until free. After a 409 the launch helper
 * calls again with the refused name appended to `liveNames`, so the answer
 * strictly advances instead of recomputing the name the daemon just refused.
 */
export function nextProjectSessionName(slug: string, liveNames: readonly string[]): string {
  const own = new RegExp(`^${escapeRegExp(slug)}(-\\d+)?$`)
  const taken = new Set(liveNames)
  let n = 1 + liveNames.filter((name) => own.test(name)).length
  while (taken.has(`${slug}-${n}`)) n++
  return `${slug}-${n}`
}

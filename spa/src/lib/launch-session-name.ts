// The session name a project launch uses when the user typed none
// (host-launcher spec §5.2): `{slug}-{N}`.

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * N = 1 + the number of live sessions named exactly `slug` or `slug-<digits>`;
 * if that name is taken, N increments until free. `bump` is added to the
 * starting N — the launch helper passes the retry count after a 409, when the
 * daemon knew a session the cached list did not.
 */
export function nextProjectSessionName(slug: string, liveNames: readonly string[], bump = 0): string {
  const own = new RegExp(`^${escapeRegExp(slug)}(-\\d+)?$`)
  const taken = new Set(liveNames)
  let n = 1 + liveNames.filter((name) => own.test(name)).length + bump
  while (taken.has(`${slug}-${n}`)) n++
  return `${slug}-${n}`
}

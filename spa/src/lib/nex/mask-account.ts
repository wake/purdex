// spa/src/lib/nex/mask-account.ts — the quota row names the account the host
// is logged in as (#1264). A worker pane ends up in screenshots, so the
// address is masked to its first two characters plus the domain; the full
// value stays reachable through the element's `title`.
const MASK = '…'

export function maskAccount(account: string): string {
  const at = account.lastIndexOf('@')
  if (at <= 0) return account.length <= 4 ? account : account.slice(0, 2) + MASK
  const local = account.slice(0, at)
  const domain = account.slice(at)
  return (local.length <= 2 ? local : local.slice(0, 2) + MASK) + domain
}

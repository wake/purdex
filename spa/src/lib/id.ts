export function generateId(): string {
  const chars = '0123456789abcdefghijklmnopqrstuvwxyz'
  let id = ''
  while (id.length < 6) {
    const [b] = crypto.getRandomValues(new Uint8Array(1))
    if (b < 252) id += chars[b % 36] // rejection sampling: 252 = 36*7
  }
  return id
}

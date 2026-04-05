const BASE58_ALPHABET = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
const BASE58_MAP = new Map<string, bigint>()
for (let i = 0; i < BASE58_ALPHABET.length; i++) {
  BASE58_MAP.set(BASE58_ALPHABET[i], BigInt(i))
}

export interface PairingCodeData {
  ip: string
  port: number
  secret: string // hex
}

/** Strip dashes, slashes, spaces, tabs from pairing code input. */
export function cleanPairingInput(input: string): string {
  return input.replace(/[-/\s]/g, '')
}


/**
 * Decode a 13-char pairing code into IP, port, and secret.
 * Returns null if the code is invalid.
 * The Go encoder always produces exactly 13 Base58 chars for a 9-byte payload.
 *
 * NOTE: We decode to bigint directly and use fixed 9-byte output, NOT the
 * leading-'1'-as-zero-bytes semantics of base58Decode. The Go encoder pads
 * with '1' characters, which are NOT real zero bytes — they are padding.
 * For small IPs like 10.x.x.x, misinterpreting them would create extra bytes.
 */
export function decodePairingCode(input: string): PairingCodeData | null {
  const cleaned = cleanPairingInput(input)
  if (cleaned.length !== 13) return null

  // Decode to bigint directly — do NOT use base58Decode's leading-1 semantics,
  // because the Go encoder pads with '1' and those are not real zero bytes.
  let n = 0n
  for (const c of cleaned) {
    const val = BASE58_MAP.get(c)
    if (val === undefined) return null
    n = n * 58n + val
  }

  // Check overflow (9 bytes = 72 bits)
  if (n >= 1n << 72n) return null

  // Fixed 9-byte output
  const data = new Uint8Array(9)
  for (let i = 8; i >= 0; i--) {
    data[i] = Number(n & 0xFFn)
    n >>= 8n
  }

  const ip = `${data[0]}.${data[1]}.${data[2]}.${data[3]}`
  const port = (data[4] << 8) | data[5]
  const secret = Array.from(data.slice(6, 9))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')

  return { ip, port, secret }
}

/**
 * Generate a purdex_ token: prefix + 40 hex chars (160-bit entropy).
 * Uses crypto.getRandomValues for cryptographic security.
 */
export function generatePurdexToken(): string {
  const bytes = new Uint8Array(20)
  crypto.getRandomValues(bytes)
  const hex = Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
  return `purdex_${hex}`
}

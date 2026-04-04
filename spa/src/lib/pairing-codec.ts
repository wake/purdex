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

/** Decode a Base58 string to bytes. Returns null on invalid input. */
function base58Decode(s: string): Uint8Array | null {
  let n = 0n
  for (const c of s) {
    const val = BASE58_MAP.get(c)
    if (val === undefined) return null
    n = n * 58n + val
  }

  // Convert bigint to bytes (ensure even-length hex)
  const rawHex = n.toString(16)
  const hex = rawHex.length % 2 ? '0' + rawHex : rawHex
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  }

  // Count leading '1's → leading zero bytes
  let leadingZeros = 0
  for (const c of s) {
    if (c !== '1') break
    leadingZeros++
  }

  if (leadingZeros > 0) {
    const padded = new Uint8Array(leadingZeros + bytes.length)
    padded.set(bytes, leadingZeros)
    return padded
  }

  return bytes
}

/**
 * Decode a 13-char pairing code into IP, port, and secret.
 * Returns null if the code is invalid.
 * The Go encoder always produces exactly 13 Base58 chars for a 9-byte payload.
 */
export function decodePairingCode(input: string): PairingCodeData | null {
  const cleaned = cleanPairingInput(input)
  if (cleaned.length !== 13) return null

  const decoded = base58Decode(cleaned)
  if (!decoded) return null

  // Pad to 9 bytes if shorter
  let data = decoded
  if (data.length < 9) {
    const padded = new Uint8Array(9)
    padded.set(data, 9 - data.length)
    data = padded
  }
  if (data.length !== 9) return null

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

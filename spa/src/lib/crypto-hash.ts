/**
 * crypto-hash — browser SHA-256 over raw bytes, returned as 64-char lowercase
 * hex. This is the content hash the Storage backup engine uses to address blobs;
 * it MUST match the daemon's `sha256(content)` (lowercase hex) so a blob the
 * client uploads under `{hash}` round-trips byte-identically (spec §4.1/§4.2).
 * Profile Sync (`lib/profile/hash.ts`, `projections.ts`) hashes sections and
 * fingerprints through the same function.
 *
 * Two paths, one output:
 *
 *  1. WebCrypto (`crypto.subtle.digest`) when it is available.
 *  2. A pure-JS FIPS 180-4 SHA-256 (`sha256HexSync`) otherwise.
 *
 * Why the fallback exists: `crypto.subtle` is only defined in a SECURE CONTEXT
 * (https, localhost, or a registered scheme such as `app://`). Purdex's dev mode
 * loads the SPA from `http://100.64.0.2:5174` — a tailnet IP over plain HTTP —
 * which is NOT a secure context, so `crypto.subtle` is `undefined` there and the
 * old code died with "Cannot read properties of undefined (reading 'digest')".
 * That silently disabled every hash consumer (Profile Sync collector, device
 * state uploader) in exactly the environment the app is used in day to day.
 * Unit tests could not see it: jsdom/node always expose `globalThis.crypto.subtle`.
 *
 * INVARIANT: both paths must produce byte-identical output for every input.
 * Two clients may hash the same content through different paths (one on
 * `app://`, one on plain HTTP); if the digests ever differed they would disagree
 * about identical content forever and never converge. The test suite pins the
 * pure-JS path to the NIST vectors and to WebCrypto across padding boundaries
 * and seeded random inputs — keep that coverage if this file changes.
 */
export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const subtle = globalThis.crypto?.subtle
  if (typeof subtle?.digest === 'function') {
    try {
      // `digest` accepts a BufferSource; pass the exact byte view (offset/length
      // honoured) rather than the backing ArrayBuffer.
      const digest = await subtle.digest('SHA-256', bytes as unknown as ArrayBuffer)
      return toHex(new Uint8Array(digest))
    } catch {
      // Present but unusable (restricted environments reject/throw) — the
      // pure-JS path yields the same digest, so fall through.
    }
  }
  return sha256HexSync(bytes)
}

function toHex(view: Uint8Array): string {
  let hex = ''
  for (let i = 0; i < view.length; i++) {
    hex += view[i].toString(16).padStart(2, '0')
  }
  return hex
}

// FIPS 180-4 §4.2.2 — first 32 bits of the fractional parts of the cube roots
// of the first 64 primes.
const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

/**
 * Compress one 64-byte block of `src` starting at `off` into the state `h`.
 *
 * Every word is kept as an unsigned 32-bit number via `>>> 0`. The ones on the
 * state update (`h[i] = (h[i] + x) >>> 0`) are load-bearing — without them the
 * state outgrows 32 bits and the hex output is wrong. The others are
 * mathematically redundant (sums of a few 32-bit values stay exact in a double
 * and the next bitwise op truncates anyway) but keep the "all words are uint32"
 * invariant local instead of relying on that argument.
 */
function compress(h: number[], w: number[], src: Uint8Array, off: number): void {
  for (let t = 0; t < 16; t++) {
    const p = off + t * 4
    w[t] = ((src[p] << 24) | (src[p + 1] << 16) | (src[p + 2] << 8) | src[p + 3]) >>> 0
  }
  for (let t = 16; t < 64; t++) {
    const x = w[t - 15]
    const y = w[t - 2]
    const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3)
    const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10)
    w[t] = (w[t - 16] + s0 + w[t - 7] + s1) >>> 0
  }

  let a = h[0]
  let b = h[1]
  let c = h[2]
  let d = h[3]
  let e = h[4]
  let f = h[5]
  let g = h[6]
  let hh = h[7]

  for (let t = 0; t < 64; t++) {
    const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7))
    const ch = (e & f) ^ (~e & g)
    const t1 = (hh + S1 + ch + K[t] + w[t]) >>> 0
    const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10))
    const maj = (a & b) ^ (a & c) ^ (b & c)
    const t2 = (S0 + maj) >>> 0
    hh = g
    g = f
    f = e
    e = (d + t1) >>> 0
    d = c
    c = b
    b = a
    a = (t1 + t2) >>> 0
  }

  h[0] = (h[0] + a) >>> 0
  h[1] = (h[1] + b) >>> 0
  h[2] = (h[2] + c) >>> 0
  h[3] = (h[3] + d) >>> 0
  h[4] = (h[4] + e) >>> 0
  h[5] = (h[5] + f) >>> 0
  h[6] = (h[6] + g) >>> 0
  h[7] = (h[7] + hh) >>> 0
}

/**
 * Pure-JS SHA-256 (FIPS 180-4), synchronous. Byte-identical to
 * `crypto.subtle.digest('SHA-256', …)` — see the invariant in the file header.
 * Honours the view's offset/length; never touches the backing buffer outside it.
 */
export function sha256HexSync(bytes: Uint8Array): string {
  // FIPS 180-4 §5.3.3 — first 32 bits of the fractional parts of the square
  // roots of the first 8 primes.
  const h = [
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]
  const w = new Array<number>(64).fill(0)
  const len = bytes.length

  // Whole blocks straight from the input — no copy.
  const fullEnd = len - (len % 64)
  for (let off = 0; off < fullEnd; off += 64) compress(h, w, bytes, off)

  // Tail: remaining bytes + 0x80 + zero pad + 64-bit big-endian BIT length.
  // The 8-byte length needs room after the 0x80, so a remainder of 56..63 bytes
  // spills into a second block.
  const rem = len - fullEnd
  const tail = new Uint8Array(rem < 56 ? 64 : 128)
  tail.set(bytes.subarray(fullEnd, len))
  tail[rem] = 0x80
  // Bit length = len * 8, as two 32-bit words. `len << 3` alone would drop the
  // top bits once len ≥ 2^29 (512 MiB), so the high word is computed by
  // division: floor(len * 8 / 2^32) = floor(len / 2^29).
  const hi = Math.floor(len / 0x20000000) >>> 0
  const lo = (len << 3) >>> 0
  const end = tail.length
  tail[end - 8] = hi >>> 24
  tail[end - 7] = hi >>> 16
  tail[end - 6] = hi >>> 8
  tail[end - 5] = hi
  tail[end - 4] = lo >>> 24
  tail[end - 3] = lo >>> 16
  tail[end - 2] = lo >>> 8
  tail[end - 1] = lo
  for (let off = 0; off < end; off += 64) compress(h, w, tail, off)

  let hex = ''
  for (let i = 0; i < 8; i++) hex += h[i].toString(16).padStart(8, '0')
  return hex
}

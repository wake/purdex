/**
 * crypto-hash — browser SHA-256 over raw bytes, returned as 64-char lowercase
 * hex. This is the content hash the Storage backup engine uses to address blobs;
 * it MUST match the daemon's `sha256(content)` (lowercase hex) so a blob the
 * client uploads under `{hash}` round-trips byte-identically (spec §4.1/§4.2).
 *
 * Uses WebCrypto (`crypto.subtle.digest`) — available in the browser and in the
 * Electron renderer; tests run under jsdom/node which expose `globalThis.crypto`.
 */
export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  // `digest` accepts a BufferSource; pass the exact byte view (offset/length
  // honoured) rather than the backing ArrayBuffer.
  const digest = await crypto.subtle.digest('SHA-256', bytes as unknown as ArrayBuffer)
  const view = new Uint8Array(digest)
  let hex = ''
  for (let i = 0; i < view.length; i++) {
    hex += view[i].toString(16).padStart(2, '0')
  }
  return hex
}

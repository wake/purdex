// spa/src/lib/upload-failure.ts — the one wording for a failed agent upload, shared by the deck / chat input and the
// terminal's status bar (#2501). `err` is an `AgentUploadError` (its `kind`) or any other failure (a plain HTTP-style one).
export interface UploadFailureText {
  key: string
  params?: Record<string, string | number>
}

export function uploadFailureText(err: unknown, name: string): UploadFailureText {
  const e = err as { kind?: string; status?: number } | null | undefined
  switch (e?.kind) {
    case 'too_large': return { key: 'deck.attach.too_large', params: { name } }
    case 'too_many': return { key: 'deck.attach.too_many' }
    case 'not_found': return { key: 'deck.attach.not_found' }
    case 'network': return { key: 'deck.attach.network', params: { name } }
    default: return { key: 'deck.attach.http', params: { name, status: e?.status || '-' } }
  }
}
